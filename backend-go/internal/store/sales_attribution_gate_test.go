// ============ sales_attribution_gate_test.go · 职责说明 ============
// 归因域数据层门禁（真 SQLite 文件库）：幂等 upsert、eval_score 默认 -1、跨租户不串、
// SyncPackStats 聚合读数、旧名零出现（源码级守卫＋反证）、并发重复 message_id 恰好一条。
// ★ 文件库（非 :memory:）：多连接共享同一库，并发腿才不会假绿。
// ================================================
package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// pinStoreSQLite 固定测试方言（config.C 全局在用例结束还原）。
func pinStoreSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newSalesAttributionTestStore 文件库建 Store 并单独跑归因域迁移（不依赖 New() 自动挂接）。
func newSalesAttributionTestStore(t *testing.T) *Store {
	t.Helper()
	pinStoreSQLite(t)
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "attribution.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// 同包直接构造，只挂本域迁移：不拉起平台全量迁移链，用例更聚焦、更稳。
	st := &Store{db: conn}
	if err := st.SalesAttributionMigrate(); err != nil {
		t.Fatalf("归因域迁移失败: %v", err)
	}
	return st
}

// attributionFixture 一条可控的归因夹具（只填聚合会读到的列）。
type attributionFixture struct {
	tid          int64
	messageID    int64
	convID       int64
	custID       int64
	packageID    string
	version      string
	draftID      string
	hooked       int
	lead         int
	pending      int
	evalScore    int
	intentBefore float64
	intentAfter  float64
	arrivedAt    string
	dealtAt      string
}

// insertAttributionFixture 参数化插入一条归因夹具（SQL 只在本 internal/store 新文件里）。
func insertAttributionFixture(t *testing.T, st *Store, f attributionFixture) {
	t.Helper()
	_, err := db.Exec(st.db, db.CurrentDialect(),
		`INSERT INTO reply_attributions
		 (tenant_id, message_id, conversation_id, customer_id, package_id, version, draft_id,
		  hooked, lead_captured, pending_human, eval_score, intent_before, intent_after,
		  arrived_at, dealt_at, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		f.tid, f.messageID, f.convID, f.custID, f.packageID, f.version, f.draftID,
		f.hooked, f.lead, f.pending, f.evalScore, f.intentBefore, f.intentAfter,
		f.arrivedAt, f.dealtAt, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("插入归因夹具失败: %v", err)
	}
}

// legacyColumnNames 三个旧列名（拆开拼：本文件自身不许出现旧名连续字面）。
var legacyColumnNames = []string{
	"pack" + "_code",
	"pack" + "_version",
	"template" + "_id",
}

// legacyNameHits 纯函数：从源码文本里挑出命中的旧列名（真源码与合成件喂同一个函数）。
func legacyNameHits(src string) []string {
	var hits []string
	for _, lit := range legacyColumnNames {
		if strings.Contains(src, lit) {
			hits = append(hits, lit)
		}
	}
	return hits
}

// approxEqual 浮点近似比较（聚合读数用）。
func approxEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

// TestSalesAttributionRecordReplyIdempotentUpsert 幂等：重复 message_id 只刷新快照、不新增行、不重置评分与标记。
func TestSalesAttributionRecordReplyIdempotentUpsert(t *testing.T) {
	st := newSalesAttributionTestStore(t)
	row := ReplyAttributionRow{
		TenantID: 1, MessageID: 5001, ConversationID: 7001, CustomerID: 8001,
		PackageID: "pkg_a", Version: "1.0.0", DraftID: "draft_1",
		IntentBefore: 0.2, IntentAfter: 0.45,
	}
	if err := st.RecordReply(row); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	// 人为把评分改成合法值、并立一个事件标记，验证重复写不覆盖它们。
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		`UPDATE reply_attributions SET eval_score=?, lead_captured=? WHERE tenant_id=? AND message_id=?`,
		72, 1, 1, 5001); err != nil {
		t.Fatalf("构造评分夹具失败: %v", err)
	}
	row.PackageID = "pkg_b"
	row.Version = "2.0.0"
	if err := st.RecordReply(row); err != nil {
		t.Fatalf("幂等重写失败: %v", err)
	}
	rows, err := st.ListReplyAttributions(1, 10, 0)
	if err != nil {
		t.Fatalf("列表读取失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("幂等写应恰好一条，实得 %d", len(rows))
	}
	got := rows[0]
	if got.PackageID != "pkg_b" || got.Version != "2.0.0" {
		t.Fatalf("重复写应刷新快照，实得 %+v", got)
	}
	if got.EvalScore != 72 || got.LeadCaptured != 1 {
		t.Fatalf("重复写不许覆盖评分/事件标记，实得 eval=%d lead=%d", got.EvalScore, got.LeadCaptured)
	}
}

// TestSalesAttributionEvalScoreDefaultMinusOne eval_score 默认 -1（未知），不是 0。
func TestSalesAttributionEvalScoreDefaultMinusOne(t *testing.T) {
	st := newSalesAttributionTestStore(t)
	if err := st.RecordReply(ReplyAttributionRow{TenantID: 1, MessageID: 5100, DraftID: "draft_e"}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	rows, err := st.ListReplyAttributions(1, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("读取失败: rows=%d err=%v", len(rows), err)
	}
	if rows[0].EvalScore != -1 {
		t.Fatalf("eval_score 未知应为 -1，实得 %d（-1≠0）", rows[0].EvalScore)
	}
	if rows[0].EvalScore == 0 {
		t.Fatal("eval_score 不许用 0 表示未知")
	}
}

// TestSalesAttributionCrossTenantIsolation 跨租户读不到：列表/快照/更新都按 tenant_id 隔离，非法 tid 被拒。
func TestSalesAttributionCrossTenantIsolation(t *testing.T) {
	st := newSalesAttributionTestStore(t)
	for _, tid := range []int64{1, 2} {
		if err := st.RecordReply(ReplyAttributionRow{
			TenantID: tid, MessageID: 6000, ConversationID: 9000, CustomerID: 9001,
			PackageID: "pkg_x", Version: "1.0.0", DraftID: "draft_x",
		}); err != nil {
			t.Fatalf("tid=%d 写入失败: %v", tid, err)
		}
	}
	// 同 message_id 跨租户各一条，互不串。
	rows1, _ := st.ListReplyAttributions(1, 10, 0)
	rows2, _ := st.ListReplyAttributions(2, 10, 0)
	if len(rows1) != 1 || len(rows2) != 1 || rows1[0].TenantID != 1 || rows2[0].TenantID != 2 {
		t.Fatalf("跨租户列表串了: tid1=%d tid2=%d", len(rows1), len(rows2))
	}
	// 只对 tid=1 回填终局，tid=2 的同 message_id 行不受影响。
	if err := st.MarkOutcome(1, 6000, "inquiry"); err != nil {
		t.Fatalf("终局回填失败: %v", err)
	}
	rows1, _ = st.ListReplyAttributions(1, 10, 0)
	rows2, _ = st.ListReplyAttributions(2, 10, 0)
	if rows1[0].OutcomeStage != "inquiry" || rows2[0].OutcomeStage != "" {
		t.Fatalf("终局回填跨租户串了: tid1=%q tid2=%q", rows1[0].OutcomeStage, rows2[0].OutcomeStage)
	}
	// 聚合也按租户隔离。
	if err := st.SyncPackStats(1); err != nil {
		t.Fatalf("聚合失败: %v", err)
	}
	if got, err := st.GetPackStats(1, "pkg_x", "1.0.0", "draft_x"); err != nil || got == nil {
		t.Fatalf("tid=1 快照应存在: got=%v err=%v", got, err)
	}
	if got, err := st.GetPackStats(2, "pkg_x", "1.0.0", "draft_x"); err != nil || got != nil {
		t.Fatalf("tid=2 未聚合不应读到快照: got=%v err=%v", got, err)
	}
	// 非法入参 reject。
	if err := st.RecordReply(ReplyAttributionRow{TenantID: 0, MessageID: 1}); err == nil {
		t.Fatal("tid=0 必须在写侧拒掉")
	}
	if _, err := st.ListReplyAttributions(0, 10, 0); err == nil {
		t.Fatal("tid=0 必须在读侧拒掉")
	}
}

// TestSalesAttributionSyncPackStatsAggregates 聚合读数：4 行样本的各率与均值按口径算对。
func TestSalesAttributionSyncPackStatsAggregates(t *testing.T) {
	st := newSalesAttributionTestStore(t)
	base := attributionFixture{
		tid: 1, convID: 8800, custID: 8801,
		packageID: "pkg_rate", version: "9.9.0", draftID: "draft_rate",
	}
	// 4 行：2 hooked、1 lead、1 pending；评分与意向差精确可控；终态列全空 ⇒ 终局率为 0。
	fixtures := []attributionFixture{
		{messageID: 1, hooked: 1, evalScore: 80, intentBefore: 0.1, intentAfter: 0.5},
		{messageID: 2, hooked: 1, lead: 1, evalScore: 60, intentBefore: 0.2, intentAfter: 0.6},
		{messageID: 3, pending: 1, evalScore: -1, intentBefore: 0.0, intentAfter: 0.4},
		{messageID: 4, evalScore: 40, intentBefore: 0.3, intentAfter: 0.7},
	}
	for i := range fixtures {
		f := base
		f.messageID = fixtures[i].messageID
		f.hooked = fixtures[i].hooked
		f.lead = fixtures[i].lead
		f.pending = fixtures[i].pending
		f.evalScore = fixtures[i].evalScore
		f.intentBefore = fixtures[i].intentBefore
		f.intentAfter = fixtures[i].intentAfter
		insertAttributionFixture(t, st, f)
	}
	if err := st.SyncPackStats(1); err != nil {
		t.Fatalf("SyncPackStats 失败: %v", err)
	}
	got, err := st.GetPackStats(1, "pkg_rate", "9.9.0", "draft_rate")
	if err != nil || got == nil {
		t.Fatalf("快照应存在: got=%v err=%v", got, err)
	}
	if got.SampleCount != 4 {
		t.Fatalf("sample_count 应为 4，实得 %d", got.SampleCount)
	}
	if !approxEqual(got.HookRate, 0.5) || !approxEqual(got.LeadRate, 0.25) || !approxEqual(got.PendingHumanRate, 0.25) {
		t.Fatalf("率不对: hook=%v lead=%v pending=%v", got.HookRate, got.LeadRate, got.PendingHumanRate)
	}
	// avg_intent_delta: (0.4+0.4+0.4+0.4)/4 = 0.4
	if !approxEqual(got.AvgIntentDelta, 0.4) {
		t.Fatalf("avg_intent_delta 应为 0.4，实得 %v", got.AvgIntentDelta)
	}
	// avg_eval_score: 只计 >=0 的三条 (80+60+40)/3 = 60
	if !approxEqual(got.AvgEvalScore, 60) {
		t.Fatalf("avg_eval_score 应为 60，实得 %v", got.AvgEvalScore)
	}
	// 终态列不写 ⇒ 终局率恒 0（一期口径）。
	if got.ArriveRate != 0 || got.DealRate != 0 {
		t.Fatalf("终态列不写时终局率应恒 0，实得 arrive=%v deal=%v", got.ArriveRate, got.DealRate)
	}
	// 再跑一次幂等：仍是同一行、读数不变。
	if err := st.SyncPackStats(1); err != nil {
		t.Fatalf("二次聚合失败: %v", err)
	}
	rows, _ := st.ListReplyAttributions(1, 10, 0)
	if len(rows) != 4 {
		t.Fatalf("聚合不许改动归因行，实得 %d", len(rows))
	}
}

// TestSalesAttributionHasNoLegacyColumnNames 三个旧名零出现（源码级守卫＋反证）。
func TestSalesAttributionHasNoLegacyColumnNames(t *testing.T) {
	pinStoreSQLite(t)
	for _, f := range []string{"sales_attribution.go", "sales_attribution_migrate.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		if hits := legacyNameHits(string(src)); len(hits) != 0 {
			t.Fatalf("表映射改形未生效：%s 里仍出现旧名 %v", f, hits)
		}
	}
	// 反证：同一个纯函数喂合成件必须命中，证明扫描器没有失明。
	if hits := legacyNameHits("CREATE TABLE t (" + legacyColumnNames[0] + " TEXT)"); len(hits) != 1 || hits[0] != legacyColumnNames[0] {
		t.Fatalf("反证：合成件应命中 1 处旧名，实得 %v", hits)
	}
}

// TestSalesAttributionConcurrentDuplicateMessageIDExactlyOne 并发重复 message_id 恰好一条。
func TestSalesAttributionConcurrentDuplicateMessageIDExactlyOne(t *testing.T) {
	st := newSalesAttributionTestStore(t)
	const n = 16
	row := ReplyAttributionRow{
		TenantID: 1, MessageID: 7777, ConversationID: 9900, CustomerID: 9901,
		PackageID: "pkg_c", Version: "1.0.0", DraftID: "draft_c",
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	errs := []error{}
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := st.RecordReply(row); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发重复写不许上抛错误，实得 %d 条: %v", len(errs), errs)
	}
	var count int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM reply_attributions WHERE tenant_id=? AND message_id=?`, 1, 7777).Scan(&count); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("并发重复 message_id 必须恰好一条，实得 %d", count)
	}
}
