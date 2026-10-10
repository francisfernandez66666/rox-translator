// ============================================================================
// reply_shape_test.go — ★ 14.3/14.4 输出净化断言（2026-10-10 决策⑪配套的纯度与复制净化批）。
//
// 钉住的形态变化：HandleText 的 Reply 从
//
//	「📝 「原文」翻译结果：… 📊 模式：… ⚡ 本次翻译消耗 N 积分」
//
// 收敛为**只含译文本体**（单目标语＝裸译文；多目标语＝「语言名：译文」逐行）。
// 原文/模式/积分改走结构化出参（Data.SourceText / Data.Mode / PointsUsed）：
// 14.3 要的是译文气泡里不再出现原词复述，14.4 要的是复制按钮复制到的就是可直接
// 粘贴的译文——脚手架拼在文案里，这两个诉求都会被穿透。
//
// 口径锁（既有断言不改方向）：upstream_failure_test.go 的「Reply 不得含『翻译结果：』」
// 在新形态下天然成立；usage_single_source_test.go 的 ModeBadgeLabel 字面量锁不动
// （mode 仍在 Data.Mode 里，只是不再拼进 Reply）。
//
// 方言口径（AGENTS §一·4）：本文件自钉 sqlite 并恢复 config.C。
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/tenant"
)

// happyUpstreamEngine 构造一台「上游恒定回同一句译文」的引擎（fast 全链，不需要
// 真模型与 KB）。校对腿拿到同一句会原样保留；en 之外的目标语若触发审校纯度拒绝，
// 调用方退回初翻——对回复**形状**断言无影响。
func happyUpstreamEngine(t *testing.T, content string) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": content}},
			},
		})
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	e := NewEngine(cfg, nil, nil, nil)
	e.retryMaxAttempts = 2
	e.retrySleep = 5 * time.Millisecond
	return e
}

// assertPurifiedReply 回复体净化判据：六类脚手架一个都不许出现。
// （🤖 是旧多语行尾缀；「原文」来自旧首段的「📝 「原文」翻译结果：」骨架。）
func assertPurifiedReply(t *testing.T, reply string) {
	t.Helper()
	for _, bad := range []string{"📝", "📊", "⚡", "🤖", "翻译结果：", "「原文」", "本次翻译消耗"} {
		if strings.Contains(reply, bad) {
			t.Fatalf("Reply 仍含净化前的脚手架 %q（原文/模式/积分必须走结构化字段）：\n%s", bad, reply)
		}
	}
}

// TestReplyBodyIsTranslationOnly 单目标语：回复体＝译文本体；原文/模式在结构化字段里齐备。
func TestReplyBodyIsTranslationOnly(t *testing.T) {
	const want = "The company focuses on smart hardware."
	e := happyUpstreamEngine(t, want)
	options := map[string]interface{}{
		"target_langs": toInterfaceLangs([]string{"en"}),
		"mode":         "fast",
		"lang":         "zh",
	}
	ctx := tenant.WithMode(context.Background(), "fast")

	res := e.HandleText(ctx, "本公司专注于智能硬件的研发与设计", options, nil)
	if res == nil {
		t.Fatal("HandleText 不应返回 nil")
	}
	if res.Error != "" {
		t.Fatalf("成功路径不该报错：Error=%s Reply=%q", res.Error, res.Reply)
	}
	assertPurifiedReply(t, res.Reply)

	tr := strings.TrimSpace(res.Data.Translations["en"])
	if tr == "" {
		t.Fatal("结构化 Translations 缺失（14.4 复制按钮与前端渲染都吃这个字段）")
	}
	// 质检闸门若出警告会追加在回复尾部，故用前缀判等而非全等。
	if !strings.HasPrefix(strings.TrimSpace(res.Reply), tr) {
		t.Fatalf("回复体必须是译文本体开头：Reply=%q 译文=%q", res.Reply, tr)
	}
	// 14.3：原文走 Data.SourceText（前端「查看原文」消费），不许再拼回内容。
	if res.Data.SourceText != "本公司专注于智能硬件的研发与设计" {
		t.Fatalf("SourceText 应为清洗后的原文，实得 %q", res.Data.SourceText)
	}
	// 14.4：模式只走 Data.Mode（前端徽标渲染），字面量契约由 usage_single_source_test 锁。
	if res.Data.Mode == "" {
		t.Fatal("Data.Mode 不应为空（前端模式徽标吃这个字段）")
	}
}

// TestReplyBodyMultiTargetPerLangLines 多目标语：逐行「语言名：译文」，不带 🤖 尾缀，
// 且整体仍是净化后的回复体。
func TestReplyBodyMultiTargetPerLangLines(t *testing.T) {
	e := happyUpstreamEngine(t, "The company focuses on smart hardware.")
	options := map[string]interface{}{
		"target_langs": toInterfaceLangs([]string{"en", "ja"}),
		"mode":         "fast",
		"lang":         "zh",
	}
	ctx := tenant.WithMode(context.Background(), "fast")

	res := e.HandleText(ctx, "本公司专注于智能硬件的研发与设计", options, nil)
	if res == nil {
		t.Fatal("HandleText 不应返回 nil")
	}
	if res.Error != "" {
		t.Fatalf("成功路径不该报错：Error=%s Reply=%q", res.Error, res.Reply)
	}
	assertPurifiedReply(t, res.Reply)
	if !strings.Contains(res.Reply, "英语：") || !strings.Contains(res.Reply, "日语：") {
		t.Fatalf("多目标语回复体必须逐行带语言名前缀：\n%s", res.Reply)
	}
}
