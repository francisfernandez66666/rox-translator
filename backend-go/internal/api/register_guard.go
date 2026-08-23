// ============ 本文件职责中文说明 ============
// 注册防薅护栏：基于内存的按 IP 注册频率限制（与登录暴力破解防护同思路）。
// 规则：
//   - 同一 IP 在 24 小时滑动窗口内最多注册 N 个账号（register_ip_daily_limit，默认 3）
//   - 同一 IP 两次注册的最小间隔（register_ip_min_interval_sec，默认 60 秒）
//
// 用途：注册接口在进入业务逻辑前调用 allowRegister 拦截，成功后 recordRegister 计数。
// 计数仅存内存，重启清零——防脚本批量薅试用额度足够，无需持久化。
// =============================================
package api

import (
	"sync"
	"time"
)

// regAttempt 单 IP 的注册计数状态
type regAttempt struct {
	count   int       // 24h 窗口内注册次数
	firstAt time.Time // 窗口起始时间
	lastAt  time.Time // 最近一次注册时间（最小间隔判断）
}

// registerGuard 注册限流器（并发安全）
type registerGuard struct {
	mu   sync.Mutex
	data map[string]*regAttempt // key: 客户端 IP
}

// newRegisterGuard 创建注册限流器。
func newRegisterGuard() *registerGuard {
	return &registerGuard{data: make(map[string]*regAttempt)}
}

// allow 判断该 IP 是否允许发起一次新注册。
// 参数 ip: 客户端 IP；dailyLimit: 24h 窗口内允许的注册次数上限；
// minIntervalSec: 两次注册最小间隔秒数。
// 返回: ok=是否放行；retryAfterSec=被拒时建议的重试等待秒数。
func (g *registerGuard) allow(ip string, dailyLimit, minIntervalSec int) (ok bool, retryAfterSec int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	a, exists := g.data[ip]
	if !exists {
		return true, 0
	}
	// 窗口过期（距首次注册超过 24h）：重置窗口
	if now.Sub(a.firstAt) > 24*time.Hour {
		delete(g.data, ip)
		return true, 0
	}
	// 最小间隔校验：距上次注册不足间隔秒数则拒绝
	if minIntervalSec > 0 {
		if wait := minIntervalSec - int(now.Sub(a.lastAt).Seconds()); wait > 0 {
			return false, wait
		}
	}
	// 当日次数校验：达到上限则拒绝并给出整点窗口剩余时间
	if dailyLimit > 0 && a.count >= dailyLimit {
		wait := 24*time.Hour - now.Sub(a.firstAt)
		return false, maxInt(int(wait.Seconds())+1, 60)
	}
	return true, 0
}

// record 登记一次成功的注册（窗口与计数推进）。
// 参数 ip: 客户端 IP。
func (g *registerGuard) record(ip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	if a, ok := g.data[ip]; ok && now.Sub(a.firstAt) <= 24*time.Hour {
		a.count++
		a.lastAt = now
		return
	}
	g.data[ip] = &regAttempt{count: 1, firstAt: now, lastAt: now}
}

// maxInt 返回两整数中的较大值。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
