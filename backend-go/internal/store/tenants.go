// ============ tenants.go · 职责说明 ============
// store 包租户域薄委托层。
// FirstActiveTenantID 等租户相关辅助查询。
// （IAM/租户子系统拆分后保留本文件维持历史调用方 import 路径稳定。）
// =============================================
package store

import "translator/internal/db"

// FirstActiveTenantID 返回最早创建的 active 租户 ID。
// 用途：平台上下文（平台管理员）签发 API Key / 建立数据时默认归属的租户。
// 实现：取 tenants 表状态为 active（含未显式设置 status 的旧数据）的最小 id，
//
//	即首个激活租户；用 db.QueryRow + 当前方言（PostgreSQL/SQLite 兼容）执行。
func (s *Store) FirstActiveTenantID() int64 {
	var id int64
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT MIN(id) FROM tenants WHERE COALESCE(status,'active')='active'").Scan(&id)
	return id
}
