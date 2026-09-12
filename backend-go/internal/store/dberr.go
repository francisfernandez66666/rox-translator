// ============ dberr.go · 职责说明 ============
// 数据库驱动错误脱敏：把 sqlite/postgres 驱动层原始错误（含表名、约束名、SQLSTATE）
// 转换为可安全返回给客户端的用户文案；原始错误由调用方写日志留证。
// 背景（2026-09-12 PG 重研）：A3 实测客户端收到
// 「创建失败: pq: duplicate key value violates unique constraint "users_username_tenant_id_key" (23505)」，
// 泄漏内部结构且文案随方言漂移。
// =============================================
package store

import (
	"strings"
)

// IsUniqueViolation 判断是否唯一约束冲突（兼容 sqlite / postgres 两方言）。
// 参数：err=驱动或包装后的错误；返回 true 表示重复键。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key value violates unique constraint") ||
		strings.Contains(msg, "(23505)")
}

// DebriefDBError 驱动错误 → 用户安全文案。
//   - 唯一冲突 → 「记录已存在…」；
//   - 其他驱动级错误（pq:/sqlite 报错码）→ 通用繁忙文案（原始错误请调用方记日志）；
//   - 业务自定义错误（如 ErrInsufficientBalance 的中文 message）原样返回。
//
// 参数：err=存储层返回的错误。
func DebriefDBError(err error) string {
	if err == nil {
		return ""
	}
	if IsUniqueViolation(err) {
		return "记录已存在（名称/编码/邮箱重复）"
	}
	msg := err.Error()
	if strings.Contains(msg, "pq:") || strings.Contains(msg, "SQLITE_") ||
		strings.Contains(msg, "constraint") || strings.Contains(msg, "syntax error at or near") {
		return "系统繁忙，请稍后重试（已通知管理员）"
	}
	return msg
}
