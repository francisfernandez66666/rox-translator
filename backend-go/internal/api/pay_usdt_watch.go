// ============ 本文件职责中文说明 ============
// USDT 链上对账器（M2，改造方案 2026-09-15）：
//
//	周期任务（默认 30s，runExclusive 单跑者）扫描启用链上收款地址的 USDT 入账：
//	  ① 拉取事件 → usdt_deposits 幂等落库（(chain,tx_hash,log_index) 唯一键）；
//	  ② 确认数达标（各链阈值配置）的未匹配入账 → 按「含尾数精确金额」匹配 pending usdt 单；
//	  ③ 唯一命中 → MarkOrderPaidByOrderNo（复用单事务幂等抢占）+ payments.tx_hash +
//	     结算快照 + 游标推进；多命中/无命中 → 不自动入账（人工裁决），超期孤儿 critical 告警。
//	开关：usdt_enabled=1 且 usdt_auto_settle=1 才运转（★ 默认关闭——真链冒烟通过前
//	仅人工核销轨生效，自动入账路径保持休眠）。
//	RPC 端点：env USDT_TRON_BASE（TronGrid 兼容，默认 api.trongrid.io）、
//	TRONGRID_API_KEY、USDT_ETH_RPC、USDT_BSC_RPC、USDT_EVM_CONTRACT_<CHAIN>（可注入 mock）。
//
// =============================================
package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/observability"
	"translator/internal/payment"
	"translator/internal/store"
)

// ============ ⑮ 监听存活态（2026-10-05 第 3 波）============
//
// 缺陷本体：TronGrid 的链头端点被裸打 `/v1/blocks` 恒回 404，对账器每 30s 失败一次、
// 8 天零成功，而失败只落一行 log.Printf——不进 alerts、不进 /api/health、不熔断；
// 同一时刻收银台还在向客户承诺「达到确认数后自动入账」⇒ **对外承诺与内部能力脱节**。
// 本段把"监听到底活不活"变成一个**有名字的状态词**，并让收银台那句承诺跟着它走。
//
// 状态词四档（只出状态词，绝不出收款地址/Key/上游域名，同 AGENTS §一·12 纪律）：
//
//	disabled 开关没开（监听根本没在跑）——收银台本来就不承诺自动
//	unknown  开关开着但还没有一轮完整读数（进程刚起，第一 tick 未到）⇒ 不承诺自动
//	ok       最近一轮所有启用链都取到链头 ⇒ 才允许承诺"达到确认数后自动入账"
//	failing  任一启用链最近一轮失败（1 轮即翻；对客承诺当场停用）——连续 ≥usdtWatchFailAlert
//	         轮还落一条 usdt_watch_dead critical 告警，见 word() 那条两阈值说明
// ===========================================================

// usdtWatchFailAlert 连续失败多少轮判定"监听已死"并落告警（3 轮＝默认 30s 间隔下约 90s）。
// ★ 这只管**告警**；收银台停承诺走的是 failing（1 轮即停），别让两者共用一个阈值——
//
//	共用的话，故障头两轮客户看到的还是"自动入账"，而这两轮足够有人把钱打进来。
const usdtWatchFailAlert = 3

// usdtWatchAlertKind 平台级告警类型（对外排障契约，逐字钉在用例里）。
const usdtWatchAlertKind = "usdt_watch_dead"

// 三条收敛腿的档名（★ 内部排障契约：只进日志的 why 字段，不进对外文案；逐字钉在用例里）。
//
//	① in_process_recovery      本进程内死过又活了；
//	② post_restart_reconcile   换件／重启之后才好的（(53) 补的那条腿）；
//	③ chain_unlisted           挂过的那条链已被运营**从 usdt_chains 里删掉**（★ (54)）——
//	   这一条**不是恢复**，是"那条告警不再是关于这台的事实"，所以日志文案单独走一档（见 resolveOpenWatchAlerts）。
const (
	usdtWatchWhyInProcessRecovery = "in_process_recovery"
	usdtWatchWhyPostRestart       = "post_restart_reconcile"
	usdtWatchWhyChainUnlisted     = "chain_unlisted"
)

// 状态词常量（对外契约：现网判据与收银台联动都按这几个字面量比）
const (
	usdtWatchDisabled = "disabled"
	usdtWatchUnknown  = "unknown"
	usdtWatchOK       = "ok"
	usdtWatchFailing  = "failing"
)

// usdtWatchState 监听存活态。
// ★ 必须带锁：写方是对账周期协程，读方是 /api/health 与收银台下单（另一个请求协程），
//
//	裸 map 并发读写是 runtime fatal error（不是 panic，recover 兜不住、进程直接挂）。
type usdtWatchState struct {
	mu         sync.Mutex
	fails      map[string]int // 链 → 连续失败轮数（成功即清零）
	everRan    bool           // 是否跑过至少一轮（没跑过＝unknown，不许谎报 ok）
	wasFailing bool           // 上一档是否处于 failing（用于恢复时收敛告警）
	// ★ (53) 2026-10-10 现网实证补的腿：本进程有没有把"库里遗留的 open 行"核过一次。
	//	wasFailing 是**进程态**，换件/重启即清零，而告警行在**库里**——
	//	现网读数：10-06 12:17 落的 usdt_watch_dead 一直 open，而 10-10 复查时监听已 ok，
	//	于是告警中心长期挂着一条"监听已死"的 critical 对着一个健康的事实 ⇒ 假告警。
	//	每进程只核一次（别每 30s 查一遍库），且**只在真的健康那一轮**才消费这一档。
	alertReconciled bool
}

// usdtWatch 进程内单例（对账器本身也是单进程周期任务，没有第二份状态）
var usdtWatch = &usdtWatchState{fails: map[string]int{}}

// note 记录一轮某链的成败；返回本轮是否**首次**判定"监听已死"（只有首次才落告警，避免堆行）
func (w *usdtWatchState) note(chain string, err error) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.everRan = true
	if err == nil {
		w.fails[chain] = 0
		return false
	}
	w.fails[chain]++
	// 阈值那一轮：把"曾经死过"记在状态上，恢复腿（tookRecovery）才有得比对。
	// 只靠 CreateAlert 的返回值判恢复是不行的——告警行在库里，状态却不知道该收敛谁。
	if w.fails[chain] == usdtWatchFailAlert {
		w.wasFailing = true
		return true
	}
	return false
}

// markDisabled 开关关闭时把状态收敛到 disabled（并清掉历史失败计数，防止重新开闸后立刻误报 failing）
func (w *usdtWatchState) markDisabled() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fails = map[string]int{}
	w.everRan = false
	w.wasFailing = false
	// ★ (53)：重新开闸＝新的一轮监控周期，遗留告警那一次核对要重做（否则关一开就把补腿永久吃掉）
	w.alertReconciled = false
}

// word 当前状态词；chains 为本轮应参与的链（跑过但没链可跑＝unknown）。
// ★ 两个阈值刻意不同，别混成一条（⑮ 的判据本体）：
//
//	状态词 failing＝**任一启用链最近一轮失败**（1 轮即翻）——它直接管着收银台那句
//	  「达到确认数后自动入账」的对客承诺，多等两轮就等于多收两笔"转了账不会入账"的钱；
//	落告警 usdt_watch_dead＝**连续 ≥usdtWatchFailAlert 轮**（3 轮）——一次网络抖动
//	  不该在告警中心刷屏，判"死"要的是持续性。
func (w *usdtWatchState) word(ran bool, chains []string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !ran {
		return usdtWatchDisabled
	}
	if !w.everRan || len(chains) == 0 {
		return usdtWatchUnknown
	}
	for _, c := range chains {
		if w.fails[c] > 0 {
			return usdtWatchFailing
		}
	}
	return usdtWatchOK
}

// tookRecovery 判断"从告警档回到健康"这一刻（返回 true 时同时清掉标记），用于收敛 open 告警行。
// ★ 这里比的是 **usdtWatchFailAlert 档**（告警那一档），不是 failing（1 轮那一档）——
//
//	两条腿的阈值本来就不同：告警按持续性生灭，对客承诺按最近一轮成败生灭。
func (w *usdtWatchState) tookRecovery() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.wasFailing {
		return false
	}
	for _, n := range w.fails {
		if n >= usdtWatchFailAlert {
			return false // 还有链停在 failing 档上，不收敛
		}
	}
	w.wasFailing = false
	return true
}

// alertReconcilePending 纯内存快判：本进程还有没有"库里遗留的 open 行"这一课要补。
// ★ 刻意**不读链清单、不查库**——它是那道"要不要为此多读一次 system_config"的门闩，
//
//	读库与收敛都归下面那一个带 chains 的判据管（(53) 的开销口径：每进程最多一次）。
func (w *usdtWatchState) alertReconcilePending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.alertReconciled
}

// takeAlertReconcile 领取"本进程唯一一次遗留告警核对"的资格：
// 只有**跑过一轮、链清单非空、且全部已记录链当前都无失败**时才发，发了即消费（不再发第二次）。
// ★ 三条不放行的形态各有意图：
//
//	没跑过（everRan=false）⇒ 此刻对监听一无所知，"没有 open 行可关"与"还不知道该不该关"是两件事；
//	链清单空 ⇒ 与 word() 同档，回落 unknown，不许拿空清单当"全链健康"；
//	任一链有失败计数 ⇒ 那条 open 告警可能正是它挂的，现在关掉就是把真故障洗绿。
func (w *usdtWatchState) takeAlertReconcile(chains []string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.alertReconciled {
		return false
	}
	if !w.everRan || len(chains) == 0 {
		return false
	}
	// 判"全链无失败"按**全部已记录链**数，不只按当前清单：运营刚改过 usdt_chains 时，
	// 清单外那条留下的失败计数照样说明"监听不是全绿的"，此刻关掉告警就是把真故障洗绿。
	for _, n := range w.fails {
		if n > 0 {
			return false
		}
	}
	w.alertReconciled = true
	return true
}

// forgetUnlisted 丢掉**已不在保留名单里**的链的失败计数；返回"是否有曾达告警档的链被丢掉"。
// 保留名单由调用方给（见 usdtListedChains：本轮实际监听的链 ∪ usdt_chains 原文里还写着的名）——
// ★ (54)：这一条补的是 (53) 自己留下的洞。note() 只按清单里的链调用，所以一条链掉出
//
//	usdt_chains 之后，它在 w.fails 里那个 ≥3 的计数**再也不会被写、也永远不会被清零**——
//	于是三条腿全被顶死：tookRecovery 见 n≥阈值 永假、takeAlertReconcile 见 n>0 永假、
//	而 word() 只扫当前清单 ⇒ 它同时报 ok 并继续承诺自动入账，库里那行 usdt_watch_dead
//	却任何一条腿都关不掉（旧写法只能靠"把总开关关一轮"来清，等于用关闸排一次配置漂移）。
//
// 取向：链既然既不被监听、也不再写在配置里，那条告警就**不再是关于这台的事实**；
// 收敛它必须出声（调用方走 why=chain_unlisted 的 WARN，**不写成"已恢复"**），不是把故障洗绿。
// ⚠️ 名单里必须带上"配置里还写着但没配收款地址"的那条链（这正是 usdtListedChains 存在的原因）：
// 那种形态是配置故障、不是运营撤链，告警必须留着等地址补回来。
func (w *usdtWatchState) forgetUnlisted(chains []string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	keep := make(map[string]bool, len(chains))
	for _, c := range chains {
		keep[c] = true
	}
	droppedAlerted := false
	for c, n := range w.fails {
		if keep[c] {
			continue
		}
		if n >= usdtWatchFailAlert {
			droppedAlerted = true
		}
		delete(w.fails, c)
	}
	return droppedAlerted
}

// usdtWatchHealthWord /api/health 的状态词读点（只读，不触发任何上游调用）
func (s *Server) usdtWatchHealthWord() string {
	if s.Store == nil {
		return usdtWatchDisabled
	}
	cfg := s.Store.GetUSDTCfg()
	if !cfg.Enabled || !cfg.AutoSettle {
		return usdtWatchDisabled
	}
	return usdtWatch.word(true, cfg.Chains)
}

// usdtWatchAllowsAutoPromise 收银台能不能对客户承诺"自动入账"：开关开着 **且** 监听自己是健康的。
// unknown/failing 一律不承诺——拿"刚启动还没探过"当"没问题"，就是把 ⑮ 的假承诺原样重写一遍。
func (s *Server) usdtWatchAllowsAutoPromise() bool {
	if s.Store == nil {
		return false
	}
	cfg := s.Store.GetUSDTCfg()
	if !cfg.Enabled || !cfg.AutoSettle {
		return false
	}
	return usdtWatch.word(true, cfg.Chains) == usdtWatchOK
}

// usdtWatchReportRound 一轮某链对账结束后的留痕与告警：
// 首次判定死 ⇒ critical 告警（CreateAlert 自带同 kind open 去重，不会堆行）；恢复 ⇒ 收敛 open 行。
func (s *Server) usdtWatchReportRound(chain string, err error) {
	if usdtWatch.note(chain, err) && s.Store != nil {
		// message 不含上游域名与收款地址：这条会进管理台告警中心
		_ = s.Store.CreateAlert(0, "critical", usdtWatchAlertKind,
			fmt.Sprintf("USDT 链上到账监听已连续 %d 轮取不到 %s 链头，自动入账实际未在工作："+
				"收银台已停用自动入账承诺，请核对链上端点配置（env USDT_TRON_BASE / USDT_*_RPC）",
				usdtWatchFailAlert, chain))
		observability.Error(context.Background(), "USDT 到账监听连续失败达阈值",
			"chain", chain, "rounds", usdtWatchFailAlert)
	}
	if err != nil || s.Store == nil {
		return
	}
	// 恢复腿有**两条**，各堵一种"死而复愈"，缺一条就有一类告警永不收敛：
	// ① tookRecovery＝本进程内死过又活了（⑮ 原来只有这一条）；
	// ② takeAlertReconcile＝**换件/重启之后**才好的——wasFailing 是进程态，重启即清零，
	//    而库里那行 open 不认识这次重启（现网实证 (53)：10-06 12:17 落的那条 open 行，
	//    到 10-10 复查时监听已连跑多轮 ok，它还在告警中心挂着）。
	if usdtWatch.tookRecovery() {
		s.resolveOpenWatchAlerts(usdtWatchWhyInProcessRecovery)
		return
	}
	if usdtWatch.alertReconcilePending() {
		// 快判只读内存；真要领资格得拿链清单，于是这里才多读一次配置（每进程最多一次）
		if usdtWatch.takeAlertReconcile(s.Store.GetUSDTCfg().Chains) {
			s.resolveOpenWatchAlerts(usdtWatchWhyPostRestart)
		}
	}
}

// resolveOpenWatchAlerts 把库里 open 的 usdt_watch_dead 行逐条收敛，并留一行可 grep 的恢复读数。
// why 是**内部排障档名**（见上面那三个常量），只进日志不进对外文案；
// 三条腿共用这一份实现，避免"其中一条被改坏、另一条还绿"的假绿（同一句 ResolveAlert 抄两遍迟早分叉）。
// ★ (54)：chain_unlisted 那一档**不许写成"监听已恢复"**——监听并没有恢复，是那条链被从配置里删掉了。
// 把"撤掉一条链"记成"恢复了"，下一次同样的故障就藏在一条假的健康读数后面（人只会去 grep「已恢复」）。
func (s *Server) resolveOpenWatchAlerts(why string) {
	rows := s.listOpenWatchAlerts()
	for _, a := range rows {
		if aerr := s.Store.ResolveAlert(a.ID); aerr != nil {
			observability.Warn(context.Background(), "USDT 监听告警收敛失败",
				"id", a.ID, "why", why, "err", aerr.Error())
		}
	}
	if len(rows) == 0 {
		return
	}
	if why == usdtWatchWhyChainUnlisted {
		observability.Warn(context.Background(), "USDT 到账监听告警因链清单收窄而收敛（非恢复，请核配置）",
			"why", why, "resolved", len(rows))
		return
	}
	observability.Info(context.Background(), "USDT 到账监听已恢复",
		"why", why, "resolved", len(rows))
}

// listOpenWatchAlerts 查平台级 open 状态的 usdt_watch_dead 告警行
func (s *Server) listOpenWatchAlerts() []*store.Alert {
	alerts, err := s.Store.ListAlerts(0, "open", 200)
	if err != nil {
		return nil
	}
	var out []*store.Alert
	for _, a := range alerts {
		if a != nil && a.Kind == usdtWatchAlertKind {
			out = append(out, a)
		}
	}
	return out
}

// usdtListedChains "这条链还挂在配置里"的完整名单：本轮实际监听的链 ∪ usdt_chains 原文里的名。
//
//	两份名单都要，是因为 store.GetUSDTCfg 会把**没配收款地址**的链从 Chains 里剔掉——
//	那条链这时既不被扫描、又还写在配置里，语义是"这台本该收这条链的钱却配坏了"，
//	不是"运营撤掉了这条链"。拿 Chains 单当保留名单就会把一次配置故障收敛成一条已解决的告警
//	（★ (54)：这一族"洗绿"比"永不收敛"更贵——前者让人以为处理过了）。
//	归一化口径与 store.GetUSDTCfg 一致（去空白＋小写、跳过空段），
//	这一份**只用来判"名字还在不在"**，不重算优先级、不重算地址有效性（那一份判定只在那一个函数里）。
func (s *Server) usdtListedChains(cfg *store.USDTSettings) []string {
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		c = strings.TrimSpace(strings.ToLower(c))
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}
	for _, c := range cfg.Chains {
		add(c)
	}
	raw, _ := s.Store.GetConfig("usdt_chains")
	for _, c := range strings.Split(raw, ",") {
		add(c)
	}
	return out
}

// usdtDefaultContract 各链 USDT 合约（生产公开地址；mock 冒烟用 env 覆盖）。
var usdtDefaultContract = map[string]string{
	"erc20": "0xdAC17F958D2ee523a2206206994597C13D831ec7",
	"bep20": "0x55d398326f99059fF775485246999027B3197955",
}

// startUSDTReconciler 启动链上对账周期任务（server.NewServer 尾部调用；开关关闭时循环空转成本可忽略）。
func (s *Server) startUSDTReconciler() {
	go func() {
		interval := 30 * time.Second
		if v := os.Getenv("USDT_SCAN_INTERVAL_SEC"); v != "" {
			if n, e := strconv.Atoi(v); e == nil && n >= 5 {
				interval = time.Duration(n) * time.Second
			}
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			s.runExclusive("usdt-reconcile", interval-time.Second, s.usdtReconcileTick)
		}
	}()
}

// usdtReconcileTick 单轮对账（任一链失败不影响其余链）。
func (s *Server) usdtReconcileTick() {
	// ★ P0 防线（2026-09-16 双实例 e2e 实测）：退化组装（Store=nil，PG 下现已 fail-fast
	//   拒绝启动，此处兜底 sqlite 开发场景）下周期协程空指针会杀掉整个进程——
	//   USDT 对账属旁路任务，绝不允许其存活态威胁主服务。
	if s.Store == nil {
		return
	}
	cfg := s.Store.GetUSDTCfg()
	if !cfg.Enabled || !cfg.AutoSettle {
		usdtWatch.markDisabled() // 开关关着就别说"健康"，也别留着上一轮的失败计数
		return                   // 自动对账默认关闭：真链冒烟通过前不产生任何自动入账
	}
	// ★ (54)：先清掉「运营已经从 usdt_chains 里删掉的链」留下的失败计数，再谈本轮成败。
	//	不清这一笔的后果不是难看，是**三条恢复腿同时被顶死**：note() 只按清单里的链调用，
	//	掉出清单那条链的计数（可能已经 ≥usdtWatchFailAlert）从此既没人写、也没人清零 ⇒
	//	tookRecovery 永假、takeAlertReconcile 永假，而 word() 只扫当前清单照样报 ok——
	//	于是库里那行 usdt_watch_dead 谁都合不掉，旧写法只能靠"把总开关关一整轮"来清
	//	（拿关闸排一次配置漂移，等于让运营用停机来消一条告警）。
	if droppedAlerted := usdtWatch.forgetUnlisted(s.usdtListedChains(cfg)); droppedAlerted {
		s.resolveOpenWatchAlerts(usdtWatchWhyChainUnlisted)
	}
	for _, chain := range cfg.Chains {
		if err := s.usdtScanChain(chain, cfg); err != nil {
			log.Printf("[usdt-watch] %s 扫描失败: %v", chain, err)
			s.usdtWatchReportRound(chain, err) // ★ ⑮：失败不再只留一行日志
			continue
		}
		s.usdtWatchReportRound(chain, nil)
	}
	// 孤儿入账告警（>72h 未匹配，人工退款/豁免处置）
	for _, d := range s.Store.ListUnmatchedDeposits(50) {
		if t, e := time.Parse(time.RFC3339, d.SeenAt); e == nil && time.Since(t) > 72*time.Hour {
			_ = s.Store.CreateAlert(0, "critical", "usdt_orphan",
				fmt.Sprintf("USDT 孤儿入账超 72h 未匹配订单：%s %s 金额 %s（from %s），请人工裁决退款或豁免",
					d.Chain, d.TxHash, payment.FormatUSDTMicro(d.AmountMicro), d.FromAddr))
		}
	}
}

// usdtFetcherFor 按链构建拉取器（端点可注入，冒烟用 mock_chain）。
func usdtFetcherFor(chain string) payment.DepositFetcher {
	switch chain {
	case "trc20":
		base := os.Getenv("USDT_TRON_BASE")
		if base == "" {
			base = "https://api.trongrid.io"
		}
		return &payment.TronFetcher{Base: base, APIKey: os.Getenv("TRONGRID_API_KEY")}
	case "erc20", "bep20":
		rpc := os.Getenv("USDT_" + map[string]string{"erc20": "ETH", "bep20": "BSC"}[chain] + "_RPC")
		contract := os.Getenv("USDT_EVM_CONTRACT_" + chain)
		if contract == "" {
			contract = usdtDefaultContract[chain]
		}
		return &payment.EVMFetcher{RPC: rpc, Contract: contract, Chain: chain}
	}
	return nil
}

// usdtScanChain 扫描一条链：拉取→落账→匹配→结算。
func (s *Server) usdtScanChain(chain string, cfg *store.USDTSettings) error {
	f := usdtFetcherFor(chain)
	if f == nil {
		return fmt.Errorf("无 %s 拉取器", chain)
	}
	addr := cfg.Addrs[chain]
	cursor := s.Store.USDTDepositCursor(chain)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	deps, newest, err := f.FetchDeposits(ctx, addr, cursor)
	if err != nil {
		return err
	}
	confMin := cfg.Confirm[chain]
	if confMin <= 0 {
		confMin = 19
	}
	for _, d := range deps {
		dep := &store.USDTDeposit{Chain: d.Chain, TxHash: d.TxHash, LogIndex: d.LogIndex,
			FromAddr: d.FromAddr, AmountMicro: d.AmountMicro, BlockNo: d.BlockNo, NewestBlockNo: d.NewestBlockNo}
		fresh, ierr := s.Store.InsertUSDTDeposit(dep)
		if ierr != nil {
			log.Printf("[usdt-watch] 入账落库失败 %s/%s: %v", d.Chain, d.TxHash, ierr)
			continue
		}
		_ = fresh // 已存在（重复扫描）也走一次匹配尝试——上轮未达确认数、本轮已达的自愈路径
	}
	// 匹配：对本链的未匹配入账（含历史轮）逐一评估
	// ★ (54)：这里过去是「全链共用一个 LIMIT 200 窗口 + Go 里按链筛」，孤儿一多就把新入账饿死
	//	在窗口外且零日志；现在按链各开窗口（口径见 store.ListUnmatchedDepositsForChain）。
	for _, d := range s.Store.ListUnmatchedDepositsForChain(chain, 200) {
		// ★ (54) 第二条：块高缺失＝确认数**无从计算**，绝不按 0 起算。
		//	旧形态 c = newest - 0 + 1 ≈ 当前链头高度 ⇒ 未达确认阈值那条判据直接被顶穿，
		//	未确认、可回滚的转账会被立刻置 paid，而且全程没有任何告警面。
		//	上游字段换名/漏发是现实存在的形态（㊾ 就是为链头读数专门造的 jsonInt64），
		//	所以"读不到"必须当成**未知**而不是当成"很久以前"。
		if d.BlockNo <= 0 {
			observability.Warn(context.Background(), "USDT 入账缺块高读数，本轮不评确认数（留在未匹配池）",
				"chain", chain, "deposit_id", d.ID)
			continue
		}
		// 实时确认数：newest - block + 1（链头用本轮查询值）
		if d.NewestBlockNo > 0 {
			d.NewestBlockNo = newest
		}
		c := newest - d.BlockNo + 1
		if c < 1 {
			c = 1
		}
		if c < confMin {
			continue // 未达确认阈值：留在未匹配池，下轮再评
		}
		s.usdtTryMatch(d, chain, newest)
	}
	s.Store.MarkUSDTDepositSeenBlock(chain, newest)
	return nil
}

// usdtTryMatch 单笔入账匹配订单并结算（唯一命中才自动入账）。
func (s *Server) usdtTryMatch(d *store.USDTDeposit, chain string, newest int64) {
	order, ambiguous, qerr := s.Store.FindPendingUSDTOrderByDeposit(chain, d.AmountMicro)
	if qerr != nil {
		// ★ (54)：查询失败＝**结论未知**，与"库里没有对应单"是两件事。
		//	旧形态把 err 折成 (nil,false) 走"无单"分支 ⇒ 这笔钱静默留在池里，72h 后才以
		//	「from 打错金额，请人工裁决退款」的误导文案冒头（真因是那次查询没成功）。
		//	现在只出声并保留原状，下一轮自然重评（入账腿幂等，重复评估无副作用）。
		observability.Warn(context.Background(), "USDT 匹配查询失败，本笔保留未匹配状态待下轮重评",
			"chain", chain, "err", qerr.Error())
		return
	}
	if ambiguous {
		_ = s.Store.CreateAlert(0, "critical", "usdt_ambiguous",
			fmt.Sprintf("USDT 入账 %s 金额 %s 命中多笔待结算订单（唯一索引失效？），已停止自动入账，请人工裁决",
				d.TxHash, payment.FormatUSDTMicro(d.AmountMicro)))
		return
	}
	if order == nil {
		// 错金额/无单入账（from 打错金额等）：留孤儿池等待人工处置，绝不部分入账
		return
	}
	// 入账走资金唯一口：MarkOrderPaid 单事务幂等抢占（内含影子失效通知）
	if perr := s.Store.MarkOrderPaid(order.OrderID, order.TenantID); perr != nil {
		log.Printf("[usdt-watch] 自动入账失败 order=#%d: %v", order.OrderID, perr)
		return
	}
	if perr := s.Store.SetPaymentTxHash(order.OrderID, d.TxHash); perr != nil {
		// 资金已入、凭证关联失败（并发/流水缺失）：critical 留痕人工补记
		_ = s.Store.CreateAlert(0, "critical", "usdt_settle",
			fmt.Sprintf("USDT 订单 #%d 已自动入账但 tx_hash 关联失败: %v", order.OrderID, perr))
	}
	_ = s.Store.SettleUSDTOrder(order.OrderID, d.TxHash)
	s.Store.LinkUSDTDeposit(d.ID, order.OrderID)
	_ = s.Store.CreateAlert(0, "info", "usdt_settled",
		fmt.Sprintf("USDT 自动对账入账：订单 #%d（租户 %d）%s USDT（链 %s，确认 %d，tx %s）",
			order.OrderID, order.TenantID, payment.FormatUSDTMicro(d.AmountMicro), chain, newest-d.BlockNo+1, d.TxHash))
	s.Store.LogAudit(order.TenantID, 0, "usdt_auto_settle", "orders", fmt.Sprintf("#%d tx=%s", order.OrderID, d.TxHash))
}
