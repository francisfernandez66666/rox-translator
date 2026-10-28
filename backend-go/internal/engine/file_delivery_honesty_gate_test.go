// ============ file_delivery_honesty_gate_test.go 职责说明 ============
// G18（2026-10-19）「文件交付链的每一次出声都必须进工单」的家族级装配闸。
//
// 缺陷形态：file.go 里有大量「择优产出」的降级/跳过点（音频副本、图片原位回填、PDF 版式重建、
// 纯文案旁路、配音回落…）。这些点在代码眼里都只是**一行日志**——`observability.Warn`／`log.Printf`
// 打过就算「处理了」，而客户与审批台看到的交付物**一个字都没变**：
// 没有 GateWarning、没有 degraded 标记，工单轨迹里查不到，用户以为一切正常。
// 这正是 AGENTS 反复点名的「有自动降级的链路，产物能打开不算验收」那一族。
//
// 纪律（硬口径）：**每一次 observability.Warn 或 log.Printf 都必须与一次
// `degraded = append` 或 `gateWarnings = append` 同处一个代码块**——出声（日志）与记账（工单）
// 不许脱钩。极少数确实不是「交付退化」的正经日志（进度、品牌术语加载、硬闸失败前的提示）
// 走 allowlist，按**首个字符串字面量**逐条点名并写明理由（禁按行号——行号漂了就失效）。
//
// 反向自证（本文件自带）：ScannerDetectsUnpaired 用合成源码喂未配对与已配对两种形态，
// 证明扫描器不是「永远返回已配对」的假绿灯；主用例还要求每个 allowlist 条目**真被命中**，
// 防止白名单被越加越宽（加了没人用的豁免＝把射程悄悄放走）。
//
// 本文件只读源码，不做任何 LLM/网络/DB 调用。
// ============================================================
package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// fdhAllow 允许「只出声、不进工单」的正经日志（按首个字符串字面量精确点名）。
// 逐条理由必须写明——不写理由的豁免就是下一次静默交付的温床。
var fdhAllow = map[string]string{
	"[file] PDF overlay 提取失败（回退既有提取键）: %v":                             "提取期回退既有键：不是交付退化，是提取策略切换（后续照常翻译）",
	"[brandterm] 文件路径加载品牌术语 %d 个语言（翻译前保护+翻后归一化）":                       "信息：品牌术语加载成功，非退化",
	"[kb-match] lang=%s progress=%d/%d":                                "进度心跳，非退化",
	"[kb-match] lang=%s 命中=%d 走模型=%d 拦截=%d":                            "统计读数，非退化",
	"[tm-hardgate] lang=%s 预算耗尽（%s），剩余 %d 段未译":                         "硬闸预算耗尽：未译段另有 untranslated 记账，非静默",
	"[tm-hardgate] lang=%s round=%d 重启LLM重译 %d 段":                      "硬闸重译轮次，非退化",
	"[tm-hardgate] lang=%s 结束：仍有 %d/%d 段未译出（已写入工单 Untranslated 供人工补译）": "硬闸收尾：未译清单已进工单 Data，非静默",
	"[file-repair] lang=%s round=%d 定向补译 %d 段（阈值 %.0f%%）":              "5% 触发后的补译进度，非交付退化",
	"[file-gate] %s 漏译 %d/%d 段（超 %.0f%%），已按补译/标记口径交付":                  "缺失段已进 untranslated/gate_warnings（同块嵌套 if），非静默",
	"[brandterm] 文件路径品牌术语归一化 %d 个语言译文":                                 "信息：归一化生效，非退化",
	"译文配音跳过（不影响字幕与译文交付）":                                               "配音跳过：返回值把 warning 交给调用方 append（见 dubTranslatedAudio 调用点），本函数内无 degraded/gateWarnings",
	"译文配音回落 mp3（原容器转码不可用）":                                             "配音容器回落：同上，warning 经返回值上抛",
	// ★ 2026-10-27（工单 T20261027152356CMY 复盘 C 项）新增两条。**记账在本函数内，只是不叫
	//   degraded/gateWarnings**：retryImageSegments 的账本是返回值 `out []fileproc.UntranslatedItem`，
	//   调用方把它逐行写进交付 .md 的「未译出内容」一节（图片原位回填的行则按原文保留）。
	//   也就是说这两条日志不是"只有日志"——它们描述的那次退化**已经进了客户可见的交付面**。
	//   扫描器只认 degraded/gateWarnings 两个变量名（见 fdhHasLedgerAppend），故在此逐条点名；
	//   若哪天把 `out` 并进 gateWarnings 口径，这两条豁免应同步删除。
	"图片工单逐行补译仍不合格，按原文回落":         "记账＝返回值 out（逐行进 .md「未译出内容」）；本 WARN 补的是**成因读数**（empty/explosion/length_info），不是新增静默",
	"图片工单逐行补译判上游断，剩余未译行一次性按原文回填": "记账＝返回值 out（剩余段逐条计入未译出）；本 WARN 是整语种一行汇总，防逐行刷屏",
}

// fdhSite 一处「出声」点位及其记账配对情况。
type fdhSite struct {
	line     int    // 行号（仅用于报错定位，判定不吃它）
	callee   string // observability.Warn / log.Printf
	firstLit string // 首个字符串字面量（allowlist 判据）
	paired   bool   // 所在代码块子树内是否有 degraded/gateWarnings append
}

// fdhScanSource 用 AST 扫出一段源码里所有 observability.Warn / log.Printf，
// 并判定每一次出声所在的最内层代码块里是否出现 `degraded = append(` 或 `gateWarnings = append(`。
func fdhScanSource(t *testing.T, name string, src []byte) []fdhSite {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败（语法坏了？）: %v", name, err)
	}
	var sites []fdhSite
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee := fdhCalleeName(call)
		if callee != "observability.Warn" && callee != "log.Printf" {
			return true
		}
		sites = append(sites, fdhSite{
			line:     fset.Position(call.Pos()).Line,
			callee:   callee,
			firstLit: fdhFirstStringLit(call),
			paired:   fdhNearestBlockPaired(stack),
		})
		return true
	})
	return sites
}

// fdhCalleeName 取调用名（支持 selector 形态 observability.Warn / log.Printf）。
func fdhCalleeName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// fdhFirstStringLit 取调用实参里第一个字符串字面量（不含 raw 反引号以外的类型）。
func fdhFirstStringLit(call *ast.CallExpr) string {
	for _, a := range call.Args {
		if bl, ok := a.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			return strings.Trim(bl.Value, "`\"")
		}
	}
	return ""
}

// fdhNearestBlockPaired 自栈内向外找最近的分支代码块，判断其子树里是否有 degraded/gateWarnings 的 append。
//
// ★ 刻意跳过**函数体**那层 BlockStmt：函数体（translateFile）上千行，任何 append 都在它的子树里，
// 拿它当「同一块」会让整条判据退化到近乎恒真（一处 append 覆盖全函数所有出声）。
// 判据要求的是「这一次出声所在的那个 if/else/for/switch 分支里就有记账」。
func fdhNearestBlockPaired(stack []ast.Node) bool {
	for i := len(stack) - 1; i >= 0; i-- {
		blk, ok := stack[i].(*ast.BlockStmt)
		if !ok {
			continue
		}
		// 该 BlockStmt 是否是某函数体（其父节点是 FuncDecl/FuncLit）——是则跳过。
		if i > 0 {
			switch stack[i-1].(type) {
			case *ast.FuncDecl, *ast.FuncLit:
				continue
			}
		}
		return fdhHasLedgerAppend(blk)
	}
	return false
}

// fdhHasLedgerAppend 子树里是否出现 `degraded = append(...)` 或 `gateWarnings = append(...)`。
func fdhHasLedgerAppend(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || (id.Name != "degraded" && id.Name != "gateWarnings") {
			return true
		}
		if ce, ok := as.Rhs[0].(*ast.CallExpr); ok {
			if fn, ok := ce.Fun.(*ast.Ident); ok && fn.Name == "append" {
				found = true
			}
		}
		return !found
	})
	return found
}

// TestFileDeliveryWarningsArePaired 主判据：file.go 里每一次出声都必须与 degraded/gateWarnings 记账同块。
func TestFileDeliveryWarningsArePaired(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go 失败: %v", err)
	}
	sites := fdhScanSource(t, "file.go", src)

	// 射程自证：扫描量级下限。若这个数掉下来，通常是 scanner 坏了（比如选择器形态改了），
	// 「全部已配对」就会变成 vacuous pass——同 AGENTS「判据级代替不了守卫级」。
	if len(sites) < 15 {
		t.Fatalf("只扫到 %d 处出声（期望 ≥15）：扫描器可能失明，闸门射程不成立", len(sites))
	}

	usedAllow := map[string]bool{}
	for _, s := range sites {
		if s.paired {
			continue
		}
		if _, ok := fdhAllow[s.firstLit]; ok {
			usedAllow[s.firstLit] = true
			continue
		}
		t.Errorf("file.go:%d %s(%q) 出声但同块内既无 degraded=append 也无 gateWarnings=append ⇒ 交付退化被静默："+
			"客户/审批台看不到这次跳过。要么补记账，要么在 fdhAllow 里逐条点名并写明理由", s.line, s.callee, s.firstLit)
	}

	// 反向自证 2：allowlist 里每一条都必须真被命中，否则白名单被越加越宽（加了没人用的豁免＝悄悄放走射程）。
	for lit, reason := range fdhAllow {
		if !usedAllow[lit] {
			t.Errorf("fdhAllow 条目 %q（理由：%s）在 file.go 里未被命中：要么日志改词了（同步改这里），要么该豁免已无对象（删掉它）", lit, reason)
		}
	}
}

// fdhCondHasZeroHit 判断条件里是否有 `…Hits == 0`（writeHits/xlsxHits 零命中守卫）。
func fdhCondHasZeroHit(cond ast.Expr) bool {
	found := false
	ast.Inspect(cond, func(n ast.Node) bool {
		be, ok := n.(*ast.BinaryExpr)
		if !ok || be.Op != token.EQL {
			return true
		}
		left, lok := be.X.(*ast.Ident)
		right, rok := be.Y.(*ast.BasicLit)
		if lok && rok && strings.HasSuffix(left.Name, "Hits") && right.Value == "0" {
			found = true
		}
		return !found
	})
	return found
}

// fdhBodyAssignsAerr 判断分支体里是否把 `aerr` 赋成错误（进退化通道的前提）。
func fdhBodyAssignsAerr(blk *ast.BlockStmt) bool {
	found := false
	ast.Inspect(blk, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		if id, ok := as.Lhs[0].(*ast.Ident); ok && id.Name == "aerr" {
			found = true
		}
		return !found
	})
	return found
}

// TestOfficeZeroHitGuardsExist ★ G17：docx/pptx/xlsx 零命中必须与写回失败同走退化通道。
// PDF 腿早有零命中守卫（pdf_overlay 内部判错），三档 Office 此前缺这条腿＝未翻译件当成功交付。
// 本闸按源码钉住：每处 `…Hits == 0` 守卫都必须把 aerr 置错（否则不进 degraded 通道）。
func TestOfficeZeroHitGuardsExist(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go 失败: %v", err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "file.go", src, 0)
	if err != nil {
		t.Fatalf("解析 file.go 失败: %v", err)
	}
	guards := 0
	ast.Inspect(f, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || !fdhCondHasZeroHit(ifs.Cond) {
			return true
		}
		guards++
		if !fdhBodyAssignsAerr(ifs.Body) {
			t.Errorf("file.go:%d 零命中守卫未把 aerr 置错 ⇒ 不会进退化通道（未翻译件会被当成功交付）",
				fset.Position(ifs.Pos()).Line)
		}
		return true
	})
	if guards < 2 {
		t.Fatalf("只找到 %d 处「…Hits == 0」守卫（xlsx ＋ office 应至少 2 处）：扫描器可能失明", guards)
	}
}

// TestFileDeliveryHonestyScannerDetectsUnpaired 反向自证：合成源码里未配对出声必须判红、已配对必须判绿。
// 没有这条，主用例可能只是扫描器坏掉在 vacuous pass（AGENTS「反证必须真红在那一条腿」）。
func TestFileDeliveryHonestyScannerDetectsUnpaired(t *testing.T) {
	const sample = `package engine

import "translator/internal/observability"

func deliver(ctx context.Context) {
	observability.Warn(ctx, "未配对的出声（应判红）", "x", 1)
	if ok {
		degraded = append(degraded, "en")
		observability.Warn(ctx, "已配对的出声（应判绿）", "x", 1)
	}
	log.Printf("未配对的 printf（应判红）: %v", err)
	if ok2 {
		gateWarnings = append(gateWarnings, "记账")
		log.Printf("已配对的 printf（应判绿）: %v", err)
	}
}
`
	sites := fdhScanSource(t, "sample.go", []byte(sample))
	if len(sites) != 4 {
		t.Fatalf("样本应有 4 处出声，实际 %d", len(sites))
	}
	wantPaired := []bool{false, true, false, true}
	for i, s := range sites {
		if s.paired != wantPaired[i] {
			t.Errorf("第 %d 处（%s:%d，%q）配对判定为 %v，期望 %v", i+1, s.callee, s.line, s.firstLit, s.paired, wantPaired[i])
		}
	}
	// 判据射程再确认：两处「应判红」的必须真的待在 allowlist 之外（否则样本本身就不构成反证）。
	for _, s := range sites {
		if s.paired == false {
			if _, ok := fdhAllow[s.firstLit]; ok {
				t.Errorf("反证样本里的未配对项 %q 竟在 allowlist 内 ⇒ 样本失去反证力", s.firstLit)
			}
		}
	}
}

// TestFileHardgateHasThreeAttempts 文件链硬闸必须是 3 轮。
//
// 轮次口径的两次变化，理由都写在这里（改这个数之前先读完整段，不要只看末次）：
//   - 2026-10-21 用户令：由「连续零进展提前熔断（实际最多 2 次）」改成**给足 5 轮**；
//   - 2026-10-28 用户令（计价与成本批）：**5 → 3**。射程不变——仍是"每轮只补缺失段"，
//     三轮之后仍缺失的段按原文交付并由 UntranslatedSegments / GateWarnings 出声。
//     降轮的唯一理由是成本：第 4/5 轮的救回率 <2%，而每一轮都是对整篇缺失段的重新调用，
//     在 1G 机器上是实打实的 token 与墙钟放大（同批的缓存 A/B 才是真正的省法）。
//     所以"给足 3 次"这条仍然成立：`attempt <= fileHardgateMaxAttempts` 的循环约束与
//     「不许按连续零进展提前熔断」两条一起判——它们才是 10-21 那条用户令不受损的部分。
func TestFileHardgateHasThreeAttempts(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go 失败: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "const fileHardgateMaxAttempts = 3") {
		t.Fatal("硬闸轮次上限必须为 3")
	}
	if !strings.Contains(s, "attempt <= fileHardgateMaxAttempts") {
		t.Fatal("硬闸循环必须受 fileHardgateMaxAttempts 约束")
	}
	if strings.Contains(s, "zeroProgressRounds") {
		t.Fatal("硬闸不许再按连续零进展提前熔断（用户令要求给足 3 次）")
	}
}
