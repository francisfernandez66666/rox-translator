// ============ listen_systemd.go · 职责说明 ============
// cmd/server 包的「systemd socket activation 接管」实现（★ #41 零停机第一阶段 S2）。
//
// 这一份文件解决的缺陷本体（因果链）：
//
//	今天 8787/8789 的监听 socket 由业务进程自己 bind，`systemctl restart` 那一跳 socket
//	随进程消失 ⇒ 内核对新连接回 RST ⇒ Caddy 记 502 ⇒ 访客看到「页面没能加载起来」。
//	现网实测的停站时长是分钟级（《部署指南》§五 那次「两站停了 1 分 24 秒」的事故账）。
//	socket 属主交给 systemd 之后（S1 那两份 .socket 单元），restart 期间监听恒在、
//	连接进内核 backlog 排队；但**进程必须认得 systemd 交来的那一个 fd**，
//	否则它会自己 bind 同一地址撞 EADDRINUSE ⇒ 当场起不来。本文件就是"认得"这一半。
//
// ★★ 与 S1 的顺序是硬约束（不是建议）：**先上线本文件（S2），再 enable .socket 单元**。
//
//	反过来的后果是停站而不是"少一点优化"。这条顺序的机械防线在
//	`deploy/install_socket_activation.sh` 的 BINARY_MARKER 预检上（它 grep 的就是下面那个常量），
//	所以**这个常量的字面值是跨语言契约**，改名必须两边一起改，
//	否则装配脚本恒红（方向安全：宁可不装，不可停站）。
//
// 三条设计取向（改动前必读）：
//
//	① **零新依赖**：按 sd_listen_fds(3) 的公开协议自己读那两个环境变量，
//	   不引 coreos/go-systemd（为一个 20 行的协议拉一个外部依赖，代价大于收益）；
//	② **条件不满足就无声回退现状路径**（自绑 ListenAndServe）：
//	   这一条就是风险表 R5「socket 配错／env 没传对」的答案 ⇒ 回滚只需
//	   `systemctl stop <单元>.socket`，**不需要回滚代码**；
//	③ **走哪一条分支必须有一条同屏读数**：启动那一行日志带「接管／自行 bind」与 fd 数，
//	   排障时不用猜（同 §一·3 那条「日志档位是对外排障契约」的口径）。
//	   ⚠️ 这里刻意**不新增 log.Printf 计数**：文案是拼进 main.go 那一行已有的启动日志里的，
//	   AGENTS §一·2 的 log 棘轮（cmd/ 基线 69）按行计数，多写一行就要同步下调基线。
//
// =============================================
package main

import (
	"net"
	"net/http"
	"os"
	"strconv"
)

// sdTakeoverMarker 是"这一支二进制认得 systemd 交来的 fd"的字面证据。
//
// ★ 跨语言契约：`deploy/install_socket_activation.sh` 里的 BINARY_MARKER 必须与此**逐字一致**，
//
//	并由 `cmd/server/listen_systemd_test.go` 的等值锁钉住（改一边不改另一边＝当场红）。
//	它出现在制品里的唯一途径就是被某一条真实字符串带上——所以"二进制里 grep 得到它"
//	等价于"这一支件编进了接管逻辑"，这正是装配脚本要在 enable 之前问的那一件事。
const sdTakeoverMarker = "LISTEN_FDS_TAKEOVER_V1"

// sdFDSStart 是 systemd 交来的第一个 fd 的编号（sd_listen_fds(3) 的固定值 SD_LISTEN_FDS_START）。
const sdFDSStart = 3

// sdTakeoverDecision 是接管判据的**纯函数本体**（只问三个入参，不碰环境、不碰 fd）。
//
// 判据（逐条对应 sd_listen_fds(3) 的语义）：
//   - `LISTEN_PID` 必须**等于本进程 pid**：这一条防的是"fd 是给父进程/别的单元的，不是给你的"，
//     少了它，任何继承到这两个环境变量的子进程都会把不属于它的 socket 认成自己的；
//   - `LISTEN_FDS` 必须是**非负整数**且 **≥1**：拿不到读数／脏值／0 都算"没人在交接"，回退自绑；
//   - 上界刻意不设：多一个 ListenStream 时**先接管第一个 fd 并把读数留在日志里**，
//     因为"接管一半"远好于"撞 EADDRINUSE 起不来"——但那一行读数必须带上 fd 数，
//     否则 socket 单元哪天多加一条监听就成了静默半可用形态（本仓点过名的那一族）。
//
// 参数 pid: 本进程 pid；listenPID/listenFDS: 那两个环境变量的原始读数（可以是不合法字符串）。
// 返回: 交来的 fd 数与是否接管（ok=false 时调用方一律回退现状路径）。
func sdTakeoverDecision(pid int, listenPID string, listenFDS string) (fds int, ok bool) {
	if listenPID == "" || listenFDS == "" {
		return 0, false // 没有这两个变量＝压根不是 socket activation 起来的
	}
	got, err := strconv.Atoi(listenPID)
	if err != nil || got != pid {
		return 0, false // pid 对不上＝那份 fd 不是给这一支进程的
	}
	n, err := strconv.Atoi(listenFDS)
	if err != nil || n < 1 {
		return 0, false // 脏值或 0＝没有可接的监听 fd
	}
	return n, true
}

// systemdSocketListener 取回 systemd 交来的监听 socket；取不到就回 (nil, 读数) 让调用方自绑。
// 真正的实现住在 systemdSocketListenerAt 里，这一层只负责把"systemd 的固定起始 fd"传进去
// （分成两层是为了让测试能喂一个**真监听器的 fd 号**进来——协议里最难测的不是读 env，
// 而是"那份 fd 真能 accept"，见下面那层的注释）。
func systemdSocketListener() (net.Listener, string) {
	return systemdSocketListenerAt(sdFDSStart)
}

// systemdSocketListenerAt 是接管那一条腿的本体：按 sd_listen_fds(3) 读两个环境变量，
// 判定通过后把 start 号那个套接字包成 net.Listener。
//
// 三条接线细节（都是这条链上真会咬人的地方）：
//   - 每个交来的 socket 都要过 `net.FileListener(os.NewFile(fd, name))`；Go 在内部 dup 一份，
//     所以随后 close 那个 *os.File 不会把监听弄没（这里刻意不长期持有它）；
//   - 接管成功后**必须把那两个环境变量清掉**：本服务会 spawn 子进程（Python 文件处理腿、
//     自愈重启的 exec），env 留着会让那些子进程也"以为"自己拿到了 systemd 的 fd，
//     于是继承链上长出一串假接管（这一条就是 sdTakeoverDecision 要核 LISTEN_PID 的同族风险）；
//   - fd 转换失败（不是 socket／没在 listen）＝按"条件不满足"处理，回退自绑并出声，
//     绝不"把半坏的 fd 当成好好的用"。
//
// 返回: 监听器（nil＝不接管）；读数串（进启动日志，说明这一支到底走了哪条分支）。
func systemdSocketListenerAt(start int) (net.Listener, string) {
	pid := os.Getpid()
	n, ok := sdTakeoverDecision(pid, os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS"))
	if !ok {
		return nil, "自行 bind（未收到 systemd 交来的监听 fd）"
	}
	ln := listenerFromFD(start)
	if ln == nil {
		// 判据成立但那份 fd 用不了：回退自绑。表现是"站点照活、只是没享受到零停机"，
		// 而读数必须把这件事说出来，否则下一次排障会把"没优化"读成"优化失效了"。
		return nil, "自行 bind（收到 " + strconv.Itoa(n) + " 个 fd，但那份套接字用不了）"
	}
	// 清掉来源 env（见上面第二条）
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")
	return ln, "已接管 systemd 交来的监听 fd（" + sdTakeoverMarker + "，fd 数=" + strconv.Itoa(n) + "）"
}

// listenerFromFD 把第 fd 号那个已监听套接字包成 net.Listener。
// 单独成函数是为了**测试能打真 fd**：sd_takeover 那一条协议里最难测的是"fd 真能 accept"，
// 把它和 env 判定分开之后，测试可以用一份自己 dup 出来的真监听器喂进来，
// 证明这条腿不是只在 env 存在时才被走到（判据级代替不了守卫级，AGENTS §三）。
func listenerFromFD(fd int) net.Listener {
	f := os.NewFile(uintptr(fd), "systemd-listen")
	if f == nil {
		return nil
	}
	defer func() { _ = f.Close() }() // Go 在 FileListener 内部已 dup，这里关掉自己那份引用
	ln, err := net.FileListener(f)
	if err != nil {
		return nil
	}
	return ln
}

// serveHTTP 是 main 的那一句"择一起跑"：有接管到的监听器就用它 Serve，否则回退现状 ListenAndServe。
// 抽成函数同样是为了守卫级断言——测试直接问"给它一份真监听器，它是不是真用上了这条腿"。
func serveHTTP(s *http.Server, ln net.Listener) error {
	if ln != nil {
		return s.Serve(ln)
	}
	return s.ListenAndServe()
}
