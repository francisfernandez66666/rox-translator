// ============ scim_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-7）：SCIM 令牌「摘要 + 密文」存储口径的回归断言。
// 钉住四条最容易被改坏的行为：
//
//	① 新写入行库内不再是明文（token 列必须带 enc:v1: 前缀，token_hash 必须是明文 SHA-256）；
//	② 鉴权走摘要命中（GetSCIMConfigByToken 明文 → 配置），错误令牌与空令牌一律不命中；
//	③ 历史明文行经 SCIMMigrate 回填后 **仍可鉴权**（读侧双轨，IdP 不掉线）且不再是明文；
//	④ 管理台回显路径（GetSCIMConfigByTenant）拿到的仍是明文，摘要列不外泄。
//
// 方言按 AGENTS.md 一.4 自钉 sqlite，避免 config.C 泄漏 PG 方言给同包内存库用例。
// =============================================
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"

	"translator/internal/config"

	_ "modernc.org/sqlite"
)

// newSCIMTestStore 内存 SQLite + 全量迁移的测试 Store（自钉 sqlite 方言并恢复全局 config）。
func newSCIMTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	// JWT_SECRET 固定：让 enc:v1: 密文在本测试进程内可稳定解密（不依赖随机进程密钥）
	t.Setenv("JWT_SECRET", "scim-test-secret")
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	s, err := New(conn)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return s
}

// rawSCIMRow 直读 scim_config 原始列（绕开 Store 的解密逻辑，验的是**库里真正存了什么**）。
func rawSCIMRow(t *testing.T, s *Store, tid int64) (token, tokenHash string) {
	t.Helper()
	err := s.db.QueryRow("SELECT COALESCE(token,''), COALESCE(token_hash,'') FROM scim_config WHERE tenant_id=?", tid).Scan(&token, &tokenHash)
	if err != nil {
		t.Fatalf("读取 scim_config 原始行失败: %v", err)
	}
	return token, tokenHash
}

// TestSCIMTokenStoredAsDigestAndCiphertext 新写入行：库内是密文 + 摘要，明文只活在内存与回显里。
func TestSCIMTokenStoredAsDigestAndCiphertext(t *testing.T) {
	s := newSCIMTestStore(t)
	cfg := &SCIMConfig{TenantID: 7, Enabled: true, RootOrgID: 3}
	if err := s.UpsertSCIMConfig(cfg); err != nil {
		t.Fatalf("保存 SCIM 配置失败: %v", err)
	}
	if cfg.Token == "" {
		t.Fatal("token 为空时应自动生成")
	}
	stored, hash := rawSCIMRow(t, s, 7)
	if stored == cfg.Token {
		t.Fatal("★ 回归：token 仍以明文入库（应为 enc:v1: 密文）")
	}
	if len(stored) <= len(SecretEncPrefix) || stored[:len(SecretEncPrefix)] != SecretEncPrefix {
		t.Fatalf("token 列应带 %s 前缀，实得 %q", SecretEncPrefix, stored)
	}
	sum := sha256.Sum256([]byte(cfg.Token))
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("token_hash 应为明文 SHA-256，实得 %q", hash)
	}
}

// TestSCIMAuthByDigestTrack 鉴权走摘要轨道：明文令牌命中、错误令牌与空令牌一律不命中。
func TestSCIMAuthByDigestTrack(t *testing.T) {
	s := newSCIMTestStore(t)
	cfg := &SCIMConfig{TenantID: 11, Enabled: true}
	if err := s.UpsertSCIMConfig(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	got, err := s.GetSCIMConfigByToken(cfg.Token)
	if err != nil || got == nil {
		t.Fatalf("明文令牌应鉴权成功，got nil err=%v", err)
	}
	if got.TenantID != 11 || !got.Enabled {
		t.Fatalf("命中的配置不符: %+v", got)
	}
	if got.Token != cfg.Token {
		t.Fatalf("返回的 token 应为解密后的明文，实得 %q", got.Token)
	}
	// 错误令牌：不得命中（摘要不同）
	if c, _ := s.GetSCIMConfigByToken(cfg.Token + "deadbeef"); c != nil {
		t.Fatalf("错误令牌不应命中，实得 %+v", c)
	}
	// 空令牌：直接短路，不落查询也不命中
	if c, _ := s.GetSCIMConfigByToken(""); c != nil {
		t.Fatalf("空令牌不应命中，实得 %+v", c)
	}
	// 禁用态：命中配置但 Enabled=false（调用方据此返回 403）
	cfg.Enabled = false
	if err := s.UpsertSCIMConfig(cfg); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if c, _ := s.GetSCIMConfigByToken(cfg.Token); c == nil || c.Enabled {
		t.Fatalf("禁用后应命中且 Enabled=false，实得 %+v", c)
	}
}

// TestSCIMLegacyPlaintextRowMigratesInPlace 历史明文行：SCIMMigrate 回填后仍可鉴权，且库里不再是明文。
func TestSCIMLegacyPlaintextRowMigratesInPlace(t *testing.T) {
	s := newSCIMTestStore(t)
	const legacyToken = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b0c1d2e3f4" // 48-hex，旧口径直接明文写入
	// 造旧态：显式把 token_hash 置空、token 写明文（模拟升级前的存量行）
	if _, err := s.db.Exec(`INSERT INTO scim_config (tenant_id, token, token_hash, enabled, root_org_id, created_at)
		VALUES (21, ?, '', 1, 0, '2026-01-01T00:00:00Z')`, legacyToken); err != nil {
		t.Fatalf("写入历史明文行失败: %v", err)
	}
	if _, err := s.GetSCIMConfigByToken(legacyToken); err != nil {
		t.Fatalf("旧行读取异常: %v", err)
	}
	// 迁移前：明文在库、摘要为空（确认这确实是「未回填」态，否则本测试没有意义）
	before, beforeHash := rawSCIMRow(t, s, 21)
	if before != legacyToken || beforeHash != "" {
		t.Fatalf("前置态不符（应为明文 + 空摘要），实得 %q/%q", before, beforeHash)
	}
	// 幂等重跑迁移（生产每次启动都会跑一遍）
	s.SCIMMigrate()
	after, afterHash := rawSCIMRow(t, s, 21)
	if after == legacyToken {
		t.Fatal("★ 回归：迁移后 token 仍是明文")
	}
	if len(after) <= len(SecretEncPrefix) || after[:len(SecretEncPrefix)] != SecretEncPrefix {
		t.Fatalf("迁移后应为 enc:v1: 密文，实得 %q", after)
	}
	sum := sha256.Sum256([]byte(legacyToken))
	if afterHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("迁移后摘要不符，实得 %q", afterHash)
	}
	// ★ 关键业务断言：IdP 仍拿旧明文令牌来请求 ⇒ 必须鉴权成功（不能因收敛口径把客户打挂）
	got, err := s.GetSCIMConfigByToken(legacyToken)
	if err != nil || got == nil {
		t.Fatalf("老明文 token 迁移后应仍可鉴权，got nil err=%v", err)
	}
	if got.TenantID != 21 || !got.Enabled {
		t.Fatalf("命中配置不符: %+v", got)
	}
	// 再跑一次必须幂等（不重复加密、不丢摘要）
	s.SCIMMigrate()
	again, againHash := rawSCIMRow(t, s, 21)
	if again != after || againHash != afterHash {
		t.Fatalf("迁移不幂等：二次执行后列被改写 %q/%q", again, againHash)
	}
}

// TestSCIMGetByTenantRevealsPlaintext 回显路径：管理台读到的仍是明文，摘要列不参与 JSON 外泄。
func TestSCIMGetByTenantRevealsPlaintext(t *testing.T) {
	s := newSCIMTestStore(t)
	cfg := &SCIMConfig{TenantID: 31, Enabled: true, RootOrgID: 5}
	if err := s.UpsertSCIMConfig(cfg); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	got, err := s.GetSCIMConfigByTenant(31)
	if err != nil || got == nil {
		t.Fatalf("按租户读取失败: %v", err)
	}
	if got.Token != cfg.Token {
		t.Fatalf("回显应为解密后的明文，实得 %q", got.Token)
	}
	if got.RootOrgID != 5 || !got.Enabled {
		t.Fatalf("字段回显不符: %+v", got)
	}
	// 摘要字段本身不参与对外序列化（json 里出现摘要等于给离线爆破多一份材料）
	if got.TokenHash == "" {
		t.Fatal("内存对象应带上算好的摘要，供调用方复用")
	}
	// 无配置的租户：返回 (nil,nil)，调用方按「未开通」处理
	if c, err := s.GetSCIMConfigByTenant(999); c != nil || err != nil {
		t.Fatalf("无配置应为 (nil,nil)，实得 %+v/%v", c, err)
	}
}

// TestSCIMTokenHashHelper 摘要函数的边界：空串不得伪装成 hash("")，48-hex 输入稳定可比对。
func TestSCIMTokenHashHelper(t *testing.T) {
	if HashSCIMToken("") != "" {
		t.Fatal("空 token 的摘要必须是空串（否则空凭证会误命中空摘要行）")
	}
	h := HashSCIMToken("x")
	if len(h) != 64 {
		t.Fatalf("SHA-256 十六进制长度应为 64，实得 %d", len(h))
	}
	if h != HashSCIMToken("x") {
		t.Fatal("摘要必须可重入（同一明文同一摘要，否则鉴权不稳定）")
	}
	if h == HashSCIMToken("y") {
		t.Fatal("不同明文不得同摘要")
	}
}
