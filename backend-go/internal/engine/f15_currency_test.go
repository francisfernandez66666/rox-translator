// ============ f15_currency_test.go 职责中文说明 ============
// F-15 批 D（2026-09-25）货币保真指令的等值锁：translateInstruction 的
// **所有** 目标语言分支（zh/zh_hant/ja/ko/哈萨克/其余语种）× 两套界面语言（中/英），
// 尾部必须携带货币口径条款——¥3,800 被翻成 "R3,800"（兰特）正是缺这条指令。
// 锁「核心指令 + 条款」的拼接关系：条款追加在尾部、核心骨架逐字不变
// （B4 前缀缓存只刷新一次，调用点零改动由薄委托保证）。
// ========================================
package engine

import (
	"strings"
	"testing"
)

// TestUATBatchD_CurrencyClauseAppendedToEveryBranch 逐分支覆盖：任一 (target, uiLang) 组合
// 缺条款都记红——历史上「只给 en 加没给 zh 加」这类半截修复最隐蔽。
func TestUATBatchD_CurrencyClauseAppendedToEveryBranch(t *testing.T) {
	targets := []string{"zh", "zh_hant", "ja", "ko", "kk", "de", "ar", ""}
	for _, ui := range []string{"en", "zh"} {
		for _, tg := range targets {
			full := translateInstruction("zh", tg, ui)
			core := translateInstructionCore("zh", tg, ui)
			if !strings.HasPrefix(full, core) || full == core {
				t.Fatalf("target=%q ui=%q 未在核心指令尾部追加货币条款：full=%q", tg, ui, full)
			}
			clause := strings.TrimPrefix(full, core)
			if ui == "en" {
				for _, kw := range []string{"Currency", "CNY", "RMB", "yuan", "NOT"} {
					if !strings.Contains(clause, kw) {
						t.Fatalf("target=%q 英文条款缺关键词 %q：%q", tg, kw, clause)
					}
				}
			} else {
				for _, kw := range []string{"人民币", "¥/CNY/RMB/yuan", "货币符号"} {
					if !strings.Contains(clause, kw) {
						t.Fatalf("target=%q 中文条款缺关键词 %q：%q", tg, kw, clause)
					}
				}
			}
		}
	}
}

// TestUATBatchD_CurrencyClauseNamedCurrencies 等值锁（修复文档断言）：
// 条款必须点名 ¥/CNY/RMB/yuan 四个人民币写法，并显式禁止 R（兰特）等其他符号。
func TestUATBatchD_CurrencyClauseNamedCurrencies(t *testing.T) {
	en := translateInstruction("zh", "en", "en")
	for _, kw := range []string{"¥", "CNY", "RMB", "yuan"} {
		if !strings.Contains(en, kw) {
			t.Fatalf("英文指令缺人民币写法 %q：%q", kw, en)
		}
	}
	if !strings.Contains(en, "R,") {
		t.Fatalf("英文指令应显式点名 R（兰特）为禁止替换目标：%q", en)
	}
	zh := translateInstruction("zh", "ja", "zh")
	for _, kw := range []string{"¥/CNY/RMB/yuan", "R、$、€、£", "金额数值不变"} {
		if !strings.Contains(zh, kw) {
			t.Fatalf("中文指令缺 %q：%q", kw, zh)
		}
	}
}
