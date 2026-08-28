// ============ memmonitor.go · 职责说明 ============
// api 包内部实现文件。
// =============================================

// ============ 本文件职责中文说明 ============
// OOM 内存监控：周期采样本进程 Go 运行时内存峰值（HeapSys/Alloc/StackSys 等），
// 并检测系统内存压力：内存占用超过阈值时读取 /proc/meminfo 与 TOP 进程列表，
// 将内存告警写入 alerts 表并输出日志，辅助排查"服务器内存耗尽 / OOM"类问题。
// 监控周期与阈值可通过 system_config 配置：
//   - mem_monitor_interval_sec: 采样周期（默认 60 秒，0=关闭）
//   - mem_pressure_pct: 系统内存压力阈值百分比（默认 85，达到则告警+列可疑进程）
//
// ========================================
package api

import (
	"log"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// memorySample 单次内存采样结果（本进程 + 系统两级）。
type memorySample struct {
	Time         string  `json:"time"`          // 采样时间
	HeapSysMB    float64 `json:"heap_sys_mb"`   // Go 堆预留内存（MB）
	HeapInuseMB  float64 `json:"heap_inuse_mb"` // Go 堆实际占用（MB）
	StackSysMB   float64 `json:"stack_sys_mb"`  // Go 栈预留内存（MB）
	NumGoroutine int     `json:"num_goroutine"` // 当前 goroutine 数
	PeakHeapMB   float64 `json:"peak_heap_mb"`  // 历史峰值堆内存（MB）
	Pid          int     `json:"pid"`           // 本进程 PID
	VmRSSKB      int64   `json:"vm_rss_kb"`     // 本进程 RSS（/proc/self/status，Linux 有效）
	TotalMemMB   int64   `json:"total_mem_mb"`  // 系统总内存（MB，Linux 有效）
	UsedPct      float64 `json:"used_pct"`      // 系统内存使用百分比（Linux 有效）
	TopProcesses string  `json:"top_processes"` // 内存占用 TOP5 进程（压力告警时采集）
}

// peakHeapBytes 记录历史堆内存峰值（字节），随每次采样更新。
var peakHeapBytes uint64

// memoryMonitorTicker 内存监控后台 goroutine：按周期采样并检测压力。
// 在 startWatchdog 中启动；store 未初始化时跳过告警写入但保留日志采样。
func (s *Server) startMemoryMonitor() {
	if s.Store == nil {
		return
	}
	go func() {
		interval := 60
		if v, _ := s.Store.GetConfig("mem_monitor_interval_sec"); v != "" {
			if n, err := parseInt(v); err == nil && n > 0 {
				interval = n
			}
		}
		if interval <= 0 {
			log.Println("内存监控已关闭（mem_monitor_interval_sec=0）")
			return
		}
		pressurePct := 85
		if v, _ := s.Store.GetConfig("mem_pressure_pct"); v != "" {
			if n, err := parseInt(v); err == nil && n > 0 {
				pressurePct = n
			}
		}
		// 启动先采样一次建立基线
		runMemorySample(pressurePct, s.Store.CreateAlert)
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			runMemorySample(pressurePct, s.Store.CreateAlert)
		}
	}()
	log.Println("内存监控已启动")
}

// alertSink 抽象告警写入，便于测试注入。
type alertSink func(tid int64, level, kind, message string) error

// runMemorySample 执行一次内存采样；系统压力超阈值时写入告警并采集 TOP 进程。
// 参数：pressurePct=压力阈值百分比，sink=告警写入回调。
func runMemorySample(pressurePct int, sink alertSink) {
	s := sampleSelfMemory()
	// 系统级内存压力检测（仅 Linux 可读 /proc/meminfo）
	total, avail, ok := systemMemInfo()
	if ok && total > 0 {
		usedPct := float64(total-avail) / float64(total) * 100
		s.TotalMemMB = total / 1024
		s.UsedPct = usedPct
		if usedPct >= float64(pressurePct) && sink != nil {
			s.TopProcesses = topMemoryProcesses(5)
			msg := "系统内存压力告警: 使用率 " + strconv.FormatFloat(usedPct, 'f', 1, 64) + "%（阈值 " + strconv.Itoa(pressurePct) + "%），TOP5 进程: " + s.TopProcesses
			_ = sink(0, "critical", "memory", msg)
			log.Printf("内存压力告警: %s", msg)
		}
	}
	// 本进程堆内存峰值记录（始终更新并周期性输出，便于事后排查）
	log.Printf("内存采样: heap_inuse=%.1fMB heap_sys=%.1fMB stack=%.1fMB goroutines=%d peak_heap=%.1fMB rss=%dkB used_pct=%.1f%%",
		s.HeapInuseMB, s.HeapSysMB, s.StackSysMB, s.NumGoroutine, s.PeakHeapMB, s.VmRSSKB, s.UsedPct)
}

// sampleSelfMemory 采集本进程 Go 运行时内存指标并更新历史峰值。
// 返回: 完整 memorySample（不含系统字段）。
func sampleSelfMemory() memorySample {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if ms.HeapSys > peakHeapBytes {
		peakHeapBytes = ms.HeapSys // 更新历史堆峰值（HeapSys 为预留量，粗粒度峰值用 HeapSys）
	}
	s := memorySample{
		Time:         time.Now().Format(time.RFC3339),
		HeapSysMB:    mb(ms.HeapSys),
		HeapInuseMB:  mb(ms.HeapInuse),
		StackSysMB:   mb(ms.StackSys),
		NumGoroutine: runtime.NumGoroutine(),
		PeakHeapMB:   mb(peakHeapBytes),
		Pid:          os.Getpid(),
	}
	s.VmRSSKB = selfRSSKB()
	return s
}

// mb 字节转兆字节（保留 1 位小数）。
func mb(b uint64) float64 {
	return float64(b) / 1024 / 1024
}

// selfRSSKB 读取本进程 RSS（/proc/self/status 的 VmRSS 字段；非 Linux 返回 0）。
func selfRSSKB() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				v, _ := strconv.ParseInt(fields[1], 10, 64)
				return v // VmRSS 单位 kB
			}
		}
	}
	return 0
}

// meminfoPath /proc/meminfo 路径（测试可注入模拟文件）。
var meminfoPath = "/proc/meminfo"

// systemMemInfo 读取系统内存总量与可用量（/proc/meminfo，单位 kB）。
// 返回: (总内存kB, 可用kB, 是否成功)。非 Linux 或读取失败返回 (0,0,false)。
func systemMemInfo() (totalKB, availKB int64, ok bool) {
	data, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0, 0, false
	}
	total, avail := int64(0), int64(0)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			total, _ = strconv.ParseInt(fields[1], 10, 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseInt(fields[1], 10, 64)
		}
	}
	return total, avail, total > 0
}

// topMemoryProcesses 采集内存占用 TOP n 的进程描述（/proc/*/status 的 VmRSS）。
// 参数：n=返回条数。返回形如 "PID 12345 RSS 512MB comm=myservice" 的逗号分隔串。
func topMemoryProcesses(n int) string {
	// 读取 /proc 下所有数字目录的进程状态
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "（无法读取 /proc）"
	}
	type procMem struct {
		pid  int64
		rss  int64
		name string
	}
	var procs []procMem
	for _, e := range entries {
		pid, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil {
			continue // 跳过非数字目录（如 self、cpuinfo）
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/status")
		if err != nil {
			continue
		}
		var rss int64
		name := ""
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				f := strings.Fields(line)
				if len(f) >= 2 {
					rss, _ = strconv.ParseInt(f[1], 10, 64)
				}
			} else if strings.HasPrefix(line, "Name:") {
				f := strings.Fields(line)
				if len(f) >= 2 {
					name = f[1]
				}
			}
		}
		if rss > 0 {
			procs = append(procs, procMem{pid: pid, rss: rss, name: name})
		}
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].rss > procs[j].rss })
	parts := make([]string, 0, n)
	for i := 0; i < len(procs) && i < n; i++ {
		parts = append(parts, "PID "+strconv.FormatInt(procs[i].pid, 10)+" RSS "+strconv.FormatInt(procs[i].rss/1024, 10)+"MB "+procs[i].name)
	}
	if len(parts) == 0 {
		return "（无进程数据）"
	}
	return strings.Join(parts, "; ")
}
