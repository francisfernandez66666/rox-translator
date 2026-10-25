// ============ 本文件职责中文说明 ============
// N-01（2026-10-25 对账补腿）：单次 LLM 调用 prompt token 超阈值的监控腿。
// 修法原文＝「单次调用 prompt token 超过阈值（如 8,000）→ WARN + 指标」，
// 对应现网「41 字即时翻译实扣 103 积分 / 0.2KB 文件实扣 1,037 积分」那一族成本失控：
// 累计计数器会被大量正常小请求摊平，**只有单次维度**能现形文化块/术语表膨胀。
// 本锁判四件事：① 超阈值出 WARN 且带 model/prompt_tokens/threshold；② 计数器随之 +1；
// ③ 未超阈值/供应商没回 usage 时一律静默（告警面被噪音填满＝等于没有告警）；
// ④ env 覆盖与 /metrics 导出腿在位（导出缺失＝计数器只存在于进程内存里）。
// ============================================
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

// promptObsMu 保护「换 slog.Default + 读进程级计数器」这一段：
// obsPromptLargeCalls 是包级单例，并发用例同判会互相加减（表现为计数对不上）。
var promptObsMu sync.Mutex

// capturePromptLog 把 slog 默认 logger 换成内存 JSON；级别压到 Debug 才有射程判「是不是 WARN」。
func capturePromptLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return buf, func() { slog.SetDefault(old) }
}

// overLines 挑出超阈值告警行（其余日志不参与判定）。
func overLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("捕获到的日志行不是合法 JSON（说明没走 observability 的 slog 链）：%q", line)
		}
		if m["msg"] == "llm_prompt_token_over_threshold" {
			out = append(out, m)
		}
	}
	return out
}

// TestPromptThresholdWarnsAndCounts 正向腿：超阈值必须同时出 WARN 与计数 +1。
func TestPromptThresholdWarnsAndCounts(t *testing.T) {
	promptObsMu.Lock()
	defer promptObsMu.Unlock()
	t.Setenv("LLM_PROMPT_TOKEN_WARN_THRESHOLD", "") // 钉默认档，避免宿主 env 把阈值改掉

	before := Observability().PromptLargeCalls
	buf, restore := capturePromptLog(t)
	recordChatUsageObs(context.Background(), "deepseek-test", 9000, 0)
	restore()

	after := Observability().PromptLargeCalls
	if after != before+1 {
		t.Errorf("超阈值调用必须让计数 +1（/metrics 那条序列的唯一来源），实得 %d→%d", before, after)
	}
	lines := overLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("超阈值只该出一行 WARN，实得 %d 行（缓冲全文：%q）", len(lines), buf.String())
	}
	got := lines[0]
	if lvl, _ := got["level"].(string); lvl != "WARN" {
		t.Errorf("这条是成本告警不是流水记录，级别必须 WARN，实得 %q", lvl)
	}
	// 三件读数缺一不可：没有 model 无法归因到路由，没有 threshold 无法判断该抬哪一侧上限
	for _, k := range []string{"model", "prompt_tokens", "threshold"} {
		if _, ok := got[k]; !ok {
			t.Errorf("告警缺读数 %q，排障时无法归因：%v", k, got)
		}
	}
	if m, _ := got["model"].(string); m != "deepseek-test" {
		t.Errorf("model 读数必须是本轮实际使用的模型名，实得 %q", m)
	}
	if pt, _ := got["prompt_tokens"].(float64); pt != 9000 {
		t.Errorf("prompt_tokens 读数应等于供应商回传的真实值，实得 %v", pt)
	}
	if th, _ := got["threshold"].(float64); th != DefaultPromptTokenWarnThreshold {
		t.Errorf("默认阈值应为 %d，实得 %v", DefaultPromptTokenWarnThreshold, th)
	}
}

// TestPromptThresholdStaysSilentBelowOrUnknown 反向对照三档：恰好等于阈值／低于阈值／
// 没有 usage（prompt=0）。第三档最重要——不可信供应商不回 usage 时不许刷屏。
func TestPromptThresholdStaysSilentBelowOrUnknown(t *testing.T) {
	promptObsMu.Lock()
	defer promptObsMu.Unlock()
	t.Setenv("LLM_PROMPT_TOKEN_WARN_THRESHOLD", "")

	before := Observability().PromptLargeCalls
	buf, restore := capturePromptLog(t)
	recordChatUsageObs(context.Background(), "deepseek-test", DefaultPromptTokenWarnThreshold, 10) // 等值不算超
	recordChatUsageObs(context.Background(), "deepseek-test", 100, 0)                              // 正常小调用
	recordChatUsageObs(context.Background(), "deepseek-test", 0, 0)                                // 无 usage
	restore()

	if lines := overLines(t, buf); len(lines) != 0 {
		t.Errorf("三档静默对照里出了告警（噪音会把真告警埋掉）：%v", lines)
	}
	if got := Observability().PromptLargeCalls; got != before {
		t.Errorf("静默档不许动计数，实得 %d→%d", before, got)
	}
	// 顺带自证累计腿没被这次改动打坏（prompt>0 才累加，等值/正常档都进分母）
	if p := Observability().PromptTokens; p < DefaultPromptTokenWarnThreshold {
		t.Errorf("prompt 累计观测腿被改坏（本用例至少加过 %d），实得 %d", DefaultPromptTokenWarnThreshold, p)
	}
}

// TestPromptThresholdEnvOverride 阈值档位是运维可调的（env > 默认），
// 且必须**每次现读**——进程启动后改 env 再重启即生效，不缓存旧值。
func TestPromptThresholdEnvOverride(t *testing.T) {
	t.Setenv("LLM_PROMPT_TOKEN_WARN_THRESHOLD", "")
	if got := promptTokenWarnThreshold(); got != DefaultPromptTokenWarnThreshold {
		t.Fatalf("未配置 env 时必须走默认 %d，实得 %d", DefaultPromptTokenWarnThreshold, got)
	}
	t.Setenv("LLM_PROMPT_TOKEN_WARN_THRESHOLD", "50")
	if got := promptTokenWarnThreshold(); got != 50 {
		t.Fatalf("env 覆盖未生效，实得 %d", got)
	}
	// 脏值（0/负数/非数字）折回默认，不许把阈值打成 0 ⇒ 每次调用都告警
	for _, bad := range []string{"0", "-1", "abc"} {
		t.Setenv("LLM_PROMPT_TOKEN_WARN_THRESHOLD", bad)
		if got := promptTokenWarnThreshold(); got != DefaultPromptTokenWarnThreshold {
			t.Errorf("脏值 %q 必须折回默认 %d，实得 %d", bad, DefaultPromptTokenWarnThreshold, got)
		}
	}
}

// TestPromptThresholdSingleChokeWired 守卫级腿：阈值判定只能落在 recordChatUsageObs 这一条咽喉上，
// 且非流式/流式两条调用点必须把 ctx 与 model 一起传进来（传丢任一件＝告警无法归因）。
// 只测函数本身的判据（上面几条）判不出「接线退回旧两参数形态」，所以这一腿按源码数调用点。
func TestPromptThresholdSingleChokeWired(t *testing.T) {
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("读 client.go 失败，守卫腿无对象：%v", err)
	}
	text := stripGoComments(string(src))
	if n := strings.Count(text, "recordChatUsageObs(ctx, model,"); n != 2 {
		t.Errorf("非流式与流式两条腿必须各调一次 recordChatUsageObs(ctx, model, …)，实得 %d 处（少于 2＝告警腿没被走到）", n)
	}
	// 负向：旧的「不传 ctx/model」两参数调用形态不许复活
	if n := strings.Count(text, "recordChatUsageObs(cr.Usage.PromptTokens,"); n != 0 {
		t.Errorf("client.go 里复活了旧的 2 参数调用（告警将拿不到 model 归因）：%d 处", n)
	}
	// 阈值判定点只许一处：再长出第二处＝同一件事两把尺子（AGENTS §一·11 那一族）。
	// 按**调用形态**数（`:= promptTokenWarnThreshold()`），裸词会把函数声明那一行也算进来（本批首跑真踩）。
	if n := strings.Count(text, ":= promptTokenWarnThreshold()"); n != 1 {
		t.Errorf("阈值判定点必须只有咽喉里那一处，实得 %d 处", n)
	}
}

// TestPromptThresholdExportedToMetrics 导出腿：/metrics 必须把计数器发出去。
// 计数器只在进程内存里＝看板看不见，修法里的「+ 指标」这一半等于没做。
// 这里按源码判（metrics.go 整段是逐行 WriteString 的导出体，非可单测的纯函数），
// 并配一条对照腿（文件必须读得到既有观测行）防止读空恒绿。
func TestPromptThresholdExportedToMetrics(t *testing.T) {
	src, err := os.ReadFile("../api/metrics.go")
	if err != nil {
		t.Fatalf("读 ../api/metrics.go 失败（导出腿的判据对象）：%v", err)
	}
	text := stripGoComments(string(src))
	for _, want := range []string{
		"translator_llm_prompt_over_threshold_total",
		"obs.PromptLargeCalls",
		// 对照腿：既有观测行必须在，否则文件被读空/改名也会恒绿
		"translator_llm_prompt_tokens_total",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("metrics.go 缺 %q——超阈值计数没有导出，看板看不见这条成本信号", want)
		}
	}
}

// stripGoComments 剥掉 // 行注释：本文件与 metrics.go 的说明注释里故意写着判据字面量，
// 不剥就会被自己新写的注释喂饱（AGENTS §一·5 那条负向锁自伤的坑）。
func stripGoComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
