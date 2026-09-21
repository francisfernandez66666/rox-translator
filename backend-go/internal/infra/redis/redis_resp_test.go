// ============ redis_resp_test.go · 职责说明 ============
// ★ #56-④（2026-09-22 全量 UAT 报告 P1「安全敏感包零测试」）：internal/infra/redis 的协议层回归断言。
// 本包是**手写的 RESP 协议 Redis 客户端**（不依赖 go-redis），此前全包零测试，
// 却被限流（ratelimit）、分布式信号量（infra/concurrency 的 SetNX/Del/RPop/LPush）、
// 队列唤醒（notify）等关键链路依赖。协议编解码写错不会编译报错，只会表现为
// 生产环境「限流永远放行」「锁永远拿不到」「信号量泄漏」，因此必须有逐字节断言兜住。
//
// 本文件只测协议层（纯内存数据源，不触网）：
//
//	① writeCmd 出站编码：`*<n>\r\n$<len>\r\n<arg>\r\n` 的逐字节口径；
//	   重点钉死「多字节（中文）参数的 $len 必须是字节数而非字符数」——这是本类代码最典型的坑，
//	   错了会让 Redis 端把参数截断/粘包，表现为 SETNX 永远失败或读到串位的值。
//	② readReply 入站解析：简单状态 / 整数 / 批量 / 空批量（RPop、BLPop 无数据的关键路径）/
//	   错误回复 / 多行批量 / 各类畸形报文，全部要求「返回 error 而非 panic 或半读卡死」。
//	③ deadline(ctx) 的 socket 超时口径（沿用 ctx 截止时间，否则 5 秒兜底）。
//	④ 脏连接判定：IO/协议畸形错误必须把连接标脏（连接池据此丢弃而非回池），
//	   服务端的 -ERR 错误回复则不得标脏（它是完整回复，流仍对齐，回池才不会把业务错误放大成重拨风暴）。
//	⑤ New 的空地址/不可达地址构造路径不得 panic。
//
// 端到端语义（假 Redis 服务）与失败降级见同包 redis_fake_server_test.go。
// 仓库约定：不连真实 Redis、不写死外部地址；只用 net.Pipe 与内存 Reader；不引第三方库。
// =============================================
package redis

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// writeCmdOnPipe 在 net.Pipe 内存连接上执行一次 writeCmd，返回对端收到的**原始字节**。
// net.Pipe 是无缓冲同步管道：Write 只有在对端读走全部字节后才返回，故「写完即 Close + 对端 ReadAll 到 EOF」
// 能确定性地拿到完整报文，不需要 sleep 或猜测长度。
func writeCmdOnPipe(t *testing.T, cmd string, args ...string) []byte {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		cc := &conn{c: client, r: bufio.NewReader(client)}
		werr := cc.writeCmd(cmd, args...)
		// 先交付数据再关闭，保证对端 ReadAll 以 EOF 表示「本条命令发送完毕」。
		client.Close()
		done <- werr
	}()
	data, rerr := io.ReadAll(server)
	server.Close()
	if werr := <-done; werr != nil {
		t.Fatalf("writeCmd 返回错误: %v", werr)
	}
	if rerr != nil {
		t.Fatalf("读取对端字节失败: %v", rerr)
	}
	return data
}

// readerConn 用内存字节流构造一条只用于 readReply 的连接。
// readReply 只触碰 cc.r（不碰 cc.c），因此 c 留空即可，省去 pipe 的 goroutine 开销。
func readerConn(raw string) *conn {
	return &conn{r: bufio.NewReader(strings.NewReader(raw))}
}

// TestWriteCmdSingleCommand 无参命令必须是标准一元数组：*1\r\n$4\r\nPING\r\n。
// 断言的生产风险：数组头计数写错（如漏掉命令本体）会让 Redis 直接拒命令，PING 健康检查恒失败。
func TestWriteCmdSingleCommand(t *testing.T) {
	got := writeCmdOnPipe(t, "PING")
	want := "*1\r\n$4\r\nPING\r\n"
	if string(got) != want {
		t.Fatalf("出站报文不符\n实得: %q\n应为: %q", string(got), want)
	}
}

// TestWriteCmdArgsWithSpace 含空格的参数必须靠 $len 界定，禁止被拆成两个元素。
// 断言的生产风险：若误用空格分隔编码，SET 的 value 会被截断到第一个空格，锁值/令牌内容错位。
func TestWriteCmdArgsWithSpace(t *testing.T) {
	got := string(writeCmdOnPipe(t, "SET", "k 1", "a b c"))
	want := "*3\r\n$3\r\nSET\r\n$3\r\nk 1\r\n$5\r\na b c\r\n"
	if got != want {
		t.Fatalf("空格参数编码错误\n实得: %q\n应为: %q", got, want)
	}
}

// TestWriteCmdChineseArgLengthIsBytes 多字节（中文）参数：$len 必须是**字节数**而不是字符数。
// 断言的生产风险：一旦改用 utf8.RuneCountInString 之类的字符计数，Redis 会按声明长度少读字节，
// 剩余字节留在流里造成粘包——之后所有命令都会读到上一条的尾巴（限流计数串位、锁误判）。
func TestWriteCmdChineseArgLengthIsBytes(t *testing.T) {
	val := "中文值" // 3 个汉字 = 9 字节
	if utf8.RuneCountInString(val) == len(val) {
		t.Fatal("用例前提失效：样本需保证字节数 ≠ 字符数")
	}
	got := string(writeCmdOnPipe(t, "SET", "锁键", val))
	want := "*3\r\n$3\r\nSET\r\n$" + strconv.Itoa(len("锁键")) + "\r\n锁键\r\n$" + strconv.Itoa(len(val)) + "\r\n" + val + "\r\n"
	if got != want {
		t.Fatalf("中文参数编码错误（$len 必须是字节数）\n实得: %q\n应为: %q", got, want)
	}
	// 进一步钉死：报文里出现的是 9 而非 3（字节数口径）。
	if !strings.Contains(got, "$9\r\n"+val) {
		t.Fatalf("未见按字节数 9 编码的中文参数: %q", got)
	}
	if strings.Contains(got, "$3\r\n"+val) {
		t.Fatalf("★ 回归：中文参数按字符数（$3）编码，会造成 RESP 粘包")
	}
}

// TestWriteCmdEmptyAndNoArgs 空串参数与「只有命令、无参数」的边界。
// 断言的生产风险：DEL ""、LPUSH key "" 这类调用若把空串编码成 $-1（nil bulk），
// Redis 会报「wrong number of arguments」，信号量释放路径就会泄漏槽位。
func TestWriteCmdEmptyAndNoArgs(t *testing.T) {
	got := string(writeCmdOnPipe(t, "SET", "", ""))
	want := "*3\r\n$3\r\nSET\r\n$0\r\n\r\n$0\r\n\r\n"
	if got != want {
		t.Fatalf("空串参数必须编码为 $0，不得写成 $-1\n实得: %q\n应为: %q", got, want)
	}

	got2 := string(writeCmdOnPipe(t, "PING"))
	if !strings.HasPrefix(got2, "*1\r\n") {
		t.Fatalf("无参命令的元素计数应为 1（命令本体），实得 %q", got2)
	}
}

// TestWriteCmdArgCountHeader 数组头计数 = 1（命令名）+ len(args)。
// 断言的生产风险：RPUSH 多值时计数少 1 会让最后一个值被当成新命令头，整条连接协议错位。
func TestWriteCmdArgCountHeader(t *testing.T) {
	got := string(writeCmdOnPipe(t, "RPUSH", "wake", "1", "2", "3"))
	want := "*5\r\n$5\r\nRPUSH\r\n$4\r\nwake\r\n$1\r\n1\r\n$1\r\n2\r\n$1\r\n3\r\n"
	if got != want {
		t.Fatalf("RPUSH 多值编码错误\n实得: %q\n应为: %q", got, want)
	}
}

// TestReadReplyBasicTypes readReply 对各类标准回复的解析口径（全部转字符串形态）。
// 断言的生产风险：
//   - "+OK"→"OK" 是 SetNX 判定「抢到锁」的唯一依据；
//   - "$-1"→("" , nil) 是 RPop/BLPop「无数据」的关键路径，若误判为 error，
//     信号量空队列会被上层当成 Redis 故障而逐次降级，多实例上限失效；
//   - 多行批量取「最后一个非空元素」是 BLPOP [key,val] 取 val 的实现约定。
func TestReadReplyBasicTypes(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"简单状态", "+OK\r\n", "OK", false},
		{"正整数", ":5\r\n", "5", false},
		{"负整数", ":-3\r\n", "-3", false},
		{"零（SET NX 失败常见回复）", ":0\r\n", "0", false},
		{"批量字符串", "$7\r\nproduct\r\n", "product", false},
		{"空批量 nil（未命中/无数据）", "$-1\r\n", "", false},
		{"零长批量", "$0\r\n\r\n", "", false},
		{"多行批量取最后非空元素", "*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n", "bar", false},
		{"数组套整数", "*1\r\n:42\r\n", "42", false},
		{"空数组", "*0\r\n", "", false},
		{"nil 数组（Redis BLPOP 超时）", "*-1\r\n", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cc := readerConn(c.raw)
			got, err := cc.readReply()
			if c.wantErr && err == nil {
				t.Fatalf("应返回错误，实得 (%q, nil)", got)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("不应报错: %v", err)
			}
			if got != c.want {
				t.Fatalf("解析值不符: 实得 %q 应为 %q", got, c.want)
			}
		})
	}
}

// TestReadReplyErrorCarriesMessageAndKeepsStreamAligned 错误回复：必须带出服务端文案，
// 且**不能把连接留在半读状态**（下一回复要能正常读到）。
// 断言的生产风险：错误文案被吞（如 AUTH 失败只报「连接失败」）会让运维无法定位；
// 残留未读字节则让后续所有命令读到错位数据。
func TestReadReplyErrorCarriesMessageAndKeepsStreamAligned(t *testing.T) {
	cc := readerConn("-ERR unknown command 'BLAHH'\r\n+PONG\r\n")
	_, err := cc.readReply()
	if err == nil {
		t.Fatal("错误回复必须返回非 nil error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("错误信息应带出服务端文案，实得: %v", err)
	}
	next, err := cc.readReply()
	if err != nil {
		t.Fatalf("出错后连接应仍可继续读取，实得错误: %v", err)
	}
	if next != "PONG" {
		t.Fatalf("出错后下一条回复错位: 实得 %q 应为 %q", next, "PONG")
	}
	if cc.r.Buffered() != 0 {
		t.Fatalf("读完两条回复后不应有残留字节，实得 %d", cc.r.Buffered())
	}
}

// TestReadReplyElementErrorMarksConnDirty 数组解析中途出错 ⇒ 剩余元素留在读缓冲里，
// 因此**必须**把该连接标成脏（put 时关闭、不回池）。
// 生产风险：标脏前，do() 仍会把这条连接归还连接池，下一条命令读到上一条回复的尾巴
// （跨请求数据串位：限流计数错位、锁值被别的 key 顶替、BLPOP 拿到别的队列的消息）。
// 这里同时保留「缓冲确有残留」的实测断言：它正是必须丢弃连接的原因，不是可以补救的状态。
func TestReadReplyElementErrorMarksConnDirty(t *testing.T) {
	cc := readerConn("*2\r\n-ERR boom\r\n$3\r\nfoo\r\n")
	if cc.dirty {
		t.Fatal("构造出来的连接不该天生为脏")
	}
	got, err := cc.readReply()
	if err == nil {
		t.Fatalf("数组元素出错应向上返回 error，实得 (%q, nil)", got)
	}
	if got != "" {
		t.Fatalf("出错时值应为空串，实得 %q", got)
	}
	if !cc.dirty {
		t.Fatal("元素级出错未标脏：该连接会被 put 回池，下一条命令必然读到残留尾巴")
	}
	if left, _ := cc.r.Peek(4); string(left) != "$3\r\n" {
		t.Fatalf("残留核对失败：未读尾巴应以 %q 开头，实得 %q", "$3\r\n", string(left))
	}
	// 残留确实可被读走（这就是「串位」的可观测表现）——所以只能丢连接，不能只靠调用方小心。
	stale, err := cc.readReply()
	if err != nil || stale != "foo" {
		t.Fatalf("残留读取应为上一条回复的尾巴 foo，实得 (%q, %v)", stale, err)
	}
}

// TestReadReplyErrorReplyKeepsConnClean 服务端的 -ERR 错误回复是**一条完整回复**：
// 字节流仍对齐，连接不得标脏（否则 AUTH 失败、WRONGTYPE 这类正常业务错误会把连接池打空，
// 每条命令都重新拨号，等于把协议错误升级成性能故障）。
func TestReadReplyErrorReplyKeepsConnClean(t *testing.T) {
	cc := readerConn("-WRONGTYPE Operation against a key\r\n+OK\r\n")
	if _, err := cc.readReply(); err == nil {
		t.Fatal("错误回复必须向上返回 error")
	}
	if cc.dirty {
		t.Fatal("错误回复不该标脏：连接读完这条回复后仍可继续使用")
	}
	next, err := cc.readReply()
	if err != nil || next != "OK" {
		t.Fatalf("错误回复后同一条连接应能继续读，实得 (%q, %v)", next, err)
	}
}

// TestReadReplyMalformedReplies 畸形/半截报文一律返回 error，不得 panic、不得无限阻塞。
// 断言的生产风险：半截批量长度若被当成合法值返回，限流会拿到错乱的计数甚至把空值当命中。
func TestReadReplyMalformedReplies(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"空行（无类型字节）", "\r\n"},
		{"未知类型字节", "hello\r\n"},
		{"批量长度非数字", "$abc\r\n"},
		{"数组长度非数字", "*xyz\r\n"},
		{"批量声明长度大于实际内容（半截）", "$10\r\nab\r\n"},
		{"批量缺少结束 CRLF", "$2\r\nab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cc := readerConn(c.raw)
			done := make(chan struct{})
			var got string
			var err error
			go func() {
				defer close(done)
				got, err = cc.readReply()
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("readReply 在半截报文上阻塞未返回（会拖死调用方协程）")
			}
			if err == nil {
				t.Fatalf("畸形报文应报错，实得 (%q, nil)", got)
			}
			if got != "" {
				t.Fatalf("报错时不应返回可用值，实得 %q", got)
			}
		})
	}
}

// TestDeadlineUsesCtxOrFallback deadline(ctx)：优先沿用 ctx 截止时间，否则 5 秒兜底。
// 断言的生产风险：兜底被改没了 ⇒ 网络半开时协程永久挂起；ctx 截止时间被忽略 ⇒ 上层超时形同虚设。
func TestDeadlineUsesCtxOrFallback(t *testing.T) {
	// 用较远的未来时间，避免机器负载让截止时间在断言途中过期而引入偶发失败。
	want := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), want)
	defer cancel()
	if got := deadline(ctx); !got.Equal(want) {
		t.Fatalf("应沿用 ctx 截止时间: 实得 %v 应为 %v", got, want)
	}

	before := time.Now()
	got := deadline(context.Background())
	if d := time.Until(got); d < 4*time.Second || d > 6*time.Second {
		t.Fatalf("无 ctx 截止时间时应 5 秒兜底，实得 %v（起点 %v）", d, before)
	}

	// 已取消的 ctx 其 Deadline() 仍返回原始截止时间（取消不改截止时间），所以
	// deadline() 本身对「仅 cancel」的 ctx 给不出快速失败——那一半由 do() 的前置 ctx.Err() 检查负责
	// （2026-09-22 修复；端到端断言见 redis_fake_server_test.go 的 TestCancelledCtxReturnsImmediately）。
	// 这里只验证「带已过期截止时间」的 ctx 能让 socket 立即失败。
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	cancel()
	if got := deadline(expired); !got.Before(time.Now()) {
		t.Fatalf("已过期 ctx 的截止时间应在过去（do() 据此让 socket 立即失败而非阻塞）: 实得 %v", got)
	}
	if got := deadline(ctx); got.Before(time.Now()) {
		t.Fatalf("仅取消（cancel）不改变截止时间，实得 %v 应仍是原始 %v", got, want)
	}
}

// TestNewRejectsEmptyAddrAndSurvivesUnreachable 构造路径：addr 为空返回 nil（上层据此降级进程内），
// 不可达地址返回非 nil 客户端但所有调用返回 error——都不得 panic。
func TestNewRejectsEmptyAddrAndSurvivesUnreachable(t *testing.T) {
	if c := New("", ""); c != nil {
		t.Fatal("addr 为空必须返回 nil，让上层降级为进程内实现")
	}
	if c := New("", "any-password"); c != nil {
		t.Fatal("带密码但 addr 为空同样必须返回 nil")
	}

	// 127.0.0.1:1 是进程内保留端口，不会有人监听（不依赖外部地址）。
	c := New("127.0.0.1:1", "")
	if c == nil {
		t.Fatal("非空地址即使拨号失败也应返回客户端（由调用方逐次降级）")
	}
	defer func() {
		// 本包无 Close：把池内连接逐个取出关闭，避免测试残留 fd。
		for {
			select {
			case cc := <-c.pool:
				cc.c.Close()
			default:
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.Ping(ctx); err == nil {
		t.Fatal("不可达地址的 Ping 必须返回 error")
	}
	if ok, err := c.SetNX(ctx, "k", "v", time.Second); ok || err == nil {
		t.Fatalf("不可达地址的 SetNX 必须返回 (false, error)，实得 (%v, %v)", ok, err)
	}
}
