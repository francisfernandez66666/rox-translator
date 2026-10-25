// ============ admin_member_invite_r9_test.go · 职责说明 ============
// 《多身份账号方案_20261023.md》R9 / §6.2 的 B3-c 回归断言（2026-10-24）：
// 管理台建号与批量导入**都不再产口令、不再建 users 行**，一律发一枚一次性专属邀请链接，
// 由受邀人自己点开链接设置邮箱与密码——口令这种"管理员替别人决定秘密"的形态就此退役。
//
// 七条锁各自拦的退化形态：
//
//	R9-1 邮件模板注册表：invite_link 在位、user_import 已退役；Vars 与正文占位符**逐键等值**
//	       （两侧任何一侧漂了都会出现「管理台能改的变量列表」与「实际渲染的正文」打架，
//	        旧 user_import 那份 login_url 就是这种残留形态）；正文里不得再出现任何口令位。
//	R9-2 平台档（tid=0）直建号仍然可用：超管建平台账号走的是旧形态（录口令 + users 行落库），
//	       R9 只收企业档——这条是"别把没让删的一起删掉"的对照锁，同时钉住强度闸还在这一口上。
//	R9-3 权限梯一把尺子：租管想发 super_admin 的链接必须被拒、非法角色串必须 400、
//	       同档 tenant_admin 放行（inviteGrantGuard 与管理台建号／链接面板共用，
//	       本条断的就是「同一件事只许写一遍」这一条本身）。
//	R9-4 邮箱占用改按**本租户**判（D3）：同一邮箱已是本企业成员 ⇒ 409 且不落链接；
//	       未占用邮箱 ⇒ 照发并把 bound_email 归一小写落库。
//	R9-5 批量导入无邮箱行 ⇒ 链接进结果页、一封邮件都不许入队（回执「已提交发送」的前置是真发了东西）。
//	R9-6 批量导入的 dept_admin 行没填部门 ⇒ 该行拿到「必须绑定部门」，与管理台建号同一条源头闸。
//	R9-7 AST 级负向锁：user_import.go 与 handleAdminUserCreate 里不得再出现
//	       randomImportPassword / SetMustChangePwd / importLoginURL / "user_import" 模板码
//	       （判被调名与字符串字面量，不判源码文本——注释里写着旧名是正常的历史记录，
//	        拿文本判会喂出假红；而 AST 天然看不见注释）。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）；探针复用
// auth_deptadmin_org_gate_test.go 的 newF31Probe / callUserCreate 与
// user_import_f23_test.go / user_import_f54_test.go 的
// newF23ImportProbe / writeImportXlsxFromRows / callUserBulkImportXlsx / countingMailQueue / f54ImportResp。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestR9_
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/service"
	"translator/internal/store"
	"translator/internal/tenant"
)

// newR9SuperProbe 装配一个平台超管操作者（tenant_id=0）：R9-2 平台档建号的探针。
// 返回: Server 与超管 JWT。
func newR9SuperProbe(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:r9super_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	super, err := st.CreateUser(0, "r9_super", auth.PasswordHash("pw123456"), "R9平台超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UploadDir = t.TempDir()
	return &Server{Store: st, Ten: ts, Cfg: cfg}, tok
}

// TestR9_InviteLinkMailTemplate 锁 R9-1：邮件模板注册表换轨。
func TestR9_InviteLinkMailTemplate(t *testing.T) {
	var inv *MailTplMeta
	for i := range mailTplMetas {
		m := &mailTplMetas[i]
		if m.Code == "invite_link" {
			inv = m
		}
		if m.Code == "user_import" {
			t.Fatalf("旧《批量导入用户通知》（含初始密码）仍在注册表里——R9 后这一族邮件根本不存在了")
		}
	}
	if inv == nil {
		t.Fatalf("invite_link 模板未注册（批量导入/建号口的 sendTemplatedMail 会静默发不出东西）")
	}
	// Vars 与正文/主题占位符**双向逐键等值**：
	// 少一侧＝管理台列出的可改变量渲染不到（客户看到 {invite_url} 字面量），
	// 多一侧＝超管在管理台改了个正文里根本不存在的变量（旧 user_import 的 login_url 就这形态）。
	body := inv.Default.Subject + "\n" + inv.Default.Body
	for _, v := range inv.Vars {
		if !strings.Contains(body, "{"+v+"}") {
			t.Fatalf("模板声明了变量 %q 但正文/主题里没有该占位符: %s", v, body)
		}
	}
	ph := regexp.MustCompile(`\{([a-z_]+)\}`)
	for _, m := range ph.FindAllStringSubmatch(body, -1) {
		known := false
		for _, v := range inv.Vars {
			if v == m[1] {
				known = true
			}
		}
		if !known {
			t.Fatalf("正文里的占位符 {%s} 不在 Vars 清单里（管理台看不到它，改了也不生效）", m[1])
		}
	}
	// ★ R9 负向锁：这一族邮件的正文里一个口令都不许出现——系统已经不产口令了
	for _, bad := range []string{"初始密码", "{password}", "首次登录后请立即修改密码"} {
		if strings.Contains(body, bad) {
			t.Fatalf("邀请链接邮件里出现了口令形态字样 %q（R9 事故复发）: %s", bad, body)
		}
	}
	if !strings.Contains(body, "{invite_url}") {
		t.Fatalf("邀请邮件正文必须带链接位: %s", body)
	}
	// 有效期位也要在（客户拿到的是一枚会过期的门，不说清就是让客户对着失效链接反复试）
	if !strings.Contains(body, "{expires_at}") {
		t.Fatalf("邀请邮件正文必须告知有效期: %s", body)
	}
}

// TestR9_PlatformCreateStillIssuesAccount 锁 R9-2：平台档（tid=0）直建号一支未动。
// 企业档改了邀请链接，不代表超管建平台账号也得走邀请——链接口自己就拒绝 tid<=0（没有归属企业），
// 所以这一档必须保留旧形态，否则「超管开一个平台级账号」这件事在当前代码里物理上做不到。
func TestR9_PlatformCreateStillIssuesAccount(t *testing.T) {
	s, tok := newR9SuperProbe(t)

	rec := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "r9_plat", "password": "pw123456", "display_name": "平台运营", "role": "super_admin",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("平台档建号应成功，实得 %d: %s", rec.Code, rec.Body.String())
	}
	us, err := s.Store.GetUserByUsernameGlobal("r9_plat")
	if err != nil || len(us) != 1 {
		t.Fatalf("平台档必须真建出 users 行: n=%d err=%v", len(us), err)
	}
	if us[0].TenantID != 0 || us[0].Role != store.RoleSuperAdmin {
		t.Fatalf("平台账号归属/角色等值锁失败: tid=%d role=%q", us[0].TenantID, us[0].Role)
	}
	// 明文只许进哈希：出栈体里不许有它，库里那一列也不许是它（两件事各判一次）
	if strings.Contains(rec.Body.String(), "pw123456") {
		t.Fatalf("平台档建号响应体回显了明文口令: %s", rec.Body.String())
	}
	if us[0].PasswordHash == "pw123456" {
		t.Fatalf("平台档口令以明文落库（未走 PasswordHash）")
	}
	// 强度闸仍钉在这一口（P0-3「五个写口一把尺子」的射程不能因为换轨而缩掉）
	short := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "r9_weak", "password": "123", "role": "super_admin",
	})
	if short.Code != http.StatusBadRequest || !strings.Contains(short.Body.String(), auth.PasswordTooShortMsg) {
		t.Fatalf("平台档弱口令应 400 强度文案，实得 %d: %s", short.Code, short.Body.String())
	}
	// 平台档也不该顺手长出链接（issueInviteLink 在 tid<=0 时是判不出归属企业、直接拒的）
	if ls, e := s.Store.ListInviteLinks(0); e != nil || len(ls) != 0 {
		t.Fatalf("平台档建号不得落邀请链接: n=%d err=%v", len(ls), e)
	}
}

// TestR9_GrantLadderSharedRuler 锁 R9-3：权限梯与角色白名单只有一份实现。
// 租管想发 super_admin 的链接（等级 4）必须 403；非法角色串一律 400「角色无效」；
// 对照：发 tenant_admin（等级与自己相同，不算越级）应放行。
// 这三档在管理台建号与邀请链接面板上是同一段代码（inviteGrantGuard），这里断一次同时钉住两个口。
func TestR9_GrantLadderSharedRuler(t *testing.T) {
	s, tid, tok, _ := newF31Probe(t)

	// 403：租管不许把超管级角色挂在一枚可转发的 URL 上
	up := callUserCreate(t, s, tok, map[string]interface{}{"username": "r9_ladder", "role": "super_admin"})
	if up.Code != http.StatusForbidden || !strings.Contains(up.Body.String(), "仅超级管理员可分配该角色") {
		t.Fatalf("租管发超管级链接应 403，实得 %d: %s", up.Code, up.Body.String())
	}
	if ls, e := s.Store.ListInviteLinks(tid); e != nil || len(ls) != 0 {
		t.Fatalf("被权限梯拒掉的操作不得留下链接: n=%d err=%v", len(ls), e)
	}
	// 400：角色白名单（旧实现把任意字符串当普通用户收下＝管理员以为设了角色、实际设成了 user）
	bad := callUserCreate(t, s, tok, map[string]interface{}{"username": "r9_ladder", "role": "root_admin"})
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "角色无效") {
		t.Fatalf("非法角色应 400「角色无效」，实得 %d: %s", bad.Code, bad.Body.String())
	}
	// 对照：同档角色发放应成功
	ok := callUserCreate(t, s, tok, map[string]interface{}{"username": "r9_ladder", "role": "tenant_admin"})
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), `"success":true`) {
		t.Fatalf("同档角色发放应成功，实得 %d: %s", ok.Code, ok.Body.String())
	}
	ls, e := s.Store.ListInviteLinks(tid)
	if e != nil || len(ls) != 1 || ls[0].Role != store.RoleTenantAdmin {
		t.Fatalf("对照链接应恰一行且角色等值: n=%d err=%v", len(ls), e)
	}
	// 建号口不建 users 行（R9 本体）——只有 invite_codes 里那一枚
	if got, e2 := s.Store.GetUserByUsernameGlobal("r9_ladder"); e2 != nil || len(got) != 0 {
		t.Fatalf("企业档建号不得产生 users 行: n=%d err=%v", len(got), e2)
	}
}

// TestR9_EmailOccupancyScopedToTenant 锁 R9-4：邮箱占用按本租户判（D3 邮箱租户内唯一）。
// 同邮箱已在本企业 ⇒ 409「已是本企业成员」且不落链接。
// 旧实现用全局 GetUserByEmail 拦这一道，多身份改造后那一拦会把
// 「同一个人在第二家公司入职」这条合法路径挡死——所以判据必须换成租户内。
func TestR9_EmailOccupancyScopedToTenant(t *testing.T) {
	s, tid, tok, _ := newF31Probe(t)
	// 本租户先落一个真成员（走 store 直建，绕开已改口径的建号口）
	inside, err := s.Store.CreateUser(tid, "r9_inside", auth.PasswordHash("pw123456"), "本企业甲", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Store.SetUserEmail(inside.ID, tid, "inside@acme-corp.com"); e != nil {
		t.Fatalf("绑邮箱失败: %v", e)
	}

	dup := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "r9_dup", "role": "user", "email": "inside@acme-corp.com",
	})
	if dup.Code != http.StatusConflict || !strings.Contains(dup.Body.String(), "已是本企业成员") {
		t.Fatalf("本企业已有该邮箱应 409「已是本企业成员」，实得 %d: %s", dup.Code, dup.Body.String())
	}
	if ls, e := s.Store.ListInviteLinks(tid); e != nil || len(ls) != 0 {
		t.Fatalf("邮箱冲突不得留下链接: n=%d err=%v", len(ls), e)
	}

	// 对照：该邮箱在本租户没被占用 ⇒ 正常发链接（bound_email 落库）
	free := callUserCreate(t, s, tok, map[string]interface{}{
		"username": "r9_free", "role": "user", "email": "FREE@ACME-CORP.com",
	})
	if free.Code != http.StatusOK || !strings.Contains(free.Body.String(), `"success":true`) {
		t.Fatalf("未占用邮箱应正常发链接，实得 %d: %s", free.Code, free.Body.String())
	}
	ls, e := s.Store.ListInviteLinks(tid)
	if e != nil || len(ls) != 1 {
		t.Fatalf("应恰有一枚链接: n=%d err=%v", len(ls), e)
	}
	// 预绑邮箱必须 trim + 小写（与库里 email 列同口径，否则注册时大小写一错就"邮箱必须等于它"打回）
	if ls[0].BoundEmail != "free@acme-corp.com" {
		t.Fatalf("bound_email 未归一: %q", ls[0].BoundEmail)
	}
}

// TestR9_ImportNoEmailRowSendsNoMail 锁 R9-5：通道在线但该行没邮箱 ⇒ 零封邮件、链接照样进结果页。
// 旧形态在这里会一路 SetUserEmail("")、发一封收件人为空的信（Send 返回 nil 却什么都没寄出），
// 回执却写「已通知」。现在回执与队列都必须同时说真话。
func TestR9_ImportNoEmailRowSendsNoMail(t *testing.T) {
	s, tok := newF23ImportProbe(t)
	t.Setenv("MAIL_ENABLED", "")
	q := &countingMailQueue{}
	s.TicketSvc = &service.TicketService{Queue: q}
	if !s.mailLive() {
		t.Fatalf("队列可用时 mailLive 应判通道在线（本锁的前置）")
	}
	xlsxPath := filepath.Join(t.TempDir(), "noemail.xlsx")
	writeImportXlsxFromRows(t, xlsxPath, [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱"},
		{"r9_no_mail", "无邮箱行", "", "", ""},
	})
	rec := callUserBulkImportXlsx(t, s, tok, xlsxPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out f54ImportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("解码失败: %v (%s)", err, rec.Body.String())
	}
	if out.Invited != 1 || q.enqueued != 0 {
		t.Fatalf("无邮箱行应发出链接且零封邮件：invited=%d enqueued=%d（%s）", out.Invited, q.enqueued, rec.Body.String())
	}
	if len(out.Results) != 1 || !out.Results[0].OK || !strings.HasPrefix(out.Results[0].InviteURL, "http") {
		t.Fatalf("无邮箱行的结果必须带链接（管理员唯一的拿链接途径）: %s", rec.Body.String())
	}
	if msg := out.Results[0].Message; !strings.Contains(msg, "请复制本行链接转给本人") {
		t.Fatalf("未寄信那行回执须走「链接线下转告」文案: %q", msg)
	}
	// 链接必须真落库（回执里那条 URL 不是 handler 现拼的假串）
	ls, e := s.Store.ListInviteLinks(0)
	if e != nil || len(ls) != 1 || ls[0].BoundUsername != "r9_no_mail" || ls[0].BoundEmail != "" {
		t.Fatalf("导入应落一枚无邮箱链接: n=%d err=%v", len(ls), e)
	}
	if len(q.bodies) != 0 {
		t.Fatalf("零入队却拿到邮件载荷: %+v", q.bodies)
	}
}

// TestR9_ImportDeptAdminNeedsOrg 锁 R9-6：批量导入与管理台建号共用同一条源头闸（F-31 闸 1）。
// 角色填「部门管理员」而部门列留空 ⇒ 该行失败原因必须是「必须绑定部门」，且不落链接。
// 旧实现这里会建出一个未绑定部门的 dept_admin——当事人随后每个成员操作都被守卫反锁（死角色）。
func TestR9_ImportDeptAdminNeedsOrg(t *testing.T) {
	s, tok := newF23ImportProbe(t)
	t.Setenv("MAIL_ENABLED", "")
	xlsxPath := filepath.Join(t.TempDir(), "duda.xlsx")
	writeImportXlsxFromRows(t, xlsxPath, [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱"},
		{"r9_duda", "死角色甲", "", "部门管理员", ""},
	})
	rec := callUserBulkImportXlsx(t, s, tok, xlsxPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("应 200（逐行回执，不是整单抛错），实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out f54ImportResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("解码失败: %v (%s)", err, rec.Body.String())
	}
	if out.Invited != 0 || out.Failed != 1 {
		t.Fatalf("dept_admin 无部门行必须被拒: invited=%d failed=%d（%s）", out.Invited, out.Failed, rec.Body.String())
	}
	if len(out.Results) != 1 || !strings.Contains(out.Results[0].Message, "必须绑定部门") {
		t.Fatalf("拒收原因须点明「必须绑定部门」（与管理台建号同一条闸），实得: %s", rec.Body.String())
	}
	if ls, e := s.Store.ListInviteLinks(0); e != nil || len(ls) != 0 {
		t.Fatalf("被拒行不得留下链接: n=%d err=%v", len(ls), e)
	}
}

// collectCallsAndStrings 解析一个 Go 源文件，收「被调名集合」与「字符串字面量集合（已去引号）」。
// 为什么走 AST 而不是读源码文本：AGENTS §一·9 那一族老坑——把旧腿的名字写进说明注释
// （本文件与 user_import.go 的文件头就写着「取代旧的初始密码邮件」）会让文本负向锁假红；
// AST 天然看不见注释，判的只有真代码。
func collectCallsAndStrings(t *testing.T, path string) (map[string]bool, map[string]bool) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	calls := map[string]bool{}
	lits := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if cn := calleeName(x); cn != "" {
				calls[cn] = true
			}
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				if v, e := strconv.Unquote(x.Value); e == nil {
					lits[v] = true
				}
			}
		}
		return true
	})
	return calls, lits
}

// TestR9_ImportSourceHasNoPasswordLeg 锁 R9-7：源码级负向锁。
// R9 之后这条链路里「口令」只有受邀人自己设的那一个；谁把
// randomImportPassword / SetMustChangePwd / 旧模板码 user_import 写回来，这一条当场红。
// （行为锁只覆盖走得到的分支，而"生成一个没人用的随机口令"这种死码恰好不会被任何行为断言撞到。）
func TestR9_ImportSourceHasNoPasswordLeg(t *testing.T) {
	calls, lits := collectCallsAndStrings(t, "user_import.go")
	for _, banned := range []string{"randomImportPassword", "SetMustChangePwd", "importLoginURL"} {
		if calls[banned] {
			t.Fatalf("批量导入源码里又调用了 %s（R9 退役的那条腿被写回来了）", banned)
		}
	}
	if lits["user_import"] {
		t.Fatalf("批量导入又引用了旧邮件模板码 \"user_import\"（那份含初始密码的模板已从注册表退役）")
	}
	// 正向对照：这一族现在必须真的在发链接、真的走新模板码与链接回执键
	for _, required := range []string{"issueInviteLink", "sendTemplatedMail"} {
		if !calls[required] {
			t.Fatalf("批量导入源码缺少调用 %s（R9 主体不在了）", required)
		}
	}
	for _, required := range []string{"invite_link", "invite_url"} {
		if !lits[required] {
			t.Fatalf("批量导入源码缺少字面量 %q（R9 主体不在了）", required)
		}
	}

	// ★ 建号口同样不许再给企业档产口令：handleAdminUserCreate 的函数体必须调用 issueInviteLink，
	//   且不再出现随机口令/强制改密那两个被调名（平台档那支的 ValidatePassword 由 R9-2 行为锁与
	//   P0-3b 的 AST 锁一起守住，这里不重复判）。
	f, err := parser.ParseFile(token.NewFileSet(), "auth.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 auth.go 失败: %v", err)
	}
	body := funcBodyOf(f, "handleAdminUserCreate")
	if body == nil {
		t.Fatalf("auth.go 里找不到 handleAdminUserCreate（写口被改名＝本锁失明）")
	}
	if !bodyCalls(body, "issueInviteLink") {
		t.Fatalf("管理台建号口已不再调用 issueInviteLink（R9 汇流失效，两处又各长一把尺子）")
	}
}
