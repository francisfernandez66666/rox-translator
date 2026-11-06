// ============ product_public_api.go · 职责说明 ============
// 一期《确定的多语言能力》§5.6/§5.7/§5.8 免登录面：两段短码对外页、知识层问答、
// 动态 sitemap、产品上下文留资、耗尽三态。
//
// ★ 五条硬口径：
//
//	① 免登录面**绝不返 403**（F-79 同形教训）：跨租户拼接 / 短码缺失 / 格式不合
//	   一律空态（200 + success:true + 空集），不出他租户内容、不挂 A 的品牌名。
//	② 公开检索语料只有一个出口 store.ListPublicCorpus（SQL 硬带 visibility='public'）；
//	   受控字段的值字面量永不进首屏 HTML / meta / JSON-LD。
//	③ 知识层问答零 LLM：只读已备语料 → engine.GuardKnowledgeAnswer 判来源 → 出栈，
//	   全程不碰 llm.Client（§7.3 N1 的唯一证据）。
//	④ sitemap 只收录 seo_indexable=1 且 status='live'（索引面与可见性面两把尺子分开）。
//	⑤ 留资 `lead-product:` 前缀与 `lead:` 两本账不互扣；落库归属只取产品行的 tenant_id，
//	   绝不采信 `?tenant_id=`（V6）。
//
// 错误一律 s.writeError + apierrors.New；空集出 []。
// ================================================
package api

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/httpx"
	"translator/internal/store"
)

// 知识层问答的耗尽三态（对外契约；三态必须不同形）。
const (
	knowledgeAvailable      = "available"
	knowledgeExhaustedToday = "exhausted_today"
	knowledgeDisabled       = "disabled"
)

// productPublicRoutePrefix 对外页两段短码路由前缀（前端 SPA 消费）。
const productPublicRoutePrefix = "/p/"

// leadProductGuardPrefix 产品留资配额前缀：与 lead: / lead-public: 各记一本账。
const leadProductGuardPrefix = "lead-product:"

// routesProductPublic 注册免登录面（挂 routes() 一行）。
func (s *Server) routesProductPublic() {
	s.mux.HandleFunc("/api/product/public", s.handleProductPublic)
	s.mux.HandleFunc("/api/product/answer", s.handleProductAnswer)
	s.mux.HandleFunc("/api/product/lead", s.handleProductLead)
	s.mux.HandleFunc("/sitemap.xml", s.handleProductSitemap)
}

// productCodePair 从请求取两段短码；缺一/格式不合 ⇒ 空态（不 500、不 403）。
// 参数：r=请求；返回租户段、页面段。
func productCodePair(r *http.Request) (string, string) {
	tc := strings.TrimSpace(r.URL.Query().Get("tenant_code"))
	pc := strings.TrimSpace(r.URL.Query().Get("page_code"))
	if tc == "" || pc == "" {
		// 也接受 /p/{tenantCode}/{pageCode} 形态（前端可能把 path 透传过来）
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		seg := strings.Split(strings.Trim(path, "/"), "/")
		if len(seg) == 3 && seg[0] == "p" {
			tc, pc = seg[1], seg[2]
		}
	}
	return tc, pc
}

// productResolved 两段短码解析（query／path 口径）+ 租户一致性校验（Q2 实现约束）。
// 参数：r=请求；返回产品与是否命中。
func (s *Server) productResolved(r *http.Request) (store.Product, bool) {
	tc, pc := productCodePair(r)
	return s.productResolvedCodes(r, tc, pc)
}

// productResolvedCodes 显式两段码 + 按访问域名的租户一致性校验（Q2 实现约束）。
// ★ 不一致一律 not-found（不返 403、不出 B 的内容、不挂 A 的品牌）。
// 参数：r=请求，tc=租户段，pc=页面段；返回产品与是否命中。
func (s *Server) productResolvedCodes(r *http.Request, tc, pc string) (store.Product, bool) {
	p, ok, err := s.Store.GetProductByCodes(tc, pc)
	if err != nil || !ok {
		return store.Product{}, false
	}
	// ★ 同租户校验只认**按访问域名解析出的**租户（`tenantByHost`），不认 `tenant.FromContext`：
	//   后者在匿名请求上被中间件钉成 1（server.go 第 4 支：历史 rox 默认档），它答的是
	//   "这条请求按哪个租户记账"，不是"这台域名叫谁"。拿它当域名用 ⇒ 2 号及以后租户的
	//   对外页与知识问答**恒 not-found**（T89／T92 那一片红的真身；公开面不该被记账口径挡死）。
	//   平台根（tid=0，含主站／回环）按设计可展示任意租户的公开页。
	if hostTid := s.tenantByHost(r); hostTid > 0 && hostTid != p.TenantID {
		return store.Product{}, false
	}
	return p, true
}

// handleProductPublic GET /api/product/public：对外页首屏数据。
// ★ 受控字段整段缺席（不出替身值字面量），只出可公开的产品信息与公开语料。
func (s *Server) handleProductPublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	p, ok := s.productResolved(r)
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"success": true, "found": false, "not_found": true, "facts": []interface{}{}})
		return
	}
	corpus, err := s.Store.ListPublicCorpus(p.TenantID, p.ID)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": true, "found": false, "not_found": true, "facts": []interface{}{}})
		return
	}
	// ★ 只出**条数**不出值：对外页那句"需登录或留言获取"的替身文案要有事实依据，
	//   否则前端只能按"列表短了几行"猜（而它根本看不到受控行）。计数不是值字面量，V4 不受影响。
	gated, _ := s.Store.CountGatedFacts(p.TenantID, p.ID)
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "found": true, "not_found": false,
		"product": map[string]interface{}{
			"name": p.Name, "tagline": p.Tagline, "industry_code": p.IndustryCode,
			"status": p.Status, "tenant_code": p.TenantCode, "page_code": p.PageCode,
			// ★ O 组：目标语言只存产品行 ⇒ 对外页要显示"这一页备了哪几语"，只能从这里出，
			//   前端不许再按界面语种猜（空数组＝卖家还没声明，界面就该显示"未设定"而不是默认一屏语种）。
			"target_langs": p.TargetLangList(),
		},
		"facts":       corpus,
		"gated_count": gated,
		"url":         productPublicRoutePrefix + p.TenantCode + "/" + p.PageCode,
	})
}

// knowledgeQuotaState 判耗尽三态：只走现网那一条 RateDayCeilReached 尺子，不新造日界。
// 参数：r=请求，tid=产品所属租户；返回状态词与恢复时刻（仅 exhausted 时非空）。
func (s *Server) knowledgeQuotaState(r *http.Request, tid int64) (string, string) {
	cfg := s.instantEffective(tid)
	if cfg.FreeIPDaily < 0 || cfg.FreeDailyPoints < 0 {
		return knowledgeDisabled, ""
	}
	used := s.dayUsed(store.RateScopeFreeIP, httpx.ClientIP(r))
	if store.RateDayCeilReached(store.DayState{Used: used}, cfg.FreeIPDaily) {
		// 现网日墙口径：created_at 为 UTC TEXT，日界取 UTC 00:00（不新造第三种日历）。
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		return knowledgeExhaustedToday, next.Format(time.RFC3339)
	}
	return knowledgeAvailable, ""
}

// productAnswerReq 知识层问答入参（两段短码 + 问题 + 可选语种）。
type productAnswerReq struct {
	TenantCode string `json:"tenant_code"`
	PageCode   string `json:"page_code"`
	Question   string `json:"question"`
	Lang       string `json:"lang"`
}

// productAnswerInput 读取问答入参：★ JSON 体优先，体里没有（或压根没带体）才回落 query／path。
//
// 坏体**不判 400**：这是免登录可达的公开面（F-79 同族纪律——老缓存包、零 JS 表单、
// 只带 query 的书签都可能没有体，把"问题没带上"打成错误页的代价是访客看到一句技术错误，
// 而产品页本来还答得出来）。解不开就按空入参走，语义与改动前完全相同。
// 参数：r=请求；返回入参（已 TrimSpace）。
func productAnswerInput(r *http.Request) productAnswerReq {
	var req productAnswerReq
	if r.Body != nil {
		if raw, err := io.ReadAll(io.LimitReader(r.Body, 16<<10)); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
			_ = json.Unmarshal(raw, &req)
		}
	}
	req.TenantCode = strings.TrimSpace(req.TenantCode)
	req.PageCode = strings.TrimSpace(req.PageCode)
	req.Question = strings.TrimSpace(req.Question)
	req.Lang = strings.TrimSpace(req.Lang)
	return req
}

// knowledgeQuestionTokens 从问题里取可匹配的候选词。
//
// 两条形态：① 按标点/空白切出的整段（≥2 字符）——拉丁与数字问题就靠它；
// ② CJK 段再补**二字窗口**——中文没有空格，"交期多久"整段永远匹配不上"交期 7 天"，
//
//	不切二字就等于这个问题字段白读（本函数存在的唯一理由）。
//
// 参数：q=访客这句问题；返回候选词（去重、保序）。
func knowledgeQuestionTokens(q string) []string {
	if q == "" {
		return []string{}
	}
	out := []string{}
	seen := map[string]bool{}
	push := func(s string) {
		if len([]rune(s)) >= 2 && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, seg := range strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if seg == "" {
			continue
		}
		push(seg)
		r := []rune(seg)
		if !isASCIILetters(seg) && len(r) > 2 {
			for i := 0; i+2 <= len(r); i++ {
				push(string(r[i : i+2]))
			}
		}
	}
	return out
}

// isASCIILetters 整段是否纯 ASCII 字母数字（是 ⇒ 不再切二字窗口，避免把 "7" 切成一堆碎片）。
// 参数：s=一段文字。
func isASCIILetters(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// preferKnowledgeLines 命中问题候选词的行优先；**一行都没命中就回全量**。
// 方向是刻意单边：宁可多答几条无关行，也不许因为"没匹配上"给客户一个空答案
// （空答案在 §19 那一族里是「没翻伪装成翻了」的同形——知识层同理）。
// 参数：lines=候选行，tokens=问题候选词；返回优先后的行清单（保序）。
func preferKnowledgeLines(lines []string, tokens []string) []string {
	if len(tokens) == 0 || len(lines) == 0 {
		return lines
	}
	hit := []string{}
	for _, l := range lines {
		for _, tk := range tokens {
			if strings.Contains(l, tk) {
				hit = append(hit, l)
				break
			}
		}
	}
	if len(hit) == 0 {
		return lines
	}
	return hit
}

// handleProductAnswer POST /api/product/answer：知识层问答（零 LLM、免登录）。
//
// ★ 零 token 事实：本函数只调 store.ListPublicCorpus / ListAssetsForAnswer /
//
//	engine.GuardKnowledgeAnswer，全链不碰 llm.Client、不写 usage_ledger。
//
// ★ 缺格时答案必须点名缺项（N2），绝不悄悄把请求转交模型。
func (s *Server) handleProductAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	in := productAnswerInput(r)
	p, ok := s.productResolved(r)
	if !ok {
		p, ok = s.productResolvedCodes(r, in.TenantCode, in.PageCode)
	}
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"success": true, "found": false, "not_found": true, "answer": "", "missing": []string{}})
		return
	}
	state, resetsAt := s.knowledgeQuotaState(r, p.TenantID)
	lang := in.Lang
	if lang == "" {
		lang = strings.TrimSpace(r.URL.Query().Get("lang"))
	}
	tokens := knowledgeQuestionTokens(in.Question)

	// ① 公开语料：唯一出口，SQL 层已挡掉受控行。
	corpus, err := s.Store.ListPublicCorpus(p.TenantID, p.ID)
	if err != nil {
		corpus = []store.PublicCorpusItem{}
	}
	// ② 已备物料原文（零模型路径）：只取 valid + approved 行。
	assets, err := s.Store.ListAssetsForAnswer(p.TenantID, p.ID, lang)
	if err != nil {
		assets = []store.ProductAsset{}
	}
	// ③ 缺项点名：六格里没有可用物料的格子逐一点名，不许静默出空答案。
	cells, _ := s.Store.ReadinessFor(p.TenantID, p.ID)
	missing := []string{}
	for _, c := range cells {
		if c.Worst != store.ReadinessValid && (lang == "" || c.Lang == lang) {
			missing = append(missing, c.AssetType+"/"+c.Lang)
		}
	}
	// ④ 拼答案文本（已备物料原文 + 公开事实），再过唯一出站判据。
	//   ★ `facts` 是**溯源宇宙**（守卫拿它判"这个数字库里有吗"），一律全量、不随问题筛；
	//   只有出给客户的那几行（`parts`）按问题候选词优先——筛错宇宙会把合法报价句判成编造。
	parts := []string{}
	facts := []string{}
	corpusLines := []string{}
	assetLines := []string{}
	for _, it := range corpus {
		if strings.TrimSpace(it.Text) == "" {
			continue
		}
		corpusLines = append(corpusLines, it.Text)
		if it.Source == "fact" {
			facts = append(facts, it.Text)
		}
	}
	for _, a := range assets {
		if strings.TrimSpace(a.Body) != "" {
			assetLines = append(assetLines, a.Body)
		}
	}
	assetLines = preferKnowledgeLines(assetLines, tokens)
	corpusLines = preferKnowledgeLines(corpusLines, tokens)
	parts = append(append(parts, assetLines...), corpusLines...)
	fallback := ""
	if len(assetLines) > 0 {
		fallback = assetLines[0]
	}
	if fallback == "" {
		fallback = strings.Join(parts, "。")
	}
	answer, reason := engine.GuardKnowledgeAnswer(strings.Join(parts, "。"), facts, fallback)
	if answer == "" && len(missing) > 0 {
		answer = "以下资料尚未备齐：" + strings.Join(uniqueStrings(missing), "、")
	}
	s.Store.LogAudit(p.TenantID, 0, "knowledge_answer_served", "products", p.ID+":"+state)
	out := map[string]interface{}{
		"success": true, "found": true,
		"answer": answer, "source": "knowledge_base", "reason": reason,
		"missing": missing, "facts": corpus,
		"quota_state": state,
		// 出 0 才是"不收费"这句承诺；缺席会被读成"字段没实现"（§6 第 4 条同判）
		"points_knowledge_query": 0,
	}
	// ★ `resets_at` **只在真耗尽那一支出栈**：available／disabled 带一个空串等于让
	//   "三态读数各不同形"退化成"只有状态词不同、其余同形"，前端按 key 在不在做判据的那一档
	//   （X5：resets_at 缺失⇒落「暂不可用」而不是「销售忙」）也会被空串骗过。
	if resetsAt != "" {
		out["resets_at"] = resetsAt
	}
	writeJSON(w, 200, out)
}

// uniqueStrings 去重保序（缺项点名不许把同一格重复报 N 次）。
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// handleProductLead POST /api/product/lead：匿名留资（产品上下文存四列）。
// ★ 复用现成免验证通道的四道自保纪律，但配额前缀独立成账（不互扣）。
func (s *Server) handleProductLead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	var req struct {
		TenantCode string `json:"tenant_code"`
		PageCode   string `json:"page_code"`
		Message    string `json:"message"`
		Email      string `json:"email"`
		Company    string `json:"company"`
		Langs      string `json:"langs"`
		Site       string `json:"site"`      // 蜜罐
		TenantID   int64  `json:"tenant_id"` // ★ 故意读但不采信（V6）
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	_ = req.TenantID
	// 蜜罐命中：假成功、不落库（不给 bot 反馈信号）
	if strings.TrimSpace(req.Site) != "" {
		writeJSON(w, 200, map[string]interface{}{"success": true})
		return
	}
	ctx, err := s.Store.ResolveProductLeadContext(req.TenantCode, req.PageCode, "product_page")
	if err != nil {
		// 空态而非 403/404：匿名面不许把「短码不存在」打成错误页
		writeJSON(w, 200, map[string]interface{}{"success": true, "found": false})
		return
	}
	ip := httpx.ClientIP(r)
	if ok, wait := s.regGuard.allow(leadProductGuardPrefix+ip, leadGuardDailyPublic, leadGuardIntervalPublic); !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "提交过于频繁，请 "+strconv.Itoa(wait)+" 秒后再试"))
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Message) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请填写联系邮箱与留言内容"))
		return
	}
	content := "【产品页线索】" + strings.TrimSpace(req.Company) + "｜" +
		strings.TrimSpace(req.Email) + "｜" + strings.TrimSpace(req.Message)
	if _, err := s.Store.CreateProductLead(ctx, content, req.Langs); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "提交失败，请稍后再试"))
		return
	}
	s.regGuard.record(leadProductGuardPrefix + ip)
	writeJSON(w, 200, map[string]interface{}{"success": true, "found": true})
}

// sitemapURLSet sitemap.xml 的一行 URL（只出 URL，零受控字段）。
type sitemapURLSet struct {
	XMLName xml.Name          `xml:"urlset"`
	Xmlns   string            `xml:"xmlns,attr"`
	URLs    []sitemapURLEntry `xml:"url"`
}

// sitemapURLEntry sitemap 中的一个 loc。
type sitemapURLEntry struct {
	Loc string `xml:"loc"`
}

// handleProductSitemap GET /sitemap.xml：动态产品页 sitemap。
// ★ 只收录 seo_indexable=1 且 status='live'（store.ListSitemapProducts 已硬带条件）；
//
//	关掉的 URL 与 canonical 一起消失（S1），app 路由仍 noindex（前端 route_meta 口径不变）。
func (s *Server) handleProductSitemap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	rows, err := s.Store.ListSitemapProducts()
	if err != nil {
		rows = []store.SitemapProduct{}
	}
	set := sitemapURLSet{Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: []sitemapURLEntry{}}
	base := "https://" + strings.TrimSpace(r.Host)
	for _, row := range rows {
		set.URLs = append(set.URLs, sitemapURLEntry{
			Loc: base + productPublicRoutePrefix + row.TenantCode + "/" + row.PageCode,
		})
	}
	body, err := xml.Marshal(set)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "sitemap 生成失败"))
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}
