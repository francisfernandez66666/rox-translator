// ============================================================================
// store/billing_platform_cost.go — 平台承担成本核算（★ 2026-09-29 〇-AD）
//
// 为什么单独成文件：AGENTS.md §一·1 冻结 billing.go（只减不增，不许追加新方法），
// 本批的「实扣/留痕分桶」是纯读侧聚合，与被冻结的写点无关。
//
// 口径（一句话）：**租户真正掉积分的行 = charge_kind 为历史空串或 'charge'**；
// 其余 charge_kind='log'（留痕计量）与 'settle'（欠费清零调整）都不再进
// 个人/组织用量看板，'log' 部分单独作为「平台承担成本」只在超管侧出栈。
//
// 为什么空串也算实扣：charge_kind 列是 P1-2（2026-09-14）才补的，历史实扣行该列为空串；
// 只认 'charge' 会把 09-14 之前的真实消费整段抹成 0（现网「本层累计」会凭空归零）。
// 这一口径不是新造的——退款消耗核算（billing.go 的 consumed 那条）早就在用同一个谓词常量，
// 本批只是把这把尺子接到用量看板上。
// 此前看板把留痕行和实扣行混在一个 SUM 里，于是现网「系统/后台任务」一行
// 337,819 积分压过全部真实用户之和（≈725）——客户看到的是「消耗」，实际大部分一分没扣。
// ============================================================================
package store

import (
	"translator/internal/db"
)

// RealDebitPred 实扣谓词（★ 单一事实源：所有「客户消耗」读法都必须引用它）。
// 判据本身由 billing_platform_cost_test.go 的等值锁钉住（含反向锁：不许出现
// 只认 'charge' 的写法，那会把 09-14 之前的历史实扣整段抹成 0）。
//
// ★ 2026-09-29 〇-AD 补丁二：导出给 api 层引用。原因是「本月已用／今日已用」这类
// 客户面数字是 api 里的内联 SQL（plans_api.go），写第二份字面量迟早和 store 侧分叉
// ——本批要收口的正是「同一个消耗量在五六个读腿上各数各的账」这个形态。
const RealDebitPred = "charge_kind IN ('','charge')"

// realDebitPred 包内引用名（〇-AD 首批的 8 条读腿已挂在它上面，保持不变）。
const realDebitPred = RealDebitPred

// PlatformTaskTypeExclPred 「不是平台自己垫的那类用量」的 SQL 谓词（★ 〇-AD 补丁二）。
//
// 为什么还要第二把尺子（这点必须看清，否则会把另一批人误伤）：
//   - 展示侧（客户「我掉了多少积分」）认的是 **是否实扣**＝RealDebitPred；
//   - 额度侧（日计数器 usage_daily、部门/组织月度预算墙、收银台「今日/本月已用」）
//     认的是 **这是不是客户自己下单产生的量**。两者不等价：
//     未开强制计费的租户（演示/历史形态）全部流水都是 charge_kind='log'，
//     把 RealDebitPred 套到额度腿上，会把这类租户的日额/部门预算判成「一点没用」，
//     预算墙从此形同虚设——那是拿「防超支」换「防误扣」，方向反了。
//   - 平台承担的行有一个额度侧也认得的特征：task_type 是下面这几个用途标签之一
//     （写点即 llm.WithPlatformCost 的 reason，见 internal/llm/platform_cost.go）。
//
// ⚠️ 这份名单与 llm.Platform* 常量必须逐字同步，跨包等值锁在
//
//	internal/api/platform_cost_charge_test.go 的 TestPlatformCost08AD2_LabelListsAgree
//	（改名式破坏——把标签改了名而谓词没跟上——必须当场红，否则平台成本会悄悄回到客户额度里）。
const PlatformTaskTypeExclPred = "task_type NOT IN ('kb_embed','system_task','pack_scrape','evals')"

// isPlatformTaskType Go 侧同款判定（写点用它决定是否累加日计数器，与上面谓词一个名单）。
func isPlatformTaskType(taskType string) bool {
	switch taskType {
	case "kb_embed", "system_task", "pack_scrape", "evals":
		return true
	}
	return false
}

// PlatformCostBreakdown 平台承担成本出栈结构（★ 仅超管可见，见 api.handleUsageCost）。
type PlatformCostBreakdown struct {
	Total    int64            `json:"total"`     // 平台承担合计（token 口径；出栈前由 api 层折积分）
	Settled  int64            `json:"settled"`   // 欠费清零调整（charge_kind='settle'）＝坏账，另一档口径，绝不混进 Total
	ByReason map[string]int64 `json:"by_reason"` // 按用途标签（usage_ledger.task_type）拆分
	ByModel  map[string]int64 `json:"by_model"`  // 按「供应商 / 模型」拆分（与 costs 同键形，便于并排读）
}

// PlatformCostSummary 汇总「平台承担」的模型成本：charge_kind='log' 的全部留痕行
// （知识库 Embedding、行业包采集、Judge 抽样、推广期免费、欠费批次留痕），
// 另把 charge_kind='settle' 的欠费清零量单独回吐到 Settled。
// 参数：from/to=日期区间（YYYY-MM-DD；均空=全部时间，仅给一端视同单日）。
//
// ⚠️ 一条必须知道的读法细节：留痕行的 cost 由 pricingCost(task_type,…) 决定，
//
//	rate_card 未为该 task_type 配行时回退 (1, 1.0)（与 'translate' 同价，故留痕金额
//	等于「本该收客户多少」）；若运维为某标签配了非 1/1.0 的行（如 'evals' 现为 ×0.5），
//	该标签的平台承担账按配的那档折算，不是全额。
func (s *Store) PlatformCostSummary(from, to string) (*PlatformCostBreakdown, error) {
	out := &PlatformCostBreakdown{ByReason: map[string]int64{}, ByModel: map[string]int64{}}
	// 一条 GROUP BY 同时喂两只拆分（按用途 / 按模型），不在 Go 里再拼两次查询
	q := `SELECT COALESCE(NULLIF(task_type,''),'unset'), COALESCE(NULLIF(provider,''),'global'),
	        COALESCE(NULLIF(model,''),'?'), charge_kind,
	        COALESCE(SUM(cost),0), COALESCE(SUM(quantity),0), COUNT(*)
	      FROM usage_ledger WHERE charge_kind IN ('log','settle')`
	args := []interface{}{}
	if pred, cargs := usageDatePred(from, to); cargs != nil {
		q += " AND created_at " + pred
		args = append(args, cargs...)
	}
	q += " GROUP BY task_type, provider, model, charge_kind"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var reason, provider, model, kind string
		var cost, quantity, count int64
		if err := rows.Scan(&reason, &provider, &model, &kind, &cost, &quantity, &count); err != nil {
			continue
		}
		// quantity/count 只用于自检（留痕行必须有量才进得来这一档），出栈口径按 cost
		_, _ = quantity, count
		if kind == "settle" {
			// 坏账（欠费清零调整）与政策免费是两件事，绝不合并进 Total
			out.Settled += cost
			continue
		}
		out.Total += cost
		out.ByReason[reason] += cost
		out.ByModel[provider+" / "+model] += cost
	}
	return out, nil
}
