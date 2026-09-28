// ============ site_flags_test.go · 职责说明 ============
// 〇-Z「站点门面开关」首页直出侧单测（internal/api/site_flags.go）：
//
//	A 默认＝开放主页，且**开放时首页 HTML 一个字节都不许多**——现网主站首页 2,591 B 是
//	  《部署指南》§十 与 deploy/smoke_brand_homepage.sh 钉死的判据，无脑注入恒真标记
//	  会把它顶翻，所以这里用等值锁把"开＝与未注入完全相等"钉住，而不是只判不含关键字。
//	B 平台策略 landing_enabled=false ⇒ 注入 window.__SITE_FLAGS__={"landing":false}，位置在 </head> 前。
//	C 优先序 env LANDING_DISABLED > 库配置 > 默认（AGENTS §一·3），两个方向都能压过库里的表态；
//	  非法 env 值视为"未表态"，回落库配置（防手滑打错一个字母就把官网首页关没）。
//	D 门面只读**平台层**策略：租户级 ops_policy 里的 front 不参与（射程口径，见 policy.go）。
//	E 健壮性：Store 为 nil、请求体为空都不能 panic，且回落"开放主页"（少关比误关安全）。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4）：config.Default() 副作用写全局 config.C，
//
//	run_uat 的 PG 模式会把方言泄漏给同包内存 SQLite 用例。
//	运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestSiteFlags
//
// =============================================
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

// siteFlagsStore 建一个内存 SQLite 的 Store（只用到 system_config 读写）。
// 参数 t: 测试上下文。返回: 已迁移完整 schema 的 Store。
func siteFlagsStore(t *testing.T) *store.Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return st
}

// siteFlagsSetPolicy 把平台 ops_policy 写成给定 JSON（模拟超管在管理台保存一次策略）。
// 参数 t: 测试上下文；st: Store；raw: 策略原文。
func siteFlagsSetPolicy(t *testing.T, st *store.Store, raw string) {
	t.Helper()
	if err := st.SetConfig("ops_policy", raw); err != nil {
		t.Fatalf("写入 ops_policy 失败: %v", err)
	}
}

const siteFlagsShell = `<!DOCTYPE html><html><head><title>t</title></head><body><div id="root"></div></body></html>`

// TestSiteFlagsLandingOpenKeepsBytesIdentical A/E：默认开放 ⇒ 载荷为 nil、注入函数原样返回。
func TestSiteFlagsLandingOpenKeepsBytesIdentical(t *testing.T) {
	t.Setenv("LANDING_DISABLED", "")
	st := siteFlagsStore(t)
	s := &Server{Store: st}
	r := httptest.NewRequest("GET", "/", nil)

	if !s.siteLandingEnabled() {
		t.Fatal("未做任何配置时主页应为开放（默认档被改动会误关主站）")
	}
	if got := s.siteFlagsPayload(r); got != nil {
		t.Fatalf("主页开放时不应产生注入载荷，实际=%v", got)
	}
	// 等值锁：开放态下首页 HTML 必须与"本特性不存在"逐字节相同
	before := siteFlagsShell
	after := injectSiteFlagsScript(s.siteFlagsPayload(r), before)
	if before != after {
		t.Fatalf("主页开放却改变了首页字节：\n  前=%d B\n  后=%d B\n  后=%s", len(before), len(after), after)
	}
	// 负向：任何情况下都不该往页面里写 "landing":true（那是把默认档当配置外泄）
	if strings.Contains(after, "landing") || strings.Contains(after, "__SITE_FLAGS__") {
		t.Fatalf("开放态首页混入了门面标记：%s", after)
	}
	// E：Store 为 nil 也不 panic，且回落开放（宁少关不误关）
	bs := &Server{}
	if !bs.siteLandingEnabled() {
		t.Fatal("Store 为 nil 时应回落『开放主页』")
	}
	if bs.siteFlagsPayload(r) != nil {
		t.Fatal("Store 为 nil 时不应注入")
	}
	if bs.siteFlagsPayload(nil) != nil {
		t.Fatal("请求为 nil 时不应注入（也不应 panic）")
	}
}

// TestSiteFlagsLandingDisabledInjects B：平台策略关闭主页 ⇒ 注入落在 </head> 前且只有关闭态一个形态。
func TestSiteFlagsLandingDisabledInjects(t *testing.T) {
	t.Setenv("LANDING_DISABLED", "")
	st := siteFlagsStore(t)
	s := &Server{Store: st}
	siteFlagsSetPolicy(t, st, `{"front":{"landing_enabled":false}}`)

	if s.siteLandingEnabled() {
		t.Fatal("平台策略 landing_enabled=false 未生效")
	}
	r := httptest.NewRequest("GET", "/", nil)
	out := injectSiteFlagsScript(s.siteFlagsPayload(r), siteFlagsShell)
	if out == siteFlagsShell {
		t.Fatal("主页已关闭却没注入标记，前端会照样渲染落地页")
	}
	if !strings.Contains(out, `window.__SITE_FLAGS__={"landing":false}`) {
		t.Fatalf("注入内容不符：\n%s", out)
	}
	iHead := strings.Index(out, "</head>")
	iTag := strings.Index(out, `<script id="__site_flags__">`)
	if iTag < 0 || iHead < 0 || iTag > iHead {
		t.Fatalf("标记必须在 </head> 之前（前端首屏分流要同步读到）：tag=%d head=%d", iTag, iHead)
	}
	// 原始壳内容不能被破坏（只增不改）
	if !strings.Contains(out, `<div id="root"></div>`) {
		t.Fatal("注入破坏了原有 HTML 结构")
	}
}

// TestSiteFlagsEnvPrecedence C：环境变量 > 库配置，含"强制开放"的反向覆盖与非法值回落。
func TestSiteFlagsEnvPrecedence(t *testing.T) {
	st := siteFlagsStore(t)
	s := &Server{Store: st}

	// env=1 覆盖"库里开放"
	siteFlagsSetPolicy(t, st, `{"front":{"landing_enabled":true}}`)
	for _, v := range []string{"1", "true", "YES", " on "} {
		t.Setenv("LANDING_DISABLED", v)
		if s.siteLandingEnabled() {
			t.Fatalf("LANDING_DISABLED=%q 应关闭主页", v)
		}
	}
	// env=0 覆盖"库里关闭"（运维急救方向：库里误关仍能不开 DB 写就恢复）
	siteFlagsSetPolicy(t, st, `{"front":{"landing_enabled":false}}`)
	for _, v := range []string{"0", "false", "NO", "off"} {
		t.Setenv("LANDING_DISABLED", v)
		if !s.siteLandingEnabled() {
			t.Fatalf("LANDING_DISABLED=%q 应强制保留主页", v)
		}
	}
	// 非法值＝未表态 ⇒ 回落库配置（此处库里是 false）
	t.Setenv("LANDING_DISABLED", "ture")
	if s.siteLandingEnabled() {
		t.Fatal("非法 env 值被当成表态，覆盖了库配置")
	}
	// 未设置＝未表态 ⇒ 同样回落库配置
	t.Setenv("LANDING_DISABLED", "")
	if s.siteLandingEnabled() {
		t.Fatal("env 未设置时应回落库配置（库里 false ⇒ 关闭）")
	}
}

// TestSiteFlagsIgnoresTenantScope D：门面无租户入参——租户级策略原文不参与。
// 这里用"平台层未表态 + 只有一个租户配置存在"的场景验证读侧只看 platform：
// 若日后有人把门面接成 effectivePolicy(tid)，本用例不会红（因为默认仍开放），
// 所以真正的射程锁是**签名里没有 tid**＋策略层的 ValidateWindowOverrides 拦截，本用例作为行为侧的补充。
func TestSiteFlagsIgnoresTenantScope(t *testing.T) {
	t.Setenv("LANDING_DISABLED", "")
	st := siteFlagsStore(t)
	s := &Server{Store: st}
	siteFlagsSetPolicy(t, st, `{"task":{"enabled":false}}`) // 平台层不含门面
	r := httptest.NewRequest("GET", "/", nil)
	if s.siteFlagsPayload(r) != nil {
		t.Fatal("平台层未表态时不应注入（其它因子无关）")
	}
}

// TestSiteFlagsServedIndexHTML 接线锁：serveIndexHTML 真的调了注入（只测纯函数会漏掉"忘了接线"这类缺陷，
// 而现网演示站的表现正是"页面上没有标记 ⇒ 前端照旧渲染落地页"）。
// 相 A 关主页 ⇒ 出口 HTML 含标记；相 B 开放 ⇒ 出口 HTML 与未注入时逐字节等长（不许多一个字节）。
func TestSiteFlagsServedIndexHTML(t *testing.T) {
	t.Setenv("LANDING_DISABLED", "")
	st := siteFlagsStore(t)
	dir := t.TempDir()
	shell := `<!DOCTYPE html><html><head><title>s</title></head><body><div id="root"></div></body></html>`
	if err := writeFileForSiteFlags(dir+"/index.html", shell); err != nil {
		t.Fatalf("写入临时 index.html 失败: %v", err)
	}
	s := &Server{Store: st, Dist: dir}
	r := httptest.NewRequest("GET", "/", nil)

	// 相 A：策略关闭主页
	siteFlagsSetPolicy(t, st, `{"front":{"landing_enabled":false}}`)
	got := serveIndexCapture(t, s, r, dir+"/index.html")
	if !strings.Contains(got, `"landing":false`) {
		t.Fatalf("演示站形态下出口 HTML 缺门面标记：\n%s", got)
	}
	if !strings.Contains(got, "__BRANDING__") {
		t.Fatal("品牌注入半边链被门面注入顶掉（两条注入必须共存）")
	}
	// 相 B：开放态必须回到"只含品牌注入"的形态，且长度与只跑品牌注入的结果相等
	siteFlagsSetPolicy(t, st, `{}`)
	open := serveIndexCapture(t, s, r, dir+"/index.html")
	if strings.Contains(open, "__SITE_FLAGS__") {
		t.Fatalf("主站形态下首页多出门面标记（会顶翻 2,591 B 判据）：\n%s", open)
	}
	wantOnlyBranding := injectBrandingScript(s.brandingPayload(r), shell)
	if len(open) != len(wantOnlyBranding) {
		t.Fatalf("开放态首页长度 %d ≠ 仅品牌注入 %d（说明注入链在无配置时也落了字节）", len(open), len(wantOnlyBranding))
	}
}

// writeFileForSiteFlags 落一个临时前端产物（serveIndexHTML 只读磁盘上的 index.html）。
// 参数 path: 目标路径；body: 文件内容。返回: 写入错误。
func writeFileForSiteFlags(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
}

// serveIndexCapture 直接驱动 serveIndexHTML 并取回出口 HTML（绕开 mux，专注注入链本身）。
// 参数 t: 测试上下文；s: 服务实例；r: 请求；path: index.html 路径。返回: 响应体字符串。
func serveIndexCapture(t *testing.T, s *Server, r *http.Request, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.serveIndexHTML(rec, r, path)
	return rec.Body.String()
}

// TestSiteFlagsEnvVisibleInAdminPolicy 读写同源锁：env 关闭主页时，管理台读到的有效策略
// 必须同样是 false。否则界面上开关显示"展示主页"、实际进不去，运维会照假现值点保存，
// 把与真实态相反的档位写进平台策略（口径同 F-75 品牌页那条红线）。
func TestSiteFlagsEnvVisibleInAdminPolicy(t *testing.T) {
	st := siteFlagsStore(t)
	s := &Server{Store: st}
	siteFlagsSetPolicy(t, st, `{}`) // 库里完全没表态

	// 未设 env：管理台与首页读侧都应为开放
	t.Setenv("LANDING_DISABLED", "")
	if !s.opsBaseEffective(0).Front.LandingEnabled {
		t.Fatal("默认档下管理台有效策略应为展示主页")
	}
	// 设 env=1：两侧必须同时翻转（这是本锁的全部意义——只改首页注入不改回显就会失同步）
	t.Setenv("LANDING_DISABLED", "1")
	if s.siteLandingEnabled() {
		t.Fatal("env=1 时首页读侧应关闭主页")
	}
	if s.opsBaseEffective(0).Front.LandingEnabled {
		t.Fatal("env=1 时管理台有效策略仍显示展示主页（读写不同源）")
	}
}
