// ============ 本文件职责中文说明 ============
// 一期 X2「物料文件腿」门禁（2026-11-06）：上传白名单派生、只回 basename、
// 取回口与 JSON 口**共用同一条作废尺子**、跨租户 404 不 403、脏 ref 防路径穿越。
//
// ★ 三枚靶子各治一病，一条都不许省：
//
//	F8 的 expired 靶子必须**由真链路造**（改事实值 ⇒ ExpireAssetsByFactChange 那唯一的写点），
//	   手改库只测到读点，测不到"这把尺子只有一把"。
//	F7 喂的是**旁路 SQL 写进来的脏 ref**（`../outside.txt`），这是 artifact_ref 唯一的越权读盘形态；
//	   只测"上传口清洗得好"等于没测读盘侧。
//	F10 是源码级腿：白名单必须从 fileproc 现读派生（AGENTS §一·14 那份清单只有一份）。
//
// ★ 所有用例自钉 SQLite 方言（pinSqliteDialect），走真路由表 srv.routes()。
// ========================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/iam"
	"translator/internal/store"
)

// newAssetFileServer 起内存库＋全量路由，并**额外交出 *sql.DB**给 F7 造脏 ref 那一档
// （脏值只能由"旁路写库"造出来，store 不提供 Debug 写口，也绝不许为测试开一个生产写口）。
func newAssetFileServer(t *testing.T) (*Server, string, *sql.DB) {
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
	u, err := st.CreateUser(1, "asset_file_user", auth.PasswordHash("pw123456"), "物料用户", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	// ★ 复制一份 Config 再改 UploadDir：srv.Cfg 与全局 config.C 是同一个指针，
	//   直接赋值会把后面所有用例的上传目录一起搬进本用例的临时目录（跨用例污染）。
	cfgCopy := *config.C
	cfgCopy.UploadDir = t.TempDir()
	srv := &Server{
		Cfg: &cfgCopy, Store: st,
		mux: newRouteMux(), metrics: newMetrics(),
		loginLimit: newLoginLimiter(st), regGuard: newRegisterGuard(st),
	}
	srv.routes()
	return srv, tok, conn
}

// postAssetFile 打一次 multipart 上传；fileName 决定扩展名那一档判定。
func postAssetFile(t *testing.T, srv *Server, tok, fileName, content string) (int, map[string]interface{}) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}
	if _, err = fw.Write([]byte(content)); err != nil {
		t.Fatalf("写 multipart 段失败: %v", err)
	}
	if err = mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/product/asset/upload", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
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

// getFileFull 打一次文件取回，返回状态、响应体、Content-Disposition 与 nosniff。
func getFileFull(t *testing.T, srv *Server, tok string, id int64) (int, string, string, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/product/asset/file?id="+strconv.FormatInt(id, 10), nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.mux.ServeHTTP(w, r)
	return w.Code, w.Body.String(), w.Header().Get("Content-Disposition"), w.Header().Get("X-Content-Type-Options")
}

// callAssetJSON 打一个产品物料面的 JSON 请求。
func callAssetJSON(t *testing.T, srv *Server, method, path, tok, body string) (int, map[string]interface{}) {
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
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestProductAssetFileLegGates 物料文件腿的九档行为＋两档源码级接线。
func TestProductAssetFileLegGates(t *testing.T) {
	srv, tok, conn := newAssetFileServer(t)
	uploadRoot := srv.Cfg.UploadDir
	assetDir := filepath.Join(uploadRoot, productAssetDirName)

	p, err := srv.Store.CreateProduct(1, store.ProductInput{Name: "整机", UserID: 1})
	if err != nil {
		t.Fatalf("建产品失败: %v", err)
	}
	fact, err := srv.Store.UpsertFact(1, store.ProductFactInput{ProductID: p.ID, Kind: "capacity", Key: "产能", Value: "1200台/月"})
	if err != nil {
		t.Fatalf("建事实失败: %v", err)
	}

	// F1：不在准入清单里的扩展名一律拒收，且磁盘上一个字节都不落。
	if code, _ := postAssetFile(t, srv, tok, "evil.exe", "MZ"); code < 400 {
		t.Fatalf("F1：.exe 必须被白名单拒收，实得 code=%d", code)
	}
	// F1b：已撤除的纯文本容器同样不在清单里，但必须给那句唯一出路文案（不是"不支持"了事）。
	if code, body := postAssetFile(t, srv, tok, "caption.srt", "1\n00:00\nhi"); code >= 400 {
		if msg, _ := body["message"].(string); !strings.Contains(msg, fileproc.PlainTextContainerHint) {
			t.Fatalf("F1b：撤除格式必须给唯一出路文案，实得 code=%d msg=%q", code, msg)
		}
	} else {
		t.Fatalf("F1b：已撤除的 .srt 必须拒收，实得 code=%d", code)
	}
	if de, _ := os.ReadDir(assetDir); len(de) != 0 {
		t.Fatalf("F1：被拒的上传不许在磁盘留件，现存 %d 份", len(de))
	}

	// F2：合法件成功；出栈 artifact_ref 必须是**纯 basename**，盘上权限不许放开（私有资产）。
	code, body := postAssetFile(t, srv, tok, "spec.pdf", "%PDF-1.4 fake")
	if code != http.StatusOK {
		t.Fatalf("F2：合法上传必须 200，实得 code=%d body=%v", code, body)
	}
	ref, _ := body["artifact_ref"].(string)
	if ref == "" || ref != filepath.Base(ref) || strings.ContainsAny(ref, `/\`) {
		t.Fatalf("F2：artifact_ref 必须是纯 basename，实得 %q", ref)
	}
	fi, err := os.Stat(filepath.Join(assetDir, ref))
	if err != nil || fi.IsDir() {
		t.Fatalf("F2：文件必须真落在物料目录（err=%v）", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("F2：物料件权限不许组/其他可读，实得 %o", fi.Mode().Perm())
	}

	// F3：未登录不许落盘（鉴权排在写盘之前）。
	if code, _ := postAssetFile(t, srv, "", "spec2.pdf", "x"); code != http.StatusUnauthorized {
		t.Fatalf("F3：匿名上传必须 401，实得 code=%d", code)
	}

	// F4：挂上附件的物料行——走现成 put 口，验证 ref 原样进库、原样取得（正向对照）。
	_, body = callAssetJSON(t, srv, http.MethodPost, "/api/product/asset/put", tok,
		`{"product_id":"`+p.ID+`","asset_type":"quote_sheet","lang":"en","body":"Capacity 1200 units/month","artifact_ref":"`+ref+`","fact_refs":[`+strconv.FormatInt(fact.ID, 10)+`]}`)
	if ok, _ := body["success"].(bool); !ok {
		t.Fatalf("F4：物料写入应成功，实得 %v", body)
	}
	assetID, _ := body["id"].(float64)
	if assetID <= 0 {
		t.Fatalf("F4：出栈缺物料 id：%v", body)
	}
	if code, raw, disp, nosniff := getFileFull(t, srv, tok, int64(assetID)); code != http.StatusOK || raw != "%PDF-1.4 fake" {
		t.Fatalf("F4：合法取回必须 200 且逐字节等值，实得 code=%d len=%d", code, len(raw))
	} else if !strings.HasPrefix(disp, "attachment") || nosniff != "nosniff" {
		t.Fatalf("F4b：取回必须带 attachment 与 nosniff（不许 inline 渲染报价单），实得 %q/%q", disp, nosniff)
	}

	// F5：无附件的纯文案物料 ⇒ 出声点名，不许回一个空 200（会被读成"附件是空的"）。
	_, body = callAssetJSON(t, srv, http.MethodPost, "/api/product/asset/put", tok,
		`{"product_id":"`+p.ID+`","asset_type":"tagline","lang":"en","body":"Pure text asset"}`)
	textID, _ := body["id"].(float64)
	if code, b := callAssetJSON(t, srv, http.MethodGet, "/api/product/asset/file?id="+strconv.FormatInt(int64(textID), 10), tok, ""); code != http.StatusBadRequest {
		t.Fatalf("F5：无附件必须 400 并点名，实得 code=%d", code)
	} else if msg, _ := b["message"].(string); !strings.Contains(msg, "没有附件") {
		t.Fatalf("F5：文案必须说明「没有附件，正文在详情里」，实得 %q", msg)
	}

	// F6：他租户的行用本租户 token 取 ⇒ 404（**不返 403**，不泄露存在性）。
	other, err := srv.Store.CreateProduct(2, store.ProductInput{Name: "他司整机", UserID: 9})
	if err != nil {
		t.Fatalf("F6：建第二租户产品失败: %v", err)
	}
	oa, err := srv.Store.PutAsset(2, store.ProductAssetInput{ProductID: other.ID, AssetType: "quote_sheet", Lang: "en", Body: "x", ArtifactRef: ref})
	if err != nil {
		t.Fatalf("F6：建他租户物料失败: %v", err)
	}
	if code, _ := callAssetJSON(t, srv, http.MethodGet, "/api/product/asset/file?id="+strconv.FormatInt(oa.ID, 10), tok, ""); code != http.StatusNotFound {
		t.Fatalf("F6：跨租户取件必须 404（不 403），实得 code=%d", code)
	}

	// F7：旁路 SQL 把 ref 写成 `../outside.txt` ⇒ 读盘侧第二道护栏必须挡住，且不许把盘外内容吐出去。
	secret := filepath.Join(uploadRoot, "outside.txt")
	if err = os.WriteFile(secret, []byte("TOP-SECRET"), 0o600); err != nil {
		t.Fatalf("F7：造靶子文件失败: %v", err)
	}
	dirty, err := srv.Store.PutAsset(1, store.ProductAssetInput{ProductID: p.ID, AssetType: "quote_sheet", Lang: "en", Body: "x"})
	if err != nil {
		t.Fatalf("F7：建脏 ref 靶子行失败: %v", err)
	}
	if _, err = conn.Exec(`UPDATE product_assets SET artifact_ref='../outside.txt' WHERE id=?`, dirty.ID); err != nil {
		t.Fatalf("F7：写脏 ref 失败（这条腿就是判据本体）: %v", err)
	}
	if code, raw, _, _ := getFileFull(t, srv, tok, dirty.ID); code == http.StatusOK || strings.Contains(raw, "TOP-SECRET") {
		t.Fatalf("F7：脏 ref 必须取不到盘外文件，实得 code=%d 命中盘外内容=%v", code, strings.Contains(raw, "TOP-SECRET"))
	}

	// F7b：**单独证明 basename 那一层的射程**（不是只证明 resolveSafePath 在挡）。
	//
	// 靶子选「物料目录**之内**的一个子目录相对名」：`sub/inside.txt` 过得了 resolveSafePath（清洗后仍在
	// `_product_assets` 里），但过不了 basename 等值——因为上传口对外的契约是**库里只存 basename**。
	// 少了这一层，脏值可以指到同目录树里别人挂上来的任意一件（同租户内不同行之间也不该互相拿得到）。
	if err = os.MkdirAll(filepath.Join(assetDir, "sub"), 0o750); err != nil {
		t.Fatalf("F7b：造子目录失败: %v", err)
	}
	if err = os.WriteFile(filepath.Join(assetDir, "sub", "inside.txt"), []byte("NEIGHBOUR-ASSET"), 0o600); err != nil {
		t.Fatalf("F7b：造子目录靶子件失败: %v", err)
	}
	nested, err := srv.Store.PutAsset(1, store.ProductAssetInput{ProductID: p.ID, AssetType: "quote_sheet", Lang: "en", Body: "x"})
	if err != nil {
		t.Fatalf("F7b：建子目录 ref 靶子行失败: %v", err)
	}
	if _, err = conn.Exec(`UPDATE product_assets SET artifact_ref='sub/inside.txt' WHERE id=?`, nested.ID); err != nil {
		t.Fatalf("F7b：写子目录 ref 失败: %v", err)
	}
	if code, raw, _, _ := getFileFull(t, srv, tok, nested.ID); code == http.StatusOK || strings.Contains(raw, "NEIGHBOUR-ASSET") {
		t.Fatalf("F7b：非 basename 形态的 ref 一律拒（库里只存文件名这条契约），实得 code=%d", code)
	}

	// F8：真链路造 expired（改事实值 ⇒ 唯一的作废写点），**两个口必须一起拦**（A3 一把尺子）。
	if _, err = srv.Store.UpsertFact(1, store.ProductFactInput{ProductID: p.ID, Kind: "capacity", Key: "产能", Value: "1500台/月"}); err != nil {
		t.Fatalf("F8：改事实失败: %v", err)
	}
	if got, _ := srv.Store.ListAssets(1, p.ID); len(got) == 0 {
		t.Fatal("F8：靶子行读不回来，作废判据无从谈起")
	}
	if code, _, _, _ := getFileFull(t, srv, tok, int64(assetID)); code != http.StatusConflict {
		t.Fatalf("F8：作废物料的文件口必须 409（与 JSON 口同一把尺子），实得 code=%d", code)
	}
	if code, _ := callAssetJSON(t, srv, http.MethodGet, "/api/product/asset/download?id="+strconv.FormatInt(int64(assetID), 10), tok, ""); code != http.StatusConflict {
		t.Fatalf("F8：作废物料的 JSON 口也必须 409，实得 code=%d（两口分尺＝A3 只剩半只锁）", code)
	}
}

// TestProductAssetFileLegWiring 源码级接线：白名单派生、作废尺子、鉴权位置、路由注册。
func TestProductAssetFileLegWiring(t *testing.T) {
	raw, err := os.ReadFile("product_asset_file.go")
	if err != nil {
		t.Fatalf("读不到 product_asset_file.go: %v", err)
	}
	src := stripApiGoCommentsForSegGate(string(raw))
	if strings.TrimSpace(src) == "" {
		t.Fatal("接线腿失明：剥注释后一行不剩")
	}
	// 派生腿必须在位，且**不许**内联第二份 Office/PDF 清单（AGENTS §一·14 同族）。
	if !strings.Contains(src, "fileproc.TranslateFormatOrder") {
		t.Fatal("W1：白名单必须从 fileproc.TranslateFormatOrder 派生")
	}
	if strings.Contains(src, `".docx"`) && strings.Contains(src, `".xlsx"`) && strings.Contains(src, `".pptx"`) {
		t.Fatal("W1：物料口里出现了内联格式清单 ⇒ 第二把尺子")
	}
	// 作废只有一把尺子。
	if !strings.Contains(src, "AssetForDownload") {
		t.Fatal("W2：文件口必须走 Store.AssetForDownload（A3 单源）")
	}
	// 两个 handler 的第一次取人必须排在任何盘/库动作之前。
	for _, fn := range []string{"func (s *Server) handleProductAssetUpload", "func (s *Server) handleProductAssetFile"} {
		i := strings.Index(src, fn)
		if i < 0 {
			t.Fatalf("W3：读不到 handler %s", fn)
		}
		head := src[i:]
		if j := strings.Index(head, "\n}"); j > 0 {
			head = head[:j]
		}
		if !strings.Contains(head, "s.productActor(r)") {
			t.Fatalf("W3：%s 必须先过 productActor", fn)
		}
	}
	// 路由注册（漏一条＝界面上那颗按钮永远 404，而单测看不见）。
	routes, err := os.ReadFile("product_api.go")
	if err != nil {
		t.Fatalf("W4：读不到 product_api.go: %v", err)
	}
	for _, path := range []string{"/api/product/asset/upload", "/api/product/asset/file"} {
		if !strings.Contains(string(routes), `"`+path+`"`) {
			t.Fatalf("W4：路由 %s 未在 routesProduct 注册", path)
		}
	}
	// 上传件必须落在独立命名空间，且**不许**落进 web 根（落进去＝Caddy 直出，绕过全部鉴权）。
	if !strings.Contains(src, `productAssetDirName = "_product_assets"`) {
		t.Fatal("W5：物料件必须有独立命名空间")
	}
	for _, bad := range []string{"web/", "/opt/translator/web", "StaticDir"} {
		if strings.Contains(src, bad) {
			t.Fatalf("W5：物料上传口不许落到 %s（那是可被静态直出的面）", bad)
		}
	}
}
