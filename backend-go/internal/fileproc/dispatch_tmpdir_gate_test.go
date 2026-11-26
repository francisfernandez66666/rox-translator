// ============ dispatch_tmpdir_gate_test.go · TMPDIR 会话隔离门禁 ============
// 2026-11-26 工单动态派发批（§3.8 双项目隔离）：广州机与量化服务共用 /tmp，
// 任何客户内容落到系统 /tmp 都可能被 sweep 或别的进程看到。链路必须把临时目录钉到会话目录：
//   fpd-bridge → export TMPDIR 指向 w/.session_tmp（最外层兜底）
//   chunk_translate.py → os.environ["TMPDIR"] 指向 w/<sid>/tmp（会话级细化）
//   chunkworker/main.go 与 chunk_translate.py 的**代码**里零 /tmp 字面量（注释允许出现说明文字）。
//
// 反证腿：假源码写 TMPDIR=/tmp 必须判红。
// ============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// tmpdirReadFile 读相对本包目录的源码；读不到判红。
func tmpdirReadFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 %s 失败（判据按相对本包目录的字面路径走）：%v", rel, err)
	}
	return string(b)
}

// tmpdirStripComments 丢掉行注释（//、#、*）与 Python 三引号 docstring，只留代码。
// 必须剥 docstring：chunk_translate.py 的文件头说明里就写着"禁用系统 /tmp"，
// 拿裸文本判会把说明当成实现，下一位会直接摘掉这条锁。
func tmpdirStripComments(src string) string {
	out := make([]string, 0)
	inDoc := false
	for _, ln := range strings.Split(src, "\n") {
		s := strings.TrimSpace(ln)
		if strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, `'''`) {
			n := strings.Count(s, `"""`) + strings.Count(s, `'''`)
			if n >= 2 {
				continue // 单行三引号，整行是说明
			}
			inDoc = !inDoc
			continue
		}
		if inDoc {
			continue
		}
		if strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "*") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// tmpdirBadPathLines 返回代码里含系统 /tmp 字面量的行（剥注释后仍出现才算）。
func tmpdirBadPathLines(src string) []string {
	var hits []string
	for _, ln := range strings.Split(tmpdirStripComments(src), "\n") {
		if strings.Contains(ln, "/tmp") {
			hits = append(hits, strings.TrimSpace(ln))
		}
	}
	return hits
}

// tmpdirPySetFnBody 切 Python 的 def _set_tmpdir(...) 函数体。
func tmpdirPySetFnBody(src string) string {
	i := strings.Index(src, "def _set_tmpdir(")
	if i < 0 {
		return ""
	}
	rest := src[i:]
	if j := strings.Index(rest, "\ndef "); j >= 0 {
		return rest[:j]
	}
	return rest
}

// TestDispatchTmpDirSessionScoped 钉住 TMPDIR 两层会话隔离与代码面零系统 /tmp。
func TestDispatchTmpDirSessionScoped(t *testing.T) {
	// ① bridge 最外层兜底：export TMPDIR 指向 w/.session_tmp。
	bridge := tmpdirReadFile(t, "../../../deploy/dispatch/fpd-bridge")
	if !strings.Contains(bridge, "export TMPDIR=") {
		t.Error("fpd-bridge 没有 export TMPDIR ⇒ 会话目录之外的临时文件会落系统 /tmp")
	}
	if !strings.Contains(bridge, "w/.session_tmp") {
		t.Error("fpd-bridge 的 TMPDIR 没指向 w/.session_tmp ⇒ 隔离目录错了")
	}

	// ② Python 胶水会话级细化：os.environ["TMPDIR"] 来自 session_dir。
	py := tmpdirReadFile(t, "../../../deploy/dispatch/chunk_translate.py")
	if !strings.Contains(py, `os.environ["TMPDIR"]`) {
		t.Error("chunk_translate.py 没有 os.environ[\"TMPDIR\"] 赋值 ⇒ 会话级隔离没落地")
	}
	setFn := tmpdirPySetFnBody(py)
	if len(setFn) < 40 || !strings.Contains(setFn, "session_dir") || !strings.Contains(setFn, `"tmp"`) {
		t.Errorf("_set_tmpdir 没把 TMPDIR 钉到 session_dir/tmp：\n%s", setFn)
	}

	// ③ 代码面零系统 /tmp（注释和 docstring 允许提到它）。
	for _, c := range []struct{ label, src string }{
		{"cmd/chunkworker/main.go", tmpdirReadFile(t, "../../cmd/chunkworker/main.go")},
		{"chunk_translate.py", py},
	} {
		if hits := tmpdirBadPathLines(c.src); len(hits) != 0 {
			t.Errorf("%s 的代码里出现系统 /tmp 字面量（%s）⇒ 客户临时内容会落到共用目录",
				c.label, strings.Join(hits, "; "))
		}
	}

	// ④ 反证：假源码写 TMPDIR=/tmp 必须判红。
	fake := "TMPDIR=/tmp\n"
	if len(tmpdirBadPathLines(fake)) == 0 {
		t.Error("反证落空：`TMPDIR=/tmp` 没被抓到 ⇒ ③ 的零命中是假绿")
	}
	// 对照：只有注释/docstring 提到 /tmp 不许判红（否则锁会被自己的说明文字自伤）。
	commented := "# 禁用系统 /tmp\n\"\"\"说明里写 /tmp，不是实现\"\"\"\n"
	if hits := tmpdirBadPathLines(commented); len(hits) != 0 {
		t.Errorf("注释/docstring 里的 /tmp 被误判（%s）⇒ 下一位会摘掉这条锁", strings.Join(hits, "; "))
	}
}
