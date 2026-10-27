// ============ estimate_costmodel_gate_test.go 职责中文说明 ============
// ★ 2026-10-13 用户令⑦（#31）「预估的积分也不准」的 **api 侧接线** 闸门。
//
// engine/costmodel_test.go 测的是公式本体（纯函数），本文件测那一段公式怎么进接口：
//
//	A 预估 markup 与扣费 markup 走**同一个解析口** modeMarkup（含模式因子优先），
//	  并用静态腿钉「全 api 包只有 modeMarkup 读 eff.MarkupMultiplier」——
//	  这条缺陷的族名就叫「同一份成本两处读数」（AGENTS §一·11），只修当前这一处不算闭环。
//	B applyMarkup 的兜底方向＝不低于原值（与 ChargeUsageRealtime 里 billed<total 按 total 计同向）：
//	  宁可多估一厘，也不许让客户按预估备钱后备不够（那会把「预估不准」换成「翻译到一半停服」）。
//	C estimateTokens 按请求形态选腿，且**逐字等于** engine 那两个函数的同参返回值（薄委托，不另算一遍）。
//	D 接口端到端：截图那条输入 → points_min 落在个位数积分档；出参键集合与改造前逐字相同
//	  （前端零改动这条要靠键名集合成立，不靠"看着没改"）；长文显著抬档（防恒零假成功）。
//	E 静态腿：handler 里不许残留旧公式的锚点（2×rate／×2.5／句数进算式）。
//
// ⚠️ 单测自钉 sqlite 方言（AGENTS §一·4）：夹具复用 est_tokens_config_test.go 的
// newEstCfgFixture（同一包内已有「内存 SQLite＋两枚 JWT」的最小栈，不造第二份）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestEstimate
// ==========================================
package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/billing"
	"translator/internal/engine"
	"translator/internal/ops"
)

// estScreenshotText 用户截图里那条输入（与 engine 侧同名常量逐字一致——
// 两侧都要在同一个案上量出同一个数，改成不同文本就等于两把尺子）。
const estScreenshotText = "Root cause visible in the log: the ledger is keyed by req.Code (suffixed, e.g. 600912.SH), my test read the bare code. Fix the test keys to use the same withSuffix derivation"

// estMux 预估接口路由（与生产注册同 handler、同路径）。
func (s *Server) estMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/translation/estimate", s.handleTranslationEstimate)
	return m
}

// A modeMarkup：全局值与模式因子的优先序，只有一份算法
func TestEstimateModeMarkupSingleSource(t *testing.T) {
	base := ops.EffectivePolicy{MarkupMultiplier: 1.5}
	if got := modeMarkup(base, "pro"); got != 1.5 {
		t.Fatalf("无模式因子时应取策略全局值 1.5, got %v", got)
	}
	withRule := ops.EffectivePolicy{
		MarkupMultiplier: 1.5,
		ModeRules:        map[string]ops.ModeRule{"fast": {Enabled: true, Charge: true, Markup: 1.2}},
	}
	if got := modeMarkup(withRule, "fast"); got != 1.2 {
		t.Fatalf("模式因子必须优先于全局值（否则运营给 fast 单独调档在读侧不生效）, got %v", got)
	}
	// 反向对照①：没配该模式因子 → 回落全局值（不许读成 0 把成本算成免费）
	if got := modeMarkup(withRule, "pro"); got != 1.5 {
		t.Fatalf("未配因子的模式应回落全局值, got %v", got)
	}
	// 反向对照②：因子写成 0 ＝「没表态」，不是「打零折」
	zeroRule := ops.EffectivePolicy{
		MarkupMultiplier: 1.5,
		ModeRules:        map[string]ops.ModeRule{"pro": {Enabled: true, Charge: true, Markup: 0}},
	}
	if got := modeMarkup(zeroRule, "pro"); got != 1.5 {
		t.Fatalf("Markup=0 必须视为未表态（回 0＝把整条链算成零成本）, got %v", got)
	}
	// 静态腿：全 api 包非测试文件里，eff.MarkupMultiplier 只许在 modeMarkup 函数体内出现一次
	violations := markupReadPointViolations(t)
	if len(violations) > 0 {
		t.Fatalf("markup 解析口不止一处（§一·11 同一份成本两处读数）: %v", violations)
	}
}

// B applyMarkup 的兜底方向
func TestEstimateApplyMarkupNeverBelowRealTokens(t *testing.T) {
	if got := applyMarkup(1000, 1.5); got != 1500 {
		t.Fatalf("1.5 档应 1500, got %d", got)
	}
	// 系数异常（<1）时按原值计——与扣费腿同向兜底
	if got := applyMarkup(1000, 0.5); got != 1000 {
		t.Fatalf("系数 0.5 必须兜回原值 1000, got %d", got)
	}
	if got := applyMarkup(1000, 0); got != 1000 {
		t.Fatalf("系数 0 必须兜回原值（零消耗不许冒充合法读数）, got %d", got)
	}
	if got := applyMarkup(0, 1.5); got != 0 {
		t.Fatalf("零 token 输入应原样回零（保底 1 积分是出参那一腿的事）, got %d", got)
	}
	// 反证形态：把兜底方向反过来（billed>tokens 才取 billed）会读成 0
	if reverseApplyMarkupForTest(1000, 0.5) != 500 {
		t.Fatal("反证夹具未复刻退化形态")
	}
}

// C estimateTokens 选腿＝逐字委托给 engine（本文件不重算公式，只问「有没有拿这一份公式」）
func TestEstimateTokensDelegatesToEngine(t *testing.T) {
	s := &Server{}
	// 有正文 → 即时翻译那一腿
	m, x := s.estimateTokens(estimateReq{Text: estScreenshotText, TargetLangs: []string{"zh"}, Mode: "pro", UILang: "zh"}, "pro")
	em, ex := engine.EstimateChatTokens(estScreenshotText, []string{"zh"}, "pro", "", "zh")
	if m != em || x != ex {
		t.Fatalf("接口与引擎必须同值（这里是第二处算式）: %d/%d vs %d/%d", m, x, em, ex)
	}
	// 只有段数 → 文件/工单那一腿
	m2, x2 := s.estimateTokens(estimateReq{SegmentCount: 12, TargetLangs: []string{"en"}, Mode: "fast"}, "fast")
	em2, ex2 := engine.EstimateSegmentTokens(12, []string{"en"}, "fast", "", "")
	if m2 != em2 || x2 != ex2 {
		t.Fatalf("段数档必须逐字委托: %d/%d vs %d/%d", m2, x2, em2, ex2)
	}
	// 正文优先级：同时给了 text 与 segment_count 时按正文算（前端工作台就是这一形态）
	m3, _ := s.estimateTokens(estimateReq{Text: estScreenshotText, SegmentCount: 9999, TargetLangs: []string{"zh"}, Mode: "pro"}, "pro")
	if m3 != m {
		t.Fatalf("有正文时必须走正文那一腿, got %d vs %d", m3, m)
	}
	// ui_lang 必须真的进公式（中英文提示词是两套，长度不同）
	mZH, _ := s.estimateTokens(estimateReq{Text: estScreenshotText, TargetLangs: []string{"zh"}, Mode: "pro", UILang: "zh"}, "pro")
	mEN, _ := s.estimateTokens(estimateReq{Text: estScreenshotText, TargetLangs: []string{"zh"}, Mode: "pro", UILang: "en"}, "pro")
	if mZH == mEN {
		t.Fatal("ui_lang 没参与计算：英文界面访客会看到按中文提示词量出的区间")
	}
}

// D 端到端：截图那条输入 → 个位数积分档；出参键集合不变；长文显著抬档
func TestEstimateHandlerResponseShapeAndMagnitude(t *testing.T) {
	f := newEstCfgFixture(t)
	f.srv.Bill = billing.NewService(f.st)
	h := f.srv.estMux()

	code, resp := doJSON(t, h, http.MethodPost, "/api/translation/estimate", f.tenant, map[string]interface{}{
		"text": estScreenshotText, "target_langs": []string{"zh"}, "mode": "pro", "ui_lang": "zh",
	})
	if code != 200 {
		t.Fatalf("预估应 200, got %d %+v", code, resp)
	}
	if ok, _ := resp["success"].(bool); !ok {
		t.Fatalf("预估应 success=true, got %+v", resp)
	}
	pmin, _ := resp["points_min"].(float64)
	pmax, _ := resp["points_max"].(float64)
	// ★ 假成功判据（§一·3「成功类判据要同时问内容体非空」同口径）：信封之外还问非零
	if pmin < 1 || pmax < pmin {
		t.Fatalf("出参积分读数非法: min=%v max=%v", resp["points_min"], resp["points_max"])
	}
	// 缺陷本体红线：旧公式在这一条出 30~75（现网截图读数）。
	// ★ 2026-10-24 N-01：文化规则块 allowance（zh 上限 2000 token）计入固定项后下限约 9 积分；
	//   仍是个位数档，且远低于旧公式 30~75。
	if pmin > 12 {
		t.Fatalf("截图那条输入的预估下限 %v 积分超出量级档（旧公式 30~75）——缺陷未修", pmin)
	}
	// 出参键集合与改造前逐字相同（前端零改动这条判据成立的地方就在键名）
	want := []string{"success", "sentences", "points_min", "points_max", "cost_sentences_approx",
		"points_balance", "balance_sentences_approx", "sufficient", "activated", "hint", "llm_queue_depth"}
	for _, k := range want {
		if _, ok := resp[k]; !ok {
			t.Fatalf("出参缺键 %s（前端读它，改造承诺是「零改动」）: %+v", k, resp)
		}
	}
	for k := range resp {
		if !strings.HasSuffix(k, "_tokens") && k != "balance_tokens" && k != "tokens_min" && k != "tokens_max" {
			continue
		}
		t.Fatalf("出参出现 token 裸值 %s（§一·5 积分口径：公开接口零 token 裸值）", k)
	}
	// sentences 读数仍在（展示用），但不再参与积分：同一文本换 8 语种 → 积分随语种涨，读数不动
	_, respOne := doJSON(t, h, http.MethodPost, "/api/translation/estimate", f.tenant, map[string]interface{}{
		"text": estScreenshotText, "target_langs": []string{"zh"}, "mode": "pro", "ui_lang": "zh",
	})
	_, respMany := doJSON(t, h, http.MethodPost, "/api/translation/estimate", f.tenant, map[string]interface{}{
		"text": estScreenshotText, "target_langs": []string{"zh", "zh", "zh", "zh", "zh", "zh", "zh", "zh"}, "mode": "pro", "ui_lang": "zh",
	})
	if got, _ := respMany["sentences"].(float64); got != pminSentences(respOne) {
		t.Fatalf("sentences 是纯读数，不该随语种数变: %v vs %v", got, pminSentences(respOne))
	}
	if pm, _ := respMany["points_min"].(float64); pm <= pmin {
		t.Fatalf("八语种的预估下限必须显著高于单语种: %v vs %v", pm, pmin)
	}
	// 长文本必须抬档（防「固定项吃掉一切」，也防接口恒回一个数）
	long := strings.Repeat(estScreenshotText, 40)
	_, respLong := doJSON(t, h, http.MethodPost, "/api/translation/estimate", f.tenant, map[string]interface{}{
		"text": long, "target_langs": []string{"zh"}, "mode": "pro", "ui_lang": "zh",
	})
	if pl, _ := respLong["points_min"].(float64); pl <= pmin {
		t.Fatalf("40 倍源文的预估没抬档（%v vs %v）——公式或接线已退化", pl, pmin)
	}
	t.Logf("截图那条：min=%v max=%v 积分／ sentences 读数=%v（旧公式在此出 30~75，实收 1）",
		pmin, pmax, resp["sentences"])
}

// E 静态腿：handler 与选腿函数里不许残留旧公式锚点，且必须真的引用 engine 公式
func TestEstimateHandlerNoOldFormula(t *testing.T) {
	src := readEstimateSource(t)
	// 判据本体在 estimateFormulaWiringViolations，正证与反证喂同一个函数
	// （AGENTS §三「判据级断言代替不了守卫级」：只测判定函数会漏掉真正接线那一处）。
	if v := estimateFormulaWiringViolations(t, src); len(v) > 0 {
		t.Fatalf("预估接线判据不过: %v", v)
	}
	// 反证：把 handler 换回旧公式（句数 × 每句系数 × 轮数 × 2.5）必须当场判红
	degenerate := strings.Replace(
		src,
		"minTokens, maxTokens := s.estimateTokens(req, mode)",
		"sents := countSentences(req.Text)\n\tbase := int64(2*rate) * sents\n\tminTokens, maxTokens := base, int64(float64(base)*2.5)",
		1)
	if degenerate == src {
		t.Fatal("反证未命中锚点（handler 的选腿调用形态变了，请同步本锁）")
	}
	if v := estimateFormulaWiringViolations(t, degenerate); len(v) == 0 {
		t.Fatal("反证未判红：装回旧公式的形态必须被抓")
	}
	// 反证②：把 engine 委托整段摘掉（换成自己算）也必须判红——
	// 这一枚抓的是「有人在接口里另写一份公式」，与① 抓的旧公式同族不同形。
	// ★ 2026-10-28：estimateTokens 现在先算两腿再叠加 OCR 判定 allowance（图片件），
	//   委托形态从 `return engine.X(...)` 变成 `minTokens, maxTokens = engine.X(...)`；
	//   反证锚点必须跟着形态改，否则这条反证会退化成"锚点不存在"（本锁自己会判 A 类红）。
	unhooked := strings.Replace(src, "minTokens, maxTokens = engine.EstimateChatTokens(req.Text, req.TargetLangs, mode, \"\", req.UILang)",
		"minTokens, maxTokens = int64(len(req.Text)), int64(len(req.Text)) * 2", 1)
	if unhooked == src {
		t.Fatal("反证② 未命中锚点（estimateTokens 的委托形态变了，请同步本锁）")
	}
	if v := estimateFormulaWiringViolations(t, unhooked); len(v) == 0 {
		t.Fatal("反证② 未判红：接口里自算一份公式必须被抓")
	}
}

// ============ 判据本体与夹具 ============

// estimateFormulaWiringViolations 预估接线的静态判据唯一本体。
// 参数 t: 失败定位；src: estimate.go 全文（正证传当前磁盘内容，反证传装回缺陷的副本）。
// 返回: 违规清单（空＝通过）。
//
// 射程＝两个函数体的**代码行**（注释先剥掉：本仓已三次踩在「负向锁误伤说明注释」上，
// 而 estimate.go 的文件头注释里就明写着旧公式的读数）。
// 两个函数分开点名，是因为「谁调谁」本身就是接线的一部分：
// handler 只许调 s.estimateTokens（选腿），公式只在 engine 里，接口只负责折算与出参。
func estimateFormulaWiringViolations(t *testing.T, src string) []string {
	t.Helper()
	var v []string
	type spec struct {
		anchor  string
		banned  []string
		mustRef []string
	}
	specs := []spec{
		{
			anchor:  "func (s *Server) handleTranslationEstimate",
			banned:  []string{"2*rate", "* 2.5", "countSentences(req.Text)"},
			mustRef: []string{"s.estimateTokens(req, mode)", "modeMarkup(", "PointsFromTokens(", "atLeastOne("},
		},
		{
			anchor:  "func (s *Server) estimateTokens",
			banned:  []string{"countSentences", "* 2.5", "2*rate"},
			mustRef: []string{"engine.EstimateChatTokens", "engine.EstimateSegmentTokens"},
		},
	}
	for _, sp := range specs {
		body, ok := estFuncBodyOf(src, sp.anchor)
		if !ok {
			v = append(v, "找不到 "+sp.anchor+"＝锚点漂了、这把锁已失明")
			continue
		}
		code := stripCommentsForGate(body)
		for _, b := range sp.banned {
			if strings.Contains(code, b) {
				v = append(v, sp.anchor+" 残留旧公式锚点 "+b)
			}
		}
		for _, m := range sp.mustRef {
			if !strings.Contains(code, m) {
				v = append(v, sp.anchor+" 缺少必需引用 "+m)
			}
		}
	}
	return v
}

// markupReadPointViolations 全 api 包（非测试文件）里「读策略算好的 markup」的读点清单：
// 除 modeMarkup 函数体内那一次，其余一律算「第二把尺子」。
//
// 判据只问 `eff.MarkupMultiplier` 这一种写法（不是宽松的 `.MarkupMultiplier`），
// 并把**赋值形态**排除在外——ops_api.go 那句 `eff.MarkupMultiplier = f` 是策略解析器
// 往 EffectivePolicy 里**写**（构造期），不是成本读点；
// admin_packages.go 那几处读的是请求结构体字段 `req.MarkupMultiplier`（管理台入参），
// 与本锁无关，用精确锚点天然排除。
func markupReadPointViolations(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读 api 包目录失败: %v", err)
	}
	var v []string
	hits := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		code := stripCommentsForGate(string(b))
		inModeMarkup := name == "billing_api.go" && strings.Contains(code, "func modeMarkup")
		for _, line := range strings.Split(code, "\n") {
			l := strings.TrimSpace(line)
			i := strings.Index(l, "eff.MarkupMultiplier")
			if i < 0 {
				continue
			}
			// 赋值给策略字段（解析器写侧）不算成本读点
			rest := strings.TrimLeft(l[i+len("eff.MarkupMultiplier"):], " \t")
			if strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==") {
				continue
			}
			hits++
			if !inModeMarkup {
				v = append(v, name+": "+l)
			}
		}
	}
	if hits == 0 {
		v = append(v, "全包找不到 eff.MarkupMultiplier 读点＝锚点漂了、这把锁已失明")
	}
	body, ok := estFuncBodyOf(readBillingAPISource(t), "func modeMarkup")
	if !ok {
		v = append(v, "找不到 modeMarkup＝markup 解析口已被改名/删除，静态锁失明")
	} else if !strings.Contains(stripCommentsForGate(body), "eff.MarkupMultiplier") {
		v = append(v, "modeMarkup 自己没读 eff.MarkupMultiplier（解析口空转）")
	}
	return v
}

// reverseApplyMarkupForTest 反证夹具：复刻「兜底方向写反」的退化形态（billed 只在更大时才取）。
// 参数 tokens: 原值；markup: 系数。返回: 退化算法的读数。
func reverseApplyMarkupForTest(tokens int64, markup float64) int64 {
	billed := int64(float64(tokens) * markup)
	if billed > tokens {
		billed = tokens
	}
	return billed
}

// pminSentences 读 resp 里的 sentences 读数（夹具，少写一次类型断言）。
func pminSentences(resp map[string]interface{}) float64 {
	v, _ := resp["sentences"].(float64)
	return v
}

// readEstimateSource 读被测接口源文件（静态腿的唯一事实来源）。
func readEstimateSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", "estimate.go"))
	if err != nil {
		t.Fatalf("读取 estimate.go 失败: %v", err)
	}
	return string(b)
}

// readBillingAPISource 读扣费源文件（markup 解析口的所在文件）。
func readBillingAPISource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", "billing_api.go"))
	if err != nil {
		t.Fatalf("读取 billing_api.go 失败: %v", err)
	}
	return string(b)
}

// estFuncBodyOf 取函数声明到第一个单独成行的右花括号之间的源码。
// 参数 src: 文件全文；anchor: 函数声明锚点。返回: (函数体, 是否命中)。
//
// 找不到**不判红**而是回 ok=false，由调用方（判据函数）把它记成一条违规——
// 静态腿最常见的死法是「锚点漂了、整段静默失明」，必须把它变成可见的红灯。
func estFuncBodyOf(src, anchor string) (string, bool) {
	i := strings.Index(src, anchor)
	if i < 0 {
		return "", false
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j > 0 {
		return rest[:j], true
	}
	return "", false
}

// stripCommentsForGate 逐行剥掉 // 之后的内容：负向锁误伤说明注释本仓已踩三次，
// 判据只问代码行（estimate.go 与 billing_api.go 的中文注释里就写着旧公式的读数）。
func stripCommentsForGate(s string) string {
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
