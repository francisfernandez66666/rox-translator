// ============================================================================
// api/trialcfg.go — ★ C6（2026-09-12）体验额度/计费系数默认值收敛。
// 注册、重发体验、套餐中心展示等原先各写一份 `300000/14/1.5` 兜底，
// 与 ops.DefaultEffective 脱钩（改默认必漏改）。统一经本文件读取。
// ============================================================================
package api

import (
	"strconv"

	"translator/internal/ops"
)

// trialConfig 体验额度生效配置（system_config free_trial_tokens/free_trial_days，
// 缺省回退 ops 默认值单一来源）。
func (s *Server) trialConfig() (tokens int64, days int) {
	tokens = ops.DefaultTrialTokens()
	days = ops.DefaultTrialDays()
	if v, _ := s.Store.GetConfig("free_trial_tokens"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			tokens = x
		}
	}
	if v, _ := s.Store.GetConfig("free_trial_days"); v != "" {
		if x, e := strconv.Atoi(v); e == nil && x > 0 {
			days = x
		}
	}
	return
}
