// ============ chat_audio_gate_single_test.go · 职责说明 ============
// 组4 收口锁（★ 2026-10-31）：TTS 这条腿**只能过一次闸**。
//
// 缺陷形态（已在现网埋着）：先无条件 `gateUsage` 拿到一个并发名额与释放闭包，
// 判成免费档后又覆盖成 `gateUsageInstant` —— 第一次的 release 被丢弃，
// 每朗读一次泄漏一个并发名额；攒到上限后全站朗读恒回「并发请求过多」，
// 日志里零异常（纯资源泄漏），只有压力测试或长期运行才现形。
//
// 判据：handleChatTTS 函数体里 gateUsageInstant 必须**先于** gateUsage 出现
// （即先判档、再只走一条分支），且两者都出现在 `if ttsRoute.Free` 的两支中。
// =============================================
package api

import (
	"os"
	"strings"
	"testing"
)

// ttsBody 取 handleChatTTS 的函数体（到下一个顶层 func 为止）。
func ttsBody(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("chat_audio.go")
	if err != nil {
		t.Fatalf("读 chat_audio.go 失败（静态锁必须以生产源码为靶子）: %v", err)
	}
	src := stripGoLineComments(string(b))
	start := strings.Index(src, "func (s *Server) handleChatTTS(")
	if start < 0 {
		t.Fatal("找不到 handleChatTTS（靶子搬家，闸门已失明）")
	}
	rest := src[start:]
	if end := strings.Index(rest[1:], "\nfunc "); end >= 0 {
		return rest[:end+1]
	}
	return rest
}

// TestChatTTS_SingleGate 先判档、再只走一支；不允许"先闸后覆盖"。
func TestChatTTS_SingleGate(t *testing.T) {
	body := ttsBody(t)
	free := strings.Index(body, "if ttsRoute.Free {")
	if free < 0 {
		t.Fatal("TTS 里没有 `if ttsRoute.Free {` 分档（判档结果被丢弃＝免费档照走付费闸）")
	}
	inst := strings.Index(body, "gateUsageInstant(r)")
	paid := strings.Index(body, "= s.gateUsage(r)")
	if inst < 0 || paid < 0 {
		t.Fatalf("两条闸门没有同时接线（instant=%d paid=%d）", inst, paid)
	}
	if inst < free || paid < free {
		t.Fatal("闸门调用出现在分档之前：先闸后覆盖会泄漏第一个 release（并发名额）")
	}
	if inst > paid {
		t.Fatal("免费闸必须在前（先判档），否则免费请求会先占一个付费名额再被覆盖")
	}
	// 反证：把旧形态（先 gateUsage 再覆盖）喂回同一个判据必须被抓到。
	old := "tid, release, gateErr := s.gateUsage(r)\nif ttsRoute.Free {\n tid, release, gateErr = s.gateUsageInstant(r)\n}"
	if !strings.Contains(old, "= s.gateUsage(r)") || !strings.Contains(old, "gateUsageInstant") {
		t.Fatal("反证模板不成立（判据会退化成空转）")
	}
	if strings.Index(old, "= s.gateUsage(r)") < strings.Index(old, "gateUsageInstant(r)") {
		return // 旧形态被判据抓住了：符合预期
	}
	t.Fatal("反证失败：旧形态没有被前置关系识别出来")
}
