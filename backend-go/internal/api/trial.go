// ============ trial.go · 职责说明 ============
// 〇-Z「免登录即时翻译试用」的后端半边：POST /api/trial/translate（匿名可打）。
//
// 为什么要这一条接口：官网首页此前所有入口都直落注册页，访客「想先试一句」也得先建账号
// ——用户反馈把这判成劝退形态（已经有账号的人更是被要求再注册一次）。试用面把
// 「值不值得注册」交还给一次真实翻译：给 5 句、专业校对模式（质量优先，不给快速模式——
// 第一句就拿到糙活，试用额度再宽也是白送），超出即引导注册。
//
// 射程刻意收得很窄（与「其他功能都要注册后使用」同一条口径）：
//
//	· 只翻单段文本、单目标语种、单次 ≤300 字符；模式在服务端钉成 pro，
//	  请求体里没有 mode/skill/options 字段可读，匿名流量不可能「挑个便宜模式刷」；
//	· 不写知识库、不进 TM、不发 webhook、不发任务中心奖励（不调 grantTranslateTask）；
//	· 语种白名单 = config.TranslateLangs ∪ {zh}，与 /api/translation/langs 同一份事实，
//	  前端语言下拉能选到的一定能过闸。
//
// ★ 成本归属（本文件最关键的一条决策）：
//
//	匿名请求经 withTenant 会被强制落默认租户 1（rox）。照搬登录态链路的话，
//	每一句试用都会**从租户 1 的余额里扣积分**——那是把市场推广费用记到客户账上，
//	客户来问账时无法解释。故进引擎前显式 `tenant.WithTenant(ctx, 0)`：
//	ChargeUsageRealtime 对 tid<=0 直接返回（既不扣余额也不自动落账），
//	而 engine.tenantID(ctx) 仍回退默认租户 1，知识库/提示词/模型路由照常工作。
//	代价由平台自己承担，另在成功路径末尾以 charge_kind='log' 的**留痕行**记进 usage_ledger
//	（tenant_id=0，不参与退款与消耗核算，只让试用成本在账务里看得见）。
//	⚠️ 排查提示：管理台「按租户用量」看不到这些行（它们不属于任何客户）；
//	   要核试用成本查 `WHERE tenant_id=0 AND biz_kind='text'`。
//
// 防刷三道闸（全部落 rate_limits 表，重启/多实例共享，与注册护栏同一后端）：
//  1. trial_dev：<设备号> 滚动 24h 内 5 句（设备号由浏览器生成并持久化，见前端 lib/trialDevice.ts）；
//  2. trial_ip：<IP> 每日句数上限——同一 NAT 出口多人时不至于第一人就吃光配额，
//     同时给「换设备号刷」设一个下限；
//  3. trial_day：平台每日总句数（硬预算顶）——试用是市场费用，必须有天花板。
//     另有同设备最小间隔（防连点脚本）。三档额度都可配（AGENTS §一·3 优先序：
//     环境变量 TRIAL_DEVICE_QUOTA / TRIAL_IP_DAILY / TRIAL_GLOBAL_DAILY
//     > system_config 同名小写键 > 代码默认）。
//
// 计数纪律：**只有成功交付译文才计数**。闸门拒绝或引擎失败都不吃配额——
// 访客没拿到结果却少了一句额度，是最容易被投诉的一种账（与 lead/forgot 同款 allow-record 语义）。
//
// 错误口径（AGENTS §一·8）：一律 writeError + 稳定码，本批新增 TRIAL_EXHAUSTED（429）。
// 前端按 code 分支：TRIAL_EXHAUSTED → 出「注册继续用」的引导卡；其余校验类 → 就地提示。
// =============================================
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/i18n"
	"translator/internal/tenant"
)

// 试用额度默认值。5 句是产品口径（用户 2026-09-28 定：试用 5 句即时翻译，超出需注册），
// 另两档是防刷/预算的工程判断。要改优先走配置，见 trialLimit() 的三级优先序。
const (
	trialDeviceQuotaDefault = 5    // 每设备号每滚动 24h 可用句数
	trialIPDailyDefault     = 40   // 每 IP 每日句数上限（家庭/企业 NAT 出口）
	trialGlobalDailyDefault = 3000 // 平台每日试用总句数（硬预算顶）
	trialMinIntervalSec     = 3    // 同设备两次请求最小间隔（防连点脚本）
	trialMaxTextRunes       = 300  // 单次原文字符上限（按 rune 计，不按字节）
	trialWindowSec          = 86400
	// trialMinCostUnit 留痕行的最小计量单位：纯知识库命中不走模型时 TokensUsed=0，
	// 按「一句一个单位」留痕，避免试用在成本视图里全是 0 的假象。
	trialMinCostUnit = 1
)

// rate_limits 的 scope 常量（单测与冒烟脚本按这些字面值核对账目，勿随意改名）。
const (
	trialScopeDevice = "trial_dev"
	trialScopeIP     = "trial_ip"
	trialScopeGlobal = "trial_day"
	trialScopeIntv   = "guard_int" // 复用注册护栏同款最小间隔 scope，key 加 trial: 前缀隔离
)

// trialDeviceIDRe 设备号格式：8~64 位字母数字/下划线/连字符（前端 crypto.getRandomValues 出的 base64url）。
// 为什么校验而不当 opaque 串用：这个值直接当 rate_limits 的 key 片段，
// 放任超长串、引号或带空格的值进来，等于把限流表变成垃圾场，还能靠构造同形 key 撞别人的窗口。
var trialDeviceIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// trialReq 试用请求体。字段比 /api/chat 少一圈——没有 mode/skill/options，
// 免得试用面变成「匿名也能指定模式」的口子。
type trialReq struct {
	Text       string `json:"text"`        // 待翻原文（≤300 字符）
	TargetLang string `json:"target_lang"` // 单个目标语种码，须在白名单内
	DeviceID   string `json:"device_id"`   // 浏览器设备号（localStorage 持久化）
}

// trialResp 试用成功出参。积分/token 一个都不出（试用不收费，报个消耗数字只会让人以为被扣了），
// 只给译文、语种、剩余句数与「注册后可用」的下一步文案键提示（left=0 时前端切注册引导）。
type trialResp struct {
	Success     bool   `json:"success"`
	Translation string `json:"translation"` // 译文
	SourceLang  string `json:"source_lang"` // 引擎侧自动检测到的源语种
	TargetLang  string `json:"target_lang"` // 本次目标语种
	Mode        string `json:"mode"`        // 恒为 "pro"（写死回显，让访客知道拿到的是校对档）
	Left        int    `json:"left"`        // 本设备剩余句数（前端据此显示「剩 N 句」并决定何时切注册引导）
	Exhausted   bool   `json:"exhausted"`   // left=0 的显式同义位，省得前端拿「0」去猜是不是没统计
}

// handleTrialTranslate POST /api/trial/translate：免登录试用一次即时翻译。
// 校验顺序：方法 → 依赖就绪 → 解析 → 设备号格式 → 原文非空与限长 → 语种白名单
// → 最小间隔 → 设备/IP/全局三档额度 → 引擎 → 计数与留痕 → 出参。
func (s *Server) handleTrialTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "该接口仅支持 POST"))
		return
	}
	// 依赖未就绪先短路：试用是匿名流量，让它跑到引擎里只会拿一串 nil 指针去撞 panic。
	if s.Store == nil || s.Engine == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrServiceUnavailable, "服务正在启动，请稍后再试"))
		return
	}
	var req trialReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	device := strings.TrimSpace(req.DeviceID)
	if !trialDeviceIDRe.MatchString(device) {
		// 设备号不合法＝没法记账，宁可让前端重新生成，也不能放成「匿名无限次」
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "试用标识不合法，请刷新页面后重试"))
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请输入要试译的内容"))
		return
	}
	// 限长文案故意不插值数字：插了就是 fmt 拼接句，后端 zh→en 词条表按静态字面量收录，
	// 英文访客会拿到中文（AGENTS §一·5 后端直出面同族坑）。上限数字由前端文案自己讲。
	if len([]rune(text)) > trialMaxTextRunes {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "试用内容过长，注册后可翻译整篇文档"))
		return
	}
	lang := strings.ToLower(strings.TrimSpace(req.TargetLang))
	if !trialLangAllowed(lang) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "该语言暂不在试用范围内"))
		return
	}
	ip := clientIP(r)
	devQuota := s.trialLimit("TRIAL_DEVICE_QUOTA", "trial_device_quota", trialDeviceQuotaDefault)
	ipQuota := s.trialLimit("TRIAL_IP_DAILY", "trial_ip_daily", trialIPDailyDefault)
	globalQuota := s.trialLimit("TRIAL_GLOBAL_DAILY", "trial_global_daily", trialGlobalDailyDefault)

	// —— 额度闸：任一不过即拒，并带上 reason／retry_after，前端才能给准话而不是猜 ——
	if st, _ := s.Store.RateLoad(trialScopeIntv, "trial:"+device); st.WindowStart > 0 {
		if wait := trialMinIntervalSec - int(time.Now().Unix()-st.WindowStart); wait > 0 {
			s.writeError(w, r, apierrors.New(apierrors.ErrRateLimited, "操作太快了，请稍后再试").WithRetryAfter(wait))
			return
		}
	}
	if st, _ := s.Store.RateLoad(trialScopeDevice, device); trialWindowActive(st.WindowStart) && st.Count >= int64(devQuota) {
		s.writeError(w, r, trialExhausted("device"))
		return
	}
	if st, _ := s.Store.RateLoad(trialScopeIP, ip); trialWindowActive(st.WindowStart) && st.Count >= int64(ipQuota) {
		s.writeError(w, r, trialExhausted("ip"))
		return
	}
	// 全局预算用固定 key：整平台一份账。越线只说「今日名额已满」，不把内部额度数字漏给匿名访客。
	if st, _ := s.Store.RateLoad(trialScopeGlobal, "global"); trialWindowActive(st.WindowStart) && st.Count >= int64(globalQuota) {
		s.writeError(w, r, trialExhausted("global"))
		s.trialBudgetAlert(st.Count) // 低频告警：预算被打满是市场费用异常信号（刷量或爆量），当天就该看见
		return
	}

	// —— 进引擎：模式与语种都在服务端钉死；ctx 租户清成 0（成本归平台，见文件头★段） ——
	options := map[string]interface{}{
		"target_langs": []interface{}{lang},
		"mode":         "pro",
		// 提示词语言跟随访客界面语种（X-App-Lang / Accept-Language，withLang 已解析）：
		// 外语访客的译文质量不该由「后端默认按中文提示词跑」决定。
		"lang": i18n.FromRequest(r),
	}
	ctx := tenant.WithTenant(tenant.WithLang(tenant.WithMode(r.Context(), "pro"), lang), 0)
	res := s.Engine.HandleText(ctx, text, options, nil)
	if res == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrTranslationFailed, "试用暂时不可用，请稍后再试或注册后使用完整功能"))
		return
	}
	if res.Error == engine.CodeSensitiveBlocked {
		// 合规闸命中：内容本身被拒（不进模型、不计数），给 400 而不是 500——
		// 换一句内容就能继续用，让客户以为服务坏了是错的归因。
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "内容不符合使用规范，请换一段文本再试"))
		return
	}
	translation := strings.TrimSpace(res.Data.Translations[lang])
	if res.Error != "" || translation == "" {
		// 引擎失败不计配额（访客没拿到译文却被扣掉一句，是最容易被投诉的一种账）
		s.writeError(w, r, apierrors.New(apierrors.ErrTranslationFailed, "试用暂时不可用，请稍后再试或注册后使用完整功能"))
		return
	}

	// —— 成功才计数：设备／来源 IP／全局预算三本账同时推进 ——
	s.Store.RateRecord(trialScopeIntv, "trial:"+device, 1)
	devState, _ := s.Store.RateRecord(trialScopeDevice, device, trialWindowSec)
	ipState, _ := s.Store.RateRecord(trialScopeIP, ip, trialWindowSec)
	s.Store.RateRecord(trialScopeGlobal, "global", trialWindowSec)
	left := devQuota - int(devState.Count)
	if left < 0 {
		left = 0
	}
	if int(ipState.Count) >= ipQuota {
		// IP 档到顶时把设备剩余也归零：下一句必然被 IP 闸拒掉，
		// 界面上却还写着「剩 3 句」，那是让访客白点一次。
		left = 0
	}

	// —— 成本留痕（租户 0，charge_kind='log'，不动任何客户余额）——
	s.trialRecordCost(ctx, lang, res)

	writeJSON(w, 200, trialResp{
		Success:     true,
		Translation: translation,
		SourceLang:  engine.DetectSourceLang(res.Data.SourceText),
		TargetLang:  lang,
		Mode:        "pro",
		Left:        left,
		Exhausted:   left <= 0,
	})
}

// trialLangAllowed 试用目标语种白名单：KB 语种 ∪ {zh}（外语翻回中文）。
// 判据与 handleTranslationLangs 下发给前端的那份语言表同源，
// 所以「前端下拉里有的」与「后端肯收的」不会各说各话。
// 入参自己再小写一次：handler 里已经 ToLower，但这是**白名单判据**，
// 不该依赖「调用方记得规范化」——否则将来任何新调用点（如开放 API 侧复用）
// 传 "EN" 就会被判成不在范围，报错还很难看（同一语种两种结果）。
func trialLangAllowed(lang string) bool {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return false
	}
	if lang == "zh" {
		return true
	}
	for _, c := range config.TranslateLangs {
		if strings.ToLower(c) == lang {
			return true
		}
	}
	return false
}

// trialWindowActive 判断 rate_limits 读到的窗口起点是否仍在有效窗内。
// 为什么不能只看 Count>0：RateLoad 不清理过期窗口（只有写路径 RateRecord 才重置），
// 于是昨天的第 5 句今天仍躺在那里——照 Count 判会把「隔夜恢复额度」锁成永久耗尽。
// 判据本体在 register_guard.go 的 windowActiveIn（★ F-81 起注册侧两档用同一把尺子），
// 这里保留试用侧的名字，是把「窗口长度」这一处差异收在调用点而不是复制一份逻辑。
func trialWindowActive(windowStart int64) bool {
	return windowActiveIn(windowStart, trialWindowSec)
}

// trialExhausted 组装「试用额度用完」的结构化拒绝：稳定码 TRIAL_EXHAUSTED（429）
// ＋面向人的文案＋reason（device|ip|global，前端据此决定引导文案，也据此判断要不要留「刷新再来」的活路）。
//
// ★ 三条文案必须**以字面量直接写在 apierrors.New 的第二个实参位**，不许先赋给变量再传：
//
//	词条覆盖棘轮 TestAPICnMessageLiteralsCovered 的正则只认 `New(code, "中文")` 这一种形，
//	写成 `msg := "…"; New(code, msg)` 会让这句**静默逃过闸门**，英文访客点开就看到一句中文——
//	正是 〇-S #12 建闸时要堵的那一类（本条由 09-28 全量闸门实跑抓到，不是假想）。
func trialExhausted(reason string) *apierrors.APIError {
	switch reason {
	case "global":
		return apierrors.New(apierrors.ErrTrialExhausted, "今日试用名额已用完，注册后可随时使用完整功能").
			WithDetails(map[string]interface{}{"reason": reason})
	case "ip":
		return apierrors.New(apierrors.ErrTrialExhausted, "当前网络今天用得比较多，注册后额度按账号计算").
			WithDetails(map[string]interface{}{"reason": reason})
	default:
		return apierrors.New(apierrors.ErrTrialExhausted, "免费试用已用完，注册后继续使用").
			WithDetails(map[string]interface{}{"reason": reason})
	}
}

// trialLimit 按 AGENTS §一·3 的优先序解析一档试用额度：环境变量 > system_config > 代码默认。
// 非法值（空、非数字、<=0）一律回落下一级，最后落到 def。
// 为什么 0 不当成「关掉试用」：把额度配成 0 多半是手滑，而静默关掉入口比写错数字更难查；
// 真要停用功能请在管理台下线该入口，而不是在这里留一个 0。
// ★ F-81 起本体收在 register_guard.go 的 configIntTier（注册侧三档与试用侧三档共用同一优先序实现），
// 这里只固定「试用额度不接受 0」这一档语义，避免两份实现各自漂移。
func (s *Server) trialLimit(envKey, cfgKey string, def int) int {
	return configIntTier(s.Store, envKey, cfgKey, def, false)
}

// trialRecordCost 把一次试用的真实消耗以「租户 0 + 留痕」记进 usage_ledger。
// 数量优先取引擎回吐的 token 用量（与收费路径同一 cost 公式，SUM(cost) 可对账）；
// 纯知识库命中不走模型时为 0，按句计一个最小单位，保证试用成本不是全 0 假象。
// provider/model 口径与 usageModel 的回退档一致（统一网关 global + 全局默认模型）。
func (s *Server) trialRecordCost(ctx context.Context, lang string, res *engine.TextTranslateResult) {
	if s.Store == nil || res == nil {
		return
	}
	model := "unknown"
	if s.Cfg != nil && s.Cfg.OnlineModel != "" {
		model = s.Cfg.OnlineModel
	}
	qty := res.TokensUsed
	if qty <= 0 {
		qty = trialMinCostUnit
	}
	// 留痕失败不阻断出参：试用已经交付给访客了，记账异常该走告警而不是把译文吞掉。
	if err := s.Store.LogUsage(0, 0, "translate", "global", model, lang, qty, "text", "pro"); err != nil {
		// 用 CreateAlert（同类 open 告警幂等去重）而不是 CreateAlertPerOrder（逐条必发）：
		// 留痕失败是**同一个故障在重复发生**，第二十条告警不提供新信息，只会把运维屏刷满；
		// 运营处理完把告警关掉，下一次失败自然会再发一条。
		_ = s.Store.CreateAlert(0, "warning", "trial_meter", "免登录试用成本留痕失败："+err.Error())
	}
	s.metrics.addUsage(qty)
}

// trialBudgetAlert 平台日预算触顶告警。
// 触发即说明试用流量异常（刷量或推广爆量），值得让运维当天看到，而不是月底对账才发现。
// 去重靠 Store.CreateAlert 的「同租户+同类型已有 open 告警即跳过」语义（alerts 表内建），
// 故这里不再自建 rate_limits 哨兵——自己造去重窗口会遇到「告警已解决却仍压在哨兵里不发」
// 与「哨兵过期但告警还开着」两种互相矛盾的状态，交给数据层那一份事实更省事。
func (s *Server) trialBudgetAlert(count int64) {
	if s.Store == nil {
		return
	}
	_ = s.Store.CreateAlert(0, "warning", "trial_budget",
		"免登录试用当日额度已用完（"+strconv.FormatInt(count, 10)+" 句），如非推广爆量请排查刷量")
}
