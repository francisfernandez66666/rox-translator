// ============================================================================
// chat_usability_face_test.go — P0-01 SSE 出口回归锁（2026-10-26）
//
// 引擎单测（internal/engine/chat_usability_test.go）锁的是“引擎内部会把 and 换掉”；
// 本文件锁的是**客户真正拿到的 SSE 帧**：done 帧里的 translations.en 不许再是 "and"，
// 且最终译文是极简腿救回的正确长译文。
//
// 假上游按现网形态分档：system 里带「币种保真」尾注 ⇒ 回 "and"；
// 极简 system（无尾注）⇒ 回正确长译文。这样一次请求就能复现 UAT V1 的完整链路。
//
// 方言口径（AGENTS §一·4）：自钉 sqlite 并恢复 config.C。
// ============================================================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/iam"
	"translator/internal/store"
)

// chatUsabilityFaceSrc 与 UAT V1 同形：含「第N段」结构标签。
const chatUsabilityFaceSrc = "这是一段用于端到端验收的中文原文。第二段：请翻译成英文并保留段落结构。第三段：金额与币种必须准确呈现。"

// chatUsabilityFaceGood 不含 translate/translation 等指令词，避免指令回显判据干扰。
const chatUsabilityFaceGood = "This is a Chinese original text for end-to-end acceptance. Paragraph two describes the paragraph structure. Paragraph three requires accurate amounts and currencies."

// chatUsabilityFaceHarness 起假上游 + 内存 Store + 已登录用户。
func chatUsabilityFaceHarness(t *testing.T) (*Server, string, *int64) {
	t.Helper()
	pinSqliteDialect(t)

	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		sys := ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				sys = m.Content
			}
		}
		reply := "and"
		// 现网形态：正常档 system 里有「币种保真」尾注 ⇒ 纯翻译模型退化成 and；
		// 极简档没有这条尾注 ⇒ 正确长译文。
		if sys != "" && !strings.Contains(sys, "币种保真") {
			reply = chatUsabilityFaceGood
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"content":%q},"finish_reason":"stop"}]}`, reply)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "sk-test"
	cfg.OnlineModel = "test-model"
	cfg.HunyuanFallbackModel = "fallback/Model"
	config.C = cfg

	db, err := sql.Open("sqlite", "file:chat_usability_face?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	u, err := st.CreateUser(1, "chat_usability_face_user", auth.PasswordHash("pw123456"), "可用性门面探针", iam.RoleUser, 0, 0)
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

// TestChatStreamNeverDeliversDegenerateAnd 直调 handleChatStream，断言 done 帧里
// 不出现 `"en":"and"`，而是极简腿救回的正确长译文。
func TestChatStreamNeverDeliversDegenerateAnd(t *testing.T) {
	s, tok, hits := chatUsabilityFaceHarness(t)
	body, _ := json.Marshal(map[string]interface{}{
		"message": chatUsabilityFaceSrc,
		"options": map[string]interface{}{"target_langs": []string{"en"}, "mode": "fast", "lang": "zh"},
	})
	r := httptest.NewRequest(http.MethodPost, "/api/chat/stream", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChatStream(w, r)
	resp := w.Body.String()

	if atomic.LoadInt64(hits) == 0 {
		t.Fatal("假上游一次都没被打：请求没走到模型链路，本用例会在离线态假绿")
	}
	if !strings.Contains(resp, `"type":"done"`) {
		t.Fatalf("应发 done 帧（极简腿救回成功），实际响应：\n%s", resp)
	}
	if strings.Contains(resp, `"en":"and"`) {
		t.Fatalf("done 帧仍交付了退化译文 en=and：\n%s", resp)
	}
	if !strings.Contains(resp, "This is a Chinese original text for end-to-end acceptance") {
		t.Fatalf("done 帧没有极简腿救回的正确译文：\n%s", resp)
	}
	// 这条负向锁防止“空壳成功”被误判通过：done 帧必须真的带非空译文。
	if strings.Contains(resp, `"translations":{"en":""}`) {
		t.Fatalf("done 帧译文为空（空壳成功形态）：\n%s", resp)
	}
}
