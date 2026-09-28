// ============ review_snapshot_test.go · 职责说明 ============
// 〇-Z（2026-09-28）回归：文本主路径「AI 校对」阶段的并发写图纪律。
//
// 复现的缺陷：handleTextCore 的校对环节旧写法是
//
//	for lc, tr := range allTr { go func(){ … allTr[lc] = revised }() }
//
// 互斥锁只挡住了校对线程**彼此**的写，挡不住「主线程还在 range、先放出去的线程正在写
// 同一张 map」。map 的边遍历边写是 Go 的 runtime fatal error
// （concurrent map iteration and map write），**不是 panic**——
// 同文件里的 recoverPipeline 兜不住，进程直接挂，表现为「翻译接口偶发整站 502/连接被重置」
// 而不是某一次请求失败。
//
// 为什么线上一直没炸、单测却稳定红：真模型调用要几百毫秒到几秒，range 早就跑完了；
// 本地假上游**瞬间返回**，线程回写就落在 range 还没结束的时候。
// 本用例因此必须配 -race 跑（race detector 会把这一对访问报出来；
// 不加 -race 时它属于"运气好没被调度到"的假绿）。
//
// 运行：env DB_DRIVER=sqlite go test -race -count=1 ./internal/engine/ -run TestReview
// =============================================
package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"translator/internal/config"
	"translator/internal/tenant"
)

// reviewSnapshotEngine 构造一个「上游瞬间回话」的引擎，专供并发窗口复现。
// 参数 replies: 每次上游调用返回的译文字面量（按序取用，取完重复最后一个）。
func reviewSnapshotEngine(t *testing.T, hits *int64) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		// 译文不含数字 ⇒ 不触发「数字保持」重翻分支，本用例只盯并发编排
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"The company focuses on smart hardware research and development"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS.md §一·4）：防 PG env 泄漏进本包
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	// NewEngine 而非结构体字面量：熔断器与各缓存私有字段由它一并初始化。
	// KB/Index 传 nil：ctx 租户为 0（平台上下文）时 translateOneInner 本来就跳过全部
	// 知识库匹配走纯模型翻译，与生产「平台级账号试用/无 KB 场景」同一条路径。
	return NewEngine(cfg, nil, nil, nil)
}

// TestReviewStageNeverWritesMapWhileIterating ★ 校对阶段的 range/写并发回归（见文件头）。
// 判据两件事：① 多语种一次请求里全部拿到译文（编排没被改坏、任务没漏发）；
// ② 整包 -race 无 DATA RACE 报告（这才是本用例真正钉的东西）。
func TestReviewStageNeverWritesMapWhileIterating(t *testing.T) {
	var hits int64
	e := reviewSnapshotEngine(t, &hits)

	langs := []string{"en", "ja", "de", "fr", "es", "ru", "th"}
	options := map[string]interface{}{
		"target_langs": toInterfaceLangs(langs),
		"mode":         "pro", // pro 才带反馈重翻，并发窗口最宽
		"lang":         "zh",
	}
	// 平台上下文（tenant 0）：无知识库依赖，纯模型链路
	ctx := tenant.WithMode(context.Background(), "pro")

	res := e.HandleText(ctx, "本公司专注于智能硬件的研发与设计", options, nil)
	if res == nil {
		t.Fatal("HandleText 返回 nil，无法判定校对编排")
	}
	if res.Error != "" {
		t.Fatalf("多语种翻译不应整体失败，Error=%q", res.Error)
	}
	missing := make([]string, 0, len(langs))
	for _, lc := range langs {
		if res.Data.Translations[lc] == "" {
			missing = append(missing, lc)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("每个目标语种都要有译文，缺失 %v（校对阶段若把任务编排改坏就会漏语种）", missing)
	}
	if n := atomic.LoadInt64(&hits); n == 0 {
		t.Fatal("假上游一次都没被打，说明请求没走到模型链路，本用例失去意义")
	} else {
		t.Logf("上游调用次数=%d，语种数=%d（pro 每语种初翻＋校对各一次）", n, len(langs))
	}
}

// toInterfaceLangs 把 []string 转成 options 期望的 []interface{}（HTTP JSON 解出来的形态）。
func toInterfaceLangs(langs []string) []interface{} {
	out := make([]interface{}, 0, len(langs))
	for _, l := range langs {
		out = append(out, l)
	}
	return out
}
