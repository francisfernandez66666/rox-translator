// ============ dispatch_cpu_stat.go · 职责说明 ============
// 广州 fpd.slice 的 CPU 配额占用率读数（2026-11-26，口径 13）。
//
// 为什么不能用 os.getloadavg()：整机 load1 被量化服务的 CPUQuota（quant 150% +
// research 60% = 210%，而机器只有 2 核）长期顶在 2.0 上下。若规则 6b 拿整机 load
// 判"广州忙"，它在交易时段几乎永不触发，与"让闲置资源真被用起来"的口径相反。
// 判据必须问 fpd.slice 自己吃了多少配额——cgroup v2 的 cpu.stat 里 usage_usec
// 在采样窗内的增量 ÷ (窗长 × quota) 就是这一档。
// =============================================
package fileproc

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// cpuStatPath fpd.slice 的 cpu.stat 路径（可注入，测试用临时文件桩）。
var cpuStatPath = "/sys/fs/cgroup/fpd.slice/cpu.stat"

// fpdSliceCPUQuota fpd.slice 的 CPUQuota（核数）。默认 0.3（30% of 2 核），
// 与 gz-install.sh 落盘值一致；env 覆盖口供升级档使用。
func fpdSliceCPUQuota() float64 {
	if v := os.Getenv("FILEPROC_FPD_CPU_QUOTA"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return 0.3
}

// readCPUStatUsage 读 cpu.stat 的 usage_usec（微秒）。读不到返回 -1。
func readCPUStatUsage(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) == 2 && fields[0] == "usage_usec" {
			v, err := strconv.ParseInt(fields[1], 10, 64)
			if err == nil {
				return v
			}
			return -1
		}
	}
	return -1
}

// SampleFPDSliceCPUQuota 导出口：用默认采样窗读 fpd.slice 配额占用率（口径 13）。
func SampleFPDSliceCPUQuota() float64 { return sampleFPDSliceCPU(200 * time.Millisecond) }

// sampleFPDSliceCPU 在 window 内采样两次 usage_usec，返回配额占用率（0..1+）。
// 读不到（缺文件/缺键/数值异常）返回 -1，决策器按"不就绪"处理。
func sampleFPDSliceCPU(window time.Duration) float64 {
	if window <= 0 {
		window = 200 * time.Millisecond
	}
	u0 := readCPUStatUsage(cpuStatPath)
	if u0 < 0 {
		return -1
	}
	time.Sleep(window)
	u1 := readCPUStatUsage(cpuStatPath)
	if u1 < u0 {
		return -1
	}
	quota := fpdSliceCPUQuota()
	if quota <= 0 {
		return -1
	}
	// 微秒差 ÷ (窗长微秒 × 配额核数) = 占用率
	return float64(u1-u0) / (float64(window.Microseconds()) * quota)
}
