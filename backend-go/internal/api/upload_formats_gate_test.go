// ============ 本文件职责中文说明 ============
// 静态＋行为双闸门：钉住 #43「工单建单预检的判据形状收成一把尺子」这件事**真的落地了**，
// 并且**没有顺手改掉任何对外结论**。七条判据腿：
//
//	L1 单源在位：四份旧副本（upload.go / tickets.go / api_openapi_tasks.go / engine/file.go）
//	   里不许再留内联准入清单字面量，且必须各自委托到 fileproc 的那一个口；体积上限同理。
//	L2 行为等值：把准入判定**真跑一遍全表**（12 项还原模式 ＋ anydoc 并集 ＋ 一批明确不准入项），
//	   与改造前四份副本的并集逐格对账——这一腿才是"没翻对外结论"的实质证据。
//	L3 文案同源：四处拒绝文案必须与改造前**逐字**相同（等值锁，不是"措辞相近"），
//	   且样本清单里举出的每一个名字都必须真在准入名单里。
//	L4 跨层现读：Go 的 40 MiB 与 Caddy 那三处 `max_size 40MB` 现读现算比档位（写死数字第二天就过时）。
//	L5 方向锁：对外承诺的 30MB 不得高于实际闸门（承诺不得高过守得住的那条线）。
//	L6 出声对齐：「页数读数取不到」这一档在两腿（上传前置拦截／派发资格判定）都必须是 WARN 且同短语。
//	L7 共享闸门不可被调用方抬起：拿到的 map 必须是副本，往里塞键不许影响全站判定。
//
// 每一条负向腿都配了正对照（本仓纪律：恒零命中的负向锁等于没测）：
// 探测器必须能把**唯一事实源自己**认出来，认不出就是探测器坏了。
// ============================================
package api

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"translator/internal/fileproc"
)

// ---------- 判据本体（正例反例共用同一份，两侧不可能各判各的） ----------

// restore12Frozen 改造前四份副本**逐字相同**的那十二项（冻结在这里当 oracle）。
//
// 为什么把旧清单抄进测试而不是"读 fileproc 再跟 fileproc 比"：
// 后者是恒真的自比，改坏 formats.go 里任何一项都不会红。这一份是**改造前的事实快照**，
// 它红了只有两种解释：要么准入面真的变了（本批明令禁止），要么有人有意改档并来改这条锁——
// 那正是我们要逼出来的一次显式决策。
//
// ★ 2026-10-15 P1a/P2 扩展：尾部追加 8 个图片格式（.png/.jpg/.jpeg/.bmp/.tif/.tiff/.webp/.heic）
// 与 7 个音频格式（.mp3/.m4a/.wav/.ogg/.flac/.aac/.opus），与 fileproc/formats.go 的
// TranslateFormatOrder 现态逐字一致（共 27 项）。这一改动本身是一次**显式对外准入决策**
// （图片经 OCR、音频经 ASR 投喂翻译引擎），由本锁逼出并记录，非静默漂移。
var restore12Frozen = []string{
	".docx", ".xlsx", ".pptx", ".pdf",
	".txt", ".csv", ".srt", ".vtt",
	".md", ".json", ".yaml", ".yml",
	".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic",
	".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus",
}

// formatCopyViolation 跑 L1 那两条：内联清单为 0 ＋ 必须留委托锚点。返回第一条问题描述（干净回空串）。
//
// 内联清单的两种形状（与前端 translateFormats.lock.test.ts 同一判据，两处刻意同写法）：
//
//	① 同一行并列出现 ≥3 个「Office/PDF 那四件」；
//	② `".docx": true` 这种 map 条目形状。
//
// 为什么不按"这个文件里出现过 .docx"判：engine/file.go 的写回格式 switch 与交付形态分档
// 都是合法的逐格式代码，按"出现过"判会把它们误伤成副本——判红一次，下一位就把这条锁摘掉了。
func formatCopyViolation(src, label string, needDelegate string) string {
	if bad := inlineFormatListLine(src); bad != "" {
		return fmt.Sprintf("%s 里还留着内联准入清单（唯一事实源是 fileproc/formats.go）：%s", label, bad)
	}
	if needDelegate != "" && !strings.Contains(src, needDelegate) {
		return fmt.Sprintf("%s 必须委托到 %s（判据只许有一处）", label, needDelegate)
	}
	return ""
}

// inlineFormatListLine 找源码里"内联准入清单"的两种形状，命中返回那一行（截 120 字便于读报错）。
func inlineFormatListLine(src string) string {
	four := []string{".docx", ".xlsx", ".pptx", ".pdf"}
	for _, line := range strings.Split(src, "\n") {
		n := 0
		for _, e := range four {
			if strings.Contains(line, `"`+e+`"`) {
				n++
			}
		}
		if n >= 3 {
			return clip(line)
		}
		if regexp.MustCompile(`"\.docx"\s*:\s*true`).MatchString(line) {
			return clip(line)
		}
	}
	return ""
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 120 {
		return string([]rune(s)[:120])
	}
	return s
}

// ---------- L1 ----------

func TestFormatsGate01SingleSourceInPlace(t *testing.T) {
	// 唯一事实源自己必须被同一个探测器认出来，否则下面四条"都干净"可能只是因为探测器坏了
	formatsSrc := readGateFile(t, "../fileproc/formats.go")
	if inlineFormatListLine(formatsSrc) == "" {
		t.Fatal("探测器射程已失效：formats.go 那份清单是**应当**被认出的正对照，认不出＝这条负向锁恒真（假绿）")
	}

	cases := []struct{ file, delegate string }{
		// upload.go 走 map 形态（parseUpload 的签名要一个 map），另三处走判定函数
		{"upload.go", "fileproc.TranslateRestoreFormatsCopy("},
		{"tickets.go", "fileproc.IsTranslateFormat("},
		{"api_openapi_tasks.go", "fileproc.IsTranslateFormat("},
		{"../engine/file.go", "fileproc.IsTranslateFormat("},
	}
	for _, c := range cases {
		src := readGateFile(t, c.file)
		if v := formatCopyViolation(src, c.file, c.delegate); v != "" {
			t.Error(v)
		}
	}

	// 体积上限：三份字面量收成一处委托。这一档原先最坏的地方是"三个数一样但各写一遍"，
	// 于是有人只改其中一份时，现网表现是同一份文件在上传口能过、在 OpenAPI 口被拒。
	sizeSites := []string{"upload.go", "api_openapi_tasks.go"}
	maxLiteral := regexp.MustCompile(`\b40\s*<<\s*20\b`)
	for _, f := range sizeSites {
		src := readGateFile(t, f)
		if !strings.Contains(src, "fileproc.MaxUploadBytes") {
			t.Errorf("%s 必须委托 fileproc.MaxUploadBytes（体积上限只许一个事实源）", f)
		}
		if maxLiteral.MatchString(src) {
			t.Errorf("%s 里又出现第二份 `40 << 20` 字面量 ⇒ 两份上限迟早各说各话", f)
		}
	}

	// 反证 ①：把判定换回本地 map 的假源码必须判红
	fake := strings.Replace("package api\nfunc f() {\n\tif !fileproc.IsTranslateFormat(ext, textMode) {\n\t\treturn\n\t}\n}\n",
		"!fileproc.IsTranslateFormat(ext, textMode)",
		"!localAllowed[ext]", 1)
	fake = fake + "\nvar localAllowed = map[string]bool{\n\t\".docx\": true, \".xlsx\": true, \".pptx\": true, \".pdf\": true,\n}\n"
	if v := formatCopyViolation(fake, "假源码", "fileproc.IsTranslateFormat("); v == "" {
		t.Error("反证①落空：把委托换回本地清单仍判通过 ⇒ L1 抓不住任何退化")
	}
	// 反证 ②：一行多项那种副本也必须被抓（形状①）
	if inlineFormatListLine(`allowed := map[string]bool{".docx": true, ".xlsx": true, ".pptx": true}`) == "" {
		t.Error("反证②落空：一行多项的清单副本没被抓到")
	}
	// 对照：逐格式 switch 的真实写法不许误判（engine/file.go 的写回分支就是这个形状）
	if bad := inlineFormatListLine("switch ext {\ncase \".docx\":\n\t\treturn inplace\ncase \".pptx\":\n\t\treturn inplace\n}\n"); bad != "" {
		t.Errorf("对照落空：逐格式 switch 被误判成清单副本（%s）⇒ 下一位会直接把这条锁摘掉", bad)
	}
}

// ---------- L2 ----------

// TestFormatsGate02BehaviorEqualsFrozenCopies 把准入判定真跑一遍全表，与改造前四份副本的并集逐格对账。
//
// 这一腿是"只收形状、不翻对外结论"的**实质**证据：L1 只能证明源码里没有第二份清单，
// 证明不了那份清单的判定结果和原来一样（删掉 .yml 也照样绿灯）。
func TestFormatsGate02BehaviorEqualsFrozenCopies(t *testing.T) {
	base := map[string]bool{}
	for _, e := range restore12Frozen {
		base[e] = true
	}
	// 明确不准入的一批：可执行／压缩包／临时件／老格式里没被 anydoc 收的那几个。
	// ★ P1a：.png/.jpg 已移出本列表（图片现属准入项，见 restore12Frozen 尾部），
	//   若此处仍列它们，下方 L2 断言会因「IsTranslateFormat(.png)=true 但 base[.png]=false」而红——
	//   那正是这条锁要暴露的「准入面与冻结快照不一致」。
	notAdmitted := []string{".exe", ".zip", ".rar", ".pdfx", ".docxx", ".pages", ".key", ".wps", ".xls_", "", ".docx.", "."}

	for _, ext := range append(append([]string{}, restore12Frozen...), notAdmitted...) {
		if got := fileproc.IsTranslateFormat(ext, false); got != base[ext] {
			t.Errorf("还原模式判定不等值：%s got=%v want=%v（改造前四份副本的并集就是这个集合，动它属对外准入决策）", ext, got, base[ext])
		}
	}
	// 纯文案模式＝base12 ∪ AnydocFormats；两侧都用真值来源，逐个格点核对并集运算本身。
	// （直接遍历真值 map 的键：这里要判的是"并集运算"而不是清单内容，清单内容由 L2 上半段与 L3② 负责）
	for ext := range fileproc.AnydocFormats {
		want := base[ext] || fileproc.AnydocFormats[ext]
		if got := fileproc.IsTranslateFormat(ext, true); got != want {
			t.Errorf("纯文案模式判定不等值：%s got=%v want=%v", ext, got, want)
		}
	}
	for _, ext := range notAdmitted {
		if fileproc.IsTranslateFormat(ext, true) {
			t.Errorf("纯文案模式多放行了 %s ⇒ 执行口会拿到一个自己处理不了的格式", ext)
		}
	}

	// 契约锁：归一（小写＋带点）由**调用方**做，这一层不做。
	// 为什么要钉住"这一层不归一"：一旦这里加一次 ToLower，四个调用点各自的大小写口径
	// 就变成第五处隐含约定（有人以为自己传的是原样扩展名）。现有四点都是 strings.ToLower(filepath.Ext(...))。
	if fileproc.IsTranslateFormat(".DOCX", false) {
		t.Error("IsTranslateFormat 不许自己做大小写归一（归一只在调用点做，见函数注释）")
	}
	if fileproc.IsTranslateFormat("docx", false) {
		t.Error("IsTranslateFormat 只认带点的键（filepath.Ext 的形态），不许再兼容无点写法")
	}
}

// TestFormatsGate03SharedGateNotRaiseable 钉住 formats.go 头注③那条设计：调用方拿不到可改的共享闸门。
//
// 旧②那份写法是"本地新建 map ＋ 纯文案模式往里补 anydoc 键"，每次都是新字面量才没出事；
// 清单收成共享常量后，**同一段代码搬过来就是往全局里写**：纯文案模式跑一次，
// 还原模式的闸门当场被抬到含 .doc/.xls，且没有任何一处报错、没有任何一行日志。
func TestFormatsGate03SharedGateNotRaiseable(t *testing.T) {
	// 先量一份**没动过**的：这一份的长度就是 L2 的 oracle 长度，两边不同源时本文件所有等值锁一起过时。
	// （刻意在注入之前量：注入之后再量同一份 map，量到自己刚塞进去的两个键，
	//  于是这条射程腿会把"副本做得对"读成"闸门被抬起来了"——反证自伤，本批首跑真踩。）
	fresh := fileproc.TranslateRestoreFormatsCopy()
	if len(fresh) != len(restore12Frozen) {
		t.Fatalf("TranslateRestoreFormatsCopy 返回 %d 项，冻结清单是 %d 项 ⇒ L2 的 oracle 与这一份已不同源，本文件的等值锁一并过时",
			len(fresh), len(restore12Frozen))
	}
	fresh[".doc"] = true
	fresh[".zzz"] = true
	if fileproc.IsTranslateFormat(".doc", false) {
		t.Fatal("调用方改一份返回的 map 就把全站闸门抬起来了 ⇒ TranslateRestoreFormatsCopy 必须返回**新建副本**")
	}
	if fileproc.IsTranslateFormat(".zzz", true) {
		t.Fatal("往返回值里塞的键渗进了纯文案模式判定 ⇒ 共享闸门被外借了")
	}
	// 再取一份：注入过的键不许出现在后来者手里（这一腿才真正排除"返回的是同一个 map"）
	if again := fileproc.TranslateRestoreFormatsCopy(); len(again) != len(restore12Frozen) {
		t.Fatalf("第二次取到的闸门有 %d 项（应为 %d）⇒ 上一轮的注入渗进了共享状态", len(again), len(restore12Frozen))
	}
}

// ---------- 图片/音频格式锁（P1a/P2） ----------
// TestImageAudioFormatSetLockedToOrder 锁死「IsImageFormat / IsAudioFormat 集合」与
// 「TranslateFormatOrder 尾部图片/音频项」逐字同源。任一漂移（格式被准入但对应判定不认 /
// 判定认了未准入的扩展名）都会红——否则 image_heavy / 交付形态判定 / 提取层分发会各说各话。
func TestImageAudioFormatSetLockedToOrder(t *testing.T) {
	for _, ext := range fileproc.TranslateFormatOrder {
		if fileproc.IsImageFormat(ext) != isImageTail(ext) {
			t.Errorf("TranslateFormatOrder 的 %s：IsImageFormat=%v 与图片尾项清单=%v 不一致（两集合已漂移）",
				ext, fileproc.IsImageFormat(ext), isImageTail(ext))
		}
		if fileproc.IsAudioFormat(ext) != isAudioTail(ext) {
			t.Errorf("TranslateFormatOrder 的 %s：IsAudioFormat=%v 与音频尾项清单=%v 不一致（两集合已漂移）",
				ext, fileproc.IsAudioFormat(ext), isAudioTail(ext))
		}
	}
	// 反向：判定认的必须是已准入项（不能认一个建单会被拒的格式）
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic"} {
		if fileproc.IsImageFormat(ext) && !fileproc.IsTranslateFormat(ext, false) {
			t.Errorf("IsImageFormat(%s)=true 但该格式未准入（建单会被拒，image_heavy 会误打）", ext)
		}
	}
	for _, ext := range []string{".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus"} {
		if fileproc.IsAudioFormat(ext) && !fileproc.IsTranslateFormat(ext, false) {
			t.Errorf("IsAudioFormat(%s)=true 但该格式未准入（建单会被拒）", ext)
		}
	}
}

// isImageTail 仅用于本测试：TranslateFormatOrder 尾部图片项列表（与 formats.go 同源，单点维护）。
func isImageTail(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".bmp", ".tif", ".tiff", ".webp", ".heic":
		return true
	}
	return false
}

// isAudioTail 仅用于本测试：TranslateFormatOrder 尾部音频项列表（与 formats.go 同源，单点维护）。
func isAudioTail(ext string) bool {
	switch ext {
	case ".mp3", ".m4a", ".wav", ".ogg", ".flac", ".aac", ".opus":
		return true
	}
	return false
}

// ---------- L3 ----------

// 改造前四处拒绝文案的**字面量**（从改造前的源码逐字抄，不是从新代码反推）。
// 这一份的存在就是为了让"文案没变"这件事可判：只把注释改成"与原措辞一致"不算落地。
var frozenSentences = []struct{ label, want string }{
	{"tickets 纯文案", "不支持的格式: a.doc（纯文案模式支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/png/jpg/jpeg/bmp/tif/tiff/webp/heic/mp3/m4a/wav/ogg/flac/aac/opus 及 doc/xls/ppt/odt/ods/odp/rtf/epub）"},
	{"tickets 还原", "不支持的格式: a.doc（仅支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/png/jpg/jpeg/bmp/tif/tiff/webp/heic/mp3/m4a/wav/ogg/flac/aac/opus）"},
	{"openapi 还原", "不支持的格式: a.doc（仅支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/png/jpg/jpeg/bmp/tif/tiff/webp/heic/mp3/m4a/wav/ogg/flac/aac/opus）"},
	{"engine 纯文案", "不支持的格式（纯文案模式支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/png/jpg/jpeg/bmp/tif/tiff/webp/heic/mp3/m4a/wav/ogg/flac/aac/opus/doc/xls/ppt/odt/ods/odp/rtf/epub 等）"},
	{"engine 还原", "不支持的格式（支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/png/jpg/jpeg/bmp/tif/tiff/webp/heic/mp3/m4a/wav/ogg/flac/aac/opus）"},
}

func TestFormatsGate04CopyIsDerivedAndUnchanged(t *testing.T) {
	// ① 两个 hint 函数各自逐字钉住当前措辞（★ P1a 已含图片格式，与 TranslateFormatOrder 同源派生）
	if got := fileproc.FormatHintBase(); got != "docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/png/jpg/jpeg/bmp/tif/tiff/webp/heic/mp3/m4a/wav/ogg/flac/aac/opus" {
		t.Errorf("FormatHintBase() 变了：现读 %q ⇒ 对外文案与准入清单不等值（要改就按对外口径决策走，并同步四处冻结句子＋前端 accept＋文档面）", got)
	}
	if got := fileproc.FormatHintText(); got != "doc/xls/ppt/odt/ods/odp/rtf/epub" {
		t.Errorf("FormatHintText() 变了：现读 %q", got)
	}

	// ② 文案必须由清单派生（改清单即改文案），而不是第五份字面量
	base := fileproc.FormatHintBase()
	for _, e := range fileproc.TranslateFormatOrder {
		if e == ".yml" {
			continue // 别名不在对外清单里重复出现（旧措辞里也只出现 yaml 一次）
		}
		if !strings.Contains("/"+base+"/", "/"+e[1:]+"/") {
			t.Errorf("清单项 %s 没出现在出栈文案里 ⇒ 文案又变成手抄的第二份了", e)
		}
	}

	// ③ 拼接后的整句必须与改造前逐字相同（用同一批 hint 函数拼，判的是"拼接骨架没变"）
	got := []string{
		"不支持的格式: " + "a.doc" + "（纯文案模式支持 " + fileproc.FormatHintBase() + " 及 " + fileproc.FormatHintText() + "）",
		"不支持的格式: " + "a.doc" + "（仅支持 " + fileproc.FormatHintBase() + "）",
		"不支持的格式: " + "a.doc" + "（仅支持 " + fileproc.FormatHintBase() + "）",
		"不支持的格式（纯文案模式支持 " + fileproc.FormatHintBase() + "/" + fileproc.FormatHintText() + " 等）",
		"不支持的格式（支持 " + fileproc.FormatHintBase() + "）",
	}
	for i, c := range frozenSentences {
		if got[i] != c.want {
			t.Errorf("%s 文案不等值：\n got=%s\nwant=%s", c.label, got[i], c.want)
		}
	}

	// ④ 样本清单里的每一个名字都必须真在准入名单里（文案不许承诺收不进来的格式）
	if v := sampleViolation(fileproc.AnydocHintSample, ".zzz"); v == "" {
		t.Error("反证落空：样本里凭空多一个名单外的格式却没判红 ⇒ 这一腿抓不住任何过度承诺")
	}
	if v := sampleViolation(fileproc.AnydocHintSample); v != "" {
		t.Errorf("%s", v)
	}
}

// sampleViolation 判「展示样本 ⊆ 准入名单」。额外入参用来喂反证（把一个名单外的名字塞进样本）。
func sampleViolation(sample string, extra ...string) string {
	list := strings.Split(sample, "/")
	list = append(list, extra...)
	for _, name := range list {
		ext := "." + strings.TrimSpace(name)
		if ext == "" || ext == "." {
			continue
		}
		if !fileproc.AnydocFormats[ext] && !fileproc.IsTranslateFormat(ext, false) {
			return fmt.Sprintf("出栈文案举了 %s，但它既不在还原模式清单也不在 AnydocFormats ⇒ 文案在承诺一个收不进来的格式", ext)
		}
	}
	return ""
}

// ---------- L4 + L5 ----------

// TestFormatsGate05UploadSizeSpansLayers 现读现算比三层档位：Go 常量 vs Caddy 那三处 `max_size`。
//
// 为什么必须现读而不是写死 40：写死之后 Go 侧改成 50MiB 那天这条锁还绿，
// 而"后端还没拒绝、反代先把请求掐了"这一段就没人守了（客户看到的是一句没有中文文案的 413）。
//
// ⚠️ 这一腿锁的是**档位数字与单位写法**（40MB ↔ 40<<20），不是"两层字节数逐字节相等"：
// Caddy 的 `40MB` 按哪个基数解析（10^6 还是 2^20）本批**未在现网实测**，
// 两种读法相差 1,943,040 字节，落在哪一侧决定"40,000,000–41,943,040 这一段被谁先拒"。
// 那一段属对外阈值口径，已单列待决策；不许有人拿这条锁的绿灯去声明"两层字节一致"。
func TestFormatsGate05UploadSizeSpansLayers(t *testing.T) {
	wantMB := (fileproc.MaxUploadBytes + (1 << 20) - 1) / (1 << 20) // 档位数字（向上取整，40<<20 → 40）
	maxSizeRe := regexp.MustCompile(`max_size\s+(\d+)([KM]?)B`)
	sites := []string{
		"../../../deploy/caddy/translator.conf",
		"../../../deploy/caddy/translator-tenant.conf",
		"../../../scripts/bootstrap-demo.sh",
	}
	for _, f := range sites {
		src := readGateFile(t, f)
		var readings []string
		matched := 0
		for _, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue // 说明行不算配置（bootstrap-demo.sh 里那句解释 max_size 的注释不是这一层的事实）
			}
			m := maxSizeRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// 本仓另有 10MB 档（挂件等其它路由），**不是**同一把尺子，所以这里只数"与上传同档"那一处，
			// 而不是要求整个文件只有一处 max_size（那会把别的路由一起判死，也是下一位摘锁的理由）。
			if m[1] == fmt.Sprint(wantMB) && m[2] == "M" {
				matched++
			}
			readings = append(readings, strings.TrimSpace(m[0]))
		}
		if matched == 0 {
			t.Errorf("%s: 没读到与 Go 侧同档（%dMB）的 max_size ⇒ 现读到的档位是 [%s]"+
				"（要么反代那层改了档没跟后端对齐，要么这条锁的锚点漂了——两种都必须当场看见）",
				f, wantMB, strings.Join(readings, " "))
		}
	}

	// L5 方向锁：对外承诺 ≤ 实际闸门。
	// 先抬文档后抬闸门的那一段里，客户按文档的承诺上传，会被真实闸门拒在一个没有中文文案的位置上。
	if int64(fileproc.OpenAPIDocLimitMB)<<(20) > fileproc.MaxUploadBytes {
		t.Errorf("对外承诺 %dMB 高于实际闸门 %d 字节 ⇒ 承诺高过守得住的那条线", fileproc.OpenAPIDocLimitMB, fileproc.MaxUploadBytes)
	}
}

// ---------- L6 ----------

// TestFormatsGate06PageCountWarnOnBothLegs 钉「页数读数取不到」两腿同时出声、且共用一个短语。
//
// 为什么这条值得锁：派发资格是「体积 ∧ 页数」两条腿，页数读 0 会让 TryDispatch 直接判不合格、
// 连一次远端都不拨，而旧形态**一行日志都不打**（〇-AF 补丁四那条事故）。
// 上传侧这一档的方向是"放行"（不误伤正常请求），派发侧是"不派"（宁窄勿宽）——
// 方向刻意相反，所以短语一致只用来保证**一条 grep 能同时抓到两条腿**，不是把两者判成同一个结论。
func TestFormatsGate06PageCountWarnOnBothLegs(t *testing.T) {
	uploadSrc := readGateFile(t, "upload.go")
	remoteSrc := readGateFile(t, "../fileproc/fileproc_remote.go")
	for _, c := range []struct{ label, src string }{{"upload.go", uploadSrc}, {"fileproc_remote.go", remoteSrc}} {
		if v := pageCountWarnViolation(c.label, c.src); v != "" {
			t.Error(v)
		}
	}

	// 两腿都用 observability（日志棘轮：新增一律不许标准库 log.Printf）。
	// 判据先剥行注释再数，否则注释里那句"禁止用 log.Printf"会自己命中自己——恒真的负向锁。
	for _, c := range []struct{ label, src string }{{"upload.go", uploadSrc}, {"fileproc_remote.go", remoteSrc}} {
		code := stripLineComments(c.src)
		if strings.Contains(code, "log.Printf(\""+pageCountPhrase) {
			t.Errorf("%s 用标准库 log 打这一条（日志棘轮：新增一律 observability）", c.label)
		}
	}
	// 反证：把两处短语改掉之后，同一套判据必须判红（否则上面那两条绿灯是恒真的）
	for _, src := range []string{uploadSrc, remoteSrc} {
		fake := strings.Replace(src, pageCountPhrase+" ⇒", "读不出页码 ⇒", 1)
		if fake == src {
			t.Fatal("反证构造失败：源码里没有「页数读数取不到 ⇒」这一处（说明短语锚点漂了，L6 已经扫不到任何东西）")
		}
		if pageCountWarnViolation("反证样本", fake) == "" {
			t.Error("反证落空：改掉短语后仍判通过 ⇒ 这一腿抓不住任何退化")
		}
	}

	// 对照（防"注释冒充实现"）：只把短语留在注释里、把真 WARN 那行删掉，必须判红。
	// 这一族本仓真踩过（§一·5 那条品牌尾注：说明注释里抄了文案，负向锁就把注释当成了实现）。
	dropped := ""
	for _, line := range strings.Split(uploadSrc, "\n") {
		if strings.Contains(line, "observability.Warn(") && strings.Contains(line, pageCountPhrase) {
			continue // 只删那一行 WARN，保留所有注释
		}
		dropped += line + "\n"
	}
	if !strings.Contains(dropped, pageCountPhrase) {
		t.Fatal("对照构造失败：删掉 WARN 行后源码里连注释中的短语都没了 ⇒ 这一对照测的不是「注释冒充实现」那一族")
	}
	if pageCountWarnViolation("对照样本", dropped) == "" {
		t.Error("对照落空：WARN 那行已被删掉、只剩注释里提到短语，判据却仍判通过 ⇒ 注释在冒充实现")
	}
}

const pageCountPhrase = "页数读数取不到"

// pageCountWarnWarnLineRe 认「同一行里既有 observability.Warn( 又有那一档短语」。
// 只认一行之内：这一族的调用都是一行写完消息再换行接 kv，跨行匹配会把无关的两行拼成假命中。
var pageCountWarnWarnLineRe = regexp.MustCompile(`observability\.Warn\([^\n]*` + pageCountPhrase)

// pageCountWarnViolation 判一腿是否"读不到页数时会吭声"。返回问题描述（合格回空串）。
//
// 判的是**日志面**而不是结论：两腿的方向刻意相反（上传侧放行、派发侧不派），
// 这条锁不许把两者判成同一个结论，只保证一条 grep 能同时抓到两条腿、且都不是静默。
func pageCountWarnViolation(label, src string) string {
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // 注释不算实现：本仓 §一·5 那条「注释里抄了文案会把负向锁读成已落地」的同族防线
		}
		if pageCountWarnWarnLineRe.MatchString(line) {
			return ""
		}
	}
	return fmt.Sprintf("%s 里没有一行「observability.Warn(...%s...)」⇒ 页数读数取不到又变回静默放行/静默不派"+
		"（本仓 §一·12：有自动降级的链路，静默不算验收）", label, pageCountPhrase)
}

// ---------- 工具 ----------

// stripLineComments 丢掉行注释，只留真会被编译的那一份文本。
// 负向锁（"这里不许出现 log.Printf"）必须先过这一层，否则**这条锁自己写的注释**就会命中自己，
// 于是红灯亮在一次说明上——下一位会把负向腿当成误报删掉（本仓 §一·5 那条同族）。
func stripLineComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// readGateFile 读**相对本包目录**（backend-go/internal/api）的源码/配置文件。路径写错必须当场红，
// 不许退成"读不到所以没命中所以干净"（那正是本文件所有负向腿最怕的形态）。
//
// ⚠️ 深度口径（两次跨语言路径真踩过，一次比一次贵）：本包在 `backend-go/internal/api`，
// 所以同级的 fileproc/engine 是 `../fileproc/…`（**一个**点），而仓库根是 `../../../`（三个点）。
// 写错不报语法错，只报 ENOENT——前端那半边的锁同理，它的 ROOT 是 frontend-react 包根，
// 后端源码要带 `../backend-go/…`。
// 不许退成"读不到所以没命中所以干净"（那正是本文件所有负向腿最怕的形态）。
func readGateFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走，读不到＝射程失效，不算绿）：%v", rel, err)
	}
	return string(b)
}
