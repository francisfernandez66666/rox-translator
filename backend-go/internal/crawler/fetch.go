// ============ fetch.go · 职责说明 ============
// crawler 包 HTTP 抓取基座：受限网页抓取（tier2）与官方 API（tier1）共用的客户端。
// 合规要点：尊重 robots.txt、User-Agent 标识来源、限频（每主机最小间隔）、超时、失败重试。
// robots.txt 规则缓存 1 小时，减少对目标站点的探测请求。
// =============================================
package crawler

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// fetchConfig 抓取基座配置（限频/超时/重试）。
type fetchConfig struct {
	UserAgent   string        // 标识来源的 UA（必填，合规溯源）
	MinInterval time.Duration // 每主机最小请求间隔（限频）
	Timeout     time.Duration // 单请求超时
	MaxRetries  int           // 失败重试次数
}

// defaultFetchConfig 默认抓取基座配置。
var defaultFetchConfig = fetchConfig{
	// ★ B11：UA 联系方式取品牌域 env（未配置则不带 URL）
	UserAgent:   crawlerUserAgent(),
	MinInterval: 1200 * time.Millisecond, // 每主机 ≥1.2s（低于常见速率限制）
	Timeout:     15 * time.Second,
	MaxRetries:  2,
}

// fetchBase 并发安全的抓取客户端。
type fetchBase struct {
	cfg    fetchConfig
	client *http.Client
	// 主机级限频：lastRequest[host] = 上次请求时刻
	mu          sync.Mutex
	lastRequest map[string]time.Time
	// robots 缓存：robots[host] = (允许, 过期时间)
	robots   map[string]robotsEntry
	robotsMu sync.Mutex
}

// robotsEntry 缓存 robots.txt 的单条 allow/deny 判定及其过期时间。
type robotsEntry struct {
	allow   bool
	expires time.Time
}

// newFetchBase 创建抓取客户端。
func newFetchBase() *fetchBase {
	cfg := defaultFetchConfig
	return &fetchBase{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
			Transport: &http.Transport{
				DialContext:  (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
				MaxIdleConns: 8,
			},
		},
		lastRequest: map[string]time.Time{},
		robots:      map[string]robotsEntry{},
	}
}

// hostOf 提取 URL 主机名。
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Host
}

// robotsAllowed 判断目标 URL 是否允许抓取（尊重 robots.txt）。
// 解析规则简化：匹配 User-agent: * 的 Disallow 前缀；robots 不可达时默认允许（保守但可用）。
// 结果缓存 1 小时。
func (f *fetchBase) robotsAllowed(ctx context.Context, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	host := u.Host
	f.robotsMu.Lock()
	if e, ok := f.robots[host]; ok && time.Now().Before(e.expires) {
		f.robotsMu.Unlock()
		return e.allow
	}
	f.robotsMu.Unlock()
	// 抓取 robots.txt。★ D12（2026-09-12）：不可达改按「拒绝」处理——
	// 旧实现 err/5xx 一律 allow（注释自认 fail-open），等于「服务器一抖就绕规」。
	// 对齐主流爬虫口径：网络错误/5xx=fail-closed 停抓（本小时缓存同样生效，防风暴）；
	// 4xx（含 404 无 robots 文件）=无约束可查，放行。
	robotsURL := u.Scheme + "://" + u.Host + "/robots.txt"
	allow := false
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	req.Header.Set("User-Agent", f.cfg.UserAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		log.Printf("[crawler] robots.txt 不可达（按禁抓处理）%s: %v", robotsURL, err)
	} else {
		defer resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			allow = !robotsDisallows(string(body), u.Path)
		case resp.StatusCode >= 500:
			log.Printf("[crawler] robots.txt 服务端 %d（按禁抓处理）%s", resp.StatusCode, robotsURL)
		default: // 4xx：无 robots 文件/不可读视为无规则，放行
			allow = true
		}
	}
	f.robotsMu.Lock()
	f.robots[host] = robotsEntry{allow: allow, expires: time.Now().Add(time.Hour)}
	f.robotsMu.Unlock()
	return allow
}

// robotsDisallows 解析 robots.txt 文本，判断路径是否被 User-agent: * 禁止。
// 简单前缀匹配：Disallow: /path 命中即以 /path 开头的路径被禁止；Disallow: / 全禁。
func robotsDisallows(body, path string) bool {
	inUAStar := false
	disallows := []string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.Split(line, "#")[0])
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "user-agent:") {
			agent := strings.TrimSpace(line[len("user-agent:"):])
			inUAStar = strings.EqualFold(agent, "*")
			continue
		}
		if !inUAStar {
			continue
		}
		if strings.HasPrefix(lower, "disallow:") {
			rule := strings.TrimSpace(line[len("disallow:"):])
			if rule != "" {
				disallows = append(disallows, rule)
			}
		}
	}
	for _, rule := range disallows {
		if strings.HasPrefix(path, rule) {
			return true
		}
	}
	return false
}

// throttle 主机级限频：距上次请求不足 MinInterval 则等待。
// ★ D9（2026-09-12）：等待改 ctx 感知——取消时返回 error 而非睡满（抓取在
// 请求线程上执行，客户端断开后不应继续占用）。锁内只算时长，睡在锁外。
func (f *fetchBase) throttle(ctx context.Context, host string) error {
	var wait time.Duration
	f.mu.Lock()
	last, ok := f.lastRequest[host]
	if ok {
		if elapsed := time.Since(last); elapsed < f.cfg.MinInterval {
			wait = f.cfg.MinInterval - elapsed
		}
	}
	f.lastRequest[host] = time.Now()
	f.mu.Unlock()
	if wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// get 带限频 + 重试 + robots 检查的 GET 请求（受限网页抓取 tier2 用）；
// 返回响应体字节。参数 ctx=上下文；rawURL=目标 URL。返回 (内容, 错误)。
func (f *fetchBase) get(ctx context.Context, rawURL string) ([]byte, error) {
	if !f.robotsAllowed(ctx, rawURL) {
		return nil, fmt.Errorf("robots.txt 禁止抓取: %s", rawURL)
	}
	return f.doGet(ctx, rawURL)
}

// doGet 限频 + 重试 + UA 的 GET 请求核心（不做 robots 检查）。
// 参数 ctx=上下文；rawURL=目标 URL。返回 (内容, 错误)。
func (f *fetchBase) doGet(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= f.cfg.MaxRetries; attempt++ {
		if terr := f.throttle(ctx, hostOf(rawURL)); terr != nil {
			return nil, terr // ★ D9：限频等待期取消，直接止损
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", f.cfg.UserAgent)
		req.Header.Set("Accept", "text/html,application/json,*/*")
		resp, err := f.client.Do(req)
		if err != nil {
			lastErr = err
			if serr := sleepCtx(ctx, time.Duration(attempt+1)*500*time.Millisecond); serr != nil {
				return nil, serr // ★ D9：网络退避期取消，不再重试
			}
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		resp.Body.Close()
		if rerr != nil {
			lastErr = rerr
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if serr := sleepCtx(ctx, time.Duration(attempt+1)*800*time.Millisecond); serr != nil {
				return nil, serr // ★ D9
			}
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, rawURL)
		}
		return body, nil
	}
	return nil, lastErr
}

// getJSON 请求 JSON 接口（官方开放 API tier1 用）。
// ★ 官方 API 属合规开放接口，不受 robots.txt 约束（仅 tier2 受限网页抓取需遵循 robots）。
// 返回响应体字节。
func (f *fetchBase) getJSON(ctx context.Context, rawURL string) ([]byte, error) {
	return f.doGet(ctx, rawURL)
}

// crawlerUserAgent 采集器 UA 串；品牌 URL 仅在 BRAND_DOMAIN_SUFFIX 配置后附带。
func crawlerUserAgent() string {
	if d := strings.TrimSpace(os.Getenv("BRAND_DOMAIN_SUFFIX")); d != "" {
		return "TranslatorKBPackCrawler/1.0 (+https://" + d + "; 术语包自动采集)"
	}
	return "TranslatorKBPackCrawler/1.0"
}

// sleepCtx ★ D9（2026-09-12）：ctx 感知的重试退避——旧裸 Sleep 在客户端
// 断开后仍继续抓取/重试，白白消耗对端配额与本进程连接。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
