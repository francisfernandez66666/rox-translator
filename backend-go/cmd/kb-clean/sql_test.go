// ============ sql_test.go · 职责说明 ============
// 清洗 job 的筛选锁：只清洗 tier3 scrape 词条，用户上传/brand/tier1/tier2/空译文一律排除；
// 断点跳过与 limit 生效；列名白名单防注入。用 SQLite 内存库，方言无关。
// ============================================================
package main

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/db"
)

func cleanFixture(t *testing.T) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	for _, ddl := range []string{
		`CREATE TABLE kb_pack_sources (id INTEGER PRIMARY KEY, tier INTEGER, lang TEXT)`,
		`CREATE TABLE kb_entries (id INTEGER PRIMARY KEY, tenant_id INTEGER, package_id INTEGER,
			layer INTEGER, source_lang TEXT, source_text TEXT, target_lang TEXT, target_text TEXT, module TEXT)`,
		`CREATE TABLE tm_segments (zh_hash TEXT, zh TEXT, tenant_id INTEGER, pack_id INTEGER, he TEXT, module TEXT)`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatal(ddl, err)
		}
	}
	// 源：314=tier3(llm_gen)，100=tier1(official_api)
	_, _ = raw.Exec(`INSERT INTO kb_pack_sources(id,tier,lang) VALUES (314,3,'he'),(100,1,'he')`)
	// 词条：1=tier3 scrape（清洗）；2=tier1 scrape（保留）；3=用户上传中文 module（保留）；
	// 4=brand（保留）；5=migrated（保留）；6=tier3 但空译文（跳过）
	rows := [][]interface{}{
		{1, 0, 18, 1, "zh", "刹车系统", "he", "מערכת ברקים", "scrape:314"},
		{2, 0, 18, 1, "zh", "汽车", "he", "מכונית", "scrape:100"},
		{3, 1, 1, 1, "zh", "电池管理", "he", "ניהול סוללה", "电池管理"},
		{4, 1, 1, 1, "zh", "奇瑞", "he", "צ'רי", "brand"},
		{5, 0, 18, 1, "zh", "订单", "he", "הזמנה", "migrated"},
		{6, 0, 18, 1, "zh", "空值", "he", "", "scrape:314"},
		{7, 0, 18, 1, "zh", "安全气囊", "ru", "подушка", "scrape:314"},
	}
	for _, r := range rows {
		if _, err := db.Exec(raw, db.CurrentDialect(),
			`INSERT INTO kb_entries(id,tenant_id,package_id,layer,source_lang,source_text,target_lang,target_text,module) VALUES(?,?,?,?,?,?,?,?,?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
	return raw
}

func TestLoadCleanEntriesOnlyTier3Scrape(t *testing.T) {
	raw := cleanFixture(t)
	got, err := loadCleanEntries(raw, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]bool{1: true, 7: true} // tier3 he + tier3 ru；其余全排除
	if len(got) != len(want) {
		t.Fatalf("条数不符: got=%d want=%d (%+v)", len(got), len(want), got)
	}
	for _, e := range got {
		if !want[e.ID] {
			t.Fatalf("不该清洗 id=%d module=%s", e.ID, e.Module)
		}
	}
}

func TestLoadCleanEntriesLangFilter(t *testing.T) {
	raw := cleanFixture(t)
	got, err := loadCleanEntries(raw, "he", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("lang=he 应只剩 id=1，实得 %+v", got)
	}
}

func TestLoadCleanEntriesCheckpointAndLimit(t *testing.T) {
	raw := cleanFixture(t)
	got, err := loadCleanEntries(raw, "", 1, map[int64]bool{1: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != 7 {
		t.Fatalf("断点跳过1后 limit=1 应得 id=7，实得 %+v", got)
	}
}

func TestClearTMSegmentGuard(t *testing.T) {
	raw := cleanFixture(t)
	// 非法列名不得写（防注入）：直接调函数不应 panic/报错，且 he 列未被清。
	_, _ = raw.Exec(`INSERT INTO tm_segments(zh_hash,zh,tenant_id,pack_id,he,module) VALUES(?,?,?,?,?,?)`,
		md5Hex("刹车系统"), "刹车系统", 0, 18, "מערכת ברקים", "scrape:314")
	clearTMSegment(raw, cleanEntry{TgtLang: "he; DROP TABLE tm_segments", SrcText: "刹车系统", PackageID: 18, Module: "scrape:314"})
	var he string
	if err := raw.QueryRow(`SELECT he FROM tm_segments WHERE zh_hash=?`, md5Hex("刹车系统")).Scan(&he); err != nil {
		t.Fatalf("非法列名清 tm_segments 应被白名单拦下，表却受影响: %v", err)
	}
	// 合法列名正常清空
	clearTMSegment(raw, cleanEntry{TgtLang: "he", SrcText: "刹车系统", PackageID: 18, Module: "scrape:314"})
	_ = raw.QueryRow(`SELECT he FROM tm_segments WHERE zh_hash=?`, md5Hex("刹车系统")).Scan(&he)
	if he != "" {
		t.Fatalf("合法 he 列应被清空，实得 %q", he)
	}
}
