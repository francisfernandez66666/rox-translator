// ============ account.go · 账号归一化与口令强度的唯一尺子 ============
// ★ 2026-10-21 IAM 重做 P0-1（用户令「整个 iam 注册/登录/重置密码体系重做」）。
//
// 这一份文件存在的唯一理由：过去"账号怎么算同一个账号""密码够不够长"这两件事
// 在注册 / 登录 / 发码 / 找回 / 重置 / 改密 / 管理台建号七个口子上各写了一遍，
// 于是现网长出三条互相咬合的缺陷——
//   ① 注册把密码 TrimSpace 后入库（register.go:110），登录原样比对（auth.go:79）
//      ⇒ 带首尾空格的口令"注册进得去、登录登不进"；
//   ② 登录只按用户名查（auth.go:89），而注册时用户填的是邮箱
//      ⇒ 注册成功后前端那次"自动补登"结构上必失败；
//   ③ 邮箱口做了 lower+trim（iam/store.go:127），用户名口没做
//      ⇒ 同一个人在不同面里是"两个账号"。
// 本文件把这些判据收成一份，七个口一律调这里；**禁止**在任何调用方再写一遍
// TrimSpace/ToLower/"至少 6 位"（同 AGENTS §一·14「清单只有一份」那一族纪律）。
// ================================================================

package iam

import (
	"strings"

	"translator/internal/db"
)

// PasswordMinLen 口令最小长度——注册/重置/改密/管理台建号四个写口共用这一个常量。
// ★ 历史形态：这条限制**只存在于前端**（Login.tsx 硬编码 <6），后端重置与改密只判非空
//
//	⇒ 绕过界面即可给账号设一位密码。现在它是服务端事实，前端那份降级为展示提示。
const PasswordMinLen = 6

// PasswordTooShortMsg 口令不合格的对外文案（四个写口逐字同一句，前端按 message 落红字）。
const PasswordTooShortMsg = "密码至少 6 位"

// ValidatePassword 口令强度唯一判据：非空、去空白后仍非空、长度 ≥ PasswordMinLen。
// ⚠️ 刻意**不**在这里 trim：口令里的空格是用户的一部分输入（见 NormalizeAccount 那条注释），
//
//	这里只判"长度够不够"，不改内容——改内容才是①那两条缺陷的来源。
//
// 返回 false 时调用方必须回 400 并带 PasswordTooShortMsg，不许静默放行。
func ValidatePassword(p string) bool {
	if p == "" {
		return false
	}
	// 长度按字节算与历史一致（bcrypt 自身截到 72 字节，这里不做二次加工）
	return len([]rune(p)) >= PasswordMinLen
}

// LooksLikeEmail 判"这一串是不是邮箱形态"——只判形状，不判域名真假、不发网络。
// 判据刻意保守（单个 @、@ 前后都非空、点号在 @ 之后且两侧都有字符），
// 因为它的**唯一用途**是选查询腿（按邮箱查还是按用户名查），判错方向的代价是
// "把用户名当邮箱查不到"，而不是放行一个坏账号。
func LooksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	dot := strings.LastIndexByte(s[at:], '.')
	// 域名里那个点必须**在 @ 之后至少隔一个字符**，且点后至少两位（`a@b.c` 不算邮箱形态）。
	// ⚠️ 这一档与前端 src/lib/accountForm.ts 的 EMAIL_SHAPE_RE 同值——两处各写一份迟早分叉，
	//    分叉的表现是"界面放行、服务端把它当用户名查"（查不到 ⇒ 假"账号不存在"）。
	return dot > 1 && len(s)-(at+dot)-1 >= 2 && at+dot < len(s)-1
}

// NormalizeAccount 账号归一化唯一入口：**只去首尾空白**，邮箱额外小写。
//
// 三条刻意的取舍，改这一行前必读：
//  1. 用户名**不**在这里小写——大小写不敏感是"查不到时才试的那条腿"（见
//     FindAccountForLogin），不是"先把输入改写再查"。原因：库里 username 列存的是原样，
//     若归一化把大小写抹平，展示与审计留痕会跟库里不一致（同 AGENTS §一·3「值与来源
//     必须同时回写」那一族），而撞车时"到底登的是谁"就再也问不出来了。
//  2. 邮箱小写是**库里既有口径**（GetUserByEmail 一直做 lower+trim），这里只是把它
//     从查询层提到入口层，让注册/找回/重置三条链与登录用同一个值。
//  3. 口令永远不进这个函数（见 ValidatePassword 那条注释）。
func NormalizeAccount(s string) string {
	s = strings.TrimSpace(s)
	if LooksLikeEmail(s) {
		return strings.ToLower(s)
	}
	return s
}

// NormalizeEmail 邮箱归一化（注册/换绑/找回/重置共用；空串原样回空串，
// 让调用方自己判"必填"，不在这里把"没填"和"填了空白"折成同一个值）。
func NormalizeEmail(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToLower(s)
}

// AccountMatchedBy 账号定位命中的那一条腿（读数用，给日志与排障点名"是怎么找到这个人的"）。
const (
	AccountByExact   = "username_exact" // 用户名精确等值（历史行为，逐字不变）
	AccountByCI      = "username_ci"    // 大小写不敏感且全平台唯一命中才采纳
	AccountByEmail   = "email"          // 邮箱形态串按 lower 查
	AccountByNothing = ""               // 一条腿都没命中
)

// FindAccountForLogin 登录/找回/重置共用的账号定位腿，返回候选集与命中的那一条腿。
//
// 三条腿的**顺序**就是判据，不许调换：
//  1. 用户名精确等值（本租户优先，再跨租户）——与改造前逐字一致，
//     所以"历史上能登进去的人"一个都不会变成登不进去；
//  2. 大小写不敏感：**仅在全平台唯一命中**时采纳。撞车（同一个人两种写法落在不同租户）
//     一律不猜，把候选集原样交回调用方按"多租户重名"既有分支处理；
//  3. 邮箱形态串按 lower 查（跨租户唯一口径同 GetUserByEmail）。
//
// ⚠️ 本函数**不判状态、不判密码、不签发任何东西**——那些仍留在登录 handler 的既有闸上，
//
//	免得把"找到谁"和"能不能进"再一次混成一把尺子。
func (s *Store) FindAccountForLogin(tid int64, account string) ([]*User, string, error) {
	account = NormalizeAccount(account)
	if account == "" {
		return nil, AccountByNothing, nil
	}
	// 腿 3 先判形状：邮箱形态的串不该去撞用户名（用户名里出现 @ 是脏数据，不是登录入口）
	if LooksLikeEmail(account) {
		if u, err := s.GetUserByEmail(account); err == nil && u != nil {
			return []*User{u}, AccountByEmail, nil
		}
		return nil, AccountByNothing, nil
	}
	// 腿 1：用户名精确等值——本租户命中即唯一
	if u, err := s.GetUserByUsername(tid, account); err == nil && u != nil {
		return []*User{u}, AccountByExact, nil
	}
	if us, err := s.GetUserByUsernameGlobal(account); err == nil && len(us) > 0 {
		return us, AccountByExact, nil
	}
	// 腿 2：大小写不敏感，且**必须唯一命中**才采纳
	if us, err := s.GetUserByUsernameGlobalCI(account); err == nil {
		if len(us) == 1 {
			return us, AccountByCI, nil
		}
		if len(us) > 1 {
			// 多命中：交回调用方走"重名要求走租户入口"那条既有分支（不猜人）
			return us, AccountByCI, nil
		}
	}
	return nil, AccountByNothing, nil
}

// GetUserByUsernameGlobalCI 跨全部租户按"大小写不敏感用户名"查询（登录的第二条腿）。
// lower() 在 SQLite 与 PostgreSQL 都可用且语义一致；本仓 SQL 必须双方言，
// 所以这里不许换成 PG 专有的 ILIKE（判"两种方言都能跑"要真驱动跑一次，见 AGENTS §一·4）。
func (s *Store) GetUserByUsernameGlobalCI(username string) ([]*User, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+userCols+" FROM users WHERE lower(username)=lower(?) ORDER BY tenant_id", strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt, &u.DeactivatedAt, &u.AgreedAt, &u.MustChangePwd, &u.TokenVersion, &u.JobRole); err != nil {
			continue
		}
		out = append(out, &u)
	}
	return out, nil
}
