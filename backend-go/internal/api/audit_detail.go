// ============================================================================
// api/audit_detail.go — 审计 detail 统一口径渲染（★ 2026-09-26 批 I-3·F-63）
// ----------------------------------------------------------------------------
// 为什么要有这个文件：本轮 E2E 实测发现同一批写动作里，审计 detail 有三类「记了等于没记」
// 的形态——资金动作留空串（order_pay 小票渠道 8 行全空）、退款只留固定串「权益已回收」
// （追不到退了哪一单、多少钱）、删除动作留空串（kb_entry_delete / org_delete / safety_delete）。
// 空 detail 的审计行只能证明「某人某点动作过一次」，证明不了「动了哪条数据、动了多少」，
// 而这几类恰恰是资金与权益变更的唯一轨迹来源（对账、纠纷、合规回查全靠它）。
// 历史上各处各写各的格式，导致同一动作在不同批次留下的轨迹不可比。本文件把口径一次定死：
//   - 计费类：订单 <单号>｜金额 <分>｜渠道 <渠道>［｜尾注］
//   - 删除类：删除 <资源类型> <数量> 条｜<被删对象标识>
//   - 配额类：走 LogAuditDiff 的字段旧→新（见 billing_api.go 的 tenant_quota_save）
//
// 金额为什么记「分」不记「元」：orders.amount_money 是 float64，元口径直写会同时出现
// "9.99"、"9.9900000000000002" 两种文本（AGENTS §一·7 记过同族坑：数值文本按字符串比对必红），
// ×100 四舍五入成整数分是唯一稳定形态，且与对账脚本的整数口径一致。
// ============================================================================
package api

import (
	"fmt"
	"math"
	"strings"

	"translator/internal/store"
)

// auditUnknown 空值在审计串里的统一占位（不用「无」，避免与留资文案 orNone 混义）。
const auditUnknown = "未知"

// moneyToFen 元 → 分（四舍五入取整），审计与对账的整数金额口径。
// 参数 yuan: 金额（元）。返回：金额（分）。
func moneyToFen(yuan float64) int64 { return int64(math.Round(yuan * 100)) }

// auditOrder 渲染计费类审计 detail：订单号＋金额分＋渠道（＋可选尾注）。
// 参数 o: 订单（可为 nil＝读取失败，此时退化为「单号未知」而不是返回空串）；
//
//	tail: 调用方补充的尾注（如链上 tx、实退金额），空串表示不追加。
//
// 返回: 供 LogAudit 直接使用的 detail 串。
func auditOrder(o *store.Order, tail string) string {
	if o == nil {
		// 订单没读到也要留痕：至少把尾注写进去，比空串更接近可追溯
		if tail == "" {
			return "订单 " + auditUnknown
		}
		return "订单 " + auditUnknown + "｜" + tail
	}
	no := strings.TrimSpace(o.OrderNo)
	if no == "" {
		no = auditUnknown
	}
	ch := strings.TrimSpace(o.Channel)
	if ch == "" {
		ch = auditUnknown
	}
	s := fmt.Sprintf("订单 %s｜金额 %d分｜渠道 %s", no, moneyToFen(o.AmountMoney), ch)
	if tail != "" {
		s += "｜" + tail
	}
	return s
}

// auditDelete 渲染删除类审计 detail：被删对象标识＋数量。
// 参数 kind: 资源类型中文名片段（如「术语条目」「组织节点」）；
//
//	label: 被删对象标识（名称/编码，空则回落到 id 串）；id: 被删对象主键；
//	n: 影响条数（连带删除的子行/词条数，1 表示仅本身）。
//
// 返回: detail 串。
func auditDelete(kind, label string, id int64, n int) string {
	if n < 1 {
		n = 1
	}
	l := strings.TrimSpace(label)
	if l == "" {
		l = "id=" + fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("删除 %s %d 条｜%s", kind, n, l)
}
