// ============ auth_password_gate_test.go · 职责说明 ============
// IAM P0-9（2026-10-21）「强制改密服务端真拦」的机械锁。
//
// 旧形态只有前端弹窗：登录回 must_change_pwd=1，登录卡弹一个改密对话框，
// 用户点叉/刷新即可绕过。中间件把那条路径堵死：mcp=1 的 token 除认证域与健康检查外
// 一律 428。本文件锁三件事：
//
//	A. 行为：mcp=1 → 受保护接口 428 + code=PASSWORD_CHANGE_REQUIRED，且**不执行**业务 handler；
//	B. 放行面：认证域前缀（改密/登出/me/重登/找回）与 /api/health 放行；无 token / 普通 token /
//	   非 /api/ 路径一律放行（未登录交给各 handler 回 401，本闸不替它们分诊）；
//	C. 接线：中间件真的挂在 Handler() 的链上（★ 判据级断言代替不了守卫级——只测中间件本身，
//	   把它从链上摘掉仍会全绿；所以另有一条按源码取 Handler() 函数体的接线锁）。
//
// 反证：① 把 passwordGateAllowed 改成恒 true ⇒ A 红；② 把中间件从链上摘掉 ⇒ C 红（A 仍绿，
// 这正是需要 C 的理由）；③ 把 mcp 判定去掉 ⇒ A 红而 B 仍绿。
// 方言：本文件不起库（只构造 JWT 与直调中间件），不依赖 DB_DRIVER。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// gateTokenOf 用指定 must_change_pwd 标记签一条测试 token。
func gateTokenOf(t *testing.T, mustChange int) string {
	t.Helper()
	u := &store.User{
		ID:            91001,
		TenantID:      1,
		Username:      "gate_user",
		Role:          store.RoleUser,
		TokenVersion:  3,
		MustChangePwd: mustChange,
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}
	return tok
}

// gateCall 直调 withPasswordGate，返回 recorder 与「业务 handler 是否被调用」。
func gateCall(t *testing.T, s *Server, path, token string) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	r := httptest.NewRequest(http.MethodPost, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.withPasswordGate(next).ServeHTTP(w, r)
	return w, called
}

// A 段：待改密 token 打受保护接口 ⇒ 428 + 统一错误码，且业务 handler 一次都不执行。
func TestPasswordGate_BlocksProtectedAPI(t *testing.T) {
	s := &Server{}
	tok := gateTokenOf(t, 1)
	for _, path := range []string{"/api/translate", "/api/me/context", "/api/admin/users", "/api/billing/balance"} {
		w, called := gateCall(t, s, path, tok)
		if called {
			t.Fatalf("%s：待改密 token 不该进入业务 handler（否则弹窗一关就能用全部功能）", path)
		}
		if w.Code != http.StatusPreconditionRequired {
			t.Fatalf("%s：应回 428，实际 %d（body=%s）", path, w.Code, w.Body.String())
		}
		var out map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s：428 响应非 JSON: %s", path, w.Body.String())
		}
		if out["code"] != "PASSWORD_CHANGE_REQUIRED" {
			t.Errorf("%s：code 应为 PASSWORD_CHANGE_REQUIRED（前端按 code 弹全局改密窗），实际 %v", path, out["code"])
		}
		if msg, _ := out["message"].(string); !strings.Contains(msg, "修改密码") {
			t.Errorf("%s：文案应点明「先修改密码」，实际 %q", path, msg)
		}
	}
}

// B 段：放行面逐条点名（少放一条＝客户点了没反应；多放一条＝闸门形同虚设）。
func TestPasswordGate_AllowedSurfaces(t *testing.T) {
	s := &Server{}
	pending := gateTokenOf(t, 1)
	normal := gateTokenOf(t, 0)

	cases := []struct {
		name  string
		path  string
		token string
	}{
		{"认证域-改密", "/api/auth/change-password", pending},
		{"认证域-登出", "/api/auth/logout", pending},
		{"认证域-自身信息", "/api/auth/me", pending},
		{"认证域-重登", "/api/auth/login", pending},
		{"健康检查", "/api/health", pending},
		{"非 API 路径", "/pricing", pending},
		{"普通 token", "/api/translate", normal},
		{"无 token", "/api/translate", ""},
	}
	for _, tc := range cases {
		w, called := gateCall(t, s, tc.path, tc.token)
		if !called {
			t.Errorf("%s（%s）：应放行到业务层，实际被 428 拦下（body=%s）", tc.name, tc.path, w.Body.String())
		}
	}
}

// C 段：接线锁——withPasswordGate 必须真挂在 Handler() 的链上，且恰好一处。
// 只测 A/B 的形态有个死角：中间件实现完好、但被从链上摘掉时 A/B 全绿。
func TestPasswordGate_WiredIntoHandlerChain(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读取 server.go 失败（本锁按包内相对路径读源，cwd 应在 internal/api）: %v", err)
	}
	body := goFuncBody(t, string(src), "func (s *Server) Handler()")
	if n := strings.Count(body, "withPasswordGate("); n != 1 {
		t.Fatalf("Handler() 链上 withPasswordGate 出现 %d 次，期望恰 1 次（0=闸门没挂上，>1=重复包装）", n)
	}
	if !strings.Contains(body, "withPasswordGate(s.mux)") {
		t.Fatalf("withPasswordGate 必须直接包住 s.mux（它是覆盖全路由的唯一位置）；实际链：\n%s", body)
	}
}

// goFuncBody 按花括号配平从源码里切出一个函数体（含签名行）。
// 用途：源码级接线锁只该看那一个函数，不能拿整份 server.go 做子串判断
// （别的函数里出现同名调用会让判据假绿）。
func goFuncBody(t *testing.T, src, signaturePrefix string) string {
	t.Helper()
	i := strings.Index(src, signaturePrefix)
	if i < 0 {
		t.Fatalf("源码里找不到 %q（签名变了就同步改这条锁，别删断言）", signaturePrefix)
	}
	open := strings.Index(src[i:], "{")
	if open < 0 {
		t.Fatalf("%q 后找不到函数体起始花括号", signaturePrefix)
	}
	start := i + open
	depth := 0
	for k := start; k < len(src); k++ {
		switch src[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[i : k+1]
			}
		}
	}
	t.Fatalf("%q 的函数体花括号未配平（源码被截断？）", signaturePrefix)
	return ""
}
