// ============ webhooks.go · 职责说明 ============
// store 包 Webhook 回调数据访问层与投递逻辑。
//   - webhooks 表：租户配置回调 URL / 签名密钥 / 订阅事件 / 启用状态
//   - store 层 CRUD：UpsertWebhook / ListWebhooks / DeleteWebhook / GetEnabledWebhooks
//   - 投递：DispatchWebhook 对翻译完成事件异步 POST 到回调 URL（HMAC-SHA256 签名，
//     失败重试 3 次，指数退避），供客户 TMS / CI 集成。
// =============================================
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"translator/internal/db"
)

// Webhook 租户回调配置记录
type Webhook struct {
	ID             int64  `json:"id"`              // 主键 ID
	TenantID       int64  `json:"tenant_id"`       // 所属租户
	URL            string `json:"url"`             // 回调 URL
	Secret         string `json:"secret"`          // 签名密钥（HMAC-SHA256）
	Events         string `json:"events"`          // 订阅事件（逗号分隔，默认 translation.completed）
	Enabled        int    `json:"enabled"`         // 1=启用 0=停用
	MaxRetries     int    `json:"max_retries"`     // 最大重试次数（默认 3）
	RetryInterval  int    `json:"retry_interval"`  // 重试间隔秒数（默认 60）
	LastDeliveryAt string `json:"last_delivery_at"` // 最近投递时间
	FailureCount   int    `json:"failure_count"`   // 连续失败次数
	CreatedAt      string `json:"created_at"`      // 创建时间
	UpdatedAt      string `json:"updated_at"`      // 更新时间
}

// WebhookDelivery 投递历史记录（死信队列）
type WebhookDelivery struct {
	ID           int64  `json:"id"`             // 主键 ID
	WebhookID    int64  `json:"webhook_id"`     // 关联 webhook 配置 ID
	TenantID     int64  `json:"tenant_id"`      // 所属租户
	Event        string `json:"event"`          // 事件名
	Payload      string `json:"payload"`        // 事件负载 JSON
	Status       string `json:"status"`         // pending/success/failed/dead
	StatusCode   int    `json:"status_code"`    // HTTP 响应码
	Response     string `json:"response"`       // 响应体（截断 1KB）
	Attempts     int    `json:"attempts"`       // 已尝试次数
	MaxRetries   int    `json:"max_retries"`    // 最大重试次数
	NextRetryAt  string `json:"next_retry_at"`  // 下次重试时间（空=不再重试）
	Error        string `json:"error"`          // 最后错误信息
	CreatedAt    string `json:"created_at"`     // 创建时间
	UpdatedAt    string `json:"updated_at"`     // 更新时间
}

// webhookCols webhooks 表通用查询列（避免各查询重复书写）
const webhookCols = "id, tenant_id, url, secret, events, enabled, COALESCE(max_retries,3), COALESCE(retry_interval,60), COALESCE(last_delivery_at,''), COALESCE(failure_count,0), COALESCE(created_at,''), COALESCE(updated_at,'')"

// deliveryCols webhook_deliveries 表通用查询列
const deliveryCols = "id, webhook_id, tenant_id, event, payload, status, status_code, response, attempts, max_retries, COALESCE(next_retry_at,''), COALESCE(error,''), COALESCE(created_at,''), COALESCE(updated_at,'')"

// UpsertWebhook 新增或更新租户 webhook 配置。
// 参数：w=webhook 配置（ID<=0 时新增，否则按 ID+租户更新）。
//
// ★ SSRF 防护（2026-08-26 P1-f 止血）：保存时即校验 URL 合法性——
//
//	仅允许 http/https 且解析结果不得命中内网/环回/链路本地地址
//	（此前零校验，租户管理员可配置 169.254.169.254 云元数据等内网地址探测）。
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
	if err := validateWebhookURL(w.URL); err != nil {
		return err
	}
	if w.MaxRetries <= 0 {
		w.MaxRetries = 3
	}
	if w.RetryInterval <= 0 {
		w.RetryInterval = 60
	}
	now := time.Now().Format(time.RFC3339)
	if w.ID > 0 {
		// 更新已有记录
		_, err := db.Exec(s.db, db.CurrentDialect(),
			"UPDATE webhooks SET url=?, secret=?, events=?, enabled=?, max_retries=?, retry_interval=?, updated_at=? WHERE id=? AND tenant_id=?",
			w.URL, w.Secret, w.Events, w.Enabled, w.MaxRetries, w.RetryInterval, now, w.ID, w.TenantID)
		return err
	}
	// 新增默认启用
	if w.Enabled == 0 {
		w.Enabled = 1
	}
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO webhooks (tenant_id, url, secret, events, enabled, max_retries, retry_interval, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)",
		w.TenantID, w.URL, w.Secret, w.Events, w.Enabled, w.MaxRetries, w.RetryInterval, now, now)
	if err != nil {
		return err
	}
	w.ID = id
	w.CreatedAt = now
	w.UpdatedAt = now
	return nil
}

// ListWebhooks 查询租户的 webhook 配置列表。
// 参数：tid=租户 ID；返回 webhook 列表（按 ID 升序）。
func (s *Store) ListWebhooks(tid int64) ([]*Webhook, error) {
	// tid<=0：跨租户全量（超管平台视角聚合）
	q := "SELECT " + webhookCols + " FROM webhooks"
	if tid > 0 {
		q += " WHERE tenant_id=?"
	}
	q += " ORDER BY id"
	var rows *sql.Rows
	var err error
	if tid > 0 {
		rows, err = db.Query(s.db, db.CurrentDialect(), q, tid)
	} else {
		rows, err = db.Query(s.db, db.CurrentDialect(), q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.TenantID, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.MaxRetries, &w.RetryInterval, &w.LastDeliveryAt, &w.FailureCount, &w.CreatedAt, &w.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &w)
	}
	return out, nil
}

// GetEnabledWebhooks 查询启用状态的 webhook（用于事件触发投递）。
// 参数：tid=租户 ID，event=订阅事件；返回匹配的启用 webhook 列表。
func (s *Store) GetEnabledWebhooks(tid int64, event string) ([]*Webhook, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT "+webhookCols+" FROM webhooks WHERE tenant_id=? AND enabled=1 ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(&w.ID, &w.TenantID, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.MaxRetries, &w.RetryInterval, &w.LastDeliveryAt, &w.FailureCount, &w.CreatedAt, &w.UpdatedAt); err != nil {
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
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM webhooks WHERE id=? AND tenant_id=?", id, tid)
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

// validateWebhookURL 回调 URL 安全校验（SSRF 防护核心，2026-08-26 P1-f）。
// 规则：
//
//	① scheme 仅允许 http/https；
//	② 域名必须可解析（DNS 失败视为无效，避免保存死链）；
//	③ 解析出的全部 IP 一旦命中黑名单类别即拒绝：环回(127/::1)、私网(RFC1918/ULA)、
//	   链路本地(169.254 云元数据)、未指定(0.0.0.0)、组播/广播。
func validateWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("回调 URL 格式不合法")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("回调 URL 仅支持 http/https")
	}
	host := u.Hostname() // 去掉端口
	// 纯 IP 形式直接校验；域名先解析再逐 IP 校验（防 DNS 指向内网）
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		resolved, err := net.LookupHost(host)
		if err != nil || len(resolved) == 0 {
			return fmt.Errorf("回调域名无法解析: %s", host)
		}
		ips = make([]net.IP, 0, len(resolved))
		for _, s := range resolved {
			if ip := net.ParseIP(s); ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("回调地址不允许指向内网/保留地址: %s", ip.String())
		}
	}
	return nil
}

// postWebhooks 实际投递：对每个 webhook 起 goroutine 发送（带签名与重试）。
//
// ★ 投递前二次校验（2026-08-26 P1-f）：DNS 记录可能在保存后被篡改指向内网
//
//	（DNS rebinding），每次投递前重新执行同一白名单校验，不合法直接跳过该目标。
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
			// 投递前 SSRF 二次校验：保存后 DNS 漂移/内网地址一律跳过
			if validateWebhookURL(hook.URL) != nil {
				return
			}
			maxRetries := hook.MaxRetries
			if maxRetries <= 0 {
				maxRetries = 3
			}
			// 创建投递记录
			deliveryID := s.createDelivery(hook, event, string(body), maxRetries)
			now := time.Now().Format(time.RFC3339)
			// 更新 webhook 最近投递时间
			db.Exec(s.db, db.CurrentDialect(), "UPDATE webhooks SET last_delivery_at=?, updated_at=? WHERE id=?", now, now, hook.ID)

			for attempt := 1; attempt <= maxRetries; attempt++ {
				req, err := http.NewRequest(http.MethodPost, hook.URL, strings.NewReader(string(body)))
				if err != nil {
					s.updateDeliveryStatus(deliveryID, "failed", 0, "", attempt, err.Error())
					s.incrementFailureCount(hook.ID)
					return
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-Event", event)
				req.Header.Set("X-Delivery-ID", fmt.Sprintf("%d", deliveryID))
				if sig := SignWebhook(body, hook.Secret); sig != "" {
					req.Header.Set("X-Signature", "sha256="+sig)
				}
				resp, err := client.Do(req)
				if err == nil {
					respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					resp.Body.Close()
					// 2xx 视为投递成功
					if resp.StatusCode >= 200 && resp.StatusCode < 300 {
						s.updateDeliveryStatus(deliveryID, "success", resp.StatusCode, string(respBody), attempt, "")
						s.resetFailureCount(hook.ID)
						return
					}
					// 非 2xx 记录状态码
					if attempt == maxRetries {
						s.updateDeliveryStatus(deliveryID, "dead", resp.StatusCode, string(respBody), attempt, fmt.Sprintf("HTTP %d", resp.StatusCode))
						s.incrementFailureCount(hook.ID)
						return
					}
					s.updateDeliveryAttempt(deliveryID, attempt, resp.StatusCode, string(respBody), "")
				} else {
					if attempt == maxRetries {
						s.updateDeliveryStatus(deliveryID, "dead", 0, "", attempt, err.Error())
						s.incrementFailureCount(hook.ID)
						return
					}
					s.updateDeliveryAttempt(deliveryID, attempt, 0, "", err.Error())
				}
				// 失败等待后重试（指数退避：1s/3s/7s...）
				if attempt < maxRetries {
					backoff := time.Duration(1<<uint(attempt)) * time.Second
					// 计算下次重试时间
					nextRetry := time.Now().Add(backoff).Format(time.RFC3339)
					db.Exec(s.db, db.CurrentDialect(), "UPDATE webhook_deliveries SET next_retry_at=?, updated_at=? WHERE id=?", nextRetry, time.Now().Format(time.RFC3339), deliveryID)
					time.Sleep(backoff)
				}
			}
		}()
	}
}

// createDelivery 创建投递记录，返回新记录 ID
func (s *Store) createDelivery(hook *Webhook, event, payload string, maxRetries int) int64 {
	now := time.Now().Format(time.RFC3339)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO webhook_deliveries (webhook_id, tenant_id, event, payload, status, attempts, max_retries, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)",
		hook.ID, hook.TenantID, event, payload, "pending", 0, maxRetries, now, now)
	if err != nil {
		return 0
	}
	return id
}

// updateDeliveryStatus 更新投递记录最终状态
func (s *Store) updateDeliveryStatus(id int64, status string, statusCode int, response string, attempts int, errMsg string) {
	now := time.Now().Format(time.RFC3339)
	db.Exec(s.db, db.CurrentDialect(),
		"UPDATE webhook_deliveries SET status=?, status_code=?, response=?, attempts=?, error=?, updated_at=? WHERE id=?",
		status, statusCode, response, attempts, errMsg, now, id)
}

// updateDeliveryAttempt 更新投递尝试中间状态
func (s *Store) updateDeliveryAttempt(id int64, attempts, statusCode int, response, errMsg string) {
	now := time.Now().Format(time.RFC3339)
	db.Exec(s.db, db.CurrentDialect(),
		"UPDATE webhook_deliveries SET attempts=?, status_code=?, response=?, error=?, updated_at=? WHERE id=?",
		attempts, statusCode, response, errMsg, now, id)
}

// incrementFailureCount 递增 webhook 连续失败计数
func (s *Store) incrementFailureCount(webhookID int64) {
	db.Exec(s.db, db.CurrentDialect(), "UPDATE webhooks SET failure_count=failure_count+1, updated_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), webhookID)
}

// resetFailureCount 重置 webhook 连续失败计数
func (s *Store) resetFailureCount(webhookID int64) {
	db.Exec(s.db, db.CurrentDialect(), "UPDATE webhooks SET failure_count=0, updated_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), webhookID)
}

// ListDeliveries 查询 webhook 投递历史（按时间倒序）。
// 参数：webhookID=webhook 配置 ID，tid=租户 ID（越权防护），limit=返回条数上限。
func (s *Store) ListDeliveries(webhookID, tid int64, limit int) ([]*WebhookDelivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+deliveryCols+" FROM webhook_deliveries WHERE webhook_id=? AND tenant_id=? ORDER BY id DESC LIMIT ?",
		webhookID, tid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WebhookDelivery
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.TenantID, &d.Event, &d.Payload, &d.Status, &d.StatusCode, &d.Response, &d.Attempts, &d.MaxRetries, &d.NextRetryAt, &d.Error, &d.CreatedAt, &d.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &d)
	}
	return out, nil
}

// GetDelivery 获取单条投递记录（含租户越权校验）。
func (s *Store) GetDelivery(id, tid int64) (*WebhookDelivery, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+deliveryCols+" FROM webhook_deliveries WHERE id=? AND tenant_id=?", id, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, fmt.Errorf("投递记录不存在")
	}
	var d WebhookDelivery
	if err := rows.Scan(&d.ID, &d.WebhookID, &d.TenantID, &d.Event, &d.Payload, &d.Status, &d.StatusCode, &d.Response, &d.Attempts, &d.MaxRetries, &d.NextRetryAt, &d.Error, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}

// RetryDelivery 重试一条失败/死信投递：重新发送原始 payload 到对应 webhook。
func (s *Store) RetryDelivery(deliveryID, tid int64) error {
	d, err := s.GetDelivery(deliveryID, tid)
	if err != nil {
		return err
	}
	if d.Status == "success" {
		return fmt.Errorf("该投递已成功，无需重试")
	}
	// 获取关联的 webhook 配置
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+webhookCols+" FROM webhooks WHERE id=? AND tenant_id=?", d.WebhookID, tid)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return fmt.Errorf("关联 webhook 配置不存在")
	}
	var w Webhook
	if err := rows.Scan(&w.ID, &w.TenantID, &w.URL, &w.Secret, &w.Events, &w.Enabled, &w.MaxRetries, &w.RetryInterval, &w.LastDeliveryAt, &w.FailureCount, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return err
	}
	// 创建新投递记录并立即发送
	maxRetries := w.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 1
	}
	newDeliveryID := s.createDelivery(&w, d.Event, d.Payload, maxRetries)
	now := time.Now().Format(time.RFC3339)
	db.Exec(s.db, db.CurrentDialect(), "UPDATE webhooks SET last_delivery_at=?, updated_at=? WHERE id=?", now, now, w.ID)

	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest(http.MethodPost, w.URL, strings.NewReader(d.Payload))
		if err != nil {
			s.updateDeliveryStatus(newDeliveryID, "failed", 0, "", 1, err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Event", d.Event)
		req.Header.Set("X-Delivery-ID", fmt.Sprintf("%d", newDeliveryID))
		req.Header.Set("X-Retry", "true")
		if sig := SignWebhook([]byte(d.Payload), w.Secret); sig != "" {
			req.Header.Set("X-Signature", "sha256="+sig)
		}
		resp, err := client.Do(req)
		if err == nil {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				s.updateDeliveryStatus(newDeliveryID, "success", resp.StatusCode, string(respBody), 1, "")
				s.resetFailureCount(w.ID)
				return
			}
			s.updateDeliveryStatus(newDeliveryID, "dead", resp.StatusCode, string(respBody), 1, fmt.Sprintf("HTTP %d", resp.StatusCode))
			s.incrementFailureCount(w.ID)
		} else {
			s.updateDeliveryStatus(newDeliveryID, "dead", 0, "", 1, err.Error())
			s.incrementFailureCount(w.ID)
		}
	}()
	return nil
}

// GetDeliveryStats 获取 webhook 投递统计。
func (s *Store) GetDeliveryStats(webhookID, tid int64) (total, success, failed, dead int, err error) {
	err = db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*), COALESCE(SUM(CASE WHEN status='success' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='dead' THEN 1 ELSE 0 END),0) FROM webhook_deliveries WHERE webhook_id=? AND tenant_id=?",
		webhookID, tid).Scan(&total, &success, &failed, &dead)
	return
}
