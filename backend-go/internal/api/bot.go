// ============ bot.go · 职责说明 ============
// 群机器人通知（第四批·渠道触达）：把关键业务事件推送到企业微信群/钉钉群/Slack/Teams。
// ★ 改造 4（2026-09-17）：推送实现整体下沉 internal/notify 包（orchestrator 评估
//
//	不合格处置等非 api 层也需推送），本文件保留 Server.notifyBots 方法作薄委托，
//	api 层既有调用点（watchdog/memleak/s9_alerts）零改动。
//
// =============================================
package api

import (
	"translator/internal/notify"
)

// notifyBots 向全部已配置的群机器人推送一条文本消息（fire-and-forget）。
// 参数 title: 消息标题；body: 正文内容。四个渠道均未配置时不发起任何请求。
func (s *Server) notifyBots(title, body string) {
	notify.Bots(s.Store, title, body)
}
