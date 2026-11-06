// ============ product_api.go · 职责说明 ============
// 一期《确定的多语言能力》§5.1 登录态 API 面：产品 CRUD、事实项、物料、齐备度、
// 已批准说法、SEO 索引开关。
//
// ★ 三条硬口径（AGENTS §一·8 / 本文 §5.1）：
//
//	① 错误一律 `s.writeError(w, r, apierrors.New(code, msg))`——错误风格棘轮基线为 0，
//	   一条内联 writeJSON(4xx/5xx) 就顶红；本文件零处内联错误响应。
//	② 读侧与写侧共用 store 的 productExistsScoped 作用域助手（SQL 恒带 tenant_id=?），
//	   写路径先做存在性前置；查不到一律 404 语义（NOT_FOUND），**不返 403**（不泄露他租户存在性）。
//	③ 空集 / 空页归一成 `[]`，Go nil slice 出 null 会让每个消费方各判一次。
//
// ★ tid 不从 effTenant 的兜底 1 取：只认登录用户自身的 u.TenantID，且 <=0 显式拒
//
//	（附录 A 第 1 条：平台超管 tid=0 上下文不许被兜底成「租户 1 的数据」）。
//
// ================================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// 分页档位镜像（store 层 ProductPageDefault/Max 是唯一真值，api 层只做同档校验）。
const (
	productAPIPageDefault = store.ProductPageDefault
	productAPIPageMax     = store.ProductPageMax
)

// productActor 取本次请求的登录用户与其租户；未登录 / 平台上下文一律 401/400。
// ★ 这是所有登录态 handler 共用的唯一身份入口（route_auth_gate 认 s.authUser）。
// 参数：r=请求；返回用户、租户与结构化错误。
func (s *Server) productActor(r *http.Request) (*store.User, int64, *apierrors.APIError) {
	u := s.authUser(r)
	if u == nil {
		return nil, 0, apierrors.New(apierrors.ErrUnauthorized, "未登录")
	}
	if u.TenantID <= 0 {
		return nil, 0, apierrors.New(apierrors.ErrValidation, "个人产品面必须带有效租户")
	}
	return u, u.TenantID, nil
}

// productNotFound 产品不存在 / 不属于该租户的统一出口（404 语义，不返 403）。
func productNotFound() *apierrors.APIError {
	return apierrors.New(apierrors.ErrNotFound, "产品不存在")
}

// decodeProductBody 解一个 JSON 请求体；解不出给 400（不静默用零值）。
// 参数：r=请求，dst=目标结构；返回结构化错误。
func decodeProductBody(r *http.Request, dst interface{}) *apierrors.APIError {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return apierrors.New(apierrors.ErrValidation, "请求格式错误")
	}
	return nil
}

// productPageArgs 分页脏参折回档位，出栈 limit 必为真值。
// 参数：r=请求；返回页码与每页条数。
func productPageArgs(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page")))
	size, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page_size")))
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > productAPIPageMax {
		size = productAPIPageDefault
	}
	return page, size
}

// routesProduct 注册一期产品面（挂 routes() 一行）。
func (s *Server) routesProduct() {
	s.mux.HandleFunc("/api/product/list", s.handleProductList)
	s.mux.HandleFunc("/api/product/save", s.handleProductSave)
	s.mux.HandleFunc("/api/product/delete", s.handleProductDelete)
	s.mux.HandleFunc("/api/product/status", s.handleProductStatus)
	s.mux.HandleFunc("/api/product/fact/list", s.handleProductFactList)
	s.mux.HandleFunc("/api/product/fact/upsert", s.handleProductFactUpsert)
	s.mux.HandleFunc("/api/product/fact/confirm", s.handleProductFactConfirm)
	s.mux.HandleFunc("/api/product/fact/withdraw", s.handleProductFactWithdraw)
	s.mux.HandleFunc("/api/product/asset/list", s.handleProductAssetList)
	s.mux.HandleFunc("/api/product/asset/put", s.handleProductAssetPut)
	s.mux.HandleFunc("/api/product/asset/review", s.handleProductAssetReview)
	s.mux.HandleFunc("/api/product/asset/download", s.handleProductAssetDownload)
	// ★ X2 物料文件腿（product_asset_file.go）：上传只落字节回 basename，取回必须过同一条作废尺子。
	s.mux.HandleFunc("/api/product/asset/upload", s.handleProductAssetUpload)
	s.mux.HandleFunc("/api/product/asset/file", s.handleProductAssetFile)
	s.mux.HandleFunc("/api/product/readiness", s.handleProductReadiness)
	s.mux.HandleFunc("/api/product/kb/phrases", s.handleProductKBPhrases)
	s.mux.HandleFunc("/api/product/seo", s.handleProductSeo)
	// ★ 2026-11-07 引导页「自由描述 → 候选字段」识别口（product_onboard_draft.go）。
	//   单独成函数而不是往这里追加一行：这一口的接线、出栈清洗与路由全收在一个新文件里，
	//   本函数只多一次委托（AGENTS §一·1「按域新建文件」的同一手法）。
	s.registerProductOnboardDraftRoutes()
}

// handleProductList GET /api/product/list：本人租户的产品分页列表。
func (s *Server) handleProductList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	_, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	page, size := productPageArgs(r)
	list, total, err := s.Store.ListProducts(tid, page, size)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "产品列表读取失败"))
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "products": list, "total": total, "page": page, "limit": size,
	})
}

// handleProductSave POST /api/product/save：建档（无 id）或改资料（带 id）。
func (s *Server) handleProductSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		Code         string `json:"code"`
		Tagline      string `json:"tagline"`
		Slug         string `json:"slug"`
		IndustryCode string `json:"industry_code"`
		Audience     string `json:"audience"`
		UseCase      string `json:"use_case"`
		StyleNotes   string `json:"style_notes"`
		BannedNotes  string `json:"banned_notes"`
		// ★ O 组：指针是为了把"引导里没勾"与"显式清空"分开（nil 沿用库里现值）。
		//   前端 Onboarding/详情页必须送这一份，跨侧锁 productQueryContract 会逐键核对键名。
		TargetLangs *[]string `json:"target_langs"`
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	in := store.ProductInput{
		Code: req.Code, Name: req.Name, Tagline: req.Tagline, Slug: req.Slug,
		IndustryCode: req.IndustryCode, Audience: req.Audience, UseCase: req.UseCase,
		StyleNotes: req.StyleNotes, BannedNotes: req.BannedNotes, UserID: u.ID,
		TargetLangs: req.TargetLangs,
	}
	if strings.TrimSpace(req.ID) == "" {
		p, err := s.Store.CreateProduct(tid, in)
		if err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
			return
		}
		// 建档即备好两包（source 供命中、gate 供输出校验）；失败不回滚档案，但出声。
		if _, _, err := s.Store.EnsureProductPacks(tid, p.ID); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "产品包建档失败"))
			return
		}
		s.Store.LogAudit(tid, u.ID, "product_save", "products", p.ID)
		writeJSON(w, 200, map[string]interface{}{"success": true, "product": p, "id": p.ID})
		return
	}
	// 改名走 UpdateProductName（短码不动），资料走 UpdateProductProfile（两段短码与 SEO 都不动）。
	// ★ **带 id 的 save 契约＝"把这份档案存回去"**，不是 PATCH：name 必填（空名建档/改名都无意义），
	//   其余档案字段整份覆盖。⇒ 调用方必须交完整表单（引导页第 3 步只改语种时也要把第 2 步收到的
	//   name/tagline/use_case 一起交回来）。这里刻意**不**把空串读成"没表态"：
	//   真表态清空一个字段与忘了带这个字段在字符串上同形，宁可让调用方交全，也不要静默丢数据。
	//   唯一走"三态指针"的是 target_langs（见下面 in.TargetLangs 的 nil 语义，§29 同一条纪律）。
	if err := s.Store.UpdateProductName(tid, req.ID, req.Name); err != nil {
		if isProductNotFound(err) {
			s.writeError(w, r, productNotFound())
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	if err := s.Store.UpdateProductProfile(tid, req.ID, in); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "产品资料更新失败"))
		return
	}
	p, ok, err := s.Store.GetProductScoped(tid, req.ID)
	if err != nil || !ok {
		s.writeError(w, r, productNotFound())
		return
	}
	s.Store.LogAudit(tid, u.ID, "product_save", "products", p.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "product": p, "id": p.ID})
}

// isProductNotFound 把 store 的哨兵错误翻成 404 语义（不返 403）。
func isProductNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "产品不存在")
}

// handleProductDelete POST /api/product/delete：只删档案行（不整租户蒸发）。
func (s *Server) handleProductDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if err := s.Store.DeleteProduct(tid, req.ID); err != nil {
		s.writeError(w, r, productNotFound())
		return
	}
	s.Store.LogAudit(tid, u.ID, "product_delete", "products", req.ID)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleProductStatus POST /api/product/status：状态三值流转。
func (s *Server) handleProductStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if err := s.Store.SetProductStatus(tid, req.ID, req.Status); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	s.Store.LogAudit(tid, u.ID, "product_status", "products", req.ID+":"+req.Status)
	writeJSON(w, 200, map[string]interface{}{"success": true, "status": req.Status})
}

// handleProductFactList GET /api/product/fact/list：某产品全部事实项（空集出 []）。
func (s *Server) handleProductFactList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	_, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	list, err := s.Store.ListFacts(tid, r.URL.Query().Get("product_id"))
	if err != nil {
		s.writeError(w, r, productNotFound())
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "facts": list})
}

// productPhraseReq 单条「已批准说法」的入参形状（批量导入时是 productPhraseReq 数组的元素）。
type productPhraseReq struct {
	SourceText string `json:"source_text"`
	TargetText string `json:"target_text"`
	TargetLang string `json:"target_lang"`
	SourceLang string `json:"source_lang"`
}

// productFactReq 事实项写入/状态流转的公共请求体。
//
// ★ 行自身的主键一律叫 `id`（与 `/api/product/save|delete|status` 与 `/asset/review` 同一条口径，
//
//	也与 `?id=` 下载口同名单）；`product_id` 只表示父产品。首版这里写成 `fact_id`，
//	前端与 UAT 脚本两处都送 `id` ⇒ confirm 恒「产品不存在」——同一族契约裂缝的第三个面。
type productFactReq struct {
	ProductID  string `json:"product_id"`
	FactID     int64  `json:"id"`
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Value      string `json:"value"`
	Visibility string `json:"visibility"`
	Source     string `json:"source"`
}

// handleProductFactUpsert POST /api/product/fact/upsert。
func (s *Server) handleProductFactUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req productFactReq
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	f, err := s.Store.UpsertFact(tid, store.ProductFactInput{
		ProductID: req.ProductID, Kind: req.Kind, Key: req.Key, Value: req.Value,
		Visibility: req.Visibility, Source: req.Source,
	})
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	if req.Visibility == store.VisibilityPublic {
		if err := s.Store.SetFactVisibility(tid, f.ID, store.VisibilityPublic); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
			return
		}
		f, _ = s.factByID(tid, f.ID)
	}
	s.Store.LogAudit(tid, u.ID, "product_fact_upsert", "product_facts", req.ProductID+":"+req.Key)
	writeJSON(w, 200, map[string]interface{}{"success": true, "fact": f, "id": f.ID})
}

// factByID 读回一条事实项（供写后出栈，避免返回体与库内值漂移）。
func (s *Server) factByID(tid, id int64) (store.ProductFact, error) {
	set, err := s.Store.FactsByIDSet(tid, []int64{id})
	if err != nil {
		return store.ProductFact{}, err
	}
	return set[id], nil
}

// handleProductFactConfirm POST /api/product/fact/confirm（仅 manual 可确认；cert 走 P4）。
func (s *Server) handleProductFactConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req productFactReq
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if err := s.Store.ConfirmFact(tid, req.FactID, u.ID); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	s.Store.LogAudit(tid, u.ID, "fact_confirm", "product_facts", strconv.FormatInt(req.FactID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "status": store.FactStatusConfirmed})
}

// handleProductFactWithdraw POST /api/product/fact/withdraw。
func (s *Server) handleProductFactWithdraw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req productFactReq
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if err := s.Store.WithdrawFact(tid, req.FactID, u.ID); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	s.Store.LogAudit(tid, u.ID, "fact_withdraw", "product_facts", strconv.FormatInt(req.FactID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true, "status": store.FactStatusWithdrawn})
}

// handleProductAssetList GET /api/product/asset/list。
func (s *Server) handleProductAssetList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	_, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	list, err := s.Store.ListAssets(tid, r.URL.Query().Get("product_id"))
	if err != nil {
		s.writeError(w, r, productNotFound())
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "assets": list})
}

// productAssetReq 物料写入/审核的公共请求体。
// ★ 物料自身主键同样叫 `id`（见上面 productFactReq 那条口径，两侧不许各写一个名字）。
type productAssetReq struct {
	ProductID   string  `json:"product_id"`
	AssetID     int64   `json:"id"`
	AssetType   string  `json:"asset_type"`
	Lang        string  `json:"lang"`
	Body        string  `json:"body"`
	ArtifactRef string  `json:"artifact_ref"`
	Visibility  string  `json:"visibility"`
	SourceKind  string  `json:"source_kind"`
	FactRefs    []int64 `json:"fact_refs"`
	Status      string  `json:"status"`
}

// handleProductAssetPut POST /api/product/asset/put。
func (s *Server) handleProductAssetPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req productAssetReq
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	a, err := s.Store.PutAsset(tid, store.ProductAssetInput{
		ProductID: req.ProductID, AssetType: req.AssetType, Lang: req.Lang, Body: req.Body,
		ArtifactRef: req.ArtifactRef, Visibility: req.Visibility,
		SourceKind: req.SourceKind, FactRefs: req.FactRefs,
	})
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	s.Store.LogAudit(tid, u.ID, "product_asset_put", "product_assets", req.ProductID+":"+req.AssetType)
	writeJSON(w, 200, map[string]interface{}{"success": true, "asset": a, "id": a.ID})
}

// handleProductAssetReview POST /api/product/asset/review。
func (s *Server) handleProductAssetReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req productAssetReq
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if err := s.Store.MarkAssetReview(tid, req.AssetID, req.Status); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, err.Error()))
		return
	}
	s.Store.LogAudit(tid, u.ID, "asset_review", "product_assets", strconv.FormatInt(req.AssetID, 10)+":"+req.Status)
	writeJSON(w, 200, map[string]interface{}{"success": true, "status": req.Status})
}

// handleProductAssetDownload GET /api/product/asset/download：A3 拦 expired（可行动文案）。
func (s *Server) handleProductAssetDownload(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, 200, map[string]interface{}{"success": true, "asset": a, "id": a.ID})
}

// isAssetExpired 把 store 的过期哨兵翻成可读错误码（A3 不静默）。
func isAssetExpired(err error) bool {
	return err != nil && strings.Contains(err.Error(), "重新生成并审核")
}

// handleProductReadiness GET /api/product/readiness：六格 × 12 语种（空集出 []）。
func (s *Server) handleProductReadiness(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	_, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	cells, err := s.Store.ReadinessFor(tid, r.URL.Query().Get("product_id"))
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "齐备度读取失败"))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "readiness": cells, "knowledge_ready": len(cells) > 0})
}

// handleProductKBPhrases GET/POST /api/product/kb/phrases：薄委托 kb_entries（不另开口径）。
func (s *Server) handleProductKBPhrases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET/POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	productID := strings.TrimSpace(r.URL.Query().Get("product_id"))
	if productID == "" {
		s.writeError(w, r, productNotFound())
		return
	}
	if _, ok, err := s.Store.GetProductScoped(tid, productID); err != nil || !ok {
		s.writeError(w, r, productNotFound())
		return
	}
	srcID, _, err := s.Store.EnsureProductPacks(tid, productID)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "产品包不可用"))
		return
	}
	if r.Method == http.MethodGet {
		entries, err := s.Store.ListEntries(tid, srcID)
		if err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "说法读取失败"))
			return
		}
		writeJSON(w, 200, map[string]interface{}{"success": true, "phrases": entries})
		return
	}
	// ★ 批量导入的入参形状只有一种：`{"phrases":[{...}]}`。
	//   旧形态这里解单条（source_text…），而前端 kbPhraseSave() 送的是 `{product_id, phrases}`
	//   ⇒ 双方各写一份契约、彼此都不报错地"导入成功、库里零行"。现在由跨语言锁 Q4 按注册表判，
	//   任何一侧再想各写一份就当场红。
	var req struct {
		Phrases []productPhraseReq `json:"phrases"`
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if len(req.Phrases) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "没有要导入的说法"))
		return
	}
	imported, failed := 0, 0
	for _, ph := range req.Phrases {
		if strings.TrimSpace(ph.SourceText) == "" || strings.TrimSpace(ph.TargetText) == "" {
			failed++
			continue
		}
		srcLang := ph.SourceLang
		if srcLang == "" {
			srcLang = "zh"
		}
		if _, err := s.Store.SaveEntry(tid, srcID, store.LayerTerm, srcLang, ph.SourceText,
			ph.TargetLang, ph.TargetText, store.TMSourceApproved); err != nil {
			failed++
			continue
		}
		imported++
	}
	s.Store.LogAudit(tid, u.ID, "product_phrase_save", "kb_entries",
		productID+":"+strconv.Itoa(imported)+"/"+strconv.Itoa(failed))
	// 一条都没落＝失败，不许回 success（§一·8：写路径 0 行影响不是错误，但绝不能报成功）
	if imported == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "这批说法都没能导入：源文与译文都必须非空"))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "imported": imported, "failed": failed})
}

// handleProductSeo GET/POST /api/product/seo：出厂默认开，卖家可显式关（S4 唯一写点）。
func (s *Server) handleProductSeo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET/POST"))
		return
	}
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	productID := strings.TrimSpace(r.URL.Query().Get("product_id"))
	if r.Method == http.MethodGet {
		p, ok, err := s.Store.GetProductScoped(tid, productID)
		if err != nil || !ok {
			s.writeError(w, r, productNotFound())
			return
		}
		writeJSON(w, 200, map[string]interface{}{
			"success": true, "seo_indexable": p.SeoIndexable == 1,
			"url": "/p/" + p.TenantCode + "/" + p.PageCode,
		})
		return
	}
	var req struct {
		ProductID string `json:"product_id"`
		// ★ 入参名与出参名同源（`indexable` ↔ `seo_indexable`）；原先写成 `on`，
		//   而前端 seoSet() 送的是 `indexable` ⇒ 卖家点"关"会被静默当成"没表态"（false 零值），
		//   接口回 success 而库里一字未动——§一·8「写路径 0 行影响不许回 success」同一族。
		Indexable bool `json:"indexable"`
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	if err := s.Store.SetProductSeoIndexable(tid, req.ProductID, req.Indexable); err != nil {
		s.writeError(w, r, productNotFound())
		return
	}
	s.Store.LogAudit(tid, u.ID, "product_seo", "products", req.ProductID)
	writeJSON(w, 200, map[string]interface{}{"success": true, "seo_indexable": req.Indexable})
}
