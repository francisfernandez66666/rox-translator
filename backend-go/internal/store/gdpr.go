// ============ gdpr.go · 职责说明 ============
// store 包 GDPR 数据主权支持。
// 租户数据全量导出（ExportTenantData）与租户数据彻底清除（EraseTenantData）。
// 导出的数据包含用户（脱敏去掉密码哈希）、订单、发票、用量、审计日志、知识库包与条目、
// 以及脱敏的 API Key 前缀；清除则遍历所有带 tenant_id 的业务表逐一删除。
// ★ 用量与审计两条腿走本文件的**游标全量取数**（usageLedgerForSubjectExport /
// auditLogsForSubjectExport），不复用前台分页接口——那两处的 limit 上限是防拖库闸，
// 依法交出的副本不能踩闸被悄悄截断（详见文件内 092x 段的取舍说明）。
// =============================================
package store

import (
	"fmt"
	"math"
	"os"
	"time"
	"translator/internal/db"
)

// ============ 租户数据主权：导出 / 删除（GDPR） ============

// ExportTenantData 导出租户全部业务数据（JSON 结构），供客户数据主权下载。
// 参数：tid=租户 ID（必须 >0）；返回 map 结构（各业务数据按 key 分组，如 users/orders/invoices/usage/audit 等）。
//
// ★ 092x（2026-09-29）两条口径改动：
//  1. 用量／审计两条腿改走本文件的游标全量取数，且**失败即返回 error**（旧写法 `if x, err := …; err == nil`
//     把取数失败和「这个租户就没有数据」合成同一个出栈形态，客户拿到的是少了整段的包却看不出来）；
//  2. tid<=0 直接拒——audit_logs.tenant_id=0 是平台级操作，
//     放行 0 等于把「导出某个租户的数据主权包」变成「导出平台自己的审计流水」。
func (s *Store) ExportTenantData(tid int64) (map[string]interface{}, error) {
	if tid <= 0 {
		return nil, fmt.Errorf("非法租户 ID %d：数据主权导出只按租户边界取数", tid)
	}
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

	// 用量明细（★ 092x：游标全量；取不全即报错，不再交残缺副本）
	ledger, err := s.usageLedgerForSubjectExport(tid)
	if err != nil {
		return nil, fmt.Errorf("用量流水导出失败：%w", err)
	}
	out["usage"] = ledger

	// 审计日志（同上：全量 + 失败必响）
	logs, err := s.auditLogsForSubjectExport(tid)
	if err != nil {
		return nil, fmt.Errorf("审计日志导出失败：%w", err)
	}
	out["audit"] = logs

	// 完整性自证：数据主体拿这份副本去做申报／举证时，「有多少行」必须能自己核对，
	// 而不是只能相信导出方。前台分页闸门的旧形态（悄悄收敛到 50／100 且零报错）
	// 之所以危险，就是因为这份 JSON 里没有任何东西能提示「你拿到的是全部吗」。
	out["export_meta"] = map[string]interface{}{
		"usage_rows":   len(ledger),
		"audit_rows":   len(logs),
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"completeness": "full-scan-by-id-cursor",
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

// ============ 数据主体导出：按 ID 游标循环取全量（★ 092x，2026-09-29）============
//
// 为什么不直接把 UsageLedgerList / ListAuditFilter 的 limit 上限抬到十万：
// 那两道闸（>500 收敛到 50、>1000 收敛到 100）保护的是**前台列表接口**——
// 用量明细页与审计页每次翻页都由普通登录态直接可达，上限一抬就等于给任何拿到账号的人
// 开了一个「一次拖走全表」的入口，防拖库形同虚设。数据主体导出是另一条路：
// super_admin 手工触发、一年打不了几次、必须交全。所以闸门原地不动，导出另走本节，
// 两边各按自己的风险定档。
//
// 为什么"取不全"必须报错而不是继续：旧写法请求 100000 行、闸门回吐 50 行、err 是 nil，
// 于是残缺包和完整包长得**一模一样**（连一行行数说明都没有）。客户会拿这份副本去申报、
// 去举证——它一旦被当成"全部数据"，比根本拿不到数据更严重。
//
// 游标口径：ORDER BY id DESC ＋ id<上一页最小 id。用 id 而不是 created_at——
// id 单调唯一，翻页不会因同秒并发写入而漏行或重行（审计表恰恰按 created_at 排序，
// 同秒行与 OFFSET 分页是历史踩点）；导出的行序因此与前台不同，但副本完整性与行序无关。
// 每页都校验「本页最小 id 严格小于游标」，游标不前进即报错退出：
// 这是死循环保险——一旦某页返回的行 id 全都 ≥ 游标，没有这一判就会把同一批反复追加到爆内存。
const (
	// gdprExportPage 单批行数。与前台分页上限同档，单次查询的内存与耗时可控；
	// 十万行也只需几百次查询，导出是后台低频人工动作，不在热路径上。
	gdprExportPage = 500
	// gdprExportRowCap 单表熔断行数。这一档离正常租户的行数还差着数量级，
	// 只用来兜住异常膨胀（导入脏数据、计数器 bug 刷流水）：宁可报错让人来查，
	// 也不让一次导出把进程吃穿。触顶即 error，**绝不静默截断**——
	// 那正是本次要修的缺陷本体，别在修法里把它复活一次。
	gdprExportRowCap = 200000
)

// usageLedgerForSubjectExport 数据主体导出用的用量流水**全量**取数（无 limit 闸门，游标取到表尾）。
// 与前台的 UsageLedgerList 有三处刻意不同，每处都是导出的法定语义：
//  1. 不收 limit——依法交副本，不该有"最多给你 50 条"；
//  2. 多带 charge_kind——前台那条 SELECT 一直没有这一列，客户拿到的副本因此答不出
//     「这一行到底扣了我多少钱 vs 只是留痕」（〇-AD 三把尺子里最要紧的那一维）；按 〇-AD 口径，
//     导出面 chargedOnly=false（留痕行也是客户的数据），扣费语义交给 charge_kind 自证；
//  3. 单行解码失败即整体报错——前台跳过坏行只影响一屏显示，导出跳过坏行＝悄悄少给数据。
func (s *Store) usageLedgerForSubjectExport(tid int64) ([]*UsageLedger, error) {
	out := []*UsageLedger{}
	cursor := int64(math.MaxInt64)
	for {
		rows, err := db.Query(s.db, db.CurrentDialect(),
			"SELECT id, tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, "+
				"COALESCE(biz_kind,''), COALESCE(biz_mode,''), COALESCE(charge_kind,''), created_at "+
				"FROM usage_ledger WHERE tenant_id=? AND id<? ORDER BY id DESC LIMIT ?",
			tid, cursor, gdprExportPage)
		if err != nil {
			return nil, err
		}
		page := make([]*UsageLedger, 0, gdprExportPage)
		lowest := cursor
		for rows.Next() {
			var u UsageLedger
			if err := rows.Scan(&u.ID, &u.TenantID, &u.UserID, &u.TaskType, &u.Provider, &u.Model,
				&u.Quantity, &u.UnitPrice, &u.Cost, &u.BizKind, &u.BizMode, &u.ChargeKind, &u.CreatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("第 %d 行起解码失败：%w", len(out)+len(page), err)
			}
			page = append(page, &u)
			lowest = u.ID // ORDER BY id DESC ⇒ 本页最后一条就是最小 id
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()

		if len(page) == 0 {
			return out, nil // 空页＝已到表尾
		}
		if lowest >= cursor {
			return nil, fmt.Errorf("游标未前进（cursor=%d、本页最小 id=%d），已取 %d 行处停手", cursor, lowest, len(out))
		}
		if len(out)+len(page) > gdprExportRowCap {
			return nil, fmt.Errorf("用量流水超过单次导出熔断上限 %d 行，请改用报表导出按时间段分批", gdprExportRowCap)
		}
		out = append(out, page...)
		if len(page) < gdprExportPage {
			return out, nil // 不满一批＝已到表尾
		}
		cursor = lowest
	}
}

// auditLogsForSubjectExport 数据主体导出用的审计日志**全量**取数（无 limit 闸门，游标取到表尾）。
// 审计里是客户侧动作的完整轨迹（谁在什么时候改了什么），before_val/after_val 可能含个人数据，
// 属于数据主体访问权的射程内，必须逐行交全。tenant_name/username 两列留空：
// 与前台 ListAuditFilter 的租户分支同口径（本就在租户边界内，没必要再 JOIN 出自己的租户名）。
func (s *Store) auditLogsForSubjectExport(tid int64) ([]*AuditLog, error) {
	out := []*AuditLog{}
	cursor := int64(math.MaxInt64)
	for {
		rows, err := db.Query(s.db, db.CurrentDialect(),
			"SELECT a.id, a.tenant_id, a.user_id, a.action, a.resource, a.detail, a.before_val, a.after_val, a.created_at "+
				"FROM audit_logs a WHERE a.tenant_id=? AND a.id<? ORDER BY a.id DESC LIMIT ?",
			tid, cursor, gdprExportPage)
		if err != nil {
			return nil, err
		}
		page := make([]*AuditLog, 0, gdprExportPage)
		lowest := cursor
		for rows.Next() {
			var a AuditLog
			if err := rows.Scan(&a.ID, &a.TenantID, &a.UserID, &a.Action, &a.Resource,
				&a.Detail, &a.BeforeVal, &a.AfterVal, &a.CreatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("第 %d 行起解码失败：%w", len(out)+len(page), err)
			}
			page = append(page, &a)
			lowest = a.ID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()

		if len(page) == 0 {
			return out, nil
		}
		if lowest >= cursor {
			return nil, fmt.Errorf("游标未前进（cursor=%d、本页最小 id=%d），已取 %d 行处停手", cursor, lowest, len(out))
		}
		if len(out)+len(page) > gdprExportRowCap {
			return nil, fmt.Errorf("审计日志超过单次导出熔断上限 %d 行，请改用时间段分批导出", gdprExportRowCap)
		}
		out = append(out, page...)
		if len(page) < gdprExportPage {
			return out, nil
		}
		cursor = lowest
	}
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
