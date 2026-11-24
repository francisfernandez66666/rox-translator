package main

import (
	"reflect"
	"testing"

	"translator/internal/evals"
	"translator/internal/llm"
	"translator/internal/qualityjudge"
)

func TestCleanArmMatchesProduct(t *testing.T) {
	c := probeCases()[0]
	a := cleanArm("test", 64)
	if a.sys != qualityjudge.JudgeSystemPrompt() {
		t.Fatal("clean 臂 system 提示词与产品漂移")
	}
	if got, want := a.user(c), qualityjudge.JudgeUserPrompt(c.src, c.langName(), c.tgt, c.back); got != want {
		t.Fatalf("clean 臂 user 提示词与产品漂移:\ngot=%q\nwant=%q", got, want)
	}
}

func TestNoThinkArmUsesSharedConstructor(t *testing.T) {
	for _, a := range probeArms() {
		if a.name == "nothink" {
			if !reflect.DeepEqual(a.extra, llm.NoThinkingExtra()) {
				t.Fatalf("nothink 臂未复用共享关思考参数: %#v", a.extra)
			}
			return
		}
	}
	t.Fatal("nothink 臂不存在")
}

func TestDim5ArmMatchesProduct(t *testing.T) {
	c := probeCases()[0]
	a := dim5Arms([]string{"TEST"}, 200)
	if len(a) != 1 || a[0].sys != "" {
		t.Fatalf("五维臂形态不对: %#v", a)
	}
	if got, want := a[0].user(c), evals.EvaluatePrompt(c.src, c.langName(), c.tgt); got != want {
		t.Fatalf("五维臂提示词与产品漂移:\ngot=%q\nwant=%q", got, want)
	}
}

func TestParseProbeScoresStrict(t *testing.T) {
	cases := []struct {
		name    string
		content string
		finish  string
		parsed  bool
	}{
		{"正常", `{"term":70,"semantic":80}`, "stop", true},
		{"截断", `{"term":70,"semantic":80}`, "length", false},
		{"缺字段", `{"term":70}`, "stop", false},
		{"越界", `{"term":101,"semantic":80}`, "stop", false},
		{"空对象", `{}`, "stop", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, parsed := parseProbeScores(tc.content, tc.finish, false)
			if parsed != tc.parsed {
				t.Fatalf("parsed=%v want=%v", parsed, tc.parsed)
			}
		})
	}
}

func TestParseProbeScoresFiveDimensions(t *testing.T) {
	body := `{"term":80,"grammar":90,"semantic":85,"numunit":100,"style":75,"total":86}`
	term, sem, total, parsed := parseProbeScores(body, "stop", true)
	if !parsed || term != 80 || sem != 85 || total != 86 {
		t.Fatalf("五维回执解析失真: term=%v sem=%v total=%v parsed=%v", term, sem, total, parsed)
	}
	if _, _, _, parsed := parseProbeScores(`{"term":80,"semantic":90}`, "stop", true); parsed {
		t.Fatal("缺维度五维回执不许按二维解析冒充有效")
	}
}

func TestProbeCaseCounts(t *testing.T) {
	counts := map[string]int{}
	for _, c := range probeCases() {
		counts[c.grp]++
	}
	if counts["prod"] != 6 || counts["ctrl"] != 4 || counts["real"] != 15 {
		t.Fatalf("探针样本分组数量漂移: %#v", counts)
	}
}
