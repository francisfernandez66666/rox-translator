// ============ admin_reconcile.go · 职责说明 ============
// 超级管理员对账视图 API（★ F9）+ 每日定时对账（★ #39 2026-09-21：对账不再只靠人点）。
// 规则集抽成纯函数 reconcileRules（handler 与 watchdog 共用一份口径，杜绝两套实现漂移）：
//
//	missing_payment    订单已支付但无 paid 流水（收款缺账）
//	duplicate_payment  同一订单有多条 paid 流水（重复支付/回调重投）——★ #39 补齐：
//	                   旧实现逐笔核对金额，两条等额 paid 各自都「对得上」从而整体漏检
//	status_mismatch    流水 paid/refunded 与订单状态矛盾
//	fen_mismatch       流水金额（分）与应收额偏差 >1 分
//	refund_no_flow     订单已退款但无 refunded 冲正流水（A3 前历史数据可能误报，detail 已注）
//	orphan_payment     流水指向窗口外/不存在订单（提示项，不计严重度）
//
// =============================================
package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"translator/internal/observability"
	"translator/internal/store"
)

// ReconIssue 一条勾稽异常。
type ReconIssue struct {
	OrderID   int64  `json:"order_id"`
	OrderNo   string `json:"order_no"`
	TenantID  int64  `json:"tenant_id"`
	Rule      string `json:"rule"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// handleAdminReconcile 对账报告（仅超管）。参数 days=回看窗口（默认 30，上限 365）。
func (s *Server) handleAdminReconcile(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil || u.Role != "super_admin" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超级管理员可对账"})
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	orders, pays, err := s.Store.ReconcileLoad(days)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	issues := s.reconcileRules(orders, pays)
	counts := map[string]int{}
	for _, x := range issues {
		counts[x.Rule]++
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "days": days, "orders_count": len(orders), "payments_count": len(pays),
		"counts": counts, "issues": issues,
	})
}

// reconcileRules 三表勾稽规则集（纯计算，不落库）：人工点「对账」与每日定时扫描共用。
// 参数 orders: 窗口内订单；pays: 窗口内流水。返回异常清单（含提示级 orphan_payment）。
func (s *Server) reconcileRules(orders []*store.Order, pays []store.PaymentRow) []ReconIssue {
	byOrder := map[int64][]int64{} // order_id → payments 下标
	ordByID := map[int64]*ReconOrderLite{}
	var issues []ReconIssue
	for i := range pays {
		byOrder[pays[i].OrderID] = append(byOrder[pays[i].OrderID], int64(i))
	}
	for _, o := range orders {
		ordByID[o.ID] = &ReconOrderLite{Status: o.Status, Tokens: o.AmountTokens}
	}
	// R1/R3/R4：逐订单
	// 逐订单两段式：①统计其 paid/refunded 流水数并核对每笔 paid 金额（分）；②按订单终态核对流水完整性（见下）
	for _, o := range orders {
		var paidRows, refundRows int
		for _, idx := range byOrder[o.ID] {
			p := pays[idx]
			switch p.Status {
			case "paid":
				paidRows++
				// ★ S1 口径修复：核对基准=订单应收金额（amount_money，下单落库的单一事实源）；
				//   历史未回填单退到尺子价 tokens×price_fen_per_million_tokens。
				want := int64(o.AmountMoney*100 + 0.5)
				if want <= 0 && o.AmountTokens > 0 {
					want = s.Store.TokensToFen(o.AmountTokens)
				}
				if want > 0 {
					diff := p.AmountFen - want
					if diff < 0 {
						diff = -diff
					}
					if diff > 1 {
						issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "fen_mismatch",
							fmt.Sprintf("fen=%d want=%d", p.AmountFen, want), o.CreatedAt})
					}
				}
			case "refunded":
				refundRows++
			}
		}
		// ★ #39（2026-09-21 评审缺陷）：多条 paid 流水 = 重复支付（回调重投/双渠道同时到账）。
		//   旧实现只逐笔比金额，两笔等额 paid 各自「通过」，资损项恒漏检。
		if paidRows > 1 {
			issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "duplicate_payment",
				fmt.Sprintf("同一订单有 %d 条 paid 流水，需核对是否重复收款并冲正", paidRows), o.CreatedAt})
		}
		// 终态核对：paid 须有 paid 流水且无退款行、refunded 须有冲正行、cancelled 不得残留支付行
		switch o.Status {
		case "paid":
			if paidRows == 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "missing_payment", "", o.CreatedAt})
			}
			if refundRows > 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "status_mismatch", "paid+refunded", o.CreatedAt})
			}
		case "refunded":
			if refundRows == 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "refund_no_flow", "A3 前历史退款可能无冲正行", o.CreatedAt})
			}
		case "cancelled":
			if paidRows > 0 {
				issues = append(issues, ReconIssue{o.ID, o.OrderNo, o.TenantID, "status_mismatch", "cancelled 但有支付流水", o.CreatedAt})
			}
		}
	}
	// R2：流水指向窗口外/不存在订单（孤儿流水提示，不计异常严重度）
	for _, p := range pays {
		if _, ok := ordByID[p.OrderID]; !ok {
			issues = append(issues, ReconIssue{p.OrderID, strconv.FormatInt(p.OrderID, 10), p.TenantID, "orphan_payment", p.Status, p.CreatedAt})
		}
	}
	return issues
}

// runReconcileScan ★ #39（2026-09-21）每日定时对账（由 watchdog 经 runExclusive 单跑者化调度）：
// 把原本「超管手点才算一次」的勾稽变成常态巡检，异常第一时间落 alerts + 告警邮件。
// 口径：
//   - 回看窗口 reconcile_scan_days（默认 30 天，1..365 夹取）；
//   - orphan_payment 属提示级（流水落在窗口边界本就找不到订单），不计入告警，避免天天误报；
//   - alerts 同 kind+tenant 的 open 记录由 store 侧幂等去重，持续未处理也只留一条。
func (s *Server) runReconcileScan() {
	if s.Store == nil {
		return
	}
	days := 30
	if v, _ := s.Store.GetConfig("reconcile_scan_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 365 {
			days = n
		}
	}
	orders, pays, err := s.Store.ReconcileLoad(days)
	if err != nil {
		observability.Error(context.Background(), "定时对账装载数据失败", "err", err.Error())
		return
	}
	issues := s.reconcileRules(orders, pays)
	counts := map[string]int{}
	hard := 0
	for _, x := range issues {
		if x.Rule == "orphan_payment" {
			continue
		}
		counts[x.Rule]++
		hard++
	}
	if hard == 0 {
		return
	}
	rules := make([]string, 0, len(counts))
	for k, v := range counts {
		rules = append(rules, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(rules)
	msg := fmt.Sprintf("近 %d 天订单↔流水勾稽异常 %d 条（订单 %d / 流水 %d）：%s。请到「管理台-对账」逐条核对，duplicate_payment 需优先处理（可能重复收款）。",
		days, hard, len(orders), len(pays), strings.Join(rules, ", "))
	observability.Error(context.Background(), "定时对账发现异常", "days", strconv.Itoa(days), "issues", strconv.Itoa(hard))
	_ = s.Store.CreateAlert(0, "warning", "reconcile_diff", msg)
	s.notifyAlert("对账异常告警", msg)
}

// ReconOrderLite 勾稽用订单摘要。
type ReconOrderLite struct {
	Status string
	Tokens int64
}
