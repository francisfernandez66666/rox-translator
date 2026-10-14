// ============ 本文件职责中文说明 ============
// 平台超管站内信的**唯一投递口径**（★ #46，2026-10-14 用户令「采集与告警类通知收敛成一把尺子」）。
//
// 收敛前的现状（五把尺子，本仓 F-33 那一族的复发）：
//
//	① packscraper.notifySuperAdmins：裸 SQL `role IN ('super_admin','admin')`——
//	   不看 tenant_id、不看账号状态，停用的历史账号也照发；
//	② pay.go 静态码待确认：ListUsersByRole(0,"admin") ＋ ListUsersByRole(0,"super_admin")
//	   （这一处 09-25 已按 F-33 修过，注释里就写着「建号/提权实际写 super_admin」）；
//	③④ feedback.go 两处、⑤ lead.go 一处：**只认 'admin'**。
//
// ⇒ 真缺陷（不是形状难看）：一个只有 role='super_admin' 账号的部署（新库、后台提权都写这个值），
//
//	问题反馈与销售线索的站内信**一条都发不到人**——提交方以为超管收到了，告警邮件之外
//	铃铛里什么都没有。pay.go 那条注释记录的正是同一个坑，只是当时只修了那一个面。
//
// 收敛后的口径（判据与**权限判定侧同源**，不是再造一把）：
//
//	· 是谁 ⇒ auth.IsSuperAdmin（role 等级 ≥4 且 tenant_id=0，2026-09-16 安全整改收紧过的那一条）；
//	· 能不能收 ⇒ auth.EffectiveUserStatus 落在 active／deactivating，
//	  与 server.go 里"能不能进后台"用的是同一条判据（注销宽限期当日仍可登录，那就当日仍该收到）。
//
// 两把都取自现成谓词，所以"投递面"与"判定面"不会再一次各长一套——那正是 F-33 的成因。
package api

import (
	"context"
	"log"

	"translator/internal/auth"
	"translator/internal/observability"
	"translator/internal/store"
)

// superAdminRecipients 平台超管收件人名单（现读现算，不缓存：收件人变更要当场生效）。
// 取不到（Store 为空／库读失败）返回 nil——调用方按"没人可发"处理，不 panic、不降级成"发给所有人"。
func (s *Server) superAdminRecipients() []*store.User {
	if s.Store == nil {
		return nil
	}
	users, err := s.Store.ListUsers(0) // 0＝平台层（超管恒挂 tenant_id=0）
	if err != nil {
		observability.Warn(context.Background(), "超管收件人名单取不到 ⇒ 本轮站内信不发", "err", err.Error())
		return nil
	}
	out := make([]*store.User, 0, len(users))
	for _, u := range users {
		if u == nil || u.ID <= 0 {
			continue
		}
		// 判据①：是不是平台超管——直接用鉴权侧那一个谓词，禁止再抄一份角色名单
		if !auth.IsSuperAdmin(u) {
			continue
		}
		// 判据②：这个账号今天能不能进后台——同 server.go 的登录闸口径
		switch auth.EffectiveUserStatus(u.Status, u.DeactivatedAt) {
		case store.UserActive, store.UserDeactivating:
		default:
			continue
		}
		out = append(out, u)
	}
	return out
}

// notifySuperAdmins 向全部平台超管发一类站内信；返回送达人数（0＝没人可发，调用方不必特判）。
// 参数 refType 必须取自 store.NotifyRef*（前端铃铛按它决定落点）；传名单外的值**不拦投递**
// ——把运维触挡掉换来的是一条难看日志，方向不对——但会 WARN 一行，且跨语言等值锁
// （frontend-react/src/lib/noticeRouting.lock.test.ts）会让"补了类型没补落点"在闸门上直接红。
func (s *Server) notifySuperAdmins(title, body, refType string, refID int64) int {
	known := false
	for _, k := range store.NotifyRefTypes {
		if k == refType {
			known = true
			break
		}
	}
	if !known {
		observability.Warn(context.Background(), "站内信 ref_type 不在名单 ⇒ 前端落点表可能缺这一类",
			"ref_type", refType, "title", title)
	}
	n := 0
	for _, u := range s.superAdminRecipients() {
		if err := s.Store.CreateNotification(u.ID, title, body, refType, refID); err != nil {
			// 逐个收件人失败只出声不中断：一个账号写不进去不该让其余超管也收不到
			logNotifyFail(u.ID, err)
			continue
		}
		n++
	}
	return n
}

// logNotifyFail 站内信写入失败的留痕（独立成函数：log.Printf 存量受棘轮基线约束，
// 口径与迁移前 pay.go 里那一行等价，只是从"每个调用方各写一份"收成这里的一份）。
func logNotifyFail(uid int64, err error) {
	log.Printf("[notify] 站内信投递失败 uid=%d: %v", uid, err)
}
