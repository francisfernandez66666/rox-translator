// ============ extract_test.go · 职责说明 ============
// crawler 包「LLM 采集输出解析」的单元测试：
//   - TestExtractJSONRobust    验证 extractJSON 对 LLM 输出 JSON 的鲁棒提取
//   - TestParseLLMOutputArray  验证 parseLLMOutput 兼容顶层数组形态（条目/安全句/包装对象）
// =============================================
package crawler

import "testing"

// TestExtractJSONRobust 验证 extractJSON 对 LLM 输出中的 JSON 提取鲁棒性：
// 覆盖带前后缀文本、含花括号值、Markdown 代码块、破损前导 JSON 等多种形态。
func TestExtractJSONRobust(t *testing.T) {
	s1 := `这是内容{"entries":[{"src":"a {x}","tgt":"b } y"}]}`
	if got := extractJSON(s1); got != `{"entries":[{"src":"a {x}","tgt":"b } y"}]}` {
		t.Fatalf("s1 got=%s", got)
	}
	s2 := `注意：开始\n{"phrases":[{"kind":"style","phrase":"请用","replacement":""}]} end g`
	if got := extractJSON(s2); got != `{"phrases":[{"kind":"style","phrase":"请用","replacement":""}]}` {
		t.Fatalf("s2 got=%s", got)
	}
	s3 := "```json\n{\"entries\":[{\"src\":\"自动驾驶\",\"tgt\":\"Autonomous Driving\"}]}\n```"
	if got := extractJSON(s3); got != `{"entries":[{"src":"自动驾驶","tgt":"Autonomous Driving"}]}` {
		t.Fatalf("s3 got=%s", got)
	}
	s4 := `{"phrases":[{"kind":"forbidden","phrase":"he said \"hey\"","replacement":"x"}]}`
	if got := extractJSON(s4); got != s4 {
		t.Fatalf("s4 got=%s", got)
	}
	s5 := `{oops true frag}\n{"entries":[{"src":"售楼","tgt":"Property"} ]} tail`
	if got := extractJSON(s5); got != `{"entries":[{"src":"售楼","tgt":"Property"} ]}` {
		t.Fatalf("s5 got=%s", got)
	}
	s6 := `{"entries":[{"src":"a{b","tgt":"c}d"}]}` // 值内花括号
	if got := extractJSON(s6); got != s6 {
		t.Fatalf("s6 got=%s", got)
	}
}

// TestParseLLMOutputArray 验证 parseLLMOutput 解析顶层数组 JSON（条目对象列表）与安全句对象。
func TestParseLLMOutputArray(t *testing.T) {
	// 顶层数组：元素为条目对象
	out, err := parseLLMOutput(`[{"src":"落地窗","tgt":"Floor-to-ceiling"},{"src":"玄关","tgt":"Entryway"}]`)
	if err != nil {
		t.Fatalf("parse array entries err=%v", err)
	}
	if len(out.Entries) != 2 || out.Entries[1].SrcText != "玄关" {
		t.Fatalf("entries=%+v", out.Entries)
	}
	// 顶层数组：元素为安全句对象
	out2, err := parseLLMOutput(`[{"kind":"forbidden","phrase":"fuck"},{"kind":"replace","phrase":"cheap","replacement":"inexpensive"}]`)
	if err != nil {
		t.Fatalf("parse array phrases err=%v", err)
	}
	if len(out2.Phrases) != 2 || out2.Phrases[1].Kind != "replace" {
		t.Fatalf("phrases=%+v", out2.Phrases)
	}
	// 对象包装数组：元素含 entries 字段
	out3, err := parseLLMOutput(`[{"entries":[{"src":"贷款","tgt":"Loan"}]}]`)
	if err != nil {
		t.Fatalf("parse wrapped err=%v", err)
	}
	if len(out3.Entries) != 1 || out3.Entries[0].TgtText != "Loan" {
		t.Fatalf("wrapped=%+v", out3.Entries)
	}
	// 混合兼修 kind 默认兜底
	out4, err := parseLLMOutput(`[{"phrase":"未知","kind":"whatever"}]`)
	if err != nil {
		t.Fatalf("kind default err=%v", err)
	}
	if len(out4.Phrases) != 1 || out4.Phrases[0].Kind != "style" {
		t.Fatalf("kind default=%+v", out4.Phrases)
	}
}
