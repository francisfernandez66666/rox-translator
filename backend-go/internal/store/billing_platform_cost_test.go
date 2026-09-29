// ============================================================================
// billing_platform_cost_test.go · 职责说明
// 「实扣 / 留痕 / 结算」三态分桶（★ 2026-09-29 〇-AD）的 store 层常设锁。
//
// 钉死的产品口径（任一条被后续改动悄悄破坏即红灯）：
//
//	① 一切「客户消耗」读法只认 charge_kind ∈ ('', 'charge')——
//	   个人用量、组织用量、平台每用户用量、租户按任务类型/按供应商/按日趋势、
//	   自服务账单的日序列与流水分页，共 8 条读腿一个尺子；
//	② 空串必须算实扣（charge_kind 列是 P1-2/2026-09-14 才补的，历史实扣行该列为空），
//	   只认 'charge' 会把 09-14 之前的真实消费整段抹成 0——这是本批最容易写反的一条；
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
		if total != 130 || cnt != 2 { // 今天 100 + 昨天 30，5000 留痕行不进
			t.Fatalf("个人用量应只数实扣=130/2 笔，实得 total=%d cnt=%d", total, cnt)
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
		if m[11] != 130 || m[12] != 40 {
			t.Fatalf("组织实扣应为 11→130 / 12→40，实得 %+v", m)
		}
		if _, ok := m[0]; ok {
			t.Fatalf("user_id=0 只剩 900 的留痕行，不该出现在实扣聚合里：%+v", m)
		}
	})
	t.Run("平台每用户用量 UsageAllByUser 同尺", func(t *testing.T) {
		m, err := st.UsageAllByUser("", "")
		if err != nil {
			t.Fatalf("UsageAllByUser 失败: %v", err)
		}
		if m[11] != 130 || m[12] != 40 {
			t.Fatalf("平台实扣 quantity 应为 11→130 / 12→40，实得 %+v", m)
		}
		if _, ok := m[0]; ok {
			t.Fatalf("留痕/结算行不该进平台实扣聚合：%+v", m)
		}
	})
	t.Run("任务类型/供应商/日趋势三个租户侧读腿同尺", func(t *testing.T) {
		byTask, total, err := st.UsageStats(1)
		if err != nil {
			t.Fatalf("UsageStats 失败: %v", err)
		}
		if total != 170 || byTask["translate"] != 170 || byTask["kb_embed"] != 0 {
			t.Fatalf("UsageStats 应只见实扣 translate=170，实得 total=%d map=%+v", total, byTask)
		}
		byProv, err := st.UsageStatsByProvider(1)
		if err != nil {
			t.Fatalf("UsageStatsByProvider 失败: %v", err)
		}
		if byProv["bigmodel / mbigmodel"] != 170 {
			t.Fatalf("按供应商实扣应为 bigmodel=170，实得 %+v", byProv)
		}
		trend, err := st.UsageTrend(1, 7)
		if err != nil {
			t.Fatalf("UsageTrend 失败: %v", err)
		}
		today := time.Now().UTC().Format("2006-01-02")
		if trend[today] != 140 {
			t.Fatalf("今日趋势应=140（100+40，900 留痕不进），实得 %+v", trend)
		}
	})
	t.Run("自服务账单的日序列与流水分页同尺（汇总与明细必须同进同出）", func(t *testing.T) {
		pts := st.MyDailyUsage(1, 11, 30)
		var sum int64
		for _, p := range pts {
			sum += p.Cost
		}
		if len(pts) != 2 || sum != 130 {
			t.Fatalf("MyDailyUsage 应为 2 天合计 130，实得 %+v", pts)
		}
		rows, cnt, err := st.MyLedgerPage(1, 11, "", 20, 0)
		if err != nil {
			t.Fatalf("MyLedgerPage 失败: %v", err)
		}
		if cnt != 2 || len(rows) != 2 {
			t.Fatalf("流水分页应只有 2 条实扣，实得 total=%d rows=%d", cnt, len(rows))
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
	if pc.Total != 5900 {
		t.Fatalf("平台承担合计应=5000(kb_embed)+900(留痕 translate)=5900，实得 %d", pc.Total)
	}
	if pc.Settled != 70 {
		t.Fatalf("欠费清零调整应单列在 Settled=70，实得 %d", pc.Settled)
	}
	if pc.ByReason["kb_embed"] != 5000 || pc.ByReason["translate"] != 900 {
		t.Fatalf("按用途拆分应 kb_embed=5000 / translate=900，实得 %+v", pc.ByReason)
	}
	if pc.ByModel["siliconflow / msiliconflow"] != 5000 {
		t.Fatalf("按模型拆分应 siliconflow=5000，实得 %+v", pc.ByModel)
	}
	// 反向锁：实扣行绝不允许被算进平台承担（否则「谁垫了钱」把客户自己付的也算一遍）
	if pc.Total == 5900+170 {
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

// TestPlatformCostRealDebitPredIsSingleSource 谓词等值锁：尺子本身钉死字面值。
// 判据写成等值而不是「包含」，是为了让「只认 'charge'」这类改法直接红在这一处，
// 而不是散落到八个读腿里各自假绿（AGENTS.md §一·5 的等值锁口径）。
func TestPlatformCostRealDebitPredIsSingleSource(t *testing.T) {
	if realDebitPred != "charge_kind IN ('','charge')" {
		t.Fatalf("实扣谓词被改动：%q（空串代表 P1-2 之前的历史实扣行，去掉即抹掉老账）", realDebitPred)
	}
}
