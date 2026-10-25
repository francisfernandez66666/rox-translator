// ============ 本文件职责中文说明 ============
// N-01（2026-10-24）：文化规则块进 prompt 的上限与相关性单测。
// 对应现网「41 字即时翻译实扣 103 积分 / 0.2KB 文件实扣 1,037 积分」——
// 根因是文化块无上限 + 逐段补漏重试，每次调用约 2 万 token。
// 本锁只判「渲染进 prompt 的文本」的上限与相关性；输出侧全量检查不在射程内。
// ★ 2026-10-25 对账补腿：修法第 2 条点名的「截断时写一行 WARN（总条数/实际条数/被截字符数）」，
// 原先只落在 Info 级、且没有 dropped_chars 读数——静默降级在默认采集里看不见，
// 故补 TestRenderCultureBlockTruncationWarnShape（级别 + 三件读数 + 算术自洽一起判）。
// ============================================
package engine

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestRenderCultureBlockCapAndRelevance(t *testing.T) {
	e := &Engine{} // St=nil ⇒ cultureLimit 走默认 2000/30/true
	var rules []cultureRule
	for i := 1; i <= 100; i++ {
		rules = append(rules, cultureRule{ID: int64(i), Kind: "style", Phrase: "规范条目" + strings.Repeat("字", i)})
	}
	// 让第 100 条与源文强相关（应排第一）
	rules = append(rules, cultureRule{ID: 101, Kind: "replace", Phrase: "极石", Replacement: "ROX"})
	text := e.renderCultureBlock(rules, "我有一辆极石汽车", "zh")
	if text == "" {
		t.Fatal("渲染结果为空：上限/相关性实现把整块吃掉了")
	}
	if !strings.Contains(text, "极石") {
		t.Fatal("源文命中的规则没有排在前面（相关性未生效）")
	}
	if got := len([]rune(text)); got > 2000+64 { // 头部说明 + 单条预算的少量余量
		t.Fatalf("渲染字符数 %d 超过上限（2000）+ 头部余量", got)
	}
	if n := strings.Count(text, "· "); n > 30 {
		t.Fatalf("渲染条数 %d 超过 max_items=30", n)
	}
}

func TestRenderCultureBlockDropsNeutralAndAutoSingleWords(t *testing.T) {
	e := &Engine{}
	rules := []cultureRule{
		{ID: 1, Kind: "forbidden", Source: "manual", Phrase: "must"},
		{ID: 2, Kind: "forbidden", Source: "llm", Phrase: "foobar"},
		{ID: 3, Kind: "forbidden", Source: "manual", Phrase: "sensitive phrase"},
	}
	text := e.renderCultureBlock(rules, "", "en")
	if strings.Contains(text, "must") || strings.Contains(text, "foobar") {
		t.Fatalf("日常词/自动采集单字仍进了 prompt：%q", text)
	}
	if !strings.Contains(text, "sensitive phrase") {
		t.Fatalf("真实多词避雷词被漏掉：%q", text)
	}
}

func TestRenderCultureBlockNoRules(t *testing.T) {
	e := &Engine{}
	if got := e.renderCultureBlock(nil, "x", "zh"); got != "" {
		t.Fatalf("无规则应返回空串，实得 %q", got)
	}
}

// cultureLogMu 只在本文件的日志捕获用例之间互斥：slog.Default() 是进程级单例，
// 换它期间别的用例若同时打日志会串进本用例的缓冲（表现为莫名多出一行）。
var cultureLogMu sync.Mutex

// captureSlogJSON 把 slog 默认 logger 换成内存 JSON 输出，返回缓冲与还原函数。
// 级别刻意压到 Debug：判据要能证明「这一条是 WARN 而不是 Info」，
// 按 Info 建 handler 会让两者都落进缓冲，等于没判级别。
func captureSlogJSON(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return buf, func() { slog.SetDefault(old) }
}

// truncationLogLines 从捕获缓冲里挑出 culture_block_truncated 那几条（其余日志不参与判定）。
func truncationLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("捕获到的日志行不是合法 JSON（说明不是走 observability 的 slog 链）：%q", line)
		}
		if m["msg"] == "culture_block_truncated" {
			out = append(out, m)
		}
	}
	return out
}

// TestRenderCultureBlockTruncationWarnShape 钉修法第 2 条的三件读数与日志级别。
// 取向：日志级别与读数齐全程度都是**对外排障契约**（同 AGENTS §一·13 的 reason 分档口径），
// 功能等价但级别退回 Info＝现网没人看得见，判红。
func TestRenderCultureBlockTruncationWarnShape(t *testing.T) {
	cultureLogMu.Lock()
	defer cultureLogMu.Unlock()

	buf, restore := captureSlogJSON(t)
	e := &Engine{} // St=nil ⇒ 默认上限 2000 字符 / 30 条
	var rules []cultureRule
	for i := 1; i <= 100; i++ {
		rules = append(rules, cultureRule{ID: int64(i), Kind: "style", Phrase: strings.Repeat("长", 40)})
	}
	text := e.renderCultureBlock(rules, "与任何规则都不相关的源文", "zh")
	if text == "" {
		t.Fatal("渲染结果为空，本用例的截断判据无对象")
	}
	restore() // 先还原 logger，后续断言失败时不该把进程 logger 留在内存缓冲上

	lines := truncationLogLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("截断只该出一行 WARN，实得 %d 行（缓冲全文：%q）", len(lines), buf.String())
	}
	got := lines[0]
	if lvl, _ := got["level"].(string); lvl != "WARN" {
		t.Errorf("截断日志级别必须是 WARN（静默降级要能进告警面），实得 %q", lvl)
	}
	num := func(k string) (float64, bool) {
		v, ok := got[k].(float64)
		return v, ok
	}
	for _, k := range []string{"total_items", "kept_items", "dropped_items", "dropped_chars", "max_chars", "max_items"} {
		if _, ok := num(k); !ok {
			t.Errorf("截断日志缺读数 %q（修法点名「总条数/实际条数/被截字符数」三件必须齐）：%v", k, got)
		}
	}
	total, _ := num("total_items")
	kept, _ := num("kept_items")
	dropped, _ := num("dropped_items")
	droppedChars, _ := num("dropped_chars")
	keptChars, _ := num("kept_chars")
	maxChars, _ := num("max_chars")
	maxItems, _ := num("max_items")
	if dropped <= 0 {
		t.Errorf("被截条数应大于 0，实得 %v", dropped)
	}
	if droppedChars <= 0 {
		t.Errorf("被截字符数应大于 0（少了这一件就看不出上限该抬多高），实得 %v", droppedChars)
	}
	if total != kept+dropped {
		t.Errorf("总条数必须等于保留+被截（否则读数不可信）：total=%v kept=%v dropped=%v", total, kept, dropped)
	}
	if kept > maxItems || keptChars > maxChars {
		t.Errorf("保留侧读数越过了上限本身（日志与实现不自洽）：kept=%v/%v kept_chars=%v/%v", kept, maxItems, keptChars, maxChars)
	}
}

// TestRenderCultureBlockNoTruncationStaysSilent 反向对照：没截断就不许出这一行。
// 少了这一条，上一锁会被「无条件打日志」的实现骗绿（同 AGENTS §三「正向判据不许落在
// 只有失败分支才会写的字面上」——这里反过来也成立：告警腿必须有静默对照）。
func TestRenderCultureBlockNoTruncationStaysSilent(t *testing.T) {
	cultureLogMu.Lock()
	defer cultureLogMu.Unlock()

	buf, restore := captureSlogJSON(t)
	e := &Engine{}
	rules := []cultureRule{{ID: 1, Kind: "style", Phrase: "只用一条规范"}, {ID: 2, Kind: "replace", Phrase: "极石", Replacement: "ROX"}}
	if got := e.renderCultureBlock(rules, "我有一辆极石汽车", "zh"); !strings.Contains(got, "极石") {
		t.Fatalf("正常渲染失败，反向对照无对象：%q", got)
	}
	restore()
	if lines := truncationLogLines(t, buf); len(lines) != 0 {
		t.Errorf("未截断却出了截断告警（告警面被噪音填满＝等于没有告警）：%v", lines)
	}
}
