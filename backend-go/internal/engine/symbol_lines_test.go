// ============ symbol_lines_test.go · 职责说明 ============
// `symbol_lines.go` 三个判据的边界锁（★ 2026-10-21 UAT 收口 M9）。
//
// 为什么需要：M9 的缺陷形态是「上下箭头被 OCR 读成 `1`/`<`，回填脚本又把它画回图标上」。
// 修法把「这一行是不是文字」收成三档（图形/内容/无字母），三档各有一个消费者：
//
//	IsGraphicOnlyLine  → 回填时**连画都不画**（原图标逐像素保住，计进 dropped）；
//	IsContentOnlyLine  → 金额/日期行要原样保留（可重画为同样的字，但不是"图形"）；
//	IsSymbolOnlyLine   → 送翻与交付共用的主判据（没有任何书写体系的字母）。
//
// 判据取向是**结构**而非名单，所以本文件刻意用「OCR 真实折出来的形态」当夹具：
// `<`/`1`/`^`/`~`/`¥1003.00`/`09-17 09:40:33`/`⇅`/`→→`，以及**必须不被误判**的真文字
// （`D`/`Hello`/`の`/`产品说明书`）。少一条射程腿，「不画」就可能变成「漏译」。
//
// 反证：本文件末尾用两份**故意写错的实现**喂同一张夹具表，要求它们与真判据至少各红一处——
// 证明这些夹具真的能区分"对的判据"与"看起来像对的判据"，而不是一组恒真的断言。
// =============================================
package engine

import "testing"

// TestIsSymbolOnlyLineBoundaries 主判据：无字母行＝真，任何书写体系的字母＝假。
func TestIsSymbolOnlyLineBoundaries(t *testing.T) {
	yes := []string{
		"<", "1", "^", "~", "|", "/", "\\", "+", "-", "…", "·",
		"¥1003.00", "-11312.00", "09-17 09:40:33", "100%", "$1,200", "1/2",
		"⇅", "→→", "★", "①②", // ①②虽不在图形区间，但同样没有字母
	}
	for _, s := range yes {
		if !IsSymbolOnlyLine(s) {
			t.Errorf("IsSymbolOnlyLine(%q) 应为真（无字母行＝不是文字）", s)
		}
	}
	no := []string{
		"", " ", "\t\n",
		"D", "Hello", "manual", "e.g.",
		"の", "はの", "产品说明书", "Привет", "مرحبا", "สวัสดี", "안녕",
		"1号线", "<hello>", "¥1003.00元",
	}
	for _, s := range no {
		if IsSymbolOnlyLine(s) {
			t.Errorf("IsSymbolOnlyLine(%q) 应为假（含字母的行是文字，跳过＝漏译）", s)
		}
	}
}

// TestIsGraphicOnlyLineBoundaries 图形档：只有图形符号（含 emoji 区间与连接件）才为真。
func TestIsGraphicOnlyLineBoundaries(t *testing.T) {
	yes := []string{"⇅", "→", "→→", "▲", "★", "✓", "✘", "®", "•", "—", "😀", "⭐️", "👨‍👩‍👧"}
	for _, s := range yes {
		if !IsGraphicOnlyLine(s) {
			t.Errorf("IsGraphicOnlyLine(%q) 应为真（图形行整行都不该重画）", s)
		}
	}
	// 关键负向：`<`/`1` 是 OCR 折出来的**普通字符**，不是图形——它们走「原文交付不重画」那一档，
	// 若这里判真，dropped 会虚高、gateWarnings 会替客户误报「剔除了图标」。
	no := []string{"", " ", "<", "1", "^", "~", "¥1003.00", "Hello", "⇅a", "→1"}
	for _, s := range no {
		if IsGraphicOnlyLine(s) {
			t.Errorf("IsGraphicOnlyLine(%q) 应为假（普通字符/混合行不属图形档）", s)
		}
	}
}

// TestIsContentOnlyLineBoundaries 内容档：数字＋金额/日期标点。
func TestIsContentOnlyLineBoundaries(t *testing.T) {
	yes := []string{"1", "¥1003.00", "-11312.00", "09-17 09:40:33", "100%", "$1,200", "(42)", "【3】"}
	for _, s := range yes {
		if !IsContentOnlyLine(s) {
			t.Errorf("IsContentOnlyLine(%q) 应为真（金额/日期/编号行要原样保留）", s)
		}
	}
	no := []string{"", " ", "<", "⇅", "1号线", "Hello", "¥1003.00元", "12kg"}
	for _, s := range no {
		if IsContentOnlyLine(s) {
			t.Errorf("IsContentOnlyLine(%q) 应为假（含字母或图形符号不属内容档）", s)
		}
	}
}

// TestSymbolOnlyLineFeedsIdentityDelivery 接线锁：主判据必须真的挡在 segmentNeedsTranslation 前面。
// 只测 `IsSymbolOnlyLine` 本身，证明不了「送翻那一跳真用它」；这里从**交付面**反查同一张表：
// 无字母行必须判 NeedsIdentityDelivery（＝文件主链预填、即时翻译不送模型）。
func TestSymbolOnlyLineFeedsIdentityDelivery(t *testing.T) {
	for _, s := range []string{"<", "1", "⇅", "¥1003.00", "09-17 09:40:33"} {
		if !NeedsIdentityDelivery(s) {
			t.Errorf("%q 是符号行，交付形态必须就是原文（NeedsIdentityDelivery＝真）", s)
		}
	}
	for _, s := range []string{"Hello", "产品说明书", "の"} {
		if NeedsIdentityDelivery(s) {
			t.Errorf("%q 是文字，必须送翻（NeedsIdentityDelivery＝假）", s)
		}
	}
	// ★ `D` 这一档刻意与「符号」分开：它是**字母**（IsSymbolOnlyLine＝假），但按 2026-10-17
	// 就有的「单字母/短 ASCII 段」规则仍属「交付原文」。两档判据各管各的，M9 没有改它的行为——
	// 若这里也判"必须送翻"，等于顺手改了另一条链的既有口径（且会让英文 `I`/`a` 都去烧一次调用）。
	if IsSymbolOnlyLine("D") {
		t.Fatalf("`D` 是字母，不能算符号行（否则它会被「图形/符号」那两档摘走）")
	}
	if !NeedsIdentityDelivery("D") {
		t.Fatalf("`D` 的交付形态仍是原文（单字母规则），本批不改这一档")
	}
}

// TestSymbolPredicatesHaveDiscriminatingPower 反证（射程自证）：
// 两份「看起来像对」的错误实现，必须在同一张夹具表上判错；
// 若它们也全绿，说明夹具只是把真判据的同义反复抄了一遍，锁没有区分力。
func TestSymbolPredicatesHaveDiscriminatingPower(t *testing.T) {
	// 错误实现 A：只认「传统标点/数字」当无字母——漏掉 OCR 折出来的 `<`/`^` 与箭头。
	onlyClassic := func(s string) bool {
		has := false
		for _, r := range s {
			if r == ' ' {
				continue
			}
			if (r >= '0' && r <= '9') || r == '.' || r == '-' || r == ',' || r == '¥' || r == '%' {
				has = true
				continue
			}
			return false
		}
		return has
	}
	// 错误实现 B：只要「没有一个 ASCII 字母」就算符号——会把 `の`/中文当成符号漏译。
	noASCII := func(s string) bool {
		has := false
		for _, r := range s {
			if r == ' ' {
				continue
			}
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				return false
			}
			has = true
		}
		return has
	}
	cases := []string{"<", "^", "⇅", "¥1003.00", "の", "产品说明书"}
	var aWrong, bWrong int
	for _, s := range cases {
		want := IsSymbolOnlyLine(s)
		if onlyClassic(s) != want {
			aWrong++
		}
		if noASCII(s) != want {
			bWrong++
		}
	}
	if aWrong == 0 {
		t.Fatalf("反证 A 失效：只认传统标点的错误实现与真判据在夹具上完全一致 ⇒ 夹具没有射程")
	}
	if bWrong == 0 {
		t.Fatalf("反证 B 失效：把非 ASCII 字母误判成符号的实现没被任何夹具抓到 ⇒ 「不漏译」那一腿是空的")
	}
}
