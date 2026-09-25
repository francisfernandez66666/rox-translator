// ============================================================================
// api/auth_resetpassword_f20_test.go — 观察2②（2026-09-25 UAT 修复批G）后端半断言
// handleResetPassword 四类失败族（用户不存在 / 验证码缺失或已过期 / 错次超限作废 /
// 一次性码失配）已从「HTTP 200 + success:false」内联 writeJSON 收口为
// s.writeError(ErrValidation) → **HTTP 400 + 统一错误码 VALIDATION_ERROR**。
// 本测锁两层：
//
//	① 错码→400+code 双锁：每个失败族 HTTP 状态恰为 400，且响应体含
//	  success:false、code:"VALIDATION_ERROR"、message 逐字等于原文案
//	  （文案是 i18n catalog 的匹配锚点，改一个字都会红灯——这里等值钉死）；
//	② 正向防枚举锁：不存在的账号走 forgot（发码）链路仍回 200 + success:true
//	  + 恒真文案——失败收口 400 不得把枚举 oracle 从 reset 侧漏回 forgot 侧。
//
// 方言：自钉内存 SQLite（AGENTS.md §一·4，经 pinSqliteDialect 统一钉法）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestUATBatchG_'
// ============================================================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"database/sql"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
)

// f20ResetServer 装配重置密码链路最小服务器（Store + 注册护栏；forgot/reset 只用到这两件）。
// 内存库用命名共享缓存（同 f17 的口径）：防并发嵌套查询打到独立空连接。
func f20ResetServer(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", "file:f20reset?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return &Server{Store: st, Cfg: config.C, regGuard: newRegisterGuard(st)}
}

// f20ClearCode 双端清干净某用户的重置码（本地 map + vcode 降级存储），
// 保证每个失败族用例从「无码」初态出发，不串别的用例留下的状态。
func f20ClearCode(uid int64) {
	resetCodes.Lock()
	delete(resetCodes.m, uid)
	resetCodes.Unlock()
	vcodeDel(vcodeResetKey(uid))
}

// f20SeedCode 以指定 resetCode 状态预置某用户的重置码（经 vcodeSet 写入降级存储，
// 与 handleResetPassword 的「Redis 优先读」路径同键，无需再动本地 map）。
func f20SeedCode(t *testing.T, uid int64, rc resetCode) {
	t.Helper()
	b, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("序列化预置验证码失败: %v", err)
	}
	vcodeSet(vcodeResetKey(uid), b, 10*time.Minute)
	t.Cleanup(func() { f20ClearCode(uid) })
}

// f20CallReset 直调 handleResetPassword，返回录得的响应。
func f20CallReset(t *testing.T, s *Server, username, code, newPwd string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"username": username, "code": code, "new_password": newPwd})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleResetPassword(w, r)
	return w
}

// f20AssertValidation400 等值锁：状态码恰 400 + success:false + code:VALIDATION_ERROR + 文案逐字相等。
func f20AssertValidation400(t *testing.T, w *httptest.ResponseRecorder, wantMsg, scene string) {
	t.Helper()
	if w.Code != 400 {
		t.Fatalf("%s：应回 HTTP 400，实际 %d（body=%s）", scene, w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s：错误响应非 JSON: %s", scene, w.Body.String())
	}
	if out["success"] != false {
		t.Fatalf("%s：success 应为 false，实际 %v", scene, out["success"])
	}
	if out["code"] != "VALIDATION_ERROR" {
		t.Fatalf("%s：应带统一错误码 VALIDATION_ERROR，实际 %v", scene, out["code"])
	}
	if out["message"] != wantMsg {
		t.Fatalf("%s：文案必须逐字保留（i18n catalog 锚点），期望 %q 实际 %q", scene, wantMsg, out["message"])
	}
}

// TestUATBatchG_ResetPasswordFailFamily400 四类失败族的「400 + code + 原文案」三锁。
func TestUATBatchG_ResetPasswordFailFamily400(t *testing.T) {
	s := f20ResetServer(t)
	u, err := s.Store.CreateUser(1, "f20_reset_user", auth.PasswordHash("pw123456"), "批G重置账号", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("预置用户失败: %v", err)
	}

	// 失败族①：用户名查无此人（跨租户匹配 0 条）→ 与验证码错误同文案，防枚举语义不变
	f20ClearCode(u.ID)
	f20AssertValidation400(t, f20CallReset(t, s, "f20_ghost_not_exists", "123456", "newpass123"),
		"验证码错误或已过期", "失败族①用户不存在")

	// 失败族①补锁：真实用户但从未发码（同样落 u!=nil→无码路径的「验证码缺失」形态）
	f20AssertValidation400(t, f20CallReset(t, s, "f20_reset_user", "123456", "newpass123"),
		"验证码错误或已过期", "失败族①b无码用户")

	// 失败族②：验证码已过期（ExpiresAt 在过去）
	f20SeedCode(t, u.ID, resetCode{Code: "654321", ExpiresAt: time.Now().Add(-time.Minute)})
	f20AssertValidation400(t, f20CallReset(t, s, "f20_reset_user", "654321", "newpass123"),
		"验证码错误或已过期", "失败族②已过期")

	// 失败族③：错误尝试超限作废（Attempts ≥ resetCodeMaxTries，即使答对也不放行）
	f20SeedCode(t, u.ID, resetCode{Code: "111222", ExpiresAt: time.Now().Add(10 * time.Minute), Attempts: resetCodeMaxTries})
	f20AssertValidation400(t, f20CallReset(t, s, "f20_reset_user", "111222", "newpass123"),
		"验证码错误次数过多，请重新获取", "失败族③超限作废")
	// 超限分支应已销毁该码：双端（降级存储 + 本地 map）都不应再残留
	if _, ok := vcodeGet(vcodeResetKey(u.ID)); ok {
		t.Fatalf("失败族③：超限作废后重置码应被删除，降级存储仍可读回")
	}
	resetCodes.Lock()
	_, mapLeak := resetCodes.m[u.ID]
	resetCodes.Unlock()
	if mapLeak {
		t.Fatalf("失败族③：超限作废后本地 map 仍残留该用户条目")
	}

	// 失败族④：一次性码失配（正确码在场、提交错码）→ 400 + 错计 +1 的旧语义不回退
	f20SeedCode(t, u.ID, resetCode{Code: "334455", ExpiresAt: time.Now().Add(10 * time.Minute)})
	f20AssertValidation400(t, f20CallReset(t, s, "f20_reset_user", "000000", "newpass123"),
		"验证码错误或已过期", "失败族④码失配")
	if b, ok := vcodeGet(vcodeResetKey(u.ID)); ok {
		var rc resetCode
		if json.Unmarshal(b, &rc) == nil && rc.Attempts != 1 {
			t.Fatalf("失败族④：失配后应回写 Attempts=1，实际 %d", rc.Attempts)
		}
	} else {
		t.Fatalf("失败族④：失配错计后重置码应仍在场（未超限不作废）")
	}
}

// TestUATBatchG_ForgotAntiEnumeration 正向防枚举恒真锁（批G处方第②条）：
// 不存在的账号走 forgot（发码）链路必须仍回 HTTP 200 + success:true + 恒真文案——
// reset 侧收口 400 不得反向把「账号是否存在」的探测面从 forgot 漏出去。
// forgot 流程的防枚举分支（:365/:370 一带）本批明确不动，这里钉死现状。
func TestUATBatchG_ForgotAntiEnumeration(t *testing.T) {
	s := f20ResetServer(t)
	raw, _ := json.Marshal(map[string]string{"username": "f20_ghost_never_registered"})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/forgot-password", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleForgotPassword(w, r)
	if w.Code != 200 {
		t.Fatalf("防枚举锁：不存在账号的 forgot 应回 HTTP 200，实际 %d（body=%s）", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("forgot 响应非 JSON: %s", w.Body.String())
	}
	if out["success"] != true {
		t.Fatalf("防枚举锁：success 应为 true，实际 %v", out["success"])
	}
	if out["message"] != "如果账号存在且绑定了邮箱，验证码已发送" {
		t.Fatalf("防枚举锁：恒真文案被改动，期望逐字 %q 实际 %q", "如果账号存在且绑定了邮箱，验证码已发送", out["message"])
	}
}
