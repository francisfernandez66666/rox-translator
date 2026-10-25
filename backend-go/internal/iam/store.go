// ============ 本文件职责中文说明 ============
// IAM 数据访问层：用户（users 表）与组织（orgs 表）的全部 SQL 操作。
// 用户：增删改查、按用户名/邮箱检索、密码重置、登录打点、组织范围归集；
// 组织：树形 CRUD（任意深度）、平台根组织、子孙归集、子树判断；
// 全部写操作经 execW 互斥串行化（SQLite 单写者模型，降低并发 SQLITE_BUSY）。
// 本层只做数据访问，不含 HTTP 与业务校验（角色/范围校验在 api 层完成）。
// =============================================
package iam

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
	"translator/internal/db"
)

// userCols 用户表查询列清单（Scan 顺序契约；email/deactivate_at/agreed_at/job_role 为老库可空列，COALESCE 兜底）。
// ★ 补列必须同步全部 Scan 目标（行式 Scan 有 4 处复制此清单，漏改会静默吞行——2026-08-26 事故口径）。
// ★ 2026-10-24 多身份方案 B1：尾部追加 account_id/is_home 两列（同样 COALESCE 兜底，
//
//	因为存量库这两列由 MultiIdentityMigrate 幂等补出，缺列/空值都必须读成「空账户号 + 非家行」而不是让整行 Scan 失败）。
//	列数与 Scan 目标数的等值由 user_scan_columns_gate_test.go 机械钉住（漏改一处＝当场红，不靠人记）。
const userCols = "id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, org_id, COALESCE(email,''), created_at, updated_at, COALESCE(deactivate_at,''), COALESCE(agreed_at,''), COALESCE(must_change_pwd,0), COALESCE(token_version,0), COALESCE(job_role,''), COALESCE(profile_setup_required,0), COALESCE(account_id,''), COALESCE(is_home,0)"

// orgCols 组织表查询列清单（token_limit 为老库可空列，COALESCE 兜底）。
// orgCols 组织表查询列清单（token_limit 为老库可空列，COALESCE 兜底）。
const orgCols = "id, tenant_id, parent_id, name, type, COALESCE(token_limit,0), created_at, updated_at"

// Store IAM 数据访问层（用户 + 组织）
type Store struct {
	db *sql.DB
	mu sync.Mutex // 写操作互斥：SQLite 单写者模型下降低并发 SQLITE_BUSY 概率
}

// withWriteMu 按方言决定是否串行化写（★ 2026-10-11 慢 SQL 批 P1②，收窄的落点在锁的**射程**不在写法）：
//
//   - SQLite（体验机/CI 内存库）：单写者模型，保留全局互斥——这是 mu 存在的唯一理由；
//   - PostgreSQL（生产）：多写者真并发＋唯一索引自己兜底，全局互斥反而把并发注册/
//     用户管理串成一条队——现网取证实测"持锁等库"平均 ~60ms，一条慢写拖住全部写操作。
//
// 三条写入口（write/execW/execWID）一律走这里，**禁止**再在任何方法里直接 s.mu.Lock()
// （接线锁见 store_write_lock_gate_test.go；已逐条点过：全部加锁体都是单语句或自管事务，
// 没有任何 PG 下依赖互斥的正确性序列——DeleteOrg 的多语句事务本来就在锁外）。
func (s *Store) withWriteMu(fn func()) {
	if db.CurrentDialect().IsPostgres() {
		fn()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// write 串行化执行写操作（事务/Exec 统一入口，多语句必须原子的段用它）。
// ★ 2026-10-24 多身份 B1：PromoteHomeRow 的"摘旧家 + 立新家"两条 UPDATE 是第一个现役调用点
// （保留它的理由正是这一族——将来出现"必须串行"的新写段时这里是对的落点）。
func (s *Store) write(fn func() error) error {
	var err error
	s.withWriteMu(func() { err = fn() })
	return err
}

// execW 互斥执行单条写 SQL。
func (s *Store) execW(query string, args ...interface{}) (sql.Result, error) {
	var res sql.Result
	var err error
	s.withWriteMu(func() {
		res, err = db.Exec(s.db, db.CurrentDialect(), query, args...)
	})
	return res, err
}

// execWID 执行 INSERT 并返回自增主键（跨方言；PostgreSQL 经 RETURNING 取回，避免 lib/pq 的 LastInsertId 不支持）。
func (s *Store) execWID(query string, args ...interface{}) (int64, error) {
	var id int64
	var err error
	s.withWriteMu(func() {
		id, err = db.InsertID(s.db, db.CurrentDialect(), "id", query, args...)
	})
	return id, err
}

// NewStore 构造函数：初始化并返回实例。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// scanUser 从单行结果扫描用户对象（列顺序与 userCols 严格一致）。
func scanUser(row *sql.Row) (*User, error) {
	var u User
	if err := u.assign(row); err != nil {
		return nil, err
	}
	return &u, nil
}

// scanUserRow 从多行游标扫描用户对象（列顺序契约与 scanUser 同一份，见 User.assign）。
func scanUserRow(rows *sql.Rows) (*User, error) {
	var u User
	if err := u.assign(rows); err != nil {
		return nil, err
	}
	return &u, nil
}

// ============ 用户 ============

// ============ 建身份行（★ 2026-10-24 多身份方案 B1：三条建号腿共用一份落库形态） ============

// identitySpec 建一条 users 身份行的入参。
//
// 为什么要把 CreateUser / CreateUserWithEmail / CreateIdentityRow 收成同一份 spec：
// 多身份体系里"新建一行"的**不变式**有三个（status=active、邮箱按租户查重、账户号与家标记必须自洽），
// 分散在三条腿里写迟早分叉——分叉的表现是"某条腿建出来的行没有家标记"，
// 而那种行登录后家优先取不到它，用户表现为"我明明注册成功了却登不进"。
type identitySpec struct {
	TenantID     int64
	Username     string
	PasswordHash string // 空=不持口令（成员行/OTP 账号，D4 口令只存家行）
	DisplayName  string
	Role         string
	CreatedBy    int64
	OrgID        int64
	Email        string // 只接受已归一化的小写值；空串=暂不绑定（管理台/SSO 兼容路径）
	AgreedAt     string // 空=未签协议；自动建号必须传真实时间，禁止静默置真
	// ProfileSetupRequired=1 时登录响应会提醒补全资料（可跳过，不阻断）。
	ProfileSetupRequired int
	// AccountID 空 ⇒ 这是一条**新账户**的行：落库后把账户号钉成自身 id、家标记置 1；
	// 非空 ⇒ 同一人已有的账户，本行是它的**又一个身份行**（调用方决定 IsHome）。
	AccountID string
	IsHome    int
}

// createIdentity 在**一个事务里**建一条身份行（邮箱占用判定、账户号/家标记落定都在同一事务内）。
//
// 为什么必须是一个事务：旧注册链先 CreateUser 再 SetUserEmail，第二步失败被 `_ =` 吞掉，
// 于是"账号建成了、邮箱没绑上"既回 success 又让人永远收不到验证码（AGENTS §一·8② 同族）。
//
// 邮箱查重口径（★ 多身份方案 D3）：**按租户**——同一人可在多个租户各持一行同邮箱，
// 但同一租户内一邮箱至多一行（DB 层另有 idx_users_email_lower_active_tenant 部分唯一索引兜底）。
// 参数：spec=建行走的入参。返回：新建的行（查询回读失败即回错误，不返回半成品）。
func (s *Store) createIdentity(spec identitySpec) (*User, error) {
	now := time.Now().Format(time.RFC3339)
	email := strings.ToLower(strings.TrimSpace(spec.Email))
	var id int64
	var err error
	s.withWriteMu(func() {
		var tx *sql.Tx
		if tx, err = s.db.Begin(); err != nil {
			return
		}
		defer func() { _ = tx.Rollback() }()
		d := db.CurrentDialect()
		if email != "" {
			// 只认仍占用该邮箱的行（active/deactivating）；停用/已注销的旧行不再是占用方（2026-10-21 口径）。
			var owner int64
			var ownerAccount string
			qerr := db.QueryRow(tx, d,
				"SELECT id, COALESCE(account_id,'') FROM users WHERE tenant_id=? AND lower(trim(email))=? AND status IN (?,?) LIMIT 1",
				spec.TenantID, email, UserActive, UserDeactivating).Scan(&owner, &ownerAccount)
			if qerr == nil && owner > 0 {
				// 文案分两档，因为调用方拿这句是要**直接回给用户**的：
				// 同一账户在本租户已有行 ⇒ 说"你已在该企业中"（§6.1 第五档），
				// 别的账户持有 ⇒ 说"已被其他账号绑定"（防枚举口径由 API 层统一措辞，这里只提供事实）。
				if spec.AccountID != "" && ownerAccount == spec.AccountID {
					err = fmt.Errorf("该账户在本租户已有身份")
				} else {
					err = fmt.Errorf("该邮箱已被其他账号绑定")
				}
				return
			}
		}
		id, err = db.InsertID(tx, d, "id",
			"INSERT INTO users (tenant_id, username, password_hash, display_name, role, status, created_by, org_id, "+
				"email, agreed_at, profile_setup_required, account_id, is_home, created_at, updated_at) "+
				"VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			spec.TenantID, spec.Username, spec.PasswordHash, spec.DisplayName, spec.Role, UserActive,
			spec.CreatedBy, spec.OrgID, email, spec.AgreedAt, spec.ProfileSetupRequired, spec.AccountID, spec.IsHome, now, now)
		if err != nil {
			// ★ 2026-10-21 IAM 重做：用户名不再是登录标识，也不再要求同租户唯一，
			//   所以这里**不**把唯一索引错误翻译成「用户名已存在」——那是旧语义的残留；
			//   能撞上的唯一索引只有按租户邮箱那条（D3），占用判定已在上面前置过，索引只兜底并发。
			return
		}
		// 新账户：账户号取自身 id 文本、本行即家行。CAST(id AS TEXT) 在两种方言都成立，
		// 且必须**在事务里**做——留到事务外就成了"行建了、账户号没钉"的半态。
		if spec.AccountID == "" {
			if _, err = db.Exec(tx, d, "UPDATE users SET account_id=CAST(id AS TEXT), is_home=1 WHERE id=?", id); err != nil {
				return
			}
		}
		err = tx.Commit()
	})
	if err != nil {
		return nil, err
	}
	return s.GetUser(id, spec.TenantID)
}

// CreateUser 创建用户（初始 active）。参数 tid=租户(0=平台级)，orgID=所属组织。返回新用户。
//
// ★ 2026-10-24 多身份方案 B1：新行落库后账户号钉成自身 id 文本、is_home=1（这一行的家）。
// 「一人一行」的现状下这两个值就是迁移回填会给的东西，所以对存量行为逐字节等价；
// 等一个人经邀请码/邀请链接长出第二行时，第二行走 CreateIdentityRow 显式带既有账户号且 is_home=0。
func (s *Store) CreateUser(tid int64, username, passHash, displayName, role string, createdBy, orgID int64) (*User, error) {
	return s.createIdentity(identitySpec{
		TenantID: tid, Username: username, PasswordHash: passHash, DisplayName: displayName,
		Role: role, CreatedBy: createdBy, OrgID: orgID,
	})
}

// CreateUserWithEmail 在一个事务里创建用户并绑定邮箱/协议/首登资料标记（列含义见 identitySpec）。
//
// ★ 2026-10-21 IAM 重做：自动建号唯一入口，禁止调用方再拆成 CreateUser + SetUserEmail（后者失败会被吞）。
// ★ 2026-10-24 多身份 B1：邮箱占用判定从**全局**收到**按租户**（D3）；
// 调用方（注册/验证码登录）仍在 API 层按"邮箱跨租户唯一"这一对外契约出 409，
// 所以现网可观察行为不变，放开的是"同一个人经邀请加入另一租户"这条新路径（B3 接线）。
func (s *Store) CreateUserWithEmail(tid int64, username, passHash, displayName, role string, createdBy, orgID int64, email, agreedAt string, profileSetupRequired int) (*User, error) {
	return s.createIdentity(identitySpec{
		TenantID: tid, Username: username, PasswordHash: passHash, DisplayName: displayName,
		Role: role, CreatedBy: createdBy, OrgID: orgID,
		Email: email, AgreedAt: agreedAt, ProfileSetupRequired: profileSetupRequired,
		IsHome: 1, // 这是账户的第一行，也就是家行
	})
}

// CreateIdentityRow 为**已存在的账户**新建一条身份行（多身份方案 §6.1：受邀加入企业的成员行）。
//
// 三条与"新建账户"不同的硬口径：
//   - accountID 必须传既有账户号 ⇒ 本行 is_home=0，**不是**家（家行仍在那条既有行上）；
//   - 口令留空（D4：口令只存家行，成员行是纯身份记录、不含凭证）；
//   - must_change_pwd 与 profile_setup_required 都不置位——这两条闸都挂在家行验口令那一刻，
//     成员行带上它们只会让用户在切换后莫名被要求"先改密码"。
//
// 参数：accountID=既有账户号（不可空，空则报错）；其余同建号入参。返回：新建的成员行。
func (s *Store) CreateIdentityRow(accountID string, tid int64, username, displayName, role string, createdBy, orgID int64, email string) (*User, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("新建身份行必须带既有账户号（空账户号请走 CreateUserWithEmail）")
	}
	return s.createIdentity(identitySpec{
		TenantID: tid, Username: username, DisplayName: displayName, Role: role,
		CreatedBy: createdBy, OrgID: orgID, Email: strings.ToLower(strings.TrimSpace(email)),
		AccountID: accountID, IsHome: 0,
	})
}

// GetUser 按 ID+租户查询用户（租户隔离校验）。
func (s *Store) GetUser(id, tid int64) (*User, error) {
	return scanUser(db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE id=? AND tenant_id=?", id, tid))
}

// GetUserByUsername 同租户内按用户名查询。
func (s *Store) GetUserByUsername(tid int64, username string) (*User, error) {
	return scanUser(db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE username=? AND tenant_id=?", username, tid))
}

// GetUserByEmail 跨租户按邮箱查询（找回密码用；邮箱小写归一化）。
//
// ★ 2026-10-24 多身份方案 B1 §四「家优先解析」：同一个人现在可以在多个租户各持一行同邮箱，
// 所以取哪一行不再是"随便挑一个"——排序改成
//
//	is_home 优先 → status（active 先于 deactivating）→ tenant_id → id
//
// 「一人一行」的现状下 is_home 恒为 1（迁移回填），这个排序与改造前逐字同效；
// 只有长出多身份行之后它才开始发挥作用：登录永远定位到家行（口令只存家行，D4）。
func (s *Store) GetUserByEmail(email string) (*User, error) {
	// ★ 2026-10-21 IAM 重做：邮箱是登录外部标识，**只认当前仍占用它的账号**
	// （active/deactivating）。停用/已注销的旧账号不再是占用方，其邮箱可被新空白账号复用；
	// 这里若把 disabled 行也返回，验证码登录会把「邮箱可复用」判成「账号已停用」而永久挡住新号。
	return scanUser(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+userCols+" FROM users WHERE lower(trim(email))=? AND status IN (?,?) ORDER BY COALESCE(is_home,0) DESC, CASE WHEN status=? THEN 0 ELSE 1 END, tenant_id, id LIMIT 1",
		strings.ToLower(strings.TrimSpace(email)), UserActive, UserDeactivating, UserActive))
}

// GetUserByEmailInTenant 按「租户 + 邮箱」取人（多身份方案 §6.1 注册分流用）。
//
// 为什么要有这一条而不复用 GetUserByEmail：跨租户取回的是**家行**，而分流要先回答
// 「这个邮箱在**这个企业租户**里是不是已经有人了」——那是按租户的那把尺子（D3）。
// 用跨租户的结果去判按租户的冲突，会把"别人在自家租户用这个邮箱"读成"他已经在这企业里"。
func (s *Store) GetUserByEmailInTenant(email string, tid int64) (*User, error) {
	return scanUser(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+userCols+" FROM users WHERE tenant_id=? AND lower(trim(email))=? AND status IN (?,?) ORDER BY id LIMIT 1",
		tid, strings.ToLower(strings.TrimSpace(email)), UserActive, UserDeactivating))
}

// ListAccountsByEmail 列出仍占用该邮箱的**全部**租户行（active/deactivating，家行在前）。
// 给身份列表与防枚举文案共用：它问的是"这个邮箱背后有哪几个身份"，不是"哪一个"。
func (s *Store) ListAccountsByEmail(email string) ([]*User, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return nil, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+userCols+" FROM users WHERE lower(trim(email))=? AND status IN (?,?) ORDER BY COALESCE(is_home,0) DESC, tenant_id, id",
		e, UserActive, UserDeactivating)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		if u, err := scanUserRow(rows); err == nil {
			out = append(out, u)
		}
	}
	return out, nil
}

// DeactivateSelf 自助注销：仅普通用户且 active 可发起；置 deactivating + 请求日期。
func (s *Store) DeactivateSelf(id, tid int64) error {
	res, err := s.execW(
		"UPDATE users SET status=?, deactivate_at=? WHERE id=? AND tenant_id=? AND role=? AND status=?",
		UserDeactivating, time.Now().Format("2006-01-02"), id, tid, RoleUser, UserActive)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("仅普通用户可自助注销，且账号需处于启用状态")
	}
	return nil
}

// FinalizeDeactivation 宽限期届满后落停用态（幂等；登录/鉴权路径惰性触发，失败静默）。
func (s *Store) FinalizeDeactivation(id, tid int64) {
	s.execW("UPDATE users SET status=? WHERE id=? AND tenant_id=? AND status=?", UserDisabled, id, tid, UserDeactivating)
}

// SetUserEmail 设置联系邮箱。
//
// ★ 2026-10-24 多身份方案 B1（§十一「SetUserEmail 同步全 account」）：邮箱属于**人**不属于某一行，
// 所以换绑要把该账户的全部可用行一起改写——只改家行会留下"家行是新邮箱、企业行还挂着旧邮箱"，
// 而旧邮箱随即可以被别人注册，那个人从此在企业租户里也能被验证码登录命中（同族"多把尺子"缺陷）。
//
// 判据两档，按 account_id 是否已回填分派：
//   - 已回填 ⇒ 撞库检查只在**本账户有行的那些租户**里查别的账户（D3 按租户唯一）；
//     写法同步全账户的可用行（active/deactivating）；
//   - 未回填（历史库还没跑迁移，或迁移失败）⇒ 逐字退回改造前的单行行为，
//     绝不写 `WHERE account_id=”`（那会把库里**所有**未迁移行一起改掉，是比缺陷更坏的事）。
//
// 空邮箱=解绑（不做唯一校验）。参数：id=行主键，tid=该行租户（租户隔离校验），email=目标邮箱。
func (s *Store) SetUserEmail(id, tid int64, email string) error {
	d := db.CurrentDialect()
	email = strings.ToLower(strings.TrimSpace(email))
	var accountID string
	// 取本行的账户号：读不到（行不存在/列缺失）就当"未迁移"，走老写法。
	_ = db.QueryRow(s.db, d, "SELECT COALESCE(account_id,'') FROM users WHERE id=? AND tenant_id=?", id, tid).Scan(&accountID)
	if accountID == "" {
		if email == "" {
			_, err := s.execW("UPDATE users SET email='' WHERE id=? AND tenant_id=?", id, tid)
			return err
		}
		var owner int64
		if err := db.QueryRow(s.db, d,
			"SELECT id FROM users WHERE lower(trim(email))=? AND status IN (?,?) AND id<>? LIMIT 1",
			email, UserActive, UserDeactivating, id).Scan(&owner); err == nil && owner != id {
			return fmt.Errorf("该邮箱已被其他账号绑定")
		}
		_, err := s.execW("UPDATE users SET email=? WHERE id=? AND tenant_id=?", email, id, tid)
		return err
	}
	if email != "" {
		// 本账户在每个租户各有一行，所以"新邮箱在别处已被占用"只要命中**其中任一租户**就必须整体拒绝：
		// 部分成功会造出"同一个人在两个租户是两个邮箱"，而登录按邮箱取家行——家行落在哪个租户就此不确定。
		var owner int64
		if err := db.QueryRow(s.db, d,
			`SELECT u.id FROM users u
			 WHERE lower(trim(u.email))=? AND u.status IN (?,?) AND u.account_id<>?
			   AND u.tenant_id IN (SELECT tenant_id FROM users WHERE account_id=?) LIMIT 1`,
			email, UserActive, UserDeactivating, accountID, accountID).Scan(&owner); err == nil && owner > 0 {
			return fmt.Errorf("该邮箱已被其他账号绑定")
		}
	}
	// 同步写全账户可用行。停用行不跟着改：它们已经不是邮箱占用方（2026-10-21 口径），
	// 给它们写新邮箱等于把"这个人退出过的那家企业"重新和一个活邮箱关联上。
	_, err := s.execW("UPDATE users SET email=? WHERE account_id=? AND status IN (?,?)", email, accountID, UserActive, UserDeactivating)
	return err
}

// GetSuperAdminByUsername 按用户名查平台级管理员（role IN admin/super_admin）。
func (s *Store) GetSuperAdminByUsername(username string) (*User, error) {
	return scanUser(db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE username=? AND role IN ('admin','super_admin') LIMIT 1", username))
}

// GetUserByUsernameGlobal 跨全部租户按用户名查询（登录时多租户重名判定用）。
func (s *Store) GetUserByUsernameGlobal(username string) ([]*User, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE username=? ORDER BY tenant_id", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		// 列→字段映射只有一处口径（User.assign），补列时不必再逐条追平复制清单
		if u, err := scanUserRow(rows); err == nil {
			out = append(out, u)
		}
	}
	return out, nil
}

// ListUsers 列出本租户全部用户（密码哈希脱敏）。
func (s *Store) ListUsers(tid int64) ([]*User, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE tenant_id=? ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		// 列→字段映射只有一处口径（User.assign）；列表类返回一律脱敏口令哈希
		u, err := scanUserRow(rows)
		if err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, u)
	}
	return out, nil
}

// UpdateUser 更新显示名/角色/状态/组织归属。
func (s *Store) UpdateUser(id, tid int64, displayName, role, status string, orgID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.execW(
		"UPDATE users SET display_name=?, role=?, status=?, org_id=?, updated_at=? WHERE id=? AND tenant_id=?",
		displayName, role, status, orgID, now, id, tid)
	return err
}

// SetUserOrg 仅更新所属组织（组织树调整时用）。
func (s *Store) SetUserOrg(id, tid, orgID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.execW("UPDATE users SET org_id=?, updated_at=? WHERE id=? AND tenant_id=?", orgID, now, id, tid)
	return err
}

// ListUsersByOrg 列出组织 ID 集合内全部用户（空集合=本租户全部）。
func (s *Store) ListUsersByOrg(tid int64, orgIDs []int64) ([]*User, error) {
	var rows *sql.Rows
	var err error
	if len(orgIDs) == 0 {
		rows, err = db.Query(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE tenant_id=? ORDER BY id", tid)
	} else {
		phs := ""
		args := []interface{}{tid}
		for i, id := range orgIDs {
			if i > 0 {
				phs += ","
			}
			phs += "?"
			args = append(args, id)
		}
		rows, err = db.Query(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users WHERE tenant_id=? AND org_id IN ("+phs+") ORDER BY id", args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		// 列→字段映射只有一处口径（User.assign）；列表类返回一律脱敏口令哈希
		u, err := scanUserRow(rows)
		if err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, u)
	}
	return out, nil
}

// ResetPassword 写口令并递增 token_version（★ B2 会话撤销：该用户全部已签发 JWT 立即失效）。
//
// ★ 2026-10-21 IAM 重做 P0-2：返回值从 `error` 改成 `(int64 受影响行数, error)`。
//
//	旧形态把 `UPDATE` 的结果整个丢掉，于是"一行都没改到"（id 与 tenant_id 不同源、
//	账号已被删、平台上下文打租户行）在调用方眼里与"改成功了"完全同形，
//	handler 照回 `{"success":true}` —— 用户看到"密码已重置"，下一次登录却吃 401。
//	这正是 AGENTS §一·8② 点名的形态（0 行影响不是错误，所以必须由显式判据出）。
//	⇒ 调用方**必须**两件事都做：写之前用 GetUser 做存在性前置（查不到 ⇒ 404，
//	  租户隔离下不返 403、不泄露他租户存在性），写之后把 0 行判成失败。
//	⚠️ 不许把"0 行"写进注释当契约（那等于把缺陷登记成设计）。
func (s *Store) ResetPassword(id, tid int64, passHash string) (int64, error) {
	now := time.Now().Format(time.RFC3339)
	// ★ B2：改密同时递增 token_version——该用户全部已签发 JWT 立即失效（会话撤销）
	res, err := s.execW("UPDATE users SET password_hash=?, token_version=token_version+1, updated_at=? WHERE id=? AND tenant_id=?", passHash, now, id, tid)
	if err != nil {
		return 0, err
	}
	n, nerr := res.RowsAffected()
	if nerr != nil {
		// 驱动不回行数（个别方言/桩）时按"改到了"处理但把错误原样带出去，
		// 由调用方的存在性前置那一腿保证不会把"没改到"写成成功。
		return -1, nerr
	}
	return n, nil
}

// SetPasswordForAccount 把口令写到该账户的**家行**（★ 2026-10-24 多身份方案 D5；D4 口令只存 home 行）。
//
// 这是「一个人一个口令」的机械落点：profileSetup / 改密 / 找回密码 / 管理台重置四条写口令的腿
// 一律改走这里，**禁止**再按 (id,tid) 各写一遍——按行写会造出"家行新哈希、成员行旧哈希"，
// 而旧哈希还留在库里就是登录 legacy 腿的一条可用凭证（方案 §十三 R1 备选 A 的病根）。
//
// 三条附带口径：
//   - 同步清掉家行的 must_change_pwd 与 profile_setup_required：用户刚亲手设过口令，
//     这两把闸都该当场关（方案 D4「成员行口令空、强制改密标记为 0、待补全标记清零」在家行侧的镜像）；
//   - token_version 只在家行 +1（D7 拍板：改密撤销家行会话，企业行会话不受影响，换设备重登即可）；
//   - 返回值沿用 P0-2 的显式行数判据，0 行＝这个账户没有家行（迁移没跑或数据被并坏），
//     调用方必须当失败，不许回 success。
//
// 参数：accountID=账户号（不可空）；passHash=bcrypt 哈希。返回：受影响行数（合法值恒 0 或 1）。
func (s *Store) SetPasswordForAccount(accountID, passHash string) (int64, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, fmt.Errorf("改口令必须带账户号")
	}
	now := time.Now().Format(time.RFC3339)
	res, err := s.execW(
		"UPDATE users SET password_hash=?, must_change_pwd=0, profile_setup_required=0, token_version=token_version+1, updated_at=? "+
			"WHERE account_id=? AND is_home=1", passHash, now, accountID)
	if err != nil {
		return 0, err
	}
	n, nerr := res.RowsAffected()
	if nerr != nil {
		// 与 ResetPassword 同一口径：驱动不回行数时把错误带出去，由调用方的存在性前置出判决。
		return -1, nerr
	}
	return n, nil
}

// GetHomeRow 取账户的家行（登录定位、口令写入、身份列表默认身份都用这一条）。
// 参数：accountID=账户号。返回：sql.ErrNoRows 表示该账户没有家行（迁移未覆盖，调用方按"无账户"处理）。
func (s *Store) GetHomeRow(accountID string) (*User, error) {
	return scanUser(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+userCols+" FROM users WHERE account_id=? AND is_home=1 ORDER BY id LIMIT 1", strings.TrimSpace(accountID)))
}

// ListIdentitiesByAccount 列出一个账户的全部身份行（家行在前，其余按租户、ID 升序）。
// 给 GET /api/auth/identities 与登录响应的 identities 数组用；口令哈希一律不出 IAM 层。
func (s *Store) ListIdentitiesByAccount(accountID string) ([]*User, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+userCols+" FROM users WHERE account_id=? ORDER BY COALESCE(is_home,0) DESC, tenant_id, id", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		// 列→字段映射只有一处口径（User.assign）；身份列表不含凭证
		u, err := scanUserRow(rows)
		if err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, u)
	}
	return out, nil
}

// PromoteHomeRow 把指定行提升为该账户的家行，并摘掉该账户其它行的家标记（§6.2 移除成员时用）。
//
// 成立场景很具体：纯企业用户（管理台邀请的新邮箱，企业行就是家行）被管理员移除 ⇒
// 停用的正是家行，必须先立一个新的家（个人行），否则这个账户从此没有家行，
// 而 D2 不变式规定「只要有 active 行就必须有且仅有一个家行」——家优先解析也就无行可取。
//
// 判据按**账户**不按租户，所以这一步只在同一人的行之间挪标记，绝不会把别账户的行抬成家。
// 参数：accountID=账户号；rowID=要成为家的行（必须属于该账户）。返回：写库错误或"行不属于该账户"。
func (s *Store) PromoteHomeRow(accountID string, rowID int64) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return fmt.Errorf("提升家行必须带账户号")
	}
	var owner string
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(account_id,'') FROM users WHERE id=?", rowID).Scan(&owner); err != nil {
		return fmt.Errorf("待提升的身份行不存在: %w", err)
	}
	if owner != accountID {
		return fmt.Errorf("待提升的身份行不属于该账户")
	}
	// s.write 是「必须串行的多语句写」段的既有入口（SQLite 单写者模型下与写入口共用同一把锁；
	// PostgreSQL 下直接执行——两条 UPDATE 各自原子，家标记的中间态只可能出现在同一连接的两次语句之间，
	// 而按租户/按账户的唯一性都由索引与调用方前置保证，不依赖这段的原子性）。
	return s.write(func() error {
		d := db.CurrentDialect()
		if _, err := db.Exec(s.db, d, "UPDATE users SET is_home=0 WHERE account_id=? AND is_home<>0", accountID); err != nil {
			return err
		}
		_, err := db.Exec(s.db, d, "UPDATE users SET is_home=1 WHERE id=? AND account_id=?", rowID, accountID)
		return err
	})
}

// TouchLogin 记录最近登录时间（失败静默）。
func (s *Store) TouchLogin(id int64) {
	_, _ = s.execW("UPDATE users SET last_login_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
}

// SetUserAgreed 记录用户协议+隐私协议签署时间（注册勾选同意时写入；空串=撤销/未签署）。
func (s *Store) SetUserAgreed(id, tid int64, at string) error {
	_, err := s.execW("UPDATE users SET agreed_at=?, updated_at=? WHERE id=? AND tenant_id=?", at, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// SetMustChangePwd 设置/清除首登强制改密标记（flag=1 开启；0 关闭）。
// ★ Excel 批量导入用户（2026-09-02 功能）：建号后置 1，用户改密成功后自动清零。
func (s *Store) SetMustChangePwd(id, tid int64, flag int) error {
	_, err := s.execW("UPDATE users SET must_change_pwd=?, updated_at=? WHERE id=? AND tenant_id=?", flag, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// SetJobRole 更新用户职业角色编码（2026-09-19 需求；code=角色包 persona code，空串=清除）。
// 角色仅绑定用户层级（users.job_role），不随企业归属变化——退出企业仍存在，可自行维护（转岗语义）。
// SetUsername 更新用户名与展示名（首登资料初始化用）。用户名已不要求唯一，
// 因此这里不做撞重翻译；空值由调用方拦截。
func (s *Store) SetUsername(id, tid int64, username string) error {
	_, err := s.execW("UPDATE users SET username=?, display_name=?, updated_at=? WHERE id=? AND tenant_id=?",
		username, username, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// SetProfileSetupRequired 设置/清除首登资料提醒标记（1=下次登录继续提醒；0=资料已完成）。
// ★ 只动 profile_setup_required，不碰 must_change_pwd——两者一个是可跳过提醒、一个是 428 强制闸。
func (s *Store) SetProfileSetupRequired(id, tid int64, flag int) error {
	_, err := s.execW("UPDATE users SET profile_setup_required=?, updated_at=? WHERE id=? AND tenant_id=?",
		flag, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// ClearUserPassword 清空密码哈希（自动建号尚未设置密码时使用；OTP 仍可登录）。
func (s *Store) ClearUserPassword(id, tid int64) error {
	_, err := s.execW("UPDATE users SET password_hash='', token_version=token_version+1, updated_at=? WHERE id=? AND tenant_id=?",
		time.Now().Format(time.RFC3339), id, tid)
	return err
}

// SetJobRole 更新用户职业角色编码（code 为空表示清除；租户隔离，不跨租户写）。
func (s *Store) SetJobRole(id, tid int64, code string) error {
	_, err := s.execW("UPDATE users SET job_role=?, updated_at=? WHERE id=? AND tenant_id=?", code, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// SetPreferredLang 更新用户界面语言偏好（★ F-17 批E 2026-09-25；lang=12 语种白名单码，空串=清除/未选）。
// 唯一写入点是注册链路（app_lang 随注册落库）；登录/切语种不回写——邮件语种只要求「注册时的界面语言」口径。
func (s *Store) SetPreferredLang(id, tid int64, lang string) error {
	_, err := s.execW("UPDATE users SET preferred_lang=?, updated_at=? WHERE id=? AND tenant_id=?", lang, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// GetPreferredLang 读取用户界面语言偏好（★ F-17 批E）。
// 缺列老库/查无用户一律回空串（中文链路），错误透传给调用方自行降级。
func (s *Store) GetPreferredLang(id int64) (string, error) {
	var lang string
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(preferred_lang,'') FROM users WHERE id=?", id).Scan(&lang); err != nil {
		return "", err
	}
	return lang, nil
}

// EnsureAdmin 确保平台超管初始账号存在（幂等；已存在同名普通账号则提升为超管）。
// 若 email 非空，则确保该账号绑定了对应邮箱（新建时写入；已存在时更新）。
func (s *Store) EnsureAdmin(tid int64, username, passHash, displayName, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if su, err := s.GetSuperAdminByUsername(username); err == nil && su != nil {
		if email != "" {
			// ★ 用户名已可重名，写路径只能按不可变主键定位；按 username 更新会一次改到多行。
			_, _ = s.execW("UPDATE users SET email=? WHERE id=?", email, su.ID)
		}
		return nil
	}
	if u, err := s.GetUserByUsername(tid, username); err == nil && u != nil {
		_, _ = s.execW("UPDATE users SET tenant_id=0, role=? WHERE id=?", RoleAdmin, u.ID)
		if email != "" {
			_, _ = s.execW("UPDATE users SET email=? WHERE id=?", email, u.ID)
		}
		return nil
	}
	_, err := s.CreateUser(0, username, passHash, displayName, RoleAdmin, 0, 0)
	if err != nil {
		return err
	}
	if email != "" {
		u, _ := s.GetUserByUsername(0, username)
		if u != nil {
			_, _ = s.execW("UPDATE users SET email=? WHERE id=?", email, u.ID)
		}
	}
	return nil
}

// ============ 组织 ============

// CreateOrg 创建组织/部门节点。
func (s *Store) CreateOrg(tid, parentID int64, name, orgType string) (*Org, error) {
	if orgType == "" {
		orgType = OrgTypeOrg
	}
	now := time.Now().Format(time.RFC3339)
	id, err := s.execWID("INSERT INTO orgs (tenant_id, parent_id, name, type, created_at, updated_at) VALUES (?,?,?,?,?,?)", tid, parentID, name, orgType, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetOrgByID(id)
}

// GetOrgByID 按主键查询组织。
func (s *Store) GetOrgByID(id int64) (*Org, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+orgCols+" FROM orgs WHERE id=?", id)
	var o Org
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.TokenLimit, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// EnsureRootOrg 确保租户根组织行存在（幂等）。
func (s *Store) EnsureRootOrg(tid int64, name string) (*Org, error) {
	root, err := s.GetRootOrg(tid)
	if err == nil {
		return root, nil
	}
	if name == "" {
		name = "组织"
	}
	return s.CreateOrg(tid, 0, name, OrgTypeRoot)
}

// GetRootOrg 查询租户根组织行。
func (s *Store) GetRootOrg(tid int64) (*Org, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+orgCols+" FROM orgs WHERE tenant_id=? AND type='root' ORDER BY id LIMIT 1", tid)
	var o Org
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.TokenLimit, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// EnsurePlatformRootOrg 确保平台根组织存在（tenant_id=0，超管专属，幂等）。
func (s *Store) EnsurePlatformRootOrg(name string) (*Org, error) {
	root, err := s.GetRootOrg(0)
	if err == nil {
		return root, nil
	}
	if name == "" {
		name = "平台"
	}
	return s.CreateOrg(0, 0, name, OrgTypeRoot)
}

// GetPlatformRootOrg 查询平台根组织。
func (s *Store) GetPlatformRootOrg() (*Org, error) {
	return s.GetRootOrg(0)
}

// ListPlatformOrgs 平台全量组织视图：INNER JOIN 租户表防孤儿；
// 租户根挂到平台根下、直属组织重挂到本租户根（修复断链），供前端组装平台树。
// includePersonal=false（平台树默认档）时在 SQL 层排除个人租户：个人注册会持续产生独立租户，
// 平铺进平台组织树会无限增长（2026-10-23 报障）。第二个返回值＝个人租户根数量（与是否包含无关），
// 供前端「显示个人租户（N）」开关展示——计数取 DISTINCT tenant_id，不是 org 行数。
func (s *Store) ListPlatformOrgs(platformRootID int64, includePersonal bool) ([]*Org, int, error) {
	// INNER JOIN tenants：已删除租户的孤儿组织不再出现在平台树
	// ★ 修复（2026-08-26 P1-b）：SELECT 补第 8 列 token_limit——旧 SQL 只查 7 列却 Scan
	//   8 个目标，每行 Scan 报错被 continue 吞掉，导致平台组织树恒为空。
	// ★ 2026-10-23 F4：补第 9 列 t.is_personal，前端据此按租户类型收起/展开。
	where := "o.tenant_id>0"
	if !includePersonal {
		where += " AND COALESCE(t.is_personal,0)=0"
	}
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT o.id, o.tenant_id, o.parent_id, o.name, o.type, COALESCE(o.token_limit,0), o.created_at, o.updated_at, COALESCE(t.is_personal,0) FROM orgs o INNER JOIN tenants t ON o.tenant_id=t.id WHERE "+where+" ORDER BY o.tenant_id, CASE o.type WHEN 'root' THEN 0 ELSE 1 END, o.parent_id, o.id")
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Org
	tenantRoot := map[int64]int64{} // tenant_id → 该租户根组织 ID
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.TokenLimit, &o.CreatedAt, &o.UpdatedAt, &o.TenantIsPersonal); err != nil {
			continue
		}
		if o.Type == OrgTypeRoot {
			tenantRoot[o.TenantID] = o.ID
			o.ParentID = platformRootID // 租户根视图上挂在平台根下
		}
		out = append(out, &o)
	}
	// 关键：租户内直属根的组织（parent_id=0）重挂到本租户根组织下，否则平台树断链
	for _, o := range out {
		if o.Type != OrgTypeRoot && o.ParentID == 0 {
			if rid, ok := tenantRoot[o.TenantID]; ok {
				o.ParentID = rid
			}
		}
	}
	// 个人租户计数是辅助读数：读失败只回 0（不拖垮平台树主链路），开关会显示 0 而不是报错。
	personalTenants := 0
	if crows, cerr := db.Query(s.db, db.CurrentDialect(), "SELECT COUNT(DISTINCT o.tenant_id) FROM orgs o INNER JOIN tenants t ON o.tenant_id=t.id WHERE o.tenant_id>0 AND COALESCE(t.is_personal,0)=1"); cerr == nil {
		defer crows.Close()
		if crows.Next() {
			_ = crows.Scan(&personalTenants)
		}
	}
	return out, personalTenants, nil
}

// ListOrgs 列出租户下全部组织（扁平列表，前端组装树）。
func (s *Store) ListOrgs(tid int64) ([]*Org, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+orgCols+" FROM orgs WHERE tenant_id=? ORDER BY CASE type WHEN 'root' THEN 0 ELSE 1 END, parent_id, id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.TokenLimit, &o.CreatedAt, &o.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &o)
	}
	return out, nil
}

// RenameOrg 重命名组织（租户根改名由 api 层联动同步租户名）。
func (s *Store) RenameOrg(id int64, name string) error {
	_, err := s.execW("UPDATE orgs SET name=?, updated_at=? WHERE id=?", name, time.Now().Format(time.RFC3339), id)
	return err
}

// MoveOrg 移动节点到新父级（根不可移；含成环校验）。
func (s *Store) MoveOrg(tid, id, parentID int64) error {
	org, err := s.GetOrgByID(id)
	if err != nil {
		return err
	}
	if org.TenantID != tid {
		return fmt.Errorf("组织不属于当前租户")
	}
	if org.Type == OrgTypeRoot {
		return fmt.Errorf("根组织不可移动")
	}
	if parentID != 0 {
		p, err := s.GetOrgByID(parentID)
		if err != nil {
			return fmt.Errorf("目标组织不存在")
		}
		if p.TenantID != tid {
			return fmt.Errorf("目标组织不属于当前租户")
		}
		desc, err := s.OrgDescendantIDs(tid, id)
		if err != nil {
			return err
		}
		for _, d := range desc {
			if d == parentID {
				return fmt.Errorf("不能把组织移动到其自身或其子组织下")
			}
		}
	}
	_, err = s.execW("UPDATE orgs SET parent_id=?, updated_at=? WHERE id=?", parentID, time.Now().Format(time.RFC3339), id)
	return err
}

// DeleteOrg 删除部门/组织：成员回收至根、部门管理员自动降级为普通用户、子级上移到父级。
func (s *Store) DeleteOrg(id int64) error {
	org, err := s.GetOrgByID(id)
	if err != nil {
		return err
	}
	if org.Type == OrgTypeRoot {
		return fmt.Errorf("根组织不可删除")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// ★ 用户回收至上级组织（非根组织）：保持层级归属语义
	if _, err := db.Exec(tx, db.CurrentDialect(), "UPDATE users SET org_id=? WHERE org_id=?", org.ParentID, id); err != nil {
		return err
	}
	// 部门删除保护：被回收的部门管理员失去管理范围，自动降级为普通用户（防"幽灵管理员"）
	if _, err := db.Exec(tx, db.CurrentDialect(), "UPDATE users SET role=?, updated_at=? WHERE org_id=0 AND role=? AND tenant_id=?",
		RoleUser, time.Now().Format(time.RFC3339), RoleDeptAdmin, org.TenantID); err != nil {
		return err
	}
	if _, err := db.Exec(tx, db.CurrentDialect(), "UPDATE orgs SET parent_id=?, updated_at=? WHERE parent_id=?", org.ParentID, time.Now().Format(time.RFC3339), id); err != nil {
		return err
	}
	if _, err := db.Exec(tx, db.CurrentDialect(), "DELETE FROM orgs WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// OrgDescendantIDs 计算指定组织及全部子孙 ID 集合（BFS）。
func (s *Store) OrgDescendantIDs(tid, orgID int64) ([]int64, error) {
	all, err := s.ListOrgs(tid)
	if err != nil {
		return nil, err
	}
	children := map[int64][]int64{}
	for _, o := range all {
		children[o.ParentID] = append(children[o.ParentID], o.ID)
	}
	result := []int64{orgID}
	queue := []int64{orgID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			result = append(result, c)
			queue = append(queue, c)
		}
	}
	return result, nil
}

// OrgAncestorIDs 计算组织祖先链（自底向上）：返回 [自身, 父, 祖父, ..., 根] 有序切片。
// 用途：KB 部门包继承链——用户可见的部门包 = 祖先链上所有 org_id 的部门包，
// 距离（切片下标）越近优先级越高（2026-08-26《KB组织继承链与部门隔离改造方案》）。
// 防御：环检测（parent_id 数据异常成环时截断）+ 深度上限 20（防脏数据死循环）。
// 参数：tid=租户 ID，orgID=起始组织 ID（<=0 返回空链=未挂组织用户）。
func (s *Store) OrgAncestorIDs(tid, orgID int64) ([]int64, error) {
	if orgID <= 0 {
		return []int64{}, nil // 未挂组织：空链，仅见企业/行业/文化层
	}
	all, err := s.ListOrgs(tid)
	if err != nil {
		return nil, err
	}
	parent := map[int64]int64{} // 组织 ID → 父 ID
	for _, o := range all {
		parent[o.ID] = o.ParentID
	}
	const maxDepth = 20
	chain := make([]int64, 0, 8)
	cur := orgID
	seen := map[int64]bool{}
	for cur > 0 && len(chain) < maxDepth {
		if seen[cur] {
			break // 环检测：数据异常成环立即截断，避免死循环
		}
		seen[cur] = true
		chain = append(chain, cur)
		cur = parent[cur]
	}
	return chain, nil
}

// IsOrgInSubtree 判断目标组织是否在根组织子树内（部门管理员范围校验用）。
func (s *Store) IsOrgInSubtree(tid, rootOrgID, targetOrgID int64) (bool, error) {
	desc, err := s.OrgDescendantIDs(tid, rootOrgID)
	if err != nil {
		return false, err
	}
	for _, id := range desc {
		if id == targetOrgID {
			return true, nil
		}
	}
	return false, nil
}

// ListAllUsers 列出全部租户（含平台 0）的所有用户（超管平台根视图用）。
// 返回：用户列表（按 tenant_id,id 排序，密码哈希脱敏）。
// ListAllUsers 跨全部租户列出所有用户（超管平台根视图聚合用，脱敏）。
func (s *Store) ListAllUsers() ([]*User, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+userCols+" FROM users ORDER BY tenant_id, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		// 列→字段映射只有一处口径（User.assign）；列表类返回一律脱敏口令哈希
		u, err := scanUserRow(rows)
		if err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, u)
	}
	return out, nil
}

// OrgNameMap 全部组织 ID→名称映射（跨租户展示所属组织用）。
// OrgNameMap 全部组织 ID→名称映射（跨租户展示所属组织用）。
func (s *Store) OrgNameMap() (map[int64]string, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, name FROM orgs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		m[id] = name
	}
	return m, nil
}

// DeleteUser 删除用户账号。
// DeleteUser 删除用户账号（不存在时报错）。
func (s *Store) DeleteUser(id, tid int64) error {
	res, err := s.execW("DELETE FROM users WHERE id=? AND tenant_id=?", id, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("用户不存在")
	}
	return nil
}
