// ============ notify 包 · 职责说明 ============
// 群机器人通知（★ 改造 4，2026-09-17 自 api 包抽出为独立包）：
//   - system_config wecom_webhook_url：企业微信群机器人地址
//   - system_config dingtalk_webhook_url：钉钉群机器人地址
//   - system_config slack_webhook_url：Slack Incoming Webhook
//   - system_config teams_webhook_url：Teams M365 连接器 Incoming Webhook
//
// 未配置的渠道静默跳过；发送异步执行不阻塞主流程；失败仅记日志。
// 接入点：模型熔断/余额耗尽（api/watchdog）、评估质量低于阈值（orchestrator）等。
// =============================================
package notify

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"translator/internal/store"
)

// Bots 向全部已配置的群机器人推送一条文本消息（fire-and-forget）。
// 参数：st=平台存储（读取 webhook 配置），title=消息标题，body=正文内容。
// 存储为 nil 或全渠道未配置时不发起任何请求。
func Bots(st *store.Store, title, body string) {
	if st == nil {
		return
	}
	wecom, _ := st.GetConfig("wecom_webhook_url")
	dingtalk, _ := st.GetConfig("dingtalk_webhook_url")
	slack, _ := st.GetConfig("slack_webhook_url")
	teams, _ := st.GetConfig("teams_webhook_url")
	content := "【能言】" + title + "\n" + body
	// 企业微信渠道：地址非空且非占位符时异步推送
	if u := strings.TrimSpace(wecom); u != "" && u != "0" {
		go post(u, map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": content},
		})
	}
	// 钉钉渠道：地址非空且非占位符时异步推送
	if u := strings.TrimSpace(dingtalk); u != "" && u != "0" {
		go post(u, map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": content},
		})
	}
	// Slack Incoming Webhook：{"text"} 纯文本（自带 mrkdwn 换行）
	if u := strings.TrimSpace(slack); u != "" && u != "0" {
		go post(u, map[string]interface{}{"text": content})
	}
	// Teams（M365 连接器）：MessageCard 卡片格式，themeColor 取告警红
	if u := strings.TrimSpace(teams); u != "" && u != "0" {
		go post(u, map[string]interface{}{
			"@type":      "MessageCard",
			"@context":   "https://schema.org/extensions",
			"themeColor": "D93F3B",
			"title":      "【能言】" + title,
			"text":       body,
		})
	}
}

// post 同步 POST 一条 JSON 消息到群机器人 webhook（调用方 goroutine 中运行，panic 自愈）。
func post(url string, payload map[string]interface{}) {
	defer func() { _ = recover() }() // 渠道通知永不影响主流程
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("[notify] 群机器人推送失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[notify] 群机器人推送返回 %d", resp.StatusCode)
	}
}
