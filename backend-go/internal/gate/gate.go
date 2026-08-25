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
	"regexp"
	"strings"
	"unicode"
)

// 8 项校验结果
type GateResult struct {
	Pass   bool    `json:"pass"`   // 是否全部通过
	Checks []Check `json:"checks"` // 各单项校验明细
}

// Check 单项校验
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
	res.Checks = append(res.Checks, Check{"目标语言合理", pass, ""})
	if !pass {
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
