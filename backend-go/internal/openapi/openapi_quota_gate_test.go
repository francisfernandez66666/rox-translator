// ============================================================================
// openapi_quota_gate_test.go — ★ D-8 第二道门（2026-09-29）：internal/openapi.Middleware 的配额接线
//
// 为什么给一个「当前没有生产调用方」的包写闸门：
//
//	全链路审计实测本包引用数为 0，现网 OpenAPI 鉴权走 internal/api/admin_openapi.go。
//	但本包提供了一个同名能力的**现成中间件**，任何一次「把它挂到某条新路由上」都会让线上
//	多出一个鉴权入口——如果它没有配额判定，那就是 D-8 的**第二个形态**（接口层读快照判一次、
//	事后另发一条自增），而这次的红灯不会在任何 api 包用例里亮。
//	所以这里锁的不是「今天的生产行为」，而是「这道门一旦被接线时必须是合格形态」。
//
// 本文件锁四件事：
//
//	① 打满即拒：limit=K 的第 K+1 次请求必须 429（不是 200、也不是 401）；
//	② 被拒不计数：拒绝那次不得吃展示用量（call_count/calls_today 停在 K）——
//	   这条同时兜住「Middleware 里既调 ReserveAPICall 又调 TouchAPIKey」的重复计数：
//	   Redis 未启用时两条都会写 calls_today，等值锁会直接翻红（2026-08-26 A3 的口径）；
//	③ 鉴权失败零计数：缺头/坏 Key 走 401，不得留下任何调用计数；
//	④ 并发真接线：8 个 goroutine 同刻打 limit=5 的 Key ⇒ 恰好 5 次 2xx。
//
// 方言自钉（AGENTS §一·4）：本文件自己钉 sqlite 并在结束时恢复 config.C。
// 并发腿用**命名共享缓存**内存库：":memory:" 在 database/sql 下是「每条连接各自的私有库」，
// goroutine 被拨到第 2 条连接时那里根本没有 api_keys 表，用例就会红在夹具而不是语义上
// （同一份坑在 store 侧、api 侧都真踩过，口径见 internal/api/openapi_quota_gate_test.go 的注记）。
// ============================================================================
package openapi

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// pinQuotaDialect ★ AGENTS §一·4：自钉 sqlite 并在结束时恢复，防 run_uat 的 PG env 渗漏。
func pinQuotaDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newMiddlewareStore 建一份只服务本文件的内存 Store。
// share=true → 命名共享缓存 DSN（并发腿必需）；连接池不收口到 1，
// 否则 store.New 的 PackagesTenantMigrate 会在 PRAGMA 游标未关时嵌套查询直接死锁。
func newMiddlewareStore(t *testing.T, share bool) *store.Store {
	t.Helper()
	var (
		raw *sql.DB
		err error
	)
	if share {
		dsn := fmt.Sprintf("file:opmw_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate",
			strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
		raw, err = sql.Open("sqlite", dsn)
		if err == nil {
			raw.SetMaxOpenConns(4)
		}
	} else {
		raw, err = sql.Open("sqlite", ":memory:")
	}
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st
}

// okHandler 被保护的业务端点：走到这里就代表鉴权 + 配额都放行。
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// doAuthed 真实地打一次中间件（走 httptest，不手搓内部函数），返回状态码与响应体。
func doAuthed(st *store.Store, key string) (int, string) {
	r := httptest.NewRequest("POST", "/openapi/v1/translate", strings.NewReader("{}"))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	Middleware(st, okHandler()).ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// readQuotaRow 读回展示用量三元组（call_count / calls_today / 当日锚日期）。
func readQuotaRow(t *testing.T, st *store.Store, id int64) (callCount, callsToday int64, date string) {
	t.Helper()
	if err := st.DB().QueryRow(`SELECT call_count, calls_today, calls_today_date FROM api_keys WHERE id=?`, id).
		Scan(&callCount, &callsToday, &date); err != nil {
		t.Fatalf("读回配额行失败: %v", err)
	}
	return
}

// TestMiddlewareQuotaRefusesAndDoesNotCount ①+②：打满即拒、被拒不进展示用量。
func TestMiddlewareQuotaRefusesAndDoesNotCount(t *testing.T) {
	pinQuotaDialect(t)
	st := newMiddlewareStore(t, false)

	const limit = 2
	plain, err := st.CreateAPIKey(1, 7, "第二道门配额", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.GetAPIKeyByHash(store.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= limit; i++ {
		code, body := doAuthed(st, plain)
		if code != http.StatusOK {
			t.Fatalf("第 %d 次（上限 %d）被拒：code=%d body=%s ⇒ 没打满就挡人", i, limit, code, body)
		}
	}
	code, body := doAuthed(st, plain)
	if code != http.StatusTooManyRequests {
		t.Fatalf("第 %d 次返回 %d（body=%s）⇒ 配额闸门没接在鉴权咽喉上（D-8 的形态）", limit+1, code, body)
	}
	// ② 拒绝那次不许吃展示用量；同时也只有「没有第二次计数」才能停在这个等值上
	callCount, callsToday, date := readQuotaRow(t, st, k.ID)
	if callsToday != limit || callCount != limit {
		t.Fatalf("用量与真放行次数不等：calls_today=%d call_count=%d（都应=%d）⇒ 要么拒了还计数，要么两侧各 +1（2026-08-26 A3）",
			callsToday, callCount, limit)
	}
	if date != time.Now().Format("2006-01-02") {
		t.Fatalf("当日日期锚没写：%q", date)
	}

	// 自证「拒绝不是全局开关坏了」：不限额的 Key 在同一份 Store 上必须照常放行
	plainFree, err := st.CreateAPIKey(1, 7, "不限额对照", "translate", 0)
	if err != nil {
		t.Fatal(err)
	}
	if c, b := doAuthed(st, plainFree); c != http.StatusOK {
		t.Fatalf("不限额 Key 被拒：code=%d body=%s ⇒ 配额判定漏到了别的 Key 身上", c, b)
	}
}

// TestMiddlewareAuthFailureHasNoCount ③：鉴权失败一律 401 且零计数。
// 这条腿的存在意义：缺这一腿就无法区分「闸门真的在挡」与「整条链根本没打到计数」。
func TestMiddlewareAuthFailureHasNoCount(t *testing.T) {
	pinQuotaDialect(t)
	st := newMiddlewareStore(t, false)

	plain, err := st.CreateAPIKey(1, 7, "鉴权失败零计数", "translate", 5)
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.GetAPIKeyByHash(store.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		key  string
	}{
		{"缺 Authorization 头", ""},
		{"坏 Key（哈希查不到）", "tk_does_not_exist_000000"},
	}
	for _, tc := range cases {
		code, _ := doAuthed(st, tc.key)
		if code != http.StatusUnauthorized {
			t.Fatalf("%s → code=%d（期望 401）", tc.name, code)
		}
	}
	// 停用态也必须是 401，而不是放行后才发现配额没事可做
	if err := st.SetAPIKeyStatus(k.ID, k.TenantID, "disabled"); err != nil {
		t.Fatalf("停用 Key 失败: %v", err)
	}
	if code, _ := doAuthed(st, plain); code != http.StatusUnauthorized {
		t.Fatalf("已停用 Key 返回 code=%d（期望 401）", code)
	}
	callCount, callsToday, _ := readQuotaRow(t, st, k.ID)
	if callCount != 0 || callsToday != 0 {
		t.Fatalf("鉴权失败却产生了计数：call_count=%d calls_today=%d（都应 0）⇒ 失败请求进了客户用量账单", callCount, callsToday)
	}
}

// TestMiddlewareConcurrentAdmitsExactlyLimit ④：并发同刻打 limit=5 ⇒ 恰好 5 次 2xx。
// 这条是 D-8 的**真判据**：串行语义一直是对的，只有并发才把「判据与计数分家」照出来。
func TestMiddlewareConcurrentAdmitsExactlyLimit(t *testing.T) {
	pinQuotaDialect(t)
	st := newMiddlewareStore(t, true)

	const limit = 5
	const goroutines = 8
	plain, err := st.CreateAPIKey(1, 7, "并发接线", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.GetAPIKeyByHash(store.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	var admitted int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // 齐发：让 8 个请求尽量落在同一个行锁窗口里
			code, _ := doAuthed(st, plain)
			if code == http.StatusOK {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&admitted); got != limit {
		t.Fatalf("并发放行 %d 次（上限 %d，共 %d 个请求）⇒ 配额不是原子判据（D-8）", got, limit, goroutines)
	}
	callCount, callsToday, _ := readQuotaRow(t, st, k.ID)
	if callCount != limit || callsToday != limit {
		t.Fatalf("展示用量与放行不等：call_count=%d calls_today=%d（都应=%d）", callCount, callsToday, limit)
	}

	// 收尾腿：并发把额度吃干之后，串行再打必须仍被拒。
	// 这条防止的反向形态是「并发计数丢了但放行照旧」——那样 admitted==limit 可能只是
	// 某条连接被拒得早，而行上的 calls_today 其实根本没涨到上限。
	if c, b := doAuthed(st, plain); c != http.StatusTooManyRequests {
		t.Fatalf("并发打满后串行仍放行：code=%d body=%s", c, b)
	}
}
