// ============ auth_logout.go · 职责说明 ============
// IAM P0-10（2026-10-21）：登出真撤销。
//
// 旧形态的「登出」只发生在前端：清掉 localStorage 里的 token，服务端一无所知——
// 一条被复制出去（日志、截图、共享设备未关的标签页）的 token 在 24 小时有效期内
// 依然能通行。本文件补上服务端那一半：
//   - JWT 签发时带 jti（见 iam.Sign），登出把该 jti 写进撤销名单；
//   - 名单存在「Redis 优先、进程内存兜底」的 vcode 抽象里（与验证码同一套读写口径），
//     TTL = 该 token 的剩余有效期——过期之后名单项自己消失，不会无界膨胀；
//   - authUserUncached 每请求查一次名单（命中即视同未登录）。
//
// ★ Redis 不可用时诚实降级：仍写进程内存（单实例下撤销真的生效），但响应体
// 带 revoked=false，告诉前端与运维「这一台只撤了自己进程里的这一份」。
// 为什么不返回失败：客户端此刻已经决定离开，报 5xx 只会把「登出失败」摆到
// 客户脸上，而它真正的影响是「别的实例可能还认这条 token」——那件事用读数
// 表达比用错误码表达更准确（同 F-64 那批「状态码要诚实」的方向）。
// =============================================
package api

import (
	"net/http"
	"time"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/infra/redis"
)

// jwtRevokedPrefix 撤销名单的键前缀（`revoked:jti:<jti>`）。
// 与验证码键（vcode:reset:/vcode:email:）同走 vcodeSet/vcodeGet，
// 好处是 Redis 不可用时的本地兜底、TTL 语义、惰性清扫全部复用现成实现。
const jwtRevokedPrefix = "revoked:jti:"

// revokeJWT 把一条 token 的 jti 写进撤销名单，TTL 取该 token 的剩余有效期。
// 返回值 revoked：是否写进了 Redis（跨实例生效）——false 表示只落在本进程内存，
// 别的实例仍会认这条 token（诚实降级，前端据此展示读数）。
// jti 为空（随机源不可用签出的旧形态 token）时无法单条撤销，返回 false。
func revokeJWT(jti string, ttl time.Duration) bool {
	if jti == "" {
		return false
	}
	if ttl <= 0 {
		// 已经过期的 token 本来就用不了，名单不必写；对调用方仍报"已失效"。
		return true
	}
	vcodeSet(jwtRevokedPrefix+jti, []byte("1"), ttl)
	return redis.Get() != nil
}

// jwtRevoked 判断一条 token 的 jti 是否已在撤销名单里。
// 空 jti 一律返回 false（不可撤销的旧 token 仅受 token_version 全端撤销约束）。
func jwtRevoked(jti string) bool {
	if jti == "" {
		return false
	}
	_, ok := vcodeGet(jwtRevokedPrefix + jti)
	return ok
}

// handleLogout 登出接口（POST /api/auth/logout）。
// 语义：作废**当前这一条** token（不牵连该用户其他设备），成功后前端清本地态。
// 对已过期/已失效的 token 同样回 401——那是既有 request() 的清态信号（core.ts 的
// handleUnauthorized），前端本来就要清，不需要为"登出"单开一条成功路径。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "请使用 POST 请求"))
		return
	}
	claims, err := auth.ClaimsOf(r)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "登录状态已失效，请重新登录"))
		return
	}
	ttl := time.Until(time.Unix(claims.Exp, 0))
	revoked := revokeJWT(claims.JTI, ttl)
	// 审计留痕：登出是账号安全事件里最常被查的一条（"这个号什么时候被谁退的"），
	// 且这里拿得到 uid/tid，无需再查用户行；失败不影响登出本身（审计不阻塞业务）。
	if s.Store != nil {
		s.Store.LogAudit(claims.TenantID, claims.UserID, "logout", "auth", "主动登出")
	}
	msg := "已登出"
	if !revoked {
		// 诚实读数：没有 Redis（或 jti 缺失）时，这条 token 只在当前实例内被拒。
		msg = "已登出（当前实例已失效；未启用 Redis，其他实例可能仍接受此凭证）"
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"revoked": revoked,
		"message": msg,
	})
}
