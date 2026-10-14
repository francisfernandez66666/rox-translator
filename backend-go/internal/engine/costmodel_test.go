// ============ costmodel_test.go 职责中文说明 ============
// ★ 2026-10-13 用户令⑦（#31）「预估的积分也不准」的专属闸门。
//
// 现网读数（用户截图，同一条气泡上下两处）：
//
//	横幅「预计消耗 ≈30~75 积分（约 60 句）」 vs 实收「本次消耗 1 积分」。
//
// 本文件钉四件事：
//
//	① token 折算按**书写体系**分档（中文 1 字≈1 token、拉丁 4 字符≈1 token），
//	   不再「不分语种一律除一个系数」；
//	② 预估的固定开销**派生自真实提示词**（translateInstruction／outputContractNote／
//	   reviewPromptFmt），改提示词即改预估——静态腿钉「不许写死字面量」，
//	   等值腿钉「必须等于按真实模板算出来的那个数」；
//	③ 量级红线：截图那一条输入（180 个拉丁字符、单语种、pro）的预估必须落在个位数积分档，
//	   旧公式在那一条上出 30~75（≥12,000 真实 token），本锁就是不让它复活；
//	④ 结构红线：**句数不进成本**（同字符数、不同句数的两条输入，预估几乎不变），
//	   而**语种数与段数进成本**（同语种 3 次＝1 次的 3 倍；文件档 10 段 > 1 段）。
//
// ⚠️ 覆盖边界（诚实交代）：本文件测的是**公式本体**（纯函数），不测 HTTP 出参接线；
// 接线那一腿在 scripts/uat/api_uat.sh 的 A6 段（真起服务、真打接口、判 points_min/points_max
// 非零且量级合理）＋ internal/api/estimate_costmodel_gate_test.go（折算与 markup 同源）。
// 两侧合起来才算这条缺陷闭环。
// ========================================
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// screenshotText 用户截图里那条输入（逐字抄，别改标点——"e.g." 与 "600912.SH" 正是
// 旧公式把它数成 8 句的两个锚点）。
const screenshotText = "Root cause visible in the log: the ledger is keyed by req.Code (suffixed, e.g. 600912.SH), my test read the bare code. Fix the test keys to use the same withSuffix derivation"

// TestScriptTokensByScript ① 书写体系两档权重。
func TestScriptTokensByScript(t *testing.T) {
	cjk := ScriptTokens(strings.Repeat("翻", 100))
	latin := ScriptTokens(strings.Repeat("a", 400))
	if cjk != 100 {
		t.Fatalf("100 个汉字应折 100 token（宽体 1:1）, got %d", cjk)
	}
	if latin != 100 {
		t.Fatalf("400 个拉丁字符应折 100 token（窄体 4:1）, got %d", latin)
	}
	// 反向对照：同样 100 个**字符**，中文的 token 必须是英文的 4 倍——
	// 旧公式在这一条上是平的（不分语种），于是中文短文本被低估、英文短文本被高估。
	if ScriptTokens(strings.Repeat("翻", 100)) != 4*ScriptTokens(strings.Repeat("a", 100)) {
		t.Fatalf("同字符数下中文/英文折算必须差 4 倍")
	}
	// 空串折 0（调用方自己兜底成 1，见 EstimateChatTokens 的兜底注释）
	if got := ScriptTokens("   "); got != 1 {
		t.Fatalf("3 个空格应折 1 token（(3+3)/4=1）, got %d", got)
	}
	if got := ScriptTokens(""); got != 0 {
		t.Fatalf("空串必须折 0（兜底 1 是预估腿的事，不是折算函数的）, got %d", got)
	}
}

// TestChatLegFixedTokensDeriveFromRealPrompts ② 等值腿：固定开销必须等于按真实模板算出来的数。
func TestChatLegFixedTokensDeriveFromRealPrompts(t *testing.T) {
	initial, review := ChatLegFixedTokens("", "zh", "zh")
	wantInitial := ScriptTokens(translateInstruction("", "zh", "zh")+outputContractNote("zh")) + ScriptTokens("【原文】")
	if initial != wantInitial {
		t.Fatalf("初翻腿固定开销不再等于真实指令的长度（提示词改了但预估抄了旧字面量？）: got %d want %d", initial, wantInitial)
	}
	wantReview := ScriptTokens(fmt.Sprintf(reviewPromptFmt, config.LangNames["zh"], "", "", "", contractNoteZh()))
	if review != wantReview {
		t.Fatalf("校对腿固定开销不再等于 reviewPromptFmt 的长度: got %d want %d", review, wantReview)
	}
	// 量级哨兵：两条腿的指令开销都在百 token 档。
	// 下界抓「有人把开销算成 0／漏拼一段模板」，上界抓「有人把整份术语表塞进固定开销」。
	for _, tc := range []struct {
		name string
		v    int64
	}{{"initial", initial}, {"review", review}} {
		if tc.v < 50 || tc.v > 2000 {
			t.Fatalf("%s 腿固定开销 %d 超出百 token 量级档（50~2000），请先交代证据", tc.name, tc.v)
		}
	}
	// 语言名查不到时回落代码本身（与 ReviewTranslation 同一取法）：不许因为回落而漏拼模板。
	if _, revXX := ChatLegFixedTokens("", "xx", "zh"); revXX < 50 {
		t.Fatalf("未知语种代码的校对腿开销 %d 不合法（模板仍在位才叫回落）", revXX)
	}
	t.Logf("实测固定开销：初翻腿=%d 校对腿=%d token", initial, review)
}

// costModelLockSpec ②③④ 静态腿的射程表：每个被测函数各点名它「必须引用什么」。
//
// 判据本体在 costModelViolations，**正证与反证喂同一个函数、同一份 mustRef 清单**
// （AGENTS §三「判据级断言代替不了守卫级」：只测判定函数会漏掉真正接线那一处）。
// mustRef 必须非空——空清单会让该锚点只剩负向腿，退化成「什么都算通过」的空转锁。
var costModelLockSpec = []struct {
	anchor  string   // 函数声明锚点（找不到即判「锁失明」）
	mustRef []string // 函数体代码行里必须出现的引用（真实提示词／折算腿）
}{
	{"func ChatLegFixedTokens", []string{"ScriptTokens(", "translateInstruction(", "outputContractNote(", "reviewPromptFmt"}},
	{"func EstimateChatTokens", []string{"ScriptTokens(", "ChatLegFixedTokens("}},
}

// TestCostModelDerivesFromRealPrompts ② 静态腿：两个函数体都不许出现「每句 N token」那类字面量，
// 且必须真的引用真实提示词与书写体系折算。
func TestCostModelDerivesFromRealPrompts(t *testing.T) {
	src := readCostModelSource(t)
	if len(costModelLockSpec) == 0 {
		t.Fatal("射程表为空＝这把锁一行都不判（空清单正锁）")
	}
	for _, sp := range costModelLockSpec {
		if len(sp.mustRef) == 0 {
			t.Fatalf("%s 的必需引用清单为空，负向腿会空转", sp.anchor)
		}
		body := funcBody(src, sp.anchor, t)
		if v := costModelViolations(body, sp.mustRef); len(v) > 0 {
			t.Fatalf("%s 的静态判据不过: %v", sp.anchor, v)
		}
	}
}

// TestEstimateScreenshotCaseMagnitude ③ 量级红线：截图那一条必须落在个位数积分档。
//
// 旧公式在同一条输入上的读数（逐字复算，作为反面对照钉在这里）：
//
//	sents=8（countSentences 把 "e.g."／"600912.SH" 各切一刀）× langs=1 × 2×500 = 8,000 真实 token
//	→ ×markup 1.5 = 12,000 → 30 积分；pro 再 ×2.5 = 30,000 → 75 积分。
//
// 实收读数：1 积分。新公式的下限就是「只走初翻那一腿」，与实收同一量级。
func TestEstimateScreenshotCaseMagnitude(t *testing.T) {
	minTok, maxTok := EstimateChatTokens(screenshotText, []string{"zh"}, "pro", "", "zh")
	// 下限必须远小于旧公式的 12,000（这是缺陷本体的量级），也必须大于 0。
	if minTok < 100 || minTok > 2000 {
		t.Fatalf("截图案的预估下限 %d 不在 100~2000 档（旧公式在这里出 12000）", minTok)
	}
	if maxTok < minTok || maxTok > 6000 {
		t.Fatalf("截图案的预估上限 %d 不在 %d~6000 档（旧公式在这里出 30000）", maxTok, minTok)
	}
	// 积分口径等值：汇率常数从 store 现读（**禁止在本文件抄第二份 400**，
	// 同 §一·11「同一份成本两处读数」那一族；库里可调的那一档由 api 侧闸门覆盖）。
	if p := pointsForTest(minTok); p > 8 {
		t.Fatalf("截图案预估下限折积分 %d 仍在两位数档，缺陷未修（实收=1）", p)
	}
	t.Logf("截图案实测：min=%d max=%d token → ≈%d~%d 积分（实收读数 1 积分，汇率=%d）",
		minTok, maxTok, pointsForTest(minTok), pointsForTest(maxTok), store.DefaultPointsTokensRate)
}

// TestEstimateIgnoresSentenceCount ④ 结构红线：句数不进成本。
//
// 两条输入**长度逐字符相同**（第二条就是把第一条里的 "j" 全换成 "."），
// 只有句末标点密度不同——旧公式在这里差 20 倍。
func TestEstimateIgnoresSentenceCount(t *testing.T) {
	one := strings.Repeat("abcdefghij", 20)   // 200 字符、0 个句末标点
	many := strings.ReplaceAll(one, "j", ".") // 200 字符、20 个句末标点
	if len(one) != len(many) {
		t.Fatalf("两条输入的字符数必须相同才能当等值对照: %d vs %d", len(one), len(many))
	}
	m1, x1 := EstimateChatTokens(one, []string{"en"}, "pro", "", "zh")
	m2, x2 := EstimateChatTokens(many, []string{"en"}, "pro", "", "zh")
	if m1 != m2 || x1 != x2 {
		t.Fatalf("句数渗进了成本（旧公式在这里出 1 句 vs 20 句）: %d/%d vs %d/%d", m1, x1, m2, x2)
	}
	// 反向对照：句末标点密度确实差 20 倍——否则「预估相等」可能只是两条输入本来就一样的空转对照。
	if strings.Count(one, ".") != 0 || strings.Count(many, ".") < 20 {
		t.Fatalf("对照样本失效：句号密度必须 one=0 / many≥20, got %d / %d",
			strings.Count(one, "."), strings.Count(many, "."))
	}
	// 另一条反向对照：同一份样本喂**旧口径**（句数×500）会拉开差距，证明本锁有射程。
	if got := countSentencesForTest(many) * 500; got <= countSentencesForTest(one)*500 {
		t.Fatalf("旧口径对照腿失效：句数读数必须显著不同, got %d vs %d", countSentencesForTest(one), countSentencesForTest(many))
	}
}

// TestEstimateScalesWithLangsAndSegments ④ 结构红线（正向那一半）：语种数与段数确实进成本。
func TestEstimateScalesWithLangsAndSegments(t *testing.T) {
	// 用**同一语种重复三次**做等值锁：zh/en/ja 的提示词长度各不相同，
	// 拿三个不同语种只能判「变大」，判不出「正好 3 倍」。
	// 判据跑在 fast 档：pro 的上限有 `maxTok + maxTok/2` 的整数除法，
	// 3 倍关系在取整上会差 1（那是取整不是结构错，别把它锁成红灯；pro 自己的口径由下面两条判）。
	m1, x1 := EstimateChatTokens(screenshotText, []string{"zh"}, "fast", "", "zh")
	m3, x3 := EstimateChatTokens(screenshotText, []string{"zh", "zh", "zh"}, "fast", "", "zh")
	if m3 != 3*m1 || x3 != 3*x1 {
		t.Fatalf("同语种三次必须是单次的 3 倍（每语种一腿）: %d→%d / %d→%d", m1, m3, x1, x3)
	}
	// pro 档同样随语种线性放大（上限允许整数除法的 ±3 误差）
	p1, q1 := EstimateChatTokens(screenshotText, []string{"zh"}, "pro", "", "zh")
	p3, q3 := EstimateChatTokens(screenshotText, []string{"zh", "zh", "zh"}, "pro", "", "zh")
	if p3 != 3*p1 || abs64(q3-3*q1) > 3 {
		t.Fatalf("pro 档必须随语种线性放大（上限允许取整误差 ±3）: %d→%d / %d→%d", p1, p3, q1, q3)
	}
	// 不同语种之间也必须各算各的指令开销（不许「语种数 × 第一语种开销」）
	mEn, _ := EstimateChatTokens(screenshotText, []string{"en"}, "pro", "", "zh")
	if mEn == m1 {
		t.Fatalf("中英两档的指令开销必须不同（各自按真实提示词算），这里读成同值说明按语种取开销那条腿没跑")
	}
	// 空语种列表不许估成零消耗
	if mn, mx := EstimateChatTokens(screenshotText, nil, "pro", "", "zh"); mn < 1 || mx < 1 {
		t.Fatalf("零语种必须回落成一档非零预估, got %d~%d", mn, mx)
	}
	if mn, _ := EstimateChatTokens("", nil, "pro", "", "zh"); mn < 1 {
		t.Fatalf("空文本＋零语种的下限必须非零（零消耗不许冒充合法读数）, got %d", mn)
	}
	// 文件档：段数进成本（那条链逐段重发指令，段是真成本单位）
	ms1, xs1 := EstimateSegmentTokens(1, []string{"en"}, "pro", "", "zh")
	ms10, xs10 := EstimateSegmentTokens(10, []string{"en"}, "pro", "", "zh")
	// 下限严格 10 倍；上限给 ±10 的整数除法容差（pro 档 `maxTok+maxTok/2` 只在最外层取整一次）
	if ms10 != 10*ms1 || abs64(xs10-10*xs1) > 10 {
		t.Fatalf("10 段必须是 1 段的 10 倍: %d→%d / %d→%d", ms1, ms10, xs1, xs10)
	}
	// 长文本单调：10 倍源文的下限必须显著抬高（防「固定项吃掉一切、字数再长也不变」）
	base := EstimateChatTokensMin(screenshotText)
	mlong, _ := EstimateChatTokens(strings.Repeat(screenshotText, 10), []string{"zh"}, "pro", "", "zh")
	if mlong <= 3*base {
		t.Fatalf("源文 ×10 后预估下限只从 %d 涨到 %d，长文本被固定项吃满", base, mlong)
	}
}

// TestEstimateFastVsPro fast 与 pro 的差别只许出现在上限（重译余量），下限同值。
func TestEstimateFastVsPro(t *testing.T) {
	mf, xf := EstimateChatTokens(screenshotText, []string{"zh"}, "fast", "", "zh")
	mp, xp := EstimateChatTokens(screenshotText, []string{"zh"}, "pro", "", "zh")
	if mf != mp {
		t.Fatalf("下限不该随模式变（两档都要至少跑一腿初翻）: fast=%d pro=%d", mf, mp)
	}
	if xp <= xf {
		t.Fatalf("pro 上限必须高于 fast（质检重译那一腿）: %d vs %d", xp, xf)
	}
	if xp > 3*xf {
		t.Fatalf("pro 上限抬到 fast 的 %d/%d 倍，超出「重译不是每次都跑」的口径（旧公式的 2.5× 就在这里）", xp, xf)
	}
}

// ============ 反证（装回缺陷必须当场红） ============

// TestCostModelCounterEvidence 把三种退化形态喂进**同一个判据函数、同一份 mustRef 清单**：
// 写死字面量／删掉真实模板引用／把句数重新请回成本公式。任一不判红即本锁无射程。
func TestCostModelCounterEvidence(t *testing.T) {
	src := readCostModelSource(t)
	specByAnchor := map[string][]string{}
	for _, sp := range costModelLockSpec {
		specByAnchor[sp.anchor] = sp.mustRef
	}

	// V0：判据函数本身不能对「空body」放行——空射程是历史假绿的第一形态。
	if v := costModelViolations("", specByAnchor["func ChatLegFixedTokens"]); len(v) == 0 {
		t.Fatal("反证 V0 未判红：空函数体必须报出全部缺失引用")
	}

	// V1：把真实指令长度换成写死的 500（旧公式的形态）——必须抓住禁用字面量。
	body1 := funcBody(src, "func ChatLegFixedTokens", t)
	mut1 := strings.Replace(body1, "ScriptTokens(instr)", "int64(500)", 1)
	if mut1 == body1 {
		t.Fatal("反证 V1 未命中锚点（ChatLegFixedTokens 的实现形态变了，请同步本锁）")
	}
	if v := costModelViolations(mut1, specByAnchor["func ChatLegFixedTokens"]); len(v) == 0 {
		t.Fatal("反证 V1 未判红：写死 500 的退化形态必须被静态腿抓住")
	}

	// V2：整段删掉校对模板引用，改成抄一份字数。
	mut2 := strings.Replace(
		body1,
		"rev := fmt.Sprintf(reviewPromptFmt, langName, \"\", \"\", \"\", contractNoteZh())\n\treview = ScriptTokens(rev)",
		"review = int64(320)",
		1)
	if mut2 == body1 {
		t.Fatal("反证 V2 未命中锚点（校对腿的模板引用形态变了，请同步本锁）")
	}
	if v := costModelViolations(mut2, specByAnchor["func ChatLegFixedTokens"]); len(v) == 0 {
		t.Fatal("反证 V2 未判红：删掉真实模板引用后必须判红")
	}

	// V3：把「句数 × 500」请回预估公式（缺陷① 的本体）。
	body3 := funcBody(src, "func EstimateChatTokens", t)
	mut3 := strings.Replace(body3, "src := ScriptTokens(sourceText)",
		"src := int64(countSentencesForTest(sourceText)) * 500", 1)
	if mut3 == body3 {
		t.Fatal("反证 V3 未命中锚点（EstimateChatTokens 的源文折算形态变了，请同步本锁）")
	}
	if v := costModelViolations(mut3, specByAnchor["func EstimateChatTokens"]); len(v) == 0 {
		t.Fatal("反证 V3 未判红：句数重新进成本必须被抓")
	}
}

// ============ 判据本体 ============

// costModelViolations 静态腿的唯一判据函数。
// 参数 body: 被测函数体源码；mustRef: 该函数必须引用的真实提示词／折算腿锚点。
// 返回: 违规清单（空＝通过）。
//
// 两条口径都是本仓真踩出来的：
//   - 判据前先剥 `//` 注释——负向锁误伤说明注释已踩三次（costmodel.go 的文件头注释里
//     就明写着 500／countSentences，不剥就是假红）；
//   - 负向（禁用字面量）与正向（必须引用真实模板）在同一个函数里判，
//     免得「负向恒真、正向没人管」的空转锁复活。
func costModelViolations(body string, mustRef []string) []string {
	code := stripLineComments(body)
	var v []string
	for _, banned := range []string{"500", "1000", "24917", "33222", "countSentences"} {
		if strings.Contains(code, banned) {
			v = append(v, "出现禁用字面量/旧口径读数 "+banned)
		}
	}
	for _, m := range mustRef {
		if !strings.Contains(code, m) {
			v = append(v, "缺少必需引用 "+m)
		}
	}
	return v
}

// stripLineComments 逐行剥掉 `//` 之后的内容（只用于静态腿的判据射程）。
// 参数 s: 源码片段。返回: 只剩代码行的片段。
func stripLineComments(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// ============ 夹具 ============

// readCostModelSource 读本包被测源文件（判「不许写字面量」这类静态腿的唯一事实来源）。
func readCostModelSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", "costmodel.go"))
	if err != nil {
		t.Fatalf("读取 costmodel.go 失败: %v", err)
	}
	return string(b)
}

// funcBody 取某个函数声明之后的第一段函数体（到第一个单独成行的右花括号为止）。
// 锚点找不到即判红——静态腿最怕的就是「锚点漂了、整段静默失明」。
func funcBody(src, anchor string, t *testing.T) string {
	t.Helper()
	i := strings.Index(src, anchor)
	if i < 0 {
		t.Fatalf("源码里找不到 %s（实现改名/搬迁，静态腿已失明）", anchor)
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j > 0 {
		return rest[:j]
	}
	t.Fatalf("%s 的函数体结束锚没找到，判据无法界定射程", anchor)
	return ""
}

// abs64 取绝对值（夹具：上限的整数除法误差判据用）。
func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// pointsForTest 按**出厂档**汇率把 token 折积分（汇率常数取自 store 的唯一事实源，
// 不在本文件抄第二份；取整方向与 store.PointsFromTokens 逐字一致＝四舍五入）。
func pointsForTest(tokens int64) int64 {
	r := store.DefaultPointsTokensRate
	if r <= 0 || tokens <= 0 {
		return 0
	}
	return (tokens + r/2) / r
}

// countSentencesForTest 复刻**改造前**旧公式的「句数」读数，只用于反面对照（证明样本密度差 20 倍）。
// 参数 text: 源文。返回: 按「. ! ? ; 换行」一律切分后的句数。
// ⚠️ 这不是 api.countSentences 的复刻（2026-10-13 那份切分已按上下文重判 ASCII 句点，见
// internal/api/sentence_count.go）。这里刻意把"一律切"的老形态**冻结在本函数内**——
// 对照腿要问的是「旧公式在同样本上会算出多大」，api 侧改了它就得跟着变，那就不是对照而是复述了。
func countSentencesForTest(text string) int64 {
	var n int64 = 1
	for _, r := range text {
		switch r {
		case '\n', '。', '！', '？', '.', '!', '?', ';', '；':
			n++
		}
	}
	return n
}

// EstimateChatTokensMin 取预估下限（夹具：长文本单调那条用例里少写一次返回值解构）。
func EstimateChatTokensMin(text string) int64 {
	m, _ := EstimateChatTokens(text, []string{"zh"}, "pro", "", "zh")
	return m
}
