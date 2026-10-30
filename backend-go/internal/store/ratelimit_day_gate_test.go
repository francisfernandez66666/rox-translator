// ============ ratelimit_day_gate_test.go · 职责说明 ============
// 组 2a「UTC 自然日免费档计数器」的家族级闸门（K1／K2／K4 三条断言的执行体）。
//
// 这四条各管一件事，缺一条就放过去一类真实缺陷：
//
//	D1 日界算法（K1 的前半）：`utcDayStart` 必须把任意时刻折到它**所在 UTC 日**的 00:00，
//	   且**与宿主机时区无关**。现网服务器跑 UTC、开发机 +08：拿本地日历切一次，
//	   两边会各自在不同时刻恢复额度，而用户报的"我的免费额度怎么没刷新"永远只有一半能复现。
//	D2 跨日行为（K1 的后半）：昨日那一行今天读必须归零、今天第一次累加必须**重置那一行**
//	   而不是新增一行——"行数不随日增加"是这条链唯一的库层证据
//	   （现仓没有 rate_limits 清理腿；用 key 里塞日期的写法能过 D1 却把表每天撑大一圈）。
//	D3 档位三态（K2）：`0＝不限`、正数＝到量拦；`-1` 不在这里判（那是组4 准入函数的"关档"）。
//	   ⚠️ 特别钉「limit=0 不许被读成"未配置"」：那是 H-1 那一族「配 0 当没配」的复发形态，
//	   运营真想要"不限"时只能配一个更大的数，第二天就有人来把 0 改成 999999。
//	D4 累加的原子形态（K4 的机械半段）：源码必须走 `SET count=count+? … AND window_start=?`
//	   这一条**判据与计数同语句**的 UPDATE；出现 `SET count=?`（读回来自己算再加）即红。
//	   为什么这条不能只靠行为测试：SQLite 单写会把读-改-写也串行化成"看起来对"的结果，
//	   现网 PG 上才会丢量（AGENTS §三 那条「裸 map 读写被连接池顺带串行化了」同一个教训）。
//
// ⚠️ D4 是**源码级负向锁**，所以先剥注释再扫（本仓老坑：说明注释里写着"不许用 SET count=?"
// 会把负向判据喂饱，于是摘掉实现也照样绿）。反证腿把真修法换回坏形态、必须抓到。
// =============================================
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"translator/internal/config"
)

// dayGateDB 建一个命名共享缓存的内存库并钉死方言（夹具形态照 systemconfig_cache_test.go 的新_sysCfgStore）。
//
// ⚠️ **不许**退回 `:memory:` 或"收口到单连接"：`:memory:` 是**每条连接各自的私有库**，
// 而 `store.New` 的迁移腿会在 PRAGMA 游标未关时开嵌套查询——单连接直接死锁在 New，
// 事后钉回 1 又把带表的连接当超额闲置关掉（那一族在本仓首跑真踩过两次，注释就写在那把夹具头上）。
// 共享缓存 DSN 之外还带 `busy_timeout(5000)` 与 `_txlock=immediate`：
// 后者是本组并发腿的前提——`RateDayAdd` 靠"条件 UPDATE 影响 0 行再分诊"，
// 惰性事务（deferred）在 SQLite 上会先拿共享读锁、升写锁时撞 SQLITE_BUSY，
// 那会把一条正确的实现读成"并发丢量"（假红），immediate 让写冲突在开事务那一刻就排队。
func dayGateDB(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	dsn := "file:daygate_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) +
		"?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开共享缓存内存库失败：%v", err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	s, err := New(sqlDB)
	if err != nil {
		t.Fatalf("建 Store 失败（迁移链本身跑不过，后面的判据都无从谈起）：%v", err)
	}
	return s, sqlDB
}

func TestRateDayUTCDateBoundary(t *testing.T) {
	daySec := int64(24 * 3600)
	cases := []struct {
		name string
		in   time.Time
		want string // 期望落到的 UTC 日（YYYY-MM-DD），现读自 utcDayStart
	}{
		{"UTC 23:59:59 仍属今天", time.Date(2026, 10, 30, 23, 59, 59, 0, time.UTC), "2026-10-30"},
		{"UTC 00:00:00 是新的一天", time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), "2026-10-31"},
		// 现网与开发机的时区差就在这一档：+08 的 10-30 01:00 其实是 UTC 10-29 17:00。
		{"东八区凌晨折到前一个 UTC 日", time.Date(2026, 10, 30, 1, 0, 0, 0, time.FixedZone("CST", 8*3600)), "2026-10-29"},
		{"西五区夜里折到同一个 UTC 日", time.Date(2026, 10, 30, 20, 0, 0, 0, time.FixedZone("EST", -5*3600)), "2026-10-31"},
	}
	for _, c := range cases {
		got := time.Unix(utcDayStart(c.in), 0).UTC().Format("2006-01-02")
		if got != c.want {
			t.Errorf("%s：utcDayStart 折到 %s，期望 %s", c.name, got, c.want)
		}
	}
	// 相邻两日的日界必须**恰差 86400**（不是 86399/86401，也不许靠夏令时——UTC 没有夏令时，
	// 而写成 AddDate(0,0,1) 那种"日历加法"在折秒时最容易差出一个闰秒量级的漂移）。
	a := utcDayStart(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	b := utcDayStart(time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	if b-a != daySec {
		t.Errorf("相邻 UTC 日的日界差 %d 秒，期望 %d", b-a, daySec)
	}
	if secondsPerUTCDay != daySec {
		t.Errorf("secondsPerUTCDay = %d，与日界实际差值 %d 不同源", secondsPerUTCDay, b-a)
	}
}

// D2 跨日行为：昨日那一行今天读＝0、今天加一次＝重置那一行且**表里仍只有一行**
func TestRateDayCrossUTCDayResetsInPlace(t *testing.T) {
	s, sqlDB := dayGateDB(t)
	scope, key := "free_pt_user", "4242"
	today := utcDayStart(time.Now())
	yesterday := today - secondsPerUTCDay

	// 造"昨天用过 100 积分"那一行（库里真实存在的形态，不是 mock）
	if _, err := sqlDB.Exec(`INSERT INTO rate_limits(scope,key,count,window_start,lock_until) VALUES(?,?,?, ?,0)`, scope, key, 100, yesterday); err != nil {
		t.Fatalf("造昨日行失败：%v", err)
	}
	st, err := s.RateDayLoad(scope, key)
	if err != nil {
		t.Fatalf("读昨日行失败：%v", err)
	}
	if st.Used != 0 {
		t.Errorf("昨日 100 用量被带进今天：Used=%d，期望 0（UTC 日界必须整窗归零）", st.Used)
	}
	if st.DayStart != today {
		t.Errorf("DayStart=%d，期望今日 00:00 %d", st.DayStart, today)
	}
	// 读路径不许改库（否则"看一眼看板"就把客户的窗口起点推前，跨日那一档再判一次假)
	var wsAfter int64
	if err := sqlDB.QueryRow(`SELECT window_start FROM rate_limits WHERE scope=? AND key=?`, scope, key).Scan(&wsAfter); err != nil {
		t.Fatalf("回读 window_start 失败：%v", err)
	}
	if wsAfter != yesterday {
		t.Errorf("读路径把 window_start 改写了（%d→%d）：归零必须是**读时判定**，落库交给第一次 Add", wsAfter, yesterday)
	}
	// 今天第一次累加：重置那一行，count 只含今天的量
	next, err := s.RateDayAdd(scope, key, 30)
	if err != nil {
		t.Fatalf("跨日累加失败：%v", err)
	}
	if next.Used != 30 || next.DayStart != today {
		t.Errorf("跨日累加读数 {Used:%d, DayStart:%d}，期望 {30, %d}（昨日的 100 不许带过来）", next.Used, next.DayStart, today)
	}
	var rows int
	var cnt, ws int64
	if err := sqlDB.QueryRow(`SELECT count, window_start FROM rate_limits`).Scan(&cnt, &ws); err != nil {
		t.Fatalf("回读全表失败（行数变了？）：%v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM rate_limits`).Scan(&rows); err != nil {
		t.Fatalf("数行数失败：%v", err)
	}
	// 上面那条 `SELECT count, window_start FROM rate_limits` 在两行时会 Scan 到第一行而不是报错，
	// 所以"只有一行"必须单独数一次 COUNT(*)——K1 的"行数不随日增加"就是这一条。
	if rows != 1 {
		t.Errorf("跨日重置新增了行（COUNT=%d，期望 1）⇒ 表会每天长大一圈，而 rate_limits 没有清理腿", rows)
	}
	if cnt != 30 || ws != today {
		t.Errorf("重置后的行 (count=%d, window_start=%d)，期望 (30, %d)", cnt, ws, today)
	}

	// 同一日再累加必须**继续在同一行上累加**（K1 的"两次 Add(5) ⇒ 10"）
	if _, err := s.RateDayAdd(scope, key, 5); err != nil {
		t.Fatalf("当日累加失败：%v", err)
	}
	again, err := s.RateDayAdd(scope, key, 5)
	if err != nil {
		t.Fatalf("当日第二次累加失败：%v", err)
	}
	if again.Used != 40 {
		t.Errorf("同一天 Add(30)+Add(5)+Add(5) 读数 Used=%d，期望 40", again.Used)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM rate_limits`).Scan(&rows); err != nil {
		t.Fatalf("再数行数失败：%v", err)
	}
	if rows != 1 {
		t.Errorf("同日累加新增了行（COUNT=%d）⇒ 计数窗口被拆成多行，判据读的是哪一行都没人知道", rows)
	}
}

// D3 档位三态（K2）
func TestRateDayCeilReachedTiers(t *testing.T) {
	cases := []struct {
		name  string
		used  int64
		limit int64
		want  bool
	}{
		{"配 0＝不限（永远不拦）", 9_999_999, 0, false},
		{"配负数＝不限（0 与负数同一条腿，别长出第二把尺子）", 500, -1, false},
		{"正数未到量", 299, 300, false},
		{"正数恰到量", 300, 300, true},
		{"正数超量（脏读数也要拦）", 301, 300, true},
		{"零用量配正档", 0, 300, false},
	}
	for _, c := range cases {
		got := RateDayCeilReached(DayState{Used: c.used}, c.limit)
		if got != c.want {
			t.Errorf("%s：RateDayCeilReached(used=%d, limit=%d)=%v，期望 %v", c.name, c.used, c.limit, got, c.want)
		}
	}
}

// D4 原子形态（K4 的机械半段）
func TestRateDayAddIsConditionalNotReadModifyWrite(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "ratelimit_day.go"))
	if err != nil {
		t.Fatalf("读 ratelimit_day.go 失败（读不到＝这条锁失明，不算绿）：%v", err)
	}
	body := stripDayComments(string(src))
	if !strings.Contains(body, "func (s *Store) RateDayAdd(") {
		t.Fatal("源码里取不到 RateDayAdd（函数改名了？同步改这条锁，别删断言）")
	}
	if v := dayGateAccumProblem(body); v != "" {
		t.Error(v)
	}
	// 反证（判据自证）：真修法片段必须干净，坏形态片段必须被同一个函数抓到。
	// 两段夹具只差"累加是不是在 SQL 里做"这一件事，判定走**同一个** dayGateAccumProblem。
	if v := dayGateAccumProblem(stripDayComments(goodDayFragment)); v != "" {
		t.Errorf("反证落空：原子累加的正确片段被判违规（判据过严，第二天就被摘）：%s", v)
	}
	if v := dayGateAccumProblem(stripDayComments(badDayFragment)); v == "" {
		t.Error("反证落空：读-改-写的坏片段没被抓 ⇒ D4 这条腿抓不住任何退化")
	}
	// 第三段夹具：把日界条件摘掉（只留 count=count+?）也必须被抓——
	// 这一档的坏法不是"丢量"，是**旧日那一行被当成今天加上**，跨日归零当场失效。
	if v := dayGateAccumProblem(stripDayComments(noBoundDayFragment)); v == "" {
		t.Error("反证落空：累加语句没带 window_start 条件却没被抓 ⇒ 跨日归零那条腿可以被静默摘掉")
	}
}

// dayGateAccumProblem 扫一段源码里的 SQL 字面量，判「当日累加」这一条语句的形状。
// 返回问题描述（合规则空串）。判据只问**语句形状**，不问变量名，所以改名式破坏照样在射程里：
//
//	① 有且只有一条 `count=count+?` 的累加语句（多一条＝把同一天的量在两条分支里各加一次）；
//	② 那条语句的 WHERE 必须带 `window_start=?`——判据与计数同语句是 `RateReserve` 定下的取向，
//	   少了日界条件就是"昨日那一行今天继续加"，K1 的归零当场失效；
//	③ 任何**绝对写回**（`SET count=?`）也必须带 `window_start=?`：跨日重置那一支是合法绝对写，
//	   但它合法的理由恰恰是"只在读到那个旧起点时才成立"。不带条件就是 read-modify-write 复活。
func dayGateAccumProblem(body string) string {
	lits := sqlLiterals(body)
	var acc []string
	for _, q := range lits {
		if strings.Contains(q, "count=count+?") {
			acc = append(acc, q)
		}
	}
	if len(acc) == 0 {
		return "RateDayAdd 里没有 `count=count+?` 的 SQL 侧原子累加 ⇒ 并发会丢量（本地 SQLite 单连接看不出来，PG 上现网才发作）"
	}
	if len(acc) != 1 {
		return fmt.Sprintf("当日累加语句有 %d 条（期望恰 1）⇒ 同一笔量可能在两条分支里各加一次", len(acc))
	}
	if !strings.Contains(acc[0], "window_start=?") {
		return "累加语句没带 `window_start=?` 条件 ⇒ 旧日那一行会被当成今天累加，跨 UTC 日归零当场失效"
	}
	for _, q := range lits {
		if strings.Contains(q, "SET count=?") && !strings.Contains(q, "window_start=?") {
			return "出现不带日界条件的绝对写回 `SET count=?`（读-改-写复活）：" + q
		}
	}
	return ""
}

// sqlLiterals 取一段 Go 源码里所有**双引号**字符串字面量（粗切：跳过转义符，够本文件的形状判定用）。
func sqlLiterals(body string) []string {
	var out []string
	runes := []rune(body)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '"' {
			continue
		}
		var sb []rune
		j := i + 1
		closed := false
		for ; j < len(runes); j++ {
			if runes[j] == '\\' {
				sb = append(sb, runes[j])
				if j+1 < len(runes) {
					sb = append(sb, runes[j+1])
					j++
				}
				continue
			}
			if runes[j] == '"' {
				closed = true
				break
			}
			sb = append(sb, runes[j])
		}
		if closed {
			out = append(out, string(sb))
			i = j
		}
	}
	return out
}

const goodDayFragment = `
func demo() {
	db.Exec("UPDATE rate_limits SET count=count+? WHERE scope=? AND key=? AND window_start=?", 1, "s", "k", 0)
	db.Exec("UPDATE rate_limits SET count=?, window_start=? WHERE scope=? AND key=? AND window_start=?", 1, 0, "s", "k", -86400)
}`

const badDayFragment = `
func demo() {
	cur, _ := load()
	db.Exec("UPDATE rate_limits SET count=? WHERE scope=? AND key=?", cur+1, "s", "k")
}`

// noBoundDayFragment 是第三种坏法：累加写成 SQL 侧，但把日界条件摘了。
const noBoundDayFragment = `
func demo() {
	db.Exec("UPDATE rate_limits SET count=count+? WHERE scope=? AND key=?", 1, "s", "k")
}`

// 行为半段：8 个 goroutine 各加 1，最终恰为 8（夹具是单连接内存库，
// 这里证明的是"每一笔都落地、没有分支把量吞掉"；原子性本身由上面那条源码锁守）。
func TestRateDayAddConcurrentKeepsEveryUnit(t *testing.T) {
	s, sqlDB := dayGateDB(t)
	// 共享缓存的 SQLite 用**表级锁**：4 条连接同时开写事务会直接退 `database table is locked (262)`，
	// 而 busy_timeout 对这一类锁不重试（夹具首跑真踩）。所以这里把连接收口到 1，让 8 笔排在同一条连接上——
	// ⚠️ 这一腿因此只证明"每一笔都落地、没有分支把量吞掉"，**不证明跨连接原子性**；
	// 后者由上面 D4 那条**源码形状锁**承担（SQLite 单写会把读-改-写也串行化成"看起来对"，
	// 拿这条行为腿当原子性证据就是 AGENTS §三 那条「裸 map 读写被连接池顺带串行化」的同形自欺）。
	// 收口与还原的写法沿用 renewal_grace_test.go 的既有夹具。
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.SetMaxOpenConns(4) })
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := s.RateDayAdd("free_pt_tenant", "777", 1)
			errs[i] = e
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("第 %d 笔累加报错：%v", i, e)
		}
	}
	st, err := s.RateDayLoad("free_pt_tenant", "777")
	if err != nil {
		t.Fatalf("读当日状态失败：%v", err)
	}
	if st.Used != n {
		t.Errorf("并发 8 笔各加 1，读数 Used=%d ⇒ 有笔次被丢掉或读旧值覆盖", st.Used)
	}
}

// stripDayComments 粗剥行注释与块注释（只服务于本文件的形状扫描，不追求词法完备）。
func stripDayComments(src string) string {
	var out []string
	inBlock := false
	for _, ln := range strings.Split(src, "\n") {
		t := strings.TrimSpace(ln)
		if inBlock {
			if strings.Contains(t, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(t, "/*") {
			inBlock = true
			continue
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}
