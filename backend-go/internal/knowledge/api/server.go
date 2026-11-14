// ============ server.go · 职责说明 ============
// 四期 W3（§4.3.4）：知识服务 HTTP 契约面（17 路径，含健康面）。
//
//   - 只连独立知识库（KnowledgeStore），只回契约字段；零模型调用、零跨域依赖（KN13）。
//   - 只绑 127.0.0.1:8791（由 cmd 装配），共享密钥头 X-Knowledge-Token 常数时间比较；
//     不挂公网、不进反代（§4.3.1 安全条）。
//   - 客户端与服务端只许引用 internal/knowledge/contract 这一份 DTO（KN1）。
//
// ================================================
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"translator/internal/knowledge/contract"
	knowledgeStore "translator/internal/knowledge/store"
	"translator/internal/observability"
)

// Server 知识服务端：持有独立知识库连接与共享密钥。
type Server struct {
	ks    *knowledgeStore.KnowledgeStore
	token string
}

// NewServer 构造知识服务端。token 为空时所有契约端点 fail-closed 拒绝（由 cmd 启动期也拒启）。
func NewServer(ks *knowledgeStore.KnowledgeStore, token string) *Server {
	return &Server{ks: ks, token: token}
}

// ServerFace KN1 三方等值：服务端只引用契约包规范面，禁止自定义 DTO/路径/枚举。
func ServerFace() contract.Face { return contract.CanonicalFace() }

// Handler 注册全部契约路径；健康面单独挂（不鉴权，供主服务探针）。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(contract.PathHealth, s.handleHealth)
	for _, p := range contract.EndpointPaths {
		if p == contract.PathHealth {
			continue
		}
		mux.HandleFunc(p, s.dispatch)
	}
	return mux
}

// authOK 共享密钥头校验（常数时间比较）。token 未配 ⇒ 全拒（不允许匿名裸奔）。
func (s *Server) authOK(r *http.Request) bool {
	if s.token == "" {
		return false
	}
	got := r.Header.Get("X-Knowledge-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "")
		return
	}
	if !s.authOK(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "")
		return
	}
	switch r.URL.Path {
	case contract.PathI18nList:
		s.hI18nList(w, r)
	case contract.PathI18nByTenantLang:
		s.hI18nByTenantLang(w, r)
	case contract.PathI18nUpsert:
		s.hI18nUpsert(w, r)
	case contract.PathEntriesQuery:
		s.hEntriesQuery(w, r)
	case contract.PathProductsUpsert, contract.PathProductsDelete, contract.PathFactsUpsert:
		s.hWrite(w, r, r.URL.Path)
	case contract.PathAnswer:
		s.hAnswer(w, r)
	default:
		s.hRead(w, r, r.URL.Path)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, errCode, reason string) {
	if status >= http.StatusInternalServerError {
		observability.Warn(context.Background(), "知识服务异常响应",
			"status", status, "error", errCode, "reason", reason)
	}
	writeJSON(w, status, contract.ErrorResponse{Error: errCode, Reason: reason})
}
