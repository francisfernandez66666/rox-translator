// ============ tmreview_tenant_test.go · 职责说明 ============
// ★ F-62（2026-09-26 批 I-8）TM 待审池租户侧只读接口 GET /api/me/tm-review/list 的接口级断言。
//
// 为什么必须有接口锁（而不只是 store 层）：F-62 的根因就是「能力在 store/超管接口里有，
// 租户拿不到」，所以本文件把三条对外契约钉死：
//
//	① 越权面：租户 4 带 X-Tenant-ID: 3 头，仍然只看得到租户 3 以外的自己的数据
//	   （tid 只认 token → 头在这条路上没有话语权，这是 F-55 同族的读写同源锁）；
//	② 鉴权面：未登录 401（UNAUTHORIZED）、平台超管 403（FORBIDDEN）且**不回任何句对**，
//	   脏 status 400（VALIDATION_ERROR）——三者都走统一错误出口、带 code；
//	③ 去向腿闭环：超管「通过」后，同一句对以 status=approved 出现在**该租户**的列表里，
//	   并且真的落到 tm_segments（承诺「审核通过后自动进翻译记忆」的机器证据）。
//
// 环境：临时文件库 kb.Open + 共享连接的 store.New（与生产装配同序），方言按 §一·4 钉 SQLite。
// 运行：cd backend-go && go test -count=1 ./internal/api/ -run MyTmReview
// ========================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/kb"
	"translator/internal/store"
)

// myTmReviewEnv 建 KB+Store 共享连接环境并钉死 SQLite 方言（返回可直接打 handler 的 Server）。
func myTmReviewEnv(t *testing.T) (*Server, *store.Store, *kb.KBDatabase) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	kdb, err := kb.Open(filepath.Join(t.TempDir(), "tmreview.db"))
	if err != nil {
		t.Fatalf("打开 KB 失败: %v", err)
	}
	t.Cleanup(func() { kdb.Close() })
	if err := kdb.EnsureTenantMigration(); err != nil {
		t.Fatalf("tm_segments 迁移失败: %v", err)
	}
	st, err := store.New(kdb.RawDB())
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return &Server{Store: st, DB: kdb}, st, kdb
}

// myTmReviewResp 本文件关心的出参字段（列表 + 摘要 + 截断标记）。
type myTmReviewResp struct {
	Success    bool `json:"success"`
	Candidates []struct {
		ID       int64  `json:"id"`
		Zh       string `json:"zh"`
		Lang     string `json:"lang"`
		Trans    string `json:"trans"`
		Source   string `json:"source"`
		Status   string `json:"status"`
		Reviewer string `json:"reviewer"`  // ★ 必须恒为空：租户侧投影不含审核人
		TenantID *int64 `json:"tenant_id"` // ★ 必须为 nil：出参根本不该有这个键
	} `json:"candidates"`
	Summary struct {
		Pending  int64 `json:"pending"`
		Approved int64 `json:"approved"`
		Rejected int64 `json:"rejected"`
		Total    int64 `json:"total"`
	} `json:"summary"`
	Truncated bool   `json:"truncated"`
	Code      string `json:"code"`
}

// getMyTmReview 打一次租户侧列表接口（hdrs 为附加请求头，成对传入）。
func getMyTmReview(t *testing.T, s *Server, bearer string, hdrs ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/me/tm-review/list", nil)
	if bearer != "" {
		req.Header.Set("Authorization", bearer)
	}
	for i := 0; i+1 < len(hdrs); i += 2 {
		req.Header.Set(hdrs[i], hdrs[i+1])
	}
	rec := httptest.NewRecorder()
	s.handleMyTmReviewList(rec, req)
	return rec
}

// decodeMyTmReview 解析成功响应（失败即 fatal，把原始体带进报错，方便看错误出口长什么样）。
func decodeMyTmReview(t *testing.T, rec *httptest.ResponseRecorder) *myTmReviewResp {
	t.Helper()
	var out myTmReviewResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	return &out
}

// mkCandidate 走生产写路径建一条候选（CreateTmReview：含脱敏与 opt-out 分支）。
func mkCandidate(t *testing.T, st *store.Store, tid int64, zh, lang, trans string) *store.TmReview {
	t.Helper()
	cr := &store.TmReview{TenantID: tid, Zh: zh, Lang: lang, Trans: trans, Source: "bitext", RefType: "import"}
	if err := st.CreateTmReview(cr); err != nil {
		t.Fatalf("建候选失败: %v", err)
	}
	if cr.ID == 0 {
		t.Fatalf("候选未落库（该租户关闭了数据回流？造数环境异常）")
	}
	return cr
}

// TestMyTmReviewListTenantScoped ① 跨租户隔离：带别人的 X-Tenant-ID 也换不走自己的作用域。
func TestMyTmReviewListTenantScoped(t *testing.T) {
	s, st, _ := myTmReviewEnv(t)
	u3, err := st.CreateUser(3, "tm_t3", "x", "租户三用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	u4, err := st.CreateUser(4, "tm_t4", "x", "租户四用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	mkCandidate(t, st, 3, "租户三的私密句", "en", "secret of three")
	mkCandidate(t, st, 4, "租户四的进度句", "de", "fortschritt")

	// u3 的正向对照（两侧都钉，防「只测已知的那一侧」）：租户 3 只看得到自己的那一条
	rec3 := getMyTmReview(t, s, bearerFor(t, u3))
	if rec3.Code != 200 {
		t.Fatalf("租户 3 应 200，实得 %d: %s", rec3.Code, rec3.Body.String())
	}
	b3 := decodeMyTmReview(t, rec3)
	if len(b3.Candidates) != 1 || b3.Candidates[0].Zh != "租户三的私密句" {
		t.Fatalf("租户 3 应只看到自己 1 条，实际: %s", rec3.Body.String())
	}
	if strings.Contains(rec3.Body.String(), "租户四的进度句") {
		t.Fatal("★ 跨租户泄漏：租户 3 的响应里出现租户 4 的句对")
	}

	rec := getMyTmReview(t, s, bearerFor(t, u4), "X-Tenant-ID", "3")
	if rec.Code != 200 {
		t.Fatalf("应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	body := decodeMyTmReview(t, rec)
	if !body.Success {
		t.Fatalf("应 success:true: %s", rec.Body.String())
	}
	if len(body.Candidates) != 1 {
		t.Fatalf("租户 4 只应看到 1 条，实际 %d 条: %s", len(body.Candidates), rec.Body.String())
	}
	for _, c := range body.Candidates {
		if strings.Contains(rec.Body.String(), "租户三的私密句") {
			t.Fatal("★ 跨租户泄漏：响应体里出现租户 3 的句对")
		}
		if c.Zh != "租户四的进度句" {
			t.Fatalf("返回了别人的句对: %+v", c)
		}
		// 投影白名单：审核人与 tenant_id 一律不下发
		if c.Reviewer != "" {
			t.Fatalf("租户侧出参带了 reviewer=%q（平台侧内部信息）", c.Reviewer)
		}
		if c.TenantID != nil {
			t.Fatalf("租户侧出参不该有 tenant_id 键（值=%d）", *c.TenantID)
		}
	}
	// 反向对照：同一时刻超管视角仍是全池（本批只补租户视图，没动超管口径）
	super, err := st.CreateUser(0, "tm_super", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 超管失败: %v", err)
	}
	recA := httptest.NewRecorder()
	reqA := httptest.NewRequest(http.MethodGet, "/api/admin/tm-review/list", nil)
	reqA.Header.Set("Authorization", bearerFor(t, super))
	s.handleTmReviewList(recA, reqA)
	if recA.Code != 200 {
		t.Fatalf("超管列表应 200，实得 %d", recA.Code)
	}
	var adminResp struct {
		Candidates []store.TmReview `json:"candidates"`
	}
	if err := json.Unmarshal(recA.Body.Bytes(), &adminResp); err != nil {
		t.Fatalf("解析超管列表失败: %v", err)
	}
	if len(adminResp.Candidates) != 2 {
		t.Fatalf("超管视角应看到两租户共 2 条（跨租户全池口径未变），实际 %d", len(adminResp.Candidates))
	}
}

// TestMyTmReviewListAuthzErrors ② 鉴权与参数面：401 / 403 / 400，全部走统一错误出口带 code。
func TestMyTmReviewListAuthzErrors(t *testing.T) {
	s, st, _ := myTmReviewEnv(t)
	u, err := st.CreateUser(8, "tm_t8", "x", "租户八用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	mkCandidate(t, st, 8, "仅本租户可见", "en", "only here")

	// ① 未登录
	rec := getMyTmReview(t, s, "")
	if rec.Code != 401 {
		t.Fatalf("未登录应 401，实得 %d: %s", rec.Code, rec.Body.String())
	}
	assertErrCode(t, rec, "UNAUTHORIZED")
	if strings.Contains(rec.Body.String(), "仅本租户可见") {
		t.Fatal("401 响应里居然带了数据")
	}

	// ② 平台超管（tenant_id=0）：本接口无租户归属 → 403 且零句对
	super, err := st.CreateUser(0, "tm_super0", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 超管失败: %v", err)
	}
	rec2 := getMyTmReview(t, s, bearerFor(t, super))
	if rec2.Code != 403 {
		t.Fatalf("平台账号打租户自助视图应 403，实得 %d: %s", rec2.Code, rec2.Body.String())
	}
	assertErrCode(t, rec2, "FORBIDDEN")
	if strings.Contains(rec2.Body.String(), "candidates") {
		t.Fatalf("403 不应回 candidates 键（防「越权但数据已在包里」）：%s", rec2.Body.String())
	}

	// ③ 脏 status → 400 VALIDATION_ERROR（不得静默当成「全部」）
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/me/tm-review/list?status=pending,approved", nil)
	req3.Header.Set("Authorization", bearerFor(t, u))
	s.handleMyTmReviewList(rec3, req3)
	if rec3.Code != 400 {
		t.Fatalf("脏 status 应 400，实得 %d: %s", rec3.Code, rec3.Body.String())
	}
	assertErrCode(t, rec3, "VALIDATION_ERROR")

	// ④ 正常三态过滤放行，且摘要与列表口径一致
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodGet, "/api/me/tm-review/list?status=Pending", nil) // 大小写容错
	req4.Header.Set("Authorization", bearerFor(t, u))
	s.handleMyTmReviewList(rec4, req4)
	if rec4.Code != 200 {
		t.Fatalf("status=Pending 应归一放行，实得 %d: %s", rec4.Code, rec4.Body.String())
	}
	b4 := decodeMyTmReview(t, rec4)
	if len(b4.Candidates) != 1 || b4.Summary.Pending != 1 || b4.Summary.Total != 1 {
		t.Fatalf("三态过滤/摘要不符: %s", rec4.Body.String())
	}
	if b4.Truncated {
		t.Fatal("1 条数据不该报 truncated")
	}
}

// assertErrCode 锁统一错误出口的 code 字段（AGENTS §一·8：前端与 SDK 要能按 code 分支）。
func assertErrCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var e struct {
		Success *bool  `json:"success"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("错误响应应为 JSON: %v (%s)", err, rec.Body.String())
	}
	if e.Code != code {
		t.Fatalf("错误码应为 %s，实际 %q（体：%s）", code, e.Code, rec.Body.String())
	}
	if e.Success == nil || *e.Success {
		t.Fatalf("错误响应必须显式 success:false（体：%s）", rec.Body.String())
	}
}

// TestMyTmReviewApproveShowsUpForTenant ③ 去向腿闭环：超管通过 → 租户列表转 approved + 真的进了 TM。
func TestMyTmReviewApproveShowsUpForTenant(t *testing.T) {
	s, st, kdb := myTmReviewEnv(t)
	owner, err := st.CreateUser(9, "tm_t9", "x", "租户九用户", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 失败: %v", err)
	}
	super, err := st.CreateUser(0, "tm_super9", "x", "超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("CreateUser 超管失败: %v", err)
	}
	cr := mkCandidate(t, st, 9, "审核通过后要进记忆", "en", "into memory")

	// 审批前：租户侧看得到，但状态是 pending（F-62 原缺陷＝这一条腿整条缺失）
	recBefore := getMyTmReview(t, s, bearerFor(t, owner))
	before := decodeMyTmReview(t, recBefore)
	if len(before.Candidates) != 1 || before.Candidates[0].Status != "pending" {
		t.Fatalf("审批前应为 1 条 pending: %s", recBefore.Body.String())
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/tm-review/approve",
		strings.NewReader(`{"id":`+itoaInt64(cr.ID)+`}`))
	req.Header.Set("Authorization", bearerFor(t, super))
	s.handleTmReviewApprove(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("超管审批应成功: %d %s", rec.Code, rec.Body.String())
	}

	after := decodeMyTmReview(t, getMyTmReview(t, s, bearerFor(t, owner)))
	if len(after.Candidates) != 1 || after.Candidates[0].Status != "approved" {
		t.Fatalf("审批后租户侧应看到 approved: %s", rec.Body.String())
	}
	if after.Summary.Pending != 0 || after.Summary.Approved != 1 {
		t.Fatalf("审批后摘要应 pending=0/approved=1，实际 %+v", after.Summary)
	}
	// 记忆侧：tm_segments 里该租户这一句确实有译文（客户拿导出/命中当证据的那张表）
	var en string
	if err := db.QueryRow(kdb.RawDB(), db.CurrentDialect(),
		"SELECT COALESCE(en,'') FROM tm_segments WHERE tenant_id=? AND zh=?", 9, "审核通过后要进记忆").Scan(&en); err != nil {
		t.Fatalf("审批后 tm_segments 查不到该句（承诺落空）: %v", err)
	}
	if en != "into memory" {
		t.Fatalf("tm_segments 译文不符，实际 %q", en)
	}
}
