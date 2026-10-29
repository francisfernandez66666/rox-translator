// ============ 本文件职责中文说明 ============
// 「行尾符号残渣」清理的机械锁（★ 2026-10-29 现网实证）。
//
// 现网形态（同一张界面截图）：源文 `已实现=加权成本重放；浮动=市值-成本×数量；30s 刷新`
// 交付成 `Realized=…; …; 30s refresh （） =；=-×；30`——
// 尾巴 `（） =；=-×；30` 不含任何字母，是模型把源行的标点与数字又拼了一遍。
// 它既不是独立符号行（那些早已被 identity 预填挡在上游），也不在 §19「行尾骨架」
// （只认中文句读）的射程里，所以单开这一条。
//
// 这一族的难点全在**反向对照**：判据稍微放宽就会把
// 「以金额收尾」「以编号收尾」「以单个冒号收尾」的正常译文一起砍掉——
// 那是"砍模型写对的"，比留一节残渣坏得多。所以下表里合法用例占一半以上。
// ============================================
package engine

import (
	"strings"
	"testing"
)

func TestStripTrailingSymbolGarbage(t *testing.T) {
	cases := []struct{ name, in, want string }{
		// —— 该剥的（现网真值）——
		{"现网形态：标点/运算符/数字被重吐一遍", "Realized=Weighted Cost Revaluation; Floating=Market Value-Cost×Quantity; 30s refresh （） =；=-×；30",
			"Realized=Weighted Cost Revaluation; Floating=Market Value-Cost×Quantity; 30s refresh"},
		{"短一点的同族残渣", "total profit and loss [( )] = ", "total profit and loss"},
		{"两个以上结构符才剥：多个运算符", "quantity × price = ×", "quantity × price"},
		{"尾巴里带中文标点也要剥干净", "Synced successfully （）＝；", "Synced successfully"},

		// —— 不许动的（正常收尾）——
		{"以金额收尾（逗号/点/货币符都不是结构符）", "Total: ￥1,003.00", "Total: ￥1,003.00"},
		{"以数字+单位收尾", "Refresh every 30s", "Refresh every 30s"},
		{"单个冒号收尾", "Currently:", "Currently:"},
		{"单个右括号收尾", "Use the global single-ticket amount (CNY)", "Use the global single-ticket amount (CNY)"},
		{"以编号收尾", "600279.SH", "600279.SH"},
		{"中文正文以句号收尾", "已实现盈亏＝0.00。", "已实现盈亏＝0.00。"},
		{"只挂一个运算符 ⇒ 不动（保守边界，宁可留一节也不砍正常收尾）", "quantity × price =", "quantity × price ="},
		{"整段都是残渣 ⇒ 保留原值不留白", "（） =；", "（） =；"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripTrailingSymbolGarbage(c.in); got != c.want {
				t.Fatalf("输入 %q\n期望 %q\n实得 %q", c.in, c.want, got)
			}
		})
	}
}

// TestTrailingGarbageWiredAtDelivery 交付咽喉必须真调用它（判据级代替不了守卫级）。
func TestTrailingGarbageWiredAtDelivery(t *testing.T) {
	code := stripGoCommentsForGate(readSourceForGate(t, "translation_guard.go"))
	if !strings.Contains(code, "stripTrailingSymbolGarbage(stripRefusalSentences(src, v))") {
		t.Fatal("交付咽喉没接行尾残渣清理（函数在、没人调＝死码，现网一行都不会变）")
	}
	// 反向：不许在别处再抄一份（两份判据迟早打架），调用点只许这一处。
	if n := strings.Count(code, "stripTrailingSymbolGarbage("); n != 2 { // 1 处定义 + 1 处调用
		t.Fatalf("stripTrailingSymbolGarbage 的出现次数应为 2（定义+咽喉调用），实得 %d ⇒ 有人另抄了一份", n)
	}
}
