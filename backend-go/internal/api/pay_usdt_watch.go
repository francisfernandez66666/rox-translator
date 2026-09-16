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
	"time"

	"translator/internal/payment"
	"translator/internal/store"
)

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
	cfg := s.Store.GetUSDTCfg()
	if !cfg.Enabled || !cfg.AutoSettle {
		return // 自动对账默认关闭：真链冒烟通过前不产生任何自动入账
	}
	for _, chain := range cfg.Chains {
		if err := s.usdtScanChain(chain, cfg); err != nil {
			log.Printf("[usdt-watch] %s 扫描失败: %v", chain, err)
		}
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
