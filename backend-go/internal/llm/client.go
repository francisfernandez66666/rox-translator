package llm

import (
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
	"time"

	"translator/internal/config"
)

// MaxLLMConcurrent 全局 LLM 并发上限：超过此数量的调用在信号量处排队等待
const MaxLLMConcurrent = 3

// Client LLM 客户端
type Client struct {
	cfg   *config.Config
	http  *http.Client
	proxy *url.URL

	// ★ 全局共享信号量：所有 LLM API 调用（chat/embed）统一限流，
	// 多用户/多语言并发共享同一上限，超过的调用排队等待，不无限叠加。
	sem chan struct{}
}

// NewClient 创建客户端
func NewClient(cfg *config.Config) *Client {
	tr := &http.Transport{}
	if p := getenvAny("PROXY_URL", "HTTPS_PROXY", "HTTP_PROXY"); p != "" {
		if u, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{
		cfg: cfg,
		// ★ 全局超时兜底：防止 LLM API 卡住时请求无限挂起
		http: &http.Client{Transport: tr, Timeout: 120 * time.Second},
		// ★ 全局并发信号量（超过 3 路排队等待，不加大并发）
		sem: make(chan struct{}, MaxLLMConcurrent),
	}
}

// acquire 获取并发名额；ctx 取消时排队中的调用立即返回错误
func (c *Client) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) release() {
	<-c.sem
}

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
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// chatPayload 请求体（map 以便按模型附加参数）
type chatPayload map[string]interface{}

// CallChat 调用 chat/completions，返回 content。baseURL 以 /v1 结尾。
// hunyuan=true 时附加专有采样参数；返回 finishReason。
func (c *Client) CallChat(ctx context.Context, baseURL, apiKey, model string, messages []map[string]string,
	maxTokens int, hunyuan bool, fallbackTemp float64) (string, string, error) {

	payload := chatPayload{
		"model":      model,
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	if hunyuan {
		payload["temperature"] = c.cfg.HunyuanTemp
		payload["top_p"] = c.cfg.HunyuanTopP
		payload["top_k"] = c.cfg.HunyuanTopK
		payload["repetition_penalty"] = c.cfg.HunyuanRepetition
	} else {
		payload["temperature"] = fallbackTemp
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	return c.doChat(ctx, endpoint, apiKey, payload)
}

// CallChatFallback 调用并处理 429 → 降级模型
// onRetry 在 429 触发时被调用（可选）
func (c *Client) CallChatFallback(ctx context.Context, baseURL, apiKey, model string,
	messages []map[string]string, maxTokens int, fallbackTemp float64,
	onRateLimited func()) (content string, finishReason string, err error) {

	content, finishReason, err = c.CallChat(ctx, baseURL, apiKey, model, messages, maxTokens, false, fallbackTemp)
	if err == nil {
		return
	}
	if isRateLimit(err) && onRateLimited != nil {
		onRateLimited()
		time.Sleep(2 * time.Second)
		fallback := c.cfg.HunyuanFallbackModel
		content, finishReason, err = c.CallChat(ctx, baseURL, apiKey, fallback, messages, maxTokens, false, 0.1)
	}
	return
}

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

	var cr ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&cr); err != nil {
		return "", "", fmt.Errorf("解析响应: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", "", fmt.Errorf("LLM 无 choices")
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), cr.Choices[0].FinishReason, nil
}

func isRateLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "429")
}

// EmbedResponse 智谱 embedding 响应
type EmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// Embed 单条文本嵌入（智谱 embedding-2，1024 维），返回归一化向量
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

// EmbedBatch 批量嵌入，返回归一化向量列表
func (c *Client) EmbedBatch(ctx context.Context, texts []string, batchSize ...int) ([][]float32, error) {
	bs := 32
	if len(batchSize) > 0 && batchSize[0] > 0 {
		bs = batchSize[0]
	}
	var out [][]float32
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

func (c *Client) embedChunk(ctx context.Context, texts []string) ([][]float32, error) {
	payload := map[string]interface{}{
		"model": "embedding-2",
		"input": texts,
	}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(c.cfg.EmbedAPIBase, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.EmbedAPIKey)

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
	var er EmbedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&er); err != nil {
		return nil, err
	}
	out := make([][]float32, len(er.Data))
	for i, d := range er.Data {
		v := make([]float32, len(d.Embedding))
		var sum float64
		for j, x := range d.Embedding {
			f := float32(x)
			v[j] = f
			sum += float64(f) * float64(f)
		}
		norm := float64(0)
		if sum > 0 {
			norm = sqrtF(sum)
		}
		if norm > 0 {
			for j := range v {
				v[j] = v[j] / float32(norm)
			}
		}
		out[i] = v
	}
	return out, nil
}

func sqrtF(x float64) float64 {
	return math.Sqrt(x)
}

func lookupEnv(k string) string {
	return os.Getenv(k)
}
