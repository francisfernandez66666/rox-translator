// ============ notifications_batch_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 §2.12 A12：站内信**批量 INSERT 与逐条 INSERT 的逐行等值锁**。
//
// 为什么必须比「逐行」而不是只比条数：批量口是为了省 N 次往返新增的第二条写腿，
// 一旦它与 CreateNotification 在字段/顺序/未读态上分叉，表现不是报错，而是
// 「超管铃铛里少一条、多一条、或 ref_type 错位」——投递口径必须逐行逐字段比。
//
//	E1 同夹具下两档写入逐行等值（含名单外 ref_type、refID=0 那两档）；
//	E2 空收件人 ⇒ 不发 SQL：把底层连接关掉之后，空入参仍须退 (0,nil)；
//	   反证＝同一份关库夹具喂非空入参必须报错，否则 E2 是恒绿空转。
//
// created_at 两侧各自取写时刻，不参与逐字等值（时刻必然不同），只判「都在位」。
// 方言自钉（AGENTS §一·4）：config.Default() 会副作用写全局 config.C，
// run_uat 的 PG 模式会把它泄漏给同包后续内存 SQLite 用例。
// =============================================
package store

import (
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// pinSQLiteForNotifBatch 把全局方言钉成本地内存 SQLite 并在用例结束时复原（AGENTS §一·4 模板）。
func pinSQLiteForNotifBatch(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// notifEqualityRow 站内信落库行的等值投影（含自增 id：两侧都是空库按同顺序插，id 也必须相等）。
type notifEqualityRow struct {
	ID        int64
	UserID    int64
	Title     string
	Body      string
	RefType   string
	RefID     int64
	ReadAt    string
	CreatedAt string
}

// readNotifEqualityRows 按 (user_id, id) 升序读出全部站内信，供两档夹具逐行比对。
func readNotifEqualityRows(t *testing.T, s *Store) []notifEqualityRow {
	t.Helper()
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, user_id, title, body, ref_type, ref_id, read_at, COALESCE(created_at,'') FROM notifications ORDER BY user_id, id")
	if err != nil {
		t.Fatalf("读站内信失败: %v", err)
	}
	defer rows.Close()
	var out []notifEqualityRow
	for rows.Next() {
		var r notifEqualityRow
		if err := rows.Scan(&r.ID, &r.UserID, &r.Title, &r.Body, &r.RefType, &r.RefID, &r.ReadAt, &r.CreatedAt); err != nil {
			t.Fatalf("扫描站内信行失败: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// TestCreateNotificationsBatchEqualsPerRow E1：批量与逐条在同样夹具下逐行等值。
func TestCreateNotificationsBatchEqualsPerRow(t *testing.T) {
	pinSQLiteForNotifBatch(t)
	items := []NotificationDraft{
		{UserID: 101, Title: "收到新的问题反馈", Body: "正文A", RefType: "feedback", RefID: 42},
		{UserID: 102, Title: "工单已完成", Body: "正文B", RefType: "ticket", RefID: 7},
		// 名单外类型照投不拦（api/notify_recipients_test.go L4 那一支），refID=0 的边界也一起钉
		{UserID: 103, Title: "未知类目", Body: "正文C", RefType: "not_in_list_yet", RefID: 0},
	}

	stBatch := newTestStore(t)
	n, err := stBatch.CreateNotificationsBatch(items)
	if err != nil {
		t.Fatalf("批量写入报错: %v", err)
	}
	if n != len(items) {
		t.Fatalf("批量送达人数 = %d，期望 %d（n 的口径不许变）", n, len(items))
	}

	stSingle := newTestStore(t)
	for _, it := range items {
		if err := stSingle.CreateNotification(it.UserID, it.Title, it.Body, it.RefType, it.RefID); err != nil {
			t.Fatalf("逐条写入 uid=%d 报错: %v", it.UserID, err)
		}
	}

	gotBatch := readNotifEqualityRows(t, stBatch)
	gotSingle := readNotifEqualityRows(t, stSingle)
	if len(gotBatch) != len(items) || len(gotSingle) != len(items) {
		t.Fatalf("两档落库行数不等：批量 %d / 逐条 %d / 期望 %d", len(gotBatch), len(gotSingle), len(items))
	}
	for i := range items {
		b, a := gotBatch[i], gotSingle[i]
		if b.ID != a.ID || b.UserID != a.UserID || b.Title != a.Title || b.Body != a.Body ||
			b.RefType != a.RefType || b.RefID != a.RefID || b.ReadAt != a.ReadAt {
			t.Errorf("第 %d 行不等值：批量 %+v vs 逐条 %+v", i, b, a)
		}
		// created_at 只判在位，不逐字比（两侧各取写时刻）
		if b.CreatedAt == "" || a.CreatedAt == "" {
			t.Errorf("第 %d 行 created_at 缺席：批量 %q / 逐条 %q", i, b.CreatedAt, a.CreatedAt)
		}
	}
	// 点名判据（防「两边一起写空也算等值」的恒绿空转）
	if gotBatch[0].UserID != 101 || gotBatch[0].RefType != "feedback" || gotBatch[0].ReadAt != "" {
		t.Fatalf("批量首行投递口径不符：%+v", gotBatch[0])
	}
	if gotBatch[2].RefType != "not_in_list_yet" || gotBatch[2].RefID != 0 {
		t.Fatalf("名单外类型/零 refID 那一档没落库：%+v", gotBatch[2])
	}
}

// TestCreateNotificationsBatchEmptySendsNoSQL E2：空收件人 ⇒ 不发 SQL。
// 判据靠「关掉底层连接」给射程：空入参若仍执行 db.Exec，会拿到 database is closed。
func TestCreateNotificationsBatchEmptySendsNoSQL(t *testing.T) {
	pinSQLiteForNotifBatch(t)
	st := newTestStore(t)
	if err := st.db.Close(); err != nil {
		t.Fatalf("关测试库失败: %v", err)
	}

	n, err := st.CreateNotificationsBatch(nil)
	if err != nil || n != 0 {
		t.Fatalf("空收件人必须不发 SQL 且退 (0,nil)，实得 n=%d err=%v", n, err)
	}
	// 反证（★ 恒零命中必须配正对照）：同一份关库夹具喂非空入参必须报错，
	// 否则上面那条「空入参没报错」只是方法整体没碰库，判据没有射程。
	if _, err := st.CreateNotificationsBatch([]NotificationDraft{{UserID: 1, Title: "x"}}); err == nil {
		t.Fatal("反证失败：关库后非空入参竟没报错 ⇒ E2 的空入参判据是空转")
	}
}
