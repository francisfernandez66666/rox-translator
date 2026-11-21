// ============ md_excess_test.go · 标记过发收敛（P1/G5 加固，真值验收 §13.2 暴露） ============
// 锁定：模型把已带 ** 的骨架再包一层 / 把编号逐字加粗时，按源计数削回，
// " ** 计数守恒、奇数行=0"，且**只删标记、内容逐字节保留**。
package fileproc

import (
	"strings"
	"testing"
)

func TestMdTrimExcessMarkers(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tr   string
		want string
	}{
		{
			name: "标题被再包一层(真值L18形态)",
			src:  `**1\.2 价值测算：效率、成本、订单**`,
			tr:   `****1.2 הערכת העלות****`,
			want: `**1.2 הערכת העלות**`,
		},
		{
			name: "编号逐字加粗(真值L128形态)",
			src:  `### **04  为什么要做**`,
			tr:   `### ****0****4****_canonical**`,
			want: `### **04_canonical**`,
		},
		{
			name: "中间多一对强调(真值诊断形态)",
			src:  `**1\.2 价值测算**`,
			tr:   `**1.2 הערכת** **העלות**`,
			want: `**1.2 הערכת העלות**`,
		},
		{
			name: "正常单对不动",
			src:  `**指标**`,
			tr:   `**מדד**`,
			want: `**מדד**`,
		},
		{
			name: "模型删了标记不在此处补(交给restore)",
			src:  `**指标**`,
			tr:   `מדד`,
			want: `מדד`,
		},
		{
			name: "源无标记时模型加粗属内容增强不动",
			src:  `价值测算`,
			tr:   `**הערכת ערך**`,
			want: `**הערכת ערך**`,
		},
		{
			name: "反引号多塞收敛",
			src:  "`shrink_draft`",
			tr:   "`` shrink_draft ``",
			want: "` shrink_draft `",
		},
		{
			name: "二轮真值:表格cell开闭都双写(源无相邻)",
			src:  `**P0 组件**`,
			tr:   `****מרכיב P****`,
			want: `**מרכיב P**`,
		},
		{
			name: "二轮真值:只有开头双写无闭(计数相同须收敛)",
			src:  `**P0 组件**`,
			tr:   `****מרכיב P`,
			want: `**מרכיב P`,
		},
		{
			name: "二轮真值:三连收敛到一对",
			src:  `**参照方案**`,
			tr:   `*****תוכנית מראה**`,
			want: `**תוכנית מראה**`,
		},
		{
			name: "源本身有相邻标记不得收敛(骨架语义)",
			src:  `**5****\.****4**** 与市场上其他方案**`,
			tr:   `**5****\.****4**** השוואה`,
			want: `**5****\.****4**** השוואה`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mdTrimExcessMarkers(c.src, c.tr)
			if got != c.want {
				t.Errorf("trim 结果不符\n  src=%q\n  tr =%q\n  got =%q\n  want=%q", c.src, c.tr, got, c.want)
			}
			// 不变量：内容（去掉所有标记字符后）不得变少/改变——只删标记不动内容。
			stripMarks := func(s string) string {
				// 收敛会消掉奇数个星里多余的单字符，内容判据须把它也当标记剥离。
				r := strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "", "_", "", "~", "")
				return strings.TrimSpace(r.Replace(s))
			}
			if stripMarks(got) != stripMarks(c.tr) {
				t.Errorf("内容被改动（违反只删标记）: got content=%q, tr content=%q", stripMarks(got), stripMarks(c.tr))
			}
		})
	}
}

// TestMdMarkerRoundTripAcrossModelShapes ★ 2026-11-21 三轮：模型对成对标记的四种形态
// （原样保留 / 只给开头 / 只给结尾 / 全丢）走**真实提取→写回链路**后，最终 `**` 计数必须等于源、
// 且不得出现 `****`。
//
// 为什么必须走整条链：三轮 tv_job3 真值暴露的根因不在单点，而在两段逻辑叠加——
// `engine.stripTrailingSymbolGarbage` 把结尾 `**` 当标点残渣剥掉，`mdRestoreMarkers` 又对
// 奇数缺口两头各加一个 ⇒ `**X` 变成 `****X**`。只测纯函数会各自"看起来对"，串起来才是坏形态。
func TestMdMarkerRoundTripAcrossModelShapes(t *testing.T) {
	srcLine := `## **1.2 价值测算**`
	srcKey := `**1.2 价值测算**`
	cases := []struct{ name, tr string }{
		{"模型原样保留标记", `**1.2 Value Calculation**`},
		{"模型只给开头（被剥尾后的形态）", `**1.2 Value Calculation`},
		{"模型只给结尾", `1.2 Value Calculation**`},
		{"模型全丢标记", `1.2 Value Calculation`},
		{"模型多包一层", `****1.2 Value Calculation****`},
	}
	wantBody := "1.2 Value Calculation"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mdApply(t, srcLine, map[string]string{srcKey: c.tr})
			if n := strings.Count(got, "**"); n != 2 {
				t.Errorf("最终 ** 计数=%d，期望 2（源计数）\n产物=%q", n, got)
			}
			if strings.Contains(got, "****") {
				t.Errorf("产物出现 `****`（过发形态）: %q", got)
			}
			if !strings.Contains(got, wantBody) {
				t.Errorf("内容丢失: %q 不含 %q", got, wantBody)
			}
			if !strings.HasPrefix(got, "## **") || !strings.HasSuffix(got, "**") {
				t.Errorf("标记未正确包裹: %q", got)
			}
		})
	}
}

// TestMdRealDocOddMarkerShapes ★ 2026-11-21 三轮：用 tv_job3 真值产物里那 6 行奇数 `**`
// 的**原始形态**（源行 + 模型输出）走真实写回链路，断言最终标记 token 数与源相等、
// 且不出现源里没有的相邻 `****`。
//
// 这组样本是本轮根因的直接证据：`**P0 组件**` 被模型输出成 `****מרכיב P**`（3 个 token），
// 以及源本身带 `**0****5****`（7 个 token，奇数！）时模型少给一个 → 旧 restore 两头各加一个。
// ⇒ 「奇数行=0」不能当唯一判据：源计数本身可以是奇数，真正的守恒口径是**token 数等于源**。
func TestMdRealDocOddMarkerShapes(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		key   string
		model string
	}{
		{"表格cell被多包一层", `|**环节**|**P0 组件**|**功能**|`, `**P0 组件**`, `****מרכיב P**`},
		{"标题闭标记被吞", `**3\\.4 大模型调用逻辑**`, `**3\\.4 大模型调用逻辑**`, `****3.4 לוגיקת הקריאה של מודלים גדולים**`},
		{"源本身含相邻标记且模型少一个", `**0****5**** 研发预算与选型：自研 vs 供应商**`,
			`**0****5**** 研发预算与选型：自研 vs 供应商**`, `**0****5**** תקציב פיתוח ובחירת מוצרים**`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mdApply(t, c.src, map[string]string{c.key: c.model})
			// ★ 基准必须是**源行**而不是单个键：表格行里还有别的 cell 的标记，
			// 拿键比会把自己算成"产物多出 4 个 token"（首跑真踩，那是判据错不是产品错）。
			wantTok := len(mdMarkerSeq(c.src))
			gotTok := len(mdMarkerSeq(got))
			if gotTok != wantTok {
				t.Errorf("标记 token 数不守恒：源行 %d / 产物 %d\n源=%q\n产物=%q", wantTok, gotTok, c.src, got)
			}
			// 源行不含相邻 `****` 时，产物也不许出现（那是过发形态，不是内容增强）。
			if !strings.Contains(c.src, "****") && strings.Contains(got, "****") {
				t.Errorf("产物出现源里没有的 `****`（过发形态）: %q", got)
			}
		})
	}
}

// TestMdExcessThenRestore 验证双向守恒串联：过发削回 + 缺失补回，最终计数==源。
func TestMdExcessThenRestore(t *testing.T) {
	src := `**1\.2 价值测算：效率、成本、订单**`
	// 模型既多塞了成对标记、又吞了另一个片段的 <br> —— 两次操作应各自按源对齐
	tr := `****1.2 הערכת העלות****`
	out := mdRestoreMarkers(src, mdTrimExcessMarkers(src, tr))
	if n := strings.Count(out, "**"); n != 2 {
		t.Errorf("双向守恒后 ** 计数=%d，期望 2（源计数）: %q", n, out)
	}
	if strings.Count(out, "הערכת העלות") == 0 {
		t.Errorf("内容丢失: %q", out)
	}
}

// TestMdTrimExcessBR ★ 2026-11-21 真值验收（tv_job3/tv_job4）暴露的第二类过发：
// 源文 30 个 `<br>`，产物 32 个——模型多塞的行内换行会把表格 cell 撑出多余一行。
//
// <br> 的删法与成对标记**不同**：它是行内换行，多余那几个必须从**尾部**删（保留前 srcN 个）。
// 从中间删会把同一个 cell 的两段文字直接粘在一起（客户看到的是"两行变一行"）。
//
// 四条腿：
//
//	A 过发从尾部削（保前 2 个，删后 2 个）；
//	B 未过发时一字不动（含 `<br/>`、`<br />` 变体，计数归一但内容不改）；
//	C 源里没有 `<br>` 时不动（同"源无标记时模型加粗属内容增强"那条口径）；
//	D 只删 `<br>` 标记本身，cell 文字逐字节保留。
func TestMdTrimExcessBR(t *testing.T) {
	cases := []struct {
		name string
		src  string
		tr   string
		want string
	}{
		{
			name: "过发从尾部削",
			src:  `甲<br>乙`,
			tr:   `A<br>B<br>C<br>D`,
			// 只删多余的 <br>，文字一个都不丢（删内容＝把客户译文吃掉）。
			want: `A<br>BCD`,
		},
		{
			name: "未过发不动(含变体)",
			src:  `甲<br>乙<br>丙`,
			tr:   `A<br/>B<br />C`,
			want: `A<br/>B<br />C`,
		},
		{
			name: "源无br时不动",
			src:  `甲乙`,
			tr:   `A<br>B`,
			want: `A<br>B`,
		},
		{
			name: "只删标记内容保留",
			src:  `甲<br>乙`,
			tr:   `甲A<br>乙B<br>丙C`,
			want: `甲A<br>乙B丙C`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mdTrimExcessMarkers(c.src, c.tr)
			if got != c.want {
				t.Fatalf("trim 结果不符\n  src=%q\n  tr =%q\n  got =%q\n  want=%q", c.src, c.tr, got, c.want)
			}
		})
	}
}
