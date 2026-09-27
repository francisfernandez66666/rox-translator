// ============ register_dedicated_invite_test.go · 职责说明 ============
// F-77（2026-09-27 〇-X 第 4 项）「专属域名注册必须凭邀请码（或走后台批量导入）」的回归锁。
//
// 为什么要有这条断言：这条行为**过去被注释写成相反的意思**（旧注释写「免邀请码」），
// 而代码里 dedicatedTid>0 分支缺邀请码是直接 400 的。注释与代码相反比代码本身错更危险——
// 下一个人照注释改前端表单（把邀请码输入框去掉）或照注释做客户答复，就会在线上架空一道闸。
// 所以本批把注释订正为「必须邀请码 / 或后台 Excel 批量导入」的同时，
// 用一条 HTTP 级断言把真实行为钉死（AGENTS.md §三：修缺陷必须同步补可复现断言）。
//
// 五段：
//
//	A) 品牌子域 + 无邀请码 → 400，且**没有**顺手建出独立企业租户（拒绝发生在建租户之前）；
//	B) 邀请码属于别家企业 → 400「不属于该企业」（防跨租户投毒）；
//	C) 邀请码正确 → 200，人落在**品牌方那家租户**里、身份是普通成员（即便表单选了 type=company，
//	   也不给建企业/升管理员）；
//	D) 同一张邀请码二次使用 → 400（一次性语义没被专用分支绕过）；
//	E) 反向对照：同样的「无邀请码」请求走主站 Host → 不再报「企业邀请码」，
//	   证明这道闸只对品牌子域生效，主站通用注册能力没被打死。
//
// ★ 方言由 newRenewFixture 自钉 sqlite（AGENTS.md §一·4）。
// ==========================================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// f77Seq 给每个夹具一个**独立且共享缓存**的内存库名（见 f77Fixture 注释）。
var f77Seq int64

// f77Fixture 本用例自带的测试栈（不复用 newRenewFixture，原因是一次首跑真踩出来的）：
//
//	":memory:" 在 database/sql 下是「每条连接各自的私有库」——迁移跑在连接 1 上，
//	注册成功链路（欢迎邮件、审计、配额）一旦并发摸库就会拨出连接 2，那里根本没有表，
//	于是本测试随机红在「no such table: users / tenants」（而把池收到 1 条又会死锁，见下）。
//	这里改用 file:<唯一名>?mode=memory&cache=shared：同一进程内所有连接共享同一份内存库，
//	名字带序号是为了并行/多次跑夹具时互不见面（各自 Cleanup 关闭后即销毁，不落磁盘）。
//
// ★ 连接池**不能**钉成 1 条：store.New 的 PackagesTenantMigrate 会在 PRAGMA 游标未关时
//
//	再开一条查询（packages.go 里的嵌套 db.Query），单连接池等于让 store.New 永远阻塞
//	（首跑实测 60s 测试超时，栈顶就停在 packages.go 那次嵌套 Query 上）。
func f77Fixture(t *testing.T) *renewFixture {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	dsn := fmt.Sprintf("file:regf77_%d?mode=memory&cache=shared", atomic.AddInt64(&f77Seq, 1))
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开共享内存库失败: %v", err)
	}
	// ★ 连接池**不能**钉成 1：PackagesTenantMigrate 会在游标未关时再开一条查询
	//   （packages.go:234 的 PRAGMA 循环里嵌 db.Query），单连接池会直接死锁在 store.New。
	//   共享缓存已经让"第二条连接"看到的是同一份库，所以这里只给足并发度、不再收口。
	raw.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	ten, err := tenant.NewStore(raw)
	if err != nil {
		t.Fatalf("创建 tenant.Store 失败: %v", err)
	}
	// 先占一家「平台宿主」租户：品牌闸夹具有 ID>1 的自证（默认平台租户按设计被订阅扫描跳过），
	//   没有这一层占位时第一家测试租户会拿到 ID=1，断言直接判红。
	if _, err := ten.Create("f77host", "平台宿主租户", "", "{}"); err != nil {
		t.Fatalf("预置宿主租户失败: %v", err)
	}
	return &renewFixture{srv: &Server{Store: st, Ten: ten, regGuard: newRegisterGuard(st)}}
}

// f77Mux 只挂注册接口，其余走真实 handler。
func f77Mux(s *Server) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/auth/register", s.handleRegister)
	return m
}

// f77Register 以指定 Host（品牌子域或主站）发起一次注册，返回状态码与解析后的响应体。
func f77Register(t *testing.T, s *Server, host string, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("编码注册请求体失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Host = host
	// clientIP(r) 在无 RemoteAddr 变化时始终是同一个测试 IP：多轮注册必须放开间隔与日限，
	// 否则第二腿起就是 429，把「邀请码闸」的判据换成「限流闸」的判据（假红且定位偏）。
	if err := s.Store.SetConfig("register_ip_min_interval_sec", "0"); err != nil {
		t.Fatalf("放开注册间隔失败: %v", err)
	}
	if err := s.Store.SetConfig("register_ip_daily_limit", "200"); err != nil {
		t.Fatalf("放开注册日限失败: %v", err)
	}
	rec := httptest.NewRecorder()
	f77Mux(s).ServeHTTP(rec, r)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析注册响应失败: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, m
}

func TestDedicatedRegisterRequiresInviteCode(t *testing.T) {
	f := f77Fixture(t)
	brandDomainCfg(t, f)

	// 品牌方租户：domain=code（brandGateTenant 已预置），基础域 lexicorn.cn ⇒ 子域 f77brand.lexicorn.cn
	brandTid := brandGateTenant(t, f, "f77brand", "renew_month", time.Now().AddDate(0, 0, 20))
	otherTid := brandGateTenant(t, f, "f77other", "renew_month", time.Now().AddDate(0, 0, 20))
	host := "f77brand.lexicorn.cn"

	// 基线：品牌方租户里的账号数与全站租户数（后面用「没多出来」做等值锁）
	baseUsers := f77UsersOf(t, f, brandTid)
	baseTenants := f77TenantCount(t, f)

	// A) 无邀请码 → 400「请填写企业邀请码」，且既不落账号也不落企业租户
	code, m := f77Register(t, f.srv, host, map[string]any{
		"username": "f77_noinvite", "password": "pass123456", "email": "f77a@t.test",
		"type": "company", "code": "f77evilco", "name": "偷建的企业", "agreed": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("品牌子域无邀请码注册应 400，实际 %d %v", code, m)
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "邀请码") {
		t.Fatalf("拒绝文案必须点名「邀请码」（否则客户不知道去哪儿要码），实际 %q", msg)
	}
	if got := f77UsersOf(t, f, brandTid); got != baseUsers {
		t.Fatalf("被拒的那轮不该在品牌方租户里落账号：%d → %d", baseUsers, got)
	}
	if got := f77TenantCount(t, f); got != baseTenants {
		t.Fatalf("被拒的那轮不该建出独立企业租户（旧注释「免邀请码」的错误读法正是这条）：%d → %d", baseTenants, got)
	}
	if _, err := f.srv.Ten.GetByCode("f77evilco"); err == nil {
		t.Fatal("请求里自带的企业编码 f77evilco 不该被建出来")
	}

	// B) 别家企业的邀请码 → 400（跨租户投毒）
	if _, err := f.srv.Store.CreateInviteCode("F77-CODE-OTHER", otherTid); err != nil {
		t.Fatalf("造别家邀请码失败: %v", err)
	}
	code, m = f77Register(t, f.srv, host, map[string]any{
		"username": "f77_wrongco", "password": "pass123456", "email": "f77b@t.test",
		"type": "company", "code": "f77evilco2", "name": "别家的码", "invite": "F77-CODE-OTHER", "agreed": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("别家邀请码在品牌子域注册应 400，实际 %d %v", code, m)
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "该企业") {
		t.Fatalf("拒绝文案应点到「不属于该企业」，实际 %q", msg)
	}

	// C) 正确邀请码 → 200，人落在品牌方租户且身份是普通成员（表单选 company 也不给升权）
	if _, err := f.srv.Store.CreateInviteCode("F77-CODE-OK", brandTid); err != nil {
		t.Fatalf("造邀请码失败: %v", err)
	}
	code, m = f77Register(t, f.srv, host, map[string]any{
		"username": "f77_ok", "password": "pass123456", "email": "f77c@t.test",
		"type": "company", "code": "f77evilco3", "name": "凭码加入", "invite": "F77-CODE-OK", "agreed": true,
	})
	if code != http.StatusOK || m["success"] != true {
		t.Fatalf("持本企业邀请码应注册成功，实际 %d %v", code, m)
	}
	u, err := f.srv.Store.GetUserByUsername(brandTid, "f77_ok")
	if err != nil || u == nil {
		t.Fatalf("受邀人应落在品牌方租户 %d 里，实际查询失败: %v", brandTid, err)
	}
	if u.Role != store.RoleUser {
		t.Fatalf("专属域名注册必须强制普通成员，实际 role=%q", u.Role)
	}
	if _, err := f.srv.Ten.GetByCode("f77evilco3"); err == nil {
		t.Fatal("受邀加入不该另外建出请求里带的企业租户")
	}

	// D) 同一张码二次使用 → 400（一次性语义没被专用分支绕过）
	code, m = f77Register(t, f.srv, host, map[string]any{
		"username": "f77_ok2", "password": "pass123456", "email": "f77d@t.test",
		"type": "personal", "invite": "F77-CODE-OK", "agreed": true,
	})
	if code != http.StatusBadRequest {
		t.Fatalf("已使用的邀请码应 400，实际 %d %v", code, m)
	}

	// E) 反向对照：同样不带邀请码，走主站 Host 不该报「企业邀请码」
	//   （证明这道闸只挂在品牌子域上，主站的通用注册/创建企业能力没被打死）。
	//   主站链路会走完整的建租户流程，夹具里可能因缺依赖而报别的错——
	//   故这里只判「拒绝理由不是企业邀请码」，不判它一定成功。
	code, m = f77Register(t, f.srv, "langcross.lexicorn.cn", map[string]any{
		"username": "f77_main", "password": "pass123456", "email": "f77e@t.test",
		"type": "company", "code": "f77mainco", "name": "主站自建企业", "agreed": true,
	})
	msg, _ := m["message"].(string)
	if code != http.StatusOK && strings.Contains(msg, "请填写企业邀请码") {
		t.Fatalf("主站注册不该被企业邀请码闸拦住，实际 %d %q", code, msg)
	}
}

// f77UsersOf 某租户下的账号数（走 Store.DB() 裸查询：夹具要的是「一行都没多」这种总量判据，
// 用现成的 ListUsers 会被分页/过滤口径带偏）。
func f77UsersOf(t *testing.T, f *renewFixture, tid int64) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow("SELECT COUNT(*) FROM users WHERE tenant_id=?", tid).Scan(&n); err != nil {
		t.Fatalf("统计租户 %d 账号数失败: %v", tid, err)
	}
	return n
}

// f77TenantCount 全站租户数（证「被拒的那轮没偷偷建企业」）。
func f77TenantCount(t *testing.T, f *renewFixture) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow("SELECT COUNT(*) FROM tenants").Scan(&n); err != nil {
		t.Fatalf("统计租户总数失败: %v", err)
	}
	return n
}
