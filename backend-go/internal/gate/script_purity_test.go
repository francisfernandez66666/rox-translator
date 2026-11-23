// ============ script_purity_test.go · 职责说明（2026-11-23 用户令三项升级之二/三）============
// 「无拉丁转写」与「标点体系」硬闸锁：
//   - 俄语转写/未登记拉丁串必须判死（tv13 审计 L151 `komponentnaya` 坐实）；
//   - 全角标点残留必须判死，容忍度可配、KB 词条字符豁免；
//   - zh 目标语、白名单词、KB 词条字符不得误伤。
//
// ================================================
package gate

import (
	"strings"
	"testing"
)

func runLatin(source, target, tr string, terms []TermRequirement) *GateResult {
	return RunWithTerms(source, target, tr, terms)
}

func TestLatinTransliterationGate(t *testing.T) {
	src := "供应商平台承载 12 组件框架"
	// 正向：俄语转写必须判死
	res := runLatin(src, "he", "מערכת ספקים מתאימה ל-12-komponentnaya framework", nil)
	if res.Pass {
		t.Fatal("俄语转写 komponentnaya/framework 必须判死")
	}
	found := false
	for _, c := range res.Checks {
		if c.Name == "无拉丁转写" && !c.Pass {
			found = true
		}
	}
	if !found {
		t.Fatalf("未产出「无拉丁转写」检查项：%+v", res.Checks)
	}
	// 反向：白名单词放行
	res2 := runLatin(src, "he", "מערכת ספקים עם Lookalike ו-Google", nil)
	for _, c := range res2.Checks {
		if c.Name == "无拉丁转写" && !c.Pass {
			t.Fatalf("白名单词被误杀：%s", c.Detail)
		}
	}
	// 反向：KB 词条 target 里的拉丁串放行
	kbTerms := []TermRequirement{{Source: "ROX 平台", Target: "Salesforce Einstein"}}
	res3 := runLatin(src, "he", "מערכת Salesforce Einstein", kbTerms)
	for _, c := range res3.Checks {
		if c.Name == "无拉丁转写" && !c.Pass {
			t.Fatalf("KB 词条拉丁串被误杀：%s", c.Detail)
		}
	}
	// 反向：zh 目标语不射程
	res4 := runLatin(src, "zh", "组件框架 framework", nil)
	for _, c := range res4.Checks {
		if c.Name == "无拉丁转写" && !c.Pass {
			t.Fatal("zh 目标语不该启用拉丁转写闸")
		}
	}
}

func TestSetLatinAllowlistRuntime(t *testing.T) {
	SetLatinAllowlist("he", []string{"Framework"})
	defer SetLatinAllowlist("he", nil)
	res := RunWithTerms("框架", "he", "מסגרת Framework", nil)
	for _, c := range res.Checks {
		if c.Name == "无拉丁转写" && !c.Pass {
			t.Fatalf("配置白名单未生效：%s", c.Detail)
		}
	}
}

func TestCJKPunctGate(t *testing.T) {
	src := "门店反馈闭环"
	// 正向：全角分号必须判死（默认容忍 0）
	res := RunWithTerms(src, "he", "סיכום תקשורת AI שיגור；② טופס", nil)
	found := false
	for _, c := range res.Checks {
		if c.Name == "标点体系" && !c.Pass {
			found = true
		}
	}
	if !found {
		t.Fatalf("全角标点未判死：%+v", res.Checks)
	}
	// 反向：KB 词条 target 含该标点字符 ⇒ 豁免
	kbTerms := []TermRequirement{{Source: "节点", Target: "节点；"}}
	res2 := RunWithTerms(src, "he", "סיכום；", kbTerms)
	for _, c := range res2.Checks {
		if c.Name == "标点体系" && !c.Pass {
			t.Fatalf("KB 豁免未生效：%s", c.Detail)
		}
	}
	// 反向：容忍度配置生效
	SetCJKPunctMax("he", 2)
	defer SetCJKPunctMax("he", 0)
	res3 := RunWithTerms(src, "he", "א。ב。ג", nil)
	for _, c := range res3.Checks {
		if c.Name == "标点体系" && !c.Pass {
			t.Fatalf("容忍度配置未生效：%s", c.Detail)
		}
	}
	// 反向：zh 目标语不射程
	res4 := RunWithTerms(src, "zh", "门店反馈闭环；", nil)
	for _, c := range res4.Checks {
		if c.Name == "标点体系" && !c.Pass {
			t.Fatal("zh 目标语不该启用标点体系闸")
		}
	}
}

func TestLatinViolationsDedup(t *testing.T) {
	vs := LatinTransliterationViolations("he", "aaa komponentnaya komponentnaya", nil)
	if len(vs) != 1 || vs[0] != "komponentnaya" {
		t.Fatalf("去重失败：%v", vs)
	}
	if strings.Join(LatinTransliterationViolations("he", "", nil), "") != "" {
		t.Fatal("空译文不该有产物")
	}
}
