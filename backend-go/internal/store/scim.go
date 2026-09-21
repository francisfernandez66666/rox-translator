// ============================================================================
// ★ H10 SCIM 2.0 组织同步——存储层
//
//	scim_config：每租户一条（token 即 IdP 侧 Bearer 凭证；enabled 总开关）。
//	users.scim_external_id：IdP 稳定标识映射（幂等 upsert 依据）。
//
// ★ #55 缺口批（2026-09-22，报告 §4.1-7）：**Token 不再明文入库**。
//
//	原实现 token TEXT 直接存 48-hex 明文并以 `WHERE token=?` 等值查询，
//	与仓库其它凭证口径（api_keys：key_hash 鉴权 + key_enc 可回显；webhooks/model_routes：
//	enc:v1: 静态加密）不一致——拿到只读 DB 权限即等价拿到活的 IdP 凭证。
//
//	落地方案（与 api_keys 同构的「双列」口径，理由见下）：
//	  - token_hash  列 = SHA-256(明文 token) 十六进制：**鉴权等值查询走这一列**
//	    （带 `WHERE token_hash<>''` 的部分唯一索引，跨租户重复 token 直接拒写）。
//	  - token       列 = `enc:v1:` 静态密文（secret.EncryptSecret）：**回显给租户管理员**
//	    （SCIM 接入信息要能随时复制给 IdP，故必须可解密，不能只留摘要）。
//
//	WHY 不「只存哈希」一列了之：明文等值查询无法在密文上做（GCM 带随机 nonce，
//	同明文两次加密结果不同），而只存摘要又会让管理台无法再次展示凭证；
//	故摘要负责「可比对」、密文负责「可回显」，两列各司其职——与 api_keys 的
//	key_hash/key_enc 完全同构，不引入新口径。
//
//	迁移与兼容（幂等，入口只有 db.EnsureColumns / db.ExecDDL，禁止一次性 ALTER 直执行）：
//	  1) EnsureColumns 补 token_hash 列（SQLite 走 PRAGMA 检查、PG 走 IF NOT EXISTS）；
//	  2) backfillSCIMTokens 一次性把历史明文行改造为「hash + 密文」（只扫 token_hash=''
//	     的行，天然幂等，重启重复执行无副作用）；
//	  3) 读侧双轨：按 hash 命中失败时回退按明文 token 等值查询，
//	     保证「迁移窗口内仍有未回填行」时 IdP 不掉线（JWT_SECRET 缺失导致无法重加密的行也走这条轨）。
//	注：解密依赖 JWT_SECRET（生产 fail-fast 已强制设置）。若临时未设置，密钥为进程级随机值，
//	此时不回填（保持明文 + 空 hash），由读侧明文兜底轨道继续鉴权。
//
// ============================================================================
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"translator/internal/db"
	"translator/internal/observability"
)

// SCIMConfig 租户级 SCIM 服务端配置。
// Token 字段在内存中始终是**明文**（管理台回显与签发用）；落库时拆成
// token_hash（鉴权比对）+ token（enc:v1: 密文，可回显），见文件头方案说明。
type SCIMConfig struct {
	TenantID  int64  `json:"tenant_id"`
	Token     string `json:"token"`
	Enabled   bool   `json:"enabled"`
	RootOrgID int64  `json:"root_org_id"` // Group 同步挂载点（0=租户组织根）
	CreatedAt string `json:"created_at"`
	// TokenHash 明文 token 的 SHA-256 十六进制摘要（等值鉴权列）。
	// json:"-"：摘要本身不外泄（虽然不可逆，但没必要出现在任何响应体里）。
	TokenHash string `json:"-"`
}

// SCIMMigrate H10 建表/加列 + Token 存储口径改造（幂等，store.New 启动调用）。
func (s *Store) SCIMMigrate() {
	d := db.CurrentDialect()
	if err := db.ExecDDL(s.db, d, `CREATE TABLE IF NOT EXISTS scim_config (
		tenant_id INTEGER PRIMARY KEY,
		token TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 0,
		root_org_id INTEGER NOT NULL DEFAULT 0,
		created_at TEXT)`); err != nil {
		observability.Error(context.Background(), "scim_config 建表失败", "err", err.Error())
	}
	// ★ 摘要列走唯一入口幂等补列（AGENTS.md 一.4：新增列必须 db.EnsureColumns）。
	if err := db.EnsureColumns(s.db, d, "scim_config", map[string]string{
		"token_hash": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		observability.Error(context.Background(), "scim_config.token_hash 补列失败", "err", err.Error())
	}
	// 鉴权索引：部分唯一索引排除空摘要（未回填/无 token 的行不参与唯一性约束）。
	if err := db.ExecDDL(s.db, d, "CREATE UNIQUE INDEX IF NOT EXISTS idx_scim_token_hash ON scim_config(token_hash) WHERE token_hash<>''"); err != nil {
		observability.Error(context.Background(), "idx_scim_token_hash 建立失败（存量重复 token 需人工清理）", "err", err.Error())
	}
	// 历史口径的明文 token 唯一索引：改造后该列存密文（GCM 随机 nonce ⇒ 同明文不同密文），
	// 唯一性已无意义但保留不影响正确性；新库不再依赖它做去重。
	if err := db.ExecDDL(s.db, d, "CREATE UNIQUE INDEX IF NOT EXISTS idx_scim_token ON scim_config(token) WHERE token<>''"); err != nil {
		observability.Warn(context.Background(), "idx_scim_token（历史明文口径）建立失败，不影响鉴权", "err", err.Error())
	}
	// users.scim_external_id：幂等补列（原实现直接 ALTER，旧库重复启动会报错被吞；
	// 本次改动顺手收口到唯一入口，符合 AGENTS.md 一.4）。
	if err := db.EnsureColumns(s.db, d, "users", map[string]string{
		"scim_external_id": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		observability.Error(context.Background(), "users.scim_external_id 补列失败", "err", err.Error())
	}
	if err := db.ExecDDL(s.db, d, "CREATE INDEX IF NOT EXISTS idx_users_scim_ext ON users(tenant_id, scim_external_id) WHERE scim_external_id<>''"); err != nil {
		observability.Error(context.Background(), "idx_users_scim_ext 建立失败", "err", err.Error())
	}
	s.backfillSCIMTokens()
}

// backfillSCIMTokens 一次性把历史明文 token 行改造为「摘要 + 密文」（幂等，只扫未回填行）。
//
// 判定：token_hash=” 且 token 非空 ⇒ 未回填。
//   - token 为明文 ⇒ 算摘要并用 EncryptSecret 重写；
//   - token 已带 enc:v1: 前缀（异常态：写入方已改造但摘要丢失）⇒ 解密取明文再算摘要，
//     解密失败（JWT_SECRET 轮换/未设置）则跳过，留给读侧明文兜底轨道，绝不破坏原值。
func (s *Store) backfillSCIMTokens() {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT tenant_id, token FROM scim_config WHERE token<>'' AND COALESCE(token_hash,'')=''")
	if err != nil {
		return // 旧库尚无表等场景：不阻断启动
	}
	type row struct {
		tid   int64
		token string
	}
	var pending []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.tid, &r.token) == nil {
			pending = append(pending, r)
		}
	}
	_ = rows.Close()
	for _, r := range pending {
		plain := r.token
		if strings.HasPrefix(plain, SecretEncPrefix) {
			if got := DecryptSecret(plain); got != "" {
				plain = got
			} else {
				continue // 无法解密（密钥不一致）：保持原样，避免把凭证洗成不可用
			}
		}
		if err := s.writeSCIMTokenHash(r.tid, plain); err != nil {
			observability.Error(context.Background(), "scim_config token 口径回填失败", "tenant_id", strconv.FormatInt(r.tid, 10), "err", err.Error())
		}
	}
	if len(pending) > 0 {
		observability.Warn(context.Background(), "scim_config 历史明文 token 已收敛为摘要+密文口径", "rows", len(pending))
	}
}

// writeSCIMTokenHash 按「明文 → 摘要 + 密文」重写指定租户的 token 存储列（回填与 upsert 共用）。
func (s *Store) writeSCIMTokenHash(tid int64, plain string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE scim_config SET token=?, token_hash=? WHERE tenant_id=?", EncryptSecret(plain), HashSCIMToken(plain), tid)
	return err
}

// HashSCIMToken 计算 SCIM 令牌的等值鉴权摘要（SHA-256 十六进制）。
// 参数：token=明文令牌；返回摘要（空串原样返回空，避免把「无 token」误当成 hash(“”)）。
func HashSCIMToken(token string) string {
	if token == "" {
		return ""
	}
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// NewSCIMToken 生成租户 SCIM 令牌（48 hex，crypto/rand 高熵）。
func NewSCIMToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GetSCIMConfigByTenant 租户配置（不存在返回 nil,nil）。
// 返回的 Token 是**明文**（密文列解密所得；历史明文行原样返回），供管理台复制给 IdP。
func (s *Store) GetSCIMConfigByTenant(tid int64) (*SCIMConfig, error) {
	c := &SCIMConfig{}
	var en, root int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT tenant_id, COALESCE(token,''), COALESCE(enabled,0), COALESCE(root_org_id,0), COALESCE(created_at,''), COALESCE(token_hash,'') FROM scim_config WHERE tenant_id=?", tid).
		Scan(&c.TenantID, &c.Token, &en, &root, &c.CreatedAt, &c.TokenHash)
	if err != nil {
		return nil, nil //nolint:nilerr // 无配置=未开通
	}
	c.Enabled, c.RootOrgID = en == 1, root
	c.Token = DecryptSecret(c.Token) // 无 enc:v1: 前缀时按历史明文原样返回
	return c, nil
}

// GetSCIMConfigByToken IdP 请求鉴权：Bearer token → 配置。
//
// 双轨查询（顺序即优先级）：
//  1. token_hash 等值命中（新口径，走部分唯一索引，密文列不参与比对）；
//  2. 明文 token 等值兜底（迁移窗口内未回填的历史行、或密钥不一致导致无法重加密的行）。
//
// 参数：token=IdP 送来的明文令牌；未命中返回 (nil,nil)，调用方按 401 处理。
func (s *Store) GetSCIMConfigByToken(token string) (*SCIMConfig, error) {
	if token == "" {
		return nil, nil
	}
	const cols = "tenant_id, COALESCE(token,''), COALESCE(enabled,0), COALESCE(root_org_id,0), COALESCE(created_at,''), COALESCE(token_hash,'')"
	// 摘要轨道（新口径）
	if c, err := s.scanSCIMConfig(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+cols+" FROM scim_config WHERE token_hash=? LIMIT 1", HashSCIMToken(token))); err == nil {
		return c, nil
	}
	// 历史明文行兜底：本查询只在摘要未命中时执行，正常态多一次索引未命中的开销，可接受。
	c, err := s.scanSCIMConfig(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+cols+" FROM scim_config WHERE token=? AND COALESCE(token_hash,'')='' LIMIT 1", token))
	if err != nil {
		return nil, nil //nolint:nilerr // 两轨都未命中=无效令牌，调用方统一按 401 处理
	}
	return c, nil
}

// scimRow 单行扫描抽象（*sql.Row 实现）：让摘要命中与明文兜底两条查询共用同一份扫描逻辑。
type scimRow interface {
	Scan(dest ...interface{}) error
}

// scanSCIMConfig 扫描单行配置：返回 (配置,nil) 表示命中；(nil,err) 表示无行或解析失败。
// 注意 Token 字段在此解密为明文（回显与后续业务用），密文本身不外传。
func (s *Store) scanSCIMConfig(row scimRow) (*SCIMConfig, error) {
	c := &SCIMConfig{}
	var en, root int64
	if err := row.Scan(&c.TenantID, &c.Token, &en, &root, &c.CreatedAt, &c.TokenHash); err != nil {
		return nil, err
	}
	c.Enabled, c.RootOrgID = en == 1, root
	c.Token = DecryptSecret(c.Token)
	return c, nil
}

// UpsertSCIMConfig 保存/更新租户配置（token 为空时自动生成）。
// 入参 c.Token 为明文；落库拆成 token_hash（摘要）+ token（密文），回填到 c 上供调用方读回摘要。
func (s *Store) UpsertSCIMConfig(c *SCIMConfig) error {
	if c.Token == "" {
		c.Token = NewSCIMToken()
	}
	if c.CreatedAt == "" {
		c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	en := 0
	if c.Enabled {
		en = 1
	}
	c.TokenHash = HashSCIMToken(c.Token)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO scim_config (tenant_id, token, token_hash, enabled, root_org_id, created_at) VALUES (?,?,?,?,?,?)
		 ON CONFLICT(tenant_id) DO UPDATE SET token=excluded.token, token_hash=excluded.token_hash, enabled=excluded.enabled, root_org_id=excluded.root_org_id`,
		c.TenantID, EncryptSecret(c.Token), c.TokenHash, en, c.RootOrgID, c.CreatedAt)
	return err
}

// SCIMFindUserByExternalId externalId 映射查询。
func (s *Store) SCIMFindUserByExternalId(tid int64, ext string) *User {
	if ext == "" {
		return nil
	}
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id FROM users WHERE tenant_id=? AND scim_external_id=? LIMIT 1", tid, ext).Scan(&id)
	if err != nil {
		return nil
	}
	u, _ := s.GetUser(id, tid)
	return u
}

// SCIMSetUserExternalId 绑定 externalId（覆盖式）。
func (s *Store) SCIMSetUserExternalId(uid, tid int64, ext string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE users SET scim_external_id=? WHERE id=? AND tenant_id=?", ext, uid, tid)
	return err
}

// SCIMExternalID 用户当前的 IdP externalId（无则 ""）。
func (s *Store) SCIMExternalID(uid int64) string {
	var ext string
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(scim_external_id,'') FROM users WHERE id=?", uid).Scan(&ext)
	return ext
}

// UserIDsByOrg 组织内成员用户 ID（SCIM Group members 视图）。
func (s *Store) UserIDsByOrg(tid, orgID int64) []int64 {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id FROM users WHERE tenant_id=? AND org_id=? ORDER BY id", tid, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
