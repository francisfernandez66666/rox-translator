// ============ review_purity.go · 职责说明 ============
// ★ 〇-AR 第 8 波（㊶）：译文的「目标语种脚本纯度」两条腿。
//
// 现网实证（匿名 POST /api/trial/translate，en→zh 三次实跑全 200、字节级留证）：
//
//	原文 We need to translate the product manual for the Frankfurt auto show next week.
//	出栈 我们需要为下周的法兰克福汽车展翻译产品手册。**We need to translate the product manual for next week's Frankfurt Motor Show.**
//
// 关键读数是**后面那段英文不是原文逐字**（auto show→Motor Show、语序也变、compatible→suitable、
// kits→kit），它是**由中文译文再生成出来的回译**。而全流程里只有审校腿手里同时有
// 【原文】与【待审校译文】那份中文（engine.ReviewTranslation），初翻腿的输入只有英文原文，
// 它要复述也只能复述逐字原文 ⇒ 缺陷本体定位在**审校腿的产物**，
// 而它随后在 text.go 的校对环节**直接覆盖**掉正确的初翻。
//
// 两条腿分工（方向都是"宁可少修一刀，不可把客户要看的正文削掉"）：
//
//	② reviewOutputRejectReason —— 审校产物脚本不纯 ⇒ **丢弃审校结果、保留初翻**（调用方把 ""
//	   当"这一轮没改"，本来就是现成的退路，爆炸半径最小）；
//	④ stripTrailingForeignResidual —— 出栈尾段整句异脚本 ⇒ **只剥尾段**，
//	   且必须同时过三道判据（只剥尾段／数字序列逐字不变／尾段够长），
//	   外加两条由实现结构保证的不变量（剥后非空、带目标脚本的行一行不少）——见该函数注释。
//	   这一条治的是"初翻腿自己复述原文"那一族（初翻被丢弃就没有更好的东西可退，只能剥不能丢）。
//
// ⚠️ 两条腿都**不碰挂件 canned 缓存**（那一路的出栈闸在 internal/assist/engine/canned_guard.go），
// 因此本批**不需要**抬 cannedPromptRev。
// ⚠️ 阈值 8 词的取值依据与误剥代价见 reviewLatinRunMinWords 的注释，改阈值前先读那一段。
// =============================================
package engine

import (
	"regexp"
	"strings"
	"sync"
)

// reviewLatinRunMinWords 判"整句外语残留"的连续拉丁词阈值。
//
// 取 8 的依据（都是拿真数据核过的，不是拍的）：
//   - ㊶ 现网三条回译尾段的长度分别是 13／10／10 词，全部 ≥8；
//   - 正常中文译文里合法的拉丁片段是**术语与单位**，不是整句：
//     「Bluetooth 5.0」「iOS 17 及以上」「ROX SUV」「kg·mm²」这类最长 3–4 词，
//     连"请参照 Product Manual for the Frankfurt Auto Show"这种带英文标题的句子也只有 7 词。
//
// 误判的代价是**不对称**的：② 那一档丢弃审校只是"这一轮没被润色"（初翻照发），
// ④ 那一档有四道前置兜着；反过来把阈值调低（比如 4 词）会把合法保留的英文术语整段削掉，
// 那才是把正确译文判成缺陷。想调这个数必须连带的把两侧反证用例一起重跑。
const reviewLatinRunMinWords = 8

// latinWordRe 拉丁字母词（含词内连字符／撇号，如 "week's"、"state-of-the-art"）。
// ⚠️ 必须用 \p{Latin} 而不是 [A-Za-z]：法/德/西的带音字母（é ü ñ）也是拉丁脚本，
// 按 ASCII 圈会把「Voici l’expérience café」这种正常法文判成非拉丁、也让拉丁目标语侧的判据失真。
var latinWordRe = regexp.MustCompile(`\p{Latin}+(?:['’\-]\p{Latin}+)*`)

// nonLatinLetterRe 任何**非拉丁**的文字字母（汉字／假名／谚文／西里尔／阿拉伯／泰文／希腊／heb…）。
// 它的作用是"最长拉丁连跑"的**断点**——数字、空白、标点都不断跑，
// 否则「产品手册。 We need to translate…」这种"中文整句＋英文整句"形态会被拆成两段各 1 词。
var nonLatinLetterRe = regexp.MustCompile(`\p{Han}|\p{Hiragana}|\p{Katakana}|\p{Hangul}|\p{Cyrillic}|\p{Arabic}|\p{Thai}|\p{Greek}|\p{Hebrew}`)

// digitSeqRe 数字序列（④ 的第二道前置：剥完数字必须一字没少）。
var digitSeqRe = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// maxLatinWordRun 返回文本里**最长的一段连续拉丁词数**（以非拉丁字母为断点）。
// 参数 text 待测文本（已经过后处理清洗的出栈形态）。返回最长连跑的拉丁词个数。
func maxLatinWordRun(text string) int {
	max, cur := 0, 0
	// 逐"词"扫描：latinWordRe 找到词，词与词之间若夹着非拉丁字母则断跑
	for len(text) > 0 {
		loc := latinWordRe.FindStringIndex(text)
		if loc == nil {
			break
		}
		// 该词之前（相对上一词结尾）的间隙里有没有非拉丁字母？有 ⇒ 断跑
		gap := text[:loc[0]]
		if nonLatinLetterRe.MatchString(gap) {
			cur = 0
		}
		cur++
		if cur > max {
			max = cur
		}
		text = text[loc[1]:]
	}
	return max
}

// purityTargetScripts 目标语种的书写体系查表（★ 派生自 postprocess.go 的 scriptMap，不另立第二份名单）。
// zh 刻意补进来：scriptMap 因为历史原因没收 zh（中文是"删除中文"那条腿的豁免档），
// 但纯度判据必须认它是 CJK 目标——㊶ 本体就是 zh。
// 返回 "" 表示不认识这个语种（不认识 ⇒ 不判，fail-soft）。
func purityTargetScripts(langCode string) string {
	if langCode == "zh" {
		return "cjk"
	}
	return scriptMap[langCode]
}

// reviewOutputRejectReason ② 审校产物纯度判据。
// 参数 targetLang 目标语种；out 审校腿清洗后的产物。
// 返回拒绝原因档名（对外排障契约，逐字钉在单测里）；"" 表示合格可用。
// 目前只有一档：review_latin_run_in_nonlatin_target。
//
// 射程刻意只圈**非拉丁目标**：拉丁目标（en/es/fr…）里的中文残留已经被
// StripChineseInNonZh 无条件删掉了，那条腿不需要第二把尺子。
// 语种不认识（scriptMap 没有）⇒ 不判（宁可不拦，不可拦错）。
func reviewOutputRejectReason(targetLang, out string) string {
	script := purityTargetScripts(targetLang)
	if script == "" || script == "latin" {
		return ""
	}
	if maxLatinWordRun(out) >= reviewLatinRunMinWords {
		return "review_latin_run_in_nonlatin_target"
	}
	return ""
}

// stripTrailingForeignResidual ④ 尾段异脚本剥离（带四道前置，任一不过就**原样返回**）。
// 参数 targetLang 目标语种（只对非拉丁目标生效）；text 已清洗的出栈文本。
// 返回（剥后的文本, 是否剥过）。
//
// 四道前置（与 §一·13 的 applyJaResidueFixups 同源思路：改坏不如不改）：
//  1. **只剥尾段**——剥点锚在「最后一个目标脚本字母之后的第一个拉丁字母」，
//     尾段整段不许含任何非拉丁字母；两者之间的**共用标点**（「手册。 We need…」的句号）留在正文里；
//  2. **剥后非空、且带目标脚本的行一行都不许少**——这两条在"后缀锚定"的实现里是**结构性不变量**
//     （剥点右侧按构造只剩拉丁字母／数字／标点，那个目标脚本字母必然留在 head 里，
//     所以 head 非空、含目标脚本的行也一行不可能落进尾段）。因此**不写走不到的死支**，
//     改由用例 TestStripTrailingForeignResidualInvariants 把这两条性质钉住
//     （AGENTS §一·13「死支别写断言」的反面用法：不变量要断言，但不要伪装成判据）；
//  3. **数字序列逐字不变**——尾段带数字＝它可能在传信息（型号／日期／数量），不许猜 ⇒ 不剥。
//
// 阈值档名与"不剥"的返回形态同为对外排障契约，逐字钉在用例里。
func stripTrailingForeignResidual(targetLang, text string) (string, bool) {
	script := purityTargetScripts(targetLang)
	if script == "" || script == "latin" {
		return text, false
	}
	r := []rune(text)
	// 前置 1：找最后一个非拉丁字母的位置（＝正文与尾段的分界依据）。
	// 数字/标点/空白都算"共用"，不当作边界。
	lastNative := -1
	for i := len(r) - 1; i >= 0; i-- {
		if nonLatinLetterRe.MatchString(string(r[i])) {
			lastNative = i
			break
		}
	}
	if lastNative < 0 {
		return text, false // 整段皆外语：剥了就剩空串，这一档不属于"尾段残留"
	}
	// ★ 剥点取「最后一个目标脚本字母之后的**第一个拉丁字母**」，不是最后一个目标脚本字母本身——
	//	两者之间夹的是**共用标点**（「手册。 We need…」的那个句号、顿号、引号、右括号）。
	//	把剥点钉在字母上会把「。」一起削掉（首跑真踩：出栈变成"…产品手册"无句号），
	//	那是把中文正文的标点也改了，超出"只剥尾段"的承诺。
	cut := -1
	for i := lastNative + 1; i < len(r); i++ {
		if latinWordRe.MatchString(string(r[i])) {
			cut = i
			break
		}
	}
	if cut < 0 {
		return text, false // 尾段没有一个拉丁字母＝没有外语整句
	}
	tail := string(r[cut:])
	// 尾段必须**够长**（≥阈值个拉丁词）才叫"整句外语残留"，一两个术语词不动
	if maxLatinWordRun(tail) < reviewLatinRunMinWords {
		return text, false
	}
	head := strings.TrimRight(string(r[:cut]), " \t")
	if strings.Join(digitSeqRe.FindAllString(head, -1), "|") != strings.Join(digitSeqRe.FindAllString(text, -1), "|") {
		return text, false // 前置 3
	}
	return head, true
}

// nativeLineCount 数「含非拉丁字母（目标脚本）的行数」，供不变量断言用（非生产判据）。
func nativeLineCount(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if nonLatinLetterRe.MatchString(line) {
			n++
		}
	}
	return n
}

// clipRunes 按**字符**（不是字节）截断日志里带的文本样本。
// 参数 s 原文；n 最多保留多少个字符。返回截后文本（超长时补 … 表明是被截的、不是全文）。
//
// 为什么不用现成的 `fmt.Sprintf("%.80q", s)`：那种精度写法按**字节**切，一条中文译文被切在
// 第 80 个字节上就是半个 UTF-8 序列，slog 的 JSON handler 会把它写成替换字符——排障时看起来像
// "译文里有个乱码尾巴"，而真相是日志自己劈了一刀（AGENTS §三「进 PG 的文本按字节截会劈成
// 非法 UTF-8」同族，只是这一刀落在观测面上）。按字符截就没有这一档。
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ============ 观测计数（㊶ 的「读得到」腿） ============
//
// ★ 为什么要有这一份计数：这两条腿的失败形态是**界面正常、只是少润色了一句**
// （② 丢掉审校、④ 剥掉尾段都不报错、不退 5xx），现网排障与「改完有没有真生效」只能靠这里。
// 口径沿用 §一·12／§一·13 的纪律：**只出动作档与语种，绝不出原文／译文／上游地址／Key**。
//
// 三个档名同为对外排障契约，逐字钉在单测与 /metrics 里：
//
//	review_rejected        —— ② 单段审校产物脚本不纯，整份丢弃、保留初翻
//	review_batch_rejected  —— ② 批量审校里某一行脚本不纯，该行不采纳（其余行照采）
//	tail_stripped          —— ④ 出栈尾段整句异脚本，剥掉尾段保留正文
var (
	purityMu     sync.Mutex
	purityCounts = map[string]int64{}
)

// recordPurityAction 累加一次「纯度动作」计数。
// 参数 lang 目标语种（空串归一成 "unknown"，别让一条腿静默消失在报表里）；action 上面三个档名之一。
func recordPurityAction(lang, action string) {
	if lang == "" {
		lang = "unknown"
	}
	key := lang + "|" + action
	purityMu.Lock()
	defer purityMu.Unlock()
	purityCounts[key]++
}

// PuritySnapshot 只读快照，供 /metrics 出栈。
// 返回一份**拷贝**（AGENTS §一·3「并发回写共享 map 必须先快照」同源：
// 直接把内部 map 交出去，采集腿 range 的时候业务腿正在写，是 runtime fatal error 而不是 panic）。
func PuritySnapshot() map[string]int64 {
	purityMu.Lock()
	defer purityMu.Unlock()
	out := make(map[string]int64, len(purityCounts))
	for k, v := range purityCounts {
		out[k] = v
	}
	return out
}
