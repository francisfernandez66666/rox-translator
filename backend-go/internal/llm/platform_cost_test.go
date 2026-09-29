// ============================================================================
// platform_cost_test.go · 职责说明
// 「平台承担用量」上下文标记（llm.WithPlatformCost）的常设锁（★ 2026-09-29 〇-AD）。
//
// 钉死三条：
//
//	① 标记语义：注入后可原样取回；空 reason 回落 system_task（不许回落成「不标记」——
//	   那样一条后台任务就会因为调用方忘了传原因而扣客户积分，是本批唯一方向的失效模式）；
//	   nil ctx 不 panic（WithPlatformCost 与 PlatformCostFromCtx 两侧都要能扛住）；
//	② **知识库 Embedding 出口自动带标记**：EmbedBatch 是全系统唯一的 embed 咽喉
//	   （Engine 查询侧、embedcache 预置、索引重建、cmd/rebuild-kb-index 全走它），
//	   在这里打标＝所有现存与未来的 embed 调用点天然平台承担，不依赖每个调用方记得包；
//	③ 负向对照：普通 CallChat **不带**标记（默认仍是「谁用谁付」）。
//	   这条是②的反证——没有它，「ctx 恒带标记」这类写反的实现也能让②绿灯全过。
//
// 判据取在 OnUsage 回调收到的 ctx 上（计费侧真正读标记的地方），比断言返回值更贴咽喉。
// 全用 httptest 本地 mock 端点，不依赖外网、不触库（无需钉 DB_DRIVER）。
// 运行：go test -count=1 ./internal/llm/ -run PlatformCost
// ============================================================================
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/config"
)

// TestPlatformCostMarkerSemantics ①：标记的注入/取出/空值回落/nil 安全。
func TestPlatformCostMarkerSemantics(t *testing.T) {
	t.Run("显式原因原样取回", func(t *testing.T) {
		reason, ok := PlatformCostFromCtx(WithPlatformCost(context.Background(), PlatformKBEmbed))
		if !ok || reason != PlatformKBEmbed {
			t.Fatalf("应取回 kb_embed，实得 reason=%q ok=%v", reason, ok)
		}
	})
	t.Run("空原因回落 system_task（不许回落成未标记）", func(t *testing.T) {
		reason, ok := PlatformCostFromCtx(WithPlatformCost(context.Background(), ""))
		if !ok || reason != PlatformSystemTask {
			t.Fatalf("空 reason 必须仍标记且回落 system_task，实得 reason=%q ok=%v", reason, ok)
		}
	})
	t.Run("未注入即未标记（默认扣租户余额）", func(t *testing.T) {
		if reason, ok := PlatformCostFromCtx(context.Background()); ok {
			t.Fatalf("裸 context 不该带平台承担标记，实得 reason=%q", reason)
		}
	})
	t.Run("nil ctx 不 panic", func(t *testing.T) {
		var nilCtx context.Context //nolint:staticcheck // 就是要造 nil，验调用方漏传时不炸进程
		_ = WithPlatformCost(nilCtx, PlatformEvals)
		if _, ok := PlatformCostFromCtx(nilCtx); ok {
			t.Fatal("nil ctx 不该判为已标记")
		}
	})
}

// embedOKBody 假上游的 embeddings 响应体（带 usage，供 OnUsage 回调取量）。
func embedOKBody() string {
	b, _ := json.Marshal(map[string]any{
		"data":  []map[string]any{{"embedding": []float64{1, 2, 3}}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 0, "total_tokens": 11},
	})
	return string(b)
}

// TestEmbedBatchMarksPlatformCost ②＋③：embed 咽喉自动带 kb_embed 标记，chat 通路不带。
func TestEmbedBatchMarksPlatformCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 同一条假上游按端点分流：/embeddings 回嵌入体，其余回 chat 体（embed 的 base 由 cfg 拼，
		// chat 的 base 由调用方传，两者都指向本 srv，故只能按路径区分是哪条通路）。
		if strings.Contains(r.URL.Path, "/embeddings") {
			_, _ = w.Write([]byte(embedOKBody()))
			return
		}
		_, _ = w.Write([]byte(chatOKBody("你好世界", "stop")))
	}))
	defer srv.Close()

	// 按 model 分别记 OnUsage 收到的 ctx（计费侧读标记的那一层拿到的就是这一只）。
	seen := map[string]context.Context{}
	c := newTestClient(t, func(cfg *config.Config) {
		cfg.EmbedAPIBase = srv.URL // chat 侧 base 由调用参数传入，这里只需钉 embed
	})
	c.OnUsage = func(ctx context.Context, model string, prompt, completion int64) error {
		seen[model] = ctx
		return nil
	}

	if _, err := c.EmbedBatch(context.Background(), []string{"要嵌入的文本"}); err != nil {
		t.Fatalf("EmbedBatch 不该报错: %v", err)
	}
	embedCtx, called := seen[c.cfg.EmbedModel]
	if !called {
		t.Fatalf("embed 用量没进 OnUsage（判据失去落点，本条断言即空转）：seen=%v", keysOf(seen))
	}
	if reason, ok := PlatformCostFromCtx(embedCtx); !ok || reason != PlatformKBEmbed {
		t.Fatalf("embed 用量必须带 kb_embed 标记（否则平台承担没接上、仍扣客户积分），实得 reason=%q ok=%v", reason, ok)
	}

	// ③ 反证：同一条链路上的普通翻译调用不许被顺手标成平台承担。
	if _, _, err := c.CallChat(context.Background(), srv.URL, "sk-test", "chat-probe",
		[]map[string]string{{"role": "user", "content": "hi"}}, 128, false, 0.3); err != nil {
		t.Fatalf("CallChat 不该报错: %v", err)
	}
	chatCtx, called := seen["chat-probe"]
	if !called {
		t.Fatalf("chat 用量没进 OnUsage（反证腿没走到目标分支）：seen=%v", keysOf(seen))
	}
	if reason, ok := PlatformCostFromCtx(chatCtx); ok {
		t.Fatalf("普通 CallChat 不该带平台承担标记（默认口径是谁用谁付），实得 reason=%q", reason)
	}
}

// keysOf 返回 seen 的键列表，仅用于失败信息可读。
func keysOf(m map[string]context.Context) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestSelfLedgeredUsageFlag 〇-AD 补丁二：抑制位的语义锁。
// 它只管「钩子不要再补一行留痕」，绝不改动「平台承担＝不扣费」这个判定；
// 反证腿（未设置时必须为 false）保证这不是一个恒真的空壳。
func TestSelfLedgeredUsageFlag(t *testing.T) {
	if IsSelfLedgeredUsage(nil) {
		t.Fatalf("nil ctx 不该被判成自备台账")
	}
	base := context.Background()
	if IsSelfLedgeredUsage(base) {
		t.Fatalf("反向对照失效：裸 ctx 恒为 true，抑制位等于常开（所有留痕行都会消失）")
	}
	if got := WithSelfLedgeredUsage(nil); got != nil {
		t.Fatalf("nil ctx 应原样返回 nil，实得非 nil")
	}
	marked := WithSelfLedgeredUsage(base)
	if !IsSelfLedgeredUsage(marked) {
		t.Fatalf("设置后应读到 true")
	}
	// 抑制位与平台标记互相独立：只打抑制不打标记 ⇒ 依旧按「未标注＝照常扣费」处理
	if _, ok := PlatformCostFromCtx(marked); ok {
		t.Fatalf("抑制位不该被当成平台承担标记（否则客户用量会被免扣费）")
	}
	// 两者同时打 ⇒ 标记仍在（不扣费成立），抑制位也在（钩子不补行）
	both := WithSelfLedgeredUsage(WithPlatformCost(base, PlatformKBEmbed))
	if r, ok := PlatformCostFromCtx(both); !ok || r != PlatformKBEmbed {
		t.Fatalf("叠加抑制位后平台标记丢了：%q ok=%v", r, ok)
	}
	if !IsSelfLedgeredUsage(both) {
		t.Fatalf("叠加后抑制位丢了")
	}
}
