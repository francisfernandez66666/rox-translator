// ============ 本文件职责中文说明 ============
// 链路追踪 ID（TraceID）的上下文透传：统一从 X-Trace-ID 头读取或新生成，
// 经 context 在请求生命周期内透传，供结构化日志与错误响应携带，实现全链路定位。
// =============================================
package errors

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKeyTraceID struct{}

// WithTraceID 将 traceID 注入 context。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, ctxKeyTraceID{}, traceID)
}

// TraceIDFromContext 从 context 取出 traceID（不存在返回空串）。
func TraceIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ctxKeyTraceID{}).(string); ok {
		return v
	}
	return ""
}

// GenTraceID 生成新的链路追踪 ID（16 字节十六进制）。
func GenTraceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "trace-fallback"
	}
	return hex.EncodeToString(b)
}
