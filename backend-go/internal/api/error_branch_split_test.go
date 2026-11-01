// ============ error_branch_split_test.go · 职责说明 ============
// DEF-P2-07（2026-11-01）：把「请求体解码失败」与「必填字段为空」并成同一句错误文案的写法，
// 在这两个入口各有一处（工单建单 / 知识库条目新增），本轮一起拆开并钉住。
//
// 为什么值得单独一条闸：现网实证是 `target_langs` 传成数组（开放 API 文档写的是逗号分隔字符串），
// 整包 JSON 解析失败，而屏幕告诉调用方「请提供源文本」——字段其实齐着。
// 这类红字**不报错、不崩、状态码也对**，只把排障方向整个带偏：调用方会去查"我是不是漏传正文"，
// 而真凶是类型。修法上属于 §一·8 状态码诚实化的同一族（错误体要带得出"错在哪一层"）。
//
// 判据取向：两种文案**互斥**判（含 A 且不含 B），不是"含 A 就行"——
// 只判"含格式错误"的话，把两个分支合并回去、文案写成"格式错误：请提供源文本"也能过。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// postJSON 以给定原始 body 直调 handler（**故意不 marshal**：要能送"字段类型不对"的原文）。
func postJSON(t *testing.T, h http.HandlerFunc, path, tok, rawBody string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(rawBody))
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

// mustErrMsg 从结构化错误体里取 message（apierrors.APIError 的字段是 success/code/message，
// 见 internal/errors/codes.go:195）——按字段取而不拿整串子串蒙判，
// 否则 trace_id 之类的噪声字段一改，判据就跟着漂。
func mustErrMsg(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("错误体不是 JSON: %v body=%s", err, w.Body.String())
	}
	if strings.TrimSpace(out.Message) == "" {
		t.Fatalf("错误体没有 message（统一出口被绕过？）: %s", w.Body.String())
	}
	return out.Message
}

// TestTicketCreateErrorBranchesAreSeparated 建单口两条分支各自可辨（DEF-P2-07 本体）。
func TestTicketCreateErrorBranchesAreSeparated(t *testing.T) {
	s, tok := f29Server(t)

	// ① 解码失败（target_langs 传数组 ⇒ 整包解析失败）：必须说"格式错误"，**不许**说"请提供源文本"
	w := postJSON(t, s.handleTicketCreate, "/api/tickets/create", tok,
		`{"source_text":"要翻的话","target_langs":["en"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("类型错误应 400，实得 %d body=%s", w.Code, w.Body.String())
	}
	if msg := mustErrMsg(t, w); !strings.Contains(msg, "格式错误") || strings.Contains(msg, "请提供源文本") {
		t.Fatalf("解码失败被并进了空值文案（把排障方向带偏的原缺陷形态）: %q", msg)
	}

	// ② 空 body（合法 JSON、但 source_text 缺失）：只许说"请提供源文本"，**不许**带"格式错误"
	w2 := postJSON(t, s.handleTicketCreate, "/api/tickets/create", tok, `{}`)
	if w2.Code != http.StatusBadRequest {
		t.Fatalf("空源文本应 400，实得 %d", w2.Code)
	}
	if msg := mustErrMsg(t, w2); !strings.Contains(msg, "请提供源文本") || strings.Contains(msg, "格式错误") {
		t.Fatalf("空值分支被写成了格式错误: %q", msg)
	}

	// ③ 真正的坏 JSON（连对象都不是）：走解码分支，同 ①
	w3 := postJSON(t, s.handleTicketCreate, "/api/tickets/create", tok, `not-json`)
	if w3.Code != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应 400，实得 %d", w3.Code)
	}
	if msg := mustErrMsg(t, w3); !strings.Contains(msg, "格式错误") || strings.Contains(msg, "请提供源文本") {
		t.Fatalf("坏 JSON 的文案不对: %q", msg)
	}

	// 射程自证（正向对照）：合法请求必须**穿过**这两道校验。
	// 只判"没被这两句文案挡回来"（建单成功后面还有配额闸门，报什么错不在本条射程内——
	// 但在内存库里通常直接成就 200，两种都算通过）。
	w4 := postJSON(t, s.handleTicketCreate, "/api/tickets/create", tok,
		`{"title":"闸门","source_text":"要翻的话","target_langs":"en"}`)
	if w4.Code == http.StatusBadRequest {
		if msg := w4.Body.String(); strings.Contains(msg, "格式错误") || strings.Contains(msg, "请提供源文本") {
			t.Fatalf("合法请求被校验分支错挡（说明两条判据写反了）: body=%s", msg)
		}
	}

	// 反向对照：未登录仍是 401（拆分不许把鉴权腿挪到解码之后，那是把"匿名可探测建单参数"当成默认）
	w5 := postJSON(t, s.handleTicketCreate, "/api/tickets/create", "", `{"source_text":"x"}`)
	if w5.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实得 %d body=%s ⇒ 鉴权被排到了解码之后", w5.Code, w5.Body.String())
	}
}

// TestKBEntryAddErrorBranchesAreSeparated 知识库条目新增口同形态（admin_kb.go 的第二处）。
func TestKBEntryAddErrorBranchesAreSeparated(t *testing.T) {
	s, tok := f29Server(t)
	w := postJSON(t, s.handleKBEntryAdd, "/api/admin/kb/entry/add", tok, `{"package_id":1,"source_text":123}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("类型错误应 400，实得 %d body=%s", w.Code, w.Body.String())
	}
	if msg := mustErrMsg(t, w); !strings.Contains(msg, "格式错误") || strings.Contains(msg, "source_text 不能为空") {
		t.Fatalf("KB 新增口仍把两分支并成一句: %q", msg)
	}
	w2 := postJSON(t, s.handleKBEntryAdd, "/api/admin/kb/entry/add", tok, `{"package_id":1}`)
	if msg := mustErrMsg(t, w2); !strings.Contains(msg, "source_text 不能为空") || strings.Contains(msg, "格式错误") {
		t.Fatalf("KB 新增口空值分支不对: code=%d msg=%q", w2.Code, msg)
	}
}
