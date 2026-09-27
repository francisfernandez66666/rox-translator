// ============ 本文件职责中文说明 ============
// 品牌展示宽限期（★ F-75，2026-09-27 〇-X 用户批准「套餐到期后宽限期 30 天＋站内信通知」）
// 的落库层。三个键全部写在 tenants.permissions JSON 内（与订阅续费宽限 grace_expires_at
// 同一本账），不新增列、不建新表 ⇒ 老库启动即兼容，无需幂等迁移。
//
// 键义：
//   - brand_grace_expires_at   品牌展示宽限截止时刻（RFC3339，空＝不在宽限期）；
//   - brand_grace_notice_start 「进入宽限期」站内信已发（去重标记）；
//   - brand_grace_notice_end   「宽限结束、品牌已停止展示」站内信已发（去重标记）。
//
// 为什么单独一套键、不复用 #74 的订阅宽限：订阅宽限默认 3 天且只给开了自动续费的租户，
// 而「品牌回收」是对**所有**靠付费解锁品牌的租户生效的商业动作，两套天数各自可配
// （见 api/branding_paid_gate.go 的配置优先序）。混用会让未开自动续费的租户永远进不了宽限。
//
// 写法口径：一律 db.JSONPatchSet 单字段原子更新（AGENTS §一·3 与 B1 整改——整体回写
// permissions 会把快照之后发生的并发续订/额度调整覆盖掉）。
//
// store 冻结规则（AGENTS §一·1）：billing.go / packages.go 只减不增，故按域另起本文件。
package store

import (
	"errors"
	"time"

	"translator/internal/db"
)

// errBrandGraceStage 未知去重档位（调用方传错 stage 时的显式失败，绝不静默当成已发送）。
var errBrandGraceStage = errors.New("品牌宽限通知档位非法：仅支持 start / end")

// 品牌宽限站内信的去重档位（MarkBrandGraceNotice 的 stage 取值）。
const (
	BrandGraceNoticeStart = "start" // 进入宽限期（到期当轮发出）
	BrandGraceNoticeEnd   = "end"   // 宽限结束、展示回收（回收当轮发出）
)

// brandGracePatch 执行一条 permissions 单字段补丁（收敛三处写法的重复样板）。
// 参数 tid=租户 ID，patch=JSON 补丁串（键值成对，与 renewal_grace.go 同口径）。
func (s *Store) brandGracePatch(tid int64, patch string) error {
	d := db.CurrentDialect()
	_, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		patch, time.Now().Format(time.RFC3339), tid)
	return err
}

// SetBrandGrace 起算品牌展示宽限期：落截止时刻并把两条站内信的去重标记复位。
// 参数 tid=租户 ID，graceEnd=宽限截止时刻（UTC 落库，与 package_expires_at 同口径）。
// 复位标记的目的：同一租户可能多次到期（续费→再到期），每次都要能再提醒一轮。
func (s *Store) SetBrandGrace(tid int64, graceEnd time.Time) error {
	return s.brandGracePatch(tid,
		`{"brand_grace_expires_at":"`+graceEnd.UTC().Format(time.RFC3339)+
			`","brand_grace_notice_start":false,"brand_grace_notice_end":false}`)
}

// MarkBrandGraceNotice 品牌宽限站内信已发（单字段原子置位，防每日扫描重复轰炸）。
// 参数 stage 取 BrandGraceNoticeStart / BrandGraceNoticeEnd；未知档位直接报错拒绝静默丢标记
// （未知档位若当"发过了"处理＝另一条永远发不出去，当"没发"处理＝每轮重复轰炸）。
func (s *Store) MarkBrandGraceNotice(tid int64, stage string) error {
	key := ""
	switch stage {
	case BrandGraceNoticeStart:
		key = "brand_grace_notice_start"
	case BrandGraceNoticeEnd:
		key = "brand_grace_notice_end"
	default:
		return errBrandGraceStage
	}
	return s.brandGracePatch(tid, `{"`+key+`":true}`)
}

// ClearBrandGrace 清品牌宽限期状态（三键一并复位）。
// 调用点两处：① 续费到账后的日扫（订阅身份已恢复，宽限期概念不再成立）；
// ② 宽限结束、发完回收通知后收尾，避免旧键长期躺在库里被误读。
func (s *Store) ClearBrandGrace(tid int64) error {
	return s.brandGracePatch(tid,
		`{"brand_grace_expires_at":"","brand_grace_notice_start":false,"brand_grace_notice_end":false}`)
}
