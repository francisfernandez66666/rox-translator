// ============ usage_export_walk_test.go · 职责说明 ============
// store 包内部测试文件。
//
// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 体验批 A8：用量明细导出改「分批 keyset ＋ 回调」后的行为闸门，
//
//	  外加日趋势那一腿的谓词形态锁。
//
//		E1 分批与单查询**行集与顺序逐字等值**（oracle＝测试自己写的一条单查询）；
//		E2 到底判据不白打第二次库（不满一批即收）；
//		E3 行数硬上限语义一字未动（limit<=0／>10 万 ⇒ 10 万；给了 limit 就按 id 倒序截最新段）；
//		E4 回调中途报错**立刻中止**且如实回已交付行数（写不下去还继续取数＝白烧一遍库）；
//		E5 chargedOnly 两档共用同一份谓词构造口（客户面导出不许混进平台承担留痕行）；
//		T1 日趋势的时间谓词必须是**裸列范围比较**——把 created_at 包进函数再做谓词比较，
//		   idx_ul_tenant_created 就废了。⚠️ GROUP BY 里出现 substr 是**无害的**（只影响分组，
//		   不影响谓词下推），所以判据精确落在"谓词不许被包"，不是"整句不许有 substr"
//		   ——后一种写法会把正确实现判成缺陷。
//
// 方言：自钉内存 SQLite（AGENTS §一·4 模板，经 newTestStore）。
// =============================================
package store

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/db"
)

// seedLedger 直插 n 行 usage_ledger（同租户同用户）。
// chargedKind 决定这行落进哪个谓词档：'charge'＝客户实扣、'settle'＝无归因留痕。
// created_at 逐行 +1s，保证 id 序与时间序同向（这样"倒序截最新段"两条判据不会互相掩盖）。
func seedLedger(t *testing.T, s *Store, tid, uid int64, n int, chargedKind string) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Duration(n) * time.Second)
	for i := 0; i < n; i++ {
		if _, err := db.Exec(s.db, db.CurrentDialect(),
			"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, biz_kind, biz_mode, charge_kind, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
			tid, uid, fmt.Sprintf("type_%d", i), "p", "m", 1, 0, int64(i+1), "text", "pro", chargedKind,
			base.Add(time.Duration(i)*time.Second).Format(time.RFC3339)); err != nil {
			t.Fatalf("种账页 %d 行失败: %v", i, err)
		}
	}
}

// oracleSingleQuery 测试自己写的一条单查询（改造前那条 SQL 的形状），作为等值对照。
// 刻意不复用 usageExportWhere：两把尺子必须真的来自两处，否则"一起改错"照样绿。
func oracleSingleQuery(t *testing.T, s *Store, tid int64, limit int, chargedOnly bool) []int64 {
	t.Helper()
	q := "SELECT id FROM usage_ledger WHERE tenant_id=?"
	if chargedOnly {
		q += " AND " + CustomerUsagePred
	}
	q += " ORDER BY id DESC LIMIT ?"
	rows, err := db.Query(s.db, db.CurrentDialect(), q, tid, limit)
	if err != nil {
		t.Fatalf("oracle 查询失败: %v", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("oracle scan 失败: %v", err)
		}
		out = append(out, id)
	}
	return out
}

// collectWalk 把分批回调摊成 id 序列。三个返回值分别是：
// 回调里见过的 id、回调次数、以及 **Walk 自己报出的已交付行数**（第三个专给 E4 用：
// 函数自报的数必须与实际交付的数对得上，否则"导出了多少行"这个读数就是在背书谎言）。
func collectWalk(t *testing.T, s *Store, tid int64, limit int, chargedOnly bool, failAfter int) ([]int64, int, int, error) {
	t.Helper()
	var ids []int64
	calls := 0
	n, err := s.WalkUsageLedgerForExport(tid, "", "", limit, chargedOnly, func(b []*UsageLedger) error {
		calls++
		for _, u := range b {
			ids = append(ids, u.ID)
		}
		if failAfter > 0 && len(ids) >= failAfter {
			return fmt.Errorf("客户端断开")
		}
		return nil
	})
	return ids, calls, n, err
}

// TestUsageExportWalkEqualsSingleQuery E1＋E2：分批与单查询逐字等值，且不满一批即收。
func TestUsageExportWalkEqualsSingleQuery(t *testing.T) {
	s := newTestStore(t)
	const tid, uid int64 = 7, 11
	seedLedger(t, s, tid, uid, 3, "charge")
	ids, calls, _, err := collectWalk(t, s, tid, 0, true, 0)
	if err != nil {
		t.Fatalf("Walk 报错: %v", err)
	}
	want := oracleSingleQuery(t, s, tid, 100000, true)
	if len(ids) != len(want) {
		t.Fatalf("行数不等值：分批 %d vs 单查询 %d", len(ids), len(want))
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("第 %d 行 id 不等值：分批 %d vs 单查询 %d（顺序漂＝CSV 行序变了）", i, ids[i], want[i])
		}
	}
	// 3 行 < 页大小 ⇒ 必须只调一次就收。若"到底"判成"取到空批才停"，
	// 每次导出都会多白打一次库——那是把省内存改成费查询。
	if calls != 1 {
		t.Fatalf("3 行应当一批取完，实得 %d 次回调（到底判据漂了）", calls)
	}
}

// TestUsageExportLimitSemanticsUnchanged E3：行数上限与"截最新段"逐字保持。
func TestUsageExportLimitSemanticsUnchanged(t *testing.T) {
	s := newTestStore(t)
	const tid, uid int64 = 8, 11
	seedLedger(t, s, tid, uid, 5, "charge")
	ids, _, _, err := collectWalk(t, s, tid, 2, true, 0)
	if err != nil {
		t.Fatalf("Walk 报错: %v", err)
	}
	want := oracleSingleQuery(t, s, tid, 2, true)
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("limit=2 的截断段不等值：分批 %v vs 单查询 %v", ids, want)
	}
	idsAll, _, _, err := collectWalk(t, s, tid, 0, true, 0)
	if err != nil || len(idsAll) != 5 {
		t.Fatalf("limit=0 应折到硬上限并回全量 5 行，实得 %d 行 err=%v", len(idsAll), err)
	}
	overIDs, _, _, err := collectWalk(t, s, tid, 999999, true, 0)
	if err != nil || len(overIDs) != 5 {
		t.Fatalf("超上限应折到 10 万档并回全量，实得 %d 行 err=%v", len(overIDs), err)
	}
}

// TestUsageExportAbortsOnCallbackError E4：回调报错立刻中止并如实回已交付行数。
func TestUsageExportAbortsOnCallbackError(t *testing.T) {
	s := newTestStore(t)
	const tid, uid int64 = 9, 11
	// 让第一批就装满到能触发 failAfter：把页大小当常量用，这里用 limit=1 分批各 1 行不好使，
	// 所以直接种 5 行、限 failAfter=3 ⇒ 第一批（5 行）交付即触发，中止后不得再取下一批。
	seedLedger(t, s, tid, uid, 5, "charge")
	ids, calls, delivered, err := collectWalk(t, s, tid, 0, true, 3)
	if err == nil {
		t.Fatal("回调错误必须冒出来（吞掉＝交出一份少了行的 CSV 还说成功）")
	}
	if !strings.Contains(err.Error(), "客户端断开") {
		t.Fatalf("错误原文不得改写: %v", err)
	}
	if calls != 1 {
		t.Fatalf("中止后不得再取下一批：回调 %d 次", calls)
	}
	if len(ids) != 5 || delivered != len(ids) {
		t.Fatalf("Walk 自报的已交付行数必须与实际交给回调的行数等值：自报 %d vs 实交付 %d", delivered, len(ids))
	}
}

// TestUsageExportChargedOnlySharesPredicate E5：两档谓词共用同一份构造口。
func TestUsageExportChargedOnlySharesPredicate(t *testing.T) {
	s := newTestStore(t)
	const tid, uid int64 = 10, 11
	seedLedger(t, s, tid, uid, 2, "charge") // 客户实扣行
	// 留痕行必须 **user_id=0** 才被 CustomerUsagePred 挡在外面：
	// 那条谓词的第二段是「NOT (charge_kind IN ('log','settle') AND COALESCE(user_id,0)=0)」，
	// 拿 uid=11 种一行 settle 是在测一个谓词根本没管的形态（首跑就红在这里，属夹具错不是产品错）。
	seedLedger(t, s, tid, 0, 1, "settle") // 无归因留痕行（chargedOnly 必须挡在外面）
	charged, _, _, err := collectWalk(t, s, tid, 0, true, 0)
	if err != nil {
		t.Fatalf("Walk 报错: %v", err)
	}
	all, _, _, err := collectWalk(t, s, tid, 0, false, 0)
	if err != nil {
		t.Fatalf("Walk 报错: %v", err)
	}
	if len(all)-len(charged) != 1 {
		t.Fatalf("chargedOnly 两档差值应为 1 行留痕，实得 charged=%d all=%d", len(charged), len(all))
	}
	viaOld, err := s.UsageLedgerForExport(tid, "", "", 0, true)
	if err != nil {
		t.Fatalf("旧函数报错: %v", err)
	}
	if len(viaOld) != len(charged) {
		t.Fatalf("旧 UsageLedgerForExport 与 Walk 行数不等（%d vs %d）——谓词被抄成两份了", len(viaOld), len(charged))
	}
}

// TestUsageTrendPredicateIsIndexFriendly T1：日趋势的时间谓词必须是裸列范围比较。
func TestUsageTrendPredicateIsIndexFriendly(t *testing.T) {
	b, err := os.ReadFile("billing.go")
	if err != nil {
		t.Fatalf("读 billing.go 失败（cwd 应在 internal/store）: %v", err)
	}
	src := stripGoLineComments(string(b))
	body := gateFuncBody(t, src, "func (s *Store) UsageTrend(")
	if strings.Contains(body, "substr(created_at,1,10)>=?") || strings.Contains(body, "substr(created_at,1,10)<=") {
		t.Fatal("UsageTrend 把时间列包进函数再做**谓词**比较——idx_ul_tenant_created 会被废掉")
	}
	if !strings.Contains(body, "created_at>=?") {
		t.Fatal("UsageTrend 没有裸列范围比较（谓词形状变了，索引判据失效）")
	}
	// 射程自证：GROUP BY 里那枚 substr **允许**存在（不许有人把它当缺陷"修"掉分组形状）
	if !strings.Contains(body, "substr(created_at,1,10)") {
		t.Fatal("日分组形状变了（GROUP BY 里不再按日截断）——本锁的判据前提要重读")
	}
}
