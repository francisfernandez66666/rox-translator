// ============ evals_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-6、§4.4）：internal/evals（LLM-as-Judge 5 维评估器）回归断言。
// 该包此前**零测试**，而它决定「翻译质量分数是否可信」：
//
//	① ShouldSample 抽样开关边界（禁用/全量/关闭/概率区间）——错判会要么白烧 Judge 成本、要么静默不评估；
//	② judgeKeyUsable 的三类不可用 Key（空、掩码回显串、启动随机占位符）——
//	   放过占位符等于每次评估都必失败并刷错误日志；
//	③ parseScores 的权重口径（术语30/语法20/语义30/数字10/风格10）、模型自带 total 优先、
//	   上限截 100、带代码块/前后缀噪声的 JSON 提取、无 JSON 必须报错；
//	④ resolveJudge 的四级优先级（stage_models 分阶段 → 旧键 evals → ModelRoutes 最高权重 → Online*）
//	   与「阶段 Key 为空继承全局 Key」的兜底；
//	⑤ SaveRecord 的幂等语义（同单同语言重复打标不产生新行）与 Store=nil 的静默跳过。
//
// 本文件不打真实 LLM 外网：Evaluate 的调用链由 mock server 场景在 UAT 覆盖。
// =============================================
package evals

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// pinSQLite 自钉 SQLite 方言（AGENTS.md 一.4：config.Default() 会写全局 config.C，
// 不钉死会在 run_uat 的 PG 模式下把方言泄漏给同包内存库用例）。
func pinSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newStore 内存 SQLite 全量迁移 Store（打标幂等用例需要真实表）。
func newStore(t *testing.T) *store.Store {
	t.Helper()
	pinSQLite(t)
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return s
}

// TestNewDisablesWithoutJudgeKey 无 Judge Key ⇒ Enabled=false（避免无意义 API 调用）。
func TestNewDisablesWithoutJudgeKey(t *testing.T) {
	pinSQLite(t)
	cfg := config.Default()
	if e := New(cfg, nil, nil, ""); e.Enabled {
		t.Fatal("空 Judge Key 应禁用评估")
	} else if e.ShouldSample() {
		t.Fatal("禁用态 ShouldSample 必须恒为 false")
	} else if _, _, err := e.Evaluate(context.Background(), "源文", "译文", "en", "translate"); err == nil {
		t.Fatal("禁用态 Evaluate 应直接报错而非打网络")
	}
	if e := New(cfg, nil, nil, "sk-real-key"); !e.Enabled {
		t.Fatal("有 Judge Key 应启用评估")
	}
}

// TestShouldSampleBoundaries 抽样率边界：>=1 全量、<=0 关闭、中间按概率（统计断言防 flaky）。
func TestShouldSampleBoundaries(t *testing.T) {
	pinSQLite(t)
	cfg := config.Default()
	full := New(cfg, nil, nil, "sk")
	full.SampleRate = 1.0
	for i := 0; i < 100; i++ {
		if !full.ShouldSample() {
			t.Fatal("SampleRate>=1 必须全量评估")
		}
	}
	off := New(cfg, nil, nil, "sk")
	off.SampleRate = 0
	if off.ShouldSample() {
		t.Fatal("SampleRate<=0 必须完全关闭（否则成本闸失效）")
	}
	neg := New(cfg, nil, nil, "sk")
	neg.SampleRate = -1
	if neg.ShouldSample() {
		t.Fatal("负抽样率应按关闭处理")
	}
	half := New(cfg, nil, nil, "sk")
	half.SampleRate = 0.5
	hit := 0
	const rounds = 4000
	for i := 0; i < rounds; i++ {
		if half.ShouldSample() {
			hit++
		}
	}
	// 二项分布 4σ 区间（0.5±0.025）：既证明「确实在抽样」，也证明没被写成恒真/恒假
	if ratio := float64(hit) / rounds; ratio < 0.47 || ratio > 0.53 {
		t.Fatalf("0.5 抽样率实测命中比 %.3f 越界", ratio)
	}
}

// TestJudgeKeyUsable 三类不可用 Key 必须被拒：空、掩码回显串、启动随机占位符。
func TestJudgeKeyUsable(t *testing.T) {
	pinSQLite(t)
	cfg := config.Default()
	cfg.OnlineAPIKey = "random-placeholder-from-boot"
	cfg.OnlineAPIKeyIsPlaceholder = true
	e := New(cfg, nil, nil, "sk")
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"空 Key", "", false},
		{"管理台掩码回显串", "sk-****abcd", false},
		{"启动随机占位符", cfg.OnlineAPIKey, false},
		{"真实 Key", "sk-abcdefghijklmnop", true},
	}
	for _, c := range cases {
		if got := e.judgeKeyUsable(c.key); got != c.want {
			t.Errorf("%s：judgeKeyUsable(%q)=%v，期望 %v", c.name, c.key, got, c.want)
		}
	}
	// 占位标记关闭时同值即视为可用（管理员真的把它填成线上 Key 的场景）
	cfg.OnlineAPIKeyIsPlaceholder = false
	if !e.judgeKeyUsable(cfg.OnlineAPIKey) {
		t.Fatal("非占位标记下同值 Key 应可用")
	}
	// Cfg 为 nil 不得 panic（历史构造路径兼容）
	if (&Evaluator{Enabled: true}).judgeKeyUsable("sk-real") != true {
		t.Fatal("无 Cfg 时正常 Key 应可用")
	}
}

// TestParseScores 权重口径、total 优先、上限截断与噪声 JSON 提取。
func TestParseScores(t *testing.T) {
	// 加权：30%*90 + 20%*80 + 30%*70 + 10%*60 + 10%*50 = 27+16+21+6+5 = 75
	total, dims, err := parseScores(`{"term":90,"grammar":80,"semantic":70,"numunit":60,"style":50}`)
	if err != nil {
		t.Fatalf("纯 JSON 解析失败: %v", err)
	}
	if math.Abs(total-75) > 1e-9 {
		t.Fatalf("加权总分应为 75，实得 %v", total)
	}
	if dims["term"] != 90 || dims["style"] != 50 {
		t.Fatalf("分维分数回传不符: %+v", dims)
	}
	// 模型自带 total 优先于本地加权（Judge 口径一致性）
	if total, _, _ := parseScores(`{"term":90,"grammar":80,"semantic":70,"numunit":60,"style":50,"total":88}`); total != 88 {
		t.Fatalf("应优先采用模型给出的 total=88，实得 %v", total)
	}
	// total 为 0/负数视为无效，回落本地加权
	if total, _, _ := parseScores(`{"term":90,"grammar":80,"semantic":70,"numunit":60,"style":50,"total":0}`); math.Abs(total-75) > 1e-9 {
		t.Fatalf("total=0 时应回落加权结果 75，实得 %v", total)
	}
	// 上限截断到 100（脏分不得穿透到下游阈值判断）
	if total, _, _ := parseScores(`{"term":150,"grammar":150,"semantic":150,"numunit":150,"style":150}`); total != 100 {
		t.Fatalf("超 100 应截断为 100，实得 %v", total)
	}
	// 容忍代码块与前后说明文字：取第一个 { 到最后一个 }
	noisy := "好的，评估如下：\n```json\n{\"term\":60,\"grammar\":60,\"semantic\":60,\"numunit\":60,\"style\":60}\n```\n以上。"
	if total, _, err := parseScores(noisy); err != nil || math.Abs(total-60) > 1e-9 {
		t.Fatalf("带噪声输出解析失败: total=%v err=%v", total, err)
	}
	// 无 JSON / 括号顺序错乱必须报错（不能静默返回 0 分——0 分会误触发「质检存疑」打标）
	for _, bad := range []string{"", "模型拒答", "{", "}{"} {
		if _, _, err := parseScores(bad); err == nil {
			t.Errorf("输入 %q 应返回错误", bad)
		}
	}
	if _, _, err := parseScores(`{"term":`); err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}

// TestResolveJudgePriority Judge 模型解析的四级优先级与继承全局 Key 的兜底。
func TestResolveJudgePriority(t *testing.T) {
	pinSQLite(t)
	cfg := config.Default()
	cfg.OnlineAPIBase = "https://online.example/v1"
	cfg.OnlineAPIKey = "sk-online"
	cfg.OnlineModel = "online-model"
	cfg.ModelRoutes = []config.ProviderConfig{
		{APIBase: "https://low.example/v1", APIKey: "sk-low", Model: "low-model", Weight: 10},
		{APIBase: "https://best.example/v1", APIKey: "sk-best", Model: "best-model", Weight: 80},
	}
	e := New(cfg, nil, nil, "sk-judge")

	// 无 Store：走 ModelRoutes 最高权重（不是数组首项）
	base, key, model := e.resolveJudge("translate")
	if base != "https://best.example/v1" || key != "sk-best" || model != "best-model" {
		t.Fatalf("应命中最高权重路由，实得 %s/%s/%s", base, key, model)
	}
	// 路由不可用（缺 model）⇒ 回落 Online*
	e.Cfg.ModelRoutes = []config.ProviderConfig{{APIBase: "https://x.example/v1", APIKey: "k", Model: "", Weight: 99}}
	if base, _, model := e.resolveJudge("translate"); base != cfg.OnlineAPIBase || model != cfg.OnlineModel {
		t.Fatalf("路由不完整应回落 Online 配置，实得 %s/%s", base, model)
	}

	// 有 Store：stage_models 分阶段配置优先
	st := newStore(t)
	sm := config.StageModels{
		config.StageInitialEvals: {APIBase: "https://ie.example/v1", APIKey: "sk-ie", Model: "ie-model"},
		config.StageReviewEvals:  {APIBase: "https://rv.example/v1", APIKey: "sk-rv", Model: "rv-model"},
		config.StageEvals:        {APIBase: "https://legacy.example/v1", APIKey: "sk-lg", Model: "lg-model"},
	}
	b, _ := json.Marshal(sm)
	if err := st.SetConfig("stage_models", string(b)); err != nil {
		t.Fatalf("写入 stage_models 失败: %v", err)
	}
	e2 := New(cfg, nil, st, "sk-judge")
	if base, key, model := e2.resolveJudge("translate"); base != "https://ie.example/v1" || key != "sk-ie" || model != "ie-model" {
		t.Fatalf("初翻评估应命中 initial_evals，实得 %s/%s/%s", base, key, model)
	}
	if base, key, model := e2.resolveJudge("review"); base != "https://rv.example/v1" || key != "sk-rv" || model != "rv-model" {
		t.Fatalf("校对评估应命中 review_evals，实得 %s/%s/%s", base, key, model)
	}
	// 只配旧键 evals ⇒ 两个阶段都回落到它（老库兼容）
	if err := st.SetConfig("stage_models", `{"evals":{"api_base":"https://legacy.example/v1","api_key":"sk-lg","model":"lg-model"}}`); err != nil {
		t.Fatalf("写入旧键配置失败: %v", err)
	}
	for _, taskType := range []string{"translate", "review", ""} {
		if base, _, model := e2.resolveJudge(taskType); base != "https://legacy.example/v1" || model != "lg-model" {
			t.Fatalf("taskType=%q 应回落旧键 evals，实得 %s/%s", taskType, base, model)
		}
	}
	// 阶段配置未填 Key ⇒ 继承全局 Online Key（不是留空，否则 Judge 必然 401）
	if err := st.SetConfig("stage_models", `{"initial_evals":{"api_base":"https://ie.example/v1","model":"ie-model"}}`); err != nil {
		t.Fatalf("写入无 Key 的阶段配置失败: %v", err)
	}
	if _, key, _ := e2.resolveJudge("translate"); key != cfg.OnlineAPIKey {
		t.Fatalf("阶段未配 Key 时应继承全局 Key，实得 %q", key)
	}
	// stage_models 是脏数据时不得 panic，继续按下一优先级解析
	if err := st.SetConfig("stage_models", "{坏 JSON"); err != nil {
		t.Fatalf("写入脏配置失败: %v", err)
	}
	if base, _, _ := e2.resolveJudge("translate"); !strings.Contains(base, "example") {
		t.Fatalf("脏 stage_models 应静默跳过并回落，实得 %q", base)
	}
}

// TestSaveRecordPersistence Store=nil 静默跳过；有库时按 (工单,阶段,语言) 各存一行，
// 且五维分数以 JSON 落库（下游徽标/详情直接解析该列）。
// 注：报告 §4.4 所说「同单同语言幂等一次」由编排层（orchestrator/workflow.go 的质检存疑打标）
// 保证，SaveRecord 本身是「每次评估一条流水」，此处按真实语义断言，避免把两层口径混成一个假测试。
func TestSaveRecordPersistence(t *testing.T) {
	pinSQLite(t)
	scores := map[string]float64{"term": 90, "grammar": 80, "semantic": 70, "numunit": 60, "style": 50}
	// ① 无存储：返回 (0,nil)，不 panic（本地/降级部署路径）
	if id, err := (&Evaluator{}).SaveRecord(context.Background(), 1, 2, 3, "translate", "m", "in", "out", scores, 77, "done"); id != 0 || err != nil {
		t.Fatalf("无 Store 时应静默跳过，实得 id=%d err=%v", id, err)
	}
	// ② 有存储：真落库并回传自增 ID
	st := newStore(t)
	e := New(config.C, nil, st, "sk")
	id, err := e.SaveRecord(context.Background(), 1, 2, 33, "translate", "en", "原文", "译文", scores, 77, "review_required")
	if err != nil || id <= 0 {
		t.Fatalf("落库失败: id=%d err=%v", id, err)
	}
	rec := loadEvalRecord(t, st, id)
	if rec.TicketID != 33 || rec.TaskType != "translate" || rec.Model != "en" || rec.Status != "review_required" {
		t.Fatalf("落库字段不符: %+v", rec)
	}
	if math.Abs(rec.Total-77) > 1e-9 {
		t.Fatalf("总分落库不符: %v", rec.Total)
	}
	var got map[string]float64
	if err := json.Unmarshal([]byte(rec.Scores), &got); err != nil {
		t.Fatalf("scores 列应为合法 JSON（下游徽标直接解析）: %v", err)
	}
	if got["term"] != 90 || got["style"] != 50 || len(got) != 5 {
		t.Fatalf("五维分数 JSON 不符: %+v", got)
	}
	// ③ 不同语言 / 不同阶段各自成行（评估流水，不去重）
	if _, err := e.SaveRecord(context.Background(), 1, 2, 33, "translate", "ja", "原文", "訳文", scores, 66, "done"); err != nil {
		t.Fatalf("另一语言落库失败: %v", err)
	}
	if _, err := e.SaveRecord(context.Background(), 1, 2, 33, "review", "en", "原文", "译文", scores, 80, "done"); err != nil {
		t.Fatalf("另一阶段落库失败: %v", err)
	}
	if n := countEvalRecords(t, st, 33); n != 3 {
		t.Fatalf("工单 33 应有 3 条评估流水（en/ja × translate/review），实得 %d", n)
	}
	// ④ 租户隔离：记录归属写入的租户，不得被别的租户读到
	if _, err := e.SaveRecord(context.Background(), 9, 2, 34, "translate", "en", "原文", "译文", scores, 77, "done"); err != nil {
		t.Fatalf("另一租户落库失败: %v", err)
	}
	if n := countEvalRecordsInTenant(t, st, 1, 34); n != 0 {
		t.Fatalf("租户 9 的记录不得出现在租户 1 的统计里，实得 %d", n)
	}
}

// loadEvalRecord 直读单条评估记录（断言库内实际写入的列值，不依赖上层封装）。
func loadEvalRecord(t *testing.T, st *store.Store, id int64) *store.EvalRecord {
	t.Helper()
	r := &store.EvalRecord{}
	err := st.DB().QueryRow(`SELECT COALESCE(tenant_id,0), COALESCE(user_id,0), COALESCE(ticket_id,0), COALESCE(task_type,''),
		COALESCE(model,''), COALESCE(scores,''), COALESCE(total,0), COALESCE(status,'') FROM eval_records WHERE id=?`, id).
		Scan(&r.TenantID, &r.UserID, &r.TicketID, &r.TaskType, &r.Model, &r.Scores, &r.Total, &r.Status)
	if err != nil {
		t.Fatalf("读取 eval_records 失败: %v", err)
	}
	return r
}

// countEvalRecords 统计指定工单的评估记录数。
func countEvalRecords(t *testing.T, st *store.Store, ticketID int64) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM eval_records WHERE ticket_id=?", ticketID).Scan(&n); err != nil {
		t.Fatalf("统计 eval_records 失败: %v", err)
	}
	return n
}

// countEvalRecordsInTenant 统计指定租户 + 工单的记录数（租户隔离断言）。
func countEvalRecordsInTenant(t *testing.T, st *store.Store, tenantID, ticketID int64) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM eval_records WHERE tenant_id=? AND ticket_id=?", tenantID, ticketID).Scan(&n); err != nil {
		t.Fatalf("统计 eval_records 失败: %v", err)
	}
	return n
}
