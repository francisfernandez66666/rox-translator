// ============ 本文件职责中文说明 ============
// 引擎并发与上下文辅助（2026-08-26 整改 D2/D4）：
//   - WithProgressCallback / progressCallbackFrom：阶段进度回调经 context 传递，
//     替代原 Engine.OnPhase 进程级单例字段（并发读改写竞争、回调串台）。
//   - recoverPipeline：文件/文本管线 goroutine 的 panic 兜底——这些 goroutine 不在
//     net/http 的连接级 recover 保护范围内，任一深处理 panic 会击穿整个进程，
//     任务滞留至租约超期。统一转日志，语言级失败由缺失段硬闸自然兜住。
//
// ========================================
package engine

import (
	"context"
	"log"
)

// phaseCtxKey 阶段回调 context 键（私有类型防跨包碰撞）
type phaseCtxKey struct{}

// WithProgressCallback 注入阶段回调（phase 取值如 "ai_generating"）。
func WithProgressCallback(ctx context.Context, cb func(phase string)) context.Context {
	if cb == nil {
		return ctx
	}
	return context.WithValue(ctx, phaseCtxKey{}, cb)
}

// progressCallbackFrom 读取阶段回调；未注入返回 nil。
func progressCallbackFrom(ctx context.Context) func(string) {
	cb, _ := ctx.Value(phaseCtxKey{}).(func(string))
	return cb
}

// recoverPipeline 管线 goroutine panic 兜底（整改 D4）。scope 用于日志定位
// （如 "file_kb:en"、"batch_chunk"、"chat_review:ja"）。
func recoverPipeline(scope string) {
	if r := recover(); r != nil {
		log.Printf("[engine] panic recovered in %s: %v", scope, r)
	}
}
