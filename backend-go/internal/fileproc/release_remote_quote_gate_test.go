// ============================================================================
// release_remote_quote_gate_test.go — ★ 2026-11-03（CF 凭据管理面批发版当场踩到）
// 把「批发版驱动脚本里跨机读数腿的引号形态」接进自动化闸门。
//
// 因果链（为什么这条锁必须存在，而不是"我改好了就算"）：
//
//	本批发版跑 deploy/release_20261102_cftoken.sh，前五段判据全绿
//	（SWAP_ALL_OK=1、P99 0.006s、新路由匿名 401、postdeploy 远端段＋公网段同轮全绿），
//	而末段「6/6 交付面读数」屏幕上只留下一行
//	bash: -c: line 6: syntax error near unexpected token `newline'
//	⇒ 两站入口／侧车份数／脏文件数／单元状态／投影文件在不在——**一项都没读出来**，
//	而发版判据照样 RELEASE_CF_ALL_OK=1（这一段落按设计不是判据，只是给下一位的坐标）。
//	这正是本仓反复点名的那一族：**读不到的读数与"一切都好"长得一模一样**。
//
//	崩因不是"多行单引号写法"本身（仓里 6 份历史批脚本都那么写、从未崩），
//	而是回退档写了个**没加引号的裸 <**：
//	    echo "  cf_token_file=$(ls -l … 2>/dev/null || echo <未配置>)"
//	`$( … )` 里 `|| echo` 后面第一个非空白字符是 `<` ⇒ bash 把它当重定向记号，
//	在**解析期**就退 `syntax error near unexpected token 'newline'`，
//	command substitution 收成空串，整段后续读数不再执行。
//
// ★ 为什么 `bash -n` 兜不住（本文件 G4 就是这条的机械证明，不是嘴上说说）：
//
//	那段 payload 在脚本里只是一个**单引号字符串的字面量**，`bash -n` 不解析字符串内部，
//	实测对坏形态退 **0**。所以这一族必须有一条按文本判的锁——
//	而它只在发版当天跑一次，坏了要到下一次发版才发现（同 dispatch_scripts_gate 那条理由）。
//
// 五条腿（G1–G5），三条纪律与仓内一致：
//
//	① 判据抽成**纯函数** remoteFallbackRedirectProblems()，真脚本与变异体喂**同一个函数**；
//	② 反向判据**先剥整行注释**：本批把事故写法原样写进了说明注释里，
//	   不剥注释就是把"记下了坑"读成"踩了坑"（G5 专测这条腿在位）；
//	③ 每条负向都要配正对照：加引号的合法写法必须放行（G3），
//	   否则下一次没人敢在批脚本里写读数腿，锁的归宿就是被摘掉。
//
// 本文件只读仓库文本，运行语法检查与假 ssh 分支，不访问现网。
// ============================================================================
package fileproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// releaseScriptsGlob 射程＝deploy/ 下**所有**批发版驱动脚本。
// 用通配而不是写死本批那一份：这一族是每批新写一份、且照上一份抄的
// （AGENTS §一·9／§三都点过：逐批手抄的脚本，抄一次就多一次抄错的机会）。
const releaseScriptsGlob = "deploy/release_*.sh"

// releaseScriptsMinCount 射程下界：现存 29 份，下界取 10。
// 判据是"文件数远低于下界 ⇒ glob 漂了、闸门已失明"，不是"清单变了"
// （同 §一·14 那两个下限的口径：探测器不是清单长度的镜像，别贴脸写死）。
const releaseScriptsMinCount = 10

// unquotedRedirectFallbackRe 目标形态＝`|| echo <…>`（`<` 前没有引号）。
// 加引号的 `|| echo '<未配置>'`、ASCII 的 `|| echo NOT_SET` 都不该命中：
// 那条合法的出路正是本次的修法，判据必须放行它，否则等于禁止写读数腿。
var unquotedRedirectFallbackRe = regexp.MustCompile(`\|\|[[:space:]]*echo[[:space:]]+<`)

// remoteFallbackRedirectProblems 判一份脚本有没有"会当场打断跨机读数的回退写法"。
// 返回空＝干净。逐行判、先剥整行注释（stripShellCommentLines 保留行号，命中才指得准），
// 报的字符串必须**自带文件名与行号**——发版屏幕上指不到是哪一条的判据等于没有判据。
func remoteFallbackRedirectProblems(rel string, content string) []string {
	var problems []string
	for i, line := range stripShellCommentLines(content) {
		if !unquotedRedirectFallbackRe.MatchString(line) {
			continue
		}
		problems = append(problems, rel+":"+strconv.Itoa(i+1)+
			" 的 `|| echo <…>` 是裸重定向记号，bash 在解析期就退 syntax error、"+
			"整个跨机读数段被打断而外层脚本照常成功 ⇒ 未命中档一律写成带引号的字面量（`|| echo '<未配置>'`）"+
			"或 ASCII 哨兵（`|| echo NOT_SET`）；本批实测崩在 deploy/release_20261102_cftoken.sh 的 6/6 读数段")
	}
	return problems
}

// 截取真实换源分支，以假 ssh 验证跳过不冒充执行成功。
func TestGuidedReleaseSkippedSwapDoesNotClaimSuccess(t *testing.T) {
	root := findDispatchRepoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, "deploy/release_20261106_guidedchat.sh"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, `log "2/6 换源换件`)
	end := strings.Index(src, "# ---- 3. 手册 PDF 铺库")
	if start < 0 || end <= start {
		t.Fatal("换源分支边界未找到")
	}
	for _, skip := range []string{"0", "1"} {
		t.Run(skip, func(t *testing.T) {
			setup := "set -eu\nPORT=1; HOST=stub; TAR_SHA=tar; BIN_SHA=bin\nSKIP_SWAP=" + skip + "\n" +
				"log(){ :; }; note(){ printf '%s\\n' \"$*\"; }; ok(){ printf '%s\\n' \"$*\"; }; bad(){ exit 9; }\n" +
				"ssh(){ printf 'SSH_CALLED\\nSWAP_ALL_OK=1\\nCF_PURGE=ok\\n'; }\n"
			out, err := exec.Command("bash", "-c", setup+src[start:end]).CombinedOutput()
			if err != nil {
				t.Fatalf("换源分支执行失败：%v\n%s", err, out)
			}
			text := string(out)
			if skip == "1" {
				if strings.Contains(text, "SWAP_ALL_OK=1") || strings.Contains(text, "SSH_CALLED") || !strings.Contains(text, "SKIP_SWAP=1") {
					t.Fatalf("跳过换源不得报告全绿或调用 ssh：%s", text)
				}
			} else if !strings.Contains(text, "SWAP_ALL_OK=1") || !strings.Contains(text, "SSH_CALLED") {
				t.Fatalf("真实执行分支必须消费 ssh 成功读数：%s", text)
			}
		})
	}
}

// TestReleaseScriptsHaveNoUnquotedRedirectFallback G1＋G2：射程自证 ＋ 全集判据。
// 射程腿单独断言（下限＋逐文件真实存在），因为"扫到 0 个文件"会让下面那条判据空转。
func TestReleaseScriptsHaveNoUnquotedRedirectFallback(t *testing.T) {
	root := findDispatchRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, releaseScriptsGlob))
	if err != nil {
		t.Fatalf("glob %s 失败：%v", releaseScriptsGlob, err)
	}
	if len(matches) < releaseScriptsMinCount {
		t.Fatalf("%s 下只扫到 %d 份（下界 %d）⇒ 要么路径漂了，要么这一族被删了；"+
			"两者都得人来改这条锁，不许静默绿灯", releaseScriptsGlob, len(matches), releaseScriptsMinCount)
	}
	var problems []string
	for _, m := range matches {
		rel, err := filepath.Rel(root, m)
		if err != nil {
			t.Fatalf("算 %s 的仓库相对路径失败：%v", m, err)
		}
		b, err := os.ReadFile(m)
		if err != nil {
			// 名单是从 glob 现读的，读不到＝刚被改名/删除，也是射程问题，不是"跳过这一条"
			t.Fatalf("glob 命中但读不到 %s：%v ⇒ 闸门锚点漂了", rel, err)
		}
		problems = append(problems, remoteFallbackRedirectProblems(filepath.ToSlash(rel), string(b))...)
	}
	if len(problems) > 0 {
		t.Fatalf("批发版脚本里有会打断跨机读数的回退写法（本批实测踩过一次，屏幕上只剩一行英文语法错）：\n%s",
			strings.Join(problems, "\n"))
	}
	t.Logf("射程＝%d 份 deploy/release_*.sh，全部无裸 < 回退", len(matches))
}

// TestReleaseScriptsParseWithDashN G3：每份批脚本必须过 `bash -n`。
// 这条与上一条**互补而不是重复**：bash -n 看得见脚本层的语法错，
// 看不见字符串内部的 payload（下一条例证专门把这一维钉死）。
// ⚠️ 这里没有 python／网络依赖，本机直接跑。
func TestReleaseScriptsParseWithDashN(t *testing.T) {
	root := findDispatchRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, releaseScriptsGlob))
	if err != nil || len(matches) == 0 {
		t.Fatalf("glob %s 无结果（err=%v）⇒ 射程没了，本闸失明", releaseScriptsGlob, err)
	}
	for _, m := range matches {
		rel, _ := filepath.Rel(root, m)
		out, err := exec.Command("bash", "-n", m).CombinedOutput()
		if err != nil {
			t.Fatalf("bash -n %s 判红：%v\n%s", filepath.ToSlash(rel), err, string(out))
		}
	}
	t.Logf("bash -n 全过：%d 份", len(matches))
}

// TestReleaseGateProvesBashDashNHasNoRangeHere ★ 必要性自证（这条锁存在的理由本身）。
//
//	对"会打断跨机读数"那一形态，`bash -n` 必须**退 0**——
//	因为那段 payload 在脚本里只是单引号字符串的字面量，bash 不解析字符串内部。
//	于是本闸不是 bash -n 的重复；反过来，**哪天 bash -n 能看见它了，这条判红**，
//	提示"两条可以合并"，而不是让这条锁无声地变成冗余。
//
// 同一条合成件还要被文本判据判红（否则"必要性"是用另一套假设证的）。
func TestReleaseGateProvesBashDashNHasNoRangeHere(t *testing.T) {
	broken := "#!/usr/bin/env bash\nset -uo pipefail\nssh host 'echo \"  f=$(ls -l /nope 2>/dev/null || echo <未配置>)\"'\n"
	problems := remoteFallbackRedirectProblems("synthetic_broken.sh", broken)
	if len(problems) == 0 {
		t.Fatal("合成件里的 `|| echo <未配置>` 没被判出 ⇒ 本闸射程为空气")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "broken_release.sh")
	if err := os.WriteFile(p, []byte(broken), 0o644); err != nil {
		t.Fatalf("写合成件失败：%v", err)
	}
	out, err := exec.Command("bash", "-n", p).CombinedOutput()
	if err == nil {
		// 期望路径：bash -n 退 0（看不见字符串内部）⇒ 文本判据是唯一能抓住它的地方
		t.Logf("实证在位：bash -n 对坏形态退 0（输出=%q），所以这一族必须由本闸按文本判", strings.TrimSpace(string(out)))
		return
	}
	t.Fatalf("bash -n 现在能看见这种 payload 了（%v\n%s）⇒ 本闸的必要性前提变了，"+
		"要么把它并进 bash -n 那条腿、要么在注释里写清还剩哪一维它看不见——不许留着当冗余闸门",
		err, string(out))
}

// TestReleaseGateJudgeCounterEvidence G4＋G5：判据自身的反证与正对照。
//
//	反证 A：崩过那一行**必须**被单行判红，且报的行号指向那一行；
//	反证 B：修法那两种形态**必须**放行（判红合法写法＝下次没人敢写读数腿，锁的归宿是被摘）；
//	反证 C：同一行写成注释**必须**不判红（剥注释这条腿在位），
//	        且紧接一条正对照：同一行不带注释时必须判红——
//	        两半一起判才分得清"剥注释生效"和"判据根本不认识这个形态"。
func TestReleaseGateJudgeCounterEvidence(t *testing.T) {
	bad := `  echo "  cf_token_file=$(ls -l /opt/translator/data/cf_token 2>/dev/null || echo <未配置>)"`
	got := remoteFallbackRedirectProblems("x.sh", "line one\n"+bad+"\n")
	if len(got) != 1 {
		t.Fatalf("反证 A：坏形态应判 1 条，实得 %d 条 ⇒ 判据射程不在这一维：%v", len(got), got)
	}
	if !strings.Contains(got[0], "x.sh:2") {
		t.Fatalf("反证 A：必须点名行号（下一位要能在发版屏幕上找到那一行），实得=%s", got[0])
	}
	for _, good := range []string{
		`printf "  cf_token_file=%s\n" "$(ls -l /x 2>/dev/null || echo NOT_SET)"`,
		`echo "  cf_token_file=$(ls -l /x 2>/dev/null || echo '<未配置>')"`,
	} {
		if p := remoteFallbackRedirectProblems("x.sh", good); len(p) != 0 {
			t.Fatalf("反证 B：合法写法被判红 ⇒ 判据过宽，会把这一族读数腿整体禁掉：\n%s", strings.Join(p, "\n"))
		}
	}
	commented := "# 事故记录：`|| echo <未配置>` 会被解析期打断\n" + bad + "\n"
	// 剥注释这条腿：先只喂注释那一行，必须干净
	if p := remoteFallbackRedirectProblems("x.sh", commented); len(p) != 1 {
		t.Fatalf("反证 C：注释那一行不该算（判据先剥注释），而代码那一行必须算 ⇒ 实得 %d 条：%v", len(p), p)
	}
	if p := remoteFallbackRedirectProblems("x.sh", "# 事故记录：`|| echo <未配置>` 会被解析期打断\n"); len(p) != 0 {
		t.Fatalf("反证 C 正对照半边：只有注释时必须干净，实得=%v", p)
	}
}
