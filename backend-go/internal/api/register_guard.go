// ============ register_guard.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 注册防薅护栏：按 IP 的注册频率限制（与登录暴力破解防护同思路）。
// 规则：
//   - 同一 IP 在 24 小时滑动窗口内最多注册 N 个账号（register_ip_daily_limit，默认 3）
//   - 同一 IP 两次注册的最小间隔（register_ip_min_interval_sec，默认 60 秒）
//
// 用途：注册接口在进入业务逻辑前调用 allowRegister 拦截，成功后 recordRegister 计数。
// 整改 R-M8：计数优先落 SQLite（rate_limits 表），重启/多副本共享；Store 未就绪时回退内存。
// =============================================
package api

import (
	"sync"
	"time"

	"translator/internal/store"
)

// regAttempt 单 IP 的注册计数状态（内存回退用）
type regAttempt struct {
	count   int       // 24h 窗口内注册次数
	firstAt time.Time // 窗口起始时间
	lastAt  time.Time // 最近一次注册时间（最小间隔判断）
}

// registerGuard 注册限流器（并发安全）
type registerGuard struct {
	st   *store.Store // 持久化后端（nil 时回退内存）
	mu   sync.Mutex
	data map[string]*regAttempt // key: 客户端 IP（内存回退）
}

// newRegisterGuard 创建注册限流器（传入 Store 以启用持久化护栏）。
func newRegisterGuard(st *store.Store) *registerGuard {
	return &registerGuard{st: st, data: make(map[string]*regAttempt)}
}

// allow 判断该 IP 是否允许发起一次新注册。
// 参数 ip: 客户端 IP；dailyLimit: 24h 窗口内允许的注册次数上限；
// minIntervalSec: 两次注册最小间隔秒数。
// 返回: ok=是否放行；retryAfterSec=被拒时建议的重试等待秒数。
func (g *registerGuard) allow(ip string, dailyLimit, minIntervalSec int) (ok bool, retryAfterSec int) {
	if g.st != nil {
		now := time.Now().Unix()
		if minIntervalSec > 0 {
			if stInt, _ := g.st.RateLoad("guard_int", ip); stInt.WindowStart > 0 {
				if wait := minIntervalSec - int(now-stInt.WindowStart); wait > 0 {
					return false, wait
				}
			}
		}
		if dailyLimit > 0 {
			stDay, _ := g.st.RateLoad("guard_day", ip)
			cnt := stDay.Count
			if now-stDay.WindowStart >= 86400 {
				cnt = 0
			}
			if cnt >= int64(dailyLimit) {
				wait := 86400 - int(now-stDay.WindowStart)
				if wait < 60 {
					wait = 60
				}
				return false, wait
			}
		}
		return true, 0
	}
	// 内存回退
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	a, exists := g.data[ip]
	if !exists {
		return true, 0
	}
	if now.Sub(a.firstAt) > 24*time.Hour {
		delete(g.data, ip)
		return true, 0
	}
	if minIntervalSec > 0 {
		if wait := minIntervalSec - int(now.Sub(a.lastAt).Seconds()); wait > 0 {
			return false, wait
		}
	}
	if dailyLimit > 0 && a.count >= dailyLimit {
		wait := 24*time.Hour - now.Sub(a.firstAt)
		return false, maxInt(int(wait.Seconds())+1, 60)
	}
	return true, 0
}

// record 登记一次成功的注册（窗口与计数推进）。
// 参数 ip: 客户端 IP。
func (g *registerGuard) record(ip string) {
	if g.st != nil {
		// guard_int：最近动作时间（窗口极短以每次刷新 WindowStart）
		g.st.RateRecord("guard_int", ip, 1)
		// guard_day：24h 日计数（窗口过期自动重置）
		g.st.RateRecord("guard_day", ip, 86400)
		return
	}
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
