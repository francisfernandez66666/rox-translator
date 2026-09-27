// ============ points_rate_rebase_test.go · 职责说明 ============
// ★ F-78（2026-09-28 〇-X）积分汇率 1:300 → 1:400「存量等值补发」的回归断言。
// 这一批改档最容易被后续改动悄悄回退的不是常量，而是三条口径，逐条钉住：
//
//	① 等值性：每一笔「按积分铸出的额度」补发前后折算出的积分数分毫不差（客户视角零位移），
//	   而「真实消耗」列一律不动（动了就是伪造历史成本）；
//	② 钱口径：token ×4/3 与尺子 ×3/4 精确抵消——3,000 积分裸充值补发前后都折 ¥299.00，
//	   且 ComputeUpgradeCredit 的「剩余/订单量」比值不因逐行取整而漂移；
//	③ 一次性：新库（种子即 400）不触发、不占凭证；老库跑完再跑第二遍零位移；
//	   即便有人事后把汇率手工改回 300，凭证也会拦住二次放大（防超发）。
//
// 另附跨包联动锁：ops 的两枚字面出厂档必须等于 store 常量的「积分面值 × 汇率」乘积
// （ops 不得 import store，见 AGENTS.md §一·3，故用测试而不是引用来防漂移）。
//
// 方言：固定 SQLite 内存库并显式钉死 config.C（AGENTS.md §4，防 run_uat 的 PG 模式泄漏）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestPointsRateRebase
// =============================================
package store

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/ops"

	_ "modernc.org/sqlite"
)

// rebaseEnv 造一套「旧汇率（1:300）时代的生产形状」库：
// 先把新库跑出来，再把汇率/尺子/三枚积分面值键按字面值改回旧档，并灌入旧口径额度。
// tenants 表需先于 New 建好（口径同 batchBEnv）；orgs.token_limit 由 New 内补列迁移负责。
func rebaseEnv(t *testing.T) (*Store, map[string]int64) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"code" TEXT UNIQUE NOT NULL,
		"name" TEXT NOT NULL DEFAULT '',
		"status" TEXT NOT NULL DEFAULT 'active',
		"expires_at" TEXT NOT NULL DEFAULT '',
		"permissions" TEXT NOT NULL DEFAULT '{}',
		"is_personal" INTEGER NOT NULL DEFAULT 0,
		"created_at" TEXT,
		"updated_at" TEXT
	)`); err != nil {
		t.Fatalf("建 tenants 表失败: %v", err)
	}
	s, err := New(conn)
	if err != nil {
		t.Fatalf("初始化 Store 失败: %v", err)
	}

	// ---------- 回退到旧档（模拟 2026-09-28 之前的生产库）----------
	legacy := [][2]string{
		{"points_tokens_rate", "300"},
		{"price_fen_per_million_tokens", "33222"},
		{"free_trial_tokens", "300000"},
		{"invite_reward_tokens", "300000"},
		{"inviter_paid_reward_tokens", "500000"},
		{"low_balance_alert_tokens", "100000"},       // token 单位阈值：不许被缩放
		{"kb_upload_reward_tokens_per_entry", "200"}, // 同上
		{"estimate_tokens_per_sentence", "500"},      // 句↔token 口径：同上
		{ConfigPointsRateRebase, ""},                 // 先占位再删除：确保没有补发凭证
	}
	for _, kv := range legacy {
		if err := s.SetConfig(kv[0], kv[1]); err != nil {
			t.Fatalf("写配置 %s 失败: %v", kv[0], err)
		}
	}
	rebaseExec(t, s, "DELETE FROM system_config WHERE key=?", ConfigPointsRateRebase)

	// ---------- 先记下「全新库出厂档」（New() 刚按 1:400 种下的值），供「老库补发==新库种子」对照 ----------
	freshSeeds := map[string]int64{}
	if rows, err := db.Query(s.db, db.CurrentDialect(), "SELECT title, reward_tokens FROM user_tasks"); err == nil {
		for rows.Next() {
			var title string
			var v int64
			if rows.Scan(&title, &v) == nil {
				freshSeeds[title] = v
			}
		}
		_ = rows.Close()
	}

	// ---------- 把 New() 里已按新汇率种下的出厂任务奖励「降回旧档算术」----------
	// 真实老库里这些行当年就是按 1:300 折的（生产实测 30,000/30,000/150,000/300,000/180,000），
	// 而本环境的 New() 用的是新档种子；不降回来，测试就变成了「新档行被再缩一次」的假象。
	// ×3/4 四舍五入即按「积分面值不变」换回旧汇率。
	rebaseExec(t, s, `UPDATE user_tasks SET reward_tokens = (reward_tokens*? + ?) / ?`, 3, 2, 4)

	// ---------- 两个租户：一个「字符派生日限额」、一个「按积分钉的日限额」----------
	rebaseExec(t, s, `INSERT INTO tenants (code, name, permissions) VALUES
		('tchar','字符墙租户','{"max_daily_chars":20000,"max_daily_tokens":20000}'),
		('tpoint','积分墙租户','{"max_daily_chars":100000,"max_daily_tokens":300000}')`)

	// ---------- 旧口径额度（全部按 1:300 铸出）----------
	// balance_accounts：tid1 90,000（=300 积分）、tid2 100,000（=333.33→333 积分）
	rebaseExec(t, s, `INSERT INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES
		(1, 90000, 'tokens', ''), (2, 100000, 'tokens', '')`)
	// quota_grants：一条部分消耗的 trial（left 非零才看得见缩放效果）、一条满额 plan
	rebaseExec(t, s, `INSERT INTO quota_grants (tenant_id, kind, total, "left", expires_at, source, ref_id, created_at) VALUES
		(1,'trial',90000,30000,'2099-01-01T00:00:00Z','register',0,''),
		(2,'plan',900000,900000,'2099-01-01T00:00:00Z','order',1,'')`)
	// orders：3,000 积分的已支付裸充值单（90 万 token）+ 一张 100 积分的待付单
	rebaseExec(t, s, `INSERT INTO orders (tenant_id, order_no, amount_tokens, amount_money, status, created_at) VALUES
		(1,'RO_LEGACY_1',900000,299,'paid',''),
		(2,'RO_LEGACY_2',30000,10.03,'pending','')`)
	rebaseExec(t, s, `INSERT INTO payments (order_id, tenant_id, amount_tokens, amount_money, status, created_at) VALUES
		(1,1,900000,299,'paid','')`)
	// user_tasks：100 积分档任务奖励（旧折 30,000）。标题必须独特——TaskRewardMigrate 已在 New()
	// 里种过出厂任务（含「每日登录」），撞名会让断言读到出厂行而不是本用例造的旧档行。
	rebaseExec(t, s, `INSERT INTO user_tasks (task_type, title, reward_tokens, enabled, sort_order, created_at, updated_at)
		VALUES ('daily','F78旧档任务',90000,1,1,'','')`)
	// orgs：200 积分档组织上限
	rebaseExec(t, s, `INSERT INTO orgs (tenant_id, parent_id, name, type, token_limit)
		VALUES (1,0,'F78总部','org',60000)`)
	// usage_ledger：真实消耗 90,000（成本事实，必须零位移）
	rebaseExec(t, s, `INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at)
		VALUES (1,1,'translate','p','m',1000,90,90000,'text','pro','charge','')`)
	return s, freshSeeds
}

// assertRebasedTo 断言某查询结果为期望整数（列级等值锁统一入口）。
func assertRebasedTo(t *testing.T, s *Store, want int64, why, q string, args ...interface{}) {
	t.Helper()
	if got := rebaseInt64(t, s, q, args...); got != want {
		t.Fatalf("%s：应 %d，got %d", why, want, got)
	}
}

// rebaseExec / rebaseInt64 测试侧直读写：统一走方言层（AGENTS.md §4），不裸用 database/sql，
// 免得整包以 PG 方言自检时因占位符差异假红。
func rebaseExec(t *testing.T, s *Store, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(s.db, db.CurrentDialect(), q, args...); err != nil {
		t.Fatalf("执行失败 %q: %v", q, err)
	}
}

// rebaseInt64 读单个整数值（列级等值锁用，SQL 报错即判红而不是当 0 处理）。
func rebaseInt64(t *testing.T, s *Store, q string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(s.db, db.CurrentDialect(), q, args...).Scan(&n); err != nil {
		t.Fatalf("查询失败 %q: %v", q, err)
	}
	return n
}

func cfgInt(t *testing.T, s *Store, key string) int64 {
	t.Helper()
	v, err := s.GetConfig(key)
	if err != nil {
		t.Fatalf("读配置 %s 失败: %v", key, err)
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if perr != nil {
		t.Fatalf("配置 %s 非整数: %q", key, v)
	}
	return n
}

// TestPointsRateRebase_LegacyBalancesKeepPointFace ①：额度桶逐列等值（积分面值零位移）。
func TestPointsRateRebase_LegacyBalancesKeepPointFace(t *testing.T) {
	s, freshSeeds := rebaseEnv(t)

	// 补发前：按旧汇率读积分面值（这就是客户看到的数）
	before := map[int64]int64{
		1: legacyPointsAt(300, 90000),
		2: legacyPointsAt(300, 100000),
	}
	s.PointsRateRebase()

	// 汇率与尺子翻到新档
	if got := cfgInt(t, s, "points_tokens_rate"); got != 400 {
		t.Fatalf("汇率应重锚为 400，got %d", got)
	}
	if got := cfgInt(t, s, "price_fen_per_million_tokens"); got != 24917 {
		t.Fatalf("尺子应反向缩到 24917（33222×3/4 四舍五入），got %d", got)
	}

	// 逐租户：新 token ÷ 新汇率 == 旧 token ÷ 旧汇率（等值补发的定义）
	for tid, wantPts := range before {
		got := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=?", tid)
		if pts := s.PointsFromTokens(got); pts != wantPts {
			t.Fatalf("租户 %d 余额补发后积分位移：应 %d 积分，got %d（token %d）", tid, wantPts, pts, got)
		}
	}
	// 精确值（防「等值」被宽松除法糊过去）
	if got := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=1"); got != 120000 {
		t.Fatalf("90,000 应等值补发为 120,000，got %d", got)
	}
	if got := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=2"); got != 133333 {
		t.Fatalf("100,000 应等值补发为 133,333（四舍五入），got %d", got)
	}
	// 台账 total 与 left 同时缩放（只缩一头会让「剩余占比」失真）
	if got := rebaseInt64(t, s, `SELECT "left" FROM quota_grants WHERE tenant_id=1`); got != 40000 {
		t.Fatalf("trial 台账 left 应 30,000→40,000，got %d", got)
	}
	if got := rebaseInt64(t, s, "SELECT total FROM quota_grants WHERE tenant_id=2"); got != 1200000 {
		t.Fatalf("plan 台账 total 应 900,000→1,200,000，got %d", got)
	}
	// 订单/流水：含 pending 与 paid（ComputeUpgradeCredit 的分母，必须与台账同标）
	if got := rebaseInt64(t, s, "SELECT amount_tokens FROM orders WHERE order_no='RO_LEGACY_1'"); got != 1200000 {
		t.Fatalf("3,000 积分充值单应 900,000→1,200,000，got %d", got)
	}
	if got := rebaseInt64(t, s, "SELECT amount_tokens FROM orders WHERE order_no='RO_LEGACY_2'"); got != 40000 {
		t.Fatalf("待付单也要补发（否则稍后结算按新汇率少给 1/4），got %d", got)
	}
	if got := rebaseInt64(t, s, "SELECT amount_tokens FROM payments WHERE order_id=1"); got != 1200000 {
		t.Fatalf("支付流水应与订单同标，got %d", got)
	}
	// 任务定义与组织上限
	if got := rebaseInt64(t, s, "SELECT reward_tokens FROM user_tasks WHERE title='F78旧档任务'"); got != 120000 {
		t.Fatalf("任务奖励应 90,000→120,000，got %d", got)
	}
	// ★ 老库补发后必须落到「全新库出厂档」：逐条出厂任务与 New() 种子对照（老库新库不许两套数）
	for title, seed := range freshSeeds {
		assertRebasedTo(t, s, seed, "出厂任务「"+title+"」补发后应等于新库种子",
			"SELECT reward_tokens FROM user_tasks WHERE title=?", title)
	}
	if got := rebaseInt64(t, s, "SELECT token_limit FROM orgs WHERE name='F78总部'"); got != 80000 {
		t.Fatalf("组织上限应 60,000→80,000，got %d", got)
	}

	// ★ 射程外（成本事实）：真实消耗零位移
	if got := rebaseInt64(t, s, "SELECT cost FROM usage_ledger WHERE id=1"); got != 90000 {
		t.Fatalf("usage_ledger 是成本事实，不许缩放，got %d", got)
	}
	// ★ 射程外（token 单位阈值）：低额预警 / KB 单条约额 / 句折算率 一律不动
	for k, want := range map[string]int64{
		"low_balance_alert_tokens":          100000,
		"kb_upload_reward_tokens_per_entry": 200,
		"estimate_tokens_per_sentence":      500,
	} {
		if got := cfgInt(t, s, k); got != want {
			t.Fatalf("token 单位阈值 %s 不该随积分汇率缩放：应 %d，got %d", k, want, got)
		}
	}
}

// TestPointsRateRebase_PointsMintedConfigMatchesFreshSeed ②：配置键走「回读积分面值再重铸」，
// 结果必须与全新库的出厂种子逐字节相等（否则新老库两套数）。
func TestPointsRateRebase_PointsMintedConfigMatchesFreshSeed(t *testing.T) {
	s, _ := rebaseEnv(t)
	s.PointsRateRebase()

	for k, want := range map[string]int64{
		"free_trial_tokens":          400000, // 1,000 积分
		"invite_reward_tokens":       400000, // 1,000 积分
		"inviter_paid_reward_tokens": 666800, // 1,667 积分（=历史内置 50 万的积分档）
	} {
		if got := cfgInt(t, s, k); got != want {
			t.Fatalf("配置键 %s 重锚应 %d（积分面值×400），got %d", k, want, got)
		}
		// 回读积分面值必须仍是出厂档（等值补发的定义：对外报价一字未动）
		if back := s.PointsFromTokens(cfgInt(t, s, k)); back <= 0 {
			t.Fatalf("配置键 %s 回读积分为 0，值异常: %d", k, cfgInt(t, s, k))
		}
	}
	// 面值口径：体验额度回读仍是 1,000 积分（客户视角没变多也没变少）
	if pts := s.PointsFromTokens(cfgInt(t, s, "free_trial_tokens")); pts != DefaultFreeTrialPoints {
		t.Fatalf("体验额度回读应 1,000 积分，got %d", pts)
	}
}

// TestPointsRateRebase_MoneyInvariant ②钱：token ×4/3 与尺子 ×3/4 精确抵消。
// 3,000 积分裸充值补发前后都折 ¥299.00；升级折算比值不因逐行取整而漂移。
func TestPointsRateRebase_MoneyInvariant(t *testing.T) {
	s, _ := rebaseEnv(t)
	if fen := s.TokensToFen(900000); fen != 29900 { // 旧档：90 万 token × 33222
		t.Fatalf("补发前 3,000 积分应收应 29900 分，got %d", fen)
	}
	s.PointsRateRebase()
	if fen := s.TokensToFen(1200000); fen != 29900 { // 新档：120 万 token × 24917
		t.Fatalf("补发后 3,000 积分应收仍应 29900 分（面值不动＝降价只体现在积分单价），got %d", fen)
	}
	// 出厂两旋钮必须自洽（不依赖库里旧值）：3,000 积分 → ¥299.00
	if fen := s.TokensToFen(s.TokensFromPoints(3000)); fen != 29900 {
		t.Fatalf("出厂口径 3,000 积分应折 29900 分，got %d", fen)
	}
	// ComputeUpgradeCredit 比值（剩余/订单量）：两侧同标 → 只允许逐行取整级别的漂移
	ratioBefore := 900000.0 / 900000.0 // 该订单台账满额（补发前 900,000/900,000）
	ratioAfter := float64(rebaseInt64(t, s, `SELECT "left" FROM quota_grants WHERE tenant_id=2`)) /
		float64(rebaseInt64(t, s, "SELECT amount_tokens FROM orders WHERE order_no='RO_LEGACY_1'"))
	if d := ratioAfter - ratioBefore; d > 0.001 || d < -0.001 {
		t.Fatalf("升级折算比值漂移 %.6f（应仅逐行取整级别，>0.001 说明有一头没缩）", d)
	}
}

// TestPointsRateRebase_DailyTokenLimitGuard ③：日限额 token 腿按「是否恰等于字符值」逐租户判定。
func TestPointsRateRebase_DailyTokenLimitGuard(t *testing.T) {
	s, _ := rebaseEnv(t)
	s.PointsRateRebase()
	// 字符派生值（20000/20000）：动了就等于把字符墙抬 1/3
	if p, _ := s.GetTenantPerms(1); p.MaxDailyTokens != 20000 || p.MaxDailyChars != 20000 {
		t.Fatalf("字符派生日限额不许缩放，got chars=%d tokens=%d", p.MaxDailyChars, p.MaxDailyTokens)
	}
	// 按积分钉的墙（1000 积分档）：必须跟涨到 400,000，否则客户日额度凭白缩 25%
	if p, _ := s.GetTenantPerms(2); p.MaxDailyTokens != 400000 || p.MaxDailyChars != 100000 {
		t.Fatalf("积分日限额应 300,000→400,000（字符腿不动），got chars=%d tokens=%d", p.MaxDailyChars, p.MaxDailyTokens)
	}
	// 其余权限键必须原样保留（JSON 合并补丁语义）
	if p, _ := s.GetTenantPerms(2); p.MaxDailyChars != 100000 {
		t.Fatalf("permissions 其余键被覆盖，got %+v", p)
	}
}

// TestPointsRateRebase_OneShotAndFreshLibrarySkip ③：一次性语义 + 新库不触发。
func TestPointsRateRebase_OneShotAndFreshLibrarySkip(t *testing.T) {
	s, _ := rebaseEnv(t)

	// 新库形状：汇率已是 400 → 直接跳过且不占凭证（rebaseEnv 里旧档被回滚成 400）
	if err := s.SetConfig("points_tokens_rate", "400"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	s.PointsRateRebase()
	if got := rebaseInt64(t, s, "SELECT COUNT(*) FROM system_config WHERE key=?", ConfigPointsRateRebase); got != 0 {
		t.Fatalf("新库不该占补发凭证，got %d 行", got)
	}
	if got := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=1"); got != 90000 {
		t.Fatalf("新库跳过时余额零位移，got %d", got)
	}

	// 老库形状：跑一次即落凭证并补发
	if err := s.SetConfig("points_tokens_rate", "300"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	s.PointsRateRebase()
	bal := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=1")
	if bal != 120000 {
		t.Fatalf("老库应补发到 120,000，got %d", bal)
	}

	// 第二次调用：幂等（凭证已存在 → 抢不到 → 整段放弃）
	s.PointsRateRebase()
	if got := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=1"); got != bal {
		t.Fatalf("重复执行不得二次缩放，got %d（应 %d）", got, bal)
	}

	// ★ 防超发：运维把汇率手工改回 300 也不能再补发一遍（凭证拦住）
	if err := s.SetConfig("points_tokens_rate", "300"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	s.PointsRateRebase()
	if got := rebaseInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=1"); got != bal {
		t.Fatalf("凭证存在时禁止二次补发，got %d（应仍为 %d）", got, bal)
	}

	// 摘要可读（运维核对用）
	raw, _ := s.GetConfig(ConfigPointsRateRebase)
	var summary map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("补发摘要非 JSON（无法事后对账）: %q", raw)
	}
	for _, k := range []string{"from_rate", "to_rate", "scaled_rows", "ruler_fen_from", "ruler_fen_to", "balances", "done_at"} {
		if _, ok := summary[k]; !ok {
			t.Fatalf("补发摘要缺字段 %s，got %q", k, raw)
		}
	}
}

// TestOpsDefaultsLockPointsRate 跨包联动锁：ops 里的字面出厂档必须等于 store 常量乘积。
// ops 不得 import store（AGENTS §一·3），一旦有人只改一头，本测试即红灯。
func TestOpsDefaultsLockPointsRate(t *testing.T) {
	if got, want := ops.DefaultEffective().Package.TrialTokens, DefaultFreeTrialPoints*DefaultPointsTokensRate; got != want {
		t.Fatalf("ops 体验额度出厂档 %d 与 store 口径 %d（%d 积分×%d）脱节", got, want, DefaultFreeTrialPoints, DefaultPointsTokensRate)
	}
	if got, want := ops.DefaultEffective().Invite.RewardTokens, DefaultInviteRewardPoints*DefaultPointsTokensRate; got != want {
		t.Fatalf("ops 邀请奖励出厂档 %d 与 store 口径 %d 脱节", got, want)
	}
	// ops 的 token 单位阈值（日限额）与积分汇率无关，必须仍是 20000 档
	if got := ops.DefaultEffective().Limits.DefaultMaxDailyTokens; got != 20000 {
		t.Fatalf("ops 默认日 token 限额应不随积分汇率改动，got %d", got)
	}
}

// legacyPointsAt 按指定汇率把内部 token 折回积分（测试侧独立实现，不复用 Store 方法，
// 否则「换算函数本身写错」会被同一函数自证通过）。
func legacyPointsAt(rate, tokens int64) int64 {
	if tokens <= 0 {
		return 0
	}
	return (tokens + rate/2) / rate
}
