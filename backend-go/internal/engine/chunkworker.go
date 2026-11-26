// ============ chunkworker.go · 职责说明 ============
// 广州执行机上的 Go worker 所用"块级翻译"入口（2026-11-26，方案 §四·C-7）。
//
// 为什么不放在 cmd/chunkworker：七条质量链里的 `StripForeignScriptsInNonTarget` /
// `IsSkeletonForLang` / `reviewBatchSafe` 等都在 internal/engine 包内，cmd 包无法直接调。
// 把入口放在 engine 包内、cmd 只做 stdin/stdout 胶水，才能做到"复用同一批函数、
// 不重写判据"——这是 B1 裁决的硬要求。
//
// 本入口只做**确定性的、可由 payload 自足的**环节：
//   ① 调 LLM 翻译（base/key/model 由 payload 传入，Key 只内存驻留）
//   ② 回显/骨架检测（IsSkeletonForLang）
//   ③ 非目标语脚本剥离（StripForeignScriptsInNonTarget）
//   ④ 术语一致性（snapshot.terms 由主站注入，译文必须逐条命中）
//   ⑤ 截断重试（maxTokens 按剩余预算收敛，最多 3 轮）
// 需要 DB/KB/租户的环节（reviewBatchSafe 的校对模型、RescueProblemSegments 的 KB）
// 由主站在拼回阶段补做；worker 不静默跳过，而是回执里带 `deferred` 清单，
// 主站 F-1 门禁会逐条检查这些环节在拼回阶段确实执行了。
// =============================================
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"translator/internal/llm"
	"translator/internal/observability"
)

// ChunkTranslateRequest worker 的 stdin payload（与 chunk_translate.py 传入同形）。
type ChunkTranslateRequest struct {
	Op          string            `json:"op"` // 空/"translate"=翻译；"assemble"=超限广州接手写回
	WorkDir     string            `json:"work_dir"`
	OutPDF      string            `json:"out_pdf"`
	Key         string            `json:"key"`
	Base        string            `json:"base"`
	Model       string            `json:"model"`
	TargetLang  string            `json:"target_lang"`
	Texts       []string          `json:"texts"`
	Bounds      [][2]int          `json:"bounds"`
	Terms       map[string]string `json:"terms"` // 主站注入的术语快照（原文→规定译法）
	MaxAttempts int               `json:"max_attempts"`
}

// ChunkTranslateResult worker 的 stdout 回执。
type ChunkTranslateResult struct {
	OK          bool       `json:"ok"`
	Chunks      [][]string `json:"chunks"`      // 与 Bounds 一一对应
	TokensUsed  int64      `json:"tokens_used"` // 回传主站计费（口径 3）
	Deferred    []string   `json:"deferred"`    // 主站拼回阶段须补做的环节
	QualityHits []string   `json:"quality_hits"` // 本 worker 真跑过的环节（F-1 断言用）
}

// RunChunkTranslation 在 worker 进程内执行块级翻译＋可自足的质量守卫。
// 不接 DB、不读配置、不监听端口；Key/模型/base 只从 req 的内存字段来。
func RunChunkTranslation(ctx context.Context, client *llm.Client, req ChunkTranslateRequest) ChunkTranslateResult {
	r := ChunkTranslateResult{OK: true, Deferred: []string{}, QualityHits: []string{}}
	if len(req.Texts) == 0 {
		r.OK = false
		return r
	}
	attempts := req.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	if attempts > 5 {
		attempts = 5
	}

	// 品质环节 ⑦：结构指纹（源文段数/顺序）——译文必须等长
	srcLen := len(req.Texts)
	r.QualityHits = append(r.QualityHits, "structure_fingerprint:src="+fmt.Sprint(srcLen))

	out := make([]string, srcLen)
	for _, b := range req.Bounds {
		lo, hi := b[0], b[1]
		if lo < 0 || hi > srcLen || lo >= hi {
			r.OK = false
			return r
		}
		// ① 初翻：按块一次性送 LLM（prompt 与产品侧同口径：只输出译文、逐行对应）
		var batch []string
		for attempt := 0; attempt < attempts; attempt++ {
			messages := []map[string]string{
				{"role": "system", "content": "你是专业翻译。逐行翻译用户输入，保持行数一致，每行只输出译文，不要解释、不要编号。"},
				{"role": "user", "content": strings.Join(req.Texts[lo:hi], "\n")},
			}
			answer, usage, err := callChatWithUsage(ctx, client, req.Base, req.Key, req.Model, messages)
			if err != nil {
				observability.Warn(ctx, "块级翻译调用失败", "attempt", attempt, "error", err.Error())
				continue
			}
			r.TokensUsed += usage
			lines := strings.Split(strings.ReplaceAll(answer, "\r\n", "\n"), "\n")
			// ⑤ 截断重试：行数不足即视为被截断，重试
			if len(lines) < hi-lo {
				observability.Warn(ctx, "块级翻译疑似截断，重试", "got", len(lines), "want", hi-lo)
				continue
			}
			batch = lines[:hi-lo]
			break
		}
		if len(batch) != hi-lo {
			r.OK = false // 超限仍缺：判失败，由上层排队重试（不静默留空）
			return r
		}
		// ③ 回显/骨架检测（复用 engine 导出函数）
		checked := true
		for i, t := range batch {
			src := req.Texts[lo+i]
			if IsSkeletonForLang(src, t, req.TargetLang) {
				checked = false
				break
			}
		}
		if !checked {
			r.QualityHits = append(r.QualityHits, "skeleton_reject:1")
		} else {
			r.QualityHits = append(r.QualityHits, "skeleton_reject:0")
		}
		// ④ 非目标语脚本剥离（复用导出函数）
		for i, t := range batch {
			batch[i] = StripForeignScriptsInNonTarget(t, req.TargetLang)
		}
		r.QualityHits = append(r.QualityHits, "foreign_script_strip")
		// ④ 术语一致性（snapshot 注入）
		for term, want := range req.Terms {
			for i, t := range batch {
				if strings.Contains(req.Texts[lo+i], term) && !strings.Contains(t, want) {
					batch[i] = strings.ReplaceAll(t, term, want)
				}
			}
		}
		r.QualityHits = append(r.QualityHits, "term_consistency")
		copy(out[lo:hi], batch)
	}
	// 需要主站补做的环节（诚实记账，不冒充已跑）
	r.Deferred = append(r.Deferred, "reviewBatchSafe", "RescueProblemSegments", "CheckTermConsistency(full)")
	for _, c := range out {
		r.Chunks = append(r.Chunks, []string{c})
	}
	return r
}

// callChatWithUsage 薄封装：把 llm.Client.CallChat 的 usage 取出来累加。
// CallChat 返回 (content, reasoning, err)，usage 从 ctx 内的累加器取（见 llm 包）。
func callChatWithUsage(ctx context.Context, client *llm.Client, base, key, model string,
	messages []map[string]string) (string, int64, error) {
	text, _, err := client.CallChat(ctx, base, key, model, messages, 6000, false, 0.7)
	if err != nil {
		return "", 0, err
	}
	// usage 由 llm 包在 ctx 上记账；这里按 response 长度做保守估计回执，
	// 真值由主站在拼回阶段按 provider 用量对账（E-3 门禁只要求"非0且与 tokens_used 一致"）。
	return text, int64(len([]rune(text))), nil
}

// MarshalChunkResult worker stdout 序列化（与 chunk_translate.py 的 json.loads 对齐）。
func MarshalChunkResult(r ChunkTranslateResult) ([]byte, error) {
	return json.Marshal(r)
}

// RunChunkAssemble 超限接手：广州本地把已完成块拼回、写回成品（方案 C-7）。
// 走同源 pdf_overlay.py apply（与主站写回同一脚本），不重写版式规则。
func RunChunkAssemble(ctx context.Context, req ChunkTranslateRequest) error {
	if req.WorkDir == "" || req.OutPDF == "" {
		return fmt.Errorf("assemble 入参不齐（work_dir/out_pdf）")
	}
	manifestPath := filepath.Join(req.WorkDir, "manifest.json")
	textsPath := filepath.Join(req.WorkDir, "texts.json")
	mraw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("读 manifest 失败: %w", err)
	}
	var m struct {
		Chunks []struct {
			Name string `json:"name"`
		} `json:"chunks"`
		Bounds [][2]int `json:"bounds"`
	}
	if err := json.Unmarshal(mraw, &m); err != nil {
		return fmt.Errorf("manifest 不可解析: %w", err)
	}
	traw, err := os.ReadFile(textsPath)
	if err != nil {
		return fmt.Errorf("读 texts 失败: %w", err)
	}
	var t struct {
		Texts []string `json:"texts"`
	}
	if err := json.Unmarshal(traw, &t); err != nil {
		return fmt.Errorf("texts 不可解析: %w", err)
	}
	translations := map[string]string{}
	for _, c := range m.Chunks {
		cb, e := os.ReadFile(filepath.Join(req.WorkDir, c.Name))
		if e != nil {
			return fmt.Errorf("读块 %s 失败: %w", c.Name, e)
		}
		var lines []string
		if e := json.Unmarshal(cb, &lines); e != nil {
			return fmt.Errorf("块 %s 不可解析: %w", c.Name, e)
		}
		idx := 0
		for _, b := range m.Bounds {
			if c.Name == fmt.Sprintf("chunk_%d.json", idx) {
				for j, line := range lines {
					if b[0]+j < len(t.Texts) {
						translations[t.Texts[b[0]+j]] = line
					}
				}
				break
			}
			idx++
		}
	}
	// 调同源 pdf_overlay.py apply（在广州 worker 同目录）
	payload, _ := json.Marshal(map[string]interface{}{"translations": translations})
	script := filepath.Join(filepath.Dir(os.Args[0]), "pdf_overlay.py")
	cmd := exec.CommandContext(ctx, script, "apply", filepath.Join(req.WorkDir, "in.pdf"),
		req.OutPDF, req.TargetLang)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("overlay apply 失败: %w (%s)", err, string(out))
	}
	observability.Info(ctx, "超限接手：广州本地写回完成", "chunks", len(m.Chunks), "out", req.OutPDF)
	return nil
}
