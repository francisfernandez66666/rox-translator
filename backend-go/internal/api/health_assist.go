// ============ health_assist.go · 职责说明 ============
// 四期 W5 收口（施工图 §4.5.5 验收第 3 条）：/api/health 的 assist 段。
//
// 旧形态：/api/health 只有 sales_facts 与 knowledge 段，即使将来把本地引擎装上了，
// 部署机也**没有任何现网读数**能证明「收敛成功与否」——正是审计 §3.3 点名的缺口。
//
// 三态：
//
//	local ＝本地引擎已装配且 ASSIST_LINK 未显式回落；ready=true；
//	proxy ＝显式 ASSIST_LINK=remote，或本地引擎未装配（handleAssistOpen 回落旧代理）；ready=false；
//	off   ＝显式 ASSIST_LINK=off（运维关停接待面）；ready=false。
//
// ★ 口径同 #42：健康面匿名可达，只出状态词与布尔，绝不出 assist 库路径 / token / 端点清单。
// ================================================
package api

import "os"

// assistHealth /api/health 的 assist 段：收敛模式 + 本地引擎是否就绪。
// 参数 s: 服务实例（本函数不读 s 的字段，保留接收者以便与同族健康段同形）。
// 返回：含 mode/ready 的映射。
func (s *Server) assistHealth() map[string]interface{} {
	mode := "local"
	ready := AssistLocalEngine() != nil
	switch os.Getenv(assistLinkLocalEnv) {
	case "off":
		mode, ready = "off", false
	case "remote":
		mode, ready = "proxy", false
	default:
		if !ready {
			mode = "proxy" // 未装配 ⇒ handleAssistOpen 会回落旧代理，别把「没收敛」写成 local
		}
	}
	return map[string]interface{}{
		"mode":  mode,
		"ready": ready,
	}
}
