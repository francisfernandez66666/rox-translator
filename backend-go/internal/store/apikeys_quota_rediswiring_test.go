// ============================================================================
// apikeys_quota_rediswiring_test.go — ★ D-8 的 Redis 档**真接线**（2026-09-29）
//
// 与同包 apikeys_quota_test.go 的分工：
//
//	那份用**假 Counter**（直接实现 ratelimit.Counter 接口）证 reserveAPICall 的内核判定；
//	本份把假服务端换成**进程内真 RESP 服务**，让请求走完生产同一条出站链
//	ratelimit.Daily() → redis.Client → TCP，因此能锁到假 Counter 锁不到的三件事：
//
//	① 出站**键名**必须是 ratelimit.KeyForAKQuota 的 `ak:quota:<id>:<date>`——
//	   写错（漏日期 / 用错 scope）会让「跨日自动清零」失效，表现是「昨天的量继续吃今天的额度」，
//	   而这条偏差在假 Counter 里永远看不见（桩自己拼的键，被测方无从犯错）；
//	② **跨实例聚合**才是 Redis 档存在的唯一理由：两个互不相干的 Store（各自的库、各自的行锁）
//	   共用一个 Redis 时，总放行必须等于限额，而不是「每个实例各放行 limit 次」；
//	③ Redis 命令**失败即降级**，不是 fail-open：INCR 回 -ERR 时必须落回条件 UPDATE，
//	   额度判据由 DB 行锁接管（旧形态里这里是「拿不到计数就放行」，等于限额可被网络抖动绕开）。
//
// 另锁一条**现行口径**（不是缺陷，是已记录的取舍）：Redis 档只维护 call_count/last_used_at 展示字段，
// calls_today 不再由 DB 维护。若哪天把它改成双写，本断言会红——那是**口径修正**而非回归，
// 需要同步更新本文件与 D-8 记录里的说明，而不是简单摘掉这条锁。
//
// 仓库约定（与 internal/infra/redis 两份协议测试同口径）：不连真实 Redis、不写死外部地址、
// 不引第三方库；监听 127.0.0.1:0，用例结束必须 redis.Init("","") 还原单例。
// ============================================================================
package store

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/infra/ratelimit"
	"translator/internal/infra/redis"
)

// ---------- 进程内假 Redis（有状态 INCR，够本用例用）----------

// fakeQuotaRedis 只实现本用例需要的命令：PING / AUTH / INCR / GET / EXPIRE。
// 它按 key 维护真计数器，所以「同一个键被两个 Store 各 INCR 几次」是可观测的事实，
// 而不是桩返回一个调用方编排好的数字。
type fakeQuotaRedis struct {
	ln net.Listener

	mu       sync.Mutex
	counts   map[string]int64
	cmds     [][]string
	failIncr bool // true ⇒ INCR 回 -ERR（模拟 Redis 故障 / 网络中断）
	closed   bool
	conns    []net.Conn
}

func newFakeQuotaRedis(t *testing.T) *fakeQuotaRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("假 Redis 监听失败: %v", err)
	}
	s := &fakeQuotaRedis{ln: ln, counts: map[string]int64{}}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				s.serve(nc)
			}()
		}
	}()
	t.Cleanup(func() {
		s.mu.Lock()
		s.closed = true
		for _, c := range s.conns {
			_ = c.Close() // 唤醒仍阻塞在读上的协程，否则 wg.Wait 挂死
		}
		s.mu.Unlock()
		_ = ln.Close()
		wg.Wait()
	})
	return s
}

func (s *fakeQuotaRedis) addr() string { return s.ln.Addr().String() }

// setFailIncr 打开/关闭 INCR 故障档（持锁写，保证服务协程可见）。
func (s *fakeQuotaRedis) setFailIncr(v bool) {
	s.mu.Lock()
	s.failIncr = v
	s.mu.Unlock()
}

// keysWithCounts 返回「键 → 累计 INCR 次数」快照，断言聚合与键名用。
func (s *fakeQuotaRedis) keysWithCounts() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.counts))
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

// commandNames 返回服务端收到的命令名序列（大写），用于自证「真打到了 INCR」而不是没打到。
func (s *fakeQuotaRedis) commandNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.cmds))
	for _, c := range s.cmds {
		if len(c) > 0 {
			out = append(out, strings.ToUpper(c[0]))
		}
	}
	return out
}

func (s *fakeQuotaRedis) serve(nc net.Conn) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = nc.Close()
		return
	}
	s.conns = append(s.conns, nc)
	s.mu.Unlock()
	defer nc.Close()

	r := bufio.NewReader(nc)
	for {
		cmd, err := readFakeRespCommand(r)
		if err != nil {
			return
		}
		name := strings.ToUpper(cmd[0])
		s.mu.Lock()
		s.cmds = append(s.cmds, cmd)
		fail := s.failIncr
		s.mu.Unlock()

		switch name {
		case "PING":
			_, _ = nc.Write([]byte("+PONG\r\n"))
		case "AUTH":
			_, _ = nc.Write([]byte("+OK\r\n"))
		case "INCR":
			if fail {
				_, _ = nc.Write([]byte("-ERR simulated redis failure\r\n"))
				continue
			}
			s.mu.Lock()
			s.counts[cmd[1]]++
			n := s.counts[cmd[1]]
			s.mu.Unlock()
			_, _ = nc.Write([]byte(":" + strconv.FormatInt(n, 10) + "\r\n"))
		case "GET":
			s.mu.Lock()
			v := s.counts[cmd[1]]
			s.mu.Unlock()
			_, _ = nc.Write([]byte(":" + strconv.FormatInt(v, 10) + "\r\n"))
		case "EXPIRE":
			_, _ = nc.Write([]byte(":1\r\n"))
		default:
			_, _ = nc.Write([]byte("+OK\r\n"))
		}
	}
}

// readFakeRespCommand 解析一条 RESP 数组命令（与 internal/infra/redis 客户端出站编码同口径）。
func readFakeRespCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" || line[0] != '*' {
		return nil, fmt.Errorf("期望 RESP 数组，实得 %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("非法数组头 %q", line)
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		h, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		h = strings.TrimRight(h, "\r\n")
		if h == "" || h[0] != '$' {
			return nil, fmt.Errorf("期望 bulk 参数，实得 %q", h)
		}
		l, err := strconv.Atoi(h[1:])
		if err != nil || l < 0 {
			return nil, fmt.Errorf("非法 bulk 头 %q", h)
		}
		buf := make([]byte, l+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:l]))
	}
	return args, nil
}

// useFakeRedis 把假服务端接到进程级单例上，用例结束还原为「未启用 Redis」。
// ⚠️ 必须 t.Cleanup 还原：本包其余用例（含同文件里 SQLite 条件 UPDATE 那几条腿）都依赖
// ratelimit.Daily() 返回 nil，单例泄漏会让它们集体改走 Redis 档，红得莫名其妙。
func useFakeRedis(t *testing.T, s *fakeQuotaRedis) {
	t.Helper()
	redis.Init(s.addr(), "")
	if !redis.Enabled() {
		t.Fatal("redis.Init 后单例仍为 nil ⇒ 假服务端地址没接上")
	}
	t.Cleanup(func() { redis.Init("", "") })
}

// ---------- ②跨实例聚合 + ①键名 ----------

// TestReserveAPICallRedisAggregatesAcrossInstances 两个互不相干的 Store 共用一份 Redis：
// 总放行必须恰好等于限额，且出站键名必须是当日口径的 ak:quota:<id>:<date>。
func TestReserveAPICallRedisAggregatesAcrossInstances(t *testing.T) {
	pinSQLiteForQuotaTest(t)
	fr := newFakeQuotaRedis(t)
	useFakeRedis(t, fr)

	const limit = 5
	a := newQuotaStore(t)
	b := newQuotaStore(t)

	plainA, err := a.CreateAPIKey(1, 7, "实例A的Key", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	ka, err := a.GetAPIKeyByHash(HashAPIKey(plainA))
	if err != nil {
		t.Fatal(err)
	}
	// B 侧用**同一个 Key ID** 建一行（跨实例部署里两实例读同一库，ID 天然相同；
	// 这里两个是独立夹具，故显式对齐 ID，避免「ID 不同所以各数各的」把这条腿测成假绿）。
	plainB, err := b.CreateAPIKey(1, 7, "实例B的Key", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := b.GetAPIKeyByHash(HashAPIKey(plainB))
	if err != nil {
		t.Fatal(err)
	}
	if ka.ID != kb.ID {
		t.Fatalf("两侧 Key ID 不等（%d vs %d）⇒ 本用例靠同 ID 才能验证聚合，夹具形态已变，请改测法", ka.ID, kb.ID)
	}

	admitted := 0
	for i := 0; i < limit*2; i++ {
		// 交替打到 A / B，模拟负载均衡下请求散落在两个实例
		s := a
		if i%2 == 1 {
			s = b
		}
		if s.ReserveAPICall(ka.ID, limit) {
			admitted++
		}
	}
	if admitted != limit {
		t.Fatalf("两实例合计放行 %d 次（限额 %d）⇒ Redis 没起到跨实例聚合的作用（每个实例各数各的＝多副本下额度实为 limit×副本数）",
			admitted, limit)
	}

	// ① 键名自证：必须真收到 INCR，且键就是当日口径（漏日期＝昨天的量吃今天的额度）
	names := fr.commandNames()
	incrSeen := 0
	for _, n := range names {
		if n == "INCR" {
			incrSeen++
		}
	}
	if incrSeen == 0 {
		t.Fatalf("假 Redis 一条 INCR 都没收到（命令序列 %v）⇒ 本用例其实走的是 SQLite 档，绿灯无效", names)
	}
	// 键名**按字面量**构造，不调 ratelimit.KeyForAKQuota：
	// 用被测函数自己当期望值＝同义反复，它哪天把日期或 scope 拼错，这条锁会跟着一起错（恒绿）。
	today := time.Now().Format("2006-01-02")
	wantKey := "ak:quota:" + strconv.FormatInt(ka.ID, 10) + ":" + today
	got := fr.keysWithCounts()
	if len(got) != 1 {
		t.Fatalf("Redis 侧出现 %d 个配额键（%v）⇒ 同一把 Key 被拆成多个计数器，跨实例聚合无从谈起", len(got), got)
	}
	if _, ok := got[wantKey]; !ok {
		t.Fatalf("配额键名不符：期望字面量 %q，实得 %v ⇒ 键里漏了日期或 scope，跨日清零会失效", wantKey, got)
	}
	// 同时钉住「常量与线上出站值同源」：KeyForAKQuota 若与字面量脱钩，说明读侧（validateAPIKey 的 Get）
	// 与写侧（本处的 INCR）用的不是同一个键，两侧各数各的。
	if k := ratelimit.KeyForAKQuota(ka.ID, today); k != wantKey {
		t.Fatalf("KeyForAKQuota 与出站字面量脱钩：常量=%q 实发=%q ⇒ 读侧预检读的是另一个键", k, wantKey)
	}
	if got[wantKey] != int64(limit*2) {
		t.Fatalf("INCR 次数=%d（应=%d：被拒的那几次也要占序号，否则第 K+1 次永远拿不到超限信号）",
			got[wantKey], limit*2)
	}

	// 现行口径（等值锁，不是观察）：Redis 档下 DB 只维护展示字段 call_count，calls_today 恒 0。
	// 若哪天改成双写，这两条会红——那是口径修正，请连同 D-8 记录与本文件头一起更新。
	_, aToday, _ := readKeyQuota(t, a, ka.ID)
	_, bToday, _ := readKeyQuota(t, b, kb.ID)
	if aToday != 0 || bToday != 0 {
		t.Fatalf("Redis 档却写了 DB 的 calls_today（A=%d B=%d，期望都=0）⇒ 计数口径已改双写，本锁需与 D-8 记录同步更新", aToday, bToday)
	}
	var aCalls, bCalls int64
	if err := a.db.QueryRow(`SELECT call_count FROM api_keys WHERE id=?`, ka.ID).Scan(&aCalls); err != nil {
		t.Fatalf("读 A 侧 call_count 失败: %v", err)
	}
	if err := b.db.QueryRow(`SELECT call_count FROM api_keys WHERE id=?`, kb.ID).Scan(&bCalls); err != nil {
		t.Fatalf("读 B 侧 call_count 失败: %v", err)
	}
	if aCalls+bCalls != int64(limit) {
		t.Fatalf("展示用量合计=%d（真放行 %d）⇒ 管理台概览与实际放行不符，对账必打架", aCalls+bCalls, limit)
	}
}

// ---------- ③ Redis 失败必须降级，绝不 fail-open ----------

// TestReserveAPICallRedisFailureFallsBackToDB INCR 回 -ERR 时：
// 额度判据必须由 DB 行锁接管——库里当日量已打满就拒绝，未打满就放行（而不是「拿不到计数＝一律放行」）。
func TestReserveAPICallRedisFailureFallsBackToDB(t *testing.T) {
	pinSQLiteForQuotaTest(t)
	fr := newFakeQuotaRedis(t)
	useFakeRedis(t, fr)

	const limit = 2
	s := newQuotaStore(t)
	plain, err := s.CreateAPIKey(1, 7, "故障降级", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.GetAPIKeyByHash(HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	// 先把 Redis 打成故障态，再把 DB 侧当日量灌到「差 1 次打满」，验证降级后仍由 DB 判据把关
	fr.setFailIncr(true)
	if !s.ReserveAPICall(k.ID, limit) {
		t.Fatal("Redis 故障 + DB 未打满却被拒 ⇒ 降级路径把放行也一起掐了（限额挂了＝服务挂了）")
	}
	// 现在 calls_today=1，再放一次到 2，第三次必须被 DB 判据拒绝
	if !s.ReserveAPICall(k.ID, limit) {
		t.Fatal("第二次放行失败（DB 侧应还剩 1 格）")
	}
	if s.ReserveAPICall(k.ID, limit) {
		_, today, _ := readKeyQuota(t, s, k.ID)
		t.Fatalf("Redis 故障时第 %d 次仍放行（calls_today=%d）⇒ 降级成了 fail-open，限额可被网络抖动绕开", limit+1, today)
	}

	// Redis 恢复后：故障期间的 INCR 根本没落到服务端，所以序号从 1 重新开始。
	// 此刻 DB 的 calls_today 已经=limit（打满），若权威源还在 DB 就会**全拒**；
	// 实际能再放行 limit 次，正好证明判定回到了 Redis 的 INCR 序号。
	// ⚠️ 同时这也是记录在案的取舍：一次中途掉线会让当日实际放行达到 limit(DB) + limit(Redis)，
	//    属「限流精度」而非资金缺口。下面的 t.Logf 把这个数字钉成可观测的留痕——
	//    将来若改成「两侧取最大值」，本段会变红，请连同 D-8 的记录一起更新，别只摘掉这条断言。
	fr.setFailIncr(false)
	admittedAfterRecover := 0
	for i := 0; i < limit+1; i++ {
		if s.ReserveAPICall(k.ID, limit) {
			admittedAfterRecover++
		}
	}
	if admittedAfterRecover != limit {
		t.Fatalf("Redis 恢复后合计放行 %d（期望恰好 %d）⇒ 权威源没回到 INCR 序号（%d＝仍按已打满的 DB 判据全拒；>%d＝序号判定失效）",
			admittedAfterRecover, limit, admittedAfterRecover, limit)
	}
	t.Logf("故障窗留痕：DB 档放行 %d + Redis 档放行 %d = %d（限额 %d）⇒ 已知的中途掉线超放，非资金问题",
		limit, admittedAfterRecover, limit+admittedAfterRecover, limit)
}
