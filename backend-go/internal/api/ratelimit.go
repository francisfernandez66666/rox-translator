// ============ 本文件职责中文说明 ============
// 登录暴力破解防护：基于内存的滑动窗口失败计数。
// 规则：同一 IP 在固定窗口内连续失败达到阈值后进入冷却期；冷却期内直接拒绝登录。
// 用途：登录接口在进入业务逻辑前调用 loginLocked 拦截，失败时 recordLoginFail 计数，
// 成功时 clearLoginFails 清零。
// =============================================
package api

import (
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// 暴力破解防护参数
const (
	loginFailThreshold = 5   // 窗口内最多允许失败次数
	loginWindowSec     = 300 // 计数窗口：5 分钟
	loginCooldownSec   = 300 // 冷却期：5 分钟
)

// loginAttempt 单 IP 的失败计数与冷却状态
type loginAttempt struct {
	count       int       // 窗口内失败次数
	firstAt     time.Time // 窗口起始时间
	lockedUntil time.Time // 冷却截止时间（未冷却为零值）
}

// loginLimiter 登录失败限流器（并发安全）
type loginLimiter struct {
	mu   sync.Mutex
	data map[string]*loginAttempt // key: 客户端 IP
}

// newLoginLimiter 创建登录失败限流器。
func newLoginLimiter() *loginLimiter {
	return &loginLimiter{data: make(map[string]*loginAttempt)}
}

// blocked 判断指定 IP 是否处于冷却期（不可登录）。
// 参数 ip: 客户端 IP；返回 true 表示需要拒绝登录。
func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.data[ip]
	if !ok {
		return false
	}
	now := time.Now()
	// 已进入冷却期（lockedUntil 非零）且冷却期已过：清空计数并解除封锁
	if !a.lockedUntil.IsZero() && now.After(a.lockedUntil) {
		delete(l.data, ip)
		return false
	}
	// 冷却期有效期内：拒绝登录
	if !a.lockedUntil.IsZero() {
		return true
	}
	// 尚未达到阈值：仅失败计数，不封锁
	return false
}

// fail 记录一次登录失败；达到阈值则进入冷却期。
// 参数 ip: 客户端 IP。
func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.data[ip]
	if !ok {
		l.data[ip] = &loginAttempt{count: 1, firstAt: now}
		return
	}
	// 窗口过期则重置
	if now.Sub(a.firstAt) > loginWindowSec*time.Second {
		a.count = 0
		a.firstAt = now
	}
	a.count++
	// 达到阈值：进入冷却期
	if a.count >= loginFailThreshold {
		a.lockedUntil = now.Add(loginCooldownSec * time.Second)
	}
}

// clear 登录成功时清零该 IP 的失败记录。
// 参数 ip: 客户端 IP。
func (l *loginLimiter) clear(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.data, ip)
}

// trustProxyXFF 是否信任反向代理注入的 X-Forwarded-For（env TRUST_PROXY_XFF=1）。
// 惰性读取一次；仅在 Caddy/Nginx 反代部署时开启——直连暴露场景下开启会被
// 伪造头绕过限流，故默认关闭保持旧行为（评审 C4）。
var trustProxyXFF = os.Getenv("TRUST_PROXY_XFF") == "1"

// clientIP 提取客户端 IP（去掉端口；无则返回空串）。
//
// ★ 反代适配（2026-08-26 全仓评审 C4）：TRUST_PROXY_XFF=1 时取 X-Forwarded-For
//   第一跳（最左侧客户端地址，由可信反代追加）。此前恒用 RemoteAddr，Caddy 反代后
//   全体用户共享 127.0.0.1——一人爆破登录/注册，全站连坐进入冷却。
func clientIP(r *http.Request) string {
	if trustProxyXFF {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// XFF 格式："client, proxy1, proxy2"——第一跳即真实客户端
			first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
			if first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
