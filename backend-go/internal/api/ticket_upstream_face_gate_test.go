// ============ 本文件职责中文说明 ============
// 2026-10-26 用户追加裁决九-1「工单上游发生的问题除了给运营超管，也要给客户看」的出栈锁。
//
// 这一面此前是**结构性看不见**：载荷里早就有 sources / unresolved_lines /
// upstream_dead_lines / truncated_lines 四档计数，但详情接口的质量视图只出
// qa_report / eval_scores / review_eval_scores / quality_flagged_langs 四件——
// 于是客户在页面上看见的是"完成了"，而"哪一语种、哪一档、几行"只存在于日志里。
// 把失败读数写成成功读数，是本仓反复登记的那一族（§一.3 那条热加载档位 `from` 撒谎同源）。
//
// 四条腿：① 有回填 ⇒ 四档逐值等值出栈；② 零回填 ⇒ 四个键**干脆不出**（不许常驻 0 让界面造空话）；
// ③ 旧数据（没有这些键）⇒ 读成空且不报错；④ 跨包键名逐字等值（载荷 tag ↔ 视图 tag），
// 抄错一个字母就是"接口说没回填而库里明明有"。
// ============================================
package api

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"translator/internal/orchestrator"
)

const upFaceFinal = `{"source_text":"第一段。\n第二段。","target_langs":["en"],` +
	`"translations":{"en":"One.\n第二段。"},"sources":{"en":"model_with_source_fallback"},` +
	`"unresolved_lines":{"en":1},"upstream_dead_lines":{"en":0},"truncated_lines":{"en":1},` +
	`"eval_scores":{"en":88}}`

// TestParseTicketQualityCarriesUpstreamFace ① 有回填时四档必须逐值出去。
func TestParseTicketQualityCarriesUpstreamFace(t *testing.T) {
	q := parseTicketQuality(upFaceFinal)
	if q.Sources["en"] != orchestrator.SourceModelWithFallback {
		t.Errorf("来源档必须原样出栈（回填过原文却仍报 model＝给失败读数背书），实得 %q", q.Sources["en"])
	}
	if q.UnresolvedLines["en"] != 1 {
		t.Errorf("未译出行总数应出栈，实得 %+v", q.UnresolvedLines)
	}
	if q.TruncatedLines["en"] != 1 {
		t.Errorf("截断档应出栈（它与上游断是两族，交付面那句要分开写），实得 %+v", q.TruncatedLines)
	}
	if _, ok := q.UpstreamDeadLines["en"]; !ok {
		t.Errorf("上游断档键应存在（本例显式给了 0，表示「这一族没发生」，与「键缺失」是两种形态），实得 %+v", q.UpstreamDeadLines)
	}
	if q.EvalScores["en"] != 88 {
		t.Errorf("既有质量字段不许被这次改动挤掉，实得 %+v", q.EvalScores)
	}
	// 出栈 JSON 的键名逐字钉住（前端与 SDK 都按字面取词，改名＝对外契约破坏）
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("序列化质量视图: %v", err)
	}
	for _, k := range []string{`"sources":`, `"unresolved_lines":`, `"upstream_dead_lines":`, `"truncated_lines":`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("出栈体缺键 %s ⇒ 前端读不到，实得 %s", k, b)
		}
	}
}

// TestParseTicketQualityOmitsCleanFace ② 零回填时四个键都不许出现。
// 取向：界面按"读不到＝这一单没有未译出行"渲染；常驻一个 0 会诱导前端写出一句
// "0 行未译出"的空话（本仓那条「空态不许伪装成检查过」）。
func TestParseTicketQualityOmitsCleanFace(t *testing.T) {
	clean := `{"source_text":"第一段。\n第二段。","target_langs":["en"],"translations":{"en":"One.\nTwo."}}`
	q := parseTicketQuality(clean)
	if len(q.Sources) != 0 || len(q.UnresolvedLines) != 0 || len(q.UpstreamDeadLines) != 0 || len(q.TruncatedLines) != 0 {
		t.Fatalf("干净单不该有回填读数，实得 %+v", q)
	}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("序列化: %v", err)
	}
	for _, k := range []string{"sources", "unresolved_lines", "upstream_dead_lines", "truncated_lines"} {
		if strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("零回填时键 %q 仍出现在出栈体里（应当整键缺席），实得 %s", k, b)
		}
	}
}

// TestParseTicketQualityLegacyPayload ③ 旧数据（载荷里根本没有这些键）⇒ 读成空、不报错、既有字段照旧。
func TestParseTicketQualityLegacyPayload(t *testing.T) {
	legacy := `{"source_text":"第一段。","translations":{"en":"One."},"sources":{"en":"model"},"eval_scores":{"en":70}}`
	q := parseTicketQuality(legacy)
	if q.Sources["en"] != "model" {
		t.Errorf("旧载荷里的 sources 仍要如实读出（不许这次改动把它读空），实得 %q", q.Sources["en"])
	}
	if len(q.UnresolvedLines) != 0 || len(q.TruncatedLines) != 0 {
		t.Errorf("旧载荷没有计数键 ⇒ 必须读成空而不是猜一个数，实得 %+v / %+v", q.UnresolvedLines, q.TruncatedLines)
	}
	if q.EvalScores["en"] != 70 {
		t.Errorf("既有评估分读数不许被新增字段挤掉，实得 %+v", q.EvalScores)
	}
	// 非 JSON（历史脏数据）仍是零值不报错——这一条是既有契约，本批改动不得把它变红
	if got := parseTicketQuality("not-json"); len(got.Sources) != 0 {
		t.Errorf("解析失败时应回零值结构，实得 %+v", got)
	}
}

// TestUpstreamFaceTagEquality ④ 跨包键名逐字等值：orchestrator 载荷 tag == api 视图 tag。
// 两侧各写一遍字面量迟早漂（漂了不报错，表现是"接口说没回填，库里明明有"）。
// 自证：判据必须**读得到** tag，读不到即判红（锚漂了＝闸门失明，比红灯更坏）。
func TestUpstreamFaceTagEquality(t *testing.T) {
	pairs := map[string]string{
		"unresolved_lines":    "",
		"upstream_dead_lines": "",
		"truncated_lines":     "",
		"sources":             "",
	}
	payloadSrc := readGoForGate(t, "../orchestrator/workflow.go")
	viewSrc := readGoForGate(t, "tickets.go")
	for tag := range pairs {
		// 前缀形态（`json:"tag`）：两侧都可以带 `,omitempty`，把它写进 needle 就会
		// 因为**一侧有序列化选项而另一侧没有**而假红——本批首跑真踩在这里。
		needle := `json:"` + tag
		if !strings.Contains(payloadSrc, needle) {
			t.Errorf("载荷侧读不到 tag %q ⇒ 判据锚漂了（字段被改名或删除），本条锁失明", needle)
			continue
		}
		if !strings.Contains(viewSrc, needle) {
			t.Errorf("出栈视图侧读不到 tag %q ⇒ 客户面拿不到那一档（键名漂了不报错，表现只能是「接口说没有」）", needle)
		}
	}
}

// readGoForGate 读源文件正文（失败即红，不把"没读到"当成"没有该字段"）。
func readGoForGate(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v（判据读不到文件＝失明，不许静默放行）", path, err)
	}
	return string(b)
}
