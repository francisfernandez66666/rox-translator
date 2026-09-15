// ============ orgmigrate_test.go · 职责说明 ============
// store 包内部测试文件（orgs 同级同名唯一约束迁移的回归测试）。
// =============================================
package store

import (
	"testing"

	"translator/internal/db"
)

// TestOrgSiblingUniqueMigrate 验证 P0-2 迁移的三段语义（内存 SQLite）：
//
//	① 幂等：重复调用不报错不重复去重；
//	② 去重：同级同名（含首尾空白变体）仅保留 id 最小行；跨父级/跨租户同名不受影响；
//	③ 约束生效：迁移后再次插入同级同名组织返回唯一冲突错误（IsUniqueViolation=true），
//	   API 层据此返回「创建失败：同级下已存在同名组织」——原死代码分支被激活。
func TestOrgSiblingUniqueMigrate(t *testing.T) {
	s := newTestStore(t) // 复用 store 包既有测试助手（建库+迁移链全跑，含本迁移）
	if db.CurrentDialect() != db.DialectSQLite {
		t.Skip("本用例跑 SQLite 方言（PG 分支由 UAT 主矩阵覆盖）")
	}
	// ---------- 场景铺设（迁移已生效，须先摘索引模拟「老库无约束」存量脏数据） ----------
	// 同租户同级三个「研发部」（一个带首尾空白变体）+ 一个「市场部」；另一父级下一个「研发部」
	// 老库模拟：摘掉唯一索引 → 铺重复行 → 重跑迁移验证「去重 + 重建约束」全链路。
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_orgs_sibling_unique`); err != nil {
		t.Fatalf("模拟老库（摘唯一索引）失败: %v", err)
	}
	ids := []struct {
		parent int64
		name   string
	}{
		{0, "研发部"}, {0, "研发部 "}, {0, "研发部"}, {0, "市场部"}, {7, "研发部"},
	}
	for i, x := range ids {
		if _, err := s.db.Exec(`INSERT INTO orgs (id, tenant_id, parent_id, name, type) VALUES (?,?,?,?, 'dept')`,
			i+1, 1, x.parent, x.name); err != nil {
			t.Fatalf("铺设组织行 %d 失败: %v", i+1, err)
		}
	}
	// ---------- ① 幂等：再跑一次迁移 ----------
	s.OrgSiblingUniqueMigrate()
	s.OrgSiblingUniqueMigrate()
	// ---------- ② 去重断言 ----------
	// 父级 0 下「研发部」三个变体应只剩 1 行（TRIM 归一后同名）；「市场部」不受影响；父级 7 的「研发部」保留
	var nSame0, nMkt, nSame7 int
	s.db.QueryRow(`SELECT COUNT(*) FROM orgs WHERE parent_id=0 AND TRIM(name)='研发部'`).Scan(&nSame0)
	s.db.QueryRow(`SELECT COUNT(*) FROM orgs WHERE parent_id=0 AND TRIM(name)='市场部'`).Scan(&nMkt)
	s.db.QueryRow(`SELECT COUNT(*) FROM orgs WHERE parent_id=7 AND TRIM(name)='研发部'`).Scan(&nSame7)
	if nSame0 != 1 {
		t.Fatalf("同级同名去重失败：parent=0 研发部期望 1 行（保留 id 最小），实际 %d 行", nSame0)
	}
	if nMkt != 1 {
		t.Fatalf("不应误删无关组织：市场部期望 1 行，实际 %d 行", nMkt)
	}
	if nSame7 != 1 {
		t.Fatalf("跨父级同名不受影响：parent=7 研发部期望 1 行，实际 %d 行", nSame7)
	}
	// ---------- ③ 约束生效断言 ----------
	// 迁移后（步骤①内已跑）插入同级同名必须触发唯一冲突
	_, err := s.db.Exec(`INSERT INTO orgs (tenant_id, parent_id, name, type) VALUES (1, 0, '研发部', 'dept')`)
	if err == nil {
		t.Fatal("唯一索引未生效：同级同名插入居然成功")
	}
	if !IsUniqueViolation(err) {
		t.Fatalf("期望唯一冲突错误（供 API 层转换为友好文案），实际: %v", err)
	}
	// 合法插入（不同名）仍成功
	if _, err := s.db.Exec(`INSERT INTO orgs (tenant_id, parent_id, name, type) VALUES (1, 0, '财务部', 'dept')`); err != nil {
		t.Fatalf("不同名插入不应被拦截: %v", err)
	}
}
