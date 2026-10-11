// ============ notifications_page_test.go · 职责说明 ============
// api 包站内信列表**HTTP 面**分页契约单测（★ 2026-10-11 跨境链路瓶颈批）。
// store 那侧测的是"取几行"，本文件测的是"对外怎么说"，四件事：
//
//	① 默认一页 20 条（旧形态一次吐 100 条，在塌到 KB/s 的链路上永远转不完 → 「铃铛有数字、面板没内容」）；
//	② 响应回显的 `limit` 必须与**真用的那个**档位一致（前端拿它当翻页步长，回显请求值会让脏参把 offset 算歪）；
//	③ `has_more` 的"本页取满即可能还有下一页"近似口径（不做 COUNT(*)：多一次查询换一个更准的按钮态是本批的反向收益）；
//	④ 老客户端兼容：`notifications` 键名与元素形态一字未改，脏参数一律折回档位而不是 4xx/5xx；
//	⑤ 空页出栈形态：列表键必须是 `[]` 不是 `null`（分页让"空页"从罕见变成常规，见那条专项说明）。
//
// ★ 方言自钉（AGENTS §一·4）：config.Default() 副作用写全局 config.C，先存旧值、Cleanup 还原。
// ==================================================================
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newNotifAPIFixture 内存 SQLite（走 store.New 的全量迁移）＋一名用户＋其 JWT，
// 并写入 n 条站内信。返回 Server 与 token，由用例自己直调 handler。
func newNotifAPIFixture(t *testing.T, n int) (*Server, string) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("开内存库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("store.New（含迁移）失败: %v", err)
	}
	if _, err := st.CreateUser(1, "notif_user", "hash-x", "站内信分页测试用户", "user", 0, 0); err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	u, err := st.GetUserByUsername(1, "notif_user")
	if err != nil {
		t.Fatalf("回读用户失败: %v", err)
	}
	for i := 1; i <= n; i++ {
		if err := st.CreateNotification(u.ID, fmt.Sprintf("通知 %03d", i), fmt.Sprintf("正文 %03d", i), "ticket", int64(i)); err != nil {
			t.Fatalf("写入第 %d 条站内信失败: %v", i, err)
		}
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签 JWT 失败: %v", err)
	}
	return &Server{Store: st}, tok
}

// notifPageResp 列表响应体：只声明本文件要判的字段（未声明字段由 json 忽略，
// 所以后端将来加字段不会把这条锁弄红——但 notifications／limit／offset／has_more 四个是对外契约）。
type notifPageResp struct {
	Success       bool `json:"success"`
	Notifications []struct {
		ID        int64  `json:"id"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		RefType   string `json:"ref_type"`
		CreatedAt string `json:"created_at"`
	} `json:"notifications"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"has_more"`
}

// getNotifications 打一次列表接口（带 Authorization，query 原样拼在 URL 上）。
func getNotifications(t *testing.T, s *Server, tok, query string) (*notifPageResp, int) {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/notifications"+query, nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.handleNotifications(w, r)
	var body notifPageResp
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应体不是合法 JSON（status=%d）: %v；原文=%s", w.Code, err, w.Body.String())
	}
	return &body, w.Code
}

// TestNotificationsPageDefaultAndHasMore 默认档 20 条＋has_more 的取满近似口径。
func TestNotificationsPageDefaultAndHasMore(t *testing.T) {
	s, tok := newNotifAPIFixture(t, 45)

	// 不带任何参数＝老客户端形态：一页 20 条（旧形态是 100），且明确还有下一页
	first, code := getNotifications(t, s, tok, "")
	if code != http.StatusOK {
		t.Fatalf("带合法 token 却退 %d（应 200）；原文见响应体", code)
	}
	if !first.Success {
		t.Fatal("success 不为 true ⇒ 列表接口把成功写成了失败信封")
	}
	if len(first.Notifications) != store.NotificationPageDefault {
		t.Fatalf("第一页应 %d 条，实际 %d 条", store.NotificationPageDefault, len(first.Notifications))
	}
	if first.Limit != store.NotificationPageDefault || first.Offset != 0 {
		t.Fatalf("档位回显不对：limit=%d offset=%d（应 %d/0）", first.Limit, first.Offset, store.NotificationPageDefault)
	}
	if !first.HasMore {
		t.Fatal("45 条数据的第一页 has_more=false ⇒ 「更多」按钮不会出现，后面的行永远取不到")
	}
	// 最新在前（与 store 侧同一条契约在 HTTP 面上再钉一次：这一层有排序参数被改写的风险）
	for i := 1; i < len(first.Notifications); i++ {
		if first.Notifications[i-1].ID <= first.Notifications[i].ID {
			t.Fatalf("出栈顺序不是最新在前：%d 后面跟了 %d", first.Notifications[i-1].ID, first.Notifications[i].ID)
		}
	}
	// 老客户端兼容腿：元素字段形态一字未改（title/body/ref_type/created_at 都在，且不是空壳）
	if first.Notifications[0].Title == "" || first.Notifications[0].CreatedAt == "" || first.Notifications[0].RefType != "ticket" {
		t.Fatalf("列表项字段形态变了：%+v", first.Notifications[0])
	}

	// 第二页（offset=20）取满 → 仍判 has_more（取满近似的定义）；第三页（offset=40）5 条 → 收口
	second, _ := getNotifications(t, s, tok, "?limit=20&offset=20")
	if len(second.Notifications) != 20 || !second.HasMore {
		t.Fatalf("第二页应 20 条且 has_more=true，实际 %d 条 has_more=%v", len(second.Notifications), second.HasMore)
	}
	if second.Limit != 20 || second.Offset != 20 {
		t.Fatalf("第二页档位回显不对：limit=%d offset=%d", second.Limit, second.Offset)
	}
	third, _ := getNotifications(t, s, tok, "?limit=20&offset=40")
	if len(third.Notifications) != 5 || third.HasMore {
		t.Fatalf("尾页应 5 条且 has_more=false，实际 %d 条 has_more=%v", len(third.Notifications), third.HasMore)
	}
	// 三页合计覆盖全部 45 条且互不重复（HTTP 面的"翻页不丢行"判据，store 侧那条看不见序列化这一层）
	seen := map[int64]bool{}
	for _, page := range []*notifPageResp{first, second, third} {
		for _, nt := range page.Notifications {
			if seen[nt.ID] {
				t.Fatalf("HTTP 面翻页出现重复 ID=%d", nt.ID)
			}
			seen[nt.ID] = true
		}
	}
	if len(seen) != 45 {
		t.Fatalf("三页合计只覆盖 %d 条（写了 45 条）", len(seen))
	}
	t.Logf("读数：45 条→20/20/5，档位回显 %d/%d/%d，has_more=%v/%v/%v",
		first.Limit, second.Limit, third.Limit, first.HasMore, second.HasMore, third.HasMore)
}

// TestNotificationsPageDirtyParamsFoldBack 脏参数一律折回档位，绝不打成 4xx/5xx。
// 这条锁的射程＝"新增防护不拿可用性交押金"（AGENTS §一·10）：
// 把 limit 写成非数字、写成负数、超过上限，界面不该报错，铃铛照样出内容。
func TestNotificationsPageDirtyParamsFoldBack(t *testing.T) {
	s, tok := newNotifAPIFixture(t, 25)

	for _, tc := range []struct {
		name      string
		query     string
		wantCount int
		wantLimit int
	}{
		{"limit 非数字", "?limit=abc", store.NotificationPageDefault, store.NotificationPageDefault},
		{"limit 负数", "?limit=-5", store.NotificationPageDefault, store.NotificationPageDefault},
		{"limit 超上限→钳到 100", "?limit=500", 25, store.NotificationPageMax},
		{"offset 负数→折回 0", "?offset=-9", store.NotificationPageDefault, store.NotificationPageDefault},
		{"offset 非数字→折回 0", "?offset=xx", store.NotificationPageDefault, store.NotificationPageDefault},
	} {
		got, code := getNotifications(t, s, tok, tc.query)
		if code != http.StatusOK {
			t.Fatalf("%s: 退 %d（脏参应折回档位，不该报错）", tc.name, code)
		}
		if !got.Success {
			t.Fatalf("%s: success=false，响应=%s", tc.name, tc.query)
		}
		if len(got.Notifications) != tc.wantCount {
			t.Fatalf("%s: 取到 %d 条，期望 %d 条", tc.name, len(got.Notifications), tc.wantCount)
		}
		// 回显的 limit 必须是**真用的那个**：前端拿它当步长，回显请求原值会把 offset 算歪
		if got.Limit != tc.wantLimit {
			t.Fatalf("%s: 回显 limit=%d，期望 %d（真用档位）", tc.name, got.Limit, tc.wantLimit)
		}
		t.Logf("读数：%s → %d 条，回显 limit=%d", tc.name, len(got.Notifications), got.Limit)
	}
}

// TestNotificationsPageEmptyPageIsArray 空页出栈必须是 `[]` 不是 `null`。
//
// 这条锁**不能**用上面那个 struct 反序列化来判：`null` 与 `[]` 解进 `[]struct{…}` 都是 nil/长度 0，
// 判据在反序列化那一层就失去了射程（同 AGENTS §三 那条「判据要问运行时真实取值」）。
// 所以这里直读原始响应体，并同时钉 `has_more:false`（防「空页还说还有下一页」的第二个形态）。
//
// 反证（手工验一次射程，随批记录）：把 handler 里那句 `if list == nil` 兜底摘掉 ⇒ 本用例红在
// 第一条（响应体出现 `"notifications":null`）；把 effectivePageLimit 的档位算错让 hasMore 恒真 ⇒ 红在第二条。
func TestNotificationsPageEmptyPageIsArray(t *testing.T) {
	s, tok := newNotifAPIFixture(t, 3)

	// 越界 offset＝空页（3 条数据、offset=9999）
	r := httptest.NewRequest("GET", "/api/notifications?offset=99999", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.handleNotifications(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("越界 offset 退 %d（应 200＋空数组，不许报错页）；原文=%s", w.Code, w.Body.String())
	}
	raw := w.Body.String()
	if !strings.Contains(raw, `"notifications":[]`) {
		t.Fatalf("空页出栈不是数组：响应=%s", raw)
	}
	if strings.Contains(raw, `"notifications":null`) {
		t.Fatalf("空页仍出 null：响应=%s", raw)
	}
	if !strings.Contains(raw, `"has_more":false`) {
		t.Fatalf("空页却带 has_more=true：前端会一直转「加载更多」而永远取不到行，响应=%s", raw)
	}

	// 正向对照（负向锁必须配正向，否则「键根本不存在」也能把上面两条判成通过）：
	// 非空页的原始响应里 `notifications` 后面必须紧跟 `[{` —— 这证明"数组形态"这一判据在真数据上
	// 也走得到，而不是因为响应里压根没有这个键才没命中 null。
	r2 := httptest.NewRequest("GET", "/api/notifications", nil)
	r2.Header.Set("Authorization", "Bearer "+tok)
	w2 := httptest.NewRecorder()
	s.handleNotifications(w2, r2)
	rawFull := w2.Body.String()
	if !strings.Contains(rawFull, `"notifications":[{`) {
		t.Fatalf("正向对照不成立：3 条数据的响应里 notifications 不是非空数组，响应=%s", rawFull)
	}
	got, code := getNotifications(t, s, tok, "")
	if code != http.StatusOK || len(got.Notifications) != 3 {
		t.Fatalf("非空页对照支解码不对：code=%d len=%d", code, len(got.Notifications))
	}
	t.Logf("读数：空页响应=%s｜非空页 %d 条", raw, len(got.Notifications))
}

// TestNotificationsPageRequiresAuth 未登录仍 401（分页批不许把归属判据写丢）。
// 这条与上一条方向相反：上一条锁"公开列表面不许因脏参报错"，这一条锁"没登录不许拿到别人的列表"。
func TestNotificationsPageRequiresAuth(t *testing.T) {
	s, _ := newNotifAPIFixture(t, 3)
	r := httptest.NewRequest("GET", "/api/notifications?limit=20&offset=0", nil)
	w := httptest.NewRecorder()
	s.handleNotifications(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录打列表接口退 %d（应 401）；原文=%s", w.Code, w.Body.String())
	}
	t.Logf("读数：未登录 → %d", w.Code)
}
