// ============ capability_facts_gate_test.go · 职责说明 ============
// 组7 的助手口径锁（★ 2026-10-31 补齐 G20／G25／G26）：
//
//	G20 能力事实单一事实源：档位宽度来自 `/api/instant/caps` **现读**（换一组非默认数字，
//	    prompt 里必须跟着变），assist 引擎源码里零写死的宽度字面量；
//	G25 匿名不冒充知道额度：拼出来的事实段里零「您/你…还剩/剩余/已用」这类第一人称存量句式；
//	G26 喂给助手的那一份不含暗额度：payload 里故意塞三个暗额度键，事实段里一个数字都不许出现。
//	另有一条失败方向：caps 取不到 ⇒ 整段不出现（宁可让模型说"看页面"，不许兜旧数字）。
//
// =============================================
package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCapabilityFacts_G20_G25_G26(t *testing.T) {
	e := newTestEngine(t)
	// 用一组**非默认**数字，才能证明"现读"而不是抄默认值（默认 1/3/30/60/40）。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/instant/caps" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// 故意把三个暗额度也塞进来：它们绝不许进 prompt（G26）。
		_, _ = w.Write([]byte(`{"success":true,"free_on":true,
			"free_image_max_mb":2,"free_audio_max_sec":45,"free_lang_limit":2,
			"image_max_mb":4,"audio_max_mb":5,"audio_max_sec":90,"ticket_max_mb":50,
			"free_daily_points":300,"free_ip_daily":300,"free_tenant_daily_points":2000}`))
	}))
	defer srv.Close()
	if err := e.db.SetConfig("main_base_url", srv.URL); err != nil {
		t.Fatalf("设置 main_base_url 失败：%v", err)
	}

	block := e.CapabilityFactsSnapshot(context.Background())
	if block == "" {
		t.Fatal("G20：caps 可取时事实段不许为空")
	}
	for _, want := range []string{"2 MB", "4 MB", "45 秒", "90 秒"} {
		if !strings.Contains(block, want) {
			t.Fatalf("G20：事实段未按现读值渲染 %q，实得：\n%s", want, block)
		}
	}
	// G28/I-3：暗额度键与它们的数字都不许出现（300/2000 在本夹具里不是任何宽度）。
	for _, bad := range []string{"free_daily_points", "free_ip_daily", "free_tenant_daily_points", "300", "2000"} {
		if strings.Contains(block, bad) {
			t.Fatalf("G26：事实段泄漏暗额度 %q：\n%s", bad, block)
		}
	}
	// G25：不许出现第一人称存量句式（正向对照：免费档这一句必须在）。
	if regexp.MustCompile(`(?:您|你)[^。；\n]{0,10}(?:还剩|剩余|已用|用完)`).MatchString(block) {
		t.Fatalf("G25：事实段出现第一人称存量句式：\n%s", block)
	}
	if !strings.Contains(block, "免费档") {
		t.Fatalf("G25：正向对照失败——事实段连「免费档」都没有：\n%s", block)
	}

	// G20 源码腿：能力事实文件里不许写死宽度数字（数字只能来自上面的响应）。
	src, err := os.ReadFile("capability_facts.go")
	if err != nil {
		t.Fatalf("读 capability_facts.go 失败：%v", err)
	}
	// 先剥整行注释：文件头"知识库里还写着旧的 3 MB"这类说明不是实现（同 §一·17 判据先剥注释）。
	code := make([]string, 0, 64)
	for _, ln := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		code = append(code, ln)
	}
	if regexp.MustCompile(`\b(?:1|3|40)\s*MB|\b(?:30|60)\s*秒`).MatchString(strings.Join(code, "\n")) {
		t.Fatal("G20：capability_facts.go 里写死了档位数字（运营改配置后助手会念旧值）")
	}

	// 取不到 ⇒ 整段不出现（不兜旧数字）。
	if err := e.db.SetConfig("main_base_url", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("重置 main_base_url 失败：%v", err)
	}
	if got := e.CapabilityFactsSnapshot(context.Background()); got != "" {
		t.Fatalf("G20：caps 取不到时必须整段不出现，实得：%s", got)
	}
}
