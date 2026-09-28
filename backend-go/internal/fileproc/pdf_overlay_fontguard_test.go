package fileproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============ pdf_overlay_fontguard_test.go · 职责说明 ============
// 守护 §12-D5（2026-09-28）兜底字体取用的**两级策略**：
//   ① 有随包资产 → 直接用；
//   ② 没有 → 找系统里一支免费可商用的替身顶上，件照样交，只打一行带标记的提示；
//   ③ 一个都找不到 → 才算真失败，且必须发生在任何昂贵动作之前。
//
// 为什么本文件存在（三条）：
//  1. 这条链的病死形态是**降级链把它完全兜住**——客户照样拿到文件、工单照样成功、账照样扣，
//     只有日志里能看出端倪，所以「看产物 / 看工单状态」永远验不出它；
//  2. 「取字／失败」必须发生在 pymupdf.open 与 `_blank_faint_images`（把每页图片读进内存，
//     全链最贵的一段）**之前**，以及 `pymupdf.Font(...)` 构造之前——原崩溃点正好在那之后，
//     结果是「内存花完、产物照样降级」，我们在为一次注定失败的转换付满内存。
//
// 判据（每条都配了正反对照，避免恒真的假绿锁）：
//  · 一个字体都没有 ⇒ 非 0 退出 + FALLBACK_FONT_MISSING_MARK，且不得提到输入文件名；
//  · 有替身 ⇒ **不得**报 missing，必须报 substituted 且给出自行下载的提示；
//  · 资产在位 ⇒ 两个标记都不能出现（否则前两条会退化成恒真）；
//  · 取字/判失败必须发生在 pymupdf.open 与 `_blank_faint_images` 之前——这条用「输入文件根本
//    不存在」来证，不依赖 pymupdf，任何有 python3 的机器都实跑；
//  · 唯一例外是「字体存在但 PyMuPDF 打不开」那条（TestOverlayApplyRejectsBrokenFontBeforeOpen）：
//    判断"PyMuPDF 认不认"只能真去构造 Font，所以该用例显式要求 pymupdf（CI 在 go test 前已装）。
// ================================================================

const (
	// overlayFontMissingMark / overlayFontSubstitutedMark / overlayFontUnusableMark
	// 必须与 pdf_overlay.py 的同名常量同值。
	// 这里不 import Python 常量（跨语言拿不到），改用「两侧都对同一字面量」的等值锁写法：
	// 一旦某一侧改名而另一侧没跟，本文件的断言立即红灯，不会退化成恒真的假绿锁。
	overlayFontMissingMark     = "[fpoverlay] fallback_font_missing"
	overlayFontSubstitutedMark = "[fpoverlay] fallback_font_substituted"
	overlayFontUnusableMark    = "[fpoverlay] fallback_font_unusable"

	// overlaySubstituteHint 替身提示里必须出现的关键词（用户口径：要提示可以自行下载更换）。
	overlaySubstituteHint = "自行下载"
	// overlayFontCandidatesEnv 仅供本组用例把搜索范围锁死，生产不设。
	overlayFontCandidatesEnv = "FPD_FONT_CANDIDATES"
)

// copyOverlayScriptTo 把仓库里的 pdf_overlay.py 单独拷到 dst（**不带 assets/ 资产树**），
// 复刻的就是现网形态：脚本在、随包字体不在。这是 D5 的根因，也是本文件的夹具核心。
func copyOverlayScriptTo(t *testing.T, dst string) {
	t.Helper()
	src, err := os.ReadFile("pdf_overlay.py")
	if err != nil {
		t.Fatalf("读 pdf_overlay.py 失败（请在 internal/fileproc 目录下执行 go test）: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "pdf_overlay.py"), src, 0o755); err != nil {
		t.Fatalf("写临时脚本失败: %v", err)
	}
}

// runOverlayApply 在指定目录下以最小 payload 跑一次 apply。返回 (stderr+stdout, 退出码)。
// 入参 inPath 可以是**根本不存在**的路径：这正是本文件用来证明「失败/取字发生在 open 之前」的手段。
// fontSearch 非空时写入 FPD_FONT_CANDIDATES，把替身搜索范围锁死（否则结果会随机器的实际装字体漂移）。
func runOverlayApply(t *testing.T, dir, inPath, fontSearch string) (string, int) {
	t.Helper()
	py := findPython()
	if py == "" {
		// ★ 严禁写成 t.Skip：本用例必须实跑。python3 缺席时不跳过而是直接判红——
		//   「环境不满足 → skip」在 CI 里等于把红灯洗成绿，本仓已有多次该类教训。
		t.Fatal("findPython() 返回空：本机没有可用 python3。本用例在所有交付环境都应实跑，" +
			"不允许以 skip 方式通过（缺解释器的机器应先把环境补齐，而不是把闸门调暗）")
	}
	out := filepath.Join(t.TempDir(), "out.pdf")
	cmd := exec.Command(py, "pdf_overlay.py", "apply", inPath, out, "zh")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), overlayFontCandidatesEnv+"="+fontSearch)
	cmd.Stdin = strings.NewReader(`{"translations":{}}`)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && cmd.ProcessState == nil {
		t.Fatalf("python3 进程未能启动: %v", err)
	}
	return stderr.String(), cmd.ProcessState.ExitCode()
}

// TestOverlayApplyFailsFastWhenNoFontAtAll ★ 第三级：机器上**连一支可用字体都没有**
// 才算真失败，且必须失败在任何昂贵动作之前、并打出可检索标记 + 自行下载的提示。
func TestOverlayApplyFailsFastWhenNoFontAtAll(t *testing.T) {
	dir := t.TempDir()
	copyOverlayScriptTo(t, dir)

	// 输入 PDF 故意不存在：若取字/check 没排在 open 之前，这里冒出来的会是「找不到输入文件」，
	// 而不是字体标记——用不存在的路径把「失败发生在多早」变成可断言的事实。
	missing := filepath.Join(t.TempDir(), "no-such-input.pdf")

	// 搜索范围锁死成一个不存在的路径 ⇒ 确定性地造出「本机零可用字体」，
	// 否则这条用例会随着机器实际装了哪些字体而漂移（CI 镜像各不相同）。
	stderr, code := runOverlayApply(t, dir, missing, filepath.Join(t.TempDir(), "none.ttf"))
	if code == 0 {
		t.Fatalf("零可用字体时 apply 竟然返回 0（这条一度是死的成功路径）：stderr=%q", stderr)
	}
	if !strings.Contains(stderr, overlayFontMissingMark) {
		t.Fatalf("零可用字体时未打出可检索标记 %q，排障时仍是黑盒：code=%d stderr=%q",
			overlayFontMissingMark, code, stderr)
	}
	if strings.Contains(stderr, filepath.Base(missing)) {
		t.Fatalf("stderr 提到了输入文件名 ⇒ 脚本在判字体之前就走到了 open/水印扫描，"+
			"那份内存照样白付了：stderr=%q", stderr)
	}
	if !strings.Contains(stderr, overlaySubstituteHint) {
		t.Fatalf("失败信息里没有提示用户自行下载字体（合理降级要给得出路）：stderr=%q", stderr)
	}
}

// TestOverlayApplySubstitutesMissingFont ★ 用户对这条容错的原话：
// 「缺了就把用户卡死不合适，应该降级为其他可免费商用字体，并提示用户自己下载后更换」。
// 本用例锁的就是这句话：缺随包资产 + 有替身 ⇒ 不许报缺失，必须报 substituted 并给提示。
func TestOverlayApplySubstitutesMissingFont(t *testing.T) {
	bundled := filepath.Join("assets", "fonts", "DroidSansFallbackFull.ttf")
	abs, err := filepath.Abs(bundled)
	if err != nil || abs == "" {
		t.Fatalf("取不到仓库内置字体的绝对路径（%v）：这条用例失去意义", err)
	}
	dir := t.TempDir()
	copyOverlayScriptTo(t, dir) // 目录里没有 assets/ ⇒ 复刻"随包资产没落盘"的形态

	missing := filepath.Join(t.TempDir(), "no-such-input.pdf")
	stderr, _ := runOverlayApply(t, dir, missing, abs)

	if strings.Contains(stderr, overlayFontMissingMark) {
		t.Fatalf("有替身可用却仍报「无字体」⇒ 等于把部署瑕疵转嫁成客户失败：stderr=%q", stderr)
	}
	if !strings.Contains(stderr, overlayFontSubstitutedMark) {
		t.Fatalf("用了替身却没有打出 %q，事后无从知道那批件不是默认字形：stderr=%q",
			overlayFontSubstitutedMark, stderr)
	}
	if !strings.Contains(stderr, overlaySubstituteHint) {
		t.Fatalf("用了替身却没给出「自行下载替换」的提示：stderr=%q", stderr)
	}
}

// TestOverlayApplyRejectsBrokenFontBeforeOpen ★ 最后一道闸：字体文件**魔数像 TTF
// 但 PyMuPDF 实际打不开**（下载截断、被覆盖成别的东西）时，必须在 pymupdf.open 之前
// 失败并打 unusable 标记——否则又回到"内存花完才知道字不能用"的老坑。
//
// ⚠️ 这一条必须有真 pymupdf 才跑得动（本机没有就 t.Fatal，**不静默跳过**）：
//
//	判断"PyMuPDF 认不认"这件事，除了真的去构造 Font 之外没有第二条路。
//	CI 的 backend job 在 go test 之前那一步已经 `pip install pymupdf`，满足前提。
func TestOverlayApplyRejectsBrokenFontBeforeOpen(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机没有可用 python3，本用例无法实跑")
	}
	if !checkPythonModule(py, "pymupdf") {
		t.Fatal("本用例需要 pymupdf（CI 在 go test 之前那一步已经 pip install；" +
			"本机手动跑请先装，或用 FILEPROC_PYTHON_BIN 指向装好的解释器）")
	}
	// 造一个"魔数像 TrueType，内容却是垃圾"的文件：_usable_font 会放行，PyMuPDF 才会拒绝。
	broken := filepath.Join(t.TempDir(), "broken.ttf")
	junk := make([]byte, 64)
	for i := range junk {
		junk[i] = byte(i % 251)
	}
	if err := os.WriteFile(broken, append([]byte{0x00, 0x01, 0x00, 0x00}, junk...), 0o644); err != nil {
		t.Fatalf("造损坏字体夹具失败: %v", err)
	}
	dir := t.TempDir()
	copyOverlayScriptTo(t, dir)
	missing := filepath.Join(t.TempDir(), "no-such-input.pdf")

	stderr, code := runOverlayApply(t, dir, missing, broken)
	if code == 0 {
		t.Fatalf("字体打不开时 apply 竟然返回 0：stderr=%q", stderr)
	}
	if !strings.Contains(stderr, overlayFontUnusableMark) {
		t.Fatalf("字体打不开时未打出 %q，排障时仍是黑盒：code=%d stderr=%q",
			overlayFontUnusableMark, code, stderr)
	}
	if strings.Contains(stderr, filepath.Base(missing)) {
		t.Fatalf("stderr 提到了输入文件名 ⇒ 脚本先走到了 open/水印扫描，那份内存照样白付了：stderr=%q", stderr)
	}
	if !strings.Contains(stderr, overlaySubstituteHint) {
		t.Fatalf("失败信息里没有提示用户自行下载字体（降级要给得出路）：stderr=%q", stderr)
	}
}

// TestOverlayApplyAssetPresentNoFontWarning ★ 上面两条的正向对照（AGENTS §一·5：
// 负向锁必须配正向对照，否则改坏代码也不红）：资产在位时，两个标记都不能出现。
func TestOverlayApplyAssetPresentNoFontWarning(t *testing.T) {
	if _, err := os.Stat(filepath.Join("assets", "fonts", "DroidSansFallbackFull.ttf")); err != nil {
		t.Fatalf("仓库缺少 assets/fonts/DroidSansFallbackFull.ttf：这条对照用例失去意义（%v）", err)
	}
	missing := filepath.Join(t.TempDir(), "no-such-input.pdf")
	stderr, _ := runOverlayApply(t, ".", missing, "")
	if strings.Contains(stderr, overlayFontMissingMark) || strings.Contains(stderr, overlayFontSubstitutedMark) {
		t.Fatalf("仓库内 assets/fonts 在位却仍报字体问题 ⇒ _pick_apply_font 写错，"+
			"上面两条会退化成恒真：stderr=%q", stderr)
	}
}
