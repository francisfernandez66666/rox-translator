// ============ system_values.go（assist/api）· 职责说明 ============
// 管理台「系统现值」读数口：GET /api/assist/admin/system-values。
//
// ★ 081x（2026-09-29）为什么要专门开这个读数口：
//
//	现值注入是一条**软路径**——取不到就整段不拼进 prompt（这是刻意的：宁可不说数，
//	也不说旧数）。但软路径的失败界面上完全看不出来：挂件照样在答，只是价格/语种数
//	又回到知识条目里的旧口径，运维以为「已经按现值走了」。这与本仓反复踩过的
//	「降级链把死分支兜住」（09-28 PDF 原版式链 100% 死着、产物仍能打开）同形。
//	所以取数结果必须在管理台露一次面：拨的哪个地址、缓存里现在是什么、多旧、
//	现取一次拿到什么。全只读，不改任何配置。
//
// ★ 口径：
//   - 只读、只有 GET；非 GET 回 405。
//   - 不出主服务凭据：拨的两个口本来就是匿名公开口，这里也没有任何密钥可泄。
//   - fresh 段带 3s 级超时预算（engine 侧每条 1.5s），点一次按钮最慢约 3 秒。
//
// =============================================
package api

import (
	"net/http"
	"time"
)

// handleSystemValues GET /api/assist/admin/system-values → 现值接线体检（只读）
// 返回：base_url 生效地址、cached 当前对话真正会用到的那段、cached_age_sec 距上次取数多久、
// fresh 现取一次的结果。任一为空串＝该项没取到（软路径按设计整段省略，不兜旧值）。
func (s *Server) handleSystemValues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": "method"})
		return
	}
	cached, at := s.eng.SystemValuesCached()
	ageSec := -1.0
	if !at.IsZero() {
		ageSec = time.Since(at).Seconds()
	}
	start := time.Now()
	fresh := s.eng.SystemValuesSnapshot(r.Context())
	writeJSON(w, 200, map[string]any{
		"base_url":       s.eng.MainBaseURL(),
		"cached":         cached,
		"cached_age_sec": ageSec,
		"fresh":          fresh,
		// ok 判据用 fresh：现取这一段有没有值，就是「客户此刻能不能拿到实时价格/语种数」的真相
		"ok": fresh != "",
		"ms": time.Since(start).Milliseconds(),
	})
}
