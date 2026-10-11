// ============ systemconfig_writepoint_gate_test.go · 职责说明 ============
// store 包 `system_config` 裸写点的**静态闸**（★ 2026-10-11 由慢 SQL 批落地账只读复核新发现立项）。
//
// 守的事：`GetConfig` 的 5 秒进程内缓存（P2⑤）有一条纪律——**凡是绕开 `SetConfig` 的裸 SQL 写点
// （DELETE／事务内 upsert／条件 INSERT），落库成功之后必须显式 `dropConfigCache`**。
// 此前只有两样东西在守：六条行为测试（射程＝作者当时想到的那几处写点）＋ AGENTS 里的文字。
// ⇒ 新加第七处裸写、忘了挂钩，闸门一声不响（现网表现与本批刚修掉的那个缺陷一模一样：
// 运营清空支付私钥后 ≤5 秒，渠道仍按「已配置」判定——方向是"以为有、其实没有"的静默错判）。
// 本闸把纪律换成**按源码现算的枚举型判据**，手法同 `observability/logratchet_test.go`／
// `fileproc/postdeploy_exit_gate_test.go`：枚举源是被守那一侧的现值，不写死文件名清单。
//
// 七条腿（W1–W7），各抓一种**真实的坏法**：
//
//	W1 现算全集＋下限正锁：抓"字面量锚漂了、整段静默失明"（哪天 SQL 拼成变量，计数会掉而不是变成"全仓零裸写"）。
//	W2 逐点分派：本函数体内没有失效调用 ⇒ 判红并**点名函数**（抓"新增裸写不挂钩"＝本闸存在的理由）。
//	W3 委托豁免＝源码注释里必须有 `cache-hook:caller` 标记，且**调用方真挂钩**；标记函数没有直接调用方也算红
//	   （抓"把豁免写成无条件放行"与"死码豁免"）。
//	W4 跨包负向锁：`internal/store` 包之外出现写 `system_config` 的 SQL 即红（缓存钩子物理上只在 store 里）。
//	   归属按**包**判（目录前缀 ∧ package 子句，见 sysCfgPartition），所以"把写点搬进 store 目录树下的子包"不是出路。
//	W5 动态表名的那条缝：`scaleColumnTx` 把表名当参数拼 SQL，函数体里没有字面量 ⇒ 现读派生清单
//	   `rebaseValueHolders` 逐条断"表名≠配置表"（这是 ast 字面量锚唯一够不到的形态）。
//	W6 失效必须在落库**之后**，且**逐对判**：W2 只问"有没有钩"，对"先碰缓存后落库""回滚分支里失效"
//	   "把循环体内的失效挪出循环"三种完全无感——本仓既有纪律：顺序锁只挂一头＝删掉另一头仍全绿。
//	W7 钩子要钩对**键**：挂了钩、废的是另一个键（缓存照样陈旧、闸一声不响）。只在"两侧都能确定是字面量或
//	   具名常量"时判；任一侧是变量／切片展开 ⇒ 刻意不硬猜等值（猜了就是逼人改坏代码的脆锁）。
//
// ★ 反证矩阵（十二枚，逐枚见 TestSysCfgWritePointGateMutations；每枚必须只红在自己那一腿）：
//
//	M1 摘掉 SetFxRates 的失效行 → W2｜M2 把 PruneScrapeLedger 的失效调用改名 → W2（抓改名式破坏）
//	M3 新增一处不挂钩的裸写文件 → W2｜M4 剥掉 rebasePointsMintedConfig 的标记行 → W3
//	M5 把 upsertConfigTx 的调用方整批改名（标记函数没了消费者）→ W3｜M6 SQL 字面量锚漂 → W1（实读掉到下限以下）
//	M7 往 rebaseValueHolders 加一行 {"system_config",…} → W5｜M8 把失效挪到落库之前 → W6（W2 仍绿＝两腿射程不同）
//	M9 把循环体里的失效行挪出循环 → W6（**反证锁本身写法**的那枚：只比"首写早于末失效"会跟着绿）
//	M10 把失效参数换成另一个常量键 → W7｜M11 往"包外"那一侧塞一份裸写 → W4（负向腿的存在理由：
//	   现仓 store 之外零写点，没有这一枚就分不清"真的没有"与"枚举压根没扫到外面"）
//	M12 把写点放进 internal/store/sub/（同目录树、不同包）→ W4（这枚打的是**路由判据**：
//	   按目录前缀归属会让它红在 W2，而 W2 的文案叫开发者补 dropConfigCache——sub 包拿不到那个非导出方法）
//
// 变异一律喂**内存里的源码副本**（`go test` 期间就地改源＝结果不可信那条），磁盘文件一字不动。
// 本文件只读源码文本、不开库、不碰 config.C ⇒ 无需按 §一·4 自钉方言（反而不给邻居留方言泄漏口）。
// =============================================
package store

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 本闸的四个锚点常量：全部从**被守那一侧的真实标识符**取，禁止在这里造第二套名字。
// 改名式破坏（把 dropConfigCache 换成别的）由 M2 那枚反证抓。
const (
	sysCfgTable         = "system_config"      // 被守的 KV 表
	sysCfgHookFn        = "dropConfigCache"    // 唯一的缓存失效入口
	sysCfgCacheField    = "sysCfgCache"        // 进程内缓存字段名
	sysCfgHookMarker    = "cache-hook:caller"  // 委托豁免标记（写在这一处代码的注释里，不写在闸的常量里）
	sysCfgSetterName    = "(*Store).SetConfig" // 唯一允许"自己写库又自己翻缓存"的函数
	sysCfgWritePointMin = 8                    // W1 下限：现读 8 处（合法删写点要同步下调并在提交里写明原因）
	sysCfgHolderMin     = 7                    // W5 清单下限（现读 7 条「表.列」）
)

// sysCfgFinding 是一条闸门违规：Leg 点名哪一腿，Msg 必须点到**函数/文件**级（不留"整段红了"的账）。
type sysCfgFinding struct {
	Leg string
	Msg string
}

// sysCfgHookKind 一个裸写点的归类（四档，W2 判据只看 Unhooked）。
type sysCfgHookKind int

// 四档归类（顺序即语义：setter 自翻 → 本函数挂钩 → 委托调用方 → 没挂钩）。
const (
	kindSetter    sysCfgHookKind = iota // setter 本体：落库成功后自己把新值翻进缓存 ⇒ 合法
	kindSameFunc                        // 本函数体内有失效调用
	kindDelegated                       // 事务助手：钩子在调用方（须带 cache-hook:caller 标记）
	kindUnhooked                        // 哪儿都没有 ⇒ 判红
)

// String 出**读数档名**（测试日志与 W1/W2 的点名文案里出现的就是这几个词，属对外排障契约，改名要同步改期望值）。
func (k sysCfgHookKind) String() string {
	switch k {
	case kindSetter:
		return "SetterSelf"
	case kindSameFunc:
		return "HookedSameFunc"
	case kindDelegated:
		return "DelegatedToCaller"
	default:
		return "Unhooked"
	}
}

// sysCfgCall 一次相关调用（写点或失效点）的读数。
// loopID 是"最近一层循环"的编号（0＝不在任何循环内）——W6 逐对判的依据：
// 循环里的写必须由**同一个循环体内**的失效来兜（挪出循环＝前面几轮继续投旧值）。
// failPath 表示它落在 `err != nil` 那一类失败分支里（W6 禁止失效出现在那里）。
type sysCfgCall struct {
	line      int
	form      string
	loopID    int
	failPath  bool
	keys      []string
	keysKnown bool
}

// sysCfgFunc 一个函数体的读数（按函数归属，不按行距——行距判据会把"距离 100 行的合法钩子"放过、
// 把"事务助手＋调用方"这一类真形态判成违规）。
type sysCfgFunc struct {
	file           string
	name           string
	params         map[string]bool
	writes         []sysCfgCall
	hooks          []sysCfgCall
	calls          map[string][]int
	commits        []int
	marker         bool
	writesCacheVal bool
	deletesCache   bool
}

// sysCfgWritePoint 一个裸写点及其归类结果。
type sysCfgWritePoint struct {
	File string
	Func string
	Line int
	Form string
	Kind sysCfgHookKind
}

// scanSystemConfigWritePoints 是本闸唯一的判据入口：落盘扫描与逐枚反证喂的是**同一个函数**。
func scanSystemConfigWritePoints(files map[string]string) ([]sysCfgWritePoint, []sysCfgFinding) {
	funcs, findings := parseSysCfgFuncs(files)

	markers := map[string]bool{}
	byName := map[string]*sysCfgFunc{}
	for _, f := range funcs {
		byName[f.name] = f
		if f.marker {
			markers[f.name] = true
		}
	}

	setterCount := 0
	var pts []sysCfgWritePoint
	for _, f := range funcs {
		if len(f.writes) == 0 {
			continue
		}
		sort.Slice(f.writes, func(i, j int) bool { return f.writes[i].line < f.writes[j].line })
		sort.Slice(f.hooks, func(i, j int) bool { return f.hooks[i].line < f.hooks[j].line })

		kind := kindUnhooked
		switch {
		case f.writesCacheVal:
			kind = kindSetter
		case len(f.hooks) > 0:
			kind = kindSameFunc
		case f.marker:
			kind = kindDelegated
		}
		for _, w := range f.writes {
			pts = append(pts, sysCfgWritePoint{File: f.file, Func: f.name, Line: w.line, Form: w.form, Kind: kind})
		}

		switch kind {
		case kindSetter:
			// 只允许一个"自己写库＋自己翻缓存"的 setter：出现第二个＝再造一把尺子（同 §一·3 单一事实源口径）。
			setterCount++
			if f.name != sysCfgSetterName {
				findings = append(findings, sysCfgFinding{"W2", "第二个自翻缓存的 setter：" + f.file + " 的 " + f.name +
					" 直接写 system_config 又直接改 " + sysCfgCacheField + "，必须并回 " + sysCfgSetterName})
			}
		case kindDelegated:
			// W3：豁免不是一句注释就放行——每个直接调用方都必须自己挂钩。
			callers := sysCfgCallers(funcs, f.name)
			if len(callers) == 0 {
				findings = append(findings, sysCfgFinding{"W3", f.file + " 的 " + f.name +
					" 带着 " + sysCfgHookMarker + " 标记却没有任何直接调用方＝死码豁免（钩子必须由真消费者兑现）"})
			}
			for _, c := range callers {
				if len(c.hooks) == 0 {
					findings = append(findings, sysCfgFinding{"W3", f.name + " 的调用方 " + c.name + "（" + c.file +
						"）函数体内没有 " + sysCfgHookFn + " 调用：委托豁免不成立"})
				}
			}
		case kindUnhooked:
			// 区分两种"本函数没钩子"：有人调它且调用方挂钩＝形态是委托，缺的只是标记（W3）；
			// 谁都不调它、也没标记＝真的裸写不挂钩（W2，本闸存在的理由那一类）。
			hookedCaller := false
			for _, c := range sysCfgCallers(funcs, f.name) {
				if len(c.hooks) > 0 {
					hookedCaller = true
				}
			}
			if hookedCaller {
				findings = append(findings, sysCfgFinding{"W3", f.file + " 的 " + f.name +
					" 在事务内裸写 system_config、钩子在调用方，却缺 `" + sysCfgHookMarker +
					"` 标记行（补标记才算声明，别改判据）"})
			} else {
				findings = append(findings, sysCfgFinding{"W2", f.file + ":" + strconv.Itoa(f.writes[0].line) +
					" 的 " + f.name + " 裸写 " + sysCfgTable + "（" + f.writes[0].form +
					"），本函数与调用方都没有 " + sysCfgHookFn + " 失效调用"})
			}
		case kindSameFunc:
			findings = append(findings, sysCfgOrderFindings(f)...)
			findings = append(findings, sysCfgKeyFindings(f)...)
			findings = append(findings, sysCfgDelegateOrderFindings(f, markers)...)
		}
	}
	if setterCount != 1 {
		findings = append(findings, sysCfgFinding{"W2", "SetterSelf 归类应恰为 1（现读 " + strconv.Itoa(setterCount) +
			"）——" + sysCfgSetterName + " 是唯一" + "合法的自翻 setter"})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].File != pts[j].File {
			return pts[i].File < pts[j].File
		}
		return pts[i].Line < pts[j].Line
	})
	return pts, findings
}

// sysCfgOrderFindings＝W6（顺序判据，逐对判）。三条子判据：
//  1. 每个写点后面必须有一次失效，且**同在一个循环体内**（写点在循环外时不要求循环配对）；
//  2. 失效调用不许落在 `err != nil` 一类失败分支里（库没写成却废缓存＝把"失败"说成"已生效"）；
//  3. 同函数里既有 `.Commit()` 又有失效时，失效必须晚于提交（事务型写点在提交前根本还没落库）。
func sysCfgOrderFindings(f *sysCfgFunc) []sysCfgFinding {
	var out []sysCfgFinding
	for _, w := range f.writes {
		matched := false
		for _, h := range f.hooks {
			if h.line <= w.line {
				continue
			}
			if w.loopID != 0 && h.loopID != w.loopID {
				continue // 挪出循环的那一次失效不算这一轮的兜底（反证 M9 打的就是这里）
			}
			matched = true
			break
		}
		if !matched {
			inLoop := ""
			if w.loopID != 0 {
				inLoop = "，且必须落在**同一个循环体内**（写点在第 " + strconv.Itoa(w.line) + " 行）"
			}
			out = append(out, sysCfgFinding{"W6", f.file + ":" + strconv.Itoa(w.line) + " 的 " + f.name +
				" 在本函数里找不到「晚于该写点」的 " + sysCfgHookFn + " 调用" + inLoop +
				"（先碰缓存后落库＝落库失败时读侧已经按新值判定）"})
		}
	}
	for _, h := range f.hooks {
		if h.failPath {
			out = append(out, sysCfgFinding{"W6", f.file + ":" + strconv.Itoa(h.line) + " 的 " + f.name +
				" 把 " + sysCfgHookFn + " 写进了 err!=nil 的失败分支（回滚/写失败一律不许失效）"})
		}
	}
	if len(f.commits) > 0 && len(f.hooks) > 0 {
		lastCommit := f.commits[len(f.commits)-1]
		for _, h := range f.hooks {
			if h.line < lastCommit {
				out = append(out, sysCfgFinding{"W6", f.file + ":" + strconv.Itoa(h.line) + " 的 " + f.name +
					" 在 tx.Commit()（第 " + strconv.Itoa(lastCommit) + " 行）之前就失效了缓存"})
			}
		}
	}
	return out
}

// sysCfgKeyFindings＝W7（钩子要钩对键）。只在"两侧都确定"时判等值交集：
// 写点侧键＝SQL 参数里能确定为字面量或具名常量的那些（函数形参不算，那是委托语义）；
// 失效侧同理。任一侧含变量／切片展开 ⇒ 整点豁免（本批真实形态：`drop...`／`chunk...`／`kv[0]`）。
func sysCfgKeyFindings(f *sysCfgFunc) []sysCfgFinding {
	var out []sysCfgFinding
	for _, w := range f.writes {
		if !w.keysKnown {
			continue
		}
		checkedAny := false
		bad := ""
		for _, h := range f.hooks {
			if !h.keysKnown {
				continue
			}
			checkedAny = true
			if !sysCfgKeysIntersect(w.keys, h.keys) {
				bad = "写点第 " + strconv.Itoa(w.line) + " 行键集=[" + strings.Join(w.keys, ",") +
					"]，失效第 " + strconv.Itoa(h.line) + " 行键集=[" + strings.Join(h.keys, ",") + "]"
				break
			}
		}
		if checkedAny && bad != "" {
			out = append(out, sysCfgFinding{"W7", f.file + " 的 " + f.name +
				" 挂钩废的是**另一个键**（缓存照样陈旧）：" + bad})
		}
	}
	return out
}

// sysCfgDelegateOrderFindings＝W6 的委托那一半：调用委托写点（带标记的事务助手）之后必须失效。
func sysCfgDelegateOrderFindings(f *sysCfgFunc, markers map[string]bool) []sysCfgFinding {
	maxDeleg := 0
	for name, lines := range f.calls {
		if !markers[name] {
			continue
		}
		for _, l := range lines {
			if l > maxDeleg {
				maxDeleg = l
			}
		}
	}
	if maxDeleg == 0 || len(f.hooks) == 0 {
		return nil
	}
	var out []sysCfgFinding
	for _, h := range f.hooks {
		if h.line < maxDeleg {
			out = append(out, sysCfgFinding{"W6", f.file + ":" + strconv.Itoa(h.line) + " 的 " + f.name +
				" 在第 " + strconv.Itoa(maxDeleg) + " 行那次委托写（" + sysCfgHookMarker +
				" 助手）之前就失效了缓存"})
		}
	}
	return out
}

// sysCfgCallers 找出同一批文件里直接调用 name 的那些函数（W3 的反查腿）。
func sysCfgCallers(funcs []*sysCfgFunc, name string) []*sysCfgFunc {
	var out []*sysCfgFunc
	for _, f := range funcs {
		if f.name == name {
			continue
		}
		if _, ok := f.calls[name]; ok {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// parseSysCfgFuncs 解析源码文本并按函数归属收集读数；解析失败按 PARSE 腿出声（不当"没写点"处理）。
func parseSysCfgFuncs(files map[string]string) ([]*sysCfgFunc, []sysCfgFinding) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var funcs []*sysCfgFunc
	var findings []sysCfgFinding
	for _, p := range paths {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, files[p], parser.ParseComments)
		if err != nil {
			findings = append(findings, sysCfgFinding{"PARSE", p + " 解析失败：" + err.Error()})
			continue
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			rec := &sysCfgFunc{file: p, name: sysCfgFuncName(fd), params: map[string]bool{}, calls: map[string][]int{}}
			for _, fl := range fd.Type.Params.List {
				for _, id := range fl.Names {
					rec.params[id.Name] = true
				}
			}
			if fd.Recv != nil {
				for _, fl := range fd.Recv.List {
					for _, id := range fl.Names {
						rec.params[id.Name] = true
					}
				}
			}
			rec.marker = fd.Doc != nil && strings.Contains(fd.Doc.Text(), sysCfgHookMarker)
			w := &sysCfgWalker{fset: fset, rec: rec}
			for _, st := range fd.Body.List {
				w.stmt(st, 0, false)
			}
			funcs = append(funcs, rec)
		}
	}
	return funcs, findings
}

// sysCfgFuncName 方法带接收者前缀（"( *Store).SetFxRates" 这一形态），保证点名可定位且不歧义。
func sysCfgFuncName(fd *ast.FuncDecl) string {
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		t := fd.Recv.List[0].Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if id, ok := t.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fd.Name.Name
		}
	}
	return fd.Name.Name
}

// sysCfgWalker 手工递归语句树：ast.Inspect 的"进出栈"分不清 if 的 then/else 两个分支，
// 而"失效调用落在失败分支里"这条判据必须知道自己在哪个分支（W6 第 2 子判据）。
type sysCfgWalker struct {
	fset *token.FileSet
	rec  *sysCfgFunc
	n    int // 循环编号自增器
}

// line 把 ast 节点换回**源码行号**（W6 的顺序判据按行号比先后，注释与空行不参与计算）。
func (w *sysCfgWalker) line(n ast.Node) int { return w.fset.Position(n.Pos()).Line }

// stmt 走一条语句：记录它落在第几个循环体（loop＝0 表示不在任何循环内）、是否处于失败/回滚分支（fail）。
// 这两份上下文是 W6 的核心：钩子必须与写点**同层**且**晚于**它，挪出循环或放进失败分支都算违规。
func (w *sysCfgWalker) stmt(st ast.Stmt, loop int, fail bool) {
	switch s := st.(type) {
	case *ast.BlockStmt:
		for _, c := range s.List {
			w.stmt(c, loop, fail)
		}
	case *ast.IfStmt:
		if s.Init != nil {
			w.stmt(s.Init, loop, fail)
		}
		thenFail := fail || sysCfgIsFailCond(w.condText(s.Cond))
		if s.Body != nil {
			w.stmt(s.Body, loop, thenFail)
		}
		if s.Else != nil {
			// err!=nil 分支的 else 才是成功路径，所以这里传**外层的 fail**，不继承 thenFail
			w.stmt(s.Else, loop, fail)
		}
	case *ast.ForStmt:
		nl := w.newLoop()
		if s.Init != nil {
			w.stmt(s.Init, nl, fail)
		}
		if s.Cond != nil {
			w.expr(s.Cond, nl, fail)
		}
		if s.Post != nil {
			w.stmt(s.Post, nl, fail)
		}
		if s.Body != nil {
			w.stmt(s.Body, nl, fail)
		}
	case *ast.RangeStmt:
		nl := w.newLoop()
		if s.X != nil {
			w.expr(s.X, nl, fail)
		}
		if s.Key != nil {
			w.expr(s.Key, nl, fail)
		}
		if s.Value != nil {
			w.expr(s.Value, nl, fail)
		}
		if s.Body != nil {
			w.stmt(s.Body, nl, fail)
		}
	case *ast.LabeledStmt:
		w.stmt(s.Stmt, loop, fail)
	case *ast.CaseClause:
		for _, c := range s.Body {
			w.stmt(c, loop, fail)
		}
	case *ast.CommClause:
		for _, c := range s.Body {
			w.stmt(c, loop, fail)
		}
	case *ast.SwitchStmt:
		if s.Tag != nil {
			w.expr(s.Tag, loop, fail)
		}
		if s.Body != nil {
			w.stmt(s.Body, loop, fail)
		}
	case *ast.SelectStmt:
		if s.Body != nil {
			w.stmt(s.Body, loop, fail)
		}
	case *ast.AssignStmt:
		// 缓存字段被直接赋值（`s.sysCfgCache[key] = …`）是 setter 的识别特征
		for _, lhs := range s.Lhs {
			if idx, ok := lhs.(*ast.IndexExpr); ok && sysCfgIsCacheSelector(idx.X) {
				w.rec.writesCacheVal = true
			}
		}
		w.expr(s, loop, fail)
	default:
		w.expr(s, loop, fail)
	}
}

// newLoop 发一个**循环编号**：写点与失效调用必须拿到同一个编号才算"在同一个循环体内"（M9 抓的就是编号丢失）。
func (w *sysCfgWalker) newLoop() int {
	w.n++
	return w.n
}

// condText 把条件表达式还原成文本（喂给 sysCfgIsFailCond 判"这是不是失败/回滚分支"）。
func (w *sysCfgWalker) condText(cond ast.Expr) string {
	if cond == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, w.fset, cond); err != nil {
		return ""
	}
	return buf.String()
}

// expr 收集该语句里的所有调用（含嵌套表达式与闭包体内的；闭包写点今天不存在，
// 真出现时 W1 的下限读数会先把它点出来）。
func (w *sysCfgWalker) expr(n ast.Node, loop int, fail bool) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		w.noteCall(call, w.line(call), loop, fail)
		return true
	})
}

// noteCall 把一次调用归成三档：裸写 system_config／失效调用／其它（其它只记被调函数名与 Commit 位置）。
func (w *sysCfgWalker) noteCall(call *ast.CallExpr, line, loop int, fail bool) {
	name := sysCfgCalleeName(call)

	// `delete(s.sysCfgCache, k)`——失效入口唯一的机械锁用得上
	if name == "delete" && len(call.Args) > 0 && sysCfgIsCacheSelector(call.Args[0]) {
		w.rec.deletesCache = true
	}

	if name == sysCfgHookFn {
		keys, known := sysCfgArgKeys(call.Args, w.rec.params)
		// ★ 切片展开形态（`dropConfigCache(chunk...)`）：go/ast 把展开的那一项**照常放进 Args**，
		// 只在 CallExpr.Ellipsis 上记一个位置——于是 `chunk` 会被 *ast.Ident 那一支当成"具名常量"收进键集，
		// 与写点侧的 `args` 一比就判成"废的是另一个键"（首发真踩的假红）。展开＝键集在运行时才知道，一律豁免等值判定。
		if call.Ellipsis != token.NoPos {
			known = false
		}
		w.rec.hooks = append(w.rec.hooks, sysCfgCall{line: line, loopID: loop, failPath: fail, keys: keys, keysKnown: known})
		return
	}

	if form, keys, known, ok := sysCfgWriteCall(call, w.rec.params); ok {
		w.rec.writes = append(w.rec.writes, sysCfgCall{line: line, form: form, loopID: loop, failPath: fail, keys: keys, keysKnown: known})
	}

	if name == "Commit" {
		w.rec.commits = append(w.rec.commits, line)
	}
	if id, ok := call.Fun.(*ast.Ident); ok {
		w.rec.calls[id.Name] = append(w.rec.calls[id.Name], line)
	} else if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		w.rec.calls[sel.Sel.Name] = append(w.rec.calls[sel.Sel.Name], line)
	}
}

// sysCfgCalleeName 取被调函数的短名（SelectorExpr 取方法名），供失效／委托反查使用。
func sysCfgCalleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// sysCfgWriteCall 判"这一句是不是在写 system_config"：某个参数的字符串字面量里含表名，
// 且紧邻表名的动词是 INSERT／UPDATE／DELETE。
// 判据刻意要求动词与表名**相邻**，这样 `SELECT … FROM system_config` 与建表 DDL 不会被误计成写点，
// 而 `INSERT OR IGNORE INTO system_config`、`DELETE … WHERE key IN (…)`＋拼接这些真形态都能命中。
func sysCfgWriteCall(call *ast.CallExpr, params map[string]bool) (string, []string, bool, bool) {
	sqlIdx := -1
	form := ""
	for i, arg := range call.Args {
		text := sysCfgLiteralText(arg)
		if text == "" || !strings.Contains(text, sysCfgTable) {
			continue
		}
		up := strings.ToUpper(text)
		switch {
		case strings.Contains(up, "INSERT") && strings.Contains(up, "INTO "+strings.ToUpper(sysCfgTable)):
			form = "INSERT"
		case strings.Contains(up, "UPDATE "+strings.ToUpper(sysCfgTable)):
			form = "UPDATE"
		case strings.Contains(up, "DELETE FROM "+strings.ToUpper(sysCfgTable)):
			form = "DELETE"
		default:
			continue
		}
		sqlIdx = i
		break
	}
	if sqlIdx < 0 {
		return "", nil, false, false
	}
	keys, known := sysCfgArgKeys(call.Args[sqlIdx+1:], params)
	if call.Ellipsis != token.NoPos {
		known = false // 同一条豁免：`Exec(..., args...)` 的键集在展开项里，不拿它做等值判定
	}
	return form, keys, known, true
}

// sysCfgArgKeys 从参数表里取"能确定的键"：字符串字面量或具名常量。
// 函数形参（`key string` 这类委托语义）、下标（`kv[0]`）、切片展开（`chunk...`）、
// 以及任何非字面量表达式 ⇒ known=false，调用方据此豁免 W7 的等值判定。
func sysCfgArgKeys(args []ast.Expr, params map[string]bool) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}
	var keys []string
	for _, arg := range args {
		switch e := arg.(type) {
		case *ast.BasicLit:
			if e.Kind == token.STRING {
				keys = append(keys, sysCfgUnquote(e.Value))
				continue
			}
			return nil, false
		case *ast.Ident:
			if params[e.Name] {
				return nil, false // 形参不是"具名常量"，拿它判等值会把委托形态读成假红
			}
			keys = append(keys, e.Name)
			continue
		default:
			return nil, false
		}
	}
	return keys, len(keys) > 0
}

// sysCfgLiteralText 把一个表达式里所有字符串字面量拼起来（覆盖 `a + b` 拼接与 `strings.Join` 混排的形态）。
func sysCfgLiteralText(e ast.Expr) string {
	var parts []string
	ast.Inspect(e, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			parts = append(parts, sysCfgUnquote(lit.Value))
		}
		return true
	})
	return strings.Join(parts, " ")
}

// sysCfgUnquote 取字面量的**值**：先走 strconv.Unquote 拿转义语义，失败（原始串等）再裸剥引号/反引号。
// 表名与键名两侧都过它，锚才不会比歪。
func sysCfgUnquote(raw string) string {
	if v, err := strconv.Unquote(raw); err == nil {
		return v
	}
	return strings.Trim(raw, `"'`+"`")
}

// sysCfgIsCacheSelector 认出形如 `s.sysCfgCache` 的选择器（缓存字段本身，不是失效函数调用）。
// 它服务 setter 那一档：`SetConfig` 是**直接写缓存值**而不是调 dropConfigCache，W2 靠这个特征把它认成合法自翻。
func sysCfgIsCacheSelector(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == sysCfgCacheField
}

// sysCfgIsFailCond 认出 `err != nil` 这一类失败分支（含 `perr != nil`、`if err := …; err != nil`）。
// 只认"!= nil"，`err == nil` 是成功路径不能算。
func sysCfgIsFailCond(cond string) bool {
	if cond == "" {
		return false
	}
	return strings.Contains(cond, "!= nil") && strings.Contains(strings.ToLower(cond), "err")
}

// sysCfgKeysIntersect W7 的等值判据本体：**两侧键集有交集即算钩对了键**（写点一次清多把、钩子按分片各清一把都合法），
// 完全不相交才是"挂了钩、废的是另一个键"。两侧键集只在"都是字面量或具名常量"时才成立，
// 任一侧 known=false ⇒ 由调用方豁免，不硬猜。
func sysCfgKeysIntersect(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// evaluateSysCfgGate 把七条腿＋两条配套锁折成一个判决（干净仓必须返回空切片）。
// outsideFiles 传 nil 时跳过 W4（反证只需要看 store 内部）。
func evaluateSysCfgGate(storeFiles, outsideFiles map[string]string) []sysCfgFinding {
	pts, findings := scanSystemConfigWritePoints(storeFiles)

	// W1：现算全集＋下限（文案打实读数，绝不把 8 写成"应该有 8 个"）
	if len(pts) < sysCfgWritePointMin {
		findings = append(findings, sysCfgFinding{"W1", "现读 system_config 裸写点 " + strconv.Itoa(len(pts)) +
			" 处，低于下限 " + strconv.Itoa(sysCfgWritePointMin) + "（现读清单：" + sysCfgPointNames(pts) +
			"）⇒ 字面量锚漂了/写点被合并／拼接，整段枚举正在静默失明"})
	}

	// W4：store 之外出现写 SQL 即红（钩子物理上只在 store 里，包外必须走 SetConfig）
	if outsideFiles != nil {
		n, names := sysCfgOccurrencesIn(outsideFiles)
		if n > 0 {
			findings = append(findings, sysCfgFinding{"W4", "store 包之外有 " + strconv.Itoa(n) + " 处写 " +
				sysCfgTable + " 的 SQL：" + names + "；缓存失效腿只在 internal/store 里，包外裸写＝无人失效"})
		}
		// W4 的正向对照：store 内确有写点，否则"包外零命中"可能只是判据空转
		m, _ := sysCfgOccurrencesIn(storeFiles)
		if m < sysCfgWritePointMin {
			findings = append(findings, sysCfgFinding{"W4", "store 内写点读数 " + strconv.Itoa(m) +
				" 低于下限，跨包负向锁失去对照（判据空转）"})
		}
	}

	// W5：动态表名的那条缝——`scaleColumnTx` 的目标来自派生清单，字面量锚看不见
	// 读法：从 rebaseValueHolders 的 var 声明里 ast 取复合字面量第一元（表名），逐条断"表名≠配置表"
	tables, ok := sysCfgHolderTables(storeFiles)
	if !ok {
		findings = append(findings, sysCfgFinding{"W5", "读不到 rebaseValueHolders 清单（改名／挪走／形态变了）" +
			"⇒ 动态表名那条缝失去射程，本闸唯一够不到的形态正在变成没人在看"})
	} else {
		if len(tables) < sysCfgHolderMin {
			findings = append(findings, sysCfgFinding{"W5", "rebaseValueHolders 现读 " + strconv.Itoa(len(tables)) +
				" 条，低于下限 " + strconv.Itoa(sysCfgHolderMin) + "（清单腿漂了）"})
		}
		for _, tb := range tables {
			if strings.EqualFold(tb, sysCfgTable) {
				findings = append(findings, sysCfgFinding{"W5", "rebaseValueHolders 里出现了 " + tb +
					"：这条缩放腿不经过任何带钩子的 Go 写点，落库后没人失效（钩子物理够不到的旁路）"})
			}
		}
	}

	// 配套锁：失效入口唯一——`delete(s.sysCfgCache, …)` 只许出现在 dropConfigCache 函数体内
	for _, f := range mustParseSysCfgFuncs(storeFiles) {
		if f.deletesCache && !strings.HasSuffix(f.name, "."+sysCfgHookFn) {
			findings = append(findings, sysCfgFinding{"HOOK", f.file + " 的 " + f.name +
				" 直接 delete(" + sysCfgCacheField + ",…)，绕过唯一入口 " + sysCfgHookFn + "（第二把尺子）"})
		}
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Leg != findings[j].Leg {
			return findings[i].Leg < findings[j].Leg
		}
		return findings[i].Msg < findings[j].Msg
	})
	return findings
}

// mustParseSysCfgFuncs 是给配套锁用的薄包装：解析错误在 scan 里已经出过，这里只取函数读数。
func mustParseSysCfgFuncs(files map[string]string) []*sysCfgFunc {
	funcs, _ := parseSysCfgFuncs(files)
	return funcs
}

// sysCfgOccurrencesIn 现数一批文件里写 system_config 的调用次数与所属函数名（W1/W4 的读数腿）。
func sysCfgOccurrencesIn(files map[string]string) (int, string) {
	funcs, _ := parseSysCfgFuncs(files)
	total := 0
	var names []string
	for _, f := range funcs {
		if len(f.writes) == 0 {
			continue
		}
		total += len(f.writes)
		// 点名到**文件**：这条腿的红是"该回哪一处去改"的唯一定位线索，只报函数名在跨包场景下不够（同名方法两处）
		names = append(names, filepath.Base(f.file)+":"+f.name+"×"+strconv.Itoa(len(f.writes)))
	}
	sort.Strings(names)
	return total, strings.Join(names, "、")
}

// sysCfgHolderTables 取 rebaseValueHolders 清单里每一条的第一个字面量（表名）。
func sysCfgHolderTables(files map[string]string) ([]string, bool) {
	found := false
	var tables []string
	for path, src := range files {
		if !strings.Contains(src, "rebaseValueHolders") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok || len(spec.Names) == 0 || spec.Names[0].Name != "rebaseValueHolders" || len(spec.Values) == 0 {
				return true
			}
			lit, ok := spec.Values[0].(*ast.CompositeLit)
			if !ok {
				return false
			}
			found = true
			for _, el := range lit.Elts {
				row, ok := el.(*ast.CompositeLit)
				if !ok || len(row.Elts) == 0 {
					continue
				}
				if bl, ok := row.Elts[0].(*ast.BasicLit); ok && bl.Kind == token.STRING {
					tables = append(tables, sysCfgUnquote(bl.Value))
				}
			}
			return false
		})
	}
	sort.Strings(tables)
	return tables, found
}

// sysCfgPointNames 把写点清单压成 `文件名:函数名[归类]` 的有序串（同一函数多处写点只点一次名）。
// 它是 W1 那条"低于下限"红项里唯一的定位线索，所以必须带文件名——只报函数名在跨文件同名方法下不够用。
func sysCfgPointNames(pts []sysCfgWritePoint) string {
	seen := map[string]bool{}
	var names []string
	for _, p := range pts {
		key := p.File + "#" + p.Func
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, filepath.Base(p.File)+":"+p.Func+"["+p.Kind.String()+"]")
	}
	sort.Strings(names)
	return strings.Join(names, "、")
}

// sysCfgStoreFilesFromDisk 从包目录向上找模块根（go.mod），把全部非测试 .go 收进一张表后交给
// sysCfgPartition 按**包**归属，返回 store 包全部非测试文件与其余包里**文本含 system_config** 的文件（W4 的预筛，判据仍是 ast）。
func sysCfgStoreFilesFromDisk(t *testing.T) (storeFiles, outsideFiles map[string]string) {
	t.Helper()
	root := sysCfgModuleRoot(t)
	all := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "node_modules", "testdata", "dist", "web", ".venv":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		all[filepath.ToSlash(rel)] = string(src)
		return nil
	})
	if err != nil {
		t.Fatalf("枚举模块源码失败（%s）：%v", root, err)
	}
	storeFiles, outsideFiles = sysCfgPartition(all)
	if len(storeFiles) < 30 {
		t.Fatalf("store 包只读到 %d 个非测试文件，枚举源本身不可信（模块根=%s）", len(storeFiles), root)
	}
	return storeFiles, outsideFiles
}

// 包归属判据的两把锚（W4 用它分「包内／包外」）：
// 口径必须是 **import path 意义上的包**，不是目录前缀——把写点搬进 internal/store/sub/ 仍是另一个包，
// 按目录前缀判会把它误归包内（第十二枚反证 M12 钉的就是这一族）。
const (
	sysCfgStoreDir = "internal/store/"
	sysCfgStorePkg = "store"
)

// sysCfgPartition 是本闸唯一的路由判据：**目录前缀 ∧ package 子句**两条同时成立才算"被守的那一个包"。
// 为什么不能只看目录前缀：`internal/store/sub/` 是**另一个包**，它物理上拿不到非导出的 `dropConfigCache`，
// 把它的写点归到 store 那一侧只会让它红在 W2（"本函数与调用方都没挂钩"）——那条文案是在叫开发者
// "去补一次失效调用"，而他在那一个包里**根本补不出来**；正确的归处是包外那一侧（W4），文案直接写明
// "缓存失效腿只在 internal/store 里，包外裸写必须走 SetConfig"。
// 现况实测：`internal/store/` 今天**没有子目录**（120 个文件全是 `package store`），这一层是防"将来搬家"，
// 反证＝M12（把假文件放在 `internal/store/sub/`，必须落到包外那一侧并红在 W4；分类器若退回前缀判＝红错一腿）。
func sysCfgPartition(all map[string]string) (storeFiles, outsideFiles map[string]string) {
	storeFiles = map[string]string{}
	outsideFiles = map[string]string{}
	for rel, src := range all {
		if sysCfgInStorePackage(rel, src) {
			storeFiles[rel] = src
			continue
		}
		if strings.Contains(src, sysCfgTable) { // 包外预筛：不含表名的文件与本闸无关，不必都塞进 ast
			outsideFiles[rel] = src
		}
	}
	return storeFiles, outsideFiles
}

// sysCfgInStorePackage 按**包**（import path）认归属，不认目录形状。
func sysCfgInStorePackage(rel, src string) bool {
	if !strings.HasPrefix(rel, sysCfgStoreDir) {
		return false
	}
	pkg, ok := sysCfgPackageClause(src)
	if !ok {
		// 连 package 子句都读不出来＝这压根不是一份 Go 源，别把它当写点归进被守那一侧（判据空转比假红更坏）
		return false
	}
	return pkg == sysCfgStorePkg
}

// sysCfgPackageClause 只解析 package 那一行（PackageClauseOnly，不做类型检查，亚毫秒）。
func sysCfgPackageClause(src string) (string, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "gate.go", src, parser.PackageClauseOnly)
	if err != nil || file == nil || file.Name == nil {
		return "", false
	}
	return file.Name.Name, true
}

// sysCfgModuleRoot 从当前工作目录向上找 go.mod（`go test` 的 cwd 就是包目录）。
func sysCfgModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("从 %s 向上找不到 go.mod（模块根定位失败）", dir)
	return ""
}

// sysCfgMutate 在内存副本上做一次变异：from 必须**恰好命中一次**，否则判"变异没生效"（
// 本仓纪律：反证喂进去的破坏如果没落到磁盘之外的那份文本上，红/绿都不作数）。
func sysCfgMutate(t *testing.T, files map[string]string, path, from, to string) map[string]string {
	t.Helper()
	src, ok := files[path]
	if !ok {
		t.Fatalf("内存副本里没有 %s（枚举源变了？）", path)
	}
	n := strings.Count(src, from)
	if n != 1 {
		t.Fatalf("变异锚在 %s 命中 %d 次（要求恰 1 次），先核对源码形态：%q", path, n, from)
	}
	out := make(map[string]string, len(files))
	for k, v := range files {
		out[k] = v
	}
	out[path] = strings.Replace(src, from, to, 1)
	return out
}

// sysCfgMutateCount 与 sysCfgMutate 同口径，只是锚允许命中 N 次（调用点整批改名那一类反证）。
func sysCfgMutateCount(t *testing.T, files map[string]string, path, from, to string, want int) map[string]string {
	t.Helper()
	src, ok := files[path]
	if !ok {
		t.Fatalf("内存副本里没有 %s（枚举源变了？）", path)
	}
	if n := strings.Count(src, from); n != want {
		t.Fatalf("变异锚在 %s 命中 %d 次（要求 %d 次），先核对源码形态：%q", path, n, want, from)
	}
	out := make(map[string]string, len(files))
	for k, v := range files {
		out[k] = v
	}
	out[path] = strings.Replace(src, from, to, want)
	return out
}

// sysCfgLegs 把发现清单摊成「腿名|消息」的稳定可读形态，供反证逐条点名比对
// （判据级断言不能代替守卫级，所以这里只出读数，实际分派仍走守卫入口那一次调用）。
func sysCfgLegs(findings []sysCfgFinding) []string {
	var legs []string
	for _, f := range findings {
		legs = append(legs, f.Leg+"|"+f.Msg)
	}
	return legs
}

// ───────────────────────── 落盘扫描：干净仓必须全绿 ─────────────────────────

func TestSysCfgWritePointGateCleanScan(t *testing.T) {
	storeFiles, outsideFiles := sysCfgStoreFilesFromDisk(t)

	pts, _ := scanSystemConfigWritePoints(storeFiles)
	t.Logf("W1 现算写点＝%d 处（下限 %d）：%s", len(pts), sysCfgWritePointMin, sysCfgPointNames(pts))

	findings := evaluateSysCfgGate(storeFiles, outsideFiles)
	if len(findings) > 0 {
		for _, f := range findings {
			t.Errorf("[%s] %s", f.Leg, f.Msg)
		}
		t.Fatalf("配置缓存裸写点静态闸在干净仓上判红 %d 条（产品侧应一条不缺挂钩）", len(findings))
	}

	// 正向对照：归类三档各自非空（只有"零红项"没有"有东西在守"，等于没跑闸）
	var setter, same, deleg int
	for _, p := range pts {
		switch p.Kind {
		case kindSetter:
			setter++
		case kindSameFunc:
			same++
		case kindDelegated:
			deleg++
		}
	}
	if setter != 1 || same < 5 || deleg < 2 {
		t.Errorf("归类读数异常：SetterSelf=%d（须 1）／HookedSameFunc=%d（须 ≥5）／DelegatedToCaller=%d（须 ≥2）", setter, same, deleg)
	}
	t.Logf("W2/W3 分派读数：setter=%d·本函数挂钩=%d·委托=%d·Unhooked=0（现读 %d 处物理写点）", setter, same, deleg, len(pts))

	// W4 的对照：包外零命中，但判据确实看见了 store 里那 8 处（否则"零命中"是空转）
	outN, outNames := sysCfgOccurrencesIn(outsideFiles)
	inN, _ := sysCfgOccurrencesIn(storeFiles)
	t.Logf("W4 读数：store 之外写点=%d（%s）／store 内=%d（正对照）", outN, outNames, inN)
	if inN < sysCfgWritePointMin {
		t.Errorf("store 内读数 %d 低于下限，负向锁没有对照支撑", inN)
	}

	tables, ok := sysCfgHolderTables(storeFiles)
	if !ok {
		t.Fatalf("W5 读不到 rebaseValueHolders 清单")
	}
	t.Logf("W5 动态表名清单现读 %d 条：%s（当前不含 %s）", len(tables), strings.Join(tables, "、"), sysCfgTable)
}

// ───────────────────────── 反证矩阵：逐枚各断一个本枚独有读数 ─────────────────────────

// TestSysCfgWritePointGateMutations 逐枚变异喂内存副本，判两件事：
// ① 该枚点名的那一腿必须红；② **别的腿不许跟着红**（射程分不开＝同色分不开，两枚反证打同一句文案没有意义）。
func TestSysCfgWritePointGateMutations(t *testing.T) {
	storeFiles, _ := sysCfgStoreFilesFromDisk(t)

	cases := []struct {
		name    string
		files   map[string]string
		outside map[string]string // 非 nil 才让 W4 的跨包负向腿参与判定（M11／M12 用）
		wantLeg string
		wantHas string // 红项文案里必须出现的点名串
	}{
		{
			name:    "M1·摘掉 SetFxRates 的失效行（W2 点名函数）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/currency.go", "\n\t\ts.dropConfigCache(ConfigFxRates) // 裸 DELETE 成功后失效，清空即时可见（P2⑤ 补腿）", ""),
			wantLeg: "W2", wantHas: "SetFxRates",
		},
		{
			name:    "M2·把 PruneScrapeLedger 的失效调用改名（改名式破坏）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/kb_scrape_ledger.go", "s.dropConfigCache(chunk...)", "s.dropConfigCacheX(chunk...)"),
			wantLeg: "W2", wantHas: "PruneScrapeLedger",
		},
		{
			name:    "M3·新增一处不挂钩的裸写（本闸存在的理由）",
			files:   sysCfgMutateFake(t, storeFiles),
			wantLeg: "W2", wantHas: "GateMutationWriter",
		},
		{
			name:    "M4·剥掉事务助手的 cache-hook:caller 标记（委托从声明里掉出来）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/points_rate_rebase.go", "// cache-hook:caller 本函数只在事务内裸写 system_config，缓存失效由调用方在提交成功后做\n// （★ 2026-10-11 静态闸 `systemconfig_writepoint_gate_test.go` W3 认这一行；删掉它当场判红。", "// （反证 M4：标记行已剥。"),
			wantLeg: "W3", wantHas: "rebasePointsMintedConfig",
		},
		{
			name:    "M5·标记函数的调用方整批改名（死码豁免）",
			files:   sysCfgMutateCount(t, storeFiles, "internal/store/points_rate_rebase.go", "upsertConfigTx(tx, d,", "upsertConfigTxOrphan(tx, d,", 3),
			wantLeg: "W3", wantHas: "死码豁免",
		},
		{
			name:    "M6·SQL 字面量锚漂走（W1 下限，整段枚举正在失明）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/billing.go", `"INSERT INTO system_config (key,value) SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM system_config WHERE key=?)"`, `"INSERT INTO cfg_kv (key,value) SELECT ?,? WHERE NOT EXISTS (SELECT 1 FROM cfg_kv WHERE key=?)"`),
			wantLeg: "W1", wantHas: "低于下限",
		},
		{
			name:    "M7·往动态表名清单里加一行配置表（钩子够不到的旁路）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/points_rate_rebase.go", "var rebaseValueHolders = []struct{ table, column string }{\n", "var rebaseValueHolders = []struct{ table, column string }{\n\t{\"system_config\", \"value\"}, // 反证 M7\n"),
			wantLeg: "W5", wantHas: "物理够不到",
		},
		{
			name:    "M8·失效挪到落库之前（W6；W2 必须仍绿＝两腿射程不同）",
			files:   sysCfgMutateOrder(t, storeFiles),
			wantLeg: "W6", wantHas: "晚于该写点",
		},
		{
			name:    "M9·把循环体里的失效行挪出循环（反证锁本身写法的那枚）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/billing.go", "\t\ts.dropConfigCache(kv[0])\n\t}\n}", "\t}\n\ts.dropConfigCache(kv[0])\n}"),
			wantLeg: "W6", wantHas: "同一个循环体内",
		},
		{
			name:    "M10·钩子废的是另一个键（W7）",
			files:   sysCfgMutate(t, storeFiles, "internal/store/currency.go", "s.dropConfigCache(ConfigFxRates) // 裸 DELETE 成功后失效", "s.dropConfigCache(ConfigFxRatesWrong) // 反证 M10：键写错了"),
			wantLeg: "W7", wantHas: "另一个键",
		},
		{
			name:  "M11·包外多出一处裸写（W4 负向腿的存在理由）",
			files: storeFiles,
			// 只给 W4 的"包外"这一侧喂一份假文件：真仓里 store 之外零写点，负向锁若没有对照支，
			// 和"判据空转"完全同形（同 §一·5 那条「负向锁必须配正向对照」）。
			outside: map[string]string{"internal/api/gate_mutation_outside.go": `package api

// GateMutationOutside 反证 M11 专用（只在内存里存在，磁盘上没有这个文件）。
func (s *Server) GateMutationOutside(key, value string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE system_config SET value=? WHERE key=?", value, key)
	return err
}
`},
			wantLeg: "W4", wantHas: "store 包之外",
		},
		{
			// 这一枚测的是**分类器**，不是判据：方案 §4.4 承诺"包搬家不是出路"，靠的就是按包归属这一层，
			// 而 M11 那枚假文件在 `internal/api`（前缀判与包判结果相同），对分类器本身完全无感。
			name:    "M12·写点搬进 internal/store/sub（同目录树、不同包）⇒ 必须归到包外那一侧",
			files:   storeFiles,
			outside: sysCfgMutateSubPackage(t, storeFiles),
			wantLeg: "W4", wantHas: "gate_mutation_sub.go",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings := evaluateSysCfgGate(c.files, c.outside)
			if len(findings) == 0 {
				t.Fatalf("反证失效：这一枚破坏喂进去，闸门全绿（等于没守）")
			}
			var hit *sysCfgFinding
			for i := range findings {
				if findings[i].Leg == c.wantLeg && strings.Contains(findings[i].Msg, c.wantHas) {
					hit = &findings[i]
				}
			}
			if hit == nil {
				t.Fatalf("期望红在 %s（含「%s」），实得：%v", c.wantLeg, c.wantHas, sysCfgLegs(findings))
			}
			// 本枚独有读数：除 PARSE 外不允许出现别的腿（同色分不开＝反证不作数）
			for _, f := range findings {
				if f.Leg != c.wantLeg {
					t.Errorf("反证不干净：%s 之外还红了 [%s] %s", c.wantLeg, f.Leg, f.Msg)
				}
			}
			t.Logf("红项＝[%s] %s", hit.Leg, hit.Msg)
		})
	}
}

// sysCfgMutateFake 给 M3 用：往内存副本里塞一份**假文件**（含裸写、无钩子），验证"新增第七处不挂钩"当场红。
func sysCfgMutateFake(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(files)+1)
	for k, v := range files {
		out[k] = v
	}
	out["internal/store/gate_mutation_fake.go"] = `package store

// GateMutationWriter 反证 M3 专用（只在内存里存在，磁盘上没有这个文件）。
func (s *Store) GateMutationWriter(key string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM system_config WHERE key=?", key)
	return err
}
`
	return out
}

// sysCfgMutateSubPackage 给 M12 用：验的是**分类器**而不是判据。
// 假文件放在 `internal/store/sub/`（同目录树、不同包），必须被路由到**包外那一侧**，
// 于是红在 W4（"包外裸写＝无人失效"，文案指向走 SetConfig）；
// 若路由退回「只看目录前缀」，它会被当成 store 内的写点，红在 W2 且文案叫开发者
// 「去本函数补一次 dropConfigCache」——而 `sub` 包**物理上拿不到**那个非导出方法，
// 这一枚抓的就是"锁把人指去一条走不通的路"。
// 这里先直接对分区判据取证（不经闸门），是因为分类器错了的时候，闸门仍可能因为**别的原因**红在 W4/W2，
// 那一红对本枚的点名串恰好不含假文件名，测试会红在"没找到期望文案"，把归因推给判据而不是路由。
func sysCfgMutateSubPackage(t *testing.T, storeFiles map[string]string) map[string]string {
	t.Helper()
	const fake = "internal/store/sub/gate_mutation_sub.go"

	all := make(map[string]string, len(storeFiles)+1)
	for k, v := range storeFiles {
		all[k] = v
	}
	all[fake] = `package sub

// GateMutationSub 反证 M12 专用（只在内存里存在，磁盘上没有这个目录）。
// 这一包的写法就是本闸要拦的形态：裸 DELETE 之后没有任何失效腿可挂（dropConfigCache 非导出）。
func GateMutationSub(dbi any, key string) error {
	_, err := db.Exec(dbi, db.CurrentDialect(), "DELETE FROM system_config WHERE key=?", key)
	return err
}
`

	in, outside := sysCfgPartition(all)
	if _, ok := outside[fake]; !ok {
		t.Fatalf("路由把 %s 判进了 store 包那一侧＝按目录前缀认归属（本枚抓的就是这一层）；"+
			"那样的话它会红在 W2，而 W2 的文案叫开发者去补一次 dropConfigCache——sub 包拿不到那个非导出方法", fake)
	}
	if len(in[fake]) != 0 {
		t.Fatalf("同一份文件同时出现在两侧＝分区判据自相矛盾（%s）", fake)
	}
	// 正对照：真 store 文件必须仍在被守那一侧，否则"假文件被甩到包外"可能只是因为两侧都空
	if _, ok := in["internal/store/billing.go"]; !ok {
		t.Fatalf("分区把真 store 文件也判到包外去了（正对照缺失，此时 outside 里出现假文件不说明任何事）")
	}
	if len(in) != len(storeFiles) {
		t.Fatalf("分区后 store 侧文件数从 %d 变成 %d＝路由改动了既有归属", len(storeFiles), len(in))
	}
	return outside
}

// sysCfgMutateOrder 给 M8 用：把 SetFxRates 的失效整句**挪到**裸 DELETE 之前，并删掉原来那一行
// （只加不删的话，"写点后头还有个失效"这条判据仍成立，红不到本枚头上）。
func sysCfgMutateOrder(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	src := files["internal/store/currency.go"]
	drop := "\t\ts.dropConfigCache(ConfigFxRates) // 裸 DELETE 成功后失效，清空即时可见（P2⑤ 补腿）\n"
	del := "\t\t_, err := db.Exec(s.db, db.CurrentDialect(), \"DELETE FROM system_config WHERE key=?\", ConfigFxRates)"
	if strings.Count(src, drop) != 1 || strings.Count(src, del) != 1 {
		t.Fatalf("M8 变异锚命中数不对（先核对 currency.go 形态）")
	}
	out := make(map[string]string, len(files))
	for k, v := range files {
		out[k] = v
	}
	mutated := strings.Replace(src, drop, "", 1)
	mutated = strings.Replace(mutated, del, "\t\ts.dropConfigCache(ConfigFxRates)\n"+del, 1)
	out["internal/store/currency.go"] = mutated
	return out
}
