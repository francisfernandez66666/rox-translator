// ============================================================================
// internal/api/docs_manual.go — 12 语种《产品手册》PDF 的公网下载面
// （★ F-69，2026-09-26 批 I-8 发布面）
//
// 缺陷因果链（为什么必须有这一条路由）：
//
//	① 批H 已把 12 份语种手册铺到服务端 manual_pdf_dir，注册邮件附件也按语种取稿
//	   （mail_tpl.go 的 loadManualPDF）；
//	② 但 server.go 只注册了 /docs/terms|sla|privacy，**全仓没有 /docs/manual 路由**，
//	   spa.go 的 "/" 兜底对未知路径回 **200 + 整页 index.html**（实测 2,591 B 壳）
//	   ⇒ 客户按 /docs/manual 找 PDF 拿到的是 SPA 空壳，还以为是「手册坏了」；
//	③ 这正是 AGENTS §一·6 写的「托管物只判 200 是无效断言」同一陷阱的服务端一侧形态：
//	   状态码绿、内容根本不是那个东西。
//
// 改法口径（严格按修复文档 8.10，不重写回落链）：
//
//	· 语种码**只用既有解析函数**：normalizeMailLang（12 码白名单）+ loadManualPDF
//	  （精确语种 → en → zh 的回落链，以及旧单文件兜底链），本文件不复制一遍判定；
//	· 白名单外的码直接 400，**绝不**静默回落到中文——「下载了日文手册却拿到中文」
//	  就是 F-64 那类「状态码诚实、内容说谎」的复发；
//	· 未铺库时回 404（JSON 错误体），**绝不**让请求漏到 SPA 兜底变成 200 HTML；
//	· 出门前校验 %PDF 魔数：目录里放错文件（HTML/图片改名 .pdf）时如实报错，
//	  不把坏字节当 PDF 发给客户。
//
// 产品口径（本批的取舍，写在代码里免得下次再议）：手册是对外说明材料，与
// /docs/terms|sla|privacy 同级公开，**不加登录/套餐门槛**；风险面只有「文件体积」，
// 已由 Cache-Control 短缓存 + 无租户数据兜住。
//
// 闸门：internal/api/docs_manual_test.go（12 语种逐码探针：200 + application/pdf
// + %PDF 魔数 + 体积下限 + 非 HTML 兜底，另锁白名单外/未铺库/坏文件三条负向路径）。
// ============================================================================
package api

import (
	"bytes"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
)

// manualPDFPrefix 手册下载路由前缀（注册串与本常量必须逐字一致，见 server.go）。
const manualPDFPrefix = "/docs/manual/"

// manualPDFSuffix 文件名后缀（只认 .pdf，大小写不敏感）。
const manualPDFSuffix = ".pdf"

// sanitizeManualFilename 把命中文件名收敛成 ASCII 安全串（老单文件链的名称不可控，
// Content-Disposition 里塞引号/换行会破坏响应头，非 ASCII 一律折成 '-' 占位）。
// 返回串保证**无引号、无换行、无斜杠**且以 .pdf 结尾（响应头兜底名与单测等值判据同源）。
func sanitizeManualFilename(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			out = append(out, r)
		default:
			out = append(out, '-')
		}
	}
	s := strings.Trim(string(out), "-.")
	if !strings.HasSuffix(strings.ToLower(s), manualPDFSuffix) {
		s += manualPDFSuffix
	}
	if s == manualPDFSuffix {
		return "manual.pdf" // 只剩后缀（原名全非 ASCII 或全点）时给个中性兜底名
	}
	return s
}

// handleManualPDFDownload GET/HEAD /docs/manual/{语种码}.pdf —— 手册 PDF 直出（公开只读）。
// 路由挂在 "/" 兜底之前（ServeMux 前缀更长者优先），因此未命中的语种码不会再生成 SPA 空壳。
func (s *Server) handleManualPDFDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "手册下载仅支持 GET"))
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, manualPDFPrefix)
	lower := strings.ToLower(raw)
	if !strings.HasSuffix(lower, manualPDFSuffix) {
		// 目录索引没有意义（12 份文件是固定集合），按 NOT_FOUND 给一条明确形状提示
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "手册地址形如 /docs/manual/<语种码>.pdf（例：/docs/manual/zh.pdf）"))
		return
	}
	// 语种码归一：连字符/大写都先收敛（zh-hant → zh_hant），再过 12 码白名单。
	// ★ 这一步同时是**路径穿越闸门**：白名单外的任何串（含 "../"）都到不了 loadManualPDF。
	code := normalizeMailLang(strings.TrimSuffix(lower, manualPDFSuffix))
	if code == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "不支持的手册语种（可选：zh/zh-hant/en/ru/fr/ar/es/pt/de/ja/ko/th）"))
		return
	}
	data, path, err := s.loadManualPDF(code)
	if err != nil || len(data) == 0 {
		// 手册未铺库/该语种及其回落链都没有：如实 404（**绝不**回 200 整页 HTML）
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "该产品手册尚未上传，请联系平台管理员"))
		return
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		// 目录里放的不是 PDF：宁红不假绿（F-44 同族教训：读侧要真判内容，不能只判取到字节）
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "手册文件格式异常（非 PDF），已停止下载"))
		return
	}
	// 文件名回显**实际命中**的那份（回落链走了 en/zh 时文件名如实显示，不假装是请求语种）。
	// 老单文件链的文件名由运维配置、不可控，故双写：ASCII 兜底名（防引号/换行注入响应头）
	// + RFC 5987 的 filename*（非 ASCII 名如「用户手册.pdf」在浏览器里仍可正常显示）。
	base := filepath.Base(path)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition",
		"inline; filename=\""+sanitizeManualFilename(base)+"\"; filename*=UTF-8''"+url.PathEscape(base))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=600")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return // HEAD 只要头不要体（冒烟脚本探针用）
	}
	_, _ = w.Write(data)
}
