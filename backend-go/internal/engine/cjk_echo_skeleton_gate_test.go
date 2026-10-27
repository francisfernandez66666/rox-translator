// ============ 本文件职责中文说明 ============
// 判据 3″ 的锁：**中文回显被 StripChineseInNonZh 洗成符号骨架后，不许再判"可用"**。
//
// 缺陷形态（本地实测复现，工单 T20261027152356CMY 的邻座缺陷）：
//
//	模型原样吐回 `已实现=加权成本重放；浮动=市值-成本×数量；30s刷新`
//	→ PostProcessTranslation 的 StripChineseInNonZh 删掉汉字 → `=；=-×；30s`
//	→ 判据 3（同文）比的是**洗之前**那份原文，早已放过；判据 4~9 一条都不命中
//	→ **usable=true**，一段"压根没翻"的东西进了交付物。
//
// 四条腿（每条都配反向对照，否则负向恒真＝空转）：
//
//	A 端到端：回显原文 ⇒ 后处理确实洗成骨架 ⇒ 判据必须判不可用（这是本锁的靶心）；
//	B 不许误杀：同一源文的正常英文译文 ⇒ 可用；
//	C 不许越界：源文无中文（`0`／`¥1003.00`／`manua`）⇒ 判据整条不适用（那一档归判据 3′/6）；
//	D 非拉丁目标语（俄/阿/泰）的正常译文同样要放行 —— hasLetterRun 用的是 unicode.IsLetter，
//	  不是 [A-Za-z]，否则"中文→俄语"的整条腿会被这条判据误杀。
//
// ★ 射程边界（如实钉住，免得后人以为这条判据管了它）：
//
//	工单交付 md 第 39 行那种**尾部挂一串骨架**的形态（`Current: All allowed; …
//	amount ：；""0，。`，源文「当前：全部允许…；"0" 。」）**不**在本判据射程内：
//	它整行有连续拉丁字母，按本判据必然放行。
//	★ 2026-10-27：该族已由 `stripTrailingCJKNotes` / `trimCJKSkeletonTail` 接手
//	（锁在 postprocess_tail_skeleton_test.go）。本文件的 E 腿保留，作用是**防止射程被改宽**——
//	一旦有人把判据 3″ 改成"含骨架即不可用"，E 腿立刻红。
//
// ============================================
package engine

import "testing"

// TestCJKEchoWashedToSkeleton A 腿：回显 → 洗成骨架 → 判不可用（端到端，跨后处理与判据两条链）。
func TestCJKEchoWashedToSkeleton(t *testing.T) {
	src := "已实现=加权成本重放；浮动=市值-成本×数量；30s刷新"
	washed := PostProcessTranslation(src, "en")
	// 前提校验：这条腿只有在"后处理确实把它洗掉了"时才有意义。
	// 若哪天 StripChineseInNonZh 改了行为（比如不再删中文），这里必须红——
	// 否则下面的断言会变成"拿原文判原文"，恒真且毫无信息量。
	if washed == src {
		t.Fatalf("前提失效：PostProcessTranslation 没有删掉中文，实得 %q ⇒ 本锁的样本不构成「洗成骨架」形态", washed)
	}
	if IsTranslationUsable(src, washed) {
		t.Errorf("中文回显被洗成骨架后必须判不可用（否则「没翻」伪装成「翻了」进交付物），实得可用：src=%q washed=%q", src, washed)
	}
	// 直接对判据函数钉一次：洗出来的骨架必须命中，不靠别的判据兜。
	if !cjkEchoWashedToSkeleton(src, washed) {
		t.Errorf("判据本体未命中骨架 %q（它必须独立成立，不能靠其他判据抢先）", washed)
	}
}

// TestCJKEchoSkeletonDoesNotKillRealTranslation B 腿：正常译文不许被误杀。
func TestCJKEchoSkeletonDoesNotKillRealTranslation(t *testing.T) {
	src := "已实现=加权成本重放；浮动=市值-成本×数量；30s刷新"
	for _, good := range []string{
		"Realized = weighted cost replay; Floating = market value - cost × quantity; refresh every 30s",
		"Realized=weighted cost replay; Floating=market value-cost×quantity; 30s refresh",
		"Realized P&L = weighted cost replay; Floating P&L = market value - cost × quantity; 30s refresh",
		"Amount", // 极短但合法的译文（单段补译腿的典型产物）
	} {
		if cjkEchoWashedToSkeleton(src, good) {
			t.Errorf("正常译文被误判成骨架：%q", good)
		}
		if !IsTranslationUsable(src, good) {
			t.Errorf("正常译文必须判可用：%q", good)
		}
	}
}

// TestCJKEchoSkeletonNotApplicableWithoutCJKSource C 腿：源文无中文 ⇒ 整条判据不适用。
func TestCJKEchoSkeletonNotApplicableWithoutCJKSource(t *testing.T) {
	for _, s := range []string{
		"0", "0.00", "¥1003.00", "09-17 09:40:33", "002584.SZ", "N_shape", "manua", "#",
	} {
		// 这些段的正确交付就是它自己（判据 3′）或必须原样（判据 6），本判据不许多管一段。
		if cjkEchoWashedToSkeleton(s, s) {
			t.Errorf("源文无中文时判据必须不适用：src=%q", s)
		}
		if !IsTranslationUsable(s, s) {
			t.Errorf("无中文源回吐应判可用（原文交付），got false: %q", s)
		}
	}
}

// TestCJKEchoSkeletonAllowsNonLatinTargets D 腿：非拉丁书写系统的正常译文必须放行。
// 这条防的是"把 hasLetterRun 写成 [A-Za-z]{2}"——那样中文→俄语/阿拉伯语/泰语的整条腿会被误杀。
func TestCJKEchoSkeletonAllowsNonLatinTargets(t *testing.T) {
	src := "已实现=加权成本重放；浮动=市值-成本×数量；30s刷新"
	for _, good := range []string{
		"Реализовано = воспроизведение взвешенной стоимости; 30 с",
		"تم التحقق = إعادة تشغيل التكلفة المرجحة; 30 ثانية",
		"รับรู้แล้ว = การเล่นต้นทุนถัวเฉลี่ย; 30 วินาที",
		"実現済み = 加重コストの再生; 30秒ごとに更新", // CJK 目标语：汉字/假名本身就是 Letter
	} {
		if cjkEchoWashedToSkeleton(src, good) {
			t.Errorf("非拉丁目标语的正常译文被误判成骨架：%q", good)
		}
	}
}

// TestCJKEchoSkeletonTailResidueNotCovered 已知未覆盖形态：如实钉住边界。
// 交付 md 第 39 行那种"整行正常、尾部挂一串全角骨架"的形态**不**由本判据处理。
// 这条测试的作用是防止后人把"尾部骨架"当成已修——它绿着，恰恰说明这里没管它。
func TestCJKEchoSkeletonTailResidueNotCovered(t *testing.T) {
	src := "当前：全部允许、仓位留空/0 = 使用全局单票金额 ；\"0\" 。"
	out := "Current: All allowed; Position left blank/0 = Use the global single ticket amount ：；\"\"0，。"
	if cjkEchoWashedToSkeleton(src, out) {
		t.Errorf("尾部骨架形态不该由本判据命中（它整行有连续字母）；若这里变红说明判据射程被改宽了，需同步改注释与上游 stripTrailingCJKNotes 的分工")
	}
}
