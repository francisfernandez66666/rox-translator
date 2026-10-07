// anonym_cleanup_test.go — 过期匿名会话清理的收敛性单测（★ 〇-AP：修 SQL 语法错的同时，
// 堵住"修好后才暴露的空转"这一族新缺陷）。
//
// 为什么这一测必须存在：旧 startAnonymCleanup 的批删循环写成 `for total == 0 { total, err = Cleanup() }`，
// 只有"删到东西"才退出内层。配合 CleanupExpiredAnonymous 的 `DELETE … LIMIT`（modernc sqlite 非法）
// 每小时首轮直接 err≠nil→break，这个"删 0 还继续转"的死循环**从没被触发过、也就没被发现**。
// SQL 修好后，"这一轮确无过期数据"会正常返回 (0,nil) ⇒ 若循环口径不改，内层 for total==0 将**永远退不出、
// 空转打满 CPU**。故修法把批删抽成 drainExpiredAnonymous，"某批删 0 行即收敛返回"。
// ★ 反证：把 drainExpiredAnonymous 改回 `for total == 0 { total, err = …; if err!=nil break }` 那种
//
//	"空转"形态，TestDrainEmptyDBTerminates（空库应秒回）会在超时上红——正是复现回归。
package api

import (
	"fmt"
	"testing"
	"time"

	"translator/internal/assist/store"
)

// newCleanupServer 只装 db 字段，directly 驱动 drainExpiredAnonymous（不启后台 ticker goroutine）。
func newCleanupServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/cleanup.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Server{db: db}, db
}

// TestDrainEmptyDBTerminates 无可清数据时 drainExpiredAnonymous 必须**正常返回 (0,nil)、不空转**。
// 用带超时的 goroutine 兜住：一旦回归成死循环，这里 3s 内判红而不是把测试挂死。
func TestDrainEmptyDBTerminates(t *testing.T) {
	s, _ := newCleanupServer(t)
	done := make(chan struct{})
	go func() {
		n, err := s.drainExpiredAnonymous()
		if err != nil {
			t.Errorf("空库清理不应报错: %v", err)
		}
		if n != 0 {
			t.Errorf("空库应删 0 行，实际 %d", n)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("drainExpiredAnonymous 在无可清数据时未收敛返回＝旧 `for total==0` 空转回归（会打满 CPU）")
	}
}

// TestShellSessionIsReclaimed ★ 〇-AR 第 7 波（㊿）：msg_count 带着旧计数、消息表里已经一行的会话
// 必须被回收掉。
// 现网实证：〇-AM 把旧 `sessions` 搬进 `sessions_base` 时把**历史计数原样搬过来**，
// 整点清理把 65 条消息按批删空后，13 个会话壳因 `msg_count>0` 永远满足不了删除判据
// （`messages_base=0`／`sessions_base=13`，每小时都删不动）。
// 这里不碰迁移、只用 TouchSession 造出同一个形态：计数被抬过、消息却不存在。
// ★ 反证：把 CleanupExpiredAnonymous 里那条「按消息表真值归一 msg_count」的 UPDATE 摘掉，
//
//	本测当场红（壳行留下的正是旧形态）。
func TestShellSessionIsReclaimed(t *testing.T) {
	_, db := newCleanupServer(t)

	// 匿名＋已过期（expires_at 为 NULL 即视为过期），计数靠 TouchSession 抬到 3，但一条消息都没落。
	if err := db.CreateSession("s-shell", "/x", 0, 0, "hash-shell"); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := db.TouchSession("s-shell"); err != nil {
			t.Fatalf("TouchSession 失败: %v", err)
		}
	}
	row, err := db.SessionRow("s-shell")
	if err != nil || row == nil {
		t.Fatalf("前置读数失败: row=%v err=%v", row, err)
	}
	if mc, _ := row["msg_count"].(int); mc != 3 {
		t.Fatalf("前置：壳行计数应为 3，实际 %v（造数据形态变了，断言射程也随之失效）", row["msg_count"])
	}

	if _, err := db.CleanupExpiredAnonymous(100); err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	row, err = db.SessionRow("s-shell")
	if err != nil {
		t.Fatalf("复查会话失败: %v", err)
	}
	if row != nil {
		t.Fatalf("消息已空却仍留着会话壳（msg_count=%v）＝㊿ 回归：清理腿还在信写时计数器，壳行永远清不掉", row["msg_count"])
	}
}

// TestLoggedInSessionWithStaleCountIsUntouched ★ ㊿ 修法的**反向对照**：归一与回收只圈「匿名＋已过期」那一小批，
// 登录态会话（anonym_hash 为空串）即使计数与消息表不一致也**一个字都不许动**。
// 误伤这一档的代价是把客户/运营还在看的会话记录洗掉，比留 13 个壳行严重得多。
func TestLoggedInSessionWithStaleCountIsUntouched(t *testing.T) {
	_, db := newCleanupServer(t)

	if err := db.CreateSession("s-auth", "/y", 7, 42, ""); err != nil {
		t.Fatalf("建登录态会话失败: %v", err)
	}
	if err := db.TouchSession("s-auth"); err != nil {
		t.Fatalf("TouchSession 失败: %v", err)
	}
	if err := db.TouchSession("s-auth"); err != nil {
		t.Fatalf("TouchSession 失败: %v", err)
	}

	if _, err := db.CleanupExpiredAnonymous(100); err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	row, err := db.SessionRow("s-auth")
	if err != nil {
		t.Fatalf("复查会话失败: %v", err)
	}
	if row == nil {
		t.Fatal("登录态会话被清理腿删掉了＝越界：回收射程只许是「匿名＋已过期」")
	}
	if mc, _ := row["msg_count"].(int); mc != 2 {
		t.Fatalf("登录态会话的计数被改写（应仍为 2，实际 %v）＝归一腿漏了 anonym_hash 条件", row["msg_count"])
	}
}

// TestExpiredAnonymousWithMessagesDrainsAndReclaimsSession 正常形态（有过期会话**且真带消息**）：
// 同一轮里消息被删空后，会话也必须跟着回收——不能出现"消息清了、壳还在"。
// 这一条同时证明归一腿没有把该留的会话误判成空（删消息在前、归一在后，顺序反了就把有消息的会话删了）。
func TestExpiredAnonymousWithMessagesDrainsAndReclaimsSession(t *testing.T) {
	_, db := newCleanupServer(t)

	if err := db.CreateSession("s-live", "/x", 0, 0, "hash-live"); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	const msgs = 2
	for i := 0; i < msgs; i++ {
		if err := db.AddMessage("s-live", "user", fmt.Sprintf("问 %d", i), nil); err != nil {
			t.Fatalf("灌消息失败: %v", err)
		}
	}
	if err := db.TouchSession("s-live"); err != nil {
		t.Fatalf("TouchSession 失败: %v", err)
	}

	n, err := db.CleanupExpiredAnonymous(100)
	if err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	if n != msgs {
		t.Fatalf("应删 %d 条消息，实际 %d", msgs, n)
	}
	if hs, _ := db.History("s-live", 100); len(hs) != 0 {
		t.Fatalf("消息应被清空，残留 %d 条", len(hs))
	}
	row, err := db.SessionRow("s-live")
	if err != nil {
		t.Fatalf("复查会话失败: %v", err)
	}
	if row != nil {
		t.Fatalf("消息已空的匿名过期会话仍留着壳（msg_count=%v）＝回收腿没跟上", row["msg_count"])
	}
}

// TestDrainMultiBatch 过期消息超过一整批（>100）时，drain 必须分批删干净并返回累计条数。
// 锁"批删循环"在修好收敛口径后仍能排空多批（不是删一批就漏剩下的）。
func TestDrainMultiBatch(t *testing.T) {
	s, db := newCleanupServer(t)

	// 一个已过期匿名会话（expires_at 为 NULL 即视为过期）＋ 150 条消息，横跨 100 一批。
	if err := db.CreateSession("s-many", "/x", 0, 0, "hash-many"); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	const msgs = 150
	for i := 0; i < msgs; i++ {
		if err := db.AddMessage("s-many", "user", fmt.Sprintf("问题 %d", i), nil); err != nil {
			t.Fatalf("灌消息失败: %v", err)
		}
	}
	// ★ 与生产同态：真实链路每轮会 TouchSession ⇒ 有消息的会话 msg_count>0，
	//   清理里那条 `msg_count=0` 的空会话回收腿就不会在批删中途把会话摘掉（否则会剩下够不着的孤儿消息）。
	//   这里手动 touch 一次把 msg_count 抬起，忠实复现"消息横跨多批、会话仍在"的排空场景。
	if err := db.TouchSession("s-many"); err != nil {
		t.Fatalf("TouchSession 失败: %v", err)
	}

	n, err := s.drainExpiredAnonymous()
	if err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	if n != msgs {
		t.Fatalf("应分批累计删除 %d 条，实际 %d（多半是循环口径又只删了一批或提前退出）", msgs, n)
	}
	if hs, _ := db.History("s-many", 1000); len(hs) != 0 {
		t.Fatalf("过期会话消息应被排空，残留 %d 条", len(hs))
	}
}
