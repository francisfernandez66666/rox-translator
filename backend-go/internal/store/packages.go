// ============ packages.go · 职责说明 ============
// store 包商业包数据访问层（packages 表）。
// 付费包 / 增量包 / 免费体验包的 CRUD，
// 以及租户句数余额（sentence_balance，存于 tenants.permissions JSON）的读写。
// 商业包模型：
//   - free（免费体验）：新租户注册自动开通，token 由 free_trial_tokens 配置（默认 300000）
//   - paid（付费包）：超管自定义（包月 X 句），订阅后向租户句数余额发放 X 句
//   - increment（增量包）：超管自定义（X 句），购买后追加到租户句数余额
//
// 句数计量口径：每源句 × 每个目标语言 = 消耗句数（与 usage_ledger 逐语言计量一致）。
// =============================================
package store

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"time"
	"translator/internal/ops"

	"translator/internal/db"
	"translator/internal/tenant"
)

// Package 商业包实体（packages 表）
type Package struct {
	ID           int64   `json:"id"`            // 包主键 ID
	TenantID     int64   `json:"tenant_id"`     // 租户 ID（0=平台）
	Code         string  `json:"code"`          // 包编码（唯一，如 trial/monthly_1000/inc_500）
	Name         string  `json:"name"`          // 包名称（如 包月 1000 句）
	PType        string  `json:"ptype"`         // 包类型：free(免费体验) / paid(付费包) / increment(增量包)
	Sentences    int64   `json:"sentences"`     // 包内含翻译句数（★ 2026-09-14 起为历史展示字段，售卖以 Points 为准）
	Points       int64   `json:"points"`        // ★ S1 积分面值（对外售卖/展示单位；内部 token=积分×points_tokens_rate）
	PriceMoney   float64 `json:"price_money"`   // 售价（元）
	DurationDays int     `json:"duration_days"` // 有效期（天，包月=30）
	Enabled      int     `json:"enabled"`       // 1=上架 0=下架
	SortOrder    int     `json:"sort_order"`    // 展示排序（升序）
	CreatedAt    string  `json:"created_at"`    // 创建时间（RFC3339 字符串）
	UpdatedAt    string  `json:"updated_at"`    // 更新时间（RFC3339 字符串）
}

// 商业包类型常量
const (
	PackageFree      = "free"      // 免费体验包
	PackagePaid      = "paid"      // 付费包（包月 X 句）
	PackageIncrement = "increment" // 翻译增量包
)

// ============ 商业包 CRUD ============

// packageCols packages 表查询列清单（统一使用，避免遗漏）
const packageCols = "id, tenant_id, code, name, ptype, sentences, points, price_money, duration_days, enabled, sort_order, created_at, updated_at"

// CreatePackage 创建商业包（超管）。
// 参数：pkg=待创建的包对象（code/name/ptype 必填）；返回新包对象。
func (s *Store) CreatePackage(pkg *Package) (*Package, error) {
	now := time.Now().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO packages (tenant_id, code, name, ptype, sentences, points, price_money, duration_days, enabled, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
		pkg.TenantID, pkg.Code, pkg.Name, pkg.PType, pkg.Sentences, pkg.Points, pkg.PriceMoney, pkg.DurationDays, pkg.Enabled, pkg.SortOrder, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetPackage(id)
}

// GetPackage 按 ID 查询商业包。
// 参数：id=包主键 ID；返回包对象。
func (s *Store) GetPackage(id int64) (*Package, error) {
	var p Package
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+packageCols+" FROM packages WHERE id=?", id).
		Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.Points, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPackageByCode 按编码查询商业包。
// ★ P2 修复（2026-09-05）：支持平台包订阅——tenant_id IN (0, ?) 同时命中租户自有包与平台包，
//
//	优先返回租户自有包（同一 code 下租户级优先于平台级），保证 /api/plans 列出的平台套餐可被任意租户订阅。
//
// 参数：tenantID=租户 ID，code=包编码；返回包对象。
func (s *Store) GetPackageByCode(tenantID int64, code string) (*Package, error) {
	var p Package
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+packageCols+
		" FROM packages WHERE code=? AND tenant_id IN (0, ?) ORDER BY CASE WHEN tenant_id=? THEN 0 ELSE 1 END, id LIMIT 1",
		code, tenantID, tenantID).
		Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.Points, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListCommercialPackages 列出全部商业包（超管管理用，含下架包）。
// 参数：无；返回包列表（按类型、排序、ID 排序）。
func (s *Store) ListCommercialPackages() ([]*Package, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+packageCols+" FROM packages ORDER BY ptype, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		var p Package
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.Points, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// ListEnabledCommercialPackages 列出上架中的商业包（公开定价页 / 注册订阅用）。
// 参数：无；返回已启用的包列表（按类型、排序、ID 排序）。
func (s *Store) ListEnabledCommercialPackages() ([]*Package, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+packageCols+" FROM packages WHERE enabled=1 ORDER BY ptype, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		var p Package
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.Points, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// UpdatePackage 更新商业包（超管）。
// 参数：pkg=待更新的包对象（全部字段整体覆盖）；返回错误。
func (s *Store) UpdatePackage(pkg *Package) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE packages SET tenant_id=?, name=?, ptype=?, sentences=?, points=?, price_money=?, duration_days=?, enabled=?, sort_order=?, updated_at=? WHERE id=?",
		pkg.TenantID, pkg.Name, pkg.PType, pkg.Sentences, pkg.Points, pkg.PriceMoney, pkg.DurationDays, pkg.Enabled, pkg.SortOrder, time.Now().Format(time.RFC3339), pkg.ID)
	return err
}

// DeletePackage 删除商业包（超管）。
// 参数：id=包主键 ID；返回错误。
// DeletePackage 删除商业包。★ C15（2026-09-12）：删除前引用检查——
// ① 存在 pending 订单（用户正在支付流程中）；② 存在仍有余量的活跃台账
// （source='order' 且 ref 挂本包订单、left>0、未过期）。任一命中即拒绝，
// 提示改用停用（enabled=0），避免订单回调/退款找不到包定义（MarkOrderPaid
// 「套餐缺失」挂起、RefundOrder 无法核算）。
func (s *Store) DeletePackage(id int64) error {
	var pendingOrders int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM orders WHERE package_id=? AND status='pending'", id).Scan(&pendingOrders); err != nil {
		return err
	}
	if pendingOrders > 0 {
		return &errPkgRef{"待支付订单 " + strconv.FormatInt(pendingOrders, 10) + " 笔正在引用，请改用停用"}
	}
	var activeGrants int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM quota_grants g JOIN orders o ON o.id=g.ref_id
		 WHERE o.package_id=? AND g.source='order' AND g."left">0 AND g.expires_at>?`,
		id, time.Now().UTC().Format(time.RFC3339)).Scan(&activeGrants); err != nil {
		return err
	}
	if activeGrants > 0 {
		return &errPkgRef{"仍有 " + strconv.FormatInt(activeGrants, 10) + " 条活跃权益台账引用本包，请改用停用"}
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM packages WHERE id=?", id)
	return err
}

// errPkgRef 套餐被引用错误（可读消息，API 层直接透出）。
type errPkgRef struct{ msg string }

// Error 实现 error 接口：套餐被订单/余额引用时禁止删除。
func (e *errPkgRef) Error() string { return "套餐被引用无法删除：" + e.msg }

// PackagesTenantMigrate 将 packages 表从「code 全局唯一」迁移为「(tenant_id, code) 租户级唯一」。
// 幂等：新库直接创建复合唯一；老库补 tenant_id 列后重建表完成约束替换。
func (s *Store) PackagesTenantMigrate() {
	d := db.CurrentDialect()
	if d != db.DialectSQLite {
		// PostgreSQL：直接保证复合唯一，无需重建表。
		_ = db.EnsureColumns(s.db, d, "packages", map[string]string{
			"tenant_id": "INTEGER NOT NULL DEFAULT 0",
		})
		sets, err := db.UniqueColumnSets(s.db, d, "packages")
		if err == nil {
			for _, set := range sets {
				if len(set) == 2 {
					m := map[string]bool{}
					for _, c := range set {
						m[c] = true
					}
					if m["tenant_id"] && m["code"] {
						return // 已是目标形态
					}
				}
			}
		}
		// 旧形态单列 code 唯一：丢弃后建复合唯一（PostgreSQL 唯一约束名由 code 自动派生）。
		_, _ = s.db.Exec("ALTER TABLE packages DROP CONSTRAINT IF EXISTS packages_code_key")
		_, _ = s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS packages_tenant_code_uniq ON packages(tenant_id, code)")
		return
	}
	// ① 补 tenant_id 列（老库没有）
	cols, err := db.Query(s.db, db.CurrentDialect(), "PRAGMA table_info(packages)")
	if err != nil {
		return
	}
	hasTenant := false
	for cols.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := cols.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		if name == "tenant_id" {
			hasTenant = true
		}
	}
	cols.Close()
	if !hasTenant {
		db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE packages ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0")
	}

	// ② 检测当前唯一约束是否仅为 code 单列；若是则重建表换为复合唯一。
	idxRows, err := db.Query(s.db, db.CurrentDialect(), "PRAGMA index_list(packages)")
	if err != nil {
		return
	}
	needRebuild := false
	for idxRows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := idxRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			continue
		}
		if unique != 1 {
			continue
		}
		irows, err := db.Query(s.db, db.CurrentDialect(), "PRAGMA index_info(?)", name)
		if err != nil {
			continue
		}
		cols := []string{}
		for irows.Next() {
			var seqno, cid int
			var colName string
			if err := irows.Scan(&seqno, &cid, &colName); err == nil {
				cols = append(cols, colName)
			}
		}
		irows.Close()
		if len(cols) == 1 && cols[0] == "code" {
			needRebuild = true
			break
		}
	}
	idxRows.Close()
	if !needRebuild {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	db.Exec(tx, db.CurrentDialect(), `CREATE TABLE packages_new (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		code TEXT NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		ptype TEXT NOT NULL DEFAULT 'paid',
		sentences INTEGER NOT NULL DEFAULT 0,
		price_money REAL NOT NULL DEFAULT 0,
		duration_days INTEGER NOT NULL DEFAULT 30,
		enabled INTEGER NOT NULL DEFAULT 1,
		sort_order INTEGER NOT NULL DEFAULT 0,
		created_at TEXT,
		updated_at TEXT,
		UNIQUE(tenant_id, code))`)
	db.Exec(tx, db.CurrentDialect(), `INSERT INTO packages_new (id, tenant_id, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at)
		SELECT id, 0, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at FROM packages`)
	db.Exec(tx, db.CurrentDialect(), `DROP TABLE packages`)
	db.Exec(tx, db.CurrentDialect(), `ALTER TABLE packages_new RENAME TO packages`)
	tx.Commit()
}

// ============ 租户句数余额 ============

// GetSentenceBalance 读取句数镜像（tenants.permissions.sentence_balance）。
// ★ C26（2026-09-12）定稿：token 是唯一真账；sentence_balance 仅为发放流水镜像
// （只增不减、退款回冲钳 0），**禁止用于判额/对账**；展示剩余句数一律
// tokens_available ÷ TokenSentenceRate() 反推（旧 DeductSentences 生产零调用已删除）。
func (s *Store) GetSentenceBalance(tid int64) (int64, error) {
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return 0, err
	}
	return perms.SentenceBalance, nil
}

// GetTenantPerms 读取租户 permissions JSON 并解析为 Perms 结构体（含句数余额/订阅包信息）。
// 参数：tid=租户 ID；返回解析后的权限结构体（读取失败返回零值结构体）。
func (s *Store) GetTenantPerms(tid int64) (*tenant.Perms, error) {
	p := &tenant.Perms{}
	var raw string
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT permissions FROM tenants WHERE id=?", tid).Scan(&raw)
	if err == sql.ErrNoRows {
		return p, nil // 租户不存在返回空权限
	}
	if err != nil {
		return p, err
	}
	if raw == "" {
		return p, nil
	}
	_ = json.Unmarshal([]byte(raw), p) // 解析失败保留零值
	return p, nil
}

// SetSentenceBalance 整体写入租户句数余额（覆盖 sentence_balance 字段，保留其余权限）。
// 参数：tid=租户 ID，balance=新句数余额；返回错误。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：读-改-写包进 IMMEDIATE 事务——
//
//	DSN _txlock=immediate 下 BEGIN 即持写锁，单写者库内与其他写者天然互斥，
//	消除「SELECT permissions → 内存改 → 整体覆盖」与并发写者的丢失更新窗口。
func (s *Store) SetSentenceBalance(tid int64, balance int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	perms, err := getTenantPermsTx(tx, tid)
	if err != nil {
		return err
	}
	perms.SentenceBalance = balance
	b, _ := json.Marshal(perms)
	if _, err := db.Exec(tx, db.CurrentDialect(), "UPDATE tenants SET permissions=?, updated_at=? WHERE id=?", string(b), time.Now().Format(time.RFC3339), tid); err != nil {
		return err
	}
	return tx.Commit()
}

// AddSentences 增加租户句数余额（增量包购买/付费包发放时调用）。
// 参数：tid=租户 ID，n=待增加句数（需为正数）；返回新余额。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：单语句原子自增，不再整体覆盖 permissions JSON。
// ★ 2026-09-12 PG 方言修复：JSON1 函数改经 db.JSONNumAdd 双方言助手。
func (s *Store) AddSentences(tid, n int64) (int64, error) {
	if n <= 0 {
		cur, _ := s.GetSentenceBalance(tid)
		return cur, nil
	}
	d := db.CurrentDialect()
	if _, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONNumAdd(d, "permissions", "sentence_balance")+", updated_at=? WHERE id=?",
		n, time.Now().Format(time.RFC3339), tid); err != nil {
		return 0, err
	}
	return s.GetSentenceBalance(tid)
}

// getTenantPermsTx 事务作用域的租户权限读取（GetTenantPerms 的 tx 变体）。
func getTenantPermsTx(tx *sql.Tx, tid int64) (*tenant.Perms, error) {
	p := &tenant.Perms{}
	var raw string
	err := db.QueryRow(tx, db.CurrentDialect(), "SELECT permissions FROM tenants WHERE id=?", tid).Scan(&raw)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if raw == "" {
		return p, nil
	}
	_ = json.Unmarshal([]byte(raw), p)
	return p, nil
}

// saveTenantPermsTx 事务作用域的租户权限写入（SaveTenantPerms 的 tx 变体）。
func saveTenantPermsTx(tx *sql.Tx, tid int64, perms *tenant.Perms) error {
	b, _ := json.Marshal(perms)
	_, err := db.Exec(tx, db.CurrentDialect(), "UPDATE tenants SET permissions=?, updated_at=? WHERE id=?", string(b), time.Now().Format(time.RFC3339), tid)
	return err
}

// GrantPackageSentences 向租户发放商业包句数（句包外壳）：
//   - paid（付费包）：设置订阅包编码/到期时间（DurationDays>0 时计算 PackageExpires），并发放包内含句数
//   - increment（增量包）：在现有句数余额上追加包内含句数（不改订阅状态与到期）
//
// ★ Token 计费主线：发放句数的同时，按换算率（estimate_tokens_per_sentence，默认 500）
// 折算为等值 token 充入租户余额账户——余额与扣费的唯一底层单位是 token，
// 句数字段仅作订阅身份与展示镜像。
//
// 参数：tid=租户 ID，pkg=商业包对象；返回发放后的句数余额（镜像值）。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：句数镜像写入与 token 入账包进同一
// IMMEDIATE 事务，消除「镜像覆盖丢失」与「镜像已加、token 未到账」的中间态。
func (s *Store) GrantPackageSentences(tid int64, pkg *Package) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	perms, err := getTenantPermsTx(tx, tid)
	if err != nil {
		return 0, err
	}
	granted := pkg.Sentences
	if pkg.PType == PackageIncrement {
		// 增量包：追加句数，不改变订阅状态（买断资产无到期概念）
		perms.SentenceBalance += granted
	} else {
		// 付费包（含免费体验包）：覆盖订阅并发放句数；续费时清除旧到期提醒标记
		perms.PackageCode = pkg.Code
		perms.SubscribedAt = time.Now().Format(time.RFC3339)
		perms.SentenceBalance += granted
		if pkg.DurationDays > 0 {
			perms.PackageExpires = time.Now().AddDate(0, 0, pkg.DurationDays).Format(time.RFC3339)
		} else {
			perms.PackageExpires = "" // 不限期
		}
		perms.NotifiedExp7 = false
		perms.NotifiedExp1 = false
	}
	if err := saveTenantPermsTx(tx, tid, perms); err != nil {
		return 0, err
	}
	// ★ 句数折算 token 入账（句包外壳→token 底层的唯一兑换点）
	if granted > 0 {
		rate := s.TokenSentenceRate()
		// ★ 与扣费侧同单位：句数×折算率×成本均摊系数 markup（billing.go:611 同口径），
		// 否则免费/体验包 token 入账不含 markup，破坏「1 入账=1 扣费」一致性。
		if tokens := int64(float64(granted*rate) * s.MarkupMultiplier()); tokens > 0 {
			// 事务内先确保余额账户行存在（等价 Charge 的 EnsureBalance 语义），
			// 再原子累加——避免账户行缺失时 UPDATE 影响 0 行导致 token 静默丢失。
			if _, err := db.Exec(tx, db.CurrentDialect(),
				"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
				tid, time.Now().Format(time.RFC3339)); err != nil {
				return 0, err
			}
			if _, err := db.Exec(tx, db.CurrentDialect(),
				"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
				tokens, time.Now().Format(time.RFC3339), tid); err != nil {
				return 0, err
			}
			// ★ P1 多实例闭环：包折算 token 入账通知影子失效
			notifyTenantBalanceChanged(tid)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return perms.SentenceBalance, nil
}

// ApplyPaidPackageIdentity 仅落付费包订阅身份与句数镜像（不折算 token）。
// 供订单确认分流使用：token 部分由 t+30 台账（CreateQuotaGrant）负责，避免双通道重复入账。
// 参数：tid=租户 ID，pkg=付费包对象；返回发放后的句数余额（镜像值）。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：读-改-写包进 IMMEDIATE 事务。
func (s *Store) ApplyPaidPackageIdentity(tid int64, pkg *Package) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	perms, err := getTenantPermsTx(tx, tid)
	if err != nil {
		return 0, err
	}
	perms.PackageCode = pkg.Code
	perms.SubscribedAt = time.Now().Format(time.RFC3339)
	perms.SentenceBalance += pkg.Sentences
	if pkg.DurationDays > 0 {
		perms.PackageExpires = time.Now().AddDate(0, 0, pkg.DurationDays).Format(time.RFC3339)
	} else {
		perms.PackageExpires = ""
	}
	perms.NotifiedExp7 = false
	perms.NotifiedExp1 = false
	if err := saveTenantPermsTx(tx, tid, perms); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return perms.SentenceBalance, nil
}

// ApplyIncrementMirror 仅追加增量包句数镜像（不改订阅状态与到期，不折算 token）。
// token 部分由永久余额通道（Charge）负责。参数：tid=租户 ID，pkg=增量包对象。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：单语句原子自增。
// ★ 2026-09-12 PG 方言修复：JSON1 改经 db.JSONNumAdd 助手。
func (s *Store) ApplyIncrementMirror(tid int64, pkg *Package) (int64, error) {
	if pkg.Sentences > 0 {
		d := db.CurrentDialect()
		if _, err := db.Exec(s.db, d,
			"UPDATE tenants SET "+db.JSONNumAdd(d, "permissions", "sentence_balance")+", updated_at=? WHERE id=?",
			pkg.Sentences, time.Now().Format(time.RFC3339), tid); err != nil {
			return 0, err
		}
	}
	return s.GetSentenceBalance(tid)
}

// TokenSentenceRate 返回句↔token 展示换算率（estimate_tokens_per_sentence，默认 500）。
// 用途：句包发放折算、前台「≈句数」展示。后台可调。
func (s *Store) TokenSentenceRate() int64 {
	if v, err := s.GetConfig("estimate_tokens_per_sentence"); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			return n
		}
	}
	return ops.DefaultTokensPerSentence // ★ C6：默认值单一来源（ops.DefaultEffective 收口）
}

// PointsTokensRate 积分↔内部计量 token 的基础汇率（points_tokens_rate，默认 300，超管可调）。
// ★ S1 积分制（2026-09-14）：对外售卖/余额/账单一律展示积分；token 仅在内部账本
//
//	（usage_ledger/rate_card/成本核算）流转，防止外部从积分单价反推平台真实成本。
func (s *Store) PointsTokensRate() int64 {
	if v, err := s.GetConfig("points_tokens_rate"); err == nil && v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil && n > 0 {
			return n
		}
	}
	return 300
}

// PointsFromTokens 内部计量 token → 积分（展示换算，四舍五入；0 保持 0）。
func (s *Store) PointsFromTokens(tokens int64) int64 {
	r := s.PointsTokensRate()
	if r <= 0 || tokens <= 0 {
		return 0
	}
	return (tokens + r/2) / r
}

// TokensFromPoints 积分 → 内部计量 token（写入口折算：对外接口只收积分，落库仍按 token）。
// 负值归零；rate<=0 时按默认 300 口径（与 PointsTokensRate 兜底一致，防配置异常丢额度）。
func (s *Store) TokensFromPoints(points int64) int64 {
	if points <= 0 {
		return 0
	}
	r := s.PointsTokensRate()
	if r <= 0 {
		r = 300
	}
	return points * r
}

// MarkupMultiplier 成本均摊系数（billing_markup_multiplier，默认 1.5，强制 ≥1.0）。
// 对外计费与权益发放统一乘以该系数：扣费侧（用量实时计量）与入账侧（包订单发放）共用同一口径，
// 保证「1 入账 token = 1 扣费 token」的单位一致；后台可调。
func (s *Store) MarkupMultiplier() float64 {
	m := ops.DefaultMarkupMultiplier() // ★ C6 默认值单一来源
	if v, err := s.GetConfig("billing_markup_multiplier"); err == nil && v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil && f >= 1.0 {
			m = f
		}
	}
	return m
}

// ExpirePackage 摘除租户订阅身份（订阅到期由后台扫描调用）：
// 清空 package_code/package_expires_at 与提醒标记；句数余额保留（已购句数为买断资产）。
// 参数：tid=租户 ID，返回被摘除的包编码（审计留痕用）与错误。
//
// ★ 整改 B1（2026-08-26）：json_set 单语句只摘订阅身份四键——此前「读整包→内存改→
// 整体覆盖」在 watchdog 扫描与用户并发购买/充值之间丢失更新（句数被旧快照抹掉）。
// 句数余额 sentence_balance 等其余键原样保留，不再参与本次写入。
func (s *Store) ExpirePackage(tid int64) (code string, err error) {
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return "", err
	}
	code = perms.PackageCode
	if code == "" {
		return "", nil // 无订阅无需摘除
	}
	// ★ A2/S3（2026-09-12 PG 方言修复）：多键原子摘除改经 db.JSONPatchSet 合并补丁——
	//   旧写法内联 SQLite JSON1 json_set，PG 下整条 UPDATE 报错，导致每日到期摘除静默失败。
	patch, _ := json.Marshal(map[string]any{
		"package_code": "", "package_expires_at": "", "notified_exp7": false, "notified_exp1": false,
	})
	d := db.CurrentDialect()
	_, err = db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		string(patch), time.Now().Format(time.RFC3339), tid)
	if err != nil {
		return "", err
	}
	return code, nil
}

// SetNotifiedExpFlag 到期提醒去重标记位（★ 整改 B1：json_set 单字段原子更新，
// 替代 watchdog「读整包→改一位→整体覆盖写回」的丢失更新窗口；flag 取
// notified_exp7 / notified_exp1 / notified_exp3（体验台账到期前3天，任务2.5））。
func (s *Store) SetNotifiedExpFlag(tid int64, flag string) error {
	if flag != "notified_exp7" && flag != "notified_exp1" && flag != "notified_exp3" && flag != "notified_renew3" {
		return &errTxt{"非法提醒标记: " + flag}
	}
	// ★ A2/S3（2026-09-12）：置 true 走 JSONPatchSet（flag 已经白名单校验，键名安全）。
	d := db.CurrentDialect()
	patch := `{"` + flag + `":true}`
	_, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		patch, time.Now().Format(time.RFC3339), tid)
	return err
}

// SaveTenantPerms 持久化租户权限 JSON（整体覆盖 tenants.permissions 列）。
// 参数：tid=租户 ID，perms=待保存的权限结构体；返回错误。
func (s *Store) SaveTenantPerms(tid int64, perms *tenant.Perms) error {
	b, _ := json.Marshal(perms)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE tenants SET permissions=?, updated_at=? WHERE id=?",
		string(b), time.Now().Format(time.RFC3339), tid)
	return err
}
