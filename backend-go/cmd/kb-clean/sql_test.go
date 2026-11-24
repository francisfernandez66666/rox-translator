// ============ sql_test.go · 职责说明 ============
// 清洗 job 的筛选锁：只清洗中文源 tier3 scrape 词条，用户上传/brand/tier1/tier2/空译文/非中文源一律排除；
// 断点跳过与 limit 生效；列名白名单防注入。用 SQLite 内存库，方言无关。
// ============================================================
package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/db"
)

func cleanFixture(t *testing.T) *sql.DB {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "clean.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	for _, ddl := range []string{
		`CREATE TABLE kb_pack_sources (id INTEGER PRIMARY KEY, tier INTEGER, lang TEXT)`,
		`CREATE TABLE kb_entries (id INTEGER PRIMARY KEY, tenant_id INTEGER, package_id INTEGER,
			layer INTEGER, source_lang TEXT, source_text TEXT, target_lang TEXT, target_text TEXT, module TEXT,
			vector BLOB, created_at TEXT, updated_at TEXT)`,
		`CREATE TABLE tm_segments (zh_hash TEXT, zh TEXT, tenant_id INTEGER, pack_id INTEGER, he TEXT, module TEXT)`,
	} {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatal(ddl, err)
		}
	}
	// 源：314=tier3(llm_gen)，100=tier1(official_api)
	_, _ = raw.Exec(`INSERT INTO kb_pack_sources(id,tier,lang) VALUES (314,3,'he'),(100,1,'he')`)
	// 词条：1=tier3 中文 scrape（清洗）；2=tier1 scrape（保留）；3=用户上传中文 module（保留）；
	// 4=brand（保留）；5=migrated（保留）；6=tier3 但空译文（跳过）；7=tier3 中文 ru（清洗）；
	// 8/9=tier3 但源语言为 he/en（必须排除，不能当中文词重译）。
	rows := [][]interface{}{
		{1, 0, 18, 1, "zh", "刹车系统", "he", "מערכת ברקים", "scrape:314"},
		{2, 0, 18, 1, "zh", "汽车", "he", "מכונית", "scrape:100"},
		{3, 1, 1, 1, "zh", "电池管理", "he", "ניהול סוללה", "电池管理"},
		{4, 1, 1, 1, "zh", "奇瑞", "he", "צ'רי", "brand"},
		{5, 0, 18, 1, "zh", "订单", "he", "הזמנה", "migrated"},
		{6, 0, 18, 1, "zh", "空值", "he", "", "scrape:314"},
		{7, 0, 18, 1, "zh", "安全气囊", "ru", "подушка", "scrape:314"},
		{8, 0, 18, 1, "he", "משתמש", "he", "משתמש", "scrape:314"},
		{9, 0, 18, 1, "en", "manual", "he", "חוברת", "scrape:314"},
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

func TestLoadCleanEntriesReturnsScanError(t *testing.T) {
	raw := cleanFixture(t)
	if _, err := raw.Exec(`UPDATE kb_entries SET target_text=NULL WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCleanEntries(raw, "he", 0, nil); err == nil {
		t.Fatal("损坏行扫描必须报错，不能静默遗漏")
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

func TestSyncTMSegmentWritesBackHeap(t *testing.T) {
	raw := cleanFixture(t)
	entry := cleanEntry{TgtLang: "he", SrcText: "粉丝", PackageID: 25, Module: "scrape:542"}
	_, _ = raw.Exec(`INSERT INTO tm_segments(zh_hash,zh,tenant_id,pack_id,he,module) VALUES(?,?,?,?,?,?)`,
		md5Hex("粉丝"), "粉丝", 0, 25, "קהל עוקבים", "scrape:534")
	syncTMSegment(raw, entry, "מעריצים")
	var he, module string
	if err := raw.QueryRow(`SELECT he,module FROM tm_segments WHERE zh_hash=? AND pack_id=?`, md5Hex("粉丝"), 25).Scan(&he, &module); err != nil {
		t.Fatal(err)
	}
	if he != "מעריצים" {
		t.Fatalf("TM he 列必须同步为新译文，实得 %q", he)
	}
	if module != "scrape:542" {
		t.Fatalf("TM module 应同步，实得 %q", module)
	}
}

func TestSyncTMSegmentReturnsWriteError(t *testing.T) {
	raw := cleanFixture(t)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	entry := cleanEntry{TenantID: 7, SrcText: "粉丝", PackageID: 25, Module: "scrape:542", TgtLang: "he"}
	if err := syncTMSegment(raw, entry, "מעריצים"); err == nil {
		t.Fatal("TM 写失败必须返回错误，不能静默报成功")
	}
}

func TestClearTMSegmentIgnoresModuleMismatch(t *testing.T) {
	raw := cleanFixture(t)
	entry := cleanEntry{TgtLang: "he", SrcText: "流量统计", PackageID: 25, Module: "scrape:542"}
	// tm 里 module 是别的源 ID（实测形态），仍必须清掉
	_, _ = raw.Exec(`INSERT INTO tm_segments(zh_hash,zh,tenant_id,pack_id,he,module) VALUES(?,?,?,?,?,?)`,
		md5Hex("流量统计"), "流量统计", 0, 25, "מדידה", "scrape:454")
	clearTMSegment(raw, entry)
	var he string
	_ = raw.QueryRow(`SELECT he FROM tm_segments WHERE zh_hash=? AND pack_id=?`, md5Hex("流量统计"), 25).Scan(&he)
	if he != "" {
		t.Fatalf("module 不匹配也必须按唯一键清空，实得 %q", he)
	}
}

// 重复自然键行存在时，清洗只能按当前 id 更新，不能误改另一条同源译文。
func TestUpdateKBEntryByIDNotNaturalKey(t *testing.T) {
	raw := cleanFixture(t)
	_, err := raw.Exec(`INSERT INTO kb_entries(id,tenant_id,package_id,layer,source_lang,source_text,target_lang,target_text,module)
		VALUES(10,0,18,1,'zh','粉丝','he','旧A','scrape:314'),(11,0,18,1,'zh','粉丝','he','旧B','scrape:314')`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	e := cleanEntry{ID: 11, TenantID: 0, PackageID: 18, Layer: 1, SrcLang: "zh", SrcText: "粉丝", TgtLang: "he", TgtText: "旧B", Module: "scrape:314"}
	if err := updateKBEntryTx(tx, e, "新B"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var a, b string
	if err := raw.QueryRow(`SELECT target_text FROM kb_entries WHERE id=10`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT target_text FROM kb_entries WHERE id=11`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if a != "旧A" || b != "新B" {
		t.Fatalf("按 id 更新失效：id1=%q id2=%q", a, b)
	}
}

// KB 更新失败时 TM 必须随事务回滚，不能留下新 TM + 旧 KB 的半态。
func TestApplyCleanChangeTxRollsBackTMOnKBFailure(t *testing.T) {
	raw := cleanFixture(t)
	e := cleanEntry{ID: 1, TenantID: 0, PackageID: 18, Layer: 1, SrcLang: "zh", SrcText: "刹车系统", TgtLang: "he", TgtText: "מערכת ברקים", Module: "scrape:314"}
	_, err := raw.Exec(`INSERT INTO tm_segments(zh_hash,zh,tenant_id,pack_id,he,module) VALUES(?,?,?,?,?,?)`,
		md5Hex(e.SrcText), e.SrcText, 0, e.PackageID, e.TgtText, e.Module)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`CREATE TRIGGER fail_kb_update BEFORE UPDATE OF target_text ON kb_entries
		BEGIN SELECT RAISE(ABORT,'boom'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyCleanChangeTx(context.Background(), raw, e, "מערכת בלמים", false); err == nil {
		t.Fatal("KB 失败必须让整个事务报错")
	}
	var kb, tm string
	if err := raw.QueryRow(`SELECT target_text FROM kb_entries WHERE id=1`).Scan(&kb); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow(`SELECT he FROM tm_segments WHERE zh_hash=?`, md5Hex(e.SrcText)).Scan(&tm); err != nil {
		t.Fatal(err)
	}
	if kb != e.TgtText || tm != e.TgtText {
		t.Fatalf("事务未回滚：kb=%q tm=%q", kb, tm)
	}
}

// 删除备份必须含完整原始行，含向量与时间戳，才能按 id 恢复。
func TestLoadCleanEntrySnapshotCarriesRestoreFields(t *testing.T) {
	raw := cleanFixture(t)
	vec := []byte{1, 2, 3, 4}
	_, err := raw.Exec(`UPDATE kb_entries SET vector=?,created_at=?,updated_at=? WHERE id=1`, vec, "2026-01-01", "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	e := cleanEntry{ID: 1, TenantID: 0, PackageID: 18, Layer: 1, SrcLang: "zh", SrcText: "刹车系统", TgtLang: "he", TgtText: "מערכת ברקים", Module: "scrape:314"}
	s, err := loadCleanEntrySnapshot(raw, e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.Vector, vec) || s.CreatedAt != "2026-01-01" || s.UpdatedAt != "2026-01-02" || s.PackageID != 18 {
		t.Fatalf("快照缺恢复字段：%+v", s)
	}
}

// TM 唯一键未命中时必须报错，不能 0 行也提交 KB 并落断点。
func TestTMSegmentRequiresOneRow(t *testing.T) {
	raw := cleanFixture(t)
	e := cleanEntry{PackageID: 999, SrcText: "不存在", TgtLang: "he", Module: "scrape:314"}
	if err := syncTMSegment(raw, e, "新译文"); err == nil {
		t.Fatal("TM 同步 0 行必须报错")
	}
	if err := clearTMSegment(raw, e); err == nil {
		t.Fatal("TM 清理 0 行必须报错")
	}
}

// 并发方只改原文时，旧清洗快照不得覆盖新行。
func TestUpdateKBEntryRejectsSourceChange(t *testing.T) {
	raw := cleanFixture(t)
	e := cleanEntry{ID: 1, TenantID: 0, PackageID: 18, Layer: 1, SrcLang: "zh", SrcText: "刹车系统", TgtLang: "he", TgtText: "מערכת ברקים", Module: "scrape:314"}
	if _, err := raw.Exec(`UPDATE kb_entries SET source_text='新原文' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := updateKBEntryTx(tx, e, "不得覆盖"); err == nil {
		t.Fatal("源文已变化时必须拒绝更新")
	}
}
