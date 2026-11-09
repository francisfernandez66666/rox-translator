// ============ sales_config_api.go · 职责说明 ============
// 二期「能前端配置的都交给前端配置」的后端半边：租户管理员在管理台配
// ① 销售模型（阶段 → 模型档位）② 销售流程引擎（开关／巡检／步数）③ 销售策略（场景／语种／词表）。
// ★ 三段配置合成一份 sales_config JSON，一租户一行；词表另走 sales_lexicon 四档表。
// 全部路由的鉴权都在**函数体内**取人（route_auth_gate_test.go 按路由全集判），写口只允许租户管理员及以上。
// ★ 诚实边界：流程／策略引擎本体尚未迁入，本批交付的是「配置面＋读口」，
// 引擎装配随余包平移批接；配置写口现在就是可用且可审计的，不是空壳页面。
// ================================================
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/salesflow"
	"translator/internal/store"
)

// 销售模型阶段编码（前端卡片与配置 JSON 共用一份真值，禁止各写一套字符串）。
const (
	// SalesStageReply 销售应答阶段。
	SalesStageReply = "sales_reply"
	// SalesStageMining 话术挖掘阶段。
	SalesStageMining = "sales_mining"
	// SalesStageFlow 流程推进阶段。
	SalesStageFlow = "sales_flow"
	// SalesStageStrategy 策略选锚阶段。
	SalesStageStrategy = "sales_strategy"
)

// salesStageCodes 返回全部销售模型阶段编码（按全集判，新增阶段必须同步扩断言）。
// 参数：无；返回：阶段编码切片。
func salesStageCodes() []string {
	return []string{SalesStageReply, SalesStageMining, SalesStageFlow, SalesStageStrategy}
}

// SalesFlowConfig 销售流程引擎的可配参数。
type SalesFlowConfig struct {
	Enabled      bool   `json:"enabled"`
	SweepRequeue bool   `json:"sweep_requeue"`
	MaxSteps     int    `json:"max_steps"`
	DefaultScene string `json:"default_scene"`
}

// SalesStrategyConfig 销售策略的可配参数。
type SalesStrategyConfig struct {
	Scene           string `json:"scene"`
	Lang            string `json:"lang"`
	MinSamples      int    `json:"min_samples"`
	DraftEnabled    bool   `json:"draft_enabled"`
	MaxDraftsPerRun int    `json:"max_drafts_per_run"`
	WindowDays      int    `json:"window_days"`
}

// SalesConfig 管理台保存的三段配置（模型／流程／策略）。
type SalesConfig struct {
	ModelStages map[string]string   `json:"model_stages"`
	Flow        SalesFlowConfig     `json:"flow"`
	Strategy    SalesStrategyConfig `json:"strategy"`
}

// defaultSalesConfig 出厂默认：全关、静默、不自动产草稿（§7 W6「上线即静默」）。
// 参数：无；返回：默认配置。
func defaultSalesConfig() SalesConfig {
	stages := map[string]string{}
	for _, c := range salesStageCodes() {
		stages[c] = ""
	}
	return SalesConfig{
		ModelStages: stages,
		Flow:        SalesFlowConfig{Enabled: false, SweepRequeue: false, MaxSteps: 20, DefaultScene: salesflow.SceneCrossBorder},
		Strategy: SalesStrategyConfig{
			Scene: salesflow.SceneCrossBorder, Lang: "zh", MinSamples: 5,
			DraftEnabled: false, MaxDraftsPerRun: 10, WindowDays: 30,
		},
	}
}

// validSalesScene 判定场景编码是否在三面配置全集里。
// 参数：scene=场景编码；返回：是否合法。
func validSalesScene(scene string) bool {
	for _, c := range []string{salesflow.SceneCrossBorder, salesflow.ScenePrivate, salesflow.SceneInStore} {
		if scene == c {
			return true
		}
	}
	return false
}

// validateSalesConfig 三段配置的唯一校验口：越界档位、未知阶段、非法语种一律 400 且不落库。
// 参数：cfg=待保存配置；返回：校验错误。
func validateSalesConfig(cfg SalesConfig) error {
	if len(cfg.ModelStages) > len(salesStageCodes()) {
		return errors.New("销售模型阶段数超出全集")
	}
	for k, v := range cfg.ModelStages {
		known := false
		for _, c := range salesStageCodes() {
			if k == c {
				known = true
			}
		}
		if !known {
			return fmt.Errorf("未知销售模型阶段: %s", k)
		}
		if len(strings.TrimSpace(v)) > 128 {
			return errors.New("模型档位名过长")
		}
	}
	if cfg.Flow.MaxSteps < 1 || cfg.Flow.MaxSteps > 100 {
		return errors.New("流程最大步数必须在 1..100")
	}
	if !validSalesScene(cfg.Flow.DefaultScene) {
		return errors.New("流程默认场景不在三面全集里")
	}
	s := cfg.Strategy
	if !validSalesScene(s.Scene) {
		return errors.New("策略场景不在三面全集里")
	}
	if s.Lang != "zh" && s.Lang != "en" {
		return errors.New("策略语种只许 zh/en（其余语种档由运营后续配置）")
	}
	if s.MinSamples < 1 || s.MinSamples > 1000 {
		return errors.New("挖掘最小样本数必须在 1..1000")
	}
	if s.MaxDraftsPerRun < 0 || s.MaxDraftsPerRun > 1000 {
		return errors.New("单轮草稿上限必须在 0..1000")
	}
	if s.WindowDays < 1 || s.WindowDays > 365 {
		return errors.New("挖掘窗口天数必须在 1..365")
	}
	return nil
}

// salesConfigAdmin 统一取租户管理员与生效租户（新路由一律走这把强闸）。
// 参数：w=响应；r=请求；返回：用户、租户、是否继续。
func (s *Server) salesConfigAdmin(w http.ResponseWriter, r *http.Request) (*store.User, int64, bool) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return nil, 0, false
	}
	return u, s.effTenant(r, u), true
}

// routesSalesConfig 注册销售配置管理路由（由 routesSales() 调用）。
// 参数：无；返回：无。
func (s *Server) routesSalesConfig() {
	s.mux.HandleFunc("/api/admin/sales/config", s.handleSalesConfigGet)
	s.mux.HandleFunc("/api/admin/sales/config/save", s.handleSalesConfigSave)
	s.mux.HandleFunc("/api/admin/sales/lexicon/save", s.handleSalesLexiconSave)
	s.mux.HandleFunc("/api/admin/sales/lexicon/delete", s.handleSalesLexiconDelete)
	s.mux.HandleFunc("/api/admin/sales/lexicon/seed", s.handleSalesLexiconSeed)
}

// handleSalesConfigGet 回显三段配置＋词表全集＋枚举值（未配置时回出厂默认，不落库）。
// 参数 w: HTTP 响应；r: 请求。返回：无。
func (s *Server) handleSalesConfigGet(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	cfg := defaultSalesConfig()
	raw, found, err := s.Store.GetSalesConfig(tid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if found {
		if e := json.Unmarshal([]byte(raw), &cfg); e != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "销售配置解析失败"))
			return
		}
	}
	rows, err := s.Store.ListLexicon(tid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "tenant_id": tid, "user_id": u.ID,
		"config": cfg, "configured": found, "lexicon": rows,
		"stages": salesStageCodes(), "kinds": salesflow.LexiconKinds(),
		"scenes": []string{salesflow.SceneCrossBorder, salesflow.ScenePrivate, salesflow.SceneInStore},
		"langs":  []string{"zh", "en"},
	})
}

// handleSalesConfigSave 保存三段配置（唯一配置写口，留审计）。
// 参数 w: HTTP 响应；r: 请求（body 为 SalesConfig）。返回：无。
func (s *Server) handleSalesConfigSave(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	var cfg SalesConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "销售配置格式错误"))
		return
	}
	if err := validateSalesConfig(cfg); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "销售配置校验失败: "+err.Error()))
		return
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "销售配置序列化失败"))
		return
	}
	if err := s.Store.SaveSalesConfig(tid, string(raw)); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "sales_config_save", "sales_config", "scope=tenant")
	writeJSON(w, 200, map[string]interface{}{"success": true, "config": cfg})
}

// salesLexiconWriteRequest 词表写口请求体（保存与删除共用形状）。
type salesLexiconWriteRequest struct {
	ID    int64    `json:"id"`
	Scene string   `json:"scene"`
	Lang  string   `json:"lang"`
	Kind  string   `json:"kind"`
	Words []string `json:"words"`
}

// validSalesLexiconKind 判定词表类别是否在配置层全集里。
// 参数：kind=类别编码；返回：是否合法。
func validSalesLexiconKind(kind string) bool {
	for _, k := range salesflow.LexiconKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// handleSalesLexiconSave 保存一档租户词表（整份覆盖；平台默认档不许由租户写）。
// 参数 w: HTTP 响应；r: 请求。返回：无。
func (s *Server) handleSalesLexiconSave(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	var req salesLexiconWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "词表格式错误"))
		return
	}
	if !validSalesScene(req.Scene) || !validSalesLexiconKind(req.Kind) || (req.Lang != "zh" && req.Lang != "en") {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "词表档位不在场景/语种/类别全集里"))
		return
	}
	if len(req.Words) > 200 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "单档词表最多 200 条"))
		return
	}
	if err := s.Store.UpsertLexicon(tid, req.Scene, req.Lang, req.Kind, req.Words); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, u.ID, "sales_config_save", "sales_lexicon", req.Scene+"/"+req.Lang+"/"+req.Kind)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleSalesLexiconDelete 删除租户自有的一档（跨租户与平台默认档在 store 层已被拒）。
// 参数 w: HTTP 响应；r: 请求。返回：无。
func (s *Server) handleSalesLexiconDelete(w http.ResponseWriter, r *http.Request) {
	u, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	var req salesLexiconWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "词表删除格式错误"))
		return
	}
	deleted, err := s.Store.DeleteLexicon(req.ID, tid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if !deleted {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "词表档不存在或不属于本租户"))
		return
	}
	s.Store.LogAudit(tid, u.ID, "sales_config_save", "sales_lexicon", "delete")
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": req.ID})
}

// handleSalesLexiconSeed 幂等补种平台默认档（只补缺失，不覆盖运营改动；仅平台超管）。
// 参数 w: HTTP 响应；r: 请求。返回：无。
func (s *Server) handleSalesLexiconSeed(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if !auth.IsSuperAdmin(u) {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "平台默认词表仅超级管理员可补种"))
		return
	}
	if e := salesflow.SeedPlatformLexicon(s.Store); e != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), e)))
		return
	}
	s.Store.LogAudit(0, u.ID, "sales_config_save", "sales_lexicon", "seed")
	writeJSON(w, 200, map[string]interface{}{"success": true, "total": len(salesflow.PlatformLexiconSeed)})
}
