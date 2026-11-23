// ============ retry_test.go · 职责说明 ============
// 清洗重试护栏锁：判错后最多重试3次、3次都失败才删除；判据报错不删。
// ============================================================
package main

import (
	"errors"
	"testing"
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

func TestRetryDecisionSingleRound(t *testing.T) {
	if v, _, _ := retryDecision([]oneRound{{reason: "bad"}}); v != verdictDelete {
		t.Fatalf("retries=1 判错应删，实得 %v", v)
	}
}
