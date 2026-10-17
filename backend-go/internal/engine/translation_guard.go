// ============ 本文件职责中文说明 ============
// 文件翻译主链「译文可用性」的**唯一判定口径**（P0 整改 RC-2：回显/同文/指令残留四处口径不一致）。
//
// 背景：HandleFile 有 5 个译文写入点——① KB 直配命中、② KB 语言的模型批量补漏、
// ③ 其他语言批量直翻、④ 硬闸小批量重译、⑤ 硬闸逐段兜底。历史上这 5 处各自手写
// `!= "" && != "[翻译失败]" && != 源文`，唯独 ① 只判了非空。后果分两层：
//   - 脏行直接进成品：TM/TMX 导入与历史沉淀里存在「源文 == 译文」的行（真实双语文件
//     大量条目本就同文、术语自映射、缓存污染），中文会被当成「命中的译文」写进交付物；
//   - 更致命的是**永久锁死硬闸**：硬闸与 untranslated 统计都按「langTranslations 里
//     有没有这个键」判断是否译出（键存在即跳过）。坏命中把键写进去了 ⇒ 该段既不重试、
//     也不计入 untranslated、更不告警，工单显示「已完成」而用户拿到残缺成品。
//
// 因此本文件把判定收口成 IsTranslationUsable 一处定义，并配套 KB 结果汇总、缺失段计算
// 两个纯函数，让「写入门槛」和「未译统计」天然同一口径（纯函数也便于不打真模型做断言）。
// ========================================
package engine

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// translationFailureMark BatchTranslate 在失败槽位写入的占位字面量（见 engine.go 批量链）。
// 集中成常量是为了让「失败占位」与「真译文」的区分只有一处定义，不再各调用点抄字符串。
const translationFailureMark = "[翻译失败]"

// IsTranslationUsable 判定一份候选译文能否作为交付（文件主链 5 个写入点的唯一口径）。
// 参数 src: 送翻的源文段；out: 候选译文（KB 命中值或模型返回，未经判定前的原始候选）。
// 判不可用的八类，任一命中即 false：
//  1. 空串 / 纯空白 —— 没有内容可言交付；
//  2. 批量失败占位 [翻译失败] —— 上游明确报错的槽位；
//  3. 与源文同文（去空白后逐字相等）—— 模型或 KB 原样回显 = 根本没翻（含 "#"/"1"/"•"
//     这类无上下文短串的回显，与既有模型路径口径一致，宁可走补漏也不静默交付）；
//  4. 指令回显残留 —— 见 hasInstructionEcho 的结构指纹；
//  5. 短格长度爆炸 —— 见 hasLengthExplosion（★ F-38，2026-09-25 批 D：6 字表格单元格被
//     翻成 2100 字符的整封英文邮件，前四判据全放行、直接进交付物）；
//  6. ★ 2026-10-17 第二批：**不需要翻译的段**（数字/货币/日期/时间/符号）被译成了别的字符串
//     —— 见 nonLinguisticMustStayVerbatim（工单 T20261017234421W7H：整排 "and"）；
//  7. ★ 同批：源段是实义词，译文却整体退化成一个孤立功能词 —— 见 degenerateToFunctionWord；
//  8. ★ 同批：译文把源段逐字引号引用再另起一句解释 —— 见 quotedVerbatimSourceExplained。
//
// ★ 不能只判非空：这是本文件存在的理由，任何新增写入点都必须先过本函数。
func IsTranslationUsable(src, out string) bool {
	o := strings.TrimSpace(out)
	if o == "" || o == translationFailureMark {
		return false
	}
	if normalizeForCompare(src) == normalizeForCompare(out) {
		return false
	}
	if hasInstructionEcho(src, out) {
		return false
	}
	if hasLengthExplosion(src, out) {
		return false
	}
	if nonLinguisticMustStayVerbatim(src, out) {
		return false
	}
	if degenerateToFunctionWord(src, out) {
		return false
	}
	if quotedVerbatimSourceExplained(src, out) {
		return false
	}
	return true
}

// normalizeForCompare 折叠全部空白用于「同文」比较。
// 为什么去空白：模型常把表格单元格里的换行/多个空格压成单空格后原样回显（源文
// "产品  方案\n书" vs 译文 "产品 方案 书"），逐字比较会漏判成「已翻译」，
// 折叠空白后仍是同文 ⇒ 正确判为未译。
func normalizeForCompare(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hasInstructionEcho 「内部指令泄漏进交付物」的结构指纹：源文本身不含 '<'，
// 候选译文却凭空出现 '<'（实测形态：`<Only output the final translated text,
// enclosed entirely within  and nothing else>`）。
//
// 成因：翻译 prompt 用 <t>…</t> 作输出契约标签，模型违约时把「剥掉标签的指令正文」
// 一起吐回来。postprocess.stripPseudoTags 会拆掉伪标签，但拆完剩下的正是这种
// 「尖括号里包着一整句英文指令」的文本，且这类串不会被后续任何中文清洗链拦住。
//
// ★ 为什么用结构规则而**不是**指令短语黑名单（已实测踩过，别再改回去）：
//   - 本工单文档正文第 259 行本来就是一行讲「只输出译文」的表格说明，
//     按 "only output"/"只输出" 之类短语匹配会把**正常正文**误杀成未译出；
//   - 短语黑名单随目标语言数量线性膨胀（12 语种 × 多模型措辞），永远补不全；
//   - 结构不变量与措辞无关：合法译文不会凭空引入源文里不存在的尖括号。
//     代价是「源文无 < 而译文写成了 a < b」这类极少见译法会被判不可用而走补漏重试，
//     这是可接受的偏保守方向（宁可重试也不交付可疑串）。
func hasInstructionEcho(src, out string) bool {
	return !strings.Contains(src, "<") && strings.Contains(out, "<")
}

// hasLengthExplosion ★ F-38 第 5 判据「短格长度爆炸」的结构指纹（2026-09-25 批 D）。
//
// 实测形态：xlsx 表格 6 字单元格「产品方案书」被上游吐回 2100 字符的整封英文邮件
// （疑似供应商侧缓存/解码串扰，账本已留字节级复现样本）。前四判据对此全放行：
// 非空、非失败占位、与源文不同文、不含凭空尖括号——脏串直接写进交付物。
//
// 判据：只对**短源文格**生效（src ≤ shortGuardMaxSrcRunes，默认 12 rune），
// 译文 rune 数超过 max(ratio×src, floor) 且封顶 absCap 即判爆炸（默认 8× / 下限 80 / 上限 300）。
// 为什么限定短格：长段落翻译（尤其中→德/俄等长语系）长度比天然偏高，全文生效会误杀；
// 而「缩写展开」这类合法短译（AWS→亚马逊云科技）在 80 rune 下限内几乎不可能被拦。
// 阈值全部 env 可配（保守默认，宁可多走一次补漏重试，不静默交付可疑串）。
func hasLengthExplosion(src, out string) bool {
	sr := []rune(strings.TrimSpace(src))
	if len(sr) == 0 || len(sr) > shortGuardMaxSrcRunes() {
		return false // 长段落/空源文不适用本判据（交既有链处理）
	}
	return len([]rune(strings.TrimSpace(out))) > shortGuardLimit(len(sr))
}

// shortGuardLimit 短格译文长度上限：min(max(ratio×srcRunes, floor), absCap)。
// 三段式的用意：ratio 管「随源文缩放」、floor 给极短格留足展开空间（1 字段落译成一句长句合法）、
// absCap 兜底——即使阈值被调大，短格也绝不允许出现「数三百倍」的邮件级长串。
func shortGuardLimit(srcRunes int) int {
	n := shortGuardRatio() * srcRunes
	if f := shortGuardFloor(); n < f {
		n = f
	}
	if c := shortGuardAbsCap(); n > c {
		n = c
	}
	return n
}

// guardEnvInt 读取守卫阈值的 env 整数项：空/非法/非正一律回退默认，防止误配置把闸门关死。
func guardEnvInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// 以下四个阈值函数单独抽出是为了让单测可用 t.Setenv 精确验证「env 可配」这条约定。
// shortGuardMaxSrcRunes 短文本判定的「源文够短」阈值（ runes ）：源文不超过该长度即按短单口径走比率护栏。
func shortGuardMaxSrcRunes() int { return guardEnvInt("LC_GUARD_SHORT_MAX_SRC_RUNES", 12) }

// shortGuardRatio 短文本长度膨胀比率上限：译文长度超过源文该倍数即判为可疑（长文本另有绝对上限）。
func shortGuardRatio() int { return guardEnvInt("LC_GUARD_SHORT_RATIO", 8) }

// shortGuardFloor 比率护栏的豁免下限（runes）：源文本就短于此值时比率天然虚高，不看比率、只看绝对上限。
func shortGuardFloor() int { return guardEnvInt("LC_GUARD_SHORT_FLOOR_RUNES", 80) }

// shortGuardAbsCap 短文本绝对长度上限（runes）：译文超过该绝对长度直接判爆，兜住比率法漏掉的中等长度源文。
func shortGuardAbsCap() int { return guardEnvInt("LC_GUARD_SHORT_ABS_CAP_RUNES", 300) }

// LengthExplosionInfo 供日志/告警用的长度比描述（provider/model 由调用侧补齐）。
// 命中 hasLengthExplosion 的槽位调用它，把「源 r 字 → 译 r 字（倍数 x，上限 y）」写成一行可聚合的哨兵字段。
func LengthExplosionInfo(src, out string) string {
	sr := len([]rune(strings.TrimSpace(src)))
	or := len([]rune(strings.TrimSpace(out)))
	ratio := 0.0
	if sr > 0 {
		ratio = float64(or) / float64(sr)
	}
	return fmt.Sprintf("src_runes=%d out_runes=%d ratio=%.1f limit=%d", sr, or, ratio, shortGuardLimit(sr))
}

// collectKBPass 汇总 KB 第一遍逐段直配的并行结果，决定「算命中并写入」还是「进模型补漏队列」。
// 参数：texts=有序源文段；kbHitIdx[i]=该段 KB 调用是否返回了内容；kbVal[i]=候选译文；
// preFilledSeg=「已预填交付、不进上游」的段（S8 敏感拦截段 ∪ 无需翻译段，见 HandleFile 的
// preFilled）——这些段既不算命中也不进补漏。
// 返回 accepted（可用命中的段号）与 needModelIdx（需送模型的段号，保持提取顺序）。
//
// ★ 判定必须走 IsTranslationUsable，**不能只判非空**：KB 脏行（源文=译文）会把源文
// 当译文命中，一旦写进 langTranslations，硬闸「按键存在判定是否译出」就永久跳过该段
// ——不重试、不计 untranslated、不告警（详见本文件头）。此处即使上游 goroutine 已判过
// 也再判一次：写入点与命中点分离（并行）时，以**写入前**的判定为准。
func collectKBPass(texts []string, kbHitIdx []bool, kbVal []string, preFilledSeg map[string]bool) (accepted []int, needModelIdx []int) {
	for i := range texts {
		if i < len(kbHitIdx) && kbHitIdx[i] && i < len(kbVal) && IsTranslationUsable(texts[i], kbVal[i]) {
			accepted = append(accepted, i)
			continue
		}
		if preFilledSeg[texts[i]] {
			continue // ★ 已预填（S8 占位 / 无需翻译的原文），不入模型补漏
		}
		needModelIdx = append(needModelIdx, i)
	}
	return accepted, needModelIdx
}

// missingSegments 按提取顺序返回该语言**尚未有可交付译文**的源文段。
// 与 IsTranslationUsable 配对使用：不可用译文一律不写入 ⇒ 「键存在」严格等价于「已译出」，
// 于是硬闸重试、untranslated 计数与未译段清单三者共用同一份真相（P0 整改的落点）。
func missingSegments(texts []string, tr map[string]string) []string {
	missing := []string{}
	for _, t := range texts {
		if _, ok := tr[t]; !ok {
			missing = append(missing, t)
		}
	}
	return missing
}

// ============ 2026-10-17 第二批：非语言段与「谈论词条本身」两类脏译文的判据 ============
// 工单 T20261017234421W7H（交易看板截图 · pro · restore · en）实测交付物：64 段里 24 段是模型噪声——
// 数字/货币/日期/时间行被整排译成 "and"（`0`/`0.00`/`4716`/`¥1003.00`/`09-17 09:40:33`…），
// 符号行 `<` 被译成一整句英文方法论，`manual` 被译成
// 「The original term "manual" remains unchanged in the translation.」这类**谈论这个词本身**的
// 解释句，`manua` 被译成 `` ` and ` ``。这些串全部通过了旧判据（非空、与源文不同、无尖括号、
// 长度不爆炸）——旧判据只问「像不像一份译文」，没问「这一段**本来需不需要翻译**」
// 「译文的信息量是不是比源段还少」。故新增三条**结构**判据（不引入任何短语黑名单，措辞无关）：
//  ① nonLinguisticMustStayVerbatim —— 不需要翻译的段只能原样交付；
//  ② degenerateToFunctionWord        —— 源段是实义词，译文却整体退化成一个孤立功能词；
//  ③ quotedVerbatimSourceExplained   —— 译文把源段逐字引号引用再另起一句解释。
// 与 ① 配对的是文件主链的**预填**（identitySegments）：那些段根本不进上游，也不计未译出。
// ============================================================================

// segmentNeedsTranslation 判定「这一段是否需要模型翻译」——按**字符类别**判，不按词表判。
// 含「非 ASCII 字母」（汉/假名/西里尔/阿拉伯/泰文…）或「≥2 连排 ASCII 字母」⇒ 需要翻译；
// 其余（纯数字、货币、日期时间、标点、单字母、符号）⇒ 不需要。
// 为什么按类别切：「0」「¥1003.00」「09-17 09:40:33」在任何语言里都是同一串字符，
// 模型对它们的输出只可能是幻觉（本工单里整排变成 "and"），回落原文才是正确交付。
// ★ 本函数有**两个消费者**，必须同源：IsTranslationUsable 的第 6 判据（拒绝模型噪声）
//
//	与文件主链的预填交付（NeedsIdentityDelivery，让这些段根本不进上游）。
func segmentNeedsTranslation(src string) bool {
	asciiRun := 0
	for _, r := range strings.TrimSpace(src) {
		switch {
		case r > unicode.MaxASCII:
			if unicode.IsLetter(r) {
				return true
			}
			asciiRun = 0
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			asciiRun++
			if asciiRun >= 2 {
				return true
			}
		default:
			asciiRun = 0
		}
	}
	return false
}

// NeedsIdentityDelivery 「这一段的交付形态就是原文本身」（数字/货币/日期/时间/符号/单字母）。
// 文件主链据此预填原文（见 identitySegments）：这些段不送上游、不进补漏、不计未译出。
func NeedsIdentityDelivery(src string) bool { return !segmentNeedsTranslation(src) }

// nonLinguisticMustStayVerbatim 判据 ①：不需要翻译的段，交付值只能是它自己（折叠空白后等值）。
// 为什么方向是「回落原文」而不是「丢弃该段」：这些段本身就是要交付的内容
// （金额、时间戳、代码），原文才是唯一正确形态；丢段会让成品缺格。
func nonLinguisticMustStayVerbatim(src, out string) bool {
	if segmentNeedsTranslation(src) {
		return false
	}
	return normalizeForCompare(src) != normalizeForCompare(out)
}

// functionWordNoise 封闭的功能词清单（冠词/连词/介词/系动词 + 各目标语最常见的连接词）。
// 只服务判据 ②「源段有实义，译文却整体退化成一个连接词」这一种形态，不参与任何其它判据——
// 因此它不是本项目明确否掉的那类「短语黑名单」（后者随目标语言与模型措辞线性膨胀、永远补不全）。
// ★ 刻意不收**假名**（の/は/が…）：单假名段在 OCR 里几乎只来自图标/开关误识（本工单的 `の`
//
//	就被译成 "and"），收进来等于给这一类噪声发通行证。
var functionWordNoise = map[string]bool{
	// 英语
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true, "to": true,
	"in": true, "on": true, "at": true, "by": true, "for": true, "with": true, "as": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true, "it": true,
	"this": true, "that": true, "there": true, "here": true, "but": true, "so": true, "if": true,
	"not": true, "no": true, "yes": true, "from": true,
	// 西/葡/法/意/德
	"y": true, "e": true, "o": true, "de": true, "del": true, "la": true, "el": true, "los": true,
	"las": true, "un": true, "una": true, "en": true, "con": true, "por": true, "para": true,
	"es": true, "son": true, "et": true, "ou": true, "le": true, "les": true, "des": true,
	"du": true, "avec": true, "est": true, "sont": true, "und": true, "oder": true, "der": true,
	"die": true, "das": true, "ein": true, "eine": true, "von": true, "zu": true, "im": true,
	"mit": true, "für": true, "ist": true, "sind": true, "di": true, "il": true,
	// 俄/阿/泰/越/印尼
	"и": true, "или": true, "в": true, "во": true, "на": true, "с": true, "со": true,
	"و": true, "في": true, "من": true, "على": true,
	"และ": true, "หรือ": true, "và": true, "hoặc": true, "của": true,
	"dan": true, "atau": true, "yang": true,
	// 韩（助词）
	"과": true, "와": true, "의": true, "은": true, "는": true,
	// 中
	"和": true, "与": true, "及": true, "的": true, "是": true, "在": true, "有": true,
	"也": true, "而": true, "就": true, "都": true, "或": true, "并": true, "则": true,
}

// trimDecor 剥掉字符串两端的装饰字符（引号/反引号/括号/标点/空白）。
// 模型常把孤立的词包装成 “ ` and ` “、`"and"` 这类形态，判据要按**内容**判而非按包装判。
func trimDecor(s string) string {
	return strings.Trim(s, "`*_\"'“”‘’()[]{}<>,.;:!?—-–—…·、。，；：！？（）【】「」『』 \t\n\u3000")
}

// bareFunctionWord 译文（去装饰后）是不是一个孤立功能词（"and"/"the"/"和"…）；是则回该词，否则回空。
func bareFunctionWord(out string) string {
	s := trimDecor(out)
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return ""
	}
	if functionWordNoise[strings.ToLower(s)] {
		return s
	}
	return ""
}

// degenerateToFunctionWord 判据 ②：源段是实义词（见 segmentNeedsTranslation），
// 译文却整体退化成一个孤立功能词（实测 `manua` → “ ` and ` “、"0" → "and"、"の" → "and"）。
// 结构不变量：源段有实义而译文只剩一个连接词 ⇒ 信息量比源段还少，不可能是翻译，
// 只能是模型对「无上下文短串」的兜底噪声。
// ★ 放行条件（防误杀正译）：**源段本身就是功能词**时不做本判据——"和"→"and"、"and"→"y" 是正译。
func degenerateToFunctionWord(src, out string) bool {
	if bareFunctionWord(out) == "" {
		return false
	}
	if !segmentNeedsTranslation(src) {
		return false // 非语言段走判据 ①（那里要求原样交付，比本判据更严）
	}
	return !functionWordNoise[strings.ToLower(trimDecor(src))]
}

// quotePairs 译文里「把源段逐字引号引用」的引号对（中西文各一套）。
var quotePairs = [][2]string{
	{`"`, `"`}, {"“", "”"}, {`'`, `'`}, {"‘", "’"}, {"「", "」"}, {"『", "』"}, {"《", "》"},
}

// quotedVerbatimSourceExplained 判据 ③：模型不翻译、而是**谈论这一段**。
// 实测形态：源段 `manual` → `manual  \nThe original term "manual" remains unchanged in the
// translation.`（把源段逐字引起来，再用一整句解释它）。
// 结构不变量：合法译文不会「引号 + 源段逐字 + 引号」之后还挂着一整句话——出现即是在解释词条本身。
// 判据刻意只认「译文里**逐字**出现被引号包裹的源段」；源段被改写后再解释（旁白式）由对话侧
// 的清洗链负责，本函数不猜，宁可漏判。
func quotedVerbatimSourceExplained(src, out string) bool {
	s := strings.TrimSpace(src)
	if s == "" {
		return false
	}
	for _, q := range quotePairs {
		if !strings.Contains(out, q[0]) {
			continue
		}
		span := q[0] + s + q[1]
		i := strings.Index(out, span)
		if i < 0 {
			continue
		}
		if letterishCount(out[:i]+out[i+len(span):]) >= quotedExplainMinLetters {
			return true
		}
	}
	return false
}

// quotedExplainMinLetters 「引号外的解释句」判定阈值（字母/数字字符数）。
// 用字符数而不是词数：中文没有空格，按词数会把中文解释句判成 1 个词（漏判）；
// 而 8 对「The original term remains unchanged in the translation.」这类英文解释句绰绰有余，
// 对「引号里就是源文、引号外空无一物」的极简包装（`"manual"`）则不会触发。
const quotedExplainMinLetters = 8

// letterishCount 统计「字母或数字」字符数（任何文字系统都算）。
func letterishCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

// identityDelivery 「无需翻译」段的交付值＝原文本身。
// **单列成一个函数**是为了让装配扫描器（file_writepoints_guard_test.go）能把「预填即原文」
// 这一豁免形态按名字认出来——否则下一个人看不出这里那处裸 addTrans 是设计内还是漏判。
func identityDelivery(src string) string { return src }

// identitySegments 汇总「交付形态就是原文本身」的段（NeedsIdentityDelivery 为真），
// 并为每种目标语言预填原文；返回值供调用方并入「不进上游」的段集合（见 HandleFile）。
// 三个目的：① 不送上游（数字/日期送翻只换来 "and" 这类幻觉，白花时间与积分）；
// ② 不进硬闸补漏（键存在即已译出，与 missingSegments 同口径）；③ 不计未译出告警
// ——否则每份含数字/日期的文档都会拖着一条「未译段」告警，而交付物其实完全正确。
// ★ 与 sensitiveBlockedSegments 同为「预填豁免」：这里写的是**原文**（不是占位符），
//
//	它过不了 IsTranslationUsable 的「同文」判据是设计内的（那条判据针对的是**模型回显**）。
func identitySegments(texts []string, langs []string, addTrans func(lc, orig, tr string)) map[string]bool {
	identity := map[string]bool{}
	for _, t := range texts {
		if identity[t] || !NeedsIdentityDelivery(t) {
			continue
		}
		identity[t] = true
		for _, lc := range langs {
			addTrans(lc, t, identityDelivery(t))
		}
	}
	return identity
}

// mergeSegSets 合并多个「段集合」（敏感拦截 / 无需翻译），供模型派发只看一次。
func mergeSegSets(sets ...map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range sets {
		for k := range s {
			out[k] = true
		}
	}
	return out
}
