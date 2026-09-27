// ============ 本文件职责中文说明 ============
// 公开算价口 GET /api/pricing/meta（★ 〇-X #55，2026-09-28，api/pricing_meta.go）单测。
//
// 这个口是「官网把计费公式公示给客户」这件事的唯一数据源，所以它同时背着两条互相拉扯的约束：
//
//	① 不能漏内部量（AGENTS §一·5：公开接口零 token 裸值——汇率、尺子、K/F 一律不外露，
//	   否则客户能从积分单价反推平台真实成本，S1 积分制的立身之本就没了）；
//	② 公示的公式必须**真的等于**建单时扣费用的公式，否则官网那句话就是虚假宣传。
//
// 本文件把这两条各钉一枚等值锁，另加「改档要跟」「脏值回退」「方法闸」三段：
//
//	A 契约面锁：出参**键集合精确等值**（多一个键就红灯——这是防漏最有效的一招，
//	  比"禁某些词"强，因为新增字段的人根本不会想到去写被禁的词）；
//	  缺省系数按积分口径等值（pro 400/7.5、fast 150/3）、每积分单价 = 汇率×尺子÷1e8，
//	  并反向锁「3,000 积分 = ¥299」这条套餐面值（F-78 联动对的公开侧投影）；
//	  外加一条 body 不含 "token" 字样与四枚内部键名的粗筛（漏口径的第一道网）。
//	B 公式同源锁：页面用公示系数跑出来的积分，必须与后端建单预检
//	  PointsFromTokens(estimateTicketTokens(...)) 逐档等值（短/中/长 × 两模式）。
//	  系数被配成非整数时放宽到 ≤1 积分——那是 token 层 int64 截断与积分层取整的量化噪声，
//	  不是口径分叉，写死等值反而会在超管调档后假红。
//	C 改档跟随锁：汇率与尺子成对改档后所有读数一起动、面值仍锁 ¥299；
//	  **只动一头**时面值必须断（负向对照，证明 A 的面值锁真有牙，不是恒绿）。
//	D 脏配置回退锁：库里躺着 "abc"/空串时公开页显示的是读侧真正在用的缺省值（与超管表单同口径）；
//	  固定项显式配 0 必须露 0（那是合法的「退回纯线性」，不许被当成脏值抹成缺省）；
//	  脏汇率不得让出参出现 0/NaN/Inf（除零兜底）。
//	E 方法闸：非 GET 一律 405 走统一错误出口（带 code，不许 200 承载失败，F-64①）。
//
// ★ 自钉 sqlite 方言（AGENTS §一·4）：夹具 newEstCfgFixture 已负责钉底并 t.Cleanup 恢复。
//
//	运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestPricingMeta
//
// ==========================================
package api

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// pricingMetaMux 公开算价口（与生产注册同 handler、同路径）。
func (s *Server) pricingMetaMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/pricing/meta", s.handlePricingMeta)
	return m
}

// pricingMetaGet 匿名 GET 并**同时**返回解析结果与原始字节
// （A 段的漏口径粗筛要看原始字节——只看 map 会漏掉"被 map 化之后才显现"的字段名）。
func pricingMetaGet(t *testing.T, h http.Handler) (map[string]interface{}, string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/pricing/meta", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("匿名读取公开算价口应 200（客户注册前就要能试算），got %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("出参不是合法 JSON: %v（body=%s）", err, w.Body.String())
	}
	return out, w.Body.String()
}

// pricingMetaModes 把 modes 取成 code→档 的映射，并顺手做逐档键集合等值锁
// （按下标取值会让"后端多加一档/换个顺序"变成静默错读，故一律按 code 取）。
func pricingMetaModes(t *testing.T, resp map[string]interface{}) map[string]map[string]interface{} {
	t.Helper()
	raw, _ := resp["modes"].([]interface{})
	if len(raw) != 2 {
		t.Fatalf("modes 必须是 fast/pro 两档，got %+v", resp["modes"])
	}
	out := map[string]map[string]interface{}{}
	for _, it := range raw {
		m, _ := it.(map[string]interface{})
		for k := range m {
			if k != "code" && k != "points_per_1k_chars" && k != "points_fixed" {
				t.Fatalf("modes 出现契约外键 %q：%+v", k, m)
			}
		}
		code, _ := m["code"].(string)
		if code == "" || m["points_per_1k_chars"] == nil || m["points_fixed"] == nil {
			t.Fatalf("modes 每档必须带 code/points_per_1k_chars/points_fixed，got %+v", m)
		}
		out[code] = m
	}
	if out["fast"] == nil || out["pro"] == nil {
		t.Fatalf("modes 必须同时有 fast 与 pro 两档，got %+v", out)
	}
	return out
}

// pricingFloat 取某档的数值字段（JSON 数字一律 float64，缺键直接 fatal，不做静默 0）
func pricingFloat(t *testing.T, m map[string]map[string]interface{}, code, field string) float64 {
	t.Helper()
	v, ok := m[code][field].(float64)
	if !ok {
		t.Fatalf("%s.%s 不是数字：%+v", code, field, m[code])
	}
	return v
}

// A 契约面：键集合精确等值、缺省系数与每积分单价、套餐面值、零 token 裸值
func TestPricingMetaContractAndNoTokenLeak(t *testing.T) {
	f := newEstCfgFixture(t)
	resp, body := pricingMetaGet(t, f.srv.pricingMetaMux())

	// 顶层键集合等值：加字段必须先过这条评审，防止"顺手回显一下内部量"
	for k := range resp {
		switch k {
		case "success", "modes", "points_price_money", "unit":
		default:
			t.Fatalf("出参出现契约外顶层键 %q：公开口加字段要评审是否漏内部口径", k)
		}
	}
	if s, _ := resp["unit"].(string); s != "points" {
		t.Fatalf("unit 应恒为 points（对外唯一计量口径），got %q", s)
	}

	// 缺省系数＝代码缺省按积分汇率折算：pro K=160→每千字 400 积分、F=3000→7.5 积分；
	// fast K=60→150、F=1200→3。**这里必须留 7.5 这种小数**，取整成 8 就是页面向客户报错数。
	modes := pricingMetaModes(t, resp)
	if got := pricingFloat(t, modes, "pro", "points_per_1k_chars"); got != 400.0 {
		t.Fatalf("pro 每千字积分档应为 160×1000÷400=400，got %v", got)
	}
	if got := pricingFloat(t, modes, "pro", "points_fixed"); got != 7.5 {
		t.Fatalf("pro 固定积分档应为 3000÷400=7.5（不许取整成 8），got %v", got)
	}
	if got := pricingFloat(t, modes, "fast", "points_per_1k_chars"); got != 150.0 {
		t.Fatalf("fast 每千字积分档应为 60×1000÷400=150，got %v", got)
	}
	if got := pricingFloat(t, modes, "fast", "points_fixed"); got != 3.0 {
		t.Fatalf("fast 固定积分档应为 1200÷400=3，got %v", got)
	}

	// 每积分人民币单价＝汇率×尺子÷1e8（分→元、每百万 token），出厂档 400×24917 → 0.099668 元
	price, _ := resp["points_price_money"].(float64)
	wantPrice := round6(float64(store.DefaultPointsTokensRate) * float64(store.DefaultPriceFenPerMillionTokens) / 1e8)
	if price != wantPrice {
		t.Fatalf("points_price_money 应 =%v，got %v", wantPrice, price)
	}
	// 面值等式：3,000 积分折回来仍是 ¥299（容差 1 分）。只动汇率不动尺子时这条会断，
	// 断点由 C 段负向对照实证，免得后人以为这是一句恒真的漂亮话。
	if d := math.Abs(price*3000 - 299); d > 0.01 {
		t.Fatalf("3,000 积分折算金额必须贴着套餐面值 ¥299，got %.4f（差 %.4f）", price*3000, d)
	}

	// 零 token 裸值（AGENTS §一·5）：整段响应体不许出现 "token" 字样、四枚内部配置键名，
	// 也不许出现 K/F/尺子的 token 裸值（出厂档下这些数都不该出现在积分口径的出参里）。
	lower := strings.ToLower(body)
	for _, banned := range []string{
		"token", "est_tokens_per_char", "est_tokens_fixed", "points_tokens_rate",
		"price_fen", "3000", "1200", "33222", "24917",
	} {
		if strings.Contains(lower, banned) {
			t.Fatalf("公开算价口泄露内部口径 %q：body=%s", banned, body)
		}
	}
}

// B 公式同源：页面用公示系数算出的积分 == 后端建单预检的积分（公示即扣费，否则是虚假宣传）
func TestPricingMetaFormulaMatchesPrecheck(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.pricingMetaMux()

	// pagePoints 前端算法（与 PriceComparePage 的算式逐字同式）：
	// 积分 = 固定积分 + 字符数 ÷ 1000 × 每千字积分 × 语种数，末尾四舍五入取整。
	pagePoints := func(resp map[string]interface{}, mode string, chars, langs float64) float64 {
		m := pricingMetaModes(t, resp)
		per1k := pricingFloat(t, m, mode, "points_per_1k_chars")
		fixed := pricingFloat(t, m, mode, "points_fixed")
		return float64(int64(fixed + chars/1000*per1k*langs + 0.5))
	}
	// backPoints 后端算法：预检折 token（含 token 层 int64 截断），再按 store 口径折积分。
	backPoints := func(mode string, chars, langs int) int64 {
		est := estimateTicketTokens(int64(chars), langs, mode, estParams{
			kPro:      f.srv.estTokensPerChar("pro"),
			kFast:     f.srv.estTokensPerChar("fast"),
			fixedPro:  f.srv.estTokensFixed("pro"),
			fixedFast: f.srv.estTokensFixed("fast"),
		})
		return f.st.PointsFromTokens(est)
	}

	// 三档字数 × 两模式：短单（固定项主导，正是 F-72 要修的那一段）、中单、长单（线性项主导）。
	// 期望值**由后端口径现场推导**再与页面口径对撞——本文件不再抄一份手算数，
	// 否则超管调档时先红的会是这条测试而不是产品。
	cases := []struct {
		mode  string
		chars int
		langs int
	}{
		{"pro", 19, 1}, {"pro", 67, 2}, {"fast", 100, 1}, {"fast", 1, 3},
		{"pro", 2000, 3}, {"pro", 80000, 5}, {"fast", 80000, 1}, {"pro", 1, 1},
	}
	resp, _ := pricingMetaGet(t, h)
	for _, c := range cases {
		got := pagePoints(resp, c.mode, float64(c.chars), float64(c.langs))
		want := float64(backPoints(c.mode, c.chars, c.langs))
		if got != want {
			t.Fatalf("%s %d字×%d语：页面公式 %v 积分 ≠ 建单预检 %v 积分（公示即扣费，两者必须同式）",
				c.mode, c.chars, c.langs, got, want)
		}
	}

	// 非整数系数（超管调档的合法形态）：token 层截断 vs 积分层取整会差出不到 1 积分，
	// 属量化噪声，故这一段判据放宽为 ≤1 积分（放宽理由见文件头 B 段）。
	if err := f.st.SetConfig(cfgEstKPro, "160.5"); err != nil {
		t.Fatalf("配非整数 k_pro 失败: %v", err)
	}
	resp2, _ := pricingMetaGet(t, h)
	for _, chars := range []int{1, 37, 512, 9999, 80000} {
		got := pagePoints(resp2, "pro", float64(chars), 1)
		want := float64(backPoints("pro", chars, 1))
		if d := got - want; d > 1 || d < -1 {
			t.Fatalf("非整数系数下页面与预检差 %v 积分（应 ≤1）：chars=%d got=%v want=%v", d, chars, got, want)
		}
	}
}

// C 改档跟随：汇率与尺子成对移动时全部读数一起动且面值不动；只动一头时面值必须断
func TestPricingMetaFollowsRepricingPair(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.pricingMetaMux()

	// —— 成对改档回 F-78 之前的旧档（1:300 + 尺子 33222）：
	//     pro 每千字 533.333 积分、固定项 10 积分、每积分 0.099666 元，面值仍 ¥299
	if err := f.st.SetConfig("points_tokens_rate", "300"); err != nil {
		t.Fatalf("配汇率失败: %v", err)
	}
	if err := f.st.SetConfig("price_fen_per_million_tokens", "33222"); err != nil {
		t.Fatalf("配尺子失败: %v", err)
	}
	resp, _ := pricingMetaGet(t, h)
	modes := pricingMetaModes(t, resp)
	if got := pricingFloat(t, modes, "pro", "points_per_1k_chars"); got != 533.333333 {
		t.Fatalf("改档后 pro 每千字积分应 =160×1000÷300=533.333333，got %v", got)
	}
	if got := pricingFloat(t, modes, "pro", "points_fixed"); got != 10.0 {
		t.Fatalf("改档后 pro 固定积分应 =3000÷300=10，got %v", got)
	}
	price, _ := resp["points_price_money"].(float64)
	if price != 0.099666 {
		t.Fatalf("改档后每积分单价应 =300×33222÷1e8=0.099666，got %v", price)
	}
	if d := math.Abs(price*3000 - 299); d > 0.01 {
		t.Fatalf("成对改档后面值必须仍是 ¥299（got %.4f，差 %.4f）", price*3000, d)
	}

	// —— 只动一头（汇率回 400、尺子留在 33222）：面值必须被推离 ¥299。
	//    这条是负向对照，证明上面的面值等值锁真在咬人——F-12 当年正是"只改一头"造成的 10% 口径打架。
	if err := f.st.SetConfig("points_tokens_rate", "400"); err != nil {
		t.Fatalf("配汇率失败: %v", err)
	}
	resp2, _ := pricingMetaGet(t, h)
	price2, _ := resp2["points_price_money"].(float64)
	if math.Abs(price2*3000-299) < 1 {
		t.Fatalf("负向对照失效：只动汇率不动尺子时面值竟仍贴着 ¥299，说明面值锁形同虚设（price=%v）", price2)
	}
}

// D 脏配置回退：公开页显示读侧真正在用的数；固定项显式 0 必须露 0；除零不得送 NaN/Inf
func TestPricingMetaFallsBackToEffectiveValues(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.pricingMetaMux()

	// 脏线性系数（非数字）：读侧回代码缺省 160 → 每千字仍是 400 积分。
	// 绝不能把脏值原样吐给界面，更不能把系数清零（清零＝预检放行，正是 F-41 事故形态）。
	if err := f.st.SetConfig(cfgEstKPro, "abc"); err != nil {
		t.Fatalf("配脏 k_pro 失败: %v", err)
	}
	resp, _ := pricingMetaGet(t, h)
	if got := pricingFloat(t, pricingMetaModes(t, resp), "pro", "points_per_1k_chars"); got != 400.0 {
		t.Fatalf("脏 k_pro 应回退代码缺省（每千字 400 积分），got %v", got)
	}

	// 脏汇率（0）：store 兜底回出厂 400，出参不得出现 null/NaN/Inf（除零必须兜住）
	if err := f.st.SetConfig(cfgEstKPro, ""); err != nil {
		t.Fatalf("清空 k_pro 失败: %v", err)
	}
	if err := f.st.SetConfig("points_tokens_rate", "0"); err != nil {
		t.Fatalf("配脏汇率失败: %v", err)
	}
	resp2, body2 := pricingMetaGet(t, h)
	for _, bad := range []string{"null", "NaN", "Inf"} {
		if strings.Contains(body2, bad) {
			t.Fatalf("脏汇率下出参出现 %q（除零没兜住）: %s", bad, body2)
		}
	}
	if got := pricingFloat(t, pricingMetaModes(t, resp2), "pro", "points_per_1k_chars"); got != 400.0 {
		t.Fatalf("脏汇率应回落出厂 400（每千字仍 400 积分），got %v", got)
	}

	// 固定项显式配 0 是合法运维选择（明知短单低估仍退回纯线性），必须如实露 0，
	// 不许被当成脏值抹回 7.5——那会让页面显示的公式和实际扣费差出一个截距。
	if err := f.st.SetConfig(cfgEstFixedPro, "0"); err != nil {
		t.Fatalf("配 fixed_pro=0 失败: %v", err)
	}
	resp3, _ := pricingMetaGet(t, h)
	if got := pricingFloat(t, pricingMetaModes(t, resp3), "pro", "points_fixed"); got != 0.0 {
		t.Fatalf("fixed_pro 显式配 0 必须露 0（那是合法档位），got %v", got)
	}
}

// E 方法闸：非 GET 一律 405 且走统一错误出口（带 code，不许 200 承载失败，F-64①）
func TestPricingMetaRejectsNonGet(t *testing.T) {
	f := newEstCfgFixture(t)
	h := f.srv.pricingMetaMux()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/pricing/meta", bytes.NewReader([]byte(`{}`)))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 公开算价口应 405，got %d body=%s", w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("405 错误体不是 JSON: %v body=%s", err, w.Body.String())
	}
	if c, _ := out["code"].(string); c != "METHOD_NOT_ALLOWED" {
		t.Fatalf("405 必须带统一错误码（前端/SDK 按 code 分支），got %+v", out)
	}
	if okv, _ := out["success"].(bool); okv {
		t.Fatalf("失败响应不得带 success:true，got %+v", out)
	}
}
