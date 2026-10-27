// ============ 本文件职责中文说明 ============
// GeeTest v4 提供方单测：签名性质（含"键与消息不能反"）、token 形状、缺密钥、
// 以及用**只认协议的假极验**走一遍成功/拒绝两条路（假服务按官方口径重算签名，
// 缺任一字段或签名的 lot_number 对不上就回 reject——桩放水＝给缺陷发证件）。
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// setGeetestConfig 写入 provider 与站点/服务端密钥，并把校验端点指向假服务。
func setGeetestConfig(t *testing.T, s *Server, url string) {
	t.Helper()
	_ = s.Store.SetConfig("captcha_provider", "geetest")
	_ = s.Store.SetConfig("captcha_site_key", "captcha-id-abc")
	_ = s.Store.SetConfig("captcha_secret_key", "captcha-key-xyz")
	old := geetestValidateURL
	geetestValidateURL = url
	t.Cleanup(func() { geetestValidateURL = old })
}

// newGeetestFake 起一个只认官方口径的假极验：六字段齐且 sign_token 与"用密钥重新签 lot_number"等值才 success。
func newGeetestFake(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		need := []string{"captcha_id", "lot_number", "captcha_output", "pass_token", "gen_time", "sign_token"}
		for _, k := range need {
			if r.FormValue(k) == "" {
				w.Header().Set("Content-Type", "application/json")
				// 缺任一字段都不是 success：假服务**不许**放水（放水的桩会给缺陷发证件，见文件头那条纪律）
				_, _ = w.Write([]byte(`{"result":"fail","reason":"missing_field"}`))
				return
			}
		}
		want := geetestSign("captcha-key-xyz", r.FormValue("lot_number"))
		if !hmac.Equal([]byte(want), []byte(r.FormValue("sign_token"))) {
			_, _ = w.Write([]byte(`{"result":"reject","reason":"bad_sign"}`))
			return
		}
		_, _ = w.Write([]byte(`{"result":"success","reason":"","captcha_args":{}}`))
	}))
}

// TestGeetestSignProperties 钉签名的**形状**：hex 小写 64 位、随流水号变、
// 且"密钥当消息/消息当密钥"这种参数顺序颠倒必须产生不同结果（真实踩坑位）。
func TestGeetestSignProperties(t *testing.T) {
	key, lot := "captcha-key-xyz", "81c1ed8e35be4f67a00bf30b578e873a"
	got := geetestSign(key, lot)
	if len(got) != 64 {
		t.Fatalf("签名长度应为 sha256 hex 的 64，实得 %d", len(got))
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Fatalf("签名不是 hex: %v", err)
	}
	if got == geetestSign(key, "other-lot") {
		t.Fatal("签名必须随 lot_number 变")
	}
	if got == geetestSign(lot, key) {
		t.Fatal("键与消息不能互换：换过来必须不同（互换＝上线后所有人永远过不了验证）")
	}
	// 与一份独立实现（直接 here-doc 式重算）逐字节等值，挡住"改函数时顺手换了拼接顺序"
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(lot))
	if got != hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("签名与 HMAC-SHA256(key, lot_number) 不等值")
	}
}

// TestGeetestRejectsBadToken 非 JSON、缺字段、缺密钥三种脏形态都必须拒绝（不许静默放行）。
func TestGeetestRejectsBadToken(t *testing.T) {
	s := newCaptchaTestServer(t)
	setGeetestConfig(t, s, "http://127.0.0.1:1/none") // 这一档必须在校验端点之前就拒掉，不该真发请求
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	for _, tok := range []string{"not-json", `{"lot_number":"a"}`, `{"lot_number":"a","captcha_output":"b","pass_token":"c","gen_time":""}`} {
		if err := s.verifyCaptcha(r, tok); err == nil {
			t.Fatalf("脏 token 应拒绝: %q", tok)
		}
	}
}

// TestGeetestMissingKeys 有合法 token 形状但库里没配密钥 ⇒ 可读错误（不是 200 也不是 panic）。
func TestGeetestMissingKeys(t *testing.T) {
	s := newCaptchaTestServer(t)
	_ = s.Store.SetConfig("captcha_provider", "geetest")
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	if err := s.verifyCaptcha(r, `{"lot_number":"a","captcha_output":"b","pass_token":"c","gen_time":"d"}`); err == nil {
		t.Fatal("缺密钥应拒绝")
	}
}

// TestGeetestSuccessRoundTrip 走一遍真链路：前端四件套 JSON → 现算签名 → 假极验认下 → 放行。
func TestGeetestSuccessRoundTrip(t *testing.T) {
	s := newCaptchaTestServer(t)
	fake := newGeetestFake(t)
	defer fake.Close()
	setGeetestConfig(t, s, fake.URL)
	tok, err := json.Marshal(geetestTokenPayload{
		LotNumber: "81c1ed8e35be4f67a00bf30b578e873a", CaptchaOutput: "out-1",
		PassToken: "pass-1", GenTime: "1700000000",
	})
	if err != nil {
		t.Fatalf("构造 token 失败: %v", err)
	}
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	if err := s.verifyCaptcha(r, string(tok)); err != nil {
		t.Fatalf("假极验已认下的凭据应放行: %v", err)
	}
}

// TestGeetestRejectResult 假极验回 reject（签名对不上/风控）时必须拒——
// 这一条同时证明"桩真的看了 sign_token"：篡改签名后仍然放行＝桩放水。
func TestGeetestRejectResult(t *testing.T) {
	s := newCaptchaTestServer(t)
	fake := newGeetestFake(t)
	defer fake.Close()
	setGeetestConfig(t, s, fake.URL)
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	// 正常凭据先证"能过"，再把密钥换掉（等价于送错签名）证"必须不过"
	good := `{"lot_number":"L1","captcha_output":"o","pass_token":"p","gen_time":"1"}`
	if err := s.verifyCaptcha(r, good); err != nil {
		t.Fatalf("正向对照失败（桩或链路不对）: %v", err)
	}
	_ = s.Store.SetConfig("captcha_secret_key", "rotated-key")
	if err := s.verifyCaptcha(r, good); err == nil {
		t.Fatal("签名与密钥不匹配必须拒绝（这条红＝桩真的在验签名）")
	}
}

// TestCaptchaEnabledProviders provider 白名单是唯一判据：none/未知一律视为关闭。
func TestCaptchaEnabledProviders(t *testing.T) {
	s := newCaptchaTestServer(t)
	for _, tc := range []struct {
		p    string
		want bool
	}{
		{"", false}, {"none", false}, {"TURNSTILE", true}, {"geetest", true}, {"tianyu", false},
	} {
		_ = s.Store.SetConfig("captcha_provider", tc.p)
		if got := s.captchaEnabled(); got != tc.want {
			t.Fatalf("provider=%q 期望 enabled=%v，实得 %v", tc.p, tc.want, got)
		}
	}
}
