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
const packageCols = "id, code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at"

// CreatePackage 创建商业包（超管）。
// 参数：pkg=待创建的包对象（code/name/ptype 必填）；返回新包对象。
func (s *Store) CreatePackage(pkg *Package) (*Package, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO packages (code, name, ptype, sentences, price_money, duration_days, enabled, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		pkg.Code, pkg.Name, pkg.PType, pkg.Sentences, pkg.PriceMoney, pkg.DurationDays, pkg.Enabled, pkg.SortOrder, now, now)
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
		Scan(&p.ID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPackageByCode 按编码查询商业包。
// 参数：code=包编码；返回包对象。
func (s *Store) GetPackageByCode(code string) (*Package, error) {
	var p Package
	err := s.db.QueryRow("SELECT "+packageCols+" FROM packages WHERE code=?", code).
		Scan(&p.ID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
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
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.PType, &p.Sentences, &p.PriceMoney, &p.DurationDays, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
		"UPDATE packages SET name=?, ptype=?, sentences=?, price_money=?, duration_days=?, enabled=?, sort_order=?, updated_at=? WHERE id=?",
		pkg.Name, pkg.PType, pkg.Sentences, pkg.PriceMoney, pkg.DurationDays, pkg.Enabled, pkg.SortOrder, time.Now().Format(time.RFC3339), pkg.ID)
	return err
}

// DeletePackage 删除商业包（超管）。
// 参数：id=包主键 ID；返回错误。
func (s *Store) DeletePackage(id int64) error {
	_, err := s.db.Exec("DELETE FROM packages WHERE id=?", id)
	return err
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
func (s *Store) SetSentenceBalance(tid int64, balance int64) error {
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return err
	}
	perms.SentenceBalance = balance
	return s.SaveTenantPerms(tid, perms)
}

// AddSentences 增加租户句数余额（增量包购买/付费包发放时调用）。
// 参数：tid=租户 ID，n=待增加句数（需为正数）；返回新余额。
func (s *Store) AddSentences(tid, n int64) (int64, error) {
	if n <= 0 {
		cur, _ := s.GetSentenceBalance(tid)
		return cur, nil
	}
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return 0, err
	}
	perms.SentenceBalance += n
	if err := s.SaveTenantPerms(tid, perms); err != nil {
		return 0, err
	}
	return perms.SentenceBalance, nil
}

// DeductSentences 扣减租户句数余额（每次翻译按「源句×目标语言数」扣减）。
// 参数：tid=租户 ID，n=待扣减句数；余额不足时返回 ErrSentenceExhausted。
// 返回：扣减后的剩余句数。
func (s *Store) DeductSentences(tid, n int64) (int64, error) {
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return 0, err
	}
	if perms.SentenceBalance < n {
		return 0, ErrSentenceExhausted
	}
	perms.SentenceBalance -= n
	if err := s.SaveTenantPerms(tid, perms); err != nil {
		return 0, err
	}
	return perms.SentenceBalance, nil
}

// ErrSentenceExhausted 句数额度耗尽错误（免费体验句/付费包句数用完后提示购买）。
var ErrSentenceExhausted = &errTxt{"翻译句数已用尽，请购买套餐或增量包"}

// GrantPackageSentences 向租户发放商业包句数（句包外壳）：
//   - paid（付费包）：设置订阅包编码/到期时间（DurationDays>0 时计算 PackageExpires），并发放包内含句数
//   - increment（增量包）：在现有句数余额上追加包内含句数（不改订阅状态与到期）
//
// ★ Token 计费主线：发放句数的同时，按换算率（estimate_tokens_per_sentence，默认 500）
// 折算为等值 token 充入租户余额账户——余额与扣费的唯一底层单位是 token，
// 句数字段仅作订阅身份与展示镜像。
//
// 参数：tid=租户 ID，pkg=商业包对象；返回发放后的句数余额（镜像值）。
func (s *Store) GrantPackageSentences(tid int64, pkg *Package) (int64, error) {
	perms, err := s.GetTenantPerms(tid)
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
	if err := s.SaveTenantPerms(tid, perms); err != nil {
		return 0, err
	}
	// ★ 句数折算 token 入账（句包外壳→token 底层的唯一兑换点）
	if granted > 0 {
		rate := s.TokenSentenceRate()
		if tokens := granted * rate; tokens > 0 {
			_ = s.Charge(tid, tokens)
		}
	}
	return perms.SentenceBalance, nil
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
// 参数：tid=租户 ID，expiredPkg=被摘除的包编码（审计留痕用）；返回错误。
func (s *Store) ExpirePackage(tid int64) (code string, err error) {
	perms, err := s.GetTenantPerms(tid)
	if err != nil {
		return "", err
	}
	code = perms.PackageCode
	perms.PackageCode = ""
	perms.PackageExpires = ""
	perms.NotifiedExp7 = false
	perms.NotifiedExp1 = false
	return code, s.SaveTenantPerms(tid, perms)
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
