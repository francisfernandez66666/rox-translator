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
	"strings"
	"unicode"
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
	Source string // KB 术语源文（中文，如「极石」）
	Target string // KB 规定译法（如 ROX）
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

	// 4. 数字保持
	srcNums := reNumbers.FindAllString(source, -1)
	trNums := reNumbers.FindAllString(tr, -1)
	missing := false
	for _, n := range srcNums {
		if !contains(trNums, n) {
			missing = true
			break
		}
	}
	pass = !missing
	res.Checks = append(res.Checks, Check{"数字保持", pass, ""})
	if !pass {
		res.Pass = false
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
		if strings.TrimSpace(tm.Source) == "" || strings.TrimSpace(tm.Target) == "" {
			continue
		}
		if !strings.Contains(source, tm.Source) {
			continue // 源文不含该术语，不适用
		}
		if tr != "" && strings.Contains(tr, tm.Target) {
			continue // 译文已含规定译法
		}
		res.Checks = append(res.Checks, Check{"术语遵循", false, fmt.Sprintf("知识库术语「%s」应译作 %s，译文未体现", tm.Source, tm.Target)})
		res.Pass = false
	}

	return res
}

// contains 判断字符串列表中是否包含指定值（数字保持校验辅助）
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// brandSuffixWords 品牌名后常见的车辆/公司类自创后缀集合（多语言小写，供 NormalizeBrandTerm 剥离）。
// 背景：KB 定义了品牌术语（module=brand，如 极石→ROX）后，模型偶发把品牌名加上业务后缀——
// 如 "ROX vehicles" / "ROX motor" / "ROX cars" / "ROX автомобиль"。产品要求品牌名一律等于
// 术语规定译法（ROX），不得带后缀。此表覆盖英文/俄文/西语/葡语等高频情形。数组多次编译一次。
var brandSuffixWords = []string{
	"vehicles", "vehicle", "motors", "motor", "automobiles", "automobile",
	"autos", "auto", "cars", "car",
	"автомобилей", "автомобиль", "автомобили", "автомобиля", "авто",
	"automóviles", "automóvil", "coches", "coche", "carros", "carro",
}

// NormalizeBrandTerm 品牌术语归一化（纯函数，RAG 硬闸品牌统一输出用）：
// 把译文里「品牌规定译法（brand）+ 空格/连字符 + 车辆类后缀词」的自创组合，规约为纯品牌名。
// 例：NormalizeBrandTerm("ROX vehicles expanding", "ROX") → "ROX expanding"；
//     多后缀连写 "ROX Motor Car" → "ROX"。大小写不敏感地匹配后缀（保持原品牌大小写不变）。
// 参数 translation：模型译文；brand：品牌规定译法（如 ROX）。brand 为空或译文中无品牌则原样返回。
func NormalizeBrandTerm(translation, brand string) string {
	b := strings.TrimSpace(brand)
	if b == "" {
		return translation
	}
	if !strings.Contains(translation, b) {
		return translation // 译文未出现品牌，无需归一
	}
	// 动态构造后缀词表正则（品牌后跟分隔符 + 一个或多个后缀词）
	suffixAlt := strings.Join(brandSuffixWords, "|")
	re := regexp.MustCompile("(?i)(" + regexp.QuoteMeta(b) + ")([\\s\\-_./·]*(?:" + suffixAlt + ")(?:[\\s\\-_./·]*(?:" + suffixAlt + "))*)")
	out := re.ReplaceAllStringFunc(translation, func(m string) string {
		group := re.FindStringSubmatch(m)
		if len(group) < 2 {
			return m
		}
		return group[1] // 仅保留品牌词本身，剥离全部车辆类后缀
	})
	return out
}

// hasRepetition 检测明显重复片段（如 3 字以上连续出现 4 次）
func hasRepetition(s string) bool {
	runes := []rune(s)
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
	return false
}

// targetCharsReasonable 目标语言词面合理性（弱校验：目标为 CJK 时应含汉字等）
func targetCharsReasonable(tr, target string) bool {
	if tr == "" {
		return true
	}
	switch target {
	case "zh_hant", "zh":
		return reCJK.MatchString(tr)
	case "ru":
		return strings.ContainsAny(tr, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя")
	case "ar":
		return strings.ContainsAny(tr, "ابتثجحخدذرزسشصضطظعغفقكلمنهوي")
	case "ja":
		return strings.ContainsAny(tr, "あいうえおかきくけこアイウエオ一二三四五六七八九十")
	case "ko":
		return strings.ContainsAny(tr, "가나다라마바사아자차카타파하")
	case "th":
		return strings.ContainsAny(tr, "กขคงจฉชซฌญฎฏฐฑฒณดตถทธนบปผฝพฟภมยรลวศษสหอฮ")
	case "kk":
		// 哈萨克语（哈萨克斯坦）：官方书写系统为西里尔字母（Қазақ тілі）。
		// 必须出现西里尔字母，且不得输出阿拉伯字母写法（中国哈萨克族所用阿拉伯字母变体）。
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
	default:
		// 拉丁系语言：应含英文字母
		for _, r := range tr {
			if unicode.IsLetter(r) && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return true
			}
		}
		return false
	}
}
