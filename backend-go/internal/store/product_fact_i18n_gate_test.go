// ============ product_fact_i18n_gate_test.go · 职责说明 ============
// A13c / A15：主库 product_facts 补列 + product_fact_i18n 建表幂等；译文预生成
// 「同 hash 幂等不重复计费 / hash 变更重生成 / 运营手工档不被冲掉 / 中文档不打 LLM」；
// 热路径（assist_open_proxy.go + assist_sales_facts.go）零 SingleLangTranslate 调用源码扫描。
// ================================================
package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/kb"
)

// pinFactI18nSQLite 固定测试方言（config.C 全局在用例结束还原）。
func pinFactI18nSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newFactI18nStore 文件库建全量 Store 并单独跑本域迁移（不依赖主 agent 已挂 store.go 域链）。
func newFactI18nStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	pinFactI18nSQLite(t)
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "fact.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	// ★ KN11：主库四表已从生产启动链摘除，测试库需独立重建，ProductFactI18nMigrate 的
	//   EnsureColumns(product_facts, lang/priority) 与建 product_fact_i18n 才能跑通。
	migrateProductTablesTest(t, st)
	if err := st.ProductFactI18nMigrate(); err != nil {
		t.Fatalf("ProductFactI18nMigrate 失败: %v", err)
	}
	return st, conn
}

// countingFactTranslator 假翻译腿：只记调用次数，不真打 LLM。
type countingFactTranslator struct {
	calls int
	langs []string
}

func (c *countingFactTranslator) SingleLangTranslate(_ context.Context, zhText, targetLang string, _ []*kb.Row, _, _ string) (string, error) {
	c.calls++
	c.langs = append(c.langs, targetLang)
	return zhText + "[" + targetLang + "]", nil
}

// TestProductFactI18nMigrateIdempotent 补列/建表跑两次幂等，lang+priority 到位、表在位。
func TestProductFactI18nMigrateIdempotent(t *testing.T) {
	st, conn := newFactI18nStore(t)
	if err := st.ProductFactI18nMigrate(); err != nil {
		t.Fatalf("二次迁移不幂等: %v", err)
	}
	d := db.CurrentDialect()
	for _, col := range []string{"lang", "priority"} {
		ok, err := db.HasColumn(conn, d, "product_facts", col)
		if err != nil || !ok {
			t.Fatalf("product_facts 缺 %s 列: ok=%v err=%v", col, ok, err)
		}
	}
	var n int
	err := db.QueryRow(conn, d,
		"SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?", "product_fact_i18n").Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("product_fact_i18n 表未建: n=%d err=%v", n, err)
	}
}

// TestProductFactI18nPreGenerateIdempotent 12 语种齐全、中文不打 LLM、同 hash 重跑不重复计费。
func TestProductFactI18nPreGenerateIdempotent(t *testing.T) {
	st, conn := newFactI18nStore(t)
	tr := &countingFactTranslator{}
	ctx := context.Background()
	if err := st.PreGenerateFactI18n(ctx, 1, 100, "质保 30 天", tr); err != nil {
		t.Fatalf("预生成失败: %v", err)
	}
	if tr.calls != 11 {
		t.Fatalf("12 语种中 zh 走 identity 不打 LLM，期望 11 次，实际 %d", tr.calls)
	}
	var cnt int
	if err := db.QueryRow(conn, db.CurrentDialect(),
		"SELECT COUNT(1) FROM product_fact_i18n WHERE tenant_id=? AND fact_id=?", 1, 100).Scan(&cnt); err != nil || cnt != 12 {
		t.Fatalf("译文行数应为 12，实际 %d (err=%v)", cnt, err)
	}
	zh, ok := st.factI18nRow(1, 100, "zh")
	if !ok || zh.Value != "质保 30 天" || zh.Engine != "identity" {
		t.Fatalf("中文档应为原文自身且 engine=identity: %+v", zh)
	}
	before := tr.calls
	if err := st.PreGenerateFactI18n(ctx, 1, 100, "质保 30 天", tr); err != nil {
		t.Fatalf("同 hash 重跑失败: %v", err)
	}
	if tr.calls != before {
		t.Fatalf("同 hash 重跑不应再打 LLM（幂等不重复计费）: before=%d after=%d", before, tr.calls)
	}
}

// TestProductFactI18nPreGenerateHashRegen 原文变更（hash 变）⇒ 译文失效重生成（M0239）。
func TestProductFactI18nPreGenerateHashRegen(t *testing.T) {
	st, _ := newFactI18nStore(t)
	tr := &countingFactTranslator{}
	ctx := context.Background()
	if err := st.PreGenerateFactI18n(ctx, 1, 101, "质保 30 天", tr); err != nil {
		t.Fatalf("首次预生成失败: %v", err)
	}
	oldRow, _ := st.factI18nRow(1, 101, "en")
	if err := st.PreGenerateFactI18n(ctx, 1, 101, "质保 20 天", tr); err != nil {
		t.Fatalf("变更后预生成失败: %v", err)
	}
	newRow, _ := st.factI18nRow(1, 101, "en")
	if tr.calls != 22 {
		t.Fatalf("hash 变更应全 11 语种重生成，期望累计 22 次，实际 %d", tr.calls)
	}
	if oldRow.SourceHash == newRow.SourceHash {
		t.Fatal("原文变更后 source_hash 必须变化（译文失效）")
	}
	if !strings.Contains(newRow.Value, "20 天") {
		t.Fatalf("译文应按新原文重生成: %q", newRow.Value)
	}
}

// TestProductFactI18nManualRowProtected 运营手工档永不被后台预生成覆盖（M0240）。
func TestProductFactI18nManualRowProtected(t *testing.T) {
	st, conn := newFactI18nStore(t)
	tr := &countingFactTranslator{}
	ctx := context.Background()
	if err := st.PreGenerateFactI18n(ctx, 1, 102, "质保 30 天", tr); err != nil {
		t.Fatalf("首次预生成失败: %v", err)
	}
	if _, err := db.Exec(conn, db.CurrentDialect(),
		"UPDATE product_fact_i18n SET value=?, engine=? WHERE tenant_id=? AND fact_id=? AND lang=?",
		"人工译文", factI18nEngineManual, 1, 102, "en"); err != nil {
		t.Fatalf("改手工档失败: %v", err)
	}
	if err := st.PreGenerateFactI18n(ctx, 1, 102, "质保 20 天", tr); err != nil {
		t.Fatalf("变更后预生成失败: %v", err)
	}
	row, _ := st.factI18nRow(1, 102, "en")
	if row.Value != "人工译文" || row.Engine != factI18nEngineManual {
		t.Fatalf("运营手工档被后台冲掉: %+v", row)
	}
}

// TestProductFactI18nHotPathZeroLLM 源码扫描：代理层热路径绝不出现 SingleLangTranslate 调用（A13c）。
func TestProductFactI18nHotPathZeroLLM(t *testing.T) {
	for _, f := range []string{
		filepath.Join("..", "api", "assist_open_proxy.go"),
		filepath.Join("..", "api", "assist_sales_facts.go"),
	} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s: %v", f, err)
		}
		if strings.Contains(string(src), "SingleLangTranslate(") {
			t.Fatalf("热路径文件 %s 出现 SingleLangTranslate 调用（译文只在写时预生成）", f)
		}
	}
}
