// ============ 本文件职责中文说明 ============
// 2026-11-23 tier3 回译模型超管配置口（api/scrape_quality_config.go）的实现锁。
//
// 最坏形态不是"配不动"，而是"配了个未经回译实测的模型，语义闸静默失效"——
// 所以主判据是白名单：只有实测能把 he 正确回译中文的模型才落得了库。
//
// 五条腿：A 未配置回显代码缺省／B 白名单内保存即生效／C 非白名单拒收（脏值不落库）／
//
//	D reset 清空回缺省／E 鉴权 401（租户 admin 403 由共享闸保证，不在此重测）。
//
// ★ 单测自钉 sqlite 方言：复用 estCfgFixture 的 config.Default 处理。
// ==========================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sqCfgPath = "/api/admin/config/scrape-backtranslate"

func (s *Server) sqCfgMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc(sqCfgPath, s.handleAdminScrapeBacktranslate)
	return m
}

func sqCfgCall(t *testing.T, h http.Handler, method, body, tok string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, sqCfgPath, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func sqCfgStored(t *testing.T, f *estCfgFixture) string {
	t.Helper()
	v, _ := f.st.GetConfig(scrapeBacktranslateConfigKey)
	return strings.TrimSpace(v)
}

// A 腿：库里没表态 ⇒ 回显代码缺省，stored 为空（界面能看出"没配过"）。
func TestScrapeBacktranslateUnsetShowsDefault(t *testing.T) {
	f := newEstCfgFixture(t)
	code, body := sqCfgCall(t, f.srv.sqCfgMux(), http.MethodGet, "", f.super)
	if code != 200 {
		t.Fatalf("GET 应 200，实得 %d: %v", code, body)
	}
	if body["model"] != defaultBacktranslateModel {
		t.Fatalf("未配置应回显缺省 %q，实得 %v", defaultBacktranslateModel, body["model"])
	}
	if body["stored"] != "" {
		t.Fatalf("未配置 stored 应为空，实得 %v", body["stored"])
	}
	allowed, ok := body["allowed"].([]interface{})
	if !ok || len(allowed) == 0 {
		t.Fatalf("回显缺白名单 allowed: %v", body)
	}
}

// B 腿：白名单内保存即时生效（GET 现读库，不等重启）。
func TestScrapeBacktranslateSaveEffective(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.sqCfgMux()
	code, body := sqCfgCall(t, h, http.MethodPost,
		`{"model":"tencent/Hunyuan-MT-7B"}`, f.super)
	if code != 200 || body["model"] != "tencent/Hunyuan-MT-7B" {
		t.Fatalf("保存应 200 且生效，实得 %d %v", code, body)
	}
	if sqCfgStored(t, f) != "tencent/Hunyuan-MT-7B" {
		t.Fatalf("库内原值未落库: %q", sqCfgStored(t, f))
	}
	code, body = sqCfgCall(t, h, http.MethodGet, "", f.super)
	if code != 200 || body["model"] != "tencent/Hunyuan-MT-7B" {
		t.Fatalf("GET 应现读到已保存值，实得 %d %v", code, body)
	}
}

// C 腿：非白名单模型（哪怕是真的 DeepSeek-V3）一律拒收，库里不许留下脏值。
func TestScrapeBacktranslateRejectsNonWhitelisted(t *testing.T) {
	f := newEstCfgFixture(t)
	for _, bad := range []string{"deepseek-ai/DeepSeek-V3", "THUDM/GLM-Z1-9B-0414", ""} {
		bodyJSON, _ := json.Marshal(map[string]string{"model": bad})
		code, body := sqCfgCall(t, f.srv.sqCfgMux(), http.MethodPost, string(bodyJSON), f.super)
		if code == 200 {
			t.Fatalf("模型 %q 不该被接受，实得 200: %v", bad, body)
		}
		if sqCfgStored(t, f) != "" {
			t.Fatalf("脏模型 %q 被写进库: %q", bad, sqCfgStored(t, f))
		}
	}
}

// D 腿：reset 把键清空 ⇒ 回显回落代码缺省，与"从未配过"同形。
func TestScrapeBacktranslateReset(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.sqCfgMux()
	_, _ = sqCfgCall(t, h, http.MethodPost, `{"model":"tencent/Hunyuan-MT-7B"}`, f.super)
	code, body := sqCfgCall(t, h, http.MethodPost, `{"reset":true}`, f.super)
	if code != 200 || body["model"] != defaultBacktranslateModel {
		t.Fatalf("reset 应回落缺省，实得 %d %v", code, body)
	}
	if sqCfgStored(t, f) != "" {
		t.Fatalf("reset 后库内应清空，实得 %q", sqCfgStored(t, f))
	}
}

// E 腿：未登录 401。
func TestScrapeBacktranslateRequiresAuth(t *testing.T) {
	f := newEstCfgFixture(t)
	code, _ := sqCfgCall(t, f.srv.sqCfgMux(), http.MethodGet, "", "")
	if code != 401 {
		t.Fatalf("未登录 GET 应 401，实得 %d", code)
	}
	code, _ = sqCfgCall(t, f.srv.sqCfgMux(), http.MethodPost, `{"model":"tencent/Hunyuan-MT-7B"}`, "")
	if code != 401 {
		t.Fatalf("未登录 POST 应 401，实得 %d", code)
	}
}
