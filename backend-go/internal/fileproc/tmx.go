// ============ tmx.go · 职责说明 ============
// fileproc 包 TMX（Translation Memory eXchange，翻译记忆交换标准 XML 格式）导入实现。
//   - ParseTMX：流式解析 TMX 文件 → 双语记录 []TMXTU（每个 tu 的 语言→译文 映射）
//   - xml:lang 归一化：zh-CN/zh_CN/zh-Hans → zh，en-US → en 等（与 KB 语言码对齐）
//
// 导入目标：tm_segments 翻译记忆库（module=tmx），与双语 xlsx/csv 导入同一落库链路。
// =============================================
package fileproc

import (
	"encoding/xml"
	"os"
	"strings"
)

// TMXTU 单条翻译单元：源文+各语言译文的映射。
type TMXTU struct {
	Variants map[string]string // 语言代码 → 译文
}

// tmxDoc TMX 文档的 XML 结构定义（只取 header@srclang 与 body/tu/tuv/seg 层级）。
type tmxDoc struct {
	Header struct {
		Srclang string `xml:"srclang,attr"` // 默认源语言（当前未用，保留解析）
	} `xml:"header"`
	Body struct {
		Tus []tmxTU `xml:"tu"`
	} `xml:"body"`
}

// tmxTU 单个 <tu>：若干 <tuv xml:lang=".."><seg>..</seg></tuv>
type tmxTU struct {
	Tuvs []struct {
		Lang string `xml:"lang,attr"`
		Seg  struct {
			Text  string `xml:",chardata"`
			Inner string `xml:",innerxml"`
		} `xml:"seg"`
	} `xml:"tuv"`
}

// NormalizeLangCode TMX 语言标识归一化为平台语言码：
// 取主子标签小写；zh 变体（zh-CN/zh_TW/zh-Hans/Hant）统一 zh / zh_hant。
func NormalizeLangCode(lang string) string {
	l := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(lang)), "_", "-")
	parts := strings.SplitN(l, "-", 2)
	main := parts[0]
	switch main {
	case "zh":
		if len(parts) > 1 && (strings.HasPrefix(parts[1], "hant") || strings.HasPrefix(parts[1], "tw") || strings.HasPrefix(parts[1], "hk")) {
			return "zh_hant"
		}
		return "zh"
	case "en", "ja", "ko", "de", "fr", "es", "ru", "pt", "it", "th", "vi", "ar":
		return main
	default:
		if main == "" {
			return ""
		}
		return main // 未知语言保留主子标签（引擎侧按码透传）
	}
}

// ParseTMX 解析 TMX 文件为翻译单元列表。
// 参数：path=TMX 文件路径；返回记录切片与错误。空 seg 与单语言 tu 自动跳过。
func ParseTMX(path string) ([]*TMXTU, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc tmxDoc
	if err := xml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	out := make([]*TMXTU, 0, len(doc.Body.Tus))
	for _, tu := range doc.Body.Tus {
		rec := &TMXTU{Variants: map[string]string{}}
		for _, tuv := range tu.Tuvs {
			code := NormalizeLangCode(tuv.Lang)
			text := strings.TrimSpace(tuv.Seg.Text)
			if text == "" {
				// seg 含内联标记（如 <b>）时 chardata 可能为空但 innerxml 有内容：剥标签兜底
				text = strings.TrimSpace(stripXMLTags(tuv.Seg.Inner))
			}
			if code == "" || text == "" {
				continue
			}
			if _, dup := rec.Variants[code]; !dup {
				rec.Variants[code] = text
			}
		}
		if len(rec.Variants) >= 2 { // 至少两种语言才有对齐价值
			out = append(out, rec)
		}
	}
	return out, nil
}

// stripXMLTags 剥除字符串中的 XML 标签仅留文本（简单状态机，够用即可）。
func stripXMLTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
