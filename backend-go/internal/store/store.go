// ============ store.go · 职责说明 ============
// store 包 SaaS 平台数据层核心实现。
// Store 结构体定义与数据库迁移。
// 基于 SQLite（共享 tm.sqlite3 同一连接）管理全部平台业务表
// （用户/工单/KB包/计费/审计/系统配置等），含幂等建表、老库列迁移、
// 默认单价初始化与超级管理员租户归属迁移。
// =============================================

// Package store 提供 SaaS 平台数据层：所有平台业务表（用户/工单/KB包/计费/审计/系统配置等）。
// 基于 SQLite（共享 tm.sqlite3 同一连接），与 internal/kb、internal/tenant 共用。
package store

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/db"
	"translator/internal/iam"

	_ "modernc.org/sqlite"
)

// Store 平台存储
type Store struct {
	db *sql.DB // 底层 SQLite 连接（与 kb/tenant 共享）

	iam *iam.Store // 用户/组织数据访问层（独立子模块）

	mu sync.Mutex // 互斥锁：保护并发写操作（SQLite 单写者模型）
}

// New 创建 Store 并确保全部表存在（幂等迁移）。
// 参数：db=已打开的 SQLite 连接；返回可用的 Store 实例。
func New(db *sql.DB) (*Store, error) {
	s := &Store{db: db, iam: iam.NewStore(db)}
	if err := s.migrate(); err != nil {
		return nil, err // 迁移失败则返回错误
	}
	s.feedbackMigrate()           // 老库补 replies 列（幂等，BBS 回复线程）
	s.backfillAPIOwnership()      // ★ 历史 Key/任务强绑定回填（幂等）
	s.TmReviewMigrate()           // TM 待审池建表（幂等）
	s.QuotaGrantMigrate()         // ★ 双桶台账建表（幂等；此前漏挂导致新库缺表）
	s.TicketStateTimingMigrate()  // ★ ticket_state 增加 started_at/duration_ms（幂等；进度耗时展示）
	s.BalanceAccountMigrate()     // ★ 余额账户去重 + tenant_id 唯一索引（幂等；P0-8 并发止血）
	s.BillingIndexMigrate()       // ★ 整改 B5：订单号唯一索引 + Key 哈希检索索引（幂等，撞重复降级告警）
	s.PackagesTenantMigrate()     // ★ 商业包租户化：packages 加 tenant_id 并改 (tenant_id, code) 复合唯一（幂等）
	s.ReferralMigrate()           // ★ 邀请裂变迁移：users.ref_code/referred_by 列 + referral_rewards 表（幂等）
	s.OneidMigrate()              // ★ 账户体系：users.email 同一时刻全局唯一（部分唯一索引+存量去重，幂等）
	s.KBRewardMigrate()           // ★ KB 上传奖励流水表（幂等；任务2.3）
	s.EnsureBillingDefaults()     // 商业化参数默认值落库（幂等，面板可改）
	s.orderMoneyBackfill()        // ★ 存量 pending 充值单应收回填（幂等；评审整改 B1，置于默认值落库后以读取到定价键）
	s.PackageOrderTokenBackfill() // ★ token 口径：存量包订单 amount_tokens 补全（幂等；句数不参与运行期计算）
	s.ArtifactsMigrate()          // ★ 产物归属登记表（幂等；评审整改 C1）
	s.KBScrapeMigrate()           // ★ 行业/语言文化包自动采集：数据源 + 待审池建表（幂等；2026-09-01）
	s.SeedDefaultScrapeSources()  // ★ 功能③：通用行业兜底包默认采集源（幂等；无任何 general 源时补建）
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
		// 工单多文件表：一个文件工单可挂多个源文件，各自记录产物路径（三期·多文件上传）
		`CREATE TABLE IF NOT EXISTS ticket_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			ticket_id INTEGER NOT NULL,
			file_name TEXT NOT NULL DEFAULT '',      -- 原始文件名（下载打包用）
			file_path TEXT NOT NULL DEFAULT '',      -- 上传保存路径
			result_path TEXT NOT NULL DEFAULT '',    -- 该文件的翻译产物路径
			error TEXT NOT NULL DEFAULT '',          -- 单文件处理失败原因（不影响其他文件）
			created_at TEXT
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
		// ★ 性能优化 B6：日用量计数器表——替代每次翻译请求都对 usage_ledger 做
		//   created_at LIKE 全表扫描（gateUsage→CheckDailyQuota→DailyUsage）。
		//   落账时增量更新，查询 O(1) 命中主键。
		`CREATE TABLE IF NOT EXISTS usage_daily (
			tenant_id INTEGER NOT NULL DEFAULT 1,
			day TEXT NOT NULL,
			total INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(tenant_id, day)
		)`,
		// ★ 性能优化 B7 索引在补列迁移后统一创建（避免引用尚未存在的列），见下方 migrate 尾部。
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
			user_id INTEGER NOT NULL DEFAULT 0,        -- 关联用户 ID（系统/租户级告警为 0）
			level TEXT NOT NULL DEFAULT 'info',      -- info/warning/critical
			kind TEXT NOT NULL DEFAULT '',            -- balance/model/error_rate
			message TEXT NOT NULL DEFAULT '',
			log TEXT NOT NULL DEFAULT '',             -- 详细日志/上下文（比 message 更完整）
			status TEXT NOT NULL DEFAULT 'open',      -- open/resolved
			created_at TEXT,
			resolved_at TEXT NOT NULL DEFAULT ''
		)`,
		// 历史库兼容：user_id / log 两列改由 migrateColumns 按列存在性幂等补列（避免 ADD COLUMN IF NOT EXISTS 在部分 SQLite 驱动下的语法错误）
		// ---------- orgs 组织层级（管理结构展示层：根组织=租户） ----------
		`CREATE TABLE IF NOT EXISTS orgs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			parent_id INTEGER NOT NULL DEFAULT 0,    -- 0 = 根组织（对应租户本身）
			name TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'org',        -- root(根组织)/org(组织)/dept(部门)
			created_at TEXT,
			updated_at TEXT
		)`,
		// 组织树按租户查询索引
		`CREATE INDEX IF NOT EXISTS idx_orgs_tenant ON orgs(tenant_id, parent_id)`,
		// ---------- webhooks 租户回调配置（翻译完成通知客户 TMS/CI） ----------
		`CREATE TABLE IF NOT EXISTS webhooks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 1,
			url TEXT NOT NULL DEFAULT '',              -- 回调 URL
			secret TEXT NOT NULL DEFAULT '',           -- 签名密钥（HMAC-SHA256）
			events TEXT NOT NULL DEFAULT 'translation.completed', -- 订阅事件（逗号分隔）
			enabled INTEGER NOT NULL DEFAULT 1,        -- 1=启用 0=停用
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_webhooks_tenant ON webhooks(tenant_id, enabled)`,
		// ---------- packages 商业包（免费体验/付费包/增量包） ----------
		`CREATE TABLE IF NOT EXISTS packages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			code TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			ptype TEXT NOT NULL DEFAULT 'paid',       -- free(免费体验) / paid(付费包) / increment(增量包)
			sentences INTEGER NOT NULL DEFAULT 0,     -- 包内含翻译句数
			price_money REAL NOT NULL DEFAULT 0,      -- 售价（元）
			duration_days INTEGER NOT NULL DEFAULT 30, -- 有效期（天，包月=30）
			enabled INTEGER NOT NULL DEFAULT 1,       -- 1=上架 0=下架
			sort_order INTEGER NOT NULL DEFAULT 0,    -- 展示排序（升序）
			created_at TEXT,
			updated_at TEXT,
			UNIQUE(tenant_id, code)
		)`,
		// 订单关联商业包（订阅付费包/增量包时使用）
		`CREATE INDEX IF NOT EXISTS idx_packages_code ON packages(code, enabled)`,
		// ---------- jobs 异步任务账本（direct 执行器 / 未来 kafka 驱动共用） ----------
		`CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL DEFAULT '',                -- 任务类型（ticket_run 等）
			payload TEXT NOT NULL DEFAULT '{}',           -- JSON 载荷（如 {"ticket_id":1}）
			status TEXT NOT NULL DEFAULT 'queued',        -- queued/running/done/failed/dead
			attempts INTEGER NOT NULL DEFAULT 0,          -- 已尝试次数
			max_attempts INTEGER NOT NULL DEFAULT 3,      -- 最大尝试次数（超限进 dead）
			leased_by TEXT NOT NULL DEFAULT '',           -- 占用 worker 标识
			leased_at TEXT NOT NULL DEFAULT '',           -- 占用时间（可见性超时判定）
			timeout_sec INTEGER NOT NULL DEFAULT 1800,    -- 租约超时秒数
			error TEXT NOT NULL DEFAULT '',               -- 最近一次失败原因
			created_at TEXT,
			updated_at TEXT
		)`,
		// ---------- translation_edits 对照编辑器（逐段编辑/通过/驳回批注） ----------
		`CREATE TABLE IF NOT EXISTS translation_edits (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,         -- 所属租户 ID
			ticket_id INTEGER NOT NULL DEFAULT 0,         -- 关联工单 ID
			lang TEXT NOT NULL DEFAULT '',                -- 目标语言
			seg_index INTEGER NOT NULL DEFAULT 0,         -- 段序号（与源文分段对齐）
			source_text TEXT NOT NULL DEFAULT '',          -- 源文段落
			target_text TEXT NOT NULL DEFAULT '',          -- 当前译文（系统产出）
			edited_text TEXT NOT NULL DEFAULT '',          -- 用户修订译文（未编辑为空）
			status TEXT NOT NULL DEFAULT 'pending',        -- pending/approved/rejected
			note TEXT NOT NULL DEFAULT '',                 -- 驳回/批注说明
			reviewer_id INTEGER NOT NULL DEFAULT 0,        -- 操作人 ID
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_translation_edits_uniq ON translation_edits(ticket_id, lang, seg_index)`,
		// ---------- notifications 站内信（通用通知中心） ----------
		`CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL DEFAULT 0,           -- 接收用户 ID
			title TEXT NOT NULL DEFAULT '',               -- 标题
			body TEXT NOT NULL DEFAULT '',                -- 正文
			ref_type TEXT NOT NULL DEFAULT '',            -- 关联类型（ticket/alert...）
			ref_id INTEGER NOT NULL DEFAULT 0,            -- 关联 ID
			read_at TEXT NOT NULL DEFAULT '',             -- 已读时间（空=未读）
			created_at TEXT
		)`,
		// ---------- feedbacks 用户反馈（前台翻译结果 → 超管） ----------
		`CREATE TABLE IF NOT EXISTS feedbacks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,         -- 所属租户 ID
			user_id INTEGER NOT NULL DEFAULT 0,           -- 反馈用户 ID
			target_type TEXT NOT NULL DEFAULT 'text',     -- 反馈对象：text | ticket
			ticket_id INTEGER NOT NULL DEFAULT 0,         -- 工单 ID（text 类型为 0）
			source_text TEXT NOT NULL DEFAULT '',         -- 源文本上下文（勾选同意才存）
			translations TEXT NOT NULL DEFAULT '',        -- 译文 JSON 上下文（勾选同意才存）
			target_langs TEXT NOT NULL DEFAULT '',        -- 目标语言列表
			mode TEXT NOT NULL DEFAULT '',                -- 翻译模式：fast | pro
			content TEXT NOT NULL,                        -- 反馈意见（必填）
			with_context INTEGER NOT NULL DEFAULT 0,      -- 是否同意附带上下文：1=是
			status TEXT NOT NULL DEFAULT 'open',          -- 处理状态：open | resolved
			handle_note TEXT NOT NULL DEFAULT '',         -- 超管处理备注
			created_at TEXT,
			handled_at TEXT
		)`,
		// ---------- 索引补全（P0-4，幂等，避免高并发/大表全扫） ----------
		`CREATE INDEX IF NOT EXISTS idx_ticket_state_ticket ON ticket_state(ticket_id)`,
		`CREATE INDEX IF NOT EXISTS idx_ticket_files_ticket ON ticket_files(ticket_id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_ledger_tenant_user ON usage_ledger(tenant_id, user_id)`,
		// ---------- 频率护栏持久化（整改 R-M8：登录/注册/验证码限流从内存迁 SQLite，重启/多副本共享） ----------
		`CREATE TABLE IF NOT EXISTS rate_limits (
			scope TEXT NOT NULL,
			key TEXT NOT NULL,
			count INTEGER NOT NULL DEFAULT 0,
			window_start INTEGER NOT NULL DEFAULT 0,
			lock_until INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (scope, key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(tenant_id, created_at)`,
	}
	// 方言：以现有 SQLite DDL 为唯一真源，PostgreSQL 下经 db.ToDialect 自动改写
	d := db.CurrentDialect()
	for _, stmt := range stmts {
		// 逐条幂等执行建表语句，失败即中止迁移
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	// 迁移：老库补充新列
	if d == db.DialectSQLite {
		// SQLite 无 ADD COLUMN IF NOT EXISTS，需先 PRAGMA 查列存在性
		if err := s.migrateColumnsSQLite(); err != nil {
			return err
		}
	} else {
		// PostgreSQL 下 ALTER 自动补 IF NOT EXISTS，幂等可重复执行
		if err := s.migrateColumnsPG(); err != nil {
			return err
		}
	}
	// 初始化默认单价表（幂等）
	if err := s.seedRateCard(); err != nil {
		return err
	}
	// 迁移：超级管理员（admin/super_admin）为平台级账号，不挂租户
	if _, err := db.Exec(s.db, db.CurrentDialect(), `UPDATE users SET tenant_id=0 WHERE role IN ('admin','super_admin')`); err != nil {
		return err
	}
	// 约束：同租户下用户名唯一（三期）。容错执行——存量库若已有重名数据，
	// 建索引失败仅记日志不阻断启动；清理重名后重启自动补建。
	if _, err := db.Exec(s.db, db.CurrentDialect(), `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_tid_username ON users(tenant_id, username)`); err != nil {
		log.Printf("[migrate] 同租户用户名唯一索引创建失败（存在重名数据，请清理后重启）: %v", err)
	}
	return nil
}

// colDef 描述一次补列迁移。
type colDef struct {
	table string // 目标表名
	col   string // 目标列名
	ddl   string // 补列的 ALTER 语句
}

// columnAdditions 老库可能缺失的列清单（SQLite 与 PostgreSQL 共用同一份 ALTER 模板，
// PostgreSQL 下经 db.RunAlter 自动补 IF NOT EXISTS）。
var columnAdditions = []colDef{
	// 老库可能缺少的列：供应商/模型/审计前后值
	{"usage_ledger", "provider", "ALTER TABLE usage_ledger ADD COLUMN provider TEXT NOT NULL DEFAULT ''"},
	{"usage_ledger", "model", "ALTER TABLE usage_ledger ADD COLUMN model TEXT NOT NULL DEFAULT ''"},
	// ★ 用量看板标注（2026-08-26 需求）：业务形态(text/file)与翻译模式(fast/pro)
	{"usage_ledger", "biz_kind", "ALTER TABLE usage_ledger ADD COLUMN biz_kind TEXT NOT NULL DEFAULT ''"},
	{"usage_ledger", "biz_mode", "ALTER TABLE usage_ledger ADD COLUMN biz_mode TEXT NOT NULL DEFAULT ''"},
	{"rate_card", "provider", "ALTER TABLE rate_card ADD COLUMN provider TEXT NOT NULL DEFAULT ''"},
	{"audit_logs", "before_val", "ALTER TABLE audit_logs ADD COLUMN before_val TEXT NOT NULL DEFAULT ''"},
	{"audit_logs", "after_val", "ALTER TABLE audit_logs ADD COLUMN after_val TEXT NOT NULL DEFAULT ''"},
	// 组织归属：用户挂到组织（0=未分配/根组织）
	{"users", "org_id", "ALTER TABLE users ADD COLUMN org_id INTEGER NOT NULL DEFAULT 0"},
	// 在线支付：订单渠道与支付凭证
	{"orders", "channel", "ALTER TABLE orders ADD COLUMN channel TEXT NOT NULL DEFAULT 'offline'"},
	{"orders", "prepay_id", "ALTER TABLE orders ADD COLUMN prepay_id TEXT NOT NULL DEFAULT ''"},
	{"orders", "qr_content", "ALTER TABLE orders ADD COLUMN qr_content TEXT NOT NULL DEFAULT ''"},
	// 联系邮箱：找回密码验证码接收地址
	{"users", "email", "ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT ''"},
	// ★ 自助注销请求日期（2026-08-26 需求）：当日宽限、次日起等效停用；数据保留不删除
	{"users", "deactivate_at", "ALTER TABLE users ADD COLUMN deactivate_at TEXT NOT NULL DEFAULT ''"},
	// 组织类型：root(根组织)/org(组织)/dept(部门)
	{"orgs", "type", "ALTER TABLE orgs ADD COLUMN type TEXT NOT NULL DEFAULT 'org'"},
	// ★ 部门预算（四期）：部门月度 token 预算上限；∑部门预算=租户总预算（双预算墙之部门墙）
	{"orgs", "token_limit", "ALTER TABLE orgs ADD COLUMN token_limit INTEGER NOT NULL DEFAULT 0"},
	// 订单关联商业包（订阅付费包/增量包）
	{"orders", "package_id", "ALTER TABLE orders ADD COLUMN package_id INTEGER NOT NULL DEFAULT 0"},
	// 静态码支付人工确认标记（用户点「我已付费」后置 1，超管确认到账后清零）
	{"orders", "manual_confirm", "ALTER TABLE orders ADD COLUMN manual_confirm INTEGER NOT NULL DEFAULT 0"},
	// ★ 整改 B3：部分退款实退金额（比例折算口径），审计与对账依据
	{"orders", "refund_money", "ALTER TABLE orders ADD COLUMN refund_money REAL NOT NULL DEFAULT 0"},
	// 知识库包归属部门（0=租户级；部门管理员创建部门包时挂本部门）
	{"kb_packages", "org_id", "ALTER TABLE kb_packages ADD COLUMN org_id INTEGER NOT NULL DEFAULT 0"},
	// ★ 跨部门共享开关（2026-08-26 KB继承链改造）：1=愿意参与跨部门降级检索（默认），
	//   0=本包仅限归属链内用户可见（包级 opt-out）；对企业/行业/文化包无意义（本就全局/全租户）
	{"kb_packages", "share_cross_dept", "ALTER TABLE kb_packages ADD COLUMN share_cross_dept INTEGER NOT NULL DEFAULT 1"},
	// 知识库应用优先级：部门包(0) > 组织包(1) > 行业包(2) > 语言文化包(3)；旧数据默认 9
	{"tm_segments", "priority", "ALTER TABLE tm_segments ADD COLUMN priority INTEGER NOT NULL DEFAULT 9"},
	// 检索条目归属知识库包（0=无归属/历史兜底数据）；停用/启用/统计按此精确摘除与回写
	{"tm_segments", "pack_id", "ALTER TABLE tm_segments ADD COLUMN pack_id INTEGER NOT NULL DEFAULT 0"},
	// 工单结果文件路径（原格式回写产物或 xlsx 对照表）
	{"tickets", "result_path", "ALTER TABLE tickets ADD COLUMN result_path TEXT NOT NULL DEFAULT ''"},
	// 翻译模式：fast 快速（无KB/初翻+校对）/ pro 专业校对（全流水线）；空=pro
	{"tickets", "mode", "ALTER TABLE tickets ADD COLUMN mode TEXT NOT NULL DEFAULT ''"},
	// R4 Key 级配额：每日调用上限（0=不限）与今日计数（跨日自动清零）
	// 四期：工单实费计费 token 数（完成时按真实用量×均摊系数写入）
	{"tickets", "tokens_billed", "ALTER TABLE tickets ADD COLUMN tokens_billed INTEGER NOT NULL DEFAULT 0"},
	{"api_keys", "daily_call_limit", "ALTER TABLE api_keys ADD COLUMN daily_call_limit INTEGER NOT NULL DEFAULT 0"},
	{"api_keys", "calls_today", "ALTER TABLE api_keys ADD COLUMN calls_today INTEGER NOT NULL DEFAULT 0"},
	// ★ Key 静态加密存储：支持任意时刻复制（明文不出库、不回显）
	{"api_keys", "key_enc", "ALTER TABLE api_keys ADD COLUMN key_enc TEXT NOT NULL DEFAULT ''"},
	{"api_keys", "calls_today_date", "ALTER TABLE api_keys ADD COLUMN calls_today_date TEXT NOT NULL DEFAULT ''"},
	// ★ 邀请码绑定组织（四期）：受邀用户归入该组织层级
	{"invite_codes", "org_id", "ALTER TABLE invite_codes ADD COLUMN org_id INTEGER NOT NULL DEFAULT 0"},
	// 工单产物保留期：完成时间 + N 天（到期由后台扫描清理文件；核心译文已入 tm_segments 长期保留）
	{"tickets", "result_expires_at", "ALTER TABLE tickets ADD COLUMN result_expires_at TEXT NOT NULL DEFAULT ''"},
	// 产物过期提醒档位去重标记（逗号分隔：14,7,3,1）
	{"tickets", "expire_notify", "ALTER TABLE tickets ADD COLUMN expire_notify TEXT NOT NULL DEFAULT ''"},
	// 安全句结构化字段（Gate 闸门）：类型/替换词/审核状态/来源
	{"kb_safety_phrases", "kind", "ALTER TABLE kb_safety_phrases ADD COLUMN kind TEXT NOT NULL DEFAULT 'style'"},
	{"kb_safety_phrases", "replacement", "ALTER TABLE kb_safety_phrases ADD COLUMN replacement TEXT NOT NULL DEFAULT ''"},
	{"kb_safety_phrases", "status", "ALTER TABLE kb_safety_phrases ADD COLUMN status TEXT NOT NULL DEFAULT 'approved'"},
	{"kb_safety_phrases", "source", "ALTER TABLE kb_safety_phrases ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'"},
	// 知识库包启停状态（停用后不参与翻译命中）
	{"kb_packages", "enabled", "ALTER TABLE kb_packages ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1"},
	// 跨部门包涵盖部门集合（JSON 数组；使用/维护范围=这些部门的成员/管理员）
	{"kb_packages", "cross_orgs", "ALTER TABLE kb_packages ADD COLUMN cross_orgs TEXT NOT NULL DEFAULT '[]'"},
	// 跨部门包是否全公司（1=涵盖租户内全部部门，0=仅 cross_orgs 列明的部门）
	{"kb_packages", "cross_all", "ALTER TABLE kb_packages ADD COLUMN cross_all INTEGER NOT NULL DEFAULT 0"},
	// ★ OpenAPI 安全绑定：Key 归属签发用户；API 任务盖印创建者用户 ID（回读校验防跨用户/租户越权）
	{"api_keys", "user_id", "ALTER TABLE api_keys ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0"},
	{"tickets", "api_user_id", "ALTER TABLE tickets ADD COLUMN api_user_id INTEGER NOT NULL DEFAULT 0"},
	{"tickets", "max_length", "ALTER TABLE tickets ADD COLUMN max_length INTEGER NOT NULL DEFAULT 0"},
	{"alerts", "user_id", "ALTER TABLE alerts ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0"},
	{"alerts", "log", "ALTER TABLE alerts ADD COLUMN log TEXT NOT NULL DEFAULT ''"},
	// 用户协议签署时间（注册即视为同意用户协议+隐私协议；空=未签署）
	{"users", "agreed_at", "ALTER TABLE users ADD COLUMN agreed_at TEXT NOT NULL DEFAULT ''"},
	// ★ 首登强制改密（2026-09-02 功能）：Excel 批量导入用户置 1，首次登录需先改密
	{"users", "must_change_pwd", "ALTER TABLE users ADD COLUMN must_change_pwd INTEGER NOT NULL DEFAULT 0"},
}

// migrateColumnsSQLite 为老库补充新增列（SQLite 3.35+ 才支持 ADD COLUMN IF NOT EXISTS，这里手工判断）。
// 实现：用 pragma_table_info 检查列是否存在，缺失才执行 ALTER TABLE ADD COLUMN。
func (s *Store) migrateColumnsSQLite() error {
	cols := columnAdditions
	for _, c := range cols {
		// 判断表是否存在（tm_segments 等表由 kb 模块延迟建表，缺表时跳过）
		trows, err := db.Query(s.db, db.CurrentDialect(), `SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, c.table)
		if err != nil {
			return err
		}
		tableExists := trows.Next()
		trows.Close()
		if !tableExists {
			continue
		}
		// 判断列是否存在
		rows, err := db.Query(s.db, db.CurrentDialect(), `SELECT 1 FROM pragma_table_info(?) WHERE name = ?`, c.table, c.col)
		if err != nil {
			return err
		}
		has := rows.Next() // 有结果行说明列已存在
		rows.Close()
		if !has {
			// 列不存在才补列迁移
			if _, err := db.Exec(s.db, db.CurrentDialect(), c.ddl); err != nil {
				return fmt.Errorf("迁移失败(%s.%s): %w", c.table, c.col, err)
			}
		}
	}
	// ★ 性能优化 B6：部署当日若 usage_daily 尚无当日行，从 usage_ledger 兜底回填当日计数，
	//   避免「切换前已产生的当日用量」丢失；覆盖写（非累加）保证幂等，后续启动因已有当日行而跳过。
	s.backfillDailyUsage()
	// ★ 性能优化 B7：补列迁移完成后再建联合索引（引用 org_id 等后加列，否则建表期报错）
	db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_users_tenant_org ON users(tenant_id, org_id)`)
	db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_usage_tenant_user ON usage_ledger(tenant_id, user_id, created_at)`)
	return nil
}

// migrateColumnsPG 在 PostgreSQL 下补充新增列：ALTER ADD COLUMN IF NOT EXISTS（幂等可重复）。
// 不执行 backfillDailyUsage（PG 为全新实例，无 legacy 当日用量需回填；且其 ? 占位符需走 PG 改写层）。
func (s *Store) migrateColumnsPG() error {
	d := db.DialectPostgres
	for _, c := range columnAdditions {
		var exists int
		if err := db.QueryRow(s.db, d, "SELECT COUNT(*) FROM information_schema.tables WHERE table_name = ?", c.table).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue // 表尚未创建（如 tm_segments 由 kb 模块延迟建表）
		}
		if err := db.RunAlter(s.db, d, c.ddl); err != nil {
			return fmt.Errorf("PG 补列失败(%s.%s): %w", c.table, c.col, err)
		}
	}
	// B7：补列后再建联合索引（引用 org_id 等后加列）
	if err := db.ExecDDL(s.db, d, `CREATE INDEX IF NOT EXISTS idx_users_tenant_org ON users(tenant_id, org_id)`); err != nil {
		return err
	}
	if err := db.ExecDDL(s.db, d, `CREATE INDEX IF NOT EXISTS idx_usage_tenant_user ON usage_ledger(tenant_id, user_id, created_at)`); err != nil {
		return err
	}
	return nil
}

// backfillDailyUsage 部署当日日计数器兜底回填（性能优化 B6）。
func (s *Store) backfillDailyUsage() {
	today := time.Now().Format("2006-01-02")
	var cnt int64
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(1) FROM usage_daily WHERE day=?", today).Scan(&cnt); err != nil || cnt > 0 {
		return // 已有当日行：跳过
	}
	_, _ = db.Exec(s.db, db.CurrentDialect(), `INSERT INTO usage_daily (tenant_id, day, total)
		SELECT tenant_id, substr(created_at,1,10) AS day, COALESCE(SUM(cost),0)
		FROM usage_ledger WHERE substr(created_at,1,10)=? GROUP BY tenant_id
		ON CONFLICT(tenant_id, day) DO UPDATE SET total=excluded.total`, today)
}

// seedRateCard 初始化默认单价表（幂等）。
// 用 INSERT OR IGNORE 保证重复执行不产生重复行；设置四类任务的全局单价。
func (s *Store) seedRateCard() error {
	_, err := db.Exec(s.db, db.CurrentDialect(), `INSERT OR IGNORE INTO rate_card (task_type, lang, provider, unit_price, multiplier, updated_at) VALUES
		('translate', '*', '*', 1, 1.0, ''), ('review', '*', '*', 1, 1.0, ''),
		('evals', '*', '*', 1, 0.5, ''), ('gate', '*', '*', 0, 0, '')`)
	return err
}

// backfillAPIOwnership 历史数据强绑定回填（幂等，启动执行）：
// ① user_id=0 的 API Key → 归属本租户最早的 admin/tenant_admin；
// ② created_by=0 且 api_user_id=0 的 API 任务 → 同上。
// 使 status/download 的「租户+用户」双重校验对全部存量生效，杜绝跨用户越权。
func (s *Store) backfillAPIOwnership() {
	db.Exec(s.db, db.CurrentDialect(), `UPDATE api_keys SET user_id=(
		SELECT MIN(u.id) FROM users u
		WHERE u.tenant_id=api_keys.tenant_id AND u.role IN ('admin','tenant_admin'))
	WHERE COALESCE(user_id,0)=0`)
	db.Exec(s.db, db.CurrentDialect(), `UPDATE tickets SET api_user_id=(
		SELECT MIN(u.id) FROM users u
		WHERE u.tenant_id=tickets.tenant_id AND u.role IN ('admin','tenant_admin'))
	WHERE created_by=0 AND COALESCE(api_user_id,0)=0`)
}

// PruneAuditLogs 删除早于 cutoff 的审计日志（长期运行表膨胀治理，P2 审计留存策略）。
// 参数 cutoff=保留截止时间（早于此时间的记录删除）；返回删除条数。忽略错误（清理失败不影响主流程）。
func (s *Store) PruneAuditLogs(cutoff time.Time) (int64, error) {
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM audit_logs WHERE created_at < ?", cutoff.Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// AuditRetentionDays 审计日志保留天数（默认 365；可由 system_config.audit_retention_days 覆盖）。
func (s *Store) AuditRetentionDays() int {
	if v, err := s.GetConfig("audit_retention_days"); err == nil {
		if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n > 0 {
			return n
		}
	}
	return 365
}
