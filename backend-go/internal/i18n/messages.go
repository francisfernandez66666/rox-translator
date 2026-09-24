// ============ 本文件职责中文说明 ============
// 后端提示语翻译入口 Msg()（★ 2026-09-24 〇-S #12 后端语言识别）：
// 把 handler 里写死的中文 message 按请求语种翻成英文，匹配顺序三级：
//  1. 精确词条（catalog_en.go 的 exactEN，343+ 条全量盘点）；
//  2. 最长前缀词条（≥6 个 rune 的键，覆盖「保存失败: 」+err 这类运行时拼接）；
//  3. 模式词条（含 %d/%s/%v 占位的 fmt 句式，编译期转正则按序回填）；
//     全部不命中 → 原样透传中文（漏词条只表现为该条没翻译，绝不报错）。
//
// 语种非 en 时直接透传，中文路径零开销。
// =============================================
package i18n

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

// prefixMinRunes 是参与前缀匹配的最短键长（rune 计）。
// 门槛 5 的用意：覆盖「四字中文+冒号」拼接前缀（如 apierrors.New 里的 "保存失败:"+err，
// 无尾空格只有 5 个 rune），同时拦住 "登录"、"失败" 这类 ≤4 字短泛词把不相干消息误翻。
const prefixMinRunes = 5

// prefixKeys 按键长降序排列的候选前缀键（init 构建），配合 first-match 即最长匹配。
var prefixKeys []string

// compiledPattern 一条已编译的模式词条：zhRe 匹配中文原句，enFmt 用捕获按序回填。
type compiledPattern struct {
	zhRe  *regexp.Regexp
	enFmt string
}

// patterns 模式词条编译结果（init 构建，顺序即声明顺序，词条间无交叠）。
var patterns []compiledPattern

func init() {
	keys := make([]string, 0, len(exactEN))
	for k := range exactEN {
		if len([]rune(k)) >= prefixMinRunes {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		ki, kj := []rune(keys[i]), []rune(keys[j])
		if len(ki) != len(kj) {
			return len(ki) > len(kj)
		}
		return keys[i] < keys[j] // 等长时定序，保证行为可复现
	})
	prefixKeys = keys

	patterns = make([]compiledPattern, 0, len(patternsEN))
	for _, p := range patternsEN {
		patterns = append(patterns, compiledPattern{zhRe: buildRE(p.zhFmt), enFmt: p.enFmt})
	}
}

// buildRE 把中文 fmt 模板转成整句锚定的正则：%d → (-?\d+)，%s/%v → (.*?)，
// 其余文字按字面量匹配（QuoteMeta）。占位符两侧必须有可区分文本，词条均已核对。
func buildRE(zhFmt string) *regexp.Regexp {
	var sb strings.Builder
	sb.WriteString("^")
	i := 0
	for i < len(zhFmt) {
		if zhFmt[i] == '%' && i+1 < len(zhFmt) {
			switch zhFmt[i+1] {
			case 'd':
				sb.WriteString("(-?\\d+)")
				i += 2
				continue
			case 's', 'v':
				sb.WriteString("(.*?)")
				i += 2
				continue
			}
		}
		// 逐 rune 收集字面量段，遇到下一个占位再 QuoteMeta
		j := i
		for j < len(zhFmt) && !(zhFmt[j] == '%' && j+1 < len(zhFmt) && strings.ContainsRune("dsv", rune(zhFmt[j+1]))) {
			j++
		}
		sb.WriteString(regexp.QuoteMeta(zhFmt[i:j]))
		i = j
	}
	sb.WriteString("$")
	return regexp.MustCompile(sb.String())
}

// Msg 按 ctx 中的语种翻译一条面向用户的提示语。
// 非英文语境或未命中词条一律原样返回；命中前缀词条时保留中文后缀（如已含英文的错误详情）。
func Msg(ctx context.Context, s string) string {
	if LangFrom(ctx) != "en" || s == "" {
		return s
	}
	if e, ok := exactEN[s]; ok {
		return e
	}
	// 最长前缀：prefixKeys 已按长度降序，first hit 即最长
	for _, k := range prefixKeys {
		if strings.HasPrefix(s, k) {
			return exactEN[k] + s[len(k):]
		}
	}
	for _, p := range patterns {
		if m := p.zhRe.FindStringSubmatch(s); m != nil {
			return fillPlaceholders(p.enFmt, m[1:])
		}
	}
	return s
}

// fillPlaceholders 把捕获组按序回填进英文模板的 %d/%s/%v 槽位。
// 中英词条占位符顺序已逐条核对一致；模板里的字面 % 不支持（现无此词条）。
func fillPlaceholders(enFmt string, caps []string) string {
	var sb strings.Builder
	ci := 0
	for i := 0; i < len(enFmt); i++ {
		if enFmt[i] == '%' && i+1 < len(enFmt) && strings.ContainsRune("dsv", rune(enFmt[i+1])) && ci < len(caps) {
			sb.WriteString(caps[ci])
			ci++
			i++
			continue
		}
		sb.WriteByte(enFmt[i])
	}
	return sb.String()
}
