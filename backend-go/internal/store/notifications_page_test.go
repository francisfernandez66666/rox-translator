// ============ notifications_page_test.go · 职责说明 ============
// store 包站内信**分页口径**单测（★ 2026-10-11 跨境链路瓶颈批）。
// 锁四件事：① 缺参/脏参折回默认档 20；② 显式抬档被钳到单页上限 100（不是报错、也不是整表拉空）；
// ③ offset 翻页不重叠、不跳行、顺序恒最新在前；④ 用户隔离在分页后仍然成立（offset 不许跨用户取行）。
// ★ 方言自钉（AGENTS §一·4）：config.Default() 读 DB_DRIVER 且副作用写全局 config.C，
//
//	先存旧值、Cleanup 还原；否则 run_uat 的 PG 模式会把方言泄漏给同包后续内存 SQLite 用例。
//
// ==================================================================
package store

import (
	"database/sql"
	"fmt"
	"testing"

	"translator/internal/config"
)

// newNotifPageStore 建内存 SQLite 并跑全量迁移（notifications 表由 store.New 的迁移建），
// 给 userID 写 n 条站内信，返回按写入顺序自增的**最小/最大 ID**用于等值断言。
func newNotifPageStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := New(db)
	if err != nil {
		t.Fatalf("store.New（含迁移）失败: %v", err)
	}
	return st
}

// seedNotifications 写 n 条站内信（标题带序号，便于逐行核对翻页落点）。
func seedNotifications(t *testing.T, st *Store, userID int64, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		if err := st.CreateNotification(userID, fmt.Sprintf("N%03d", i), fmt.Sprintf("正文 %03d", i), "ticket", int64(i)); err != nil {
			t.Fatalf("写入第 %d 条站内信失败: %v", i, err)
		}
	}
}

// idsOf 取列表 ID 序列（顺序即 SQL 返回顺序，判"最新在前"与"两页不重叠"都要它）。
func idsOf(list []*Notification) []int64 {
	out := make([]int64, 0, len(list))
	for _, n := range list {
		out = append(out, n.ID)
	}
	return out
}

// TestNotificationPageDefaultClamp 默认档与上限钳制：0／负数／脏值 → 20，超限 → 100。
// 这条锁的射程是 store 那两个 if：把默认档改成 100 或把上限放开，本条当场红。
func TestNotificationPageDefaultClamp(t *testing.T) {
	st := newNotifPageStore(t)
	seedNotifications(t, st, 1, 105) // 105 > 上限 100：不写够这么多，"钳到 100"和"没钳"都是 105 行，判不出来

	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{"缺参（0）走默认档", 0, NotificationPageDefault},
		{"负数脏参走默认档", -5, NotificationPageDefault},
		{"显式抬档钳到单页上限", 500, NotificationPageMax},
		{"档位内的请求原样生效", 7, 7},
	} {
		got, err := st.ListNotifications(1, tc.limit, 0)
		if err != nil {
			t.Fatalf("%s: 查询失败: %v", tc.name, err)
		}
		if len(got) != tc.want {
			t.Fatalf("%s: 取到 %d 行，期望 %d 行（档位常量＝默认 %d／上限 %d）",
				tc.name, len(got), tc.want, NotificationPageDefault, NotificationPageMax)
		}
		t.Logf("读数：%s → limit=%d 实际 %d 行", tc.name, tc.limit, len(got))
	}
}

// TestNotificationPageOffsetNoOverlap offset 翻页必须逐页不重叠、不跳行，且顺序恒最新在前。
// 脏 offset（负数）折回 0 也在本条测：它与"报错"是两种对外形态，铃铛只认前者。
func TestNotificationPageOffsetNoOverlap(t *testing.T) {
	st := newNotifPageStore(t)
	seedNotifications(t, st, 2, 45) // 45 = 两整页(40) + 半页(5)

	// 三页翻到底：offset 0／20／40 → 20／20／5 行（45 = 两整页 + 半页）。
	// 判据按**页序**逐对判相邻，而不是只比第一页和最后一页——只比首尾会把"中间跳一行"放过去。
	pages := []struct {
		off  int
		want int
	}{{0, NotificationPageDefault}, {20, NotificationPageDefault}, {40, 45 - 40}}
	seen := map[int64]bool{}
	var prev []*Notification
	for i, pg := range pages {
		list, err := st.ListNotifications(2, NotificationPageDefault, pg.off)
		if err != nil {
			t.Fatalf("第 %d 页（offset=%d）查询失败: %v", i+1, pg.off, err)
		}
		if len(list) != pg.want {
			t.Fatalf("第 %d 页（offset=%d）应 %d 行，实际 %d 行", i+1, pg.off, pg.want, len(list))
		}
		// 页内顺序：ID 严格递减（最新在前）
		ids := idsOf(list)
		for j := 1; j < len(ids); j++ {
			if ids[j-1] <= ids[j] {
				t.Fatalf("第 %d 页页内顺序不是最新在前：%v", i+1, ids)
			}
		}
		// 跨页不重叠 + 首尾相接（本页第一条正好是上一页最后一条的下一条）
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("offset=%d 那一页出现重复行 ID=%d ⇒ 翻页步长算歪", pg.off, id)
			}
			seen[id] = true
		}
		if len(prev) > 0 && list[0].ID != prev[len(prev)-1].ID-1 {
			t.Fatalf("第 %d 页与上一页之间有跳行：上页尾 ID=%d，本页首 ID=%d", i+1, prev[len(prev)-1].ID, list[0].ID)
		}
		prev = list
	}
	if len(seen) != 45 {
		t.Fatalf("三页合计只覆盖 %d 条（夹具写了 45 条）⇒ 有行在任何一页都取不到", len(seen))
	}
	// 脏 offset 折回 0：与"报参数错误"是两种对外形态，这里是前者
	p1, err := st.ListNotifications(2, 0, 0)
	if err != nil {
		t.Fatalf("脏参对照页查询失败: %v", err)
	}
	neg, err := st.ListNotifications(2, 0, -9)
	if err != nil {
		t.Fatalf("负 offset 查询失败（脏参应折回而不该报错）: %v", err)
	}
	if len(neg) != NotificationPageDefault || neg[0].ID != p1[0].ID {
		t.Fatalf("负 offset 没折回第一页：%d 行，首 ID=%d（页 1 首 ID=%d）", len(neg), neg[0].ID, p1[0].ID)
	}
	// 越界 offset：空页而不是回绕到第一页
	tail, err := st.ListNotifications(2, 0, 1000)
	if err != nil {
		t.Fatalf("越界 offset 查询失败: %v", err)
	}
	if len(tail) != 0 {
		t.Fatalf("越界 offset 应回空页，实际 %d 行", len(tail))
	}
	t.Logf("读数：45 行→前三页 %d/%d/%d／脏 offset %d 行且首 ID 同页 1／越界 %d 行",
		pages[0].want, pages[1].want, pages[2].want, len(neg), len(tail))
}

// TestNotificationPageUserIsolation 分页后仍按用户隔离：A 的 offset 取不到 B 的行。
// 这条是"加了 OFFSET 就把 WHERE user_id=? 写漏"这类改动的唯一防线——漏了不会报错，
// 只会把别人的站内信混进铃铛，而且恰好只在翻第二页时才现形。
func TestNotificationPageUserIsolation(t *testing.T) {
	st := newNotifPageStore(t)
	seedNotifications(t, st, 7, 30) // 用户 7
	seedNotifications(t, st, 8, 30) // 用户 8（同页数，制造"混进来看不出来"的条件）

	for _, off := range []int{0, 20} {
		list, err := st.ListNotifications(7, 0, off)
		if err != nil {
			t.Fatalf("用户 7 offset=%d 查询失败: %v", off, err)
		}
		for _, n := range list {
			if n.UserID != 7 {
				t.Fatalf("offset=%d 那一页混进了用户 %d 的站内信（ID=%d）⇒ 分页把归属条件写丢了", off, n.UserID, n.ID)
			}
			if n.Title == "" {
				t.Fatalf("offset=%d 取到空标题行，夹具数据本身就不该有（前置坏了）", off)
			}
		}
	}
	// 用户 9 一行没有：分页不许把别人的行补上来
	none, err := st.ListNotifications(9, 0, 0)
	if err != nil {
		t.Fatalf("空数据用户查询失败: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("用户 9 没有站内信，却取到 %d 行", len(none))
	}
	// 读数现算现打（写死数字会在别人改动夹具时变成"看着对"的假读数）
	first, _ := st.ListNotifications(7, 0, 0)
	second, _ := st.ListNotifications(7, 0, 20)
	t.Logf("读数：用户 7 页1 %d 行／页2 %d 行（均归属 7）／用户 9 %d 行", len(first), len(second), len(none))
}
