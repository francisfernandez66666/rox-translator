// ============ 本文件职责中文说明 ============
// 跨单持久译文记忆（缓存 B）的数据访问层闸门锁 —— ★ 2026-10-28 计价与成本优化批。
//
// 这一族为什么必须有锁：译文记忆的失效是**静默**的，出错时界面完全正常——
//
//	唯一键少一维 ⇒ 换了模型/改了提示词，旧译文继续投（"质量莫名下降"，无人能归因）；
//	来源优先级反了 ⇒ 人工确认过的稿子被机翻洗回（客户投诉"我改过的地方又变回去了"）；
//	清理写成尾随 LIMIT ⇒ 两种方言都语法错，清理从第一次起就恒失败（库表无界增长）；
//	保留期跟着"读"刷新 ⇒ 热门句永不过期，术语换代后旧词再也换不掉。
//
// 八条腿：建表幂等 / 读写往返与读数语义 / 方言真驱动 / 来源优先级 / 键维度隔离 /
// 清理（子查询 LIMIT + 边界回默认）/ 哈希碰撞校验 / 两侧常量等值。
//
// 方言口径（AGENTS §一·4）：本族主腿固定内存 SQLite 并**显式钉 config.C.DatabaseDriver**；
// PG 方言腿在 `DB_DRIVER=postgres DB_DSN=...`（run_uat 主矩阵）下真跑，环境不具备时 Skip 并留注释。
// ============================================
package store

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/db"
)

// tmTestStore 建内存 SQLite Store（显式钉方言，防 UAT PG 矩阵的 env 泄漏进本包）。
func tmTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s, err := New(conn)
	if err != nil {
		t.Fatalf("创建 Keep Store 失败: %v", err)
	}
	return s
}

// tmRec 默认作用域的一条记录（各腿只改自己关心的那一维）。
func tmRec(tid int64, src, out string) TranslationMemoryRecord {
	return TranslationMemoryRecord{
		TenantID: tid, SrcLang: "zh", TgtLang: "en",
		Mode: "mode-a", ModelFP: "siliconflow|m1", ContextHash: "",
		Src: src, Out: out, Source: TMSourceModel,
	}
}

// tmColumnSet 现读表的列名集合（判"缺列有没有补上"）。
func tmColumnSet(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`PRAGMA table_info(translation_memory)`)
	if err != nil {
		t.Fatalf("读表结构失败: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("扫描列信息失败: %v", err)
		}
		out[name] = true
	}
	return out
}

// tmIndexSet 现读该表的索引名集合。
func tmIndexSet(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='translation_memory'`)
	if err != nil {
		t.Fatalf("读索引清单失败: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("扫描索引名失败: %v", err)
		}
		out[n] = true
	}
	return out
}

// ============ L1：建表幂等 + 列/索引齐备 ============

// TestTMMigrateIsIdempotent 连跑两次不报错；七维唯一键所需列与两个索引都在。
func TestTMMigrateIsIdempotent(t *testing.T) {
	s := tmTestStore(t)
	s.TranslationMemoryMigrate() // store.New 已跑过一次；再跑一次必须无副作用
	cols := tmColumnSet(t, s)
	for _, c := range []string{"id", "tenant_id", "src_lang", "tgt_lang", "src_hash", "src",
		"mode", "model_fp", "context_hash", "out", "source", "hits", "created_at", "updated_at"} {
		if !cols[c] {
			t.Fatalf("缺列 %q ⇒ 七维唯一键或来源优先级缺输入（幂等补列没生效）", c)
		}
	}
	idx := tmIndexSet(t, s)
	for _, want := range []string{"ux_tm", "idx_tm_tenant_updated"} {
		if !idx[want] {
			t.Fatalf("缺索引 %q（现有 %v）⇒ 唯一性/清理排序失去保障", want, idx)
		}
	}
}

// ============ L2：读写往返 + hits/updated_at 语义 ============

// TestTMPutGetRoundTrip 存→取等值；hits 自增；**updated_at 不因读而变**。
func TestTMPutGetRoundTrip(t *testing.T) {
	s := tmTestStore(t)
	src, out := "请勿拆卸电池。", "Do not disassemble the battery."
	s.TranslationMemoryPut(tmRec(1, src, out))

	got, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src)
	if !ok || got != out {
		t.Fatalf("往返失败：got=%q ok=%v（写进去的读不回来＝缓存等于不存在）", got, ok)
	}
	var hits int
	var updated string
	if err := s.db.QueryRow(`SELECT hits, updated_at FROM translation_memory WHERE tenant_id=1`).Scan(&hits, &updated); err != nil {
		t.Fatalf("读 hits/updated_at 失败: %v", err)
	}
	if hits != 1 {
		t.Fatalf("命中应把 hits 加 1，实际 %d（运营用它判断缓存收益）", hits)
	}
	// ⚠️ 再读一次：hits 继续涨，updated_at **一个字都不许变**
	time.Sleep(10 * time.Millisecond)
	if _, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src); !ok {
		t.Fatal("第二次读应命中")
	}
	var hits2 int
	var updated2 string
	if err := s.db.QueryRow(`SELECT hits, updated_at FROM translation_memory WHERE tenant_id=1`).Scan(&hits2, &updated2); err != nil {
		t.Fatalf("复读 hits/updated_at 失败: %v", err)
	}
	if hits2 != 2 {
		t.Fatalf("hits 应累加到 2，实际 %d", hits2)
	}
	if updated2 != updated {
		t.Fatalf("读命中改写了 updated_at（%s → %s）⇒ 保留期变成「多久没被读到」，热门句永不过期、术语换代再也换不掉", updated, updated2)
	}
}

// ============ L3：两种方言都真的执行过这条 UPSERT ============

// TestTMPutSpeaksBothDialects 用**真驱动**跑一次同键 UPSERT（SQLite 恒跑；PG 在 UAT 矩阵下跑）。
//
// 为什么不能只靠语法直觉：`ON CONFLICT(...) DO UPDATE ... WHERE` 与占位符改写（`?`→`$n`）
// 是两个方言各自的事实，只有真跑一次才能证明这条 SQL 在本仓的 db 辅助层里成立。
func TestTMPutSpeaksBothDialects(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		s := tmTestStore(t)
		src := "请勿拆卸电池。"
		s.TranslationMemoryPut(tmRec(1, src, "Do not disassemble the battery."))
		s.TranslationMemoryPut(tmRec(1, src, "Never take the battery apart."))
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM translation_memory WHERE tenant_id=1`).Scan(&n); err != nil {
			t.Fatalf("读数失败: %v", err)
		}
		if n != 1 {
			t.Fatalf("同键 UPSERT 应只留一行，实际 %d 行（唯一键没生效）", n)
		}
		got, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src)
		if !ok || got != "Never take the battery apart." {
			t.Fatalf("同键第二次写入应覆盖译文，got=%q ok=%v", got, ok)
		}
	})

	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("DB_DSN")
		if os.Getenv("DB_DRIVER") != "postgres" || dsn == "" {
			t.Skip("PG 方言腿需要 DB_DRIVER=postgres DB_DSN=...（run_uat 主矩阵提供）；" +
				"SQLite 腿已覆盖同名 SQL 的解析与执行，环境缺失不是回归")
		}
		prev := config.C
		cfg := config.Default()
		cfg.DatabaseDriver = "postgres"
		config.C = cfg
		t.Cleanup(func() { config.C = prev })

		conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
		if err != nil {
			t.Fatalf("打开 PG 失败（矩阵环境下这算回归，不许当环境缺失跳过）: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		s, err := New(conn)
		if err != nil {
			t.Fatalf("store.New(PG) 失败: %v", err)
		}
		// 用不存在的租户号做隔离，跑完按租户清干净（不碰矩阵正在用的数据）。
		const tid = 999999
		src := "请勿拆卸电池。"
		s.TranslationMemoryPut(tmRec(tid, src, "Do not disassemble the battery."))
		s.TranslationMemoryPut(tmRec(tid, src, "Never take the battery apart."))
		t.Cleanup(func() { _, _ = s.db.Exec(`DELETE FROM translation_memory WHERE tenant_id=$1`, tid) })
		got, ok := s.TranslationMemoryGet(tid, "zh", "en", "mode-a", "siliconflow|m1", "", src)
		if !ok || got != "Never take the battery apart." {
			t.Fatalf("PG 上同键 UPSERT 失败：got=%q ok=%v（两种方言必须都能跑，不是靠语法直觉）", got, ok)
		}
	})
}

// ============ L4：来源优先级 ============

// TestTMSourcePriority human/approved 不被 model 覆盖；反向可以覆盖。
func TestTMSourcePriority(t *testing.T) {
	s := tmTestStore(t)
	src := "确认过的译文。"
	for _, human := range []string{TMSourceHuman, TMSourceApproved} {
		rec := tmRec(1, src, "Approved translation.")
		rec.Source = human
		s.TranslationMemoryPut(rec)
		// model 反向覆盖必须无效
		s.TranslationMemoryPut(tmRec(1, src, "Machine translation."))
		got, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src)
		if !ok || got != "Approved translation." {
			t.Fatalf("来源优先级反了：%s 被 model 覆盖成 %q ⇒ 客户改过的稿子被机翻洗回", human, got)
		}
		// 正向对照：同档之间可以覆盖（否则就是"写死一次永不再更新"）
		rec2 := tmRec(1, src, "Second approval.")
		rec2.Source = human
		s.TranslationMemoryPut(rec2)
		got, ok = s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src)
		if !ok || got != "Second approval." {
			t.Fatalf("%s 之间的覆盖必须生效，got=%q ok=%v", human, got, ok)
		}
		// 复原成 model 供下一轮 human/approved 各测一次
		_, _ = s.db.Exec(`UPDATE translation_memory SET source='model' WHERE tenant_id=1`)
	}
	// model → human 可以覆盖（这是"人工确认"的正常路径）
	s.TranslationMemoryPut(tmRec(1, src, "Machine translation."))
	rec := tmRec(1, src, "Human approved.")
	rec.Source = TMSourceHuman
	s.TranslationMemoryPut(rec)
	if got, _ := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src); got != "Human approved." {
		t.Fatalf("model → human 的覆盖必须生效（人工确认是正常路径），got=%q", got)
	}
}

// ============ L5：键维度隔离 ============

// TestTMKeyDimensionsIsolate 任一维不同 ⇒ 不命中。
func TestTMKeyDimensionsIsolate(t *testing.T) {
	s := tmTestStore(t)
	src := "请勿拆卸电池。"
	s.TranslationMemoryPut(tmRec(1, src, "Do not disassemble the battery."))

	cases := []struct {
		name                            string
		tid                             int64
		srcLang, tgtLang, mode, mfp, ch string
	}{
		{"租户", 2, "zh", "en", "mode-a", "siliconflow|m1", ""},
		{"源语种", 1, "ja", "en", "mode-a", "siliconflow|m1", ""},
		{"目标语种", 1, "zh", "ja", "mode-a", "siliconflow|m1", ""},
		{"提示词口径", 1, "zh", "en", "mode-b", "siliconflow|m1", ""},
		{"模型指纹", 1, "zh", "en", "mode-a", "deepseek|m1", ""},
		{"表头上下文", 1, "zh", "en", "mode-a", "siliconflow|m1", "hdr-1"},
	}
	for _, c := range cases {
		if _, ok := s.TranslationMemoryGet(c.tid, c.srcLang, c.tgtLang, c.mode, c.mfp, c.ch, src); ok {
			t.Fatalf("%s 不同却命中了 ⇒ 该维度没进唯一键（换模型/改提示词后旧译文继续投）", c.name)
		}
	}
	// 正向对照：原作用域必须命中（否则上面六条是"永远不命中"的空转）
	if _, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src); !ok {
		t.Fatal("原作用域应命中（负向腿失去对照）")
	}
}

// ============ L6：清理（子查询 LIMIT + 边界回默认） ============

// tmRawInsert 直接插一行（可指定 updated_at），用于造"过期/超额/碰撞"形态。
func tmRawInsert(t *testing.T, s *Store, tid int64, src, out, updated string) {
	t.Helper()
	hash := translationMemoryHash(src)
	id := translationMemoryID(tid, hash, "zh", "en", "mode-a", "siliconflow|m1", "")
	if _, err := s.db.Exec(`INSERT INTO translation_memory
		(id, tenant_id, src_lang, tgt_lang, src_hash, src, mode, model_fp, context_hash, out, source, hits, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
		id, tid, "zh", "en", hash, src, "mode-a", "siliconflow|m1", "", out, TMSourceModel, updated, updated); err != nil {
		t.Fatalf("造数失败: %v", err)
	}
}

// TestTMPruneSubqueryLimitAndBounds 过期删、超额删最旧、边界（0/负数回默认）。
func TestTMPruneSubqueryLimitAndBounds(t *testing.T) {
	s := tmTestStore(t)
	now := time.Now().UTC()
	// 3 条过期（200 天前）+ 4 条新鲜（按 updated_at 递增，最旧的是 fresh-1）
	old := now.AddDate(0, 0, -200).Format(time.RFC3339)
	for _, src := range []string{"过期一。", "过期二。", "过期三。"} {
		tmRawInsert(t, s, 1, src, "stale", old)
	}
	for i, src := range []string{"新鲜一。", "新鲜二。", "新鲜三。", "新鲜四。"} {
		tmRawInsert(t, s, 1, src, "fresh", now.Add(time.Duration(i)*time.Minute).Format(time.RFC3339))
	}
	// 保留 180 天、上限 10 ⇒ 只删 3 条过期的
	if n := s.TranslationMemoryPrune(1, 180, 10); n != 3 {
		t.Fatalf("保留 180 天应删 3 条过期行，实际 %d 条", n)
	}
	var cnt int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM translation_memory WHERE tenant_id=1`).Scan(&cnt); err != nil {
		t.Fatalf("读数失败: %v", err)
	}
	if cnt != 4 {
		t.Fatalf("过期清理后应剩 4 行，实际 %d", cnt)
	}
	// 上限 2 ⇒ 删最旧的 2 条（新鲜一/二）
	if n := s.TranslationMemoryPrune(1, 180, 2); n != 2 {
		t.Fatalf("超额裁旧应删 2 条，实际 %d 条", n)
	}
	var survivors int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM translation_memory WHERE tenant_id=1`).Scan(&survivors); err != nil {
		t.Fatalf("复读失败: %v", err)
	}
	if survivors != 2 {
		t.Fatalf("裁旧后应剩 2 行，实际 %d", survivors)
	}
	if _, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", "新鲜四。"); !ok {
		t.Fatal("最新的一行被删了 ⇒ 清理按 asc 取到了错的那一端")
	}
	if _, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", "新鲜一。"); ok {
		t.Fatal("最旧的一行没被删 ⇒ 超额裁旧没生效（库表会无界增长）")
	}
	// 边界：keepDays/maxRows 传 0 或负数 ⇒ 回默认（180 天 / 20 万行），一行都不该删
	for _, bad := range [][2]int{{0, 0}, {-5, -5}} {
		if n := s.TranslationMemoryPrune(1, bad[0], bad[1]); n != 0 {
			t.Fatalf("keepDays/maxRows=%v 应回默认且不删任何行，实际删了 %d 行", bad, n)
		}
	}
}

// ============ L7：批量读的原文等值校验 ============

// TestTMBatchReadVerifiesSourceText 塞一条「哈希相同但原文不同」的行 ⇒ 判不命中（防碰撞串句）。
func TestTMBatchReadVerifiesSourceText(t *testing.T) {
	s := tmTestStore(t)
	collide := "碰撞样本。"
	other := "另一句话。"
	// 人为造出哈希碰撞形态：src_hash 用 collide 的，src 写 other。
	hash := translationMemoryHash(collide)
	id := translationMemoryID(1, hash, "zh", "en", "mode-a", "siliconflow|m1", "")
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`INSERT INTO translation_memory
		(id, tenant_id, src_lang, tgt_lang, src_hash, src, mode, model_fp, context_hash, out, source, hits, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
		id, 1, "zh", "en", hash, other, "mode-a", "siliconflow|m1", "", "WRONG OUT", TMSourceModel, now, now); err != nil {
		t.Fatalf("造碰撞行失败: %v", err)
	}
	got := s.TranslationMemoryGetBatch(1, "zh", "en", "mode-a", "siliconflow|m1", "", []string{collide})
	if v, ok := got[collide]; ok {
		t.Fatalf("哈希相同但原文不同却命中了（返回 %q）⇒ 会把别人的句子投给客户", v)
	}
	// 单条腿同一把尺子
	if v, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", collide); ok {
		t.Fatalf("单条读也必须做原文等值校验，却返回了 %q", v)
	}
	// 正向对照：真正的原文行必须能批量命中（否则上面是空转）
	s.TranslationMemoryPut(tmRec(1, collide, "Collision sample."))
	got = s.TranslationMemoryGetBatch(1, "zh", "en", "mode-a", "siliconflow|m1", "", []string{collide, "没存过的句子。"})
	if got[collide] != "Collision sample." {
		t.Fatalf("正常行批量读应命中，got=%v", got)
	}
	if _, ok := got["没存过的句子。"]; ok {
		t.Fatal("没存过的句子不许出现在批量结果里")
	}
}

// ============ L8：缺省常量 ============

// TestTMConstantsArePinned 保留期与行数上限是**对外承诺的数字**（文档、管理台、客户合规口径都引它）。
func TestTMConstantsArePinned(t *testing.T) {
	if TranslationMemoryRetentionDays != 180 {
		t.Fatalf("保留天数应为 180 天（用户令 / 文档口径），实际 %d", TranslationMemoryRetentionDays)
	}
	if TranslationMemoryMaxRowsPerTenant != 200000 {
		t.Fatalf("单租户行数上限应为 20 万（用户令 / 文档口径），实际 %d", TranslationMemoryMaxRowsPerTenant)
	}
	// 来源档三值也是对外契约（写侧只许写这三个值）
	if TMSourceModel != "model" || TMSourceHuman != "human" || TMSourceApproved != "approved" {
		t.Fatalf("来源档字面量变了：%q/%q/%q", TMSourceModel, TMSourceHuman, TMSourceApproved)
	}
}

// ============ L9：平台单（tid==0）与无归属（tid<0） ============

// TestTMPlatformTenantSemantics 平台单落 tenant_id=0 且只与 0 互查；tid<0 不读不写。
//
// 为什么单独一条腿：`tid==0` 在本仓有两种含义——「平台单」（有归属，只是不挂客户）
// 与「没有归属」（不该落库）。两者混成一档就会出现「一张没有租户的工单把译文写进
// 平台桶，下一张别的单命中它」的静默串台（v1 §4.1）。
func TestTMPlatformTenantSemantics(t *testing.T) {
	s := tmTestStore(t)
	src := "平台单样本。"
	s.TranslationMemoryPut(tmRec(0, src, "Platform sample."))
	if got, ok := s.TranslationMemoryGet(0, "zh", "en", "mode-a", "siliconflow|m1", "", src); !ok || got != "Platform sample." {
		t.Fatalf("平台单（tid=0）自身的读写必须成立，got=%q ok=%v", got, ok)
	}
	// 客户租户不许看到平台桶（也不许把自己的写进平台桶）
	if _, ok := s.TranslationMemoryGet(1, "zh", "en", "mode-a", "siliconflow|m1", "", src); ok {
		t.Fatal("客户租户命中了平台桶（tid=0）⇒ 平台与客户共享译文")
	}
	if got := s.TranslationMemoryGetBatch(1, "zh", "en", "mode-a", "siliconflow|m1", "", []string{src}); len(got) != 0 {
		t.Fatalf("客户租户批量读命中平台桶：%v", got)
	}
	// tid<0 不读不写
	s.TranslationMemoryPut(tmRec(-1, "无归属样本。", "Orphan."))
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM translation_memory WHERE tenant_id<0`).Scan(&n); err != nil {
		t.Fatalf("读数失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("tid<0 不许落库（实际写了 %d 行）⇒ 无归属翻译进库 = 谁也清不掉", n)
	}
	if _, ok := s.TranslationMemoryGet(-1, "zh", "en", "mode-a", "siliconflow|m1", "", "无归属样本。"); ok {
		t.Fatal("tid<0 不许读库")
	}
}
