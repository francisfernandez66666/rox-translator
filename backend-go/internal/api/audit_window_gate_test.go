// ============ audit_window_gate_test.go · 职责说明 ============
// 审计列表「缺省时间界」（★ 2026-11-04 体验批 A3）的行为闸门。打的是真 handler
// （handleSystemAudit），不是比源码字面量——这一族的坏形态恰好是「界写在源码里但没接到读点」。
//
// 四条射程：
//
//	A1 列表腿（没给 from）⇒ 只回窗口内的行，且出参如实报出 window_days（界必须出声）；
//	A2 显式给了 from ⇒ 一律以调用方的界为准，缺省界不得覆盖它（否则「筛不到旧记录」变成不可解释）；
//	A3 导出腿 ⇒ **不套**缺省界（合规留痕少一天＝缺件），且 CSV 仍带 before_val/after_val 两列；
//	A4 脏 limit（非数字／负数／超上限）⇒ 折回档位、不报错、行照常回。
//
// ⚠️ 射程自证（A1 不是空锁）：A1 判"窗口外的行不在列表里"，而这条只有和 A2/A3 一起才成立——
//
//	那两条在同一张表、同一个账号上把同一行读得到，才证明红绿都来自「界」而不是「行没写进去」。
//
// 方言：自钉内存 SQLite（AGENTS §一·4 模板，经 newF44Probe→pinSqliteDialect）。
// 超管全量视图 LEFT JOIN tenants／users，内存库补最小 tenants 表（同 o1_mock_audit_test 口径）。
// =============================================
package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// auditGateUsernames 记录本测各用例自建的超管名（同包内多次建号，名字必须互不撞）。
//
// newAuditGateProbe 起一块内存库＋一枚超管 token，并种两条 gate_probe 审计：
// 一条"此刻"（走产品写点 LogAuditDiff）、一条"窗口之外"（直插 SQL——产品写点造不出过期行，
// created_at 按写入侧同一格式 time.Now().Format(RFC3339) 的偏移量塞，保证字典序比较可比）。
func newAuditGateProbe(t *testing.T, who string) (*Server, string, string, string) {
	t.Helper()
	s, sqlDB, _ := newF44Probe(t)
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT DEFAULT '')`); err != nil {
		t.Fatalf("建最小 tenants 表失败: %v", err)
	}
	created, err := s.Store.CreateUser(0, who, "hash", "审计窗口闸门超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("建超管失败: %v", err)
	}
	tok, err := auth.Sign(created, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	oldDetail, newDetail := "窗口外审计行", "窗口内审计行"
	oldTS := time.Now().AddDate(0, 0, -(store.AuditListWindowDays + 30)).Format(time.RFC3339)
	if _, err := sqlDB.Exec(
		"INSERT INTO audit_logs (tenant_id, user_id, action, resource, detail, before_val, after_val, created_at) VALUES (?,?,?,?,?,?,?,?)",
		int64(0), created.ID, "gate_probe", "system", oldDetail, `{"old":1}`, `{"new":2}`, oldTS); err != nil {
		t.Fatalf("种窗口外审计行失败: %v", err)
	}
	s.Store.LogAuditDiff(0, created.ID, "gate_probe", "system", newDetail, `{"a":1}`, `{"b":2}`)
	return s, tok, oldDetail, newDetail
}

// callAuditList 直调 GET /api/system/audit?<query>，返回状态码与解码后的响应体。
func callAuditList(t *testing.T, s *Server, tok, query string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/system/audit"+query, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleSystemAudit(rec, req)
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("审计响应不是 JSON（code=%d）: %s", rec.Code, rec.Body.String())
	}
	return rec.Code, out
}

// auditDetails 把响应里的 logs 摊成 detail 列表；顺带把「空页必须是 [] 不是 null」这条判掉。
func auditDetails(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	raw, ok := body["logs"]
	if !ok {
		t.Fatalf("响应缺 logs 键: %v", body)
	}
	if raw == nil {
		t.Fatal("logs 出栈成 null——每个消费方都要各判一次空（AGENTS §一·5 空页归一口径）")
	}
	arr, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("logs 不是数组: %T", raw)
	}
	var out []string
	for _, it := range arr {
		m, _ := it.(map[string]interface{})
		out = append(out, fmt.Sprint(m["detail"]))
	}
	return out
}

// TestAuditListDefaultWindowApplied A1＋出声：缺省界真把窗口外的行挡住，且出参报出天数。
func TestAuditListDefaultWindowApplied(t *testing.T) {
	s, tok, oldDetail, newDetail := newAuditGateProbe(t, "audit_gate_w1")
	code, body := callAuditList(t, s, tok, "")
	if code != http.StatusOK {
		t.Fatalf("列表应 200，实得 %d: %v", code, body)
	}
	got := auditDetails(t, body)
	if !containsString(got, newDetail) {
		t.Fatalf("窗口内的行没回（缺省界把当天也切了，或读点根本没走到）: %v", got)
	}
	if containsString(got, oldDetail) {
		t.Fatalf("缺省时间界没生效——窗口外的行仍在列表里: %v", got)
	}
	// 出声那一半：界是服务端加的，出参必须报出天数，否则审计页把"被挡住"画成"没有"
	if wd, _ := body["window_days"].(float64); int(wd) != store.AuditListWindowDays {
		t.Fatalf("window_days 应等于缺省档 %d，实得 %v", store.AuditListWindowDays, body["window_days"])
	}
}

// TestAuditExplicitFromOverridesDefault A2：显式给了 from ⇒ 以调用方为准，缺省界不得覆盖。
func TestAuditExplicitFromOverridesDefault(t *testing.T) {
	s, tok, oldDetail, newDetail := newAuditGateProbe(t, "audit_gate_w2")
	from := time.Now().AddDate(0, 0, -(store.AuditListWindowDays + 60)).Format("2006-01-02")
	code, body := callAuditList(t, s, tok, "?from="+from)
	if code != http.StatusOK {
		t.Fatalf("带显式 from 应 200，实得 %d: %v", code, body)
	}
	got := auditDetails(t, body)
	if !containsString(got, oldDetail) || !containsString(got, newDetail) {
		t.Fatalf("显式 from 应把窗口外那一行一起带回（缺省界不许覆盖调用方的界）: %v", got)
	}
	if wd, _ := body["window_days"].(float64); int(wd) != 0 {
		t.Fatalf("调用方已表态，window_days 必须回 0（回非 0＝把别人的界冒充成自己的缺省档）: %v", body["window_days"])
	}
}

// TestAuditExportNotWindowedKeepsBigFields A3：导出腿不套缺省界、仍带 before_val/after_val。
func TestAuditExportNotWindowedKeepsBigFields(t *testing.T) {
	s, tok, oldDetail, newDetail := newAuditGateProbe(t, "audit_gate_w3")
	req := httptest.NewRequest(http.MethodGet, "/api/system/audit?export=csv", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleSystemAudit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("导出应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("CSV 解析失败: %v（体首 %q）", err, headBytes(body, 80))
	}
	if len(rows) < 2 {
		t.Fatalf("CSV 行数不足（表头＋至少一条留痕）: %d 行", len(rows))
	}
	// 表头逐列点名（判"砍列"必须按列名判；正文子串会被 detail 里的字冒充）
	want := map[string]bool{"before_val": false, "after_val": false, "detail": false}
	for _, h := range rows[0] {
		if _, ok := want[h]; ok {
			want[h] = true
		}
	}
	for h, seen := range want {
		if !seen {
			t.Fatalf("导出表头缺列 %q（列表可以瘦，导出砍列＝对外契约变更）: %v", h, rows[0])
		}
	}
	// 不套界那一半：窗口外那一条必须在导出体里（同时是 A1 的正向对照——同表同账号，
	// 列表读不到、导出读得到，才证明那条红来自「界」而不是「行没写进去」）
	if !strings.Contains(body, oldDetail) {
		t.Fatalf("CSV 导出被套上了缺省时间界——合规留痕少一天就是缺件: %s", headBytes(body, 300))
	}
	if !strings.Contains(body, newDetail) {
		t.Fatalf("CSV 导出连窗口内的行都没有（上游写点断了？）: %s", headBytes(body, 300))
	}
}

// TestAuditDirtyLimitFoldsBack A4：脏 limit 折回档位、不报错、行照常回。
func TestAuditDirtyLimitFoldsBack(t *testing.T) {
	s, tok, _, newDetail := newAuditGateProbe(t, "audit_gate_w4")
	for _, dirty := range []string{"limit=abc", "limit=-5", "limit=0", "limit=999999"} {
		code, body := callAuditList(t, s, tok, "?"+dirty)
		if code != http.StatusOK {
			t.Fatalf("%s 应折回档位并 200，实得 %d: %v", dirty, code, body)
		}
		if got := auditDetails(t, body); !containsString(got, newDetail) {
			t.Fatalf("%s 折档后行没回（档位判据把请求打歪了）: %v", dirty, got)
		}
	}
}

// TestAuditLimitTiersAreConstants 档位数字只有一份尺子（AGENTS §一·5）：
// 脏档折回的行为判据收在 store 那一个出口，api 层不许再判一次。
func TestAuditLimitTiersAreConstants(t *testing.T) {
	if got := store.EffectiveAuditLimit(0); got != store.AuditPageDefault {
		t.Fatalf("0 应折到缺省档 %d，实得 %d", store.AuditPageDefault, got)
	}
	if got := store.EffectiveAuditLimit(-7); got != store.AuditPageDefault {
		t.Fatalf("负数应折到缺省档，实得 %d", got)
	}
	if got := store.EffectiveAuditLimit(store.AuditPageMax + 1); got != store.AuditPageDefault {
		t.Fatalf("超上限应折到缺省档（与改造前逐字一致），实得 %d", got)
	}
	if got := store.EffectiveAuditLimit(50); got != 50 {
		t.Fatalf("合法档位必须原样生效，实得 %d", got)
	}
	// 缺省界那一档必须由 store 现算、且真的比"今天"早（写死日期串＝隔日必红，见记忆那条夹具教训）
	bound := store.AuditListDefaultFrom()
	if len(bound) != len("2006-01-02") {
		t.Fatalf("缺省界必须是日期档（与 created_at 字典序可比的形状），实得 %q", bound)
	}
	parsed, err := time.ParseInLocation("2006-01-02", bound, time.Local)
	if err != nil {
		t.Fatalf("缺省界解析失败 %q: %v", bound, err)
	}
	if days := int(time.Now().Sub(parsed).Hours() / 24); days < store.AuditListWindowDays || days > store.AuditListWindowDays+1 {
		t.Fatalf("缺省界应落在 %d 天前（允许跨日 1 天误差），实得 %d 天前", store.AuditListWindowDays, days)
	}
}

// headBytes 取响应体前 n 字节用于报错（整体 dump 会把 CSV 全文送进测试日志）。
func headBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
