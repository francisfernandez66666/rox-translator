// ============ admin_models.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 模型配置 / 模型路由策略 / 策略参数（handleModels / handleModelRoutes / handlePolicy 系列）
// 安全要点：全部接口仅超管可访问（requireAdminUser）；模型与策略属平台级配置，
// 租户管理员无权读写。所有写操作均记录审计日志（LogAudit）。
// ========================================

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"translator/internal/config"
	apierrors "translator/internal/errors"
	"translator/internal/llmsource"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 密钥静态加密辅助（评审整改 D3） ============
//
// 库内 system_config.model_routes / stage_models 的 api_key 一律以 enc:v1: AES-GCM
// 密文落库（密钥派生自 JWT_SECRET）；内存/热同步链路使用明文；前端只见过掩码。

// loadRoutesDecrypted 读取 model_routes 并解密为明文副本。
func (s *Server) loadRoutesDecrypted() []config.ProviderConfig {
	rs := []config.ProviderConfig{}
	if s.Store == nil {
		return rs
	}
	if v, e := s.Store.GetConfig("model_routes"); e == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &rs)
		for i := range rs {
			rs[i].APIKey = store.DecryptSecret(rs[i].APIKey)
			if rs[i].APIKey == "" && strings.HasPrefix(v, "enc:") {
				log.Printf("[models] 路由 %s(%s) 密钥解密失败（疑 JWT_SECRET 轮换未同步），已跳过", rs[i].Provider, rs[i].Model)
			}
		}
	}
	return rs
}

// encryptRoutes 入库前加密副本的 api_key（不改原切片）。
func encryptRoutes(rs []config.ProviderConfig) []config.ProviderConfig {
	out := make([]config.ProviderConfig, len(rs))
	copy(out, rs)
	for i := range out {
		out[i].APIKey = store.EncryptSecret(out[i].APIKey)
	}
	return out
}

// llmKeyState 查询某个以密文落库的密钥配置（如 embed_api_key）的当前状态。
//   - 入参 key：system_config 中的配置键名（其值应为 store.EncryptSecret 产生的 enc:v1: 密文）。
//   - 返回 (是否已设置, 脱敏后的掩码)：未配置/解密失败均返回 (false, "")。
//
// 用途：在「全局模型」tab 的 GET 接口中向前端返回密钥是否已配置及掩码展示，
//
//	避免将真实密钥明文回传到前端。
func (s *Server) llmKeyState(key string) (bool, string) {
	// 从 system_config 读取密文（为空或读取失败视为未配置）
	v, err := s.Store.GetConfig(key)
	if err != nil || v == "" {
		return false, ""
	}
	// 解密（密钥派生自 JWT_SECRET；解密失败返回空串，同样视为未配置）
	dec := store.DecryptSecret(v)
	if dec == "" {
		return false, ""
	}
	// 解密成功：对明文做掩码（仅首尾若干字符可见）后返回
	return true, maskKey(dec)
}

// handleModels 读取模型配置（仅超管）：
// 读取全局配置（全局默认单模型 + system_config.model_routes 全局路由）。
//
// 返回 model 单模型 + routes 多供应商路由。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	// 读全局配置（★ 〇-AR 第 5 波「每台热加载」：三件套读的是**这台此刻生效的快照**，不是开机 cfg）
	// 快照读点统一走 liveLLM（本包唯一入口，见 llmsource_read.go 文件头）。
	// 为什么管理台也要惰性重探：这一页是运营"改完想立刻看一眼有没有生效"的那个面，
	// 只等下一次翻译请求才换快照的话，它会一直显示旧值＝"我配了但你说是空的"，
	// 于是运营反复保存、甚至去重启进程。探测本身有默认 5 秒节流，不会把这一页打成读库风暴。
	// 路由表本身仍读库内原文（loadRoutesDecrypted）：编辑面要显示"存着什么"，
	// 含被快照停用的坏路由——把停用路由从表里抹掉，等于让运营下一次整表保存时把它永久删掉。
	snap := s.liveLLM(r.Context())
	base := snap.OnlineAPIBase
	key := snap.OnlineAPIKey
	model := snap.OnlineModel
	routes := s.loadRoutesDecrypted()
	if routes == nil {
		routes = []config.ProviderConfig{}
	}
	maskedRoutes := make([]config.ProviderConfig, 0, len(routes))
	for _, rt := range routes {
		rt.APIKey = maskKey(rt.APIKey)
		maskedRoutes = append(maskedRoutes, rt)
	}
	// ★ LLM Key 合并：除原有的「在线模型」单模型与多供应商路由外，
	//   本接口额外返回 Embedding 密钥（KB 向量重建用）的状态，供「全局模型」tab 渲染。
	// 读取 embed_api_key 是否已配置及其掩码（库内为密文，这里解密后脱敏）。
	embSet, embMask := s.llmKeyState("embed_api_key")
	// 翻译密钥是否已真实配置：占位随机 Key（未配置环境变量时生成的 sk-xxxx）视为「未配置」，
	// 避免前端把占位 Key 误判为已生效，导致翻译实际失败却显示正常。
	// 判据取快照的 Placeholder（构造期来源标记，与 /api/health 的 llm_global_key 同一把尺子）。
	transSet := key != "" && !snap.Placeholder
	writeJSON(w, 200, map[string]interface{}{"success": true,
		// model：在线翻译/工单任务密钥（api_key 已掩码；set 表示是否真实配置）
		"model":     map[string]interface{}{"api_base": base, "api_key": maskKey(key), "model": model, "set": transSet},
		"embedding": map[string]interface{}{"set": embSet, "masked": embMask, "api_base": s.Cfg.EmbedAPIBase},
		"routes":    maskedRoutes})
}

// handleModelsSave 保存模型配置（仅超管）：
// 保存全局配置——单模型字段（api_base+model）作为主路由合并写入 model_routes，
// 并热更新运行配置；routes 全量覆盖全局路由。
//
// 支持多供应商路由（ChatGPT/Gemini 等 OpenAI 兼容端点）。
func (s *Server) handleModelsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		APIBase string                  `json:"api_base"` // 模型 API 基础地址（在线翻译用；可为空=不修改）
		APIKey  string                  `json:"api_key"`  // 在线翻译/工单任务 API Key（掩码值不覆盖原密钥）
		Model   string                  `json:"model"`    // 在线翻译模型名称
		Routes  []config.ProviderConfig `json:"routes"`   // 多供应商路由（可为空=清空；平台统一网关多供应商调度）
		// ★ LLM Key 合并（2026-08-27）：将原本独立的 /api/admin/llm-key 接口功能并入本接口
		EmbedAPIKey  string `json:"embed_api_key"`  // KB 向量重建用的 Embedding Key（掩码值不覆盖原密钥）
		EmbedAPIBase string `json:"embed_api_base"` // Embedding 网关地址（如智谱 …/api/paas/v4）
		// clear_keys：显式清空某个密钥作用域，取值 "translation"（在线翻译 Key）或 "embedding"（向量重建 Key）。
		// 前端「清除」按钮即发送该字段，避免把空串误当作「清空」而误删。
		ClearKeys []string `json:"clear_keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 保存全局配置
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 读取现有全局路由并解密（库内为 enc:v1: 密文或历史明文，回填需明文）
	oldRoutes := s.loadRoutesDecrypted()
	// 构建新路由列表：单模型字段优先作为主路由（api_base+model 非空），其余来自 routes
	merged := make([]config.ProviderConfig, 0, len(req.Routes)+1)
	if req.APIBase != "" && req.Model != "" {
		merged = append(merged, config.ProviderConfig{
			Provider: "global", APIBase: req.APIBase, APIKey: req.APIKey, Model: req.Model, Weight: 100,
		})
	}
	for _, rt := range req.Routes {
		// ★ B5（方案 B Phase 2 同批必修）：结构体透传，不再逐字段手构——
		//   旧写法会静默吞掉 ProviderConfig 的新能力位（SupportsConstraints 曾被丢过，
		//   SupportsCache 若同样处理将永远存不进库）。
		merged = append(merged, rt)
	}
	// 掩码密钥回填（★ 2026-08-26 修复脆弱匹配）：
	//   旧逻辑按「api_base+model 双字段相等」找旧路由——管理员只改 model 名即匹配失败，
	//   掩码串（如 sk-a****xyz）会被当真实 Key 入库，路由静默坏死。
	//   新规则：掩码 = 未修改 ⇒ 按位置对齐回填。合并列表结构与库内一致
	//   （[0]=单模型主路由(可省)，其后为 routes 全量），同一下标即同一条路由，
	//   前端整表回传时顺序天然保持。
	for i := range merged {
		if hasMask(merged[i].APIKey) && i < len(oldRoutes) {
			merged[i].APIKey = oldRoutes[i].APIKey // 回填明文旧值
		}
	}
	// ★ 入库前整体加密（评审整改 D3：库内不再存任何明文供应商 Key）
	//
	// ★★ 顺序是这一段的修法本体（〇-AR 第 5 波，㊻ 的另一半）：**先落库、落成了才碰进程侧**。
	// 旧写法把 `s.Cfg.ModelRoutes = merged` 排在 SetConfig 之前，于是写库失败当场 500 返回时，
	// 这一台的内存里已经是客户没提交成功的那份配置，库里还是旧的——
	// 同一租户在两台实例上会拿到两套上游（一台按新配置、其余按库跑），
	// 而且表现是"保存失败但有些请求已经变了"，比整批没生效更难归因。
	b, _ := json.Marshal(encryptRoutes(merged))
	if err := s.Store.SetConfig("model_routes", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// 清空作用域先处理：clear_keys 指定的密钥直接从库里删（先于写入，避免刚写又被清）
	for _, sc := range req.ClearKeys {
		if sc == "translation" {
			// 清除在线翻译 Key：库内三项（密钥/网关/模型）置空
			_ = s.Store.SetConfig("online_api_key", "")
			_ = s.Store.SetConfig("online_api_base", "")
			_ = s.Store.SetConfig("online_model", "")
		}
		if sc == "embedding" {
			// 清除 Embedding Key：库内密钥/网关置空
			_ = s.Store.SetConfig("embed_api_key", "")
			_ = s.Store.SetConfig("embed_api_base", "")
		}
	}
	// 在线翻译 Key 持久化：仅在非空且非掩码时写入（掩码串表示前端未改动、保留原值）。
	if req.APIKey != "" && !hasMask(req.APIKey) {
		_ = s.Store.SetConfig("online_api_key", store.EncryptSecret(req.APIKey))
	}
	if req.APIBase != "" {
		_ = s.Store.SetConfig("online_api_base", req.APIBase)
	}
	if req.Model != "" {
		_ = s.Store.SetConfig("online_model", req.Model)
	}
	// Embedding Key 持久化（KB 向量重建用）：同样仅在非空且非掩码时写入，密文落库。
	if req.EmbedAPIKey != "" && !hasMask(req.EmbedAPIKey) {
		_ = s.Store.SetConfig("embed_api_key", store.EncryptSecret(req.EmbedAPIKey))
	}
	if req.EmbedAPIBase != "" {
		_ = s.Store.SetConfig("embed_api_base", req.EmbedAPIBase)
	}
	// —— 以下全部是"库已经写成这样了"才做的本进程同步（★ 热同步用明文）——
	// 若单模型字段非空，同时更新全局默认单模型（引擎回退链的最终兜底）
	if req.APIBase != "" {
		s.Cfg.OnlineAPIBase = req.APIBase
	}
	if req.APIKey != "" && !hasMask(req.APIKey) {
		s.Cfg.OnlineAPIKey = req.APIKey
		s.Cfg.OnlineAPIKeyIsPlaceholder = false
	}
	if req.Model != "" {
		s.Cfg.OnlineModel = req.Model
	}
	// 清除作用域的进程侧腿：运行配置恢复占位/清空。
	// ⚠️ 这一条不许跟着"库读回来"那条统一路径走：环境变量那档优先于库，
	//    本进程 cfg 里此刻装的还是启动期那把 Key，光删库里的行它不会自己松手，
	//    "清除"按钮就会变成点了没反应（表现和缺陷本体一样是"配置与实际不符"）。
	for _, sc := range req.ClearKeys {
		if sc == "translation" {
			s.Cfg.OnlineAPIKey = ""
			s.Cfg.OnlineAPIKeyIsPlaceholder = true
		}
		if sc == "embedding" {
			s.Cfg.EmbedAPIKey = ""
		}
	}
	// Embedding 一族的进程侧现值（不在 llmsource 快照射程内，仍按字段同步）
	if req.EmbedAPIKey != "" && !hasMask(req.EmbedAPIKey) {
		s.Cfg.EmbedAPIKey = req.EmbedAPIKey
	}
	if req.EmbedAPIBase != "" {
		s.Cfg.EmbedAPIBase = req.EmbedAPIBase
	}
	// ★ 把本进程的"当前生效"整体换成刚写库这一份（〇-AR 第 5 波「每台热加载」的写入侧）。
	// 为什么还要 Publish：热加载腿是**惰性**的（引擎取配置时才按 TTL 探库），
	// 若这里不发，改完配置的这一台要等到下一次翻译请求（且超过 TTL）才换上新配置，
	// 而管理台紧接着的 GET 就已经在读快照了——"我保存了但它显示旧的"。
	// PublishFrom 同时钉住"这份是从这座库读的、什么时候读的"，TTL 节流因此不会白探一次库。
	// 其余实例不必重启：它们各自的 TTL 探测探到指纹变化即换上新快照。
	// ApplyTo 保留的是既有口径：路由与三件套仍回写运行配置——启动期一次性判定
	// （H2 能力位探测）与 Embedding 一族还在问 cfg，B5 那条能力位热同步断言也钉在这上面。
	if s.Cfg != nil {
		snap := llmsource.Resolve(s.Cfg, s.Store)
		snap.ApplyTo(s.Cfg)
		llmsource.PublishFrom(s.Store, snap)
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_save", "system", fmt.Sprintf("%d 条全局路由", len(merged)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleModelRoutes 读取模型路由策略（super_admin）。★ 输出掩码（评审整改 D3）
func (s *Server) handleModelRoutes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	routes := s.loadRoutesDecrypted()
	masked := make([]config.ProviderConfig, len(routes))
	for i, rt := range routes {
		rt.APIKey = maskKey(rt.APIKey)
		masked[i] = rt
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "routes": masked})
}

// handleModelRoutesSave 保存模型路由策略（仅超管）
// 覆盖式保存：全量提交，空数组表示清空路由回退单供应商 Online* 配置。
func (s *Server) handleModelRoutesSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Routes []config.ProviderConfig `json:"routes"` // 模型路由全量配置（空数组=清空路由回退单供应商）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 校验：非空时必须每条含 api_base/model/api_key
	for i, rt := range req.Routes {
		if rt.APIBase == "" || rt.Model == "" {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": fmt.Sprintf("第 %d 条路由缺少 api_base/model", i+1)})
			return
		}
		if rt.Provider == "" {
			req.Routes[i].Provider = "global"
		}
	}
	// 掩码密钥不覆盖：保留原值（旧库读出后先解密再回填）
	if len(req.Routes) > 0 {
		old := s.loadRoutesDecrypted()
		for i := range req.Routes {
			if hasMask(req.Routes[i].APIKey) {
				for _, o := range old {
					if o.APIBase == req.Routes[i].APIBase && o.Model == req.Routes[i].Model {
						req.Routes[i].APIKey = o.APIKey
						break
					}
				}
			}
		}
	}
	// ★ 入库加密（评审整改 D3）；★ 〇-AR 第 5 波：先落库，落成才同步本进程（与 handleModelsSave 同口径）
	b, _ := json.Marshal(encryptRoutes(req.Routes))
	if err := s.Store.SetConfig("model_routes", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	// 把"当前生效"整份换成刚写库这一份：其余实例靠各自的 TTL 探测追上来，这一台立刻到位。
	// ApplyTo 同 handleModelsSave：路由与三件套要回写 cfg（启动期判定与既有 B5 断言都读那里），
	// 但必须在 SetConfig 成功之后——旧写法先改 cfg 再写库，写库失败就留下"这台新、库旧"的分叉。
	if s.Cfg != nil {
		snap := llmsource.Resolve(s.Cfg, s.Store)
		snap.ApplyTo(s.Cfg)
		llmsource.PublishFrom(s.Store, snap)
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "model_routes_save", "system", fmt.Sprintf("%d 条", len(req.Routes)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 各流程阶段模型配置（仅超管） ============

// handleStageModels 读取各流程阶段模型配置（仅超管）。
// 返回的档位**一律从 config.AllStages() 派生**（★ R-1 修法 D，2026-10-04）：
// 旧写法在这里手抄五项、漏了 kb_match，而引擎确实拿 kb_match 取模
// （orchestrator/workflow.go 与 engine/file.go、engine/text.go），于是运营既看不见也配不了；
// 又因为保存面是覆盖式提交，"看不见"直接等于"每次保存都把它删掉"。
// 现在读面缺项也补一个空档返回，前端才能把它渲染出来。
func (s *Server) handleStageModels(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	stages := config.StageModels{}
	if s.Store != nil {
		if v, err := s.Store.GetConfig("stage_models"); err == nil && v != "" {
			_ = json.Unmarshal([]byte(v), &stages)
			// ★ 库内密文 → 明文后再掩码输出（评审整改 D3）
			for k := range stages {
				sm := stages[k]
				sm.APIKey = store.DecryptSecret(sm.APIKey)
				stages[k] = sm
			}
		}
	}
	// 掩码所有 API Key 再返回（密钥仅保存后返回一次）；输出的档位＝config.AllStages() 派生
	out := config.StageModels{}
	for _, k := range config.AllStages() {
		sm := stages[k]
		out[k] = config.StageModel{
			Provider: sm.Provider,
			APIBase:  sm.APIBase,
			APIKey:   maskKey(sm.APIKey),
			Model:    sm.Model,
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "stages": out})
}

// handleStageModelsSave 保存各流程阶段配置（仅超管）。
// 语义（★ R-1 修法 D，2026-10-04 从「整表替换」改为「按提交键合并」）：
//   - 本次提交里出现过的键：有 api_base+model 即覆盖，两者皆空即清空该档（删除键，回落全局/路由）；
//   - 本次没出现的键：保留库里旧值，不再被顺手抹掉（旧形态下客户端少渲染一张卡＝每次保存都删一档真实配置）；
//   - 名单（config.AllStages()）外的键：直接 400 拒收，不再原样落库成永不生效的死配置。
//
// 密钥面：库内密文存储；回显是掩码（sk-****），提交值仍为掩码时按该档旧真值回填，绝不把掩码写回库。
func (s *Server) handleStageModelsSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Stages config.StageModels `json:"stages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Store == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "平台存储未初始化"})
		return
	}
	// 读取旧配置并解密，掩码密钥保留原值
	old := config.StageModels{}
	if v, err := s.Store.GetConfig("stage_models"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &old)
		// 遍历旧配置，解密各阶段密钥以便后续对比
		for k := range old {
			sm := old[k]
			sm.APIKey = store.DecryptSecret(sm.APIKey)
			old[k] = sm
		}
	}
	// 遍历请求中各阶段配置，做校验与缺省值补全
	// ★ 合法阶段名单只有一份：config.AllStages()（修法 D 的派生源，禁止在此再抄一份字面量）
	allowed := map[string]bool{}
	for _, k := range config.AllStages() {
		allowed[k] = true
	}
	// submitted 必须在下面那个循环**之前**采集：循环里的 delete 会把「这一档本次被清空」
	// 这一事实抹掉，事后按 req.Stages 的键集合判定就会把被清空的档误判成"没提交"而保留旧值。
	submitted := map[string]bool{}
	for k := range req.Stages {
		submitted[k] = true
	}
	for k := range req.Stages {
		sm := req.Stages[k]
		// 名单外的键一律拒：旧写法把任意 JSON 键原样落库，
		// 于是"阶段名拼错"会留下一条永不生效的配置，运营看面板以为配上了（静默坏死）。
		if !allowed[k] {
			// ★ AGENTS §一·8：新增错误响应走统一出口（结构化 code，前端/SDK 才能按 code 分支）
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, fmt.Sprintf("未知流程阶段 %s", k)))
			return
		}
		if sm.APIBase == "" && sm.Model == "" {
			// 清空该阶段 → 删除键
			delete(req.Stages, k)
			continue
		}
		if sm.APIBase == "" || sm.Model == "" {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, fmt.Sprintf("阶段 %s 缺少 api_base 或 model", k)))
			return
		}
		if sm.Provider == "" {
			req.Stages[k] = config.StageModel{Provider: "stage_" + k, APIBase: sm.APIBase, APIKey: sm.APIKey, Model: sm.Model}
			sm = req.Stages[k]
		}
		if hasMask(sm.APIKey) {
			if o, ok := old[k]; ok {
				req.Stages[k] = config.StageModel{Provider: sm.Provider, APIBase: sm.APIBase, APIKey: o.APIKey, Model: sm.Model}
			} else {
				req.Stages[k] = config.StageModel{Provider: sm.Provider, APIBase: sm.APIBase, APIKey: "", Model: sm.Model}
			}
		}
	}
	// ★ 覆盖式的边界（修法 D 配套，2026-10-04）：只有**本次提交里出现过的键**才参与覆盖/删除，
	//   没出现的键保留旧值。旧写法是「整表替换」，客户端只要少渲染一张卡（历史上的 kb_match，
	//   以及旧键 evals），保存一次就把那一档的真实配置从库里抹掉，且面板上看不出发生过什么。
	//   清空某档的正路仍是显式提交该档的空值（上面那条 delete 分支），语义没有变松。
	//   ⚠️ submitted 必须在上面那个循环之前采集：循环里的 delete 会把"提交过"这一事实抹掉，
	//      事后按 req.Stages 的键集合判定会漏判所有被清空的档（把它们当成"没提交"而保留旧值）。
	stored := config.StageModels{}
	for k, sm := range old {
		if !submitted[k] {
			sm.APIKey = store.EncryptSecret(sm.APIKey) // 旧值已是解密态，回写要重新加密
			stored[k] = sm
		}
	}
	for k, sm := range req.Stages {
		sm.APIKey = store.EncryptSecret(sm.APIKey)
		stored[k] = sm
	}
	b, _ := json.Marshal(stored)
	if err := s.Store.SetConfig("stage_models", string(b)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "stage_models_save", "system", fmt.Sprintf("%d 阶段", len(stored)))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// ============ 策略参数（仅超管） ============

// handlePolicy 读取翻译策略参数（仅超管；经 X-Tenant-ID 切换生效租户，未配置回退全局默认）
func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	pc := tenant.PolicyConfig{}
	if s.Ten != nil {
		pc, _ = s.Ten.GetPolicyConfig(s.effTenant(r, u))
	}
	high := pc.HighSim
	med := pc.MedSim
	evals := pc.EvalsPassThreshold
	if high <= 0 {
		high = s.Cfg.HighSim
	}
	if med <= 0 {
		med = s.Cfg.MedSim
	}
	if evals <= 0 {
		evals = 75
	}
	// ★ 跨部门降级检索开关（2026-08-26 KB继承链）：nil=默认开；输出解析后的布尔供前端渲染
	cross := true
	if pc.CrossDeptFallback != nil {
		cross = *pc.CrossDeptFallback == 1
	}
	// ★ 数据回流开关（评审整改 D7）：默认参与共建
	feedbackOut := pc.DataFeedbackOptOut != nil && *pc.DataFeedbackOptOut == 1
	writeJSON(w, 200, map[string]interface{}{"success": true, "policy": map[string]interface{}{
		"high_sim":              high,
		"med_sim":               med,
		"evals_pass_threshold":  evals,
		"cross_dept_fallback":   cross,
		"data_feedback_opt_out": feedbackOut,
	}})
}

// handlePolicySave 保存翻译策略参数（仅超管）
func (s *Server) handlePolicySave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	var req struct {
		Policy             map[string]float64 `json:"policy"`
		CrossDeptFallback  *bool              `json:"cross_dept_fallback"`   // 跨部门降级检索（nil=不修改）
		DataFeedbackOptOut *bool              `json:"data_feedback_opt_out"` // ★ 数据回流关闭开关（D7；nil=不修改）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Ten == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	// 以当前策略为底做增量合并：仅覆盖显式传入且 >0 的数值字段，未传字段保持原值
	cur, _ := s.Ten.GetPolicyConfig(s.effTenant(r, u))
	pc := cur
	if v, ok := req.Policy["high_sim"]; ok && v > 0 {
		pc.HighSim = v
	}
	if v, ok := req.Policy["med_sim"]; ok && v > 0 {
		pc.MedSim = v
	}
	if v, ok := req.Policy["evals_pass_threshold"]; ok && v > 0 {
		pc.EvalsPassThreshold = v
	}
	// ★ 跨部门开关：显式传入才修改（bool→*int 三态存储）
	if req.CrossDeptFallback != nil {
		v := 0
		if *req.CrossDeptFallback {
			v = 1
		}
		pc.CrossDeptFallback = &v
	}
	if req.DataFeedbackOptOut != nil {
		v := 0
		if *req.DataFeedbackOptOut {
			v = 1
		}
		pc.DataFeedbackOptOut = &v
	}
	if err := s.Ten.SetPolicyConfig(s.effTenant(r, u), pc); err != nil {
		// F-64②：策略是「读现值→增量合并→整份写回」，写回失败＝租户配置存储故障（500）；
		// 旧写法回 200 会让管理台提示「已保存」而实际没落库（下次进面板看到旧值，用户反复重试）。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	// ★ F-63（2026-09-26 批 I-3）：detail 原为空串。策略是「增量合并」语义（未提交的键保持原值），
	//   所以轨迹只记本次实际提交的键值对；map 迭代无序，先排序保证同一提交两次渲染文本可比对。
	changed := make([]string, 0, len(req.Policy)+2)
	for k, v := range req.Policy {
		if v > 0 {
			changed = append(changed, fmt.Sprintf("%s=%g", k, v))
		}
	}
	if req.CrossDeptFallback != nil {
		changed = append(changed, fmt.Sprintf("cross_dept_fallback=%v", *req.CrossDeptFallback))
	}
	if req.DataFeedbackOptOut != nil {
		changed = append(changed, fmt.Sprintf("data_feedback_opt_out=%v", *req.DataFeedbackOptOut))
	}
	sort.Strings(changed)
	policyDetail := "本次提交 0 项（仅触达保存，无键变更）"
	if len(changed) > 0 {
		policyDetail = fmt.Sprintf("策略变更 %d 项｜%s", len(changed), strings.Join(changed, "｜"))
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "policy_save", "tenants", policyDetail)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
