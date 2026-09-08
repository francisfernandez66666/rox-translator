// ============ vcode.go · 职责说明 ============
// api 包「验证码存储」统一抽象（2026-09-09 技术债①P1 横向扩容改造）：
// 忘记密码验证码（resetCodes）与邮箱验证码（emailCodes）从纯进程内存
// 改为「Redis 键值 + 内存兜底」双后端——Redis 启用时跨实例共享（任意实例
// 生成的码其他实例可校验），未启用或 Redis 故障时降级进程内存（单实例兼容）。
//
// 设计原则：本地 map（resetCodes.m / emailCodes.codes）是唯一内存事实来源，
// Redis 是跨实例共享镜像。读写顺序：
//   - 写：先写本地 map，再镜像到 Redis（Redis 失败静默，等同旧行为）。
//   - 读：先读 Redis（跨实例共享），未命中再读本地 map（单实例兜底）。
//   - 删：本地与 Redis 同时清理。
//
// 键带业务前缀（vcode:reset:<uid> / vcode:email:<key>），TTL 由调用方传入（秒）。
// 值 JSON 编码：resetCode{code,expires_at,attempts} / emailCode{code,expires_at,attempts,sent_at}。
// 原子性：验证码「校验+消费+错计」为读-改-写，Redis 下 Get+Set 读改写；
// 实例间竞争窗口极小（单码生命周期内通常仅一次校验），可接受的弱一致。
// =============================================
package api

import (
	"context"
	"time"

	"translator/internal/infra/redis"
)

// vcodeSet 写验证码到 Redis（未启用/故障静默失败；本地 map 由调用方维护）。
// key=Redis 键（含前缀），val=JSON 字节，ttl=过期时长。
func vcodeSet(key string, val []byte, ttl time.Duration) {
	if rdb := redis.Get(); rdb != nil {
		_ = rdb.Set(context.Background(), key, string(val), ttl)
	}
}

// vcodeGet 读验证码（Redis 优先）；命中返回 (字节, true)，未命中/未启用返回 (nil,false)。
// 调用方拿到结果后应回退本地 map（若 Redis 未命中）。
func vcodeGet(key string) ([]byte, bool) {
	if rdb := redis.Get(); rdb != nil {
		if val, err := rdb.Get(context.Background(), key); err == nil && val != "" {
			return []byte(val), true
		}
	}
	return nil, false
}

// vcodeDel 删验证码（消费/作废/超限销毁）。本地 map 由调用方维护。
func vcodeDel(key string) {
	if rdb := redis.Get(); rdb != nil {
		_ = rdb.Del(context.Background(), key)
	}
}

// vcodeResetKey 忘记密码验证码 Redis 键（vcode:reset:<uid>）。
func vcodeResetKey(uid int64) string { return "vcode:reset:" + itoaInt64(uid) }

// vcodeEmailKey 邮箱验证码 Redis 键（vcode:email:<lowerEmail>）。
func vcodeEmailKey(lowerEmail string) string { return "vcode:email:" + lowerEmail }

// itoaInt64 int64→string（本地薄封装，避免重复实现）。
func itoaInt64(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
