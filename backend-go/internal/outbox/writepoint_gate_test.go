// ============ writepoint_gate_test.go · 职责说明 ============
// E1（§4.4.3）：每个登记的业务写点都与 outbox 同事务；反证 M0292「把 outbox 写移到事务外 ⇒ 红」。
// 两条腿互不替代：
//
//	① 行为（真事务/真 SQL/真回滚）：business 失败 ⇒ 业务行与事件行都不在（任务 D 的硬判据）。
//	② 源码（E1 类型=源码）：写点清单现算 + 「InsertOutboxEvent(tx,…) 在 tx.Commit() 之前」+ 名单外写点为零。
//
// ================================================
package outbox

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"translator/internal/db"
)

func srcDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("取当前测试文件位置失败")
	}
	return filepath.Dir(thisFile)
}

func readSrc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读源码 %s 失败: %v", path, err)
	}
	return string(b)
}

func TestWritepointSameTxRollsBackBoth(t *testing.T) {
	conn := newTestDB(t)
	d := db.DialectSQLite
	ctx := context.Background()

	// ① 正常路径：业务行 + 事件行都必须落库（真事务、真 SQL）。
	evOK := testStoredEvent(t, testEnv(t, TopicLeadCaptured, "ev-ok", 7, "tr-ok"))
	if err := WritePoint(ctx, conn, d, bizInsert("lead-ok"), evOK); err != nil {
		t.Fatalf("E1 正常写点必须成功，实得 %v", err)
	}
	if got := countRows(t, conn, "wp_biz"); got != 1 {
		t.Fatalf("E1 正常路径业务行应为 1，实得 %d", got)
	}
	if got := countRows(t, conn, "event_outbox"); got != 1 {
		t.Fatalf("E1 正常路径事件行应为 1，实得 %d", got)
	}

	// ② 回滚路径：business 在同一 tx 写业务行后失败 ⇒ 整体回滚，两者都不在。
	sentinel := errors.New("业务写故意失败")
	evRollback := testStoredEvent(t, testEnv(t, TopicLeadCaptured, "ev-rollback", 7, "tr-rb"))
	bizRollback := func(tx *sql.Tx) error {
		if _, err := db.Exec(tx, d, "INSERT INTO wp_biz(note) VALUES(?)", "lead-rollback"); err != nil {
			return err
		}
		return sentinel
	}
	if err := WritePoint(ctx, conn, d, bizRollback, evRollback); err == nil {
		t.Fatal("E1 business 失败必须上抛错误，不许静默吞")
	}
	if got := countRows(t, conn, "wp_biz"); got != 1 {
		t.Fatalf("E1 回滚后业务行不应新增（仍应为 1），实得 %d —— 业务写没进同一事务", got)
	}
	if got := countRows(t, conn, "event_outbox"); got != 1 {
		t.Fatalf("E1 回滚后 event_outbox 不应新增（仍应为 1），实得 %d —— 事件写没进同一事务", got)
	}
	// 下界：显式证 ev-rollback 这条事件确实不在（不是靠总数蒙混）。
	if _, ok := scalarString(t, conn, "SELECT event_id FROM event_outbox WHERE event_id=?", "ev-rollback"); ok {
		t.Fatal("E1 回滚后 ev-rollback 事件行必须不存在")
	}

	// ③ 源码射程自证：写点清单现算 + 同 tx 形态 + 名单外写点为零（E1 的源码腿）。
	dir := srcDir(t)
	wp := readSrc(t, filepath.Join(dir, "writepoint.go"))
	if !strings.Contains(wp, "InsertOutboxEvent(tx, d, ev)") {
		t.Fatal("E1 源码：WritePoint 必须在同一事务 tx 内调 InsertOutboxEvent(tx, d, ev)（把事件写移到事务外即红，M0292）")
	}
	iCall := strings.Index(wp, "InsertOutboxEvent(tx, d, ev)")
	iCommit := strings.Index(wp, "tx.Commit()")
	if iCall < 0 || iCommit < 0 || iCall > iCommit {
		t.Fatal("E1 源码：事件写必须排在 tx.Commit() 之前，才与业务写同事务")
	}
	// 现算写点清单：扫 outbox 包 + api 适配层 + store 迁移，禁止把名单硬编码进测试。
	scan := []string{
		filepath.Join(dir, "writepoint.go"),
		filepath.Join(dir, "outbox.go"),
		filepath.Join(dir, "..", "api", "lead_captured_outbox.go"),
		filepath.Join(dir, "..", "store", "outbox_migrate.go"),
	}
	callSites, topicRefs := 0, 0
	for _, f := range scan {
		src := readSrc(t, f)
		callSites += strings.Count(src, "InsertOutboxEvent(")
		topicRefs += strings.Count(src, "TopicLeadCaptured")
	}
	if callSites < 2 { // 至少含 1 个定义 + 1 个真实调用点，防止只测已登记的那几个点＝给新写点放假
		t.Fatalf("E1 源码：现算写点清单射程不足（InsertOutboxEvent 出现 %d 次）", callSites)
	}
	if topicRefs == 0 || len(RegisteredTopics) == 0 {
		t.Fatal("E1 下界自证失败：登记白名单为空或在源码里零引用（登记被架空）")
	}
	// 名单外的写点为零：未登记主题必须被 InsertOutboxEvent 运行时拒收（真写入，非桩）。
	outsider := testStoredEvent(t, testEnv(t, "topic.not.registered", "ev-outside", 7, "tr-out"))
	if err := InsertOutboxEvent(conn, d, outsider); err == nil {
		t.Fatal("E1 名单外的写点为零：未登记主题必须被拒收，不许静默放过新写点")
	}
}
