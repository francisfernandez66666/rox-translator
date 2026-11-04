// ============ stats_agg_equals_test.go · 职责说明 ============
// ★ 2026-11-04 体验批 A9 的等值锁：kb.Stats 从「逐行拉 34 列进 Go 再按语种数」改成
// 「库侧 SUM(CASE…) 聚合」之后，读数必须与**改造前那套 Go 语义**逐键等值。
//
// 判据的"另一把尺子"是**测试自己写的 oracle**（不是留一份生产死码当对照）：
// 直接把 tm_segments 的语种列整行读回来，按 Go 的 strings.TrimSpace 数一遍，
// 再与 Stats() 的库侧聚合比。这样"改造前的语义"被钉在测试里，将来谁改了谓词而没改另一把，
// 在这一条上现形，而不是在客户的首屏数字上现形。
//
// 三条必测形态（一条不测就是给静默差异放行）：
//
//	W1 正常值 ⇒ 计入；
//	W2 NULL / 空串 / 纯空格 ⇒ **不计入**；
//	W3 制表符、换行、CR、**不换行空格 U+00A0** ⇒ 也不计入。
//	   这一档是本次改造唯一真正有牙的地方：SQL 的 TRIM/BTRIM 默认只剥空格，
//	   照默认写就把 W3 算成非空——而 W3 值在库里确实存在（模型回包含换行的译文段）。
//	W4 某语种一条都没有 ⇒ 那个键**整条缺席**（不许出 0；前端把"没覆盖"与"覆盖 0 条"读成两件事）。
//
// =============================================
package kb

import (
	"database/sql"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// statsOracle 按**改造前的 Go 语义**独立数一遍各语种非空条数。
// 刻意不复用 Stats 的任何 SQL：两把尺子必须真的来自两处。
func statsOracle(t *testing.T, k *KBDatabase, tenantID int64) map[string]int {
	t.Helper()
	cols := strings.Split(langCols, ",")
	if len(cols) != len(AllLangs) {
		t.Fatalf("langCols 与 AllLangs 长度漂了（%d vs %d）", len(cols), len(AllLangs))
	}
	for i, c := range cols {
		cols[i] = strings.TrimSpace(c)
	}
	rows, err := db.Query(k.RawDB(), db.CurrentDialect(),
		"SELECT "+strings.Join(cols, ",")+" FROM tm_segments WHERE tenant_id=?", tenantID)
	if err != nil {
		t.Fatalf("oracle 读行失败: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	vals := make([]interface{}, len(cols))
	for i := range vals {
		vals[i] = new(sql.NullString)
	}
	for rows.Next() {
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("oracle Scan 失败: %v", err)
		}
		for i, lc := range AllLangs {
			v := vals[i].(*sql.NullString)
			if v.Valid && strings.TrimSpace(v.String) != "" {
				out[lc]++
			}
		}
	}
	return out
}

// TestStatsAggEqualsOracle W1–W4：库侧聚合与改造前 Go 语义逐键等值。
func TestStatsAggEqualsOracle(t *testing.T) {
	old := config.C
	cfg := config.Default()
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	k, err := Open(t.TempDir() + "/kb_stats.db")
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer k.Close()

	// 语种清单的前三个拿来当靶子（不写死具体语种名：换语种表时这条不该一起红）
	if len(AllLangs) < 3 {
		t.Skipf("语种表不足 3 个，夹具无法铺开")
	}
	l1, l2, l3 := AllLangs[0], AllLangs[1], AllLangs[2]

	// ① 正常值 ⇒ 计入
	if _, err := k.SaveBack("统计夹具 A", map[string]string{l1: "alpha", l2: "beta"}, "stats", 1); err != nil {
		t.Fatalf("SaveBack A 失败: %v", err)
	}
	// ② 空串／纯空格 ⇒ 不计入（有些语种留空是常态）
	if _, err := k.SaveBack("统计夹具 B", map[string]string{l1: "", l2: "   "}, "stats", 1); err != nil {
		t.Fatalf("SaveBack B 失败: %v", err)
	}
	// ③ 制表／换行／CR／不换行空格 ⇒ 也不计入（SQL 默认 TRIM 够不到这一族，见文件头 W3）
	if _, err := k.SaveBack("统计夹具 C", map[string]string{
		// l3 这一格刻意放**不换行空格 U+00A0**（Go 的 TrimSpace 认它，SQL 默认 TRIM 不认）；
		// 写成转义而不是裸字符：肉眼分不出它与普通空格，判据不能吃歧义字面。
		l1: "\t", l2: "\n", l3: "\u00a0",
	}, "stats", 1); err != nil {
		t.Fatalf("SaveBack C 失败: %v", err)
	}
	if _, err := k.SaveBack("统计夹具 D", map[string]string{l1: "\r", l2: "\r\n"}, "stats", 1); err != nil {
		t.Fatalf("SaveBack D 失败: %v", err)
	}
	// ④ 直接 SQL 塞一行 NULL（SaveBack 不会写 NULL，而改造前那把尺子对 NULL 有一支专门的腿）
	if _, err := db.Exec(k.RawDB(), db.CurrentDialect(),
		"INSERT INTO tm_segments (zh, tenant_id, "+l3+") VALUES ('统计夹具 E', 1, NULL)"); err != nil {
		t.Fatalf("塞 NULL 行失败: %v", err)
	}

	total, perLang, seg, err := k.Stats(1)
	if err != nil {
		t.Fatalf("Stats 报错: %v", err)
	}
	if seg != 0 {
		t.Fatalf("segCount 口径不该变（改造前恒 0），实得 %d", seg)
	}
	if total != 5 {
		t.Fatalf("总条数应为 5 行，实得 %d", total)
	}
	want := statsOracle(t, k, 1)
	for lc, n := range want {
		if perLang[lc] != n {
			t.Fatalf("语种 %s 读数不等值：库侧聚合 %d vs 改造前 Go 语义 %d", lc, perLang[lc], n)
		}
	}
	// 反向逐键：聚合侧多算出来的键（含把空白算成非空）必须被抓到
	for lc, n := range perLang {
		if want[lc] != n {
			t.Fatalf("语种 %s 聚合侧多算/少算：聚合 %d vs oracle %d", lc, n, want[lc])
		}
	}
	// W3 的点名判据：三个靶子里，只有"有正常值"的那两个语种该计数，第三个（只吃了
	// \t / NBSP / NULL 的那一列）必须**整条缺席**——这条不点名就退化成"两边一起错也算等值"
	if perLang[l1] != 1 {
		t.Fatalf("%s 应恰 1 条非空（其余是 \\t、\\r、NULL），实得 %d", l1, perLang[l1])
	}
	if perLang[l2] != 1 {
		t.Fatalf("%s 应恰 1 条非空（其余是空格、\\n、\\r\\n），实得 %d", l2, perLang[l2])
	}
	if _, ok := perLang[l3]; ok {
		t.Fatalf("%s 只有空串/不换行空格/NULL，键必须整条缺席，实得 %d（W4 那一档漂了）", l3, perLang[l3])
	}
	// 射程自证：oracle 自己不是空转——它必须真的读到过 5 行里的这些列
	if len(want) == 0 {
		t.Fatal("oracle 一个语种都没数到（夹具没落库或列名漂了），本锁是空转")
	}
}

// TestStatsOtherTenantIsolated 租户隔离：换租户的 Stats 不得串读数（这条以前也没破，收进来防"聚合改写时漏谓词"）。
func TestStatsOtherTenantIsolated(t *testing.T) {
	old := config.C
	cfg := config.Default()
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	k, err := Open(t.TempDir() + "/kb_stats_iso.db")
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer k.Close()
	if _, err := k.SaveBack("隔离夹具", map[string]string{AllLangs[0]: "only-tenant-1"}, "stats", 1); err != nil {
		t.Fatalf("SaveBack 失败: %v", err)
	}
	total, perLang, _, err := k.Stats(2)
	if err != nil {
		t.Fatalf("Stats 报错: %v", err)
	}
	if total != 0 {
		t.Fatalf("租户 2 不该看到租户 1 的行，实得 total=%d", total)
	}
	if len(perLang) != 0 {
		t.Fatalf("租户 2 的 perLang 必须空（出 0 也算漂），实得 %v", perLang)
	}
}
