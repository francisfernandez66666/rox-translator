// ============ chat_audio_test.go · 职责说明 ============
// P3 语音两件套（/api/chat/asr、/api/chat/tts）的 HTTP 层回归锁。
//
// 为什么**只测 HTTP 这一层**：这两个端点的工作量在 Python 子进程里（真跑 ASR/TTS 要几十秒
// CPU 加模型权重），单测不该依赖部署机有没有装资产；
// 音频的正确性由 fileproc/asr_p2_test.go（时间轴/解析）与 capabilities_p0_test.go
// （probe 协议与依赖清单等值）覆盖，这里守的是**接口契约本身**：
//
//	① 登录闸门：必须登录。否则一个匿名脚本就能按次烧本地算力——ASR/TTS 是本机 CPU，
//	   没有外部 API 那种天然限流；且与用户令「试用态不开放语音」一致
//	   （免登录的 /api/trial/translate 只有文本一条腿，碰不到这两个端点）。
//	② 方法闸门：GET 必须 405，别让「接口好像不存在」误导下一个人去别处找。
//
// =============================================
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pickChatAudioHandler 按路径取对应 handler（两条路由共用一个表格驱动，判据只有一份）。
func pickChatAudioHandler(s *Server, path string) http.HandlerFunc {
	if strings.HasSuffix(path, "/tts") {
		return s.handleChatTTS
	}
	return s.handleChatASR
}

// TestChatVoiceEndpointsRequireLogin ① 匿名访问两条语音端点必须 401。
func TestChatVoiceEndpointsRequireLogin(t *testing.T) {
	s := &Server{}
	for _, path := range []string{"/api/chat/asr", "/api/chat/tts"} {
		rec := httptest.NewRecorder()
		pickChatAudioHandler(s, path)(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader("")))
		if rec.Code != 401 {
			t.Fatalf("%s 匿名 POST 应返回 401，实际 %d（body=%s）", path, rec.Code, rec.Body.String())
		}
		// ② GET 明确 405
		rec2 := httptest.NewRecorder()
		pickChatAudioHandler(s, path)(rec2, httptest.NewRequest(http.MethodGet, path, nil))
		if rec2.Code != 405 {
			t.Fatalf("%s 用 GET 访问应返回 405，实际 %d", path, rec2.Code)
		}
	}
}
