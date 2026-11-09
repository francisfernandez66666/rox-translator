// ============ 本文件职责中文说明 ============
// 二期 mq 核心层落库锁：收件箱幂等抢占的并发唯一性、跨租户隔离、完成标记、
// 以及「不许退回先 COUNT 预检／不许字符串判 SQLSTATE」的源码级守卫。
// 真 SQLite 文件库驱动，并发用例配 -race。
// ========================================
package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// newInboxTestStore 用文件库建 Store：内存库多连接各自建库，并发用例会假绿。
// 参数：t=测试句柄；返回：已跑过收件箱域迁移的 Store。
func newInboxTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "inbox.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	if err := st.SalesInboxMigrate(); err != nil {
		t.Fatalf("收件箱域迁移失败: %v", err)
	}
	return st
}

// I1：16 个并发重复投递同一事件必须恰好一个赢家，败方不许上抛错误。
func TestInboxConcurrentDuplicateOnlyOneWinner(t *testing.T) {
	st := newInboxTestStore(t)
	const n = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, errs := 0, []error{}
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := st.TryProcessInboxEvent(1, "event-X", "one-1", "flow_drive")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			if ok {
				wins++
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("I1：并发败方不许上抛错误，实得 %d 条：%v", len(errs), errs)
	}
	if wins != 1 {
		t.Fatalf("I1：%d 个并发重复投递必须恰好 1 个成功，实得 %d", n, wins)
	}
}

// I2：同键第二次必败；不同租户同 event_id 互不干扰（唯一键必须带 tenant_id）。
func TestInboxTenantIsolationAndRepeat(t *testing.T) {
	st := newInboxTestStore(t)
	ok, err := st.TryProcessInboxEvent(1, "same-event", "one-1", "user_event")
	if err != nil || !ok {
		t.Fatalf("I2：租户 1 首次应成功: ok=%v err=%v", ok, err)
	}
	ok, err = st.TryProcessInboxEvent(1, "same-event", "one-1", "user_event")
	if err != nil || ok {
		t.Fatalf("I2：同租户同键第二次必须 false 且无错误: ok=%v err=%v", ok, err)
	}
	ok, err = st.TryProcessInboxEvent(2, "same-event", "one-2", "user_event")
	if err != nil || !ok {
		t.Fatalf("I2：另一租户同键必须各自成功: ok=%v err=%v", ok, err)
	}
	_, _, found, err := st.GetInboxEvent(3, "same-event")
	if err != nil || found {
		t.Fatalf("I2：第三租户不许读到别人的事件: found=%v err=%v", found, err)
	}
}

// I3：完成标记只写一次、按租户作用域，读回状态与时间必须符合口径。
func TestInboxMarkProcessedOnceAndScoped(t *testing.T) {
	st := newInboxTestStore(t)
	if ok, err := st.TryProcessInboxEvent(1, "ev-mark", "one-1", "flow_result"); err != nil || !ok {
		t.Fatalf("I3：抢占应成功: ok=%v err=%v", ok, err)
	}
	ok, err := st.MarkInboxProcessed(2, "ev-mark")
	if err != nil || ok {
		t.Fatalf("I3：跨租户完成标记不许写: ok=%v err=%v", ok, err)
	}
	ok, err = st.MarkInboxProcessed(1, "ev-mark")
	if err != nil || !ok {
		t.Fatalf("I3：首次完成标记应成功: ok=%v err=%v", ok, err)
	}
	ok, err = st.MarkInboxProcessed(1, "ev-mark")
	if err != nil || ok {
		t.Fatalf("I3：重复完成标记必须 false: ok=%v err=%v", ok, err)
	}
	status, processedAt, found, err := st.GetInboxEvent(1, "ev-mark")
	if err != nil || !found || status != "done" || processedAt == "" {
		t.Fatalf("I3：读回必须是 done＋非空完成时间: status=%s at=%s found=%v err=%v", status, processedAt, found, err)
	}
}

// I4：入参 fail-closed；空租户与空幂等键一律拒收。
func TestInboxRejectsInvalidArguments(t *testing.T) {
	st := newInboxTestStore(t)
	if _, err := st.TryProcessInboxEvent(0, "e", "o", "t"); err == nil {
		t.Fatal("I4：tid<=0 必须拒收")
	}
	if _, err := st.TryProcessInboxEvent(1, "  ", "o", "t"); err == nil {
		t.Fatal("I4：空幂等键必须拒收")
	}
	if _, err := st.MarkInboxProcessed(1, ""); err == nil {
		t.Fatal("I4：完成标记缺幂等键必须拒收")
	}
}

// I5 源码守卫：抢占实现不许退回先 COUNT 预检，不许字符串判 SQLSTATE，
// 唯一冲突只认 db.IsUniqueViolation（PG/SQLite 双方言共用一把尺子）。
func TestInboxUsesSingleConflictPredicate(t *testing.T) {
	raw, err := os.ReadFile("sales_inbox.go")
	if err != nil {
		t.Fatalf("I5 读实现失败: %v", err)
	}
	code := string(raw)
	if strings.Contains(code, "COUNT(") {
		t.Fatal("I5：收件箱抢占不许退回先 COUNT 预检的非原子形态")
	}
	if strings.Contains(code, "235"+"05") || strings.Contains(code, "strings.Contains(err.Error()") {
		t.Fatal("I5：唯一冲突判据不许用字符串判 SQLSTATE")
	}
	if !strings.Contains(code, "db.IsUniqueViolation(err)") {
		t.Fatal("I5：唯一冲突必须只走 db.IsUniqueViolation")
	}
	if !strings.Contains(code, "WHERE NOT EXISTS") {
		t.Fatal("I5：抢占必须是一条条件 INSERT")
	}
}
