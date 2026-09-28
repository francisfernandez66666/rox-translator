// ============ register_guard.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// 注册防薅护栏：按「来源 IP／浏览器设备号／平台当日总名额」三档限制新账号（与登录暴力破解防护、
// 免登录试用 trial.go 同一思路、同一张 rate_limits 表）。
// 规则：
//   - 同一 IP 在 24 小时滑动窗口内最多注册 N 个账号（REGISTER_IP_DAILY > register_ip_daily_limit，默认 3）
//   - 同一 IP 两次注册的最小间隔（REGISTER_IP_MIN_INTERVAL > register_ip_min_interval_sec，默认 60 秒）
//   - ★ F-81（2026-09-28 用户令「注册免费账号也要和免费体验试用一样有防薅限制」）新增两档：
//     · 同一设备号 24h 内最多注册 regDeviceDailyDefault 个免费账号（REGISTER_DEVICE_DAILY > register_device_daily_limit）
//     —— 治的是「换 IP／换邮箱刷号」：代理池能换 IP、临时域名能换邮箱，唯有这台浏览器上的
//     localStorage 设备号换不掉（与试用面 lib/trialDevice.ts 同一份事实，刷过试用的人再来批量注册会被同一本账认出）。
//     · 平台每日新免费账号总名额（REGISTER_GLOBAL_DAILY > register_global_daily_limit，默认 500）
//     —— 每个免费账号＝一份 free_trial_tokens 额度＝一笔市场费用，必须有天花板；触顶发 register_budget 告警。
//
// 用途：注册接口在进入业务逻辑前调用 allow 拦 IP 档；确认本次要**新建免费租户**时调 reserveFreeAccount
// 原子占住设备／全局两档的格子（拿到 ticket），额度真正发出去后 ticket.markSpent() 坐实，
// 中途任何失败由 defer ticket.release() 退格；注册整体成功后再调 record 推进 IP 窗口。
// 整改 R-M8：计数优先落 SQLite（rate_limits 表），重启/多副本共享；Store 未就绪时回退内存。
// =============================================
package api

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/observability"
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

// ============ ★ F-81（2026-09-28）：免费账号的设备档与平台日预算档 ============
//
// 用户原话：「注册免费账号也是和免费体验试用一样有防薅限制。」
// 本文件原先只有 IP 一档，而 IP 恰恰是三档里**最容易被绕开**的一档：代理池换 IP、临时域名换邮箱
// 都是现成工具。真正换不掉的是这台浏览器 localStorage 里的设备号——它已经先被免登录试用记过一本账
// （lib/trialDevice.ts 与 trial_dev 同一份事实），所以「先刷 5 句试用、再批量注册领免费额度」
// 这类连号行为在设备档上是同一本账，不用重新发明标识。
// 平台日预算档治的是另一半：免费账号每个都自带一份 free_trial_tokens 额度，那是真金白银的市场费用，
// 没有天花板时刷号损失上不封顶（与 trial_day 同一口径：额度是费用，费用必须有顶）。
//
// 三档都写进同一张 rate_limits 表（重启／多实例共享），优先序按 AGENTS §一·3：
// 环境变量 > system_config 同名小写键 > 代码默认。
// ★ 记账口径：进入业务逻辑时先**原子占一格**，额度真正发出去才**坐实**（ticket.markSpent），
//
//	中途失败退格。占格与判据在同一条 UPDATE 里完成，所以并发请求不能同时读到「还差一格」；
//	坐实点放在建租户＋发额度之后而不是接口返回 200：被薅的是额度而不是那个状态码。
const (
	regScopeDevice = "reg_dev" // 设备号档：key = 浏览器设备号
	regScopeGlobal = "reg_day" // 平台日预算档：key 固定 regGlobalKey（整平台一份账）
	regGlobalKey   = "global"  // 平台档那份账的固定 key（平台级只有一条，不按任何用户维度分片）
	regWindowSec   = 86400     // 滚动 24h 窗口（与 trialWindowSec 同长度，判据共用 windowActiveIn）

	regDeviceDailyDefault = 3   // 每设备号 24h 内新建免费账号上限
	regGlobalDailyDefault = 500 // 平台每日新建免费账号总名额
	regIPDailyDefault     = 3   // 每 IP 24h 内注册上限（F-81 起可经环境变量覆盖）
	regIPMinIntervalDef   = 60  // 同 IP 两次注册最小间隔秒数
)

// windowActiveIn 判断 rate_limits 读到的窗口起点是否仍在长度 sec 的有效窗内。
// 判据与 trialWindowActive 完全同源（RateLoad 不清理过期窗口，只看 Count 会把「隔夜恢复额度」锁成永久耗尽），
// 这里只是把窗口长度做成参数，供注册侧与试用侧共用一份事实而不是各抄一遍。
func windowActiveIn(windowStart int64, sec int) bool {
	if windowStart <= 0 {
		return false
	}
	return time.Now().Unix()-windowStart < int64(sec)
}

// configIntTier 按 AGENTS §一·3 的优先序解析一档整数额度：环境变量 > system_config > 代码默认。
// allowZero=false 时 0 与非法值一样回落下一级（额度配成 0 多半是手滑，静默关闭功能比写错数字更难查）；
// allowZero=true 供「最小间隔」这类 0 有合法含义（不限制间隔）的档位使用。
func configIntTier(st *store.Store, envKey, cfgKey string, def int, allowZero bool) int {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && (allowZero || n > 0) {
			return n
		}
	}
	if st != nil {
		if v, err := st.GetConfig(cfgKey); err == nil {
			if n, perr := strconv.Atoi(strings.TrimSpace(v)); perr == nil && (allowZero || n > 0) {
				return n
			}
		}
	}
	return def
}

// freeAccountTicket 一次免费账号注册在设备档／平台档上**占住的那一格**。
//
// 为什么从「先判（allowFreeAccount）后记（recordFreeAccount）」改成「先占格、后坐实」：
// 两步式在并发下会被击穿——刷号脚本本来就是并发打的，一批请求会同时读到「还差一格」，
// 于是全部放行，上限实际变成 limit+N（C25 在邀请奖励上踩过的同一形态）。
// 现在占格子走 store.RateReserve（判据收在一条带条件的 UPDATE 里，影响 1 行才算抢到），
// 抢到之后有两条出路：
//   - markSpent()：额度真的发出去了 ⇒ 这一格从此坐实，不再退；
//   - release()：请求在额度发放之前就失败了（校验、建租户撞码等）⇒ 退一格，
//     免得把「系统这边没做成」的那几次也算进用户当日额度（那是误伤真人，不是防薅）。
//
// 两个方法都幂等，且调用侧一律写 defer release()＋成功后 markSpent()，
// 这样将来在中间插任何一条 return 分支都不会漏退格（漏退格＝用户被白扣额度，是最难查的一类投诉）。
type freeAccountTicket struct {
	g          *registerGuard
	device     string // 合规设备号（空串＝本笔没占设备档）
	heldDevice bool   // 设备档是否真占到了格子
	heldGlobal bool   // 平台档是否真占到了格子
	spent      bool   // 已坐实（额度已发出），release 不再退格
}

// markSpent 把占住的格子标记为「已消费」，此后 release 不再退格。
func (t *freeAccountTicket) markSpent() {
	if t != nil {
		t.spent = true
	}
}

// release 退回本笔占住、且尚未坐实的格子（defer 调用，可对 nil ticket 安全调用）。
func (t *freeAccountTicket) release() {
	if t == nil || t.spent {
		return
	}
	t.spent = true // 幂等闸门：即便被 defer 与显式调用各走一次，也只退一遍
	if t.g.st != nil {
		if t.heldDevice {
			if err := t.g.st.RateRelease(regScopeDevice, t.device, regWindowSec); err != nil {
				observability.Error(context.Background(), "注册防薅：设备档退格失败", "err", err.Error(), "device", t.device)
			}
		}
		if t.heldGlobal {
			if err := t.g.st.RateRelease(regScopeGlobal, regGlobalKey, regWindowSec); err != nil {
				observability.Error(context.Background(), "注册防薅：平台档退格失败", "err", err.Error())
			}
		}
		return
	}
	// 内存回退（Store 未就绪）：与 reserveMem 同一把锁，退格同样带防下溢保护
	t.g.mu.Lock()
	defer t.g.mu.Unlock()
	if t.heldDevice {
		t.g.bumpMem("dev:"+t.device, -1)
	}
	if t.heldGlobal {
		t.g.bumpMem(regGlobalKey, -1)
	}
}

// regRetryWaitIn 由触顶读到的窗口起点算「建议等待秒数」，最少 60 秒
// （回 0 会让前端把 retry_after 当"立刻再试"，脚本据此打得更密）。
func regRetryWaitIn(windowStart int64) int {
	return maxInt(regWindowSec-int(time.Now().Unix()-windowStart), 60)
}

// bumpMem 内存回退账本上推进 delta（+1 占格／-1 退格）。必须在持有 g.mu 时调用。
// 退格不许把计数写成负数：计数下溢等于把后续每一笔都放行。
func (g *registerGuard) bumpMem(key string, delta int) {
	now := time.Now()
	if delta < 0 {
		if a, ok := g.data[key]; ok && now.Sub(a.firstAt) <= 24*time.Hour && a.count > 0 {
			a.count--
			a.lastAt = now
		}
		return
	}
	if a, ok := g.data[key]; ok && now.Sub(a.firstAt) <= 24*time.Hour {
		a.count++
		a.lastAt = now
		return
	}
	g.data[key] = &regAttempt{count: 1, firstAt: now, lastAt: now}
}

// reserveFreeAccount 为「本次要新建免费账号」原子占住设备档与平台档各一格。
// 参数 device: 已过 trialDeviceIDRe 白名单的设备号（空串＝客户端没上报，只占平台档，见下）；
// devLimit/globalLimit: 两档上限（<=0 表示该档不生效，供测试与运营显式放开）。
// 返回: ticket=占格凭证（**放行时必为非 nil**，调用方必须 defer release＋额度发出后 markSpent）；
// retryAfterSec=被拒时建议等待秒数；reason=""|device|global（前端与告警据此分文案）。
//
// ★ 为什么 device 为空时**不**把它并进某个「匿名设备桶」：
// 那等于让「不带设备号」这个动作本身变成一条更紧的限流，老前端缓存包、脚本客户端、
// 管理台代客注册会一起被误伤（F-64/F-79 那批"对外契约改动引发现网故障"的同形教训）。
// 不带设备号的流量仍然吃 IP 档（默认 3 次/24h，比设备档更紧）＋邮箱验证＋人机验证＋一次性邮箱黑名单，
// 不是放开口子；设备档抓的是「换了 IP 但没换浏览器」这一类，两档各管各的。
//
// ★ 占格子这一步本身出错（数据库故障）时**放行**（fail-open）并打错误日志：
// 限流表坏了就把注册全关，等于把「防薅」升级成「全站不可用」——F-64 那批教训里，
// 对外契约层面的连带故障比多放走几个账号严重得多，而且 IP 档、邮箱验证、人机验证三道还在。
func (g *registerGuard) reserveFreeAccount(device string, devLimit, globalLimit int) (ticket *freeAccountTicket, retryAfterSec int, reason string) {
	t := &freeAccountTicket{g: g, device: device}
	if g.st != nil {
		if device != "" && devLimit > 0 {
			ok, st, err := g.st.RateReserve(regScopeDevice, device, regWindowSec, int64(devLimit))
			if err != nil {
				observability.Error(context.Background(), "注册防薅：设备档占格失败，本次放行", "err", err.Error(), "device", device)
			} else if !ok {
				return nil, regRetryWaitIn(st.WindowStart), "device"
			}
			t.heldDevice = err == nil && ok
		}
		if globalLimit > 0 {
			ok, st, err := g.st.RateReserve(regScopeGlobal, regGlobalKey, regWindowSec, int64(globalLimit))
			if err != nil {
				observability.Error(context.Background(), "注册防薅：平台档占格失败，本次放行", "err", err.Error())
			} else if !ok {
				// 平台档没抢到 ⇒ 先把已经占住的设备那一格退回，
				// 否则「一次失败尝试」会把用户当日额度磨光（真用户被误伤，脚本却无所谓）。
				t.release()
				return nil, regRetryWaitIn(st.WindowStart), "global"
			}
			t.heldGlobal = err == nil && ok
		}
		return t, 0, ""
	}
	// 内存回退（Store 未就绪）：key 加前缀隔离，避免与 IP 键撞车
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	// 判＋占在同一把锁里完成：进程内并发不会击穿（跨进程击穿由持久化那半边管）
	tryKey := func(key string, limit int) (bool, int) {
		a, exists := g.data[key]
		if !exists || now.Sub(a.firstAt) > 24*time.Hour {
			g.data[key] = &regAttempt{count: 1, firstAt: now, lastAt: now}
			return true, 0
		}
		if a.count >= limit {
			wait := int((24*time.Hour - now.Sub(a.firstAt)).Seconds())
			return false, maxInt(wait+1, 60)
		}
		a.count++
		a.lastAt = now
		return true, 0
	}
	if device != "" && devLimit > 0 {
		if ok, wait := tryKey("dev:"+device, devLimit); !ok {
			return nil, wait, "device"
		}
		t.heldDevice = true
	}
	if globalLimit > 0 {
		ok, wait := tryKey(regGlobalKey, globalLimit)
		if !ok {
			if t.heldDevice {
				t.g.bumpMem("dev:"+device, -1)
			}
			return nil, wait, "global"
		}
		t.heldGlobal = true
	}
	return t, 0, ""
}
