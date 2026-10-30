// ============ instant_free.go · 职责说明 ============
// 即时翻译免费档的**唯一准入判据**（★ 2026-10-30 三事合一批 组4，D-1／D-16／A-0～A-5）。
//
// 一句话：四条腿（文本/图片/语音识别/朗读）在免费档内不扣积分；超出免费档**不拒绝**，
// 转付费档；超过付费即时硬顶才引导去翻译工单（`use_ticket`）——那是一次漏斗转场，不是失败。
//
// 判定顺序写死（§5.2，第 0 步必须在最前）：
//
//  0. **尺寸/时长先定档**：超硬顶 ⇒ 引导工单；超免费档且 ≤ 硬顶 ⇒ 付费档
//  1. 免费档关闭（任一额度键 = -1）⇒ 付费档
//  2. 目标语种数 > free_lang_limit ⇒ 付费档（多语种同出是付费权益，**不拒绝**）
//  3. 账号当日免费积分 + 本次预估 > 上限 ⇒ 付费档
//  4. 租户共享池同理 ⇒ 付费档
//  5. 本 IP 当日免费次数 ≥ 上限 ⇒ 付费档
//
// 为什么第 0 步必须最前：先判身份后判尺寸会让"有余额但图只有 800 KB"的请求也去吃付费闸，
// 白撞一次余额墙，而且会让「交给工单」这条漏斗永远走不到。
//
// 计数的落点（三处，各自唯一）：
//
//	· **积分**（free_pt_user / free_pt_tenant）在 `ChargeUsageRealtime` 的免费分支里加——
//	  那里才拿得到真实 billed；在 handler 里预扣＝拿预估当账单（§一·11 那族）。
//	· **IP 次数**（free_ip）在被判成免费的**那一刻** +1（请求级事实；被拒不加，
//	  否则刷墙的人会在真正使用之前把当天的格子吃光）。
//	· **撞墙脚印**（free_wall）只在被拒那一枪 +1，成功路径一次都不加。
//
// fail-open：计数器读写失败 ⇒ 按"未用"处理并出声（同 §一·10「新增防护不拿可用性交押金」）。
// =============================================
package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
	"translator/internal/httpx"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/ops"
	"translator/internal/store"
)

// 免费档的分流原因码（对外契约；前端按它选文案与按钮，**不许**按中文文案猜）。
const (
	instantReasonTooLargePaid = "too_large_paid" // 超免费档、仍在付费即时档内 ⇒ 按量计费继续
	instantReasonTooLarge     = "too_large"      // 超付费即时硬顶 ⇒ 引导工单
	instantReasonTooLong      = "too_long"       // 音频超长（付费即时档内 ⇒ 付费；超硬顶 ⇒ 工单）
	instantReasonUseTicket    = "use_ticket"     // 即时翻译这一档整体不收 ⇒ 直接引导工单
	instantReasonUnreadable   = "unreadable"     // 探测不出时长/解不开 ⇒ 不静默放行
	instantReasonFreeOff      = "free_off"       // 免费档被运营关闭
	instantReasonLangLimit    = "lang_limit"     // 多语种＝付费权益
	instantReasonQuotaUser    = "quota_user"     // 账号暗额度用尽
	instantReasonQuotaTenant  = "quota_tenant"   // 租户共享池用尽
	instantReasonQuotaIP      = "quota_ip"       // IP 次数用尽
)

// instantMiB 尺寸换算（两档都以 MiB 为单位配置）。
const instantMiB = int64(1) << 20

// instantDefaultHardMB 脏配置（0）时的硬顶兜底（与出厂默认一致）。
const instantDefaultHardMB = 3

// instantInput 一次即时翻译请求的**可观测事实**（全部由调用方现读，判据本身不读库）。
type instantInput struct {
	Kind        string  // text|image|audio
	SizeBytes   int64   // 图片/音频体积；文本填 0
	DurationSec float64 // 音频时长；非音频填 0
	LangCount   int     // 目标语种数（至少 1）
	EstPoints   int64   // 本次预估积分（文本可按字符估；图片/音频无文本量可估 ⇒ 0）
	FreeUser    int64   // 账号当日已用免费积分
	FreeTenant  int64   // 租户共享池当日已用
	FreeIP      int64   // 本 IP 当日已用免费次数
}

// instantRoute 判定结果。
type instantRoute struct {
	Free          bool
	Reason        string // ""=免费；其余见 instantReason*
	SuggestTicket bool   // true 时出参带 suggest:"ticket"（前端带文件跳工单页）
}

// instantFreeEligible 纯函数判定（不读库、不碰 HTTP）——N3/N9 两条锁直接打这里。
func instantFreeEligible(cfg ops.InstantEffective, in instantInput) instantRoute {
	// —— 第 0 步：尺寸/时长先定档 ——
	switch in.Kind {
	case "image":
		if cfg.ImageMaxMB < 0 { // 负值＝即时翻译这一档不收图片
			return instantRoute{Reason: instantReasonUseTicket, SuggestTicket: true}
		}
		hardMB := cfg.ImageMaxMB
		if hardMB == 0 {
			hardMB = instantDefaultHardMB
		}
		if in.SizeBytes > hardMB*instantMiB {
			return instantRoute{Reason: instantReasonTooLarge, SuggestTicket: true}
		}
		if cfg.FreeImageMaxMB < 0 || (cfg.FreeImageMaxMB > 0 && in.SizeBytes > cfg.FreeImageMaxMB*instantMiB) {
			return instantRoute{Reason: instantReasonTooLargePaid} // 付费即时档内，继续按量计费
		}
	case "audio":
		if cfg.AudioMaxSec < 0 || (cfg.AudioMaxSec > 0 && in.DurationSec > float64(cfg.AudioMaxSec)) {
			return instantRoute{Reason: instantReasonTooLong, SuggestTicket: true}
		}
		hardMB := cfg.AudioMaxMB
		if hardMB == 0 {
			hardMB = instantDefaultHardMB
		}
		if cfg.AudioMaxMB > 0 && in.SizeBytes > hardMB*instantMiB {
			return instantRoute{Reason: instantReasonTooLong, SuggestTicket: true}
		}
		if cfg.FreeAudioMaxSec < 0 || (cfg.FreeAudioMaxSec > 0 && in.DurationSec > float64(cfg.FreeAudioMaxSec)) {
			return instantRoute{Reason: instantReasonTooLong}
		}
	}
	// —— 第 1 步：免费档总闸 ——
	if cfg.FreeDailyPoints < 0 || cfg.FreeIPDaily < 0 {
		return instantRoute{Reason: instantReasonFreeOff}
	}
	// —— 第 2 步：语种数是付费权益 ——
	if cfg.FreeLangLimit > 0 && int64(in.LangCount) > cfg.FreeLangLimit {
		return instantRoute{Reason: instantReasonLangLimit}
	}
	// —— 第 3/4 步：暗额度（隐形上限）——
	if cfg.FreeDailyPoints > 0 && in.FreeUser+in.EstPoints > cfg.FreeDailyPoints {
		return instantRoute{Reason: instantReasonQuotaUser}
	}
	if cfg.FreeTenantDailyPoints > 0 && in.FreeTenant+in.EstPoints > cfg.FreeTenantDailyPoints {
		return instantRoute{Reason: instantReasonQuotaTenant}
	}
	// —— 第 5 步：IP 次数 ——
	// 走 store.RateDayCeilReached 这一处判据（limit<=0 恒不拦的 0/负数语义在那边只有一份），
	// 禁止在这里再写一遍 `> 0 && >=` —— 那是 §一·11「多把尺子」的同族形态。
	if store.RateDayCeilReached(store.DayState{Used: in.FreeIP}, cfg.FreeIPDaily) {
		return instantRoute{Reason: instantReasonQuotaIP}
	}
	return instantRoute{Free: true}
}

// instantEffective 现读该租户的免费档配置（平台策略 → 租户覆盖 → 时间窗，已由 ops 合并好）。
func (s *Server) instantEffective(tid int64) ops.InstantEffective {
	if s == nil {
		return ops.DefaultEffective().Instant
	}
	return s.effectivePolicyCached(tid).Instant
}

// dayUsed 读一个 UTC 日计数桶的当日已用量（读失败 fail-open＝按 0 处理并出声）。
func (s *Server) dayUsed(scope, key string) int64 {
	if s.Store == nil || key == "" {
		return 0
	}
	st, err := s.Store.RateDayLoad(scope, key)
	if err != nil {
		observability.Warn(context.Background(), "免费档计数器读取失败（按未用放行，fail-open）",
			"scope", scope, "key", key, "error", err.Error())
		return 0
	}
	return st.Used
}

// instantEstimatePoints 文本的预估积分（与 /api/estimate 同一把尺子：engine/costmodel ＋ PointsFromTokens）。
// 图片/音频没有文本量可估 ⇒ 返回 0（成本顶由 IP 次数与体积/时长档兜住，见文件头）。
func (s *Server) instantEstimatePoints(text string, langCount int, mode, srcLang, uiLang string) int64 {
	if s.Store == nil || text == "" || langCount <= 0 {
		return 0
	}
	langs := make([]string, 0, langCount)
	for i := 0; i < langCount; i++ {
		langs = append(langs, uiLang)
	}
	tokens, _ := engine.EstimateChatTokens(text, langs, mode, srcLang, uiLang)
	return s.Store.PointsFromTokens(tokens)
}

// instantDecide 聚合一次判定：现读配置与三个计数器，交给纯函数判，并在判成免费时 +1 IP 次数。
func (s *Server) instantDecide(r *http.Request, tid, uid int64, in instantInput) instantRoute {
	cfg := s.instantEffective(tid)
	in.FreeUser = s.dayUsed(store.RateScopeFreePointUser, strconv.FormatInt(uid, 10))
	in.FreeTenant = s.dayUsed(store.RateScopeFreePointTenant, strconv.FormatInt(tid, 10))
	ip := httpx.ClientIP(r)
	in.FreeIP = s.dayUsed(store.RateScopeFreeIP, ip)
	route := instantFreeEligible(cfg, in)
	if route.Free {
		if s.Store != nil && ip != "" {
			if _, err := s.Store.RateDayAdd(store.RateScopeFreeIP, ip, 1); err != nil {
				observability.Warn(context.Background(), "免费档 IP 计数写入失败（本次照常免费）", "ip", ip, "error", err.Error())
			}
		}
		return route
	}
	// 撞墙脚印：只在这一枪 +1，成功路径一次都不加（§5.2 那张表的不对称是设计）
	if uid > 0 && s.Store != nil && cfg.FreeDailyPoints > 0 && in.FreeUser >= cfg.FreeDailyPoints {
		if _, err := s.Store.RateDayAdd(store.RateScopeFreeWall, strconv.FormatInt(uid, 10), 1); err != nil {
			observability.Warn(context.Background(), "免费档撞墙脚印写入失败", "uid", uid, "error", err.Error())
		}
	}
	return route
}

// gateUsageInstant 免费档专用闸门：保留 QPS / 并发 / 日限额，**跳过余额与预算墙**
// （免费不涉及钱；余额墙留给付费档，§5.4）。实现＝原 gateUsage 的同一份代码，
// 由 gateUsageDepth 的 withMoney 开关决定要不要走"余额 + 预算墙"那两段——
// 复制一份判据是 AGENTS §一·11 明令禁止的形态，所以这里只切开关。
func (s *Server) gateUsageInstant(r *http.Request) (int64, func(), error) {
	return s.gateUsageDepth(r, false)
}

// instantMarkCtx 给 ctx 装免费标记（四个 handler 共用；只有这一个写点）。
func instantMarkCtx(ctx context.Context) context.Context { return llm.WithFreeInstant(ctx) }

// instantFreeValidate 免费档九键的写入侧校验（H-3）。
// 规则三条：① 任一键 `< -1` 拒收（-1/0/N 都是合法档：关/不限/上限）；
// ② **免费档不得宽于付费档**：free_image_max_mb ≤ image_max_mb、free_audio_max_sec ≤ audio_max_sec；
// ③ 免费音频折算体积不得超过音频硬顶明显过多（24 kbps opus 下 60 s ≈ 180 KB，留 10 倍余量判）。
// 只校验**本次提交里出现的键**与出厂默认/既有值的组合？——不：保存是整包覆盖语义，
// 所以缺省键取默认值一起算，避免"只改一项、另一项还是旧值"时校验漏过。
func instantFreeValidate(p ops.InstantPatch) error {
	chk := func(name string, v *int64) error {
		if v != nil && *v < -1 {
			return fmt.Errorf("%s 不能小于 -1（-1＝关闭 / 0＝不限 / N＝上限）", name)
		}
		return nil
	}
	for _, kv := range []struct {
		name string
		v    *int64
	}{
		{"free_daily_points", p.FreeDailyPoints},
		{"free_tenant_daily_points", p.FreeTenantDailyPoints},
		{"free_ip_daily", p.FreeIPDaily},
		{"free_image_max_mb", p.FreeImageMaxMB},
		{"free_audio_max_sec", p.FreeAudioMaxSec},
		{"image_max_mb", p.ImageMaxMB},
		{"audio_max_mb", p.AudioMaxMB},
		{"audio_max_sec", p.AudioMaxSec},
		{"free_lang_limit", p.FreeLangLimit},
	} {
		if err := chk(kv.name, kv.v); err != nil {
			return err
		}
	}
	d := ops.DefaultEffective().Instant
	pick := func(v *int64, def int64) int64 {
		if v != nil {
			return *v
		}
		return def
	}
	imgFree := pick(p.FreeImageMaxMB, d.FreeImageMaxMB)
	imgHard := pick(p.ImageMaxMB, d.ImageMaxMB)
	if imgFree > 0 && imgHard > 0 && imgFree > imgHard {
		return fmt.Errorf("免费档图片上限（%d MiB）不得大于即时翻译硬顶（%d MiB）", imgFree, imgHard)
	}
	audFree := pick(p.FreeAudioMaxSec, d.FreeAudioMaxSec)
	audHard := pick(p.AudioMaxSec, d.AudioMaxSec)
	if audFree > 0 && audHard > 0 && audFree > audHard {
		return fmt.Errorf("免费档音频时长（%d 秒）不得大于即时翻译硬顶（%d 秒）", audFree, audHard)
	}
	// 24 kbps ≈ 3 KB/s，60 s ≈ 180 KB；留 10 倍余量（1.8 MB）判"配置倒挂到离谱"
	if audFree > 0 {
		if estKB := audFree * 3; estKB > 1800 {
			return fmt.Errorf("免费档音频时长（%d 秒）折算体积过大，请同时核对音频体积硬顶", audFree)
		}
	}
	return nil
}

// instantFreeHealthWord 免费档状态词（/api/health 的 instant_free 字段）。
// 只出三档词：on＝开放；off＝运营显式关档（任一暗额度为 -1）；unknown＝启动早期存储未就绪。
// ⚠️ 不许在这里带出任何额度数字（H-2/W-4）：这是匿名可达的健康面，数字出栈即被拿去算薅羊毛的量。
func (s *Server) instantFreeHealthWord(r *http.Request) string {
	if s == nil || s.Store == nil {
		return "unknown"
	}
	cfg := s.instantEffective(s.currentTenant(r))
	if cfg.FreeDailyPoints < 0 || cfg.FreeIPDaily < 0 {
		return "off"
	}
	return "on"
}

// handleInstantCaps GET /api/instant/caps —— 即时翻译**对外档位宽度**的公开读数。
//
// 为什么单独开一个匿名接口：官网文案、引导卡、助手提示词三面都吃同一份数字（S2 跨面同尺）。
// **只出宽度，不出额度**：图片/录音两档上限、免费档宽度、免费语种数与"免费档是否开着"的状态词。
// `free_daily_points`/`free_ip_daily`/`free_tenant_daily_points` 三个暗额度**一个字都不出**——
// 数字一旦出栈，薅羊毛的算力立刻用在这条链上（W-4），而助手拿到就会把它念给客户（I-3）。
func (s *Server) handleInstantCaps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 GET"))
		return
	}
	cfg := s.instantEffective(s.currentTenant(r))
	writeJSON(w, 200, map[string]interface{}{
		"success":            true,
		"free_on":            !(cfg.FreeDailyPoints < 0 || cfg.FreeIPDaily < 0),
		"free_image_max_mb":  cfg.FreeImageMaxMB,
		"free_audio_max_sec": cfg.FreeAudioMaxSec,
		"free_lang_limit":    cfg.FreeLangLimit,
		"image_max_mb":       cfg.ImageMaxMB,
		"audio_max_mb":       cfg.AudioMaxMB,
		"audio_max_sec":      cfg.AudioMaxSec,
		"ticket_max_mb":      fileproc.MaxUploadBytes >> 20,
	})
}
