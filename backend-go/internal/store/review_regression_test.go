// ============ 本文件职责说明 ============
// 全仓端到端评审（2026-08-26）修复项回归测试：
//   B3 句数镜像原子性（Add/DeductSentences 守卫与并发守恒）
//   B4 legacy Deduct 守卫式扣减（并发不扣负）
//   A2 SaveEntry 语言码白名单（标识符注入拦截）
//   C1 ListUsersByRole 列数对齐（通知链路复活回归）
package store

import (
	"sync"
	"testing"
)

// ensureTenantRow 在并发测试库（临时文件库）中补建 tenants 表与租户 1
// （newConcurrentTestStore 只跑 store.New 迁移，tenants 表属 tenant 域）。
func ensureTenantRow(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB().Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		code TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'active',
		expires_at TEXT NOT NULL DEFAULT '',
		permissions TEXT NOT NULL DEFAULT '{}',
		created_at TEXT, updated_at TEXT)`); err != nil {
		t.Fatalf("建 tenants 表失败: %v", err)
	}
	if _, err := s.DB().Exec(`INSERT OR IGNORE INTO tenants (id, code, name) VALUES (1,'test','测试租户')`); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
}

// TestDeductGuardConcurrent 并发 Deduct 守卫：余额 100，40 个 goroutine 各扣 3——
// 恰好 33 个成功、7 个返回 ErrInsufficientBalance；最终余额 100-99=1 且绝不为负。
func TestDeductGuardConcurrent(t *testing.T) {
	s := newConcurrentTestStore(t)
	if err := s.Charge(1, 100); err != nil {
		t.Fatalf("Charge: %v", err)
	}
	var mu sync.Mutex
	var okCnt, failCnt int
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Deduct(1, 3)
			mu.Lock()
			if err == nil {
				okCnt++
			} else {
				failCnt++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if okCnt != 33 || failCnt != 7 {
		t.Fatalf("期望 33 成功 / 7 失败，实际 %d / %d", okCnt, failCnt)
	}
	bal, err := s.GetBalance(1)
	if err != nil || bal.Balance != 1 {
		t.Fatalf("最终余额应为 1，实际 %v (err=%v)", bal, err)
	}
}

// TestSentenceMirrorAtomic 句数镜像并发：余额 100，50 个 goroutine 各扣 4——
// 恰好 25 个成功、25 个 ErrSentenceExhausted；终值 0。
func TestSentenceMirrorAtomic(t *testing.T) {
	s := newConcurrentTestStore(t)
	ensureTenantRow(t, s)
	if _, err := s.AddSentences(1, 100); err != nil {
		t.Fatalf("AddSentences: %v", err)
	}
	var mu sync.Mutex
	var okCnt, failCnt int
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.DeductSentences(1, 4)
			mu.Lock()
			if err == nil {
				okCnt++
			} else if err == ErrSentenceExhausted {
				failCnt++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if okCnt != 25 || failCnt != 25 {
		t.Fatalf("期望 25 成功 / 25 耗尽，实际 %d / %d", okCnt, failCnt)
	}
	if cur, _ := s.GetSentenceBalance(1); cur != 0 {
		t.Fatalf("句数余额应为 0，实际 %d", cur)
	}
}

// TestSaveEntryRejectsInvalidLang A2 白名单：非法 target_lang 必须报错，
// 不允许进入 tm_segments 列名拼接位。
func TestSaveEntryRejectsInvalidLang(t *testing.T) {
	s := newTestStoreWithTenants(t)
	for _, bad := range []string{"en'); DROP TABLE tm_segments--", "zh_hant WHERE 1=1", "../../etc", ""} {
		if _, err := s.SaveEntry(1, 1, LayerTM, "zh", "原文", bad, "x", "t"); err == nil {
			t.Fatalf("非法语言码 %q 应被拒绝", bad)
		}
	}
	// 合法语言码正常写入（包不存在仅跳过检索层写通，条目本身落库）
	id, err := s.SaveEntry(1, 9999, LayerTM, "zh", "原文", "en", "ok", "t")
	if err != nil || id <= 0 {
		t.Fatalf("合法语言码应写入成功: id=%d err=%v", id, err)
	}
}

// TestListUsersByRoleReturnsRows C1 回归：建号后按角色必须能查出用户
// （旧实现 SELECT/Scan 列数错位恒返空）。
func TestListUsersByRoleReturnsRows(t *testing.T) {
	s := newTestStoreWithTenants(t)
	u, err := s.CreateUser(1, "notifier", "$2a$10$abcdefghijklmnopqrstuv", "通知员", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	list := s.ListUsersByRole(1, RoleUser)
	found := false
	for _, x := range list {
		if x.ID == u.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListUsersByRole 应包含新建用户 %d（实际 %d 行）", u.ID, len(list))
	}
}
