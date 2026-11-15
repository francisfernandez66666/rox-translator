// ============ outbox_wiring.go · 职责说明 ============
// 四期 W4 收口（2026-11-15，审计 §3.4）：把 outbox 框架**装到车上**。
//
// 旧形态：`NewRelay` / `NewConsumer` 只在测试里出现，watchdog 没有 relay 槽，
// `event_outbox` 恒空，而 E1–E6 全绿（组件行为正确、产品上零事件）。
//
// 本文件只做组合根的三件事：
//
//	① `InstallOutboxRelay(conn)` 由 cmd/server 在 NewServer 之后显式调一次，
//	   构造 queue 账本 → consumer → relay（**初始 enabled=false**，E6）；
//	② `CurrentOutboxRelay()` 供 watchdog 槽每轮现取（槽在 NewServer 内启动，
//	   组合根随后装配仍来得及，故不在启动时捕获指针）；
//	③ `outboxRelaySwitch()` 三级开关：env OUTBOX_RELAY > system_config.outbox_relay > 默认 off。
//
// ★ 关着时一行 event_outbox 都不碰（RelayOnce 立即返回，pending 原地积压）；
//
//	本轮**不摘** bridgeLeadCapturedToInbox 的直接写（双写窗口要留到 consumer 有现网读数后）。
//
// ================================================
package api

import (
	"database/sql"
	"os"
	"strings"
	"sync/atomic"

	"translator/internal/db"
	"translator/internal/outbox"
	"translator/internal/queue"
)

const (
	outboxRelayEnv        = "OUTBOX_RELAY"
	outboxRelayConfigKey  = "outbox_relay"
	outboxRelaySlotPeriod = "5s" // 仅用于日志/文档口径；真实 ticker 在 watchdog.go
)

var outboxRelayPtr atomic.Pointer[outbox.Relay]

// InstallOutboxRelay 组合根唯一装配入口（cmd/server/main.go 调用一次）。
// 参数：conn=event_outbox / queue 账本所在的业务库连接（s.Store.DB()）。
// 返回：无（conn 为 nil 时静默不装，health 面据此可见 relay 缺席）。
func InstallOutboxRelay(conn *sql.DB) {
	if conn == nil {
		return
	}
	d := db.CurrentDialect()
	q := queue.NewDirect(conn)
	consumer := outbox.NewConsumer(conn, d, nil)
	// ★ 初始恒关：开关只在 watchdog 槽里按 outboxRelaySwitch() 拨，
	//   绝不让「装配成功」等价于「已经开始投递」。
	outboxRelayPtr.Store(outbox.NewRelay(conn, d, q, consumer.Handle, false))
}

// CurrentOutboxRelay 取当前装配的 relay；未装配返回 nil。
func CurrentOutboxRelay() *outbox.Relay { return outboxRelayPtr.Load() }

// relaySwitchOn 把一档配置值翻成布尔；只认显式 on/1/true/yes，其余一律 off。
// ★ 不许「非空即开」：运营把值写成 "off" / "0" / "false" 时必须真的关掉。
func relaySwitchOn(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "1", "true", "yes":
		return true
	default:
		return false
	}
}

// outboxRelaySwitch 三级开关：env > system_config > 默认 off。
// ★ env 显式给了值（哪怕是 "0"）就短路，不再回落库配置——否则「用 env 关掉」
//
//	会被库里的 on 重新打开，正是 AGENTS §二「谁优先要问构造期锚点」那一族坑。
func (s *Server) outboxRelaySwitch() bool {
	if v, ok := os.LookupEnv(outboxRelayEnv); ok {
		return relaySwitchOn(v)
	}
	if s != nil && s.Store != nil {
		if v, err := s.Store.GetConfig(outboxRelayConfigKey); err == nil && strings.TrimSpace(v) != "" {
			return relaySwitchOn(v)
		}
	}
	return false // 默认关（E6）
}
