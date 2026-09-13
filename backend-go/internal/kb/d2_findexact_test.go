// ============ 本文件职责说明 ============
// D2 回归（2026-09-12）：FindExact 的 SELECT 列必须与 scanRow 列数/列序对账——
// 旧实现漏选 pack_id（41 列 vs 扫 42 列），Scan 恒报错被上层当「无命中」吞掉，
// 精确命中链整条静默失效。
// =============================================
package kb

import (
	"testing"

	"translator/internal/db"
)

func TestD2FindExactColumnParity(t *testing.T) {
	k, err := Open(t.TempDir() + "/kb.db")
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer k.Close()
	// 共享过滤子句依赖的辅表（生产同库存在；测试补齐最小集）
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, industry TEXT DEFAULT '')",
		"CREATE TABLE IF NOT EXISTS kb_packages (id INTEGER PRIMARY KEY, tenant_id INTEGER, pack_type TEXT, code TEXT, priority INTEGER DEFAULT 0)",
		"INSERT INTO tenants (id, industry) VALUES (1,'general')",
	} {
		if _, e := db.Exec(k.RawDB(), db.CurrentDialect(), ddl); e != nil {
			t.Fatalf("辅表准备失败: %v", e)
		}
	}
	if _, e := k.SaveBack("服务器", map[string]string{"en": "server"}, "test", 1); e != nil {
		t.Fatalf("SaveBack 失败: %v", e)
	}
	r, e := k.FindExact("服务器", 1)
	if e != nil {
		t.Fatalf("FindExact 应命中（D2 列错位回归）: %v", e)
	}
	if r.Langs["en"] != "server" || r.ID == 0 {
		t.Fatalf("命中内容异常: %+v", r)
	}
	// 模糊子串链同口径回归（sharedFilterSQL 参数个数错误曾使整链静默失效）
	fs, e := k.FuzzyHits("服务", 10, 1)
	if e != nil {
		t.Fatalf("FuzzyHits 报错: %v", e)
	}
	if len(fs) == 0 {
		t.Fatal("FuzzyHits 应命中子串条目")
	}
}
