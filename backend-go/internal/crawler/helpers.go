// ============ helpers.go · 职责说明 ============
// crawler 包内部小工具函数（字符串解析等）。
// =============================================
package crawler

import (
	"strconv"
	"strings"
)

// atoiSafe 安全字符串转 int。
// 实现：先 TrimSpace 去除首尾空白再解析（容忍 " 123 " 这类带空格的输入），
// 解析失败返回 0 与错误（由调用方决定是否降级/跳过）。
// 用于从网页/接口文本中提取数字字段（页数、大小、计数等）。
func atoiSafe(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
