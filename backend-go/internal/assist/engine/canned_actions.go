// ============ canned_actions.go · 职责说明（★ 0AR 第 4 波 ⑱）============
// 挂件**动作按钮名**（feature_links.name）跟着「本轮作答语言」出，走 canned 那一条
// 翻译＋落库缓存＋出栈闸的路，与欢迎词／chips 同一套判据（不另立第二把尺子）。
//
// 缺陷本体（台账 ⑱，现网实证）：功能卡表里的 name 是运营写的中文（「充值与账单」「价格页」），
// 而 `FeatureLinksByKey` 把它**原样**塞进 `Reply.Actions`，挂件按字面渲染成按钮。
// 于是英文界面访客看到的一屏是「一段英文回答 ＋ 三个中文按钮」——
// 正文那条腿早就被 〇-AC／082x 那批接管了，按钮这一路根本不在翻译层的清单里，
// 所以它不是"翻坏了"，是**压根没翻**（这类"某条腿不在射程内"的形态，日志里一行都看不见）。
//
// ★ 语种口径：吃 **`effectiveReplyLang` 那一个答案**（本轮作答语言），不吃裸 `uiLang`。
// 理由与 AGENTS §一·13 那条 〇-AC 口径同源：中文界面里的英文提问，这一轮正文是英文，
// 按钮就必须是英文；反过来英文界面里访客用中文追问，这一轮正文回中文，按钮跟着回中文。
// 拿界面语言决定按钮语种，就是「提示词教英文、闸门看中文」那一族分叉的下一个实例。
//
// ★ 粒度口径：**一个 key 一行缓存**（`i18n:feature_<key>:<lang>`），不是整表一批一行。两档取舍：
//   - 整表一批只拨一枪，但它的源文是「当前所有启用功能卡的名字」——运营改任意一个名字
//     就让 12 个语种全部重翻，且条数契约一崩就是**整排按钮**回中文（一崩全崩）；
//   - 逐键则是改哪个重翻哪个、坏一个只坏一个，还保住 `!manual` 那一档的粒度
//     （运营可以只手写「充值与账单」这一个按钮的外文，这条路是 ㊷ 里点名的正路）。
//     代价是冷语种一枪要拨 N 次 ⇒ 用**一条共享预算**压住（见下面 actCtx 那段），
//     总等待不超过一档 `canned_translate_timeout_sec`，超出的键本轮不翻、也不白拨。
//
// ★ 缓存键的形状是一条**硬约束**：`splitCannedCacheKey` 认的是恰好三段（`i18n:<kind>:<lang>`），
// 所以 kind 里**不许有冒号**（这就是用 `feature_<key>` 而不是 `feature:<key>` 的原因）。
// key 里若出现非 `[A-Za-z0-9_-]` 的字符（将来有人把功能卡 key 写成带空格／斜杠的形态），
// 拼出来的键会拆不开 ⇒ 启动期清理永远扫不到它（坏行常驻），写侧却照样能命中它。
// 因此这里先验一次形状，不合格就**不翻、出声**，而不是让库里长出拆不开的键。
//
// ★ 与启动期清理腿（canned_purge.go）的同步义务：新增一档 canned 原文来源，
// 必须同时让 `cannedSourceFor` 认这一档——清理腿要能**现算出这一行该配的指纹**。
// 漏了那条的后果不是报错，而是"清理跑绿、报 0 行、旧字节还在投"（本文件头第 3 段那一族）。
// 等值锁＝canned_actions_0ar_test.go 的「写侧落的行，清理腿能现算出同一个指纹」。
//
// ★ 本批**没有为这一档再抬一次 `cannedPromptRev`**，理由是射程而不是省事：
// 新增一档文本不会改变欢迎词／chips 任何一行的出栈形态，而这一档在旧二进制里根本没有行
// （库里不存在需要作废的旧代形态）。⚠️ 将来若改的是**按钮名的出栈形态**（口径措辞／条数契约／
// 卫生判据），按 §一·13 那条通用口径必须抬档，别拿本段当"这一族都可以不抬"的先例。
// =============================================
package engine

import (
	"context"
	"strings"

	"translator/internal/observability"
)

// featureKindPrefix 按钮名这一档在 canned 缓存里的 kind 前缀（键形如 `i18n:feature_billing:en`）。
const featureKindPrefix = "feature_"

// cannedFeatureKeyUnsafe 对外排障契约（与那六个 reject*／五个 bg_* 同族）：
// 功能卡 key 的形状拼不出合法缓存键 ⇒ 这一档本轮不翻。**刻意不并进退避**
// （那不是上游坏了，重拨一万次也不会好；进退避反而会把同语种的其他按钮一起押掉）。
const cannedFeatureKeyUnsafe = "canned_feature_key_unsafe"

// featureKindFor 功能卡 key → canned 的 kind（只拼前缀，不做校验——校验在调用方那一步）。
func featureKindFor(key string) string { return featureKindPrefix + key }

// featureKeyIsCacheSafe 判这个 key 能不能进缓存键：非空且只含 ASCII 字母数字／下划线／连字符。
//
// 判据与 `isGoMarkerWordRune` 同一档字符集（控制序列那一路对 key 形状的要求本来就一致），
// 但**不复用那个函数**：它管的是「正文里的标记词」，这里管「库里的键形」，
// 两处一旦共用，下一次给标记词放宽（比如允许点号）就会顺手把缓存键的形状契约也放宽掉。
// 刻意不含冒号——那是 `splitCannedCacheKey` 的分段符。
func featureKeyIsCacheSafe(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if !isGoMarkerWordRune(r) {
			return false
		}
	}
	return true
}

// featureNameFor 现读某个功能卡 key 的**启用行** name（清理腿与写侧共用同一个读法）。
//
// 两条放行都是刻意的，返回值假＝「这一行现在该由哪句原文翻出来」这件事**算不出来**：
//   - 表读失败（DB 故障）⇒ 假；此时清理腿必须跳过，把"读不到"当"过期"就是把缓存当垃圾清；
//   - key 不在启用行里（被停用／被删）⇒ 假；此时那一行缓存已经不会再被写侧命中，
//     留着它比删掉它安全（删错方向的代价永远大于多留一行）。
//
// key 按库口径小写归一：`FeatureLinksByKey` 与 `knownFeatureKeys` 都拿小写 key 比，
// 这里不归一就会出现「按钮出得来、清理腿认不出」的同族分叉。
func (e *Engine) featureNameFor(key string) (string, bool) {
	rows, err := e.db.List("feature_links", true)
	if err != nil {
		return "", false
	}
	want := strings.ToLower(strings.TrimSpace(key))
	for _, r := range rows {
		if strings.ToLower(asStr(r["key"])) == want {
			name := strings.TrimSpace(asStr(r["name"]))
			if name == "" {
				return "", false // 名字没配：没有原文可翻，不猜
			}
			return name, true
		}
	}
	return "", false
}

// localizeActionNames 把一批动作按钮的名字翻成本轮作答语言（★ 0AR ⑱）。
//
// 只在 `Engine.Respond` 那一条咽喉上调用（锁＝TestLocalizeActionNamesOnlyAtRespondThroat）：
// 四道回复分支与兜底各自调一遍，就是下一次「某一道漏了、按钮语种跟正文不一致」的产地
// （AGENTS §一·13 给守卫链立的同一条规矩）。
//
// 四条行为口径：
//  1. 中文系／未知语种 ⇒ **原样返回**（`localize` 自己会早退，这里连键都不碰）；
//  2. 整段共用**一条** `CannedSyncBudget` 截止：第一个键吃掉预算，之后的键不再白等、
//     也不再拨后台枪（现网那条 30 秒反代砍的是整次 reply，不是单条文本）；
//  3. 翻不出就保留中文原名（与 canned 那条「绝不编一份译文」同口径），**不许**回空串——
//     按钮名空了就是一个隐形入口，客户点不到页面比看到中文严重；
//  4. 只在真换了字时才动 Action 结构，且**先复制一份切片**再写：`Reply.Actions` 会被
//     api 侧同时用于出栈与落库（server.go 的 actionMaps），就地改共享切片是并发回写那一族。
func (e *Engine) localizeActionNames(ctx context.Context, actions []Action, answerLang string) []Action {
	if len(actions) == 0 || visitorWantsChinese(answerLang) {
		return actions
	}
	out := make([]Action, len(actions))
	copy(out, actions)

	// ★ 一条共享预算罩住整排按钮（见上面口径 2）。子 ctx 的 deadline 天然取"更早的那一个"，
	// 所以 localize 内部那条 `WithTimeout(ctx, CannedSyncBudget())` 不会把它放宽回 N×8 秒。
	actCtx, cancel := context.WithTimeout(ctx, e.CannedSyncBudget())
	defer cancel()

	done := map[string]bool{} // 同一次应答里同一 key 只翻一次（多道分支可能并排给重复按钮）
	for i := range out {
		key := strings.ToLower(strings.TrimSpace(out[i].Key))
		name := strings.TrimSpace(out[i].Name)
		if key == "" || name == "" || done[key] {
			continue
		}
		done[key] = true
		if !featureKeyIsCacheSafe(key) {
			// 出声但不翻：这一档的坏形态是"库里长出一行拆不开的键"，代价比"这个按钮这次是中文"大得多。
			observability.Warn(ctx, "assist.engine 功能卡 key 形状拼不出合法缓存键，按钮名本轮不翻",
				"kind", featureKindFor(key), "lang", answerLang, "reason", cannedFeatureKeyUnsafe,
				"before", firstRunes(name, 60))
			continue
		}
		if actCtx.Err() != nil {
			// 预算已用尽：剩下的键本轮直接出中文原名。**不再拨枪**（含后台腿）——
			// 拨出去也是一进 ctx 就 canceled，只留下几行没人看的 WARN 和一批互相抢上游的后台腿。
			observability.Info(ctx, "assist.engine 按钮名整排翻译预算用尽，其余本轮出中文原名",
				"kind", featureKindFor(key), "lang", answerLang, "reason", cannedSyncTimeout,
				"budget_ms", e.CannedSyncBudget().Milliseconds())
			continue
		}
		// wantLines 传 1：按钮名必须是一行。翻成两行（模型爱补一句解释）就整条判失败留中文——
		// 与 chips 拿条数当契约是同一把尺子，不因为"这条只有几个字"就放宽。
		got := e.localize(actCtx, featureKindFor(key), name, answerLang, 1)
		if strings.TrimSpace(got) == "" || got == name {
			continue // 失败／被闸拒／本来就等价：保留中文原名（口径 3）
		}
		out[i].Name = got
	}
	return out
}
