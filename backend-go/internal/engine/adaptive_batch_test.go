package engine

import (
	"strings"
	"sync"
	"testing"
)

// `adaptiveBatchSize` 的判据 —— ★ 2026-10-28 计价与成本优化批（方案 §5.2）。
//
// 为什么这一条要有独立的锁：批大小过去是写死的 15／10，改成分批器之后**没有任何读数能看出它坏了**
// ——批太小只是"多花固定指令开销"（成本涨、界面照常），批太大只是"偶尔顶爆上下文"（个别段回落原文）。
// 两种坏形态都不报错、不进闸门，所以只能在这里用**可计算的输入**把边界钉住。
//
// 三个维度分开测（段数上限／token 预算／边界兜底），别揉成一条"大致差不多"：
// 揉在一起时，把 maxSegs 写死 40、把 token 支摘掉，这条锁照样绿。

// TestAdaptiveBatchSizeShortSegmentsFillToMaxSegs 短行文档：token 预算远没到，按段数上限走。
// 表格/字幕这类"一行几个字"的输入如果只给十几段一批，固定指令开销会被重复几十次（成本批要治的就是它）。
func TestAdaptiveBatchSizeShortSegmentsFillToMaxSegs(t *testing.T) {
	texts := make([]string, 50)
	for i := range texts {
		texts[i] = "abcd" // ScriptTokens=1，50 段合计 50 token ≪ 6000
	}
	if got := adaptiveBatchSize(texts, 40, 6000); got != 40 {
		t.Fatalf("短行文档应吃满段数上限 40，实际 %d ⇒ 分批器没按段数上限收口（固定指令开销会被重复几十次）", got)
	}
	// 段数比上限少时，上限就是全部（不许多吐）。
	if got := adaptiveBatchSize(texts[:7], 40, 6000); got != 40 {
		t.Fatalf("段数不足上限时应返回上限 40（由调用方按 len 切片），实际 %d", got)
	}
}

// TestAdaptiveBatchSizeLongSegmentsNarrowByTokens 长段落：按源文 token 预算收窄。
// 这里刻意让预算成为**唯一**的约束（段数上限 40 远大于结果），否则测不出 token 支是否还在。
func TestAdaptiveBatchSizeLongSegmentsNarrowByTokens(t *testing.T) {
	mk := func(n int) []string {
		out := make([]string, 10)
		for i := range out {
			out[i] = strings.Repeat("a", n)
		}
		return out
	}
	// 800 个 ASCII 字符 = 200 token ⇒ 6000/200 = 30 段。
	if got := adaptiveBatchSize(mk(800), 40, 6000); got != 30 {
		t.Fatalf("长段落应按 token 预算收窄到 30，实际 %d ⇒ token 支被摘掉或公式改了（长段落会顶爆上下文）", got)
	}
	// 10,000 字符 = 2,500 token ⇒ 6000/2500 = 2 段。
	if got := adaptiveBatchSize(mk(10000), 40, 6000); got != 2 {
		t.Fatalf("超长段落应收到 2 段，实际 %d ⇒ 预算换算口径变了", got)
	}
	// 反过来把预算调大，同一批要能放回上限：证明收窄确实来自预算这一维，不是别处写死的数。
	if got := adaptiveBatchSize(mk(800), 40, 100000); got != 40 {
		t.Fatalf("预算放大后应回到段数上限 40，实际 %d ⇒ 30 那个读数不是预算算出来的", got)
	}
}

// TestAdaptiveBatchSizeNeverBelowOne 单段就超预算：仍必须给 1 段。
// 返回 0 会让调用方切出空批（`texts[i:i+0]`），那是"这一整批一段都不翻"的死循环形态——
// 比批小贵得多，所以 1 是底线，不是"差不多就行"。
func TestAdaptiveBatchSizeNeverBelowOne(t *testing.T) {
	huge := []string{strings.Repeat("a", 100000)} // 25,000 token ≫ 6,000 预算
	if got := adaptiveBatchSize(huge, 40, 6000); got != 1 {
		t.Fatalf("单段超预算应兜底为 1，实际 %d ⇒ 调用方会切出空批（整批不翻）", got)
	}
	// 参数脏值同一条底线：maxSegs/maxTokens 传 0 或负数不许返回 0。
	if got := adaptiveBatchSize(huge, 0, 0); got < 1 {
		t.Fatalf("段数/token 上限传脏值时仍须 ≥1，实际 %d", got)
	}
}

// TestAdaptiveBatchSizeEmptySegmentsAreFree 空段/纯空白段不占额度。
// 逐行翻译链会把"原文里的空行"当段送进来：它们不该把批压小（否则一整篇带空行的文档批批都变短）。
func TestAdaptiveBatchSizeEmptySegmentsAreFree(t *testing.T) {
	texts := []string{"", "   ", "\n", "\t \n"}
	if got := adaptiveBatchSize(texts, 40, 6000); got != 40 {
		t.Fatalf("空/空白段应零成本，实际批=%d ⇒ 空行把批压小了（成本会被空行放大）", got)
	}
	// 空切片（没有任何段）也要给上限，不许 0。
	if got := adaptiveBatchSize(nil, 40, 6000); got != 40 {
		t.Fatalf("空切片应返回段数上限 40，实际 %d ⇒ 调用方会在空批上打转", got)
	}
}

// TestAdaptiveBatchSizeConcurrentIsPure 并发调用无共享写（`-race` 下必须干净）。
//
// 分批器是**纯函数**：这一条同时钉住"不许为了省算力在包级加缓存"——
// 一旦有人加个 map 缓存而不加锁，线上表现是偶发 fatal error（concurrent map read and map write），
// `recover` 兜不住、进程直接挂（AGENTS §三 那条）。
func TestAdaptiveBatchSizeConcurrentIsPure(t *testing.T) {
	cases := []struct {
		texts []string
		want  int
	}{
		{make([]string, 50), 40}, // nil 串＝0 token
		{[]string{strings.Repeat("a", 800)}, 30},
		{[]string{strings.Repeat("a", 100000)}, 1},
		{[]string{"", "  "}, 40},
	}
	for i := range cases[0].texts {
		cases[0].texts[i] = "abcd"
	}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c := cases[n%len(cases)]
			if got := adaptiveBatchSize(c.texts, 40, 6000); got != c.want {
				t.Errorf("并发第 %d 次读数漂了：got=%d want=%d ⇒ 分批器不是纯函数（或加了未加锁的缓存）", n, got, c.want)
			}
		}(i)
	}
	wg.Wait()
}

// TestAdaptiveBatchSizeBoundsThreeDims 射程自证：三条支各自都能改变结果。
// 只测"结果在 1..40 之间"等于什么都没测——那种判据把实现改成 `return 40` 也照样绿。
func TestAdaptiveBatchSizeBoundsThreeDims(t *testing.T) {
	texts := []string{strings.Repeat("a", 800), strings.Repeat("a", 800)}
	readings := map[string]int{
		"baseline":    adaptiveBatchSize(texts, 40, 6000),   // 30（预算定）
		"segs_cap_2":  adaptiveBatchSize(texts, 2, 6000),    // 2（段数上限定）
		"budget_wide": adaptiveBatchSize(texts, 40, 100000), // 40（预算放宽）
	}
	if readings["baseline"] != 30 || readings["segs_cap_2"] != 2 || readings["budget_wide"] != 40 {
		t.Fatalf("三维读数应分别是 30/2/40，实际 %v ⇒ 至少一维不再是判据的一部分（实现被简化成常量了）",
			[]int{readings["baseline"], readings["segs_cap_2"], readings["budget_wide"]})
	}
	for name, n := range readings {
		if n < 1 {
			t.Fatalf("%s 的批大小为 %d（空批＝整批不翻）⇒ 下限兜底被摘掉了", name, n)
		}
	}
}
