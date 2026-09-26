// ============================================================================
// internal/api/brand_assets_test.go — 品牌图落静态件 + 首屏注入只带 URL 的闸门
// （★ F-46，2026-09-26 批 I-9）
//
// 这组锁按「F-46 怎么发生、就怎么反证」排布，共八组：
//
//	① dataURI → 静态件：返回值必须是 /brand/<owner>-<哈希>.<扩展名>，盘上字节等于解码字节，
//	   且**重复调用不产生第二份文件**（内容寻址的幂等性，否则首屏每刷一次写一次盘）；
//	② 收敛口径：http(s) 与本站根相对路径原样透出；协议相对 //host、svg、伪 PNG（字节与
//	   声明不符）、畸形 dataURI、超硬上限一律回空串（= 回落默认背景），
//	   **任何输入都不允许吐出 dataURI**（这条是首屏体积能锁死 30 KB 的前提）；
//	③ 咽喉点：brandingPayload 的平台分支喂 1.2 MB dataURI，出栈的 brand_logo/brand_home_bg
//	   必须已是 /brand/…，且 payload 序列化后整串不含 "data:image"；
//	④ 写侧严格：传了内容却转不出可信图形 ⇒ 400（**不许**静默清空租户已有品牌）；
//	   未携带（空串）仍按既有"清空"语义走；已是 URL 的再存一次必须原样保留（读写同源）；
//	⑤ 首屏体积与缓存口径：serveIndexHTML 在 1.2 MB 存量 dataURI 的前提下，
//	   出参 HTML 仍 < 30 KB、不含 data:image、且 HTML 体积必须远小于注入值本身（病根对照）；
//	   缓存一律 no-cache —— 本批**推翻**了修复文档里"无品牌站给 public max-age=60"的改法
//	   （线上实测 Cloudflare 对 HTML 回 DYNAMIC + no-cache,must-revalidate，长缓存换不来收益，
//	    却会埋下"开了边缘缓存后跨域名串品牌"的雷；决策全文在 spa.go serveIndexHTML 注释）；
//	⑥ /brand/ 直出面：200 + 正确 Content-Type + immutable + nosniff；
//	   缺件 404 JSON、非白名单后缀 404、含斜杠/点开头 404、POST 405、
//	   盘上字节与扩展名不符 ⇒ 500（宁红不假绿，不把坏字节当图发）；
//	⑦ 真接线：生产 routes() 的 mux 上 /brand/<件> 必须落在静态件处理方，
//	   **不得**被 spa.go 的 "/" 兜底吃成 200 整页 HTML（托管物判据，AGENTS §一·6）。
//	⑧ 自愈门：盘上的品牌件被写成"同尺寸但内容不是图"时，读侧不许按"已就位"复用，
//	   必须重写自愈（本锁由 deploy 冒烟脚本 --selftest 首次跑红暴露，见该用例注释）。
//
// 运行：go test -count=1 -run 'TestBrand|TestServeIndex' ./internal/api/
// ============================================================================
package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
)

// brandPNG 只保证**魔数**为 PNG 的合成件（brandMagicMatches 只验头部 8 字节）。
// 刻意不放真图：闸门要测的是「声明与字节是否相符、有没有把 dataURI 漏进 HTML」，
// 与图像编码内容无关；塞真图只会让测试文件带上二进制噪声。
var brandPNG = append([]byte("\x89PNG\r\n\x1a\n"), []byte("LC-BRAND-TEST-BLOB-0123456789")...)

// brandDataURI 按浏览器 dataURL 口径拼一条 data:<mime>;base64,<payload>。
func brandDataURI(mime string, b []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// brandEnv 起一个「有 Store、有 UserDataDir」的最小服务端（品牌闸门不需要引擎/租户存储）。
func brandEnv(t *testing.T) *Server {
	t.Helper()
	s := f41StoreServer(t)
	s.Cfg = &config.Config{UserDataDir: t.TempDir()}
	return s
}

// brandFileNames 列目录（判「有没有产生第二份文件」用）。
func brandFileNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("读品牌件目录失败: %v", err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// ---------------------------------------------------------------------------
// ① + ② 收敛口径
// ---------------------------------------------------------------------------

func TestBrandImageURLMaterializesDataURI(t *testing.T) {
	s := brandEnv(t)
	ctx := t.Context()
	uri := brandDataURI("image/png", brandPNG)

	got := s.brandImageURL(ctx, brandAssetOwner(5, "logo"), uri)
	if !strings.HasPrefix(got, brandAssetPrefix) {
		t.Fatalf("① 期望返回 %s 前缀的 URL，实际 %q", brandAssetPrefix, got)
	}
	if strings.Contains(got, "data:") {
		t.Fatalf("① 返回值仍含 data: 片段: %q", got)
	}
	name := strings.TrimPrefix(got, brandAssetPrefix)
	if !strings.HasPrefix(name, "t5-logo-") || !strings.HasSuffix(name, ".png") {
		t.Fatalf("① 文件名口径不符（owner 前缀 + 内容哈希 + 扩展名）: %q", name)
	}
	p := filepath.Join(s.brandAssetDir(), name)
	disk, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("① 静态件没落盘 %s: %v", p, err)
	}
	if !bytes.Equal(disk, brandPNG) {
		t.Fatalf("① 盘上字节与解码字节不符（盘 %d B / 期望 %d B）", len(disk), len(brandPNG))
	}
	// 幂等：同内容再解一次必须复用同一文件名，且目录里仍只有一件
	again := s.brandImageURL(ctx, brandAssetOwner(5, "logo"), uri)
	if again != got {
		t.Fatalf("① 重复调用 URL 漂移: %q vs %q", got, again)
	}
	if files := brandFileNames(t, s.brandAssetDir()); len(files) != 1 {
		t.Fatalf("① 重复调用写出了第二份文件: %v", files)
	}
	// 不同 owner 同内容 ⇒ 两件（归属信息在文件名里，排障时能认出这是谁的图）
	other := s.brandImageURL(ctx, brandAssetOwner(6, "logo"), uri)
	if other == got {
		t.Fatalf("② 不同 owner 拿到了同一文件名，归属信息被抹掉: %q", other)
	}
}

func TestBrandImageURLPassthroughAndRejections(t *testing.T) {
	s := brandEnv(t)
	ctx := t.Context()
	// 已合法的两类输入：原样透出（不二次落件）
	for _, v := range []string{"https://cdn.example.com/a.png", "http://a/b.png", "/brand/t1-logo-x.png"} {
		if got := s.brandImageURL(ctx, "t1-logo", v); got != v {
			t.Fatalf("② URL 输入应原样透出: want %q got %q", v, got)
		}
	}
	// 一律回空（= 回落默认背景）的六类：任何一种只要被"宽容"透出，
	// 首屏体积锁与格式锁就同时失效，所以逐条点名。
	bad := map[string]string{
		"空串":          "",
		"仅空白":         "   ",
		"协议相对 URL":    "//evil.example.com/x.png", // 浏览器会解析到外站，不是本站根路径
		"反斜杠伪绝对":      `\brand\x.png`,
		"裸文本":         "not-an-image-at-all",
		"SVG dataURI": brandDataURI("image/svg+xml", []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>")), // 可带脚本的文档，不同源直出位
		"非 base64 段":  "data:image/png;charset=utf-8,%89PNG",
		"缺逗号":         "data:image/png;base64",
		"字节与声明不符":     brandDataURI("image/png", []byte("<!DOCTYPE html><script>alert(1)</script>")),
	}
	for name, v := range bad {
		if got := s.brandImageURL(ctx, "t1-logo", v); got != "" {
			t.Fatalf("② %s 应回落空串（宁缺不假），实际 %q", name, got)
		}
	}
	// 超硬上限：payload 长度超限即拒（防「品牌字段里躺 50 MB」）
	huge := "data:image/png;base64," + strings.Repeat("QUJD", brandAssetMaxB64Chars/4+8)
	if got := s.brandImageURL(ctx, "t1-logo", huge); got != "" {
		t.Fatalf("② 超上限输入应拒绝，实际 %q", got[:min(len(got), 40)])
	}
	if files := brandFileNames(t, s.brandAssetDir()); len(files) != 0 {
		t.Fatalf("② 全部负向输入都不该落件，实际落了 %v", files)
	}
	// UserDataDir 缺失（配置没给）⇒ 不落件也不报错，回空串走默认背景
	noCfg := &Server{}
	if got := noCfg.brandImageURL(ctx, "t1-logo", brandDataURI("image/png", brandPNG)); got != "" {
		t.Fatalf("② 无 UserDataDir 时应回空串，实际 %q", got)
	}
}

func TestBrandImageFormatTablesAgree(t *testing.T) {
	// 落盘表白名单与直出表白名单必须互为反向：
	// 只改一处会造出「能落件却 404」或「路由认得但落不了件」的半截支持。
	for mime, ext := range brandImageExts {
		if _, ok := brandImageMimes[ext]; !ok {
			t.Fatalf("② mime %s 的扩展名 %s 在直出表里缺席", mime, ext)
		}
	}
	for ext := range brandImageMimes {
		found := false
		for _, e := range brandImageExts {
			if e == ext {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("② 直出表认 %s 但落盘表不认（反向不一致）", ext)
		}
	}
	if _, ok := brandImageExts["image/svg+xml"]; ok {
		t.Fatal("② SVG 被放进了品牌图白名单：同源直出可带脚本的文档是 XSS 载荷位，文件头已写明不许")
	}
}

// ---------------------------------------------------------------------------
// ③ 咽喉点：brandingPayload 出栈不得带 dataURI
// ---------------------------------------------------------------------------

func TestBrandingPayloadNeverLeaksDataURI(t *testing.T) {
	s := brandEnv(t)
	// 造一个「存量已超上限」的平台品牌：2 MB 级 dataURI（F-46 演示站的真实形态）。
	// 注意这**过不了写侧校验**，只能直连 setPlatformBranding 造——因为本锁要测的正是
	// 「上限生效前写进库的存量行，读侧还能不能兜住」。
	big := brandDataURI("image/png", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("x"), 1_200_000)...))
	if len(big) < 1_000_000 {
		t.Fatalf("夹具太小，测不出体积（%d 字符）", len(big))
	}
	if err := s.setPlatformBranding(map[string]string{
		"brand_name": "能言", "brand_logo": big, "brand_home_bg": big,
	}); err != nil {
		t.Fatalf("setPlatformBranding 失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "langcross.example.com"
	p := s.brandingPayload(r)
	for _, k := range []string{"brand_logo", "brand_home_bg"} {
		v, _ := p[k].(string)
		if v == "" {
			t.Fatalf("③ %s 被读侧抹成空了（品牌图不该凭空消失）: %v", k, p[k])
		}
		if strings.HasPrefix(v, "data:") {
			t.Fatalf("③ %s 仍是 dataURI，首屏会继续背 %d 字符: %q", k, len(v), v[:40])
		}
		if !strings.HasPrefix(v, brandAssetPrefix) {
			t.Fatalf("③ %s 期望 /brand/… 形态，实际 %q", k, v)
		}
	}
	// 整包序列化后也不许出现 data:image（防"改了字段却从别处再塞回来"）
	if strings.Contains(fmt.Sprint(p), "data:image") {
		t.Fatal("③ 品牌 payload 里仍能扫到 data:image")
	}
	if files := brandFileNames(t, s.brandAssetDir()); len(files) != 2 {
		t.Fatalf("③ 两张图应各落一件（logo/home-bg），实际 %v", files)
	}
}

// ---------------------------------------------------------------------------
// ④ 写侧严格（helper 层直接锁：handler 那条要起租户存储，锁在这里射程不减）
// ---------------------------------------------------------------------------

func TestBrandImagesForWriteIsStrictButNotDestructive(t *testing.T) {
	s := brandEnv(t)
	ctx := t.Context()
	uri := brandDataURI("image/png", brandPNG)

	// 正常上传：两张都转成 URL，无错误
	l, b, msg := s.brandImagesForWrite(ctx, 7, uri, uri)
	if msg != "" || !strings.HasPrefix(l, brandAssetPrefix) || !strings.HasPrefix(b, brandAssetPrefix) {
		t.Fatalf("④ 正常上传应通过: msg=%q logo=%q bg=%q", msg, l, b)
	}
	// 未携带（空串）：仍是"清空"语义，且**不报错**——保存别的字段时不许被品牌图拦下
	if l2, b2, msg2 := s.brandImagesForWrite(ctx, 7, "", ""); msg2 != "" || l2 != "" || b2 != "" {
		t.Fatalf("④ 空输入应通过且落空: msg=%q l=%q b=%q", msg2, l2, b2)
	}
	// 已是 URL 再存一次（后台表单只改名称时的回传形态）：原样保留，不许二次加工或报错
	if l3, _, msg3 := s.brandImagesForWrite(ctx, 7, l, ""); msg3 != "" || l3 != l {
		t.Fatalf("④ URL 复存应原样保留: want %q got %q msg=%q", l, l3, msg3)
	}
	// ★ 关键一条：传了内容却转不出可信图形 ⇒ 必须给错误说明（静默转空＝替租户删图）
	for _, bad := range []string{"garbage", "//evil/x.png", brandDataURI("image/svg+xml", []byte("<svg/>"))} {
		if _, _, m := s.brandImagesForWrite(ctx, 7, bad, ""); m == "" {
			t.Fatalf("④ 非空但不可识别的输入必须报错，实际放过了 %q", bad)
		}
		if _, _, m := s.brandImagesForWrite(ctx, 7, "", bad); m == "" {
			t.Fatalf("④ 背景图同理必须报错，实际放过了 %q", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// ⑤ 首屏：体积上限 + 缓存口径（本批决定不做无品牌站长缓存，理由见 spa.go）
// ---------------------------------------------------------------------------

// brandDist 造一份前端外壳（真实 index.html 约 2.5 KB 壳，这里给一个够用的同形夹具）。
func brandDist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	shell := "<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"UTF-8\"/>\n<title>LangCross</title>\n" +
		strings.Repeat("<!-- 首屏外壳填充，模拟真实的 meta/预加载声明 -->", 20) +
		"</head>\n<body><div id=\"root\"></div><script type=\"module\" src=\"/assets/index-BOIA4YDV.js\"></script></body>\n</html>\n"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(shell), 0o644); err != nil {
		t.Fatalf("写外壳失败: %v", err)
	}
	return dir
}

func TestServeIndexHTMLSizeAndNoCache(t *testing.T) {
	s := brandEnv(t)
	s.Dist = brandDist(t)
	big := brandDataURI("image/png", append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("y"), 1_200_000)...))

	// A) 无品牌定制：外壳原样出，且**也是 no-cache**
	//    ★ 这里曾按修复文档写成 public, max-age=60，被线上实测头推翻：主站回的是
	//      `cf-cache-status: DYNAMIC` + `no-cache, must-revalidate`（Cloudflare 那层加的），
	//      说明 HTML 在 CDN 侧根本不缓存 ⇒ 长缓存换不来收益，反而在"哪天开了 HTML 边缘缓存
	//      但缓存键没带 Host"时会串品牌（品牌站 HTML 被主站访客命中）。
	//      决策与证据全文写在 spa.go serveIndexHTML 的函数注释里，本锁负责让决策不被悄悄翻回。
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Host = "langcross.example.com"
	s.serveIndexHTML(rec, r, filepath.Join(s.Dist, "index.html"))
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("⑤ 无品牌站缓存口径不符（本批决定不引入 public max-age，理由见 spa.go 注释）: %q", got)
	}
	base := len(rec.Body.String())

	// B) 配了品牌 + 存量 1.2 MB dataURI：仍 no-cache，且 HTML 体积不许跟着图长
	if err := s.setPlatformBranding(map[string]string{"brand_name": "能言", "brand_home_bg": big}); err != nil {
		t.Fatalf("setPlatformBranding 失败: %v", err)
	}
	rec2 := httptest.NewRecorder()
	s.serveIndexHTML(rec2, r, filepath.Join(s.Dist, "index.html"))
	if got := rec2.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("⑤ 品牌站必须 no-cache（后台改完要立刻见效）: %q", got)
	}
	html := rec2.Body.String()
	if strings.Contains(html, "data:image") {
		t.Fatal("⑤ 首屏 HTML 里仍有 data:image ⇒ F-46 复发")
	}
	if !strings.Contains(html, brandAssetPrefix) {
		t.Fatalf("⑤ 首屏没注入静态件 URL: %s", html[:min(len(html), 300)])
	}
	// ★ 修复文档点名的那条锁：首页 HTML < 30 KB（口径=解码后字节，冒烟脚本用 decodedBodySize）。
	//   配套一条"病根尺寸"对照：注入前的字段值本身有 ~1.6 MB，若按老写法整串进 HTML，
	//   HTML 必然 ≥ 该值 ⇒ 只看"< 30 KB"在 2.6 KB 夹具上是空锁，加上这条才是"同一尺子的两侧"。
	if len(html) >= 30*1024 {
		t.Fatalf("⑤ 首屏 HTML %d B ≥ 30 KB 上限（存量 dataURI 漏进来了）", len(html))
	}
	if len(big) < 100_000 {
		t.Fatalf("⑤ 夹具失真：dataURI 只有 %d 字符，撑不起病根对照", len(big))
	}
	if len(html) >= len(big) {
		t.Fatalf("⑤ HTML(%d B) 没小于注入值(%d 字符) ⇒ 又整串塞进去了", len(html), len(big))
	}
	// 增量锁：品牌注入相对无品牌外壳只多一截 URL，不该多出任何图像量级
	if delta := len(html) - base; delta > 4096 {
		t.Fatalf("⑤ 品牌注入让 HTML 涨了 %d B（外壳 %d B → 品牌 %d B）", delta, base, len(html))
	}
}

// ---------------------------------------------------------------------------
// ⑥ 直出面
// ---------------------------------------------------------------------------

// brandPut 在静态件目录里放一份文件（模拟已落件）。
func brandPut(t *testing.T, s *Server, name string, b []byte) {
	t.Helper()
	dir := s.brandAssetDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
		t.Fatalf("写件失败: %v", err)
	}
}

func TestBrandAssetRouteServing(t *testing.T) {
	s := brandEnv(t)
	name := "t5-logo-deadbeef.png"
	brandPut(t, s, name, brandPNG)

	rec := httptest.NewRecorder()
	s.handleBrandAsset(rec, httptest.NewRequest(http.MethodGet, brandAssetPrefix+name, nil))
	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("⑥ 正常件应 200，实际 %d body=%q", res.StatusCode, rec.Body.String())
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("⑥ Content-Type 应为 image/png，实际 %q", ct)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("⑥ 内容寻址件应 immutable，实际 %q", cc)
	}
	if sn := res.Header.Get("X-Content-Type-Options"); sn != "nosniff" {
		t.Fatalf("⑥ 应带 nosniff，实际 %q", sn)
	}
	if !bytes.Equal(rec.Body.Bytes(), brandPNG) {
		t.Fatal("⑥ 出参字节与盘上不一致")
	}

	// HEAD：只要头不要体（冒烟探针用），且 Content-Length 如实
	hrec := httptest.NewRecorder()
	s.handleBrandAsset(hrec, httptest.NewRequest(http.MethodHead, brandAssetPrefix+name, nil))
	if hrec.Result().StatusCode != http.StatusOK || hrec.Body.Len() != 0 {
		t.Fatalf("⑥ HEAD 应 200 且零体，实际 %d / %d B", hrec.Result().StatusCode, hrec.Body.Len())
	}

	// 负向：缺件 / 非白名单后缀 / 含目录段 / 点开头的隐藏件
	for _, p := range []string{
		brandAssetPrefix + "t5-logo-missing.png",
		brandAssetPrefix + "t5-logo-x.html",
		brandAssetPrefix + "t5-logo-x.svg",
		brandAssetPrefix + "sub/" + name,
		brandAssetPrefix + ".hidden.png",
	} {
		r := httptest.NewRecorder()
		s.handleBrandAsset(r, httptest.NewRequest(http.MethodGet, p, nil))
		body := r.Body.String()
		if r.Result().StatusCode != http.StatusNotFound {
			t.Fatalf("⑥ %s 应 404，实际 %d body=%q", p, r.Result().StatusCode, body)
		}
		// ★ 404 必须是 JSON 错误体，**不得**是 HTML（漏给 SPA 兜底就是「状态码会说谎」复发）
		if !strings.Contains(body, "\"success\":false") || strings.Contains(strings.ToLower(body), "<html") {
			t.Fatalf("⑥ %s 的 404 形态不是 JSON 错误体: %q", p, body)
		}
	}

	// 盘上字节被换成非图内容（人工挪件/磁盘写坏）⇒ 500，绝不把坏字节当图发
	brandPut(t, s, "t5-logo-broken.png", []byte("<!DOCTYPE html>oops"))
	rrec := httptest.NewRecorder()
	s.handleBrandAsset(rrec, httptest.NewRequest(http.MethodGet, brandAssetPrefix+"t5-logo-broken.png", nil))
	if rrec.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("⑥ 坏件应 500，实际 %d", rrec.Result().StatusCode)
	}

	// POST 不放行（静态件只读）
	prec := httptest.NewRecorder()
	s.handleBrandAsset(prec, httptest.NewRequest(http.MethodPost, brandAssetPrefix+name, nil))
	if prec.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("⑥ POST 应 405，实际 %d", prec.Result().StatusCode)
	}
}

// ---------------------------------------------------------------------------
// ⑦ 真接线（生产 routes() 的 mux，不是测试自建 mux）
// ---------------------------------------------------------------------------

func TestBrandAssetRegisteredOnRealRoutes(t *testing.T) {
	s := brandEnv(t)
	s.Dist = brandDist(t)
	name := "t5-logo-wired.png"
	brandPut(t, s, name, brandPNG)
	s.mux = newRouteMux()
	s.routes()

	probe := func(path string) (int, string, string) {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		res := rec.Result()
		return res.StatusCode, res.Header.Get("Content-Type"), rec.Body.String()
	}
	st, ct, body := probe(brandAssetPrefix + name)
	if st != http.StatusOK || ct != "image/png" || !bytes.HasPrefix([]byte(body), []byte("\x89PNG")) {
		t.Fatalf("⑦ 真 routes() 上 /brand/ 未落到静态件处理方: status=%d ct=%q 前 60B=%q", st, ct, body[:min(len(body), 60)])
	}
	// 缺件同样不许漏给 "/" 兜底（兜底会回 200 整页 HTML，正是 F-69 的形态）
	if st2, ct2, body2 := probe(brandAssetPrefix + "t5-logo-none.png"); st2 == http.StatusOK || strings.Contains(ct2, "html") {
		t.Fatalf("⑦ 缺件漏给了 SPA 兜底: status=%d ct=%q 前 60B=%q", st2, ct2, body2[:min(len(body2), 60)])
	}
}

// ---------------------------------------------------------------------------
// ⑧ 复用前的自愈门：同名同尺寸的坏件不许被当成"已就位"复用
//   （★ 本锁由 deploy/smoke_brand_homepage.sh --selftest 第一次跑红时暴露：
//    自检把盘上的品牌件**改写成同字节数的 HTML 文本**，读侧只看 os.Stat 的尺寸就判定可复用，
//    于是继续把 URL 发给前端、直出面又把坏字节当图发出去 —— 自愈特性漏了"内容被写坏"这一态。
//    修法：尺寸门之外再加一道 12 字节魔数门（brandHeadMagicOK），不符即按缺件重写。）
// ---------------------------------------------------------------------------

func TestBrandAssetSelfHealsCorruptedFileOnDisk(t *testing.T) {
	s := brandEnv(t)
	ctx := t.Context()
	uri := brandDataURI("image/png", brandPNG)
	url1 := s.brandImageURL(ctx, "t9-logo", uri)
	if !strings.HasPrefix(url1, brandAssetPrefix) {
		t.Fatalf("⑧ 首次落件应返回静态件地址: %q", url1)
	}
	p := filepath.Join(s.brandAssetDir(), strings.TrimPrefix(url1, brandAssetPrefix))
	good, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("⑧ 读落件失败: %v", err)
	}
	// 造一个**同尺寸**的坏件：只换内容不换长度，专门绕开"尺寸相符即可复用"这个弱判据。
	bad := bytes.Repeat([]byte{'x'}, len(good))
	if len(bad) != len(good) {
		t.Fatalf("⑧ 夹具自身失真（尺寸没对齐）: %d vs %d", len(bad), len(good))
	}
	if err := os.WriteFile(p, bad, 0o644); err != nil {
		t.Fatalf("⑧ 写坏件失败: %v", err)
	}
	url2 := s.brandImageURL(ctx, "t9-logo", uri)
	if url2 != url1 {
		t.Fatalf("⑧ 地址应保持稳定（内容寻址同名）: %q vs %q", url2, url1)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("⑧ 复检读件失败: %v", err)
	}
	if !bytes.Equal(after, good) {
		t.Fatalf("⑧ 坏件未被自愈：盘上仍是 %d 字节的 %q（期望原字节 %d B）",
			len(after), after[:min(len(after), 24)], len(good))
	}
	if !bytes.HasPrefix(after, []byte("\x89PNG")) {
		t.Fatal("⑧ 自愈后的字节又不是 PNG 了")
	}
}
