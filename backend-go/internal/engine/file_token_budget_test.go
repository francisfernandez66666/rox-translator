// ============ 本文件职责中文说明 ============
// N-01（2026-10-25 对账补腿）：单工单累计 token 成本闸门的断言。
// 分两层：
// ① 判据层（TestRepairTokenBudget*）：倍数档位、fail-open 方向、到线即停的边界；
// ② 守卫层（TestRepairTokenBudgetWiredInFileChain）：按**源码**数文件链上的复核点位——
// 只测函数本身判不出「预算算好了却没人查」那一族短路（AGENTS §三「判据级断言代替不了守卫级」）。
// 现网本体＝UAT 工单 115：0.2KB／4 行文件实扣 1,037 积分（预估约 44），
// 墙钟预算（600s）对这种"每次都很贵"完全无感。
// ============================================
package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"translator/internal/llm"
)

// addTokens 往 ctx 里的用量收集器累加一次真实用量（模拟一次 LLM 调用回传的 usage）。
func addTokens(t *testing.T, ctx context.Context, prompt, completion int64) {
	t.Helper()
	uc := llm.CollectorFrom(ctx)
	if uc == nil {
		t.Fatal("ctx 里没有用量收集器，本用例的判据对象不存在")
	}
	uc.Add(prompt, completion)
}

// TestRepairTokenBudgetNoCollectorIsFailOpen 无计量口径 ⇒ 不判（返回 -1）。
// 方向性断言：观测腿缺失不许变成产品缺陷（把补译机会摘掉）。
func TestRepairTokenBudgetNoCollectorIsFailOpen(t *testing.T) {
	t.Setenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE", "")
	e := &Engine{}
	ctx := context.Background() // 没有 WithUsageRecorder 的调用面（如单测直接调）
	if got := e.repairTokenBudgetCap(ctx, 5000); got != -1 {
		t.Errorf("无收集器必须返回 -1（不启用），实得 %d", got)
	}
	if over, spent := e.repairBudgetSpent(ctx, -1); over || spent != 0 {
		t.Errorf("不启用时一律放行且不出读数，实得 over=%v spent=%d", over, spent)
	}
}

// TestRepairTokenBudgetMultipleAndFloor 三条档位：默认倍数、env 覆盖、首翻零用量的绝对余量。
func TestRepairTokenBudgetMultipleAndFloor(t *testing.T) {
	e := &Engine{}
	ctx := e.WithUsageRecorder(context.Background())

	t.Setenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE", "")
	if got := e.repairTokenBudgetCap(ctx, 10000); got != int64(10000*DefaultRepairTokenBudgetMultiple) {
		t.Fatalf("默认档预算必须＝首翻用量×默认 %d 倍，实得 %d", DefaultRepairTokenBudgetMultiple, got)
	}
	t.Setenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE", "2")
	if got := e.repairTokenBudgetCap(ctx, 10000); got != 20000 {
		t.Fatalf("env 覆盖未生效，实得 %d", got)
	}
	// 脏值折回默认：倍数配成 0/负数＝一次补译都不许跑，属误配不是意图
	for _, bad := range []string{"0", "-3", "abc"} {
		t.Setenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE", bad)
		if got := e.repairTokenBudgetCap(ctx, 10000); got != int64(10000*DefaultRepairTokenBudgetMultiple) {
			t.Errorf("脏值 %q 必须折回默认倍数，实得 %d", bad, got)
		}
	}
	// 首翻零用量（全部命中缓存/术语直出）不许算出 0 预算，否则任何一次补译都被判"超支"
	t.Setenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE", "")
	if got := e.repairTokenBudgetCap(ctx, 0); got != defaultSingleCallTokensFloor {
		t.Errorf("首翻零用量必须给绝对余量档 %d，实得 %d", defaultSingleCallTokensFloor, got)
	}
	if got := e.repairTokenBudgetCap(ctx, -1); got != defaultSingleCallTokensFloor {
		t.Errorf("负读数（理论不可能，取数处失守时的兜底）同样走余量档，实得 %d", got)
	}
}

// TestRepairTokenBudgetStopsAtLine 到线即停，且**首翻不受影响**：
// 用真实累加器走一遍「首翻 4.1 万 → 逐次补译到 16.4 万」的 UAT 量级形态。
func TestRepairTokenBudgetStopsAtLine(t *testing.T) {
	t.Setenv("FILE_REPAIR_TOKEN_BUDGET_MULTIPLE", "")
	e := &Engine{}
	ctx := e.WithUsageRecorder(context.Background())

	// 首翻：2 次整块调用 × 约 2.06 万 token（UAT 工单 115 的实测单行量级）
	addTokens(t, ctx, 20600, 600)
	firstPass, ok := collectorTokens(ctx)
	if !ok || firstPass != 21200 {
		t.Fatalf("首翻读数没归集，实得 %d（ok=%v）", firstPass, ok)
	}
	budget := e.repairTokenBudgetCap(ctx, firstPass)
	if want := int64(21200 * DefaultRepairTokenBudgetMultiple); budget != want {
		t.Fatalf("预算线取错，期望 %d 实得 %d", want, budget)
	}
	// 预算线之内：补译照常（这是交付质量的地盘，闸门只许管失控）
	for i := 0; i < DefaultRepairTokenBudgetMultiple-1; i++ {
		addTokens(t, ctx, 20000, 600)
		if over, spent := e.repairBudgetSpent(ctx, budget); over {
			t.Fatalf("第 %d 次补译尚在预算内却被拦（spent=%d budget=%d）", i+1, spent, budget)
		}
	}
	// 到线：立即判停，且带得出当前读数（出声时要用）
	addTokens(t, ctx, 20000, 600)
	over, spent := e.repairBudgetSpent(ctx, budget)
	if !over {
		t.Fatalf("已到线却判未超支（spent=%d budget=%d）", spent, budget)
	}
	if spent < budget {
		t.Errorf("出声用的读数必须≥预算线，实得 spent=%d budget=%d", spent, budget)
	}
	warning := e.warnRepairBudgetStopped(ctx, "en", spent, budget, 3)
	for _, want := range []string{"en", "3", "token", "预算"} {
		if !strings.Contains(warning, want) {
			t.Errorf("客户面文案缺 %q（这条是自动停补唯一可见出口）：%q", want, warning)
		}
	}
	if strings.Contains(warning, "失败") || strings.Contains(warning, "退款") {
		t.Errorf("成本闸门不是失败判据（F-01 口径：补不回不判失败、不退款），文案越界：%q", warning)
	}
}

// TestRepairTokenBudgetWiredInFileChain 守卫层：预算复核必须真接在文件链的重试腿上。
// 判的是**调用形态**（repairBudgetSpent 的判停 + stopForTokenBudget 的出声 + 首翻之后取基准），
// 只测上面那几个函数判不出「算好了预算却一行都没查」——那正是本缺陷现网藏住的形态。
func TestRepairTokenBudgetWiredInFileChain(t *testing.T) {
	src, err := os.ReadFile("file.go")
	if err != nil {
		t.Fatalf("读 file.go 失败，守卫腿无对象：%v", err)
	}
	text := stripFileComments(string(src))
	// ① 基准必须在首翻之后取（wg.Wait() 之后、per-language 循环之前才算得到首翻实际用量）
	if !strings.Contains(text, "firstPassTokens, hasUsage := collectorTokens(ctx)") {
		t.Fatal("没在重试腿之前取首翻用量基准，预算无从算起")
	}
	// ② 判定只有一处咽喉（闭包内）：三个点位各自复制一次判定＝迟早三份判据打架，
	//    而判定被摘掉＝预算算好了却一行都不查（本缺陷现网藏住的形态）。
	if n := strings.Count(text, "e.repairBudgetSpent(ctx, repairBudget)"); n != 1 {
		t.Errorf("预算判定必须收在唯一咽喉里，期望 1 处，实得 %d", n)
	}
	// ③ 三处复核点位：硬闸轮首 / 硬闸逐段之间 / F-01 追加补译轮首。
	//    按 `if stopForTokenBudget(` 的**调用形态**数——只数裸名会把"调了但没接返回值"
	//    （不 break，等于没拦）也算成合法点位，那正是判据该红的那一档。
	if n := strings.Count(text, "if stopForTokenBudget(lc,"); n != 3 {
		t.Errorf("文件链上的预算复核点位数期望 3（硬闸轮首＋逐段＋追加补译轮首），实得 %d", n)
	}
	// 出声并进客户/审批面必须**按这一句的完整形态**判：file.go 里另有原始漏译告警也写
	// `leakWarnings = append(leakWarnings,`，只数那半截会被它喂饱（M5 变异实测假绿）。
	if !strings.Contains(text, "leakWarnings = append(leakWarnings, e.warnRepairBudgetStopped(ctx, lc, spent, repairBudget, remaining))") {
		t.Error("预算停补的文案没并进 leakWarnings ⇒ 客户/审批台看不见停止原因")
	}
	// ④ 负向射程：取基准之前的那一段（首翻/校对腿）不许出现预算判定——
	//    把闸门装在交付本身上，客户会拿到空译文。
	firstPassSection := text
	if i := strings.Index(text, "firstPassTokens, hasUsage := collectorTokens(ctx)"); i >= 0 {
		firstPassSection = text[:i]
	} else {
		t.Fatal("取首翻基准那一行不在源码里（上面的①已红，此处再挡一次射程空转）")
	}
	if strings.Contains(firstPassSection, "repairBudgetSpent") || strings.Contains(firstPassSection, "repairTokenBudgetCap") {
		t.Error("预算判停出现在首翻之前＝把闸门装在了交付本身上（客户会拿到空译文）")
	}
}

// stripFileComments 剥 // 行注释：本文件与 file.go 的说明注释里故意写着判据字面量，
// 不剥就会被自己新写的注释喂饱（AGENTS §一·5 那条负向锁自伤的坑）。
func stripFileComments(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
