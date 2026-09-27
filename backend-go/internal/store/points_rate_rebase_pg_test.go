// ============ points_rate_rebase_pg_test.go · 职责说明 ============
// ★ F-78（2026-09-28 〇-X）积分汇率 1:300→1:400「存量等值补发」的 **PostgreSQL 真库** 回归锁。
//
// 为什么 SQLite 那一份（points_rate_rebase_test.go）不够、必须再来这一份：
// 补发是一次整库 UPDATE 的迁移，而生产是 PG、本地快跑与单测默认是 SQLite。
// 这段 SQL 里有四处「SQLite 通过、PG 可能坏」的历史高频踩坑点，逐处都是本用例的射程：
//
//	① 整数除法取整语义：(col*? + ?)/? 在 PG 若被推成 numeric 就走浮点除法，
//	   与 SQLite 的整数向零截断不同 ⇒ 每行都可能差 1 token，累积成对账不平；
//	② 保留字 "left"（quota_grants.left）在 PG 必须带引号，SQLite 裸写也认；
//	③ INSERT OR IGNORE 抢「补发凭证」——PG 由 internal/db 改写成 ON CONFLICT DO NOTHING，
//	   RowsAffected 的 0/1 语义若翻译不到位，多实例下就会二次放大（超发＝资损）；
//	④ 租户日限额走 JSON 合并补丁（db.JSONPatchSet），正是 A2/S3 那批「PG 下整条 UPDATE 报错、
//	   每日任务静默失败」的同型风险面。
//
// 而这条链路在 run_uat 的 PG 主矩阵里**根本不会被触发**：UAT 每次重建全新库，
// 出厂汇率即 400 ⇒ 前置判定直接跳过。所以「PG 上的补发」只有本用例这一条腿。
//
// 隔离做法：在本地 PG 上另建一次性库 f78_rebase_pg（整库级隔离，绝不碰 pgtest/translator_uat/生产），
// 跑完 DROP；本机无 PG、无建库权限或缺扩展时 **Skip 而不是判红**（与 f44_pg_test.go 同一口径，
// 环境缺失不是回归）。PG 方言覆盖的发布闸门侧仍由 run_uat.sh 主矩阵承担。
//
// 运行：env PG_ADMIN_DSN='postgres://$USER@127.0.0.1:5432/postgres?sslmode=disable' \
//
//	go test -count=1 ./internal/store/ -run TestPointsRateRebaseOnPG
//
// =============================================
package store_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "github.com/lib/pq"
)

// pgProbeDB 一次性探测库名（整库隔离，跑完即删）。
const pgProbeDB = "f78_rebase_pg"

// pgAdminDSN 管理库连接串（只为建/删探测库，不承载业务 SQL）。
func pgAdminDSN() string {
	if v := os.Getenv("PG_ADMIN_DSN"); v != "" {
		return v
	}
	return "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/postgres?sslmode=disable"
}

// pgProbeDSN 把管理串里的 /postgres 库名换成探测库名。
func pgProbeDSN() string { return strings.Replace(pgAdminDSN(), "/postgres?", "/"+pgProbeDB+"?", 1) }

// openRebasePG 建一次性 PG 库并返回跑完整迁移链后的 Store
// （此刻是「全新库」形态：汇率即出厂 400，补发未触发也未占凭证）。环境不具备时 Skip。
func openRebasePG(t *testing.T) *store.Store {
	t.Helper()
	admin, err := sql.Open("postgres", pgAdminDSN())
	if err != nil {
		t.Skipf("本机 PG 不可连（%v），跳过 F-78 的 PG 方言腿", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	// FORCE：残留连接不阻塞重建（本机 PG 15.x 支持）
	if _, err := admin.Exec("DROP DATABASE IF EXISTS " + pgProbeDB + " WITH (FORCE)"); err != nil {
		t.Skipf("PG 建库权限/语句不可用（%v），跳过 F-78 的 PG 方言腿", err)
	}
	if _, err := admin.Exec("CREATE DATABASE " + pgProbeDB); err != nil {
		t.Skipf("PG 建探测库失败（%v），跳过", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec("DROP DATABASE IF EXISTS " + pgProbeDB + " WITH (FORCE)") // 清库失败只影响下次复用
	})

	// 方言显式钉成 postgres 并在结束时恢复：config.C 是全局的，不恢复会把 PG 方言
	// 漏给同包后续内存 SQLite 用例（AGENTS.md §一·4 记的两次整包假红）。
	prevC := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	config.C = cfg
	t.Cleanup(func() { config.C = prevC })

	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: pgProbeDSN()})
	if err != nil {
		t.Fatalf("打开探测库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	for _, ext := range []string{"vector", "pg_trgm"} { // 迁移链依赖的扩展：缺了是环境问题
		if _, e := conn.Exec("CREATE EXTENSION IF NOT EXISTS " + ext); e != nil {
			t.Skipf("探测库缺扩展 %s（%v），跳过 F-78 的 PG 方言腿", ext, e)
		}
	}
	if _, err := tenant.NewStore(conn); err != nil { // tenants 由 tenant 包建表（口径同生产启动链）
		t.Fatalf("tenant.NewStore(PG) 失败: %v", err)
	}
	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	return s
}

// pgExec / pgInt64 PG 侧方言化读写助手（与 SQLite 那份同名助手同语义：SQL 报错即判红，不当 0 处理）。
func pgExec(t *testing.T, s *store.Store, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(s.DB(), db.CurrentDialect(), q, args...); err != nil {
		t.Fatalf("执行失败 %q: %v", q, err)
	}
}

func pgInt64(t *testing.T, s *store.Store, q string, args ...interface{}) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(s.DB(), db.CurrentDialect(), q, args...).Scan(&n); err != nil {
		t.Fatalf("查询失败 %q: %v", q, err)
	}
	return n
}

// seedLegacyOnPG 把全新库「降回 1:300 时代的生产形状」：汇率/尺子/三枚积分面值键回旧档、
// 出厂任务奖励按 ×3/4 降档，再灌入旧口径的余额/台账/订单/流水/任务/组织/日限额与一条真实消耗。
// 归属 ID 显式写死（901/902/9001…）且不靠序列自增顺序，两方言才能共用同一组期望值。
// 返回「全新库出厂任务档」，供「老库补发后 == 新库种子」对照。
func seedLegacyOnPG(t *testing.T, s *store.Store) map[string]int64 {
	t.Helper()
	for _, kv := range [][2]string{
		{"points_tokens_rate", "300"},
		{"price_fen_per_million_tokens", "33222"},
		{"free_trial_tokens", "300000"},
		{"invite_reward_tokens", "300000"},
		{"inviter_paid_reward_tokens", "500000"},
		{"low_balance_alert_tokens", "100000"},       // token 单位阈值：不许缩放
		{"kb_upload_reward_tokens_per_entry", "200"}, // 同上
		{"estimate_tokens_per_sentence", "500"},      // 句↔token 口径：同上
	} {
		if err := s.SetConfig(kv[0], kv[1]); err != nil {
			t.Fatalf("写配置 %s 失败: %v", kv[0], err)
		}
	}
	pgExec(t, s, "DELETE FROM system_config WHERE key=?", store.ConfigPointsRateRebase)

	fresh := map[string]int64{}
	rows, err := db.Query(s.DB(), db.CurrentDialect(), "SELECT title, reward_tokens FROM user_tasks")
	if err != nil {
		t.Fatalf("读出厂任务失败: %v", err)
	}
	for rows.Next() {
		var title string
		var v int64
		if rows.Scan(&title, &v) == nil {
			fresh[title] = v
		}
	}
	_ = rows.Close()
	// 出厂任务降回旧档算术（否则测的是「新档行被再缩一次」的假象）
	pgExec(t, s, `UPDATE user_tasks SET reward_tokens = (reward_tokens*? + ?) / ?`, 3, 2, 4)

	pgExec(t, s, `INSERT INTO tenants (id, code, name, permissions) VALUES
		(901,'tchar','字符墙租户','{"max_daily_chars":20000,"max_daily_tokens":20000}'),
		(902,'tpoint','积分墙租户','{"max_daily_chars":100000,"max_daily_tokens":300000}')`)
	pgExec(t, s, `INSERT INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES
		(901, 90000, 'tokens', ''), (902, 100000, 'tokens', '')`)
	pgExec(t, s, `INSERT INTO quota_grants (id, tenant_id, kind, total, "left", expires_at, source, ref_id, created_at) VALUES
		(9001,901,'trial',90000,30000,'2099-01-01T00:00:00Z','register',0,''),
		(9002,902,'plan',900000,900000,'2099-01-01T00:00:00Z','order',1,'')`)
	pgExec(t, s, `INSERT INTO orders (id, tenant_id, order_no, amount_tokens, amount_money, status, created_at) VALUES
		(9001,901,'RO_PG_1',900000,299,'paid','2026-01-01T00:00:00Z'),
		(9002,902,'RO_PG_2',30000,10.03,'pending','2026-01-01T00:00:00Z')`)
	pgExec(t, s, `INSERT INTO payments (id, order_id, tenant_id, amount_tokens, amount_fen, status, created_at) VALUES
		(9001,9001,901,900000,29900,'paid','2026-01-01T00:00:00Z')`)
	pgExec(t, s, `INSERT INTO user_tasks (task_type, title, reward_tokens, enabled, sort_order, created_at, updated_at)
		VALUES ('daily','F78PG旧档任务',90000,1,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	pgExec(t, s, `INSERT INTO orgs (tenant_id, parent_id, name, type, token_limit)
		VALUES (901,0,'F78PG总部','org',60000)`)
	pgExec(t, s, `INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at)
		VALUES (901,1,'translate','p','m',1000,90,90000,'text','pro','charge','2026-01-01T00:00:00Z')`)
	return fresh
}

// TestPointsRateRebaseOnPG 在真实 PG 上跑一遍补发，期望值与 SQLite 那份**逐字相同**——
// 「两方言同值」就是这个用例的全部结论：任何一处方言分叉都会体现在数字上，而不是被悄悄 skip 掉。
func TestPointsRateRebaseOnPG(t *testing.T) {
	s := openRebasePG(t)
	fresh := seedLegacyOnPG(t, s)

	s.PointsRateRebase() // ← 本用例主体：PG 方言下的整段补发

	// 两旋钮翻档
	if got := pgInt64(t, s, `SELECT CAST(value AS BIGINT) FROM system_config WHERE key='points_tokens_rate'`); got != 400 {
		t.Fatalf("PG：汇率应重锚 400，got %d", got)
	}
	if got := pgInt64(t, s, `SELECT CAST(value AS BIGINT) FROM system_config WHERE key='price_fen_per_million_tokens'`); got != 24917 {
		t.Fatalf("PG：尺子应反向缩到 24917，got %d", got)
	}
	// 额度桶（含 "left" 保留字列与整数除法取整两处方言点）＋射程外的成本事实/阈值
	for q, want := range map[string]int64{
		"SELECT balance FROM balance_accounts WHERE tenant_id=901":                             120000,
		"SELECT balance FROM balance_accounts WHERE tenant_id=902":                             133333, // 100,000×4/3 四舍五入
		`SELECT "left" FROM quota_grants WHERE id=9001`:                                        40000,
		"SELECT total FROM quota_grants WHERE id=9002":                                         1200000,
		"SELECT amount_tokens FROM orders WHERE order_no='RO_PG_1'":                            1200000,
		"SELECT amount_tokens FROM orders WHERE order_no='RO_PG_2'":                            40000,
		"SELECT amount_tokens FROM payments WHERE id=9001":                                     1200000,
		"SELECT reward_tokens FROM user_tasks WHERE title='F78PG旧档任务'":                         120000,
		"SELECT token_limit FROM orgs WHERE name='F78PG总部'":                                    80000,
		"SELECT cost FROM usage_ledger WHERE tenant_id=901":                                    90000,  // 成本事实：零位移
		"SELECT CAST(value AS BIGINT) FROM system_config WHERE key='low_balance_alert_tokens'": 100000, // token 阈值：零位移
	} {
		if got := pgInt64(t, s, q); got != want {
			t.Fatalf("PG 补发读数不符：%s 应 %d，got %d", q, want, got)
		}
	}
	// 积分面值零位移（客户视角）
	if pts := s.PointsFromTokens(pgInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=902")); pts != 333 {
		t.Fatalf("PG：100,000@300＝333 积分，补发后回读应仍 333，got %d", pts)
	}
	// 三枚积分面值键必须与全新库种子逐字节一致（老库新库不许两套数）
	for k, want := range map[string]int64{"free_trial_tokens": 400000, "invite_reward_tokens": 400000, "inviter_paid_reward_tokens": 666800} {
		if got := pgInt64(t, s, "SELECT CAST(value AS BIGINT) FROM system_config WHERE key=?", k); got != want {
			t.Fatalf("PG：配置键 %s 应重铸为 %d，got %d", k, want, got)
		}
	}
	// 出厂任务：补发后回到新库种子
	for title, seed := range fresh {
		if got := pgInt64(t, s, "SELECT reward_tokens FROM user_tasks WHERE title=?", title); got != seed {
			t.Fatalf("PG：出厂任务「%s」补发后应等于新库种子 %d，got %d", title, seed, got)
		}
	}
	// 钱口径：3,000 积分补发前后都折 ¥299.00（尺子×3/4 与 token×4/3 抵消）
	if fen := s.TokensToFen(1200000); fen != 29900 {
		t.Fatalf("PG：1,200,000 token × 24917 应折 29900 分，got %d", fen)
	}
	// 日限额两腿：字符派生值不动、积分档跟涨（db.JSONPatchSet 的 PG 路径）
	if p, err := s.GetTenantPerms(901); err != nil || p.MaxDailyTokens != 20000 || p.MaxDailyChars != 20000 {
		t.Fatalf("PG：字符派生日限额不许缩放，got chars=%d tokens=%d err=%v", p.MaxDailyChars, p.MaxDailyTokens, err)
	}
	if p, err := s.GetTenantPerms(902); err != nil || p.MaxDailyTokens != 400000 || p.MaxDailyChars != 100000 {
		t.Fatalf("PG：积分日限额应 300,000→400,000，got chars=%d tokens=%d err=%v", p.MaxDailyChars, p.MaxDailyTokens, err)
	}
	// 凭证：PG 下抢得动、且摘要落的是合法 JSON；再跑一遍零位移（一次性语义）
	raw, err := s.GetConfig(store.ConfigPointsRateRebase)
	if err != nil || raw == "" || raw == "claimed" {
		t.Fatalf("PG：补发凭证/摘要未落库，raw=%q err=%v", raw, err)
	}
	var summary map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		t.Fatalf("PG：补发摘要非 JSON: %q", raw)
	}
	bal := pgInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=901")
	s.PointsRateRebase()
	if got := pgInt64(t, s, "SELECT balance FROM balance_accounts WHERE tenant_id=901"); got != bal {
		t.Fatalf("PG：重复执行不得二次缩放，got %d（应 %d）", got, bal)
	}
	t.Logf("PG 补发摘要 scaled_rows=%v ruler=%v→%v", summary["scaled_rows"], summary["ruler_fen_from"], summary["ruler_fen_to"])
}
