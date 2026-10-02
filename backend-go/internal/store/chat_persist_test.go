// ============================================================================
// chat_persist_test.go — ★ 〇-AP 收口①回归（2026-10-02）
//
// 钉的是 chat.go 迁移到方言包装后「真正写进去」这件事，而不是闸门绿灯：
// 〇-AM 初版把 CreateChatConversation 写成「不给 id 赋值 + 从返回值里拿 id」，
// 而 SSE handler 用的是**自己另生成的 convID**去写 chat_messages.conversation_id，
// 创建会话那一腿的返回值整个丢弃 ⇒ 会话行的 PK 与消息的外键在结构上永远对不上
// （SQLite 下 PK 落成 NULL，PG 下那条 `FROM (INSERT...)` 更是语法错、整条没跑）。
// PG run_uat 里 `server.log` 反复冒 `pq: syntax error at or near "INTO"` 就是它，
// 只有把那几处 log.Printf 迁到结构化日志（收口②）才第一次被看见。
//
// 本用例锁三条，缺一即回潮：
//
//	① 调用方传入的 id == 落库的 chat_conversations.id（按 UUID 直查命中 1 行）；
//	② 用同一个 id 追加的两条消息，GetChatMessages(id) 读得回且正序；
//	③ ListChatConversations 能列出该会话（updated_at 经时间戳投影为文本、非空）。
//
// 方言自钉（AGENTS §一·4）：本用例自钉 sqlite，绝不把 DB_DRIVER 泄漏给同包后续用例；
// 连接用命名共享缓存内存库（与 apikeys_quota_test.go 同口径），避免 :memory: 私有库假象。
// ============================================================================
package store

import (
	"database/sql"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// chatSeq 给每个用例的共享内存库唯一命名，防同包并发用例串库。
var chatSeq int64

// pinSQLiteForChatTest ★ AGENTS §一·4：本用例自钉方言并在结束时恢复。
func pinSQLiteForChatTest(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newChatStore 开一份跑完全部迁移（含 initChatTables）的共享内存 Store。
func newChatStore(t *testing.T) *Store {
	t.Helper()
	name := "chat_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "_" + strconv.FormatInt(atomic.AddInt64(&chatSeq, 1), 10)
	dsn := "file:" + name + "?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate"
	db0, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开共享内存库失败: %v", err)
	}
	db0.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db0.Close() })
	s, err := New(db0)
	if err != nil {
		t.Fatalf("创建共享内存 Store 失败: %v", err)
	}
	return s
}

// TestChatConversationIDIsCallerSupplied 验证收口①的正身：会话 PK 必须等于调用方传入的 id。
func TestChatConversationIDIsCallerSupplied(t *testing.T) {
	pinSQLiteForChatTest(t)
	s := newChatStore(t)

	const convID = "11111111-2222-3333-4444-555555555555" // 模拟 handler 生成的 UUID v4
	conv, err := s.CreateChatConversation(convID, 42, 7, "翻译请求标题")
	if err != nil {
		t.Fatalf("CreateChatConversation 失败: %v", err)
	}
	if conv.ID != convID {
		t.Fatalf("①返回 id 应等于传入 convID：got=%q want=%q", conv.ID, convID)
	}
	// 直接按 UUID 查库——旧形态（id 从不赋值）下这一查必为 0 行，是本用例的反证面。
	var gotID string
	if err := s.db.QueryRow(`SELECT id FROM chat_conversations WHERE id=?`, convID).Scan(&gotID); err != nil {
		t.Fatalf("①按传入 id 直查会话行失败（PK 未与 convID 对齐＝〇-AM 旧缺陷回潮）: %v", err)
	}
	if gotID != convID {
		t.Fatalf("①落库 PK 应等于传入 id：got=%q want=%q", gotID, convID)
	}
}

// TestChatMessagesLinkToConversationID 验证用同一 id 追加的消息能被读回且正序、时间戳非空。
func TestChatMessagesLinkToConversationID(t *testing.T) {
	pinSQLiteForChatTest(t)
	s := newChatStore(t)

	const convID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if _, err := s.CreateChatConversation(convID, 42, 7, "会话"); err != nil {
		t.Fatalf("CreateChatConversation 失败: %v", err)
	}
	if err := s.AppendChatMessage(convID, "user", "你好", ""); err != nil {
		t.Fatalf("追加 user 消息失败: %v", err)
	}
	if err := s.AppendChatMessage(convID, "assistant", "在的", "glm-4"); err != nil {
		t.Fatalf("追加 assistant 消息失败: %v", err)
	}

	msgs, err := s.GetChatMessages(convID, 100)
	if err != nil {
		t.Fatalf("GetChatMessages 失败: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("②应读回 2 条消息（旧形态下消息外键与会话 PK 对不上会读成孤儿/空）：got=%d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("②消息须按写入正序返回：got roles=[%s,%s]", msgs[0].Role, msgs[1].Role)
	}
	if msgs[1].Model != "glm-4" {
		t.Fatalf("②model 应回读：got=%q", msgs[1].Model)
	}
	// 时间戳投影为文本（chatTsCol 的 SQLite 支＝原样），断言非空即证明列取回成功。
	if strings.TrimSpace(msgs[0].CreatedAt) == "" {
		t.Fatalf("②created_at 投影不应为空")
	}
}

// TestChatListShowsConversationWithTextTimestamp 验证列表读腿在方言包装 + 时间戳文本投影下正常出栈。
func TestChatListShowsConversationWithTextTimestamp(t *testing.T) {
	pinSQLiteForChatTest(t)
	if db.CurrentDialect() != db.DialectSQLite {
		t.Fatalf("方言自钉失效：期望 sqlite，实际 %v", db.CurrentDialect())
	}
	s := newChatStore(t)

	const convID = "cafe0000-0000-0000-0000-000000000001"
	if _, err := s.CreateChatConversation(convID, 99, 3, "列表可见会话"); err != nil {
		t.Fatalf("CreateChatConversation 失败: %v", err)
	}
	convs, err := s.ListChatConversations(99, 3, 20)
	if err != nil {
		t.Fatalf("ListChatConversations 失败: %v", err)
	}
	if len(convs) != 1 || convs[0].ID != convID {
		t.Fatalf("③列表应恰含该会话：got=%+v", convs)
	}
	if strings.TrimSpace(convs[0].UpdatedAt) == "" {
		t.Fatalf("③updated_at 文本投影不应为空（PG 下由 ::text 保证，SQLite 下由 TEXT 存储保证）")
	}
}
