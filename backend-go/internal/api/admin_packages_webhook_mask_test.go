// ============ admin_packages_webhook_mask_test.go · 职责说明 ============
// 2026-09-26 发布前 UAT 修复批 I-6：群机器人 Webhook 明文回显整改的读写两侧同批闸门。
//
// 缺陷（本轮核实新发现，见《缺陷核实与修复文档_20260926.md》批 I-6）：
// GET /api/admin/packages/settings 把 wecom／dingtalk／slack／teams 四条群机器人
// Incoming Webhook **完整地址明文回显**。这类 URL 本身就是凭证（企微/钉钉的 ?key=、
// Slack 的 /services/T…/B…/token、Teams 的 /webhookb2/…/IncomingWebhook/<hash>/<guid>），
// 拿到即可向企业群发任意消息——等价于密钥泄漏（截图进工单、代理进浏览器扩展、
// 任何一次 XSS 都能读走）。同仓别处（SMTP 密码、LLM key、支付渠道密钥、assist token）
// 都已掩码，说明这是本 handler 漏做而非全站口径。
//
// ★ 关键约束：**读侧掩码与写侧跳过必须同批在位**，只做一半都是事故——
//
//	只改读侧：超管「打开面板直接点保存」就把 http****4e5f 写回库，机器人集体失效；
//	只改写侧：明文依旧外泄。故本文件两条锁成对存在（范式照抄 billing_payconfig.go
//	的「收到掩码就跳过」）。
//
// 锁清单：
//
//	A) 读侧零明文 + 清单双向锁：四个键回显必含 ****、整份响应不得出现任一真实凭证片段；
//	   未配置键回空串（不得打码成 ****，否则「未配置」看着像「已配置」且下次保存被当成没改）；
//	   响应里所有 *webhook*url 键必须都在 webhookMaskKeys 清单内（新增键忘了登记即红灯）；
//	B) 写侧跳过掩码回提：原样 POST 回显值 → 库内真值一字不变；
//	C) 写侧正常通道不受伤：传真值即覆盖、传空串即清除、字段缺席（nil）即不动。
//
// 方言：自钉 SQLite 内存库（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestWebhookMask
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

// whProbeCreds 四条「看起来就像真凭证」的 webhook 地址（假值，专供断言明文不得外泄）。
var whProbeCreds = map[string]string{
	"wecom_webhook_url":    "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=8f2c1d3e-4a5b-4c6d-8e9f-0a1b2c3d4e5f",
	"dingtalk_webhook_url": "https://oapi.dingtalk.com/robot/send?access_token=abcdef0123456789abcdef0123456789",
	"slack_webhook_url":    "https://hooks.slack.com/services/T02ABCDE/B03FGHIJK4/LmNoPqRsTuVwXyZ012345678",
	"teams_webhook_url":    "https://company.webhook.office.com/webhookb2/1a2b3c4d-5e6f@7a8b9c/IncomingWebhook/0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d/9f8e7d6c",
}

// whProbeSecrets 每条地址里**一旦出现在响应里就算泄漏**的凭证子串
// （不用整串比对：掩码本身允许留首尾 4 字符，判据要落在凭证实体上）。
var whProbeSecrets = map[string]string{
	"wecom_webhook_url":    "8f2c1d3e-4a5b-4c6d-8e9f-0a1b2c3d4e5f",
	"dingtalk_webhook_url": "access_token=abcdef0123456789",
	"slack_webhook_url":    "T02ABCDE/B03FGHIJK4/LmNoPqRsTuVwXyZ",
	"teams_webhook_url":    "0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d",
}

// newWebhookMaskProbe 平台超管 + 内存 SQLite 的最小服务栈（配置口不碰租户表，Ten 留空）。
func newWebhookMaskProbe(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	super, err := st.CreateUser(0, "wh_super", "hash", "群机器人掩码超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st}, tok
}

// callPackageSettingsGet 直调 GET /api/admin/packages/settings，返回响应体字符串与解码结果。
func callPackageSettingsGet(t *testing.T, s *Server, tok string) (string, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/packages/settings", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleAdminPackageSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings 应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("GET settings 解码失败: %v (%s)", err, body)
	}
	return body, out
}

// callPackageSettingsSave 以 JSON 直调 POST /api/admin/packages/settings/save。
// 参数 patch 只带本用例关心的键（其余字段缺席＝nil＝不动，正是 handler 的语义）。
func callPackageSettingsSave(t *testing.T, s *Server, tok string, patch map[string]interface{}) {
	t.Helper()
	blob, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/packages/settings/save", bytes.NewReader(blob))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleAdminPackageSettingsSave(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("SAVE settings 应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
}

// seedWebhooks 把四条探针地址写进 system_config（返回值即库内真值）。
func seedWebhooks(t *testing.T, s *Server) {
	t.Helper()
	for k, v := range whProbeCreds {
		if err := s.Store.SetConfig(k, v); err != nil {
			t.Fatalf("预置 %s 失败: %v", k, err)
		}
	}
}

// TestWebhookMaskReadSideNeverLeaksPlaintext 锁 A：读侧掩码 + 清单双向锁。
func TestWebhookMaskReadSideNeverLeaksPlaintext(t *testing.T) {
	s, tok := newWebhookMaskProbe(t)
	seedWebhooks(t, s)
	body, out := callPackageSettingsGet(t, s, tok)

	for key, raw := range whProbeCreds {
		v, ok := out[key]
		if !ok {
			t.Fatalf("GET 应回显 %s（前端表单要有初始值），响应缺键", key)
		}
		got, _ := v.(string)
		if got == raw {
			t.Fatalf("%s 明文整串回显（URL 即凭证，等于把机器人密钥交给任何能读响应的面）: %q", key, got)
		}
		if !strings.Contains(got, "****") {
			t.Fatalf("%s 回显未掩码（缺 ****）: %q", key, got)
		}
		if secret := whProbeSecrets[key]; strings.Contains(body, secret) {
			t.Fatalf("%s 的凭证实体出现在响应里: %q", key, secret)
		}
		// 掩码要「看得出是哪条通道」：保留首尾各 4 字符，超管据此辨认配置
		if len(got) < 10 || got[:4] != raw[:4] || got[len(got)-4:] != raw[len(raw)-4:] {
			t.Fatalf("%s 掩码形态异常（期望 首4＋****＋尾4）：got=%q raw=%q", key, got, raw)
		}
	}
	// 清单双向锁：响应里凡是 *_webhook_url 键都必须在必须掩码的清单内
	//（新增通道键只加 GET 不加清单 → 明文外泄，这里红灯拦住「做了一半」）
	for k := range out {
		if strings.HasSuffix(k, "_webhook_url") && !webhookMaskKeys[k] {
			t.Errorf("响应含未登记进 webhookMaskKeys 的 webhook 键 %q：明文外泄风险，请加入清单", k)
		}
	}
	// 反向：清单里的键都得在响应里（漏回显会让前端把空当清除，下次保存真被抹掉）
	for k := range webhookMaskKeys {
		if _, ok := out[k]; !ok {
			t.Errorf("webhookMaskKeys 含 %q 但 GET 未回显该键", k)
		}
	}
	// 空值口径：未配置的 webhook 必须回空串而不是 "****"
	if v, ok := out["wecom_webhook_url"]; !ok || v.(string) == "" {
		t.Fatalf("已配置的键不得回空（前端会误判未配置）")
	}
	s2, tok2 := newWebhookMaskProbe(t)
	_, outEmpty := callPackageSettingsGet(t, s2, tok2)
	for k := range webhookMaskKeys {
		if got, _ := outEmpty[k].(string); got != "" {
			t.Errorf("未配置键 %s 应回空串，实得 %q（打码成 **** 会让未配置看着像已配置，且下次保存被当成「没改」永远写不进）", k, got)
		}
	}
}

// TestWebhookMaskSaveSkipsMaskedEcho 锁 B（★ 与读侧同批的配套守卫）：
// 超管「打开面板什么都不改、直接点保存」＝把掩码回显原样 POST 回来。
// 此时库内真值必须一字不变——否则机器人当场失效，且是「没人做错任何事」导致的失效，最难排查。
func TestWebhookMaskSaveSkipsMaskedEcho(t *testing.T) {
	s, tok := newWebhookMaskProbe(t)
	seedWebhooks(t, s)
	_, out := callPackageSettingsGet(t, s, tok)

	// 取 GET 回显的掩码串，原样回提（模拟前端「没改就直接存」）
	patch := map[string]interface{}{}
	for k := range webhookMaskKeys {
		masked, _ := out[k].(string)
		if !strings.Contains(masked, "****") {
			t.Fatalf("前置不成立：%s 的 GET 回显不含掩码", k)
		}
		patch[k] = masked
	}
	// 顺带夹带一个普通键，确认「跳过」不会把整次保存一起吞掉
	patch["email_verify_enabled"] = "1"
	callPackageSettingsSave(t, s, tok, patch)

	for k, raw := range whProbeCreds {
		got, err := s.Store.GetConfig(k)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", k, err)
		}
		if got != raw {
			t.Errorf("%s 被掩码回显覆盖了（真值丢失＝机器人失效）：got=%q want=%q", k, got, raw)
		}
	}
	if v, _ := s.Store.GetConfig("email_verify_enabled"); v != "1" {
		t.Fatalf("同批普通键应照常落库（守卫不得扩大成整单跳过）: %q", v)
	}
	// 二次等值：跳过写回后回显仍是同一掩码（防止「读到掩码再掩一次」变成 http************）
	_, out2 := callPackageSettingsGet(t, s, tok)
	for k := range webhookMaskKeys {
		first, _ := out[k].(string)
		second, _ := out2[k].(string)
		if first != second {
			t.Errorf("%s 二次回显漂移（%q → %q）：疑似对掩码串重复打码", k, first, second)
		}
	}
}

// TestWebhookMaskSaveAcceptsRealValuesAndClear 锁 C：真值覆盖、空串清除、字段缺席不动。
// 掩码守卫只能挡住「含 **** 的回提」，绝不能顺手把正常写入通道也堵死
// （空串＝显式清除是管理台「清空配置」按钮的唯一口径）。
func TestWebhookMaskSaveAcceptsRealValuesAndClear(t *testing.T) {
	s, tok := newWebhookMaskProbe(t)
	seedWebhooks(t, s)

	newSlack := "https://hooks.slack.com/services/T11ZZZZZ/B22YYYYYY/aBcDeFgHiJkLmNoPqRsTuVwX"
	callPackageSettingsSave(t, s, tok, map[string]interface{}{
		"slack_webhook_url":    newSlack, // 传真值 → 覆盖
		"dingtalk_webhook_url": "",       // 传空串 → 清除
		// teams/wecom 缺席 → 不动
	})
	if got, _ := s.Store.GetConfig("slack_webhook_url"); got != newSlack {
		t.Fatalf("新真值应覆盖成功: %q", got)
	}
	if got, _ := s.Store.GetConfig("dingtalk_webhook_url"); got != "" {
		t.Fatalf("空串应清除配置（不是被掩码守卫跳过）: %q", got)
	}
	if got, _ := s.Store.GetConfig("teams_webhook_url"); got != whProbeCreds["teams_webhook_url"] {
		t.Fatalf("未出现在请求里的键不得被动到: %q", got)
	}
	if got, _ := s.Store.GetConfig("wecom_webhook_url"); got != whProbeCreds["wecom_webhook_url"] {
		t.Fatalf("未出现在请求里的键不得被动到: %q", got)
	}
	// 清除后的键在 GET 侧回到「空串」形态（前端据此显示未配置）
	_, out := callPackageSettingsGet(t, s, tok)
	if v, _ := out["dingtalk_webhook_url"].(string); v != "" {
		t.Fatalf("清除后应回空串，实得 %q", v)
	}
	// 覆盖后的新值仍走掩码回显（等值锁：与 maskKey 口径一致）
	if v, _ := out["slack_webhook_url"].(string); v != maskKey(newSlack) {
		t.Fatalf("覆盖后回显应为掩码串，实得 %q（期望 %q）", v, maskKey(newSlack))
	}
}

// TestWebhookMaskHelperSemantics 纯函数锁：webhookSaveSkipped / maskWebhookValue 的边界。
// 尤其钉住「非清单键不受守卫影响」——守卫做太宽会把普通配置项的掩码值一起吞掉（静默丢改动），
// 做太窄就是本次事故的复发面。
func TestWebhookMaskHelperSemantics(t *testing.T) {
	if !webhookSaveSkipped("wecom_webhook_url", "http****4e5f") {
		t.Errorf("清单键 + 含掩码 = 应跳过写回")
	}
	if webhookSaveSkipped("wecom_webhook_url", "https://real.example/robot/send?key=abc") {
		t.Errorf("清单键 + 真值不得跳过")
	}
	if webhookSaveSkipped("wecom_webhook_url", "") {
		t.Errorf("空串＝显式清除，不得被当成掩码回提跳过")
	}
	if webhookSaveSkipped("email_verify_enabled", "****") {
		t.Errorf("非清单键不得受 webhook 守卫影响（否则普通配置项改动被静默丢弃）")
	}
	if maskWebhookValue("   ") != "" || maskWebhookValue("") != "" {
		t.Errorf("空白值应原样回空")
	}
	if got := maskWebhookValue(whProbeCreds["teams_webhook_url"]); !strings.Contains(got, "****") ||
		strings.Contains(got, whProbeSecrets["teams_webhook_url"]) {
		t.Errorf("长地址掩码失效: %q", got)
	}
	// 短值（≤8 字符，历史脏数据/手工填的半截串）整串打码，不得把原文挤进首尾
	if got := maskWebhookValue("abcdefg"); got != "****" {
		t.Errorf("短值应整串 ****，实得 %q", got)
	}
}
