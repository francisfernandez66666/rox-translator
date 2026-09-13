// ============================================================================
// H5 分片续传底层测试：分片命名/收齐判定/TTL 清理（handler 级鉴权由 UAT 覆盖）。
// ============================================================================
package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"translator/internal/config"
)

func h5Server(t *testing.T) *Server {
	s := &Server{}
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 防 UAT PG 矩阵 env 泄漏方言（Default 副作用同指针，一并修全局）
	cfg.UploadDir = t.TempDir()
	s.Cfg = cfg
	return s
}

func TestH5ChunkNamingAndCollection(t *testing.T) {
	if chunkPartName(0) != "idx_00000.part" || chunkPartName(12345) != "idx_12345.part" {
		t.Fatal("分片命名错误")
	}
	// 字典序 == 数值序（merge 依赖目录排序）
	if chunkPartName(9) >= chunkPartName(10) {
		t.Fatal("命名排序破坏连续性校验前提")
	}
	s := h5Server(t)
	dir := s.chunkDirPath("abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, i := range []int{0, 1, 2} {
		if err := os.WriteFile(filepath.Join(dir, chunkPartName(i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "meta"), []byte("3"), 0o644)
	rec := receivedChunks(dir)
	if len(rec) != 3 || rec[0] != 0 || rec[2] != 2 {
		t.Fatalf("已收分片解析错误: %v", rec)
	}
	// 缺一片 → 收不齐
	os.Remove(filepath.Join(dir, chunkPartName(1)))
	if len(receivedChunks(dir)) != 2 {
		t.Fatal("删除分片未被感知")
	}
}

func TestH5SweepOldChunks(t *testing.T) {
	s := h5Server(t)
	root := filepath.Join(s.Cfg.UploadDir, "_chunks")
	old := filepath.Join(root, "expired00")
	_ = os.MkdirAll(old, 0o755)
	_ = os.WriteFile(filepath.Join(old, chunkPartName(0)), []byte("x"), 0o644)
	// 伪造成 48h 前
	oldTime := s.chunkDirPath("fresh00000")
	_ = os.MkdirAll(oldTime, 0o755)
	past := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, past, past)
	s.sweepOldChunks()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("过期分片目录应被清理")
	}
	if _, err := os.Stat(oldTime); err != nil {
		t.Fatal("新鲜目录不应被误删")
	}
}
