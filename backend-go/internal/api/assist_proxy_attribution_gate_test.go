// ============ assist_proxy_attribution_gate_test.go · 职责说明 ============
// A04b（三期 W2，§0.4 三段链路）：跨进程契约的行为锁 + 源码序锁。
//
// 锁住四档（cases<4 即 Fatal，射程自证）：
//
//	① 入站伪造事实集头 ⇒ 先删再自赋，上游一个都收不到；匿名 ⇒ 不写归因；答复字节原样；
//	② 归因回写失败（表被毁）⇒ 访客仍 200 且 payload 一字不变（fire-and-forget）；
//	③ payload 非 JSON ⇒ 不 panic、不写库、字节原样；
//	④ 事实集超 200 条 / 超 32 KB ⇒ 截断且置 truncated=1（LimitSalesFacts 纯函数）。
//
// 外加两腿：正向对照（登录租户 ⇒ 签名头可验、归因行按 message_id 精确落库、pending_human 落行）；
// 源码序锁（w.Write(payload) 必须先于 s.writeBackAttribution —— M0210 的靶）。
// ================================================
package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/db"
	mainengine "translator/internal/engine"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// salesFactsUpstream 假 assist 上游：记录事实集四头与请求次数，响应体可控。
type salesFactsUpstream struct {
	hits       int
	gotFacts   string
	gotTs      string
	gotSig     string
	gotVersion string
	respBody   string
}

func (a *salesFactsUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.hits++
	a.gotFacts = r.Header.Get(mainengine.SalesFactsHeader)
	a.gotTs = r.Header.Get(mainengine.SalesFactsTsHeader)
	a.gotSig = r.Header.Get(mainengine.SalesFactsSigHeader)
	a.gotVersion = r.Header.Get(mainengine.SalesFactsVersionHeader)
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(a.respBody))
}

// newSalesFactsProxyFixture 文件库主服务 + 假上游 + 租户 1 登录用户。
// ★ 用文件库不用 :memory:：连接池下 :memory: 每连接一库，DROP TABLE 故障注入会漂。
func newSalesFactsProxyFixture(t *testing.T) (*Server, *salesFactsUpstream, string) {
	t.Helper()
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatalf("打开文件库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	u, err := st.CreateUser(1, "u_sales", "hash-s", "销售租户用户", "user", 1, 0)
	if err != nil || u == nil {
		t.Fatalf("创建租户用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	up := &salesFactsUpstream{}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	t.Setenv("ASSIST_BASE_URL", srv.URL)
	t.Setenv("ASSIST_PROFILE_KEY", "test-sales-facts-key")
	return &Server{Store: st}, up, tok
}

// doSalesFactsChat 打一轮访客 chat（token 可空＝匿名；extraHeaders 模拟客户端伪造头）。
func doSalesFactsChat(s *Server, token string, extraHeaders map[string]string) (int, string) {
	r := httptest.NewRequest(http.MethodPost, "/assist-api/api/assist/chat", strings.NewReader(`{"session":"s1","tok":"t1","message":"多少钱"}`))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extraHeaders {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.handleAssistOpenProxy(w, r)
	return w.Code, w.Body.String()
}

// attributionRowCount 主库归因行数（故障注入后表不存在时返回 -1，调用方只在表健在时判 0）。
func attributionRowCount(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := db.QueryRow(s.Store.DB(), db.CurrentDialect(), `SELECT COUNT(*) FROM reply_attributions`).Scan(&n); err != nil {
		return -1
	}
	return n
}

const salesFactsAttrBody = `{"reply":"好的","attribution":{"message_id":9001,"source":"llm","model":"m1","pending_human":true}}`

// TestSalesFactsForgedHeadersStripped ①：伪造四头先删再自赋；匿名不写归因；答复字节原样。
func TestSalesFactsForgedHeadersStripped(t *testing.T) {
	s, up, _ := newSalesFactsProxyFixture(t)
	up.respBody = salesFactsAttrBody
	forged := map[string]string{
		mainengine.SalesFactsHeader:        "Zm9yZ2Vk",
		mainengine.SalesFactsTsHeader:      "1",
		mainengine.SalesFactsSigHeader:     "deadbeef",
		mainengine.SalesFactsVersionHeader: "1",
	}
	code, body := doSalesFactsChat(s, "", forged)
	if code != 200 || body != up.respBody {
		t.Fatalf("匿名转发应 200 且字节原样：code=%d body=%q", code, body)
	}
	if up.gotFacts != "" || up.gotTs != "" || up.gotSig != "" || up.gotVersion != "" {
		t.Fatalf("伪造事实集头到达上游（facts=%q ts=%q sig=%q ver=%q）——先删再自赋被拆", up.gotFacts, up.gotTs, up.gotSig, up.gotVersion)
	}
	if n := attributionRowCount(t, s); n != 0 {
		t.Fatalf("匿名请求写了 %d 行归因（W2 只认登录会话，绝不兜底进租户 1）", n)
	}
}

// TestSalesFactsSignedAndAttributionWritten 正向对照：签名头可验 + 空集协议跑通 + 归因精确落行。
func TestSalesFactsSignedAndAttributionWritten(t *testing.T) {
	s, up, tok := newSalesFactsProxyFixture(t)
	up.respBody = salesFactsAttrBody
	code, body := doSalesFactsChat(s, tok, nil)
	if code != 200 || body != up.respBody {
		t.Fatalf("登录转发应 200 且字节原样：code=%d body=%q", code, body)
	}
	if up.gotVersion != mainengine.SalesFactsVersion {
		t.Fatalf("协议版本位缺失/漂移：%q", up.gotVersion)
	}
	set, err := mainengine.VerifySalesFacts([]byte("test-sales-facts-key"), up.gotFacts, up.gotTs, up.gotSig, time.Now())
	if err != nil {
		t.Fatalf("出站事实集验签失败（两侧口径漂移）: %v", err)
	}
	if set.TenantID != 1 || len(set.Facts) != 0 {
		t.Fatalf("W2 静态形态应是租户 1 空集：tid=%d facts=%d", set.TenantID, len(set.Facts))
	}
	var tid, msgID, pending int64
	var provider string
	err = db.QueryRow(s.Store.DB(), db.CurrentDialect(),
		`SELECT tenant_id, message_id, provider, pending_human FROM reply_attributions WHERE message_id=9001`).
		Scan(&tid, &msgID, &provider, &pending)
	if err != nil {
		t.Fatalf("归因行未落库: %v", err)
	}
	if tid != 1 || provider != "assist" || pending != 1 {
		t.Fatalf("归因行口径漂移：tid=%d provider=%q pending=%d", tid, provider, pending)
	}
}

// TestSalesFactsWritebackFailureKeepsResponse ②：归因表被毁 ⇒ 回写失败只 WARN，访客 200 字节原样。
func TestSalesFactsWritebackFailureKeepsResponse(t *testing.T) {
	s, up, tok := newSalesFactsProxyFixture(t)
	up.respBody = salesFactsAttrBody
	if _, err := db.Exec(s.Store.DB(), db.CurrentDialect(), `DROP TABLE reply_attributions`); err != nil {
		t.Fatalf("故障注入失败: %v", err)
	}
	code, body := doSalesFactsChat(s, tok, nil)
	if code != 200 {
		t.Fatalf("回写失败拖垮访客响应：code=%d body=%q", code, body)
	}
	if body != up.respBody {
		t.Fatalf("回写失败改了答复字节：%q", body)
	}
}

// TestSalesFactsNonJSONPayloadNoWrite ③：上游异常体（HTML）⇒ 不 panic、不写库、字节原样。
func TestSalesFactsNonJSONPayloadNoWrite(t *testing.T) {
	s, up, tok := newSalesFactsProxyFixture(t)
	up.respBody = `<html>upstream broke</html>`
	code, body := doSalesFactsChat(s, tok, nil)
	if code != 200 || body != up.respBody {
		t.Fatalf("非 JSON 应原样透传：code=%d body=%q", code, body)
	}
	if n := attributionRowCount(t, s); n != 0 {
		t.Fatalf("非 JSON 响应写了 %d 行归因", n)
	}
}

// TestSalesFactsLimitTruncates ④：超条数/超字节 ⇒ 截断置位（纯函数，两侧共用同一把尺）。
func TestSalesFactsLimitTruncates(t *testing.T) {
	set := mainengine.SalesFactsSet{TenantID: 1, Lang: "zh"}
	for i := 0; i < mainengine.MaxSalesFacts+1; i++ {
		set.Facts = append(set.Facts, mainengine.SalesFact{Kind: "spec", Key: "k", Value: "v", Lang: "zh"})
	}
	out := mainengine.LimitSalesFacts(set)
	if !out.Truncated || len(out.Facts) != mainengine.MaxSalesFacts {
		t.Fatalf("超条数未截断：truncated=%v n=%d", out.Truncated, len(out.Facts))
	}
	big := mainengine.SalesFactsSet{TenantID: 1, Lang: "zh",
		Facts: []mainengine.SalesFact{{Kind: "spec", Key: "k", Value: strings.Repeat("x", mainengine.MaxSalesFactsBytes), Lang: "zh"}}}
	out2 := mainengine.LimitSalesFacts(big)
	b, err := json.Marshal(out2)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(b) > mainengine.MaxSalesFactsBytes {
		t.Fatalf("超字节未截断：%d B", len(b))
	}
	if len(out2.Facts) > 0 && !out2.Truncated {
		t.Fatalf("丢事实必须置 truncated=1")
	}
}

// TestSalesFactsWriteAfterResponse 源码序锁（M0210 的靶）：w.Write(payload) 必须先于回写调用。
func TestSalesFactsWriteAfterResponse(t *testing.T) {
	b, err := os.ReadFile("assist_open_proxy.go")
	if err != nil {
		t.Fatalf("读转发层源码失败: %v", err)
	}
	src := string(b)
	wi := strings.Index(src, "w.Write(payload)")
	ai := strings.Index(src, "s.writeBackAttribution(")
	if wi < 0 || ai < 0 {
		t.Fatalf("锚点缺失（write=%d attr=%d）——出站口或回写装配被摘", wi, ai)
	}
	if wi > ai {
		t.Fatal("w.Write(payload) 挪到回写之后：回写阻塞/异常会拖住访客答复（M0210）")
	}
}
