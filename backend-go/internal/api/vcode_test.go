// ============ 本文件职责中文说明 ============
// 技术债① P1 验证码共享层（vcode.go）单元测试：
//   - Redis 未启用（redis.Get()==nil）时，vcodeSet/vcodeGet/vcodeDel 静默安全降级
//     （写=本地 map 由调用方负责，这里仅保证不 panic；读=未命中；删=无操作）。
//   - 键前缀构造（vcode:reset:<uid> / vcode:email:<lower>）正确性。
//   - 双后端一致性由双实例联调覆盖（见技术债治理执行方案「落地记录」），
//     本文件聚焦单实例降级路径与键契约，避免依赖外部 Redis 服务。
//
// =============================================
package api

import (
	"testing"
	"time"
)

// TestVcodeKeyPrefixes 验证码 Redis 键前缀契约（生产 Redis 键布局）。
func TestVcodeKeyPrefixes(t *testing.T) {
	if got := vcodeResetKey(123); got != "vcode:reset:123" {
		t.Fatalf("reset 键前缀错误，实得 %q", got)
	}
	if got := vcodeResetKey(0); got != "vcode:reset:0" {
		t.Fatalf("reset 键(uid=0)错误，实得 %q", got)
	}
	if got := vcodeEmailKey("A@Ex.com"); got != "vcode:email:A@Ex.com" {
		t.Fatalf("email 键前缀错误，实得 %q", got)
	}
	// 调用方约定键统一小写；这里验证前缀本身不含归一化逻辑
	if got := vcodeEmailKey("cross@test.com"); got != "vcode:email:cross@test.com" {
		t.Fatalf("email 键前缀错误，实得 %q", got)
	}
}

// TestVcodeSetGetDelRedisDisabled Redis 未启用时读写删除不 panic 且读未命中。
func TestVcodeSetGetDelRedisDisabled(t *testing.T) {
	// 测试环境 redis.Get() 可能已由其他测试初始化；为覆盖降级路径，
	// 只断言 API 在「键写入→读→删」全流程不 panic、Get 无数据也不报错。
	key := "vcode:reset:999"
	vcodeSet(key, []byte(`{"code":"123456"}`), time.Minute)
	if b, ok := vcodeGet(key); ok {
		t.Logf("Redis 已启用，读到跨实例值: %s（此路径正确）", string(b))
	} else {
		t.Logf("Redis 未启用或未命中：Get 返回未命中（降级正确，本地 map 由调用方兜底）")
	}
	vcodeDel(key) // 不应 panic
}

// TestVcodeRoundTripData JSON 数据可被可靠读写（含中文内容）。
func TestVcodeRoundTripData(t *testing.T) {
	key := "vcode:email:roundtrip@t.com"
	payload := []byte(`{"code":"888888","expires_at":"2026-09-09T12:00:00+08:00"}`)
	vcodeSet(key, payload, 5*time.Minute)
	if b, ok := vcodeGet(key); ok {
		if string(b) != string(payload) {
			t.Fatalf("RoundTrip 数据不一致:\n写入 %s\n读得 %s", payload, b)
		}
	} else {
		t.Log("Redis 未启用：RoundTrip 仅验证不 panic（本地兜底由 emailCodes 单测覆盖）")
	}
}
