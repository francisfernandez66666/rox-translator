// system_values_test.go — 管理台「系统现值」读数口 + 新配置键白名单（★ 081x 2026-09-29）
//
// 为什么要专门测这个只读口：现值注入走的是「取不到就整段不拼」的软路径，
// 挂件界面在失败态和成功态长得一模一样（只是价格/语种数回到知识文案的旧口径）。
// 本测试钉的是**运维看得见的那一面**：拨的哪个地址、取到没有、取到的是哪段文本。
// 这条口一旦退化（比如失败时返回 500 让管理台弹错，或 ok 恒真），软失败就又变成暗病了。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubMainService 主服务替身：只答复价口与语种口。pricing/langs 传 "" 表示该口 500。
func stubMainService(t *testing.T, pricing, langs string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pricing/meta":
			if pricing == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(pricing))
		case "/api/translation/langs":
			if langs == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(langs))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// setCfg 走管理口写一项配置（白名单外的键会 400，正好当白名单断言用）
func setCfg(t *testing.T, srv *httptest.Server, key, val string) int {
	t.Helper()
	code, _ := doJSON(t, srv, "PUT", "/api/assist/admin/config", "test-token",
		map[string]any{"key": key, "value": val})
	return code
}

// TestSystemValuesEndpointOK 现值可用时：200 + ok=true + fresh 里就是那两个实时数。
// 样本值 35 / 3.2 / 0.099668 全部不是仓库里出现过的常量（真值走现取，不是抄来的）。
func TestSystemValuesEndpointOK(t *testing.T) {
	main := stubMainService(t,
		`{"success":true,"modes":[{"code":"fast","points_per_1k_chars":3.2,"points_fixed":3},{"code":"pro","points_per_1k_chars":8.1,"points_fixed":7.5}],"points_price_money":0.099668,"unit":"points"}`,
		`{"kb_langs":[{"code":"en","name":"英语","kb":"true"},{"code":"ja","name":"日语","kb":"true"}]}`)
	srv := newTestServer(t)
	if code := setCfg(t, srv, "main_base_url", main.URL); code != 200 {
		t.Fatalf("main_base_url 应可后台配置，实得 %d", code)
	}
	code, body := doJSON(t, srv, "GET", "/api/assist/admin/system-values", "test-token", nil)
	if code != 200 {
		t.Fatalf("现值读数口应 200，实得 %d", code)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("两口都通还报 ok=false：%v", body)
	}
	fresh, _ := body["fresh"].(string)
	for _, want := range []string{"可选目标语言：2 种", "快速模式每 1000 源字符·单语种 3.2 积分", "1 积分 ≈ 0.099668 元"} {
		if !strings.Contains(fresh, want) {
			t.Fatalf("fresh 缺 %q：\n%s", want, fresh)
		}
	}
	if got, _ := body["base_url"].(string); got != main.URL {
		t.Fatalf("base_url 回显错（运维要照它排查拨错地址）：%v", got)
	}
	// 只读判据：本口不写任何东西，cached 在没人建过 prompt 前就是空 + Age 负数
	if age, _ := body["cached_age_sec"].(float64); age != -1 {
		t.Fatalf("还没走过对话就报了缓存年龄：%v", body["cached_age_sec"])
	}
}

// TestSystemValuesEndpointShowsFailure 主服务挂了：管理台必须**明确说不确定**，
// 而不是 500（那会把面板打成报错页）也不是 ok 恒真（那是暗病）。
func TestSystemValuesEndpointShowsFailure(t *testing.T) {
	srv := newTestServer(t)
	// 127.0.0.1:1 必然拒连（刻意不用默认值 8787：本机开发时那上面可能真起着主服务）
	if code := setCfg(t, srv, "main_base_url", "http://127.0.0.1:1"); code != 200 {
		t.Fatalf("main_base_url 应可后台配置，实得 %d", code)
	}
	code, body := doJSON(t, srv, "GET", "/api/assist/admin/system-values", "test-token", nil)
	if code != 200 {
		t.Fatalf("取不到现值也必须 200（读数口不是健康检查）：%d", code)
	}
	if ok, _ := body["ok"].(bool); ok {
		t.Fatalf("两口都连不上却报 ok=true：%v", body)
	}
	if fresh, _ := body["fresh"].(string); fresh != "" {
		t.Fatalf("取不到却有 fresh 文本（多半是把旧文案兜出来了）：%q", fresh)
	}
}

// TestSystemValuesEndpointGuard 鉴权与动词：无 Token 401、非 GET 405（读数口只读）。
func TestSystemValuesEndpointGuard(t *testing.T) {
	srv := newTestServer(t)
	if code, _ := doJSON(t, srv, "GET", "/api/assist/admin/system-values", "", nil); code != 401 {
		t.Fatalf("无 Token 应 401，实得 %d", code)
	}
	if code, _ := doJSON(t, srv, "POST", "/api/assist/admin/system-values", "test-token", nil); code != 405 {
		t.Fatalf("POST 应 405（只读口），实得 %d", code)
	}
}

// TestPromiseAndMainBaseUrlWhitelisted ★ 081x：新配置键必须在白名单里，否则运营改不动、
// 承诺口径就只能发版（这正是这一批要拆掉的东西）。同一条测试顺带钉住"未知键仍 400"。
func TestPromiseAndMainBaseUrlWhitelisted(t *testing.T) {
	srv := newTestServer(t)
	// 每键给**互不相同**的值：三个键都写同一个值时，"读回原值"这条断言证明不了是哪把键回来的
	vals := map[string]string{
		"promise_rules": "承诺口径甲",
		"main_base_url": "http://127.0.0.1:1",
		"tone_rules":    "说话方式乙",
	}
	for k, v := range vals {
		if code := setCfg(t, srv, k, v); code != 200 {
			t.Fatalf("配置键 %s 应可后台写，实得 %d", k, code)
		}
	}
	if code := setCfg(t, srv, "promise_rulesx", "x"); code != 400 {
		t.Fatalf("非白名单键应 400，实得 %d", code)
	}
	// 回读：承诺口径不是密文，管理台必须原样拿到（写进去读不回＝界面在骗运营）
	code, r := doJSON(t, srv, "GET", "/api/assist/admin/config", "test-token", nil)
	raw, _ := json.Marshal(r)
	if code != 200 {
		t.Fatalf("配置读取应 200，实得 %d", code)
	}
	for _, want := range []string{"承诺口径甲", "说话方式乙"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("配置 %q 写后读不回原值（管理台会显示成空，运营以为没保存）：%s", want, raw)
		}
	}
}
