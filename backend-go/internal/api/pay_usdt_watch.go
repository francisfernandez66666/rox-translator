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
	if err == nil && s.Store != nil && usdtWatch.tookRecovery() {
		for _, a := range s.listOpenWatchAlerts() {
			if aerr := s.Store.ResolveAlert(a.ID); aerr != nil {
				observability.Warn(context.Background(), "USDT 监听告警收敛失败", "id", a.ID, "err", aerr.Error())
			}
		}
		observability.Info(context.Background(), "USDT 到账监听已恢复", "chain", chain)
	}
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
	// 匹配：对全部未匹配入账（含历史轮）逐一评估
	for _, d := range s.Store.ListUnmatchedDeposits(200) {
		if d.Chain != chain {
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
	order, ambiguous := s.Store.FindPendingUSDTOrderByDeposit(chain, d.AmountMicro)
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
