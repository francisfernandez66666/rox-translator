// ============ admin_scrape.go · 职责说明 ============
// api 包「行业包/语言文化包自动采集」管理接口（2026-09-01 新功能）。
// 全部接口仅超管（L4）：
//   GET    /api/admin/kb-scrape/sources     数据源列表（含启用状态）
//   POST   /api/admin/kb-scrape/sources     新增数据源
//   POST   /api/admin/kb-scrape/sources/upd 更新数据源
//   POST   /api/admin/kb-scrape/sources/status 启停数据源
//   POST   /api/admin/kb-scrape/sources/run 手动立即采集一轮
//   GET    /api/admin/kb-scrape/staged      待审池列表（条目+安全句+汇总）
//   POST   /api/admin/kb-scrape/approve     批量通过/驳回（通过→落正式库+热加载）
//   GET    /api/admin/kb-scrape/summary     概览（待审数/源数/最近完成日）
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/crawler"
	"translator/internal/store"
)

// requireSuperAdmin 校验当前请求用户为超管（L4，auth.IsSuperAdmin）。
// 返回：用户对象或错误（已写入 403 响应）。
func (s *Server) requireSuperAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return nil, err
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅平台超管可操作数据采集"})
		return nil, &apiErr{"非超管"}
	}
	return u, nil
}

// handleKBScrapeSources 数据源列表（超管）。
func (s *Server) handleKBScrapeSources(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	sources, err := s.Store.ListScrapeSources()
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "sources": sources})
}

// handleKBScrapeSourceCreate 新增数据源（超管）。
func (s *Server) handleKBScrapeSourceCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	var req store.KBScrapeSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "name 必填"})
		return
	}
	// 合法性校验
	switch req.Kind {
	case "official_api", "limited_web", "llm_gen":
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "kind 仅支持 official_api/limited_web/llm_gen"})
		return
	}
	switch req.PackType {
	case "industry", "locale":
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "pack_type 仅支持 industry/locale"})
		return
	}
	if req.Tier < 1 || req.Tier > 3 {
		req.Tier = 3
	}
	src, cerr := s.Store.CreateScrapeSource(&req)
	if cerr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": cerr.Error()})
		return
	}
	s.Store.LogAudit(1, u.ID, "kb_scrape_source_create", "kb_pack_sources", req.Name)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": src.ID})
}

// handleKBScrapeSourceUpdate 更新数据源（超管）。
func (s *Server) handleKBScrapeSourceUpdate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	var req store.KBScrapeSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "id 必填"})
		return
	}
	if req.Tier < 1 || req.Tier > 3 {
		req.Tier = 3
	}
	if uerr := s.Store.UpdateScrapeSource(req.ID, &req); uerr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": uerr.Error()})
		return
	}
	s.Store.LogAudit(1, u.ID, "kb_scrape_source_update", "kb_pack_sources", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBScrapeSourceStatus 启停数据源（超管）。
func (s *Server) handleKBScrapeSourceStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	var req struct {
		ID      int64 `json:"id"`
		Enabled int   `json:"enabled"` // 1=启用 0=停用
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 || (req.Enabled != 0 && req.Enabled != 1) {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if serr := s.Store.SetScrapeSourceEnabled(req.ID, req.Enabled); serr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": serr.Error()})
		return
	}
	s.Store.LogAudit(1, u.ID, "kb_scrape_source_status", "kb_pack_sources", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleKBScrapeSourceRun 手动立即采集一轮（超管；受负载判定约束，高占用自动暂停）。
func (s *Server) handleKBScrapeSourceRun(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	c := crawler.New(s.Store)
	if s.Engine != nil {
		c.LLM = s.Engine.LLM
	}
	c.Probe = func() bool { return s.lowOccupancyForScrape() }
	done, err := c.RunDaily(r.Context())
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "sources_done": done})
}

// handleKBScrapeStaged 待审池列表（超管）。
// query: pack_type / status / lang / limit / offset
func (s *Server) handleKBScrapeStaged(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	q := r.URL.Query()
	packType := q.Get("pack_type")
	status := q.Get("status")
	lang := q.Get("lang")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	entries, err := s.Store.ListStagedEntries(packType, status, lang, limit, offset)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	phrases, err := s.Store.ListStagedPhrases(status, lang, limit, offset)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "entries": entries, "phrases": phrases})
}

// handleKBScrapeApprove 批量审批：通过→落正式库（行业/语言文化条目用 SaveEntry，
// 语言文化安全句用 SaveSafetyPhraseEx）+ 热加载（invKB 失效缓存）；驳回→仅置状态。
// body: {kind:"entries"|"phrases", ids:[], action:"approve"|"reject"}
func (s *Server) handleKBScrapeApprove(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	var req struct {
		Kind   string  `json:"kind"`   // entries / phrases
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"` // approve / reject
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "ids 必填"})
		return
	}
	if req.Action != "approve" && req.Action != "reject" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "action 仅支持 approve/reject"})
		return
	}
	tid := int64(1) // 采集内容宿主租户1（平台共享行业/语言文化包）
	applied := 0
	if req.Action == "approve" {
		// 先读待审数据，应用后再置 approved（应用失败不置状态）
		if req.Kind == "entries" {
			items, gerr := s.Store.GetStagedEntriesByIDs(req.IDs)
			if gerr != nil {
				writeJSON(w, 200, map[string]interface{}{"success": false, "message": gerr.Error()})
				return
			}
			for _, e := range items {
				if e.TargetPackID <= 0 {
					continue
				}
				// 语言码白名单校验（SaveEntry 内部校验，此处按层写正式库）
				if _, serr := s.Store.SaveEntry(tid, e.TargetPackID, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, "scrape:"+strconv.FormatInt(e.SourceID, 10)); serr != nil {
					continue
				}
				applied++
			}
		} else {
			items, gerr := s.Store.GetStagedPhrasesByIDs(req.IDs)
			if gerr != nil {
				writeJSON(w, 200, map[string]interface{}{"success": false, "message": gerr.Error()})
				return
			}
			for _, p := range items {
				if p.PackageID <= 0 {
					continue
				}
				if _, serr := s.Store.SaveSafetyPhraseEx(tid, p.PackageID, p.Lang, p.Phrase, p.Kind, p.Replacement); serr != nil {
					continue
				}
				applied++
			}
		}
	}
	// 更新待审状态（approve/reject；仅 pending 可流转）
	n, serr := s.Store.SetStagedStatus(req.Kind, req.IDs, req.Action)
	if serr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": serr.Error()})
		return
	}
	if applied > 0 {
		// 热加载：失效 CJK 精确缓存（语言文化规则走 60s TTL 自动刷新，无需额外处理）
		s.invKB()
		// 行业/语言文化条目落正式库后，若为向量包可异步重建索引（无则跳过）
		s.rebuildIndexAsync()
	}
	s.Store.LogAudit(tid, u.ID, "kb_scrape_approve", "kb_staged_"+req.Kind,
		strings.Join([]string{req.Action, strconv.Itoa(applied), strconv.Itoa(n)}, "/"))
	writeJSON(w, 200, map[string]interface{}{"success": true, "updated": n, "applied": applied})
}

// handleKBScrapeSummary 采集概览（超管）。
func (s *Server) handleKBScrapeSummary(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "summary": s.Store.ScrapeStagedSummary()})
}
