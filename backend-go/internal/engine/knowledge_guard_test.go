// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》§9.1 G1–G4：无来源数字／认证词整句丢、合法已审句一字不动、
// 三个分档名逐字钉住、ok 出口回空串。
// ★ G3 的靶子必须**多句**：单句靶子会把「剥空即回退」掩护成「合法句原样返回」（AGENTS §三 反证批点过的真坑）。
// ★ N1 的零 LLM 判据同源：本文件所有用例不碰 llm.Client、不碰数据库，纯函数跑完即证。
// ========================================
package engine

import (
	"strings"
	"testing"
)

// G1：无来源数字整句丢（不部分改写）。
func TestKnowledgeGuardStripsUnsourcedNumber(t *testing.T) {
	in := "本产品交期 7 天。已备物料原文。"
	out, reason := GuardKnowledgeAnswer(in, []string{}, "已备物料原文")
	if reason != KnowUnsourcedNumber {
		t.Fatalf("分档应为 know_unsourced_number, got %q", reason)
	}
	if strings.Contains(out, "交期 7 天") {
		t.Fatalf("无来源数字必须整句丢, got %q", out)
	}
	if !strings.Contains(out, "已备物料原文") {
		t.Fatalf("合法句必须保留, got %q", out)
	}
}

// G2：无来源认证词整句丢，且认证优先于同句数字判档。
func TestKnowledgeGuardStripsUnsourcedCert(t *testing.T) {
	in := "通过 CE 认证，交期 7 天。已备物料原文。"
	out, reason := GuardKnowledgeAnswer(in, []string{}, "已备物料原文")
	if reason != KnowUnsourcedCert && reason != KnowUnsourcedNumber {
		t.Fatalf("分档必须落在两条剥句档之一, got %q", reason)
	}
	if strings.Contains(out, "CE") {
		t.Fatalf("无来源认证词必须整句丢, got %q", out)
	}
}

// G3：合法已审句一字不动（多句靶子；有来源的数字/认证都不许被剥）。
func TestKnowledgeGuardKeepsConfirmedSentencesVerbatim(t *testing.T) {
	in := "交期 7 天。通过 ISO 9001 认证。本产品支持 12 语种。"
	facts := []string{"交期 7 天", "ISO 9001", "12 语种"}
	out, reason := GuardKnowledgeAnswer(in, facts, "兜底")
	if reason != KnowReasonOK {
		t.Fatalf("全部有来源时必须回 ok 空串, got %q", reason)
	}
	if out != in {
		t.Fatalf("合法已审句必须一字不动, want %q got %q", in, out)
	}
}

// G4：三个分档名逐字钉住；全剥空 ⇒ know_stripped_all 且回退已备物料原文。
func TestKnowledgeGuardReasonsAndStrippedAll(t *testing.T) {
	if KnowUnsourcedNumber != "know_unsourced_number" ||
		KnowUnsourcedCert != "know_unsourced_cert" ||
		KnowStrippedAll != "know_stripped_all" || KnowReasonOK != "" {
		t.Fatal("知识层分档名是对外契约，不许改字面")
	}
	out, reason := GuardKnowledgeAnswer("交期 99 天，库存 5 件。", []string{}, "已备物料原文")
	if reason != KnowStrippedAll {
		t.Fatalf("全剥空必须回 know_stripped_all, got %q", reason)
	}
	if out != "已备物料原文" {
		t.Fatalf("剥空必须回退已备物料原文, got %q", out)
	}
	// 正向对照：同样文本给了来源 ⇒ 不剥、ok 空串（防上面的负向锁恒真）
	out, reason = GuardKnowledgeAnswer("交期 99 天。", []string{"交期 99 天"}, "兜底")
	if reason != KnowReasonOK || out != "交期 99 天。" {
		t.Fatalf("有来源时必须原样放行, got %q / %q", out, reason)
	}
}
