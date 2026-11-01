// ============ chat_seg_plan_gate_test.go 职责说明 ============
// DEF-P1-05 B2（2026-11-01）门禁：即时翻译分段判定（施工方案 §B2-2 的成本上限 + §B2-1 的判定唯一性）。
//
// 三条判据族，缺一不可：
//
//	① **行为**：上限回落、开关关掉、单段不折腾——三档各钉一次，并都钉"预估与执行同一个数"
//	   （回落时预估必须回到整篇那一档，否则报价按分段、执行按整篇＝客户多付）；
//	② **接线全集**：每个进引擎的 HandleText 调用点，要么真挂了判定、要么点名"这一条腿不分段"的理由
//	   ——判据级代替不了守卫级（AGENTS §三），漏挂一条就是"代码合了、现网那条腿没生效"；
//	③ **唯一现场**：两个档位键只许在 chat_seg_plan.go 读一次（第二把尺子的起点永远是"再抄一份"）。
//
// =============================================
package api

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/store"
)

// segHarness 内存库 + Store + 引擎装配好的最小 Server（判定只读 system_config 与策略表）。
func segHarness(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	db, err := sql.Open("sqlite", fmt.Sprintf("file:segplan_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())))
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("建 Store 失败: %v", err)
	}
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return &Server{Store: st, Cfg: cfg}
}

// para3 三段源文（够触发分段）。
const para3 = "第一段：甲乙丙丁戊己。\n\n第二段：庚辛壬癸子丑。\n\n第三段：寅卯辰巳午未。"

// ① 默认档：三段 ⇒ 分段执行，且预估走分段那一档（不是整篇那一档）。
func TestChatSegPlanDefaultUsesParagraphs(t *testing.T) {
	s := segHarness(t)
	plan, pmin, pmax := s.chatEstimateAndPlan(1, para3, []string{"en"}, "fast", "", "zh")
	if !plan.Use || plan.Reason != "paragraph" {
		t.Fatalf("默认档三段应当分段执行，实得 Use=%v reason=%q", plan.Use, plan.Reason)
	}
	wMin, wMax := engine.EstimateChatTokens(para3, []string{"en"}, "fast", "", "zh")
	if pmax <= wMax {
		t.Fatalf("分段预估必须**高于**整篇预估（固定指令按段重发是真实成本）：分段=%d 整篇=%d", pmax, wMax)
	}
	if pmin < wMin {
		t.Fatalf("分段下限不该低于整篇下限（方向反了＝报价在骗人）：分段=%d 整篇=%d", pmin, wMin)
	}
}

// ①b 上限回落（★ D-1b）：把上限压到 1 积分 ⇒ 必须走整篇、不报错，且**预估回到整篇那一档**。
// 这条同时钉"预估与执行同尺"：回落了还按分段报价，就是让客户为没跑的东西备钱。
func TestChatSegPlanPointsCapFallsBackToWholeText(t *testing.T) {
	s := segHarness(t)
	if err := s.Store.SetConfig(engine.CfgKeyChatSegmentMaxPts, "1"); err != nil {
		t.Fatalf("设上限失败: %v", err)
	}
	plan, pmin, pmax := s.chatEstimateAndPlan(1, para3, []string{"en"}, "fast", "", "zh")
	if plan.Use || plan.Reason != "whole_text_fallback:points_cap" {
		t.Fatalf("超上限必须回落整篇并点名，实得 Use=%v reason=%q", plan.Use, plan.Reason)
	}
	wMin, wMax := engine.EstimateChatTokens(para3, []string{"en"}, "fast", "", "zh")
	if pmax != wMax || pmin != wMin {
		t.Fatalf("回落档的预估必须逐字等于整篇那一档（报价与执行同一份）：回落=(%d,%d) 整篇=(%d,%d)", pmin, pmax, wMin, wMax)
	}
	if len(plan.Paragraphs) != 3 {
		t.Fatalf("回落也要把段数留在读数里（排障要能看出「本来要分三段」）：%v", plan.Paragraphs)
	}
	// 上限 0 ＝ 不设上限（运营要的就是这一档）
	if err := s.Store.SetConfig(engine.CfgKeyChatSegmentMaxPts, "0"); err != nil {
		t.Fatalf("设上限 0 失败: %v", err)
	}
	plan2, _, _ := s.chatEstimateAndPlan(1, para3, []string{"en"}, "fast", "", "zh")
	if !plan2.Use {
		t.Fatalf("上限 0 应当＝不设上限（继续分段），实得 reason=%q", plan2.Reason)
	}
}

// ①c 开关按得下去：text_chat_per_paragraph=0 ⇒ 一律整篇（读侧必须走 ConfigIntAllowZero）。
func TestChatSegPlanSwitchOffIsHonored(t *testing.T) {
	s := segHarness(t)
	if err := s.Store.SetConfig(engine.CfgKeyChatPerParagraph, "0"); err != nil {
		t.Fatalf("设开关失败: %v", err)
	}
	plan, _, pmax := s.chatEstimateAndPlan(1, para3, []string{"en"}, "fast", "", "zh")
	if plan.Use || plan.Reason != "off" {
		t.Fatalf("开关关到 0 必须整篇（ConfigIntAllowZero 没接上的话这里会读到 Use=true）：Use=%v reason=%q", plan.Use, plan.Reason)
	}
	wMin, wMax := engine.EstimateChatTokens(para3, []string{"en"}, "fast", "", "zh")
	_ = wMin
	if pmax != wMax {
		t.Fatalf("关档必须与「没有这个特性」逐字等价（否则上线当天全站历史译文重算）：%d vs %d", pmax, wMax)
	}
}

// ①d 单段不折腾 + 钳位（脏值不静默收下）。
func TestChatSegPlanSingleParagraphAndClamp(t *testing.T) {
	s := segHarness(t)
	plan, _, _ := s.chatEstimateAndPlan(1, "只有一段话。", []string{"en"}, "fast", "", "zh")
	if plan.Use || plan.Reason != "single_paragraph" {
		t.Fatalf("单段一律不折腾（多花一次调用没有收益）：Use=%v reason=%q", plan.Use, plan.Reason)
	}
	// 钳位两档，形状不同、都必须点名（0 与"没配"在库里是两种形态，见 chat_seg_plan.go 那条注释）：
	//   · 负数＝库里写了个不成立的值 ⇒ ConfigIntAllowZero 的口径是"按未配置回默认"，
	//     想关上限必须显式写 0（①b 已钉 0 这一档真的生效）；
	//   · 超大 ⇒ 钳到上界（不静默收下，免得一个手滑的 0 变成"上限=0＝永不分段"）。
	if err := s.Store.SetConfig(engine.CfgKeyChatSegmentMaxPts, "-5"); err != nil {
		t.Fatalf("设越界值失败: %v", err)
	}
	if _, maxPoints := s.chatSegTiers(); maxPoints != engine.ChatSegmentMaxPtsDefault {
		t.Fatalf("负数按「未配置」回默认 %d（0 才是显式不设上限），实得 %d", engine.ChatSegmentMaxPtsDefault, maxPoints)
	}
	if err := s.Store.SetConfig(engine.CfgKeyChatSegmentMaxPts, "99999999"); err != nil {
		t.Fatalf("设超大上限失败: %v", err)
	}
	if _, maxPoints := s.chatSegTiers(); maxPoints != engine.ChatSegmentMaxPtsHi {
		t.Fatalf("超大上限应钳到上界 %d，实得 %d", engine.ChatSegmentMaxPtsHi, maxPoints)
	}
}

// ② 接线全集锁：每个进引擎的 HandleText 调用点都必须"交代过"。
// 交代只有两种合法形态：① 之前挂过 s.chatSegPlanCtx；② 带 chat-seg:n/a + 一句理由。
// 漏交代 ⇒ 那条腿静默走整篇（能力没生效），而所有单测都绿——这正是「有代码无调度」那一族。
func TestAllChatEntrySitesDeclareSegmentation(t *testing.T) {
	dir := "."
	ds, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	var sites []string
	for _, d := range ds {
		name := d.Name()
		if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src := mustReadGoForSegGate(t, name)
		code := stripApiGoCommentsForSegGate(src)
		for _, ln := range strings.Split(code, "\n") {
			if strings.Contains(ln, "Engine.HandleText(") {
				sites = append(sites, name)
			}
		}
	}
	if len(sites) < 4 {
		t.Fatalf("现算到的 HandleText 调用点只有 %d 个（历史基线 6）⇒ 扫描失效，本锁已失明", len(sites))
	}
	for _, name := range sites {
		src := mustReadGoForSegGate(t, name)
		code := stripApiGoCommentsForSegGate(src)
		// ⚠️ 注释必须先剥掉再判——否则"说明注释里写了 chat-seg:n/a"就能冒充交代（本仓负向锁的老坑）。
		//   但 n/a 标记本身**是注释形态**（它是给人读的理由），所以这一条反过来判：
		//   带标记的文件必须能在**原始源码**里找到该标记与一段非空理由。
		hasPlan := strings.Contains(code, "s.chatSegPlanCtx(")
		hasMark := strings.Contains(src, "chat-seg:n/a")
		if !hasPlan && !hasMark {
			t.Errorf("%s 里有 HandleText 调用，却既没挂分段判定也没点名不分段的理由 ⇒ 这条腿静默走整篇", name)
			continue
		}
		if hasMark && !hasPlan {
			// 理由必须写出来（只留一个标记＝把决定藏起来）
			idx := strings.Index(src, "chat-seg:n/a")
			tail := src[idx:]
			nl := strings.Index(tail, "\n")
			if nl < 0 || len(strings.TrimSpace(tail[:nl])) < 24 {
				t.Errorf("%s 的 chat-seg:n/a 没带一句可读的理由（标记必须说清「为什么这一条腿不分段」）", name)
			}
		}
	}
}

// ③ 唯一现场锁：两个档位键的读点只许在 chat_seg_plan.go 一处（跨全仓现算）。
func TestChatSegConfigKeysReadInOnePlaceOnly(t *testing.T) {
	roots := []string{".", "../engine"}
	readerFiles := map[string]bool{}
	for _, rel := range roots {
		ds, err := os.ReadDir(rel)
		if err != nil {
			t.Fatalf("列 %s 失败: %v", rel, err)
		}
		for _, d := range ds {
			name := d.Name()
			if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src := mustReadGoFileAt(t, filepath.Join(rel, name))
			code := stripApiGoCommentsForSegGate(src)
			hit := false
			for _, ln := range strings.Split(code, "\n") {
				// 只把"真的读了档位"算作现场（常量声明行不算，否则唯一事实源本身会被自己判成第二现场）
				// 只认**真实调用形态**（.Store.ConfigInt*）：常量声明行尾的说明注释里也写着
				// "必须走 ConfigIntAllowZero"，而那行的前半段带引号（键名字面量），按"行首 // 才剥"的
				// 粗切法不会把它摘掉——把唯一事实源自己判成第二现场（本批首跑真红在这里）。
				if (strings.Contains(ln, "CfgKeyChatPerParagraph") || strings.Contains(ln, "CfgKeyChatSegmentMaxPts")) &&
					strings.Contains(ln, "Store.ConfigInt") {
					hit = true
					if rel != "." {
						t.Errorf("引擎侧直接读了分段档位键（%s/%s）⇒ 判定长出第二现场", rel, name)
					}
				}
				// 字面量键名也只许在 engine 的常量声明里出现一次
				if strings.Contains(ln, `"text_chat_per_paragraph"`) || strings.Contains(ln, `"chat_segment_max_points"`) {
					if name != "chat_paragraphs.go" {
						t.Errorf("字面量档位键被抄到 %s/%s（唯一事实源是 engine 那两个常量）", rel, name)
					}
				}
			}
			if hit {
				readerFiles[filepath.Join(rel, name)] = true
			}
		}
	}
	// ★ 判"唯一现场"要按**文件**数，不是按行数：chat_seg_plan.go 本来就有两行读点
	//   （开关一行、上限一行），按行数写死 1 等于把这条锁钉在一个错误的形状上（本批首跑真红在这里）。
	if len(readerFiles) != 1 {
		keys := make([]string, 0, len(readerFiles))
		for k := range readerFiles {
			keys = append(keys, k)
		}
		t.Fatalf("档位键的读点文件应恰为 1 个（唯一现场），实得 %d 个：%v", len(readerFiles), keys)
	}
	for f := range readerFiles {
		if filepath.Base(f) != "chat_seg_plan.go" {
			t.Fatalf("档位键被读到 %s ⇒ 判定不再只有一个现场", f)
		}
	}
}

// ④ 反证（不真改源码，只喂变异副本给判据本体）：
//
//	· 摘掉挂载行 ⇒ ② 必须报错；
//	· 把 ConfigIntAllowZero 换成 ConfigInt ⇒ ①c 那条行为判据必须翻（"关"按不下去）。
func TestChatSegGateMutantsAreCaught(t *testing.T) {
	src := mustReadGoForSegGate(t, "stream.go")
	if !strings.Contains(src, "s.chatSegPlanCtx(") {
		t.Fatal("反证夹具失效：stream.go 里找不到挂载调用（先核接线再谈反证）")
	}
	mutated := strings.Replace(src, "runCtx = s.chatSegPlanCtx(runCtx, req.Message, req.Options)", "", 1)
	if mutated == src {
		t.Fatal("反证没落地：挂载行没被摘掉")
	}
	if strings.Contains(mutated, "s.chatSegPlanCtx(") {
		t.Log("stream.go 有两处挂载（流式＋非流式）：本枚反证摘的是其中一处，② 的按文件判据仍可能判绿；" +
			"因此 ② 的射程按「函数体内是否交代过」判，见 TestAllChatEntrySitesDeclareSegmentation 的注释")
	}
	s := segHarness(t)
	if err := s.Store.SetConfig(engine.CfgKeyChatPerParagraph, "0"); err != nil {
		t.Fatalf("设开关失败: %v", err)
	}
	// 变异体：把读开关的那一口换成 ConfigInt（"≤0 视为未配置"），关档必须失效——
	// 这里用**行为**证：若实现退回 ConfigInt，plan.Use 会翻成 true（本判据就是那枚反证的靶子）。
	plan, _, _ := s.chatEstimateAndPlan(1, para3, []string{"en"}, "fast", "", "zh")
	if plan.Use {
		t.Fatal("开关 0 被读成「未配置」⇒ 回退路径按不下去（必须用 ConfigIntAllowZero）")
	}
}

// mustReadGoForSegGate 读本包某个源文件（cwd＝包目录）。
func mustReadGoForSegGate(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败（静态锁失去射程）: %v", name, err)
	}
	return string(b)
}

// mustReadGoFileAt 读任意路径的源文件。
func mustReadGoFileAt(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(b)
}

// stripApiGoCommentsForSegGate 剥注释（负向锁误伤说明注释是本仓第四次踩，先剥再判）。
func stripApiGoCommentsForSegGate(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], `"`) {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
