// ============ 本文件职责中文说明 ============
// ★ 2026-11-01 DEF-P1-05 B2 的运营面锁：即时翻译分段两条档位
//
//	（`text_chat_per_paragraph`／`chat_segment_max_points`）的管理台配置口
//	（api/chat_seg_config.go）。
//
// 形态照抄 text_line_config_gate_test.go（A~G 七条腿），但**主判据是反的那一条**：
// 这一族 0 是真实档位，所以本文件最关键的一条不是"越界被拒"，而是
// **「存 0 ⇒ 读侧必须读到 0」**——那正是 `ConfigIntAllowZero` 与 `ConfigInt` 的分界：
// 读点错用后者时，写 0 会被读成"库里没表态"而回默认 1，表现是"运营按下了关分段的开关、
// 链路照旧逐段跑"，屏幕上零读数、接口还回 200。这条只能按行为证，比源码字面量无效
// （§一·19 九-4 那条「两侧边界同一个数、用行为各证一次」在此同用，只是方向相反）。
//
// 十条腿：A 未配置态／B 合法保存即刻生效／★C 0 档真生效（两键各一）／D 上界两侧等值（行为双向）
//
//	E 缺项整批拒／F reset 清空回缺省／G 鉴权分流 401·403·方法闸／H 脏值回显生效值不是库内字面量
//	／I 审计留痕／J 出栈契约（ranges/defaults/zero_semantics 必须在场——前端不抄第二份判据）
//
// ★ 单测自钉 sqlite 方言由 newEstCfgFixture 承担（AGENTS §一·4：config.Default() 副作用写全局 config.C）。
// ==========================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const csCfgPath = "/api/admin/config/chat-segments"

// csCfgMux 分段档位口（与生产注册同 handler、同路径）。
func (s *Server) csCfgMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc(csCfgPath, s.handleAdminChatSegConfig)
	return m
}

// csCfgCall 打一次接口（method+body+token），返回状态码与解析过的响应体。
func csCfgCall(t *testing.T, h http.Handler, method, body, tok string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, csCfgPath, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]interface{}{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// csCfgTiers 从响应体取两档**生效值**（缺字段即红，不许返回 0 冒充"没配"）。
// 参数：body=响应体；返回 on（走不走分段）与 maxPoints（积分上限）。
func csCfgTiers(t *testing.T, body map[string]interface{}) (on bool, maxPoints int) {
	t.Helper()
	raw, ok := body["tiers"].(map[string]interface{})
	if !ok {
		t.Fatalf("响应里没有 tiers 对象 ⇒ 回显面被摘掉，实得 %v", body)
	}
	p, ok1 := raw["per_paragraph"].(float64)
	m, ok2 := raw["max_points"].(float64)
	if !ok1 || !ok2 {
		t.Fatalf("tiers 缺字段（读侧退成 nil 就会这样）：per_paragraph=%v max_points=%v", raw["per_paragraph"], raw["max_points"])
	}
	// per_paragraph 出栈是 0/1 整数档（不是布尔）：与合法区间 [0,1] 同形，前端按区间预拦
	if p != 0 && p != 1 {
		t.Fatalf("per_paragraph 生效值只许 0 或 1，实得 %v", p)
	}
	return p == 1, int(m)
}

// csCfgLive 现读**读侧**此刻真正生效的那一对档位（走 chatSegTiers 唯一读点）。
// 判据用它而不是接口回显：本文件要证的是"接口边界与读侧钳位/AllowZero 是同一个数"，
// 拿回显当读侧证据＝同一份数据自己证明自己。
func csCfgLive(t *testing.T, s *Server) (bool, int64) {
	t.Helper()
	if s == nil {
		t.Fatal("夹具没装服务栈 ⇒ 读侧真值读不到，本条等值锁失明")
	}
	return s.chatSegTiers()
}

const (
	csKeySwitch  = "text_chat_per_paragraph"
	csKeyMaxPts  = "chat_segment_max_points"
	csBodyOffNo  = `{"per_paragraph":0,"max_points":60}`
	csBodyOnZero = `{"per_paragraph":1,"max_points":0}`
)

// TestChatSegCfgUnsetShowsDefaults A 腿：库里没表态 ⇒ 回显代码缺省（开分段＋上限 60），stored 两键皆空。
func TestChatSegCfgUnsetShowsDefaults(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	code, body := csCfgCall(t, h, http.MethodGet, "", f.super)
	if code != http.StatusOK {
		t.Fatalf("超管 GET 应 200，实得 %d body=%v", code, body)
	}
	on, maxPts := csCfgTiers(t, body)
	if !on || maxPts != 60 {
		t.Errorf("未配置态应回代码缺省（开分段／上限 60），实得 on=%v max=%d", on, maxPts)
	}
	stored, ok := body["stored"].(map[string]interface{})
	if !ok {
		t.Fatalf("响应缺 stored ⇒ 界面无法区分「没配过」与「配了同一个数」")
	}
	for _, k := range []string{csKeySwitch, csKeyMaxPts} {
		if v := strings.TrimSpace(tlStringOfJSON(stored[k])); v != "" {
			t.Errorf("未配置态 stored[%s] 应为空串，实得 %q", k, v)
		}
	}
}

// TestChatSegCfgSaveTakesEffectNow B 腿：保存后**同一个响应**就是新档，库里逐键等值，二次 GET 同值。
func TestChatSegCfgSaveTakesEffectNow(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	code, body := csCfgCall(t, h, http.MethodPost, `{"per_paragraph":1,"max_points":200}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("合法保存应 200，实得 %d body=%v", code, body)
	}
	on, maxPts := csCfgTiers(t, body)
	if !on || maxPts != 200 {
		t.Errorf("保存后回显必须立刻是新档，实得 on=%v max=%d", on, maxPts)
	}
	if got := f.estCfgStoredOf(t, csKeySwitch); got != "1" {
		t.Errorf("库内 %s 应为 1，实得 %q", csKeySwitch, got)
	}
	if got := f.estCfgStoredOf(t, csKeyMaxPts); got != "200" {
		t.Errorf("库内 %s 应为 200，实得 %q", csKeyMaxPts, got)
	}
	code2, body2 := csCfgCall(t, h, http.MethodGet, "", f.super)
	if code2 != http.StatusOK {
		t.Fatalf("二次 GET 应 200，实得 %d", code2)
	}
	if on2, max2 := csCfgTiers(t, body2); !on2 || max2 != 200 {
		t.Errorf("二次 GET 读数必须与保存后一致（两把尺子的典型形态就是这里翻回去），实得 on=%v max=%d", on2, max2)
	}
}

// TestChatSegCfgZeroIsARealTier ★C 腿（本文件主判据，与逐行翻译那一族唯一的判据差异）：
// 0 是一个**真实档位**，写侧必须收下、读侧必须读到 0，而且必须读到"分段被关掉／上限被取消"这一事实。
// 三条证据缺一不可：① HTTP 200；② 库里那一列字面量是 "0"（不是被写成空串冒充"没配"）；
// ③ 读侧 chatSegTiers 真的返回 false / 0。少了 ③ 就是「接口答应了、链路没听见」那一族。
func TestChatSegCfgZeroIsARealTier(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()

	t.Run("per_paragraph=0 关掉分段", func(t *testing.T) {
		code, body := csCfgCall(t, h, http.MethodPost, csBodyOffNo, f.super)
		if code != http.StatusOK {
			t.Fatalf("0 是合法档位，却被拒：%d body=%v（照抄那族「0 一律拒收」就是把这颗闸做成摆设）", code, body)
		}
		if got := f.estCfgStoredOf(t, csKeySwitch); got != "0" {
			t.Errorf("库内开关应为 \"0\"（与\"从未配过\"的空串必须不同形），实得 %q", got)
		}
		on, _ := csCfgLive(t, f.srv)
		if on {
			t.Errorf("存了 0 而读侧仍判「开分段」 ⇒ 读点错用了 ConfigInt（≤0 被读成没表态），那颗闸按不下去")
		}
		// 回显也必须说"现在是关的"：界面填的必须是生效值
		if echoOn, _ := csCfgTiers(t, body); echoOn {
			t.Errorf("保存 0 后回显仍为开 ⇒ 表单与链路两把尺子")
		}
	})

	t.Run("max_points=0 不设上限", func(t *testing.T) {
		code, body := csCfgCall(t, h, http.MethodPost, csBodyOnZero, f.super)
		if code != http.StatusOK {
			t.Fatalf("上限 0（不设上限）是合法档位，却被拒：%d body=%v", code, body)
		}
		if got := f.estCfgStoredOf(t, csKeyMaxPts); got != "0" {
			t.Errorf("库内上限应为 \"0\"，实得 %q", got)
		}
		_, maxPts := csCfgLive(t, f.srv)
		if maxPts != 0 {
			t.Errorf("读侧上限应真是 0（＝不设上限），实得 %d ⇒ 0 被回退成默认 60，长文会被误回落整篇", maxPts)
		}
		if _, echoMax := csCfgTiers(t, body); echoMax != 0 {
			t.Errorf("保存 0 后回显成 %d ⇒ 界面把「不设上限」显示成「上限 60」", echoMax)
		}
	})
}

// TestChatSegCfgUpperBoundMatchesReadClamp D 腿：接口拒收的上界 == 读侧钳位的上界（同一个数、行为双向）。
// 只判上界：本族下界 0 合法（C 腿已判），拿"低于下界"来证一条不存在的边界＝结构性空转锁。
func TestChatSegCfgUpperBoundMatchesReadClamp(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	// 边界内（上界 100000）⇒ 收下且读侧真用到
	var (
		code   int
		body   map[string]interface{}
		before string
	)
	code, body = csCfgCall(t, h, http.MethodPost, `{"per_paragraph":1,"max_points":100000}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("max_points=100000 是合法档，应 200，实得 %d body=%v", code, body)
	}
	if _, live := csCfgLive(t, f.srv); live != 100000 {
		t.Errorf("接口收下的上界读侧必须真用到，实得 %d", live)
	}
	// 越界 ⇒ 400 且库里一字不动
	before = f.estCfgStoredOf(t, csKeyMaxPts)
	code, resp := csCfgCall(t, h, http.MethodPost, `{"per_paragraph":1,"max_points":100001}`, f.super)
	if code == http.StatusOK {
		t.Errorf("max_points=100001 越界却收下 ⇒ 写入侧没有边界，脏值会落库")
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "100000") {
		t.Errorf("400 必须点名合法区间上界（100000）并说明 0 档语义，实得 %q", msg)
	}
	if !strings.Contains(msg, "0 是合法档位") {
		t.Errorf("400 文案必须写清「0 合法」（这一族与逐行翻译那族相反，不点名就会把人引向「填 0 试试」）：实得 %q", msg)
	}
	if after := f.estCfgStoredOf(t, csKeyMaxPts); after != before {
		t.Errorf("拒收后库里不许被半套参数改写：%q → %q", before, after)
	}
	// 旁路 SQL 写进来的超界脏值 ⇒ 读侧钳到同一个数（H 腿的另一半证据）
	if err := f.st.SetConfig(csKeyMaxPts, "999999999"); err != nil {
		t.Fatalf("预置脏值: %v", err)
	}
	if _, live := csCfgLive(t, f.srv); live != 100000 {
		t.Errorf("读侧对超上界脏值应钳到 100000（与接口拒收的上界同一个数），实得 %d", live)
	}
	// ★ 开关那一档的**上界**同样两侧一个数：合法只有 0/1，2 必须拒（读侧按"!=0 即开"解释脏值，
	//   所以库里被旁路写成 2 时链路读成"开"——界面回显的生效值也必须是 1，而不是 2）。
	if err := f.st.SetConfig(csKeySwitch, "1"); err != nil {
		t.Fatalf("预置开关: %v", err)
	}
	before = f.estCfgStoredOf(t, csKeySwitch)
	code, resp = csCfgCall(t, h, http.MethodPost, `{"per_paragraph":2,"max_points":100000}`, f.super)
	if code == http.StatusOK {
		t.Errorf("per_paragraph=2 越界却收下 ⇒ 开关面没有边界，界面会出现既不是开也不是关的第三态")
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "0 ~ 1") {
		t.Errorf("开关越界的 400 必须点名区间 0 ~ 1，实得 %q", msg)
	}
	if after := f.estCfgStoredOf(t, csKeySwitch); after != before {
		t.Errorf("拒收后库里开关不许被改写：%q → %q", before, after)
	}
	// 旁路脏值 2 ⇒ 出栈的生效值必须仍落在 0/1 那一形（csCfgTiers 里"非 0/1 即红"这条判据在这里真被走到）
	if err := f.st.SetConfig(csKeySwitch, "2"); err != nil {
		t.Fatalf("预置脏开关: %v", err)
	}
	code, body = csCfgCall(t, h, http.MethodGet, "", f.super)
	if code != http.StatusOK {
		t.Fatalf("脏开关态 GET 应仍 200（读侧按「!=0 即开」解释，不是接口坏），实得 %d", code)
	}
	if echoOn, _ := csCfgTiers(t, body); !echoOn {
		t.Errorf("库内 2 在读侧仍是「开分段」，回显却判成关 ⇒ 表单与链路两把尺子")
	}
}

// TestChatSegCfgRequiresBothFields E 腿：缺任一项整批拒（半套参数＝一条腿分段、一条腿按旧上限回落）。
func TestChatSegCfgRequiresBothFields(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	for _, body := range []string{`{"per_paragraph":0}`, `{"max_points":30}`, `{}`} {
		code, resp := csCfgCall(t, h, http.MethodPost, body, f.super)
		if code == http.StatusOK {
			t.Errorf("半套参数 %s 却收下 ⇒ 两档会不一致", body)
		}
		if msg, _ := resp["message"].(string); !strings.Contains(msg, "一次提交") {
			t.Errorf("缺项的 400 必须点名「两条档位必须一次提交」，实得 %q", msg)
		}
		if f.estCfgStoredOf(t, csKeySwitch) != "" || f.estCfgStoredOf(t, csKeyMaxPts) != "" {
			t.Errorf("拒收后两键都必须仍是空（未配置态）")
		}
	}
	// ★ 反向对照：{"per_paragraph":0} 这一条上面判"缺 max_points 应拒"，
	//   而它**必须**是因为缺项被拒、不是因为 0 被拒——文案里出现"0"这一档的拒收理由即判红。
	_, resp := csCfgCall(t, h, http.MethodPost, `{"per_paragraph":0}`, f.super)
	if msg, _ := resp["message"].(string); strings.Contains(msg, "必须落在") {
		t.Errorf("单提交 per_paragraph=0 的失败理由应是「缺 max_points」，而不是把 0 判成越界：实得 %q", msg)
	}
}

// TestChatSegCfgResetClearsToDefault F 腿：reset 把两键写成空串（与"从未配过"同形），生效值回代码缺省。
// 刻意不 DELETE：那会造出第三种形态（行不存在 vs 行是空串），库里两种形态在配置面是两把尺子。
func TestChatSegCfgResetClearsToDefault(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	if code, body := csCfgCall(t, h, http.MethodPost, `{"per_paragraph":0,"max_points":5}`, f.super); code != http.StatusOK {
		t.Fatalf("预置合法档失败: %d %v", code, body)
	}
	code, body := csCfgCall(t, h, http.MethodPost, `{"reset":true}`, f.super)
	if code != http.StatusOK {
		t.Fatalf("reset 应 200，实得 %d body=%v", code, body)
	}
	for _, k := range []string{csKeySwitch, csKeyMaxPts} {
		if got := f.estCfgStoredOf(t, k); got != "" {
			t.Errorf("reset 后 %s 应为空串，实得 %q", k, got)
		}
	}
	on, maxPts := csCfgTiers(t, body)
	if !on || maxPts != 60 {
		t.Errorf("reset 后生效值必须回代码缺省（开／60），实得 on=%v max=%d", on, maxPts)
	}
}

// TestChatSegCfgAuthSplit G 腿：匿名 401、租户管理员 403（读面同档，不给半条通道）、未注册方法不 200。
func TestChatSegCfgAuthSplit(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	if code, _ := csCfgCall(t, h, http.MethodGet, "", ""); code != http.StatusUnauthorized {
		t.Errorf("匿名 GET 应 401，实得 %d", code)
	}
	if code, resp := csCfgCall(t, h, http.MethodPost, `{"per_paragraph":0,"max_points":10}`, f.tenant); code != http.StatusForbidden {
		t.Errorf("租户管理员 POST 应 403（这两条改的是所有租户每一单的形态与成本上限），实得 %d body=%v", code, resp)
	}
	if code, _ := csCfgCall(t, h, http.MethodGet, "", f.tenant); code != http.StatusForbidden {
		t.Errorf("租户管理员 GET 也应 403（读面同档），实得 %d", code)
	}
	if code, _ := csCfgCall(t, h, http.MethodDelete, "", f.super); code == http.StatusOK {
		t.Errorf("未注册的方法不许 200（写成 200 就是「有人能改档位」的假读数）")
	}
}

// TestChatSegCfgDirtyStoredValueReportsLive H 腿：库里被旁路写成脏值时，回显必须是**读侧真正在用的那一档**。
// 三档脏值落点各不相同、必须分开点名（混成一档就是把"读侧怎么解释脏值"写成一句猜测）：
//
//	"abc"  → 解析失败 ⇒ 代码缺省（60／开）
//	"-5"   → ConfigIntAllowZero 判 n<0 ⇒ 代码缺省（**不是** 0，也不是钳到下界）
//	""     → 从未配过 ⇒ 代码缺省
func TestChatSegCfgDirtyStoredValueReportsLive(t *testing.T) {
	cases := []struct {
		name    string
		stored  string
		wantOn  bool
		wantMax int
	}{
		{"非数字 abc", "abc", true, 60},
		{"负数 -5", "-5", true, 60},
		{"空串（从未配过）", "", true, 60},
		{"合法的 0", "0", true, 0}, // ★ 唯一一条落点不是缺省的：0 必须读出 0
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newEstCfgFixture(t)
			h := f.srv.csCfgMux()
			if err := f.st.SetConfig(csKeyMaxPts, c.stored); err != nil {
				t.Fatalf("预置脏值: %v", err)
			}
			if err := f.st.SetConfig(csKeySwitch, "1"); err != nil {
				t.Fatalf("预置开关: %v", err)
			}
			code, body := csCfgCall(t, h, http.MethodGet, "", f.super)
			if code != http.StatusOK {
				t.Fatalf("脏值态 GET 应仍 200（读侧按缺省走，不是接口坏），实得 %d", code)
			}
			on, maxPts := csCfgTiers(t, body)
			if on != c.wantOn || maxPts != c.wantMax {
				t.Errorf("stored=%q 的生效档应为 on=%v max=%d（落点见用例表注释），实得 on=%v max=%d",
					c.stored, c.wantOn, c.wantMax, on, maxPts)
			}
			stored := body["stored"].(map[string]interface{})
			if v := tlStringOfJSON(stored[csKeyMaxPts]); v != strings.TrimSpace(c.stored) {
				t.Errorf("stored 必须如实回吐库内原值（运营要看得见自己存了什么），实得 %q 期望 %q", v, strings.TrimSpace(c.stored))
			}
		})
	}
}

// TestChatSegCfgAudited I 腿：保存与重置各落一条审计，detail 带上新档（谁把分段关掉要可查）。
func TestChatSegCfgAudited(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	if code, body := csCfgCall(t, h, http.MethodPost, `{"per_paragraph":0,"max_points":30}`, f.super); code != http.StatusOK {
		t.Fatalf("保存失败: %d %v", code, body)
	}
	d := f.estCfgAuditLatest(t, "chat_seg_config_save")
	if !strings.Contains(d, "per_paragraph=0") || !strings.Contains(d, "max_points=30") {
		t.Errorf("审计 detail 必须带上改成的数，实得 %q", d)
	}
	if code, _ := csCfgCall(t, h, http.MethodPost, `{"reset":true}`, f.super); code != http.StatusOK {
		t.Fatalf("reset 失败: %d", code)
	}
	if d := f.estCfgAuditLatest(t, "chat_seg_config_reset"); !strings.Contains(d, "回落代码缺省") {
		t.Errorf("reset 也要留痕（清空与从没配过在库里同形，只有审计能证明是谁清的），实得 %q", d)
	}
}

// TestChatSegCfgPayloadCarriesRanges J 腿：出栈体必须带 ranges／defaults／zero_semantics。
// 前端拿 ranges 做保存前预拦、拿 defaults 做提示、拿 zero_semantics 判"0 能不能填"——
// 这三样若缺席，前端只能自己抄一份数字（§一·5「报价只有一个事实源」同族的第二把尺子起点）。
func TestChatSegCfgPayloadCarriesRanges(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.csCfgMux()
	code, body := csCfgCall(t, h, http.MethodGet, "", f.super)
	if code != http.StatusOK {
		t.Fatalf("GET 应 200，实得 %d", code)
	}
	for _, k := range []string{"ranges", "defaults", "zero_semantics", "tiers", "stored"} {
		if _, ok := body[k]; !ok {
			t.Errorf("出栈体缺 %q ⇒ 前端只能另抄一份判据", k)
		}
	}
	if got, _ := body["zero_semantics"].(string); got != "zero_is_a_real_tier" {
		t.Errorf("zero_semantics 是对外契约（这一族 0 合法）；它翻成别的值说明判据方向被改回\"0 拒收\"那一族，实得 %v", body["zero_semantics"])
	}
	ranges, _ := body["ranges"].(map[string]interface{})
	switchR, _ := ranges["per_paragraph"].([]interface{})
	maxR, _ := ranges["max_points"].([]interface{})
	if len(switchR) != 2 || len(maxR) != 2 {
		t.Fatalf("ranges 两档都必须出 [下界, 上界]，实得 %v / %v", ranges["per_paragraph"], ranges["max_points"])
	}
	if switchR[0] != float64(0) || switchR[1] != float64(1) {
		t.Errorf("开关区间必须是 [0,1]（0 合法），实得 %v", switchR)
	}
	if maxR[0] != float64(0) || maxR[1] != float64(100000) {
		t.Errorf("上限区间必须是 [0,100000]（0＝不设上限），实得 %v", maxR)
	}
	defs, _ := body["defaults"].(map[string]interface{})
	if defs["per_paragraph"] != float64(1) || defs["max_points"] != float64(60) {
		t.Errorf("defaults 必须是代码缺省 1/60（与 engine 常量同源），实得 %v", defs)
	}
}
