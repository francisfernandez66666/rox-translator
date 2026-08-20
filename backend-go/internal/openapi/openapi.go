// Package openapi 提供租户开放 API：API Key 鉴权、调用统计、开放接口能力。
package openapi

// ============ 本文件职责中文说明 ============
// 开放 API 鉴权：为外部集成方提供基于 API Key（Authorization: Bearer rk_xxx）的
// 鉴权中间件（校验哈希、检查状态为 active、更新最后调用时间并注入 context），
// 以及 API Key 权限校验（RequirePerm，支持 all 通配或精确权限名）。
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
		st.TouchAPIKey(ak.ID)
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
