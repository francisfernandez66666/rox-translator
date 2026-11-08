// ============ sales_draft_mined_gate_test.go · 职责说明 ============
// 二期 S2 talkmining 落库口的真驱动行为锁（M2 的「新产物恒 draft」＋幂等＋租户隔离）。
// 与 sales_draft_gate_test.go 的分工：那边管转正唯一入口与自学习边界，
// 这边管「挖掘侧新开的 INSERT 口不许绕过草稿态」（配变异体 M0183）。
// ================================================
package store

import "testing"

// TestMinedDraftIsAlwaysDraftAndIdempotent 幂等写入、恒 draft、跨租户不串、非法入参拒收。
func TestMinedDraftIsAlwaysDraftAndIdempotent(t *testing.T) {
	s := newDraftTestStore(t)
	const id = "sdr_m_deadbeef_12345678"

	created, err := s.CreateMinedDraft(1, id, "anchor:2/s1", "客户原话（已脱敏）", "可复用说法", "sig-1")
	if err != nil || !created {
		t.Fatalf("首次写入应成功：created=%v err=%v", created, err)
	}
	got, err := s.GetSalesDraft(1, id)
	if err != nil {
		t.Fatalf("读回草稿失败: %v", err)
	}
	if got.Status != SalesDraftStatusDraft {
		t.Fatalf("新产物必须是 draft（挖掘产物不许自动生效），实得 %s", got.Status)
	}
	if got.SuggestedText != "可复用说法" || got.AttributionRef != "sig-1" {
		t.Fatalf("落库字段串位：%+v", got)
	}

	// 同 ID 重跑：幂等命中，不重复出稿。
	again, err := s.CreateMinedDraft(1, id, "anchor:2/s1", "客户原话（已脱敏）", "可复用说法", "sig-1")
	if err != nil || again {
		t.Fatalf("同 ID 重跑必须判已存在：created=%v err=%v", again, err)
	}
	all, err := s.ListSalesDrafts(1, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("幂等失败：租户 1 应恰 1 条，实得 %d err=%v", len(all), err)
	}

	// 换个租户用另一个确定性 ID：各自独立，不互相幂等掉。
	if ok, err := s.CreateMinedDraft(2, "sdr_m_deadbeef_87654321", "anchor:2/s1", "源", "说法", "sig-2"); err != nil || !ok {
		t.Fatalf("租户 2 写入应成功：created=%v err=%v", ok, err)
	}
	if list2, _ := s.ListSalesDrafts(2, ""); len(list2) != 1 {
		t.Fatalf("租户 2 应有 1 条，实得 %d", len(list2))
	}
	if _, err := s.GetSalesDraft(1, "sdr_m_deadbeef_87654321"); err == nil {
		t.Fatal("跨租户按 ID 读到了别人的草稿")
	}

	// 非法入参：无租户／空 ID／空建议说法一律拒。
	for _, bad := range []struct {
		tid int64
		id  string
		sug string
	}{
		{0, "sdr_m_x", "说法"},
		{1, "", "说法"},
		{1, "sdr_m_x", "   "},
	} {
		if ok, err := s.CreateMinedDraft(bad.tid, bad.id, "a", "src", bad.sug, "sig"); err == nil || ok {
			t.Fatalf("非法入参未拒：%+v created=%v err=%v", bad, ok, err)
		}
	}
}
