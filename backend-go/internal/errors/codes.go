// ============ 本文件职责中文说明 ============
// 统一错误码体系（工作流 C）：定义全平台错误码枚举 ErrorCode、结构化错误 APIError
// （含 code/message/details/trace_id），以及对应的 HTTP 状态码映射与 JSON 写出。
// 目标：前后端以稳定 error_code 通信，而非随实现漂移的中文字符串。
// =============================================
// Package errors 提供统一错误码体系：ErrorCode 枚举、APIError 结构化错误、
// HTTP 状态码映射与 JSON 写出，供各 API handler 统一返回标准错误。
package errors

import (
	"context"
	"encoding/json"
	"net/http"
)

// ErrorCode 统一错误码（前端据此做 i18n 与分支处理）。
type ErrorCode string

// 系统级错误码
const (
	ErrInternal   ErrorCode = "INTERNAL_ERROR"
	ErrValidation ErrorCode = "VALIDATION_ERROR"
	ErrUnauthorized ErrorCode = "UNAUTHORIZED"
	ErrForbidden  ErrorCode = "FORBIDDEN"
	ErrNotFound   ErrorCode = "NOT_FOUND"
	ErrConflict   ErrorCode = "CONFLICT"
	ErrRateLimited ErrorCode = "RATE_LIMITED"
)

// 业务级错误码
const (
	ErrInsufficientBalance ErrorCode = "INSUFFICIENT_BALANCE"
	ErrQuotaExceeded       ErrorCode = "QUOTA_EXCEEDED"
	ErrTicketNotFound      ErrorCode = "TICKET_NOT_FOUND"
	ErrKBNotFound          ErrorCode = "KB_NOT_FOUND"
	ErrTranslationFailed   ErrorCode = "TRANSLATION_FAILED"
	ErrFileTooLarge        ErrorCode = "FILE_TOO_LARGE"
	ErrModelUnreachable    ErrorCode = "MODEL_UNREACHABLE"
	ErrCircuitBreakerOpen  ErrorCode = "CIRCUIT_BREAKER_OPEN"
	ErrQuotaReserved       ErrorCode = "QUOTA_RESERVED"
)

// APIError 结构化错误响应体。
// 说明：success 字段保持为 false 以兼容既有前端（前端以 res.success 判断成败），
// 新增 code 字段供前端做 i18n 与分支处理，trace_id 供全链路定位。
type APIError struct {
	Success bool        `json:"success"`
	Code    ErrorCode   `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// New 构造带消息的结构化错误。
func New(code ErrorCode, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

// WithDetails 附加结构化细节（返回自身，便于链式）。
func (e *APIError) WithDetails(details interface{}) *APIError {
	e.Details = details
	return e
}

// WithTraceID 附加链路追踪 ID（返回自身，便于链式）。
func (e *APIError) WithTraceID(traceID string) *APIError {
	e.TraceID = traceID
	return e
}

// HTTPStatus 将错误码映射为标准 HTTP 状态码。
func (e *APIError) HTTPStatus() int {
	switch e.Code {
	case ErrUnauthorized:
		return http.StatusUnauthorized
	case ErrForbidden:
		return http.StatusForbidden
	case ErrValidation, ErrRateLimited, ErrQuotaExceeded, ErrFileTooLarge:
		return http.StatusBadRequest
	case ErrNotFound, ErrTicketNotFound, ErrKBNotFound:
		return http.StatusNotFound
	case ErrConflict:
		return http.StatusConflict
	case ErrInsufficientBalance:
		return http.StatusPaymentRequired
	case ErrInternal, ErrTranslationFailed, ErrModelUnreachable, ErrCircuitBreakerOpen, ErrQuotaReserved:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// WriteError 将结构化错误以 JSON 写出（含正确 HTTP 状态码）。
// 若 err.TraceID 为空且 ctx 中存在 trace_id，则自动补全，便于前端联动排查。
func WriteError(w http.ResponseWriter, ctx context.Context, e *APIError) {
	if e.TraceID == "" {
		if tid := TraceIDFromContext(ctx); tid != "" {
			e.TraceID = tid
		}
	}
	e.Success = false
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(e.HTTPStatus())
	_ = json.NewEncoder(w).Encode(e)
}
