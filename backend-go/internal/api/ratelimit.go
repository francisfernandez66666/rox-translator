// ============ ratelimit.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 登录暴力破解防护：基于内存的滑动窗口失败计数。
// 规则：同一 IP 在固定窗口内连续失败达到阈值后进入冷却期；冷却期内直接拒绝登录。
// 用途：登录接口在进入业务逻辑前调用 loginCooldownSec 取剩余冷却秒数（>0 即以 429 +
// Retry-After 拒绝，★ F-47 批 I-7：旧版只返回 bool 且实际发 400，注释与行为不符已一并纠正），
// 失败时 recordLoginFail 计数，成功时 clearLoginFails 清零。
// =============================================
package api

import (
	"net/http"
	"sync"
	"time"

	"translator/internal/httpx"
	"translator/internal/store"
)

// 暴力破解防护参数
const (
	loginFailThreshold = 5   // 窗口内最多允许失败次数
	loginWindowSec     = 300 // 计数窗口：5 分钟
	loginCooldownSec   = 300 // 冷却期：5 分钟
)

// ★ IAM P0-6（2026-10-21）：按**账号**并行的失败锁定（与按 IP 的那一档各记一本账）。
// 存在的理由：IP 档只能治"同一来源打得凶"，而分布式/代理池爆破每个 IP 只打两三次就换，
// 每条腿都在阈值以内——真正被打的是一个账号名。账号档与 IP 档**并行**（任一触发即拒），
// 但不互相顶替：只按账号锁会让"一个公共出口 NAT 后面多人同时输错"误伤同办公室的人，
// 只按 IP 锁则挡不住换 IP 的定向爆破。
const loginAcctScope = "login_acct"

// loginIPScope 按 IP 那一档的 scope 名（rate_limits.scope 列；内存回退的 map 键前缀同源）。
// ★ 2026-10-21：单独提成常量而不是散在 fail/clear/retryAfterSec 里各写一遍字面量——
// 内存回退的键形是 `scope + "|" + key`，一旦三处里有任何一处拼错，冷却项就记在另一个键上，
// 表现是「锁不住」而日志全绿。夹具要伪造冷却时刻时也按这个键取（见 login_f47_ratelimit_test.go）。
const loginIPScope = "login_fail"

// loginAttempt 单 IP 的失败计数与冷却状态（内存回退用）
type loginAttempt struct {
	count       int       // 窗口内失败次数
	firstAt     time.Time // 窗口起始时间
	lockedUntil time.Time // 冷却截止时间（未冷却为零值）
}

// loginLimiter 登录失败限流器（并发安全）
type loginLimiter struct {
	st   *store.Store // 持久化后端（nil 时回退内存）
	mu   sync.Mutex
	data map[string]*loginAttempt // key: 客户端 IP（内存回退）
}

// newLoginLimiter 创建登录失败限流器（传入 Store 以启用持久化护栏）。
func newLoginLimiter(st *store.Store) *loginLimiter {
	return &loginLimiter{st: st, data: make(map[string]*loginAttempt)}
}

// blocked 判断指定 IP 是否处于冷却期（不可登录）。
// 参数 ip: 客户端 IP；返回 true 表示需要拒绝登录。
// ★ F-47（批 I-7）：判据收敛到 retryAfterSec——旧写法与冷却时长各算一遍，
// 两处一旦口径分家就会出现「说被锁了但 retry_after=0」这种自相矛盾的响应。
func (l *loginLimiter) blocked(ip string) bool {
	return l.retryAfterSec(ip) > 0
}

// retryAfterSec 返回该 IP 登录冷却的剩余秒数（0＝当前不受限）。
// ★ F-47（批 I-7）：429 不带时长＝让客户端瞎猜退避窗口——猜短了继续撞闸
// （并把 5 分钟失败窗口一路往后推，用户越试越久），猜长了真人白等。
// 持久化后端与内存回退两条路都要给同一个口径，故与 blocked 共用本函数。
func (l *loginLimiter) retryAfterSec(ip string) int {
	return l.retryAfterSecIn(loginIPScope, ip)
}

// retryAfterSecIn 按 scope+key 取冷却剩余秒数（★ IAM P0-6：IP 档与账号档共用这一条实现）。
// 两档各占一张账（rate_limits 的 scope 列），调用方只决定用哪个 scope，
// 冷却时长与「至少 1 秒」的口径不再各写一遍。
func (l *loginLimiter) retryAfterSecIn(scope, key string) int {
	if l.st != nil {
		st, _ := l.st.RateLoad(scope, key)
		return secsUntilUnix(st.LockUntil)
	}
	mk := scope + "|" + key
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.data[mk]
	if !ok || a.lockedUntil.IsZero() {
		return 0
	}
	remain := secsUntilUnix(a.lockedUntil.Unix())
	if remain == 0 {
		delete(l.data, mk) // 冷却已届满顺手清内存项（沿用旧 blocked 的清理时机，防无界增长）
	}
	return remain
}

// acctRetryAfterSec 返回该账号登录冷却的剩余秒数（0＝当前不受限）。
// account 必须是已归一化的账号（调用方用 auth.NormalizeAccount），
// 否则 "Alice" 与 "alice" 会各记一本账，爆破者只换大小写就绕过账号档。
func (l *loginLimiter) acctRetryAfterSec(account string) int {
	return l.retryAfterSecIn(loginAcctScope, account)
}

// secsUntilUnix Unix 秒级截止时刻距今的剩余秒数：已过期/零值回 0，未来时刻至少回 1。
// 「至少 1」是必需的——同一秒内截断成 0 会让 HTTP 头变成 `Retry-After: 0`，
// 语义上等于「现在就能再试」，与判据本身（仍在锁内）冲突。
func secsUntilUnix(until int64) int {
	if until <= 0 {
		return 0
	}
	remain := until - time.Now().Unix()
	if remain <= 0 {
		return 0
	}
	return int(remain)
}

// fail 记录一次登录失败；达到阈值则进入冷却期。
// 参数 ip: 客户端 IP。
func (l *loginLimiter) fail(ip string) {
	l.failIn(loginIPScope, ip)
}

// acctFail 记录一次**按账号**的登录失败（阈值与冷却同 IP 档）。
func (l *loginLimiter) acctFail(account string) {
	l.failIn(loginAcctScope, account)
}

// failIn 按 scope+key 记一次失败并在达阈值时上锁（★ IAM P0-6：IP 档与账号档共用）。
func (l *loginLimiter) failIn(scope, key string) {
	if l.st != nil {
		st, _ := l.st.RateRecord(scope, key, loginWindowSec)
		if st.Count >= loginFailThreshold {
			l.st.RateSetLock(scope, key, time.Now().Unix()+loginCooldownSec)
		}
		return
	}
	mk := scope + "|" + key
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.data[mk]
	if !ok {
		l.data[mk] = &loginAttempt{count: 1, firstAt: now}
		return
	}
	if now.Sub(a.firstAt) > loginWindowSec*time.Second {
		a.count = 0
		a.firstAt = now
	}
	a.count++
	if a.count >= loginFailThreshold {
		a.lockedUntil = now.Add(loginCooldownSec * time.Second)
	}
}

// clear 登录成功时清零该 IP 的失败记录。
// 参数 ip: 客户端 IP。
func (l *loginLimiter) clear(ip string) {
	l.clearIn(loginIPScope, ip)
}

// acctClear 登录成功时清零该账号的失败记录。
func (l *loginLimiter) acctClear(account string) {
	l.clearIn(loginAcctScope, account)
}

// clearIn 按 scope+key 清零失败记录（★ IAM P0-6）。
// 只在**登录成功那一刻**调用：清早了等于把"试错"变成免费重试；
// 两档各清各的，谁成功都不替另一档背书（IP 档清的是这台机器，账号档清的是这个号）。
func (l *loginLimiter) clearIn(scope, key string) {
	if l.st != nil {
		l.st.RateReset(scope, key)
		return
	}
	mk := scope + "|" + key
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.data, mk)
}

// clientIP 提取客户端 IP（去掉端口；无则返回空串）。
//
// ★ 2026-10-30 三事合一批（组2a）：实现收拢到 `internal/httpx.ClientIP`（全仓唯一一把尺子，K7 按
//
//	「谁读 X-Forwarded-For」的**全集**机械守住）。这里保留同名薄委托，是为了让本包 50 多个调用点零改动。
//	旧形态的两条缺陷一并被那一个口治掉：① `TRUST_PROXY_XFF=1` 时**只看开关、不看这一枪从哪儿来**
//	（直连暴露或本地跑套件时，一个 `X-Forwarded-For: 1.2.3.4` 就能把自己伪装成新访客）；
//	② 那个 env 现在**不再被读**（信任改按"直连对端是不是本机回环"判，见 httpx 文件头）——
//	现网 Caddy 已把 XFF 重写成 Cloudflare 覆写过的 `CF-Connecting-IP`，所以结论与旧行为一致，
//	`deploy/systemd/prod.conf` 里那一行随之成为空配置，同批注掉而不是留着让人以为它还在管。
func clientIP(r *http.Request) string { return httpx.ClientIP(r) }
