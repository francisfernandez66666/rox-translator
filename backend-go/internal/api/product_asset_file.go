// ============ product_asset_file.go · 职责说明 ============
// 一期 X2（2026-11-06 第四轮裁定）：物料**文件腿**——卖家把已经备好的对外物料
// （文档／图片／配音）传上来，挂到某一条产品物料行上，再经带租户作用域的出口取回。
//
// ★ 四条硬口径（改这一族前必读）：
//
//	① **作废只有一把尺子**：文件流出口必须先过 `Store.AssetForDownload`
//	   （它内含 `validity='expired'` 拦截）。新开任何一个产物出口都要先问「它过不过那一条闸」——
//	   「JSON 口拦得住、文件口拦不住」＝A3 只剩半只锁，而客户拿到的是引用已改事实的旧成品。
//	② **库里只存 basename**：上传口对外只回文件名（不含目录、不含分隔符），
//	   取回口再做一次 `filepath.Base` 等值校验＋`resolveSafePath` 目录白名单。
//	   `artifact_ref` 是卖家可写的字符串，直接拼路径＝任意文件读。两道都留，不靠"记得清洗"。
//	③ **格式白名单从 `fileproc` 现读派生**（AGENTS §一·14：清单只有一份）。
//	   在调用方抄一份 `.docx/.xlsx/...` 字面量会被"按形状判内联白名单"的那道闸当场判红。
//	④ 上传件按租户私有落盘（目录 0750／文件 0600），**不**放进 web 根，也不经 Caddy 直出；
//	   取回一律走本文件这一个带鉴权的口。
//
// ============================================
package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
)

// productAssetDirName 物料上传件的独立命名空间（照 `_qr`／`_chunks` 那条先例，不与翻译上传件混放）。
const productAssetDirName = "_product_assets"

// productAssetUploadMax 单件体积上限＝**直接取 fileproc 那把尺子**（40 MiB），
// 不在这里另立一个数：工单上传、物料上传对"多大算太大"必须是同一个口径。
const productAssetUploadMax = fileproc.MaxUploadBytes

// productAssetWhitelist 允许的扩展名集合，由 `fileproc.TranslateFormatOrder` 现算派生。
// 派生而非抄写：将来撤某个格式（像 2026-10-30 撤那五个纯文本容器）时，物料口自动跟着收，
// 不会留下一份"没人维护的第二清单"。
var productAssetWhitelist = func() map[string]bool {
	m := make(map[string]bool, len(fileproc.TranslateFormatOrder))
	for _, ext := range fileproc.TranslateFormatOrder {
		m[ext] = true
	}
	return m
}()

// productAssetDir 物料上传件根目录（`UploadDir/_product_assets`）。
func (s *Server) productAssetDir() string {
	return filepath.Join(s.Cfg.UploadDir, productAssetDirName)
}

// handleProductAssetUpload POST /api/product/asset/upload（multipart `file`）。
//
// 只负责"把字节落到磁盘并回一个 basename"，**不建物料行**——行仍由 `/api/product/asset/put` 写，
// 这样同一条 put 路径既是新建也是改档，不会出现"上传建了半行、正文还没填"的第三种状态。
func (s *Server) handleProductAssetUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	// 体积与扩展名一律过现成的 parseUpload（它用 MaxBytesReader 硬拦，超限部分不会静默落临时文件）。
	if err := parseUpload(r, productAssetUploadMax, productAssetWhitelist); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少文件"))
		return
	}
	defer f.Close()

	dir := s.productAssetDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "无法创建上传目录"))
		return
	}
	// uniqueName 只回 basename（随机名＋原扩展名），因此库里存的这一串天然不含路径。
	// 权限取 0600／目录 0750：卖家的报价单与参数表是**租户私有**，组内其他服务账号也不该读到。
	name := uniqueName(header.Filename)
	savePath := filepath.Join(dir, name)
	out, err := os.OpenFile(savePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "文件写入失败"))
		return
	}
	written, err := io.Copy(out, f)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(savePath) // 半截件绝不留给下一次取回
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "文件写入失败"))
		return
	}
	s.Store.LogAudit(tid, u.ID, "product_asset_upload", "product_assets", name)
	writeJSON(w, 200, map[string]interface{}{
		"success":      true,
		"artifact_ref": name,
		"size_bytes":   written,
		"orig_name":    header.Filename,
	})
}

// handleProductAssetFile GET /api/product/asset/file?id= ：物料附件的**唯一**取回口。
//
// 与 `/api/product/asset/download`（出 JSON 正文）是两个面、但共用同一把作废尺子。
func (s *Server) handleProductAssetFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("id")), 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "物料 ID 不合法"))
		return
	}
	// ① 先过那把唯一的尺子：不存在／跨租户／已作废，一律在这里挡住，不到磁盘那一跳。
	a, err := s.Store.AssetForDownload(tid, id)
	if err != nil {
		if isAssetExpired(err) {
			s.Store.LogAudit(tid, u.ID, "asset_download_blocked", "product_assets", strconv.FormatInt(id, 10))
			s.writeError(w, r, apierrors.New(apierrors.ErrConflict, err.Error()))
			return
		}
		s.writeError(w, r, productNotFound())
		return
	}
	ref := strings.TrimSpace(a.ArtifactRef)
	if ref == "" {
		// 纯文案物料（body 就是全部交付物）走 /asset/download 出正文，这里如实点名而不是回空文件。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "这条物料没有附件，正文内容在物料详情里"))
		return
	}
	// ② 第二道护栏：库里那一串必须逐字等于它的 basename（防历史脏值／旁路 SQL 写进来的路径）。
	base := filepath.Base(ref)
	if base != ref || base == "." || base == ".." || strings.ContainsAny(ref, `/\`) {
		s.writeError(w, r, productNotFound())
		return
	}
	// ③ 目录白名单：Clean 后必须落在 `_product_assets` 里，否则一律按"文件不存在"处理。
	safePath, ok := resolveSafePath([]string{s.productAssetDir()}, filepath.Join(s.productAssetDir(), base))
	if !ok {
		s.writeError(w, r, productNotFound())
		return
	}
	f, err := os.Open(safePath)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "物料文件不存在或已被清理，请重新上传"))
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		s.writeError(w, r, productNotFound())
		return
	}
	// 附件一律按下载处理（inline 会让浏览器直接渲染，卖家的报价单不该变成网页）。
	// ★ 走 contentDisposition 那一个出口：它同时给 ASCII 回退名与 filename*=UTF-8''，
	//   而物料文件名十有八九是中文（"报价单-2026.pdf"）。在这里手拼一句＝第二把尺子，
	//   且会把中文名退化成随机 basename（TestContentDispositionSingleExit 当场抓出这一处）。
	w.Header().Set("Content-Disposition", contentDisposition(base))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, base, fi.ModTime(), f)
	// ★ 成功取回不写审计——与既有 `/asset/download`（只审「被拦」那一档）保持同一口径：
	//	下载是高频只读动作，把每一次都记进审计表会把真正的异常（拦下来的那一次）淹掉。
}
