// ============ sales_facts_verify_test.go · 职责说明 ============
// 三期 W2（§0.4 第②段）：assist 侧事实集验签的行为锁（照 profile_verify_test.go 族形）。
//
//	A) 正确签名 + 回环对端 + 窗口内 + 版本位匹配 ⇒ 事实集生效并进 ctx；
//	B) 错签 / 过期 / 非回环 / 缺头 / 错版本 / 未配密钥 ⇒ 一律 ok=false（ctx 里什么都没有，
//	   由 W4 守卫按「无事实头」fail-closed 回避），且不返 4xx——身份腿坏了不许把挂件打死。
//
// ================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	mainengine "translator/internal/engine"
)

// salesFactsHeaders 用给定密钥/时间戳/版本生成本进程口径的四个头。
func salesFactsHeaders(t *testing.T, key string, set mainengine.SalesFactsSet, ts int64, version string) map[string]string {
	t.Helper()
	payload, err := mainengine.EncodeSalesFacts(set)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return map[string]string{
		mainengine.SalesFactsHeader:        payload,
		mainengine.SalesFactsTsHeader:      strconv.FormatInt(ts, 10),
		mainengine.SalesFactsSigHeader:     mainengine.SignSalesFacts([]byte(key), payload, ts),
		mainengine.SalesFactsVersionHeader: version,
	}
}

func TestSalesFactsAccepted(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "verify-key")
	s := newProfileVerifyServer(t)
	set := mainengine.SalesFactsSet{TenantID: 7, Lang: "zh", Facts: []mainengine.SalesFact{{Kind: "spec", Key: "warranty", Value: "30 天", Lang: "zh"}}}
	r := withHeaders(salesFactsHeaders(t, "verify-key", set, time.Now().Unix(), mainengine.SalesFactsVersion))
	got, ok := s.salesFactsFromRequest(r)
	if !ok || got.TenantID != 7 || len(got.Facts) != 1 || got.Facts[0].Value != "30 天" {
		t.Fatalf("正确签名应生效，实际 ok=%v %+v", ok, got)
	}
	ctx := s.injectSalesFacts(r.Context(), r)
	fromCtx, ok := mainengine.SalesFactsFromContext(ctx)
	if !ok || fromCtx.TenantID != 7 {
		t.Fatalf("事实集未进 ctx（W4 守卫将无判据）：ok=%v", ok)
	}
}

func TestSalesFactsFailClosed(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "verify-key")
	s := newProfileVerifyServer(t)
	set := mainengine.SalesFactsSet{TenantID: 7, Lang: "zh"}
	now := time.Now().Unix()
	cases := []struct {
		name string
		hdr  map[string]string
		peer string
	}{
		{"错签", salesFactsHeaders(t, "wrong-key", set, now, mainengine.SalesFactsVersion), "127.0.0.1:1"},
		{"过期", salesFactsHeaders(t, "verify-key", set, now-120, mainengine.SalesFactsVersion), "127.0.0.1:1"},
		{"非回环", salesFactsHeaders(t, "verify-key", set, now, mainengine.SalesFactsVersion), "10.9.8.7:5555"},
		{"错版本", salesFactsHeaders(t, "verify-key", set, now, "999"), "127.0.0.1:1"},
		{"缺头", nil, "127.0.0.1:1"},
	}
	for _, c := range cases {
		r := withHeaders(c.hdr)
		r.RemoteAddr = c.peer
		if _, ok := s.salesFactsFromRequest(r); ok {
			t.Fatalf("%s：必须 fail-closed（ok=false），实际放行", c.name)
		}
		ctx := s.injectSalesFacts(r.Context(), r)
		if _, ok := mainengine.SalesFactsFromContext(ctx); ok {
			t.Fatalf("%s：ctx 里不许有事实集", c.name)
		}
	}
}

func TestSalesFactsNoKeyConfigured(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "")
	s := newProfileVerifyServer(t)
	set := mainengine.SalesFactsSet{TenantID: 7, Lang: "zh"}
	r := withHeaders(salesFactsHeaders(t, "verify-key", set, time.Now().Unix(), mainengine.SalesFactsVersion))
	if _, ok := s.salesFactsFromRequest(r); ok {
		t.Fatal("未配密钥必须 fail-closed")
	}
}

// TestSalesFactsBadHeaderDoesNotBreakChat 错签不得把挂件打成 4xx（P-5 同族：身份腿坏了照常回答）。
func TestSalesFactsBadHeaderDoesNotBreakChat(t *testing.T) {
	t.Setenv(assistProfileKeyEnv, "verify-key")
	s := newProfileVerifyServer(t)
	w := httptest.NewRecorder()
	s.handleGreeting(w, httptest.NewRequest(http.MethodGet, "/api/assist/greeting?page=/", nil))
	if w.Code != 200 {
		t.Fatalf("greeting 应 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var g map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatalf("greeting 响应非 JSON: %v", err)
	}
	sid, _ := g["session"].(string)
	tok, _ := g["tok"].(string)
	if sid == "" || tok == "" {
		t.Fatalf("greeting 未下发 session/tok: %s", w.Body.String())
	}
	body := `{"session":"` + sid + `","tok":"` + tok + `","message":"你好"}`
	r := httptest.NewRequest(http.MethodPost, "/api/assist/chat", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:43210"
	for k, val := range salesFactsHeaders(t, "bad-key", mainengine.SalesFactsSet{TenantID: 7, Lang: "zh"}, time.Now().Unix(), mainengine.SalesFactsVersion) {
		r.Header.Set(k, val)
	}
	w2 := httptest.NewRecorder()
	s.handleChat(w2, r)
	if w2.Code != 200 {
		t.Fatalf("错签必须照常回答（不返 4xx），实际 %d: %s", w2.Code, w2.Body.String())
	}
	if !strings.Contains(w2.Body.String(), `"reply"`) {
		t.Fatalf("错签时仍应有正常回复体，实际 %s", w2.Body.String())
	}
	// W2 归因段必须随响应回吐（第③段的输入）；pending_human 在 W2 恒 false（守卫 W4 才挂）。
	if !strings.Contains(w2.Body.String(), `"attribution"`) || !strings.Contains(w2.Body.String(), `"message_id"`) {
		t.Fatalf("响应缺归因段（主服务回写将无输入）：%s", w2.Body.String())
	}
}
