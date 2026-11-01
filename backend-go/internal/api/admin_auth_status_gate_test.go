// ============ admin_auth_status_gate_test.go · 职责说明 ============
// DEF-P3-05（2026-11-01）：/api/admin/ 全路由的「未登录」状态码语义一致性闸门。
//
// 立这条闸的理由不是"这三条路由错了"（那三条已在 slo.go / referral.go 就地分流），
// 而是这一族缺陷的**复发方式**：handler 里写 `if err != nil || !auth.IsSuperAdmin(u)`，
// 于是「未登录」和「已登录但角色不足」被并成同一个 403。危害有两层——
//
//	① 前端与 SDK 只在 401 走重登录链路（见 writeAuthzError 的口径），403 会让用户停在
//	   「没权限」的困惑上而不是被引导回登录；
//	② 读代码的人看不出这条路由到底认不认身份，排障时把 403 当成"权限配错"去查租户角色。
//
// 判据取向（三条刻意为之）：
//
//	· 走**真实路由注册表**（routeMux 登记每一个注册串）逐条打 GET，不 grep 源码字符串
//	  ——grep 只能证明"写对了的样子"，证明不了"注册上了、也真会 401"；
//	· **现读现判**：允许状态集合按语义判（401=未登录、405=方法先判），
//	  **禁止把条数写进断言**（路由数会随批次漂移：295 是唯一字面量 pattern 数，
//	  含动态注册与运行期展开就不是；写死条数的锁下一次合法新增路由即恒红，
//	  而这种锁的归宿是被摘掉，比红灯更坏）；
//	· 配**射程自证**：探针集合非空且确实拿到过 401，否则整张表为空会让"没有违规"结构性恒真。
package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"

	"translator/internal/auth"
)

// adminAuthStatusMethodMismatchAllow 是"GET 打过去必然不是鉴权码"的例外清单：
// 注册时带了非 GET 的方法前缀（ServeMux 先判方法再进 handler），405 属**方法语义**不是鉴权语义。
// 每条必须写理由；空理由与"只加 key 不写原因"都判红（同 model_for_stage 的 allowedBypass 口径）。
// 现状为空表。**留这张表不是预备放行，而是逼"将来出现 405 时说明理由"**：
// 一旦有路由让未登录请求拿到 405，要么改鉴权顺序（首选，见 watchdog.go 的订阅扫描那处修法），
// 要么在此登记理由——空理由／只加 key 不写原因都判红。
var adminAuthStatusMethodMismatchAllow = map[string]string{}

// TestAdminRoutesUnauthorizedMustBe401 一致性锁：未登录打任意 /api/admin/ 路由，
// 只许回 401（或方法先判的 405），**不得出现"未登录却回 403"**。
func TestAdminRoutesUnauthorizedMustBe401(t *testing.T) {
	srv, _ := adminGateServer(t)
	probed, offenders, got401 := 0, []string{}, 0
	for _, meta := range srv.mux.allRoutes() {
		path := meta.Pattern
		if i := strings.IndexByte(path, ' '); i > 0 { // 剥掉方法前缀，只按路径打 GET
			path = path[i+1:]
		}
		if !strings.HasPrefix(path, "/api/admin/") {
			continue
		}
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil)) // 无 Authorization ⇒ 未登录
		probed++
		switch w.Code {
		case http.StatusUnauthorized:
			got401++
		case http.StatusMethodNotAllowed:
			if reason, ok := adminAuthStatusMethodMismatchAllow[meta.Pattern]; !ok || strings.TrimSpace(reason) == "" {
				offenders = append(offenders, meta.Pattern+" ⇒ 405 但不在例外清单（或清单里没写理由）")
			}
		default:
			// 403 是本闸要抓的本体；其余（200/400/404/500…）说明鉴权**之前**就有别的分支，
			// 或压根没鉴权（那比 403 更坏），一律算违规，逼人来解释而不是静默放行。
			offenders = append(offenders, meta.Pattern+" ⇒ "+http.StatusText(w.Code))
		}
	}
	if probed < 100 {
		t.Fatalf("射程自证失败：只探到 %d 条 /api/admin/ 路由（历史基线 122 条字面量 pattern）⇒ 注册表读空/装配退化，本闸门已失明", probed)
	}
	if got401 == 0 {
		t.Fatalf("正向对照失败：%d 条探针里一条 401 都没有 ⇒ 分流出口没走到，绿灯是假的", probed)
	}
	if len(offenders) > 0 {
		t.Fatalf("★ 未登录状态码语义不一致（%d/%d）：\n%s\n⇒ 未登录必须 401（角色不足才 403）；方法先判的请登记进 adminAuthStatusMethodMismatchAllow 并写理由",
			len(offenders), probed, strings.Join(offenders, "\n"))
	}
	t.Logf("OK /api/admin/ 探针 %d 条：401=%d，403=0，方法先判例外 %d 条", probed, got401, len(adminAuthStatusMethodMismatchAllow))
}

// TestAdminRoutesThreeFixedSitesAre401 三条已修路由的**点名**判据（DEF-P3-05 本体）。
//
// 为什么单独钉：上面那条是全集锁，"三条都修好"这件事在全集里只占三格——
// 有人把分流改回去而同时给那三条登记了 405 例外，全集锁照样绿。这一条堵的就是"绕道放行"。
func TestAdminRoutesThreeFixedSitesAre401(t *testing.T) {
	srv, _ := adminGateServer(t)
	for _, p := range []string{"/api/admin/ops/slo", "/api/admin/ops/routes", "/api/admin/referral/config"} {
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s 未登录应回 401，实得 %d body=%s ⇒ 分流腿被改回 `err != nil || !IsSuperAdmin` 那一族了",
				p, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// TestAdminAuthzErrorStillForbiddenForInsufficientRole 反向对照：分流不是"一律 401"。
//
// 拿一个**已登录但等级不足**的用户（普通会员）打超管面，必须仍回 403。
// 缺这一条对照时，"把 writeAuthzError 换成一律 401"这种更偷懒的改法会全绿——
// 而那是把"没权限"说成"没登录"，客户端会拿去反复重登录（同 §一·8 状态码诚实化那一族）。
func TestAdminAuthzErrorStillForbiddenForInsufficientRole(t *testing.T) {
	srv, tok := adminGateServer(t) // tok = 普通会员（iam.RoleUser），对 /api/admin/ 等级不足
	for _, p := range []string{"/api/admin/ops/slo", "/api/admin/ops/routes", "/api/admin/referral/config"} {
		r := httptest.NewRequest(http.MethodGet, p, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s 已登录但角色不足应回 403，实得 %d body=%s", p, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// adminGateServer 装配一张内存 SQLite ＋ **全量路由表**（srv.routes()），并签一个普通会员的 JWT。
//
// 为什么必须走 routes() 而不是自己挑几条：本闸门的价值就在"新路由漏写鉴权当场红"，
// 手挑清单等于把判据退回"人记住要加一条测试"（AGENTS §三「家族级锁要按全集判」）。
// 返回值里的 tok 是**等级不足**的那个身份——反向对照用它（已登录但非管理员必须仍 403）。
func adminGateServer(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("内存库起不来: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	u, err := st.CreateUser(1, "gate_member", auth.PasswordHash("pw123456"), "闸门普通会员", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("建普通会员失败: %v", err)
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
