// ============ 本文件职责中文说明 ============
// 文档转换子进程统一执行器（2026-08-26 整改 D1）：
// 此前 pdfwrite.go 三处 exec.Command + CombinedOutput 无超时、不分离输出、
// 取消不杀进程——pdf2docx/LibreOffice 任一挂死即永久占用容量=1 的资源闸，
// 全站文件管线停摆（死锁单点）；CombinedOutput 无界缓存还有 OOM 面。
// 本执行器提供：分级超时、独立进程组整组击杀、WaitDelay 排空兜底、
// stdout/stderr 各 4MB 限量捕获、可取消的闸门排队。
// ========================================
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

// acquireProcGateCtx 可取消地获取转换名额（整改 D1：工单取消后不再无限排队占位）。
// 返回释放函数；ctx 已取消时返回 no-op（未获得名额无需释放）。
func acquireProcGateCtx(ctx context.Context) func() {
	g := procGateChan()
	start := time.Now()
	select {
	case g <- struct{}{}:
	case <-ctx.Done():
		return func() {}
	}
	if d := time.Since(start); d > 2*time.Second {
		log.Printf("[fileproc-queue] waited=%s max=%d", d.Round(10*time.Millisecond), cap(g))
	}
	return func() { <-g }
}

// runSubprocess 受控执行子进程（Unix/Darwin/Linux）：
//   - CommandContext 到时取消；Setpgid 独立进程组 + Cancel 杀负 PID 整组
//     （LibreOffice 由 python 派生，仅杀直属子进程会留孤儿继续吃 CPU）
//   - WaitDelay=5s：ctx 触发后强杀并限时排空管道
//   - stdout/stderr 分别限量 4MB（替代 CombinedOutput 的无界 CombinedOutput 缓存）
//
// stdin 非 nil 时经标准输入传入 payload。返回分离后的 stdout/stderr 与错误。
func runSubprocess(ctx context.Context, timeout time.Duration, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
	release := acquireProcGateCtx(ctx)
	defer release()

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...)
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // 独立进程组
		cmd.Cancel = func() error {
			if p := cmd.Process; p != nil {
				_ = syscall.Kill(-p.Pid, syscall.SIGKILL) // 杀整组
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
	return outBuf.b.Bytes(), errBuf.b.Bytes(), err
}

// sweepStalePdfDocxCache 清扫超过 24h 的 pdfdocx_*.docx 崩溃残留
// （整改 D1：ExtractTextsPdfDocx 每次创建缓存前顺带执行，成本一次目录遍历）。
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
