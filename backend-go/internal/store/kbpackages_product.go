// ============ kbpackages_product.go · 职责说明 ============
// store 包「产品包」（kb_packages 窄域），一期《确定的多语言能力》批次 2。
//
// ★ 为什么不新建「已批准说法表」：术语硬闸（engine/gates.go）、固定标签档、
//
//	FindTermsBySubstringAllowed、待审池纪律都只读 kb_entries；另建一张表＝第二把尺子，
//	且所有既有闸对新产品失明。因此产品包只是 kb_packages 上的两行（source/gate），
//	靠补列 product_id 标识归属；kbpackages.go 是冻结文件，一行不改。
//
// ★ pack_type 复用 PackTenant（不新增枚举）：个人租户收口 SQL
//
//	`pack_type NOT IN ('department','cross_dept')` 因此天然放行产品包（断言 K3）。
//
// ★ 幂等靠「先查后建」＋部分唯一索引 ux_pkg_product 双保险；product_id=” 的存量包
//
//	不参与唯一约束（WHERE product_id <> ''），老库升级零影响。
//
// 所有 SQL 一律走 db.Exec/Query/QueryRow ＋ db.CurrentDialect()。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"translator/internal/db"
)

// productPackCodePrefix 产品包 code 前缀；code 不参与唯一约束，仅作排障辨识。
const productPackCodePrefix = "product:"

// EnsureProductPacks 为产品幂等建/取 source 与 gate 两个包，返回两个包 ID。
// ★ 文档 §3.1 写返回值 string，但 kb_packages.id 是 INTEGER、PackBrief.ID/Rank 的
//
//	packID 均为 int64，且 ListProductPackIDs 返回 map[int64]bool ⇒ 按 int64 落地（审阅修正）。
//
// 参数：tid=租户 ID，productID=产品主键；返回 source 包 ID、gate 包 ID 与错误。
func (s *Store) EnsureProductPacks(tid int64, productID string) (int64, int64, error) {
	if err := s.productExistsScoped(tid, productID); err != nil {
		return 0, 0, err
	}
	src, err := s.ensureOneProductPack(tid, productID, PackRoleSource)
	if err != nil {
		return 0, 0, err
	}
	gate, err := s.ensureOneProductPack(tid, productID, PackRoleGate)
	if err != nil {
		return 0, 0, err
	}
	return src, gate, nil
}

// ensureOneProductPack 单角色包的「先查后建」。建包走现网 CreateKBPackage（冻结文件的既有方法），
// 随后只补 product_id 一列——kbPkgCols 不含该列，老查询一行不受影响。
// 参数：tid=租户 ID，productID=产品主键，role=PackRoleSource/PackRoleGate；返回包 ID 与错误。
func (s *Store) ensureOneProductPack(tid int64, productID, role string) (int64, error) {
	if role != PackRoleSource && role != PackRoleGate {
		return 0, fmt.Errorf("非法的产品包角色: %s", role)
	}
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT id FROM kb_packages
		 WHERE tenant_id=? AND product_id=? AND pack_type=? AND role=? ORDER BY id LIMIT 1`,
		tid, productID, PackTenant, role).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	code := productPackCodePrefix + productID + ":" + role
	p, err := s.CreateKBPackage(tid, 0, code, "产品说法包·"+role, PackTenant, role)
	if err != nil {
		return 0, fmt.Errorf("产品包建档失败: %w", err)
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_packages SET product_id=?, updated_at=? WHERE id=? AND tenant_id=?",
		productID, time.Now().UTC().Format(time.RFC3339), p.ID, tid); err != nil {
		return 0, fmt.Errorf("产品包归属回填失败: %w", err)
	}
	return p.ID, nil
}

// ListProductPackIDs 列出该租户全部产品包 ID（供 §4.1 PackScope.ProductPackIDs 装配秩 150）。
// 参数：tid=租户 ID；返回包 ID 集合与错误。
func (s *Store) ListProductPackIDs(tid int64) (map[int64]bool, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id FROM kb_packages
		 WHERE tenant_id=? AND COALESCE(enabled,1)=1 AND product_id <> ''`,
		tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if e := rows.Scan(&id); e != nil {
			return nil, e
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ListProductPacksFor 列出该租户全部产品包简要信息（§4.2 与 ListApplicablePacks 结果取并集）。
// 参数：tid=租户 ID；返回包简要列表与错误（空集出 []）。
func (s *Store) ListProductPacksFor(tid int64) ([]*PackBrief, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT id, pack_type, name, COALESCE(enabled,1) FROM kb_packages
		 WHERE tenant_id=? AND COALESCE(enabled,1)=1 AND product_id <> ''
		 ORDER BY id`, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PackBrief{}
	for rows.Next() {
		var p PackBrief
		if e := rows.Scan(&p.ID, &p.PackType, &p.Name, &p.Enabled); e != nil {
			return nil, e
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}
