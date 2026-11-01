// ============ lead_public_gate_test.go 职责说明 ============
// DEF-P0-03（2026-11-01）门禁 C-3.2：留资免验证降级通道（POST /api/lead/public）的行为表。
//
// 十二条判据按施工方案那张表逐行落码，另加三条"登记表说自己守得住"的形状腿。
// 三条写法上的讲究（每条都对应一种"锁会撒谎"的坏法）：
//
//	① **测试自己造 IP**：allow/record 都以 IP 为键，而本端点的最小间隔是 10s——
//	   共用一个 IP 的用例会在第二条就撞 429，然后被读成"接口坏了"；每个用例各占一个 IP。
//	② **要跳开间隔只能改库里的时间戳，不许 sleep**：guard_int 的 WindowStart 由 record() 写成 now，
//	   测试里把那一行拨回 11 秒前是**确定性**的；sleep(11s) 属于"靠调度器碰运气"的假判据（同 §三 并发用例那条）。
//	③ **升级信号必须配"不升级"的对照**：只验"命中后要验证码"等于什么都没验——
//	   把判据写成恒真（例如拿 >=0 比较）时，正向那条照样绿。
//
// ⚠️ 人机验证在本文件一律"启用 provider 但不给 token"：verifyCaptcha 在 token 为空时**先拒**、
//
//	不外呼 Cloudflare（见 captcha.go 的判据顺序），所以这里不会出现真实网络依赖。
//
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

// leadPubHarness 建一套「Store + registerGuard 就绪、无鉴权」的免验证通道环境。
// 返回的 *sql.DB 是给用例**拨时间戳**用的（见文件头第 ② 条），不是让用例绕过业务写库。
func leadPubHarness(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	pinSqliteDialect(t)
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	old := config.C
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", fmt.Sprintf("file:leadpub_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())))
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("建 Store 失败: %v", err)
	}
	return &Server{Store: st, Cfg: cfg, regGuard: newRegisterGuard(st)}, db
}

// leadPubIP 给每个用例一个可预期的客户端 IP（用例之间不互相扣配额格子）。
func leadPubIP(n int) string { return fmt.Sprintf("203.0.113.%d", n) }

// postLeadPubJSON 以 JSON 形态打免验证通道（老客户端形状）。
func postLeadPubJSON(t *testing.T, s *Server, ip string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/lead/public", strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip + ":51234"
	w := httptest.NewRecorder()
	s.handleLeadCreatePublic(w, r)
	return w
}

// postLeadPubForm 以原生 <form> 的表单编码形态打免验证通道。
func postLeadPubForm(t *testing.T, s *Server, ip string, v url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/lead/public", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = ip + ":51234"
	w := httptest.NewRecorder()
	s.handleLeadCreatePublic(w, r)
	return w
}

func postLeadJSON(t *testing.T, s *Server, ip string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/lead", strings.NewReader(string(b)))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip + ":51234"
	w := httptest.NewRecorder()
	s.handleLeadCreate(w, r)
	return w
}

// leadForm 留资表单的三件必填（蜜罐恒空＝真人形态）。
func leadForm(company, email string) url.Values {
	return url.Values{"company": {company}, "email": {email}, "site": {""}, "source": {"static_fallback"}}
}

// redirectOf 取 3xx 的 Location（表单档的出路判据都读它）。
func redirectOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return w.Header().Get("Location")
}

// countLeads 现读 feedbacks 里 target_type='lead' 的行数（"落库了没"的唯一凭据，不看响应信封）。
func countLeads(t *testing.T, s *Server) int {
	t.Helper()
	list, err := s.Store.ListFeedbacks("")
	if err != nil {
		t.Fatalf("读反馈列表失败: %v", err)
	}
	n := 0
	for _, f := range list {
		if f.TargetType == "lead" {
			n++
		}
	}
	return n
}

// rewindGuardInt 把某 key 的"最近动作时间"拨回 n 秒前（跳开最小间隔用；见文件头第 ② 条）。
func rewindGuardInt(t *testing.T, db *sql.DB, key string, sec int) {
	t.Helper()
	res, err := db.Exec("UPDATE rate_limits SET window_start=? WHERE scope='guard_int' AND key=?",
		time.Now().Unix()-int64(sec), key)
	if err != nil {
		t.Fatalf("拨 guard_int 时间戳失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		t.Fatalf("拨 guard_int 没命中任何行（key=%q）⇒ 上一条提交根本没记格子，用例的下一步 429 会是假的", key)
	}
}

// enableCaptcha 打开人机验证提供方（只设 provider，不配 secret：token 为空时 verifyCaptcha 先拒，不外呼）。
func enableCaptcha(t *testing.T, s *Server) {
	t.Helper()
	if err := s.Store.SetConfig("captcha_provider", "turnstile"); err != nil {
		t.Fatalf("设 captcha_provider 失败: %v", err)
	}
}

// ① 表单编码 + 必填齐 ⇒ 303 到 /?lead=ok，并且**真落库**。
func TestLeadPublicFormSuccess303AndPersists(t *testing.T) {
	s, _ := leadPubHarness(t)
	w := postLeadPubForm(t, s, leadPubIP(1), leadForm("示例贸易", "Biz@Example.com"))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("表单档成功必须 303（原生 <form> 提交后必须落回 HTML 页），got %d body=%s", w.Code, w.Body.String())
	}
	if got := redirectOf(t, w); got != "/?lead=ok" {
		t.Fatalf("成功出路不对：%q", got)
	}
	if n := countLeads(t, s); n != 1 {
		t.Fatalf("应落 1 条线索，got %d", n)
	}
	list, _ := s.Store.ListFeedbacks("")
	if len(list) == 0 || !strings.Contains(list[0].Content, "示例贸易") || !strings.Contains(list[0].Content, "biz@example.com") {
		t.Fatalf("摘要行缺公司或邮箱（或邮箱没归一小写）：%+v", list)
	}
}

// ② 缺 company ⇒ 303 到 /?lead=err&t=<稳定码>，且不落库。
//
//	★ 只带码不带原文：把文案塞进 query 等于开一个"服务端文案可被 URL 反射"的面。
func TestLeadPublicMissingCompanyRedirectsErr(t *testing.T) {
	s, _ := leadPubHarness(t)
	w := postLeadPubForm(t, s, leadPubIP(2), leadForm("", "a@b.co"))
	loc := redirectOf(t, w)
	if w.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/?lead=err&t=") {
		t.Fatalf("缺必填字段必须 303 到 /?lead=err&t=…，got %d loc=%q", w.Code, loc)
	}
	if strings.Contains(loc, "请填写") {
		t.Fatalf("出路里不许携带后端原文（只许稳定码）：%q", loc)
	}
	if n := countLeads(t, s); n != 0 {
		t.Fatalf("校验失败不许落库，got %d", n)
	}
}

// ③ 邮箱非法 ⇒ 同 ② 的出路形态。
func TestLeadPublicBadEmail(t *testing.T) {
	s, _ := leadPubHarness(t)
	for _, bad := range []string{"no-at", "@x.co", "a@b", "a b@c.co"} {
		w := postLeadPubForm(t, s, leadPubIP(3), leadForm("公司", bad))
		if w.Code != http.StatusSeeOther || !strings.HasPrefix(redirectOf(t, w), "/?lead=err&t=") {
			t.Fatalf("坏邮箱 %q 必须走错误出路，got %d loc=%q", bad, w.Code, redirectOf(t, w))
		}
	}
	if n := countLeads(t, s); n != 0 {
		t.Fatalf("四条坏邮箱都不许落库，got %d", n)
	}
}

// ④ JSON 路径回归：形状一字不改（**不许为了表单把 JSON 接口改坏**——老客户端与 SDK 都打这一份）。
func TestLeadPublicJSONPathUnchanged(t *testing.T) {
	s, _ := leadPubHarness(t)
	w := postLeadPubJSON(t, s, leadPubIP(4), map[string]any{"company": "老客户端", "email": "ok@x.co"})
	if w.Code != http.StatusOK {
		t.Fatalf("JSON 档必须仍 200，got %d body=%s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Fatalf("JSON 档不许被改成重定向/HTML：%s", ct)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("JSON 档响应体必须可解析: %v body=%s", err, w.Body.String())
	}
	if out["success"] != true || out["message"] != "已收到，我们会尽快联系您" {
		t.Fatalf("成功信封形状变了（老客户端按这两个字段判）：%v", out)
	}
	if w.Header().Get("Location") != "" {
		t.Fatal("JSON 档不该带 Location")
	}
	if n := countLeads(t, s); n != 1 {
		t.Fatalf("JSON 档应落 1 条，got %d", n)
	}
}

// ⑤ 蜜罐命中 ⇒ 成功壳但不落库（不给 bot 反馈信号，与 /api/lead 同语义）。
func TestLeadPublicHoneypotFakeSuccess(t *testing.T) {
	s, _ := leadPubHarness(t)
	v := leadForm("botco", "bot@spam.io")
	v.Set("site", "http://seo.example")
	w := postLeadPubForm(t, s, leadPubIP(5), v)
	if w.Code != http.StatusSeeOther || redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("蜜罐必须回「看起来成功」的壳，got %d loc=%q", w.Code, redirectOf(t, w))
	}
	if n := countLeads(t, s); n != 0 {
		t.Fatalf("蜜罐命中不得落库，got %d", n)
	}
}

// ⑥ 乱 body（表单档声明了 Content-Type 却给一段非表单字节 / JSON 档给垃圾）⇒
//
//	必须说"请求格式错误"，**不许**说"请填写…"（解码失败与必填缺失是两个病因，§E-2 同一条纪律）。
func TestLeadPublicGarbageBodyIsFormatError(t *testing.T) {
	s, _ := leadPubHarness(t)
	r := httptest.NewRequest(http.MethodPost, "/api/lead/public", strings.NewReader("{not json"))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = leadPubIP(6) + ":51234"
	w := httptest.NewRecorder()
	s.handleLeadCreatePublic(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("乱 body 应 400，got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "请求格式错误") {
		t.Fatalf("解码失败必须说「请求格式错误」：%s", body)
	}
	if strings.Contains(body, "请填写") {
		t.Fatalf("解码失败不许被并成「请填写…」那一档：%s", body)
	}
}

// ⑦ S1 升级：同 IP＋同设备组内出现 ≥2 个登录身份 ⇒ 当场要求人机验证；
//
//	并配一条**单身份不升级**的反向对照（否则"恒要验证码"的坏实现也能让正向绿）。
func TestLeadPublicEscalateMultiIdentity(t *testing.T) {
	s, db := leadPubHarness(t)
	enableCaptcha(t, s)
	ip, dev := leadPubIP(7), "devabcdef12"
	// 同 (IP,设备号) 组内两个不同 uid 的留资身份（走业务写点，不裸写库）
	for _, uid := range []string{"1001", "1002"} {
		if _, err := s.Store.RateRecord(leadIdentUIDScope, ip+":"+dev+":"+uid, leadIdentWindowSec); err != nil {
			t.Fatalf("预置身份账本失败: %v", err)
		}
	}
	_ = db
	// 表单档（零 JS 那张表单的形态）：命中升级信号 ⇒ 走错误出路，且码读得出是校验类失败
	wf := postLeadPubForm(t, s, ip, withDevice(leadForm("多人出口", "a@x.co"), dev))
	if wf.Code != http.StatusSeeOther || !strings.HasPrefix(redirectOf(t, wf), "/?lead=err&t=") {
		t.Fatalf("组内两个身份 ⇒ 表单档必须被拒，got %d loc=%q", wf.Code, redirectOf(t, wf))
	}
	// JSON 档（JS 表单的形态，它才可能补一个 token 重来）：400 ＋ 文案点名"人机验证"
	wj := postLeadPubJSON(t, s, ip, map[string]any{"company": "多人出口", "email": "a@x.co", "device_id": dev})
	if wj.Code != http.StatusBadRequest || !strings.Contains(wj.Body.String(), "人机验证") {
		t.Fatalf("JSON 档命中升级信号必须回 400 并点名「人机验证」，got %d body=%s", wj.Code, wj.Body.String())
	}
	if n := countLeads(t, s); n != 0 {
		t.Fatalf("被升级信号拒的两单都不许落库，got %d", n)
	}
	// 反向对照：同 IP 同设备但组内只有一个身份 ⇒ 不升级（免验证通道本来就是给这一档开的）
	ip2, dev2 := leadPubIP(8), "devzzzzzz1"
	if _, err := s.Store.RateRecord(leadIdentUIDScope, ip2+":"+dev2+":2001", leadIdentWindowSec); err != nil {
		t.Fatalf("预置单身份失败: %v", err)
	}
	w2 := postLeadPubForm(t, s, ip2, withDevice(leadForm("一个人", "b@x.co"), dev2))
	if w2.Code != http.StatusSeeOther || redirectOf(t, w2) != "/?lead=ok" {
		t.Fatalf("单身份不该触发升级，got %d loc=%q body=%s", w2.Code, redirectOf(t, w2), w2.Body.String())
	}
}

// ⑧ S2 升级：同 IP 近 5 分钟登录失败 ≥2 次 ⇒ 要求人机验证；
//
//	★ 并必须配一条"窗口已过（301s）⇒ 不升级"的对照——这一条钉住的正是"窗口是 5 分钟、不是 24 小时"。
func TestLeadPublicEscalateCredFailBurst(t *testing.T) {
	s, db := leadPubHarness(t)
	enableCaptcha(t, s)

	ip := leadPubIP(9)
	if _, err := s.Store.RateRecord(loginIPScope, ip, loginWindowSec); err != nil {
		t.Fatalf("预置失败 1 次: %v", err)
	}
	if _, err := s.Store.RateRecord(loginIPScope, ip, loginWindowSec); err != nil {
		t.Fatalf("预置失败 2 次: %v", err)
	}
	w := postLeadPubJSON(t, s, ip, map[string]any{"company": "爆破出口", "email": "a@x.co"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "人机验证") {
		t.Fatalf("近 5 分钟两次登录失败 ⇒ 必须要求人机验证，got %d body=%s", w.Code, w.Body.String())
	}

	// 窗口对照：把 window_start 拨到 301 秒前 ⇒ 同一份计数必须**不再**触发
	if _, err := db.Exec("UPDATE rate_limits SET window_start=? WHERE scope=? AND key=?",
		time.Now().Unix()-int64(loginWindowSec)-1, loginIPScope, ip); err != nil {
		t.Fatalf("拨 login_fail 窗口起点失败: %v", err)
	}
	w2 := postLeadPubJSON(t, s, ip, map[string]any{"company": "窗口已过", "email": "b@x.co"})
	if w2.Code != http.StatusOK {
		t.Fatalf("窗口过期后仍要验证码 ⇒ 判据把 300s 读成了 24h，got %d loc=%q body=%s",
			w2.Code, redirectOf(t, w2), w2.Body.String())
	}
}

// ⑨ 打标：免验证通道落库的行点名「免验证通道」；/api/lead（原端点）落库的行**不含**这几个字。
//
//	两类线索可信度不同，混在一起看一次垃圾洪峰之后这个面就再没人信。
func TestLeadPublicMarksChannel(t *testing.T) {
	s, _ := leadPubHarness(t)
	if w := postLeadPubForm(t, s, leadPubIP(10), leadForm("降级通道", "pub@x.co")); w.Code != http.StatusSeeOther {
		t.Fatalf("免验证提交应成功，got %d body=%s", w.Code, w.Body.String())
	}
	if w := postLeadJSON(t, s, leadPubIP(11), map[string]any{"company": "正常通道", "email": "norm@x.co"}); w.Code != http.StatusOK {
		t.Fatalf("/api/lead 提交应成功，got %d body=%s", w.Code, w.Body.String())
	}
	list, err := s.Store.ListFeedbacks("")
	if err != nil || len(list) != 2 {
		t.Fatalf("应共 2 条线索，got %d err=%v", len(list), err)
	}
	var pub, norm string
	for _, f := range list {
		switch {
		case strings.Contains(f.Content, "降级通道"):
			pub = f.Content
		case strings.Contains(f.Content, "正常通道"):
			norm = f.Content
		}
	}
	if pub == "" || norm == "" {
		t.Fatalf("两条都没找到（摘要行形状变了）：%+v", list)
	}
	if !strings.HasPrefix(pub, "【销售线索·免验证通道】") {
		t.Fatalf("免验证通道必须以前缀打标：%q", pub)
	}
	if strings.Contains(norm, "免验证通道") {
		t.Fatalf("正常通道不该带免验证字样：%q", norm)
	}
	if !strings.Contains(pub, "来源：static_fallback") {
		t.Fatalf("免验证通道的来源标签必须可归因：%q", pub)
	}
}

// ⑩ 配额独立且更紧：本端点 3/日 ⇒ 第 4 条 429；而同一 IP 打 /api/lead **照旧成功**
//
//	（两本账不许互相扣格子——前缀 "lead-public:" 与 "lead:" 就是这条的落点）。
func TestLeadPublicQuotaSeparate(t *testing.T) {
	s, db := leadPubHarness(t)
	ip := leadPubIP(12)
	for i := 1; i <= 3; i++ {
		w := postLeadPubForm(t, s, ip, leadForm(fmt.Sprintf("配额测试%d", i), "q@x.co"))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("第 %d 条应放行，got %d body=%s", i, w.Code, w.Body.String())
		}
		if i < 3 {
			rewindGuardInt(t, db, "lead-public:"+ip, 11) // 跳开 10s 最小间隔（不是跳开日额）
		}
	}
	if n := countLeads(t, s); n != 3 {
		t.Fatalf("应落 3 条，got %d", n)
	}
	w4 := postLeadPubForm(t, s, ip, leadForm("第四条", "q@x.co"))
	if w4.Code != http.StatusSeeOther || !strings.HasPrefix(redirectOf(t, w4), "/?lead=err&t=") {
		t.Fatalf("日额打满后第 4 条必须走错误出路，got %d loc=%q", w4.Code, redirectOf(t, w4))
	}
	if cv := queryOf(t, w4, "t"); cv != "RATE_LIMITED" {
		t.Fatalf("日额打满那一枪的出路码必须是稳定码 RATE_LIMITED（运营与前端都按它分支），实得 t=%q loc=%q", cv, redirectOf(t, w4))
	}
	if n := countLeads(t, s); n != 3 {
		t.Fatalf("被日额拒的一单不许落库，got %d", n)
	}
	// 同 IP 打正常通道：不许被免验证那本账牵连
	wl := postLeadJSON(t, s, ip, map[string]any{"company": "正常通道", "email": "n@x.co"})
	if wl.Code != http.StatusOK {
		t.Fatalf("/api/lead 与 /api/lead/public 各记一本账，同 IP 正常通道应仍可提交，got %d body=%s", wl.Code, wl.Body.String())
	}
}

// ⑪ 失败不许吃掉当天的格子：校验失败/被拒的那一枪**不计数**（AGENTS §一·10「失败链路一律退格」的同一取向）。
func TestLeadPublicFailureDoesNotConsumeQuota(t *testing.T) {
	s, _ := leadPubHarness(t)
	ip := leadPubIP(13)
	for i := 0; i < 5; i++ { // 连打 5 次缺必填的（都进不了 record）
		w := postLeadPubForm(t, s, ip, leadForm("", "a@x.co"))
		if w.Code != http.StatusSeeOther || !strings.HasPrefix(redirectOf(t, w), "/?lead=err") {
			t.Fatalf("缺必填必须走错误出路，got %d loc=%q", w.Code, redirectOf(t, w))
		}
	}
	w := postLeadPubForm(t, s, ip, leadForm("仍然可提交", "c@x.co"))
	if w.Code != http.StatusSeeOther || redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("前 5 次失败不该吃掉格子，成功那一枪必须放行，got %d loc=%q body=%s",
			w.Code, redirectOf(t, w), w.Body.String())
	}
	if n := countLeads(t, s); n != 1 {
		t.Fatalf("只应落 1 条，got %d", n)
	}
}

// ⑫ 取值口径腿：`?company=注入值` **不许**替用户填表。
// leadDecode 用 r.PostFormValue 而不是 r.FormValue——后者会把 query 一起算进来，
// 那开的不是"留资能用 query 提交"，而是"任何表单接口的 POST 体都能被 URL 改写"这个形状。
func TestLeadDecodeQueryParamsCannotFillForm(t *testing.T) {
	s, _ := leadPubHarness(t)
	r := httptest.NewRequest(http.MethodPost, "/api/lead/public?company=%E6%B3%A8%E5%85%A5&email=inj@x.co",
		strings.NewReader(url.Values{"site": {""}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.RemoteAddr = leadPubIP(14) + ":51234"
	w := httptest.NewRecorder()
	s.handleLeadCreatePublic(w, r)
	if redirectOf(t, w) == "/?lead=ok" {
		t.Fatalf("query 参数把表单填全了 ⇒ PostFormValue 被换成了 FormValue（形状性后门）")
	}
	if n := countLeads(t, s); n != 0 {
		t.Fatalf("注入形态不许落库，got %d", n)
	}
}

// ⑬ multipart 也认（原生表单 enctype 的另一档），且 512KB 上限是真生效的。
func TestLeadDecodeMultipartAccepted(t *testing.T) {
	s, _ := leadPubHarness(t)
	ip := leadPubIP(15)
	boundary := "b0undaryForLead"
	body := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"company\"\r\n\r\nmultipart公司\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"email\"\r\n\r\nmp@x.co\r\n" +
		"--" + boundary + "--\r\n"
	r := httptest.NewRequest(http.MethodPost, "/api/lead/public", strings.NewReader(body))
	r.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	r.RemoteAddr = ip + ":51234"
	w := httptest.NewRecorder()
	s.handleLeadCreatePublic(w, r)
	if w.Code != http.StatusSeeOther || redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("multipart 形态必须被接受，got %d loc=%q body=%s", w.Code, redirectOf(t, w), w.Body.String())
	}
}

// ⑭ 一次性邮箱黑名单：内置档命中、配置覆盖生效、正常域名放行（名单唯一事实源在 system_config，前端不抄第二份）。
func TestLeadPublicDisposableDomainBlacklist(t *testing.T) {
	s, _ := leadPubHarness(t)
	if w := postLeadPubForm(t, s, leadPubIP(16), leadForm("临时邮箱", "me@mailinator.com")); !strings.HasPrefix(redirectOf(t, w), "/?lead=err") {
		t.Fatalf("内置档必须挡住 mailinator.com，got %d loc=%q", w.Code, redirectOf(t, w))
	}
	if err := s.Store.SetConfig("lead_disposable_domains", "spam.example"); err != nil {
		t.Fatalf("设名单失败: %v", err)
	}
	if w := postLeadPubForm(t, s, leadPubIP(17), leadForm("换名单后", "me@mailinator.com")); redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("配置现值必须盖过内置档（运营能改才是现值），got loc=%q", redirectOf(t, w))
	}
	if w := postLeadPubForm(t, s, leadPubIP(18), leadForm("换名单后", "me@spam.example")); !strings.HasPrefix(redirectOf(t, w), "/?lead=err") {
		t.Fatalf("配置里的域名必须挡住，got loc=%q", redirectOf(t, w))
	}
	// 子域也挡（mail.<黑名单域>），但**同后缀的正常域不能被误伤**（notspam.example ≠ spam.example）
	if w := postLeadPubForm(t, s, leadPubIP(19), leadForm("子域", "me@mail.spam.example")); !strings.HasPrefix(redirectOf(t, w), "/?lead=err") {
		t.Fatalf("子域必须命中，got loc=%q", redirectOf(t, w))
	}
	if w := postLeadPubForm(t, s, leadPubIP(20), leadForm("近似域", "me@notspam.example")); redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("近似后缀的正常域不许误伤，got loc=%q body=%s", redirectOf(t, w), w.Body.String())
	}
}

// ⑮ 设备号格式不合＝当没上报，**不返 400**（AGENTS §一·10「新增防护不拿可用性交押金」）。
func TestLeadPublicBadDeviceIdIsIgnoredNotRejected(t *testing.T) {
	s, _ := leadPubHarness(t)
	v := withDevice(leadForm("怪设备号", "d@x.co"), "短") // <8 位 ⇒ 不合 trialDeviceIDRe
	w := postLeadPubForm(t, s, leadPubIP(21), v)
	if redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("设备号格式不合不许拒一条真线索，got %d loc=%q body=%s", w.Code, redirectOf(t, w), w.Body.String())
	}
}

// ⑭b 升级信号本身不许把"没配人机验证"的部署挡在门外（软档：判据只在 captcha 真开着时才咬人）。
func TestLeadPublicEscalationIsSilentWhenCaptchaOff(t *testing.T) {
	s, _ := leadPubHarness(t) // 不设 captcha_provider ⇒ 关闭档
	ip, dev := leadPubIP(22), "devabcdef12"
	for _, uid := range []string{"3001", "3002"} {
		if _, err := s.Store.RateRecord(leadIdentUIDScope, ip+":"+dev+":"+uid, leadIdentWindowSec); err != nil {
			t.Fatalf("预置身份失败: %v", err)
		}
	}
	w := postLeadPubForm(t, s, ip, withDevice(leadForm("没开验证的部署", "e@x.co"), dev))
	if redirectOf(t, w) != "/?lead=ok" {
		t.Fatalf("未启用人机验证时，升级信号不许变成「提交不出去」，got %d loc=%q body=%s",
			w.Code, redirectOf(t, w), w.Body.String())
	}
}

// ============ 三条形状腿（登记/射程/判据自伤） ============

// ⑯ 登记腿：本端点必须在公开白名单里**并带非空理由**。
// ⚠️ 为什么还要单独钉这一条：鉴权结构闸门（TestRouteAuthGate）按 handler 调用闭包判"有没有鉴权"，
// 而本 handler 闭包里有 s.authUser（只为取 uid 记 S1 账本）⇒ 它会被判成"已鉴权"，
// **删掉白名单那一行并不会让它红**。所以"公开可达"这件事必须由本文件自己钉死（施工方案 C-1.2
// 那句"不登记当场红"是按判据形状写的假设，实测不成立，已按事实改口径）。
func TestLeadPublicAllowlistRegistered(t *testing.T) {
	reason, ok := publicRouteAllowlist["/api/lead/public"]
	if !ok {
		t.Fatal("/api/lead/public 不在 publicRouteAllowlist ⇒ 匿名可达这件事没有人工评审记录")
	}
	if len([]rune(reason)) < 20 {
		t.Fatalf("白名单理由必须写清「为什么可以匿名」（现读长度 %d）：%q", len([]rune(reason)), reason)
	}
	for _, must := range []string{"蜜罐", "配额", "黑名单"} {
		if !strings.Contains(reason, must) {
			t.Errorf("公开理由必须点名四道自保之一的「%s」：%q", must, reason)
		}
	}
}

// ⑰ 匿名可达的机械判据：本 handler 的调用闭包里**不许**出现会中断请求的强闸
// （requireAdminUser/requireTenantAdmin/authenticateAPIKey* 一类；判据集合复用闸门自己的 authGateHardGuards，
// 不在这里抄第二份名单）。反证：在 handler 里加一句 requireTenantAdmin ⇒ 本锁必须红。
func TestLeadPublicHandlerHasNoHardAuthGuard(t *testing.T) {
	_, funcs, err := scanAuthGatePackage(".")
	if err != nil {
		t.Fatalf("解析本包失败: %v", err)
	}
	if authGateReachableNamed(funcs, "handleLeadCreatePublic", authGateHardGuards) {
		t.Fatal("免验证通道里出现了会中断请求的强闸 ⇒ 零 JS 访客又被挡在门外（DEF-P0-03 复发）")
	}
	// 射程自证：判据集合本身必须能认出"有强闸"的那一类，否则上面那句是恒真的空锁
	if !authGateReachableNamed(funcs, "handleAPIKeys", authGateHardGuards) {
		t.Fatal("射程自证失败：handleAPIKeys（requireTenantAdmin 那一面）都没被强闸判据认出 ⇒ 判据退化成空集")
	}
}

// ⑱ 出路只有一个事实源：表单档成功出路必须是 "/?lead=ok"（前端按这个 query 显示一次性提示）。
// 写进代码里的第二份（比如 handler 里就地拼 "/lead-ok"）会让前端那条提示永远不出现。
func TestLeadPublicOutboundPathIsSharedWithLanding(t *testing.T) {
	s, _ := leadPubHarness(t)
	w := postLeadPubForm(t, s, leadPubIP(23), leadForm("出路形状", "o@x.co"))
	loc := redirectOf(t, w)
	u, err := url.Parse(loc)
	if err != nil || u.Path != "/" {
		t.Fatalf("成功出路必须回首页（HTML 面），loc=%q err=%v", loc, err)
	}
	if u.Query().Get("lead") != "ok" {
		t.Fatalf("成功标记必须是 ?lead=ok（前端按这个名字取值），loc=%q", loc)
	}
	// 源码级接线：出路只有 leadOK/leadFail 两个出口，且各自的那一句字面量在包里**只出现一次**
	//（前端按 ?lead=ok|err 显示一次性提示；第二份拼法一出现，就是"提示永远不出现"的开端）
	src := readApiSourceForLeadGate(t, "lead.go")
	for _, lit := range []string{`"/?lead=ok"`, `"lead": {"err"`} {
		if n := strings.Count(src, lit); n != 1 {
			t.Fatalf("出路字面量 %s 在 lead.go 里出现 %d 次（期望恰 1；多份＝迟早分家）", lit, n)
		}
	}
	// 反向对照：两个 handler 都不许自己拼出路（必须经 leadOK/leadFail）
	for _, f := range []string{"lead.go", "lead_public.go"} {
		body := readApiSourceForLeadGate(t, f)
		for _, want := range []string{"s.leadOK(w, r, formMode)", "s.leadFail(w, r, formMode"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s 里没走统一出路（%s）⇒ 出路又长回各写一份的形态", f, want)
			}
		}
	}
}

// withDevice 给表单值加设备号（S1 判据的输入）。
func withDevice(v url.Values, dev string) url.Values {
	v.Set("device_id", dev)
	return v
}

// queryOf 取 303 出路里的 query 参数值。
func queryOf(t *testing.T, w *httptest.ResponseRecorder, k string) string {
	t.Helper()
	u, err := url.Parse(redirectOf(t, w))
	if err != nil {
		t.Fatalf("出路 Location 解析不了 %q: %v", redirectOf(t, w), err)
	}
	return u.Query().Get(k)
}

// readApiSourceForLeadGate 读本包源文件（接线锁的原料）。
func readApiSourceForLeadGate(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(b)
}
