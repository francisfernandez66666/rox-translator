// ============ user_import_f23_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 F 的 F-23 回归断言（user_import.go 四改）：
//
//	① 表头列索引「首中即停」——旧实现后中覆写，表头行里的长句（旧模板 F1 填写说明，
//	   句中含「用户名称/管理员/邮箱」等关键词）会把五列索引一路劫持到说明列，
//	   username 被指到说明列后整单静默错位/报废；
//	② 说明挪出表头行（模板 F1 → F2），与①构成双保险；
//	③ CSV 真支持：白名单里有 .csv，旧解析只走 excelize——上传 CSV 必被假装成
//	   「Excel 解析失败」；现 CSV 走 encoding/csv，与 xlsx 共用 buildImportRows 纯函数；
//	④ 回执诚实：importSuccessMessage 按「通知邮件是否真的寄出」分两版中文文案，
//	   mailLive() 把 Noop 兜底（Send 返回 nil 但永不外发）判为通道不可用。
//
// 锁清单：
//
//	A) 模板即回归资产：buildUserImportTemplate 产物直接喂 readImportRows，示例行必须活着
//	   且逐字段等值（username/姓名/部门/角色/邮箱），说明列不得参与建模；
//	B) 说明劫持负向锁：构造「旧版说明挂 F1 表头行」的工作簿行，断首中即停后列索引不跑偏
//	   （旧 last-wins 实现在此必红：username 被劫持到 F 列 → 无有效数据行）；
//	C) CSV 行数等值 + 角色归一 + 邮箱小写：CSV 与 xlsx 两路对同一份数据建模结果全等；
//	D) 回执两版文案 + mailLive 三态（无队列无 SMTP=假 / MAIL_ENABLED+HOST+USER 齐=真）；
//	E) handler 全链：multipart 上传 xlsx 两行（一行带邮箱一行不带），离线态（Noop 通道）
//	   两行回执都必须落「线下转告」版，created=2（旧实现在此会虚假承诺「已邮件通知」）。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestUATBatchF
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/xuri/excelize/v2"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// writeImportXlsxFromRows 把 [][]string 落成 xlsx 文件（F-23 负向锁的构造器：
// 需要手工摆出「说明挂表头行」等旧模板形态，不能复用新模板）。
func writeImportXlsxFromRows(t *testing.T, path string, all [][]string) {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, row := range all {
		cells := make([]interface{}, len(row))
		for j, c := range row {
			cells[j] = c
		}
		if err := f.SetSheetRow(sheet, fmt.Sprintf("A%d", i+1), &cells); err != nil {
			t.Fatalf("写 xlsx 行失败: %v", err)
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("保存 xlsx 失败: %v", err)
	}
}

// TestUATBatchF_ImportTemplateRoundTrip 锁 A（模板即回归资产）：
// 下载的模板原件必须能被自己的解析器读回——恰好 1 行示例用户且逐字段等值，
// 挂在 F2 的填写说明不得进入任何列（若说明回表头行且解析器回归 last-wins，此锁翻红）。
func TestUATBatchF_ImportTemplateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template.xlsx")
	if err := buildUserImportTemplate().SaveAs(path); err != nil {
		t.Fatalf("模板落盘失败: %v", err)
	}
	rows, err := readImportRows(path)
	if err != nil {
		t.Fatalf("模板应能被自家解析器读回: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("模板示例行应恰好 1 行，实得 %d（%+v）", len(rows), rows)
	}
	got := rows[0]
	want := importUserRow{
		Username:    "zhangsan",
		DisplayName: "张三",
		OrgName:     "销售部",
		Role:        store.RoleUser,
		Email:       "zhangsan@example.com",
	}
	if got != want {
		t.Fatalf("模板示例行逐字段等值锁失败：\n实际 %+v\n期望 %+v", got, want)
	}
}

// TestUATBatchF_HeaderHijackNegativeLock 锁 B（说明劫持负向）：
// 手工构造旧版模板形态——填写说明挂在表头行 F1（句中含「用户名称/管理员/邮箱」关键词）。
// 旧 last-wins 实现会把 username/display/role/email 索引覆写到 F 列（i=5），
// 数据行 F 列为空 → 用户名判空全部跳过 → 「无有效数据行」。
// 首中即停修复后：五列索引钉死在各自首次命中列，示例行照常读出。
func TestUATBatchF_HeaderHijackNegativeLock(t *testing.T) {
	longNote := "填写说明：用户名称必填且租户内唯一；角色可留空=普通用户（可填 普通用户/管理员/部门管理员/租户管理员）；部门须与现有组织名称一致，留空挂根组织；邮箱用于发送账号开通通知"
	all := [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱", longNote},
		{"lisi", "李四", "研发部", "管理员", "lisi@example.com", ""},
	}
	rows, err := buildImportRows(all)
	if err != nil {
		t.Fatalf("旧版说明挂表头行也必须能解析（首中即停=F-23① 核心修复）: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应恰好 1 行，实得 %d", len(rows))
	}
	got := rows[0]
	want := importUserRow{
		Username:    "lisi",
		DisplayName: "李四",
		OrgName:     "研发部",
		Role:        store.RoleDeptAdmin, // 「管理员」归一为部门管理员
		Email:       "lisi@example.com",
	}
	if got != want {
		t.Fatalf("说明行劫持未卡死，列索引跑偏：\n实际 %+v\n期望 %+v", got, want)
	}
}

// TestUATBatchF_CSVImportPath 锁 C（CSV 真支持 + 两路口径同源）：
// 同一份数据分别经 CSV 与 xlsx 喂 readImportRows，行数与逐字段结果必须全等
// （旧实现 CSV 必红：excelize.OpenFile 打不开 .csv → 「Excel 解析失败」假象）。
func TestUATBatchF_CSVImportPath(t *testing.T) {
	dir := t.TempDir()
	// CSV 一路：3 行数据（第 3 行稀疏：无邮箱，角色留空=普通用户；邮箱大写须被归一）
	csvPath := filepath.Join(dir, "users.csv")
	csvBody := "用户名称,姓名,部门,角色,邮箱\n" +
		"csvuser1,赵一,销售部,普通用户,CSV1@Example.COM\n" +
		"csvuser2,钱二,研发部,部门管理员,csv2@example.com\n" +
		"csvuser3,孙三,,,\n" +
		"csvuser4,,,,\n"
	if err := os.WriteFile(csvPath, []byte(csvBody), 0o644); err != nil {
		t.Fatal(err)
	}
	csvRows, err := readImportRows(csvPath)
	if err != nil {
		t.Fatalf("CSV 导入解析应成功（F-23③），实得: %v", err)
	}
	if len(csvRows) != 4 {
		t.Fatalf("CSV 行数等值锁失败：期望 4，实得 %d（%+v）", len(csvRows), csvRows)
	}
	if csvRows[0].Email != "csv1@example.com" {
		t.Fatalf("CSV 邮箱应统一小写，实得 %q", csvRows[0].Email)
	}
	if csvRows[0].Role != store.RoleUser || csvRows[1].Role != store.RoleDeptAdmin {
		t.Fatalf("CSV 角色归一失败: %q / %q", csvRows[0].Role, csvRows[1].Role)
	}
	if csvRows[2].DisplayName != "孙三" || csvRows[2].Email != "" || csvRows[2].Role != store.RoleUser {
		t.Fatalf("CSV 稀疏行（无角色无邮箱）建模失败: %+v", csvRows[2])
	}
	if csvRows[3].DisplayName != "csvuser4" {
		t.Fatalf("空姓名列应回退用户名，实得 %q", csvRows[3].DisplayName)
	}
	// xlsx 一路：同一份数据，结果必须与 CSV 逐字段全等（两路共用 buildImportRows）
	xlsxPath := filepath.Join(dir, "users.xlsx")
	writeImportXlsxFromRows(t, xlsxPath, [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱"},
		{"csvuser1", "赵一", "销售部", "普通用户", "CSV1@Example.COM"},
		{"csvuser2", "钱二", "研发部", "部门管理员", "csv2@example.com"},
		{"csvuser3", "孙三", "", "", ""},
		{"csvuser4", "", "", "", ""},
	})
	xlsxRows, err := readImportRows(xlsxPath)
	if err != nil {
		t.Fatalf("xlsx 导入解析应成功: %v", err)
	}
	if len(xlsxRows) != len(csvRows) {
		t.Fatalf("两路行数不等: csv=%d xlsx=%d", len(csvRows), len(xlsxRows))
	}
	for i := range csvRows {
		if csvRows[i] != xlsxRows[i] {
			t.Fatalf("两路第 %d 行建模不等: csv=%+v xlsx=%+v", i+1, csvRows[i], xlsxRows[i])
		}
	}
}

// TestUATBatchF_ImportReceiptHonesty 锁 D（回执两版 + mailLive 三态）。
func TestUATBatchF_ImportReceiptHonesty(t *testing.T) {
	if m := importSuccessMessage(true); m != "导入成功（初始密码已通过邮件通知）" {
		t.Fatalf("已寄出版文案等值锁失败: %q", m)
	}
	m := importSuccessMessage(false)
	if m == importSuccessMessage(true) || !strings.Contains(m, "线下转告") {
		t.Fatalf("未寄出版必须走线下转告文案（杜绝虚假承诺）: %q", m)
	}
	// mailLive：离线态（无队列 + MAIL_ENABLED 未开）判假——Noop 的 nil 不算送达
	s := &Server{}
	t.Setenv("MAIL_ENABLED", "")
	if s.mailLive() {
		t.Fatalf("Noop 兜底（MAIL_ENABLED≠1 且无队列）不得判为通道可用")
	}
	t.Setenv("MAIL_ENABLED", "1")
	t.Setenv("SMTP_HOST", "")
	if s.mailLive() {
		t.Fatalf("SMTP_HOST 缺失不得判为通道可用")
	}
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_USER", "svc@example.com")
	if !s.mailLive() {
		t.Fatalf("SMTP 三件套齐全应判为通道可用")
	}
	t.Setenv("SMTP_USER", "")
	if s.mailLive() {
		t.Fatalf("SMTP_USER 缺失不得判为通道可用")
	}
}

// newF23ImportProbe 装配 F-23 锁 E 的 handler 探针：
// 命名共享缓存内存 SQLite + 租户 + tenant_admin JWT + 独立 UploadDir。
func newF23ImportProbe(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:f23imp_%s?mode=memory&cache=shared", t.Name()))
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
	tn, err := ts.Create("f23t", "F23探针租", "", "{}")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(tn.ID, "f23-admin", auth.PasswordHash("pw123456"), "F23探针管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UploadDir = t.TempDir()
	return &Server{Store: st, Ten: ts, Cfg: cfg}, tok
}

// callUserBulkImportXlsx 以 multipart 上传一份 xlsx（file 字段），直调批量导入 handler。
func callUserBulkImportXlsx(t *testing.T, s *Server, tok, xlsxPath string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := os.ReadFile(xlsxPath)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "users.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(fw, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/bulk-import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleUserBulkImport(rec, req)
	return rec
}

// TestUATBatchF_BulkImportHandlerOfflineReceipt 锁 E（handler 全链 · 离线态回执）：
// 两行用户（一行带邮箱一行不带）经真实 xlsx 上传导入成功，但邮件通道处于 Noop 兜底
// （MAIL_ENABLED 未开、无队列）——两行回执都必须落「线下转告」版。
// 旧实现（无条件「已通过邮件通知」）在此必红；修复丢失（mailLive 判断被删）同样必红。
func TestUATBatchF_BulkImportHandlerOfflineReceipt(t *testing.T) {
	s, tok := newF23ImportProbe(t)
	t.Setenv("MAIL_ENABLED", "") // 钉死离线态：t.Setenv 自动在用例结束恢复
	xlsxPath := filepath.Join(t.TempDir(), "bulk.xlsx")
	writeImportXlsxFromRows(t, xlsxPath, [][]string{
		{"用户名称", "姓名", "部门", "角色", "邮箱"},
		{"imp1", "导入一", "", "普通用户", "imp1@example.com"},
		{"imp2", "导入二", "", "", ""},
	})
	rec := callUserBulkImportXlsx(t, s, tok, xlsxPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("批量导入应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Success bool `json:"success"`
		Created int  `json:"created"`
		Results []struct {
			Username string `json:"username"`
			OK       bool   `json:"ok"`
			Message  string `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("导入响应解码失败: %v (%s)", err, rec.Body.String())
	}
	if out.Created != 2 || len(out.Results) != 2 {
		t.Fatalf("应成功导入 2 行，实得 created=%d results=%d（%s）", out.Created, len(out.Results), rec.Body.String())
	}
	for _, rr := range out.Results {
		if !rr.OK {
			t.Fatalf("行 %s 应导入成功: %s", rr.Username, rr.Message)
		}
		if !strings.Contains(rr.Message, "线下转告") {
			t.Fatalf("离线态（Noop 通道）下回执不得承诺「已通过邮件通知」，行 %s 实得: %q", rr.Username, rr.Message)
		}
	}
	// 落库侧顺带等值：imp1 角色归一为普通用户、imp2 邮箱为空
	// （GetUserByUsernameGlobal 返回同名切片——用户名仅租户内唯一，全局可重名）
	u1s, e1 := s.Store.GetUserByUsernameGlobal("imp1")
	u2s, e2 := s.Store.GetUserByUsernameGlobal("imp2")
	if e1 != nil || len(u1s) != 1 || e2 != nil || len(u2s) != 1 {
		t.Fatalf("导入用户应各恰有一行落库: imp1=%v(%v) imp2=%v(%v)", len(u1s), e1, len(u2s), e2)
	}
	if u1s[0].Role != store.RoleUser || u2s[0].Email != "" {
		t.Fatalf("落库等值锁失败: u1.role=%q u2.email=%q", u1s[0].Role, u2s[0].Email)
	}
}
