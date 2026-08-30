// ============ xmltext.go · 职责说明 ============
// fileproc 包 OOXML 文本转义/反转义工具。
//
// 背景（审计发现的两个真实缺陷，本工具一并修复）：
//  1. 写回未转义：docx/pptx 的译文此前被以裸字符串直接拼入 document.xml / slideN.xml，
//     译文一旦含 & < > " '（如 "R&D"、"<tag>"）即产出非法 XML，Office/WPS 打开报「文件损坏」；
//  2. 提取实体不一致：提取侧走 encoding/xml 解析（文本已自动反转义为 "R&D"），
//     而写回侧的手写扫描器拿到的是含实体的原始串 "R&amp;D"——两侧键不匹配，
//     含特殊字符的段落永远无法命中译文，且发给大语言模型的原文也是带实体的脏文本。
//
// 使用约定：
//   - 读（匹配键）：paragraphRunText / rawPptxText 等手写扫描器的输出必须先过 UnescapeXMLText，
//     与 encoding/xml 提取侧对齐；
//   - 写（注入译文）：任何把大语言模型译文拼回 OOXML 字符串的位置必须先过 EscapeXML。
// =============================================
package fileproc

import (
	"strings"
)

// xmlEscaper 替换表：OOXML 文本节点中必须转义的五个预定义实体。
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
	"'", "&apos;",
)

// xmlUnescaper 反向替换表：与 xmlEscaper 一一对应。
// 注意 &amp; 必须最后处理（否则会被二次还原），strings.NewReplacer 按单遍扫描
// 最长匹配且不重复替换，天然避免 "&amp;lt;" → "&lt;" → "<" 的连锁问题。
var xmlUnescaper = strings.NewReplacer(
	"&lt;", "<",
	"&gt;", ">",
	"&quot;", "\"",
	"&apos;", "'",
	"&amp;", "&",
)

// EscapeXML 将任意文本转义为可安全嵌入 OOXML/XML 文本节点的形式。
// 参数 s: 原始文本（通常为 LLM 译文）；返回: 转义后文本。
// 所有 docx/pptx 写回路径在拼接译文前必须调用本函数。
func EscapeXML(s string) string {
	return xmlEscaper.Replace(s)
}

// UnescapeXMLText 将 XML 文本中的预定义实体还原为字面字符。
// 参数 s: 手写扫描器从 XML 中截取的原始内文；返回: 与 encoding/xml 解析结果一致的纯文本。
// 所有以字符串扫描方式读取 OOXML 文本的位置必须调用本函数，保证与提取侧键一致。
func UnescapeXMLText(s string) string {
	return xmlUnescaper.Replace(s)
}
