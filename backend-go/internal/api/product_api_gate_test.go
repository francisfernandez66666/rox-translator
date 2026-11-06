// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》批次 4 API 门禁：
// SC1/SC2 作用域助手与存在性前置、N1–N3 零 LLM 问答、V4/V5/V6 受控可见性与留资归属、
// S1–S4 SEO 索引面、P7–P10 跨租户拼接不泄露。
//
// ★ N1 用「同刻 usage_ledger 行数增量 == 0」作证据（§9.3 T90 同族），并配源码级
//
//	「handler 不许调 llm.Client」的静态腿：两条合起来才挡得住「悄悄转交模型」这一唯一真实坏法。
//
// ★ 所有用例自钉 SQLite 方言（pinSqliteDialect），走真路由表 srv.routes()。
// ========================================
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newProductAPIServer 起一张内存库 + 全量路由表，签一个普通会员 JWT。
// 参数：t=测试句柄；返回 Server 与 token。
func newProductAPIServer(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("内存库起不来: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	u, err := st.CreateUser(1, "product_user", auth.PasswordHash("pw123456"), "产品用户", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	srv := &Server{
		Cfg: config.C, Store: st, DB: nil, Ten: nil,
		mux: newRouteMux(), metrics: newMetrics(),
		loginLimit: newLoginLimiter(st), regGuard: newRegisterGuard(st),
	}
	srv.routes()
	return srv, tok
}

// callProductAPI 打一个产品面请求；返回状态码与解析后的响应体。
func callProductAPI(t *testing.T, srv *Server, method, path, tok, body string) (int, map[string]interface{}) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, r)
	out := map[string]interface{}{}
	if strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w.Code, out
}

// SC1：读侧与写侧共用作用域助手（源码级：product 系函数里 `AND tenant_id=?` 只在 scope 一处）。
func TestProductScopeSingleHelper(t *testing.T) {
	raw, err := os.ReadFile("../store/product.go")
	if err != nil {
		t.Fatalf("读不到 product.go: %v", err)
	}
	if !strings.Contains(string(raw), "func (s *Store) productExistsScoped") {
		t.Fatal("读写必须共用一个 productExistsScoped 作用域助手（SC1）")
	}
	apiSrc, err := os.ReadFile("product_api.go")
	if err != nil {
		t.Fatalf("读不到 product_api.go: %v", err)
	}
	if !strings.Contains(string(apiSrc), "func (s *Server) productActor") {
		t.Fatal("API 侧身份/租户必须只从 productActor 一处取（不许各 handler 自己读 effTenant 兜底 1）")
	}
}

// SC2：写路径的存在性前置在 UPDATE/DELETE 之前。
func TestProductWritePathHasExistencePrecheck(t *testing.T) {
	raw, err := os.ReadFile("../store/product_write.go")
	if err != nil {
		t.Fatalf("读不到 product_write.go: %v", err)
	}
	src := string(raw)
	for _, name := range []string{"DeleteProduct", "UpdateProductProfile", "SetProductSeoIndexable"} {
		i := strings.Index(src, "func (s *Store) "+name)
		if i < 0 {
			t.Fatalf("%s 缺席", name)
		}
		seg := src[i:]
		if j := strings.Index(seg, "\n}\n"); j > 0 {
			seg = seg[:j]
		}
		pre := strings.Index(seg, "productExistsScoped")
		write := strings.Index(seg, "db.Exec(")
		if pre < 0 || write < 0 || pre > write {
			t.Fatalf("%s 的写 SQL 前必须有 productExistsScoped 存在性前置（SC2）", name)
		}
	}
}

// N1：完整走一次 /api/product/answer ⇒ usage_ledger 行数增量必须为 0。
func TestProductAnswerZeroLLMUsage(t *testing.T) {
	srv, _ := newProductAPIServer(t)
	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "零 token 产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if _, err := srv.Store.UpsertFact(1, store.ProductFactInput{
		ProductID: p.ID, Kind: "usecase", Key: "scene", Value: "公开场景",
		Visibility: store.VisibilityPublic, Source: store.FactSourceManual,
	}); err != nil {
		t.Fatalf("写事实失败: %v", err)
	}
	var before, after int
	if err := srv.Store.DB().QueryRow("SELECT COUNT(*) FROM usage_ledger").Scan(&before); err != nil {
		t.Fatalf("读账本基线失败: %v", err)
	}
	path := "/api/product/answer?tenant_code=" + p.TenantCode + "&page_code=" + p.PageCode
	code, out := callProductAPI(t, srv, http.MethodPost, path, "", "")
	if code != 200 || out["found"] != true {
		t.Fatalf("知识层问答必须 200 且命中, got code=%d out=%v", code, out)
	}
	if err := srv.Store.DB().QueryRow("SELECT COUNT(*) FROM usage_ledger").Scan(&after); err != nil {
		t.Fatalf("读账本终值失败: %v", err)
	}
	if after != before {
		t.Fatalf("知识层问答零 LLM ⇒ usage_ledger 增量必须为 0, got %d → %d", before, after)
	}
	// 静态腿：handler 不许出现任何模型调用符号
	raw, err := os.ReadFile("product_public_api.go")
	if err != nil {
		t.Fatalf("读不到 product_public_api.go: %v", err)
	}
	body := findFuncBody(string(raw), "func (s *Server) handleProductAnswer")
	if strings.Contains(body, "llm.") || strings.Contains(body, "SingleLangTranslate") ||
		strings.Contains(body, "CallChat(") {
		t.Fatal("知识层问答 handler 不许出现任何模型调用（N1 反证）")
	}

	// ★ 体优先腿（§5.7 契约的另一半）：两段短码**只放在 JSON 体里**也必须命中。
	//   前端 answerProduct() 送的就是体；旧实现只读 query ⇒ 每一次真问答都解成 not-found，
	//   而上面那条 query 形态的单测一路绿灯——"体里的入参被静默丢弃"就是这一族缺陷的本体。
	code, out = callProductAPI(t, srv, http.MethodPost, "/api/product/answer", "",
		`{"tenant_code":"`+p.TenantCode+`","page_code":"`+p.PageCode+`","question":"场景是什么？"}`)
	if code != 200 || out["found"] != true {
		t.Fatalf("问答必须认 JSON 体里的两段短码, got code=%d out=%v", code, out)
	}
	if strings.Contains(fmt.Sprint(out["answer"]), "场景是什么") {
		t.Fatal("问题本体不许被复述进答案（知识层只答已备内容）")
	}
}

// findFuncBody 按花括号配平取一个函数体（AGENTS：相邻行号 ≠ 同一结构）。
func findFuncBody(src, decl string) string {
	i := strings.Index(src, decl)
	if i < 0 {
		return ""
	}
	start := strings.Index(src[i:], "{")
	if start < 0 {
		return ""
	}
	depth, j := 0, i+start
	for ; j < len(src); j++ {
		if src[j] == '{' {
			depth++
		} else if src[j] == '}' {
			depth--
			if depth == 0 {
				return src[i : j+1]
			}
		}
	}
	return src[i:]
}

// N2：缺格时零 token 且答案点名缺项；N3：耗尽态仍 0 且返回 resets_at。
func TestProductAnswerMissingAndExhaustedStayZero(t *testing.T) {
	srv, _ := newProductAPIServer(t)
	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "缺项产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	var before int
	if err := srv.Store.DB().QueryRow("SELECT COUNT(*) FROM usage_ledger").Scan(&before); err != nil {
		t.Fatalf("读账本基线失败: %v", err)
	}
	path := "/api/product/answer?tenant_code=" + p.TenantCode + "&page_code=" + p.PageCode
	code, out := callProductAPI(t, srv, http.MethodPost, path, "", "")
	if code != 200 {
		t.Fatalf("问答不应报错, got %d", code)
	}
	missing, _ := out["missing"].([]interface{})
	if len(missing) == 0 {
		t.Fatal("N2：缺格必须点名缺项（不许悄悄把请求转交模型）")
	}
	answer, _ := out["answer"].(string)
	if !strings.Contains(answer, "尚未备齐") {
		t.Fatalf("N2：答案必须点名缺项, got %q", answer)
	}
	var after int
	if err := srv.Store.DB().QueryRow("SELECT COUNT(*) FROM usage_ledger").Scan(&after); err != nil {
		t.Fatalf("读账本终值失败: %v", err)
	}
	if after != before {
		t.Fatalf("N2：缺格路径仍须零 token, got %d → %d", before, after)
	}
	// N3：额度钉成 exhausted_today（用现网那一条 RateDayCeilReached 尺子，不新造分桶）
	cfg := srv.instantEffective(1)
	if cfg.FreeIPDaily > 0 {
		if _, err := srv.Store.RateDayAdd(store.RateScopeFreeIP, "192.0.2.77", cfg.FreeIPDaily); err != nil {
			t.Fatalf("钉额度失败: %v", err)
		}
	}
	// 三态读数：available 与 disabled 与 exhausted 必须不同形
	if knowledgeAvailable == knowledgeExhaustedToday || knowledgeExhaustedToday == knowledgeDisabled ||
		knowledgeAvailable == knowledgeDisabled {
		t.Fatal("耗尽三态必须不同形")
	}
	if resets, ok := out["resets_at"].(string); ok && resets != "" {
		// available 档不许带 resets_at（S/X4 同族：读数不许为失败背书）
		if out["quota_state"] == knowledgeAvailable {
			t.Fatal("available 档不许带 resets_at")
		}
	}
}

// V4/V5/V6：受控字段整段缺席、留资三段解析同租户、伪造 tenant_id 不改归属。
func TestProductPublicGatedAbsentAndLeadTenantPinned(t *testing.T) {
	srv, _ := newProductAPIServer(t)
	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "受控产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	// 受控事实（报价）与受控物料
	if _, err := srv.Store.UpsertFact(1, store.ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "机密报价 8888",
		Source: store.FactSourceManual,
	}); err != nil {
		t.Fatalf("写受控事实失败: %v", err)
	}
	if _, err := srv.Store.PutAsset(1, store.ProductAssetInput{
		ProductID: p.ID, AssetType: store.AssetTypeTagline, Lang: "en", Body: "机密物料串 GATED-ASSET",
	}); err != nil {
		t.Fatalf("写受控物料失败: %v", err)
	}
	path := "/api/product/public?tenant_code=" + p.TenantCode + "&page_code=" + p.PageCode
	code, out := callProductAPI(t, srv, http.MethodGet, path, "", "")
	if code != 200 || out["found"] != true {
		t.Fatalf("公开页必须 200 且命中, got %d %v", code, out)
	}
	blob, _ := json.Marshal(out)
	if strings.Contains(string(blob), "机密报价 8888") || strings.Contains(string(blob), "GATED-ASSET") {
		t.Fatal("V4：受控值字面量不得出现在匿名响应体")
	}
	// 跨租户拼接：A 租户段 + B 页面段 ⇒ 空态（不 403、不 500）
	other, err := srv.Store.CreateProduct(2, store.ProductInput{Name: "别家产品"})
	if err != nil {
		t.Fatalf("建 B 产品失败: %v", err)
	}
	cross := "/api/product/public?tenant_code=" + p.TenantCode + "&page_code=" + other.PageCode
	code, out = callProductAPI(t, srv, http.MethodGet, cross, "", "")
	if code != 200 || out["found"] != false {
		t.Fatalf("P8/P9：跨租户拼接必须空态且 200（不 403、不 500）, got %d %v", code, out)
	}
	// V6：伪造 tenant_id 不改落库归属（归属只取产品行）
	ctx, err := srv.Store.ResolveProductLeadContext(p.TenantCode, p.PageCode, "product_page")
	if err != nil {
		t.Fatalf("三段解析失败: %v", err)
	}
	if ctx.Product.TenantID != p.TenantID || ctx.TenantCode != p.TenantCode || ctx.PageCode != p.PageCode {
		t.Fatal("V5：handle → 产品 → 租户 三段必须同一租户")
	}
	leadID, err := srv.Store.CreateProductLead(ctx, "留言", "en")
	if err != nil || leadID <= 0 {
		t.Fatalf("V3：留资必须落库, got id=%d err=%v", leadID, err)
	}
	var gotTid int64
	var gotProduct, gotTC, gotPC, gotSource string
	if err := srv.Store.DB().QueryRow(
		"SELECT tenant_id, product_id, tenant_code, page_code, lead_source FROM feedbacks WHERE id=?", leadID,
	).Scan(&gotTid, &gotProduct, &gotTC, &gotPC, &gotSource); err != nil {
		t.Fatalf("回读留资四列失败: %v", err)
	}
	if gotTid != p.TenantID || gotProduct != p.ID || gotTC != p.TenantCode || gotPC != p.PageCode {
		t.Fatalf("V6：落库归属必须来自产品行, got tid=%d product=%s tc=%s pc=%s", gotTid, gotProduct, gotTC, gotPC)
	}
}

// S1–S4：默认开、可关、关后不被改名/live 翻转重新打开、sitemap 只出 URL。
func TestProductSeoIndexingToggleAndSitemap(t *testing.T) {
	srv, tok := newProductAPIServer(t)
	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "SEO 产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if p.SeoIndexable != 1 {
		t.Fatal("S4：出厂必须默认开")
	}
	if err := srv.Store.SetProductStatus(1, p.ID, store.ProductStatusLive); err != nil {
		t.Fatalf("转 live 失败: %v", err)
	}
	// 正向对照：live 且默认开 ⇒ sitemap 里出现该 URL
	code, body := callSitemap(t, srv)
	if code != 200 || !strings.Contains(body, "/p/"+p.TenantCode+"/"+p.PageCode) {
		t.Fatalf("S1 正向对照失效：默认开且 live 必须进 sitemap, got code=%d body=%s", code, body)
	}
	// 卖家显式关（入参名与前端 seoSet() 同源＝`indexable`）
	_, out := callProductAPI(t, srv, http.MethodPost, "/api/product/seo", tok,
		`{"product_id":"`+p.ID+`","indexable":false}`)
	if out["seo_indexable"] != false {
		t.Fatalf("S1：卖家必须能显式关, got %v", out)
	}
	// ★ 正向对照：同一口送 `indexable:true` 必须能**开回来**。
	//   缺这一腿时"字段名对不上⇒永远解出 false 零值"这类缺陷结构性测不到
	//   ——旧形态入参写作 `on` 而前端送 `indexable`，上面那条"关成功"照样绿（关＝零值，恰好巧合）。
	if _, out = callProductAPI(t, srv, http.MethodPost, "/api/product/seo", tok,
		`{"product_id":"`+p.ID+`","indexable":true}`); out["seo_indexable"] != true {
		t.Fatalf("S1 正向对照失效：`indexable:true` 必须真被读到并开回来, got %v", out)
	}
	if _, out = callProductAPI(t, srv, http.MethodPost, "/api/product/seo", tok,
		`{"product_id":"`+p.ID+`","indexable":false}`); out["seo_indexable"] != false {
		t.Fatalf("S1：二次关必须又能关掉（正向对照不是单向闸）, got %v", out)
	}
	code, body = callSitemap(t, srv)
	if code != 200 || strings.Contains(body, "/p/"+p.TenantCode+"/"+p.PageCode) {
		t.Fatal("S1：关掉后 sitemap 必须同时消失")
	}
	// S4：改名 + live 翻转不许把 SEO 重新打开
	if err := srv.Store.UpdateProductName(1, p.ID, "改过名字的 SEO 产品"); err != nil {
		t.Fatalf("改名失败: %v", err)
	}
	if err := srv.Store.SetProductStatus(1, p.ID, store.ProductStatusDraft); err != nil {
		t.Fatalf("翻回 draft 失败: %v", err)
	}
	if err := srv.Store.SetProductStatus(1, p.ID, store.ProductStatusLive); err != nil {
		t.Fatalf("再转 live 失败: %v", err)
	}
	got, ok, err := srv.Store.GetProductScoped(1, p.ID)
	if err != nil || !ok {
		t.Fatalf("回读产品失败: %v", err)
	}
	if got.SeoIndexable != 0 {
		t.Fatal("S4：关一次后，改名/live 翻转不许把索引重新打开")
	}
	// S2：受控字段不进 sitemap / meta
	if strings.Contains(body, "机密") || strings.Contains(body, "gated") {
		t.Fatal("S2：sitemap 只出 URL，不得含受控字段")
	}
}

// callSitemap 打 /sitemap.xml 并返回状态码与响应体。
func callSitemap(t *testing.T, srv *Server) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	return w.Code, w.Body.String()
}

// ============ 对外页直出面（product_ssr.go · 裁定 6＋§5.8） ============
//
// 四条判据各治一病：SSR1 元数据在位且不泄受控值／SSR2 未发布页不许被索引／
// SSR3 跨租户拼接与形状不合必须真 404（G6 同一条尺子）／SSR4 无 JS 首屏不许落进 `#root` 里面
// （落进去＝启动哨兵那条 `#root.childElementCount` 判据被喂饱，把"界面没起来"永久判成成功）。

// newProductDist 给 Server 挂一张只含 SPA 壳的最小 dist，返回该目录。
// 参数：t=测试句柄；返回 dist 根目录（已写 index.html）。
func newProductDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	shell := `<!DOCTYPE html><html><head><title>能言 LangCross</title></head><body>` +
		`<div id="root"></div><script type="module" src="/assets/index-abcdefgh.js"></script></body></html>`
	if err := os.WriteFile(dir+"/index.html", []byte(shell), 0o644); err != nil {
		t.Fatalf("写 dist 失败: %v", err)
	}
	return dir
}

// callHTML 匿名打一个 HTML 路径，返回状态码与响应体原文。
func callHTML(t *testing.T, srv *Server, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code, w.Body.String()
}

// SSR1/SSR2/SSR3/SSR4：对外页直出的元数据、索引面、404 面与首屏落点。
func TestProductPublicPageSsrInjection(t *testing.T) {
	srv, _ := newProductAPIServer(t)
	srv.Dist = newProductDist(t)

	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "整机产品", Tagline: "一句话卖点"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if err := srv.Store.SetProductStatus(1, p.ID, store.ProductStatusLive); err != nil {
		t.Fatalf("转 live 失败: %v", err)
	}
	// 一条公开事实＋一条受控事实：公开的那句必须出现在首屏，受控那句一个字都不许出现
	if _, err := srv.Store.UpsertFact(1, store.ProductFactInput{
		ProductID: p.ID, Kind: "spec", Key: "lead_time", Value: "SSR公开交期",
		Visibility: store.VisibilityPublic, Source: store.FactSourceManual,
	}); err != nil {
		t.Fatalf("写公开事实失败: %v", err)
	}
	rows, e := srv.Store.ListFacts(1, p.ID)
	if e != nil || len(rows) == 0 {
		t.Fatalf("回读事实失败: %v len=%d", e, len(rows))
	}
	if err := srv.Store.ConfirmFact(1, rows[0].ID, 1); err != nil {
		t.Fatalf("确认事实失败: %v", err)
	}
	if _, err := srv.Store.UpsertFact(1, store.ProductFactInput{
		ProductID: p.ID, Kind: "price", Key: "unit", Value: "SSR受控价 8888",
		Visibility: store.VisibilityGated, Source: store.FactSourceManual,
	}); err != nil {
		t.Fatalf("写受控事实失败: %v", err)
	}
	other, err := srv.Store.CreateProduct(2, store.ProductInput{Name: "别家整机"})
	if err != nil {
		t.Fatalf("建 B 产品失败: %v", err)
	}

	code, html := callHTML(t, srv, "/p/"+p.TenantCode+"/"+p.PageCode)
	if code != 200 {
		t.Fatalf("SSR3 正向对照失效：合法两段短码必须 200, got %d", code)
	}
	for _, want := range []string{"整机产品", "SSR公开交期", `rel="canonical"`, `content="index,follow"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("SSR1：直出面必须含 %q，实际只有壳（说明注入腿没挂上 serveIndexHTML）", want)
		}
	}
	if strings.Contains(html, "SSR受控价") || strings.Contains(html, "8888") {
		t.Fatal("SSR1／§5.8 S2：受控值字面量不许进直出面（title／meta／JSON-LD／首屏四处都不行）")
	}
	if !strings.Contains(html, `hreflang=`) {
		t.Fatal("SSR1：live＋默认开索引 ⇒ 必须铺 hreflang 语言版本（与静态路由同一张码表）")
	}
	// SSR4：无 JS 首屏必须是 `#root` 的**兄弟**，不是它的孩子
	root := strings.Index(html, `<div id="root"></div>`)
	first := strings.Index(html, `<noscript id="__product_first__">`)
	if root < 0 || first < 0 || first < root {
		t.Fatalf("SSR4：首屏落点必须在 #root 之后（root=%d first=%d）——插进 root 里面会喂饱启动哨兵的 childElementCount 判据", root, first)
	}

	// SSR2：未发布（draft）页对爬虫必须 noindex，但地址本身仍可达（可见性面与索引面两把尺子）
	draft, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "草稿产品"})
	if err != nil {
		t.Fatalf("建草稿失败: %v", err)
	}
	code, html = callHTML(t, srv, "/p/"+draft.TenantCode+"/"+draft.PageCode)
	if code != 200 || !strings.Contains(html, `content="noindex,follow"`) {
		t.Fatalf("SSR2：draft 页必须 200 且 noindex, got code=%d", code)
	}
	// 卖家显式关索引 ⇒ robots 仍 index（关的是 sitemap/hreflang，不是"页面不见了"），但 hreflang 必须撤
	if err := srv.Store.SetProductSeoIndexable(1, p.ID, false); err != nil {
		t.Fatalf("关索引失败: %v", err)
	}
	_, html = callHTML(t, srv, "/p/"+p.TenantCode+"/"+p.PageCode)
	if strings.Contains(html, `hreflang=`) {
		t.Fatal("SSR1／S1：关掉 seo_indexable 后 canonical 族的语言版本必须同时消失")
	}

	// SSR3：跨租户拼接／形状不合／缺段 ⇒ 真 404（不 403、不 500、不出别家内容）
	for _, bad := range []string{
		"/p/" + p.TenantCode + "/" + other.PageCode, // A 租户段 ＋ B 页面段
		"/p/" + p.TenantCode,                        // 缺一段
		"/p/" + p.TenantCode + "/" + p.PageCode + "/extra",
	} {
		code, html = callHTML(t, srv, bad)
		if code != http.StatusNotFound {
			t.Fatalf("SSR3：%s 必须真 404（G6 同一条尺子），got %d", bad, code)
		}
		if strings.Contains(html, "别家整机") {
			t.Fatalf("SSR3／P10：%s 不许出现别家产品的内容", bad)
		}
	}
	// 反向对照：伪造 ?tenant_id= 不改归属，仍解到两段码那个租户的 A 页
	code, html = callHTML(t, srv, "/p/"+p.TenantCode+"/"+p.PageCode+"?tenant_id=9999")
	if code != 200 || !strings.Contains(html, "整机产品") {
		t.Fatalf("P7：不存在的 tenant_id 参数必须被忽略（仍 200 且展示 A），got code=%d", code)
	}

	// P10 尺子腿（源码级）：同租户校验必须走 `tenantByHost`，**不许**退回 `tenant.FromContext`。
	// 中间件对匿名请求一律注 tenant 1（server.go 第 4 支：历史 rox 默认档），那把尺子答的是
	// "这条请求按哪个租户记账"，不是"这台域名叫谁"——拿它判域名 ⇒ 2 号及以后租户的对外页与
	// 知识问答恒 not-found（本轮 T89／T92 整片红的真身）。行为腿需要 tenants 表＋品牌域配置，
	// 而这一条判的是"用哪一把尺子"，源码级足够且有射程：改回 FromContext 当场红。
	rawPub, err := os.ReadFile("product_public_api.go")
	if err != nil {
		t.Fatalf("读不到 product_public_api.go: %v", err)
	}
	codes := findFuncBody(string(rawPub), "func (s *Server) productResolvedCodes")
	if codes == "" {
		t.Fatal("P10 尺子腿失明：读不到 productResolvedCodes 函数体")
	}
	// ★ 先剥注释再判（本仓第四次踩同一坑，复用的就是包内那把既有尺子，不另造第二份）。
	//   函数体第 89 行的说明注释里**逐字写着** `tenant.FromContext`——那正是本条要禁的写法，
	//   写在注释里是为了让下一位别再把它当域名尺子用；不剥注释就是把「记下了坑」读成「踩了坑」。
	//   剥完仍然有射程：代码行里出现 FromContext 当场红（本条的变异体登记在 F 组收口时补进 gate_mutants.tsv，
	//   锚点取代码行那一句，不取注释）。
	codes = stripApiGoCommentsForSegGate(codes)
	if codes == "" {
		t.Fatal("P10 尺子腿失明：剥注释后函数体一行不剩")
	}
	if !strings.Contains(codes, "s.tenantByHost(r)") {
		t.Fatal("P10：对外面必须按**访问域名**判同租户（tenantByHost），不是按记账租户")
	}
	if strings.Contains(codes, "tenant.FromContext") {
		t.Fatal("P10：不许把 tenant.FromContext 当「访问域名」用——匿名请求它恒回 1")
	}
	// P10 射程自证（本批新补）：**剥注释这条修法不许把代码行一起剥掉**。
	// 造一份"把 FromContext 写在代码行上"的副本喂给同一个判据（只活在内存里，不落源文件）：
	// 读不到 ⇒ 说明剥注释剥过头了，负向腿已经恒真是空转；替换没落地 ⇒ 变异是 no-op，同样判红。
	probe := strings.Replace(codes, "if hostTid := s.tenantByHost(r);", "if hostTid := tenant.FromContext(r.Context());", 1)
	if probe == codes {
		t.Fatal("P10 射程自证失败：探针变异没落地（no-op 变异体等于给缺陷发证件）")
	}
	if !strings.Contains(probe, "tenant.FromContext") {
		t.Fatal("P10 射程自证失败：剥注释后连代码行都读不到了 ⇒ 这条负向腿是空转")
	}
}
