// ============ domain_profiles_test.go · 职责说明 ============
// 文档级领域词表缓存表的读写锁（2026-10-29 第十二轮）：upsert / 未过期命中 / 过期不命中 / 清理。
// ============================================
package store

import (
	"testing"
	"time"
)

func TestDomainProfilesCRUD(t *testing.T) {
	st, _ := newKBEnv(t)
	now := time.Now().UTC()
	fp := "fp-domain-1"
	row := &DomainProfileRow{
		Fingerprint: fp, TenantID: 1, Code: "quant", Label: "量化交易", Confidence: 0.9,
		TermsJSON: `[{"src":"动量","targets":{"en":"Momentum"}}]`,
		ExpiresAt: now.Add(time.Hour).Format(time.RFC3339),
	}
	if err := st.PutDomainProfile(row); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	got, err := st.GetDomainProfile(fp, now)
	if err != nil || got == nil || got.Code != "quant" || got.Label != "量化交易" {
		t.Fatalf("未过期行应命中：%+v err=%v", got, err)
	}
	// upsert：同一指纹改 code，读出必须是最新那份。
	row.Code = "finance"
	if err := st.PutDomainProfile(row); err != nil {
		t.Fatalf("更新失败：%v", err)
	}
	if got, _ := st.GetDomainProfile(fp, now); got == nil || got.Code != "finance" {
		t.Fatalf("upsert 后应读到新值：%+v", got)
	}
	// 过期：按"此刻"判不命中。
	if got, _ := st.GetDomainProfile(fp, now.Add(2*time.Hour)); got != nil {
		t.Fatalf("过期行不该命中：%+v", got)
	}
	// 清理：过期行被删（清理按传入时刻）。
	if err := st.PruneDomainProfiles(now.Add(2 * time.Hour)); err != nil {
		t.Fatalf("清理失败：%v", err)
	}
	if got, _ := st.GetDomainProfile(fp, now); got != nil {
		t.Fatalf("清理后仍能读到：%+v", got)
	}
	if got, err := st.GetDomainProfile("missing", now); err != nil || got != nil {
		t.Fatalf("不存在的指纹应返回 (nil,nil)，实得 %+v err=%v", got, err)
	}
}
