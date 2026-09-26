// ============ stream_f53_test.go 职责中文说明 ============
// F-53（2026-09-26 〇-U 批 I-8）SSE error 帧载荷的回归断言——
// engineErrorPayload(errStr, reply) 是「引擎业务失败 → 前端可见文案」的唯一收口：
//   - 敏感词拒译（errStr == engine.CodeSensitiveBlocked）必须把**稳定码放进 error_code**、
//     把**人类话术放进 error**，绝不允许把裸键名当文案发出去（本轮 UAT 实测到的客户可见缺陷）；
//   - Reply 意外为空时回落码本身，不发空串（空 error 会让气泡空白，比露键名更难排查）；
//   - 非码类失败（「文件不存在或无法读取」这类本就是人话的 Error）只发 error、
//     **不硬造 error_code**——没有稳定码却下发码，前端与 SDK 会按不存在的码分支。
//   - 帧级锁：sseEvent 合并后 error_code 必须在同一帧里，防载荷与协议层脱节。
//
// 判据全部对纯函数取值，不起服务、不碰 DB，所以无方言风险。
//
// ========================================
package api

import (
	"encoding/json"
	"strings"
	"testing"

	"translator/internal/engine"
)

// TestEngineErrorPayloadSensitiveBlocked 敏感词分支：码进 error_code、话术进 error。
func TestEngineErrorPayloadSensitiveBlocked(t *testing.T) {
	const humanMsg = "该内容包含敏感信息，已停止翻译"
	got := engineErrorPayload(engine.CodeSensitiveBlocked, humanMsg)

	if got["error_code"] != engine.CodeSensitiveBlocked {
		t.Errorf("敏感词失败必须下发稳定码 error_code=%s，got %#v", engine.CodeSensitiveBlocked, got["error_code"])
	}
	if got["error"] != humanMsg {
		t.Errorf("error 必须是引擎那句人话（客户气泡正文），got %#v", got["error"])
	}
	// ★ 本缺陷的原形：error 里出现键名即回归。
	if e, _ := got["error"].(string); strings.Contains(e, "sensitive_blocked") {
		t.Errorf("error 不得回显裸错误码（旧写法把 res.Error 当文案发，客户看到键名），got %q", e)
	}
	if len(got) != 2 {
		t.Errorf("该分支载荷只应有 error/error_code 两键，got %#v", got)
	}
}

// TestEngineErrorPayloadSensitiveBlankReplyFallsBack Reply 为空时的回落：发码不发空串。
func TestEngineErrorPayloadSensitiveBlankReplyFallsBack(t *testing.T) {
	for _, reply := range []string{"", "   ", "\t\n"} {
		got := engineErrorPayload(engine.CodeSensitiveBlocked, reply)
		msg, _ := got["error"].(string)
		if strings.TrimSpace(msg) == "" {
			t.Errorf("reply=%q 时 error 不得为空串（空文案会渲染成空白气泡），got %#v", reply, got)
		}
		if got["error_code"] != engine.CodeSensitiveBlocked {
			t.Errorf("回落分支也必须带 error_code，got %#v", got["error_code"])
		}
	}
}

// TestEngineErrorPayloadPlainMessageKeepsNoFakeCode 非码类失败：只发文案，不硬造假码。
func TestEngineErrorPayloadPlainMessageKeepsNoFakeCode(t *testing.T) {
	for _, msg := range []string{"文件不存在或无法读取", "不支持的格式", "上游模型超时"} {
		got := engineErrorPayload(msg, "引擎顺手写的无关回复")
		if got["error"] != msg {
			t.Errorf("非码类失败 error 必须原样是 res.Error，got %#v", got["error"])
		}
		if _, ok := got["error_code"]; ok {
			t.Errorf("没有稳定码时不得下发 error_code（前端/SDK 会按不存在的码分支），got %#v", got)
		}
		if len(got) != 1 {
			t.Errorf("该分支载荷只应有 error 一键，got %#v", got)
		}
	}
}

// TestEngineErrorPayloadTrimmedCodeMatch 码比对走 TrimSpace：带空白的码也认得出（引擎侧拼接容错）。
func TestEngineErrorPayloadTrimmedCodeMatch(t *testing.T) {
	got := engineErrorPayload("  "+engine.CodeSensitiveBlocked+"  ", "有话术")
	if got["error_code"] != engine.CodeSensitiveBlocked {
		t.Errorf("码两侧带空白也应命中敏感词分支，got %#v", got)
	}
	if got["error"] != "有话术" {
		t.Errorf("命中分支后 error 应取话术，got %#v", got["error"])
	}
}

// TestEngineErrorPayloadFrameLock 帧级锁：error_code 必须和 error 落在同一个 SSE 帧。
// 拆帧或载荷被协议层丢弃都属于本缺陷族（前端只读当前帧的 error 字段）。
func TestEngineErrorPayloadFrameLock(t *testing.T) {
	frame := sseEvent("error", engineErrorPayload(engine.CodeSensitiveBlocked, "该内容包含敏感信息"))
	if !strings.HasPrefix(frame, "data: ") || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("SSE 帧形态不合法：%q", frame)
	}
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(frame, "data: "), "\n\n")), &ev); err != nil {
		t.Fatalf("帧体不是合法 JSON：%v（帧=%q）", err, frame)
	}
	if ev["type"] != "error" {
		t.Errorf("帧类型应为 error，got %#v", ev["type"])
	}
	if ev["error_code"] != engine.CodeSensitiveBlocked {
		t.Errorf("error_code 必须与 error 同帧下发，got %#v", ev)
	}
	if ev["error"] != "该内容包含敏感信息" {
		t.Errorf("帧内 error 应是人话文案，got %#v", ev["error"])
	}
	// 帧文本里出现键名 = 前端按文案渲染时露码，同族回归。
	if strings.Contains(frame, `"error":"sensitive_blocked"`) {
		t.Errorf("帧内 error 不得是裸码：%q", frame)
	}
}

// TestCodeSensitiveBlockedWireValueLocked 对外契约锁：敏感词码的**线上传输值**不许改。
// T36/T37（OpenAPI 断言）与 SDK 消费方按该字符串分支，engine.CodeSensitiveBlocked 常量
// 可以移动位置、可以改引用方式，但值一改即破坏兼容（F-53 的改法是补 error_code 这条腿，
// 不是重命名码值）。
func TestCodeSensitiveBlockedWireValueLocked(t *testing.T) {
	if engine.CodeSensitiveBlocked != "sensitive_blocked" {
		t.Fatalf("敏感词稳定码的线上值被改动：%q —— 这是对外契约，T36/T37 与 SDK 会同时失联", engine.CodeSensitiveBlocked)
	}
}
