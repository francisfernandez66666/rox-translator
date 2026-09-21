// ============ redis.go · 职责说明 ============
// redis 包极简自包含 Redis 客户端实现（RESP 协议）。
// 仅在离线环境无法引入 go-redis 时作为零依赖落地；
// 覆盖本服务所需命令：PING / SETNX / GET / INCR / DECR / EXPIRE / DEL / LLEN /
// RPUSH / LPUSH / RPOP / BLPOP / EXISTS。连接池按租约复用，BLPOP 使用独立连接避免阻塞池。
//
// 设计取舍：仅实现「分布式锁 / 滑动窗口计数 / 信号量」所需原语，不做全量 Redis 支持；
// 命令均为单条往返，复杂事务（如 Lua 脚本）不在范围内——信号量改用 list 令牌桶原子实现，
// 规避了 Lua 依赖。失败统一返回 error，调用方据此降级到进程内实现（单实例兼容）。
// =============================================
package redis

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client 极简 Redis 客户端（带连接池）。
type Client struct {
	addr     string
	password string
	pool     chan *conn
	mu       sync.Mutex
	closed   bool
	dialNet  string
}

// conn 是连接池中的单条底层连接及其读缓冲；inUse 标记防止并发复用。
type conn struct {
	c     net.Conn
	r     *bufio.Reader
	inUse bool
	// dirty 标记「这条连接的字节流已不再可信」：写半途失败、或读到 IO/协议错误（Redis 的
	// -ERR 错误回复不算，它是完整的一条回复，流仍对齐）。
	// ★ 修复（2026-09-22，#56-④ 端到端测试实测发现）：此前 put 无任何健康判定，出错连接原样回池，
	//   服务端只断开一次连接，坏连接就会随池轮转被反复取出，限流/锁/信号量在重启前持续降级为进程内；
	//   数组解析中途出错还会把半截回复留在缓冲里，下一条命令读到上一条回复的尾巴（跨请求串值）。
	//   现在脏连接一律 Close 不回池，下一条命令自动重建（失败一次即自愈）。
	dirty bool
}

// New 创建客户端并预热 minIdle 条连接；addr 为空返回 nil（调用方降级进程内）。
func New(addr, password string) *Client {
	if addr == "" {
		return nil
	}
	c := &Client{
		addr:     addr,
		password: password,
		pool:     make(chan *conn, 16),
		dialNet:  "tcp",
	}
	for i := 0; i < 4; i++ {
		if cc, err := c.dial(); err == nil {
			c.pool <- cc
		}
	}
	return c
}

// dial 建立一条新的 Redis TCP 连接（含可选密码 AUTH 鉴权）。
// 仅当连接池空时被 get 调用；失败返回错误由调用方决定重试。
func (c *Client) dial() (*conn, error) {
	nc, err := net.DialTimeout(c.dialNet, c.addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	cc := &conn{c: nc, r: bufio.NewReader(nc)}
	if c.password != "" {
		if err := cc.writeCmd("AUTH", c.password); err != nil {
			nc.Close()
			return nil, err
		}
		if _, err := cc.readReply(); err != nil {
			nc.Close()
			return nil, err
		}
	}
	return cc, nil
}

// get 从连接池取一条可用连接；池空时新建（dial）。
// 不阻塞：池空立即新拨号，避免高并发下排队拖慢。
func (c *Client) get() (*conn, error) {
	select {
	case cc := <-c.pool:
		return cc, nil
	default:
		return c.dial()
	}
}

// put 把用完的连接归还连接池；池满则直接关闭（避免闲置连接堆积）。
// 脏连接（本次往返出过错）绝不回池：详见 conn.dirty 的说明。
func (c *Client) put(cc *conn) {
	if cc == nil {
		return
	}
	if cc.dirty {
		_ = cc.c.Close()
		return
	}
	select {
	case c.pool <- cc:
	default:
		cc.c.Close()
	}
}

// Ping 探活。
func (c *Client) Ping(ctx context.Context) error {
	cc, err := c.get()
	if err != nil {
		return err
	}
	defer c.put(cc)
	return cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("PING")
	}, func(r *bufio.Reader) error {
		_, err := cc.readReply()
		return err
	})
}

// SetNX 仅当 key 不存在时写入（分布式锁获取）。
func (c *Client) SetNX(ctx context.Context, key, val string, ttl time.Duration) (bool, error) {
	cc, err := c.get()
	if err != nil {
		return false, err
	}
	defer c.put(cc)
	var ok bool
	e := cc.do(ctx, func(r *bufio.Reader) error {
		if ttl > 0 {
			return cc.writeCmd("SET", key, val, "PX", strconv.FormatInt(ttl.Milliseconds(), 10), "NX")
		}
		return cc.writeCmd("SET", key, val, "NX")
	}, func(r *bufio.Reader) error {
		v, err := cc.readReply()
		if err != nil {
			return err
		}
		ok = strings.EqualFold(v, "OK")
		return nil
	})
	return ok, e
}

// Set 无条件写入字符串（覆盖旧值；ttl>0 时带过期）。
func (c *Client) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	cc, err := c.get()
	if err != nil {
		return err
	}
	defer c.put(cc)
	return cc.do(ctx, func(r *bufio.Reader) error {
		if ttl > 0 {
			return cc.writeCmd("SET", key, val, "PX", strconv.FormatInt(ttl.Milliseconds(), 10))
		}
		return cc.writeCmd("SET", key, val)
	}, func(r *bufio.Reader) error {
		_, err := cc.readReply()
		return err
	})
}

// Get 读取字符串（未命中返回 ""）。
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	cc, err := c.get()
	if err != nil {
		return "", err
	}
	defer c.put(cc)
	var out string
	e := cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("GET", key)
	}, func(r *bufio.Reader) error {
		v, err := cc.readReply()
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, e
}

// GetInt 读取整数计数（★ #42 更正 finding ⑤ 的失真注释，代码行为不变）：
// 参数：ctx 控制本次命令；key 计数键。
// 返回：
//   - 键不存在 / 值为空串 → (0, nil)，即「未命中按 0 计」，调用方可直接用于限流比较；
//   - 值不是合法整数（被别的实现写歪、或被手工 SET 成非数字）→ (0, *strconv.NumError)，
//     **不会**静默当 0：限流把脏值当 0 等于放行全部请求，必须让调用方看到错误并按降级口径处理；
//   - 连接/命令失败 → (0, err)。
func (c *Client) GetInt(ctx context.Context, key string) (int64, error) {
	s, err := c.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// Incr 原子自增并返回新值。
func (c *Client) Incr(ctx context.Context, key string) (int64, error) {
	cc, err := c.get()
	if err != nil {
		return 0, err
	}
	defer c.put(cc)
	var out int64
	e := cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("INCR", key)
	}, func(r *bufio.Reader) error {
		v, err := cc.readReply()
		if err != nil {
			return err
		}
		out, err = strconv.ParseInt(v, 10, 64)
		return err
	})
	return out, e
}

// Decr 原子自减。
func (c *Client) Decr(ctx context.Context, key string) (int64, error) {
	cc, err := c.get()
	if err != nil {
		return 0, err
	}
	defer c.put(cc)
	var out int64
	e := cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("DECR", key)
	}, func(r *bufio.Reader) error {
		v, err := cc.readReply()
		if err != nil {
			return err
		}
		out, err = strconv.ParseInt(v, 10, 64)
		return err
	})
	return out, e
}

// Expire 设置过期秒数。
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	cc, err := c.get()
	if err != nil {
		return err
	}
	defer c.put(cc)
	return cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("EXPIRE", key, strconv.FormatInt(int64(ttl.Seconds()), 10))
	}, func(r *bufio.Reader) error {
		_, err := cc.readReply()
		return err
	})
}

// Del 删除 key。
func (c *Client) Del(ctx context.Context, key string) error {
	cc, err := c.get()
	if err != nil {
		return err
	}
	defer c.put(cc)
	return cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("DEL", key)
	}, func(r *bufio.Reader) error {
		_, err := cc.readReply()
		return err
	})
}

// LLen 列表长度。
func (c *Client) LLen(ctx context.Context, key string) (int64, error) {
	cc, err := c.get()
	if err != nil {
		return 0, err
	}
	defer c.put(cc)
	var out int64
	e := cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("LLEN", key)
	}, func(r *bufio.Reader) error {
		v, err := cc.readReply()
		if err != nil {
			return err
		}
		out, err = strconv.ParseInt(v, 10, 64)
		return err
	})
	return out, e
}

// RPush 向列表尾部推入若干值。
func (c *Client) RPush(ctx context.Context, key string, vals ...string) error {
	cc, err := c.get()
	if err != nil {
		return err
	}
	defer c.put(cc)
	args := append([]string{key}, vals...)
	return cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("RPUSH", args...)
	}, func(r *bufio.Reader) error {
		_, err := cc.readReply()
		return err
	})
}

// LPush 向列表头部推入一个值（信号量释放）。
func (c *Client) LPush(ctx context.Context, key, val string) error {
	cc, err := c.get()
	if err != nil {
		return err
	}
	defer c.put(cc)
	return cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("LPUSH", key, val)
	}, func(r *bufio.Reader) error {
		_, err := cc.readReply()
		return err
	})
}

// RPop 弹出列表尾元素（信号量获取）。
func (c *Client) RPop(ctx context.Context, key string) (string, error) {
	cc, err := c.get()
	if err != nil {
		return "", err
	}
	defer c.put(cc)
	var out string
	e := cc.do(ctx, func(r *bufio.Reader) error {
		return cc.writeCmd("RPOP", key)
	}, func(r *bufio.Reader) error {
		v, err := cc.readReply()
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, e
}

// BLPop 阻塞弹出（信号量获取，带超时）；超时返回 ("", nil)。
// 参数：ctx 控制等待上限；key 队列名；timeout 服务端阻塞秒数（写进 BLPOP 命令）。
// 返回：弹出的元素值；无数据（服务端超时/ctx 到期）时为空串。
func (c *Client) BLPop(ctx context.Context, key string, timeout time.Duration) (string, error) {
	nc, err := c.dial()
	if err != nil {
		return "", err
	}
	defer nc.c.Close()
	var out string
	e := nc.do(ctx, func(r *bufio.Reader) error {
		return nc.writeCmd("BLPOP", key, strconv.FormatFloat(timeout.Seconds(), 'f', 1, 64))
	}, func(r *bufio.Reader) error {
		// ★ #42（Redis 报告 finding ④）语义更正：readReply 对 RESP 数组的口径是
		//   「递归解析各元素并返回最后一个非空元素」，因此 BLPOP 的 [key, val] 到这里**已经是 val**，
		//   不是形如 "[key,val]" 的字符串。旧实现在此又补了一段 `if strings.HasPrefix(v, "[")` 的
		//   「按逗号取第二段」解析：该分支对真数组回复恒不成立（死代码），
		//   却会被**字面以 '[' 开头的元素值**命中（如队列消息 "[] 待办" 或 "[a,b] 片段"），
		//   把用户数据按第一个逗号截断——静默改数据比死代码更糟。故整段删除，只按原值返回。
		//   空串即超时：服务端 nil 多回复（*-1）与空数组都在 readReply 折成 ""。
		v, err := nc.readReply()
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	if errors.Is(e, errTimeout) {
		return "", nil
	}
	return out, e
}

// ---- 底层 RESP 读写 ----

var errTimeout = errors.New("timeout")

// do 在单条连接上执行一次完整的 RESP 读写（写命令→读回复），
// 先按 ctx 截止时间设置 socket 超时，避免卡死；任一阶段出错即返回。
func (cc *conn) do(ctx context.Context, write func(*bufio.Reader) error, read func(*bufio.Reader) error) error {
	// ★ 已取消的 ctx 必须立即返回（2026-09-22，#56-④ 实测发现）：此前只靠 SetDeadline 传截止时间，
	//   而「只有 cancel、无 deadline」的 ctx 会走到默认 5 秒兜底超时——请求早就放弃了还白占 5 秒，
	//   高并发降级场景下等于把 Redis 故障放大成线程堆积。此时尚未发出任何字节，流仍对齐，
	//   故**不**置 dirty（连接可以复用）。
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cc.c.SetDeadline(deadline(ctx)); err != nil {
		// SetDeadline 失败只有「连接已被关闭」这一类原因（含本地 Close 与对端断开后的
		// 句柄失效）：这条连接已经废了，标脏交给 put 丢弃、由 get 重建。
		cc.dirty = true
		return err
	}
	if err := write(cc.r); err != nil {
		return err
	}
	if err := read(cc.r); err != nil {
		return err
	}
	return nil
}

// deadline 计算本次操作的 socket 截止时间：优先沿用 ctx 的截止时间，
// 否则默认 5 秒超时（防底层卡死）。
func deadline(ctx context.Context) (t time.Time) {
	if dl, ok := ctx.Deadline(); ok {
		return dl
	}
	return time.Now().Add(5 * time.Second)
}

// writeCmd 编码并发送一条 RESP 命令（数组 + bulk）。
func (cc *conn) writeCmd(cmd string, args ...string) error {
	var sb strings.Builder
	sb.WriteString("*")
	sb.WriteString(strconv.Itoa(len(args) + 1))
	sb.WriteString("\r\n")
	writeBulk := func(s string) {
		sb.WriteString("$")
		sb.WriteString(strconv.Itoa(len(s)))
		sb.WriteString("\r\n")
		sb.WriteString(s)
		sb.WriteString("\r\n")
	}
	writeBulk(cmd)
	for _, a := range args {
		writeBulk(a)
	}
	_, err := cc.c.Write([]byte(sb.String()))
	if err != nil {
		// 写失败可能只送出一半命令：服务端会把它当成一条完整命令回包，
		// 之后所有读取都会错一位 ⇒ 连接必须丢弃。
		cc.dirty = true
	}
	return err
}

// readReply 读取一条 RESP 回复，返回字符串形态（整型/状态/批量均转字符串；错误返回 error）。
//
// 脏连接判定（★ 2026-09-22 修复，见 conn.dirty）：只有 IO/协议畸形错误才置 dirty。
// 服务端的 -ERR 错误回复是**一条完整回复**（字节流仍对齐），连接可以继续用；
// 而读到一半失败（长度声明大于实际内容、数组元素出错、未知类型字节…）会让后续读取全部错位，
// 必须丢弃连接，否则下一条命令会读到上一条回复的尾巴。
func (cc *conn) readReply() (string, error) {
	line, err := cc.r.ReadString('\n')
	if err != nil {
		cc.dirty = true
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 {
		cc.dirty = true
		return "", fmt.Errorf("redis: 空响应")
	}
	switch line[0] {
	case '+':
		return line[1:], nil
	case ':':
		return line[1:], nil
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			cc.dirty = true
			return "", err
		}
		if n == -1 {
			return "", nil // nil bulk
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(cc.r, buf); err != nil {
			cc.dirty = true
			return "", err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			cc.dirty = true
			return "", err
		}
		if n <= 0 {
			return "", nil
		}
		// 聚合数组元素：返回最后一个非空元素的值（BLPOP 返回 [key,val]，取 val）。
		var last string
		for i := 0; i < n; i++ {
			v, err := cc.readReply()
			if err != nil {
				// 元素出错即停止读剩余元素：若不是最后一个（i < n-1），缓冲里就留下了
				// 本条回复未读的尾巴 ⇒ 下一条命令会读到它（跨请求串值），连接必须丢弃。
				// 最后一个元素出错则流仍对齐（-ERR 本身是一条完整回复），可以回池。
				if i < n-1 {
					cc.dirty = true
				}
				return "", err
			}
			if v != "" {
				last = v
			}
		}
		return last, nil
	case '-':
		return "", fmt.Errorf("redis error: %s", line[1:])
	default:
		// 未知类型字节：无法得知该回复占多少字节，流已不可对齐 ⇒ 丢弃连接。
		cc.dirty = true
		return "", fmt.Errorf("redis: 未知响应 %q", line)
	}
}
