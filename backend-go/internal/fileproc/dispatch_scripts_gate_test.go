// ============================================================================
// dispatch_scripts_gate_test.go — ★ 2026-09-29 把「远程派发」四条运维脚本接进自动化闸门
//
// 因果链（为什么今日开发必须配这一条）：
//
//	派发这条能力的**唯一验收面**是仓库根下的四个 shell 脚本
//	（sync=同步资产、preflight=开闸前一致性门禁、apply=开闸、revert=关闸）。
//	它们不在 go test 的射程里、也不在 vitest 的射程里，历史上就出过
//	「文档齐全但脚本连 bash -n 都没过」的合入（dispatch_sync.sh 头注记的那次：
//	命令替换少一个收尾引号，跑到第 91 行 unexpected EOF）。
//	更隐蔽的一类是**跨语言接线漂移**：脚本里写的 env 名、脚本清单、字体相对路径、
//	fpdexec 的 header 协议，任何一侧改了另一侧不知道，症状都是
//	"preflight 永远判不过 / 派出去的件和主站不一样"，而这类差异不会在任何日志里报错
//	（改造方案 §12-D5 就是字体路径这一条漂移藏到现网 100% 静默降级）。
//
// 判据形态（每条都配反证，见下面各 Test 的注释）：
//
//	① 语法面：bash -n 必须过；缺省必须**停手并退出非 0**（不许"忘了配就默默跑下去"）；
//	② 纪律面：AGENTS §一·7 的 BRE `grep 'a\|b'`、bash 4 内建 mapfile、生产 IP 裸值，命中即红；
//	③ 协议面：发给 fpdexec 的 header 必须是 base64（正向对照=fpdexec 真在 b64decode 这一行）；
//	④ 集合面：三侧脚本清单等值（sync 同步的 ＝ preflight 校验的 ＝ fpdexec 白名单）；
//	⑤ 资产面：脚本里点名的每个同步对象在仓库里真实存在（缺一个＝半套同步）；
//	⑥ 写读同源：apply 写进 env 的每个 FILEPROC_DISPATCH* 键，二进制侧必须真读它。
//
// 本文件只读仓库文本、只在临时目录里执行脚本的前置校验分支，
// **绝不碰主站配置、绝不拨任何远端**（负路径用例都在 FPD_SSH/root 守卫处就退出）。
// ============================================================================
package fileproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// dispatchRepoRelScripts 射程内的四条派发脚本（相对仓库根，与 go 包目录不同级）。
var dispatchRepoRelScripts = []string{
	"scripts/dispatch_sync.sh",
	"scripts/dispatch_preflight.sh",
	"scripts/dispatch_apply.sh",
	"scripts/dispatch_revert.sh",
}

// dispatchRepoRelFpdexec 远端唯一入口脚本：脚本侧协议判据的正向对照物。
const dispatchRepoRelFpdexec = "deploy/dispatch/fpdexec.py"

// findDispatchRepoRoot 从测试工作目录（backend-go/internal/fileproc）向上找仓库根。
// 判据取「同时有 scripts/dispatch_sync.sh 与 deploy/dispatch/fpdexec.py」两条，
// 只问其一会在半套 checkout 里认错根；找不到即 Fatal——静默 skip 等于闸门空转（恒真假绿）。
func findDispatchRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if fileExistsAt(dir, "scripts/dispatch_sync.sh") && fileExistsAt(dir, dispatchRepoRelFpdexec) {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("向上 6 层没找到派发脚本所在仓库根（当前目录 %s）⇒ 目录结构变了，本闸门需要跟着改路径，不许改成 skip", filepath.Dir(dir))
	return ""
}

// fileExistsAt 判断 root 下相对路径是否存在（用正斜杠书写，跨平台由 filepath 处理）。
func fileExistsAt(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// readRepoText 读仓库文本；读不到直接 Fatal（"文件没了"本身就是本闸门要抓的事）。
func readRepoText(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读 %s 失败: %v", rel, err)
	}
	return string(b)
}

// stripShellCommentLines 逐行剥掉「trim 后以 # 开头」的整行注释（保留行号，命中才指得准）。
// 注意：只剥整行注释，不剥行内 # ——派发脚本的注释里大量出现判据名与示例命令，
// 把行内 # 之后也剥掉会把 `grep -v '^$' # 空行` 这类真实代码改写成别的语义。
func stripShellCommentLines(content string) []string {
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines[i] = ""
		}
	}
	return lines
}

// TestDispatchShellScriptsParseClean ① 语法面：四条脚本必须 bash -n 通过，且带 set -u。
// 反证（本轮实跑过）：把 dispatch_sync.sh 里一处命令替换的收尾引号摘掉，本用例立刻红。
func TestDispatchShellScriptsParseClean(t *testing.T) {
	root := findDispatchRepoRoot(t)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("本机没有 bash（%v）⇒ 语法面无法验证。这些脚本的 shebang 就是 bash，缺 bash 的环境不该当成放行", err)
	}
	for _, rel := range dispatchRepoRelScripts {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		out, err := exec.Command(bash, "-n", abs).CombinedOutput()
		if err != nil {
			t.Errorf("%s bash -n 失败: %v\n%s", rel, err, strings.TrimSpace(string(out)))
		}
		content := readRepoText(t, root, rel)
		if !strings.HasPrefix(content, "#!/usr/bin/env bash") {
			t.Errorf("%s 首行不是 #!/usr/bin/env bash ⇒ 执行解释器不可预期（sh 下数组/[[ ]] 都会炸）", rel)
		}
		// set -u 是这一批脚本的硬纪律：未定义变量必须炸而不是展开成空串。
		// 旧版 dispatch_sync.sh 就吃过 `$FPD_REMOTE_ROOT` 在单引号里为空、兜底分支却在本地展开的亏。
		if !regexp.MustCompile(`(?m)^set -u\b`).MatchString(content) {
			t.Errorf("%s 没有 set -u ⇒ 变量写错会静默展开成空串（派发脚本一律钉 -u）", rel)
		}
	}
}

// TestDispatchScriptsRefuseWithoutEnv ①' 负路径：缺配置必须**停手且退出非 0**，
// 而且必须在拨远端/写文件**之前**就停（守卫顺序锁）。
// 为什么判"退出码"而不是判"报错文案里有没有 FPD_SSH"：文案会变，
// 但「没配就不许往下走」这一条行为是不变量；顺序锁则保证停手发生在副作用之前。
func TestDispatchScriptsRefuseWithoutEnv(t *testing.T) {
	root := findDispatchRepoRoot(t)

	// —— 可安全实跑的两条：sync/preflight 的第一道守卫就是 FPD_SSH，清空后在 ssh 之前就退出
	for _, rel := range []string{"scripts/dispatch_sync.sh", "scripts/dispatch_preflight.sh"} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		cmd := exec.Command("bash", abs)
		// 只给最小 env：FPD_* 一律不带（本机 shell 里可能正留着调试值，那会让本用例失去意义）
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "TMPDIR=" + os.TempDir()}
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s 在没配 FPD_SSH 的情况下**竟然跑成功了** ⇒ 负路径守卫失效（输出前 200 字：%s）", rel, firstN(string(out), 200))
			continue
		}
		if !strings.Contains(string(out), "FPD_SSH") {
			t.Errorf("%s 失败了但没点名 FPD_SSH ⇒ 排障的人不知道该配什么（输出前 200 字：%s）", rel, firstN(string(out), 200))
		}
		// 守卫顺序：第一处**真正的** ssh/rsync 调用必须出现在 FPD_SSH 判空之后。
		// 为什么要三条正则而不是一把 `\bssh\b`：脚本里 "ssh" 这个词还出现在
		// 私钥路径（$HOME/.ssh/dispatch_ed25519）和数组定义（SSH=(ssh -i …)）里，
		// 按字面判会把"第 29 行的路径"当成网络调用，本锁就成了自伤型假红。
		lines := stripShellCommentLines(readRepoText(t, root, rel))
		guardIdx := indexOfSub(lines, `[ -n "$FPD_SSH" ]`)
		netIdx := firstIndexNetCall(lines)
		if guardIdx < 0 {
			t.Errorf("%s 找不到 FPD_SSH 判空守卫 ⇒ 无从确认它先于网络动作", rel)
		} else if netIdx >= 0 && netIdx < guardIdx {
			t.Errorf("%s 第 %d 行就有 ssh/rsync 调用，而 FPD_SSH 守卫在第 %d 行 ⇒ 未配置时会先拨网络", rel, netIdx+1, guardIdx+1)
		}
	}

	// —— 不可实跑的两条：apply/revert 以 root 为前提（在服务器上跑就会真改 systemd），
	//    所以这里只做**静态**顺序锁：root 守卫必须在第一条写/删命令之前。
	for _, rel := range []string{"scripts/dispatch_apply.sh", "scripts/dispatch_revert.sh"} {
		lines := stripShellCommentLines(readRepoText(t, root, rel))
		rootIdx := firstIndexRegex(lines, regexp.MustCompile(`\[ "\$\(id -u\)" = "0" \]`))
		if rootIdx < 0 {
			t.Errorf("%s 没有 root 守卫 ⇒ 非 root 跑会留下半套配置", rel)
			continue
		}
		sideIdx := firstIndexRegex(lines, regexp.MustCompile(`(cat >|rm -f|systemctl (restart|daemon-reload|enable)|cp )`))
		if sideIdx >= 0 && sideIdx < rootIdx {
			t.Errorf("%s 第 %d 行已有写/删/重启动作，而 root 守卫在第 %d 行 ⇒ 顺序反了，非 root 也会先动系统", rel, sideIdx+1, rootIdx+1)
		}
	}
}

// TestDispatchScriptsShellDiscipline ② 纪律面（AGENTS §一·7 + §一·5 的公开面口径）：
//   - 禁止 BRE 的 `grep 'a\|b'`：BSD/精简环境的 grep 不认 \|，会**静默 0 命中**＝恒真空闸门；
//   - 禁止 mapfile：macOS 自带 /bin/bash 是 3.2，没有该内建（sync 头注记踩过）；
//   - 禁止生产 IP 裸值：只放行回环 127.0.0.1 与文档示例 1.2.3.4，其余点分四段一律红
//     （写死生产地址＝把脚本变成「换机器就红」的假绿源，e2e 红线 §一·6 同口径）。
func TestDispatchScriptsShellDiscipline(t *testing.T) {
	root := findDispatchRepoRoot(t)
	breGrep := regexp.MustCompile(`grep[^\n]*'[^'\n]*\\\|[^'\n]*'`)
	ipRe := regexp.MustCompile(`\b[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\b`)
	allowedIP := map[string]bool{"127.0.0.1": true, "1.2.3.4": true, "0.0.0.0": true}
	for _, rel := range dispatchRepoRelScripts {
		for i, line := range stripShellCommentLines(readRepoText(t, root, rel)) {
			if line == "" {
				continue
			}
			if breGrep.MatchString(line) {
				t.Errorf("%s:%d 用了 BRE 交替 `grep 'a\\|b'` ⇒ 部分实现的 grep 会当字面量、静默 0 命中；改 grep -E 'a|b'：%s", rel, i+1, strings.TrimSpace(line))
			}
			if regexp.MustCompile(`\bmapfile\b`).MatchString(line) {
				t.Errorf("%s:%d 用了 mapfile（macOS bash 3.2 无此内建）：%s", rel, i+1, strings.TrimSpace(line))
			}
			for _, ip := range ipRe.FindAllString(line, -1) {
				if !allowedIP[ip] {
					t.Errorf("%s:%d 出现 IP 裸值 %s ⇒ 脚本只能在目标机上现读主机；写死地址换环境即假绿：%s", rel, i+1, ip, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestDispatchHeaderProtocolIsBase64 ③ 协议面：发给 fpdexec 的 header 必须是 base64(JSON)。
//
// 正向对照（先证锁有靶子，否则负向判据恒真＝空转）：fpdexec.py 真的对 header 首行做
//
//	base64.b64decode —— 这一条不在，本判据就只是在猜协议。
//
// 负向本体：四条脚本里任何一行**发出** {"mode":...} 形态的 header，同一行必须经过 base64。
// 反证（本轮实跑过）：把 preflight 的 probe header 改回「裸 JSON 直发」，本用例判红，
//
//	症状正是 D5 那类「脚本能跑通一半、远端报 header 解析失败、闸永远开不了」。
func TestDispatchHeaderProtocolIsBase64(t *testing.T) {
	root := findDispatchRepoRoot(t)
	fpd := readRepoText(t, root, dispatchRepoRelFpdexec)
	if !strings.Contains(fpd, "base64.b64decode") {
		t.Fatalf("正向对照失效：fpdexec.py 不再对 header 做 base64 解码 ⇒ 本闸门的判据前提变了，先改这里再看脚本")
	}
	headerRe := regexp.MustCompile(`\{"mode"`)
	for _, rel := range dispatchRepoRelScripts {
		for i, line := range stripShellCommentLines(readRepoText(t, root, rel)) {
			if !headerRe.MatchString(line) {
				continue
			}
			if !strings.Contains(line, "base64") {
				t.Errorf("%s:%d 把 header 明文直接发了出去（fpdexec 只认 base64 首行）⇒ 远端必报 header 解析失败：%s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestDispatchScriptInventoryAgreesAcrossSides ④ 集合面：三侧脚本清单必须**等值**。
//
//	sync 同步的 = preflight 指纹校验的 = fpdexec 白名单。
//
// 为什么是等值而不是单向子集：任一侧少一个名字，症状都不是"报错"而是
// 「那一类转换永远在本地跑」或「远端有这份脚本却没人校验它的指纹」——
// 两侧各自看都健康，合起来是半盲。
// 反证（本轮实跑过）：从 preflight 的清单里删一个脚本名 ⇒ 本用例点名差集判红。
func TestDispatchScriptInventoryAgreesAcrossSides(t *testing.T) {
	root := findDispatchRepoRoot(t)

	// 远端白名单：ALLOWED_SCRIPTS = { "a.py", ... }
	fpd := readRepoText(t, root, dispatchRepoRelFpdexec)
	remote := setFromBlock(extractBlock(fpd, "ALLOWED_SCRIPTS = {", "}"))

	// preflight 的 G1 清单：FINGERPRINTS="a.py b.py ..."
	pre := readRepoText(t, root, "scripts/dispatch_preflight.sh")
	m := regexp.MustCompile(`(?m)^FINGERPRINTS="([^"]*)"`).FindStringSubmatch(pre)
	if m == nil {
		t.Fatal(`preflight 里没有 FINGERPRINTS="…" 这一行 ⇒ G1 校验清单换了形态，本闸门需同步（不许直接删锁）`)
	}
	checked := setFromList(strings.Fields(m[1]))

	// sync 的 SCRIPTS=(a.py b.py ...)
	syncSh := readRepoText(t, root, "scripts/dispatch_sync.sh")
	sm := regexp.MustCompile(`(?m)^SCRIPTS=\(([^)]*)\)`).FindStringSubmatch(syncSh)
	if sm == nil {
		t.Fatal(`dispatch_sync.sh 里没有 SCRIPTS=(…) 这一行 ⇒ 同步清单换了形态，本闸门需同步`)
	}
	synced := setFromQuotedWords(sm[1])

	a, b, c := sortedKeys(remote), sortedKeys(checked), sortedKeys(synced)
	if strings.Join(a, ",") != strings.Join(b, ",") || strings.Join(a, ",") != strings.Join(c, ",") {
		t.Errorf("派发脚本清单三侧不等值：\n  fpdexec ALLOWED_SCRIPTS = %v\n  preflight FINGERPRINTS   = %v\n  dispatch_sync SCRIPTS      = %v\n⇒ 任一侧缺项＝那一类转换永远在本地跑或永远不被指纹校验（两侧各自看都健康，合起来半盲）",
			a, b, c)
	}
	// 空集不算通过：三个解析器都可能在形态变更后恒返回空，那会让上面的等值判据恒真
	if len(a) < 3 {
		t.Errorf("远端白名单只解析到 %d 项（<3）⇒ 解析器已失效，本闸门正在空转", len(a))
	}
}

// TestDispatchSyncTargetsExistInRepo ⑤ 资产面：脚本点名的每个同步对象在仓库里真实存在。
// 这是 D5（随包兜底字体没落盘）的**主站侧**预截：sync 自己有前置检查，
// 但它检查的是「跑脚本那一刻」，本闸门让漂移在 CI/go test 阶段就红。
// 反证（本轮实跑过）：把 ASSETS 里的字体名改一个字 ⇒ 本用例点名"仓库里不存在"。
func TestDispatchSyncTargetsExistInRepo(t *testing.T) {
	root := findDispatchRepoRoot(t)
	syncSh := readRepoText(t, root, "scripts/dispatch_sync.sh")
	srcDir := regexp.MustCompile(`(?m)^SRC="\$REPO_ROOT/([^"]+)"`).FindStringSubmatch(syncSh)
	if srcDir == nil {
		t.Fatal(`dispatch_sync.sh 里 SRC="$REPO_ROOT/…" 的写法变了 ⇒ 本闸门需同步，不许删锁`)
	}
	base := filepath.Join(root, filepath.FromSlash(srcDir[1]))

	var names []string
	if m := regexp.MustCompile(`(?m)^SCRIPTS=\(([^)]*)\)`).FindStringSubmatch(syncSh); m != nil {
		names = append(names, setKeys(setFromQuotedWords(m[1]))...)
	}
	if m := regexp.MustCompile(`(?m)^ASSETS=\(([^)]*)\)`).FindStringSubmatch(syncSh); m != nil {
		names = append(names, setKeys(setFromQuotedWords(m[1]))...)
	}
	if len(names) == 0 {
		t.Fatal("没解析到任何同步对象 ⇒ 判据恒真，闸门空转")
	}
	sort.Strings(names)
	for _, rel := range names {
		p := filepath.Join(base, filepath.FromSlash(rel))
		st, err := os.Stat(p)
		if err != nil {
			t.Errorf("同步对象在仓库里不存在：%s（脚本会走到 → %s）⇒ 半套同步＝派出去必崩或远端跑旧逻辑", rel, p)
			continue
		}
		if st.Size() == 0 {
			t.Errorf("同步对象是空文件：%s（0 字节）⇒ 指纹等值照样能过，但远端拿到的是空壳", rel)
		}
	}
	// ★ 字体这一支单独点名：它就是现网 D5 的那颗雷（主站没落盘 ⇒ 原版式链 100% 静默降级）
	if !strings.Contains(syncSh, "DroidSansFallbackFull.ttf") {
		t.Error("dispatch_sync.sh 不再同步随包兜底字体 ⇒ 远端 apply 会崩在字体加载；D5 的口径要求它必须在清单里")
	}
	pre := readRepoText(t, root, "scripts/dispatch_preflight.sh")
	if !strings.Contains(pre, "DroidSansFallbackFull.ttf") {
		t.Error("dispatch_preflight.sh 不再校验随包兜底字体 ⇒ G1 对 D5 那颗雷失明")
	}
}

// TestApplyWritesOnlyEnvNamesBinaryReads ⑥ 写读同源：apply 写进 dispatch.env 的每个
// FILEPROC_DISPATCH* 键，二进制侧必须真的读它。
// 为什么这一条值得单独锁：env 名写错不会有任何报错——服务照常起、/api/health 照回 200，
// 只是那档配置**永远是默认值**（开关写了 1 而代码读的是另一个键，就是"开了但其实没开"）。
// 反证（本轮实跑过）：把脚本里的 FILEPROC_DISPATCH_MIN_MB 改个后缀 ⇒ 本用例判红。
func TestApplyWritesOnlyEnvNamesBinaryReads(t *testing.T) {
	root := findDispatchRepoRoot(t)
	applySh := readRepoText(t, root, "scripts/dispatch_apply.sh")
	keyRe := regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]+)=`)
	envBlock := extractBlock(applySh, "<<EOF", "EOF")
	if strings.TrimSpace(envBlock) == "" {
		t.Fatal("没解析到 dispatch.env 的写入块 ⇒ apply 的写法变了，本闸门需同步")
	}
	written := map[string]bool{}
	for _, line := range strings.Split(envBlock, "\n") {
		if m := keyRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			written[m[1]] = true
		}
	}
	if len(written) == 0 {
		t.Fatal("dispatch.env 里一个键都没解析到 ⇒ 判据恒真，闸门空转")
	}

	// 二进制侧真读过的 env：扫 fileproc 包非测试源码里的 os.Getenv("X") / 常量赋值 "X"
	goSrc := map[string]string{}
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr == nil {
			goSrc[path] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫 fileproc 源码失败: %v", err)
	}
	joined := strings.Join(mapValues(goSrc), "\n")
	if !strings.Contains(joined, `"FILEPROC_DISPATCH"`) {
		t.Fatal("正向对照失效：fileproc 源码里读不到 FILEPROC_DISPATCH 常量 ⇒ 本闸门的靶子没了")
	}
	for k := range written {
		if !strings.Contains(joined, `"`+k+`"`) {
			t.Errorf("apply 写了 %s 但 fileproc 里没有一处按这个名字读 ⇒ 该档配置永远是默认值（配置写了却不生效，最难查的一类）", k)
		}
	}
}

// ============ 以下为解析小工具（每个都只服务上面一条判据，刻意写小） ============

// firstN 截前 n 个 rune 给失败信息用（按字节截会劈开中文）。
func firstN(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// indexOfSub 返回第一条**包含** sub 的行号（0 基），找不到返回 -1。
func indexOfSub(lines []string, sub string) int {
	for i, l := range lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

// firstIndexRegex 返回第一条匹配 re 的**非空**行行号（0 基），找不到返回 -1。
func firstIndexRegex(lines []string, re *regexp.Regexp) int {
	for i, l := range lines {
		if l != "" && re.MatchString(l) {
			return i
		}
	}
	return -1
}

// 网络调用判据的三个零件（见上面 TestDispatchScriptsRefuseWithoutEnv 的注释）。
var (
	// netCallAtStart 行首（含缩进）或连接符之后的 ssh|rsync 真调用（`ssh "${SSH_OPTS[@]}" …`、`rsync -az …`）。
	netCallAtStart = regexp.MustCompile(`(?:^\s*|[;&|]\s*)(?:ssh|rsync)\s`)
	// sshArrayCall 经数组发起的调用：`"${SSH[@]}" >out …`（preflight 用的就是这种形态）。
	sshArrayCall = regexp.MustCompile(`\$\{?SSH\[@\]\}?`)
	// assignLine 变量赋值行：右边的 ssh 是路径或数组定义，不是调用。
	assignLine = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*=`)
)

// firstIndexNetCall 返回第一条「真的会拨网络」的行号（0 基），找不到返回 -1。
// 刻意把赋值行排除：`FPD_KEY="$HOME/.ssh/x"`、`SSH=(ssh -i …)`、`RSH_STR="ssh -i …"`
// 三处都含 ssh 字样但都不拨号，按字面判会让守卫顺序锁自伤（首跑真踩：报第 29 行）。
func firstIndexNetCall(lines []string) int {
	for i, l := range lines {
		if l == "" || assignLine.MatchString(l) {
			continue
		}
		if netCallAtStart.MatchString(l) || sshArrayCall.MatchString(l) {
			return i
		}
	}
	return -1
}

// extractBlock 取 startMarker 之后、下一个 endMarker 之前的文本块（本仓脚本的块都很小，够用）。
// endMarker 用「trim 后完全相等」判定，避免把 heredoc 里出现同名字符串的行误当收尾。
func extractBlock(content, startMarker, endMarker string) string {
	si := strings.Index(content, startMarker)
	if si < 0 {
		return ""
	}
	rest := content[si+len(startMarker):]
	for i, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) == endMarker {
			return strings.Join(strings.Split(rest, "\n")[:i], "\n")
		}
	}
	return ""
}

// setFromBlock 从 `{ "a", "b", }` 形态的 Python 集合块里抽出引号内的名字。
func setFromBlock(block string) map[string]bool {
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(block, -1) {
		out[m[1]] = true
	}
	return out
}

// setFromList 空格分隔的名字列表 → 集合。
func setFromList(fields []string) map[string]bool {
	out := map[string]bool{}
	for _, f := range fields {
		out[f] = true
	}
	return out
}

// setFromQuotedWords 从 shell 数组体（`SCRIPTS=(a.py "b.py" 'c/d.ttf')`）里取成员名。
// 三种写法都得收：本仓现状是**不带引号**的空格分隔（ASSETS/SCRIPTS 都是这种），
// 只解引号的话集合会恒空，于是 ④ 的三侧等值判据变成「空 == 空」→ 恒真假绿。
// 因此这里同时剥引号并按空白切，且调用侧必须有"解析到 0 项即红"的量级守卫兜底。
func setFromQuotedWords(body string) map[string]bool {
	out := map[string]bool{}
	for _, f := range strings.Fields(body) {
		w := strings.Trim(f, `'"\s`)
		if w == "" || strings.HasPrefix(w, "#") {
			continue
		}
		out[strings.TrimPrefix(w, "./")] = true
	}
	return out
}

// sortedKeys 集合键排序（比较用稳定序，错误信息也好读）。
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// setKeys 同 sortedKeys，语义上表示"把集合摊成列表"。
func setKeys(m map[string]bool) []string { return sortedKeys(m) }

// mapValues 取 map 的全部值（扫描源码文本用，顺序无关）。
func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
