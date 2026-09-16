// ============================================================================
// ★ H11 SLO/SLI + Burn Rate 告警（Google SRE 多窗口策略）
//
// SLI 采集（每分钟由 watchdog selfcheck 循环驱动 sloSampleTick）：
//
//	① 可用性 availability：selfcheck 探活成败；SLO=99.9%
//	② 翻译成功率 translate_success：countTranslate ok/fail 增量；SLO=99.0%
//	③ 请求延迟 P99 latency_p99：withMetrics 时延环形缓冲分位；SLO≤阈值 ms
//
// Burn Rate = 窗口内错误预算消耗速率 / 预算速率；双窗口确认（快窗+慢窗同时
// 越线才告警）压制毛刺噪音：
//
//	critical: burn(1h)≥2 且 burn(6h)≥2；warning: burn(1h)≥1 且 burn(6h)≥1；
//	恢复即 ResolveAlert（同 kind 去重，噪音目标 <20%）。
//
// 端点：GET /api/admin/ops/slo（超管，面板/巡检可视化）。
// ============================================================================
package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
)

// sloDef 单个 SLO 目标定义。
type sloDef struct {
	Key    string  // 标识（availability / translate_success / latency_p99）
	Name   string  // 中文名
	Target float64 // 目标值：可用性/成功率为达标占比(%)；latency_p99 为上限 ms
	IsPct  bool    // true=百分比型（错误率=budget 消耗）；false=阈值型
}

// sloDefs SLO 目标定义表（可用性 99.9% / 翻译成功率 99% / P99 延迟 8s 上限）。
var sloDefs = []sloDef{
	{"availability", "服务可用性", 99.9, true},
	{"translate_success", "翻译成功率", 99.0, true},
	{"latency_p99", "请求 P99 延迟", 8000, false},
}

// sloSample 一分钟粒度的 SLI 原始增量样本。
type sloSample struct {
	T      time.Time
	Up     bool    // 探活成功
	Totals int64   // 本分钟翻译请求增量（ok+fail）
	Fails  int64   // 失败增量
	P99    float64 // 本分钟请求 P99（ms，无样本时 0）
}

// sloRing SLI 样本环容量：保留 6 小时分钟样本（慢窗口上界）
const sloRing = 6 * 60 // 保留 6 小时分钟样本（慢窗口上界）

// sloState Server 内 SLO 追踪器（惰性初始化）。
type sloState struct {
	mu       sync.Mutex
	ring     [sloRing]sloSample
	pos      int
	filled   int
	lastOK   int64
	lastFail int64
}

// sloSampleTick watchdog 每分钟调用：拉取指标增量并落环形缓冲。
func (s *Server) sloSampleTick(up bool) {
	if s.metrics == nil || s.Store == nil {
		return
	}
	ok, fail := s.metrics.TranslationsTotals()
	dOK, dFail := ok-s.sloLastOK, fail-s.sloLastFail
	if dOK < 0 {
		dOK = 0
	}
	if dFail < 0 {
		dFail = 0
	}
	s.sloLastOK, s.sloLastFail = ok, fail
	if s.slo == nil {
		s.slo = &sloState{}
	}
	st := s.slo
	st.mu.Lock()
	st.ring[st.pos] = sloSample{T: time.Now(), Up: up, Totals: dOK + dFail, Fails: dFail, P99: s.metrics.P99()}
	st.pos = (st.pos + 1) % sloRing
	if st.filled < sloRing {
		st.filled++
	}
	st.mu.Unlock()
	s.sloEvaluate()
}

// sloWindowStat 窗口聚合：错误率与预算消耗率（burn rate）。
type sloWindowStat struct {
	Window  string  `json:"window"`
	Samples int     `json:"samples"`
	ErrRate float64 `json:"err_rate"` // 失败占比 / 超阈值分钟占比
	Burn    float64 `json:"burn"`     // errRate / (1-target)
}

// sloStatus 单 SLO 当前评估快照。
type sloStatus struct {
	Key           string          `json:"key"`
	Name          string          `json:"name"`
	Target        float64         `json:"target"`
	Level         string          `json:"level"` // ok / warning / critical
	Burn1H        float64         `json:"burn_1h"`
	Burn6H        float64         `json:"burn_6h"`
	Stats         []sloWindowStat `json:"stats"`
	BudgetLeftPct float64         `json:"budget_left_pct"` // 30 天预算剩余（近似=6h 视图外推）
}

// sloRateAt 窗口 [now-d, now] 的窗口统计。
func (st *sloState) sloRateAt(def sloDef, d time.Duration, now time.Time) sloWindowStat {
	total, bad, n := int64(0), int64(0), 0
	for i := 0; i < st.filled; i++ {
		sm := st.ring[i]
		if sm.T.IsZero() || now.Sub(sm.T) > d {
			continue
		}
		n++
		switch def.Key {
		case "availability":
			total++
			if !sm.Up {
				bad++
			}
		case "translate_success":
			total += sm.Totals
			bad += sm.Fails
		case "latency_p99":
			if sm.P99 > 0 {
				total++
				if sm.P99 > def.Target {
					bad++
				}
			}
		}
	}
	out := sloWindowStat{Window: d.String(), Samples: n}
	if total > 0 {
		out.ErrRate = float64(bad) / float64(total)
	}
	budget := 1 - def.Target/100 // 百分比型：允许失败占比（如 99.9 → 0.1%）
	if !def.IsPct {
		budget = 0.05 // 阈值型 SLO：允许 5% 的分钟超线
	}
	if budget <= 0 {
		budget = 0.001
	}
	out.Burn = out.ErrRate / budget
	return out
}

// sloEvaluate 计算三 SLO 级别并同步告警（去重 + 自动恢复）。
func (s *Server) sloEvaluate() {
	if s.slo == nil {
		return
	}
	st := s.slo
	st.mu.Lock()
	now := time.Now()
	var statuses []sloStatus
	for _, def := range sloDefs {
		w1h := st.sloRateAt(def, time.Hour, now)
		w6h := st.sloRateAt(def, 6*time.Hour, now)
		lvl := "ok"
		if w1h.Samples >= 3 && w1h.Burn >= 1 && w6h.Burn >= 1 {
			lvl = "warning"
		}
		if w1h.Samples >= 3 && w1h.Burn >= 2 && w6h.Burn >= 2 {
			lvl = "critical"
		}
		budget30d := 1 - def.Target/100
		if !def.IsPct {
			budget30d = 0.05
		}
		left := 100.0
		if budget30d > 0 && w6h.ErrRate > 0 {
			left = 100 * (1 - w6h.ErrRate/budget30d)
			if left < 0 {
				left = 0
			}
		}
		statuses = append(statuses, sloStatus{
			Key: def.Key, Name: def.Name, Target: def.Target, Level: lvl,
			Burn1H: round3(w1h.Burn), Burn6H: round3(w6h.Burn),
			Stats: []sloWindowStat{w1h, w6h}, BudgetLeftPct: round1(left),
		})
	}
	st.mu.Unlock()
	s.sloAlertSync(statuses)
}

// sloAlertSync 按级别开/关告警（同 kind 仅一条 open，恢复自动 resolve）。
func (s *Server) sloAlertSync(statuses []sloStatus) {
	for _, ss := range statuses {
		kind := "slo_" + ss.Key
		open, id := s.openAlertOfKind(kind)
		if ss.Level == "ok" {
			if open && id > 0 {
				_ = s.Store.ResolveAlert(id)
			}
			continue
		}
		if open {
			continue // 已告警：升级级别也先静默（降噪音，一个窗口周期内不重复刷）
		}
		level := "warning"
		if ss.Level == "critical" {
			level = "critical"
		}
		_ = s.Store.CreateAlert(0, level, kind, fmt.Sprintf(
			"SLO %s 燃烧率越限：1h=%.2f 6h=%.2f（目标 %v，预算剩余 %.1f%%）",
			ss.Name, ss.Burn1H, ss.Burn6H, ss.Target, ss.BudgetLeftPct))
	}
}

// openAlertOfKind 查询平台级未关闭告警（kind 精确匹配）。
func (s *Server) openAlertOfKind(kind string) (bool, int64) {
	list, err := s.Store.ListAlerts(0, "open", 200)
	if err != nil {
		return false, 0
	}
	for _, a := range list {
		if a.Kind == kind {
			return true, a.ID
		}
	}
	return false, 0
}

// handleOpsSLO GET /api/admin/ops/slo —— 当前 SLI/燃烧率/预算快照（超管可视化）。
func (s *Server) handleOpsSLO(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil || !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可查看 SLO 状态"})
		return
	}
	st := s.slo
	if st == nil {
		writeJSON(w, 200, map[string]interface{}{"success": true, "slos": []sloStatus{}, "note": "采样未启动（等待 watchdog 首轮）"})
		return
	}
	s.sloEvaluate() // 强制刷新级别
	st.mu.Lock()
	var statuses []sloStatus
	now := time.Now()
	for _, def := range sloDefs {
		w1 := st.sloRateAt(def, time.Hour, now)
		w6 := st.sloRateAt(def, 6*time.Hour, now)
		statuses = append(statuses, sloStatus{Key: def.Key, Name: def.Name, Target: def.Target,
			Burn1H: round3(w1.Burn), Burn6H: round3(w6.Burn), Stats: []sloWindowStat{w1, w6}})
	}
	samples := st.filled
	st.mu.Unlock()
	writeJSON(w, 200, map[string]interface{}{"success": true, "slos": statuses, "samples": samples, "ring_minutes": sloRing})
}

// round3 四舍五入保留 3 位小数。
func round3(v float64) float64 { return float64(int(v*1000+0.5)) / 1000 }

// round1 四舍五入保留 1 位小数。
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// handleOpsRoutes ★ H7 GET /api/admin/ops/routes —— 路由实时统计
// （P50/P95/错误率/单位 token 成本 + 动态权重开关状态），供后台可视化。
func (s *Server) handleOpsRoutes(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil || !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可查看路由统计"})
		return
	}
	var routes []engine.RouteStatView
	if s.Engine != nil {
		routes = s.Engine.RouteStats()
	}
	if routes == nil {
		routes = []engine.RouteStatView{}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "routes": routes,
		"dynamic_routing": config.C.DynamicRouting, "hedge_enabled": config.C.HedgeEnabled,
	})
}
