// ============ bot.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 群机器人通知（第四批·渠道触达）：把关键业务事件推送到企业微信群/钉钉群。
//   - system_config wecom_webhook_url：企业微信群机器人地址
//   - system_config dingtalk_webhook_url：钉钉群机器人地址
// 未配置的渠道静默跳过；发送异步执行不阻塞主流程；失败仅记日志。
// 接入点：模型熔断/余额耗尽等 critical 告警、订阅到期摘除（watchdog）。
// =============================================

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// notifyBots 向全部已配置的群机器人推送一条文本消息（fire-and-forget）。
// 参数 title: 消息标题；body: 正文内容。两个渠道均未配置时不发起任何请求。
func (s *Server) notifyBots(title, body string) {
	if s.Store == nil {
		return
	}
	// 从系统配置读取企业微信和钉钉群机器人 webhook 地址
	wecom, _ := s.Store.GetConfig("wecom_webhook_url")
	dingtalk, _ := s.Store.GetConfig("dingtalk_webhook_url")
	content := "【能言】" + title + "\n" + body
	// 企业微信渠道：地址非空且非占位符时异步推送
	if u := strings.TrimSpace(wecom); u != "" && u != "0" {
		go postBot(u, map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": content},
		})
	}
	// 钉钉渠道：地址非空且非占位符时异步推送
	if u := strings.TrimSpace(dingtalk); u != "" && u != "0" {
		go postBot(u, map[string]interface{}{
			"msgtype": "text",
			"text":    map[string]string{"content": content},
		})
	}
}

// postBot 同步 POST 一条 JSON 消息到群机器人 webhook（调用方 goroutine 中运行，panic 自愈）。
func postBot(url string, payload map[string]interface{}) {
	defer func() { _ = recover() }() // 渠道通知永不影响主流程
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("[bot] 群机器人推送失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[bot] 群机器人推送返回 %d", resp.StatusCode)
	}
}
