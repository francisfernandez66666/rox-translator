// ============ stream_contract_test.go 职责中文说明 ============
// DEF-P1-04（2026-11-01）门禁 B1-2.1：delta 流契约过滤器的**行为表 + 分层等值锁**。
//
// 三条写法上的讲究（都不是形式主义，每条都对应一种"锁会撒谎"的坏法）：
//  1. 等值锁的尺子是 stripPseudoTags(extractContractTranslation(raw))，**不是** extract 单独一层：
//     最终腿真顺序是 extract → strip（postprocess.go:616 → :636），把裸标签吃掉的是 strip 的第 3 步。
//     v1 草案按 extract 写会**恒红**（未闭合那一例 extract 原样返回带标签的串）。
//  2. 「长前置正文 + 后随契约块」这一族**必然与最终腿分叉**（流式吐出去收不回），
//     所以单列成"已知分叉表"并显式断言分叉方向，**禁止**塞进等值锁里当"两条管线已合一"的证据。
//  3. 射程只钉契约+伪标签两层；术语替换／去中文／尾段截断／品牌归一／审校改写**不在 delta 上做**
//     ——本文件任何一条锁都不许被读成"delta 与 done 逐字相同"。
//
// =============================================
package engine

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// feedFrames 按给定的 chunk 序列喂一遍过滤器，返回客户屏幕上实际收到的整串。
func feedFrames(frames ...string) string {
	f := NewContractFilter()
	var b strings.Builder
	for _, c := range frames {
		b.WriteString(f.Push(c))
	}
	b.WriteString(f.Flush())
	return b.String()
}

// splitByRune 逐字符切帧（比任何人工分帧都狠：标签一定被拆在帧边界上）。
func splitByRune(s string) []string {
	out := make([]string, 0, len(s))
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

// TestContractFilterBehaviorTable 行为表八例（施工方案 §B1-2.1 那张表逐行落码）。
func TestContractFilterBehaviorTable(t *testing.T) {
	cases := []struct {
		name   string
		frames []string
		want   string
	}{
		{"1 标签跨 chunk", []string{"<t", "Hello", "</t>"}, "Hello"},
		// ★ 边界①：只吐开标签、全文无闭合 ⇒ Flush 必须把 inside 残留**全部透传**（丢了比现状更坏）
		{"2 只吐开标签", []string{"<t", "Hello", " world"}, "Hello world"},
		{"3 无契约原样透传", []string{"Hello", " world"}, "Hello world"},
		// ★ 边界④的另一半：块外的注释丢弃（extract 也是整段丢）
		{"4 标签外注释丢弃", []string{"<t>Body</t>", "\n注释：术语对照"}, "Body"},
		// ★ 边界⑤：多块之间**必须**补 \n（extract 用 strings.Join(parts,"\n")，v1 写"均可"与自己的等值锁矛盾）
		{"5 多个契约块补分隔符", []string{"<t>A</t>", "<t>B</t>"}, "A\nB"},
		{"6 空输入不 panic", nil, ""},
		{"7 逐字符喂仍然只出正文", splitByRune("<t>你好，世界</t>尾注"), "你好，世界"},
		// 属性形态（<t lang="zh">）与大小写形态：与 contractOpenRe 同判据
		{"8 带属性的开标签", []string{`<t lang="zh">Body</t>`}, "Body"},
		{"9 大写标签", []string{"<T>Body</T>"}, "Body"},
		// 走形白名单标签（<tt>／<target>）：stripPseudoTags 会剥掉，delta 也必须剥掉
		{"10 走形伪标签不泄漏", []string{"<tt>Body</tt>"}, "Body"},
		{"11 合法尖括号内容保留", []string{"a<b 且 c>d"}, "a<b 且 c>d"},
		// ★ 边界③：`<t` 被拆两帧且后面直接黏正文（实测 <tHello</t> 那一族）
		{"12 开标签黏正文", []string{"<t", "Hello", "</t>"}, "Hello"},
		// 空块不占分隔符（extract 丢掉空 parts）
		{"13 空块不补换行", []string{"<t>A</t>", "<t></t>", "<t>B</t>"}, "A\nB"},
		// 孤立闭标签（无开标签）：整串无契约 ⇒ 透传正文、剥掉标签（与 extract→strip 同果）
		{"14 孤立闭标签", []string{"abc</t>def"}, "abcdef"},
		// ★ 边界①的第二半：下面三例都是「只有 Flush() 才结算得出来」的形态——
		// 把 Flush 摘掉（反证 M2）就必须在这里现形，否则"未闭合也要把正文交出来"那条承诺没有腿。
		{"15 流末开标签未判定完，残字符按正文出栈", []string{"<t", "a"}, "a"},
		{"16 流末扣着的块尾空白不许丢", []string{"<t>Hello", " world", " "}, "Hello world "},
		// 属性段没收到 '>' 就到流尾：整串都是标签字节 ⇒ 全部丢弃，不把 lang="zh" 吐上屏幕
		{"17 属性段未收口整段丢弃", []string{`<t lang="zh"`}, ""},
	}
	for _, c := range cases {
		got := feedFrames(c.frames...)
		if got != c.want {
			t.Errorf("%s: 输入=%q 期望=%q 实得=%q", c.name, strings.Join(c.frames, "|"), c.want, got)
		}
	}
}

// TestContractFilterNeverLeaksContractTags 泄漏负向锁：任何输入下，delta 侧都不许出现
// 契约标签本体（`<t`／`</t`）。这条才是本缺陷的产品判据（行为表逐例钉的是"对不对"，
// 这一条钉的是"绝不再泄漏"），也是反证里"摘掉过滤器"必然打中的那一腿。
func TestContractFilterNeverLeaksContractTags(t *testing.T) {
	leakRe := regexp.MustCompile(`(?i)</?\s*t\b`)
	inputs := [][]string{
		{"<t", "Hello", "</t>"},
		{"<t", "Hello", " world"},
		{"<t>Body</t>", "\n注释"},
		{"<t>A</t>", "<t>B</t>"},
		{"<t"},
		{`<t lang=`},
		{`<t lang="zh">B`, "ody</t"},
		{"<ta>", "x</t>"},
		{"<tHello</t>"},
		{"abc", "</t", "def"},
		{"<T>Body</T>"},
		{"<t></t>", "<t>只吐开标签不闭合"},
	}
	for _, frames := range inputs {
		// 逐帧断言（不是只断整串）：泄漏发生在某一帧上就必须在那一帧抓到
		f := NewContractFilter()
		var all strings.Builder
		for _, fr := range frames {
			emit := f.Push(fr)
			if leakRe.MatchString(emit) {
				t.Errorf("单帧泄漏契约标签：输入=%q 帧=%q 该帧输出=%q", strings.Join(frames, "|"), fr, emit)
			}
			all.WriteString(emit)
		}
		tail := f.Flush()
		if leakRe.MatchString(tail) {
			t.Errorf("Flush 泄漏契约标签：输入=%q 尾帧输出=%q", strings.Join(frames, "|"), tail)
		}
		all.WriteString(tail)
		if leakRe.MatchString(all.String()) {
			t.Errorf("整串泄漏契约标签：输入=%q 输出=%q", strings.Join(frames, "|"), all.String())
		}
	}
}

// TestStreamContractFilterEqualsFinalLeg 分层等值锁（见文件头第 1 条）。
// ⚠️ 射程只钉「契约提取 + 伪标签拆除」两层；delta 侧**不做**术语替换／去中文／
// 尾段截断／品牌归一／审校改写——别把本锁当成"两条管线已合一"的证据。
func TestStreamContractFilterEqualsFinalLeg(t *testing.T) {
	for _, raw := range []string{
		"<t>Hello world</t>",
		"<t>Hello</t>\n<t>World</t>",
		"\n<t>未闭合标签也要透传",
		"没有契约的普通译文",
		"<t>Body</t>\n注释：术语对照表",
		"<t>A</t><t></t><t>B</t>",
		"abc</t>def",
		`<t lang="zh">带属性的契约块</t>`,
	} {
		f := NewContractFilter()
		var streamed strings.Builder
		for _, ch := range splitByRune(raw) {
			streamed.WriteString(f.Push(ch))
		}
		streamed.WriteString(f.Flush())
		want := stripPseudoTags(extractContractTranslation(raw)) // ★ 与最终腿同两层、同顺序
		if got := strings.TrimSpace(streamed.String()); got != strings.TrimSpace(want) {
			t.Fatalf("流式与最终腿不一致:\n输入=%q\n流式=%q\n最终=%q", raw, got, want)
		}
	}
}

// TestStreamContractKnownDivergence 已知分叉表（★ 边界④，设计内取舍，不是缺陷）。
//
// extract 会把 <t> 之前的前置正文整段丢掉，而流式**吐出去就收不回**；过滤器的取舍是
// "只憋住判定一个标签开头所需的字节"，于是这一族流式含前置正文、最终不含。
// 判据问的是**方向**（流式更早出内容、最终腿更干净），**禁止**为了逐字对齐而把首字延迟拉长。
func TestStreamContractKnownDivergence(t *testing.T) {
	raw := "这是一段很长的前置正文，模型违约把它念在了契约块之前。\n<t>Body</t>\n尾注同样丢弃"
	f := NewContractFilter()
	var streamed strings.Builder
	for _, ch := range splitByRune(raw) {
		streamed.WriteString(f.Push(ch))
	}
	streamed.WriteString(f.Flush())
	got := streamed.String()
	want := stripPseudoTags(extractContractTranslation(raw))

	if !strings.Contains(got, "前置正文") {
		t.Fatalf("分叉表失效：流式按设计应当**保留**先看到的前置正文，实得=%q", got)
	}
	if strings.Contains(want, "前置正文") {
		t.Fatalf("分叉表失效：最终腿按设计应当**丢掉**前置正文，实得=%q", want)
	}
	if !strings.Contains(got, "Body") || want != "Body" {
		t.Fatalf("两侧都必须把契约块正文交出来：流式=%q 最终=%q", got, want)
	}
	// 逐字对齐不是本族的判据，但**首字延迟**必须是：过滤器在等到标签判定之前不许憋住正文
	if firstEmitDelay := firstEmitFrames(raw); firstEmitDelay > 1 {
		t.Fatalf("为对齐而憋正文＝把缺陷换成更慢：首个字等到第 %d 帧才出", firstEmitDelay)
	}
}

// firstEmitFrames 数"第几帧才第一次有东西吐出去"（首字延迟的读数）。
func firstEmitFrames(raw string) int {
	f := NewContractFilter()
	for i, ch := range splitByRune(raw) {
		if f.Push(ch) != "" {
			return i + 1
		}
	}
	return len(raw)
}

// TestContractFilterStateIsPerAttempt ★ 调用点约束：实例必须每次上游调用各一份。
// 这里用"同一实例喂两轮完整流"证明状态不会把第二轮吞掉；真正的防线在接线锁
// （TestContractFilterWiredPerAttempt）——包级共享实例的坏法是"整轮正文消失"，
// 只在递归重试那一拍才现形，所以两腿都要有。
func TestContractFilterStateIsPerAttempt(t *testing.T) {
	f := NewContractFilter()
	round1 := ""
	for _, c := range []string{"<t", "First", "</t>"} {
		round1 += f.Push(c)
	}
	round1 += f.Flush()
	if round1 != "First" {
		t.Fatalf("第一轮输出不符：%q", round1)
	}
	round2 := ""
	for _, c := range []string{"<t", "Second", "</t>"} {
		round2 += f.Push(c)
	}
	round2 += f.Flush()
	if !strings.Contains(round2, "Second") {
		t.Fatalf("第二轮正文被上一轮状态吞掉（共享实例的坏法）：%q", round2)
	}
	if strings.Contains(round2, "<t") || strings.Contains(round2, "</t") {
		t.Fatalf("第二轮泄漏标签：%q", round2)
	}
}

// TestContractFilterWiredPerAttempt 接线锁（守卫级，不是判据级）：
// NewContractFilter() 必须出现在 singleLangRaw 的函数体内，且**不许**存在包级实例。
// 反证形态：把 `cf := NewContractFilter()` 提到包级 var ⇒ 本锁红（而行为表全绿——
// 这正是「判据级断言代替不了守卫级」那一族）。
func TestContractFilterWiredPerAttempt(t *testing.T) {
	src := readEngineSource(t, "engine.go")
	fn := contractFilterFnBody(t, src, "func (e *Engine) singleLangRaw(")
	if !strings.Contains(fn, "NewContractFilter()") {
		t.Fatal("契约过滤器没接在 singleLangRaw 的 sink 装配处（＝delta 侧一个字节都没洗）")
	}
	if !strings.Contains(fn, "cf.Push(") || !strings.Contains(fn, "cf.Flush()") {
		t.Fatal("装配处必须同时有 Push 与 Flush：只 Push 会把「扣住待判定的那几个字节」永久吞掉")
	}
	// 包级实例负向锁（先剥注释：说明注释里合法地写着 NewContractFilter()）。
	// ⚠️ 按**全包非测试文件**扫，不只看 engine.go：把 var 挪进任何一个文件都算同一种坏法，
	//    只盯一个文件的锁等于给"换个文件塞共享实例"留了后门（家族级锁要按全集判）。
	code := stripGoCommentsForGate(src)
	if strings.Contains(code, "NewContractFilter()") && !strings.Contains(code, "cf := NewContractFilter()") {
		t.Fatal("NewContractFilter() 不再出现在任何函数体内＝被提成了包级共享实例")
	}
	for _, name := range packageGoFilesForContract(t) {
		other := stripGoCommentsForGate(readEngineSource(t, name))
		for _, line := range strings.Split(other, "\n") {
			ln := strings.TrimSpace(line)
			if strings.HasPrefix(ln, "var ") && strings.Contains(ln, "NewContractFilter()") {
				t.Fatalf("存在包级共享过滤器实例：%s ⇒ 重试轮次会把整轮正文吞掉（文件 %s）", ln, name)
			}
		}
	}
	// 最终腿不许被这次改动顺手动掉（正向对照：PostProcessTranslation 仍在 singleLangRaw 里）
	if !strings.Contains(fn, "PostProcessTranslation(content, targetLang)") {
		t.Fatal("singleLangRaw 的最终腿 PostProcessTranslation 不见了＝done.reply 也会开始泄漏")
	}
}

// packageGoFilesForContract 列出本包（当前目录）的非测试 .go 文件——接线锁的射程全集。
func packageGoFilesForContract(t *testing.T) []string {
	t.Helper()
	ds, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("列不出本包文件（静态锁失去射程）: %v", err)
	}
	var out []string
	for _, d := range ds {
		n := d.Name()
		if d.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		out = append(out, n)
	}
	if len(out) < 10 {
		t.Fatalf("本包非测试文件数=%d，明显不是全集（判据射程可疑，先修列出逻辑）", len(out))
	}
	return out
}

// functionBodyFromSource 按花括号配平取函数体。
// ⚠️ 本仓老坑：用「下一个顶层声明」当右边界在本仓代码风格下会切过头（多吞后续函数），
// 配平计数才是唯一可靠切法；判"某两行在同一函数里"必须先按函数切。
func contractFilterFnBody(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("找不到函数声明 %q（锚点漂了）", decl)
	}
	// ⚠️ 函数体左括号**不能**用"声明之后的第一个 {"：签名里就带 map[string]interface{} 这种
	//   花括号（本仓大量 handler 都是这个形态），取到它会把函数体切成一对空花括号，
	//   于是所有 mustRef 锚点集体"找不到"——判据不会红在语法上，只会红在"实现没接线"上，
	//   是这类静态锁最容易骗人的一族。正确做法：先跳过参数列表那对圆括号，再取下一个 {。
	// 体左括号 ＝ "圆括号深度回到 0 且已经收过一至少一对圆括号"之后遇到的第一个 {。
	// （接收者那对括号先闭合，参数列表里又藏着 map[string]interface{} 的 {}，
	//   所以只"跳过第一对圆括号"仍然会取错——本仓 handler 声明普遍长这个形状。）
	rest := src[i:]
	parenDepth, sawParen, open := 0, false, -1
	for k := 0; k < len(rest); k++ {
		switch rest[k] {
		case '(':
			parenDepth++
		case ')':
			parenDepth--
			if parenDepth == 0 {
				sawParen = true
			}
		case '{':
			if parenDepth == 0 && sawParen {
				open = k
			}
		}
		if open >= 0 {
			break
		}
	}
	if open < 0 {
		t.Fatalf("%s 之后找不到函数体左括号（声明形态变了）", decl)
	}
	depth := 0
	for p := i + open; p < len(src); p++ {
		switch src[p] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i+open : p+1]
			}
		}
	}
	t.Fatalf("%s 的花括号不配平（切不出函数体）", decl)
	return ""
}

// TestContractHalvesAgreeWithContractTagRe 尺子同源锁：
// 流式过滤器用的开/闭标签两半，必须与整串提取用的 contractTagRe 逐例等值。
// （抄两份判据迟早打架——本仓「多把尺子」那一族的老病根。）
func TestContractHalvesAgreeWithContractTagRe(t *testing.T) {
	opens := []string{"<t>", "<T>", `<t lang="zh">`, "<t >", "<tt>", "<target>", "<ta>", "<t", "<div>", "</t>"}
	for _, o := range opens {
		full := o + "Body</t>"
		if whole, half := contractTagRe.MatchString(full), contractOpenRe.MatchString(o); whole != half {
			// full 里可能同时含闭标签，整串能配上 ⇔ 这半个确实是契约**开**标签
			t.Errorf("开标签两半与整串判据不等值：开段=%q 整串配得上=%v 开段判据=%v", o, whole, half)
		}
	}
	closes := []string{"</t>", "</T>", "</t >", "</t >", "</tt>", "</target>", "</t x>"}
	for _, c := range closes {
		full := "<t>Body" + c
		if whole, half := contractTagRe.MatchString(full), contractCloseRe.MatchString(c); whole != half {
			t.Errorf("闭标签两半与整串判据不等值：闭段=%q 整串配得上=%v 闭段判据=%v", c, whole, half)
		}
	}
}

// TestContractFilterPushIsIncremental 流式形态锁：过滤器必须**边到边吐**，
// 不许攒到 Flush 才一次性给出（否则"流式"退化成一帧，前端的逐字渲染与首字延迟全白做）。
func TestContractFilterPushIsIncremental(t *testing.T) {
	frames := []string{"<t>Hel", "lo wo", "rld</t>"}
	f := NewContractFilter()
	var emitted []string
	for i, c := range frames {
		got := f.Push(c)
		if got == "" && i == 0 && strings.HasPrefix(c, "<t>") {
			t.Fatalf("第一帧已含正文却零输出＝攒流：%q", c)
		}
		if i == 0 && got != "Hel" {
			t.Fatalf("开标签后的首个增量应为 Hel，实得 %q（帧=%q）", got, c)
		}
		emitted = append(emitted, got)
	}
	if last := f.Flush(); last != "" {
		t.Fatalf("闭合标签后不该再有尾帧，实得 %q", last)
	}
	if j := strings.Join(emitted, ""); j != "Hello world" {
		t.Fatalf("增量拼接不符：%q", j)
	}
}
