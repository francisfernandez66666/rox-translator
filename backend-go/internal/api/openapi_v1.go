// ============ openapi_v1.go · 职责说明 ============
// 阶段十一：OpenAPI 3.0 规范服务端点。
//   - GET /openapi/v1.json  返回 JSON 规范（供 openapi-generator / 前端 SDK 生成消费）
//
// 规范源文件经 go:embed 内联，无需运行时外挂文件（单二进制部署友好）。
// 人类可读 YAML 版本见 deploy/openapi/openapi.v1.yaml（与 JSON 规范同源结构）。
package api

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

//go:embed openapi.v1.json
var openapiV1JSON []byte

// routesOpenAPISpec 注册规范服务路由（在 routes() 中调用）。
func (s *Server) routesOpenAPISpec() {
	s.mux.HandleFunc("/openapi/v1.json", s.handleOpenAPIV1JSON)
}

// handleOpenAPIV1JSON 返回 JSON 规范。
// ★ F10：默认在手工规范之上做「路由注册期自动导出」——遍历 mux 中所有 /openapi/ 前缀
// 路由，缺失的路径自动补 GET/POST 占位条目（x-auto=true 标记，凭据无关）；
// ?manual_only=1 可退回纯手工规范（openapi-generator 消费建议用手工视图）。
func (s *Server) handleOpenAPIV1JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(200)
	if r.URL.Query().Get("manual_only") == "1" {
		w.Write(openapiV1JSON)
		return
	}
	var spec map[string]interface{}
	if err := json.Unmarshal(openapiV1JSON, &spec); err != nil {
		w.Write(openapiV1JSON) // 规范损坏时兜底原样输出（保 embed 校验测试之外的极端情况）
		return
	}
	paths, _ := spec["paths"].(map[string]interface{})
	if paths == nil {
		paths = map[string]interface{}{}
	}
	added := 0
	// 路由登记表（route_registry.go）在注册期已收集全部 pattern + 方法前缀
	seen := map[string][]string{}
	for _, rt := range s.mux.allRoutes() {
		if !strings.HasPrefix(rt.Pattern, "/openapi/") || rt.Pattern == "/openapi/v1.json" {
			continue
		}
		ms := rt.Methods
		if len(ms) == 0 {
			ms = []string{"GET", "POST"} // 旧式注册（无方法前缀）：GET/POST 双写占位
		}
		seen[rt.Pattern] = append(seen[rt.Pattern], ms...)
	}
	for p, ms := range seen {
		if _, ok := paths[p]; ok {
			continue
		}
		entry := map[string]interface{}{}
		for _, m := range ms {
			if _, dup := entry[strings.ToLower(m)]; dup {
				continue
			}
			entry[strings.ToLower(m)] = map[string]interface{}{
				"summary":   "auto-generated route entry",
				"x-auto":    true,
				"responses": map[string]interface{}{"200": map[string]interface{}{"description": "auto"}},
			}
		}
		paths[p] = entry
		added++
	}
	if added > 0 {
		spec["paths"] = paths
		spec["x-auto-routes"] = added
	}
	out, err := marshalSpecOrdered(spec)
	if err != nil {
		w.Write(openapiV1JSON)
		return
	}
	w.Write(out)
}

// marshalSpecOrdered 按 OpenAPI 惯例重建顶层键顺序（openapi/info 先行）。
// ★ G4 修复：map 直接 Marshal 会按字母序把 "openapi" 版本键排到 components/info 之后，
//
//	破坏以首字节嗅探规范版本的老派工具与 UAT 断言。其余键维持字母序，输出仍为合法 JSON。
func marshalSpecOrdered(spec map[string]interface{}) ([]byte, error) {
	head := []string{"openapi", "info", "servers", "security", "tags"}
	keys := make([]string, 0, len(spec))
	seen := map[string]bool{}
	for _, k := range head {
		if _, ok := spec[k]; ok && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
		}
	}
	rest := make([]string, 0, len(spec))
	for k := range spec {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	keys = append(keys, rest...)
	var sb strings.Builder
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		kj, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vj, err := json.Marshal(spec[k])
		if err != nil {
			return nil, err
		}
		sb.Write(kj)
		sb.WriteByte(':')
		sb.Write(vj)
	}
	sb.WriteByte('}')
	return []byte(sb.String()), nil
}
