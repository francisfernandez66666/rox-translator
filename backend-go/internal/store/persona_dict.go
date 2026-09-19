// ============================================================================
// store/persona_dict.go — 用户职业角色（job_role）与角色知识库包（persona pack）
// （2026-09-19 需求：个人/企业用户可选职业角色，角色专业用词从维基/LLM 采集，
//  模仿行业包体系。）
// 设计口径（与行业包对齐，但**只绑用户层级、不与企业耦合**）：
//   - 角色字典唯一数据源 = kb_packages 中 pack_type='persona' 的包，宿主恒为平台租户0；
//   - users.job_role 存角色包 code（如 frontend）；退出企业仍存在，用户可自行维护（转岗语义）；
//   - 检索装配见 persona_scope.go（PersonaPackIDs，优先级 350 档：行业 300 之下、语言文化 400 之上）；
//   - 本文件独立于 kbpackages.go（冻结规则：只减不增）。
// ============================================================================
package store

import (
	"database/sql"
	"time"
	"translator/internal/db"
)

// PackPersona 角色知识库包类型（宿主租户0，全租户按用户 job_role 装配）。
const PackPersona = "persona"

// builtinPersonas 出厂角色字典（幂等种入）：code → 显示名。
// code 与前端 roles 选择器、tier1/tier3 采集种子（crawler.builtinPersonaSeeds/personaNames）一一对齐。
var builtinPersonas = []struct{ code, name string }{
	{"fullstack", "全栈工程师"},
	{"frontend", "前端开发"},
	{"backend", "后端开发"},
	{"pm", "产品经理"},
	{"pj", "项目经理"},
	{"uiux", "UI/UX 设计"},
	{"ops", "运营"},
	{"sales", "销售"},
}

// PersonaMigrate 角色功能幂等迁移（store.New 挂载）：
// ①users 补 job_role 列；②种入 8 个出厂角色包（已存在同 code 跳过，不覆盖超管改名/停用）。
func (s *Store) PersonaMigrate() {
	// 补列失败不阻断启动（与其它迁移口径一致；查询侧 COALESCE 兜底缺列老库）
	_ = db.EnsureColumns(s.db, db.CurrentDialect(), "users", map[string]string{
		"job_role": "TEXT NOT NULL DEFAULT ''",
	})
	for _, p := range builtinPersonas {
		// kb_packages 无 (tenant_id,code) 唯一约束，先查重再种入（重复启动幂等）
		exists, err := s.PersonaCodeExists(p.code)
		if err != nil || exists {
			continue
		}
		_, _ = s.CreateKBPackage(SharedHostTenant, 0, p.code, p.name, PackPersona, PackRoleSource)
		// 出厂采集源（对齐 SeedDefaultScrapeSources 口径）：按角色种子词从维基词典拉取英文对照，
		// 低占用调度自动跑；超管可在「数据源」面板改词表 URL/停用或补 llm_gen 源。
		_, _ = s.CreateScrapeSource(&KBScrapeSource{
			Kind: "official_api", Name: "角色·" + p.name + "专业用词（维基词典）",
			Lang: "en", Industry: p.code, PackType: PackPersona,
			Enabled: 1, FreqHours: 24, Tier: 2,
		})
	}
}

// ListPersonas 列出平台全部角色包（pack_type=persona，宿主租户0）——角色字典唯一数据源。
// 按 sort_order 升序，供注册角色下拉、个人中心角色维护、后台角色管理面板共用。
func (s *Store) ListPersonas() ([]*KBPackage, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+kbPkgCols+" FROM kb_packages WHERE tenant_id=? AND pack_type=? ORDER BY sort_order, id", SharedHostTenant, PackPersona)
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

// FindEnabledPersonaByCode 按 code 查找启用中的角色包（注册/改角色校验用；停用包视为不可选）。
func (s *Store) FindEnabledPersonaByCode(code string) (*KBPackage, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+kbPkgCols+" FROM kb_packages WHERE tenant_id=? AND pack_type=? AND code=? AND COALESCE(enabled,1)=1 LIMIT 1",
		SharedHostTenant, PackPersona, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if rows.Next() {
		return scanKBPackage(rows)
	}
	return nil, sql.ErrNoRows
}

// PersonaCodeExists 判断角色 code 是否已存在（平台宿主租户0 全局唯一）。创建前查重。
func (s *Store) PersonaCodeExists(code string) (bool, error) {
	var cnt int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM kb_packages WHERE tenant_id=? AND pack_type=? AND code=?", SharedHostTenant, PackPersona, code).Scan(&cnt)
	return cnt > 0, err
}

// UpdatePersona 更新角色包显示名（超管维护）。仅操作平台宿主租户0 的角色包。
func (s *Store) UpdatePersona(id int64, name string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_packages SET name=?, updated_at=? WHERE id=? AND tenant_id=? AND pack_type=?",
		name, time.Now().Format(time.RFC3339), id, SharedHostTenant, PackPersona)
	return err
}

// TogglePersona 启用/停用角色包（停用后不再参与翻译命中，也不再出现在注册/个人页下拉）。
func (s *Store) TogglePersona(id int64, enabled int) error {
	invalidateTermCache()
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE kb_packages SET enabled=?, updated_at=? WHERE id=? AND tenant_id=? AND pack_type=?",
		enabled, time.Now().Format(time.RFC3339), id, SharedHostTenant, PackPersona)
	return err
}

// DeletePersona 删除角色包（连带其下条目与安全句）。仅操作宿主租户0 的角色包。
func (s *Store) DeletePersona(id int64) error {
	invalidateTermCache()
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM kb_entries WHERE package_id=? AND tenant_id=?", id, SharedHostTenant); err != nil {
		return err
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM kb_safety_phrases WHERE package_id=? AND tenant_id=?", id, SharedHostTenant); err != nil {
		return err
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM kb_packages WHERE id=? AND tenant_id=? AND pack_type=?", id, SharedHostTenant, PackPersona)
	return err
}

// PersonaReferenced 判断角色 code 是否仍被用户引用（users.job_role 指向该 code）。
// 供角色删除前置校验——被引用的角色禁止删除（检索装配会静默失效，应停用替代）。
func (s *Store) PersonaReferenced(code string) (bool, error) {
	var cnt int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM users WHERE job_role=?", code).Scan(&cnt)
	return cnt > 0, err
}
