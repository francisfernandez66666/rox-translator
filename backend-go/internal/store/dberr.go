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

	"translator/internal/db"
)

// IsUniqueViolation 判断是否唯一约束冲突（双方言）。
// ★ 判据本体已下沉到 internal/db.IsUniqueViolation（AGENTS §一·3 通用能力下沉）：
//
//	本函数只保留薄委托，供 api/ 与 store/ 内既有调用点继续使用；
//	禁止在此重建第二套字符串判定，否则 SQLite／PG 两把尺子会再度分叉。
//
// 参数：err=驱动或包装后的错误；返回 true 表示重复键。
func IsUniqueViolation(err error) bool {
	return db.IsUniqueViolation(err)
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
