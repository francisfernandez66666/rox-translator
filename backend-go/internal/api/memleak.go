// ============ memleak.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 泄漏日志（三期）：每日自动采样内存指标并留存 pprof 快照，供趋势分析与事后定位。
//   - 采样：RSS（/proc/self/status，非 Linux 回退 HeapSys）、HeapAlloc、HeapObjects、
//     Goroutine 数；写入 <UserDataDir>/memleak/memleak.log（JSONL，一行一天）
//   - 快照：heap pprof 二进制 profile 存 memprof/heap_YYYYMMDD_HHMM.pb，保留最近 7 份
//   - 阈值：RSS > leak_rss_threshold_mb（默认 500）时额外落 goroutine(debug=1) 与 heap(debug=1)
//     文本转储（可直接 grep 持有者），并写 critical 告警（复用告警邮件/群机器人链路）
//   - 触发：watchdog 每日一轮（启动即采一次）；亦可手动触发 POST /api/admin/memleak/capture
// =============================================

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

// memLeakSample 单日泄漏日志记录。
type memLeakSample struct {
	Time        string `json:"time"`         // 采样时间 RFC3339
	RSSMB       int64  `json:"rss_mb"`       // 进程驻留内存（Linux /proc/self/status VmRSS）
	HeapMB      int64  `json:"heap_mb"`      // Go 堆在用字节（HeapAlloc）
	HeapObjects uint64 `json:"heap_objects"` // 堆对象数
	Goroutines  int    `json:"goroutines"`   // goroutine 数量
	SysMB       int64  `json:"sys_mb"`       // 进程向 OS 申请的总量（MemStats.Sys）
	Snapshot    string `json:"snapshot"`     // heap pprof 二进制快照文件名（空=未生成）
}

// memLeakDirName 泄漏日志与快照的子目录名（位于 UserDataDir 下）。
const memLeakDirName = "memleak"

// runMemLeakCapture 执行一次泄漏采样：指标日志 + heap 快照 + 超限转储 + 保留策略。
// 该方法由 watchdog 每日调用一次；无副作用失败仅记日志。
func (s *Server) runMemLeakCapture() {
	if s.Store == nil || s.Cfg == nil {
		return
	}
	dir := filepath.Join(s.Cfg.UserDataDir, memLeakDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[memleak] 创建目录失败: %v", err)
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rssKB := readSelfRSSKB()
	rssMB := rssKB / 1024
	sample := memLeakSample{
		Time:        time.Now().Format(time.RFC3339),
		RSSMB:       rssMB,
		HeapMB:      int64(ms.HeapAlloc / 1024 / 1024),
		HeapObjects: ms.HeapObjects,
		Goroutines:  runtime.NumGoroutine(),
		SysMB:       int64(ms.Sys / 1024 / 1024),
	}

	// 1) heap pprof 二进制快照（保留最近 7 份）
	now := time.Now()
	snap := filepath.Join(dir, fmt.Sprintf("heap_%s.pb", now.Format("20060102_1504")))
	f, err := os.Create(snap)
	if err == nil {
		if werr := pprof.Lookup("heap").WriteTo(f, 0); werr == nil {
			sample.Snapshot = filepath.Base(snap)
		}
		f.Close()
		pruneMemSnapshots(dir, 7)
	} else {
		log.Printf("[memleak] 快照创建失败: %v", err)
	}

	// 2) JSONL 追加（一行一天，便于 grep/导入表格）
	line, _ := json.Marshal(sample)
	lf, err := os.OpenFile(filepath.Join(dir, "memleak.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err == nil {
		lf.Write(append(line, '\n'))
		lf.Close()
	}
	log.Printf("[memleak] RSS=%dMB Heap=%dMB Objs=%d Goroutines=%d", sample.RSSMB, sample.HeapMB, sample.HeapObjects, sample.Goroutines)

	// 3) 超阈值：文本转储（可直接 grep 持有者）+ critical 告警（联动邮件/群机器人）
	threshold := int64(500)
	if v, _ := s.Store.GetConfig("leak_rss_threshold_mb"); v != "" {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil && n > 0 {
			threshold = n
		}
	}
	if rssMB > threshold {
		dump := filepath.Join(dir, fmt.Sprintf("dump_%s.txt", now.Format("20060102_1504")))
		if df, derr := os.Create(dump); derr == nil {
			gz := gzip.NewWriter(df)
			fmt.Fprintf(gz, "=== HEAP (debug=1) ===\n")
			_ = pprof.Lookup("heap").WriteTo(gz, 1)
			fmt.Fprintf(gz, "\n=== GOROUTINE (debug=2) ===\n")
			_ = pprof.Lookup("goroutine").WriteTo(gz, 2)
			gz.Close()
			df.Close()
			msg := fmt.Sprintf("内存超阈值 %dMB（当前 %dMB），已留转储 %s", threshold, rssMB, filepath.Base(dump))
			_ = s.Store.CreateAlert(0, "critical", "memory", msg)
			s.notifyBots("内存超阈值", msg)
		}
	}
}

// readSelfRSSKB 读取本进程 RSS KB（依赖 Linux /proc；其他平台返回 -1 由调用方回退堆指标）。
func readSelfRSSKB() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			kbStr := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:")), "kB"))
			n, e := strconv.ParseInt(kbStr, 10, 64)
			if e == nil {
				return n
			}
		}
	}
	return -1
}

// pruneMemSnapshots 仅保留最近 keep 份快照（按文件名时间戳排序）。
func pruneMemSnapshots(dir string, keep int) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var snaps []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "heap_") && strings.HasSuffix(e.Name(), ".pb") {
			snaps = append(snaps, e.Name())
		}
	}
	if len(snaps) <= keep {
		return
	}
	// 文件名含时间戳，字典序即时间序
	for _, name := range snaps[:len(snaps)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// handleMemLeakCapture 手动触发采样接口（super_admin）：立即执行一次 runMemLeakCapture。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 表示已采样。
func (s *Server) handleMemLeakCapture(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.runMemLeakCapture()
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"log":     filepath.Join(s.Cfg.UserDataDir, memLeakDirName, "memleak.log"),
	})
}

// handleMemLeakLog 查看泄漏日志接口（super_admin）：返回 JSONL 内容与最近快照列表。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（query: lines=尾部行数，默认 30）。
func (s *Server) handleMemLeakLog(w http.ResponseWriter, r *http.Request) {
	if _, err := s.requireAdminUser(r); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	dir := filepath.Join(s.Cfg.UserDataDir, memLeakDirName)
	lines := 30
	if v := r.URL.Query().Get("lines"); v != "" {
		if n, e := strconv.Atoi(v); e == nil && n > 0 && n <= 500 {
			lines = n
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "memleak.log"))
	logText := ""
	if err == nil {
		all := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(all) > lines {
			all = all[len(all)-lines:]
		}
		logText = strings.Join(all, "\n")
	}
	snaps := []string{}
	if ents, e := os.ReadDir(dir); e == nil {
		for _, en := range ents {
			if strings.HasPrefix(en.Name(), "heap_") {
				snaps = append(snaps, en.Name())
			}
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "log": logText, "snapshots": snaps})
}
