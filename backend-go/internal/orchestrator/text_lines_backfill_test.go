// ============ 本文件职责中文说明 ============
// 2026-10-26 用户令①/裁决八-1/裁决九 的落码锁：文本逐行链「不留白、不丢行、回落必须被计数、
// 上游断要早停且早停不许把已到货的译文扔掉」，以及"回填语种一条都不写正式 TM"（W13）。
//
// 取向说明（为什么每条都要配正向对照）：这一批的判据本体是**打没打电话**与**有没有落空行**，
// 不是产物好不好看。计费按真实 token 聚合扣减，"早停省下的补译枪数"与"回落被计数"都是钱与诚实，
// 只断言产物会把"整条链压根没跑"判成通过。所以：
//
//	· 判"零补译"（L2/L3）必配"补译确实打过"（L1）同一把尺子；
//	· 判"回填不写 TM"（L6 甲）必配"不写是守卫造成的，不是这条路本来就写不进去"（L6 乙）。
//
// ============================================
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"translator/internal/engine"
	"translator/internal/qa"
	"translator/internal/store"
)

// pwPromptSplit 按**提示词形态**把假上游收到的请求分成「批量腿」与「单行腿」两类。
// 判据取批量提示词必带的 `<s1>` 编号契约（engine/engine.go 的段块组装），
// 不取"第几枪"——枪数会随动态批大小、并发数、分块边界变，而形态是两条腿的唯一区别。
func pwPromptSplit(h *pwHarness) (batch, single int) {
	h.Stub.mu.Lock()
	defer h.Stub.mu.Unlock()
	for _, p := range h.Stub.prompts {
		if strings.Contains(p, "<s1>") {
			batch++
			continue
		}
		single++
	}
	return batch, single
}

const (
	bfL1 = "第一段内容在这里。"
	bfL2 = "第二段内容在这里。"
	bfL3 = "第三段内容在这里。"
	bfL4 = "第四段内容在这里。"
)

// bfSource 四行非空源文中夹一行空行：空行必须原样占位，否则「行数恒等」这条判据天生测不到它。
func bfSource() string { return bfL1 + "\n" + bfL2 + "\n\n" + bfL3 + "\n" + bfL4 }

// TestBackfillKeepsEveryLineNonEmpty 用户令①「不留白、不整句丢失」的行为锁本体。
//
// 形态：批量腿第三槽回空（现网 9B 上游抖动就是这种半批），单行补译腿吐回中文原样
// （同一把可用性判据仍判不可用）⇒ 这一行只能按原文回落。
// 旧形态在这里写 `out = ""`，于是 12 行源文出 10 行译文（BUG-01/BUG-06 的母缺陷），
// 而被抹掉那一行的数字一起消失，再被数字硬闸按"源有译无"整单驳回。
func TestBackfillKeepsEveryLineNonEmpty(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		if strings.Contains(prompt, "<s1>") {
			return "<s1>Alpha.</s1><s2>Beta.</s2><s3></s3><s4>Delta.</s4>"
		}
		return bfL3 // 原样回显＝必过不了 TextTranslationUsable（同 text_lines_test.go 的 bad 靶子）
	})
	p := &ticketPayload{
		SourceText:   bfSource(),
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行链未接管，本条锁失明")
	}
	got := strings.Split(p.Translations["en"], "\n")
	src := strings.Split(bfSource(), "\n")
	if len(got) != len(src) {
		t.Fatalf("行数必须与源文恒等（%d 行），实得 %d 行: %q", len(src), len(got), p.Translations["en"])
	}
	for i := range src {
		if strings.TrimSpace(src[i]) == "" {
			if strings.TrimSpace(got[i]) != "" {
				t.Errorf("第 %d 行源文是空行，译文不许凭空多出内容: %q", i+1, got[i])
			}
			continue
		}
		// 判据本体：**每一个非空源行都必须有非空译文行**——回落原文也算非空，绝不留白。
		if strings.TrimSpace(got[i]) == "" {
			t.Errorf("第 %d 行留白（用户令①禁止的形态：不留白/不丢行），实得空串；全量 %q", i+1, p.Translations["en"])
		}
	}
	// 下标 3＝bfL3（源文第 3 行是空行，占一位下标 2 ⇒ 回落行落在 got[3]，不是 got[2]）。
	if got[3] != bfL3 {
		t.Errorf("补译仍不合格的第三段应按原文回落而不是留空，实得 %q（期望 %q）", got[3], bfL3)
	}
	if got[0] != "Alpha." || got[1] != "Beta." || got[4] != "Delta." {
		t.Errorf("批量腿到货的三行应逐字保留，实得 %q", got)
	}
	if p.UnresolvedLines["en"] != 1 {
		t.Errorf("回落行必须被计数（交付面不许静默），UnresolvedLines[en] 实得 %d（期望 1）", p.UnresolvedLines["en"])
	}
	if p.UpstreamDeadLines["en"] != 0 {
		t.Errorf("这一单没判上游断，UpstreamDeadLines 不该有数，实得 %d", p.UpstreamDeadLines["en"])
	}
	if p.Sources["en"] != SourceModelWithFallback {
		t.Errorf("有回填行的语种来源必须是 %q，实得 %q", SourceModelWithFallback, p.Sources["en"])
	}
	// 正向对照：补译梯真打过了（否则上面"零补译"那两条锁会因为"整链没跑"而恒真）。
	// 计数只认 **system 不带尾注** 的那一型（`minimalCalls`）：按 user 段数会把
	// `BatchTranslate` 内部的逐段兜底一起算进来（实测四行全断＝12 枪），那是引擎的钱不是本链的枪。
	if n := h.Stub.minimalCalls(); n < 1 {
		t.Errorf("不合格行必须走单行补译梯，实得极简腿 %d 次", n)
	}
}

// TestUpstreamDeadBackfillsWithoutFiringRetries 裁决八-1＋方案 §七「上游断早停」：
// 整批零候选＝上游整条不通 ⇒ 一枪补译都不打，剩余未译行一次性按原文回填，来源列如实写 upstream_failed。
func TestUpstreamDeadBackfillsWithoutFiringRetries(t *testing.T) {
	h := pwSetup(t, func(prompt string) string { return "" }) // 上游一律回空＝401／欠费／超时那一族
	p := &ticketPayload{
		SourceText:   bfSource(),
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行链未接管，本条锁失明")
	}
	if batch, _ := pwPromptSplit(h); batch == 0 {
		t.Fatal("批量腿一次都没打 ⇒ 本条锁测的是「链压根没跑」而不是「早停」，判据失明")
	}
	if n := h.Stub.minimalCalls(); n != 0 {
		t.Errorf("判上游断后一枪补译都不许打（上游不通时多打一万次也不会好，客户白等白扣），实得极简腿 %d 次", n)
	}
	got := strings.Split(p.Translations["en"], "\n")
	src := strings.Split(bfSource(), "\n")
	if len(got) != len(src) {
		t.Fatalf("行数必须恒等，实得 %d 行: %q", len(got), p.Translations["en"])
	}
	for i := range src {
		if strings.TrimSpace(src[i]) == "" {
			continue
		}
		if got[i] != src[i] {
			t.Errorf("上游断的语种每一行都应按原文出货（不留白），第 %d 行实得 %q", i+1, got[i])
		}
	}
	if p.UpstreamDeadLines["en"] != 4 {
		t.Errorf("UpstreamDeadLines[en] 应为 4（上游断档回填行数），实得 %d", p.UpstreamDeadLines["en"])
	}
	if p.UnresolvedLines["en"] != 4 {
		t.Errorf("UnresolvedLines[en] 应为 4（总数），实得 %d", p.UnresolvedLines["en"])
	}
	if p.Sources["en"] != SourceUpstreamFailed {
		t.Errorf("整语种判上游断时来源必须是 %q；写 model＝给失败读数背书（八-1 点名的那处撒谎），实得 %q",
			SourceUpstreamFailed, p.Sources["en"])
	}
}

// TestUpstreamDeadKeepsArrivedTranslations 早停省的是**补译枪**，不是已到货的译文。
//
// 同一批（≤15 行）后面的槽位常常已经翻出来了，那些是本单已经付过钱的成品；
// 无条件回落原文＝把已到货的译文丢回给客户看中文。形态：五槽里 2~4 回空（连续三槽触发早停），
// 第五槽本来有可用译文 ⇒ 必须保留，同时那一行的回填数不许把它算进去。
func TestUpstreamDeadKeepsArrivedTranslations(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		if strings.Contains(prompt, "<s1>") {
			return "<s1>Alpha.</s1><s2></s2><s3></s3><s4></s4><s5>Echo.</s5>"
		}
		return ""
	})
	src := strings.Join([]string{bfL1, bfL2, bfL3, bfL4, "第五段内容在这里。"}, "\n")
	p := &ticketPayload{
		SourceText:   src,
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行链未接管，本条锁失明")
	}
	got := strings.Split(p.Translations["en"], "\n")
	if len(got) != 5 {
		t.Fatalf("行数应恒等 5，实得 %d 行: %q", len(got), p.Translations["en"])
	}
	if got[4] != "Echo." {
		t.Errorf("连续档触发早停**之后**已到货的第五行译文必须保留（早停不该扔掉已付钱的成品），实得 %q", got[4])
	}
	if got[1] != bfL2 || got[2] != bfL3 || got[3] != bfL4 {
		t.Errorf("空槽那三行应按原文出货，实得 %q", got[1:4])
	}
	if got[0] != "Alpha." {
		t.Errorf("第一行到货译文不该被连带覆盖，实得 %q", got[0])
	}
	// 连续档触发点之前那两行仍走补译梯（这是"部分失败"与"整批零候选"的分工，不是漏停）。
	if n := h.Stub.minimalCalls(); n == 0 {
		t.Errorf("触发早停之前的空槽应照常走补译梯，实得极简腿 0 次")
	}
	// 第 2、3 槽空 ⇒ deadStreak 1、2；第 4 槽空 ⇒ 3 触发早停。**触发那一行的回填才算上游断档**，
	// 前两行是"补译梯打完了才回落"，归 not_usable 档——两档各记各的，交付面才说得出是哪一族。
	if p.UpstreamDeadLines["en"] != 1 {
		t.Errorf("只有触发早停**当行及之后**的回填才计入上游断档，UpstreamDeadLines[en] 实得 %d（期望 1）", p.UpstreamDeadLines["en"])
	}
	if p.UnresolvedLines["en"] != 3 {
		t.Errorf("回填总数（两档合计）应为 3，UnresolvedLines[en] 实得 %d", p.UnresolvedLines["en"])
	}
	if p.Sources["en"] != SourceUpstreamFailed {
		t.Errorf("一旦判过上游断，来源档就报上游断（几行回填另有 UpstreamDeadLines 这个口，本列不兼顾精度），实得 %q", p.Sources["en"])
	}
}

// TestUpstreamDeadSkipsRemainingLangs 裁决八-1 的第二层：上游是**所有语种共用的一条腿**。
// 第一个语种整批回 `[翻译失败]` ⇒ 后面每个语种只会同样整批失败，而每一批在引擎内部还要逐段再兜三枪
// （2026-10-26 实测：一个四行语种全断＝1 次批量 + 12 次单发）。三语种旧形态要拨 3 批 36 单发，
// 现在只拨 1 批，其余两个语种直接按原文出货并各自计数——交付字节一字不差，钱与等待都少 N 倍。
func TestUpstreamDeadSkipsRemainingLangs(t *testing.T) {
	h := pwSetup(t, func(prompt string) string { return "" })
	p := &ticketPayload{
		SourceText:   bfSource(),
		TargetLangs:  []string{"en", "ja", "de"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行链未接管，本条锁失明")
	}
	batch, _ := pwPromptSplit(h)
	if batch != 1 {
		t.Errorf("上游判断后剩余语种一次批量都不许拨（同一条腿不会自己好），实得批量请求 %d 次（期望 1）", batch)
	}
	if n := h.Stub.minimalCalls(); n != 0 {
		t.Errorf("上游判断后不许出现任何极简补译枪，实得 %d 次", n)
	}
	src := strings.Split(bfSource(), "\n")
	for _, lc := range p.TargetLangs {
		got := strings.Split(p.Translations[lc], "\n")
		if len(got) != len(src) {
			t.Fatalf("%s 行数必须恒等 %d，实得 %d 行: %q", lc, len(src), len(got), p.Translations[lc])
		}
		for i := range src {
			if strings.TrimSpace(src[i]) == "" {
				continue
			}
			if got[i] != src[i] {
				t.Errorf("%s 第 %d 行应按原文出货（不留白不丢行），实得 %q", lc, i+1, got[i])
			}
		}
		if p.Sources[lc] != SourceUpstreamFailed {
			t.Errorf("%s 来源必须是 %q（跳过的语种与打过的语种同一个诚实档），实得 %q", lc, SourceUpstreamFailed, p.Sources[lc])
		}
		if p.UpstreamDeadLines[lc] != 4 {
			t.Errorf("%s 回填行数必须逐语种记满（交付面那句『哪一语种几行』就读不到），实得 %d", lc, p.UpstreamDeadLines[lc])
		}
	}
}

// TestTruncatedSlotsAreNotJudgedUpstreamDead 八-3c／九-3（W8）：被 max_tokens 截断的空槽
// **一律不许**喂进"上游整条不通"的计数器。
//
// 为什么这一条必须与早停同批上（用户裁决九-3 的原话是"W8 一起上"）：截空与"上游没返回"
// 在字节上完全同形（都是空串／`[翻译失败]`），拆开上线就是先投一版会把可救的行集体打成原文的判据。
// 靶子刻意给**连续三槽**截断——早停阈值恰是 3：不带截断维时这一批必然判死，
// 于是"没判死"这个结果只能来自那一条新腿（归属由下面两条读数当场自证，不靠注释声称）。
func TestTruncatedSlotsAreNotJudgedUpstreamDead(t *testing.T) {
	// 半截回复：**只翻第一段**（按内容认，不按位置认），第二槽开了个头就被砍 ⇒ 本批里只有
	// 「源文第一段」那一槽有内容，其余全空。
	//
	// ⚠️ 为什么必须按内容锚定（2026-10-28 计价/成本批真踩）：夹具过去写成"prompt 含 `<s1>` 就回 Alpha."，
	// 那是**位置式**假设。批量腿前面接了译文记忆（缓存 A/B）之后，上面那段「归属自证」先跑过一次
	// BatchTranslateMarked，第一段已被记忆命中 ⇒ 端到端那一次的批量窗口从第二段开始，s1 变成了
	// "第二段"，位置式假上游就把 Alpha. 配给了第二段。产品侧的 idx 映射是对的（s1 的译文就该落到
	// 本次批量的第一段），坏的是夹具。按内容锚定后，无论批量窗口从哪一段开始，回话的永远只有真正的第一段。
	reply := func(prompt string) string {
		if strings.Contains(prompt, "<s1>"+bfL1+"</s1>") {
			return "<s1>Alpha.</s1><s2>Beta"
		}
		return ""
	}
	h := pwSetup(t, reply)
	h.Stub.finish = "length"
	ctx := context.Background()
	src := strings.Join([]string{bfL1, bfL2, bfL3, bfL4}, "\n")
	need := []string{bfL1, bfL2, bfL3, bfL4}

	// ── 归属自证（两条读数一起看，缺一即判夹具前提不成立）：
	//   ① 摘掉截断维时，连续占位的**最长段必须够到早停阈值**——不然"没判死"这个结果
	//      不是新腿给的，这条锁就是空转（靶子刻意给连续三槽截断，阈值正是 3）；
	//   ② 带上截断维时，最长段必须**够不到**阈值——这才证明是这一维把它救回来的。
	batch, trunc := h.W.Engine.BatchTranslateMarked(ctx, need, "en", 15, nil)
	min := h.W.textLineDeadStreakMin()
	if len(trunc) == 0 {
		t.Fatalf("假上游回了 finish_reason=length 却一个截断槽都没标 ⇒ 引擎那一条腿没接上（读数 %q）", batch)
	}
	if run := maxPlaceholderRun(batch, nil); run < min {
		t.Fatalf("夹具前提不成立：忽略截断维时最长连续占位只有 %d，够不到阈值 %d ⇒ 摘腿也不会红，本条锁空转", run, min)
	}
	if run := maxPlaceholderRun(batch, trunc); run >= min {
		t.Fatalf("截断维没生效：带上 truncated 后最长连续占位仍是 %d（阈值 %d，读数 %q）", run, min, batch)
	}

	// ── 端到端：整语种不得判上游断，回填行归"截断档"而不是"上游断档"。
	p := &ticketPayload{
		SourceText:   src,
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(ctx, p) {
		t.Fatal("逐行链未接管，本条锁失明")
	}
	got := strings.Split(p.Translations["en"], "\n")
	if len(got) != 4 {
		t.Fatalf("行数必须恒等 4，实得 %d 行: %q", len(got), p.Translations["en"])
	}
	for i := 1; i < 4; i++ {
		if got[i] != need[i] {
			t.Errorf("第 %d 行补译仍空 ⇒ 应按原文出货（不留白），实得 %q", i+1, got[i])
		}
	}
	if got[0] != "Alpha." {
		t.Errorf("第一行到货译文必须保留，实得 %q", got[0])
	}
	if p.Sources["en"] == SourceUpstreamFailed {
		t.Errorf("三行连续截空把整语种误判成上游断＝把可救的行集体打成原文（八-3c 点名的形态），来源实得 %q", p.Sources["en"])
	}
	if p.Sources["en"] != SourceModelWithFallback {
		t.Errorf("来源应为 %q（有回填、但成因是截断不是上游断），实得 %q", SourceModelWithFallback, p.Sources["en"])
	}
	if p.UpstreamDeadLines["en"] != 0 {
		t.Errorf("上游断档计数必须为 0（这一单没有一行是早停回填），实得 %d", p.UpstreamDeadLines["en"])
	}
	if p.TruncatedLines["en"] != 3 {
		t.Errorf("截断档应记满 3 行（交付文档点名的那句是「输出被截断」），实得 %d", p.TruncatedLines["en"])
	}
	if p.UnresolvedLines["en"] != 3 {
		t.Errorf("回填总数应为 3，实得 %d", p.UnresolvedLines["en"])
	}
}

// maxPlaceholderRun 空/失败占位槽位的**最长连续段**（与 text_lines.go 里那个计数同口径）。
// 参数：trunc 为 nil 时忽略截断维（当作"这版判据没有那一条腿"来量射程）。
func maxPlaceholderRun(batch []string, trunc map[int]bool) int {
	cur, best := 0, 0
	for j, v := range batch {
		if engine.IsFailurePlaceholder(v) && !trunc[j] {
			cur++
			if cur > best {
				best = cur
			}
			continue
		}
		cur = 0
	}
	return best
}

// TestKBLaneKeepsItsSourceLabel 知识库直配命中且整语种复用（零模型调用）时，来源列必须仍是 kb。
// 旧形态在循环末尾无条件写 "model"——那是「读数为失败背书」的同一族反方向：给零成本的一单记上模型来源，
// 管理台按来源统计与「本单零模型成本」那条判据（pipeline_workflow_test.go:273 同源）跟着失真。
func TestKBLaneKeepsItsSourceLabel(t *testing.T) {
	h := pwSetup(t, func(prompt string) string { return "UNSET" })
	good := "Alpha.\nBeta.\n\nGamma.\nDelta."
	p := &ticketPayload{
		SourceText:   bfSource(),
		TargetLangs:  []string{"en"},
		Translations: map[string]string{"en": good},
		Sources:      map[string]string{"en": SourceKB},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行链未接管，本条锁失明")
	}
	batch, single := pwPromptSplit(h)
	if batch+single != 0 {
		t.Errorf("全部行都合格时一行模型都不许打（重复扣费），实得批量 %d／单行 %d", batch, single)
	}
	if n := h.Stub.minimalCalls(); n != 0 {
		t.Errorf("零模型调用的语种不许出现极简腿，实得 %d 次", n)
	}
	if p.Sources["en"] != SourceKB {
		t.Errorf("来源必须保持 kb，实得 %q", p.Sources["en"])
	}
	if p.Translations["en"] != good {
		t.Errorf("复用行应逐字保留，实得 %q", p.Translations["en"])
	}
	if p.UnresolvedLines["en"] != 0 {
		t.Errorf("零回填时不许出现未译出计数，实得 %d", p.UnresolvedLines["en"])
	}
}

// TestSourceRosterIsClosed 名单锁：来源列是**对外契约**（管理台来源列、按来源统计、CSV 导出、
// 开放 API 的 `sources` 字段、客户工单详情都读它），名单之外不许长出第五种值，
// 也不许再有裸字面量写点（裸字面量＝下一位改它时这条锁看不见）。
//
// 射程只到 **orchestrator 包内**：`internal/engine` 那份 `"model"`/`"kb"` 是它自己的 map
// （engine/text.go 的 allSrc、TranslateLangsInto 的回填），与工单载荷那一列不是同一个事实。
func TestSourceRosterIsClosed(t *testing.T) {
	// ① 四个值逐字钉住（改名式破坏＝改了常量却同步改了消费方的期望，这里当场红）。
	want := map[string]string{
		"SourceKB":                SourceKB,
		"SourceModel":             SourceModel,
		"SourceModelWithFallback": SourceModelWithFallback,
		"SourceUpstreamFailed":    SourceUpstreamFailed,
		"SourceApproved":          SourceApproved,
	}
	got := map[string]string{
		"SourceKB":                "kb",
		"SourceModel":             "model",
		"SourceModelWithFallback": "model_with_source_fallback",
		"SourceUpstreamFailed":    "upstream_failed",
		"SourceApproved":          "approved",
	}
	for name, v := range want {
		if v != got[name] {
			t.Errorf("%s 的出栈值变了：期望 %q 实得 %q（对外契约，改动必须同步 12 语种文案与开放 API 三面文档）",
				name, got[name], v)
		}
	}
	// ② 生产写点零裸字面量：扫**两个包**的非测试文件里的 `Sources[...] = "xxx"`。
	//    名单里那五个值一律必须走常量，否则这条锁只能靠人记住；
	//    只扫本包＝给审批面那一处（api/tickets.go 写 approved）留盲区，锁就只锁了一半。
	scanned, hits := pwBareSourceWrites(t)
	if len(hits) != 0 {
		t.Errorf("发现裸字面量来源写点（名单锁的盲区），必须改用 Source* 常量：\n%s", strings.Join(hits, "\n"))
	}
	if scanned < 2000 {
		t.Fatalf("扫描行数只有 %d ⇒ 射程不对（两个包的非测试文件远多于此），裸写点数 0 属假绿", scanned)
	}
	// ③ 判据自证：同一条扫描函数喂一句真裸写点必须报出来（否则②那条恒真是空转锁）。
	if n := countBareSourceWrites("\tp.Sources[lc] = \"model\""); n != 1 {
		t.Errorf("自证失败：裸写点样张应报 1 处，实得 %d ⇒ 正则够不到真实形态", n)
	}
	if n := countBareSourceWrites("\t\tp.Sources[lc] = SourceModel\n"); n != 0 {
		t.Errorf("自证失败：走常量的样张不该报，实得 %d ⇒ 正则把常量也当裸字面量，②那条会恒红", n)
	}
}

// pwBareSourceWrites 扫本包非测试 .go 文件，返回「扫到的总行数」与裸字面量写点清单。
func pwBareSourceWrites(t *testing.T) (int, []string) {
	t.Helper()
	// 靶子＝全部生产写点所在的包。审批面（api）也在射程内（它写 approved 这一档）。
	dirs := []string{".", "../api"}
	total, hits := 0, []string{}
	for _, d := range dirs {
		files, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("读目录 %s 失败: %v", d, err)
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := d + "/" + name
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("读 %s: %v", path, err)
			}
			for i, line := range strings.Split(string(b), "\n") {
				total++
				if n := countBareSourceWrites(line); n > 0 {
					hits = append(hits, fmt.Sprintf("%s:%d %s", path, i+1, strings.TrimSpace(line)))
				}
			}
		}
	}
	return total, hits
}

// bareSourceWriteRE 只认「写点」形态：`Sources[任意] = "字面量"`。
// 声明处的 `SourceKB = "kb"` 左侧不是下标，够不到（自证③的两条样张专门钉这两侧）。
var bareSourceWriteRE = regexp.MustCompile(`Sources\[[^\]]+\]\s*=\s*"`)

func countBareSourceWrites(line string) int {
	// 注释行不算写点（说明文字里出现这个形态是记录，不是行为）。
	if t := strings.TrimSpace(line); strings.HasPrefix(t, "//") {
		return 0
	}
	return len(bareSourceWriteRE.FindAllString(line, -1))
}

// TestFeedbackSkipsBackfilledLangs W13：回填过原文的语种**一条都不写正式 TM**。
//
// 这一条是用户令①「不留白」的**直接副产品**，不堵就是新通道：
// 现成的预筛拦不住它——`qa/qa.go:94` 把"译文与源文相同"判成 warning，`screener.go:20` 打分
// `100 - 40*error - 8*warning` ⇒ 同文对子拿 92 分，默认阈值 80 判"达标直写"。
// 于是「中文→中文」这类对子沉淀进客户记忆里，此后每次命中都直出＝一次上游抖动固化成长期复发，
// 比漏一行坏得多（漏一行只坏这一单，脏 TM 会自己复发）。
//
// 两腿缺一不可：甲腿（有回填⇒零写入）没有乙腿就是空转——乙腿先证明这条路本来写进去了，
// 再证明是**守卫**把它挡住的。
func TestFeedbackSkipsBackfilledLangs(t *testing.T) {
	const src = "第一段说明在这里。\n第二段说明在这里。"
	const good = "The first note is here.\nThe second note is here."
	const mixed = "第一段说明在这里。\nThe second note is here." // 一行原文混在一篇译文里

	h := pwSetup(t, nil)
	// 乙腿的前置证据：同文混排这一对在现成预筛下**照样算达标**（否则甲腿的红要归因给评分而不是守卫）。
	if sc := qa.ScreenPair(src, mixed).Score; sc < 80 {
		t.Fatalf("夹具前提不成立：同文混排样张在预筛下只得 %d 分（<80），那甲腿的零写入就不是守卫拦的", sc)
	}

	approvedPayload := func(unresolved int) string {
		p := ticketPayload{
			SourceText:   src,
			TargetLangs:  []string{"en"},
			Translations: map[string]string{"en": mixed},
			Sources:      map[string]string{"en": SourceModelWithFallback},
		}
		if unresolved > 0 {
			p.UnresolvedLines = map[string]int{"en": unresolved}
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("序列化载荷: %v", err)
		}
		return string(b)
	}
	newApproved := func(body string) *store.Ticket {
		tk, err := h.St.CreateTicket(1, 7, "写库守卫", src, "", "en")
		if err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		tk.FinalResult = body
		tk.Status = store.TicketApproved
		if err := h.St.UpdateTicket(tk); err != nil {
			t.Fatalf("写工单: %v", err)
		}
		return tk
	}

	// ── 甲腿：有回填 ⇒ 整语种跳过，正式 TM 零行
	if err := h.W.runFeedback(context.Background(), newApproved(approvedPayload(1))); err != nil {
		t.Fatalf("runFeedback(甲): %v", err)
	}
	if n := pwTMFeedbackRows(t, h, src); n != 0 {
		t.Fatalf("回填语种必须一条都不写正式 TM（防「中文→中文」沉淀），实得 %d 行", n)
	}

	// ── 乙腿：同一对句、同一条路径，只是没标回填 ⇒ 必须真写进去
	if err := h.W.runFeedback(context.Background(), newApproved(approvedPayload(0))); err != nil {
		t.Fatalf("runFeedback(乙): %v", err)
	}
	if n := pwTMFeedbackRows(t, h, src); n != 1 {
		t.Fatalf("正向对照：零回填时这对句应照常入正式 TM（甲腿的红必须来自守卫本身），实得 %d 行", n)
	}
}
