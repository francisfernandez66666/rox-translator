// ============================================================================
// dispatch_expiry_timer_test.go — ★ 2026-10-01（〇-AF 待决策 ①）到期回归的**行为锁**
//
// 这一条锁抓的是什么缺陷（因果链，全部是现网实读，不是推演）：
//
//	主站有一份"到期自动关闸"的闹钟（translator-dispatch-expiry.timer，每天 04:10 调
//	`dispatch_revert.sh --if-expired`）。它的**旧形态**在 unit 里带一行
//	`ExecStartPost=systemctl disable --now <本 timer>` —— 意图是"到期处理完就别再吵 journalctl"，
//	但它把「今天跑过一次」当成「到期已处理」：systemd 的 ExecStartPost 在 ServiceExit 之后
//	**无条件**执行，脚本那三条提前退出（未到期退 0／非 root 退 2／health 不是 off 退 4）它一概不看。
//	现网读数（10-01 04:10:05）：journal 实读 `Removed "/etc/systemd/system/timers.target.wants/…"`
//	⇒ is-enabled=disabled、is-active=inactive，而到期日还差 **25 天** ⇒ 到期那天不会再有自动回滚，
//	主站会持续朝一台已经不存在的机器派发（每一单多付一次"上传→超时→回落本地"的双份时间）。
//
// 修法（本文件验证的就是这三条语义）：退役这一步从**环境层**（unit 的 ExecStartPost）
// 挪进**脚本内部**，并且位置在"实测 dispatch=off 已坐实"之后。于是：
//
//	① 未到期（含到期日文件缺失）⇒ 退 0 且**一次 systemctl 都不碰**，闹钟照旧每天来；
//	② 已到期且关闸坐实（health=off）⇒ 关掉闸，然后让闹钟退役；
//	③ 已到期但 health≠off（关闸没成功）⇒ 退 4 且**闹钟必须还在**，明天再试。
//
// 为什么必须是行为锁而不是静态 grep 锁（本批真踩过的判断）：
//
//	静态锁只能问"文件里有没有 disable 这一行"，而这一行**该有**（②要用它退役）。
//	真正的判据是**位置与条件**——"disable 在 health 判定之后、且未到期那一支走不到它"。
//	这正是 unit 里那行 ExecStartPost 藏了那么久的原因：单看文件内容它完全合理。
//	所以 `dispatch_revert.sh` 的三个路径开了 env 覆盖口（ENV_FILE/DROPIN/EXPIRY_FILE），
//	本测试用假 systemctl／假 curl／假 id 在临时目录里**真跑一遍**三条分支，
//	并各配一条"故意破坏 ⇒ 必红"的反证（M1 把退役装回无条件位置、M2 把退役挪到 health 之前）。
//
// 安全边界（一句话）：全程只碰 t.TempDir()，假 curl 不联网（脚本里的
// http://127.0.0.1:8787 只是字符串，被假 curl 接走），**绝不动主站配置、绝不重启任何服务**。
// ============================================================================
package fileproc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// revertTimerName 闹钟的名字（脚本里点名的那个 unit）。反证与断言都按这个字符串比对。
const revertTimerName = "translator-dispatch-expiry.timer"

// expiryRepoRelService 到期回归的 service unit（静态锁的射程：那行 ExecStartPost 不许复活）。
const expiryRepoRelService = "deploy/systemd/translator-dispatch-expiry.service"

// revertHarness 一次 `dispatch_revert.sh` 的真跑：临时目录 + 三个假命令 + 两份可读写的状态文件。
type revertHarness struct {
	binDir     string // 放假 systemctl/curl/id， PATH 第一位
	logPath    string // 假 systemctl 把每次调用的参数逐行写这里（＝"闹钟有没有被退役"的唯一证据）
	healthFile string // 假 curl 的 /api/health 响应体从这里读（可控成 off / online / 缺字段）
	dropin     string // 派发 drop-in（存在＝闸开着；脚本删它＝真关闸）
	expiryFile string // 到期日文件（脚本就是拿它和今天比）
	envFile    string // --purge 才会碰，这里只保证存在
}

// newRevertHarness 搭好临时环境并写好三个假命令。
// 假命令一律用 `#!/usr/bin/env bash`，不依赖本机 bash 绝对路径（Linux/macOS 的 env 都在 /usr/bin）。
func newRevertHarness(t *testing.T) *revertHarness {
	t.Helper()
	root := t.TempDir()
	h := &revertHarness{
		binDir:     filepath.Join(root, "bin"),
		logPath:    filepath.Join(root, "systemctl.calls"),
		healthFile: filepath.Join(root, "health.json"),
		dropin:     filepath.Join(root, "dispatch.conf"),
		expiryFile: filepath.Join(root, "dispatch_expiry"),
		envFile:    filepath.Join(root, "dispatch.env"),
	}
	if err := os.MkdirAll(h.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFake := func(name, body string) {
		t.Helper()
		p := filepath.Join(h.binDir, name)
		if err := os.WriteFile(p, []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// 假 systemctl：只记账，不改任何真实服务。
	writeFake("systemctl", `printf '%s\n' "$*" >> "${HARNESS_LOG:-/dev/null}"
exit 0`)
	// 假 curl：按 URL 尾巴分派；/livez 回 200 让脚本的"重启后起来了"这一关能过，
	// /api/health 回一份**可控**的 JSON。绝不联网（真 URL 也被这里接走）。
	writeFake("curl", `url=""
for a in "$@"; do case "$a" in http*) url="$a";; esac; done
case "$url" in
  */livez)      printf '200' ;;
  */api/health) cat "${HARNESS_HEALTH:?}" ;;
  *) printf '000' >&2; exit 7 ;;
esac`)
	// 假 id：脚本要求 root（生产上 timer 就是 root 跑的），这里固定放行，
	// 免得把"非 root 退 2"那一档和退役判据混在一条用例里。
	writeFake("id", `printf '0\n'`)

	for _, p := range []string{h.healthFile, h.dropin, h.envFile} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// setExpiry 写到期日；days 为相对今天的偏移（负数＝已到期）。
// 口径与脚本一致：`date +%F` 的本地日历日，所以这里也用本地时间格式化。
func (h *revertHarness) setExpiry(t *testing.T, days int) {
	t.Helper()
	d := time.Now().AddDate(0, 0, days).Format("2006-01-02")
	if err := os.WriteFile(h.expiryFile, []byte(d+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// removeExpiry 模拟"到期日文件没落盘"这一档（生产上＝运维只写了 drop-in 忘了写日期）。
// harness 建环境时刻意**不**预建这个文件，所以这里只保证"跑完之后它确实不存在"。
func (h *revertHarness) removeExpiry(t *testing.T) {
	t.Helper()
	if err := os.Remove(h.expiryFile); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// setDispatchHealth 控制假 /api/health 的 dispatch 读数。
func (h *revertHarness) setDispatchHealth(t *testing.T, raw string) {
	t.Helper()
	if err := os.WriteFile(h.healthFile, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run 真跑一次脚本。scriptPath 允许指到变异副本（反证用）；空串指仓库原件。
// 返回退出码与"假 systemctl 被调了几次、分别是什么"（按调用顺序）。
func (h *revertHarness) run(t *testing.T, scriptPath, mode string) (int, []string) {
	t.Helper()
	if scriptPath == "" {
		scriptPath = filepath.Join(findDispatchRepoRoot(t), "scripts", "dispatch_revert.sh")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("本机没有 bash（%v）⇒ 本锁的射程就是这条脚本，缺 bash 不许当放行", err)
	}
	// 清空上一分支的账本与状态（同一 harness 可连跑多次，方便反证复用）。
	for _, p := range []string{h.logPath} {
		_ = os.Remove(p)
	}
	if _, err := os.Stat(h.dropin); err != nil {
		if err := os.WriteFile(h.dropin, []byte("[Service]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(bash, scriptPath, mode)
	// PATH 第一位＝假命令；后面必须留真实系统目录（脚本要用 date/head/tr/seq/sleep/python3）。
	cmd.Env = append(os.Environ(),
		"PATH="+h.binDir+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin",
		"ENV_FILE="+h.envFile,
		"DROPIN="+h.dropin,
		"EXPIRY_FILE="+h.expiryFile,
		"HARNESS_LOG="+h.logPath,
		"HARNESS_HEALTH="+h.healthFile,
		// 派发开关的默认值不影响本锁（脚本只比 health 里的 dispatch 词），
		// 但把 LOCAL_BASE 钉成假地址，杜绝任何一次真连接。
		"LOCAL_BASE=http://127.0.0.1:8787",
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !asExitError(err, &ee) {
			t.Fatalf("启动脚本失败：%v（输出：%s）", err, out)
		}
		code = ee.ExitCode()
	}
	var calls []string
	if b, err := os.ReadFile(h.logPath); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if strings.TrimSpace(l) != "" {
				calls = append(calls, l)
			}
		}
	}
	if t.Failed() {
		t.Logf("dispatch_revert.sh 输出：\n%s", out)
	}
	return code, calls
}

// asExitError 把 exec 的错误收成退出码错误（单独一个函数只为让上面那段少一层嵌套）。
func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// callsDisableTimer 账本里有没有"退役闹钟"那一次调用。
func callsDisableTimer(calls []string) bool {
	for _, c := range calls {
		if strings.Contains(c, "disable") && strings.Contains(c, revertTimerName) {
			return true
		}
	}
	return false
}

// TestDispatchRevertExpiryTimerBehaviour ★ 三条分支的行为锁（本文件的主判据）。
func TestDispatchRevertExpiryTimerBehaviour(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		// 脚本用 python3 解析 health JSON。缺它会让"关闸坐实"那一支恒判失败——
		// 那不是回归，是本地环境残缺，所以按项目口径直接 Fatal 点名，**不许静默 skip**。
		t.Fatalf("本机没有 python3（%v）⇒ 脚本的 health 解析腿跑不动，本锁空转比不跑更坏", err)
	}

	t.Run("① 未到期：退 0 且一次 systemctl 都不碰（闹钟照旧）", func(t *testing.T) {
		h := newRevertHarness(t)
		h.setExpiry(t, 25) // 与现网读数同档：到期日还差 25 天
		code, calls := h.run(t, "", "--if-expired")
		if code != 0 {
			t.Fatalf("未到期应退 0（这是 timer 每天调的正常态），实际 %d", code)
		}
		if len(calls) != 0 {
			t.Fatalf("未到期那天不许碰 systemctl（daemon-reload/restart 都没有 ⇒ 服务被无谓重启过），实际：%v", calls)
		}
		if _, err := os.Stat(h.dropin); err != nil {
			t.Fatalf("未到期却把 drop-in 删了 ⇒ 派发被闹钟关掉了（现网事故的反向形态）：%v", err)
		}
		if callsDisableTimer(calls) {
			t.Fatalf("未到期就退役闹钟 ⇒ 正是 10-01 抓到的那个缺陷本体")
		}
	})

	t.Run("② 到期日文件缺失：保守不动作，闹钟也还在", func(t *testing.T) {
		h := newRevertHarness(t)
		h.removeExpiry(t)
		code, calls := h.run(t, "", "--if-expired")
		if code != 0 {
			t.Fatalf("缺到期日应退 0（保守：不擅自关正在运行的派发），实际 %d", code)
		}
		if len(calls) != 0 {
			t.Fatalf("缺到期日那天不该有任何 systemctl 动作，实际：%v", calls)
		}
	})

	t.Run("③ 已到期且实测 dispatch=off：先关闸、再退役闹钟", func(t *testing.T) {
		h := newRevertHarness(t)
		h.setExpiry(t, -1)
		h.setDispatchHealth(t, `{"dispatch":"off","status":"ok"}`)
		code, calls := h.run(t, "", "--if-expired")
		if code != 0 {
			t.Fatalf("已到期且关闸成功应退 0，实际 %d", code)
		}
		joined := strings.Join(calls, "\n")
		for _, want := range []string{"daemon-reload", "restart translator", "disable --now " + revertTimerName} {
			if !strings.Contains(joined, want) {
				t.Fatalf("关账动作缺一步：%q\n实际 systemctl 调用：\n%s", want, joined)
			}
		}
		if _, err := os.Stat(h.dropin); err == nil {
			t.Fatalf("drop-in 还在 ⇒ 没真关闸，但脚本已退 0（「说了关了其实还开着」的形态）")
		}
		// 顺序也是判据：退役必须**在**重启与实测之后（否则又回到"跑过一次就算处理完"）。
		idxDisable, idxRestart := -1, -1
		for i, c := range calls {
			if strings.Contains(c, "disable") {
				idxDisable = i
			}
			if strings.Contains(c, "restart translator") {
				idxRestart = i
			}
		}
		if idxDisable < idxRestart {
			t.Fatalf("退役闹钟（第 %d 次调用）排在重启服务（第 %d 次）之前 ⇒ 关闸结果还没读就自灭", idxDisable, idxRestart)
		}
	})

	t.Run("④ 已到期但 health≠off：退 4 且闹钟必须还在（明天再试）", func(t *testing.T) {
		h := newRevertHarness(t)
		h.setExpiry(t, -1)
		h.setDispatchHealth(t, `{"dispatch":"online","status":"ok"}`)
		code, calls := h.run(t, "", "--if-expired")
		if code != 4 {
			t.Fatalf("关闸没坐实应退 4（脚本头注里的「此时派发可能仍在生效」档），实际 %d", code)
		}
		if callsDisableTimer(calls) {
			t.Fatalf("health 还是 online 就把闹钟退役 ⇒ 从此再也不会有人试，到期日那天没有自动回滚。账本：\n%s",
				strings.Join(calls, "\n"))
		}
	})

	t.Run("⑤ 已到期但 health 里根本没 dispatch 字段：同样不退订", func(t *testing.T) {
		// 就绪判据不吃零值（AGENTS §一·12 同一条口径）：字段缺失＝不可信＝不许当成"已关"。
		h := newRevertHarness(t)
		h.setExpiry(t, -1)
		h.setDispatchHealth(t, `{"status":"ok"}`)
		code, calls := h.run(t, "", "--if-expired")
		if code != 4 {
			t.Fatalf("dispatch 字段缺失应退 4，实际 %d", code)
		}
		if callsDisableTimer(calls) {
			t.Fatalf("把「读不到字段」当成「已关闸」并退役闹钟 ⇒ 零值放行，本仓反复点名的那一类：%s",
				strings.Join(calls, "\n"))
		}
	})
}

// TestDispatchRevertExpiryTimerMutation 反证：把旧缺陷装回去，上面的判据必须立刻红。
// 变异体写在临时目录的副本上，**绝不动仓库原件**（本仓红线：不许在跑测试期间就地改源）。
func TestDispatchRevertExpiryTimerMutation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("本机没有 python3（%v）⇒ 反证跑不动", err)
	}
	root := findDispatchRepoRoot(t)
	src := readRepoText(t, root, "scripts/dispatch_revert.sh")

	// M1＝旧形态：退役这一步不脚本内部，而是"一进来就无条件做"（等价于 unit 里那行 ExecStartPost）。
	// 它必须被**分支 ①**（未到期零 systemctl）抓到。
	mutateUnconditional := strings.Replace(src,
		`MODE="${1:-}"`,
		`MODE="${1:-}"`+"\n"+`systemctl disable --now `+revertTimerName+` >/dev/null 2>&1 || true`,
		1)
	// M2＝半吊子修法：确实挪进脚本了，但放在**读 health 之前**（"跑都跑完了就退役"）。
	// 它必须被**分支 ④**（health≠off 不许退役）抓到。
	mutateBeforeHealth := strings.Replace(src,
		`log "[3/3] 实测必须 = off`,
		`systemctl disable --now `+revertTimerName+` >/dev/null 2>&1 || true`+"\n"+`log "[3/3] 实测必须 = off`,
		1)

	for _, m := range []struct {
		name    string
		body    string
		marker  string
		expired bool
		health  string
	}{
		{"M1 无条件退役（=旧 ExecStartPost 形态）", mutateUnconditional, `MODE="${1:-}"`, false, `{"dispatch":"off"}`},
		{"M2 在实测 health 之前退役", mutateBeforeHealth, `log "[3/3]`, true, `{"dispatch":"online"}`},
	} {
		t.Run(m.name+" ⇒ 对应分支判红", func(t *testing.T) {
			if strings.Count(src, m.marker) == 0 {
				t.Fatalf("变异锚点 %q 在脚本里找不到 ⇒ 脚本形态变了，本反证需同步（不许直接删锁）", m.marker)
			}
			if m.body == src {
				t.Fatalf("变异没生效（锚点 %q 替换后与原文件一致）⇒ 反证空转", m.marker)
			}
			dir := t.TempDir()
			p := filepath.Join(dir, "dispatch_revert_mutant.sh")
			if err := os.WriteFile(p, []byte(m.body), 0o755); err != nil {
				t.Fatal(err)
			}
			h := newRevertHarness(t)
			if m.expired {
				h.setExpiry(t, -1)
			} else {
				h.setExpiry(t, 25)
			}
			h.setDispatchHealth(t, m.health)
			code, calls := h.run(t, p, "--if-expired")
			if !callsDisableTimer(calls) {
				t.Fatalf("变异体居然没退役闹钟 ⇒ 判据抓不到这一类破坏（code=%d calls=%v）", code, calls)
			}
			// 顺带钉住"这一档本来该是什么码"：M1 在未到期那天仍退 0（所以只有行为锁能抓它），
			// M2 在 health≠off 时退 4（退出码对、退役错 ⇒ 又一次证明退出码不够用）。
			wantCode := 0
			if m.expired {
				wantCode = 4
			}
			if code != wantCode {
				t.Fatalf("变异体的退出码从 %d 变了（说明变异打偏到了别的分支）", wantCode)
			}
			t.Logf("反证成立：%s 在 code=%d（与正常态无法区分）的情况下被行为判据抓到，systemctl 账本=%v",
				m.name, code, calls)
		})
	}
}

// TestDispatchExpiryUnitHasNoUnconditionalExecStartPost ★ 环境层的那道静态锁：
// unit 里不许再出现"无条件退役本 timer"的 ExecStartPost。
// 为什么行为锁之外还要这一条：行为锁管的是脚本的语义，unit 是**另一份文件**，
// 谁都可能"顺手加回去"（当年就是这么加上的），而它一旦被装上，脚本怎么改都白改。
// 反证（本轮实跑过）：把注释里那行示例真写成 ExecStartPost ⇒ 本用例点名。
func TestDispatchExpiryUnitHasNoUnconditionalExecStartPost(t *testing.T) {
	root := findDispatchRepoRoot(t)
	unit := readRepoText(t, root, expiryRepoRelService)

	lines := strings.Split(unit, "\n")
	var active []string
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, ";") {
			continue // 注释里保留着那行的原文与理由，那是文档不是配置
		}
		active = append(active, fmt.Sprintf("%d:%s", i+1, s))
	}
	joined := strings.Join(active, "\n")
	if !strings.Contains(joined, "ExecStart=") {
		t.Fatalf("unit 的**生效行**里连 ExecStart 都没有 ⇒ 解析口径不对（本用例正在空转，别当绿灯）：\n%s", joined)
	}
	for _, l := range active {
		body := l[strings.Index(l, ":")+1:]
		if strings.HasPrefix(body, "ExecStartPost") && strings.Contains(body, "disable") {
			t.Fatalf("到期回归 unit 里又出现无条件 ExecStartPost 退役闹钟：%s\n"+
				"⇒ 它在脚本未到期正常退 0 那天照样把自己关掉（10-01 现网事故本体：is-enabled=disabled 而到期日还差 25 天）。"+
				"退役归脚本管，且只在实测 dispatch=off 之后（见 scripts/dispatch_revert.sh 的 ★ 10-01 段）", l)
		}
	}
	// 正向对照：脚本仍然带 --if-expired（少了它，闹钟每天就真的"无条件关闸"了）。
	if !strings.Contains(joined, "dispatch_revert.sh --if-expired") {
		t.Errorf("unit 的 ExecStart 不再是 `dispatch_revert.sh --if-expired` ⇒ 每天一开的到期判断被挪走了：\n%s", joined)
	}
}
