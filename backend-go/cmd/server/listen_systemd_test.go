// ============ listen_systemd_test.go · 职责说明 ============
// cmd/server 包「systemd socket activation 接管」那条腿的守卫级断言（★ #41 S2，2026-10-14）。
//
// 这个文件钉的是三件事（按「最容易出事的地方」排序，不是按代码顺序）：
//
//	T 组 **双向等值锁**（方案文档 §五·2 点名的那一条）：
//	   有 LISTEN_PID/LISTEN_FDS 时**确实走了接管分支**（拿到一个真能 accept 的监听器），
//	   没有它们时**确实回退到自绑分支**（回 nil＋读数说明原因）。
//	   两个方向必须同时成立：只测「接管成功」的话，把判据写成「永远接管」也测不出来；
//	   只测「没有 env 就回退」的话，把接管分支删空也测不出来。
//	F 组 **判据的四档形状**（纯函数层）：pid 对不上／fd 数为 0／脏字符串／缺变量
//	   一律回退——这四档在现网都对应真实的错配形态（手工 start、env 漂了、unit 写错端口）。
//	X 组 **跨语言契约等值锁**：Go 里那一条标记常量必须逐字等于装配脚本里的 BINARY_MARKER，
//	   接管那一行的日志锚必须真是接管读数会产出的文案。
//	   这条锁存在的理由：装配脚本用「制品里 grep 得到标记」当作「这台可以 enable socket」的**唯一**前置，
//	   两边任一侧改名都会把它变成**假绿**（脚本放行、二进制不认 ⇒ 现网停站），
//	   而这件事在 Go 单测和 shell 脚本各自的视野里**都看不见**。
//
// ⚠️ 写法说明：接管那一条腿用**真监听器的 dup 出来的 fd** 打（T1），
//
//	不是把 net.FileListener 换成假构造——协议里最难测的从来不是读 env，
//	而是"那份 fd 真能 accept"（判据级代替不了守卫级，AGENTS §三 第二条）。
//
// =============================================
package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dupListenerFD 把一份真监听器的 fd dup 成一个**独立的 fd 号**返回（模拟 systemd 交来的那一个）。
// dup 之后原监听器随即关闭：交出来的那一份必须自己活着，否则测的是「引用还没断」而不是"接管成立"。
func dupListenerFD(t *testing.T) (fd int, addr string) {
	t.Helper()
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("造真监听器失败：%v", err)
	}
	tl, ok := raw.(*net.TCPListener)
	if !ok {
		raw.Close()
		t.Fatalf("拿不到 *net.TCPListener，无法取 fd")
	}
	addr = tl.Addr().String()
	var dup int
	scErr := error(nil)
	rc, e := tl.SyscallConn()
	if e != nil {
		raw.Close()
		t.Fatalf("SyscallConn 取 fd 失败：%v", e)
	}
	if e := rc.Control(func(c uintptr) {
		dup, scErr = syscall.Dup(int(c))
	}); e != nil {
		raw.Close()
		t.Fatalf("Control 里取 fd 失败：%v", e)
	}
	if scErr != nil || dup < 0 {
		raw.Close()
		t.Fatalf("dup 监听 fd 失败：%v", scErr)
	}
	// 原监听器关掉：那份 fd 归运行时管，dup 那一份是独立文件描述符
	if e := raw.Close(); e != nil {
		t.Fatalf("关闭原监听器失败：%v", e)
	}
	return dup, addr
}

// TestSocketActivationTakeoverBothWays 双向等值锁（T 组）。
func TestSocketActivationTakeoverBothWays(t *testing.T) {
	// —— 正测：env 齐全且那份 fd 是真监听器 ⇒ 必须走接管分支 ——
	fd, addr := dupListenerFD(t)
	t.Cleanup(func() { _ = syscall.Close(fd) })
	t.Setenv("LISTEN_PID", fmt.Sprintf("%d", os.Getpid()))
	t.Setenv("LISTEN_FDS", "1")

	ln, how := systemdSocketListenerAt(fd)
	if ln == nil {
		t.Fatalf("env 齐全＋fd 真能 listen 却没接管（读数 %q）⇒ 接管分支根本没走到，本文件的锁全是空转", how)
	}
	if !strings.Contains(how, sdTakeoverMarker) {
		t.Fatalf("接管读数里不许丢标记（这是装配脚本判「这台可以 enable」的唯一抓手）：%q", how)
	}
	if !strings.Contains(how, "已接管 systemd 交来的监听 fd") {
		t.Fatalf("接管读数的文案锚漂了（装配脚本 LOG_ANCHOR 按这一串数现网日志）：%q", how)
	}
	// 真 accept 一次：证明这份 fd 不是「对象构造出来了但没人能服务」
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		_ = c.Close()
		done <- nil
	}()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		ln.Close()
		t.Fatalf("连自己刚接管的那个端口都连不上（%s）：%v", addr, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("接管来的监听器 accept 失败：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("接管来的监听器 3s 内没 accept 到连接＝这份 fd 其实没在服务")
	}
	_ = conn.Close()
	ln.Close()

	// 接管之后那两个 env 必须被清掉（本服务会 spawn 子进程，env 留着＝继承链上一串假接管）
	if v := os.Getenv("LISTEN_PID"); v != "" {
		t.Fatalf("接管后 LISTEN_PID 没清（子进程会把它也认成交接）：%q", v)
	}
	if v := os.Getenv("LISTEN_FDS"); v != "" {
		t.Fatalf("接管后 LISTEN_FDS 没清：%q", v)
	}

	// —— 反向对照：同一段代码、env 全缺 ⇒ 必须走回退分支并拿到 nil ——
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	ln2, how2 := systemdSocketListenerAt(fd)
	if ln2 != nil {
		ln2.Close()
		t.Fatalf("没有 systemd 的 env 却「接管成功」了＝兜底分支被写空，socket 单元一配错这台会拿到一份不存在的 fd：%q", how2)
	}
	if !strings.Contains(how2, "自行 bind") {
		t.Fatalf("回退分支的读数必须明说「自己 bind」（排障时这是唯一能区分两条腿的读数）：%q", how2)
	}
}

// TestSocketActivationFallbackOnUnusableFD fd 用不了那一档：判据成立但那份 fd 不是监听套接字。
// 现网形态＝unit 写了别的端口／systemd 交来的是一个普通文件 fd。回退方向必须是"站点照活"。
func TestSocketActivationFallbackOnUnusableFD(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "not-a-socket")
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("造普通文件失败：%v", err)
	}
	defer f.Close()
	t.Setenv("LISTEN_PID", fmt.Sprintf("%d", os.Getpid()))
	t.Setenv("LISTEN_FDS", "1")

	ln, how := systemdSocketListenerAt(int(f.Fd()))
	if ln != nil {
		ln.Close()
		t.Fatalf("普通文件 fd 被判成可用监听器＝net.FileListener 的错误被吞了：%q", how)
	}
	if !strings.Contains(how, "但那份套接字用不了") {
		t.Fatalf("这一档的读数必须点名「fd 用不了」，否则运维只会看到「自行 bind」、去查一个没坏的东西：%q", how)
	}
}

// TestSocketTakeoverDecisionFourShapes 判据四档（纯函数层，F 组）。
func TestSocketTakeoverDecisionFourShapes(t *testing.T) {
	self := os.Getpid()
	cases := []struct {
		name       string
		pid        string
		fds        string
		want       bool
		wantFDS    int
		whyOnProda string // 现网对应哪一种错配（写出来是给下一个人看的，不参与判定）
	}{
		{"齐全", fmt.Sprintf("%d", self), "1", true, 1, "正常 socket activation"},
		{"pid 对不上", fmt.Sprintf("%d", self+1), "1", false, 0, "手工 start 时残留的 env／被别的环境变量注了进来"},
		{"fd 数为 0", fmt.Sprintf("%d", self), "0", false, 0, "socket 单元 enable 了但没交来任何东西"},
		{"脏字符串", fmt.Sprintf("%d", self), "yes", false, 0, "unit 写错、或有人往 Environment= 里塞了别的 LISTEN_FDS"},
		{"缺变量", "", "1", false, 0, "今天的样子（没装 socket 单元）"},
	}
	for _, c := range cases {
		fds, ok := sdTakeoverDecision(self, c.pid, c.fds)
		if ok != c.want || fds != c.wantFDS {
			t.Fatalf("%s（现网形态：%s）：期望 ok=%v fds=%d，实得 ok=%v fds=%d", c.name, c.whyOnProda, c.want, c.wantFDS, ok, fds)
		}
	}
}

// socketContractProblems 是跨语言契约的判据本体（X 组用，反证也走这同一个函数）。
//
// 两条腿：
//
//	C1 装配脚本里的 `BINARY_MARKER="<那一条常量>"` 必须**逐字**等于 Go 侧常量值；
//	C2 脚本里的 `LOG_ANCHOR="<那一串>"` 必须是接管读数**真会产出**的子串。
//
// 任一不成立 ⇒ 脚本那道前置就失去射程：C1 漂了脚本会**恒红**（装不上，安全），
// 但也可能**恒绿**（标记改名后旧制品里恰好还留着旧串）——两种都不许带着走。
func socketContractProblems(script, goMarker, takeoverReading string) []string {
	var bad []string
	wantC1 := "BINARY_MARKER=\"" + goMarker + "\""
	if !strings.Contains(script, wantC1) {
		bad = append(bad, "C1 装配脚本里的 BINARY_MARKER 与 Go 侧常量不等值（找："+wantC1+"）")
	}
	// 锚＝脚本里 LOG_ANCHOR="…" 那一对引号内的字面量
	line := ""
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "LOG_ANCHOR=") {
			line = l
			break
		}
	}
	if line == "" {
		return append(bad, "C2 装配脚本里读不到 LOG_ANCHOR 这一行（判据对象不存在）")
	}
	anchor := strings.Trim(strings.SplitN(line, "=", 2)[1], " \t\"")
	if anchor == "" {
		return append(bad, "C2 LOG_ANCHOR 是空串")
	}
	if !strings.Contains(takeoverReading, anchor) {
		bad = append(bad, "C2 接管读数里不含脚本那一串锚（现网会读成「没接管」）：anchor="+anchor+" reading="+takeoverReading)
	}
	return bad
}

// TestSocketActivationCrossLanguageContract 跨语言契约等值锁＋反证（X 组）。
func TestSocketActivationCrossLanguageContract(t *testing.T) {
	repo := repoRootFromCMD(t)
	raw, err := os.ReadFile(filepath.Join(repo, "deploy", "install_socket_activation.sh"))
	if err != nil {
		t.Fatalf("读不到装配脚本（这条锁的判据对象没了）：%v", err)
	}
	script := string(raw)
	// 现算的接管读数：与 systemdSocketListenerAt 的产出同一形态（fd 数=1 那一档）
	reading := "已接管 systemd 交来的监听 fd（" + sdTakeoverMarker + "，fd 数=1）"

	if p := socketContractProblems(script, sdTakeoverMarker, reading); len(p) != 0 {
		t.Fatalf("Go 侧常量/文案与装配脚本已经打架：%s", strings.Join(p, "；"))
	}

	// 反证 1：脚本侧标记改名 ⇒ C1 必须红（否则脚本会放行一支不认 fd 的件）
	mutated := strings.Replace(script, "BINARY_MARKER=\""+sdTakeoverMarker+"\"", "BINARY_MARKER=\"SOMETHING_ELSE\"", 1)
	if mutated == script {
		t.Fatalf("反证没打到靶子：脚本里找不到那一行 BINARY_MARKER")
	}
	if len(socketContractProblems(mutated, sdTakeoverMarker, reading)) == 0 {
		t.Fatalf("反证 1 失效：脚本改了标记名，等值锁却全绿")
	}
	// 反证 2：Go 侧文案漂了（脚本没跟）⇒ C2 必须红
	if len(socketContractProblems(script, sdTakeoverMarker, "已接管 systemd 交来的监听套接字（fd 数=1）")) == 0 {
		t.Fatalf("反证 2 失效：接管文案改了、脚本的日志锚判据却还判绿")
	}
	// 正向对照：干净原文必须零问题（两条反证都红的同时证明"红来自改动、不是恒红"）
	if p := socketContractProblems(script, sdTakeoverMarker, reading); len(p) != 0 {
		t.Fatalf("正向对照失败：%s", strings.Join(p, "；"))
	}
}

// repoRootFromCMD 从测试的工作目录（backend-go/cmd/server）往上找到仓库根。
// 判据对象在仓库根的 deploy/ 下，所以不许写死绝对路径（e2e 红线同口径）。
func repoRootFromCMD(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "deploy", "install_socket_activation.sh")); err == nil {
			return dir
		}
		// backend-go 的上一级才是仓库根
		if filepath.Base(dir) == "backend-go" {
			return filepath.Dir(dir)
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("找不到仓库根（deploy/install_socket_activation.sh 不在射程内）")
	return ""
}

// TestSocketActivationMarkerIsInTheBinarySource 标记常量的「活着」锁：
// 它必须出现在某一条**会被编译进制品**的字符串里，而不是只在注释里。
// 装配脚本判的是"制品里 grep 得到这一串"，而注释不会进制品 ⇒ 注释里写它一次都不算。
func TestSocketActivationMarkerIsInTheBinarySource(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败：%v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "listen_systemd.go"))
	if err != nil {
		t.Fatalf("读不到实现文件：%v", err)
	}
	code := stripGoComments(string(raw))
	if !strings.Contains(code, sdTakeoverMarker) {
		t.Fatalf("标记 %s 只出现在注释里＝它进不了制品，装配脚本那道前置会恒红：%q", sdTakeoverMarker, code)
	}
	// 反证：把注释全剥掉之后仍然要有（正向对照与负向对照同一把尺子）
	if strings.Contains(stripGoComments("// "+sdTakeoverMarker), sdTakeoverMarker) {
		t.Fatalf("剥注释的腿自己失效了（注释里的标记被当成了代码）")
	}
}

// stripGoComments 按行剥掉 Go 行注释与空行（判「这一串是不是代码里的字面量」用）。
// 刻意不做词法分析：这里只需要「注释行不算」这一条，同仓多把同类尺子（AGENTS §一·9 那条注释预演口径）。
func stripGoComments(src string) string {
	var out []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
