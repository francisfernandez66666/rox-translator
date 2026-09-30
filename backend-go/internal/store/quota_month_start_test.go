// ============================================================================
// quota_month_start_test.go · 职责说明
// 「本月额度」那条读腿的**时区自洽**常设锁（★ 2026-10-01 〇-AF 建立）。
//
// 钉死的产品口径：
//
//	月度预算墙（TenantTokensUsedThisMonth / OrgTokensUsedThisMonth 供 CheckBudgetWalls 判墙）
//	的"本月起点"必须与 usage_ledger.created_at 的**写入口径**在同一时间轴上可比——
//	日历语义保持"本地自然月"不变，渲染口径必须是 UTC。
//
// 为什么需要这条锁（现网病灶，非假设）：
//
//	created_at 在本仓一律以 `time.Now().UTC().Format(time.RFC3339)` 写入，列型 TEXT，
//	谓词是 created_at>=? 的**字典序**比较；而 store.monthStart() 旧实现用**本地时区**渲染
//	边界（"2026-10-01T00:00:00+08:00" 这种带偏移的形态）。两者不同区 ⇒ 只要主机偏移非零，
//	"月初零点的 UTC 日期前缀"就和边界前缀错开：
//	  · +08 主机：每月 1 号本地 00:00–08:00 这八小时，本月已用恒读 **0**；
//	  · 任何非零偏移主机：跨月边界那一天的 UTC 行整体被判给错误的月份。
//	读 0 不是显示问题——CheckBudgetWalls 拿它判部门墙/组织墙，命中才拦翻译请求，
//	于是**月初那段预算墙形同虚设**（〇-AD 定的底线是"防超支不能换防误扣"，
//	这次的洞由日历给出，比谓词写错更难发现：全年只在月初亮一次，且当日没有任何报错）。
//
// 方言：钉 SQLite 内存库并显式改 config.C（AGENTS.md §一·4——run_uat 的 PG 模式会把
// 方言泄漏给同包内存库用例）。PG 侧覆盖由 run_uat 矩阵承担。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run MonthStart
// ============================================================================
package store

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
)

// insertLedgerAt 按**生产写入口径**（UTC RFC3339、TEXT 列）埋一条本月用量行。
// 刻意不复用 billing_platform_cost_test.go 的 seedPlatformCostLedger：那一族按"天偏移"造数，
// 跨不出月边界；这里要的正是"跨月边界 ±1 分钟"这种贴边的时刻，只能显式给时间戳。
func insertLedgerAt(t *testing.T, st *Store, at time.Time, quantity int64) {
	t.Helper()
	if _, err := st.db.Exec(
		`INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at)
		 VALUES (1, 11, 'translate', 'bigmodel', 'm', ?, 1, ?, 'text', 'pro', 'charge', ?)`,
		quantity, quantity, at.UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("埋 usage_ledger 行失败: %v", err)
	}
}

// TestMonthStartRendersUTCWhileKeepingLocalCalendar 边界本身的等值锁：
// monthStart() 必须等于「本地历法月初零点」的 **UTC 渲染**，一个字符都不许差。
// 反证：把 .UTC() 去掉（回到旧形态）在偏移非零的主机上立刻红；
// 把日历改成 UTC 月（业务口径迁移）同样红——两个方向都被这一条挡住。
func TestMonthStartRendersUTCWhileKeepingLocalCalendar(t *testing.T) {
	n := time.Now()
	want := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.Local).UTC().Format(time.RFC3339)
	got := monthStart()
	if got != want {
		t.Fatalf("本月起点应＝本地月初零点的 UTC 渲染 %s（与 usage_ledger.created_at 的 UTC 写入口径同轴可比），实得 %s"+
			" ⇒ 边界与戳记不同区，跨月那一天的行会被整体判给错误的月份", want, got)
	}
	// 形状锁：边界必须以 "Z" 收尾（UTC 渲染）。偏移形态哪怕日期正确，进了字典序比较就是另一回事。
	if !strings.HasSuffix(got, "Z") {
		t.Fatalf("本月起点不是 UTC 渲染：%s ⇒ 与 created_at 的「…Z」戳记不同轴，跨月窗口按字典序错位", got)
	}
}

// TestMonthWindowCountsRowsAcrossLocalMidnight 端到端腿：贴着本地月边界两侧各埋一行，
// 只有"新月"那一行必须被计数。这一条在任何偏移非零的主机上都能咬住旧形态
// （+08：新月行戳在 UTC 的上月最后一天，旧边界字符串 "本月01T00:00:00+08:00" 按字典序
// 反而比它大 ⇒ 该计量的行漏计；-05：同理漏计），在 UTC 主机上两侧本来重合 ⇒ 恒绿且无害。
func TestMonthWindowCountsRowsAcrossLocalMidnight(t *testing.T) {
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

	now := time.Now()
	localMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	// 上月最后一分钟（本地历法）⇒ 必须**不**进本月
	insertLedgerAt(t, st, localMonthStart.Add(-1*time.Minute), 700)
	// 新月第一分钟（本地历法）⇒ 必须进本月
	insertLedgerAt(t, st, localMonthStart.Add(1*time.Minute), 300)

	got, err := st.TenantTokensUsedThisMonth(1)
	if err != nil {
		t.Fatalf("TenantTokensUsedThisMonth 失败: %v", err)
	}
	if got != 300 {
		t.Fatalf("本月额度应恰=新月那一行 300（上月 700 必须缺席），实得 %d"+
			" ⇒ 月边界与 created_at 的 UTC 戳记不同轴，预算墙会在跨月处读错量（读 0＝月初八小时不设墙）", got)
	}

	// 反向对照：本用例的靶子不能是恒真的——若月起点函数整个坏成"取到当前时刻"，
	// 新月那行也会被判到窗口外，读数翻成 0 而不是 300。
	if zero := monthStart(); zero > time.Now().UTC().Format(time.RFC3339) {
		t.Fatalf("monthStart() 落到未来（%s）⇒ 本窗口天然为空集，上面的 300 断言没有意义", zero)
	}
}

// TestDayStartBoundAgreesWithDailyUsageKey 「显示尺子＝拦截尺子」的等值锁（★ 2026-10-01 〇-AF 补丁三）。
//
// 背景：收银台/订阅页的「今日已用」新走 store.DayStartBound()，而日额度墙
// （gateUsage → CheckDailyQuota → DailyUsage）用的是 usage_daily 的 day 主键
// `time.Now().UTC().Format("2006-01-02")`。两者必须落在**同一个日历日**上，
// 否则客户看到的"今日"与真拿去拦请求的"今日"是两把尺子（§一·11 与 F-12 的成因形态）。
// 反证：把 DayStartBound 改成本地日（或改用 Truncate 那种"绝对时长取整"的写法）在本条下立刻红。
func TestDayStartBoundAgreesWithDailyUsageKey(t *testing.T) {
	got := DayStartBound()
	wantKey := time.Now().UTC().Format("2006-01-02") // 与 billing.go 写侧/读侧同一个键
	if len(got) < 10 || got[:10] != wantKey {
		t.Fatalf("DayStartBound 的日期前缀必须＝日额度墙的 day 键 %s，实得 %s"+
			" ⇒ 页面「今日已用」与日限额判定不同区，会出现「没到限额却显示用满」或反之", wantKey, got)
	}
	if !strings.HasSuffix(got, "Z") || !strings.HasSuffix(got, "T00:00:00Z") {
		t.Fatalf("DayStartBound 必须是 UTC 零点渲染（与 created_at 的 UTC 戳记同轴可做字典序比较），实得 %s", got)
	}
	// 端到端腿：贴日边界两侧各埋一行，只有"新日"那行进今日窗口。
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := New(sqlDB)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// 昨日最后一分钟 / 今日第一分钟（都按 UTC 历法，与 day 键同区）
	nowU := time.Now().UTC()
	todayStart := time.Date(nowU.Year(), nowU.Month(), nowU.Day(), 0, 0, 0, 0, time.UTC)
	insertLedgerAt(t, st, todayStart.Add(-1*time.Minute), 500)
	insertLedgerAt(t, st, todayStart.Add(1*time.Minute), 90)

	var used int64
	if err := sqlDB.QueryRow(
		`SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=1 AND created_at>=?`,
		DayStartBound()).Scan(&used); err != nil {
		t.Fatalf("按 DayStartBound 聚合今日用量失败: %v", err)
	}
	if used != 90 {
		t.Fatalf("今日窗口应恰=新日那一行 90（昨日 500 必须缺席），实得 %d"+
			" ⇒ 日边界与 created_at 的 UTC 戳记不同轴（月初同款病灶，只是这次是日）", used)
	}
}
