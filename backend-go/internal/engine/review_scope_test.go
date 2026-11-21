// ============ review_scope_test.go · 职责说明 ============
// 「复核链路运营开关」（★ 2026-11-21 用户令：即时翻译 / 翻译工单各自可配）的机械锁。
//
// 这一族最容易长出的缺陷不是"读错了值"，而是**读了却没生效**：
// 引擎里 6+ 个复核调用点（ReviewTranslation / ReviewTranslationBatch / 闸门带反馈重译 /
// 上下文重译 / 补译梯 / 工单审校与复核 evals）各自判一次，漏一处就是"运营关了它还在烧模型"，
// 而屏幕上没有任何读数会指出这件事。所以本文件按三层判：
//
//	A 作用域：缺省＝即时；显式 ticket 才走工单档；非法值归一成即时（不许出现第三档）。
//	B 解析：QualityOf 的缓存键是 ops_policy **原文**（原文一变自然失效），
//	  避免"改完配置要重启才生效"这种没人想得起来的坑。
//	C 生效：真起一个假上游，关闭档**零模型调用**且返回值是"这一轮没改"的形态；
//	  开启档必须真打一枪（正向对照——只测关闭侧等于给"闸门被整个摘掉"发绿灯）。
//
// 只起 httptest 假上游 + 内存库，不碰真模型、不碰真库。
// ============================================
package engine

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/llm"
	"translator/internal/store"
)

// reviewTestStore 建一个共享缓存的内存库（★ AGENTS §25-b：裸 :memory: 是"每连接一个库"，
// 迁移落在连接 A、后续 SELECT 可能落连接 B ⇒ no such table 假红）。
func reviewTestStore(t *testing.T) *store.Store {
	t.Helper()
	oldCfg := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = oldCfg })

	dsn := "file:review_scope_test?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	// 共享缓存库在最后一个连接关闭前一直存在；这里刻意不关，避免跨用例被回收后重建。
	t.Cleanup(func() { _ = db.Close() })
	s, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return s
}

// reviewTestEngine 装配一个能真打模型的引擎（假上游），并按需挂上策略库。
// 参数：policyRaw=ops_policy 原文（空串＝不写库，走"读不到＝按开"那一档）。
func reviewTestEngine(t *testing.T, reply func(user string) string, policyRaw string) (*Engine, *imgStub) {
	t.Helper()
	e, stub := imgEngine(t, reply)
	if policyRaw != "" {
		st := reviewTestStore(t)
		if err := st.SetConfig("ops_policy", policyRaw); err != nil {
			t.Fatalf("写 ops_policy 失败: %v", err)
		}
		e.St = st
	}
	return e, stub
}

// TestReviewScopeDefaultsToInstant A 腿：缺省即时档，显式 ticket 才换档，非法值归一。
func TestReviewScopeDefaultsToInstant(t *testing.T) {
	if got := reviewScopeFrom(context.Background()); got != ReviewScopeInstant {
		t.Fatalf("缺省作用域应为即时档，实得 %q", got)
	}
	if got := reviewScopeFrom(nil); got != ReviewScopeInstant {
		t.Fatalf("nil ctx 应回落即时档（不许 panic），实得 %q", got)
	}
	//nolint:staticcheck // 故意传 nil ctx 验回落
	if got := reviewScopeFrom(context.TODO()); got != ReviewScopeInstant {
		t.Fatalf("空标记应回落即时档，实得 %q", got)
	}

	ctx := WithReviewScope(context.Background(), ReviewScopeTicket)
	if got := reviewScopeFrom(ctx); got != ReviewScopeTicket {
		t.Fatalf("显式打标后应为工单档，实得 %q", got)
	}
	// 反向：从 ticket ctx 再派生一个（不改标记）仍必须是 ticket —— 派生不会丢标记
	derived := WithProgressCallback(ctx, func(string) {})
	if got := reviewScopeFrom(derived); got != ReviewScopeTicket {
		t.Fatalf("派生 ctx 丢了作用域标记，实得 %q", got)
	}
	// 非法值归一成即时（只此两档，不许长出第三档）
	for _, bad := range []string{"", "TICKET", "tickets", "admin"} {
		if got := reviewScopeFrom(WithReviewScope(context.Background(), bad)); got != ReviewScopeInstant {
			t.Fatalf("非法作用域 %q 应归一成即时档，实得 %q", bad, got)
		}
	}
}

// TestQualityOfCachedInvalidatesOnRawChange B 腿：缓存键＝原文，改配置立刻生效。
func TestQualityOfCachedInvalidatesOnRawChange(t *testing.T) {
	off := `{"quality":{"instant_review_enabled":false}}`
	on := `{"quality":{"instant_review_enabled":true}}`

	if got := qualityOfCached(off); got.InstantReviewEnabled {
		t.Fatal("原文声明关闭却解析成开启")
	}
	// 同一份原文必须命中缓存并给同一个答案（否则"现读现算"的说法不成立）
	if got := qualityOfCached(off); got.InstantReviewEnabled {
		t.Fatal("同一份原文两次解析结果不一致")
	}
	// 原文一变，缓存必须自然失效——这条是"改完配置不用重启"的全部依据
	if got := qualityOfCached(on); !got.InstantReviewEnabled {
		t.Fatal("原文改成开启后仍读到旧值（缓存没有按原文失效 ⇒ 运营改了不生效）")
	}
	// 再切回去也必须跟得上（不是只认"第一次见到的那一份"）
	if got := qualityOfCached(off); got.InstantReviewEnabled {
		t.Fatal("切回关闭档后仍读到旧值")
	}
	// 空串（库读不到／没配过）⇒ 出厂默认全开
	if got := qualityOfCached(""); !got.InstantReviewEnabled || !got.TicketReviewEnabled {
		t.Fatalf("空原文应回落全开，实得 %+v", got)
	}
}

// TestReviewEnabledMatrix 两档互相独立，读不到策略时按开。
func TestReviewEnabledMatrix(t *testing.T) {
	instantCtx := context.Background()
	ticketCtx := WithReviewScope(context.Background(), ReviewScopeTicket)

	// 1) 没有 Store（读不到策略）⇒ 两档都按开
	eNoStore := &Engine{}
	if !eNoStore.ReviewEnabled(instantCtx) || !eNoStore.ReviewEnabled(ticketCtx) {
		t.Fatal("读不到策略时应按开处理（一次库抖动不许把全站质量静默降一档）")
	}
	// nil 引擎也不许 panic，且按开
	var nilEng *Engine
	if !nilEng.ReviewEnabled(instantCtx) {
		t.Fatal("nil 引擎应回落按开")
	}

	// 2) 只关即时档：即时关、工单开
	e, _ := reviewTestEngine(t, func(string) string { return "x" }, `{"quality":{"instant_review_enabled":false}}`)
	if e.ReviewEnabled(instantCtx) {
		t.Fatal("即时档已关，ReviewEnabled(即时 ctx) 仍返回开")
	}
	if !e.ReviewEnabled(ticketCtx) {
		t.Fatal("只关即时档却把工单档一起关了（两档耦合＝运营无法只降即时成本）")
	}

	// 3) 只关工单档：工单关、即时开
	e2, _ := reviewTestEngine(t, func(string) string { return "x" }, `{"quality":{"ticket_review_enabled":false}}`)
	if e2.ReviewEnabled(ticketCtx) {
		t.Fatal("工单档已关，ReviewEnabled(工单 ctx) 仍返回开")
	}
	if !e2.ReviewEnabled(instantCtx) {
		t.Fatal("只关工单档却把即时档一起关了")
	}

	// 4) 两档都关
	e3, _ := reviewTestEngine(t, func(string) string { return "x" },
		`{"quality":{"instant_review_enabled":false,"ticket_review_enabled":false}}`)
	if e3.ReviewEnabled(instantCtx) || e3.ReviewEnabled(ticketCtx) {
		t.Fatal("两档都关时仍有一档返回开")
	}
}

// TestReviewTranslationSkipsModelWhenDisabled C 腿（关闭侧）：零模型调用、返回"这一轮没改"。
func TestReviewTranslationSkipsModelWhenDisabled(t *testing.T) {
	e, stub := reviewTestEngine(t, func(string) string { return "This is a revised translation." },
		`{"quality":{"instant_review_enabled":false,"ticket_review_enabled":false}}`)

	for _, ctx := range []context.Context{
		context.Background(),
		WithReviewScope(context.Background(), ReviewScopeTicket),
	} {
		if got := e.ReviewTranslation(ctx, "产品说明书", "Product manual", "en", config.StageReview); got != "" {
			t.Fatalf("复核链路关闭时 ReviewTranslation 应返回空串（＝这一轮没改），实得 %q", got)
		}
		out := e.ReviewTranslationBatch(ctx, []string{"产品说明书"}, []string{"Product manual"}, "en", config.StageReview)
		if len(out) != 1 || out[0] != "Product manual" {
			t.Fatalf("复核链路关闭时 ReviewTranslationBatch 必须原样返回，实得 %+v", out)
		}
	}
	if n := stub.calls(); n != 0 {
		t.Fatalf("复核链路关闭后仍打了 %d 次上游——开关没落在咽喉上（关了还在烧模型）", n)
	}
}

// TestReviewTranslationCallsModelWhenEnabled C 腿（开启侧，正向对照）：
// 只测关闭侧等于给"闸门被整个摘掉"发绿灯，所以这里必须证明开关打开时真会拨一枪。
func TestReviewTranslationCallsModelWhenEnabled(t *testing.T) {
	e, stub := reviewTestEngine(t, func(string) string { return "This is a revised translation." }, "")

	got := e.ReviewTranslation(context.Background(), "产品说明书", "Product manual", "en", config.StageReview)
	if got == "" {
		t.Fatal("复核链路开启时 ReviewTranslation 必须给出审校结果（空串＝闸门把正向也拦了）")
	}
	if n := stub.calls(); n == 0 {
		t.Fatal("复核链路开启时一次上游都没拨——关闭侧的绿灯无从对照（判据失明）")
	}

	before := stub.calls()
	out := e.ReviewTranslationBatch(context.Background(),
		[]string{"产品说明书"}, []string{"Product manual"}, "en", config.StageReview)
	if len(out) != 1 {
		t.Fatalf("批量审校必须与输入等长，实得 %+v", out)
	}
	if stub.calls() == before {
		t.Fatal("复核链路开启时 ReviewTranslationBatch 一次上游都没拨")
	}
}

// 编译期护栏：本文件依赖 llm 包（假上游走真客户端），别让后续重构把它删成死导入。
var _ = llm.NewClient
