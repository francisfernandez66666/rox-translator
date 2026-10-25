// ============ 本文件职责中文说明 ============
// 2026-10-24 DEF-02（文本工单结构失败自动整单重发）的判据锁：
// `Workflow.StructureRetryNeeded` 是「要不要把这一单重新入队」的唯一决策口，
// 三条射程一次测全——
//
//	① 上限 3 次（默认档）：第 4 次必须拒，否则坏单会在队列里无限重发、每一轮都真打上游扣钱；
//	② 档位 `ticket_repair_max` 现读（env > system_config > 代码默认那三级里的第二级）：
//	   运营配 1 就只发一次、配 0 就一次都不发（0 是合法档位，不是"没配"）；
//	③ 只认 qa 的 `structure` 规则：数字/占位符那类 error 由既有硬闸与重译链处理，
//	   拿它们触发整单重发＝同一单两条链各重发一轮（成本翻倍且归因混乱）。
//
// ★ 2026-10-25 对账（§10.1.2）添第 ④ 条射程：计数落在 **`tickets.repair_attempts` 列**，
//
//	不再挤在 `final_result` 那段 JSON 里。这条必须由「整段重写载荷也不许归零」的行为锁来钉：
//	savePayload 是逐步骤覆盖 FinalResult 的（人工重跑/驳回重翻/补译都会走到），
//	旧形态下一次覆盖就把上限抹回 0 ⇒ 同一单能再来三轮真扣费，而界面与日志一切正常。
//
// 反证取向：③ 那条用「结构完好但数字缺失」的样本喂进去必须回 false——
// 若有人把 `iss.Rule == "structure"` 放宽成"有任何 error 即重发"，这一条当场红。
// =============================================
package orchestrator

import (
	"testing"

	"translator/internal/store"
)

// srPayload 构造一张「译文把多段源文合成了一段」的工单载荷（qa 的 structure 判据必命中）。
// 参数：w=工作流，t=工单，src=多段源文，tgt=语言→译文。
func srPayload(t *testing.T, w *Workflow, tk *store.Ticket, src string, tgt map[string]string) {
	t.Helper()
	p := &ticketPayload{SourceText: src, TargetLangs: []string{"en"}, Translations: tgt}
	w.savePayload(tk, p)
}

// srAttempts 现读那一单的整单重发计数（列，不是载荷）。
func srAttempts(t *testing.T, h *pwHarness, tk *store.Ticket) int {
	t.Helper()
	n, err := h.St.TicketRepairAttempts(tk.ID)
	if err != nil {
		t.Fatalf("读 tickets.repair_attempts 失败: %v", err)
	}
	return n
}

// TestStructureRepairMaxDefaultThree 默认档：同一单最多自动重发 3 次，第 4 次必须拒。
func TestStructureRepairMaxDefaultThree(t *testing.T) {
	h := pwSetup(t, nil)
	tk := pwTicket(t, h, "第一段内容。\n第二段内容。\n第三段内容。", "en")
	srPayload(t, h.W, tk, "第一段内容。\n第二段内容。\n第三段内容。", map[string]string{"en": "一段被合成在一起的译文"})
	for i := 1; i <= 3; i++ {
		if !h.W.StructureRetryNeeded(tk) {
			t.Fatalf("第 %d 次结构失败应重发（默认上限 3），实得 false", i)
		}
		if got := srAttempts(t, h, tk); got != i {
			t.Fatalf("第 %d 次重发后计数应=%d，实得 %d（计数不落列＝运维看不见、载荷一重写就归零，上限形同虚设）", i, i, got)
		}
	}
	if h.W.StructureRetryNeeded(tk) {
		t.Fatal("第 4 次仍判重发 ⇒ 上限没生效，坏单会在队列里无限重发并每轮真扣费")
	}
	if got := srAttempts(t, h, tk); got != 3 {
		t.Fatalf("拒发的那一次不许吃掉预算一格，实得 %d", got)
	}
}

// TestStructureRepairCountSurvivesPayloadRewrite §10.1.2 第 ④ 条：载荷整段重写不许把计数抹回 0。
// 形态取自真实路径——每个流程步骤收尾都调 savePayload 覆盖 FinalResult（驳回重翻、人工重跑、
// 补译都走这一条），所以"计数住在载荷里"等于"每一步都在给它归零"。
func TestStructureRepairCountSurvivesPayloadRewrite(t *testing.T) {
	h := pwSetup(t, nil)
	src := "第一段内容。\n第二段内容。\n第三段内容。"
	tk := pwTicket(t, h, src, "en")
	srPayload(t, h.W, tk, src, map[string]string{"en": "一段被合成在一起的译文"})
	if !h.W.StructureRetryNeeded(tk) {
		t.Fatal("首次结构失败应重发")
	}
	// 模拟「下一步骤把载荷整段重写」（旧形态正是在这里把 structure_repair_attempts 抹掉）
	srPayload(t, h.W, tk, src, map[string]string{"en": "一段被合成在一起的译文"})
	if got := srAttempts(t, h, tk); got != 1 {
		t.Fatalf("载荷重写后列上的计数应仍是 1，实得 %d ⇒ 上限每被重写一次就复位一次", got)
	}
	// 预算仍按同一把尺子剩 2 格（第 2、3 次放行，第 4 次拒）
	for i := 2; i <= 3; i++ {
		if !h.W.StructureRetryNeeded(tk) {
			t.Fatalf("第 %d 次结构失败应重发（重写载荷不该多给预算），实得 false", i)
		}
	}
	if h.W.StructureRetryNeeded(tk) {
		t.Fatal("重写载荷后第 4 次判重发 ⇒ 计数被抹回 0，坏单会无限重发")
	}
}

// TestStructureRepairMaxConfigTier 档位现读：配 1 只发一次、配 0 一次都不发、脏值回落默认 3。
func TestStructureRepairMaxConfigTier(t *testing.T) {
	cases := []struct {
		name  string
		val   string
		allow int // 期望允许的重发次数
	}{
		{"配1只发一次", "1", 1},
		{"配0一次不发", "0", 0},
		{"脏值回落默认3", "abc", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := pwSetup(t, nil)
			if err := h.St.SetConfig("ticket_repair_max", c.val); err != nil {
				t.Fatalf("SetConfig: %v", err)
			}
			tk := pwTicket(t, h, "第一段内容。\n第二段内容。", "en")
			srPayload(t, h.W, tk, "第一段内容。\n第二段内容。", map[string]string{"en": "合成一段的译文"})
			got := 0
			for i := 0; i < 5; i++ {
				if h.W.StructureRetryNeeded(tk) {
					got++
				}
			}
			if got != c.allow {
				t.Fatalf("ticket_repair_max=%q 应重发 %d 次，实得 %d 次", c.val, c.allow, got)
			}
		})
	}
}

// TestStructureRepairOnlyForStructureRule 反证射程：非 structure 的 qa error 不许触发整单重发。
// 样本刻意「行数与源文一致（结构完好）但数字丢了」⇒ qa 出 number error、不出 structure error。
func TestStructureRepairOnlyForStructureRule(t *testing.T) {
	h := pwSetup(t, nil)
	src := "第 12 项交付物。\n第 34 项交付物。"
	tk := pwTicket(t, h, src, "en")
	srPayload(t, h.W, tk, src, map[string]string{"en": "Item twelve.\nItem thirty-four."})
	if h.W.StructureRetryNeeded(tk) {
		t.Fatal("结构完好而数字缺失的单被判整单重发 ⇒ 数字类问题会走两条链各重发一轮")
	}
	if p := h.W.loadPayload(tk); p == nil {
		t.Fatal("载荷读取失败（样本没落库）")
	}
	if got := srAttempts(t, h, tk); got != 0 {
		t.Fatalf("未触发重发时计数必须保持 0（误加一次就把默认 3 次预算吃掉一格），实得 %d", got)
	}
}

// TestStructureRepairNeedsPayload 空载荷／无译文都不许重发（重发一条空单只会把队列填满空气）。
func TestStructureRepairNeedsPayload(t *testing.T) {
	h := pwSetup(t, nil)
	tk := pwTicket(t, h, "第一段内容。\n第二段内容。", "en")
	if h.W.StructureRetryNeeded(tk) {
		t.Fatal("载荷还没落库就判重发")
	}
	srPayload(t, h.W, tk, "第一段内容。\n第二段内容。", map[string]string{"en": ""})
	if h.W.StructureRetryNeeded(tk) {
		t.Fatal("译文为空（还没翻出来）时判重发 ⇒ 与「初翻环节尚未跑」这一形态无法区分")
	}
}
