// ============ 本文件职责中文说明 ============
// Webhook 回调数据访问层与投递逻辑：
//   - webhooks 表：租户配置回调 URL / 签名密钥 / 订阅事件 / 启用状态
//   - store 层 CRUD：UpsertWebhook / ListWebhooks / DeleteWebhook / GetEnabledWebhooks
//   - 投递：DispatchWebhook 对翻译完成事件异步 POST 到回调 URL（HMAC-SHA256 签名，
//     失败重试 3 次，指数退避），供客户 TMS / CI 集成。
//
// =============================================
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Webhook 租户回调配置记录
type Webhook struct {
	ID        int64  `json:"id"`         // 主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户
	URL       string `json:"url"`        // 回调 URL
	Secret    string `json:"secret"`     // 签名密钥（HMAC-SHA256）
	Events    string `json:"events"`     // 订阅事件（逗号分隔，默认 translation.completed）
	Enabled   int    `json:"enabled"`    // 1=启用 0=停用
	CreatedAt string `json:"created_at"` // 创建时间
	UpdatedAt string `json:"updated_at"` // 更新时间
}

// webhookCols webhooks 表通用查询列（避免各查询重复书写）
const webhookCols = "id, tenant_id, url, secret, events, enabled, COALESCE(created_at,''), COALESCE(updated_at,'')"

// UpsertWebhook 新增或更新租户 webhook 配置。
// 参数：w=webhook 配置（ID<=0 时新增，否则按 ID+租户更新）。
func (s *Store) UpsertWebhook(w *Webhook) error {
	if w.TenantID <= 0 {
		w.TenantID = 1
	}
	if w.Events == "" {
		w.Events = "translation.completed"
	}
	if w.URL == "" {
		return fmt.Errorf("回调 URL 不能为空")
	}
	now := time.Now().Format(time.RFC3339)
	if w.ID > 0 {
		// 更新已有记录
		_, err := s.db.Exec(
			"UPDATE webhooks SET url=?, secret=?, events=?, enabled=?, updated_at=? WHERE id=? AND tenant_id=?",
			w.URL, w.Secret, w.Events, w.Enabled, now, w.ID, w.TenantID)
		return err
	}
	// 新增默认启用
	if w.Enabled == 0 {
		w.Enabled = 1
	}
	res, err := s.db.Exec(
		"INSERT INTO webhooks (tenant_id, url, secret, events, enabled, created_at, updated_at) VALUES (?,?,?,?,?,?,?)",
		w.TenantID, w.URL, w.Secret, w.Events, w.Enabled, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	w.ID = id
	w.CreatedAt = now
	w.UpdatedAt = now
	return nil
}

// ListWebhooks 查询租户的 webhook 配置列表。
// 参数：tid=租户 ID；返回 webhook 列表（按 ID 升序）。
func (s *Store) ListWebhooks(tid int64) ([]*Webhook, error) {
	rows, err := s.db.Query("SELECT "+webhookCols+" FROM webhooks WHERE tenant_id=? ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.TenantID, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.CreatedAt, &w.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &w)
	}
	return out, nil
}

// GetEnabledWebhooks 查询启用状态的 webhook（用于事件触发投递）。
// 参数：tid=租户 ID，event=订阅事件；返回匹配的启用 webhook 列表。
func (s *Store) GetEnabledWebhooks(tid int64, event string) ([]*Webhook, error) {
	rows, err := s.db.Query("SELECT "+webhookCols+" FROM webhooks WHERE tenant_id=? AND enabled=1 ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.TenantID, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.CreatedAt, &w.UpdatedAt); err != nil {
			continue
		}
		// 事件过滤：event 为空表示不过滤（如测试 ping）；否则逗号分隔订阅列表含目标事件才投递
		if event != "" && w.Events != "" && !containsEvent(w.Events, event) {
			continue
		}
		out = append(out, &w)
	}
	return out, nil
}

// containsEvent 判断事件订阅串（逗号分隔）是否包含目标事件。
func containsEvent(events, target string) bool {
	for _, e := range strings.Split(events, ",") {
		if strings.TrimSpace(e) == target {
			return true
		}
	}
	return false
}

// DeleteWebhook 删除租户的 webhook 配置。
// 参数：id=webhook ID，tid=租户 ID（越权防护）。
func (s *Store) DeleteWebhook(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM webhooks WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// SignWebhook 生成 webhook 请求签名：HMAC-SHA256(body, secret) 的十六进制串。
// 参数：body=请求体原始字节，secret=签名密钥；返回签名串（密钥为空时返回空串）。
func SignWebhook(body []byte, secret string) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// DispatchWebhook 异步投递 webhook 事件到租户配置的回调 URL。
// 实现：goroutine 内 POST 请求，HMAC-SHA256 签名写入 X-Signature 头，失败重试 3 次指数退避。
// 参数：tid=租户 ID，event=事件名，payload=事件负载（将整体作为 body JSON）。
func (s *Store) DispatchWebhook(tid int64, event string, payload interface{}) {
	// 查询启用且订阅该事件的 webhook
	hooks, err := s.GetEnabledWebhooks(tid, event)
	if err != nil || len(hooks) == 0 {
		return
	}
	s.postWebhooks(hooks, event, payload)
}

// DispatchWebhookForce 异步投递 webhook（测试 ping 专用）：忽略事件订阅过滤，
// 仅要求 webhook 启用，用于验证回调端点可达性。
// 参数：tid=租户 ID，event=事件名，payload=事件负载。
func (s *Store) DispatchWebhookForce(tid int64, event string, payload interface{}) {
	hooks, err := s.GetEnabledWebhooks(tid, "")
	if err != nil || len(hooks) == 0 {
		return
	}
	s.postWebhooks(hooks, event, payload)
}

// postWebhooks 实际投递：对每个 webhook 起 goroutine 发送（带签名与重试）。
func (s *Store) postWebhooks(hooks []*Webhook, event string, payload interface{}) {
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second} // 单次投递 10 秒超时
	for _, h := range hooks {
		// 拷贝循环变量（goroutine 延迟执行）
		hook := h
		go func() {
			for attempt := 1; attempt <= 3; attempt++ {
				req, err := http.NewRequest(http.MethodPost, hook.URL, strings.NewReader(string(body)))
				if err != nil {
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Event", event)
				if sig := SignWebhook(body, hook.Secret); sig != "" {
					req.Header.Set("X-Signature", "sha256="+sig)
				}
				resp, err := client.Do(req)
				if err == nil {
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					// 2xx 视为投递成功
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						return
					}
				}
				// 失败等待后重试（指数退避：1s/3s/7s）
				if attempt < 3 {
					time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				}
			}
		}()
	}
}
