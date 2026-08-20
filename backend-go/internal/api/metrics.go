package api

// ============ 本文件职责中文说明 ============
// 本文件实现系统级指标收集与 Prometheus 文本格式导出：
//   - Metrics 进程内计数器（原子递增），提供 HTTP 请求数、翻译成功/失败数、累计计量 token 等指标
//   - metricsText：将收集的指标快照渲染为 Prometheus 文本格式（含版本/运行时长/Go 运行时/LLM 健康/平台规模）
//   - handleMetrics：/metrics 接口（监控探针无鉴权，仅暴露聚合指标，不涉及租户数据）
// 用途：被 Prometheus 等监控系统抓取，用于服务健康与平台运营可视化。

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// ============ 系统级指标收集（Prometheus 文本格式导出） ============

// Metrics 进程内指标计数器（原子递增，/metrics 时快照输出）。
type Metrics struct {
	// HTTP 请求计数：path → 计数（按请求路径标签聚合）
	httpReqs map[string]*int64
	// 翻译成功/失败计数：kind(text/file/openapi/ticket) → count
	translationsOK   map[string]*int64
	translationsFail map[string]*int64
	// 累计计量 token（成本核算口径，计费计量累加）
	usageTokens int64
	// 服务启动时间（用于计算 uptime）
	startedAt time.Time
}

// newMetrics 初始化指标收集器：创建空计数映射并记录启动时间。
// 返回: 已初始化的 *Metrics 实例。
func newMetrics() *Metrics {
	return &Metrics{
		httpReqs:         map[string]*int64{},
		translationsOK:   map[string]*int64{},
		translationsFail: map[string]*int64{},
		startedAt:        time.Now(),
	}
}

// countHTTP 记录一次 HTTP 请求（按路径标签聚合计数）。
// 参数 path: 请求路径；无返回。并发安全（原子递增）。
func (m *Metrics) countHTTP(path string) {
	if m == nil {
		return
	}
	// 惰性初始化该路径的计数器（先取后增，避免热点锁）
	v, ok := m.httpReqs[path]
	if !ok {
		v = new(int64)
		m.httpReqs[path] = v
	}
	atomic.AddInt64(v, 1)
}

// countTranslate 记录一次翻译结果（按 kind 与成功/失败分别计数）。
// 参数 kind: 翻译类型(text/file/openapi/ticket)；ok: 是否成功。无返回。
func (m *Metrics) countTranslate(kind string, ok bool) {
	if m == nil {
		return
	}
	var v *int64
	var m2 map[string]*int64
	// 按结果选择成功或失败计数映射
	if ok {
		m2 = m.translationsOK
	} else {
		m2 = m.translationsFail
	}
	v, ok2 := m2[kind]
	if !ok2 {
		v = new(int64)
		m2[kind] = v
	}
	atomic.AddInt64(v, 1)
}

// addUsage 累加计量 token（成本核算口径，与计费计量联动）。
// 参数 tokens: 本次用量 token 数（<=0 忽略）。无返回。
func (m *Metrics) addUsage(tokens int64) {
	if m == nil || tokens <= 0 {
		return
	}
	atomic.AddInt64(&m.usageTokens, tokens)
}

// metricsText 生成 Prometheus 文本格式指标快照（供 /metrics 导出）。
// 返回: Prometheus 文本格式的指标字符串；metrics 未初始化时返回空串。
func (s *Server) metricsText() string {
	var sb strings.Builder
	now := time.Now()
	m := s.metrics
	if m == nil {
		return ""
	}
	// 版本与启动信息（固定 gauge）
	sb.WriteString("# HELP translator_info 版本与启动信息\n")
	sb.WriteString("# TYPE translator_info gauge\n")
	sb.WriteString(fmt.Sprintf("translator_info{version=\"2.0.0-go\"} 1\n"))
	sb.WriteString(fmt.Sprintf("# HELP translator_uptime_seconds 服务运行秒数\n# TYPE translator_uptime_seconds gauge\ntranslator_uptime_seconds %d\n", int(now.Sub(m.startedAt).Seconds())))

	// 运行时指标：goroutine 数、堆内存、累计分配（Go runtime 采样）
	sb.WriteString("# HELP go_goroutines 当前 goroutine 数\n# TYPE go_goroutines gauge\n")
	sb.WriteString(fmt.Sprintf("go_goroutines %d\n", runtime.NumGoroutine()))
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	sb.WriteString("# HELP go_heap_bytes Go 堆内存字节\n# TYPE go_heap_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("go_heap_bytes %d\n", mem.HeapAlloc))
	sb.WriteString("# HELP go_alloc_bytes 累计分配字节\n# TYPE go_alloc_bytes counter\n")
	sb.WriteString(fmt.Sprintf("go_alloc_bytes %d\n", mem.TotalAlloc))

	// HTTP 请求：按路径排序输出，保证指标文本稳定
	sb.WriteString("# HELP translator_http_requests_total HTTP 请求总数\n# TYPE translator_http_requests_total counter\n")
	paths := make([]string, 0, len(m.httpReqs))
	for p := range m.httpReqs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sb.WriteString(fmt.Sprintf("translator_http_requests_total{path=%q} %d\n", p, atomic.LoadInt64(m.httpReqs[p])))
	}

	// 翻译计数：合并成功/失败 key 集合，按 kind + result 输出
	sb.WriteString("# HELP translator_translations_total 翻译调用总数\n# TYPE translator_translations_total counter\n")
	kinds := map[string]bool{}
	for k := range m.translationsOK {
		kinds[k] = true
	}
	for k := range m.translationsFail {
		kinds[k] = true
	}
	kindList := make([]string, 0, len(kinds))
	for k := range kinds {
		kindList = append(kindList, k)
	}
	sort.Strings(kindList)
	for _, k := range kindList {
		okN := int64(0)
		if v, ok := m.translationsOK[k]; ok {
			okN = atomic.LoadInt64(v)
		}
		failN := int64(0)
		if v, ok := m.translationsFail[k]; ok {
			failN = atomic.LoadInt64(v)
		}
		sb.WriteString(fmt.Sprintf("translator_translations_total{kind=%q,result=\"ok\"} %d\n", k, okN))
		sb.WriteString(fmt.Sprintf("translator_translations_total{kind=%q,result=\"fail\"} %d\n", k, failN))
	}

	// 计量 token：累计计量量（成本口径）
	sb.WriteString("# HELP translator_usage_tokens_total 累计计量 token（成本口径）\n# TYPE translator_usage_tokens_total counter\n")
	sb.WriteString(fmt.Sprintf("translator_usage_tokens_total %d\n", atomic.LoadInt64(&m.usageTokens)))

	// LLM 健康：主模型熔断状态与错误率（引擎可用时输出）
	if s.Engine != nil {
		open := 0
		if s.Engine.BreakerOpen() {
			open = 1
		}
		sb.WriteString("# HELP translator_llm_breaker_open 主模型熔断状态（1=熔断中）\n# TYPE translator_llm_breaker_open gauge\n")
		sb.WriteString(fmt.Sprintf("translator_llm_breaker_open %d\n", open))
		sb.WriteString("# HELP translator_llm_error_rate LLM 调用窗口错误率(0~1)\n# TYPE translator_llm_error_rate gauge\n")
		sb.WriteString(fmt.Sprintf("translator_llm_error_rate %v\n", s.Engine.ErrorRate()))
	}

	// 平台规模（租户/余额/KB 全平台汇总，仅聚合不泄露租户明细）
	if s.Store != nil {
		if tenants, err := s.Ten.List(); err == nil {
			active := 0
			var totalBalance int64
			for _, t := range tenants {
				// 统计启用租户数并累加各租户余额
				if t.Status == "active" {
					active++
				}
				if b, err := s.Store.GetBalance(t.ID); err == nil {
					totalBalance += b.Balance
				}
			}
			sb.WriteString("# HELP translator_tenants_active 启用租户数\n# TYPE translator_tenants_active gauge\n")
			sb.WriteString(fmt.Sprintf("translator_tenants_active %d\n", active))
			sb.WriteString("# HELP translator_balance_tokens_total 平台余额总 token\n# TYPE translator_balance_tokens_total gauge\n")
			sb.WriteString(fmt.Sprintf("translator_balance_tokens_total %d\n", totalBalance))
		}
		// 知识库条目总数（全平台汇总）
		if s.DB != nil {
			if total, _, _, err := s.DB.Stats(0); err == nil {
				sb.WriteString("# HELP translator_kb_entries_total 知识库条目总数\n# TYPE translator_kb_entries_total gauge\n")
				sb.WriteString(fmt.Sprintf("translator_kb_entries_total %d\n", total))
			}
		}
	}

	return sb.String()
}

// handleMetrics 导出 Prometheus 指标接口（/metrics）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。无鉴权（监控探针），仅暴露聚合指标不涉及租户数据。
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// 输出 Prometheus 文本格式（指定版本 0.0.4）
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(s.metricsText()))
}
