// ============ 本文件职责中文说明 ============
// #46 超管站内信「一把尺子」的行为锁（2026-10-14，用户令「采集与告警类通知收敛成一把尺子」）。
//
// 收敛前的真缺陷（不是形状难看）：投递面按角色**字面量**圈人，五处各不相同——
// 反馈／线索那三处只认 'admin'，而权限判定侧 RoleLevel≥4 认 admin/super_admin 双值、
// 建号与后台提权实际写 super_admin。⇒ 一个只有 super_admin 账号的部署（新库即此形态），
// 问题反馈与销售线索的站内信**一条都发不到人**：提交方以为超管看见了，铃铛里其实什么都没有。
// 下面 L1 就是这条缺陷的复现锁——收敛后必须送达。
//
// 五条判据：
//
//	L1 只有 super_admin 账号 ⇒ 送达（收敛前必红）；
//	L2 判定与投递同源 ⇒ 挂 tenant_id>0 的 role='admin' 旁路行**不收**（IsSuperAdmin 要求平台层）；
//	L3 停用账号不收、deactivating 宽限期当日仍收（与登录闸同一条判据）；
//	L4 ref_type 取自唯一名单；名单外的值**不拦投递**（只出声），且常量值与收敛前落库的字面量逐字相同
//	   ——改了字面量＝存量行的 ref_type 认不到，前端落点表跟着错位；
//	L5 Store 为空 ⇒ 退 0，不 panic，更不许降级成"发给所有人"。
//
// 方言自钉（AGENTS §一·4）：config.Default() 有副作用写全局 config.C，run_uat 的 PG 模式会把
// 方言泄漏给同包后续内存 SQLite 测试，所以每条用例开头都钉一次 sqlite。
// =============================================
package api

import (
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// pinSQLite 把方言钉成本地内存 SQLite（见文件头「方言自钉」那条）。
func pinSQLite(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// countRef 数某用户收到的某类站内信条数（按 ref_type 过滤，读的是铃铛真正会读的那张表）。
func countRef(t *testing.T, st *store.Store, uid int64, refType string) int {
	t.Helper()
	list, err := st.ListNotifications(uid, 100, 0)
	if err != nil {
		t.Fatalf("读取 uid=%d 的站内信失败: %v", uid, err)
	}
	n := 0
	for _, x := range list {
		if x.RefType == refType {
			n++
		}
	}
	return n
}

// TestSuperAdminRecipientsUseAuthPredicate L1＋L2＋L3：收件人名单必须来自鉴权侧的同一谓词。
func TestSuperAdminRecipientsUseAuthPredicate(t *testing.T) {
	pinSQLite(t)
	st := newTestAPIStore(t)
	s := &Server{Store: st}

	// ① 平台超管（新库/后台提权实际写的就是这个字面量）——收敛前三处调用点都认不到它
	super, err := st.CreateUser(0, "only_super", "x", "唯一超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	// ② 挂租户的 role='admin' 旁路行：ListUsers(0) 物理上取不到它，留作"收件人必须按平台层取"的对照
	alias, err := st.CreateUser(7, "tenant_admin_alias", "x", "租户管理员", store.RoleAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	// ③ 已停用的历史超管账号：登录进不来，站内信也不该发给他
	disabled, err := st.CreateUser(0, "old_super", "x", "停用超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建停用超管失败: %v", err)
	}
	if err := st.UpdateUser(disabled.ID, 0, "停用超管", store.RoleSuperAdmin, "disabled", 0); err != nil {
		t.Fatalf("停用账号失败: %v", err)
	}
	// ④ 注销宽限期内的超管：当日仍可登录使用 ⇒ 当日仍该收到（与登录闸同判据）
	grace, err := st.CreateUser(0, "grace_super", "x", "宽限期超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建宽限期超管失败: %v", err)
	}
	if err := st.UpdateUser(grace.ID, 0, "宽限期超管", store.RoleSuperAdmin, "deactivating", 0); err != nil {
		t.Fatalf("置注销宽限期失败: %v", err)
	}
	// ⑤ 挂在平台层（tenant_id=0）但等级不够的账号：这条专门给 IsSuperAdmin 的**等级那一半**加射程。
	//    没有它，"只判 tenant_id、不判角色等级"的写法能全绿通过本用例（反证 A6 首跑就是这么暴露的）。
	underLevel, err := st.CreateUser(0, "level3_at_zero", "x", "平台层三级号", store.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建平台层三级账号失败: %v", err)
	}

	got := s.notifySuperAdmins("收到新的问题反馈", "正文", store.NotifyRefFeedback, 42)
	if got != 2 {
		t.Fatalf("送达人数 = %d，期望 2（active 超管＋宽限期超管）；"+
			"若为 0 ⇒ 投递面又用上了认不出 super_admin 的旧口径（#46 缺陷复现）；"+
			"若 >2 ⇒ 停用号、平台层低等级号或租户旁路行被圈进来了", got)
	}
	// 逐人核对：名单里只许有那两位，各 1 条
	for _, u := range []*store.User{super, grace} {
		if n := countRef(t, st, u.ID, store.NotifyRefFeedback); n != 1 {
			t.Errorf("超管 %s(id=%d) 收到该类站内信 %d 条，期望 1", u.Username, u.ID, n)
		}
	}
	// 负向对照（★ 恒零命中必须有正对照在上一条里，否则这条只是空转）
	for _, u := range []*store.User{alias, disabled, underLevel} {
		if n := countRef(t, st, u.ID, store.NotifyRefFeedback); n != 0 {
			t.Errorf("%s(id=%d) 收到 %d 条平台超管站内信，期望 0 ⇒ 投递面与鉴权面不同源", u.Username, u.ID, n)
		}
	}
}

// TestNotifySuperAdminsRefTypeSingleList L4：ref_type 走唯一名单，名单外不拦投递；常量值逐字钉死。
func TestNotifySuperAdminsRefTypeSingleList(t *testing.T) {
	pinSQLite(t)
	st := newTestAPIStore(t)
	s := &Server{Store: st}
	super, err := st.CreateUser(0, "list_super", "x", "名单超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	// 采集这一类走唯一写点后必须仍能落库，且 ref_type 与收敛前那份裸 SQL 写进去的值逐字相同
	if n := s.notifySuperAdmins("行业/语言文化包采集完成", "正文", store.NotifyRefKbScrape, 0); n != 1 {
		t.Fatalf("采集通知送达人数 = %d，期望 1", n)
	}
	if got := countRef(t, st, super.ID, store.NotifyRefKbScrape); got != 1 {
		t.Fatalf("采集通知落库条数 = %d，期望 1", got)
	}
	// 名单外类型：投递照旧（把运维触挡掉换来的只是一条难看日志，方向不对）
	if n := s.notifySuperAdmins("未知类目", "正文", "not_in_list_yet", 0); n != 1 {
		t.Fatalf("名单外类型送达人数 = %d，期望 1（唯一写点不许悄悄丢投递）", n)
	}
	if got := countRef(t, st, super.ID, "not_in_list_yet"); got != 1 {
		t.Fatalf("名单外类型落库条数 = %d，期望 1", got)
	}
	// 六个字面量逐个钉死：改任一个＝存量行的 ref_type 与前端落点表错位（跨语言锁读的就是这份）
	want := []string{"ticket", "feedback", "quota", "tenant", "kb_scrape", "pay_manual"}
	if len(store.NotifyRefTypes) != len(want) {
		t.Fatalf("NotifyRefTypes 长度 = %d，期望 %d", len(store.NotifyRefTypes), len(want))
	}
	for i, k := range want {
		if store.NotifyRefTypes[i] != k {
			t.Errorf("NotifyRefTypes[%d] = %q，期望 %q（顺序与字面量都被前端落点表依赖）", i, store.NotifyRefTypes[i], k)
		}
	}
	// 常量＝字面量（防止"改名式重构"把落库值一起换掉）
	for _, p := range [][2]string{
		{store.NotifyRefTicket, "ticket"},
		{store.NotifyRefFeedback, "feedback"},
		{store.NotifyRefQuota, "quota"},
		{store.NotifyRefTenant, "tenant"},
		{store.NotifyRefKbScrape, "kb_scrape"},
		{store.NotifyRefPayManual, "pay_manual"},
	} {
		if p[0] != p[1] {
			t.Errorf("ref_type 常量被改成 %q，历史行仍是 %q ⇒ 铃铛落点认不到", p[0], p[1])
		}
	}
}

// TestNotifySuperAdminsNilStoreIsSilentZero L5：Store 为空 ⇒ 退 0，不 panic、不猜收件人。
func TestNotifySuperAdminsNilStoreIsSilentZero(t *testing.T) {
	pinSQLite(t)
	s := &Server{} // Store=nil
	if n := s.notifySuperAdmins("标题", "正文", store.NotifyRefFeedback, 1); n != 0 {
		t.Fatalf("Store 为空时送达人数 = %d，期望 0", n)
	}
}
