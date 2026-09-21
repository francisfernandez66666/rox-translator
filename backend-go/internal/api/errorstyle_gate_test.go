// ============================================================================
// errorstyle_gate_test.go — 错误响应写法「只减不增」棘轮闸门（★ #42 P2 技术债收尾，2026-09-22）
//
// 背景：#37 把错误**脱敏**收口到 internal/errors（apierrors）后，本包仍残留 741 处
// 「内联手搓错误响应」——`writeJSON(w, 4xx/5xx, map[...]{"success":false,"message":...})`。
// 全仓写错的形态是 1266 处 writeJSON 调用里的这些内联 map：错误码不在任何枚举里、
// trace_id 不经统一出口、字段名靠各 handler 自觉拼（success/message/error/error_code 混用），
// 前端只能按字符串猜。把 741 处一次性改成 s.writeError 既不现实（改动面 >3000 行、零行为收益，
// 直接违反 AGENTS.md 三「改动原则」第一条），也无法在本轮评审窗口内验证。
//
// 因此与 internal/observability/logratchet_test.go（log.Printf 棘轮）、
// internal/archguard/layering_test.go（分层违例豁免）同一手法：
// **不做大爆炸重构，先立棘轮——存量只减不增，新增一处即 CI 红灯，迁移随改动顺带做。**
// 约定：谁在某个 handler 里顺手把一段错误分支换成 s.writeError(w, r, apierrors.New(...))，
// 就把 errorStyleBaselineTotal 与 errorStylePerFileBaselines 里对应文件的数字同步下调；
// **上调基线须在评审中说明理由**，闸门红灯的提示语刻意写成「怎么降」而不是「怎么改数字」。
//
// 判据口径（写清楚，别让人猜）：
//   - 计入：第二实参是 4xx/5xx 整数字面量（writeJSON(w, 403, ...)）
//     或 4xx/5xx 的 http.Status* 常量（writeJSON(w, http.StatusServiceUnavailable, ...)）
//     —— 后者一并计入是为了堵「把 403 换成 http.StatusForbidden 就绕过闸门」这条路；
//   - 不计：2xx/3xx 正常响应、第二实参为变量（如透传上游 code）的调用、
//     以及已经走 s.writeError / apierrors.WriteError 的统一出口（那正是迁移目标态）；
//   - 只扫本包**非 _test.go** 源码（测试里构造 4xx 响应是正常手法，见 archguard 同口径）。
//
// 已知边界（诚实记录，不当成完备）：
//   - 只认 writeJSON 这一个出口函数；本包另有 SSE/流式/直写 csv 的错误分支不在本口径内；
//   - 第二实参走变量或 map 变量的调用不计（要绕过于容易，但这类写法本就该在评审里被问）；
//   - 豁免只按「整个文件」粒度，不给到单个调用点——理由见 errorStyleAllowlist 注释。
//
// 运行：cd backend-go && go test -count=1 ./internal/api/ -run TestErrorStyle
// 本包用例不连库、不读 config（纯静态解析源码），无需 AGENTS.md 一.4 的方言自钉。
// ============================================================================
package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// errorStyleBaselineTotal 非豁免文件的内联错误响应存量基线（只减不增）。
// 建立方式：2026-09-22 用本文件的扫描器实测全包后一次性钉住（口径见文件头），
// 不取整、不四舍五入——基线必须是**同一把尺子量出来的真实数字**，否则第一版就在骗人。
const errorStyleBaselineTotal = 741

// errorStylePerFileBaselines 分文件基线快照（同 logratchet 的「分根设基线」思路）：
// 只看总数会让「A 文件迁走 20 处、B 文件新加 20 处」互相掩盖，逐文件钉才守得住增量。
// 迁移某文件后把它的数字改成新实测值（或清零后删掉该行，僵尸项自检会提醒）。
var errorStylePerFileBaselines = map[string]int{
	"admin_kb.go":           78,
	"tenant.go":             58,
	"auth.go":               54,
	"admin_packages.go":     45,
	"tickets.go":            42,
	"register.go":           29,
	"admin_openapi.go":      26,
	"kb.go":                 26,
	"orgs.go":               24,
	"pay.go":                22,
	"admin_models.go":       20,
	"api_openapi_tasks.go":  20,
	"persona_api.go":        20,
	"admin_billing.go":      18,
	"stream.go":             18,
	"upload_chunk.go":       18,
	"ops_api.go":            17,
	"admin_apikeys.go":      16,
	"feedback.go":           16,
	"admin_webhooks.go":     13,
	"admin_scrape.go":       12,
	"billing_api.go":        12,
	"bitext.go":             11,
	"admin_evals.go":        10,
	"tasks.go":              10,
	"email_verify.go":       9,
	"referral.go":           8,
	"lead.go":               7,
	"user_import.go":        7,
	"admin_flow.go":         6,
	"coupons_api.go":        6,
	"kb_grants.go":          6,
	"tmreview.go":           6,
	"my_billing.go":         5,
	"notifications.go":      5,
	"plans_api.go":          5,
	"admin_assist_proxy.go": 4,
	"mail_tpl.go":           4,
	"spa.go":                4,
	"admin_assist.go":       3,
	"pay_renew.go":          3,
	"scim.go":               3,
	"estimate.go":           2,
	"memleak.go":            2,
	"metrics.go":            2,
	"s9_alerts.go":          2,
	"slo.go":                2,
	"watchdog.go":           2,
	"admin_reconcile.go":    1,
	"funnel.go":             1,
	"server.go":             1,
}

// errorStyleAllowlist 刻意保留内联错误响应的文件 → 豁免理由（对应 archguard 的 legacyAllow）。
// 粒度只到文件、并要求写明理由：**豁免是为了承认「这里的响应体本身就是对外契约」，
// 不是给「还没迁」预留空位**——新写的业务错误分支一律该走 s.writeError，没有豁免资格。
var errorStyleAllowlist = map[string]string{
	// #42 探针拆分：/readyz 的 503 出参是给编排器/监控解析的固定契约
	// （{status,store,distributed,failed_dependencies}），换成 apierrors 会改坏运维口径。
	"health_probes.go": "存活/就绪探针的 503 响应体即监控契约，不适用统一错误出口（见该文件头判定口径）",
}

// errStatusErrConsts 计入判据的 4xx/5xx http.Status* 常量名（堵住「数字换常量」这条绕闸路径）。
var errStatusErrConsts = map[string]bool{
	"http.StatusBadRequest":          true,
	"http.StatusUnauthorized":        true,
	"http.StatusPaymentRequired":     true,
	"http.StatusForbidden":           true,
	"http.StatusNotFound":            true,
	"http.StatusMethodNotAllowed":    true,
	"http.StatusConflict":            true,
	"http.StatusGone":                true,
	"http.StatusUnprocessableEntity": true,
	"http.StatusTooManyRequests":     true,
	"http.StatusInternalServerError": true,
	"http.StatusNotImplemented":      true,
	"http.StatusBadGateway":          true,
	"http.StatusServiceUnavailable":  true,
	"http.StatusGatewayTimeout":      true,
}

// TestErrorStyleRatchet 主断言：总数与逐文件存量均不得超过基线（只减不增）。
func TestErrorStyleRatchet(t *testing.T) {
	counts := errStyleScan(t, ".")
	actual := 0
	for f, n := range counts {
		if errStyleExempt(f) {
			continue
		}
		actual += n
	}
	if actual > errorStyleBaselineTotal {
		t.Errorf("★ 内联错误响应存量上升：%d > 基线 %d。新代码请改走 s.writeError(w, r, apierrors.New(code, msg))"+
			"（统一错误码 + trace_id + 出参结构），明细见下；\n"+
			"如确需上调基线，必须在评审中说明理由（本闸门的存在意义就是让这个数字单调下降）：\n  %s",
			actual, errorStyleBaselineTotal, errStyleTopOffenders(counts, 10))
	}
	if actual < errorStyleBaselineTotal {
		t.Logf("内联错误响应存量已降至 %d（基线 %d）：请把 errorStyleBaselineTotal 与对应文件条目同步下调，继续迁移"+
			"（大头：%s）", actual, errorStyleBaselineTotal, errStyleTopOffenders(counts, 3))
	}
	// 逐文件：防「一处降掩盖另一处升」
	var up []string
	for f, base := range errorStylePerFileBaselines {
		if errStyleExempt(f) {
			continue
		}
		n := counts[f]
		if n > base {
			up = append(up, f+": "+strconv.Itoa(n)+" > 基线 "+strconv.Itoa(base))
		}
	}
	if len(up) > 0 {
		sort.Strings(up)
		t.Errorf("★ 以下文件**新增**了内联错误响应（该文件的基线只减不增）：\n  %s\n"+
			"修法：把新增的 writeJSON(w, 4xx/5xx, map[...]{\"success\":false,...}) 换成 "+
			"s.writeError(w, r, apierrors.New(apierrors.ErrXxx, \"中文文案\"))。", strings.Join(up, "\n  "))
	}
	// 基线自洽：逐文件条目之和必须等于总数基线，否则两处账会各说各话
	if sum := errStyleBaselinesSum(); sum != errorStyleBaselineTotal {
		t.Errorf("基线自洽性被破坏：errorStylePerFileBaselines 之和 = %d，但 errorStyleBaselineTotal = %d。"+
			"下调基线时两处必须一起改（新增文件请登记，清零文件请删行）", sum, errorStyleBaselineTotal)
	}
	// 未登记且有存量的文件：只提示不红灯（拆文件是 AGENTS.md 一.1 鼓励的方向，不该被记账规则卡住），
	// 但它们的存量已计入总数基线，所以「新开一个文件塞错误响应」依然会把总数顶红。
	// 新写的 handler 没有历史包袱，请直接走 s.writeError —— 那才是本闸门希望收敛到的形态。
	var unlisted []string
	for f, n := range counts {
		if errStyleExempt(f) || n == 0 {
			continue
		}
		if _, ok := errorStylePerFileBaselines[f]; !ok {
			unlisted = append(unlisted, f+": "+strconv.Itoa(n))
		}
	}
	if len(unlisted) > 0 {
		sort.Strings(unlisted)
		t.Logf("未登记进 errorStylePerFileBaselines 的文件（已计入总数基线）：%s", strings.Join(unlisted, ", "))
	}
}

// TestErrorStyleScanActuallyCovers 扫描有效性 + 判据反影子自检：
// 闸门最危险的失效形态是「扫了个空集然后永远绿」（同 archguard 的 TestScanActuallyCoversCodebase）。
func TestErrorStyleScanActuallyCovers(t *testing.T) {
	counts := errStyleScan(t, ".")
	total := 0
	files := 0
	for _, n := range counts {
		if n > 0 {
			files++
			total += n
		}
	}
	if total < 500 {
		t.Fatalf("本包内联错误响应实测仅 %d 处，明显低于实际规模（基线 %d）——扫描根或判据已失效，本闸门当前不可信",
			total, errorStyleBaselineTotal)
	}
	if files < 30 {
		t.Fatalf("命中的文件数仅 %d，疑似只扫到子集（本包存量分布在数十个文件上）", files)
	}
	// 抽样锚点：这些是仓库现实，扫不到即遍历/判据逻辑错了
	for _, anchor := range []struct {
		file string
		min  int
	}{
		{"admin_kb.go", 50},
		{"auth.go", 30},
		{"server.go", 1}, // 连 server.go 这种薄文件都有存量，进一步证明不是只扫了大头
	} {
		if counts[anchor.file] < anchor.min {
			t.Errorf("抽样锚点 %s 实测 %d 处 < 预期下限 %d：扫描逻辑已失配", anchor.file, counts[anchor.file], anchor.min)
		}
	}
	// 反影子：判据既不能恒真也不能恒假（内存构造源码喂给同一个判定函数，不依赖仓库现状）
	cases := []struct {
		src  string
		want int
		why  string
	}{
		{"package api\nfunc f(w interface{}) { writeJSON(w, 400, nil) }", 1, "4xx 整数字面量应计入"},
		{"package api\nfunc f(w interface{}) { writeJSON(w, 503, nil) }", 1, "5xx 整数字面量应计入"},
		{"package api\nfunc f(w interface{}) { writeJSON(w, http.StatusServiceUnavailable, nil) }", 1, "4xx/5xx 常量应计入（防绕闸）"},
		{"package api\nfunc f(w interface{}) { writeJSON(w, 200, nil) }", 0, "200 正常响应不应计入"},
		{"package api\nfunc f(w interface{}) { writeJSON(w, http.StatusOK, nil) }", 0, "http.StatusOK 不应计入"},
		{"package api\nfunc f(w interface{}, code int) { writeJSON(w, code, nil) }", 0, "状态码为变量时不计（口径边界，见文件头）"},
		{"package api\nfunc f(w interface{}) { writeJSON(w, 403, nil); writeJSON(w, 404, nil) }", 2, "同文件多处应逐个计入"},
		{"package api\n// writeJSON(w, 500, x) 只是注释\nfunc f() {}", 0, "注释里的示例代码不应被算成存量"},
	}
	for _, c := range cases {
		if got := errStyleCountSource(t, "case.go", c.src); got != c.want {
			t.Errorf("判据自检失败（%s）：got %d want %d，源码 %s", c.why, got, c.want, c.src)
		}
	}
}

// TestErrorStyleNoZombieExemption 僵尸豁免守卫（同 archguard 的 TestNoZombieExemption）：
// 豁免理由一旦不再成立（文件删了 / 错误分支迁干净了），豁免行必须一起删掉，
// 否则它就成了下一个违例者的空位。
func TestErrorStyleNoZombieExemption(t *testing.T) {
	counts := errStyleScan(t, ".")
	for f, why := range errorStyleAllowlist {
		if counts[f] == 0 {
			t.Errorf("errorStyleAllowlist 僵尸豁免：%s 已无内联错误响应（或文件已不存在），请删除该行\n  （原豁免理由：%s）", f, why)
		}
	}
	for f, base := range errorStylePerFileBaselines {
		if counts[f] == 0 && base > 0 {
			t.Errorf("errorStylePerFileBaselines 僵尸条目：%s 已清零（基线记 %d），请删掉该行并把 errorStyleBaselineTotal 同额下调",
				f, base)
		}
	}
}

// errStyleExempt 文件是否在豁免名单内。
func errStyleExempt(file string) bool {
	_, ok := errorStyleAllowlist[file]
	return ok
}

// errStyleBaselinesSum 逐文件基线之和（供自洽性断言）。
func errStyleBaselinesSum() int {
	s := 0
	for _, v := range errorStylePerFileBaselines {
		s += v
	}
	return s
}

// errStyleTopOffenders 按存量降序取前 n 个文件的可读串（红灯信息里直接给出该从哪儿下手）。
func errStyleTopOffenders(counts map[string]int, n int) string {
	type kv struct {
		f string
		c int
	}
	all := make([]kv, 0, len(counts))
	for f, c := range counts {
		if c == 0 || errStyleExempt(f) {
			continue
		}
		all = append(all, kv{f, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].c != all[j].c {
			return all[i].c > all[j].c
		}
		return all[i].f < all[j].f
	})
	if len(all) > n {
		all = all[:n]
	}
	parts := make([]string, 0, len(all))
	for _, e := range all {
		parts = append(parts, e.f+"="+strconv.Itoa(e.c))
	}
	return strings.Join(parts, ", ")
}

// errStyleScan 解析 dir（本包目录，go test 的工作目录即包目录）下全部非测试源码，
// 返回「文件名 → 内联错误响应处数」。用 AST 而非逐行正则：判据要落在真实调用上，
// 不能被注释、字符串里的示例代码误计（同 route_auth_gate_test.go 的解析口径）。
func errStyleScan(t *testing.T, dir string) map[string]int {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", dir, err)
	}
	counts := map[string]int{}
	for _, pkg := range pkgs {
		for fileName, file := range pkg.Files {
			base := filepath.Base(fileName)
			if n := errStyleCountFile(file); n > 0 {
				counts[base] += n
			}
		}
	}
	return counts
}

// errStyleCountSource 从源码串统计（供判据自检用例直接喂代码，不碰仓库现状）。
func errStyleCountSource(t *testing.T, name, src string) int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析自检源码 %s 失败: %v", name, err)
	}
	return errStyleCountFile(file)
}

// errStyleCountFile 统计单个文件里 writeJSON(w, <4xx/5xx>, ...) 的处数。
func errStyleCountFile(file *ast.File) int {
	n := 0
	ast.Inspect(file, func(node ast.Node) bool {
		ce, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		// 只认包内裸调用 writeJSON（本包统一出口；带接收者的 s.writeJSON 形态不存在）
		id, ok := ce.Fun.(*ast.Ident)
		if !ok || id.Name != "writeJSON" {
			return true
		}
		if len(ce.Args) < 2 {
			return true
		}
		if errStyleIsErrorStatus(ce.Args[1]) {
			n++
		}
		return true
	})
	return n
}

// errStyleIsErrorStatus 第二实参是否表示 4xx/5xx：整数字面量 或 已登记的 http.Status* 常量名。
func errStyleIsErrorStatus(e ast.Expr) bool {
	switch arg := e.(type) {
	case *ast.BasicLit:
		if arg.Kind != token.INT {
			return false
		}
		code, err := strconv.Atoi(arg.Value)
		if err != nil {
			return false
		}
		return code >= 400 && code < 600
	case *ast.SelectorExpr:
		sel, ok := arg.X.(*ast.Ident)
		if !ok || sel.Name != "http" {
			return false
		}
		return errStatusErrConsts["http."+arg.Sel.Name]
	default:
		return false
	}
}
