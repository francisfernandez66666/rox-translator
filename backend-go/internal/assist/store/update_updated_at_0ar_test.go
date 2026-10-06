// ============ update_updated_at_0ar_test.go · 职责说明 ============
// 锁住「通用更新腿要不要补时间戳列」这一判据（★ 0AR 第 4 波，现网实证的管理台 500）。
//
// 缺陷本体：`DB.Update` 过去拿一张**手写表名名单**判断「这张表没有 updated_at 列」，
// `feature_links` 不在那份名单上、库里也确实没有这一列，于是给功能卡发一次 PUT
// 得到的是一句解析期就失败的 SQL（`no such column: updated_at`），
// 管理台侧回 500 ⇒ 运营**改不动也停不掉任何一张功能入口卡**。
//
// 四条断言各自挡住的一种退化（每条都对应一次可以把判据写歪的方向）：
//  1. 没有该列的表写得进去（把派生判据换回名单，或者将来又漏一张表 ⇒ 这条红）；
//  2. 有该列的表**仍然**要盖时间戳（把追加那一腿整个删掉也能让 1 变绿 ⇒ 这条替它作证，
//     正向对照不是可选项：只有负向锁的修复等于把功能拆了还没人看见）；
//  3. 探测失败按「没有」处理，且**不进缓存**（翻成「有」＝让一次瞬时故障打回 500；
//     把失败缓存下来＝一次抖动永久定格成「这张表没有时间戳列」）；
//  4. 那张缓存 map 的两个出入口必须持锁（裸 map 并发写在生产是 runtime fatal error：
//     recover 兜不住、整台挂件进程挂）。⚠️ 这一条的射程**只能由 ② 那条纯 map 并发腿提供**：
//     反证实测「并发 Update 那一批」摘掉互斥锁仍然绿——这个库只有一条连接（SetMaxOpenConns(1)），
//     并发 goroutine 全排在 Query 上，map 读写被连接池顺带串行化了。
//     所以 map 那两行被拆成 cachedUpdatedAt／rememberUpdatedAt，判据绕开连接池直接打它们。
//
// =============================================
package store

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// seededUpdatedAt 一个**不可能**由 CURRENT_TIMESTAMP 生成的哨兵值：
// 拿它当"改之前"的读数，改之后只要不等于它，就证明时间戳那一腿真的写进去了
// （比"两次读到的字符串不同"可靠——SQLite 的 CURRENT_TIMESTAMP 只到秒，
// 同一秒内连改两次会得到完全相同的值，那种判据是一台会漏报的闸门）。
const seededUpdatedAt = "1970-01-01 00:00:00"

// rawExec 测试侧的直写通道（只为把哨兵值种进去；生产代码一律走通用 CRUD）。
func rawExec(t *testing.T, db *DB, q string, args ...any) {
	t.Helper()
	if _, err := db.sql.Exec(q, args...); err != nil {
		t.Fatalf("直写失败 %q：%v", q, err)
	}
}

// readCell 读一个单元格的字符串形态（时间戳列在两种驱动下可能回 string 也可能回 []byte）。
func readCell(t *testing.T, db *DB, table, col string, id int64) string {
	t.Helper()
	row, err := db.Get(table, id)
	if err != nil {
		t.Fatalf("读 %s#%d 失败：%v", table, id, err)
	}
	switch v := row[col].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// TestUpdateTableWithoutUpdatedAtColumn 没有 updated_at 列的表必须改得动（缺陷本体）。
func TestUpdateTableWithoutUpdatedAtColumn(t *testing.T) {
	db := newTestDB(t)

	id, err := db.Create("feature_links", map[string]any{
		"key": "billing", "name": "充值与账单", "url": "https://example.test/billing",
		"ftype": "route", "icon": "wallet", "sort": 10, "enabled": 1,
	})
	if err != nil {
		t.Fatalf("种功能卡失败：%v", err)
	}
	if err := db.Update("feature_links", id, map[string]any{"name": "账单与充值", "enabled": 0}); err != nil {
		t.Fatalf("改功能卡失败（这就是现网管理台那个 500）：%v", err)
	}
	row, err := db.Get("feature_links", id)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if row["name"] != "账单与充值" {
		t.Fatalf("名字没写进去：%v", row["name"])
	}
	if en, ok := row["enabled"].(int64); !ok || en != 0 {
		t.Fatalf("停用没写进去（运营关不掉一张卡）：%#v", row["enabled"])
	}

	// 同族第二张表：sessions_base 也没有这一列（旧名单里就有它），一并钉住，
	// 防「只把 feature_links 补进名单」那种**把名单继续抄长**的假修法。
	if has := db.hasUpdatedAt("sessions_base"); has {
		t.Fatal("sessions_base 明明没有 updated_at 列，探测却返回了「有」")
	}

	// 反向对照（判据射程）：探测必须真的在问**库里的表结构**，而不是把两张表名硬编码成新名单。
	// 造一张运行期才出现、且带 updated_at 列的表——名单式判据对它必然答「有」（不在名单上），
	// 派生式判据答「有」才是对的；这张表就是「判据没退化成名单」的证据。
	rawExec(t, db, `CREATE TABLE probe_with_ts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, updated_at DATETIME)`)
	if !db.hasUpdatedAt("probe_with_ts") {
		t.Fatal("运行期新建且带该列的表被判成没有 ⇒ 判据又成了按表名查名单")
	}
	rawExec(t, db, `CREATE TABLE probe_without_ts (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)`)
	if db.hasUpdatedAt("probe_without_ts") {
		t.Fatal("运行期新建且不带该列的表被判成有 ⇒ 会拼出一句解析期就失败的 SQL")
	}
}

// TestUpdateStillBumpsUpdatedAtWhenColumnExists 有该列的表继续盖时间戳（正向对照）。
func TestUpdateStillBumpsUpdatedAtWhenColumnExists(t *testing.T) {
	db := newTestDB(t)

	id, err := db.Create("kb_entries", map[string]any{
		"key": "k-ts", "category": "usage", "title": "T", "content": "C",
		"keywords": "a", "link_keys": "", "priority": 5, "enabled": 1,
	})
	if err != nil {
		t.Fatalf("种 kb 失败：%v", err)
	}
	// 先把时间戳种成哨兵，再走通用更新：改后必须**离开**哨兵。
	// ⚠️ 不拿字面串做等值：这一列在建表语句里是 **DATETIME**，驱动会把它读成 time.Time
	// （实测读回 `"1970-01-01 00:00:00 +0000 UTC"`），拿种进去的字面量直接比会**恒不等**——
	// 那是一台"怎么改都绿"的判据。正确做法＝同一个读法各读一次，比**改前改后**。
	rawExec(t, db, `UPDATE kb_entries SET updated_at=? WHERE id=?`, seededUpdatedAt, id)
	before := readCell(t, db, "kb_entries", "updated_at", id)
	if !strings.Contains(before, "1970") {
		t.Fatalf("哨兵没种上，改前读到 %q（这条判据的基线不成立）", before)
	}
	if err := db.Update("kb_entries", id, map[string]any{"title": "T2"}); err != nil {
		t.Fatalf("更新失败：%v", err)
	}
	got := readCell(t, db, "kb_entries", "updated_at", id)
	if got == before || strings.TrimSpace(got) == "" {
		t.Fatalf("updated_at 没被刷新（改前 %q / 改后 %q）——追加那一腿被摘掉了；kb 的向量索引重建靠这一列，静默失效最贵", before, got)
	}

	// scripts／flows 同样有这一列，一并点名（防止"探测只对某几张表准"）。
	for _, tbl := range []string{"scripts", "flows"} {
		if !db.hasUpdatedAt(tbl) {
			t.Fatalf("%s 建表语句里明明有 updated_at，探测却说没有", tbl)
		}
	}
	for _, tbl := range []string{"feature_links", "configs", "sessions_base", "messages_base"} {
		if db.hasUpdatedAt(tbl) {
			t.Fatalf("%s 没有 updated_at 列，探测却说有（翻兜底方向的退化）", tbl)
		}
	}
}

// TestUpdatedAtProbeIsCachedAndFailsSafe 探测结果的缓存纪律与失败兜底方向。
func TestUpdatedAtProbeIsCachedAndFailsSafe(t *testing.T) {
	db := newTestDB(t)

	if !db.hasUpdatedAt("kb_entries") {
		t.Fatal("kb_entries 应判为有该列")
	}
	if _, ok := db.cachedUpdatedAt("kb_entries"); !ok {
		t.Fatal("成功探测的结果没进缓存（每张表每次写都要多一次 PRAGMA，等于没缓存）")
	}
	// 不存在的表：PRAGMA 在 SQLite 下**不报错、退空结果集** ⇒ 判「没有」。
	// 这是一个成功的探测拿到的是一个真实答案（这张表确实没有该列），缓存它没问题；
	// 而 Update 一张不存在的表本来就会在解析期失败，错误由那一句如实返回，不靠这里拦。
	if db.hasUpdatedAt("no_such_table_0ar") {
		t.Fatal("不存在的表被判成有该列")
	}

	// 真正的**探测失败**（连接都不通）必须走「宁可不盖时间戳」那一支，且**不进缓存**：
	// 把一次瞬时故障缓存下来，等于让它永久定格成「这张表没有时间戳列」——
	// 那是把"抖一下"升级成"这台库从此不再记录任何改动时刻"，而没有任何一行日志会说这件事。
	if err := db.Close(); err != nil {
		t.Fatalf("关库失败：%v", err)
	}
	if db.hasUpdatedAt("scripts") {
		t.Fatal("库都关了还能探出「有该列」")
	}
	if _, ok := db.cachedUpdatedAt("scripts"); ok {
		t.Fatal("一次失败的探测被缓存成了永久答案")
	}
	t.Cleanup(func() { _ = db.Close() }) // 幂等，防后面的分支再拿已关的库跑一次
}

// TestUpdatedAtProbeConcurrentAccess 并发写混合表时，缓存 map 不能裸奔（配 -race 跑）。
func TestUpdatedAtProbeConcurrentAccess(t *testing.T) {
	db := newTestDB(t)

	kbID, err := db.Create("kb_entries", map[string]any{
		"key": "k-race", "title": "T", "content": "C", "enabled": 1,
	})
	if err != nil {
		t.Fatalf("种 kb 失败：%v", err)
	}
	flID, err := db.Create("feature_links", map[string]any{
		"key": "fl-race", "name": "N", "url": "u", "enabled": 1,
	})
	if err != nil {
		t.Fatalf("种功能卡失败：%v", err)
	}

	wg := sync.WaitGroup{}
	mu := sync.Mutex{}
	errs := []string{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = db.Update("kb_entries", kbID, map[string]any{"title": fmt.Sprintf("T%d", i)})
			} else {
				err = db.Update("feature_links", flID, map[string]any{"name": fmt.Sprintf("N%d", i)})
			}
			if err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("并发更新出错：%v", errs)
	}
	if got := readCell(t, db, "feature_links", "name", flID); !strings.HasPrefix(got, "N") {
		t.Fatalf("功能卡名字读数不对：%q", got)
	}

	// ② **纯 map 那一段**的并发腿（不碰库）。
	//
	// 为什么上面那批并发 Update 不足以证明互斥锁有用：这个库只有**一条**连接
	// （SetMaxOpenConns(1)），并发的 goroutine 全排在 `Query` 上，map 读写被连接池顺带串行化了，
	// 于是"把 d.colsMu 摘掉"这种破坏在这条腿上**测不出来**（反证实跑读数：仍绿）。
	// 判据要打到那两行 map 操作本身，就必须绕开连接池：这里直接并发调
	// cachedUpdatedAt／rememberUpdatedAt 那两个唯一出入口，读的是**没探过的**表名
	// ⇒ 一边写新条目、一边读整张 map，摘锁后 `-race` 当场报（裸 map 并发写在生产是
	// runtime fatal error：recover 兜不住、整台挂件进程挂，表现是偶发连接被重置）。
	wg2 := sync.WaitGroup{}
	for i := 0; i < 12; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			for n := 0; n < 200; n++ {
				tbl := fmt.Sprintf("hammer_%d_%d", i, n)
				db.rememberUpdatedAt(tbl, n%2 == 0)
				_, _ = db.cachedUpdatedAt(tbl)
				_, _ = db.cachedUpdatedAt(fmt.Sprintf("hammer_%d_%d", (i+1)%12, n))
			}
		}(i)
	}
	wg2.Wait()
	if _, ok := db.cachedUpdatedAt("hammer_0_0"); !ok {
		t.Fatal("并发写之后缓存读数丢了（写腿被并发覆盖掉了一格）")
	}
}
