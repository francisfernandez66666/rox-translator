// ============ 本文件职责中文说明 ============
// FlowDef 双模式覆盖单元测试：fast 精简流水线 / API 自动任务跳过审批。
package orchestrator

import (
	"testing"

	"translator/internal/store"
)

// newTestFlow 构建全步骤启用的流程定义（与 DefaultFlowSteps 键一致）。
func newTestFlow() *FlowDef {
	keys := []string{"kb_match", "ai_initial", "evals_initial", "review", "evals_review", "gate", "culture_gate", "qa", "approval", "feedback"}
	fd := &FlowDef{Name: "test", Steps: []*Step{}}
	for _, k := range keys {
		fd.Steps = append(fd.Steps, &Step{Key: k, Enabled: true})
	}
	return fd
}

// enabledKeys 统计启用步骤集合。
func enabledKeys(fd *FlowDef) map[string]bool {
	out := map[string]bool{}
	for _, s := range fd.Steps {
		if s.Enabled {
			out[s.Key] = true
		}
	}
	return out
}

// TestModeOverrideFast fast 工单：关闭 KB/双评估/硬闸/文化闸/自迭代，强制保留 初翻+校对+QA。
func TestModeOverrideFast(t *testing.T) {
	fd := newTestFlow()
	applyModeOverride(fd, &store.Ticket{Mode: "fast", CreatedBy: 7})
	got := enabledKeys(fd)
	for _, k := range []string{"kb_match", "evals_initial", "evals_review", "gate", "culture_gate", "feedback"} {
		if got[k] {
			t.Fatalf("fast 模式应关闭 %s", k)
		}
	}
	for _, k := range []string{"ai_initial", "review", "qa"} {
		if !got[k] {
			t.Fatalf("fast 模式应保留 %s", k)
		}
	}
}

// TestModeOverrideProInternal pro 内部工单：不改变租户配置的步骤启停。
func TestModeOverrideProInternal(t *testing.T) {
	fd := newTestFlow()
	applyModeOverride(fd, &store.Ticket{Mode: "", CreatedBy: 7})
	if len(enabledKeys(fd)) != 10 {
		t.Fatalf("pro 内部工单不应改动任何步骤")
	}
}

// TestModeOverrideAPITask API 自动任务（CreatedBy=0）：不停审批台、不做未审自迭代；pro 保留 KB 与硬闸。
func TestModeOverrideAPITask(t *testing.T) {
	fd := newTestFlow()
	applyModeOverride(fd, &store.Ticket{Mode: "pro", CreatedBy: 0})
	got := enabledKeys(fd)
	if got["approval"] || got["feedback"] {
		t.Fatalf("API 任务应跳过 approval/feedback")
	}
	if !got["kb_match"] || !got["gate"] || !got["review"] {
		t.Fatalf("pro API 任务应保留知识库/硬闸/校对")
	}
}

// TestModeOverrideFastAPITask fast + API 叠加：两者禁用集合并集生效。
func TestModeOverrideFastAPITask(t *testing.T) {
	fd := newTestFlow()
	applyModeOverride(fd, &store.Ticket{Mode: "fast", CreatedBy: 0})
	got := enabledKeys(fd)
	for _, k := range []string{"kb_match", "gate", "approval", "feedback", "evals_review"} {
		if got[k] {
			t.Fatalf("fast+API 应关闭 %s", k)
		}
	}
	for _, k := range []string{"ai_initial", "review", "qa"} {
		if !got[k] {
			t.Fatalf("fast+API 应保留 %s", k)
		}
	}
}
