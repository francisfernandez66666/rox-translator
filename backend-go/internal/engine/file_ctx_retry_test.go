// ============ file_ctx_retry_test.go · 职责说明 ============
// 「上下文重译」这条腿的锁（★ 2026-11-21 用户令：翻译不出的内容要带上下文进复核模型重新翻译）。
//
// 为什么必须有这组断言：这条腿挂在硬闸全部腿之后、按原文回落之前，是"最后一条能救回来的路"。
// 它有三种典型的坏法，且**屏幕上都只有一行正常日志**：
//
//	① 语境没真进请求（拼了 note 但没写进 user 尾）⇒ 与普通重译无异，白烧一次调用；
//	② 失败也写入（把空串/回显/原文写进 langTranslations）⇒ 比不救更坏，脏行还会锁死硬闸；
//	③ 把已有译文的段再翻一遍（have 里的段没跳过）⇒ 覆盖掉正确译文，且计费翻倍。
//
// 五条腿（正反成对，缺一条这锁就空转）：
//
//	A 语境真进 user 尾：请求体里必须出现 segmentContextNote 的标记，且源文在最后；
//	B 救回缺失段：只送缺失段，返回结果含该段；
//	C 失败不覆盖：回显原文/空串一律不采纳，have 一字不动；
//	D 已有译文不重发：have 里的段不得出现在请求里（按调用次数与请求体双判）；
//	E 档位：env FILE_CTX_RETRY_MAX 三态（0＝关闭 / N / 非法回落默认）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/engine/ -run FileCtxRetry
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
)

// ctxRetryFake 假上游：按"源文→译文"映射回内容，并记录收到的 user 段。
type ctxRetryFake struct {
	mu      sync.Mutex
	replies map[string]string // 源文 → 译文（缺省按 "Translated: "+源文）
	users   []string          // 每次请求的 user 段（供"语境有没有真进去"判据用）
}

// handler 组装 OpenAI 形状的回包（单语种腿不带 <t> 包装，源文在 user 最后一行）。
func (f *ctxRetryFake) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	user := ""
	for _, m := range req.Messages {
		if m.Role != "system" {
			user = m.Content
		}
	}
	f.mu.Lock()
	f.users = append(f.users, user)
	f.mu.Unlock()

	lines := strings.Split(strings.TrimRight(user, "\n"), "\n")
	src := strings.TrimSpace(lines[len(lines)-1])
	out, ok := f.replies[src]
	if !ok {
		out = "Translated: " + src
	}
	w.Header().Set("Content-Type", "application/json")
	b, _ := json.Marshal(out)
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, string(b))
}

// snapshotUsers 取请求体快照（判据只读快照，避免与并发写竞争）。
func (f *ctxRetryFake) snapshotUsers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.users...)
}

// newCtxRetryEngine 装配假上游 + 引擎（必须走 NewEngine：单发腿会读构造期字段）。
func newCtxRetryEngine(t *testing.T, fake *ctxRetryFake) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(fake.handler))
	t.Cleanup(srv.Close)
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return NewEngine(cfg, nil, nil, nil)
}

// TestFileCtxRetrySendsContextAndRescues A+B 腿：语境真进 user 尾，缺失段被救回。
func TestFileCtxRetrySendsContextAndRescues(t *testing.T) {
	fake := &ctxRetryFake{replies: map[string]string{
		"第二行原文": "Second line",
	}}
	eng := newCtxRetryEngine(t, fake)

	texts := []string{"第一行原文", "第二行原文"}
	have := map[string]string{"第一行原文": "First line"}
	got := eng.retranslateMissingWithContext(context.Background(), "en", texts, have, "表头", 20)

	if len(got) != 1 || got["第二行原文"] != "Second line" {
		t.Fatalf("缺失段应被救回，实得 %v", got)
	}
	users := fake.snapshotUsers()
	if len(users) != 1 {
		t.Fatalf("只该送 1 段（缺失段），实得 %d 次请求：%v", len(users), users)
	}
	// A 腿：语境标记必须在 user 段里，且待译条目在最后一行（尾注在前，源文在后）。
	if !strings.Contains(users[0], "Reference context") {
		t.Fatalf("语境尾注没进请求（这一腿就退化成普通重译）：%q", users[0])
	}
	if !strings.HasSuffix(strings.TrimRight(users[0], "\n"), "第二行原文") {
		t.Fatalf("待译条目必须是 user 段最后一行：%q", users[0])
	}
}

// TestFileCtxRetryRejectsUnusable C 腿：失败/回显一律不采纳，已有译文不被覆盖。
func TestFileCtxRetryRejectsUnusable(t *testing.T) {
	cases := []struct {
		name string
		rep  string
	}{
		{"回显原文", "第二行原文"},
		{"空串", ""},
		{"失败占位", "[翻译失败]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &ctxRetryFake{replies: map[string]string{"第二行原文": c.rep}}
			eng := newCtxRetryEngine(t, fake)
			have := map[string]string{"第一行原文": "First line"}
			got := eng.retranslateMissingWithContext(context.Background(), "en",
				[]string{"第一行原文", "第二行原文"}, have, "", 20)
			if len(got) != 0 {
				t.Fatalf("不可用译文不得被采纳，实得 %v", got)
			}
			if have["第二行原文"] != "" || have["第一行原文"] != "First line" {
				t.Fatalf("失败不得改写调用方的已有译文：%v", have)
			}
		})
	}
}

// TestFileCtxRetrySkipsExisting D 腿：已有译文的段不重发（否则覆盖正确译文且计费翻倍）。
func TestFileCtxRetrySkipsExisting(t *testing.T) {
	fake := &ctxRetryFake{}
	eng := newCtxRetryEngine(t, fake)
	have := map[string]string{"第一行原文": "First line", "第二行原文": "Second line"}
	got := eng.retranslateMissingWithContext(context.Background(), "en",
		[]string{"第一行原文", "第二行原文"}, have, "", 20)
	if len(got) != 0 {
		t.Fatalf("没有缺失段时不该救回任何东西：%v", got)
	}
	if users := fake.snapshotUsers(); len(users) != 0 {
		t.Fatalf("没有缺失段时一次都不该拨上游，实得 %d 次", len(users))
	}
	// 上限 0 ⇒ 关闭（同一形态，另一条早退分支）。
	fake2 := &ctxRetryFake{}
	eng2 := newCtxRetryEngine(t, fake2)
	got2 := eng2.retranslateMissingWithContext(context.Background(), "en",
		[]string{"第二行原文"}, map[string]string{}, "", 0)
	if len(got2) != 0 || len(fake2.snapshotUsers()) != 0 {
		t.Fatalf("上限 0 必须整条关闭，实得 got=%v calls=%d", got2, len(fake2.snapshotUsers()))
	}
}

// TestFileCtxRetryMaxTiers E 腿：档位三态（0 是真实档位，非法值回落默认）。
func TestFileCtxRetryMaxTiers(t *testing.T) {
	eng := &Engine{}
	cases := []struct {
		env  string
		want int
	}{
		{"0", 0},
		{"5", 5},
		{"abc", 20}, // 非法 ⇒ 回落代码默认
		{"-3", 20},  // 负数不合法 ⇒ 回落默认（0 才是关闭）
		{"", 20},
	}
	for _, c := range cases {
		t.Run("env="+c.env, func(t *testing.T) {
			t.Setenv("FILE_CTX_RETRY_MAX", c.env)
			if got := eng.fileCtxRetryMax(); got != c.want {
				t.Errorf("fileCtxRetryMax()=%d, want %d（env=%q）", got, c.want, c.env)
			}
		})
	}
}

// TestBuildSegmentContext 语境只作参考：带表头与邻段、**不含本段自己**、逐段限长。
func TestBuildSegmentContext(t *testing.T) {
	texts := []string{"A0", "A1", "本段", "A3", "A4", "A5"}
	got := buildSegmentContext(texts, 2, "表头行")
	for _, want := range []string{"表头行", "A0", "A1", "A3", "A4"} {
		if !strings.Contains(got, want) {
			t.Errorf("语境缺 %q：%q", want, got)
		}
	}
	if strings.Contains(got, "本段") {
		t.Errorf("语境不得包含本段自己（模型会把它当成待译内容的一部分）：%q", got)
	}
	if strings.Contains(got, "A5") {
		t.Errorf("只取前后各两段，A5 不该进来：%q", got)
	}
	// 限长：超长邻段必须被截断，否则语境会盖过待译内容。
	long := strings.Repeat("很", 500)
	got = buildSegmentContext([]string{long}, 0, long)
	if len([]rune(got)) > 160+120+8 {
		t.Errorf("语境必须限长（表头 160 / 邻段 120），实得 %d 字符", len([]rune(got)))
	}
}
