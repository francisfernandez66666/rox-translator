// ============ pipeline_number_retry_test.go · 职责说明 ============
// ★ 2026-10-10 批次 ⑫ 配套断言：数字类失败短重试 + firstFail 悬挂冒号。
//
//	① 「数字保持」失败（mock 重译永远产出丢数字译文）重译 2 次即报
//	   「已自动重译 2 次仍不通过」，计数拨了 2 次后停（不再烧满 gate_retry_max=8）；
//	② 非数字类失败（长度压缩）仍按 gate_retry_max 走（用配置 3 验证档位选择：
//	   报文必须是「已自动重译 3 次」而不是 2）；
//	③ firstFail 对空 Detail 只出名称（不出「数字保持: 」悬挂冒号）、
//	   有 Detail 出「名称: 详情」。
//	既有 pipeline_workflow_test.go 的「已自动重译 1 次」断言（gate_retry_max=1 的
//	非数字/混合失败）不受影响：数字类上限取 min(2, gate_retry_max)。
//
// 夹具复用 pwSetup/pwTicket/pwSeedKB/pwStatus（pipeline_workflow_test.go），
// 方言已在夹具里自钉 sqlite。
// ========================================
package orchestrator

import (
	"context"
	"strings"
	"testing"

	"translator/internal/gate"
	"translator/internal/store"
)

// TestGateNumberFailRetriesOnlyTwice 数字类失败：重译 2 次即判死，不烧满 8 次上限。
// 改坏了会怎样：档位选择逻辑摘掉（统一走 gate_retry_max）→ 本用例在 attempt>=2 时
// 还会继续拨重译，asked 计数超过 2、报文变成「已自动重译 8 次」双双翻红。
func TestGateNumberFailRetriesOnlyTwice(t *testing.T) {
	// 审校失败（保留 KB 脏译文）；重译永远产出「丢了 45」的英文译文——
	// 非空、无中文残留，唯一卡点就是「数字保持」，与现网 T20261010132330UEM 同形态。
	h := pwSetup(t, pwReply{retrans: pwNoDigits}.fn())
	pwSeedKB(t, h, pwSource, "en", pwBad)
	tk := pwTicket(t, h, pwSource, "en")
	// 不配置 gate_retry_max：走默认 8，验证数字类上限独立压到 2

	err := h.W.Executor.Execute(context.Background(), tk, nil)
	if err == nil {
		t.Fatal("重译永远丢数字时必须判死")
	}
	if !strings.Contains(err.Error(), "已自动重译 2 次") {
		t.Fatalf("数字类失败应报「已自动重译 2 次仍不通过」，实得 %v", err)
	}
	if n := h.Stub.asked("请严格按照意见修正重译"); n != 2 {
		t.Fatalf("数字类失败应恰好拨 2 次重译就停，实得 %d 次", n)
	}
	if pwStatus(t, h, tk.ID) != store.TicketRejected {
		t.Fatalf("判死后工单必须 rejected，实得 %s", pwStatus(t, h, tk.ID))
	}
}

// TestGateNonNumberFailKeepsConfiguredMax 非数字类失败（长度压缩）仍按 gate_retry_max 走：
// 配 3 即报「已自动重译 3 次」——证明数字类短上限没有误伤其他检查项的档位选择。
// 用配置 3 而不是默认 8，避免真跑 8 轮（档位选择逻辑已由读数分辨，不必烧满）。
func TestGateNonNumberFailKeepsConfiguredMax(t *testing.T) {
	// 源文带数字 45（译文必须保留）且足够长；译文 "No. 45" 保留数字但长度压缩过半
	// ⇒ 唯一失败项是「长度合理」（非数字类）。
	src := "本系统支持 45 种多格式文件翻译能力并且包含详尽的说明文档与使用手册"
	short := "No. 45"
	h := pwSetup(t, pwReply{retrans: short}.fn())
	pwSeedKB(t, h, src, "en", short)
	tk := pwTicket(t, h, src, "en")
	if err := h.St.SetConfig("gate_retry_max", "3"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	err := h.W.Executor.Execute(context.Background(), tk, nil)
	if err == nil {
		t.Fatal("长度压缩重译不变时必须判死")
	}
	if !strings.Contains(err.Error(), "已自动重译 3 次") {
		t.Fatalf("非数字类失败应按 gate_retry_max=3 判死（而非数字类的 2），实得 %v", err)
	}
	if n := h.Stub.asked("请严格按照意见修正重译"); n != 3 {
		t.Fatalf("非数字类失败应恰好拨 3 次重译，实得 %d 次", n)
	}
}

// TestFirstFailNoDanglingColon firstFail 的 Detail 空串不出悬挂冒号、有 Detail 出「名称: 详情」。
// 反证形态：旧写法无条件拼 ": "，Detail 为空时产出「数字保持: 」这类尾巴。
func TestFirstFailNoDanglingColon(t *testing.T) {
	if got := firstFail([]gate.Check{{Name: gate.CheckNumberKeep, Pass: false, Detail: ""}}); got != "数字保持" {
		t.Fatalf("空 Detail 应只出名称（不出悬挂冒号），实得 %q", got)
	}
	if got := firstFail([]gate.Check{{Name: gate.CheckNumberKeep, Pass: false, Detail: "缺失: 2026"}}); got != "数字保持: 缺失: 2026" {
		t.Fatalf("有 Detail 应出「名称: 详情」，实得 %q", got)
	}
	if got := firstFail([]gate.Check{{Name: "非空", Pass: true}}); got != "" {
		t.Fatalf("无失败项应返回空串，实得 %q", got)
	}
}
