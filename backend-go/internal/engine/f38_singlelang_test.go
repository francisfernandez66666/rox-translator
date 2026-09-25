// ============ f38_singlelang_test.go 职责中文说明 ============
// F-38 批 D（2026-09-25）文本通道装配级回归：singleLangRaw 成功返回前的「短格长度爆炸」复核。
// 文件链靠 5 个写入点的 IsTranslationUsable 拦截（translation_guard_test.go 已钉），
// 但 /api/chat/stream 与 HandleText 的产物**直接交付**、不经过文件链写回点——
// 若本函数不自查，「6 字单元格翻出 2100 字符邮件」在文本通道照样静默出货。
// 策略（修复文档决策项 5）：回退重译优先（≤2 次，与缩翻硬闸同范式）；
// 重译仍爆炸 ⇒ 按失败返回错误，交 translateLangsConcurrent 既有 3 轮重试队列，
// 最终置空由漏译率硬闸/漏翻可见性告警兜底——绝不交付可疑串。
// 全程 httptest 假上游（零真实 LLM 调用）、不落 PostgreSQL。
// ========================================
package engine

import (
	"context"
	"testing"
	"time"
)

// f38Harness 复用 fwpGate 假上游装配并补齐 singleLangRaw 需要的熔断器
// （applySegmentGates 路径不经过 breaker，直接驱动 singleLangRaw 时必须显式初始化）。
func f38Harness(t *testing.T, replies ...string) *fwpGateHarness {
	t.Helper()
	h := fwpGateEngine(t, replies...)
	h.eng.breaker = NewBreaker(3, time.Minute)
	return h
}

// TestUATBatchD_SingleLangRawAdoptsRethranslationAfterExplosion 首译爆炸→带反馈重译过闸：
// 采纳修正译文正常返回（回退重译优先，不把上游一次抖动放大成失败）。
func TestUATBatchD_SingleLangRawAdoptsRethranslationAfterExplosion(t *testing.T) {
	good := "Product Proposal"
	h := f38Harness(t, f38MailGarbage, good)
	out, err := h.eng.singleLangRaw(context.Background(), "产品方案书", "en", nil, "zh", "", 0)
	if err != nil {
		t.Fatalf("重译已过闸应正常返回，got err=%v", err)
	}
	if out != good {
		t.Fatalf("应采纳重译后的修正译文，got %q", out)
	}
	if n := h.hitCount(); n < 2 {
		t.Fatalf("首译爆炸后必须发起带反馈的重译（至少 2 次上游调用），实际 %d", n)
	}
}

// TestUATBatchD_SingleLangRawFailsInsteadOfDeliveringExplosion 重译仍爆炸 ⇒ 返回错误。
// 改坏了会怎样：若有人把兜底改成「照常返回 content」，用户界面就会再次收到
// 「Dear Valued Customer…」整封邮件——本用例钉的就是「宁可置空走漏译告警」。
func TestUATBatchD_SingleLangRawFailsInsteadOfDeliveringExplosion(t *testing.T) {
	h := f38Harness(t, f38MailGarbage) // 上游稳定吐垃圾长文
	out, err := h.eng.singleLangRaw(context.Background(), "产品方案书", "en", nil, "zh", "", 0)
	if err == nil {
		t.Fatalf("重译仍爆炸必须按失败处理（绝不移交可疑串），却返回了 %d rune 的译文", len([]rune(out)))
	}
	if out != "" {
		t.Fatalf("失败路径应返回空串，got %q", out)
	}
	// 1 次首译 + 2 次反馈重译 = 3 次上游调用（回退重译 ≤2 次的等值锁）
	if n := h.hitCount(); n != 3 {
		t.Fatalf("重译轮次漂移（策略=回退重译优先≤2，仍爆炸才失败）：实际上游调用 %d 次", n)
	}
}

// TestUATBatchD_SingleLangRawNormalLongTextUnaffected 正常长文本不受影响：
// 源文 >12 rune 时长度判据整体不生效（长段落/长语系的高长度比是合法翻译，不得误杀）。
func TestUATBatchD_SingleLangRawNormalLongTextUnaffected(t *testing.T) {
	src := "本设备支持四十五种工作模式，请在维护周期内按时校准，并遵守以下每一条安全规范要求"
	tr := "This device supports forty-five working modes; please calibrate within the maintenance cycle and comply with every safety requirement listed below."
	h := f38Harness(t, tr)
	out, err := h.eng.singleLangRaw(context.Background(), src, "en", nil, "zh", "", 0)
	if err != nil || out != tr {
		t.Fatalf("正常长文被长度判据误杀：err=%v out=%q", err, out)
	}
	if n := h.hitCount(); n != 1 {
		t.Fatalf("合规译文应零重试一次通过，实际上游调用 %d 次", n)
	}
}
