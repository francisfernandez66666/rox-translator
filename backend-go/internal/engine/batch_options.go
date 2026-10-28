// ============ 本文件职责中文说明 ============
// 批量腿的可选上下文（★ 2026-10-28 P5：表头上下文）与其记忆键指纹。
//
// 为什么单开一个文件而不是塞进 engine.go：
//   - `BatchOptions` 是**对外类型**（file.go 三处批量调用点要构造它），放这里比塞进 3000 行的
//     engine.go 更容易被下一位找到；`tmHeaderContextHash` 与记忆键同源，必须和 tm_cache.go 相邻读。
//
// 三条口径（改这块前必读）：
//  1. **老签名一律薄委托**：`BatchTranslate`/`BatchTranslateMarked` 不改签名，内部传零值 options。
//     50+ 处调用点里只要有一处传错，就是"某条腿没有上下文"的静默不一致（不报错、只是译文差一点）。
//  2. **空表头必须与"没有这个特性"逐字等价**：`tmHeaderContextHash("") == ""`，
//     这样关闭 P5 时记忆键与改造前完全相同，不会把已有命中率打散。
//  3. **上下文只进 system 前缀，不进 user**：user 里是待译段，任何额外文本都会被模型当成
//     「也要翻译的内容」（同 batchContextNote 用英文写的理由）。
//
// ==========================================
package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// batchSplitMaxDepth 截断二分的深度上限（★ 2026-10-28 成本批）。
//
// 取 2 的理由：深度 d 时块宽为原块 /2^d，而 `finish_reason=length` 的成因是**输出总长超预算**；
// 深度 2 已把单块压到 1/4（典型自适应块 5~40 段 ⇒ 最细 2~10 段），再深就是"逐段翻译"
// ——那时逐段兜底腿（`singleLang` 自带截断重拨）本来就会接手，多一层二分只是多付管理成本。
// 调用上界＝1+2+4 个块，叶子再各重拨一次 ⇒ 每个原始块最坏 11 枪，仍受批量并发闸约束。
const batchSplitMaxDepth = 2

// BatchOptions 批量腿的可选上下文。
type BatchOptions struct {
	// HeaderContext 表头上下文（已按 FILE_HEADER_CONTEXT_MAX_CHARS 截断）；空＝无上下文。
	HeaderContext string
}

// tmHeaderContextHash 表头上下文进记忆键的指纹：空/纯空白 ⇒ ""（与"没有上下文"完全等价，
// 保证关闭 P5 时缓存键与改造前逐字相同）。
//
// ★ 哈希的是 **Trim 之后**的那一份，与 `headerContextNote` 真正拼进 prompt 的值必须逐字同源：
//
//	出栈那句用的是 `strings.TrimSpace(header)`（见 headerContextNote），所以"发出去的是什么"
//	就是 trim 过的；拿原始串算指纹等于给同一份 prompt 编两个指纹，
//	后果是同一篇文档换个抽取时刻（首尾空白抖一下）就整篇失效、命中率被空白打散。
//	将来改 headerContextNote 的取词形态，这里必须同步改（两处同尺，别各写一份）。
func tmHeaderContextHash(header string) string {
	h := strings.TrimSpace(header)
	if h == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(h))
	return hex.EncodeToString(sum[:8])
}
