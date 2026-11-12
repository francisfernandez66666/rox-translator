// ============ sales_facts_anon_gate_test.go · 职责说明 ============
// 四期 W1（施工图 §4.1.3-②③⑤⑥、§4.1.6 的 F2–F7）：**匿名托管公开页也能拿到事实集**，
// 且"多产品串台 / 跨租户 / 非 live"三档一律 fail-closed。
//
// 证什么（每条对应施工图一行）：
//
//	F2① 同一产品页：匿名与登录拿到**同一份**事实集（三期那句 `u == nil` 早退的正面靶子）；
//	F2② 客户端伪造页面头：本进程按它解析（事实真的注入了），但入站头被清、上游一个字节都收不到；
//	F3   产品页 A 只带 A 的事实，**不含** B 的值（伴随 B 的正向对照：直读读口确有 B 的值）；
//	F4   多产品租户无页面 ⇒ 不下发 + scope_ambiguous 计数 +1；
//	F5   单产品租户无页面 ⇒ 自动定档并把 E 的值注入；
//	F6   跨租户登录用户请求别人的产品页 ⇒ 不下发（伴随"请求自己产品页照常注入"的正向对照）；
//	F7   draft 产品的公开页 ⇒ 不下发（该产品的事实本身是准入的，证明拦它的是产品状态不是事实状态）。
//
// 反证（M0269/M0270/M0271）：分别拆"匿名早退"、"产品过滤传空串"、"租户一致性校验"，
// 三枚各自只红对应那一条腿（见 scripts/gate_mutants.tsv）。
//
// ★ 事实用**直插 + PreGenerateFactI18n** 建：`store.ConfirmFact` 会写 locked=1，而准入读口要求
// locked=0（现网已登记的口径不一致，见 PROGRESS 四期 W1 记录），所以夹具走直插并用生产预生成口落译文。
// ================================================
package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"translator/internal/db"
	mainengine "translator/internal/engine"
	"translator/internal/kb"
	"translator/internal/store"
)

// w1FactTranslator 假翻译腿：英文回 EN(原文)，其余语种回 语种:原文（只为把译文行落齐）。
// 与 PreGenerateFactI18n 的调用签名逐字对齐（store.FactTranslator）。
type w1FactTranslator struct{}

// SingleLangTranslate 实现 store.FactTranslator（只有 en 的内容参与断言）。
func (w1FactTranslator) SingleLangTranslate(_ context.Context, zhText, targetLang string, _ []*kb.Row, _, _ string) (string, error) {
	if targetLang == "en" {
		return "EN(" + zhText + ")", nil
	}
	return targetLang + ":" + zhText, nil
}

// w1SeedFact 直插一条 product_facts（精确控制准入三条件 + 产品归属）并预生成 12 语种译文。
// 返回事实主键。★ 测试内不写死语句复用生产口：译文一律走 PreGenerateFactI18n。
func w1SeedFact(t *testing.T, st *store.Store, tid int64, productID, kind, key, value, status, visibility string, locked, priority int) int64 {
	t.Helper()
	id, err := db.InsertID(st.DB(), db.CurrentDialect(), "id",
		`INSERT INTO product_facts
		 (tenant_id, product_id, kind, key, value, status, visibility, source, locked, created_at, updated_at, priority)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		tid, productID, kind, key, value, status, visibility, "manual", locked,
		"2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", priority)
	if err != nil {
		t.Fatalf("插事实失败（tid=%d prod=%q key=%q）: %v", tid, productID, key, err)
	}
	if err := st.PreGenerateFactI18n(context.Background(), tid, id, value, w1FactTranslator{}); err != nil {
		t.Fatalf("事实译文预生成失败（tid=%d id=%d）: %v", tid, id, err)
	}
	return id
}

// seedW1Facts 给 F2–F7 铺齐七条事实（每个产品一条准入 + A 的一条受控）。
//
// 值刻意互不相同：串台/错产品**读得出具体是谁的值**，不需要靠条数猜。
func seedW1Facts(t *testing.T, fx *w1Fixture) {
	t.Helper()
	st := fx.srv.Store
	// 租户 7：A 一条准入 + 一条受控（visibility=gated ⇒ 只进受控键、值不下发）。
	w1SeedFact(t, st, 7, fx.A.ID, "moq", "moq", "MOQ-A-50", "confirmed", "public", 0, 100)
	w1SeedFact(t, st, 7, fx.A.ID, "price", "retail_price", "CTRL-A-SECRET", "confirmed", "gated", 0, 90)
	// 租户 7：B 一条准入（F3 的"不该出现"那一条 + 正向对照）。
	w1SeedFact(t, st, 7, fx.B.ID, "moq", "moq", "MOQ-B-99", "confirmed", "public", 0, 100)
	// 租户 7：D 是 draft 产品，其事实本身完全准入 ⇒ 挡住 F7 的只能是产品状态。
	w1SeedFact(t, st, 7, fx.D.ID, "moq", "moq", "MOQ-DRAFT-11", "confirmed", "public", 0, 100)
	// 租户 8：C（F6 正向对照与"别人产品页"的靶子）。
	w1SeedFact(t, st, 8, fx.C.ID, "moq", "moq", "MOQ-C-77", "confirmed", "public", 0, 100)
	// 租户 9：E（单产品自动定档）。
	w1SeedFact(t, st, 9, fx.E.ID, "moq", "moq", "MOQ-E-33", "confirmed", "public", 0, 100)
}

// w1DoProxy 打一轮访客 chat：token 为空即匿名；headers 可塞页面上下文头/X-App-Lang。
// 返回响应与**同一个入站请求对象**（F2② 要在 handler 返回后读它的头，证"入站已被清"）。
func w1DoProxy(s *Server, token string, headers map[string]string) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequest(http.MethodPost, "/assist-api/api/assist/chat",
		strings.NewReader(`{"session":"w1s","tok":"w1t","message":"你们的最小起订量是多少？"}`))
	r.Header.Set("X-App-Lang", "en")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleAssistOpenProxy(w, r)
	return w, r
}

// w1DecodeUpstreamFacts 取上游收到的那份事实集：先验签（两侧口径同源）、再解码。
// 没有事实集头 ⇒ Fatal（调用方要先单独判"该不该有"）。
func w1DecodeUpstreamFacts(t *testing.T, up *salesFactsUpstream) mainengine.SalesFactsSet {
	t.Helper()
	if up.gotFacts == "" {
		t.Fatalf("上游没收到事实集头（version=%q sig=%q）", up.gotVersion, up.gotSig)
	}
	set, err := mainengine.VerifySalesFacts([]byte("test-w1-key"), up.gotFacts, up.gotTs, up.gotSig, time.Now())
	if err != nil {
		t.Fatalf("事实集验签失败（签名口径漂移）: %v", err)
	}
	return set
}

// w1FactValues 事实集里的值序列（顺带证明"键值对没被换成受控清单"）。
func w1FactValues(set mainengine.SalesFactsSet) []string {
	out := make([]string, 0, len(set.Facts))
	for _, f := range set.Facts {
		out = append(out, f.Value)
	}
	return out
}

// w1PayloadHas 在事实集**解码后的 JSON 字节**里找子串——扫描对象是真正发出去的形态，
// 不是结构体字段（受控值若哪天被塞进任何新字段，这里也能抓到）。
func w1PayloadHas(payloadB64, needle string) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payloadB64))
	if err != nil {
		return false
	}
	return strings.Contains(string(raw), needle)
}

// TestSalesFactsAnonymousProductPage F2：匿名托管公开页（主战场）拿到与登录同一份事实集；
// 伪造页面头被本进程消费、但绝不继续往下传。
func TestSalesFactsAnonymousProductPage(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)
	page := w1PagePath(fx.A)

	// ——① 同一页：匿名与登录得到同一份证据 ——
	_, _ = w1DoProxy(fx.srv, fx.tok7, map[string]string{productPageHeader: page})
	loggedSet := w1DecodeUpstreamFacts(t, fx.up)
	if loggedSet.TenantID != 7 {
		t.Fatalf("登录档事实集租户不对：tid=%d（期望 7）", loggedSet.TenantID)
	}
	if got := w1FactValues(loggedSet); !reflect.DeepEqual(got, []string{"EN(MOQ-A-50)"}) {
		t.Fatalf("登录档事实值不对：%v（期望只有 A 的 MOQ）", got)
	}

	_, _ = w1DoProxy(fx.srv, "", map[string]string{productPageHeader: page})
	anonSet := w1DecodeUpstreamFacts(t, fx.up)
	if anonSet.TenantID != 7 {
		t.Fatalf("匿名档 TenantID 应取产品自身租户（7），实得 %d", anonSet.TenantID)
	}
	if !reflect.DeepEqual(w1FactValues(anonSet), w1FactValues(loggedSet)) || !reflect.DeepEqual(anonSet.Controlled, loggedSet.Controlled) {
		t.Fatalf("同一产品页两档事实集不同：anon facts=%v controlled=%v / login facts=%v controlled=%v",
			w1FactValues(anonSet), anonSet.Controlled, w1FactValues(loggedSet), loggedSet.Controlled)
	}
	// 受控键到位但**值一个字节都没出栈**（受控值与准入值在同一条事实行上，最容易串）。
	var hasControlled bool
	for _, c := range anonSet.Controlled {
		if c.Kind == "price" && c.Key == "retail_price" {
			hasControlled = true
		}
	}
	if !hasControlled {
		t.Fatalf("受控键 retail_price 未进受控清单：%v", anonSet.Controlled)
	}
	if w1PayloadHas(fx.up.gotFacts, "CTRL-A-SECRET") {
		t.Fatal("受控值 CTRL-A-SECRET 出现在下发载荷里——受控键清单必须是只有 Kind/Key 的形态")
	}

	// ——② 伪造页面头：本进程消费它，但不传给上游 ——
	rec, r := w1DoProxy(fx.srv, "", map[string]string{productPageHeader: page})
	if rec.Code != 200 {
		t.Fatalf("匿名转发应 200，实际 %d", rec.Code)
	}
	// 正向：解析发生在 strip 之前（否则拿到的是空作用域，事实一条都不会有）。
	forgedSet := w1DecodeUpstreamFacts(t, fx.up)
	if got := w1FactValues(forgedSet); !reflect.DeepEqual(got, []string{"EN(MOQ-A-50)"}) {
		t.Fatalf("伪造页面头没有被本进程消费（作用域没解析出来）：facts=%v", got)
	}
	// 负向两条：入站头已清 + 上游收到的同名头为空。
	if v := r.Header.Get(productPageHeader); v != "" {
		t.Fatalf("入站页面头未被清除（残留 %q）——客户端那份被继续往下传了", v)
	}
	if fx.up.gotPage != "" {
		t.Fatalf("页面头到达上游（%q）——它只是链路卫生，绝不能当内部契约继续传", fx.up.gotPage)
	}
}

// TestSalesFactsProductScopedNoCrosstalk F3：产品页 A 只带 A 的值，B 的值不得出现（带 B 正向对照）。
func TestSalesFactsProductScopedNoCrosstalk(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)

	// 正向对照：B 的值确实在库里、也确实能从读口取出（否则"没出现"可能只是数据没铺上）。
	bFacts, _, err := fx.srv.Store.ListSalesFactsForInjection(7, fx.B.ID, "en")
	if err != nil {
		t.Fatalf("读口直读 B 失败: %v", err)
	}
	var bHas bool
	for _, f := range bFacts {
		if f.Value == "EN(MOQ-B-99)" {
			bHas = true
		}
	}
	if !bHas {
		t.Fatalf("B 的准入事实读不出来（正向对照失效，本锁空转）：%v", bFacts)
	}

	_, _ = w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.A)})
	set := w1DecodeUpstreamFacts(t, fx.up)
	for _, v := range w1FactValues(set) {
		if strings.Contains(v, "MOQ-B-99") {
			t.Fatalf("A 的产品页带出了 B 的值（%q）——产品过滤被拆（三期串台那条）: %v", v, w1FactValues(set))
		}
	}
	if !reflect.DeepEqual(w1FactValues(set), []string{"EN(MOQ-A-50)"}) {
		t.Fatalf("A 的产品页事实集不对：%v（期望只有 A 的值）", w1FactValues(set))
	}
}

// TestSalesFactsAmbiguousTenantNoHeader F4：多产品租户无页面 ⇒ 不下发 + scope_ambiguous +1。
func TestSalesFactsAmbiguousTenantNoHeader(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)
	before := fx.srv.salesFactsHealth()["scope_ambiguous"].(int64)

	rec, _ := w1DoProxy(fx.srv, fx.tok7, nil)
	if rec.Code != 200 {
		t.Fatalf("多产品租户请求应 200（只是不注入），实际 %d", rec.Code)
	}
	if fx.up.gotFacts != "" || fx.up.gotVersion != "" {
		t.Fatalf("多产品无作用域仍下发了事实集（facts=%q ver=%q）——fail-closed 被拆成'全租户'",
			fx.up.gotFacts, fx.up.gotVersion)
	}
	after := fx.srv.salesFactsHealth()["scope_ambiguous"].(int64)
	if after-before != 1 {
		t.Fatalf("scope_ambiguous 计数增量应为 1，实际 %d（before=%d after=%d）", after-before, before, after)
	}
}

// TestSalesFactsSingleProductAutoScope F5：单产品租户无页面 ⇒ 自动定档并注入该产品的事实。
func TestSalesFactsSingleProductAutoScope(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)

	rec, _ := w1DoProxy(fx.srv, fx.tok9, nil)
	if rec.Code != 200 {
		t.Fatalf("单产品租户请求应 200，实际 %d", rec.Code)
	}
	set := w1DecodeUpstreamFacts(t, fx.up)
	if set.TenantID != 9 {
		t.Fatalf("单产品定档的租户不对：%d（期望 9）", set.TenantID)
	}
	if got := w1FactValues(set); !reflect.DeepEqual(got, []string{"EN(MOQ-E-33)"}) {
		t.Fatalf("单产品租户未注入自家唯一产品的事实：%v", got)
	}
	if fx.up.gotPage != "" {
		t.Fatalf("客户端没送页面头，上游却收到 %q（查作用域解析把它当成了产品页）", fx.up.gotPage)
	}
}

// TestSalesFactsCrossTenantDenied F6：跨租户登录用户请求别人的产品页 ⇒ 不下发。
// 正向对照：同一用户请求**自己**的产品页照常注入——证明拦它的是租户一致性，不是"任何产品页都拦"。
func TestSalesFactsCrossTenantDenied(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)

	// ① 租户 8 的用户请求租户 7 的产品页 A。
	_, _ = w1DoProxy(fx.srv, fx.tok8, map[string]string{productPageHeader: w1PagePath(fx.A)})
	if fx.up.gotFacts != "" {
		set, err := mainengine.DecodeSalesFacts(fx.up.gotFacts)
		if err == nil {
			t.Fatalf("跨租户登录用户拿到了别人产品的事实：tid=%d facts=%v", set.TenantID, w1FactValues(set))
		}
		t.Fatal("跨租户登录用户收到了事实集头（解码失败也得先判'不该有'）")
	}

	// ② 正向对照：同一用户请求自己租户的产品页 C ⇒ 正常注入 C 的值。
	_, _ = w1DoProxy(fx.srv, fx.tok8, map[string]string{productPageHeader: w1PagePath(fx.C)})
	set := w1DecodeUpstreamFacts(t, fx.up)
	if set.TenantID != 8 {
		t.Fatalf("自家产品页的租户不对：%d（期望 8）", set.TenantID)
	}
	if got := w1FactValues(set); !reflect.DeepEqual(got, []string{"EN(MOQ-C-77)"}) {
		t.Fatalf("自家产品页事实集不对：%v（正向对照失效 ⇒ 上面那条'不下发'可能是闸门整体失灵）", got)
	}
}

// TestSalesFactsDraftProductDenied F7：draft 产品的公开页不下发（其事实本身是准入的）。
func TestSalesFactsDraftProductDenied(t *testing.T) {
	fx := newW1Fixture(t)
	seedW1Facts(t, fx)

	// 正向对照：D 的事实确实准入（直读读口拿得到），否则本锁测的是"数据没铺"。
	dFacts, _, err := fx.srv.Store.ListSalesFactsForInjection(7, fx.D.ID, "en")
	if err != nil {
		t.Fatalf("读口直读 D 失败: %v", err)
	}
	var dHas bool
	for _, f := range dFacts {
		if f.Value == "EN(MOQ-DRAFT-11)" {
			dHas = true
		}
	}
	if !dHas {
		t.Fatalf("D 的准入事实读不出来（正向对照失效，本锁空转）: %v", dFacts)
	}

	_, _ = w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.D)})
	if fx.up.gotFacts != "" {
		t.Fatal("draft 产品的公开页下发了事实集——产品状态闸被拆（'没上线的东西不该对外开口'）")
	}
}
