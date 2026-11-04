// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 超时与慢查询批的**索引闸**（配套 indexperf_migrate.go）。
//
// 为什么必须有它：建索引失败是**没有症状**的——建不上时查询照样返回正确结果，只是慢；
// 慢被用户读成"后端坏了"、被运维读成"网络慢"、被下一位读成"这条索引本来就是装饰"。
// ⇒ 正腿不写成"源码里出现过 CREATE INDEX 字样"（那是判据级），而是**真驱动跑一遍迁移再问库里有没有**
// （AGENTS §三 那条"判生效必须由那一个消费方自己读出值来"）。
//
// 五道腿：
//
//	P1 清单是唯一写点：别的文件再建同一个索引名 ⇒ 红（两处建法迟早漂）；
//	P2 真驱动跑完迁移后，清单里每一条都必须能在 sqlite_master 里查到，且列形含预期列；
//	P3 倒序形状：这些查询都是「按 id/时间 DESC 取前 N」，被改回升序时 SQLite 会实打实排序，
//	   所以点名哪几条必须含 DESC（不点名"所有索引都必须 DESC"——那是错的，等值过滤用的索引不需要）；
//	P4 自证：把清单删掉一条／整段清空喂给**同一个函数** ⇒ 必须逐条报出缺失（防空集恒真）；
//	P5 幂等：同一块库再跑一次迁移不报错、索引不翻倍（老库启动自动补的形态）。
//
// 方言口径：本文件只跑 SQLite（PG 由 run_uat.sh 的 PG 矩阵承担，见 AGENTS §一·4）。
// 这里刻意**不**断"查询计划必须走该索引"——小表上规划器选 seq scan 是正当行为，
// 钉计划只会得到一条与性能无关的脆锁（同 jobs_index_gate_test.go 的取舍与理由）。
// ============================================
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"translator/internal/config"

	_ "modernc.org/sqlite"
)

// idxNameRe 从 DDL 里抽索引名（清单与建法的一致性靠它，不靠人抄）。
var idxNameRe = regexp.MustCompile(`CREATE INDEX IF NOT EXISTS\s+([A-Za-z0-9_]+)`)

// newIdxPerfStore 建一块**文件库**跑完整迁移链。
// 为什么不用 ":memory:"：那是"每个连接一个库"，迁移落在连接 A、断言查询落在连接 B 时
// 会得到 no such table 假红（本仓踩过，见项目记忆）。文件库物理上排除这一维。
func newIdxPerfStore(t *testing.T) *sql.DB {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "idxperf.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := New(raw); err != nil {
		t.Fatalf("跑迁移链失败: %v", err)
	}
	return raw
}

// indexMissing 是 P2/P4 共用的判据函数：给定"库里实际存在的索引名"与"清单"，报出缺了哪几条。
// ⚠️ 必须是**纯函数**并且被**变异体喂同一个函数**——否则 P4 只是在测另一件事。
func indexMissing(have map[string]bool, specs []indexSpec) []string {
	var out []string
	for _, s := range specs {
		if !have[s.name] {
			out = append(out, s.name)
		}
	}
	return out
}

// TestIndexPerfAllCreatedInRealDB P2：真驱动跑完迁移，逐条断言"这个索引现在在库里"。
func TestIndexPerfAllCreatedInRealDB(t *testing.T) {
	raw := newIdxPerfStore(t)
	specs := IndexPerfSpecs()
	if len(specs) < 8 {
		t.Fatalf("清单只有 %d 条 ⇒ 与本批点名的四张表对不上（少建的表就是还在全表扫的那张）", len(specs))
	}
	rows, err := raw.Query("SELECT name, tbl_name, sql FROM sqlite_master WHERE type='index'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	cols := map[string]string{}
	tbl := map[string]string{}
	for rows.Next() {
		var n, tn string
		var s sql.NullString
		if err := rows.Scan(&n, &tn, &s); err != nil {
			t.Fatal(err)
		}
		have[n] = true
		cols[n] = s.String
		tbl[n] = tn
	}
	if miss := indexMissing(have, specs); len(miss) > 0 {
		t.Fatalf("清单里有 %d 条索引没建成（这些查询将继续全表扫，而界面上一切正常）：%s",
			len(miss), strings.Join(miss, ", "))
	}
	// 清单里的 name 必须与 DDL 里抽出来的名字一致（防"名字改了两边漂"：
	// 回验按 name 查库，而实际建的是另一个名字 ⇒ 查不到会被误读成"迁移没跑"）。
	for _, s := range specs {
		m := idxNameRe.FindStringSubmatch(s.ddl)
		if len(m) != 2 || m[1] != s.name {
			t.Errorf("%s 的清单名与 DDL 里的索引名不一致：%q", s.name, s.ddl)
		}
	}
}

// TestIndexPerfColumnShape P2 的第二半：在位还不够，**列形必须罩住那条真实查询**。
func TestIndexPerfColumnShape(t *testing.T) {
	raw := newIdxPerfStore(t)
	want := []struct {
		idx, table string
		cols       []string
	}{
		{"idx_notifications_user_id", "notifications", []string{"user_id", "id"}},
		{"idx_tickets_tenant_id", "tickets", []string{"tenant_id", "id"}},
		{"idx_tickets_status_id", "tickets", []string{"status", "id"}},
		{"idx_audit_created_id", "audit_logs", []string{"created_at", "id"}},
		{"idx_ul_tenant_id", "usage_ledger", []string{"tenant_id", "id"}},
		{"idx_users_tenant_id", "users", []string{"tenant_id", "id"}},
		{"idx_apikeys_tenant_id", "api_keys", []string{"tenant_id", "id"}},
		{"idx_orders_status_pending", "orders", []string{"status", "manual_confirm", "id"}},
	}
	for _, w := range want {
		var gotTable, gotSQL string
		err := raw.QueryRow("SELECT tbl_name, sql FROM sqlite_master WHERE type='index' AND name=?", w.idx).Scan(&gotTable, &gotSQL)
		if err != nil {
			t.Errorf("%s 查不到: %v", w.idx, err)
			continue
		}
		if gotTable != w.table {
			t.Errorf("%s 挂在 %s 上，应为 %s", w.idx, gotTable, w.table)
		}
		for _, c := range w.cols {
			if !strings.Contains(gotSQL, c) {
				t.Errorf("%s 的列形缺 %q：%s", w.idx, c, gotSQL)
			}
		}
	}
}

// TestIndexPerfDescKept P3：倒序形状不许被改软。
func TestIndexPerfDescKept(t *testing.T) {
	needDesc := map[string]bool{
		"idx_notifications_user_id": true,
		"idx_tickets_tenant_id":     true,
		"idx_tickets_status_id":     true,
		"idx_audit_created_id":      true,
		"idx_ul_tenant_id":          true,
		"idx_apikeys_tenant_id":     true,
		"idx_orders_status_pending": true,
	}
	for _, s := range IndexPerfSpecs() {
		if !needDesc[s.name] {
			continue
		}
		if !strings.Contains(s.ddl, "DESC") {
			t.Errorf("%s 丢了 DESC ⇒ 取「最近 N 条」那条形状在 SQLite 上要重新全排序；DDL: %s", s.name, s.ddl)
		}
	}
	// 反向自证：这张"必须含 DESC"的名单本身不能是空的，否则上面那条循环恒真。
	if len(needDesc) < 5 {
		t.Errorf("DESC 名单只剩 %d 条 ⇒ 这条腿在空转", len(needDesc))
	}
}

// TestIndexPerfGateIsNotVacuous P4：判据函数对"少建一条"和"整段没建"都必须报得出来。
// 这一条是防"闸门自己失明"——只测正向在位的话，清单被清空时 P2 也会全绿。
func TestIndexPerfGateIsNotVacuous(t *testing.T) {
	specs := IndexPerfSpecs()
	full := map[string]bool{}
	for _, s := range specs {
		full[s.name] = true
	}
	if miss := indexMissing(full, specs); len(miss) != 0 {
		t.Fatalf("全量在位时不该报缺失，实得 %v", miss)
	}
	// 摘掉任意一条 ⇒ 恰报那一条
	one := map[string]bool{}
	for k := range full {
		one[k] = true
	}
	drop := specs[0].name
	delete(one, drop)
	miss := indexMissing(one, specs)
	if len(miss) != 1 || miss[0] != drop {
		t.Errorf("摘掉 %s 后应恰报它一个，实得 %v", drop, miss)
	}
	// 空库 ⇒ 全报（防"清单为空"与"库里什么都没有"同形）
	if all := indexMissing(map[string]bool{}, specs); len(all) != len(specs) {
		t.Errorf("什么都没建时应报全部 %d 条，实得 %d 条", len(specs), len(all))
	}
	if none := indexMissing(full, nil); len(none) != 0 {
		t.Errorf("空清单不该凭空报东西: %v", none)
	}
	// 清单本身不许空（空清单会让上面三条断言全部"正确地"什么都不知道）
	if len(specs) == 0 {
		t.Fatal("索引清单为空 ⇒ 本闸整段失明")
	}
}

// TestIndexPerfSingleSource P1：清单是唯一写点。
//
// 为什么单独钉一条：同一批里 idx_usage_tenant_user 就有两处建法（建表段一份、补列迁移段一份），
// 那是历史真实存在的形态。两处建法迟早漂——一处加了列、另一处没加，
// 最后"库里到底哪个版本生效"要看谁跑得晚，而这种事在界面上一行读数都没有。
func TestIndexPerfSingleSource(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("扫不到本包源文件（射程为 0 的锁等于没有锁）: %v", err)
	}
	scanned := 0
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") || p == "indexperf_migrate.go" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s: %v", p, err)
		}
		scanned++
		// ★ 必须先剥注释再判：本仓吃过两次亏——登记注释里点名索引名是**尽责**的写法，
		//   不剥注释就会把"记下了这条索引建在哪"读成"第二处建法"（AGENTS §一·5 那条
		//   「裁决＝给锁补剥注释的形，不删注释」的同一裁决；本轮首跑真踩在自己写的迁移链注释上）。
		code := stripGoLineComments(string(b))
		for _, s := range IndexPerfSpecs() {
			if strings.Contains(code, s.name) {
				t.Errorf("索引 %s 在 %s 里又出现一次 ⇒ 两处建法（清单唯一写点是 indexperf_migrate.go）", s.name, p)
			}
		}
	}
	if scanned < 30 {
		t.Fatalf("只扫了 %d 个非测试文件 ⇒ 射程不对，上面那条“没找到”属假绿", scanned)
	}
	// 自证（假目标注入）：剥注释之后**代码里**真出现第二处建法时必须报得出来。
	// 没有这一腿，"注释误伤"修完之后这条锁可能退化成"什么都看不见"——
	// 那是比假红更坏的形态（本仓原话：被摘掉的闸门比红灯更坏）。
	if strings.Contains(stripGoLineComments("\t// idx_notifications_user_id 只是注释里提一句\n"), "idx_notifications_user_id") {
		t.Error("剥注释失效：整行注释里的索引名仍被看见 ⇒ P1 会假红，下一位的处置是摘锁")
	}
	if !strings.Contains(stripGoLineComments("\t`CREATE INDEX IF NOT EXISTS idx_notifications_user_id ON notifications(user_id)`\n"), "idx_notifications_user_id") {
		t.Error("判据失明：代码里的索引名被一起吃掉了 ⇒ P1 恒绿，比没有锁更坏")
	}
	// 刻意不断"下标等于几"——那是位置假设，改一下缩进就恒红（AGENTS §三 那条
	// "判据不许对随机串/分隔符做结构假设"的同一族）。问的是**该留的留着、该掉的掉**。
	line := "\tx := \"https://a/b\" // 注释"
	i := indexCommentStart(line)
	if i < 0 {
		t.Fatal("行尾注释没被认出来（引号内的 // 与真注释分不开 ⇒ 要么削掉代码、要么留下注释）")
	}
	if !strings.Contains(line[:i], "https://a/b") {
		t.Errorf("引号内的 // 被当注释切了 ⇒ 代码会被削掉：%q", line[:i])
	}
	if strings.Contains(line[:i], "注释") {
		t.Errorf("真注释没被切掉：%q", line[:i])
	}
}

// stripGoLineComments 剥掉 Go 源码里的注释，只留代码行参与"第二处建法"的判断。
//
// 刻意只做**行级**处理（整行注释丢掉、行尾注释从 `//` 处截断），不写跨行块注释匹配：
// 本包没有 `/* */` 块注释，而一个粗糙的跨行匹配会把字符串里的 `/*` 也吃掉
// ——那种"顺手把真代码削掉"的剥法比不剥更坏（同 §一·5 那条踩坑记录）。
// 行尾截断还要绕开一种情况：`https://…` 里的 `//` 在引号内，不能当注释切。
func stripGoLineComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			continue
		}
		if i := indexCommentStart(line); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// indexCommentStart 找行尾注释的起点；引号内的 `//`（URL 等）不算。
func indexCommentStart(line string) int {
	inStr := false
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				inStr = false
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			inStr = true
			quote = c
		case '/':
			if i+1 < len(line) && line[i+1] == '/' {
				return i
			}
		}
	}
	return -1
}

// TestIndexPerfMigrateIdempotent P5：同一块库再跑一次迁移不得报错、索引不得翻倍。
func TestIndexPerfMigrateIdempotent(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "idem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	if _, err := New(raw); err != nil {
		t.Fatalf("首次迁移: %v", err)
	}
	if _, err := New(raw); err != nil {
		t.Fatalf("重复迁移报错（IF NOT EXISTS 腿失效）: %v", err)
	}
	for _, s := range IndexPerfSpecs() {
		var n int
		if err := raw.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", s.name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s 复跑后份数为 %d（应为 1）", s.name, n)
		}
	}
}
