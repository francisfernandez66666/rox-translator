// ============ sink_spool.go · 职责说明 ============
// 实时计量缓冲的磁盘兜底（★ #39 2026-09-21 评审缺陷⑩：sink 落库无持久化）。
//
// 背景：B2 把每次 LLM 调用的写事务收敛成「内存缓冲 + 每 2s 批量落库」，
// 换来吞吐但也留下收入泄漏窗口——进程退出（优雅停机最终 flush 失败 / DB 长时间不可写）
// 时缓冲里的用量既不落 ledger 也不扣费，账上完全无痕。
//
// 本文件补两件事：
//   - Stop 后仍有残留 → 逐条写 JSONL spool 文件（USER_DATA_DIR/_billing_spool），不静默丢弃；
//   - 启动时回收 spool：重命名占用（多实例只有一台能拿到某个文件）→ 读回缓冲 → 落库成功后删除。
//
// 已知边界：SIGKILL/OOM 直接杀进程时来不及 spool，窗口仍 ≤ flush 周期（2s），
// 该窗口由「触顶丢弃告警」+ 优雅停机（main.go 先 Shutdown 再 Stop sink）共同收敛。
// =============================================
package billing

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"translator/internal/config"
	"translator/internal/observability"
)

// sinkSpoolRecord spool 文件的单行结构（usageRecord 的可序列化子集：Abort 回调跨进程无意义）。
type sinkSpoolRecord struct {
	Tid      int64  `json:"tid"`
	UID      int64  `json:"uid"`
	TaskType string `json:"task_type"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Lang     string `json:"lang"`
	Quantity int64  `json:"quantity"`
	BizKind  string `json:"biz_kind"`
	BizMode  string `json:"biz_mode"`
	When     string `json:"when"` // RFC3339：保留用量实际发生时刻，落账不因重启而错日
}

// sinkSpoolDir spool 目录（跟随 USER_DATA_DIR，与上传/备份同根，备份策略天然覆盖）。
func sinkSpoolDir() string {
	root := strings.TrimSpace(config.C.UserDataDir)
	if root == "" {
		root = "."
	}
	return filepath.Join(root, "_billing_spool")
}

// spoolPending 把缓冲里仍未落库的记录写入一个带时间戳的 JSONL 文件。
// 返回写入条数与文件名；写失败只告警（停机路径不再向上抛错，避免掩盖原始故障）。
func (s *UsageSink) spoolPending() (int, string) {
	s.mu.Lock()
	recs := s.buf
	s.buf = nil
	s.mu.Unlock()
	if len(recs) == 0 {
		return 0, ""
	}
	dir := sinkSpoolDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		observability.Error(context.Background(), "计量 spool 目录创建失败（缓冲丢弃）", "dir", dir, "err", err.Error())
		return 0, ""
	}
	name := filepath.Join(dir, fmt.Sprintf("pending-%d-%d.jsonl", time.Now().UnixNano(), os.Getpid()))
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		observability.Error(context.Background(), "计量 spool 文件创建失败（缓冲丢弃）", "file", name, "err", err.Error())
		return 0, ""
	}
	w := bufio.NewWriter(f)
	n := 0
	var werr error
	for _, r := range recs {
		line, e := json.Marshal(sinkSpoolRecord{
			Tid: r.Tid, UID: r.UID, TaskType: r.TaskType, Provider: r.Provider, Model: r.Model,
			Lang: r.Lang, Quantity: r.Quantity, BizKind: r.BizKind, BizMode: r.BizMode,
			When: r.When.Format(time.RFC3339),
		})
		if e != nil {
			werr = e
			continue
		}
		if _, e := w.Write(line); e != nil {
			werr = e
			break
		}
		if e := w.WriteByte('\n'); e != nil {
			werr = e
			break
		}
		n++
	}
	if e := w.Flush(); e != nil {
		werr = e
	}
	_ = f.Close()
	if werr != nil {
		observability.Error(context.Background(), "计量 spool 写入中断", "file", name, "written", fmt.Sprint(n), "err", werr.Error())
	}
	if n > 0 {
		observability.Warn(context.Background(), "停机前仍有未落库计量，已转磁盘 spool", "file", name, "rows", fmt.Sprint(n))
	}
	return n, name
}

// recoverSpool 启动时回收历史 spool：逐文件重命名占用（多实例并发只有一台成功）后读回缓冲。
// 返回恢复条数；调用方（InitGlobalSink）随后交给 flusher 正常落库，落库成功即删文件。
func (s *UsageSink) recoverSpool() int {
	dir := sinkSpoolDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0 // 目录不存在=无残留，正常
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // 旧文件先进缓冲，保持用量发生次序
	total := 0
	for _, nm := range names {
		src := filepath.Join(dir, nm)
		claim := src + fmt.Sprintf(".loading-%d", os.Getpid())
		if err := os.Rename(src, claim); err != nil {
			continue // 已被其他实例占用或正被写，下次再说
		}
		f, err := os.Open(claim)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		var recs []usageRecord
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var sr sinkSpoolRecord
			if json.Unmarshal([]byte(line), &sr) != nil {
				continue // 半行（写入中断）直接跳过，缓冲里其余记录仍可落账
			}
			when, perr := time.Parse(time.RFC3339, sr.When)
			if perr != nil {
				when = time.Now()
			}
			recs = append(recs, usageRecord{
				Tid: sr.Tid, UID: sr.UID, TaskType: sr.TaskType, Provider: sr.Provider, Model: sr.Model,
				Lang: sr.Lang, Quantity: sr.Quantity, BizKind: sr.BizKind, BizMode: sr.BizMode, When: when,
			})
		}
		_ = f.Close()
		if len(recs) == 0 {
			_ = os.Remove(claim)
			continue
		}
		s.mu.Lock()
		s.buf = append(recs, s.buf...) // 补在队首：先还旧账
		s.mu.Unlock()
		// spool 文件的生命周期到「读回缓冲」为止：落库若再失败，会由停机路径重新 spool。
		_ = os.Remove(claim)
		total += len(recs)
	}
	if total > 0 {
		observability.Warn(context.Background(), "已从磁盘 spool 恢复未落库计量", "rows", fmt.Sprint(total))
	}
	return total
}
