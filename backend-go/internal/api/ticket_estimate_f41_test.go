// ============ ticket_estimate_f41_test.go 职责中文说明 ============
// F-41（2026-09-25 批 D）建单余额预检估算重写（chars × langs × K(mode)）的字节级基准锁：
//   - 88 案：md 11,244B / 3 语 pro，实烧 1,075,400 token 后在 100% 进度处烧穿 rejected——
//     新估算必须 ≥ 实烧（旧口径估 17.3k，低估 ~62 倍正是缺陷本体）；
//   - 89 案：md 97,332B / 3 语 pro，按 88 单位成本外推实需 ≈930 万 token——同样必须覆盖；
//   - estTokensPerChar：system_config 键（est_tokens_per_char_pro/fast）可调 + 非法值回退默认。
//
// ========================================
package api

import (
	"database/sql"
	"testing"

	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// f41StoreServer 构造带内存 SQLite 的最小 Server（K 配置读取链测试用，计费开关不在射程）。
func f41StoreServer(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return &Server{Store: st}
}

// TestUATBatchD_EstimateByteBaseline8889 生产两案字节基准锁（修复文档断言①）。
// 88/89 的字符数取自 estimateFileSourceChars（md 按 /3 折字符，与预检调用点同一口径），
// 钉的是「新估算 ≥ 实烧/外推需求」这条方向性红线——K 再被调错也必须先把这两单拦住。
func TestUATBatchD_EstimateByteBaseline8889(t *testing.T) {
	// 88 案：11,244B md → 3,748 字符 × 3 语 × K160 = 1,799,040 ≥ 实烧 1,075,400
	chars88 := estimateFileSourceChars("doc.md", 11244)
	if chars88 != 3748 {
		t.Fatalf("88 案字符折算漂移（预检口径变了需同步本锁）: %d", chars88)
	}
	est88 := estimateTicketTokens(chars88, 3, "pro", 160, 60)
	if est88 != 1799040 {
		t.Fatalf("88 案等值锁: est=%d, want 1799040", est88)
	}
	if est88 < 1075400 {
		t.Fatalf("88 案估算低于实烧 1,075,400（F-41 复发）: %d", est88)
	}
	// 89 案：97,332B md → 32,444 字符 × 3 语 × K160 = 15,573,120 ≥ 外推实需 ≈9,300,000
	chars89 := estimateFileSourceChars("big.md", 97332)
	est89 := estimateTicketTokens(chars89, 3, "pro", 160, 60)
	if est89 < 9300000 {
		t.Fatalf("89 案估算低于外推实需 930 万 token: %d", est89)
	}
	// 反向对照（旧公式为何拦不住）：旧口径 est=chars/1.3×langs×markup2 仅 ≈17.3k，
	// 远低于实烧——若有人把公式改回「按单次译文折算」，上面两条等值/下限锁会同时红灯。
}

// TestUATBatchD_EstTokensPerCharConfig K 值 system_config 可调 + 非法回退默认（160/60）。
// 键缺失/空/非数字/≤0 一律回退：估算被配置清零＝全放行，正是 F-41 的事故形态，必须红灯。
func TestUATBatchD_EstTokensPerCharConfig(t *testing.T) {
	s := f41StoreServer(t)
	if got := s.estTokensPerChar("pro"); got != 160 {
		t.Fatalf("缺省 K(pro) 应为 160, got %v", got)
	}
	if got := s.estTokensPerChar("fast"); got != 60 {
		t.Fatalf("缺省 K(fast) 应为 60, got %v", got)
	}
	if err := s.Store.SetConfig("est_tokens_per_char_pro", "200"); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}
	if err := s.Store.SetConfig("est_tokens_per_char_fast", "80"); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}
	if got := s.estTokensPerChar("pro"); got != 200 {
		t.Fatalf("K(pro) 未跟随配置: %v", got)
	}
	if got := s.estTokensPerChar("fast"); got != 80 {
		t.Fatalf("K(fast) 未跟随配置: %v", got)
	}
	// 非法值回退默认（管理台误填不能把闸门拆成放行）
	for _, bad := range []string{"abc", "0", "-5", "  "} {
		if err := s.Store.SetConfig("est_tokens_per_char_pro", bad); err != nil {
			t.Fatalf("SetConfig(%q) 失败: %v", bad, err)
		}
		if got := s.estTokensPerChar("pro"); got != 160 {
			t.Fatalf("非法配置 %q 应回退 160, got %v", bad, got)
		}
	}
}
