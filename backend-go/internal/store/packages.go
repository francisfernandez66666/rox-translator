// ============ 本文件职责中文说明 ============
// 商业包数据访问层（packages 表）：付费包 / 增量包 / 免费体验包的 CRUD，
// 以及租户句数余额（sentence_balance，存于 tenants.permissions JSON）的读写。
// 商业包模型：
//   - free（免费体验）：新租户注册自动开通，句数由 trial_sentences 配置（默认 500）
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

	"translator/internal/tenant"
)

// Package 商业包实体（packages 表）
type Package struct {
	ID           int64   `json:"id"`            // 包主键 ID
	TenantID     int64   `json:"tenant_id"`     // 租户 ID（0=平台）
	Code         string  `json:"code"`          // 包编码（唯一，如 trial/monthly_1000/inc_500）
	Name         string  `json:"name"`          // 包名称（如 包月 1000 句）
	PType        string  `json:"ptype"`         // 包类型：free(免费体验) / paid(付费包) / increment(增量包)
	Sentences    int64   `json:"sentences"`     // 包内含翻译句数
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
const packageCols = "id, tenant_id, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at"

// CreatePackage 创建商业包（超管）。
// 参数：pkg=待创建的包对象（code/name/ptype 必填）；返回新包对象。
func (s *Store) CreatePackage(pkg *Package) (*Package, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO packages (tenant_id, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		pkg.TenantID, pkg.Code, pkg.Name, pkg.PType, pkg.Sentences, pkg.PriceMoney, pkg.DurationDays, pkg.Enabled, pkg.SortOrder, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetPackage(id)
}

// GetPackage 按 ID 查询商业包。
// 参数：id=包主键 ID；返回包对象。
func (s *Store) GetPackage(id int64) (*Package, error) {
	var p Package
	err := s.db.QueryRow("SELECT "+packageCols+" FROM packages WHERE id=?", id).
		Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPackageByCode 按编码查询商业包。
// 参数：code=包编码；返回包对象。
func (s *Store) GetPackageByCode(tenantID int64, code string) (*Package, error) {
	var p Package
	err := s.db.QueryRow("SELECT "+packageCols+" FROM packages WHERE tenant_id=? AND code=?", tenantID, code).
		Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListCommercialPackages 列出全部商业包（超管管理用，含下架包）。
// 参数：无；返回包列表（按类型、排序、ID 排序）。
func (s *Store) ListCommercialPackages() ([]*Package, error) {
	rows, err := s.db.Query("SELECT " + packageCols + " FROM packages ORDER BY ptype, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		var p Package
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// ListEnabledCommercialPackages 列出上架中的商业包（公开定价页 / 注册订阅用）。
// 参数：无；返回已启用的包列表（按类型、排序、ID 排序）。
func (s *Store) ListEnabledCommercialPackages() ([]*Package, error) {
	rows, err := s.db.Query("SELECT " + packageCols + " FROM packages WHERE enabled=1 ORDER BY ptype, sort_order, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Package
	for rows.Next() {
		var p Package
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// UpdatePackage 更新商业包（超管）。
// 参数：pkg=待更新的包对象（全部字段整体覆盖）；返回错误。
func (s *Store) UpdatePackage(pkg *Package) error {
	_, err := s.db.Exec(
		"UPDATE packages SET tenant_id=?, name=?, ptype=?, sentences=?, price_money=?, duration_days=?, enabled=?, sort_order=?, updated_at=? WHERE id=?",
		pkg.TenantID, pkg.Name, pkg.PType, pkg.Sentences, pkg.PriceMoney, pkg.DurationDays, pkg.Enabled, pkg.SortOrder, time.Now().Format(time.RFC3339), pkg.ID)
	return err
}

// DeletePackage 删除商业包（超管）。
// 参数：id=包主键 ID；返回错误。
func (s *Store) DeletePackage(id int64) error {
	_, err := s.db.Exec("DELETE FROM packages WHERE id=?", id)
	return err
}

// PackagesTenantMigrate 将 packages 表从「code 全局唯一」迁移为「(tenant_id, code) 租户级唯一」。
// 幂等：新库直接创建复合唯一；老库补 tenant_id 列后重建表完成约束替换。
func (s *Store) PackagesTenantMigrate() {
	// ① 补 tenant_id 列（老库没有）
	cols, err := s.db.Query("PRAGMA table_info(packages)")
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
		s.db.Exec("ALTER TABLE packages ADD COLUMN tenant_id INTEGER NOT NULL DEFAULT 0")
	}

	// ② 检测当前唯一约束是否仅为 code 单列；若是则重建表换为复合唯一。
	idxRows, err := s.db.Query("PRAGMA index_list(packages)")
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
		irows, err := s.db.Query("PRAGMA index_info(?)", name)
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
	tx.Exec(`CREATE TABLE packages_new (
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
	tx.Exec(`INSERT INTO packages_new (id, tenant_id, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at)
		SELECT id, 0, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at FROM packages`)
	tx.Exec(`DROP TABLE packages`)
	tx.Exec(`ALTER TABLE packages_new RENAME TO packages`)
	tx.Commit()
}

// ============ 租户句数余额 ============

// GetSentenceBalance 读取租户句数余额（来自 tenants.permissions JSON 的 sentence_balance 字段）。
// 参数：tid=租户 ID；返回剩余句数（未设置返回 0）。
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
	err := s.db.QueryRow("SELECT permissions FROM tenants WHERE id=?", tid).Scan(&raw)
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
	if _, err := tx.Exec("UPDATE tenants SET permissions=?, updated_at=? WHERE id=?", string(b), time.Now().Format(time.RFC3339), tid); err != nil {
		return err
	}
	return tx.Commit()
}

// AddSentences 增加租户句数余额（增量包购买/付费包发放时调用）。
// 参数：tid=租户 ID，n=待增加句数（需为正数）；返回新余额。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：改为 json_set 单语句原子自增，
// 不再整体覆盖 permissions JSON——并发发放/扣减不再互相踩掉对方写入。
func (s *Store) AddSentences(tid, n int64) (int64, error) {
	if n <= 0 {
		cur, _ := s.GetSentenceBalance(tid)
		return cur, nil
	}
	if _, err := s.db.Exec(
		"UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'), '$.sentence_balance', COALESCE(json_extract(permissions,'$.sentence_balance'),0)+?), updated_at=? WHERE id=?",
		n, time.Now().Format(time.RFC3339), tid); err != nil {
		return 0, err
	}
	return s.GetSentenceBalance(tid)
}

// DeductSentences 扣减租户句数余额（每次翻译按「源句×目标语言数」扣减）。
// 参数：tid=租户 ID，n=待扣减句数；余额不足时返回 ErrSentenceExhausted。
// 返回：扣减后的剩余句数。
//
// ★ 并发安全（2026-08-26 全仓评审 B3）：json_set 单语句原子自减 + WHERE 余额守卫，
// RowsAffected==0 即余额不足（或租户不存在）——守卫式核销与 DeductWithGrants 同款双保险。
func (s *Store) DeductSentences(tid, n int64) (int64, error) {
	res, err := s.db.Exec(
		"UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'), '$.sentence_balance', COALESCE(json_extract(permissions,'$.sentence_balance'),0)-?), updated_at=? WHERE id=? AND COALESCE(json_extract(permissions,'$.sentence_balance'),0)>=?",
		n, time.Now().Format(time.RFC3339), tid, n)
	if err != nil {
		return 0, err
	}
	if cnt, _ := res.RowsAffected(); cnt == 0 {
		return 0, ErrSentenceExhausted
	}
	return s.GetSentenceBalance(tid)
}

// ErrSentenceExhausted 句数额度耗尽错误（免费体验句/付费包句数用完后提示购买）。
var ErrSentenceExhausted = &errTxt{"翻译句数已用尽，请购买套餐或增量包"}

// getTenantPermsTx 事务作用域的租户权限读取（GetTenantPerms 的 tx 变体）。
func getTenantPermsTx(tx *sql.Tx, tid int64) (*tenant.Perms, error) {
	p := &tenant.Perms{}
	var raw string
	err := tx.QueryRow("SELECT permissions FROM tenants WHERE id=?", tid).Scan(&raw)
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
	_, err := tx.Exec("UPDATE tenants SET permissions=?, updated_at=? WHERE id=?", string(b), time.Now().Format(time.RFC3339), tid)
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
		if tokens := granted * rate; tokens > 0 {
			// 事务内先确保余额账户行存在（等价 Charge 的 EnsureBalance 语义），
			// 再原子累加——避免账户行缺失时 UPDATE 影响 0 行导致 token 静默丢失。
			if _, err := tx.Exec(
				"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
				tid, time.Now().Format(time.RFC3339)); err != nil {
				return 0, err
			}
			if _, err := tx.Exec(
				"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
				tokens, time.Now().Format(time.RFC3339), tid); err != nil {
				return 0, err
			}
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
// ★ 并发安全（2026-08-26 全仓评审 B3）：改为 json_set 单语句原子自增。
func (s *Store) ApplyIncrementMirror(tid int64, pkg *Package) (int64, error) {
	if pkg.Sentences > 0 {
		if _, err := s.db.Exec(
			"UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'), '$.sentence_balance', COALESCE(json_extract(permissions,'$.sentence_balance'),0)+?), updated_at=? WHERE id=?",
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
	return 500
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
	_, err = s.db.Exec(`UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'),
		'$.package_code', '', '$.package_expires_at', '',
		'$.notified_exp7', json('false'), '$.notified_exp1', json('false')),
		updated_at=? WHERE id=?`,
		time.Now().Format(time.RFC3339), tid)
	if err != nil {
		return "", err
	}
	return code, nil
}

// SetNotifiedExpFlag 订阅到期提醒去重标记位（★ 整改 B1：json_set 单字段原子更新，
// 替代 watchdog「读整包→改一位→整体覆盖写回」的丢失更新窗口；flag 取
// notified_exp7 / notified_exp1）。
func (s *Store) SetNotifiedExpFlag(tid int64, flag string) error {
	if flag != "notified_exp7" && flag != "notified_exp1" {
		return &errTxt{"非法提醒标记: " + flag}
	}
	_, err := s.db.Exec(
		"UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'), '$."+flag+"', json('true')), updated_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), tid)
	return err
}

// SaveTenantPerms 持久化租户权限 JSON（整体覆盖 tenants.permissions 列）。
// 参数：tid=租户 ID，perms=待保存的权限结构体；返回错误。
func (s *Store) SaveTenantPerms(tid int64, perms *tenant.Perms) error {
	b, _ := json.Marshal(perms)
	_, err := s.db.Exec(
		"UPDATE tenants SET permissions=?, updated_at=? WHERE id=?",
		string(b), time.Now().Format(time.RFC3339), tid)
	return err
}
