// ============ ocr_image_symbol_gate_test.go · 职责说明 ============
// 图片原位回填的「非语言行」闸门：Go 侧读数解析＋Python 侧接线（★ 2026-10-21 UAT 收口 M9）。
//
// 背景：UAT 截图里上下箭头被 OCR 读成 `1`/`<`，回填脚本把识别结果照原样画回图标上，
// 客户看到「原文根本没有的字盖住了图标」。修法在脚本里落了三档：
//
//	graphic_drop   → 图形符号整行**连画都不画**，原图标逐像素保留，计进 dropped；
//	symbol_identity→ 无字母行且本轮交付原文 ⇒ 不重画，计进 blank_skipped；
//	其余（含字母）→ 照旧绘制（否则"不造字"变成"漏译"）。
//
// 本文件锁两件事：
//
//	① Go 侧 `parseImageDropped` 真能读到脚本自报的读数（读不到＝GateWarnings 永远是 0，
//	   客户以为那一行漏翻了，运维也看不到成因）；
//	② 脚本里的三档接线与 INFO 读数**必须同时在场**——只留判据函数而摘掉调用点，
//	   Python 侧的单测/自证仍会绿，但产品路径已经不再经过它（本仓最典型的"白修"形态）。
//
// 反证：把接线逐条改坏喂给同一个判据函数，必须每条各红一次。
// =============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// TestParseImageDropped ① 读数解析：取第一个 dropped=N；缺失/负数/非数字一律回 0。
// 回 0 是安全默认（调用方只在 >0 时出声），但**不能**把 `dropped=-3` 读成 3 或 panic。
func TestParseImageDropped(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   int
	}{
		{"正常", "INFO:painted=3/9 overflow=0 blank_skipped=5 dropped=2\n", 2},
		{"零", "INFO:painted=9/9 overflow=0 blank_skipped=0 dropped=0\n", 0},
		{"旧脚本无此字段", "INFO:painted=3/9 overflow=0 blank_skipped=5\n", 0},
		{"空串", "", 0},
		{"负数", "INFO:painted=1/2 overflow=0 blank_skipped=1 dropped=-3\n", 0},
		{"非数字", "INFO:painted=1/2 overflow=0 blank_skipped=1 dropped=abc\n", 0},
		{"夹杂其它日志", "WARN:x\nINFO:painted=1/2 overflow=0 blank_skipped=0 dropped=7\n", 7},
	}
	for _, c := range cases {
		if got := parseImageDropped(c.stderr); got != c.want {
			t.Errorf("%s：parseImageDropped(%q)=%d，期望 %d", c.name, c.stderr, got, c.want)
		}
	}
}

// imageSymbolGate 静态判据：脚本必须同时具备三档接线与 dropped 读数。
// 返回第一处缺失（空串＝全部在位）；反证直接喂变异体。
func imageSymbolGate(src string) string {
	body := stripPyBareComments(src)
	for _, want := range []string{
		"graphic_drop = _is_graphic_only_line(src_raw)",
		"symbol_identity = _is_symbol_only_line(src_raw) and text == src_raw.strip()",
		"if graphic_drop or no_ink_identity or lone_noise or symbol_identity:",
		"                dropped += 1",
		`print("INFO:painted=%d/%d overflow=%d blank_skipped=%d dropped=%d"`,
	} {
		if !strings.Contains(body, want) {
			return "缺接线: " + want
		}
	}
	return ""
}

// stripPyBareComments 去掉整行注释（`#` 开头的行）。
// 只按行首判：本文件的说明注释里会提到这些判据名字，整段抄进注释会让静态锁变成
// 「注释冒充实现」（AGENTS §一·5 页脚那条同形）。
func stripPyBareComments(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// TestImageRenderScriptSymbolGateWired ② 真件接线锁＋四枚反证。
func TestImageRenderScriptSymbolGateWired(t *testing.T) {
	raw, err := os.ReadFile("ocr_translate_image.py")
	if err != nil {
		t.Fatalf("读 ocr_translate_image.py 失败: %v（脚本必须随仓库提交）", err)
	}
	src := string(raw)
	if msg := imageSymbolGate(src); msg != "" {
		t.Fatalf("Python 回填门未接线：%s", msg)
	}
	mutations := []struct{ old, new, why string }{
		{"        symbol_identity = _is_symbol_only_line(src_raw) and text == src_raw.strip()",
			"        symbol_identity = False",
			"符号行不再跳过（`<`/`1` 又被画回图标上）"},
		{"        graphic_drop = _is_graphic_only_line(src_raw)",
			"        graphic_drop = False",
			"图形行不再整行剔除（箭头被噪声盖住）"},
		{"                dropped += 1",
			"                dropped += 0",
			"剔除计数恒 0（GateWarnings 永不发声）"},
		{`print("INFO:painted=%d/%d overflow=%d blank_skipped=%d dropped=%d"`,
			`print("INFO:painted=%d/%d overflow=%d blank_skipped=%d"`,
			"INFO 行不再上报 dropped（Go 侧读数永远取不到）"},
	}
	for i, m := range mutations {
		bad := strings.Replace(src, m.old, m.new, 1)
		if bad == src || !strings.Contains(bad, m.new) {
			t.Fatalf("反证 %d 的变异体没生效（no-op 会把自己伪装成「锁抓住了破坏」）", i+1)
		}
		if imageSymbolGate(bad) == "" {
			t.Fatalf("反证 %d 失效：%s 之后判据仍绿", i+1, m.why)
		}
	}
}

// TestImageDroppedReachesGateWarnings ③ 消费面接线：Dropped 必须从 Go 结构体一路走到客户可见的
// GateWarnings（与 Residue 同一档）。只在脚本里数 dropped、Go 侧没人读，等于"排障读数活在一行日志里"。
// 读的是 engine/file.go 源码（跨包只读源码，不起依赖）。
func TestImageDroppedReachesGateWarnings(t *testing.T) {
	raw, err := os.ReadFile("../engine/file.go")
	if err != nil {
		t.Fatalf("读 engine/file.go 失败: %v", err)
	}
	body := stripGoBareComments(string(raw))
	for _, want := range []string{
		"stats.Dropped > 0",
		"图片原位回填有 %d 行图形符号",
		"gateWarnings = append(gateWarnings",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Dropped 没接到 GateWarnings（缺 %q）⇒ 客户看不到「这些行是设计内保留」的成因", want)
		}
	}
}

// stripGoBareComments 去掉 Go 的整行 `//` 注释（判据靶子只许落在代码行上）。
func stripGoBareComments(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}
