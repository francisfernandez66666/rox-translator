// ============ openapi.go · 职责说明 ============
// 开放 API（OpenAPI）任务模型：任务创建/查询/下载的数据结构与常量定义。
// =============================================
// Package openapi 提供租户开放 API：API Key 鉴权、调用统计、开放接口能力。
package openapi

// ============ 本文件职责中文说明 ============
// 开放 API 鉴权：为外部集成方提供基于 API Key（Authorization: Bearer rk_xxx）的
// 鉴权中间件（校验哈希、检查状态为 active、原子占用当日额度并注入 context），
// 以及 API Key 权限校验（RequirePerm，支持 all 通配或精确权限名）。
//
// ⚠️ 射程现状（2026-09-29 全链路审计实测）：本包当前**没有任何生产调用方**
// （全仓 `translator/internal/openapi` 引用为 0），现网的 OpenAPI 鉴权走
// internal/api/admin_openapi.go 的 authenticateAPIKey。因此本文件属于「待接线的第二道门」，
// 它的口径必须与那道真门一致——否则哪天有人把它挂上去，就会长出一个**没有配额判定的鉴权入口**
// （D-8 的缺陷形态从来不是 SQL 写错，而是接线方式让判据失效）。
// 一致性由 openapi_quota_gate_test.go 锁住；改动本包请同步看 admin_openapi.go。
// ========================================

import (
	"context"
	"errors"
	"net/http"

	"translator/internal/store"
)

// ctxKey context 存取 API Key 信息的键类型
type ctxKey struct{}

// APIKeyCtx 注入 context 的 API Key 信息
type APIKeyCtx struct {
	APIKey *store.APIKey // 当前请求对应的 API Key 记录
}

// FromContext 从 context 取 API Key 信息
func FromContext(ctx context.Context) *APIKeyCtx {
	v, _ := ctx.Value(ctxKey{}).(*APIKeyCtx)
	return v
}

// Middleware API Key 鉴权中间件（开放 API 专用）
// 请求头 Authorization: Bearer rk_xxx
//
// 判据顺序与 admin_openapi.go 的 authenticateAPIKey 保持同形：
//
//	① 取不到/已停用/无归属用户 → 401（不计数：鉴权失败不是业务调用）；
//	② 当日额度**原子占用**（Store.ReserveAPICall，判据与计数同一条语句）→ 打满回 429。
//
// ★ 这里刻意**不调** TouchAPIKey：那条函数在启用 Redis 时会对同一个配额键再 INCR 一次，
//
//	与 ReserveAPICall 并走＝每请求 +2，正是 2026-08-26 整改 A3 杀掉的「两侧各 +1」形态的复现。
//	ReserveAPICall 自己已经落了 call_count/last_used_at 展示字段，不需要第二次计数。
//
// ⚠️ 与真门的一处已知差异：这里的日配额判定只有 ReserveAPICall 一条（没有 validateAPIKey 的
//
//	快照预检）。预检只是「明显打满时省一次 UPDATE」的止损优化，不影响放行语义，故不搬过来。
func Middleware(st *store.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if len(h) < 8 || h[:7] != "Bearer " {
			http.Error(w, `{"error":"缺少 API Key"}`, http.StatusUnauthorized)
			return
		}
		key := h[7:]
		ak, err := st.GetAPIKeyByHash(store.HashAPIKey(key))
		if err != nil || ak.Status != "active" {
			http.Error(w, `{"error":"API Key 无效或已停用"}`, http.StatusUnauthorized)
			return
		}
		if ak.UserID <= 0 {
			// 强绑定口径（与 validateAPIKey 同）：无归属用户的 Key 一律无效，避免任务归属无处可查
			http.Error(w, `{"error":"API Key 无效或已停用"}`, http.StatusUnauthorized)
			return
		}
		if !st.ReserveAPICall(ak.ID, ak.DailyCallLimit) {
			// 配额打满：本次不占用展示计数（被拒的调用不该进客户的用量概览），与真门同码 429
			http.Error(w, `{"error":"API Key 今日调用次数已达上限"}`, http.StatusTooManyRequests)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, &APIKeyCtx{APIKey: ak})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePerm 校验 API Key 权限
func RequirePerm(ak *store.APIKey, perm string) error {
	if ak == nil {
		return errors.New("未鉴权")
	}
	if ak.Perms == "all" || ak.Perms == perm {
		return nil
	}
	return errors.New("API Key 无此权限: " + perm)
}
