// ============ kbscrape_test.go · 职责说明 ============
// store 包「行业/语言文化包自动采集」待审池单元测试：
// 待审条目/安全句 hash 去重、批量写入、审批状态流转、断点续传键。
// =============================================
package store

import "testing"

// TestScrapeEntryHash 验证条目去重键稳定性与区分度。
func TestScrapeEntryHash(t *testing.T) {
	h1 := scrapeEntryHash("zh", "汽车", "en", "Automobile")
	h2 := scrapeEntryHash("zh", "汽车", "en", "Automobile")
	h3 := scrapeEntryHash("zh", "汽车", "en", "Car")
	if h1 != h2 {
		t.Fatalf("相同输入 hash 应一致: %s != %s", h1, h2)
	}
	if h1 == h3 {
		t.Fatal("不同译文 hash 应不同")
	}
}

// TestScrapePhraseHash 验证安全句去重键。
func TestScrapePhraseHash(t *testing.T) {
	h1 := scrapePhraseHash("en", "forbidden", "badword", "")
	h2 := scrapePhraseHash("en", "forbidden", "badword", "")
	if h1 != h2 {
		t.Fatalf("相同输入 hash 应一致: %s != %s", h1, h2)
	}
}

// TestStageEntriesBatchDedup 验证批量写入待审条目时 hash 去重（同内容只入池一次）。
func TestStageEntriesBatchDedup(t *testing.T) {
	s := newTestStore(t)
	items := []*KBStagedEntry{
		{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "汽车", TgtLang: "en", TgtText: "Automobile"},
		{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "汽车", TgtLang: "en", TgtText: "Automobile"}, // 重复
		{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "刹车", TgtLang: "en", TgtText: "Brake"},
	}
	n, err := s.StageEntriesBatch(items)
	if err != nil {
		t.Fatalf("StageEntriesBatch 失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("期望去重后新增 2 条，实际 %d", n)
	}
	// 再次写入同样的内容 → 全部去重
	n2, _ := s.StageEntriesBatch(items)
	if n2 != 0 {
		t.Fatalf("二次写入应全部去重，实际新增 %d", n2)
	}
}

// TestStagePhrasesBatchDedup 验证安全句批量去重。
func TestStagePhrasesBatchDedup(t *testing.T) {
	s := newTestStore(t)
	items := []*KBStagedPhrase{
		{PackageID: 1, Lang: "en", Kind: "forbidden", Phrase: "badword"},
		{PackageID: 1, Lang: "en", Kind: "forbidden", Phrase: "badword"}, // 重复
		{PackageID: 1, Lang: "en", Kind: "replace", Phrase: "cheap", Replacement: "affordable"},
	}
	n, err := s.StagePhrasesBatch(items)
	if err != nil {
		t.Fatalf("StagePhrasesBatch 失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("期望去重后新增 2 条，实际 %d", n)
	}
}

// TestSetStagedStatusApprove 验证审批状态流转：pending→approved（applied_at 写入）与还原 pending（applied_at 清空）。
// ★ 回归：此前把 "approve/reject" 直接传入导致状态永不更新（2026-09-03 修复），此处锁定 correct 值。
func TestSetStagedStatusApproveAndRestore(t *testing.T) {
	s := newTestStore(t)
	items := []*KBStagedEntry{
		{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "汽车", TgtLang: "en", TgtText: "Automobile"},
		{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "刹车", TgtLang: "en", TgtText: "Brake"},
	}
	if _, err := s.StageEntriesBatch(items); err != nil {
		t.Fatalf("StageEntriesBatch 失败: %v", err)
	}
	list, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if len(list) != 2 {
		t.Fatalf("待审应 2 条，实际 %d", len(list))
	}
	ids := []int64{list[0].ID, list[1].ID}
	// 通过与驳回（修复后传入 approved/rejected）
	n, err := s.SetStagedStatus("entries", ids, "approved")
	if err != nil || n != 2 {
		t.Fatalf("approve 应更新 2 行，实际 n=%d err=%v", n, err)
	}
	appr, _ := s.ListStagedEntries("", "approved", "", 0, 0)
	if len(appr) != 2 || appr[0].AppliedAt == "" {
		t.Fatalf("approved 状态或 applied_at 未生效: len=%d applied_at=%q", len(appr), appr[0].AppliedAt)
	}
	// 还原为待审：approved→pending，applied_at 清空
	n2, err := s.SetStagedStatus("entries", ids, "pending")
	if err != nil || n2 != 2 {
		t.Fatalf("restore 应更新 2 行，实际 n=%d err=%v", n2, err)
	}
	pend, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if len(pend) != 2 || pend[0].AppliedAt != "" {
		t.Fatalf("还原后应回到 pending 且 applied_at 清空: len=%d applied_at=%q", len(pend), pend[0].AppliedAt)
	}
	// 再通过应可流转（pending→approved）
	if n3, _ := s.SetStagedStatus("entries", ids, "approved"); n3 != 2 {
		t.Fatalf("还原后再次 approve 应更新 2 行，实际 %d", n3)
	}
	// 对已 approved 行直接 approve 不应再流转（仅 pending 可流转）
	if n4, _ := s.SetStagedStatus("entries", ids, "approved"); n4 != 0 {
		t.Fatalf("非 pending 行重复 approve 应 0 行，实际 %d", n4)
	}
}

// TestAutoApproveEntryEmbedsAndTrails 验证自动审批：直接落正式库 + 待审留 approved 痕迹。
// 回归：自动审批不上人工审核即可用（SaveEntry 入库 + ON CONFLICT 提升既有 pending 为 approved）。
func TestAutoApproveEntryEmbedsAndTrails(t *testing.T) {
	s := newTestStore(t)
	e := &KBStagedEntry{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "汽车", TgtLang: "en", TgtText: "Automobile", Tier: 3, Layer: 1}
	if err := s.AutoApproveEntry(1, e); err != nil {
		t.Fatalf("AutoApproveEntry 失败: %v", err)
	}
	// 正式库应有该条目
	entries, err := s.ListEntries(1, 1)
	if err != nil {
		t.Fatalf("ListEntries 失败: %v", err)
	}
	found := false
	for _, en := range entries {
		if en.SourceText == "汽车" && en.TargetLang == "en" {
			found = true
		}
	}
	if !found {
		t.Fatal("自动审批后正式库应包含该条术语")
	}
	// 待审留痕应为 approved（含 applied_at）
	appr, _ := s.ListStagedEntries("", "approved", "", 0, 0)
	if len(appr) != 1 || appr[0].SrcLang != "zh" || appr[0].AppliedAt == "" {
		t.Fatalf("应留 1 条 approved 痕迹: len=%d src_lang=%q applied_at=%q", len(appr), appr[0].SrcLang, appr[0].AppliedAt)
	}
}

// TestAutoApprovePhraseEmbedsAndTrails 验证自动审批安全句落库 + 痕迹。
func TestAutoApprovePhraseEmbedsAndTrails(t *testing.T) {
	s := newTestStore(t)
	p := &KBStagedPhrase{PackageID: 1, Lang: "en", Phrase: "cheap", Kind: "replace", Replacement: "affordable", Tier: 3}
	if err := s.AutoApprovePhrase(1, p); err != nil {
		t.Fatalf("AutoApprovePhrase 失败: %v", err)
	}
	appr, _ := s.ListStagedPhrases("approved", "", 0, 0)
	if len(appr) != 1 || appr[0].AppliedAt == "" {
		t.Fatalf("应留 1 条 approved 安全句痕迹: len=%d applied_at=%q", len(appr), appr[0].AppliedAt)
	}
}

// TestUpdateStagedEntrySrcLang 验证清洗历史误标源语言：更新 src_lang 并重算 hash。
func TestUpdateStagedEntrySrcLang(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.StageEntriesBatch([]*KBStagedEntry{{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "blended learning", TgtLang: "zh", TgtText: "混合式学习"}}); err != nil {
		t.Fatalf("StageEntriesBatch 失败: %v", err)
	}
	list, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if len(list) != 1 {
		t.Fatalf("待审应 1 条，实际 %d", len(list))
	}
	ok, err := s.UpdateStagedEntrySrcLang(list[0].ID, "en")
	if err != nil || !ok {
		t.Fatalf("UpdateStagedEntrySrcLang 应成功: ok=%v err=%v", ok, err)
	}
	after, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if after[0].SrcLang != "en" {
		t.Fatalf("源语言应更正为 en，实际 %q", after[0].SrcLang)
	}
	// hash 应随新源语言重算（不同于含 zh 的旧 hash）
	if after[0].SrcHash == scrapeEntryHash("zh", "blended learning", "zh", "混合式学习") {
		t.Fatal("源语言变更后 hash 应重算，不应等于旧 hash")
	}
}

// TestAutoApproveEntryAfterSrcLangChange 回归：源语言更正后再自动审批，不得因 stale hash 插入重复行。
// ★ 2026-09-03 修复：AutoApproveEntry 必须按当前字段重算 hash，否则 UpdateStagedEntrySrcLang
//   已更换 src_lang 后沿用旧 hash 会 INSERT 出重复 approved 行（实测 15818→18841 +3023 事故）。
func TestAutoApproveEntryAfterSrcLangChange(t *testing.T) {
	s := newTestStore(t)
	e := &KBStagedEntry{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "blended learning", TgtLang: "en", TgtText: "Blended Learning", Tier: 3, Layer: 1}
	if _, err := s.StageEntriesBatch([]*KBStagedEntry{e}); err != nil {
		t.Fatalf("StageEntriesBatch 失败: %v", err)
	}
	list, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if len(list) != 1 {
		t.Fatalf("待审应 1 条，实际 %d", len(list))
	}
	row := list[0]
	// 模拟 backfill：先更正源语言 zh→en（DB 内 src_lang/src_hash 已换新）
	if ok, err := s.UpdateStagedEntrySrcLang(row.ID, "en"); err != nil || !ok {
		t.Fatalf("UpdateStagedEntrySrcLang 应成功: ok=%v err=%v", ok, err)
	}
	// 用内存中仍旧局的字段（SrcHash 仍是旧的 zh hash）调 AutoApproveEntry → 不得产生第二行
	row.SrcLang = "en"
	if err := s.AutoApproveEntry(1, row); err != nil {
		t.Fatalf("AutoApproveEntry 失败: %v", err)
	}
	all, _ := s.ListStagedEntriesAll(100, 0)
	if len(all) != 1 {
		t.Fatalf("更正后自动审批应 1 行（不得重复插入），实际 %d 行", len(all))
	}
	if all[0].Status != "approved" {
		t.Fatalf("状态应为 approved，实际 %q", all[0].Status)
	}
	if all[0].SrcLang != "en" {
		t.Fatalf("源语言应为 en，实际 %q", all[0].SrcLang)
	}
	if all[0].SrcHash != scrapeEntryHash("en", "blended learning", "en", "Blended Learning") {
		t.Fatal("hash 应等于按 en 重算值")
	}
}

// TestUpdateStagedEntryContent 验证还原前编辑待审内容。
func TestUpdateStagedEntryContent(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.StageEntriesBatch([]*KBStagedEntry{{TargetPackID: 1, PackType: "locale", SrcLang: "zh", SrcText: "汽车", TgtLang: "en", TgtText: "Automobile"}}); err != nil {
		t.Fatalf("StageEntriesBatch 失败: %v", err)
	}
	list, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if len(list) != 1 {
		t.Fatalf("待审应 1 条，实际 %d", len(list))
	}
	ok, err := s.UpdateStagedEntryContent(list[0].ID, "汽车", "Car")
	if err != nil || !ok {
		t.Fatalf("UpdateStagedEntryContent 应成功: ok=%v err=%v", ok, err)
	}
	after, _ := s.ListStagedEntries("", "pending", "", 0, 0)
	if after[0].TgtText != "Car" {
		t.Fatalf("译文应更新为 Car，实际 %q", after[0].TgtText)
	}
}

// TestScrapeCheckpoint 验证断点续传键读写（system_config 持久化）。
func TestScrapeCheckpoint(t *testing.T) {
	s := newTestStore(t)
	date := "2026-09-01"
	sourceID := int64(7)
	if got := s.GetScrapeCheckpoint(date, sourceID); got != "" {
		t.Fatalf("初始游标应为空，实际 %q", got)
	}
	if err := s.SetScrapeCheckpoint(date, sourceID, "42"); err != nil {
		t.Fatalf("SetScrapeCheckpoint 失败: %v", err)
	}
	if got := s.GetScrapeCheckpoint(date, sourceID); got != "42" {
		t.Fatalf("游标读取失败，期望 42 实际 %q", got)
	}
	if s.SourceDone(date, sourceID) {
		t.Fatal("未标记完成时 SourceDone 应为 false")
	}
	if err := s.MarkSourceDone(date, sourceID); err != nil {
		t.Fatalf("MarkSourceDone 失败: %v", err)
	}
	if !s.SourceDone(date, sourceID) {
		t.Fatal("标记完成后 SourceDone 应为 true")
	}
}
