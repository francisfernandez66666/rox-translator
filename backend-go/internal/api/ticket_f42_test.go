// ============ ticket_f42_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 C（F-42 假 completed 专项）api 层回归断言。
//
// 钉两侧「同源判据」：
//
//	① ticketDeliverable 纯函数四形态 —— 文本有产物 / 文本空载荷（鬼单形状，
//	   生产任务 90 即 {"translations":{"en":""}}）/ 文件有回写产物 / 文件全空产物，
//	   外加 tickets 级产物列兜底与坏 JSON 保守判 false；
//	② 对外契约：/openapi/v1/tasks/status 遇 completed 但无产物必须降级 failed
//	   并带 error_code（旧映射照发空 translations，SDK 按成功消费到空结果），
//	   /openapi/v1/tasks/download 同态必须回 not_ready（旧实现只判 status，
//	   能打出零字节 zip）；
//	③ 负向对照：真有产物的 completed 单不得被误降级（否则就是把交付闸焊死）。
//
// 方言：自钉 SQLite 内存库 + 显式覆盖 config.C（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestUATBatchC
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

// f42Setup 建内存库 + 一个带 translate 权限 Key 的租户用户（OpenAPI 任务形态）。
// 返回：探针 Server、Store、Key 明文、归属用户 ID。
func f42Setup(t *testing.T) (*Server, *store.Store, string, int64) {
	t.Helper()
	pinSqliteDialect(t)
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	u, err := st.CreateUser(1, "f42_owner", auth.PasswordHash("pw123456"), "接口测试人", "tenant_admin", 0, 0)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	key, err := st.CreateAPIKey(1, u.ID, "f42-key", "translate", 0)
	if err != nil {
		t.Fatalf("签发 API Key 失败: %v", err)
	}
	return &Server{Store: st}, st, key, u.ID
}

// f42APITicket 建一张 OpenAPI 形态工单（created_by=0 + api_user_id 盖印），
// 并按入参写终态/载荷/文件产物行。参数：st=存储，uid=Key 归属用户，
// finalResult=载荷 JSON，withFile=是否挂文件行，fileResult=文件产物路径（空=无产物）。
func f42APITicket(t *testing.T, st *store.Store, uid int64, finalResult string, withFile bool, fileResult string) *store.Ticket {
	t.Helper()
	filePath := ""
	if withFile {
		filePath = "/tmp/f42_src.docx"
	}
	tk, err := st.CreateTicket(1, 0, "F42 接口单", "本系统支持多格式文件翻译", filePath, "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if _, err := st.DB().Exec(
		"UPDATE tickets SET status='completed', final_result=?, api_user_id=?, tokens_billed=300 WHERE id=?",
		finalResult, uid, tk.ID); err != nil {
		t.Fatalf("置终态失败: %v", err)
	}
	if withFile {
		tf, err := st.AddTicketFile(&store.TicketFile{TenantID: 1, TicketID: tk.ID, FileName: "a.docx", FilePath: filePath})
		if err != nil {
			t.Fatalf("挂文件行失败: %v", err)
		}
		if fileResult != "" {
			if err := st.SetTicketFileResult(tf.ID, fileResult); err != nil {
				t.Fatalf("写文件产物路径失败: %v", err)
			}
		}
	}
	fresh, err := st.GetTicket(tk.ID, 1)
	if err != nil || fresh == nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	return fresh
}

// f42Get 以 Bearer Key 打开放接口，返回响应记录器。
// 参数：handler=被测开放接口（status/download），key=Bearer 明文，query=查询串。
func f42Get(t *testing.T, s *Server, handler http.HandlerFunc, key, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/openapi/v1/tasks?"+query, nil)
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

// f42ContainsAny 响应体是否含任一子串（不引 strings 到断言噪声里的最小工具）。
func f42ContainsAny(body string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(body, sub) {
			return true
		}
	}
	return false
}

// TestUATBatchC_TicketDeliverableShapes ①：产物判据四形态 + 两向兜底。
func TestUATBatchC_TicketDeliverableShapes(t *testing.T) {
	textOK := &store.Ticket{FinalResult: `{"translations":{"en":"This system supports..."}}`}
	textGhost := &store.Ticket{FinalResult: `{"translations":{"en":""}}`} // 任务 90 形状
	textBlank := &store.Ticket{FinalResult: ``}
	textBroken := &store.Ticket{FinalResult: `{not-json`}
	fileGhost := &store.Ticket{FilePath: "/tmp/a.docx", ResultPath: "", TextResultPath: ""}
	fileByTicketCol := &store.Ticket{FilePath: "/tmp/a.docx", ResultPath: "/tmp/a_out.docx"}
	fileByChild := &store.Ticket{FilePath: "/tmp/a.docx"}
	cases := []struct {
		name  string
		t     *store.Ticket
		files []*store.TicketFile
		want  bool
	}{
		{"文本有译文", textOK, nil, true},
		{"文本空槽位（鬼 completed）", textGhost, nil, false},
		{"文本无载荷", textBlank, nil, false},
		{"文本载荷坏 JSON（保守判无产物）", textBroken, nil, false},
		{"文件工单零产物", fileGhost, []*store.TicketFile{{FileName: "a.docx"}}, false},
		{"文件工单工单级产物列", fileByTicketCol, nil, true},
		{"文件工单子文件产物", fileByChild, []*store.TicketFile{{FileName: "a.docx", ResultPath: "/tmp/a_out.docx"}}, true},
	}
	for _, c := range cases {
		if got := ticketDeliverable(c.t, c.files); got != c.want {
			t.Errorf("%s：期望 %v 实得 %v", c.name, c.want, got)
		}
	}
}

// TestUATBatchC_OpenAPIStatusDowngradesGhostCompleted ②③：
// 鬼 completed 在状态接口降级 failed（含欠费前缀取码），真产物单保持 completed。
func TestUATBatchC_OpenAPIStatusDowngradesGhostCompleted(t *testing.T) {
	s, st, key, uid := f42Setup(t)

	ghost := f42APITicket(t, st, uid, `{"translations":{"en":""}}`, false, "")
	ghost.RejectReason = "insufficient_balance: 余额不足，请充值或升级套餐"
	ghost.RejectSource = store.RejectSourceSystem
	if err := st.UpdateTicket(ghost); err != nil {
		t.Fatalf("写驳回原因失败: %v", err)
	}
	real := f42APITicket(t, st, uid, `{"translations":{"en":"This system supports multi-format translation"}}`, false, "")

	w := f42Get(t, s, s.handleOpenAPITaskStatus, key, "id="+strconv.FormatInt(ghost.ID, 10))
	var resp struct {
		Success      bool              `json:"success"`
		Status       string            `json:"status"`
		ErrorCode    string            `json:"error_code"`
		Message      string            `json:"message"`
		Translations map[string]string `json:"translations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("响应解析失败: %v %s", err, w.Body.String())
	}
	if resp.Status != "failed" {
		t.Fatalf("鬼 completed 必须降级 failed，实得 %q（响应体 %s）", resp.Status, w.Body.String())
	}
	if resp.ErrorCode != "insufficient_balance" {
		t.Errorf("欠费前缀应取到 insufficient_balance 码，实得 %q", resp.ErrorCode)
	}
	if len(resp.Translations) != 0 {
		t.Errorf("降级后不得再回 translations，实得 %v", resp.Translations)
	}

	w2 := f42Get(t, s, s.handleOpenAPITaskStatus, key, "id="+strconv.FormatInt(real.ID, 10))
	var resp2 struct {
		Status       string            `json:"status"`
		Translations map[string]string `json:"translations"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("对照单响应解析失败: %v %s", err, w2.Body.String())
	}
	if resp2.Status != "completed" || resp2.Translations["en"] == "" {
		t.Errorf("有产物单被误降级（交付闸焊死）：status=%q translations=%v", resp2.Status, resp2.Translations)
	}
}

// TestUATBatchC_OpenAPIDownloadBlocksGhostCompleted ②：下载端点同口径拦截（旧实现只判 status）。
func TestUATBatchC_OpenAPIDownloadBlocksGhostCompleted(t *testing.T) {
	s, st, key, uid := f42Setup(t)
	ghost := f42APITicket(t, st, uid, `{"translations":{"en":""}}`, true, "")
	w := f42Get(t, s, s.handleOpenAPITaskDownload, key, "id="+strconv.FormatInt(ghost.ID, 10))
	body := w.Body.String()
	// ★ F-64①（批 I-7 2026-09-26）订正本行旧说法：writeTaskError 的「200 + success:false」
	// 形态已被 writeOpenAPIError 取代——鬼 completed 去要产物是请求时机与资源状态冲突，
	// 现回 **409 + error_code/code=not_ready**（旧形态让客户在 200 里解析文案才知道拿不到东西）。
	// 判据仍是两条：必须是 not_ready 错误码；不得带附件交付形态。
	if w.Code != http.StatusConflict {
		t.Errorf("鬼 completed 下载应回 409（状态冲突），实得 %d body=%s", w.Code, body)
	}
	if !f42ContainsAny(body, `"error_code":"not_ready"`) {
		t.Errorf("鬼 completed 下载应回 not_ready 错误码，实得 code=%d body=%s", w.Code, body)
	}
	if !f42ContainsAny(body, `"code":"not_ready"`) {
		t.Errorf("not_ready 必须同时以正主键 code 下发（对外文档定的 code，error_code 只是别名）：%s", body)
	}
	if f42ContainsAny(body, `"Content-Type": "application/`, `attachment;`) {
		t.Errorf("鬼 completed 不得带附件交付头，实得 %s", body)
	}
}
