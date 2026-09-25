// ============================================================================
// api/register_lang_f17_test.go — F-17 批E（2026-09-25）注册语言落库端到端断言
// 修复文档断言「register 单测断 app_lang 落库」：直调 handleRegister（个人用户分支，
// 不触发邀请码/专属域名/人机验证/邮箱验证），锁三条口径：
//
//	①载荷 app_lang 命中白名单 → users.preferred_lang 落库；
//	②载荷缺失回落 X-App-Lang 头（〇-S #12 authHeaders 自动附带链路）；
//	③载荷非法 + 头合法 → 头顶上；双非法 → 空串（未选语种，中文链路，不阻断注册）。
//
// 方言：自钉内存 SQLite（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestUATBatchE_Register
// ============================================================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"
)

// f17RegisterServer 装配注册链路最小服务器（Store + 租户服务 + 注册护栏）。
func f17RegisterServer(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	// 注册链路有并发 goroutine 查库：裸 :memory: 每条新连接都是独立空库（no such table 随机红），
	// 而钉单连接会在「持 Rows 时嵌套查询」处池死锁——用命名共享缓存内存库：同进程全部连接共用一库。
	sqlDB, err := sql.Open("sqlite", "file:f17reg?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// 同 IP 连发多条注册是本测形态：最小间隔归 0（默认 60s）+ 日上限放宽（默认 3 次），
	// 否则第 2/第 4 条会被注册护栏打死成 429——护栏本身另有专测，不在本锁射程。
	if err := st.SetConfig("register_ip_min_interval_sec", "0"); err != nil {
		t.Fatalf("关闭注册最小间隔失败: %v", err)
	}
	if err := st.SetConfig("register_ip_daily_limit", "50"); err != nil {
		t.Fatalf("放宽注册日上限失败: %v", err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatalf("创建租户服务失败: %v", err)
	}
	return &Server{Store: st, Ten: ts, Cfg: config.C, regGuard: newRegisterGuard(st)}
}

// callF17Register 直调 handleRegister（个人用户、免邮箱验证态），返回注册响应。
// 参数 username 唯一化避免同库串号；appLang 空=载荷不下发该字段。
func callF17Register(t *testing.T, s *Server, username, appLang, headerLang string) map[string]interface{} {
	t.Helper()
	body := map[string]interface{}{
		"username": username, "password": "pw123456", "type": "personal",
		"email": username + "@uat-f17.test", "agreed": true,
	}
	if appLang != "" {
		body["app_lang"] = appLang
	}
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	if headerLang != "" {
		r.Header.Set("X-App-Lang", headerLang)
	}
	w := httptest.NewRecorder()
	s.handleRegister(w, r)
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("注册响应非 JSON（status=%d）: %s", w.Code, w.Body.String())
	}
	if out["success"] != true {
		t.Fatalf("注册应成功（status=%d）: %s", w.Code, w.Body.String())
	}
	return out
}

// f17PreferredOf 读回某用户名的 preferred_lang（跨租户按用户名唯一定位）。
func f17PreferredOf(t *testing.T, s *Server, username string) string {
	t.Helper()
	us, err := s.Store.GetUserByUsernameGlobal(username)
	if err != nil || len(us) != 1 {
		t.Fatalf("按用户名查注册账号失败: len=%d err=%v", len(us), err)
	}
	got, err := s.Store.GetPreferredLang(us[0].ID)
	if err != nil {
		t.Fatalf("GetPreferredLang 失败: %v", err)
	}
	return got
}

// TestUATBatchE_RegisterAppLangPersists 四条落库口径等值锁。
func TestUATBatchE_RegisterAppLangPersists(t *testing.T) {
	s := f17RegisterServer(t)

	callF17Register(t, s, "f17_ja", "ja", "de") // 载荷压头
	if got := f17PreferredOf(t, s, "f17_ja"); got != "ja" {
		t.Fatalf("app_lang=ja 应落库 ja（且压过 X-App-Lang 头），实际 %q", got)
	}
	callF17Register(t, s, "f17_hdr", "", "fr") // 载荷缺失回落头
	if got := f17PreferredOf(t, s, "f17_hdr"); got != "fr" {
		t.Fatalf("无载荷应回落 X-App-Lang 头 fr，实际 %q", got)
	}
	callF17Register(t, s, "f17_bad", "xx", "ko") // 非法载荷由头顶上
	if got := f17PreferredOf(t, s, "f17_bad"); got != "ko" {
		t.Fatalf("载荷非法应回落头 ko，实际 %q", got)
	}
	callF17Register(t, s, "f17_none", "", "") // 双空=未选语种（中文链路），注册照常成功
	if got := f17PreferredOf(t, s, "f17_none"); got != "" {
		t.Fatalf("双空应落空串（不写非法值），实际 %q", got)
	}
	callF17Register(t, s, "f17_hant", "zh-hant", "") // 连字符码归一为 zh_hant 落库（与 PDF/模板键同口径）
	if got := f17PreferredOf(t, s, "f17_hant"); got != "zh_hant" {
		t.Fatalf("zh-hant 应归一落库 zh_hant，实际 %q", got)
	}
}
