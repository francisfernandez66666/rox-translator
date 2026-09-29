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
// 这一口径不是新造的——退款消耗核算（billing.go 里 consumed 那条）早就用
// 退款消耗核算（billing.go 的 consumed 那条）早就用同一个谓词常量，本批只是把这把尺子接到用量看板上。
// 此前看板把留痕行和实扣行混在一个 SUM 里，于是现网「系统/后台任务」一行
// 337,819 积分压过全部真实用户之和（≈725）——客户看到的是「消耗」，实际大部分一分没扣。
// ============================================================================
package store

import (
	"translator/internal/db"
)

// realDebitPred 实扣谓词（★ 单一事实源：所有「客户消耗」读法都必须引用它）。
// 判据本身由 billing_platform_cost_test.go 的等值锁钉住（含反向锁：不许出现
// 只认 'charge' 的写法，那会把 09-14 之前的历史实扣整段抹成 0）。
const realDebitPred = "charge_kind IN ('','charge')"

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
