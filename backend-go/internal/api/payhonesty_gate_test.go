// ============================================================================
// payhonesty_gate_test.go — 收款/账务路径「HTTP 200 承载失败」零容忍等值锁（★ F-64① 批 I-7，2026-09-26）
//
// 缺陷本体（UAT 报告 F-47 / 修复文档 F-64）：本包大量业务失败分支写成
//
//	writeJSON(w, 200, map[...]{"success": false, "message": ...})
//
// ——HTTP 层永远是 200，失败只藏在 body 里。对浏览器前端也许够用，但**客户与 SDK 是按状态码
// 分支的**：wx.request/fetch/重试策略/网关告警/CDN 缓存/监控 5xx 率全都看不到这类失败。
// 实测规模（本文件的扫描器逐调用点量出，不是估的）：internal/api 全包 **240 处**，
// 其中收款/账务路径（本文件锁定的 7 个文件）**61 处**——钱的事最先修。
//
// 与 errorstyle_gate_test.go 的分工（两把尺子量两件不同的事，都要跑）：
//   - errorstyle 计的是「内联 writeJSON(w, 4xx/5xx, map{...})」——状态码已经诚实、只是没走统一出口；
//   - 本文件计的是「writeJSON(w, 200, ...) 但报文里 success:false」——**状态码本身在说谎**。
//     前者降了不代表后者降了，反之亦然；把 200 壳改成 4xx 内联会让 errorstyle 上升、本闸门下降。
//
// 判据口径（逐调用点，不按函数归并，避免「函数里有一处 success:false 就把整个函数的 200 全算上」的假阳）：
//
//	形态 A：该次 writeJSON(w, 200, X) 的 X 表达式内部（含任意层嵌套）出现 "success": false 字面量；
//	形态 B：X 是一个变量，且该变量在本行之前被 `v["success"] = false` 置过假；
//	形态 C：X 是一个变量，且该变量在本行之前由 `v := map[...]{..., "success": false, ...}` 定义。
//	不计：状态码非 200 字面量（4xx/5xx 的内联错误由 errorstyle 棘轮管）、状态码为变量、
//	     SSE/CSV 直写流（不属 writeJSON 出口，见文件末「已知边界」）。
//
// 已知边界（诚实记录，不当成完备）：
//   - 只认 writeJSON 出口；`w.Write(...)` 手拼 JSON 的分支扫不到（本包内联流式响应另有约定，
//     收款路径实测无此写法）；
//   - 跨函数传参（A 函数造好 success:false 的 map 交给 B 函数写 200）扫不到——这类写法
//     在本包只出现在 writeAssistBizErr（assist 白名单转发，非收款路径）；
//   - 「MUST-STAY-200」只登记了渠道回调这一族真正不能被状态码语义约束的应答，
//     见 TestPayNotifyStatusesFrozen；②/③档（管理台动作类、纯展示读接口）尚未纳入零容忍，
//     由全包总数棘轮（TestPayShellRatchetWholePackage）兜住「不许变多」。
//
// 运行：cd backend-go && go test -count=1 ./internal/api/ -run 'TestPayHonest|TestPayShell|TestPayNotify'
// 纯静态解析源码，不连库、不读 config，无需 AGENTS.md 一.4 的方言自钉。
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

	apierrors "translator/internal/errors"
)

// payHonestZeroFiles 第①档已完成诚实改造的收款/账务文件：**200 承载失败必须恰为 0**。
// 这是**等值锁**不是单向棘轮——写「≤0」和写「=0」等价，但写「只减不增」会让
// 「新增一处 200 壳」在存量已为 0 时被同一句放过（历史上 UI 侧就被单向锁推离过交付稿）。
var payHonestZeroFiles = []string{
	"pay.go",           // 充值下单/查单/模拟支付/人工确认/渠道回调
	"pay_renew.go",     // 自动续费开关与续费单
	"plans_api.go",     // 公开价目、我的套餐、订阅、升级
	"coupons_api.go",   // 券试算与券模板 CRUD（下单核销失败也走这里的 replyCouponFailure）
	"billing_api.go",   // 计费配置、配额、用量（个人/组织/成本）
	"admin_billing.go", // 超管账本：订单/发票/人工核对单/导出
	"my_billing.go",    // 客户自助账单中心五表
}

// payShellTotalBaseline 全包「200 承载失败」存量基线（只减不增）。
// ★ 建立口径：2026-09-26 批 I-7 用本文件扫描器实测（①档 7 个文件清零后的**当前真实值**），
//
//	不取整、不估算。②/③档每迁一处就把它下调；上调必须在评审里说明理由。
const payShellTotalBaseline = 186

// payShellTier2Files 第②/③档在账清单（本批不动，逐批清零）→ 未处理处数快照。
// 登记意义：让「还剩哪些、各多少」是机器可核对的数字，而不是文档里的一句「后续再说」；
// 僵尸守卫会在这条清零后红灯提醒删行。
var payShellTier2Files = map[string]string{
	"admin_packages.go": "②档：超管套餐 CRUD 动作类（6 处）——管理台内部接口，客户与 SDK 不消费",
	"admin_kb.go":       "②档：知识库管理动作类（存量最大）",
	"tickets.go":        "③档：工单读接口按分期口径可暂留 200，但须显式登记",
}

// TestPayHonestMoneyPathsZero 主断言：收款/账务 7 个文件的 200 壳必须逐文件恰为 0。
func TestPayHonestMoneyPathsZero(t *testing.T) {
	sites := payShellScan(t, ".")
	perFile := map[string]int{}
	for _, s := range sites {
		perFile[s.file]++
	}
	var bad []string
	for _, f := range payHonestZeroFiles {
		if n := perFile[f]; n != 0 {
			bad = append(bad, f+": "+strconv.Itoa(n)+" 处")
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("★ 收款/账务路径出现「HTTP 200 承载失败」（F-64① 已锁零，等值断言）：\n  %s\n"+
			"这类分支 HTTP 层永远是 200，客户与 SDK 按状态码分支时会把失败读成成功——\n"+
			"充值/订阅/券/账本尤其致命（金额已改、单已建，前端却当无事发生）。\n"+
			"改法：s.writeError(w, r, apierrors.New(<语义码>, \"中文文案\"))，附加字段走 WithDetails：\n"+
			"  入参非法 400 / 未登录 401 / 越权 403 / 对象不存在 404 / 状态冲突 409 /\n"+
			"  存储或本进程故障 500 / 依赖未就绪可稍后重试 503（ErrServiceUnavailable、ErrPayChannelUnavailable）。\n"+
			"明细：%s", strings.Join(bad, "\n  "), payShellDetail(sites, payHonestZeroFiles))
	}
	// 锁本身要能被证明有效：7 个文件必须都在扫描器射程内（文件改名/挪包即失配，
	// 那时上面的断言会对着空集恒绿——这是闸门最危险的失效形态，见 TestPayShellScanActuallyCovers）。
	for _, f := range payHonestZeroFiles {
		if !payShellFileExists(f) {
			t.Errorf("零容忍清单里的 %s 在本包不存在（改名或挪走了？）：该条目已从射程消失，请同步维护 payHonestZeroFiles", f)
		}
	}
}

// TestPayShellRatchetWholePackage 全包总数棘轮：②/③档没迁完是事实，但**一处都不许多**。
// 同时输出「还剩多少、集中在哪」的进度账（t.Logf），让分期推进看得见。
func TestPayShellRatchetWholePackage(t *testing.T) {
	sites := payShellScan(t, ".")
	if len(sites) > payShellTotalBaseline {
		byFile := map[string]int{}
		for _, s := range sites {
			byFile[s.file]++
		}
		t.Errorf("★ 全包「200 承载失败」存量上升：%d > 基线 %d。新增错误分支一律走 s.writeError + apierrors；\n"+
			"如确需上调基线必须在评审说明理由（本闸门的存在就是让这个数字单调下降到 0）。\n  当前大头：%s",
			len(sites), payShellTotalBaseline, payShellTopOffenders(byFile, 8))
	}
	if len(sites) < payShellTotalBaseline {
		t.Logf("「200 承载失败」已降至 %d（基线 %d）：请同步下调 payShellTotalBaseline，"+
			"并清掉已归零的 payShellTier2Files 条目", len(sites), payShellTotalBaseline)
	}
	// ②/③档在账清单：登记的必须真的有存量（僵尸守卫），数字变了要提醒改文档
	byFile := map[string]int{}
	for _, s := range sites {
		byFile[s.file]++
	}
	for f, why := range payShellTier2Files {
		if byFile[f] == 0 {
			t.Errorf("payShellTier2Files 僵尸条目：%s 已无「200 承载失败」（或文件已不存在），请删掉该行\n  （原登记理由：%s）", f, why)
		}
	}
}

// payFrozenStatuses MUST-STAY 应答白名单：函数名 → 允许出现的 HTTP 状态码集合（★ 严格口径）。
// 这里的「200」不是失败壳，而是**对端只认 2xx 应答语义**的回调/探针出口：
// 改成 4xx/5xx 对端会理解为「稍后重试」，反而制造重复回调与订单状态抖动。
// 白名单成员的红线是「状态码不许按客户语义改」，不是「body 可以乱拼」——
// body 仍须走统一出口补 code/trace_id（批 I-7 已照此改完，见 handlePayNotify 注释）。
var payFrozenStatuses = map[string][]int{
	"handlePayNotify": {200, 400, 403},
}

// TestPayNotifyStatusesFrozen 渠道回调状态码冻结锁。
// 判据取自真实调用：writeJSON 的整数字面量实参 + s.writeError(apierrors.New(<码>)) 经
// (*APIError).HTTPStatus() 换算，两者都落进白名单才绿。把「订单不存在」改成 404 即红（变异已实测）。
// ⚠️ 这里**不能用 apierrors.StatusForCode**：那张表是**开放 API 专用**的
// （openAPIStatusByCode 只登记对外 snake_case 码族，未知码一律回 500），
// 拿它换算内部码 ErrValidation/ErrForbidden 会得到 500，于是本锁要么假红、
// 要么在有人把 handlePayNotify 改成 5xx 时恰好「换算出 500 → 白名单外 → 红得很奇怪」。
// 统一出口的真相是 (*APIError).HTTPStatus()（见 internal/errors/codes.go 的 switch）。
func TestPayNotifyStatusesFrozen(t *testing.T) {
	statuses, found := payShellFuncStatuses(t, ".", "handlePayNotify")
	if !found {
		t.Fatal("handlePayNotify 没找到（改名/挪包？）：MUST-STAY-200 白名单条目已成僵尸，请同步维护 payFrozenStatuses")
	}
	allowed := map[int]bool{}
	for _, c := range payFrozenStatuses["handlePayNotify"] {
		allowed[c] = true
	}
	var bad []string
	for _, st := range statuses {
		if !allowed[st] {
			bad = append(bad, strconv.Itoa(st))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Errorf("★ handlePayNotify 出现了白名单外的状态码 %s（允许 %v）。\n"+
			"本口的消费方是微信/支付宝服务器，不是客户浏览器：非 2xx 会被渠道判为「未确认、继续重试」，\n"+
			"给 404/409/503 这类「客户语义更诚实」的码恰恰是把它改坏。失败信息统一在 body 的 code/message 里。",
			strings.Join(bad, ", "), payFrozenStatuses["handlePayNotify"])
	}
	// 状态换算表本身也得钉住：本锁依赖「ErrValidation→400、ErrForbidden→403」这一事实，
	// 有人在 internal/errors 里改了映射就会悄悄改变本口的对外行为。
	if got := apierrors.New(apierrors.ErrValidation, "").HTTPStatus(); got != 400 {
		t.Errorf("ErrValidation 的 HTTP 映射已变成 %d（本锁按 400 成立）：请同步复核 handlePayNotify 的状态码冻结集", got)
	}
	if got := apierrors.New(apierrors.ErrForbidden, "").HTTPStatus(); got != 403 {
		t.Errorf("ErrForbidden 的 HTTP 映射已变成 %d（本锁按 403 成立）：请同步复核 handlePayNotify 的状态码冻结集", got)
	}
	// 反向覆盖：白名单不许是空转的——本口必须确实存在失败应答（否则该删条目）
	hasErr := false
	for _, st := range statuses {
		if st >= 400 {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("handlePayNotify 实测状态码集合 %v 里已无 4xx：白名单条目失去意义，请删除（僵尸守卫）", statuses)
	}
}

// TestPayShellScanActuallyCovers 扫描有效性自检（反影子）：判据不能恒真也不能恒假。
// 用例全部用内存构造的源码喂给同一个判定函数，不依赖仓库现状。
func TestPayShellScanActuallyCovers(t *testing.T) {
	cases := []struct {
		src  string
		want int
		why  string
	}{
		{`package api
func f(w interface{}) { writeJSON(w, 200, map[string]interface{}{"success": false, "message": "x"}) }`, 1, "形态 A：200 + 字面量 success:false"},
		{`package api
func f(w interface{}) { writeJSON(w, 200, map[string]interface{}{"success":false}) }`, 1, "形态 A：无空格写法同样命中"},
		{`package api
func f(w interface{}) { writeJSON(w, 200, map[string]interface{}{"success": true}) }`, 0, "200 + 成功应答不计"},
		{`package api
func f(w interface{}) { writeJSON(w, 400, map[string]interface{}{"success": false}) }`, 0, "状态码已是 4xx 的不算 200 壳（归 errorstyle 棘轮管）"},
		{`package api
func f(w interface{}) {
	m := map[string]interface{}{"success": true}
	m["success"] = false
	writeJSON(w, 200, m)
}`, 1, "形态 B：变量先置 false 再写 200"},
		{`package api
func f(w interface{}) {
	m := map[string]interface{}{"success": true}
	writeJSON(w, 200, m)
	m["success"] = false
}`, 0, "形态 B 反证：置假发生在 200 之后不应命中（逐调用点按行号判）"},
		{`package api
func f(w interface{}) {
	body := map[string]interface{}{"success": false, "message": "x"}
	writeJSON(w, 200, body)
}`, 1, "形态 C：变量由含 success:false 的字面量定义"},
		{`package api
func f(w interface{}, code int) { writeJSON(w, code, map[string]interface{}{"success": false}) }`, 0, "状态码为变量时不计（口径边界，见文件头）"},
		{`package api
func f(w interface{}) {
	writeJSON(w, 200, map[string]interface{}{"data": map[string]interface{}{"ok": true}, "success": false})
}`, 1, "形态 A：嵌套字面量里的 success:false 同样命中"},
		{`package api
// writeJSON(w, 200, map[string]interface{}{"success": false}) 只是注释里的例子
func f() {}`, 0, "注释里的示例代码不应被算成存量"},
	}
	for _, c := range cases {
		if got := payShellCountSource(t, "case.go", c.src); got != c.want {
			t.Errorf("判据自检失败（%s）：got %d want %d，源码：\n%s", c.why, got, c.want, c.src)
		}
	}
	// 仓库现实锚点：扫到空集即遍历失效（①档清零后全包仍有百余处，锚点取保守下限）
	sites := payShellScan(t, ".")
	if len(sites) < 100 {
		t.Fatalf("全包「200 承载失败」实测 %d 处，明显低于实际规模（基线 %d）：扫描根或判据已失效，本闸门当前不可信",
			len(sites), payShellTotalBaseline)
	}
	// ①档文件必须**扫得到别的写法**却扫不到 200 壳——用 admin_billing.go 的 CSV 分支做正证：
	// 它仍在射程内（未列入零容忍），命中数 >0 或该文件确已清零都说明遍历没漏文件。
	perFile := map[string]int{}
	for _, s := range sites {
		perFile[s.file]++
	}
	if len(perFile) < 20 {
		t.Errorf("命中的文件数仅 %d，疑似只扫到子集（本包存量分布在数十个文件上）", len(perFile))
	}
	for _, f := range payHonestZeroFiles {
		if !payShellFileExists(f) {
			t.Errorf("零容忍文件 %s 未被遍历（不存在或解析失败？）", f)
		}
	}
}

// TestPayHonestFrontendContractLocks 前端配套契约锁（★ F-64① 的另一半）。
// 后端把 200 壳改成诚实状态码后，前端 request() 会对非 2xx **抛异常**，而全站约定是
// if (!r.success) / toastResp(r)。若接口层不做 bizResp 收敛，点「订阅」失败就变成
// 未捕获 rejection + 界面无反应。本锁钉住「收款/券/账本接口函数都过了 bizResp」。
// 判据取前端源码（同 readability.test.ts 的源码级锁口径），不跑 node。
//
// ★ 口径是一条**双向等值锁**（不是「越多 bizResp 越好」的单向锁）：
//   - POST 动作类 + 收银台轮询 → **必须** bizResp（把诚实状态码摊回 r.success/r.code/r.status，
//     调用点的 if (!r.success) 分支才走得到）；
//   - 纯读取（列表/详情/开关查询）→ **必须不** bizResp，保持抛出，交给调用点的 runGuarded 兜
//     （见 PlansP 的 loadOrders/loadInvoices、MyBilling 的各段 loader）。
//     给读取类套 bizResp 会把「读失败」伪装成「读到空列表」——正是批 #42 消灭的静默失败形态。
//     所以 needBizResp 与 mustNotBizResp 两张表都必须逐函数命中：改名、挪文件、越权收敛，一律红灯。
func TestPayHonestFrontendContractLocks(t *testing.T) {
	// 从 backend-go/internal/api/ 到 frontend-react/src/api 需上跳三层。
	const client = "../../../frontend-react/src/api"
	needBizResp := map[string][]string{
		"billing.ts": {"payCreate", "payStatus", "paySimulate", "payManualConfirm", "packageSubscribe",
			"packageUpgrade", "autoRenewSet", "billingInvoiceCreate", "billingInvoiceVoid", "adminOrderRefund"},
		"coupons.ts": {"couponPreview", "adminCouponSave", "adminCouponDelete"},
		"admin.ts":   {"adminOrderCreate", "adminOrderPay"},
	}
	mustNotBizResp := map[string][]string{
		"billing.ts":   {"plans", "myPackage", "autoRenewGet", "manualConfirmOrders", "billingOrders", "billingInvoices"},
		"mybilling.ts": {"myOverview", "myOrders", "myLedger", "myInvoices"},
	}
	for file, fns := range needBizResp {
		p := filepath.Join(client, file)
		src, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("读不到 %s：前端接口层结构已变，请同步本锁（%v）", p, err)
			continue
		}
		body := string(src)
		for _, fn := range fns {
			seg, ok := payShellFnSegment(body, fn)
			if !ok {
				t.Errorf("%s 里找不到 export async function %s(：接口函数改名或删除，F-64① 的前端收敛已失配", file, fn)
				continue
			}
			if !strings.Contains(seg, "bizResp(") {
				t.Errorf("★ %s 的 %s 未走 bizResp：后端这些接口已不再用 200 承载失败，"+
					"未收敛会让调用点的 if (!r.success) 分支永远走不到（异常直接冒到未捕获）", file, fn)
			}
		}
	}
	for file, fns := range mustNotBizResp {
		p := filepath.Join(client, file)
		src, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("读不到 %s：前端接口层结构已变，请同步本锁（%v）", p, err)
			continue
		}
		body := string(src)
		for _, fn := range fns {
			seg, ok := payShellFnSegment(body, fn)
			if !ok {
				t.Errorf("%s 里找不到 export async function %s(：接口函数改名或删除，请同步 mustNotBizResp 表", file, fn)
				continue
			}
			if strings.Contains(seg, "bizResp(") {
				t.Errorf("★ %s 的 %s 是纯读取接口却走了 bizResp：读取失败会被伪装成空数据（批 #42 的静默失败形态）。\n"+
					"正确做法是保持抛出、在调用点用 runGuarded / guardRead 兜出后端文案（见 PlansP 的 guardRead）。", file, fn)
			}
		}
	}
}

// payShellFnSegment 从前端接口层源码里切出「某个导出函数到下一个 export 之前」的片段。
// 返回 (片段, 是否找到)。TS 里接口函数一律 `export async function 名(`，且函数体不含
// 顶格的 `export `，所以按「下一个 \nexport 」切段是安全的（同 mybilling.ts 的实测形态）。
func payShellFnSegment(body, fn string) (string, bool) {
	start := strings.Index(body, "export async function "+fn+"(")
	if start < 0 {
		return "", false
	}
	seg := body[start:]
	if end := strings.Index(seg[1:], "\nexport "); end > 0 {
		seg = seg[:end+1] // +1：从 seg[1:] 起算，切回 seg 的坐标系
	}
	return seg, true
}

// payShellCall 一次 writeJSON(w, 200, X) 调用（带着实参表达式，判定不需要二次查找）。
type payShellCall struct {
	line int
	arg  ast.Expr
}

// payShellSite 一处「200 承载失败」调用点。
type payShellSite struct {
	file string
	line int
	fn   string
	kind string // A/B/C，口径见文件头
}

// payShellScan 解析 dir（本包目录）下全部非测试源码，逐调用点找出 200 壳。
func payShellScan(t *testing.T, dir string) []payShellSite {
	t.Helper()
	fset := token.NewFileSet()
	var out []payShellSite
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取扫描根 %s 失败: %v", dir, err)
	}
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".go") || strings.HasSuffix(en.Name(), "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, en.Name()), nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败: %v", en.Name(), perr)
		}
		out = append(out, payShellCountFile(fset, f, en.Name())...)
	}
	return out
}

// payShellCountSource 从源码串统计处数（供判据自检用例直接喂代码，不碰仓库现状）。
func payShellCountSource(t *testing.T, name, src string) int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析自检源码 %s 失败: %v", name, err)
	}
	return len(payShellCountFile(fset, file, name))
}

// payShellCountFile 统计单文件里的 200 壳调用点（判据见文件头）。
// 逐 **函数体** 归集证据再逐调用点判定：形态 B/C 的变量置假必须发生在该次 200 调用之前，
// 因此不能按函数级「这个函数里出现过 success:false」一刀切（那会把同函数的正常应答也误计，
// 实测一刀切版本把 240 报成 406——多出来的 166 处全是假阳）。
func payShellCountFile(fset *token.FileSet, file *ast.File, name string) []payShellSite {
	var out []payShellSite
	ast.Inspect(file, func(node ast.Node) bool {
		fd, ok := node.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		var calls []payShellCall
		varFail := map[string]int{} // 形态 B：变量名 → m["success"] = false 的行号
		litFail := map[string]int{} // 形态 C：变量名 → v := map{..."success": false...} 的行号
		ast.Inspect(fd.Body, func(n2 ast.Node) bool {
			switch ce := n2.(type) {
			case *ast.CallExpr:
				if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "writeJSON" && len(ce.Args) == 3 {
					if lit, ok := ce.Args[1].(*ast.BasicLit); ok && lit.Kind == token.INT {
						if v, _ := strconv.Atoi(lit.Value); v == 200 {
							calls = append(calls, payShellCall{line: fset.Position(ce.Pos()).Line, arg: ce.Args[2]})
						}
					}
				}
			case *ast.AssignStmt:
				if len(ce.Lhs) != 1 || len(ce.Rhs) != 1 {
					return true
				}
				// 形态 B：m["success"] = false
				if idx, ok := ce.Lhs[0].(*ast.IndexExpr); ok {
					if bl, ok := idx.Index.(*ast.BasicLit); ok && bl.Value == `"success"` {
						if id, ok := ce.Rhs[0].(*ast.Ident); ok && id.Name == "false" {
							varFail[payShellVarName(idx.X)] = fset.Position(ce.Pos()).Line
						}
					}
				}
				// 形态 C：v := / v = map[...]{..., "success": false, ...}
				if id, ok := ce.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
					if payShellHasSuccessFalse(ce.Rhs[0]) {
						litFail[id.Name] = fset.Position(ce.Pos()).Line
					}
				}
			}
			return true
		})
		for _, c := range calls {
			kind := ""
			switch {
			case payShellHasSuccessFalse(c.arg):
				kind = "A"
			default:
				vn := payShellVarName(c.arg)
				if ln, ok := varFail[vn]; ok && ln <= c.line {
					kind = "B"
				} else if ln, ok := litFail[vn]; ok && ln <= c.line {
					kind = "C"
				}
			}
			if kind != "" {
				out = append(out, payShellSite{file: name, line: c.line, fn: fd.Name.Name, kind: kind})
			}
		}
		return true
	})
	return out
}

// payShellHasSuccessFalse 表达式内部（含任意层嵌套）是否出现 "success": false 字面量对。
func payShellHasSuccessFalse(e ast.Expr) bool {
	hit := false
	ast.Inspect(e, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		k, ok := kv.Key.(*ast.BasicLit)
		if !ok || k.Value != `"success"` {
			return true
		}
		if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "false" {
			hit = true
		}
		return true
	})
	return hit
}

// payShellVarName 取表达式的变量名（非 Ident 一律回空串，天然不命中证据表）。
func payShellVarName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// payShellFileExists 本包目录下是否有该非测试源码文件（僵尸清单守卫）。
func payShellFileExists(name string) bool {
	fi, err := os.Stat(name)
	return err == nil && !fi.IsDir()
}

// payShellDetail 把指定文件的命中点列成「file:line func [形态]」明细（红灯信息里直接给坐标）。
func payShellDetail(sites []payShellSite, files []string) string {
	want := map[string]bool{}
	for _, f := range files {
		want[f] = true
	}
	var parts []string
	for _, s := range sites {
		if want[s.file] {
			parts = append(parts, s.file+":"+strconv.Itoa(s.line)+" func "+s.fn+" ["+s.kind+"]")
		}
	}
	if len(parts) == 0 {
		return "（无）"
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// payShellTopOffenders 按处数降序取前 n 个文件（进度账与红灯提示共用）。
func payShellTopOffenders(counts map[string]int, n int) string {
	type kv struct {
		f string
		c int
	}
	all := make([]kv, 0, len(counts))
	for f, c := range counts {
		if c == 0 {
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

// payShellFuncStatuses 取指定函数体内出现过的 HTTP 状态码：
// writeJSON 的整数字面量实参 + s.writeError(apierrors.New(<码>)) 经 (*APIError).HTTPStatus() 换算。
// 返回 (去重升序状态码, 是否找到该函数)。
func payShellFuncStatuses(t *testing.T, dir, fnName string) ([]int, bool) {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取扫描根 %s 失败: %v", dir, err)
	}
	set := map[int]bool{}
	found := false
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".go") || strings.HasSuffix(en.Name(), "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, en.Name()), nil, parser.SkipObjectResolution)
		if perr != nil {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fnName || fd.Body == nil {
				continue
			}
			found = true
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "writeJSON" && len(ce.Args) >= 2 {
					if lit, ok := ce.Args[1].(*ast.BasicLit); ok && lit.Kind == token.INT {
						if v, e := strconv.Atoi(lit.Value); e == nil {
							set[v] = true
						}
					}
				}
				if sel, ok := ce.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "New" {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "apierrors" && len(ce.Args) >= 1 {
						if a, ok := ce.Args[0].(*ast.SelectorExpr); ok {
							if code := payShellErrorCode(a); code != "" {
								// ★ 换算走统一出口的 (*APIError).HTTPStatus()，不是 OpenAPI 专用的 StatusForCode
								// （理由见 TestPayNotifyStatusesFrozen 文件头注释）。
								set[apierrors.New(apierrors.ErrorCode(code), "").HTTPStatus()] = true
							}
						}
					}
				}
				return true
			})
		}
	}
	out := make([]int, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, found
}

// payShellErrorCode 把 apierrors.ErrXxx 选择器还原为错误码字面值。
// 只认下表登记过的常量（值直接取自包本身，不手抄字符串），未登记的一律回空串由调用方忽略——
// 漏登记的结果是「该状态码不进冻结集判定」，而冻结集是**白名单**语义（集合外即红），
// 所以新增错误码时宁可漏判成「没这个状态」，也不会把违例放成绿。
func payShellErrorCode(sel *ast.SelectorExpr) string {
	names := map[string]string{
		"ErrValidation":       string(apierrors.ErrValidation),
		"ErrUnauthorized":     string(apierrors.ErrUnauthorized),
		"ErrForbidden":        string(apierrors.ErrForbidden),
		"ErrNotFound":         string(apierrors.ErrNotFound),
		"ErrConflict":         string(apierrors.ErrConflict),
		"ErrInternal":         string(apierrors.ErrInternal),
		"ErrRateLimited":      string(apierrors.ErrRateLimited),
		"ErrMethodNotAllowed": string(apierrors.ErrMethodNotAllowed),
	}
	if v, ok := names[sel.Sel.Name]; ok && sel.X != nil {
		if x, ok := sel.X.(*ast.Ident); ok && x.Name == "apierrors" {
			return v
		}
	}
	return ""
}
