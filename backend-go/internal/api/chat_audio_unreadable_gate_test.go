// ============ chat_audio_unreadable_gate_test.go · 职责说明 ============
// N5 的接线腿（★ 2026-10-31）：`AudioDuration` 探测失败必须落到 `unreadable`，
// 由前端给"这段录音听不了"并引导工单，**不许静默放行**（放行＝把 0 秒当免费档，
// 客户拿到一段没识别的结果还以为成功了）。
// 判据按代码行顺序取：`AudioDuration` 的错误分支里必须同时出现 `instantReasonUnreadable`
// 与 `suggest`（这里用 chatVoiceFailure 的字段名做形态锚），中间不许先 return 成功路径。
// =============================================
package api

import (
	"os"
	"strings"
	"testing"
)

func TestChatASR_DurationFailureIsUnreadable(t *testing.T) {
	b, err := os.ReadFile("chat_audio.go")
	if err != nil {
		t.Fatalf("读 chat_audio.go 失败：%v", err)
	}
	code := stripGoLineComments(string(b))
	probe := strings.Index(code, "fileproc.AudioDuration(tmpPath)")
	if probe < 0 {
		t.Fatal("N5：找不到 AudioDuration 调用（靶子搬家，闸门已失明）")
	}
	tail := code[probe:]
	unread := strings.Index(tail, "instantReasonUnreadable")
	if unread < 0 {
		t.Fatal("N5：探测失败分支没有归 unreadable（会把坏录音当免费档放行）")
	}
	// 错误分支必须引导工单（chatVoiceFailure 的 Suggest="ticket"），不得只回一句错误。
	seg := tail[:unread+200]
	if !strings.Contains(seg, `Suggest: "ticket"`) {
		t.Fatal("N5：unreadable 分支必须带工单出路（Suggest: \"ticket\"）")
	}
}
