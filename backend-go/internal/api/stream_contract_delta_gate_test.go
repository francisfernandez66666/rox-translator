// ============ stream_contract_delta_gate_test.go 职责中文说明 ============
// 门禁 B1-2.2（DEF-P1-04，2026-11-01）：/api/chat/stream 的 **delta 帧不许带契约标签**。
//
// 与 engine 侧那批单测的分工：单测钉的是过滤器本身算得对不对，本文件钉的是
// 「过滤器真接在了这条链上、客户屏幕那一跳真的被洗过」——判据级代替不了守卫级。
//
// ⚠️ 桩只认协议（本仓老坑：按"我以为的契约"实现桩＝给缺陷发证件）：
// 帧形态逐字照 internal/llm.StreamChat 的解析代码写——
//
//	· `data: {"choices":[{"delta":{"content":"…"}}]}` 若干条；
//	· 必须有一条带 usage 的尾包，且 prompt/completion **不能同时为 0**（否则 StreamChat
//	  退 errNoStreamUsage ⇒ 引擎透明回退非流式 CallChat ⇒ 一个 delta 都不会发，
//	  本用例就会"因为没帧所以没泄漏"地空转）；
//	· 响应 Content-Type 必须含 text/event-stream（不含就被判"端点不支持流式"）。
//
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/iam"
	"translator/internal/store"
)

// deltaLeakRe 契约标签本体（大小写与 </t> 都算）。
var deltaLeakRe = regexp.MustCompile(`(?i)</?\s*t\b`)

// streamingChatHarness 建一套「假上游按 SSE 逐帧回指定内容」的对话面环境。
// 参数 frames: 上游 delta 帧的 content 序列（**按实测形态传**：<t 与 >正文 分在两帧）。
func streamingChatHarness(t *testing.T, frames []string) (*Server, string, *sql.DB) {
	t.Helper()
	pinSqliteDialect(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			// 非流式腿（降级／审校）：给整段拼接结果，保证 done.reply 也拿得到东西
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{{"message": map[string]string{"content": strings.Join(frames, "")}, "finish_reason": "stop"}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]interface{}{
				"choices": []map[string]interface{}{{"delta": map[string]string{"content": f}}},
			}))
		}
		// 尾包：finish_reason + usage（缺 usage 就被判"不支持流式"，整条 delta 腿根本不会走）
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(map[string]interface{}{
			"choices": []map[string]interface{}{{"delta": map[string]string{}, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "test-model" // 不含 Hunyuan ⇒ tryMainModel 才优先走流式腿
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())))
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("建测试 Store 失败: %v", err)
	}
	u, err := st.CreateUser(1, "uat_b1_delta_user", auth.PasswordHash("pw123456"), "契约泄漏探针", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("建测试用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发令牌失败: %v", err)
	}
	eng := engine.NewEngine(cfg, nil, nil, nil)
	eng.St = st
	return &Server{Store: st, Cfg: cfg, metrics: newMetrics(), Engine: eng}, tok, db
}

// postFastChat 直调 handleChatStream（fast 档：本用例只关心 delta 腿，不想把审校那条非流式腿卷进来）。
func postFastChat(t *testing.T, s *Server, tok, message string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"message": message,
		"options": map[string]interface{}{"target_langs": []string{"en"}, "mode": "fast", "lang": "zh"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/chat/stream", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChatStream(w, r)
	return w
}

// sseDeltaTexts 从 SSE 响应体里取出每一帧 delta 的 text（按真实现场那一种写法解析，
// 不靠"event: delta"这种本仓不存在的行）。
func sseDeltaTexts(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "" || data == "[DONE]" {
			continue
		}
		var fr struct {
			Type   string `json:"type"`
			Text   string `json:"text"`
			Result struct {
				Reply string `json:"reply"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(data), &fr) != nil {
			continue
		}
		if fr.Type == "delta" {
			out = append(out, fr.Text)
		}
	}
	return out
}

// TestDeltaFramesNeverCarryContractTags 主判据：实测拆分形态的上游回帧，delta 一帧都不许带标签。
// 三件一起断（缺任一件都可能"看起来对"）：
//
//	① 逐帧不含 <t / </t（泄漏本体）；
//	② 拼起来含正文（证明没因为"标签没闭合"就把正文一起吞掉——那是比泄漏更坏的静默形态）；
//	③ done.result.reply 非空且不含标签（**正向对照**：证明最终腿没被这次改动弄坏。
//	   没有③，过滤器写错把正文全吞了时 ① 与 ② 会双双"符合预期"）。
func TestDeltaFramesNeverCarryContractTags(t *testing.T) {
	// 现网实测帧序：第一帧 `<t`、第二帧 `>This`、全文没有闭合标签
	s, tok, db := streamingChatHarness(t, []string{"<t", ">This is", " the translated", " answer"})
	w := postFastChat(t, s, tok, "这是翻译后的答复")
	waitChatPersistDrained(t, db, "translated")
	body := w.Body.String()

	deltas := sseDeltaTexts(t, body)
	if len(deltas) == 0 {
		t.Fatalf("一条 delta 都没有 ⇒ 本用例空转（流式腿没被走到，看响应体找原因）：\n%s", body)
	}
	for i, d := range deltas {
		if deltaLeakRe.MatchString(d) {
			t.Errorf("第 %d 帧 delta 泄漏契约标签：%q（全部帧=%q）", i+1, d, strings.Join(deltas, "|"))
		}
	}
	joined := strings.Join(deltas, "")
	if norm := strings.Join(strings.Fields(joined), " "); !strings.Contains(norm, "This is the translated answer") {
		t.Errorf("delta 拼起来丢了正文（未闭合标签那一族必须全量透传）：%q", joined)
	}
	if !strings.Contains(body, `"type":"done"`) {
		t.Fatalf("没有 done 帧（上游桩或引擎链失败，判据③无法成立）：\n%s", body)
	}
	if fr := doneReply(t, body); fr == "" {
		t.Errorf("done.result.reply 为空 ⇒ 最终腿被改坏了（判据③）：\n%s", body)
	} else if deltaLeakRe.MatchString(fr) {
		t.Errorf("done.result.reply 含标签 ⇒ 最终腿 PostProcessTranslation 失效：%q", fr)
	}
}

// doneReply 取 done 帧的 result.reply。
func doneReply(t *testing.T, body string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		var fr struct {
			Type   string `json:"type"`
			Result struct {
				Reply string `json:"reply"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(data), &fr) == nil && fr.Type == "done" {
			return fr.Result.Reply
		}
	}
	return ""
}

// TestDeltaClosedContractKeepsBodyAndDropsComment 闭合契约块 + 块外注释：
// delta 只出正文，注释那一段不许进屏幕（与 extract 的白名单方向一致）。
func TestDeltaClosedContractKeepsBodyAndDropsComment(t *testing.T) {
	s, tok, db := streamingChatHarness(t, []string{"<t>Good", " morning</t>", "\n术语对照：xxx"})
	w := postFastChat(t, s, tok, "早上好")
	waitChatPersistDrained(t, db, "Good")
	deltas := sseDeltaTexts(t, w.Body.String())
	if len(deltas) == 0 {
		t.Fatalf("没有 delta ⇒ 空转：\n%s", w.Body.String())
	}
	joined := strings.Join(deltas, "")
	if !strings.Contains(joined, "Good morning") {
		t.Errorf("闭合契约的正文没出来：%q", joined)
	}
	if strings.Contains(joined, "术语对照") {
		t.Errorf("标签外的注释进了客户屏幕（方向应是与 extract 一致地丢弃）：%q", joined)
	}
	if deltaLeakRe.MatchString(joined) {
		t.Errorf("泄漏标签：%q", joined)
	}
}

// TestDeltaStreamHarnessActuallyStreams 桩的射程自证：如果假上游没被当成流式消费
// （usage 缺失／Content-Type 不对／模型名撞上 Hunyuan），引擎会透明回退非流式，
// 那时上面两条判据都会"因为没有 delta 帧而通过"。这一条把那条退路钉死。
//
// ⚠️ 靶子刻意用**不带标签**的两帧：带 `<t` 开头时过滤器合法地要扣住那两个字节的标签
// （那正是 B1 的修法），delta 会合成一帧——拿它当"流式没走"的证据就是把正确实现读成退路。
func TestDeltaStreamHarnessActuallyStreams(t *testing.T) {
	s, tok, db := streamingChatHarness(t, []string{"plain ", "body"})
	w := postFastChat(t, s, tok, "普通正文")
	waitChatPersistDrained(t, db, "plain")
	deltas := sseDeltaTexts(t, w.Body.String())
	if len(deltas) < 2 {
		t.Fatalf("流式腿没被真走到（delta 帧数=%d）⇒ 其余泄漏判据全部失去射程：\n%s", len(deltas), w.Body.String())
	}
	if raw := w.Body.String(); !strings.Contains(raw, `"type":"delta"`) {
		t.Fatalf("响应体里没有 delta 事件名（帧形态与本文件的解析口径不一致，桩需按 stream.go 的 sseEvent 重写）：\n%s", raw)
	}
}

// waitChatPersistDrained 排空 SSE 收尾后那条 **fire-and-forget 的持久化 goroutine**。
//
// 存在的理由（-race 实测抓到的，不是假想）：handleChatStream 返回后还有一条 goroutine 在写
// chat_conversations / chat_messages，那条腿里 store 会读全局方言（db.CurrentDialect ← config.C），
// 而包内任何测试的 pinSqliteDialect 都会在 cleanup 里**覆写同一个全局** ⇒ goroutine 活得比测试长
// 就是数据竞争（AGENTS §一·4 那条「config.Default() 副作用写全局 config.C」的老坑，
// 这里由异步腿放大：不是某个测试写错，是"谁都得等到它走完"）。
// 等待的证据取**那一行真的落库了**（不是 sleep 撞运气）；等不到只报读数不判红——
// 本文件的靶子是 delta 帧，持久化只是必须先排空的副作用。
func waitChatPersistDrained(t *testing.T, db *sql.DB, contain string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM chat_messages WHERE content LIKE ?", "%"+contain+"%").Scan(&n); err == nil && n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Logf("chat_messages 里没等到含 %q 的那一行（持久化腿可能被跳过：无 convID / 无用户）", contain)
}
