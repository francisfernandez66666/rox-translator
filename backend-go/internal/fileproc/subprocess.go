// ============ subprocess.go · 职责说明 ============
// fileproc 包文档转换子进程统一执行器。
// 此前 pdfwrite.go 三处 exec.Command + CombinedOutput 无超时、不分离输出、
// 取消不杀进程——pdf2docx/LibreOffice 任一挂死即永久占用容量=1 的资源闸，
// 全站文件管线停摆（死锁单点）；CombinedOutput 无界缓存还有内存溢出风险。
// 本执行器提供：分级超时、独立进程组整组击杀、WaitDelay 排空兜底、
// stdout/stderr 各 4MB 限量捕获、可取消的闸门排队。
// =============================================
package fileproc

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// fileprocTimeout 子进程墙钟预算（FILEPROC_TIMEOUT_SEC 可调，默认 600s）。
// extract/apply 含 LibreOffice 转换（脚本内自身 180s），取宽裕值防大文档误杀。
func fileprocTimeout() time.Duration {
	if v := os.Getenv("FILEPROC_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 600 * time.Second
}

// subTimeout 短超时（pdftotext/pdfinfo 等外部小工具）
func subTimeout() time.Duration {
	if v := os.Getenv("FILEPROC_SUB_TIMEOUT_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Second
}

// limitedBuffer 限量写入缓冲：超出上限的字节被丢弃（保 Write 永不报错、内存有界）
type limitedBuffer struct {
	b     bytes.Buffer
	limit int
}

// Write 限量写入：超出上限的字节静默丢弃，保证 Write 永不报错且内存有界（防止子进程输出爆炸 OOM）
func (w *limitedBuffer) Write(p []byte) (int, error) {
	room := w.limit - w.b.Len()
	if room <= 0 {
		return len(p), nil // 已满：吞掉后续输出
	}
	if len(p) > room {
		w.b.Write(p[:room])
		return len(p), nil
	}
	w.b.Write(p)
	return len(p), nil
}

// acquireProcGateCtx 可取消地获取转换名额（工单取消后不再无限排队占位）。
// 返回释放函数；ctx 已取消时返回 no-op（未获得名额无需释放）。
func acquireProcGateCtx(ctx context.Context) func() {
	g := procGateChan()
	start := time.Now()
	select {
	case g <- struct{}{}:
	case <-ctx.Done():
		return func() {}
	}
	d := time.Since(start)
	if d > 2*time.Second {
		log.Printf("[fileproc-queue] waited=%s max=%d", d.Round(10*time.Millisecond), cap(g))
		RecordQueueWait(d) // 记录队列等待指标
	}
	return func() { <-g }
}

// runSubprocess 受控执行子进程（Unix/Darwin/Linux）：薄委托，保持与历史完全等价的行为
// （本地闸 acquireProcGateCtx；详细说明见下方 runSubprocessGated）。
//
// ★ 2026-09-28（改造方案 §3）：原先"取本地转换名额"这件事写死在函数体里，
// 远端派发要想不被锁串行就必须复用不同的闸。这里把闸从函数内部提到入参：
// runSubprocess 只保留历史语义，调用点零改动（这也是 AGENTS §三 的"薄委托"口径）。
func runSubprocess(ctx context.Context, timeout time.Duration, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
	return runSubprocessGated(ctx, timeout, bin, args, stdin, acquireProcGateCtx)
}

// runSubprocessGated 受控执行子进程，**闸由调用方注入**
// （本地＝FILEPROC_MAX_CONCURRENT 的 acquireProcGateCtx，远端＝acquireRemoteGateCtx）。
// 为什么开这个口子：远端执行器若复用本地那道容量通常为 1 的闸，
// 等于把「把大件送走」的收益又锁回串行——本地那一单还在排队，远端却被同一把锁挡着。
//
// gate 为 nil 时**不取闸**（★ 这不是偷懒，是必须的：就绪探测自己要 ssh，而闸的容量又是
// 由探测结果决定的——若探测也去取闸，就会形成"取闸 → 初始化容量 → 探测 → 取闸"的重入，
// sync.Once 在同 goroutine 重入是**永久死锁**（2026-09-28 单测实测：进程挂死 60s 被 timeout 杀）。
// 探测是轻量的短命令且全局只成功一次，不占名额不会打满远端。
//
// 除取闸这一步外，本函数与改造前的 runSubprocess 逐字节等价：
//   - CommandContext 到时取消；Setpgid 独立进程组 + Cancel 杀负 PID 整组
//     （LibreOffice 由 python 派生，仅杀直属子进程会留孤儿继续吃 CPU）
//   - WaitDelay=5s：ctx 触发后强杀并限时排空管道
//   - stdout/stderr 分别限量 4MB（替代 CombinedOutput 的无界缓存，防子进程输出爆炸 OOM）
//   - 监控指标：启动/成功/失败/超时/SIGKILL 次数，供 /metrics 导出
//
// stdin 非 nil 时经标准输入传入 payload。返回分离后的 stdout/stderr 与错误。
func runSubprocessGated(ctx context.Context, timeout time.Duration, bin string, args []string, stdin []byte,
	gate func(context.Context) func()) ([]byte, []byte, error) {
	if gate != nil {
		release := gate(ctx)
		defer release()
	}

	// 记录子进程启动
	RecordStart()
	startTime := time.Now()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // 独立进程组
		cmd.Cancel = func() error {
			if p := cmd.Process; p != nil {
				_ = syscall.Kill(-p.Pid, syscall.SIGKILL) // 杀整组
				RecordSigkill()                           // 记录 SIGKILL
			}
			return nil // 返回 nil 让 WaitDelay 继续排空管道
		}
	}
	cmd.WaitDelay = 5 * time.Second

	var outBuf, errBuf limitedBuffer
	outBuf.limit, errBuf.limit = 4<<20, 4<<20
	cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	duration := time.Since(startTime)

	// 记录执行结果
	if err != nil {
		// 区分超时和其他错误
		if cctx.Err() == context.DeadlineExceeded {
			RecordTimeout(duration)
		} else {
			RecordFailure(duration)
		}
	} else {
		RecordSuccess(duration)
	}

	return outBuf.b.Bytes(), errBuf.b.Bytes(), err
}

// sweepStalePdfDocxCache 清扫超过 24h 的 pdfdocx_*.docx 崩溃残留。
// ExtractTextsPdfDocx 每次创建缓存前顺带执行，成本一次目录遍历。
func sweepStalePdfDocxCache() {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "pdfdocx_") || !strings.HasSuffix(name, ".docx") {
			continue
		}
		if info, ierr := e.Info(); ierr == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(tmp, name))
		}
	}
}
