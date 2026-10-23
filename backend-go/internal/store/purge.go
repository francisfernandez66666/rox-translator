// ============ purge.go · 职责说明 ============
// 超管「删除任意租户」的级联清理落地（★ 2026-10-23）。
//
// 背景（为什么不是复用 gdpr.go 的 EraseTenantData）：
//
//	EraseTenantData 是**数据主权擦除**，射程是「清掉这个租户的业务数据」，并且刻意
//	best-effort（每张表失败就 continue、最后恒返回 nil）。它有两个性质不适合当「删租户」用：
//	  ① 不清 `orgs`、不清 `tenants` 行本身 —— 擦完数据，租户和组织还在后台列表里挂着；
//	  ② 失败静默 —— 调用方拿不到「哪张表没删掉」，于是「删了」这件事只是屏幕上的一句话。
//	删租户是**不可逆的管理动作**，必须给出可核对的读数（每张表删了几行、哪张表失败了），
//	所以这里另起一条，不复用、也不改 GDPR 那条（它的 best-effort 是有意为之的合规取舍）。
//
// 表清单口径：
//
//	清单**不在这里**，而是 tenant.ScopedTables（internal/tenant/scoped_tables.go）。
//	★ 2026-10-23 收敛：本文件原来自带一份清单，与 tenant.Store.Delete 那份各自维护，
//	新增带 tenant_id 的表要改两处、漏一处就留孤儿行。现在两边读同一份 —— store 依赖 tenant
//	（packages.go 已 import），tenant 反向 import store 会成环，所以清单只能放在 tenant 那侧。
//	仍然坚持「显式列全、不运行时扫 information_schema 动态删」：删数据这件事上，
//	清单写死、漏了看得见（下一批补），比自动覆盖安全得多。
//
// 顺序与事务：
//
//	两库都没有外键约束（schema 刻意 FK-free，见 store 其它文件的说明），所以删除顺序不影响正确性；
//	`tenants` 行**最后**删 —— 它是「这个租户存在过」的唯一锚点，中途失败时它还在，
//	运维能据此看出「数据已清、租户行还在」这个半途状态，而不是面对一个无从查起的空壳。
//
// =============================================
package store

import (
	"fmt"
	"sort"

	"translator/internal/db"
	"translator/internal/tenant"
)

// PurgeTenantResult 一次级联删除的可核对读数。
// 刻意不用 map 作为对外结构（JSON 里 map 顺序不稳定、也不便于前端排序展示），
// 但 Deleted/Failed 两栏在写库阶段用 map 累积，最后由 API 层转成有序切片。
type PurgeTenantResult struct {
	// TenantID 被清理的租户 ID
	TenantID int64
	// Deleted 每张表实际删除的行数（只记成功且行数 >0 的表）
	Deleted map[string]int64
	// Failed 每张表失败的原因（表不存在 / 权限 / 连接故障等），空表示全绿
	Failed map[string]string
	// TenantRowDeleted `tenants` 行是否已删除；false 表示数据清了但租户行还在
	TenantRowDeleted bool
}

// FailedTables 返回失败表名（有序），供调用方拼错误信息与日志。
func (r *PurgeTenantResult) FailedTables() []string {
	if r == nil || len(r.Failed) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Failed))
	for t := range r.Failed {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// TotalDeleted 本次级联删除的总行数（不含 tenants 行）。
func (r *PurgeTenantResult) TotalDeleted() int64 {
	var n int64
	if r == nil {
		return 0
	}
	for _, v := range r.Deleted {
		n += v
	}
	return n
}

// TenantBrief 取租户的 code/name（删除前的二次确认与审计留痕用）。
// 返回 ok=false 表示该 ID 不存在——调用方据此回 404，而不是把「不存在」当成「删成功」。
// 刻意只取这两列：删租户前的确认只需要「用户看到的那个名字」，
// 把整行读出来会让调用方有机会顺手依赖别的列（而那一列随时可能被下一次迁移改掉）。
func (s *Store) TenantBrief(tid int64) (code, name string, ok bool) {
	if tid <= 0 {
		return "", "", false
	}
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(code,''), COALESCE(name,'') FROM tenants WHERE id=?", tid).Scan(&code, &name)
	if err != nil {
		return "", "", false
	}
	return code, name, true
}

// PurgeTenantCascade 删除一个租户及其全部租户级数据（不可逆）。
//
// 参数：tid=租户 ID，必须 >0（0 是平台上下文，删它等于抹掉平台审计与根组织）。
// 返回：逐表读数；只有「`tenants` 行删除失败」会以 error 形式返回——
// 其余单表失败记进 Result.Failed 而不中断：删到一半停手只会留下更难解释的半途状态，
// 而调用方需要的是「哪些表没删掉」这份清单去补，不是一句 500。
func (s *Store) PurgeTenantCascade(tid int64) (*PurgeTenantResult, error) {
	if tid <= 0 {
		return nil, fmt.Errorf("租户 ID 必须大于 0（0 是平台上下文，不允许删除）")
	}
	res := &PurgeTenantResult{
		TenantID: tid,
		Deleted:  map[string]int64{},
		Failed:   map[string]string{},
	}
	dialect := db.CurrentDialect()
	// ① 逐表删租户级数据（清单来自 tenant.ScopedTables，与注册补偿那条共用一份）
	for _, table := range tenant.ScopedTables {
		rs, err := db.Exec(s.db, dialect, "DELETE FROM "+table+" WHERE tenant_id=?", tid)
		if err != nil {
			// 表不存在（老库未升级）与真故障在这里混在一起，都如实记下——
			// 让调用方自己判断「这张表本来就没有」还是「这张表没删掉」。
			res.Failed[table] = err.Error()
			continue
		}
		if n, e := rs.RowsAffected(); e == nil && n > 0 {
			res.Deleted[table] = n
		}
	}
	// ② 最后删租户行本身（中途失败时它还在，是「半途状态」唯一可查的锚点）
	rs, err := db.Exec(s.db, dialect, "DELETE FROM tenants WHERE id=?", tid)
	if err != nil {
		return res, fmt.Errorf("删除租户行失败：%w", err)
	}
	if n, e := rs.RowsAffected(); e == nil && n > 0 {
		res.TenantRowDeleted = true
	} else {
		return res, fmt.Errorf("租户行未删除（可能已被并发删除）")
	}
	return res, nil
}
