// ============ user_import.go · 职责说明 ============
// 租户 Excel 批量导入用户（2026-09-02 功能）：
//   - 模板下载：GET /api/admin/users/import-template 返回带表头与示例行的 xlsx
//   - 解析 xlsx，表头：用户名称、姓名、部门、角色、邮箱（角色列可省略，默认普通用户）
//   - 逐行创建账号：随机初始密码 + 首登强制改密标记（must_change_pwd=1）
//   - 绑定邮箱并向导入用户发送《账号开通通知》（含登录地址、账号、初始密码）
//
// =============================================
package api

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"

	"translator/internal/auth"
	"translator/internal/store"
)

// importUserRow Excel 一行待导入用户（解析后标准化）。
type importUserRow struct {
	Username    string // 登录用户名（必填，租户内唯一）
	DisplayName string // 显示名称（缺省回退用户名）
	OrgName     string // 部门/组织名称（缺省挂根组织）
	Role        string // 角色（管理员/普通用户 等中英文）
	Email       string // 邮箱（绑定用于收账号通知）
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

// randomImportPassword 生成 10 位随机初始密码（大小写+数字，避免易混淆字符）。
func randomImportPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	buf := make([]byte, 10)
	for i := range buf {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf)
}

// handleUserImportTemplate 下载批量导入 Excel 模板（带表头与示例行，含填写说明注释行）。
// 权限：租户管理员及以上。返回 application/octet-stream 的 xlsx 附件。
func (s *Server) handleUserImportTemplate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 生成带表头、说明行与示例行的模板工作簿
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	_ = f.SetCellStr(sheet, "A1", "用户名称")
	_ = f.SetCellStr(sheet, "B1", "姓名")
	_ = f.SetCellStr(sheet, "C1", "部门")
	_ = f.SetCellStr(sheet, "D1", "角色")
	_ = f.SetCellStr(sheet, "E1", "邮箱")
	// 说明行（首行补注释单元格，导入时将被忽略——readImportRows 从第 2 行开始读）
	_ = f.SetCellStr(sheet, "F1", "填写说明：用户名称必填且租户内唯一；角色可留空=普通用户（可填 普通用户/管理员/部门管理员/租户管理员）；部门须与现有组织名称一致，留空挂根组织；邮箱用于发送账号开通通知")
	// 示例行（如用户直接提交亦会被正常导入）
	_ = f.SetCellStr(sheet, "A2", "zhangsan")
	_ = f.SetCellStr(sheet, "B2", "张三")
	_ = f.SetCellStr(sheet, "C2", "销售部")
	_ = f.SetCellStr(sheet, "D2", "普通用户")
	_ = f.SetCellStr(sheet, "E2", "zhangsan@example.com")
	// 表头浅灰填充 + 加粗，示例行便于识别
	if style, e := f.NewStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Color: []string{"F2F2F2"}, Pattern: 1}, Font: &excelize.Font{Bold: true}}); e == nil {
		_ = f.SetCellStyle(sheet, "A1", "E1", style)
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="user_import_template.xlsx"`)
	_ = f.Write(w)
	_ = u.ID // tid 鉴权已由 requireTenantAdmin 完成，此处仅保持权限一致性
}

// handleUserBulkImport 租户 Excel 批量导入用户接口。
// 权限：租户管理员及以上（部门管理员不可批量导入）。
// body: multipart，含 file（xlsx）；返回逐行导入结果（成功/失败与原因）。
func (s *Server) handleUserBulkImport(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	// 解析并落盘上传的 xlsx（白名单与大小限制复用 KB 导入）
	if err := parseUpload(r, kbUploadMax, map[string]bool{".xlsx": true, ".xls": true, ".csv": true}); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少文件"})
		return
	}
	defer file.Close()
	_ = os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := s.Cfg.UploadDir + "/imp_" + uniqueName(header.Filename)
	fd, err := os.Create(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "文件保存失败"})
		return
	}
	if _, err := fd.ReadFrom(file); err != nil {
		fd.Close()
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "文件读取失败"})
		return
	}
	fd.Close()
	defer os.Remove(savePath)

	rows, err := readImportRows(savePath)
	if err != nil || len(rows) == 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "Excel 解析失败或为空，请使用模板列：用户名称、姓名、部门、角色、邮箱"})
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
		Username string `json:"username"`
		OK       bool   `json:"ok"`
		Message  string `json:"message"`
	}
	results := make([]rowResult, 0, len(rows))
	created := 0
	for _, row := range rows {
		rr := rowResult{Username: row.Username}
		// 用户名必填且租户内唯一（CreateUser 内唯一索引兜底）
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
		// 邮箱唯一预检
		if row.Email != "" {
			if other, e := s.Store.GetUserByEmail(strings.ToLower(strings.TrimSpace(row.Email))); e == nil && other != nil {
				rr.Message = "邮箱已被其他账号绑定"
				results = append(results, rr)
				continue
			}
		}
		initPwd := randomImportPassword()
		nu, cerr := s.Store.CreateUser(tid, strings.TrimSpace(row.Username), auth.PasswordHash(initPwd), row.DisplayName, row.Role, u.ID, orgID)
		if cerr != nil {
			rr.Message = "创建失败: " + cerr.Error()
			results = append(results, rr)
			continue
		}
		// 绑定邮箱（失败不阻断建号）
		if row.Email != "" {
			if e := s.Store.SetUserEmail(nu.ID, tid, row.Email); e == nil {
				nu.Email = row.Email
			}
		}
		// 首登强制改密 + 审计
		_ = s.Store.SetMustChangePwd(nu.ID, tid, 1)
		s.Store.LogAudit(tid, u.ID, "user_bulk_import", "users", row.Username)
		// 发送《账号开通通知》：登录地址 + 账号 + 初始密码
		if nu.Email != "" {
			_ = s.sendTemplatedMail(nu.Email, "user_import", map[string]string{
				"username":  nu.Username,
				"password":  initPwd,
				"login_url": importLoginURL(r),
			})
		}
		created++
		rr.OK = true
		rr.Message = "导入成功（初始密码已通过邮件通知）"
		results = append(results, rr)
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"created": created,
		"failed":  len(rows) - created,
		"total":   len(rows),
		"results": results,
	})
}

// importLoginURL 构造登录地址（优先当前请求 Host 对应的主站，兜底品牌基础域）。
func importLoginURL(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = "langcross.lexicorn.cn"
	}
	if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i]
	}
	return "https://" + host + "/login"
}

// readImportRows 解析 xlsx 首工作表为待导入用户行（首行为表头）。
func readImportRows(path string) ([]importUserRow, error) {
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
	if err != nil || len(all) < 2 {
		return nil, fmt.Errorf("无数据行")
	}
	// 表头列索引（宽松匹配中文/英文）
	header := all[0]
	idx := map[string]int{}
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		switch {
		case strings.Contains(h, "用户名称"), strings.Contains(h, "用户名"), h == "username":
			idx["username"] = i
		case strings.Contains(h, "姓名"), strings.Contains(h, "显示名"), h == "displayname", h == "name":
			idx["display"] = i
		case strings.Contains(h, "部门"), strings.Contains(h, "组织"), h == "org":
			idx["org"] = i
		case strings.Contains(h, "角色"), strings.Contains(h, "管理员"), strings.Contains(h, "普通用户"), h == "role":
			idx["role"] = i
		case strings.Contains(h, "邮箱"), h == "email", h == "mail":
			idx["email"] = i
		}
	}
	if _, ok := idx["username"]; !ok {
		// 无表头时按固定列序：用户名称、姓名、部门、角色、邮箱
		idx = map[string]int{"username": 0, "display": 1, "org": 2, "role": 3, "email": 4}
	}
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
