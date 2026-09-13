// ============================================================================
// H5 大文件导入断点续传：分片上传三步端点（chunk → status → merge）。
//
//	POST /api/upload/chunk   multipart: upload_id,index,total,chunk(≤8MB)
//	GET  /api/upload/status  ?upload_id=  → 已收分片索引（刷新/断网后续传）
//	POST /api/upload/merge   {upload_id,filename} → 合并出 kbmerged_<hex>.<ext>
//
// 合并产物即 recognize-kb 的 `?merged=NAME` 直读文件（跳过重复上传），
// 之后的识别/导入链路与整文件上传完全一致。分片目录 24h TTL 顺手清理。
// ============================================================================
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	kbChunkMax     = 8 << 20 // 单分片上限 8MB
	chunkTTL       = 24 * time.Hour
	chunkMaxTotal  = 20000 // 分片数上限（≈160GB 硬顶，实际受文件类型约束）
	chunkMaxActual = 500 << 20
)

var (
	uploadIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)
	mergedNameRe = regexp.MustCompile(`^kbmerged_[0-9a-f]{12}\.[A-Za-z0-9]{1,8}$`)
	chunkPartRe  = regexp.MustCompile(`^idx_(\d{5})\.part$`)
)

// chunkDirPath 分片暂存目录（UploadDir/_chunks/<upload_id>）。
func (s *Server) chunkDirPath(uploadID string) string {
	return filepath.Join(s.Cfg.UploadDir, "_chunks", uploadID)
}

// sweepOldChunks 顺手清理超期分片目录（无独立后台任务，上传动作驱动）。
func (s *Server) sweepOldChunks() {
	root := filepath.Join(s.Cfg.UploadDir, "_chunks")
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > chunkTTL {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// handleKBUploadChunk 接收单个分片（幂等覆盖写）。
func (s *Server) handleKBUploadChunk(w http.ResponseWriter, r *http.Request) {
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if err := r.ParseMultipartForm(24 << 20); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "分片解析失败"})
		return
	}
	uploadID := r.FormValue("upload_id")
	idx, _ := strconv.Atoi(r.FormValue("index"))
	total, _ := strconv.Atoi(r.FormValue("total"))
	if !uploadIDRe.MatchString(uploadID) || idx < 0 || total <= 0 || total > chunkMaxTotal || idx >= total {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "upload_id/index/total 参数非法"})
		return
	}
	f, hdr, err := r.FormFile("chunk")
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少分片内容"})
		return
	}
	defer f.Close()
	if hdr.Size <= 0 || hdr.Size > kbChunkMax {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "分片大小须在 0-8MB"})
		return
	}
	dir := s.chunkDirPath(uploadID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if idx == 0 {
		s.sweepOldChunks()
	}
	tmp := filepath.Join(dir, "tmp.part")
	out, err := os.Create(tmp)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	n, err := io.Copy(out, io.LimitReader(f, kbChunkMax+1))
	out.Close()
	if err != nil || n > kbChunkMax {
		os.Remove(tmp)
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "分片写入失败或超限"})
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, chunkPartName(idx))); err != nil {
		os.Remove(tmp)
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 记录 total 供 status/merge 校验（简单文件，避免内存态）
	_ = os.WriteFile(filepath.Join(dir, "meta"), []byte(strconv.Itoa(total)), 0o644)
	writeJSON(w, 200, map[string]interface{}{"success": true, "index": idx})
}

// chunkPartName 分片文件名（5 位零填充保证排序即索引序）。
func chunkPartName(idx int) string { return "idx_" + pad5(idx) + ".part" }

// pad5 将序号零填充到 5 位，保证分片文件按字典序即分片顺序。
func pad5(n int) string {
	t := strconv.Itoa(n)
	for len(t) < 5 {
		t = "0" + t
	}
	return t
}

// receivedChunks 目录内已收分片索引升序。
func receivedChunks(dir string) []int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		if m := chunkPartRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				out = append(out, n)
			}
		}
	}
	sort.Ints(out)
	return out
}

// handleKBUploadStatus 已收分片清单（续传定位）。
func (s *Server) handleKBUploadStatus(w http.ResponseWriter, r *http.Request) {
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	uploadID := r.URL.Query().Get("upload_id")
	if !uploadIDRe.MatchString(uploadID) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "upload_id 非法"})
		return
	}
	dir := s.chunkDirPath(uploadID)
	rec := receivedChunks(dir)
	total := 0
	if b, err := os.ReadFile(filepath.Join(dir, "meta")); err == nil {
		total, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "received": rec, "total": total})
}

// handleKBUploadMerge 校验分片完整后合并为 kbmerged 文件（供 recognize-kb 直读）。
func (s *Server) handleKBUploadMerge(w http.ResponseWriter, r *http.Request) {
	if s.authUser(r) == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		UploadID string `json:"upload_id"`
		Filename string `json:"filename"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !uploadIDRe.MatchString(req.UploadID) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "upload_id 非法"})
		return
	}
	ext := strings.ToLower(filepath.Ext(req.Filename))
	if !kbExtWhitelist[ext] {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "不支持的文件类型：" + ext})
		return
	}
	ext = strings.TrimPrefix(ext, ".")
	dir := s.chunkDirPath(req.UploadID)
	total := 0
	if b, err := os.ReadFile(filepath.Join(dir, "meta")); err == nil {
		total, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	rec := receivedChunks(dir)
	if total <= 0 || len(rec) != total {
		writeJSON(w, 200, map[string]interface{}{"success": false,
			"message": "分片不完整（已收 " + strconv.Itoa(len(rec)) + "/" + strconv.Itoa(total) + "）"})
		return
	}
	// 连续性校验（received 升序应为 0..total-1）
	for i, n := range rec {
		if n != i {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "分片序号有缺口，请续传后重试"})
			return
		}
	}
	name := "kbmerged_" + randHex(12) + "." + ext
	tmp := name + ".merging"
	path := filepath.Join(s.kbTempDir(), tmp)
	out, err := os.Create(path)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var written int64
	for i := 0; i < total; i++ {
		part, err := os.Open(filepath.Join(dir, chunkPartName(i)))
		if err != nil {
			out.Close()
			os.Remove(path)
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "分片读取失败"})
			return
		}
		n, err := io.Copy(out, part)
		part.Close()
		written += n
		if err != nil || written > chunkMaxActual {
			out.Close()
			os.Remove(path)
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "合并失败或文件超限"})
			return
		}
	}
	out.Close()
	if err := os.Rename(path, filepath.Join(s.kbTempDir(), name)); err != nil {
		os.Remove(path)
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	_ = os.RemoveAll(dir)
	writeJSON(w, 200, map[string]interface{}{"success": true, "merged": name, "bytes": written})
}
