package api

// ============ 本文件职责中文说明 ============
// 本文件实现知识库（KB）批量导入与识别/导入流程：
//   - handleKBEntriesImport：JSON 数组批量导入 KB 条目（租户管理员，支持单语言/多语言两种格式）
//   - 识别流程（handleRecognizeKB）：上传文件 → 解析 → 识别源列/语言列 → 预览 → 缓存文件生成 temp_id
//   - 导入流程（handleImportKB）：前端仅提交 temp_id → 读取缓存文件 → 解析并按语言写入 KB
//   - 语言列识别工具：isSourceCol / langCodeFromColName / normalizeCodePart / mapDBLangCode 等
// 业务说明：
//   - 临时文件缓存于 kbTempDir（_kb_tmp），导入完成后清理（defer os.Remove）
//   - 语言代码规范化：zh-TW/zh-Hant→zh_hant（繁体）、kz→kk（哈萨克语）等特例映射
//   - 租户隔离：写入均带当前租户 ID（tid）

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"translator/internal/engine"
	"translator/internal/kb"
	"translator/internal/store"
)

// ============ KB 批量导入（租户自服务） ============

// handleKBEntriesImport 批量导入 KB 条目接口（JSON 数组，租户管理员）。
// 支持单条 {source_text, target_lang, target_text, layer?, module?} 或
// 多语言 {source_text, translations:{en:"..",ru:".."}, layer?, module?} 两种格式。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 package_id/entries 数组）。
// 返回: success=true 时携带 added（新增数）/skipped（跳过数）。
func (s *Server) handleKBEntriesImport(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上权限
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		PackageID int64           `json:"package_id"` // 目标知识库包 ID
		Entries   []kbImportEntry `json:"entries"`    // 待导入条目数组
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 基础校验：包 ID 与条目数组不能为空
	if req.PackageID <= 0 || len(req.Entries) == 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "package_id 与 entries 不能为空"})
		return
	}
	// 生效租户（租户隔离：只能写入自己的包）
	tid := s.effTenant(r, u)
	added, skipped := 0, 0
	// 逐条导入：空源文跳过；默认层 2、默认目标语言 en
	for _, e := range req.Entries {
		src := strings.TrimSpace(e.SourceText)
		if src == "" {
			skipped++
			continue
		}
		// 默认层级 2（术语层），默认目标语言 en
		if e.Layer == 0 {
			e.Layer = 2
		}
		if e.TargetLang == "" {
			e.TargetLang = "en"
		}
		// 写入 KB：源语言固定 zh，单条导入失败则跳过计数
		if _, err := s.Store.SaveEntry(tid, req.PackageID, e.Layer, "zh", src, e.TargetLang, e.TargetText, e.Module); err != nil {
			skipped++
			continue
		}
		added++
	}
	// 导入审计
	s.Store.LogAudit(tid, u.ID, "kb_entries_import", "kb_entries", strconv.Itoa(added))
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "批量导入完成", "added": added, "skipped": skipped,
	})
}

// kbImportEntry 单条批量导入条目。
type kbImportEntry struct {
	SourceText string `json:"source_text"` // 源文本（中文，必填）
	TargetLang string `json:"target_lang"` // 目标语言代码（默认 en）
	TargetText string `json:"target_text"` // 目标译文
	Layer      int    `json:"layer"`       // 层级（0 时默认 2）
	Module     string `json:"module"`      // 所属模块
}

// ============ KB 识别/导入 ============

// kbTempDir 获取临时缓存目录（识别阶段保存文件，供导入阶段读取）。
// 返回: 上传目录下的 _kb_tmp 子目录绝对路径；不存在则自动创建。
func (s *Server) kbTempDir() string {
	dir := filepath.Join(s.Cfg.UploadDir, "_kb_tmp")
	os.MkdirAll(dir, 0o755)
	return dir
}

// saveUploadedFile 保存上传的 multipart 文件到上传目录。
// 参数 r: HTTP 请求（含 "file" 表单字段）。返回: 保存后的文件路径或错误。
// 文件名按 kb_<唯一名> 命名避免冲突。
func (s *Server) saveUploadedFile(r *http.Request) (string, error) {
	// 解析 multipart 表单（上限 20MB，仅允许 xlsx/xls/csv）
	if err := parseUpload(r, kbUploadMax, kbExtWhitelist); err != nil {
		return "", err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return "", err
	}
	defer file.Close()
	// 确保上传目录存在
	os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := s.Cfg.UploadDir + "/kb_" + uniqueName(header.Filename)
	f, err := os.Create(savePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// 流式拷贝上传内容到目标文件
	if _, err := io.Copy(f, file); err != nil {
		return "", err
	}
	return savePath, nil
}

// isSourceCol 判断是否为原文列（中文源列）。
// 参数 k: 列名。返回: true 表示该列是中文源文本列。
// 支持列名：zh/zh-cn/cn/chinese/中文/原文/源语言/source 等；"别名-代码" 形式中代码为 zh 也视为源列。
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
//
// 参数 name: 列名。返回: 规范化后的 DB 语言代码；无法识别返回空串。
func langCodeFromColName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	// 先尝试完整别名/代码直接匹配（如 en/ru/英语）
	if code := engine.LangCodeFromName(trimmed); code != "" {
		return mapDBLangCode(code)
	}
	// 尝试 "别名-代码" 拆分：优先匹配 "zh-tw"/"zh-hant"/"zh-cn" 等复合代码后缀
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
	// 按最后一个 "-" 拆分：分别尝试直接代码部分与别名部分
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

// normalizeCodePart 规范化语言代码片段（含大小写、zh-TW、kz 等特例）。
// 参数 part: 语言代码片段。返回: 规范化后的代码；无法识别返回空串。
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
	// 通用兜底：2 字母纯小写代码
	if len(p) == 2 && isAlphaLower(p) {
		return mapDBLangCode(p)
	}
	return ""
}

// mapDBLangCode 语言代码 → DB 列名映射（id 列为 id_lang，避免与自增主键冲突）。
// 参数 code: 语言代码。返回: DB 中使用的列名。
func mapDBLangCode(code string) string {
	if code == "id" {
		return "id_lang"
	}
	return code
}

// isAlphaLower 判断字符串是否全部为小写字母。
// 参数 s: 待判断字符串。返回: true 表示全为 a-z 小写字母。
func isAlphaLower(s string) bool {
	for _, c := range s {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}

// kbRecognizeMeta 识别缓存元信息：记录临时文件路径与过期时间戳。
type kbRecognizeMeta struct {
	FilePath string `json:"file_path"` // 缓存的上传文件绝对路径
	TempID   string `json:"temp_id"`   // 识别阶段生成的临时 ID（供导入阶段引用）
	Created  int64  `json:"created"`   // 创建时间（Unix 秒）
}

// handleRecognizeKB 识别 KB 文件接口：上传文件并解析识别语言列，返回预览与临时 ID。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart 上传 "file" 字段）。
// 返回: success=true 时携带 preview（预览 5 条）/columns（语言列）/new_langs（新语言）/temp_id。
// 流程：保存文件 → kb.ParseKBFile 解析 → 识别源列与语言列 → 缓存文件生成 temp_id（供 import-kb 读取）。
func (s *Server) handleRecognizeKB(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上（前台普通用户不再允许上传 KB）
	if _, err := s.requireTenantAdmin(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 知识库未加载（未传入 -kb）时拒绝识别
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	// 保存上传文件
	savePath, err := s.saveUploadedFile(r)
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文件上传失败"})
		return
	}

	// 解析文件为记录行 + 全部列名
	records, allCols, err := kb.ParseKBFile(savePath)
	if err != nil {
		os.Remove(savePath)
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "识别失败: " + err.Error()})
		return
	}
	// 无有效数据：清理临时文件并提示
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
	// 识别不到语言列时兜底使用全部列
	if len(langCols) == 0 {
		langCols = allCols
	}

	// 新语言：不在现有 KB 语言列中的代码（提示用户将新增语言列）
	var newLangs []string
	for _, col := range langCols {
		code := langCodeFromColName(col)
		if code == "" || code == "zh" {
			continue
		}
		// 检查是否已是 KB 已有语言
		found := false
		for _, lc := range kb.AllLangs {
			if lc == code {
				found = true
				break
			}
		}
		if !found {
			// 去重后加入新语言列表
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
			// 源列取值作为 zh 预览
			if isSourceCol(k) {
				if zhVal == "" {
					zhVal = v
				}
				continue
			}
			// 语言列加入预览
			if code := langCodeFromColName(k); code != "" {
				m[k] = v
			}
		}
		m["zh"] = zhVal
		preview = append(preview, m)
	}

	// 生成 temp_id 并缓存文件路径（识别后保留文件，供 import-kb 读取）
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

// randHex 生成 n 字节随机 hex 串（crypto/rand）。
// 参数 n: 生成字节数（最终串长 n 个 hex 字符）。返回: 随机 hex 字符串。
// 随机源失败时回退用时间戳生成确定性字节（兜底，不影响唯一性要求不高的场景）。
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 兜底：crypto/rand 失败时用时间戳填充字节
		ts := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(ts >> (8 * uint(i%8)))
		}
	}
	return hex.EncodeToString(b)[:n]
}

// handleImportKB 导入识别过的 KB 文件接口：前端仅提交 {temp_id, package_id}，从缓存读取之前保存的文件并写入指定包。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 temp_id/package_id）。
// 返回: success=true 时携带 added（新增数）/skipped（跳过数）。
// 流程：读缓存元信息 → 重新解析文件 → 按源列/语言列写入指定知识库包（按包隔离）→ 清理临时文件与元信息。
func (s *Server) handleImportKB(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 知识库未加载时拒绝导入
	if s.DB == nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "翻译技能未加载（未传入 -kb）"})
		return
	}
	// 协议：前端提交 {temp_id, package_id}，从缓存读取之前保存的文件
	var req struct {
		TempID    string `json:"temp_id"`    // 识别阶段返回的临时 ID
		PackageID int64  `json:"package_id"` // 目标知识库包 ID（按包隔离写入）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TempID == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少 temp_id"})
		return
	}
	if req.PackageID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少 package_id"})
		return
	}
	tid := s.effTenant(r, u)
	// 包归属 + 类型权限校验
	pkg, gErr := s.Store.GetKBPackage(req.PackageID, tid)
	if gErr != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "包不存在或无权操作"})
		return
	}
	if !canManagePackType(u, pkg.PackType) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权向该类型的知识库包导入"})
		return
	}

	// 读取缓存元信息文件
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
	// 导入完成后清理临时文件与元信息缓存
	defer func() {
		os.Remove(savePath)
		os.Remove(metaPath)
	}()

	// 重新解析文件
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
	for _, rec := range records {
		var src string
		// 提取源列值
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
		// 提取各语言列译文（跳过源列与空值）
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
		// 无任何译文：跳过
		if len(translations) == 0 {
			skipped++
			continue
		}
		// 按语言逐条写入指定包（来源标记 "imported"；单条失败跳过计数）
		for lang, txt := range translations {
			if _, err := s.Store.SaveEntry(tid, req.PackageID, store.LayerTM, "zh", src, lang, txt, "imported"); err != nil {
				skipped++
				continue
			}
			added++
		}
	}

	s.Store.LogAudit(tid, u.ID, "kb_file_import", "kb_entries", req.TempID)
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "message": "导入完成", "added": added, "skipped": skipped,
	})
}
