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
	"log"
	"net/http"
	"sync"
	"time"
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

// Client 降级链客户端
type Client struct {
	mu        sync.RWMutex
	providers []Provider
	fails     map[int]int       // 连续失败次数
	coolUntil map[int]time.Time // 冷却截止
	http      *http.Client
	budget    time.Duration
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
			log.Printf("[llm] provider[%d] %s 冷却中，跳过", i, p.Model)
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
		log.Printf("[llm] provider[%d] %s 失败: %v，降级", i, p.Model, err)
		c.markFail(i)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all providers cooling down")
	}
	return "", "", Usage{}, lastErr
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
func (c *Client) markFail(i int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails[i]++
	// 认证类/连续失败 3 次进冷却 5 分钟
	if c.fails[i] >= 3 {
		c.coolUntil[i] = time.Now().Add(5 * time.Minute)
		delete(c.fails, i)
		log.Printf("[llm] provider[%d] 连续失败进冷却 5 分钟", i)
	}
}

// trimSlash 去除结尾斜杠（base URL 归一）
func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}
