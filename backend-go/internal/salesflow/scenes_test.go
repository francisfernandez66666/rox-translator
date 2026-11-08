// ============ 本文件职责中文说明 ============
// S3 三面归一的行为判据：三份场景配置按全集判、各自 Validate 通过、编码与 key 一致、
// 词表非空；引导流五步稳定且不含任何行业分支字面（差异只许进配置与资产层）。
// 本文件不叫 *_gate_test.go：属行为锁，不进 gateguard C1 的家族级锁集合。
// ========================================
package salesflow

import (
	"strings"
	"testing"
)

// 三面配置实例：恰好三份、各自合法、编码与 key 对齐、词表非空。
func TestSceneConfigsThreeFacesAreValid(t *testing.T) {
	cfgs := SceneConfigs()
	if len(cfgs) != 3 {
		t.Fatalf("S3 三面归一必须恰好三份配置实例，实得 %d", len(cfgs))
	}
	for _, key := range []string{SceneCrossBorder, ScenePrivate, SceneInStore} {
		c, ok := cfgs[key]
		if !ok {
			t.Fatalf("缺场景配置: %s", key)
		}
		if err := c.Validate(); err != nil {
			t.Fatalf("场景 %s 配置不合法: %v", key, err)
		}
		if c.IndustryCode != key {
			t.Fatalf("场景 %s 的编码与 key 不一致: %s", key, c.IndustryCode)
		}
		if len(c.AnchorLexicon) == 0 {
			t.Fatalf("场景 %s 必须有注入的跨语言锚位词表", key)
		}
	}
}

// 引导流 schema：五步稳定、字段齐全、零行业字面。
func TestGuidedFlowSchemaIsIndustryNeutral(t *testing.T) {
	steps := GuidedFlowSchema()
	if len(steps) != 5 {
		t.Fatalf("通用引导流必须是五步（门店端 P1–P5 抽象），实得 %d", len(steps))
	}
	seen := map[string]bool{}
	for _, s := range steps {
		if s.Key == "" || s.TitleKey == "" || len(s.Fields) == 0 {
			t.Fatalf("引导流步骤不完整: %+v", s)
		}
		if seen[s.Key] {
			t.Fatalf("引导流步骤 key 重复: %s", s.Key)
		}
		seen[s.Key] = true
		parts := append([]string{s.Key, s.TitleKey}, s.Fields...)
		for _, p := range parts {
			for _, banned := range BannedIndustryBranchTokens() {
				if banned != "" && strings.Contains(p, banned) {
					t.Fatalf("引导流 schema 出现行业分支字面 %q（步骤 %s）", banned, s.Key)
				}
			}
		}
	}
}
