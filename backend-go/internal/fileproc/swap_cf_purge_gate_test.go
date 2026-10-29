// ============ swap_cf_purge_gate_test.go · 职责说明 ============
// CF purge 腿的**形态锁**（2026-10-29 第十一轮）。
//
// 为什么要有它：T8d（「第一枪 000、第二枪通 ⇒ 重拨会救」）的射程完全取决于两件事——
//  1. 执行体把 curl 失败读成「这一跳没走完」（旧写法 `|| echo 000` + `[ "$code" = "000" ]`
//     在真 curl 失败时读到 "000000"，重拨永不触发）；
//  2. 桩的失败形态与真 curl 同形（打 000 **且** 退出码非零；桩 `exit 0` 会让 T8d 在坏执行体上假绿）。
//
// 这两件事**各改一处**就能让 T8d 常绿 ⇒ 一条行为腿（T8d）配两条形态腿（C1/C2），任一侧退化当场红。
// =============================================
package fileproc

import (
	"strings"
	"testing"
)

// shellFuncBody 抠出 shell 函数体（`name() {` 到配对的 `}`），按花括号配平切。
//
// 括号在 `${…}` 与 JSON 字面量里都是成对的，配平不受影响；找不到即返回 ("", false)，
// 调用方按「尺子不见了」判红，**不许**当成合规。
func shellFuncBody(src, header string) (string, bool) {
	i := strings.Index(src, header)
	if i < 0 {
		return "", false
	}
	rest := src[i:]
	j := strings.Index(rest, "{")
	if j < 0 {
		return "", false
	}
	depth := 0
	for k := j; k < len(rest); k++ {
		switch rest[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[:k+1], true
			}
		}
	}
	return "", false
}

// stripShellComments 剥掉 shell 行注释（`#` 到行尾），单/双引号内的 `#` 不当注释。
//
// 为什么必须先剥：本函数体的说明注释里就写着旧写法 `|| echo 000`；不剥的话负向腿会被
// **自己的注释**喂饱（本仓老坑：注释冒充实现，反证与正向一起假红/假绿）。
func stripShellComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		inSingle, inDouble := false, false
		cut := len(line)
		for i := 0; i < len(line); i++ {
			switch line[i] {
			case '\'':
				if !inDouble {
					inSingle = !inSingle
				}
			case '"':
				if !inSingle {
					inDouble = !inDouble
				}
			case '#':
				if !inSingle && !inDouble && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
					cut = i
					i = len(line)
				}
			}
		}
		b.WriteString(line[:cut])
		b.WriteString("\n")
	}
	return b.String()
}

// cfAPIProblems 返回 cf_api 函数体的形态问题（空＝合规）。判之前先剥注释。
func cfAPIProblems(body string) []string {
	body = stripShellComments(body)
	var out []string
	if !strings.Contains(body, "|| rc=$?") {
		out = append(out, "cf_api 没把 curl 的退出码收下来（`|| rc=$?`）⇒ 真失败时读不到「这一跳没走完」")
	}
	if strings.Contains(body, "|| echo 000") {
		out = append(out, "cf_api 里还有 `|| echo 000`：真 curl 失败时 -w 已打 000，再补一个就是 000000 ⇒ 重拨永不触发")
	}
	if !strings.Contains(body, "000*") {
		out = append(out, "cf_api 的归一没按前缀认 000（`000*`）⇒ 000000 这种形态仍漏判")
	}
	return out
}

// C1：执行体形态 —— curl 退出码必须被收下来，旧的「再补一个 000」必须清零。
func TestSwapCfPurgeRetryShape(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary.sh")
	body, ok := shellFuncBody(src, "cf_api() {")
	if !ok {
		t.Fatalf("找不到 cf_api 函数体 ⇒ 这条锁此刻没有输入，是空转的")
	}
	if ps := cfAPIProblems(body); len(ps) > 0 {
		t.Fatalf("CF purge 重拨腿形态不合规：%s", strings.Join(ps, "；"))
	}
	// 反证一：旧形态必须被同一判据判死（判据有射程的自证）。
	if ps := cfAPIProblems("  curl \"${args[@]}\" \"$url\" 2>/dev/null || echo 000\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：旧形态喂进去竟然判绿")
	}
	// 反证二：空函数体必须判死（「尺子丢了」与「这次很干净」不能同形）。
	if ps := cfAPIProblems(""); len(ps) == 0 {
		t.Fatalf("判据自证不过：空函数体判绿")
	}
	// 反证三：只在注释里写齐形态、正文没有 ⇒ 必须判死（注释不算实现）。
	if ps := cfAPIProblems("# || rc=$? 000*\n  curl x\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：注释里的形态被当成实现")
	}
}

// C2：桩的失败形态 —— 两处失败分支都必须「打 000 + 非零退出」，且不许再出现 exit 0。
func TestSwapCfPurgeStubFailureShape(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary_selftest.sh")
	if n := strings.Count(src, "echo 000; exit 7"); n != 2 {
		t.Fatalf("桩的失败分支应是 2 处 `echo 000; exit 7`，实读 %d ⇒ 有一处还是 exit 0（T8d 会假绿）", n)
	}
	if strings.Contains(src, "echo 000; exit 0") {
		t.Fatalf("桩里还有 `echo 000; exit 0`：真 curl 失败是**非零退出**，这样写测不到执行体的缺陷")
	}
}

// cfTailProblems 返回 cf_purge_tail 的「阻塞」问题（空＝合规）。
// 判据只问一件事：函数体里有没有 `bad` 调用（那会把一次 purge 抖动升级成整次发版判死，
// 而此刻 web 已落位 ⇒ 留下「web 新 + bin 旧」的混合半态）。先剥注释，防说明文字喂饱负向腿。
func cfTailProblems(body string) []string {
	clean := stripShellComments(body)
	if strings.Contains(clean, "bad \"") || strings.Contains(clean, " bad ") {
		return []string{"cf_purge_tail 里出现 bad：尾随 purge 必须非阻塞（此刻已落位）"}
	}
	return nil
}

// C4：尾随 purge 非阻塞 + 接在 commit_site 之后。
func TestSwapCfPurgeTailIsNonBlocking(t *testing.T) {
	src := readProbeScript(t, "swap_web_and_binary.sh")
	body, ok := shellFuncBody(src, "cf_purge_tail() {")
	if !ok {
		t.Fatalf("找不到 cf_purge_tail 函数体 ⇒ 尾随 purge 没落地")
	}
	if ps := cfTailProblems(body); len(ps) > 0 {
		t.Fatalf("%s", strings.Join(ps, "；"))
	}
	commit := strings.Index(src, `for d in $DEPLOY_DIRS; do commit_site `)
	call := strings.LastIndex(src, "cf_purge_tail")
	if commit < 0 || call < 0 || call < commit {
		t.Fatalf("尾随 purge 没接在 commit_site 循环之后（加函数不接线＝白修）")
	}
	// 反证：带 bad 的函数体必须判死；干净函数体必须判绿。
	if ps := cfTailProblems("  bad \"x\"\n"); len(ps) == 0 {
		t.Fatalf("判据自证不过：带 bad 的尾随 purge 竟然判绿")
	}
	if ps := cfTailProblems("  echo \"CF_PURGE_TAIL=ok\"\n"); len(ps) > 0 {
		t.Fatalf("判据自证不过：干净函数体被判红")
	}
}
