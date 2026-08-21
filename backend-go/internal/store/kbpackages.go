// ============ 本文件职责中文说明 ============
// 知识库包与条目数据访问层：三级分层包（企业包>行业包>语言文化习惯包，树形继承）管理，
// 以及四层统一条目表（L1 术语 / L2 翻译记忆 / L3 安全句 / L4 AI 碎片）的增删改查。
// 提供来源包优先命中查找（FindEntriesBySource：企业包→行业包按优先级排序）与安全句管理。
// =============================================
package store

import (
	"crypto/md5"
	"encoding/hex"
	"time"
)

// KBPackage 知识库包（三级分层：企业包>行业包>语言文化习惯包，树形继承；部门包归属部门）
type KBPackage struct {
	ID        int64  `json:"id"`         // 包主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	ParentID  int64  `json:"parent_id"`  // 父包 ID（0 表示根节点）
	Code      string `json:"code"`       // 包编码（唯一标识，如 tenant/industry/locale）
	Name      string `json:"name"`       // 包名称（如 企业包/行业包/语言文化习惯包）
	PackType  string `json:"pack_type"`  // 包类型：tenant(企业) / industry(行业) / locale(语言文化) / department(部门)
	Role      string `json:"role"`       // 包角色：source(匹配来源) / gate(输出闸门)
	OrgID     int64  `json:"org_id"`     // 归属部门组织 ID（0=租户级）
	Enabled   int    `json:"enabled"`    // 启用状态：1=启用（参与翻译命中）0=停用
	SortOrder int    `json:"sort_order"` // 同级排序权重（升序）
	CreatedAt string `json:"created_at"` // 创建时间（RFC3339 字符串）
	UpdatedAt string `json:"updated_at"` // 更新时间（RFC3339 字符串）
}

// 包类型常量
const (
	PackTenant     = "tenant"     // 企业包
	PackIndustry   = "industry"   // 行业包
	PackLocale     = "locale"     // 语言文化习惯包
	PackDepartment = "department" // 部门包
)

// 包角色常量
const (
	PackRoleSource = "source" // 匹配来源：翻译时优先查询
	PackRoleGate   = "gate"   // 输出闸门：翻译结果写回前校验
)

// KBEntry 知识库条目（四层统一表）
type KBEntry struct {
	ID         int64  `json:"id"`          // 条目主键 ID
	TenantID   int64  `json:"tenant_id"`   // 所属租户 ID
	PackageID  int64  `json:"package_id"`  // 所属包 ID
	Layer      int    `json:"layer"`       // 条目层：1术语/2TM/3安全句/4碎片
	SourceLang string `json:"source_lang"` // 源语言代码（如 zh）
	SourceText string `json:"source_text"` // 源文本
	TargetLang string `json:"target_lang"` // 目标语言代码
	TargetText string `json:"target_text"` // 目标译文
	Module     string `json:"module"`      // 来源模块/业务来源标识（如 manual/approved）
	CreatedAt  string `json:"created_at"`  // 创建时间（RFC3339 字符串）
	UpdatedAt  string `json:"updated_at"`  // 更新时间（RFC3339 字符串）
}

// KBSafetyPhrase 安全句锁死串库
type KBSafetyPhrase struct {
	ID        int64  `json:"id"`         // 安全句主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	PackageID int64  `json:"package_id"` // 所属包 ID
	Lang      string `json:"lang"`       // 语言代码
	Phrase    string `json:"phrase"`     // 锁死的安全句（禁止出现在输出中）
	CreatedAt string `json:"created_at"` // 创建时间（RFC3339 字符串）
}

// 层常量
const (
	LayerTerm   = 1 // L1 术语
	LayerTM     = 2 // L2 翻译记忆
	LayerSafety = 3 // L3 安全句
	LayerFrag   = 4 // L4 AI 碎片
)

// kbPkgCols 知识库包查询列清单（统一使用，避免遗漏新增列）
const kbPkgCols = "id, tenant_id, parent_id, code, name, pack_type, role, org_id, COALESCE(enabled,1), sort_order, created_at, updated_at"

// placeholders 生成 n 个 ? 占位符（逗号分隔），用于 IN 查询。
func placeholders(n int) string {
	if n <= 0 {
		return "0"
	}
	s := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			s += ","
		}
		s += "?"
	}
	return s
}

// ============ 包 ============

// CreateKBPackage 创建知识库包。
// 参数：tid=租户 ID，parentID=父包 ID（0=根），code=包编码，name=包名称，
// packType=包类型（tenant/industry/locale），role=包角色（source/gate）。
// 返回：新包对象。
func (s *Store) CreateKBPackage(tid int64, parentID int64, code, name, packType, role string) (*KBPackage, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,0,0,?,?)",
		tid, parentID, code, name, packType, role, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetKBPackage(id, tid)
}

// CreateKBPackageForOrg 创建归属指定部门的包（部门管理员创建部门包时使用）。
// 参数：tid=租户 ID，parentID=父包 ID（0=根），code/name/packType/role 同 CreateKBPackage，
// orgID=归属部门组织 ID。
// 返回：新包对象。
func (s *Store) CreateKBPackageForOrg(tid, parentID int64, code, name, packType, role string, orgID int64) (*KBPackage, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,0,?,?)",
		tid, parentID, code, name, packType, role, orgID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetKBPackage(id, tid)
}

// ListDeptPackages 列出部门可管理的包（部门管理员视角）：本部门及子部门下属的部门包 + 租户级企业包。
// 参数：tid=租户 ID，orgIDs=本部门及子部门组织 ID 集合。
// 返回：包列表（部门包 org_id 命中，或租户级企业包）。
func (s *Store) ListDeptPackages(tid int64, orgIDs []int64) ([]*KBPackage, error) {
	q := "SELECT " + kbPkgCols + " FROM kb_packages WHERE tenant_id=? AND (pack_type='department' AND org_id IN (" + placeholders(len(orgIDs)) + ")) ORDER BY sort_order, id"
	args := []interface{}{tid}
	for _, oid := range orgIDs {
		args = append(args, oid)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackage
	for rows.Next() {
		var p KBPackage
		if err := rows.Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.OrgID, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

// GetKBPackage 按 ID+租户查询包（租户隔离校验）。
// 参数：id=包主键 ID，tid=租户 ID；返回包对象。
func (s *Store) GetKBPackage(id, tid int64) (*KBPackage, error) {
	var p KBPackage
	err := s.db.QueryRow("SELECT id, tenant_id, parent_id, code, name, pack_type, role, org_id, COALESCE(enabled,1) as enabled, sort_order, created_at, updated_at FROM kb_packages WHERE id=? AND tenant_id=?", id, tid).
		Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.OrgID, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListKBPackages 列出租户全部包（按包类型、排序权重、ID 排序）。
// 参数：tid=租户 ID；返回包列表。
func (s *Store) ListKBPackages(tid int64) ([]*KBPackage, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, parent_id, code, name, pack_type, role, org_id, COALESCE(enabled,1) as enabled, sort_order, created_at, updated_at FROM kb_packages WHERE tenant_id=? ORDER BY pack_type, sort_order, id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackage
	for rows.Next() {
		var p KBPackage
		if err := rows.Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.OrgID, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// UpdateKBPackage 更新包名称。
// 参数：id=包主键 ID，tid=租户 ID，name=新名称；返回错误。
func (s *Store) UpdateKBPackage(id, tid int64, name string) error {
	_, err := s.db.Exec("UPDATE kb_packages SET name=?, updated_at=? WHERE id=? AND tenant_id=?",
		name, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// DeleteKBPackage 删除包（连带删除其下条目与安全句）。
// 参数：id=包主键 ID，tid=租户 ID；返回错误。
func (s *Store) DeleteKBPackage(id, tid int64) error {
	// 先删包下条目（外键无级联，需显式清理）
	if _, err := s.db.Exec("DELETE FROM kb_entries WHERE package_id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	// 再删包下安全句
	if _, err := s.db.Exec("DELETE FROM kb_safety_phrases WHERE package_id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM kb_packages WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// EnsureDefaultPackages 确保租户存在默认三级包结构（企业包/行业包/语言文化习惯包，幂等）。
// 参数：tid=租户 ID；按 code 检查，缺失则创建。
func (s *Store) EnsureDefaultPackages(tid int64) error {
	defs := []struct {
		code, name, packType, role string
	}{
		{"tenant", "企业包", PackTenant, PackRoleSource},
		{"industry", "行业包", PackIndustry, PackRoleSource},
		{"locale", "语言文化习惯包", PackLocale, PackRoleGate},
		{"department", "部门包", PackDepartment, PackRoleSource},
	}
	for _, d := range defs {
		// 行业包单轨制：内容只在共享宿主（租户1）维护，其他租户不建行业包壳
		if d.packType == PackIndustry && tid != 1 {
			continue
		}
		// 幂等判断：已存在同 code 包则跳过
		var cnt int
		_ = s.db.QueryRow("SELECT COUNT(*) FROM kb_packages WHERE tenant_id=? AND code=?", tid, d.code).Scan(&cnt)
		if cnt > 0 {
			continue
		}
		_, err := s.CreateKBPackage(tid, 0, d.code, d.name, d.packType, d.role)
		if err != nil {
			return err
		}
	}
	return nil
}

// FindIndustryByCode 在默认租户（tenant 1，超管维护行业包的租户）中按 code 查找行业包。
// 参数：code=行业包编码；返回行业包对象（供注册行业校验与名称引用）。
func (s *Store) FindIndustryByCode(code string) (*KBPackage, error) {
	var p KBPackage
	err := s.db.QueryRow("SELECT id, tenant_id, parent_id, code, name, pack_type, role, org_id, COALESCE(enabled,1) as enabled, sort_order, created_at, updated_at FROM kb_packages WHERE tenant_id=1 AND pack_type=? AND code=?", PackIndustry, code).
		Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.OrgID, &p.Enabled, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// EnsureIndustryPackage 确保新租户存在指定行业包（按 code 幂等）。
// 参数：tid=租户 ID，code=行业包编码，name=行业包名称；返回错误。
func (s *Store) EnsureIndustryPackage(tid int64, code, name string) error {
	if code == "" {
		return nil
	}
	var cnt int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM kb_packages WHERE tenant_id=? AND code=?", tid, code).Scan(&cnt)
	if cnt > 0 {
		return nil // 已存在同 code 行业包
	}
	_, err := s.CreateKBPackage(tid, 0, code, name, PackIndustry, PackRoleSource)
	return err
}

// ============ 条目 ============

// SaveEntry 新增/更新 KB 条目：同租户+包内按 (源语言, 源文本, 目标语言) 判重，命中则更新。
// 参数：tid=租户 ID，pkgID=包 ID，layer=条目层，srcLang/srcText=源语言与文本，
// tgtLang/tgtText=目标语言与译文，module=来源模块。
// 返回：条目 ID 或错误。
func (s *Store) SaveEntry(tid, pkgID int64, layer int, srcLang, srcText, tgtLang, tgtText, module string) (int64, error) {
	now := time.Now().Format(time.RFC3339)
	var id int64
	// 先按唯一键查找已有条目
	err := s.db.QueryRow("SELECT id FROM kb_entries WHERE tenant_id=? AND package_id=? AND source_lang=? AND source_text=? AND target_lang=?", tid, pkgID, srcLang, srcText, tgtLang).Scan(&id)
	if err == nil {
		// 已存在：更新层/译文/模块
		_, err := s.db.Exec("UPDATE kb_entries SET layer=?, target_text=?, module=?, updated_at=? WHERE id=?",
			layer, tgtText, module, now, id)
		return id, err
	}
	// 不存在：新增条目
	res, err := s.db.Exec("INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, pkgID, layer, srcLang, srcText, tgtLang, tgtText, module, now, now)
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	// ★ 写通翻译检索层（tm_segments）：按包类型落优先级与宿主租户
	// 部门包(0)/组织包(1) → 本租户；行业包(2)/语言文化包(3) → 租户1（共享宿主）
	var packType string
	if e2 := s.db.QueryRow("SELECT pack_type FROM kb_packages WHERE id=?", pkgID).Scan(&packType); e2 == nil {
		prio := 9
		host := tid
		switch packType {
		case "department":
			prio, host = 0, tid
		case "tenant":
			prio, host = 1, tid
		case "industry":
			prio, host = 2, 1
		case "locale":
			prio, host = 3, 1
		}
		sum := md5.Sum([]byte(srcText))
		hash := hex.EncodeToString(sum[:])
		_, _ = s.db.Exec(
			"INSERT INTO tm_segments (zh_hash, zh, tenant_id, priority, pack_id, "+tgtLang+", module, updated_at) VALUES (?,?,?,?,?,?,?,?) "+
				"ON CONFLICT(zh_hash, tenant_id) DO UPDATE SET "+tgtLang+"=excluded."+tgtLang+", priority=excluded.priority, pack_id=excluded.pack_id, updated_at=excluded.updated_at",
			hash, srcText, host, prio, pkgID, tgtText, module, now)
	}
	return id, nil
}

// ListEntries 列出包内全部条目（按层、ID 排序，最多 2000 条）。
// 参数：tid=租户 ID，pkgID=包 ID；返回条目列表。
func (s *Store) ListEntries(tid, pkgID int64) ([]*KBEntry, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at FROM kb_entries WHERE tenant_id=? AND package_id=? ORDER BY layer, id LIMIT 2000", tid, pkgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBEntry
	for rows.Next() {
		var e KBEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.PackageID, &e.Layer, &e.SourceLang, &e.SourceText, &e.TargetLang, &e.TargetText, &e.Module, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &e)
	}
	return out, nil
}

// DeleteEntry 删除单条条目（租户隔离校验）。
// 参数：id=条目主键 ID，tid=租户 ID；返回错误。
func (s *Store) DeleteEntry(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM kb_entries WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// FindEntriesBySource 按源文本查找匹配来源包（企业包优先）中的条目。
// 参数：tid=租户 ID，srcLang=源语言，srcText=源文本。
// 返回：企业包→行业包按优先级命中的条目列表（只查 role='source' 的来源包）。
func (s *Store) FindEntriesBySource(tid int64, srcLang, srcText string) ([]*KBEntry, error) {
	// 用 CASE 把企业包排最前、行业包次之、其余最后，再按层与 ID 排序保证确定性
	rows, err := s.db.Query(
		"SELECT e.id, e.tenant_id, e.package_id, e.layer, e.source_lang, e.source_text, e.target_lang, e.target_text, e.module, e.created_at, e.updated_at "+
			"FROM kb_entries e JOIN kb_packages p ON e.package_id=p.id "+
			"WHERE e.tenant_id=? AND e.source_lang=? AND e.source_text=? AND p.role='source' "+
			"ORDER BY CASE p.pack_type WHEN 'tenant' THEN 0 WHEN 'industry' THEN 1 ELSE 2 END, e.layer, e.id",
		tid, srcLang, srcText)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBEntry
	for rows.Next() {
		var e KBEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.PackageID, &e.Layer, &e.SourceLang, &e.SourceText, &e.TargetLang, &e.TargetText, &e.Module, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &e)
	}
	return out, nil
}

// ============ 安全句 ============

// SaveSafetyPhrase 新增一条安全句。
// 参数：tid=租户 ID，pkgID=包 ID，lang=语言代码，phrase=锁死的安全句。
// 返回：新安全句 ID。
func (s *Store) SaveSafetyPhrase(tid, pkgID int64, lang, phrase string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO kb_safety_phrases (tenant_id, package_id, lang, phrase, created_at) VALUES (?,?,?,?,?)",
		tid, pkgID, lang, phrase, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListSafetyPhrases 列出租户全部安全句（按 ID 排序）。
// 参数：tid=租户 ID；返回安全句列表。
func (s *Store) ListSafetyPhrases(tid int64) ([]*KBSafetyPhrase, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, package_id, lang, phrase, created_at FROM kb_safety_phrases WHERE tenant_id=? ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBSafetyPhrase
	for rows.Next() {
		var p KBSafetyPhrase
		if err := rows.Scan(&p.ID, &p.TenantID, &p.PackageID, &p.Lang, &p.Phrase, &p.CreatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// DeleteSafetyPhrase 删除单条安全句（租户隔离校验）。
// 参数：id=安全句主键 ID，tid=租户 ID；返回错误。
func (s *Store) DeleteSafetyPhrase(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM kb_safety_phrases WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// SetKBPackageEnabled 启用/停用知识库包，并联动翻译检索层（tm_segments）。
// 停用：从 tm_segments 摘除该包条目（module 前缀 pkg:<id>| 标识）；启用：按包优先级重新写回。
func (s *Store) SetKBPackageEnabled(id int64, enabled int) error {
	var tid, orgID int64
	var packType string
	if err := s.db.QueryRow("SELECT tenant_id, COALESCE(org_id,0), pack_type FROM kb_packages WHERE id=?", id).Scan(&tid, &orgID, &packType); err != nil {
		return err
	}
	prio := 9
	host := tid
	switch packType {
	case "department":
		prio, host = 0, tid
	case "tenant":
		prio, host = 1, tid
	case "industry":
		prio, host = 2, 1
	case "locale":
		prio, host = 3, 1
	}
	now := time.Now().Format("2006-01-02T15:04:05")
	if enabled == 0 {
		// 按 pack_id 精确摘除该包在检索层的全部条目
		if _, err := s.db.Exec("DELETE FROM tm_segments WHERE pack_id=?", id); err != nil {
			return err
		}
	} else {
		// 从 kb_entries 重写回检索层
		rows, err := s.db.Query("SELECT source_text, target_lang, target_text, COALESCE(module,'') FROM kb_entries WHERE package_id=? AND target_lang<>'' AND target_text<>''", id)
		if err != nil {
			return err
		}
		type ent struct{ src, lang, txt, module string }
		var ents []ent
		for rows.Next() {
			var e ent
			if err := rows.Scan(&e.src, &e.lang, &e.txt, &e.module); err == nil {
				ents = append(ents, e)
			}
		}
		rows.Close()
		for _, e := range ents {
			sum := md5.Sum([]byte(e.src))
			hash := hex.EncodeToString(sum[:])
			mod := e.module
			if _, err := s.db.Exec(
				"INSERT INTO tm_segments (zh_hash, zh, tenant_id, priority, pack_id, "+e.lang+", module, updated_at) VALUES (?,?,?,?,?,?,?,?) "+
					"ON CONFLICT(zh_hash, tenant_id) DO UPDATE SET "+e.lang+"=excluded."+e.lang+", priority=excluded.priority, pack_id=excluded.pack_id, updated_at=excluded.updated_at",
				hash, e.src, host, prio, id, e.txt, mod, now); err != nil {
				return err
			}
		}
	}
	_, err := s.db.Exec("UPDATE kb_packages SET enabled=?, updated_at=? WHERE id=?", enabled, time.Now().Format(time.RFC3339), id)
	return err
}

// ApplicablePackIDs 计算租户可应用的知识库包集合（向量检索白名单）。
// 规则：本租户全部包 + 全部语言文化包（共享宿主）+ 注册行业匹配的行业包（共享宿主）。
// 返回：包 ID 集合。
func (s *Store) ApplicablePackIDs(tid int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	rows, err := s.db.Query(`
		SELECT id FROM kb_packages WHERE tenant_id=?
		UNION
		SELECT id FROM kb_packages WHERE pack_type='locale'
		UNION
		SELECT id FROM kb_packages WHERE pack_type='industry'
			AND code=(SELECT COALESCE(NULLIF(industry,''),'') FROM tenants WHERE id=?)`, tid, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			out[id] = true
		}
	}
	return out, nil
}
