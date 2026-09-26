// ============================================================================
// payhonesty_gate_test.go — 「HTTP 200 承载失败」零容忍等值锁
// （★ F-64① 批 I-7 建立，2026-09-26；★ F-64②③ 批 I-10 收口成等值锁，同日深夜）
//
// 缺陷本体（UAT 报告 F-47 / 修复文档 F-64）：本包大量业务失败分支写成
//
//	writeJSON(w, 200, map[...]{"success": false, "message": ...})
//
// ——HTTP 层永远是 200，失败只藏在 body 里。对浏览器前端也许够用，但**客户与 SDK 是按状态码
// 分支的**：wx.request/fetch/重试策略/网关告警/CDN 缓存/监控 5xx 率全都看不到这类失败。
// 实测规模与本批处置（本文件的扫描器逐调用点量出，不是估的）：
//   - 建立闸门时 internal/api 全包 **240 处**，其中收款/账务 7 个文件 **61 处**（①档，批 I-7 清零）；
//   - 余量 **182 处** 于批 I-10（②档管理台与业务动作类）全部迁到 s.writeError + apierrors，
//     外加 ③档最后 1 处 writeAssistBizErr（assist 代理本层失败）⇒ **全包现为 0**；
//   - 前端配套：104 个接口调用点包上 bizResp()，调用点的 `if (!r.success)` 判据一字未改。
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
//   - 跨函数传参（A 函数造好 success:false 的 map 交给 B 函数写 200）扫不到——历史上唯一的这种写法
//     是 writeAssistBizErr，批 I-10 已把它改成直接走 s.writeError，本包现无此类间接出口；
//     ⚠️ 若日后有人再造一个「统一回 200 的私有 helper」，本闸门看不见，得先让判据覆盖跨函数形态。
//   - 「对端只认 2xx」的应答豁免分两处：本文件的 payShellMustStay200（200 壳白名单，现为空集）
//     与 payFrozenStatuses（handlePayNotify 的状态码冻结集，渠道回调）。
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

// payShellMustStay200 ③档收口后的**显式白名单**（★ 批 I-10，2026-09-26 深夜）：
// 键＝`文件名#函数名`，值＝「为什么这里必须回 200」的一句话理由。
//
// 口径变化（本闸门从「棘轮」升级成「等值锁」的那一步）：
//   - 批 I-7 建立本闸门时全包实测 240 处，只能记总数做「只减不增」棘轮；
//   - 批 I-10（②/③档）把 240→0 全部迁完（①档收款/账务 7 文件 61 处 + 余量 182 处 +
//     最后 1 处 writeAssistBizErr），于是「还剩多少」不再是进度问题，而是**违例**；
//   - 因此删掉 payShellTotalBaseline 与 payShellTier2Files 两张表，换成下面的白名单：
//     白名单之外出现任何一处 200 壳即红灯，白名单里的条目若已归零同样红灯（僵尸守卫）。
//
// 什么情况才允许往这里加一行：**对端只认 2xx 应答语义**、给它 4xx/5xx 反而制造重复提交或
// 重试风暴的出口（参考 handlePayNotify 的渠道回调）。以下理由**不成立**，别写进来：
//   - 「前端调用点太多，改不过来」→ 用 bizResp 收敛（本批 104 个调用点就是这么包的）；
//   - 「SSE/CSV 已经 flush 了」→ 那种分支根本不经过 writeJSON，本扫描器不统计，无需登记；
//   - 「管理台内部接口，客户不消费」→ 监控、SDK、重试器与自动化都按状态码分支，内部不等于可以骗。
var payShellMustStay200 = map[string]string{}

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

// TestPayShellWhitelistZero 全包等值锁（★ 批 I-10 ③档收口，取代原「总数棘轮」）：
// 白名单之外「200 承载失败」必须**恰为 0**，且白名单自身不许留僵尸条目。
//
// 为什么不再是棘轮：只减不增的写法在存量已为 0 时会把「新增一处 200 壳」和「维持 0」
// 用同一句放过（`0 > 0` 假、`0 ≤ 0` 真），等于没有闸门——历史上 UI 侧就被这种单向锁一路推离交付稿。
func TestPayShellWhitelistZero(t *testing.T) {
	sites := payShellScan(t, ".")
	violations, zombies := payShellWhitelistCheck(sites, payShellMustStay200)
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("★ 白名单之外出现「HTTP 200 承载失败」%d 处（等值锁：必须为 0）：\n  %s\n"+
			"HTTP 层永远是 200，按状态码分支的一方（客户 SDK、重试器、网关告警、监控 5xx 率）会把失败读成成功。\n"+
			"改法：s.writeError(w, r, apierrors.New(<语义码>, \"中文文案\"))，附加字段走 WithDetails；\n"+
			"前端接口层用 bizResp() 收敛，调用点的 if (!r.success) 判据即可原样保留。\n"+
			"确属「对端只认 2xx」的出口，才允许登记进 payShellMustStay200（登记门槛见其注释）。",
			len(violations), strings.Join(violations, "\n  "))
	}
	for _, z := range zombies {
		t.Errorf("★ payShellMustStay200 僵尸条目：%s 已无 200 壳，请删掉该行并复核理由是否还成立\n  （原登记理由：%s）", z.key, z.why)
	}
	// 进度账（t.Logf）：白名单成员与处数每次跑都打出来，便于评审时看见「谁在被豁免」。
	if len(payShellMustStay200) > 0 {
		byKey := map[string]int{}
		for _, s := range sites {
			byKey[s.file+"#"+s.fn]++
		}
		keys := make([]string, 0, len(byKey))
		for k := range byKey {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Logf("200 壳白名单在账 %d 处：%s", len(sites), strings.Join(keys, ", "))
	} else {
		t.Logf("全包「200 承载失败」= %d 处，白名单为空（F-64①②③ 已收口，任何新增即红灯）", len(sites))
	}
}

// payShellWhitelistCheck 等值锁的纯判定核（抽出来是为了能被内存用例正反两向喂，见其自检用例）。
// 返回：白名单外的违例明细、白名单里已无存量的僵尸条目。
func payShellWhitelistCheck(sites []payShellSite, whitelist map[string]string) ([]string, []zombieEntry) {
	byKey := map[string]int{}
	var violations []string
	for _, s := range sites {
		k := s.file + "#" + s.fn
		byKey[k]++
		if _, ok := whitelist[k]; !ok {
			violations = append(violations, s.file+":"+strconv.Itoa(s.line)+" func "+s.fn+" ["+s.kind+"]")
		}
	}
	var zombies []zombieEntry
	for k, why := range whitelist {
		if byKey[k] == 0 {
			zombies = append(zombies, zombieEntry{key: k, why: why})
		}
	}
	return violations, zombies
}

// zombieEntry 僵尸白名单条目（键 + 当初登记的豁免理由）。
type zombieEntry struct {
	key string
	why string
}

// TestPayShellWhitelistCheckSelfCheck 等值锁的反证：判据核必须既能抓违例、也能抓僵尸，
// 且「白名单命中」与「无违例」两种绿态都不会把红态吞掉（守卫没有反证＝迟早变成假绿）。
func TestPayShellWhitelistCheckSelfCheck(t *testing.T) {
	sites := []payShellSite{
		{file: "a.go", line: 10, fn: "handleA", kind: "A"},
		{file: "b.go", line: 20, fn: "handleB", kind: "C"},
	}
	// ① 空白名单 → 两处都算违例，无僵尸
	v, z := payShellWhitelistCheck(sites, map[string]string{})
	if len(v) != 2 || len(z) != 0 {
		t.Errorf("反证①失败：空白名单应报 2 违例 0 僵尸，实得 %v / %v", v, z)
	}
	// ② 登记 a.go#handleA → 该处豁免，b.go#handleB 仍违例
	v, z = payShellWhitelistCheck(sites, map[string]string{"a.go#handleA": "对端只认 2xx"})
	if len(v) != 1 || !strings.Contains(v[0], "b.go") || len(z) != 0 {
		t.Errorf("反证②失败：应只剩 b.go 一处违例，实得 %v / %v", v, z)
	}
	// ③ 登记一个已不存在的键 → 僵尸守卫必须响
	v, z = payShellWhitelistCheck(sites, map[string]string{"c.go#handleC": "已失效的历史豁免"})
	if len(v) != 2 || len(z) != 1 || z[0].key != "c.go#handleC" {
		t.Errorf("反证③失败：应报 2 违例 + 1 僵尸，实得 %v / %v", v, z)
	}
	// ④ 全登记 → 双向皆绿（证明「绿」是可达状态，不是一句永远红的手工锁）
	v, z = payShellWhitelistCheck(sites, map[string]string{"a.go#handleA": "x", "b.go#handleB": "y"})
	if len(v) != 0 || len(z) != 0 {
		t.Errorf("反证④失败：全量登记后应 0 违例 0 僵尸，实得 %v / %v", v, z)
	}
	// ⑤ 空站点集 + 空白名单 → 什么都不报（锁在「已收口」状态下静默通过，而非恒红）
	v, z = payShellWhitelistCheck(nil, map[string]string{})
	if len(v) != 0 || len(z) != 0 {
		t.Errorf("反证⑤失败：零存量零豁免应全绿，实得 %v / %v", v, z)
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
	// 仓库现实锚点（★ 批 I-10 改口径）：②/③档收口后全包真实存量是 **0**，
	// 所以「扫到空集」不再能证明遍历失效——旧的 `len(sites) < 100` 锚点当场作废（它会恒红）。
	// 换成**阳性对照**：拿本包一个真实文件在内存里追加一处 200 壳，
	// 计数必须恰好 +1；再加一处成功应答，计数必须不变。
	// 这样「判据失效」与「存量真的为 0」两种情况就能分开，锁不会把前者伪装成后者。
	sites := payShellScan(t, ".")
	if got := len(sites); got != 0 {
		t.Errorf("★ 扫描到 %d 处「200 承载失败」，但白名单等值锁要求 0：请核对是哪一批改动新增（本用例只做覆盖自检）", got)
	}
	probeFile, probeSrc := payShellPickProbeSource(t, ".")
	base := payShellCountSource(t, probeFile, probeSrc)
	withShell := payShellCountSource(t, probeFile, probeSrc+`
func payShellProbeTmp(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]interface{}{"success": false, "message": "probe"})
}
`)
	withOK := payShellCountSource(t, probeFile, probeSrc+`
func payShellProbeTmp2(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]interface{}{"success": true, "n": 1})
}
`)
	if withShell != base+1 {
		t.Fatalf("阳性对照失败（%s）：追加一处 200 壳后应 %d→%d，实得 %d——扫描器已不可信，本闸门的所有绿灯一律作废",
			probeFile, base, base+1, withShell)
	}
	if withOK != base {
		t.Fatalf("阴性对照失败（%s）：追加成功应答不应计数，期望 %d 实得 %d", probeFile, base, withOK)
	}
	// 遍历覆盖面锚点（★ 批 I-10 改口径）：存量已合法归零，所以「命中文件数」不再是有效锚点，
	// 换成「被解析的非测试文件数」——遍历漏目录/漏文件是本闸门唯一还能悄悄失效的方式。
	// 本包当前有 60+ 个非测试源文件，取下限 40（留出按域拆分的余量，又能在只扫到子集时报警）。
	if n := payShellScannedFileCount(t, "."); n < 40 {
		t.Errorf("扫描根只遍历到 %d 个非测试 .go 文件（本包实际 60+）：遍历或过滤条件已失效，判据对所有文件不成立", n)
	}
	for _, f := range payHonestZeroFiles {
		if !payShellFileExists(f) {
			t.Errorf("零容忍文件 %s 未被遍历（不存在或解析失败？）", f)
		}
	}
}

// payShellPickProbeSource 从扫描根里挑一个「当前无 200 壳」的真实文件源码做对照实验的基底。
// 选它而不是凭空造一段：基底来自生产代码，能同时证明解析器吃得下本包的真实写法
// （长函数、嵌套 map、SSE 分支），对照结果才有意义。
func payShellPickProbeSource(t *testing.T, dir string) (string, string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取扫描根 %s 失败: %v", dir, err)
	}
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".go") || strings.HasSuffix(en.Name(), "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(filepath.Join(dir, en.Name()))
		if rerr != nil {
			continue
		}
		// 基底要求自身无命中（否则 +1 的算术仍成立但读起来含混），实测本包收口后所有文件都满足
		if payShellCountSource(t, en.Name(), string(src)) == 0 {
			return en.Name(), string(src)
		}
	}
	t.Fatal("找不到一个「当前无 200 壳」的真实文件做对照基底：包内状态与本闸门前提不符")
	return "", ""
}

// payShellScannedFileCount 统计扫描根里被遍历到的非测试 .go 文件数（覆盖面锚点，与 payShellScan 同判据）。
func payShellScannedFileCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取扫描根 %s 失败: %v", dir, err)
	}
	n := 0
	for _, en := range entries {
		if !en.IsDir() && strings.HasSuffix(en.Name(), ".go") && !strings.HasSuffix(en.Name(), "_test.go") {
			n++
		}
	}
	return n
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
