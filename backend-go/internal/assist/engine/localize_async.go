// ============ localize_async.go · 职责说明 ============
// 挂件首屏（greet）那条 canned 翻译路的「**同步有界 + 后台补翻**」（★ 0AF，2026-10-01）。
//
// ★ 现网实证（不是假设）：泰文这类**冷缓存语种**的第一次翻译在上游要跑满 >30 秒，
// 而主站 `/assist-api` 的反代超时是 30 秒 ⇒ 反代先把连接砍断，访客看到的是
// HTTP 502 / 30.001890s / `UPSTREAM_UNAVAILABLE`；日志侧同期只有 `"reason":"upstream_error"` 那行
// （assist 的日志是 JSON、字段带引号）。更要命的是**缓存行永远写不进去**：
// 上游那一次的结果随请求一起被丢弃，下一个访客再来还是同样的 502 —— 一个冷语种能把自己
// 永久钉在坏状态上，而界面看起来「只是慢」。
//
// 根因不是模型慢，是**慢的那一次被放在了访客的请求链上**。这一条把两件事拆开：
//   - 同步腿：只等 `canned_translate_timeout_sec`（默认 8 秒，硬夹 ≤25 秒），到点就出中文原文；
//   - 后台腿：请求链之外再打一次，用 `context.Background()` 派生 + 自己的预算
//     （`canned_background_timeout_sec`，默认 60 秒），成功后**同样过出栈闸**再写缓存。
//     下一次 greet 即命中译文，502 那一条路径从此不再出现。
//
// ★ 为什么两条腿合用**一个在途声明**（single-flight），而不是"后台各自去重"：
// 冷语种上线那一刻是 N 个并发访客同时 miss。若只有后台腿去重，N 条同步腿照样各拨一枪
// （N 次 8 秒的白等 + N 次上游往返），第 N+1 条后台腿再去重就已经晚了。
// 声明在**打上游之前**拿，赢家做同步腿，输家立刻出中文原文（不排队、不等待）；
// 赢家超时后把声明**移交**给后台腿，直到后台腿收尾才释放 ⇒ 整个窗口里同键只有一条腿在打。
//
// ★ 为什么「出栈闸拒掉的那一稿」不触发后台补翻（与"上游失败"刻意不同形）：
// 闸门拒的是**模型已经认真回答、但产物不合格**的那一稿。同一句提示词立刻重拨一次，
// 期望收益接近零，而代价是「模型系统性翻坏这一语种」时把上游打成重拨风暴
// （canned_guard.go 文件头那条代价注释讲的正是这个形态：被拒时退化成每次 greet 现翻，
// 由访客流量自带节流）。⇒ 只有**同步腿没拿到产物**（超时／被取消／上游报错）才值得后台补一次。
//
// ★ 后台腿的四条纪律（都是本仓踩过的坑，改这块前先读）：
//  1. **绝不复用请求 ctx** —— handler 一返回它就被 cancel，后台腿会当场死掉，
//     等于把这条修法退回到"缓存永远写不上"的原状态（派生口径单测＝TestCannedBackgroundCtxIsIndependent，
//     端到端反证＝TestCannedBackgroundSurvivesCanceledRequest：传一个已 cancel 的 reqCtx，
//     后台腿仍必须把译文写进缓存；把 reqCtx 直接传下去那条用例当场红）；
//     trace_id 是**值传递**接过去的（不是 ctx 继承），所以日志仍能把「访客那一次 greet」和
//     「后台这一枪」串成同一条链。
//  2. **同键去重**：见上面那个在途声明；释放一律走 defer，panic 也会释放（见第 4 条）。
//  3. **不写负缓存**：与 localize.go / canned_guard.go 的既有口径一致（失败与不合格都不落库，
//     给"模型改好了"留一条自动生效的路）。过期判定只有一条：这条腿在途期间库里那一行被
//     运营手工改过（!manual）或原文/口径又变了（指纹不等）⇒ 后台稿作废不覆盖，
//     理由同 F-75 那条「把读到的旧值当现值载回就覆盖掉真配置」。
//  4. **panic 不许带崩进程**：同步腿有 net/http 的 recover 兜着，后台腿没有——
//     一个没兜住的 panic 是整个 assist 进程挂掉（同 §一·12 那条「第三方解析器 panic 击穿同步路径」
//     的另一面）。⇒ 后台腿自带 recover 收成一档 WARN（canned_bg_panic），并照常释放在途声明。
//
// ⚠️ 本文件**不碰**译文最终形态：cannedPromptRev、cannedOutboundReject 的三条判据、
// translateContract 按源文投的口径、指纹计算（localizeContract / srcFingerprint）全部照旧。
// 后台腿与同步腿调的是**同一个** translateOnce，同函数同入参必然同值（096x-1 那条指纹义务的延伸）。
//
// ⚠️ 没有 shutdown 钩子可挂：本包的 Engine 没有关闭机制，也没有新建一套框架的必要。
// 后台腿的收口口径就是它**自己的 ctx 超时**（默认 60 秒）+ defer 释放；
// 进程退出时 goroutine 随进程消失，在途声明是内存态、不落库，不会留下"永久占格"的状态。
// 唯一的额外保险是声明的 TTL（cannedFlightTTL）：万一将来有人在腿里加了不看 ctx 的挂死调用，
// 同键也不会永久无人能翻（见 claimCannedFlight 的注释）。
// =============================================
package engine

import (
	"context"
	stderrors "errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"translator/internal/assist/llm"
	"translator/internal/errors"
	"translator/internal/observability"
)

// configs 表里的两个预算键（★ 0AF）。
// 手法照本包既有配置：读 configs 表 + 代码默认值，管理台可改、不新建 env 解析层
// （同 engine.go 的 temperature／max_tokens，以及 vector.go 的 embed_recall）。
const (
	cfgCannedSyncTimeoutSec = "canned_translate_timeout_sec"
	cfgCannedBgTimeoutSec   = "canned_background_timeout_sec"
)

// 预算的默认值与硬夹区间（★ 0AF）。
// ⚠️ 同步腿的上限钉死在 25 秒：这条修法存在的全部理由就是「绝不顶到反代那 30 秒」，
// 运维把键写成 999 也拿不到 999（同 AGENTS §一·10「档位数字三级来源、代码默认兜底」的取向）。
// 后台腿的下限 5 秒：小于这个数的重试基本等于再失败一次，只是把日志刷响。
const (
	cannedSyncTimeoutDefaultSec = 8
	cannedSyncTimeoutMinSec     = 1
	cannedSyncTimeoutMaxSec     = 25
	cannedBgTimeoutDefaultSec   = 60
	cannedBgTimeoutMinSec       = 5
	cannedBgTimeoutMaxSec       = 300
	// cannedFlightSlackSec 在途声明的 TTL 余量：两条腿的预算之和再加这一点，
	// 覆盖「预算读到了但上游连接比预算更晚死」的正常抖动，不构成第二条超时链。
	cannedFlightSlackSec = 30
)

// canned 有界/后台这条链的失败分档（★ 0AF）。
// 与 han_residue.go 那七个 reject*、canned_guard.go 那三个 cannedReject* 同族：
// **日志里看得见的名字就是对外排障契约**，逐字钉进 localize_async_test.go，改字面量当场红。
// 分成「同步／后台」两组而不是共用一组，是因为这两组的**运维动作完全不同**：
// 同步档多＝预算或上游耗时的问题（要么调档要么找上游），后台档多＝上游真的在报错或产物不合格。
const (
	// 同步档（访客那一侧，决定"这一屏为什么还是中文"）
	cannedSyncTimeout  = "canned_sync_timeout"  // 超出自有预算——现网 502 那一批的正身
	cannedSyncCanceled = "canned_sync_canceled" // 访客/反代先走了（连接被砍），跟上游挂了是两种病
	cannedSyncUpstream = "canned_sync_upstream" // 上游在预算内明确报错（网络/5xx/截断/空译文）
	// 后台档（决定"下一次 greet 为什么还没命中译文"）
	cannedBgTimeout  = "canned_bg_timeout"  // 后台腿也踩到自己的预算：预算给小了，或上游这一语种根本不通
	cannedBgUpstream = "canned_bg_upstream" // 后台腿拿到的是上游错误（同步腿那次失败没自愈）
	cannedBgGated    = "canned_bg_gated"    // 后台腿的产物没过出栈闸：翻成功了但不合格，一行都不写
	cannedBgStale    = "canned_bg_stale"    // 后台腿在途期间库里那一行变了（运营手工改／原文又变）
	cannedBgPanic    = "canned_bg_panic"    // 后台腿 panic（被 recover 收住，进程没事；出现即必须查栈）
)

// CannedSyncBudget 同步腿的预算（对外可读：api 层用它给「欢迎词 + chips」两次翻译**共用一个截止**，
// 见 server.go handleGreeting；不共用就是最坏 2×预算 的串行等待）。
func (e *Engine) CannedSyncBudget() time.Duration {
	return e.cannedBudgetSec(cfgCannedSyncTimeoutSec, cannedSyncTimeoutDefaultSec, cannedSyncTimeoutMinSec, cannedSyncTimeoutMaxSec)
}

// cannedBgBudget 后台腿的预算。给得比同步腿宽得多是刻意的：这一侧没有访客在等，
// 而"泰文第一次就是要 30 秒"这件事本身没有错——把它放到请求链外跑完，才是这条修法的收益所在。
// ⚠️ 实际上限还会被 llm.Client 自己的降级链总预算（ASSIST_LLM_TIMEOUT，默认 45 秒）截一道，
// 运维若要让更慢的语种落库，调的是那一档而不是这里。
func (e *Engine) cannedBgBudget() time.Duration {
	return e.cannedBudgetSec(cfgCannedBgTimeoutSec, cannedBgTimeoutDefaultSec, cannedBgTimeoutMinSec, cannedBgTimeoutMaxSec)
}

// cannedBudgetSec 读一个「秒」形配置并硬夹到 [minSec,maxSec]；键缺失／非数字／越界一律回落默认。
// 为什么不 0＝关闭：这两档管的是"等多久算失败"，配 0 语义上是"不等"，
// 真想要"永不后台补翻"的运维动作是关 LLM 配置而不是把这里写成 0（写 0 只会拿到下限）。
func (e *Engine) cannedBudgetSec(key string, defSec, minSec, maxSec int) time.Duration {
	sec := defSec
	if v := strings.TrimSpace(e.db.GetConfig(key, "")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			sec = n
		}
	}
	if sec < minSec {
		sec = minSec
	}
	if sec > maxSec {
		sec = maxSec
	}
	return time.Duration(sec) * time.Second
}

// cannedFlightKey 在途声明的键。
// 带上指纹而不是只用 (kind,lang)：运营改原文（或口径升档）时指纹会变，
// 新那一档必须能立刻重新开始翻，而不是等旧那一档的残留声明过期——
// 而"同一 (kind,lang) 的并发补翻只允许一条腿"这条要求不受影响，因为并发访客吃的是同一份原文。
func cannedFlightKey(kind, uiLang, fp string) string {
	return kind + "\x00" + canonicalLang(uiLang) + "\x00" + fp
}

// cannedFlightTTL 声明的最长持有时间＝两条腿预算之和 + 余量。
// 正常路径下释放全走 defer（含 panic 路径），这一档只是兜住"将来有人在腿里加了不看 ctx 的挂死调用"
// 那种退化形态：没有它，一次没释放的声明会让这一 (kind,lang,指纹) **永久**直接出中文，
// 而日志里一行错误都没有——那是比 502 更难发现的坏状态。
func (e *Engine) cannedFlightTTL() time.Duration {
	return e.CannedSyncBudget() + e.cannedBgBudget() + cannedFlightSlackSec*time.Second
}

// claimCannedFlight 拿同键的在途声明；返回 false＝已有一条腿在打，调用方直接出中文原文（不排队）。
func (e *Engine) claimCannedFlight(key string) bool {
	e.cannedFlightsMu.Lock()
	defer e.cannedFlightsMu.Unlock()
	if e.cannedFlights == nil {
		e.cannedFlights = map[string]time.Time{}
	}
	if started, ok := e.cannedFlights[key]; ok && time.Since(started) < e.cannedFlightTTL() {
		return false
	}
	e.cannedFlights[key] = time.Now()
	return true
}

// releaseCannedFlight 释放在途声明（defer 调用；重复释放无害）。
func (e *Engine) releaseCannedFlight(key string) {
	e.cannedFlightsMu.Lock()
	defer e.cannedFlightsMu.Unlock()
	delete(e.cannedFlights, key)
}

// cannedFailureReason 把一次"没拿到产物"归到对外排障分档。
// **两问都要**（先看自己那条有界 ctx，再看上游返回的 error）：
// http 客户端会把 deadline 包进 *url.Error，只问 errors.Is 在某些路径拿不到；
// 只问 ctx.Err() 又会把「上游自己挂了但还没到预算」错记成超时——那两种病的运维动作完全不同。
// 后台腿没有"被访客取消"这一档（它的父 ctx 是 context.Background()），
// 所以调用方传进来的三个档名按自己的射程给。
func cannedFailureReason(err error, callCtx context.Context, timeoutTag, canceledTag, otherTag string) string {
	// 先问 error 本身：调用方没给有界 ctx（或那条 ctx 已经释放）时这是唯一判据，
	// 而且上游自己把 deadline 包进错误返回的那一档也只有这里接得住。
	switch {
	case stderrors.Is(err, context.DeadlineExceeded):
		return timeoutTag
	case stderrors.Is(err, context.Canceled):
		return canceledTag
	case callCtx == nil:
		return otherTag
	case callCtx.Err() == context.DeadlineExceeded:
		return timeoutTag
	case callCtx.Err() == context.Canceled:
		return canceledTag
	}
	return otherTag
}

// cannedBackgroundCtx 派生后台腿自己的 ctx：**只从 reqCtx 取 trace_id 这一个值**，不继承它的
// deadline 与 cancel，然后挂上后台腿自己的预算。
//
// 为什么单独抽出来（而不是就地两行）：这一行就是本批最容易写错的地方——
// 直接 `context.WithTimeout(reqCtx, …)` 语法更自然、编译更顺，而它的后果是后台腿一出生就带着
// 「访客已经走了」这个事实，第一行调用即 ctx canceled ⇒ 缓存永远写不上 ⇒ 修法看起来上线了、
// 其实从没跑过（同 §一·12 那句「产物能打开不算验收」）。单测点＝TestCannedBackgroundCtxIsIndependent。
func cannedBackgroundCtx(reqCtx context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	tid := errors.TraceIDFromContext(reqCtx)
	if tid == "" {
		tid = errors.GenTraceID() // 后台腿自己造一条，日志仍可按这个 id 串起来
	}
	return context.WithTimeout(errors.WithTraceID(context.Background(), tid), budget)
}

// launchCannedBackground 在请求链之外再补一枪；返回假＝没起成（调用方自己释放在途声明）。
//
// 参数 reqCtx 只用来取 trace_id（值传递，见 cannedBackgroundCtx），
// 声明键 flightKey 由本函数接手释放——移交成功后同步腿那条 defer 必须跳过，
// 否则后台腿还在打上游时同键就能被第二个访客重新声明，去重直接失效（双拨上游）。
//
// wantLines（★ 0AR 第 4 波）由同步腿原样传下来：后台腿的产物**同样要过条数契约**，
// 两侧不共用一个数就是"同步侧拦住了、后台侧把同一份坏稿写进库"的产地。
func (e *Engine) launchCannedBackground(reqCtx context.Context, client *llm.Client, kind, text, uiLang, fp, flightKey, syncReason string, wantLines int) bool {
	if client == nil || !client.Enabled() {
		return false // 没上游就没资格补翻（同 translateOnce 的「LLM 未接入」档），也让声明立即释放
	}
	bgCtx, cancel := cannedBackgroundCtx(reqCtx, e.cannedBgBudget())
	e.cannedBgWg.Add(1)
	go func() {
		defer e.cannedBgWg.Done()
		defer cancel()
		defer e.releaseCannedFlight(flightKey)
		e.runCannedBackground(bgCtx, client, kind, text, uiLang, fp, flightKey, syncReason, wantLines)
	}()
	return true
}

// runCannedBackground 后台腿本体：打上游 → 过**同一道**出栈闸 → 过期判定 → 写缓存。
// 与同步腿共用 translateOnce 和 cannedOutboundReject，一个字都不另写：
// 两条腿各写一份判据就是下一次「同步侧修了、后台侧照投坏稿」的产地（canned_guard.go 同一口径）。
//
// ★ 0AR 第 4 波：这一腿**失败即开退避窗口**（见 canned_backoff.go 为什么连同步腿一起押），
// 成功即清窗——退避的状态机只有这两个转移，没有第三态，所以不会有"永久占格"那一族 bug。
func (e *Engine) runCannedBackground(ctx context.Context, client *llm.Client, kind, text, uiLang, fp, flightKey, syncReason string, wantLines int) {
	// ★ 纪律第 4 条：这一条 goroutine 不在 net/http 的 recover 覆盖范围内，
	// 一次没兜住的 panic 会把整个 assist 进程带走（所有访客一起挂，比 502 严重一个量级）。
	// 退避照开：panic 的成因通常是**这批数据本身**（不是抖动），下一位访客这一枪同样不该打。
	defer func() {
		if r := recover(); r != nil {
			e.markCannedBackoff(flightKey)
			e.noteCannedCold(ctx, kind, uiLang, cannedBgPanic)
			observability.Warn(ctx, "assist.engine canned 后台补翻腿 panic，已收住（进程无碍，缓存未写）",
				"kind", kind, "lang", uiLang, "reason", cannedBgPanic, "panic", fmt.Sprint(r),
				"sync_reason", syncReason, "stack", firstRunes(string(debug.Stack()), 800))
		}
	}()

	out, err := e.translateOnce(ctx, client, text, uiLang, localizeMaxTokens,
		"产品欢迎语/短问句", cannedSurface)
	if err != nil {
		reason := cannedFailureReason(err, ctx, cannedBgTimeout, cannedBgUpstream, cannedBgUpstream)
		// 后台也失败 ⇒ 一行都不写（既有「不写负缓存」口径），下一次 greet 重新走一次有界同步腿。
		// 这一行是"冷语种到底落没落库"的唯一读数：出现 canned_bg_timeout 就该调
		// ASSIST_LLM_TIMEOUT／canned_background_timeout_sec，出现 canned_bg_upstream 就该查上游本身。
		e.markCannedBackoff(flightKey)
		e.noteCannedCold(ctx, kind, uiLang, reason)
		observability.Warn(ctx, "assist.engine canned 后台补翻未成功，缓存保持不写",
			"kind", kind, "lang", uiLang, "reason", reason, "sync_reason", syncReason, "err", err,
			"backoff_sec", int(e.CannedBackoff().Seconds()))
		return
	}
	topics := srcTopicsOf(text)
	gateFinal, gateReason, gateDetail, bad := cannedOutboundReject(uiLang, text, out, topics, wantLines)
	if bad {
		// 后台腿的产物同样要过闸：这一稿会**常驻**首屏，闸门在后台腿失效＝坏形态自动落库，
		// 比同步侧漏放更糟（同步侧至少每次 greet 都重新判一次）。
		// ★ 0AR ㊷：这一档正是现网 th 的闭环终点（同步超时 → 后台补枪 → 后台那稿被闸拒 →
		// 缓存写不上 → 下一位访客从零再来）。旧形态这里 return 之后什么都不留，
		// 于是同一句提示词每隔一位访客重拨一次、每次都拒；现在开退避窗口，
		// 让"再试一次"发生在窗口到期那一次，而不是发生在每一位访客的首屏上。
		e.markCannedBackoff(flightKey)
		e.noteCannedCold(ctx, kind, uiLang, cannedBgGated)
		observability.Warn(ctx, "assist.engine canned 后台补翻的译文未过出栈闸，按不写处理",
			"kind", kind, "lang", uiLang, "reason", cannedBgGated, "gate_reason", gateReason,
			"detail", gateDetail, "sync_reason", syncReason, "before", firstRunes(out, 160),
			"backoff_sec", int(e.CannedBackoff().Seconds()))
		return
	}
	if gateReason != "" { // ★ 0AR 纯观测档：后台腿同一口径（三处共用一个方法，见 observeCannedSoftTier）
		e.observeCannedSoftTier(ctx, kind, uiLang, gateReason, gateDetail, "bg_write", out)
	}
	// ★ 纪律第 3 条的过期判定：这条腿在途期间库里那一行可能已被运营手工改掉（!manual 头），
	// 也可能原文/口径又变了一次（指纹不等）。两种都不许把后台这份旧稿盖回去——
	// 后台腿没有访客在等，覆盖一份人工配置是纯风险零收益。
	key := cannedCacheKey(kind, uiLang)
	if cur := e.db.GetConfig(key, ""); cur != "" {
		head, _, ok := splitCachedTranslation(cur)
		if !ok || head != fp {
			observability.Warn(ctx, "assist.engine canned 后台补翻的译文已过期，不覆盖库里现值",
				"kind", kind, "lang", uiLang, "reason", cannedBgStale, "sync_reason", syncReason,
				"want_head", firstRunes(head, 40), "cur_head", firstRunes(cur, 40))
			return
		}
	}
	if gateFinal != out { // ★ 0AR 修正腿生效：落库的必须是判过的那一串（与同步腿同一口径）
		observability.Info(ctx, "assist.engine canned 后台补翻的译文经修正腿清洗后落库（同指纹，非新稿）",
			"kind", kind, "lang", uiLang, "reason", cannedRepaired, "sync_reason", syncReason,
			"before", firstRunes(out, 120), "after", firstRunes(gateFinal, 120))
	}
	_ = e.db.SetConfig(key, fp+"\n"+gateFinal)
	// ★ 0AR ㊷：成功即清退避窗口——退避的全部意义是"下一批访客别再白烧一枪"，
	// 而这一枪已经成了，窗口留着就会把**已经好了**的语种继续按坏了处理（那是把修法做成新故障）。
	e.clearCannedBackoff(flightKey)
	e.observeCannedScriptImpurity(ctx, kind, uiLang, gateFinal, false)
	// 生效必留一行 INFO：这一条链的失败形态是"界面正常、缓存一直空"，
	// 没有这行就没法证明后台腿真跑通过（排障时"从没出现这一行"本身就是结论）。
	observability.Info(ctx, "assist.engine canned 后台补翻生效，译文已落缓存",
		"kind", kind, "lang", uiLang, "sync_reason", syncReason, "len", len(gateFinal))
}
