// ============ md_marker_conserve_test.go · 职责说明 ============
// P1 Markdown 标记守恒的对抗性单测（2026-11-20）：用"主动删 **、把 <br> 写成 <br"
// 的破坏性假翻译器走真实提取→写回链路，验证写回层按源补回标记（**/~~/`/<br 守恒、
// 无 <br| 断裂）；另配 mdRestoreMarkers 纯函数用例钉住补回规则的边界。
// ============================================
package fileproc

import (
	"strings"
	"testing"
	"unicode"
)

// destructivePseudo 对抗性假翻译器：把汉字换成 "Zh"，同时**故意破坏结构标记**——
// 删掉成对的 **/~~/`（保留内容），把 <br> 写成 <br（吞掉右尖括号）。
// 用它模拟"模型改坏标记"，验证写回层能否按源 key 把标记补回去（P1/G5）。
func destructivePseudo(s string) string {
	var b strings.Builder
	inHan := false
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "~~") {
			i += 2 // 吞掉标记，保留后面的内容
			continue
		}
		if s[i] == '`' {
			i++
			continue
		}
		if strings.HasPrefix(strings.ToLower(s[i:]), "<br") {
			j := i + 3
			for j < len(s) && s[j] != '>' {
				j++
			}
			if j < len(s) {
				b.WriteString(s[i:j]) // 写 <br 但不写 '>'
				i = j + 1
				continue
			}
		}
		r := rune(s[i])
		if r < 0x80 {
			b.WriteByte(s[i])
			i++
			continue
		}
		// 汉字连续串压成单个 Zh
		decoded := []rune(s[i:])
		if unicode.Is(unicode.Han, decoded[0]) {
			if !inHan {
				b.WriteString("Zh")
				inHan = true
			}
			// 跳过一个 UTF-8 汉字（3 字节）
			i += 3
			continue
		}
		inHan = false
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func mdApplyDestructive(t *testing.T, md string) string {
	t.Helper()
	keys := mdKeys(t, md)
	tr := make(map[string]string, len(keys))
	for _, k := range keys {
		tr[k] = destructivePseudo(k)
	}
	return mdApply(t, md, tr)
}

// TestMdMarkerConserveUnderDestructiveTranslation 对抗性假翻译器删标记后，
// 写回层必须按源 key 把 **/<br> 补回去，计数守恒。
func TestMdMarkerConserveUnderDestructiveTranslation(t *testing.T) {
	md := strings.Join([]string{
		"| 指标 | 数值 |",
		"|---|---|",
		"| **覆盖率**<br>目标 | **40%+**<br>覆盖国家 |",
		"| ~~旧口径~~ 与 `shrink_draft` | 说明文字 |",
	}, "\n")

	got := mdApplyDestructive(t, md)
	for _, marker := range []string{"**", "~~", "`", "<br"} {
		if want, have := strings.Count(md, marker), strings.Count(strings.ToLower(got), strings.ToLower(marker)); have != want {
			t.Errorf("标记 %q 计数不守恒：源 %d / 产物 %d\n产物=%q", marker, want, have, got)
		}
	}
	// 具体形态：不得出现 <br| 断裂（吞掉 > 后与表格竖线粘连）
	if strings.Contains(strings.ToLower(got), "<br|") {
		t.Errorf("产物出现 <br| 断裂形态：%q", got)
	}
}

// TestMdRestoreMarkers 纯函数层：源标记丢失时按数量补回，没丢时不动。
func TestMdRestoreMarkers(t *testing.T) {
	cases := []struct{ src, tr, want string }{
		{"**覆盖率**", "覆盖率", "**覆盖率**"},
		// <br> 被破坏时先就地补 '>'；成对标记按"包裹主体"补回（位置可能落在 <br> 外侧，
		// 但计数守恒且包裹成立，HTML 渲染无害）——真实链路 span 更细，** 与 <br> 少有同 span）。
		{"**40%+**<br>覆盖国家", "40%+<br 覆盖国家", "**40%+<br>覆盖国家**"},
		{"~~旧~~", "旧", "~~旧~~"},
		{"`code`", "code", "`code`"},
		{"**已**有", "**已有**", "**已有**"}, // 译文标记数 >= 源，不动
		// ★ 2026-11-21 三轮：奇数缺口必须按**缺失侧**补，不许两头各加（那会造出 `****X**`）。
		{"**标题**", "**标题", "**标题**"},
		{"**标题**", "标题**", "**标题**"},
		{"**标题**", "标题", "**标题**"},
		{"无标记", "无标记", "无标记"},
	}
	for _, c := range cases {
		if got := mdRestoreMarkers(c.src, c.tr); got != c.want {
			t.Errorf("mdRestoreMarkers(%q, %q) = %q, want %q", c.src, c.tr, got, c.want)
		}
	}
}
