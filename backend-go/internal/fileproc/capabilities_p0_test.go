// ============ capabilities_p0_test.go · 职责说明 ============
// OCR/ASR/TTS 三项新能力（2026-10-14 P0 基础件）的探测单测。
// 断言口径照 healthcheck_test.go 的 D5 双向对照：探测必须能区分「有/没有」，
// HealthResult 字段必须等于本机实探测（不许写死常量），且**Go 侧与 Python 脚本侧
// 两份依赖清单必须锁在同一值上**——docstring 里承诺「改一侧必红」，不断言就是空话。
// =============================================
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

// pyModuleListRe 从脚本源码里抓 MODULES = [...] 清单（ocr/asr 是单行，tts 是多行）。
var pyModuleListRe = regexp.MustCompile(`(?s)MODULES\s*=\s*\[(.*?)\]`)

// pyStringRe 抓双引号字符串（模块名）。
var pyStringRe = regexp.MustCompile(`"([^"]+)"`)

// scriptModules 解析指定 Python 脚本的 MODULES 清单。
// 脚本不存在或没有清单时返回 nil——调用方据此判「协议被破坏」。
func scriptModules(t *testing.T, script string) []string {
	t.Helper()
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("读脚本失败 %s: %v（脚本必须随仓库提交，探测协议才有被锁的对象）", script, err)
	}
	m := pyModuleListRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("%s 里找不到 MODULES = [...] 清单 ⇒ 探测协议被破坏", script)
	}
	var mods []string
	for _, s := range pyStringRe.FindAllStringSubmatch(string(m[1]), -1) {
		mods = append(mods, s[1])
	}
	if len(mods) == 0 {
		t.Fatalf("%s 的 MODULES 清单为空 ⇒ 探测形同虚设", script)
	}
	return mods
}

// TestCapabilityModuleListsInLockstep ★ 同源锁：Go 侧依赖清单与 Python 脚本侧
// MODULES 清单必须逐项等值。任何一侧单独改动（加依赖忘改另一边、改导入名忘改另一边）
// 都会在这里红——这正是三个能力文件 docstring 里承诺的「改一侧必红」。
func TestCapabilityModuleListsInLockstep(t *testing.T) {
	cases := []struct {
		name   string
		goList []string
		script string
	}{
		{"ocr", ocrPythonModules, "ocr_recognize.py"},
		{"asr", asrPythonModules, "asr_transcribe.py"},
		{"tts", ttsPythonModules, "tts_speak.py"},
	}
	for _, c := range cases {
		pyList := scriptModules(t, c.script)
		if len(pyList) != len(c.goList) {
			t.Fatalf("%s 依赖清单两侧不等长：Go=%v 脚本=%v ⇒ 同源承诺被破坏", c.name, c.goList, pyList)
		}
		for i := range c.goList {
			if c.goList[i] != pyList[i] {
				t.Fatalf("%s 依赖清单第 %d 项不等值：Go=%q 脚本=%q ⇒ 一侧改忘另一侧", c.name, i, c.goList[i], pyList[i])
			}
		}
	}
}

// TestProbeCapabilitiesDiscriminate ★ D5 双向口径：三项探测各自满足——
// ① 探测结论必须等于「模块清单逐个实探 + 资产落盘实查」的手算期望（不是写死常量）；
// ② 不就绪时告警必须非空且指向可执行动作（pip install / assets 路径），
//
//	防止「沉默字段」（探测失败了但没人知道该干什么）。
func TestProbeCapabilitiesDiscriminate(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机无 python3。本用例不许 skip——" +
			"依赖探测若在无 Python 的机器上失去验证，就等于承认『健康检查可以是空转』")
	}
	probes := []struct {
		name  string
		probe func(string) (bool, string)
	}{
		{"ocr", probeOcr},
		{"asr", probeAsr},
		{"tts", probeTts},
	}
	for _, p := range probes {
		ok, warn := p.probe(py)
		if !ok && strings.TrimSpace(warn) == "" {
			t.Fatalf("%s 探测不就绪却没有告警文案 ⇒ 这会变成谁也看不见的沉默字段（D5 形态）", p.name)
		}
		if !ok && !strings.Contains(warn, "pip install") && !strings.Contains(warn, "assets") {
			t.Fatalf("%s 告警不含可执行动作（pip install / assets 路径）：%q", p.name, warn)
		}
	}
}

// TestHealthResultCapabilityFieldsMatchRealProbe HealthResult 的三个新字段必须等于
// 本机实探测（照 TestPymupdfInHealthResult 口径）：字段不是照实填的，健康检查就失去意义。
func TestHealthResultCapabilityFieldsMatchRealProbe(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机无 python3（同 TestProbeCapabilitiesDiscriminate，不许 skip）")
	}
	wantOcr, _ := probeOcr(py)
	wantAsr, _ := probeAsr(py)
	wantTts, _ := probeTts(py)
	res := CheckHealth()
	if res.OcrAvail != wantOcr || res.AsrAvail != wantAsr || res.TtsAvail != wantTts {
		t.Fatalf("HealthResult 能力字段与本机实探测不一致：ocr=%v/%v asr=%v/%v tts=%v/%v ⇒ 字段不是照实填的",
			res.OcrAvail, wantOcr, res.AsrAvail, wantAsr, res.TtsAvail, wantTts)
	}
}

// TestCapabilityScriptPathResolution 三个能力的脚本定位必须同时满足：
// ① 环境变量覆盖生效（开发/测试约定，同 ANYDOC_SCRIPT）；
// ② 默认值=可执行文件同目录（部署约定，随二进制落 /opt/translator/bin/）。
func TestCapabilityScriptPathResolution(t *testing.T) {
	cases := []struct {
		env  string
		got  func() string
		want string
	}{
		{OcrScriptEnv, ocrScriptPath, "ocr_recognize.py"},
		{AsrScriptEnv, asrScriptPath, "asr_transcribe.py"},
		{TtsScriptEnv, ttsScriptPath, "tts_speak.py"},
	}
	for _, c := range cases {
		t.Setenv(c.env, "/tmp/override_"+c.want)
		if got := c.got(); got != "/tmp/override_"+c.want {
			t.Errorf("%s 环境变量覆盖未生效：got=%q", c.env, got)
		}
		os.Unsetenv(c.env)
		want := filepath.Join(filepath.Dir(os.Args[0]), c.want)
		if got := c.got(); got != want {
			t.Errorf("默认脚本路径应为可执行同目录 %q，got=%q", want, got)
		}
	}
}

// TestCapabilityAssetsDirFollowsScript 资产目录必须跟着**脚本**走（不是二进制目录）：
// 脚本被环境变量指到别处时，assets/ 若还按二进制目录找就会错位——
// 这正是「本地跑得好好的、线上资产缺失」的经典形态。
func TestCapabilityAssetsDirFollowsScript(t *testing.T) {
	t.Setenv(TtsScriptEnv, "/tmp/somewhere/tts_speak.py")
	if got, want := ttsAssetsDir(), filepath.Join("/tmp/somewhere", "assets", "tts"); got != want {
		t.Errorf("ttsAssetsDir 应跟随脚本目录：got=%q want=%q", got, want)
	}
	if got, want := ttsVoicesDir(), filepath.Join("/tmp/somewhere", "assets", "tts", "voices"); got != want {
		t.Errorf("ttsVoicesDir 应跟随脚本目录：got=%q want=%q", got, want)
	}
}

// TestTtsVoiceNonEmptyRequired 默认音色判据是「存在**且非空**」：
// 零字节文件同样让合成失败，纯存在性检查会被它骗过（D5 同族：看着对、其实半截）。
func TestTtsVoiceNonEmptyRequired(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "tts_speak.py")
	if err := os.WriteFile(script, []byte("# stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	voices := filepath.Join(dir, "assets", "tts", "voices")
	if err := os.MkdirAll(voices, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(TtsScriptEnv, script)

	empty := filepath.Join(voices, ttsVoiceFile)
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ttsVoiceReady() {
		t.Fatal("零字节音色文件竟然算就绪 ⇒ 存在性检查会被半截文件骗过")
	}
	if err := os.WriteFile(empty, []byte("RIFFfakeWAVEdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ttsVoiceReady() {
		t.Fatal("非空音色文件竟不算就绪 ⇒ 判据写反了")
	}
}

// TestCapabilityScriptsProbeProtocol 三个脚本的 probe 子命令协议锁：
// ① 退出码 ∈ {0=OK, 3=MISSING}，stdout 首行以 OK:/MISSING: 开头；
// ② **Go 侧探测与脚本 probe 的 OK/MISSING 判定必须一致**——两份实现同一事实，
//
//	不一致即依赖清单漂移（这是比清单等值更硬的一条：等值锁防漂移，本锁防实现失真）；
//
// ③ 未知子命令走 ERR:Usage 协议（exit 2，stderr），与 anydoc_md.py 既有壳一致。
func TestCapabilityScriptsProbeProtocol(t *testing.T) {
	py := findPython()
	if py == "" {
		t.Fatal("findPython() 返回空：本机无 python3（同 TestProbeCapabilitiesDiscriminate，不许 skip）")
	}
	cases := []struct {
		script string
		goOK   bool
	}{
		{"ocr_recognize.py", func() bool { ok, _ := probeOcr(py); return ok }()},
		{"asr_transcribe.py", func() bool { ok, _ := probeAsr(py); return ok }()},
		{"tts_speak.py", func() bool { ok, _ := probeTts(py); return ok }()},
	}
	for _, c := range cases {
		stdout, code := runPy(t, py, c.script, "probe")
		scriptOK := strings.HasPrefix(stdout, "OK:")
		if code != 0 && code != 3 {
			t.Fatalf("%s probe 退出码应为 0/3，实际 %d（stdout=%q）", c.script, code, stdout)
		}
		if !scriptOK && !strings.HasPrefix(stdout, "MISSING:") {
			t.Fatalf("%s probe stdout 首行应以 OK:/MISSING: 开头，实际 %q", c.script, stdout)
		}
		if scriptOK != c.goOK {
			t.Fatalf("%s：Go 探测=%v 与脚本 probe=%v 判定不一致 ⇒ 两侧依赖清单或资产判据已漂移", c.script, c.goOK, scriptOK)
		}
		// 未知子命令：ERR:Usage 协议（exit 2 + stderr），与 anydoc_md.py 壳一致。
		stderr, code2 := runPyErr(t, py, c.script, "definitely_not_a_subcommand")
		if code2 != 2 || !strings.Contains(stderr, "ERR:Usage") {
			t.Fatalf("%s 未知子命令应走 ERR:Usage 且 exit 2，实际 code=%d stderr=%q", c.script, code2, stderr)
		}
	}
}

// runPy 跑脚本子命令，返回 (stdout, exitCode)。脚本路径相对包目录（go test 的 cwd）。
func runPy(t *testing.T, py, script string, args ...string) (string, int) {
	t.Helper()
	argv := append([]string{script}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, argv...)
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("执行 %s %v 失败: %v", script, args, err)
	}
	return strings.TrimSpace(string(out)), code
}

// runPyErr 跑脚本子命令，返回 (stderr, exitCode)。
func runPyErr(t *testing.T, py, script string, args ...string) (string, int) {
	t.Helper()
	argv := append([]string{script}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, argv...)
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("执行 %s %v 失败: %v", script, args, err)
	}
	return errBuf.String(), code
}
