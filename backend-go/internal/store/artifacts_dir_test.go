// ============ artifacts_dir_test.go 职责说明 ============
// RemoveEmptyArtifactDir 的行为回归（#65 产物分目录的收尾半边）。
// 产物改成 translated/<落盘名>/ 后，保留期清理与删单只删文件会留下永久空目录，
// 本函数负责回收，但**只准**命中产物子目录：
//   - 目录已空且在 translated/ 之下 ⇒ 删除；
//   - 目录非空 ⇒ 原地保留（绝不能连带删掉同目录里还没到期的产物）；
//   - 不在 translated/ 之下（原件在 tickets/ 根、上传根目录）⇒ 一律不动，即使它是空的。
//
// 纯文件系统断言，不起服务、不碰数据库方言。
// ======================================================
package store

import (
	"os"
	"path/filepath"
	"testing"
)

// mkArtifactTree 造一棵「translated/<落盘名>/产物」目录树，返回产物文件路径与子目录路径。
// keepSibling=true 时同目录多放一份产物，用来模拟「同一上传件的多语言产物只清理了一部分」，
// 这是判空回收最容易误删的场景（每个用例都用独立的 t.TempDir()，互不干扰）。
func mkArtifactTree(t *testing.T, keepSibling bool) (artifact, dir string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "tickets", "translated", "1790026522069352000_报价单")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("造目录失败: %v", err)
	}
	artifact = filepath.Join(dir, "报价单_en.docx")
	if err := os.WriteFile(artifact, []byte("x"), 0o644); err != nil {
		t.Fatalf("造产物失败: %v", err)
	}
	if keepSibling {
		if err := os.WriteFile(filepath.Join(dir, "报价单_ja.docx"), []byte("y"), 0o644); err != nil {
			t.Fatalf("造旁物失败: %v", err)
		}
	}
	return artifact, dir
}

// TestRemoveEmptyArtifactDirRemovesOnlyEmptiedArtifactDir 空了就删、没空就留。
func TestRemoveEmptyArtifactDirRemovesOnlyEmptiedArtifactDir(t *testing.T) {
	artifact, dir := mkArtifactTree(t, false)
	if err := os.Remove(artifact); err != nil {
		t.Fatalf("删产物失败: %v", err)
	}
	RemoveEmptyArtifactDir(artifact)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("产物子目录已空却没被回收：%v", err)
	}

	// 同一语言目录里还有未清理的产物：目录必须原样保留（误删等于删用户交付物）
	artifact2, dir2 := mkArtifactTree(t, true)
	if err := os.Remove(artifact2); err != nil {
		t.Fatalf("删产物失败: %v", err)
	}
	RemoveEmptyArtifactDir(artifact2)
	if _, err := os.Stat(dir2); err != nil {
		t.Fatalf("目录内仍有产物却被删了：%v", err)
	}
}

// TestRemoveEmptyArtifactDirNeverTouchesNonArtifactDirs 上传件目录（tickets/ 根）与空根目录
// 都不在 translated/ 之下，一律不得动——这条是「清理脚本顺手删错目录」的负向锁。
func TestRemoveEmptyArtifactDirNeverTouchesNonArtifactDirs(t *testing.T) {
	root := t.TempDir()
	ticketsDir := filepath.Join(root, "tickets")
	if err := os.MkdirAll(ticketsDir, 0o755); err != nil {
		t.Fatalf("造目录失败: %v", err)
	}
	src := filepath.Join(ticketsDir, "1790026522069352000_报价单.docx")
	// 产物还在上传根目录里（未分进 translated/ 的旧数据/上传原件）：只准不动
	RemoveEmptyArtifactDir(src)
	if _, err := os.Stat(ticketsDir); err != nil {
		t.Fatalf("上传目录被误删：%v", err)
	}
	RemoveEmptyArtifactDir("") // 空路径（工单没有该字段）不得panic、不得删任何东西
	// 空串路径会让 filepath.Dir 逐级退化成 "."，若不显式短路就可能删到当前工作目录，
	// 故这里复查上传目录仍在：确认「短路」真的短路了。
	if _, err := os.Stat(ticketsDir); err != nil {
		t.Fatalf("空路径调用误删了上传目录：%v", err)
	}
}
