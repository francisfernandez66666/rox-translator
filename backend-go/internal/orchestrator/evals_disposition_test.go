// ============================================================================
// evals_disposition_test.go — 评估不合格处置单测（改造 4，2026-09-17）：
//   - 阈值边界：total<阈值 → status=failed + 打标；>=阈值 → passed
//   - 阈值关闭（<=0）：仅记录，不打标不提醒
//   - 提醒开关（evals_alert_enabled=0）：仍打标落库但不打扰运营
//   - 提醒幂等：同工单同语言同阶段仅告警一次（payload.EvalNotified）
//   - 初翻/校对阶段各自独立提醒
//
// 使用内存 SQLite Store（复用引擎测试族方言钉死模式），群机器人渠道未配置=仅告警中心落库。
// ============================================================================
package orchestrator

import (
	"database/sql"
	"encoding/json"
	"testing"

	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// dispSetup 构建处置测试用 Workflow + Store（内存 SQLite，方言钉死防 PG 矩阵泄漏）。
func dispSetup(t *testing.T, threshold int) (*Workflow, *store.Store) {
	t.Helper()
	oldCfg := config.C
	config.C = config.Default()
	config.C.DatabaseDriver = "sqlite"
	t.Cleanup(func() { config.C = oldCfg })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if threshold >= 0 {
		_ = st.SetConfig("evals_fail_threshold", itoa(threshold))
	}
	return &Workflow{Store: st}, st
}

// itoa 整数转字符串（测试辅助）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// dispTicket 构建处置测试工单。
func dispTicket() *store.Ticket {
	return &store.Ticket{ID: 7, TenantID: 2, TicketNo: "T20260917UAT"}
}

// TestApplyEvalDispositionThreshold 阈值边界：59 失败打标、60 通过、61 通过。
func TestApplyEvalDispositionThreshold(t *testing.T) {
	w, _ := dispSetup(t, 60)
	tk := dispTicket()
	p := &ticketPayload{}

	if got := w.applyEvalDisposition(tk, p, "en", 59.9, "initial"); got != "failed" {
		t.Fatalf("total=59.9 期望 failed，实际 %s", got)
	}
	if len(p.QualityFlaggedLangs) != 1 || p.QualityFlaggedLangs[0] != "en" {
		t.Fatalf("59.9 应打标 en: %v", p.QualityFlaggedLangs)
	}

	p2 := &ticketPayload{}
	for _, total := range []float64{60, 60.1, 95} {
		if got := w.applyEvalDisposition(tk, p2, "en", total, "initial"); got != "passed" {
			t.Fatalf("total=%v 期望 passed，实际 %s", total, got)
		}
	}
	if len(p2.QualityFlaggedLangs) != 0 {
		t.Fatalf("达标不应打标: %v", p2.QualityFlaggedLangs)
	}
}

// TestApplyEvalDispositionDisabled 阈值置 0（关闭）：低分也仅记录，passed 且不打标。
func TestApplyEvalDispositionDisabled(t *testing.T) {
	w, _ := dispSetup(t, 0)
	tk := dispTicket()
	p := &ticketPayload{}
	if got := w.applyEvalDisposition(tk, p, "en", 12.3, "initial"); got != "passed" {
		t.Fatalf("处置关闭应恒 passed，实际 %s", got)
	}
	if len(p.QualityFlaggedLangs) != 0 || len(p.EvalNotified) != 0 {
		t.Fatalf("处置关闭不应打标或提醒: %v %v", p.QualityFlaggedLangs, p.EvalNotified)
	}
}

// TestApplyEvalDispositionAlertSwitchOff 提醒单独开关：evals_alert_enabled=0 时仍打标落库，
// 但不写告警中心、不记 EvalNotified（灰度期静默观察口径）。
func TestApplyEvalDispositionAlertSwitchOff(t *testing.T) {
	w, st := dispSetup(t, 60)
	if err := st.SetConfig("evals_alert_enabled", "0"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	tk := dispTicket()
	p := &ticketPayload{}
	if got := w.applyEvalDisposition(tk, p, "en", 30, "initial"); got != "failed" {
		t.Fatalf("提醒关闭仍应判 failed，实际 %s", got)
	}
	if len(p.QualityFlaggedLangs) != 1 || p.QualityFlaggedLangs[0] != "en" {
		t.Fatalf("提醒关闭仍应打标: %v", p.QualityFlaggedLangs)
	}
	if len(p.EvalNotified) != 0 {
		t.Fatalf("提醒关闭不应记 EvalNotified: %v", p.EvalNotified)
	}
	alerts, _ := st.ListAlerts(2, "open", 50)
	if len(alerts) != 0 {
		t.Fatalf("提醒关闭不应写告警中心，实际 %d 条", len(alerts))
	}
}

// TestApplyEvalDispositionNotifyIdempotent 同语言同阶段重复触发仅告警一次；跨阶段独立。
func TestApplyEvalDispositionNotifyIdempotent(t *testing.T) {
	w, st := dispSetup(t, 60)
	tk := dispTicket()
	p := &ticketPayload{}

	// 首次：failed + 告警 1 条
	if got := w.applyEvalDisposition(tk, p, "en", 40, "initial"); got != "failed" {
		t.Fatalf("期望 failed，实际 %s", got)
	}
	// 同阶段同语言再次低分：仍 failed 但不重复告警
	if got := w.applyEvalDisposition(tk, p, "en", 35, "initial"); got != "failed" {
		t.Fatalf("二次低分应仍 failed，实际 %s", got)
	}
	// 校对阶段：独立提醒一次
	if got := w.applyEvalDisposition(tk, p, "en", 45, "review"); got != "failed" {
		t.Fatalf("review 低分应 failed，实际 %s", got)
	}
	// 打标去重：en 只出现一次
	cnt := 0
	for _, f := range p.QualityFlaggedLangs {
		if f == "en" {
			cnt++
		}
	}
	if cnt != 1 {
		t.Fatalf("打标应去重（en 出现 1 次），实际 %d 次: %v", cnt, p.QualityFlaggedLangs)
	}
	// 告警中心幂等（CreateAlertEx 同 tenant+kind open 去重）+ EvalNotified 两阶段各 1
	alerts, _ := st.ListAlerts(2, "open", 50)
	if len(alerts) != 1 {
		t.Fatalf("告警中心同 kind 幂等应为 1 条 open，实际 %d", len(alerts))
	}
	if !p.EvalNotified["initial:en"] || !p.EvalNotified["review:en"] {
		t.Fatalf("EvalNotified 标记缺失: %v", p.EvalNotified)
	}
}

// TestApplyEvalDispositionMultiLang 多语言独立打标：en 低分 de 达标，只标 en。
func TestApplyEvalDispositionMultiLang(t *testing.T) {
	w, _ := dispSetup(t, 60)
	tk := dispTicket()
	p := &ticketPayload{}
	_ = w.applyEvalDisposition(tk, p, "en", 30, "initial")
	_ = w.applyEvalDisposition(tk, p, "de", 88, "initial")
	if len(p.QualityFlaggedLangs) != 1 || p.QualityFlaggedLangs[0] != "en" {
		t.Fatalf("仅 en 应被打标: %v", p.QualityFlaggedLangs)
	}
	if _, ok := p.EvalNotified["initial:de"]; ok {
		t.Fatalf("达标语言不应有提醒标记: %v", p.EvalNotified)
	}
}

// TestEvalDispositionPayloadRoundTrip 打标字段 JSON 序列化往返（payload 持久化口径）。
func TestEvalDispositionPayloadRoundTrip(t *testing.T) {
	p := ticketPayload{
		EvalScores:          map[string]float64{"en": 42.5},
		QualityFlaggedLangs: []string{"en"},
		EvalNotified:        map[string]bool{"initial:en": true},
	}
	b, err := json.Marshal(&p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var q ticketPayload
	if err := json.Unmarshal(b, &q); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if q.QualityFlaggedLangs[0] != "en" || !q.EvalNotified["initial:en"] || q.EvalScores["en"] != 42.5 {
		t.Fatalf("往返数据不符: %+v", q)
	}
}
