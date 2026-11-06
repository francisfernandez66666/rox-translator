// ============ kbterms_scoped.go · 职责说明 ============
// 术语硬闸的「可见域版」查询（2026-10-29 第十一轮 T-B）。
//
// 背景：`FindTermsBySubstring` 的 `WHERE e.tenant_id=?` 只认**本租户**的条目，
// 而行业包/角色包/语言文化包都宿主在平台 tenant 0 ⇒ 它们对硬闸完全不可见。
// 本文件按「调用方算好的可见包白名单」查，装配规则仍只由 `BuildPackScope` 一处负责。
//
// 为什么单独开文件而不是往 kbpackages.go 加：AGENTS §一·1 的 store 冻结规则
// （kbpackages.go 只减不增，新方法按域新建文件）。
// ============================================
package store

import (
	"sort"
	"strings"

	"translator/internal/db"
)

// FindTermsBySubstringAllowed 同 FindTermsBySubstring，但把结果限制在 allow 包集合内。
//
// allow 为空 ⇒ 返回空（没有可见域就没有术语，**绝不放宽成"查全部"**）。
// 排序：源文更长优先（长术语更具体），其次包类型（产品 > 企业 > 角色 > 行业 > 其它），最后 id。
// 调用方（engine.gateCtx.termReqs）按 `source+lang` 去重，取第一条 ⇒ 产品私有说法优先。
//
// ★ 一期审阅修正（相对施工文档 §4.3「新增可选入参 productPackIDs」）：改用 SQL 直判
//
//	`COALESCE(p.product_id,'') <> ''`，**不改函数签名、不动任何调用点**。理由：
//	① product_id 只可能由产品包写入（唯一真值在库里），传第二份集合进函数＝第二把尺子，
//	   漏传/传旧集合会让同一份数据出现两种排序；② 该函数被 engine/gates.go 的术语硬闸
//	   对全租户默认调用，改签名要动所有调用点，正是发布前置 E1 最怕的改动面；
//	③ 旧库所有存量包 product_id 补列默认 '' ⇒ −1 分支永不命中 ⇒ 老租户行序逐条不变。
//
// 不加跨请求缓存：可见域含「组织链 + job_role」，缓存键必须带上它们才不会串
// （那一维的唯一实现是 engine.cjkCacheScopeKey；这里只走请求内缓存，代价是每段一次小查询）。
func (s *Store) FindTermsBySubstringAllowed(srcLang, srcText string, allow map[int64]bool) ([]*KBEntry, error) {
	if len(allow) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(allow))
	for id := range allow {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	q := "SELECT e.id, e.tenant_id, e.package_id, e.layer, e.source_lang, e.source_text, e.target_lang, e.target_text, e.module, e.created_at, e.updated_at " +
		"FROM kb_entries e JOIN kb_packages p ON e.package_id=p.id " +
		"WHERE e.source_lang=? AND e.layer=1 AND p.role='source' AND COALESCE(p.enabled,1)=1 " +
		"AND e.package_id IN (" + ph + ") " +
		"AND ? LIKE '%' || e.source_text || '%' " +
		"ORDER BY length(e.source_text) DESC, CASE " +
		"WHEN COALESCE(p.product_id,'') <> '' THEN -1 " + // 产品私有说法：一期插层，老库恒不命中
		"WHEN p.pack_type='tenant' THEN 0 WHEN p.pack_type='persona' THEN 1 " +
		"WHEN p.pack_type='industry' THEN 2 ELSE 3 END, e.id"
	args := make([]interface{}, 0, len(ids)+2)
	args = append(args, srcLang)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, srcText)
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBEntry
	for rows.Next() {
		var e KBEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.PackageID, &e.Layer, &e.SourceLang,
			&e.SourceText, &e.TargetLang, &e.TargetText, &e.Module, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &e)
	}
	return out, nil
}

// TermSourcesAllowed 返回 allow 包集合内全部 layer=1 源词（小写）。
//
// 用途：文档级领域词表（第十二轮 W4）在**查 wiki 之前**把「租户已有术语层」的源词剔掉——
// 同一 source 两套译法会让提示词自相矛盾；品牌术语也走这条（它们是 layer=1 + module=brand）。
func (s *Store) TermSourcesAllowed(allow map[int64]bool) (map[string]bool, error) {
	out := map[string]bool{}
	if len(allow) == 0 {
		return out, nil
	}
	ids := make([]int64, 0, len(allow))
	for id := range allow {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	q := "SELECT DISTINCT e.source_text FROM kb_entries e JOIN kb_packages p ON e.package_id=p.id " +
		"WHERE e.layer=1 AND p.role='source' AND COALESCE(p.enabled,1)=1 " +
		"AND e.package_id IN (" + ph + ")"
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			continue
		}
		if v := strings.ToLower(strings.TrimSpace(src)); v != "" {
			out[v] = true
		}
	}
	return out, nil
}
