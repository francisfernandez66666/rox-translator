// ============ sales_facts_scope_gate_test.go · 职责说明 ============
// 四期 W1（施工图 §4.1.3-①②④、§4.1.6 的 F1）：销售事实集**作用域解析表**的行为锁。
//
// 证什么：sales_facts_scope.go 文件头那张「八行三档表」逐行等值——开关未全开、登录产品页、
// 匿名产品页、单产品定档、多产品歧义、零产品、匿名无作用域、产品不存在/非 live/跨租户，
// 每一行的 (TenantID, ProductID, Reason) 都要与表一致。
//
// 反证（M0268）：删掉「产品数 >= 2 ⇒ ambiguous」那一支（返回 single_product/空串），
// 多产品腿当场红——它是「三期串台」那行代码的正面靶子。
//
// ★ 夹具（newW1Fixture）被 sales_facts_anon_gate_test.go 复用；两个文件同属一个锁族，
// 任一文件删掉都会让 F1–F11 的射程断一半（gateguard C1 按文件登记变异体）。
// ================================================
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// w1Fixture W1 作用域/注入/归因三条腿共用的夹具。
//
// 租户布局（每个数字都对应表里的一行，改这里必须同步改断言）：
//
//	租户 7：产品 A(live) / B(live) / D(draft)　⇒ 多产品（ambiguous 的靶子）、跨租户（F6 的靶子）
//	租户 8：产品 C(live)　　　　　　　　　　⇒ 跨租户登录用户（请求 A 的产品页）
//	租户 9：产品 E(live) 单产品　　　　　　　⇒ single_product 自动定档
//	租户 10：零产品　　　　　　　　　　　　　⇒ denied（且 TenantID 已被赋成 10，见解析器注释）
type w1Fixture struct {
	srv *Server
	up  *salesFactsUpstream
	// 产品行（TenantCode/PageCode 由 store 现签，断言不许写死短码）
	A, B, C, D, E store.Product
	// 各租户登录用户 JWT（租户 10 用于「零产品」那一档）
	tok7, tok8, tok9, tok10 string
}

// newW1Fixture 建文件库主服务 + 五件产品 + 四个登录用户 + 两把开关全开。
//
// ★ 用文件库不用 :memory:：连接池下 :memory: 每连接一库，store.New 的迁移与后续查询会落到
// 两个不同的库上（AGENTS §25-b 的假红形态）。★ 方言必须自钉（AGENTS §一·4）：
// config.Default() 读 DB_DRIVER env 且副作用写全局 config.C，run_uat 的 PG 模式会泄漏给本包。
func newW1Fixture(t *testing.T) *w1Fixture {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	// 配了 TTL 后「写完下一个请求即读到」不成立（UAT 夹具同口径）：0＝现查档。
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "0")

	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "w1.db"))
	if err != nil {
		t.Fatalf("打开文件库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}

	fx := &w1Fixture{srv: &Server{Store: st}}

	// 假 assist 上游（F2/F10 走转发层；F1 只直调解析器，不经过它）。
	fx.up = &salesFactsUpstream{respBody: salesFactsAttrBody}
	upSrv := httptest.NewServer(fx.up)
	t.Cleanup(upSrv.Close)
	t.Setenv("ASSIST_BASE_URL", upSrv.URL)
	t.Setenv("ASSIST_PROFILE_KEY", "test-w1-key")

	// 产品：A 必须先建（同租户的 tenant_code 由它签发，B/D 复用同一段）。
	fx.A = mustCreateProduct(t, st, 7, "产品 A（公开页主靶子）", store.ProductStatusLive)
	fx.B = mustCreateProduct(t, st, 7, "产品 B（同租户第二个产品）", store.ProductStatusLive)
	fx.D = mustCreateProduct(t, st, 7, "产品 D（draft，F7 的靶子）", store.ProductStatusDraft)
	fx.C = mustCreateProduct(t, st, 8, "产品 C（他租户）", store.ProductStatusLive)
	fx.E = mustCreateProduct(t, st, 9, "产品 E（租户唯一产品）", store.ProductStatusLive)

	// 登录用户（租户 10 零产品那一档也要一个身份）。
	fx.tok7 = mustUserToken(t, st, 7, "w1u7")
	fx.tok8 = mustUserToken(t, st, 8, "w1u8")
	fx.tok9 = mustUserToken(t, st, 9, "w1u9")
	fx.tok10 = mustUserToken(t, st, 10, "w1u10")

	// 两把开关缺省关，夹具显式打开（三档表第一行的"未全开"由用例自己关）。
	setW1Gate(t, st, true)
	return fx
}

// mustCreateProduct 建一个产品并落到目标状态；返回产品行（含现签的两段短码）。
func mustCreateProduct(t *testing.T, st *store.Store, tid int64, name, status string) store.Product {
	t.Helper()
	p, err := st.CreateProduct(tid, store.ProductInput{Name: name})
	if err != nil {
		t.Fatalf("建产品失败（tid=%d name=%q）: %v", tid, name, err)
	}
	if status != store.ProductStatusDraft {
		if err := st.SetProductStatus(tid, p.ID, status); err != nil {
			t.Fatalf("置产品状态失败（tid=%d id=%s status=%s）: %v", tid, p.ID, status, err)
		}
		p.Status = status
	}
	if p.TenantCode == "" || p.PageCode == "" {
		t.Fatalf("产品两段短码未签发：%+v", p)
	}
	return p
}

// mustUserToken 建一个普通用户并签一枚 1 小时 JWT。
func mustUserToken(t *testing.T, st *store.Store, tid int64, username string) string {
	t.Helper()
	u, err := st.CreateUser(tid, username, "hash-w1-"+username, username, "user", 0, 0)
	if err != nil || u == nil {
		t.Fatalf("建用户失败（tid=%d name=%q）: %v", tid, username, err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败（tid=%d）: %v", tid, err)
	}
	return tok
}

// setW1Gate 写两把开关（on=false 时两把都写 0：只开一把属 half，不是全开）。
func setW1Gate(t *testing.T, st *store.Store, on bool) {
	t.Helper()
	v := "0"
	if on {
		v = "1"
	}
	if err := st.SetConfig("sales_facts_injection_enabled", v); err != nil {
		t.Fatalf("写开关注入档失败: %v", err)
	}
	if err := st.SetConfig("sales_claim_guard_enabled", v); err != nil {
		t.Fatalf("写开关守卫档失败: %v", err)
	}
}

// w1PagePath 产品行的公开页路径（/p/{tenant_code}/{page_code}，与前端 pageHeader 同形）。
func w1PagePath(p store.Product) string {
	return "/p/" + p.TenantCode + "/" + p.PageCode
}

// TestSalesFactsScopeTable F1：八行三档表逐行等值（末行展开成三种非法形态各判一次）。
func TestSalesFactsScopeTable(t *testing.T) {
	fx := newW1Fixture(t)

	type scopeCase struct {
		name     string
		gate     bool   // 两把开关是否全开
		token    string // "" = 匿名
		page     string // 客户端自报的页面上下文
		wantTid  int64
		wantProd string // 期望的 ProductID（"" = 不可注入）
		wantWord string
	}

	cases := []scopeCase{
		{
			// 表第 1 行：开关未全开 ⇒ 连解析都不算一次判定（disabled，且不计入 scope_*）。
			name: "开关未全开_空作用域", gate: false,
			wantTid: 0, wantProd: "", wantWord: salesScopeDisabled,
		},
		{
			// 表第 2 行：登录 + 产品页 + live + 同租户。
			name: "登录_产品页A", gate: true, token: fx.tok7, page: w1PagePath(fx.A),
			wantTid: 7, wantProd: fx.A.ID, wantWord: salesScopeProductPage,
		},
		{
			// 表第 3 行：匿名 + 同一页 ⇒ 租户取产品自身。
			name: "匿名_产品页A", gate: true, page: w1PagePath(fx.A),
			wantTid: 7, wantProd: fx.A.ID, wantWord: salesScopeProductPageAnon,
		},
		{
			// 表第 4 行：登录 + 无作用域 + 唯一产品 ⇒ 自动定档。
			name: "登录_无页面_单产品租户9", gate: true, token: fx.tok9,
			wantTid: 9, wantProd: fx.E.ID, wantWord: salesScopeSingleProduct,
		},
		{
			// 表第 5 行：登录 + 无作用域 + 多产品 ⇒ fail-closed（M0268 的靶子）。
			name: "登录_无页面_多产品租户7", gate: true, token: fx.tok7,
			wantTid: 7, wantProd: "", wantWord: salesScopeAmbiguous,
		},
		{
			// 表第 6 行：登录 + 无作用域 + 零产品（TenantID 已被赋成 tid，见解析器注释）。
			name: "登录_无页面_零产品租户10", gate: true, token: fx.tok10,
			wantTid: 10, wantProd: "", wantWord: salesScopeDenied,
		},
		{
			// 表第 7 行：匿名 + 无作用域。
			name: "匿名_无页面", gate: true,
			wantTid: 0, wantProd: "", wantWord: salesScopeDenied,
		},
		{
			// 表第 8 行形态 ①：产品码不存在（两段码拼不出任何行）。
			name: "登录_产品页不存在", gate: true, token: fx.tok7, page: "/p/zzzzzzzz/yyyyyyyy",
			wantTid: 0, wantProd: "", wantWord: salesScopeDenied,
		},
		{
			// 表第 8 行形态 ②：产品存在但非 live（F7 的解析器侧）。
			name: "登录_产品页D非live", gate: true, token: fx.tok7, page: w1PagePath(fx.D),
			wantTid: 0, wantProd: "", wantWord: salesScopeDenied,
		},
		{
			// 表第 8 行形态 ③：跨租户（F6 的解析器侧；真边界是登录态租户一致性）。
			name: "登录租户8_请求租户7产品页A", gate: true, token: fx.tok8, page: w1PagePath(fx.A),
			wantTid: 0, wantProd: "", wantWord: salesScopeDenied,
		},
	}
	if len(cases) < 8 {
		t.Fatalf("射程自证：表驱动只剩 %d 行（<8）——三档表被裁，本锁已失明", len(cases))
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setW1Gate(t, fx.srv.Store, c.gate)
			r := httptest.NewRequest(http.MethodGet, "/assist-api/api/assist/greeting", nil)
			r.Header.Set("X-App-Lang", "en")
			if c.token != "" {
				r.Header.Set("Authorization", "Bearer "+c.token)
			}
			sc := fx.srv.resolveSalesFactsScope(r, c.page)
			if sc.TenantID != c.wantTid || sc.ProductID != c.wantProd || sc.Reason != c.wantWord {
				t.Fatalf("作用域与三档表不一致：实得 (tid=%d prod=%q reason=%q)，期望 (tid=%d prod=%q reason=%q)",
					sc.TenantID, sc.ProductID, sc.Reason, c.wantTid, c.wantProd, c.wantWord)
			}
			if sc.Lang != "en" {
				t.Fatalf("作答语言未按请求现读：%q（X-App-Lang: en）", sc.Lang)
			}
			wantInjectable := c.wantTid > 0 && c.wantProd != ""
			if sc.injectable() != wantInjectable {
				t.Fatalf("injectable()=%v 与 (tid,%q) 不一致：租户与产品必须同时有效才算可注入",
					sc.injectable(), c.wantProd)
			}
		})
	}

	// 表跑完把夹具复位：后续用例（同包其他文件串跑时）看到的是"两把全开"的缺省态。
	setW1Gate(t, fx.srv.Store, true)
}
