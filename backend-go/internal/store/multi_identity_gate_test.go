// ============ 本文件职责中文说明 ============
// multi_identity_gate_test.go · 多身份账号体系 **B1（数据层）** 的出厂判据
// （方案《多身份账号方案_20261023.md》§十一「store 层」那一整块，逐条对号）：
//
//	M1 迁移回填：account_id=CAST(id AS TEXT)、is_home=1，且第二次跑零改动（幂等）；
//	M2 家行不变式收敛：多枚家行/没有家行两种坏形态都必须收敛成「有且仅有一枚」；
//	M3 同邮箱**不同租户**可各持一行、**同租户**撞拒（D3 换轨的两个方向都要有判据）；
//	M4 GetUserByEmail 家优先（§四）——反证用「换家」而不是「再造一行」，
//	   这样红的一定是排序腿，不是数据腿；
//	M5 SetUserEmail 同步全账户（改一处=改这个人的所有身份行），且撞别人邮箱整体拒绝；
//	M6 SetPasswordForAccount 只写家行（D4），成员行口令一字不动（D7 撤销也只在家行）；
//	M7 CreateIdentityRow 建出来的是**非家、无口令**的行；空账户号必须报错（不许静默建新账户）；
//	M8 PromoteHomeRow 只在本账户的行之间挪标记（跨账户必须拒），并且旧家被摘干净。
//
// B1 的总判据是「**一人一行时行为逐字节等价**」：M1/M3/M4 都刻意先在"一人一行"形态上断言，
// 回归矩阵（run_uat）绿才算这条成立，本文件负责的是"新形态建得出来且自洽"那一半。
//
// 方言按 AGENTS §一·4 自钉（config.Default() 有写全局 config.C 的副作用，用完还原）。
// ========================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"translator/internal/config"
)

// pinStoreDialect 把全局方言钉成 sqlite 并在用例结束后还原。
func pinStoreDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// legacyUsersDDL 是**加这两列之前**的 users 表形状（M1 的存量夹具）。
// 逐列照生产旧版抄，唯独不写 account_id/is_home——这两个值必须由迁移自己长出来，
// 夹具"顺手给上"就等于把 M1 判空。
const legacyUsersDDL = `CREATE TABLE users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	tenant_id INTEGER NOT NULL DEFAULT 1,
	username TEXT NOT NULL,
	password_hash TEXT NOT NULL,
	display_name TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL DEFAULT 'user',
	status TEXT NOT NULL DEFAULT 'active',
	created_by INTEGER NOT NULL DEFAULT 0,
	last_login_at TEXT NOT NULL DEFAULT '',
	org_id INTEGER NOT NULL DEFAULT 0,
	email TEXT NOT NULL DEFAULT '',
	agreed_at TEXT NOT NULL DEFAULT '',
	must_change_pwd INTEGER NOT NULL DEFAULT 0,
	token_version INTEGER NOT NULL DEFAULT 0,
	job_role TEXT NOT NULL DEFAULT '',
	preferred_lang TEXT NOT NULL DEFAULT '',
	ref_code TEXT DEFAULT '',
	referred_by INTEGER DEFAULT 0,
	scim_external_id TEXT NOT NULL DEFAULT '',
	deactivate_at TEXT NOT NULL DEFAULT '',
	profile_setup_required INTEGER NOT NULL DEFAULT 0,
	created_at TEXT,
	updated_at TEXT
)`

// rawQueryInt 现读一个整数读数（带错误出，禁止把"读失败"读成 0）。
func rawQueryInt(t *testing.T, conn *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("读数失败（%s）: %v", q, err)
	}
	return n
}

// TestMultiIdentityMigrateBackfillsLegacyRows M1：存量库（没有这两列）经完整启动链后必须回填自洽。
func TestMultiIdentityMigrateBackfillsLegacyRows(t *testing.T) {
	pinStoreDialect(t)
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(legacyUsersDDL); err != nil {
		t.Fatalf("建存量夹具失败: %v", err)
	}
	// 三行分属两个租户，邮箱互不重复（避免撞按租户唯一索引，那会把"回填"判成"迁移失败"）。
	fx := []string{
		`INSERT INTO users (id,tenant_id,username,password_hash,display_name,email) VALUES (11,2,'a','h','A','a@example.com')`,
		`INSERT INTO users (id,tenant_id,username,password_hash,display_name,email) VALUES (12,2,'b','h','B','b@example.com')`,
		`INSERT INTO users (id,tenant_id,username,password_hash,display_name,email) VALUES (13,3,'c','h','C','c@example.com')`,
	}
	for _, q := range fx {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("插入存量行失败: %v\nSQL: %s", err, q)
		}
	}
	// 走**真实启动链**（store.New 里 MultiIdentityMigrate 排在 OneidMigrate 之后），
	// 而不是只调那一个函数：判据要成立的是"这台机器启动完就是这样"。
	s, err := New(raw)
	if err != nil {
		t.Fatalf("存量库启动迁移失败: %v", err)
	}
	for _, id := range []int64{11, 12, 13} {
		var acct string
		var home int
		if err := raw.QueryRow(`SELECT account_id, is_home FROM users WHERE id=?`, id).Scan(&acct, &home); err != nil {
			t.Fatalf("读回填结果失败 id=%d: %v", id, err)
		}
		if want := fmt.Sprint(id); acct != want {
			t.Fatalf("account_id 回填不等值 id=%d：want %q got %q ⇒ 人键没落到自身 id（D1）", id, want, acct)
		}
		if home != 1 {
			t.Fatalf("is_home 回填失败 id=%d：现状一人一行必须是自己的家（D2），实得 %d", id, home)
		}
	}
	// 幂等：第二次跑零改动（同一账户的家仍然只有一枚、行数不变）。
	before := rawQueryInt(t, raw, `SELECT COUNT(*) FROM users`)
	s.MultiIdentityMigrate()
	if after := rawQueryInt(t, raw, `SELECT COUNT(*) FROM users`); after != before {
		t.Fatalf("重复迁移改动行数 before=%d after=%d ⇒ 不幂等", before, after)
	}
	if bad := rawQueryInt(t, raw, `SELECT COUNT(*) FROM (SELECT account_id FROM users WHERE account_id<>'' GROUP BY account_id HAVING SUM(is_home)<>1)`); bad != 0 {
		t.Fatalf("重复迁移后有账户的家行数 ≠ 1（账户数=%d）⇒ 家行不变式被第二次跑打破", bad)
	}
}

// TestMultiIdentityMigrateReconcilesHomeRows M2：两种坏形态都要收敛成「有且仅有一枚家行」。
func TestMultiIdentityMigrateReconcileHomeRows(t *testing.T) {
	s := newTestStore(t)
	raw := s.DB()
	// 造一个账户的三行（同一邮箱不行——同租户唯一；这里用空邮箱，只测家行收敛）。
	for i, tid := range []int64{2, 3, 4} {
		_, err := s.DB().Exec(`INSERT INTO users (tenant_id,username,password_hash,display_name,role,status,email,account_id,is_home,created_at,updated_at)
			VALUES (?,?,'h','RC','user','active','','',0,datetime('now'),datetime('now'))`, tid, fmt.Sprintf("rc%d", i))
		if err != nil {
			t.Fatalf("造夹具行失败 tid=%d: %v", tid, err)
		}
	}
	// 坏形态①：三枚都是家 ⇒ 只留 id 最小那枚（最早的行＝历史上的本人账号）。
	if _, err := raw.Exec(`UPDATE users SET account_id='rc1', is_home=1 WHERE username LIKE 'rc%'`); err != nil {
		t.Fatalf("写坏形态①失败: %v", err)
	}
	s.MultiIdentityMigrate()
	if homes := rawQueryInt(t, raw, `SELECT COUNT(*) FROM users WHERE account_id='rc1' AND is_home=1`); homes != 1 {
		t.Fatalf("多枚家行未收敛：homes=%d（应恰 1）", homes)
	}
	var keepID int64
	if err := raw.QueryRow(`SELECT id FROM users WHERE account_id='rc1' AND is_home=1`).Scan(&keepID); err != nil {
		t.Fatalf("读收敛后的家行失败: %v", err)
	}
	var minID int64
	if err := raw.QueryRow(`SELECT MIN(id) FROM users WHERE account_id='rc1'`).Scan(&minID); err != nil {
		t.Fatalf("读最小 id 失败: %v", err)
	}
	if keepID != minID {
		t.Fatalf("收敛后留下的家不是最早那行：keep=%d min=%d（D2 规定家＝那一行本人账号）", keepID, minID)
	}
	// 坏形态②：一枚家都没有，但有 active 行 ⇒ 提升 id 最小的可用行。
	if _, err := raw.Exec(`UPDATE users SET is_home=0 WHERE account_id='rc1'`); err != nil {
		t.Fatalf("写坏形态②失败: %v", err)
	}
	s.MultiIdentityMigrate()
	if homes := rawQueryInt(t, raw, `SELECT COUNT(*) FROM users WHERE account_id='rc1' AND is_home=1`); homes != 1 {
		t.Fatalf("无家账户未被提升：homes=%d（应恰 1）", homes)
	}
	if err := raw.QueryRow(`SELECT MIN(id) FROM users WHERE account_id='rc1' AND is_home=1`).Scan(&keepID); err != nil {
		t.Fatalf("读提升后的家失败: %v", err)
	}
	if keepID != minID {
		t.Fatalf("无家账户提升的不是最小可用行：keep=%d min=%d", keepID, minID)
	}
}

// TestEmailUniquenessIsPerTenant M3：同邮箱跨租户可各持一行，同租户必须撞拒。
func TestEmailUniquenessIsPerTenant(t *testing.T) {
	s := newTestStore(t)
	raw := s.DB()
	home, err := s.CreateUserWithEmail(2, "mi-home", "h", "首页人", "user", 0, 0, "mi@example.com", "", 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	if home.AccountID == "" || !home.IsHomeRow() {
		t.Fatalf("新建的第一行必须自带账户号且是家行，实得 account_id=%q is_home=%d", home.AccountID, home.IsHome)
	}
	// 同一账户在另一租户再持一行同邮箱（§6.1 受邀加入另一企业那一档的数据层前提）。
	second, err := s.CreateIdentityRow(home.AccountID, 3, "mi-member", "成员", "user", 0, 0, "mi@example.com")
	if err != nil {
		t.Fatalf("同邮箱跨租户建身份行应合法，实际失败: %v", err)
	}
	if second.IsHome != 0 {
		t.Fatalf("成员行不该是家行（家仍在原行上），实得 is_home=%d", second.IsHome)
	}
	// 同租户内另一个账户撞同一邮箱：数据层必须拒（前置判定与索引两条腿任一条红都算守住）。
	if _, err := s.CreateUserWithEmail(3, "mi-other", "h", "别人", "user", 0, 0, "mi@example.com", "", 0); err == nil {
		t.Fatal("同租户重复邮箱竟然建成功了 ⇒ 按租户唯一没落地（D3 换轨失败）")
	} else if !strings.Contains(err.Error(), "邮箱") {
		t.Fatalf("同租户撞邮箱的报错要点名邮箱，实得: %v", err)
	}
	// 索引形状：库里只剩一把邮箱尺子（全局那条不许复活）。
	if n := rawQueryInt(t, raw, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, EmailUniqueIndexTenant); n != 1 {
		t.Fatalf("按租户邮箱唯一索引不在位（命中 %d，应 1）⇒ 数据层没有兜底，并发注册会造出同租户双号", n)
	}
	if n := rawQueryInt(t, raw, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, EmailUniqueIndexLegacy); n != 0 {
		t.Fatalf("旧**全局**邮箱唯一索引复活（命中 %d，应 0）⇒ 同一个人加入第二个租户会被整库挡死", n)
	}
	// 直接打一条裸 INSERT 验索引本身（前置判定被绕过时这一腿仍要红）。
	if _, err := raw.Exec(`INSERT INTO users (tenant_id,username,password_hash,display_name,role,status,email,account_id,is_home,created_at,updated_at)
		VALUES (3,'mi-raw','h','RAW','user','active','mi@example.com','zz',0,datetime('now'),datetime('now'))`); err == nil {
		t.Fatal("同租户同邮箱的裸 INSERT 竟然落库 ⇒ 唯一索引没生效（前面那条绿灯是假绿）")
	}
}

// TestGetUserByEmailPrefersHomeRow M4：家优先解析，且判据射程在排序腿。
func TestGetUserByEmailPrefersHomeRow(t *testing.T) {
	s := newTestStore(t)
	home, err := s.CreateUserWithEmail(2, "hp-home", "h", "家", "user", 0, 0, "hp@example.com", "", 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	member, err := s.CreateIdentityRow(home.AccountID, 5, "hp-member", "成员", "user", 0, 0, "hp@example.com")
	if err != nil {
		t.Fatalf("建成员行失败: %v", err)
	}
	got, err := s.GetUserByEmail("hp@example.com")
	if err != nil {
		t.Fatalf("按邮箱取家行失败: %v", err)
	}
	if got.ID != home.ID {
		t.Fatalf("家优先没生效：want id=%d got id=%d", home.ID, got.ID)
	}
	// 反证式换家：家标记挪到成员行后，同一个邮箱必须读出另一行。
	// 这一步证明红点落在排序腿（is_home 优先），而不是"数据凑巧只有一行"。
	if err := s.PromoteHomeRow(home.AccountID, member.ID); err != nil {
		t.Fatalf("提升家行失败: %v", err)
	}
	got2, err := s.GetUserByEmail("Hp@Example.com ")
	if err != nil {
		t.Fatalf("换家后按邮箱取家行失败（大小写/空白归一化腿）: %v", err)
	}
	if got2.ID != member.ID {
		t.Fatalf("换家后仍取旧行：want id=%d got id=%d ⇒ GetUserByEmail 的 is_home 排序不生效", member.ID, got2.ID)
	}
	// 按租户那一腿仍要能定位到本租户的行（登录/加入判据用它，不受家优先影响）。
	inT, err := s.GetUserByEmailInTenant("hp@example.com", 5)
	if err != nil {
		t.Fatalf("按租户取行失败: %v", err)
	}
	if inT.ID != member.ID {
		t.Fatalf("按租户取行取错：want id=%d got id=%d", member.ID, inT.ID)
	}
	// 全账户视图：两行都要在，家行在前。
	all, err := s.ListAccountsByEmail("hp@example.com")
	if err != nil {
		t.Fatalf("列账户行失败: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("同邮箱应有 2 个身份行，实得 %d", len(all))
	}
	if !all[0].IsHomeRow() {
		t.Fatal("身份列表没有把家行排在最前（§四 家优先的展示侧同源）")
	}
}

// TestSetUserEmailSyncsWholeAccount M5：改邮箱=改这个人的所有可用身份行；撞别人邮箱整体拒绝。
func TestSetUserEmailSyncsWholeAccount(t *testing.T) {
	s := newTestStore(t)
	raw := s.DB()
	home, err := s.CreateUserWithEmail(2, "se-home", "h", "家", "user", 0, 0, "se@example.com", "", 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	if _, err := s.CreateIdentityRow(home.AccountID, 6, "se-member", "成员", "user", 0, 0, "se@example.com"); err != nil {
		t.Fatalf("建成员行失败: %v", err)
	}
	if err := s.SetUserEmail(home.ID, home.TenantID, "se-new@example.com"); err != nil {
		t.Fatalf("改邮箱失败: %v", err)
	}
	if n := rawQueryInt(t, raw, `SELECT COUNT(*) FROM users WHERE account_id=? AND email='se-new@example.com'`, home.AccountID); n != 2 {
		t.Fatalf("账户级同步没落到全部身份行：命中 %d 行（应 2）⇒ 家行改了、成员行还是旧邮箱，登录取到的行不确定", n)
	}
	// 另一账户在本账户已有的租户里占着目标邮箱 ⇒ 整体拒绝，一行都不许改（部分成功＝双邮箱形态）。
	other, err := s.CreateUserWithEmail(6, "se-other", "h", "别人", "user", 0, 0, "other@example.com", "", 0)
	if err != nil {
		t.Fatalf("建对照账户失败: %v", err)
	}
	if err := s.SetUserEmail(other.ID, other.TenantID, "se-new@example.com"); err == nil {
		t.Fatal("改成别人占用的邮箱竟然成功 ⇒ 换绑会造出同租户双号")
	}
	// 未迁移行（账户号为空）必须退回旧单行写法，且**绝不**写 WHERE account_id=''：
	// 那一支会把库里所有未迁移行一起改掉（比缺陷更坏）。
	if _, err := raw.Exec(`INSERT INTO users (tenant_id,username,password_hash,display_name,role,status,email,account_id,is_home,created_at,updated_at)
		VALUES (9,'se-legacy','h','旧行','user','active','legacy@example.com','',0,datetime('now'),datetime('now'))`); err != nil {
		t.Fatalf("造未迁移夹具行失败: %v", err)
	}
	legacyID := int64(rawQueryInt(t, raw, `SELECT MAX(id) FROM users WHERE username='se-legacy'`))
	if err := s.SetUserEmail(legacyID, 9, "legacy2@example.com"); err != nil {
		t.Fatalf("未迁移行改邮箱失败: %v", err)
	}
	if n := rawQueryInt(t, raw, `SELECT COUNT(*) FROM users WHERE email='legacy2@example.com'`); n != 1 {
		t.Fatalf("未迁移行的改邮箱影响了 %d 行（应恰 1）⇒ 空账户号那一腿写成了账户级 UPDATE", n)
	}
}

// TestSetPasswordForAccountWritesHomeOnly M6+M7：口令只落家行；成员行不持凭证。
func TestSetPasswordForAccountWriteHomeOnly(t *testing.T) {
	s := newTestStore(t)
	raw := s.DB()
	home, err := s.CreateUserWithEmail(2, "pw-home", "oldhash", "家", "user", 0, 0, "pw@example.com", "", 1)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	member, err := s.CreateIdentityRow(home.AccountID, 7, "pw-member", "成员", "user", 0, 0, "pw@example.com")
	if err != nil {
		t.Fatalf("建成员行失败: %v", err)
	}
	if member.PasswordHash != "" {
		t.Fatalf("成员行不该带口令（D4：凭证只存家行），实得 %q", member.PasswordHash)
	}
	n, err := s.SetPasswordForAccount(home.AccountID, "newhash")
	if err != nil {
		t.Fatalf("改口令失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("SetPasswordForAccount 应恰改 1 行（家行），实得 %d ⇒ 要么写进了成员行、要么这个账户没有家", n)
	}
	var hash string
	var tv, must int
	if err := raw.QueryRow(`SELECT password_hash, token_version, must_change_pwd FROM users WHERE id=?`, home.ID).Scan(&hash, &tv, &must); err != nil {
		t.Fatalf("读家行失败: %v", err)
	}
	if hash != "newhash" || tv != 1 || must != 0 {
		t.Fatalf("家行形态不对：hash=%q token_version=%d must_change_pwd=%d（应 newhash/1/0：改密即撤销会话并关两把闸）", hash, tv, must)
	}
	var mHash string
	var mTV int
	if err := raw.QueryRow(`SELECT password_hash, token_version FROM users WHERE id=?`, member.ID).Scan(&mHash, &mTV); err != nil {
		t.Fatalf("读成员行失败: %v", err)
	}
	if mHash != "" || mTV != 0 {
		t.Fatalf("成员行被写入口令/抬了版本：hash=%q tv=%d ⇒ 违反 D4/D7（改密不该撤销企业身份会话）", mHash, mTV)
	}
	// 空账户号必须报错（不许静默当"所有未迁移行"）。
	if _, err := s.SetPasswordForAccount("  ", "x"); err == nil {
		t.Fatal("空账户号改口令竟然成功 ⇒ 会把库里没有账户号的行一起改掉")
	}
	// 没有家的账户 ⇒ 0 行，调用方必须当失败（AGENTS §一·8②）。
	if n, err := s.SetPasswordForAccount("no-such-account", "x"); err != nil || n != 0 {
		t.Fatalf("不存在的账户应回 rows=0 且无错，实得 rows=%d err=%v", n, err)
	}
}

// TestCreateIdentityRowGuards M7：空账户号拒绝、非家、无口令、两把闸不置位。
func TestCreateIdentityRowGuards(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateIdentityRow("", 8, "ci-x", "X", "user", 0, 0, ""); err == nil {
		t.Fatal("空账户号建身份行竟然成功 ⇒ 会静默长出一个新账户（家行不变式当场破）")
	}
	home, err := s.CreateUserWithEmail(8, "ci-home", "h", "家", "user", 0, 0, "ci@example.com", "", 0)
	if err != nil {
		t.Fatalf("建家行失败: %v", err)
	}
	row, err := s.CreateIdentityRow(home.AccountID, 9, "ci-member", "成员", "user", 1, 0, "CI@Example.com ")
	if err != nil {
		t.Fatalf("建成员行失败: %v", err)
	}
	if row.IsHome != 0 || row.PasswordHash != "" || row.MustChangePwd != 0 || row.ProfileSetupRequired != 0 {
		t.Fatalf("成员行形态不对：is_home=%d hash=%q must_change_pwd=%d profile_setup_required=%d（应 0/''/0/0）",
			row.IsHome, row.PasswordHash, row.MustChangePwd, row.ProfileSetupRequired)
	}
	if row.Email != "ci@example.com" {
		t.Fatalf("成员行邮箱没归一化：got %q", row.Email)
	}
	ids, err := s.ListIdentitiesByAccount(home.AccountID)
	if err != nil {
		t.Fatalf("列身份失败: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("应有 2 个身份行，实得 %d", len(ids))
	}
	if ids[0].PasswordHash != "" {
		t.Fatal("ListIdentitiesByAccount 泄露了口令哈希 ⇒ 出栈面必须脱敏（凭证不出 IAM 层）")
	}
	h, err := s.GetHomeRow(home.AccountID)
	if err != nil {
		t.Fatalf("取家行失败: %v", err)
	}
	if h.ID != home.ID {
		t.Fatalf("家行取错：want %d got %d", home.ID, h.ID)
	}
}

// TestPromoteHomeRowScopeGuard M8：只许在本账户的行之间挪家，跨账户必须拒。
func TestPromoteHomeRowScopeGuard(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateUserWithEmail(2, "pr-a", "h", "A", "user", 0, 0, "pr-a@example.com", "", 0)
	if err != nil {
		t.Fatalf("建账户 A 失败: %v", err)
	}
	b, err := s.CreateUserWithEmail(3, "pr-b", "h", "B", "user", 0, 0, "pr-b@example.com", "", 0)
	if err != nil {
		t.Fatalf("建账户 B 失败: %v", err)
	}
	if err := s.PromoteHomeRow(a.AccountID, b.ID); err == nil {
		t.Fatal("把**别人账户**的行提升为 A 的家竟然成功 ⇒ 家标记跨账户漂移，两个人会互相顶身份")
	}
	if err := s.PromoteHomeRow("", a.ID); err == nil {
		t.Fatal("空账户号提升家行竟然成功")
	}
	member, err := s.CreateIdentityRow(a.AccountID, 4, "pr-a2", "A2", "user", 0, 0, "")
	if err != nil {
		t.Fatalf("建 A 的第二行失败: %v", err)
	}
	if err := s.PromoteHomeRow(a.AccountID, member.ID); err != nil {
		t.Fatalf("本账户内提升家行失败: %v", err)
	}
	if n := rawQueryInt(t, s.DB(), `SELECT COUNT(*) FROM users WHERE account_id=? AND is_home=1`, a.AccountID); n != 1 {
		t.Fatalf("提升后 A 的家行数=%d（应恰 1）⇒ 旧家没摘干净（两枚家会让家优先取到不确定行）", n)
	}
	// B 的家不受影响（按账户写，绝不碰别人的行）。
	if n := rawQueryInt(t, s.DB(), `SELECT COUNT(*) FROM users WHERE account_id=? AND is_home=1`, b.AccountID); n != 1 {
		t.Fatalf("B 的家被挪走了（homes=%d）⇒ PromoteHomeRow 的 WHERE 少了账户条件", n)
	}
}

// TestMultiIdentityMigrationFailuresAreLoggedNotFatal 迁移失败口径：只留日志不阻断启动。
// 这一条测的是「闸门别把启动打死」——把 users 表摘掉后 MultiIdentityMigrate 仍不许 panic。
func TestMultiIdentityMigrationFailuresAreLoggedNotFatal(t *testing.T) {
	pinStoreDialect(t)
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	s := &Store{db: raw}
	// 表都不存在：回填腿必须报错出（走 Warn 分支）而不是 panic。
	s.MultiIdentityMigrate()
	if _, err := raw.Exec(`SELECT 1 FROM users LIMIT 0`); err == nil {
		t.Fatal("夹具库里竟然有 users 表 ⇒ 这一段测的不是缺表形态")
	} else if !errors.Is(err, sql.ErrNoRows) && !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("缺表读数报的是别的错（判据射程可疑）: %v", err)
	}
}
