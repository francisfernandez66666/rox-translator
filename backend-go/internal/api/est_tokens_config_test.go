// ============ 本文件职责中文说明 ============
// 计费预估系数超管配置口（★ F-72 补口，2026-09-27 〇-X 第 5 项，api/est_tokens_config.go）单测：
//
//	A GET 未配置态：回显＝代码缺省、stored 四键皆空（"库里没表态"要能在界面上看出来）、
//	  defaults 与 formula 在场（公式要能对客户公开，界面上的数必须和读侧用的是同一份）。
//	B POST 合法保存 → 200 且回显新值；库里四行逐键等值；再 GET 稳定。
//	C 校验先行、整批拒绝：缺项 / K 清零 / 负数 / 超上限 一律 400 且 message 可读中文，
//	  ★ 拒绝后库内仍是上一批的值（半套参数＝短单与长单口径不一致，比不调更糟）；
//	  反向对照：F 档配 0 必须 200（那是「明知短单低估仍退回纯线性」的合法选择，不许误伤）。
//	D reset：四键清空、生效值回代码缺省（与从未配置过完全同形，不留 0 这种会被误读的中间态）。
//	E 鉴权分流：匿名 401、租户管理员 403（未登录与等级不足必须分流——前端只在 401 走重登录）。
//	F 回显即可信值：库里被旁路写成脏值（"abc"）时，GET 回显的是读侧真正在用的缺省，
//	  且 estimateTicketTokens 的取数入口（estTokensPerChar/estTokensFixed）同值——
//	  否则管理台显示一个数、预检用另一个数，表单就成了误导源。
//	G 审计留痕：保存与重置各落一条 audit_logs，detail 里带得上改成的数（谁把闸门拧松了要可查）。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4）：config.Default() 副作用写全局 config.C，
//
//	run_uat 的 PG 模式会把方言泄漏给同包内存 SQLite 用例（历史两次踩坑），必须钉底。
//	运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestEstTokens
//
// ==========================================
package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// estCfgFixture 内存 SQLite + 一个平台超管与一个租户管理员的最小服务栈。
type estCfgFixture struct {
	srv    *Server
	st     *store.Store
	super  string // 超管 JWT（tenant_id=0）
	tenant string // 本租户管理员 JWT（tenant_id=7，等级达标但非超管）
}

// newEstCfgFixture 建栈并签发两枚 JWT。
// 参数 t 用于注册清理与失败定位。返回: 两角色齐备的夹具。
func newEstCfgFixture(t *testing.T) *estCfgFixture {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	// tenants 由 internal/tenant 的迁移建表、store.New 不涵盖，而超管审计视图要 JOIN 它，
	// 故按 f63_audit_detail_test 的既有做法补一张最小表（只读断言用，不参与业务）。
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT DEFAULT '')`); err != nil {
		t.Fatalf("补 tenants 表失败: %v", err)
	}
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	super, err := st.CreateUser(0, "est_super", "hash", "预估配置超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	ta, err := st.CreateUser(7, "est_tenant_admin", "hash", "租户管理员", store.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	stok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatalf("签发超管 JWT 失败: %v", err)
	}
	ttok, err := auth.Sign(ta, time.Hour)
	if err != nil {
		t.Fatalf("签发租户管理员 JWT 失败: %v", err)
	}
	return &estCfgFixture{srv: &Server{Store: st}, st: st, super: stok, tenant: ttok}
}

// estCfgMux 预估系数口（与生产注册同 handler、同路径）。
func (s *Server) estCfgMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/config/est-tokens", s.handleAdminEstTokens)
	return m
}

// estCfgBody 组装四项一次交齐的请求体（数值由各用例给定）。
func estCfgBody(kPro, kFast, fPro, fFast float64) map[string]interface{} {
	return map[string]interface{}{"k_pro": kPro, "k_fast": kFast, "fixed_pro": fPro, "fixed_fast": fFast}
}

// estCfgStoredOf 直读 system_config 某键的原始值（键不存在或值为空都回 ""，
// 两者对读侧的含义一致：都走代码缺省，故不区分）。
func (f *estCfgFixture) estCfgStoredOf(t *testing.T, key string) string {
	t.Helper()
	v, err := f.st.GetConfig(key)
	if err != nil {
		t.Fatalf("直读 system_config[%s] 失败: %v", key, err)
	}
	return strings.TrimSpace(v)
}

// estCfgAuditLatest 取最近一条指定 action 的审计 detail（无记录回空串）。
func (f *estCfgFixture) estCfgAuditLatest(t *testing.T, action string) string {
	t.Helper()
	rows, err := f.st.ListAuditFilter(0, action, "system_config", 0, "", "", 5)
	if err != nil {
		t.Fatalf("读审计 %s 失败: %v", action, err)
	}
	if len(rows) == 0 {
		return ""
	}
	return rows[0].Detail
}

// A+E 未配置态回显与鉴权分流
func TestEstTokensConfigGetDefaultsAndAuthz(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.estCfgMux()

	code, resp := doJSON(t, h, http.MethodGet, "/api/admin/config/est-tokens", f.super, nil)
	if code != 200 {
		t.Fatalf("超管读取应 200，got %d %+v", code, resp)
	}
	eff, _ := resp["coefficients"].(map[string]interface{})
	if eff["k_pro"] != defEstKPro || eff["k_fast"] != defEstKFast ||
		eff["fixed_pro"] != defEstFixedPro || eff["fixed_fast"] != defEstFixedFast {
		t.Fatalf("未配置时应回代码缺省 160/60/3000/1200，got %+v", eff)
	}
	stored, _ := resp["stored"].(map[string]interface{})
	for _, k := range []string{"k_pro", "k_fast", "fixed_pro", "fixed_fast"} {
		if s, _ := stored[k].(string); s != "" {
			t.Fatalf("未配置态 stored[%s] 应为空串（界面据此标注「走缺省」），got %q", k, s)
		}
	}
	defs, _ := resp["defaults"].(map[string]interface{})
	if defs["k_pro"] != defEstKPro {
		t.Fatalf("defaults 回显错误: %+v", defs)
	}
	if fs, _ := resp["formula"].(string); !strings.Contains(fs, "F") || !strings.Contains(fs, "K") {
		t.Fatalf("formula 应含两段式表达式，got %q", fs)
	}
	if r, _ := resp["points_tokens_rate"].(float64); r <= 0 {
		t.Fatalf("points_tokens_rate 应回显正数供界面折算积分，got %v", resp["points_tokens_rate"])
	}

	// 鉴权分流：匿名 401、租户管理员（L3）403
	if code, _ = doJSON(t, h, http.MethodGet, "/api/admin/config/est-tokens", "", nil); code != 401 {
		t.Fatalf("匿名读取应 401，got %d", code)
	}
	if code, _ = doJSON(t, h, http.MethodGet, "/api/admin/config/est-tokens", f.tenant, nil); code != 403 {
		t.Fatalf("租户管理员读取应 403，got %d", code)
	}
	if code, _ = doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.tenant,
		estCfgBody(200, 80, 4000, 1500)); code != 403 {
		t.Fatalf("租户管理员保存应 403，got %d", code)
	}
	// 越权尝试不得改动任何配置
	if v := f.estCfgStoredOf(t, cfgEstKPro); v != "" {
		t.Fatalf("越权保存不得落库，got %q", v)
	}
}

// B+C+G 保存链路：合法整批生效、非法整体拒绝且不半写、审计留痕
func TestEstTokensConfigSaveValidatesAllBeforeWriting(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.estCfgMux()

	// 第一批合法值落库
	code, resp := doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.super,
		estCfgBody(150, 55, 2800, 1100))
	if code != 200 {
		t.Fatalf("合法保存应 200，got %d %+v", code, resp)
	}
	eff, _ := resp["coefficients"].(map[string]interface{})
	if eff["k_pro"] != 150.0 || eff["fixed_fast"] != 1100.0 {
		t.Fatalf("保存响应应回显新生效值，got %+v", eff)
	}
	for key, want := range map[string]string{cfgEstKPro: "150", cfgEstKFast: "55", cfgEstFixedPro: "2800", cfgEstFixedFast: "1100"} {
		if got := f.estCfgStoredOf(t, key); got != want {
			t.Fatalf("库内 %s 应为 %q，got %q", key, want, got)
		}
	}

	// 非法批次（三项好值 + 一项 K 清零）必须整体 400，且库里仍是第一批
	bad := []struct {
		name string
		body map[string]interface{}
	}{
		{"K 清零", estCfgBody(150, 0, 2800, 1100)},
		{"负数固定项", estCfgBody(150, 55, -1, 1100)},
		{"超上限", estCfgBody(1e9, 55, 2800, 1100)},
	}
	for _, b := range bad {
		code, resp = doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.super, b.body)
		if code != 400 {
			t.Fatalf("%s 应 400，got %d %+v", b.name, code, resp)
		}
		if msg, _ := resp["message"].(string); msg == "" || !containsChinese(msg) {
			t.Fatalf("%s 的拒绝理由应为可读中文，got=%q", b.name, msg)
		}
		for key, want := range map[string]string{cfgEstKPro: "150", cfgEstKFast: "55", cfgEstFixedPro: "2800", cfgEstFixedFast: "1100"} {
			if got := f.estCfgStoredOf(t, key); got != want {
				t.Fatalf("%s 被拒后不得半写：%s 变成 %q（应为 %q）", b.name, key, got, want)
			}
		}
	}
	// 缺项同样整体拒绝（四项一次交齐是「不留半套参数」的前提）
	code, resp = doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.super,
		map[string]interface{}{"k_pro": 150, "k_fast": 55, "fixed_pro": 2800})
	if code != 400 {
		t.Fatalf("缺 fixed_fast 应 400，got %d %+v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "fixed_fast") {
		t.Fatalf("缺项报错必须点出缺的是哪项，got=%q", msg)
	}
	// 反向对照：F 档配 0 合法（＝运维显式退回纯线性），不得误伤
	if code, resp = doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.super,
		estCfgBody(150, 55, 0, 0)); code != 200 {
		t.Fatalf("固定项配 0 应 200（合法表态），got %d %+v", code, resp)
	}
	if got := f.estCfgStoredOf(t, cfgEstFixedPro); got != "0" {
		t.Fatalf("固定项 0 应原样入库，got %q", got)
	}
	if v := f.srv.estTokensFixed("pro"); v != 0 {
		t.Fatalf("读侧应取到 0（关闭固定项），got %v", v)
	}
	// 审计留痕：成功保存的每一批都要能对上数
	detail := f.estCfgAuditLatest(t, "est_tokens_save")
	if !strings.Contains(detail, "fixed_pro=0") || !strings.Contains(detail, "k_pro=150") {
		t.Fatalf("审计 detail 应含本批改后的数，got=%q", detail)
	}
}

// D reset：清空回代码缺省，与从未配置同形
func TestEstTokensConfigResetToDefaults(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.estCfgMux()
	if code, resp := doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.super,
		estCfgBody(150, 55, 2800, 1100)); code != 200 {
		t.Fatalf("前置保存失败: %d %+v", code, resp)
	}
	code, resp := doJSON(t, h, http.MethodPost, "/api/admin/config/est-tokens", f.super,
		map[string]interface{}{"reset": true})
	if code != 200 {
		t.Fatalf("reset 应 200，got %d %+v", code, resp)
	}
	eff, _ := resp["coefficients"].(map[string]interface{})
	if eff["k_pro"] != defEstKPro || eff["fixed_pro"] != defEstFixedPro {
		t.Fatalf("reset 后应回代码缺省，got %+v", eff)
	}
	for _, key := range []string{cfgEstKPro, cfgEstKFast, cfgEstFixedPro, cfgEstFixedFast} {
		if got := f.estCfgStoredOf(t, key); got != "" {
			t.Fatalf("reset 后 %s 的库值应清空（空＝走缺省），got %q", key, got)
		}
	}
	if d := f.estCfgAuditLatest(t, "est_tokens_reset"); !strings.Contains(d, "缺省") {
		t.Fatalf("reset 也要留审计，got=%q", d)
	}
}

// F 脏库值：读侧回退保守缺省，GET 回显与预检取数同值（表单不得成为误导源）
func TestEstTokensConfigDirtyValueEchoesEffective(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.estCfgMux()
	if err := f.st.SetConfig(cfgEstKPro, "abc"); err != nil {
		t.Fatalf("写入脏值失败: %v", err)
	}
	if err := f.st.SetConfig(cfgEstFixedPro, "-5"); err != nil {
		t.Fatalf("写入负数固定项失败: %v", err)
	}
	code, resp := doJSON(t, h, http.MethodGet, "/api/admin/config/est-tokens", f.super, nil)
	if code != 200 {
		t.Fatalf("脏值下读取仍应 200（回显可信值，不是把库中原样吐出），got %d", code)
	}
	eff, _ := resp["coefficients"].(map[string]interface{})
	if eff["k_pro"] != defEstKPro || eff["fixed_pro"] != defEstFixedPro {
		t.Fatalf("脏值应回显读侧真正在用的缺省，got %+v", eff)
	}
	stored, _ := resp["stored"].(map[string]interface{})
	if s, _ := stored["k_pro"].(string); s != "abc" {
		t.Fatalf("stored 要如实回显库内原值供排障，got %q", s)
	}
	// 与预检取数入口逐键等值：界面显示的数＝系统用的数
	if v := f.srv.estTokensPerChar("pro"); v != eff["k_pro"] {
		t.Fatalf("回显与预检取数不一致：界面 %v / 预检 %v", eff["k_pro"], v)
	}
	if v := f.srv.estTokensFixed("pro"); v != eff["fixed_pro"] {
		t.Fatalf("固定项回显与预检不一致：界面 %v / 预检 %v", eff["fixed_pro"], v)
	}
	// 脏值下建单预检仍按保守档放行判断（不放大、不清零）
	if got := estimateTicketTokens(1000, 2, "pro", estParams{
		kPro: f.srv.estTokensPerChar("pro"), kFast: f.srv.estTokensPerChar("fast"),
		fixedPro: f.srv.estTokensFixed("pro"), fixedFast: f.srv.estTokensFixed("fast"),
	}); got != int64(defEstFixedPro)+1000*2*int64(defEstKPro) {
		t.Fatalf("脏值下预估应按 pro 保守缺省，got %d", got)
	}
}
