// ============ spa.go · 职责说明 ============
// SPA 兜底路由：非 API 路径统一回退 index.html，静态资源直出并设缓存头。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现前端静态资源（SPA）托管服务：
//   - 拦截 /api 前缀的未匹配路由，统一返回 404 JSON（避免误回退到前端页面）
//   - 前端 dist 目录未构建时（index.html 缺失），首页返回安装引导页，其余路径返回 404
//   - 提供前端构建产物（HTML/JS/CSS/图片等）的静态文件服务
//   - SPA 路由回退：请求路径不存在时回退到 index.html（配合前端 history 路由）
//   - 缓存策略：带 hash 的 /assets/ 资源长缓存（immutable），其余资源每次重新校验
// 安全约束：对请求路径做 filepath.Clean 归一化并校验前缀，防止目录穿越（path traversal）。

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleSPA 前端静态资源服务入口；无 dist 构建产物时给出安装提示页。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（依据 r.URL.Path 分发静态资源或回退路由）。
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// 拦截 /api 前缀的未匹配路由：API 由其他 Handler 处理，这里避免把接口请求回退到前端页面
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, 404, map[string]string{"error": "接口不存在"})
		return
	}

	// 确定前端 dist 根目录：未配置时默认取进程工作目录下的 "dist"
	dist := s.Dist
	if dist == "" {
		dist = "dist"
	}
	// 校验 dist 下是否存在 index.html：缺失说明前端尚未构建，给出安装引导
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		if r.URL.Path == "/" {
			// 首页：返回静态安装提示 HTML（说明如何构建前端）
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<!DOCTYPE html><html><body style="font-family:sans-serif;max-width:640px;margin:60px auto">
<h1>能言服务已启动</h1>
<p>前端页面尚未构建。请先在前端目录执行 <code>npm run build</code>，
或在 <code>backend-go/cmd/server/main.go</code> 中通过 <code>-frontend</code> 指定前端 dist 目录。</p>
<p>API 正常：<a href="/api/health">/api/health</a></p>
</body></html>`))
		} else {
			// 非首页且未构建：返回 404
			writeJSON(w, 404, map[string]string{"error": "未找到前端资源"})
		}
		return
	}

	// 拼接实际文件路径：根路径映射到 index.html
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	// 归一化路径并校验前缀：防止 "../" 目录穿越读取 dist 之外的文件
	full := filepath.Join(dist, filepath.Clean(filepath.FromSlash(path)))
	if !strings.HasPrefix(full, filepath.Clean(dist)) {
		writeJSON(w, 404, map[string]string{"error": "非法路径"})
		return
	}
	// 目标文件不存在或是目录：SPA 路由回退到 index.html（每次重新校验，避免缓存旧 bundle）
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dist, "index.html"))
		return
	}
	// 缓存策略：带 hash 的 /assets/ 静态资源可长缓存；index.html 等入口每次重新校验
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, full)
}
