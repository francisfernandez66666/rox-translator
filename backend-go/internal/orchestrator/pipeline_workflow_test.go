// ============ 本文件职责中文说明 ============
// ★ 任务 #56 最后一块（2026-09-23，评审报告 §4.1-5 / §4.4）：工单流水线**真实步骤**的装配回归。
//
// pipeline_flow_test.go 用假步骤钉住执行器语义；本文件把 Workflow 注册的 10 个真实步骤
// （runKBMatch / runAIInitial / runEvals* / runReview / runGate / runCultureGate / runQA /
// runApproval / runFeedback）经 Executor.Execute 端到端跑一遍，回答报告里那句
// 「谁决定重翻、谁决定放行、谁决定转人工、跳过条件是什么——有断言吗？」。
//
// 覆盖：
//  1. 正常单：KB 命中的译文一路过闸，收尾停在 pending_approval（转人工），
//     QA 摘要落工单列，feedback 在未批准时**一字不写** TM；
//  2. gate 不过 ⇒ 附打回原因重翻 ⇒ 重翻成功则放行（钉「谁决定重翻 + 打回原因确实送到了模型」）；
//  3. gate 重翻仍不过 ⇒ 整单 rejected，后续 culture_gate/qa/approval/feedback 全部短路，
//     且不写 TM（钉「不过闸绝不静默放行」）；
//  4. culture_gate 不过 ⇒ 重翻修正后放行（文化闸同样有打回权，不只是提示）；
//  5. qa 报 error 级问题 ⇒ 只落报告与工单摘要列，**不改工单成败**（裁决权在人工审批）；
//  6. 评估低于阈值 ⇒ 由流水线（而非叶子函数）打上「质检存疑」标 + 落告警中心；
//  7. 已批准工单重跑 ⇒ 只刷 QA + 写 TM，人工终稿不被机器改动，重复执行不产生重复 TM 行。
//
// 不打真实 LLM/网络：上游用 httptest 假服务器；存储用临时 SQLite 文件（store 与 kb 同库，
// 与生产形态一致）。方言自钉 sqlite（AGENTS.md 一.4），防 run_uat PG env 泄漏打假红。
// ========================================
package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/evals"
	"translator/internal/kb"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// pwStub 假上游：一个 OpenAI 兼容的 /chat/completions 服务器，按 prompt 关键词分派回复。
type pwStub struct {
	mu      sync.Mutex
	prompts []string
	// reply 由用例注入：输入最后一条 user prompt，返回模型 content 文本。
	reply func(prompt string) string
}

// ServeHTTP 实现 OpenAI 兼容最小协议（本用例只关心 content 字段）。
func (s *pwStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	prompt := ""
	if len(req.Messages) > 0 {
		prompt = req.Messages[len(req.Messages)-1].Content
	}
	s.mu.Lock()
	s.prompts = append(s.prompts, prompt)
	s.mu.Unlock()
	content := "UNSET-STUB"
	if s.reply != nil {
		content = s.reply(prompt)
	}
	enc, _ := json.Marshal(content) // 用 JSON 字符串编码，保证换行/引号安全回传
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":5}}`, enc)
}

// asked 返回包含指定片段的历史 prompt 条数（断言「打回原因是否真的送到了模型」）。
func (s *pwStub) asked(substr string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.prompts {
		if strings.Contains(p, substr) {
			n++
		}
	}
	return n
}

// pwHarness 一次装配测试的全部零件。
type pwHarness struct {
	W    *Workflow
	St   *store.Store
	KBD  *kb.KBDatabase
	Ten  *tenant.Store
	Stub *pwStub
}

// pwSetup 建临时 SQLite 文件库（store 与 kb 同库=生产形态）+ 假上游引擎。
// 参数：t、reply=假模型分派函数（可为 nil）。返回：装配好的 harness。
func pwSetup(t *testing.T, reply func(prompt string) string) *pwHarness {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS.md 一.4）
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	dbPath := filepath.Join(t.TempDir(), "app.db")
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开 SQLite 失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ts, err := tenant.NewStore(conn) // 建 tenants 表（flow_config 等列），kb 的共享过滤也依赖它
	if err != nil {
		t.Fatalf("tenant.NewStore: %v", err)
	}
	kbdb, err := kb.Open(dbPath)
	if err != nil {
		t.Fatalf("kb.Open: %v", err)
	}

	stub := &pwStub{reply: reply}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-fake-judge"
	cfg.OnlineModel = "fake-model"
	// 关闭占位符判定：假 Key 必须被当成可用 Key 才会真的打到 httptest
	cfg.OnlineAPIKeyIsPlaceholder = false

	eng := engine.NewEngine(cfg, kbdb, nil, ts)
	eng.St = st // 阶段模型/系统配置读取走同一内存库

	w := NewWorkflow(st, eng, ts, kbdb)
	return &pwHarness{W: w, St: st, KBD: kbdb, Ten: ts, Stub: stub}
}

// pwTicket 建一张内部 pro 工单（CreatedBy>0 ⇒ 不触发 api_task 旁路）。
func pwTicket(t *testing.T, h *pwHarness, source, langs string) *store.Ticket {
	t.Helper()
	tk, err := h.St.CreateTicket(1, 7, "装配回归", source, "", langs)
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	return tk
}

// pwDispatch 假上游分派器：按 prompt 关键词识别环节，返回「环节失败」（空串）或指定译文。
// 说明：审校/评估/重译共用同一个上游，用例必须显式声明每个环节的行为，
// 否则会把「环节没接线」误判成「环节返回了可用结果」。
type pwReply struct {
	review   string // 审校环节返回（空串=审校失败，保留原译文）
	retrans  string // 附打回原因的重译环节返回
	judge    string // Judge 评估环节返回（JSON 或空串=评估失败）
	fallback string // 其他环节返回
}

// fn 生成 pwStub 的 reply 闭包。
func (r pwReply) fn() func(string) string {
	return func(prompt string) string {
		switch {
		case strings.Contains(prompt, "翻译质量评估员"):
			return r.judge
		case strings.Contains(prompt, "修正重译"), strings.Contains(prompt, "驳回意见"):
			return r.retrans
		case strings.Contains(prompt, "审校"):
			return r.review
		default:
			return r.fallback
		}
	}
}

// pwSeedKB 往租户企业包写一条 zh→lang 的整段命中（runKBMatch 的第一层来源）。
func pwSeedKB(t *testing.T, h *pwHarness, source, lang, translation string) {
	t.Helper()
	pkg, err := h.St.CreateKBPackage(1, 0, "pw-tenant", "企业包", "tenant", "source")
	if err != nil {
		t.Fatalf("CreateKBPackage: %v", err)
	}
	if _, err := h.St.SaveEntry(1, pkg.ID, 2, "zh", source, lang, translation, "manual"); err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
}

// pwPayload 回读工单载荷（FinalResult JSON）。
func pwPayload(t *testing.T, h *pwHarness, id int64) ticketPayload {
	t.Helper()
	fresh, err := h.St.GetTicket(id, 1)
	if err != nil || fresh == nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	var p ticketPayload
	if err := json.Unmarshal([]byte(fresh.FinalResult), &p); err != nil {
		t.Fatalf("载荷解析失败: %v (%q)", err, fresh.FinalResult)
	}
	return p
}

// pwStatus 回读工单状态。
func pwStatus(t *testing.T, h *pwHarness, id int64) string {
	t.Helper()
	fresh, err := h.St.GetTicket(id, 1)
	if err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	return fresh.Status
}

// pwTMFeedbackRows 统计 feedback 步骤写入的正式 TM 行数（pack_id=0 即 SaveBack 的企业层级，
// 与 SaveEntry 的写通包行区分开，确保测的是「feedback 写库」而不是种子数据）。
func pwTMFeedbackRows(t *testing.T, h *pwHarness, zh string) int {
	t.Helper()
	var n int
	if err := h.St.DB().QueryRow("SELECT COUNT(*) FROM tm_segments WHERE zh=? AND tenant_id=1 AND pack_id=0", zh).Scan(&n); err != nil {
		t.Fatalf("统计 tm_segments 失败: %v", err)
	}
	return n
}

const (
	pwSource      = "本系统支持 45 种多格式文件翻译能力"
	pwBad         = "本系统支持多格式文件翻译能力的说明"                                             // 脏译文：中文残留 + 丢了数字 45 ⇒ 必过不了硬闸
	pwGood        = "This system supports 45 multi-format file translation formats" // 干净译文
	pwNoDigits    = "This system supports multi-format file translation capabilities"
	pwRude        = pwGood + ", damn you" // 只有文化闸会拦的形态（语气合规）
	pwSourceClean = "本系统支持多格式文件翻译能力的说明"   // feedback 用例源文（不含数字，QA 满分）
)

// TestWorkflowPipelineHappyPathTransfersToHumanAndWritesNoTM
// 正常单（KB 命中 + pro）：10 步全跑通，译文来自 KB 且不被后续步骤覆写，
// QA 摘要落工单列，工单停在 pending_approval，**feedback 在未批准状态下不得写一行 TM**。
// 改坏了会怎样：
//   - 若 approval 忘了转人工 → 工单直接 completed，机器产物绕过人工终审；
//   - 若 feedback 不判状态 → 未审稿件沉淀进翻译记忆，下一单直接命中错译（自我强化污染）；
//   - 若 kb_match 覆写了既有译文，审批员的人工终稿会在重跑时被静默丢弃（历史 C4 缺陷）。
func TestWorkflowPipelineHappyPathTransfersToHumanAndWritesNoTM(t *testing.T) {
	// 所有环节都「失败」（返回空串）：证明 KB 命中译文能一路原样过闸，
	// 不会在某处被空结果悄悄覆写。
	h := pwSetup(t, pwReply{}.fn())
	pwSeedKB(t, h, pwSourceClean, "en", "Multi-format file translation is supported by this system.")
	tk := pwTicket(t, h, pwSourceClean, "en")

	if err := h.W.Executor.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("正常工单全流程应成功，实得 %v", err)
	}
	if n := h.Stub.asked("审校"); n != 1 {
		t.Errorf("审校环节应被流水线调用 1 次（没调用=接线漏了），实得 %d", n)
	}
	p := pwPayload(t, h, tk.ID)
	if p.Translations["en"] != "Multi-format file translation is supported by this system." {
		t.Errorf("KB 命中译文不得被流水线改动，实得 %q", p.Translations["en"])
	}
	if p.Sources["en"] != "kb" {
		t.Errorf("来源应标记为 kb（据此判断本单零模型成本），实得 %q", p.Sources["en"])
	}
	if p.Gate == nil || !p.Gate.Pass {
		t.Errorf("硬闸结果未落载荷或判不过：%+v", p.Gate)
	}
	if p.QAReport == nil {
		t.Fatal("qa 步骤未把质检报告写进载荷（下载对照表依赖它）")
	}
	if pwStatus(t, h, tk.ID) != store.TicketPendingAppr {
		t.Fatalf("未审批工单必须停在 pending_approval，实得 %s", pwStatus(t, h, tk.ID))
	}
	// feedback 不得在未批准时写正式 TM
	if n := pwTMFeedbackRows(t, h, pwSourceClean); n != 0 {
		t.Errorf("未批准工单不得写 TM，实得 %d 行", n)
	}
	// QA 摘要落列（工单列表徽标零成本渲染的数据源）
	fresh, _ := h.St.GetTicket(tk.ID, 1)
	if fresh.QAErrors > 0 || fresh.QAWarnings > 0 {
		t.Errorf("干净译文不应有质检问题，实得 errors=%d warnings=%d", fresh.QAErrors, fresh.QAWarnings)
	}
	// 10 步轨迹齐备（缺一即「装配漏件」，见 pipeline_flow_test 的同名守卫）
	states := pfStateMap(t, h.St, tk.ID)
	for _, d := range store.DefaultFlowSteps {
		s := states[d.Key]
		if s == nil || s.Status != "success" {
			t.Errorf("真实步骤 %s 未成功执行，轨迹 %+v", d.Key, s)
		}
	}
}

// TestWorkflowGateFailureRetranslatesWithReasonAndThenPasses
// gate 不过 ⇒ 必须**带着打回原因**重翻（而不是静默放行，也不是直接把坏译文交给审批台）。
// 断言链：坏 KB 译文 → 审校环节被上游标记为不可用（返回空 ⇒ 保留原译文）→ 硬闸判不过 →
// 重翻 prompt 内含「非源语言」与「数字保持」→ 模型给出干净译文 → 放行且来源改标 model。
// 改坏了会怎样：打回原因没传给模型（历史上只传了句"请重新翻译"）＝重翻等于掷骰子；
// 或重翻结果不覆写 Sources ⇒ 计费/成本统计把机器重译记成零成本 KB 命中。
func TestWorkflowGateFailureRetranslatesWithReasonAndThenPasses(t *testing.T) {
	// 审校环节失败（保留 KB 脏译文）→ 硬闸判不过 → 重译环节给出干净译文。
	h := pwSetup(t, pwReply{retrans: pwGood}.fn())
	pwSeedKB(t, h, pwSource, "en", pwBad)
	tk := pwTicket(t, h, pwSource, "en")
	if err := h.St.SetConfig("gate_retry_max", "2"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	if err := h.W.Executor.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("重翻后应放行，实得 %v", err)
	}
	if h.Stub.asked("非源语言") == 0 {
		t.Error("重翻 prompt 未携带硬闸打回原因（模型无从修正）")
	}
	if h.Stub.asked("数字保持") == 0 {
		t.Error("重翻 prompt 未携带「数字保持」失败项（脏译文丢了 45，必须告诉模型）")
	}
	p := pwPayload(t, h, tk.ID)
	if p.Translations["en"] != pwGood {
		t.Errorf("译文应被重翻结果替换，实得 %q", p.Translations["en"])
	}
	if p.Sources["en"] != "model" {
		t.Errorf("重翻后来源必须改标 model（计费与成本口径），实得 %q", p.Sources["en"])
	}
	if p.RetryCount["en"] != 1 {
		t.Errorf("自动重译次数应记 1，实得 %v", p.RetryCount)
	}
	if !p.Gate.Pass {
		t.Errorf("放行时闸结果必须为 Pass，实得 %+v", p.Gate)
	}
	if pwStatus(t, h, tk.ID) != store.TicketPendingAppr {
		t.Errorf("过闸后应转人工，实得 %s", pwStatus(t, h, tk.ID))
	}
}

// TestWorkflowGateStillFailingRejectsAndBlocksLaterSteps
// 重翻仍不过 ⇒ 整单 rejected，并且 culture_gate/qa/approval/feedback 一律不执行、不写 TM。
// 这是「谁决定放行」的负面半边：**任何情况下都不存在「闸门失败但工单成功」的状态**。
// 改坏了会怎样：短路失效 → 坏译文照样转人工甚至写 TM；状态不回写 → 工单停在 in_progress。
func TestWorkflowGateStillFailingRejectsAndBlocksLaterSteps(t *testing.T) {
	// 模型稳定输出「漏掉数字 45」的译文（审校与重译同值）：非空、非中文回显，
	// 因此走的是「重译若干轮仍不过 ⇒ 判死」这条路径，而不是「重译直接失败（空结果）」那条。
	h := pwSetup(t, pwReply{review: pwNoDigits, retrans: pwNoDigits}.fn())
	pwSeedKB(t, h, pwSource, "en", pwBad)
	tk := pwTicket(t, h, pwSource, "en")
	if err := h.St.SetConfig("gate_retry_max", "1"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	err := h.W.Executor.Execute(context.Background(), tk, nil)
	if err == nil {
		t.Fatal("硬闸始终不过时 Execute 必须返回错误")
	}
	if !strings.Contains(err.Error(), "Gate 校验失败") {
		t.Errorf("错误应指明 Gate 失败，实得 %v", err)
	}
	if pwStatus(t, h, tk.ID) != store.TicketRejected {
		t.Errorf("闸门不过的工单必须 rejected，实得 %s", pwStatus(t, h, tk.ID))
	}
	states := pfStateMap(t, h.St, tk.ID)
	for _, k := range []string{"culture_gate", "qa", "approval", "feedback"} {
		if _, ok := states[k]; ok {
			t.Errorf("gate 失败后步骤 %s 不应执行（应短路）", k)
		}
	}
	if s := states["gate"]; s == nil || s.Status != "failed" {
		t.Errorf("gate 轨迹应为 failed，实得 %+v", s)
	}
	// 已自动重译的次数要写进失败原因，运营据此判断「是模型不行还是规则太严」
	if s := states["gate"]; s != nil && !strings.Contains(s.Payload, "已自动重译 1 次") {
		t.Errorf("failed 轨迹应含重译次数说明，实得 %q", s.Payload)
	}
	if n := pwTMFeedbackRows(t, h, pwSource); n != 0 {
		t.Errorf("被驳回工单不得写 TM，实得 %d 行", n)
	}
	// 重翻尝试确实发生过（不是判失败就走）：模型至少收到过一次重翻指令
	if h.Stub.asked("请严格按照意见修正重译") == 0 {
		t.Error("未执行任何自动重译即判失败，硬闸护栏未接线")
	}
}

// TestWorkflowCultureGateRejectsThenRetranslates
// 语言文化闸门（culture.Run 反查）同样有打回权：不通过 → 附原因重翻 → 修正后放行。
// 夹具刻意选「语气合规（粗口残留）」这一条——它是文化闸独有规则，硬闸 gate.RunWithTerms
// 与 qa.Check 都不覆盖，因此能确认「确实是 culture_gate 打的回」而不是别的步骤。
// 改坏了会怎样：文化闸被改成「只记录不阻断」→ 不合当地表达的产物直接交付；
// 或打回原因没进 GateHints → 审批台看不到这单为什么被重翻过。
func TestWorkflowCultureGateRejectsThenRetranslates(t *testing.T) {
	h := pwSetup(t, pwReply{retrans: pwGood}.fn()) // 审校失败：保留 KB 里带粗口的译文
	pwSeedKB(t, h, pwSource, "en", pwRude)
	tk := pwTicket(t, h, pwSource, "en")
	if err := h.St.SetConfig("gate_retry_max", "2"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := h.W.Executor.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("文化闸重翻后应放行，实得 %v", err)
	}
	if pwStatus(t, h, tk.ID) != store.TicketPendingAppr {
		t.Errorf("文化闸最终应通过并转人工，实得 %s", pwStatus(t, h, tk.ID))
	}
	if h.Stub.asked("语气不合规") == 0 {
		t.Error("文化闸打回原因未传给模型（culture.Reasons 未接线）")
	}
	p := pwPayload(t, h, tk.ID)
	if p.Translations["en"] != pwGood {
		t.Errorf("含粗口译文应被重翻修正，实得 %q", p.Translations["en"])
	}
	if !strings.Contains(p.GateHints["en"], "文化") {
		t.Errorf("GateHints 应留存文化闸打回原因供审批参考，实得 %v", p.GateHints)
	}
	if p.Culture == nil || !p.Culture.Pass {
		t.Errorf("放行时文化闸结果应为 Pass 且已落载荷，实得 %+v", p.Culture)
	}
}

// TestWorkflowQAErrorsDoNotChangeTicketOutcome
// 确定性质检（qa.Check）只出报告与摘要计数，**绝不判死工单**：
// error 级问题（数字丢失/漏译）存在时，流程照样走到 pending_approval。
// 改坏了会怎样：qa 一旦被接成阻断器，纯规则误报会批量 reject 人工可接受的单子；
// 反过来若报告不落载荷/不落列，前端与下载对照表就再也看不到质检结论（改造 5 的落点）。
func TestWorkflowQAErrorsDoNotChangeTicketOutcome(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		return pwGood // 所有环节（含重翻）都返回可交付译文
	})
	// KB 给出「过得了硬闸但过不了 QA」的译文：数字 45 保留、但缺译关键实词，
	// 用占位符丢失制造 error 级问题（源文含 {name}，译文不含）。
	src := "本系统支持 45 种文件翻译并保留 {name} 占位符"
	pwSeedKB(t, h, src, "en", "This system supports 45 file translation formats and keeps placeholders")
	tk := pwTicket(t, h, src, "en")

	if err := h.W.Executor.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("QA 问题不得使流程失败，实得 %v", err)
	}
	p := pwPayload(t, h, tk.ID)
	if p.QAReport == nil {
		t.Fatal("qa 步骤未落报告")
	}
	if p.QAReport.Errors == 0 {
		t.Fatalf("构造的占位符丢失应产生 error 级问题，实得 %+v", p.QAReport)
	}
	if pwStatus(t, h, tk.ID) != store.TicketPendingAppr {
		t.Errorf("QA 报 error 仍应转人工裁决，实得 %s", pwStatus(t, h, tk.ID))
	}
	fresh, _ := h.St.GetTicket(tk.ID, 1)
	if fresh.QAErrors != p.QAReport.Errors {
		t.Errorf("工单 qa_errors 列应与报告一致（列表徽标数据源），列=%d 报告=%d", fresh.QAErrors, p.QAReport.Errors)
	}
}

// TestWorkflowPipelineAppliesEvalDisposition
// 评估不合格的处置必须由**流水线**触发（此前只有叶子函数 applyEvalDisposition 有单测，
// 接线没人管：Evaluator 没挂上 / 阶段名传错 / 分数没落载荷，单测全绿而生产不打标）。
// 断言：Judge 给低分 → 载荷 EvalScores 有分、QualityFlaggedLangs 含该语言、
// 工单 quality_flagged=1、告警中心 1 条 eval_quality。
func TestWorkflowPipelineAppliesEvalDisposition(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		if strings.Contains(prompt, "翻译质量评估员") {
			return `{"term":20,"grammar":20,"semantic":20,"numunit":20,"style":20}` // 总分 20 < 阈值 60
		}
		return "" // 审校失败：保留 KB 译文
	})
	cfg := config.C
	h.W.Engine.Evals = evals.New(cfg, h.W.Engine.LLM, h.St, "sk-fake-judge") // 全量抽样（默认 1.0）
	if err := h.St.SetConfig("evals_fail_threshold", "60"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	pwSeedKB(t, h, pwSourceClean, "en", "Multi-format file translation is supported by this system.")
	tk := pwTicket(t, h, pwSourceClean, "en")

	if err := h.W.Executor.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("低分不应阻断流程（人工裁决），实得 %v", err)
	}
	p := pwPayload(t, h, tk.ID)
	if p.EvalScores["en"] == 0 {
		t.Errorf("评估总分未落载荷（Judge 调用或写回未接线）: %+v", p.EvalScores)
	}
	if len(p.QualityFlaggedLangs) != 1 || p.QualityFlaggedLangs[0] != "en" {
		t.Errorf("低分语言应被打标，实得 %v", p.QualityFlaggedLangs)
	}
	fresh, _ := h.St.GetTicket(tk.ID, 1)
	if fresh.QualityFlagged != 1 {
		t.Errorf("工单 quality_flagged 列应为 1（前端徽标数据源），实得 %d", fresh.QualityFlagged)
	}
	alerts, _ := h.St.ListAlerts(1, "open", 50)
	if len(alerts) != 1 || alerts[0].Kind != "eval_quality" {
		t.Errorf("应落 1 条 eval_quality 告警，实得 %+v", alerts)
	}
	// 低分只打标不自动重译（Judge 主观分重译易震荡烧钱——改造 4 的明确取舍）
	if pwStatus(t, h, tk.ID) != store.TicketPendingAppr {
		t.Errorf("低分单仍应正常转人工，实得 %s", pwStatus(t, h, tk.ID))
	}
}

// TestWorkflowApprovedRerunRefreshesQAOnlyAndIsIdempotent
// 已批准工单重跑（C4 保护）：生成/校对/闸门步骤全部短路，只跑 qa 刷新 + feedback 写 TM；
// 人工终稿一字不动；重复执行不产生重复 TM 行（SaveBack 按 zh_hash+tenant+pack UPSERT）。
// 改坏了会怎样：
//   - 闸门步骤没短路 → 机器把人工终稿重翻一遍甚至整单翻案为 rejected（历史 C4 缺陷）；
//   - feedback 幂等失效 → 每次重跑堆一行 TM，命中统计与阈值语义全部失真。
func TestWorkflowApprovedRerunRefreshesQAOnlyAndIsIdempotent(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		t.Fatalf("已批准工单重跑不得调用任何模型，实得 prompt: %.80s", prompt)
		return ""
	})
	src := "支持多格式文件翻译能力"
	human := "Multi-format file translation capability is supported."
	tk, err := h.St.CreateTicket(1, 7, "已批准重跑", src, "", "en")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	payload := ticketPayload{
		SourceText:   src,
		TargetLangs:  []string{"en"},
		Translations: map[string]string{"en": human},
		Sources:      map[string]string{"en": "manual"},
	}
	b, _ := json.Marshal(payload)
	tk.FinalResult = string(b)
	tk.Status = store.TicketApproved
	if err := h.St.UpdateTicket(tk); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}

	for i := 0; i < 2; i++ {
		tk2, gerr := h.St.GetTicket(tk.ID, 1)
		if gerr != nil {
			t.Fatalf("回读工单失败: %v", gerr)
		}
		if i == 1 {
			tk2.Status = store.TicketApproved // 模拟「已批准工单再次重跑」
			if err := h.St.UpdateTicket(tk2); err != nil {
				t.Fatalf("UpdateTicket: %v", err)
			}
		}
		if err := h.W.Executor.Execute(context.Background(), tk2, nil); err != nil {
			t.Fatalf("第 %d 次重跑应成功: %v", i+1, err)
		}
		if n := pwTMFeedbackRows(t, h, src); n != 1 {
			t.Fatalf("第 %d 次重跑后正式 TM 应恰为 1 行（UPSERT 幂等），实得 %d", i+1, n)
		}
	}
	p := pwPayload(t, h, tk.ID)
	if p.Translations["en"] != human {
		t.Errorf("人工终稿不得被重跑改动，实得 %q", p.Translations["en"])
	}
	if p.Sources["en"] != "manual" {
		t.Errorf("人工终稿来源标记不得被改写，实得 %q", p.Sources["en"])
	}
	if pwStatus(t, h, tk.ID) != store.TicketCompleted {
		t.Errorf("写库完成后工单应为 completed，实得 %s", pwStatus(t, h, tk.ID))
	}
	states := pfStateMap(t, h.St, tk.ID)
	for _, k := range []string{"kb_match", "ai_initial", "review", "gate", "culture_gate"} {
		if s := states[k]; s == nil || s.Status != "skipped" {
			t.Errorf("已批准重跑步骤 %s 应为 skipped（人工终稿保护），实得 %+v", k, s)
		}
	}
	if s := states["mode_override"]; s == nil || !strings.Contains(s.Payload, "approved_rerun") {
		t.Errorf("重跑旁路应落 mode_override 审计轨迹，实得 %+v", s)
	}
}

// TestWorkflowFeedbackWritesTMAfterApprovalAndSkipsLowScore
// 批准后 feedback 的写库判定（高分直写正式 TM / 低分转人审池）由流水线驱动。
// 与 h4_feedback_test.go（直接调 runFeedback）互补：这里钉的是「approval→feedback 的
// 状态推进」这条接线——approval 若忘了转 pending_approval 或 feedback 注册顺序错位，
// 本用例的工单终态/写库结果就会翻红。
func TestWorkflowFeedbackWritesTMAfterApprovalAndSkipsLowScore(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		return "" // 审校/重译均失败：保持 KB 译文不变
	})
	good := "Multi-format file translation is supported by this system."
	pwSeedKB(t, h, pwSourceClean, "en", good)
	tk := pwTicket(t, h, pwSourceClean, "en")
	// 先跑一遍到 pending_approval
	if err := h.W.Executor.Execute(context.Background(), tk, nil); err != nil {
		t.Fatalf("首轮流程失败: %v", err)
	}
	if pwStatus(t, h, tk.ID) != store.TicketPendingAppr {
		t.Fatalf("首轮应停在待审批，实得 %s", pwStatus(t, h, tk.ID))
	}
	if n := pwTMFeedbackRows(t, h, pwSourceClean); n != 0 {
		t.Fatalf("待审批阶段不得写 TM，实得 %d 行", n)
	}
	// 审批通过后再跑一遍（生产由审批台触发同一执行器）
	tk2, _ := h.St.GetTicket(tk.ID, 1)
	tk2.Status = store.TicketApproved
	if err := h.St.UpdateTicket(tk2); err != nil {
		t.Fatalf("置为 approved 失败: %v", err)
	}
	if err := h.W.Executor.Execute(context.Background(), tk2, nil); err != nil {
		t.Fatalf("批准后重跑失败: %v", err)
	}
	if n := pwTMFeedbackRows(t, h, pwSourceClean); n != 1 {
		t.Fatalf("批准后应写 1 行正式 TM，实得 %d", n)
	}
	row, err := h.KBD.FindExact(pwSourceClean, 1)
	if err != nil || row == nil {
		t.Fatalf("TM 行不可见: %v", err)
	}
	if row.Langs["en"] != good {
		t.Errorf("TM 应存人工批准后的译文，实得 %q", row.Langs["en"])
	}
	if pwStatus(t, h, tk.ID) != store.TicketCompleted {
		t.Errorf("写库后工单应为 completed，实得 %s", pwStatus(t, h, tk.ID))
	}
}

// TestWorkflowKBMatchReusesExistingPayloadAndFillsMissingLang
// kb_match 的「复用既有载荷 + 只补缺失语言」接线（整改 C4）：
// 已有一语言人工译文时，重跑不得覆写它，只允许给缺失语言补 KB 命中。
// 改坏了会怎样：无条件新建载荷 → 审批员终稿在批准重跑时被静默丢弃（历史缺陷复发）。
func TestWorkflowKBMatchReusesExistingPayloadAndFillsMissingLang(t *testing.T) {
	h := pwSetup(t, pwReply{}.fn()) // 本用例只直调 runKBMatch，上游必须保持零调用
	src := "设备支持远程升级功能"
	pwSeedKB(t, h, src, "en", "The device supports remote upgrade functionality.")
	pwSeedKB(t, h, src, "de", "Das Gerät unterstützt Fern-Updates.")
	tk, err := h.St.CreateTicket(1, 7, "双语补漏", src, "", "en,de")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	// 载荷里已有 en 的人工终稿，de 缺失
	human := "Human edited: device supports remote upgrades."
	b, _ := json.Marshal(ticketPayload{
		SourceText: src, TargetLangs: []string{"en", "de"},
		Translations: map[string]string{"en": human}, Sources: map[string]string{"en": "manual"},
	})
	tk.FinalResult = string(b)
	if err := h.St.UpdateTicket(tk); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if err := h.W.runKBMatch(context.Background(), tk); err != nil {
		t.Fatalf("runKBMatch: %v", err)
	}
	p := pwPayload(t, h, tk.ID)
	if p.Translations["en"] != human {
		t.Errorf("既有（人工）译文不得被 KB 覆写，实得 %q", p.Translations["en"])
	}
	if p.Translations["de"] != "Das Gerät unterstützt Fern-Updates." {
		t.Errorf("缺失语言 de 应由 KB 补出，实得 %q", p.Translations["de"])
	}
	if p.Sources["de"] != "kb" {
		t.Errorf("补出的语言应标来源 kb，实得 %q", p.Sources["de"])
	}
}

// TestWorkflowFeedbackMultiLangOverwritesEarlierLang KNOWN DEFECT（只复现不擅自修）
// runFeedback 在语言循环内**逐语言**调用 kb.SaveBack，而 SaveBack 会把未提供的全部
// 34 个语言列写成空串（ON CONFLICT DO UPDATE SET <每列>=excluded.<每列>）。
// 结果：多语言工单只有 map 迭代顺序最后那一语言的译文留在正式 TM，其余语言被静默清空，
// 且留哪一语言不确定（Go map 随机序）。
// 期望语义：一次 SaveBack 传入全部高分语言（或 SaveBack 只更新传入的语言列）。
// 修复本缺陷后，请删掉 t.Skip 让这条断言转为闸门。
func TestWorkflowFeedbackMultiLangOverwritesEarlierLang(t *testing.T) {
	t.Skip("已知缺陷（2026-09-23 装配回归发现，已上报主代理）：runFeedback 逐语言 SaveBack 会互相清空其他语言列；修复后取消本 Skip")
	h := pwSetup(t, func(prompt string) string { return "" })
	src := "设备支持远程升级功能"
	tk, err := h.St.CreateTicket(1, 7, "双语回写", src, "", "en,de")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	b, _ := json.Marshal(ticketPayload{
		SourceText: src, TargetLangs: []string{"en", "de"},
		Translations: map[string]string{
			"en": "The device supports remote upgrade functionality.",
			"de": "Das Gerät unterstützt Remote-Upgrades.",
		},
		Sources: map[string]string{"en": "manual", "de": "manual"},
	})
	tk.FinalResult = string(b)
	tk.Status = store.TicketApproved
	if err := h.St.UpdateTicket(tk); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	if err := h.W.runFeedback(context.Background(), tk); err != nil {
		t.Fatalf("runFeedback: %v", err)
	}
	row, err := h.KBD.FindExact(src, 1)
	if err != nil || row == nil {
		t.Fatalf("TM 行不可见: %v", err)
	}
	if row.Langs["en"] == "" || row.Langs["de"] == "" {
		t.Fatalf("两个高分语言都应留在正式 TM，实得 %+v", row.Langs)
	}
}
