// ============ capability_shape_gate_test.go · 职责说明 ============
// ★ 2026-10-15：把 OCR/ASR 两条能力腿的「形状契约」接进自动化闸门。
//
// 为什么既有闸门抓不到这一族（同 §一·12「有自动降级的链路，产物能打开不算验收」）：
//
//	`/api/health` 的 `ocr_ready/asr_ready` 由 `CheckHealth()` 探测，判据只有两件事——
//	「Python 依赖装了没」＋「权重落盘没」。本批修的六个缺陷**没有一个是这两件事**：
//	  · CTC blank 记在末位（正确＝类 0）⇒ 检测框照常出、字数对、置信度 0.94，只有每个字都错开一格；
//	  · 检测输入没对齐 32 的倍数 ⇒ onnxruntime 在内核执行期抛 Shape mismatch，
//	    被 cmd_image 的兜底 except 收成「图片识别过程出错」，根因指不到尺寸；
//	  · 缺 unclip 膨胀 ⇒ 40px 行高被裁成 20px，「翻译工作台 2026 测试」识别成「秘圣作台测订」；
//	  · cls 档位写死 48x192 ⇒ 换上 v5 的 80x160 权重整单 OCR 失败；
//	  · 归一化没做 BGR 翻转 ⇒ 把红通道当蓝通道，掉哪几个框不可预测、日志零行；
//	  · ASR 侧 `sherpa_onnx.read_wave` 在现网装的 **1.13.8 里根本不存在**（只有 write_wave），
//	    而 probe 回 OK、`asr_ready=true`、健康面全绿，只有真转一单才暴露。
//	⇒ 全部是**运行时形状错**，源码 grep 得到字样也判不出对错，只能「喂进去、看吐出来」。
//
// 三层设计（对齐仓内既有口径）：
//
//	① 行为级判据抽在 `capability_shape_selftest.py`（假 session＋合成 score map＋手搓 PCM，
//	   零模型权重、零网络、零 GPU，因此 CI 与本机都能跑）；本文件是它的**接线与射程**。
//	② 射程自证＝**逐腿变异**：每条腿各配一枚把真实实现改坏的副本（喂给同一个自证入口），
//	   要求「那一号腿必须红」。AGENTS §三「判据级断言代替不了守卫级」在这里的形态是：
//	   光看这些行 `LEG Lxx OK` 不能证明这些腿有射程——摘掉一条腿的实现而全绿，才是本锁要抓的事。
//	③ 全集锁：Go 侧腿清单与自证脚本里的 `@leg("Lxx")` 注册**双向等值**，
//	   删腿（那行 LEG 消失）与加腿不登记（清单不等值）都当场红。
//
// ⚠️ 本文件**不 skip**：没有 python 或缺 numpy/PIL 的机器判红并点名 `FILEPROC_PYTHON_BIN`
//
//	（口径同 capabilities_p0_test.go：探测若在这类机器上失去验证，等于承认「健康检查可以是空转」）。
//	现网那一层的真件取证不在这里，由 `cmd/capability-smoke` 逐件实跑负责。
//
// ================================================================
package fileproc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// 自证脚本与被测脚本的相对路径（go test 的 cwd＝包目录，同 capabilities_p0_test.go 的口径）。
const (
	capShapeSelftestFile = "capability_shape_selftest.py"
	capShapeOcrFile      = "ocr_recognize.py"
	capShapeAsrFile      = "asr_transcribe.py"
	capShapeImgFile      = "ocr_translate_image.py" // ★ 2026-10-17 批：图片原位回填的绘制壳
)

// capShapeLegs 是腿的**全集**清单，与自证脚本里的 @leg("Lxx") 注册双向等值（Test ③）。
var capShapeLegs = []string{
	"L01", "L02", "L03", "L04", "L05", "L06", "L07",
	"L08", "L09", "L10", "L11", "L12", "L13",
	"L14", "L15", "L16", "L17", "L18",
	"L19", "L20", "L21", "L22", "L23", "L24",
	"L26", "L27",
	"L28", "L29", "L30", "L31", "L32",
	"L33", "L34", "L35", "L36",
	"L37", "L38", "L39",
}

// capShapeMutation 一枚变异：把某个真实脚本的一处实现改坏，要求指定那条腿判红。
type capShapeMutation struct {
	leg      string // 必须变红的那号腿
	file     string // 被改的脚本
	old      string // 原样（必须在文件里**恰好一处**，否则「靶子写漂」当场判死）
	newValue string // 变异后的形态
	why      string // 这一枚对应本批哪一条真缺陷（失败信息里要能读出病因）
}

// capShapeMutations 逐腿一枚（L01~L13 各一枚；L14~L18 见下方 ★ 2026-10-17 批）——
// **每条腿都有且只有自己的实现被改坏时会红**。
// 锚点全部取自仓库真文本（改名式破坏必须当场红，见 Test ② 的 src.count==1 判据）。
var capShapeMutations = []capShapeMutation{
	{"L01", capShapeOcrFile, `words = [""] + lines`, `words = lines + [""]`,
		"blank 记回末位（本批第一条：换上任何官方权重后每个字错开一格）"},
	{"L02", capShapeOcrFile, "    while len(words) < n_classes:\n        words.append(\" \")\n    return words",
		"    if len(words) > n_classes:\n        words = words[:n_classes]\n    return words",
		"补齐改成截断（类别数比词典长时末类永远取不到字符）"},
	{"L03", capShapeOcrFile, `if p != 0 and p != prev and p < len(words):`,
		`if p != len(words) - 1 and p != prev and p < len(words):`,
		"解码按「blank 在末位」跳（与 L01 同族，但那一半在解码侧）"},
	{"L04", capShapeOcrFile, `int(round(w * scale / DET_STRIDE)) * DET_STRIDE`, `int(round(w * scale))`,
		"对齐 32 的倍数被抹掉（官方导出的 Resize 节点直接拒收）"},
	{"L05", capShapeOcrFile, `dx = int(round(d))`, `dx = 0`,
		"unclip 膨胀算出来不落地（40px 行高被裁成穿过字腰的窄带）"},
	{"L06", capShapeOcrFile, `sy = oh / float(sh)`, `sy = sx`,
		"纵轴拿横轴比例反算（框整体拉偏、末行掉到图外）"},
	{"L07", capShapeOcrFile, `boxes.sort(key=lambda b: (b[1] // 20, b[0]))`, `boxes.sort(key=lambda b: b[0])`,
		"阅读顺序按左右排（多行文本被排成乱序，译文按错序回填）"},
	{"L08", capShapeOcrFile, `    if bgr:`, `    if bgr and False:`,
		"BGR 翻转被绕过（把红通道当蓝通道，掉框不可预测）"},
	{"L09", capShapeOcrFile, `            return h, w`, `            return CLS_HW_LEGACY`,
		"方向分类档位不再从模型形状读（换 v5 的 80x160 权重整单失败）"},
	{"L10", capShapeAsrFile, `arr = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 32768.0`,
		`arr = np.frombuffer(raw, dtype="<i2").astype(np.float32) / 2147483648.0`,
		"16-bit 按 32-bit 的除数归一（样本量级差 65536 倍，识别成乱码）"},
	{"L11", capShapeAsrFile, `arr = arr.reshape(-1, n_ch)[:, 0]`, `arr = arr.reshape(-1, n_ch)[:, 1]`,
		"多声道取了第二路（无 ffmpeg 直通时的原始 wav 会拿错声道）"},
	{"L12", capShapeAsrFile, `    if samp_w == 2:`, `    if samp_w in (2, 3):`,
		"24-bit 被当 16-bit 硬解（下游静默吃半截样本，比报错更坏）"},
	{"L13", capShapeAsrFile, `reader = getattr(sherpa_module, "read_wave", None)`, `reader = None`,
		"read_wave 优先腿被摘（将来版本又导出它时不再走模型侧同款解析）"},
	// ===== ★ 2026-10-17 批：图片原位回填（L14~L18）。两条真缺陷（框判据按八值、字体缺拉丁字形）
	// 都是「运行期形状错」——健康面全绿、日志零红，只有把译文图打开才看得出。
	{"L14", capShapeImgFile, `            if _valid_box(box):`, `            if len(box) >= 8:`,
		"退回旧判据：四值矩形被当坏行跳过 ⇒ 译文图＝重编码的原图（本批用户报障形态）"},
	{"L15", capShapeImgFile, `return isinstance(box, (list, tuple)) and len(box) >= 4 and len(box) % 2 == 0`,
		`return isinstance(box, (list, tuple)) and len(box) == 4`,
		"只收四值框、把八值四点当坏行（换回多边形检测器时整张图不画）"},
	{"L16", capShapeImgFile, `return isinstance(box, (list, tuple)) and len(box) >= 4 and len(box) % 2 == 0`,
		`return True`,
		"坏行校验被摘（坐标不成对也去算矩形，画出歪框还当成功产物交出去）"},
	{"L17", capShapeImgFile, "        if _has_glyph(f, ch):", "        if True:",
		"逐字挑字体退化成「永远用链首」（随包 CJK 字体画英文字母＝整片豆腐块）"},
	{"L18", capShapeImgFile, "        f = _pick_font(chain, ch)\n        draw.text((x, y), ch, font=f, fill=fill)",
		"        f = chain[0]\n        draw.text((x, y), ch, font=f, fill=fill)",
		"绘制不再逐字挑字体（挑字函数写对却没人调用＝白修）"},
	// ===== ★ 2026-10-19 批：图片保真（G3-a 字号/底色/扩框、G3-b 底色与文字色）＋ ASR 无语音门（G11）。
	// 这四条同样是「运行期形状错」——健康面全绿、日志零红，只有把译后图/录音真跑一次才看得出。
	{"L19", capShapeImgFile, `FONT_FLOOR_RATIO = 0.6`, `FONT_FLOOR_RATIO = 0.0`,
		"字号地板被抹平（长译文从框高一路缩到 8px 蚂蚁字）"},
	{"L20", capShapeImgFile, `    ink[:, full_cols] = False`, `    pass`,
		"全高墨色列不剔除（框线让原行高被读成满框高，地板随之抬错）"},
	{"L21", capShapeImgFile, "    lum = _luma_of(bg)\n    return (0, 0, 0) if lum >= 128 else (255, 255, 255)",
		"    return (0, 0, 0)",
		"兜底文字色恒黑（深色底/彩色卡片上字看不见——注：正常路径先走 L37~L39 的字色还原）"},
	{"L22", capShapeImgFile, `        return tuple(int(round(v)) for v in np.median(ring, axis=0))`,
		`        return (128, 128, 128)`,
		"抹字底色恒灰（彩色底/深色底被抹出一块突兀的灰）"},
	{"L23", capShapeImgFile, `    grew = 0`, `    grew = max_px`,
		"扩框被摘（长译文不再争取空白区，只能靠缩字兜底）"},
	{"L24", capShapeAsrFile, `    if rms < NO_SPEECH_RMS and peak < NO_SPEECH_PEAK:`, `    if True:`,
		"无语音门退化成「一律判无语音」（小声真人语音被误伤＝客户说话被判没听到）"},
	// ===== ★ 2026-10-19（下午·现网图片单）批：框内满宽横线剔除（L26）＋ 空框不造字（L27）。
	// 两处都是「运行期形状错」：健康面全绿、日志零红，只有把译后图打开才看得出
	// （前者＝字号忽大忽小，后者＝原文里根本不存在的 `0`/`4` 幽灵内容）。
	{"L26", capShapeImgFile, `        ink[full_rows, :] = False`, `        pass`,
		"满宽墨色行不剔除（输入框上下边让原行高被读成满框高，短内容被画成整格巨字）"},
	{"L27", capShapeImgFile, `        no_ink_identity = ink_h == 0 and text == (ln.get("text") or "").strip()`, `        no_ink_identity = False`,
		"空框 + 原文交付不再跳过（OCR 在图标/边框上误切的空框被画成幽灵内容）"},
	// ===== ★ 2026-10-19（缺陷②：字号忽大忽小 / 字被裁半截）批。三条真因各一枚，外加两条形态腿。
	// 全部是「运行期形状错」：健康面全绿、日志零红，只有把译后图打开（或逐行读 trace）才看得出。
	{"L28", capShapeImgFile, `    ink[:, _thin_rule_cols(ink, full_cols)] = False`, `    pass`,
		"细长色条不剔除（`因子战法` 框里那条 6px 色条把 ink_h 从 25 抬到 44，同级标题字号 32 vs 20）"},
	{"L29", capShapeImgFile, `RULE_MAX_COLS = 8`, `RULE_MAX_COLS = 32`,
		"宽度上限放太宽（`の` 左弧那样的 13 列宽笔画被当色条剔空，字号退到兜底档）"},
	{"L30", capShapeImgFile, `        block_h = _block_ink_h(chain, lines, px, int(px * LINE_SPACING))`,
		`        block_h = int(px * LINE_SPACING) * (len(lines) - 1) + px`,
		"块高退回名义式（漏算下伸部与字体留白 ⇒ 虚报放得下，越框那半行被邻行底色抹掉）"},
	{"L31", capShapeImgFile, `        patch = ref_img.crop(r0)`, `        patch = img.crop(r0)`,
		"量墨迹改回就地画布（上一行画下的字落进下一行框内 ⇒ 字号忽大忽小，现网 fac_1：19px→36px）"},
	{"L32", capShapeImgFile, `                    img.paste(fill, (left, top), tile)`, `                    img.paste(fill, (left, top), mask)`,
		"贴回不再按框裁剪（越框墨迹落进邻行地盘，随后被邻行底色抹成半截字）"},
	// ===== ★ 2026-10-19（缺陷②尾项：外观）批。四条同样是「运行期形状错」：健康面全绿、
	// 日志零红，只有把译后图打开（或逐行读 trace）才看得出——蓝徽章被填成白块、
	// `Yuan/time` 被劈成 `Yuan/t`+`ime`、绿色开关被 OCR 的 `の` 涂改。
	{"L33", capShapeImgFile, `        if counts[k] / float(inside.shape[0]) >= BG_INNER_PURE_MIN:`, `        if False:`,
		"抹字底色不再先认框内纯色块（蓝徽章/胶囊里那块蓝底被环带的白填掉）"},
	{"L34", capShapeImgFile, `        if ch in SOFT_BREAK_AFTER:`, `        if False:`,
		"超长 token 不再在分隔符后断（`Yuan/time` 又被按字符劈成两截）"},
	{"L35", capShapeImgFile, `        if fits and (not hard or px <= floor):`, `        if fits:`,
		"布局把硬切当首选（有「不劈词也放得下」的字号也不再降字号 ⇒ `Synchronized` 被劈成两行）"},
	{"L36", capShapeImgFile, `        noise_identity = _is_lone_nonascii(ln.get("text") or "") and text == (ln.get("text") or "").strip()`,
		`        noise_identity = False`,
		"单字噪声行被重画（OCR 把开关读成 `の` ⇒ 旧形态整块涂色再画大白字，圆钮消失）"},
	// ===== ★ 2026-10-19 用户令批：译后图必须用**原背景色 + 原字体色**（L37~L39）。
	// 同样是「运行期形状错」：健康面全绿、日志零红，只有把译后图打开才看得出
	// （旧形态＝蓝徽章上的白字被拍成黑字——用户报的「译文没沿用原来的配色」）。
	{"L37", capShapeImgFile, `        fill = _pick_fg(patch, bg) or _text_color(bg)`, `        fill = _text_color(bg)`,
		"字色又只按底色亮度二选一（浅蓝徽章上的白字被画成黑字，用户报障形态）"},
	{"L38", capShapeImgFile, `    core = ink & (delta >= float(np.percentile(delta[ink], FG_CORE_PCT)))`, `    core = ink`,
		"字色改回整块墨迹取中位（抗锯齿过渡色把深红估成粉、白字估成浅灰）"},
	{"L39", capShapeImgFile, `    if int(ink.sum()) < FG_MIN_INK_PX:`, `    if False:`,
		"墨太少（样本不足）也照样估字色（不可信的读数被当成原字体色用上）"},
}

// capShapeLegLineRe 抓自证脚本的读数行（`LEG L07 OK` / `LEG L07 FAIL <原因>`）。
// 尾巴那段（第三组）必须一起吃进来：`m[0]` 只有匹配到的前缀，用它当排障读数会把病因切掉。
var capShapeLegLineRe = regexp.MustCompile(`(?m)^LEG (L\d{2}) (OK|FAIL)\b(.*)$`)

// capShapePythonBin 取本机 Python：没有即判红，不 skip（同 capabilities_p0_test.go 的口径）。
func capShapePythonBin(t *testing.T) string {
	t.Helper()
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机没有可用 python3，OCR/ASR 形状契约的自证完全跑不到 ⇒ 判红。" +
			"装 python3 或用 FILEPROC_PYTHON_BIN 指到装了 numpy/Pillow 的那个（§二 的补依赖口径，不许放宽判据）")
	}
	return py
}

// runCapShape 用指定 env 覆盖跑一次自证脚本，返回 (stdout, exitCode)。
// env 为 nil 时不加任何覆盖＝仓库真件的正常态；反证只把被测模块指向变异副本。
func runCapShape(t *testing.T, py string, env map[string]string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, capShapeSelftestFile)
	var extra []string
	for k, v := range env {
		extra = append(extra, k+"="+v)
	}
	if len(extra) > 0 {
		cmd.Env = append(os.Environ(), extra...)
	}
	out, err := cmd.Output()
	stdout := string(out)
	// 依赖缺失要**点名的判红**：否则「本机 python 没装 numpy」会被读成「形状契约没被破坏」。
	low := strings.ToLower(stdout)
	if strings.Contains(low, "no module named") {
		t.Fatalf("自证脚本的依赖缺失（%s）⇒ 判红不判通过。用 FILEPROC_PYTHON_BIN 指到装了 numpy/Pillow 的解释器"+
			"（run_uat.sh 已自动发现 ~/.venvs/langcross-fileproc；§二 口径：只许补依赖，不许放宽或跳过用例）\nstdout=%s",
			py, stdout)
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("执行 %s %s 失败：%v（stdout=%s）", py, capShapeSelftestFile, err, stdout)
	}
	return stdout, code
}

// capShapeReadings 把 stdout 拆成「通过的腿」「失败的腿」与每腿首行读数。
func capShapeReadings(stdout string) (pass, fail []string, detail map[string]string) {
	detail = map[string]string{}
	for _, m := range capShapeLegLineRe.FindAllStringSubmatch(stdout, -1) {
		if m[2] == "OK" {
			pass = append(pass, m[1])
		} else {
			fail = append(fail, m[1])
		}
		detail[m[1]] = strings.TrimSpace(m[0])
	}
	return
}

// TestCapabilityShapeSelftestGreen ① 真件必须**全部腿**（现 35 条）全绿，且**读数形状本身合规**：
// 每腿恰好一行、没有 FAIL、末行 CAPSHAPE_ALL_OK=1。
// 「少一行」也算红——那是腿被删掉的形态，不是「剩下的都过了」。
func TestCapabilityShapeSelftestGreen(t *testing.T) {
	py := capShapePythonBin(t)
	if _, err := os.Stat(capShapeSelftestFile); err != nil {
		t.Fatalf("读不到 %s：%v ⇒ 自证脚本必须随仓库提交，本闸门才有被锁的对象（不许改成 skip）", capShapeSelftestFile, err)
	}
	stdout, code := runCapShape(t, py, nil)
	pass, fail, _ := capShapeReadings(stdout)
	if code != 0 || len(fail) > 0 {
		t.Fatalf("形状契约自证未全绿：exit=%d failed=%v\n%s", code, fail, stdout)
	}
	if len(pass) != len(capShapeLegs) {
		t.Fatalf("腿的读数行数=%d，期望=%d（%v）⇒ 有腿被删或重复登记\n%s",
			len(pass), len(capShapeLegs), capShapeLegs, stdout)
	}
	seen := map[string]int{}
	for _, l := range pass {
		seen[l]++
	}
	for _, want := range capShapeLegs {
		switch seen[want] {
		case 0:
			t.Errorf("腿 %s 压根没跑（那一行 LEG 不在读数里）⇒ 这条契约失去射程", want)
		case 1: // 正常
		default:
			t.Errorf("腿 %s 出现 %d 行读数 ⇒ 重复登记，全集计数已不可信", want, seen[want])
		}
	}
	if !strings.Contains(stdout, "CAPSHAPE_ALL_OK=1") {
		t.Errorf("末行缺 CAPSHAPE_ALL_OK=1（判绿只认这一行）：\n%s", stdout)
	}
}

// TestCapabilityShapeLegListsAgree ③ 全集等值锁：Go 侧清单 ↔ 自证脚本的 @leg 注册，双向。
// 动态那一条（Test ①）能抓「删腿」，但抓不到「新加一条腿而没进 Go 清单」——
// 那种形态下自证照样全绿，只是没人知道多出来的腿存在。
func TestCapabilityShapeLegListsAgree(t *testing.T) {
	src, err := os.ReadFile(capShapeSelftestFile)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", capShapeSelftestFile, err)
	}
	// 锚点带 `^`：只数**真注册**（行首装饰器）。脚本的说明注释里就写着 `@leg("L01")` 这个例子，
	// 不限定行首会被它冒充成第二条实现（本批首跑真红过一次，判据对、锚点少一层）。
	regRe := regexp.MustCompile(`(?m)^@leg\("(L\d{2})"\)`)
	got := map[string]int{}
	for _, m := range regRe.FindAllStringSubmatch(string(src), -1) {
		got[m[1]]++
	}
	for _, want := range capShapeLegs {
		switch got[want] {
		case 1: // 正常
		case 0:
			t.Errorf("Go 清单里的腿 %s 在自证脚本里没有 @leg(%q) 注册 ⇒ 清单与实现脱钩（本锁的靶子已漂）", want, want)
		default:
			t.Errorf("腿 %s 被注册 %d 次 ⇒ 同一号腿两条实现，读数无法归属", want, got[want])
		}
	}
	for l := range got {
		if !strings.Contains(strings.Join(capShapeLegs, ","), l) {
			t.Errorf("自证脚本里有腿 %s 没进 Go 侧 capShapeLegs 清单 ⇒ 新增契约没被闸门点名", l)
		}
	}
}

// linkCapShapeAssets 把仓库的 assets/ 目录链接进变异夹具目录。
// ★ 为什么需要：`ocr_translate_image.py` 用「脚本自身所在目录 / assets/fonts」定位随包字体，
// 而变异夹具是把脚本**拷到临时目录**跑的——不链接就会以「缺字体」判红，
// 那红来自夹具形态、不来自变异（正向对照会当场把它抓出来，本批首跑正是这样）。
// 链接而不是整棵拷贝：fonts 那棵树数 MB，几十枚变异各拷一份纯属浪费。
func linkCapShapeAssets(t *testing.T, dir string) {
	t.Helper()
	abs, err := filepath.Abs("assets")
	if err != nil {
		t.Fatalf("取 assets 绝对路径失败：%v", err)
	}
	if err := os.Symlink(abs, filepath.Join(dir, "assets")); err != nil && !os.IsExist(err) {
		t.Fatalf("链接 assets 到夹具目录失败：%v", err)
	}
}

// TestCapabilityShapeMutationsBreakTheirLegs ② 射程自证：逐腿把真实实现改坏，
// 要求**那一号腿判红**且退出码非零。这是本文件的核心——没有它，那一串绿灯
// 只能证明「脚本跑通了」，证明不了「这些断言真的在守着形状」。
//
// 变异副本落在临时目录、经环境变量指向（同 §三 那条「不许就地改源」：
// 跑 go test 期间写坏仓库真件会污染同包其他用例，且反证与正证读的是同一份文件）。
func TestCapabilityShapeMutationsBreakTheirLegs(t *testing.T) {
	py := capShapePythonBin(t)

	// 正向对照：把两份脚本**原样**拷进临时目录并指向副本，必须仍全绿。
	// 它证明「红来自变异」而不是「红来自拷贝这件事本身」（路径／assets 定位／编码都可能自己把腿打断）。
	control := t.TempDir()
	for _, rel := range []string{capShapeOcrFile, capShapeAsrFile, capShapeImgFile} {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(control, rel), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	linkCapShapeAssets(t, control)
	stdout, code := runCapShape(t, py, map[string]string{
		"CAPSHAPE_OCR_MODULE": filepath.Join(control, capShapeOcrFile),
		"CAPSHAPE_ASR_MODULE": filepath.Join(control, capShapeAsrFile),
		"CAPSHAPE_IMG_MODULE": filepath.Join(control, capShapeImgFile),
	})
	if code != 0 || !strings.Contains(stdout, "CAPSHAPE_ALL_OK=1") {
		t.Fatalf("变异夹具的正向对照不绿（exit=%d）⇒ 拷贝机制自己就在打断腿，反证读数全部不可信：\n%s", code, stdout)
	}

	for _, m := range capShapeMutations {
		m := m
		t.Run(m.leg+" "+m.why, func(t *testing.T) {
			raw, err := os.ReadFile(m.file)
			if err != nil {
				t.Fatalf("读 %s 失败：%v", m.file, err)
			}
			text := string(raw)
			// 锚点必须**恰好一处**：0 处＝靶子写漂（这一枚等于没测），
			// >1 处＝改到了别人的实现，红会落在错误的腿上。
			if n := strings.Count(text, m.old); n != 1 {
				t.Fatalf("%s 里锚点 %q 命中 %d 处（应为 1）⇒ 靶子写漂，这枚反证没有射程", m.file, m.old, n)
			}
			dir := t.TempDir()
			dst := filepath.Join(dir, filepath.Base(m.file))
			if err := os.WriteFile(dst, []byte(strings.Replace(text, m.old, m.newValue, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			linkCapShapeAssets(t, dir)
			env := map[string]string{}
			switch m.file {
			case capShapeOcrFile:
				env["CAPSHAPE_OCR_MODULE"] = dst
				env["CAPSHAPE_ASR_MODULE"] = filepath.Join(control, capShapeAsrFile)
				env["CAPSHAPE_IMG_MODULE"] = filepath.Join(control, capShapeImgFile)
			case capShapeAsrFile:
				env["CAPSHAPE_ASR_MODULE"] = dst
				env["CAPSHAPE_OCR_MODULE"] = filepath.Join(control, capShapeOcrFile)
				env["CAPSHAPE_IMG_MODULE"] = filepath.Join(control, capShapeImgFile)
			case capShapeImgFile:
				env["CAPSHAPE_IMG_MODULE"] = dst
				env["CAPSHAPE_OCR_MODULE"] = filepath.Join(control, capShapeOcrFile)
				env["CAPSHAPE_ASR_MODULE"] = filepath.Join(control, capShapeAsrFile)
			default:
				t.Fatalf("未知被测脚本 %q", m.file)
			}
			out, exitCode := runCapShape(t, py, env)
			_, fail, detail := capShapeReadings(out)
			hit := false
			for _, l := range fail {
				if l == m.leg {
					hit = true
				}
			}
			if exitCode == 0 || !hit {
				t.Fatalf("变异「%s」后腿 %s 没有判红（exit=%d failed=%v）⇒ 这条形状契约是空转锁\n%s",
					m.why, m.leg, exitCode, fail, out)
			}
			// 失败信息必须带上病因文案，否则下一位只能自己 diff。
			t.Logf("射程确认：%s → %s", m.leg, detail[m.leg])
		})
	}
}
