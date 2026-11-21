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

// TestFileCtxRetryTriggersOnNumbers F 腿（★ 2026-11-21 用户令扩展）：
// 「数字不保持」也进候选集。现网 tv7 实证：`150–250 万` 被译成 `150–250 אלפי`（千，差 10 倍），
// 这一族缺的不是"翻不出"而是**这一格属于哪张表、上一行是什么量级**——正是语境能救的形态。
func TestFileCtxRetryTriggersOnNumbers(t *testing.T) {
	const src = "25–50 万"
	fake := &ctxRetryFake{replies: map[string]string{src: "250,000–500,000 yuan"}}
	eng := newCtxRetryEngine(t, fake)
	have := map[string]string{src: "25–50 \u05d0\u05dc\u05e4\u05d9"} // 「25–50 千」——量级错
	got := eng.retranslateMissingWithContext(context.Background(), "en", []string{src}, have, "| 项目 | 价格 |", 20)
	if got[src] != "250,000–500,000 yuan" {
		t.Fatalf("数字不过的段应被带语境重译救回，实得 %v", got)
	}
	users := fake.snapshotUsers()
	if len(users) != 1 {
		t.Fatalf("应只拨 1 次（这一条候选），实得 %d 次", len(users))
	}
	// 语境必须真进请求：这一族的价值全在语境上，退化成"普通重译"就白烧一次调用。
	if !strings.Contains(users[0], "Reference context") {
		t.Fatalf("数字触发腿也必须带语境：%q", users[0])
	}
}

// TestFileCtxRetryTriggersOnScript G 腿：目标语脚本不纯也进候选集
// （tv7 实证：两行标题被译成泰文，混在希伯来语交付里）。
func TestFileCtxRetryTriggersOnScript(t *testing.T) {
	const src = "产品说明书"
	fake := &ctxRetryFake{replies: map[string]string{src: "\u05de\u05d3\u05e8\u05d9\u05da \u05d4\u05de\u05d5\u05e6\u05e8"}} // מדריך המוצר
	eng := newCtxRetryEngine(t, fake)
	have := map[string]string{src: "\uc2e0\ud638 \uc124\uba85"} // 신호 설명（韩文混进 he 交付）
	got := eng.retranslateMissingWithContext(context.Background(), "he", []string{src}, have, "", 20)
	if got[src] == "" {
		t.Fatalf("脚本不纯的段应被带语境重译救回，实得 %v", got)
	}
	if n := len(fake.snapshotUsers()); n != 1 {
		t.Fatalf("应只拨 1 次，实得 %d 次", n)
	}
}

// TestFileCtxRetryKeepsOldWhenStillBad H 腿（替换侧的反向对照）：
// 已有译文时，"重译结果仍过不了那条判据"**不许**替换——把一个错的换成另一个错的是净亏，
// 而屏幕上只会多一行"救回 N 段"的成功日志。
func TestFileCtxRetryKeepsOldWhenStillBad(t *testing.T) {
	const src = "25–50 万"
	fake := &ctxRetryFake{replies: map[string]string{src: "250–500 \u05d0\u05dc\u05e4\u05d9"}} // 仍然丢量级（千）
	eng := newCtxRetryEngine(t, fake)
	old := "25–50 \u05d0\u05dc\u05e4\u05d9"
	have := map[string]string{src: old}
	got := eng.retranslateMissingWithContext(context.Background(), "en", []string{src}, have, "", 20)
	if len(got) != 0 {
		t.Fatalf("重译仍不过数字判据时不得采纳，实得 %v", got)
	}
	if have[src] != old {
		t.Fatalf("不得改写调用方的已有译文，实得 %q", have[src])
	}
}

// TestFileCtxRetryCandidatesOrderAndLimit J 腿：候选集按源文序、去重，上限截断只截尾部。
func TestFileCtxRetryCandidatesOrderAndLimit(t *testing.T) {
	texts := []string{"A 10 万", "B 正常句", "C 20 万", "A 10 万"}
	have := map[string]string{
		"A 10 万": "A 10 thousand", // 数字不过
		"B 正常句":  "B normal",      // 健康：不进候选
	}
	got := ctxRetryTargets("en", texts, have)
	if len(got) != 2 {
		t.Fatalf("候选应为 2 段（A 与 C，B 健康且 A 去重），实得 %d：%+v", len(got), got)
	}
	if got[0].text != "A 10 万" || got[1].text != "C 20 万" {
		t.Fatalf("候选必须按源文序且去重，实得 %+v", got)
	}
	if got[0].old == "" || got[1].old != "" {
		t.Fatalf("已译段的 old 必须非空、缺失段的 old 必须为空：%+v", got)
	}
	// 健康段一个都不许重发（与 D 腿同判据，这里是候选集层面的对照）。
	for _, tg := range got {
		if tg.text == "B 正常句" {
			t.Fatal("健康段混进了候选集")
		}
	}
}

// TestCtxRetryNotGatedByMissingSegments 形态锁（**守卫级**，不是判据级）。
//
// 为什么必须有这一条：候选集自 2026-11-21 起包含「译了但形态不过」的段（数字/脚本），
// 而这类单子往往**一个缺失段都没有**。只要这条腿被任何"按 missing0 分支"的 if 包住，
// 它就在现网静默不生效——而且症状是"日志里一条都没有"，与"这一单不需要救"完全同形。
// 本批就是这么踩的：ctx2 小件交付后 3 条数字不过，journal 里 `[file-ctx-retry]` 计数 0，
// 根因是语言循环开头那句 `if len(missing0) == 0 { continue }` 把整段（含这条腿）跳过了。
//
// 两条判据（缺一条都会让这条腿重新变回死码）：
//
//	① 源码里不许再出现 `if len(missing0) == 0` 这种前置 continue 形态；
//	② `ctxRetryTargets(` 的调用点不许落在 `if len(missing0) > 0 { … }` 的块内
//	   （把前置 continue 改成包一层 if，效果一模一样）。
func TestCtxRetryNotGatedByMissingSegments(t *testing.T) {
	src := readEngineSource(t, "file.go")
	if strings.Contains(src, "if len(missing0) == 0") {
		t.Fatal("`if len(missing0) == 0 { continue }` 会让上下文重译腿在零缺失的单子上整段跳过（本批真实踩过）")
	}
	// ★ 必须按**HandleFile 里那一处调用**取（带 `langTranslations[lc]`）。本批连踩两次空转：
	//   ① 先用 `ctxRetryTargets(lc, texts` ⇒ 命中的是函数**定义**（在 HandleFile 之外）；
	//   ② 再缩成 `ctxRetryTargets(lc, texts,` ⇒ 命中的是另一处**内部转发**（retranslateMissingWithContext 的
	//      `... ctxRetryTargets(lc, texts, have)`）。两处都在 HandleFile 之前，于是"腿被包进 if"恒判绿。
	//   教训：形态锁的锚点必须能唯一定位到被测的那一次调用，否则反证永远不红。
	call := strings.Index(src, "ctxRetryTargets(lc, texts, langTranslations[lc])")
	if call < 0 {
		t.Fatal("找不到上下文重译的候选集调用点（判据已失明，先核函数名）")
	}
	head := "if len(missing0) > 0 {"
	h := strings.Index(src, head)
	if h < 0 {
		t.Log("没有 `if len(missing0) > 0` 包裹块（形态变化），只跑第①条")
		return
	}
	// 从该 if 的左花括号开始做花括号配对（跳过行注释与双引号字符串）。
	open := h + strings.Index(src[h:], "{")
	end := matchBrace(src, open)
	if end < 0 {
		t.Fatal("花括号配对失败（源码形态变了，先修这条锁的扫描器）")
	}
	if call > open && call < end {
		t.Fatal("上下文重译的候选集调用被 `if len(missing0) > 0` 包住了 ⇒ 零缺失的单子上这条腿不跑（屏幕上只有'一次都没触发'）")
	}
}

// matchBrace 返回 src[open] 这对花括号的闭合下标（open 必须指向 '{'）。
// 只跳过行注释与双引号字符串——够用于本仓 file.go 的形态；匹配失败返回 -1。
func matchBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			if nl := strings.IndexByte(src[i:], '\n'); nl >= 0 {
				i += nl
				continue
			}
			return -1
		case src[i] == '"':
			i++
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' {
					i++
				}
				i++
			}
		case src[i] == '{':
			depth++
		case src[i] == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// TestFileCtxRetryHintNamesTheDefect 提示词侧：只给语境不点名，模型往往照旧犯同一个错
// （现网实测 `65–120 万`→`65–120 מיליון`，同一列的正确写法就在语境里也没救回来）。
// 因此候选段的 reason 必须变成提示词里的一句硬要求；而"缺失段"不带这句
// ——多一句不该有的指令会把纯翻译模型带偏，也让这条腿与改造前的提示词不再逐字一致。
func TestFileCtxRetryHintNamesTheDefect(t *testing.T) {
	// ① 数字保持：必须点名"保留每一个数字与量级"
	const numSrc = "65–120 万"
	fake := &ctxRetryFake{replies: map[string]string{numSrc: "650,000–1,200,000"}}
	eng := newCtxRetryEngine(t, fake)
	have := map[string]string{numSrc: "65–120 \u05de\u05d9\u05dc\u05d9\u05d5\u05df"} // 65–120 מיליון（差 10 倍）
	_ = eng.retranslateMissingWithContext(context.Background(), "he", []string{numSrc}, have, "| 金额 |", 20)
	users := fake.snapshotUsers()
	if len(users) != 1 {
		t.Fatalf("应拨 1 次，实得 %d", len(users))
	}
	if !strings.Contains(users[0], "Hard requirement") || !strings.Contains(users[0], "magnitude") {
		t.Fatalf("数字不过的重译必须在提示词里点名「保留数字与量级」：%q", users[0])
	}

	// ② 缺失段：不许带这句（与改造前逐字一致）
	const missSrc = "第二行原文"
	fake2 := &ctxRetryFake{replies: map[string]string{missSrc: "Second line"}}
	eng2 := newCtxRetryEngine(t, fake2)
	_ = eng2.retranslateMissingWithContext(context.Background(), "en", []string{missSrc}, map[string]string{}, "", 20)
	u2 := fake2.snapshotUsers()
	if len(u2) != 1 {
		t.Fatalf("应拨 1 次，实得 %d", len(u2))
	}
	if strings.Contains(u2[0], "Hard requirement") {
		t.Fatalf("缺失段不该带「形态」硬要求（提示词必须与改造前逐字一致）：%q", u2[0])
	}

	// ③ 档位表：只认两条，其余一律空串（防止下一个人以为还有别的触发条件）
	for _, c := range []struct{ in, want string }{
		// 提示词是**英文**（中文指令会被纯翻译模型当成待译内容一起翻），所以这里按英文锚判。
		{"数字保持", "number"}, {"目标语言脚本一致", "script"}, {"缺失", ""}, {"", ""}, {"随便", ""},
	} {
		got := segmentHintNote(c.in)
		if c.want == "" && got != "" {
			t.Fatalf("segmentHintNote(%q) 应返回空串，实得 %q", c.in, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) {
			t.Fatalf("segmentHintNote(%q) 应含 %q，实得 %q", c.in, c.want, got)
		}
	}
}
