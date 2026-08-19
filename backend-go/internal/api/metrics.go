package api

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

// Metrics 进程内计数器（原子递增，/metrics 时快照输出）
type Metrics struct {
	// HTTP 请求计数：path → 计数
	httpReqs map[string]*int64
	// 翻译成功/失败计数：kind(text/file/openapi/ticket) → count
	translationsOK   map[string]*int64
	translationsFail map[string]*int64
	// 累计计量 token
	usageTokens int64
	startedAt   time.Time
}

// newMetrics 初始化指标收集器
func newMetrics() *Metrics {
	return &Metrics{
		httpReqs:         map[string]*int64{},
		translationsOK:   map[string]*int64{},
		translationsFail: map[string]*int64{},
		startedAt:        time.Now(),
	}
}

// countHTTP 记录一次 HTTP 请求（按路径标签）
func (m *Metrics) countHTTP(path string) {
	if m == nil {
		return
	}
	v, ok := m.httpReqs[path]
	if !ok {
		v = new(int64)
		m.httpReqs[path] = v
	}
	atomic.AddInt64(v, 1)
}

// countTranslate 记录一次翻译结果
func (m *Metrics) countTranslate(kind string, ok bool) {
	if m == nil {
		return
	}
	var v *int64
	var m2 map[string]*int64
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

// addUsage 累加计量 token（成本核算口径）
func (m *Metrics) addUsage(tokens int64) {
	if m == nil || tokens <= 0 {
		return
	}
	atomic.AddInt64(&m.usageTokens, tokens)
}

// metricsText 生成 Prometheus 文本格式指标快照
func (s *Server) metricsText() string {
	var sb strings.Builder
	now := time.Now()
	m := s.metrics
	if m == nil {
		return ""
	}
	sb.WriteString("# HELP translator_info 版本与启动信息\n")
	sb.WriteString("# TYPE translator_info gauge\n")
	sb.WriteString(fmt.Sprintf("translator_info{version=\"2.0.0-go\"} 1\n"))
	sb.WriteString(fmt.Sprintf("# HELP translator_uptime_seconds 服务运行秒数\n# TYPE translator_uptime_seconds gauge\ntranslator_uptime_seconds %d\n", int(now.Sub(m.startedAt).Seconds())))

	// 运行时指标
	sb.WriteString("# HELP go_goroutines 当前 goroutine 数\n# TYPE go_goroutines gauge\n")
	sb.WriteString(fmt.Sprintf("go_goroutines %d\n", runtime.NumGoroutine()))
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	sb.WriteString("# HELP go_heap_bytes Go 堆内存字节\n# TYPE go_heap_bytes gauge\n")
	sb.WriteString(fmt.Sprintf("go_heap_bytes %d\n", mem.HeapAlloc))
	sb.WriteString("# HELP go_alloc_bytes 累计分配字节\n# TYPE go_alloc_bytes counter\n")
	sb.WriteString(fmt.Sprintf("go_alloc_bytes %d\n", mem.TotalAlloc))

	// HTTP 请求
	sb.WriteString("# HELP translator_http_requests_total HTTP 请求总数\n# TYPE translator_http_requests_total counter\n")
	paths := make([]string, 0, len(m.httpReqs))
	for p := range m.httpReqs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sb.WriteString(fmt.Sprintf("translator_http_requests_total{path=%q} %d\n", p, atomic.LoadInt64(m.httpReqs[p])))
	}

	// 翻译计数
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

	// 计量 token
	sb.WriteString("# HELP translator_usage_tokens_total 累计计量 token（成本口径）\n# TYPE translator_usage_tokens_total counter\n")
	sb.WriteString(fmt.Sprintf("translator_usage_tokens_total %d\n", atomic.LoadInt64(&m.usageTokens)))

	// LLM 健康
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

	// 平台规模（租户/余额/KB 全平台汇总）
	if s.Store != nil {
		if tenants, err := s.Ten.List(); err == nil {
			active := 0
			var totalBalance int64
			for _, t := range tenants {
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
		if s.DB != nil {
			if total, _, _, err := s.DB.Stats(0); err == nil {
				sb.WriteString("# HELP translator_kb_entries_total 知识库条目总数\n# TYPE translator_kb_entries_total gauge\n")
				sb.WriteString(fmt.Sprintf("translator_kb_entries_total %d\n", total))
			}
		}
	}

	return sb.String()
}

// handleMetrics 导出 Prometheus 指标（监控探针无鉴权，仅暴露聚合指标不涉及租户数据）
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(s.metricsText()))
}