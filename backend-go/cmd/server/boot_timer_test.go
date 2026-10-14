// ============ boot_timer_test.go · 职责说明 ============
// cmd/server 包「启动路径计时与 bind 前瘦身」的守卫级断言（★ #41 S3，2026-10-14）。
//
// 这个文件要守的**不是** boot_timer.go 那几十行（它简单到不需要测试），
// 而是 main.go 里那几处**结构事实**——因为它们一旦退化，现网没有任何症状：
//
//	S 组 计时器本体：起点必须钉在「包初始化时刻」，不是 newBootStager 被调用的那一刻
//	   （后者会把 Go 运行时装载＋各包 init 整段漏掉，"亚秒"读数会假好）；nil 接收者不许 panic
//	   （main 里有一条 `--init-db` 提前退出路径，计时器不能成为任何路径的前置条件）。
//	A 组 CheckHealth 必须在 go 语句之内（S3-a 的全部价值就在那一个字面 `go`）：
//	   把它挪回主流程＝bind 之前重新串行等 4 次 Python 冷启动，
//	   **界面照常、健康面照常、构建照常绿**，只有发版窗口那 3 秒还是 3 秒——
//	   这类"退化没有任何症状"的改动只能靠结构锁拦（AGENTS §三「判据级代替不了守卫级」）。
//	O 组 阶段顺序：段数下界 ＋「可服务」那一条必须**早于** serveHTTP。
//	   挪到后面＝total_ms 里含"已经在服务了"的时间，亚秒判据从此恒绿——
//	   这正是方案 §八 第 3 条点名的"把没跑过判定读成没问题"同族形态。
//	L 组 日志口径：新观测行走 observability，不许往 cmd/ 的 log.Printf 棘轮账上再加一行；
//	   且后台腿必须自己出读数（异步化 ≠ 把读数扔掉）。
//
// 反证一律喂**小片段 fixture** 给同一个判据函数（不改仓库源码、不用 go:overlay），
// 真文件作对照腿：判据函数必须同时"在交付形态上判绿、在退化形态上判红"，否则它没有射程。
// =============================================
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// readGateFile 读本包目录下的源文件（测试的工作目录＝backend-go/cmd/server）。
// 读不到即 Fatal：判据看不见实现时**不许**静默跳过（跳过＝这一腿恒绿，比没有更坏）。
func readGateFile(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("读不到 %s：%v ⇒ 判据没有输入，这条锁此刻是空转的", name, err)
	}
	return string(raw)
}

// mainFuncDecl 从源码里取 main() 的 FuncDecl；取不到返回 nil（调用方按"判据不成立"处理）。
func mainFuncDecl(src string) *ast.FuncDecl {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "gate.go", src, parser.AllErrors)
	if err != nil {
		return nil
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != "main" || fd.Body == nil {
			continue
		}
		return fd
	}
	return nil
}

// isFileprocCheckHealthCall 判一个 CallExpr 是不是 `fileproc.CheckHealth(...)`。
// 只认包限定写法（本包内部的裸 CheckHealth() 不属于这一腿）。
func isFileprocCheckHealthCall(ce *ast.CallExpr) bool {
	se, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok || se.Sel == nil || se.Sel.Name != "CheckHealth" {
		return false
	}
	x, ok := se.X.(*ast.Ident)
	return ok && x.Name == "fileproc"
}

// checkHealthPlacements 数 main() 里的 fileproc.CheckHealth 调用点，分「在 go 语句内／不在」。
// 交付形态期望：inGo ≥1 且 sync == 0。
func checkHealthPlacements(src string) (inGo, sync int) {
	fd := mainFuncDecl(src)
	if fd == nil {
		return -1, -1 // 解析失败：调用方必须把它当问题，不许当"没问题"
	}
	var stack []ast.Node
	ast.Inspect(fd, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1] // 退出该节点
			return false
		}
		if ce, ok := n.(*ast.CallExpr); ok && isFileprocCheckHealthCall(ce) {
			inside := false
			for _, a := range stack {
				if _, isGo := a.(*ast.GoStmt); isGo {
					inside = true
					break
				}
			}
			if inside {
				inGo++
			} else {
				sync++
			}
		}
		stack = append(stack, n)
		return true
	})
	return inGo, sync
}

// bootGateMinStages 交付形态的阶段数**下界**（不是等值：多加一段计时是改进，不该被这条锁判红；
// 但"只加了一条就交差"必须判红——那正是"计时器在位、读数什么也没说"的形态）。
const bootGateMinStages = 8

// bootGateFinalStage 必须存在、且必须早于 serveHTTP 的那一条终段名（它就是"进程起点→能服务"的总读数）。
const bootGateFinalStage = "可服务（listener就绪）"

// bootStageOrderProblems 对**已剥注释**的源码做三判，返回问题清单（空＝结构成立）：
// ① main() 里 boot.stage 调用数 ≥ 下界；
// ② 终段 "可服务（listener就绪）" 在位；
// ③ 终段位置早于 serveHTTP 调用（顺序锁的 last 那一枚——first 由②的名称点名负责）。
// 解析失败/参照物缺失一律算问题：这条判据的射程全靠 serveHTTP 那个参照物存在。
func bootStageOrderProblems(strippedSrc string) []string {
	fd := mainFuncDecl(strippedSrc)
	if fd == nil {
		return []string{"fixture 解析失败或找不到 func main()，判据无法成立"}
	}
	var names []string
	var finalPos, servePos token.Pos
	ast.Inspect(fd, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if se, isSel := ce.Fun.(*ast.SelectorExpr); isSel && se.Sel != nil && se.Sel.Name == "stage" {
			if x, isIdent := se.X.(*ast.Ident); isIdent && x.Name == "boot" && len(ce.Args) > 0 {
				if lit, isStr := ce.Args[0].(*ast.BasicLit); isStr {
					nm := strings.Trim(lit.Value, `"`)
					names = append(names, nm)
					if nm == bootGateFinalStage && finalPos == 0 {
						finalPos = ce.Pos()
					}
				}
			}
			return true
		}
		if id, isIdent := ce.Fun.(*ast.Ident); isIdent && id.Name == "serveHTTP" && servePos == 0 {
			servePos = ce.Pos()
		}
		return true
	})
	var out []string
	if len(names) < bootGateMinStages {
		out = append(out, "boot.stage 调用数="+strconv.Itoa(len(names))+"，期望 ≥ "+strconv.Itoa(bootGateMinStages)+
			"（阶段计时只剩一两条＝读数说明不了瓶颈在哪一步）")
	}
	if finalPos == 0 {
		out = append(out, "找不到终段 \""+bootGateFinalStage+"\" ⇒ 总读数不在，亚秒判据没有输入")
	}
	if servePos == 0 {
		out = append(out, "找不到 serveHTTP 调用 ⇒ 顺序锁的参照物漂了（先判是代码改名还是 fixture 缺腿）")
	}
	if finalPos != 0 && servePos != 0 && finalPos > servePos {
		out = append(out, "终段计时排在 serveHTTP **之后** ⇒ total_ms 含已在服务的时间，亚秒判据恒绿")
	}
	return out
}

// S1：计时器起点钉在包初始化时刻、stage 让 last 前进、nil 接收者安全。
func TestBootStagerStartIsPackageInitTime(t *testing.T) {
	if bootProcessStart.IsZero() {
		t.Fatal("bootProcessStart 是零值 ⇒ 起点从没被赋值，所有 total_ms 都是垃圾读数")
	}
	if bootProcessStart.After(time.Now()) {
		t.Errorf("bootProcessStart 在未来（%v）⇒ 时钟或初始化顺序有问题，total_ms 不可信", bootProcessStart)
	}
	b := newBootStager()
	if !b.start.Equal(bootProcessStart) {
		t.Errorf("newBootStager 的 start(%v) 不等于 bootProcessStart(%v) ⇒ 起点变成了「调用计时器那一刻」，"+
			"Go 运行时装载与各包 init（embed 资源）那段被漏掉，亚秒读数会假好", b.start, bootProcessStart)
	}
	first := b.last
	time.Sleep(2 * time.Millisecond) // 让"本段耗时"真的可测，而不是靠调度运气
	b.stage("夹具段")
	if !b.last.After(first) {
		t.Errorf("stage 之后 last 没前进 ⇒ 下一段会把这一段的耗时算进自己头上（从第二段起累计全错）")
	}
	// nil 接收者：--init-db 那类提前退出路径不许因为"没建计时器"而 panic
	var nilStager *bootStager
	nilStager.stage("nil 段")
}

// A1：CheckHealth 只能在 go 语句里；三枚 fixture 证明判据函数真有射程。
func TestCheckHealthIsOffTheBindCriticalPath(t *testing.T) {
	code := stripGoComments(readGateFile(t, "main.go"))
	inGo, sync := checkHealthPlacements(code)
	if inGo < 1 {
		t.Errorf("main.go 里找不到 go 语句内的 fileproc.CheckHealth（读数 inGo=%d sync=%d）⇒ S3-a 那条异步腿没了，"+
			"现网表现是发版窗口的秒级延迟原样回来，而构建／单测／健康面全绿——只有这条锁看得见", inGo, sync)
	}
	if sync != 0 {
		t.Errorf("main.go 里还有 %d 处**同步**的 fileproc.CheckHealth ⇒ bind 之前又在串行等 Python 探测（#41 S3-a 退化）", sync)
	}

	// 反证 1：同步调用形态必须判红（否则上面的绿灯来自空转）
	if g, s := checkHealthPlacements(bootGateSrcSyncCall); g != 0 || s != 1 {
		t.Errorf("反证 fixture（同步调用）读数=(inGo=%d, sync=%d)，期望 (0, 1) ⇒ 判据函数本身没有射程", g, s)
	}
	// 反证 2：go 关键字在、但 CheckHealth 在 go 语句**外面**——"顺手包了个 go"必须被抓
	if g, s := checkHealthPlacements(bootGateSrcGoIsElsewhere); g != 0 || s != 1 {
		t.Errorf("反证 fixture（go 在别处）读数=(inGo=%d, sync=%d)，期望 (0, 1) ⇒ 这条锁会把没异步当成异步化成功", g, s)
	}
	// 正对照：交付形态必须判绿（负向腿不许误伤）
	if g, s := checkHealthPlacements(bootGateSrcAsyncCall); g != 1 || s != 0 {
		t.Errorf("正对照 fixture（go 语句内）读数=(inGo=%d, sync=%d)，期望 (1, 0) ⇒ 判据把交付形态也读成违规", g, s)
	}
	// 解析失败必须算问题，不能算"没问题"（同 AGENTS §三 那条"读不出＝无牌"）
	if g, s := checkHealthPlacements("这不是 Go"); g != -1 || s != -1 {
		t.Errorf("喂进非 Go 源码的读数=(%d, %d)，期望 (-1, -1) ⇒ 判据看不见实现时会往「结构成立」那侧靠", g, s)
	}
}

// O1：阶段数下界＋终段早于 serveHTTP（四枚 fixture 两红两绿）。
func TestBootStagesCoverServePath(t *testing.T) {
	code := stripGoComments(readGateFile(t, "main.go"))
	if problems := bootStageOrderProblems(code); len(problems) > 0 {
		t.Errorf("启动阶段计时的结构不成立：%s", strings.Join(problems, "；"))
	}
	cases := []struct {
		name string
		src  string
		red  bool
	}{
		{"正对照（八段且终段在 serveHTTP 之前）", bootGateSrcWellOrdered, false},
		{"反证（段数不足）", bootGateSrcTooFewStages, true},
		{"反证（终段排在 serveHTTP 之后）", bootGateSrcStageAfterServe, true},
		{"反证（根本没有终段）", bootGateSrcNoFinalStage, true},
		{"反证（没有 serveHTTP 参照物）", bootGateSrcNoServe, true},
	}
	for _, c := range cases {
		got := len(bootStageOrderProblems(c.src)) > 0
		if got != c.red {
			t.Errorf("%s：判据读数=%v，期望=%v（%v）⇒ 这条锁的射程与它声称的不一致", c.name, got, c.red, c.src)
		}
	}
}

// L1：日志口径与"后台腿自己出声"。
func TestBootTimerUsesObservabilityNotStdlibLog(t *testing.T) {
	code := stripGoComments(readGateFile(t, "boot_timer.go"))
	if n := strings.Count(code, "log.Printf("); n != 0 {
		t.Errorf("boot_timer.go 里有 %d 处 log.Printf ⇒ 往 cmd/ 棘轮基线加账（新观测行一律走 observability，AGENTS §一·2）", n)
	}
	if !strings.Contains(code, "observability.Info(") {
		t.Error("boot_timer.go 里没有 observability.Info ⇒ 计时读数根本没出声，这段计时的存在理由就没了")
	}
	if !strings.Contains(code, "bootAsyncDone") {
		t.Error("bootAsyncDone 不在位 ⇒ S3-a 变成「把读数挪进黑洞」，现网再也看不到那条后台腿花了多久")
	}
	// 交付形态必须**真的用上**后台腿（在 main.go 里），而不是只写着一个没人调的函数
	if !strings.Contains(stripGoComments(readGateFile(t, "main.go")), "bootAsyncDone(") {
		t.Error("main.go 没调 bootAsyncDone ⇒ 后台自检的完成读数不在，异步化的那一条腿没有观测面")
	}
}

// ---------------- fixture ----------------

const bootGateSrcSyncCall = `package main

func main() {
	fileproc.CheckHealth()
	serveHTTP(s, ln)
}
`

const bootGateSrcAsyncCall = `package main

func main() {
	go func() {
		fileproc.CheckHealth()
	}()
	serveHTTP(s, ln)
}
`

const bootGateSrcGoIsElsewhere = `package main

func main() {
	go worker()
	fileproc.CheckHealth()
	serveHTTP(s, ln)
}
`

// bootGateSrcWellOrdered 八段且终段在 serveHTTP 之前：判据必须在它上面判绿。
const bootGateSrcWellOrdered = `package main

func main() {
	boot.stage("a")
	boot.stage("b")
	boot.stage("c")
	boot.stage("d")
	boot.stage("e")
	boot.stage("f")
	boot.stage("g")
	boot.stage("可服务（listener就绪）")
	serveHTTP(s, ln)
}
`

// bootGateSrcTooFewStages 只有一条计时：下限锁必须判红。
const bootGateSrcTooFewStages = `package main

func main() {
	boot.stage("a")
	serveHTTP(s, ln)
}
`

// bootGateSrcStageAfterServe 终段排在 serveHTTP 之后：顺序锁必须判红。
const bootGateSrcStageAfterServe = `package main

func main() {
	boot.stage("a")
	boot.stage("b")
	boot.stage("c")
	boot.stage("d")
	boot.stage("e")
	boot.stage("f")
	boot.stage("g")
	serveHTTP(s, ln)
	boot.stage("可服务（listener就绪）")
}
`

// bootGateSrcNoFinalStage 段数够但没有终段：总读数锁必须判红。
const bootGateSrcNoFinalStage = `package main

func main() {
	boot.stage("a")
	boot.stage("b")
	boot.stage("c")
	boot.stage("d")
	boot.stage("e")
	boot.stage("f")
	boot.stage("g")
	boot.stage("h")
	serveHTTP(s, ln)
}
`

// bootGateSrcNoServe 参照物不见了（改名／fixture 缺腿）：必须判红而不是"没问题"。
const bootGateSrcNoServe = `package main

func main() {
	boot.stage("a")
	boot.stage("b")
	boot.stage("c")
	boot.stage("d")
	boot.stage("e")
	boot.stage("f")
	boot.stage("g")
	boot.stage("可服务（listener就绪）")
	bindAndServe(s, ln)
}
`
