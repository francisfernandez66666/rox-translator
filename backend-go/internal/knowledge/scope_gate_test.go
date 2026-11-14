package knowledge

import (
	"testing"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
	knowledgeStore "translator/internal/knowledge/store"
)

// KN3 跨租户查询 ⇒ 空（不泄露存在性），四张表各判一次。
// ★ 每表都配「本租户正向对照」：防止「永远返回空」的假实现骗过判据。
func TestKN3CrossTenantIsEmptyFourTables(t *testing.T) {
	conn := openKnSQLite(t, "scope")
	seedKnFourTables(t, conn, 1)

	for _, table := range contract.KnowledgeTableNames {
		// 跨租户（tid=2）⇒ 必须 0 行。
		n, err := knowledgeStore.ScopedCount(conn, db.DialectSQLite, table, 2)
		if err != nil {
			t.Fatalf("KN3 表 %s 跨租户查询报错: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("KN3 跨租户泄露：表 %s 用 tenant_id=2 查到 %d 行（去掉 tenant_id 条件 ⇒ M0285 在此红）", table, n)
		}
		// 正向对照（tid=1）⇒ 必须查得到，否则判据是「永远返回空」的空转锁。
		n1, err := knowledgeStore.ScopedCount(conn, db.DialectSQLite, table, 1)
		if err != nil || n1 == 0 {
			t.Fatalf("KN3 判据失明：表 %s 本租户也查不到（n1=%d err=%v）", table, n1, err)
		}
	}
	// 非法/匿名作用域 tid<=0 ⇒ fail-closed 回 0，绝不读全表。
	for _, table := range contract.KnowledgeTableNames {
		n, err := knowledgeStore.ScopedCount(conn, db.DialectSQLite, table, 0)
		if err != nil || n != 0 {
			t.Fatalf("KN3 tid<=0 必须 fail-closed 回 0：表 %s n=%d err=%v", table, n, err)
		}
	}
}
