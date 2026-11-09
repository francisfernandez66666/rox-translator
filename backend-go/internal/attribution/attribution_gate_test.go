// ============ attribution_gate_test.go · 职责说明 ============
// A2 门禁：终局档只许 inquiry（含反证）、tid<=0 fail-closed、纯函数 SnapshotFromBinding、
// 注入式 OutcomeSource 的 nil 分支（跳过＋WARN，不是静默 return）。
// 本包不连库，用假件观测「有没有落库」，避免把单测退化成集成测试。
// ================================================
package attribution

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/salesflow"
	"translator/internal/store"
)

// pinSQLiteDialect 固定测试方言（照迁入包统一口径，config.C 全局在用例结束还原）。
func pinSQLiteDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// fakeAttributionStore 记录终局回填调用，用假件观测引擎有没有真的落库。
type fakeAttributionStore struct {
	markOutcomeCalls int
	lastStage        string
}

// RecordReply 空实现（本用例不写归因记录）。
func (f *fakeAttributionStore) RecordReply(row store.ReplyAttributionRow) error { return nil }

// MarkHookedBeforeMessage 空实现。
func (f *fakeAttributionStore) MarkHookedBeforeMessage(tid, conversationID, customerMessageID int64) error {
	return nil
}

// MarkLeadCaptured 空实现。
func (f *fakeAttributionStore) MarkLeadCaptured(tid, conversationID, customerID int64) error {
	return nil
}

// MarkPendingHuman 空实现。
func (f *fakeAttributionStore) MarkPendingHuman(tid, conversationID, customerID int64) error {
	return nil
}

// MarkOutcome 记录被调用次数与终局档。
func (f *fakeAttributionStore) MarkOutcome(tid, messageID int64, stage string) error {
	f.markOutcomeCalls++
	f.lastStage = stage
	return nil
}

// SyncPackStats 空实现。
func (f *fakeAttributionStore) SyncPackStats(tid int64) error { return nil }

// fakeOutcomeSource 可注入的终局数据源假件。
type fakeOutcomeSource struct {
	called   bool
	signals  []OutcomeSignal
	failWith error
}

// ListOutcomes 返回预置信号并记录被调用。
func (f *fakeOutcomeSource) ListOutcomes(ctx context.Context, tid int64, limit int) ([]OutcomeSignal, error) {
	f.called = true
	return f.signals, f.failWith
}

// TestAttributionMarkOutcomeOnlyInquiry A2 主判据：非 inquiry 一律拒且不落库；inquiry 放行（反证）。
func TestAttributionMarkOutcomeOnlyInquiry(t *testing.T) {
	pinSQLiteDialect(t)
	fake := &fakeAttributionStore{}
	eng := &Engine{st: fake}
	for _, bad := range []string{"arrived", "ordered", "", "INQUIRY", "  "} {
		if err := eng.MarkOutcome(context.Background(), 1, 100, bad); err == nil {
			t.Fatalf("A2：终局档 %q 必须被拒", bad)
		} else if !strings.Contains(err.Error(), "只许") {
			t.Fatalf("A2：拒绝理由应点名「只许 inquiry」，实得 %q", err.Error())
		}
	}
	if fake.markOutcomeCalls != 0 {
		t.Fatalf("A2：非法终局档不许落库，实得 %d 次写入", fake.markOutcomeCalls)
	}
	// 反证：inquiry 真的放行并写入，证明校验不是「一律拒绝」。
	if err := eng.MarkOutcome(context.Background(), 1, 100, salesflow.OutcomeStageInquiry); err != nil {
		t.Fatalf("A2：inquiry 必须放行，实得 %v", err)
	}
	if fake.markOutcomeCalls != 1 || fake.lastStage != salesflow.OutcomeStageInquiry {
		t.Fatalf("A2：inquiry 应恰好写一次且原样透传，实得 calls=%d stage=%q",
			fake.markOutcomeCalls, fake.lastStage)
	}
}

// TestAttributionFailClosedOnInvalidTenant tid<=0 一律 fail-closed，且不回落默认租户。
func TestAttributionFailClosedOnInvalidTenant(t *testing.T) {
	pinSQLiteDialect(t)
	eng := NewEngine(nil)
	for _, tid := range []int64{0, -1, -99} {
		if err := eng.RecordReply(context.Background(), tid, store.ReplyAttributionRow{MessageID: 1}); !errors.Is(err, salesflow.ErrInvalidTenant) {
			t.Fatalf("RecordReply(tid=%d) 应返回 ErrInvalidTenant，实得 %v", tid, err)
		}
		if err := eng.MarkOutcome(context.Background(), tid, 1, salesflow.OutcomeStageInquiry); !errors.Is(err, salesflow.ErrInvalidTenant) {
			t.Fatalf("MarkOutcome(tid=%d) 应返回 ErrInvalidTenant，实得 %v", tid, err)
		}
		if err := eng.SyncPackStats(context.Background(), tid); !errors.Is(err, salesflow.ErrInvalidTenant) {
			t.Fatalf("SyncPackStats(tid=%d) 应返回 ErrInvalidTenant，实得 %v", tid, err)
		}
		if _, err := eng.BackfillOutcomes(context.Background(), tid, 10); !errors.Is(err, salesflow.ErrInvalidTenant) {
			t.Fatalf("BackfillOutcomes(tid=%d) 应返回 ErrInvalidTenant，实得 %v", tid, err)
		}
	}
}

// TestAttributionSnapshotFromBindingPure 纯函数：nil/企业包/行业包三条腿。
func TestAttributionSnapshotFromBindingPure(t *testing.T) {
	pinSQLiteDialect(t)
	if got := SnapshotFromBinding(nil); got.PackageID != "" || got.Version != "" {
		t.Fatalf("nil 绑定应返回空快照，实得 %+v", got)
	}
	ent := &Binding{
		PackageID: "pkg_industry", AppliedVersion: "1.0.0",
		EnterprisePackID: 7, EnterpriseCode: "pkg_ent", EnterpriseVersion: "1.1.0",
	}
	if got := SnapshotFromBinding(ent); got.PackageID != "pkg_ent" || got.Version != "1.1.0" {
		t.Fatalf("企业包应优先，实得 %+v", got)
	}
	plain := &Binding{PackageID: "pkg_industry", AppliedVersion: "2.0.0"}
	if got := SnapshotFromBinding(plain); got.PackageID != "pkg_industry" || got.Version != "2.0.0" {
		t.Fatalf("无企业包应回退行业包，实得 %+v", got)
	}
}

// TestAttributionOutcomeSourceNilBranchWarns nil 分支必须跳过并 WARN（源码级钉「不是静默 return」），
// 且注入路径反证：数据源真被调用，证明 nil 分支是「跳过」而非「永远不做事」。
func TestAttributionOutcomeSourceNilBranchWarns(t *testing.T) {
	pinSQLiteDialect(t)
	// 源码级守卫：nil 分支必须带 observability.Warn，不许退化成静默 return。
	src, err := os.ReadFile("outcome_backfill.go")
	if err != nil {
		t.Fatalf("读 outcome_backfill.go 失败: %v", err)
	}
	if !strings.Contains(string(src), "outcomeSource == nil") || !strings.Contains(string(src), "observability.Warn") {
		t.Fatal("nil 分支必须判空并出一条 WARN，不许静默 return")
	}
	eng := &Engine{}
	n, err := eng.BackfillOutcomes(context.Background(), 1, 10)
	if err != nil || n != 0 {
		t.Fatalf("数据源未注入应跳过且返回 (0,nil)，实得 (%d,%v)", n, err)
	}
	// 反证：注入后数据源真被调用、且非 inquiry 信号不写。
	source := &fakeOutcomeSource{signals: []OutcomeSignal{
		{MessageID: 11, OutcomeStage: "arrived"},
		{MessageID: 12, OutcomeStage: salesflow.OutcomeStageInquiry},
	}}
	fake := &fakeAttributionStore{}
	eng2 := &Engine{st: fake, outcomeSource: source}
	n, err = eng2.BackfillOutcomes(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("注入数据源后不应报错: %v", err)
	}
	if !source.called {
		t.Fatal("反证：注入的数据源必须被调用")
	}
	if n != 1 || fake.markOutcomeCalls != 1 || fake.lastStage != salesflow.OutcomeStageInquiry {
		t.Fatalf("反证：只应有 inquiry 一条写入，实得 n=%d calls=%d stage=%q",
			n, fake.markOutcomeCalls, fake.lastStage)
	}
}
