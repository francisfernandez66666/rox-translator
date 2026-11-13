// ============ assist_attribution_product_gate_test.go · 职责说明 ============
// 四期 W1（施工图 §4.1.3-⑥、§4.1.6 的 F10 / F11）：**归因回写的写入边界**。
//
// 这一族锁的从来不是"某一列有没有写"，而是三期那句「匿名 = 不写」被拆开之后，
// 每一档**各自该写什么、各自不该写什么**：
//
//	F10  产品维度真的落库：匿名产品页那一条归因行 tenant_id = **产品所属租户**（不是默认租户 1）、
//	     product_id = 该产品主键；登录档同样带（两档各判一条）。
//	     反证 M0309：把 sc.ProductID 传成 "" ⇒ 两档产品维度都恒空。
//	F11① 幂等刷新**不抹**非空 product_id；反向对照（第一次空、第二次带作用域）必须**补得上**
//	     ——只测"不抹"会给"永远不更新"那种坏法放行。
//	     反证 M0310：去掉 CASE WHEN 直接覆盖 ⇒ 抹除腿红。
//	F11② 匿名只放行 product_page_anon：匿名无页面 / 非 live 产品页 ⇒ 零行（同一夹具里正向对照一条）。
//	     反证 M0311（原施工图写作"恢复 u == nil 全早退"，那与 F2 同一条腿、在 F11 无射程；
//	     按施工时裁定改用「匿名租户兜底成 1」这一坏法，见 scripts/gate_mutants.tsv 的 note）。
//	F11③ 匿名回写**不建** handoff_sessions（孤儿行），且 conversation_id 保持 0；
//	     正向对照＝登录档同一会话 id 必须建行 —— 否则"表坏了"也能让这条腿绿。
//	     反证 M0312：匿名也调 EnsureHandoffSession ⇒ 孤儿行出现。
//
// ★ 夹具复用 sales_facts_scope_gate_test.go 的 newW1Fixture（产品 A/B/D@7、C@8、E@9、四枚 token），
// 转发层走真 handleAssistOpenProxy（回写在 w.Write 之后**同步**发生，返回即可读库）。
// ================================================
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"translator/internal/db"
)

// w1AttrBody 造一份带归因段的上游响应体（message_id / session_id 由用例点名）。
// 只带主服务 writeBackAttribution 认的那几个字段，其余原样透传语义与 salesFactsAttrBody 一致。
func w1AttrBody(messageID int64, sessionID string) string {
	var doc struct {
		Reply       string `json:"reply"`
		Attribution struct {
			MessageID int64  `json:"message_id"`
			Source    string `json:"source"`
			Model     string `json:"model"`
			SessionID string `json:"session_id"`
		} `json:"attribution"`
	}
	doc.Reply = "好的"
	doc.Attribution.MessageID = messageID
	doc.Attribution.Source = "llm"
	doc.Attribution.Model = "w1-model"
	doc.Attribution.SessionID = sessionID
	b, err := json.Marshal(doc)
	if err != nil {
		panic("w1AttrBody: " + err.Error())
	}
	return string(b)
}

// w1AttrRow 归因行的三个关键列（产品维度 + 会话映射）。
type w1AttrRow struct {
	TenantID       int64
	ProductID      string
	ConversationID int64
}

// w1ReadAttrRow 按 message_id 取一行；ok=false 表示该消息没有任何归因行。
func w1ReadAttrRow(t *testing.T, s *Server, messageID int64) (w1AttrRow, bool) {
	t.Helper()
	var row w1AttrRow
	err := db.QueryRow(s.Store.DB(), db.CurrentDialect(),
		`SELECT tenant_id, COALESCE(product_id,''), COALESCE(conversation_id,0)
		   FROM reply_attributions WHERE message_id=?`, messageID).
		Scan(&row.TenantID, &row.ProductID, &row.ConversationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return w1AttrRow{}, false
		}
		t.Fatalf("读归因行失败（message_id=%d）: %v", messageID, err)
	}
	return row, true
}

// w1AttrCount 归因行总数（"不该写"那一族的判据：零行，而不是"某列是空"）。
func w1AttrCount(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := db.QueryRow(s.Store.DB(), db.CurrentDialect(),
		`SELECT COUNT(*) FROM reply_attributions`).Scan(&n); err != nil {
		t.Fatalf("数归因行失败: %v", err)
	}
	return n
}

// w1HandoffCount 指定 (租户, 会话 id) 的 handoff_sessions 行数。
func w1HandoffCount(t *testing.T, s *Server, tid int64, sessionID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(s.Store.DB(), db.CurrentDialect(),
		`SELECT COUNT(*) FROM handoff_sessions WHERE tenant_id=? AND session_id=?`, tid, sessionID).Scan(&n); err != nil {
		t.Fatalf("数 handoff_sessions 失败（tid=%d sid=%q）: %v", tid, sessionID, err)
	}
	return n
}

// TestAttributionCarriesProductDimension F10：匿名与登录两档都必须带产品维度，
// 且匿名档的 tenant_id 是**产品所属租户**（这里刻意是 7，不是默认租户 1——串租户的最短判据）。
func TestAttributionCarriesProductDimension(t *testing.T) {
	fx := newW1Fixture(t)

	// ① 匿名产品页：/p/{A 的租户码}/{A 的页码} ⇒ 租户 7 + 产品 A。
	fx.up.respBody = w1AttrBody(9101, "w1-anon-9101")
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.A)})
	anon, ok := w1ReadAttrRow(t, fx.srv, 9101)
	if !ok {
		t.Fatal("匿名产品页的归因行没落库（F10 腿①：匿名 + product_page_anon 必须写）")
	}
	if anon.TenantID != 7 {
		t.Fatalf("匿名归因行租户错：实得 %d，期望 7（产品 A 的租户；兜底成 1 就是串租户，M0311 的靶）", anon.TenantID)
	}
	if anon.ProductID != fx.A.ID {
		t.Fatalf("匿名归因行产品维度错：实得 %q，期望 %q（M0309 的靶）", anon.ProductID, fx.A.ID)
	}

	// ② 登录产品页：同一页 ⇒ 同一 (租户, 产品)。
	fx.up.respBody = w1AttrBody(9102, "w1-login-9102")
	w1DoProxy(fx.srv, fx.tok7, map[string]string{productPageHeader: w1PagePath(fx.A)})
	login, ok := w1ReadAttrRow(t, fx.srv, 9102)
	if !ok {
		t.Fatal("登录档的归因行没落库")
	}
	if login.TenantID != 7 || login.ProductID != fx.A.ID {
		t.Fatalf("登录归因行维度错：实得 (tid=%d prod=%q)，期望 (7, %q)",
			login.TenantID, login.ProductID, fx.A.ID)
	}

	// ③ 单产品自动定档同样要带：租户 9 无页面 ⇒ single_product ⇒ product_id = E。
	fx.up.respBody = w1AttrBody(9103, "w1-login-9103")
	w1DoProxy(fx.srv, fx.tok9, nil)
	single, ok := w1ReadAttrRow(t, fx.srv, 9103)
	if !ok {
		t.Fatal("单产品档的归因行没落库")
	}
	if single.TenantID != 9 || single.ProductID != fx.E.ID {
		t.Fatalf("单产品档归因行维度错：实得 (tid=%d prod=%q)，期望 (9, %q)",
			single.TenantID, single.ProductID, fx.E.ID)
	}
}

// TestAttributionIdempotentProductID F11①：幂等刷新两向都得对——
//
//	(a) 已有非空 product_id：第二次回写来自无作用域路径（多产品租户、无页面头 ⇒ ambiguous）
//	    必须**不抹**；
//	(b) 第一次没有产品维度：后续带作用域的回写必须**补上**。
//
// 只说 (a) 会把"幂等分支永远不更新"放行（那同样满足"不抹"）。
func TestAttributionIdempotentProductID(t *testing.T) {
	fx := newW1Fixture(t)

	// (a) 先按匿名产品页写下非空产品维度。
	fx.up.respBody = w1AttrBody(9201, "w1-idem-9201")
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.A)})
	first, ok := w1ReadAttrRow(t, fx.srv, 9201)
	if !ok || first.ProductID != fx.A.ID {
		t.Fatalf("前置条件不成立：首写没带上产品维度（ok=%v row=%+v）", ok, first)
	}

	// 同一 message_id 再来一次，这次来自**多产品租户无页面**（租户 7 ⇒ ambiguous ⇒ ProductID=""）。
	w1DoProxy(fx.srv, fx.tok7, nil)
	kept, ok := w1ReadAttrRow(t, fx.srv, 9201)
	if !ok {
		t.Fatal("幂等刷新把行弄丢了")
	}
	if kept.ProductID != fx.A.ID {
		t.Fatalf("幂等刷新抹掉了非空 product_id：实得 %q，期望保留 %q（M0310 的靶）",
			kept.ProductID, fx.A.ID)
	}

	// (b) 反向：先写一条无产品维度的（登录 + 多产品无页面 ⇒ ambiguous）。
	fx.up.respBody = w1AttrBody(9202, "w1-idem-9202")
	w1DoProxy(fx.srv, fx.tok7, nil)
	before, ok := w1ReadAttrRow(t, fx.srv, 9202)
	if !ok {
		t.Fatal("歧义档的归因行没落库（W1 起歧义不是「不写」，是「写但产品维度为空」）")
	}
	if before.ProductID != "" {
		t.Fatalf("歧义档前置条件不成立：product_id 应为空，实得 %q", before.ProductID)
	}

	// 同一 message_id 再来一次带产品页的 ⇒ 必须补上。
	w1DoProxy(fx.srv, fx.tok7, map[string]string{productPageHeader: w1PagePath(fx.A)})
	filled, ok := w1ReadAttrRow(t, fx.srv, 9202)
	if !ok {
		t.Fatal("补写后行不见了")
	}
	if filled.ProductID != fx.A.ID {
		t.Fatalf("幂等刷新没补上产品维度：实得 %q，期望 %q（CASE 的 `? <> ''` 那一支被摘）",
			filled.ProductID, fx.A.ID)
	}
}

// TestAttributionAnonymousOnlyProductPage F11②：匿名只放行 product_page_anon 这一支。
//
// 三条不该写的匿名形态各判一次（无页面 / 非 live 产品页），并配一条正向对照——
// 否则"一条都没写"可能只是回写链路整个坏了（§三 假红那一族）。
func TestAttributionAnonymousOnlyProductPage(t *testing.T) {
	fx := newW1Fixture(t)

	// 正向对照：一个合法匿名产品页 ⇒ 恰好 1 行。
	fx.up.respBody = w1AttrBody(9301, "w1-anon-9301")
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.A)})
	if _, ok := w1ReadAttrRow(t, fx.srv, 9301); !ok {
		t.Fatal("正向对照失效：合法匿名产品页没写行——后面的负向腿是空转")
	}
	base := w1AttrCount(t, fx.srv)

	// ① 匿名无任何作用域（无页面头）⇒ denied ⇒ 零新增行。
	fx.up.respBody = w1AttrBody(9302, "w1-anon-9302")
	w1DoProxy(fx.srv, "", nil)
	// ② 匿名打非 live 产品（D 是 draft）的公开页 ⇒ denied ⇒ 零新增行。
	fx.up.respBody = w1AttrBody(9303, "w1-anon-9303")
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.D)})
	// ③ 匿名打一个不存在的产品页 ⇒ denied ⇒ 零新增行。
	fx.up.respBody = w1AttrBody(9304, "w1-anon-9304")
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: "/p/zzzzzzzz/yyyyyyyy"})

	if got := w1AttrCount(t, fx.srv); got != base {
		t.Fatalf("匿名非产品页档写了归因行：基线 %d，现 %d（M0311 同族：匿名只许放行 product_page_anon）", base, got)
	}
	for _, mid := range []int64{9302, 9303, 9304} {
		if _, ok := w1ReadAttrRow(t, fx.srv, mid); ok {
			t.Fatalf("message_id=%d 落了行：匿名在无作用域/非 live 档必须零行", mid)
		}
	}
}

// TestAttributionAnonymousSkipsHandoffSession F11③：匿名回写不建 handoff_sessions（孤儿行），
// conversation_id 保持 0；正向对照＝登录档同一会话 id 必须建行。
func TestAttributionAnonymousSkipsHandoffSession(t *testing.T) {
	fx := newW1Fixture(t)
	const sid = "w1-handoff-sid"

	// ① 匿名产品页（上游回了 session_id——它不是空，所以"没建行"只能是防线在起作用）。
	fx.up.respBody = w1AttrBody(9401, sid)
	w1DoProxy(fx.srv, "", map[string]string{productPageHeader: w1PagePath(fx.A)})
	anon, ok := w1ReadAttrRow(t, fx.srv, 9401)
	if !ok {
		t.Fatal("匿名归因行没落库——F11③ 的判据会空转（先看 F10）")
	}
	if anon.ConversationID != 0 {
		t.Fatalf("匿名归因行挂上了会话映射：conversation_id=%d，期望 0", anon.ConversationID)
	}
	if n := w1HandoffCount(t, fx.srv, 7, sid); n != 0 {
		t.Fatalf("匿名回写建了 %d 行 handoff_sessions（M0312 的靶：匿名没有身份，建出来是孤儿行）", n)
	}

	// ② 正向对照：登录档同一会话 id ⇒ 必须建行且归因行挂上映射。
	fx.up.respBody = w1AttrBody(9402, sid)
	w1DoProxy(fx.srv, fx.tok7, map[string]string{productPageHeader: w1PagePath(fx.A)})
	login, ok := w1ReadAttrRow(t, fx.srv, 9402)
	if !ok {
		t.Fatal("登录归因行没落库")
	}
	if n := w1HandoffCount(t, fx.srv, 7, sid); n != 1 {
		t.Fatalf("登录档没建 handoff_sessions（实得 %d 行）——上面那条匿名负向腿因此无法区分"+
			"「防线生效」与「表/接线坏了」", n)
	}
	if login.ConversationID <= 0 {
		t.Fatalf("登录归因行没挂上会话映射：conversation_id=%d", login.ConversationID)
	}
}

// TestAttributionProductDimensionWired 源码接线腿（M0309 的另一半）：
// writeBackAttribution 必须把 sc.ProductID 传进 ReplyAttributionRow——
// 函数签名字面上有 sc 不代表真的用了它（AGENTS §三：判据级代替不了守卫级）。
func TestAttributionProductDimensionWired(t *testing.T) {
	b, err := os.ReadFile("assist_sales_facts.go")
	if err != nil {
		t.Fatalf("读 assist_sales_facts.go 失败: %v", err)
	}
	// 折叠空白后判（gofmt 对齐会随相邻 key 长度变，按字面空格判是"锁比实现先坏"的老病）。
	src := strings.Join(strings.Fields(string(b)), " ")
	for _, need := range []string{
		"row := store.ReplyAttributionRow{",
		"ProductID: sc.ProductID,",
	} {
		if !strings.Contains(src, need) {
			t.Fatalf("assist_sales_facts.go 里找不到锚点 %q——产品维度可能被传成 \"\"（M0309 的靶）", need)
		}
	}
	// 匿名档必须按 product_page_anon 判（而不是"匿名一律写"或"匿名一律不写"）。
	if !strings.Contains(src, "sc.Reason != salesScopeProductPageAnon || sc.TenantID <= 0") {
		t.Fatal("匿名放行判据漂移：必须只放行 product_page_anon 且租户>0（F11② 的靶）")
	}
}
