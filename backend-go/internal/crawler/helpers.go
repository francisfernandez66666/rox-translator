// ============ helpers.go · 职责说明 ============
// crawler 包内部小工具函数（字符串解析等）。
// =============================================
package crawler

import (
	"strconv"
	"strings"
)

// atoiSafe 安全字符串转 int（失败返回 0 与错误）。
func atoiSafe(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
