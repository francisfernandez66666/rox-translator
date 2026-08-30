// ============ 本文件职责中文说明 ============
// 极简自包含 Redis 客户端（RESP 协议），仅在离线环境无法引入 go-redis 时作为零依赖落地；
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

type conn struct {
	c     net.Conn
	r     *bufio.Reader
	inUse bool
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

func (c *Client) get() (*conn, error) {
	select {
	case cc := <-c.pool:
		return cc, nil
	default:
		return c.dial()
	}
}

func (c *Client) put(cc *conn) {
	if cc == nil {
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

// GetInt 读取整数（未命中/非整数返回 0）。
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
		v, err := nc.readReply()
		if err != nil {
			return err
		}
		// BLPOP 成功返回数组 [key, val]
		if strings.HasPrefix(v, "[]") || v == "" {
			return nil // 超时（空数组）
		}
		// 解析 [key, val]：简单取第二个元素
		if strings.HasPrefix(v, "[") {
			parts := strings.SplitN(v, ",", 2)
			if len(parts) == 2 {
				out = strings.TrimSpace(parts[1])
				out = strings.TrimSuffix(strings.TrimPrefix(out, "\""), "\"")
			}
		} else {
			out = v
		}
		return nil
	})
	if errors.Is(e, errTimeout) {
		return "", nil
	}
	return out, e
}

// ---- 底层 RESP 读写 ----

var errTimeout = errors.New("timeout")

func (cc *conn) do(ctx context.Context, write func(*bufio.Reader) error, read func(*bufio.Reader) error) error {
	if err := cc.c.SetDeadline(deadline(ctx)); err != nil {
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
	return err
}

// readReply 读取一条 RESP 回复，返回字符串形态（整型/状态/批量均转字符串；错误返回 error）。
func (cc *conn) readReply() (string, error) {
	line, err := cc.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 {
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
			return "", err
		}
		if n == -1 {
			return "", nil // nil bulk
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(cc.r, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
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
		return "", fmt.Errorf("redis: 未知响应 %q", line)
	}
}
