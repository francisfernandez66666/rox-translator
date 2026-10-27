// ============ 本文件职责中文说明 ============
// 图片链补译失败**必须出声**的锁（工单 T20261027152356CMY 复盘 C 项）。
//
// 缺陷形态：`retryImageSegments` 补译失败时只构造 reason 字符串、**一行日志都不打**；
// 而文本链（orchestrator/text_lines.go）同一件事是打 WARN 的。后果是本单排障时
// 只能拿交付 .md 与 gate_warnings 反推"这一行到底是没回、回了空的、还是回了被我们判死的"——
// 三者在交付面上同形（都写"仍为空"），成因却完全不同，对应三种完全不同的修法。
//
// 四条腿（每条配正向对照，否则"没打日志"与"打了但没抓到"分不开）：
//
//	A 恒空上游 ⇒ 每行一条"仍不合格"WARN，且 empty=true（＝上游压根没给）；
//	B 恒吐拒译话术 ⇒ empty=false、usable_have=true（＝给了但被判不可用），与 A 分得开；
//	C 判上游断 ⇒ 汇总 WARN **只出现一次**（早停省的是调用，不是把"哪些行没翻"也省掉）；
//	D 救回来的行 ⇒ 一行 WARN 都不许有（日志噪音同样会淹掉真信号）。
//
// 本文件只起一个假上游（httptest，复用 image_retry_19_test.go 的 imgEngine/imgStub），
// 不连库、不跑真渲染脚本。
// ============================================
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// warnCapture 抓包期间的 slog 输出，提供按 msg 计数的读取口。
// 为什么用 sync.Mutex：补译梯内部有并发腿（降级模型/截断抬预算各打一枪），
// bytes.Buffer 本身不是并发安全的。
type warnCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write 实现 io.Writer，让 slog 的 JSON handler 直接写进本抓包器。
func (c *warnCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

// count 返回 msg 字段完全等于 want 的日志条数。
func (c *warnCapture) count(want string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if m, _ := rec["msg"].(string); m == want {
			n++
		}
	}
	return n
}

// lastKind 返回 msg 等于 want 的最后一条日志里的 last_kind 字段（诊断档位）。
func (c *warnCapture) lastKind(want string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	got := ""
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if m, _ := rec["msg"].(string); m == want {
			if k, _ := rec["last_kind"].(string); k != "" {
				got = k
			}
		}
	}
	return got
}

// has 是否出现过 msg 等于 want 的日志（用于"正向对照"——证明抓包本身没坏）。
func (c *warnCapture) has(want string) bool { return c.count(want) > 0 }

// captureSlog 把默认 slog 换成写进 c 的 JSON handler，返回还原函数。
func captureWarnSlog(t *testing.T) *warnCapture {
	t.Helper()
	c := &warnCapture{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return c
}

// 本文件要断言的两条日志文案（与 file.go 里的字面量逐字一致，改词要同步改这里）。
const (
	// warnLineBad 逐行补译失败的 WARN（与 file.go 里的字面量逐字一致，改词要同步改这里）。
	warnLineBad = "图片工单逐行补译仍不合格，按原文回落"
	// warnUpstreamDead 判上游断的汇总 WARN。
	warnUpstreamDead = "图片工单逐行补译判上游断，剩余未译行一次性按原文回填"
)

// TestImageRetryWarnsPerLineWhenUpstreamEmpty A 腿：恒空上游 ⇒ 每行一条 WARN。
func TestImageRetryWarnsPerLineWhenUpstreamEmpty(t *testing.T) {
	c := captureWarnSlog(t)
	// 三段是刻意的：LineDeadStreakMin 默认 3，只有到第 3 段才会判「上游断」。
	// 用两段跑这条腿会让汇总 WARN 永远不出现（第一版就踩了这个坑），那时"没抓到"会被误读成"没打"。
	texts := []string{"第一段说明。", "第二段说明。", "第三段说明。"}
	e, _ := imgEngine(t, func(user string) string { return "" })
	notes, _ := e.retryImageSegments(context.Background(), texts, map[string]string{}, "en")
	if len(notes) != len(texts) {
		t.Fatalf("前提失效：%d 段都该进清单，实得 %+v", len(texts), notes)
	}
	if got := c.count(warnLineBad); got != len(texts) {
		t.Errorf("每一段失败都要出声（排障时才知道是哪几行、什么成因），期望 %d 条，实得 %d", len(texts), got)
	}
	if !c.has(warnUpstreamDead) {
		t.Errorf("恒空上游必须触发「判上游断」汇总 WARN —— 这一行是值班判断「这单上游坏了」的唯一信号")
	}
	// 成因档必须是 empty：这一腿的上游连空串都没给，它与 B 腿的"给了但不可用"是两种修法。
	if k := c.lastKind(warnLineBad); k != "empty" {
		t.Errorf("上游恒空时 last_kind 必须是 empty，实得 %q", k)
	}
}

// TestImageRetryWarnDistinguishesEmptyFromUnusable B 腿：给了但被判不可用 ⇒ 与"没给"必须分得开。
func TestImageRetryWarnDistinguishesEmptyFromUnusable(t *testing.T) {
	c := captureWarnSlog(t)
	const src = "产品方案书"
	e, _ := imgEngine(t, func(user string) string {
		return "I am unable to provide the translation you requested" // 现网 M1 拒译话术
	})
	notes, _ := e.retryImageSegments(context.Background(), []string{src}, map[string]string{src: ""}, "en")
	if len(notes) != 1 {
		t.Fatalf("恒坏形态必须进清单，实得 %+v", notes)
	}
	if !c.has(warnLineBad) {
		t.Fatal("正向对照：失败一行都没出声 ⇒ 抓包坏了或 WARN 被删了")
	}
	// 这一档的读数必须与 A 腿分得开：上游**给了**东西（last_cand_len>0），
	// 只是被统一判据判死（last_kind≠empty）。两者混为一谈，下一位就会去改判据而不是去换模型。
	c.mu.Lock()
	raw := c.buf.String()
	c.mu.Unlock()
	var sawUnusable bool
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if m, _ := rec["msg"].(string); m != warnLineBad {
			continue
		}
		kind, _ := rec["last_kind"].(string)
		n, _ := rec["last_cand_len"].(float64)
		if kind != "" && kind != "empty" && n > 0 {
			sawUnusable = true
		}
	}
	if !sawUnusable {
		t.Errorf("「给了但不可用」的 WARN 必须 last_kind≠empty 且 last_cand_len>0（这是它与「上游没给」的唯一区别）\n抓到的原文：%s", raw)
	}
}

// TestImageRetryUpstreamDeadWarnOnce C 腿：早停汇总只打一次，不逐行刷屏。
func TestImageRetryUpstreamDeadWarnOnce(t *testing.T) {
	c := captureWarnSlog(t)
	texts := []string{"一。", "二。", "三。", "四。", "五。", "六。"}
	e, _ := imgEngine(t, func(user string) string { return "" })
	notes, _ := e.retryImageSegments(context.Background(), texts, map[string]string{}, "en")
	if len(notes) != len(texts) {
		t.Fatalf("前提失效：六段都该进清单，实得 %+v", notes)
	}
	if got := c.count(warnUpstreamDead); got != 1 {
		t.Errorf("「判上游断」是整语种事件，必须恰好一次（多了＝逐行刷屏淹掉真信号），实得 %d 次", got)
	}
	if got := c.count(warnLineBad); got == 0 {
		t.Errorf("逐行失败仍要逐条出声（早停只省调用，不省可见性），实得 0 条")
	}
}

// TestImageRetryNoWarnWhenSalvaged D 腿：救回来的行不许产生失败 WARN（日志噪音同样会淹掉真信号）。
func TestImageRetryNoWarnWhenSalvaged(t *testing.T) {
	c := captureWarnSlog(t)
	const src = "蓝牙钥匙已激活，请勿拆卸。"
	const good = "The Bluetooth key has been activated. Do not disassemble it."
	e, stub := imgEngine(t, func(user string) string {
		if strings.Contains(user, "<s1>") {
			return "<s1></s1>"
		}
		return good
	})
	tr := map[string]string{src: ""}
	notes, _ := e.retryImageSegments(context.Background(), []string{src}, tr, "en")
	if stub.calls() == 0 {
		t.Fatal("正向对照：补译一枪都没打 ⇒ 本腿是空转")
	}
	if len(notes) != 0 || tr[src] != good {
		t.Fatalf("前提失效：这一行本该被救回来，notes=%+v tr=%q", notes, tr[src])
	}
	if c.has(warnLineBad) || c.has(warnUpstreamDead) {
		t.Errorf("救回来的行不许打失败 WARN（否则正常单也被标成坏单）")
	}
}
