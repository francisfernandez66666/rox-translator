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
	"bytes"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/assist/engine"
	"translator/internal/assist/llm"
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
		n, sess, err := s.drainExpiredAnonymous()
		if err != nil {
			t.Errorf("空库清理不应报错: %v", err)
		}
		if n != 0 {
			t.Errorf("空库应删 0 行，实际 %d", n)
		}
		if sess != 0 {
			t.Errorf("空库应回收 0 个会话，实际 %d", sess)
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

	// ★ 52：壳行回收必须**自己有读数**（旧形态这条腿的条数被 `_, _ =` 丢掉）。
	msgs, sess, err := db.CleanupExpiredAnonymous(100)
	if err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	if msgs != 0 {
		t.Fatalf("前置：壳行没有消息，不该删到消息，实际 %d", msgs)
	}
	if sess != 1 {
		t.Fatalf("壳行回收读数应为 1，实际 %d＝会话腿又静默了（52）", sess)
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

	if _, _, err := db.CleanupExpiredAnonymous(100); err != nil {
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

	n, sess, err := db.CleanupExpiredAnonymous(100)
	if err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	if n != msgs {
		t.Fatalf("应删 %d 条消息，实际 %d", msgs, n)
	}
	if sess != 1 {
		t.Fatalf("同一轮里会话壳也要报数（应为 1，实际 %d）＝52 的读数腿没接上", sess)
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

	n, sess, err := s.drainExpiredAnonymous()
	if err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	if n != msgs {
		t.Fatalf("应分批累计删除 %d 条，实际 %d（多半是循环口径又只删了一批或提前退出）", msgs, n)
	}
	if sess != 1 {
		t.Fatalf("跨批排完后会话壳也该报数（应为 1，实际 %d）", sess)
	}
	if hs, _ := db.History("s-many", 1000); len(hs) != 0 {
		t.Fatalf("过期会话消息应被排空，残留 %d 条", len(hs))
	}
}

// ============================================================
// ★ 〇-AR 第 8 波（52）：清理腿的**读数**必须存在，而且要读得到。
//
// ㊿ 把"删不掉的壳行"修好之后，这一族还剩最后一半没闭合：
//   · 出参侧——会话回收的 RowsAffected 被 `_, _ = Exec(...)` 丢弃，调用方只拿得到消息条数；
//   · 日志侧——旧判据 `if total > 0` 里的 total 只数消息，于是
//     「这一轮回收了 13 个壳行、一条消息都没删」（㊿ 之后每小时都撞的形态）**一行日志都不写**；
//   · 健康侧——/health 压根没有这一档，现网问"清理腿还在跑吗"只能 grep 日志、
//     而日志按上面那条判据本来就不写 ⇒ 只能人手数表。
// 下面三条断言各钉一维，反证见各自注释（三条都能在**只改一处**的情况下当场红）。
// ============================================================

// lockedBuf 并发安全的日志缓冲（后台清理 goroutine 也可能写日志，裸 bytes.Buffer 会 race）。
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// captureCleanupLogs 把默认 slog 换进内存缓冲，用例结束自动还原。
func captureCleanupLogs(t *testing.T) *lockedBuf {
	t.Helper()
	old := slog.Default()
	lb := &lockedBuf{}
	slog.SetDefault(slog.New(slog.NewTextHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return lb
}

// TestAnonymCleanupRoundReclaimsShellRows 主判据：只壳行、零消息的一轮，
// 三条读数面必须**同时**出声（出参 / 日志 / 健康面）。
// ★ 反证三处（各改一处即红）：
//  1. store.go 会话回收改回 `_, _ = Exec(...)` ＋ return 0 ⇒ 出参与健康面两条红；
//  2. runAnonymCleanupRound 的日志判据改回 `if msgs > 0` ⇒ 日志那条红（现网静默形态）；
//  3. runAnonymCleanupRound 里删掉 recordAnonymCleanupRound 调用 ⇒ 健康面红，日志仍绿
//     （＝"日志有、读面没接"那一族，正是本条要单独立一维的原因）。
func TestAnonymCleanupRoundReclaimsShellRows(t *testing.T) {
	s, db := newCleanupServer(t)
	if err := db.CreateSession("s-shell-52", "/x", 0, 0, "hash-52"); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if err := db.TouchSession("s-shell-52"); err != nil {
		t.Fatalf("TouchSession 失败: %v", err)
	}
	lb := captureCleanupLogs(t)

	msgs, sess, err := s.runAnonymCleanupRound()
	if err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}
	if msgs != 0 {
		t.Fatalf("壳行没有消息，消息条数应为 0，实际 %d", msgs)
	}
	if sess != 1 {
		t.Fatalf("出参侧：会话回收条数应为 1，实际 %d＝那条 DELETE 的 RowsAffected 又被丢了（52）", sess)
	}
	logged := lb.String()
	if !strings.Contains(logged, "deleted_sessions=1") {
		t.Fatalf("日志侧：本轮回收 1 个壳行却没写进日志＝旧 `total>0` 只数消息的静默形态回归\n日志实得:\n%s", logged)
	}
	if strings.Contains(logged, "level=ERROR") {
		t.Fatalf("正常轮次不许报 ERROR:\n%s", logged)
	}
	h := s.anonymCleanupHealth()
	if h["status"] != anonymCleanupOK {
		t.Fatalf("健康面：删到东西的一轮状态词应为 %q，实得 %v", anonymCleanupOK, h["status"])
	}
	if h["deleted_sessions"] != 1 {
		t.Fatalf("健康面：deleted_sessions 读数应为 1，实得 %v＝健康侧那根读腿没接上", h["deleted_sessions"])
	}
}

// TestAnonymCleanupHealthWords 三个状态词的**分档**判据（never／idle／failing 必须各归各位）。
// never 与 idle 不许合成一个词：前者＝周期任务压根没跑过一轮（腿没接），后者＝跑过但确实无事可做。
// 合成一个"看着都正常"的词，就是把〇-AP 那类「清理从没成功跑过」重新写成绿灯。
// ★ 反证：把 recordAnonymCleanupRound 里的 idle 分支去掉（无事可做也报 ok）⇒ 第二条红；
//
//	把 anonymRounds==0 那条 never 分支改成回 idle（没跑过也装"跑过且空闲"）⇒ 第一条红。
func TestAnonymCleanupHealthWords(t *testing.T) {
	s, _ := newCleanupServer(t)
	if got := s.anonymCleanupHealth()["status"]; got != anonymCleanupNever {
		t.Fatalf("一轮都没跑过时状态词应为 %q，实得 %v（never／idle 混档＝分不清「腿没接」与「无事可做」）", anonymCleanupNever, got)
	}
	if _, _, err := s.runAnonymCleanupRound(); err != nil { // 空库：无事可做
		t.Fatalf("清理不应报错: %v", err)
	}
	if got := s.anonymCleanupHealth()["status"]; got != anonymCleanupIdle {
		t.Fatalf("跑过但零删除应为 %q，实得 %v", anonymCleanupIdle, got)
	}
	// 上游报错档：把库关掉，下一轮必然 err≠nil ⇒ 读数翻成 failing（而不是静默留在 idle）。
	_ = s.db.Close()
	if _, _, err := s.runAnonymCleanupRound(); err == nil {
		t.Fatal("前置：库已关闭，这一轮应当报错")
	}
	h := s.anonymCleanupHealth()
	if h["status"] != anonymCleanupFailing {
		t.Fatalf("失败轮次状态词应为 %q，实得 %v＝失败仍然静默（52 的读面档没分出来）", anonymCleanupFailing, h["status"])
	}
	if rounds, _ := h["rounds"].(int); rounds != 2 {
		t.Fatalf("轮次计数应为 2，实得 %v", h["rounds"])
	}
}

// TestHealthRouteCarriesAnonymCleanupReadout 路由级对照：anonymCleanupHealth() 这个函数绿了
// 不等于 /health 上真带这一档（判据级断言代替不了守卫级——见 AGENTS §三 那条"自撒谎"形态②）。
// 这里**真打一次 /health**，按出栈 JSON 里那一个字段核。
// ★ 反证：Handler() 里删掉 `"anonym_cleanup": ...` 那一行 ⇒ 本条当场红（前两条仍绿）。
func TestHealthRouteCarriesAnonymCleanupReadout(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/health52.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewServer(db, engine.New(db, llm.New(nil, 5)), "test-token", "*")
	// ★ 真拨一次那一轮（不手拼读数）：NewServer 内部的 ticker 间隔是 1h，本测不等它。
	// ⚠️ 别在 NewServer 之后再写 s.anonymCleanupInterval——后台 goroutine 会读同一个字段，`-race` 当场现形。
	if err := db.CreateSession("s-h52", "/x", 0, 0, "hash-h52"); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if _, _, err := s.runAnonymCleanupRound(); err != nil {
		t.Fatalf("清理不应报错: %v", err)
	}

	code, body := doJSON(t, httptest.NewServer(s.Handler()), "GET", "/health", "", nil)
	if code != 200 {
		t.Fatalf("健康检查应 200，实得 %d", code)
	}
	seg, ok := body["anonym_cleanup"].(map[string]any)
	if !ok {
		t.Fatalf("/health 出栈里没有 anonym_cleanup 这一档：%+v", body)
	}
	if seg["status"] != anonymCleanupOK || seg["deleted_sessions"] != 1.0 {
		t.Fatalf("路由级读数不对：status=%v deleted_sessions=%v（期望 %q／1）",
			seg["status"], seg["deleted_sessions"], anonymCleanupOK)
	}
}
