// ============ auth_password_gate.go · 职责说明 ============
// IAM P0-9（2026-10-21）：强制改密**服务端真拦**。
//
// 旧形态只有"前端弹窗"：登录响应带 must_change_pwd=1，登录卡弹一个改密对话框，
// 用户点右上角叉/刷新页面即可绕过，直接进入系统。对 Excel 批量导入的账号而言，
// 那条初始密码可能进过导出文件——"弹窗可绕"等于这条安全设计从未成立。
//
// 本文件把它变成**请求级**事实：签发 token 时写入 mcp 声明（见 iam.Sign），
// 本中间件挂在 mux 最内层，mcp=1 时除认证域与健康检查外一律回 428。
//
// 为什么不是"在 authUser 里判"：authUser 只被**需要用户行**的 handler 调用，
// 而不调 authUser 的 handler（统计、字典、公开配置等）照样能读到数据——
// 那样等于只拦住了"看得见的那一半"。中间件是唯一覆盖全路由的位置。
//
// 为什么放最内层（包 mux）而不是最外层：它与业务 handler 共享同一份请求上下文，
// 且要在 CORS 预检（OPTIONS 在外层已短路）、体限、访问日志之后才判，
// 这样 428 在访问日志里与普通业务响应同形，排障时能按 status 直接筛出来。
// =============================================
package api

import (
	"net/http"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
)

// passwordGateAllowed 判断该路径在「待改密」状态下是否放行。
//
// 放行面刻意取**整个认证域前缀** `/api/auth/`：改密、登出、会话恢复（me）、
// 重新登录、找回密码都在这条前缀下，都是"把自己弄回正常状态"该走的路；
// 逐个列精确路径的形态每加一条认证接口就要回来改一次，而漏一条的表现是
// "客户点了没反应"（428 被前端按未知错误吞掉），属于不该靠人记住的那类。
// 健康检查放行：它不返回任何账号数据，且探针被 428 打红会掩盖真实故障。
func passwordGateAllowed(path string) bool {
	if strings.HasPrefix(path, "/api/auth/") {
		return true
	}
	return path == "/api/health"
}

// withPasswordGate 强制改密闸（挂在 Handler() 最内层，见文件头）。
// 非 /api/ 路径（前端静态资源、SPA 兜底、文档页）一律放行——本闸只保护接口；
// token 缺失或无效时也放行：那种情况由各 handler 自己回 401，本闸不替它们分诊
// （否则一条过期 token 会收到 428 而不是"请重新登录"）。
func (s *Server) withPasswordGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || passwordGateAllowed(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		claims, err := auth.ClaimsOf(r)
		if err != nil || !claims.MustChangePassword {
			next.ServeHTTP(w, r)
			return
		}
		s.writeError(w, r, apierrors.New(apierrors.ErrPasswordChangeRequired,
			"账号处于首登强制改密状态：请先修改密码，再使用其他功能"))
	})
}
