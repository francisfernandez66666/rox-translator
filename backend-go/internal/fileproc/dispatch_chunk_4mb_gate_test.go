// ============ dispatch_chunk_4mb_gate_test.go · 块链路绕开 4MiB stdout 门禁 ============
// 2026-11-26 工单动态派发批：subprocess 的 stdout 被 limitedBuffer 截在 4MiB（见 subprocess.go），
// 大件文本若走 stdout 回传会被静默吞尾。块链路必须改成"产物落文件 + outputs 回拉"：
//   extract → texts.json，translate → result.zip；Go 侧 DispatchChunkRun 里不许再出现
//   limitedBuffer，也不许从 stdout 读产物。
//
// 本文件用源码形态断言（不真跑 Python worker）：
//   ① chunk_translate.py 的 extract/translate 函数体写文件、不写 stdout 数据；
//   ② dispatch_chunk_run.go 的 DispatchChunkRun 函数体不含 limitedBuffer、两腿用 outputs。
//
// 反证腿：假源码里出现 `print(json.dumps(texts))` 形态必须判红。
// ============================================
package fileproc

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// chunk4ReadFile 读相对本包目录的源码/脚本；读不到判红，不许退成"没读到所以干净"。
func chunk4ReadFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走）：%v", rel, err)
	}
	return string(b)
}

// chunk4PyFuncBody 从 Python 源码里切一个顶层 def 的函数体（到下一个顶层 def 为止）。
// 切不到返回空串，调用方据此判红，不能当"干净"用。
func chunk4PyFuncBody(src, sig string) string {
	i := strings.Index(src, sig)
	if i < 0 {
		return ""
	}
	rest := src[i+len(sig):]
	if j := strings.Index(rest, "\ndef "); j >= 0 {
		return rest[:j]
	}
	if j := strings.Index(rest, "\nif __name__"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// chunk4GoFuncBody 从 Go 源码里按大括号配对切一个函数体（含外层大括号）。
// 只数字符不解析字符串：目标函数体里没有含大括号的字符串字面量，够用。
func chunk4GoFuncBody(src, sig string) string {
	i := strings.Index(src, sig)
	if i < 0 {
		return ""
	}
	brace := strings.Index(src[i:], "{")
	if brace < 0 {
		return ""
	}
	start := i + brace
	depth := 0
	for k := start; k < len(src); k++ {
		switch src[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : k+1]
			}
		}
	}
	return ""
}

// chunk4StdoutDataRe 认"把数据序列化后打到 stdout"的形态（不是状态日志 print("CHUNK_...")）。
var chunk4StdoutDataRe = regexp.MustCompile(`print\s*\(\s*json\.dumps\s*\(`)

// chunk4WritesFile 判函数体是否把产物写到指定文件名（Python 的 `out = session / "name"` / `zpath = ... / "name"`）。
func chunk4WritesFile(body, name string) bool {
	return strings.Contains(body, `"`+name+`"`) &&
		(strings.Contains(body, "write_text(") || strings.Contains(body, "write_bytes(") ||
			strings.Contains(body, "ZipFile("))
}

// chunk4ChunkRunOK 判 DispatchChunkRun 函数体是否走 outputs、且不含有限缓冲stdout。
func chunk4ChunkRunOK(body string) bool {
	if len(body) < 200 {
		return false
	}
	if strings.Contains(body, "limitedBuffer") {
		return false
	}
	if !strings.Contains(body, "DispatchRun(") {
		return false
	}
	if !strings.Contains(body, `"texts.json": req.TextsOut`) || !strings.Contains(body, `"result.zip": req.OutputZip`) {
		return false
	}
	return true
}

// TestDispatchChunkAvoids4MiBStdout 钉住块链路的产物通道：落文件 + outputs，不走 stdout。
func TestDispatchChunkAvoids4MiBStdout(t *testing.T) {
	// ① Python 胶水：extract 写 texts.json，translate 写 result.zip，且两者都不往 stdout 打数据。
	py := chunk4ReadFile(t, "../../../deploy/dispatch/chunk_translate.py")
	extractBody := chunk4PyFuncBody(py, "def cmd_extract(")
	translateBody := chunk4PyFuncBody(py, "def cmd_translate(")
	if len(extractBody) < 100 || len(translateBody) < 100 {
		t.Fatalf("切函数体失败：extract=%d translate=%d 字节（判据失明，不算绿）", len(extractBody), len(translateBody))
	}
	if !chunk4WritesFile(extractBody, "texts.json") {
		t.Error("cmd_extract 没把产物写到 texts.json ⇒ 文本段会被 4MiB stdout 截断")
	}
	if !chunk4WritesFile(translateBody, "result.zip") {
		t.Error("cmd_translate 没把产物打成 result.zip ⇒ 块译文会被 4MiB stdout 截断")
	}
	for _, c := range []struct{ label, body string }{{"extract", extractBody}, {"translate", translateBody}} {
		if chunk4StdoutDataRe.MatchString(c.body) {
			t.Errorf("%s 把数据序列化打到 stdout ⇒ 超 4MiB 静默吞尾", c.label)
		}
	}

	// ② 反证：假源码里出现 print(json.dumps(texts)) 必须判红。
	fake := strings.Replace(extractBody,
		`print("CHUNK_EXTRACT ok=1 texts=%d out=%s" % (len(texts), out.name))`,
		`print(json.dumps(texts))`, 1)
	if fake == extractBody {
		t.Fatal("反证构造失败：extract 体里没有那条状态日志锚点")
	}
	if !chunk4StdoutDataRe.MatchString(fake) {
		t.Error("反证落空：`print(json.dumps(texts))` 形态没被抓到 ⇒ ① 的 stdout 负向是假绿")
	}

	// ③ Go 侧：DispatchChunkRun 不含 limitedBuffer，两腿产物都声明为 outputs（回拉落点）。
	goSrc := chunk4ReadFile(t, "dispatch_chunk_run.go")
	runBody := chunk4GoFuncBody(goSrc, "func DispatchChunkRun(")
	if !chunk4ChunkRunOK(runBody) {
		t.Error("DispatchChunkRun 没通过「无 limitedBuffer + 两腿 outputs 回拉」门禁 ⇒ 块链路可能退回 stdout 通道")
	}

	// ④ 反证 2：把 limitedBuffer 塞回函数体，判据必须判红。
	withBuf := strings.Replace(runBody, "var out ChunkRunResult", "var out ChunkRunResult\n\tvar lb limitedBuffer", 1)
	if withBuf == runBody {
		t.Fatal("反证 2 构造失败：函数体里没有 `var out ChunkRunResult` 锚点")
	}
	if chunk4ChunkRunOK(withBuf) {
		t.Error("反证 2 落空：塞回 limitedBuffer 仍判通过 ⇒ ③ 抓不住退化")
	}
}
