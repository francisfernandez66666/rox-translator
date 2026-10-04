// ============================================================================
// model_for_stage_test.go — ★ R-1 修法 C 的取模咽喉断言（A4，2026-10-04）。
//
// 钉住的历史缺陷：单语腿（singleLangRaw）只有「本阶段配了就用」一档，缺了批量腿
// 一直有的第二档「本阶段没配 ⇒ 退回初翻阶段」。现网形态是"运营只在 ai_initial 上
// 配过一份可用端点"（管理台默认就这么用），于是 kb_match / review 这些阶段
// 会绕开那份配置去拨全局默认端点；而 R-1 期间全局 Key 恰是随机占位符 ⇒
// 这几条腿表现为「面板上是绿的、调用必 401」的静默失败。
//
// 本文件同时钉住修法 C 的另一半：**两条腿必须走同一个咽喉**。
// 判据是源码级的（BatchTranslate 段里不许再出现 resolveStageModel 直调），
// 因为"抄两份、改一份漏一份"正是这个缺陷的成因，只测行为挡不住它复发。
//
// 反证：删掉 resolveModelForStage 里 ai_initial 那一档 ⇒ A4 红；
//
//	把批量腿改回自己写一遍 resolveStageModel ⇒ 咽喉锁红。
//
// 方言口径（AGENTS §一·4）：本测试族固定内存 SQLite。
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// setStageModels 把一批阶段配置写进测试库（明文 Key：resolveStageModel 走
// store.DecryptSecret，而 DecryptSecret 对历史明文原样放行，与生产兼容口径一致）。
func setStageModels(t *testing.T, st *store.Store, m config.StageModels) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("序列化 stage_models 失败: %v", err)
	}
	if err := st.SetConfig("stage_models", string(raw)); err != nil {
		t.Fatalf("写入 stage_models 失败: %v", err)
	}
}

// TestSingleLangFallsBackToAIInitialStage = 断言 A4。
// 库里只配 ai_initial，请求 stage=kb_match / review ⇒ 取模结果必须是 ai_initial 那一份，
// 且 stageActive=true（下游据此把降级链的主路也换成这一份，而不是全局那份）。
func TestSingleLangFallsBackToAIInitialStage(t *testing.T) {
	st := newTestStore(t)
	setStageModels(t, st, config.StageModels{
		config.StageAIInitial: {Provider: "ai_initial", APIBase: "https://initial.example/v1", APIKey: "sk-initial", Model: "initial/Model"},
	})
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = "https://global.example/v1" // 全局腿：本用例里它代表"R-1 期间那条死腿"
	cfg.OnlineAPIKey = "sk-global"
	cfg.OnlineModel = "global/Model"
	e := &Engine{St: st, Cfg: cfg}
	ctx := context.Background()

	for _, stage := range []string{config.StageKBMatch, config.StageReview, config.StageEvals} {
		base, key, model, active := e.resolveModelForStage(ctx, stage)
		if !active {
			t.Fatalf("阶段 %s 应退回 ai_initial 并标 stageActive=true（否则降级链主路仍按全局算）", stage)
		}
		if base != "https://initial.example/v1" || key != "sk-initial" || model != "initial/Model" {
			t.Fatalf("阶段 %s 没退回初翻配置：base=%s key=%s model=%s", stage, base, key, model)
		}
		// 决定性负向：全局那条腿一次都不许被选中
		if base == cfg.OnlineAPIBase || key == cfg.OnlineAPIKey {
			t.Fatalf("阶段 %s 仍在打全局腿：%s/%s", stage, base, key)
		}
	}

	// 本阶段自己有配置时，初翻档不许越权（优先序：本阶段 ＞ 初翻 ＞ 全局）
	setStageModels(t, st, config.StageModels{
		config.StageAIInitial: {APIBase: "https://initial.example/v1", APIKey: "sk-initial", Model: "initial/Model"},
		config.StageReview:    {APIBase: "https://review.example/v1", APIKey: "sk-review", Model: "review/Model"},
	})
	base, key, model, active := e.resolveModelForStage(ctx, config.StageReview)
	if !active || base != "https://review.example/v1" || key != "sk-review" || model != "review/Model" {
		t.Fatalf("本阶段配置被初翻档越权：base=%s key=%s model=%s active=%v", base, key, model, active)
	}

	// 一档都没配 ⇒ 才回落到全局（正向对照，防"把回退写成永远命中 ai_initial"）
	if err := st.SetConfig("stage_models", "{}"); err != nil {
		t.Fatalf("清空 stage_models 失败: %v", err)
	}
	base, key, model, active = e.resolveModelForStage(ctx, config.StageKBMatch)
	if active || base != cfg.OnlineAPIBase || key != cfg.OnlineAPIKey || model != cfg.OnlineModel {
		t.Fatalf("未配任何阶段时应回落全局，实际 base=%s key=%s model=%s active=%v", base, key, model, active)
	}
}

// TestStageModelResolutionHasSingleThroat 咽喉唯一性（源码级，派生式判据）。
// 引擎里"给一次翻译取模"这件事只许有 resolveModelForStage 一个咽喉：
// 单语腿与批量腿各自手写一遍 resolveStageModel 的回退，就是 R-1 里
// "批量腿是好的、单语腿是坏的"这种半修形态的成因。
//
// 判据不钉「直调总数」（那是清单式锁：包里有五条语义不同的合法直调——
// Embed 端点覆盖与审校族要的是"换一个模型"，不是"退回初翻"——
// 把它们按数量压成 2 会在任何一次合法改动后假红，也会在新死腿加进来时钝化）。
// 换成三条派生等值锁：
//
//	① ai_initial 那一档回退在全包只许出现一次，且必须在咽喉函数体内
//	   ——「抄一份回退」正是本缺陷的成因，这一条抓的是成因本身；
//	② 两条翻译腿（singleLangRaw / BatchTranslate）函数体内：咽喉恰好 1 次、直调恰好 0 次；
//	③ 越喉直调的函数名单必须与下面逐条写明理由的白名单**集合相等**
//	   （多一个＝新腿绕过咽喉，少一个＝白名单腐烂成摆设，两个方向都判红）。
func TestStageModelResolutionHasSingleThroat(t *testing.T) {
	// 射程＝engine 包全部非测试源文件（只扫 engine.go 会漏掉 file.go / kb_screen.go
	// 这类"新腿长在别的文件"的形态，而那正是本缺陷的复发路径）。
	funcs, text := readPackageFuncs(t)

	// ① 回退档唯一性：`e.resolveStageModel(ctx, config.StageAIInitial)` 全包恰好 1 处，且在咽喉里
	const aiInitialFallback = "e.resolveStageModel(ctx, config.StageAIInitial)"
	if n := strings.Count(text, aiInitialFallback); n != 1 {
		t.Fatalf("ai_initial 回退档在全包非测试源里出现 %d 次（应恰好 1 次、只在 resolveModelForStage 内）"+
			"⇒ 有腿自己抄了一遍回退，或回退档被删掉", n)
	}
	throat, ok := funcs["resolveModelForStage"]
	if !ok {
		t.Fatalf("找不到咽喉函数 resolveModelForStage ⇒ 咽喉被改名或删除")
	}
	if !strings.Contains(throat, aiInitialFallback) {
		t.Fatalf("ai_initial 回退档不在咽喉函数体内 ⇒ 咽喉已不承接「本阶段没配 ⇒ 退回初翻」这一档")
	}

	// ② 两条翻译腿都必须走咽喉，且不许自己直调阶段配置
	for _, name := range []string{"singleLangRaw", "BatchTranslate"} {
		body, ok := funcs[name]
		if !ok {
			t.Fatalf("找不到翻译腿函数 %s ⇒ 被改名或删除，本锁射程失效", name)
		}
		if n := strings.Count(body, "e.resolveModelForStage("); n != 1 {
			t.Fatalf("%s 应恰好调用咽喉 1 次，实际 %d 次", name, n)
		}
		if n := strings.Count(body, "e.resolveStageModel("); n != 0 {
			t.Fatalf("%s 里出现 %d 次 resolveStageModel 直调 ⇒ 绕过咽喉自己写回退（R-1 半修形态的成因）", name, n)
		}
	}

	// ③ 越喉直调白名单（集合相等）。每条都写清"为什么它不该走翻译咽喉"：
	//    新加直调必须先在这里登记理由，没登记即判红。
	allowedBypass := map[string]string{
		// Embed 端点覆盖：取的是向量模型（kb_embed），与"翻译用哪个模型"不是一个问题，
		// 退回初翻那份对话模型会把 Embed 请求打到 chat 端点上。
		"translateOneInner":       "kb_embed：语义检索的 Embed 端点覆盖，不属翻译取模",
		"RebuildKBIndex":          "kb_embed：重建索引的 Embed 端点覆盖，不属翻译取模",
		"HandleFile":              "kb_embed：文件翻管线预取向量的 Embed 端点覆盖，不属翻译取模",
		"translateWithFeedbackEx": "审校族：要的是「换一个模型挑错」，不是「退回初翻那份模型」（同模型自审＝复读）",
		"ReviewTranslation":       "审校族：同上",
		"ReviewTranslationBatch":  "审校族：同上",
		"screenEntryBatch":        "kb_screen：企业包行业化筛查的判分模型，自带专属阶段键，不是翻译腿",
	}
	var got []string
	for name, body := range funcs {
		if name == "resolveModelForStage" || name == "resolveStageModel" {
			continue // 咽喉自身与直读实现不算越喉
		}
		if strings.Count(body, "e.resolveStageModel(") > 0 {
			got = append(got, name)
		}
	}
	sortStrings(got)
	var want []string
	for name := range allowedBypass {
		want = append(want, name)
	}
	sortStrings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("越喉直调函数集合与白名单不等：实得 [%s]，白名单 [%s] ⇒ 前者多出的必须走咽喉或登记理由，后者说明白名单已腐烂",
			strings.Join(got, " "), strings.Join(want, " "))
	}
	// 白名单不许是空摆设：每条都得真在文件里存在（改名式破坏会同时躲过"实得集合"与旧名单）
	for name, reason := range allowedBypass {
		if _, ok := funcs[name]; !ok {
			t.Fatalf("白名单里的 %s 在全包非测试源中不存在（理由：%s）⇒ 名单腐烂，请同步清理", name, reason)
		}
		if reason == "" {
			t.Fatalf("白名单条目 %s 没写理由 ⇒ 清单必须自带「为什么合法」，否则下次会被整段删掉", name)
		}
	}
}

// readPackageFuncs 读 engine 包全部非测试 .go 源，返回「函数名 → 函数体」映射与拼接全文。
// 测试文件必须排除：本锁钉的是产品代码的取模形态，把断言自己写的那串字面量算进去
// 会让计数恒偏（例如本文件里的 aiInitialFallback 常量就正好等于被锁的那次调用）。
func readPackageFuncs(t *testing.T) (map[string]string, string) {
	t.Helper()
	names, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	funcs := map[string]string{}
	var all strings.Builder
	n := 0
	for _, d := range names {
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", d.Name()))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", d.Name(), err)
		}
		text := string(src)
		all.WriteString(text)
		all.WriteString("\n")
		for name, body := range splitTopLevelFuncBodies(t, text) {
			if _, dup := funcs[name]; dup {
				t.Fatalf("函数名 %s 在包里出现两次 ⇒ 本锁的「函数体归属」判据失去意义，请改用 AST 作用域", name)
			}
			funcs[name] = body
		}
		n++
	}
	if n == 0 {
		t.Fatalf("没扫到任何非测试源文件 ⇒ 判据恒空，属白锁")
	}
	return funcs, all.String()
}

// splitTopLevelFuncBodies 按「行首 func 」把源文件切成函数体（key = 函数名）。
// 只服务本文件的源码级锁：本包所有函数都在第 0 列声明、结束大括号也在第 0 列，
// 且没有行首的嵌套函数声明，所以粗切即可，不需要真正的 AST。
func splitTopLevelFuncBodies(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	lines := strings.Split(text, "\n")
	var name string
	var buf strings.Builder
	flush := func() {
		if name != "" {
			out[name] = buf.String()
			buf.Reset()
		}
	}
	for _, ln := range lines {
		if strings.HasPrefix(ln, "func ") {
			flush()
			name = funcNameOf(ln)
			if name == "" {
				t.Fatalf("无法从声明行解析函数名: %s", ln)
			}
			buf.WriteString(ln)
			buf.WriteString("\n")
			continue
		}
		if name != "" {
			buf.WriteString(ln)
			buf.WriteString("\n")
		}
	}
	flush()
	return out
}

// funcNameOf 从一行 func 声明里取出函数名（去掉接收者与 package 限定，参数之前为止）。
func funcNameOf(decl string) string {
	rest := strings.TrimPrefix(decl, "func ")
	if strings.HasPrefix(rest, "(") { // 带接收者的方法：func (e *Engine) Name(...)
		if i := strings.Index(rest, ") "); i >= 0 {
			rest = rest[i+2:]
		}
	}
	if i := strings.IndexByte(rest, '('); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// sortStrings 就地排序（本测试族不引 sort 包以外的依赖，保持文件自解释）。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
