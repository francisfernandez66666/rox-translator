// ============ tenants.go · 职责说明 ============
// store 包租户域薄委托层。
// FirstActiveTenantID 等租户相关辅助查询。
// （IAM/租户子系统拆分后保留本文件维持历史调用方 import 路径稳定。）
// =============================================
package store

import "translator/internal/db"

// FirstActiveTenantID 返回最早创建的 active 租户 ID（平台上下文签发 Key 时的默认归属）。
func (s *Store) FirstActiveTenantID() int64 {
	var id int64
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT MIN(id) FROM tenants WHERE COALESCE(status,'active')='active'").Scan(&id)
	return id
}
