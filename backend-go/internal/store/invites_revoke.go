// ============ invites_revoke.go · 职责说明 ============
// store 包邀请码撤销（invite_codes 表）窄域文件（★ G9，2026-10-19）。
// 承载本批新增能力，避免扩建既有 invite.go 的既有方法群：
//   - InvitesRevokeMigrate：invite_codes 幂等补 revoked / revoked_at 两列；
//   - RevokeInviteCode：把一枚**未使用**的邀请码置为已撤销（绝不删行）。
//
// 设计口径：撤销是状态位而不是删除——「谁通过这枚码进来」是追溯资产
// （used_by/used_at 一旦删行就永久丢失，客户会来问「我发的码到底被谁用了」）。
// =============================================
package store

import (
	"database/sql"
	"time"

	"translator/internal/db"
)

// inviteCodeCols invite_codes 表通用查询列（★ G9：revoked/revoked_at 走 COALESCE 兜底，
// 使列补列之前的历史库也能扫出不报错）。既有 invite.go 的四个读方法统一引用它，
// 保证「撤销位」在每条读路径上都被带出，不会出现某个接口看不到 revoked 的形态。
// ★ 多身份方案 B3：尾部追加 kind/bound_username/bound_email/role/expires_at 五列（同样 COALESCE 兜底）——
// 理由与 revoked 完全同族：链接形态的预绑信息与到期位必须在**每一条**读路径带出，
// 否则就会出现「管理台列表看得到有效期、注册口校验读不到 ⇒ 过期链接照样能注册」这种分叉。
const inviteCodeCols = "id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,''), COALESCE(org_id,0), COALESCE(revoked,0), COALESCE(revoked_at,''), " +
	"COALESCE(kind,'code'), COALESCE(bound_username,''), COALESCE(bound_email,''), COALESCE(role,''), COALESCE(expires_at,'')"

// scanInviteCode 按 inviteCodeCols 顺序扫描一行并补派生 Status（★ G9）。
// 参数 sc：具备 Scan 的行扫描器（*sql.Row / *sql.Rows）。返回：邀请码行与错误。
func scanInviteCode(sc interface{ Scan(...interface{}) error }) (InviteCode, error) {
	var c InviteCode
	if err := sc.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt, &c.OrgID, &c.Revoked, &c.RevokedAt,
		&c.Kind, &c.BoundUsername, &c.BoundEmail, &c.Role, &c.ExpiresAt); err != nil {
		return c, err
	}
	c.Status = inviteStatusOf(&c)
	return c, nil
}

// InvitesRevokeMigrate ★ G9（2026-10-19）幂等迁移：
// invite_codes 补 revoked（0=未撤销 / 1=已撤销）与 revoked_at（撤销时间，未撤销为空）。
// 老行默认 revoked=0（历史码一律视为有效，行为与迁移前一致）。补列失败静默容忍口径与
// TicketQualityMigrate 一致（EnsureColumns 自身幂等，启动期失败由后续调用的 SQL 报错暴露）。
// 参数：无（随 Store.New 调用）；返回：无。
func (s *Store) InvitesRevokeMigrate() {
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "invite_codes", map[string]string{
		"revoked":    "INTEGER NOT NULL DEFAULT 0",
		"revoked_at": "TEXT",
	})
}

// inviteStatusOf 由行内状态位派生对外展示状态（★ G9）：revoked 优先于 used。
// 参数 c：邀请码行（只读）。返回："active" / "used" / "revoked" / "expired"。空行返回 "active"。
//
// ★ 多身份方案 B3 加 expired 一档，且**排在最后**：撤销/已用是「人动过手」的事实，
// 比「时间到了」更值得让管理员先看到（一个已用又过期的码，说"已使用"才是真相——
// 谁通过它进来了，used_by 那一份追溯资产比到期位重要）。
// 存量行 expires_at 为空 ⇒ IsExpired 恒假 ⇒ 状态与改造前逐字节相同（§八·6 的兼容判据）。
func inviteStatusOf(c *InviteCode) string {
	if c == nil {
		return "active"
	}
	if c.Revoked == 1 {
		return "revoked"
	}
	if c.Used == 1 {
		return "used"
	}
	if c.IsExpired() {
		return "expired"
	}
	return "active"
}

// RevokeInviteCode 撤销一枚邀请码（★ G9，2026-10-19）。
//
// 只置 revoked=1，**绝不删行**（used_by/used_at 是「谁通过这枚码进来」的追溯资产）。
// 原子条件更新：仅当 used=0 AND revoked=0 时生效，与注册口的 MarkInviteCodeUsed（同样带
// used=0 AND revoked=0 条件）互斥——「A 在注册、B 同时撤销」这种竞态里必有一方落败，
// 不会出现「码已撤销但注册仍放行」或「注册成功后又把码标成撤销」的中间态。
//
// 参数：id=邀请码主键 ID。返回 (ok, used, err)：
//   - ok=true          撤销成功（该码此前未使用且未撤销）；
//   - ok=false,used=true  该码已被使用——撤销无意义（人已经进来了），调用方给明确文案；
//   - ok=false,used=false 该码不存在或此前已撤销（幂等：重复撤销不报错）。
func (s *Store) RevokeInviteCode(id int64) (ok bool, used bool, err error) {
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE invite_codes SET revoked=1, revoked_at=? WHERE id=? AND used=0 AND revoked=0",
		now, id)
	if err != nil {
		return false, false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true, false, nil
	}
	// 0 行影响不是错误：要么已使用、要么已撤销、要么不存在。查一次把 used 档与其余档分开，
	// 让调用方能给出「已使用不可撤销」而不是含糊的「撤销失败」（同 §一·8 存在性前置口径）。
	var curUsed int
	qerr := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT used FROM invite_codes WHERE id=?", id).Scan(&curUsed)
	if qerr == sql.ErrNoRows {
		return false, false, nil // 不存在：幂等放行，不泄露存在性
	}
	if qerr != nil {
		return false, false, qerr
	}
	return false, curUsed == 1, nil
}
