// ============ ocr_reconcile.go · 职责说明 ============
// OCR 文本 AI 判定与补偿（主方案 §8）：只吃清洗层标出的 uncertain/quarantine 行，
// 在**封闭候选集 + 距离闸**上做三选一裁决：fix / keep / not_text。
//
// 为什么放在 engine 而不是 fileproc：fileproc 是基础包（不 import store、不持有模型路由/计费），
// 而本腿要用 stage_models、计入预估与实扣、按阶段记失败——这些全在 engine（包边界要求，不是出境要求）。
//
// 四条硬口径：
//  1. **只送待判行**：clean 行一条都不送（省成本、省风险）；dup 已在清洗层闭环；
//  2. **采纳闸**：fix 只许"候选集命中"或"改一个字符"（且字符集不变、数字不变、conf<0.6）；
//     not_text 只许在 glyph_set/geom_anomaly 且长度 ≤2 时成立——其余一律降级 keep；
//  3. **fail-open 全档 keep**：不可用/超时/解析失败/未过闸一律保留原识别，绝不因 AI 掉线丢内容；
//  4. **无重试**：同一模型对同一形态答案恒定，重拨只是重复扣费。
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/textscript"
)

// OCR_FIX 的预算与上限（改这些必须同步主方案 §8.3 与单测）。
const (
	// ★ 2026-10-29 二次收敛：60 行/次 → **20 行/次**、2 次 → 4 次。
	//   现网实证：58 个候选塞进一枪，15s 预算下 **tokens=0 全 timeout**（模型一个字都没回）——
	//   载荷越大首包越慢，抬超时治不了本。切小批后每次请求的输入/输出都小一个量级，
	//   总覆盖行数反而不降（20×4=80 ≥ 旧的 60×2=120？不，旧的 120 更大，但旧的整批超时=实际 0 行）。
	ocrFixMaxLinesPerCall = 20
	ocrFixMaxCalls        = 4
	// ocrFixCallTimeoutDefault 单次调用默认超时；`OCR_FIX_TIMEOUT_SEC` 可覆盖（钳 2~60s）。
	// ★ 2026-10-29 由 8s 抬到 15s：现网一单 17 个候选**全部** timeout（模型一次要判 17 行，
	//   跨境 + 推理模型的首包延迟就吃掉 8s），等于这条腿对客户完全没生效。
	//   抬到 15s 保住"一次工单不超过半分钟"的总等待（总预算=两次单调用=30s），
	//   同时把上限放到 60s 供运维按链路状况显式放宽。
	ocrFixCallTimeoutDefault = 15 * time.Second
	// ocrFixTotalBudgetDefault 整份文档的 ③ 总预算（默认＝三次单调用＝45s；
	// 用 3 而不是 4 是给客户等待封顶：19~40 行（2~3 批）能跑完，80 行那种大图第 4 批留给下次。
	ocrFixTotalBudgetDefault = 45 * time.Second
	ocrFixMaxPayloadBytes    = 64 << 10
	// ocrFixMaxTokens 单次回执的 max_tokens：60 行 × 每行约 12 token（含 JSON 骨架）留一倍余量。
	ocrFixMaxTokens = 1600
)

// ocrFixCallTimeout 单次 ③ 调用的墙钟预算：env `OCR_FIX_TIMEOUT_SEC`（默认 15s，钳 2~60s）。
//
// 为什么钳位：小于 2s 在跨境链路上几乎必然超时（等于把这条腿关掉却仍记调用），
// 大于 60s 会把一次图片工单的整体等待拖成分钟级——两者都属于"配置写错"，按安全档折回。
func ocrFixCallTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv("OCR_FIX_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 2 {
				n = 2
			}
			if n > 60 {
				n = 60
			}
			return time.Duration(n) * time.Second
		}
	}
	return ocrFixCallTimeoutDefault
}

// ocrFixTotalBudget 整份文档的总预算＝三次单调用（等待封顶；批次上限另由 ocrFixMaxCalls 管）。
func ocrFixTotalBudget() time.Duration { return 3 * ocrFixCallTimeout() }

// OcrReconcileStats 一次 ③ 的读数（日志与 GateWarnings 的唯一来源，见主方案 §4.5）。
type OcrReconcileStats struct {
	Candidates int // 送进 ③ 的行数
	Calls      int // 实际拨出次数（≤ ocrFixMaxCalls）
	Fixed      int // 采纳 fix 的行数
	Kept       int // 明确 keep 的行数
	NotText    int // 采纳 not_text（最终丢弃）的行数
	Fallback   int // 因不可用/超时/解析失败/未过闸而按 keep 处理的行数
	Unclear    int // low_conf_hard 且未拿到 fix 的行数（存疑读数，不改变交付）
	Skipped    int // 超出预算未送审的行数（fail-open keep）
	DocFixed   int // 由**确定性**文档内佐证修掉的行数（不经模型，见 fixDocTruncations）
	// Reasons 失败原因计数：off/no_model/no_stage_model/timeout/cancelled/upstream/schema/
	// dup_index/truncated/budget。**这些词是对外排障契约**，逐字钉在单测里。
	Reasons   map[string]int
	Tokens    int64 // 本次 ③ 的真实 token（内部成本读数，不外发）
	LatencyMs int64 // 本次 ③ 的墙钟总耗时（排障读数：区分"模型慢"与"没拨通"）
}

// OcrFixEnabled 读 kill-switch：`OCR_FIX=off|0|false` 关闭，其余（含空）开启。
//
// 关掉是**配置面回滚**（改 env + 重启进程），不需要换二进制——这是主方案 §8.8 的口径。
func (e *Engine) OcrFixEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("OCR_FIX")))
	return v != "off" && v != "0" && v != "false"
}

// OcrFixHealth 健康面状态词：off（主动关）/ degraded（没有能干活的人）/ backoff（零产出冷却中）/ ok。
//
// 两个词必须分开：degraded 是"该有人去配"，off 是"正常态"。写成同一个词，
// 现网就会出现"运营以为关了、其实是坏的"（主方案 §8.8）。
//
// ★ 2026-10-29：ok 的判据收紧到**显式配了 `ocr_fix` 阶段模型**（stageActive）。
// 回落全局主模型不再算 ok——现网全局是翻译模型，它答不了这份判定题，六次运行全是
// `tokens:0 / timeout`，而健康面一直写着 ok（读数与事实相反）。没有阶段模型 = degraded。
func (e *Engine) OcrFixHealth(ctx context.Context) string {
	if !e.OcrFixEnabled() {
		return "off"
	}
	// 防御零值引擎（健康面会在启动早期被拨；自己 panic 把服务打挂是本仓明令禁止的形态）。
	if e == nil || e.Cfg == nil || e.LLM == nil {
		return "degraded"
	}
	// ★ 熔断窗口单独一个词：`backoff` 的动作是**等**（模型慢/不答，冷却到期自己会再试），
	//   `degraded` 的动作是**去配**（这一档压根没有能干活的人）。混成一个词，读的人会对错病因下手。
	if e.ocrFixCooling() {
		return "backoff"
	}
	b, k, m, stageActive := e.ocrFixStageModel(ctx)
	if !stageActive {
		return "degraded"
	}
	if strings.TrimSpace(b) != "" && strings.TrimSpace(k) != "" && strings.TrimSpace(m) != "" {
		return "ok"
	}
	return "degraded"
}

// ReconcileOcrDoc 对一份 OcrDoc 跑 AI 判定，并把采纳结果写回 Final。
//
// 调用点：engine/file.go（文件链）与 api/chat_image.go（即时图片翻译），都在 CommitOcrDoc 之前。
// 失败绝不返回 error 让整单失败——一次模型抖动变成"客户文档大面积丢行"是本方案最贵的错误方向。
func (e *Engine) ReconcileOcrDoc(ctx context.Context, doc *fileproc.OcrDoc) OcrReconcileStats {
	st := OcrReconcileStats{Reasons: map[string]int{}}
	if doc == nil || len(doc.Lines) == 0 {
		return st
	}
	pending := make([]int, 0, len(doc.Lines))
	for i := range doc.Lines {
		ln := &doc.Lines[i]
		if strings.TrimSpace(ln.Final) == "" {
			continue // dup 已在清洗层闭环（Final 为空 = 不交付）
		}
		// ★ 2026-10-29 用户令「该翻就全部翻」：③ 的射程从「隔离行」扩到**全部有可译内容的行**。
		// 旧口径只送 low_conf/column_mode/glyph_set/geom_anomaly 判过的行，高置信错字（现网 `manua`、
		// 把 `l` 吃掉的整类）永远进不来——那条腿看着是绿的，实际对客户最常见的错字完全无感。
		// 不含字母的行（纯数字/符号/单非汉字）不送：它们没有"错字"可修，
		// 把开关误读出的 `0` 送进去只会被模型改写成别的字（现网真踩，见 RenderSafeTranslations 那条）。
		if !ocrFixableLine(ln.Text) {
			st.Skipped++
			continue
		}
		pending = append(pending, i)
	}
	st.Candidates = len(pending)
	if len(pending) == 0 {
		return st
	}
	// 排队优先：quarantine 先于 uncertain 先于 clean（越可疑越先送，预算不够时先保要紧的），
	// 组内按阅读顺序。三档都用 rank 表达，别再用"是不是 quarantine"的布尔（加第三档时布尔就分不出来了）。
	rank := func(i int) int {
		switch doc.Lines[i].Verdict {
		case fileproc.OcrVerdictQuarantine:
			return 0
		case fileproc.OcrVerdictUncertain:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(pending, func(a, b int) bool {
		ra, rb := rank(pending[a]), rank(pending[b])
		if ra != rb {
			return ra < rb
		}
		return doc.Lines[pending[a]].Index < doc.Lines[pending[b]].Index
	})
	// finish 统一收尾：存疑读数与日志在**所有**返回路径上都要算——
	// 否则"AI 关了"那一档永远报 0 条存疑，读的人会以为识别没问题。
	finish := func() OcrReconcileStats {
		for _, idx := range pending {
			ln := &doc.Lines[idx]
			if ln.Reason == fileproc.OcrReasonLowConfHard && strings.TrimSpace(ln.Final) != "" {
				st.Unclear++
			}
		}
		observability.Info(ctx, "ocr_fix",
			"candidates", st.Candidates, "calls", st.Calls, "fixed", st.Fixed, "doc_fixed", st.DocFixed,
			"kept", st.Kept,
			"not_text", st.NotText, "fallback", st.Fallback, "unclear", st.Unclear,
			"skipped", st.Skipped, "tokens", st.Tokens, "latency_ms", st.LatencyMs, "reasons", st.Reasons)
		return st
	}
	if !e.OcrFixEnabled() {
		st.Reasons["off"] = len(pending)
		e.ocrMarkFallback(doc, pending, &st)
		return finish()
	}
	// ★ 2026-10-29 ③-a：确定性「文档内佐证」修正（不经模型、不花一秒）。
	//   现网实证：`manua`（丢尾字母）在六次运行里一次都没被模型修掉——③ 的模型腿在全局
	//   翻译模型下每次都是 `tokens:0 / timeout`。而这一族错字在文档里**自带证据**：
	//   同一个词在别处写对过。用这份证据治，可机械复算、无需重试，也不引入新的猜测。
	//   finish 的存疑读数按**收手时**的待判集算：被确定性腿修掉的行已有明确结论，不再算存疑。
	pending = fixDocTruncations(doc, pending, &st)
	if len(pending) == 0 {
		return finish()
	}
	// ★ 2026-10-29：模型腿**只认「`ocr_fix` 这张卡自己显式配了模型」**——不走 resolveModelForStage
	//   那条 `ai_initial` → 全局主模型的回落链。回落链在别的阶段是对的，唯独这一档的活是
	//   "按 JSON 判定文字"，而现网初翻/全局都是**翻译模型**（tencent/Hunyuan-MT-7B）：
	//   六次运行全是 `calls:3, tokens:0, latency_ms:44998`＝每张图白等 45 秒、零收益，
	//   而 `/api/health.ocr_fix` 因为"回落得到东西"一直报 ok——**读数与事实相反**是本仓最坏那一族。
	//   ⇒ 没配这一档就是"这条腿没有能干活的人"：立刻收手、如实记 `no_stage_model`，
	//   由上面那条确定性腿承担主要修正；运营在第八张卡配一个通用指令模型，这条腿当场生效。
	b, k, m, stageActive := e.ocrFixStageModel(ctx)
	if !stageActive {
		st.Reasons["no_stage_model"] += len(pending)
		e.ocrMarkFallback(doc, pending, &st)
		return finish()
	}
	if strings.TrimSpace(b) == "" || strings.TrimSpace(k) == "" || strings.TrimSpace(m) == "" {
		st.Reasons["no_model"] += len(pending)
		e.ocrMarkFallback(doc, pending, &st)
		return finish()
	}
	// ★ 2026-10-29：模型腿的熔断窗口内**一次都不拨**（与主模型那只 breaker 分账，见 Engine 字段注释）。
	if cooling := e.ocrFixCooling(); cooling {
		st.Reasons["backoff"] += len(pending)
		e.ocrMarkFallback(doc, pending, &st)
		return finish()
	}
	groups, skipped := ocrFixGroups(pending)
	st.Skipped += skipped
	// 排队优先：quarantine 先于 uncertain（它们更可能是噪声/错字），组内按阅读顺序。
	deadline := time.Now().Add(ocrFixTotalBudget())
	for _, g := range groups {
		if len(g) == 0 {
			continue
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			st.Reasons["budget"] += len(g)
			e.ocrMarkFallback(doc, g, &st)
			continue
		}
		to := ocrFixCallTimeout()
		if remain < to {
			to = remain
		}
		items, err := ocrFixPayload(doc, g)
		if err != nil {
			st.Reasons["schema"] += len(g)
			e.ocrMarkFallback(doc, g, &st)
			continue
		}
		payload, _ := json.Marshal(items)
		// 载荷上限：超限时把本组截半（被截掉的行按 keep 交付），绝不让一次请求无边膨胀。
		for len(payload) > ocrFixMaxPayloadBytes && len(items.Items) > 1 {
			cut := len(items.Items) / 2
			dropped := items.Items[cut:]
			items.Items = items.Items[:cut]
			g = g[:cut]
			st.Skipped += len(dropped)
			payload, _ = json.Marshal(items)
		}
		cctx, cancel := context.WithTimeout(ctx, to)
		// ★ 2026-10-29 用户明令：**这一腿写死不推理，不留"换了模型再复开推理"的空间**。
		//   第八张卡现配的是 `THUDM/GLM-Z1-9B-0414`（GLM-**Z1** 是推理系）——它先吐一长段思维链再给 JSON，
		//   而这一腿的预算是 `max_tokens=1600` ＋ 单次 15 s ⇒ 思考链把预算与时间一起吃光：
		//   六次运行全是 `calls:3, tokens:0, latency_ms:44998`。
		//   **不是模型不行，是拿思考模型干一件不该想的活**：判定"这行是不是文字/少没少一个字符"
		//   没有需要推理的中间步骤，思维链在这里纯属成本与延迟。
		//   所以这里不设 env 开关（与 kill-switch `OCR_FIX` 不同档：那个关的是整条腿，这里关的是"先想一遍"）。
		//   换成别的模型也照发这一字段：OpenAI 兼容的供应商对未知字段一般忽略；
		//   万一某家回 400，表现是这条腿 fail-open 保留原识别（零收益、零破坏），不会把工单打回。
		// ★ 2026-10-29 第三次归因（前两版都没修到点上）：③ 继承文件模式的 ctx ⇒ 排在 fileChatSem(6) 后面，
		//   而图片件自己那批翻译正好把这个池占满（实测 `kind=fileChat waited=18.17s inflight=6`）：
		//   15 秒预算**在队列里**就耗光，请求根本没到模型。日志上与"模型太慢"同形，所以
		//   关思维链/换模型/抬超时都不动。⇒ 这一支走**交互通道**（不进文件池），
		//   它是用户正在等的同步判定，不是后台批任务。
		callCtx := llm.WithChatExtraParams(llm.WithInteractive(llm.WithoutFileMode(cctx)), ocrFixNoThinking())
		callStart := time.Now()
		before := ocrCollectorTotal(ctx)
		content, finishReason, cerr := e.LLM.CallChat(callCtx, b, k, m,
			[]map[string]string{
				{"role": "system", "content": ocrFixPrompt},
				{"role": "user", "content": string(payload)},
			}, ocrFixMaxTokens, false, 0.0)
		cancel()
		st.Calls++
		st.LatencyMs += time.Since(callStart).Milliseconds()
		if after := ocrCollectorTotal(ctx); after > before {
			st.Tokens += after - before
		}
		if cerr != nil {
			st.Reasons[ocrFixCallReason(cerr)] += len(g)
			e.ocrMarkFallback(doc, g, &st)
			continue
		}
		// ★ 2026-10-29：出栈必读 finish_reason。回执被 max_tokens 截断时 JSON 一定不完整，
		//   旧形态把它记成 `schema`（"模型答的不是 JSON"）——而真凶是"答到一半被掐了"，
		//   处置完全不同（前者该换提示词/模型，后者该抬预算或减批次）。
		//   同 AGENTS §19 那条：截断档**不许**并进上游断/超时的计数里。
		if finishReason == "length" {
			st.Reasons["truncated"] += len(g)
			e.ocrMarkFallback(doc, g, &st)
			continue
		}
		results, perr := parseOcrFixReply(content)
		if perr != nil {
			st.Reasons[perr.Error()] += len(g)
			e.ocrMarkFallback(doc, g, &st)
			continue
		}
		for _, idx := range g {
			r, ok := results[doc.Lines[idx].Index]
			if !ok {
				st.Fallback++ // 模型漏了这一行：按"没判过"处理
				continue
			}
			adopted := e.applyOcrFix(doc, idx, r, &st)
			if !adopted {
				st.Fallback++
			}
		}
	}
	// 本轮产出没产出，决定熔断计数（判据只看 tokens：模型真答了才会有 usage 读数，
	// 而 fixed/kept 为零可能只是"这一图本来就没毛病"，拿它当失败会把好模型也押进冷却）。
	e.ocrFixNoteOutcome(ctx, st.Tokens)
	return finish()
}

// ocrFixFailStreak 触发 ③ 模型腿熔断的**连续零产出轮数**（env `OCR_FIX_FAIL_STREAK`，默认 3，钳 1~10）。
func ocrFixFailStreak() int {
	if v := strings.TrimSpace(os.Getenv("OCR_FIX_FAIL_STREAK")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				n = 1
			}
			if n > 10 {
				n = 10
			}
			return n
		}
	}
	return 3
}

// ocrFixBackoff 熔断后的冷却时长（env `OCR_FIX_BACKOFF_SEC`，默认 600s，钳 1~3600s）。
//
// 下界取 1s 而不是 30s 只为一件事：让"冷却到期会自己再试一次"这条判据能被单测真的跑到
// （把下界抬到 30s，那条断言就只能靠 sleep 或者根本没人写——于是这一族只有"进"没有"出"）。
func ocrFixBackoff() time.Duration {
	if v := strings.TrimSpace(os.Getenv("OCR_FIX_BACKOFF_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			if n < 1 {
				n = 1
			}
			if n > 3600 {
				n = 3600
			}
			return time.Duration(n) * time.Second
		}
	}
	return 600 * time.Second
}

// ocrFixCooling ③ 模型腿是否处于零产出冷却窗口内。**唯一读点**——
// 拨不拨（ReconcileOcrDoc）与健康面报什么词（OcrFixHealth）必须问同一个问题，
// 两处各判一次迟早长出一句"health 说 ok、实际根本没拨"（AGENTS §一·5 那一族多把尺子）。
func (e *Engine) ocrFixCooling() bool { return e.ocrFixBreaker().IsOpen() }

// ocrFixStageModel ③ 模型腿的**档位判据唯一读点**：只认 `ocr_fix` 这张卡自己配的模型。
//
// 为什么单独收一个函数而不调 `resolveModelForStage`：后者在 ocr_fix 没配时会继续回落到
// `ai_initial` 的阶段模型／全局主模型，并把 `stageActive` 一起报成 true——
// 现网 `/api/health.ocr_fix` 因此一直报 ok，而 ③ 六次运行全是 `tokens:0 全 timeout`
// （回落过去的是**翻译模型** tencent/Hunyuan-MT-7B，它答不了"按 JSON 判定二十行"）。
// ⇒ 这一档没有"不配也能跑"：没配就是不拨。
func (e *Engine) ocrFixStageModel(ctx context.Context) (base, key, model string, configured bool) {
	return e.resolveStageModel(ctx, config.StageOcrFix)
}

// ocrFixBreaker 取（并按需建）③ 模型腿的熔断器。
//
// 懒建是因为 `Engine{}` 零值在单测与启动早期都要能用；构造参数在那一刻从 env 读，
// 之后改 env 需要重启进程才生效（与 kill-switch `OCR_FIX` 同一档，不是新的配置面）。
// 复用本包既有的 `Breaker`（主模型那只同形），不另造第二套熔断语义。
func (e *Engine) ocrFixBreaker() *Breaker {
	e.ocrFixBreakMu.Lock()
	defer e.ocrFixBreakMu.Unlock()
	if e.ocrFixBreak == nil {
		e.ocrFixBreak = NewBreaker(ocrFixFailStreak(), ocrFixBackoff())
	}
	return e.ocrFixBreak
}

// ocrFixNoteOutcome 按本轮的真实 token 记一笔：有产出即清零，零产出计一次失败。
//
// 只在**真拨过**之后调用（`no_stage_model`/`off`/`cancelled` 那些提前返回的分支不算失败：
// 压根没拨＝没资格记账，否则一次上层取消就能把这条腿押进冷却）。
func (e *Engine) ocrFixNoteOutcome(ctx context.Context, tokens int64) {
	if tokens > 0 {
		e.ocrFixBreaker().Reset()
		return
	}
	if e.ocrFixBreaker().Fail("zero_tokens") {
		observability.Warn(ctx, "ocr_fix 模型腿连续零产出，进入冷却",
			"streak", ocrFixFailStreak(), "backoff_sec", int(ocrFixBackoff().Seconds()),
			"reason", "zero_tokens")
	}
}

// ocrFixResult 一条模型回执。
type ocrFixResult struct {
	Index   int    `json:"index"`
	Verdict string `json:"verdict"`
	Value   string `json:"value"`
}

// ocrFixReply 整份回执（带版本号，防旧回执错配新一轮文档）。
type ocrFixReply struct {
	Version int            `json:"version"`
	Results []ocrFixResult `json:"results"`
}

// applyOcrFix 把一条回执按采纳闸落到 Final 上；返回是否被**明确处理**（keep/fix/not_text 之一）。
//
// 采纳闸（主方案 §8.2）：
//   - keep：合法；
//   - fix：候选集命中，或"改一个字符"且字符集不变、数字不变、conf<0.6；否则丢弃回执；
//   - not_text：只许 glyph_set/geom_anomaly 且长度 ≤2；其余降级 keep。
func (e *Engine) applyOcrFix(doc *fileproc.OcrDoc, idx int, r ocrFixResult, st *OcrReconcileStats) bool {
	ln := &doc.Lines[idx]
	switch r.Verdict {
	case "keep":
		st.Kept++
		return true
	case "fix":
		val := strings.TrimSpace(r.Value)
		if val == "" {
			return false
		}
		if val == ln.Text {
			st.Kept++
			return true
		}
		if ocrFixFromAlts(val, ln.Alts) || ocrFixNewCandidate(val, *ln) || ocrFixDocSupported(doc, val, *ln) {
			ln.Final = val
			st.Fixed++
			return true
		}
		return false
	case "not_text":
		if !ocrNotTextAllowed(*ln, doc.Profile) {
			return false
		}
		ln.Final = ""
		st.NotText++
		return true
	}
	return false
}

// ocrFixFromAlts 候选集命中（去空白后逐字相等）。
func ocrFixFromAlts(val string, alts []string) bool {
	for _, a := range alts {
		if strings.TrimSpace(a) == val {
			return true
		}
	}
	return false
}

// ocrFixableLine 这一行值不值得送 ③：至少含一个字母或汉字。
//
// 纯数字/符号/单非汉字没有"错字"可修；送进模型只会诱它改写（现网把开关误读出的 `0` 送进去，
// 模型完全可能"修正"成别的字形）。清洗层已把这类行按 §19 族二原样交付。
func ocrFixableLine(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// ocrFixDocSupported 高置信行的「文档内佐证」开口（★ 2026-10-29 用户令「高置信错字也要修」）。
//
// 为什么必须再开一条：`ocrFixNewCandidate` 的开口带 `Conf < 0.6`，而现网最常见的错字
// （`manua`、`double_bum`）恰恰是**高置信**的——模型认得很自信，只是少了个字符。
// 不开这条，扩了射程也修不动（模型给了 fix 也会被闸丢掉）。
//
// 判据（全部成立才采纳，缺一不可）：
//
//	· 只改一个字符（插入/删除/替换各一以内），字符集与数字序列不变；
//	· 词数不变，且**恰好一个词变了**；
//	· 新词（≥3 字符）在本文档**别的行里出现过**（大小写不敏感）。
//
// 最后一条是结构上的证据：模型不能凭空造一个本文件里不存在的词，只能把某个词改成本文件里
// 真实出现过的写法（`manua` + 同文件里另两处 `manual` ⇒ 采纳；`cat`→`car` 这种没有任何佐证的
// 单字符替换一律拒绝）。这样扩射程不会把"改一个字"变成"改写正文"。
func ocrFixDocSupported(doc *fileproc.OcrDoc, val string, ln fileproc.OcrLine) bool {
	if doc == nil {
		return false
	}
	if ln.ConfMissing || ln.Conf < 0.6 {
		return false // 低置信走既有 NewCandidate 口径，不重复开口
	}
	if absIntLocal(len([]rune(val))-len([]rune(ln.Text))) > 1 {
		return false
	}
	if !ocrWithinOneEdit(val, ln.Text) {
		return false
	}
	if !sameScriptSet(val, ln.Text) {
		return false
	}
	if digitsOf(val) != digitsOf(ln.Text) {
		return false
	}
	oldW, newW := strings.Fields(ln.Text), strings.Fields(val)
	if len(oldW) == 0 || len(oldW) != len(newW) {
		return false
	}
	changed, diff := "", 0
	for i := range oldW {
		if oldW[i] == newW[i] {
			continue
		}
		diff++
		changed = newW[i]
		if diff > 1 {
			return false
		}
	}
	if diff != 1 || len([]rune(changed)) < 3 {
		return false
	}
	target := strings.ToLower(changed)
	for i := range doc.Lines {
		if doc.Lines[i].Index == ln.Index {
			continue
		}
		for _, w := range strings.Fields(doc.Lines[i].Text) {
			if strings.ToLower(w) == target {
				return true
			}
		}
	}
	return false
}

// ocrFixNewCandidate "新候选"开口：改一个字符、字符集不变、数字不变、且只在最不确定时开。
//
// 为什么允许这条路：候选集可能缺正确写法（同列只出现两次、众数门限没到）。
// 五条同时成立时，模型在**结构上编不出内容**——这是它能自动上线、不需要任何复核的前提。
func ocrFixNewCandidate(val string, ln fileproc.OcrLine) bool {
	if ln.ConfMissing || ln.Conf >= 0.6 {
		return false
	}
	if absIntLocal(len([]rune(val))-len([]rune(ln.Text))) > 1 {
		return false
	}
	if !ocrWithinOneEdit(val, ln.Text) {
		return false
	}
	if !sameScriptSet(val, ln.Text) {
		return false
	}
	if digitsOf(val) != digitsOf(ln.Text) {
		return false
	}
	return true
}

// ocrNotTextAllowed not_text 的两支合法情形（长度 ≤2 是共同上限）。
func ocrNotTextAllowed(ln fileproc.OcrLine, prof textscript.Profile) bool {
	if len([]rune(strings.TrimSpace(ln.Text))) > 2 {
		return false
	}
	switch ln.Reason {
	case fileproc.OcrReasonGeomAnomaly:
		return true
	case fileproc.OcrReasonGlyphSet:
		// glyph_set 本身只在"该体系占比 <0.5% 或全篇没有"时触发，这里再核一次读数，
		// 防清洗层被改坏后这条闸跟着失守。
		var first rune
		for _, r := range ln.Text {
			if unicode.IsLetter(r) {
				first = r
				break
			}
		}
		if first == 0 {
			return false
		}
		return prof.ShareOf(textscript.Of(first)) < 0.005
	}
	return false
}

// ocrMarkFallback 一组行按 fail-open keep 处理（Final 保持原识别）。
func (e *Engine) ocrMarkFallback(doc *fileproc.OcrDoc, idxs []int, st *OcrReconcileStats) {
	for _, i := range idxs {
		ln := &doc.Lines[i]
		if strings.TrimSpace(ln.Final) == "" {
			ln.Final = ln.Text
		}
		st.Fallback++
	}
}

// ocrDocTokenRe 「词」的形状：以字母开头，可含字母/数字/下划线（`double_bump` 这类标识符也算一个词）。
var ocrDocTokenRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_]*`)

// fixDocTruncations 用**文档自身的词表**修正「丢尾字符/首字符」那一族识别错字，返回仍需送模型的行走。
//
// 为什么要有这一条（2026-10-29 用户实证）：`manua` 这一族在六次现网运行里一次都没被模型修掉，
// 而它恰恰是客户最容易看见的错——模型腿慢到超时（详见 ReconcileOcrDoc 的 no_stage_model 口径）。
// 但这类错字**在文件里有现成的证据**：同一个词在别处写对过。判据只吃这份证据，不猜、不补全。
//
// 五条同时成立才动（缺一不可，任何一条不满足就原样交给后面的模型腿）：
//  1. 待修词 T：全篇**只出现 1 次**，长度 ≥5，纯 ASCII 字母/数字/下划线；
//  2. 佐证词 U：全篇出现 **≥2 次**，且比 T 多**恰好一个字符**、多出来的那个在最前或最后
//     （即 T 是 U 丢了一个边字符后剩下的那段——OCR 截断的典型形态）；
//  3. 多出来的那个字符**不是 `s`/`S`**：英文单复数与第三人称恰好长这一族
//     （items/orders/totals），不排掉就会把合法的单数改成复数——那是"改对形、改错义"；
//  4. 满足条件的 U **唯一**：出现两个不同的候选就放弃（宁可留给模型，也不在两个都像的词里挑）；
//  5. 该行置信度 **< 0.95**：认得很自信的文本不动（启发式只配纠正"连识别器自己都不确定"的字）。
//
// 替换形态取 U 在文中出现最多的那个原样大小写（`Manual` 就还是 `Manual`），
// 且每行最多改一处——一行里两处都符合判据时，第二处留给下一轮/模型，不让修正在同一行上叠加。
func fixDocTruncations(doc *fileproc.OcrDoc, pending []int, st *OcrReconcileStats) []int {
	if doc == nil || len(pending) == 0 {
		return pending
	}
	count, forms := ocrDocTokenStats(doc)
	rest := make([]int, 0, len(pending))
	for _, idx := range pending {
		ln := &doc.Lines[idx]
		if ln.ConfMissing || ln.Conf >= 0.95 {
			rest = append(rest, idx)
			continue
		}
		fixed, ok := truncationFix(ln.Text, count, forms)
		if !ok {
			rest = append(rest, idx)
			continue
		}
		ln.Final = fixed
		st.DocFixed++
		st.Fixed++
	}
	return rest
}

// ocrDocTokenStats 统计全篇每个词（小写归一）的出现次数，以及每个词的各个原样写法的次数。
func ocrDocTokenStats(doc *fileproc.OcrDoc) (map[string]int, map[string]map[string]int) {
	count := make(map[string]int, len(doc.Lines)*4)
	forms := make(map[string]map[string]int, len(doc.Lines)*4)
	for i := range doc.Lines {
		for _, tok := range ocrDocTokenRe.FindAllString(doc.Lines[i].Text, -1) {
			lc := strings.ToLower(tok)
			count[lc]++
			if forms[lc] == nil {
				forms[lc] = make(map[string]int, 2)
			}
			forms[lc][tok]++
		}
	}
	return count, forms
}

// truncationFix 对一行文本试一次"截断错字"修正；返回改好的整行与是否成立。
func truncationFix(text string, count map[string]int, forms map[string]map[string]int) (string, bool) {
	target, cand, found := "", "", false
	for _, tok := range ocrDocTokenRe.FindAllString(text, -1) {
		if len(tok) < 5 || count[strings.ToLower(tok)] != 1 {
			continue
		}
		lc := strings.ToLower(tok)
		for other, n := range count {
			if n < 2 || len(other) != len(lc)+1 {
				continue
			}
			var extra byte
			switch {
			case strings.HasPrefix(other, lc):
				extra = other[len(other)-1]
			case strings.HasSuffix(other, lc):
				extra = other[0]
			default:
				continue
			}
			if extra == 's' || extra == 'S' {
				continue // 单复数/第三人称那一族：形状对得上，语义不一定对得上
			}
			if found && other != cand {
				return "", false // 两个不同的佐证词同时成立：不猜
			}
			target, cand, found = tok, other, true
		}
	}
	if !found {
		return "", false
	}
	repl := majorityForm(forms[cand])
	if repl == "" {
		return "", false
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(target) + `\b`)
	if !re.MatchString(text) {
		return "", false
	}
	return re.ReplaceAllLiteralString(text, repl), true
}

// majorityForm 取一个词在文中出现最多的那个原样写法（次数相同取字典序小的，保证可复算）。
func majorityForm(m map[string]int) string {
	best, bestN := "", 0
	for f, n := range m {
		if n > bestN || (n == bestN && f < best) {
			best, bestN = f, n
		}
	}
	return best
}

// ocrFixGroups 把待判行分成至多 ocrFixMaxCalls 组（每组 ≤ ocrFixMaxLinesPerCall 行），
// 并返回因超出上限而未送审的行数。
func ocrFixGroups(pending []int) ([][]int, int) {
	// 顺序由调用方排好（quarantine 先于 uncertain 先于 clean）；这里只按数量切：
	// 每组 ≤ ocrFixMaxLinesPerCall 行、至多 ocrFixMaxCalls 组，其余记 skipped（fail-open keep）。
	// ★ 2026-10-29 改成按常量通用切分：旧实现对 61~120 行走"对半"，切出来的组可能远超 20 行，
	//   小批收敛就不成立了（"对半"那段是 60 行时代的产物）。
	var groups [][]int
	for i := 0; i < len(pending) && len(groups) < ocrFixMaxCalls; i += ocrFixMaxLinesPerCall {
		end := i + ocrFixMaxLinesPerCall
		if end > len(pending) {
			end = len(pending)
		}
		groups = append(groups, pending[i:end])
	}
	covered := 0
	for _, g := range groups {
		covered += len(g)
	}
	return groups, len(pending) - covered
}

// ocrFixItems ③ 的请求载荷。
type ocrFixItems struct {
	Version    int                `json:"version"`
	DocScripts map[string]float64 `json:"doc_scripts"`
	Items      []ocrFixItem       `json:"items"`
}

// ocrFixItem 单行载荷。
type ocrFixItem struct {
	Index      int           `json:"index"`
	Text       string        `json:"text"`
	Conf       float64       `json:"conf"`
	Reason     string        `json:"reason"`
	Script     string        `json:"script"`
	Candidates []string      `json:"candidates,omitempty"`
	LatencyMs  int64         `json:"latency_ms,omitempty"`
	Column     []ocrFixCount `json:"column,omitempty"`
	Neighbors  []string      `json:"neighbors,omitempty"`
}

// ocrFixCount 同列某文本的出现次数（候选集来源）。
type ocrFixCount struct {
	Text string `json:"text"`
	N    int    `json:"n"`
}

// ocrFixPayload 组装一份请求载荷（只送待判行 + 同列统计 + 书写体系占比，不送图片/整篇原文）。
func ocrFixPayload(doc *fileproc.OcrDoc, idxs []int) (ocrFixItems, error) {
	out := ocrFixItems{Version: doc.Version, DocScripts: map[string]float64{}}
	for s, v := range doc.Profile.Share {
		out.DocScripts[s.String()] = math.Round(v*10000) / 10000
	}
	for _, idx := range idxs {
		if idx < 0 || idx >= len(doc.Lines) {
			return out, fmt.Errorf("行下标越界: %d", idx)
		}
		ln := doc.Lines[idx]
		it := ocrFixItem{
			Index:      ln.Index,
			Text:       ln.Text,
			Conf:       ln.Conf,
			Reason:     ln.Reason,
			Script:     dominantScript(ln.Text),
			Candidates: ln.Alts,
		}
		// 同列统计：只取该列非空行，按出现次数取前 2（不把整篇文本送出去）。
		if ln.Column >= 0 && ln.Column < len(doc.Columns) {
			counts := map[string]int{}
			for _, j := range doc.Columns[ln.Column].Lines {
				t := strings.TrimSpace(doc.Lines[j].Final)
				if t != "" {
					counts[t]++
				}
			}
			type kv struct {
				t string
				n int
			}
			arr := make([]kv, 0, len(counts))
			for t, n := range counts {
				if t == ln.Text {
					continue
				}
				arr = append(arr, kv{t, n})
			}
			sort.Slice(arr, func(a, b int) bool {
				if arr[a].n != arr[b].n {
					return arr[a].n > arr[b].n
				}
				return arr[a].t < arr[b].t
			})
			for i := 0; i < len(arr) && i < 2; i++ {
				it.Column = append(it.Column, ocrFixCount{Text: arr[i].t, N: arr[i].n})
			}
		}
		// 邻居：同列前后各取 2 行（无列则按阅读顺序前后各取 2 行），只取非空文本。
		it.Neighbors = ocrNeighbors(doc, idx)
		out.Items = append(out.Items, it)
	}
	return out, nil
}

// ocrNeighbors 取同列（无列则阅读顺序）前后各 2 行的非空文本。
func ocrNeighbors(doc *fileproc.OcrDoc, idx int) []string {
	var order []int
	if ln := doc.Lines[idx]; ln.Column >= 0 && ln.Column < len(doc.Columns) {
		order = doc.Columns[ln.Column].Lines
	} else {
		for i := range doc.Lines {
			order = append(order, i)
		}
	}
	pos := -1
	for i, j := range order {
		if j == idx {
			pos = i
			break
		}
	}
	if pos < 0 {
		return nil
	}
	out := make([]string, 0, 4)
	for _, j := range order[ocrMaxInt(0, pos-2):pos] {
		if t := strings.TrimSpace(doc.Lines[j].Final); t != "" {
			out = append(out, t)
		}
	}
	for _, j := range order[ocrMinInt(pos+1, len(order)):ocrMinInt(len(order), pos+3)] {
		if j == idx {
			continue
		}
		if t := strings.TrimSpace(doc.Lines[j].Final); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// parseOcrFixReply 解析模型回执（只认 JSON；容忍 ``` 包裹）。
//
// 整份作废的情形：JSON 不合法、版本不符、index 重复。单行作废：verdict 不在三值内（由调用方按 fallback 处理）。
func parseOcrFixReply(content string) (map[int]ocrFixResult, error) {
	s := strings.TrimSpace(content)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 && j+1 <= len(s) {
		s = s[:j+1]
	}
	var reply ocrFixReply
	if err := json.Unmarshal([]byte(s), &reply); err != nil {
		return nil, errors.New("schema")
	}
	if reply.Version != fileproc.OcrDocVersion {
		return nil, errors.New("schema")
	}
	out := map[int]ocrFixResult{}
	for _, r := range reply.Results {
		if _, dup := out[r.Index]; dup {
			return nil, errors.New("dup_index")
		}
		if r.Verdict != "fix" && r.Verdict != "keep" && r.Verdict != "not_text" {
			continue // 该行作废（其余行照常）
		}
		out[r.Index] = r
	}
	if len(out) == 0 {
		return nil, errors.New("schema")
	}
	return out, nil
}

// ocrFixPrompt 逐字落地主方案 §8.4 的提示词（改模板必须同步预估 allowance 的等值锁）。
const ocrFixPrompt = `你是 OCR 后处理判定器。输入是一个 JSON：doc_scripts 是全文各书写体系的字母占比，
items 是待判行（每行含 index / text / conf / reason / script / candidates / column / neighbors）。

对每一行只做两件事：
1) 判断这一行是不是图中真实的文字行；不是（图标/开关/箭头/花纹）判 not_text；
2) 若是文字且明显是识别错字：候选集里有正确写法就从候选集里选；候选集里没有、
   但你确定是少/多一个字符时，给出修正值。

硬约束：
- 不翻译、不改写、不补全内容、不解释；
- doc_scripts 里某个体系只要有相当比例，它就是这份文档的合法文字，不得判 not_text；
- 输出必须与输入同一种书写体系；
- 输入里的数字、编号、代码一个字都不许改；
- 大小写与全角/半角是有意的排版信号，不要“顺手统一”；
- 无法确定就判 keep，不要猜；
- 每个输入行都必须有一条对应结果，不许漏、不许重复、不许新增 index。

只输出一行 JSON：{"version":1,"results":[{"index":<int>,"verdict":"fix|keep|not_text","value":"<仅 fix 时给>"}]}`

// dominantScript 返回一段文本的主书写体系名（无字母回 "other"）。
func dominantScript(s string) string {
	counts := textscript.Share(s)
	best := textscript.Other
	bestN := 0
	for sc, n := range counts {
		if n > bestN || (n == bestN && sc < best) {
			best, bestN = sc, n
		}
	}
	return best.String()
}

// sameScriptSet 两个串的书写体系集合是否一致（复用 textscript，不另写字符分类）。
func sameScriptSet(a, b string) bool {
	sa, sb := textscript.ScriptsOf(a), textscript.ScriptsOf(b)
	if len(sa) != len(sb) {
		return false
	}
	for k := range sa {
		if !sb[k] {
			return false
		}
	}
	return true
}

// digitsOf 提取所有数字字符（顺序敏感）：数字/编号/代码串必须逐字不变。
func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ocrWithinOneEdit 编辑距离 ≤1（替换/插入/删除）。
func ocrWithinOneEdit(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if absIntLocal(len(ra)-len(rb)) > 1 {
		return false
	}
	if len(ra) == len(rb) {
		diff := 0
		for i := range ra {
			if ra[i] != rb[i] {
				diff++
				if diff > 1 {
					return false
				}
			}
		}
		return true
	}
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	i, j, skipped := 0, 0, false
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i++
			j++
			continue
		}
		if skipped {
			return false
		}
		skipped = true
		j++
	}
	return true
}

// ocrCollectorTotal 本次链路累计真实 token（输入+输出）；无收集器回 -1。
func ocrCollectorTotal(ctx context.Context) int64 {
	if uc := llm.CollectorFrom(ctx); uc != nil {
		return uc.Total()
	}
	return -1
}

// ocrFixNoThinking ③ 发给上游的厂商私有字段：关掉思维链（写死，无开关）。
//
// 形制只在这里出现一次：`llm.WithChatExtraParams` 是唯一注入口，
// 而"③ 不推理"这一条只有这一个消费者——不要在别的腿上也顺手关一遍
// （审校/补译那两腿是真的需要推理的，关了就是拿质量换延迟）。
func ocrFixNoThinking() map[string]interface{} {
	return map[string]interface{}{"thinking": map[string]interface{}{"type": "disabled"}}
}

// ocrFixCallReason 把调用错误归到 reason 档（排障只数不数内容）。
//
// ★ 2026-10-29：`context.Canceled` 与 `DeadlineExceeded` 必须分档。现网实证：一单里三次调用
// 只花了两千多毫秒却全记成 `timeout`——那是**上层把这一单的 ctx 取消了**（重试/放弃），
// 不是模型慢。混成一档会把排查指向"换模型/抬超时"，而真凶在上一层。
func ocrFixCallReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	return "upstream"
}

// absIntLocal 整数绝对值。
func absIntLocal(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// minInt / maxInt 小工具（本文件内部用，避免 import 额外包）。
func ocrMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ocrMaxInt 取两数较大者（本文件内部用）。
func ocrMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
