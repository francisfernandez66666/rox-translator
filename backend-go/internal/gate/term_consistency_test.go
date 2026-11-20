// ============ term_consistency_test.go · 职责说明 ============
// P3 高频词一致性后检的单测（2026-11-20）：统一译法出现次数不足/源词残留即失败，
// 全部统一则通过；别名算合规；KB 词（FromHighfreq=false）不进本检查（职责分离）；
// 另配 TermRequirement 新字段的向后兼容用例。
// ============================================
package gate

import "testing"

// TestCheckTermConsistency 术语一致性后检：统一译法出现次数不足/源词残留即失败，
// 全部统一则通过；KB 词（FromHighfreq=false）不进本检查。
func TestCheckTermConsistency(t *testing.T) {
	snapshot := []TermRequirement{
		{Source: "线索", Target: "lead", FromHighfreq: true},
		{Source: "经销商", Target: "dealer", Aliases: []string{"distributor"}, FromHighfreq: true},
		{Source: "极石", Target: "ROX", FromHighfreq: false}, // KB 词，不该被本检查管
	}
	cases := []struct {
		name  string
		src   string
		tr    string
		failN int
	}{
		{"全部统一", "线索线索线索经销商经销商", "lead lead lead dealer distributor", 0},
		{"线索漂移", "线索线索线索", "lead lead hint", 1},
		{"源词残留", "线索线索", "lead 线索", 1},
		{"别名也算合规", "经销商经销商", "dealer distributor", 0},
		{"KB词漂移不管", "极石极石", "ROX 极石", 0}, // FromHighfreq=false 跳过
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			checks := CheckTermConsistency(c.src, c.tr, snapshot)
			if len(checks) != c.failN {
				t.Errorf("CheckTermConsistency(%q, %q) 失败项=%d want=%d; checks=%v", c.src, c.tr, len(checks), c.failN, checks)
			}
		})
	}
	// 反证：变体计数改"出现过即 pass"会让"线索漂移"漏检——上面该用例 failN=1 已锁。
}

// TestTermRequirementAliases Aliases 字段向后兼容：默认空时行为不变。
func TestTermRequirementAliases(t *testing.T) {
	tm := TermRequirement{Source: "线索", Target: "lead"}
	if len(tm.Aliases) != 0 || tm.FromHighfreq {
		t.Error("默认构造的 TermRequirement 应无别名、非高频词")
	}
}
