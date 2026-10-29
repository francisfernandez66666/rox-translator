// ============ 本文件职责中文说明 ============
// P5 表头上下文的「预估与实扣同尺」闸门锁 —— ★ 2026-10-28 计价与成本优化批（方案 §6.2(d)(f)）。
//
// 这一族锁的是三件**只有放在一起看才成立**的事：
//  1. **提示句真的进了 system 前缀**（捕获真发出去的请求体，不是读源码猜）；
//  2. **预估从那条真提示句现量**（改模板即改预估，AGENTS §一·5④ 同一把尺子）；
//  3. **空表头时 system 逐字不变**（关闭 P5 ⇒ 提示词与改造前字节等价，历史记忆键不被打散）。
//
// 只测其中任何一条都会漏掉真实的坏形态：
//
//	· 只测 allowance 公式 ⇒ 提示句可能压根没进 prompt（客户白付钱、译文没变好）；
//	· 只测 prompt 注入 ⇒ 预估可能另写一份字数（预估与实扣两把尺子，本仓最老的坑）；
//	· 只测"有表头时变了" ⇒ 空表头那一路可能多了一句空壳指令（"表头为：''"）。
//
// ============================================
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
)

// hdrCaptureUpstream 记下每一次请求的 system 前缀，并按 `<sN>` 回固定译文。
// 与 tmStubUpstream 的区别只有一处：它**必须**把 system 存下来（本文件全部判据的问句都在那里）。
type hdrCaptureUpstream struct {
	mu      sync.Mutex
	systems []string
	users   []string
	reply   string
}

func (u *hdrCaptureUpstream) handler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sys, user := "", ""
	for _, m := range body.Messages {
		switch m.Role {
		case "system":
			sys = m.Content
		case "user":
			user = m.Content
		}
	}
	u.mu.Lock()
	u.systems = append(u.systems, sys)
	u.users = append(u.users, user)
	u.mu.Unlock()

	content := ""
	for _, m := range tmSlotRe.FindAllStringSubmatch(user, -1) {
		content += fmt.Sprintf("<s%s>%s</s%s>\n", m[1], u.reply, m[1])
	}
	if content == "" {
		// 单条兜底腿（singleLang）没有 <sN>：整段直接回。
		content = u.reply
	}
	raw, _ := json.Marshal(content)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, raw)
}

func (u *hdrCaptureUpstream) lastSystem(t *testing.T) string {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.systems) == 0 {
		t.Fatal("一次上游请求都没发生 ⇒ 本文件所有判据都读不到 prompt（夹具自伤）")
	}
	return u.systems[len(u.systems)-1]
}

// hdrTestEngine 装配：内存 Store + 捕获型假上游 + 自钉 sqlite 方言（AGENTS §一·4）。
func hdrTestEngine(t *testing.T, u *hdrCaptureUpstream) *Engine {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(u.handler))
	t.Cleanup(srv.Close)
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	e := NewEngine(cfg, nil, nil, nil)
	e.St = newTestStore(t)
	return e
}

// hdrSample 一段必被真正送进 prompt 的源文（含拉丁与汉字，走批量腿）。
var hdrSample = []string{"请勿拆卸电池。", "首次使用前请充电。"}

// ============ H1：提示句真的进了 system ============

// TestHeaderContextReachesSystemPrompt 捕获真请求：表头文本与"只作语境"那句都必须出现在 system 里，
// 且**不许**出现在 user（user 是待译段，多一段文本会被模型当成也要翻的内容）。
func TestHeaderContextReachesSystemPrompt(t *testing.T) {
	u := &hdrCaptureUpstream{reply: "Do not disassemble the battery."}
	e := hdrTestEngine(t, u)
	ctx := context.Background()
	hdr := "项目 | 金额 | 说明；甲方"
	if out := e.BatchTranslateOpts(ctx, hdrSample, "en", 0, nil, BatchOptions{HeaderContext: hdr}); len(out) != len(hdrSample) {
		t.Fatalf("结果长度应为 %d，实际 %d", len(hdrSample), len(out))
	}
	sys := u.lastSystem(t)
	if !strings.Contains(sys, hdr) {
		t.Fatalf("system 前缀里找不到表头 %q ⇒ 表头没被注入（allowance 却照收钱）\nsystem=%q", hdr, sys)
	}
	if !strings.Contains(sys, "terminology context") {
		t.Fatalf("system 里缺少「只作语境」那句约束 ⇒ 模型会把表头当成一条待译内容，回吐多一条把后面的段挤位\nsystem=%q", sys)
	}
	u.mu.Lock()
	user := u.users[len(u.users)-1]
	u.mu.Unlock()
	if strings.Contains(user, hdr) {
		t.Fatalf("表头进了 user 消息 ⇒ 那是待译段的位置，模型会把它也翻出来（只许挂 system）\nuser=%q", user)
	}
}

// ============ H2：空表头 ⇒ system 逐字不变 ============

// TestHeaderContextEmptyKeepsSystemByteIdentical 关闭 P5 的两个等价形态（零值 options / 纯空白表头）
// 必须给出**逐字相同**的 system。不成立就是"上线当天提示词变了一句、全站记忆键作废"。
func TestHeaderContextEmptyKeepsSystemByteIdentical(t *testing.T) {
	u := &hdrCaptureUpstream{reply: "Do not disassemble the battery."}
	e := hdrTestEngine(t, u)
	ctx := context.Background()

	e.BatchTranslateOpts(ctx, hdrSample, "en", 0, nil, BatchOptions{})
	base := u.lastSystem(t)

	e.BatchTranslateOpts(ctx, hdrSample, "en", 0, nil, BatchOptions{HeaderContext: "   \n\t"})
	blank := u.lastSystem(t)

	if base != blank {
		t.Fatalf("空表头与零值 options 的 system 不一样 ⇒ 关闭 P5 时提示词变了（历史记忆键全废）\nbase=%q\nblank=%q", base, blank)
	}
	// 反向读数：上面两条若**都**没注入成功，这个等值判据会恒真 —— 所以同一夹具里再验一次"有表头时真的不同"。
	e.BatchTranslateOpts(ctx, hdrSample, "en", 0, nil, BatchOptions{HeaderContext: "项目 | 金额"})
	if u.lastSystem(t) == base {
		t.Fatal("有表头的 system 与无表头一样 ⇒ 注入腿根本没接上（本用例的等值判据因此是空转的）")
	}
}

// ============ H3：allowance 从真提示句现量 ============

// TestHeaderContextAllowanceDerivesFromRealPrompt 预估的固定项必须**逐字**等于真提示句的 token 数。
//
// 为什么写成等值而不是"大于 0"：改了 `headerContextNoteEN` 的措辞而忘改预估，
// 是这一族唯一的漂移方向；把两边都从同一个常量现算，改模板就自动同尺，
// 而任何"另抄一份字数"的写法会在这里当场红。
func TestHeaderContextAllowanceDerivesFromRealPrompt(t *testing.T) {
	fixed := ScriptTokens(fmt.Sprintf(headerContextNoteEN, ""))
	if fixed <= 0 {
		t.Fatalf("提示句固定开销应 >0，实际 %d ⇒ 常量读空了（本锁会恒绿）", fixed)
	}
	// 逐档等值：allowance(chars) == fixed + chars（按宽体 1 字符≈1 token 取上界）
	for _, chars := range []int{1, 40, 400} {
		if got, want := HeaderContextAllowanceTokens(chars), fixed+int64(chars); got != want {
			t.Fatalf("allowance(%d)=%d，必须等于固定项 %d + 表头字符数 %d = %d（改提示句必须同时动预估）",
				chars, got, fixed, chars, want)
		}
	}
	// 边界：0 与负数一律 0（关闭 P5 / 没有表头时与改造前完全等价）
	for _, chars := range []int{0, -1, -10000} {
		if got := HeaderContextAllowanceTokens(chars); got != 0 {
			t.Fatalf("allowance(%d)=%d，必须为 0（否则关着开关也在收表头的钱）", chars, got)
		}
	}
	// 上界：钳在 4000（与 /api/estimate 入参钳位同档），超限不许继续线性上涨
	if got := HeaderContextAllowanceTokens(4000); got != fixed+4000 {
		t.Fatalf("allowance(4000) 应为 %d，实际 %d", fixed+4000, got)
	}
	if a, b := HeaderContextAllowanceTokens(4001), HeaderContextAllowanceTokens(999999); a != b || a != fixed+4000 {
		t.Fatalf("超上限必须钳在 4000（%d/%d）⇒ 异常入参能算出天价，客户按它备钱", a, b)
	}
	// 反向对照：钳位不许把正常档也钳住（400 是默认档，必须原样）
	if HeaderContextAllowanceTokens(400) == HeaderContextAllowanceTokens(4000) {
		t.Fatal("默认档 400 与上界 4000 算出同一个值 ⇒ 钳位把正常档也压平了")
	}
}

// ============ H4：源码接线（预估与注入用同一个常量） ============

// TestHeaderContextWiringIsSingleSourced 静态腿：注入与预估必须引用**同一个**提示句常量，
// 且批量 sysBatch 的拼装顺序里表头句在编号契约之前。
//
// 为什么加这一条（判据级代替不了守卫级）：allowance 与注入各自"看起来对"却用了两份措辞时，
// 上面的等值锁仍会绿（它只比 ScriptTokens(同一个常量)），而实际发出去的句子更长 ⇒ 客户被少算。
func TestHeaderContextWiringIsSingleSourced(t *testing.T) {
	wiring := map[string][]string{
		"costmodel.go": {"headerContextNoteEN"},
		"engine.go": {
			"func headerContextNote", "headerContextNoteEN",
			"headerContextNote(opts.HeaderContext)", "BatchTranslateMarkedOpts",
		},
		"file.go": {"BatchOptions{HeaderContext: fileHeaderCtx"},
	}
	for file, mustRef := range wiring {
		src := readEngineSource(t, file)
		for _, m := range mustRef {
			if !strings.Contains(src, m) {
				t.Fatalf("%s 里找不到 %q ⇒ 接线断了一处（改这块必须同步注入与预估两条腿）", file, m)
			}
		}
	}
	// 负向：成本模型不许把提示句措辞再抄一份（第二份尺子=预估与实扣不同步）。
	// 判定前必须剥注释：本仓已三次踩在"负向锁被自己新写的说明注释喂红"上。
	cm := dropGoComments(readEngineSource(t, "costmodel.go"))
	if c := strings.Count(cm, "terminology context"); c != 0 {
		t.Fatalf("costmodel.go 里出现了提示句措辞的副本（%d 处）⇒ 预估与注入成了两把尺子，改模板只改一边", c)
	}
}

// ============ H5：三处调用点都在开关里（源码腿） ============

// headerContextCallSiteViolations 表头接线的静态判据唯一本体（正证与反证喂同一个函数）。
// 射程＝file.go：
//  1. 抽取必须发生在 `if fileproc.FileHeaderContextEnabled() {` 之内（关开关 ⇒ 连子进程都不许起）；
//  2. 开关之前不许给 `fileHeaderCtx` 赋值（否则"关着也注入了"）；
//  3. 三处批量调用点（KB 路径 / directOther 路径 / 硬闸重译）都必须是带 opts 的那一口，
//     且**不许**残留老签名 `e.BatchTranslate(ctx`（那条腿会静默没有上下文）。
func headerContextCallSiteViolations(src string) []string {
	var v []string
	code := dropGoComments(src)
	const marker = "if fileproc.FileHeaderContextEnabled() {"
	i := strings.Index(code, marker)
	if i < 0 {
		return []string{"file.go 里找不到表头开关 ⇒ 抽取腿已失明（或判据被摘掉）"}
	}
	// 花括号配平切出 guard 体（同仓代码风格下"下一个顶层声明"那种切法会切过头）。
	open := i + strings.Index(code[i:], "{")
	depth, end := 0, -1
	for k := open; k < len(code); k++ {
		switch code[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = k
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return []string{"表头开关的花括号没配平（本锁切不出 guard 体）"}
	}
	guard, before := code[open:end+1], code[:i]
	if !strings.Contains(guard, "TableHeaderContext(") {
		v = append(v, "表头抽取不在 FILE_HEADER_CONTEXT 开关内 ⇒ 关着开关照样起子进程、照样记读数")
	}
	if strings.Contains(before, "fileHeaderCtx =") {
		v = append(v, "开关之前就给 fileHeaderCtx 赋值 ⇒ 关着开关也会注入")
	}
	// ★ 锚点只取**前缀**（不写闭括号）：BatchOptions 后来加了 LiteralOCR 这一维，
	// 按整串字面量数会把"加了第三个开关"判成"漏了表头"——那是锚漂，不是回归（2026-10-29 真红过一次）。
	if sites := strings.Count(code, "BatchOptions{HeaderContext: fileHeaderCtx"); sites != 3 {
		v = append(v, fmt.Sprintf("三处批量调用点都必须带表头 opts，实际 %d 处 ⇒ 漏掉的那条腿静默没有上下文", sites))
	}
	if strings.Contains(code, "e.BatchTranslate(ctx") {
		v = append(v, "仍有老签名 e.BatchTranslate 调用点 ⇒ 那条腿拿不到表头上下文（且不会报错）")
	}
	return v
}

// TestHeaderContextCallSitesAreGated 源码腿 + 三枚反证。
func TestHeaderContextCallSitesAreGated(t *testing.T) {
	src := readEngineSource(t, "file.go")
	if v := headerContextCallSiteViolations(src); len(v) > 0 {
		t.Fatalf("表头接线判据不过: %v", v)
	}

	// 反证①：开关改成恒真（等于"永远抽取"）⇒ 必须判红（锚点消失也是一种红：说明形态被改坏了）
	m1 := strings.Replace(src, "if fileproc.FileHeaderContextEnabled() {", "if true {", 1)
	if m1 == src {
		t.Fatal("反证①未命中锚点（开关形态变了，请同步本锁）")
	}
	if len(headerContextCallSiteViolations(m1)) == 0 {
		t.Fatal("反证①未判红：把开关写成恒真必须被抓（关着开关也起子进程）")
	}

	// 反证②：摘掉一处 opts（模拟"某条腿漏传"）⇒ 计数必须变 2 并判红。
	// 同样按前缀摘：整串字面量在 BatchOptions 加第二个开关后就不存在了，
	// 拿它做锚点会让这枚反证**静默空转**（strings.Replace 找不到就是原串，然后靠 m2==src 才报错——
	// 那正是本仓「反证被自己的锚点喂饱」那一族，2026-10-29 真踩）。
	m2 := strings.Replace(src, "BatchOptions{HeaderContext: fileHeaderCtx", "BatchOptions{", 1)
	if m2 == src {
		t.Fatal("反证②未命中锚点（调用点形态变了，请同步本锁）")
	}
	if v := headerContextCallSiteViolations(m2); len(v) == 0 {
		t.Fatal("反证②未判红：漏传一处表头 opts 必须被抓（那条腿会静默没有上下文）")
	}

	// 反证③：在开关之前给 fileHeaderCtx 赋值 ⇒ 必须判红
	m3 := strings.Replace(src, "\tfileHeaderCtx := \"\"", "\tfileHeaderCtx := \"\"\n\tfileHeaderCtx = \"偷跑\"", 1)
	if m3 == src {
		t.Fatal("反证③未命中锚点（fileHeaderCtx 声明形态变了，请同步本锁）")
	}
	if v := headerContextCallSiteViolations(m3); len(v) == 0 {
		t.Fatal("反证③未判红：开关之外赋值必须被抓（关着开关也注入了）")
	}
}
