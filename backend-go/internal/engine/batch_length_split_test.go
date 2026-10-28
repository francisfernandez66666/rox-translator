// ============ 本文件职责中文说明 ============
// `finish_reason=length` 二分重试的闸门锁 —— ★ 2026-10-28 计价与成本优化批（方案 §5.3）。
//
// 为什么这一族必须有机械锁：截断重试的失效**在界面上看不出来**，而且两条失效方向都很贵——
//
//	该二分不二分 ⇒ 一整块（几十段）被 max_tokens 砍断，块内没吐到的段全部回落
//	              「上游没吐」那一档，可救的行白等、客户按原文收货；
//	不该分却分   ⇒ finish_reason=stop 的正常块也被拆开重拨，成本反向上涨（这是成本批的初衷反面）。
//
// 六条腿覆盖：二分救回（L1）/ 叶子仍截断只记那一槽（L2）/ stop 绝不二分（L3）/
// 叶子预算抬档与钳位（L4）/ 递归深度上限（L5）/ 下标映射（L6）。
// M0008（`finish=="length"` 判据本身）由 scripts/gate_mutants.tsv 做变异登记。
//
// 假上游按**协议**应答（读 `<sN>` 段标记、写 `finish_reason`），不按"我以为的调用次数"应答：
// 段计数与预算都从请求体现读，否则桩会替产品发合格证（AGENTS §三 那条「假上游按我以为的契约实现」）。
// 方言口径（AGENTS §一·4）：固定内存 SQLite，显式钉 config.C.DatabaseDriver。
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/tenant"
)

// ============ 假上游（按协议应答，记录每次请求的段清单与预算） ============

// lenCall 一次请求的读数：本次带了哪几段源文、要了多少 max_tokens。
type lenCall struct {
	slots     []string
	maxTokens int
}

// lenStub 截断二分用的假上游。`fn` 决定应答形态（内容 + finish_reason），
// 由各条腿自己指定——同一份桩要能演"半截"、"全空"、"正常结束"三种形态。
type lenStub struct {
	mu    sync.Mutex
	calls []lenCall
	fn    func(slots []string, maxTokens int) (content, finish string)
	// single 逐段兜底腿（不带 <sN> 标记）的应答；空串＝该腿一律拿不到可用译文。
	// 为什么单独一档：兜底腿的提示词与批量腿**不是同一个协议**（单段直译，
	// 没有编号契约），拿批量桩的应答喂它只会得到一个空串——那会把"兜底没接手"
	// 误读成产品缺陷。
	single string
}

// lenSlotRe 从 user 消息里现读 `<sN>源文</sN>`（与产品侧组装格式同源）。
var lenSlotRe = regexp.MustCompile(`(?s)<s(\d+)>(.*?)</s\d+>`)

func (u *lenStub) handler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	user := ""
	for _, m := range body.Messages {
		if m.Role == "user" {
			user = m.Content
		}
	}
	var slots []string
	for _, m := range lenSlotRe.FindAllStringSubmatch(user, -1) {
		slots = append(slots, m[2])
	}
	u.mu.Lock()
	u.calls = append(u.calls, lenCall{slots: slots, maxTokens: body.MaxTokens})
	u.mu.Unlock()

	content, finish := "", "stop"
	if len(slots) == 0 {
		content = u.single
	} else {
		content, finish = u.fn(slots, body.MaxTokens)
	}
	if finish == "" {
		finish = "stop"
	}
	raw, _ := json.Marshal(content)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":%q}]}`, raw, finish)
}

// recorded 取本次测试的全部调用读数（副本，避免与并发协程竞态）。
func (u *lenStub) recorded() []lenCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]lenCall(nil), u.calls...)
}

// lenEcho 把每一段按 `<sN>EN:源文</sN>` 回吐。
// 带上下标的好处：**下标映射错了立刻能从结果看出来**（拿到的译文属于另一段）。
func lenEcho(slots []string) string {
	var sb strings.Builder
	for i, s := range slots {
		fmt.Fprintf(&sb, "<s%d>EN:%s</s%d>\n", i+1, s, i+1)
	}
	return sb.String()
}

// lenTestEngine 装配：内存 Store（译文记忆有落点）+ 假上游 + 钉死 sqlite 方言。
func lenTestEngine(t *testing.T, u *lenStub) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(u.handler))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	e := NewEngine(cfg, nil, nil, nil)
	e.St = newTestStore(t)
	return e
}

// lenRun 跑一次批量腿（租户 1）。
func lenRun(t *testing.T, e *Engine, texts []string, bs int) ([]string, map[int]bool) {
	t.Helper()
	ctx := tenant.WithTenant(context.Background(), 1)
	return e.BatchTranslateMarkedOpts(ctx, texts, "en", bs, nil, BatchOptions{})
}

// lenSlotSeq 把调用序列压成「每次带了几段」，用于钉二分形状。
func lenSlotSeq(calls []lenCall) []int {
	out := make([]int, 0, len(calls))
	for _, c := range calls {
		out = append(out, len(c.slots))
	}
	return out
}

// ============ L1：二分救回（块被砍断 ⇒ 劈半后每半正常结束） ============

// TestLengthSplitRecovers 4 段块被砍断 ⇒ 恰好分两半各拨一枪，四段全部拿到译文、
// 且**没有任何槽位**被记成截断（救回来了就不该进截断档）。
func TestLengthSplitRecovers(t *testing.T) {
	u := &lenStub{fn: func(slots []string, _ int) (string, string) {
		if len(slots) >= 3 {
			// 这一形态正是现网：吐到第二段就撞预算，后面的段一个都没出来。
			return "<s1>EN:" + slots[0] + "</s1>", "length"
		}
		return lenEcho(slots), "stop"
	}}
	e := lenTestEngine(t, u)
	texts := []string{"alpha", "bravo", "charlie", "delta"}

	out, truncated := lenRun(t, e, texts, len(texts))
	seq := lenSlotSeq(u.recorded())
	if len(seq) != 3 || seq[0] != 4 || seq[1] != 2 || seq[2] != 2 {
		t.Fatalf("调用形状应为 [4 2 2]（砍断后劈两半各一枪），实际 %v ⇒ 二分没按块宽劈，或劈了没拨", seq)
	}
	for i := range texts {
		if want := "EN:" + texts[i]; out[i] != want {
			t.Fatalf("第 %d 段译文应为 %q，实际 %q ⇒ 劈半后的结果没按全局下标写回", i, want, out[i])
		}
	}
	if len(truncated) != 0 {
		t.Fatalf("四段全部救回来了，截断档应为空，实际 %v ⇒ orchestrator 会把\"救回来了\"读成\"被截断\"", truncated)
	}
}

// ============ L2：叶子仍截断 ⇒ 只记那一槽 ============

// TestLengthLeafTruncationMarksOnlyThatSlot 单段（叶子）两次都被砍断 ⇒ 只有它进截断档，
// 同批其他段一个都不许被连坐；叶子必须真抬过预算（同预算重拨必然再砍一次）。
func TestLengthLeafTruncationMarksOnlyThatSlot(t *testing.T) {
	truncText := strings.Repeat("TRUNC", 100) // 500 rune ⇒ 首枪 2048、叶子 4000（可区分）
	u := &lenStub{fn: func(slots []string, _ int) (string, string) {
		if len(slots) == 1 && slots[0] == truncText {
			return "", "length" // 输出被预算砍断，一个字符都没留下
		}
		return lenEcho(slots), "stop"
	}}
	e := lenTestEngine(t, u)
	texts := []string{"alpha", truncText, "charlie"}

	_, truncated := lenRun(t, e, texts, 1) // 每段独立成块 ⇒ 只有中间那段是叶子截断
	if len(truncated) != 1 || !truncated[1] {
		t.Fatalf("截断档应恰好是 {1:true}，实际 %v ⇒ 叶子截断被连坐到别的段，或压根没记", truncated)
	}
	var budget []int
	for _, c := range u.recorded() {
		if len(c.slots) == 1 && c.slots[0] == truncText {
			budget = append(budget, c.maxTokens)
		}
	}
	if len(budget) != 2 || budget[0] != 2048 || budget[1] != 4000 {
		t.Fatalf("那一槽应恰好拨 2 枪且预算为 [2048 4000]（叶子抬档），实际 %v ⇒ 叶子没重拨或没抬档，可救的行白等", budget)
	}
}

// ============ L3：正常结束绝不二分 ============

// TestLengthNoSplitOnStop 模型正常结束但漏了编号（stop + 只吐第一段）时，
// **不许**当成截断去劈块——那会把成本反向推高，且重拨同一批也未必好（解析层的事）。
func TestLengthNoSplitOnStop(t *testing.T) {
	u := &lenStub{
		single: "EN:bravo",
		fn: func(slots []string, _ int) (string, string) {
			if len(slots) > 1 {
				return "<s1>EN:" + slots[0] + "</s1>", "stop"
			}
			return lenEcho(slots), "stop"
		},
	}
	e := lenTestEngine(t, u)
	texts := []string{"alpha", "bravo", "charlie"}

	out, truncated := lenRun(t, e, texts, len(texts))
	multi := 0
	for _, c := range u.recorded() {
		if len(c.slots) > 1 {
			multi++
		}
	}
	if multi != 1 {
		t.Fatalf("多段请求应为 1 次（不二分），实际 %d 次 ⇒ finish_reason=stop 被当成截断劈块，成本反向上涨", multi)
	}
	if len(truncated) != 0 {
		t.Fatalf("正常结束不该产生截断档，实际 %v", truncated)
	}
	// 漏编号的段由逐段兜底接手（假上游单段能回）⇒ 交付面仍必须补齐，不能因为不二分就留空
	for i := range texts {
		if out[i] == "" || IsFailurePlaceholder(out[i]) {
			t.Fatalf("第 %d 段（%s）没拿到可用译文（%q）⇒ 不二分之后漏编号的段没人接手（留空交付）", i, texts[i], out[i])
		}
	}
}

// ============ L4：叶子预算抬档与钳位 ============

// TestLengthLeafBudgetRaisesAndClamps 同一次请求的两次调用：首枪按旧公式（×4），
// 叶子按 ×8 且仍钳在 2048..8192——`length` 是"预算不够"，同预算重拨只会再砍一次。
func TestLengthLeafBudgetRaisesAndClamps(t *testing.T) {
	cases := []struct {
		name      string
		runes     int
		wantFirst int
		wantLeaf  int
	}{
		{"短段两头都压在下限", 10, 2048, 2048},
		{"中段叶子按 ×8 抬档", 300, 2048, 2400},
		{"长段叶子顶到上限", 2000, 8000, 8192},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := &lenStub{fn: func([]string, int) (string, string) { return "", "length" }}
			e := lenTestEngine(t, u)
			text := strings.Repeat("字", c.runes)
			lenRun(t, e, []string{text}, 1)

			calls := u.recorded()
			if len(calls) < 2 {
				t.Fatalf("应至少拨 2 枪（首枪 + 叶子重拨），实际 %d 枪", len(calls))
			}
			if calls[0].maxTokens != c.wantFirst {
				t.Fatalf("首枪预算应为 %d，实际 %d ⇒ 首枪公式被改动（那是既有口径，不该跟着抬）", c.wantFirst, calls[0].maxTokens)
			}
			if calls[1].maxTokens != c.wantLeaf {
				t.Fatalf("叶子预算应为 %d，实际 %d ⇒ 要么没抬档（同预算重拨必然再砍断），要么没钳位", c.wantLeaf, calls[1].maxTokens)
			}
		})
	}
}

// ============ L5：递归深度上限 ============

// TestLengthSplitDepthIsBounded 一直砍断的 5 段块：按 depth≤2 只劈到 1~2 段即停，
// 调用形状必须是 [5 2 1 1 1 1 3 1 1 2 2]（共 11 枪）。
// 为什么钉死这个序列：深度多一层时，末尾那个 2 段块会被再劈成 1+1（多 2 枪），
// 序列立刻不同——这是"递归有没有边界"唯一能直接观测到的读数。
func TestLengthSplitDepthIsBounded(t *testing.T) {
	u := &lenStub{fn: func([]string, int) (string, string) { return "", "length" }}
	e := lenTestEngine(t, u)
	texts := []string{"alpha", "bravo", "charlie", "delta", "echo"}

	_, truncated := lenRun(t, e, texts, len(texts))
	want := []int{5, 2, 1, 1, 1, 1, 3, 1, 1, 2, 2}
	calls := u.recorded()
	if len(calls) < len(want) {
		t.Fatalf("调用数 %d 少于期望的 %d 枪", len(calls), len(want))
	}
	got := lenSlotSeq(calls[:len(want)])
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("二分形状应为 %v，实际 %v ⇒ 递归深度上限（batchSplitMaxDepth）被改动", want, got)
	}
	// 5 段全空 ⇒ 5 个截断槽（叶子判据只认没内容的槽）
	if len(truncated) != len(texts) {
		t.Fatalf("5 段全被砍空，截断档应有 5 条，实际 %v", truncated)
	}
}

// ============ L6：下标映射 ============

// TestLengthSplitKeepsIndexMapping 劈半后每半的 `<sN>` 都从 1 重新编号，
// 结果必须按**全局下标**落位（否则第 3 段会拿到第 1 段的译文，且界面完全正常）。
func TestLengthSplitKeepsIndexMapping(t *testing.T) {
	u := &lenStub{fn: func(slots []string, _ int) (string, string) {
		if len(slots) == 4 {
			// 首枪半截：只有第 3 段吐出来了——这段内容**必须被丢弃**（它属于被砍断的批）
			return "<s3>WRONG</s3>", "length"
		}
		return lenEcho(slots), "stop"
	}}
	e := lenTestEngine(t, u)
	texts := []string{"alpha", "bravo", "charlie", "delta"}

	out, _ := lenRun(t, e, texts, len(texts))
	for i := range texts {
		if want := "EN:" + texts[i]; out[i] != want {
			t.Fatalf("第 %d 段（%s）译文应为 %q，实际 %q ⇒ 劈半后下标映射错位（或把砍断那批的半截内容写回来了）",
				i, texts[i], want, out[i])
		}
	}
}
