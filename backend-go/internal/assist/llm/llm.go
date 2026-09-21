// Package llm OpenAI 兼容 Chat Completions 客户端 + 多模型降级链。
// 降级思想移植自 ai-scrm internal/ai/ai_router.go：
//   - 每次对话从主模型开始尝试（不永久切走）
//   - 单条消息内失败自动降级到下一个模型
//   - 降级带冷却（连续失败 3 次进冷却 5 分钟），定时恢复
//   - 整条链共享总预算 deadline，防止挂死叠超时
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sync"
	"time"

	"translator/internal/observability"
)

// Message OpenAI 兼容消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage token 用量
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Provider 单个模型候选
type Provider struct {
	Name    string // 展示名
	BaseURL string // 如 https://api.siliconflow.cn/v1
	APIKey  string
	Model   string
}

// Client 降级链客户端：按 providers 顺序依次尝试，单条消息内失败自动降下一个模型。
type Client struct {
	mu        sync.RWMutex      // 保护下方可变字段的并发读写
	providers []Provider        // 按优先级排列的模型候选列表
	fails     map[int]int       // 各 provider 连续失败次数（下标 → 次数），达阈值进冷却
	coolUntil map[int]time.Time // 各 provider 冷却截止时间，未到期则本次跳过
	http      *http.Client      // 复用底层 HTTP 连接
	budget    time.Duration     // 整条降级链的总超时预算（防挂死叠加超时）
}

// New 构建客户端；providers 按优先级排列
func New(providers []Provider, timeoutSec int) *Client {
	if timeoutSec <= 0 {
		timeoutSec = 45
	}
	return &Client{
		providers: providers,
		fails:     map[int]int{},
		coolUntil: map[int]time.Time{},
		http:      &http.Client{},
		budget:    time.Duration(timeoutSec*len(providers)+10) * time.Second,
	}
}

// Enabled 是否有可用 provider
func (c *Client) Enabled() bool { return c != nil && len(c.providers) > 0 }

// Chat 走降级链生成回复。返回正文、实际模型名、用量、错误。
func (c *Client) Chat(ctx context.Context, temperature float64, maxTokens int, messages []Message) (string, string, Usage, error) {
	if !c.Enabled() {
		return "", "", Usage{}, fmt.Errorf("no llm provider")
	}
	chainCtx, cancel := context.WithTimeout(ctx, c.budget)
	defer cancel()

	c.mu.RLock()
	provCount := len(c.providers)
	c.mu.RUnlock()

	var lastErr error
	now := time.Now()
	for i := 0; i < provCount; i++ {
		// 冷却检查
		c.mu.RLock()
		cool := c.coolUntil[i].After(now)
		p := c.providers[i]
		c.mu.RUnlock()
		if cool {
			// ★ 改造 1A：接主仓 observability（slog JSON + trace_id），不再散落 log.Printf
			observability.Warn(ctx, "assist.llm provider 冷却中，跳过",
				"provider_index", i, "model", p.Model)
			continue
		}

		// 单模型预算 = 总预算/候选数（防止首个挂死饿死后续）
		deadline, _ := chainCtx.Deadline()
		remain := time.Until(deadline)
		per := c.budget / time.Duration(provCount)
		if per > remain {
			per = remain
		}
		if per <= 0 {
			break
		}
		mctx, mcancel := context.WithTimeout(chainCtx, per)
		text, usage, err := c.chatOne(mctx, p, temperature, maxTokens, messages)
		mcancel()
		if err == nil && text != "" {
			c.markOK(i)
			return text, p.Model, usage, nil
		}
		lastErr = err
		observability.Warn(ctx, "assist.llm provider 调用失败，降级",
			"provider_index", i, "model", p.Model, "err", err)
		c.markFail(ctx, i)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all providers cooling down")
	}
	return "", "", Usage{}, lastErr
}

// ============================================================
// 嵌入调用（★ 分级召回第 3 级，engine/vector.go 唯一消费方）
// 与 chat 同 base_url / api_key（OpenAI 兼容网关两家共用，主服务
// internal/llm 的 embeddings 调用即同款协议），模型名由调用方传入。
// 默认不被触发：engine 侧 embed_recall 开关关闭时本方法零调用、零网络请求。
// ============================================================

// Embed 批量文本嵌入，返回 L2 归一化向量（点积即余弦相似度）。
// 参数：model=嵌入模型名（如 BAAI/bge-m3）；texts=待嵌入文本。
// 失败语义：显式返回 error，由调用方降级（向量召回回落字面召回），不 panic。
func (c *Client) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if !c.Enabled() {
		return nil, fmt.Errorf("no llm provider")
	}
	if model == "" {
		return nil, fmt.Errorf("embed model 未配置")
	}
	if len(texts) == 0 {
		return nil, nil
	}
	c.mu.RLock()
	p := c.providers[0] // 嵌入固定走主 provider（降级链对向量无意义，失败即回落）
	c.mu.RUnlock()

	body, _ := json.Marshal(map[string]any{"model": model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trimSlash(p.BaseURL)+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 向量响应可较大，限读 8MB
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding http %d: %.200s", resp.StatusCode, string(rb))
	}
	var out struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return nil, err
	}
	vecs := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		v := make([]float32, len(d.Embedding))
		var sum float64
		for j, x := range d.Embedding {
			f := float32(x)
			v[j] = f
			sum += float64(f) * float64(f)
		}
		if sum > 0 { // L2 归一：检索侧点积即余弦，与主服务 kb 嵌入口径一致
			norm := float32(math.Sqrt(sum))
			for j := range v {
				v[j] /= norm
			}
		}
		vecs = append(vecs, v)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedding 响应为空")
	}
	return vecs, nil
}

// chatOne 单次 Chat Completions 调用
func (c *Client) chatOne(ctx context.Context, p Provider, temperature float64, maxTokens int, messages []Message) (string, Usage, error) {
	body := map[string]any{
		"model":       p.Model,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, trimSlash(p.BaseURL)+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()
	rb, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", Usage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, fmt.Errorf("http %d: %.200s", resp.StatusCode, string(rb))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(rb, &out); err != nil {
		return "", Usage{}, err
	}
	if len(out.Choices) == 0 {
		return "", Usage{}, fmt.Errorf("empty choices")
	}
	return out.Choices[0].Message.Content, out.Usage, nil
}

// markOK 调用成功：清零失败计数与冷却
func (c *Client) markOK(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.fails, i)
	delete(c.coolUntil, i)
}

// markFail 调用失败：累计失败计数，连续 3 次进入 5 分钟冷却
// ★ 改造 1A：签名加 ctx，冷却日志经 observability 输出以继承 trace_id。
func (c *Client) markFail(ctx context.Context, i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails[i]++
	// 认证类/连续失败 3 次进冷却 5 分钟
	if c.fails[i] >= 3 {
		c.coolUntil[i] = time.Now().Add(5 * time.Minute)
		delete(c.fails, i)
		observability.Warn(ctx, "assist.llm provider 连续失败进冷却",
			"provider_index", i, "cooldown", "5m")
	}
}

// trimSlash 去除结尾斜杠（base URL 归一）
func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
