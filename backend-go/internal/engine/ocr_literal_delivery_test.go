// ============ 本文件职责中文说明 ============
// 「对 OCR 结果只补全、不发散翻译」这一条用户令的机械锁（★ 2026-10-29）。
//
// 现网实证（同一张界面截图，OCR 已修对之后剩下的那一层坏）：
//
//	· 图上「战法」列的值本来就是英文键 `manual`，交付成了 `handbook` —— 把代码键意译成同义词；
//	· `卖出胜负`(4 字) 成了 "Sell the outcome of the competition"(35 字) —— 把 UI 短标签解释成一句散文。
//
// 两类动作不同，所以两条腿各治一类，**一条都不许省**：
//
//	I   机械腿：目标语 en ＋ 文档里有汉字 ＋ 源段是「单个全小写拉丁标识符」⇒ 交付只能是它自己
//	    （`enforceLatinIdentifierVerbatim`，收在 deliveryTranslations 这一个咽喉上）。
//	L   指令腿：图片/扫描件这批段的 system 前缀多挂一句「只直译、不解释、不同义替换、保持简短」
//	    （`literalOCRNote`），并且**必须进译文记忆键**（`tmContextHash`）——
//	    改了交付口径却不换键，等于让"直译档"复用"发散档"的旧译文（§22 那条「换了模型或改了提示词，
//	    旧译文继续投」就是这个形态）。
//	W   接线腿：三处批量调用点都必须带上这个开关（漏一处＝那条腿继续发散，而它恰恰是补译那一腿）。
//	C   成本腿：那一句是每块都付的固定开销 ⇒ 预估必须计入（AGENTS §一·5④ 同一把尺子）。
//
// 反向对照同样在位：表头哈希与"两个开关都关"时记忆键必须与改造前**逐字相同**，
// 否则上线当天全站历史命中作废（P5 那条纪律的同一条判据）。
// ============================================
package engine

import (
	"os"
	"strings"
	"testing"
)

// identifierVerdict 判一次「标识符在该不该被改回原文」，把三条边界一次跑完。
func TestEnforceLatinIdentifierVerbatim(t *testing.T) {
	cases := []struct {
		name  string
		langs []string // 只取第一个当目标语（与交付腿同口径）
		tr    map[string]string
		want  string
	}{
		{"现网形态：中文文档里英文键被意译成同义词", []string{"en"},
			map[string]string{"manual": "handbook", "买入": "Buy"}, "manual"},
		{"带下划线/数字的键同样受保护", []string{"en"},
			map[string]string{"fac_1": "facility one", "买入": "Buy"}, "fac_1"},
		{"只差大小写也算发散：Manual 必须改回 manual", []string{"en"},
			map[string]string{"manual": "Manual", "买入": "Buy"}, "manual"},
		{"首字母大写的正常词不属这一档", []string{"en"},
			map[string]string{"Hello": "你好", "买入": "Buy"}, "你好"},
		{"带空格的正常句不属这一档", []string{"en"},
			map[string]string{"double bump": "Double Bump", "买入": "Buy"}, "Double Bump"},
		{"目标语不是 en 时不生效（manual→ja 该翻）", []string{"ja"},
			map[string]string{"manual": "マニュアル", "买入": "買い"}, "マニュアル"},
		{"文档里没有汉字 ⇒ 这是外语正文文档，照翻", []string{"en"},
			map[string]string{"livre": "book", "page": "page"}, "book"},
		{"两字符以下的短串不属这一档（A1/B2 这类缩写留给正文）", []string{"en"},
			map[string]string{"ab": "xy", "买入": "Buy"}, "xy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := map[string]string{}
			for k, v := range c.tr {
				tr[k] = v
			}
			got := deliveryTranslations(c.langs[0], tr)
			key := ""
			for k := range c.tr {
				if k != "买入" && k != "page" {
					key = k
				}
			}
			if got[key] != c.want {
				t.Fatalf("源段 %q 的交付值应为 %q，实得 %q（全表 %+v）", key, c.want, got[key], got)
			}
		})
	}
}

// TestShapePredicateBoundaries 形状判据单独点名（它是上面那张表的唯一依据，改一侧必须同步另一侧）。
func TestShapePredicateBoundaries(t *testing.T) {
	for s, want := range map[string]bool{
		"manual": true, "momentum": true, "fac_1": true, "double_bump": true, "v2.2": true,
		"N_shape": false, "Buy": false, "Hello": false, "AWS": false, "0.00": false,
		"double bump": false, "买入": false, "ab": false, "": false, "a-b": false, "x/1": false,
	} {
		if got := latinIdentifierToken(s); got != want {
			t.Errorf("latinIdentifierToken(%q)=%v，期望 %v", s, got, want)
		}
	}
}

// TestTMContextHashKeepsLegacyKeysAlive 记忆键的向后兼容与"换口径必须换键"两半一起判。
func TestTMContextHashKeepsLegacyKeysAlive(t *testing.T) {
	hdr := "战法|买入额|卖出额"
	if got := tmContextHash(BatchOptions{}); got != "" {
		t.Fatalf("两个开关都关时必须是空串（否则全站历史命中当场作废），实得 %q", got)
	}
	if got := tmContextHash(BatchOptions{HeaderContext: hdr}); got != tmHeaderContextHash(hdr) {
		t.Fatalf("只开表头档时必须与改造前逐字相同，实得 %q vs %q", got, tmHeaderContextHash(hdr))
	}
	lit := tmContextHash(BatchOptions{LiteralOCR: true})
	if lit == "" {
		t.Fatal("只开直译档时键维度不能还是空——那会让发散档的旧译文被直译档复用")
	}
	if lit == tmContextHash(BatchOptions{}) {
		t.Fatal("直译档与关档同键（同上）")
	}
	both := tmContextHash(BatchOptions{HeaderContext: hdr, LiteralOCR: true})
	if both == lit || both == tmContextHash(BatchOptions{HeaderContext: hdr}) {
		t.Fatalf("两个开关同时开必须与各自单开都不同键，实得 %q", both)
	}
}

// TestLiteralOCRNoteOnlyWhenOn 提示词那一半：关档逐字不加，开档必须把三条硬约束都带上。
func TestLiteralOCRNoteOnlyWhenOn(t *testing.T) {
	if optionalLiteralOCRNote(false) != "" {
		t.Fatal("关档时那一句必须不拼（非图片件的提示词要与改造前逐字等价）")
	}
	on := optionalLiteralOCRNote(true)
	for _, must := range []string{"literally", "as short as the original", "synonym", "identifier"} {
		if !strings.Contains(on, must) {
			t.Errorf("直译指令缺 %q 这一条约束（每条各钉一种现网发散形态）", must)
		}
	}
	// 与表头语境同一条纪律：只走英文（纯 MT 模型会把中文指令当待译内容）。
	for _, r := range on {
		if r > 0x2E80 && r < 0x9FFF {
			t.Fatal("直译指令里出现了中日韩字符——会被纯 MT 模型当成待译内容一起翻")
		}
	}
}

// TestLiteralOCRAllowanceDerivesFromRealPrompt 预估必须从**真常量**现量（改模板即改预估）。
func TestLiteralOCRAllowanceDerivesFromRealPrompt(t *testing.T) {
	if got := LiteralOCRAllowanceTokens(0, 3); got != 0 {
		t.Fatalf("没有图片件时 allowance 必须为 0，实得 %d", got)
	}
	if got := LiteralOCRAllowanceTokens(2, 0); got != 0 {
		t.Fatalf("没有目标语言时 allowance 必须为 0，实得 %d", got)
	}
	one := LiteralOCRAllowanceTokens(1, 1)
	if one <= 0 {
		t.Fatalf("allowance 必须为正，实得 %d", one)
	}
	if two := LiteralOCRAllowanceTokens(2, 1); two != 2*one {
		t.Fatalf("应随图片件数线性：%d vs %d", two, 2*one)
	}
	if three := LiteralOCRAllowanceTokens(1, 3); three != 3*one {
		t.Fatalf("应随目标语言数线性（每语种各一批前缀）：%d vs %d", three, 3*one)
	}
	// 现量证据：allowance 里必须含真提示词的 token 数，而不是一个写死的整数。
	if ScriptTokens(literalOCRNote) <= 0 {
		t.Fatal("literalOCRNote 现量出 0 token ⇒ 这条 allowance 是写死的数字")
	}
}

// TestLiteralOCRWiredAtEveryBatchCall 三处批量调用点都必须带上开关（W 腿）。
//
// 只测函数本身不够：漏一处就是"那一腿继续发散翻译"，而它是**补译/硬闸**那一腿——
// 客户看到的恰恰是第一腿翻坏之后没人纠正的成品（AGENTS §三「判据级代替不了守卫级」）。
func TestLiteralOCRWiredAtEveryBatchCall(t *testing.T) {
	src := stripGoCommentsForGate(readSourceForGate(t, "file.go"))
	// 判据＝「带表头的调用点总数」与「其中带直译开关的个数」必须相等，
	// 而不是数某一个完整字面量（那会让下一位再加一个开关时把这条锁判成回归）。
	total := strings.Count(src, "BatchOptions{HeaderContext: fileHeaderCtx")
	withFlag := strings.Count(src, "LiteralOCR: ocrSourced")
	if total < 3 {
		t.Fatalf("只找到 %d 处批量调用点（应 ≥3：主批 / 缺失段补译 / 硬闸重译）⇒ 锚漂了，本条锁失明", total)
	}
	if withFlag != total {
		t.Errorf("%d 处批量调用点里只有 %d 处带 LiteralOCR ⇒ 漏掉那条腿的图片件仍会发散翻译", total, withFlag)
	}
	// 开关的判据只许有一处来源（图片格式 ∪ OCR 文档在途），不许三处各判一次。
	guard := stripGoCommentsForGate(readSourceForGate(t, "batch_options.go"))
	if !strings.Contains(guard, "func tmContextHash(opts BatchOptions) string") {
		t.Fatal("tmContextHash 不在了：直译档要么没进键、要么在调用方各算一份")
	}
	scope := stripGoCommentsForGate(readSourceForGate(t, "engine.go"))
	// 那一句必须真拼进 system 前缀（只加常量、不加拼接＝常量是死码，现网看不出来）。
	if !strings.Contains(scope, "optionalLiteralOCRNote(opts.LiteralOCR)") {
		t.Error("literalOCRNote 没被拼进 sysBatch：开关与常量都在，唯独咽喉没接（现网仍是发散译文）")
	}
	if strings.Contains(scope, "ctxHash: tmHeaderContextHash(opts.HeaderContext)") {
		t.Error("tmScope 还在只折表头 ⇒ 改了直译口径却不换键，发散档的旧译文会被直译档复用")
	}
}

// readSourceForGate 读本包一个源文件（守卫级锁按源码现算，不起依赖、不跑模型）。
// 名字与 refusal_delivery_gate_test.go 那份返回 []byte 的 readFileForGate 分开，两把锁各自用各自的。
func readSourceForGate(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(raw)
}
