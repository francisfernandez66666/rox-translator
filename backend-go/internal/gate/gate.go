// ============ gate.go · 职责说明 ============
// gate 包内部实现文件。
// =============================================
// Package gate 提供 ConstraintGate 8 项硬校验。
// 校验译文：与源文对照，确认没有漏译、空译、非目标语言、乱码、超长压缩等硬性问题。
package gate

// ============ 本文件职责中文说明 ============
// 译文约束闸门（硬校验）：对模型产出的译文执行 8 项硬性检查——非空、
// 不应残留源语言（中文）、无乱码、数字保持、长度合理（不得压缩过半）、
// 无回环复读、无批量 <sN> 残留标记、目标语言书写系统合理性。
// 任一项不通过即整体 Pass=false（用于质控打回）。
// ========================================

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"translator/internal/textscript"
)

// GateResult 译文约束闸门的整体校验结果：汇总全部硬性校验项，
// Pass 表示是否所有单项均通过（任一不通过即为 false，用于质控打回）。
type GateResult struct {
	Pass   bool    `json:"pass"`   // 是否全部通过
	Checks []Check `json:"checks"` // 各单项校验明细
}

// TermRequirement 术语遵循要求：源文命中 KB 术语时，译文必须包含其规定译法。
// 用于 RAG 硬闸——知识库检索到的术语（如 极石→ROX）若未在译文中体现，判不通过。
type TermRequirement struct {
	Source  string   // KB 术语源文（中文，如「极石」）
	Target  string   // KB 规定译法（如 ROX）
	Aliases []string // 合法别名（高频词表用；KB 词默认空=只有 Target 一种合法译法）
	// FromHighfreq 标记该条来自高频词表（非 KB）。KB 硬闸 ForceTerms/RunWithTerms 只认
	// 非本标记的词——保证"KB 词强制不变、高频词不碰 KB"的职责分离（2026-11-20 用户拍板）。
	FromHighfreq bool
}

// Check 单条硬性校验项的结果：Name 标识校验项、Pass 标记是否通过、
// Detail 在失败时给出原因说明（通过时为空）。
type Check struct {
	Name   string `json:"name"`   // 校验项名称（如 非空/无乱码/数字保持）
	Pass   bool   `json:"pass"`   // 该项是否通过
	Detail string `json:"detail"` // 失败时的详情说明
}

// 硬校验用正则集合（包级复用，避免每次编译）。
var (
	reCJK     = regexp.MustCompile(`\p{Han}`)             // 匹配汉字（检测残留源语言）
	reGarbled = regexp.MustCompile(`[\x{FFFD}]|(\?{4,})`) // 匹配替换符乱码或连续问号
	reDigits  = regexp.MustCompile(`[0-9]+`)              // 匹配数字（预留）
	reNumbers = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`) // 匹配整数/小数（数字保持校验用）
)

// Run 执行 8 项硬校验
// source: 源文本（中文）；target: 目标语言代码；translation: 译文
func Run(source, target, translation string) *GateResult {
	return RunWithTerms(source, target, translation, nil)
}

// RunWithTerms 执行 8 项硬校验 + 第 9 项「KB 术语遵循」。
// source: 源文本（中文）；target: 目标语言代码；translation: 译文；
// terms: KB 命中的术语要求（源文→规定译法）。术语要求非空时，
// 源文出现该术语、译文却未包含规定译法，则判不通过（RAG 硬闸护栏）。
// CheckScriptConsistency 检查项名称「目标语言脚本一致」（对外/跨包契约）。
//
// ★ 2026-11-21 收成常量：引擎侧"这一段要不要带语境重译"的判据按**检查项名字**认，
// 调用方各写一份字面量时，改一处名字就会让那条触发条件静默失效（屏幕上一行红都不会出）。
const CheckScriptConsistency = "目标语言脚本一致"

// 脚本一致性阈值（检查 11）。只在这里定义一次：引擎侧的重译触发条件走 ScriptConsistent，
// 禁止在调用方再抄一份阈值（抄一份＝两把尺子，改一处就会互相打脸）。
const (
	scriptMinTargetRatio  = 0.15 // 目标书写体系占比下限
	scriptMaxForeignRatio = 0.10 // 任一外来非拉丁脚本占比上限
)

func RunWithTerms(source, target, translation string, terms []TermRequirement) *GateResult {
	res := &GateResult{Pass: true}
	tr := strings.TrimSpace(translation)

	// 1. 非空
	pass := tr != ""
	res.Checks = append(res.Checks, Check{"非空", pass, ""})
	if !pass {
		res.Pass = false
	}

	// 2. 不应输出源语言（中文）— 检查是否仍含大量中文（除非目标为 zh_hant/zh）
	if target != "zh_hant" && target != "zh" {
		zhChars := len([]rune(strings.Join(reCJK.FindAllString(tr, -1), "")))
		srcLen := len([]rune(strings.Join(reCJK.FindAllString(source, -1), "")))
		pass = srcLen == 0 || zhChars <= srcLen/3 // 允许少量残留
		res.Checks = append(res.Checks, Check{"非源语言", pass, ""})
		if !pass {
			res.Pass = false
		}
	}

	// 3. 无乱码
	pass = !reGarbled.MatchString(tr)
	res.Checks = append(res.Checks, Check{"无乱码", pass, ""})
	if !pass {
		res.Pass = false
	}

	// 4. 数字保持（★ 2026-10-10 批次 ⑫ 归一化比对）：旧判据是字符串精确等值，
	//    千分位（1,000 vs 1000）、数量词换算（1.5 million vs 150万）、中文数字写法
	//    （3 steps vs 三步）全部误判死——现网实证工单 T20261010132330UEM 重译 8 次后失败。
	//    现改为 missingNumbers 归一化比对（数值等值＋数量词乘子＋中文数字单向认领），
	//    判死时 Detail 列出源文有、译文找不到的数字清单；通过时 Detail 保持空串。
	//    检查项名称仍为「数字保持」（对外可见文案，不改名；orchestrator 按此名走短重试）。
	if missingNums := missingNumbers(source, tr); len(missingNums) > 0 {
		res.Pass = false
		res.Checks = append(res.Checks, Check{CheckNumberKeep, false, "缺失: " + strings.Join(missingNums, ", ")})
	} else {
		res.Checks = append(res.Checks, Check{CheckNumberKeep, true, ""})
	}

	// 5. 长度合理（不严重压缩）
	srcLen := len([]rune(source))
	trLen := len([]rune(tr))
	pass = srcLen == 0 || trLen >= srcLen/2 // 不得压缩过半
	res.Checks = append(res.Checks, Check{"长度合理", pass, ""})
	if !pass {
		res.Pass = false
	}

	// 6. 无重复内容（明显回环复读）
	pass = !hasRepetition(tr)
	res.Checks = append(res.Checks, Check{"无回环复读", pass, ""})
	if !pass {
		res.Pass = false
	}

	// 7. 无残留编号标记（<s1> 等批量标记）
	pass = !strings.Contains(tr, "<s") && !strings.Contains(tr, "</s")
	res.Checks = append(res.Checks, Check{"无残留标记", pass, ""})
	if !pass {
		res.Pass = false
	}

	// 8. 目标语言合理性（词面检查，弱校验）
	pass = targetCharsReasonable(tr, target)
	detail := ""
	if !pass && target == "kk" {
		detail = "哈萨克语必须使用西里尔字母书写（Қазақ тілі），不得输出阿拉伯字母写法"
	}
	res.Checks = append(res.Checks, Check{"目标语言合理", pass, detail})
	if !pass {
		res.Pass = false
	}

	// 9. KB 术语遵循（RAG 硬闸）：源文出现 KB 命中术语、译文却未含规定译法 → 不通过。
	//   仅对「源文确实包含该术语」的要求生效；目标译法为空或源文不包含则跳过。
	//   校验口径：译文非空且包含术语规定译法（子串匹配）即通过。
	for _, tm := range terms {
		if tm.FromHighfreq {
			continue // 高频词是"建议统一"，不进 KB 强制硬闸（保交付，避免误杀品牌/一词多义）
		}
		if strings.TrimSpace(tm.Source) == "" || strings.TrimSpace(tm.Target) == "" {
			continue
		}
		if !termContains(source, tm.Source) {
			continue // 源文不含该术语，不适用
		}
		if tr != "" && termContains(tr, tm.Target) {
			continue // 译文已含规定译法（拉丁术语忽略大小写，见 term_match.go）
		}
		res.Checks = append(res.Checks, Check{"术语遵循", false, fmt.Sprintf("知识库术语「%s」应译作 %s，译文未体现", tm.Source, tm.Target)})
		res.Pass = false
	}

	// 10. 译文非无意义填充词（幻觉检测）：模型偶发对占位文本（待补充/TBD）输出 "and"
	//   等无意义连词作为整条译文。译文仅为单个常见填充词且源文非对应含义时拦截。
	if tr != "" && isFillerWordOnly(tr) && !sourceIsFiller(source) {
		res.Checks = append(res.Checks, Check{"译文非填充词", false, fmt.Sprintf("译文「%s」疑似无意义填充词，非有效翻译", tr)})
		res.Pass = false
	}

	// 11. 目标语言脚本一致性（2026-11-20 P2）：译文混入非目标语言脚本（如希伯来文里
	//   混入韩文 신호/西里尔カнал）必须检出。用 textscript 单一口径，拉丁品牌白名单不误杀。
	//   阈值取"明显污染"档（目标体系占比 >= 0.15、任一外来非拉丁脚本 <= 0.10），避免误杀
	//   短译文/术语混排。失败动作＝记 Check 让上层走既有重译闭环，不新增"阻断交付"档。
	if tr != "" && !ScriptConsistent(target, source, tr) {
		res.Checks = append(res.Checks, Check{CheckScriptConsistency, false,
			fmt.Sprintf("译文混入非目标语言(%s)的书写系统字符", target)})
		res.Pass = false
	}

	return res
}

// ScriptConsistent 目标语言脚本一致性（检查 11）的唯一出口。
//
// 为什么要有这个薄委托：引擎侧「已译但形态不过 ⇒ 带语境重译」的触发条件必须与闸门
// **同一把尺子**。若调用方自己调 textscript.TargetConsistency 并各写一对阈值，
// 闸门说"过"、重译腿说"不过"（或反过来）就会长期并存，而两边都不报错
// （AGENTS §一·11「多把尺子」）；抄字面量 0.15/0.10 是同一个病的轻症形态。
//
// 拉丁体系与其他（Other）目标语不判：拉丁译文本就允许混入品牌名/术语，
// 硬判会把所有英文交付打成"脚本不纯"（阈值与检查 11 完全同源）。
//
// ★ 2026-11-22 tv9 补第三档「脚本中立格不判」（source 参数为此而来）：
// 表格里 `SaaS`、`650,000–1,200,000<br>` 这类格两侧的字母**全是拉丁**——它本来就没有
// 目标语内容可写，却因为「分母只算字母、拉丁不进目标占比分子」被算成 0% 目标体系 ⇒ 判不纯。
// 现网实证（tv9）：`gate.ScriptConsistent("he","650,000–1,200,000<br>")` = false，
// 于是三个**完全正确**的数字格被送进上下文重译（白占预算），而其中一次重译若把 `<br>`
// 丢掉、其余不变，反而能通过判据 ⇒ 有拿正确译文换坏形态的风险。
// 判据方向：只有**两侧都没有非拉丁字母**才豁免（`SaaS`→`סאס` 不豁免、`SaaS`→`사스` 不豁免、
// 中文源→韩文译文更不豁免——中文源有汉字，第一步就不成立）。这样"拉丁格不动"与
// "回显原文/串语照样抓"两件事同时成立。
func ScriptConsistent(target, source, translation string) bool {
	want := textscript.LangToScript(target)
	if want == textscript.Other || want == textscript.Latin || strings.TrimSpace(translation) == "" {
		return true
	}
	// 脚本中立格：源与译文两侧的字母都只有拉丁（纯数字/术语/HTML 标签/缩写）⇒ 无从判纯度。
	if !hasNonLatinLetters(source) && !hasNonLatinLetters(translation) {
		return true
	}
	return textscript.TargetConsistency(translation, want, scriptMinTargetRatio, scriptMaxForeignRatio)
}

// hasNonLatinLetters 文本里是否出现非拉丁字母（数字/标点/空白不算）。
//
// 与 textscript.Share 同一套分档（它就是 TargetConsistency 的分母来源），
// 所以"这里判中立"与"那里算占比"永远看同一批字符。
func hasNonLatinLetters(text string) bool {
	for s, n := range textscript.Share(text) {
		if n > 0 && s != textscript.Latin && s != textscript.Other {
			return true
		}
	}
	return false
}

// ForceTerms 术语强制替换（H1）：源文命中术语但译文未含规定译法时，
// 若译文残留源术语字面（未翻译直通，如中文术语出现在英文译文中），直接以 KB 规定译法覆写——
// 零成本命中 100% 遵循；无法确定性覆写的（译文用了第三种写法）返回原文，交由调用方走重翻闭环。
// 返回修正后的译文与实际覆写的术语数。
func ForceTerms(source, translation string, terms []TermRequirement) (string, int) {
	tr := translation
	fixed := 0
	if strings.TrimSpace(tr) == "" || len(terms) == 0 {
		return tr, 0
	}
	for _, tm := range terms {
		if tm.FromHighfreq {
			continue // 高频词不参与 KB 强制覆写（同上，职责分离）
		}
		src := strings.TrimSpace(tm.Source)
		tgt := strings.TrimSpace(tm.Target)
		if src == "" || tgt == "" || !termContains(source, src) {
			continue
		}
		if termContains(tr, tgt) {
			continue // 已合规（拉丁术语忽略大小写）
		}
		if termContains(tr, src) {
			n := strings.Count(tr, src)
			tr = strings.ReplaceAll(tr, src, tgt)
			fixed += n
		}
	}
	return tr, fixed
}

// isFillerWordOnly 判断译文是否仅为无意义填充词（模型幻觉输出）。
// 常见填充词：and/or/but/so/yet/for/nor/the/a/an 等。
func isFillerWordOnly(tr string) bool {
	fillerWords := []string{"and", "or", "but", "so", "yet", "for", "nor", "the", "a", "an", "with", "by", "in", "on", "at", "to", "of"}
	trimmed := strings.TrimSpace(strings.ToLower(tr))
	for _, w := range fillerWords {
		if trimmed == w {
			return true
		}
	}
	return false
}

// sourceIsFiller 判断源文是否本身为无意义占位符（纯标点/TBD/N/A 等）。
// 注意：中文「待补充」「待定」等是有意义的短语，不算占位符，应被正确翻译。
func sourceIsFiller(source string) bool {
	// 纯标点/无语义占位符
	fillerPatterns := []string{"—", "–", "...", "....", "TBD", "N/A", "NA", "TODO", "FIXME", "xxx", "XXX", "___", "---", "-"}
	trimmed := strings.TrimSpace(source)
	for _, p := range fillerPatterns {
		if trimmed == p {
			return true
		}
	}
	return false
}

// brandSuffixWords 品牌名后常见的车辆/公司类自创后缀集合（多语言小写，供 NormalizeBrandTerm 剥离）。
// 背景：KB 定义了品牌术语（module=brand，如 极石→ROX）后，模型偶发把品牌名加上业务后缀——
// 如 "ROX vehicles" / "ROX motor" / "ROX cars" / "ROX автомобиль"。产品要求品牌名一律等于
// 术语规定译法（ROX），不得带后缀。此表覆盖英文/俄文/西语/阿语等高频情形。数组多次编译一次。
var brandSuffixWords = []string{
	"vehicles", "vehicle", "motors", "motor", "automobiles", "automobile",
	"autos", "auto", "cars", "car",
	"автомобилей", "автомобиль", "автомобили", "автомобиля", "авто",
	"automóviles", "automóvil", "coches", "coche", "carros", "carro",
	"سيارات", "سيارة",
}

// NormalizeBrandTerm 品牌术语归一化（纯函数，RAG 硬闸品牌统一输出用）：
// 把译文里「品牌规定译法（brand）」与其相邻的车辆类后缀词组成的组合，规约为纯品牌名。
// 采用「两步正则替换」：先剥后缀词块（品牌+分隔符+后缀词），再剥前缀词块
// （后缀词+分隔符+品牌），替换时保留品牌旁的分隔符，避免中阿/俄文粘连。
// 覆盖形态：
//  1. 品牌+后缀：   "ROX vehicles expanding"  → "ROX expanding"
//  2. 后缀+品牌：   "Автомобили ROX мчатся"   → "ROX мчатся"
//  3. 前后环绕：    "سيارات ROX في العالم"     → "ROX في العالم"
//
// 多后缀连写 "ROX Motor Car" 整体剥离。大小写不敏感（(?i) 只折叠 ASCII，故词表
// 已内置首字母大写变体覆盖俄/欧语句首名词大写）。短词（<5 字符）追加 \b 防误切
// "automóviles" 等长词（auto→automóviles）。收尾压缩连续空白并清理标点前空格。
// 参数 translation：模型译文；brand：品牌规定译法（如 ROX）。brand 为空或译文中无品牌则原样返回。
func NormalizeBrandTerm(translation, brand string) string {
	b := strings.TrimSpace(brand)
	if b == "" {
		return translation
	}
	if !strings.Contains(translation, b) {
		return translation // 译文未出现品牌，无需归一
	}
	// 动态构造后缀词表正则：每个词内置「首字母大写」变体；短词追加 \b。
	suffixAlt := make([]string, 0, len(brandSuffixWords)*2)
	for _, w := range brandSuffixWords {
		esc := regexp.QuoteMeta(w)
		if len([]rune(w)) < 5 {
			esc += `\b`
		}
		suffixAlt = append(suffixAlt, esc)
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		escCap := regexp.QuoteMeta(string(r))
		if len(r) < 5 {
			escCap += `\b`
		}
		suffixAlt = append(suffixAlt, escCap)
	}
	// 按字符长度降序，保证长词（automóviles）优先于短词（auto）参与匹配。
	sort.SliceStable(suffixAlt, func(i, j int) bool { return len(suffixAlt[i]) > len(suffixAlt[j]) })
	joined := strings.Join(suffixAlt, "|")
	sep := `[\s\-_./·]`
	sw := `(?:` + joined + `)`
	sblk := `(?:` + sw + `(?:` + sep + `+` + sw + `)*)`
	escB := regexp.QuoteMeta(b)

	// 第一步：剥后缀词块 —— (brand)(sep+)(词块) → $1$2（保留品牌+其后分隔符）。
	out := regexp.MustCompile(`(?i)(`+escB+`)(`+sep+`+)(`+sblk+`)`).
		ReplaceAllString(translation, `${1}${2}`)

	// 第二步：剥前缀词块 —— (词块)(sep+)(brand) → $2$3（保留分隔符+品牌）。
	out = regexp.MustCompile(`(?i)(`+sblk+`)(`+sep+`+)(`+escB+`)`).
		ReplaceAllString(out, `${2}${3}`)

	// 收尾：压缩连续空白、清理标点前空格、去首尾空白（替换可能残留单个分隔符）。
	out = regexp.MustCompile(`[ \t]+`).ReplaceAllString(out, " ")
	out = regexp.MustCompile(` ([.,;:!?…。！？，；：])`).ReplaceAllString(out, "$1")
	return strings.TrimSpace(out)
}

// hasRepetition 检测明显重复片段（3+ 字连续出现 4+ 次，或常见词如 and/and/and 出现 3+ 次）
func hasRepetition(s string) bool {
	runes := []rune(s)
	// 原逻辑：3+ 字段连续 4+ 次
	for size := 3; size <= 8 && size*4 <= len(runes); size++ {
		for i := 0; i+size*4 <= len(runes); i++ {
			seg := string(runes[i : i+size])
			count := 0
			for j := i; j+size <= len(runes); j += size {
				if string(runes[j:j+size]) == seg {
					count++
				} else {
					break
				}
			}
			if count >= 4 {
				return true
			}
		}
	}
	// 补充：短词（2-4 字）连续 3+ 次（如 "and and and"）
	for size := 2; size <= 4 && size*3 <= len(runes); size++ {
		for i := 0; i+size*3 <= len(runes); i++ {
			seg := string(runes[i : i+size])
			if seg == " " || strings.TrimSpace(seg) == "" {
				continue
			}
			count := 0
			for j := i; j+size <= len(runes); j += size {
				if string(runes[j:j+size]) == seg {
					count++
				} else {
					break
				}
			}
			if count >= 3 {
				return true
			}
		}
	}
	return false
}

// targetCharsReasonable 目标语言词面合理性（弱校验：目标体系字母是否出现）。
//
// ★ 2026-11-20 P2 重写：原 switch 无 he 等语种分支，落 default 要求拉丁字母——
//
//	方向反了，希伯来语污染译文含 `arketing` 反而通过。现转调 textscript 单一口径，
//	覆盖全部已在 LangToScript 登记的语言（含 he/ar/ru/kk）。
//	kk 的"西里尔且不含阿拉伯"专属约束保留（见下方特判）。
func targetCharsReasonable(tr, target string) bool {
	if tr == "" {
		return true
	}
	// 哈萨克语专属：官方用西里尔，不得输出阿拉伯字母变体（比通用脚本校验更严）。
	if target == "kk" {
		var cyr, arabic int
		for _, r := range tr {
			if !unicode.IsLetter(r) {
				continue
			}
			switch {
			case r >= '\u0400' && r <= '\u04FF':
				cyr++
			case r >= '\u0600' && r <= '\u06FF':
				arabic++
			}
		}
		return cyr > 0 && arabic == 0
	}
	want := textscript.LangToScript(target)
	if want == textscript.Other {
		// 未映射语言：沿用旧的兜底（有非 ASCII 字母即放行），fail-soft
		for _, r := range tr {
			if r > unicode.MaxASCII && unicode.IsLetter(r) {
				return true
			}
		}
		return false
	}
	// 拉丁目标保留旧语义：必须至少有一个拉丁字母（`<`/纯符号无字母 ⇒ 不通过，
	// 让闸门打回重译——现网"源段是 `<`、重译回吐指令"那条锁依赖此行为）。
	if want == textscript.Latin {
		for _, r := range tr {
			if unicode.IsLetter(r) && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return true
			}
		}
		return false
	}
	// 非拉丁目标：只判"目标体系字母占比 >= 极低阈值"；外来脚本拦截交给检查 11。
	return textscript.TargetConsistency(tr, want, 0.05, 1.0)
}
