// ============ retry_test.go · 职责说明 ============
// 清洗重试护栏锁：判错后最多重试3次、3次都失败才删除；判据报错不删。
// ============================================================
package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/qualityjudge"
)

func TestRetryDecisionDeleteOnlyAfterAllFail(t *testing.T) {
	// 3 轮都判错且修不好 ⇒ 删除
	v, _, reason := retryDecision([]oneRound{
		{reason: "term=30"},
		{reason: "term=30"},
		{reason: "term=30"},
	})
	if v != verdictDelete {
		t.Fatalf("3轮全失败应删除，实得 %v (%s)", v, reason)
	}
}

func TestRetryDecisionRepairWins(t *testing.T) {
	// 第1轮修不好，第2轮修正成功 ⇒ 回写（不是等到第3轮）
	v, fixed, _ := retryDecision([]oneRound{
		{reason: "term=30"},
		{reason: "term=30", fixed: "מערכת בלמים"},
	})
	if v != verdictRepaired || fixed != "מערכת בלמים" {
		t.Fatalf("第2轮修正成功应回写，实得 v=%v fixed=%q", v, fixed)
	}
}

func TestRetryDecisionPassWins(t *testing.T) {
	// 第1轮判据报错，第2轮达标 ⇒ 保留
	v, _, _ := retryDecision([]oneRound{
		{err: errors.New("429")},
		{pass: true, reason: "term=90"},
	})
	if v != verdictKeep {
		t.Fatalf("报错后重试达标应保留，实得 %v", v)
	}
}

func TestRetryDecisionAllErrorsNeverDelete(t *testing.T) {
	// 3 轮全是判据报错 ⇒ 跳过不删（绝不把网络抖动当错译）
	v, _, reason := retryDecision([]oneRound{
		{err: errors.New("429")},
		{err: errors.New("timeout")},
		{err: errors.New("empty")},
	})
	if v != verdictErr {
		t.Fatalf("全报错应跳过不删，实得 %v (%s)", v, reason)
	}
}

func TestRetryDecisionMixedErrorBreaksWrongStreak(t *testing.T) {
	v, _, reason := retryDecision([]oneRound{
		{reason: "term=30"},
		{err: errors.New("timeout")},
		{reason: "term=30"},
	})
	if v != verdictErr {
		t.Fatalf("判错序列被报错打断时不许删除，实得 %v (%s)", v, reason)
	}
}

// apply 必须先落完整备份再改库，TM/KB 同事务，断点只能写在事务提交之后。
func TestApplyBackupPrecedesMutationAndCheckpointFollows(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(raw)
	for _, mutation := range []string{
		`applyCleanChangeTx(ctx, raw, e, "", true)`,
		`applyCleanChangeTx(ctx, raw, e, fixed, false)`,
	} {
		pos := strings.Index(code, mutation)
		if pos < 0 {
			t.Fatalf("找不到原子写库调用 %s", mutation)
		}
		backup := strings.LastIndex(code[:pos], "if !persistRec(rec)")
		if backup < 0 {
			t.Fatalf("%s 之前没有先写完整备份", mutation)
		}
	}
	checkpoint := strings.Index(code, "markDone(e.ID)")
	lastMutation := strings.LastIndex(code, "applyCleanChangeTx(ctx, raw, e, fixed, false)")
	if checkpoint < lastMutation {
		t.Fatal("断点不能早于事务提交")
	}
	errBranch := strings.Index(code, "case verdictErr:")
	if errBranch < 0 || strings.Index(code[errBranch:], "continue // 无法判定不落 checkpoint") < 0 {
		t.Fatal("无法判定分支必须跳过 checkpoint")
	}
	if !strings.Contains(code, "if *apply && stat.writeFail > 0") || !strings.Contains(code, "os.Exit(3)") {
		t.Fatal("写失败必须非零退出，不能交给调度方误判成功")
	}

	sqlRaw, err := os.ReadFile("sql.go")
	if err != nil {
		t.Fatal(err)
	}
	sqlCode := string(sqlRaw)
	clearPos := strings.Index(sqlCode, "clearTMSegment(tx, e)")
	deletePos := strings.Index(sqlCode, "deleteKBEntryTx(tx, e)")
	syncPos := strings.Index(sqlCode, "syncTMSegment(tx, e, fixed)")
	updatePos := strings.Index(sqlCode, "updateKBEntryTx(tx, e, fixed)")
	commitPos := strings.Index(sqlCode, "return tx.Commit()")
	if clearPos < 0 || deletePos < clearPos || syncPos < 0 || updatePos < syncPos || commitPos < updatePos {
		t.Fatal("TM/KB 必须同事务且提交收尾：删除先清 TM，回写先同步 TM")
	}
}

type repairErrorJudge struct{}

func (repairErrorJudge) Evaluate(context.Context, string, string, string) (qualityjudge.Result, error) {
	return qualityjudge.Result{Pass: false, Conclusive: true, Reason: "term=30"}, nil
}
func (repairErrorJudge) Repair(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("429")
}

type inconclusiveJudge struct{}

func (inconclusiveJudge) Evaluate(context.Context, string, string, string) (qualityjudge.Result, error) {
	return qualityjudge.Result{Pass: false, Conclusive: false, Reason: "两腿都不可用"}, nil
}
func (inconclusiveJudge) Repair(context.Context, string, string) (string, bool, error) {
	return "", false, nil
}

func TestInconclusiveNeverCountsAsWrongStreak(t *testing.T) {
	v, _, reason := cleanWithRetry(context.Background(), inconclusiveJudge{}, cleanEntry{SrcText: "数据备份", TgtLang: "pl"}, 3)
	if v != verdictErr || !strings.Contains(reason, "未给出有效裁决") {
		t.Fatalf("Conclusive=false 即使 err=nil 也必须跳过不删：v=%v reason=%s", v, reason)
	}
}

func TestRepairErrorNeverCountsAsWrongStreak(t *testing.T) {
	v, _, reason := cleanWithRetry(context.Background(), repairErrorJudge{}, cleanEntry{SrcText: "数据备份", TgtLang: "pl"}, 3)
	if v != verdictErr {
		t.Fatalf("Repair 报错必须打断判错连续链，不能删除：v=%v reason=%s", v, reason)
	}
}

func TestLoadCheckpointRejectsBadLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "checkpoint.txt"), []byte("1\nbroken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCheckpoint(dir); err == nil {
		t.Fatal("断点坏行必须报错，不能静默丢断点")
	}
}

func TestRetryDecisionSingleRound(t *testing.T) {
	if v, _, _ := retryDecision([]oneRound{{reason: "bad"}}); v != verdictDelete {
		t.Fatalf("retries=1 判错应删，实得 %v", v)
	}
}
