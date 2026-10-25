// ============ invite_link_qr_test.go · 职责说明 ============
// 《多身份账号方案_20261023.md》§6.2「复制 / 发邮件 / 出二维码」第三件的回归断言
// （★ 多身份方案 B4，2026-10-24）：GET /api/admin/invite-links/qrcode?id=<id>。
//
// 五条锁各自拦的退化形态：
//
//	QR-1 可用链接出**真 PNG**（魔数＋体积下限，不是 200 壳）＋ download=1 带文件名。
//	       「返回 200」在这类端点上毫无信息量：SPA 兜底与空响应体都能是 200（AGENTS §一·6 托管物口径）。
//	QR-2 三档死链（已使用／已撤销／已过期）一律 400 且零 PNG 字节 —— 二维码是"把门禁发出去"的动作，
//	       发一张扫不开的图给员工，比当场告诉他"去重新生成"更坏。
//	QR-3 越权与不存在折叠成**同一句 404**（别家租户的管理员打这枚 id，读到的不能是 403「存在但没权限」，
//	       那等于一条免费的跨租户链接 ID 探针；AGENTS §一·8② 同族）。
//	QR-4 列表逐行的 url 与创建回执的 url **逐字等值**（同一把尺子 inviteLinkURL＋inviteLinkHostFor）：
//	       前端一旦自己拼 https://<window.location.host>/?invite=，超管在平台后台给品牌域租户
//	       列出来的就是错域名链接，而它"看着能用"。
//	QR-5 鉴权闸：普通成员（非部管）打这一面 ⇒ 403；未登录 ⇒ 401；POST ⇒ 405；
//	       并带一条"同一条链接、租管身份必须真出图"的正向对照（否则前三条红的可能只是端点整个坏了）。
//
// 方言：自钉 SQLite 内存库并显式钉死 config.C（AGENTS.md §一·4）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestInviteLinkQr
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// qrPngMagic PNG 文件头（\x89 P N G）——二进制端点的"真出图"证据只认它。
var qrPngMagic = []byte{0x89, 'P', 'N', 'G'}

// newInviteQrProbe 装配二维码端点的探针库：内存 SQLite ＋ 一家企业租户 ＋ 租管 JWT。
// 链接本身由各场景自己造（createInviteQrLink），因为"已使用／已撤销／已过期"三档
// 最好是**造出来**的而不是**改出来**的——改要依赖只有测试才调的写方法，那是把产品API撑大。
// 返回: Server、租户 ID、租管 JWT。
func newInviteQrProbe(t *testing.T) (*Server, int64, string) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", fmt.Sprintf("file:inviteqr_%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := tenant.NewStore(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	tn, err := ts.Create("iqr", "二维码探针租", "", "{}")
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateUser(tn.ID, "iqr-admin", auth.PasswordHash("pw123456"), "探针管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(op, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.UploadDir = t.TempDir()
	return &Server{Store: st, Ten: ts, Cfg: cfg}, tn.ID, tok
}

// createInviteQrLink 落一枚链接凭据（expiresAt 由场景给定；空=不过期）。
// 码串按真实形状造（inv_ ＋ 32 hex），但熵是假的——本文件不测码的可枚举性（那在 store 侧另有锁）。
func createInviteQrLink(t *testing.T, s *Server, tid int64, n int, expiresAt string) *store.InviteCode {
	t.Helper()
	code := fmt.Sprintf("inv_%032x", n)
	link, err := s.Store.CreateInviteLink(code, tid, 0, store.RoleUser, fmt.Sprintf("iqr_user_%d", n), "", expiresAt)
	if err != nil {
		t.Fatalf("创建链接失败：%v", err)
	}
	return link
}

// futureExpiry 默认有效期形态（7 天后），与 inviteLinkDefaultDays 同档但不去读那个常量——
// 测试要的是"一条还没到期的链接"，不是"当前默认档是多少"（后者变了本文件不该跟着红）。
func futureExpiry() string { return time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339) }

// callInviteQr 直调二维码端点（query 参数由 q 传入，Authorization 由 tok 传入，空串＝匿名）。
func callInviteQr(t *testing.T, s *Server, tok string, q map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	u := "/api/admin/invite-links/qrcode"
	if len(q) > 0 {
		parts := make([]string, 0, len(q))
		for k, v := range q {
			parts = append(parts, k+"="+v)
		}
		u += "?" + strings.Join(parts, "&")
	}
	req := httptest.NewRequest(http.MethodGet, u, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	s.handleInviteLinkQrcode(rec, req)
	return rec
}

// callInviteLinkCreate 直调创建端点并解出回执（QR-4 的基准读数）。
func callInviteLinkCreate(t *testing.T, s *Server, tok, username string) (id int64, code, url string) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"username": username})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/invite-links", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleInviteLinkCreate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("创建链接失败 %d body=%s", rec.Code, rec.Body.String())
	}
	var r struct {
		Success bool   `json:"success"`
		ID      int64  `json:"id"`
		Code    string `json:"code"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || !r.Success {
		t.Fatalf("创建回执解析失败：%v %s", err, rec.Body.String())
	}
	return r.ID, r.Code, r.URL
}

// TestInviteLinkQr_PngForActiveLink 锁 QR-1：可用链接出真 PNG，download=1 带文件名。
func TestInviteLinkQr_PngForActiveLink(t *testing.T) {
	s, tid, tok := newInviteQrProbe(t)
	link := createInviteQrLink(t, s, tid, 1, futureExpiry())
	rec := callInviteQr(t, s, tok, map[string]string{"id": fmt.Sprint(link.ID)})
	if rec.Code != http.StatusOK {
		t.Fatalf("可用链接应出图，实得 %d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type 应为 image/png，实得 %q", ct)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, qrPngMagic) || len(body) < 200 {
		head := body
		if len(head) > 4 {
			head = head[:4]
		}
		t.Fatalf("响应体不是真 PNG（前 %v，总长 %d）——200 壳／SPA 兜底页都长这样", head, len(body))
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Fatalf("不带 download 时不该给附件头，实得 %q", cd)
	}
	// 第二枪：download=1 必须带文件名（管理台"下载二维码"按钮的落盘名）
	rec2 := callInviteQr(t, s, tok, map[string]string{"id": fmt.Sprint(link.ID), "download": "1"})
	if cd2 := rec2.Header().Get("Content-Disposition"); !strings.Contains(cd2, "invite-"+link.Code) {
		t.Fatalf("download=1 应带 invite-<code>.png，实得 %q", cd2)
	}
}

// TestInviteLinkQr_DeadLinksRefused 锁 QR-2：已使用／已撤销／已过期三档都不出图。
func TestInviteLinkQr_DeadLinksRefused(t *testing.T) {
	scenes := []struct {
		name   string
		expiry string
		// dead 把刚造出来的链接推入死档；返回要额外做的事（无则返回 nil 闭包）
		dead func(t *testing.T, s *Server, link *store.InviteCode)
	}{
		{"已使用", futureExpiry(), func(t *testing.T, s *Server, link *store.InviteCode) {
			// 签名是 (ok, err)：ok 为假＝这行早已被用掉／撤掉，夹具就没造出「刚变死档」那一态
			ok, err := s.Store.MarkInviteCodeUsed(link.ID, "someone@acme.cn")
			if err != nil || !ok {
				t.Fatalf("置已用失败 ok=%v err=%v", ok, err)
			}
		}},
		{"已撤销", futureExpiry(), func(t *testing.T, s *Server, link *store.InviteCode) {
			ok, used, err := s.Store.RevokeInviteCode(link.ID)
			if err != nil || !ok {
				t.Fatalf("撤销失败 ok=%v used=%v err=%v", ok, used, err)
			}
		}},
		// 过期档＝造一枚"昨天就到期"的链接（expires_at 是库里的列，造比改诚实）
		{"已过期", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), func(t *testing.T, s *Server, link *store.InviteCode) {
			if !link.IsExpired() {
				t.Fatalf("夹具没造出过期态（IsExpired 判假），本条锁失明")
			}
		}},
	}
	for i, scene := range scenes {
		t.Run(scene.name, func(t *testing.T) {
			s, tid, tok := newInviteQrProbe(t)
			link := createInviteQrLink(t, s, tid, 10+i, scene.expiry)
			scene.dead(t, s, link)
			// 撤销/使用后再读一次库（判据不吃内存里的旧行）
			fresh, err := s.Store.GetInviteCode(link.ID)
			if err != nil || fresh == nil {
				t.Fatalf("回读失败 %v", err)
			}
			rec := callInviteQr(t, s, tok, map[string]string{"id": fmt.Sprint(link.ID)})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s 的链接不该出图，应回 400，实得 %d body=%s", scene.name, rec.Code, rec.Body.String())
			}
			if bytes.HasPrefix(rec.Body.Bytes(), qrPngMagic) {
				t.Fatalf("%s 的链接仍写出了 PNG 字节", scene.name)
			}
			if !strings.Contains(rec.Body.String(), "已不可用") {
				t.Fatalf("400 文案要点名「已不可用」并指出去重新生成，实得 %s", rec.Body.String())
			}
		})
	}
}

// TestInviteLinkQr_ForeignAndMissingSame404 锁 QR-3：越权与不存在折叠成同一句 404。
func TestInviteLinkQr_ForeignAndMissingSame404(t *testing.T) {
	s, tid, tok := newInviteQrProbe(t)
	link := createInviteQrLink(t, s, tid, 2, futureExpiry())
	// 另一家企业的管理员（同库不同租户）来打这一枚 id
	other, err := s.Ten.Create("iqr2", "另一家租", "", "{}")
	if err != nil {
		t.Fatal(err)
	}
	op2, err := s.Store.CreateUser(other.ID, "iqr2-admin", auth.PasswordHash("pw123456"), "别家管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := auth.Sign(op2, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	foreign := callInviteQr(t, s, tok2, map[string]string{"id": fmt.Sprint(link.ID)})
	missing := callInviteQr(t, s, tok, map[string]string{"id": "999999"})
	for name, rec := range map[string]*httptest.ResponseRecorder{"越权": foreign, "不存在": missing} {
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s 应回 404，实得 %d body=%s", name, rec.Code, rec.Body.String())
		}
	}
	if foreign.Body.String() != missing.Body.String() {
		t.Fatalf("越权与不存在的响应体必须逐字相同（分开回＝一条跨租户 ID 探针）：\n越权=%s\n不存在=%s",
			foreign.Body.String(), missing.Body.String())
	}
	// 脏参数（非数字）同样判 404，不许退化成 500 或"未登录"
	if dirty := callInviteQr(t, s, tok, map[string]string{"id": "abc"}); dirty.Code != http.StatusNotFound {
		t.Fatalf("脏 id 应回 404，实得 %d body=%s", dirty.Code, dirty.Body.String())
	}
	// 正向对照：同一条链接、本租户租管必须真出图（否则上面三条红的是端点整个坏了）
	if got := callInviteQr(t, s, tok, map[string]string{"id": fmt.Sprint(link.ID)}); got.Code != http.StatusOK {
		t.Fatalf("正向对照失败：本租户租管应 200，实得 %d body=%s", got.Code, got.Body.String())
	}
}

// TestInviteLinkQr_ListUrlEqualsCreateUrl 锁 QR-4：列表逐行 url 与创建回执 url 逐字等值。
func TestInviteLinkQr_ListUrlEqualsCreateUrl(t *testing.T) {
	s, tid, tok := newInviteQrProbe(t)
	seed := createInviteQrLink(t, s, tid, 3, futureExpiry())
	createdID, createdCode, createdURL := callInviteLinkCreate(t, s, tok, "iqr_li")

	lreq := httptest.NewRequest(http.MethodGet, "/api/admin/invite-links", nil)
	lreq.Header.Set("Authorization", "Bearer "+tok)
	lrec := httptest.NewRecorder()
	s.handleInviteLinks(lrec, lreq)
	var listed struct {
		Success bool `json:"success"`
		Links   []struct {
			ID   int64  `json:"id"`
			Code string `json:"code"`
			URL  string `json:"url"`
		} `json:"links"`
	}
	if err := json.Unmarshal(lrec.Body.Bytes(), &listed); err != nil || !listed.Success {
		t.Fatalf("列表解析失败：%v %s", err, lrec.Body.String())
	}
	byID := map[int64]string{}
	byCode := map[string]string{}
	for _, row := range listed.Links {
		if strings.TrimSpace(row.URL) == "" {
			t.Fatalf("列表第 %d 行没有 url（前端只能自己拼 host ⇒ 品牌域那一档必错）：%+v", row.ID, row)
		}
		byID[row.ID] = row.URL
		byCode[row.Code] = row.URL
	}
	if _, ok := byID[seed.ID]; !ok {
		t.Fatalf("列表没带出预置链接 %d，实得 %+v", seed.ID, listed.Links)
	}
	if byID[createdID] != createdURL {
		t.Fatalf("列表 url 与创建回执 url 不同源：列表=%q 创建=%q", byID[createdID], createdURL)
	}
	if byCode[createdCode] != createdURL {
		t.Fatalf("同一枚码在列表里按 id/按 code 读到两个 url：%q vs %q", byCode[createdCode], createdURL)
	}
	for _, u := range []string{byID[seed.ID], createdURL} {
		if !strings.Contains(u, "/?invite=") {
			t.Fatalf("url 形态应是 <host>/?invite=<code>（§6.3 点名的参数名），实得 %q", u)
		}
	}
	// 手输码（kind=code）不得从这一面出栈：它是另一套入口形态（§6.1 门一），混进来会让
	// 「企业邀请链接」面板出现一条没有预绑信息、也不能当链接发的行。
	// ⚠️ 必须**造完再打一次**：拿上面那一次的响应体做负向判据，判的是"创建之前"的字节，恒真＝空转锁。
	if _, err := s.Store.CreateInviteCode("plain_code_3", tid, 0); err != nil {
		t.Fatalf("预置手输码失败：%v（没有这一行，下面的负向判据无射程）", err)
	}
	lrec2 := httptest.NewRecorder()
	lreq2 := httptest.NewRequest(http.MethodGet, "/api/admin/invite-links", nil)
	lreq2.Header.Set("Authorization", "Bearer "+tok)
	s.handleInviteLinks(lrec2, lreq2)
	if lrec2.Code != http.StatusOK {
		t.Fatalf("第二次列表应 200，实得 %d body=%s", lrec2.Code, lrec2.Body.String())
	}
	if strings.Contains(lrec2.Body.String(), "plain_code_3") {
		t.Fatalf("库里已有 kind=code 的手输码，列表却把它带出来了（kind 过滤失效＝§6.4 命名隔离破口）")
	}
	// 反向对照：同一把尺子必须还能看见那一枚 link 码——否则上面那条红的是"列表整个空了"，不是过滤
	if !strings.Contains(lrec2.Body.String(), createdCode) {
		t.Fatalf("第二次列表把专属链接也弄丢了（负向判据会把\"读不到任何东西\"读成\"过滤生效\"）：%s", lrec2.Body.String())
	}
}

// TestInviteLinkQr_AuthzGate 锁 QR-5：未登录 401、普通成员 403、POST 405。
func TestInviteLinkQr_AuthzGate(t *testing.T) {
	s, tid, tok := newInviteQrProbe(t)
	link := createInviteQrLink(t, s, tid, 4, futureExpiry())
	mem, err := s.Store.CreateUser(tid, "iqr-member", auth.PasswordHash("pw123456"), "成员", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	memTok, err := auth.Sign(mem, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	rec403 := callInviteQr(t, s, memTok, map[string]string{"id": fmt.Sprint(link.ID)})
	if rec403.Code != http.StatusForbidden {
		t.Fatalf("普通成员应 403，实得 %d body=%s", rec403.Code, rec403.Body.String())
	}
	rec401 := callInviteQr(t, s, "", map[string]string{"id": fmt.Sprint(link.ID)})
	if rec401.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实得 %d body=%s", rec401.Code, rec401.Body.String())
	}
	// 方法闸：POST 同一面 405（二进制读面不许把写方法当读处理）
	req := httptest.NewRequest(http.MethodPost, "/api/admin/invite-links/qrcode?id=1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleInviteLinkQrcode(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST 应 405，实得 %d body=%s", rec.Code, rec.Body.String())
	}
}
