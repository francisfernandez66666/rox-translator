// ============ 本文件职责中文说明 ============
// 二期 §5 词表配置层落库锁：租户档优先于平台档、显式空档不回落、跨租户删不到、
// 平台默认档不许由租户删、Upsert 整份覆盖并规范化、非法档位 fail-closed。
// ========================================
package store

import (
	"path/filepath"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// newLexiconTestStore 用文件库建 Store 并跑词表域迁移。
// 参数：t=测试句柄；返回：可用 Store。
func newLexiconTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "lexicon.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	if err := st.SalesLexiconMigrate(); err != nil {
		t.Fatalf("词表域迁移失败: %v", err)
	}
	return st
}

// X1：读序租户档 > 平台档 > 空；租户显式空档不许回落平台档。
func TestLexiconTenantOverridesPlatformAndFallsBack(t *testing.T) {
	st := newLexiconTestStore(t)
	const scene, lang, kind = "cross_border", "zh", "anchor_greeting"
	if err := st.UpsertLexicon(0, scene, lang, kind, []string{"你好", "您好"}); err != nil {
		t.Fatalf("平台档写入失败: %v", err)
	}
	words, err := st.LoadLexiconWords(1, scene, lang, kind)
	if err != nil || len(words) != 2 || words[0] != "你好" {
		t.Fatalf("X1：租户未配置时必须回落平台档，实得 %v err=%v", words, err)
	}
	if err := st.UpsertLexicon(1, scene, lang, kind, []string{"欢迎"}); err != nil {
		t.Fatalf("租户档写入失败: %v", err)
	}
	words, err = st.LoadLexiconWords(1, scene, lang, kind)
	if err != nil || len(words) != 1 || words[0] != "欢迎" {
		t.Fatalf("X1：租户档必须覆盖平台档，实得 %v err=%v", words, err)
	}
	if err := st.UpsertLexicon(1, scene, lang, kind, []string{}); err != nil {
		t.Fatalf("租户空档写入失败: %v", err)
	}
	words, err = st.LoadLexiconWords(1, scene, lang, kind)
	if err != nil || len(words) != 0 {
		t.Fatalf("X1：租户显式空档不许回落平台档，实得 %v err=%v", words, err)
	}
	words, err = st.LoadLexiconWords(2, scene, lang, kind)
	if err != nil || len(words) != 2 {
		t.Fatalf("X1：另一租户仍应回落平台档，实得 %v err=%v", words, err)
	}
}

// X2：列表只含本租户＋平台档；跨租户删除与删平台档都必须被拒。
func TestLexiconListIsTenantScopedAndDeleteCannotCross(t *testing.T) {
	st := newLexiconTestStore(t)
	if err := st.UpsertLexicon(0, "cross_border", "en", "anchor_intent", []string{"price"}); err != nil {
		t.Fatalf("平台档写入失败: %v", err)
	}
	if err := st.UpsertLexicon(1, "cross_border", "en", "anchor_intent", []string{"quote"}); err != nil {
		t.Fatalf("租户 1 写入失败: %v", err)
	}
	if err := st.UpsertLexicon(2, "cross_border", "en", "anchor_intent", []string{"moq"}); err != nil {
		t.Fatalf("租户 2 写入失败: %v", err)
	}
	rows, err := st.ListLexicon(1)
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("X2：列表必须恰好含本租户＋平台两档，实得 %d", len(rows))
	}
	for _, r := range rows {
		if r.TenantID != 0 && r.TenantID != 1 {
			t.Fatalf("X2：列表串进了别的租户: %+v", r)
		}
	}
	// 找到租户 1 与平台档的行号，验删除作用域。
	var tenantID, platformID int64
	for _, r := range rows {
		if r.TenantID == 1 {
			tenantID = r.ID
		} else {
			platformID = r.ID
		}
	}
	if tenantID == 0 || platformID == 0 {
		t.Fatal("X2 夹具不完整：缺租户档或平台档行号")
	}
	if ok, err := st.DeleteLexicon(tenantID, 2); err != nil || ok {
		t.Fatalf("X2：跨租户删除必须一行都删不到: ok=%v err=%v", ok, err)
	}
	if ok, err := st.DeleteLexicon(platformID, 1); err != nil || ok {
		t.Fatalf("X2：平台默认档不许被租户删: ok=%v err=%v", ok, err)
	}
	still, err := st.LoadLexiconWords(1, "cross_border", "en", "anchor_intent")
	if err != nil || len(still) != 1 || still[0] != "quote" {
		t.Fatalf("X2：越权删除后本租户数据必须原样还在，实得 %v err=%v", still, err)
	}
	if ok, err := st.DeleteLexicon(tenantID, 1); err != nil || !ok {
		t.Fatalf("X2：本租户自有档必须删得掉: ok=%v err=%v", ok, err)
	}
}

// X3：Upsert 整份覆盖＋规范化（去空白、去重、丢空串）；种子幂等不覆盖运营改动。
func TestLexiconUpsertNormalizesAndSeedIsIdempotent(t *testing.T) {
	st := newLexiconTestStore(t)
	const scene, lang, kind = "private_domain", "zh", "resistance_price"
	if err := st.UpsertLexicon(1, scene, lang, kind, []string{" 太贵 ", "太贵", "", " 再降 "}); err != nil {
		t.Fatalf("Upsert 失败: %v", err)
	}
	words, err := st.LoadLexiconWords(1, scene, lang, kind)
	if err != nil || len(words) != 2 || words[0] != "太贵" || words[1] != "再降" {
		t.Fatalf("X3：词表必须去空白去重丢空串，实得 %v err=%v", words, err)
	}
	if err := st.UpsertLexicon(1, scene, lang, kind, []string{"预算不够"}); err != nil {
		t.Fatalf("第二次 Upsert 失败: %v", err)
	}
	words, err = st.LoadLexiconWords(1, scene, lang, kind)
	if err != nil || len(words) != 1 || words[0] != "预算不够" {
		t.Fatalf("X3：Upsert 必须整份覆盖，实得 %v err=%v", words, err)
	}
	ok, err := st.SeedLexiconIfAbsent(1, scene, lang, kind, []string{"种子词"})
	if err != nil || ok {
		t.Fatalf("X3：已存在的档不许被种子覆盖: ok=%v err=%v", ok, err)
	}
	ok, err = st.SeedLexiconIfAbsent(0, scene, "en", kind, []string{"too expensive"})
	if err != nil || !ok {
		t.Fatalf("X3：新档种子必须种入: ok=%v err=%v", ok, err)
	}
	got, err := st.LoadLexiconWords(1, scene, "en", kind)
	if err != nil || len(got) != 1 {
		t.Fatalf("X3：平台档必须能被租户读到，实得 %v err=%v", got, err)
	}
}

// X4：非法档位与非法删除入参一律拒收。
func TestLexiconRejectsInvalidKeys(t *testing.T) {
	st := newLexiconTestStore(t)
	if err := st.UpsertLexicon(-1, "s", "zh", "k", []string{"x"}); err == nil {
		t.Fatal("X4：负租户档位必须拒收")
	}
	if err := st.UpsertLexicon(1, "", "zh", "k", []string{"x"}); err == nil {
		t.Fatal("X4：缺场景必须拒收")
	}
	if _, err := st.LoadLexiconWords(1, "s", "", "k"); err == nil {
		t.Fatal("X4：缺语种必须拒收")
	}
	if _, err := st.ListLexicon(0); err == nil {
		t.Fatal("X4：列表必须带有效租户")
	}
	if _, err := st.DeleteLexicon(0, 1); err == nil {
		t.Fatal("X4：缺行号必须拒收")
	}
}
