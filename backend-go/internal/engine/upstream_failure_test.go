// ============================================================================
// upstream_failure_test.go — ★ 修法 F 的断言（A7 ＋ ⑭ 的出口收敛，2026-10-04 〇-AR 第 2 波）。
//
// 钉住的历史缺陷（本轮 UAT 实测到的形态）：HandleText 在「模型/知识库全腿都没产出可用译文」
// 时仍然返回 Error==""，因为 text.go 的组装段只是「跳过空译文不拼接」——空壳就此被装成成功。
// 三个客户面于是同时说谎：
//   - 对话 SSE 发 done 帧，气泡里是一句「📝 …翻译结果：📊 模式：纯模型翻译」后面什么都没有；
//   - OpenAPI 回 success:true ＋ 空 translations（客户按报文入账，记一次"成功但没产物"）；
//   - 只有试用面靠 trial.go 自己补了 500（同一次故障三种对外表现，正是 F-64 那批"契约不一致"的形态）。
//
// 判据收在 HandleText 出口这一条咽喉上：码走 Error、人话走 Reply，三面自然同口径
// （消费侧的断言见 internal/api/upstream_failure_face_test.go 与前端 useChat 的锁）。
//
// 反证（本文件里的三条各自对应一次"改回旧形态"）：
//
//	① 把 convergeEmptyResult 从 HandleText 里摘掉 ⇒ A7 红（回到空壳成功）；
//	② 把判据写成「Translations 为空」而非「请求确有目标语种」⇒ 早退话术分支被误判成失败，
//	   TestNonTranslationEarlyReturnsStayUntouched 红；
//	③ 把余额中止那一档并进 upstream_failed ⇒ 客户看到「暂时不可用，请稍后重试」而白等，
//	   TestBalanceAbortKeepsInsufficientCode 红。
//
// ============================================================================
package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"time"
	"translator/internal/config"

	"translator/internal/store"
	"translator/internal/tenant"
)

// failingUpstreamEngine 构造一台「上游一律 500」的引擎：每条翻译腿都失败，
// 用来复现 ⑭ 的空壳形态（不需要真模型，也不需要 KB）。
func failingUpstreamEngine(t *testing.T, status int) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	// 备用供应商也指向同一个 500 端点：本用例要的是"全腿失败"，
	// 否则 P1-6 的降级会打到真配置上（既慢又不可控，且判据就不再是纯函数了）。
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	e := NewEngine(cfg, nil, nil, nil)
	e.retryMaxAttempts = 2 // 空壳与重试轮数无关，压小只为让用例别白等退避
	e.retrySleep = 5 * time.Millisecond
	return e
}

// TestEmptyTranslationsSurfacesError = 断言 A7：全腿失败 ⇒ HandleText 必须报失败，
// 且码/文案分居 Error/Reply（消费侧据此下 error_code 与人话）。
func TestEmptyTranslationsSurfacesError(t *testing.T) {
	e := failingUpstreamEngine(t, http.StatusInternalServerError)
	options := map[string]interface{}{
		"target_langs": toInterfaceLangs([]string{"en", "ja"}),
		"mode":         "pro",
		"lang":         "zh",
	}
	ctx := tenant.WithMode(context.Background(), "pro")

	res := e.HandleText(ctx, "本公司专注于智能硬件的研发与设计", options, nil)
	if res == nil {
		t.Fatal("HandleText 不应返回 nil")
	}
	if res.Error == "" {
		t.Fatalf("⑭ 空壳形态回来了：全腿失败却 Error==\"\"（Reply=%q）——"+
			"对话会发 done 空壳、OpenAPI 会回 success:true", res.Reply)
	}
	if !IsStableErrorCode(res.Error) {
		t.Fatalf("Error 必须是登记的稳定码，实得 %q", res.Error)
	}
	if res.Error != CodeUpstreamFailed {
		t.Fatalf("上游全腿失败应报 %s，实得 %s", CodeUpstreamFailed, res.Error)
	}
	if strings.TrimSpace(res.Reply) == "" {
		t.Fatal("Reply 必须带人类话术（消费侧按码取不到词条时要回落它，空串＝空白气泡）")
	}
	// 决定性负向：旧形态那句"什么都没翻译出来"的脚手架不许再当成功文案发给客户。
	if strings.Contains(res.Reply, "翻译结果：") {
		t.Fatalf("失败结果的 Reply 仍带成功脚手架，客户会以为翻出来了：\n%s", res.Reply)
	}
	for _, lc := range []string{"en", "ja"} {
		if strings.TrimSpace(res.Data.Translations[lc]) != "" {
			t.Fatalf("上游 500 时不该有译文（%s=%q），说明假上游被打穿了", lc, res.Data.Translations[lc])
		}
	}
	if len(res.Data.TargetLangs) == 0 {
		t.Fatal("TargetLangs 为空会让判据失去依据（本用例判据正是「确有目标语种却零译文」）")
	}
}

// TestPartialSuccessIsNotFailed 部分成功不许整单作废：
// 一门语种成功＝客户拿到了可用的那部分，判据必须是「一条可用译文都没有」。
// 这条同时兜住"止损写宽"的风险（把漏一个语种报成整单失败，会把能用的结果也扣掉）。
func TestPartialSuccessIsNotFailed(t *testing.T) {
	res := &TextTranslateResult{
		Data: TextTranslateData{
			TargetLangs: []string{"en", "ja"},
			Translations: map[string]string{
				"en": "The company focuses on smart hardware.",
				"ja": "   ", // 上游失败后落进来的空串（旧形态就是这个形态）
			},
		},
	}
	e := &Engine{}
	if e.convergeEmptyResult(context.Background(), res) {
		t.Fatal("有一条可用译文就不算整单失败（不许把漏语种升级成漏整单）")
	}
	if res.Error != "" {
		t.Fatalf("部分成功不应被置码，实得 %q", res.Error)
	}
	if got := targetsAllEmpty(res); got {
		t.Fatal("targetsAllEmpty 判据错：存在非空译文时必须为 false")
	}
}

// TestBalanceAbortKeepsInsufficientCode 余额不足中止导致的零译文要报
// insufficient_balance（客户知道要充值），不许并成「上游不可用，请稍后重试」（客户会白等）。
func TestBalanceAbortKeepsInsufficientCode(t *testing.T) {
	e := &Engine{}
	res := &TextTranslateResult{
		Data: TextTranslateData{
			TargetLangs:  []string{"en"},
			Translations: map[string]string{"en": ""},
		},
	}
	// 实时计费中止的现场形态：WithUsageRecorder 用 WithCancelCause 注入 cause，
	// abortReasonFrom 读的就是它（见 engine.go 的注释）。
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(store.ErrInsufficientBalance)

	if !e.convergeEmptyResult(ctx, res) {
		t.Fatal("零译文必须被收敛成失败")
	}
	if res.Error != CodeInsufficientBalance {
		t.Fatalf("欠费中止应报 %s（与 billing/api/OpenAPI 既有对外码同源），实得 %s",
			CodeInsufficientBalance, res.Error)
	}
	if !strings.Contains(res.Reply, "余额不足") {
		t.Fatalf("Reply 要带中止原因，实得 %q", res.Reply)
	}
	if len(res.Data.GateWarnings) == 0 {
		t.Fatal("失败话术要同时进 GateWarnings（前端结构化展示走这一字段）")
	}
}

// TestNonTranslationEarlyReturnsStayUntouched 非翻译类早退分支（空输入／未指定语言／账号不可用）
// 回的是**有内容的话术**、TargetLangs 为空，不许被出口收敛误判成"翻译失败"。
// 判据②的反证就在这条：把它改成「Translations 为空即失败」会当场红。
func TestNonTranslationEarlyReturnsStayUntouched(t *testing.T) {
	e := &Engine{}
	for _, tc := range []struct {
		name string
		res  *TextTranslateResult
	}{
		{"空输入", &TextTranslateResult{Reply: "请输入要翻译的文本"}},
		{"未指定语言", &TextTranslateResult{Reply: "你选择了「其他语言」，但没告诉我翻译成什么语言"}},
		{"账号不可用", &TextTranslateResult{Reply: "❌ 账号不可用"}},
	} {
		if e.convergeEmptyResult(context.Background(), tc.res) {
			t.Fatalf("%s：这类分支没有目标语种，不该被改写成失败（实得 Error=%q）", tc.name, tc.res.Error)
		}
		if tc.res.Error != "" {
			t.Fatalf("%s：Error 应保持为空，实得 %q", tc.name, tc.res.Error)
		}
	}
	// nil 安全（HandleText 的调用方里有判 nil 的分支，这里不能反过来 panic）
	if e.convergeEmptyResult(context.Background(), nil) {
		t.Fatal("nil 结果必须返回 false")
	}
}

// TestStableErrorCodeRegistryShape 登记表自身的形状锁：
// 三个码都在册；**人话不是码**（否则 ① 那类"本来就是中文句子"的 Error 会被当键名下发）。
func TestStableErrorCodeRegistryShape(t *testing.T) {
	for _, code := range []string{CodeSensitiveBlocked, CodeUpstreamFailed, CodeInsufficientBalance} {
		if !IsStableErrorCode(code) {
			t.Fatalf("稳定码 %s 不在登记表里：消费侧会把它当文案发给客户（F-53 同形态）", code)
		}
	}
	if IsStableErrorCode("文件不存在或无法读取") {
		t.Fatal("人类话术不得进稳定码表")
	}
	if IsStableErrorCode("") || IsStableErrorCode("   ") {
		t.Fatal("空串不得判成稳定码（HandleText 用 Error==\"\" 表示成功）")
	}
	// 码值字面量锁：对外契约一旦发布就是客户按串分支的依据，改名＝破坏契约。
	if CodeUpstreamFailed != "upstream_failed" {
		t.Fatalf("对外码字面量被改动：实得 %q", CodeUpstreamFailed)
	}
}
