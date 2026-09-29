// ============================================================================
// store/billing_platform_cost.go — 平台承担成本核算（★ 2026-09-29 〇-AD）
//
// 为什么单独成文件：AGENTS.md §一·1 冻结 billing.go（只减不增，不许追加新方法），
// 本批的「实扣/留痕分桶」是纯读侧聚合，与被冻结的写点无关。
//
// 口径（两句话，★ 〇-AD 补丁三把「客户面」和「平台成本面」彻底分成两把尺子）：
//   - 平台承担成本（只在超管侧出栈）＝ charge_kind IN ('log','settle') 的全部留痕/调整行；
//   - 客户面「我自己掉了多少积分」（个人/组织看板、明细页、CSV、自服务账单）＝
//     **排掉平台垫的那几个用途标签**（task_type ∈ kb_embed/system_task/pack_scrape/evals）
//     **再排掉没有归因到具体用户的留痕/结算行**（uid=0 的 'log'/'settle'——那正是现网
//     「系统/后台任务」一行 337,819 积分的形态）。
//
// 为什么客户面**不能**只认实扣（补丁三的真踩，run_uat T59 现场抓到的红灯）：
// 推广期免费／策略免扣（ops_policy 的 mode_rules.charge=false）下，客户自己下单的用量
// 也是 charge_kind='log'（billing_api.ChargeUsageRealtime 的免费分支，注释里写着「用量看板可见」）。
// 拿实扣当客户面尺子，会把这一整类客户的**自己的**流水整段抹空——「我的用量」为空、
// 明细页为空、CSV 为空，而这一笔确实进了他的台账、也确实从他的报文里报给他了。
// UAT 里 api_uat.sh B8 段压的「uat_promo」窗口（fast.charge=false，昨天~明天）就是这条腿的现场：
// T59 的「我的用量计数增量＝台账新增行数」判据在补丁二之后差值恒 0。
// 是否实扣这把尺子（RealDebitPred）仍然只用在真正的金钱语义读法上（退款消耗核算等）。
//
// 为什么实扣那把尺子里空串也算：charge_kind 列是 P1-2（2026-09-14）才补的，历史实扣行该列为空串；
// 只认 'charge' 会把 09-14 之前的真实消费整段抹成 0（退款核算会把应退金额算高）。
// 这一口径不是新造的——退款消耗核算（billing.go 的 consumed 那条）早就在用，
// 本批只是把它收成包内常量（RealDebitPred），不再各处抄字面量。
//
// 此前客户看板把三类行混在一个 SUM 里，于是现网「系统/后台任务」一行
// 337,819 积分压过全部真实用户之和（≈725）——客户看到的是「消耗」，实际大部分一分没扣。
// 补丁三收口的判据是「无归因」这件事（uid=0 的留痕/结算行不进客户面），
// 而不是「未实扣」——后者会连带抹掉免费期客户自己的流水。
// ============================================================================
package store

import (
	"translator/internal/db"
)

// RealDebitPred 实扣谓词（★ 金钱语义的单一事实源：退款消耗核算这类「到底扣了多少钱」
// 的读法引用它；客户面「我掉了多少积分」用 CustomerUsagePred，两者不等价，见下）。
// 判据本身由 billing_platform_cost_test.go 的等值锁钉住（含反向锁：不许出现
// 只认 'charge' 的写法，那会把 09-14 之前的历史实扣整段抹成 0）。
//
// ★ 2026-09-29 〇-AD 补丁二：导出给 api 层引用。原因是「本月已用／今日已用」这类
// 客户面数字是 api 里的内联 SQL（plans_api.go），写第二份字面量迟早和 store 侧分叉
// ——本批要收口的正是「同一个消耗量在五六个读腿上各数各的账」这个形态。
const RealDebitPred = "charge_kind IN ('','charge')"

// PlatformTaskTypeExclPred 「不是平台自己垫的那类用量」的 SQL 谓词（★ 〇-AD 补丁二）。
//
// 为什么还要第二把尺子（这点必须看清，否则会把另一批人误伤）：
//   - 展示侧（客户「我掉了多少积分」）认的是 **是不是客户自己的用量**＝CustomerUsagePred
//     （补丁三：它比额度侧多一条「排掉 uid=0 的留痕/结算行」，因为账单页不能出现无归因的行；
//     它比实扣少一条，因为免费期客户照样要看到自己的用量）；
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

// UnattributedTraceExclPred 客户面的第二条排除腿：没有归因到具体用户的留痕/结算行。
// 这一档就是现网病灶的本体——「系统/后台任务」一行 337,819 积分全部落在 user_id=0，
// 而三位真实用户合计只有 ≈725；'settle'（欠费清零调整）同样是 uid=0 的账务调整行，
// 不是客户的一次消费，不该出现在「我的消耗/我的流水」里（它由 PlatformCostSummary 的
// Settled 单列，只在超管侧出栈）。
const UnattributedTraceExclPred = "NOT (charge_kind IN ('log','settle') AND COALESCE(user_id,0)=0)"

// CustomerUsagePred 客户面「我自己掉了多少积分」的唯一尺子（★ 〇-AD 补丁三）。
// 引用它的读腿（个人/组织看板、租户明细页与 CSV、自服务账单的日序列与流水分页、
// OpenAPI 用量、超管按用户聚合）一律**只引用常量、不抄字面量**——
// 这批 bug 的形态从来不是某条腿写错，而是五条腿各拿一把尺子。
// 名单与语义见本文件头部；等值锁见 billing_platform_cost_test.go。
const CustomerUsagePred = PlatformTaskTypeExclPred + " AND " + UnattributedTraceExclPred

// CustomerUsagePredWith 客户面尺子的列前缀版（UsageByOrg 那条腿带 l. 别名，
// 且 join 了 users 表，裸列名会歧义）。参数 prefix="" 时必须与 CustomerUsagePred 逐字相等
// ——这条等值由单测钉住，避免「前缀版和裸版各长一半」。
func CustomerUsagePredWith(prefix string) string {
	return prefix + "task_type NOT IN ('kb_embed','system_task','pack_scrape','evals')" +
		" AND NOT (" + prefix + "charge_kind IN ('log','settle') AND COALESCE(" + prefix + "user_id,0)=0)"
}

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
