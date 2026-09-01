package crawler

import "testing"

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
}
