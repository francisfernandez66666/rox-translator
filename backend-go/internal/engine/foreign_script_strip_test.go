// ============ foreign_script_strip_test.go · 职责说明 ============
// P2 字符级外来脚本清洗的单测（2026-11-20）：韩文/西里尔整词删除、目标脚本/拉丁品牌/
// 数字/标点/孤立字母保留；拉丁目标与未知语言 fail-soft 不清洗；反劣化确保正常译文过
// PostProcessTranslation 后数字不丢。
// ============================================
package engine

import (
	"strings"
	"testing"
)

// TestStripForeignScriptsInNonTarget 字符级外来脚本清洗：韩文/西里尔整词删除，
// 目标脚本/拉丁品牌/数字/标点/孤立字母保留。
func TestStripForeignScriptsInNonTarget(t *testing.T) {
	cases := []struct {
		lang string
		in   string
		want string
		// note 非空时失败信息点名该缺陷——变异体 M0365 按这条字面判
		// "红在锁自己的消息上"，而不是红在通用比对差异上。
		note string
	}{
		{"he", "דרך 신호 פרסום", "דרך  פרסום", ""},
		{"he", "דרך канал פרסום", "דרך  פרסום", ""},
		{"he", "דרך Facebook פרסום 30%", "דרך Facebook פרסום 30%", ""}, // 拉丁品牌+数字保留
		{"he", "שלום עולם", "שלום עולם", ""},                           // 纯目标不动
		{"ar", "عبر القناة 신호", "عبر القناة ", ""},
		{"ru", "Через 신호 канал", "Через  канал", ""}, // 西里尔目标：韩文删、西里尔留
		{"en", "Hello 世界", "Hello 世界", ""},           // 拉丁目标不清洗（中文由 StripChineseInNonZh 管）
		{"xx", "anything 신호", "anything 신호", ""},     // 未知语言 fail-soft

		// ★ 2026-11-22 真值验收 L143：模型串语时吐出的泰文「组合符 + 单字母」残片必须清掉。
		//   旧实现只数「字母」，组合符（Mn）被当普通字符逐个放行 ⇒ 交付物里留下看不懂的脏字符，
		//   而闸门只报「脚本一致」不说是什么（实测产物 `### ******ั้ ื: vs ผู้่******`）。
		{"he", "### ******ั้ ื: vs ผู้่******", "### ****** : vs ******",
			"外来组合符不清洗（L143 泰文残片复发：模型串语时交付物里留脏字符）"},
		// 目标脚本自带的组合符（希伯来 niqqud）一个都不许动
		{"he", "שָׁלוֹם", "שָׁלוֹם", ""},
		// 拉丁分解式重音（Inherited 组合符）不是「外来书写体系残留」，必须保留
		{"he", "café", "café", ""},
		{"he", "cafe\u0301", "cafe\u0301", ""},
		// 泰语是目标时，泰文组合符同样是目标脚本的一部分
		{"th", "สวัสดี 신호", "สวัสดี ", ""},
		// 孤立外来单字母（无组合符）仍保留：误删代价大于放过
		{"he", "דרך α פרסום", "דרך α פרסום", ""},
		// ★ 2026-11-22 真值验收 tv8 的 L45：`מפ춘` —— 单个韩文音节**夹在希伯来词中间**。
		//   它与上面 α 那一档的差别是「孤立」的定义：α 两侧是空白（真正孤立），
		//   춘 紧贴目标脚本字母（已是那个词的一部分，删掉即恢复原词）。
		//   旧实现按「连续同脚本字母数」判，两种形态一视同仁 ⇒ 交付物里留脏字符，
		//   而脚本纯度闸只记「hangul 1」，客户看到的是「מפ춘」这种读不出来的词。
		{"he", "מודל הדימוגמה מפ춘 נקודות", "מודל הדימוגמה מפ נקודות",
			"外来单字母嵌在目标语单词里必须清掉（L45 希伯来词中的韩文字母复发）"},
		// 反向对照：同样单个外来字母，两侧是标点/数字时仍算孤立，保留不动
		{"he", "דרך (α) 30% פרסום", "דרך (α) 30% פרסום", ""},
	}
	for _, c := range cases {
		if got := StripForeignScriptsInNonTarget(c.in, c.lang); got != c.want {
			if c.note != "" {
				t.Errorf("%s：StripForeignScriptsInNonTarget(%q, %s) = %q, want %q", c.note, c.in, c.lang, got, c.want)
				continue
			}
			t.Errorf("StripForeignScriptsInNonTarget(%q, %s) = %q, want %q", c.in, c.lang, got, c.want)
		}
	}
}

// TestPostProcessTranslationNoFalseStrip 反劣化：正常目标语译文过清洗后内容不丢。
func TestPostProcessTranslationNoFalseStrip(t *testing.T) {
	for _, c := range []struct{ lang, in string }{
		{"he", "דרך Facebook ו-Instagram, ההמרה = 30%"},
		{"ar", "عبر القناة.rpm 30%"},
		{"ru", "Через канал 30%"},
	} {
		got := PostProcessTranslation(c.in, c.lang)
		if !strings.Contains(got, "30") {
			t.Errorf("清洗误删数字：lang=%s in=%q got=%q", c.lang, c.in, got)
		}
	}
}
