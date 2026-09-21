// ============ daily_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-6）：internal/infra/ratelimit 的回归断言。
// 本包承载「API Key 日配额」的跨实例计数（Redis INCR + 次日零点过期），80 行此前零测试。
// 钉住的行为：
//
//	① 未启用 Redis ⇒ Daily() 返回 nil Counter，调用方据此降级到 SQLite/PG 字段口径
//	   （这里若误返回非 nil，配额会在无 Redis 环境静默失效）；
//	② 计数键构造 KeyForAKQuota 的格式（ak:quota:<id>:<date>）——它是 TTL 与配额归属的唯一坐标；
//	③ untilNextMidnight 的窗口边界：必须落在「下一本地零点 ~ +1 分钟缓冲」内，
//	   过期时间算短了会让配额在一天内提前重置（超发），算长了当日不重置（误限）；
//	④ Redis 故障路径：Incr/Get 必须把错误如实上抛（不吞错、不谎报 0 成功）。
//
// 真实 Redis 聚合语义（跨实例 INCR）由 internal/infra/infra_integration_test.go 覆盖，本文件不依赖服务。
// =============================================
package ratelimit

import (
	"testing"
	"time"

	"translator/internal/infra/redis"
)

// TestDailyNilWithoutRedis 未启用 Redis 时返回 nil（调用方走 DB 降级口径）。
func TestDailyNilWithoutRedis(t *testing.T) {
	// 单例未 Init（测试进程内无人设置）⇒ Get()==nil ⇒ Daily() 必须为 nil
	if redis.Get() != nil {
		t.Skip("本进程已启用 Redis 单例，跳过降级断言")
	}
	if c := Daily(); c != nil {
		t.Fatalf("无 Redis 时 Daily() 应返回 nil，实得 %T", c)
	}
}

// TestKeyForAKQuotaFormat 配额键格式（含零 ID、负 ID 与 int64 上界的边界）。
func TestKeyForAKQuotaFormat(t *testing.T) {
	cases := []struct {
		id   int64
		date string
		want string
	}{
		{1, "2026-09-22", "ak:quota:1:2026-09-22"},
		{0, "2026-01-01", "ak:quota:0:2026-01-01"},
		{-5, "2026-09-22", "ak:quota:-5:2026-09-22"},
		{9223372036854775807, "2026-09-22", "ak:quota:9223372036854775807:2026-09-22"},
	}
	for _, c := range cases {
		if got := KeyForAKQuota(c.id, c.date); got != c.want {
			t.Errorf("KeyForAKQuota(%d,%q)=%q，期望 %q", c.id, c.date, got, c.want)
		}
	}
}

// TestUntilNextMidnightWindow 过期窗口必须覆盖到次日零点并带 1 分钟缓冲，且不超过 24h+2min。
func TestUntilNextMidnightWindow(t *testing.T) {
	now := time.Now()
	got := untilNextMidnight()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	want := next.Sub(now) + time.Minute
	// 允许 2 秒执行抖动（两次 time.Now 之间）
	if got < want-2*time.Second || got > want+2*time.Second {
		t.Fatalf("untilNextMidnight=%v，期望约 %v（次日零点 + 1 分钟缓冲）", got, want)
	}
	if got <= 0 || got > 25*time.Hour {
		t.Fatalf("过期时长越界（会提前重置或长期不重置配额）：%v", got)
	}
}

// TestRedisCounterErrorPath Redis 不可达时如实上抛错误（不得静默当作「配额未消耗」）。
func TestRedisCounterErrorPath(t *testing.T) {
	c := &redisCounter{rdb: redis.New("127.0.0.1:1", "")} // 端口 1 必然拒连
	// Incr/Get 内部走 context.Background()，此处直接断言其返回错误且值为 0
	// （吞错返回 (0,nil) 会让上层误判「还有额度」，是最危险的一类降级）。
	if n, err := c.Incr("ak:quota:test"); err == nil {
		t.Fatalf("Redis 不可达时 Incr 不应成功，实得 n=%d", n)
	} else if n != 0 {
		t.Fatalf("Incr 失败时计数应为 0，实得 %d", n)
	}
	if n, err := c.Get("ak:quota:test"); err == nil {
		t.Fatalf("Redis 不可达时 Get 不应成功，实得 n=%d", n)
	}
}

// TestItoaEdges 自实现的整数转字符串（0/负数/进位边界），拼错键会让计数落在错误租户桶上。
func TestItoaEdges(t *testing.T) {
	cases := map[int64]string{0: "0", 1: "1", 9: "9", 10: "10", 99: "99", 100: "100", -3: "-3", 1234567: "1234567"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d)=%q，期望 %q", in, got, want)
		}
	}
}
