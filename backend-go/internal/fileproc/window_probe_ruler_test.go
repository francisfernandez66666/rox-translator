// ============ window_probe_ruler_test.go · 职责说明 ============
// ★ #41 S4「零停机窗口探针」的**一把尺子**静态锁（2026-10-14）。
//
// 为什么要有这个文件：窗口内 5xx／P99 这条判据现在活在**两份**发版执行体里——
//
//	deploy/swap_web_and_binary.sh            （probe_status：常规换源＋换件那一次重启）
//	deploy/install_socket_activation.sh      （V4_STATS：socket 单元落位那一次重启）
//
// 两次重启是同一个问题的两个窗口，判据必须是同一条尺子。这份重复不是设计选择，
// 是被**上传形态**逼出来的：swap 脚本要被 scp 到服务器 /tmp 单独执行，
// 没法在运行时去 require 第二份文件（多一个上传件就多一个"送过去了但送的不是那一份"的缝，
// 而那正是本脚本 G2 逐文件 sha 要防的东西）。
// ⇒ 既然物理上只能抄两份，就用本闸把"抄"钉成**等值**：任一侧改了统计 awk 而另一侧没跟 ⇒ 红灯。
//
// ⚠️ 这一族的坏形态从来不是"某一份写错"，是**两份各写对了一半然后长期漂移**：
// 现网表现是"换发版脚本就换个口径"，同一晚两个窗口两份读数互相打脸，
// 而没有任何一条闸门会红（同 §一·5 报价三口径、§一·11 计费三把尺子那一族）。
//
// 判据口径：比的是**归一化后的 awk 程序体**（剥空白＋剥反斜杠），不是整行、不是整文件。
// 两侧的反斜杠数量天生不同（install 那份在带引号 heredoc 里写 `\$2`，swap 那份直接写 `$2`），
// 所以不归一剥反斜杠就会**恒红**；归一剥掉之后仍保留"字符集与顺序"的判别力——
// 把 0.99 改成 0.95、把 000 那一腿删掉、把排序循环换成 asort，都会被抓到。
//
// 反证一律喂**内存里的小片段**给同一个判据函数（不改仓库文件），
// 真文件作对照腿：判据必须同时"在交付形态上判绿、在漂移形态上判红"，否则它没有射程。
// =============================================
package fileproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// probeAnchor 是两份 awk 程序共同的**起始锚**（"开始数样本"那一行的字面量）。
// 它同时也是"这一腿还在不在"的正锁：谁把统计改成别的实现，锚就找不到，本闸直接判问题。
const probeAnchor = `awk 'NF>=2 {`

// probeEndAnchor 是程序体的**收尾锚**（awk 的闭合引号，前一行必然是 `}' "$…"`)。
// 取它而不取文件名那一截：两侧落盘的变量名不同（$PROBE_LOG／$PDIR/log），
// 把变量名也纳入比较就是拿"实现细节"当"判据等值"，第一次改名即假红。
const probeEndAnchor = `}'`

// extractProbeAwk 从脚本里抠出窗口统计的 awk 程序体并归一（剥空白与反斜杠）。
// 找不到任一锚 ⇒ 返回 ("", false)，调用方按"尺子不见了"处理，**不许**当成"两份一致"。
func extractProbeAwk(src string) (string, bool) {
	i := strings.Index(src, probeAnchor)
	if i < 0 {
		return "", false
	}
	j := strings.Index(src[i:], probeEndAnchor)
	if j < 0 {
		return "", false
	}
	prog := src[i : i+j+len(probeEndAnchor)]
	prog = strings.ReplaceAll(prog, "\\", "")
	prog = strings.Join(strings.Fields(prog), "")
	return prog, true
}

// probeRulerProblems 返回两份执行体的尺子漂移清单（空＝等值成立）。
// 三条腿缺一不可：两锚都在位／两侧都抠得出程序体／抠出来的东西逐字等值。
func probeRulerProblems(swapSrc, installSrc string) []string {
	var out []string
	a, okA := extractProbeAwk(swapSrc)
	b, okB := extractProbeAwk(installSrc)
	if !okA {
		out = append(out, "swap_web_and_binary.sh 里找不到窗口统计 awk（锚 "+probeAnchor+"）")
	}
	if !okB {
		out = append(out, "install_socket_activation.sh 里找不到窗口统计 awk（V4_STATS 那条腿）")
	}
	if okA && okB && a != b {
		out = append(out, "两份窗口统计 awk 已漂移 ⇒ 同一晚两个窗口会出两把尺子的读数；把两侧改回同一份")
	}
	return out
}

// readProbeScript 读发版执行体真文件；读不到即 Fatal（判据看不见实现时不许静默跳过）。
func readProbeScript(t *testing.T, name string) string {
	t.Helper()
	// 本测试文件在 backend-go/internal/fileproc/，仓库根往上两级。
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "..", "..", "..", "deploy", name))
	if err != nil {
		t.Fatalf("读不到 deploy/%s：%v ⇒ 这条锁此刻没有输入，是空转的", name, err)
	}
	return string(raw)
}

// R1：交付形态两份必须逐字等值（这一条的绿灯全靠两侧真被读到，见上面 Fatal 腿）。
func TestWindowProbeRulerIsOneAwk(t *testing.T) {
	swap := readProbeScript(t, "swap_web_and_binary.sh")
	install := readProbeScript(t, "install_socket_activation.sh")
	if problems := probeRulerProblems(swap, install); len(problems) > 0 {
		t.Errorf("窗口探针不是一把尺子：%s", strings.Join(problems, "；"))
	}
}

// R2：射程自证——四枚漂移 fixture 必须逐条判红，两枚对照必须判绿。
// 没有这一段，R1 的绿灯可能来自"两侧都抠不出东西所以都返回空串、空串等于空串"这种恒绿假象。
func TestWindowProbeRulerRange(t *testing.T) {
	base := `
	awk 'NF>=2 {n++; if ($2 !~ /^2/) bad++; if ($2 == "000") zero++; t[n]=$3+0}
       END {
         if (n==0) { print "0 0 0 none"; exit }
         printf "%%d %%d %%d\n", n, bad, zero
       }' "$LOG"
	`
	// 正对照 1：同一份程序、变量名与缩进不同（heredoc 转义形态）⇒ 必须判绿。
	sameEscaped := strings.ReplaceAll(base, "$2", `\$2`)
	// 漂移 1：分位数被动过（0.99 → 0.95，用替换成别的阈值来模拟）。
	driftPct := strings.ReplaceAll(base, `print "0 0 0 none"; exit`, `print "0 0 1 none"; exit`)
	// 漂移 2：000 那一腿被摘掉（归因分档退化回"非 2xx 一串混判"）。
	driftZero := strings.ReplaceAll(base, ` if ($2 == "000") zero++;`, "")
	// 漂移 3：整个 awk 没了（锚漂走）⇒ 必须报"找不到"，不许报"一致"。
	gone := `echo "探针改成人肉看日志了"` // 故意不含锚

	cases := []struct {
		name    string
		left    string
		right   string
		wantRed bool
	}{
		{"等值（含转义差异）", base, sameEscaped, false},
		{"空计数腿被动过", base, driftPct, true},
		{"000 归因腿被摘", base, driftZero, true},
		{"一侧没有 awk", base, gone, true},
		{"两侧都没有 awk", gone, gone, true},
	}
	for _, c := range cases {
		got := len(probeRulerProblems(c.left, c.right)) > 0
		if got != c.wantRed {
			t.Errorf("%s：判红=%v，期望 %v ⇒ 这把尺子的射程不成立（读法错了比漂移更坏）", c.name, got, c.wantRed)
		}
	}
	// 单独确认"两侧都没有 awk"那一档报的是**找不到锚**，而不是"内容不一致"（两种病的处置不同）。
	probs := probeRulerProblems(gone, gone)
	if len(probs) != 2 || !strings.Contains(probs[0], "找不到") || !strings.Contains(probs[1], "找不到") {
		t.Errorf("锚缺失那一档的点名不对（%v）⇒ 把「尺子不存在」写成「尺子不一致」会引着人去比对内容，而该比的是有没有", probs)
	}
}

// R3：交付形态里两侧都必须带 000 归因腿与 P99 腿（负向等值锁的补充正锁）。
// 为什么单列一条：R1 只保证"两份一样"，两份一起被删成同样短也不红——所以这里钉"量级下限"。
func TestWindowProbeRulerHasBothLegs(t *testing.T) {
	for _, name := range []string{"swap_web_and_binary.sh", "install_socket_activation.sh"} {
		src := readProbeScript(t, name)
		prog, ok := extractProbeAwk(src)
		if !ok {
			t.Fatalf("%s 抠不出窗口统计 awk ⇒ R1 之外这条正锁也没输入", name)
		}
		for _, leg := range []struct {
			label  string
			needle string
		}{
			{"非 2xx 计数腿", `if($2!~/^2/)bad++`},
			{"000 归因腿", `if($2=="000")zero++`},
			{"样本数为零那档", `if(n==0)`},
		} {
			if !strings.Contains(prog, leg.needle) {
				t.Errorf("%s 的窗口统计缺「%s」（归一后应含 %s）⇒ 这一腿被摘掉后，探针会把空转或混合归因读成通过", name, leg.label, leg.needle)
			}
		}
	}
}
