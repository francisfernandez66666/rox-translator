// ============ kb_tm_host.go · 职责说明 ============
// TM 宿主租户规则与 SaveEntry 同源：部门/组织包落本租户，行业/语言文化包落平台宿主 0。
// 事务型清洗写方直接按 id 更新 KB，不能复用 SaveEntry，因此宿主租户必须从这里取，
// 禁止在 cmd/kb-clean 再抄一份 switch。
// ================================================================
package store

import (
	"database/sql"
	"fmt"

	"translator/internal/db"
)

// TMHostTenantForPackage 返回指定包在 tm_segments 中的宿主租户；包不存在或读取失败返回错误。
func TMHostTenantForPackage(raw *sql.DB, pkgID, tid int64) (int64, error) {
	var packType string
	if err := db.QueryRow(raw, db.CurrentDialect(), "SELECT pack_type FROM kb_packages WHERE id=?", pkgID).Scan(&packType); err != nil {
		return 0, fmt.Errorf("读取包类型失败: %w", err)
	}
	switch packType {
	case "industry", "locale":
		return SharedHostTenant, nil
	default:
		return tid, nil
	}
}
