package knowledge

import (
	"context"
	"testing"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
)

// KN10 删表前反向搬迁演练：知识库 → 主库，逐行逐列等值（双向指纹）+ 跑第二遍行数不变。
// ★ 故意漏一列（M0313）会让目标库该列落 DEFAULT，指纹按 contract 全量列算 ⇒ 不等 ⇒ 返回 error ⇒ 此处红。
func TestKN10ReverseMigrateIdempotentAndColumnComplete(t *testing.T) {
	know := openKnSQLite(t, "rev_src")
	main := openKnSQLite(t, "rev_dst")
	seedKnFourTables(t, know, 1) // priority=7 是非默认哨兵

	rep, err := ReverseMigrate(context.Background(), know, db.DialectSQLite, main, db.DialectSQLite)
	if err != nil {
		t.Fatalf("KN10 反向搬迁失败（故意漏一列 ⇒ M0313 在此红）: %v", err)
	}
	if !rep.Matched {
		t.Fatalf("KN10 逐行逐列指纹不等")
	}
	// 逐列等值的一处显式读数：漏 priority 列时这里会读到 DEFAULT 0 而不是 7。
	var priority int
	if err := db.QueryRow(main, db.DialectSQLite,
		"SELECT priority FROM product_facts WHERE tenant_id=?", 1).Scan(&priority); err != nil {
		t.Fatalf("KN10 反向后读 priority 失败: %v", err)
	}
	if priority != 7 {
		t.Fatalf("KN10 反向漏列：priority=%d（应为非默认 7）", priority)
	}

	rep2, err := ReverseMigrate(context.Background(), know, db.DialectSQLite, main, db.DialectSQLite)
	if err != nil {
		t.Fatalf("KN10 第二遍反向失败: %v", err)
	}
	for _, table := range contract.KnowledgeTableNames {
		if rep2.Counts[table] != rep.Counts[table] {
			t.Fatalf("KN10 跑第二遍行数变了（幂等失效）: %s %d→%d", table, rep.Counts[table], rep2.Counts[table])
		}
	}
}
