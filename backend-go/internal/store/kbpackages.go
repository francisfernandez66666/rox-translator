// ============ kbpackages.go · 职责说明 ============
// store 包知识库包与条目数据访问层。
// 三级分层包（企业包>行业包>语言文化习惯包，树形继承）管理，
// 以及四层统一条目表（L1 术语 / L2 翻译记忆 / L3 安全句 / L4 AI 碎片）的增删改查。
// 提供来源包优先命中查找（FindEntriesBySource：企业包→行业包按优先级排序）与安全句管理。
// =============================================
package store

import (
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"translator/internal/db"
	"translator/internal/kb"
)

// KBPackage 知识库包（三级分层：企业包>行业包>语言文化习惯包，树形继承；部门包归属部门）
type KBPackage struct {
	ID             int64  `json:"id"`               // 包主键 ID
	TenantID       int64  `json:"tenant_id"`        // 所属租户 ID
	ParentID       int64  `json:"parent_id"`        // 父包 ID（0 表示根节点）
	Code           string `json:"code"`             // 包编码（唯一标识，如 tenant/industry/locale）
	Name           string `json:"name"`             // 包名称（如 企业包/行业包/语言文化习惯包）
	PackType       string `json:"pack_type"`        // 包类型：tenant(企业) / industry(行业) / locale(语言文化) / department(部门)
	Role           string `json:"role"`             // 包角色：source(匹配来源) / gate(输出闸门)
	OrgID          int64  `json:"org_id"`           // 归属部门组织 ID（0=租户级）
	OrgName        string `json:"org_name"`         // 归属部门名称（展示用，0=租户级时为空）
	TenantName     string `json:"tenant_name"`      // 所属租户（企业）名称（展示用）
	Enabled        int     `json:"enabled"`          // 启用状态：1=启用（参与翻译命中）0=停用
	ShareCrossDept int     `json:"share_cross_dept"` // ★ 跨部门共享开关（2026-08-26 KB继承链）：1=愿意参与跨部门降级检索（默认）0=仅限归属链内用户可见（包级 opt-out）；仅对部门包有意义
	CrossOrgs      []int64 `json:"cross_orgs"`       // 跨部门包涵盖的部门集合（使用/维护范围为这些部门的成员/管理员）
	CrossAll       bool    `json:"cross_all"`        // 跨部门包是否全公司（涵盖租户内全部部门）
	SortOrder      int     `json:"sort_order"`       // 同级排序权重（升序）
	CreatedAt      string  `json:"created_at"`       // 创建时间（RFC3339 字符串）
	UpdatedAt      string  `json:"updated_at"`       // 更新时间（RFC3339 字符串）
}

// 包类型常量
const (
	PackTenant     = "tenant"     // 企业包
	PackIndustry   = "industry"   // 行业包
	PackLocale     = "locale"     // 语言文化习惯包（无scope）
	PackDepartment = "department" // 部门包（链内直接采用）
	PackCrossDept  = "cross_dept" // 跨部门包（独立可见类型：跨部门+超管+租管可见，仅参考例句）

	// GeneralIndustryCode 通用行业兜底包编码（2026-08-26 UAT 产品决策）：
	// 注册缺选/错选行业时回落到本行业，不再拒绝注册——注册漏斗每多一步都是流失。
	// 包由 EnsureDefaultPackages 在共享宿主（租户1）幂等创建。
	GeneralIndustryCode = "general"
	// GeneralIndustryName 通用行业展示名。
	GeneralIndustryName = "通用行业"
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
	ID          int64  `json:"id"`                    // 安全句主键 ID
	TenantID    int64  `json:"tenant_id"`             // 所属租户 ID
	PackageID   int64  `json:"package_id"`            // 所属包 ID（语言文化包）
	Lang        string `json:"lang"`                  // 目标语言代码
	Phrase      string `json:"phrase"`                // 规则内容：style=规范文本 / forbidden=禁用词 / replace=原词
	Kind        string `json:"kind"`                  // 类型：style(风格规范)/forbidden(禁用词)/replace(替换对)
	Replacement string `json:"replacement,omitempty"` // 替换词（仅 replace 类型；译文中命中 Phrase 时替换为该值）
	Status      string `json:"status"`                // 审核状态：pending/approved/rejected（仅 approved 生效）
	Source      string `json:"source"`                // 来源：manual(人工)/llm(LLM 投喂)
	CreatedAt   string `json:"created_at"`            // 创建时间（RFC3339 字符串）
}

// 层常量
const (
	LayerTerm   = 1 // L1 术语
	LayerTM     = 2 // L2 翻译记忆
	LayerSafety = 3 // L3 安全句
	LayerFrag   = 4 // L4 AI 碎片
)

// kbPkgCols 知识库包查询列清单（统一使用，避免遗漏新增列）
const kbPkgCols = "id, tenant_id, parent_id, code, name, pack_type, role, org_id, COALESCE(enabled,1), COALESCE(share_cross_dept,1), sort_order, created_at, updated_at, COALESCE(cross_orgs,'[]') as cross_orgs, COALESCE(cross_all,0) as cross_all"

// scanKBPackage 从查询结果行扫描知识库包（含 cross_orgs/cross_all 字段）。
func scanKBPackage(rows *sql.Rows) (*KBPackage, error) {
	var p KBPackage
	var crossOrgs string
	var crossAll int
	if err := rows.Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.OrgID, &p.Enabled, &p.ShareCrossDept, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt, &crossOrgs, &crossAll); err != nil {
		return nil, err
	}
	p.CrossAll = crossAll == 1
	p.CrossOrgs = parseCrossOrgs(crossOrgs)
	return &p, nil
}

// queryKBPackage 执行单行包查询并返回 *KBPackage（无结果返回 sql.ErrNoRows）。
func (s *Store) queryKBPackage(query string, args ...interface{}) (*KBPackage, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanKBPackage(rows)
}

// parseCrossOrgs 解析跨部门包部门集合的 JSON 文本；空/非法返回 nil。
func parseCrossOrgs(s string) []int64 {
	if s == "" {
		return nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(s), &ids); err != nil || ids == nil {
		return nil
	}
	return ids
}

// encodeCrossOrgs 将部门集合编码为 JSON 文本（空集合返回 "[]"）。
func encodeCrossOrgs(ids []int64) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

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
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,0,0,?,?)",
		tid, parentID, code, name, packType, role, now, now)
	if err != nil {
		return nil, err
	}
	return s.GetKBPackage(id, tid)
}

// CreateKBPackageForOrg 创建归属指定部门的包（部门管理员创建部门包时使用）。
// 参数：tid=租户 ID，parentID=父包 ID（0=根），code/name/packType/role 同 CreateKBPackage，
// orgID=归属部门组织 ID。
// 返回：新包对象。
func (s *Store) CreateKBPackageForOrg(tid, parentID int64, code, name, packType, role string, orgID int64) (*KBPackage, error) {
	now := time.Now().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,?,0,?,?)",
		tid, parentID, code, name, packType, role, orgID, now, now)
	if err != nil {
		return nil, err
	}
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
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackage
	for rows.Next() {
		p, err := scanKBPackage(rows)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// GetKBPackage 按 ID+租户查询包（租户隔离校验）。
// 参数：id=包主键 ID，tid=租户 ID；返回包对象。
func (s *Store) GetKBPackage(id, tid int64) (*KBPackage, error) {
	return s.queryKBPackage("SELECT "+kbPkgCols+" FROM kb_packages WHERE id=? AND tenant_id=?", id, tid)
}

// ListKBPackages 列出租户全部包（按包类型、排序权重、ID 排序）。
// 参数：tid=租户 ID；返回包列表。
func (s *Store) ListKBPackages(tid int64) ([]*KBPackage, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+kbPkgCols+" FROM kb_packages WHERE tenant_id=? ORDER BY pack_type, sort_order, id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackage
	for rows.Next() {
		p, err := scanKBPackage(rows)
		if err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, p)
	}
	return out, nil
}

// UpdateKBPackage 更新包名称。
// 参数：id=包主键 ID，tid=租户 ID，name=新名称；返回错误。
func (s *Store) UpdateKBPackage(id, tid int64, name string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_packages SET name=?, updated_at=? WHERE id=? AND tenant_id=?",
		name, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// DeleteKBPackage 删除包（连带删除其下条目与安全句）。
// 参数：id=包主键 ID，tid=租户 ID；返回错误。
func (s *Store) DeleteKBPackage(id, tid int64) error {
	// 先删包下条目（外键无级联，需显式清理）
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_entries WHERE package_id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	// 再删包下安全句
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_safety_phrases WHERE package_id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_packages WHERE id=? AND tenant_id=?", id, tid)
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
		// ★ 通用行业兜底包（2026-08-26 UAT 产品决策）：注册行业缺选/错选时的回落目标，
		//   仅在共享宿主（租户1）创建（下方 industry 跳过逻辑同样适用）
		{GeneralIndustryCode, GeneralIndustryName, PackIndustry, PackRoleSource},
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
		_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM kb_packages WHERE tenant_id=? AND code=?", tid, d.code).Scan(&cnt)
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
	return s.queryKBPackage("SELECT "+kbPkgCols+" FROM kb_packages WHERE tenant_id=1 AND pack_type=? AND code=?", PackIndustry, code)
}

// EnsureIndustryPackage 确保新租户存在指定行业包（按 code 幂等）。
// 参数：tid=租户 ID，code=行业包编码，name=行业包名称；返回错误。
func (s *Store) EnsureIndustryPackage(tid int64, code, name string) error {
	if code == "" {
		return nil
	}
	var cnt int
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM kb_packages WHERE tenant_id=? AND code=?", tid, code).Scan(&cnt)
	if cnt > 0 {
		return nil // 已存在同 code 行业包
	}
	_, err := s.CreateKBPackage(tid, 0, code, name, PackIndustry, PackRoleSource)
	return err
}

// ============ 条目 ============

// isValidLangColumn 校验语言码是否属于 tm_segments 的固定语言白名单列。
// 用途：所有把语言码拼进 SQL 列名位置的写入点（SaveEntry / 包启停重写回）必须先过此闸，
// 杜绝标识符注入（2026-08-26 全仓评审 A2）。
func isValidLangColumn(lang string) bool {
	if lang == "" {
		return false
	}
	for _, l := range kb.AllLangs {
		if l == lang {
			return true
		}
	}
	return false
}

// SaveEntry 新增/更新 KB 条目：同租户+包内按 (源语言, 源文本, 目标语言) 判重，命中则更新。
// 参数：tid=租户 ID，pkgID=包 ID，layer=条目层，srcLang/srcText=源语言与文本，
// tgtLang/tgtText=目标语言与译文，module=来源模块。
// 返回：条目 ID 或错误。
func (s *Store) SaveEntry(tid, pkgID int64, layer int, srcLang, srcText, tgtLang, tgtText, module string) (int64, error) {
	// ★ 语言码白名单（2026-08-26 全仓评审 A2）：tgtLang 会被拼进 tm_segments 列名
	//  （SQL 标识符位置），必须限定在固定语言列集合内，否则构成标识符注入。
	if !isValidLangColumn(tgtLang) {
		return 0, fmt.Errorf("不支持的目标语言: %s", tgtLang)
	}
	// ★ 整改 R-L4：四层契约（1术语/2TM/3安全句/4碎片）强制校验，防止越界层值写入，
	// 确保 kb_entries.layer 仅取四个语义层的合法值。
	if layer < LayerTerm || layer > LayerFrag {
		return 0, fmt.Errorf("非法的条目层: %d（合法范围 %d-%d）", layer, LayerTerm, LayerFrag)
	}
	now := time.Now().Format(time.RFC3339)
	var id int64
	// 先按唯一键查找已有条目
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id FROM kb_entries WHERE tenant_id=? AND package_id=? AND source_lang=? AND source_text=? AND target_lang=?", tid, pkgID, srcLang, srcText, tgtLang).Scan(&id)
	if err == nil {
		// 已存在：更新层/译文/模块
		_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_entries SET layer=?, target_text=?, module=?, updated_at=? WHERE id=?",
			layer, tgtText, module, now, id)
		return id, err
	}
	// 不存在：新增条目
	id, err = db.InsertID(s.db, db.CurrentDialect(), "id", "INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, pkgID, layer, srcLang, srcText, tgtLang, tgtText, module, now, now)
	if err != nil {
		return 0, err
	}
	// ★ 写通翻译检索层（tm_segments）：按包类型落优先级与宿主租户
	// 部门包(0)/组织包(1) → 本租户；行业包(2)/语言文化包(3) → 租户1（共享宿主）
	var packType string
	if e2 := db.QueryRow(s.db, db.CurrentDialect(), "SELECT pack_type FROM kb_packages WHERE id=?", pkgID).Scan(&packType); e2 == nil {
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
		_, _ = db.Exec(s.db, db.CurrentDialect(),
			"INSERT INTO tm_segments (zh_hash, zh, tenant_id, priority, pack_id, "+tgtLang+", module, updated_at) VALUES (?,?,?,?,?,?,?,?) "+
				"ON CONFLICT(zh_hash, tenant_id, pack_id) DO UPDATE SET "+tgtLang+"=excluded."+tgtLang+", priority=excluded.priority, pack_id=excluded.pack_id, updated_at=excluded.updated_at",
			hash, srcText, host, prio, pkgID, tgtText, module, now)
	}
	return id, nil
}

// ListEntries 列出包内全部条目（按层、ID 排序，最多 2000 条）。
// 参数：tid=租户 ID，pkgID=包 ID；返回条目列表。
func (s *Store) ListEntries(tid, pkgID int64) ([]*KBEntry, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT id, tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at FROM kb_entries WHERE tenant_id=? AND package_id=? ORDER BY layer, id LIMIT 2000", tid, pkgID)
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
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_entries WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// FindEntriesBySource 按源文本查找匹配来源包（企业包优先）中的条目。
// 参数：tid=租户 ID，srcLang=源语言，srcText=源文本。
// 返回：企业包→行业包按优先级命中的条目列表（只查 role='source' 的来源包）。
func (s *Store) FindEntriesBySource(tid int64, srcLang, srcText string) ([]*KBEntry, error) {
	// 用 CASE 把企业包排最前、行业包次之、其余最后，再按层与 ID 排序保证确定性
	rows, err := db.Query(s.db, db.CurrentDialect(),
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

// FindEntriesBySourceScoped 按源文本查找匹配来源包条目，并施加组织可见性隔离（整改 R2）。
// 部门私有术语（org_id>0 且不等于调用方部门）默认不可见，除非包开启 share_cross_dept=1
// （跨部门降级检索 opt-in）；租户级/行业级/语言文化级包对所有用户可见。
// orgID=0（调用方不在任何部门，如租户管理员）时，仅可见 org_id=0 及行业/语言文化包，
// 其余部门私有包一律隔离，杜绝跨部门泄漏。
// 参数：tid=租户 ID，orgID=调用方组织 ID，srcLang=源语言，srcText=源文本。
func (s *Store) FindEntriesBySourceScoped(tid, orgID int64, srcLang, srcText string) ([]*KBEntry, error) {
	q := "SELECT e.id, e.tenant_id, e.package_id, e.layer, e.source_lang, e.source_text, e.target_lang, e.target_text, e.module, e.created_at, e.updated_at " +
		"FROM kb_entries e JOIN kb_packages p ON e.package_id=p.id " +
		"WHERE e.tenant_id=? AND e.source_lang=? AND e.source_text=? AND p.role='source' " +
		"AND (" +
		"  p.org_id = 0" + // 租户级包（企业/共享）全员可见
		"  OR p.pack_type IN ('industry','locale')" + // 行业/语言文化包全员可见
		"  OR p.org_id = ?" + // 调用方所在部门
		"  OR (p.org_id <> 0 AND p.org_id <> ? AND COALESCE(p.share_cross_dept,1)=1)" + // 其他部门 opt-in 共享
		") " +
		"ORDER BY CASE p.pack_type WHEN 'tenant' THEN 0 WHEN 'industry' THEN 1 ELSE 2 END, e.layer, e.id"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, tid, srcLang, srcText, orgID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBEntry
	for rows.Next() {
		var e KBEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.PackageID, &e.Layer, &e.SourceLang, &e.SourceText, &e.TargetLang, &e.TargetText, &e.Module, &e.CreatedAt, &e.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &e)
	}
	return out, nil
}

// ============ 安全句 ============

// SaveSafetyPhrase 新增一条安全句。
// 参数：tid=租户 ID，pkgID=包 ID，lang=语言代码，phrase=锁死的安全句。
// 返回：新安全句 ID。
// SaveSafetyPhraseEx 结构化保存安全句（含类型/替换词；默认 approved+manual）。
func (s *Store) SaveSafetyPhraseEx(tid, pkgID int64, lang, phrase, kind, replacement string) (int64, error) {
	if kind == "" {
		kind = "style"
	}
	now := time.Now().Format(time.RFC3339)
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"INSERT INTO kb_safety_phrases (tenant_id, package_id, lang, phrase, kind, replacement, status, source, created_at) VALUES (?,?,?,?,?,?, 'approved','manual',?) RETURNING id",
		tid, pkgID, lang, phrase, kind, replacement, now).Scan(&id)
	return id, err
}

// SaveSafetyPhrase 兼容入口（等价 style 类型）。
func (s *Store) SaveSafetyPhrase(tid, pkgID int64, lang, phrase string) (int64, error) {
	return db.InsertID(s.db, db.CurrentDialect(), "id", "INSERT INTO kb_safety_phrases (tenant_id, package_id, lang, phrase, created_at) VALUES (?,?,?,?,?)",
		tid, pkgID, lang, phrase, time.Now().Format(time.RFC3339))
}

// ListSafetyPhrases 列出租户全部安全句（按 ID 排序）。
// 参数：tid=租户 ID；返回安全句列表。
func (s *Store) ListSafetyPhrases(tid int64) ([]*KBSafetyPhrase, error) {
	return s.ListSafetyPhrasesFilter(tid, "")
}

// ListSafetyPhrasesFilter 按审核状态过滤列出安全句（status 空=全部）。
func (s *Store) ListSafetyPhrasesFilter(tid int64, status string) ([]*KBSafetyPhrase, error) {
	q := "SELECT id, tenant_id, package_id, lang, phrase, COALESCE(kind,'style'), COALESCE(replacement,''), COALESCE(status,'approved'), COALESCE(source,'manual'), created_at FROM kb_safety_phrases WHERE tenant_id=?"
	args := []interface{}{tid}
	if status != "" {
		q += " AND COALESCE(status,'approved')=?"
		args = append(args, status)
	}
	q += " ORDER BY id DESC"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBSafetyPhrase
	for rows.Next() {
		var p KBSafetyPhrase
		if err := rows.Scan(&p.ID, &p.TenantID, &p.PackageID, &p.Lang, &p.Phrase, &p.Kind, &p.Replacement, &p.Status, &p.Source, &p.CreatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &p)
	}
	return out, nil
}

// SetSafetyPhraseStatus 审核安全句（通过/驳回）；仅 pending 状态可流转，approved/rejected 可人工改判。
//
// ★ 租户隔离（2026-08-26 全仓评审 A1）：SQL 必须携带 tenant_id 条件——
//
//	 此前仅 WHERE id=?，任一租户的部门管理员可遍历自增 ID 改判其他租户的安全句
//	（approved 即生效于该租户的文化闸门拦截逻辑），构成跨租户越权写。
func (s *Store) SetSafetyPhraseStatus(id, tid int64, status string) error {
	if status != "pending" && status != "approved" && status != "rejected" {
		return fmt.Errorf("非法状态: %s", status)
	}
	res, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_safety_phrases SET status=?, created_at=created_at WHERE id=? AND tenant_id=?", status, id, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 不区分「不存在」与「他租记录」：一律报不存在，不泄露跨租户存在性
		return fmt.Errorf("记录不存在")
	}
	return nil
}

// BulkImportSafetyPhrases LLM 投喂批量导入：统一落 pending+llm，逐条跳过完全重复项。
// 返回：新增条数。
func (s *Store) BulkImportSafetyPhrases(tid, pkgID int64, items []*KBSafetyPhrase) (int, error) {
	now := time.Now().Format(time.RFC3339)
	added := 0
	for _, it := range items {
		if it.Lang == "" || it.Phrase == "" {
			continue
		}
		kind := it.Kind
		if kind != "style" && kind != "forbidden" && kind != "replace" {
			kind = "style"
		}
		var cnt int
		_ = db.QueryRow(s.db, db.CurrentDialect(),
			"SELECT COUNT(*) FROM kb_safety_phrases WHERE tenant_id=? AND package_id=? AND lang=? AND phrase=? AND kind=?",
			tid, pkgID, it.Lang, it.Phrase, kind).Scan(&cnt)
		if cnt > 0 {
			continue
		}
		if _, err := db.Exec(s.db, db.CurrentDialect(),
			"INSERT INTO kb_safety_phrases (tenant_id, package_id, lang, phrase, kind, replacement, status, source, created_at) VALUES (?,?,?,?,?,'pending','llm',?)",
			tid, pkgID, it.Lang, it.Phrase, kind, it.Replacement, now); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// DeleteSafetyPhrase 删除单条安全句（租户隔离校验）。
// 参数：id=安全句主键 ID，tid=租户 ID；返回错误。
func (s *Store) DeleteSafetyPhrase(id, tid int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM kb_safety_phrases WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// SetKBPackageEnabled 启用/停用知识库包，并联动翻译检索层（tm_segments）。
// 停用：从 tm_segments 摘除该包条目（module 前缀 pkg:<id>| 标识）；启用：按包优先级重新写回。
func (s *Store) SetKBPackageEnabled(id int64, enabled int) error {
	var tid, orgID int64
	var packType string
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT tenant_id, COALESCE(org_id,0), pack_type FROM kb_packages WHERE id=?", id).Scan(&tid, &orgID, &packType); err != nil {
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
		if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM tm_segments WHERE pack_id=?", id); err != nil {
			return err
		}
	} else {
		// 从 kb_entries 重写回检索层
		rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT source_text, target_lang, target_text, COALESCE(module,'') FROM kb_entries WHERE package_id=? AND target_lang<>'' AND target_text<>''", id)
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
			// ★ 白名单守卫（A2 纵深防御）：kb_entries.target_lang 属于历史落库数据，
			//   若存在被污染的非白名单语言码，跳过该条（不中断整个包的重写回）。
			if !isValidLangColumn(e.lang) {
				continue
			}
			sum := md5.Sum([]byte(e.src))
			hash := hex.EncodeToString(sum[:])
			mod := e.module
			if _, err := db.Exec(s.db, db.CurrentDialect(),
				"INSERT INTO tm_segments (zh_hash, zh, tenant_id, priority, pack_id, "+e.lang+", module, updated_at) VALUES (?,?,?,?,?,?,?,?) "+
					"ON CONFLICT(zh_hash, tenant_id, pack_id) DO UPDATE SET "+e.lang+"=excluded."+e.lang+", priority=excluded.priority, pack_id=excluded.pack_id, updated_at=excluded.updated_at",
				hash, e.src, host, prio, id, e.txt, mod, now); err != nil {
				return err
			}
		}
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_packages SET enabled=?, updated_at=? WHERE id=?", enabled, time.Now().Format(time.RFC3339), id)
	return err
}

// ApplicablePackIDs 计算租户可应用的知识库包集合（向量检索白名单）。
// 规则：本租户全部包 + 全部语言文化包（共享宿主）+ 注册行业匹配的行业包（共享宿主）。
// 返回：包 ID 集合。
func (s *Store) ApplicablePackIDs(tid int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	rows, err := db.Query(s.db, db.CurrentDialect(), `
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

// PackBrief 包简要信息（前台身份栏展示用）。
type PackBrief struct {
	ID       int64  `json:"id"`        // 包 ID
	PackType string `json:"pack_type"` // 包类型：department/tenant/industry/locale
	Name     string `json:"name"`      // 包名称
	Enabled  int    `json:"enabled"`   // 启用状态（停用包不展示为生效）
}

// ListApplicablePacks 列出租户用户实际可应用的知识库包（按优先级链排序）。
// 范围：本租户全部包 + 语言文化包（全系统）+ 注册行业匹配的行业包；仅返回启用中的包。
// 参数：tid=租户 ID（<=0 返回空，平台上下文无知识库）。
func (s *Store) ListApplicablePacks(tid int64) ([]*PackBrief, error) {
	if tid <= 0 {
		return []*PackBrief{}, nil
	}
	rows, err := db.Query(s.db, db.CurrentDialect(), `
		SELECT id, pack_type, name, COALESCE(enabled,1) FROM kb_packages
		WHERE COALESCE(enabled,1)=1 AND (
			tenant_id=?
			OR pack_type='locale'
			OR (pack_type='industry' AND code=(SELECT COALESCE(NULLIF(industry,''),'~none~') FROM tenants WHERE id=?))
		)
		ORDER BY CASE pack_type WHEN 'department' THEN 0 WHEN 'tenant' THEN 1 WHEN 'industry' THEN 2 ELSE 3 END, id`, tid, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PackBrief
	for rows.Next() {
		var p PackBrief
		if err := rows.Scan(&p.ID, &p.PackType, &p.Name, &p.Enabled); err == nil {
			out = append(out, &p)
		}
	}
	return out, nil
}

// ============ 组织继承链可见范围（2026-08-26《KB组织继承链与部门隔离改造方案》） ============

// BuildPackScope 按用户组织祖先链组装知识库可见范围（检索三路径统一消费）。
// 装配规则（与方案 §一语义总图一一对应）：
//
//	链内部门包   pack_type='department' 且 org_id ∈ chain → ChainPacks[包ID]=距离(下标)
//	企业包       pack_type='tenant'                      → TenantPackIDs
//	历史无主行   tm_segments.pack_id=0                    → 检索层按企业层对待（无需登记）
//	行业包       平台共享且 code=租户注册行业              → SharedPackIDs
//	通用语言习惯包 pack_type='locale'（全用户可见）        → UniversalPackIDs（最低优先级档）
//	跨部门包     pack_type='cross_dept'（独立可见类型）     → CrossDeptPacks（仅开关开且：超管/租管或归属部门在本链内时装配；仅参考例句）
//	跨部门候选   本租户其余 department 包且 share_cross_dept=1 → CrossDeptPacks（仅开关开时装配，兼容旧机制；租户内全部门可见）
//
// 参数：tid=租户 ID；chain=OrgAncestorIDs 输出（空链=未挂组织用户，只见企业/共享层）。
// 返回：kb.PackScope（store→kb 单向依赖：kb 不反向 import store）。
func (s *Store) BuildPackScope(tid int64, chain []int64, allowCross bool) (*kb.PackScope, error) {
	scope := &kb.PackScope{
		TenantID:        tid,
		Chain:           chain,
		ChainPacks:      map[int64]int{},
		TenantPackIDs:   map[int64]bool{},
		SharedPackIDs:   map[int64]bool{},
		UniversalPackIDs: map[int64]bool{},
		CrossDeptPacks:  map[int64]string{},
		AllowCrossDept:  allowCross,
	}
	// 祖先距离映射：chain[0]=本部门(0)、chain[1]=父(1)…
	dist := map[int64]int{}
	for i, orgID := range chain {
		if _, dup := dist[orgID]; !dup {
			dist[orgID] = i
		}
	}
	// 租户注册行业（共享行业包匹配键）
	industry := ""
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(industry,'') FROM tenants WHERE id=?", tid).Scan(&industry)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, pack_type, COALESCE(org_id,0), code, name, COALESCE(share_cross_dept,1), COALESCE(cross_orgs,'[]'), COALESCE(cross_all,0) FROM kb_packages WHERE tenant_id=? AND COALESCE(enabled,1)=1", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var ptype, code, name string
		var orgID int64
		var share, crossAll int
		var crossOrgs string
		if err := rows.Scan(&id, &ptype, &orgID, &code, &name, &share, &crossOrgs, &crossAll); err != nil {
			continue
		}
		switch ptype {
		case PackDepartment:
			if d, ok := dist[orgID]; ok {
				scope.ChainPacks[id] = d // ① 链内部门包（就近覆盖；本链可见性与 share 无关）
			} else if allowCross && len(chain) > 0 && share == 1 {
				// ④ 跨部门候选：需同时满足 租户开关开 + 包级未退出(share_cross_dept=1)。
				// ★ 空链守卫：未挂组织/匿名用户不装配跨部门集——回退层的语义是
				// 「有链而链内没有」，无链用户只享企业/共享层，不见任何部门包。
				scope.CrossDeptPacks[id] = name
			}
		case PackTenant:
			scope.TenantPackIDs[id] = true // ② 企业包
		case PackIndustry:
			// 行业包本应宿主在租户1（平台共享）；本租户自建的行业包同样纳入共享判定
			if industry != "" && (tid == 1 || code == industry) {
				scope.SharedPackIDs[id] = true
			}
		case PackLocale:
			// 通用语言习惯包：全用户可见，独立最低优先级档（无scope）
			scope.UniversalPackIDs[id] = true
		case PackCrossDept:
			// 跨部门包（独立可见类型）：仅其涵盖部门成员 + 超管/租管可见，仅参考例句、不直接采用。
			// 维护人：超管/租管/涵盖部门管理员；使用人：超管/租管/涵盖部门成员。
			// 全公司(cross_all=1)对租户内全部用户可见；否则仅对涵盖部门在本链内的用户可见；
			// 无组织链用户（超管/租管，org=0）见全部跨部门包。
			if allowCross {
				if crossAll == 1 || len(chain) == 0 {
					scope.CrossDeptPacks[id] = name
					break
				}
				for _, o := range parseCrossOrgs(crossOrgs) {
					if _, ok := dist[o]; ok {
						scope.CrossDeptPacks[id] = name
						break
					}
				}
			}
		}
	}
	// 平台宿主租户1 的共享行业/文化包（全租户可见；行业码校验与 sharedFilterSQL 口径一致）
	hostRows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, pack_type, code FROM kb_packages WHERE tenant_id=1 AND COALESCE(enabled,1)=1 AND pack_type IN ('industry','locale')")
	if err != nil {
		return scope, nil // 宿主查询失败不阻断：链内/企业层已装配
	}
	defer hostRows.Close()
	for hostRows.Next() {
		var id int64
		var ptype, code string
		if err := hostRows.Scan(&id, &ptype, &code); err != nil {
			continue
		}
		switch ptype {
		case PackIndustry:
			if industry != "" && code == industry {
				scope.SharedPackIDs[id] = true
			}
		case PackLocale:
			// 通用语言习惯包：全用户可见，独立最低优先级档
			scope.UniversalPackIDs[id] = true
		}
	}
	return scope, nil
}

// SetKBPackageCrossDeptShare 设置部门包的跨部门共享开关（包级 opt-out）。
// 参数：id=包 ID，tid=租户 ID（越权防护），share=1 共享 / 0 退出。
// 仅对 department 包生效（其他类型本就全局/全租户，无操作意义）。
func (s *Store) SetKBPackageCrossDeptShare(id, tid int64, share int) error {
	var ptype string
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT pack_type FROM kb_packages WHERE id=? AND tenant_id=?", id, tid).Scan(&ptype); err != nil {
		return fmt.Errorf("包不存在或无权操作")
	}
	if ptype != PackDepartment {
		return fmt.Errorf("仅部门包支持跨部门共享设置")
	}
	if share != 0 {
		share = 1
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_packages SET share_cross_dept=?, updated_at=? WHERE id=? AND tenant_id=?",
		share, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// SetKBPackageCrossScope 设置跨部门包的跨部门范围（维护/使用部门集合或全公司）。
// 参数：id/tid=包主键与租户；all=true 表示全公司（涵盖租户内全部部门）；
// orgs=跨部门包涵盖的部门集合（仅 all=false 时有效）。返回错误。
func (s *Store) SetKBPackageCrossScope(id, tid int64, all bool, orgs []int64) error {
	allInt := 0
	if all {
		allInt = 1
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE kb_packages SET cross_all=?, cross_orgs=?, updated_at=? WHERE id=? AND tenant_id=?",
		allInt, encodeCrossOrgs(orgs), time.Now().Format(time.RFC3339), id, tid)
	return err
}
