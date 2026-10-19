// ============ health_capabilities_test.go · 职责说明 ============
// /api/health 的 OCR/ASR/TTS 就绪位出参单测（2026-10-14 P0 接入）。
// 口径同 TestHandleHealthAnydocReady：空 Server 下也要正常出参——就绪位全 false，
// 不因未初始化而缺字段。就绪位的**取值**随机器依赖而变，这里只锁「字段在且是布尔」。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleHealthCapabilityReadiness ★ 2026-10-14：/api/health 必须暴露
// ocr_ready / asr_ready / tts_ready 三个布尔字段（数据源 fileproc healthcheck 缓存，
// 含模型资产落盘判定）。部署验收与运维探测按这三个位判断「节点有没有启用识别/合成」。
func TestHandleHealthCapabilityReadiness(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	(&Server{}).handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("handleHealth 应 200，实际 %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("health 出参非法 JSON: %v", err)
	}
	for _, key := range []string{"ocr_ready", "asr_ready", "tts_ready"} {
		if _, ok := body[key].(bool); !ok {
			t.Errorf("%s 应为布尔字段，出参: %s", key, rec.Body.String())
		}
	}
	// ★ 2026-10-19 G12：asr_engine 是**字符串**字段（有效路由档；不就绪时为空串）。
	//   类型判据在这里锁"字段在且是字符串"，取值随机器权重而变（同上面三个布尔的口径）。
	if _, ok := body["asr_engine"].(string); !ok {
		t.Errorf("asr_engine 应为字符串字段，出参: %s", rec.Body.String())
	}
}
