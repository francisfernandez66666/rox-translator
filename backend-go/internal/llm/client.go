// ============ 本文件职责中文说明 ============
// LLM 客户端：OpenAI 兼容 chat/completions 与智谱 embedding-2 嵌入调用。
// 核心能力（2026-08-26 评审整改 R1/R6/R7）：
//   - 三路独立信号量：chat 后台槽（LLM_CHAT_CONCURRENT，默认 2）+ 交互保留槽（1，
//     仅带 Interactive 标记的请求可抢占，保证前台划译级请求不被批任务饿死）
//   - embed 槽（LLM_EMBED_CONCURRENT，默认 6）——Chat 与 Embed 分属两家供应商、
//     账号限额互不相干，此前共用一个 3 槽信号量是文件并翻卡顿的首要根因；
//   - 排队观测：任一信号量等待 >1s 打 [llm-queue] 日志；
//   - 调用级 context 超时 + transport 层响应头/握手超时兜底（整改 D3）、
//     429 触发降级模型重试、嵌入向量 L2 归一化。
//
// =============================================
package llm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"translator/internal/config"
)

// DefaultChatConcurrent chat 后台默认并发槽；DefaultEmbedConcurrent embed 默认并发槽。
const (
	DefaultChatConcurrent  = 2 // 交互另有 1 个保留槽，总 chat 上限=3（与历史口径一致）
	DefaultEmbedConcurrent = 6
	// ★ 文件/后台批任务专用并发池（整改：大文件翻译高并发曾打满共享交互信号量，
	//   拖垮全站）。与交互池彻底隔离。容量需匹配主机规格——过大会把小规格 VM 的
	//   CPU/网络/磁盘打满，反而让全站（含即时翻译与健康检查）无响应。默认取保守值，
	//   大规格主机可用 LLM_FILE_CHAT_CONCURRENT / LLM_FILE_EMBED_CONCURRENT 调大。
	DefaultFileChatConcurrent  = 6
	DefaultFileEmbedConcurrent = 12
)

// Client LLM 客户端
type Client struct {
	cfg   *config.Config // 全局配置（模型/温度等参数来源）
	http  *http.Client   // HTTP 客户端（含代理与全局超时）
	proxy *url.URL       // 代理地址（PROXY_URL/HTTPS_PROXY/HTTP_PROXY 环境变量解析结果）

	// ★ 三路独立信号量（评审整改 R1/R6）：超过容量的调用排队等待，不无限叠加。
	//   chatSem：后台批任务共用；chatFast：交互保留槽（批任务不可占用）；
	//   embSem：嵌入调用独立池（智谱侧额度与 SiliconFlow 无关）。
	chatSem  chan struct{}
	chatFast chan struct{}
	embSem   chan struct{}

	// ★ 文件/后台批任务专用信号量（与交互池隔离，避免大文件翻译饿死交互请求）。
	fileChatSem chan struct{}
	fileEmbSem  chan struct{}

	// 知识库 Embed 阶段覆盖（stage_models.kb_embed；空=用全局 Embed 配置）。
	// ★ R7：读改一律持锁——引擎每请求都可能调用 SetEmbedOverride，裸写字段是数据竞争。
	embedMu                         sync.Mutex
	embedBase, embedKey, embedModel string

	// OnUsage 实时计费钩子：每次 LLM 调用产生真实 token 用量后回调（边工作边计费）。
	// 返回 error（如余额不足）时，本次 LLM 调用向上返回该错误，从而中止翻译，
	// 防止「后置计费」被取消/断开绕过（白嫖）。nil 表示不启用实时计费（仅归集用量）。
	OnUsage func(ctx context.Context, model string, prompt, completion int64) error

	// inflight 当前在途 LLM 调用数（观测用，原子计数）
	inflight atomic.Int64
}

// SetEmbedOverride 设置知识库 Embed 阶段覆盖端点（stage_models.kb_embed，超管维护）。
func (c *Client) SetEmbedOverride(base, key, model string) {
	c.embedMu.Lock()
	defer c.embedMu.Unlock()
	c.embedBase, c.embedKey, c.embedModel = base, key, model
}

// embedOverride 快照读取（持锁拷贝，消除竞态）。
func (c *Client) embedOverride() (base, key, model string) {
	c.embedMu.Lock()
	defer c.embedMu.Unlock()
	return c.embedBase, c.embedKey, c.embedModel
}

// embedOverrideCtxKey 请求级 Embed 覆盖端点键（整改 R3：消除全局可变状态的跨请求污染）。
// 此前的全局 SetEmbedOverride 会被并发请求 last-writer-wins 串味，导致本请求无 kb_embed 阶段
// 配置却误用其他请求的 Embed 模型；改为经 ctx 透传后，覆盖仅对当前请求生效。
type embedOverrideCtxKey struct{}

// WithEmbedOverride 把 kb_embed 阶段覆盖端点注入 ctx（仅对当前请求生效）。
// 参数：ctx=上下文，base/key/model=覆盖的 Embed 端点。
func WithEmbedOverride(ctx context.Context, base, key, model string) context.Context {
	return context.WithValue(ctx, embedOverrideCtxKey{}, [3]string{base, key, model})
}

// embedOverrideFromCtx 读取请求级 Embed 覆盖；未设置返回 ok=false。
func embedOverrideFromCtx(ctx context.Context) (base, key, model string, ok bool) {
	if v, yes := ctx.Value(embedOverrideCtxKey{}).([3]string); yes {
		return v[0], v[1], v[2], true
	}
	return "", "", "", false
}

// Inflight 当前在途 LLM 调用数（/status 与排障观测用）。
func (c *Client) Inflight() int64 { return c.inflight.Load() }

// envInt 读整型环境变量（非法或缺省返回 def）。
func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// NewClient 构造函数：初始化并返回实例。
func NewClient(cfg *config.Config) *Client {
	tr := &http.Transport{
		// ★ 禁用 HTTP/2：siliconflow 偶发 h2 流挂起（roundTrip 2min+ 无响应），
		//   HTTP/1.1 下未观测到该问题；同时缩短整体超时快速失败。
		TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}},
		ForceAttemptHTTP2:     false,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second, // 响应头兜底（体级超时仍由各调用 ctx 控制）
		IdleConnTimeout:       90 * time.Second,
	}
	if p := getenvAny("PROXY_URL", "HTTPS_PROXY", "HTTP_PROXY"); p != "" {
		// 配置 HTTP 代理（用于公司内网/受限网络访问 LLM API）
		if u, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{
		cfg: cfg,
		// ★ 整改 D3：移除 Client 级全局 45s Timeout——它会先于调用方 ctx（120s）触发，
		//   使长输出（maxTokens=8192 全量重翻/大块批量）在慢供应商下被伪超时→降级链
		//   双倍调用双倍计费。挂起防护改由 Transport 层（响应头 60s/握手 15s/空闲回收）
		//   + 各调用点 context.WithTimeout 分级承担。
		http: &http.Client{Transport: tr},
		// ★ 三路信号量容量可经环境变量调整（评审整改 R1）
		chatSem:  make(chan struct{}, envInt("LLM_CHAT_CONCURRENT", DefaultChatConcurrent)),
		chatFast: make(chan struct{}, 1),
		embSem:   make(chan struct{}, envInt("LLM_EMBED_CONCURRENT", DefaultEmbedConcurrent)),
		// ★ 文件/后台批任务专用池（容量可经环境变量调整）
		fileChatSem: make(chan struct{}, envInt("LLM_FILE_CHAT_CONCURRENT", DefaultFileChatConcurrent)),
		fileEmbSem:  make(chan struct{}, envInt("LLM_FILE_EMBED_CONCURRENT", DefaultFileEmbedConcurrent)),
	}
}

// ============ 交互标记（QoS 保留槽判定，评审整改 R6） ============

// interactiveKey ctx 存取键：交互式请求（前台 /api/chat* 与 OpenAPI 同步短文翻译）置位，
// 使 doChat 可抢占 chatFast 保留槽，不被后台批量任务排队饿死。
type interactiveKey struct{}

// WithInteractive 标记本次请求为交互式（QoS 保留槽资格）。ctx 值随引擎内部包装链透传。
func WithInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

// isInteractive 读取交互标记。
func isInteractive(ctx context.Context) bool {
	v, _ := ctx.Value(interactiveKey{}).(bool)
	return v
}

// fileModeKey ctx 存取键：文件/后台批任务（大文档翻译、批量模型补漏等）置位后，
// doChat/embedChunk 改用「文件专用信号量池」（容量更大、与交互池隔离），
// ★ 避免长文档高并发打满共享交互信号量、饿死前台即时翻译、乃至拖垮全站。
type fileModeKey struct{}

// WithFileMode 标记本次请求走文件/后台批任务专用信号量池。
func WithFileMode(ctx context.Context) context.Context {
	return context.WithValue(ctx, fileModeKey{}, true)
}

// isFileMode 读取文件模式标记。
func isFileMode(ctx context.Context) bool {
	v, _ := ctx.Value(fileModeKey{}).(bool)
	return v
}

// semSlot 一次成功 acquire 的凭据：Release 归还到「当初取得」的同一通道
// （chat/chatFast 容量不同，错位归还会逐步污染两池容量）。
type semSlot struct{ ch chan struct{} }

// Release 归还名额（幂等保护：重复调用无效果）。
func (s *semSlot) Release() {
	if s == nil || s.ch == nil {
		return
	}
	<-s.ch
	s.ch = nil // 置空防二次释放
}

// acquireWaitTimeout 信号量获牌等待上限（LLM_ACQUIRE_TIMEOUT_SEC，默认 90s）。
// ★ 防死锁整改：文件翻译等大并发场景会把全局 chatSem/embSem 打满，若无上限，
//
//	等待方在无 deadline 的 ctx 下会永久阻塞（持一锁等另一锁的循环等待），拖垮整个
//	worker 池乃至全站。加此上限后，久等不到即报错返回，由上层降级/重试，绝不会卡死。
func acquireWaitTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("LLM_ACQUIRE_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 90 * time.Second
}

// acquireChat 获取 chat 并发名额：交互请求可使用后台槽+保留槽（先试后台、再双通道竞争），
// 后台任务只能使用后台槽。文件/后台批任务（isFileMode）使用独立的大容量文件池，
// 与交互池彻底隔离。等待超过 1s 打观测日志（评审整改 R7）。
// 返回 (名额凭据, 错误)；ctx 取消或等待超时时排队中的调用立即返回错误（防永久阻塞）。
func (c *Client) acquireChat(ctx context.Context) (*semSlot, error) {
	start := time.Now()
	// ★ 文件/后台批任务：走专用信号量池（容量更大，且与交互池隔离，避免饿死前台）。
	if isFileMode(ctx) {
		acqCtx, acqCancel := context.WithTimeout(ctx, acquireWaitTimeout())
		defer acqCancel()
		select {
		case c.fileChatSem <- struct{}{}:
			c.noteAcquired("fileChat", start)
			return &semSlot{ch: c.fileChatSem}, nil
		case <-acqCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("llm 文件翻译 chat 并发槽获取超时（请稍后重试）")
		}
	}
	if isInteractive(ctx) {
		// 非阻塞优先取后台槽（避免保留槽被无关紧要地占用）
		select {
		case c.chatSem <- struct{}{}:
			c.noteAcquired("chat", start)
			return &semSlot{ch: c.chatSem}, nil
		default:
		}
	}
	// ★ 防死锁：以等待上限派生 ctx，避免无 deadline 的调用在信号量打满时永久卡死。
	acqCtx, acqCancel := context.WithTimeout(ctx, acquireWaitTimeout())
	defer acqCancel()
	var got chan struct{}
	if isInteractive(ctx) {
		// 双通道阻塞竞争（先到先用）
		select {
		case c.chatSem <- struct{}{}:
			got = c.chatSem
		case c.chatFast <- struct{}{}:
			got = c.chatFast
		case <-acqCtx.Done():
		}
	} else {
		select {
		case c.chatSem <- struct{}{}:
			got = c.chatSem
		case <-acqCtx.Done():
		}
	}
	if got == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("llm chat 并发槽获取超时（信号量可能已耗尽，请稍后重试）")
	}
	c.noteAcquired("chat", start)
	return &semSlot{ch: got}, nil
}

// acquireEmbed 获取 embed 并发名额（独立池，不与 chat 抢占）。
// 文件/后台批任务走专用文件池（容量更大、与交互池隔离）。同样带等待上限，
// 避免嵌入调用在信号量打满时永久阻塞。
func (c *Client) acquireEmbed(ctx context.Context) (*semSlot, error) {
	start := time.Now()
	if isFileMode(ctx) {
		acqCtx, acqCancel := context.WithTimeout(ctx, acquireWaitTimeout())
		defer acqCancel()
		select {
		case c.fileEmbSem <- struct{}{}:
			c.noteAcquired("fileEmbed", start)
			return &semSlot{ch: c.fileEmbSem}, nil
		case <-acqCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("llm 文件翻译 embed 并发槽获取超时（请稍后重试）")
		}
	}
	acqCtx, acqCancel := context.WithTimeout(ctx, acquireWaitTimeout())
	defer acqCancel()
	select {
	case c.embSem <- struct{}{}:
		c.noteAcquired("embed", start)
		return &semSlot{ch: c.embSem}, nil
	case <-acqCtx.Done():
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("llm embed 并发槽获取超时（信号量可能已耗尽，请稍后重试）")
	}
}

// noteAcquired 记录在途数并按需输出排队观测日志。
func (c *Client) noteAcquired(kind string, start time.Time) {
	c.inflight.Add(1)
	if d := time.Since(start); d > time.Second {
		log.Printf("[llm-queue] kind=%s waited=%s inflight=%d", kind, d.Round(10*time.Millisecond), c.inflight.Load())
	}
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

// abortKey ctx 存取键：实时计费钩子在余额不足时取出取消函数，中止整次翻译任务。
// 否则仅当前 LLM 调用失败、引擎对单段错误容忍并继续，其余段会被供应商「免费」翻译（白嫖漏洞）。
type abortKey struct{}

// WithAbort 向 ctx 注入余额不足时的中止函数（引擎在翻译任务入口创建并注入）。
func WithAbort(ctx context.Context, fn func()) context.Context {
	return context.WithValue(ctx, abortKey{}, fn)
}

// AbortFromCtx 取余额不足中止函数；未注入返回 nil。
func AbortFromCtx(ctx context.Context) func() {
	fn, _ := ctx.Value(abortKey{}).(func())
	return fn
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
	model := ""
	if m, ok := payload["model"].(string); ok {
		model = m
	}
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

	// ★ chat 并发限流：后台任务共用 chatSem，交互请求另可抢占保留槽（评审整改 R1/R6）
	slot, err := c.acquireChat(ctx)
	if err != nil {
		return "", "", err
	}
	defer c.inflight.Add(-1)
	defer slot.Release()

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
	// ★ 实时计费：每次 chat 调用后立即扣减，余额不足则中止翻译（边工作边计费，防白嫖）
	if c.OnUsage != nil {
		if err := c.OnUsage(ctx, model, cr.Usage.PromptTokens, cr.Usage.CompletionTokens); err != nil {
			return "", "", err
		}
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), cr.Choices[0].FinishReason, nil
}

// isRateLimit 判断错误是否为限流（错误消息包含 "429"）。
// 参数：err=待判断错误；返回是否为限流错误。
func isRateLimit(err error) bool {
	return err != nil && strings.Contains(err.Error(), "429")
}

// EmbedResponse 嵌入响应（OpenAI 兼容 embeddings 格式，兼容 SiliconFlow 智谱等）
type EmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"` // 嵌入向量（float64 数组）
	} `json:"data"`
	Usage Usage `json:"usage"` // 嵌入调用 token 用量（KB 匹配成本归集）
}

// Embed 单条文本嵌入（默认 SiliconFlow BAAI/bge-m3，1024 维），返回 L2 归一化向量。
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
	// 阶段覆盖（kb_embed）：超管在分阶段模型里配置的 Embed 端点优先（R7：持锁快照读取）
	base, key, model := c.cfg.EmbedAPIBase, c.cfg.EmbedAPIKey, c.cfg.EmbedModel
	// 优先级：ctx 请求级覆盖 > 全局快照覆盖 > 默认配置（整改 R3）
	if cb, ck, cm, cok := embedOverrideFromCtx(ctx); cok && cb != "" && cm != "" {
		base, key, model = cb, ck, cm
	} else if eb, ek, em := c.embedOverride(); eb != "" && em != "" {
		base, key, model = eb, ek, em
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

	// ★ embed 独立并发限流（评审整改 R1）：不与 chat 抢占——两家供应商额度本就独立
	slot, err := c.acquireEmbed(ctx2)
	if err != nil {
		return nil, err
	}
	defer c.inflight.Add(-1)
	defer slot.Release()

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
	// ★ 实时计费：每次 embed 调用后立即扣减，余额不足则中止（边工作边计费，防白嫖）
	if c.OnUsage != nil {
		if err := c.OnUsage(ctx2, model, er.Usage.PromptTokens, er.Usage.CompletionTokens); err != nil {
			return nil, err
		}
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
