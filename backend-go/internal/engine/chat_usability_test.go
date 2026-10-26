// ============ chat_usability_test.go · 职责说明 ============
// P0-01（2026-10-25 云端 UAT）即时翻译可用性写入门的回归锁。
//
// 假上游按现网实测形态分档，而不是“我以为的契约”：
//   - 正常档（system 里带币种/纯度中文尾注）⇒ 回 "and"（Hunyuan-MT 退化形态）；
//   - 极简档（system 里没有那两条尾注）⇒ 回正确长译文；
//   - alwaysAnd 档 ⇒ 所有调用都回 "and"，用来验证“兜底也失败时诚实失败”；
//   - fallbackOnly 档 ⇒ 只有 model=fallback/Model 且极简 prompt 时才回正确译文，
//     用来验证 forcedFallback 开关真的切到了 cfg.HunyuanFallbackModel。
//
// 判据覆盖：
//  1. 正常档回 "and" ⇒ 极简腿救回，最终交付正确译文；
//  2. 所有腿都回 "and" ⇒ Error=upstream_failed，translations 为空，绝不交付 "and"；
//  3. 正常译文零额外调用（不误伤正常路径）；
//  4. en→zh 同文回吐判 identity（不能用文件链“ASCII 源同文可用”的例外放行）；
//  5. 正常含“翻译服务/translation service”的译文不被指令回显判据误杀；
//  6. finalizeChatTranslations 在闸门之后仍能清掉不可用串。
//
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
)

// chatFakeMode 假上游的分档口径（见文件头）。
type chatFakeMode int

// chatFakeMode 四种档位：derail=正常档回 and/极简档正确；alwaysAnd=全失败；
// fallbackOnly=只有 fallback 模型+极简档正确；correct=全部正确。
const (
	chatFakeDerail       chatFakeMode = iota // 正常档回 and，极简档回正确
	chatFakeAlwaysAnd                        // 所有调用都回 and
	chatFakeFallbackOnly                     // 只有 fallback 模型 + 极简档回正确
	chatFakeCorrect                          // 所有调用都回正确
)

// chatTestSrc 与 UAT V1 同形：含「第N段」结构标签。
const chatTestSrc = "这是一段用于端到端验收的中文原文。第二段：请翻译成英文并保留段落结构。第三段：金额与币种必须准确呈现。"

// chatTestGood 一段正常长译文：刻意不包含 translate/translation 等指令词，
// 避免把「指令回显」判据的误杀混进本条用例的因果关系。
const chatTestGood = "This is a Chinese original text for end-to-end acceptance. Paragraph two describes the paragraph structure. Paragraph three requires accurate amounts and currencies."

// chatFakeCall 记录一次上游调用的关键字段，供用例断言模型/提示词/审校腿。
type chatFakeCall struct {
	model  string // 请求体里的 model（验证 forcedFallback 真的切了模型）
	system string // system 提示词（验证极简档/正常档）
	user   string // 最后一条 user 消息（识别 review 腿是否被走到）
}

// chatFakeHarness 假上游 + 引擎 + 调用记录；所有字段都在用例内串行读，
// mode/call 用 mu 保护（HTTP handler 在独立 goroutine 跑）。
type chatFakeHarness struct {
	eng  *Engine
	mu   sync.Mutex
	mode chatFakeMode
	call []chatFakeCall
}

// calls 返回本用例观测到的全部上游调用（按顺序）。
func (h *chatFakeHarness) calls() []chatFakeCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]chatFakeCall(nil), h.call...)
}

// newChatFakeHarness 起一台假上游并构造无 Store 的 Engine。
// 无 Store 时 TranslateOne 走平台直翻（tid<=0），不会触碰 KB 查询；
// HandleText 的 tenantOK / fetchBrandTerms / applyOutputGates 都有 nil 保护。
func newChatFakeHarness(t *testing.T, mode chatFakeMode) *chatFakeHarness {
	t.Helper()
	h := &chatFakeHarness{mode: mode}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sys, userMsg := "", ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				sys = m.Content
			}
			if m.Role == "user" {
				userMsg = m.Content
			}
		}
		h.mu.Lock()
		h.call = append(h.call, chatFakeCall{model: req.Model, system: sys, user: userMsg})
		mode := h.mode
		h.mu.Unlock()

		reply := "and"
		switch mode {
		case chatFakeCorrect:
			reply = chatTestGood
		case chatFakeDerail:
			// 只有「有 system 且不含中文尾注」的极简档才回正确译文；
			// 续翻/反馈重译没有 system，同样回 "and"，保持现网退化形态。
			if sys != "" && !strings.Contains(sys, miCurrencyMarker) && !strings.Contains(sys, miPurityMarker) {
				reply = chatTestGood
			}
		case chatFakeFallbackOnly:
			if req.Model == "fallback/Model" && sys != "" && !strings.Contains(sys, miCurrencyMarker) {
				reply = chatTestGood
			}
		case chatFakeAlwaysAnd:
			// 保持 "and"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, reply)
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言，防 UAT 矩阵的 PG env 泄漏（AGENTS §4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	h.eng = NewEngine(cfg, nil, nil, nil)
	return h
}

// chatOptions 与前端 chat/stream 的请求同形（fast 档，避免 review 腿引入额外调用）。
func chatOptions(target string) map[string]interface{} {
	return map[string]interface{}{
		"target_langs": []interface{}{target},
		"mode":         "fast",
		"lang":         "zh",
	}
}

// TestChatUsabilityRescuesDegenerateAnd 正常档退化 ⇒ 极简腿救回。
func TestChatUsabilityRescuesDegenerateAnd(t *testing.T) {
	h := newChatFakeHarness(t, chatFakeDerail)
	res := h.eng.HandleText(context.Background(), chatTestSrc, chatOptions("en"), nil)
	if res == nil {
		t.Fatal("HandleText 不应返回 nil")
	}
	if res.Error != "" {
		t.Fatalf("极简腿应救回，不应报错（Error=%q Reply=%q）", res.Error, res.Reply)
	}
	got := strings.TrimSpace(res.Data.Translations["en"])
	if got != chatTestGood {
		t.Fatalf("最终译文应为极简腿的正确结果，got %q", got)
	}
	if strings.EqualFold(got, "and") {
		t.Fatal("退化串 and 被交付了")
	}
	minimalSeen := false
	for _, c := range h.calls() {
		if c.system != "" && !strings.Contains(c.system, miCurrencyMarker) && !strings.Contains(c.system, miPurityMarker) {
			minimalSeen = true
			break
		}
	}
	if !minimalSeen {
		t.Fatal("没有观测到极简档 system（修复腿没被走到，或旗标没传下去）")
	}
}

// TestChatUsabilityDropsWhenAllLegsDegenerate 所有腿都失败 ⇒ 诚实失败，绝不交付 and。
func TestChatUsabilityDropsWhenAllLegsDegenerate(t *testing.T) {
	h := newChatFakeHarness(t, chatFakeAlwaysAnd)
	res := h.eng.HandleText(context.Background(), chatTestSrc, chatOptions("en"), nil)
	if res == nil {
		t.Fatal("HandleText 不应返回 nil")
	}
	if res.Error != CodeUpstreamFailed {
		t.Fatalf("所有兜底都失败时应报 %s，实得 %q（Reply=%q）", CodeUpstreamFailed, res.Error, res.Reply)
	}
	if got := strings.TrimSpace(res.Data.Translations["en"]); got != "" {
		t.Fatalf("不可用译文必须清空，got %q", got)
	}
	if strings.TrimSpace(res.Reply) == "" {
		t.Fatal("失败必须带人类话术（前端顶部错误条要显示）")
	}
}

// TestChatUsabilityKeepsGoodTranslationWithoutExtraCalls 正常译文不得被误伤，也不得多调模型。
func TestChatUsabilityKeepsGoodTranslationWithoutExtraCalls(t *testing.T) {
	h := newChatFakeHarness(t, chatFakeCorrect)
	res := h.eng.HandleText(context.Background(), chatTestSrc, chatOptions("en"), nil)
	if res == nil || res.Error != "" {
		t.Fatalf("正常译文不应失败，res=%+v", res)
	}
	if got := strings.TrimSpace(res.Data.Translations["en"]); got != chatTestGood {
		t.Fatalf("正常译文被改动了，got %q", got)
	}
	if n := len(h.calls()); n != 1 {
		t.Fatalf("正常译文应只调用 1 次上游（修复腿只在不可用时触发），实际 %d 次", n)
	}
}

// TestChatUsabilitySkipsReviewForDegenerate pro 模式下，退化译文不应再送审校腿。
// 理由：审校模型对 "and" 这类退化串同样救不回来，送审只是多烧一次调用；
// 本用例要求最终仍由极简腿救回，且调用记录里不出现 reviewPromptFmt 的标记句。
func TestChatUsabilitySkipsReviewForDegenerate(t *testing.T) {
	h := newChatFakeHarness(t, chatFakeDerail)
	opts := chatOptions("en")
	opts["mode"] = "pro"
	res := h.eng.HandleText(context.Background(), chatTestSrc, opts, nil)
	if res == nil || res.Error != "" {
		t.Fatalf("pro 模式应被极简腿救回，res=%+v", res)
	}
	if got := strings.TrimSpace(res.Data.Translations["en"]); got != chatTestGood {
		t.Fatalf("pro 模式最终译文不对，got %q", got)
	}
	for _, c := range h.calls() {
		if strings.Contains(c.user, "资深翻译审校") {
			t.Fatal("退化译文被送进审校腿了（review 前应先过 chatUsabilityIssue 跳过）")
		}
	}
}

// TestChatUsabilityFallbackModelUsed 极简腿仍失败时，第二档必须真的切到 fallback 模型。
func TestChatUsabilityFallbackModelUsed(t *testing.T) {
	h := newChatFakeHarness(t, chatFakeFallbackOnly)
	res := h.eng.HandleText(context.Background(), chatTestSrc, chatOptions("en"), nil)
	if res == nil || res.Error != "" {
		t.Fatalf("fallback 模型应救回，res=%+v", res)
	}
	if got := strings.TrimSpace(res.Data.Translations["en"]); got != chatTestGood {
		t.Fatalf("fallback 模型兜底结果不对，got %q", got)
	}
	fallbackSeen := false
	for _, c := range h.calls() {
		if c.model == "fallback/Model" {
			fallbackSeen = true
			break
		}
	}
	if !fallbackSeen {
		t.Fatal("没有观测到 model=fallback/Model 的调用（forcedFallback 开关没生效）")
	}
}

// TestChatUsabilityRejectsIdentityAcrossLanguages en→zh 原样回吐英文必须判 identity。
func TestChatUsabilityRejectsIdentityAcrossLanguages(t *testing.T) {
	e := &Engine{}
	if got := e.chatUsabilityIssue(context.Background(), "Hello world", "en", "zh", "Hello world"); got != "identity" {
		t.Fatalf("en→zh 同文应判 identity，got %q", got)
	}
	// 同语种原样返回是正确交付形态，不能误判。
	if got := e.chatUsabilityIssue(context.Background(), "Hello world", "en", "en", "Hello world"); got != "" {
		t.Fatalf("同语种同文不应判不可用，got %q", got)
	}
	// 纯数字段无需翻译，原样交付也不能误判。
	if got := e.chatUsabilityIssue(context.Background(), "12345", "en", "zh", "12345"); got != "" {
		t.Fatalf("纯数字段同文不应判不可用，got %q", got)
	}
}

// TestChatUsabilityDoesNotOverkillNormalTranslation 正常含“翻译服务/translation service”的译文不得被指令回显误杀。
func TestChatUsabilityDoesNotOverkillNormalTranslation(t *testing.T) {
	e := &Engine{}
	src := "我们的翻译服务支持十二种语言，覆盖合同、说明书与技术文档。"
	out := "Our translation service supports twelve languages and covers contracts, manuals, and technical documents."
	if got := e.chatUsabilityIssue(context.Background(), src, "zh", "en", out); got != "" {
		t.Fatalf("正常译文被误杀：issue=%q（指令回显判据射程过宽）", got)
	}
}

// TestFinalizeChatTranslationsClearsDegenerate 闸门/纯度之后的最终断言必须能清掉退化串。
func TestFinalizeChatTranslationsClearsDegenerate(t *testing.T) {
	e := &Engine{}
	allTr := map[string]string{"en": "and", "ja": "これは正常な翻訳です。"}
	var warnings []string
	e.finalizeChatTranslations(context.Background(), chatTestSrc, "zh", allTr, &warnings)
	if allTr["en"] != "" {
		t.Fatalf("最终断言应清空 en=and，got %q", allTr["en"])
	}
	if allTr["ja"] == "" {
		t.Fatal("正常语种不得被最终断言误清")
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "译文不可用") {
		t.Fatalf("清空必须带语种警告，got %v", warnings)
	}
}
