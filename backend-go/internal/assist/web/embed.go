// ============ web/embed.go · 职责说明 ============
// 管理台页面内嵌（★ 改造 1A，2026-09-17）：admin.html 随二进制编译，
// 消除「部署需额外投放 web/ 目录」的隐性依赖——单构建产物即可运行。
// ASSIST_WEB 环境变量仍可覆盖为外置目录（运维临时改页面用），优先级见 api.Server.adminPage。
// =============================================
package web

import _ "embed"

// AdminHTML 管理台单文件页面（内嵌默认值）。
//
//go:embed admin.html
var AdminHTML []byte
