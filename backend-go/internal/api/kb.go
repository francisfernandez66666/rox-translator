package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"translator/internal/engine"
	"translator/internal/kb"
)

// ============ KB 识别/导入 ============

// kbTempDir 临时缓存目录（识别阶段保存文件，供导入阶段读取）
func (s *Server) kbTempDir() string {
	dir := filepath.Join(s.Cfg.UploadDir, "_kb_tmp")
	os.MkdirAll(dir, 0o755)
	return dir
}

func (s *Server) saveUploadedFile(r *http.Request) (string, error) {
	file, header, err := r.FormFile("file")
	if err != nil {
		return "", err
	}
	defer file.Close()
	os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := s.Cfg.UploadDir + "/kb_" + uniqueName(header.Filename)
	f, err := os.Create(savePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, file); err != nil {
		return "", err
	}
	return savePath, nil
}

// isSourceCol 判断是否是原文列（中文源列）
func isSourceCol(k string) bool {
	k2 := strings.ToLower(strings.TrimSpace(k))
	switch k2 {
	case "zh", "zh-cn", "cn", "chinese", "中文", "中文原文", "源语言", "source", "原文", "原语", "源文":
		return true
	}
	// "中文-zh" 这类 "别名-代码" 列名，代码为 zh 视为源列
	code := langCodeFromColName(k)
	return code == "zh"
}

// langCodeFromColName 从列名解析语言代码。
// 支持格式：
//   - 纯代码：en / ru / zh-TW
//   - 别名：英语 / english / 哈撒语(哈萨克语)
//   - 别名-代码：英语-en / 中文-zh / 繁体中文-zh-TW / 哈撒语-kz
func langCodeFromColName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	// 先尝试完整别名/代码直接匹配
	if code := engine.LangCodeFromName(trimmed); code != "" {
		return mapDBLangCode(code)
	}
	// 尝试 "别名-代码" 拆分：优先匹配 "zh-tw"/"zh-hant"/"zh-cn" 等复合代码
	lower := strings.ToLower(trimmed)
	compound := map[string]string{
		"zh-tw": "zh_hant", "zh-hant": "zh_hant", "zh-cn": "zh",
		"zh-hans": "zh", "zh-sg": "zh", "zh-mo": "zh_hant",
	}
	for comp, code := range compound {
		if strings.HasSuffix(lower, "-"+comp) || strings.HasSuffix(lower, comp) && len(trimmed) == len(comp) {
			return mapDBLangCode(code)
		}
	}
	if idx := strings.LastIndex(lower, "-"); idx > 0 {
		codePart := strings.TrimSpace(lower[idx+1:])
		// 直接代码部分（如 en / ru / zh-TW / kz）
		if c := normalizeCodePart(codePart); c != "" {
			return c
		}
		// 别名部分（如 "哈撒语" / "繁体中文"）
		aliasPart := strings.TrimSpace(trimmed[:idx])
		if c := engine.LangCodeFromName(aliasPart); c != "" {
			return mapDBLangCode(c)
		}
	}
	return ""
}

// normalizeCodePart 规范化语言代码片段（含大小写、zh-TW、kz 等特例）
func normalizeCodePart(part string) string {
	p := strings.ToLower(strings.TrimSpace(part))
	if p == "" {
		return ""
	}
	// zh-TW / zh-Hant → 繁体中文
	if p == "zh-tw" || p == "zh-hant" || p == "zht" {
		return "zh_hant"
	}
	// zh-CN / zh-Hans → 简体（源）
	if p == "zh" || p == "zh-cn" || p == "zh-hans" || p == "cn" {
		return "zh"
	}
	// 哈萨克语 kz（ISO 3166 国家码）→ kk（ISO 639 语言码）
	if p == "kz" {
		return "kk"
	}
	// 已有别名表直接匹配（en / ru / it ...）
	if c := engine.LangCodeFromName(p); c != "" {
		return mapDBLangCode(c)
	}
	// 通用：2~3 字母小写代码
	if len(p) == 2 && isAlphaLower(p) {
		return mapDBLangCode(p)
	}
	return ""
}

// mapDBLangCode 语言代码 → DB 列名（id 列为 id_lang）
func mapDBLangCode(code string) string {
	if code == "id" {
		return "id_lang"
	}
	return code
}

func isAlphaLower(s string) bool {
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// kbRecognizeMeta 识别缓存元信息
type kbRecognizeMeta struct {
	FilePath string `json:"file_path"`
	TempID   string `json:"temp_id"`
	Created  int64  `json:"created"`
}

func (s *Server) handleRecognizeKB(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	savePath, err := s.saveUploadedFile(r)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文件上传失败"})
		return
	}

	records, allCols, err := kb.ParseKBFile(savePath)
	if err != nil {
		os.Remove(savePath)
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "识别失败: " + err.Error()})
		return
	}
	if len(records) == 0 {
		os.Remove(savePath)
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "文件无有效数据"})
		return
	}

	// 识别语言列：跳过源列 + 非语言列（country_code 等无 "-" 且非别名的列）
	var langCols []string
	for _, col := range allCols {
		if isSourceCol(col) {
			continue
		}
		if code := langCodeFromColName(col); code != "" {
			langCols = append(langCols, col)
		}
	}
	if len(langCols) == 0 {
		langCols = allCols
	}

	// 新语言：不在现有 KB 语言列中的代码
	var newLangs []string
	for _, col := range langCols {
		code := langCodeFromColName(col)
		if code == "" || code == "zh" {
			continue
		}
		found := false
		for _, lc := range kb.AllLangs {
			if lc == code {
				found = true
				break
			}
		}
		if !found {
			dup := false
			for _, nl := range newLangs {
				if nl == code {
					dup = true
					break
				}
			}
			if !dup {
				newLangs = append(newLangs, code)
			}
		}
	}

	// 预览前 5 条（zh 源值 + 各语言列，排除源列）
	preview := []map[string]interface{}{}
	for i := 0; i < len(records) && i < 5; i++ {
		m := map[string]interface{}{}
		var zhVal string
		for k, v := range records[i] {
			if isSourceCol(k) {
				if zhVal == "" {
					zhVal = v
				}
				continue
			}
			if code := langCodeFromColName(k); code != "" {
				m[k] = v
			}
		}
		m["zh"] = zhVal
		preview = append(preview, m)
	}

	// ★ 生成 temp_id 并缓存文件路径（识别后保留文件，供 import-kb 读取）
	meta := kbRecognizeMeta{FilePath: savePath, TempID: randHex(12), Created: time.Now().Unix()}
	metaBytes, _ := json.Marshal(meta)
	metaPath := filepath.Join(s.kbTempDir(), "kb_"+meta.TempID+".json")
	if err := os.WriteFile(metaPath, metaBytes, 0o644); err != nil {
		os.Remove(savePath)
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "临时缓存写入失败"})
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"success": true, "preview": preview,
		"columns": langCols, "total_rows": len(records), "total": len(records),
		"lang_cols": langCols, "new_langs": newLangs, "message": "识别成功",
		"temp_id": meta.TempID,
	})
}

// randHex 生成 n 字节随机 hex 串（crypto/rand）
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		ts := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(ts >> (8 * uint(i%8)))
		}
	}
	return hex.EncodeToString(b)[:n]
}

func (s *Server) handleImportKB(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	// ★ 协议：前端仅提交 {temp_id}，从缓存读取之前保存的文件
	var req struct {
		TempID string `json:"temp_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TempID == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少 temp_id"})
		return
	}

	metaPath := filepath.Join(s.kbTempDir(), "kb_"+req.TempID+".json")
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "识别数据已过期，请重新上传识别"})
		return
	}
	var meta kbRecognizeMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil || meta.FilePath == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "识别数据无效，请重新上传"})
		return
	}
	savePath := meta.FilePath
	defer func() {
		os.Remove(savePath)
		os.Remove(metaPath)
	}()

	records, _, err := kb.ParseKBFile(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "解析失败: " + err.Error()})
		return
	}
	if len(records) == 0 {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "文件无有效数据"})
		return
	}

	added := 0
	skipped := 0
	tid := s.currentTenant(r)
	for _, rec := range records {
		var src string
		for k, v := range rec {
			if isSourceCol(k) {
				src = v
				break
			}
		}
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		translations := map[string]string{}
		for k, v := range rec {
			if v == "" || isSourceCol(k) {
				continue
			}
			code := langCodeFromColName(k)
			if code == "" || code == "zh" {
				continue
			}
			translations[code] = v
		}
		if len(translations) == 0 {
			skipped++
			continue
		}
		if _, err := s.DB.SaveBack(src, translations, "imported", tid); err != nil {
			skipped++
			continue
		}
		added++
	}

	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "导入完成", "added": added, "skipped": skipped,
	})
}
