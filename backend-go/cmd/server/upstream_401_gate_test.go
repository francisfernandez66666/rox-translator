// ============================================================================
// upstream_401_gate_test.go — ★ R-1 断言 A9 的门禁本身（2026-10-04）。
//
// A9 的载体是 deploy/check_upstream_401.sh：发版验收时数「本次启动之后」日志里
// 「api key 无效 (401)」的条数，必须为 0。它要抓的是 R-1 那一族形态——
// /api/health 回 status:ok、UAT 矩阵全绿（mock 上游不看凭据）、Go 单测全绿（假上游回 200），
// 唯独真进程打真上游时每条翻译都 401，因为全局 Key 在启动水合那一步就没装进来。
//
// 本文件不重复服务端逻辑，只保证**这只闹钟是活的**：一个永远判绿的验收脚本
// 比没有脚本更糟（读验收的人会把它当证据）。四条分支各自真跑一次：
//
//	① 干净日志 ⇒ 退 0；
//	② 锚点之后出现 401 ⇒ 退 1（**这一条就是反证**：把计数写死成 0 的脚本必在这里红）；
//	③ 锚点之前的历史 401 ⇒ 退 0（证明时间窗真的在起作用，不是"全文件 grep"）；
//	④ 锚点缺失／文件不存在 ⇒ 退 1（判红不判 0；日志轮转掉绝不能等于"这次上线没问题"）。
//
// 另加一条**保鲜锁**：脚本里的锚点文案与 401 字样都是产线代码里的字面量，
// 哪天有人改了 main.go 那行启动日志或 llm/client.go 那句错误，脚本的 grep 就会
// **永远命中 0 行 ⇒ 永远 PASS**（恒真假绿，AGENTS §一·6 同族）。所以判据要求
// 这两个字面量在 Go 源码里逐字还在。
// ============================================================================
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot401 从本文件所在目录向上找到仓库根（判据＝同时有 deploy 脚本与 backend-go 模块文件）。
// 两条一起问：只有其一会在半套 checkout 里认错根；找不到即 Fatal——静默 skip 等于闸门空转。
func repoRoot401(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if exists(filepath.Join(dir, "deploy", "check_upstream_401.sh")) && exists(filepath.Join(dir, "backend-go", "go.mod")) {
			return dir
		}
		// 兼容从 backend-go 里跑 go test 的形态：根也可能是上一级的上一级
		if exists(filepath.Join(dir, "..", "deploy", "check_upstream_401.sh")) {
			abs, err := filepath.Abs(filepath.Join(dir, ".."))
			if err == nil {
				return abs
			}
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("向上 6 层没找到仓库根（deploy/check_upstream_401.sh 与 backend-go/go.mod 不同在）⇒ 目录结构变了要跟着改本测试，不许改成 skip")
	return ""
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// writeLog 在临时目录写一份假日志并返回路径。
func writeLog(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// runChecker 真跑一次脚本（不 mock、不改写判据），返回退出码与合并输出。
func runChecker(t *testing.T, root, logPath string) (int, string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("本机没有 bash：%v ⇒ 脚本 shebang 就是 bash，缺 bash 的环境不该当成放行", err)
	}
	cmd := exec.Command(bash, filepath.Join(root, "deploy", "check_upstream_401.sh"), logPath)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("执行脚本失败：%v（%s）", err, out)
	}
	return code, string(out)
}

// TestUpstream401CheckerFourBranches 四条分支各跑一次（②即本门禁的反证）。
func TestUpstream401CheckerFourBranches(t *testing.T) {
	root := repoRoot401(t)

	cases := []struct {
		name      string
		content   string // ""＝故意用一个不存在的路径
		absent    bool
		want      int // 期望退出码
		wantSubst string
	}{
		{
			name:      "①干净日志判绿",
			content:   "能言 v2.0.0-go 服务已启动: http://localhost:8787\n[translate] en ok\n[translate] ja ok\n[translate] zh ok\n",
			want:      0,
			wantSubst: "PASS|upstream-401",
		},
		{
			// ★ 反证支：把脚本里的计数写死成 0、或把锚点判据放宽成"文件存在就放行"，本条立刻红。
			name:      "②启动后出现 401 必须判红",
			content:   "能言 v2.0.0-go 服务已启动: http://localhost:8787\n[translate] en 翻译失败（第1次尝试）: api key 无效 (401)\n[translate] ja 翻译失败: api key 无效 (401)\n",
			want:      1,
			wantSubst: "FAIL|upstream-401",
		},
		{
			name:      "③锚点之前的历史 401 不算（时间窗真的在起作用）",
			content:   "[translate] en 翻译失败: api key 无效 (401)\n能言 v2.0.0-go 服务已启动: http://localhost:8787\n[translate] en ok\n[translate] ja ok\n[translate] zh ok\n",
			want:      0,
			wantSubst: "PASS|upstream-401",
		},
		{
			name:      "④没有锚点行判红不判 0",
			content:   "一行启动日志都没有的历史残片\n第二行\n",
			want:      1,
			wantSubst: "锚点",
		},
		{
			name:      "⑤日志文件不存在判红",
			absent:    true,
			want:      1,
			wantSubst: "日志文件不存在",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "missing-translator.log")
			if !tc.absent {
				path = writeLog(t, "translator.log", tc.content)
			}
			code, out := runChecker(t, root, path)
			if code != tc.want {
				t.Fatalf("退出码=%d 期望=%d\n输出：%s", code, tc.want, out)
			}
			if !strings.Contains(out, tc.wantSubst) {
				t.Fatalf("输出里找不到关键判据片段 %q：\n%s", tc.wantSubst, out)
			}
			// 判绿的一律不许带 FAIL 行，判红的一律不许带 PASS 行——
			// 否则"两条都打印、靠退出码挑一条"的写法会让读日志的人拿到自相矛盾的验收单。
			if tc.want == 0 && strings.Contains(out, "FAIL|") {
				t.Fatalf("判绿却打印了 FAIL 行：%s", out)
			}
			if tc.want == 1 && strings.Contains(out, "PASS|") {
				t.Fatalf("判红却打印了 PASS 行：%s", out)
			}
		})
	}
}

// TestUpstream401CheckerLiteralsStillMatchProduction 保鲜锁：脚本 grep 的两个字面量必须逐字还在产线代码里。
// 反证：把 main.go 那行启动日志改写成「服务已就绪」⇒ 本条红（而不是让验收脚本悄悄变成"永远 0 条"）。
func TestUpstream401CheckerLiteralsStillMatchProduction(t *testing.T) {
	root := repoRoot401(t)
	relScript := filepath.Join("deploy", "check_upstream_401.sh")
	script := readRepoFile401(t, root, filepath.Join(root, relScript))

	anchor := literalAfter401(t, script, "UPSTREAM_401_ANCHOR:-")
	bad := literalAfter401(t, script, "UPSTREAM_401_BAD_TEXT:-")
	if anchor == "" || bad == "" {
		t.Fatalf("没从脚本里解析出锚点/401 字面量（anchor=%q bad=%q）⇒ 脚本口径变了，本锁要跟着改，不许直接放行", anchor, bad)
	}
	mainGo := readRepoFile401(t, root, filepath.Join(root, "backend-go", "cmd", "server", "main.go"))
	clientGo := readRepoFile401(t, root, filepath.Join(root, "backend-go", "internal", "llm", "client.go"))
	if !strings.Contains(mainGo, anchor) {
		t.Fatalf("启动日志里已找不到锚点 %q ⇒ 脚本的 grep 会恒命中 0 行、验收永远判绿。改这段文案必须同日改 check_upstream_401.sh", anchor)
	}
	if !strings.Contains(clientGo, bad) {
		t.Fatalf("上游错误里已找不到 %q ⇒ 同上：负向锁会退化成恒真假绿", bad)
	}
}

// TestUpstream401CheckerParsesClean 语法面：bash -n 必须过，且带 set -u（未定义变量要炸不要展开成空串）。
func TestUpstream401CheckerParsesClean(t *testing.T) {
	root := repoRoot401(t)
	abs := filepath.Join(root, "deploy", "check_upstream_401.sh")
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("本机没有 bash：%v", err)
	}
	if out, err := exec.Command(bash, "-n", abs).CombinedOutput(); err != nil {
		t.Fatalf("bash -n 失败：%v\n%s", err, out)
	}
	content := readRepoFile401(t, root, abs)
	if !strings.HasPrefix(content, "#!/usr/bin/env bash") {
		t.Error("首行不是 #!/usr/bin/env bash ⇒ 执行解释器不可预期")
	}
	if !strings.Contains(content, "set -u") {
		t.Error("缺 set -u：未定义变量会被展开成空串，时间窗判据会静默失真")
	}
	// grep -c 在 0 命中时退 1：本仓 pipefail 下必须吃掉退码，否则"干净日志反而判红"。
	if strings.Contains(content, "grep -c \"") && !strings.Contains(content, "|| true") {
		t.Error("grep -c 没有吃掉退码（pipefail 下 0 命中会污染变量取值）")
	}
}

func readRepoFile401(t *testing.T, root, abs string) string {
	t.Helper()
	b, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("读 %s 失败（相对根 %s）: %v —— 『文件没了』本身就是本闸门要抓的事", abs, strings.TrimPrefix(abs, root), err)
	}
	return string(b)
}

// literalAfter401 取脚本里 `${UPSTREAM_401_*:-文案}` 的那段文案（标记之后到第一个 } 为止）。
// 为什么不去匹配双引号串：这两处字面量本来就**在赋值的双引号内部**，标记后面紧跟的是中文文案
// 与收尾的 `}"`；按"找下一对引号"去解析只会拿到空串，而空串让下面的等值判据恒真（恒真假绿）。
func literalAfter401(t *testing.T, text, marker string) string {
	t.Helper()
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	rest := text[i+len(marker):]
	j := strings.IndexAny(rest, "}\n")
	if j <= 0 {
		return ""
	}
	return strings.TrimSpace(rest[:j])
}
