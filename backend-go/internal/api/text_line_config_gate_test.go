// ============ 本文件职责中文说明 ============
// 2026-10-26 裁决九-4 的实现锁：逐行翻译两条档位（`text_line_retry_max`／
// `text_line_upstream_dead_streak`）的管理台配置口（api/text_line_config.go）。
//
// 这一族最坏的形态不是"改不动"，而是**管理台显示一个数、读侧用另一个数**：
// 钳位在两侧各写一份，运营把 8 存进库、界面回显 8，而读侧按上限 10 用（或反过来把脏值读成 0）——
// 表单从此成为误导源。所以本文件的主判据是**跨侧行为等值**：
// 同一把库内值，POST 拒收的那条边界必须与 engine 钳位落到同一档的那条边界**是同一个数**，
// 用行为各证一次（而不是比两段源码里的字面量——比字面量的锁会放过"数字一样但一侧是死码"）。
//
// 七条腿：A 未配置态／B 合法保存即刻生效（现读，不等重启）／C 边界两侧等值（上下各双向）
//
//	D 缺项整批拒／E reset 清空回缺省／F 鉴权分流 401·403／G 脏值（库里被旁路写成 abc/0/99）
//	  回显的是读侧真正在用的那一档，而不是库里那个字面量。
//
// ★ 单测自钉 sqlite 方言（AGENTS §一.4）：`config.Default()` 副作用写全局 `config.C`。
// ==========================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"translator/internal/engine"
)

const tlCfgPath = "/api/admin/config/text-lines"

// tlCfgMux 两条档位口（与生产注册同 handler、同路径）。
func (s *Server) tlCfgMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc(tlCfgPath, s.handleAdminTextLineConfig)
	return m
}

// tlCfgFixture 复用 est 那套最小栈，只多装配一样东西：一个能读 system_config 的引擎。
// 为什么必须装引擎：GET 回显的是**读侧钳位后的生效值**（本文件的全部等值判据都靠它），
// 引擎没装就会退回"按代码缺省回显"，那种回显与库内值有没有生效毫无关系——锁会恒绿。
func tlCfgFixture(t *testing.T) *estCfgFixture {
	t.Helper()
	f := newEstCfgFixture(t)
	f.srv.Engine = &engine.Engine{St: f.st}
	return f
}

// tlCfgCall 打一次接口（method+body+token）。返回状态码与解析过的响应体。
func tlCfgCall(t *testing.T, h http.Handler, method, body, tok string) (int, map[string]interface{}) {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, tlCfgPath, rd)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// tlCfgTiers 从响应体里取两档生效值（缺字段即红，不返回 0 冒充"没配"）。
func tlCfgTiers(t *testing.T, body map[string]interface{}) (retry, streak int) {
	t.Helper()
	raw, ok := body["tiers"].(map[string]interface{})
	if !ok {
		t.Fatalf("响应里没有 tiers 对象 ⇒ 回显面被摘掉，实得 %v", body)
	}
	r, ok1 := raw["retry_max"].(float64)
	s, ok2 := raw["upstream_dead_streak"].(float64)
	if !ok1 || !ok2 {
		t.Fatalf("tiers 缺字段（读侧钳位函数退成 nil 就会这样）：retry=%v streak=%v", raw["retry_max"], raw["upstream_dead_streak"])
	}
	return int(r), int(s)
}

// TestTextLineCfgUnsetShowsDefaults A 腿：库里没表态时回显代码缺省，且 stored 两键皆空。
// "库里没这一行"与"库里是空串"对读侧同义（都走缺省），但界面上必须看得出来——
// 否则运营会以为自己配过 3。
func TestTextLineCfgUnsetShowsDefaults(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	code, body := tlCfgCall(t, h, http.MethodGet, "", f.super)
	if code != http.StatusOK {
		t.Fatalf("超管 GET 应 200，实得 %d body=%v", code, body)
	}
	retry, streak := tlCfgTiers(t, body)
	if retry != 3 || streak != 3 {
		t.Errorf("未配置态两档应都是代码缺省 3，实得 retry=%d streak=%d", retry, streak)
	}
	stored, ok := body["stored"].(map[string]interface{})
	if !ok {
		t.Fatalf("响应缺 stored ⇒ 界面无法区分「没配过」与「配了同一个数」")
	}
	for _, k := range []string{"text_line_retry_max", "text_line_upstream_dead_streak"} {
		if v := strings.TrimSpace(tlStringOfJSON(stored[k])); v != "" {
			t.Errorf("未配置态 stored[%s] 应为空串，实得 %q", k, v)
		}
	}
	if body["defaults"] == nil || body["ranges"] == nil {
		t.Errorf("defaults/ranges 必须在场（表单的提示与前端校验取这两个数，禁止前端另抄一份）")
	}
}

// TestTextLineCfgSaveTakesEffectNow B 腿：保存后**同一个响应**里就是新档，且库里逐键等值。
// 判据不写"等 5 秒再看"——读侧每次现读 system_config，这条腿同时钉住"改完即生效"这一契约。
func TestTextLineCfgSaveTakesEffectNow(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	code, body := tlCfgCall(t, h, http.MethodPost, `{"retry_max":5,"upstream_dead_streak":4}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("合法保存应 200，实得 %d body=%v", code, body)
	}
	retry, streak := tlCfgTiers(t, body)
	if retry != 5 || streak != 4 {
		t.Errorf("保存后回显必须立刻是新档（现读钳位），实得 retry=%d streak=%d", retry, streak)
	}
	if got := f.estCfgStoredOf(t, "text_line_retry_max"); got != "5" {
		t.Errorf("库内 retry_max 应为 5，实得 %q", got)
	}
	if got := f.estCfgStoredOf(t, "text_line_upstream_dead_streak"); got != "4" {
		t.Errorf("库内 upstream_dead_streak 应为 4，实得 %q", got)
	}
	// 再 GET 一次必须同值（防"保存回显新值、下一次又读回旧的"那种两把尺子）
	code2, body2 := tlCfgCall(t, h, http.MethodGet, "", f.super)
	if code2 != http.StatusOK {
		t.Fatalf("二次 GET 应 200，实得 %d", code2)
	}
	r2, s2 := tlCfgTiers(t, body2)
	if r2 != 5 || s2 != 4 {
		t.Errorf("二次 GET 读数必须与保存后一致，实得 retry=%d streak=%d", r2, s2)
	}
}

// TestTextLineCfgBoundsMatchEngineClamp C 腿（本文件主判据）：接口拒收的边界 == 读侧钳位的边界。
// 两侧各双向证一次：边界内 ⇒ 200 且生效；越界 ⇒ 400 且库里**一字不动**。
// 为什么用行为而不是比源码数字：比数字会放过"一侧的数字是死码"（钳位块被摘掉仍等值）。
func TestTextLineCfgBoundsMatchEngineClamp(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		field    string
		inLo     int
		inHi     int
		loBad    int // 低于下界
		hiBad    int // 高于上界
		clampLo  int // 读侧对 loBad 的**实际落点**（0 被 ConfigInt 读成"没配"→缺省；1 是正数→钳到下界）
		clampHi  int // 读侧对 hiBad 的钳位结果（必须==inHi，与接口上界同一个数）
		other    int // 另一条档的合法值（两项必须一次交齐）
		minBound int
	}{
		//        inLo inHi loBad hiBad  loBad落点 hiBad落点
		//        （loBad=0 走"没配"→缺省 3；loBad=1 是正数，钳到下界 2 —— 两种脏值都必须点名）
		{"retry_max", "text_line_retry_max", "retry_max", 1, 5, 0, 6, 3, 5, 3, 5},
		{"upstream_dead_streak", "text_line_upstream_dead_streak", "upstream_dead_streak", 2, 10, 1, 11, 2, 10, 3, 10},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := tlCfgFixture(t)
			h := f.srv.tlCfgMux()
			// 边界内两端都收
			for _, v := range []int{c.inLo, c.inHi} {
				body := `{"` + c.field + `":` + tlItoa(v) + `,"upstream_dead_streak":` + tlItoa(tlOtherOf(c.field)) + `}`
				if c.field == "upstream_dead_streak" {
					body = `{"retry_max":` + tlItoa(tlOtherOf(c.field)) + `,"` + c.field + `":` + tlItoa(v) + `}`
				}
				code, resp := tlCfgCall(t, h, http.MethodPost, body, f.super)
				if code != http.StatusOK {
					t.Fatalf("%s=%d 是合法档，应 200，实得 %d body=%v", c.field, v, code, resp)
				}
				if got := tlLiveTier(t, f.srv.Engine, c.key); got != v {
					t.Errorf("接口收下的值读侧必须真用到：%s 存 %d 读 %d", c.key, v, got)
				}
			}
			// 越界一律拒，且库里不动
			for _, bad := range []int{c.loBad, c.hiBad} {
				if err := f.st.SetConfig(c.key, tlItoa(bad)); err != nil {
					t.Fatalf("预置库值: %v", err)
				}
				before := f.estCfgStoredOf(t, c.key)
				body := `{"` + c.field + `":` + tlItoa(bad) + `,"upstream_dead_streak":` + tlItoa(tlOtherOf(c.field)) + `}`
				if c.field == "upstream_dead_streak" {
					body = `{"retry_max":` + tlItoa(tlOtherOf(c.field)) + `,"` + c.field + `":` + tlItoa(bad) + `}`
				}
				code, resp := tlCfgCall(t, h, http.MethodPost, body, f.super)
				if code == http.StatusOK {
					t.Errorf("%s=%d 越界却收下 ⇒ 写入侧没有边界，脏值会落库（这一族没有 0 档语义）", c.field, bad)
				}
				if msg, _ := resp["message"].(string); !strings.Contains(msg, tlItoa(c.minBound)) && msg == "" {
					t.Errorf("400 必须带可读中文并点名合法区间（含下界 %d），实得 %v", c.minBound, resp)
				}
				if after := f.estCfgStoredOf(t, c.key); after != before {
					t.Errorf("拒收后库里不许被半套参数改写：%q → %q", before, after)
				}
				// 同一条边界在读侧的另一半证据。两个方向的落点**不一样，且都必须点名**：
				//   · 高于上界（6 / 11）⇒ 读侧钳到上界，这个数与接口拒收的上界是同一个 ⇒ 两侧等值；
				//   · 低于下界那一个脏值：0 被 `Store.ConfigInt` 读成"库里没表态"⇒ 回**代码缺省**
				//     （裁决九-4 明写"这一族没有 0 档语义，脏值按默认"）；而 1 是正数，
				//     走的是钳到下界那一条腿（落点=下界，不是缺省）。两种落点都由表里的 loBad落点逐条点名。
				//     ⚠️ 上界必须单独判死（拿 clampLo 一起放行就会把"两侧上界不同"这个真缺陷放过）；
				//     下界这一档只判"确实回落到缺省、没被读成 0"，读成 0＝一枪都不打，是更坏的形态。
				got := tlLiveTier(t, f.srv.Engine, c.key)
				if bad > c.inHi && got != c.clampHi {
					t.Errorf("读侧对超上界脏值 %d 应钳到上界 %d（接口上界必须是同一个数），实得 %d", bad, c.clampHi, got)
				}
				if bad < c.inLo && got != c.clampLo {
					t.Errorf("读侧对低于下界的脏值 %d 的落点应为 %d（0→缺省／正数→钳到下界，见用例表注释），实得 %d", bad, c.clampLo, got)
				}
			}
		})
	}
}

// TestTextLineCfgRequiresBothFields D 腿：缺任一项整批拒收（半套参数＝一条链新档一条链旧档）。
func TestTextLineCfgRequiresBothFields(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	for _, body := range []string{`{"retry_max":4}`, `{"upstream_dead_streak":6}`, `{}`} {
		code, resp := tlCfgCall(t, h, http.MethodPost, body, f.super)
		if code == http.StatusOK {
			t.Errorf("半套参数 %s 却收下 ⇒ 两档会不一致（补译轮数新、早停旧）", body)
		}
		if f.estCfgStoredOf(t, "text_line_retry_max") != "" || f.estCfgStoredOf(t, "text_line_upstream_dead_streak") != "" {
			t.Errorf("拒收后两键都必须仍是空（未配置态），实得 retry=%q streak=%q",
				f.estCfgStoredOf(t, "text_line_retry_max"), f.estCfgStoredOf(t, "text_line_upstream_dead_streak"))
		}
		_ = resp
	}
}

// TestTextLineCfgResetClearsToDefault E 腿：reset 把两键写成空串（与"从未配过"完全同形），
// 生效值回代码缺省。刻意不写 DELETE：那会造出第三种形态（行不存在 vs 行是空串）。
func TestTextLineCfgResetClearsToDefault(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	if code, body := tlCfgCall(t, h, http.MethodPost, `{"retry_max":5,"upstream_dead_streak":9}`, f.super); code != http.StatusOK {
		t.Fatalf("预置合法档失败: %d %v", code, body)
	}
	code, body := tlCfgCall(t, h, http.MethodPost, `{"reset":true}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("reset 应 200，实得 %d body=%v", code, body)
	}
	for _, k := range []string{"text_line_retry_max", "text_line_upstream_dead_streak"} {
		if got := f.estCfgStoredOf(t, k); got != "" {
			t.Errorf("reset 后 %s 应为空串，实得 %q", k, got)
		}
	}
	retry, streak := tlCfgTiers(t, body)
	if retry != 3 || streak != 3 {
		t.Errorf("reset 后生效值必须回代码缺省 3/3，实得 %d/%d", retry, streak)
	}
}

// TestTextLineCfgAuthSplit F 腿：匿名 401、租户管理员 403（分流必须准——前端只在 401 走重登录）。
func TestTextLineCfgAuthSplit(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	if code, _ := tlCfgCall(t, h, http.MethodGet, "", ""); code != http.StatusUnauthorized {
		t.Errorf("匿名 GET 应 401，实得 %d", code)
	}
	if code, resp := tlCfgCall(t, h, http.MethodPost, `{"retry_max":2,"upstream_dead_streak":2}`, f.tenant); code != http.StatusForbidden {
		t.Errorf("租户管理员 POST 应 403（这两条改的是所有租户每一单的成本与等待），实得 %d body=%v", code, resp)
	}
	if code, _ := tlCfgCall(t, h, http.MethodGet, "", f.tenant); code != http.StatusForbidden {
		t.Errorf("租户管理员 GET 也应 403（读面同档，别给半条通道），实得 %d", code)
	}
	if code, _ := tlCfgCall(t, h, http.MethodDelete, "", f.super); code == http.StatusOK {
		t.Errorf("未注册的方法不许 200（写成 200 就是「有人能改档位」的假读数）")
	}
}

// TestTextLineCfgDirtyStoredValueReportsLive G 腿：库里被旁路写成脏值时，
// 回显必须是**读侧真正在用的那一档**，而不是库里那个字面量——否则表单成了误导源。
func TestTextLineCfgDirtyStoredValueReportsLive(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	if err := f.st.SetConfig("text_line_retry_max", "abc"); err != nil {
		t.Fatalf("预置脏值: %v", err)
	}
	code, body := tlCfgCall(t, h, http.MethodGet, "", f.super)
	if code != http.StatusOK {
		t.Fatalf("脏值态 GET 应仍 200（读侧按缺省走，不是接口坏），实得 %d", code)
	}
	retry, _ := tlCfgTiers(t, body)
	if retry != 3 {
		t.Errorf("脏值 abc 的生效档必须是读侧钳位后的 3（这一族没有 0 档语义），实得 %d", retry)
	}
	stored := body["stored"].(map[string]interface{})
	if v := tlStringOfJSON(stored["text_line_retry_max"]); v != "abc" {
		t.Errorf("stored 必须如实回吐库内原值（运营要看得见自己存了什么），实得 %q", v)
	}
}

// TestTextLineCfgAudited 审计留痕：保存与重置各落一条，detail 带上新档（谁把补译轮数调大要可查）。
func TestTextLineCfgAudited(t *testing.T) {
	f := tlCfgFixture(t)
	h := f.srv.tlCfgMux()
	if code, body := tlCfgCall(t, h, http.MethodPost, `{"retry_max":4,"upstream_dead_streak":7}`, f.super); code != http.StatusOK {
		t.Fatalf("保存失败: %d %v", code, body)
	}
	d := f.estCfgAuditLatest(t, "text_line_config_save")
	if !strings.Contains(d, "retry_max=4") || !strings.Contains(d, "upstream_dead_streak=7") {
		t.Errorf("审计 detail 必须带上改成的数，实得 %q", d)
	}
	if code, _ := tlCfgCall(t, h, http.MethodPost, `{"reset":true}`, f.super); code != http.StatusOK {
		t.Fatalf("reset 失败: %d", code)
	}
	if d := f.estCfgAuditLatest(t, "text_line_config_reset"); !strings.Contains(d, "回落代码缺省") {
		t.Errorf("reset 也要留痕（清空与从没配过在库里同形，只有审计能证明是谁清的），实得 %q", d)
	}
}

// tlStringOfJSON 把响应里的任意 JSON 值取成字符串（nil ⇒ 空串；数字/布尔按字面回）。
// 只为本文件的 stored 判据服务，不参与任何业务判定。
func tlStringOfJSON(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, _ := json.Marshal(v)
		return strings.TrimSpace(string(b))
	}
}

// tlItoa 小整数转字符串（包内已有一个裸名 itoa，撞名会编译不过 ⇒ 本文件用带前缀的这一份）。
func tlItoa(n int) string { return strconv.Itoa(n) }

// tlOtherOf 另一条档的合法值（接口要求两项一次交齐，测试得给一个恒合法的配对值）。
func tlOtherOf(field string) int {
	if field == "retry_max" {
		return 3
	}
	return 3
}

// tlLiveTier 现读**读侧**真正生效的那一档（走 engine 那两个函数——唯一实现）。
// 参数：e=服务栈里的引擎（夹具已装配 St）。
// 判据用它而不是用接口回显：本文件要证的正是"接口边界与读侧钳位是同一个数"，
// 拿回显当读侧证据＝同一份数据自己证明自己。
func tlLiveTier(t *testing.T, e *engine.Engine, key string) int {
	t.Helper()
	if e == nil {
		t.Fatal("夹具没装引擎 ⇒ liveTier 读不到读侧真值，本条等值锁失明")
	}
	switch key {
	case "text_line_retry_max":
		return e.LineRetryMax()
	case "text_line_upstream_dead_streak":
		return e.LineDeadStreakMin()
	}
	t.Fatalf("未知档位键 %q ⇒ 判据靶子漂了", key)
	return 0
}
