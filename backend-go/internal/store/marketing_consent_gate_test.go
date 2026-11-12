// ============ marketing_consent_gate_test.go · 职责说明 ============
// 三期 W5 外联合规域的落库锁（真 SQLite 文件库驱动）：
//   1. 占位幂等：同 (租户,客户,节点) 并发占位只有一个赢家（ux_outbound_once 结构保证）；
//   2. 终态只许从 claimed 推进（败方/重复回写不覆盖赢家结果）；
//   3. 订阅状态机：登记→改态→幂等；抑制名单 email/域名双匹配 + 重复加幂等；
//   4. 频限计数口径：当日 claimed+sent 计数、跨日不累计；
//   5. 反模式守卫：不许先 COUNT 预检、不许按冲突字符串分支（源码级）。
// ================================================
package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// newOutboundTestStore 文件库建 Store 并跑外联合规域迁移（内存库多连接各自建库会假绿）。
func newOutboundTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "outbound.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := New(conn)
	if err != nil {
		t.Fatalf("Store 迁移失败: %v", err)
	}
	return st
}

// TestOutboundClaimOnceAndTerminal 并发占位唯一赢家 + 终态只从 claimed 推进。
func TestOutboundClaimOnceAndTerminal(t *testing.T) {
	st := newOutboundTestStore(t)
	const tid = int64(7)
	// 并发 16 个占位：恰 1 个赢家（A4/M0246 的行为腿）。
	var winner atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if id, first, err := st.ClaimOutboundSend(tid, "a@b.com", "n1"); err == nil && first {
				winner.Store(id)
			}
		}()
	}
	wg.Wait()
	if winner.Load() == 0 {
		t.Fatal("并发占位没有赢家：ux_outbound_once 唯一键未生效或占位口坏了")
	}
	// 赢家终态推进；随后重复占位恒 false；终态不可被第二次回写覆盖。
	id := winner.Load()
	if err := st.MarkOutboundSent(tid, id); err != nil {
		t.Fatalf("MarkOutboundSent: %v", err)
	}
	if _, first, _ := st.ClaimOutboundSend(tid, "a@b.com", "n1"); first {
		t.Fatal("已终态的 (客户,节点) 再次占位成功：唯一键失守，会重复发信")
	}
	if err := st.MarkOutboundFailed(tid, id, "retry"); err != nil {
		t.Fatalf("MarkOutboundFailed 不该报错: %v", err)
	}
	rows, _ := st.ListOutboundSends(tid, 10)
	if len(rows) != 1 || rows[0].Status != OutboundStatusSent {
		t.Fatalf("终态被覆盖成 failed（期望保持 sent）：rows=%+v", rows)
	}
}

// TestConsentAndSuppression 订阅两态 + 抑制名单双匹配。
func TestConsentAndSuppression(t *testing.T) {
	st := newOutboundTestStore(t)
	const tid = int64(7)
	// 从未订阅＝未授权。
	if _, found, _ := st.ConsentStatus(tid, "c@d.com"); found {
		t.Fatal("无订阅记录却查到状态：把「没说过」当成「同意过」")
	}
	if err := st.SetConsentStatus(tid, "c@d.com", ConsentOptedIn, "ui"); err != nil {
		t.Fatalf("opt-in: %v", err)
	}
	// 改态幂等（覆盖旧态，不撞唯一键）。
	if err := st.SetConsentStatus(tid, "c@d.com", ConsentOptedOut, "ui2"); err != nil {
		t.Fatalf("opt-out 改态: %v", err)
	}
	if v, found, _ := st.ConsentStatus(tid, "c@d.com"); !found || v != ConsentOptedOut {
		t.Fatalf("改态未生效: found=%v v=%q", found, v)
	}
	// 抑制名单：邮箱命中 + 同域其它邮箱经域名命中 + 重复加幂等。
	if err := st.AddSuppression(tid, "x@evil.com", "email", "t"); err != nil {
		t.Fatalf("AddSuppression: %v", err)
	}
	if err := st.AddSuppression(tid, "evil.com", "domain", "t"); err != nil {
		t.Fatalf("AddSuppression domain: %v", err)
	}
	if err := st.AddSuppression(tid, "evil.com", "domain", "t2"); err != nil {
		t.Fatalf("重复加名单应幂等: %v", err)
	}
	for _, e := range []string{"x@evil.com", "y@evil.com"} {
		if ok, _ := st.InSuppression(tid, e); !ok {
			t.Fatalf("%s 未命中抑制名单", e)
		}
	}
}

// TestDailySendCountDayScoped 频限计数只算当日 claimed+sent（UTC 日界）。
func TestDailySendCountDayScoped(t *testing.T) {
	st := newOutboundTestStore(t)
	const tid = int64(7)
	st.SetConsentStatus(tid, "f@g.com", ConsentOptedIn, "ui")
	id1, first, err := st.ClaimOutboundSend(tid, "f@g.com", "n1")
	if err != nil || !first {
		t.Fatalf("首次占位: id=%d first=%v err=%v", id1, first, err)
	}
	cnt, err := st.DailySendCount(tid, "f@g.com")
	if err != nil || cnt != 1 {
		t.Fatalf("当日计数应含 claimed：cnt=%d err=%v", cnt, err)
	}
	if err := st.MarkOutboundSent(tid, id1); err != nil {
		t.Fatalf("MarkOutboundSent: %v", err)
	}
	if cnt, _ := st.DailySendCount(tid, "f@g.com"); cnt != 1 {
		t.Fatalf("sent+claimed 同行不重复计：cnt=%d", cnt)
	}
	// 不同客户/不同节点互不影响计数对象。
	if _, first, _ = st.ClaimOutboundSend(tid, "other@g.com", "n1"); !first {
		t.Fatal("其它客户首占位失败")
	}
	if cnt, _ := st.DailySendCount(tid, "f@g.com"); cnt != 1 {
		t.Fatalf("他人占位串进了本客户计数：cnt=%d", cnt)
	}
}

// TestPurgeCustomerOutboundTrail GDPR 删除请求权（§8 C4）：擦发送/订阅留痕，抑制名单保留。
func TestPurgeCustomerOutboundTrail(t *testing.T) {
	st := newOutboundTestStore(t)
	const tid = int64(7)
	_ = st.SetConsentStatus(tid, "g@h.com", ConsentOptedIn, "ui")
	if _, first, _ := st.ClaimOutboundSend(tid, "g@h.com", "n1"); !first {
		t.Fatal("占位失败")
	}
	_ = st.AddSuppression(tid, "g@h.com", "email", "gdpr")
	purged, err := st.PurgeCustomerOutboundTrail(tid, "g@h.com")
	if err != nil || purged < 2 {
		t.Fatalf("擦除应至少删 2 行（发送+订阅），实得 purged=%d err=%v", purged, err)
	}
	if _, found, _ := st.ConsentStatus(tid, "g@h.com"); found {
		t.Fatal("擦除后订阅行仍在：删除请求权没兑现")
	}
	sends, _ := st.ListOutboundSends(tid, 10)
	if len(sends) != 0 {
		t.Fatalf("擦除后发送留痕仍在: %+v", sends)
	}
	if ok, _ := st.InSuppression(tid, "g@h.com"); !ok {
		t.Fatal("抑制名单被删除请求洗掉：下游会重新外联此人（证据必须保留）")
	}
	// 跨租户隔离：他租户同邮箱的行不许被擦（这里只拨租户 7 的擦除，租户 8 的行必须原样）。
	_ = st.SetConsentStatus(8, "g@h.com", ConsentOptedIn, "ui")
	if _, err := st.PurgeCustomerOutboundTrail(tid, "g@h.com"); err != nil {
		t.Fatalf("二次擦除: %v", err)
	}
	if _, found, _ := st.ConsentStatus(8, "g@h.com"); !found {
		t.Fatal("跨租户擦除：他租户的订阅行被误删")
	}
}

// TestOutboundNoAntiPatterns 反模式守卫：先 COUNT 预检 / 冲突字符串分支（源码级）。
func TestOutboundNoAntiPatterns(t *testing.T) {
	for _, f := range []string{"marketing_consent.go", "marketing_consent_migrate.go"} {
		b, err := os.ReadFile(filepath.Join(".", f))
		if err != nil {
			t.Fatalf("读 %s: %v", f, err)
		}
		src := string(b)
		for _, bad := range []string{"23505", "UNIQUE constraint failed", "duplicate key"} {
			if strings.Contains(src, bad) {
				t.Fatalf("%s 出现冲突字符串分支 %q：幂等必须走 db.IsUniqueViolation 双方言判定", f, bad)
			}
		}
	}
}
