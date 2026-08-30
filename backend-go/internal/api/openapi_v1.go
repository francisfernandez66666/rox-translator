// ============ openapi_v1.go · 职责说明 ============
// 阶段十一：OpenAPI 3.0 规范服务端点。
//   - GET /openapi/v1.json  返回 JSON 规范（供 openapi-generator / 前端 SDK 生成消费）
// 规范源文件经 go:embed 内联，无需运行时外挂文件（单二进制部署友好）。
// 人类可读 YAML 版本见 deploy/openapi/openapi.v1.yaml（与 JSON 规范同源结构）。
package api

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.v1.json
var openapiV1JSON []byte

// routesOpenAPISpec 注册规范服务路由（在 routes() 中调用）。
func (s *Server) routesOpenAPISpec() {
	s.mux.HandleFunc("/openapi/v1.json", s.handleOpenAPIV1JSON)
}

// handleOpenAPIV1JSON 返回 JSON 规范。
func (s *Server) handleOpenAPIV1JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	w.Write(openapiV1JSON)
}
