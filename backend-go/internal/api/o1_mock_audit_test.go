// o1_mock_audit_test.go —— O-1 模拟支付切换审计标记行为锁（批 I-10 定夺的配套断言，2026-09-27）。
//
// 背景（〇-U 观察项 O-1）：超管「支付模式」单选含 mock 项是研发自助验收必需入口（不删），
// 真正的风险是**发布后误切**——切到 mock 后全站收银台出现「模拟支付」，零成本开通套餐。
// 批 I-10 定的处置是三层：① 后端保存时把「谁把 pay_mode 切成 mock」写成带 ⚠ 的硬账审计
// （admin_packages.go 的 package_settings_save）；② 前端 mock 档保存前二次确认；
// ③ 发布红线——两站 pay_mode 必须非 mock（《部署指南》§十）。
// 本测锁 ①：mock 保存必须留 ⚠ 标记行；非 mock 保存**不得**带 ⚠（防空切也报警）；
// 且其余键只记键名不记值（static_qr_image 可能是整张 base64，禁止送进审计）。
//
// 方言：自钉内存 SQLite（AGENTS §一·4，经 newF44Probe→pinSqliteDialect 统一钉法）。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callPayModeSave 以超管 token 直调 POST /api/admin/packages/settings/save 保存 pay_mode。
func callPayModeSave(t *testing.T, s *Server, tok, mode string) {
	t.Helper()
	blob, _ := json.Marshal(map[string]interface{}{"pay_mode": mode})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/packages/settings/save", strings.NewReader(string(blob)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleAdminPackageSettingsSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存 pay_mode=%s 应 200，实得 %d: %s", mode, rec.Code, rec.Body.String())
	}
}

// lastSettingsAuditDetail 取最新一条 package_settings_save 审计的 detail（超管平台上下文 tid=0，
// 查询走 ListAuditFilter 全量视图，需要最小 tenants 表——同 f63 闸门的建表口径）。
func lastSettingsAuditDetail(t *testing.T, s *Server) string {
	t.Helper()
	rows, err := s.Store.ListAuditFilter(0, "package_settings_save", "system", 0, "", "", 5)
	if err != nil {
		t.Fatalf("读取 package_settings_save 审计失败: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("保存动作没有留下审计行（LogAudit 调用点被动过？）")
	}
	return rows[0].Detail
}

// TestO1MockPayModeAuditMarker O-1：mock 切换必须留 ⚠ 硬账；非 mock 与其余键不得带值/告警。
func TestO1MockPayModeAuditMarker(t *testing.T) {
	s, sqlDB, tok := newF44Probe(t)
	// 审计超管视图 LEFT JOIN tenants；内存库迁移不建 tenants，补最小表（同 f63 闸门）。
	if _, err := sqlDB.Exec(`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, name TEXT DEFAULT '')`); err != nil {
		t.Fatalf("建最小 tenants 表失败: %v", err)
	}
	// ① 切到 mock → detail 必须含 pay_mode=mock 与 ⚠ 警示句
	callPayModeSave(t, s, tok, "mock")
	d := lastSettingsAuditDetail(t, s)
	if !strings.Contains(d, "pay_mode") || !strings.Contains(d, "⚠模拟支付已开启") {
		t.Fatalf("★ O-1：mock 切换审计缺显式标记（谁在何时切成 mock 必须一眼可查），实得 %q", d)
	}
	// ② 切回静态码 → 同动作审计不得再带 ⚠（否则告警恒在，出事故时定位不了切换时刻）
	callPayModeSave(t, s, tok, "static_qr")
	d = lastSettingsAuditDetail(t, s)
	if strings.Contains(d, "⚠") {
		t.Fatalf("★ O-1：非 mock 保存不应带 ⚠ 标记，实得 %q", d)
	}
	if !strings.Contains(d, "pay_mode") {
		t.Fatalf("非 mock 保存仍应记键名 pay_mode，实得 %q", d)
	}
}
