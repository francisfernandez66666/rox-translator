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
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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
	"translator/internal/infra/concurrency"
	"translator/internal/infra/redis"
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
	//   chatSem：后台批任务共用（Redis 全局上限，未启用则进程内）；
	//   chatFast：交互保留槽（进程内，容量 1，批任务不可占用）；
	//   embSem：嵌入调用独立池（智谱侧额度与 SiliconFlow 无关）。
	// 阶段二：后台/文件/embed 池经 concurrency.Semaphore 实现——Redis 启用时跨实例共享上限，
	// 未启用 Redis 自动降级为进程内 channel（单实例行为不变）。
	chatSem  concurrency.Semaphore
	chatFast concurrency.Semaphore
	embSem   concurrency.Semaphore

	// ★ 文件/后台批任务专用信号量（与交互池隔离，避免大文件翻译饿死交互请求）。
	fileChatSem concurrency.Semaphore
	fileEmbSem  concurrency.Semaphore

	// 知识库 Embed 阶段覆盖（stage_models.kb_embed；空=用全局 Embed 配置）。
	// ★ R7：读改一律持锁——引擎每请求都可能调用 SetEmbedOverride，裸写字段是数据竞争。
	embedMu                         sync.Mutex
	embedBase, embedKey, embedModel string

	// OnUsage 实时计费钩子：每次 LLM 调用产生真实 token 用量后回调（边工作边计费）。
	// 返回 error（如余额不足）时，本次 LLM 调用向上返回该错误，从而中止翻译，
	// 防止「后置计费」被取消/断开绕过（白嫖）。nil 表示不启用实时计费（仅归集用量）。
	OnUsage func(ctx context.Context, model string, prompt, completion int64) error

	// ★ H2 双轨约束解码：true 表示当前路由供应商声明支持术语约束扩展
	//   （x_term_constraints / guided 类），CallChat/StreamChat 会随请求注入。
	//   由 main.go 按 model_routes 的 supports_constraints 一致性统一设定；
	//   不支持时引擎自然降级为 H1 事后强制闭环。
	SupportsConstraints bool

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
		// ★ 三路信号量容量可经环境变量调整（评审整改 R1）；阶段二：Redis 启用时跨实例共享上限。
		//   chatFast 保留槽固定进程内容量 1（交互 QoS 本地优先，不被全局容量稀释）。
		chatSem:  concurrency.New("sem:llm:chat", envInt("LLM_CHAT_CONCURRENT", DefaultChatConcurrent), redis.Get()),
		chatFast: concurrency.New("", 1, nil), // 恒进程内
		embSem:   concurrency.New("sem:llm:embed", envInt("LLM_EMBED_CONCURRENT", DefaultEmbedConcurrent), redis.Get()),
		// ★ 文件/后台批任务专用池（容量可经环境变量调整）
		fileChatSem: concurrency.New("sem:llm:filechat", envInt("LLM_FILE_CHAT_CONCURRENT", DefaultFileChatConcurrent), redis.Get()),
		fileEmbSem:  concurrency.New("sem:llm:fileembed", envInt("LLM_FILE_EMBED_CONCURRENT", DefaultFileEmbedConcurrent), redis.Get()),
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
// 返回 (释放函数, 错误)；ctx 取消或等待超时时排队中的调用立即返回错误（防永久阻塞）。
// 阶段二：后台/文件池为 concurrency.Semaphore（Redis 全局上限或进程内），保留槽恒进程内。
func (c *Client) acquireChat(ctx context.Context) (func(), error) {
	start := time.Now()
	// ★ 文件/后台批任务：走专用信号量池（容量更大，且与交互池隔离，避免饿死前台）。
	if isFileMode(ctx) {
		acqCtx, cancel := withAcqTimeout(ctx)
		defer cancel()
		rel, err := c.fileChatSem.Acquire(acqCtx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("llm 文件翻译 chat 并发槽获取超时（请稍后重试）")
		}
		c.noteAcquired("fileChat", start)
		return rel, nil
	}
	if isInteractive(ctx) {
		// 非阻塞优先取后台槽（避免保留槽被无关紧要地占用）
		if rel, ok := c.chatSem.TryAcquire(); ok {
			c.noteAcquired("chat", start)
			return rel, nil
		}
	}
	// ★ 防死锁：以等待上限派生 ctx，避免无 deadline 的调用在信号量打满时永久卡死。
	//   交互请求可在「本地保留槽」与「全局后台槽」间二选一竞争，保证前台不饿死。
	if isInteractive(ctx) {
		acqCtx, cancel := withAcqTimeout(ctx)
		defer cancel()
		rel, err := concurrency.AcquireEither(acqCtx, c.chatFast, c.chatSem)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("llm chat 并发槽获取超时（信号量可能已耗尽，请稍后重试）")
		}
		c.noteAcquired("chat", start)
		return rel, nil
	}
	acqCtx, cancel := withAcqTimeout(ctx)
	defer cancel()
	rel, err := c.chatSem.Acquire(acqCtx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("llm chat 并发槽获取超时（信号量可能已耗尽，请稍后重试）")
	}
	c.noteAcquired("chat", start)
	return rel, nil
}

// acquireEmbed 获取 embed 并发名额（独立池，不与 chat 抢占）。
// 文件/后台批任务走专用文件池（容量更大、与交互池隔离）。同样带等待上限，
// 避免嵌入调用在信号量打满时永久阻塞。
func (c *Client) acquireEmbed(ctx context.Context) (func(), error) {
	start := time.Now()
	if isFileMode(ctx) {
		acqCtx, cancel := withAcqTimeout(ctx)
		defer cancel()
		rel, err := c.fileEmbSem.Acquire(acqCtx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("llm 文件翻译 embed 并发槽获取超时（请稍后重试）")
		}
		c.noteAcquired("fileEmbed", start)
		return rel, nil
	}
	acqCtx, cancel := withAcqTimeout(ctx)
	defer cancel()
	rel, err := c.embSem.Acquire(acqCtx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("llm embed 并发槽获取超时（信号量可能已耗尽，请稍后重试）")
	}
	c.noteAcquired("embed", start)
	return rel, nil
}

// withAcqTimeout 派生带等待上限的 ctx（防永久阻塞，见 acquireWaitTimeout），返回 cancel 由调用方 defer 释放。
func withAcqTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, acquireWaitTimeout())
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
	// ★ B1 缓存度量（2026-09-19）：OpenAI 兼容 usage.prompt_tokens_details.cached_tokens。
	//   仅进程内观测（/metrics），供应商不回传时保持 0；计费口径不变，仍按 prompt-completion 结算。
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"` // 命中 prompt 缓存的输入 token 数
	} `json:"prompt_tokens_details"`
}

// Total 返回输入+输出合计 token 数。
func (u Usage) Total() int64 { return u.PromptTokens + u.CompletionTokens }

// ============ ★ B1 LLM 观测面（prompt 缓存命中 + 流式首 token 延迟，2026-09-19） ============
// 进程级累计计数器，与计费链路（OnUsage/usage_ledger）解耦——只供 /metrics 导出，
// 用于观察 B4 前缀重组后缓存是否真实生效、以及流式 TTFT 的改善幅度。
// 红线：cached_tokens 属内部成本信号，一律不进用户面页面与公开接口响应（积分口径）。
var (
	obsPromptTokens atomic.Int64 // chat 累计输入 token（缓存命中率分母；embed 不计，保持口径纯净）
	obsCachedTokens atomic.Int64 // 其中命中 prompt 缓存的输入 token（分子）
	obsStreamCalls  atomic.Int64 // 完成且可信计费的流式调用次数
	obsTtftSumMs    atomic.Int64 // TTFT 样本毫秒合计
	obsTtftCnt      atomic.Int64 // TTFT 样本数（每调用 1 个：首个非空 delta）
)

// LLMObservability /metrics 快照形状（字段全为内部观测口径）
type LLMObservability struct {
	PromptTokens    int64   // chat 累计输入 token
	CachedTokens    int64   // 累计命中缓存的输入 token
	CacheRate       float64 // 缓存命中率 0~1（无样本时 0）
	StreamCalls     int64   // 流式调用完成数
	StreamTtftAvgMs float64 // 平均首 token 延迟 ms（无样本时 0）
	TtftSamples     int64   // TTFT 样本数（子毫秒调用会记 0ms，样本数用于判断均值可信度）
}

// Observability 取进程级 LLM 观测快照（api 层 /metrics 导出用）。
func Observability() LLMObservability {
	p, c, n := obsPromptTokens.Load(), obsCachedTokens.Load(), obsTtftCnt.Load()
	var rate, ttft float64
	if p > 0 {
		rate = float64(c) / float64(p)
	}
	if n > 0 {
		ttft = float64(obsTtftSumMs.Load()) / float64(n)
	}
	return LLMObservability{PromptTokens: p, CachedTokens: c, CacheRate: rate,
		StreamCalls: obsStreamCalls.Load(), StreamTtftAvgMs: ttft, TtftSamples: n}
}

// recordChatUsageObs 归集一次 chat 调用（非流式/流式共用）的输入侧观测：prompt 与缓存命中量。
func recordChatUsageObs(prompt, cached int64) {
	if prompt > 0 {
		obsPromptTokens.Add(prompt)
	}
	if cached > 0 {
		obsCachedTokens.Add(cached)
	}
}

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
// ★ H2 术语约束解码（双轨之「事前约束」轨）。
// TermConstraint 单条术语约束：源术语 → 规定译法。
type TermConstraint struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// termConstraintsKey 是术语约束在 ctx 中的键。
type termConstraintsKey struct{}

// WithTermConstraints 把术语约束挂到 ctx（引擎仅在客户端 SupportsConstraints 时注入）。
func WithTermConstraints(ctx context.Context, cs []TermConstraint) context.Context {
	if len(cs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, termConstraintsKey{}, cs)
}

// TermConstraintsFromCtx 读取 ctx 中的术语约束（无则 nil）。
func TermConstraintsFromCtx(ctx context.Context) []TermConstraint {
	cs, _ := ctx.Value(termConstraintsKey{}).([]TermConstraint)
	return cs
}

// chatPayload 为 OpenAI 兼容 chat/completions 请求体（map 形态便于附加厂商私有字段）。
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
	// ★ H2：声明支持约束解码的供应商——随请求注入术语约束（OpenAI 兼容扩展字段）
	if cs := TermConstraintsFromCtx(ctx); len(cs) > 0 && c.SupportsConstraints {
		payload["x_term_constraints"] = cs
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
		if !sleepCtx(ctx, 2*time.Second) {
			return // ★ D9：取消即止损（保留原始错误返回）
		}
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
	rel, err := c.acquireChat(ctx)
	if err != nil {
		return "", "", err
	}
	defer c.inflight.Add(-1)
	defer rel()

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	// 状态码分类处理：429 限流、401 密钥无效、其余非 200 返回错误体摘要
	// ★ D15（2026-09-12）：改抛类型化 StatusError——限流/鉴权判定不再依赖
	// 文案子串（供应商改措辞即失灵），errors.As 直达状态码。
	if resp.StatusCode == 429 {
		return "", "", &StatusError{Code: 429}
	}
	if resp.StatusCode == 401 {
		return "", "", fmt.Errorf("api key 无效 (401)")
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", "", &StatusError{Code: resp.StatusCode, Body: string(b)}
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
	// ★ B1 观测面：输入侧 prompt 与缓存命中量归集（仅 /metrics，计费行为不变）
	recordChatUsageObs(cr.Usage.PromptTokens, cr.Usage.PromptTokensDetails.CachedTokens)
	// ★ 实时计费：每次 chat 调用后立即扣减，余额不足则中止翻译（边工作边计费，防白嫖）
	if c.OnUsage != nil {
		if err := c.OnUsage(ctx, model, cr.Usage.PromptTokens, cr.Usage.CompletionTokens); err != nil {
			return "", "", err
		}
	}
	return strings.TrimSpace(cr.Choices[0].Message.Content), cr.Choices[0].FinishReason, nil
}

// ★ D20（2026-09-12）：token 级流式调用（chat 路径）。
// onDelta 收到 OpenAI 兼容 SSE 的增量文本；结束块必须携带 usage（stream_options.include_usage），
// 否则视为不可信供应商——返回 errNoStreamUsage 让调用方回退非流式重发（计费口径不蒸发，
// 代价是极小概率用户看到一次重复输出，仅出现于不兼容 stream_options 的端点）。
var errNoStreamUsage = errors.New("stream 未回传 usage（不可计费）")

// StreamChat 流式 chat：endpoint 语义同 CallChat；hunyuan 参数不支持流式（返回错误走回退）。
// 返回：完整累计文本、finishReason、错误（含“不支持流式”类错误，调用方应回退 CallChat）。
func (c *Client) StreamChat(ctx context.Context, baseURL, apiKey, model string,
	messages []map[string]string, maxTokens int, temp float64, onDelta func(string)) (string, string, error) {

	endpoint := strings.TrimRight(baseURL, "/") + "/chat/completions"
	payload := chatPayload{
		"model":          model,
		"messages":       messages,
		"max_tokens":     maxTokens,
		"temperature":    temp,
		"stream":         true,
		"stream_options": map[string]interface{}{"include_usage": true},
	}
	// ★ H2：流式路径同样注入术语约束（与 CallChat 同规则）
	if cs := TermConstraintsFromCtx(ctx); len(cs) > 0 && c.SupportsConstraints {
		payload["x_term_constraints"] = cs
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 180*time.Second)
		defer cancel()
		req = req.WithContext(ctx)
	}
	rel, err := c.acquireChat(ctx)
	if err != nil {
		return "", "", err
	}
	defer c.inflight.Add(-1)
	defer rel()

	// ★ B1：TTFT 起点取「请求发出前一刻」（信号量排队时间不计入模型首 token 延迟，
	// 排队劣化由 [llm-queue] 日志与路由耗时统计另行承担）
	ttftStart := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 429:
		return "", "", &StatusError{Code: 429}
	case resp.StatusCode == 401:
		return "", "", errors.New("api key 无效 (401)")
	case resp.StatusCode != 200:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", "", &StatusError{Code: resp.StatusCode, Body: string(b)}
	}
	// 非 SSE 响应（mock/不兼容端点返回 application/json）：不消费，判为不支持流式
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		return "", "", errors.New("端点不支持流式（Content-Type=" + ct + "）")
	}

	var (
		full         strings.Builder
		finish       string
		prompt       int64
		completion   int64
		cached       int64 // ★ B1：usage.prompt_tokens_details.cached_tokens（内部观测，不参与计费判定）
		ttftRecorded bool
	)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				// ★ B1：部分兼容端点在尾包 usage 里回传缓存命中明细（无则为 0，不影响计费）
				PromptTokensDetails struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue // 心跳/非 JSON 分片
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				// ★ B1：首个非空 delta 即 TTFT 样本（每调用只记 1 个）
				if !ttftRecorded {
					ttftRecorded = true
					obsTtftSumMs.Add(time.Since(ttftStart).Milliseconds())
					obsTtftCnt.Add(1)
				}
				full.WriteString(ch.Delta.Content)
				if onDelta != nil {
					onDelta(ch.Delta.Content)
				}
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				finish = *ch.FinishReason
			}
		}
		if chunk.Usage != nil {
			prompt = chunk.Usage.PromptTokens
			completion = chunk.Usage.CompletionTokens
			cached = chunk.Usage.PromptTokensDetails.CachedTokens
		}
	}
	if err := sc.Err(); err != nil {
		return "", "", err
	}
	if prompt == 0 && completion == 0 {
		return "", "", errNoStreamUsage
	}
	// ★ B1 观测面：流式调用完成后归集（走到这里即 usage 可信）
	recordChatUsageObs(prompt, cached)
	obsStreamCalls.Add(1)
	if uc := CollectorFrom(ctx); uc != nil {
		uc.Add(prompt, completion)
	}
	if c.OnUsage != nil {
		if err := c.OnUsage(ctx, model, prompt, completion); err != nil {
			return "", "", err
		}
	}
	return strings.TrimSpace(full.String()), finish, nil
}

// isRateLimit 判断错误是否为限流（错误消息包含 "429"）。
// 参数：err=待判断错误；返回是否为限流错误。
func isRateLimit(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code == 429 // ★ D15：类型判定优先
	}
	return strings.Contains(err.Error(), "429") // 兼容旧文案/透传错误
}

// StatusError ★ D15：LLM HTTP 非 2xx 的类型化错误（承载状态码供 errors.As 判定）。
type StatusError struct {
	Code int
	Body string // 非 200 响应体摘要（≤500B）
}

// Error 实现 error 接口：429 统一归一化为 rate_limited 标记，便于上游限流识别。
func (e *StatusError) Error() string {
	if e.Code == 429 {
		return "rate_limited: HTTP 429"
	}
	return fmt.Sprintf("LLM API HTTP %d: %s", e.Code, e.Body)
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
	acqCtx, cancel := withAcqTimeout(ctx2)
	defer cancel()
	rel, err := c.embSem.Acquire(acqCtx)
	if err != nil {
		return nil, err
	}
	defer c.inflight.Add(-1)
	defer rel()

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
