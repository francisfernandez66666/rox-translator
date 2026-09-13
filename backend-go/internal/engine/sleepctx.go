// ============================================================================
// engine/sleepctx.go — ★ D9（2026-09-12）可取消退避。
// 旧重试/降级链用裸 time.Sleep：客户端断开（ctx 已取消）后仍整段睡满并继续
// 发起 LLM 调用，白烧 token 且占并发额度。sleepCtx 睡满返回 true，
// ctx 先行取消返回 false，调用方据此立即终止重试。
// ============================================================================
package engine

import (
	"context"
	"os"
	"strconv"
	"strings"
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

// envPositiveInt 读取正整数环境变量（缺省/非法回退 def）。
func envPositiveInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
