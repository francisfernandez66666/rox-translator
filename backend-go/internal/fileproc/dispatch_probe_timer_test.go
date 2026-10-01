// ============================================================================
// dispatch_probe_timer_test.go — ★ 2026-10-01 派发链路**日探针**的静态锁 + 行为锁
//
// 这一条锁抓的是什么缺陷（因果链，全是现网实读，不是推演）：
//
//	派发这条链路有两条排障腿——`scripts/dispatch_preflight.sh`（G1~G5，其中 G5 真机全往返
//	是**唯一的开闸判据**）与 `cmd/fpdprobe`（主站 Go 派发腿 fileproc.TryDispatch 的真机往返）。
//	10-01 开闸之后，这两条腿**只靠人记得拨**：现网因此只有"手工跑那一次"的往返证据。
//	而这条链路的失败形态是**静默降级**（TryDispatch 失败只打一条 WARN 就返回 false，调用方
//	回落本地链，客户照样拿到能打开的 PDF，AGENTS §一·12 点名的就是这一型）——
//	零症状 ⇒ 没有人会被提醒去跑 ⇒ "闸开着但从没真跑过一次"可以长期存在而无人知晓。
//	⇒ 本批给它们配一只每日闹钟（translator-dispatch-probe.{service,timer} + wrapper
//	   scripts/dispatch_probe_daily.sh），并把"闹钟只会读、只会告警"这件事钉成机械锁。
//
// 射程边界（本文件为什么要同时锁 **unit 的关键字** 和 **wrapper 的行为**）：
//
//	到期回归那只（translator-dispatch-expiry.timer）会删 drop-in、重启服务，是**会写配置**的；
//	本只**只许读**。这不是审美：一只会关闸的 unit 一旦和日检绑在一起，运维就会因为怕它乱动
//	而整只 enable 关掉，本批要堵的洞立刻开回去。所以：
//	  ① 静态锁——两只 unit 与 wrapper 的**生效行**里不许出现 systemctl stop/restart/disable、
//	     daemon-reload、dispatch_revert、写 /etc 的任何动作，也不许出现无条件 ExecStartPost
//	     （10-01 到期闹钟事故的同形死法：ServiceExit 之后无条件执行，脚本的提前退出一概不看）；
//	  ② 行为锁——用假 curl／假 systemctl／假 runuser 加两份假腿脚本，在 t.TempDir() 里
//	     **真跑 wrapper 的成功支与七条失败支**，每条都顺带断言"假 systemctl 一次都没被调用"。
//
// 为什么光有静态锁不够（本仓反复踩过的判断）：
//
//	"红的时候到底告不告警""无样张是判红还是判跳过"都是**位置与条件**的语义，
//	文件里有那行字不等于那行字在正确的分支上。dispatch_revert.sh 的 ★ 10-01 段就是实证：
//	一行 `systemctl disable` 无论放在哪，静态看都完全合理。
//	所以 wrapper 的固定路径全部留了 env 覆盖口（ENV_FILE/LOG_FILE/PREFLIGHT_SCRIPT/
//	FPDPROBE_BIN/PROBE_SAMPLE/PROBE_RUN_AS/LOCAL_BASE），行为锁才测得到真分支而不是恒真分支。
//
// 反证（M1~M4，每条都在临时目录的变异副本上跑，**绝不动仓库原件**）：
//
//	M1 把"有理由就判红"短路成永远没理由（失败也记 ok）；
//	M2 摘掉 red_exit 里的 alert_intake（红了但没人知道）；
//	M3 在 red_exit 里顺手加一行 `systemctl stop translator`（排障闹钟变成关闸器）；
//	M4 把"无样张判红"放宽成"跳过并继续"（09-29 事故的形状被重新装回来）。
//
// 安全边界（一句话）：全程只碰 t.TempDir()，假 curl 把 127.0.0.1 的 URL 接走、**绝不联网**，
// 假 systemctl 只记不发，**绝不动主站配置、绝不重启任何服务、绝不拨任何远端**。
// ============================================================================
package fileproc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"translator/internal/config"
)

// 射程内的三份新文件（unit 两份 + wrapper 一份）。
const (
	probeRepoRelService  = "deploy/systemd/translator-dispatch-probe.service"
	probeRepoRelTimer    = "deploy/systemd/translator-dispatch-probe.timer"
	probeRepoRelWrapper  = "scripts/dispatch_probe_daily.sh"
	probeServiceUnitName = "translator-dispatch-probe.service"
	probeTimerUnitName   = "translator-dispatch-probe.timer"
	// probeProdWrapperPath wrapper 在生产上的落点：unit 的 ExecStart 与 wrapper 头注的安装命令
	// 必须逐字同值——"文档写 A 路径、unit 跑 B 路径"是那种装完永远起不来、而两边都自觉正确的漂移。
	probeProdWrapperPath = "/opt/translator/bin/dispatch_probe_daily.sh"
)

// probeEnvOmit 是 run() 里的删除标记：某些场景要让 wrapper 走**默认值**那一支
// （最典型是 PROBE_RUN_AS 不设＝现网永远 runuser 降级），不能靠传空串表达。
const probeEnvOmit = "\x00__omit__"

// ============================================================================ 静态锁

// probeActiveLines 取 unit/shell 文件的**生效行**（剥整行注释与空行），返回 "行号:内容"。
// 为什么必须剥注释：这三份文件的注释里大量写着被禁止的东西本身（"不许出现 systemctl stop"、
// "旧形态那行 ExecStartPost"），不剥就是自伤型假红；而只剥**整行**注释、不剥行内 #，
// 与 dispatch_scripts_gate_test.go 的 stripShellCommentLines 同一口径（行内 # 之后一并剥掉
// 会把 `grep -v '^$' # 空行` 这类真实代码改写成别的语义）。
func probeActiveLines(content string) []string {
	var out []string
	for i, l := range strings.Split(content, "\n") {
		s := strings.TrimSpace(l)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		out = append(out, fmt.Sprintf("%d:%s", i+1, s))
	}
	return out
}

// probeForbiddenLines 按关键字名单扫生效行，返回命中项（"行号:内容"）。
// 名单里每一条都是"会改现网状态"的动作，而这三份文件的射程是**只读探针**：
// 命中即越界，不论它在哪个分支——分支语义归行为锁管，这一层只保证"根本没这行字"。
func probeForbiddenLines(t *testing.T, rel, content string, keywords []string) []string {
	t.Helper()
	var hits []string
	for _, l := range probeActiveLines(content) {
		body := l[strings.Index(l, ":")+1:]
		for _, kw := range keywords {
			if strings.Contains(body, kw) {
				hits = append(hits, fmt.Sprintf("%s:%s（命中禁止关键字 %q）", rel, l, kw))
			}
		}
	}
	return hits
}

// TestDispatchProbeUnitsExistAndWired 存在性 ＋ 接线等值：
// 三份文件都在、timer 指对 service、ExecStart 指对 wrapper 落点、且落点在 wrapper 头注里有同款写法。
func TestDispatchProbeUnitsExistAndWired(t *testing.T) {
	pinSQLiteDialectForProbeTests(t)
	root := findDispatchRepoRoot(t)
	svc := readRepoText(t, root, probeRepoRelService)
	tmr := readRepoText(t, root, probeRepoRelTimer)
	wrp := readRepoText(t, root, probeRepoRelWrapper)

	svcActive := strings.Join(probeActiveLines(svc), "\n")
	tmrActive := strings.Join(probeActiveLines(tmr), "\n")

	// 正向对照（先证锁有靶子）：解析口径不对时"负向恒真"就是空转。
	if !strings.Contains(svcActive, "ExecStart=") {
		t.Fatalf("%s 的**生效行**里连 ExecStart 都没有 ⇒ 解析口径不对，本用例正在空转（别当绿灯）：\n%s",
			probeRepoRelService, svcActive)
	}
	if !strings.Contains(tmrActive, "OnCalendar=") {
		t.Fatalf("%s 的生效行里没有 OnCalendar ⇒ 这只 timer 根本不会自己起：\n%s", probeRepoRelTimer, tmrActive)
	}

	for _, want := range []string{
		"Type=oneshot",
		"ExecStart=/bin/bash " + probeProdWrapperPath,
		"EnvironmentFile=-/etc/translator/secrets.env", // 告警凭证的来源（与 restore-drill 同一条通道）
		"ALERT_INTAKE_URL=http://127.0.0.1:8787/api/alerts/alertmanager",
	} {
		if !strings.Contains(svcActive, want) {
			t.Errorf("%s 生效行缺 %q ⇒ 闹钟要么起不来，要么红了没人知道\n生效行：\n%s", probeRepoRelService, want, svcActive)
		}
	}
	for _, want := range []string{
		"Unit=" + probeServiceUnitName,
		"Persistent=true",
		"AccuracySec=1min",
		"WantedBy=timers.target",
	} {
		if !strings.Contains(tmrActive, want) {
			t.Errorf("%s 生效行缺 %q ⇒ 停机错过的那次补不上／闹钟不属于 timers.target\n生效行：\n%s",
				probeRepoRelTimer, want, tmrActive)
		}
	}
	// 落点同源（跨文件等值锁）：unit 的 ExecStart 目标必须是 wrapper 头注里那条 install 的落位目录，
	// 且脚本文件名对得上——"文档装 A、unit 跑 B"是装完永远起不来、两边却都自觉正确的漂移。
	if m := regexp.MustCompile(`ExecStart=(\S+)\s+(\S+)`).FindStringSubmatch(svcActive); len(m) != 3 ||
		filepath.Base(m[2]) != "dispatch_probe_daily.sh" || m[2] != probeProdWrapperPath {
		t.Errorf("%s 的 ExecStart 目标不是 %q（实际 %v）⇒ 闹钟跑的不是这份 wrapper", probeRepoRelService, probeProdWrapperPath, m)
	}
	if !strings.Contains(wrp, filepath.Dir(probeProdWrapperPath)+"/") {
		t.Errorf("%s 头注的安装落点里没有 %q ⇒ unit 跑的脚本与文档装的脚本迟早各说各话",
			probeRepoRelWrapper, filepath.Dir(probeProdWrapperPath))
	}
	// 这只 unit 的 [Install] 必须是 multi-user.target（WantedBy=timers.target 是 timer 的事）。
	if !strings.Contains(svcActive, "WantedBy=multi-user.target") {
		t.Errorf("%s 缺 WantedBy=multi-user.target ⇒ systemctl start 之外没有任何挂载点", probeRepoRelService)
	}
}

// TestDispatchProbeUnitsNeverTouchGate ★ 射程边界的静态半（与行为锁的"零 systemctl 调用"配对）：
// 两只 unit 与 wrapper 的生效行里不许出现任何关闸／改配置／重启服务的动作，
// 也不许出现**无条件 ExecStartPost**（10-01 到期闹钟事故本体：ServiceExit 之后无条件执行，
// wrapper 的提前退出它一概不看，于是"跑过一次"被当成"已处理"）。
// 反证（本轮实跑）：把 wrapper 里加一行 `systemctl stop translator`（M3）→ 本用例与行为锁一起红。
func TestDispatchProbeUnitsNeverTouchGate(t *testing.T) {
	pinSQLiteDialectForProbeTests(t)
	root := findDispatchRepoRoot(t)

	unitForbidden := []string{"ExecStartPost", "systemctl", "dispatch_revert", "daemon-reload", "rm -f", "sed -i"}
	wrapForbidden := []string{"systemctl", "dispatch_revert", "daemon-reload", "EnvironmentFile=", "sed -i"}

	var hits []string
	for _, rel := range []string{probeRepoRelService, probeRepoRelTimer} {
		hits = append(hits, probeForbiddenLines(t, rel, readRepoText(t, root, rel), unitForbidden)...)
	}
	hits = append(hits, probeForbiddenLines(t, probeRepoRelWrapper, readRepoText(t, root, probeRepoRelWrapper), wrapForbidden)...)
	for _, h := range hits {
		t.Errorf("越界动作：%s\n⇒ 这只闹钟的射程是**只读探针 + 告警**；关闸归 dispatch_revert.sh / "+
			"translator-dispatch-expiry.timer（两件事不合并，否则运维会因怕它乱动而把日检整个关掉）", h)
	}

	// 正向对照：wrapper 确实**有**读腿与告警腿，否则上面那条负向锁是扫空气。
	wrp := readRepoText(t, root, probeRepoRelWrapper)
	for _, want := range []string{"dispatch_preflight.sh", "fpdprobe", "alert_intake", "DISPATCH_PROBE_RED"} {
		if !strings.Contains(wrp, want) {
			t.Errorf("wrapper 里找不到 %q ⇒ 它已经不拨那条腿／不再告警，上面的负向锁同时失效", want)
		}
	}
}

// TestDispatchProbeWrapperShellDiscipline AGENTS §一·7 的 shell 纪律 + §一·4 的可测口：
//   - bash -n 必须过（派发脚本历史上合入过连语法都没过的版本，见 dispatch_sync.sh 头注）；
//   - shebang 必须 #!/usr/bin/env bash 且有 set -u；
//   - 禁止 BRE 交替 `grep 'a\|b'`（精简实现当字面量 ⇒ **静默 0 命中**＝恒真假绿）；
//   - 禁止 mapfile（macOS bash 3.2 无此内建）；
//   - 禁止生产 IP 裸值（写死地址＝换环境即假绿，§一·6 同口径）；
//   - 必须留着行为测试依赖的 env 覆盖口（口没了 ⇒ 行为锁只能测到恒真分支）。
//
// 反证（本轮实跑）：把 count_mark 里的 `grep -cE` 改回 BRE 的 `grep -c '❌'` 形态并把覆盖口
// 之一（PREFLIGHT_SCRIPT）写死成常量 ⇒ 本用例点名。
func TestDispatchProbeWrapperShellDiscipline(t *testing.T) {
	pinSQLiteDialectForProbeTests(t)
	root := findDispatchRepoRoot(t)
	rel := probeRepoRelWrapper
	abs := filepath.Join(root, filepath.FromSlash(rel))
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("本机没有 bash（%v）⇒ 语法面无法验证；这些脚本的 shebang 就是 bash，缺 bash 不该当放行", err)
	}
	if out, err := exec.Command(bash, "-n", abs).CombinedOutput(); err != nil {
		t.Errorf("%s bash -n 失败：%v\n%s", rel, err, strings.TrimSpace(string(out)))
	}

	content := readRepoText(t, root, rel)
	if !strings.HasPrefix(content, "#!/usr/bin/env bash") {
		t.Errorf("%s 首行不是 #!/usr/bin/env bash ⇒ sh 下数组/[[ ]] 都会炸", rel)
	}
	if !regexp.MustCompile(`(?m)^set -u\b`).MatchString(content) {
		t.Errorf("%s 没有 set -u ⇒ 变量写错会静默展开成空串（派发脚本一律钉 -u）", rel)
	}

	breGrep := regexp.MustCompile(`grep[^\n]*'[^'\n]*\\\|[^'\n]*'`)
	ipRe := regexp.MustCompile(`\b[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\b`)
	allowedIP := map[string]bool{"127.0.0.1": true, "0.0.0.0": true}
	for i, line := range stripShellCommentLines(content) {
		if line == "" {
			continue
		}
		if breGrep.MatchString(line) {
			t.Errorf("%s:%d 用了 BRE 交替 ⇒ 静默 0 命中；改 grep -E 'a|b'：%s", rel, i+1, strings.TrimSpace(line))
		}
		if regexp.MustCompile(`\bmapfile\b`).MatchString(line) {
			t.Errorf("%s:%d 用了 mapfile（macOS bash 3.2 无此内建）：%s", rel, i+1, strings.TrimSpace(line))
		}
		for _, ip := range ipRe.FindAllString(line, -1) {
			if !allowedIP[ip] {
				t.Errorf("%s:%d 出现 IP 裸值 %s ⇒ 主机只许从 dispatch.env 现读：%s", rel, i+1, ip, strings.TrimSpace(line))
			}
		}
	}

	for _, knob := range []string{"${ENV_FILE", "${LOG_FILE", "${PREFLIGHT_SCRIPT", "${FPDPROBE_BIN",
		"${PROBE_SAMPLE", "${PROBE_RUN_AS", "${LOCAL_BASE"} {
		if !strings.Contains(content, knob) {
			t.Errorf("%s 少了 %s 这个 env 覆盖口 ⇒ 行为锁只能测到恒真分支（同 dispatch_revert.sh 留口的理由）",
				rel, knob)
		}
	}
	// 判读三态必须都在（缺 closed 就是"闸关着天天告警"，缺 red 就是"永远绿"）。
	for _, word := range []string{"probe_result=ok", "probe_result=red", "probe_result=closed"} {
		if !strings.Contains(content, word) {
			t.Errorf("wrapper 不再输出 %s ⇒ 结论档位少了一档，日志侧的 grep 判据会静默失真", word)
		}
	}
}

// TestDispatchProbeFilesCarryNoCredentialTopology 凭据红线（任务书原文口径）：
// 派发私钥的**文件名与目录**都不许出现在这三份新文件里（主机/密钥路径的唯一来源是
// 0600 的 dispatch.env，wrapper 只出 credentials_configured=yes/no，连值都不打）。
// 另锁一条：wrapper 里不许有"把 FPD_SSH/FPD_KEY 打进 emit 读数"的写法——
// 那份日志会被原样贴进交接文档，主机名外流＝拓扑外流（/api/health 同口径）。
func TestDispatchProbeFilesCarryNoCredentialTopology(t *testing.T) {
	pinSQLiteDialectForProbeTests(t)
	root := findDispatchRepoRoot(t)
	forbidden := []string{"dispatch_ed25519", "/etc/translator-dispatch", "fpdbox", "seoul"}
	for _, rel := range []string{probeRepoRelService, probeRepoRelTimer, probeRepoRelWrapper} {
		content := readRepoText(t, root, rel)
		for _, f := range forbidden {
			if strings.Contains(content, f) {
				t.Errorf("%s 出现凭据/拓扑字面值 %q ⇒ 私钥落点与主机名只存在于 0600 的 dispatch.env，"+
					"不进 unit、不进脚本、不进文档", rel, f)
			}
		}
	}
	wrp := readRepoText(t, root, probeRepoRelWrapper)
	leak := regexp.MustCompile(`emit[^\n]*\$\{?FPD_(SSH|KEY|KNOWN_HOSTS)\b`)
	for i, line := range stripShellCommentLines(wrp) {
		if leak.MatchString(line) {
			t.Errorf("%s:%d 把凭据值打进了结构化读数 ⇒ 只许出 yes/no：%s", probeRepoRelWrapper, i+1, strings.TrimSpace(line))
		}
	}
	// 正向对照：确实有一行"只出 yes/no"的读数在，否则上面的负向锁扫的是空气。
	if !strings.Contains(wrp, "credentials_configured=") {
		t.Errorf("wrapper 不再出 credentials_configured 读数 ⇒ 凭据面回到「没人知道配没配」的状态")
	}
}

// ============================================================================ 行为锁

// probeHarness 一次 wrapper 真跑的全部假环境：临时目录 + 假 curl/systemctl/runuser + 两份假腿脚本。
type probeHarness struct {
	root       string
	binDir     string // 假命令（PATH 第一位）
	callsLog   string // 所有假命令的调用账本（"有没有偷偷关闸"的唯一证据）
	healthFile string // 假 /api/health 响应体
	envFile    string // 假 dispatch.env（wrapper 只读它）
	sample     string // 假样张
	preflight  string // 假预检脚本
	probeBin   string // 假 fpdprobe
	logFile    string // LOG_FILE 覆盖口的落点
}

// newProbeHarness 搭好临时环境。所有假命令用 #!/usr/bin/env bash，不依赖本机 bash 绝对路径。
func newProbeHarness(t *testing.T) *probeHarness {
	t.Helper()
	root := t.TempDir()
	h := &probeHarness{
		root:       root,
		binDir:     filepath.Join(root, "bin"),
		callsLog:   filepath.Join(root, "calls.log"),
		healthFile: filepath.Join(root, "health.json"),
		envFile:    filepath.Join(root, "dispatch.env"),
		sample:     filepath.Join(root, "probe.pdf"),
		preflight:  filepath.Join(root, "dispatch_preflight.sh"),
		probeBin:   filepath.Join(root, "fpdprobe"),
		logFile:    filepath.Join(root, "probe.log"),
	}
	if err := os.MkdirAll(h.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	// 假 curl：按 URL 尾巴分派。真 URL 也一律被这里接走 ⇒ 本锁**绝不联网**（127.0.0.1 只是字符串）。
	write(filepath.Join(h.binDir, "curl"), `#!/usr/bin/env bash
url=""
for a in "$@"; do case "$a" in http*) url="$a";; esac; done
case "$url" in
  */api/health) cat "${HARNESS_HEALTH:?}" ;;
  */api/alerts*) printf 'alert POST\n' >> "${HARNESS_CALLS:?}" ;;
  *) printf '000' >&2; exit 7 ;;
esac
`, 0o755)
	// 假 systemctl：只记不发。**每一次调用都是越界**（本闹钟不许动任何 unit）。
	write(filepath.Join(h.binDir, "systemctl"), `#!/usr/bin/env bash
printf 'systemctl %s\n' "$*" >> "${HARNESS_CALLS:-/dev/null}"
exit 0
`, 0o755)
	// 假 runuser：记账后剥掉 "-u <user> --" 真跑剩余命令（现网那一支必须能被真跑到）。
	write(filepath.Join(h.binDir, "runuser"), `#!/usr/bin/env bash
printf 'runuser %s\n' "$*" >> "${HARNESS_CALLS:-/dev/null}"
while [ $# -gt 0 ]; do
  case "$1" in
    -u) shift 2 ;;
    --) shift; break ;;
    *) break ;;
  esac
done
exec "$@"
`, 0o755)
	// 假预检：输出形态对齐真预检（✅/❌ 计数是 wrapper 的判据之一），RC/FAIL 由 env 控。
	write(h.preflight, `#!/usr/bin/env bash
{
  printf 'preflight called sample='
  if [ -s "${FPD_SAMPLE:-}" ]; then printf 'present'; else printf 'absent'; fi
  printf ' hostcfgd=%s\n' "${FPD_SSH:+set}"
} >> "${HARNESS_CALLS:-/dev/null}"
if [ "${FAKE_PF_FAIL:-0}" = "1" ]; then echo "❌ G5 未给 FPD_SAMPLE ⇒ 判红而非跳过"; fi
if [ "${FAKE_PF_FAIL2:-0}" = "1" ]; then echo "❌ G1 脚本指纹不等值"; fi
echo "  ✅ G1 其余项等值"
exit "${FAKE_PF_RC:-0}"
`, 0o755)
	// 假 fpdprobe：出一手 @@FPD 读数，并按 "$1.fpdprobe.pdf" 造一个残骸（清理判据的靶子）。
	write(h.probeBin, `#!/usr/bin/env bash
printf 'probe called argc=%s\n' "$#" >> "${HARNESS_CALLS:-/dev/null}"
echo "@@FPD input_size=24101554"
echo "@@FPD pages=parser=32 trailer_count=32 markers=32 used=32"
echo "@@FPD extract_leg=OK 3.4s"
echo "@@FPD apply_leg=OK 55.4s"
echo "@@FPD remote_evidence=${FAKE_EV:-true}"
echo "@@FPD verdict=${FAKE_VERDICT:-OK ⇒ Go 派发腿真机往返过一次}"
printf 'ART' > "$1.fpdprobe.pdf"
exit "${FAKE_PR_RC:-0}"
`, 0o755)

	write(h.envFile, "# 假 dispatch.env（现网这份 0600 属主 translator；这里只验 wrapper 的读腿）\n"+
		"FILEPROC_DISPATCH=1\n"+
		"FILEPROC_DISPATCH_HOST=fpd@fake-ssh-alias\n"+
		"FILEPROC_DISPATCH_SSH_KEY="+filepath.Join(root, "fake_key")+"\n"+
		"FILEPROC_DISPATCH_KNOWN_HOSTS="+filepath.Join(root, "fake_known_hosts")+"\n"+
		"FILEPROC_DISPATCH_MIN_MB=20\nFILEPROC_DISPATCH_MIN_PAGES=30\n", 0o600)
	write(h.sample, "%PDF-1.7 假样张（够档由档位判据管，这里只判 -s）\n", 0o644)
	write(h.healthFile, `{"dispatch":"online","status":"ok"}`, 0o644)
	_ = os.WriteFile(filepath.Join(root, "fake_key"), []byte("not-a-real-key\n"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "fake_known_hosts"), []byte("fake-ssh-alias ssh-ed25519 AAAA\n"), 0o600)
	_ = os.WriteFile(h.callsLog, nil, 0o644)
	return h
}

func (h *probeHarness) setHealth(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(h.healthFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (h *probeHarness) removeSample(t *testing.T) {
	t.Helper()
	if err := os.Remove(h.sample); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// calls 返回假命令账本（每行一次调用）。
func (h *probeHarness) calls() []string {
	b, err := os.ReadFile(h.callsLog)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// probeRun 一次运行的读数集合。
type probeRun struct {
	code  int
	out   string
	calls []string
}

// reading 取最后一条 `@@PROBE … key=value` 的值（同键多次出现时以最终那次为准）。
func (r probeRun) reading(key string) string {
	val := ""
	for _, line := range strings.Split(r.out, "\n") {
		if !strings.HasPrefix(line, "@@PROBE ") {
			continue
		}
		for _, f := range strings.Fields(line[len("@@PROBE "):]) {
			if strings.HasPrefix(f, key+"=") {
				val = strings.TrimPrefix(f, key+"=")
			}
		}
	}
	return val
}

func (r probeRun) has(s string) bool { return strings.Contains(r.out, s) }

// probeWant 一个场景的**完整**期望：结论档位、退出码、告警、reason、两条腿拨没拨。
// 为什么这些维度必须一起断言：本批要防的是"某一维独自好看"——只断退出码抓不到"红了但没告警"，
// 只断告警抓不到"告警发了但顺手把闸关了"，只断 reason 抓不到"reason 对但腿根本没拨"。
type probeWant struct {
	result      string // ok / red / closed
	exit        int
	wantAlert   bool
	wantReason  string // 子串；空串＝不检查
	preflight   bool   // 是否应真拨预检
	probeCalled bool   // 是否应真跑 fpdprobe
	// probeRunuser 非 nil 时检查 runuser 那一支（值为期望出现的账本子串；空串表示"不许有 runuser 调用"）。
	probeRunuser *string
}

// checkProbeRun 把期望逐条化成 violations（不直接 t.Errorf，是为了让反证复用同一份判据：
// 变异体必须被**同一套**判据抓到，否则反证就是在测另一件事）。
func checkProbeRun(r probeRun, w probeWant) []string {
	var bad []string
	if got := r.reading("probe_result"); got != w.result {
		bad = append(bad, fmt.Sprintf("probe_result=%q 期望 %q", got, w.result))
	}
	if r.code != w.exit {
		bad = append(bad, fmt.Sprintf("退出码=%d 期望 %d", r.code, w.exit))
	}
	alerted := false
	for _, c := range r.calls {
		if strings.HasPrefix(c, "alert POST") {
			alerted = true
		}
	}
	if alerted != w.wantAlert {
		bad = append(bad, fmt.Sprintf("告警发送=%v 期望 %v", alerted, w.wantAlert))
	}
	// ERROR 行与告警必须**同时**在/不在：只有一条就是"日志红着但没人知道"或反向的假绿。
	if has := strings.Contains(r.out, "DISPATCH_PROBE_RED"); has != (w.result == "red") {
		bad = append(bad, fmt.Sprintf("ERROR 关键字行存在=%v 但结论是 %q（两者必须同进同退）", has, w.result))
	}
	if w.wantReason != "" {
		if got := r.reading("reason"); !strings.Contains(got, w.wantReason) {
			bad = append(bad, fmt.Sprintf("reason=%q 不含 %q", got, w.wantReason))
		}
	}
	preflightCalled, probeCalled, runuserCalled := false, false, false
	for _, c := range r.calls {
		switch {
		case strings.HasPrefix(c, "preflight called"):
			preflightCalled = true
		case strings.HasPrefix(c, "probe called"):
			probeCalled = true
		case strings.HasPrefix(c, "runuser "):
			runuserCalled = true
		}
	}
	if preflightCalled != w.preflight {
		bad = append(bad, fmt.Sprintf("预检被拨=%v 期望 %v", preflightCalled, w.preflight))
	}
	if probeCalled != w.probeCalled {
		bad = append(bad, fmt.Sprintf("fpdprobe 被拨=%v 期望 %v", probeCalled, w.probeCalled))
	}
	if w.probeRunuser != nil {
		want := *w.probeRunuser != "" // 空串＝期望"没有 runuser 调用"
		if runuserCalled != want {
			bad = append(bad, fmt.Sprintf("runuser 降级=%v 期望 %v（现网必须降到服务账号跑，root 跑通不算）",
				runuserCalled, want))
		}
	}
	// ★ 射程边界（每个场景都断，无一例外）：假 systemctl 一次都不许被调用。
	for _, c := range r.calls {
		if strings.HasPrefix(c, "systemctl") {
			bad = append(bad, fmt.Sprintf("越界：排障闹钟调用了 systemctl（%s）⇒ 它开始动配置/关闸了", c))
		}
	}
	return bad
}

// run 真跑一次 wrapper。scriptPath 指到变异副本（反证用），空串＝仓库原件；env 里值为
// probeEnvOmit 的键会被**删掉**（让 wrapper 走默认值那一支）。
func (h *probeHarness) run(t *testing.T, scriptPath string, env map[string]string) probeRun {
	t.Helper()
	if scriptPath == "" {
		scriptPath = filepath.Join(findDispatchRepoRoot(t), filepath.FromSlash(probeRepoRelWrapper))
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("本机没有 bash（%v）⇒ 本锁的射程就是这份 wrapper", err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("本机没有 python3（%v）⇒ wrapper 的 health 解析腿跑不动，空转比不跑更坏", err)
	}
	_ = os.Remove(h.callsLog)
	_ = os.Remove(h.logFile)

	base := map[string]string{
		"PATH":             h.binDir + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		"HOME":             os.Getenv("HOME"),
		"TMPDIR":           os.TempDir(),
		"ENV_FILE":         h.envFile,
		"LOCAL_BASE":       "http://127.0.0.1:8787",
		"PREFLIGHT_SCRIPT": h.preflight,
		"FPDPROBE_BIN":     h.probeBin,
		"PROBE_SAMPLE":     h.sample,
		"PROBE_LANG":       "en",
		"LOG_FILE":         h.logFile,
		"PROBE_RUN_AS":     "", // 默认把 runuser 那一支绕开（本机没有该命令，且不是本场景的靶子）
		"ALERT_INTAKE_URL": "http://127.0.0.1:8787/api/alerts/alertmanager",
		"ALERT_TOKEN":      "fake-token-for-harness",
		"HARNESS_HEALTH":   h.healthFile,
		"HARNESS_CALLS":    h.callsLog,
	}
	for k, v := range env {
		if v == probeEnvOmit {
			delete(base, k)
			continue
		}
		base[k] = v
	}
	cmd := exec.Command(bash, scriptPath)
	cmd.Env = make([]string, 0, len(base))
	for k, v := range base {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !asExitError(err, &ee) {
			t.Fatalf("启动 wrapper 失败：%v（输出：%s）", err, out)
		}
		code = ee.ExitCode()
	}
	r := probeRun{code: code, out: string(out), calls: h.calls()}
	if t.Failed() {
		t.Logf("wrapper 输出：\n%s", out)
	}
	return r
}

// assertProbe 把 violations 摊开报红。
func assertProbe(t *testing.T, r probeRun, w probeWant) {
	t.Helper()
	if v := checkProbeRun(r, w); len(v) > 0 {
		t.Fatalf("判据不符：%s\n实际读数：probe_result=%s reason=%s exit=%d\n输出：\n%s",
			strings.Join(v, " / "), r.reading("probe_result"), r.reading("reason"), r.code, r.out)
	}
}

// TestDispatchProbeDailyBehaviour ★ wrapper 的分支行为锁（成功支 + 七条失败/回落支）。
func TestDispatchProbeDailyBehaviour(t *testing.T) {
	pinSQLiteDialectForProbeTests(t)

	withRunuser := "runuser"

	t.Run("① 双绿：ok、退 0、不发告警、不碰 systemctl", func(t *testing.T) {
		h := newProbeHarness(t)
		r := h.run(t, "", nil)
		assertProbe(t, r, probeWant{result: "ok", exit: 0, wantAlert: false, preflight: true, probeCalled: true})
		if r.has("LEVEL=ERROR") {
			t.Errorf("绿的那一轮却打了 ERROR 行 ⇒ 订阅面会把正常日检当成事故：%s", r.out)
		}
		// 持久面正对照：配了 LOG_FILE 就必须出一行 log_written=yes，并且读数真的落盘。
		// （emit 的写文件是 `|| true`——不写这一行的话，"配了文件却一个字节都没落"在现网零症状。）
		if !r.has("log_written=yes") {
			t.Errorf("配了 LOG_FILE 却没有 log_written=yes 读数 ⇒ 运维无法区分「journal only」与「持久文件已写」：%s", r.out)
		}
		b, err := os.ReadFile(h.logFile)
		if err != nil || !strings.Contains(string(b), "probe_result=ok") {
			t.Errorf("配了 LOG_FILE 却没写读数（err=%v 内容=%q）⇒ 运维要的持久面是空的", err, string(b))
		}
		// 探针残骸必须被清掉（每天一次不清就胀盘）。
		if _, err := os.Stat(h.sample + ".fpdprobe.pdf"); err == nil {
			t.Errorf("跑完还留着 %s ⇒ 日积月累的残骸会把样张目录撑爆", h.sample+".fpdprobe.pdf")
		}
	})

	t.Run("② 预检退 0 但输出里仍有 ❌：判红（假绿形态本体）", func(t *testing.T) {
		h := newProbeHarness(t)
		r := h.run(t, "", map[string]string{"FAKE_PF_RC": "0", "FAKE_PF_FAIL": "1"})
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "preflight_red",
			preflight: true, probeCalled: true})
		if got := r.reading("preflight_fail"); got == "0" {
			t.Errorf("❌ 计数读到 0 ⇒ count_mark 那条 grep 判据失效（BRE 静默 0 命中的同族）：%s", r.out)
		}
	})

	t.Run("③ fpdprobe 退 2（产物能打开但无远端证据）：判红", func(t *testing.T) {
		h := newProbeHarness(t)
		r := h.run(t, "", map[string]string{"FAKE_PR_RC": "2"})
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "probe_red",
			preflight: true, probeCalled: true})
	})

	t.Run("④ 探针退 0 却 remote_evidence=false：仍然判红", func(t *testing.T) {
		// 这一档是"只改一侧就静默"的形态：探针自己回 0（有人放宽了它的证据判据），
		// wrapper 必须独立再看一次 remote_evidence，绝不因为"退出码绿"就放行。
		h := newProbeHarness(t)
		r := h.run(t, "", map[string]string{"FAKE_EV": "false"})
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "probe_red",
			preflight: true, probeCalled: true})
	})

	t.Run("⑤ 探针退 0 但输出里没有 verdict：判红（零值不放行）", func(t *testing.T) {
		h := newProbeHarness(t)
		quiet := filepath.Join(h.root, "fpdprobe_quiet")
		if err := os.WriteFile(quiet, []byte("#!/usr/bin/env bash\necho 什么都没读出来\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := h.run(t, "", map[string]string{"FPDPROBE_BIN": quiet})
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "probe_output_missing",
			preflight: true, probeCalled: false})
	})

	t.Run("⑥ health 里没有 dispatch 字段：判红且两条腿都不拨", func(t *testing.T) {
		// 就绪判据不许吃零值（AGENTS §一·12）：读不到／字段缺失＝不可信，
		// 而"读不到就当没事"正是把一次服务没起来伪装成一轮正常日检的形态。
		h := newProbeHarness(t)
		h.setHealth(t, `{"status":"ok"}`)
		r := h.run(t, "", nil)
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "health_unreadable"})
	})

	t.Run("⑦ health 回三态词以外的值：同样判红", func(t *testing.T) {
		h := newProbeHarness(t)
		h.setHealth(t, `{"dispatch":"enabled"}`)
		r := h.run(t, "", nil)
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "health_unreadable"})
	})

	t.Run("⑧ 闸关着（dispatch=off）：closed、退 0、不发告警、一条都不拨", func(t *testing.T) {
		// 这一档**不是**静默跳过：closed 是显式档位，红/绿都不许冒充它。
		// 也不告警——闸关着时传输腿本就不该被拨，天天告警会把真回归钝化（§一·6 同款判断）。
		h := newProbeHarness(t)
		h.setHealth(t, `{"dispatch":"off"}`)
		r := h.run(t, "", nil)
		assertProbe(t, r, probeWant{result: "closed", exit: 0, wantAlert: false})
		if r.has("probe_result=ok") {
			t.Errorf("闸关着却记了 ok ⇒ 交接文档会以为今天真往返过一次：%s", r.out)
		}
	})

	t.Run("⑨ 无样张：预检照拨（让 G5 自己红）、探针腿跳过、整体判红", func(t *testing.T) {
		// ★ 硬口径：G5 无样张**判红不判跳过**是既有约定（09-29 事故形状），放宽一条就把闸门拆了。
		h := newProbeHarness(t)
		h.removeSample(t)
		r := h.run(t, "", nil)
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "no_sample",
			preflight: true, probeCalled: false})
		sawAbsent := false
		for _, c := range r.calls {
			if strings.Contains(c, "preflight called") && strings.Contains(c, "sample=absent") {
				sawAbsent = true
			}
		}
		if !sawAbsent {
			t.Errorf("预检没收到「样张缺失」这个事实（FPD_SAMPLE 被兜底成了别的）⇒ G5 会假绿：\n%v", r.calls)
		}
	})

	t.Run("⑩ 凭据现值取不到：判红且一次都不拨", func(t *testing.T) {
		h := newProbeHarness(t)
		if err := os.WriteFile(h.envFile, []byte("FILEPROC_DISPATCH=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := h.run(t, "", nil)
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "credentials_missing"})
		if r.has("hostcfgd") {
			t.Errorf("读数里出现了主机串 ⇒ 拓扑外流：%s", r.out)
		}
	})

	t.Run("⑪ 现网默认那一支：PROBE_RUN_AS 不设 ⇒ 必须 runuser 降到服务账号", func(t *testing.T) {
		// 探针要复现的是**服务账号**那条腿（私钥/known_hosts 可读性都长在这个 uid 上，
		// 09-30 开闸首跑"预检全绿、一单不派"就是这么藏的），root 跑通不算。
		h := newProbeHarness(t)
		r := h.run(t, "", map[string]string{"PROBE_RUN_AS": probeEnvOmit})
		assertProbe(t, r, probeWant{result: "ok", exit: 0, wantAlert: false,
			preflight: true, probeCalled: true, probeRunuser: &withRunuser})
		sawTranslator := false
		for _, c := range r.calls {
			if strings.HasPrefix(c, "runuser") && strings.Contains(c, "translator") {
				sawTranslator = true
			}
		}
		if !sawTranslator {
			t.Errorf("runuser 没降到 translator（账本 %v）⇒ 降档口径漂了", r.calls)
		}
	})

	t.Run("⑫ runuser 存在但降不动档（非 root 宿主）：点名 runuser_unusable，不退回当前 uid", func(t *testing.T) {
		// wrapper 判的是"能不能真降档"而不是"这个文件在不在"：只查存在性的话，
		// 非 root 宿主会把每条探针都跑成失败并报 probe_red，症状被推给下游、
		// 排障的人看不到"这一腿从来没以正确 uid 跑过"（09-30 凭据面同族）。
		h := newProbeHarness(t)
		bin2 := filepath.Join(h.root, "bin2")
		if err := os.MkdirAll(bin2, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"curl", "systemctl"} {
			b, err := os.ReadFile(filepath.Join(h.binDir, n))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin2, n), b, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		// 假 runuser：**记账但退非 0**（＝有命令、降不了档）。
		if err := os.WriteFile(filepath.Join(bin2, "runuser"),
			[]byte("#!/usr/bin/env bash\nprintf 'runuser %s\\n' \"$*\" >> \"${HARNESS_CALLS:-/dev/null}\"\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := h.run(t, "", map[string]string{
			"PATH":         bin2 + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
			"PROBE_RUN_AS": "translator",
		})
		assertProbe(t, r, probeWant{result: "red", exit: 1, wantAlert: true, wantReason: "runuser_unusable",
			preflight: true, probeCalled: false, probeRunuser: &withRunuser})
		// 且**绝不许**在降档失败后仍以当前 uid 把探针跑一遍——那会把"跑过了"写成一句假话。
		if got := r.reading("probe_skip"); got != "runuser_unusable" {
			t.Errorf("probe_skip=%q 而非 runuser_unusable ⇒ 降档失败被读成了别的东西：%s", got, r.out)
		}
	})

	t.Run("⑬ PROBE_KEEP_ARTIFACT=1：残骸保留（人工复跑要看产物时的口子）", func(t *testing.T) {
		h := newProbeHarness(t)
		r := h.run(t, "", map[string]string{"PROBE_KEEP_ARTIFACT": "1"})
		assertProbe(t, r, probeWant{result: "ok", exit: 0, wantAlert: false, preflight: true, probeCalled: true})
		if _, err := os.Stat(h.sample + ".fpdprobe.pdf"); err != nil {
			t.Errorf("显式要求保留却删了产物：%v", err)
		}
	})
}

// TestDispatchProbeDailyBehaviourMutations ★ 四条反证：把缺陷装回 wrapper 的临时副本，
// 必须被**同一套**判据抓到（变异体不落仓库原件——本仓红线：跑测试期间不许就地改源）。
func TestDispatchProbeDailyBehaviourMutations(t *testing.T) {
	pinSQLiteDialectForProbeTests(t)
	root := findDispatchRepoRoot(t)
	src := readRepoText(t, root, probeRepoRelWrapper)

	cases := []struct {
		name    string
		marker  string
		replace string
		// 用哪个场景去验这条破坏（选**该当红**的那一支：变异体在那一场上必须违反判据）
		env map[string]string
	}{
		{
			// M1＝失败也记 ok：把"有 reason 就判红"的总闸短路。
			// 这是最恶性的一类——unit 绿、退出码 0、告警不发，而传输腿其实已经死了。
			name:    "M1 失败也记 ok（总判据短路）",
			marker:  `if [ -n "$REASONS" ]; then`,
			replace: `if [ -n "" ]; then`,
			env:     map[string]string{"FAKE_PF_RC": "0", "FAKE_PF_FAIL": "1", "FAKE_PR_RC": "2"},
		},
		{
			// M2＝红了但不告警：只留日志。现网唯一有人看的通道就此断掉（本批的交付本体之一）。
			name:    "M2 摘掉 alert_intake（红着却没人知道）",
			marker:  "  alert_intake \"$summary\"\n  exit 1",
			replace: "  exit 1",
			env:     map[string]string{"FAKE_PF_FAIL": "1"},
		},
		{
			// M3＝顺手关闸：把"排障闹钟"变成"关闸器"（射程越界，两码事混一份文件）。
			name:    "M3 判红时顺手关闸（systemctl stop）",
			marker:  "  emit \"LEVEL=ERROR DISPATCH_PROBE_RED $summary\"",
			replace: "  systemctl stop translator\n  emit \"LEVEL=ERROR DISPATCH_PROBE_RED $summary\"",
			env:     map[string]string{"FAKE_PF_FAIL": "1"},
		},
		{
			// M4＝把"无样张判红"放宽成"跳过并继续"：09-29 事故的形状被原样装回来。
			name:    "M4 无样张放宽成跳过（不记红）",
			marker:  "  add_reason \"$SKIP_PROBE\"",
			replace: "  :",
			env:     map[string]string{"FAKE_PR_RC": "0"},
		},
	}

	for _, c := range cases {
		t.Run(c.name+" ⇒ 同一套判据当场红", func(t *testing.T) {
			if strings.Count(src, c.marker) == 0 {
				t.Fatalf("变异锚点 %q 在 wrapper 里找不到 ⇒ wrapper 形态变了，本反证需同步（不许直接删锁）", c.marker)
			}
			mutant := strings.Replace(src, c.marker, c.replace, 1)
			if mutant == src {
				t.Fatalf("变异没生效（锚点 %q 替换后与原文件一致）⇒ 反证空转", c.marker)
			}
			dir := t.TempDir()
			p := filepath.Join(dir, "dispatch_probe_daily_mutant.sh")
			if err := os.WriteFile(p, []byte(mutant), 0o755); err != nil {
				t.Fatal(err)
			}
			h := newProbeHarness(t)
			// M4 的靶场是"样张不存在"那一支，得先把样张撤掉。
			if strings.Contains(c.name, "M4") {
				h.removeSample(t)
			}
			r := h.run(t, p, c.env)
			v := checkProbeRun(r, probeWant{result: "red", exit: 1, wantAlert: true,
				preflight: true, probeCalled: c.name != "M4 无样张放宽成跳过（不记红）"})
			if len(v) == 0 {
				t.Fatalf("变异体居然完全符合期望 ⇒ 判据抓不到这一类破坏（输出：\n%s）", r.out)
			}
			t.Logf("反证成立：%s 被抓到 → %s", c.name, strings.Join(v, " / "))
		})
	}
}

// pinSQLiteDialectForProbeTests 自钉方言（AGENTS §一·4 模板）。
//
// fileproc 包本身零 DB 依赖（只读 env），本批的锁也不碰库；但 `config.Default()` 会把
// DB_DRIVER 读进全局 config.C，run_uat 的 PG 模式会把这个全局泄漏给同包后续测试。
// 这里按模板自钉一次并还原，将来谁在本包接了 store 也不会拿到脏方言——成本三行，
// 省掉的是"no such table: information_schema.tables"那种假红一整天的排障。
func pinSQLiteDialectForProbeTests(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}
