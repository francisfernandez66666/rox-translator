// ============ orgs_readpath_no_write_gate_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 A4 的行为闸门：「补租户根组织行」这件事不许再发生在读路径上。
//
// 改造前形态（现网＝超管一进平台视图就慢）：GET /api/admin/orgs 的平台分支每次请求都
// `Ten.List()` 全量列租户 → 逐个 `GetRootOrg` → 查不到就 `EnsureRootOrg` 写一条。
// N 个租户＝1＋2N 次往返，且写落在读路径上（SQLite 下还是写锁竞争源）。
//
// 四条射程：
//
//	W1 行为（本体）：一个**没有根组织行**的租户，打完平台视图 GET 之后仍然没有——
//	   读路径一个字节都不许多写；随后手动跑 RootOrgMigrate ⇒ 行出现、GET 读得到（正向对照，
//	   证明 W1 的红来自「界/写点」而不是「租户根本没建上」）。
//	W2 形态：handleOrgList 函数体内不得再出现 `s.Ten.List()`（那条遍历的锚点）。
//	W3 建租户挂钩：createTenantWithRoot 之后根组织行立刻在位（增量租户的第二条腿）。
//	W4 唯一入口：api 包非测试代码里 `s.Ten.Create(` 只许出现在 createTenantWithRoot 内一处
//	   （漏挂一个建租户口＝那个租户在平台树里不出现且零报错，这正是本批要消灭的静默形态）。
//
// 方言：自钉内存 SQLite（AGENTS §一·4）；tenants 表由 tenant.NewStore 的幂等建表提供。
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// newOrgGateProbe 起一块内存库：store.New（迁移链含 RootOrgMigrate）＋ tenant.NewStore ＋ 一枚超管 token。
func newOrgGateProbe(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	pinSqliteDialect(t)
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("建 Store 失败: %v", err)
	}
	ts, err := tenant.NewStore(raw)
	if err != nil {
		t.Fatalf("建租户 Store 失败: %v", err)
	}
	super, err := st.CreateUser(0, "orggate_super", "hash", "A4 闸门超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("建超管失败: %v", err)
	}
	tok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	return &Server{Store: st, Ten: ts}, raw, tok
}

// orgCountForTenant 数该租户在 orgs 表里的行数（W1 的"读路径有没有多写"就按这个判）。
func orgCountForTenant(t *testing.T, raw *sql.DB, tid int64) int {
	t.Helper()
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM orgs WHERE tenant_id=?", tid).Scan(&n); err != nil {
		t.Fatalf("数 orgs 行失败: %v", err)
	}
	return n
}

// callOrgList 直调 GET /api/admin/orgs，返回状态码与 orgs 数组长度。
func callOrgList(t *testing.T, s *Server, tok string) (int, int) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/orgs", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleOrgList(rec, req)
	var out struct {
		Success bool          `json:"success"`
		Orgs    []interface{} `json:"orgs"`
		Root    interface{}   `json:"root"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("组织视图响应不是 JSON（code=%d）: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, len(out.Orgs)
}

// TestOrgListReadPathWritesNothing W1：平台视图 GET 不再替租户补根组织行。
func TestOrgListReadPathWritesNothing(t *testing.T) {
	s, raw, tok := newOrgGateProbe(t)
	// 绕过一切助手，直接按"库外"形态建一个租户（模拟 bootstrap/SQL 直插那类存量租户）
	co, err := s.Ten.Create("gate_a4", "A4 闸门租户", "", "{}")
	if err != nil {
		t.Fatalf("建租户失败: %v", err)
	}
	if orgCountForTenant(t, raw, co.ID) != 0 {
		t.Fatalf("夹具就不干净：新建租户已经带 org 行，W1 会空转")
	}
	code, _ := callOrgList(t, s, tok)
	if code != http.StatusOK {
		t.Fatalf("平台视图应 200，实得 %d", code)
	}
	// 本体判据：GET 之后该租户仍然 0 行＝读路径没写（改造前这里会被补成 1 行）
	if n := orgCountForTenant(t, raw, co.ID); n != 0 {
		t.Fatalf("读路径仍在补根组织行（GET 前 0 行、GET 后 %d 行）——A4 的那条遍历被写回来了", n)
	}
	// 正向对照：回填迁移跑过之后行必须在位，且平台视图读得到该租户
	s.Store.RootOrgMigrate()
	if n := orgCountForTenant(t, raw, co.ID); n != 1 {
		t.Fatalf("RootOrgMigrate 没补上该租户根组织行（期望 1 行，实得 %d）", n)
	}
	_, orgsAfter := callOrgList(t, s, tok)
	_, orgsBefore := callOrgList(t, s, tok)
	if orgsAfter != orgsBefore {
		t.Fatalf("同一个 GET 两次结果不一致（回填之后不该再有任何补写）: %d vs %d", orgsBefore, orgsAfter)
	}
	// 回填幂等第二腿：再跑一次不得翻倍
	s.Store.RootOrgMigrate()
	if n := orgCountForTenant(t, raw, co.ID); n != 1 {
		t.Fatalf("RootOrgMigrate 非幂等（再跑一次变成 %d 行）", n)
	}
}

// TestOrgListBodyHasNoTenantLoop W2：函数体内不许再出现遍历全租户那条锚点。
// ⚠️ 判据**先剥注释**：本文件上方那段"改造前形态"的说明里逐字写着 s.Ten.List()，
//
//	不剥注释会把"记下了坑"读成"踩了坑"（AGENTS §一·5 那条同判：给锁补形，不删注释）。
func TestOrgListBodyHasNoTenantLoop(t *testing.T) {
	b, err := os.ReadFile("orgs.go")
	if err != nil {
		t.Fatalf("读取 orgs.go 失败（本锁按包内相对路径读源，cwd 应在 internal/api）: %v", err)
	}
	src := stripGoCommentsForGate(string(b))
	body := goFuncBody(t, src, "func (s *Server) handleOrgList(")
	if strings.Contains(body, "s.Ten.List()") {
		t.Fatal("平台视图读路径又带回了 Ten.List() 全量遍历——每次 GET 1＋2N 次往返")
	}
	// 射程自证：取到的必须真是那一段（否则"不含 Ten.List()"是空话）
	if !strings.Contains(body, "ListPlatformOrgs") {
		t.Fatal("函数体里没有 ListPlatformOrgs——取到的 body 不是那一段，W2 判据射程可疑")
	}
	// 同族第二条锚点：逐租户补行的动词也不许在平台分支里成环（EnsureRootOrg 允许出现在
	// 单租户的 O(1) 自愈分支，所以这条判的是"循环＋List"这一对，不判这个词）
	if strings.Count(body, "range tenants") > 0 {
		t.Fatal("平台分支仍按租户 range 补行")
	}
}

// TestCreateTenantWithRootHook W3：建租户入口必须当场补根组织行。
func TestCreateTenantWithRootHook(t *testing.T) {
	s, raw, _ := newOrgGateProbe(t)
	co, err := s.createTenantWithRoot("gate_hook", "挂钩租户", "", "{}")
	if err != nil {
		t.Fatalf("createTenantWithRoot 失败: %v", err)
	}
	if n := orgCountForTenant(t, raw, co.ID); n != 1 {
		t.Fatalf("建租户后根组织行不在位（期望 1 行，实得 %d）——增量租户的第二条腿断了", n)
	}
	if _, err := s.Store.GetRootOrg(co.ID); err != nil {
		t.Fatalf("GetRootOrg 读不到刚补的根组织: %v", err)
	}
}

// TestTenantCreateSingleEntryPoint W4：api 包非测试代码里 s.Ten.Create( 只许出现在 createTenantWithRoot 内。
// 判据吃**剥注释后的全集源码**（apiNonTestSources）——新增一个绕过助手的建租户口当场红。
func TestTenantCreateSingleEntryPoint(t *testing.T) {
	var total int
	var where string
	for name, src := range apiNonTestSources(t) {
		n := strings.Count(src, "s.Ten.Create(")
		if n == 0 {
			continue
		}
		total += n
		where = name
		if n > 1 {
			t.Fatalf("%s 里 s.Ten.Create( 出现 %d 次（建租户口必须只有一个入口）", name, n)
		}
	}
	if total != 1 {
		t.Fatalf("api 包非测试代码里 s.Ten.Create( 应恰 1 处（createTenantWithRoot 内部），实得 %d 处（最后命中在 %s）", total, where)
	}
	src := apiNonTestSources(t)[where]
	body := goFuncBody(t, src, "func (s *Server) createTenantWithRoot(")
	if !strings.Contains(body, "s.Ten.Create(") {
		t.Fatalf("那一处不在 createTenantWithRoot 函数体内——建租户又有旁路了")
	}
	if !strings.Contains(body, "EnsureRootOrg") {
		t.Fatalf("createTenantWithRoot 里没有 EnsureRootOrg——挂钩被摘掉了（增量租户缺根组织且零报错）")
	}
}
