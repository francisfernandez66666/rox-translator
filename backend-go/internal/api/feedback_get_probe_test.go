// ============ feedback_get_probe_test.go · 职责说明 ============
// 反馈详情接口 `/api/feedback/get` 的「存在性探针」关闭锁（★ 〇-U 批 I-10 收尾定夺 · F-64②）。
//
// 缺陷因果链（改前）：
//
//	① 这条接口按 id **全局**取行，归属判定在取到行之后；
//	② 于是「这条不存在」回 404、「这条存在但属于别人」回 403；
//	③ 任何登录用户都能拿 id 逐一试探：403＝存在、404＝不存在。
//	   反馈总量与提交节奏（连号 id 的密度）就这样变成一个免费的计数接口，
//	   而客户反馈条数与留资量是本产品的经营数据。
//	④ HTTP 200 + success:false 的旧写法（本批更早形态）让监控完全看不见这些探测。
//
// 修法（业界口径）：两种情况并成**同一个 404、同一句文案**——归属不符也不承认存在。
//
// 本文件的四条锁：
//
//	A) 匿名 401（未登录与越权必须分流：core.ts 只在 401 走重登录）；
//	B) 本人 200 且拿到自己的那条（正向对照，防「整面改成报错」的反向翻车）；
//	C) 超管 200（超管列表本来就看得到，运营侧不受影响）；
//	D) ★ 同码同文案：别人的反馈 与 不存在的 id 两次请求，状态码/code/message
//	   三者必须逐字节相等——只要有任何一项不同，存在性探针就还开着。
//
// 方言：自钉 SQLite 内存库（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestFeedbackGet
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"translator/internal/store"
)

// itoa64F 把 int64 主键转成查询串里的 id（本地小工具，避免与包内其它同名 helper 撞）。
func itoa64F(v int64) string { return strconv.FormatInt(v, 10) }

// getFeedbackDetail 打 /api/feedback/get?id=...，返回状态码与解析后的错误体。
func getFeedbackDetail(t *testing.T, s *Server, token, id string) (int, string, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/feedback/get?id="+id, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handleFeedbackGet(rec, req)
	var body map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	code, _ := body["code"].(string)
	msg, _ := body["message"].(string)
	return rec.Code, code, msg
}

// TestFeedbackGetProbeClosed 锁 D（核心）+ A/B/C 三条配套。
func TestFeedbackGetProbeClosed(t *testing.T) {
	pinSqliteDialect(t) // 自钉方言（AGENTS.md §一·4）：run_uat 的 PG env 不得泄漏给本内存 SQLite 用例
	s, tokens := newAdminScopeTestServer(t)

	// 造一条「甲企业用户 u_co_a」本人的反馈：归属信息必须真实落到 UserID 列，
	// 否则下面的分支判定会整片失真（这条也是 A/B/C 三条锁的前置）。
	f := &store.Feedback{
		TenantID:   findUser(t, s, "u_co_a").TenantID,
		UserID:     findUser(t, s, "u_co_a").ID,
		TargetType: "text",
		Content:    "F64 探针关闭锁用反馈",
	}
	if err := s.Store.CreateFeedback(f); err != nil {
		t.Fatalf("CreateFeedback 失败: %v", err)
	}
	if f.ID <= 0 {
		t.Fatalf("CreateFeedback 未回填 ID: %+v", f)
	}
	ghostID := f.ID + 100000 // 明确不存在的 id（同表内不可能与 f.ID 撞）

	// B) 本人：200 且能取到自己这条
	if code, c, m := getFeedbackDetail(t, s, tokens["u_co_a"], itoa64F(f.ID)); code != 200 {
		t.Fatalf("本人应 200，实际 %d code=%s msg=%s", code, c, m)
	}

	// C) 超管：200（超管看全量，改判 404 会把运营侧查询打断）
	if code, c, m := getFeedbackDetail(t, s, tokens["admin"], itoa64F(f.ID)); code != 200 {
		t.Fatalf("超管应 200，实际 %d code=%s msg=%s", code, c, m)
	}

	// A) 匿名：401（与 403 分流是 core.ts 重登录链路的依赖）
	code401, c401, m401 := getFeedbackDetail(t, s, "", itoa64F(f.ID))
	if code401 != 401 || c401 != "UNAUTHORIZED" {
		t.Fatalf("未登录应 401/UNAUTHORIZED，实际 %d/%s msg=%s", code401, c401, m401)
	}

	// D) ★ 同码同文案：别人的反馈 vs 根本不存在的 id
	peerCode, peerC, peerM := getFeedbackDetail(t, s, tokens["u_co_b"], itoa64F(f.ID))
	ghostCode, ghostC, ghostM := getFeedbackDetail(t, s, tokens["u_co_b"], itoa64F(ghostID))
	if peerCode != 404 || peerC != "NOT_FOUND" {
		t.Fatalf("别人的反馈应判 404/NOT_FOUND（旧实现回 403＝存在性探针），实际 %d/%s msg=%s",
			peerCode, peerC, peerM)
	}
	if peerCode != ghostCode || peerC != ghostC || peerM != ghostM {
		t.Fatalf("存在性探针未关闭：别人的反馈=(%d,%s,%q)，不存在的 id=(%d,%s,%q)。\n"+
			"三项必须逐字节相等，否则调用方可用状态码/错误码/文案任一通道数出「这条存在」。",
			peerCode, peerC, peerM, ghostCode, ghostC, ghostM)
	}
	// 反向锁：这一支不许退回 403（403 本身就是「存在但你不是他」的泄漏）
	if peerCode == 403 {
		t.Fatalf("不得回 403：403 等于承认这条反馈存在")
	}
	// 响应体不许带出反馈内容（只承认「不存在」，不承认「有这么一条」）
	if rec := getFeedbackProbeBody(t, s, tokens["u_co_b"], f.ID); rec != "" {
		t.Fatalf("越权详情不得回带反馈内容或字段，实际 body=%s", rec)
	}
}

// getFeedbackProbeBody 取越权详情的响应体原文，并做「不得含业务字段」的过滤判定：
// 命中 feedback/content/user_id 任一字段名即返回原文（让调用方直接红），否则返回空串。
func getFeedbackProbeBody(t *testing.T, s *Server, token string, id int64) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/feedback/get?id="+itoa64F(id), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleFeedbackGet(rec, req)
	body := rec.Body.String()
	for _, k := range []string{`"feedback"`, `"content"`, `"user_id"`, `"source_text"`, `"replies"`} {
		if containsCI(body, k) {
			return body
		}
	}
	return ""
}
