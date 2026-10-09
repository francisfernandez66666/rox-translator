// ============ 本文件职责中文说明 ============
// USDT 收款数据层单元测试：尾数唯一分配、收款要素快照读写、链上入账幂等落库、
// 精确金额匹配（含过期/错金额不匹配）、payments.tx_hash 一笔交易防复用到两单、
// 配置解析（链过滤/确认数默认/汇率）。
// ★ (54) 两条：匹配查询失败不许折成「没有单」；未匹配入账窗口必须按链各开一个。
// =============================================
package store

import (
	"errors"
	"fmt"
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
	// 错金额不匹配（★ (54) 三值返回值：查询失败＝err 非空，判据要把它和"没命中"分开数）
	if got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro+1); qerr != nil || got != nil || amb {
		t.Fatalf("错金额不应命中订单（且查询必须成功）: got=%+v amb=%v err=%v", got, amb, qerr)
	}
	// 精确金额命中
	got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro)
	if qerr != nil || amb || got == nil || got.OrderID != o.ID {
		t.Fatalf("精确金额应唯一命中: %+v amb=%v err=%v", got, amb, qerr)
	}
	// 订单转 paid 后不再匹配（防已结算单被二次匹配）
	if err := s.MarkOrderPaid(o.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got, _, qerr := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro); qerr != nil || got != nil {
		t.Fatalf("已支付订单不应再被匹配（查询本身必须成功）: got=%+v err=%v", got, qerr)
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
	if got, _, qerr := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro); qerr != nil || got != nil {
		t.Fatalf("过期单不应匹配（查询本身必须成功）: got=%+v err=%v", got, qerr)
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

// TestFindPendingUSDTOrderKeepsQueryErrorDistinct ★ (54) 第一条：
// 「这一次没查出来」与「库里确实没有对应单」必须是两个不同的返回值。
// 旧形态把 err 折成 (nil,false)，调用方（api/pay_usdt_watch.go 的 usdtTryMatch）据此走「无单」分支，
// 于是查询故障期间的钱一路留在未匹配池，72 小时后以「from 打错金额，请人工裁决退款」的**误导文案**冒头
// ——库里其实有那一单，只是那一次没查出来。
// 反证：把 store 里那条 `return nil, false, err` 改回 `return nil, false, nil` ⇒ 第二段当场红
// （err 为 nil 就等于声明"查询成功了且没命中"，与第一段对照腿同形，判据失去区分力）。
func TestFindPendingUSDTOrderKeepsQueryErrorDistinct(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStore(t)
	o, err := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	m, err := s.CreateUSDTOrderMeta(o.ID, 1, "trc20", "Taddr", 1_245_833, 720, true)
	if err != nil {
		t.Fatalf("挂收款要素失败: %v", err)
	}
	// ① 对照腿：表在、查询成功、真的没命中 ⇒ (nil, false, nil)
	if got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro+1); qerr != nil || got != nil || amb {
		t.Fatalf("错金额应为「查询成功的无命中」: got=%+v amb=%v err=%v", got, amb, qerr)
	}
	// ② 把表打掉 ⇒ 查询在解析期就失败：err 必须原样交出去，且不许伪装成 ambiguous
	if _, err := s.DB().Exec("DROP TABLE usdt_orders"); err != nil {
		t.Fatalf("打掉 usdt_orders 失败: %v", err)
	}
	got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", m.AmountMicro)
	if qerr == nil {
		t.Fatal("查询本身失败必须回 err——把它折成「没有单」就是 (54) 的缺陷本体")
	}
	if got != nil || amb {
		t.Fatalf("查询失败时只该有 err 这一个信号: got=%+v amb=%v", got, amb)
	}
}

// TestFindPendingUSDTOrderScanFailureJudgedAmbiguous ★ (54) 第一条的第二半：
// **「读不全」不许伪装成「读得清」**。旧形态把 rows.Scan 失败写成 `continue`（丢一行继续数），
// 两种后果都是钱的方向：
//  1. 库里只有一行且它解析失败 ⇒ 退化成 (nil,false)＝"没有对应单"，那笔钱被留在未匹配池里
//     静等 72 小时，最后以「from 打错金额，请人工裁决退款」的**误导文案**冒头；
//  2. 库里两行、坏的那行被丢掉 ⇒ 双命中被数成单命中，`len(found)==1` 直接把钱**自动入到
//     另一张单上**，而 usdt_ambiguous 告警反而不响（该响的不响，是最坏的一种静默）。
//
// 触发手段：SQLite 的 INTEGER 列**允许存 TEXT**（亲和性只对"能完整转成数字的串"生效），
// 所以把 rate_fen 写成 '12abc' 就能让 Scan 在真实驱动里失败——比 mock 驱动诚实（AGENTS §一·4：
// 判"某句 SQL/某条读腿行为对不对"必须靠真驱动跑一次，不靠语法直觉）。
// ⚠️ 这一档触发方式是 **SQLite 特有**的（PG 会在 UPDATE 当场拒掉），所以它测的是"Scan 失败之后
// 代码怎么走"，不是"库里能存脏值"；rows.Err() 那一腿（迭代中断）在单测里造不出来，只能靠
// 上面的 err 档与代码同形性保证——这一格**没被自动化覆盖**，写在这里是为了别把它记成已闭环。
//
// 反证：把 store 里那两处 `return nil, true, nil` 改回 `continue` ⇒ ②③ 两段当场红
// （②拿到的会是那张好单、③退化成"无单"）。
func TestFindPendingUSDTOrderScanFailureJudgedAmbiguous(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStore(t)

	// 两张单：同链同应得金额（B 的 amount 用 UPDATE 对齐 A），to_addr 不同 ⇒ 唯一索引不拦，
	// 而匹配查询只看 chain＋amount ⇒ 天然双命中。
	oA, err := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	if err != nil {
		t.Fatalf("建单 A 失败: %v", err)
	}
	mA, err := s.CreateUSDTOrderMeta(oA.ID, 1, "trc20", "TaddrA", 1_245_833, 720, true)
	if err != nil {
		t.Fatalf("挂收款要素 A 失败: %v", err)
	}
	oB, err := s.CreateOrderChannel(1, 30000, 8.97, 0, "usdt", "")
	if err != nil {
		t.Fatalf("建单 B 失败: %v", err)
	}
	mB, err := s.CreateUSDTOrderMeta(oB.ID, 1, "trc20", "TaddrB", 9_999_999, 720, true)
	if err != nil {
		t.Fatalf("挂收款要素 B 失败: %v", err)
	}
	if _, err := s.DB().Exec("UPDATE usdt_orders SET amount_micro=? WHERE order_id=?", mA.AmountMicro, mB.OrderID); err != nil {
		t.Fatalf("对齐 B 的应得金额失败: %v", err)
	}

	// ① 对照腿：两行都读得动 ⇒ 双命中**本来就**判 ambiguous（后面那段才谈得上"被丢行骗成单命中"）。
	if got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", mA.AmountMicro); qerr != nil || !amb || got != nil {
		t.Fatalf("①前置双命中应判歧义: got=%+v amb=%v err=%v", got, amb, qerr)
	}

	// ② 把 B 那行弄成读不动 ⇒ **绝不许**退化成"唯一命中 A"并把钱自动入到 A 上。
	if _, err := s.DB().Exec("UPDATE usdt_orders SET rate_fen='12abc' WHERE order_id=?", mB.OrderID); err != nil {
		t.Fatalf("写入脏 rate_fen 失败: %v", err)
	}
	if got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", mA.AmountMicro); qerr != nil || !amb || got != nil {
		t.Fatalf("②一行读不全时不许出结论（got=%+v amb=%v err=%v）⇒ 丢行会把双命中伪装成单命中，钱会入到别的单上", got, amb, qerr)
	}

	// ③ 只剩那一行坏数据 ⇒ 不许伪装成"库里没有单"（(nil,false,nil) 与①对照腿同形，正是缺陷本体）。
	if _, err := s.DB().Exec("DELETE FROM usdt_orders WHERE order_id=?", mB.OrderID); err != nil {
		t.Fatalf("清掉 B 失败: %v", err)
	}
	if _, err := s.DB().Exec("UPDATE usdt_orders SET rate_fen='12abc' WHERE order_id=?", mA.OrderID); err != nil {
		t.Fatalf("写入脏 rate_fen 失败: %v", err)
	}
	if got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", mA.AmountMicro); qerr != nil || !amb || got != nil {
		t.Fatalf("③唯一一行读不全时应判歧义而不是「无单」（got=%+v amb=%v err=%v）⇒ 旧形态在这里把已知故障写成未知，72h 后拿误导文案冒头", got, amb, qerr)
	}

	// ④ 正向对照：把脏值改回数字 ⇒ 同一把判据必须翻回"单命中"，否则上面三段是在扫空气。
	if _, err := s.DB().Exec("UPDATE usdt_orders SET rate_fen=720 WHERE order_id=?", mA.OrderID); err != nil {
		t.Fatalf("复原 rate_fen 失败: %v", err)
	}
	got, amb, qerr := s.FindPendingUSDTOrderByDeposit("trc20", mA.AmountMicro)
	if qerr != nil || amb || got == nil || got.OrderID != mA.OrderID {
		t.Fatalf("④数据干净时应回到唯一命中: got=%+v amb=%v err=%v", got, amb, qerr)
	}
}

// usdtDepositForTest 测试用入账行构造（三处用例同形，避免手写字段漏一个把幂等键写成空串）。
func usdtDepositForTest(chain, txHash string, amountMicro int64) *USDTDeposit {
	return &USDTDeposit{Chain: chain, TxHash: txHash, LogIndex: 0,
		FromAddr: "0xVisitor", AmountMicro: amountMicro, BlockNo: 100, NewestBlockNo: 200}
}

// TestListUnmatchedDepositsForChainNotStarvedByOtherChain ★ (54) 第二条：
// 未匹配池的窗口必须**按链各开一个**。旧形态是全链共用一个 `ORDER BY id ASC LIMIT 200`、
// 再由调用方在 Go 里按链筛——一条链的孤儿一旦积压过 200 行，最旧那 200 行被反复评估，
// **新到的钱永远进不了匹配**，而且零错误零日志
// （表现是「客户转了账但一直不入账」，排障会从链上端点开始找，根因却在池子窗口）。
// 反证：把 api 侧那行改回 `ListUnmatchedDeposits(200)`＋按链筛 ⇒ 第一段红；
// 第二段对照腿证明的正是「旧窗口里根本没有这笔」，所以它同时兜住"缺陷不存在"这种空转锁。
func TestListUnmatchedDepositsForChainNotStarvedByOtherChain(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStore(t)
	// 205 条 erc20 孤儿（id 靠前，把全链窗口占满）
	for i := 0; i < 205; i++ {
		if _, err := s.InsertUSDTDeposit(usdtDepositForTest("erc20", fmt.Sprintf("0xstarved-%03d", i), 1_300_000)); err != nil {
			t.Fatalf("灌 erc20 孤儿 #%d: %v", i, err)
		}
	}
	// 新到的一笔 trc20（id 最靠后）——它才是「客户的钱」
	if _, err := s.InsertUSDTDeposit(usdtDepositForTest("trc20", "0xfresh-payment", 1_245_999)); err != nil {
		t.Fatalf("灌 trc20 新入账: %v", err)
	}
	rows := s.ListUnmatchedDepositsForChain("trc20", 200)
	if len(rows) != 1 || rows[0].TxHash != "0xfresh-payment" {
		t.Fatalf("按链窗口必须取到 trc20 那笔新入账，实得 %d 行", len(rows))
	}
	// 对照腿：旧的那把全链窗口的确把它挤掉了（没有这一腿，上面就是在测一个不存在的缺陷）
	var gotFresh bool
	for _, d := range s.ListUnmatchedDeposits(200) {
		if d.Chain == "trc20" {
			gotFresh = true
		}
	}
	if gotFresh {
		t.Fatal("对照腿失效：全链 200 窗口本该被 erc20 的 205 行占满，trc20 这笔不该在里面")
	}
	// erc20 自己那条链照样拿得到行（按链窗口不是「只服务新链」，上限内取满）
	if e := s.ListUnmatchedDepositsForChain("erc20", 200); len(e) != 200 {
		t.Fatalf("erc20 按链窗口应回上限 200 行，实得 %d", len(e))
	}
}
