// ============ user_import.go · 职责说明 ============
// 租户 Excel/CSV 批量导入成员（2026-09-02 功能；★ 2026-09-25 F-23 批F 收口；
// ★ 2026-10-24 多身份方案 R9／B3-c 改造为「批量发邀请链接」）：
//   - 模板下载：GET /api/admin/users/import-template 返回带表头与示例行的 xlsx
//     （填写说明挂示例行 F2，不再污染表头行）
//   - 解析 xlsx/xls/csv（★ F-23③：CSV 走 encoding/csv，与 Excel 共用
//     buildImportRows 纯函数，表头列索引首中即停防长句劫持），
//     表头：用户名称、姓名、部门、角色、邮箱（角色列可省略，默认普通用户）
//   - ★★ R9：逐行**不再创建账号、不再随机初始密码、不再打首登强制改密标记**。
//     每一行走 issueInviteLink 发一枚一次性专属邀请链接（与管理台建号同一把尺子）：
//     有邮箱 ⇒ 走《企业邀请链接通知》邮件（模板 code=invite_link，正文里只有链接、没有任何口令）；
//     无邮箱 ⇒ 链接进该行回执（results[].invite_url），由管理员自行转给本人。
//     旧形态三段都在转手凭据：管理员/系统设一个口令 → 口令进邮件正文 → 当事人首登被迫改一次。
//     现在口令只由本人在他自己点开的页面上产生，管理员全程接触不到。
//   - 回执按「邀请邮件是否真的寄出」分两版中文文案（★ F-23④，不向管理员虚假承诺），
//     寄出版只说「已提交发送，稍后送达」（入队≠送达，★ F-54）
//   - ★ F-54：邮箱列过 importEmailRejection 两道闸（格式＋RFC 2606 示例保留域），
//     模板示例行落在 example.invalid ⇒「下载模板不改一字上传」不会再发出任何邀请
//
// =============================================
package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// importUserRow Excel 一行待导入用户（解析后标准化）。
type importUserRow struct {
	Username    string // 登录用户名（必填；展示/检索用，同租户可重名）
	DisplayName string // 显示名称（缺省回退用户名）
	OrgName     string // 部门/组织名称（缺省挂根组织）
	Role        string // 角色（管理员/普通用户 等中英文）
	Email       string // 邮箱（★ R9：作为链接的预绑邮箱，并决定邀请发到哪个收件箱）
}

// normalizeImportRole 将 Excel 角色列归一为系统角色常量。
func normalizeImportRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "管理员", "部门管理员", "dept_admin", "admin":
		return store.RoleDeptAdmin
	case "租户管理员", "tenant_admin":
		return store.RoleTenantAdmin
	default:
		return store.RoleUser
	}
}

// handleUserImportTemplate 下载批量导入 Excel 模板（带表头与示例行，含填写说明）。
// 权限：租户管理员及以上。返回 application/octet-stream 的 xlsx 附件。
// 模板本体在纯函数 buildUserImportTemplate 中构建（★ F-23：模板即回归资产，
// 单测直接喂给 readImportRows 断示例行能被本解析器读回）。
func (s *Server) handleUserImportTemplate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	f := buildUserImportTemplate()
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", contentDisposition("user_import_template.xlsx"))
	_ = f.Write(w)
	_ = u.ID // tid 鉴权已由 requireTenantAdmin 完成，此处仅保持权限一致性
}

// buildUserImportTemplate 构建导入模板工作簿（表头 + 示例行 + 填写说明）。
// ★ F-23②：填写说明从表头行 F1 挪到示例行 F2——说明长句里含「用户名称/管理员」等
// 关键词，挂在表头行会参与列名匹配（配合 buildImportRows 的首中即停构成双保险）。
func buildUserImportTemplate() *excelize.File {
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	_ = f.SetCellStr(sheet, "A1", "用户名称")
	_ = f.SetCellStr(sheet, "B1", "姓名")
	_ = f.SetCellStr(sheet, "C1", "部门")
	_ = f.SetCellStr(sheet, "D1", "角色")
	_ = f.SetCellStr(sheet, "E1", "邮箱")
	// 示例行（★ F-54 批 I-6 2026-09-26：不再是「照抄也能建号」的真投递形态）
	//   旧形态 A2=zhangsan / E2=zhangsan@example.com ＋ 注释「如用户直接提交亦会被正常导入」，
	//   于是「下载模板 → 不改一字上传」＝ 真建一个 active 账号 ＋ 真发一封含初始密码的邮件
	//   （现网 email_notify_enabled=1）——本轮 E2E 实跑就在生产多建出一个 zhangsan（见 UAT 第八节处置清单）。
	//   现改为：用户名/姓名写成明显占位，邮箱落 RFC 2606 保留域 example.invalid（永不投递），
	//   并由 importEmailRejection 在导入时显式拒绝保留域 ⇒ 原样上传只会得到一行失败回执。
	_ = f.SetCellStr(sheet, "A2", "示例行请替换")
	_ = f.SetCellStr(sheet, "B2", "示例姓名请替换")
	_ = f.SetCellStr(sheet, "C2", "")
	_ = f.SetCellStr(sheet, "D2", "普通用户")
	_ = f.SetCellStr(sheet, "E2", "sample@example.invalid")
	// 填写说明（★ F-23② 挪出表头行，挂示例行 F2；解析器只认 A-E 五列，不会读到它）
	_ = f.SetCellStr(sheet, "F2", "填写说明：第 2 行是示例行，提交前请删除或整行替换为真实信息；用户名称必填（同租户可重名，邮箱是登录标识）；角色可留空=普通用户（可填 普通用户/管理员/部门管理员/租户管理员）；部门须与现有组织名称一致，留空挂根组织；邮箱用于发送企业邀请链接通知，必须是可以收信的真实邮箱（example.com／*.invalid 等示例保留域会被拒收）")
	// 表头浅灰填充 + 加粗，示例行便于识别
	if style, e := f.NewStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Color: []string{"F2F2F2"}, Pattern: 1}, Font: &excelize.Font{Bold: true}}); e == nil {
		_ = f.SetCellStyle(sheet, "A1", "E1", style)
	}
	return f
}

// handleUserBulkImport 租户 Excel/CSV 批量导入成员接口（★ R9：批量发邀请链接，不建号）。
// 权限：租户管理员及以上（部门管理员不可批量导入）。
// body: multipart，含 file（xlsx/xls/csv）；返回逐行结果（成功/失败与原因、成功行的邀请链接）。
//
// 三条 B3-c 口径：
//  1. 必须落在具体企业（tid<=0 一律 400）——平台上下文没有预算墙也没有归属企业，
//     「批量导入」这个动作的语义就是给一家企业添人；issueInviteLink 里也有一道同判据，
//     这里提前收口是为了给管理员一句「批量导入只用于企业成员」而不是逐行失败。
//  2. 一行一枚链接，走 issueInviteLink（与管理台建号同一把尺子：角色白名单、权限梯、
//     部门归属、随机码失败口径全部共用）。
//  3. 邮箱占用按**本租户**判（D3 邮箱租户内唯一）：旧形态用全局 GetUserByEmail 拦一道，
//     多身份改造后那一拦会挡死「同一个人在第二家企业入职」这条新模型明确要放开的合法路径。
func (s *Server) handleUserBulkImport(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
		return
	}
	tid := s.effTenant(r, u)
	if tid <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "批量导入只用于企业成员：请先切换到具体企业再上传文件"))
		return
	}
	// 解析并落盘上传的 xlsx（白名单与大小限制复用 KB 导入）
	if err := parseUpload(r, kbUploadMax, map[string]bool{".xlsx": true, ".xls": true, ".csv": true}); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, publicErrMessage(r.Context(), err)))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少文件"))
		return
	}
	defer file.Close()
	_ = os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := s.Cfg.UploadDir + "/imp_" + uniqueName(header.Filename)
	fd, err := os.Create(savePath)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "文件保存失败"))
		return
	}
	if _, err := fd.ReadFrom(file); err != nil {
		fd.Close()
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "文件读取失败"))
		return
	}
	fd.Close()
	defer os.Remove(savePath)

	// 解析导入表为行模型（表头可缺省，按模板固定列序兜底）
	rows, err := readImportRows(savePath)
	if err != nil || len(rows) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "Excel 解析失败或为空，请使用模板列：用户名称、姓名、部门、角色、邮箱"))
		return
	}
	// 组织名 → ID 映射（用于按名称挂部门）
	orgByName := map[string]int64{}
	if orgs, e := s.Store.ListOrgs(tid); e == nil {
		for _, o := range orgs {
			orgByName[strings.TrimSpace(o.Name)] = o.ID
		}
	}

	type rowResult struct {
		Username  string `json:"username"`
		OK        bool   `json:"ok"`
		InviteURL string `json:"invite_url,omitempty"` // ★ R9：成功行的专属邀请链接（无邮箱时管理员只能从这里拿链接）
		Message   string `json:"message"`
	}
	results := make([]rowResult, 0, len(rows))
	invited := 0
	for _, row := range rows {
		rr := rowResult{Username: row.Username}
		// 用户名必填（同租户可重名；链接的 bound_username 就是受邀人注册页的预填名）
		if strings.TrimSpace(row.Username) == "" {
			rr.Message = "用户名称不能为空"
			results = append(results, rr)
			continue
		}
		// 按名称解析部门（未命中挂根组织，不报错）
		orgID := int64(0)
		if row.OrgName != "" {
			if id, ok := orgByName[strings.TrimSpace(row.OrgName)]; ok {
				orgID = id
			}
		}
		// ★ F-54（批 I-6）：邮箱格式与示例保留域闸——放在发链接**之前**，
		// 旧实现任何字符串都照收，模板示例行（example.com）因此能被原样上传并真发一封含初始密码的邮件。
		if reason := importEmailRejection(row.Email); reason != "" {
			rr.Message = reason
			results = append(results, rr)
			continue
		}
		// 邮箱占用按本租户判：同一邮箱在本企业已是成员 ⇒ 这枚链接注册时必然撞「你已在该企业中」，
		// 在源头就拒掉，管理员看到的是「已经是成员」而不是「邀请发了却进不来」。
		if row.Email != "" {
			if other, e := s.Store.GetUserByEmailInTenant(row.Email, tid); e == nil && other != nil {
				rr.Message = "该邮箱已是本企业成员，无需重复邀请"
				results = append(results, rr)
				continue
			}
		}
		// ★ 收口（2026-09-16 安全整改）：批量导入禁止创建 tenant_id>0 的超管级角色
		//   （admin/super_admin 必须平台级归属，与 users/create 不变量对齐）。
		if auth.RoleLevel(row.Role) >= 4 {
			rr.Message = "超管级角色不能归属具体租户，请在本租户内使用 dept_admin/tenant_admin 等角色"
			results = append(results, rr)
			continue
		}
		// ★ R9：一行一枚邀请链接（不建 users 行、不生成任何口令）
		link, url, apiErr := s.issueInviteLink(r, u, tid, inviteLinkSpec{
			Username: row.Username, Role: row.Role, OrgID: orgID, Email: row.Email,
		})
		if apiErr != nil {
			rr.Message = publicErrMessage(r.Context(), apiErr)
			results = append(results, rr)
			continue
		}
		// 发送《企业邀请链接通知》：正文只有链接与有效期，没有任何口令
		// ★ F-23④+：先经 mailLive() 判通道真实可用再承诺「已寄出」——Noop 兜底
		//   （MAIL_ENABLED≠1 且队列不可用）下 Send 返回 nil 但邮件永远不会出门，
		//   以「Send 无错」为成功口径同样是虚假承诺。
		mailSent := false
		if row.Email != "" && s.mailLive() {
			// ★ F-17（批E）：导入成员未经历过注册界面选语种（preferred_lang 恒空），语种传空=中文链路
			mailSent = s.sendTemplatedMail(row.Email, "invite_link", "", map[string]string{
				"username":   strings.TrimSpace(row.Username),
				"invite_url": url,
				"expires_at": inviteExpiryLabel(link.ExpiresAt),
			}) == nil
		}
		invited++
		rr.OK = true
		rr.InviteURL = url
		rr.Message = importSuccessMessage(mailSent)
		results = append(results, rr)
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		// ★ R9 改名：这一档数的不再是「建了几个号」（一个都没建），是「发出了几枚邀请链接」。
		//   留着 created 这个名字会让下一个读代码的人以为 users 表真多了几行。
		"invited": invited,
		"failed":  len(rows) - invited,
		"total":   len(rows),
		"results": results,
	})
}

// importEmailRejection 校验导入行的邮箱是否可用（★ F-54 批 I-6）。
// 返回非空字符串＝该行应被拒绝并把这个原因回给管理员；返回 ""＝放行（含「邮箱留空」——
// 留空是合法形态：链接照样生成、只是不发信，由管理员把链接线下转给本人）。
//
// 两道闸的由来：
//  1. 格式闸：复用注册/换绑同一份 emailRe（口径不分叉）。旧实现把任何字符串都当邮箱收下，
//     「zhangsan@examplecom」「abc@@x.com」这类会一路 SetUserEmail 成功，然后发信静默失败——
//     账号建了、通知没到，管理员却看不到任何异常。
//  2. 保留域闸：RFC 2606 的 example.com／example.net／example.org／example.edu、任意 .invalid、
//     以及 .test 与 localhost 都是**永不投递**的示例域。模板示例行就落在 example.invalid 上，
//     于是「下载模板不改一字直接上传」不再会建出真账号（本轮实跑在生产多建出一个 zhangsan），
//     而是拿到一行明确的「示例保留域」失败回执。
//
// 域名比较用整段匹配而非前缀，避免把 myexample.com 这种真实域误杀。
func importEmailRejection(email string) string {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return "" // 留空＝不绑邮箱，合法
	}
	if !emailRe.MatchString(e) {
		return "邮箱格式不合法（应形如 name@domain.com），请修正后重试；留空表示不发送开通邮件"
	}
	at := strings.LastIndex(e, "@")
	domain := e[at+1:]
	switch domain {
	case "example.com", "example.net", "example.org", "example.edu", "example":
		return "邮箱是示例保留域（" + domain + "），邮件永远不会送达，请替换为真实邮箱或删除示例行"
	}
	if strings.HasSuffix(domain, ".invalid") || strings.HasSuffix(domain, ".test") ||
		domain == "localhost" || strings.HasSuffix(domain, ".localhost") {
		return "邮箱是示例/保留域（" + domain + "），邮件永远不会送达，请替换为真实邮箱或删除示例行"
	}
	return ""
}

// mailLive 判断邮件通道是否真实可用（★ F-23④，与 enqueueMail/mail.NewSender 的兜底口径对齐）：
// 工单队列可用（异步入队投递）或 SMTP 配置齐全（同步投递）才算活；
// NoopSender（MAIL_ENABLED≠1 且无队列）的 Send 恒返回 nil 却永不外发，不得计入可用。
func (s *Server) mailLive() bool {
	if s.TicketSvc != nil && s.TicketSvc.Queue != nil {
		return true
	}
	return os.Getenv("MAIL_ENABLED") == "1" && os.Getenv("SMTP_HOST") != "" && os.Getenv("SMTP_USER") != ""
}

// importSuccessMessage 导入行成功回执（★ F-23④：两版中文文案按「通知邮件是否真的寄出」分支，
// 无邮箱/通道未配置/发送失败一律走线下转告版，杜绝「已通过邮件通知」的无条件虚假承诺）。
// ★ F-54（批 I-6）已寄出版再收一档口径：mailLive() 为真且 sendTemplatedMail 返回 nil，
// 只证明「已受理/已入队」（enqueueMail 入队即 return nil），真正投递要等 worker、还可能永久滞留，
// 所以文案不许写成完成的「已通过邮件通知」，改为「已提交发送，稍后送达」＋未收到的兜底指引。
// （与记忆里那条纪律一致：发码 success ≠ 投递。）
// ★ R9（2026-10-24 B3-c）文案主体换轨：线下转告的内容从「初始密码」变成「邀请链接」——
//
//	系统已经不产任何口令了，回执再写「把初始密码线下转告」就是承诺一件根本不存在的东西。
func importSuccessMessage(mailSent bool) string {
	if mailSent {
		return "邀请链接已提交发送（稍后送达；若长时间未收到，可复制本行链接转给本人）"
	}
	return "邀请链接已生成（未绑定邮箱或邮件发送失败）：请复制本行链接转给本人，他点开即可自行设置邮箱与密码"
}

// inviteExpiryLabel 把链接到期时间（RFC3339 UTC）折成邮件里给人看的日期。
// 解析失败就原样返回：宁可让客户看到一串带时分秒的 UTC，也不要在邮件渲染处编一个日期。
func inviteExpiryLabel(expiresAt string) string {
	v := strings.TrimSpace(expiresAt)
	if v == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.Format("2006-01-02")
	}
	return v
}

// readImportRows 按扩展名分发解析导入表（★ F-23③：白名单里有 .csv，解析就必须真支持 csv，
// 否则上传 CSV 一律被 excelize 打回「Excel 解析失败」假象）。
// .csv 走标准库 encoding/csv（RFC 4180），其余（xlsx/xls）走 excelize 首工作表；
// 两路统一产出 [][]string 交给纯函数 buildImportRows 建模，口径不再分叉。
func readImportRows(path string) ([]importUserRow, error) {
	if strings.EqualFold(filepath.Ext(path), ".csv") {
		return readCSVImportRows(path)
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	if sheet == "" {
		return nil, fmt.Errorf("空工作簿")
	}
	all, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	return buildImportRows(all)
}

// readCSVImportRows 解析 CSV 导入表（列数不限、行间允许不齐，交由 buildImportRows 统一判空）。
func readCSVImportRows(path string) ([]importUserRow, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	cr := csv.NewReader(fd)
	cr.FieldsPerRecord = -1 // 允许稀疏行（模板说明列只在一行出现等场景不炸整单）
	all, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	return buildImportRows(all)
}

// buildImportRows 把 [][]string（首行表头）转成待导入用户行。
// 表头列索引宽松匹配中文/英文列名；★ F-23①：每个键**首中即停**——
// 旧实现后中覆写，表头行里任何含关键词的长句（如旧版 F1 填写说明）会把五列索引
// 一路劫持到说明所在列（username 被指到说明列），整单导入静默错位。
// 无「用户名称」表头时按模板固定列序兜底：用户名称、姓名、部门、角色、邮箱。
func buildImportRows(all [][]string) ([]importUserRow, error) {
	if len(all) < 2 {
		return nil, fmt.Errorf("无数据行")
	}
	header := all[0]
	idx := map[string]int{}
	setIfFirst := func(key string, i int) {
		if _, ok := idx[key]; !ok {
			idx[key] = i
		}
	}
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case strings.Contains(h, "用户名称"), strings.Contains(h, "用户名"), h == "username":
			setIfFirst("username", i)
		case strings.Contains(h, "姓名"), strings.Contains(h, "显示名"), h == "displayname", h == "name":
			setIfFirst("display", i)
		case strings.Contains(h, "部门"), strings.Contains(h, "组织"), h == "org":
			setIfFirst("org", i)
		case strings.Contains(h, "角色"), strings.Contains(h, "管理员"), strings.Contains(h, "普通用户"), h == "role":
			setIfFirst("role", i)
		case strings.Contains(h, "邮箱"), h == "email", h == "mail":
			setIfFirst("email", i)
		}
	}
	if _, ok := idx["username"]; !ok {
		// 无表头时按固定列序：用户名称、姓名、部门、角色、邮箱
		idx = map[string]int{"username": 0, "display": 1, "org": 2, "role": 3, "email": 4}
	}
	// 逐行按列索引提取：用户名必填、空姓名回退用户名、角色归一化、邮箱统一小写
	rows := make([]importUserRow, 0, len(all)-1)
	for i := 1; i < len(all); i++ {
		line := all[i]
		username := strings.TrimSpace(cell(line, idx["username"]))
		if username == "" {
			continue
		}
		display := strings.TrimSpace(cell(line, idx["display"]))
		if display == "" {
			display = username
		}
		rows = append(rows, importUserRow{
			Username:    username,
			DisplayName: display,
			OrgName:     strings.TrimSpace(cell(line, idx["org"])),
			Role:        normalizeImportRole(cell(line, idx["role"])),
			Email:       strings.ToLower(strings.TrimSpace(cell(line, idx["email"]))),
		})
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("无有效数据行")
	}
	return rows, nil
}
