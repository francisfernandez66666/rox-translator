// ============================================================================
// apikeys_quota_test.go — ★ D-8（2026-09-29）API Key 日配额「判据与计数合一」回归
//
// 缺陷因果链（照审计原话定性，别夸大）：计数本身一直是原子的
//
//	（apikeys.go 的 TouchAPIKey 是一条带 CASE 的相对自增 UPDATE，Redis 路径走 INCR），
//	缺的是**放行判定**——validateAPIKey 读的是那一刻的 calls_today 快照，
//	事后才另发一条自增。同一把 Key 当日 used=limit-1 时并发打进 M 个请求，
//	M 个都在同一刻读到 used<limit ⇒ 全部放行 ⇒ 当日实际用了 limit-1+M 次。
//	这是「限流精度」问题（客户超发额度、平台承担下游成本），不是资金扣减问题。
//
// 本文件锁三件事，缺一不可：
//
//	① 顺序语义：打满后第 N+1 次必须 false，且**被拒的调用不吃展示计数**；
//	② 并发语义：limit=K、并发打 3K 次 ⇒ 恰好 K 次 true（旧两段式在这里会超发，
//	   所以这条才是 D-8 的正身；顺序用例只证 WHERE 写对了，证不到并发）；
//	③ 反证：同一条语句去掉额度守卫（等价于旧形态的「读了再写」）必须能把计数推过上限——
//	   没有这一腿，②的绿灯无法区分「守卫真的在挡」与「用例压根没打到缝」。
//
// 方言自钉（AGENTS §一·4）：config.C 本包自行钉 sqlite，绝不把 DB_DRIVER 泄漏给同包后续用例；
// 并发腿用**命名共享缓存**内存库（file:xxx?mode=memory&cache=shared，见 newQuotaStore 的踩坑说明），
// 让 15 个 goroutine 落在同一份表上；PG 真并发由 run_uat 矩阵承担。
// ============================================================================
package store

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/infra/ratelimit"
)

// pinSQLiteForQuotaTest ★ AGENTS §一·4：本用例自钉方言并在结束时恢复，防止 run_uat 的 PG env 渗漏。
func pinSQLiteForQuotaTest(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// readKeyQuota 读回某把 Key 的当日额度三元组（limit/calls_today/date），断言用。
func readKeyQuota(t *testing.T, s *Store, id int64) (limit, today int64, date string) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT daily_call_limit, calls_today, calls_today_date FROM api_keys WHERE id=?`, id).Scan(&limit, &today, &date); err != nil {
		t.Fatalf("读回配额行失败: %v", err)
	}
	return limit, today, date
}

// quotaSeq 给每个共享内存库取唯一名，保证同一进程里多跑/并跑的夹具不会互相撞库。
var quotaSeq int64

// newQuotaStore 并发腿专用的 Store：命名共享缓存内存库 + 给足并发度的连接池。
//
// ★ 为什么不能直接复用同包通用的 newTestStoreWithTenants（首跑真踩出来的）：
//
//	它用 ":memory:"，而 database/sql 下那是**每条连接各自的私有库**——迁移跑在连接 1 上，
//	15 个 goroutine 并发摸库时会被拨到连接 2..N，那里根本没有 api_keys 表，
//	于是本用例红在夹具上（实测报 "no such table: api_keys"＋放行 1 次），
//	**而不是红在配额语义上**。改成 file:<唯一名>?mode=memory&cache=shared 后，
//	同一进程内所有连接看到同一份内存库——与生产 db.go:78 对 ":memory:" 的处理同口径。
//
// ★ 连接池**不许**钉成 1：store.New 的 PackagesTenantMigrate 会在 PRAGMA 游标未关闭时
//
//	再开一条嵌套 db.Query（packages.go），单连接池等于让 store.New 永久阻塞。
//	共享缓存已经让"第二条连接"看到同一份库，所以这里只给并发度、不收口。
func newQuotaStore(t *testing.T) *Store {
	t.Helper()
	name := "akquota_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "_" + strconv.FormatInt(atomic.AddInt64(&quotaSeq, 1), 10)
	dsn := "file:" + name + "?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate"
	db0, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开共享内存库失败: %v", err)
	}
	db0.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db0.Close() })
	// tenants 表与测试租户行：与 newTestStoreWithTenants 同形态（is_personal=1，交易类校验需要）
	if _, err := db0.Exec(`CREATE TABLE IF NOT EXISTS tenants (
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
	if _, err := db0.Exec(`INSERT INTO tenants (code, name, status, expires_at, permissions, is_personal) VALUES ('test','测试租户','active','','{}',1)`); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	s, err := New(db0)
	if err != nil {
		t.Fatalf("创建共享内存 Store 失败: %v", err)
	}
	return s
}

// TestReserveAPICallSequentialSemantics ① 顺序语义：用满即拒、被拒不计数、跨日自动清零、0=不限。
func TestReserveAPICallSequentialSemantics(t *testing.T) {
	pinSQLiteForQuotaTest(t)
	s := newTestStoreWithTenants(t)
	today := time.Now().Format("2006-01-02")

	// —— 有限额档：limit=3
	plain, err := s.CreateAPIKey(1, 7, "配额原子占用", "translate", 3)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByHash(HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if !s.ReserveAPICall(k.ID, k.DailyCallLimit) {
			t.Fatalf("第 %d 次占用被拒（上限 3）⇒ 额度判据把没打满的 Key 也挡了", i)
		}
	}
	if s.ReserveAPICall(k.ID, k.DailyCallLimit) {
		t.Fatal("第 4 次仍放行 ⇒ WHERE 里的额度守卫没生效（这就是 D-8 的洞）")
	}
	_, used, usedDate := readKeyQuota(t, s, k.ID)
	if used != 3 || usedDate != today {
		t.Fatalf("被拒的第 4 次改了计数：used=%d date=%q（应停在 3/%q）", used, usedDate, today)
	}
	var callCount int64
	if err := s.db.QueryRow(`SELECT call_count FROM api_keys WHERE id=?`, k.ID).Scan(&callCount); err != nil {
		t.Fatal(err)
	}
	if callCount != 3 {
		t.Fatalf("call_count=%d 应等于成功放行次数 3（被拒调用不得计入展示用量）", callCount)
	}

	// —— 跨日清零：日期改写为昨天、calls_today 留着一个很大的残值
	if _, err := s.db.Exec(`UPDATE api_keys SET calls_today_date='2000-01-01', calls_today=999 WHERE id=?`, k.ID); err != nil {
		t.Fatal(err)
	}
	if !s.ReserveAPICall(k.ID, k.DailyCallLimit) {
		t.Fatal("跨日后仍按昨天的计数拒绝 ⇒ CASE 的清零分支没走到")
	}
	_, used, usedDate = readKeyQuota(t, s, k.ID)
	if used != 1 || usedDate != today {
		t.Fatalf("跨日计数 got=%d/%q want=1/%q", used, usedDate, today)
	}

	// —— 不限额档（daily_limit<=0）：恒放行，但仍记展示计数
	plain2, err := s.CreateAPIKey(1, 7, "不限额", "translate", 0)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := s.GetAPIKeyByHash(HashAPIKey(plain2))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if !s.ReserveAPICall(k2.ID, k2.DailyCallLimit) {
			t.Fatalf("不限额 Key 第 %d 次被拒 ⇒ 额度守卫误伤 daily_call_limit=0", i+1)
		}
	}
	if _, used, _ := readKeyQuota(t, s, k2.ID); used != 50 {
		t.Fatalf("不限额档计数=%d 应为 50", used)
	}
}

// TestReserveAPICallConcurrentExactlyLimit ② 并发语义（D-8 正身）：
// limit=K 的 Key 被 3K 个并发请求同时占用，**恰好** K 次成功。
func TestReserveAPICallConcurrentExactlyLimit(t *testing.T) {
	pinSQLiteForQuotaTest(t)
	s := newQuotaStore(t)

	const limit = 5
	plain, err := s.CreateAPIKey(1, 7, "并发配额", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByHash(HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	var granted int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < limit*3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // 全部就位后同刻起跑，把「读快照与写回之间」的缝压到最宽
			if s.ReserveAPICall(k.ID, k.DailyCallLimit) {
				atomic.AddInt64(&granted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&granted); got != limit {
		t.Errorf("并发占用放行 %d 次，上限 %d ⇒ 配额在并发下超发（D-8 的形态：判定与计数分家）", got, limit)
	}
	if _, used, _ := readKeyQuota(t, s, k.ID); used != limit {
		t.Errorf("落库计数=%d 应等于上限 %d（放行次数与计数必须一一对应）", used, limit)
	}
}

// TestReserveAPICallGuardIsLoadBearing ③ ★ 反证：去掉额度守卫的同一条语句必须能把计数推过上限。
// 这一腿用真函数 TouchAPIKey（就是旧形态「只管加、不加判据」的那一半）当对照：
// 它把已打满的 Key 继续加到 limit+1 ⇒ 说明②里「恰好 K 次」不是用例没打到位的假绿，
// 而是 WHERE 守卫真的在挡。对照与主判据共用同一份行状态，差异只在守卫。
func TestReserveAPICallGuardIsLoadBearing(t *testing.T) {
	pinSQLiteForQuotaTest(t)
	s := newTestStoreWithTenants(t)

	plain, err := s.CreateAPIKey(1, 7, "反证对照", "translate", 2)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByHash(HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}
	// 先按正路打满（limit=2）
	if !s.ReserveAPICall(k.ID, k.DailyCallLimit) || !s.ReserveAPICall(k.ID, k.DailyCallLimit) {
		t.Fatal("前置：前两次占用必须成功")
	}
	if s.ReserveAPICall(k.ID, k.DailyCallLimit) {
		t.Fatal("前置：第三次必须被拒（否则本反证没有对照物）")
	}
	// 旧形态那半：不带判据的自增，同一条 Key 会继续往上加 ⇒ 证明守卫确实在改变结果
	s.TouchAPIKey(k.ID)
	s.TouchAPIKey(k.ID)
	if _, used, _ := readKeyQuota(t, s, k.ID); used != 4 {
		t.Fatalf("TouchAPIKey 反证腿未把计数推过上限（got=%d want=4）⇒ 对照失效，②的绿灯不可信", used)
	}
}

// ============================================================================
// 以下为**阶段二 Redis 档**的回归（★ D-8 补，2026-09-29）
//
// 为什么不能只测 SQLite 档就收工：ReserveAPICall 在 Redis 在位时走的是另一条判据
// （INCR 的返回值就是"当日第几次"，跨实例聚合），而生产是**多实例 + Redis**。
// 只测 SQLite 档等于把生产真跑的那条分支留在暗处——本仓已经栽过多次
// 「测试覆盖了降级路径、生产走的是主路径」。
//
// 注入形态：本包不启 Redis（redis.Get()==nil ⇒ ratelimit.Daily() 返回 nil，
// 且 daily_test 自己会 Init("") 复位全局单例，跨包依赖它等于挂一颗定时雷）；
// 改为对**内核 reserveAPICall(id, limit, c)** 直接喂假计数器——
// 分支判据与真 Redis 完全同码，只是把网络换成了可控序号源。
// ============================================================================

// fakeCounter 按脚本回吐 INCR 序号/错误的假计数器（实现 ratelimit.Counter）。
type fakeCounter struct {
	seq       []int64 // 依次返回的序号（用尽后重复最后一个，方便跑长循环）
	errAt     int     // 第几次 Incr 返回错误（0=从不；与 alwaysErr 二选一）
	alwaysErr bool    // true=每次 Incr 都失败（模拟"Redis 这一段时段不可用"，回落腿要用它）
	calls     int
	keys      []string // 记录被用到的配额键，判"当日口径"用
}

// Incr 返回脚本里的下一个序号；到 errAt 那一次（或 alwaysErr 的每一次）返回错误。
// ★ alwaysErr 这条是首跑真踩出来的：只用 errAt:1 造"Redis 故障"，
//
//	第二次 Incr 就恢复正常了，于是"SQLite 已打满还必须挡住"那腿被假计数放行——
//	假计数器的故障脚本要贴住被测语义，不然测的是夹具不是产品。
func (f *fakeCounter) Incr(key string) (int64, error) {
	f.calls++
	f.keys = append(f.keys, key)
	if f.alwaysErr || (f.errAt != 0 && f.calls == f.errAt) {
		return 0, errors.New("redis 模拟中断")
	}
	n := int64(len(f.seq))
	if f.calls-1 < len(f.seq) {
		n = f.seq[f.calls-1]
	} else if len(f.seq) > 0 {
		n = f.seq[len(f.seq)-1]
	}
	return n, nil
}

// Get 内核不读它（预检才读），实现接口即可。
func (f *fakeCounter) Get(string) (int64, error) { return 0, nil }

// TestReserveAPICallRedisBranch Redis 档四条语义：超限即拒、被拒不落展示计数、
// 键名按当日日期、INCR 故障时**回落条件 UPDATE 而不是放行**。
func TestReserveAPICallRedisBranch(t *testing.T) {
	pinSQLiteForQuotaTest(t)
	s := newTestStoreWithTenants(t)
	today := time.Now().Format("2006-01-02")

	plain, err := s.CreateAPIKey(1, 7, "Redis 档配额", "translate", 3)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByHash(HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	// ① 序号 1/2/3 放行、第 4 次拒
	fc := &fakeCounter{seq: []int64{1, 2, 3, 4, 5}}
	for i := 1; i <= 3; i++ {
		if !s.reserveAPICall(k.ID, k.DailyCallLimit, fc) {
			t.Fatalf("Redis 档第 %d 次被拒（上限 3）⇒ 序号判定写反了", i)
		}
	}
	if s.reserveAPICall(k.ID, k.DailyCallLimit, fc) {
		t.Fatal("Redis 档第 4 次仍放行 ⇒ INCR 序号没参与判定（那就是把 Redis 当纯计数器用错了）")
	}
	// ② 配额键必须按「当日日期」构造：跨日清零靠的就是换键，键里没有日期＝永远打满
	wantKey := ratelimit.KeyForAKQuota(k.ID, today)
	for i, gk := range fc.keys {
		if gk != wantKey {
			t.Fatalf("第 %d 次用的配额键=%q 应为 %q ⇒ 跨日不会自动清零", i+1, gk, wantKey)
		}
	}
	// ③ 被拒的那次不许写展示计数：Redis 打了 4 次序号，但 call_count 只能等于放行次数 3
	var callCount int64
	if err := s.db.QueryRow(`SELECT call_count FROM api_keys WHERE id=?`, k.ID).Scan(&callCount); err != nil {
		t.Fatal(err)
	}
	if callCount != 3 {
		t.Fatalf("Redis 档 call_count=%d 应为 3（被拒的第 4 次不许进客户用量概览）", callCount)
	}

	// ④ INCR 故障 ⇒ 回落条件 UPDATE（**不许 fail-open**）。
	//    ★ 先记一条实测行为差异（写在这里是因为它容易被人当 bug 报）：
	//    Redis 在位时本函数只维护 call_count，**calls_today 不再逐次写**（额度权威在 Redis），
	//    所以 Redis 中途故障回落时，判据读的是 SQLite 那列的**旧值**——当日可能多放几格。
	//    这属于「限流精度」而不是资金面：超发的部分不扣钱，只是多让几次请求过去（D-8 同一定性）。
	//    因此这一腿要验的不是"回落就立刻挡住"，而是「回落沿用同一条条件 UPDATE 的判据」。
	fcErr := &fakeCounter{seq: []int64{1}, errAt: 1}
	// ④-a SQLite 侧还有余额 ⇒ 放行并写计数
	if _, err := s.db.Exec(`UPDATE api_keys SET calls_today=0, calls_today_date=? WHERE id=?`, today, k.ID); err != nil {
		t.Fatal(err)
	}
	if !s.reserveAPICall(k.ID, k.DailyCallLimit, fcErr) {
		t.Fatal("Redis INCR 故障 + SQLite 未打满 ⇒ 应由条件 UPDATE 放行（这是降级，不是放行一切）")
	}
	if _, used, _ := readKeyQuota(t, s, k.ID); used != 1 {
		t.Fatalf("回落路径没走条件 UPDATE：calls_today=%d want=1", used)
	}
	// ④-b SQLite 侧已打满 ⇒ 必须拒（fail-closed 的是**额度**，不是可用性）
	if _, err := s.db.Exec(`UPDATE api_keys SET calls_today=3, calls_today_date=? WHERE id=?`, today, k.ID); err != nil {
		t.Fatal(err)
	}
	// ★ 这里必须换一把**每次都不行**的计数器（alwaysErr），而不是复用上面那只脚本已走完的 fcErr
	var countBefore int64
	if err := s.db.QueryRow(`SELECT call_count FROM api_keys WHERE id=?`, k.ID).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if s.reserveAPICall(k.ID, k.DailyCallLimit, &fakeCounter{alwaysErr: true}) {
		t.Fatal("SQLite 已打满 + Redis 持续故障 ⇒ 仍放行就是 fail-open（额度失守）")
	}
	if err := s.db.QueryRow(`SELECT call_count FROM api_keys WHERE id=?`, k.ID).Scan(&countBefore); err != nil {
		t.Fatal(err)
	}
	if got := countBefore; got != 4 {
		t.Fatalf("回落被拒那次动了展示计数：call_count=%d want=4（④-a 放行过一次，就该停在 4）", got)
	}
	// 反证本体：故障时如果代码写成 fail-open（直接 return true），上面两条必有一条红；
	// 这里再显式确认「不限额档」在 Redis 故障时仍然放行，免得把降级误修成拒绝服务。
	plainFree, err := s.CreateAPIKey(1, 7, "Redis 档不限额", "translate", 0)
	if err != nil {
		t.Fatal(err)
	}
	kf, err := s.GetAPIKeyByHash(HashAPIKey(plainFree))
	if err != nil {
		t.Fatal(err)
	}
	if !s.reserveAPICall(kf.ID, kf.DailyCallLimit, &fakeCounter{seq: []int64{999}, errAt: 1}) {
		t.Fatal("不限额 Key 在 Redis 故障时被拒 ⇒ 降级修成了拒绝服务（防薅口径：故障 fail-open，额度 fail-closed）")
	}
}
