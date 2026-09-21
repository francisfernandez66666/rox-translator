// ============ engine/vector.go · 职责说明 ============
// 分级召回第 3 级：可插拔向量召回（embedding 余弦相似度），★ 默认关闭。
//
// 背景（报告 §4.3 能力缺口 / §6 P3#21）：第 1/2 级都是字面手段，跨词表的语义
// 改写（「翻出来的效果不好能重做吗」→ 质量兜底知识、「公司要用多人协同」→ 企业版）
// 只能靠 embedding。主服务翻译侧已有 pgvector 口径（internal/kb VectorSearch），
// 但 assist 是独立二进制 + 独立 SQLite，不能复用其表，故本文件在 assist 内自含：
//   - 嵌入调用：复用 assist 现有 OpenAI 兼容 LLM 客户端（llm.Client.Embed，
//     与 chat 同 base_url/api_key，模型名走 configs.embed_model）；
//   - 向量索引：进程内缓存（SQLite 无向量索引，条量为销售知识规模，线性点积足够）；
//   - 触发时机：仅当第 1+2 级检索不足时按需构建/查询，不预热、不阻塞启动。
//
// 开关与降级（硬性要求，WHY）：
//   - configs.embed_recall 非 "on"（默认，库内不存在该键即关）或 embed_model 未配
//     或 LLM provider 不可用 → 整级完全绕过，零网络调用，生产未配置时行为与
//     第 2 级方案完全一致；
//   - 嵌入调用失败（超时/HTTP 错误/维度不符）→ observability.Warn 后回落第 2 级，
//     绝不把错误抛给前端；并进入 60s 冷却，避免每次对话都撞一次挂死的嵌入端点；
//   - 相似度阈值取保守值（见 vecMinCosine），低于阈值的条目不参与，
//     命中分数仍低于第 2 级（entry 打分注释），语义召回错误时最多退化为兜底引导。
//
// =============================================
package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"translator/internal/observability"
)

// ---------- 第 3 级参数 ----------
const (
	vecMinCosine    = 0.60 // 余弦相似度下限：宁缺勿滥（bge-m3 同主题中文对通常 ≥0.65，无关对 <0.4）
	vecMaxEntries   = 2    // 向量通道单请求最多注入条数（素材位让给字面命中）
	vecEmbedTimeout = 8 * time.Second
	vecFailCooldown = 60 * time.Second // 嵌入失败后的熔断窗口，防每轮对话重复挂 8s
	vecScoreBase    = 4                // 向量-only 条目分数基数（低于任何精确命中 10，也低于第 2 级 8）
)

// vectorEnabled 第 3 级是否启用：embed_recall=on 且 embed_model 非空且 LLM 可用。
// WHY 读 ensureLLM 而非单独配 embed base_url/key：嵌入与对话共用同一供应商凭证，
// 少一个配置面就少一处密钥泄露点；OpenAI 兼容网关（硅基流动等）chat 与
// embeddings 本就同 base 同 key。
func (e *Engine) vectorEnabled(ctx context.Context) bool {
	if strings.TrimSpace(e.db.GetConfig("embed_recall", "")) != "on" {
		return false
	}
	if strings.TrimSpace(e.db.GetConfig("embed_model", "")) == "" {
		return false
	}
	c := e.ensureLLM(ctx)
	return c != nil && c.Enabled()
}

// ensureVectorIndex 惰性构建/复用知识向量索引（按 embed_model + kb 内容指纹失效重建）。
// 失败返回 error（调用方负责降级），不 panic、不写脏缓存。
func (e *Engine) ensureVectorIndex(ctx context.Context) error {
	model := strings.TrimSpace(e.db.GetConfig("embed_model", ""))
	rows, err := e.db.List("kb_entries", true)
	if err != nil {
		return fmt.Errorf("kb 列表读取失败: %w", err)
	}
	var fpb strings.Builder
	fpb.WriteString(model)
	texts := make([]string, 0, len(rows))
	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		key := asStr(r["key"])
		title := asStr(r["title"])
		kw := asStr(r["keywords"])
		content := asStr(r["content"])
		if len([]rune(content)) > 120 {
			content = string([]rune(content)[:120])
		}
		// 嵌入文本 = 标题+关键词+内容摘要：贴近用户提问形态（问句对问文档正文效果差）
		texts = append(texts, title+"。"+kw+"。"+content)
		keys = append(keys, key)
		fmt.Fprintf(&fpb, "\x00%s\x00%s", key, asStr(r["updated_at"]))
	}
	fp := fpb.String()

	e.vecMu.Lock()
	defer e.vecMu.Unlock()
	if e.vecFP == fp && len(e.vecKeys) == len(keys) {
		return nil // 指纹未变，复用缓存
	}
	if now := time.Now(); now.Before(e.vecCoolUntil) {
		return fmt.Errorf("嵌入端点冷却中") // 上次失败未出窗口，本次直接降级
	}
	cctx, cancel := context.WithTimeout(ctx, vecEmbedTimeout)
	defer cancel()
	vecs, err := e.ensureLLM(cctx).Embed(cctx, model, texts)
	if err != nil {
		e.vecCoolUntil = time.Now().Add(vecFailCooldown)
		return err
	}
	if len(vecs) != len(keys) {
		e.vecCoolUntil = time.Now().Add(vecFailCooldown)
		return fmt.Errorf("嵌入返回条数 %d 与知识条数 %d 不符", len(vecs), len(keys))
	}
	// 全部就绪才换缓存：失败/半途不污染旧索引
	e.vecModel, e.vecFP, e.vecKeys, e.vecMat = model, fp, keys, vecs
	observability.Info(ctx, "assist.engine 向量索引已构建", "model", model, "entries", len(keys))
	return nil
}

// vectorRecall 对 input 做嵌入并按余弦取 top（vecMaxEntries 条、阈值 vecMinCosine）。
// 任何不可用/失败情形返回 nil（= 本请求绕过第 3 级，回落第 2 级），并 Warn 留痕。
func (e *Engine) vectorRecall(ctx context.Context, input string) []vecHit {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	if err := e.ensureVectorIndex(ctx); err != nil {
		observability.Warn(ctx, "assist.engine 向量召回不可用，回落字面召回", "err", err)
		return nil
	}
	model := strings.TrimSpace(e.db.GetConfig("embed_model", ""))
	cctx, cancel := context.WithTimeout(ctx, vecEmbedTimeout)
	defer cancel()
	qv, err := e.ensureLLM(cctx).Embed(cctx, model, []string{input})
	if err != nil || len(qv) == 0 {
		e.vecMu.Lock()
		e.vecCoolUntil = time.Now().Add(vecFailCooldown)
		e.vecMu.Unlock()
		observability.Warn(ctx, "assist.engine 问句嵌入失败，回落字面召回", "err", err)
		return nil
	}
	q := qv[0]

	e.vecMu.Lock()
	keys, mat := e.vecKeys, e.vecMat
	e.vecMu.Unlock()

	// 线性余弦：向量已由 Embed 侧 L2 归一，点积即余弦
	type scored struct {
		key string
		sim float64
	}
	var top []scored
	for i, v := range mat {
		if len(v) != len(q) {
			continue // 维度不一致的历史缓存：跳过（重建指纹后即消失）
		}
		dot := 0.0
		for j := range q {
			dot += float64(q[j]) * float64(v[j])
		}
		if dot >= vecMinCosine {
			top = append(top, scored{keys[i], dot})
		}
	}
	// 按相似度降序截断（条量小，插入排序级别开销）
	for i := 1; i < len(top); i++ {
		for j := i; j > 0 && top[j].sim > top[j-1].sim; j-- {
			top[j], top[j-1] = top[j-1], top[j]
		}
	}
	if len(top) > vecMaxEntries {
		top = top[:vecMaxEntries]
	}
	out := make([]vecHit, 0, len(top))
	for _, t := range top {
		out = append(out, vecHit{key: t.key, sim: t.sim})
	}
	return out
}

// vecHit 向量通道单条命中
type vecHit struct {
	key string
	sim float64
}
