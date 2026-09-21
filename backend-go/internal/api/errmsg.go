// ============================================================================
// api/errmsg.go — 对外错误文案统一出口（★ P0-3 错误脱敏，2026-09-21 评估报告整改）
// 背景：全站 300+ 处 handler 把 err.Error() 裸拼进响应 message 字段，DB 驱动/
//
//	文件系统/网络/TLS/序列化错误会把表名、SQL、绝对路径、连接串等内部信息透给
//	任意调用方（信息泄露 + 吓跑客户）。
//
// 口径：本文件是唯一裁判——
//
//	① *apierrors.APIError 与含中文的业务文案 → 原样透出（前端直接展示、UAT 断言依赖）；
//	② 命中「内部实现特征」（SQL/驱动/路径/网络/TLS/序列化等，见 leakMarkers）→ 对外
//	   固定为 serviceBusyMessage 一句人话，原文以 error_masked 写 slog（带 trace_id），
//	   排查走日志不走响应体。
//
// 使用：handler 里 publicErrMessage(r.Context(), err) 替换一切 err.Error() 出参。
// ============================================================================
package api

import (
	"context"
	"errors"
	"log/slog"
	"unicode"

	apierrors "translator/internal/errors"
	"translator/internal/observability"
)

// serviceBusyMessage 内部错误对外统一文案（不透露任何失败原因类别）。
const serviceBusyMessage = "服务处理出现异常，请稍后重试；如持续出现请联系管理员"

// leakMarkers 内部实现特征（全部小写，对 err.Error() 做子串扫描）。
// 宁滥勿漏：命中即脱敏；业务文案因含中文或人话措辞不会踩中这些标记。
var leakMarkers = []string{
	// 数据库/ORM
	"sql", "pq:", "pgx", "sqlite", "postgres", "no such table", "no such column",
	"relation \"", "constraint", "duplicate key", "syntax error at or near", "prepared statement",
	"rows", "scan:", "driver", "bad connection", "deadlock", "serialization failure",
	// 网络/系统
	"dial tcp", "dial udp", "connection refused", "connection reset", "i/o timeout",
	"context deadline", "broken pipe", "permission denied", "no such file", "cannot assign",
	"address already in use", "host unreachable", "name resolution",
	// 文件系统/进程
	"/opt/", "/usr/", "/var/", "/etc/", "/home/", "open ", "mkdir", "remove ", "rename ",
	"exec:", "exit status", "signal", "fork/exec",
	// 运行时/序列化
	"panic:", "goroutine", "nil pointer", "index out of range", "slice bounds",
	"json:", "unmarshal", "marshal", "invalid character", "unexpected eof", "proto:",
	// TLS/认证材料
	"tls", "x509", "certificate", "cipher", "handshake",
	// 凭据类词（原始库错误常把 key/token/dsn 拼进消息）
	"dsn", "api_key", "apikey", "secret", "bearer",
}

// publicErrMessage 计算任意 error 的对外安全文案（详见文件头口径）。
// 参数：ctx=请求上下文（脱敏发生时写日志带 trace_id，可为 nil）；err=原始错误。
func publicErrMessage(ctx context.Context, err error) string {
	if err == nil {
		return "未知错误"
	}
	// ① 结构化错误：Message 字段本就是设计给用户看的
	var ae *apierrors.APIError
	if errors.As(err, &ae) {
		return ae.Message
	}
	msg := err.Error()
	if !hasInternalLeak(msg) {
		return msg // ② 业务文案原样透出
	}
	// ③ 内部错误：原文进日志，对外只给固定文案
	if ctx == nil {
		ctx = context.Background()
	}
	observability.Log(ctx, slog.LevelError, "error_masked", "detail", msg)
	return serviceBusyMessage
}

// hasInternalLeak 判定消息是否含内部实现特征：
// 含中日韩汉字的文案视为人工设计的业务话术直接放行（但仍先过标记扫描，
// 防「读取用户失败: sql: no rows in result set」这类把内部错误拼进中文前缀）。
func hasInternalLeak(msg string) bool {
	low := lowerASCII(msg)
	for _, m := range leakMarkers {
		if contains(low, m) {
			return true
		}
	}
	// 全 ASCII 且不含中文的裸错误（库默认文案）一律按内部错误处理；
	// 例外白名单：这些短词已被前端/测试当作用户话术使用
	if !hasCJK(msg) {
		switch msg {
		case "unauthorized", "forbidden", "not found", "bad request", "conflict", "too many requests":
			return false
		}
		return true
	}
	return false
}

// hasCJK 是否含 CJK 统一表意文字（汉字文案判定，全角标点不算）。
func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// lowerASCII 仅折叠 ASCII 大写为小写（避免 strings.ToLower 的 Unicode 特殊映射改变匹配），
// contains 为小写子串包含判断——拆两个函数让 300+ 调用点零额外分配顾虑。
func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

// contains 小写子串包含。
func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

// indexOf 朴素子串定位（错误消息都很短，无需花哨算法）。
func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
