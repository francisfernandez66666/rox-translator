// ============================================================================
// admin_assist_test.go — AI 助手管理台 Token 托管接口测试
// （★ 改造 1A 建立；★ 〇-LK 2026-09-22 按「模型配置」范式重做读取与保存语义后同步改写）
//
// 覆盖：
//
//	A) 未登录 / 非超管 → 403（凭据状态不外泄给租户管理员与普通用户）
//	B) ★ 超管 GET 只回掩码：响应体任何字段都不得出现 Token 明文（旧实现回明文，已修）
//	C) 优先级契约：env ASSIST_ADMIN_TOKEN 压过库内配置，且 env_overridden=true 供前端置灰
//	D) POST 保存 → 写库为 enc:v1: 密文（明文绝不落库），回新掩码
//	E) POST 留空 = 不修改（★ 语义变更：旧口径「空串即清除」已换成显式 clear=true）
//	F) POST 掩码回写 = 不修改（前端把掩码原样提交回来时不得把凭据写坏）
//	G) POST clear=true → 清除库内配置并回落 env
//	H) 解密失败（JWT_SECRET 轮换未同步重存）→ 报 none 而非静默返回空串
//	I) 未支持方法 → 405
//
// 使用内存 SQLite + 签发真实超管 JWT，走 handler 全链路。
// 推送链路（pushAssistAdminToken）一律把 ASSIST_BASE_URL 指到不可达端口：
// 单元测试不许去打开发者本机正在跑的 assist 实例，只验推送失败时主链路仍成功。
// ============================================================================
package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// doAssistToken 以指定 JWT 调用 /api/admin/assist/token。
// 参数：s=服务，method=HTTP 方法，token=登录 JWT（空=未登录），body=请求体（可空）。
// 返回：状态码与解析后的响应体。
func doAssistToken(t *testing.T, s *Server, method, token, body string) (int, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, "/api/admin/assist/token", rdr)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.handleAdminAssistToken(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestAssistTokenAuthScope 鉴权范围：未登录与非超管一律 403（Token 状态不外泄）。
func TestAssistTokenAuthScope(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	// 未登录
	if code, _ := doAssistToken(t, s, "GET", "", ""); code != 403 {
		t.Fatalf("未登录应 403，实际 %d", code)
	}
	// 普通用户（非超管）
	if code, _ := doAssistToken(t, s, "GET", tokens["u_co_a"], ""); code != 403 {
		t.Fatalf("普通用户应 403，实际 %d", code)
	}
	// 超管 → 200（此时库内无配置且 env 未设 → source=none、set=false）
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	code, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 {
		t.Fatalf("超管应 200，实际 %d %v", code, resp)
	}
	if resp["set"] != false || resp["source"] != "none" {
		t.Fatalf("未配置时应 none/false，实际 %v", resp)
	}
}

// TestAssistTokenGetNeverLeaksPlaintext ★ B：GET 只回掩码，明文不得出现在任何字段。
// 反例保护：旧实现把明文写进响应 token 字段（当时给 iframe 用），#34 之后已无消费方，
// 留着等于凭据进浏览器/代理日志/Playwright trace。
func TestAssistTokenGetNeverLeaksPlaintext(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	const plain = "db-secret-token-abcdef"
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret(plain)); err != nil {
		t.Fatalf("写入密文失败: %v", err)
	}
	// 库内明文绝不出现：原始值必须是密文前缀
	raw, _ := s.Store.GetConfig(assistAdminTokenKey)
	if !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("库内应为 enc:v1: 密文，实际 %q", raw)
	}
	code, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 || resp["source"] != "db" || resp["set"] != true {
		t.Fatalf("应从库内解密并标记已配置，实际 %d %v", code, resp)
	}
	if resp["token"] != nil {
		t.Fatalf("响应不得再带 token 字段，实际 %v", resp["token"])
	}
	if got, _ := resp["masked"].(string); got != maskKey(plain) || strings.Contains(got, plain) {
		t.Fatalf("masked 应为掩码且不含明文，实际 %q", got)
	}
	if got, _ := resp["db_masked"].(string); got != maskKey(plain) {
		t.Fatalf("db_masked 应为库内值掩码，实际 %q", got)
	}
}

// TestAssistTokenEnvPriority ★ C：env 压过库内配置，并如实回 env_overridden。
func TestAssistTokenEnvPriority(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("db-secret-token")); err != nil {
		t.Fatalf("写入密文失败: %v", err)
	}
	t.Setenv("ASSIST_ADMIN_TOKEN", "env-secret-token-0000")
	_, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if resp["source"] != "env" {
		t.Fatalf("env 应压过库内配置，实际 %v", resp)
	}
	if resp["env_overridden"] != true {
		t.Fatalf("env 占用生效位时 env_overridden 必须为 true，实际 %v", resp)
	}
	if m, _ := resp["masked"].(string); m != maskKey("env-secret-token-0000") {
		t.Fatalf("masked 应取生效值（env）掩码，实际 %q", m)
	}
	// 库内掩码仍单独回显：否则前端无法解释「我保存的值去哪了」
	if dm, _ := resp["db_masked"].(string); dm != maskKey("db-secret-token") {
		t.Fatalf("db_masked 应回库内值掩码，实际 %q", dm)
	}
}

// TestAssistTokenSaveRotateAndPushFail ★ D+I：保存写密文、回新掩码；推送不通不影响保存成功。
func TestAssistTokenSaveRotateAndPushFail(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	// 推送目标指向不可达端口：测试期绝不连开发者本机可能正在监听的 assist 实例
	t.Setenv("ASSIST_BASE_URL", "http://127.0.0.1:1")

	code, resp := doAssistToken(t, s, "POST", tokens["admin"], `{"token":"rotated-token-1"}`)
	if code != 200 || resp["success"] != true || resp["changed"] != true {
		t.Fatalf("保存应成功且标记 changed，实际 %d %v", code, resp)
	}
	raw, _ := s.Store.GetConfig(assistAdminTokenKey)
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "rotated-token-1") {
		t.Fatalf("保存后库内应为密文且不含明文，实际 %q", raw)
	}
	if resp["source"] != "db" || resp["set"] != true {
		t.Fatalf("保存后应即刻以库内值生效，实际 %v", resp)
	}
	if resp["pushed"] != false {
		t.Fatalf("上游不可达时 pushed 应为 false（主库仍是事实源），实际 %v", resp)
	}
	if m, _ := resp["masked"].(string); m == "" || m == "rotated-token-1" {
		t.Fatalf("保存响应应只回掩码，实际 %q", m)
	}
}

// TestAssistTokenEmptyMeansUnchanged ★ E：留空=不修改（旧「空串即清除」语义必须已消失）。
func TestAssistTokenEmptyMeansUnchanged(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	t.Setenv("ASSIST_BASE_URL", "http://127.0.0.1:1")
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("keep-me-token")); err != nil {
		t.Fatalf("写入密文失败: %v", err)
	}
	_, resp := doAssistToken(t, s, "POST", tokens["admin"], `{"token":"   "}`)
	if resp["changed"] != false || resp["success"] != true {
		t.Fatalf("留空提交应视为不修改，实际 %v", resp)
	}
	raw, _ := s.Store.GetConfig(assistAdminTokenKey)
	if store.DecryptSecret(raw) != "keep-me-token" {
		t.Fatalf("留空提交不得动库内配置，实际 %q", raw)
	}
}

// TestAssistTokenMaskedWritebackRejected ★ F：掩码回写拦截（把掩码当新值提交不得写坏凭据）。
func TestAssistTokenMaskedWritebackRejected(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	t.Setenv("ASSIST_BASE_URL", "http://127.0.0.1:1")
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("original-token-9")); err != nil {
		t.Fatalf("写入密文失败: %v", err)
	}
	masked := maskKey("original-token-9")
	_, resp := doAssistToken(t, s, "POST", tokens["admin"], `{"token":"`+masked+`"}`)
	if resp["changed"] != false {
		t.Fatalf("掩码值应视为未修改，实际 %v", resp)
	}
	raw, _ := s.Store.GetConfig(assistAdminTokenKey)
	if store.DecryptSecret(raw) != "original-token-9" {
		t.Fatalf("掩码绝不能写回库，实际 %q", raw)
	}
}

// TestAssistTokenExplicitClear ★ G：显式 clear=true 才清除库内配置并回落 env。
func TestAssistTokenExplicitClear(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	t.Setenv("ASSIST_BASE_URL", "http://127.0.0.1:1")
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("to-be-cleared")); err != nil {
		t.Fatalf("写入密文失败: %v", err)
	}
	t.Setenv("ASSIST_ADMIN_TOKEN", "env-after-clear")
	code, resp := doAssistToken(t, s, "POST", tokens["admin"], `{"clear":true}`)
	if code != 200 || resp["changed"] != true || resp["source"] != "env" {
		t.Fatalf("清除后应回落 env，实际 %d %v", code, resp)
	}
	_, resp = doAssistToken(t, s, "GET", tokens["admin"], "")
	if resp["source"] != "env" || resp["set"] != true {
		t.Fatalf("清除后应下发 env 生效态，实际 %v", resp)
	}
	if dm, _ := resp["db_masked"].(string); dm != "" {
		t.Fatalf("清除后库内掩码应为空，实际 %q", dm)
	}
}

// TestAssistTokenUndecryptable ★ H：库里是解不开的密文（JWT_SECRET 轮换）→ 明确报 none。
// 反例保护：旧实现若直接返回密文原串，前端会拿着坏凭据反复重试并困惑「Token 不匹配」。
func TestAssistTokenUndecryptable(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	// 构造不可解密文：合法前缀 + 非法 base64 体
	if err := s.Store.SetConfig(assistAdminTokenKey, "enc:v1:!!!not-base64!!!"); err != nil {
		t.Fatalf("写入损坏密文失败: %v", err)
	}
	code, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 {
		t.Fatalf("应 200（不报错，只是无可用 Token），实际 %d", code)
	}
	if resp["set"] != false || resp["source"] != "none" {
		t.Fatalf("不可解密文应报 none/false，实际 %v", resp)
	}
}

// TestAssistTokenMethodNotAllowed ★ I：DELETE 等未支持方法应 405。
func TestAssistTokenMethodNotAllowed(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	if code, _ := doAssistToken(t, s, "DELETE", tokens["admin"], ""); code != 405 {
		t.Fatalf("DELETE 应 405，实际 %d", code)
	}
}
