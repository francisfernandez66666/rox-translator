// ============ 本文件职责中文说明 ============
// SaaS 平台数据层核心：Store 结构体定义与数据库迁移。
// 职责：基于 SQLite（共享 tm.sqlite3 同一连接）管理全部平台业务表
// （用户/工单/KB包/计费/审计/系统配置等），含幂等建表、老库列迁移、
// 默认单价初始化与超级管理员租户归属迁移。
// =============================================

// Package store 提供 SaaS 平台数据层：所有平台业务表（用户/工单/KB包/计费/审计/系统配置等）。
// 基于 SQLite（共享 tm.sqlite3 同一连接），与 internal/kb、internal/tenant 共用。
package store

import (
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// Store 平台存储
type Store struct {
	db *sql.DB // 底层 SQLite 连接（与 kb/tenant 共享）

	mu sync.Mutex // 互斥锁：保护并发写操作（SQLite 单写者模型）
}

// New 创建 Store 并确保全部表存在（幂等迁移）。
// 参数：db=已打开的 SQLite 连接；返回可用的 Store 实例。
func New(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err // 迁移失败则返回错误
	}
	return s, nil
}

// DB 返回底层连接（供需要跨表事务的调用方）。
func (s *Store) DB() *sql.DB { return s.db }

// migrate 顺序执行建表（幂等）。
// 步骤：① 全部 CREATE TABLE IF NOT EXISTS 建表；② 老库补列迁移；③ 初始化默认单价；④ 超级管理员提租户。
func (s *Store) migrate() error {
	stmts := []string{
		// ---------- users 用户体系 ----------
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT 'user',            -- user / approver / admin
			status TEXT NOT NULL DEFAULT 'active',        -- active / disabled
			created_by INTEGER NOT NULL DEFAULT 0,
			last_login_at TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			updated_at TEXT,
			UNIQUE(username, tenant_id)
		)`,
		// ---------- tickets 工单 ----------
		`CREATE TABLE IF NOT EXISTS tickets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			ticket_no TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',   -- draft/in_progress/pending_approval/approved/rejected/completed
			source_text TEXT NOT NULL DEFAULT '',
			file_path TEXT NOT NULL DEFAULT '',
			target_langs TEXT NOT NULL DEFAULT '',
			created_by INTEGER NOT NULL DEFAULT 0,
			approver_id INTEGER NOT NULL DEFAULT 0,
			reviewer_id INTEGER NOT NULL DEFAULT 0,
			reject_reason TEXT NOT NULL DEFAULT '',
			final_result TEXT NOT NULL DEFAULT '',  -- JSON
			created_at TEXT,
			updated_at TEXT
		)`,
		// 工单状态轨迹表：记录每一步骤的运行快照（Projector 物化）
		`CREATE TABLE IF NOT EXISTS ticket_state (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ticket_id INTEGER NOT NULL,
			step TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',         -- pending/running/success/failed/skipped
			payload TEXT NOT NULL DEFAULT '',        -- JSON 轨迹快照
			version INTEGER NOT NULL DEFAULT 1,
			updated_at TEXT
		)`,
		// ---------- kb_packages 知识库三级包 ----------
		`CREATE TABLE IF NOT EXISTS kb_packages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			parent_id INTEGER NOT NULL DEFAULT 0,    -- 0 = 根（租户根节点下挂企业包/行业包/语言文化包）
			code TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			pack_type TEXT NOT NULL DEFAULT 'industry',  -- tenant(企业) / industry(行业) / locale(语言文化)
			role TEXT NOT NULL DEFAULT 'source',     -- source(匹配来源) / gate(输出闸门)
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT,
			updated_at TEXT
		)`,
		// ---------- kb_entries 知识库条目（四层统一表） ----------
		`CREATE TABLE IF NOT EXISTS kb_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			package_id INTEGER NOT NULL DEFAULT 0,
			layer INTEGER NOT NULL DEFAULT 2,        -- L1术语 / L2TM / L3安全句 / L4碎片
			source_lang TEXT NOT NULL DEFAULT 'zh',
			source_text TEXT NOT NULL DEFAULT '',
			target_lang TEXT NOT NULL DEFAULT '',
			target_text TEXT NOT NULL DEFAULT '',
			module TEXT NOT NULL DEFAULT '',
			vector BLOB,                              -- 1024维 float32 (pgvector 等价物)
			created_at TEXT,
			updated_at TEXT
		)`,
		// 为按包+语言检索建立的索引
		`CREATE INDEX IF NOT EXISTS idx_kb_entries_pkg ON kb_entries(package_id, source_lang, target_lang)`,
		// ---------- kb_safety_phrases 安全句锁死串库 ----------
		`CREATE TABLE IF NOT EXISTS kb_safety_phrases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			package_id INTEGER NOT NULL DEFAULT 0,
			lang TEXT NOT NULL DEFAULT '',
			phrase TEXT NOT NULL DEFAULT '',
			created_at TEXT
		)`,
		// ---------- balance_accounts 租户 token 余额账本 ----------
		`CREATE TABLE IF NOT EXISTS balance_accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			balance INTEGER NOT NULL DEFAULT 0,      -- 剩余 token
			currency TEXT NOT NULL DEFAULT 'tokens',
			updated_at TEXT
		)`,
		// ---------- usage_ledger 计量明细 ----------
		`CREATE TABLE IF NOT EXISTS usage_ledger (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			user_id INTEGER NOT NULL DEFAULT 0,
			task_type TEXT NOT NULL DEFAULT '',      -- translate/review/evals/gate
			provider TEXT NOT NULL DEFAULT '',       -- LLM 供应商（成本核算维度）
			model TEXT NOT NULL DEFAULT '',          -- LLM 模型名
			quantity INTEGER NOT NULL DEFAULT 0,     -- 字符数或句数
			unit_price INTEGER NOT NULL DEFAULT 0,   -- 每单位 token
			cost INTEGER NOT NULL DEFAULT 0,         -- 扣减 token
			created_at TEXT
		)`,
		// 按租户+时间查询用量的索引
		`CREATE INDEX IF NOT EXISTS idx_usage_tenant ON usage_ledger(tenant_id, created_at)`,
		// ---------- rate_card 单价表 ----------
		`CREATE TABLE IF NOT EXISTS rate_card (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_type TEXT NOT NULL DEFAULT 'translate',
			lang TEXT NOT NULL DEFAULT '*',
			provider TEXT NOT NULL DEFAULT '*',      -- 供应商（* 表示全局通用）
			unit_price INTEGER NOT NULL DEFAULT 1,   -- token / 字符
			multiplier REAL NOT NULL DEFAULT 1.0,    -- 高膨胀语种倍率
			updated_at TEXT
		)`,
		// ---------- orders / payments 充值订单 ----------
		`CREATE TABLE IF NOT EXISTS orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			order_no TEXT NOT NULL DEFAULT '',
			amount_tokens INTEGER NOT NULL DEFAULT 0,
			amount_money REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',   -- pending/paid/refunded/cancelled
			pay_method TEXT NOT NULL DEFAULT 'offline',
			created_by INTEGER NOT NULL DEFAULT 0,
			created_at TEXT,
			paid_at TEXT NOT NULL DEFAULT ''
		)`,
		// 支付流水表：记录每次订单支付确认
		`CREATE TABLE IF NOT EXISTS payments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			order_id INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			amount_tokens INTEGER NOT NULL DEFAULT 0,
			amount_money REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT
		)`,
		// ---------- api_keys 租户开放 API Key ----------
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			key_hash TEXT NOT NULL DEFAULT '',
			key_prefix TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			perms TEXT NOT NULL DEFAULT 'translate',   -- translate / kb / billing / all
			status TEXT NOT NULL DEFAULT 'active',     -- active / disabled
			created_at TEXT,
			last_used_at TEXT NOT NULL DEFAULT '',
			call_count INTEGER NOT NULL DEFAULT 0
		)`,
		// ---------- eval_records 评估记录 ----------
		`CREATE TABLE IF NOT EXISTS eval_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			user_id INTEGER NOT NULL DEFAULT 0,
			ticket_id INTEGER NOT NULL DEFAULT 0,
			task_type TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			input_text TEXT NOT NULL DEFAULT '',
			output_text TEXT NOT NULL DEFAULT '',
			scores TEXT NOT NULL DEFAULT '',          -- JSON 5维
			total REAL NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'skipped',    -- passed/failed/retried/skipped
			created_at TEXT
		)`,
		// ---------- audit_logs 审计日志 ----------
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			user_id INTEGER NOT NULL DEFAULT 0,
			action TEXT NOT NULL DEFAULT '',
			resource TEXT NOT NULL DEFAULT '',
			detail TEXT NOT NULL DEFAULT '',
			before_val TEXT NOT NULL DEFAULT '',     -- 操作前值（JSON 字符串，结构化轨迹）
			after_val TEXT NOT NULL DEFAULT '',      -- 操作后值（JSON 字符串，结构化轨迹）
			created_at TEXT
		)`,
		// 审计日志按租户+时间查询索引
		`CREATE INDEX IF NOT EXISTS idx_audit_tenant ON audit_logs(tenant_id, created_at)`,
		// ---------- system_config 系统配置（热更新） ----------
		`CREATE TABLE IF NOT EXISTS system_config (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '',
			updated_at TEXT
		)`,
		// ---------- invite_codes 自助注册邀请码 ----------
		`CREATE TABLE IF NOT EXISTS invite_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			code TEXT NOT NULL UNIQUE,
			used INTEGER NOT NULL DEFAULT 0,          -- 0 未使用 / 1 已使用
			tenant_id INTEGER NOT NULL DEFAULT 0,    -- 指定绑定租户（0=新建独立租户）
			used_by TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			used_at TEXT NOT NULL DEFAULT ''
		)`,
		// ---------- invoices 发票 ----------
		`CREATE TABLE IF NOT EXISTS invoices (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			order_id INTEGER NOT NULL DEFAULT 0,
			invoice_no TEXT NOT NULL DEFAULT '',
			amount_money REAL NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',          -- 发票抬头
			tax_no TEXT NOT NULL DEFAULT '',         -- 税号
			status TEXT NOT NULL DEFAULT 'pending',  -- pending/issued/cancelled
			created_at TEXT
		)`,
		// ---------- alerts 监控告警 ----------
		`CREATE TABLE IF NOT EXISTS alerts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			level TEXT NOT NULL DEFAULT 'info',      -- info/warning/critical
			kind TEXT NOT NULL DEFAULT '',            -- balance/model/error_rate
			message TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'open',      -- open/resolved
			created_at TEXT,
			resolved_at TEXT NOT NULL DEFAULT ''
		)`,
		// ---------- orgs 组织层级（管理结构展示层，根组织=租户） ----------
		`CREATE TABLE IF NOT EXISTS orgs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			parent_id INTEGER NOT NULL DEFAULT 0,    -- 0 = 根组织（对应租户本身）
			name TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			updated_at TEXT
		)`,
		// 组织树按租户查询索引
		`CREATE INDEX IF NOT EXISTS idx_orgs_tenant ON orgs(tenant_id, parent_id)`,
	}
	for _, stmt := range stmts {
		// 逐条幂等执行建表语句，失败即中止迁移
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	// 迁移：老库补充新列（SQLite 无 IF NOT EXISTS 的 ALTER，需先查列）
	if err := s.migrateColumns(); err != nil {
		return err
	}
	// 初始化默认单价表（幂等）
	if err := s.seedRateCard(); err != nil {
		return err
	}
	// 迁移：超级管理员（admin/super_admin）为平台级账号，不挂租户
	if _, err := s.db.Exec(`UPDATE users SET tenant_id=0 WHERE role IN ('admin','super_admin')`); err != nil {
		return err
	}
	return nil
}

// migrateColumns 为老库补充新增列（SQLite 3.35+ 才支持 ADD COLUMN IF NOT EXISTS，这里手工判断）。
// 实现：用 pragma_table_info 检查列是否存在，缺失才执行 ALTER TABLE ADD COLUMN。
func (s *Store) migrateColumns() error {
	type colDef struct {
		table string // 目标表名
		col   string // 目标列名
		ddl   string // 补列的 ALTER 语句
	}
	cols := []colDef{
		// 老库可能缺少的列：供应商/模型/审计前后值
		{"usage_ledger", "provider", "ALTER TABLE usage_ledger ADD COLUMN provider TEXT NOT NULL DEFAULT ''"},
		{"usage_ledger", "model", "ALTER TABLE usage_ledger ADD COLUMN model TEXT NOT NULL DEFAULT ''"},
		{"rate_card", "provider", "ALTER TABLE rate_card ADD COLUMN provider TEXT NOT NULL DEFAULT '*"},
		{"audit_logs", "before_val", "ALTER TABLE audit_logs ADD COLUMN before_val TEXT NOT NULL DEFAULT ''"},
		{"audit_logs", "after_val", "ALTER TABLE audit_logs ADD COLUMN after_val TEXT NOT NULL DEFAULT ''"},
		// 组织归属：用户挂到组织（0=未分配/根组织）
		{"users", "org_id", "ALTER TABLE users ADD COLUMN org_id INTEGER NOT NULL DEFAULT 0"},
	}
	for _, c := range cols {
		// 判断列是否存在
		rows, err := s.db.Query(`SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, c.table, c.col)
		if err != nil {
			return err
		}
		has := rows.Next() // 有结果行说明列已存在
		rows.Close()
		if !has {
			// 列不存在才补列迁移
			if _, err := s.db.Exec(c.ddl); err != nil {
				return fmt.Errorf("迁移失败(%s.%s): %w", c.table, c.col, err)
			}
		}
	}
	return nil
}

// seedRateCard 初始化默认单价表（幂等）。
// 用 INSERT OR IGNORE 保证重复执行不产生重复行；设置四类任务的全局单价。
func (s *Store) seedRateCard() error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO rate_card (task_type, lang, provider, unit_price, multiplier, updated_at) VALUES
		('translate', '*', '*', 1, 1.0, ''), ('review', '*', '*', 1, 1.0, ''),
		('evals', '*', '*', 1, 0.5, ''), ('gate', '*', '*', 0, 0, '')`)
	return err
}
