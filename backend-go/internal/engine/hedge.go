// ============================================================================
// ★ H6 Hedged Requests 竞速路由 + H7 路由实时统计基座
//
// 竞速（控成本、只在确定性场景启用）：
//
//	· 仅当 HedgeEnabled 且 非流式（stream sink 为空）且主路由未熔断、非 Hunyuan
//	  短超时特例、且存在备用供应商路由时，才把主 CallChat 交给 hedgedChat。
//	· 触发时机「慢分位感知」：基础延迟 HedgeDelayMs；有历史样本时取
//	  max(基础值, 1.5×主路由 P50)，上限 8s——主请求疑似掉队才打第二路。
//	· 先成功者胜出，败者 ctx 取消止损；两路都失败才把错误交回原降级链。
//	· 计费口径不变：仅真正返回 usage 的调用计量（被取消路多数不产生尾包）。
//
// 路由统计（H7 数据源，同时驱动本文件慢分位判定）：
//
//	每条 "base|model" 保留最近 256 个 (耗时ms, 成败, token 费用估算) 样本，
//	提供 P50/P95/错误率/平均成本快照供后台可视化与动态权重使用。
//
// ============================================================================
package engine

import (
	"context"
	"sort"
	"time"

	"translator/internal/config"
)

const (
	hedgeMaxDelay  = 8 * time.Second
	routeRingSize  = 256
	hedgeDelayBase = 1500 * time.Millisecond
)

// routeLatency 单路由滑窗统计（无锁外部访问统一经 routeMu）。
type routeLatency struct {
	lat     [routeRingSize]float64 // 耗时 ms（0 占位未写入）
	occ     [routeRingSize]bool
	pos     int
	n       int
	okCnt   int64
	failCnt int64
	tokIn   int64 // ★ H7：prompt token 累计（成本代理）
	tokOut  int64 // ★ H7：completion token 累计
}

// observeRoute 记录一次 LLM 路由调用结果（hedgedChat/常规降级链共用）。
func (e *Engine) observeRoute(base, model string, dur time.Duration, err error) {
	key := base + "|" + model
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	if e.routeStats == nil {
		e.routeStats = map[string]*routeLatency{}
	}
	st := e.routeStats[key]
	if st == nil {
		st = &routeLatency{}
		e.routeStats[key] = st
	}
	st.lat[st.pos] = float64(dur.Milliseconds())
	st.occ[st.pos] = true
	st.pos = (st.pos + 1) % routeRingSize
	if st.n < routeRingSize {
		st.n++
	}
	if err == nil {
		st.okCnt++
	} else {
		st.failCnt++
	}
}

// routeStatsSnapshot 加锁取指定 key 的统计（仅测试/诊断使用）。
func (e *Engine) routeStatsSnapshot() map[string]*routeLatency {
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	cp := make(map[string]*routeLatency, len(e.routeStats))
	for k, v := range e.routeStats {
		cp[k] = v
	}
	return cp
}

// routePctl 该路由最近样本的指定分位耗时（ms；无样本返回 0）。
func (e *Engine) routePctl(base, model string, p float64) float64 {
	key := base + "|" + model
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	st := e.routeStats[key]
	if st == nil || st.n == 0 {
		return 0
	}
	vals := make([]float64, 0, st.n)
	for i := 0; i < routeRingSize; i++ {
		if st.occ[i] {
			vals = append(vals, st.lat[i])
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	idx := int(float64(len(vals)-1) * p)
	return vals[idx]
}

// RouteStatView H7 后台可视化单行快照。
type RouteStatView struct {
	Route         string  `json:"route"`   // base|model
	Samples       int     `json:"samples"` // 滑窗样本数
	P50Ms         float64 `json:"p50_ms"`
	P95Ms         float64 `json:"p95_ms"`
	OK            int64   `json:"ok"`
	Fail          int64   `json:"fail"`
	ErrRate       float64 `json:"err_rate"`
	TokensPerCall float64 `json:"tokens_per_call"` // ★ H7 单位成本代理（in+out/成功调用）
}

// RouteStats 全部路由统计快照（按 key 排序稳定输出）。
func (e *Engine) RouteStats() []RouteStatView {
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	keys := make([]string, 0, len(e.routeStats))
	for k := range e.routeStats {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []RouteStatView
	for _, k := range keys {
		st := e.routeStats[k]
		base, model, _ := cutRouteKey(k)
		total := st.okCnt + st.failCnt
		var errRate float64
		if total > 0 {
			errRate = float64(st.failCnt) / float64(total)
		}
		var tpc float64
		if st.okCnt > 0 {
			tpc = float64(st.tokIn+st.tokOut) / float64(st.okCnt)
		}
		out = append(out, RouteStatView{
			Route: k, Samples: st.n, OK: st.okCnt, Fail: st.failCnt, ErrRate: errRate,
			P50Ms: e.routePctlLocked(base, model, 0.50), P95Ms: e.routePctlLocked(base, model, 0.95),
			TokensPerCall: tpc,
		})
	}
	return out
}

// routePctlLocked 分位数（调用方持锁；避免 RouteStats 组装时重复加锁死锁）。
func (e *Engine) routePctlLocked(base, model string, p float64) float64 {
	st := e.routeStats[base+"|"+model]
	if st == nil || st.n == 0 {
		return 0
	}
	vals := make([]float64, 0, st.n)
	for i := 0; i < routeRingSize; i++ {
		if st.occ[i] {
			vals = append(vals, st.lat[i])
		}
	}
	if len(vals) == 0 {
		return 0
	}
	sort.Float64s(vals)
	return vals[int(float64(len(vals)-1)*p)]
}

// ObserveRouteTokens ★ H7：OnUsage 回调侧记账（按 model 匹配全部路由条目）。
func (e *Engine) ObserveRouteTokens(model string, prompt, completion int64) {
	if e.routeStats == nil || model == "" {
		return
	}
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	for k, st := range e.routeStats {
		if _, m, ok := cutRouteKey(k); ok && m == model {
			st.tokIn += prompt
			st.tokOut += completion
		}
	}
}

// routeHealth 供动态选路：错误率 + P95（无样本 → 中性 0 / 0）。
func (e *Engine) routeHealth(base, model string) (errRate, p95 float64, calls int64) {
	key := base + "|" + model
	e.routeMu.Lock()
	st := e.routeStats[key]
	e.routeMu.Unlock()
	if st == nil {
		return 0, 0, 0
	}
	total := st.okCnt + st.failCnt
	if total == 0 {
		return 0, 0, 0
	}
	return float64(st.failCnt) / float64(total), e.routePctl(base, model, 0.95), total
}

// routeAvgTokens 每成功调用 token 成本代理。
func (e *Engine) routeAvgTokens(base, model string) float64 {
	key := base + "|" + model
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	st := e.routeStats[key]
	if st == nil || st.okCnt == 0 {
		return 0
	}
	return float64(st.tokIn+st.tokOut) / float64(st.okCnt)
}

// cutRouteKey 拆分 "base|model"。
func cutRouteKey(k string) (string, string, bool) {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == '|' {
			return k[:i], k[i+1:], true
		}
	}
	return k, "", false
}

// hedgeApplicable 判定本次主调用是否走竞速通道。
// 约束：HedgeEnabled、主路由未熔断、非 Hunyuan 短超时特例、存在备援路由、
// 且非流式（流式已向用户吐字，无法安全竞速）——即主要覆盖 pro 精修/复核
// 与异步文件任务这类「批量非交互」调用（控成本意图）。
func (e *Engine) hedgeApplicable(ctx context.Context, mainOpen, hunyuan bool, fallbacks []config.ProviderConfig) bool {
	if !config.C.HedgeEnabled || mainOpen || hunyuan || len(fallbacks) == 0 {
		return false
	}
	if e.LLM == nil {
		return false
	}
	return streamSinkInnerFromCtx(ctx) == nil
}

// hedgedChat 主/次双路竞速，先成者得；两路皆败返回后到错误（保留原降级链语义）。
func (e *Engine) hedgedChat(ctx context.Context, cfg *config.Config, base, key, model string,
	messages []map[string]string, maxTokens int, sec config.ProviderConfig) (string, string, error) {

	type out struct {
		content, finish string
		err             error
	}
	ch := make(chan out, 2)

	delay := time.Duration(cfg.HedgeDelayMs) * time.Millisecond
	if delay <= 0 {
		delay = hedgeDelayBase
	}
	if p50 := e.routePctl(base, model, 0.5); p50 > 0 {
		if d := time.Duration(p50 * 1.5 * float64(time.Millisecond)); d > delay {
			delay = d
		}
		if delay > hedgeMaxDelay {
			delay = hedgeMaxDelay
		}
	}

	pctx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	pStart := time.Now()
	go func() {
		c, f, err := e.LLM.CallChat(pctx, base, key, model, messages, maxTokens, false, cfg.FallbackTemp)
		e.observeRoute(base, model, time.Since(pStart), err)
		ch <- out{c, f, err}
	}()

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.content, r.finish, r.err
	case <-timer.C: // 主路进入慢分位区间 → 对冲次优供应商
	case <-ctx.Done():
		return "", "", ctx.Err()
	}

	sctx, scancel := context.WithCancel(ctx)
	defer scancel()
	sStart := time.Now()
	go func() {
		c, f, err := e.LLM.CallChat(sctx, sec.APIBase, sec.APIKey, sec.Model, messages, maxTokens, false, cfg.FallbackTemp)
		e.observeRoute(sec.APIBase, sec.Model, time.Since(sStart), err)
		ch <- out{c, f, err}
	}()

	var lastErr error
	for done := 0; done < 2; done++ {
		select {
		case r := <-ch:
			if r.err == nil {
				return r.content, r.finish, nil
			}
			lastErr = r.err
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	return "", "", lastErr
}
