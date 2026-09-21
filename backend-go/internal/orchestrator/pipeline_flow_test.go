// ============ 本文件职责中文说明 ============
// ★ 任务 #56 最后一块（2026-09-23，评审报告 §4.1-5 / §4.4）：工单流水线「**装配**」回归。
//
// 报告实测：此前**全部** *_test.go 里 grep `Executor.Execute` 命中数为 0 —— 叶子函数
// （gate/qa/postprocess）有 30+ 断言，但「谁决定重翻、谁决定放行、谁决定转人工、
// 跳过条件是什么」这条编排接线没有任何回归保护。这正是「三条写入路径只有两条有守卫」
// 这类缺陷能长期潜伏的根因：闸门本身的单测全绿，接线漏了没人红。
//
// 本文件用**可控假步骤**驱动真实 Executor.Execute（不打 LLM、不碰网络），钉住执行器语义：
//  1. 10 步按 store.DefaultFlowSteps 既定顺序执行（顺序变了 = 硬闸跑到产物写回之后，闸门失效）；
//  2. NewWorkflow 必须为每个默认步骤注册执行器（漏注册 ⇒ Execute 静默按「无执行器」跳过，
//     工单显示成功却根本没过闸门——这是整条流水线最危险的失效模式）；
//  3. 步骤失败的短路语义（后续步骤不得执行 + 工单置 rejected + failed 轨迹 + 错误回传）；
//  4. 三种「跳过」口径：租户 flow_config 关闭 / RegisterSkip 条件跳过 / 未注册执行器容错，
//     共同点必须是「跳过≠失败」，工单不得被误判 rejected；
//  5. 默认流程不启用补偿重试（失败即止损，不静默重跑步骤，防重复计费/重复写库）；
//  6. 步骤内部 panic 被 runGuarded 兜成该步失败，工单状态与轨迹照常收口（不卡 running）；
//  7. ctx 取消立即中止，不再执行任何后续步骤；
//  8. 模式旁路（fast）必须落 mode_override 审计轨迹（P0-5：旁路不得静默）。
//
// 真实步骤函数（runKBMatch/runGate/runQA/runApproval/runFeedback）的装配断言
// 见 pipeline_workflow_test.go；本文件只钉执行器，两者互不重复。
//
// 方言自钉 sqlite（AGENTS.md 一.4）：config.Default() 会副作用写全局 config.C，
// 不钉死会在 run_uat 的 PG 模式下把方言泄漏给本包内存库用例，产生假红。
// ========================================
package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// pfStore 建一个自钉 SQLite 方言的内存平台存储（工单/轨迹/配置表齐备）。
// 参数：t。返回：*store.Store（测试结束自动关连接）。
func pfStore(t *testing.T) *store.Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 本包测试固定 SQLite（防 PG 矩阵 env 泄漏方言）
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st
}

// pfTicket 建一张「内部人工工单」：CreatedBy>0 且不设 fast，
// 使 applyModeOverride 不产生任何旁路——这样本文件测到的步骤序列就是租户配置的原样。
func pfTicket(t *testing.T, st *store.Store) *store.Ticket {
	t.Helper()
	tk, err := st.CreateTicket(1, 7, "装配回归单", "本系统支持多格式文件翻译能力", "", "en")
	if err != nil {
		t.Fatalf("创建工单失败: %v", err)
	}
	return tk
}

// pfSpy 步骤探针：记录调用顺序、每步调用次数，并可按步骤注入错误/panic。
type pfSpy struct {
	mu     sync.Mutex
	order  []string          // 实际执行顺序（含重复）
	counts map[string]int    // 步骤 → 被调用次数
	failAt map[string]error  // 步骤 → 返回错误
	panics map[string]string // 步骤 → panic 内容
	hook   map[string]func() // 步骤 → 执行前副作用（用于中途 cancel ctx）
}

// newSpy 创建探针。
func newSpy() *pfSpy {
	return &pfSpy{counts: map[string]int{}, failAt: map[string]error{}, panics: map[string]string{}, hook: map[string]func(){}}
}

// fn 返回可注册到 Executor 的步骤函数（按 key 记录并执行注入行为）。
func (s *pfSpy) fn(key string) RunFunc {
	return func(ctx context.Context, ticket *store.Ticket) error {
		s.mu.Lock()
		s.order = append(s.order, key)
		s.counts[key]++
		hook := s.hook[key]
		s.mu.Unlock()
		if hook != nil {
			hook()
		}
		if msg, ok := s.panics[key]; ok {
			panic(msg)
		}
		if err, ok := s.failAt[key]; ok {
			return err
		}
		return nil
	}
}

// called 取调用顺序快照（加锁读，避免与步骤 goroutine 竞争）。
func (s *pfSpy) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.order...)
}

// times 取某步被调用次数。
func (s *pfSpy) times(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[key]
}

// pfStateMap 读工单步骤轨迹，返回 step → 最新状态（同步骤 UPSERT 只保留一行）。
func pfStateMap(t *testing.T, st *store.Store, ticketID int64) map[string]*store.TicketState {
	t.Helper()
	rows, err := st.TicketStates(ticketID)
	if err != nil {
		t.Fatalf("读取步骤轨迹失败: %v", err)
	}
	out := map[string]*store.TicketState{}
	for _, r := range rows {
		out[r.Step] = r
	}
	return out
}

// pfExec 组装一个「假步骤全量注册」的执行器：默认 10 步逐一注册探针函数。
func pfExec(st *store.Store, spy *pfSpy) *Executor {
	ex := NewExecutor(st)
	for _, d := range store.DefaultFlowSteps {
		ex.Register(d.Key, spy.fn(d.Key))
	}
	return ex
}

// TestExecuteRunsDefaultTenStepsInOrder ★ 核心装配断言：Execute 必须按
// store.DefaultFlowSteps 的既定顺序调用全部 10 步（kb_match→…→feedback）。
// 改坏了会怎样：任何一步被调换顺序（如 gate 跑到 feedback 之后、approval 跑到 review 之前），
// 这里立刻红灯——顺序即语义：硬闸必须在人工审批之前、自迭代必须在批准之后。
func TestExecuteRunsDefaultTenStepsInOrder(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)

	var seen []string
	if err := ex.Execute(context.Background(), tk, func(step string, ok bool, errMsg string) {
		seen = append(seen, step+"|"+map[bool]string{true: "ok", false: "fail"}[ok])
	}); err != nil {
		t.Fatalf("全流程应成功返回 nil，实得 %v", err)
	}

	want := make([]string, 0, len(store.DefaultFlowSteps))
	for _, d := range store.DefaultFlowSteps {
		want = append(want, d.Key)
	}
	got := spy.called()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("步骤执行顺序不符\n 实际: %v\n 期望: %v", got, want)
	}
	// 每步恰好一次（多了说明被静默重试，少了说明被跳过）
	for _, k := range want {
		if n := spy.times(k); n != 1 {
			t.Errorf("步骤 %s 应恰好执行 1 次，实得 %d", k, n)
		}
	}
	// 进度回调必须逐步收到成功事件（前端流程条依赖它）
	if len(seen) != len(want) {
		t.Fatalf("onStep 回调次数 %d ≠ 步骤数 %d", len(seen), len(want))
	}
	for _, s := range seen {
		if !strings.HasSuffix(s, "|ok") {
			t.Errorf("onStep 出现非成功事件: %s", s)
		}
	}
	// 轨迹逐步骤 success（审批台/工单详情据此判断「本单过了哪些闸门」）
	states := pfStateMap(t, st, tk.ID)
	for _, k := range want {
		got := states[k]
		if got == nil || got.Status != "success" {
			t.Errorf("步骤 %s 轨迹应为 success，实得 %+v", k, got)
		}
	}
}

// TestWorkflowRegistersEveryDefaultStep ★ 最危险失效模式的守卫：NewWorkflow 必须为
// 每个 DefaultFlowSteps 键注册执行器。漏注册时 Execute 走「无执行器 → skipped」容错分支，
// **不报错**，工单照样 completed：表现为「全部语言过了硬闸」，实际那道闸根本没跑。
// 改坏了会怎样：新增一个流程步骤（只加 DefaultFlowSteps 忘了 registerSteps）或改了下线
// 步骤的 key（gate → gates），这条断言立刻红灯，而不是等生产漏检。
func TestWorkflowRegistersEveryDefaultStep(t *testing.T) {
	st := pfStore(t)
	w := NewWorkflow(st, nil, nil, nil)
	if w == nil || w.Executor == nil {
		t.Fatal("NewWorkflow 应返回可用工作流")
	}
	for _, d := range store.DefaultFlowSteps {
		if _, ok := w.Executor.Runs[d.Key]; !ok {
			t.Errorf("步骤 %s(%s) 未注册执行器：Execute 会静默跳过它，闸门形同虚设", d.Key, d.Name)
		}
	}
	// 反向：注册表里不得存在 DefaultFlowSteps 之外的野步骤（否则租户流程面板看不到、无法关停）
	if len(w.Executor.Runs) != len(store.DefaultFlowSteps) {
		t.Errorf("注册的步骤数 %d ≠ 默认步骤数 %d，存在未纳入流程配置的步骤", len(w.Executor.Runs), len(store.DefaultFlowSteps))
	}
}

// TestExecuteFailureShortCircuitsLaterSteps gate 失败必须：短路后续步骤 + 工单 rejected
// + failed 轨迹 + 错误带步骤中文名。改坏了会怎样：若短路逻辑失效，approval/feedback 会在
// 「闸门未过」的译文上继续转人工/写 TM；若状态不回写，工单永远停在 in_progress 无人知晓。
func TestExecuteFailureShortCircuitsLaterSteps(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	spy.failAt["gate"] = errors.New("Gate 校验失败 [en]: 非源语言")
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)

	var failedStep, failedMsg string
	err := ex.Execute(context.Background(), tk, func(step string, ok bool, errMsg string) {
		if !ok {
			failedStep, failedMsg = step, errMsg
		}
	})
	if err == nil {
		t.Fatal("gate 失败时 Execute 必须返回错误（不得静默放行）")
	}
	if !strings.Contains(err.Error(), "ConstraintGate 硬校验") {
		t.Errorf("错误应带步骤中文名便于定位，实得 %v", err)
	}
	// gate 之后的 4 步一次都不许执行
	for _, k := range []string{"culture_gate", "qa", "approval", "feedback"} {
		if n := spy.times(k); n != 0 {
			t.Errorf("gate 失败后步骤 %s 不得执行，实得 %d 次", k, n)
		}
	}
	// gate 之前的步骤正常执行完毕
	for _, k := range []string{"kb_match", "ai_initial", "evals_initial", "review", "evals_review"} {
		if n := spy.times(k); n != 1 {
			t.Errorf("前置步骤 %s 应执行 1 次，实得 %d", k, n)
		}
	}
	// 工单被置 rejected 并写明原因（审批台据此展示「哪一步失败」）
	fresh, gerr := st.GetTicket(tk.ID, tk.TenantID)
	if gerr != nil {
		t.Fatalf("回读工单失败: %v", gerr)
	}
	if fresh.Status != store.TicketRejected {
		t.Errorf("失败工单应为 rejected，实得 %s", fresh.Status)
	}
	if !strings.Contains(fresh.RejectReason, "Gate 校验失败") {
		t.Errorf("RejectReason 应保留原始失败原因，实得 %q", fresh.RejectReason)
	}
	// 轨迹：失败步骤 failed（带原因），后续步骤**不得留下任何行**（否则前端误判为已执行）
	states := pfStateMap(t, st, tk.ID)
	if states["gate"] == nil || states["gate"].Status != "failed" {
		t.Errorf("gate 轨迹应为 failed，实得 %+v", states["gate"])
	}
	if states["gate"] != nil && !strings.Contains(states["gate"].Payload, "非源语言") {
		t.Errorf("failed 轨迹应落原因，实得 %q", states["gate"].Payload)
	}
	for _, k := range []string{"culture_gate", "qa", "approval", "feedback"} {
		if _, ok := states[k]; ok {
			t.Errorf("短路后步骤 %s 不应有轨迹行", k)
		}
	}
	// 进度回调必须把失败步与原因透出去（SSE/工单详情的唯一失败信号）
	if failedStep != "gate" || !strings.Contains(failedMsg, "非源语言") {
		t.Errorf("onStep 未透出失败信息: step=%q msg=%q", failedStep, failedMsg)
	}
}

// TestExecuteDefaultFlowHasNoCompensationRetry 钉住「失败即止损」的当前接线：
// store.DefaultFlowSteps 没有任何步骤被标 compensable，Executor.GetFlow 也不产出该位，
// 因此失败步骤**只执行一次**、不自动重试。
// 为什么值得钉：flow.go 文件头声称「失败自动补偿重试（≤2 次）」，但接线从未置位——
// 一旦有人按注释把 Compensable 打开，重译/写库类步骤会在无人可见的情况下重复执行
// （重复计费、重复 TM 回写），本断言会红灯逼他补审计轨迹。已作为文档漂移上报主代理。
func TestExecuteDefaultFlowHasNoCompensationRetry(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	spy.failAt["review"] = errors.New("审校失败")
	ex := pfExec(st, spy)
	// 执行器构建出的流程里不得有任何步骤带补偿位
	for _, s := range ex.GetFlow(1).Steps {
		if s.Compensable {
			t.Errorf("默认流程步骤 %s 不应启用补偿重试（无审计轨迹配套）", s.Key)
		}
	}
	if err := ex.Execute(context.Background(), pfTicket(t, st), nil); err == nil {
		t.Fatal("失败步骤应短路返回错误")
	}
	if n := spy.times("review"); n != 1 {
		t.Errorf("review 失败后应只执行 1 次（当前接线不重试），实得 %d", n)
	}
}

// TestExecuteTenantFlowConfigDisablesStep 租户 flow_config 关闭某步 = 该步不执行、
// 轨迹记 skipped、**流程整体仍成功**（关闸是租户的权利，不能把工单打回 rejected）。
// 改坏了会怎样：若 skipped 被当成失败，租户关一个可选步骤就整单报错；若反过来把
// 「关闭」实现成「照常执行」，租户配置失效（流程面板成为摆设）。
func TestExecuteTenantFlowConfigDisablesStep(t *testing.T) {
	st := pfStore(t)
	ts, err := tenant.NewStore(st.DB())
	if err != nil {
		t.Fatalf("创建租户存储失败: %v", err)
	}
	tt, err := ts.Create("pf-cfg", "流程配置租户", "", "")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	if err := ts.SetFlowConfig(tt.ID, tenant.FlowConfig{Steps: map[string]bool{"gate": false}}); err != nil {
		t.Fatalf("写入流程配置失败: %v", err)
	}

	spy := newSpy()
	ex := pfExec(st, spy)
	ex.Ten = ts
	tk, err := st.CreateTicket(tt.ID, 7, "租户关闸单", "本系统支持多格式文件翻译能力", "", "en")
	if err != nil {
		t.Fatalf("创建工单失败: %v", err)
	}
	if err := ex.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("租户关闭单步不应导致整单失败: %v", err)
	}
	if n := spy.times("gate"); n != 0 {
		t.Errorf("被租户关闭的 gate 不得执行，实得 %d 次", n)
	}
	if n := spy.times("qa"); n != 1 {
		t.Errorf("gate 之后的步骤仍应照常执行，qa 实得 %d 次", n)
	}
	states := pfStateMap(t, st, tk.ID)
	if states["gate"] == nil || states["gate"].Status != "skipped" {
		t.Errorf("gate 轨迹应为 skipped，实得 %+v", states["gate"])
	}
	fresh, _ := st.GetTicket(tk.ID, tt.ID)
	if fresh.Status == store.TicketRejected {
		t.Error("跳过步骤不得把工单判为 rejected")
	}
}

// TestExecuteSkipFuncAndMissingRunner 另外两条「跳过」通道：
//   - RegisterSkip 判定为真 → 该步不执行、记 skipped、流程继续；
//   - 未注册执行器 → 容错跳过而不是 panic / 报错卡死。
//
// 改坏了会怎样：跳过判断若被当成失败，OpenAPI 精简链路会整批 reject；
// 若未注册执行器改成报错，历史工单（流程定义多于已注册步骤）将永久无法重跑。
func TestExecuteSkipFuncAndMissingRunner(t *testing.T) {
	st := pfStore(t)

	// ① 条件跳过
	spy := newSpy()
	ex := pfExec(st, spy)
	ex.RegisterSkip("culture_gate", func(ctx context.Context, ticket *store.Ticket) bool { return true })
	tk1 := pfTicket(t, st)
	if err := ex.Execute(context.Background(), tk1, nil); err != nil {
		t.Fatalf("条件跳过不应使流程失败: %v", err)
	}
	if n := spy.times("culture_gate"); n != 0 {
		t.Errorf("已声明跳过的步骤不得执行，实得 %d 次", n)
	}
	if s := pfStateMap(t, st, tk1.ID)["culture_gate"]; s == nil || s.Status != "skipped" {
		t.Errorf("条件跳过应记 skipped 轨迹，实得 %+v", s)
	}
	if n := spy.times("qa"); n != 1 {
		t.Errorf("跳过一步后续步骤必须继续（qa），实得 %d 次", n)
	}

	// ② 未注册执行器：一个步骤都没注册 → 逐个 skipped 且整体成功
	ex2 := NewExecutor(st)
	tk2 := pfTicket(t, st)
	if err := ex2.Execute(context.Background(), tk2, nil); err != nil {
		t.Fatalf("未注册执行器应容错跳过，实得 %v", err)
	}
	states := pfStateMap(t, st, tk2.ID)
	for _, d := range store.DefaultFlowSteps {
		if s := states[d.Key]; s == nil || s.Status != "skipped" {
			t.Errorf("步骤 %s 应记 skipped，实得 %+v", d.Key, s)
		}
	}
}

// TestExecutePanicBecomesStepFailure 步骤内部 panic 必须被兜成**该步失败**：
// 工单置 rejected、轨迹写 failed（含 panic 文本）、后续步骤不执行、错误回传调用方。
// 改坏了会怎样（去掉 runGuarded 的真实后果）：panic 沿 goroutine 冒到 worker，
// 该步轨迹永久停在 running、工单停在 in_progress，只能等 20 分钟卡死巡检重排，
// 用户侧表现为「无限处理中」，且审批台看不到任何失败原因。
func TestExecutePanicBecomesStepFailure(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	spy.panics["qa"] = "模拟步骤内部空指针"
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)

	var cbStep, cbMsg string
	var cbOK = true
	err := ex.Execute(context.Background(), tk, func(step string, ok bool, errMsg string) {
		if !ok {
			cbStep, cbMsg, cbOK = step, errMsg, false
		}
	})
	if err == nil {
		t.Fatal("步骤 panic 必须让 Execute 返回错误（不得当成成功）")
	}
	if !strings.Contains(err.Error(), "模拟步骤内部空指针") {
		t.Errorf("错误应带 panic 现场，实得 %v", err)
	}
	if n := spy.times("approval"); n != 0 {
		t.Errorf("panic 后后续步骤不得执行，approval 实得 %d 次", n)
	}
	states := pfStateMap(t, st, tk.ID)
	if s := states["qa"]; s == nil || s.Status != "failed" {
		t.Errorf("panic 步骤轨迹应为 failed（不能停在 running），实得 %+v", s)
	}
	fresh, _ := st.GetTicket(tk.ID, tk.TenantID)
	if fresh.Status != store.TicketRejected {
		t.Errorf("panic 工单应置 rejected，实得 %s", fresh.Status)
	}
	if cbOK || cbStep != "qa" || !strings.Contains(cbMsg, "模拟步骤内部空指针") {
		t.Errorf("onStep 回调未收到 panic 失败事件: step=%q msg=%q", cbStep, cbMsg)
	}
	// panic 前已成功的步骤必须保持成功轨迹（护栏不得回滚已落库的进度）
	if s := states["gate"]; s == nil || s.Status != "success" {
		t.Errorf("前置步骤轨迹应保持 success，实得 %+v", s)
	}
}

// TestExecuteContextCancelAbortsPipeline 取消（用户中止/超时）后不得再执行任何步骤，
// 且必须原样返回 ctx.Err() 让上游区分「取消」与「失败」。
// 改坏了会怎样：取消被吞 → 已计费的重译/写库步骤继续跑完，白烧 token 并写入被废弃的译文。
func TestExecuteContextCancelAbortsPipeline(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spy.hook["review"] = func() { cancel() } // 审校开始时取消：其后的步骤都不得执行
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)

	err := ex.Execute(ctx, tk, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消应原样返回 context.Canceled，实得 %v", err)
	}
	for _, k := range []string{"evals_review", "gate", "culture_gate", "qa", "approval", "feedback"} {
		if n := spy.times(k); n != 0 {
			t.Errorf("取消后步骤 %s 不得执行，实得 %d 次", k, n)
		}
	}
	// 取消不是失败：工单不得被置 rejected（worker 据此决定重排续跑）
	fresh, _ := st.GetTicket(tk.ID, tk.TenantID)
	if fresh.Status == store.TicketRejected {
		t.Error("取消不得把工单判为 rejected")
	}
}

// TestExecuteModeOverrideIsAudited fast 工单关闭闸门时，必须在轨迹里留下 mode_override 行
// （P0-5 整改：旁路是设计内行为，但必须对审批台与租户可见）。
// 改坏了会怎样：旁路不再留痕 ⇒ 审批员看到「已过全部闸门」的工单实际跳过了硬闸，
// 而这正是 §4.1-5 说的「零件有测、装配无测」无法被发现的原因。
func TestExecuteModeOverrideIsAudited(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)
	tk.Mode = "fast"
	if err := st.UpdateTicket(tk); err != nil {
		t.Fatalf("更新工单模式失败: %v", err)
	}
	if err := ex.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("fast 流程应成功: %v", err)
	}
	states := pfStateMap(t, st, tk.ID)
	row := states["mode_override"]
	if row == nil {
		t.Fatal("fast 旁路必须落 mode_override 轨迹")
	}
	var rec struct {
		Bypass string `json:"bypass"`
	}
	if err := json.Unmarshal([]byte(row.Payload), &rec); err != nil || rec.Bypass != "fast" {
		t.Errorf("mode_override 轨迹应记 bypass=fast，实得 %q (err=%v)", row.Payload, err)
	}
	// fast 保留的三步必须真被执行，关掉的闸门必须记 skipped
	for _, k := range []string{"ai_initial", "review", "qa"} {
		if n := spy.times(k); n != 1 {
			t.Errorf("fast 应保留步骤 %s，实得 %d 次", k, n)
		}
	}
	for _, k := range []string{"kb_match", "gate", "culture_gate", "feedback"} {
		if n := spy.times(k); n != 0 {
			t.Errorf("fast 应跳过步骤 %s，实得 %d 次", k, n)
		}
		if s := states[k]; s == nil || s.Status != "skipped" {
			t.Errorf("fast 跳过的步骤 %s 应记 skipped 轨迹，实得 %+v", k, s)
		}
	}
}
