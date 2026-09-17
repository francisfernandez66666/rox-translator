// ============ seed/embed.go · 职责说明 ============
// 初始数据 seed 内嵌（★ 改造 1A，2026-09-17）：seed.json 随二进制编译，
// 首次启动且对应表为空时灌入知识库/话术/流程/功能入口。
// ASSIST_SEED 环境变量仍可指向外置文件（自定义初始数据），优先级见 cmd/assist-server。
// =============================================
package seed

import _ "embed"

// SeedJSON 初始数据（知识库/话术/流程/功能入口/配置）。
//
//go:embed seed.json
var SeedJSON []byte
