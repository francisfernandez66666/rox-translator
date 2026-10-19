// ============ content_disposition.go · 职责说明 ============
// Content-Disposition 响应头的**唯一出口函数**（AGENTS §一·5「对外报价只有一个事实源」同族纪律：
// 全包 20 处下载点不再各自拼头，历史形态是「只有 3 处过 mimeEscape、17 处直接拼名」，
// 于是中文文件名以裸 UTF-8 字节写进 filename=（curl/SDK/直调一律读成乱码），
// 且引号/换行/路径分隔符可注入响应头）。
//
// 输出两个参数：
//
//	attachment; filename="<ASCII 兜底>"; filename*=UTF-8''<percent 编码真名>
//
// 现代浏览器优先取 filename*（RFC 5987），拿到完整非 ASCII 真名；老客户端退回 ASCII 兜底名。
// =============================================
package api

import (
	"path/filepath"
	"strings"
)

// contentDisposition 构造下载响应头的值（全包唯一出口，见文件头）。
// 参数 filename: 期望的下载文件名（可含非 ASCII）。返回: 完整的 Content-Disposition 头值。
func contentDisposition(filename string) string {
	name := sanitizeDownloadName(filename)
	fallback := asciiFallbackName(name)
	star := name
	if star == "" {
		star = fallback
	}
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + rfc5987Encode(star)
}

// sanitizeDownloadName 收敛下载名：剥掉任何目录成分（`/`、`\`）、去掉 CR/LF/NUL 与 "."/".." 退化名。
// 防的是「路径分隔符进响应头」与「换行注入」；非 ASCII 不动（真名交给 filename*）。
// 参数 name: 原始名。返回: 净化后的基础名（可能为空串）。
func sanitizeDownloadName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// asciiFallbackName 生成 ASCII 兜底名：只保留 ASCII 安全字符（字母/数字/点/下划线/连字符），
// 其余（含非 ASCII、引号、空格、控制字符）一律丢弃；主干被丢空时回落 download.<ext>。
// 参数 name: 已过 sanitizeDownloadName 的基础名。返回: 保证无引号/换行/斜杠/非 ASCII 的兜底名。
func asciiFallbackName(name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
				b.WriteRune(r)
			}
		}
		return strings.Trim(b.String(), ".-_")
	}
	cs := clean(stem)
	ce := clean(ext)
	if cs == "" {
		cs = "download"
	}
	if ce != "" {
		return cs + "." + ce
	}
	return cs
}

// rfc5987Encode 按 RFC 5987 的 attr-char 集合对 UTF-8 字节做百分号编码（大写十六进制）。
// 只保留 `A-Za-z0-9!#$&+-.^_`|~`，其余逐字节编码——引号、空格、`'`、`(`、`)`、`*`、`,` 都在编码之列。
// 参数 s: 任意字符串。返回: 可直接拼进 `filename*=UTF-8”` 的编码串。
func rfc5987Encode(s string) string {
	const upper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			strings.IndexByte("!#$&+-.^_`|~", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upper[c>>4])
		b.WriteByte(upper[c&0x0f])
	}
	return b.String()
}
