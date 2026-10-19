package engine

// ============ minimal_instruction_test.go · 职责说明 ============
// 锁「极简指令兜底腿」（★ 2026-10-19，现网工单 107 实证的中文泄露根因）。
//
// 缺陷形态：文件硬闸的逐段兜底腿走的是**同一条**带中文尾注的提示词（币种保真 + 输出纯度），
// 而 tencent/Hunyuan-MT-7B 是纯翻译模型——源段越短/越不像中文，它越会把 system 里那两条中文尾注
// 当成待译内容，产出 `and`（注记连词尾巴）或整段注记英译；IsTranslationUsable 据实判其不可用，
// 于是该段**每一轮都失败**，`金额`、`当前：全部允许·仓位留空/0 =使用全局单票金额` 这类真中文
// 原样留在成品与译图里（用户报的「中文泄露」）。实测对照：同一 12 段带尾注 11/12 出 `and`，
// 换成「骨架+契约」这一档 12/12 正确（含 `金额→Amount`）。
//
// 本文件两条腿各锁一半，缺一即失效：
//  ① 开关本身：带旗标时 system 里不许再有那两条尾注，不带旗标时**必须**还在（负向对照，
//     否则「有人把尾注从正常档里删掉」也会判绿——那是另一处产品口径变更，不该被这条锁顺带放行）；
//  ② 接线：translateSegmentMinimal 必须真的把旗标传下去（判据级锁不住"忘记传 ctx"）。
// 另配一条「同把尺子」腿：兜底腿的返回值仍要过 IsTranslationUsable，噪声不许被写进 langTranslations。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/llm"
)

// 两条中文尾注的**独占**标记（指令骨架里没有这两个串：骨架用的是「不要复述原文」，尾注用「不得复述原文」）。
const (
	miCurrencyMarker = "币种保真"
	miPurityMarker   = "不得复述原文"
	miCoreMarker     = "把下面的中文翻译为英语"
)

type miHarness struct {
	eng    *Engine
	mu     sync.Mutex
	sys    []string
	derail bool // true=按现网实测形态分档回复（带尾注回 "and"，极简档回 "Amount"）
}

// systemsSeen 返回抓到的每一份 system 提示词（按调用顺序）。
func (h *miHarness) systemsSeen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.sys...)
}

// miEngine 起一个假上游，把每次请求的 system 提示词记下来（reply 决定模型回什么）。
func miEngine(t *testing.T, reply string) *miHarness {
	t.Helper()
	h := &miHarness{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sys := ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				sys = m.Content
			}
		}
		h.mu.Lock()
		h.sys = append(h.sys, sys)
		derail := h.derail
		h.mu.Unlock()
		if derail {
			reply = "Amount"
			if strings.Contains(sys, miCurrencyMarker) {
				reply = "and" // 实测：带尾注时纯翻译模型对短段产出功能词噪声
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, reply)
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏进本包（AGENTS.md §4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	h.eng = &Engine{LLM: llm.NewClient(cfg), Cfg: cfg, breaker: NewBreaker(5, time.Minute)}
	return h
}

// TestMinimalInstructionDropsPromptNotes 腿①：旗标决定 system 里那两条中文尾注在不在。
func TestMinimalInstructionDropsPromptNotes(t *testing.T) {
	t.Run("正常档带尾注", func(t *testing.T) {
		h := miEngine(t, "Amount")
		if _, err := h.eng.SingleLangTranslate(context.Background(), "金额", "en", nil, "zh", config.StageAIInitial); err != nil {
			t.Fatalf("正常档翻译出错: %v", err)
		}
		sys := h.systemsSeen()
		if len(sys) == 0 {
			t.Fatal("没抓到 system 提示词（本轮锁失明）")
		}
		s := sys[len(sys)-1]
		if !strings.Contains(s, miCoreMarker) {
			t.Fatalf("正常档 system 应含指令骨架，got %q", s)
		}
		if !strings.Contains(s, miCurrencyMarker) || !strings.Contains(s, miPurityMarker) {
			t.Fatalf("正常档 system 必须仍带两条中文尾注（负向对照：尾注是被刻意保留下来的产品口径）got %q", s)
		}
	})
	t.Run("极简档去尾注留骨架", func(t *testing.T) {
		h := miEngine(t, "Amount")
		ctx := WithMinimalInstruction(context.Background())
		if _, err := h.eng.SingleLangTranslate(ctx, "金额", "en", nil, "zh", config.StageAIInitial); err != nil {
			t.Fatalf("极简档翻译出错: %v", err)
		}
		sys := h.systemsSeen()
		if len(sys) == 0 {
			t.Fatal("没抓到 system 提示词（本轮锁失明）")
		}
		s := sys[len(sys)-1]
		if strings.Contains(s, miCurrencyMarker) || strings.Contains(s, miPurityMarker) {
			t.Fatalf("极简档 system 不得再带币种/纯度中文尾注（正是它们被纯翻译模型当成待译内容）got %q", s)
		}
		if !strings.Contains(s, miCoreMarker) {
			t.Fatalf("极简档仍须保留指令骨架 got %q", s)
		}
		if !strings.Contains(s, "<t>") {
			t.Fatalf("极简档仍须保留输出契约（否则解析不出译文）got %q", s)
		}
	})
}

// TestHardGateLastResortUsesMinimalInstruction 腿②：硬闸末段腿真的把极简旗标传下去了。
// 反证（改坏法）：把 translateSegmentMinimal 里的 WithMinimalInstruction(ctx) 换回裸 ctx ⇒ 本腿红。
func TestHardGateLastResortUsesMinimalInstruction(t *testing.T) {
	t.Run("走极简档", func(t *testing.T) {
		h := miEngine(t, "Amount")
		v, ok := h.eng.translateSegmentMinimal(context.Background(), "金额", "en")
		if !ok || v != "Amount" {
			t.Fatalf("末段兜底应取到候选译文 Amount，got ok=%v v=%q", ok, v)
		}
		sys := h.systemsSeen()
		if len(sys) == 0 {
			t.Fatal("兜底腿没发出任何上游调用（本轮锁失明）")
		}
		if s := sys[len(sys)-1]; strings.Contains(s, miCurrencyMarker) || strings.Contains(s, miPurityMarker) {
			t.Fatalf("末段兜底腿必须以极简指令出站（未传 ctx 旗标时这里会带尾注）got %q", s)
		}
	})
}

// TestLastResortRescuesSegmentTheNormalLegLoses 腿③：现网形态的复现锁。
// 假上游按**实测读数**实现（不是"我以为的契约"）：system 里带币种尾注 ⇒ 回 `and`
// （工单 107 实测 11/12 就是这个读数）；不带尾注 ⇒ 回正确译文 `Amount`。
// 断言链就是用户报障的那条：正常腿拿不到可用译文 ⇒ 末段腿能救回来 ⇒ 该段不再计未译出。
func TestLastResortRescuesSegmentTheNormalLegLoses(t *testing.T) {
	h := miDerailEngine(t)
	ctx := context.Background()

	// 1) 正常腿（带尾注）：模型回 `and` ⇒ 判定不可用 ⇒ 这一段会被计未译出（现网症状）。
	norm, err := h.eng.TranslateOne(ctx, "金额", []string{"en"}, false, config.StageAIInitial)
	if err != nil {
		t.Fatalf("正常腿出错: %v", err)
	}
	if v := norm.Translations["en"]; IsTranslationUsable("金额", v) {
		t.Fatalf("正常腿在现网形态下应拿不到可用译文，got %q（假上游形态与实测读数不一致？）", v)
	}

	// 2) 末段腿（极简指令）：拿到 `Amount`，且写入点那一把尺子放行 ⇒ 中文不再泄露。
	v, ok := h.eng.translateSegmentMinimal(ctx, "金额", "en")
	if !ok || !IsTranslationUsable("金额", v) {
		t.Fatalf("末段腿应救回这一段，got ok=%v v=%q", ok, v)
	}
	if v != "Amount" {
		t.Fatalf("末段腿译文应为 Amount，got %q", v)
	}
}

// miDerailEngine 假上游按现网实测形态分档回复（见用例说明）。
func miDerailEngine(t *testing.T) *miHarness {
	t.Helper()
	h := miEngine(t, "")
	h.derail = true
	return h
}
