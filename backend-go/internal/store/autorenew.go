// ============ 本文件职责中文说明 ============
// 自动续费的数据访问层（★ #41 商业洞二，2026-09-21）：
//   - SetTenantAutoRenew：租户级「自动续费」开关，单字段原子置位（JSONPatchSet），
//     不整体覆盖 permissions（避免与并发的到期提醒标记写入互相吞字段，见 B1 整改口径）；
//   - HasPendingPackageOrder：同一商业包是否已有待支付订单，作为续费单的去重键。
//
// 订阅身份与到期时间仍存于 tenants.permissions（package_code / package_expires_at），
// 本文件只补「是否自动续费」与「续费单是否已存在」两件事；建单与触达逻辑在 api/pay_renew.go。
// ==========================================
package store

import (
	"time"

	"translator/internal/db"
)

// SetTenantAutoRenew 置位/复位租户自动续费开关。
// 参数：tid=租户 ID；on=true 开启（到期前自动创建续费单并通知管理员付款）。
func (s *Store) SetTenantAutoRenew(tid int64, on bool) error {
	d := db.CurrentDialect()
	patch := `{"auto_renew":true}`
	if !on {
		patch = `{"auto_renew":false}`
	}
	_, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		patch, time.Now().Format(time.RFC3339), tid)
	return err
}

// HasPendingPackageOrder 该租户是否已存在同一商业包的待支付订单（续费单去重）。
// 参数：tid=租户 ID，packageID=商业包 ID（<=0 直接返回 false）。
// 口径：只认 pending——渠道侧超时后由 CloseStalePendingOrders 置 cancelled，
// 下一轮扫描即可重新建单，无需额外状态机。
func (s *Store) HasPendingPackageOrder(tid, packageID int64) (bool, error) {
	if packageID <= 0 {
		return false, nil
	}
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(1) FROM orders WHERE tenant_id=? AND package_id=? AND status='pending'", tid, packageID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
