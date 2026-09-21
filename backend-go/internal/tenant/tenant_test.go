// ============ tenant_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-6）：internal/tenant（600 行，多租户核心）的回归断言。
// 分两块：
//
//	A. 纯逻辑（无 DB）：context 注入/取值「缺失即 0/空串」的兜底语义、有效期状态计算、
//	   权限 JSON 解析容错、options 目标语种提取。这些分支直接决定「漏注入租户时会不会串数据」，
//	   改坏的表现是跨租户读写或订阅到期不失效，属于静默故障，必须钉住。
//	B. 存储层（内存 SQLite）：建表/补列幂等、默认租户、创建与唯一性、有效期与停用状态、
//	   策略/流程配置读写回环、品牌字段 JSON 校验、删除租户的容错。
//
// 方言自钉 sqlite（AGENTS.md 一.4）：config.Default() 会写全局 config.C，
// 不钉死会在 PG 模式下把方言泄漏给同包内存库用例，产生 `no such table` 假红。
// 另附 Benchmark：租户/模式上下文解析在每个翻译请求的热路径上（报告 §4.1-8 零 Benchmark 缺口）。
// =============================================
package tenant

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"

	_ "modernc.org/sqlite"
)

// pinSQLite 自钉 SQLite 方言并在测试结束后恢复全局 config（AGENTS.md 一.4 模板）。
func pinSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// ============ A. 纯逻辑：上下文兜底 ============

// TestContextTenantAndUserFallback 未注入必须返回 0（调用方据此回退默认租户/记归属缺失），
// 且不同键互不干扰（历史上曾因键类型复用导致 user_id 被当成 tenant_id 读取）。
func TestContextTenantAndUserFallback(t *testing.T) {
	bg := context.Background()
	if got := FromContext(bg); got != 0 {
		t.Fatalf("空 ctx 的租户 ID 应为 0，实得 %d", got)
	}
	if got := UserFromContext(bg); got != 0 {
		t.Fatalf("空 ctx 的用户 ID 应为 0，实得 %d", got)
	}
	// 注入后各自取回，且不串键
	ctx := WithUser(WithTenant(bg, 42), 7)
	if got := FromContext(ctx); got != 42 {
		t.Fatalf("租户 ID 应为 42，实得 %d", got)
	}
	if got := UserFromContext(ctx); got != 7 {
		t.Fatalf("用户 ID 应为 7，实得 %d", got)
	}
	// 非 int64 值（误用键位）必须落到 0 而不是 panic
	if got := FromContext(context.WithValue(bg, ctxKey{}, "not-an-int")); got != 0 {
		t.Fatalf("类型不匹配时应回退 0，实得 %d", got)
	}
}

// TestContextModeAndLang 计费模式与目标语种上下文的兜底语义（空=按 pro / 按通配定价）。
func TestContextModeAndLang(t *testing.T) {
	bg := context.Background()
	if got := ModeFromContext(bg); got != "" {
		t.Fatalf("未注入模式应为空串，实得 %q", got)
	}
	if got := LangFromContext(bg); got != "" {
		t.Fatalf("未注入语种应为空串，实得 %q", got)
	}
	ctx := WithLang(WithMode(bg, "fast"), "en")
	if ModeFromContext(ctx) != "fast" || LangFromContext(ctx) != "en" {
		t.Fatalf("注入后取值不符：%q / %q", ModeFromContext(ctx), LangFromContext(ctx))
	}
	// 空模式也要能原样存取（上层按「空视为 pro」处理，此处不得吞成别的值）
	if got := ModeFromContext(WithMode(bg, "")); got != "" {
		t.Fatalf("空模式应仍为空串，实得 %q", got)
	}
}

// TestLangFromOptions 从翻译 options 提取主目标语种：非数组/空数组/元素非字符串都必须回空。
func TestLangFromOptions(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]interface{}
		want string
	}{
		{"正常多语种取首个", map[string]interface{}{"target_langs": []interface{}{"en", "ja"}}, "en"},
		{"单语种", map[string]interface{}{"target_langs": []interface{}{"ru"}}, "ru"},
		{"空数组", map[string]interface{}{"target_langs": []interface{}{}}, ""},
		{"元素非字符串", map[string]interface{}{"target_langs": []interface{}{123}}, ""},
		{"键缺失", map[string]interface{}{}, ""},
		{"类型是字符串而非数组（前端误传）", map[string]interface{}{"target_langs": "en"}, ""},
		{"nil options", nil, ""},
	}
	for _, c := range cases {
		if got := LangFromOptions(c.in); got != c.want {
			t.Errorf("%s：LangFromOptions=%q，期望 %q", c.name, got, c.want)
		}
	}
}

// ============ A. 纯逻辑：状态与权限解析 ============

// TestEffectiveStatus 有效期/停用状态计算：disabled 永远优先，过期自动 expired，格式错不降级。
func TestEffectiveStatus(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	past := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		name, status, expires, want string
	}{
		{"无有效期即原状态", StatusActive, "", StatusActive},
		{"未到期保持 active", StatusActive, future, StatusActive},
		{"到期转 expired", StatusActive, past, StatusExpired},
		{"手动停用优先级最高（即便未到期）", StatusDisabled, future, StatusDisabled},
		{"手动停用覆盖已过期", StatusDisabled, past, StatusDisabled},
		{"有效期格式非法不做过期判定（老数据兼容）", StatusActive, "2026-13-45", StatusActive},
		{"未知状态原样透出", "weird", "", "weird"},
	}
	for _, c := range cases {
		if got := effectiveStatus(c.status, c.expires); got != c.want {
			t.Errorf("%s：effectiveStatus(%q,%q)=%q，期望 %q", c.name, c.status, c.expires, got, c.want)
		}
	}
}

// TestParsePerms 权限 JSON 解析容错：空/非法均返回零值结构（不得 panic），关键开关按名取回。
func TestParsePerms(t *testing.T) {
	if p := ParsePerms(""); p == nil || len(p.Langs) != 0 || p.MaxDailyTokens != 0 {
		t.Fatalf("空权限应为零值结构，实得 %+v", p)
	}
	if p := ParsePerms("{不是JSON"); p == nil {
		t.Fatal("非法 JSON 应返回零值结构而非 nil")
	} else if len(p.Langs) != 0 {
		t.Fatalf("非法 JSON 不得解析出内容，实得 %+v", p)
	}
	raw := `{"langs":["en","ja"],"max_daily_tokens":500000,"package_code":"pro_y","auto_renew":true,"sentence_balance":300}`
	p := ParsePerms(raw)
	if len(p.Langs) != 2 || p.Langs[0] != "en" {
		t.Fatalf("langs 解析不符：%+v", p.Langs)
	}
	if p.MaxDailyTokens != 500000 || p.PackageCode != "pro_y" || !p.AutoRenew || p.SentenceBalance != 300 {
		t.Fatalf("关键字段解析不符：%+v", p)
	}
	// 未出现的键保持零值（新增面板键不得让旧配置误判为「已配置」）
	if q := ParsePerms(`{"langs":["en"]}`); q.PackageCode != "" || q.AutoRenew || q.MaxDailyTokens != 0 {
		t.Fatalf("缺省字段应为零值，实得 %+v", q)
	}
}

// ============ B. 存储层（内存 SQLite） ============

// newTenantStore 内存 SQLite 上的租户 Store（NewStore 内部幂等建表 + 补列）。
func newTenantStore(t *testing.T) *Store {
	t.Helper()
	pinSQLite(t)
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s, err := NewStore(conn)
	if err != nil {
		t.Fatalf("创建租户 Store 失败: %v", err)
	}
	return s
}

// TestEnsureDefaultIdempotent 默认租户 rox 幂等：重复调用返回同一 ID。
func TestEnsureDefaultIdempotent(t *testing.T) {
	s := newTenantStore(t)
	id1, err := s.EnsureDefault()
	if err != nil || id1 <= 0 {
		t.Fatalf("EnsureDefault 失败: id=%d err=%v", id1, err)
	}
	id2, err := s.EnsureDefault()
	if err != nil || id2 != id1 {
		t.Fatalf("重复执行应返回同一默认租户 ID：%d vs %d (err=%v)", id1, id2, err)
	}
	byCode, err := s.GetByCode("rox")
	if err != nil || byCode == nil || byCode.ID != id1 {
		t.Fatalf("默认租户应可按 code 查到: %+v err=%v", byCode, err)
	}
}

// TestTenantLifecycle 创建 → 唯一性 → 到期/停用状态计算 → 列表。
func TestTenantLifecycle(t *testing.T) {
	s := newTenantStore(t)
	if _, err := s.Create("", "无编码", "", ""); err == nil {
		t.Fatal("空编码必须被拒绝")
	} else if !strings.Contains(err.Error(), "编码") {
		t.Fatalf("错误信息应说明编码问题，实得 %v", err)
	}
	t1, err := s.Create("acme", "Acme 科技", time.Now().AddDate(0, 0, 30).Format(time.RFC3339), `{"langs":["en"]}`)
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	if t1.Status != StatusActive {
		t.Fatalf("新建租户应 active，实得 %s", t1.Status)
	}
	if _, err := s.Create("acme", "重复编码", "", ""); err == nil {
		t.Fatal("编码唯一约束应拒绝重复创建")
	}
	// 改成已过期：状态由有效期自动算出 expired（不改库里的 status 字段）
	if err := s.Update(t1.ID, t1.Name, time.Now().Add(-time.Hour).Format(time.RFC3339), t1.Permissions); err != nil {
		t.Fatalf("更新有效期失败: %v", err)
	}
	got, _ := s.GetByID(t1.ID)
	if got.Status != StatusExpired {
		t.Fatalf("有效期过后应计算为 expired，实得 %s", got.Status)
	}
	// 手动停用优先于有效期
	if err := s.SetStatus(t1.ID, StatusDisabled); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	if got, _ = s.GetByID(t1.ID); got.Status != StatusDisabled {
		t.Fatalf("手动停用应覆盖过期态，实得 %s", got.Status)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("列表应含 1 个租户，实得 %d err=%v", len(list), err)
	}
}

// TestTenantConfigRoundTrip 策略配置与流程配置的 JSON 回环（含空值/未配置读取语义）。
func TestTenantConfigRoundTrip(t *testing.T) {
	s := newTenantStore(t)
	tt, err := s.Create("cfg-test", "配置测试", "", "")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	// 未配置 → 零值且不报错（上层据此走平台默认策略）
	pc, err := s.GetPolicyConfig(tt.ID)
	if err != nil || pc.HighSim != 0 || pc.OpsPolicy != "" {
		t.Fatalf("未配置策略应为零值: %+v err=%v", pc, err)
	}
	pass := 1
	cross := 0 // 显式关闭跨部门降级（与「未设置=nil=默认开启」必须可区分）
	want := PolicyConfig{HighSim: 0.92, MedSim: 0.75, EvalsPassThreshold: 80, DataFeedbackOptOut: &pass, CrossDeptFallback: &cross}
	want.OpsPolicy = `{"boost":1}`
	if err := s.SetPolicyConfig(tt.ID, want); err != nil {
		t.Fatalf("写入策略失败: %v", err)
	}
	got, err := s.GetPolicyConfig(tt.ID)
	if err != nil {
		t.Fatalf("读取策略失败: %v", err)
	}
	if got.HighSim != 0.92 || got.MedSim != 0.75 || got.EvalsPassThreshold != 80 || got.OpsPolicy != `{"boost":1}` {
		t.Fatalf("策略回环不符: %+v", got)
	}
	if got.DataFeedbackOptOut == nil || *got.DataFeedbackOptOut != 1 {
		t.Fatalf("指针三态在回环后丢失: %v", got.DataFeedbackOptOut)
	}
	if got.CrossDeptFallback == nil || *got.CrossDeptFallback != 0 {
		t.Fatalf("显式 0 的跨部门开关被丢弃（会被误判为默认开启）: %v", got.CrossDeptFallback)
	}
	// 流程步骤配置
	fc := FlowConfig{Steps: map[string]bool{"gate": true, "culture_gate": false}}
	if err := s.SetFlowConfig(tt.ID, fc); err != nil {
		t.Fatalf("写入流程配置失败: %v", err)
	}
	read, err := s.GetFlowConfig(tt.ID)
	if err != nil {
		t.Fatalf("读取流程配置失败: %v", err)
	}
	if !read.Steps["gate"] || read.Steps["culture_gate"] {
		t.Fatalf("流程步骤回环不符: %+v", read.Steps)
	}
	// 未知租户读策略：SQL 错误被吞成零值（保持「未配置」语义，不得向上抛错打断请求）
	if pc, err := s.GetPolicyConfig(9999); err != nil || pc.HighSim != 0 {
		t.Fatalf("未知租户应返回零值不报错: %+v err=%v", pc, err)
	}
}

// TestTenantBrandingAndFlags 品牌字段与布尔开关（PG 方言踩坑点：bool 必须转 1/0 写入）。
func TestTenantBrandingAndFlags(t *testing.T) {
	s := newTenantStore(t)
	tt, err := s.Create("brand-test", "品牌测试", "", "")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	if err := s.SetBranding(tt.ID, "极石", "https://cdn/logo.png", "brand.example.com",
		`[{"label":"关于我们","label_en":"About","url":"https://x.com/about"}]`); err != nil {
		t.Fatalf("保存品牌失败: %v", err)
	}
	got, _ := s.GetByID(tt.ID)
	if got.BrandName != "极石" || got.Domain != "brand.example.com" {
		t.Fatalf("品牌回显不符: %+v", got)
	}
	if byDomain, err := s.GetByDomain("brand.example.com"); err != nil || byDomain == nil || byDomain.ID != tt.ID {
		t.Fatalf("应能按域名反查租户: %+v err=%v", byDomain, err)
	}
	// 非法 JSON 必须被拒（历史上会静默写坏页脚渲染）
	if err := s.SetBranding(tt.ID, "x", "", "", `{不是数组}`); err == nil {
		t.Fatal("brand_links 非法 JSON 应被拒绝")
	}
	// 品牌多语言名：空串归一为 "{}"，非法对象被拒
	if err := s.SetBrandNames(tt.ID, `{"zh":"极石","en":"ROX"}`); err != nil {
		t.Fatalf("保存品牌多语言名失败: %v", err)
	}
	if g, _ := s.GetByID(tt.ID); !strings.Contains(g.BrandNames, "ROX") {
		t.Fatalf("品牌多语言名回显不符: %q", g.BrandNames)
	}
	if err := s.SetBrandNames(tt.ID, "[1,2]"); err == nil {
		t.Fatal("brand_names 非对象 JSON 应被拒绝")
	}
	if err := s.SetBrandNames(tt.ID, ""); err != nil {
		t.Fatalf("清空品牌多语言名失败: %v", err)
	}
	if g, _ := s.GetByID(tt.ID); g.BrandNames != "{}" {
		t.Fatalf("清空后应为 {}，实得 %q", g.BrandNames)
	}
	// bool 开关（invite / personal）：读写必须一致
	if err := s.SetInviteEnabled(tt.ID, true); err != nil {
		t.Fatalf("设置邀请开关失败: %v", err)
	}
	if err := s.SetPersonal(tt.ID, true); err != nil {
		t.Fatalf("设置个人租户标记失败: %v", err)
	}
	if g, _ := s.GetByID(tt.ID); !g.InviteEnabled || !g.IsPersonal {
		t.Fatalf("布尔开关回显不符: invite=%v personal=%v", g.InviteEnabled, g.IsPersonal)
	}
	if err := s.SetInviteEnabled(tt.ID, false); err != nil {
		t.Fatalf("关闭邀请开关失败: %v", err)
	}
	if g, _ := s.GetByID(tt.ID); g.InviteEnabled {
		t.Fatal("关闭后 invite_enabled 应为 false（1/0 转换回归）")
	}
	if err := s.SetIndustry(tt.ID, "manufacturing"); err != nil {
		t.Fatalf("设置行业失败: %v", err)
	}
	if g, _ := s.GetByID(tt.ID); g.Industry != "manufacturing" {
		t.Fatalf("行业回显不符: %q", g.Industry)
	}
	if n, err := s.Name(tt.ID); err != nil || n != "品牌测试" {
		t.Fatalf("Name 查询不符: %q err=%v", n, err)
	}
	if _, err := s.Name(9999); err == nil {
		t.Fatal("未知租户 Name 应返回错误（调用方据此判存在性）")
	}
}

// TestTenantDeleteGuards 删除入参守卫与「旧库缺表不中断」的容错。
func TestTenantDeleteGuards(t *testing.T) {
	s := newTenantStore(t)
	if err := s.Delete(0); err == nil {
		t.Fatal("ID<=0 的删除必须被拒绝")
	}
	tt, err := s.Create("del-test", "待删除", "", "")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	// 本测试库里只有 tenants 表（业务表未建），删除必须照样成功——
	// 这正是 delTenantRows 对 "no such table" 容错的意义（旧库升级路径不中断）。
	if err := s.Delete(tt.ID); err != nil {
		t.Fatalf("删除租户失败（应容忍缺表）: %v", err)
	}
	if _, err := s.GetByID(tt.ID); err == nil {
		t.Fatal("删除后不应再查到该租户")
	}
}

// TestEnsureColumnsIdempotent 二次 NewStore 必须幂等（补列入口唯一、启动可重复执行）。
func TestEnsureColumnsIdempotent(t *testing.T) {
	pinSQLite(t)
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for i := 0; i < 2; i++ {
		if _, err := NewStore(conn); err != nil {
			t.Fatalf("第 %d 次建表失败: %v", i+1, err)
		}
	}
	// 补列后再写入应能吃到默认值（brand_names 默认 '{}'）
	if _, err := db.Exec(conn, db.CurrentDialect(),
		"INSERT INTO tenants (code, name, created_at, updated_at) VALUES ('col-test','补列验证','a','b')"); err != nil {
		t.Fatalf("插入失败: %v", err)
	}
	g, err := (&Store{db: conn}).GetByCode("col-test")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if g.BrandNames != "{}" || g.Permissions != "{}" {
		t.Fatalf("补列默认值不符: brand_names=%q permissions=%q", g.BrandNames, g.Permissions)
	}
}

// ============ Benchmark（报告 §4.1-8） ============

// BenchmarkContextResolution 租户/用户/模式/语种四键解析（每个翻译请求至少各读写一次）。
func BenchmarkContextResolution(b *testing.B) {
	base := WithLang(WithMode(WithUser(WithTenant(context.Background(), 7), 21), "fast"), "en")
	b.ReportAllocs()
	b.ResetTimer()
	var sink int64
	var s string
	for i := 0; i < b.N; i++ {
		sink += FromContext(base) + UserFromContext(base)
		s = ModeFromContext(base) + LangFromContext(base)
	}
	_ = sink
	_ = s
}

// BenchmarkParsePerms 权限 JSON 解析（每请求 gateUsage/限额判定都会解一遍）。
func BenchmarkParsePerms(b *testing.B) {
	raw := `{"langs":["en","ja","de"],"max_daily_tokens":500000,"package_code":"pro_year","auto_renew":true,"cross_dept_fallback":1}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ParsePerms(raw)
	}
}
