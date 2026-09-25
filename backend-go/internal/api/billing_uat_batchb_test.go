// ============ billing_uat_batchb_test.go（api 侧）· 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 B 的 api 层回归断言（store 侧同编号断言见
// internal/store/billing_uat_batchb_test.go），钉两条只有过 handler 才测得到的口径：
//
//	① F-21②（双墙联动 + 字符墙钳制）：保存配额时「只改字符墙」必须联动重算积分墙
//	   （旧实现 token 墙纹丝不动，而运行时判据 token 墙优先 → 管理员改字符墙等于没改）；
//	   max_daily_chars 受 system_config quota_max_daily_chars 钳制（B6 收敛口径补齐）；
//	   字符墙置 0（不限）时积分墙同步撤销，不留幽灵墙；
//	② F-21③（闸门拒绝出参）：writeGateError 把闸门拒绝统一成 4xx 结构化错误体——
//	   insufficient_balance → 402，其余（QPS/并发/日限额/预算墙/余额预检）→ 400，
//	   文案原样保留（前端 toast 读 message 的既有链路零改动）。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestUATBatchB
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// newQuotaSaveProbe 装配「Store + 租户服务 + 一个租户及其 tenant_admin」的最小服务器。
func newQuotaSaveProbe(t *testing.T) (*Server, int64, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := ts.Create("uatq2", "配额联动探针租", "", `{"max_daily_chars":20000,"max_daily_tokens":20000}`)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(tn.ID, "uatq2-admin", auth.PasswordHash("pw123456"), "配额探针管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st, Ten: ts, Cfg: config.C}, tn.ID, tok
}

// callQuotaSave 直调配额保存 handler，返回响应记录。
// 参数 s=服务器；tok=JWT；body=请求 JSON。
func callQuotaSave(t *testing.T, s *Server, tok, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/billing/quota_save", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleTenantQuotaSave(rec, req)
	return rec
}

// readProbePerms 读取探针租户当前 permissions（双墙落库判据）。
func readProbePerms(t *testing.T, s *Server, tid int64) *tenant.Perms {
	t.Helper()
	tr, err := s.Ten.GetByID(tid)
	if err != nil {
		t.Fatalf("读租户失败: %v", err)
	}
	p := tenant.ParsePerms(tr.Permissions) // ParsePerms 值返回 *Perms（空串为零值结构体）
	if p == nil {
		t.Fatalf("permissions 解析为 nil: %q", tr.Permissions)
	}
	return p
}

// TestUATBatchB_F21_QuotaSaveDualWallLinkage ①：只改字符墙必须联动积分墙；钳制与清零两向都钉。
func TestUATBatchB_F21_QuotaSaveDualWallLinkage(t *testing.T) {
	s, tid, tok := newQuotaSaveProbe(t)
	// 场景 1：未传 max_daily_points，改字符墙 12345 → 积分墙按 1:1 同源联动
	if rec := callQuotaSave(t, s, tok, `{"qps":5,"concurrent":2,"max_daily_chars":12345}`); rec.Code != 200 {
		t.Fatalf("保存应成功，got %d %s", rec.Code, rec.Body.String())
	}
	p := readProbePerms(t, s, tid)
	if p.MaxDailyChars != 12345 || p.MaxDailyTokens != 12345 {
		t.Fatalf("双墙应同源联动 12345/12345，got %d/%d", p.MaxDailyChars, p.MaxDailyTokens)
	}
	// GET 出参同步：max_daily_points = 积分墙折算值（不再展示旧值误导管理员）
	req := httptest.NewRequest(http.MethodGet, "/api/admin/billing/quota", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleTenantQuota(rec, req)
	var got map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("quota 出参非法 JSON: %v", err)
	}
	if want := float64(s.Store.PointsFromTokens(12345)); got["max_daily_points"] != want {
		t.Fatalf("max_daily_points 应联动为 %v，got %v", want, got["max_daily_points"])
	}
	// 场景 2：显式传积分（2000 分 ×300=600000 token）→ 字符墙保持、积分墙按折算落库
	if rec := callQuotaSave(t, s, tok, `{"qps":5,"concurrent":2,"max_daily_chars":12345,"max_daily_points":2000}`); rec.Code != 200 {
		t.Fatalf("带积分保存应成功，got %d %s", rec.Code, rec.Body.String())
	}
	if p := readProbePerms(t, s, tid); p.MaxDailyTokens != 600000 {
		t.Fatalf("显式积分应折 600000 token，got %d", p.MaxDailyTokens)
	}
	// 场景 3：字符墙置 0（不限）且未传积分 → 积分墙同步撤销（幽灵墙不留库）
	if rec := callQuotaSave(t, s, tok, `{"qps":5,"concurrent":2,"max_daily_chars":0}`); rec.Code != 200 {
		t.Fatalf("清零保存应成功，got %d", rec.Code)
	}
	if p := readProbePerms(t, s, tid); p.MaxDailyChars != 0 || p.MaxDailyTokens != 0 {
		t.Fatalf("两墙应同时清零，got %d/%d", p.MaxDailyChars, p.MaxDailyTokens)
	}
	// 场景 4：平台钳制 quota_max_daily_chars=1000 → 自调 999999 收敛到 1000（B6 口径）
	if err := s.Store.SetConfig("quota_max_daily_chars", "1000"); err != nil {
		t.Fatalf("写钳制配置失败: %v", err)
	}
	if rec := callQuotaSave(t, s, tok, `{"qps":5,"concurrent":2,"max_daily_chars":999999}`); rec.Code != 200 {
		t.Fatalf("钳制保存应成功（收敛非拒绝），got %d", rec.Code)
	}
	if p := readProbePerms(t, s, tid); p.MaxDailyChars != 1000 || p.MaxDailyTokens != 1000 {
		t.Fatalf("字符墙应被钳到 1000 且联动同源，got %d/%d", p.MaxDailyChars, p.MaxDailyTokens)
	}
}

// TestUATBatchB_F21_GateErrorStructured4xx ②：闸门拒绝统一 4xx——
// 402 只留给余额耗尽，其余配额类 400；文案原样透出（前端 toast 链路零改动）。
func TestUATBatchB_F21_GateErrorStructured4xx(t *testing.T) {
	s := &Server{}
	cases := []struct {
		name       string
		gerr       error
		wantStatus int
		wantCode   string
	}{
		{"余额耗尽→402", billing.NewQuotaErr("组织积分已耗尽，请联系管理员及时充值", "insufficient_balance"), 402, "INSUFFICIENT_BALANCE"},
		{"日限额→400", billing.NewQuotaErr("已达到今日用量上限", "daily_quota_exceeded"), 400, "QUOTA_EXCEEDED"},
		{"QPS 裸文案→400", &apiErr{"请求过于频繁，请稍后再试"}, 400, "QUOTA_EXCEEDED"},
		{"预算墙→400", &apiErr{"部门月度预算已用尽"}, 400, "QUOTA_EXCEEDED"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/tickets/create", nil)
			rec := httptest.NewRecorder()
			s.writeGateError(rec, req, c.gerr)
			if rec.Code != c.wantStatus {
				t.Fatalf("状态码应为 %d，got %d（body=%s）", c.wantStatus, rec.Code, rec.Body.String())
			}
			var body map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("错误体非法 JSON: %v", err)
			}
			if body["success"] != false || body["code"] != c.wantCode {
				t.Fatalf("错误体应含 success=false + code=%s，got %v", c.wantCode, body)
			}
			if body["message"] != c.gerr.Error() {
				t.Fatalf("文案必须原样透出，got %q want %q", body["message"], c.gerr.Error())
			}
			// 旧「200 + success:false」口径已死：状态码不得再出现 200
			if rec.Code == 200 {
				t.Fatal("闸门拒绝禁止回 200")
			}
		})
	}
}
