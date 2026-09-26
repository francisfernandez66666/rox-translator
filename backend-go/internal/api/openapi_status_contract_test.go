// ============ openapi_status_contract_test.go · 职责说明 ============
// 2026-09-26 发布前 UAT 修复批 I-7：F-64①「对外契约状态码诚实」的机制闸门。
//
// 缺陷（本轮 UAT 实测点到的形态）：开放 API 把失败藏在 200 里——
// /openapi/v1/tasks 缺 text、余额不足、文件超上限……HTTP 层一律 200，
// 只有响应体里 `success:false` + `error_code` 才是真相；而同一条契约的鉴权分支又回 401/403/404/429。
// 于是：
//   - 客户的通用 HTTP 客户端/重试器/网关告警/APM 错误率把失败统计成成功（最贵的一条：
//     开放 API 的调用方是**别人的程序**，状态码是它唯一的分支语言）；
//   - 同仓对外文档 openapi.v1.json 早就声明了 402/429 与 Error 模型，
//     实现比文档落后 ⇒ 照文档写重试逻辑的人反而拿不到 4xx；
//   - 「200 承载失败」的写法在 internal/api 里约 235 处，一次性扫是 >3000 行的大爆炸
//     （AGENTS.md 三「改动原则」第一条禁止），所以本批只做第①档（对外契约面），
//     并**用闸门把这一档焊死**，防止它被后人悄悄改回去。
//
// 本文件的锁（缺一不可，各自对应一类会复发的漂移）：
//
//	A 源码形态锁：openapi 面的 handler 及其**私有 helper**里不得再出现「内联 4xx/5xx writeJSON」
//	  或「200 + success:false」（AST 判定，注释里的字样不算，避免负向 grep 锁的老坑）。
//	  ★ 射程按文件分两档：api_openapi_tasks.go 整文件扫（错误分支大多在 openAPITaskCreateText
//	    这类非 handleOpenAPI 前缀的 helper 里，按名字圈定会漏——M7 变异实测过），
//	    admin_openapi.go / openapi_v1.go 只扫 handleOpenAPI*（同文件住着超管接口，属第②档）。
//	A2 路由覆盖锁：从**注册现场**（全包 `HandleFunc("/openapi/…", …)`）反查每条对外路由的 handler
//	  定义文件，要求它落在 A 段射程内。堵住三种清单漂移：新增对外面写在新文件、
//	  handler 改名不再带 handleOpenAPI 前缀、文件搬走而清单没跟着改。
//	B 文档↔状态表双向穷举锁：规范 Error.enum 与 internal/errors 的 openAPIStatusByCode
//	  必须一一对应——文档有码而表没登记 ⇒ 客户拿到 500；表有码而文档没写 ⇒ 客户无从分支；
//	C 状态可达锁：规范里声明过的每个 4xx/5xx 状态必须至少有一个码映射到它，
//	  反之每个码的状态必须在某条路径上被声明（堵「文档写着 418、代码永远不会发」这类装饰性条目）；
//	D 端到端形态锁：真发一次请求，验 4xx + code/error_code 双键同值 + trace_id 出现；
//	E trace_id 腿：有 trace 的 ctx 必须原样带出、无 trace 不得造空键。
//
// 十处变异（M1～M10）逐条实测为红，见《缺陷核实与修复文档_20260926.md》批 I-7 执行账。
//
// 方言：自钉 SQLite 内存库（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestOpenAPIContract
// =============================================
package api

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	apierrors "translator/internal/errors"
)

// f64OpenAPIWholeFiles 整文件都属对外契约面的宿主文件：文件里除错误出口之外的每一处
// 「内联 4xx writeJSON」或「200 + success:false」都是本批要焊死的形态。
//
// ★ 为什么这里必须整文件扫、不能按 handler 名圈定（2026-09-26 变异矩阵 M7 真踩出来的射程洞）：
// api_openapi_tasks.go 里除了 handleOpenAPI* 还住着 openAPITaskCreateText /
// openAPITaskCreateFiles / gateUserMessage 这类**私有 helper**，
// 真正的参数校验与错误分支大多写在它们里面。上一版按 `strings.HasPrefix(name,"handleOpenAPI")`
// 圈定，结果在 helper 里复活一条 `writeJSON(w, 400, map{"success":false,"error_code":"bad_request"})`
// 闸门照样绿灯——把内联写法搬回 helper 就能绕过锁，等于没锁。
var f64OpenAPIWholeFiles = []string{"api_openapi_tasks.go"}

// f64OpenAPIHandlerFiles 混住「超管接口 + 开放 API 读接口」的文件，只能按 handleOpenAPI* 前缀圈定
// （超管那部分属第②档：状态码翻转要与前端 catch 同批，本批不接管）。
var f64OpenAPIHandlerFiles = []string{"admin_openapi.go", "openapi_v1.go"}

// f64IsOpenAPIFunc 判定函数是否属开放 API 面（只对 f64OpenAPIHandlerFiles 有意义）：
// handler 名以 handleOpenAPI 起头。
func f64IsOpenAPIFunc(name string) bool {
	return strings.HasPrefix(name, "handleOpenAPI")
}

// f64ScanCovers 判定「某个文件里的某个函数」是否在 A 段射程内。
// 路由覆盖锁（F64RouteCoverage）与形态锁共用这一条判据，两者才不会各说各话。
func f64ScanCovers(file, funcName string) bool {
	if slicesContains(f64OpenAPIWholeFiles, file) {
		return true
	}
	return slicesContains(f64OpenAPIHandlerFiles, file) && f64IsOpenAPIFunc(funcName)
}

func slicesContains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// A 段：openapi 面源码形态锁。
func TestOpenAPIContractF64NoInlineNoTwoHundredFailure(t *testing.T) {
	seenIn := map[string]int{} // 每个文件射程内实际看到的 writeJSON 处数（防「射程空转＝恒绿」的假绿）
	for _, fname := range append(append([]string{}, f64OpenAPIWholeFiles...), f64OpenAPIHandlerFiles...) {
		whole := slicesContains(f64OpenAPIWholeFiles, fname)
		path := fname
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("扫描根失效（应在 backend-go/internal/api 下运行）：%v", err)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		seen := 0
		ast.Inspect(file, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			if !whole && !f64IsOpenAPIFunc(fd.Name.Name) {
				return true // 超管面：本批不接管（第②档），继续往下找嵌套函数
			}
			ast.Inspect(fd.Body, func(n2 ast.Node) bool {
				ce, ok := n2.(*ast.CallExpr)
				if !ok || len(ce.Args) != 3 {
					return true
				}
				fn, ok := ce.Fun.(*ast.Ident)
				if !ok || fn.Name != "writeJSON" {
					return true
				}
				lit, ok := ce.Args[1].(*ast.BasicLit)
				if !ok || lit.Kind != token.INT {
					return true // 变量状态码（唯一错误出口 writeOpenAPIError）另议，本锁只管字面量
				}
				seen++
				status, _ := strconv.Atoi(lit.Value)
				pos := fset.Position(ce.Pos()).Line
				if status >= 400 {
					t.Errorf("%s:%d %s 内仍有内联 4xx/5xx writeJSON（F-64① 要求一律走 writeOpenAPIError，"+
						"状态码由 internal/errors 单点表决定，不在 handler 里写数字）", path, pos, fd.Name.Name)
					return true
				}
				if status == 200 && mapHasSuccessFalse(ce.Args[2]) {
					t.Errorf("%s:%d %s 出现「200 承载失败」（success:false）——开放 API 的调用方是程序，"+
						"200 就是它眼里的成功，失败必须换成诚实状态码", path, pos, fd.Name.Name)
				}
				return true
			})
			return true
		})
		seenIn[path] = seen
		// 假绿防护只针对「整文件扫」这一档：这些文件是对外契约的主体，射程内一处 writeJSON
		// 都扫不到就意味着文件被搬空或判定式失效（openapi_v1.go 是纯规范端点，用 w.Write 直出
		// 嵌入的 JSON，本来就没有 writeJSON，故不吃这条下限）。
		if whole && seen == 0 {
			t.Errorf("%s 射程内一处 writeJSON 都没看到：要么文件被搬空、要么判定式失效，"+
				"此时本锁会恒绿（假绿比缺测更有害）", path)
		}
	}
	t.Logf("A 段射程实测：%v（openapi 面 writeJSON 处数）", seenIn)
}

// A 段配套：路由覆盖锁——「凡是挂在 /openapi/ 上的路由，其 handler 必须在形态锁射程内」。
//
// 为什么要单独一把锁：A 段的射程由两份**手写文件清单**决定，清单会漂移。
// 新增一条 /openapi/ 路由、或把 handler 搬到新文件、或在混住式文件里给它换个不以
// handleOpenAPI 起头的名字——三种情况都会让「对外契约面」悄悄长出锁外的角落，
// 而 A 段照样绿。2026-09-26 M7 变异（把内联 4xx 写回私有 helper）就是这个盲区的一种，
// 那份变异由整文件扫堵住，这一把锁堵住其余两种。
//
// 判据取自**注册现场**（全仓非测试 .go 里形如 `HandleFunc("/openapi/…", s.xxx)` 的语句），
// 不取自手写清单，否则就是拿结论证明结论。
func TestOpenAPIContractF64RouteCoverage(t *testing.T) {
	routes := f64OpenAPIRoutes(t)
	if len(routes) < 10 {
		t.Fatalf("只解析到 %d 条 /openapi/ 路由：解析式失效或被大面积改写，覆盖锁失去意义（当前口径 11 条）", len(routes))
	}
	owners := f64FuncOwners(t)
	scanned := map[string]bool{}
	for _, rt := range routes {
		file, ok := owners[rt.handler]
		if !ok {
			t.Errorf("%s 的 handler %s 在本包找不到定义（路由表与代码已脱节）", rt.pattern, rt.handler)
			continue
		}
		if !f64ScanCovers(file, rt.handler) {
			t.Errorf("%s（%s 里的 %s，注册于 %s:%d）不在 F-64① 形态锁射程内："+
				"对外契约面的 handler 必须落在 f64OpenAPIWholeFiles（整文件扫）或"+
				"f64OpenAPIHandlerFiles + handleOpenAPI* 前缀里；若确属新增对外面，"+
				"请把文件登记进清单并按单点表发状态码", rt.pattern, file, rt.handler, rt.file, rt.line)
		}
		scanned[rt.pattern] = true
	}
	// 反向：清单里的文件一个都不许是空射程（搬空文件不删清单＝自欺）
	for _, fname := range append(append([]string{}, f64OpenAPIWholeFiles...), f64OpenAPIHandlerFiles...) {
		if _, err := os.Stat(fname); err != nil {
			t.Errorf("清单登记的文件 %s 已不存在：%v（清单要跟着搬迁一起改）", fname, err)
		}
	}
	if len(scanned) != len(routes) {
		t.Logf("重复 pattern（同一路径多次注册）：%d 条去重后 %d 条", len(routes), len(scanned))
	}
}

// f64OpenAPIRoute 一条 /openapi/ 前缀路由的注册现场。
type f64OpenAPIRoute struct {
	pattern string
	handler string // 已剥掉 s. / 接收者前缀的函数名
	file    string
	line    int
}

var f64HandleRe = regexp.MustCompile(`HandleFunc\(\s*"(/openapi/[^"]*)"\s*,\s*(?:[\w.]+\.)?([\w]+)\s*\)`)

// f64OpenAPIRoutes 扫描全包（非测试 .go）的 /openapi/ 路由注册语句。
func f64OpenAPIRoutes(t *testing.T) []f64OpenAPIRoute {
	t.Helper()
	var out []f64OpenAPIRoute
	for _, src := range f64PackageSources(t) {
		for i, line := range strings.Split(src.body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue // 注释里的示例注册语句不算
			}
			for _, m := range f64HandleRe.FindAllStringSubmatch(line, -1) {
				out = append(out, f64OpenAPIRoute{pattern: m[1], handler: m[2], file: src.name, line: i + 1})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pattern != out[j].pattern {
			return out[i].pattern < out[j].pattern
		}
		return out[i].file < out[j].file
	})
	return out
}

type f64Source struct {
	name string
	body string
}

// f64PackageSources 读出本目录全部非测试、非生成 go 文件（测试自己按目录运行）。
func f64PackageSources(t *testing.T) []f64Source {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	var out []f64Source
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("读取 %s: %v", n, err)
		}
		out = append(out, f64Source{name: n, body: string(b)})
	}
	if len(out) < 20 {
		t.Fatalf("只读到 %d 个源文件，扫描根不对（本包非测试文件量级 40+）", len(out))
	}
	return out
}

// f64FuncOwners 返回「函数名 → 定义文件」，只收**路由表能指向的那两类**：
// 自由函数，以及 *Server 上的方法（路由挂的都是 s.handleXxx）。
// 其它接收者（例如测试替身上再定义一遍的 WriteHeader）不参与映射，
// 否则同名方法会把「重复定义」误报成表冲突、锁还没跑就先自伤。
func f64FuncOwners(t *testing.T) map[string]string {
	t.Helper()
	owners := map[string]string{}
	fset := token.NewFileSet()
	for _, src := range f64PackageSources(t) {
		file, err := parser.ParseFile(fset, src.name, src.body, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", src.name, err)
		}
		for _, d := range file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name == nil {
				continue
			}
			if fd.Recv != nil && !f64RecvIsServer(fd) {
				continue
			}
			if _, dup := owners[fd.Name.Name]; dup {
				t.Errorf("%s 在两个文件里重复定义（映射表会指错文件、覆盖锁失去意义）", fd.Name.Name)
				continue
			}
			owners[fd.Name.Name] = src.name
		}
	}
	return owners
}

// f64RecvIsServer 判定方法的接收者类型是否为 *Server / Server。
func f64RecvIsServer(fd *ast.FuncDecl) bool {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return false
	}
	typ := fd.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	id, ok := typ.(*ast.Ident)
	return ok && id.Name == "Server"
}

// mapHasSuccessFalse 判断 map 字面量里是否存在键 "success" 且值为 false 字面量。
func mapHasSuccessFalse(e ast.Expr) bool {
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		return false
	}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		k, ok := kv.Key.(*ast.BasicLit)
		if !ok || strings.Trim(k.Value, `"`) != "success" {
			continue
		}
		if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "false" {
			return true
		}
	}
	return false
}

// f64Spec 解析嵌入的对外规范。
func f64Spec(t *testing.T) map[string]interface{} {
	t.Helper()
	var spec map[string]interface{}
	if err := json.Unmarshal(openapiV1JSON, &spec); err != nil {
		t.Fatalf("openapi.v1.json 非法（规范本身坏了后面所有锁都无意义）: %v", err)
	}
	return spec
}

// f64SpecEnum 取规范里 Error.code.enum（文档承认的对外码全集）。
func f64SpecEnum(t *testing.T, spec map[string]interface{}) []string {
	t.Helper()
	comps, _ := spec["components"].(map[string]interface{})
	schemas, _ := comps["schemas"].(map[string]interface{})
	errModel, _ := schemas["Error"].(map[string]interface{})
	props, _ := errModel["properties"].(map[string]interface{})
	codeProp, _ := props["code"].(map[string]interface{})
	raw, _ := codeProp["enum"].([]interface{})
	if len(raw) == 0 {
		t.Fatal("规范里 Error.code.enum 缺失或为空：文档与状态表的交叉锁失去基准")
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// B 段：文档 ↔ 状态表双向穷举。
func TestOpenAPIContractF64SpecEnumMatchesStatusTable(t *testing.T) {
	enum := f64SpecEnum(t, f64Spec(t))
	table := apierrors.KnownOpenAPIStatusCodes()

	inTable := func(code string) bool { _, ok := table[apierrors.ErrorCode(code)]; return ok }
	for _, c := range enum {
		if !inTable(c) {
			t.Errorf("规范声明了码 %q 但 openAPIStatusByCode 没登记 ⇒ StatusForCode 回落 500，"+
				"客户把自己的参数错误当成服务端故障", c)
		}
	}
	for c := range table {
		found := false
		for _, e := range enum {
			if e == string(c) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("状态表登记了码 %q 但对外文档没写 ⇒ 客户永远无从按码分支（文档才是接入方读的东西）", c)
		}
	}
	// error_code 别名键必须仍在文档里（<1.0.4 SDK 在读它，删键＝打断在生产的接入方）
	comps, _ := f64Spec(t)["components"].(map[string]interface{})
	schemas, _ := comps["schemas"].(map[string]interface{})
	errModel, _ := schemas["Error"].(map[string]interface{})
	props, _ := errModel["properties"].(map[string]interface{})
	if _, ok := props["error_code"]; !ok {
		t.Error("规范 Error 模型丢了 error_code 别名键")
	}
}

// C 段：规范里声明的状态码必须「发得出来」，表里映射的状态必须「文档写过」。
func TestOpenAPIContractF64DeclaredStatusesReachable(t *testing.T) {
	spec := f64Spec(t)
	paths, _ := spec["paths"].(map[string]interface{})
	declared := map[string]bool{}
	for _, item := range paths {
		ops, _ := item.(map[string]interface{})
		for _, op := range ops {
			o, _ := op.(map[string]interface{})
			resps, _ := o["responses"].(map[string]interface{})
			for st := range resps {
				if strings.HasPrefix(st, "4") || strings.HasPrefix(st, "5") {
					declared[st] = true
				}
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("规范里一条错误响应都没声明：C 段失去意义（检查 paths 是否被改坏）")
	}
	table := apierrors.KnownOpenAPIStatusCodes()
	reachable := map[string]bool{}
	for _, st := range table {
		reachable[strconv.Itoa(st)] = true
	}
	for st := range declared {
		if !reachable[st] {
			t.Errorf("规范声明了 %s 但没有任何对外码映射到它（装饰性条目：文档写着、代码永远不发）", st)
		}
	}
	for st := range reachable {
		if !declared[st] {
			t.Errorf("状态表会发 %s 却没有任何路径声明它（接入方按文档写重试逻辑会漏这一档）", st)
		}
	}
	// 正反向各一条硬事实，防表被整体改空/改错
	if got := apierrors.StatusForCode("not_ready"); got != http.StatusConflict {
		t.Errorf("not_ready 应映射 409（任务状态与请求时机冲突），实得 %d", got)
	}
	if got := apierrors.StatusForCode("definitely_unknown_code"); got != http.StatusInternalServerError {
		t.Errorf("未登记码必须响亮回落 500 而不是悄悄 200，实得 %d", got)
	}
}

// D 段：端到端形态锁——真发一次 openapi 请求，验 4xx + 双键同值 + trace_id。
func TestOpenAPIContractF64LiveResponseShape(t *testing.T) {
	s, _, key, _ := f42Setup(t)

	// ① 缺 id → 400 + bad_request（旧形态：200 + success:false）
	w := f42Get(t, s, s.handleOpenAPITaskStatus, key, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺任务 id 应回 400，实得 %d body=%s", w.Code, w.Body.String())
	}
	f64AssertErrorShape(t, w.Body.String(), "bad_request")

	// ② 坏 Key → 401 + invalid_api_key（这条历史上状态码就是对的，本锁防「统一出口」把它改坏）
	w2 := f42Get(t, s, s.handleOpenAPITaskStatus, "sk-not-a-real-key", "id=1")
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("无效 Key 应回 401，实得 %d body=%s", w2.Code, w2.Body.String())
	}
	f64AssertErrorShape(t, w2.Body.String(), "invalid_api_key")

	// ③ 不存在的任务 → 404 + not_found（租户隔离口径：跨租户也是 404，不泄露存在性）
	w3 := f42Get(t, s, s.handleOpenAPITaskStatus, key, "id=999999")
	if w3.Code != http.StatusNotFound {
		t.Fatalf("任务不存在应回 404，实得 %d body=%s", w3.Code, w3.Body.String())
	}
	f64AssertErrorShape(t, w3.Body.String(), "not_found")
}

// f64AssertErrorShape 校验开放 API 错误体的形态：success=false、code 与 error_code 同值、
// message 非空、trace_id 存在（trace_id 只在中间件注入了 trace 时出现，
// 这里请求直接打 handler、没经过 trace 中间件，故只验「键存在时值非空」，
// 并把「双键同值」当成硬判据——那才是客户分支依赖的东西）。
func f64AssertErrorShape(t *testing.T, body, wantCode string) {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("错误体非合法 JSON: %v\n%s", err, body)
	}
	if b, _ := out["success"].(bool); b {
		t.Errorf("success 必须 false：%s", body)
	}
	code, _ := out["code"].(string)
	ec, _ := out["error_code"].(string)
	if code != wantCode || ec != wantCode {
		t.Errorf("code=%q error_code=%q 都必须等于 %q（code 是文档正主，error_code 是 <1.0.4 SDK 别名）",
			code, ec, wantCode)
	}
	if m, _ := out["message"].(string); m == "" {
		t.Error("message 为空：客户界面/日志只能看到裸错误码")
	}
	if tid, ok := out["trace_id"]; ok {
		if sv, _ := tid.(string); sv == "" {
			t.Error("trace_id 键存在却是空串：要么省略、要么给真值，空串会让报障人误以为链路没打通")
		}
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "code,error_code,message,success" {
		t.Errorf("错误体键集漂移：%v（对外契约的键集合变化＝SDK 解析面变化，须与文档同批改）", keys)
	}
}

// E 段：trace_id 那条腿单独钉死。
// D 段直调 handler、没经过 trace 中间件，ctx 里本来就没有 trace_id，
// 所以「键缺席」在那里既证不了对也证不了错——这里直接给 writeOpenAPIError 喂一个
// 带 trace 的 ctx（与统一出口 apierrors.WriteError 同一取法），把「有 trace 就必须带、
// 值必须等于 ctx 里那条」钉成实断言。客户报障时给这一串就能定位日志，
// 这条腿一旦断（忘了透传 ctx／写成空串），D 段是抓不到的。
func TestOpenAPIContractF64TraceIDLeg(t *testing.T) {
	const tid = "f64deadbeef0123456789"
	w := httptest.NewRecorder()
	writeOpenAPIError(w, apierrors.WithTraceID(context.Background(), tid),
		string(apierrors.OpenAPIBadRequest), "text 不能为空")
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("错误体非法 JSON: %v %s", err, w.Body.String())
	}
	if got, _ := out["trace_id"].(string); got != tid {
		t.Errorf("trace_id=%q want %q（对外错误必须带可定位的链路 ID）", out["trace_id"], tid)
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("状态码=%d want 400", w.Code)
	}
	// 无 trace 的 ctx 不得凭空造一个空串键（键集与 D 段锁的口径一致）
	w2 := httptest.NewRecorder()
	writeOpenAPIError(w2, context.Background(), string(apierrors.OpenAPINotFound), "任务不存在")
	if strings.Contains(w2.Body.String(), "trace_id") {
		t.Errorf("ctx 无 trace 时不应出现 trace_id 键：%s", w2.Body.String())
	}
	// message 走 i18n 中间件的翻译链路（键名必须仍是 message，改名＝12 语种响应翻译集体失效）
	if !strings.Contains(w2.Body.String(), `"message"`) {
		t.Errorf("错误体缺 message 键（lang 中间件按 message/error 键做响应翻译）：%s", w2.Body.String())
	}
}

// F 段：对客文档（/openapi/docs 的 zh+en 默认页）里的「code | HTTP」表
// 必须与 internal/errors 的单点状态表逐行一致，且覆盖全部对外码。
//
// 为什么要单独钉：B/C 段锁的是 openapi.v1.json（机器读的规范），
// 但**人读的是文档页**——客户接入时先看这一页。两者一旦分叉（改了表忘了文档、
// 或管理员在后台把默认文档改回旧口径），照文档写重试逻辑的人会拿到和文档不一样的状态码。
// 判据取「表格行的形状」而不是整段字符串相等：管理员在线编辑文档是产品功能，
// 逐字锁会把正常的内容维护全判成红（那是把锁架在功能上）。
func TestOpenAPIContractF64DocTableMatchesStatusTable(t *testing.T) {
	table := apierrors.KnownOpenAPIStatusCodes()
	rowRe := regexp.MustCompile(`(?m)^\|\s*"?([a-z_]+)"?\s*\|\s*(\d{3})\s*\|`)
	for _, doc := range []struct{ name, body string }{
		{"zh", defaultDocsMDZh},
		{"en", defaultDocsMDEn},
	} {
		rows := rowRe.FindAllStringSubmatch(doc.body, -1)
		seen := map[string]string{}
		if len(rows) == 0 {
			t.Fatalf("文档 %s 页里一条「code | HTTP」行都没有：解析式失效或状态码表被整段删除", doc.name)
		}
		for _, m := range rows {
			code, st := m[1], m[2]
			want, ok := table[apierrors.ErrorCode(code)]
			if !ok {
				// 允许文档里出现别的三列同形表格（如语种表），只要求「进了这张表就得是已知码」；
				// 未知码直接红：那等于向客户承诺一个代码永远不会发的错误码。
				t.Errorf("文档 %s 页声明了码 %q（HTTP %s）但状态表里没有它 ⇒ 客户按文档分支、代码却永远不发",
					doc.name, code, st)
				continue
			}
			if got := strconv.Itoa(want); got != st {
				t.Errorf("文档 %s 页写 %q→%s，状态表实得 %q（客户照文档写重试逻辑会拿到不同状态码）",
					doc.name, code, st, got)
			}
			seen[code] = st
		}
		for c := range table {
			if _, ok := seen[string(c)]; !ok {
				t.Errorf("状态表会发 %q 但文档 %s 页没写 ⇒ 接入方无从知道要处理这一档", c, doc.name)
			}
		}
	}
}
