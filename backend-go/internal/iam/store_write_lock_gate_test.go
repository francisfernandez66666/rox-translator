// 本文件锁 P1②（2026-10-11 慢 SQL 批）：iam 写互斥的**按方言收窄**。
// 两条腿：
//  1. 守卫级——withWriteMu 在 sqlite 档真持锁、在 postgres 档真放锁（用"阻塞体在跑时
//     TryLock 能不能拿到"判据，这是行为级实跑，不是读源码猜）；
//  2. 接线级——s.mu.Lock() 全文件只许出现在 withWriteMu 内部一处，三条写入口都必须
//     经 withWriteMu（谁再绕过它直接摸锁，判据当场红——"收窄被写回"是这一族唯一退化形态）。
//
// 方言按 AGENTS §一·4 自钉（config.Default() 有写全局 config.C 的副作用，用完必须还原）。
package iam

import (
	"os"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// pinDialect 把全局方言钉成指定值并在用例结束后还原。
func pinDialect(t *testing.T, driver string) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = driver
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	if got := db.CurrentDialect().IsPostgres(); got != (driver == "postgres") {
		t.Fatalf("方言自钉失败：driver=%s IsPostgres=%v（后面两条判据都会失去射程）", driver, got)
	}
}

// TestWithWriteMuSerializesOnlyOnSqlite 判据＝锁真的在 fn 执行期间被持有/不被持有。
// sqlite：entered 时锁必在手里 ⇒ TryLock 拿不到；postgres：fn 无锁跑 ⇒ TryLock 立刻拿到。
func TestWithWriteMuSerializesOnlyOnSqlite(t *testing.T) {
	t.Run("sqlite 档持锁", func(t *testing.T) {
		pinDialect(t, "sqlite")
		s := &Store{}
		entered := make(chan struct{})
		release := make(chan struct{})
		go s.withWriteMu(func() { close(entered); <-release })
		<-entered // entered 在 Lock 之后关闭：观测到时锁必已持有
		if s.mu.TryLock() {
			s.mu.Unlock()
			close(release)
			t.Fatal("sqlite 档写互斥没持锁 ⇒ 单写者模型下并发写重新裸奔（P1② 只该收窄 PG）")
		}
		close(release)
	})
	t.Run("postgres 档放锁", func(t *testing.T) {
		pinDialect(t, "postgres")
		s := &Store{}
		entered := make(chan struct{})
		release := make(chan struct{})
		go s.withWriteMu(func() { close(entered); <-release })
		<-entered
		if !s.mu.TryLock() {
			t.Fatal("postgres 档仍被全局互斥串行化 ⇒ 现网「持锁等库」一条队拖全部写（收窄没生效）")
		}
		s.mu.Unlock()
		close(release)
	})
}

// TestWriteEntriesAllGoThroughWithWriteMu 接线级静态锁（读实文件，判派生式形态而非具体行号）。
func TestWriteEntriesAllGoThroughWithWriteMu(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("读 store.go 失败: %v", err)
	}
	body := string(src)
	// 直接摸锁的地方只许有 withWriteMu 内部那一只。
	// 计数锚定「行首 tab + 调用」形态：说明注释里的同一字面量（本文件头就有）不许算进射程
	// ——AGENTS「负向锁误伤说明注释」同族，判据按真调用行的排版数。
	if n := strings.Count(body, "\n\ts.mu.Lock()"); n != 1 {
		t.Fatalf("s.mu.Lock() 真调用期望恰 1 处（withWriteMu 内部）实际 %d 处 ⇒ 绕过方言判据的写入口复活", n)
	}
	// 三条写入口必须都经 withWriteMu。
	for _, fn := range []string{"func (s *Store) write(", "func (s *Store) execW(", "func (s *Store) execWID("} {
		i := strings.Index(body, fn)
		if i < 0 {
			t.Fatalf("找不到写入口 %s（接线锁锚点失效）", fn)
		}
		j := strings.Index(body[i:], "\nfunc ")
		if j < 0 {
			j = len(body) - i
		}
		if !strings.Contains(body[i:i+j], "withWriteMu(") {
			t.Fatalf("写入口 %s 没经 withWriteMu ⇒ 它绕过按方言收窄（PG 档会被重新串行化或 SQLite 档裸奔）", fn)
		}
	}
}
