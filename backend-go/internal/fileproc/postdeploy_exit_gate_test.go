// ============================================================================
// postdeploy_exit_gate_test.go — ★ 2026-10-10（〇-AR 第 9 波收尾）把「发版后接线复查」
// 这类脚本的**退出码接线**接进自动化闸门。
//
// 因果链（为什么本批要给它加一条锁，而不是"顺手改了就算"）：
//
//	第 9 波换件后跑 deploy/postdeploy_20261010.sh，屏幕上明明白白出了
//	`POSTDEPLOY_FAIL=1（上面有 bad 行）`，而我在外面套的 `echo $?` 回的是 **0**。
//	顺着查发现两份复查脚本（10-06／10-10）都是同一个形态：远端 heredoc 里数好了 RC、
//	也把它打进了读数行，但**远端段最后一句是 echo**（echo 恒退 0）⇒ ssh 退 0；
//	本机层又把那个 0 打进 `POSTDEPLOY_LOCAL_EXIT=…` 就结束了 ⇒ 脚本自己**永远退 0**。
//	这正是本波点名的「有脚本无调度」往下沉了一层：判据在、红也出得了，
//	但**没有任何调度方按退出码能拿到这件事**——把它挂进定时器或 CI 的人，
//	第一个读的就是 exit code，于是"现网有 FAIL"会被读成"这一步过了"。
//
// 三条设计（都和 AGENTS 里已有的口径对齐）：
//
//	① 判据抽成**纯函数** postdeployExitWiring()，仓库里的每一份复查脚本和反证用的变异副本
//	   **都走同一个函数**（§三 那条"判据级断言代替不了守卫级"——只测函数等于没测文件）。
//	② 反证必须"打在靶子上"：变异只摘那一句退出码传递，别的都不动；摘掉后必须红，
//	   不红就说明这条锁射程是空气（本批 M1/M2 两个变异体各抓一条独立腿）。
//	③ 顺带 `bash -n`：这类脚本历史上出过"文档齐全但连语法都没过"的合入
//	   （见 dispatch_scripts_gate_test.go 头部那条），而复查脚本只在发版当天跑一次，
//	   坏了要到下一次发版才发现。
//
// 本文件**只读仓库文本、只在临时目录里跑 bash -n**，绝不 ssh、绝不碰现网。
// ============================================================================
package fileproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// postdeployGlobPattern 射程＝deploy/ 下所有发版后复查脚本。
// 为什么用通配而不是写死文件名：这一族是**每波新写一份**的（10-06、10-10……），
// 写死名单等于"下一份不受保护"，而下一份八成是照这一份抄的——抄的时候把
// 最后两行抄漏正是本批抓到的缺陷形态。
const postdeployGlobPattern = "deploy/postdeploy_*.sh"

// postdeployExitWiring 判一份复查脚本有没有把**远端的失败计数**一路带到本机退出码。
// 返回的问题清单为空＝接线成立。三条腿各管一条真实的断链方式：
//
//	L1 远端段自己没 `exit "$RC"` ⇒ 远端 bash 的退出码是最后那句 echo 的 0，
//	   本机再怎么捕获都只能捕到 0（这一条是本次现网实测踩到的本体）；
//	L2 本机层没把 ssh 退码收进变量、`exit` 出去 ⇒ 脚本整体恒退 0，调度方读不到红；
//	L3 收了却没 `exit` 它（只打成一行读数）⇒ 和 L2 同形，但更隐蔽，
//	   因为屏幕上两个数都对（`POSTDEPLOY_LOCAL_EXIT=1`），只有退出码是骗人的。
//
// 判据一律按**代码行**判，先剥整行注释：这一族脚本的注释里大量出现
// `exit "$RC"`、`POSTDEPLOY_LOCAL_EXIT` 这类字样（就是解释这个缺陷的段落），
// 不剥注释会把"写明了坑"读成"踩了坑"（同仓反复点的注释/代码同形坑）。
func postdeployExitWiring(rel string, content string) []string {
	var problems []string
	code := strings.Join(stripShellCommentLines(content), "\n")

	// 远端 heredoc 段＝`<<'REMOTE_EOF'` 到独立成行的 `REMOTE_EOF` 之间。
	// 找不到这对标记就**拒绝判**（记一条问题），不静默放行——改名式破坏（换成
	// `<<'REMOTE_SCRIPT'`）会让下面两条腿一行都不跑，而那正是这条锁要防的事。
	start := strings.Index(code, "<<'REMOTE_EOF'")
	if start < 0 {
		return append(problems, rel+": 认不出远端段起点（<<'REMOTE_EOF'）⇒ 这一份不在射程，判未验证")
	}
	rest := code[start:]
	end := strings.Index(rest, "\nREMOTE_EOF")
	if end < 0 {
		return append(problems, rel+": 远端段没有收尾的 REMOTE_EOF ⇒  heredoc 没收住")
	}
	remoteBody := rest[:end]
	afterRemote := rest[end:]

	if !strings.Contains(remoteBody, `exit "$RC"`) {
		problems = append(problems, rel+": 远端段没把失败计数 exit 出去（L1）⇒ 远端最后一句 echo 恒退 0，本机捕不到红")
	}
	if !strings.Contains(afterRemote, "SSH_RC=$?") {
		problems = append(problems, rel+": 本机层没把 ssh 退码收进变量（L2）⇒ 捕不到就是传不到")
	}
	if !strings.Contains(afterRemote, `exit "$SSH_RC"`) {
		problems = append(problems, rel+": 本机层收了 SSH_RC 却没 exit 它（L3）⇒ 屏幕上读数对、退出码恒 0，调度方按码判就全绿")
	}
	return problems
}

// TestPostdeployScriptsPropagateRemoteExit 对仓库里每一份发版后复查脚本跑同一把尺子。
// 一条都不许 skip：找不到文件即 Fatal（"这一族没了"本身就是这条锁该出声的事）。
func TestPostdeployScriptsPropagateRemoteExit(t *testing.T) {
	repo := findDispatchRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(repo, postdeployGlobPattern))
	if err != nil {
		t.Fatalf("glob %s 失败：%v", postdeployGlobPattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("%s 下一份复查脚本都没有 ⇒ 要么路径变了，要么这一族被删了；两者都要人来改这条锁，不许静默通过", postdeployGlobPattern)
	}

	var problems []string
	for _, p := range matches {
		rel, e := filepath.Rel(repo, p)
		if e != nil {
			t.Fatalf("算相对路径失败：%v", e)
		}
		rel = filepath.ToSlash(rel)
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatalf("读 %s 失败：%v", rel, e)
		}
		// ③ 语法面：bash -n 过不了就无需谈退出码（这一族只在发版当天跑一次，
		//    坏到下一次发版才发现是本闸门的射程）。
		if out, e := exec.Command("bash", "-n", p).CombinedOutput(); e != nil {
			t.Errorf("%s 连 bash -n 都没过：%v\n%s", rel, e, out)
		}
		problems = append(problems, postdeployExitWiring(rel, string(b))...)
	}
	if len(problems) > 0 {
		t.Errorf("发版后复查脚本的退出码接线不成（每一行都是一条独立断链）：\n  %s\n"+
			"要改的形态：远端段末尾 `exit \"$RC\"` ⇒ 本机 `SSH_RC=$?` ⇒ 末尾 `exit \"$SSH_RC\"`",
			strings.Join(problems, "\n  "))
	}
	t.Logf("射程内复查脚本 %d 份，退出码接线全部成立", len(matches))
}

// TestPostdeployExitGateCounterproof 反证两条（都在临时副本上跑，**仓库原件一字不动**）：
//
//	M1 摘掉远端段的 `exit "$RC"`（＝本次现网实测的那一份的真实形态）⇒ 必须被 L1 抓到；
//	M2 保留捕获、只摘本机那句 `exit "$SSH_RC"`（＝"读了但没往外传"那一族）⇒ 必须被 L3 抓到。
//
//	两条都必须**让同一把尺子出声**：判据级断言代替不了守卫级（AGENTS §三），
//	所以这里调的是 postdeployExitWiring 本体，不是复制一份字符串比较。
func TestPostdeployExitGateCounterproof(t *testing.T) {
	cases := []struct {
		name string
		old  string
		new  string
		want string // 期望问题行里必须含的关键词（抓不到靶子就 Fatal）
	}{
		{
			name: "M1 远端段不 exit（最后一句 echo 恒退 0＝10-10 实测形态）",
			old:  "exit \"$RC\"\nREMOTE_EOF",
			new:  "REMOTE_EOF",
			want: "L1",
		},
		{
			name: "M2 本机只读出不外传（屏幕对、退出码骗人）",
			// 靶子按**整行顶格**写：那两句 `exit` 都在文件顶层（不在 if/函数体里），
			// 上一版这里带了个前导 Tab 结果命中 0 次——反证不是"打偏了"，是**一行都没打掉**，
			// 于是这条腿会被"靶子串出现 0 次"那条 Fatal 抓出来（而不是假绿），属于自伤不是产品问题。
			old:  "\nexit \"$SSH_RC\"",
			new:  "\ntrue",
			want: "L3",
		},
	}
	repo := findDispatchRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(repo, postdeployGlobPattern))
	if err != nil || len(matches) == 0 {
		t.Fatalf("没有射程内脚本可反证（%v，命中 %d 份）", err, len(matches))
	}
	// **每一份**都要过反证，不是只拿第一份演一次：这一族是"下一份照上一份抄"长出来的，
	// 只测一份等于给其余各份发了免检证（本批抓到的恰恰是两份都缺同一条腿）。
	for _, m := range matches {
		srcRel, err := filepath.Rel(repo, m)
		if err != nil {
			t.Fatalf("算相对路径失败：%v", err)
		}
		// readRepoText 收的是**仓库相对路径**（它自己拼 root），把绝对路径塞进去会得到
		// 一个"root／root／文件"的双层路径而读不到——那种红和这条锁要测的事毫无关系。
		rel := filepath.ToSlash(srcRel)
		src := readRepoText(t, repo, rel)
		// 对照腿：原件在这把尺子下必须**全清**，否则变异体的红说明不了任何事。
		if base := postdeployExitWiring(rel, src); len(base) != 0 {
			t.Fatalf("%s 原件在这条尺子下就不成（%v）⇒ 对照腿失效", rel, base)
		}
		for _, c := range cases {
			c := c
			t.Run(rel+"/"+c.name, func(t *testing.T) {
				n := strings.Count(src, c.old)
				if n != 1 {
					t.Fatalf("靶子串在 %s 里出现 %d 次（期望恰 1 次）⇒ 反证打不到那一处或打错地方：%q", rel, n, c.old)
				}
				variant := strings.Replace(src, c.old, c.new, 1)
				// 摘完要是连语法都坏了，红就落在无关原因上（仿 §三"反证打在靶子上"），
				// 所以这一句不是走过场：它证明变异体仍是"跑得动的脚本、只坏了一处接线"。
				dir := t.TempDir()
				p := filepath.Join(dir, "variant.sh")
				if err := os.WriteFile(p, []byte(variant), 0o644); err != nil {
					t.Fatalf("写变异副本失败：%v", err)
				}
				if out, err := exec.Command("bash", "-n", p).CombinedOutput(); err != nil {
					t.Fatalf("变异体连 bash -n 都没过（不是有效反证）：%v\n%s", err, out)
				}
				problems := postdeployExitWiring("variant.sh", variant)
				var joined string
				for _, q := range problems {
					joined += q + "\n"
				}
				if len(problems) == 0 {
					t.Fatalf("摘掉『%s』之后这条锁**一行问题都没出** ⇒ 射程是空气，绿灯是假的", c.old)
				}
				if !strings.Contains(joined, c.want) {
					t.Fatalf("问题清单没点到预期的那条腿 %s（实际：\n%s）⇒ 抓到的是别的事，反证无效", c.want, joined)
				}
			})
		}
	}
}
