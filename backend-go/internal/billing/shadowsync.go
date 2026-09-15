// ============ shadowsync.go · 职责说明 ============
// billing 包内部实现文件（★ P1 多实例闭环 2026-09-15，见《P0P2待办核实报告_20260915.md》）。
//
// 影子余额跨实例失效广播：
//   背景：UsageSink 的影子余额（shadow）是进程内存缓存，多实例部署下 A 实例充值后
//   调用 InvalidateShadow 仅清 A 进程缓存，B 实例影子仍是旧值（甚至负化）→
//   误触发「余额不足」中止在途翻译——正是单实例时代已修复 bug（sink.go P2-2）的多实例版。
//
// 方案（Redis 失效信号列表，尽力而为 best-effort）：
//   - 广播：InvalidateShadow → 本进程清影子 + Redis 启用时 RPUSH "billing:shadow_invalidate" <tid>；
//   - 订阅：InitGlobalSink 时若 Redis 启用，启动后台 goroutine BLPOP 循环消费该列表，
//     收到 tid 即清本进程影子（下次 Record 重新 seed 自 DB）；
//   - 语义边界：多实例下影子最多滞后「广播间隔 + flush 周期(默认2s)」；
//     flush 每周期每租户回读真实余额自愈（sink.go），误中止窗口被收窄到与
//     单实例「余额变动到下次刷新」同级的固有延迟，不引入新的资金风险；
//   - 降级：Redis 未启用（单实例标准形态）不广播不订阅，行为与现状完全一致；
//     BLPOP 出错时退避 1s 重试，Redis 故障不影响计量主链路（Record/flush 均不依赖本组件）。
//
// 信号积压安全性：实例宕机期间积压的失效信号会被存活实例消费——多清几次影子
// （多触发几次 seed DB 读）无害，不需要精确去重。
// =============================================

package billing

import (
	"context"
	"log"
	"strconv"
	"time"

	"translator/internal/infra/redis"
)

// shadowInvalidateKey 影子余额失效信号的 Redis 列表键（RPUSH/BLPOP 对）。
const shadowInvalidateKey = "billing:shadow_invalidate"

// broadcastShadowInvalidate 向全部实例广播「租户影子余额已失效」信号。
// Redis 未启用或广播失败均静默返回（尽力而为；本进程影子已由 Invalidate 清除）。
// 参数：tid=租户 ID。
func broadcastShadowInvalidate(tid int64) {
	r := redis.Get()
	if r == nil {
		return // 单实例形态：无需跨实例广播
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := r.RPush(ctx, shadowInvalidateKey, strconv.FormatInt(tid, 10)); err != nil {
		// 广播失败不重试：其余实例最迟在下一次余额变动/flush 自愈时收敛
		log.Printf("[shadowsync] 影子失效广播失败 tid=%d（其余实例将经 flush 自愈收敛）: %v", tid, err)
	}
}

// startShadowInvalidateWatcher 启动跨实例失效信号订阅循环（InitGlobalSink 内调用一次）。
// Redis 未启用直接返回（无跨实例语义）；BLPOP 常驻阻塞（1s 超时轮询防连接悬挂），
// 收到信号即清除本进程对应租户的影子缓存（含 shadowOk 标记，下次 Record 重新 seed）。
func startShadowInvalidateWatcher(s *UsageSink) {
	r := redis.Get()
	if r == nil {
		return
	}
	go func() {
		log.Println("[shadowsync] 影子余额跨实例失效订阅已启动（Redis）")
		for {
			select {
			case <-s.stop: // 进程退出：随 flusher 一同结束
				return
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			val, err := r.BLPop(ctx, shadowInvalidateKey, 900*time.Millisecond)
			cancel()
			if err != nil {
				continue // 超时（无信号）或连接抖动：循环重试
			}
			tid, perr := strconv.ParseInt(val, 10, 64)
			if perr != nil || tid <= 0 {
				continue // 非法信号：丢弃（防御性，正常写入方只产 tid 数字串）
			}
			// 收到他实例广播：清本进程影子 → 下次 Record 重新从 DB seed 真实双桶余额
			s.Invalidate(tid)
		}
	}()
}
