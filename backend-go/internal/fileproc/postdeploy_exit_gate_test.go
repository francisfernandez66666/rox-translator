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
	"fmt"
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

// postdeployAssistFaceJudgement 判一份**现役**复查脚本的「挂件面」判据是不是活的形态。
//
// 为什么这条腿要存在（2026-11-21 发版实测）：
//
//	挂件自 2026-10-31 起整段收进主服务（Caddy 的 /assist-api/* 落 8787），独立的
//	ai-assist.service 与 127.0.0.1:8790 自此不存在。而 postdeploy_20261012.sh 的 A 段
//	仍按那两个对象判 ⇒ **每次发版恒两条假红**（实测 `unit ai-assist = inactive` ＋
//	`挂件 /health = 000000`，其中 000000 还是 `|| echo 000` 与 curl 的 000 拼出来的）。
//	假红的代价不是"多看一眼"：真回归混在同形红里没人分得清（AGENTS §三「判据比实现先坏」）。
//
// 三条腿各管一种"判了不存在的对象"的写法（先剥整行注释——说明回滚位的注释里
// 必然写着 ai-assist 与 8790，不剥就会把"写明了退役"读成"还在按它判"）：
//
//	L4a 还在判已退役的 ai-assist 单元；
//	L4b 还在探 127.0.0.1:8790 的 /health；
//	L4c 两条现役判据缺一（主服务 /api/health 的 assist 段 ＋ 访客端点 /assist-api/…）——
//	    只删旧判据不补新判据，等于把"挂件面没验"写成绿。
func postdeployAssistFaceJudgement(rel string, content string) []string {
	var problems []string
	code := strings.Join(stripShellCommentLines(content), "\n")
	if strings.Contains(code, "ai-assist") {
		problems = append(problems, rel+": 还在判已退役的 ai-assist 单元（L4a）⇒ 那个单元自 2026-10-31 起不存在，这条恒假红")
	}
	if strings.Contains(code, "8790") {
		problems = append(problems, rel+": 还在探 127.0.0.1:8790（L4b）⇒ 挂件已收进主服务，这个端口上没有服务")
	}
	// 现役形态的两条锚：health 的 assist 段（判装配态）＋ /assist-api 访客端点（判真可达）。
	// 刻意按**两处各自**判：只留 health 那条会漏掉"分发器没接上"（health 是装配态，不是路由面），
	// 只留 greeting 那条会漏掉"引擎没装配、端点回兜底"。
	if !strings.Contains(code, "/api/health") || !strings.Contains(code, `"assist"`) {
		problems = append(problems, rel+": 没判主服务 /api/health 的 assist 段（L4c）⇒ 挂件装配态没人看")
	}
	if !strings.Contains(code, "/assist-api/") {
		problems = append(problems, rel+": 没判 /assist-api/ 访客端点（L4c）⇒ 分发链断没断没人看")
	}
	return problems
}

// postdeploy000DoublingSites 找「这一跳没走完」被拼成两个 000 的写法：
// `curl -w '%{http_code}' … || echo 000`。
//
// 真身（2026-11-21 发版实测的那一条 BAD）：curl 连不上时**自己就回 000**，`|| echo 000` 再补一个
// ⇒ 屏幕上读到 `挂件 /health = 000000`。这不只是"读数不准"：同一个失败被显示成一种谁也没见过的形态，
// 于是判据表里那一行永远对不上，也没人能一眼说出它是"一次都没拨通"还是"拨了两次"。
// 口径（同 AGENTS「这一跳没走完必须与判据不合格不同形」）：失败归一化只有一个出口（脚本里的 `http_code`）。
//
// ★ 射程刻意只认 `|| echo 000` 这一种：`|| echo CURL_FAIL` 是**正确**写法（哨兵与真实状态码不同形），
// 连带判红等于逼人拆掉一条好读数（锁的归宿会变成被摘掉）。
//
// 先剥整行注释：事故写法必然抄在注释里当记录，不剥会把"记下了坑"读成"踩了坑"。
func postdeploy000DoublingSites(rel string, content string) []string {
	var problems []string
	for i, line := range stripShellCommentLines(content) {
		if !strings.Contains(line, "%{http_code}") || !strings.Contains(line, "|| echo") {
			continue
		}
		if !strings.Contains(line, "|| echo 000") && !strings.Contains(line, `|| echo "000"`) &&
			!strings.Contains(line, `|| echo '000'`) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s:%d 把 curl 的 000 又拼了一个 ⇒ 失败会显示成 000000；"+
			"改走 http_code 助手（单出口归一）", rel, i+1))
	}
	return problems
}

// TestPostdeployStandingScriptJudgesLiveAssistFace 只判**现役那一份**（文件名日期最大者）。
// 为什么不是 glob 全集：10-06／10-10 那两份是历史批次的取证记录，它们按当时的形态判是**正确**的；
// 让锁去判历史件只会逼人删历史或改历史。而"现役那一份"是可派生的（这一族按批次日期命名、glob 有序），
// 不写死文件名——写死会让下一份 `postdeploy_2027xxxx.sh` 天然免检，正是这一族最容易复发的地方。
func TestPostdeployStandingScriptJudgesLiveAssistFace(t *testing.T) {
	repo := findDispatchRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(repo, postdeployGlobPattern))
	if err != nil || len(matches) == 0 {
		t.Fatalf("没有射程内脚本（%v，命中 %d 份）", err, len(matches))
	}
	newest := matches[len(matches)-1] // filepath.Glob 返回**已排序**结果，末位即最新批次
	rel, err := filepath.Rel(repo, newest)
	if err != nil {
		t.Fatalf("算相对路径失败：%v", err)
	}
	rel = filepath.ToSlash(rel)
	src := readRepoText(t, repo, rel)
	if problems := postdeployAssistFaceJudgement(rel, src); len(problems) > 0 {
		t.Errorf("现役复查脚本 %s 的挂件面判据不是活的形态：\n  %s\n"+
			"要的形态：删掉 ai-assist 单元与 8790 两条，改判主服务 /api/health 的 assist 段 ＋ /assist-api/ 访客端点",
			rel, strings.Join(problems, "\n  "))
	}
	if problems := postdeploy000DoublingSites(rel, src); len(problems) > 0 {
		t.Errorf("现役复查脚本 %s 有「000 拼接」读数（失败会被显示成 000000）：\n  %s",
			rel, strings.Join(problems, "\n  "))
	}
	// 反证：把正常写法换回拼接形态（事故现场那一行），必须被同一把尺子抓到。
	// 变异是**追加**一行，不动原件——它证明的是"这个判据真认得那种写法"，
	// 而不是"文件里恰好没有那几个字"。
	dbl := src + "\nX=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:1/x || echo 000)\n"
	if problems := postdeploy000DoublingSites(rel, dbl); len(problems) == 0 {
		t.Fatalf("反证无效：拼接形态一行问题都没出 ⇒ 这条锁没有射程")
	}
	// 反证：把现役脚本换回退役形态（补一条 ai-assist 判据、摘掉现役两条），必须被同一把尺子抓到。
	mutant := src
	mutant += "\nfor u in ai-assist; do systemctl is-active \"$u\"; done\n"
	mutant = strings.ReplaceAll(mutant, "/assist-api/", "/legacy-assist/")
	if problems := postdeployAssistFaceJudgement(rel, mutant); len(problems) == 0 {
		t.Fatalf("反证无效：换回退役形态之后这条锁一行问题都没出 ⇒ 射程是空气")
	}
	t.Logf("现役复查脚本 %s 的挂件面判据已按现役形态判", rel)
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
