package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleSPA 前端静态资源服务；无 dist 时给出安装提示
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, 404, map[string]string{"error": "接口不存在"})
		return
	}

	dist := s.Dist
	if dist == "" {
		dist = "dist"
	}
	if _, err := os.Stat(filepath.Join(dist, "index.html")); err != nil {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<!DOCTYPE html><html><body style="font-family:sans-serif;max-width:640px;margin:60px auto">
<h1>翻译助手服务已启动</h1>
<p>前端页面尚未构建。请先在前端目录执行 <code>npm run build</code>，
或在 <code>backend-go/cmd/server/main.go</code> 中通过 <code>-frontend</code> 指定前端 dist 目录。</p>
<p>API 正常：<a href="/api/health">/api/health</a></p>
</body></html>`))
		} else {
			writeJSON(w, 404, map[string]string{"error": "未找到前端资源"})
		}
		return
	}

	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}
	full := filepath.Join(dist, filepath.Clean(filepath.FromSlash(path)))
	if !strings.HasPrefix(full, filepath.Clean(dist)) {
		writeJSON(w, 404, map[string]string{"error": "非法路径"})
		return
	}
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		// SPA 回退到 index.html（每次重新校验，避免缓存旧 bundle）
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(dist, "index.html"))
		return
	}
	// 带 hash 的静态资源：长缓存；index.html：每次重新校验
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFile(w, r, full)
}