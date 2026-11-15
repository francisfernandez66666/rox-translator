// ============ outbox_wiring_test.go · 职责说明 ============
// W4 收口：relay 三级开关（env > system_config > 默认 off）的行为锁。
// 用普通 _test.go 命名（不是家族级 _gate_test.go），因为它测的是本批新增装配的
// 开关语义，不进 gate_mutants 台账；E6「默认关」的家族级锁仍在 outbox/relay_off_gate_test.go。
// ================================================
package api

import (
	"os"
	"testing"
)

// unsetRelayEnv 清掉 env（os.LookupEnv 判「有没有这把键」，t.Setenv 造不出「没有」）。
func unsetRelayEnv(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv(outboxRelayEnv)
	if err := os.Unsetenv(outboxRelayEnv); err != nil {
		t.Fatalf("清 env 失败: %v", err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(outboxRelayEnv, old)
		} else {
			_ = os.Unsetenv(outboxRelayEnv)
		}
	})
}

func TestOutboxRelaySwitchDefaultsOff(t *testing.T) {
	s := leadInboxHarness(t)
	unsetRelayEnv(t)
	if s.outboxRelaySwitch() {
		t.Fatal("E6 违例：未给任何配置时 relay 竟然是开的（默认必须关，pending 只积压不丢）")
	}
}

func TestOutboxRelaySwitchEnvWinsOverDB(t *testing.T) {
	s := leadInboxHarness(t)
	unsetRelayEnv(t)
	if err := s.Store.SetConfig(outboxRelayConfigKey, "on"); err != nil {
		t.Fatalf("写 system_config 失败: %v", err)
	}
	if !s.outboxRelaySwitch() {
		t.Fatal("库配置 on 未生效（三级开关的第二级断了）")
	}
	// ★ env 显式给 "0" 必须短路，不许被库里的 on 重新打开。
	t.Setenv(outboxRelayEnv, "0")
	if s.outboxRelaySwitch() {
		t.Fatal("env 显式关被库配置覆盖（AGENTS §二「谁优先」那一族坑）")
	}
	t.Setenv(outboxRelayEnv, "on")
	if !s.outboxRelaySwitch() {
		t.Fatal("env 显式开未生效")
	}
}
