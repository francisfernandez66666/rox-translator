// ============ backup.go · 职责说明 ============
// store 包数据库备份实现。
// 提供在线安全备份，跨方言。
//   - SQLite：VACUUM INTO 生成一致性快照，运行中不阻塞读写。
//   - PostgreSQL：走 pg_dump（自定义格式，可由 pg_restore 还原），连接串取自全局配置。
//
// 备份文件默认保留最近 N 份（自动清理旧备份，防止磁盘耗尽）。
// =============================================
package store

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"translator/internal/config"
	"translator/internal/db"
)

// Backup 在线备份数据库到目标目录（跨方言）。
// 参数：backupDir=备份目录（自动创建），dbPath=源数据库路径（仅用于命名）。
// 返回：备份文件完整路径。
//   - SQLite：VACUUM INTO 由 SQLite 原生支持（3.27+），生成的 .bak.db 可直接作为新库打开。
//   - PostgreSQL：pg_dump --format=custom，生成的 .bak.dump 可由 pg_restore 还原。
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
	d := db.CurrentDialect()
	if d == db.DialectPostgres {
		// PostgreSQL：pg_dump 连接串取自全局配置（与运行期一致）
		dest := filepath.Join(backupDir, fmt.Sprintf("%s_%s.bak.dump", name, time.Now().Format("20060102_150405")))
		if err := pgDumpBackup(dest); err != nil {
			return "", fmt.Errorf("备份失败: %w", err)
		}
		return dest, nil
	}
	// 备份文件名：<库名>_<YYYYMMDD_HHMMSS>.bak.db
	dest := filepath.Join(backupDir, fmt.Sprintf("%s_%s.bak.db", name, time.Now().Format("20060102_150405")))
	// VACUUM INTO：在线一致性快照
	if _, err := db.Exec(s.db, d, "VACUUM INTO '"+strings.ReplaceAll(dest, "'", "''")+"'"); err != nil {
		return "", fmt.Errorf("备份失败: %w", err)
	}
	return dest, nil
}

// pgDumpBackup 使用 pg_dump 生成 PostgreSQL 一致性快照（自定义格式）。
func pgDumpBackup(dest string) error {
	dsn := config.C.DatabaseDSN
	if dsn == "" {
		return fmt.Errorf("缺少 DatabaseDSN，无法执行 pg_dump")
	}
	cmd := exec.Command("pg_dump", "--format=custom", "--no-owner", "--file", dest, dsn)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pg_dump 失败: %v: %s", err, out)
	}
	return nil
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
