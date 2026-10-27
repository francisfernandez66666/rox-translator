// ============ captcha_geetest.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// GeeTest v4（极验）人机验证的服务端二次校验，captcha_provider=geetest 时启用。
// 口径（对齐官方"服务端部署"文档，2026-10-27 复核）：
//   - 前端验证成功后拿到**四个字段**：lot_number / captcha_output / pass_token / gen_time；
//     本仓把它们**原样序列化成一段 JSON 当作 token**送进来，因此 verifyCaptcha 的入参形状不变、
//     四个业务调用点零改动（这是"多提供方并存不扩接口"的关键一条）。
//   - 签名 sign_token = HMAC-SHA256(key=captcha_key, msg=lot_number) 的 hex；
//     **只签流水号**，不签其余字段——签错消息＝永远判失败，且症状是"所有人过不了验证"。
//   - 校验请求 form 参数六个：captcha_id / lot_number / captcha_output / pass_token / gen_time / sign_token；
//     响应只认 {"result":"success"}，其余（reject/fail/解析失败/网络失败）**一律拒绝**，
//     与 turnstile 那一条同档（宁可拒一次请求，不许把"没验过"写成"验过了"）。
// 测试面：geetestValidateURL 是 var 而不是 const，单测把它指向 httptest 假服务
//   ——假服务只认协议（缺 sign_token 或非 success 一律回 reject），否则"按我以为的契约实现桩"
//   会给缺陷发证件（AGENTS §三 那条真踩过）。
// =============================================

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// geetestValidateURL 极验二次校验端点（可在单测里改指假服务；生产不配 env，写死这一处）。
var geetestValidateURL = "https://gcaptcha4.geetest.com/validate"

// geetestTokenPayload 前端上传的 token 形状（JSON）；字段名与极验 getValidate() 逐字一致，
// 故意不做大小写兼容或别名——别名的代价是"两种写法都收"，最后没人知道线上在送哪一种。
type geetestTokenPayload struct {
	LotNumber     string `json:"lot_number"`
	CaptchaOutput string `json:"captcha_output"`
	PassToken     string `json:"pass_token"`
	GenTime       string `json:"gen_time"`
}

// geetestSign 计算 sign_token：HMAC-SHA256(captcha_key, lot_number) 的 hex 小写。
func geetestSign(captchaKey, lotNumber string) string {
	mac := hmac.New(sha256.New, []byte(captchaKey))
	mac.Write([]byte(lotNumber))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyGeetest 校验极验 token：解析四件套 → 现算签名 → 打极验端点 → 只认 result=success。
// 参数 r: HTTP 请求（只为拿不到密钥/字段时报可读错误，不用于 remoteip）；token: 前端送来的 JSON。
func (s *Server) verifyGeetest(r *http.Request, token string) error {
	var p geetestTokenPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(token)), &p); err != nil {
		return &apiErr{"人机验证凭据格式不正确，请重新验证"}
	}
	if p.LotNumber == "" || p.CaptchaOutput == "" || p.PassToken == "" || p.GenTime == "" {
		return &apiErr{"人机验证凭据不完整，请重新验证"}
	}
	captchaID, _ := s.Store.GetConfig("captcha_site_key")
	captchaKey, _ := s.Store.GetConfig("captcha_secret_key")
	if strings.TrimSpace(captchaID) == "" || strings.TrimSpace(captchaKey) == "" {
		return &apiErr{"人机验证未配置密钥，请联系管理员"}
	}
	form := url.Values{
		"captcha_id":     {strings.TrimSpace(captchaID)},
		"lot_number":     {p.LotNumber},
		"captcha_output": {p.CaptchaOutput},
		"pass_token":     {p.PassToken},
		"gen_time":       {p.GenTime},
		"sign_token":     {geetestSign(captchaKey, p.LotNumber)},
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.PostForm(geetestValidateURL, form)
	if err != nil {
		return &apiErr{"人机验证服务不可用，请稍后再试"}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return &apiErr{"人机验证服务不可用，请稍后再试"}
	}
	var vr struct {
		Result string `json:"result"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &vr); err != nil || !strings.EqualFold(vr.Result, "success") {
		return &apiErr{"人机验证失败，请重试"}
	}
	return nil
}
