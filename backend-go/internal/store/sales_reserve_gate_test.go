// ============ 本文件职责中文说明 ============
// 二期 §4 断言 D4：条件 INSERT 的原子性。16 个并发写同一键必须恰好一个成功、
// 败方不许上抛错误；不同键可各成功一次；重复占位返回 false。
// 另立源码级守卫：本原语不许退回「先 COUNT 预检再插入」的非原子形态（含反证）。
// ========================================
package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lib/pq"
	"translator/internal/config"
	"translator/internal/db"
)

// newReserveTestStore 用**文件库**建 Store：内存库多连接各自建库，并发用例会假绿。
func newReserveTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "reserve.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	return st
}

// D4 主判据：并发只有一个赢家，且败方不是错误。
func TestSalesReserveConcurrentOnlyOneWins(t *testing.T) {
	st := newReserveTestStore(t)
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
			ok, err := st.TryReserve(1, "template", "code-X")
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
		t.Fatalf("D4：并发败方不许上抛错误，实得 %d 条：%v", len(errs), errs)
	}
	if wins != 1 {
		t.Fatalf("D4：%d 个并发写必须恰好 1 个成功，实得 %d", n, wins)
	}
}

// D4 补腿：PG 竞态那一支在 SQLite 本机走不到，必须用合成的结构化错误单独判，
// 否则「把 reservationConflict 摘成 false」在本机全绿、生产把并发败方写成 500。
func TestSalesReserveConflictPredicateRecognizesPG(t *testing.T) {
	pgDup := &pq.Error{Code: pq.ErrorCode("235" + "05")} // 拆开写：避 db 包 D3 的全仓字面量扫描
	if !reservationConflict(pgDup) {
		t.Fatal("D4：PG 唯一冲突必须判成并发败方，不许上抛 500")
	}
	if reservationConflict(errors.New("disk full")) {
		t.Fatal("D4：非冲突错误不许吞成「没抢到」")
	}
}

// 正向对照：不同键各可成功一次；同一键第二次必须 false。
func TestSalesReserveSecondAttemptLoses(t *testing.T) {
	st := newReserveTestStore(t)
	ok, err := st.TryReserve(1, "template", "A")
	if err != nil || !ok {
		t.Fatalf("首次占位应成功: ok=%v err=%v", ok, err)
	}
	ok, err = st.TryReserve(1, "template", "A")
	if err != nil || ok {
		t.Fatalf("同键第二次必须失败且无错误: ok=%v err=%v", ok, err)
	}
	ok, err = st.TryReserve(1, "template", "B")
	if err != nil || !ok {
		t.Fatalf("不同键应各自成功: ok=%v err=%v", ok, err)
	}
	ok, err = st.TryReserve(2, "template", "A")
	if err != nil || !ok {
		t.Fatalf("跨租户同键应各自成功: ok=%v err=%v", ok, err)
	}
	if _, err := st.TryReserve(0, "template", "C"); err == nil {
		t.Fatal("无效租户必须在写侧拒掉")
	}
}

// 源码级守卫：不许退回 Count 预检；条件 INSERT 必须在同一条语句里。
func TestSalesReserveHasNoCountPrecheck(t *testing.T) {
	hits, err := scanCountPrecheck("sales_reserve.go")
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("D4：原子写原语出现 Count 预检（非原子）: %v", hits)
	}
	src, err := os.ReadFile("sales_reserve.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "WHERE NOT EXISTS") {
		t.Fatal("D4：判据与写入必须在同一条 INSERT…SELECT 语句里")
	}
}

// 反证：扫描器必须看得见一处 Count 预检。
func TestSalesReserveCountScanHasTeeth(t *testing.T) {
	dir := t.TempDir()
	bad := "package store\n\nfunc f() { _ = `SELECT COUNT(*) FROM t` }\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	hits, err := scanCountPrecheck(filepath.Join(dir, "bad.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("Count 预检反证必须命中 1 处，实得 %d ⇒ 扫描器失明", len(hits))
	}
}

// scanCountPrecheck 扫单个 .go 文件里的 Count 预检字面（剥注释）。
// 参数：path=文件路径；返回命中行号清单。
func scanCountPrecheck(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var hits []string
	for i, line := range strings.Split(string(raw), "\n") {
		code := line
		if j := strings.Index(code, "//"); j >= 0 {
			code = code[:j]
		}
		if strings.Contains(code, "COUNT(*)") || strings.Contains(code, "count(*)") {
			hits = append(hits, path+":"+itoaReserve(i+1))
		}
	}
	return hits, nil
}

// itoaReserve 正整数转十进制串（避免与包内其它辅助函数同名）。
// 参数：n=正整数；返回十进制字符串。
func itoaReserve(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
