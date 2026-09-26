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
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// requireSuperAdmin 校验当前请求用户为超管（L4，auth.IsSuperAdmin）。
// 返回：用户对象或错误（已写入 403 响应）。
func (s *Server) requireSuperAdmin(w http.ResponseWriter, r *http.Request) (*store.User, error) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// F-64②：查询失败是服务端出错（500），旧写法回 200 让管理台把它当成功渲染空列表
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	case "industry", "locale", "persona": // ★ 角色功能（2026-09-19）：persona 角色包纳入采集口径
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "pack_type 仅支持 industry/locale/persona"})
		return
	}
	// Tier 优先级限 1..3，越界一律回落最低优先级 3
	if req.Tier < 1 || req.Tier > 3 {
		req.Tier = 3
	}
	src, cerr := s.Store.CreateScrapeSource(&req)
	if cerr != nil {
		// F-64②：kb_pack_sources 无 name 唯一约束，插入失败只可能是数据库出错（500），
		// 不是「名称重复」的 409 冲突；旧写法回 200 会让管理台 toast 判成功。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, cerr.Error()))
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
	// Tier 优先级限 1..3，越界一律回落最低优先级 3
	if req.Tier < 1 || req.Tier > 3 {
		req.Tier = 3
	}
	if uerr := s.Store.UpdateScrapeSource(req.ID, &req); uerr != nil {
		// F-64②：UPDATE 对不存在的 id 不报错（0 行影响），走到这里只能是数据库执行失败（500）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, uerr.Error()))
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
		// F-64②：启停走 UPDATE ... WHERE id=?，id 不存在不报错（0 行影响），
		// 此处失败只能是数据库执行出错（500），旧 200 壳让管理台开关组件误判已生效。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, serr.Error()))
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
	// 复用全局引擎的 LLM 客户端，供 llm_gen 类数据源生成条目
	if s.Engine != nil {
		c.LLM = s.Engine.LLM
	}
	c.Probe = func() bool { return s.lowOccupancyForScrape() }
	done, err := c.RunDaily(r.Context())
	if err != nil {
		// F-64②：RunDaily 返回的 err 只有三类——store 未初始化、启用源列表查询失败、
		// 上下文取消；单个数据源的上游抓取失败被引擎吞成日志并记入 last_status，
		// 不会走到这里，故本层无从区分 404/409/502，诚实语义＝服务端出错（500）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	// 过滤参数透传 store；limit 夹取到 1..500（缺省 200），offset 负值归零
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
	// 合并行集查询（条目+安全句同口径），total 为精确总数供前端翻页
	rows, total, err := s.Store.ListStagedMerged(packType, status, lang, industry, limit, offset)
	if err != nil {
		// F-64②：待审池合并查询失败是服务端出错（500），旧 200 壳让管理台当成功渲染空表
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
	var tid int64 = store.SharedHostTenant // 采集内容宿主=平台共享包宿主租户（行业包/语言文化包，store.SharedHostTenant=0）
	applied := 0
	// 审批通过后按投稿租户累计源文字符数，用于功能⑥审批触发奖励
	rewardChars := map[int64]int64{} // tenant_id → 审批通过条目的源文字符数合计
	if req.Action == "approve" {
		// 先读待审数据，应用后再置 approved（应用失败不置状态）
		if req.Kind == "entries" {
			items, gerr := s.Store.GetStagedEntriesByIDs(req.IDs)
			if gerr != nil {
				// F-64②：批量读取待审条目失败是服务端出错（500），不是「条目不存在」的 404——
				// 读的是本次审批动作的输入集，读不通整批审批无从谈起。
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, gerr.Error()))
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
				// F-64②：读取待审安全句失败同上——服务端出错（500），旧 200 壳会让 toast 误报审批完成
				s.writeError(w, r, apierrors.New(apierrors.ErrInternal, gerr.Error()))
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
		// F-64②：SetStagedStatus 只在 SQL 执行失败时报错（「非 pending 不可流转」表现为
		// RowsAffected=0 而非 error），走到这里＝数据库出错（500），旧写法回 200 会被当成审批成功。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, serr.Error()))
		return
	}
	// 功能⑥ 审批触发奖励：用户投稿（tenant_id>0）通过后按源文字符数发放永久余额
	// （内部 token 记账，出参折积分，2026-09-19）
	rewards := []map[string]interface{}{}
	if req.Action == "approve" {
		for tidX, chars := range rewardChars {
			if tidX <= 0 || chars <= 0 {
				continue
			}
			if granted, tokens, used := s.Store.GrantKBRewardByChars(tidX, 0, 0, chars); granted {
				rewards = append(rewards, map[string]interface{}{
					"tenant_id": tidX, "reward_points": s.Store.PointsFromTokens(tokens),
					"chars": chars, "daily_used_points": s.Store.PointsFromTokens(used),
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
	var tid int64 = store.SharedHostTenant // 采集内容宿主=平台共享包宿主租户（行业包/语言文化包，store.SharedHostTenant=0）
	reverted := 0
	if req.Kind == "entries" {
		items, gerr := s.Store.GetStagedEntriesAllByIDs(req.IDs)
		if gerr != nil {
			// F-64②：还原前读取待审条目失败是服务端出错（500），旧 200 壳让管理台误报还原成功
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, gerr.Error()))
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
		// 安全句撤销：edits 覆盖 phrase/replacement 回写待审行；已落库的先删正式库安全句
		items, gerr := s.Store.GetStagedPhrasesAllByIDs(req.IDs)
		if gerr != nil {
			// F-64②：读取待审安全句（含已通过态）失败是服务端出错（500），
			// 旧 200 壳会让「还原」按钮弹成功、正式库残留已落库短语。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, gerr.Error()))
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
	// 统一把目标行退回 pending 并计数；成功数 n>0 时失效缓存+重建索引（见下）
	n, serr := s.Store.SetStagedStatus(req.Kind, req.IDs, "pending")
	if serr != nil {
		// F-64②：退回 pending 的 UPDATE 失败＝数据库出错（500）；「已处于 pending」不算错，
		// 由 RowsAffected=0 静默回 n=0，不会走到这里，故无需 409 分支。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, serr.Error()))
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
	// 三项开关各自独立 SetConfig（仅传了有效值才动），changed 收集明细供审计追溯
	changed := []string{}
	if req.Enabled != nil {
		v := "0"
		if *req.Enabled {
			v = "1"
		}
		if err := s.Store.SetConfig("kb_upload_reward_enabled", v); err != nil {
			// F-64②：开关写入失败是数据库 upsert 出错（500）；SetConfig 无校验分支，
			// 不存在 400 语义，旧 200 壳会让管理台开关回显「已保存」但库里没动。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存开关失败: "+err.Error()))
			return
		}
		changed = append(changed, "enabled="+v)
	}
	// 单价/日封顶仅在 >0 时写入（0=不覆盖既有配置，防误清）
	if req.PerChar > 0 {
		if err := s.Store.SetConfig("kb_upload_reward_tokens_per_char", strconv.FormatInt(req.PerChar, 10)); err != nil {
			// F-64②：单价写入失败同样是数据库出错（500），与开关项独立判断、独立报错，
			// 便于运维定位是哪一项配置没落库（文案原样保留 "保存单价失败: " 前缀）。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存单价失败: "+err.Error()))
			return
		}
		changed = append(changed, "per_char="+strconv.FormatInt(req.PerChar, 10))
	}
	if req.DailyCap > 0 {
		if err := s.Store.SetConfig("kb_upload_reward_daily_cap", strconv.FormatInt(req.DailyCap, 10)); err != nil {
			// F-64②：日封顶写入失败＝数据库出错（500）；本函数三处失败分支同为写库语义，
			// 逐项保留各自文案前缀，不做一刀切合并（GET 读取分支与 400 校验分支不在本批射程）。
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存日封顶失败: "+err.Error()))
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
