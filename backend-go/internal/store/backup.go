// ============ 本文件职责中文说明 ============
// 数据库备份：提供在线安全备份（SQLite VACUUM INTO），
// 在服务运行中也可生成一致性快照，不阻塞读写。
// 备份文件默认保留最近 N 份（自动清理旧备份，防止磁盘耗尽）。
// =============================================
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup 在线备份数据库到目标目录（VACUUM INTO 生成一致性快照）。
// 参数：backupDir=备份目录（自动创建），dbPath=源数据库路径（仅用于命名）。
// 返回：备份文件完整路径。
// 说明：VACUUM INTO 由 SQLite 原生支持（3.27+），对运行中的库安全，
//
//	生成的 .db 文件可直接作为新库打开。
func (s *Store) Backup(backupDir, dbPath string) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("数据库连接未初始化")
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	base := filepath.Base(dbPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	// 备份文件名：<库名>_<YYYYMMDD_HHMMSS>.bak.db
	dest := filepath.Join(backupDir, fmt.Sprintf("%s_%s.bak.db", name, time.Now().Format("20060102_150405")))
	// VACUUM INTO：在线一致性快照
	if _, err := s.db.Exec("VACUUM INTO '" + strings.ReplaceAll(dest, "'", "''") + "'"); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	return dest, nil
}

// PruneBackups 清理备份目录，仅保留最近 keep 份（按文件名时间戳排序）。
// 参数：backupDir=备份目录，prefix=备份文件名前缀（如 "tm"），keep=保留份数。
func PruneBackups(backupDir, prefix string, keep int) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		files = append(files, filepath.Join(backupDir, e.Name()))
	}
	// 按名称（时间戳）升序排序
	sort.Strings(files)
	// 删除超出保留数量的最旧文件
	for i := 0; i < len(files)-keep; i++ {
		_ = os.Remove(files[i])
	}
}
