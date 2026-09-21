// ============ redis_fake_server_test.go · 职责说明 ============
// ★ #56-④（2026-09-22 全量 UAT 报告 P1「安全敏感包零测试」）：internal/infra/redis 的**端到端语义**断言。
// 协议编解码（见 redis_resp_test.go）正确不代表命令语义正确：本包被限流、
// 分布式信号量（infra/concurrency.semaphore.go 的 redisSem 以 `ok, _ := SetNX(...)` 的口径取返回值）、
// 队列唤醒（notify.go）依赖，一旦「返回值语义」被改坏（例如把 nil 回复当成错误、
// 把错误当成未命中），表现是**限流永远放行**或**信号量泄漏/永久占位**，且不会编译报错。
//
// 本文件用**进程内假 Redis 服务端**（127.0.0.1:0，解析真实 RESP 命令 + 可编程回复）覆盖：
//
//	① SetNX 的三类回复（+OK → true；$-1 / :0 → false；错误 → false + 非 nil error）与出站命令参数；
//	② Get 命中/未命中、Incr/Decr/GetInt 的整数解析与坏回复错误路径；
//	③ RPop/BLPop 的空队列与超时路径（超时必须是 ("", nil) 而非 error）；
//	④ ctx 取消 / 截止时间已过时 do() 必须快速返回，不阻塞到默认 5 秒 socket 超时
//	   （TestBLPopPaths 覆盖「带 deadline」，TestCancelledCtxReturnsImmediately 覆盖「仅 cancel」）；
//	⑤ AUTH 拨号鉴权链路与密码错误的降级；
//	⑥ 失败与降级：服务端断开连接 / 回半截报文 / 回错误回复时，调用方拿到 error 而非 panic 或无限阻塞；
//	   并钉住连接池的**自愈**口径（出错连接标 dirty 并丢弃、绝不回池，见 TestPoolDiscardsConnAfterFailure /
//	   TestPoolSelfHealsAfterOneFailure —— 修复前坏连接会永久随池轮转，限流/锁长期降级为进程内）；
//	⑦ 单例 Init/Get/Enabled/Ping/Availability 的启用与降级口径（进程级状态，用例结束必须还原）。
//
// 仓库约定：不连真实 Redis、不写死外部地址、不引第三方库、本包测试不需要数据库（不读 config）。
// =============================================
package redis

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// connAction 假服务端对某条命令的「附加动作」，用于模拟各类网络故障。
type connAction int

const (
	actNone     connAction = iota // 正常回包
	actClose                      // 不回包直接断开连接（模拟 Redis 崩溃 / LB 空闲切断）
	actHang                       // 永不回包（模拟服务端卡死）
	actTruncate                   // 回半截报文后断开（模拟粘包/截断）
)

// fakeRedis 进程内假 Redis 服务端：解析客户端送来的 RESP 命令，按 handler 回原始报文。
// handler 在持锁状态下调用（seq 为全局第 n 条命令，1 起），便于按调用次序编排回复。
type fakeRedis struct {
	ln   net.Listener
	stop chan struct{} // 关闭以释放 actHang 中的服务协程

	mu      sync.Mutex
	cmds    [][]string
	seq     int
	handler func(cmd []string, seq int) (string, connAction)
	pooled  []net.Conn // 已建立的服务端连接，清理时强制关闭以唤醒阻塞在读上的协程
	closed  bool       // 测试已结束：此后 Accepted 的连接直接被关掉，避免协程卡在永久读上
}

func newFakeRedis(t *testing.T, handler func(cmd []string, seq int) (string, connAction)) *fakeRedis {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听本地端口失败: %v", err)
	}
	s := &fakeRedis{ln: ln, stop: make(chan struct{}), handler: handler}
	var wg sync.WaitGroup
	wg.Add(1) // accept 协程本身计入：保证子协程的 Add 永不与 Wait 并发
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
		close(s.stop)
		_ = ln.Close() // 唤醒 Accept
		s.mu.Lock()
		s.closed = true // 之后 serve 拿到的新连接立即自关（见 serve 开头）
		for _, c := range s.pooled {
			_ = c.Close() // 唤醒仍阻塞在读上的服务协程
		}
		s.mu.Unlock()
		wg.Wait()
	})
	return s
}

// addr 返回假服务端地址（127.0.0.1:随机端口），仅进程内使用。
func (s *fakeRedis) addr() string { return s.ln.Addr().String() }

// calls 返回服务端已记录的命令序列（含 AUTH），用于断言出站命令参数。
func (s *fakeRedis) calls() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]string, len(s.cmds))
	for i, c := range s.cmds {
		out[i] = append([]string(nil), c...)
	}
	return out
}

// lastCmd 返回最后一条命令（断言 wire 口径用）。t 由调用方传入，避免测试对象被后台协程共享。
func (s *fakeRedis) lastCmd(t *testing.T) []string {
	cs := s.calls()
	if len(cs) == 0 {
		t.Fatal("假服务端没有收到任何命令")
	}
	return cs[len(cs)-1]
}

// setHandler 在测试中途更换回复编排（持锁写入，保证与服务协程读到的可见性）。
func (s *fakeRedis) setHandler(h func(cmd []string, seq int) (string, connAction)) {
	s.mu.Lock()
	s.handler = h
	s.mu.Unlock()
}

func (s *fakeRedis) serve(nc net.Conn) {
	// 登记与「测试已结束」的判定必须在同一把锁下完成：否则刚 Accept 到的连接会漏出清理快照，
	// 该协程就会永久阻塞在读上，把整个用例（wg.Wait）拖挂。
	// 协程退出时关闭服务端这一侧：actClose / actTruncate 的「断开」语义靠它落地，正常结束也回收 fd。
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		nc.Close()
		return
	}
	s.pooled = append(s.pooled, nc)
	s.mu.Unlock()
	defer nc.Close()
	r := bufio.NewReader(nc)
	for {
		cmd, err := readRespCommand(r)
		if err != nil {
			return // 客户端关闭或报文非法
		}
		s.mu.Lock()
		s.cmds = append(s.cmds, cmd)
		s.seq++
		h := s.handler
		s.mu.Unlock()
		if h == nil {
			_, _ = nc.Write([]byte("+OK\r\n"))
			continue
		}
		// handler 持锁调用：让「按 seq 编排回复」的测试无需自己加锁。
		s.mu.Lock()
		reply, act := h(cmd, s.seq)
		s.mu.Unlock()
		switch act {
		case actClose:
			return
		case actHang:
			<-s.stop
			return
		case actTruncate:
			_, _ = io.WriteString(nc, reply)
			return
		default:
			if _, err := nc.Write([]byte(reply)); err != nil {
				return
			}
		}
	}
}

// readRespCommand 解析一条 RESP 命令（*<n>\r\n$<len>\r\n<arg>\r\n...）。
func readRespCommand(r *bufio.Reader) ([]string, error) {
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

// fixed 构造一个「所有命令都回同一报文」的 handler。
func fixed(reply string) func(cmd []string, seq int) (string, connAction) {
	return func([]string, int) (string, connAction) { return reply, actNone }
}

// scripted 按命令名回复（未命中映射时回 +OK），便于模拟一个「有状态」的最小 Redis。
func scripted(m map[string]string) func(cmd []string, seq int) (string, connAction) {
	return func(cmd []string, seq int) (string, connAction) {
		if r, ok := m[strings.ToUpper(cmd[0])]; ok {
			return r, actNone
		}
		return "+OK\r\n", actNone
	}
}

// newTestClient 建一个指向假服务端的客户端；测试结束自动取出并关闭池内连接（本包无 Close）。
func newTestClient(t *testing.T, s *fakeRedis, password string) *Client {
	t.Helper()
	c := New(s.addr(), password)
	if c == nil {
		t.Fatal("New 对非空地址不应返回 nil")
	}
	t.Cleanup(func() { drainPool(c) })
	return c
}

// drainPool 取出并关闭池内所有连接（本包没有 Close 方法，避免测试残留 fd 与服务端协程）。
func drainPool(c *Client) {
	if c == nil {
		return
	}
	for {
		select {
		case cc := <-c.pool:
			cc.c.Close()
		default:
			return
		}
	}
}

// testCtx 给一个足够宽（但远小于默认 5 秒兜底）的 ctx，保证断言不被默认超时掩盖。
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// TestSetNXReplySemantics 钉死 SetNX 的返回值语义。
// 生产风险：调用方（infra/concurrency 的 redisSem、distlock）写的是 `ok, _ := SetNX(...)`——
// **丢弃 error**。于是：
//   - 错误回复若被误判为 ok=true ⇒ 同一个锁/槽被两个实例同时持有（超卖）；
//   - nil 回复（key 已存在）若被误判为 error 也无妨（被丢弃），但若误判为 ok=true 就是正确性问题。
func TestSetNXReplySemantics(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		wantOK  bool
		wantErr bool
	}{
		{"抢到锁：+OK", "+OK\r\n", true, false},
		{"已被占用：nil bulk（真实 Redis 的 SET NX 失败回复）", "$-1\r\n", false, false},
		{"已被占用：整数 0", ":0\r\n", false, false},
		{"服务端错误：-BUSYKEY", "-BUSYKEY Target key name already exists.\r\n", false, true},
		{"小写 ok 也须判定为成功（EqualFold 口径）", "+ok\r\n", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newFakeRedis(t, fixed(c.reply))
			cl := newTestClient(t, s, "")
			ok, err := cl.SetNX(testCtx(t), "lock:task", "token-1", time.Minute)
			if (err != nil) != c.wantErr {
				t.Fatalf("err 期望 %v，实得 %v", c.wantErr, err)
			}
			if ok != c.wantOK {
				t.Fatalf("★ 回归：ok 期望 %v 实得 %v（会直接导致锁超卖或永远拿不到锁）", c.wantOK, ok)
			}
			if c.wantErr && ok {
				t.Fatal("出错时绝不能同时报告抢到了锁")
			}
		})
	}
}

// TestSetNXWireArguments 断言 SetNX 的出站命令参数（ttl>0 走 SET ... PX <ms> NX，ttl=0 不带 PX）。
// 生产风险：PX 的毫秒数写成分秒 ⇒ 锁 TTL 放大 1000 倍，实例崩溃后锁长期不被释放（全站卡死）。
func TestSetNXWireArguments(t *testing.T) {
	s := newFakeRedis(t, fixed("+OK\r\n"))
	cl := newTestClient(t, s, "")

	ctx := testCtx(t)
	if _, err := cl.SetNX(ctx, "lock:k", "v1", 90*time.Second); err != nil {
		t.Fatalf("SetNX 失败: %v", err)
	}
	want := []string{"SET", "lock:k", "v1", "PX", "90000", "NX"}
	if got := s.lastCmd(t); !equalArgs(got, want) {
		t.Fatalf("带 TTL 的 SetNX 命令参数错误\n实得: %v\n应为: %v", got, want)
	}

	if _, err := cl.SetNX(ctx, "lock:k", "v2", 0); err != nil {
		t.Fatalf("SetNX(ttl=0) 失败: %v", err)
	}
	want = []string{"SET", "lock:k", "v2", "NX"}
	if got := s.lastCmd(t); !equalArgs(got, want) {
		t.Fatalf("ttl=0 时不应带 PX\n实得: %v\n应为: %v", got, want)
	}
}

// TestGetHitAndMiss Get 命中/未命中的返回口径。
// 生产风险：未命中（nil bulk）必须是 ("", nil)——返回 error 会让上层把「键不存在」误判成
// 「Redis 故障」而逐次降级；反之把错误吞成 ("", nil) 会让限流窗口永远读不到计数（永远放行）。
func TestGetHitAndMiss(t *testing.T) {
	s := newFakeRedis(t, scripted(map[string]string{"GET": "$7\r\nproduct\r\n"}))
	cl := newTestClient(t, s, "")
	ctx := testCtx(t)

	got, err := cl.Get(ctx, "app:name")
	if err != nil || got != "product" {
		t.Fatalf("命中路径应返回 (product, nil)，实得 (%q, %v)", got, err)
	}
	if want := []string{"GET", "app:name"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("GET 命令参数错误: 实得 %v 应为 %v", s.lastCmd(t), want)
	}

	s.setHandler(fixed("$-1\r\n"))
	got, err = cl.Get(ctx, "app:missing")
	if err != nil {
		t.Fatalf("未命中不应返回 error: %v", err)
	}
	if got != "" {
		t.Fatalf("未命中应返回空串，实得 %q", got)
	}

	// 现状记录：零长批量与 nil 批量都归一为空串，调用方无法区分「值为空串」与「键不存在」。
	s.setHandler(fixed("$0\r\n\r\n"))
	empty, err := cl.Get(ctx, "app:empty")
	if err != nil || empty != "" {
		t.Fatalf("零长批量应返回 (\"\", nil)，实得 (%q, %v)", empty, err)
	}

	// 错误回复必须向上抛出，且不能带出脏值。
	s.setHandler(fixed("-ERR wrongtype\r\n"))
	v, err := cl.Get(ctx, "app:bad")
	if err == nil || v != "" {
		t.Fatalf("错误回复必须返回 (\"\", error)，实得 (%q, %v)", v, err)
	}
}

// TestCountersIncrDecrGetInt INCR/DECR/GETINT 的整数解析与坏回复错误路径。
// 生产风险：坏回复（非整数）被吞成 0 ⇒ 限流窗口计数永远为 0 ⇒ 限流永远放行。
func TestCountersIncrDecrGetInt(t *testing.T) {
	s := newFakeRedis(t, scripted(map[string]string{"INCR": ":7\r\n"}))
	cl := newTestClient(t, s, "")
	ctx := testCtx(t)

	n, err := cl.Incr(ctx, "rate:uid:1")
	if err != nil || n != 7 {
		t.Fatalf("Incr 应返回 7，实得 (%d, %v)", n, err)
	}
	if want := []string{"INCR", "rate:uid:1"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("INCR 命令参数错误: %v", s.lastCmd(t))
	}

	s.setHandler(fixed(":-2\r\n"))
	if n, err := cl.Decr(ctx, "rate:uid:1"); err != nil || n != -2 {
		t.Fatalf("Decr 应返回 -2，实得 (%d, %v)", n, err)
	}

	s.setHandler(fixed("$3\r\n421\r\n"))
	if n, err := cl.GetInt(ctx, "rate:uid:1"); err != nil || n != 421 {
		t.Fatalf("GetInt 应解析出 421，实得 (%d, %v)", n, err)
	}

	// 键不存在：0 且无错误（限流按「窗口首次」处理）。
	s.setHandler(fixed("$-1\r\n"))
	if n, err := cl.GetInt(ctx, "rate:uid:1"); err != nil || n != 0 {
		t.Fatalf("未命中时 GetInt 应返回 (0, nil)，实得 (%d, %v)", n, err)
	}

	// ★ 现状：注释宣称「非整数返回 0」，实现是把 strconv 的 error 直接抛给调用方。
	// 这里按现状断言——非整数必须报错，绝不能静默返回 0（静默 0 = 限流放行）。
	s.setHandler(fixed("$3\r\nabc\r\n"))
	if n, err := cl.GetInt(ctx, "rate:uid:1"); err == nil || n != 0 {
		t.Fatalf("非整数值必须返回 error 且值为 0，实得 (%d, %v)", n, err)
	}
	s.setHandler(fixed("+abc\r\n"))
	if n, err := cl.Incr(ctx, "rate:uid:1"); err == nil || n != 0 {
		t.Fatalf("Incr 收到非整数回复必须返回 error，实得 (%d, %v)", n, err)
	}
}

// TestListAndTTLCommandsWire LLEN / RPUSH / LPUSH / RPOP / EXPIRE / DEL 的命令口径与空队列语义。
// 生产风险：
//   - RPop 空队列必须是 ("", nil)：否则信号量把它当成 Redis 故障，多实例并发上限整体失控；
//   - LPUSH 的空串令牌不得被编码成 nil bulk（协议层已断言，这里端到端复核）；
//   - EXPIRE 用秒：单位写错 ⇒ 计数键永不过期（限流永久封禁）或秒级过期（限流形同虚设）。
func TestListAndTTLCommandsWire(t *testing.T) {
	s := newFakeRedis(t, scripted(map[string]string{"LLEN": ":3\r\n", "RPOP": "$-1\r\n"}))
	cl := newTestClient(t, s, "")
	ctx := testCtx(t)

	if n, err := cl.LLen(ctx, "queue:pending"); err != nil || n != 3 {
		t.Fatalf("LLen 应返回 3，实得 (%d, %v)", n, err)
	}
	if err := cl.RPush(ctx, "queue:pending", "1", "2"); err != nil {
		t.Fatalf("RPush 失败: %v", err)
	}
	if want := []string{"RPUSH", "queue:pending", "1", "2"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("RPUSH 参数错误: %v", s.lastCmd(t))
	}

	if err := cl.LPush(ctx, "sem:tokens", ""); err != nil {
		t.Fatalf("LPush 空串令牌失败: %v", err)
	}
	if want := []string{"LPUSH", "sem:tokens", ""}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("空串令牌被错误编码（不得变成 nil bulk）: %v", s.lastCmd(t))
	}

	v, err := cl.RPop(ctx, "sem:tokens")
	if err != nil || v != "" {
		t.Fatalf("★ 回归：RPop 空队列应返回 (\"\", nil)，实得 (%q, %v)", v, err)
	}

	if err := cl.Expire(ctx, "rate:uid:1", 15*time.Minute); err != nil {
		t.Fatalf("Expire 失败: %v", err)
	}
	if want := []string{"EXPIRE", "rate:uid:1", "900"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("EXPIRE 必须以秒为单位: 实得 %v 应为 %v", s.lastCmd(t), want)
	}

	if err := cl.Del(ctx, "lock:task"); err != nil {
		t.Fatalf("Del 失败: %v", err)
	}
	if want := []string{"DEL", "lock:task"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("DEL 参数错误: %v", s.lastCmd(t))
	}
	if err := cl.Del(ctx, ""); err != nil {
		t.Fatalf("Del 空键名应能正常往返: %v", err)
	}
	if want := []string{"DEL", ""}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("空键名编码错误: %v", s.lastCmd(t))
	}
}

// TestBLPopPaths BLPop 的三条路径：拿到数据 / 服务端超时 / 客户端 ctx 超时。
// 生产风险：BLPop 是队列 Wait 与信号量阻塞获取的唯一阻塞点，
// 超时若被当成 error 上抛，唤醒循环会疯狂报错；反之错误被吞成超时，会漏掉整批任务。
func TestBLPopPaths(t *testing.T) {
	s := newFakeRedis(t, scripted(map[string]string{"BLPOP": "*2\r\n$4\r\nwake\r\n$1\r\n7\r\n"}))
	cl := newTestClient(t, s, "")
	ctx := testCtx(t)

	got, err := cl.BLPop(ctx, "wake", 2*time.Second)
	if err != nil {
		t.Fatalf("BLPop 拿到数据时不应报错: %v", err)
	}
	if got != "7" {
		t.Fatalf("BLPop 应取数组 [key,val] 的 val，实得 %q", got)
	}
	if want := []string{"BLPOP", "wake", "2.0"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("BLPOP 超时参数应为秒（1 位小数）: 实得 %v 应为 %v", s.lastCmd(t), want)
	}

	// ★ #42（Redis 报告 finding ④）：元素值**字面以 '[' 开头**时必须原样返回。
	// 旧实现在 BLPop 里又做了一次「HasPrefix("[") → 按第一个逗号取第二段」的伪解析
	// （readReply 早就把 RESP 数组折成末元素了，那段解析对真数组恒不成立），
	// 结果只有载荷本身形如 "[a,b" 时才会命中——把队列里的合法数据静默截断成 "b"。
	// 这类「按猜测二次解析」比死代码更危险，故本用例把原样返回钉死。
	s.setHandler(fixed("*2\r\n$4\r\nwake\r\n$4\r\n[a,b\r\n"))
	got, err = cl.BLPop(ctx, "wake", 2*time.Second)
	if err != nil || got != "[a,b" {
		t.Fatalf("以 '[' 开头的元素值必须原样返回，实得 (%q, %v)", got, err)
	}

	// 服务端自身超时（真实 Redis 回 *-1）：必须返回 ("", nil)，即「本轮无事件」而非错误。
	s.setHandler(fixed("*-1\r\n"))
	got, err = cl.BLPop(ctx, "wake", 2*time.Second)
	if err != nil || got != "" {
		t.Fatalf("服务端超时应返回 (\"\", nil)，实得 (%q, %v)", got, err)
	}

	// 服务端卡住且 ctx 截止时间已过：现状是返回 socket 超时的 error（不会伪装成 ("", nil)）。
	// 断言点是「必须快速返回、不得阻塞到默认 5 秒」，保证调用方能按 ctx 取消退出。
	s.setHandler(func(cmd []string, seq int) (string, connAction) { return "", actHang })
	shortCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	got, err = cl.BLPop(shortCtx, "wake", time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("服务端卡死且 ctx 过期时应返回 error，实得 (%q, nil)", got)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("★ 回归：未沿用 ctx 截止时间，阻塞了 %v（会拖住队列循环/请求协程）", elapsed)
	}
}

// TestCancelledCtxReturnsImmediately ★ 2026-09-22 修复：do() 现在**先查 ctx.Err()** 再落 socket。
// 修复前只有「自带截止时间」的 ctx 能快速返回，「仅 cancel、无 deadline」的取消要等满 5 秒兜底超时——
// 调用方早已放弃，协程却还被占着；Redis 抖动时等于把故障放大成协程堆积。
// 本用例用「服务端永不回包 + 已取消无截止时间的 ctx」钉住快速返回（>1 秒即判回归）。
func TestCancelledCtxReturnsImmediately(t *testing.T) {
	s := newFakeRedis(t, func(cmd []string, seq int) (string, connAction) { return "", actHang })
	cl := newTestClient(t, s, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 只取消，不设截止时间
	start := time.Now()
	if err := cl.Ping(ctx); err == nil {
		t.Fatal("已取消 ctx 下的命令必须返回 error（不得伪装成成功）")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("已取消 ctx 仍等满兜底超时（耗时 %v）：do() 的 ctx.Err() 前置检查失效", elapsed)
	}
}

// TestQueueNotifierSignalWait notify.go 的 Notifier 语义（跨实例唤醒）。
// 生产风险：Signal 写错键名 ⇒ 唤醒信号永远收不到，队列退化为纯轮询（延迟飙升）。
func TestQueueNotifierSignalWait(t *testing.T) {
	s := newFakeRedis(t, scripted(map[string]string{"RPUSH": ":1\r\n", "BLPOP": "*2\r\n$4\r\nwake\r\n$1\r\n1\r\n"}))
	cl := newTestClient(t, s, "")
	n := NewQueueNotifier(cl)
	ctx := testCtx(t)

	if err := n.Signal(ctx); err != nil {
		t.Fatalf("Signal 失败: %v", err)
	}
	if want := []string{"RPUSH", "wake", "1"}; !equalArgs(s.lastCmd(t), want) {
		t.Fatalf("Signal 必须 RPUSH 到 wake 列表: 实得 %v 应为 %v", s.lastCmd(t), want)
	}
	if err := n.Wait(ctx, time.Second); err != nil {
		t.Fatalf("Wait 拿到事件时不应报错: %v", err)
	}
	// 超时（服务端回 *-1）按契约「最长等待 d 后返回」，不得报错。
	s.setHandler(fixed("*-1\r\n"))
	if err := n.Wait(ctx, time.Second); err != nil {
		t.Fatalf("Wait 超时应返回 nil，实得 %v", err)
	}
	// 真实故障（错误回复）必须上抛，供上层记日志并降级为轮询。
	s.setHandler(fixed("-ERR badlist\r\n"))
	if err := n.Wait(ctx, time.Second); err == nil {
		t.Fatal("服务端错误必须从 Wait 上抛")
	}
}

// TestAuthDuringDial 带密码时 dial 必须先 AUTH 并读回复；密码错误 ⇒ 拨号失败 ⇒ 所有命令报错。
// 生产风险：AUTH 回复未读走会让首条命令读到 +OK（协议错位）；AUTH 失败被吞会让客户端「看似已连接」
// 却永久 NOAUTH。
func TestAuthDuringDial(t *testing.T) {
	s := newFakeRedis(t, func(cmd []string, seq int) (string, connAction) {
		if strings.EqualFold(cmd[0], "AUTH") && len(cmd) == 2 && cmd[1] == "s3cr3t" {
			return "+OK\r\n", actNone
		}
		if strings.EqualFold(cmd[0], "AUTH") {
			return "-WRONGPASS invalid password\r\n", actNone
		}
		return "+PONG\r\n", actNone
	})
	cl := newTestClient(t, s, "s3cr3t")
	ctx := testCtx(t)
	if err := cl.Ping(ctx); err != nil {
		t.Fatalf("正确密码下 Ping 应成功: %v", err)
	}
	cs := s.calls()
	if len(cs) == 0 || !strings.EqualFold(cs[0][0], "AUTH") {
		t.Fatalf("带密码的首条命令必须是 AUTH，实得 %v", cs)
	}
	if len(cs[0]) != 2 || cs[0][1] != "s3cr3t" {
		t.Fatalf("AUTH 参数应为 [AUTH <password>]，实得 %v", cs[0])
	}
	// 每条新连接都要 AUTH 一次：池内 4 条预热 + Ping 复用，至少 1 次。
	authN := 0
	for _, c := range cs {
		if strings.EqualFold(c[0], "AUTH") {
			authN++
		}
	}
	if authN == 0 {
		t.Fatal("未发出任何 AUTH")
	}

	// 密码错误：New 不 panic（预热拨号全部失败、池为空），但命令必须返回携带文案的 error。
	bad := newTestClient(t, s, "wrong-password")
	err := bad.Ping(ctx)
	if err == nil {
		t.Fatal("密码错误时 Ping 必须失败")
	}
	if !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("错误应带出服务端文案，实得 %v", err)
	}
}

// TestServerFailurePaths 服务端故障（断开 / 半截报文 / 错误回复）下调用方的表现：
// 只能拿到 error，不得 panic、不得无限阻塞。
func TestServerFailurePaths(t *testing.T) {
	t.Run("服务端收到命令后立即断开", func(t *testing.T) {
		s := newFakeRedis(t, func(cmd []string, seq int) (string, connAction) { return "", actClose })
		cl := newTestClient(t, s, "")
		ctx := testCtx(t)
		if err := cl.Ping(ctx); err == nil {
			t.Fatal("连接被断开时 Ping 必须返回 error")
		}
		// 反复调用不得 panic（每次失败都会丢弃脏连接并重建，见 TestPoolDiscardsConnAfterFailure）。
		for i := 0; i < 5; i++ {
			if _, err := cl.Incr(ctx, "rate:uid:1"); err == nil {
				t.Fatal("服务端持续断开时不得返回成功")
			}
		}
	})

	t.Run("服务端回半截批量报文", func(t *testing.T) {
		s := newFakeRedis(t, func(cmd []string, seq int) (string, connAction) {
			return "$10\r\nabc", actTruncate // 声明 10 字节只给 3 字节，随后断开
		})
		cl := newTestClient(t, s, "")
		ctx := testCtx(t)
		done := make(chan error, 1)
		go func() {
			v, err := cl.Get(ctx, "app:name")
			if err == nil {
				done <- fmt.Errorf("半截报文不应被当成成功，实得 %q", v)
				return
			}
			if v != "" {
				done <- fmt.Errorf("报错时不得返回脏值，实得 %q", v)
				return
			}
			done <- nil
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("半截报文导致调用方阻塞（未拿到 EOF/超时错误）")
		}
	})

	t.Run("未知响应类型", func(t *testing.T) {
		s := newFakeRedis(t, fixed("x 这不是 RESP\r\n"))
		cl := newTestClient(t, s, "")
		ctx := testCtx(t)
		if err := cl.Set(ctx, "k", "v", 0); err == nil || !strings.Contains(err.Error(), "未知响应") {
			t.Fatalf("未知响应类型必须报错并带类型说明，实得 %v", err)
		}
	})
}

// TestPoolDiscardsConnAfterFailure 连接池自愈三段断言（★ 2026-09-22 修复坏连接轮转问题）：
//  1. put 本身**不探活**：服务端静默切断一条连接时，TCP 层看不出来，未发生读写失败就仍会回池
//     （这是刻意的取舍——每次取连接都 PING 等于把每条命令的往返数翻倍）；
//  2. 一旦这条连接上的一次往返失败（readReply/writeCmd 置 dirty），put 必须关闭它且**不再回池**；
//  3. 于是下一次 get 拿到的是新建连接，命令立即恢复成功。
//
// 修复前的实测后果（量化见下一条用例）：坏连接会永久留在池里随池轮转，
// 限流/分布式锁/信号量的调用方会持续看到「命令失败」并降级为进程内实现，
// 多实例一致性（跨副本限流上限、全局并发槽）在进程重启前无法恢复。
func TestPoolDiscardsConnAfterFailure(t *testing.T) {
	s := newFakeRedis(t, fixed("+OK\r\n"))
	cl := newTestClient(t, s, "")
	// 排空 New 预热的连接，保证下面 put/get 命中的就是手工构造的那一条（断言才可确定）。
	drainPool(cl)

	ping := func(cc *conn) error {
		return cc.do(testCtx(t), func(*bufio.Reader) error { return cc.writeCmd("PING") },
			func(*bufio.Reader) error { _, e := cc.readReply(); return e })
	}

	cc, err := cl.dial()
	if err != nil {
		t.Fatalf("dial 失败: %v", err)
	}
	cc.c.Close() // ① 模拟 Redis / LB 空闲切断这条连接
	cl.put(cc)
	got, err := cl.get()
	if err != nil {
		t.Fatalf("get 失败: %v", err)
	}
	if got != cc {
		t.Fatalf("①核对失败：put 不做探活，未出过错的连接应原样回池，实得不同对象")
	}

	if err := ping(got); err == nil {
		t.Fatal("坏连接上的命令必须返回 error，让上层降级")
	}
	if !got.dirty {
		t.Fatal("命令失败后连接未标 dirty：修复失效，坏连接会被 put 回池继续轮转")
	}
	cl.put(got) // ② 脏连接只关闭、不回池
	again, err := cl.get()
	if err != nil {
		t.Fatalf("get 失败: %v", err)
	}
	if again == got {
		t.Fatal("②脏连接被归还进池：下一条命令必然再次失败")
	}
	if err := ping(again); err != nil {
		t.Fatalf("③重建连接后命令仍失败（自愈未生效）: %v", err)
	}
	cl.put(again)
}

// TestPoolSelfHealsAfterOneFailure 端到端量化「失败一次即自愈」：
// 服务端只切断一次（全局第 1 条命令），60 次 Ping 里**最多失败 1 次**，且尾部 30 次必须全绿。
// 修复前实测：约 15/60 失败且永不自愈（同一条已关闭连接反复被取出，EOF / reset / broken pipe 交替）。
func TestPoolSelfHealsAfterOneFailure(t *testing.T) {
	s := newFakeRedis(t, func(cmd []string, seq int) (string, connAction) {
		if seq == 1 {
			return "", actClose // 只切断第一条命令所在的那一次往返
		}
		return "+PONG\r\n", actNone
	})
	cl := newTestClient(t, s, "")
	ctx := testCtx(t)

	const total = 60
	failed := 0
	for i := 0; i < total; i++ {
		if err := cl.Ping(ctx); err != nil {
			failed++
		}
	}
	if failed > 1 {
		t.Fatalf("自愈失败：%d 次 Ping 出错 %d 次（坏连接仍在池里轮转，期望最多 1 次）", total, failed)
	}
	// 尾部连续 30 次全绿：证明被切断的那条连接已经出池，而不是恰好抽到好连接。
	for i := 0; i < 30; i++ {
		if err := cl.Ping(ctx); err != nil {
			t.Fatalf("尾部第 %d 次 Ping 仍失败（连接池未恢复健康）: %v", i+1, err)
		}
	}
}

// TestConcurrentCommandsUnderRace 并发复用连接池：每笔命令必须完整往返（写→读不串位）。
// 生产风险：连接池若把同一连接同时交给两个 goroutine，回复会互相错读——限流计数与锁值串位。
func TestConcurrentCommandsUnderRace(t *testing.T) {
	var mu sync.Mutex
	next := 100
	s := newFakeRedis(t, func(cmd []string, seq int) (string, connAction) {
		mu.Lock()
		v := next
		next++
		mu.Unlock()
		return ":" + strconv.Itoa(v) + "\r\n", actNone
	})
	cl := newTestClient(t, s, "")
	ctx := testCtx(t)

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	vals := make([]int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := cl.Incr(ctx, "rate:uid")
			if err != nil {
				errs <- fmt.Errorf("第 %d 个 goroutine 出错: %w", i, err)
				return
			}
			vals[i] = v
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发调用出错: %v", err)
	}
	// 每笔命令各拿到一个不同的整数 ⇒ 没有两条命令共用同一连接互相错读回复。
	seen := make(map[int64]int, n)
	for i, v := range vals {
		if v < 100 {
			t.Fatalf("第 %d 个 goroutine 读到可疑值 %d", i, v)
		}
		if prev, dup := seen[v]; dup {
			t.Fatalf("★ 回归：第 %d 与第 %d 个 goroutine 读到同一个回复 %d（连接被并发复用，回复串位）", prev, i, v)
		}
		seen[v] = i
	}
	if len(seen) != n {
		t.Fatalf("应有 %d 个不同回复，实得 %d 个", n, len(seen))
	}
}

// TestSingletonInitAndGet 单例入口：空地址 ⇒ 未启用（上层降级），假地址 ⇒ 启用但 Ping 失败。
// 生产风险：Init("") 后 Enabled() 仍为 true ⇒ 各组件不再降级，锁/限流全线报错。
// 注意：本包单例是进程级状态，用例结束必须还原，避免污染同包其他测试。
func TestSingletonInitAndGet(t *testing.T) {
	prev := Get()
	prevAvail := Availability()
	t.Cleanup(func() {
		mu.Lock()
		instance = prev
		availability = prevAvail
		mu.Unlock()
	})

	Init("", "")
	if Enabled() || Get() != nil {
		t.Fatal("空地址必须置 nil 并报告未启用")
	}
	if err := Ping(); err != ErrDisabled {
		t.Fatalf("未启用时 Ping 必须返回 ErrDisabled，实得 %v", err)
	}

	s := newFakeRedis(t, fixed("+PONG\r\n"))
	Init(s.addr(), "")
	// 单例客户端没有 Close：用例结束先排空它的连接池（本清理先于 newFakeRedis 的清理执行），
	// 让服务端协程读到 EOF 正常退出，不把连接泄漏给后续用例。
	t.Cleanup(func() { drainPool(Get()) })
	if !Enabled() || Get() == nil {
		t.Fatal("配置了地址应启用单例")
	}
	if err := Ping(); err != nil {
		t.Fatalf("假服务端下 Ping 应成功: %v", err)
	}
	SetAvailability("redis")
	if got := Availability(); got != "redis" {
		t.Fatalf("Availability 应回显启动闸门结论，实得 %q", got)
	}
}

// equalArgs 比较命令参数（长度与逐元素）。
func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
