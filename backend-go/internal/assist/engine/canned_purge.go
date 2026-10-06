// ============ canned_purge.go · 职责说明 ============
// 启动期**一次性**清掉「口径已经换代」的 canned 译文孤儿行（★ 0AR 第 4 波 ③）。
//
// 为什么需要这一腿（不靠运行期那条 MISS 自动覆盖就够了吗）：
//
//	`cannedPromptRev` 一抬，库里**每一行**非人工档译文的指纹都当场失效。运行期确实会自愈
//	（head ≠ fp ⇒ 按未命中重翻、翻成后盖掉那一行），但自愈的前提是**有人来**：
//	一个语种十天没有一个访客，那一行旧字节就在 configs 里躺十天，
//	而它是这条链上唯一一份"客户屏幕上正在投什么"的持久事实——管理台导出、排障查库、
//	以及下一次「这行到底是谁写的」的判断，全部以它为准。留着旧代行不是"反正不命中"，
//	是**让库里的读数和界面里的读数长期不一致**（本仓为这类不一致付过几次账了）。
//
// ★ 判据是**逐行重算指纹**，不是 `LIKE '<旧 rev>%'`：
//
//	指纹头只有 12 个 hex 字符（见 srcFingerprint），**里面根本没有 rev 字样**——
//	按前缀匹配一条都抓不到，还会让人以为"抓不到＝库里干净"。
//	唯一可靠的问法＝拿这一行**该由哪句原文＋哪份口径**翻出来，在这里现算一遍再比：
//	现算的入参与 localize 完全同源（同一句源文现读、同一个 cannedCacheKey、同一个 localizeContract），
//	所以"清理脚本判它过期"与"下一次 greet 判它过期"必然是同一个结论——
//	两侧各算一套就是下一次「脚本清了 20 行、界面还在投第 21 行」的产地。
//
// ★ 三条"宁可漏删不可误删"的放行分支（每一条都比"多留一行旧字节"重要）：
//  1. **`!manual` 行永不删**：运营手写的那一行就是他要投出去的那一行，
//     换代不该把人工配置当旧数据洗掉（AGENTS §一·13 与 localize.go 文件头第 3 条同一口径）；
//  2. **原文取不到就整段跳过**：`configs.welcome` 与 `configs.quick_chips` 都为空时，
//     这一行该配什么指纹根本算不出来——算不出就不删（此时删等于拿猜测当证据）；
//  3. **不认识的后缀直接跳过**：kind 不在 canned 的那几档里（welcome／chips／★ 0AR ⑱ 起的
//     `feature_<key>`）说明这一行不是本模块写的形态，动它＝替别人清库。
//
// 只做"删"，不做"改写指纹头"：与 localize.go 读侧那条纪律同源——
// 头一旦被写成别的值，后台腿会按"已过期"永久拒写，失效就变成永久失效。
// =============================================
package engine

import (
	"context"
	"strings"

	"translator/internal/observability"
)

// cannedCachePrefix canned 译文在 configs 里的键前缀（与 cannedCacheKey 的拼法逐字对应）。
// 启动期扫描只罩这一段：**禁止**把它改成全表扫（configs 里还住着凭据、人设、流程开关，
// 一次 LIKE '%' 就把排障脚本变成了全库 dump）。
const cannedCachePrefix = "i18n:"

// splitCannedCacheKey 拆 canned 缓存键：`i18n:<kind>:<归一语种>`。
// 返回值假＝这不是本模块写的形态（段数不对／kind 或语种为空），调用方按"不认识"跳过。
func splitCannedCacheKey(key string) (kind, lang string, ok bool) {
	if !strings.HasPrefix(key, cannedCachePrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, cannedCachePrefix)
	// 语种在前、kind 在后都**不**可能：拼法就是三段，用 SplitN 上限 3 让"多出来的段"暴露成
	// 第三段里的冒号，再由下面那句 Contains 判掉——比静默拆成两段更诚实。
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	kind, lang = parts[0], parts[1]
	if kind == "" || lang == "" || strings.Contains(lang, ":") {
		return "", "", false
	}
	return kind, lang, true
}

// cannedSourceFor 现读「这一档 canned 真会送去翻的那串中文原文」＋它的条数契约。
//
// 唯一存在的理由：让启动期清理与 greet 请求链**读到同一句原文**。
// 入参形态必须与 api 侧的 greet 一致（welcome 取 configs.welcome、空则回落 greeting 话术；
// chips 取 configs.quick_chips 并按逗号拆条后以换行拼串），否则两边算出的指纹天然不等，
// 清理就会把**每一行**都判成过期——那不是清理，那是把缓存全洗空然后让每个首屏现翻。
//
// known 假＝kind 不是 canned 那两档（调用方按"不认识"跳过，禁止猜）；
// 第二返回值空串＝原文此刻取不到（调用方必须跳过，见文件头第 2 条放行分支）。
func (e *Engine) cannedSourceFor(kind string) (text string, wantLines int, known bool) {
	switch kind {
	case "welcome":
		text = e.db.GetConfig("welcome", "")
		if strings.TrimSpace(text) == "" {
			text = e.Greeting() // 与 handleGreet 同一回落链（api/server.go），别只读 configs
		}
		return text, 0, true
	case "chips":
		lines := chipsLinesFromCSV(e.db.GetConfig("quick_chips", ""))
		if len(lines) == 0 {
			return "", 0, true
		}
		return strings.Join(lines, "\n"), len(lines), true
	default:
		// ★ 0AR 第 4 波 ⑱：动作按钮名那一档（kind＝`feature_<功能卡 key>`）。
		// 这一条**必须**与写侧（canned_actions.go 的 localizeActionNames）读同一行 feature_links、
		// 按同一个 wantLines 算：漏在这里的形态不是报错，而是"清理跑绿、报 0 行、旧字节还在投"
		// （文件头那段"脚本报 0 行"就是它）。认不出前缀才走下面的 known 假。
		if strings.HasPrefix(kind, featureKindPrefix) {
			name, ok := e.featureNameFor(strings.TrimPrefix(kind, featureKindPrefix))
			if !ok {
				return "", 0, true // 读不到／已停用：算不出该配什么指纹 ⇒ 那一行留着（放行分支 2）
			}
			return name, 1, true
		}
		return "", 0, false
	}
}

// PurgeStaleCannedTranslations 删掉指纹已与当前（原文＋口径）不等的 canned 译文行。
//
// 只在**启动期**调一次（见 cmd/assist-server/main.go），不进请求链：
// 它要遍历整段键、每行一次读一次判，放在 greet 上就是把首屏打成扫库。
// 返回值是删掉的行数（0 是常态：一代 rev 存续期间库里本来就没有旧代行）。
//
// ⚠️ 调用时机必须在 seed 之后——seed 会写 configs.welcome／quick_chips，
// 在它之前算出的"当前原文"可能是空串，于是本次清理整段走上面那条放行分支，
// 白跑一趟还报 0 行（现网没人会知道它没清）。
func (e *Engine) PurgeStaleCannedTranslations(ctx context.Context) int {
	keys, err := e.db.ConfigKeysByPrefix(cannedCachePrefix)
	if err != nil {
		observability.Warn(ctx, "assist.engine canned 旧代译文清理跳过（键列表没取到）",
			"reason", "canned_purge_no_keys", "err", err)
		return 0
	}
	deleted := 0
	for _, key := range keys {
		kind, lang, ok := splitCannedCacheKey(key)
		if !ok {
			continue // 不是本模块写的键：一行都不碰
		}
		cur := e.db.GetConfig(key, "")
		if cur == "" {
			continue // 已被读侧作废腿置空（那一条自己会重翻），这里不重复记
		}
		head, _, ok := splitCachedTranslation(cur)
		if !ok {
			continue // 值不是「指纹\n译文」形态，说明不是这一路写的，不猜
		}
		if strings.HasSuffix(head, manualMark) {
			continue // ★ 人工档永不自动作废（文件头第 1 条）
		}
		src, wantLines, known := e.cannedSourceFor(kind)
		if !known {
			continue // ★ 认不出的 kind（文件头第 3 条）
		}
		if strings.TrimSpace(src) == "" {
			continue // ★ 原文取不到，算不出该配什么指纹（文件头第 2 条）
		}
		// 与 localize 里那一行同函数同入参：源文＋（rev＋按这句源文筛出来的口径段）→ 12 位指纹。
		fp := srcFingerprint(src + "\x00" + localizeContract(lang, srcTopicsOf(src)))
		if head == fp {
			continue // 仍是当前代：内容好坏由出栈闸在读写两侧管，不属于本腿射程
		}
		if err := e.db.DeleteConfig(key); err != nil {
			observability.Warn(ctx, "assist.engine canned 旧代译文行删除失败，本次保留",
				"kind", kind, "lang", lang, "reason", "canned_purge_delete_failed",
				"head", firstRunes(head, 40), "want", fp, "err", err)
			continue
		}
		deleted++
		// 逐行一行 INFO：清理是幂等的，但"清了多少、清的是哪些语种"是发版后排障要读的那一行
		// （只报一个总数的话，第二天发现某语种一直没译文，就分不清是没清还是没重翻）。
		observability.Info(ctx, "assist.engine canned 旧代译文孤儿行已清理",
			"kind", kind, "lang", lang, "reason", "canned_purge_stale",
			"head", firstRunes(head, 40), "want", fp, "want_lines", wantLines)
	}
	if deleted > 0 {
		observability.Info(ctx, "assist.engine canned 旧代译文清理完成",
			"reason", "canned_purge_done", "rev", cannedPromptRev, "deleted", deleted, "scanned", len(keys))
	}
	return deleted
}
