// ============ instant_free_charge_test.go · 职责说明 ============
// G1／G4／G7 的**咽喉级**锁（★ 2026-10-31 断言缺口补齐第二轮）。
//
// 为什么这三条要打在 `ChargeUsageRealtime`／`instantDecide` 上，而不是再写一份纯函数判据：
//
//	G1（免费档：不扣钱、留痕一行、两个暗额度桶各自 +1、撞墙脚印不加）——
//	   `instant_free_points_test.go` 的 W24 只锁了**留痕行的形状**（直调 `LogUsage`），
//	   K3 只锁了 `free_wall` 的不对称；**没有任何一条判据真跑过免费分支**，
//	   所以"积分累加写在哪儿"这件事当时是无人看的：把 `RateDayAdd` 挪出免费分支、
//	   或者把 uid 与 tid 两个桶加成同一个键，全仓一片绿。
//	G4（IP 次数**按客户端 IP 分桶**，换 IP 恢复）——只做账号维度判据的实现在这里必红。
//	G7（免费档不许挤掉收费面）——同一租户同一人，未标记腿必须照旧进扣费通道、
//	   且**不吃**免费暗额度；这条与 N2（免费标记唯一写点）合起来才是"工单/OpenAPI 仍扣积分"。
//
// ⚠️ 三条本文件**不**断言的（写在这里免得下位误以为漏了）：
//
//	① 「余额严格减少」——扣减走 `billing.DefaultSink` 异步批量落库，内存单测里全局 sink 未 InitGlobalSink，
//	   Record 被安全丢弃（同 `platform_cost_charge_test.go` 文件头那条 ⚠️）；这一侧由 `run_uat` 的 PG 矩阵承担。
//	   本文件的等值判据是**收集器口径**：`points_used` 出参与实扣同源（F-49①），免费腿必须为 0。
//	② `usage_daily` 不含免费用量——store 级锁在 `wallet_preds_test.go`。
//	③ QPS／并发闸的行为腿——见 `chat_audio_qps_gate_test.go`（G14）。
//
// 方言：复用 `newPlatformCostProbe`（自钉 SQLite 内存库＋显式钉死 config.C，AGENTS §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestInstantFree_(G1|G4|G7)'
// =============================================
package api

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"translator/internal/llm"
	"translator/internal/ops"
	"translator/internal/store"
)

// ifcPrompt/ifcCompletion 一次用量的 token 数（25k＋10k）。
// ⚠️ 为什么不用 60＋40 那种小量：积分汇率是 24917／33222 token 一档，几十上百个 token
// 折成积分就是 **0**，于是「两个暗额度桶各 +1」这条判据在字节上恒等于"桶没动"——
// 那不是被测行为，是判据自己空转（AGENTS §一·14 P0 那一族「射程腿」的同款问题）。
// 真实现里小量折 0 分**是设计**（免费档按天累计，单枪零点几分不抬桶），本锁要判的是
// 「够档的那一枪会不会把两桶各推一次同一个折算值」，所以靶子必须给到够档的量。
const (
	ifcPrompt     = int64(25000)
	ifcCompletion = int64(10000)
)

// ifcBucket 读一个 UTC 日计数桶的已用量（读不到即 fatal：这条判据的落点就是它，不许 fail-open 蒙绿）。
func ifcBucket(t *testing.T, st *store.Store, scope, key string) int64 {
	t.Helper()
	st2, err := st.RateDayLoad(scope, key)
	if err != nil {
		t.Fatalf("读计数器 %s/%s 失败（判据失去落点）: %v", scope, key, err)
	}
	return st2.Used
}

// ifcFreeRow 读回免费留痕行（task_type=instant_free）。
type ifcFreeRow struct {
	tid, uid, quantity int64
	chargeKind         string
}

func ifcFreeRows(t *testing.T, p *platformCostProbe) []ifcFreeRow {
	t.Helper()
	rws, err := p.sqlDB.Query(`SELECT tenant_id, user_id, quantity, charge_kind FROM usage_ledger WHERE task_type='instant_free' ORDER BY id`)
	if err != nil {
		t.Fatalf("读免费留痕行失败: %v", err)
	}
	defer rws.Close()
	var out []ifcFreeRow
	for rws.Next() {
		var r ifcFreeRow
		if err := rws.Scan(&r.tid, &r.uid, &r.quantity, &r.chargeKind); err != nil {
			t.Fatalf("扫描免费留痕行失败: %v", err)
		}
		out = append(out, r)
	}
	if err := rws.Err(); err != nil {
		t.Fatalf("遍历免费留痕行失败: %v", err)
	}
	return out
}

// TestInstantFree_G1FreeLeg 免费腿的四件事：收集器 0／留痕一行／两个暗额度桶各 +1 同一个值／脚印不加。
func TestInstantFree_G1FreeLeg(t *testing.T) {
	p := newPlatformCostProbe(t)
	uidKey, tidKey := strconv.FormatInt(p.memberID, 10), strconv.FormatInt(p.tid, 10)

	// 前置：两个桶都必须是 0，否则"各 +1"这条判据在旧值上根本算不出增量。
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointUser, uidKey); got != 0 {
		t.Fatalf("前置失败：账号暗额度桶应为 0，实得 %d", got)
	}
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointTenant, tidKey); got != 0 {
		t.Fatalf("前置失败：租户暗额度桶应为 0，实得 %d", got)
	}

	uc := &llm.UsageCollector{}
	ctx := llm.WithUsageCollector(llm.WithFreeInstant(pcBaseCtx(p, p.memberID)), uc)
	if err := p.s.ChargeUsageRealtime(ctx, "bigmodel/glm-4", ifcPrompt, ifcCompletion); err != nil {
		t.Fatalf("免费腿不该报错: %v", err)
	}

	// ① 出参口径：收集器恒 0（免费还报"消耗 N 积分"就是 F-49① 那一族「读数与事实相反」）
	if got := uc.BilledTotal(); got != 0 {
		t.Fatalf("G1：免费腿写了对外收集器 %d（客户会看到 points_used>0 而钱没掉）", got)
	}
	// ② 留痕行：恰好一行，归因到人、charge_kind='log'、quantity>0
	rows := ifcFreeRows(t, p)
	if len(rows) != 1 {
		t.Fatalf("G1：免费腿应恰好留痕一行，实得 %d 行", len(rows))
	}
	r := rows[0]
	if r.chargeKind != "log" {
		t.Fatalf("G1：免费留痕行 charge_kind 必须是 log，实得 %q", r.chargeKind)
	}
	if r.tid != p.tid || r.uid != p.memberID {
		t.Fatalf("G1：留痕行归因错（tid=%d uid=%d，期望 %d/%d）", r.tid, r.uid, p.tid, p.memberID)
	}
	if r.quantity <= 0 {
		t.Fatalf("G1：留痕行 quantity=%d ⇒ 这条链压根没量，后面三条等值判据全是空转", r.quantity)
	}
	// ③ 两个暗额度桶各自 +1，且**值等于同一份折算**（积分口径只有一个：PointsFromTokens(billed)）
	want := p.s.Store.PointsFromTokens(r.quantity)
	if want <= 0 {
		t.Fatalf("G1：PointsFromTokens(%d)=%d，折算成 0 ⇒ 桶增量判据无从判定（汇率/系数档位配错了）", r.quantity, want)
	}
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointUser, uidKey); got != want {
		t.Fatalf("G1：账号暗额度桶累加实得 %d，应为 %d（真实 billed 的折算值）", got, want)
	}
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointTenant, tidKey); got != want {
		t.Fatalf("G1：租户暗额度桶累加实得 %d，应为 %d（两桶各算各的，少写一个就永远撞不到上限）", got, want)
	}
	// ④ 撞墙脚印：成功路径一次都不加（K3 在 decide 层判过，这里是**咽喉层**的同一条不对称）
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreeWall, uidKey); got != 0 {
		t.Fatalf("G1：免费成功那一枪不该写 free_wall，实得 %d（画像与引导卡会因此把「用完了」念给正常用户）", got)
	}
}

// TestInstantFree_G7PaidLegStillCharges G7：同一租户同一人，未标记腿照旧扣、且不吃免费暗额度。
func TestInstantFree_G7PaidLegStillCharges(t *testing.T) {
	p := newPlatformCostProbe(t)
	uidKey, tidKey := strconv.FormatInt(p.memberID, 10), strconv.FormatInt(p.tid, 10)

	// 未标记腿＝文件工单／OpenAPI／普通翻译走的同一咽喉（N2 已锁"免费标记只有四个即时入口会写"）
	uc := &llm.UsageCollector{}
	if err := p.s.ChargeUsageRealtime(llm.WithUsageCollector(pcBaseCtx(p, p.memberID), uc), "bigmodel/glm-4", ifcPrompt, ifcCompletion); err != nil {
		t.Fatalf("未标记腿不该报错: %v", err)
	}
	billed := uc.BilledTotal()
	if billed <= 0 {
		t.Fatalf("G7：未标记腿没进扣费通道（收集器=%d）⇒ 免费档把收费面一起免掉了，这是本条要抓的形态", billed)
	}
	// 收费腿**不许**动两个暗额度桶（动了＝同一次真实消耗记两笔账，免费档会被自己提前烧光）
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointUser, uidKey); got != 0 {
		t.Fatalf("G7：收费腿写入了账号暗额度桶（实得 %d）", got)
	}
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointTenant, tidKey); got != 0 {
		t.Fatalf("G7：收费腿写入了租户暗额度桶（实得 %d）", got)
	}
	// 同租户同一个人再来一条免费腿：两条通道互不影响（收费腿已扣、免费腿仍只留痕＋累加暗额度）
	ucFree := &llm.UsageCollector{}
	ctxFree := llm.WithUsageCollector(llm.WithFreeInstant(pcBaseCtx(p, p.memberID)), ucFree)
	if err := p.s.ChargeUsageRealtime(ctxFree, "bigmodel/glm-4", ifcPrompt, ifcCompletion); err != nil {
		t.Fatalf("免费腿不该报错: %v", err)
	}
	if ucFree.BilledTotal() != 0 {
		t.Fatalf("G7：同租户跑过收费腿之后，免费腿开始扣钱了（实得 %d）", ucFree.BilledTotal())
	}
	rows := ifcFreeRows(t, p)
	if len(rows) != 1 {
		t.Fatalf("G7：两轮之后免费留痕应只有一行，实得 %d 行（收费腿被写成了 instant_free ⇒ 客户账上少了一笔真消耗）", len(rows))
	}
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreePointUser, uidKey); got <= 0 {
		t.Fatalf("G7：免费腿的暗额度累加被收费腿挤掉了（实得 %d）", got)
	}
}

// TestInstantFree_G4IPLimitIsPerClientIP G4：IP 档按**客户端 IP 分桶**——同 IP 到量转付费、换 IP 恢复。
func TestInstantFree_G4IPLimitIsPerClientIP(t *testing.T) {
	p := newPlatformCostProbe(t)
	one := int64(1)
	// 经**保存面同一条落库路径**把 free_ip_daily 收到 1（读侧现读，不新造第二套配置形态）
	patch := ops.OperationsPolicy{Instant: ops.InstantPatch{FreeIPDaily: &one}}
	if err := p.s.Store.SetConfig("ops_policy", marshalOps(patch)); err != nil {
		t.Fatalf("写入 ops_policy 失败: %v", err)
	}
	p.s.invalidatePolicyCache() // ★ C31：不抬这一代，判据测的就是"缓存里的旧档"，与现网形态不符

	ipA, ipB := "203.0.113.7:5555", "198.51.100.9:5555"
	decide := func(remote string) instantRoute {
		r := httptest.NewRequest("POST", "/api/chat", nil)
		r.RemoteAddr = remote
		return p.s.instantDecide(r, p.tid, p.memberID, instantInput{Kind: "text", LangCount: 1, EstPoints: 1})
	}
	if rt := decide(ipA); !rt.Free {
		t.Fatalf("G4：同 IP 第一枪应免费（实得 reason=%q）——IP 桶为空就被拦＝判据读错了键", rt.Reason)
	}
	rt := decide(ipA)
	if rt.Free || rt.Reason != instantReasonQuotaIP {
		t.Fatalf("G4：同 IP 第二枪必须因**IP 档**转付费（实得 free=%v reason=%q）", rt.Free, rt.Reason)
	}
	// 换 IP ⇒ 立刻恢复免费。只做账号维度判据的实现在这一枪必红（现网形态＝一个人换网就永远出不免费档）
	if rt2 := decide(ipB); !rt2.Free {
		t.Fatalf("G4：换 IP 后仍被拦（reason=%q）⇒ 桶的键不是客户端 IP，是账号或租户", rt2.Reason)
	}
	// 反向对照：账号维度没被这一枪误伤（同账号、另一个 IP 仍然免费，说明 IP 与账号两把尺各自成立）
	uidKey := strconv.FormatInt(p.memberID, 10)
	if got := ifcBucket(t, p.s.Store, store.RateScopeFreeWall, uidKey); got != 0 {
		t.Fatalf("G4：IP 档转付费**不是撞墙**，不该写 free_wall（实得 %d）——写进去会把引导卡的文案点亮给一个正常付费用户", got)
	}
}
