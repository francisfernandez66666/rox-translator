// ============================================================================
// ★ 2026-10-24 云端 UAT 修复批 DEF-02（方案 §10.1.2）：tickets.repair_attempts 列的落库回归。
//
// 方案点名的是**列**，不是载荷字段。本批对账前实现把「文本工单整单重发次数」写在
// final_result 那段 JSON 里，行为上一样限得住 3 次，坏在两处：
//
//	① 运维问「哪些单正在被自动重发」只能整段拉 blob 再解析，一句 `WHERE repair_attempts>=3` 的事；
//	② 计数与译文挤同一段 blob ⇒ 任何一次「整段重写载荷」（每步 savePayload 都是覆盖写，
//	   人工重跑/驳回重翻/补译都走它）都会顺手把它抹回 0 ⇒ 上限复位、坏单无限重发、每轮真扣钱。
//
// 本组用例守四件事：
//
//	① 老库形态（列不存在）启动即自动补列，且**存量行**拿到 0 而不是 NULL；
//	② 补列幂等：连跑两次不报错、不重复加列、计数不被重置；
//	③ 补列真的挂在 store.New 的启动链上（关连接重开一次，才算"老库升级"真发生）；
//	④ 读/增两条腿的往返值正确，并发各加一格一格都不吞（文件库夹具，见那条注释）；
//	⑤ 工单不存在的 id 必须**报错上抛**，不许静默回 0——
//	   回 0 会被调用方读成「这单还没重发过」，于是照样放行重发（等于没有上限）。
//
// 方言：本文件钉 SQLite（AGENTS §一·4 新增单测自钉方言），PG 覆盖由 run_uat 矩阵承担。
// ============================================================================
package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/db"

	_ "modernc.org/sqlite"
)

// raOld 恢复 config.C 全局单例（config.Default() 有写全局的副作用）。
func raOld(t *testing.T) func() {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	return func() { config.C = old }
}

// raColumnExists 问 tickets 表里到底有没有这一列（直接 SELECT 它，缺列即报错——
// 刻意不用 pragma：那条腿只在 SQLite 成立，PG 侧要换 information_schema，两种方言各一份判据迟早打架）。
func raColumnExists(t *testing.T, s *Store) bool {
	t.Helper()
	var n int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT repair_attempts FROM tickets ORDER BY id LIMIT 1").Scan(&n)
	if err == nil {
		return true
	}
	// 空表时 SELECT 不报错但扫不到行（ErrNoRows）——这恰恰证明列存在，需要单独判。
	if err.Error() == "sql: no rows in result set" {
		return true
	}
	return false
}

// raFileStore 建**文件库** Store（并发用例专用，见 TestTicketRepairAttemptsConcurrentEachGetsOneCell 注释）。
// 参数：t。返回：可用的 Store；busy_timeout 让并发 UPDATE 排队而不是当场 SQLITE_BUSY。
func raFileStore(t *testing.T) *Store {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "ra.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开文件库失败: %v", err)
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
	if _, err := conn.Exec(`INSERT INTO tenants (code, name, status, expires_at, permissions, is_personal) VALUES ('test','测试租户','active','','{}',1)`); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	s, err := New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return s
}

// TestTicketRepairAttemptsLegacyColumnAdded 老库（列已被摘掉）启动补列，存量行取 0。
func TestTicketRepairAttemptsLegacyColumnAdded(t *testing.T) {
	restore := raOld(t)
	t.Cleanup(restore)
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "重发计数老库补列", "第一段。\n第二段。", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	// 造「老库」：把列摘掉，模拟 2026-10-24 之前的那张表（存量行必须留着，否则测不到默认值那一档）
	if _, err := db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE tickets DROP COLUMN repair_attempts"); err != nil {
		t.Fatalf("夹具摘列失败（老库形态造不出来，下面的补列断言会空转）: %v", err)
	}
	if raColumnExists(t, s) {
		t.Fatal("夹具摘列后列还在 ⇒ 后面的补列断言恒真")
	}
	s.TicketRepairAttemptsMigrate()
	if !raColumnExists(t, s) {
		t.Fatal("补列没生效 ⇒ 老库升级后 repair_attempts 依旧不存在，重发计数无处可落")
	}
	n, err := s.TicketRepairAttempts(tk.ID)
	if err != nil {
		t.Fatalf("补列后读计数失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("存量行的 repair_attempts 必须默认 0（NOT NULL DEFAULT 0），实得 %d", n)
	}
	// 幂等：再跑一次不许报错、不许把已有的 1 格重置
	if _, err = s.IncTicketRepairAttempts(tk.ID); err != nil {
		t.Fatalf("IncTicketRepairAttempts: %v", err)
	}
	s.TicketRepairAttemptsMigrate()
	if got, _ := s.TicketRepairAttempts(tk.ID); got != 1 {
		t.Fatalf("重复补列把计数抹回 %d（幂等迁移只许加列，不许动数据）", got)
	}
}

// TestTicketRepairAttemptsWiredIntoNew 守卫级：补列真的挂在 store.New 的启动链上。
// 为什么单开一条——上面那条只**直调** `TicketRepairAttemptsMigrate()`，
// 把 store.go 里那一行摘掉它照样绿（AGENTS §三「判据级断言代替不了守卫级」），
// 而现网表现是「代码合了、老库永远没这列、重发计数一写就炸」。
// 形态取自真实升级路径：老库文件（列已被摘）→ 进程重启 → 列自己回来、存量行仍是 0。
func TestTicketRepairAttemptsWiredIntoNew(t *testing.T) {
	restore := raOld(t)
	t.Cleanup(restore)
	path := filepath.Join(t.TempDir(), "app.db") + "?_pragma=busy_timeout(10000)"
	s := raOpenStore(t, path)
	tk, err := s.CreateTicket(1, 1, "重启补列", "第一段。\n第二段。", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if _, err := s.IncTicketRepairAttempts(tk.ID); err != nil {
		t.Fatalf("首次加格失败: %v", err)
	}
	// 造老库：摘列（保留那行工单），然后**关连接再开**——只有 New 的启动链会补回来
	if _, err := db.Exec(s.DB(), db.CurrentDialect(), "ALTER TABLE tickets DROP COLUMN repair_attempts"); err != nil {
		t.Fatalf("夹具摘列失败: %v", err)
	}
	if err := s.DB().Close(); err != nil {
		t.Fatalf("关闭老库失败: %v", err)
	}
	s2 := raOpenStore(t, path)
	if !raColumnExists(t, s2) {
		t.Fatal("重启后 tickets 仍无 repair_attempts ⇒ store.New 没挂补列，老库升级缺列")
	}
	n, err := s2.TicketRepairAttempts(tk.ID)
	if err != nil {
		t.Fatalf("重启后读计数失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("补列后存量行应回默认 0，实得 %d", n)
	}
}

// raOpenStore 按给定 DSN 打开/建库并跑完整 store.New（含 tenants 前置表，同 raFileStore）。
// 参数：t、dsn。返回：可用的 Store（连接由 t.Cleanup 关闭；调用方自行 Close 亦可）。
func raOpenStore(t *testing.T, dsn string) *Store {
	t.Helper()
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开 %s 失败: %v", dsn, err)
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
	// tenants 只在**首启**灌测试租户；第二次打开同一份文件时该行已在，撞 UNIQUE 会让夹具自己判红
	var cnt int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM tenants WHERE code='test'`).Scan(&cnt); err != nil {
		t.Fatalf("数测试租户失败: %v", err)
	}
	if cnt == 0 {
		if _, err := conn.Exec(`INSERT INTO tenants (code, name, status, expires_at, permissions, is_personal) VALUES ('test','测试租户','active','','{}',1)`); err != nil {
			t.Fatalf("插入测试租户失败: %v", err)
		}
	}
	s, err := New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return s
}

// TestTicketRepairAttemptsIncrementsAndMissingRow 逐次加一格；不存在的工单必须报错上抛。
func TestTicketRepairAttemptsIncrementsAndMissingRow(t *testing.T) {
	restore := raOld(t)
	t.Cleanup(restore)
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "重发计数逐格", "第一段。\n第二段。", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	for want := 1; want <= 3; want++ {
		got, err := s.IncTicketRepairAttempts(tk.ID)
		if err != nil {
			t.Fatalf("第 %d 次加格失败: %v", want, err)
		}
		if got != want {
			t.Fatalf("第 %d 次加格应回 %d（回读值就是调用方下一步比的那个数），实得 %d", want, want, got)
		}
	}
	if _, err := s.TicketRepairAttempts(99999999); err == nil {
		t.Fatal("读不存在的工单回 nil 错误 ⇒ 调用方把它当「还没重发过」，上限形同虚设")
	}
	if _, err := s.IncTicketRepairAttempts(99999999); err == nil {
		t.Fatal("给不存在的工单加格不回错误 ⇒ 0 行影响被写成成功（AGENTS §一·8②）")
	}
}

// TestTicketRepairAttemptsConcurrentEachGetsOneCell 并发各加一格，一格都不许吞。
// 取向：坏单被两个 worker 同时判结构失败时，两边都必须各自记下自己那一格；
// 若计数被吞（读-改-写非原子），上限就会软成「实际发出去 N 格只记 1」。
//
// ⚠️ 夹具口径（AGENTS §三 那条「并发判据必须绕开连接池」的同族坑）：
// 这里**不能**用 newTestStoreWithTenants 的 `:memory:`——内存库对**每一个连接**都是一份新库，
// 第二条连接拿到的是一张空表（首跑实读 `no such table: tickets`），测的是连接池不是并发。
// 换成 t.TempDir() 下的**文件库**并带 busy_timeout＋WAL，多连接看同一份数据，
// 而 `repair_attempts=repair_attempts+1` 是**服务端算术**（不是 Go 侧读-改-写），
// 所以并发各加一格必须真的各落一格。
func TestTicketRepairAttemptsConcurrentEachGetsOneCell(t *testing.T) {
	restore := raOld(t)
	t.Cleanup(restore)
	s := raFileStore(t)
	tk, err := s.CreateTicket(1, 1, "重发计数并发", "第一段。\n第二段。", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	const n = 6
	var wg sync.WaitGroup
	vals := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vals[i], errs[i] = s.IncTicketRepairAttempts(tk.ID)
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("并发加格第 %d 次失败: %v", i, errs[i])
		}
	}
	final, err := s.TicketRepairAttempts(tk.ID)
	if err != nil {
		t.Fatalf("终值读取失败: %v", err)
	}
	if final != n {
		t.Fatalf("并发 %d 次加格后终值应=%d，实得 %d（有格子被吞 ⇒ 上限比实际重发次数软）", n, n, final)
	}
	// ★ 判据口径（2026-10-25 -race 首跑纠正，属"实现对、判据错"）：
	// 这里**不能**要求 n 次返回值互不相同。`repair_attempts=repair_attempts+1` 是原子的
	// （这正是唯一要守的不变量：一格都不许吞），但紧跟的那次回读与别人的 UPDATE 之间没有锁，
	// 两个 worker 完全可能都读回同一个中间值——回读值只是"这一次发出去了"的日志读数，
	// 上限判定走的是**下一次的现读**（Workflow.StructureRetryNeeded 每次重新 SELECT）。
	// 所以这一腿只问两件事：每个返回值都落在 1..n 之内（没有凭空更大的数＝把别人那一格也认领了），
	// 且**至少有一枪读到 n**（终值被真实看见，说明没有格子在算术层丢失）。
	maxSeen := 0
	for _, v := range vals {
		if v < 1 || v > n {
			t.Fatalf("加格返回值 %d 越界（合法区间 1..%d）⇒ 回读值不是这一格", v, n)
		}
		if v > maxSeen {
			maxSeen = v
		}
	}
	if maxSeen != n {
		t.Fatalf("并发 %d 枪里最大回读值只到 %d ⇒ 有格子在算术层丢了", n, maxSeen)
	}
}
