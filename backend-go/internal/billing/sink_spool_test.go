// ============ 本文件职责中文说明 ============
// #39（2026-09-21 评审缺陷⑩）计量 spool 持久化单测：
//
//	① 停机残留缓冲能落成 JSONL 文件；
//	② 下次启动能从文件读回缓冲并保持用量发生时刻（错日会错账）；
//	③ 半行损坏（写入中断）只跳过该行，不牵连其余记录。
//
// ★ 方言自钉：本用例不碰数据库，只钉 config.C 的 UserDataDir 到临时目录并 Cleanup 还原，
//
//	避免污染同包其它用例与真实数据根目录。
package billing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"translator/internal/config"
)

// useSpoolDir 把 spool 根目录指到用例临时目录，返回该目录。
func useSpoolDir(t *testing.T) string {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.UserDataDir = t.TempDir()
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return cfg.UserDataDir
}

// TestSpoolRoundTrip 落盘 → 回收：条数、字段、发生时刻三者无损往返。
func TestSpoolRoundTrip(t *testing.T) {
	root := useSpoolDir(t)
	when := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	s := &UsageSink{buf: []usageRecord{
		{Tid: 7, UID: 3, TaskType: "chat", Provider: "openai", Model: "gpt-x", Lang: "en", Quantity: 1200, BizKind: "ticket", BizMode: "pro", When: when},
		{Tid: 8, UID: 4, TaskType: "embed", Provider: "openai", Model: "emb-1", Quantity: 30, BizKind: "kb", BizMode: "fast", When: when},
	}}
	n, name := s.spoolPending()
	if n != 2 || name == "" {
		t.Fatalf("spool 应写 2 条到文件，实际 n=%d name=%q", n, name)
	}
	if _, err := os.Stat(name); err != nil {
		t.Fatalf("spool 文件不存在: %v", err)
	}
	if len(s.buf) != 0 {
		t.Fatalf("spool 后缓冲应清空，实际 %d 条", len(s.buf))
	}
	// 新实例（模拟重启）回收
	s2 := &UsageSink{}
	if got := s2.recoverSpool(); got != 2 {
		t.Fatalf("回收应为 2 条，实际 %d", got)
	}
	if len(s2.buf) != 2 {
		t.Fatalf("回收后缓冲 %d 条，期望 2", len(s2.buf))
	}
	head := s2.buf[0]
	if head.Tid != 7 || head.Quantity != 1200 || head.Model != "gpt-x" || head.BizMode != "pro" {
		t.Fatalf("回收记录字段失真: %+v", head)
	}
	if !head.When.Equal(when) {
		t.Fatalf("发生时刻未保持（错日会错账）: %v != %v", head.When, when)
	}
	// 回收后目录应清空（文件不残留，避免重复补账）
	entries, _ := os.ReadDir(filepath.Join(root, "_billing_spool"))
	if len(entries) != 0 {
		t.Fatalf("spool 文件未清理，残留 %d 项", len(entries))
	}
}

// TestSpoolSkipsCorruptLine 半行损坏（写入中断/磁盘抖动）只丢该行，其余照常回收。
func TestSpoolSkipsCorruptLine(t *testing.T) {
	useSpoolDir(t)
	dir := sinkSpoolDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	body := `{"tid":7,"uid":3,"task_type":"chat","provider":"openai","model":"gpt-x","quantity":100,"when":"2026-09-21T10:00:00Z"}
{"tid":8,"uid":4,"task_type":"emb` // 第二行故意截断
	name := filepath.Join(dir, "pending-manual.jsonl")
	if err := os.WriteFile(name, []byte(body), 0o640); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
	s := &UsageSink{}
	if got := s.recoverSpool(); got != 1 {
		t.Fatalf("只应恢复 1 条完好记录，实际 %d", got)
	}
	if s.buf[0].Tid != 7 || s.buf[0].Quantity != 100 {
		t.Fatalf("恢复内容不对: %+v", s.buf[0])
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("回收后文件应删除，实际仍存在")
	}
}
