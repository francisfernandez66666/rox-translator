package api

// ============ 本文件职责中文说明 ============
// 结构化访问日志中间件：每个 HTTP 请求输出一行 JSON 日志（含耗时/状态/路径/客户端IP），
// 便于运维检索与定位慢请求 / 异常来源（如本轮 OOM 排查中 quant 前端高频轮询的定位）。
// 日志字段：time/level/method/path/status/duration_ms/ip/bytes。
// ========================================

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// accessLogEntry 访问日志单条记录（JSON 序列化）。
type accessLogEntry struct {
	Time       string  `json:"time"`        // 请求时间（RFC3339）
	Level      string  `json:"level"`       // 日志级别（info）
	Method     string  `json:"method"`      // HTTP 方法
	Path       string  `json:"path"`        // 请求路径
	Status     int     `json:"status"`      // 响应状态码
	DurationMS float64 `json:"duration_ms"` // 处理耗时（毫秒）
	IP         string  `json:"ip"`          // 客户端 IP
	Bytes      int64   `json:"bytes"`       // 响应字节数
}

// statusWriter 包装 ResponseWriter 以捕获状态码与字节数。
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

// WriteHeader 记录响应状态码并转发。
func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Write 记录响应字节数并转发。
func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// withAccessLog 结构化访问日志中间件：包裹下一层 Handler 并输出 JSON 日志。
// 参数 next: 下一层 Handler。返回: 包装后的 Handler。
func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		// 输出一行结构化 JSON 日志
		entry := accessLogEntry{
			Time:       time.Now().Format(time.RFC3339),
			Level:      "info",
			Method:     r.Method,
			Path:       r.URL.Path,
			Status:     sw.status,
			DurationMS: float64(time.Since(start).Microseconds()) / 1000.0,
			IP:         clientIP(r),
			Bytes:      sw.bytes,
		}
		b, err := json.Marshal(entry)
		if err == nil {
			log.Println(string(b))
		}
	})
}
