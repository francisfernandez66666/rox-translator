// ============ number.go · 职责说明 ============
// 「数字保持」检查项的归一化比对实现（★ 2026-10-10 批次 ⑫）。
// 背景：旧判据把源文与译文各拆成数字串后做字符串精确等值，千分位（1,000 vs 1000）、
// 数量词换算（1.5 million vs 150万）、中文数字写法（3 steps vs 三步）全部误判为丢数字，
// 现网实证工单 T20261010132330UEM 重译 8 次仍判死后 rejected。
// 本文件提供：
//   - stripNumSeparators：剥千分位逗号/数字间空格（仅「数字+分隔符+恰好3位数字」形态）；
//   - suffixMultiplier：认领紧跟数字后的数量词词缀（万/亿/k/K/M/million/B/billion）；
//   - missingNumbers：主判据——返回源文有而译文（归一后）找不到的数字字面量清单；
//   - cnNumberReadings / containsCnNumber：中文数字「单向认领」（只放行「数字还在、写法变了」，
//     绝不放行「数字没了」——译文侧的阿拉伯数字仍必须能对回源文）。
//
// 检查项名称「数字保持」是对外可见文案，保持不变（常量 CheckNumberKeep）。
// ========================================
package gate

import (
	"regexp"
	"strconv"
	"strings"
)

// CheckNumberKeep 「数字保持」检查项名称（对外可见文案）。
// orchestrator 依赖该名字判定「数字类失败」走短重试（≤2 次），改名会连带打断那条链路。
const CheckNumberKeep = "数字保持"

// numSuffixMults 数量词词缀 → 基数乘子表。
// 顺序敏感：长词缀（million/billion）必须排在单字母（M/B）之前，否则 "1.5 million"
// 会先被 'M' 截断成 1.5×1e6 后残留 "illion"。中文词缀（万/亿）无需词边界保护。
var numSuffixMults = []struct {
	suf  string
	mult float64
}{
	{"million", 1e6}, {"Million", 1e6},
	{"billion", 1e9}, {"Billion", 1e9},
	{"万", 1e4}, {"亿", 1e8},
	{"k", 1e3}, {"K", 1e3}, {"M", 1e6}, {"B", 1e9},
}

// cnNumChars 中文数字字符全集（含 〇 与「两」变体），供边界判定用：
// 认领命中时要求读法两侧不紧邻其他中文数字字符，防止「三」误命中「十三」「三百」。
const cnNumChars = "零〇一二两三四五六七八九十百千万点"

// cnDigits 中文数字单字表（下标即数值）。★ 必须经 cnDigitRunes 按字节下标取 rune——
// 直接对多字节 UTF-8 字符串做下标索引会切出半个字符。
const cnDigits = "零一二三四五六七八九"

var cnDigitRunes = []rune(cnDigits)

// reThousandSep 千分位形态：数字 + （逗号或空格）+ 恰好 3 位数字 + 后面不再是数字。
// 刻意要求「恰好 3 位」：避免把 "3 5"（空格分隔的并列数字）或 "2026,10,10"（日期）
// 误剥成一个大数——那会把旧判据下合法的译文打成假丢数字。
// 说明：Go 的 RE2 不支持 (?!) 前瞻，故第 3 组用 ([^0-9]|$) 捕获尾随字符并在替换里原样保留。
var reThousandSep = regexp.MustCompile(`([0-9])(?:[,,] ?| )([0-9]{3})([^0-9]|$)`)

// stripNumSeparators 剥除千分位分隔符（"1,000"→"1000"、"1 500 000"→"1500000"）。
// 循环应用直到稳定：'1,500,000' 第一遍剥成 '1500,000'，第二遍剥成 '1500000'。
// 纯函数：空串/无分隔符/纯符号原样返回，不 panic。
func stripNumSeparators(s string) string {
	for {
		next := reThousandSep.ReplaceAllString(s, "${1}${2}${3}")
		if next == s {
			return next
		}
		s = next
	}
}

// suffixMultiplier 识别紧跟在数字后面的数量词词缀并返回应乘回的基数（无词缀返回 1）。
// 只处理「跟在数字后面」的词缀（允许一个空白间隔），不做跨词猜测：
// 拉丁词缀后若紧跟字母（km / MB / Millions）视为普通单词不认领，
// 避免 "5 km" 被当成 5000、"3MB" 被当成 3e6。
func suffixMultiplier(rest string) float64 {
	r := strings.TrimLeft(rest, " \t")
	for _, e := range numSuffixMults {
		if !strings.HasPrefix(r, e.suf) {
			continue
		}
		// 拉丁词缀（ASCII 首字母）需要词边界保护：词缀后紧跟字母＝更长的英文单词。
		if e.suf[0] < 0x80 {
			tail := r[len(e.suf):]
			if tail != "" {
				if c := tail[0]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
					continue
				}
			}
		}
		return e.mult
	}
	return 1
}

// extractNumValues 从（已剥千分位的）文本中抽取全部数字的数值。
// 每个数字先按字面解析为浮点，再乘上其后紧跟的数量词基数（"150万"→150×1e4）。
// 解析失败的 token（理论上是畸形小数）直接跳过，不参与比对。
func extractNumValues(s string) []float64 {
	locs := reNumbers.FindAllStringIndex(s, -1)
	vals := make([]float64, 0, len(locs))
	for _, loc := range locs {
		v, err := strconv.ParseFloat(s[loc[0]:loc[1]], 64)
		if err != nil {
			continue // 畸形字面量跳过，不 panic
		}
		vals = append(vals, v*suffixMultiplier(s[loc[1]:]))
	}
	return vals
}

// numValueIn 判断 vals 中是否存在与 v 数值相等的元素。
// 容差取相对 1e-12（数量词乘法的浮点舍入误差量级 ≈1e-16，留 4 个数量级余量），
// 既放行 "1.5 million" vs 1500000 的乘法抖动，又不会把 2000000001 误认成 2e9。
func numValueIn(vals []float64, v float64) bool {
	for _, x := range vals {
		d := x - v
		if d < 0 {
			d = -d
		}
		scale := v
		if x > scale {
			scale = x
		}
		tol := scale * 1e-12
		if tol < 1e-9 {
			tol = 1e-9 // 极小值下的绝对下限，防 0.3*1e4 一类抖动漏放
		}
		if d <= tol {
			return true
		}
	}
	return false
}

// cnIntRead 0..9999 整数的常规中文读法（"15"→十五、"23"→二十三、"2026"→二千零二十六）。
// 刻意不做完整进位乘法（≥10000 不产出读法，交由数量词乘子那条腿比对）。
func cnIntRead(n int) string {
	if n == 0 {
		return "零"
	}
	var b strings.Builder
	th := n / 1000
	rem := n % 1000
	if th > 0 {
		b.WriteRune(cnDigitRunes[th])
		b.WriteString("千")
	}
	hu := rem / 100
	rem %= 100
	if hu > 0 {
		b.WriteRune(cnDigitRunes[hu])
		b.WriteString("百")
	}
	// 跳位补零：千位后直接进十/个位（2026→二千零二十六）、百位后直接进个位（105→一百零五）。
	if th > 0 && hu == 0 && rem > 0 {
		b.WriteString("零")
	}
	te := rem / 10
	un := rem % 10
	if te > 0 {
		// 独立 10~19 省略「一」（十五，不写一十五）；百/千之后的十位按正式读法带「一」（一百一十）。
		if te > 1 || th > 0 || hu > 0 {
			b.WriteRune(cnDigitRunes[te])
		}
		b.WriteString("十")
	} else if un > 0 && hu > 0 {
		b.WriteString("零") // 105 → 一百零五
	}
	if un > 0 {
		b.WriteRune(cnDigitRunes[un])
	}
	return b.String()
}

// cnIntReadings 返回整数的中文读法候选（含「两」变体：二千/二百 位置口语读两千/两百，
// 数值 2 另收独立的「两」）。调用方对全部候选逐一尝试命中，任一命中即认领。
func cnIntReadings(n int) []string {
	if n < 0 || n > 9999 {
		return nil
	}
	r := cnIntRead(n)
	out := []string{r}
	if v := strings.Replace(r, "二千", "两千", 1); v != r {
		out = append(out, v)
	}
	if v := strings.Replace(r, "二百", "两百", 1); v != r {
		out = append(out, v)
	}
	if n == 2 {
		out = append(out, "两")
	}
	return out
}

// cnNumberReadings 把数字字面量（整数或小数，整数部分 ≤9999）转成中文读法候选。
// 小数按「点+逐位」读（1.5→一点五）。超出射程（整数部分 >9999）返回 nil——
// 那类数字正常写法就是阿拉伯数字，中文认领不适用。
func cnNumberReadings(lit string) []string {
	intPart, frac := lit, ""
	if i := strings.IndexByte(lit, '.'); i >= 0 {
		intPart, frac = lit[:i], lit[i+1:]
	}
	n, err := strconv.Atoi(intPart)
	if err != nil || n > 9999 || n < 0 {
		return nil
	}
	ints := cnIntReadings(n)
	if frac == "" {
		return ints
	}
	outs := make([]string, 0, len(ints))
	for _, base := range ints {
		var b strings.Builder
		b.WriteString(base)
		b.WriteString("点")
		for _, d := range []byte(frac) {
			if d < '0' || d > '9' {
				return nil // 畸形小数部分不认领
			}
			b.WriteRune(cnDigitRunes[d-'0'])
		}
		outs = append(outs, b.String())
	}
	return outs
}

// containsCnNumber 判断译文是否包含读法 reading，且命中处两侧不紧邻其他中文数字字符。
// 边界判定是防误放行的关键：源文 3 的读法「三」不得命中译文里的「十三」「三百」，
// 源文 20 的读法「二十」不得命中「二十三」——那等于放行了数字被改动。
func containsCnNumber(tr, reading string) bool {
	if reading == "" {
		return false
	}
	runes := []rune(tr)
	pat := []rune(reading)
	for i := 0; i+len(pat) <= len(runes); i++ {
		if string(runes[i:i+len(pat)]) != reading {
			continue
		}
		if i > 0 && strings.ContainsRune(cnNumChars, runes[i-1]) {
			continue // 左邻是数字字符：这是更长数字的一部分
		}
		if i+len(pat) < len(runes) && strings.ContainsRune(cnNumChars, runes[i+len(pat)]) {
			continue // 右邻是数字字符：同上
		}
		return true
	}
	return false
}

// missingNumbers 「数字保持」主判据（纯函数）：返回源文有、译文（归一后）找不到的
// 数字字面量清单（去重、按源文出现序）。比对三步：
//  1. 两侧同形态归一（剥千分位）后按「数值+数量词乘子」等值比对（"1,000"≡"1000"≡"1000.0"，
//     "150万"≡"1,500,000"≡"1.5 million"）；
//  2. 数值比对不中时做中文数字单向认领（"3 steps"→"三步"）；
//  3. 仍不中才计入缺失。方向刻意单向：译文多出的数字不检查（保持旧语义），
//     译文侧的阿拉伯数字必须能对回源文。
func missingNumbers(source, translation string) []string {
	srcNorm := stripNumSeparators(source)
	trVals := extractNumValues(stripNumSeparators(translation))
	locs := reNumbers.FindAllStringIndex(srcNorm, -1)
	var missing []string
	seen := map[string]bool{}
	for _, loc := range locs {
		lit := srcNorm[loc[0]:loc[1]]
		if seen[lit] {
			continue
		}
		v, err := strconv.ParseFloat(lit, 64)
		if err != nil {
			continue // 畸形字面量不参与判定，也不计入缺失
		}
		if numValueIn(trVals, v*suffixMultiplier(srcNorm[loc[1]:])) {
			continue
		}
		// 中文数字单向认领：只放行「数字还在、写法变了」；对原文（未剥分隔符形态）检索，
		// 千分位剥除不影响中文数字。
		found := false
		for _, rd := range cnNumberReadings(lit) {
			if containsCnNumber(translation, rd) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, lit)
			seen[lit] = true
		}
	}
	return missing
}
