// ============ quality_gate_test.go · 职责说明 ============
// 历史自动审批工具的 Tier3 质量闸锁：未注入、结构化错误、语义错误、修复产物错误均拒绝。
// ================================================================
package main

import (
	"context"
	"errors"
	"testing"

	"translator/internal/qualityjudge"
	"translator/internal/store"
)

type fakeTier3Judge struct {
	result qualityjudge.Result
	err    error
	fixed  string
	ok     bool
}

func (f fakeTier3Judge) Evaluate(context.Context, string, string, string) (qualityjudge.Result, error) {
	return f.result, f.err
}
func (f fakeTier3Judge) Repair(context.Context, string, string) (string, bool, error) {
	return f.fixed, f.ok, f.err
}

func TestApproveTier3Gates(t *testing.T) {
	e := &store.KBStagedEntry{SrcLang: "zh", SrcText: "数据备份", TgtLang: "he", TgtText: "גיבוי נתונים", Tier: 3}
	if _, keep, err := approveTier3(context.Background(), nil, e); err == nil || keep {
		t.Fatal("未注入质量判据必须拒绝")
	}
	if _, keep, err := approveTier3(context.Background(), fakeTier3Judge{}, &store.KBStagedEntry{SrcLang: "zh", SrcText: "同形", TgtLang: "he", TgtText: "同形"}); err == nil || keep {
		t.Fatal("源译同形必须拒绝")
	}
	if _, keep, err := approveTier3(context.Background(), fakeTier3Judge{err: errors.New("down")}, e); err == nil || keep {
		t.Fatal("Judge 报错必须拒绝")
	}
	if fixed, keep, err := approveTier3(context.Background(), fakeTier3Judge{result: qualityjudge.Result{Pass: true, Conclusive: true}}, e); err != nil || !keep || fixed != "" {
		t.Fatalf("主判通过应放行：fixed=%q keep=%v err=%v", fixed, keep, err)
	}
	if _, keep, err := approveTier3(context.Background(), fakeTier3Judge{result: qualityjudge.Result{Pass: false, Conclusive: false}}, e); err == nil || keep {
		t.Fatal("Conclusive=false 即使 err=nil 也必须拒绝")
	}
	if _, keep, err := approveTier3(context.Background(), fakeTier3Judge{result: qualityjudge.Result{Conclusive: true}, fixed: "badlatin", ok: true}, e); err == nil || keep {
		t.Fatal("修复产物拉丁转写必须拒绝")
	}
	if fixed, keep, err := approveTier3(context.Background(), fakeTier3Judge{result: qualityjudge.Result{Conclusive: true}, fixed: "גיבוי", ok: true}, e); err != nil || !keep || fixed != "גיבוי" {
		t.Fatalf("合格修复产物应放行：fixed=%q keep=%v err=%v", fixed, keep, err)
	}
}
