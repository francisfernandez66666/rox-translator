package store

import (
	"time"
)

// KBPackage 知识库包（三级分层：企业包>行业包>语言文化习惯包，树形继承）
type KBPackage struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	ParentID  int64  `json:"parent_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	PackType  string `json:"pack_type"` // tenant(企业) / industry(行业) / locale(语言文化)
	Role      string `json:"role"`      // source(匹配来源) / gate(输出闸门)
	SortOrder int    `json:"sort_order"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// 包类型常量
const (
	PackTenant   = "tenant"
	PackIndustry = "industry"
	PackLocale   = "locale"
)

// 包角色常量
const (
	PackRoleSource = "source" // 匹配来源
	PackRoleGate   = "gate"   // 输出闸门
)

// KBEntry 知识库条目（四层统一表）
type KBEntry struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	PackageID   int64  `json:"package_id"`
	Layer       int    `json:"layer"` // 1术语/2TM/3安全句/4碎片
	SourceLang  string `json:"source_lang"`
	SourceText  string `json:"source_text"`
	TargetLang  string `json:"target_lang"`
	TargetText  string `json:"target_text"`
	Module      string `json:"module"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// KBSafetyPhrase 安全句锁死串库
type KBSafetyPhrase struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	PackageID int64  `json:"package_id"`
	Lang      string `json:"lang"`
	Phrase    string `json:"phrase"`
	CreatedAt string `json:"created_at"`
}

// 层常量
const (
	LayerTerm   = 1 // L1 术语
	LayerTM     = 2 // L2 翻译记忆
	LayerSafety = 3 // L3 安全句
	LayerFrag   = 4 // L4 AI 碎片
)

// ============ 包 ============

// CreateKBPackage 创建包
func (s *Store) CreateKBPackage(tid int64, parentID int64, code, name, packType, role string) (*KBPackage, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, sort_order, created_at, updated_at) VALUES (?,?,?,?,?,?,0,?,?)",
		tid, parentID, code, name, packType, role, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetKBPackage(id, tid)
}

// GetKBPackage 查询包
func (s *Store) GetKBPackage(id, tid int64) (*KBPackage, error) {
	var p KBPackage
	err := s.db.QueryRow("SELECT id, tenant_id, parent_id, code, name, pack_type, role, sort_order, created_at, updated_at FROM kb_packages WHERE id=? AND tenant_id=?", id, tid).
		Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListKBPackages 列出租户全部包
func (s *Store) ListKBPackages(tid int64) ([]*KBPackage, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, parent_id, code, name, pack_type, role, sort_order, created_at, updated_at FROM kb_packages WHERE tenant_id=? ORDER BY pack_type, sort_order, id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackage
	for rows.Next() {
		var p KBPackage
		if err := rows.Scan(&p.ID, &p.TenantID, &p.ParentID, &p.Code, &p.Name, &p.PackType, &p.Role, &p.SortOrder, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

// UpdateKBPackage 更新包
func (s *Store) UpdateKBPackage(id, tid int64, name string) error {
	_, err := s.db.Exec("UPDATE kb_packages SET name=?, updated_at=? WHERE id=? AND tenant_id=?",
		name, time.Now().Format(time.RFC3339), id, tid)
	return err
}

// DeleteKBPackage 删除包（连带其条目）
func (s *Store) DeleteKBPackage(id, tid int64) error {
	if _, err := s.db.Exec("DELETE FROM kb_entries WHERE package_id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	if _, err := s.db.Exec("DELETE FROM kb_safety_phrases WHERE package_id=? AND tenant_id=?", id, tid); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM kb_packages WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// EnsureDefaultPackages 确保租户存在默认三级包结构（企业包/行业包/语言文化习惯包）
func (s *Store) EnsureDefaultPackages(tid int64) error {
	defs := []struct {
		code, name, packType, role string
	}{
		{"tenant", "企业包", PackTenant, PackRoleSource},
		{"industry", "行业包", PackIndustry, PackRoleSource},
		{"locale", "语言文化习惯包", PackLocale, PackRoleGate},
	}
	for _, d := range defs {
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

// ============ 条目 ============

// SaveEntry 新增/更新 KB 条目
func (s *Store) SaveEntry(tid, pkgID int64, layer int, srcLang, srcText, tgtLang, tgtText, module string) (int64, error) {
	now := time.Now().Format(time.RFC3339)
	var id int64
	err := s.db.QueryRow("SELECT id FROM kb_entries WHERE tenant_id=? AND package_id=? AND source_lang=? AND source_text=? AND target_lang=?", tid, pkgID, srcLang, srcText, tgtLang).Scan(&id)
	if err == nil {
		_, err := s.db.Exec("UPDATE kb_entries SET layer=?, target_text=?, module=?, updated_at=? WHERE id=?",
			layer, tgtText, module, now, id)
		return id, err
	}
	res, err := s.db.Exec("INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, pkgID, layer, srcLang, srcText, tgtLang, tgtText, module, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListEntries 列出包内条目
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
			continue
		}
		out = append(out, &e)
	}
	return out, nil
}

// DeleteEntry 删除条目
func (s *Store) DeleteEntry(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM kb_entries WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// FindEntriesBySource 按源文本查找匹配来源包（企业包优先）中的条目
// 返回企业包→行业包按优先级命中的条目列表
func (s *Store) FindEntriesBySource(tid int64, srcLang, srcText string) ([]*KBEntry, error) {
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
			continue
		}
		out = append(out, &e)
	}
	return out, nil
}

// ============ 安全句 ============

// SaveSafetyPhrase 新增安全句
func (s *Store) SaveSafetyPhrase(tid, pkgID int64, lang, phrase string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO kb_safety_phrases (tenant_id, package_id, lang, phrase, created_at) VALUES (?,?,?,?,?)",
		tid, pkgID, lang, phrase, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListSafetyPhrases 列出安全句
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
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

// DeleteSafetyPhrase 删除安全句
func (s *Store) DeleteSafetyPhrase(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM kb_safety_phrases WHERE id=? AND tenant_id=?", id, tid)
	return err
}