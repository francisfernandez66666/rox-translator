package engine

// release_marker_gate_test.go —— 「发布件特征串」与 engine 侧常量的跨语言等值锁（★ 2026-10-18 #4 批）。
//
// 为什么要有这一条：发布脚本 `deploy/release_20261018.sh` 的 P0-B 用**二进制里的字面量**证明
// "编的是不是本棵工作树"，而这一层最容易两头都坏：
//   · 抄成**函数名** ⇒ 内联掉的函数不进名字表，编好的件被判"编错树"（本批首跑真踩：同一棵树同一个件里
//     `degenerateToFunctionWord` 命中 1、`identityDelivery` 命中 0，见 translation_guard.go 的常量注释）；
//   · 抄错成别的串 ⇒ 判据永远绿，等于没有这道闸门（"尺子丢了"和"这次很干净"长得一模一样）。
// 判据形态照抄 cmd/server/listen_systemd_test.go 的 socket activation 契约锁：**现读脚本现算**，
// 并把"这条字面量真的在运行期被引用"也一起判掉（否则常量被优化掉，脚本照样红/绿得莫名其妙）。
//
// 三枚反证（改坏任一处必须让这里变红）：
//   M1 脚本侧特征串改名   ⇒ C1 红（脚本放行一支不含本批代码的件）
//   M2 摘掉那行 grep      ⇒ C1 红（判据对象不存在）
//   M3 常量/日志在源码里消失 ⇒ C2/C3 红（判据在，载体没了 —— 恒绿闸门）

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseScriptForMarkers 现读本批发布脚本；读不到即 t.Fatal（判据对象没了要立刻出声，不能跳过）。
func releaseScriptForMarkers(t *testing.T) string {
	t.Helper()
	return releaseScriptNamed(t, "release_20261018.sh")
}

// releaseScriptNamed 现读指定发布脚本（同一条口径：靶子没了必须立刻出声）。
// ★ 新增批次脚本时**不要**改上面那个 10-18 的靶子名：改名字＝把老锁的射程悄悄挪走，
// 每个批次的契约锁各自点自己的脚本（本批＝release_20261019b.sh）。
func releaseScriptNamed(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	for i := 0; i < 6; i++ {
		p := filepath.Join(dir, "deploy", name)
		if raw, err := os.ReadFile(p); err == nil {
			return string(raw)
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("找不到 deploy/%s（本判据的靶子）", name)
	return ""
}

// engineSourceForMarkers 现读 engine 侧两个载体文件的源码，用于判"字面量真有运行期引用点"。
func engineSourceForMarkers(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	var sb strings.Builder
	for _, name := range []string{"translation_guard.go", "file.go"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读不到 %s（本判据的载体）：%v", name, err)
		}
		sb.Write(raw)
	}
	return sb.String()
}

// prefillMarkerContractProblems 是判据本体（纯函数，便于反证直接喂变异体）。
//
//	C1 脚本必须**逐字 grep** marker（`grep -a -c -F -- "<marker>"`）
//	C2 marker 必须在 engine 源码里出现（常量真被定义，不是脚本自己编的串）
//	C3 logLiteral 必须在脚下**两处**都在位：脚本的 grep 与 engine 源码 ——
//	   它才是"常量被运行期引用"的那一条腿（日志被删＝常量会被编掉）
func prefillMarkerContractProblems(script, marker, logLiteral, engineSrc string) []string {
	var bad []string
	wantGrep := `grep -a -c -F -- "` + marker + `"`
	if !strings.Contains(script, wantGrep) {
		bad = append(bad, "C1 发布脚本没逐字 grep 特征串（找："+wantGrep+"）⇒ 要么改名、要么这一行被摘了")
	}
	if !strings.Contains(engineSrc, `"`+marker+`"`) {
		bad = append(bad, "C2 特征串在 engine 源码里不存在（常量被改名/删除？脚本会在编好的件上恒红）")
	}
	if !strings.Contains(script, logLiteral) {
		bad = append(bad, "C3 发布脚本没判日志字面量（找："+logLiteral+"）")
	}
	if !strings.Contains(engineSrc, `"`+logLiteral+`"`) {
		bad = append(bad, "C3 日志字面量不在 engine 源码里（常量失去运行期引用点 ⇒ 会被编掉、特征串两向都不可信）")
	}
	return bad
}

// TestIdentityPrefillMarkerMatchesReleaseScript 跨语言契约等值锁＋三枚反证。
func TestIdentityPrefillMarkerMatchesReleaseScript(t *testing.T) {
	script := releaseScriptForMarkers(t)
	src := engineSourceForMarkers(t)
	const logLiteral = "文件翻译：无需翻译段按原文预填交付"

	if p := prefillMarkerContractProblems(script, identityPrefillMarker, logLiteral, src); len(p) != 0 {
		t.Fatalf("发布脚本与本批 engine 常量/文案已经打架：%s", strings.Join(p, "；"))
	}

	// M1：脚本侧特征串改名 ⇒ C1 必须红（否则脚本会放行一支不含本批代码的件）
	m1 := strings.Replace(script, `"`+identityPrefillMarker+`"`, `"SOMETHING_ELSE_V1"`, 1)
	if len(prefillMarkerContractProblems(m1, identityPrefillMarker, logLiteral, src)) == 0 {
		t.Fatal("M1 反证失败：脚本侧特征串改了名，C1 仍然全绿 ⇒ 这条锁没有射程")
	}

	// M2：摘掉那条 grep 行 ⇒ C1 必须红（判据对象不存在）
	m2 := strings.Replace(script, `grep -a -c -F -- "`+identityPrefillMarker+`"`, "", 1)
	if len(prefillMarkerContractProblems(m2, identityPrefillMarker, logLiteral, src)) == 0 {
		t.Fatal("M2 反证失败：摘掉 grep 行仍全绿 ⇒ 判据对着空对象判绿")
	}

	// M3a：常量在源码里消失（载体没了）⇒ C2 必须红
	srcNoConst := strings.ReplaceAll(src, identityPrefillMarker, "X")
	if len(prefillMarkerContractProblems(script, identityPrefillMarker, logLiteral, srcNoConst)) == 0 {
		t.Fatal("M3a 反证失败：源码里常量没了仍全绿 ⇒ C2 无射程")
	}

	// M3b：日志被删（常量失去运行期引用点）⇒ C3 必须红
	srcNoLog := strings.ReplaceAll(src, logLiteral, "已被删掉")
	if len(prefillMarkerContractProblems(script, identityPrefillMarker, logLiteral, srcNoLog)) == 0 {
		t.Fatal("M3b 反证失败：日志被删仍全绿 ⇒ C3 无射程（常量会被编掉而无人知）")
	}
}

// TestMinimalInstrRescueMarkerMatchesReleaseScript 图片缺陷批（deploy/release_20261019b.sh）
// 的同款跨语言契约锁＋三枚反证：本批改了 engine 的提示词档（极简指令）与 fileproc 的 *.py
// （框行墨剔除 / 无墨 identity 跳过），件里必须找得到"极简腿真被走到"的运行期字面量。
// 反证取向与 10-18 那条一致：改名/摘 grep ⇒ C1 红；常量或日志字面量少了 ⇒ C2/C3 红（恒绿闸门）。
func TestMinimalInstrRescueMarkerMatchesReleaseScript(t *testing.T) {
	script := releaseScriptNamed(t, "release_20261019b.sh")
	src := engineSourceForMarkers(t)
	const logLiteral = "文件翻译：极简指令兜底腿救回未译段"

	if p := prefillMarkerContractProblems(script, minimalInstrRescueMarker, logLiteral, src); len(p) != 0 {
		t.Fatalf("发布脚本与本批 engine 常量/文案已经打架：%s", strings.Join(p, "；"))
	}

	m1 := strings.Replace(script, `"`+minimalInstrRescueMarker+`"`, `"SOMETHING_ELSE_V1"`, 1)
	if len(prefillMarkerContractProblems(m1, minimalInstrRescueMarker, logLiteral, src)) == 0 {
		t.Fatal("M1 反证失败：脚本侧特征串改了名，C1 仍然全绿 ⇒ 这条锁没有射程")
	}
	m2 := strings.Replace(script, `grep -a -c -F -- "`+minimalInstrRescueMarker+`"`, "", 1)
	if len(prefillMarkerContractProblems(m2, minimalInstrRescueMarker, logLiteral, src)) == 0 {
		t.Fatal("M2 反证失败：摘掉 grep 行仍全绿 ⇒ 判据对着空对象判绿")
	}
	srcNoConst := strings.ReplaceAll(src, minimalInstrRescueMarker, "X")
	if len(prefillMarkerContractProblems(script, minimalInstrRescueMarker, logLiteral, srcNoConst)) == 0 {
		t.Fatal("M3a 反证失败：源码里常量没了仍全绿 ⇒ C2 无射程")
	}
	srcNoLog := strings.ReplaceAll(src, logLiteral, "已被删掉")
	if len(prefillMarkerContractProblems(script, minimalInstrRescueMarker, logLiteral, srcNoLog)) == 0 {
		t.Fatal("M3b 反证失败：日志被删仍全绿 ⇒ C3 无射程（常量会被编掉而无人知）")
	}
}
