// ============ bitext.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 双语语料对齐导入（第四批）：把客户已有的双语对照表（xlsx/xls/csv）批量写入
// 翻译记忆库（tm_segments，module=bitext），冷启动即有 TM 命中率。
//   - POST /api/translation/import-bitext（multipart：file；需部门管理员及以上）
//   - 解析复用 kb.ParseKBFile（源列识别 + 语言列识别与 KB 导入同规则）
//   - 逐行逐语言 kb.SaveBack 写入（zh_hash 幂等去重，重复导入自动覆盖）
// 返回 added/skipped 计数供前端提示。
// =============================================

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"translator/internal/store"

	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
	"translator/internal/kb"
)

// handleImportBitext 双语语料导入接口（tenant_admin/dept_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart 文件，xlsx/xls/csv）。
// 返回: success=true 时携带 added（写入行×语言数）/skipped（空源文或失败跳过数）。
func (s *Server) handleImportBitext(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需部门管理员及以上权限
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 检查知识库是否已加载
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	// 保存上传的对照表文件
	savePath, err := s.saveUploadedFile(r)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文件上传失败"})
		return
	}
	defer os.Remove(savePath) // 用完即删（对照表不落库留痕）

	// 解析对照表文件（复用 KB 解析逻辑）
	records, allCols, err := kb.ParseKBFile(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "解析失败: " + err.Error()})
		return
	}
	if len(records) == 0 {
		// ★ F-64②（批 I-10）：对照表解析出 0 行＝上传件本身没有内容可导（换文件即可）→ 400；
		//   旧 200 + success:false 与「导入成功但新增 0 条」在客户端里不可区分。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "文件无有效数据"))
		return
	}

	tid := s.effTenant(r, u)
	added, skipped := 0, 0
	// 逐行解析并写入翻译记忆库
	for _, rec := range records {
		// 提取源文本：识别到的源列优先（zh/source/src），否则取第一列
		src := ""
		for _, col := range allCols {
			if isSourceCol(col) {
				src = rec[col]
				break
			}
		}
		if src == "" && len(allCols) > 0 {
			src = rec[allCols[0]]
		}
		src = trimSpace(src)
		if src == "" {
			skipped++
			continue
		}
		// 提取各语言列的译文
		trans := map[string]string{}
		for _, col := range allCols {
			if isSourceCol(col) {
				continue
			}
			code := langCodeFromColName(col)
			if code == "" {
				code = col // 兜底：把列名当语言码（en 等直命名场景）
			}
			v := trimSpace(rec[col])
			if v != "" {
				trans[code] = v
			}
		}
		if len(trans) == 0 {
			skipped++
			continue
		}
		// 整改 R7：逐语言生成待审候选（此前仅取首个语言，多语种 TM 冷启动数据大量丢失）
		for lc, tv := range trans {
			if err := s.Store.CreateTmReview(&store.TmReview{TenantID: tid, Zh: src, Lang: lc, Trans: tv, Source: "bitext", RefType: "import"}); err != nil {
				skipped++
				continue
			}
			added++
		}
	}
	// 记录审计日志
	s.Store.LogAudit(tid, u.ID, "bitext_import", "tm_segments", strconv.Itoa(added))
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "added": added, "skipped": skipped,
	})
}

// trimSpace 本地空白裁剪别名。
func trimSpace(s string) string {
	b := []rune(s)
	i, j := 0, len(b)
	// 从前向后跳过前导空白字符
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	// 从后向前跳过后缀空白字符
	for j > i && (b[j-1] == ' ' || b[j-1] == '\t' || b[j-1] == '\n' || b[j-1] == '\r') {
		j--
	}
	return string(b[i:j])
}

// handleImportTMX TMX 翻译记忆标准格式导入接口（部门管理员及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart 文件，.tmx/.xml）。
// 返回: success=true 时携带 tus（有效翻译单元数）/added（写入行×语言数）/skipped。
func (s *Server) handleImportTMX(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	// 上传→解析链：临时文件用完即删；TMX 需含 ≥2 语言的有效 tu 才继续
	// ★ #38：走 TMX 专用白名单（.tmx/.xml），并把真实校验失败原因回给前端
	//   （旧实现一律吞成「文件上传失败」，用户无从知道是类型不对还是超了 20MB）
	savePath, err := s.saveUploadedFileWith(r, tmxExtWhitelist)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	defer os.Remove(savePath)
	tus, err := fileproc.ParseTMX(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "TMX 解析失败: " + err.Error()})
		return
	}
	if len(tus) == 0 {
		// ★ F-64②（批 I-10）：TMX 里没有 ≥2 语言的翻译单元＝文件不符合 TMX 导入口径（客户端改文件即可）→ 400；
		//   旧 200 壳让前端把「一个都没导」当成导入成功。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "TMX 无有效双语单元（需 ≥2 种语言的 tu）"))
		return
	}
	tid := s.effTenant(r, u)
	added, skipped := 0, 0
	for _, tu := range tus {
		// 源语言：优先 zh，否则取字典序第一个语言（SaveBack 以 zh 列为唯一键）
		srcLang := "zh"
		if _, ok := tu.Variants[srcLang]; !ok {
			for lc := range tu.Variants {
				if srcLang == "" || lc < srcLang {
					srcLang = lc
				}
			}
		}
		src := tu.Variants[srcLang]
		trans := map[string]string{}
		for lc, v := range tu.Variants {
			if lc != srcLang {
				trans[lc] = v
			}
		}
		// 整改 R7：逐语言生成待审候选（此前仅取首个语言）
		for lc, tv := range trans {
			if err := s.Store.CreateTmReview(&store.TmReview{TenantID: tid, Zh: src, Lang: lc, Trans: tv, Source: "tmx", RefType: "import"}); err != nil {
				skipped++
				continue
			}
			added++
		}
	}
	s.Store.LogAudit(tid, u.ID, "tmx_import", "tm_segments", strconv.Itoa(added))
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "tus": len(tus), "added": added, "skipped": skipped,
	})
}

// firstLang 取译文映射的首个语言键（保留供其他导入路径复用）。
func firstLang(m map[string]string) string {
	// 返回映射中任意一个语言键（Go 遍历顺序随机，取首个即可）
	for k := range m {
		return k
	}
	return "en"
}

// firstVal 取译文映射的首个值。
func firstVal(m map[string]string) string {
	for _, v := range m {
		return v
	}
	return ""
}

// ============ ★ H12 TMX 导出（Trados / memoQ 桥接） ============
//
// Trados Studio 与 memoQ 均以 TMX 1.x 为标准交换格式：本端点把当前租户
// 翻译记忆（tm_segments）导出为 .tmx，可被客户工具「导入 TM」直接落库；
// 反向通道沿用已有 /api/translation/import-tmx，形成双向同步闭环。
// ?lang=de 可仅导出目标语言非空的句对（增量迁移常用）。

// buildTMX 把行集渲染为 TMX 1.4 文档（纯函数，可脱库单测）。
func buildTMX(rows []*kb.Row, moduleFilter string) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<tmx version="1.4">` + "\n")
	sb.WriteString(`<header creationtool="LangCross 能言" creationtoolversion="2.0" segtype="sentence" adminstatus="managed" datatype="plain text" srclang="zh-CN" ta="langcross" />` + "\n")
	sb.WriteString("<body>\n")
	// 逐行渲染：空源文/模块不符/无有效译文跳过；tuv 按语言字典序输出，内容统一 XML 转义
	for _, r := range rows {
		if r == nil || strings.TrimSpace(r.Zh) == "" {
			continue
		}
		if moduleFilter != "" && r.Module != moduleFilter {
			continue
		}
		// 收集非空目标语言对（排除 zh 源列）；langs 排序保证导出字节级稳定（可脱库单测）
		pairs := map[string]string{}
		for lc, v := range r.Langs {
			if strings.TrimSpace(v) != "" && lc != "zh" {
				pairs[lc] = v
			}
		}
		if len(pairs) == 0 {
			continue
		}
		langs := make([]string, 0, len(pairs))
		for lc := range pairs {
			langs = append(langs, lc)
		}
		sort.Strings(langs)
		sb.WriteString(fmt.Sprintf("<tu tuid=\"%s\" datatype=\"plaintext\">\n", xmlEscape(kb.MD5Hex(r.Zh))))
		sb.WriteString("<tuv xml:lang=\"zh-CN\"><seg>" + xmlEscape(r.Zh) + "</seg></tuv>\n")
		for _, lc := range langs {
			sb.WriteString("<tuv xml:lang=\"" + tmxLangTag(lc) + "\"><seg>" + xmlEscape(pairs[lc]) + "</seg></tuv>\n")
		}
		sb.WriteString("</tu>\n")
	}
	sb.WriteString("</body>\n</tmx>\n")
	return []byte(sb.String())
}

// tmxLangTag 语言代码 → BCP47 标签（Trados/memoQ 识别度高的常用映射，其余原样）。
func tmxLangTag(lc string) string {
	switch lc {
	case "en":
		return "en-US"
	case "zh_hant":
		return "zh-TW"
	case "pt":
		return "pt-BR"
	case "he":
		return "he-IL"
	case "id_lang":
		return "id-ID"
	}
	return lc
}

// xmlEscape 对文本做 XML 转义（防止 TMX 导出内容破坏文档结构）。
func xmlEscape(str string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(str))
	return b.String()
}

// handleExportTMX GET /api/translation/export-tmx[?lang=xx&module=approved]
// 鉴权：部门管理员及以上；租户上下文与 KB 查询口径一致（kbTenant）。
func (s *Server) handleExportTMX(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "知识库未加载"})
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	mod := strings.TrimSpace(r.URL.Query().Get("module"))
	rows, qErr := s.DB.ExportRows(s.kbTenant(r, u), lang)
	if qErr != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": qErr.Error()})
		return
	}
	doc := buildTMX(rows, mod)
	filename := fmt.Sprintf("langcross_tm_%s.tmx", time.Now().UTC().Format("20060102T150405"))
	w.Header().Set("Content-Type", "application/x-tmx; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	_, _ = w.Write(doc)
}
