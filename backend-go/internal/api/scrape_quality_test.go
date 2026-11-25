// ============ scrape_quality_test.go · 职责说明 ============
// api 语义闸适配层回归：JSON/阈值、真实 Judge 调用与平台成本 usage 钩子。
// 判据本体（HY 回译 + evals 打分）的测试在 internal/qualityjudge 包。
// 真调用用例使用本地假上游与内存 SQLite，不访问外网或生产库。
// ============================================================
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"translator/internal/config"
	"translator/internal/engine"
	"translator/internal/llm"
	"translator/internal/llmsource"
	"translator/internal/qualityjudge"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

func TestExtractQualityJSON(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		hasObj bool
	}{
		{`{"term":80,"semantic":90}`, `{"term":80,"semantic":90}`, true},
		{"```json\n{\"term\":0,\"semantic\":30}\n```", `{"term":0,"semantic":30}`, true},
		{"打分：{\"term\":50,\"semantic\":60} 以上", `{"term":50,"semantic":60}`, true},
		{"我不确定", "我不确定", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got := extractQualityJSON(tc.in)
		if !tc.hasObj && strings.Contains(got, "{") {
			t.Fatalf("输入 %q 不该有 JSON 对象，却得到 %q", tc.in, got)
		}
		if tc.hasObj && got != tc.want {
			t.Fatalf("输入 %q: got=%q want=%q", tc.in, got, tc.want)
		}
	}
}

type fakeQualityJudgeAPI struct {
	result qualityjudge.Result
	err    error
}

func (f fakeQualityJudgeAPI) Evaluate(context.Context, string, string, string) (qualityjudge.Result, error) {
	return f.result, f.err
}

func (f fakeQualityJudgeAPI) Repair(context.Context, string, string) (string, bool, error) {
	return "", false, f.err
}

func TestAPIQualityJudgeKeepsRescueMetadata(t *testing.T) {
	a := &apiQualityJudge{j: fakeQualityJudgeAPI{result: qualityjudge.Result{
		Pass: true, Conclusive: true, RescuedByReview: true, ReviewNote: "复核改判通过",
	}}}
	pass, reason, err := a.JudgeTerm(context.Background(), "用户互动", "תגובות", "he")
	if err != nil || !pass || reason != "复核改判通过" {
		t.Fatalf("救回元数据在适配层丢失: pass=%v reason=%q err=%v", pass, reason, err)
	}
}

func TestAPIQualityJudgeErrorIsFailClosed(t *testing.T) {
	a := &apiQualityJudge{j: fakeQualityJudgeAPI{err: errors.New("judge down")}}
	pass, reason, err := a.JudgeTerm(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err == nil || pass || !strings.Contains(reason, "无法判定") {
		t.Fatalf("无法判定必须 fail-closed：pass=%v reason=%q err=%v", pass, reason, err)
	}
}

func TestAPIQualityJudgeInconclusiveIsFailClosed(t *testing.T) {
	a := &apiQualityJudge{j: fakeQualityJudgeAPI{result: qualityjudge.Result{Pass: false, Reason: "两腿都不可用"}}}
	pass, reason, err := a.JudgeTerm(context.Background(), "数据备份", "Rezerwacja danych", "pl")
	if err == nil || pass || !strings.Contains(reason, "无法判定") {
		t.Fatalf("Conclusive=false 即使 err=nil 也必须 fail-closed：pass=%v reason=%q err=%v", pass, reason, err)
	}
}

func TestQualityJudgeClientReusesEngineUsageHook(t *testing.T) {
	cfg := &config.Config{}
	client := llm.NewClient(cfg)
	client.OnUsage = func(context.Context, string, int64, int64) error { return nil }
	eng := &engine.Engine{LLM: client}
	if got := qualityJudgeClient(cfg, eng); got != client || got.OnUsage == nil {
		t.Fatal("语义闸必须复用主引擎客户端，保留 OnUsage 平台成本留痕与路由统计")
	}
	if got := qualityJudgeClient(cfg, nil); got == nil || got == client {
		t.Fatal("无引擎测试形态应构造独立客户端，但不能冒充已继承 usage 钩子")
	}
}

func TestQualityJudgeUsageCallbackRunsWithPlatformEvals(t *testing.T) {
	oldCfg := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = oldCfg })
	llmsource.ResetForTest()
	t.Cleanup(llmsource.ResetForTest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("假上游解码失败: %v", err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Model {
		case "HY-TEST":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"排放"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":10}}`))
		case "GLM-TEST":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"term\":90,\"semantic\":90}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":10}}`))
		default:
			t.Errorf("语义闸拨到未配置模型: %s", req.Model)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)

	name := "quality_usage_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	conn, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	stages, _ := json.Marshal(config.StageModels{
		"initial_evals": {APIBase: srv.URL, APIKey: "k", Model: "GLM-TEST"},
		"review":        {APIBase: srv.URL, APIKey: "k", Model: "DS-TEST"},
	})
	for k, v := range map[string]string{
		"stage_models": string(stages), "online_api_base": srv.URL,
		"online_api_key": "k", "online_model": "HY-TEST",
		"scrape_tier3_backtranslate_model": "HY-TEST",
	} {
		if err := st.SetConfig(k, v); err != nil {
			t.Fatal(err)
		}
	}
	var usageCalls atomic.Int64
	client := llm.NewClient(cfg)
	client.OnUsage = func(ctx context.Context, _ string, _, _ int64) error {
		usageCalls.Add(1)
		if reason, ok := llm.PlatformCostFromCtx(ctx); !ok || reason != llm.PlatformEvals {
			t.Errorf("Judge usage 缺平台成本标记: reason=%q ok=%v", reason, ok)
		}
		return nil
	}
	a := newQualityJudge(cfg, &engine.Engine{LLM: client}, st)
	pass, _, err := a.JudgeTerm(context.Background(), "排放", "פליטה", "he")
	if err != nil || !pass {
		t.Fatalf("真实 Judge 调用应通过: pass=%v err=%v", pass, err)
	}
	if got := usageCalls.Load(); got != 2 {
		t.Fatalf("回译+主判应触发两次 usage 回调，实得 %d", got)
	}
}

func TestScoreThreshold(t *testing.T) {
	const min = 70.0
	cases := []struct {
		term, sem float64
		pass      bool
	}{
		{100, 100, true},
		{70, 70, true}, // 边界：= 阈值放行
		{69, 90, false},
		{90, 69, false},
		{0, 0, false},
	}
	for _, tc := range cases {
		if got := scoreThresholdPass(tc.term, tc.sem, min); got != tc.pass {
			t.Fatalf("term=%.0f sem=%.0f: got=%v want=%v", tc.term, tc.sem, got, tc.pass)
		}
	}
}
