// Package store 聊天对话持久化层（★ 〇-AM：主后台工作台 SSE 翻译通道的后端存储）。
// 两张表：chat_conversations（会话）+ chat_messages（消息），同时兼容 SQLite / PostgreSQL。
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"translator/internal/db"
)

// ChatConversation 一条用户与 AI 的对话会话（工作台 SSE 通道）。
type ChatConversation struct {
	ID        string // 全局唯一 UUID v4
	UserID    int64  // 创建者用户 ID（登录态必填）
	TenantID  int64  // 所属租户
	CreatedAt string // ISO 8601 时间戳
	UpdatedAt string // 最后活跃时间
	Title     string // 自动生成的标题（取第一条用户消息的前 40 字）
}

// ChatMessage 会话中的一条消息（用户提问 / AI 回复）。
type ChatMessage struct {
	ID             int64  // 自增主键
	ConversationID string // FK → chat_conversations.id
	Role           string // "user" | "assistant"
	Content        string // 消息正文（用户输入或 AI 回复文本）
	Model          string // 生成该回复使用的模型名（可空）
	CreatedAt      string // ISO 8601 时间戳
}

// ===== 建表（幂等，SQLite + PG 双方言） =====

// ★ 〇-AP 收口（2026-10-02，全量 -race 抓出的历史存量红）：本文件〇-AM 初版把 CRUD 一律写成裸 `s.db.Exec/Query/QueryRow`
//   （绕过 `db` 方言包装），被 `internal/db/guard_test.go` 的 TestNoRawSQLOutsideDialectWrapper 判红——
//   **这不只是闸门问题，是真的 PG 缺陷**：非分支的读腿（List/Get/Append/Delete/Mark/Export）把 `?` 占位符
//   直接交给 lib/pq（PG 只认 `$n`），生产 PG 下这些语句一律报语法错；`DeleteChatConversation` 还把两条带参
//   DELETE 拼在一次 Exec 里，PG 扩展协议禁止带参多语句、同样必错。
//   修法＝全部走 `db.Exec/db.Query/db.QueryRow(s.db, db.CurrentDialect(), ...)` 唯一安全入口（AGENTS §一·4），
//   `?` 由包装按方言自动改写；分支腿里 SQLite-only 的 `rowid` 与 PG-only 的 `INSERT...RETURNING` 子查询
//   都按各自方言显式传 dialect，避免包装再改写已存在的 `$n` 或把 SQLite 专用语法送进 PG。
//   ★ 收口不止改通道——PG run_uat 真跑时 `server.log` 反复冒 `pq: syntax error at or near "INTO"`（迁移到结构化日志才看得见），
//     定位到 `CreateChatConversation` 的**第二层历史缺陷**：〇-AM 初版 INSERT 从不给 `id TEXT PRIMARY KEY` 赋值，
//     为拿回自增 id 才写成 `FROM (INSERT ... RETURNING *) t` 子查询——这**不是合法 PG 语法**（数据改写子查询须走 WITH CTE）；
//     而 SSE handler 早已自行生成会话 UUID（`convID`）并拿它去 `AppendChatMessage`，创建会话那一腿却把返回值整个丢弃，
//     ⇒ 会话行的 PK 与消息的 `conversation_id` 结构上永远对不上，SQLite 下 PK 落成 NULL、PG 下整条语法错，两边都没真写过一条会话。
//   ⇒ 本次一并改正：`CreateChatConversation` 的 `id` 改为**调用方传入**（与后续消息的 conversation_id 同源同一个 UUID），
//     INSERT 显式带 id、两支合一（不再有 rowid／RETURNING 子查询方言分叉），彻底消掉那条 PG 语法错与消息孤儿。

// initChatTables 初始化聊天对话表（在 New() 后的迁移链中被调用）。
func (s *Store) initChatTables() error {
	d := db.CurrentDialect()
	if d == db.DialectPostgres {
		return s.initChatTablesPG()
	}
	return s.initChatTablesSQLite()
}

// initChatTablesSQLite SQLite 版本（使用 datetime('now')）。
func (s *Store) initChatTablesSQLite() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chat_conversations(
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			tenant_id INTEGER DEFAULT 0,
			title TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conv_user ON chat_conversations(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conv_tenant ON chat_conversations(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS chat_messages(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id TEXT NOT NULL,
			role TEXT NOT NULL CHECK(role IN ('user','assistant')),
			content TEXT NOT NULL,
			model TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(conversation_id, id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_msg_conv ON chat_messages(conversation_id, id)`,
	}
	// SQLite 专用 DDL（datetime('now')／AUTOINCREMENT）：显式按 SQLite 方言走包装，包装对 SQLite 不改写。
	for _, stmt := range stmts {
		if _, err := db.Exec(s.db, db.DialectSQLite, stmt); err != nil {
			return fmt.Errorf("initChatTables: %w", err)
		}
	}
	return nil
}

// initChatTablesPG PostgreSQL 版本（使用 now()::text）。
func (s *Store) initChatTablesPG() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chat_conversations(
			id TEXT PRIMARY KEY,
			user_id BIGINT NOT NULL,
			tenant_id BIGINT DEFAULT 0,
			title TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT now(),
			updated_at TIMESTAMP DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS conv_user_pg ON chat_conversations(user_id)`,
		`CREATE INDEX IF NOT EXISTS conv_tenant_pg ON chat_conversations(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS chat_messages(
			id SERIAL,
			conversation_id TEXT NOT NULL REFERENCES chat_conversations(id),
			role TEXT NOT NULL CHECK(role IN ('user','assistant')),
			content TEXT NOT NULL,
			model TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS msg_conv_pg ON chat_messages(conversation_id, id)`,
	}
	// PG 专用 DDL（SERIAL／now()／REFERENCES）：已是 PG 原生、无 `?`、无 SQLite 关键字，按 PG 方言走包装不改写出栈语义。
	for _, stmt := range stmts {
		if _, err := db.Exec(s.db, db.DialectPostgres, stmt); err != nil {
			return fmt.Errorf("initChatTables PG: %w", err)
		}
	}
	return nil
}

// ===== CRUD 操作 =====

// chatTsCol 把时间戳列在当前方言下投影成文本：PG 的 TIMESTAMP 列经 lib/pq 会解成 time.Time，
// 直接 Scan 进 Go string 会报「converting time.Time to string」；显式 `::text` 取回文本形态，
// 与 SQLite 侧「DATETIME 以 TEXT 存储、原样回字符串」的出栈口径对齐。
// ★ 〇-AP 收口第二条读腿：〇-AM 初版把整个 chat 读写一律写成裸 `?`，PG 下**读也死在占位符**、从没走到这一步，
//
//	故时间戳扫描这层坑一直被前一个错误掩盖；本次把读腿迁到方言包装后，这层必须一并显式处理，否则「占位符修好了、时间戳又翻红」。
func chatTsCol(col string) string {
	if db.CurrentDialect() == db.DialectPostgres {
		return col + "::text"
	}
	return col
}

// CreateChatConversation 插入新对话会话（★ 〇-AM 建立，★ 〇-AP 收口改正）。
// id 由调用方（SSE handler 生成的 UUID v4）传入，与随后 AppendChatMessage 的 conversation_id 同源，
// 保证会话行 PK 与消息外键必然对得上；两支合一：只有一条带 id 的 INSERT + 一条按 id 回查，
// `?` 占位符交方言包装（SQLite 原样、PG 自动改写为 $n），不再各写一套 rowid／RETURNING 子查询。
// 参数：id=会话全局唯一 ID（调用方生成）；userID=创建者；tenantID=所属租户；title=自动标题。
func (s *Store) CreateChatConversation(id string, userID, tenantID int64, title string) (*ChatConversation, error) {
	var conv ChatConversation
	d := db.CurrentDialect()
	if _, err := db.Exec(s.db, d,
		"INSERT INTO chat_conversations(id, user_id, tenant_id, title) VALUES(?, ?, ?, ?)",
		id, userID, tenantID, title); err != nil {
		return nil, err
	}
	// created_at/updated_at 由建表默认值填充（SQLite CURRENT_TIMESTAMP / PG now()），回查取回给调用方；
	// 时间戳列经 chatTsCol 在当前方言下投影为文本，PG 支补 ::text 以免 lib/pq 解成 time.Time 后 Scan 进 string 报错。
	err := db.QueryRow(s.db, d,
		"SELECT id, user_id, COALESCE(tenant_id,0), title, "+chatTsCol("created_at")+", "+chatTsCol("updated_at")+" FROM chat_conversations WHERE id=?", id).
		Scan(&conv.ID, &conv.UserID, &conv.TenantID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &conv, nil
}

// ListChatConversations 按用户获取最近 N 条对话列表（降序）。
func (s *Store) ListChatConversations(userID, tenantID int64, limit int) ([]ChatConversation, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id,user_id,COALESCE(tenant_id,0),title,"+chatTsCol("created_at")+","+chatTsCol("updated_at")+" FROM chat_conversations WHERE user_id=? AND tenant_id=? ORDER BY updated_at DESC LIMIT ?",
		userID, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChatConversation, 0)
	for rows.Next() {
		var c ChatConversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.TenantID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// AppendChatMessage 异步追加消息到数据库（★ 〇-AM：SSE handler 回调此方法，不阻塞流式返回）。
func (s *Store) AppendChatMessage(convID, role, content, model string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO chat_messages(conversation_id,role,content,model) VALUES(?,?,?,?)",
		convID, role, content, model)
	return err
}

// UpsertConversationTitle 更新会话标题（前端展示用）。
func (s *Store) UpsertConversationTitle(convID, title string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE chat_conversations SET title=?, updated_at=CURRENT_TIMESTAMP WHERE id=?", title, convID)
	return err
}

// GetChatMessages 取某会话最近的 N 条消息（正序）。
func (s *Store) GetChatMessages(convID string, limit int) ([]ChatMessage, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id,conversation_id,role,content,model,"+chatTsCol("created_at")+" FROM chat_messages WHERE conversation_id=? ORDER BY id DESC LIMIT ?",
		convID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChatMessage, 0)
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Model, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	// 结果已经是逆序，翻回来
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// DeleteChatConversation 删除会话及其全部消息（级联清理）。
// ★ 〇-AP 收口：原把「删消息; 删会话」两条带参语句拼进**一次** Exec——SQLite 驱动能跑，
//
//	PG 扩展协议却禁止「带参数 + 多语句」，生产 PG 恒报语法错。拆成两次独立 Exec，语义逐字不变（先删子消息再删会话）。
func (s *Store) DeleteChatConversation(convID string) error {
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM chat_messages WHERE conversation_id=?", convID); err != nil {
		return err
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM chat_conversations WHERE id=?", convID)
	return err
}

// MarkConvUpdated 更新会话 last_active 为当前时间。
// ★ 〇-AP 收口：两支仍按各自方言选时间函数（PG=now()／SQLite=CURRENT_TIMESTAMP），但都走 `db.Exec` 包装
//
//	（原裸 `?` 直送 lib/pq 在 PG 下必错）；显式钉方言避免包装把已存在的 `$n`/时间函数二次改写。
func (s *Store) MarkConvUpdated(convID string) error {
	if db.CurrentDialect() == db.DialectPostgres {
		_, _ = db.Exec(s.db, db.DialectPostgres, "UPDATE chat_conversations SET updated_at=now() WHERE id=?", convID)
	} else {
		_, _ = db.Exec(s.db, db.DialectSQLite, "UPDATE chat_conversations SET updated_at=CURRENT_TIMESTAMP WHERE id=?", convID)
	}
	return nil
}

// ExportChatJSON 导出单条会话为 JSON（GDPR 数据导出用）。
func (s *Store) ExportChatJSON(convID string) (map[string]any, error) {
	conv := ChatConversation{}
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id,user_id,COALESCE(tenant_id,0),title,"+chatTsCol("created_at")+","+chatTsCol("updated_at")+" FROM chat_conversations WHERE id=?", convID).
		Scan(&conv.ID, &conv.UserID, &conv.TenantID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt); err != nil {
		return nil, err
	}
	msgs, _ := s.GetChatMessages(convID, 99999)
	out := map[string]any{"conversation": conv, "messages": msgs}
	b, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(b, &out) // re-scan for any sql.Null types serialization
	return out, nil
}

// 确保 import 了 database/sql（供外部编译检查）。
var _ = sql.ErrNoRows
