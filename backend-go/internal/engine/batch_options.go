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
	// LiteralOCR 这一批段块来自**图片 OCR**（界面截图/扫描件），交付要求是「只直译、不发散」。
	//
	// ★ 2026-10-29 用户令：「翻译时，对于 ocr 结果，ai 只补全，不发散翻译」。
	// 现网实证：图上「战法」列的值本来就是英文键 `manual`，交付成了 `handbook`；
	// `卖出胜负`(4 字) 成了 "Sell the outcome of the competition"(35 字) ——
	// 前者是**把代码键意译成同义词**（已由 enforceLatinIdentifierVerbatim 机械收口），
	// 后者是**把 UI 短标签解释成一句散文**（这一档只能靠指令收，见 literalOCRNote）。
	//
	// 关掉（false）时提示词与改造前**逐字等价**、记忆键也不变（同 HeaderContext 那条纪律）。
	LiteralOCR bool
}

// tmContextHash 把「会改变译文的全部上下文」折成记忆键的那一个维度。
//
// ★ 为什么折成一维而不是给 tmScope 再加一个字段：`ctxHash` 在库里是**一列文本**
// （`translation_memory.context_hash`），加维度＝加迁移；而这一维的语义本来就是
// "system 前缀里那些不在源文里的东西"，表头与直译指令都属这一族。
// 两条不变式：① 两个开关都关时必须是 ""（与改造前的键逐字相同，历史命中不作废）；
// ② 只开直译档时也必须非空——否则图片件的"直译译文"会被当成同一段的正常译文复用。
func tmContextHash(opts BatchOptions) string {
	base := tmHeaderContextHash(opts.HeaderContext)
	if !opts.LiteralOCR {
		return base
	}
	if base == "" {
		return tmLiteralOCRMark
	}
	return base + tmLiteralOCRMark
}

// tmLiteralOCRMark 直译档进记忆键的标记（前导 `|` 保证与表头哈希的十六进制段不会粘连同形）。
const tmLiteralOCRMark = "|lit"

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
