// ============ 本文件职责中文说明 ============
// GDPR 数据主权支持：租户数据全量导出（ExportTenantData）与租户数据彻底清除（EraseTenantData）。
// 导出的数据包含用户（脱敏去掉密码哈希）、订单、发票、用量、审计日志、知识库包与条目、
// 以及脱敏的 API Key 前缀；清除则遍历所有带 tenant_id 的业务表逐一删除。
// =============================================
package store

import (
	"os"
	"translator/internal/db"
)

// ============ 租户数据主权：导出 / 删除（GDPR） ============

// ExportTenantData 导出租户全部业务数据（JSON 字符串），供客户数据主权下载。
// 参数：tid=租户 ID；返回 map 结构（各业务数据按 key 分组，如 users/orders/invoices/usage/audit 等）。
func (s *Store) ExportTenantData(tid int64) (map[string]interface{}, error) {
	out := map[string]interface{}{}

	// 用户（不含密码哈希）
	users, err := s.ListUsers(tid)
	if err == nil {
		clean := []interface{}{}
		for _, u := range users {
			// 脱敏：只导出非敏感字段，剔除 password_hash
			clean = append(clean, map[string]interface{}{
				"id": u.ID, "username": u.Username, "display_name": u.DisplayName,
				"role": u.Role, "status": u.Status, "created_at": u.CreatedAt,
			})
		}
		out["users"] = clean
	}

	// 充值订单
	if orders, err := s.ListOrders(tid); err == nil {
		out["orders"] = orders
	}

	// 发票
	if inv, err := s.ListInvoices(tid); err == nil {
		out["invoices"] = inv
	}

	// 用量明细
	if ledger, err := s.UsageLedgerList(tid, 100000, 0); err == nil {
		out["usage"] = ledger
	}

	// 审计日志
	if logs, err := s.ListAuditFilter(tid, "", "", 0, "", "", 100000); err == nil {
		out["audit"] = logs
	}

	// 知识库包与条目
	if pkgs, err := s.ListPackages(tid); err == nil {
		out["kb_packages"] = pkgs
		entries := []interface{}{}
		for _, p := range pkgs {
			// 逐包导出其下全部条目
			es, err := s.ListEntries(tid, p.ID)
			if err == nil {
				for _, e := range es {
					entries = append(entries, e)
				}
			}
		}
		out["kb_entries"] = entries
	}

	// API Key（脱敏：仅前缀）
	if keys, err := s.ListAPIKeys(tid); err == nil {
		masked := []interface{}{}
		for _, k := range keys {
			// 脱敏：只导出 ID/名称/前缀/权限/状态，不导出哈希
			masked = append(masked, map[string]interface{}{
				"id": k.ID, "name": k.Name, "key_prefix": k.KeyPrefix, "perms": k.Perms, "status": k.Status,
			})
		}
		out["api_keys"] = masked
	}

	return out, nil
}

// EraseTenantData 删除租户全部业务数据（GDPR 数据清除）。
// 参数：tid=租户 ID；注意：tenants 表本身由 tenant.Store 管理，由调用方负责。
//
// ★ 清单补全（2026-08-26 全仓评审 C5）：原清单漏 12 张表——
//
//	referral_rewards（含被邀邮箱快照，个人数据）/ tm_review / tm_hit_count /
//	feedbacks / notifications / eval_records / ticket_files / ticket_state /
//	output_artifacts / quota_grants / balance_accounts / jobs。
//	并追加磁盘产物清理（工单源文件与译文产物 best-effort 删除）。
func (s *Store) EraseTenantData(tid int64) error {
	tables := []string{
		"users", "kb_entries", "kb_packages", "kb_safety_phrases",
		"orders", "payments", "invoices", "api_keys",
		"usage_ledger", "audit_logs", "alerts", "tickets",
		// ↓ 2026-08-26 C5 补全
		"referral_rewards", "tm_review", "tm_hit_count", "feedbacks",
		"notifications", "eval_records", "ticket_files", "ticket_state",
		"output_artifacts", "quota_grants", "balance_accounts", "jobs",
	}
	for _, t := range tables {
		// 只删除明确带 tenant_id 列的表；忽略不存在的表
		if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM "+t+" WHERE tenant_id=?", tid); err != nil {
			// 表可能不存在，忽略
			continue
		}
	}
	// 邀请码：删除绑定该租户的邀请码
	_, _ = db.Exec(s.db, db.CurrentDialect(), "DELETE FROM invite_codes WHERE tenant_id=?", tid)
	return nil
}

// collectTenantArtifactPaths 收集租户全部工单相关磁盘文件路径（擦除前调用）。
func (s *Store) collectTenantArtifactPaths(tid int64) []string {
	paths := []string{}
	rows, err := db.Query(s.db, db.CurrentDialect(), `SELECT COALESCE(file_path,''), COALESCE(result_path,'') FROM tickets WHERE tenant_id=?`, tid)
	if err == nil {
		for rows.Next() {
			var fp, rp string
			if rows.Scan(&fp, &rp) == nil {
				if fp != "" {
					paths = append(paths, fp)
				}
				if rp != "" {
					paths = append(paths, rp)
				}
			}
		}
		rows.Close()
	}
	rows2, err := db.Query(s.db, db.CurrentDialect(), `SELECT COALESCE(file_path,''), COALESCE(result_path,'') FROM ticket_files WHERE tenant_id=?`, tid)
	if err == nil {
		for rows2.Next() {
			var fp, rp string
			if rows2.Scan(&fp, &rp) == nil {
				if fp != "" {
					paths = append(paths, fp)
				}
				if rp != "" {
					paths = append(paths, rp)
				}
			}
		}
		rows2.Close()
	}
	return paths
}

// EraseTenantDataFull 擦除前置：先收集磁盘路径 → 删表 → 删文件（完整版入口）。
// API 层原调 EraseTenantData 处改调本方法即可获得文件清理能力。
func (s *Store) EraseTenantDataFull(tid int64) error {
	files := s.collectTenantArtifactPaths(tid)
	if err := s.EraseTenantData(tid); err != nil {
		return err
	}
	removeFilesBestEffort(files)
	return nil
}

// removeFilesBestEffort 逐个删除文件（忽略错误；目录内残留子目录交由产物留存期扫描兜底）。
func removeFilesBestEffort(paths []string) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		_ = os.Remove(p)
	}
}

// ListPackages 租户 KB 包列表。
// 参数：tid=租户 ID；返回该租户全部知识库包（KBPackage 定义于 kbpackages.go）。
func (s *Store) ListPackages(tid int64) ([]*KBPackage, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+kbPkgCols+" FROM kb_packages WHERE tenant_id=?", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackage
	for rows.Next() {
		var p KBPackage
		if err := rows.Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.OrgID, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}
