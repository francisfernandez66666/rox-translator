// ============ billing_wallet_pred.go · 职责说明 ============
// 免费档在「三把尺子」上的落点（★ 2026-10-30 三事合一批 组3 #9，D-3／W16／W17／W19）。
//
// 本批只新增一个用途标签 `instant_free`（即时翻译免费档的留痕行），但它落在**哪把尺子上**
// 必须逐条说清，因为三把尺子（AGENTS §一·11）各有射程，混一把就是把另一批人的账算错：
//
//	① **额度侧**（日计数器 usage_daily／部门与组织月度预算墙／收银台「今日已用」）：
//	   `instant_free` **不进**（D-3）。理由不是"免费所以不算"这么含糊——是**否则免费档自己会被自己拦死**：
//	   个人版日限额按 usage_daily 判，免费额度一天 300 积分折算下来很快就把日限额顶满，
//	   于是"免费的即时翻译"在第一天下午开始回 `达到今日用量上限`；共享租户里更糟，
//	   一个免费用户的用量会把同租户付费成员的日额一起吃掉。
//	   ⚠️ **绝不复用 `PlatformTaskTypeExclPred` 来实现这一条**：那个名单同时决定客户面可见性
//	   （CustomerUsagePred 引用它），把 instant_free 塞进去＝免费用量从客户账单里整段消失——
//	   客户看不到自己用了什么，我们自己也失去"免费档到底被用了多少"的唯一读数。
//	   所以单列一个名单：`DailyUsageExclTaskTypes`。
//
//	② **客户面**（我的用量／明细／CSV／自服务账单）：`instant_free` **仍在**射程内（W17）。
//	   它的形态是 `charge_kind='log'`（用了、没扣钱），与现有推广期免费同形；uid 有归因
//	   ⇒ `CustomerUsagePred` 天然包含它，本批**不改**那条谓词。
//
//	③ **金钱语义**（RealDebitPred，退款消耗核算）：不涉及——免费行本来就不是实扣。
//
// 另一个必须一起修的裂缝：`isPlatformTaskType`（Go 侧写点）与 `PlatformTaskTypeExclPred`（SQL 侧）
// 曾各抄一份名单，2026-10-30 加 `domain_detect` 时只改了 SQL 一侧 ⇒ 判域成本照旧被累进
// 客户的 usage_daily（客户一分没掉、日额却被平台自己烧掉）。现在 Go 侧从同一份切片派生，
// 等值由 `TestWalletPreds_GoAndSQLLabelListsAgree` 钉住。
// =============================================
package store

import "strings"

// TaskTypeInstantFree 即时翻译免费档的用途标签（对外读数：超管成本面按它单列一行）。
const TaskTypeInstantFree = "instant_free"

// DailyUsageExclTaskTypes **不累加进 usage_daily** 的用途标签（D-3）。
// 名单目前只有一项；之所以做成切片而不是 `taskType == "instant_free"` 的内联判据，
// 是为了让 W19 的三名单等值锁有个稳定的"名单"可以逐值点名（内联判据没有可点数的地方）。
var DailyUsageExclTaskTypes = []string{TaskTypeInstantFree}

// CustomerVisibleFreeTaskTypes 客户面**仍可见**的免费用途标签（W17 的正向对照）。
// 与上面那张表不是互补关系：一张管"计不计入日额"，一张管"客户账单里看不看得见"，
// 本批 `instant_free` **同时**属于这两张表——这恰恰是它容易被人"顺手合并"的原因。
var CustomerVisibleFreeTaskTypes = []string{TaskTypeInstantFree}

// platformTaskTypes 平台自己承担成本的用途标签（Go 侧唯一名单，SQL 谓词由它派生口径）。
// ⚠️ 与 llm.Platform* 常量逐字同步（跨包锁在 api/platform_cost_charge_test.go）；
// 本文件只负责让 Go 侧判定与 SQL 谓词**不再各抄一份**。
var platformTaskTypes = []string{"kb_embed", "system_task", "pack_scrape", "evals", "domain_detect"}

// isDailyUsageExcluded 判某用途标签是否不累加日计数器（LogUsage／LogUsageBatch 的写点用它）。
func isDailyUsageExcluded(taskType string) bool {
	for _, t := range DailyUsageExclTaskTypes {
		if t == taskType {
			return true
		}
	}
	return false
}

// countsTowardDailyUsage 日计数器的唯一判据：平台承担的、以及明确豁免的用途标签都不计入。
// 参数 taskType: usage_ledger.task_type 的字面。
func countsTowardDailyUsage(taskType string) bool {
	return !isPlatformTaskType(taskType) && !isDailyUsageExcluded(strings.TrimSpace(taskType))
}

// DailyUsageLedgerPred 日计数器的**SQL 形态**判据（两条排除腿：平台标签 ＋ 本批豁免标签）。
//
// 为什么必须有它：`DailyUsage` 与启动期回填（store.go）在当日 `usage_daily` 行缺失时会
// **回退去扫 usage_ledger**，那两条腿过去只拼了平台标签那一半 ⇒ 免费即时用量在那里又被算回
// 「今日已用」：写点明明不计入，读点却在没有当日行时把它算进来（W16 的读侧复现）。
// 一张表两处判据就是这样长出来的——名单只在这一处派生，两条读腿都引用本函数。
func DailyUsageLedgerPred() string {
	return PlatformTaskTypeExclPred + " AND task_type NOT IN ('" + strings.Join(DailyUsageExclTaskTypes, "','") + "')"
}
