// ============ feedback_f45_test.go · 职责说明 ============
// 用户反馈「译文上下文」列的 F-45 回归锁（★ 〇-U 批 I-2，2026-09-26）。
// 缺陷：反馈上下文为空时，写侧把 json.Marshal(nil map) 的结果——字符串 "null"——落进了
// feedbacks.translations；"null" 是合法 JSON，读侧与前端 try/catch 都拦不住它，
// 最终在「Object.entries(null)」处抛 TypeError，超管点进这条反馈详情整块白屏。
// 本文件钉三件事：①写入口一律归一为 "{}"；②老库脏行由启动迁移洗净（幂等）；
// ③归一后列表/详情读回的是可解析对象文本。
// =============================================
package store

import (
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// pinSqliteForF45 自钉方言（AGENTS §一·4）：config.Default() 读 DB_DRIVER 且有写全局 config.C 的副作用，
// PG 形态下会把方言泄漏给同包内存 SQLite 用例，造出「no such table」级假红。
func pinSqliteForF45(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// TestCreateFeedbackNormalizesNullTranslations 写侧归一：三种脏输入（"null"/空串/纯空白）
// 落库后都必须读回 "{}"，正常映射必须原样保留（不许被顺手抹成空对象）。
func TestCreateFeedbackNormalizesNullTranslations(t *testing.T) {
	pinSqliteForF45(t)
	s := newTestStore(t)
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"字面量 null", "null", "{}"},
		{"空串", "", "{}"},
		{"带空白的 null", "  null ", "{}"},
		{"合法空对象", "{}", "{}"},
		{"正常映射", `{"en":"Hello"}`, `{"en":"Hello"}`},
	}
	for i, c := range cases {
		f := &Feedback{TenantID: 3, UserID: 9, TargetType: "text",
			Content: "反馈内容", Translations: c.in, WithContext: true}
		if err := s.CreateFeedback(f); err != nil {
			t.Fatalf("%s: 写入失败: %v", c.name, err)
		}
		got, err := s.GetFeedback(f.ID)
		if err != nil {
			t.Fatalf("%s: 读回失败: %v", c.name, err)
		}
		if got.Translations != c.want {
			t.Errorf("用例%d %s: 库里应为 %q，实得 %q", i, c.name, c.want, got.Translations)
		}
		if f.Translations != c.want {
			t.Errorf("用例%d %s: 返回给调用方的结构体应与库里一致，实得 %q", i, c.name, f.Translations)
		}
	}
}

// TestFeedbackMigrateCleansLegacyNullRows 存量清洗：老库里已经存在的 "null" 行，
// 启动迁移必须洗净（代码只修未来写入的话，历史详情照样白屏），且重复执行幂等；
// 同时不得顺手改写 ”（未勾选上下文的正常行与线索都是它，改了没有收益）。
func TestFeedbackMigrateCleansLegacyNullRows(t *testing.T) {
	pinSqliteForF45(t)
	s := newTestStore(t)
	// 绕过写入口直插脏行，模拟修复前落库的历史数据
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO feedbacks (tenant_id, user_id, target_type, ticket_id, source_text, translations, target_langs, mode, content, with_context, status, created_at)
		 VALUES (3, 9, 'text', 0, '源文', 'null', 'en', 'fast', '脏行一', 1, 'open', '2026-09-26T00:00:00Z')`); err != nil {
		t.Fatalf("种脏行失败: %v", err)
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO feedbacks (tenant_id, user_id, target_type, ticket_id, content, translations, status, created_at)
		 VALUES (3, 9, 'lead', 0, '正常空上下文行', '', 'open', '2026-09-26T00:00:00Z')`); err != nil {
		t.Fatalf("种正常行失败: %v", err)
	}
	s.feedbackMigrate()
	if n := countDirtyTranslations(t, s); n != 0 {
		t.Errorf("迁移后脏行应清零，实得 %d", n)
	}
	if got := mustTranslation(t, s, "脏行一"); got != "{}" {
		t.Errorf("脏行一应被洗成 {}，实得 %q", got)
	}
	// 幂等 + 不误伤：再跑一次不报错，'' 形态的正常行保持原样
	s.feedbackMigrate()
	if got := mustTranslation(t, s, "正常空上下文行"); got != "" {
		t.Errorf("未勾选上下文的正常行不该被迁移改写，实得 %q", got)
	}
	if n := countDirtyTranslations(t, s); n != 0 {
		t.Errorf("重复迁移应幂等，脏行数实得 %d", n)
	}
}

// countDirtyTranslations 统计仍会被前端判崩的脏形态行数（与本包迁移的判据同口径）。
func countDirtyTranslations(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM feedbacks WHERE translations='null' OR translations IS NULL`).Scan(&n); err != nil {
		t.Fatalf("统计脏行失败: %v", err)
	}
	return n
}

// mustTranslation 按 content 取一条反馈的译文上下文列。
func mustTranslation(t *testing.T, s *Store, content string) string {
	t.Helper()
	var v string
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT COALESCE(translations,'<NULL>') FROM feedbacks WHERE content=?`, content).Scan(&v); err != nil {
		t.Fatalf("读取 %s 失败: %v", content, err)
	}
	return v
}
