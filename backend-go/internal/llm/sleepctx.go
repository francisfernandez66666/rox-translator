// ============ 本文件职责说明 ============
// ★ D9（2026-09-12）：可取消退避 sleep（ctx 断开立即返回，不再白烧 token）。
// =============================================
package llm

import (
	"context"
	"time"
)

// sleepCtx 等待 d；期间 ctx 取消则提前返回 false。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
