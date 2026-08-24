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
	"net/http"
	"os"
	"strconv"

	"translator/internal/fileproc"
	"translator/internal/kb"
)

// handleImportBitext 双语语料导入接口（tenant_admin/dept_admin）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart 文件，xlsx/xls/csv）。
// 返回: success=true 时携带 added（写入行×语言数）/skipped（空源文或失败跳过数）。
func (s *Server) handleImportBitext(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	savePath, err := s.saveUploadedFile(r)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文件上传失败"})
		return
	}
	defer os.Remove(savePath) // 用完即删（对照表不落库留痕）

	records, allCols, err := kb.ParseKBFile(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "解析失败: " + err.Error()})
		return
	}
	if len(records) == 0 {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "文件无有效数据"})
		return
	}

	tid := s.effTenant(r, u)
	added, skipped := 0, 0
	for _, rec := range records {
		// 源文本：识别到的源列优先（zh/source/src），否则取第一列
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
		// 目标语言列 → 逐语言写入 TM
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
		if _, err := s.DB.SaveBack(src, trans, "bitext", tid); err != nil {
			skipped++
			continue
		}
		added += len(trans)
	}
	s.Store.LogAudit(tid, u.ID, "bitext_import", "tm_segments", strconv.Itoa(added))
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "added": added, "skipped": skipped,
	})
}

// trimSpace 本地空白裁剪别名。
func trimSpace(s string) string {
	b := []rune(s)
	i, j := 0, len(b)
	for i < j && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
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
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	savePath, err := s.saveUploadedFile(r)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文件上传失败"})
		return
	}
	defer os.Remove(savePath)
	tus, err := fileproc.ParseTMX(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "TMX 解析失败: " + err.Error()})
		return
	}
	if len(tus) == 0 {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "TMX 无有效双语单元（需 ≥2 种语言的 tu）"})
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
		if _, err := s.DB.SaveBack(src, trans, "tmx", tid); err != nil {
			skipped++
			continue
		}
		added += len(trans)
	}
	s.Store.LogAudit(tid, u.ID, "tmx_import", "tm_segments", strconv.Itoa(added))
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "tus": len(tus), "added": added, "skipped": skipped,
	})
}
