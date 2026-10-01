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
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
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
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("initChatTables PG: %w", err)
		}
	}
	return nil
}

// ===== CRUD 操作 =====

// CreateChatConversation 插入新对话会话（★ 〇-AM）。
func (s *Store) CreateChatConversation(userID, tenantID int64, title string) (*ChatConversation, error) {
	var conv ChatConversation
	// 方言差异：PG 用 $1 占位符，SQLite 用 ?
	if db.CurrentDialect() == db.DialectPostgres {
		err := s.db.QueryRow(`SELECT id, user_id, COALESCE(tenant_id,0), title, created_at, updated_at
			FROM (INSERT INTO chat_conversations(user_id,tenant_id,title) VALUES($1,$2,$3) RETURNING *) t`,
			userID, tenantID, title).Scan(&conv.ID, &conv.UserID, &conv.TenantID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)
		if err != nil {
			return nil, err
		}
	} else {
		// SQLite：先 INSERT，再 SELECT
		result, err := s.db.Exec("INSERT INTO chat_conversations(user_id, tenant_id, title) VALUES (?, ?, ?)", userID, tenantID, title)
		if err != nil {
			return nil, err
		}
		lastID, err := result.LastInsertId()
		if err != nil {
			return nil, err
		}
		// SQLite TEXT id 需要重新查询（因为插入时没传 id）
		err = s.db.QueryRow("SELECT id, user_id, COALESCE(tenant_id,0), title, created_at, updated_at FROM chat_conversations WHERE rowid=?", lastID).
			Scan(&conv.ID, &conv.UserID, &conv.TenantID, &conv.Title, &conv.CreatedAt, &conv.UpdatedAt)
		if err != nil {
			return nil, err
		}
	}
	return &conv, nil
}

// ListChatConversations 按用户获取最近 N 条对话列表（降序）。
func (s *Store) ListChatConversations(userID, tenantID int64, limit int) ([]ChatConversation, error) {
	rows, err := s.db.Query(
		"SELECT id,user_id,COALESCE(tenant_id,0),title,created_at,updated_at FROM chat_conversations WHERE user_id=? AND tenant_id=? ORDER BY updated_at DESC LIMIT ?",
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
	_, err := s.db.Exec(
		"INSERT INTO chat_messages(conversation_id,role,content,model) VALUES(?,?,?,?)",
		convID, role, content, model)
	return err
}

// UpsertConversationTitle 更新会话标题（前端展示用）。
func (s *Store) UpsertConversationTitle(convID, title string) error {
	_, err := s.db.Exec("UPDATE chat_conversations SET title=?, updated_at=CURRENT_TIMESTAMP WHERE id=?", title, convID)
	return err
}

// GetChatMessages 取某会话最近的 N 条消息（正序）。
func (s *Store) GetChatMessages(convID string, limit int) ([]ChatMessage, error) {
	rows, err := s.db.Query(
		"SELECT id,conversation_id,role,content,model,created_at FROM chat_messages WHERE conversation_id=? ORDER BY id DESC LIMIT ?",
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
func (s *Store) DeleteChatConversation(convID string) error {
	_, err := s.db.Exec("DELETE FROM chat_messages WHERE conversation_id=?; DELETE FROM chat_conversations WHERE id=?", convID, convID)
	return err
}

// MarkConvUpdated 更新会话 last_active 为当前时间。
func (s *Store) MarkConvUpdated(convID string) error {
	if db.CurrentDialect() == db.DialectPostgres {
		_, _ = s.db.Exec("UPDATE chat_conversations SET updated_at=now() WHERE id=?", convID)
	} else {
		_, _ = s.db.Exec("UPDATE chat_conversations SET updated_at=CURRENT_TIMESTAMP WHERE id=?", convID)
	}
	return nil
}

// ExportChatJSON 导出单条会话为 JSON（GDPR 数据导出用）。
func (s *Store) ExportChatJSON(convID string) (map[string]any, error) {
	conv := ChatConversation{}
	if err := s.db.QueryRow("SELECT id,user_id,COALESCE(tenant_id,0),title,created_at,updated_at FROM chat_conversations WHERE id=?", convID).
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
