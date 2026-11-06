// ============ product_onboard_draft.go · 职责说明 ============
// 一期引导页「自由描述 → 候选字段」的**登录态识别口**（★ 2026-11-07）：
//
//	POST /api/product/onboard/draft  {"text":"…","ui_lang":"…"}
//	⇒ {"success":true,"draft":{"name":"","tagline":"","use_case":"","target_langs":[]}}
//
// ★ 三条硬口径，每条都对应仓里一条既有纪律（改这一腿前必读）：
//
//	① **只读不写**：这一口出的是"候选"，卖家在界面上确认之后才走 /api/product/save 与
//	   /api/product/fact/upsert 落库。本文件对 products / product_facts / product_assets
//	   **零写点**，也**不写审计动作**——新审计动作名会顶红 `audit.action.*` 的 12 语种覆盖棘轮
//	   （AGENTS §一·5），而"一份还没被确认的草稿"本来就不是一条账务/留痕事件（§一·8 的镜像：
//	   没发生的事不许留痕，留了痕就会被当成发生过）。零写这条由单测按行数反验，不靠注释。
//	② **零成本**：判定腿的 token 全平台承担（见 engine/onboard_draft.go ②），预估侧走
//	   engine.OnboardDraftAllowanceTokens 恒 0，/api/pricing/meta 明出 points_onboard_draft: 0
//	   （缺席会被读成"字段没实现"，AGENTS §28）。⇒ 卖家的余额/日额在这条链上一分不动。
//	③ **模型抖动不是卖家的错误**：没配阶段模型／超时／回执截断／JSON 解不出 ⇒ 一律
//	   200 ＋ 空草稿 ＋ 一行 WARN（reason 只进日志，**绝不进报文**），因为向导手打那条路
//	   必须一直开着；只有**卖家自己的载荷有问题**才回 4xx，且一律走 s.writeError（错误风格棘轮
//	   基线 0，一处内联 writeJSON(4xx) 就顶红整包）。
//
// ★ 语种清单只有一份：过滤依据是 store.ProductLangs（与 api/seo_meta.json 的 languages、
//
//	前端 src/lib/onboardingSchema.ts 的 LANG_OPTIONS 同源；跨侧那条腿见本包源码级用例）。
//	本文件**不许**出现第二份"支持哪些语种"的判断——下面那个 switch 只做**形状**归一
//	（简/繁中文的常见写法），认不认得某个码永远问 store.ProductLangs。
//
// ===========================================================
package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/observability"
	"translator/internal/store"
)

// 候选字段的出栈上限（字符口径，与建档表单的输入框同档；超出按**截断**处理）。
// 为什么截断而不是拒：这是候选不是成品——界面里那三个格子卖家还能改，
// 而"模型多说了一句"绝不该把整次识别打成 4xx（那是把上游的话痨算在卖家头上）。
const (
	productOnboardDraftMaxNameRunes    = 120
	productOnboardDraftMaxTaglineRunes = 200
	productOnboardDraftMaxUseCaseRunes = 1000
)

// productOnboardDraftTextMaxChars 自由描述的入参上限（**超出即拒，不静默截断**）。
// 与上面三条相反的方向：这一段是**卖家自己交来的载荷**，截掉后半段＝让他以为"系统看漏了"，
// 所以宁可给一句可读的 400 让他自己拆开来贴。
const productOnboardDraftTextMaxChars = 6000

// ★ 防刷那一格（★ 2026-11-07 收尾轮补的腿，AGENTS §一·10 同族）。
//
//	为什么这个口**必须**有格子：它的模型成本由平台承担（llm.PlatformOnboardDraft ⇒ 卖家余额一分不动），
//	于是"注册一批账号轮流打这一个口"对卖家免费、对平台是真实支出。§一·10 那条防薅的成立前提是
//	「每一个发放口都按同一 scope 记账」，而"平台承担"这一档恰恰是最容易被当成免费口子的那一类——
//	它没有余额墙挡着，也没有日额挡着（那些判据都只看客户侧），所以它是这条链上**唯一**的封顶。
//
//	上限取 20/人/日：向导里这一步的真实用法是"贴一段、改一段、再贴一段"，二十次足够；
//	越界**不报错**（口径③：手打那条路必须一直开着，§一·10「新增防护不拿可用性交押金」），
//	只出空草稿＋一行 WARN，报文里不出现"次数用完"这种指向不存在问题的句子。
//
//	档位三级＝env ONBOARD_DRAFT_DAILY > system_config onboard_draft_daily_limit > 代码默认（§一·10），
//	且**收 0 档**（allowZero）：0＝运营显式关掉这一档。这一族的"0 算不算一个真实档位"必须写在结构里
//	（AGENTS §25-b 那条），不许靠照抄邻居那一段——这里 0 的含义是"不设封顶"，不是"全部拒"。
const (
	onboardDraftDailyScope   = "onboard-draft:"
	onboardDraftDailyDefault = 20
	onboardDraftDailyWindow  = 86400
)

// onboardDraftPlaceholders 模型"没抽到"时最常见的几种偷懒写法。
// ★ 判据问的是形态不是内容：这一档清洗**只针对整格恰好等于占位串**的情形，
// 拿它当"关键词屏蔽"去剥正文会把合法译文一起吃掉（同 §一·13 那条"误剥的代价"）。
var onboardDraftPlaceholders = map[string]bool{
	"未提供": true, "暂无": true, "未知": true, "待定": true, "无": true, "没有": true,
	"未提及": true, "不明确": true, "未说明": true, "待补充": true,
	"n/a": true, "na": true, "null": true, "none": true, "nil": true, "unknown": true,
	"-": true, "—": true, "_": true, "。": true, "？": true, "?": true,
}

// registerProductOnboardDraftRoutes 把识别口挂进一期产品面的路由注册点（由 routesProduct 调一次）。
// 为什么单独成函数：本批的全部新增接线收在这一个新文件里（AGENTS §一·1 的"按域新建文件"），
// routesProduct 只多一行委托，不往既有 handler 堆里再塞一个分支。
func (s *Server) registerProductOnboardDraftRoutes() {
	s.mux.HandleFunc("/api/product/onboard/draft", s.handleProductOnboardDraft)
}

// onboardDraftLegWired 这一腿今天到底会不会真拨模型——只问 OnboardDraftHealthWord 那一个读点
// （它和 ExtractOnboardDraft 用的是同一个 onboardDraftStageModel 咽喉，所以不构成第二把尺子）。
//
// 为什么要先问这一句再占格子：没配第 ⑨ 张卡时这一腿**一次上游都不拨、一分钱不花**，
// 却照样把卖家当天的格子吃掉 ⇒ 运营日后翻 rate_limits 会看到一本"有人刷了 20 次"的账，
// 而真实支出是 0（§一·11 那一族「读数要为事实背书」；同 §21 那条「健康面不许报 ok 而这条腿根本没配」）。
func (s *Server) onboardDraftLegWired(ctx context.Context) bool {
	if s.Engine == nil {
		return false
	}
	return s.Engine.OnboardDraftHealthWord(ctx) == "ok"
}

// onboardDraftQuotaReserve 占一格（判据与自增收在 RateReserve 那一条带条件的 UPDATE 里，
// 并发打不会同时读到"还差一格"——§一·10 C25 那一族两步式被击穿的形态）。
// 三条出路：limit<=0＝运营显式关掉这一档，直接放行不记账；库故障＝**放行**并记错误
// （防薅表自己坏了不该把向导那一步打死，§一·10 fail-open）；抢到格子＝回 true 且调用方欠一次 release。
func (s *Server) onboardDraftQuotaReserve(ctx context.Context, uid int64) (reserved bool) {
	if s.Store == nil {
		return true
	}
	limit := configIntTier(s.Store, "ONBOARD_DRAFT_DAILY", "onboard_draft_daily_limit", onboardDraftDailyDefault, true)
	if limit <= 0 {
		return true
	}
	ok, _, err := s.Store.RateReserve(onboardDraftDailyScope, strconv.FormatInt(uid, 10), onboardDraftDailyWindow, int64(limit))
	if err != nil {
		observability.Error(ctx, "onboard_draft 防刷占格失败，本次放行", "err", err.Error(), "uid", uid)
		return true
	}
	if !ok {
		// 只报档与上限，绝不报描述正文（卖家的业务信息不进日志）。
		observability.Warn(ctx, "onboard_draft 今日识别次数已达上限，本单出空候选（向导按手打走）",
			"reason", "capped", "uid", uid, "limit", limit)
	}
	return ok
}

// onboardDraftQuotaRelease 把"这一格其实没换来任何模型调用"那几次退回去。
// 为什么必须退：本口的失败原因（没配／超时／上游错／截断／坏 JSON）全在平台这一侧，
// 卖家什么也没得到；不退就等于让一次上游抖动吃掉卖家当天的额度（§一·10「此前任何一次失败都不许扣格子」）。
// 注意 `capped` 那一档**不调这里**——它压根没占到格子，退了会把别人的格子退出来。
func (s *Server) onboardDraftQuotaRelease(uid int64) {
	if s.Store == nil {
		return
	}
	if err := s.Store.RateRelease(onboardDraftDailyScope, strconv.FormatInt(uid, 10), onboardDraftDailyWindow); err != nil {
		observability.Warn(context.Background(), "onboard_draft 退格失败（本次仍按已用计）", "err", err.Error(), "uid", uid)
	}
}

// handleProductOnboardDraft POST /api/product/onboard/draft：自由描述 → 待确认的候选字段。
// 落库一律没有：卖家确认后才由 /api/product/save 那条既有腿写（口径 ①）。
func (s *Server) handleProductOnboardDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "识别接口只支持 POST"))
		return
	}
	// ★ 身份只走 productActor 那一个入口（未登录 401；平台上下文 tid<=0 明确拒，
	//   不许被 effTenant 的兜底 1 读成「租户 1 的数据」——AGENTS §29 那条尺子的同一个咽喉）。
	u, tid, e := s.productActor(r)
	if e != nil {
		s.writeError(w, r, e)
		return
	}
	var req struct {
		Text   string `json:"text"`
		UILang string `json:"ui_lang"`
	}
	if e := decodeProductBody(r, &req); e != nil {
		s.writeError(w, r, e)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		// 文案点名"要贴什么"而不是"参数错"：这一格空着就点识别，是卖家第一次用向导最常撞的一枪。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "先把产品描述贴进来，再让系统认一遍"))
		return
	}
	if len([]rune(text)) > productOnboardDraftTextMaxChars {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "这段描述太长了，先挑最核心的那段贴进来"))
		return
	}

	// 计费归属与用量收集：与其余会拨模型的登录态口同形（userOrgCtx 挂 user，中间件挂 tenant；
	// WithUsageRecorder 让"这一单到底有没有产生实收"有一个可读的地方——本口恒不该有实收，
	// 因为模型腿打了 llm.PlatformOnboardDraft ⇒ 只写留痕行、不碰余额与日额，见 AGENTS §一·11）。
	ctx := s.userOrgCtx(r)
	if s.Engine != nil {
		ctx = s.Engine.WithUsageRecorder(ctx)
	}
	// ★ 占格子排在拨模型之前，且**只在真的会拨那一支**占（两条顺序都是判据的一部分）：
	//   ① 先占后拨＝并发打这一口时只有一支拿得到那一格（两步式"先判后记"会被击穿，§一·10 C25）；
	//   ② 没配第 ⑨ 张卡那一档连占格都不做，而是照样把请求交给引擎——
	//      这样"这一腿为什么没拨"的答案永远只有 `no_stage_model` 那一个来源，不在 handler 里另造一句。
	//   ③ 拨了却没拨成（超时／上游错／截断／坏 JSON）⇒ 退格：失败原因在平台这一侧，
	//      不许吃掉卖家当天的格子（同 §一·10「此前任何一次失败都不许扣格子」的方向）。
	var cand engine.OnboardDraftCandidate
	reason := ""
	reserved := false
	if s.onboardDraftLegWired(ctx) {
		if s.onboardDraftQuotaReserve(ctx, u.ID) {
			reserved = true
			cand, reason = s.Engine.ExtractOnboardDraft(ctx, text, req.UILang, store.ProductLangs)
		} else {
			reason = "capped"
		}
	} else {
		_, reason = s.Engine.ExtractOnboardDraft(ctx, text, req.UILang, store.ProductLangs)
	}
	if reserved && reason != "" {
		s.onboardDraftQuotaRelease(u.ID)
	}

	name := onboardDraftLine(cand.Name, productOnboardDraftMaxNameRunes)
	tagline := onboardDraftLine(cand.Tagline, productOnboardDraftMaxTaglineRunes)
	useCase := onboardDraftBlock(cand.UseCase, productOnboardDraftMaxUseCaseRunes)
	langs := productOnboardDraftLangs(req.UILang, cand.TargetLangs)

	if reason != "" {
		// ★ 只报 reason 档与长度，**绝不**把描述正文／上游地址/模型名写进日志（卖家的业务信息＋内部坐标）。
		//   报文里也不出现这一档：卖家看到的是"这张格子还空着，可以自己填"，不是一句技术失败。
		observability.Warn(r.Context(), "onboard_draft 本单出的是空候选（向导按手打走）",
			"reason", reason, "tid", tid, "uid", u.ID, "chars", len([]rune(text)))
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"draft": map[string]interface{}{
			"name": name, "tagline": tagline, "use_case": useCase, "target_langs": langs,
		},
	})
}

// productOnboardDraftLangs 把模型给的语种列表折进**站点支持的对外码**，并剔掉卖家自己的界面语言。
// 三条各对一个真实坏法（顺序即判据顺序，一条都不许放宽）：
//
//	① 脏码：模型会凭空造 `zh-CN`/`it`/`hi` 这类清单外的码 ⇒ 一旦落进建档表单，
//	   对外页的 hreflang 就长出脏值（§29 那条"受控值物理上进不来"同一个方向）；
//	② 自指：中文界面的卖家会被算出 target_langs 含 zh ⇒ 产品"面向中文"这件事在描述里从没提过；
//	③ 空值：一个都不剩时出**空数组**而不是 null——Go 侧 nil slice 序列化成 null，
//	   每个消费方（前端向导、管理台、SDK）都要各判一次（AGENTS §一·5「空页必须归一成 []」同族）。
func productOnboardDraftLangs(uiLang string, raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	skip := normalizeProductLangCode(uiLang)
	for _, item := range raw {
		code := normalizeProductLangCode(item)
		if code == "" || seen[code] {
			continue
		}
		if !productLangSupported(code) {
			continue
		}
		if skip != "" && code == skip {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out
}

// productLangSupported 认不认得某个对外码——只问 store.ProductLangs 那一份清单（唯一事实源）。
func productLangSupported(code string) bool {
	for _, c := range store.ProductLangs {
		if c == code {
			return true
		}
	}
	return false
}

// normalizeProductLangCode 只做**形状**归一（大小写／连字符／简繁中文的常见写法），
// 把形态差折进 store.ProductLangs 的那一份码表。
// ★ 这里不含"支持哪些语种"的判断：除了 zh 一支天生有简繁两形，其余一律小写＋`-`→`_`，
//
//	折出来的结果仍要回 productLangSupported 问一次才算数——所以本函数不是第二份清单。
func normalizeProductLangCode(tag string) string {
	t := strings.ToLower(strings.TrimSpace(tag))
	switch t {
	case "":
		return ""
	case "zh", "zh-cn", "zh-hans", "zh-hans-cn", "zh_hans", "zh_cn":
		return "zh"
	case "zh-hant", "zh-tw", "zh-hk", "zh-mo", "zh_hant":
		return "zh_hant"
	}
	return strings.ReplaceAll(t, "-", "_")
}

// onboardDraftLine 单行候选值（name/tagline）：折空白 → 剥占位 → 按 rune 上限截断。
// 折行是有意的：tagline 只有一行才有意义，模型偶尔把它写成两句带换行，
// 直接落进 `<input>` 会变成"看着只填了一半"（界面上后面那半截看不见）。
func onboardDraftLine(v string, maxRunes int) string {
	return onboardDraftField(strings.Join(strings.Fields(v), " "), maxRunes)
}

// onboardDraftBlock 多行候选值（use_case）：只首尾裁剪，**保留段内换行**——
// 这一格在向导里是 textarea，把换行吃掉等于把卖家自己写的分行排版抹平。
func onboardDraftBlock(v string, maxRunes int) string {
	return onboardDraftField(strings.TrimSpace(v), maxRunes)
}

// onboardDraftField 三个文本字段共用的那一把尺子：空/占位一律出**空串**（不是"未提供"、不是 null）。
// 参数 v: 已按行的形态归一过的候选值；maxRunes: 出栈上限（按字符计，避免把一个词劈成半个字节）。
func onboardDraftField(v string, maxRunes int) string {
	if v == "" || onboardDraftPlaceholders[strings.ToLower(v)] {
		return ""
	}
	if maxRunes > 0 {
		if rs := []rune(v); len(rs) > maxRunes {
			v = string(rs[:maxRunes])
		}
	}
	return v
}
