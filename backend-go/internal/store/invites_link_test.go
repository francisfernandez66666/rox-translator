// ============ invites_link_test.go · 职责说明 ============
// 多身份方案 B3（2026-10-24）store 层机械锁：专属邀请链接（invite_codes kind='link'）。
// 对应《多身份账号方案_20261023.md》§十一「store 层」那半句：
//
//	invite_codes 补列幂等 + 存量码行为不变 + 链接五态校验（无效/已用/已撤销/过期/正常）。
//
// 两条判据各治一种历史事故形态：
//   - 「补列幂等」防老库反复启动把补列跑成错误（EnsureColumns 自身幂等，但迁移挂载点写错就恒失败）；
//   - 「存量码行为不变」是本批**唯一真正危险的兼容面**：kind/expires_at 有 DEFAULT，
//     若把 expires_at 的默认写成"当下"或把空串读成"已过期"，现网每一枚历史手输码会在上线那一刻集体失效，
//     而客户是在注册页看到「邀请码无效」才知道的（日志零红、界面照常）。所以这一档必须逐字段点名。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestInviteLink
// =============================================
package store

import (
	"regexp"
	"testing"
	"time"
)

// TestInviteLinkMigrateIdempotent 补列幂等：同一库连跑三次迁移不报错、建码建链接都正常。
func TestInviteLinkMigrateIdempotent(t *testing.T) {
	s := newTestStoreWithTenants(t)
	for i := 0; i < 3; i++ {
		s.InviteLinkMigrate()
	}
	if _, err := s.CreateInviteCode("LINKMIG-CODE", 1); err != nil {
		t.Fatalf("补列后建手输码失败: %v", err)
	}
	code, err := NewInviteLinkCode()
	if err != nil {
		t.Fatalf("生成链接码失败: %v", err)
	}
	if _, err := s.CreateInviteLink(code, 1, 0, "", "", "", ""); err != nil {
		t.Fatalf("补列后建链接失败: %v", err)
	}
}

// TestInviteLinkLegacyCodeShapeUnchanged ★ 存量码行为不变（本批最要紧的一档）。
// 手输码走 CreateInviteCode（INSERT 语句里根本没有 kind/expires_at 那几列），
// 补列后读回来的五字段必须是「语义上的旧形态」：kind=code、四个新字段全空、Status 与改造前逐字相同。
func TestInviteLinkLegacyCodeShapeUnchanged(t *testing.T) {
	s := newTestStoreWithTenants(t)
	c, err := s.CreateInviteCode("LEGACY-1", 1)
	if err != nil {
		t.Fatalf("建手输码失败: %v", err)
	}
	if c.Kind != "code" {
		t.Fatalf("存量手输码 kind 必须是 'code'（DEFAULT 兜底），实得 %q", c.Kind)
	}
	if c.ExpiresAt != "" {
		t.Fatalf("存量码 expires_at 必须是空串（空=永不过期），实得 %q", c.ExpiresAt)
	}
	if c.IsExpired() {
		t.Fatalf("存量码判成已过期＝现网每一枚历史码上线即失效，绝不允许")
	}
	if c.Status != "active" {
		t.Fatalf("存量码派生状态应为 active（与改造前逐字相同），实得 %q", c.Status)
	}
	if c.BoundEmail != "" || c.BoundUsername != "" || c.Role != "" {
		t.Fatalf("存量码的三个预绑字段应为空，实得 email=%q username=%q role=%q", c.BoundEmail, c.BoundUsername, c.Role)
	}

	// 反向对照：手输码被烧掉之后仍然不认「过期」这一档（revoked/used 的优先级也不许被过期插队）。
	if _, err := s.MarkInviteCodeUsed(c.ID, "someone"); err != nil {
		t.Fatalf("占用手输码失败: %v", err)
	}
	got, err := s.GetInviteCodeByCode("LEGACY-1")
	if err != nil {
		t.Fatalf("读回已用手输码失败: %v", err)
	}
	if got.Status != "used" {
		t.Fatalf("已用手输码状态应仍为 used，实得 %q", got.Status)
	}

	// 另一枚存量码撤销后也必须读成 revoked（新加 expired 档不许插到 revoked 前面）。
	c2, err := s.CreateInviteCode("LEGACY-2", 1)
	if err != nil {
		t.Fatalf("建第二枚手输码失败: %v", err)
	}
	if ok, _, e := s.RevokeInviteCode(c2.ID); !ok || e != nil {
		t.Fatalf("撤销存量码应成功，实得 ok=%v err=%v", ok, e)
	}
	if g2, _ := s.GetInviteCodeByCode("LEGACY-2"); g2.Status != "revoked" {
		t.Fatalf("撤销存量码状态应仍为 revoked，实得 %q", g2.Status)
	}
}

// TestInviteLinkCodeShape 链接码形状与不可枚举性：`inv_` + 32 位小写 hex，两次生成不相等。
// 判这个形状而不是只判长度——kind 列才是语义判据，码串形状是「外人猜不猜得到」的那一维，
// 长度够但字符集退化（例如只出 hex 前 8 位）在测试里同样能过长度判据。
func TestInviteLinkCodeShape(t *testing.T) {
	re := regexp.MustCompile(`^inv_[0-9a-f]{32}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := NewInviteLinkCode()
		if err != nil {
			t.Fatalf("生成链接码失败: %v", err)
		}
		if !re.MatchString(c) {
			t.Fatalf("链接码形状不合 §6.2 口径（inv_<32hex>）: %q", c)
		}
		if seen[c] {
			t.Fatalf("链接码出现重复（不可枚举性已破）: %q", c)
		}
		seen[c] = true
	}
}

// TestInviteLinkFiveStates ★ 链接五态：正常 / 已使用 / 已撤销 / 已过期 / 无效。
// 五档必须各自可分辨（公开校验端点要按原因给明确文案），且每一档都真去读那一个字段。
func TestInviteLinkFiveStates(t *testing.T) {
	s := newTestStoreWithTenants(t)
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	past := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)

	// ① 正常：预绑信息逐字段回显（公开端点要拿这些去预填注册页）。
	okCode, err := NewInviteLinkCode()
	if err != nil {
		t.Fatalf("生成链接码失败: %v", err)
	}
	must := func(c *InviteCode, e error) *InviteCode {
		t.Helper()
		if e != nil {
			t.Fatalf("建链接失败: %v", e)
		}
		return c
	}
	l1 := must(s.CreateInviteLink(okCode, 1, 0, "user", "张三", "zhang@example.test", future))
	if l1.Status != "active" {
		t.Fatalf("未过期链接应 active，实得 %q", l1.Status)
	}
	if l1.Kind != "link" || l1.Role != "user" || l1.BoundUsername != "张三" || l1.BoundEmail != "zhang@example.test" || l1.ExpiresAt != future {
		t.Fatalf("链接预绑信息回显不全: %+v", l1)
	}
	got, err := s.GetInviteLinkByCode(okCode)
	if err != nil || got == nil || got.ID != l1.ID {
		t.Fatalf("公开校验按码串取链接失败: got=%+v err=%v", got, err)
	}

	// ② 已使用：烧码后状态翻成 used，且注册口二次占用被拒（一次性契约）。
	if claimed, _ := s.MarkInviteCodeUsed(l1.ID, "zhang"); !claimed {
		t.Fatalf("首次烧码应成功")
	}
	if g, _ := s.GetInviteLinkByCode(okCode); g == nil || g.Status != "used" {
		t.Fatalf("已用链接应读成 used，实得 %+v", g)
	}
	if claimed, _ := s.MarkInviteCodeUsed(l1.ID, "another"); claimed {
		t.Fatalf("已烧链接二次占用必须被拒（链接注册成功即失效）")
	}

	// ③ 已撤销：撤销位优先于「未过期」。
	revCode, _ := NewInviteLinkCode()
	l2 := must(s.CreateInviteLink(revCode, 1, 0, "user", "", "", future))
	if ok, _, err := s.RevokeInviteCode(l2.ID); !ok || err != nil {
		t.Fatalf("撤销链接失败: ok=%v err=%v", ok, err)
	}
	if g, _ := s.GetInviteLinkByCode(revCode); g == nil || g.Status != "revoked" {
		t.Fatalf("已撤销链接应读成 revoked，实得 %+v", g)
	}
	// 撤销与烧码互斥（同 G9 那套原子条件更新）：撤销后注册口必须拒收。
	if claimed, _ := s.MarkInviteCodeUsed(l2.ID, "someone"); claimed {
		t.Fatalf("已撤销链接不许被占用")
	}

	// ④ 已过期：expires_at 在过去 ⇒ Status=expired，且注册口拒收。
	expCode, _ := NewInviteLinkCode()
	l3 := must(s.CreateInviteLink(expCode, 1, 0, "user", "", "", past))
	if g, _ := s.GetInviteLinkByCode(expCode); g == nil || !g.IsExpired() || g.Status != "expired" {
		t.Fatalf("过期链接应 IsExpired=true/Status=expired，实得 %+v", g)
	}
	// 过期是**状态派生**而不是行内状态位：used 仍为 0、行仍在（管理台要把这行列成「已过期」给人看，
	// 而不是让它凭空消失——"谁拿到过这枚码"和"这枚码后来没用了"都是追溯资产）。
	if g, _ := s.GetInviteCode(l3.ID); g == nil || g.Used != 0 || g.Revoked != 0 {
		t.Fatalf("过期链接的行内状态位应保持未用未撤销，实得 %+v", g)
	}

	// ⑤ 无效：不存在的码串取不到行且**不报错**（公开面「查不到」是业务态不是故障）。
	if g, err := s.GetInviteLinkByCode("inv_0000000000000000000000000000dead"); err != nil || g != nil {
		t.Fatalf("不存在的链接码应 (nil, nil)，实得 got=%+v err=%v", g, err)
	}
	// ⑤b 同族第五档：手输码不许从「链接」这一面出栈（它没有预绑信息，回显会误导用户）。
	if _, err := s.CreateInviteCode("PLAIN-CODE-1", 1); err != nil {
		t.Fatalf("建手输码失败: %v", err)
	}
	if g, err := s.GetInviteLinkByCode("PLAIN-CODE-1"); err != nil || g != nil {
		t.Fatalf("手输码不该被当成链接，实得 got=%+v err=%v", g, err)
	}
}

// TestInviteLinkExpiryBoundary 到期判定的三档边界：到期前 1 秒未过期、到点即过期、坏时间串判未过期（fail-open）。
// 单测喂固定 now 而不是等墙钟跨过那一秒——`IsExpiredAt` 存在的唯一理由就是这个。
func TestInviteLinkExpiryBoundary(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	at := now.Format(time.RFC3339)
	c := &InviteCode{ExpiresAt: at}
	if c.IsExpiredAt(now.Add(-time.Second)) {
		t.Fatalf("到期前 1 秒不许判过期（链接提前失效＝客户昨天拿到的码今天点不开）")
	}
	if !c.IsExpiredAt(now) {
		t.Fatalf("到期时刻应判过期（>= 语义，边界归过期一侧）")
	}
	if !c.IsExpiredAt(now.Add(time.Second)) {
		t.Fatalf("到期后应判过期")
	}
	// 坏时间串 ⇒ 判未过期（方向是 fail-open：写坏了的时间戳不该把管理员刚发出去的链接判成失效）。
	if (&InviteCode{ExpiresAt: "not-a-time"}).IsExpiredAt(now) {
		t.Fatalf("无法解析的 expires_at 必须判未过期")
	}
	// 空串 ⇒ 永不过期（存量码那一档，见 TestInviteLinkLegacyCodeShapeUnchanged）。
	if (&InviteCode{ExpiresAt: ""}).IsExpiredAt(now) {
		t.Fatalf("空 expires_at 必须判永不过期")
	}
	// 带时区偏移的时间串也要解得开（PG/SQLite 与运维手填都可能给 +08:00 形态）。
	if !(&InviteCode{ExpiresAt: "2026-10-24T20:00:00+08:00"}).IsExpiredAt(now) {
		t.Fatalf("同一时刻的 +08:00 写法必须与 UTC 写法等值判过期")
	}
}

// TestInviteLinkListScope 管理台列表：只列 kind='link'，且租户维度按「tid<=0＝平台视角」那把尺子。
// 读侧与写侧同尺（AGENTS §一·8）在这里的表现是：链接列表不许混进手输码，
// 否则管理台「待使用/已使用/已撤销/已过期」四档筛选会把手输码算进链接数，管理员按错的数去撤销。
func TestInviteLinkListScope(t *testing.T) {
	s := newTestStoreWithTenants(t)
	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	if _, err := s.CreateInviteCode("LIST-PLAIN", 1); err != nil {
		t.Fatalf("建手输码失败: %v", err)
	}
	lc, _ := NewInviteLinkCode()
	if _, err := s.CreateInviteLink(lc, 1, 0, "user", "", "", future); err != nil {
		t.Fatalf("建链接失败: %v", err)
	}
	links, err := s.ListInviteLinks(1)
	if err != nil {
		t.Fatalf("列链接失败: %v", err)
	}
	if len(links) != 1 || links[0].Code != lc {
		t.Fatalf("本租户链接列表应只含那一份链接，实得 %d 条 %+v", len(links), links)
	}
	// 平台视角（tid<=0）同样只列链接、不列手输码。
	all, err := s.ListInviteLinks(0)
	if err != nil {
		t.Fatalf("平台视角列链接失败: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("平台视角应 1 条链接，实得 %d", len(all))
	}
	// 反向对照：手输码全集列表照旧含两形态（它本来就是 invite_codes 的全量视图，本批不许收窄）。
	codes, err := s.ListInviteCodes()
	if err != nil || len(codes) != 2 {
		t.Fatalf("手输码列表应含 2 行（一码一链接），实得 %d err=%v", len(codes), err)
	}
}
