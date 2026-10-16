// ============ tts_lang_coverage_gate_test.go · 职责说明 ============
// TTS「覆盖语种」与「产物容器」这两把尺子的同源锁（2026-10-16 中英档上线批建立）。
//
// 为什么需要这个文件：覆盖语种在前端（normTtsLang）、Go（ttsLangCovered）、脚本
// （norm_lang/lang_covered）三处各自判一次，产物容器在 Go（DubContainerFor）与脚本
// （CONTAINER_ARGS）两处各自判一次。历史形态反复证明「两侧各写一套判据必然漂」——
// 前端送 zh_hant、后端按 zh-hant 判，边界永远差一档；容器表一侧加了 .opus 另一侧没加，
// 表现是「服务器上明明转得出来，产物却还是 mp3」这种没有任何报错的静默降级。
//
// 四条腿：
//
//	L1 清单等值：ttsCoveredLangs ↔ tts_speak.py 的 LANGS（逐项含顺序 + 形状自证）；
//	L2 行为等值：ttsLangCovered ↔ 脚本 lang_covered，**同一张输入表**逐条比对（不是比对实现文本）；
//	L3 形状腿：脚本里不许再出现 `OfflineTts.from_*`——现役 sherpa-onnx 1.13.8 的 Python API
//	   只有「配置对象」一种构造形态，旧实现照不存在的类方法写（探测全绿、一合成必败）；
//	L4 容器表等值：DubContainerFor ↔ 脚本 CONTAINER_ARGS（＋wav 原生档），并锁住
//	   engine/file.go 的音频配音确实走了这一处映射（不是把 mp3 写死）。
//
// 反证：L3/L4 各自把人工变异体喂给**同一个判据函数**，证明这两条腿真能红
// （AGENTS §三：判据级断言代替不了守卫级——没有变异体就分不清"腿有射程"与"腿恒真"）。
// =============================================
package fileproc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// pyLangListRe 抓 `LANGS = [...]` 清单（与 capabilities_p0_test.go 的 pyModuleListRe 同族）。
var pyLangListRe = regexp.MustCompile(`(?m)^LANGS\s*=\s*\[(.*?)\]`)

// pyContainerKeysRe 抓 `CONTAINER_ARGS = { ... }` 块，用于 L4 的容器表等值。
var pyContainerKeysRe = regexp.MustCompile(`(?s)CONTAINER_ARGS\s*=\s*\{(.*?)\}`)

// pyContainerKeyRe 抓容器表里的键（".mp3" 这类带点后缀）。
var pyContainerKeyRe = regexp.MustCompile(`"(\.[a-z0-9]+)"`)

// scriptLangs 解析脚本的 LANGS 清单；取不到即判红（清单丢了和"清单为空"长得一样，前者更坏）。
func scriptLangs(t *testing.T, script string) []string {
	t.Helper()
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("读脚本失败 %s: %v（脚本必须随仓库提交，同源锁才有被锁的对象）", script, err)
	}
	m := pyLangListRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s 里找不到 LANGS = [...] ⇒ 覆盖语种协议被破坏", script)
	}
	var langs []string
	for _, s := range pyStringRe.FindAllStringSubmatch(string(m[1]), -1) {
		langs = append(langs, s[1])
	}
	if len(langs) == 0 {
		t.Fatalf("%s 的 LANGS 清单为空 ⇒ 语种门形同虚设（越界不会被拒）", script)
	}
	return langs
}

// TestTtsCoveredLangsMatchScript L1：Go 侧覆盖语种清单与脚本 LANGS 逐项等值。
//
// 顺序也判：清单是出栈给前端与 /api/health 的（`tts_langs`），顺序变了就是对外读数变了。
func TestTtsCoveredLangsMatchScript(t *testing.T) {
	pyLangs := scriptLangs(t, "tts_speak.py")
	if len(pyLangs) != len(ttsCoveredLangs) {
		t.Fatalf("覆盖语种两侧不等长：Go=%v 脚本=%v ⇒ 一侧改忘另一侧", ttsCoveredLangs, pyLangs)
	}
	for i := range ttsCoveredLangs {
		if ttsCoveredLangs[i] != pyLangs[i] {
			t.Fatalf("覆盖语种第 %d 项不等值：Go=%q 脚本=%q", i, ttsCoveredLangs[i], pyLangs[i])
		}
	}
	// 形状自证：清单项必须是 `zh` 这种**裸两字母小写码**。放进来一个 "zh-Hant" 或大写
	// 会让 ttsLangCovered 的归一化结果永远匹配不上（清单自身不可达 ⇒ 语种门恒判越界）。
	for _, lc := range ttsCoveredLangs {
		if lc != strings.ToLower(lc) || strings.ContainsAny(lc, "-_") || len(lc) < 2 {
			t.Fatalf("覆盖语种项形状不合规：%q（必须是裸小写码，如 zh/en）", lc)
		}
	}
}

// ttsLangParityTable 归一化／覆盖判定的**同一张输入表**（L2 用；前端 vitest 的 P7 用同族形态）。
// 覆盖四类边界：大小写、地域段（- 与 _）、空缺值、越界语种。
var ttsLangParityTable = []string{
	"zh", "en", "ZH", "en-US", "zh-Hant", "zh_hant", "ZH-HANT", "zh-CN",
	"ja", "fr", "ko", "", " ", "-en", "-", "zh-", "xx",
}

// TestTtsLangCoverageBehaviorParity L2：拿同一张表比对 Go 与脚本的**判定行为**。
//
// 刻意不比对实现文本（两边写法本就不同：Go 用 IndexAny 切、Python 用 replace+split）——
// 要比的是"同一个输入给出同一个答案"，那才是前端/后端/脚本三处口径不漂的充要条件。
func TestTtsLangCoverageBehaviorParity(t *testing.T) {
	py := findPython()
	if strings.TrimSpace(py) == "" {
		// AGENTS §二：不许把"依赖没装"写成"跳过"。本腿只用标准库，跑不起来说明 Python 解释器缺失。
		t.Fatalf("找不到 python3 解释器 ⇒ 覆盖语种行为等值无法验证（只许补依赖，不许放宽或跳过）")
	}
	// 组装 Python 侧算式：import 脚本（模块顶层只用标准库）→ 对同一张表逐条判 → 打 1/0 串。
	lit := make([]string, 0, len(ttsLangParityTable))
	for _, s := range ttsLangParityTable {
		lit = append(lit, strconv.Quote(s))
	}
	code := fmt.Sprintf(
		`import sys; sys.path.insert(0, %s); import tts_speak as m; `+
			`print(",".join("1" if m.lang_covered(x) else "0" for x in [%s]))`,
		strconv.Quote(filepath.Dir("tts_speak.py")), strings.Join(lit, ", "))
	out, err := exec.Command(py, "-c", code).Output()
	if err != nil {
		t.Fatalf("调用脚本 lang_covered 失败：%v（stderr 见下）\n%s", err, errStderr(err))
	}
	pyVerdicts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(pyVerdicts) != len(ttsLangParityTable) {
		t.Fatalf("脚本返回 %d 条判定，期望 %d 条 ⇒ 脚本侧判据形态变了", len(pyVerdicts), len(ttsLangParityTable))
	}
	seen := 0
	for i, lang := range ttsLangParityTable {
		want := pyVerdicts[i] == "1"
		got := ttsLangCovered(lang)
		if got != want {
			t.Fatalf("语种 %q 判定两侧不一致：Go=%v 脚本=%v ⇒ 归一化口径已漂", lang, got, want)
		}
		if want {
			seen++
		}
	}
	// 射程自证：整张表必须**两档都出现过**，否则"两边都恒判是/恒判否"也能全绿。
	if seen == 0 || seen == len(ttsLangParityTable) {
		t.Fatalf("输入表射程失效：%d/%d 条判为覆盖 ⇒ 表里只有一档，锁不住边界", seen, len(ttsLangParityTable))
	}
}

// errStderr 取出 exec.ExitError 的 stderr 文本（排障用；不是所有 error 都带）。
func errStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return string(ee.Stderr)
	}
	return ""
}

// ttsPhantomClassmethodRe 现役引擎**不存在**的类方法调用形态（sherpa-onnx 1.13.8 无 from_*）。
var ttsPhantomClassmethodRe = regexp.MustCompile(`OfflineTts\.from_[A-Za-z_]+`)

// stripPyNoise 去掉 Python 源码里的注释与**文档字符串**（两者都是"说明"，不是"实现"）。
// ★ 为什么连 docstring 一起剥：本脚本 `_build_tts` 的文档字符串里**故意写着**那些不存在的
// 类方法名（那是记录事故本身），只剥 `#` 注释就会把说明判成实现——本批首跑正是这样自伤一次。
// 判据只该盯**代码行**（AGENTS §一·5「注释里不许出现那串字面」那条的镜像形态：
// 给锁补剥注释的形，不删注释）。
func stripPyNoise(src string) string {
	var b strings.Builder
	runes := []rune(src)
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case c == '#':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			if i+2 < len(runes) && runes[i+1] == c && runes[i+2] == c {
				// 三引号（文档字符串）：整体丢弃，只补一个换行保持行号可读
				i += 3
				for i+2 < len(runes) && !(runes[i] == c && runes[i+1] == c && runes[i+2] == c) {
					i++
				}
				if i+2 < len(runes) {
					i += 3
				} else {
					i = len(runes)
				}
				b.WriteRune('\n')
				continue
			}
			// 单行字符串：原样保留（那是代码），但跳过其内部字符，
			// 免得把 `"#"` 当注释起点、也免得把字符串里的引号当代码。
			b.WriteRune(c)
			i++
			for i < len(runes) {
				if runes[i] == '\\' && i+1 < len(runes) {
					b.WriteRune(runes[i])
					b.WriteRune(runes[i+1])
					i += 2
					continue
				}
				b.WriteRune(runes[i])
				if runes[i] == c {
					i++
					break
				}
				i++
			}
		default:
			b.WriteRune(c)
			i++
		}
	}
	return b.String()
}

// ttsPhantomHits 判据本体（纯函数，L3 与其反证共用同一份）。
func ttsPhantomHits(src []byte) []string {
	return ttsPhantomClassmethodRe.FindAllString(stripPyNoise(string(src)), -1)
}

// TestTtsScriptHasNoPhantomClassmethods L3：脚本里不许出现现役引擎不存在的构造类方法。
//
// 为什么这条必须机械化：旧实现就是照着 `OfflineTts.from_vits(...)` 写的，而 1.13.8 只认
// 配置对象——那种代码**探测全绿**（模块在、权重在、音色在），只有真合成时才抛，
// 属于「全绿但一进去必崩」那一族（AGENTS §一·12 D5 口径）。
func TestTtsScriptHasNoPhantomClassmethods(t *testing.T) {
	src, err := os.ReadFile("tts_speak.py")
	if err != nil {
		t.Fatalf("读脚本失败: %v", err)
	}
	if hits := ttsPhantomHits(src); len(hits) > 0 {
		t.Fatalf("tts_speak.py 出现现役引擎不存在的类方法调用：%v ⇒ 合成时必崩（改用 OfflineTtsConfig + OfflineTts(cfg)）", hits)
	}
	// 反证：同一个判据喂人工变异体必须能抓到（否则这条腿可能只是"恰好没命中"）。
	// ⚠️ 变异锚点必须落在**代码行**上：文件里第一处 `sherpa_onnx.OfflineTts(` 就在它自己的
	// 文档字符串里（记录事故那句），拿它当锚点会被剥噪声剥掉 ⇒ 反证假绿（首跑真踩一次）。
	mutated := bytes.Replace(src, []byte("return sherpa_onnx.OfflineTts("), []byte("return sherpa_onnx.OfflineTts.from_vits("), 1)
	if bytes.Equal(mutated, src) {
		t.Fatalf("反证自伤：变异锚点 `return sherpa_onnx.OfflineTts(` 在脚本代码行里找不到（脚本形态变了就得同步改这里）")
	}
	if hits := ttsPhantomHits(mutated); len(hits) == 0 {
		t.Fatalf("反证失败：把 from_vits 调用装回脚本后判据仍判绿 ⇒ L3 没有射程")
	}
}

// scriptContainerKeys 解析脚本 CONTAINER_ARGS 的键集合。
func scriptContainerKeys(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("tts_speak.py")
	if err != nil {
		t.Fatalf("读脚本失败: %v", err)
	}
	m := pyContainerKeysRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("tts_speak.py 里找不到 CONTAINER_ARGS 表 ⇒ 产物容器协议被破坏")
	}
	var keys []string
	for _, s := range pyContainerKeyRe.FindAllStringSubmatch(string(m[1]), -1) {
		keys = append(keys, s[1])
	}
	if len(keys) == 0 {
		t.Fatalf("CONTAINER_ARGS 表为空 ⇒ 除 wav 外任何容器都转不出来")
	}
	return keys
}

// TestDubContainerTableAgreesWithScript L4：Go 的产物容器映射与脚本转码表逐项等值，
// 且 engine 侧音频配音确实走这一处映射（不是把 mp3 写死）。
func TestDubContainerTableAgreesWithScript(t *testing.T) {
	keys := scriptContainerKeys(t)
	hasMP3 := false
	for _, k := range keys {
		if k == ".mp3" {
			hasMP3 = true
		}
		// 表里的每个容器，Go 侧必须原样放行（否则脚本转得出来、产物名却还是 mp3＝静默改容器）。
		if got := DubContainerFor(k); got != k {
			t.Fatalf("容器 %s 两侧不等值：Go=%q ⇒ 脚本会转出该容器，Go 却写成别的后缀", k, got)
		}
	}
	// 回落容器必须在脚本表里（否则回落时脚本自己都编不出来）。
	if !hasMP3 {
		t.Fatalf("脚本 CONTAINER_ARGS 缺 .mp3 ⇒ Go 的回落档在脚本侧不可达")
	}
	// wav 是引擎原生档（不进转码表），必须原样放行。
	if got := DubContainerFor(".wav"); got != ".wav" {
		t.Fatalf("wav 应原样放行（引擎原生落盘），实得 %q", got)
	}
	// 表外／空值／大小写：一律回落 mp3，且大小写不敏感。
	for _, in := range []string{".xyz", "", ".m4a.bak", ".MP3", ".M4A", " .ogg "} {
		got := DubContainerFor(in)
		if got != ".mp3" && got != strings.ToLower(strings.TrimSpace(in)) {
			t.Fatalf("容器 %q 的回落不合口径：%q（表外应回 .mp3，表内应回归一后原值）", in, got)
		}
	}
	if got := DubContainerFor(".xyz"); got != ".mp3" {
		t.Fatalf("表外容器应回落 .mp3，实得 %q", got)
	}
	// engine 侧接线：配音产物名的主干必须由 DubContainerFor 给出（写死 mp3 就抓不到了）。
	eng, err := os.ReadFile(filepath.Join("..", "engine", "file.go"))
	if err != nil {
		t.Fatalf("读 engine/file.go 失败: %v", err)
	}
	if !ttsDubWiringOK(eng) {
		t.Fatalf("engine/file.go 未走 fileproc.DubContainerFor ⇒ 配音容器回到「写死 mp3」的老形态")
	}
	// 反证：把那一行摘掉后同一判据必须判红。⚠️ 必须摘**全部**出现（file.go 有两处：
	// dubWithFallback 的取容器点 + warning 文案里回显原容器），只摘第一处会留下第二处 ⇒ 反证假绿。
	stripped := bytes.ReplaceAll(eng, []byte("fileproc.DubContainerFor("), []byte("mp3ContainerFor("))
	if bytes.Equal(stripped, eng) {
		t.Fatalf("反证自伤：engine/file.go 里找不到 fileproc.DubContainerFor( 锚点")
	}
	if ttsDubWiringOK(stripped) {
		t.Fatalf("反证失败：摘掉 DubContainerFor 接线后判据仍判绿 ⇒ L4 的接线腿没有射程")
	}
}

// ttsDubWiringOK engine 侧配音容器接线的判据本体（L4 与其反证共用）。
func ttsDubWiringOK(src []byte) bool {
	return bytes.Contains(src, []byte("fileproc.DubContainerFor("))
}
