// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 立（用户裁决①②的出栈腿）。`api/tickets.go` 的 `ticketQualityView` 新增了
// `compliance_skipped` / `compliance_items` 两个键，本文件钉住它们的四件事：
//
//	V1 **解析得到且字段不丢**：`Lead`（处置说明）与 `Terms`（命中词面）必须原样过一遍，
//	   少一个字段客户界面上就是半句话。`Lead` 尤其关键——文本工单不产 .md，
//	   前端详情区读的就是这一格（本批真踩过：`ComplianceItem` 当初压根没有这个字段，
//	   而常量 `engine.SensitiveSkippedLead` 已经写好了，零消费方）。
//	V2 **空值口径**：没有跳过时两个键**整条缺席**（不是 0、不是 null、不是 `[]`）。
//	   出 0 会被读成"这一单本该跳 0 处"，与"没跳"合起来是一句给失败背书的话。
//	V3 **跨包键名等值**：api 侧解析结构的 json tag 必须与 `orchestrator.ticketPayload` 逐字相同。
//	   抄错一个字母的症状是"库里明明有、接口说没有"——本仓已经有 `sources` 那条同型教训，
//	   这里把它机械锁住（不是靠人记得）。
//	V4 **不与三档混记**：只带 `compliance_skipped` 的载荷，`unresolved_lines` 等三档必须仍为空。
//	   混记＝把"平台决定不翻"记成"没翻出来"，还会抬起「质检存疑」徽标（§19 族二）。
//
// ⚠️ V2 必须**真序列化**再看字节，不能只判字段为 nil：
// `omitempty` 写在 struct 上却没生效（例如误写成 `json:"x,omitempty"` 的非指针 map 之外的形状、
// 或有人把 tag 改成 `json:"x"`）时，字段 nil 但输出仍是 `"x":null`——那正是本仓
// 「读数与事实相反」那一族。判字节才有射程。
// ============================================
package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"translator/internal/orchestrator"
)

// TestComplianceViewParsesAndKeepsEveryField V1：解析后 Lead / Terms / Side 一个都不丢。
func TestComplianceViewParsesAndKeepsEveryField(t *testing.T) {
	const lead = "内容合规审核：该处未翻译，原文已按原样保留"
	final := `{
	  "translations": {"en": "a\nb\nc"},
	  "compliance_skipped": {"en": 2},
	  "compliance_items": [
	    {"lang":"en","line":2,"terms":["枪支"],"side":"input","lead":"` + lead + `"},
	    {"lang":"en","line":3,"side":"output","lead":"` + lead + `"}
	  ]
	}`
	v := parseTicketQuality(final)

	if v.ComplianceSkipped["en"] != 2 {
		t.Fatalf("compliance_skipped.en 应为 2，实得 %v", v.ComplianceSkipped)
	}
	if len(v.ComplianceItems) != 2 {
		t.Fatalf("compliance_items 应解析出 2 条，实得 %d 条: %+v", len(v.ComplianceItems), v.ComplianceItems)
	}
	first := v.ComplianceItems[0]
	if first.Line != 2 || first.Side != orchestrator.ComplianceSideInput {
		t.Errorf("第一条的行号/成因档不对：%+v", first)
	}
	if len(first.Terms) != 1 || first.Terms[0] != "枪支" {
		t.Errorf("用户令要求「写明跳过的禁词」：词面必须过这一层，实得 %+v", first.Terms)
	}
	// ★ 这一条是本批补字段的防线：Lead 丢了，前端只能自己再造一句措辞。
	for i, it := range v.ComplianceItems {
		if it.Lead != lead {
			t.Errorf("第 %d 条的处置说明必须原样过一遍（它是唯一事实源的载体），实得 %q", i+1, it.Lead)
		}
	}
	// 输出侧刻意不回显词面（只点名第几行）：这条口径在 api 侧也不能被"顺手补上"。
	if len(v.ComplianceItems[1].Terms) != 0 {
		t.Errorf("输出侧（模型自产）不该带词面，实得 %+v", v.ComplianceItems[1].Terms)
	}
}

// TestComplianceViewKeysAbsentWhenNothingSkipped V2：空值两态。
func TestComplianceViewKeysAbsentWhenNothingSkipped(t *testing.T) {
	// ① 解析面：载荷里没有这两键 ⇒ 字段为 nil。
	v := parseTicketQuality(`{"translations":{"en":"ok"}}`)
	if v.ComplianceSkipped != nil {
		t.Errorf("没有跳过时 ComplianceSkipped 必须是 nil，实得 %v", v.ComplianceSkipped)
	}
	if v.ComplianceItems != nil {
		t.Errorf("没有跳过时 ComplianceItems 必须是 nil，实得 %v", v.ComplianceItems)
	}

	// ② 出栈面：真序列化，两个键必须**一个字节都不出现**。
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	body := string(b)
	for _, key := range []string{"compliance_skipped", "compliance_items"} {
		if strings.Contains(body, key) {
			t.Errorf("没有跳过时 %q 必须整条缺席（不是 0/null/[]），实得出参: %s", key, body)
		}
	}

	// ③ 在位对照：有跳过时两个键都必须出现（否则"恒缺席"的实现也能过 ②）。
	v2 := parseTicketQuality(`{"compliance_skipped":{"ja":1},"compliance_items":[{"lang":"ja","line":1,"side":"input","lead":"L"}]}`)
	b2, err := json.Marshal(v2)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, key := range []string{"compliance_skipped", "compliance_items"} {
		if !strings.Contains(string(b2), key) {
			t.Errorf("有跳过时 %q 必须出现在出参里，实得: %s", key, b2)
		}
	}
}

// TestComplianceViewKeysMatchPayloadTags V3：跨包键名等值（抄错一个字母即红）。
//
// 判据读两边的源码取 tag，而不是自己再拼一遍字符串——
// 自己拼一遍的话，改了一侧这断言仍按"我以为的名字"判，抄错照样绿。
func TestComplianceViewKeysMatchPayloadTags(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s 失败（这一腿没跑到，不许当通过）: %v", p, err)
		}
		return string(b)
	}
	apiSrc := read("tickets.go")
	orchSrc := read("../orchestrator/workflow.go")

	for _, tag := range []string{"compliance_skipped", "compliance_items"} {
		// 两侧都必须**恰好出现 tag 字面量**（api 侧出栈字段 + 解析侧 tag 两处都写）。
		// ⚠️ 不带引号判：真源码里的形态是 `json:"compliance_skipped,omitempty"`，
		// 带引号匹配会恒 0（"判据自己扫错了靶子"那一族，首跑真踩）。
		if n := strings.Count(apiSrc, tag); n < 2 {
			t.Errorf("api/tickets.go 里 %q 应至少出现 2 次（出栈字段 + 解析侧 tag，两处都要写），实得 %d 次", tag, n)
		}
		if !strings.Contains(orchSrc, tag) {
			t.Errorf("orchestrator/workflow.go 的载荷里没有 %q ⇒ 两侧键名漂了（症状是「库里明明有、接口说没有」）", tag)
		}
	}
}

// TestComplianceViewNotMixedIntoUnresolved V4：不与三档混记。
//
// 只带合规两键的载荷，三档必须仍为 nil —— 这是"把设计给客户看成缺陷"那条的机械防线。
// 反向对照（V4b）在同一用例内：真带 unresolved_lines 时它必须解析得出来，
// 否则"三档恒空"的实现也能过上面那条。
func TestComplianceViewNotMixedIntoUnresolved(t *testing.T) {
	v := parseTicketQuality(`{"compliance_skipped":{"en":3},"compliance_items":[]}`)
	if v.UnresolvedLines != nil || v.UpstreamDeadLines != nil || v.TruncatedLines != nil {
		t.Errorf("合规跳过**不许**落进「未译出」那三档（成因与补救动作都不同），实得 unresolved=%v dead=%v truncated=%v",
			v.UnresolvedLines, v.UpstreamDeadLines, v.TruncatedLines)
	}
	v2 := parseTicketQuality(`{"unresolved_lines":{"en":2},"upstream_dead_lines":{"en":2}}`)
	if v2.UnresolvedLines["en"] != 2 || v2.UpstreamDeadLines["en"] != 2 {
		t.Fatalf("反向对照失败：真带未译出三档时必须解析得出来，实得 %+v / %+v ⇒ "+
			"上面那条「不许混记」的判据是空转的", v2.UnresolvedLines, v2.UpstreamDeadLines)
	}
	if v2.ComplianceSkipped != nil {
		t.Errorf("反向对照：没有合规跳过时该键必须仍缺席，实得 %v", v2.ComplianceSkipped)
	}
}
