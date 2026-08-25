package store


// FirstActiveTenantID 返回最早创建的 active 租户 ID（平台上下文签发 Key 时的默认归属）。
func (s *Store) FirstActiveTenantID() int64 {
	var id int64
	_ = s.db.QueryRow("SELECT MIN(id) FROM tenants WHERE COALESCE(status,'active')='active'").Scan(&id)
	return id
}
