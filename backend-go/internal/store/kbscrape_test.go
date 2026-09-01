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
