// ============ file_writepoints_guard_test.go 职责说明 ============
// 文件主链「5 个译文写入点必须同一口径」的装配级回归（2026-09-23 评审 §4.1-5 / 任务 #56 补齐）。
//
// 与 translation_guard_test.go 的分工：那个文件测**判定函数本身**（IsTranslationUsable /
// collectKBPass / missingSegments 的输入输出），本文件测**装配关系**——
// 即 file.go 的每一个写 langTranslations 的点是否真的接在判定函数后面。
// 这正是历史缺陷的形态：判定逻辑一直存在且被单测覆盖，但 ① KB 直配命中写入点手写
// 「只判非空」，于是 TM 里的脏行（源文=译文）被当命中写进成品，且键一存在就让硬闸
// 「按键存在=已译出」永久跳过该段（不重试、不计 untranslated、不告警）。
// 单测口径修好了，装配漏一处照样复发——所以这里用 AST 扫源码把「接线」钉住：
//   - TestFileTranslationWritePointsAllGuarded：任何新增的 addTrans 写入点若没过
//     IsTranslationUsable（或不是 S8 占位预填这一豁免），本用例直接红；
//   - TestCollectKBPassRoutesInstructionEchoToRepair：指令回显候选（源文无 '<' 而候选含 '<'）
//     与脏行同口径——不算命中、进补漏队列、收尾计入未译清单；
//   - TestApplySegmentGates*：文件主路径质量闸门「只告警不丢译文」，且重翻结果同样要过判定，
//     S8 占位段完全不进闸门。
//
// 本文件不做任何真实 LLM/网络调用（上游一律 httptest 假服务器），不落 PostgreSQL。
// ==========================================================
package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/llm"
)

// ---------- 写入点口径 ----------

// fwpExpectedWritePoints file.go 中「向 langTranslations 写译文」的调用点数量基线。
// 钉这个数字不是为了惩罚改动，而是**强制改动者显式表态**：新增第 6 个写入点必须
// 同时改这里并保证它带判定；删掉一个写入点也要改这里。历史上口径就是被「多写一处
// 但忘了带判定」破坏的，而那种改动不会让任何既有测试变红。
const fwpExpectedWritePoints = 5

// fwpSensitiveFile 唯一豁免文件：S8 敏感拦截段要**直接预填占位交付**（占位符不是译文，
// 过判定反而会被「同文/空串」规则拦掉），因此 sensitive.go 的裸写入是设计内的。
const fwpSensitiveFile = "sensitive.go"

type fwpSite struct {
	file    string
	line    int
	guarded string // 命中的豁免/判定形态，空串表示无守护
}

// fwpScanWritePoints 用 AST 扫出 engine 包内所有 addTrans(...) 调用及其守护形态。
// 三种合法形态：
//  1. 祖先 if 的条件里调用了 IsTranslationUsable（写入前判定，覆盖模型/兜底 4 个写入点）；
//  2. 所在 for-range 的迭代源是 collectKBPass(...) 的返回值（KB 命中写入点，判定收口在纯函数内）；
//  3. 第三个实参是字面量 SensitivePlaceholderText（S8 占位预填豁免）。
func fwpScanWritePoints(t *testing.T, filenames []string) []fwpSite {
	t.Helper()
	var sites []fwpSite
	for _, name := range filenames {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 %s 失败（文件名变了还是语法坏了？）: %v", name, err)
		}
		var stack []ast.Node
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1] // ast.Inspect 以 nil 回调标记子树退出，栈即祖先链
				return false
			}
			stack = append(stack, n)
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "addTrans" || len(call.Args) != 3 {
				return true
			}
			sites = append(sites, fwpSite{
				file:    name,
				line:    fset.Position(call.Pos()).Line,
				guarded: fwpGuardOf(stack),
			})
			return true
		})
	}
	return sites
}

// fwpGuardOf 在祖先链上找守护形态（栈自内而外遍历，就近优先）。
func fwpGuardOf(stack []ast.Node) string {
	// 豁免 3：占位预填（实参字面量）
	last := stack[len(stack)-1]
	if call, ok := last.(*ast.CallExpr); ok {
		if id, ok := call.Args[2].(*ast.Ident); ok && id.Name == "SensitivePlaceholderText" {
			return "S8 占位预填"
		}
	}
	for i := len(stack) - 1; i >= 0; i-- {
		switch nd := stack[i].(type) {
		case *ast.IfStmt:
			if fwpUses(nd.Cond, "IsTranslationUsable") {
				return "IsTranslationUsable"
			}
		case *ast.RangeStmt:
			// KB 命中写入点：迭代 collectKBPass 返回的 accepted 切片。
			// 守护不在这个 if 里（判定收口在纯函数内部），故按数据流回溯：
			// range 的迭代变量必须由 `accepted, _ := collectKBPass(...)` 的**第一个**返回值赋得。
			if id, ok := nd.X.(*ast.Ident); ok && fwpAssignedFromCollect(stack, id.Name) {
				return "collectKBPass"
			}
		}
	}
	return ""
}

// fwpAssignedFromCollect 回溯栈上最近的函数体，检查 name 是否由 collectKBPass 的
// **第一个返回值 accepted**（已过判定）赋得。
// 为什么必须区分返回值：collectKBPass 的第二个返回值 needModelIdx 是「待补漏队列」，
// 里面正是判定不通过、还要送模型的段——按它裸写等于把门槛整个绕过（历史缺陷的翻版）。
func fwpAssignedFromCollect(stack []ast.Node, name string) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		var body *ast.BlockStmt
		switch fn := stack[i].(type) {
		case *ast.FuncLit:
			body = fn.Body
		case *ast.FuncDecl:
			body = fn.Body
		}
		if body == nil {
			continue
		}
		hit := false
		ast.Inspect(body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			if len(as.Rhs) == 1 && fwpUses(as.Rhs[0], "collectKBPass") && len(as.Lhs) > 0 {
				if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == name {
					hit = true
				}
			}
			return !hit
		})
		if hit {
			return true
		}
	}
	return false
}

// fwpUses 判断节点子树内是否出现指定名字的函数调用。
func fwpUses(node ast.Node, fn string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok && fwpCallNamed(ce, fn) {
			found = true
		}
		return !found
	})
	return found
}

func fwpCallNamed(ce *ast.CallExpr, fn string) bool {
	id, ok := ce.Fun.(*ast.Ident)
	return ok && id.Name == fn
}

// TestFileTranslationWritePointsAllGuarded 钉住「写入门槛只有一处定义」这条装配约束。
// 改坏了会怎样：任何人在 file.go 里加一个 `addTrans(lc, src, out)` 而没先过
// IsTranslationUsable（例如照抄老写法 `if out != ""`），脏行/回显就会重新流进成品文件，
// 且该段被硬闸永久跳过——本用例会直接列出未守护的 file:line。
func TestFileTranslationWritePointsAllGuarded(t *testing.T) {
	sites := fwpScanWritePoints(t, []string{"file.go", fwpSensitiveFile})
	inFile := 0
	byKind := map[string]int{}
	for _, s := range sites {
		if s.file == fwpSensitiveFile {
			if s.guarded != "S8 占位预填" {
				t.Errorf("%s:%d 的裸写入必须写 SensitivePlaceholderText 占位（唯一豁免形态），got %q", s.file, s.line, s.guarded)
			}
			continue
		}
		inFile++
		if s.guarded != "IsTranslationUsable" && s.guarded != "collectKBPass" {
			t.Errorf("file.go:%d 写入 langTranslations 未经译文可用性判定（guarded=%q）：必须走 IsTranslationUsable 或 collectKBPass，否则脏行会静默交付并永久锁死硬闸", s.line, s.guarded)
		}
		byKind[s.guarded]++
		t.Logf("写入点 file.go:%d 守护形态=%s", s.line, s.guarded)
	}
	if inFile != fwpExpectedWritePoints {
		t.Fatalf("file.go 译文写入点数量基线被改动：实际 %d，期望 %d（基线清单：%v）。新增/删除写入点请同步更新 fwpExpectedWritePoints 并说明理由",
			inFile, fwpExpectedWritePoints, sites)
	}
	// 反向自证：扫描器必须真的认出两类守护形态（各 4 处 / 1 处），
	// 否则「全部判为未守护」与「全部判为已守护」两种坏掉都能让上面的断言假绿。
	if byKind["IsTranslationUsable"] != fwpExpectedWritePoints-1 || byKind["collectKBPass"] != 1 {
		t.Fatalf("写入点守护形态分布与既有装配不符：%v（期望 IsTranslationUsable %d 处 + collectKBPass 1 处）",
			byKind, fwpExpectedWritePoints-1)
	}
}

// TestFileWritePointScannerDetectsUnguardedWrite 自证扫描器不是「永远返回已守护」的假绿灯：
// 用一段合成源码喂给它，内含三种真实违规形态，必须全部判为未守护。
// 没有这条，上面那条断言「全部已守护」的用例可能只是检测器坏掉在 vacuous pass。
func TestFileWritePointScannerDetectsUnguardedWrite(t *testing.T) {
	const sample = `package engine

func handle() {
	addTrans("en", srcA, outA) // 违规1：完全裸写
	if outB != "" && outB != srcB {
		addTrans("en", srcB, outB) // 违规2：旧的「只判非空/不等源文」口径
	}
	_, need := collectKBPass(texts, hit, val, blocked)
	for _, i := range need {
		addTrans("en", texts[i], val[i]) // 违规3：迭代 collectKBPass 的第二个返回值（补漏队列）却裸写
	}
	accepted, _ := collectKBPass(texts, hit, val, blocked)
	for _, i := range accepted {
		addTrans("en", texts[i], val[i]) // 合规：判定收口在 collectKBPass 内
	}
	if IsTranslationUsable(srcC, outC) {
		addTrans("en", srcC, outC) // 合规：写入前判定
	}
	addTrans("en", t, SensitivePlaceholderText) // 合规：S8 占位预填豁免
}
`
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("写样本: %v", err)
	}
	sites := fwpScanWritePoints(t, []string{path})
	if len(sites) != 6 {
		t.Fatalf("样本应有 6 个 addTrans 调用，实际 %d", len(sites))
	}
	want := []string{"", "", "", "collectKBPass", "IsTranslationUsable", "S8 占位预填"}
	for i, s := range sites {
		if s.guarded != want[i] {
			t.Errorf("第 %d 处（%s:%d）守护形态判定为 %q，期望 %q", i+1, s.file, s.line, s.guarded, want[i])
		}
	}
}

// TestAddTransClosureStaysRaw 钉住 addTrans 本身**不做**判定（裸写入器契约）。
// 为什么反向也要钉：如果把判定塞进 addTrans，调用点的 `if` 分支与 kbHits/modelHits 计数
// 就会与实际写入脱钩（计数说命中、实际没写），且 S8 占位预填会被判定拦掉 ⇒ 拦截段丢失交付。
func TestAddTransClosureStaysRaw(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "file.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("解析 file.go: %v", err)
	}
	var closure *ast.FuncLit
	var decl ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
			if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == "addTrans" {
				if fl, ok := as.Rhs[0].(*ast.FuncLit); ok {
					closure = fl
					decl = as
				}
			}
		}
		return true
	})
	if closure == nil {
		t.Fatal("未找到 addTrans 闭包定义（被改名或内联了？本用例需同步调整）")
	}
	if fwpUses(closure.Body, "IsTranslationUsable") {
		t.Error("addTrans 内部不应做可用性判定：判定属于写入点职责，否则命中计数与实际写入会脱钩，且 S8 占位预填会被误拦")
	}
	if fwpUses(decl, "collectKBPass") {
		t.Error("collectKBPass 不应出现在 addTrans 定义里（职责错位）")
	}
}

// ---------- 指令回显在装配层的归属 ----------

// TestCollectKBPassRoutesInstructionEchoToRepair 指令回显候选与脏行同口径：
// KB/模型给的候选若「源文无 '<' 而译文凭空含 '<'」，① 不算命中（不写 langTranslations）、
// ② 进模型补漏队列（还要重试）、③ 补漏仍未译出时计入未译清单（人工可定位）。
// 与 translation_guard_test.go 的 TestCollectKBPassRejectsDirtyRow 互补：那条钉「同文回显」，
// 本条钉指令残留分支——改坏了会出现「交付物里躺着 <Only output the final translated text…>」。
func TestCollectKBPassRoutesInstructionEchoToRepair(t *testing.T) {
	echo := "<Only output the final translated text, enclosed entirely within  and nothing else>"
	texts := []string{
		"The system matches the built-in automotive database", // 候选=指令回显
		"支持多格式文件翻译",                                           // 候选=正常译文
	}
	kbHitIdx := []bool{true, true}
	kbVal := []string{echo, "Multi-format file translation is supported"}

	accepted, needModelIdx := collectKBPass(texts, kbHitIdx, kbVal, nil)
	if len(accepted) != 1 || accepted[0] != 1 {
		t.Fatalf("指令回显候选不得算命中：accepted=%v，want [1]", accepted)
	}
	if len(needModelIdx) != 1 || needModelIdx[0] != 0 {
		t.Fatalf("指令回显候选必须进模型补漏队列：needModelIdx=%v，want [0]", needModelIdx)
	}

	// 补漏仍失败（模型稳定吐回显）后的收尾统计：该段必须出现在未译清单里。
	tr := map[string]string{}
	for _, i := range accepted {
		tr[texts[i]] = kbVal[i]
	}
	missing := missingSegments(texts, tr)
	if len(missing) != 1 || missing[0] != texts[0] {
		t.Fatalf("指令回显段必须计入未译清单，got %q", missing)
	}
	// 关键反向断言：未写入的键必须真的不存在（硬闸「按键存在判定已译出」依赖这条）。
	if _, leaked := tr[texts[0]]; leaked {
		t.Fatalf("回显段被写进了 langTranslations（键存在 ⇒ 硬闸永久跳过该段）：%q", tr[texts[0]])
	}
}

// ---------- 文件主路径质量闸门：只告警、不丢译文、重翻也要过判定 ----------

// fwpGateHarness 假上游 + Engine 装配（不落 DB：St=nil 时术语/阶段模型/文化规则自动降级）。
type fwpGateHarness struct {
	eng *Engine
	mu  sync.Mutex
	// replies 按调用顺序返回模型回复；耗尽后回落到 last 值
	replies []string
	calls   int
}

func (h *fwpGateHarness) next() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	i := h.calls
	h.calls++
	if i < len(h.replies) {
		return h.replies[i]
	}
	if len(h.replies) > 0 {
		return h.replies[len(h.replies)-1]
	}
	return ""
}

func (h *fwpGateHarness) hitCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

func fwpGateEngine(t *testing.T, replies ...string) *fwpGateHarness {
	t.Helper()
	h := &fwpGateHarness{replies: replies}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 回复含引号/反斜杠需转义后回塞，故走 strconv.Quote 而非裸拼
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`,
			strconv.Quote(h.next()))
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言：防 UAT 矩阵的 PG env 泄漏进本包（AGENTS.md §4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	h.eng = &Engine{LLM: llm.NewClient(cfg), Cfg: cfg}
	return h
}

// fwpBadEN 含数字源文 + 丢数字的译文 ⇒ 约束闸门「数字保持」必不通过（稳定可复现的违规样本）。
const (
	fwpSrcBad = "本设备支持 45 种工作模式，请在维护周期内校准"
	fwpTrBad  = "This device supports various working modes, please calibrate in time"
	fwpTrOK   = "This device supports 45 working modes; please calibrate within the maintenance cycle"
)

// TestApplySegmentGatesWarnsButNeverDropsTranslation 文件主路径闸门的核心语义：
// 违规译文以**警告**透出，但绝不从 langTranslations 里删除、也不置空。
// 改坏了会怎样：若有人把「警告」改成「删除该段」，成品文件会出现整段空白（比错误译文更糟），
// 且 untranslated 统计与交付内容会互相矛盾。
func TestApplySegmentGatesWarnsButNeverDropsTranslation(t *testing.T) {
	h := fwpGateEngine(t, fwpTrBad) // 重翻仍给出同样违规的译文
	lt := map[string]map[string]string{"en": {fwpSrcBad: fwpTrBad}}

	warnings := h.eng.applySegmentGates(context.Background(), lt, true)
	if len(warnings) == 0 {
		t.Fatal("违规译文必须以警告透出（静默放行=闸门形同不存在）")
	}
	joined := ""
	for _, w := range warnings {
		joined += w + "|"
	}
	if !strings.Contains(joined, "数字保持") {
		t.Errorf("警告应指出未通过的约束项，got %q", joined)
	}
	m, ok := lt["en"]
	if !ok || len(m) != 1 {
		t.Fatalf("闸门不得删除段键，got %v", lt)
	}
	if strings.TrimSpace(m[fwpSrcBad]) == "" {
		t.Fatalf("闸门不得把译文置空：违规段仍需交付 + 人工补译，got %q", m[fwpSrcBad])
	}
	if h.hitCount() == 0 {
		t.Error("retry=true 时首轮违规应带反馈重翻一次（文件交付物必须保证数字/格式正确）")
	}
}

// TestApplySegmentGatesAdoptsOnlyGateCleanRethranslation 重翻结果的采纳门槛：
// 只有「过闸」的修正才覆写；模型给出空串或仍违规时保留原译文并保留警告。
// 改坏了会怎样：无条件覆写会把可交付的（虽有瑕疵的）译文换成更烂的串（空串=整段空白）。
func TestApplySegmentGatesAdoptsOnlyGateCleanRethranslation(t *testing.T) {
	t.Run("修正合格则覆写", func(t *testing.T) {
		h := fwpGateEngine(t, fwpTrOK)
		lt := map[string]map[string]string{"en": {fwpSrcBad: fwpTrBad}}
		if ws := h.eng.applySegmentGates(context.Background(), lt, true); len(ws) != 0 {
			t.Fatalf("重翻后已过闸，不应再留警告：%v", ws)
		}
		if lt["en"][fwpSrcBad] != fwpTrOK {
			t.Fatalf("已过闸的修正应覆写原译文，got %q", lt["en"][fwpSrcBad])
		}
	})
	t.Run("修正为空则保留原译文", func(t *testing.T) {
		h := fwpGateEngine(t, "   ")
		lt := map[string]map[string]string{"en": {fwpSrcBad: fwpTrBad}}
		ws := h.eng.applySegmentGates(context.Background(), lt, true)
		if lt["en"][fwpSrcBad] != fwpTrBad {
			t.Fatalf("模型空回复不得覆写已交付内容，got %q", lt["en"][fwpSrcBad])
		}
		if len(ws) == 0 {
			t.Error("仍未过闸必须留警告（供审批/QA 定位）")
		}
	})
	t.Run("修正仍是源文回显则保留原译文", func(t *testing.T) {
		h := fwpGateEngine(t, fwpSrcBad) // 中文回显：非源语言项必不过 ⇒ 不采纳
		lt := map[string]map[string]string{"en": {fwpSrcBad: fwpTrBad}}
		h.eng.applySegmentGates(context.Background(), lt, true)
		if lt["en"][fwpSrcBad] != fwpTrBad {
			t.Fatalf("回显/未过闸的修正不得写回交付物，got %q", lt["en"][fwpSrcBad])
		}
	})
}

// TestApplySegmentGatesSkipsSensitivePlaceholder S8 拦截占位段不进质量闸门：
// 既不调用模型（上游零暴露承诺），也不产生「未通过质量校验」噪音警告。
// 改坏了会怎样：占位符被送去重翻 = 把拦截内容回灌上游；或每段拦截都刷一条警告淹没真问题。
func TestApplySegmentGatesSkipsSensitivePlaceholder(t *testing.T) {
	h := fwpGateEngine(t, fwpTrOK)
	lt := map[string]map[string]string{"en": {"违规待拦截段": SensitivePlaceholderText}}
	if ws := h.eng.applySegmentGates(context.Background(), lt, true); len(ws) != 0 {
		t.Fatalf("拦截占位段不应产生闸门警告：%v", ws)
	}
	if got := lt["en"]["违规待拦截段"]; got != SensitivePlaceholderText {
		t.Fatalf("拦截占位段内容必须原样交付，got %q", got)
	}
	if n := h.hitCount(); n != 0 {
		t.Fatalf("拦截占位段不得触发任何模型调用（上游零暴露），实际调用 %d 次", n)
	}
}

// TestApplySegmentGatesCleanTranslationNoLLMCall 合规译文零成本通过：不得为过闸段调模型。
// 钉的是成本红线——大文件逐段重翻会把 token 打爆（retry 只在违规时触发）。
func TestApplySegmentGatesCleanTranslationNoLLMCall(t *testing.T) {
	h := fwpGateEngine(t, fwpTrOK)
	lt := map[string]map[string]string{"en": {fwpSrcBad: fwpTrOK}}
	if ws := h.eng.applySegmentGates(context.Background(), lt, true); len(ws) != 0 {
		t.Fatalf("合规译文不应产生警告：%v", ws)
	}
	if n := h.hitCount(); n != 0 {
		t.Fatalf("合规段不得触发重翻调用，实际 %d 次", n)
	}
}

// TestApplySegmentGatesEmptyMapSafe 空 map 直接返回 nil（不 panic、不查库）。
// 钉住的是 fast/异常路径下 langTranslations 可能为空的事实。
func TestApplySegmentGatesEmptyMapSafe(t *testing.T) {
	h := fwpGateEngine(t)
	if ws := h.eng.applySegmentGates(context.Background(), map[string]map[string]string{}, true); ws != nil {
		t.Fatalf("空输入应返回 nil，got %v", ws)
	}
	if ws := h.eng.applySegmentGates(context.Background(), nil, false); ws != nil {
		t.Fatalf("nil 输入应返回 nil，got %v", ws)
	}
}
