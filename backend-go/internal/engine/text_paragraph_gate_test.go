// ============ text_paragraph_gate_test.go 职责说明 ============
// DEF-P1-05 B2（2026-11-01，D-1 拍板"全部要做流式"）即时翻译**段落能力**的门禁 B2-3.1。
//
// 假上游按**两种传输形态**都答（stream:true 走 SSE 分帧、否则走 JSON）：
//
//	只回 JSON 的话，流式腿会"失败→透明回退非流式"，于是 delta 一帧都没有，
//	"delta===done"那几条判据会因为**没有 delta** 而恒真——那正是本仓反复踩的空转锁。
//	所以本文件先钉"流式腿真的被走到"（射程自证），再钉逐字等值。
//
// 用例对应施工方案那张表：1 三段对三段／2 单段不折腾／3 多空行只算两段／
// 4 结构等值（两档）／5 fast 逐字等值／6 pro 逐字等值＋初翻必须静默＋审校期间必须有进度帧／
// 7 审校坏产物不覆盖初翻／8 长文两级切分分隔符都在／12 可用性尺子不缩水。
// 9（词表只判一次）与 10（记忆键）是**静态/结构性**判据，见本文件末尾两条。
// =============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
)

// paraSrc 三段源文（段间空行；每段含唯一子串，假上游据此认段）。
const paraSrc = "第一段：甲乙丙丁。\n\n第二段：戊己庚辛。\n\n第三段：壬癸子丑。"

// paraInitials／paraRevised 假上游的两套回答（初翻与审校各一版，**用词刻意不同**：
// 现网 DEF-P1-05 的病症就是"用户读到一版、成品是另一版"，只有两版不同才测得出静默覆盖）。
var paraInitials = map[string]string{
	"第一段：甲乙丙丁。": "First paragraph alpha beta.",
	"第二段：戊己庚辛。": "Second paragraph gamma delta.",
	"第三段：壬癸子丑。": "Third paragraph epsilon zeta.",
}

// paraUnknownReply 认不出源文时的通用可用回答（不触发修复腿）。
const paraUnknownReply = "Generic translated sentence for the given source text."

var paraRevised = map[string]string{
	"第一段：甲乙丙丁。": "First paragraph, revised alpha.",
	"第二段：戊己庚辛。": "Second paragraph, revised gamma.",
	"第三段：壬癸子丑。": "Third paragraph, revised epsilon.",
}

// paraFake 假上游：按提示词认出"这是第几段的初翻还是审校"，回对应版本。
type paraFake struct {
	mu         sync.Mutex
	calls      []string     // 每次调用的档位标记（initial1/review2…），供调用次数与顺序判据
	failReview map[int]bool // 指定段号的审校回空（模拟"坏产物不许覆盖初翻"）
	badReview  map[int]bool // 指定段号的审校回**退化串 "and"**（用例 13 的靶子：过了长度/纯度、没过可用性）
	badInitial map[int]bool // 指定段号的初翻回退化串 "and"（用例 12 的靶子）
	stream     bool
}

func (f *paraFake) record(tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, tag)
}

// newParaHarness 起假上游并构造引擎。参数 stream=true ⇒ 支持 SSE（流式腿真的能走通）。
func newParaHarness(t *testing.T, stream bool) (*Engine, *paraFake) {
	t.Helper()
	fake := &paraFake{failReview: map[int]bool{}, badReview: map[int]bool{}, badInitial: map[int]bool{}, stream: stream}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream   bool `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		user := ""
		for _, m := range req.Messages {
			if m.Role == "user" {
				user = m.Content
			}
		}
		// ★ 并发纪律（-race 会抓）：HTTP handler 跑在独立 goroutine，测试主线程在用例里写
		//   failReview/badInitial ⇒ 读侧必须在锁内取快照（map 的并发读写是 runtime fatal error，
		//   recover 兜不住；这是 AGENTS §三「并发回写共享 map 必须先快照」的同族）。
		fake.mu.Lock()
		failReview := map[int]bool{}
		badReview := map[int]bool{}
		badInitial := map[int]bool{}
		for k, v := range fake.failReview {
			failReview[k] = v
		}
		for k, v := range fake.badReview {
			badReview[k] = v
		}
		for k, v := range fake.badInitial {
			badInitial[k] = v
		}
		isStream := fake.stream
		fake.mu.Unlock()
		// 认段：源文唯一子串出现在提示词里就是那一段
		idx, srcPara := 0, ""
		for i, para := range SplitParagraphs(paraSrc) {
			if strings.Contains(user, para) {
				idx, srcPara = i+1, para
				break
			}
		}
		isReview := strings.Contains(user, "资深翻译审校") || strings.Contains(user, "【待审校译文】")
		// 认不出段时的默认回答：一条**可用**的普通译文。
		// ⚠️ 不能图省事回 "and"（那是现网退化形态）：它会额外触发极简/fallback 修复腿，
		//   于是"调用次数"测的就不是分段与否，而是修复链——本文件用例 2 判的就是调用次数。
		//   要测退化形态的用例自己点名（badInitial 那一档）。
		reply := paraUnknownReply
		switch {
		case isReview && srcPara != "":
			switch {
			case failReview[idx]:
				reply = "" // 该段审校没给出内容 ⇒ 必须保留初翻
			case badReview[idx]:
				// 该段审校给出**内容合法但退化**的一串：它能过 finish_reason 与目标语纯度两道内部判据
				// （全 ASCII），但过不了可用性尺子（IsTranslationUsable 的 "and" 那一类）。
				// 用例 13 要的就是这一档——只有这种"内部判据放行、可用性判据拒绝"的形态
				// 才能证明采纳侧那道尺子真的在干活（回空那一档证不了：那是 ReviewTranslation 自己拦的）。
				reply = "and"
			default:
				reply = paraRevised[srcPara]
			}
			fake.record(fmt.Sprintf("review%d", idx))
		case srcPara != "":
			reply = paraInitials[srcPara]
			if badInitial[idx] {
				reply = "and" // 该段初翻退化成现网那种"and"串（用例 12 的靶子）
			}
			fake.record(fmt.Sprintf("initial%d", idx))
		}
		if isReview && srcPara == "" { // 整篇审校（回落路径）：把三段初翻拼一起回一版
			reply = strings.Join([]string{paraRevised["第一段：甲乙丙丁。"], paraRevised["第二段：戊己庚辛。"], paraRevised["第三段：壬癸子丑。"]}, "\n\n")
			fake.record("reviewWhole")
		}
		if srcPara == "" && !isReview {
			fake.record("initialOther")
		}
		if isStream && req.Stream && reply != "" {
			w.Header().Set("Content-Type", "text/event-stream")
			// 分两帧吐（第二帧刻意切在词中），并带 usage 尾包——与 llm.StreamChat 的解析口径同形
			half := len(reply) / 2
			fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, reply[:half]))
			fmt.Fprintf(w, "data: %s\n\n", fmt.Sprintf(`{"choices":[{"delta":{"content":%q}}]}`, reply[half:]))
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`, reply)
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return NewEngine(cfg, nil, nil, nil), fake
}

// paraOptions 单语种 en（多语种并发会把 delta 交错，逐字等值就没法判了）。
func paraOptions(mode string) map[string]interface{} {
	return map[string]interface{}{"target_langs": []interface{}{"en"}, "mode": mode, "lang": "zh"}
}

// runPara 跑一次 HandleText，返回 (done 译文, delta 拼接, 进度帧序列, 整份结果)。
// 四个返回值都要：逐字等值吃前两个，进度帧吃第三个，gateWarnings 吃第四个。
func runPara(t *testing.T, e *Engine, src, mode string, plan *ChatSegPlan) (string, string, []string, *TextTranslateResult) {
	t.Helper()
	ctx := context.Background()
	if plan != nil && plan.Use && len(plan.Paragraphs) > 1 {
		ctx = WithChatSegPlan(ctx, *plan)
	}
	var mu sync.Mutex
	var deltas, progs []string
	ctx = WithStreamSink(ctx, func(lang, d string) {
		mu.Lock()
		deltas = append(deltas, d)
		mu.Unlock()
	})
	res := e.HandleText(ctx, src, paraOptions(mode), func(step string, done, total int) {
		mu.Lock()
		progs = append(progs, step)
		mu.Unlock()
	})
	if res == nil {
		t.Fatal("引擎没返回结果")
	}
	mu.Lock()
	defer mu.Unlock()
	return res.Data.Translations["en"], strings.Join(deltas, ""), progs, res
}

func planFor(src string) *ChatSegPlan {
	return &ChatSegPlan{Use: true, Paragraphs: SplitParagraphs(src), Reason: "paragraph"}
}

// 射程自证：流式腿必须真的被走到（否则下面所有 delta 判据都因为"没有 delta"而恒真）。
func TestParaHarnessActuallyStreams(t *testing.T) {
	e, fake := newParaHarness(t, true)
	_, deltas, _, _ := runPara(t, e, paraSrc, "fast", planFor(paraSrc))
	if strings.TrimSpace(deltas) == "" {
		t.Fatalf("一条 delta 都没有 ⇒ 流式腿没走通，本文件其余判据全部失去射程（调用记录=%v）", fake.calls)
	}
}

// 用例 1＋4：三段 → 译文必须还是三段（fast/pro 两档都钉），且第 i 段对第 i 源段。
func TestParaStructureKeepsParagraphCount(t *testing.T) {
	for _, mode := range []string{"fast", "pro"} {
		e, _ := newParaHarness(t, true)
		got, deltas, _, _ := runPara(t, e, paraSrc, mode, planFor(paraSrc))
		if got == "" {
			t.Fatalf("%s 档译文为空", mode)
		}
		if n := NonEmptyParagraphs(got); n != 3 {
			t.Fatalf("%s 档：源文 3 段，译文只有 %d 段（分隔符没补回来）：\n%q", mode, n, got)
		}
		ps := SplitParagraphs(got)
		want0 := paraInitials["第一段：甲乙丙丁。"]
		if mode == "pro" {
			want0 = paraRevised["第一段：甲乙丙丁。"]
		}
		if ps[0] != want0 {
			t.Fatalf("%s 档第 1 段内容不对：%q（期望 %q；全部=%q）", mode, ps[0], want0, got)
		}
		if n := NonEmptyParagraphs(deltas); n != 3 {
			t.Fatalf("%s 档：delta 的段数是 %d，不等于源文 3 段（结构等值破了）：\n%q", mode, n, deltas)
		}
	}
}

// 用例 5＋6：★ 逐字等值（两档都承诺，D-1 的落点）。
// pro 档另钉两条：① delta **不含初翻串**（初翻被静默）；② 审校期间必须有进度帧（不许整段静默）。
func TestParaDeltaEqualsDoneBothModes(t *testing.T) {
	e, fake := newParaHarness(t, true)
	_ = fake
	// ★ 2026-11-02 收口：这一腿此前喂的是 `&ChatSegPlan{}`（Use=false）⇒ 走的是**整篇旧路径**，
	//   于是"fast 档逐字等值"这条判据结构性测不到分段那一档（用例 1/4/8 都用 planFor，只有这里没有）。
	//   方案 B2-3.3 承诺的是"两档都钉逐字等值"，所以必须挂上分段计划才叫有落点。
	gotFast, deltasFast, _, _ := runPara(t, e, paraSrc, "fast", planFor(paraSrc))
	if NonEmptyParagraphs(gotFast) != 3 {
		t.Fatalf("fast 档挂分段计划后译文不是三段（这条腿其实没走分段）：%q", gotFast)
	}
	if strings.TrimSpace(deltasFast) != strings.TrimSpace(gotFast) {
		t.Fatalf("fast 档 delta 与 done 不是同一份：\ndelta=%q\ndone=%q", deltasFast, gotFast)
	}

	e2, fake2 := newParaHarness(t, true)
	gotPro, deltasPro, progs, _ := runPara(t, e2, paraSrc, "pro", planFor(paraSrc))
	if strings.TrimSpace(deltasPro) != strings.TrimSpace(gotPro) {
		t.Fatalf("pro 档 delta 与 done 不是同一份（本批的核心承诺）：\ndelta=%q\ndone=%q", deltasPro, gotPro)
	}
	for _, initial := range paraInitials {
		if strings.Contains(deltasPro, initial) {
			t.Fatalf("pro 档 delta 里出现了初翻串 ⇒ 初翻没被静默，用户先读一版再被换成另一版（DEF-P1-05 原形态复发）：%q", deltasPro)
		}
	}
	if strings.Contains(gotPro, paraInitials["第一段：甲乙丙丁。"]) {
		t.Fatalf("pro 档成品里还留着初翻：%q", gotPro)
	}
	sawReviewProgress := false
	for _, p := range progs {
		if strings.Contains(p, "校对中") && strings.Contains(p, "/3") {
			sawReviewProgress = true
		}
	}
	if !sawReviewProgress {
		t.Fatalf("逐段审校期间没有「校对中·第 i/N 段」进度帧（客户对着静默屏幕等）：%v", progs)
	}
	// 审校必须逐段调（3 次 review、3 次 initial），不许退回整篇那一枪
	kinds := map[string]int{}
	for _, c := range fake2.calls {
		kinds[c]++
	}
	if kinds["review1"]+kinds["review2"]+kinds["review3"] != 3 {
		t.Fatalf("审校不是逐段三次（%v）⇒「逐段 + 流式」退回了整篇审校", kinds)
	}
	if kinds["reviewWhole"] != 0 {
		t.Fatalf("分段执行却打出了整篇审校那一枪（说明走的是回落路径）：%v", kinds)
	}
}

// 用例 2：单段不折腾——段数 <2 一律走整篇，调用次数与旧路径等值（防"给单段多花一次钱"）。
func TestParaSingleParagraphNotDisturbed(t *testing.T) {
	src := "只有一段的话，不该被拆成多次调用。"
	e, fake := newParaHarness(t, true)
	_, _, _, _ = runPara(t, e, src, "fast", &ChatSegPlan{})
	n := fake.callCount("initial")
	// 短到不触发句级切分 ⇒ 初翻一枪；多一枪就是分段把单段也拆了
	if n != 1 {
		t.Fatalf("单段源文的初翻调用次数=%d，期望 1（调用记录=%v）", n, fake.calls)
	}
	if NonEmptyParagraphs(src) >= ChatSegmentMinParagraphs {
		t.Fatal("夹具源文被切成了多段，本用例失去意义")
	}
	// 正向对照：同样这条源文挂上"分段计划"也必须按整篇走（Use 由段数门决定，不由标记决定）
	e2, fake2 := newParaHarness(t, true)
	_, _, _, _ = runPara(t, e2, src, "fast", &ChatSegPlan{Use: true, Paragraphs: SplitParagraphs(src)})
	if fake2.callCount("initial") != 1 {
		t.Fatalf("被标记骗成多打一次：%v", fake2.calls)
	}
}

// 用例 3：连续多个空行只算两个段界（空行不独立成段）。
func TestParaConsecutiveBlankLinesCountOnce(t *testing.T) {
	src := "甲乙丙丁。\n\n\n\n\n戊己庚辛。"
	if n := NonEmptyParagraphs(src); n != 2 {
		t.Fatalf("多空行应当只算一个段界，实得 %d 段：%q", n, src)
	}
	if !ParagraphRoundTripStable(src) {
		t.Fatal("切分/拼回不互逆（段数在往返中变了）")
	}
}

// 用例 7：某段审校给空 ⇒ 该段**保留初翻**、段数不变、其余各段照常采纳。
func TestParaBadReviewKeepsInitial(t *testing.T) {
	e, fake := newParaHarness(t, true)
	fake.mu.Lock()
	fake.failReview[2] = true
	fake.mu.Unlock()
	got, deltas, _, _ := runPara(t, e, paraSrc, "pro", planFor(paraSrc))
	if NonEmptyParagraphs(got) != 3 {
		t.Fatalf("段数被审校坏产物打乱：%q", got)
	}
	if !strings.Contains(got, paraInitials["第二段：戊己庚辛。"]) {
		t.Fatalf("第二段没保留初翻（审校回空却覆盖了初翻）：%q", got)
	}
	if !strings.Contains(got, paraRevised["第一段：甲乙丙丁。"]) || !strings.Contains(got, paraRevised["第三段：壬癸子丑。"]) {
		t.Fatalf("其它段没被采纳（一坏俱坏的形态复发）：%q", got)
	}
	// 分叉窗口按**形态**分两档，本例是第一档（不产生分叉）：
	//   · 审校回空 ⇒ 那一段一个字都没吐出去 ⇒ delta 与 done 仍然同一份（就是本例断言的这档）；
	//   · 审校吐出内容后被 purity/截断判据丢弃 ⇒ 字已经收不回 ⇒ 这一档 delta≠done（方向＝客户看到过
	//     一版被弃用的稿），见 engine.go ReviewTranslation 上面那段注释与 §B2-3 的分叉表说明。
	//   ⚠️ 不许把这条写成"delta 永远等于 done"——那是会恒红的锁；也不许写成"永不分叉"——那是假话。
	if strings.Contains(deltas, paraRevised["第二段：戊己庚辛。"]) {
		t.Fatalf("被拒的审校稿不该出现在 delta 里（本例是回空档）：%q", deltas)
	}
}

// 用例 13：★ 审校产物也必须过**同一把**可用性判据（AGENTS §19 族四），并把第二种分叉形态钉成方向断言。
//
// 靶子与用例 7 的区别是本案的全部意义：
//
//	· 用例 7 喂的是"审校回空"——那一档 ReviewTranslation 内部就拦了，采纳侧那道尺子**根本没被走到**，
//	  所以它对"采纳前有没有再过一遍可用性判据"完全无感（摘掉那条判据照样全绿＝空锁）；
//	· 本案喂"审校回退化串 and"：全 ASCII ⇒ 过 finish_reason 与纯度两道内部判据，
//	  但过不了 IsTranslationUsable ⇒ 只有采纳侧那把尺子能拦住它。
//
// 两件事一起断（一条判成品、一条判分叉方向，**不许**混成"delta 永远等于 done"那种恒红的锁）：
//  1. 成品里第二段仍是初翻，"and" 一个字都不许进交付（否则 P0-01 从审校这一侧复发）；
//  2. delta 里**有** "and"——字已经吐出去收不回，这是设计内的分叉窗口（方向＝客户看到过一版被弃用的稿，
//     比"成品停在坏稿上"轻）。把它钉成断言的理由：不钉的话，下一位为了"逐字等值"把静默窗口拉长到审校之后，
//     就回到 DEF-P1-05 的原形态（用户读完一版、成品是另一版）。
func TestParaReviewOutputPassesUsabilityRuler(t *testing.T) {
	e, fake := newParaHarness(t, true)
	fake.mu.Lock()
	fake.badReview[2] = true
	fake.mu.Unlock()
	got, deltas, _, _ := runPara(t, e, paraSrc, "pro", planFor(paraSrc))

	if NonEmptyParagraphs(got) != 3 {
		t.Fatalf("成品段数被审校退化串打乱：%q", got)
	}
	if strings.Contains(got, "and") {
		t.Fatalf("审校的退化串进了成品（采纳侧没可用性判据）：%q", got)
	}
	if !strings.Contains(got, paraInitials["第二段：戊己庚辛。"]) {
		t.Fatalf("第二段没保留初翻，而是被别的东西覆盖：%q", got)
	}
	// 另外两段仍应拿到审校稿（一坏俱坏＝把逐段采纳写成了整批退回）
	if !strings.Contains(got, paraRevised["第一段：甲乙丙丁。"]) || !strings.Contains(got, paraRevised["第三段：壬癸子丑。"]) {
		t.Fatalf("其它段被连坐退回初翻：%q", got)
	}
	// 分叉方向断言：这一档 delta≠done，且差的就是那串被弃用的稿
	if strings.Contains(deltas, "and") == false {
		t.Fatalf("分叉表失效：退化审校稿按设计**已经流出去**（本例应当 delta≠done），实得 delta=%q", deltas)
	}
	if strings.TrimSpace(deltas) == strings.TrimSpace(got) {
		t.Fatalf("分叉表失效：本例应当 delta≠done（字收不回），两串却逐字相同：\ndelta=%q\ndone=%q", deltas, got)
	}
}

// 用例 8：长文（>1200 rune 触发句级切分）＋三段 ⇒ 两级分隔符都在，段数仍为 3。
func TestParaLongTextKeepsBothSeparators(t *testing.T) {
	long := paraSrc
	for len([]rune(long)) < chatSplitTriggerRunes+50 {
		long += "\n\n" + paraSrc
	}
	e, _ := newParaHarness(t, true)
	segs := SplitParagraphs(long)
	got, deltas, _, _ := runPara(t, e, long, "fast", &ChatSegPlan{Use: true, Paragraphs: segs})
	if NonEmptyParagraphs(got) != len(segs) {
		t.Fatalf("长文段数不等：源 %d 段，译文 %d 段", len(segs), NonEmptyParagraphs(got))
	}
	if NonEmptyParagraphs(deltas) != len(segs) {
		t.Fatalf("delta 段数与源不等（句级腿的分隔符没补回来）：%q", clipRunes(deltas, 200))
	}
}

// 用例 12：可用性尺子不缩水——分段之后每段仍过**同一把**判据（AGENTS §19 族一/§22 那条唯一尺子）。
// 靶子＝第三段初翻退化成现网的 "and" 串。可接受的结局只有两种：
//
//	① 修复腿（极简/fallback）把它救回成正常译文；② 救不回来就**点名**（gateWarnings 非空）并
//	按设计处理——**不许**第三静默把 "and" 当成品交付（那正是 P0-01 修过的形态，只是被分段绕过去）。
func TestParaUsabilityRulerStillApplies(t *testing.T) {
	e, fake := newParaHarness(t, false)
	fake.mu.Lock()
	fake.badInitial[3] = true
	fake.mu.Unlock()
	got, _, _, res := runPara(t, e, paraSrc, "fast", planFor(paraSrc))
	ps := SplitParagraphs(got)
	for i, p := range ps {
		if strings.TrimSpace(p) == "and" {
			t.Fatalf("第 %d 段把退化串当成品交付了（分段的可用性判据没接上）：%q", i+1, got)
		}
	}
	if len(ps) != 3 && len(res.Data.GateWarnings) == 0 {
		t.Fatalf("少了一段却没有一句点名（静默丢内容＝比漏译更坏）：段数=%d 译文=%q", len(ps), got)
	}
	if NonEmptyParagraphs(got) == 3 && len(fake.callCountPrefix("initial")) < 4 {
		t.Fatalf("三段齐但修复腿一次都没打 ⇒ 退化串是怎么变好的？calls=%v", fake.calls)
	}
}

// 用例 9（结构性判据）：词表与品牌判定**只判一次**，必须留在任何循环之外。
// 判法＝按源码位置：glossaryProfile / fetchBrandTerms 的行号必须早于逐段循环所在函数的第一行；
// 出现"在 chatInitialByParagraph 里再调一次"的形态当场红（那会把每单一次的预算按段吃掉）。
func TestParaGlossaryAndBrandStayOutsideLoop(t *testing.T) {
	src := readEngineSource(t, "text.go")
	body := contractFilterFnBody(t, src, "func (e *Engine) handleTextCore(")
	iGloss := strings.Index(body, "e.glossaryProfile(")
	iBrand := strings.Index(body, "e.fetchBrandTerms(")
	iCall := strings.Index(body, "e.chatInitialByParagraph(")
	if iGloss < 0 || iBrand < 0 || iCall < 0 {
		t.Fatalf("锚点漂了：glossary=%d brand=%d segCall=%d", iGloss, iBrand, iCall)
	}
	if iGloss > iCall || iBrand > iCall {
		t.Fatal("词表/品牌判定被移到分段调用之后——判定次数会按段翻倍（AGENTS §23 同形教训）")
	}
	seg := readEngineSource(t, "chat_paragraphs.go")
	if strings.Contains(seg, "e.glossaryProfile(") || strings.Contains(seg, "e.fetchBrandTerms(") {
		t.Fatal("逐段执行体里出现了词表/品牌判定 ⇒ 同一份样本被按段重复判")
	}
}

// 用例 10（结构性判据）：分段执行**不需要**新造记忆键维度——
// TM 的键按"那一段的源文"算，分段与整篇天然是不同的键；本锁钉的是"不许再造第二把键"。
func TestParaNoSecondTMKey(t *testing.T) {
	seg := readEngineSource(t, "chat_paragraphs.go")
	for _, banned := range []string{"tmCacheKey", "tmContextHash", "TranslationMemoryKey"} {
		if strings.Contains(seg, banned) {
			t.Fatalf("分段执行体里自己算了 %s ⇒ 记忆键长出第二把尺子（键只在 batch_options/tm_cache 那两处）", banned)
		}
	}
}

// 拼回分隔符的等值锁：即时按段与工单按行必须是**同一对**切/拼实现（薄委托，不是抄一份）。
func TestParagraphAndLineComposeAreSameImplementation(t *testing.T) {
	orch := readOrchestratorSourceForParaGate(t)
	for _, fn := range []string{"splitLines", "nonEmptyLines", "composeLineOutput"} {
		if !strings.Contains(orch, "engine.") || !strings.Contains(orch, fn) {
			t.Fatalf("orchestrator 的 %s 不再是薄委托（两份实现迟早分家）", fn)
		}
	}
	for _, want := range []string{"engine.SplitLines(", "engine.NonEmptyLines(", "engine.ComposeLineOutput("} {
		if !strings.Contains(orch, want) {
			t.Fatalf("缺少薄委托 %s ⇒ 行切分又回长成第二份实现", want)
		}
	}
}

// callCountPrefix 数某档位前缀的调用次数（区别于 callCount：本文件两种档位都要数）。
func (f *paraFake) callCountPrefix(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// callCount 数某档位前缀的调用次数（"initial"/"review"）。
func (f *paraFake) callCount(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// readOrchestratorSourceForParaGate 读工单侧那个文件（跨包静态腿）。
// 测试的 cwd 是本包目录，所以路径是 ../orchestrator/…；读不到即判红——
// "读不到就当过"是这类跨包锁最常见的空转形态。
func readOrchestratorSourceForParaGate(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "orchestrator", "text_lines.go"))
	if err != nil {
		t.Fatalf("读取工单侧源文件失败（跨包等值锁失去射程）: %v", err)
	}
	return string(b)
}
