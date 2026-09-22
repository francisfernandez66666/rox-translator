// ============ artifacts.go · 职责说明 ============
// 产物/上传件归属登记表（评审整改 C1）：
// /api/download 具备 JWT+目录白名单后，仍缺「租户/用户归属」维度——白名单内任意登录
// 用户可读任意租户的产物。本表在写入点登记 path→(tenant,user,ticket) 归属，
// 下载时按归属判定；未登记的历史产物按 Phase1 灰度放行并留审计（一个产物保留周期后收紧）。
// 另挂产物目录收尾工具 RemoveEmptyArtifactDir（#65 每上传件独立子目录后的空目录清理）。
// =============================================
package store

import (
	"os"
	"path/filepath"
	"time"
	"translator/internal/db"
)

// Artifact 归属登记行
// 字段口径：TicketID=0 表示即时翻译（/api/translate 与 SSE 文件通道）产出的产物、无关联工单行；
// CreatedAt 为 RFC3339 文本，列允许 NULL，读取侧统一 COALESCE 成空串（免用 sql.NullString）。
type Artifact struct {
	ID        int64
	Path      string
	TenantID  int64
	UserID    int64
	TicketID  int64
	CreatedAt string
}

// ArtifactsMigrate 建表与唯一索引（幂等，Store.New 迁移链调用）。
//
// 唯一键口径是 (tenant_id, path)：同一路径在不同租户下各有一行归属，
// 租户隔离才能成立（旧形态只约束 path，会让第二个租户登记不进去）。
// 老库若已按「仅 path」建了唯一索引，SQLite 不支持 ALTER 约束，只能在事务里
// 走「建新表 → 整表搬运 → 删旧表 → 改名」重建；检测手段（PRAGMA）是 SQLite 专属，
// 所以整个探测/重建分支只在 SQLite 方言下执行，PG 侧仍由 CREATE TABLE IF NOT EXISTS
// 保证新库形态正确。
func (s *Store) ArtifactsMigrate() {
	d := db.CurrentDialect()
	needRebuild := false
	if d == db.DialectSQLite {
		var tableExists int
		// 表不存在时无需重建（下面直接走 CREATE TABLE IF NOT EXISTS 建正确形态）
		if err := db.QueryRow(s.db, d, `SELECT 1 FROM sqlite_master WHERE type='table' AND name='output_artifacts'`).Scan(&tableExists); err != nil {
			tableExists = 0
		}
		if tableExists == 1 {
			// 逐个唯一索引取其列清单：命中「恰好单列 path」这一旧形态才判定需要重建；
			// 已是 (tenant_id, path) 复合唯一键（或建表时的表级 UNIQUE 约束）则原样放过，
			// 于是整个方法在正确库上重复执行无副作用 —— 幂等要求。
			rows, err := db.Query(s.db, d, `PRAGMA index_list('output_artifacts')`)
			if err == nil {
				for rows.Next() {
					var seq, unique, partial int
					var name, origin string
					if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
						continue
					}
					if unique != 1 {
						continue
					}
					irows, err := db.Query(s.db, d, `PRAGMA index_info(?)`, name)
					if err != nil {
						continue
					}
					cols := []string{}
					for irows.Next() {
						var seqno, cid int
						var colName string
						if err := irows.Scan(&seqno, &cid, &colName); err == nil {
							cols = append(cols, colName)
						}
					}
					irows.Close()
					if len(cols) == 1 && cols[0] == "path" {
						needRebuild = true
						break
					}
				}
				rows.Close()
			}
		}
	}
	if needRebuild {
		// 重建必须在单个事务里完成：中途失败（进程被杀/磁盘满）不能留下「旧表已删、
		// 新表半截」的产物归属真空——归属表一旦缺行，下载侧就退化成灰度放行。
		tx, err := s.db.Begin()
		if err != nil {
			return
		}
		// INSERT ... SELECT * 依赖两表列序完全一致（新表定义即本方法下方 CREATE TABLE 的形态），
		// 改列时必须同步改这里，否则会把值搬到错位列上。
		db.Exec(tx, d, `CREATE TABLE output_artifacts_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			ticket_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT,
			UNIQUE(tenant_id, path))`)
		db.Exec(tx, d, `INSERT INTO output_artifacts_new SELECT * FROM output_artifacts`)
		db.Exec(tx, d, `DROP TABLE output_artifacts`)
		db.Exec(tx, d, `ALTER TABLE output_artifacts_new RENAME TO output_artifacts`)
		tx.Commit()
		return
	}
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS output_artifacts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		ticket_id INTEGER NOT NULL DEFAULT 0,
		created_at TEXT,
		UNIQUE(tenant_id, path))`)
}

// RegisterArtifact 写入点登记（幂等：同租户内同 path 重复登记忽略）。
// 参数：path=产物/上传件绝对路径；tid=归属租户；uid=归属用户；ticketID=关联工单（可 0）。
// 登记动作发生在翻译完成之后，属于「附加信息」：写失败只丢一条归属行（下载侧走 Phase1
// 灰度放行），绝不允许把已经成功的翻译回成失败，故这里不返回 error。
// 方言：SQL 按 SQLite 的 INSERT OR IGNORE 写，PG 下由 internal/db 的改写层自动翻成
// ON CONFLICT DO NOTHING，两侧语义一致（冲突即忽略，不报错）。
func (s *Store) RegisterArtifact(path string, tid, uid, ticketID int64) {
	if path == "" {
		return
	}
	db.Exec(s.db, db.CurrentDialect(), `INSERT OR IGNORE INTO output_artifacts (path, tenant_id, user_id, ticket_id, created_at)
		VALUES (?,?,?,?,?)`, path, tid, uid, ticketID, time.Now().Format(time.RFC3339))
}

// GetArtifactByPath 按路径查归属；不存在返回 (nil, err)。
// 下载侧此刻手上只有绝对路径，归属只能反查，故 WHERE 不带 tenant_id。
// 唯一键含 tenant_id，理论上同一路径可有多行；这里取第一行（QueryRow）做判定。
// 实际不会歧义：落盘名带纳秒时间戳且产物按上传件分子目录，绝对路径不可能跨租户重复。
// COALESCE 是双方言的 NULL 兜底写法（created_at 允许 NULL，直接 Scan 进 string 会报错）。
func (s *Store) GetArtifactByPath(path string) (*Artifact, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id, path, tenant_id, user_id, ticket_id, COALESCE(created_at,'') FROM output_artifacts WHERE path=?", path)
	var a Artifact
	if err := row.Scan(&a.ID, &a.Path, &a.TenantID, &a.UserID, &a.TicketID, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}

// RemoveEmptyArtifactDir 产物文件删除后的收尾：它所在的「每上传件产物子目录」若已空就删掉。
// 参数 artifactPath: 刚被删除的产物/上传件绝对路径（空串直接返回）。
//
// ★ #65 配套（产物目录由共用平铺改为 translated/<落盘名>/ 后新增）：保留期清理与删单只删文件
//
//	会把空目录永久留在盘上，目录数随工单线性增长。判据取「父目录名必须是 translated」，
//	即只可能命中产物子目录，绝不会去删上传根目录（tickets/、_uploads/）；
//	且只用 os.Remove——非空目录必然失败，无需先 Stat 再猜（也免掉 TOCTOU）。
func RemoveEmptyArtifactDir(artifactPath string) {
	if artifactPath == "" {
		return
	}
	dir := filepath.Dir(artifactPath)
	if filepath.Base(filepath.Dir(dir)) != "translated" {
		return
	}
	_ = os.Remove(dir)
}
