// ============ shortcode.go · 职责说明 ============
// 通用「不可枚举短码」生成器：对外页两段 URL、邀请链接等需要「猜不到」的凭据共用一把尺子。
// 形态照 store/invites_link.go:40-46（crypto/rand 16 字节 → 32 位小写 hex ＋ 人眼可辨前缀）。
// ★ 随机源失败必须直接返回错误，绝不退化成时间戳／计数器等可猜测串：
//
//	那种「看起来生成成功了」的兜底会把不可枚举性抹掉，而调用方拿到的仍是一条能用的链接。
//
// ★ 本包属于基础能力层，禁止 import internal/store（避免循环依赖，AGENTS §一·3）。
// ================================================
package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// shortCodeEntropyBytes 短码熵字节数（16 字节＝128 bit，与现网邀请链接同档）。
const shortCodeEntropyBytes = 16

// NewCode 生成一枚不可枚举短码：prefix + 32 位小写 hex。
// 参数：prefix 为人眼可辨前缀，允许空串；返回码串与错误。
func NewCode(prefix string) (string, error) {
	var b [shortCodeEntropyBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成短码失败: %w", err)
	}
	return prefix + hex.EncodeToString(b[:]), nil
}
