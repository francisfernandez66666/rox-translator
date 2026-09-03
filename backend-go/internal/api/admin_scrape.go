// ============ admin_scrape.go · 职责说明 ============
// api 包「行业包/语言文化包自动采集」管理接口（2026-09-01 新功能）。
// 全部接口仅超管（L4）：
//
//	GET    /api/admin/kb-scrape/sources     数据源列表（含启用状态）
//	POST   /api/admin/kb-scrape/sources     新增数据源
//	POST   /api/admin/kb-scrape/sources/upd 更新数据源
//	POST   /api/admin/kb-scrape/sources/status 启停数据源
//	POST   /api/admin/kb-scrape/sources/run 手动立即采集一轮
//	GET    /api/admin/kb-scrape/staged      待审池列表（条目+安全句+汇总）
//	POST   /api/admin/kb-scrape/approve     批量通过/驳回（通过→落正式库+热加载）
//	GET    /api/admin/kb-scrape/summary     概览（待审数/源数/最近完成日）
//
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
	// 自动审批模式：采集即落正式库，采集后失效 KB 缓存 + 异步重建向量索引
	s.invKB()
	s.rebuildIndexAsync()
	writeJSON(w, 200, map[string]interface{}{"success": true, "sources_done": done})
}

// handleKBScrapeStaged 待审池列表（超管）。
// query: pack_type / status / lang / industry / limit / offset
// ★ 服务端分页：返回合并行集 rows + 精确总数 total（条目+安全句同口径），前端据此翻页
func (s *Server) handleKBScrapeStaged(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	q := r.URL.Query()
	packType := q.Get("pack_type")
	status := q.Get("status")
	lang := q.Get("lang")
	industry := q.Get("industry")
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, total, err := s.Store.ListStagedMerged(packType, status, lang, industry, limit, offset)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "rows": rows, "total": total, "limit": limit, "offset": offset,
	})
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
		Kind   string  `json:"kind"` // entries / phrases
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
	// 审批通过后按投稿租户累计源文字符数，用于功能⑥审批触发奖励
	rewardChars := map[int64]int64{} // tenant_id → 审批通过条目的源文字符数合计
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
				// 来源标记：采集投喂=scrape:<source_id>；用户投稿（source_id=0）=imported
				module := "imported"
				if e.SourceID > 0 {
					module = "scrape:" + strconv.FormatInt(e.SourceID, 10)
				}
				if _, serr := s.Store.SaveEntry(tid, e.TargetPackID, e.Layer, e.SrcLang, e.SrcText, e.TgtLang, e.TgtText, module); serr != nil {
					continue
				}
				applied++
				// 用户投稿（tenant_id>0）：累计源文字符数供奖励
				if e.TenantID > 0 {
					rewardChars[e.TenantID] += int64(len([]rune(e.SrcText)))
				}
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
	// 更新待审状态（approve→approved / reject→rejected；仅 pending 可流转）
	// ★ 修复：SetStagedStatus 仅接受 approved/rejected，此前直接把 "approve/reject" 传入导致状态从未更新。
	status := "approved"
	if req.Action == "reject" {
		status = "rejected"
	}
	n, serr := s.Store.SetStagedStatus(req.Kind, req.IDs, status)
	if serr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": serr.Error()})
		return
	}
	// 功能⑥ 审批触发奖励：用户投稿（tenant_id>0）通过后按源文字符数发永久 token
	rewards := []map[string]interface{}{}
	if req.Action == "approve" {
		for tidX, chars := range rewardChars {
			if tidX <= 0 || chars <= 0 {
				continue
			}
			if granted, tokens, used := s.Store.GrantKBRewardByChars(tidX, 0, 0, chars); granted {
				rewards = append(rewards, map[string]interface{}{
					"tenant_id": tidX, "tokens": tokens, "chars": chars, "daily_used": used,
					"per_char": s.Store.KBRewardTokensPerChar(),
				})
				s.Store.LogAudit(tidX, 0, "kb_review_reward", "balance_accounts",
					strings.Join([]string{strconv.FormatInt(chars, 10), strconv.FormatInt(tokens, 10)}, "/"))
			}
		}
	}
	if applied > 0 {
		// 热加载：失效 CJK 精确缓存（语言文化规则走 60s TTL 自动刷新，无需额外处理）
		s.invKB()
		// 行业/语言文化条目落正式库后，若为向量包可异步重建索引（无则跳过）
		s.rebuildIndexAsync()
	}
	s.Store.LogAudit(tid, u.ID, "kb_scrape_approve", "kb_staged_"+req.Kind,
		strings.Join([]string{req.Action, strconv.Itoa(applied), strconv.Itoa(n)}, "/"))
	writeJSON(w, 200, map[string]interface{}{"success": true, "updated": n, "applied": applied, "rewards": rewards})
}

// handleKBScrapeRestore 还原为待审（超管）：把已通过/已驳回的待审条目或安全句拉回待审池，
// 支持还原前编辑内容（修改译文/替换词等），并回收已通过条目在正式库的落库（kb_entries/tm_segments、
// kb_safety_phrases）+ 失效缓存，保证「还原」语义完整。
// body: {kind:"entries"|"phrases", ids:[], edits:{ "<id>": {src_text?,tgt_text?} | {phrase?,replacement?} }}
func (s *Server) handleKBScrapeRestore(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	var req struct {
		Kind  string                    `json:"kind"`
		IDs   []int64                   `json:"ids"`
		Edits map[string]map[string]any `json:"edits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "ids 必填"})
		return
	}
	if req.Kind != "entries" && req.Kind != "phrases" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "kind 仅支持 entries/phrases"})
		return
	}
	tid := int64(1) // 采集内容宿主租户1
	reverted := 0
	if req.Kind == "entries" {
		items, gerr := s.Store.GetStagedEntriesAllByIDs(req.IDs)
		if gerr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": gerr.Error()})
			return
		}
		for _, e := range items {
			// 可选：还原前编辑内容
			if ed, ok := req.Edits[strconv.FormatInt(e.ID, 10)]; ok {
				srcText, tgtText := e.SrcText, e.TgtText
				if v, ok := ed["src_text"].(string); ok && v != "" {
					srcText = v
				}
				if v, ok := ed["tgt_text"].(string); ok && v != "" {
					tgtText = v
				}
				s.Store.UpdateStagedEntryContent(e.ID, srcText, tgtText)
			}
			// 已通过（已落正式库）→ 回收正式库条目与检索层
			if e.Status == "approved" && e.TargetPackID > 0 {
				_ = s.Store.DeleteAppliedEntry(tid, e.TargetPackID, e.SrcLang, e.SrcText, e.TgtLang)
			}
			reverted++
		}
	} else {
		items, gerr := s.Store.GetStagedPhrasesAllByIDs(req.IDs)
		if gerr != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": gerr.Error()})
			return
		}
		for _, p := range items {
			if ed, ok := req.Edits[strconv.FormatInt(p.ID, 10)]; ok {
				phrase, replacement := p.Phrase, p.Replacement
				if v, ok := ed["phrase"].(string); ok && v != "" {
					phrase = v
				}
				if v, ok := ed["replacement"].(string); ok {
					replacement = v
				}
				s.Store.UpdateStagedPhraseContent(p.ID, phrase, replacement)
			}
			if p.Status == "approved" && p.PackageID > 0 {
				_ = s.Store.DeleteAppliedPhrase(tid, p.PackageID, p.Lang, p.Phrase, p.Replacement)
			}
			reverted++
		}
	}
	n, serr := s.Store.SetStagedStatus(req.Kind, req.IDs, "pending")
	if serr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": serr.Error()})
		return
	}
	if n > 0 {
		// 失效 KB 缓存 + 异步重建语义索引（撤销落库后引用不再命中）
		s.invKB()
		s.rebuildIndexAsync()
	}
	s.Store.LogAudit(tid, u.ID, "kb_scrape_restore", "kb_staged_"+req.Kind,
		strings.Join([]string{strconv.Itoa(reverted), strconv.Itoa(n)}, "/"))
	writeJSON(w, 200, map[string]interface{}{"success": true, "restored": n, "reverted": reverted})
}

// handleKBScrapeSummary 采集概览（超管）。
func (s *Server) handleKBScrapeSummary(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireSuperAdmin(w, r); err != nil {
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "summary": s.Store.ScrapeStagedSummary()})
}

// handleKBRewardConfig 知识库上传奖励开关配置（仅超管）。
// GET：读取当前开关/单价/日封顶/今日已发放；POST：更新。
func (s *Server) handleKBRewardConfig(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireSuperAdmin(w, r)
	if err != nil {
		return
	}
	if r.Method == "GET" {
		writeJSON(w, 200, map[string]interface{}{
			"success":   true,
			"enabled":   s.Store.KBRewardEnabled(),
			"per_char":  s.Store.KBRewardTokensPerChar(),
			"daily_cap": s.Store.KBRewardDailyCap(),
		})
		return
	}
	// POST：更新开关与单价（0 值不动，避免误清）
	var req struct {
		Enabled  *bool `json:"enabled"` // nil=不修改
		PerChar  int64 `json:"per_char"`
		DailyCap int64 `json:"daily_cap"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	changed := []string{}
	if req.Enabled != nil {
		v := "0"
		if *req.Enabled {
			v = "1"
		}
		if err := s.Store.SetConfig("kb_upload_reward_enabled", v); err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "保存开关失败: " + err.Error()})
			return
		}
		changed = append(changed, "enabled="+v)
	}
	if req.PerChar > 0 {
		if err := s.Store.SetConfig("kb_upload_reward_tokens_per_char", strconv.FormatInt(req.PerChar, 10)); err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "保存单价失败: " + err.Error()})
			return
		}
		changed = append(changed, "per_char="+strconv.FormatInt(req.PerChar, 10))
	}
	if req.DailyCap > 0 {
		if err := s.Store.SetConfig("kb_upload_reward_daily_cap", strconv.FormatInt(req.DailyCap, 10)); err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "message": "保存日封顶失败: " + err.Error()})
			return
		}
		changed = append(changed, "daily_cap="+strconv.FormatInt(req.DailyCap, 10))
	}
	s.Store.LogAudit(0, u.ID, "kb_reward_config", "system_config", strings.Join(changed, ","))
	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"enabled":   s.Store.KBRewardEnabled(),
		"per_char":  s.Store.KBRewardTokensPerChar(),
		"daily_cap": s.Store.KBRewardDailyCap(),
	})
}
