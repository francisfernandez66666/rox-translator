// ============ ticketartifacts.go · 职责说明 ============
// store 包「工单交付产物」数据层（ticket_artifacts 表，★ 2026-10-30 组5 两段式交付）。
//
// 为什么要有这张表（而不是继续靠 result_path 一个字符串）：
//   - 旧形态把「多语言产物」打成一个 zip 存进 tickets.result_path / ticket_files.result_path，
//     详情面只能把 zip 打开列条目名——**产物一旦进了 zip 就没有身份**（哪一语种、哪一种、
//     是不是降级件全靠文件名猜），而两段式交付要求「md 先到、原格式后到」这两个时刻可见，
//     zip 天然只表达"最终态"。
//   - 本表按 (ticket_id, file_id, lang, kind, path) 唯一登记每一件产物，`kind` 只有两档
//     （md／original，口径见 AGENTS §一·14 与方案 §5.1）。同键 upsert 幂等：引擎重发同一件
//     （例如写回失败后把 md 标成 degraded）不会长出第二行。
//
// 本文件是新文件（AGENTS §一·1 store 冻结规则：新方法落新域文件，不往 billing.go/kbpackages.go 加）。
// 建表语句在 store.go 的建表列表里（`CREATE TABLE IF NOT EXISTS` 幂等，经 db.ExecDDL 走方言翻译）。
// 所有 SQL 同时服务 SQLite 与 PostgreSQL（AGENTS §一·4）：唯一键冲突用 ON CONFLICT，两者都支持。
// =============================================
package store

import (
	"database/sql"
	"time"
	"translator/internal/db"
)

// 产物 kind 的两档值（对外契约：详情出栈、下载、预览都按这两档分派，别在各调用方自造第三档）。
const (
	TicketArtifactKindMD       = "md"       // 纯文案译文（预览/下载 md）
	TicketArtifactKindOriginal = "original" // 该语言主产物：回写件 / 译后图 / 译后配音
)

// TicketArtifact 一件工单交付产物。
// 字段口径：
//   - FileID 对应 ticket_files.id；单文件（旧形态）工单为 0；
//   - Path 是磁盘绝对路径（**只从库里取**，下载端点不接受用户传入的路径——防目录穿越）；
//   - Name 是下载展示名（与 zip 条目名同一口径：取 Path 的 basename，旧客户端零改动）；
//   - Primary 表示「该语言『下载原格式』的目标件」（同路径去重时 md 行也会被置 1）；
//   - Degraded 表示这一件是降级交付（原格式没生成出来，只剩 md），UI 据此禁用右钮并给原因。
type TicketArtifact struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	TicketID  int64  `json:"ticket_id"`
	FileID    int64  `json:"file_id"`
	Lang      string `json:"lang"`
	Kind      string `json:"kind"`
	Path      string `json:"-"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Primary   bool   `json:"primary"`
	Degraded  bool   `json:"degraded"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"-"`
}

// ticketArtifactCols 通用查询列清单（Scan 顺序契约；created_at/updated_at 可空，COALESCE 兜底）。
const ticketArtifactCols = "id, tenant_id, ticket_id, file_id, lang, kind, path, name, size, is_primary, degraded, COALESCE(created_at,''), COALESCE(updated_at,'')"

// scanTicketArtifact 按 ticketArtifactCols 的顺序解一行（List/Get 共用，避免两处列序漂移）。
func scanTicketArtifact(sc interface{ Scan(...interface{}) error }) (*TicketArtifact, error) {
	var a TicketArtifact
	var primary, degraded int
	if err := sc.Scan(&a.ID, &a.TenantID, &a.TicketID, &a.FileID, &a.Lang, &a.Kind, &a.Path, &a.Name,
		&a.Size, &primary, &degraded, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Primary = primary != 0
	a.Degraded = degraded != 0
	return &a, nil
}

// UpsertTicketArtifact 登记一件产物（同键幂等）。
// 唯一键 (ticket_id, file_id, lang, kind, path)：同一次交付重发同一件只会更新，
// 而「同一语种的 md 与 original」是两行（kind 不同），不会互相顶掉。
// 返回：落库后的行（带 ID；路径/名称等以本次入参为准）。
func (s *Store) UpsertTicketArtifact(a *TicketArtifact) (*TicketArtifact, error) {
	if a == nil {
		return nil, nil
	}
	now := time.Now().Format(time.RFC3339)
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	primary, degraded := 0, 0
	if a.Primary {
		primary = 1
	}
	if a.Degraded {
		degraded = 1
	}
	// ON CONFLICT 的目标必须是那条唯一索引的列（顺序无关，列集合必须在位）。
	// 只更新"可演进"的列：name/size/primary/degraded 由后到的同一件覆盖；
	// tenant_id 一并跟随后到值（OpenAPI 任务等场景下租户是同一份，写回它可修历史脏行）。
	const q = `INSERT INTO ticket_artifacts
		(tenant_id, ticket_id, file_id, lang, kind, path, name, size, is_primary, degraded, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(ticket_id, file_id, lang, kind, path) DO UPDATE SET
			tenant_id=excluded.tenant_id,
			name=excluded.name,
			size=excluded.size,
			is_primary=excluded.is_primary,
			degraded=excluded.degraded,
			updated_at=excluded.updated_at`
	if _, err := db.Exec(s.db, db.CurrentDialect(), q,
		a.TenantID, a.TicketID, a.FileID, a.Lang, a.Kind, a.Path, a.Name, a.Size, primary, degraded, a.CreatedAt, a.UpdatedAt); err != nil {
		return nil, err
	}
	// 回读取回真实 ID（冲突分支不会返回新 ID；这里按唯一键定位，语义与「同键一行」一致）。
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+ticketArtifactCols+" FROM ticket_artifacts WHERE ticket_id=? AND file_id=? AND lang=? AND kind=? AND path=?",
		a.TicketID, a.FileID, a.Lang, a.Kind, a.Path)
	got, err := scanTicketArtifact(row)
	if err == sql.ErrNoRows {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	return got, nil
}

// ListTicketArtifacts 列出某工单的全部产物（按 file_id、lang、id 升序——即「同一组的 md 在 original 之前」）。
// 顺序稳定是详情面与按需打包的共同前提：调用方不会再排一次序（两处各排一遍迟早不一致）。
func (s *Store) ListTicketArtifacts(ticketID int64) ([]*TicketArtifact, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+ticketArtifactCols+" FROM ticket_artifacts WHERE ticket_id=? ORDER BY file_id, lang, id", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TicketArtifact{}
	for rows.Next() {
		a, serr := scanTicketArtifact(rows)
		if serr != nil {
			continue // 单行解析失败跳过（历史脏行不该让整张清单读不出来）
		}
		out = append(out, a)
	}
	return out, nil
}

// GetTicketArtifact 取单件产物（强制 ticket_id 归属：aid 不属于该单即查不到 ⇒ 上层回 404）。
// 参数：id=产物 ID；ticketID=工单 ID（来自已鉴权的详情/下载路径，绝不由客户端直接给表）。
func (s *Store) GetTicketArtifact(id, ticketID int64) (*TicketArtifact, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+ticketArtifactCols+" FROM ticket_artifacts WHERE id=? AND ticket_id=?", id, ticketID)
	a, err := scanTicketArtifact(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// FindTicketArtifact 按唯一键（ticket,file,lang,kind,path）查单行；查不到返回 (nil, nil)。
// 用途是服务层的**同路径收敛**：收到 original 时要问「同一路径上是不是已经有一行 md」
// （.md 输入件的 md 与 original 是同一份文件）——问到了就把那一行置主产物，不插第二行。
func (s *Store) FindTicketArtifact(ticketID, fileID int64, lang, kind, path string) (*TicketArtifact, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+ticketArtifactCols+" FROM ticket_artifacts WHERE ticket_id=? AND file_id=? AND lang=? AND kind=? AND path=?",
		ticketID, fileID, lang, kind, path)
	a, err := scanTicketArtifact(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return a, nil
}

// SetTicketArtifactPrimary 把某行标成该语种主产物（同路径去重：.md 输入件的 md 行即主产物）。
// 为什么单独一个方法而不复用 Upsert：调用方（服务层 sink）手里只有 DB 行、没有完整的 Artifact 载荷，
// 硬拼一份再 upsert 会把 name/size 一起写坏（那些值来自落盘时那一刻）。
func (s *Store) SetTicketArtifactPrimary(id int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE ticket_artifacts SET is_primary=1, updated_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
	return err
}

// TicketArtifactPaths 该工单全部产物的磁盘路径（去重，空路径剔除）。
// 保留期清理与 GDPR 擦除共用：只删行不删文件＝磁盘上留下永久孤儿（B11/W25 的判据正是这两件事）。
func (s *Store) TicketArtifactPaths(ticketID int64) ([]string, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT path FROM ticket_artifacts WHERE ticket_id=? AND path != ''", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	out := []string{}
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil || p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// DeleteTicketArtifactsForTicket 删除某工单的全部产物行。
// 口径：产物行是「这张单交付过什么」的唯一事实源，单据被清理/删除后它必须一起消失；
// 文件由调用方按 TicketArtifactPaths 收集后 best-effort 删除（行的删除在这里，文件不归 store 管）。
func (s *Store) DeleteTicketArtifactsForTicket(ticketID int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM ticket_artifacts WHERE ticket_id=?", ticketID)
	return err
}
