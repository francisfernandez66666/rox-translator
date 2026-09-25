// ============ f42_flow_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 C（F-42 假 completed 专项）编排层回归断言，钉两层：
//
//	① F-42-a（executor 层）：翻译途中余额烧穿的中止原因必须原样穿透 Execute 返回值。
//	   旧写法 `return ctx.Err()` 把 WithCancelCause 注入的 store.ErrInsufficientBalance
//	   压扁成 context.Canceled ⇒ 上层只能写 'context canceled'（无码可读、还会被
//	   runAIInitial 当成人工驳回意见）。同批把步骤失败的 fmt.Errorf %s 改为 %w，
//	   使哨兵在「步骤内直接返回错误」这条路径上同样可被 errors.Is 认出。
//	② F-42-b（workflow 层）：驳回重翻分支的进入判据收紧为
//	   「reject_reason 非空 && reject_source=='human' && 载荷确有非空译文」。
//	   三条件缺一即落回正常翻译流程——载荷全空时旧分支逐语言 continue、
//	   零次 LLM 调用后 return nil，流水线后半段把没译动的工单刷成 completed。
//
// harness 复用同包既有两套：pfStore/pfSpy/pfExec（假步骤钉执行器）、
// pwSetup/pwReply（httptest 假上游钉真实步骤，不打真实 LLM）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/orchestrator/ -run TestUATBatchC
// =============================================
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"translator/internal/store"
)

// stepKeysAfter 返回默认流程里 key 之后的步骤标识（判「中止后不得再执行任何步骤」）。
func stepKeysAfter(key string) []string {
	out := []string{}
	hit := false
	for _, d := range store.DefaultFlowSteps {
		if hit {
			out = append(out, d.Key)
		}
		if d.Key == key {
			hit = true
		}
	}
	return out
}

// TestUATBatchC_ExecutePropagatesBalanceCancelCause ①：WithCancelCause 注入欠费哨兵后，
// Execute 必须把 cause 原样返回（修复前恒为 context.Canceled，真因丢失）。
func TestUATBatchC_ExecutePropagatesBalanceCancelCause(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil) // 兜底释放（cause 已在钩子里注入）
	spy.hook["review"] = func() { cancel(store.ErrInsufficientBalance) }

	err := ex.Execute(ctx, tk, nil)
	if err == nil {
		t.Fatal("中止后 Execute 必须返回错误，实得 nil")
	}
	if !errors.Is(err, store.ErrInsufficientBalance) {
		t.Errorf("返回值必须能 errors.Is 认出欠费哨兵（cause 被压扁即 F-42 复发），实得 %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("cause 已注入却仍回裸 Canceled：%v", err)
	}
	for _, k := range stepKeysAfter("review") {
		if n := spy.times(k); n != 0 {
			t.Errorf("欠费中止后步骤 %s 不得再执行，实得 %d 次", k, n)
		}
	}
}

// TestUATBatchC_StepFailureKeepsErrorChainAndSystemSource ①补：步骤内直接返回欠费哨兵时，
// 错误链要保住（%w）且工单驳回来源落 'system'（不得留空，否则重翻判据与认领收窄都失据）。
func TestUATBatchC_StepFailureKeepsErrorChainAndSystemSource(t *testing.T) {
	st := pfStore(t)
	spy := newSpy()
	spy.failAt["ai_initial"] = store.ErrInsufficientBalance
	ex := pfExec(st, spy)
	tk := pfTicket(t, st)

	err := ex.Execute(context.Background(), tk, nil)
	if err == nil {
		t.Fatal("步骤失败必须短路返回错误")
	}
	if !errors.Is(err, store.ErrInsufficientBalance) {
		t.Errorf("步骤失败包装不得丢错误链（旧 %%s 写法会丢），实得 %v", err)
	}
	fresh, gerr := st.GetTicketGlobal(tk.ID)
	if gerr != nil || fresh == nil {
		t.Fatalf("回读工单失败: %v", gerr)
	}
	if fresh.Status != store.TicketRejected {
		t.Errorf("步骤失败应置 rejected，实得 %s", fresh.Status)
	}
	if fresh.RejectSource != store.RejectSourceSystem {
		t.Errorf("系统失败来源应为 system，实得 %q", fresh.RejectSource)
	}
	if !strings.Contains(fresh.RejectReason, "ai_initial") && !strings.Contains(fresh.RejectReason, "失败") {
		t.Errorf("驳回原因应含步骤失败说明，实得 %q", fresh.RejectReason)
	}
}

// f42PayloadJSON 组装一份可控工单载荷（translations 由入参决定，空串=有槽位但无产物）。
func f42PayloadJSON(t *testing.T, source string, trans map[string]string) string {
	t.Helper()
	srcs := map[string]string{}
	data, err := json.Marshal(ticketPayload{
		SourceText: source, TargetLangs: []string{"en"}, Translations: trans, Sources: srcs, Mode: "无知识库直翻",
	})
	if err != nil {
		t.Fatalf("载荷序列化失败: %v", err)
	}
	return string(data)
}

// TestUATBatchC_ReworkBranchRequiresHumanSourceAndPayload ②：真实步骤层的三条件判据。
// 三个子态用「最终译文取到的是哪一路回复」来证伪/证实分支走向（假上游按环节返回不同文本）：
//
//	A 系统来源 + 载荷空   → 落回正常翻译（得 fallback 译文）；旧实现走重翻分支零调用 return nil，
//	                       译文保持为空 ⇒ 本断言在修复前必红；
//	B 人工来源 + 载荷有译文 → 走驳回重翻（得 retrans 译文）；
//	C 人工来源 + 载荷全空   → 双保险生效，落回正常翻译（绝不空转成功）。
func TestUATBatchC_ReworkBranchRequiresHumanSourceAndPayload(t *testing.T) {
	const norm = "This system supports 45 multi-format file translation formats"
	const rework = "REWORK 45 multi-format file translation formats"
	cases := []struct {
		name       string
		reason     string // reject_reason（非空才会考虑重翻分支）
		rejectSrc  string // reject_source：human / system / 空串（历史行）
		payloadHas bool   // 载荷里是否已有非空译文
		want       string // 期望最终译文：norm=走了正常翻译，rework=走了驳回重翻
	}{
		// A 系统来源 + 空载荷：修复前会被当成人工驳回意见 ⇒ 零 LLM 调用 return nil ⇒ 译文为空（假 completed）
		{"A_系统来源空载荷走正常翻译", "context canceled", store.RejectSourceSystem, false, norm},
		// B 人工来源 + 有载荷：唯一合法的重翻入口
		{"B_人工来源有载荷走重翻", "术语不一致，请统一为「数据资产」", store.RejectSourceHuman, true, rework},
		// C 人工来源 + 空载荷：双保险生效，落回正常翻译（绝不空转成功）
		{"C_人工来源空载荷仍走正常翻译", "术语不一致", store.RejectSourceHuman, false, norm},
		// D 历史空串行 + 空载荷：按「非人工」处理，同样走正常翻译
		{"D_历史空串来源走正常翻译", "步骤 初翻 失败: 余额不足", "", false, norm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := pwSetup(t, pwReply{fallback: norm, retrans: rework}.fn())
			tk := pwTicket(t, h, pwSource, "en")
			payload := map[string]string{"en": ""}
			if c.payloadHas {
				payload = map[string]string{"en": norm}
			}
			tk.FinalResult = f42PayloadJSON(t, pwSource, payload)
			tk.RejectReason = c.reason
			tk.RejectSource = c.rejectSrc
			if err := h.W.runAIInitial(context.Background(), tk); err != nil {
				t.Fatalf("runAIInitial 出错: %v", err)
			}
			p := pwPayload(t, h, tk.ID)
			got := strings.TrimSpace(p.Translations["en"])
			if got == "" {
				t.Fatal("runAIInitial 空转成功（假 completed 复发）：译文仍为空")
			}
			if got != c.want {
				t.Errorf("译文走的分支不符：期望 %q 实得 %q", c.want, got)
			}
		})
	}
}
