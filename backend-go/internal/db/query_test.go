// 职责：占位符改写（SQLite ? → PostgreSQL $n）单元测试，覆盖基础替换、
// 字符串字面量内的 ? 应被忽略、以及 '' 转义连续单引号的处理。
package db

import "testing"

func TestRewritePlaceholdersBasic(t *testing.T) {
	in := "SELECT * FROM users WHERE id = ? AND name = ?"
	want := "SELECT * FROM users WHERE id = $1 AND name = $2"
	if got := RewritePlaceholders(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewritePlaceholdersInsideLiteral(t *testing.T) {
	in := "SELECT * FROM t WHERE note = 'a?b' AND id = ?"
	want := "SELECT * FROM t WHERE note = 'a?b' AND id = $1"
	if got := RewritePlaceholders(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewritePlaceholdersEscapedQuote(t *testing.T) {
	in := "SELECT 'it''s ? ok' AS a, id = ?"
	want := "SELECT 'it''s ? ok' AS a, id = $1"
	if got := RewritePlaceholders(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
