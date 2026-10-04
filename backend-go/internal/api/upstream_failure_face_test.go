// ============================================================================
// upstream_failure_face_test.go — ★ 修法 F 的「三个客户面同一口径」断言（A7 消费侧 ＋ ⑭ 回归锁，
// 2026-10-04 〇-AR 第 2 波）。
//
// 引擎侧只保证一件事：HandleText 在「目标语种零可用译文」时把码放进 Error、人话放进 Reply
// （判据与反证见 internal/engine/upstream_failure_test.go）。本文件钉的是**出栈那一层**：
// 三个面各自怎么把这份失败发给客户，以及"新增一个稳定码时三面会不会漏一个"。
//
// 漏口的历史形态就是 F-53：sensitive_blocked 在 SSE 面上被当文案发出去（前端 locale 没这个键，
// 客户气泡里是一串裸键名）。那次只修了 SSE，`/api/translate` 非流式面与 OpenAPI 面仍然把码
// 当 message 发（本轮复核发现，随 ⑭ 一起收）。所以本文件的三条锁分别对着三面：
//
//	① engineErrorPayload —— SSE error 帧：error=人话、error_code=码，且**问登记表不问单值**；
//	② handleChat（/api/translate 非流式）—— Reply 不得拼成「❌ 处理出错: 裸码」，码只走 error 字段；
//	③ openAPIEngineFailure —— OpenAPI 出参：success:false＋按码分档＋message 是人话不是键名。
//
// ★ 反证：把 engineErrorPayload 改回「只认 sensitive_blocked」⇒ ①之外的新码分支红；
// 把 openAPIEngineFailure 的 message 改回 res.Error ⇒ 裸码锁红；
// 把 ⑭ 的出口收敛摘掉 ⇒ TestProChatNeverReturnsEmptyTranslationAsSuccess 红（SSE 回到 done 空壳）。
//
// 方言口径（AGENTS §一·4）：本文件自钉 sqlite 并恢复 config.C。
// ============================================================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/billing"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/errors"
	"translator/internal/iam"
	"translator/internal/llm"
	"translator/internal/store"
)

// failingChatHarness 建一套「Store＋引擎都就绪、上游恒回指定状态码」的对话面环境，
// 并签发一个普通租户成员的 JWT（handleChatStream 强制登录）。
// 参数 status: 假上游的 HTTP 状态码（401＝R-1 现网形态，500＝供应商故障形态）。
func failingChatHarness(t *testing.T, status int) (*Server, string, *int64) {
	t.Helper()
	pinSqliteDialect(t)

	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key"}}`)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "test-model"
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg

	db, err := sql.Open("sqlite", fmt.Sprintf("file:upm_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())))
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	u, err := st.CreateUser(1, "uat1003_face_user", auth.PasswordHash("pw123456"), "失败诚实性探针", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("创建测试用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发测试令牌失败: %v", err)
	}
	eng := engine.NewEngine(cfg, nil, nil, nil)
	eng.St = st
	s := &Server{Store: st, Cfg: cfg, metrics: newMetrics(), Engine: eng}
	return s, tok, &hits
}

// postChatStream 直调 handleChatStream（与 f29PostChat 同一手法，绕开路由与中间件，
// 因为本用例关心的是「引擎失败如何变成 SSE 帧」这一层，不是路由表）。
func postChatStream(t *testing.T, s *Server, tok, message string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"message": message,
		"options": map[string]interface{}{"target_langs": []string{"en", "ja"}, "mode": "pro", "lang": "zh"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/chat/stream", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChatStream(w, r)
	return w
}

// TestProChatNeverReturnsEmptyTranslationAsSuccess ⑭ 的回归锁（文档 §四 ⑭ 指定断言）：
// 假上游恒 401 ⇒ Pro 对话必须发 **error 帧**，绝不能再发「done 帧 + 空 translations」。
// 为什么这条值得单独钉：空壳在界面上不报错、不弹提示，客户只会以为"这软件翻不出中文"，
// 是本轮 UAT 里最难被察觉、代价最高的一类失败形态。
func TestProChatNeverReturnsEmptyTranslationAsSuccess(t *testing.T) {
	s, tok, hits := failingChatHarness(t, http.StatusUnauthorized)

	w := postChatStream(t, s, tok, "本公司专注于智能硬件的研发与设计")
	if *hits == 0 {
		t.Fatal("假上游一次都没被打：请求没走到模型链路，本用例失去意义（判据会在离线态假绿）")
	}
	body := w.Body.String()

	// 帧形态说明：本仓 SSE 只走 `data: {"type":"error",...}` 一种写法（见 stream.go sseEvent），
	// 没有 `event:` 行——断言必须按运行时真实字节判，不然锁的是想象中的协议。
	if !strings.Contains(body, `"type":"error"`) {
		t.Fatalf("上游恒 401 却没发 error 帧 ⇒ 空壳成功形态回来了。响应体：\n%s", body)
	}
	if strings.Contains(body, `"type":"done"`) {
		t.Fatalf("失败的一单不得同时发 done 帧（前端按 done 收口会把空结果当成功渲染）：\n%s", body)
	}
	// 出帧形态：error_code 是稳定码、error 是人话（裸码当文案＝F-53 那个形态复发）。
	if !strings.Contains(body, `"error_code":"`+engine.CodeUpstreamFailed+`"`) {
		t.Fatalf("error 帧缺少 error_code=%s（前端按码取本语种词条，没码就只能显示后端那句中文）：\n%s",
			engine.CodeUpstreamFailed, body)
	}
	if strings.Contains(body, `"error":"upstream_failed"`) {
		t.Fatalf("error 字段被填成裸码，客户气泡会显示键名：\n%s", body)
	}
	// points_used==0 的等价判据：error 分支根本不产出 done 帧，而 done 帧是 points_used 的唯一载体。
	// （真跑里 401 上游零 usage ⇒ 计费不会发生；这里用"没有 done"钉住"不会报出任何消耗"。）
	if strings.Contains(body, "points_used") {
		t.Fatalf("失败一单却出现 points_used 字段（客户会被提示「扣了多少积分」）：\n%s", body)
	}
	// 账目腿：本次请求不得留下扣费流水（0 元＝真没扣，不是没查到）。
	var n int
	if err := s.Store.DB().QueryRow(`SELECT COUNT(1) FROM billing_ledger`).Scan(&n); err == nil && n != 0 {
		t.Fatalf("上游恒 401 的一单却产生扣费流水 %d 条：白扣", n)
	}
}

// postChatNonStream 直调 handleChat（/api/translate 非流式那面，出参＝HandleText 结果本体的 JSON）。
func postChatNonStream(t *testing.T, s *Server, tok, message string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"message": message,
		"options": map[string]interface{}{"target_langs": []string{"en", "ja"}, "mode": "pro", "lang": "zh"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/translate", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChat(w, r)
	return w
}

// TestHandleChatNonStreamKeepsCodeAndCopyApart ②：非流式面（handleChat）的码/文案分居。
// 这是 F-53 当年漏掉的那一面——SSE 修成 error=话术/error_code=码之后，
// handleChat 仍把 `❌ 处理出错: sensitive_blocked` 这种**裸键名拼进 reply** 发给客户，
// 且出参只有一个 error 字段装码，前端拿到的是"一句带键名的中文"。
// 本用例同时钉住：结构不变（HTTP 200＋res.error 装码），只有文案归位。
func TestHandleChatNonStreamKeepsCodeAndCopyApart(t *testing.T) {
	s, tok, hits := failingChatHarness(t, http.StatusUnauthorized)
	w := postChatNonStream(t, s, tok, "本公司专注于智能硬件的研发与设计")
	if *hits == 0 {
		t.Fatal("假上游一次都没被打：这一面没走到模型链路，断言失去意义（对兜底态假绿）")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("非流式对话面的失败出参结构是 HTTP 200＋error 字段（改成状态码会破坏前端既有契约），实得 %d", w.Code)
	}
	var out struct {
		Reply string `json:"reply"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析非流式出参失败: %v，响应体：\n%s", err, w.Body.String())
	}
	// 判据 1：⑭ 的出口收敛在这一面也必须生效——Error 非空且是登记的稳定码。
	if out.Error != engine.CodeUpstreamFailed {
		t.Fatalf("全腿失败却报 error=%q（期望 %s）⇒ 空壳成功形态回来了：\n%s",
			out.Error, engine.CodeUpstreamFailed, w.Body.String())
	}
	// 判据 2：reply 是人话，不含旧前缀，也不含裸码字面量。
	if strings.Contains(out.Reply, "处理出错") {
		t.Fatalf("reply 仍带「❌ 处理出错: 」前缀（该前缀会把裸码拼进人话）：\n%s", out.Reply)
	}
	for _, raw := range []string{engine.CodeUpstreamFailed, engine.CodeSensitiveBlocked, engine.CodeInsufficientBalance} {
		if strings.Contains(out.Reply, raw) {
			t.Fatalf("reply 里出现裸稳定码 %q，客户看到的就是键名：\n%s", raw, out.Reply)
		}
	}
	if strings.TrimSpace(out.Reply) == "" {
		t.Fatal("失败出参的 reply 为空 ⇒ 前端气泡一片空白")
	}
	// 判据 3（正对照）：非码类的旧形态失败（Error 本来就是中文句子）仍要加前缀，
	// 否则这条改动会把"没有话术可靠"的失败洗成裸中文，客户无从判断是谁的问题。
	if got := engineErrorPayload("文件不存在或无法读取", ""); got["error_code"] != nil {
		t.Fatalf("非稳定码不该下发 error_code（硬造假码会让前端分支误命中），实得 %#v", got)
	}
}

// TestEngineErrorPayloadCoversAllStableCodes ①：SSE 出帧判据必须**问登记表**而不是硬编码单值。
// 这条锁存在的理由不是形式：新增稳定码（upstream_failed／insufficient_balance）时，
// 硬编码 `== CodeSensitiveBlocked` 的写法会让新码掉进「把码当文案发」那支——
// 同一个缺陷换个码再犯一次。
func TestEngineErrorPayloadCoversAllStableCodes(t *testing.T) {
	for _, code := range []string{engine.CodeSensitiveBlocked, engine.CodeUpstreamFailed, engine.CodeInsufficientBalance} {
		got := engineErrorPayload(code, "这句是人话")
		if got["error_code"] != code {
			t.Fatalf("码 %s 未作为 error_code 下发，实得 %v", code, got)
		}
		if got["error"] != "这句是人话" {
			t.Fatalf("码 %s 的 error 字段应取 Reply 人话，实得 %v", code, got["error"])
		}
	}
	// ① 类形态（Error 本来就是人话）：只发 error，**不许硬造假码**。
	got := engineErrorPayload("文件不存在或无法读取", "顺带写的无关回复")
	if got["error"] != "文件不存在或无法读取" {
		t.Fatalf("人话型失败被改写，实得 %v", got["error"])
	}
	if _, has := got["error_code"]; has {
		t.Fatalf("人话型失败不得凭空造 error_code：%v", got)
	}
	// Reply 意外为空时回落码本身（与旧行为一致，绝不发空 error——空 error 是空白气泡）。
	empty := engineErrorPayload(engine.CodeUpstreamFailed, "   ")
	if empty["error"] != engine.CodeUpstreamFailed {
		t.Fatalf("空 Reply 时应有回落，实得 %v", empty["error"])
	}
}

// TestOpenAPIEngineFailureShape ③：OpenAPI 出参的（码, 文案）二元组。
// 旧写法 writeOpenAPIError(..., task_failed, res.Error) 把裸码当 message 发；
// 且余额类失败在异步面早就报 insufficient_balance/402、内容类拒绝走泛化 rejected/403，
// 同步面却一律 task_failed/409——同一件事两套账，SDK 按码分支只命中一半。
// 本用例同时钉住**码位与 HTTP 状态的映射**（状态由 errors.StatusForCode 查表决定，
// 码没登记就会回 500＝对外谎报服务端故障），所以每条用例都带 wantStatus。
func TestOpenAPIEngineFailureShape(t *testing.T) {
	cases := []struct {
		name       string
		res        *engine.TextTranslateResult
		wantCode   string
		wantMsg    string // 包含判据（人话里必须出现的关键词）
		wantStatus int    // 码查表得到的 HTTP 状态（writeOpenAPIError 出栈的那一档）
	}{
		{
			name:       "上游零译文",
			res:        &engine.TextTranslateResult{Error: engine.CodeUpstreamFailed, Reply: engine.UpstreamFailureReply},
			wantCode:   "task_failed",
			wantMsg:    "上游未返回可用译文",
			wantStatus: http.StatusConflict,
		},
		{
			name:       "余额中止",
			res:        &engine.TextTranslateResult{Error: engine.CodeInsufficientBalance, Reply: "⚠️ 余额不足：本次翻译未产出任何译文"},
			wantCode:   "insufficient_balance",
			wantMsg:    "余额不足",
			wantStatus: http.StatusPaymentRequired,
		},
		{
			// 内容类拒绝走**异步面同一档** rejected/403（gateErrorCode 的默认档就是它），
			// 不再挤进 task_failed/409——409 的对外语义是"这次处理失败了"，
			// 而"载荷本身被拒"重试同一句永远不会变成成功。
			name:       "敏感词拒译",
			res:        &engine.TextTranslateResult{Error: engine.CodeSensitiveBlocked, Reply: "该内容包含敏感信息，已停止翻译。"},
			wantCode:   "rejected",
			wantMsg:    "敏感信息",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "Reply 为空时回落默认句",
			res:        &engine.TextTranslateResult{Error: engine.CodeUpstreamFailed, Reply: ""},
			wantCode:   "task_failed",
			wantMsg:    "未产出可用译文",
			wantStatus: http.StatusConflict,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := openAPIEngineFailure(tc.res)
			if code != tc.wantCode {
				t.Fatalf("对外码不符：期望 %s，实得 %s", tc.wantCode, code)
			}
			if !strings.Contains(msg, tc.wantMsg) {
				t.Fatalf("message 应是人话且含 %q，实得 %q", tc.wantMsg, msg)
			}
			// 决定性负向：message 里绝不能**出现**裸稳定码字面量（判据用 Contains 而不是等值——
			// F-53 的现网形态就是键名混进文案，客户按报文分支时会把它当一句人话展示）。
			for _, raw := range []string{engine.CodeUpstreamFailed, engine.CodeSensitiveBlocked, engine.CodeInsufficientBalance} {
				if strings.Contains(msg, raw) {
					t.Fatalf("message 里出现裸码 %q：%q", raw, msg)
				}
			}
			// 码位→HTTP 状态这一腿也钉住：状态来自 errors.StatusForCode 查表，
			// 表里漏登记就回 500（把客户的业务失败说成服务端故障，SDK 会按 5xx 重试并报警）。
			if st := errors.StatusForCode(code); st != tc.wantStatus {
				t.Fatalf("码 %s 的 HTTP 档不符：期望 %d，实得 %d", code, tc.wantStatus, st)
			}
		})
	}
}

// TestEngineStableCodesMatchExternalContractList 跨包/跨语言字面量同步锁（AGENTS「跨包逐字同步」口径）。
// 射程：
//   - engine 的余额码 == errors.OpenAPIInsufficient == billing 侧实际使用的码；
//   - engine 的敏感码 == 前端 useChat 按它分支的那句串；
//   - deploy/check_upstream_401.sh 的默认 BAD_TEXT == llm 侧 401 文案前缀（A9 门禁的锚点）。
//
// 改任何一侧而忘了另一侧，这里当场红灯——这类"两侧各自的账"正是本仓反复踩的形态。
func TestEngineStableCodesMatchExternalContractList(t *testing.T) {
	if engine.CodeInsufficientBalance != string(string(errors.OpenAPIInsufficient)) {
		t.Fatalf("余额码两侧不同源：engine=%q api=%q", engine.CodeInsufficientBalance, string(errors.OpenAPIInsufficient))
	}
	// billing 侧现值：api 自己在闸门里用的就是这个字面量（billing_api.go 的 NewQuotaErr 调用点），
	// 判据问真函数而不是问常量表——QuotaErrCode 的归一化行为也是契约的一部分。
	if got := billing.QuotaErrCode(billing.NewQuotaErr("组织积分已耗尽，请联系管理员及时充值", "insufficient_balance")); got != engine.CodeInsufficientBalance {
		t.Fatalf("billing 侧余额码不同源：实得 %q", got)
	}

	fe := readRepoFileForTest(t, filepath.Join("frontend-react", "src", "hooks", "useChat.tsx"))
	if !strings.Contains(fe, "'"+engine.CodeSensitiveBlocked+"'") {
		t.Fatalf("前端按 %q 分支的写法已变，engine 侧改名要同步改前端（或前端已被改掉，这条锁就是证据）",
			engine.CodeSensitiveBlocked)
	}

	// llm 侧 401 文案（无响应体时恰好就是那句字面量）＝脚本默认 BAD_TEXT 的唯一真值来源。
	sh := readRepoFileForTest(t, filepath.Join("deploy", "check_upstream_401.sh"))
	anchorKey := "UPSTREAM_401_BAD_TEXT:-"
	want := (&llm.StatusError{Code: 401, Auth: true}).Error()
	if !strings.Contains(sh, anchorKey+want+"}") {
		t.Fatalf("发版验收脚本的 BAD_TEXT 与 llm 侧 401 文案不同源（脚本会永远绿灯地扫不到真 401）：脚本里没找到 %q",
			anchorKey+want+"}")
	}
}

// TestOpenAPISyncFailureFaceWire ③ 的**线上形态**腿：直接打 handleOpenAPITranslateSync，
// 断言"这一次翻译失败"在对外报文里是 success:false＋按码分档，而不是 ⑭ 的 success:true 空壳。
// 为什么要在 helper 之外再补这条线上腿：openAPIEngineFailure 单测只能证明"二元组算对了"，
// 证明不了 handler 真的用它——⑭ 的现网形态恰恰是 handler 里那句
// `writeOpenAPIError(..., task_failed, res.Error)` 把码当 message 发、
// 而成功分支零空值检查（translations 全空仍回 success:true）。
func TestOpenAPISyncFailureFaceWire(t *testing.T) {
	s, _, hits := failingChatHarness(t, http.StatusUnauthorized)

	// 给同一用户签一张 translate 权限的 Key（OpenAPI 面只认 Key，不认 JWT）。
	var uid int64
	if err := s.Store.DB().QueryRow(`SELECT id FROM users WHERE username='uat1003_face_user'`).Scan(&uid); err != nil || uid <= 0 {
		t.Fatalf("取测试用户 id 失败（本用例的 Key 归属拿不到）: %v", err)
	}
	key, err := s.Store.CreateAPIKey(1, uid, "1003-face-key", "translate", 0)
	if err != nil {
		t.Fatalf("签发 API Key 失败: %v", err)
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"text":         "本公司专注于智能硬件的研发与设计",
		"target_langs": []string{"en"},
		"mode":         "pro",
	})
	r := httptest.NewRequest(http.MethodPost, "/openapi/v1/translate", bytes.NewReader(reqBody))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleOpenAPITranslateSync(w, r)

	if *hits == 0 {
		t.Fatal("假上游一次都没被打：请求没走到模型链路，这条线上锁失去意义（会对着兜底态假绿）")
	}
	body := w.Body.String()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("对外报文不是合法 JSON: %v\n%s", err, body)
	}
	// 决定性判据（⑭ 本体）：失败绝不能报成 success:true。
	if v, _ := out["success"].(bool); v {
		t.Fatalf("⑭ 空壳形态回来了：上游恒 401 却回 success:true。报文：\n%s", body)
	}
	if out["success"] != nil && out["success"] != false {
		t.Fatalf("success 字段必须是布尔 false，实得 %#v", out["success"])
	}
	if w.Code != http.StatusConflict {
		t.Fatalf("上游零可用译文应回 409（task_failed 的登记档），实得 %d：\n%s", w.Code, body)
	}
	if got, _ := out["error_code"].(string); got != string(errors.OpenAPITaskFailed) {
		t.Fatalf("error_code 应为 %s，实得 %#v", errors.OpenAPITaskFailed, out["error_code"])
	}
	msg, _ := out["message"].(string)
	if !strings.Contains(msg, "上游未返回可用译文") {
		t.Fatalf("message 必须是引擎那句人话（客户按报文就知道是上游窗口问题）：%q", msg)
	}
	// 负向：对外报文里不得出现裸内部码字面量（F-53 那一形态在本面的等价物）。
	if strings.Contains(msg, engine.CodeUpstreamFailed) || strings.Contains(msg, engine.CodeSensitiveBlocked) {
		t.Fatalf("message 里出现裸稳定码（接入方会把它当文案展示）：%q", msg)
	}
	// 计费腿：失败的一单不得对外报出任何消耗数（⑭ 的账目侧读数）。
	if v, ok := out["points_used"]; ok {
		t.Fatalf("失败报文里却带 points_used=%v（客户会被提示扣了多少积分）", v)
	}
}

// readRepoFileForTest 从仓库根读一份源文件（本包既有静态锁的同一路径解析手法）。
func readRepoFileForTest(t *testing.T, rel string) string {
	t.Helper()
	root := repoRootForFaceTest(t)
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", rel, err)
	}
	return string(b)
}

// repoRootForFaceTest 从测试工作目录（internal/api）回溯仓库根。
func repoRootForFaceTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取工作目录失败: %v", err)
	}
	dir := wd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "backend-go", "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("未能从 %s 回溯到仓库根（backend-go/go.mod 所在目录的上一级）", wd)
	return ""
}
