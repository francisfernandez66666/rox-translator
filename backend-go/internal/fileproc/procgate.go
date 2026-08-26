// ============ procgate.go · 职责说明 ============
// 文档转换子进程资源闸（2026-08-26 评审整改 R4）：
// pdf2docx / LibreOffice 单实例峰值内存数百 MB——1.6G 生产机上若随工单并发多开，
// 会触发 swap 抖动拖垮整机（quant-research 事故同款机制）。本闸把
// docx_translate.py / pdfwrite.py 的全部子进程调用收敛到可配置的串行/受限并发：
//   - FILEPROC_MAX_CONCURRENT（默认 1）：升配机器后可调大；
//   - Linux 下自动以 nice 10 低优先级运行，防转换抢占 Go 主进程 CPU；
//   - 排队超过 2s 打 [fileproc-queue] 观测日志。
package fileproc

import (
	"log"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

var (
	procGateOnce sync.Once
	procGate     chan struct{}
)

// procGateMax 转换子进程并发上限。
func procGateMax() int {
	if v := os.Getenv("FILEPROC_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 1 // 默认串行：小内存机器的安全值
}

// procGateChan 惰性初始化信号量（Once 保证容量只按首次环境取值）。
func procGateChan() chan struct{} {
	procGateOnce.Do(func() { procGate = make(chan struct{}, procGateMax()) })
	return procGate
}

// acquireProcGate 阻塞获取一个转换名额；返回释放函数（defer 调用）。
func acquireProcGate() func() {
	start := time.Now()
	g := procGateChan()
	g <- struct{}{}
	if d := time.Since(start); d > 2*time.Second {
		log.Printf("[fileproc-queue] waited=%s max=%d", d.Round(10*time.Millisecond), cap(g))
	}
	return func() { <-g }
}

// wrapNice Linux 下以低优先级运行转换子进程；其余平台原样返回。
func wrapNice(bin string, args []string) (string, []string) {
	if runtime.GOOS == "linux" {
		return "nice", append([]string{"-n", "10", bin}, args...)
	}
	return bin, args
}
