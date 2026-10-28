// ============ 本文件职责中文说明 ============
// 工单 T20261027152356CMY（2026-10-27）复盘锁：**初翻空槽必须送进校对腿**。
//
// 缺陷本体（现网实证，图片工单 136）：
//
//	`已实现=加权成本重放；浮动=市值-成本×数量；30s刷新` 这一行在 `<sN>` 批量里被初翻模型
//	（Hunyuan-MT）整槽吐空；而校对腿（现网配 GPT）是链路上**唯一**可能由另一族模型经手的环节，
//	它却被 `if tr == "" || tr == "[翻译失败]" { continue }` 把空槽剔出了清单——
//	于是那一段在校对腿上"物理不存在"，后面硬闸 3 轮 + 逐行补译 3 轮仍然全是初翻模型在打同一句话，
//	八次调用八次空槽，最终按原文回落并在交付文档里点名。
//	而同一句话走单条腿（试用接口实测）初翻模型给出的译文完全正确 ⇒
//	不是"这行译不出来"，是"译得出来的那个模型从未被问到"。
//
// 三条腿（每条都配正向对照，否则负向恒真＝空转）：
//
//	A 单段 + 空槽 ⇒ **必须**送校对腿，且校对给出的译文被采纳（旧门槛 `len(idxs)<2` 会把它挡掉）；
//	B 单段 + 有译文 ⇒ 仍按旧口径跳过（不能把"单段审校收益低"那条一并拆了）；
//	C 失败占位 `[翻译失败]` ⇒ 按"未译出"送进去，但**占位符字面量绝不出现在 prompt 里**
//	  （否则模型会当成一条真译文去润色，输出带占位符的垃圾）。
//
// 本文件复用 image_retry_19_test.go 的假上游（imgEngine），不连库、不打真模型。
// ============================================
package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// reviewCaptureStub 记录送进校对腿的 user 段（要断言"空槽确实进了 prompt"）。
type reviewCaptureStub struct {
	mu    sync.Mutex
	users []string
}

// reviewEngine 装配一个只回固定内容的假上游，并保留收到的每一段 prompt。
func reviewEngine(t *testing.T, reply string) (*Engine, *reviewCaptureStub, *imgStub) {
	t.Helper()
	cap := &reviewCaptureStub{}
	e, stub := imgEngine(t, func(user string) string {
		cap.mu.Lock()
		cap.users = append(cap.users, user)
		cap.mu.Unlock()
		return reply
	})
	return e, cap, stub
}

// TestReviewBatchSafeFeedsEmptySlot A 腿：空槽进清单、被翻出来、写回结果。
func TestReviewBatchSafeFeedsEmptySlot(t *testing.T) {
	const src = "已实现=加权成本重放；浮动=市值-成本×数量；30s刷新"
	const good = "Realized = weighted cost replay; Floating = market value - cost × quantity; 30s refresh"
	e, cap, stub := reviewEngine(t, "1. "+good)

	// ★ 单段 + 空译文：旧形态被 `len(idxs) < 2` 与 `tr == ""` 双重挡掉，是本锁的靶心。
	out := e.reviewBatchSafe(context.Background(), []string{src}, []string{""}, "en", nil)

	if stub.calls() == 0 {
		t.Fatalf("正向对照：空槽根本没送校对腿（一次上游都没打）⇒ A 腿的绿是无中生有")
	}
	if len(out) != 1 || out[0] != good {
		t.Fatalf("校对腿翻出来的译文必须写回结果，实得 %q", out[0])
	}
	// 空槽在 prompt 里必须呈现为「【原文】有内容 / 【译文】空」——这是校对模型判断"要翻"的唯一依据。
	var seen bool
	for _, u := range cap.users {
		if strings.Contains(u, src) && strings.Contains(u, "【译文】\n") {
			seen = true
		}
	}
	if !seen {
		t.Errorf("送进校对腿的 prompt 里没有「【原文】目标句 + 【译文】空」这一条，实得：%q",
			strings.Join(cap.users, "\n---\n"))
	}
}

// TestReviewBatchSafeSkipsLoneTranslatedPair B 腿：单段且本来就有译文 ⇒ 仍跳过（旧口径不许被拆）。
func TestReviewBatchSafeSkipsLoneTranslatedPair(t *testing.T) {
	const src = "蓝牙钥匙已激活。"
	const tr = "The Bluetooth key has been activated."
	e, _, stub := reviewEngine(t, "1. whatever")

	out := e.reviewBatchSafe(context.Background(), []string{src}, []string{tr}, "en", nil)

	if stub.calls() != 0 {
		t.Errorf("单段有译文时不该送审校（旧口径：收益低），实打上游 %d 次", stub.calls())
	}
	if len(out) != 1 || out[0] != tr {
		t.Errorf("跳过时必须原样返回原译文，实得 %q", out[0])
	}
}

// TestReviewBatchSafeNeverLeaksFailurePlaceholder C 腿：占位符按"未译出"送，但字面量不进 prompt。
func TestReviewBatchSafeNeverLeaksFailurePlaceholder(t *testing.T) {
	const src = "产品方案书"
	const good = "Product Proposal"
	e, cap, stub := reviewEngine(t, "1. "+good)

	out := e.reviewBatchSafe(context.Background(), []string{src}, []string{translationFailureMark}, "en", nil)

	if stub.calls() == 0 {
		t.Fatalf("失败占位槽同样属于「未译出」，必须送校对腿，实打上游 0 次")
	}
	for _, u := range cap.users {
		if strings.Contains(u, translationFailureMark) {
			t.Errorf("失败占位符字面量不许出现在校对 prompt 里（模型会当成真译文去润色）：%q", u)
		}
	}
	if len(out) != 1 || out[0] != good {
		t.Errorf("占位槽被翻出来后必须写回，实得 %q", out[0])
	}
}
