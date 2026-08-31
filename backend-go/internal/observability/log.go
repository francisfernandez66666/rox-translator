// ============ 本文件职责中文说明 ============
// 结构化日志（工作流 C）：基于标准库 log/slog 的 JSON 日志器，统一带 trace_id，
// 替代散落的 log.Printf，支撑全链路定位与后续接入 Loki/Tempo。
// =============================================
// Package observability 提供结构化日志：基于 log/slog 的 JSON 日志器，
// 输出统一携带 trace_id（从 context 读取），替代散落的 log.Printf。
package observability

import (
	"context"
	"log/slog"
	"os"

	"translator/internal/errors"
)

// NewLogger 创建 JSON 格式的结构化日志器（输出到 stdout，交由 Caddy/systemd 采集）。
func NewLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// Log 输出一条带 trace_id 的结构化日志。
// 参数 ctx: 请求上下文（含 trace_id）；level: 日志级别；msg: 消息；args: 键值对（同 slog）。
func Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if tid := errors.TraceIDFromContext(ctx); tid != "" {
		args = append(args, "trace_id", tid)
	}
	slog.Default().Log(ctx, level, msg, args...)
}

// Info / Warn / Error 便捷封装。
// Info 输出一条 INFO 级结构化日志（带 trace_id）。
func Info(ctx context.Context, msg string, args ...any)  { Log(ctx, slog.LevelInfo, msg, args...) }
// Warn 输出一条 WARN 级结构化日志（带 trace_id，用于非致命但需关注的情况）。
func Warn(ctx context.Context, msg string, args ...any)  { Log(ctx, slog.LevelWarn, msg, args...) }
// Error 输出一条 ERROR 级结构化日志（带 trace_id，用于异常与失败路径）。
func Error(ctx context.Context, msg string, args ...any) { Log(ctx, slog.LevelError, msg, args...) }
