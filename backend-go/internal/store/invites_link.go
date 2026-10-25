// ============ invites_link.go · 职责说明 ============
// store 包「专属邀请链接」（invite_codes 表 kind='link'）窄域文件（★ 多身份方案 B3，2026-10-24）。
// 承载《多身份账号方案_20261023.md》§6.2 / §八·6 新增能力：
//   - InviteLinkMigrate：invite_codes 幂等补五列（kind / bound_username / bound_email / role / expires_at）；
//   - (*InviteCode).IsExpired：按 expires_at 判链接是否过期（空串=不过期，存量码行为逐字节不变的关键）；
//   - CreateInviteLink：落一行系统生成的一次性链接凭据（**不建 users 行**）。
//
// 设计口径：
//  1. 链接与手输码共用 invite_codes 那一套状态机（used / revoked / 一次性原子烧码），
//     差别只在入口形态与预绑信息——所以这里只补列、不建新表，Register 口两段逻辑天然汇流。
//  2. 待激活成员**不落 users 表**：pending 状态完全活在邀请表里，users 的
//     active/disabled/deactivating 状态机零改动，登录/取人/列表/权限全部不受影响。
//  3. kind 列 DEFAULT 'code'：存量行补列后自动是「手输码」语义，读路径逐字节等价于改造前。
//     expires_at DEFAULT ”（不是 NULL）：空串即「永不过期」，历史码不会因为补列而突然失效。
//
// =============================================
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"translator/internal/db"
)

// inviteLinkPrefix 链接码串前缀（★ §6.2：系统生成高熵串 `inv_<32hex>`，不再由管理员手输）。
// 前缀本身只用于人眼分辨「这是链接还是手输码」；判据仍读 kind 列——
// 码串可被管理员手输成任意形状，拿前缀当语义判据就会长出「形状像链接但 kind=code」的第三种形态。
const inviteLinkPrefix = "inv_"

// NewInviteLinkCode 生成一枚链接码（`inv_` + 32 位小写 hex ＝ 128 bit 熵）。
// code 即密钥（★ §6.2 安全口径）：它必须不可枚举，否则外人可以逐个试出别人的企业门禁。
// 随机源失败直接返回错误、**不许**退化成时间戳之类的可猜测串（那种"看起来生成了"的兜底
// 把不可枚举性抹掉，而调用方拿到的仍是一个能用的链接）。
// 参数：无；返回：码串与错误。
func NewInviteLinkCode() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return inviteLinkPrefix + hex.EncodeToString(b[:]), nil
}

// InviteLinkMigrate ★ 多身份方案 B3（2026-10-24）幂等迁移：
// invite_codes 补五列，支撑「管理台生成专属邀请链接建号」（§6.2）：
//   - kind            'code'=现状手输码 / 'link'=系统生成一次性专属链接；
//   - bound_username  链接预填的用户名（管理员录入，用户注册页看到并可改）；
//   - bound_email     非空 ⇒ 注册邮箱必须等于它（最强绑定，换邮箱 ⇒ 400）；
//   - role            受邀者进企业后的角色（空 ⇒ 注册口按默认 user）；
//   - expires_at      UTC RFC3339；空=不过期（**存量码行为逐字节不变的关键**）。
//
// 判据与默认值都是刻意选的：DEFAULT 让补列本身就把老行填成合法语义，
// 因此读路径只需 COALESCE 兜底（见 inviteCodeCols），不必区分「列不存在」与「值为空」。
// 参数：无（随 Store.New 调用）；返回：无（EnsureColumns 自身幂等，失败静默容忍口径同 InvitesRevokeMigrate）。
func (s *Store) InviteLinkMigrate() {
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "invite_codes", map[string]string{
		"kind":           "TEXT NOT NULL DEFAULT 'code'",
		"bound_username": "TEXT NOT NULL DEFAULT ''",
		"bound_email":    "TEXT NOT NULL DEFAULT ''",
		"role":           "TEXT NOT NULL DEFAULT ''",
		"expires_at":     "TEXT NOT NULL DEFAULT ''",
	})
}

// IsExpired 判断凭据是否已过有效期（★ §6.2 链接五态之一）。
//
// 判据只有一条：expires_at 为空串 ⇒ **永不过期**（历史码、以及刻意不设有效期的链接都走这一档）。
// 时间串按 UTC RFC3339 比较（落库侧统一这一格式，见 CreateInviteLink）；
// 解析失败一律**判未过期**——方向是 fail-open：一条写坏了的时间戳不该把管理员刚发出去、
// 还没人用的链接判成失效（那会让客户点开会看到「链接已过期」，而他明明昨天才拿到）。
// 真正的到期判定必须由落库那一刻写对，不靠读侧猜。
// 参数：无（方法接收者）；返回：true=已过期。
func (c *InviteCode) IsExpired() bool {
	if c == nil || strings.TrimSpace(c.ExpiresAt) == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return false
	}
	return time.Now().UTC().Unix() >= exp.UTC().Unix()
}

// IsExpiredAt 用调用方给定的"当前时刻"判过期，语义与 IsExpired 完全一致。
// 存在理由只有一个：单测需要喂一个固定的 now 来造"到期前一秒 / 到期后一秒"两种读数，
// 而 IsExpired 读墙钟，测试里无法钉住。生产调用方一律用 IsExpired。
func (c *InviteCode) IsExpiredAt(now time.Time) bool {
	if c == nil || strings.TrimSpace(c.ExpiresAt) == "" {
		return false
	}
	exp, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return false
	}
	return now.UTC().Unix() >= exp.UTC().Unix()
}

// CreateInviteLink 落一行专属邀请链接凭据（★ §6.2 管理员侧第 2 步）。
//
// ⚠️ 这个方法**只写 invite_codes，绝不碰 users 表**：pending 成员完全活在邀请表里（设计口径 2）。
// 到期时间由调用方算好传进来（默认天数读 system_config 是 api 层的事，数据层不造第二把尺子）。
//
// 参数：code=NewInviteLinkCode() 生成的码串；tenantID=目标租户（>0，链接必须有归属企业）；
// orgID=预绑部门（0=不绑）；role=预绑角色（空⇒注册口按 user）；boundUsername/boundEmail=预填与强绑
// （均可空）；expiresAt=UTC RFC3339 或空串（空=不过期）。
// 返回：新链接行与错误。
func (s *Store) CreateInviteLink(code string, tenantID int64, orgID int64, role, boundUsername, boundEmail, expiresAt string) (*InviteCode, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO invite_codes (code, used, tenant_id, org_id, created_at, kind, bound_username, bound_email, role, expires_at)"+
			" VALUES (?,0,?,?,?,'link',?,?,?,?)",
		code, tenantID, orgID, now, boundUsername, boundEmail, role, expiresAt)
	if err != nil {
		return nil, err
	}
	return s.GetInviteCode(id)
}

// GetInviteLinkByCode 按码串取一行**链接形态**的凭据（公开校验端点用，★ §6.2 用户侧第 1 步）。
//
// 与 GetInviteCodeByCode 的差别只有一条：kind 必须是 'link'。
// 手输码不该从这一面出栈——它没有预绑信息可回显，公开端点把它当链接回会误导用户
// 「填了邮箱就能进」，而那一族码的正当入口是注册表单里手输（§6.1 门一）。
// 参数：code=链接码串；返回：链接行（不存在或形态不符返回 nil，不报错——无效码是一种正常业务态）。
func (s *Store) GetInviteLinkByCode(code string) (*InviteCode, error) {
	c, err := s.GetInviteCodeByCode(strings.TrimSpace(code))
	if err != nil {
		// sql.ErrNoRows 在这里不是故障：链接码是外人可猜着打的公开面，"查不到"必须走 nil 而不是错误，
		// 否则调用方要用错误分支去表达"这码不存在"（五种无效原因里的一种），迟早和其余四档打架。
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if c.Kind != "link" {
		return nil, nil
	}
	return c, nil
}

// ListInviteLinks 列出指定租户的链接凭据（管理台邀请列表，★ §6.2 管理员侧第 3 步）。
// 参数：tenantID=>0 时只列该租户，<=0（平台上下文）列全部——与仓内既有「tid<=0＝平台视角」口径一致
// （见 api/auth_identity.go 与 store/apiKeyIDScope；读侧与写侧必须同一把尺子）。
// 返回：按 ID 倒序的链接行列表（含已用/已撤销/已过期，四档状态由 Status 派生）。
func (s *Store) ListInviteLinks(tenantID int64) ([]*InviteCode, error) {
	q := "SELECT " + inviteCodeCols + " FROM invite_codes WHERE kind='link'"
	args := []interface{}{}
	if tenantID > 0 {
		q += " AND tenant_id=?"
		args = append(args, tenantID)
	}
	q += " ORDER BY id DESC"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InviteCode
	for rows.Next() {
		c, serr := scanInviteCode(rows)
		if serr != nil {
			continue // 单行解析失败跳过（同 ListInviteCodes 口径）
		}
		cc := c
		out = append(out, &cc)
	}
	return out, nil
}
