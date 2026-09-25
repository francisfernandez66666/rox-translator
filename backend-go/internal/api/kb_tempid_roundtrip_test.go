// ============ kb_tempid_roundtrip_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 F 的 F-08 回归断言（kb.go temp_id 长度焊死）。
//
// 缺陷回顾：识别阶段 handleRecognizeKB 曾生成 randHex(12)（12 位 hex）的 temp_id，
// 而导入阶段 handleImportKB 的格式白名单 tempIDRe 要求 24 位 hex——正常链路生成即自拒
// （新 KB 文件导入 100% 报「temp_id 无效」），且注释口径与实际生成串长互相矛盾。
// 修复：TempID 生成改为 randHex(24)，与 tempIDRe（^[0-9a-f]{24}$）严格同长。
//
// 本文件钉两层锁：
//
//	① 弱锁（格式同源）：randHex(24) 产串必须恰好 24 位且命中 tempIDRe；
//	   randHex(12) 产串必须 NOT 命中 tempIDRe（钉死「24 是判据下限」，防止反向偷改正则）；
//	② 强锁（handler 全链往返）：httptest 直调 handleRecognizeKB（multipart 上传 CSV）
//	   → 取响应 temp_id（断命中 tempIDRe）→ handleImportKB 断 200 且 added=2；
//	   并补三条负向：路径穿越载荷 "../../x" → 400「temp_id 无效」（A3 白名单闸不许退化）、
//	   12 位 hex（randHex(12) 时代产物）→ 400、同一 temp_id 二次导入 → 400「已过期」
//	   （导入成功后元信息必须被清理，防重放）。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）；
// 内存库用命名共享缓存 DSN（file:xxx?mode=memory&cache=shared），
// 保证连接池多连接看到同一份库（裸 :memory: 每连接各开一个空库）。
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
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/kb"
	"translator/internal/store"
	"translator/internal/tenant"
)

// TestUATBatchF_TempIDFormatLock 弱锁：temp_id 生成串长与格式白名单严格同源。
// randHex(24) 必须产出恰好 24 位 hex 并命中 tempIDRe；randHex(12)（缺陷时代产物）
// 必须 NOT 命中——若有人反向把 tempIDRe 改成 {12,24} 之类放宽判据，此锁即红。
func TestUATBatchF_TempIDFormatLock(t *testing.T) {
	id24 := randHex(24)
	if len(id24) != 24 {
		t.Fatalf("randHex(24) 应产出 24 位串，实际 len=%d (%q)", len(id24), id24)
	}
	if !tempIDRe.MatchString(id24) {
		t.Fatalf("randHex(24) 产物必须命中 tempIDRe（导入白名单），实际 %q", id24)
	}
	id12 := randHex(12)
	if len(id12) != 12 {
		t.Fatalf("randHex(12) 应产出 12 位串，实际 len=%d (%q)", len(id12), id12)
	}
	if tempIDRe.MatchString(id12) {
		t.Fatalf("12 位 hex 不得命中 tempIDRe（放宽判据=F-08 复发），实际 %q", id12)
	}
}

// newKBTempIDProbe 装配 F-08 强锁探针服务器：
// 命名共享缓存内存 SQLite（Store + 租户服务 + 一个租户及其 tenant_admin）
// + 一次性 UploadDir（t.TempDir）+ 非空 s.DB（KB 已加载态，两 handler 的入口守卫）。
// 返回: Server、租户 ID、tenant_admin 的 JWT。
func newKBTempIDProbe(t *testing.T) (*Server, int64, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:f08kb_%s?mode=memory&cache=shared", t.Name()))
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
	tn, err := ts.Create("f08t", "F08探针租", "", "{}")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(tn.ID, "f08-admin", auth.PasswordHash("pw123456"), "F08探针管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UploadDir = t.TempDir()
	// s.DB 只需非空（两 handler 仅以 nil 判定「翻译技能是否加载」，导入写库走 s.Store）
	return &Server{Store: st, Ten: ts, Cfg: cfg, DB: &kb.KBDatabase{}}, tn.ID, tok
}

// callRecognizeKB 以 multipart 上传一份 中文×英语-en 两列小 CSV，直调识别 handler，
// 返回响应记录（调用方自行解码 temp_id）。参数 s=服务器；tok=JWT。
func callRecognizeKB(t *testing.T, s *Server, tok string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "f08术语表.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(fw, "中文,英语-en\n人工智能,Artificial Intelligence\n机器翻译,Machine Translation\n"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/admin/recognize-kb", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleRecognizeKB(rec, req)
	return rec
}

// callImportKB 以 JSON {temp_id, package_id} 直调导入 handler，返回响应记录。
// 参数 s=服务器；tok=JWT；tempID=待导入引用；pkgID=目标企业包 ID。
func callImportKB(t *testing.T, s *Server, tok, tempID string, pkgID int64) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"temp_id": tempID, "package_id": pkgID})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/import-kb", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleImportKB(rec, req)
	return rec
}

// TestUATBatchF_KBTempIDRoundTrip 强锁：识别→导入全链往返必须成功，
// 并钉死三条负向（穿越载荷 / 12 位旧串 / 二次导入重放）。
// 反向验证口径：把 kb.go 的 randHex(24) 回改成 randHex(12)，本用例第一步断言即红。
func TestUATBatchF_KBTempIDRoundTrip(t *testing.T) {
	s, tid, tok := newKBTempIDProbe(t)

	// —— 识别阶段：200 且 temp_id 命中导入侧格式白名单 ——
	rec := callRecognizeKB(t, s, tok)
	var recog struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		TempID  string `json:"temp_id"`
		Total   int    `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &recog); err != nil {
		t.Fatalf("识别响应非 JSON: %v (%s)", err, rec.Body.String())
	}
	if !recog.Success || recog.Total != 2 {
		t.Fatalf("识别应成功且 2 行，实得 success=%v total=%d msg=%s", recog.Success, recog.Total, recog.Message)
	}
	if !tempIDRe.MatchString(recog.TempID) {
		t.Fatalf("识别返回的 temp_id 必须命中导入侧白名单 tempIDRe（F-08 核心判据），实际 %q", recog.TempID)
	}

	// —— 建目标企业包（tenant 类型，tenant_admin 可管理）——
	pkg, err := s.Store.CreateKBPackage(tid, 0, "f08pkg", "F08探针包", store.PackTenant, "source")
	if err != nil {
		t.Fatalf("建探针知识库包失败: %v", err)
	}

	// —— 导入阶段：正常链路必须 200 且 2 条全入库 ——
	imp := callImportKB(t, s, tok, recog.TempID, pkg.ID)
	if imp.Code != http.StatusOK {
		t.Fatalf("正常 temp_id 导入应 200（生成侧与判据侧串长不同源即 F-08 复发），实得 %d: %s", imp.Code, imp.Body.String())
	}
	var impOut struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Added   int    `json:"added"`
	}
	if err := json.Unmarshal(imp.Body.Bytes(), &impOut); err != nil || !impOut.Success || impOut.Added != 2 {
		t.Fatalf("导入应成功且 added=2，实得 %s", imp.Body.String())
	}

	// —— 负向①：路径穿越载荷必须被格式白名单卡死（A3 双重闸的第一道）——
	tr := callImportKB(t, s, tok, "../../etc/passwd", pkg.ID)
	if tr.Code != http.StatusBadRequest || !bytes.Contains(tr.Body.Bytes(), []byte("temp_id 无效")) {
		t.Fatalf("穿越载荷应 400「temp_id 无效」，实得 %d: %s", tr.Code, tr.Body.String())
	}

	// —— 负向②：12 位 hex（randHex(12) 时代产物）同样必须 400 ——
	tw := callImportKB(t, s, tok, "a1b2c3d4e5f6", pkg.ID)
	if tw.Code != http.StatusBadRequest {
		t.Fatalf("12 位旧串应 400 拒绝，实得 %d: %s", tw.Code, tw.Body.String())
	}

	// —— 负向③：导入成功后元信息必须已清理，同 ID 重放 → 400「已过期」——
	rp := callImportKB(t, s, tok, recog.TempID, pkg.ID)
	if rp.Code != http.StatusBadRequest || !bytes.Contains(rp.Body.Bytes(), []byte("已过期")) {
		t.Fatalf("二次导入应 400「识别数据已过期」，实得 %d: %s", rp.Code, rp.Body.String())
	}
}
