// ============ wallet_tier.go · 职责说明 ============
// 积分钱包「消费顺序」的唯一写点（★ 2026-10-30 三事合一批 组2b／组3，用户逐条拍板）。
//
// 用户口径（两批原话，第二批是对第一批的细化，两条都生效）：
//
//	第一批（档位清单）：个人 ①每日免费 ②注册赠送 ③任务获得 ④充值月包 ⑤充值年包 ⑥充值长期；
//	                  租户 ①每日免费池 ②注册赠送池 ③任务获得 ④⑤⑥ 套餐里「该用户可用」的那部分。
//	第二批（同一次扣减内的判序，实现时我提出坏法、用户当场补的裁决）：
//	  ㋐ 分三桶——**免费赠送 > 收费有时效 > 收费无时效**；
//	  ㋑ **同一桶内「先到期」先于「档位号」**（档位号只作桶内次级判据与展示次序）。
//
// 为什么第二批必须存在（第一批单独成立的坏法）：
//
//	只按 ①~⑥ 的档位号排，会出现"明天就作废的任务奖励"排在"月底才到期的注册赠送"后面被跳过——
//	客户实际拿到的是**白送的、有期限的那一档自己过期作废**，而账面上什么都没坏（合计仍然对得上），
//	对账腿看不见、客户要等到额度消失了才发现。㋑ 把"先到期先花"提到桶内第一判据正是治这个；
//	而㋐ 的桶间次序仍保证：**收费的钱不会被拿去顶掉快过期的免费额度**，
//	也保证还有时效包可烧时不会先去烧不过期的长期包。
//
// 三桶划分（名单只在这里有一份）：
//
//	桶 1 免费赠送：free（每日免费，不落台账）／register／task
//	桶 2 收费有时效：plan_month／plan_year
//	桶 3 收费无时效：plan_permanent／legacy（历史永久余额与一切未知来源）
//
// 为什么必须收成一个文件（AGENTS §一·11 那一族的又一次复发）：
//
//	这条链此前只有**一把按到期日排序的尺子**（`quota_grants.go` 的 `ORDER BY expires_at ASC`），
//	它把「注册赠送／任务获得／月包／年包／长期」全部压成一个维度——到期日恰好是唯一秩序时看起来是对的，
//	一旦运营配出「同一天到期、但档位不同」的两行（长期包与月包同日到期是真会发生的），扣谁就没人在管。
//	所以：**次序只能来自 TierRank／TierBucketOf／TierOrderPred，任何调用方再写一次 ORDER BY 即红**（W2 按全集扫）。
//
// 三条设计纪律：
//
//	① **未知档位落桶 3、名次排最后**（legacy 那一桶），不许"未知＝第一"或"未知＝按到期日猜"——
//	   凭空造出来的优先级会在下一次加档时静默改变现有客户的扣法；
//	② SQL 片段由 `tierOrder`／`tierBucket` 这两张表**派生**，不在字符串里再抄一遍档位名，
//	   也不在 Go 侧写一份顺序、SQL 侧写另一份（两份迟早打架，同 §一·5「对外报价只有一个事实源」）；
//	③ 第 ① 档「每日免费」**不落 `quota_grants`**（它是一条 UTC 日计数器，见 `ratelimit_day.go`），
//	   所以 `TierFree` 有桶号与名次（用于展示与出栈排序），但 `TierOf` **永不**返回它——
//	   返回了就是"把一天的临时额度当成一条可核销的台账行"，退款会去回补一个根本不存在的桶。
//
// =============================================
package store

import (
	"fmt"
	"strings"
)

// 六个可核销档位 + 每日免费（不落台账）+ 未知兜底。
// ⚠️ 字符串值是**对外契约**：`/api/me/package` 的 `wallet[].tier`、`usage_debit_lines.tier`、
// 前端逐档面板与 12 语种词典都按这些字面取词。改名等于把客户历史明细那一列打成未知档。
const (
	TierFree          = "free"           // ① 每日免费（UTC 日计数器，不落 quota_grants）
	TierRegister      = "register"       // ② 注册赠送
	TierTask          = "task"           // ③ 任务获得
	TierPlanMonthly   = "plan_month"     // ④ 月包
	TierPlanYearly    = "plan_year"      // ⑤ 年包
	TierPlanPermanent = "plan_permanent" // ⑥ 长期包（D-6：expires_at 用 9999 哨兵，不是 NULL）
	TierLegacy        = "legacy"         // 兜底最后一桶：历史永久余额与一切未知来源
)

// 三个扣减桶（用户令㋐）。桶号就是跨桶次序。
const (
	TierBucketFreeGift = 1 // 免费赠送：每日免费／注册赠送／任务获得
	TierBucketPaidTemp = 2 // 收费有时效：月包／年包
	TierBucketPaidPerm = 3 // 收费无时效：长期包／永久余额兜底
)

// tierRankUnknown 是"未知档位"的名次——**必须排在已知档位之后**。
// 写成常量而不是 len(tierOrder)：将来加新档时这里跟着动，而不是悄悄把未知档挤到中间。
const tierRankUnknown = 99

// tierOrder 是台账里可能出现的档位全集（含每日免费那一档——它不落台账，但名次与桶要参与展示次序）。
// 这张表就是"档位号"本身：TierRank／TierBucketOf／TierOrderPred 都从它派生，改这里即改全站。
var tierOrder = []string{TierFree, TierRegister, TierTask, TierPlanMonthly, TierPlanYearly, TierPlanPermanent, TierLegacy}

// tierBucket 是每个档位落在哪个桶（用户令㋐的名单唯一一份）。
// ⚠️ 表里没有的档位（将来新加却忘了登记、或库里脏值）一律按桶 3 处理——
// 与"未知名次排最后"同一条取向：宁可保守地最后扣，也不给它一个凭空的优先权。
var tierBucket = map[string]int{
	TierFree:          TierBucketFreeGift,
	TierRegister:      TierBucketFreeGift,
	TierTask:          TierBucketFreeGift,
	TierPlanMonthly:   TierBucketPaidTemp,
	TierPlanYearly:    TierBucketPaidTemp,
	TierPlanPermanent: TierBucketPaidPerm,
	TierLegacy:        TierBucketPaidPerm,
}

// TierRank 返回某档位的**名次**（1 最先）。
// 参数 tier: 台账行上的档位字面；未知值回 tierRankUnknown。
// ⚠️ 名次不再单独决定扣法：它只在**同桶内**当次级判据用（用户令㋑"先到期先于档位号"），
// 跨桶次序由 TierBucketOf 决定。这个函数还服务另一件事——出栈数组与前端面板的固定次序。
func TierRank(tier string) int {
	for i, t := range tierOrder {
		if t == tier {
			return i + 1
		}
	}
	return tierRankUnknown
}

// TierBucketOf 返回某档位所属的桶（未知档位回桶 3＝收费无时效那一兜底桶）。
func TierBucketOf(tier string) int {
	if b, ok := tierBucket[tier]; ok {
		return b
	}
	return TierBucketPaidPerm
}

// TierSpendable 判一个档位是否会出现在 `quota_grants` 台账里（即能不能被核销）。
// 每日免费不落台账 ⇒ false；未知档算 true（它归进 legacy 那一桶，兜底扣，不丢钱）。
func TierSpendable(tier string) bool { return tier != TierFree }

// TierOrderPred 返回**唯一那条完整 ORDER BY 片段**，四截按用户两条令拼成，顺序与内容都不许调用方增删：
//
//	① 桶号（㋐ 免费赠送 > 收费有时效 > 收费无时效）
//	② expires_at ASC（㋑ 同桶内先到期先花——快作废的免费额度不会被挡在后面）
//	③ 档位号（㋑ 到期时刻完全相同时，仍按用户第一批给的档位次序：注册赠送先于任务奖励）
//	④ id ASC（方言兜底：PG 对同值行的游标顺序不做保证、SQLite 另有自己的顺序；
//	   不钉这一截，同一份库两次扣减可能挑不同的行——AGENTS §18 那条 is_home 假红同族）
//
// 调用形态就是 `ORDER BY ` + 本函数返回值；**别在后面再补一次 expires_at/id**（那会变成第五把尺子）。
func TierOrderPred() string {
	var b strings.Builder
	// ① 桶号：只把非兜底桶写进 CASE，其余走 ELSE（桶 3）
	b.WriteString("CASE tier")
	for _, t := range tierOrder {
		if TierBucketOf(t) == TierBucketPaidPerm {
			continue
		}
		b.WriteString(" WHEN '" + t + "' THEN " + fmt.Sprint(TierBucketOf(t)))
	}
	b.WriteString(" ELSE " + fmt.Sprint(TierBucketPaidPerm) + " END")
	// ② 同桶内先到期先花
	b.WriteString(", expires_at ASC, CASE tier")
	// ③ 档位号：七档逐个写出来，ELSE 走 tierRankUnknown
	for _, t := range tierOrder {
		b.WriteString(" WHEN '" + t + "' THEN " + fmt.Sprint(TierRank(t)))
	}
	// ④ id 兜底
	b.WriteString(" ELSE " + fmt.Sprint(tierRankUnknown) + " END, id ASC")
	return b.String()
}

// TierNamesInOrder 按扣减次序给出全部档位名（含第 ① 档免费与最后的 legacy 兜底）。
// 用途＝出栈数组的固定长度与前端逐档面板的靶子：**名单只从这里出**，别处不许再拼一份。
func TierNamesInOrder() []string {
	out := make([]string, 0, len(tierOrder))
	for _, t := range tierOrder {
		out = append(out, t)
	}
	return out
}

// TierOf 把一条台账行的既有字段归一成档位（回填与新建行**共用这一个判据**）。
// 参数 kind: quota_grants.kind（trial|plan|task|…）；source: 发放来源（register|admin|task|…）；
// pkgTier: 该行引用的套餐的 `packages.tier` 列现值（非套餐行传空串）。
// 返回名单之外的形态一律 TierLegacy（**不许**在这里猜档位，也不许返回空串——空串是"未回填"的记号，
// 把猜出来的值写成空串等于让回填腿永远判不出"这行到底归过档没有"）。
func TierOf(kind, source, pkgTier string) string {
	switch strings.TrimSpace(kind) {
	case "trial":
		// 注册赠送：现仓的形态是 kind='trial' AND source='register'（账号级发放那一路还会同时把 uid 落进 ref_id 与 user_id）。
		// 其余 trial（超管重发、活动补发等）不冒充注册赠送——宁可落 legacy 慢慢核，
		// 也不给一个来路不明的行发"免费赠送桶优先烧"的待遇。
		if strings.TrimSpace(source) == "register" {
			return TierRegister
		}
		return TierLegacy
	case "task":
		return TierTask
	case "plan":
		switch strings.TrimSpace(pkgTier) {
		case TierPlanMonthly, TierPlanYearly, TierPlanPermanent:
			return strings.TrimSpace(pkgTier)
		default:
			// 套餐行却拿不到合法档位列 ⇒ legacy ＋ 调用侧 WARN 计数（W10：不许改回按 duration_days 猜）。
			return TierLegacy
		}
	default:
		return TierLegacy
	}
}
