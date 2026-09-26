// ============ tmreview_tenant_test.go · 职责说明 ============
// ★ F-62（2026-09-26 批 I-8）TM 待审池**租户侧只读视图**的 store 层断言。
//
// 钉的三件事（都对应本轮 UAT 抓到的缺陷形状）：
//
//	① 跨租户隔离：租户 4 的列表里绝不能出现租户 3 的句对，tid<=0 一律空集（不退化成全表）；
//	② 状态白名单归一：三态与「全部」合法，脏值（'pending,approved'／大小写混写）判非法，
//	   而不是被静默当成「全部」返回；
//	③ 摘要是真计数：列表受 200 条上限截断时，summary 仍报全量真数（防「rows.length 冒充总数」）。
//
// 环境：内存 SQLite，方言按 AGENTS.md §一·4 自钉（防 PG 模式泄漏给同包后续用例）。
// 运行：cd backend-go && go test -count=1 ./internal/store/ -run TmReviewTenant
// ========================================
package store

import (
	"database/sql"
	"encoding/json"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// tmTenantEnv 建一个钉死 SQLite 方言的内存 store（本文件专用，不与他包共用环境）。
func tmTenantEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	st, err := New(sqlDB)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// 注意变量名：store 包内 `db` 既是包名（translator/internal/db）也是连接句柄的惯用局部名，
	// 这里刻意叫 sqlDB，免得遮蔽包名后 seedReview 里的 db.Exec / db.CurrentDialect 读错。
	t.Cleanup(func() { sqlDB.Close() })
	return st
}

// tmReviewTenantJSONKeys 把租户侧出参序列化后取 JSON 键集合（投影契约锁：结构体没写的字段就不该出现）。
func tmReviewTenantJSONKeys(t *testing.T, row *TmReviewTenantRow) map[string]bool {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("序列化出参失败: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析出参失败: %v (%s)", err, raw)
	}
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

// seedReview 直插一条候选（绕开 CreateTmReview 的 opt-out/脱敏分支，只测读侧裁剪）。
func seedReview(t *testing.T, st *Store, tid int64, zh, lang, trans, status string) int64 {
	t.Helper()
	res, err := db.Exec(st.db, db.CurrentDialect(),
		`INSERT INTO tm_review (tenant_id, zh, lang, trans, source, ref_type, ref_id, hit_count, status, created_at)
		 VALUES (?,?,?,?, 'hit_threshold', 'ticket', 0, 0, ?, ?)`,
		tid, zh, lang, trans, status, "2026-09-26T00:00:00Z")
	if err != nil {
		t.Fatalf("插入候选失败: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("取候选 ID 失败: %v", err)
	}
	return id
}

// TestTmReviewTenantIsolation ① 跨租户隔离 + 无租户账号空集。
func TestTmReviewTenantIsolation(t *testing.T) {
	st := tmTenantEnv(t)
	// ★ 造数口径（反证逼出来的，别改回「一 pending 一 approved」）：两租户必须**同状态**，
	//   否则「摘掉 tenant 过滤」的退化实现会被残留的 status 条件恰好挡住，
	//   用例照样绿——2026-09-26 实测过一次：过滤条件被换成 WHERE 1=1 后 store 侧仍 ok，
	//   只有 API 侧（查的是 pending 池）翻了红。同状态造数才能让本用例独立抓到隔离失效。
	seedReview(t, st, 3, "租户三的句子", "en", "tenant three", "approved")
	seedReview(t, st, 4, "租户四的句子", "de", "tenant vier", "approved")

	rows, err := st.ListTmReviewsForTenant(4, "approved", false, 200)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("租户 4 只应看到自己 1 条，实际 %d 条（跨租户泄漏的形态就是这个数字变了）", len(rows))
	}
	for _, r := range rows {
		if r.Zh == "租户三的句子" {
			t.Fatal("★ 跨租户泄漏：租户 4 看到了租户 3 的句对")
		}
		if r.Status != "approved" {
			t.Fatalf("状态过滤失效: %+v", r)
		}
	}
	// 「全部」口径同样只回本租户（防实现只在带 status 过滤时裁剪、放开 status 就漏）
	allRows, err := st.ListTmReviewsForTenant(4, "", true, 200)
	if err != nil {
		t.Fatalf("全量查询失败: %v", err)
	}
	if len(allRows) != 1 || allRows[0].Zh != "租户四的句子" {
		t.Fatalf("status=全部 时同样必须按租户裁剪，实际 %+v", allRows)
	}
	// 投影契约：租户侧视图结构体压根没有 tenant_id / reviewer 字段，
	// 这里锁「序列化后的 JSON 键集合」，防有人日后图省事把 store.TmReview 直接换上来。
	jsonKeys := tmReviewTenantJSONKeys(t, rows[0])
	for _, banned := range []string{"tenant_id", "reviewer", "ref_id", "ref_type"} {
		if jsonKeys[banned] {
			t.Fatalf("租户侧出参出现了平台侧字段 %q（字段白名单被破）", banned)
		}
	}
	// tid<=0：平台/无归属账号在本视图没有内容，且**不是**全表
	all, err := st.ListTmReviewsForTenant(0, "", true, 200)
	if err != nil {
		t.Fatalf("tid=0 查询不应报错: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("tid<=0 必须空集，实际返回 %d 条（退化成跨租户读）", len(all))
	}
}

// TestNormTmReviewStatus ② 状态参数归一：三态/全部合法，脏值判非法（不静默放宽成全部）。
func TestNormTmReviewStatus(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantAll bool
		wantOK  bool
	}{{"", "", true, true}, {"  ", "", true, true}, {"all", "", true, true}, {"*", "", true, true},
		{"pending", "pending", false, true}, {"Approved", "approved", false, true},
		{" rejected ", "rejected", false, true},
		{"pending,approved", "", false, false}, {"dropped", "", false, false}, {"%20pending", "", false, false}}
	for _, c := range cases {
		got, all, ok := NormTmReviewStatus(c.in)
		if got != c.want || all != c.wantAll || ok != c.wantOK {
			t.Fatalf("NormTmReviewStatus(%q) = (%q,%v,%v)，期望 (%q,%v,%v)", c.in, got, all, ok, c.want, c.wantAll, c.wantOK)
		}
	}
}

// TestTmReviewTenantSummaryIsRealCount ③ 摘要真计数 + 上限截断时不拿列表长度冒充总数。
func TestTmReviewTenantSummaryIsRealCount(t *testing.T) {
	st := tmTenantEnv(t)
	for i := 0; i < 3; i++ {
		seedReview(t, st, 5, "句对甲", "en", "pair-a", "pending")
	}
	seedReview(t, st, 5, "句对乙", "en", "pair-b", "approved")
	seedReview(t, st, 6, "别租户", "en", "other", "pending")

	sum, err := st.SummarizeTmReviewsForTenant(5)
	if err != nil {
		t.Fatalf("摘要失败: %v", err)
	}
	if sum.Pending != 3 || sum.Approved != 1 || sum.Rejected != 0 || sum.Total != 4 {
		t.Fatalf("摘要计数不符: %+v", sum)
	}
	if sum.Pending+sum.Approved+sum.Rejected != sum.Total {
		t.Fatalf("三态相加应等于总数（否则摘要出现假账）: %+v", sum)
	}
	// 列表上限截断：limit=2 时 rows 只有 2 条，但 summary 仍是全量 4 条
	rows, err := st.ListTmReviewsForTenant(5, "", true, 2)
	if err != nil {
		t.Fatalf("截断查询失败: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("limit=2 应回 2 条，实际 %d", len(rows))
	}
	if int64(len(rows)) == sum.Total {
		t.Fatal("本用例前提是「列表被截断而摘要报真数」，两者相等说明造数失效")
	}
	// tid<=0 的摘要同样必须是全零，不能顺手数别人的池子
	zero, err := st.SummarizeTmReviewsForTenant(0)
	if err != nil {
		t.Fatalf("tid=0 摘要不应报错: %v", err)
	}
	if zero.Total != 0 {
		t.Fatalf("tid<=0 摘要必须为 0，实际 %+v", zero)
	}
}
