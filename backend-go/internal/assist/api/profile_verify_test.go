// ============================================================================
// profile_verify_test.go — assist 侧画像验签与降级（★ 2026-10-30 组7 §8.2，G30）
//
// 锁住：
//
//	A) 正确签名 + 回环对端 + 窗口内 ⇒ 画像生效；
//	B) 错签 / 过期 / 非回环 / 缺头 ⇒ 一律匿名；
//	C) 错签**不返 4xx**：挂件照常拿到 200 回答（P-5：身份腿坏了不许把挂件打死）。
//
// ============================================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"translator/internal/assist/engine"
	"translator/internal/assist/llm"
	"translator/internal/assist/profile"
	assiststore "translator/internal/assist/store"
)

// newProfileVerifyServer 起一个最小 assist 服务（临时库 + 无 LLM 引擎）。
func newProfileVerifyServer(t *testing.T) *Server {
	t.Helper()
	db, err := assiststore.Open(t.TempDir() + "/a.db")
	if err != nil {
		t.Fatalf("open assist db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewServer(db, engine.New(db, llm.New(nil, 5)), "", "")
}

// signedHeaders 用给定密钥/时间戳生成本进程口径的三个头。
func signedHeaders(t *testing.T, key string, v *profile.Visitor, ts int64) map[string]string {
	t.Helper()
	payload, err := profile.Encode(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return map[string]string{
		profile.HeaderProfile: payload,
		profile.HeaderTs:      strconv.FormatInt(ts, 10),
		profile.HeaderSig:     profile.Sign([]byte(key), payload, ts),
	}
}

func withHeaders(h map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/assist/chat", nil)
	r.RemoteAddr = "127.0.0.1:43210" // 回环对端（现网 Caddy 与本服务同机）
	for k, val := range h {
		r.Header.Set(k, val)
	}
	return r
}

func TestVisitorProfileAccepted(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "verify-key")
	s := newProfileVerifyServer(t)
	v := &profile.Visitor{UID: 77, DisplayName: "张三", Email: "z***@example.com", PointsBalance: 320}
	r := withHeaders(signedHeaders(t, "verify-key", v, time.Now().Unix()))
	got := s.visitorProfileFromRequest(r)
	if got == nil || got.UID != 77 || got.DisplayName != "张三" {
		t.Fatalf("正确签名应生效，实际 %+v", got)
	}
}

func TestVisitorProfileDegradesToAnonymous(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "verify-key")
	s := newProfileVerifyServer(t)
	v := &profile.Visitor{UID: 77, DisplayName: "张三"}
	now := time.Now().Unix()

	cases := []struct {
		name string
		hdr  map[string]string
		peer string
	}{
		{"错签", signedHeaders(t, "wrong-key", v, now), "127.0.0.1:1"},
		{"过期", signedHeaders(t, "verify-key", v, now-120), "127.0.0.1:1"},
		{"非回环", signedHeaders(t, "verify-key", v, now), "10.9.8.7:5555"},
		{"缺头", nil, "127.0.0.1:1"},
	}
	for _, c := range cases {
		r := withHeaders(c.hdr)
		r.RemoteAddr = c.peer
		if got := s.visitorProfileFromRequest(r); got != nil {
			t.Fatalf("%s：必须回落匿名，实际 %+v", c.name, got)
		}
	}
}

// TestBadSignatureDoesNotBreakChat C)：错签不得把挂件打成 4xx。
func TestBadSignatureDoesNotBreakChat(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "verify-key")
	s := newProfileVerifyServer(t)

	// 先 greet 拿 sid+tok（与真实挂件同一路径）。
	w := httptest.NewRecorder()
	s.handleGreeting(w, httptest.NewRequest(http.MethodGet, "/api/assist/greeting?page=/", nil))
	if w.Code != 200 {
		t.Fatalf("greeting 应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var g map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatalf("greeting 响应非 JSON: %v", err)
	}
	sid, _ := g["session"].(string)
	tok, _ := g["tok"].(string)
	if sid == "" || tok == "" {
		t.Fatalf("greeting 未下发 session/tok: %s", w.Body.String())
	}

	body := `{"session":"` + sid + `","tok":"` + tok + `","message":"你好"}`
	r := httptest.NewRequest(http.MethodPost, "/api/assist/chat", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:43210"
	for k, val := range signedHeaders(t, "bad-key", &profile.Visitor{UID: 1}, time.Now().Unix()) {
		r.Header.Set(k, val)
	}
	w2 := httptest.NewRecorder()
	s.handleChat(w2, r)
	if w2.Code != 200 {
		t.Fatalf("错签必须按匿名照常回答（P-5：不返 4xx），实际 %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), `"reply"`) {
		t.Fatalf("错签时仍应有正常回复体，实际 %s", w2.Body.String())
	}
}

// TestProfileKeyMaskedIsRejected 掩码串不是密钥（管理台回显形态），必须当作未配置。
func TestProfileKeyMaskedIsRejected(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "")
	s := newProfileVerifyServer(t)
	if err := s.db.SetConfig(assistProfileKeyConfig, "ab****cd"); err != nil {
		t.Fatalf("set config: %v", err)
	}
	if k := s.profileKey(); len(k) != 0 {
		t.Fatalf("掩码串不得当密钥用，实际 %q", k)
	}
}
