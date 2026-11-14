// ============ client.go · 职责说明 ============
// 四期 W3-4/W3-5：主服务侧知识服务 HTTP 客户端（§4.3.4 / KN4 / KN7）。
//
//   - 四档失败语义由 DecideReadOutcome 单一真值定义（KN4 两条腿互不覆盖）：
//     ① 影子期（KNOWLEDGE_SERVICE_READ=off）服务不可达 ⇒ 回落直读 + WARN（仅 W3-4）；
//     ② 切读后（=on）服务不可达 ⇒ 503 + WARN，**不许**退化成空结果/静默降级；
//     ③ 可达但未接管契约（W3 期 entries/query）⇒ 503 + reason=not_owned_until_w5；
//     ④ 业务性空结果 ⇒ 200 + 空数组。
//     ⚠️ ② 与 ④ 必须不同形：把不可达写成空结果会与「业务空」合形（AGENTS §一·12 静默降级）。
//   - 超时预算 RequestBudget（KN7）：Call 用 context.WithTimeout 落实，http.Client 同档兜底。
//   - 客户端与服务端只许引用 internal/knowledge/contract 这一份 DTO（KN1）。
//   - 日志走 internal/observability，禁止标准库 log.Printf。
//
// ================================================
package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"translator/internal/knowledge/contract"
	"translator/internal/observability"
)

// RequestBudget 单次知识服务调用的超时预算（KN7）。
// ★ 现算常量，不写死分子：latency_gate_test.go 从本变量现读；调成 0 ⇒ 判据红（M0289）。
var RequestBudget = 800 * time.Millisecond

// Outcome 四档失败语义的机器判据（KN4 单一真值）。
type Outcome struct {
	HTTPStatus       int    // 主服务 handler 应出的状态
	FallbackToDirect bool   // ① 影子期回落直读
	Warn             bool   // 需要 WARN + 报警
	EmptyArray       bool   // ④ 业务性空结果（出栈空数组）
	Reason           string // ③ 未接管 / ② 不可达 的原因码
}

// DecideReadOutcome 四档失败语义的单一真值（KN4）。
// 参数：serviceRead=切读开关；reachable=服务是否可达；owned=该契约 W3 期是否接管；businessEmpty=业务空。
// 返回：该档应有的出栈形态（四档互不覆盖）。
func DecideReadOutcome(serviceRead, reachable, owned, businessEmpty bool) Outcome {
	if !reachable {
		if !serviceRead {
			// ① 影子读（W3-4 专用）：不可达 ⇒ 回落直读 + WARN。
			return Outcome{HTTPStatus: http.StatusOK, FallbackToDirect: true, Warn: true}
		}
		// ② 切读后不可达：必须 503 + 报警，绝不回落、绝不假装空结果（M0316 单独再验这一腿）。
		return Outcome{HTTPStatus: http.StatusServiceUnavailable, Warn: true, Reason: "knowledge_service_unavailable"}
	}
	if !owned {
		// ③ 可达但未接管（W3 期 entries/query）：503 + reason，不给空结果。
		return Outcome{HTTPStatus: http.StatusServiceUnavailable, Reason: contract.ReasonNotOwnedUntilW5}
	}
	if businessEmpty {
		// ④ 业务性空结果：200 + 空数组（与 ② 的 503 不同形）。
		return Outcome{HTTPStatus: http.StatusOK, EmptyArray: true}
	}
	return Outcome{HTTPStatus: http.StatusOK}
}

// Client 主服务侧知识客户端。只连 KNOWLEDGE_BASE_URL（127.0.0.1:8791），带 X-Knowledge-Token。
type Client struct {
	BaseURL     string
	Token       string
	ServiceRead bool // KNOWLEDGE_SERVICE_READ=on ⇒ 切读（② 生效）；off ⇒ 影子期（① 生效）
	HTTPClient  *http.Client
	// DirectRead 影子期回落直读回调（W3-4）。切读后（W3-5）应为 nil：不可达必须 503，不许回落。
	DirectRead func(ctx context.Context, path string, req any) (any, error)
}

// NewClient 从启动期配置构造客户端（超时预算与 http.Client 同档）。
func NewClient(cfg Config) *Client {
	return &Client{
		BaseURL:     cfg.BaseURL,
		Token:       cfg.Token,
		ServiceRead: cfg.ServiceRead,
		HTTPClient:  &http.Client{Timeout: RequestBudget},
	}
}

// ClientFace KN1 三方等值：客户端只引用契约包规范面，禁止自定义 DTO/路径/枚举。
// ★ 改动本函数让它多一个字段/路径（M0283）⇒ contract_gate_test.go 三方等值判红。
func ClientFace() contract.Face { return contract.CanonicalFace() }

// Call 走一次知识服务读，按 DecideReadOutcome 分档落实失败语义与超时预算。
// 参数：ctx=请求上下文；path=契约路径；req=请求 DTO；out=响应 DTO 指针（可为 nil）。
// 返回：Outcome（handler 据 HTTPStatus 出栈）与错误（网络/解码等真错误）。
func (c *Client) Call(ctx context.Context, path string, req, out any) (Outcome, error) {
	// ③ W3 期 entries/query 未接管：即便服务可达也只回 503+reason，绝不打假空结果。
	if path == contract.PathEntriesQuery {
		return DecideReadOutcome(c.ServiceRead, true, false, false), nil
	}

	// KN7：超时预算落到这一次调用（context + http.Client 双档）。
	callCtx, cancel := context.WithTimeout(ctx, RequestBudget)
	defer cancel()

	body, err := json.Marshal(req)
	if err != nil {
		return Outcome{}, fmt.Errorf("knowledge: 请求序列化失败: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return Outcome{}, fmt.Errorf("knowledge: 构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		httpReq.Header.Set("X-Knowledge-Token", c.Token)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		o := DecideReadOutcome(c.ServiceRead, false, true, false)
		if o.Warn {
			observability.Warn(callCtx, "知识服务不可达",
				"path", path, "service_read", c.ServiceRead, "outcome", o.HTTPStatus, "err", err)
		}
		if o.FallbackToDirect {
			if c.DirectRead == nil {
				return o, fmt.Errorf("knowledge: 影子期回落直读回调未配置（不可静默降级为空）")
			}
			if _, derr := c.DirectRead(callCtx, path, req); derr != nil {
				return o, fmt.Errorf("knowledge: 影子期回落直读失败: %w", derr)
			}
		}
		return o, nil // ② 的 503 由 handler 按 o.HTTPStatus 出栈，绝不在此假装空结果
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		var er contract.ErrorResponse
		_ = json.NewDecoder(resp.Body).Decode(&er)
		o := DecideReadOutcome(c.ServiceRead, true, false, false)
		if er.Reason != "" {
			o.Reason = er.Reason
		}
		return o, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Outcome{}, fmt.Errorf("knowledge: 服务返回非预期状态 %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Outcome{}, fmt.Errorf("knowledge: 读响应失败: %w", err)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return Outcome{}, fmt.Errorf("knowledge: 响应反序列化失败: %w", err)
		}
	}
	return DecideReadOutcome(c.ServiceRead, true, true, false), nil
}

// I18nByTenantLang 批量取该租户该语种全部事实译文（带 fact_id），
// 供主服务 W1 热路径「一次取齐、按 fact_id 匹配」。
// 返回：译文响应、四档失败语义 Outcome、网络/解码错误。不可达且在切读模式下，out 为空、o.HTTPStatus=503（fail-closed）。
func (c *Client) I18nByTenantLang(ctx context.Context, tid int64, lang string) (contract.I18nByTenantLangResponse, Outcome, error) {
	var out contract.I18nByTenantLangResponse
	o, err := c.Call(ctx, contract.PathI18nByTenantLang, contract.I18nByTenantLangRequest{TenantID: tid, Lang: lang}, &out)
	return out, o, err
}

// FactsListResponse 通用事实列表读口响应（契约端 hRead 返回 items 为列名键的 map 数组）。
type FactsListResponse struct {
	Items []map[string]any `json:"items"`
}

// FactsList 取该租户（可选 product_id 过滤）全部事实行（列名键 map），主服务据 FactRow 结构解码。
// 返回：事实列表（已解码为 contract.FactRow）、四档失败语义 Outcome、网络/解码错误。
func (c *Client) FactsList(ctx context.Context, tid int64, productID string) ([]contract.FactRow, Outcome, error) {
	req := map[string]any{"tenant_id": tid}
	if strings.TrimSpace(productID) != "" {
		req["product_id"] = strings.TrimSpace(productID)
	}
	var raw FactsListResponse
	o, err := c.Call(ctx, contract.PathFactsList, req, &raw)
	if err != nil {
		return nil, o, err
	}
	// 列名键 map → contract.FactRow（json tag 对齐列名，零 LLM、零跨域）。
	b, _ := json.Marshal(raw.Items)
	var rows []contract.FactRow
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, o, fmt.Errorf("knowledge: FactsList 解码失败: %w", err)
	}
	return rows, o, nil
}
