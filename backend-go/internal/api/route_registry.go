// ============ route_registry.go · 职责说明 ============
// http.ServeMux 薄包装（★ F10 OpenAPI 路由注册期自动导出）：
// 在标准注册行为之外记录每个 pattern（含新式 "GET /p" 方法前缀解析），
// 供 /openapi/v1.json 在装配期枚举 /openapi/ 路由补齐规范；对既有
// 231 处 s.mux.HandleFunc 调用零改动（方法名与签名完全一致）。
// =============================================
package api

import (
	"net/http"
	"strings"
)

// RouteMeta 一条路由登记（Pattern 为原始注册串；Methods 解析自新式前缀，旧式为 nil）。
type RouteMeta struct {
	Pattern string
	Methods []string
}

// routeMux 包装 ServeMux 并登记路由。
type routeMux struct {
	*http.ServeMux
	routes []RouteMeta
}

// newRouteMux 创建包裹标准 ServeMux 的路由登记器。
func newRouteMux() *routeMux {
	return &routeMux{ServeMux: http.NewServeMux()}
}

// recordPattern 解析并登记注册串："GET /api/x" → Pattern="/api/x", Methods=["GET"]；
// 旧式 "/api/x" → Methods=nil（语义为全方法）。
func (m *routeMux) recordPattern(pattern string) {
	p, methods := pattern, []string(nil)
	if i := strings.IndexByte(pattern, ' '); i > 0 {
		head := pattern[:i]
		if !strings.Contains(head, "/") { // 方法前缀（GET/POST/PUT/DELETE/HEAD/OPTIONS/PATCH 或 {host} 之外的纯词）
			p = pattern[i+1:]
			methods = []string{head}
		}
	}
	m.routes = append(m.routes, RouteMeta{Pattern: p, Methods: methods})
}

// HandleFunc 登记后透传标准实现。
func (m *routeMux) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	m.recordPattern(pattern)
	m.ServeMux.HandleFunc(pattern, h)
}

// Handle 登记后透传标准实现。
func (m *routeMux) Handle(pattern string, h http.Handler) {
	m.recordPattern(pattern)
	m.ServeMux.Handle(pattern, h)
}

// allRoutes 返回登记快照（仅装配/只读用途）。
func (m *routeMux) allRoutes() []RouteMeta { return m.routes }
