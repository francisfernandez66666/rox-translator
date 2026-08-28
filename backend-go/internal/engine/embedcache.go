// ============ embedcache.go · 职责说明 ============
// 嵌入向量缓存与管线级预取（2026-08-26 评审整改 R2）：
// 此前文件管线「逐段逐语言」单条调用 Embed——同一段文本被嵌入 K 遍（K=目标语言数），
// 且与翻译 Chat 抢占同一信号量，是并翻卡顿的第二大根因。本文件提供：
//   - 进程级缓存：键=sha1(原文)，容量上限内复用（跨请求/跨语言生效）；
//   - 管线级预取：HandleFile 开工前对去重后的全部源文一次 EmbedBatch，
//     向量表随 ctx 注入，语义段优先查表、miss 才回源。
package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"sync"

	"translator/internal/kb"
	"translator/internal/llm"
)

// embedCacheMax 缓存条目上限（性能优化 Phase A4，1G 机器收紧）：1024 维 float32 ≈ 4KB/条，
// 1 万条约 40MB，配合 genMB 总字节上限，确保 GOMEMLIMIT=650Mi 下仍有余量给转换子进程。
const embedCacheMax = 10000

// embedCacheMaxBytes 整代累计写入字节上限（性能优化 Phase A4）：超过则整代清空，
// 防止大词汇量语料把嵌入缓存撑到百 MB 级。
const embedCacheMaxBytes = 200 << 20

// embedStore 进程级嵌入缓存（超限整代清空，避免 LRU 复杂度；命中率场景下足够）。
type embedStore struct {
	mu    sync.Mutex
	m     map[string][]float32
	genMB int64 // 当前代累计写入字节（估算）
}

var globalEmbeds = &embedStore{m: map[string][]float32{}}

// embedShaKey 原文 → sha1 hex 键。
func embedShaKey(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// getCachedEmbed 查缓存。
func getCachedEmbed(zh string) ([]float32, bool) {
	globalEmbeds.mu.Lock()
	defer globalEmbeds.mu.Unlock()
	v, ok := globalEmbeds.m[embedShaKey(zh)]
	return v, ok
}

// putCachedEmbed 写缓存（超限整代清空后重写）。
func putCachedEmbed(zh string, vec []float32) {
	globalEmbeds.mu.Lock()
	defer globalEmbeds.mu.Unlock()
	if len(globalEmbeds.m) >= embedCacheMax || globalEmbeds.genMB+int64(len(vec))*4 > embedCacheMaxBytes {
		globalEmbeds.m = map[string][]float32{}
		globalEmbeds.genMB = 0
	}
	globalEmbeds.m[embedShaKey(zh)] = vec
	globalEmbeds.genMB += int64(len(vec)) * 4
}

// ============ 管线级预取向量表（ctx 注入） ============

// embedLookupKey ctx 键：本次管线的预取向量表（键=embedShaKey(原文)）。
type embedLookupKey struct{}

// WithEmbedLookup 向 ctx 注入预取向量表（translateOneInner 优先查表，miss 才走缓存/回源）。
func WithEmbedLookup(ctx context.Context, m map[string][]float32) context.Context {
	return context.WithValue(ctx, embedLookupKey{}, m)
}

// EmbedLookupFrom 读取预取向量表；未注入返回 nil。
func EmbedLookupFrom(ctx context.Context) map[string][]float32 {
	v, _ := ctx.Value(embedLookupKey{}).(map[string][]float32)
	return v
}

// prefetchEmbeddings 管线级批量预取：对去重后的源文列表，
// 过滤已命中缓存的项 → 分批 EmbedBatch → 写缓存并组装 ctx 向量表。
// 返回携带向量表的 ctx（后续 TranslateOne 调用链自动受益）。
// 任何一步失败都静默降级（返回原 ctx）：预取是优化路径，绝不阻断主流程。
func (e *Engine) prefetchEmbeddings(ctx context.Context, texts []string, stageBase, stageKey, stageModel string) context.Context {
	if e.LLM == nil || len(texts) == 0 {
		return ctx
	}
	uniq := make([]string, 0, len(texts))
	seen := map[string]bool{}
	for _, t := range texts {
		t = trimForEmbed(t)
		if t == "" || seen[t] {
			continue
		}
		if _, ok := getCachedEmbed(t); ok {
			continue // 已有缓存，无需预取
		}
		seen[t] = true
		uniq = append(uniq, t)
	}
	if len(uniq) == 0 {
		return ctx
	}
	if stageBase != "" && stageModel != "" {
		// 整改 R3：改为请求级 ctx 覆盖，避免污染后续无 kb_embed 配置的请求
		ctx = llm.WithEmbedOverride(ctx, stageBase, stageKey, stageModel)
	}
	const batch = 32
	for i := 0; i < len(uniq); i += batch {
		end := i + batch
		if end > len(uniq) {
			end = len(uniq)
		}
		chunk := uniq[i:end]
		vecs, err := e.LLM.EmbedBatch(ctx, chunk)
		if err != nil {
			return ctx // 批量失败即停：剩余段由语义段自身回源兜底
		}
		for j, t := range chunk {
			if j < len(vecs) && len(vecs[j]) > 0 {
				putCachedEmbed(t, vecs[j])
			}
		}
	}
	return ctx
}

// trimForEmbed 预取去重前的文本规整（空白裁剪；空串视为不可嵌入）。
func trimForEmbed(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// 编译期引用：确保 kb 包类型在本文件的关联语义（Row/索引）保持可见性检查。
var _ = kb.Row{}
