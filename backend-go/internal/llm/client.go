// ============ 本文件职责中文说明 ============
// LLM 客户端：OpenAI 兼容 chat/completions 与智谱 embedding-2 嵌入调用。
// 核心能力：全局并发限流（信号量，上限 3 路排队等待）、单次调用超时兜底、
// 429 触发降级模型重试、嵌入向量 L2 归一化（供余弦相似度检索使用）。
// =============================================
package llm

import (
	"crypto/tls"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"translator/internal/config"
)

// MaxLLMConcurrent 全局 LLM 并发上限：超过此数量的调用在信号量处排队等待
const MaxLLMConcurrent = 3

// Client LLM 客户端
type Client struct {
	cfg   *config.Config // 全局配置（模型/温度等参数来源）
	http  *http.Client   // HTTP 客户端（含代理与全局超时）
	proxy *url.URL       // 代理地址（PROXY_URL/HTTPS_PROXY/HTTP_PROXY 环境变量解析结果）

	// ★ 全局共享信号量：所有 LLM API 调用（chat/embed）统一限流，
	// 多用户/多语言并发共享同一上限，超过的调用排队等待，不无限叠加。
	sem chan struct{}

	// 知识库 Embed 阶段覆盖（stage_models.kb_embed；空=用全局 Embed 配置）
	embedBase, embedKey, embedModel string
}

// NewClient 创建客户端：解析代理、设置全局 120s 超时与并发信号量。
// 参数：cfg=全局配置；返回可用的 LLM 客户端。
// SetEmbedOverride 设置知识库 Embed 阶段覆盖端点（stage_models.kb_embed，超管维护）。
func (c *Client) SetEmbedOverride(base, key, model string) {
	c.embedBase, c.embedKey, c.embedModel = base, key, model
}

// NewClient 构造函数：初始化并返回实例。
func NewClient(cfg *config.Config) *Client {
	tr := &http.Transport{
		// ★ 禁用 HTTP/2：siliconflow 偶发 h2 流挂起（roundTrip 2min+ 无响应），
		//   HTTP/1.1 下未观测到该问题；同时缩短整体超时快速失败。
		TLSClientConfig: &tls.Config{NextProtos: []string{"http/1.1"}},
		ForceAttemptHTTP2: false,
	}
	if p := getenvAny("PROXY_URL", "HTTPS_PROXY", "HTTP_PROXY"); p != "" {
		// 配置 HTTP 代理（用于公司内网/受限网络访问 LLM API）
		if u, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{
		cfg: cfg,
		// ★ 全局超时兜底：防止 LLM API 卡住时请求无限挂起
		http: &http.Client{Transport: tr, Timeout: 45 * time.Second},
		// ★ 全局并发信号量（超过 3 路排队等待，不加大并发）
		sem: make(chan struct{}, MaxLLMConcurrent),
	}
}

// acquire 获取并发名额；ctx 取消时排队中的调用立即返回错误。
// 参数：ctx=调用上下文；成功获取返回 nil，ctx 被取消返回 ctx.Err()。
func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil // 取得名额
	case <-ctx.Done():
		return ctx.Err() // 排队中被取消
	}
}

// release 释放并发名额（acquire 成功后的配对调用）。
func (c *Client) release() {
	<-c.sem
}

// getenvAny 依次读取多个环境变量，返回首个非空值。
// 参数：keys=环境变量名列表；返回首个非空环境变量值（全空返回 ""）。
func getenvAny(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(lookupEnv(k)); v != "" {
			return v
		}
	}
	return ""
}

// ChatResponse OpenAI 兼容响应
type ChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"` // 模型返回的文本内容
		} `json:"message"`
		FinishReason string `json:"finish_reason"` // 结束原因（如 stop / length）
	} `json:"choices"`
	Usage Usage `json:"usage"` // 本次调用真实 token 用量（OpenAI 兼容 usage 字段）
}

// Usage 单次 LLM 调用的真实 token 用量（按实际费用计费的数据来源）
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`     // 输入 token 数
	CompletionTokens int64 `json:"completion_tokens"` // 输出 token 数
}

// Total 返回输入+输出合计 token 数。
func (u Usage) Total() int64 { return u.PromptTokens + u.CompletionTokens }

// ============ Token 用量收集器（ctx 传播，计费聚合用） ============

// UsageCollector 并发安全的 token 用量累计器：随 ctx 注入并自动传播到全部下游
// LLM 调用（初翻/校对/Judge/文化闸门/embedding），任务结束时一次性读取汇总值计费。
type UsageCollector struct {
	mu         sync.Mutex // 保护并发累加（多语言并发翻译同时写）
	prompt     int64      // 累计输入 token
	completion int64      // 累计输出 token
}

// Add 累加一次调用的 token 用量。
func (c *UsageCollector) Add(prompt, completion int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.prompt += prompt
	c.completion += completion
	c.mu.Unlock()
}

// Totals 返回累计的（输入, 输出）token 数。
func (c *UsageCollector) Totals() (int64, int64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prompt, c.completion
}

// Total 返回累计的输入+输出合计 token 数。
func (c *UsageCollector) Total() int64 {
	p, n := c.Totals()
	return p + n
}

// usageCollectorKey ctx 存取键（私有类型防碰撞）
type usageCollectorKey struct{}

// WithUsageCollector 向 ctx 注入用量收集器（引擎/API 层在进入翻译前调用）。
func WithUsageCollector(ctx context.Context, uc *UsageCollector) context.Context {
	return context.WithValue(ctx, usageCollectorKey{}, uc)
}

// CollectorFrom 从 ctx 取收集器；未注入返回 nil（调用方判空）。
func CollectorFrom(ctx context.Context) *UsageCollector {
	uc, _ := ctx.Value(usageCollectorKey{}).(*UsageCollector)
	return uc
}

// chatPayload 请求体（map 以便按模型附加参数）
type chatPayload map[string]interface{}

// CallChat 调用 chat/completions，返回 content。baseURL 以 /v1 结尾。
// 参数：baseURL=API 基础地址，apiKey=密钥，model=模型名，messages=对话消息列表，
// maxTokens=最大生成 token 数，hunyuan=true 时附加专有采样参数，fallbackTemp=兜底温度。
// 返回：模型输出内容与 finishReason。
func (c *Client) CallChat(ctx context.Context, baseURL, apiKey, model string, messages []map[string]string,
	maxTokens int, hunyuan bool, fallbackTemp float64) (string, string, error) {

	payload := chatPayload{
		"model":      model,
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	if hunyuan {
		// 混元模型：附加专属采样参数（温度/核采样/惩罚）
		payload["temperature"] = c.cfg.HunyuanTemp
		payload["top_p"] = c.cfg.HunyuanTopP
		payload["top_k"] = c.cfg.HunyuanTopK
		payload["repetition_penalty"] = c.cfg.HunyuanRepetition
	} else {
		payload["temperature"] = fallbackTemp // 通用模型用兜底温度
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	return c.doChat(ctx, endpoint, apiKey, payload)
}

// CallChatFallback 调用并处理 429 → 降级模型。
// 参数：baseURL/apiKey/model=请求配置，messages=消息，maxTokens=最大 token，
// fallbackTemp=温度，onRateLimited=触发 429 时的回调（可选）。
// 返回：content 与 finishReason；429 时自动换 cfg.HunyuanFallbackModel 重试。
func (c *Client) CallChatFallback(ctx context.Context, baseURL, apiKey, model string,
	messages []map[string]string, maxTokens int, fallbackTemp float64,
	onRateLimited func()) (content string, finishReason string, err error) {

	content, finishReason, err = c.CallChat(ctx, baseURL, apiKey, model, messages, maxTokens, false, fallbackTemp)
	if err == nil {
		return // 首次调用成功
	}
	// 限流（HTTP 429）：触发回调、短暂等待后用降级模型重试
	if isRateLimit(err) && onRateLimited != nil {
		onRateLimited()
		time.Sleep(2 * time.Second)
		fallback := c.cfg.HunyuanFallbackModel
		content, finishReason, err = c.CallChat(ctx, baseURL, apiKey, fallback, messages, maxTokens, false, 0.1)
	}
	return
}

// doChat 实际执行 chat 请求：构造请求、单次调用超时、全局并发限流、状态码校验与响应解析。
// 参数：endpoint=完整接口地址，apiKey=密钥，payload=请求体。
// 返回：模型输出内容与 finishReason。
func (c *Client) doChat(ctx context.Context, endpoint, apiKey string, payload chatPayload) (string, string, error) {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// ★ 单次调用超时保护：调用方 ctx 未设超时时默认 120s
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
	}

	// ★ 全局并发限流：超过 3 路并发时排队等待
	if err := c.acquire(ctx); err != nil {
		return "", "", err
	}
	defer c.release()

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	// 状态码分类处理：429 限流、401 密钥无效、其余非 200 返回错误体摘要
	if resp.StatusCode == 429 {
		return "", "", fmt.Errorf("rate_limited: HTTP 429")
	}
	if resp.StatusCode == 401 {
		return "", "", fmt.Errorf("api key 无效 (401)")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", "", fmt.Errorf("LLM API HTTP %d: %s", resp.StatusCode, string(b))
	}

	// 解析 OpenAI 兼容响应（限制读取 4MB 防异常大响应）
	var cr ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&cr); err != nil {
		return "", "", fmt.Errorf("解析响应: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", "", fmt.Errorf("LLM 无 choices")
	}
	// ★ 真实 token 用量归集：ctx 注入收集器时累加（供按实际费用计费）
	if uc := CollectorFrom(ctx); uc != nil {
		uc.Add(cr.Usage.PromptTokens, cr.Usage.CompletionTokens)
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), cr.Choices[0].FinishReason, nil
}

// isRateLimit 判断错误是否为限流（错误消息包含 "429"）。
// 参数：err=待判断错误；返回是否为限流错误。
func isRateLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "429")
}

// EmbedResponse 智谱 embedding 响应
type EmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"` // 嵌入向量（float64 数组）
	} `json:"data"`
	Usage Usage `json:"usage"` // 嵌入调用 token 用量（KB 匹配成本归集）
}

// Embed 单条文本嵌入（智谱 embedding-2，1024 维），返回归一化向量。
// 参数：text=待嵌入文本；返回归一化后的 float32 向量。
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedding 为空")
	}
	return vecs[0], nil
}

// EmbedBatch 批量嵌入，返回归一化向量列表。
// 参数：texts=待嵌入文本列表，batchSize=可选分片大小（默认 32）。
// 返回：归一化向量列表（顺序与输入一致）。
func (c *Client) EmbedBatch(ctx context.Context, texts []string, batchSize ...int) ([][]float32, error) {
	bs := 32
	if len(batchSize) > 0 && batchSize[0] > 0 {
		bs = batchSize[0]
	}
	var out [][]float32
	// 按分片逐批调用（控制单请求大小）
	for i := 0; i < len(texts); i += bs {
		end := i + bs
		if end > len(texts) {
			end = len(texts)
		}
		chunk := texts[i:end]
		vecs, err := c.embedChunk(ctx, chunk)
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// embedChunk 实际执行一批文本的嵌入请求并做 L2 归一化。
// 参数：texts=单批文本；返回归一化向量列表。
func (c *Client) embedChunk(ctx context.Context, texts []string) ([][]float32, error) {
	// 阶段覆盖（kb_embed）：超管在分阶段模型里配置的 Embed 端点优先
	base, key, model := c.cfg.EmbedAPIBase, c.cfg.EmbedAPIKey, "embedding-2"
	if c.embedBase != "" && c.embedModel != "" {
		base, key, model = c.embedBase, c.embedKey, c.embedModel
	}
	payload := map[string]interface{}{
		"model": model,
		"input": texts,
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(base, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	// 嵌入请求单次 60s 超时
	timeout := 60 * time.Second
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(ctx2)

	// ★ 全局并发限流：超过 3 路并发时排队等待
	if err := c.acquire(ctx2); err != nil {
		return nil, err
	}
	defer c.release()

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return nil, fmt.Errorf("embedding HTTP %d: %s", resp.StatusCode, string(b))
	}
	// 解析嵌入响应（限制读取 8MB）
	var er EmbedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&er); err != nil {
		return nil, err
	}
	// ★ embedding token 用量归集（KB 匹配成本计入租户账单）
	if uc := CollectorFrom(ctx2); uc != nil {
		uc.Add(er.Usage.PromptTokens, er.Usage.CompletionTokens)
	}
	// 转 float32 并做 L2 归一化（除以向量模长，便于余弦相似度点积检索）
	out := make([][]float32, len(er.Data))
	for i, d := range er.Data {
		v := make([]float32, len(d.Embedding))
		var sum float64
		for j, x := range d.Embedding {
			f := float32(x)
			v[j] = f
			sum += float64(f) * float64(f) // 累加平方和求模长
		}
		norm := float64(0)
		if sum > 0 {
			norm = sqrtF(sum) // 模长
		}
		if norm > 0 {
			for j := range v {
				v[j] = v[j] / float32(norm) // 归一化：每个分量除以模长
			}
		}
		out[i] = v
	}
	return out, nil
}

// sqrtF 计算平方根（封装 math.Sqrt）。
func sqrtF(x float64) float64 {
	return math.Sqrt(x)
}

// lookupEnv 读取环境变量（封装 os.Getenv）。
func lookupEnv(k string) string {
	return os.Getenv(k)
}
