// ============ gate_test.go · 职责说明 ============
// gate 包单元测试：覆盖 RunWithTerms 的 KB 术语遵循校验（第 9 项）——
// 源文出现 KB 命中术语时译文必须体现规定译法，否则判不通过。
// 含命中/未命中/源文不含术语不适用/术语列为空等同原 Run 行为四类场景。
// =============================================
package gate

import (
	"strings"
	"testing"
)

// TestRunWithTermsTermFollow KB 术语遵循校验：
//  1. 源文含术语「极石」、译文未含 ROX → 不通过且检查项为「术语遵循」；
//  2. 译文含 ROX → 通过；
//  3. 源文不含该术语 → 校验不适用，不因该项判失败；
//  4. 术语列为空 → 行为与 Run 完全一致（8 项校验）。
func TestRunWithTermsTermFollow(t *testing.T) {
	source := "山海无界，极石致远。国际标准赋能制造，极石汽车驰骋全球山海"
	terms := []TermRequirement{{Source: "极石", Target: "ROX"}}

	t.Run("译文未含规定译法应拦截", func(t *testing.T) {
		g := RunWithTerms(source, "ar", "لا حدود للجبال والبحار، وتسعى «جي شي» إلى آفاق بعيدة.", terms)
		if g.Pass {
			t.Fatalf("译文未含 ROX 应判不通过，实为通过")
		}
		found := false
		for _, c := range g.Checks {
			if c.Name == "术语遵循" && !c.Pass {
				found = true
				if !strings.Contains(c.Detail, "极石") || !strings.Contains(c.Detail, "ROX") {
					t.Fatalf("Detail 应包含术语与规定译法，实得: %q", c.Detail)
				}
			}
		}
		if !found {
			t.Fatalf("应存在「术语遵循」失败检查项，实得 checks=%v", g.Checks)
		}
	})

	t.Run("译文含规定译法应通过", func(t *testing.T) {
		g := RunWithTerms(source, "ar", "لا حدود للجبال والبحار، وتمضي ROX نحو آفاق بعيدة.", terms)
		if !g.Pass {
			t.Fatalf("译文含 ROX 应通过，实为失败: %v", g.Checks)
		}
	})

	t.Run("源文不含术语则不适用", func(t *testing.T) {
		other := "今天天气不错。"
		g := RunWithTerms(other, "en", "The weather is nice today.", terms)
		for _, c := range g.Checks {
			if c.Name == "术语遵循" && !c.Pass {
				t.Fatalf("源文不含术语不应触发术语遵循失败: %v", g.Checks)
			}
		}
		if !g.Pass {
			t.Fatalf("常规译文应通过全部校验，实为失败: %v", g.Checks)
		}
	})

	t.Run("术语列为空与 Run 等价", func(t *testing.T) {
		src := "测试数字 100 与 50。"
		tr := "Test numbers 100 and 50."
		base := Run(src, "en", tr)
		with := RunWithTerms(src, "en", tr, nil)
		if base.Pass != with.Pass || len(base.Checks) != len(with.Checks) {
			t.Fatalf("nil 术语应等价 Run: Run=%v RunWithTerms=%v", base.Pass, with.Pass)
		}
	})

	t.Run("多术语部分未遵循仍拦截", func(t *testing.T) {
		multiSrc := "极石车主俱乐部欢迎您。"
		multi := []TermRequirement{{Source: "极石", Target: "ROX"}, {Source: "车主", Target: "owner"}}
		tr := "Welcome to the ROX car club."
		g := RunWithTerms(multiSrc, "en", tr, multi)
		if g.Pass {
			t.Fatalf("多术语仅遵循其一（ROX 已含、owner 缺失）应判不通过: %v", g.Checks)
		}
	})
}

// TestNormalizeBrandTerm 品牌术语归一化：译文把品牌名带上车辆类后缀时应剥除、统一为纯品牌名。
func TestNormalizeBrandTerm(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		brand string
		want  string
	}{
		{"ro单后缀", "ROX vehicles expanding globally", "ROX", "ROX expanding globally"},
		{"ro汽车联合实验室", "The Weiqiao-ROX motor lightweight joint lab", "ROX", "The Weiqiao-ROX lightweight joint lab"},
		{"俄文 автомобиль", "ROX автомобиль ищет партнёров", "ROX", "ROX ищет партнёров"},
		{"多后缀连写", "ROX motor car sales boom", "ROX", "ROX sales boom"},
		{"大小写后缀", "ROX Vehicles are popular", "ROX", "ROX are popular"},
		{"无后缀不动", "ROX is expanding", "ROX", "ROX is expanding"},
		{"brand为空原样", "ROX vehicles", "", "ROX vehicles"},
		{"译文无brand不动", "Honda vehicles expanding", "ROX", "Honda vehicles expanding"},
		{"句首品牌", "ROX Motor unveiled its plans", "ROX", "ROX unveiled its plans"},
		{"后无空格", "ROX vehicles.", "ROX", "ROX."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeBrandTerm(c.in, c.brand); got != c.want {
				t.Fatalf("NormalizeBrandTerm(%q, %q) = %q, want %q", c.in, c.brand, got, c.want)
			}
		})
	}
}
