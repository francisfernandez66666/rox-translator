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
	// ★ 现算写点清单（2026-11-15 收口，审计第 11 条）：
	//   原形态是四条**硬编码路径**（writepoint.go / outbox.go / api/lead_captured_outbox.go /
	//   store/outbox_migrate.go）——注释写着「现算」，实际是「只测已登记的那几个点」，
	//   新增写点只要落在别的文件里就完全在射程之外。
	//   现改成遍历 backend-go/internal 全树统计三个写入函数，并强制返回扫描文件数（射程自证）。
	root := filepath.Join(dir, "..") // backend-go/internal
	var defs, calls, files int
	var writeCalls int
	walkErr := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		files++
		src := readSrc(t, p)
		defs += strings.Count(src, "func InsertOutboxEvent(")
		calls += strings.Count(src, "InsertOutboxEvent(")
		writeCalls += strings.Count(src, "WriteLeadCaptured(") + strings.Count(src, "EmitLeadCapturedInTx(")
		return nil
	})
	if walkErr != nil {
		t.Fatalf("E1 扫描 backend-go/internal 失败: %v", walkErr)
	}
	// 下界 400：现读 internal 非测试 .go 恰 494 个（2026-11-15 实测）。
	// 方案 §2.2 写的 ≥800 出自施工前的 900+ 快照，镜像旧数字会让本锁恒红；
	// 这里按「比现状短一大截」重取探测器，只拦扫描面塌了，不拦合法的文件增删。
	// 它只拦「扫描面塌了」，不拦合法的文件增删（同 AGENTS §一·14：这是探测器，不是镜像）。
	if files < 400 {
		t.Fatalf("E1 射程失明：只扫到 %d 个非测试 .go 文件（下界 400）—— 写点清单是假的", files)
	}
	if defs != 1 {
		t.Fatalf("E1 源码：InsertOutboxEvent 定义点应为 1 处，实得 %d（重复定义或定义被删）", defs)
	}
	if calls-defs < 1 {
		t.Fatalf("E1 源码：InsertOutboxEvent 真实调用点为 0（定义之外至少要有 1 个写点）")
	}
	if writeCalls == 0 {
		t.Fatalf("E1 源码：WriteLeadCaptured／EmitLeadCapturedInTx 在整棵 internal 树里零引用 —— 写点被架空")
	}
	// ★ 2026-11-15 收口补腿：上面那个计数会被**函数定义与注释**凑满（审计 §4.3 指出
	//   「框架级正确 ≠ 链路级生效」）。真正要问的是「生产挂点有没有真的调它」：
	//   留资桥接必须在一个业务事务里同时写 inbox_events 与 event_outbox。
	bridge := readSrc(t, filepath.Join(root, "api", "sales_lead_inbox_bridge.go"))
	if !strings.Contains(bridge, "WriteLeadCaptured(ctx, s.Store.DB()") {
		t.Fatal("E1 生产写点被摘：留资桥接不再调用 WriteLeadCaptured ⇒ event_outbox 会重新恒空")
	}
	if !strings.Contains(bridge, "TryProcessInboxEventTx(tx,") {
		t.Fatal("E1 生产写点不在业务事务内：inbox_events 与 event_outbox 不再同生共死")
	}
	if len(RegisteredTopics) == 0 {
		t.Fatal("E1 下界自证失败：登记白名单为空（登记被架空）")
	}
	// 名单外的写点为零：未登记主题必须被 InsertOutboxEvent 运行时拒收（真写入，非桩）。
	outsider := testStoredEvent(t, testEnv(t, "topic.not.registered", "ev-outside", 7, "tr-out"))
	if err := InsertOutboxEvent(conn, d, outsider); err == nil {
		t.Fatal("E1 名单外的写点为零：未登记主题必须被拒收，不许静默放过新写点")
	}
}
