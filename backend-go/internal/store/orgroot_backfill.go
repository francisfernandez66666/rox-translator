// ============ orgroot_backfill.go · 职责说明 ============
// store 包内部实现文件。
//
// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 体验批 A4：把「补租户根组织行」从**读路径**搬到**迁移链**。
//
// 改造前形态（现网表现＝超管一进平台视图就慢/超时）：
//
//	GET /api/admin/orgs 的平台分支每次请求都 `Ten.List()` 全量列租户，
//	再逐个 `GetRootOrg`（查不到就 `EnsureRootOrg` **写一条**）。
//	三笔账：① N 个租户＝1＋2N 次往返；② 读路径里夹写，SQLite 下是写锁竞争源；
//	③ 「看不见的那条腿才是真腿」——这条写只在平台视图被打开时才发生，
//	于是首启/新库的那批租户在有人进后台之前根本没有根组织行。
//
// 改造后：启动迁移一次性回填（幂等，跑第二次零写入），建租户口各补一行（见
// `api.createTenantWithRoot`），读路径只查不写。
//
// 三条容错口径（一条放宽就把"回填"变成"静默不回填"）：
//
//	① `tenants` 表还不存在（内存库/极老库首启）⇒ 一行都不补并出声，**不 panic**；
//	② 单租户补行失败 ⇒ 继续下一个（一颗坏行不该挡整轮回填），计数入 WARN；
//	③ 已有根组织行 ⇒ `EnsureRootOrg` 自己命中即返回，天然幂等。
//
// =============================================
package store

import (
	"context"

	"translator/internal/db"
	"translator/internal/observability"
)

// PlatformRootOrgName 平台根组织的名字。原先这个字面量只写在读路径里
// （api/orgs.go 的 EnsurePlatformRootOrg("能言")），回填搬进 store 后必须有同一个常量，
// 否则"迁移建的那一行"和"读路径找的那一行"名字分叉＝平台树永远缺一根柱子。
const PlatformRootOrgName = "能言"

// RootOrgMigrate 一次性回填平台根组织＋全部租户的根组织行（幂等；Store.New 迁移链调用）。
// 回填必须排在 IndexPerfMigrate **之前**：那条给 orgs 之外的表建索引，而这里补的行
// 要被同一次启动里的读路径看见（顺序颠倒＝首启那一轮平台树少一批租户）。
func (s *Store) RootOrgMigrate() {
	ctx := context.Background()
	// ① 平台根（tenant_id=0）
	if _, err := s.EnsurePlatformRootOrg(PlatformRootOrgName); err != nil {
		observability.Warn(ctx, "根组织回填: 平台根组织补建失败", "err", err.Error())
	}
	// ② 各租户根。表还不存在时（内存库/极老库首启）读失败即整段跳过并出声——
	//    绝不把「没表」读成「没有租户需要补」（那正是本文件要消灭的那类静默）。
	rows, err := db.Query(s.db, db.CurrentDialect(), `SELECT id, name FROM tenants ORDER BY id`)
	if err != nil {
		observability.Warn(ctx, "根组织回填: 读租户清单失败，本轮整段跳过（tenants 表不在位也算这一档）", "err", err.Error())
		return
	}
	defer rows.Close()
	type pending struct {
		id   int64
		name string
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.name); err != nil {
			continue
		}
		list = append(list, p)
	}
	// range 完再写（AGENTS §三「并发回写共享 map 必须先快照」同一形状：读游标与写库不重叠）
	failed := 0
	for _, p := range list {
		if _, err := s.EnsureRootOrg(p.id, p.name); err != nil {
			failed++
		}
	}
	if failed > 0 {
		observability.Warn(ctx, "根组织回填: 部分租户根组织补建失败", "failed", failed, "total", len(list))
	}
}
