// ============================================================================
// billing_platform_cost_test.go · 职责说明
// 「实扣 / 留痕 / 结算」三态分桶（★ 2026-09-29 〇-AD）的 store 层常设锁。
//
// 钉死的产品口径（任一条被后续改动悄悄破坏即红灯）：
//
//	① 一切「客户消耗」读法只认 store.CustomerUsagePred（补丁三定稿）＝
//	   排掉平台承担的用途标签行（task_type ∈ kb_embed/system_task/pack_scrape/evals）
//	   ＋排掉没有归因到具体用户的留痕/结算行（uid=0 的 'log'/'settle'）——
//	   个人用量、组织用量、平台每用户用量、租户按任务类型/按供应商/按日趋势、
//	   自服务账单的日序列与流水分页，共 8 条读腿一个尺子；
//	② 空串必须算客户消耗（charge_kind 列是 P1-2/2026-09-14 才补的，历史实扣行该列为空）；
//	②b 推广期免费（策略 charge=false）客户自己的行是 charge_kind='log'，**必须仍然可见**——
//	   把客户面尺子写成「只认实扣」会把这一类客户的「我的用量/我的流水/CSV」整页抹空，
//	   这正是补丁一上线后 run_uat T59「我的用量计数增量＝台账新增行数」差值恒 0 抓到的红灯；
//	③ 'log'（平台承担/推广期免费/欠费批次留痕）与 'settle'（欠费清零调整）
//	   既不进客户看板，也不互相混：平台承担合计=Σ 'log'，坏账=Σ 'settle' 单列；
//	④ 留痕金额不会消失，它必须能从 PlatformCostSummary 读回来（可审计性）。
//
// 现网病灶（本批修复的动因，非假设）：「系统用量」本层累计 338,544 积分里
// 337,819 落在 user_id=0 的「系统/后台任务」一行（占 99.8%），而三位真实用户
// 合计仅 ≈725 积分——旧 SUM 不分 charge_kind，把一分没扣的留痕当客户消耗投给客户。
//
// 方言：钉 SQLite 内存库并显式改 config.C（AGENTS.md §一·4——run_uat 的 PG 模式会把
// 方言泄漏给同包内存库用例）。PG 侧覆盖由 run_uat 矩阵承担。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run PlatformCost
// ============================================================================
package store

import (
	"database/sql"
	"testing"
	"time"

	"translator/internal/config"
)

// pcLedgerRow 一条 usage_ledger 造数（只给必要列，其余按库默认）。
type pcLedgerRow struct {
	tid, uid           int64
	taskType, provider string
	quantity, cost     int64
	chargeKind         string // '' = 历史行（该列默认值之前的形态）
	dayOffset          int    // 0=今天；N=往前 N 天（测区间谓词用）
}

// newPlatformCostEnv 建分桶测试环境：钉方言 + 租户 1 + 两名用户（11/12）。
func newPlatformCostEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	if _, err := st.CreateUser(1, "pc_u11", "hash-1", "实扣用户", RoleUser, 1, 0); err != nil {
		t.Fatalf("创建用户 11 失败: %v", err)
	}
	if _, err := st.CreateUser(1, "pc_u12", "hash-2", "历史空串用户", RoleUser, 1, 0); err != nil {
		t.Fatalf("创建用户 12 失败: %v", err)
	}
	return st
}

// seedPlatformCostLedger 按上表造数（created_at 一律 UTC RFC3339，与 C22 台账域口径一致）。
func seedPlatformCostLedger(t *testing.T, st *Store, rows []pcLedgerRow) {
	t.Helper()
	for i, r := range rows {
		ts := time.Now().UTC().AddDate(0, 0, -r.dayOffset).Format(time.RFC3339)
		// charge_kind 为空串时必须显式写 ''（吃库默认值 'charge' 就不是「历史行」形态了）
		if _, err := st.db.Exec(
			`INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at)
			 VALUES (?,?,?,?,?,?,1,?, 'text','pro',?,?)`,
			r.tid, r.uid, r.taskType, r.provider, "m"+r.provider, r.quantity, r.cost, r.chargeKind, ts); err != nil {
			t.Fatalf("第 %d 条造数失败: %v", i, err)
		}
	}
}

// pcRows 本批锁的标准盘面：两种实扣形态 + 两种留痕来源 + 一条结算 + 一条昨天行。
func pcRows() []pcLedgerRow {
	return []pcLedgerRow{
		{tid: 1, uid: 11, taskType: "translate", provider: "bigmodel", quantity: 100, cost: 100, chargeKind: "charge"},
		{tid: 1, uid: 12, taskType: "translate", provider: "bigmodel", quantity: 40, cost: 40, chargeKind: ""}, // ②历史空串
		{tid: 1, uid: 11, taskType: "kb_embed", provider: "siliconflow", quantity: 5000, cost: 5000, chargeKind: "log"},
		{tid: 1, uid: 0, taskType: "translate", provider: "bigmodel", quantity: 900, cost: 900, chargeKind: "log"},
		{tid: 1, uid: 0, taskType: "settle_exhausted", provider: "settle", quantity: 70, cost: 70, chargeKind: "settle"},
		{tid: 1, uid: 11, taskType: "translate", provider: "bigmodel", quantity: 30, cost: 30, chargeKind: "charge", dayOffset: 1},
		// ②b 推广期免费：策略 charge=false 时客户自己下单的行就是这种形态（log + 真实 uid），
		//    它没扣积分，但它是客户的用量——客户面必须看得见，平台承担账也要认它是平台垫的。
		{tid: 1, uid: 11, taskType: "translate", provider: "bigmodel", quantity: 20, cost: 20, chargeKind: "log"},
	}
}

// TestPlatformCostRealDebitOnly ①②：八条「客户消耗」读腿一律只数实扣，且空串算实扣。
func TestPlatformCostRealDebitOnly(t *testing.T) {
	st := newPlatformCostEnv(t)
	seedPlatformCostLedger(t, st, pcRows())

	t.Run("个人用量 UsageByUser 只数实扣", func(t *testing.T) {
		total, _, cnt, err := st.UsageByUser(1, 11, "", "")
		if err != nil {
			t.Fatalf("UsageByUser 失败: %v", err)
		}
		if total != 150 || cnt != 3 { // 今天 100 + 免费期 20 + 昨天 30，5000 的平台承担行不进
			t.Fatalf("个人用量应=150/3 笔（含免费期那条 log），实得 total=%d cnt=%d", total, cnt)
		}
	})
	t.Run("历史空串行必须仍算实扣（只认 'charge' 即假绿）", func(t *testing.T) {
		total, _, _, err := st.UsageByUser(1, 12, "", "")
		if err != nil || total != 40 {
			t.Fatalf("空串 charge_kind 应计入实扣=40，实得 total=%d err=%v", total, err)
		}
	})
	t.Run("组织用量 UsageByOrg 不再出现 user_id=0 的留痕行", func(t *testing.T) {
		m, err := st.UsageByOrg(1, nil, "", "")
		if err != nil {
			t.Fatalf("UsageByOrg 失败: %v", err)
		}
		if m[11] != 150 || m[12] != 40 {
			t.Fatalf("组织用量应为 11→150 / 12→40，实得 %+v", m)
		}
		if _, ok := m[0]; ok {
			t.Fatalf("user_id=0 只剩无归因的留痕/结算行，不该出现在客户聚合里：%+v", m)
		}
	})
	t.Run("平台每用户用量 UsageAllByUser 同尺", func(t *testing.T) {
		m, err := st.UsageAllByUser("", "")
		if err != nil {
			t.Fatalf("UsageAllByUser 失败: %v", err)
		}
		if m[11] != 150 || m[12] != 40 {
			t.Fatalf("平台侧每用户 quantity 应为 11→150 / 12→40，实得 %+v", m)
		}
		if _, ok := m[0]; ok {
			t.Fatalf("平台承担/结算行不该进平台侧客户消耗聚合：%+v", m)
		}
	})
	t.Run("任务类型/供应商/日趋势三个租户侧读腿同尺", func(t *testing.T) {
		byTask, total, err := st.UsageStats(1)
		if err != nil {
			t.Fatalf("UsageStats 失败: %v", err)
		}
		if total != 190 || byTask["translate"] != 190 || byTask["kb_embed"] != 0 {
			t.Fatalf("UsageStats 应只见客户用量 translate=190，实得 total=%d map=%+v", total, byTask)
		}
		byProv, err := st.UsageStatsByProvider(1)
		if err != nil {
			t.Fatalf("UsageStatsByProvider 失败: %v", err)
		}
		if byProv["bigmodel / mbigmodel"] != 190 {
			t.Fatalf("按供应商的客户用量应为 bigmodel=190，实得 %+v", byProv)
		}
		trend, err := st.UsageTrend(1, 7)
		if err != nil {
			t.Fatalf("UsageTrend 失败: %v", err)
		}
		today := time.Now().UTC().Format("2006-01-02")
		if trend[today] != 160 {
			t.Fatalf("今日趋势应=160（100+40+20，900 的无归因留痕不进），实得 %+v", trend)
		}
	})
	t.Run("自服务账单的日序列与流水分页同尺（汇总与明细必须同进同出）", func(t *testing.T) {
		pts := st.MyDailyUsage(1, 11, 30)
		var sum int64
		for _, p := range pts {
			sum += p.Cost
		}
		if len(pts) != 2 || sum != 150 {
			t.Fatalf("MyDailyUsage 应为 2 天合计 150，实得 %+v", pts)
		}
		rows, cnt, err := st.MyLedgerPage(1, 11, "", 20, 0)
		if err != nil {
			t.Fatalf("MyLedgerPage 失败: %v", err)
		}
		if cnt != 3 || len(rows) != 3 {
			t.Fatalf("流水分页应只有 3 条客户用量（含免费期那条），实得 total=%d rows=%d", cnt, len(rows))
		}
		for _, r := range rows {
			if r.TaskType == "kb_embed" {
				t.Fatalf("留痕行（kb_embed 5000）混进了客户自己的流水明细")
			}
		}
	})
}

// TestPlatformCostSummary ③④：平台承担合计=Σ'log'、坏账单列不混，日期区间谓词照旧生效。
func TestPlatformCostSummary(t *testing.T) {
	st := newPlatformCostEnv(t)
	seedPlatformCostLedger(t, st, pcRows())

	pc, err := st.PlatformCostSummary("", "")
	if err != nil {
		t.Fatalf("PlatformCostSummary 失败: %v", err)
	}
	if pc.Total != 5920 {
		t.Fatalf("平台承担合计应=5000(kb_embed)+900(无归因留痕)+20(免费期客户行)=5920，实得 %d", pc.Total)
	}
	if pc.Settled != 70 {
		t.Fatalf("欠费清零调整应单列在 Settled=70，实得 %d", pc.Settled)
	}
	if pc.ByReason["kb_embed"] != 5000 || pc.ByReason["translate"] != 920 {
		t.Fatalf("按用途拆分应 kb_embed=5000 / translate=920，实得 %+v", pc.ByReason)
	}
	if pc.ByModel["siliconflow / msiliconflow"] != 5000 {
		t.Fatalf("按模型拆分应 siliconflow=5000，实得 %+v", pc.ByModel)
	}
	// 反向锁：实扣行绝不允许被算进平台承担（否则「谁垫了钱」把客户自己付的也算一遍）
	if pc.Total == 5920+150 {
		t.Fatalf("实扣行被混进平台承担合计")
	}

	// 区间谓词：只取昨天 → 5900 全部落在今天，昨天只有一条 charge 行 ⇒ 平台承担应为 0
	y := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	pc2, err := st.PlatformCostSummary(y, y)
	if err != nil {
		t.Fatalf("区间 PlatformCostSummary 失败: %v", err)
	}
	if pc2.Total != 0 || pc2.Settled != 0 {
		t.Fatalf("昨日无留痕/结算行，应全为 0，实得 total=%d settled=%d", pc2.Total, pc2.Settled)
	}
}

// TestPlatformCostPredsAreSingleSource 谓词等值锁：三把尺子各自钉死字面值，且互不等价。
// 判据写成等值而不是「包含」，是为了让「顺手统一成一把」这类改法直接红在这一处，
// 而不是散落到八条读腿里各自假绿（AGENTS.md §一·5 的等值锁口径）。
func TestPlatformCostPredsAreSingleSource(t *testing.T) {
	if RealDebitPred != "charge_kind IN ('','charge')" {
		t.Fatalf("实扣谓词被改动：%q（空串代表 P1-2 之前的历史实扣行，去掉即抹掉老账）", RealDebitPred)
	}
	if PlatformTaskTypeExclPred != "task_type NOT IN ('kb_embed','system_task','pack_scrape','evals')" {
		t.Fatalf("额度侧/平台标签谓词被改动：%q", PlatformTaskTypeExclPred)
	}
	if CustomerUsagePred != PlatformTaskTypeExclPred+" AND "+UnattributedTraceExclPred {
		t.Fatalf("客户面尺子不再是「标签名单 ＋ 无归因留痕」两段拼出来的：%q", CustomerUsagePred)
	}
	if UnattributedTraceExclPred != "NOT (charge_kind IN ('log','settle') AND COALESCE(user_id,0)=0)" {
		t.Fatalf("无归因留痕排除腿被改动：%q（去掉即把现网 337,819 那种 uid=0 的行投回客户看板）", UnattributedTraceExclPred)
	}
	// 前缀版与裸版必须逐字同源（UsageByOrg 用带 l. 的那一份，抄写即分叉）
	if CustomerUsagePredWith("") != CustomerUsagePred {
		t.Fatalf("CustomerUsagePredWith 与常量分叉：\n%q\n%q", CustomerUsagePredWith(""), CustomerUsagePred)
	}
	if got := CustomerUsagePredWith("l."); got != "l."+PlatformTaskTypeExclPred+" AND NOT (l.charge_kind IN ('log','settle') AND COALESCE(l.user_id,0)=0)" {
		t.Fatalf("列前缀版形态不对：%q", got)
	}
	// 三把尺子两两不等价：谁被并成一把，谁就误伤另一类客户（补丁一/二/三各踩过一次）
	if CustomerUsagePred == RealDebitPred || CustomerUsagePred == PlatformTaskTypeExclPred {
		t.Fatal("客户面尺子被并成了实扣尺子或额度尺子（免费期客户流水会被抹空／平台行会重新吃额度）")
	}
}

// TestPlatformCostFreePromoUsageStaysVisible ②b：推广期免费（策略 charge=false）客户自己的
// 'log' 行必须留在客户面——这条就是 run_uat T59 现场抓到的红灯（补丁一把实扣当客户面尺子，
// 「我的用量」计数增量恒 0，客户流水整页空）。反向对照用同盘的 uid=0 留痕行：
// 同是 charge_kind='log'，有归因的进客户面、无归因的不进，差值只能由这条腿产生。
func TestPlatformCostFreePromoUsageStaysVisible(t *testing.T) {
	st := newPlatformCostEnv(t)
	seedPlatformCostLedger(t, st, pcRows())

	rows, cnt, err := st.MyLedgerPage(1, 11, "", 20, 0)
	if err != nil || cnt != 3 {
		t.Fatalf("免费期那条 log 行必须在客户自己的流水里（应 3 条），实得 cnt=%d err=%v", cnt, err)
	}
	// MyLedgerPage 的出参不带 charge_kind 列（客户面不需要它），所以按量认行：
	// 三条应恰为 100（实扣）／30（昨日实扣）／20（免费期留痕）——缺 20 即「只认实扣」复发。
	seen := map[int64]bool{}
	for _, r := range rows {
		seen[r.Quantity] = true
	}
	if !seen[20] || !seen[100] || !seen[30] || len(seen) != 3 {
		t.Fatalf("流水应含 100/30/20 三笔（20 是免费期那条 log），实得 %+v", seen)
	}
	// 反向对照：同盘 uid=0 的 900 留痕行确实被排掉了（否则本用例只是「压根没过滤」的假绿）
	m, err := st.UsageByOrg(1, nil, "", "")
	if err != nil {
		t.Fatalf("UsageByOrg 失败: %v", err)
	}
	if _, ok := m[0]; ok {
		t.Fatalf("无归因留痕行又回到客户看板了：%+v", m)
	}
	if m[11] != 150 {
		t.Fatalf("客户 11 应=150（100+20 免费+30 昨日），实得 %+v", m)
	}
}

// TestPlatformCost08AD2_QuotaLegsExcludePlatform 〇-AD 补丁二·额度侧那只口子。
//
// 现网形态（本批第一只口子修完仍然会发生的）：平台承担的用量不再扣积分，
// 但它照旧进 usage_daily（CheckDailyQuota 的读数源）与部门/组织月度预算（CheckBudgetWalls
// 的读数源）⇒ 客户一分没掉，却被「已达到今日用量上限 / 部门 token 已耗尽」拦在门外。
// 所以额度腿必须排平台标签；而它**不能**用实扣谓词（未开强制计费的租户全是 'log' 行，
// 套实扣会把他们的预算墙判成「一点没用」，防超支直接失效）——两条腿各用各的尺子，
// 这里同时把「两者不等价」这件事钉成断言，防止后人顺手统一成一把。
func TestPlatformCost08AD2_QuotaLegsExcludePlatform(t *testing.T) {
	st := newPlatformCostEnv(t)
	seedPlatformCostLedger(t, st, pcRows()) // 100+40+30 实扣 / 20 免费期 / 5000 kb_embed / 900 无归因留痕 / 70 settle

	t.Run("全租户本月额度：排 kb_embed，但历史留痕 translate 仍计（两把尺子的分界）", func(t *testing.T) {
		got, err := st.TenantTokensUsedThisMonth(1)
		if err != nil {
			t.Fatalf("TenantTokensUsedThisMonth 失败: %v", err)
		}
		if got != 1160 { // 100+40+30（实扣）＋20+900（客户自发的免费留痕）＋70（settle 行按量计，生产该行 quantity=0）
			t.Fatalf("本月额度应=1160（5000 的平台承担必须缺席），实得 %d", got)
		}
		// 反向对照：展示腿（客户面尺子）在同一盘数据上是 190，和额度腿的 1160 必然不等
		// ——这个差值就是「900 的无归因留痕算客户自发用量、但不进客户看板」这件事本身
		if same := got == 190; same {
			t.Fatalf("额度腿与展示腿被统一成一把尺子了（未强制计费租户的预算墙会因此恒 0）")
		}
	})
	t.Run("日额度读数：平台行不进计数器，客户行进", func(t *testing.T) {
		before, err := st.DailyUsage(1) // usage_daily 无当日行 ⇒ 走 ledger 兜底
		if err != nil {
			t.Fatalf("DailyUsage 失败: %v", err)
		}
		if before != 1130 { // 今日 100+40+900+70+20，5000 的 kb_embed 缺席
			t.Fatalf("兜底日用量应=1130（平台 Embedding 不计），实得 %d", before)
		}
		// 写一笔平台承担的留痕：日计数器不许被碰
		if err := st.LogUsage(1, 11, "kb_embed", "siliconflow", "bge-m3", "", 9000, "platform", "pro"); err != nil {
			t.Fatalf("LogUsage(kb_embed) 失败: %v", err)
		}
		if v, _ := st.DailyUsage(1); v != before {
			t.Fatalf("平台承担的 LogUsage 绝不该累加 usage_daily（客户的今日额度被吃），实得 %d vs %d", v, before)
		}
		// 反向对照：同参但 task_type=translate 必须累加，否则上面那条「没变」是链路没跑的假绿
		if err := st.LogUsage(1, 11, "translate", "bigmodel", "glm-4", "zh", 123, "text", "pro"); err != nil {
			t.Fatalf("LogUsage(translate) 失败: %v", err)
		}
		after, _ := st.DailyUsage(1)
		want := st.logRowCostForTest(t, "translate", 123)
		// 计数器一旦有当日行，DailyUsage 就优先读它（B6 的 O(1) 口径）：
		// 造数那 6 行是裸 SQL 插的、没走计数器，所以这里应恰好只等于刚累加进去的这一笔。
		// 这一等式同时钉住两件事：客户的量进了计数器（≠0 且 ≠before），平台的 9000 没进。
		if after != want || after == before {
			t.Fatalf("客户自发用量应累加进日计数器且平台那笔不计：应=%d（且≠%d），实得 %d", want, before, after)
		}
	})
	t.Run("明细列表与 CSV 导出：客户面只数实扣，全量面保留", func(t *testing.T) {
		charged, err := st.UsageLedgerList(1, 50, 0, true)
		if err != nil {
			t.Fatalf("UsageLedgerList(chargedOnly) 失败: %v", err)
		}
		if len(charged) != 5 { // 100/40/30 实扣 + 20 免费期 + 123 那笔 translate 留痕（有归因＝客户自己的）
			t.Fatalf("客户面明细应只有 5 条客户用量，实得 %d", len(charged))
		}
		full, err := st.UsageLedgerList(1, 500, 0, false)
		if err != nil {
			t.Fatalf("UsageLedgerList(全量) 失败: %v", err)
		}
		if len(full) != 9 { // 7 条造数 + 上面两笔 LogUsage（kb_embed 9000 / translate 123）
			t.Fatalf("全量明细应含留痕行（超管与 GDPR 面可审计），实得 %d", len(full))
		}
		expCharged, err := st.UsageLedgerForExport(1, "", "", 1000, true)
		// 两笔 LogUsage 都是 charge_kind='log'：kb_embed 那笔被标签排掉，
		// translate 那笔有归因（uid=11）⇒ 留在客户面（免费期客户对账要能翻到自己这一行）
		if err != nil || len(expCharged) != 5 {
			t.Fatalf("客户面 CSV 导出应=5 条客户用量，实得 %d err=%v", len(expCharged), err)
		}
		expFull, err := st.UsageLedgerForExport(1, "", "", 1000, false)
		if err != nil || len(expFull) != 9 {
			t.Fatalf("全量 CSV 导出应=9 条，实得 %d err=%v", len(expFull), err)
		}
		for _, r := range expCharged {
			if r.ChargeKind == "log" && isPlatformTaskType(r.TaskType) {
				t.Fatalf("平台承担行混进了客户面对账 CSV：%+v", r)
			}
		}
	})
}

// logRowCostForTest 用与写点同一条定价函数算出 quantity 对应的 cost（避免测试里硬编码倍率）。
func (s *Store) logRowCostForTest(t *testing.T, taskType string, quantity int64) int64 {
	t.Helper()
	_, cost := s.pricingCost(taskType, "bigmodel", "zh", quantity)
	return cost
}
