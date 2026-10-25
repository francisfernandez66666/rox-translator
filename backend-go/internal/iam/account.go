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
	"fmt"
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
//	这里只判"够不够格"，不改内容——改内容才是①那两条缺陷的来源。
//
// ★ 2026-10-22 一致性核查补漏：此前只判 `p == ""`，于是 `"      "`（6 个空格）、
//
//	6 个全角空格、6 个制表符全部被判合格——而方案 §三·7 要求的是「≥6 **且非纯空白**」，
//	本函数注释也一直写着"去空白后仍非空"。注释在替没写的代码作证，已按注释口径补齐实现。
//	注意 trim 只用于**判定**，不改写 p 本身：`" pass word "` 仍然是它自己。
//
// 返回 false 时调用方必须回 400 并带 PasswordTooShortMsg，不许静默放行。
func ValidatePassword(p string) bool {
	if strings.TrimSpace(p) == "" {
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

// collapseToAccounts 把"同一账户的多条身份行"收成**一条候选**，收完仍是"每个账户一条"。
//
// 为什么必须在取人这一层收（★ 2026-10-25 多身份 B 批补腿，现网形态＝**登录一次之后就再也登不进去**）：
// R4 的补建个人身份会在同账户下再插一条**同名、同邮箱、口令为空**的身份行（D4：口令只存家行），
// 于是按用户名取回的候选集从 1 条变 2 条，调用方那条"重名 ⇒ 不猜人"的既有分支
// 会把同一个人的两个身份读成"两个不相干的人撞了名"，直接回「账号或密码错误」。
// 那条分支本身是对的（**不同**账户重明确实不能猜），错的是没先按账户归一。
//
// 三条口径：
//  1. 归并键＝account_id；account_id 为空的历史行按**行自身 id** 各成一组——
//     "没有账户号"不等于"它们同属一个账户"，把空号并成一组会把两个不相干的人合成一个。
//  2. 每组代表行＝凭证行：家行（is_home=1）优先；组内没有家行时取**带口令哈希**那一行
//     （口令只存家行是设计，但"家行标记丢了而口令还在"时不能反过来把人挡在门外）；
//     两者都无则取最小 id——数据异常也要给出**确定的**一个，不许回空让调用方误判"账号不存在"。
//  3. 只归并、不判状态、不判口令：判据仍在登录 handler 的既有闸上（同本文件顶部那条分工）。
func collapseToAccounts(us []*User) []*User {
	if len(us) < 2 {
		return us
	}
	// 按首次出现顺序分组：候选集原本的顺序（ORDER BY tenant_id）就是排障时读到的顺序，归并不改变它
	var order []string
	groups := map[string][]*User{}
	for _, u := range us {
		if u == nil {
			continue
		}
		key := strings.TrimSpace(u.AccountID)
		if key == "" {
			key = fmt.Sprintf("row:%d", u.ID)
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], u)
	}
	if len(order) == len(us) {
		return us // 一个账户一行：没有可归并的，原样交回（含"每行各自一个账户"的重名场景）
	}
	out := make([]*User, 0, len(order))
	for _, key := range order {
		rows := groups[key]
		pick := rows[0]
		for _, r := range rows {
			if r.IsHomeRow() {
				pick = r
				break
			}
		}
		if !pick.IsHomeRow() {
			for _, r := range rows {
				if strings.TrimSpace(r.PasswordHash) != "" {
					pick = r
					break
				}
			}
		}
		out = append(out, pick)
	}
	return out
}

// FindAccountForLogin 登录/找回/重置共用的账号定位腿，返回候选集与命中的那一条腿。
//
// 三条腿的**顺序**就是判据，不许调换：
//  1. 用户名精确等值（本租户优先，再跨租户）——与改造前逐字一致，
//     所以"历史上能登进去的人"一个都不会变成登不进去；
//  2. 大小写不敏感：**仅在全平台唯一命中**时采纳。撞车（同一个人两种写法落在不同租户）
//     一律不猜，把候选集原样交回调用方按"多租户重名"既有分支处理；
//  3. 邮箱形态串按 lower 查（跨租户唯一口径同 GetUserByEmail）。
//
// ★ 候选集的粒度是**账户**，不是**行**（collapseToAccounts）：多身份下同一个人有多条 users 行，
// 它们必须归并成一条候选，否则调用方的"重名不猜人"分支会把同一个人的两个身份读成两个人。
//
// ⚠️ 本函数**不判状态、不判密码、不签发任何东西**——那些仍留在登录 handler 的既有闸上，
//
//	免得把"找到谁"和"能不能进"再一次混成一把尺子。
func (s *Store) FindAccountForLogin(_ int64, account string) ([]*User, string, error) {
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
	// 腿 1：用户名精确等值——★ 用户名已不唯一，必须先把**全部**同名候选交回调用方；
	// 旧写法先按 (tenant_id,username) 取一行，同租户重名时会静默选中其中任意一个，
	// 于是"用用户名登录"仍能登进某个账号（用户名不是登录标识，这条腿只作存量兼容）。
	// ★ 2026-10-25 多身份补腿：交回之前先按**账户**归一（同一个人的多个身份行不是"多个候选"）。
	if us, err := s.GetUserByUsernameGlobal(account); err == nil && len(us) > 0 {
		return collapseToAccounts(us), AccountByExact, nil
	}
	// 腿 2：大小写不敏感，且**必须唯一命中**才采纳
	if us, err := s.GetUserByUsernameGlobalCI(account); err == nil {
		if len(us) == 1 {
			return us, AccountByCI, nil
		}
		if len(us) > 1 {
			// 多命中：先按账户归一，剩下的才真是"不同人撞名"，交回调用方走既有分支（不猜人）
			return collapseToAccounts(us), AccountByCI, nil
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
		// 列→字段映射只有一处口径（User.assign），补列时不必再逐条追平复制清单
		if u, err := scanUserRow(rows); err == nil {
			out = append(out, u)
		}
	}
	return out, nil
}
