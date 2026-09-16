// ============ 本文件职责中文说明 ============
// USDT 收款数据层单元测试：尾数唯一分配、收款要素快照读写、链上入账幂等落库、
// 精确金额匹配（含过期/错金额不匹配）、payments.tx_hash 一笔交易防复用到两单、
// 配置解析（链过滤/确认数默认/汇率）。
// =============================================
package store

import (
	"errors"
	"testing"
	"time"
)

func TestUSDTMetaTailUnique(t *testing.T) {
	s := newTestStore(t)
	base := int64(1_245_833)
	var amounts []int64
	for i := 0; i < 6; i++ {
		o, err := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
		if err != nil {
			t.Fatalf("建单失败: %v", err)
		}
		m, err := s.CreateUSDTOrderMeta(o.ID, 1, "trc20", "Taddr", base, 720, true)
		if err != nil {
			t.Fatalf("挂收款要素失败: %v", err)
		}
		if m.AmountMicro <= base || m.AmountMicro > base+9999 {
			t.Fatalf("尾数越界: %d", m.AmountMicro)
		}
		amounts = append(amounts, m.AmountMicro)
	}
	// 金额两两不等（尾数唯一分配）
	seen := map[int64]bool{}
	for _, a := range amounts {
		if seen[a] {
			t.Fatalf("尾数冲突未消除: %d", a)
		}
		seen[a] = true
	}
	// 回读与声明哈希
	m, err := s.GetUSDTOrderMeta(int64(3))
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if err := s.SetUSDTDeclaredTxHash(m.OrderID, "abc"); err != nil {
		t.Fatalf("声明哈希失败: %v", err)
	}
	m2, _ := s.GetUSDTOrderMeta(m.OrderID)
	if m2.ClientTxHash != "abc" || m2.DeclaredAt == "" {
		t.Fatalf("声明未落库: %+v", m2)
	}
}

func TestUSDTDepositIdempotentAndMatch(t *testing.T) {
	s := newTestStore(t)
	o, _ := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	m, err := s.CreateUSDTOrderMeta(o.ID, 1, "trc20", "Taddr", 1_245_833, 720, true)
	if err != nil {
		t.Fatal(err)
	}
	dep := &USDTDeposit{Chain: "trc20", TxHash: "deadbeef", LogIndex: 0, FromAddr: "Tfrom",
		AmountMicro: m.AmountMicro, BlockNo: 100, NewestBlockNo: 120, SeenAt: time.Now().UTC().Format(time.RFC3339)}
	fresh, err := s.InsertUSDTDeposit(dep)
	if err != nil || !fresh {
		t.Fatalf("首插应成功: %v fresh=%v", err, fresh)
	}
	fresh2, err := s.InsertUSDTDeposit(dep)
	if err != nil {
		t.Fatal(err)
	}
	if fresh2 {
		t.Fatal("重复入账应幂等跳过")
	}
	// 错金额不匹配
	if got, amb := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro+1); got != nil || amb {
		t.Fatal("错金额不应命中订单")
	}
	// 精确金额命中
	got, amb := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro)
	if amb || got == nil || got.OrderID != o.ID {
		t.Fatalf("精确金额应唯一命中: %+v amb=%v", got, amb)
	}
	// 订单转 paid 后不再匹配（防已结算单被二次匹配）
	if err := s.MarkOrderPaid(o.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro); got != nil {
		t.Fatal("已支付订单不应再被匹配")
	}
}

func TestSetPaymentTxHashUnique(t *testing.T) {
	s := newTestStore(t)
	o1, _ := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	o2, _ := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	if err := s.MarkOrderPaid(o1.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkOrderPaid(o2.ID, 1); err != nil {
		t.Fatal(err)
	}
	h := "a1b2c3d4"
	if err := s.SetPaymentTxHash(o1.ID, h); err != nil {
		t.Fatalf("首单关联失败: %v", err)
	}
	// 同一笔 tx 复用到第二单必须被拒（幂等防线）
	if err := s.SetPaymentTxHash(o2.ID, h); !errors.Is(err, ErrUSTXHashUsed) {
		t.Fatalf("重复 tx 应返回 ErrUSTXHashUsed，实得 %v", err)
	}
	// 同单重复关联幂等拒绝（tx_hash<>'' 条件）
	if err := s.SetPaymentTxHash(o1.ID, h); err == nil {
		t.Fatal("同单重复关联应报错")
	}
}

func TestGetUSDTCfg(t *testing.T) {
	s := newTestStore(t)
	if cfg := s.GetUSDTCfg(); cfg.Enabled || cfg.AutoSettle {
		t.Fatal("默认应为关闭（安全基线）")
	}
	_ = s.SetConfig("usdt_enabled", "1")
	_ = s.SetConfig("usdt_rate_fen_per_usdt", "720")
	_ = s.SetConfig("usdt_chains", "trc20,erc20")
	_ = s.SetConfig("usdt_addr_trc20", "TaddrSample")
	_ = s.SetConfig("usdt_addr_erc20", "0xabc") // 有地址即入链（格式校验在 API 写入点）
	cfg := s.GetUSDTCfg()
	if !cfg.Enabled || !cfg.TailEnabled {
		t.Fatalf("开关解析错误: %+v", cfg)
	}
	if len(cfg.Chains) != 2 || cfg.Chains[0] != "trc20" {
		t.Fatalf("链列表错误: %+v", cfg.Chains)
	}
	if cfg.RateFen != 720 || cfg.Confirm["trc20"] != 19 || cfg.Confirm["erc20"] != 12 {
		t.Fatalf("汇率/确认数错误: %+v", cfg)
	}
	_ = s.SetConfig("usdt_confirmations_trc20", "28")
	if s.GetUSDTCfg().Confirm["trc20"] != 28 {
		t.Fatal("确认数自定义未生效")
	}
	// 空地址链剔除
	_ = s.SetConfig("usdt_chains", "trc20,bep20") // bep20 无地址
	cfg = s.GetUSDTCfg()
	for _, c := range cfg.Chains {
		if c == "bep20" {
			t.Fatal("无地址链应被剔除")
		}
	}
}

func TestUSDTExpiredNotMatched(t *testing.T) {
	s := newTestStore(t)
	o, _ := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	m, err := s.CreateUSDTOrderMeta(o.ID, 1, "trc20", "Taddr", 1_245_833, 720, true)
	if err != nil {
		t.Fatal(err)
	}
	// 人工把到期时间拨回过去，模拟超窗
	_, _ = s.DB().Exec("UPDATE usdt_orders SET expires_at=? WHERE order_id=?",
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), m.OrderID)
	if got, _ := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro); got != nil {
		t.Fatal("过期单不应匹配")
	}
	ids := s.ExpiredUSDTMetaOrderIDs()
	found := false
	for _, id := range ids {
		if id == m.OrderID {
			found = true
		}
	}
	if !found {
		t.Fatal("到期清单应含本单")
	}
}
