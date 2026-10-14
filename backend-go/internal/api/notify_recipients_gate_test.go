// ============ 本文件职责中文说明 ============
// #46 静态闸（2026-10-14）：站内信的**超管收件人**只许由 internal/api/notify_recipients.go 一处圈定。
//
// 为什么行为锁不够：notify_recipients_test.go 锁的是"这一处口径对不对"，
// 而本批的缺陷形态是**五处各写一套**（三处只认一个角色字面量、一处双值合并、一处裸 SQL 全表）。
// 只要仓库里还允许出现第二处圈超管的写法，下一位加通知的人就会照着"最近的那一份"抄，
// 于是这套口径迟早再长第二次——这正是 F-33 修了 pay 面、其余三面继续错的原因。
// 所以这里守的是**写法**：api 层里凡是"按角色字面量圈平台收件人"的形态一律判红。
//
// 四条腿：
//
//	G1 全 api 包（剥注释后）不许出现 `ListUsersByRole(0,`——平台层按角色字面量圈人；
//	G2 全 api 包不许出现"SQL 里按角色名单圈人"（`role IN (` 同行/邻行带 super_admin）；
//	G3 四个迁移点必须真的走唯一写点（按文件点名，防"删了旧循环但忘了接新的"这种静默失投）；
//	G4 唯一写点自己不许被绕开：CreateNotification 在 api 层的调用点只许出现在
//	   ①超管唯一写点内部 ②明确的两类非超管受众（提交者本人／租户管理员面）——
//	   新增第三种受众面必须在这里点名，否则判红。
//
// 每条负向腿都配**正对照**（把违规形态喂给同一个判定函数必须报出来）——
// 恒零命中的负向锁没有反证就是空转（AGENTS §三 那条）。
// =============================================
package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// stripGoCommentsForGate 剥掉 Go 源码注释（与前端跨语言锁同一口径：注释冒充实现＝假绿）。
func stripGoCommentsForGate(src string) string {
	src = regexp.MustCompile(`(?s)/\*[\s\S]*?\*/`).ReplaceAllString(src, "")
	var out []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		out = append(out, regexp.MustCompile(`//.*$`).ReplaceAllString(l, ""))
	}
	return strings.Join(out, "\n")
}

// apiNonTestSources 读全 api 包的非测试 .go（文件名→剥注释后的源码）。
func apiNonTestSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil //nolint:nilerr // 读不动的条目跳过：本闸扫的是现存源码，不是文件系统故障
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("读 %s 失败: %v", path, rerr)
		}
		out[filepath.ToSlash(path)] = stripGoCommentsForGate(string(b))
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 api 包失败: %v", err)
	}
	if len(out) < 50 { // 射程下限：扫到个位数文件＝路径写错，负向腿会集体假绿
		t.Fatalf("api 包非测试源码只扫到 %d 个文件，射程可疑（本包实测量级为 90+）", len(out))
	}
	return out
}

// findSuperAdminRoleLoops 找出"按角色字面量圈平台收件人"的写法（本闸的判据本体，正/负向都喂它）。
func findSuperAdminRoleLoops(sources map[string]string) []string {
	var bad []string
	for name, src := range sources {
		// G1：平台层（tid=0）按角色字面量圈人
		if strings.Contains(src, `ListUsersByRole(0,`) {
			bad = append(bad, name+": ListUsersByRole(0,…)")
		}
		// G2：SQL 里带角色名单圈人（ notifications 那类跨面复制的裸 SQL 形态）
		for _, re := range []*regexp.Regexp{
			regexp.MustCompile(`(?i)role\s+IN\s*\([^)]*super_admin[^)]*\)`),
			regexp.MustCompile(`(?i)role\s*=\s*'super_admin'`),
		} {
			if re.MatchString(src) {
				bad = append(bad, name+": SQL 按角色字面量圈人")
				break
			}
		}
	}
	return bad
}

// TestGateNoSecondSuperAdminRecipientLoop G1＋G2：api 层不许出现第二处按角色字面量圈平台收件人。
func TestGateNoSecondSuperAdminRecipientLoop(t *testing.T) {
	sources := apiNonTestSources(t)
	if bad := findSuperAdminRoleLoops(sources); len(bad) > 0 {
		t.Fatalf("发现 %d 处「按角色字面量圈平台收件人」的写法（收件人只许由 notify_recipients.go 的 "+
			"superAdminRecipients 一处圈定，口径与鉴权侧同源）：%v", len(bad), bad)
	}
	// ★ 正对照（反证）：同一函数喂真缺陷形态必须报出来，否则上面那条"零命中"只是空转
	cases := []struct {
		name string
		src  string
	}{
		{"迁移前的 pay.go 形态", "recipients := append(s.Store.ListUsersByRole(0, \"admin\"), s.Store.ListUsersByRole(0, \"super_admin\")...)\n"},
		{"迁移前的 packscraper 形态", "rows, err := db.Query(s.Store.DB(), d, \"SELECT id FROM users WHERE role IN ('super_admin','admin')\")\n"},
		{"迁移前的 feedback 形态", "for _, sa := range s.Store.ListUsersByRole(0, \"admin\") {\n"},
		{"单值等值写法", "_ = db.QueryRow(\"SELECT id FROM users WHERE role = 'super_admin'\")\n"},
	}
	for _, c := range cases {
		got := findSuperAdminRoleLoops(map[string]string{"counter/" + c.name: c.src})
		if len(got) == 0 {
			t.Errorf("反证 %q 未被判出 ⇒ G1/G2 的判据对该形态无射程（真缺陷会一起绿灯）", c.name)
		}
	}
	// 对照的对照：合规写法不许被判出（否则 G1 会把唯一写点自己也算成违规，逼出"给闸门开后门"的下一步）
	clean := map[string]string{
		"ok1": "users, err := s.Store.ListUsers(0)\nif !auth.IsSuperAdmin(u) { continue }\n",
		"ok2": "s.notifySuperAdmins(\"标题\", \"正文\", store.NotifyRefFeedback, id)\n",
		"ok3": "for _, u := range s.Store.ListUsersByRole(tid, \"tenant_admin\") {\n", // 租户内受众，不在本闸射程
	}
	if bad := findSuperAdminRoleLoops(clean); len(bad) > 0 {
		t.Errorf("合规写法被判红（判据过宽，迟早被加白名单绕过）：%v", bad)
	}
}

// TestGateMigratedCallSitesStillCallTheWriter G3：四个迁移点必须真的在调唯一写点。
func TestGateMigratedCallSitesStillCallTheWriter(t *testing.T) {
	sources := apiNonTestSources(t)
	want := map[string]string{
		"packscraper.go": "采集轮次完成",
		"pay.go":         "静态码/USDT 待人工确认",
		"feedback.go":    "新反馈＋提交者补充",
		"lead.go":        "销售线索",
	}
	for file, what := range want {
		src, ok := sources[file]
		if !ok {
			t.Fatalf("%s 不在扫描结果里（文件改名/移动会让本闸静默失去射程）", file)
		}
		if strings.Count(src, "s.notifySuperAdmins(") == 0 {
			t.Errorf("%s 不再调用唯一写点 notifySuperAdmins ⇒ %s 这一类通知要么失投、要么又长了第二套收件人", file, what)
		}
	}
	// feedback 面有两条（新反馈／补充回复），采集面有两条文案分支：数量下限按现读派生，不写死总数
	if n := strings.Count(sources["feedback.go"], "s.notifySuperAdmins("); n < 2 {
		t.Errorf("feedback.go 的 notifySuperAdmins 调用点 = %d，期望 ≥2（新建＋补充回复两支都在）", n)
	}
	if n := strings.Count(sources["packscraper.go"], "s.notifySuperAdmins("); n < 2 {
		t.Errorf("packscraper.go 的 notifySuperAdmins 调用点 = %d，期望 ≥2（有待审／无待审两支）", n)
	}
}

// TestGateCreateNotificationCallSitesNamed G4：api 层 CreateNotification 的调用点必须逐一点名。
// 新增一类受众（第三种"发给谁"）必须在这里登记并说明它为什么不在超管唯一写点里，
// 否则本闸红——目的是让"再加一条站内信"这件事在仓库里留下一处必须被人读过的痕迹。
func TestGateCreateNotificationCallSitesNamed(t *testing.T) {
	sources := apiNonTestSources(t)
	allowed := map[string]int{ // 文件名 → 该文件里允许的直接写点数
		"notify_recipients.go": 1, // 超管唯一写点自己
		"feedback.go":          1, // 超管回复 → 通知提交者本人（不是圈超管）
		"register.go":          1, // notifyTenantAdmins：租户管理员面
		"watchdog.go":          2, // 工单产物过期 → 工单创建者（ref_type=ticket）
	}
	hits := map[string]int{}
	for file, src := range sources {
		if n := strings.Count(src, "Store.CreateNotification("); n > 0 {
			hits[filepath.Base(file)] = n
		}
	}
	for file, n := range hits {
		if lim, ok := allowed[file]; !ok {
			t.Errorf("%s 出现 %d 处直接 CreateNotification 而未点名 ⇒ 新增受众面必须说明它为何不走唯一写点", file, n)
		} else if n > lim {
			t.Errorf("%s 直接 CreateNotification %d 处，超过登记上限 %d", file, n, lim)
		}
	}
	// 正对照：登记表里那几处必须真的在（登记漂了要红，否则闸门守的是一张过期名单）
	for file := range allowed {
		if _, ok := sources[file]; !ok {
			t.Errorf("登记表里的 %s 不存在（登记与代码已漂移，请同步这一份名单）", file)
		}
	}
}
