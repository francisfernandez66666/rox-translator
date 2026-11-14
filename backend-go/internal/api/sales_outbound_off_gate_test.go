// ============ sales_outbound_off_gate_test.go · 职责说明 ============
// 四期 §7.2 / §4.5.3 **A8 负向锁**（M0305）：三期留下的 outbound_* 表与 API
// 原样保留、开关保持关闭、四期一笔不动。这条锁防的正是「过一阵有人觉得顺手，
// 把默认值打开」。
//
// 与既有 sales_outbound_gate_test.go（三期 A16–A18：sendOne 唯一出口／先占位／
// kill-switch 顺序）互补，不重复：那边钉的是**顺序**，本文件钉的是**缺省语义**与
// **零调用方**，三问各自独立：
//
//	① outbound 默认关：GetConfig 读不到（返回空串）≠ 开；把缺省翻成「开」必红；
//	② runOutboundSequenceOnce 之外没有第二个生产发送调用方；
//	③ 开关显式 "1" 才会进发送路径（其它任何值，含 "true"/"yes"/"on"，一律拦）。
//
// ★ 为什么判据要「从生产源码抽出来求值」，而不是在测试里抄一份判据：
//
//	抄一份 v != "1" 的镜像函数，改生产代码时镜像照旧绿——那正是
//	AGENTS §三 说的「判据级代替不了守卫级」／空转锁。本文件把
//	runOutboundSequenceOnce 里 GetConfig 之后的条件串抽出来喂给求值器，
//	条件一变（翻缺省 / 删判据 / 改比较符），真值表立刻不等 ⇒ 红在锁自己的消息上。
//
// 每条都带反证：翻缺省的坏条件、多一个调用方的坏样本、非 "1" 的放行样本
// 都会被同一套判定函数判成「坏」，证明锁不是恒绿。
// ================================================
package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// a8ReadSource 读 internal/api 下一个非测试源文件。
func a8ReadSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", name))
	if err != nil {
		t.Fatalf("A8 读 %s 失败: %v", name, err)
	}
	return string(b)
}

// a8FuncBody 从函数签名行起截到下一个顶层 func 之前的体（简单行扫描，够用且不引 AST）。
func a8FuncBody(src, header string) string {
	i := strings.Index(src, header)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:]
	}
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// a8ExtractGateCond 从 runOutboundSequenceOnce 体内抽出 kill-switch 的条件串。
// 生产形态：`if v, _ := s.Store.GetConfig("outbound_enabled"); v != "1" {`
// 返回 `v != "1"`；抽不到（判据被删/改形）返回 ok=false ⇒ 锁判红（失明也算坏）。
func a8ExtractGateCond(body string) (string, bool) {
	const marker = `s.Store.GetConfig("outbound_enabled")`
	i := strings.Index(body, marker)
	if i < 0 {
		return "", false
	}
	rest := body[i+len(marker):]
	semi := strings.Index(rest, ";")
	brace := strings.Index(rest, "{")
	if semi < 0 || brace < 0 || semi > brace {
		return "", false
	}
	return strings.TrimSpace(rest[semi+1 : brace]), true
}

// a8GateBlocks 求值一个形如 `v != "X"` / `v == "X"` 的条件串：
// 返回该配置值是否被 kill-switch 拦住（不进发送路径）与「条件串能否解析」。
// 只支持这两种字面比较——生产判据一旦改成语气更松的形态（如 ParseBool、
// 白名单、或干脆删掉）⇒ ok=false ⇒ 锁判红，绝不静默放行。
func a8GateBlocks(cond, v string) (blocks, ok bool) {
	re := regexp.MustCompile(`^v\s*(==|!=)\s*"([^"]*)"$`)
	m := re.FindStringSubmatch(strings.TrimSpace(cond))
	if m == nil {
		return false, false
	}
	switch m[1] {
	case "!=":
		return v != m[2], true
	case "==":
		return v == m[2], true
	}
	return false, false
}

// a8ExpectedBlocks 当前生产判据的期望真值表：只有显式 "1" 放行，其余一律拦。
func a8ExpectedBlocks(v string) bool { return v != "1" }

// a8RunBody 读一次 runOutboundSequenceOnce 的函数体。
func a8RunBody(t *testing.T) (src, body string) {
	t.Helper()
	src = a8ReadSource(t, "sales_outbound_api.go")
	body = a8FuncBody(src, "func (s *Server) runOutboundSequenceOnce(")
	if body == "" {
		t.Fatal("A8 截不到 runOutboundSequenceOnce 函数体：拨轮被删或改名，锁失明")
	}
	return src, body
}

// TestA8OutboundDefaultOff ① outbound 默认关：GetConfig 读不到 ≠ 开；翻缺省为 true 必红。
func TestA8OutboundDefaultOff(t *testing.T) {
	_, body := a8RunBody(t)
	cond, ok := a8ExtractGateCond(body)
	if !ok {
		t.Fatal("A8 ① 抽不到 kill-switch 条件串：outbound_enabled 判据被删/改形（默认关失去守卫）")
	}
	// 判据后面必须真的 return（而不是只判不拦）。
	if !strings.Contains(body[strings.Index(body, `GetConfig("outbound_enabled")`):], "return") {
		t.Fatal("A8 ① kill-switch 判据之后没有 return：判据成了摆设")
	}
	// 真值：GetConfig 读不到 ⇒ 空串 ⇒ 被拦（关）。
	blocks, parsed := a8GateBlocks(cond, "")
	if !parsed {
		t.Fatalf("A8 ① 条件串 %q 不是可判的字面比较：默认关无法机械验证", cond)
	}
	if !blocks {
		t.Fatalf("A8 ① outbound 默认开：GetConfig 读不到（空串）被当成放行（cond=%q）——翻缺省为 true 必红（M0305）", cond)
	}
	// ★ 反证（射程自证）：把缺省翻成「读不到即放行」的坏法喂同一个求值器，
	//   必须判成不拦；若求值器恒返回 true，这条反证立刻红 ⇒ 锁不是空转。
	badCond := `v == "1"` // 翻缺省：空串时 false ⇒ 不 return ⇒ 默认进发送路径
	badBlocks, badParsed := a8GateBlocks(badCond, "")
	if !badParsed || badBlocks {
		t.Fatalf("A8 ① 反证失明：坏条件 %q 没被判成「读不到即放行」，锁无法区分缺省被翻", badCond)
	}
	// ★ 「GetConfig 读不到＝空串」不是测试编的，钉住主库真实实现（§8.1 第 3 条同族）。
	sysCfg := a8ReadSource(t, filepath.Join("..", "store", "systemconfig.go"))
	if !strings.Contains(sysCfg, "if err == sql.ErrNoRows") || !strings.Contains(sysCfg, `v = ""`) {
		t.Fatal("A8 ① 主库 GetConfig 的「键不存在按空串处理」语义被改：读不到 ≠ 开 的前提不再成立")
	}
}

// TestA8SingleProductionSender ② runOutboundSequenceOnce 之外没有第二个生产发送调用方。
func TestA8SingleProductionSender(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("A8 ② 列 api 目录失败: %v", err)
	}
	total := 0
	outside := []string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatalf("A8 ② 读 %s 失败: %v", f, rerr)
		}
		src := string(b)
		n := strings.Count(src, "s.sendOne(")
		if n == 0 {
			continue
		}
		total += n
		runBody := a8FuncBody(src, "func (s *Server) runOutboundSequenceOnce(")
		if strings.Count(runBody, "s.sendOne(") != n {
			outside = append(outside, f)
		}
	}
	if total != 1 {
		t.Fatalf("A8 ② 生产发送调用方出现 %d 次（期望恰 1，只在 runOutboundSequenceOnce 内）：%v ——加一个调用方必红", total, outside)
	}
	if len(outside) > 0 {
		t.Fatalf("A8 ② 这些文件在拨轮之外另起了一个发送调用方: %v", outside)
	}
	// watchdog 槽恰一处、且没有 @startup 直接拨（沿用 A18 口径，但这里问的是「第二个调用方」）。
	wd := a8ReadSource(t, "watchdog.go")
	if n := strings.Count(wd, "s.runOutboundSequenceOnce"); n != 1 {
		t.Fatalf("A8 ② watchdog.go 里 runOutboundSequenceOnce 出现 %d 次（期望恰 1）：多处＝@startup 启动即跑复活", n)
	}
	// ★ 反证（射程自证）：把「多一个调用方」的坏样本喂同一个计数口径，必须数出 2。
	//   不改工作树，只证明计数判据对「加一处 s.sendOne(」有判别力。
	badSample := "func (s *Server) runOutboundSequenceOnce() {\n\ts.sendOne(ctx, 1, \"a\", \"n\", \"s\", \"b\")\n}\nfunc (s *Server) handleOutboundSequence(...) {\n\ts.sendOne(ctx, 1, \"a\", \"n\", \"s\", \"b\")\n}\n"
	if n := strings.Count(badSample, "s.sendOne("); n != 2 {
		t.Fatalf("A8 ② 反证失明：坏样本里两个调用方数成 %d（期望 2），计数判据是空转的", n)
	}
	if strings.Count(a8FuncBody(badSample, "func (s *Server) runOutboundSequenceOnce("), "s.sendOne(") == 2 {
		t.Fatal("A8 ② 反证失明：a8FuncBody 没把拨轮体与其他函数切开，「之外」这条腿测不到")
	}
}

// TestA8ExplicitOneOnly ③ 开关显式 "1" 才会进发送路径。
func TestA8ExplicitOneOnly(t *testing.T) {
	_, body := a8RunBody(t)
	cond, ok := a8ExtractGateCond(body)
	if !ok {
		t.Fatal("A8 ③ 抽不到 kill-switch 条件串：显式 1 判据失明")
	}
	// 真值表：只有 "1" 放行，其它（读不到 / 0 / true / yes / on / 2 / 带空格的 1）全拦。
	values := []string{"", "0", "false", "true", "yes", "on", "2", " 1 ", "1"}
	for _, v := range values {
		blocks, parsed := a8GateBlocks(cond, v)
		if !parsed {
			t.Fatalf("A8 ③ 条件串 %q 在 v=%q 时不可判：判据形态被改坏", cond, v)
		}
		if want := a8ExpectedBlocks(v); blocks != want {
			t.Fatalf("A8 ③ v=%q 的拦/放不符：blocks=%v want=%v（cond=%q）——开关必须显式 \"1\" 才进发送路径", v, blocks, want, cond)
		}
	}
	// kill-switch 的 return 必须排在唯一的 s.sendOne( 之前。
	iGate := strings.Index(body, `GetConfig("outbound_enabled")`)
	iSend := strings.Index(body, "s.sendOne(")
	if iGate < 0 || iSend < 0 || iGate > iSend {
		t.Fatalf("A8 ③ kill-switch(@%d) 必须先于发送调用(@%d)：开关成了摆设", iGate, iSend)
	}
	// ★ 反证：坏条件（翻缺省）在真值表上至少有一格与期望相反，证明「改生产判据」会红。
	badCond := `v == "1"`
	diff := 0
	for _, v := range values {
		badBlocks, parsed := a8GateBlocks(badCond, v)
		if !parsed {
			t.Fatal("A8 ③ 反证失明：坏条件不可判")
		}
		if badBlocks != a8ExpectedBlocks(v) {
			diff++
		}
	}
	if diff == 0 {
		t.Fatalf("A8 ③ 反证失明：坏条件 %q 与当前判据真值表完全相同，锁抓不到「翻缺省」", badCond)
	}
}
