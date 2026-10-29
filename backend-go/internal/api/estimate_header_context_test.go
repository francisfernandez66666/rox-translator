// ============ 本文件职责中文说明 ============
// `POST /api/estimate` 的 `header_context_chars` 闸门锁 —— ★ 2026-10-28 计价与成本优化批（方案 §6.2(d)(f)）。
//
// 三件事必须同时成立，缺一条就是"预估与实扣两把尺子"：
//  1. 字段**缺省 0 与老客户端逐字等价**（不新增必填字段，老缓存包照常工作）；
//  2. 开关打开且 >0 时预估**恰好**多出一条 allowance（不是"大约多一点"）；
//  3. 开关关闭时**一分钱都不计**——产品此时根本不注入表头，照它收费＝让客户为没发生的事备钱。
//
// 第三条是这一族最容易长歪的地方：写成"只要传了就计价"看起来更"保守"，
// 实际是把运维忘开的开关变成对客户的**多收**（同 §一·5「取不到就渲染空态且零金额」的镜像）。
// ============================================
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"translator/internal/engine"
)

// estBaseReq 文件/工单形态的基础入参（走 EstimateSegmentTokens 那一腿，最贴近真实调用方）。
func estBaseReq() estimateReq {
	return estimateReq{SegmentCount: 12, TargetLangs: []string{"en"}, Mode: "pro", UILang: "zh"}
}

// TestEstimateHeaderContextCharsDefaultIsZero 缺省 0 与"传都不传"逐字等价。
func TestEstimateHeaderContextCharsDefaultIsZero(t *testing.T) {
	s := &Server{}
	t.Setenv("FILE_HEADER_CONTEXT", "on") // 连开关打开时也要等价（缺省就是 0）
	m0, x0 := s.estimateTokens(estBaseReq(), "pro", 0)

	r := estBaseReq()
	r.HeaderContextChars = 0
	m1, x1 := s.estimateTokens(r, "pro", 0)
	if m0 != m1 || x0 != x1 {
		t.Fatalf("header_context_chars 缺省 0 必须与不传等价：%d/%d vs %d/%d", m1, x1, m0, x0)
	}
	// 负数是脏值：折回 0，不许倒扣（倒扣会让预估比真实成本低，客户备的钱不够）。
	r.HeaderContextChars = -50
	if m2, x2 := s.estimateTokens(r, "pro", 0); m2 != m0 || x2 != x0 {
		t.Fatalf("负数字符数必须折回 0，实际 %d/%d（基准 %d/%d）", m2, x2, m0, x0)
	}
}

// TestEstimateHeaderContextCharsCountedOnlyWhenEnabled 开关三态：on ⇒ 精确加一条 allowance；
// off/空 ⇒ 一字不加（这才是"只在真会注入时才计价"）。
func TestEstimateHeaderContextCharsCountedOnlyWhenEnabled(t *testing.T) {
	s := &Server{}
	const chars = 400
	allow := engine.HeaderContextAllowanceTokens(chars)
	if allow <= 0 {
		t.Fatalf("allowance(%d) 应 >0，实际 %d ⇒ 本锁会恒绿", chars, allow)
	}
	r := estBaseReq()
	r.HeaderContextChars = chars

	// ① 关闭档（默认）⇒ 不加价
	t.Setenv("FILE_HEADER_CONTEXT", "off")
	baseM, baseX := s.estimateTokens(estBaseReq(), "pro", 0)
	offM, offX := s.estimateTokens(r, "pro", 0)
	if offM != baseM || offX != baseX {
		t.Fatalf("FILE_HEADER_CONTEXT=off 时不许计表头钱：%d/%d vs 基准 %d/%d ⇒ 客户为没发生的事备钱",
			offM, offX, baseM, baseX)
	}

	// ② 打开档 ⇒ 逐字多一条 allowance（下限与上限一起抬，区间形状不变）
	t.Setenv("FILE_HEADER_CONTEXT", "on")
	onM, onX := s.estimateTokens(r, "pro", 0)
	if onM != baseM+allow || onX != baseX+allow {
		t.Fatalf("开启后预估应恰好 +%d：%d/%d，实际 %d/%d ⇒ 要么没接上，要么另算了一份字数",
			allow, baseM+allow, baseX+allow, onM, onX)
	}

	// ③ 上界钳位：异常入参按 4000 计（与 engine 的钳位同档），不许线性膨胀成天价
	r.HeaderContextChars = 999999
	capM, _ := s.estimateTokens(r, "pro", 0)
	if want := baseM + engine.HeaderContextAllowanceTokens(4000); capM != want {
		t.Fatalf("超上限应钳在 4000（基准+%d），实际 %d ⇒ 一个异常入参能把预估算成天价",
			engine.HeaderContextAllowanceTokens(4000), capM)
	}
	// 反向读数：默认档 400 与上界 4000 必须给出不同读数（证明上面那条不是"钳位把正常档压平了"）
	if capM == onM {
		t.Fatal("400 与 999999 算出同一个预估 ⇒ 钳位把正常档也压平了（本锁失去射程）")
	}
}

// TestEstimateHeaderContextCharsFieldContract 入参字段名是**对外契约**：老客户端不传、
// 新客户端按 `header_context_chars` 传；改名会让新客户端的字段被静默忽略（少收/多收都没人发现）。
func TestEstimateHeaderContextCharsFieldContract(t *testing.T) {
	var r estimateReq
	if err := json.Unmarshal([]byte(`{"segment_count":3,"header_context_chars":123}`), &r); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if r.HeaderContextChars != 123 {
		t.Fatalf("字段 header_context_chars 没被解析（%d）⇒ 前端发了也白发，预估永远按 0 算", r.HeaderContextChars)
	}
	// 老客户端形态（完全没有这个键）必须照旧
	var old estimateReq
	if err := json.Unmarshal([]byte(`{"segment_count":3}`), &old); err != nil {
		t.Fatalf("老客户端报文解析失败：%v ⇒ 破坏了向后兼容", err)
	}
	if old.HeaderContextChars != 0 {
		t.Fatalf("缺省应为 0，实际 %d", old.HeaderContextChars)
	}
}

// TestEstimateHeaderContextGatedBySameSwitch 源码腿：计价开关必须与**注入**开关是同一个判据
// （`fileproc.FileHeaderContextEnabled()`）。
//
// 为什么这条不能只靠上面那几条行为锁：行为锁只证明"env=on 时加、off 时不加"，
// 而真实的坏形态是"计价读 env 字符串、注入读另一个函数"——两边各写一份解析，
// 某天有人给注入侧加了 `yes` 也认，于是那一档就是"真注入了但不计价"。
func TestEstimateHeaderContextGatedBySameSwitch(t *testing.T) {
	src := readEstimateSource(t)
	code := stripCommentsForGate(src)
	body, ok := estFuncBodyOf(code, "func (s *Server) estimateTokens")
	if !ok {
		t.Fatal("找不到 estimateTokens ⇒ 锚点漂了、本锁已失明")
	}
	if !strings.Contains(body, "fileproc.FileHeaderContextEnabled()") {
		t.Fatal("estimateTokens 必须用 fileproc.FileHeaderContextEnabled() 判开关（与注入侧同一个判据）⇒ " +
			"各写一份解析迟早出现「真注入了但不计价」")
	}
	if !strings.Contains(body, "engine.HeaderContextAllowanceTokens(") {
		t.Fatal("estimateTokens 必须薄委托 engine.HeaderContextAllowanceTokens（公式只在 engine 里一份）")
	}
	// 负向：接口层不许自己写"字符数 × 系数"这类算式（那是第二把尺子）。
	for _, banned := range []string{"HeaderContextChars * ", "HeaderContextChars*", "HeaderContextChars / "} {
		if strings.Contains(body, banned) {
			t.Fatalf("estimateTokens 里出现自算算式 %q ⇒ 预估与实扣不再同尺", banned)
		}
	}
}
