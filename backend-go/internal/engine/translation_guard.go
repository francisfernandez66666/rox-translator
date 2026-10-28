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

	"translator/internal/textscript"
)

// translationFailureMark BatchTranslate 在失败槽位写入的占位字面量（见 engine.go 批量链）。
// 集中成常量是为了让「失败占位」与「真译文」的区分只有一处定义，不再各调用点抄字符串。
const translationFailureMark = "[翻译失败]"

// IsFailurePlaceholder 判定一份候选是不是「上游压根没吐内容」那一档（空串／批量失败占位）。
// 为什么单独导出：调用方要把「上游断了」与「译出来了但形态不合格」分开记账——
// 前者重试一万次也不会好（文本工单逐行链据此早停），后者才值得再补译几轮。
// ★ 只认这两类，不认「回显／拒译」：那些是有内容的坏答案，判据在 IsTranslationUsable 那一侧。
func IsFailurePlaceholder(out string) bool {
	o := strings.TrimSpace(out)
	return o == "" || o == translationFailureMark
}

// RenderSafeTranslations 把「占位符值」从图片原位回填的映射里摘掉，让 fileproc 的空串分支把**原文**画上图。
//
// ★ 2026-10-28 图片版式回退批立：交付图上绝不允许出现占位符字面量。三态一律摘除——
//
//	空串／`[翻译失败]`（上游失败槽）／`[已拦截·REDACTED]`（敏感词拦截段）。
//	图上画原文＝"这一行保持原样"，客户看得见、也认得出；画占位符＝把内部记号交给客户
//	（AGENTS §19 族一：兜底形态一律「回落原文＋点名」，点名发生在 .md 与工单读数，不在图上）。
//	为什么收在 engine 而不是 fileproc 自判：两份名单（translationFailureMark／SensitivePlaceholderText）
//	都住在 engine，而 import 方向是 fileproc ← engine——在 fileproc 里再抄一遍字面量
//	就是 §一·14 那一族「清单长出第二份」，迟早打架。
//	摘除后该键**缺席**（不是写空串）：fileproc 侧 `tr[b.Text]` 取零值即走「保留原文」那一支，行为与空串同形。
func RenderSafeTranslations(tr map[string]string) map[string]string {
	out := make(map[string]string, len(tr))
	for src, v := range tr {
		s := strings.TrimSpace(v)
		if s == "" || IsFailurePlaceholder(s) || s == SensitivePlaceholderText {
			continue
		}
		out[src] = v
	}
	return out
}

// IsTranslationUsable 判定一份候选译文能否作为交付（文件主链 5 个写入点的唯一口径）。
// 参数 src: 送翻的源文段；out: 候选译文（KB 命中值或模型返回，未经判定前的原始候选）。
// 判不可用的九类，任一命中即 false：
//  1. 空串 / 纯空白 —— 没有内容可言交付；
//  2. 批量失败占位 [翻译失败] —— 上游明确报错的槽位；
//  3. 与源文同文（去空白后逐字相等）—— 模型或 KB 原样回显 = 根本没翻（含 "#"/"1"/"•"
//     这类无上下文短串的回显，与既有模型路径口径一致，宁可走补漏也不静默交付）；
//     ★ 例外见判据 3′（2026-10-19 工单 107）：源段**没有任何非 ASCII 字母**时，
//     回吐即原文交付形态，判可用（这类段无中文字面可泄露，告警是假的）。
//  4. 指令回显残留 —— 见 hasInstructionEcho 的结构指纹；
//  5. 短格长度爆炸 —— 见 hasLengthExplosion（★ F-38，2026-09-25 批 D：6 字表格单元格被
//     翻成 2100 字符的整封英文邮件，前四判据全放行、直接进交付物）；
//  6. ★ 2026-10-17 第二批：**不需要翻译的段**（数字/货币/日期/时间/符号）被译成了别的字符串
//     —— 见 nonLinguisticMustStayVerbatim（工单 T20261017234421W7H：整排 "and"）；
//  7. ★ 同批：源段是实义词，译文却整体退化成一个孤立功能词 —— 见 degenerateToFunctionWord；
//  8. ★ 同批：译文把源段逐字引号引用再另起一句解释 —— 见 quotedVerbatimSourceExplained。
//  9. ★ 2026-10-21（UAT M1）：模型整段回绝/答非所问（"I am unable to provide the translation
//     you requested"）—— 见 refusalResidue。命中后该段回落原文交付并出声（GateWarnings）。
//
// ★ 不能只判非空：这是本文件存在的理由，任何新增写入点都必须先过本函数。
func IsTranslationUsable(src, out string) bool {
	o := strings.TrimSpace(out)
	if o == "" || o == translationFailureMark {
		return false
	}
	if normalizeForCompare(src) == normalizeForCompare(out) {
		// ★ 判据 3′（★ 2026-10-19 工单 107 收口；用户令「无中文源回吐即视为原文交付」）：
		//   **源段没有任何非 ASCII 字母**时，原样回吐就是它的正确交付形态，不记"未译出"。
		//   这类段（股票代码 002584.SZ / 标识符 N_shape·dragon_return·fac_1·manua / 纯数字符号）
		//   在主链本该由 identitySegments 预填、根本不进上游；只因 segmentNeedsTranslation 的
		//   「≥2 连排 ASCII 字母」把标识符也判成"要翻"，它们才落到写入判定这里。
		//   ⚠️ 交付字节与「判不可用⇒回落原文」**完全一致**——少掉的只是一条假告警
		//      （现网工单 107：12 条未译段里 9 条是这种无中文碎片，每份含编号的文档都会背一条）。
		//   ⚠️ 反之**有**非 ASCII 字母时仍按原样判不可用：那正是「模型把中文原封不动吐回来」
		//      的泄露形态（本工单 `金额`、`当前：全部允许·仓位留空/0 =使用全局单票金额` 即此档），
		//      绝不能在此放行——它是本函数存在的理由。
		// ★ 2026-10-28（主方案 §10.2）：交付形态就是原文本身的段（数字/符号/单假名/代码短标识），
		//   同文即正确交付。与 identitySegments 的预填同源（同一个 NeedsIdentityDelivery），不另立判据。
		//   不补这支会出现**两把尺子互相否决**：预填说"这段交付原文"，判据说"同文不可用"，
		//   于是图片链每一轮补译都被同一判据否掉，最终把「の」写成"未译出"（本单 T20261027214604CXY）。
		return !sourceHasTranslatableLetters(src) || NeedsIdentityDelivery(src)
	}
	// ★ 判据 3″（2026-10-27 工单 136 复盘）：**中文回显被后处理洗成符号骨架**。
	// 必须紧跟判据 3：它正是判据 3 被"后处理发生在判定之前"绕过之后留下的那个洞。
	if cjkEchoWashedToSkeleton(src, out) {
		return false
	}
	if hasInstructionEcho(src, out) {
		return false
	}
	// ★ 判据 9（2026-10-21 UAT 收口 M1）：模型「拒译/答非所问」——
	//   现网五件交付物全叫 "I am unable to provide the translation you requested"，
	//   旧八类判据逐条放行（详见本文件下方 refusalResidue 的注释）。
	if refusalResidue(src, out) {
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

// sourceHasTranslatableLetters 源段里是否存在「某种语言的文字」：任一**非 ASCII 字母**
// （汉字/假名/谚文/西里尔/阿拉伯/泰文/带重音的拉丁…）即视为有可译内容。
// 为什么用「非 ASCII 字母」而不是「汉字」：文件管线也吃非中文源（en→de 等），重音拉丁同样是文字；
// 而纯 ASCII 串（编号、代码、单字母）在任何语言里都是同一串字符。
// ★ 与 segmentNeedsTranslation 的同一半句**同一把尺子**（`r > unicode.MaxASCII && unicode.IsLetter(r)`），
//
//	别在这里另立一套字符分类（两套尺子迟早打架）。
func sourceHasTranslatableLetters(src string) bool {
	for _, r := range src {
		if r > unicode.MaxASCII && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// cjkEchoWashedToSkeleton 判据 3″：**中文回显被 StripChineseInNonZh 洗成符号骨架**。
//
// 现网形态（工单 T20261027152356CMY 的邻座缺陷，本地实测复现）：
//
//	模型把源文原样吐回 —— `已实现=加权成本重放；浮动=市值-成本×数量；30s刷新`；
//	PostProcessTranslation 的 StripChineseInNonZh 把汉字全删掉，剩 `=；=-×；30s`。
//
// 此时判据 3（同文）**已经比过了**：它比的是洗之前那份原文，两边逐字相等；而写入点拿到的
// 是洗完的骨架，字面已不同文，判据 4~9 也一条都不命中 ⇒ **判可用**。
// 于是一段"压根没翻"的东西被当成译文写进交付物——客户看到 `=；=-×；30s`，
// 比看到中文更莫名其妙（至少中文他一眼知道没翻）。交付 md 第 39 行末尾那串
// `：；""0，。`（源文「当前：全部允许…；"0" 。」的标点骨架）就是这条通道的指纹。
//
// 判据：源文有可译文字，译文却**没有任何连续两个字母** ⇒ 这段没有承载任何词语，
// 只能是"中文被删干净后剩下的标点/数字骨架"。
// 为什么是「连续两个字母」而不是「字母个数」：单字母（`30s` 的 s、`A=1` 的 A）不构成一个词，
// 而任何语言的最小实义词都在两个字母以上；阈值 2 与 segmentNeedsTranslation 的
// 「≥2 连排 ASCII 字母」同一把尺子——那边判"要不要翻"，这边判"翻了没有"。
// 为什么对 CJK 目标语天然安全：out 是汉字/假名/谚文时它们本身就是 Letter，连续两个即放行；
// 而 `StripChineseInNonZh` 对 zh/zh_hant/ja/ko 根本不删，走不到这条。
// 反向保护：源文无中文（`0`／`¥1003.00`／`002584.SZ`／`manua`）时整条不适用——
// 那些段的正确交付就是它自己，由判据 3′ 与判据 6 负责，这里不许多管一段。
func cjkEchoWashedToSkeleton(src, out string) bool {
	if !sourceHasTranslatableLetters(src) {
		return false
	}
	return !hasLetterRun(out, 2)
}

// hasLetterRun 译文里是否存在至少 n 个**连续字母**（任何书写系统：拉丁/西里尔/假名/谚文/阿拉伯…）。
// 用 unicode.IsLetter 而不是 [A-Za-z]：西里尔/泰文/阿拉伯等目标语的正常译文同样要能放行。
func hasLetterRun(s string, n int) bool {
	if n <= 0 {
		return true
	}
	run := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			run++
			if run >= n {
				return true
			}
			continue
		}
		run = 0
	}
	return false
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
// IsLengthExplosion 导出「短格译文长度爆炸」这一档的判据（与内部写入点判定同一把尺子，不另立第二把）。
// 为什么需要导出：调用方要分得开**回落原文的成因**——
// 「上游压根没吐」（族一，重拨无用）／「被 max_tokens 截空」（族三，抬预算可救）／
// 「吐出一整封邮件那样的污染串」（族三，必须**单独重拨**而不是回同一批里再赌一次）。
// 三种成因的补救动作不一样，混成一味就会出现"该重拨的没重拨、不该重拨的白烧一万次"。
func IsLengthExplosion(src, out string) bool { return hasLengthExplosion(src, out) }

// hasLengthExplosion 「短格长度爆炸」判据本体（导出壳 IsLengthExplosion 之外，包内一律调这一个）。
// 参数：src=原文，out=候选译文；返回是否"短原文换来远超上限的输出"。
// 只在原文很短（≤shortGuardMaxSrcRunes）时启用：长段落本来就会出现译文比原文长的正常形态，
// 拿同一比例去判会把整篇合法译文误杀——这一档专治"6 个字的单元格吐出一整封邮件"。
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
func collectKBPass(texts []string, kbHitIdx []bool, kbVal []string, preFilledSeg map[string]bool, usable func(src, out string) bool) (accepted []int, needModelIdx []int) {
	for i := range texts {
		if i < len(kbHitIdx) && kbHitIdx[i] && i < len(kbVal) && usable(texts[i], kbVal[i]) {
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

// isLoneKana 单个假名（含半角片假名）——OCR 把开关/图标误识成假名的典型形态。
// 与 ocr_translate_image.py 的 _is_lone_nonascii 是同一现网实证（绿色开关被读成 `の`）；
// 文件链在这里就把它按原文预填，既不再送模型，也不会在图片回填时被重画。
func isLoneKana(s string) bool {
	r := []rune(strings.TrimSpace(s))
	if len(r) != 1 {
		return false
	}
	// 书写体系判据只有一份实现（internal/textscript）：区间不再在本文件手抄，
	// 扩展区/半角形式/Common 字符（ー・々…）的统一口径都在那个包里。
	// ★ 这条判据**不带文档画像**（主方案 §10.2 定稿）：画像在 fileproc、判据在 engine，
	//   穿透四层只为一件事不值得；"开关被误识成の"由 OCR 清洗层的语言无关腿收口。
	return textscript.Of(r[0]).Kana()
}

// isCodeLikeSegment 代码/编号类短段：股票代码（002584.SZ）、策略标识（N_shape/dragon_return/fac_1）、
// 版本号/编号（v1.2、A-1）以及 2~4 位大写字母代码（SZ/SH/USD/ROX）。
// 判据取向：只收「无空格、仅字母数字与 _/. 、且带数字或分隔符」的形状；
// 刻意不收连字符/斜杠：`line-00` 这类「单词+编号」仍按正文送翻，避免把正常行误判成代码。
// manual/dragon/Hello 这类普通单词不带数字或分隔符，仍按正常文本送翻（不误伤）。
func isCodeLikeSegment(s string) bool {
	t := strings.TrimSpace(s)
	r := []rune(t)
	if len(r) == 0 || len(r) > 32 {
		return false
	}
	hasDigit, hasSep, hasLetter := false, false, false
	for _, c := range r {
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			hasLetter = true
		case c == '_' || c == '.':
			hasSep = true
		default:
			return false
		}
	}
	if !hasLetter && !hasDigit {
		return false // 纯分隔符不是编号（交给 IsSymbolOnlyLine 那一档）
	}
	// 纯字母（含 AI/AWS/USD/ROX 这类短缩写）不在这里判代码：缩写本身可能就是有意义的
	// 文件名/正文（AWS→亚马逊云科技 是正译），必须继续送翻；只有带数字/分隔符的编号才预填。
	return hasDigit || hasSep
}

// segmentNeedsTranslation 判定「这一段是否需要模型翻译」——按**字符类别**判，不按词表判。
// 含「非 ASCII 字母」（汉/假名/西里尔/阿拉伯/泰文…）或「≥2 连排 ASCII 字母」⇒ 需要翻译；
// 其余（纯数字、货币、日期时间、标点、单字母、符号、代码/编号、单假名）⇒ 不需要。
// 为什么按类别切：「0」「¥1003.00」「09-17 09:40:33」在任何语言里都是同一串字符，
// 模型对它们的输出只可能是幻觉（本工单里整排变成 "and"），回落原文才是正确交付。
// ★ 本函数有**两个消费者**，必须同源：IsTranslationUsable 的第 6 判据（拒绝模型噪声）
//
//	与文件主链的预填交付（NeedsIdentityDelivery，让这些段根本不进上游）。
func segmentNeedsTranslation(src string) bool {
	// ★ 2026-10-21 UAT 收口 M9：先把「整行根本不是文字」这一档显式挡掉，判据收在
	//   `symbol_lines.go` 的唯一一把尺子上（IsSymbolOnlyLine：任何书写体系的字母都没有）。
	//   与下面按字符类别扫的结果等价（无字母 ⇒ 不会命中任何一个 true 分支），这样写是为了：
	//     ① 让共享常量表（graphicSymbolRunes/contentSymbolRunes）有一个真正的消费点，
	//        Python 侧的回填门与它同源，而不是各写一份「什么算符号」；
	//     ② 给未来加「图形符号整行剔除」的调用方一个可读的入口，不必再自己数码位。
	//   反证：把这一行改成 `if false` 必须**没有**任何既有断言变红（行为等价），
	//   而新增的符号表等值锁与回填门静态锁仍各自红在它该红的地方。
	if IsSymbolOnlyLine(src) {
		return false
	}
	// ★ 2026-10-26 用户令：无实义词的极短段原样贴回。单假名（OCR 开关误识别）与
	//   代码/编号（股票代码、策略标识、版本号、2~4 位大写短码）都不该烧模型调用，
	//   更不该因为模型回吐 `<target>…` 而进入硬闸重试链。
	if isLoneKana(src) || isCodeLikeSegment(src) {
		return false
	}
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

// ============ 2026-10-21（UAT 收口 M1）：拒译/答非所问的判据 ============
// 现网实证（发布前 E2E UAT 20261020，工单 07M5/07M4）：模型对整份文件只回一句
// "I am unable to provide the translation you requested"，旧八类判据逐条放行
// （非空、与源文不同文、不含凭空尖括号、长度不爆炸、源文是语言段、译文不是功能词、
// 也没有引号引用源文）⇒ 这句回绝同时进了**译文正文**与**产物文件名**（五件交付物同名）。
//
// 判据取向与 hasInstructionEcho 一致：**结构不变量优先，词表只作第一道快筛**——合法译文
// 不会在源文完全没有"翻译"字样的情况下，同时出现【回绝措辞】与【翻译二字】。
//
//	① paired 结构：译文含回绝措辞 且 含"翻译/translat" 且 **源文两者都没有**；
//	② n-gram 兜底：译文很短（≤260 rune）且 ≥60% 的 4-gram 落在回绝语料里，
//	   专治回绝话术换措辞（"I'm sorry, but I can't help with that translation"）。
//
// ★ 反向保护（防误杀正译）：源文自己就含回绝措辞或"翻译"字样时一律不判——本仓自己的
// 产品文档（"翻译服务不可用时…"）会被译成含 translat 的正常句子，不能当回绝拦掉。
var refusalPhrases = []string{
	// 英语：回绝某件事的固定说法（不收单独的 "sorry"——正常译文里出现它太常见）
	"unable to provide", "cannot provide", "can't provide", "not able to provide",
	"unable to translate", "cannot translate", "can't translate", "not able to translate",
	"unable to assist", "cannot assist", "can't assist", "not able to assist",
	"unable to comply", "cannot comply", "can't comply",
	"as an ai language model", "as a language model",
	"i'm sorry, but", "i am sorry, but", "i apologize, but",
	// 中文：回绝/推脱的成对说法（不收裸"无法/抱歉"——它们在正常正文里太常见）
	"无法提供", "不能提供", "无法翻译", "不能翻译", "无法完成", "不能完成",
	"无法协助", "不能协助", "抱歉，", "很抱歉，", "对不起，",
}

// refusalTaskWords 判定"这段文本在谈翻译这件事"的词根。用词根而不是整词，
// 让 translate/translation/translating/translations 只需要一条。
var refusalTaskWords = []string{"translat", "翻译", "翻訳", "번역", "перевод", "traducc", "traduc", "übersetz", "ترجم"}

// refusalCanonicalCorpus 回绝话术的**极窄**语料（只用来算字符 4-gram 覆盖率，
// 不做逐句匹配）。每条都是现网/常见模型真实回绝的形态，改动这一份要同步看
// TestIsTranslationUsableRejectsRefusalResidue 的正反腿。
var refusalCanonicalCorpus = []string{
	"i am unable to provide the translation you requested",
	"i am unable to provide a translation for this content",
	"i cannot provide a translation of this content",
	"i cannot translate this text",
	"i can't translate this text",
	"i'm sorry, but i cannot help with that translation",
	"i'm sorry, but i can't help with that translation",
	"as an ai language model, i cannot translate this content",
	"抱歉，我无法提供您要求的翻译",
	"很抱歉，我无法完成这个翻译",
	"无法提供这段文字的翻译",
}

// refusalGramSet 回绝语料的字符 4-gram 集合（包级初始化一次；语料是常量，无并发写）。
var refusalGramSet = buildRefusalGramSet()

// buildRefusalGramSet 把回绝语料拆成 4-gram 集合。用 rune 而不是 byte：
// 中文一字 3 字节，按字节切片会把一个汉字劈成两半，覆盖率读数失去意义。
func buildRefusalGramSet() map[string]bool {
	set := map[string]bool{}
	for _, line := range refusalCanonicalCorpus {
		r := []rune(strings.ToLower(normalizeForCompare(line)))
		for i := 0; i+refusalGramN <= len(r); i++ {
			set[string(r[i:i+refusalGramN])] = true
		}
	}
	return set
}

// refusalGramN 字符 n-gram 的长度。取 4：比 3 少误伤、比 5 更耐措辞微调。
const refusalGramN = 4

// deliveryTranslations 交付面最后一道同名清洗：把任何一段值里的**模型回绝整句**剥掉。
// 参数 langTr=原文段 → 该语言译文。返回一份新 map（不改写入侧那份）。
//
// ★ 为什么在采纳判据之外还要这一道（2026-10-26 用户裁决八-3b：
//
//	「风控拒译——改不拒绝，但**文档第一页和文件名写名请慎重核对**，我们不负责任」）：
//	交付面有四个口都会吐出这一句——正文/首页首行（.md 第一段）、xlsx 单元格、
//	docx/PDF 首页、产物文件名。现网 M1 那一次的形态是**五件交付物同名**
//	`I am unable to provide the translation you requested_en.*`：一处没拦就是整批成品都带着它。
//	采纳判据（refusalResidue）在第一道拦，这里按同一把尺子**逐句**再核一次——
//	取向是"宁可多剥一句，不许把回绝话交给客户"，但**剥不掉就按原文交付、绝不留白**。
//	误杀风险由源文豁免守住：客户自己的文档里就在谈"无法提供翻译服务"时，
//	那句照译是合法正文（refusalResidue 已按该段原文判过一次，同一豁免条件在这里照用）。
//
// normalizeImageTranslations 图片件「单行源文 → 单行译文」收敛（★ 2026-10-29 用户实证）。
//
// 现网形态：某段源文是单行 `manual`，模型回了 `"manual  \nManual"`，原位回填把两行画进
// **同一个框** ⇒ 客户看到同一个词翻了两遍（"manual / Manual"），.md 里也成了两行。
// 图片件每个框按构造就是**一行**，所以对单行源文做收敛：
//
//	· 各行去空白后忽略大小写相同（回声/重复）⇒ 只留最后一行（模型通常把译文放在后面）；
//	· 其余多行 ⇒ 用单个空格连接成一行（换行在这里没有语义，只有"画进一个框"的后果）；
//	· 去空白后为空 ⇒ 原样返回（交给下游的占位符/原文回落判据，不在这里造空串）。
//
// 多行源文（扫描件 PDF 的段落）**不动**——那里的换行是源文自己的结构。
// 只在图片链调用（`HandleFile` 的 image 分支），别搬到通用交付路径：
// docx/xlsx 的段级换行有各自的回写语义。
func normalizeImageTranslations(tr map[string]string) map[string]string {
	if len(tr) == 0 {
		return tr
	}
	out := make(map[string]string, len(tr))
	for src, v := range tr {
		if strings.Contains(src, "\n") {
			out[src] = v
			continue
		}
		lines := strings.Split(v, "\n")
		kept := make([]string, 0, len(lines))
		for _, ln := range lines {
			if s := strings.TrimSpace(ln); s != "" {
				kept = append(kept, s)
			}
		}
		switch {
		case len(kept) == 0:
			out[src] = v
		case len(kept) == 1:
			out[src] = kept[0]
		case sameFoldAll(kept):
			out[src] = kept[len(kept)-1]
		default:
			out[src] = strings.Join(kept, " ")
		}
	}
	return out
}

// sameFoldAll 各行忽略大小写是否完全相同（`manual`／`Manual` 这种回声-译文双行）。
func sameFoldAll(lines []string) bool {
	for i := 1; i < len(lines); i++ {
		if !strings.EqualFold(lines[0], lines[i]) {
			return false
		}
	}
	return true
}

// deliveryTranslations 交付前统一过一遍回绝话术剥离（采纳判据之外的第二道，口径见 stripRefusalSentences）。
func deliveryTranslations(langTr map[string]string) map[string]string {
	if len(langTr) == 0 {
		return langTr
	}
	out := make(map[string]string, len(langTr))
	for src, v := range langTr {
		out[src] = stripRefusalSentences(src, v)
	}
	return out
}

// stripRefusalSentences 从一段译文里剥掉**开头的**模型回绝整句，保留其后的真实译文。
// 参数：src=该段原文（豁免判据要它），out=候选译文。
// 三步：① 整段判不出回绝 ⇒ 原样返回（绝大多数交付值走这一条，零开销）；
// ② 反复剥"开头那一句"（句末符 = . 。 ! ！ ? ？ ; ； 换行），每句都用**同一把**
//
//	refusalResidue 配该段原文判（源文自己谈这件事时豁免照旧生效）；
//
// ③ 剥空 ⇒ 返回该段**原文**（不留白；把回绝话交给客户比把原文交给客户更坏，用户令①同口径）。
//
// ★ 为什么这里自己走一遍句末，而不是复用句界切分器 `splitSentenceRaw`：
//
//	那个切分器按既有口径**不把英文句号「.」当切分点**（对话链刻意如此——缩写/序号满天飞）。
//	而现网 M1 那句回绝话是**英文**且以句号收尾："I am unable to provide the translation you requested."，
//	借它切就永远切不开，混合段会被整段判死、后面那半句正常译文跟着陪葬——
//	那正是本函数要避免的"砍多了"。句界的**判据**（哪些字符算句末）在这里显式列出来，
//	与对话链各自服务于自己的动作，不是同一件事的两把尺子。
func stripRefusalSentences(src, out string) string {
	if strings.TrimSpace(out) == "" {
		return out
	}
	if !refusalResidue(src, out) {
		return out // 绝大多数交付值一步走过去，零改动零开销
	}
	rest := out
	for {
		lead, tail, hasEnd := firstSentenceOf(rest)
		if !refusalResidue(src, lead) {
			break // 开头这一句是正常译文 ⇒ 停手，其后一个字都不动（只砍模型多出来的）
		}
		if !hasEnd {
			rest = "" // 整段没有句末且这一句就是回绝 ⇒ 全剥（M1 现网那句正是这一形态）
			break
		}
		rest = strings.TrimLeft(tail, " \t\n")
		if strings.TrimSpace(rest) == "" {
			break
		}
	}
	if strings.TrimSpace(rest) == "" {
		return strings.TrimSpace(src) // 剥空 ⇒ 回落原文交付，绝不留白（用户令①）
	}
	return rest
}

// firstSentenceOf 取开头的整句（含句末符）与其后的剩余部分。
// 参数：s=待切文本；返回 lead=第一句（含句末符）、tail=其余、hasEnd=是否找到句末符。
// 句末符集合显式列在 isRefusalTerminator 里（剥回绝话专用，不借对话链那套句界——
// 那个切分器刻意不把英文「.」当句末，见 stripRefusalSentences 上方那段）。
// 缩写/编号保护：大写字母后的「.」（"U.S."、"Dr."）与数字后的「.」都不算句末——
// 宁可少剥一句，也不许把 "The value is 2.0 million." 从中间切开。
func firstSentenceOf(s string) (lead, tail string, hasEnd bool) {
	r := []rune(s)
	for i, ch := range r {
		if !isRefusalTerminator(ch) {
			continue
		}
		if ch == '.' && i > 0 {
			prev := r[i-1]
			if (prev >= 'A' && prev <= 'Z') || (prev == ' ' && i > 1 && r[i-2] >= 'A' && r[i-2] <= 'Z') {
				continue
			}
			if prev >= '0' && prev <= '9' {
				continue
			}
		}
		return string(r[:i+1]), string(r[i+1:]), true
	}
	return s, "", false
}

// isRefusalTerminator 这一批字符算句末（剥回绝话专用；见 stripRefusalSentences 那条"为什么不复用切分器"）。
func isRefusalTerminator(ch rune) bool {
	switch ch {
	case '.', '。', '!', '！', '?', '？', ';', '；', '\n':
		return true
	}
	return false
}

// refusalResidue 判据 9：候选译文是模型「拒译/答非所问」的一段话。
func refusalResidue(src, out string) bool {
	o := strings.ToLower(normalizeForCompare(out))
	if o == "" {
		return false
	}
	s := strings.ToLower(normalizeForCompare(src))
	// ★ 源文自身就含回绝措辞或"翻译"词根 ⇒ 整条判据豁免（两条腿都豁免）：
	//   合法译文完全可能照译一句"不能提供该服务"、也完全可能谈"翻译服务维护中"。
	if sourceExemptFromRefusal(s) {
		return false
	}
	if refusalPairedStructure(o) {
		return true
	}
	return refusalNGramCovered(o)
}

// sourceExemptFromRefusal 源文本身是否谈到了「回绝」或「翻译」。
// 两者任一出现 ⇒ 候选译文里的相关字样都是**照译**而不是回绝，必须放行。
func sourceExemptFromRefusal(srcLower string) bool {
	for _, w := range refusalTaskWords {
		if strings.Contains(srcLower, w) {
			return true
		}
	}
	for _, p := range refusalPhrases {
		if strings.Contains(srcLower, p) {
			return true
		}
	}
	return false
}

// refusalPairedStructure 判据 9①：回绝措辞 + "翻译"词根同时出现。
// 参数 outLower 是**已小写、已折叠空白**的译文（源文豁免由 refusalResidue 先做）。
func refusalPairedStructure(outLower string) bool {
	hasTask := false
	for _, w := range refusalTaskWords {
		if strings.Contains(outLower, w) {
			hasTask = true
			break
		}
	}
	if !hasTask {
		return false
	}
	for _, p := range refusalPhrases {
		if strings.Contains(outLower, p) {
			return true
		}
	}
	return false
}

// refusalNGramCovered 判据 9②：短译文里 ≥60% 的 4-gram 落在回绝语料里。
// 长度上限 260 rune：回绝都是一两句话；放开上限会让一段很长、恰好谈"无法提供"的
// 正常译文被覆盖率误判（长文里零星命中几个 gram 就会被稀释，反而不会误判，但上限
// 同时也挡住了"拿一段长文包住回绝句"的逃逸，两个方向都更稳）。
func refusalNGramCovered(outLower string) bool {
	r := []rune(outLower)
	if len(r) < 8 || len(r) > 260 {
		return false
	}
	hits, total := 0, 0
	for i := 0; i+refusalGramN <= len(r); i++ {
		total++
		if refusalGramSet[string(r[i:i+refusalGramN])] {
			hits++
		}
	}
	return total > 0 && float64(hits)/float64(total) >= 0.6
}

// identityDelivery 「无需翻译」段的交付值＝原文本身。
// **单列成一个函数**是为了让装配扫描器（file_writepoints_guard_test.go）能把「预填即原文」
// 这一豁免形态按名字认出来——否则下一个人看不出这里那处裸 addTrans 是设计内还是漏判。
func identityDelivery(src string) string { return src }

// identityPrefillMarker —— 「无需翻译段按原文预填交付」这一形态在二进制里的**字面证据**。
// 为什么要有它（口径同 cmd/server/listen_systemd.go 的 sdTakeoverMarker，2026-10-18 真踩）：
// 发布件是 `-ldflags="-s -w"` 编出来的，而"函数名留不留在件里"**不可依赖**——同一棵树同一个件，
// `degenerateToFunctionWord` 命中 1、`identityDelivery` 命中 0（后者被内联后不进名字表）。
// 于是"编的是不是本棵工作树"只能靠一个**被真正引用**的字符串常量：本常量在 file.go 的预填日志里出现，
// 发布脚本 `deploy/release_20261018.sh` 的 P0-B 特征串与它**逐字等值**。
// 等值锁＝translation_guard_test.go 的 TestIdentityPrefillMarkerMatchesReleaseScript（现读脚本现算）。
const identityPrefillMarker = "PREFILL_IDENTITY_SEGMENTS_V1"

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
