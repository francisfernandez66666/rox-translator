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
// 原子性：★ P1-12（2026-09-14）验证码「校验+消费+错计」经 vcodeLockOf per-key 互斥
// 串行化；Redis 多实例间仍为 Get+Set 弱一致（窗口极小，单码通常仅一次校验）。
// =============================================
package api

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"translator/internal/infra/redis"
)

// vcodeMu per-key 互斥锁（★ P1-12 修复 2026-09-14）：
// 「读 attempts → 判断 → 比对 → attempts++ 回写」是多步非原子流程，并发请求可全部
// 读到 attempts=0，在 10 分钟窗口内一次并发脉冲即近似穷举 6 位码空间（任意账号接管）。
// 验证码消费路径（改密码/邮箱码）以本函数取得的锁串行化 check-and-consume。
var vcodeMu sync.Map // key -> *sync.Mutex

// vcodeLockOf 取得（并惰性创建）指定 key 的互斥锁。返回解锁函数。
func vcodeLockOf(key string) func() {
	v, _ := vcodeMu.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// vcodeLocalFallback 无 Redis 时的进程内回退存储（★ P1-14 修复 2026-09-14）。
// 旧实现 vcodeSet 在 Redis 未启用时静默丢弃——但 SSO 兑换码（sso_xchg:*）没有
// 调用方本地 map 兜底，导致「单实例 + 无 Redis」部署下 SSO 登录整体失效。
// 现统一：Redis 不可用时写本进程 map（带 TTL），读 Redis 未命中回退 map，双删。
type vcodeEntry struct {
	val []byte
	exp time.Time
}

// vcodeLocal Redis 未启用时的本地验证码降级存储；vcodeLocalWrites 累计写入数（观测用）。
var vcodeLocal = sync.Map{} // key -> vcodeEntry
var vcodeLocalWrites int64

// vcodeLocalSet 写入内存验证码缓存（Redis 未启用时的本地降级存储）。
// 参数 key=校验键（邮箱/手机号哈希等）；val=序列化的校验载荷；ttl=有效期。
func vcodeLocalSet(key string, val []byte, ttl time.Duration) {
	vcodeLocal.Store(key, vcodeEntry{val: val, exp: time.Now().Add(ttl)})
	// 惰性清扫：每 256 次写全量清一次过期项，防无界膨胀
	if n := atomic.AddInt64(&vcodeLocalWrites, 1); n%256 == 0 {
		now := time.Now()
		vcodeLocal.Range(func(k, v any) bool {
			if e, ok := v.(vcodeEntry); ok && now.After(e.exp) {
				vcodeLocal.Delete(k)
			}
			return true
		})
	}
}

// vcodeLocalGet 读取内存验证码缓存；过期项视同不存在（并顺手删除）。
func vcodeLocalGet(key string) ([]byte, bool) {
	v, ok := vcodeLocal.Load(key)
	if !ok {
		return nil, false
	}
	e := v.(vcodeEntry)
	if time.Now().After(e.exp) {
		vcodeLocal.Delete(key)
		return nil, false
	}
	return e.val, true
}

// vcodeSet 写验证码到 Redis（未启用/故障回退进程内存，★ P1-14）。
// key=Redis 键（含前缀），val=JSON 字节，ttl=过期时长。
func vcodeSet(key string, val []byte, ttl time.Duration) {
	if rdb := redis.Get(); rdb != nil {
		_ = rdb.Set(context.Background(), key, string(val), ttl)
		return
	}
	vcodeLocalSet(key, val, ttl)
}

// vcodeGet 读验证码（Redis 优先，未启用回退进程内存）；命中返回 (字节, true)。
// Redis 已启用但未命中仍返回 false——由调用方回退各自本地 map（跨实例语义不变）。
func vcodeGet(key string) ([]byte, bool) {
	if rdb := redis.Get(); rdb != nil {
		if val, err := rdb.Get(context.Background(), key); err == nil && val != "" {
			return []byte(val), true
		}
		return nil, false
	}
	return vcodeLocalGet(key)
}

// vcodeDel 删验证码（消费/作废/超限销毁；Redis 与内存回退双删）。
func vcodeDel(key string) {
	if rdb := redis.Get(); rdb != nil {
		_ = rdb.Del(context.Background(), key)
		return
	}
	vcodeLocal.Delete(key)
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
