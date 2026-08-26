// ============ 本文件职责中文说明 ============
// PackScope 检索作用域的调试型单元测试（临时库种子数据场景）。
// ============================================
package store

import "testing"

func TestDbgProbe2(t *testing.T) {
	env := newScopeEnv(t)
	rows, _ := env.st.db.Query("SELECT id, code, org_id, COALESCE(share_cross_dept,-1) FROM kb_packages WHERE tenant_id=2 ORDER BY id")
	for rows.Next() {
		var id, org, sh int64
		var code string
		rows.Scan(&id, &code, &org, &sh)
		t.Logf("pkg id=%d code=%s org=%d share=%d", id, code, org, sh)
	}
	rows.Close()
	var cnt int
	env.st.db.QueryRow("SELECT COUNT(*) FROM tenants").Scan(&cnt)
	t.Logf("tenants rows=%d", cnt)
}
