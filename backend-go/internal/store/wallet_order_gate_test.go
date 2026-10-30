// ============ wallet_order_gate_test.go · 职责说明 ============
// 组3 的**次序与写点**静态闸（★ 2026-10-30 三事合一批，W2／W5／W11／W22 的机械防线）。
//
// 为什么必须静态判：这三条纪律的坏法都不报错、不改任何可见读数——
//
//	W2  某处自己写一条 `ORDER BY expires_at`（或 `ORDER BY tier`）⇒ 只有"同一天到期、档位不同"
//	    那一档排序会不一样，测试与现网都看不出来，客户却在那一天被扣错桶；
//	W5  拆扣拆成两条 SELECT（先个人、不足再共享）⇒ "个人档只剩 10、共享 500、扣 100"
//	    那条行为腿可能仍然绿（结果恰好对），但第二条腿读的是旧快照；
//	W11 SettleExhausted 与主链各一套次序 ⇒ AGENTS §一·11 那族"多把尺子"在新面上复发；
//	W22 新增一处直改 `quota_grants."left"` ⇒ 明细/退款/对账一起失真（这是"静默扣错钱"的入口）。
//
// 判据都按**源码现算**，不写死文件清单（新增一处即进射程）；注释先剥再判（否则说明注释会喂饱判据）。
// 行为锁（W3／W4／W6／W12）也在本文件：它们证明"次序真的按桶→到期→档位→id 走"。
// =============================================
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"translator/internal/db"
)

// repoRoot 从当前测试文件所在目录向上找 go.mod（backend-go）。
func gateRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("找不到 go.mod（测试目录结构变了？）")
	return ""
}

// stripGoComments 剥掉 Go 注释（保留字符串字面量里的 // 与 /* */，避免把 SQL 注释当代码删）。
// 只服务于"按字面量锚扫描"，不追求完整词法分析；引号内一律原样保留。
func stripGoComments(src string) string {
	var b strings.Builder
	inLine, inBlock, inStr, inRaw := false, false, false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlock = false
				i++
			}
		case inStr:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				b.WriteByte(src[i])
			} else if c == '"' {
				inStr = false
			}
		case inRaw:
			b.WriteByte(c)
			if c == '`' {
				inRaw = false
			}
		default:
			switch {
			case c == '/' && i+1 < len(src) && src[i+1] == '/':
				inLine = true
				i++
			case c == '/' && i+1 < len(src) && src[i+1] == '*':
				inBlock = true
				i++
			case c == '"':
				inStr = true
				b.WriteByte(c)
			case c == '`':
				inRaw = true
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// walkGoSources 收集 root 下（排除 _test.go）的源码：rel 路径（slash）→ 剥注释后的正文。
func walkGoSources(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = stripGoComments(string(raw))
		return nil
	})
	if err != nil {
		t.Fatalf("遍历源码失败: %v", err)
	}
	if len(out) < 50 {
		t.Fatalf("扫到的源文件只有 %d 个（枚举失明，闸门已失效）", len(out))
	}
	return out
}

// mustReadGateSource 读一份生产源码并剥注释（供行为锁里补一条"源码级第二道防线"用）。
func mustReadGateSource(t *testing.T, rel string) string {
	t.Helper()
	root := gateRepoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("读源码失败 %s: %v", rel, err)
	}
	return string(raw)
}

// W1：次序片段的结构（三桶 → 到期 → 档位号 → id）。结构错位会让"同桶先到期"这条用户令失效。
func TestWalletOrder_W1PredShape(t *testing.T) {
	p := TierOrderPred()
	if !strings.HasPrefix(p, "CASE tier") {
		t.Fatalf("W1：第一截必须是桶号 CASE，实得 %q", p)
	}
	if !strings.Contains(p, ", expires_at ASC, CASE tier") {
		t.Fatalf("W1：第二截必须是 expires_at ASC 且紧跟档位号 CASE，实得 %q", p)
	}
	if !strings.HasSuffix(p, ", id ASC") {
		t.Fatalf("W1：最后一截必须是 id ASC（方言兜底），实得 %q", p)
	}
	// 桶号必须真按 ㋐ 排列：免费赠送 < 收费有时效 < 收费无时效
	if !(TierBucketOf(TierRegister) < TierBucketOf(TierPlanMonthly) &&
		TierBucketOf(TierPlanMonthly) < TierBucketOf(TierPlanPermanent)) {
		t.Fatal("W1：三桶次序不对（免费赠送 > 收费有时效 > 收费无时效）")
	}
	// 未知档位必须落最后一桶，且名次在已知档位之后
	if TierBucketOf("no_such_tier") != TierBucketPaidPerm {
		t.Fatal("W1：未知档位必须落兜底桶")
	}
	if TierRank("no_such_tier") <= TierRank(TierLegacy) {
		t.Fatal("W1：未知档位名次必须在已知档位之后（不许凭空拿优先权）")
	}
}

// W2：全仓（store 包）**除 wallet_tier.go 外**不许自己写带 expires_at 的 ORDER BY。
// 另：两处调用点必须真的引用 TierOrderPred（摘掉任一处即红）。
func TestWalletOrder_W2SingleOrderingSource(t *testing.T) {
	root := gateRepoRoot(t)
	src := walkGoSources(t, filepath.Join(root, "internal"))
	for rel, body := range src {
		if rel == "store/wallet_tier.go" {
			continue
		}
		if strings.Contains(body, "ORDER BY expires_at") || strings.Contains(body, "ORDER BY tier") {
			t.Fatalf("W2：%s 自己写了一条次序（次序只能来自 store.TierOrderPred()）", rel)
		}
	}
	// 两处调用点：拆扣主链与欠费结算（W11 的"两条次序合一"）
	for _, rel := range []string{"store/wallet_debit.go", "store/billing.go"} {
		if !strings.Contains(src[rel], "TierOrderPred()") {
			t.Fatalf("W2/W11：%s 必须引用 TierOrderPred()（次序唯一写点）", rel)
		}
	}
}

// W5：拆扣只有一条 SELECT（个人档 ∪ 共享档同一次取回）。
func TestWalletOrder_W5SingleSelect(t *testing.T) {
	root := gateRepoRoot(t)
	src := walkGoSources(t, filepath.Join(root, "internal"))
	body := src["store/wallet_debit.go"]
	if body == "" {
		t.Fatal("W5：读不到 wallet_debit.go（闸门失明）")
	}
	if n := strings.Count(body, `"left">0 AND expires_at>?`); n != 1 {
		t.Fatalf("W5：归属查询只许有一条（含 left>0 且未过期那一串），实得 %d——拆成两条＝两条腿各判一次归属", n)
	}
	if n := strings.Count(body, "(user_id=? OR user_id=0)"); n != 1 {
		t.Fatalf("W5/W6：个人档 ∪ 共享档的条件必须恰好一次，实得 %d", n)
	}
}

// W22：`quota_grants."left"` 的写点必须登记（新增一处即红）。
// 白名单是"语义分封"：每一档都有正当理由，且都写在 allowlist 里；**不是**"暂时放行"。
func TestWalletWritePoint_W22Allowlist(t *testing.T) {
	root := gateRepoRoot(t)
	src := walkGoSources(t, filepath.Join(root, "internal"))
	// 允许出现"直改 left"的文件（按职责，不是按历史）：
	allow := map[string]string{
		// 拆扣唯一实现（本批引入）
		"store/wallet_debit.go": "拆扣主链（不在这里扣＝没有别处该扣）",
		// 按档回补（本批引入）
		"store/billing_refund_wallet.go": "按档退款回补",
		// 欠费结算：按 TierOrderPred 选行后逐行扣（清账语义，不是"又一次拆扣"）
		"store/billing.go": "SettleExhausted 清账 ＋ RefundOrder 作废本单剩余份额（两处都在既有函数体内）",
		// 退款时回收升级子单的结转份额（F-36 闭环）
		"store/billing_refund.go": "回收已退款升级子单的 order_carry 份额",
		// 超管手动把任务积分拉回 total（#33）
		"store/task_rewards.go": "ResetTaskGrantConsumption 重置（不改到期、不跨档）",
	}
	found := 0
	for rel, body := range src {
		if !strings.Contains(body, `quota_grants SET "left"`) {
			continue
		}
		found++
		reason, ok := allow[rel]
		if !ok {
			t.Fatalf("W22：%s 新增了直改 quota_grants.\"left\" 的写点；要么改走 deductWalletTx/RefundUsageByLedger，"+
				"要么在本闸的 allow 表里登记职责理由（不许静默放行）", rel)
		}
		if strings.TrimSpace(reason) == "" {
			t.Fatalf("W22：allow 表里 %s 的理由为空（空理由＝没登记）", rel)
		}
	}
	if found < 4 {
		t.Fatalf("W22：只扫到 %d 处写点（少于已知的白名单条目数＝枚举失明，闸门已失效）", found)
	}
}

// W3／W4：真机次序——桶号压过到期日；同桶内先到期；到期相同按 id。
func TestWalletOrder_W3W4Behavior(t *testing.T) {
	st := newTestStore(t)
	tid, uid := int64(401), int64(9401)
	now := time.Now().UTC()
	// 关键设计：月包 +1 天（比注册赠送 +10 天更早到期），但它在桶 2，必须先吃桶 1
	gTask := mustInsertGrant(t, st, tid, uid, TierTask, 100, now.Add(5*24*time.Hour))
	gReg := mustInsertGrant(t, st, tid, uid, TierRegister, 100, now.Add(10*24*time.Hour))
	gMonth := mustInsertGrant(t, st, tid, 0, TierPlanMonthly, 100, now.Add(24*time.Hour))
	// 同档同到期：两条 task（id 小的先吃）
	gTaskOld := mustInsertGrant(t, st, tid, uid, TierTask, 10, now.Add(5*24*time.Hour))

	ledgerID := mustDebitLedger(t, st, tid, uid, 150)
	// 桶1 内部按到期：两条 task 同到期 ⇒ 按 id 升序（先插入的那条先吃），共 110；
	// 再 register(10d) 吃 40 —— 月包(1d) 明明更早到期，但因为它在桶 2，必须排最后。
	if l := grantLeftOf(t, st, gTaskOld); l != 0 {
		t.Fatalf("W4：同档同到期必须按 id 依次吃满，实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gTask); l != 0 {
		t.Fatalf("W4：task 档应被吃满，实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gReg); l != 60 {
		t.Fatalf("W4：register 档应剩 60（150-110=40 被它吃掉），实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gMonth); l != 100 {
		t.Fatalf("W3：月包只早到期 1 天，但它在桶 2，必须排在桶 1 之后，实得 left=%d", l)
	}
	// 明细行数与合计（W3）
	var lines, sum int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		"SELECT COUNT(*), COALESCE(SUM(tokens),0) FROM usage_debit_lines WHERE ledger_id=?", ledgerID).Scan(&lines, &sum); err != nil {
		t.Fatalf("读明细失败: %v", err)
	}
	if lines != 3 || sum != 150 {
		t.Fatalf("W3：明细应为 3 行合计 150，实得 %d 行 %d", lines, sum)
	}
}

// W6：跨用户不许扣他人个人档；共享档任何成员可扣。
func TestWalletOrder_W6UserIsolation(t *testing.T) {
	st := newTestStore(t)
	tid := int64(402)
	now := time.Now().UTC()
	gA := mustInsertGrant(t, st, tid, 9402, TierRegister, 100, now.Add(24*time.Hour)) // A 的个人档
	gShared := mustInsertGrant(t, st, tid, 0, TierTask, 100, now.Add(24*time.Hour))   // 共享档
	if err := st.DeductWithGrants(tid, 9403, 100); err != nil {                       // B 来扣
		t.Fatalf("拆扣失败: %v", err)
	}
	if l := grantLeftOf(t, st, gA); l != 100 {
		t.Fatalf("W6：B 不许扣 A 的注册赠送，实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gShared); l != 0 {
		t.Fatalf("W6：共享档任何成员可扣，实得 left=%d", l)
	}
}

// W12：第一档不够但后面够 ⇒ 正常拆扣，不许报欠费。
func TestWalletOrder_W12SplitThenInsufficient(t *testing.T) {
	st := newTestStore(t)
	tid := int64(403)
	now := time.Now().UTC()
	mustInsertGrant(t, st, tid, 0, TierTask, 10, now.Add(2*time.Hour))
	mustInsertGrant(t, st, tid, 0, TierPlanMonthly, 500, now.Add(30*24*time.Hour))
	if err := st.DeductWithGrants(tid, 0, 100); err != nil {
		t.Fatalf("W12：第一档只有 10 但后面够，必须正常拆扣（实得错误 %v）", err)
	}
	// 全都扣光之后才报欠费
	if err := st.DeductWithGrants(tid, 0, 1000); err == nil {
		t.Fatal("W12：合计不足时必须报欠费")
	}
}
