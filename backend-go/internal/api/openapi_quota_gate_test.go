// ============================================================================
// openapi_quota_gate_test.go — ★ D-8（2026-09-29）OpenAPI Key 日配额的**接口层**回归
//
// 与 internal/store/apikeys_quota_test.go 的分工（两条腿缺一不可）：
//
//	store 那份证的是「条件 UPDATE 这条语句」在并发下恰好放行 K 次；
//	本份证的是**这条语句真的接在鉴权咽喉上**——D-8 的缺陷形态从来不是 SQL 写错，
//	而是「handler 读快照判一次、事后另发一条自增」的分家写法：
//	语句本身没问题，接线方式让判据失效。只测 store 等于测了武器没测扳机。
//
// 本文件锁四件事：
//
//	① 打满即拒：limit=K 的第 K+1 次 authenticateAPIKey 必须回 key_quota_exceeded；
//	② 被拒不计数：拒绝的那次不得吃掉展示用量（call_count/calls_today 停在 K）——
//	   客户在管理台看到的用量必须等于**真放行次数**，多计＝对账争议；
//	③ NoTouch 不产生计数：withTenant 中间件那次解析不算业务调用（2026-08-26 A3 的口径，
//	   当年就是两侧各 +1 导致 limit=N 实际只放行 N/2）；
//	④ 并发真接线：15 个 goroutine 同刻打 limit=5 的 Key ⇒ 恰好 5 次通过（D-8 的正身）。
//
// ★ 反证账（2026-09-29 实跑，别把覆盖说大）：把 authenticateAPIKey 退回旧两段式
//
//	（validateAPIKey 读快照预检 + TouchAPIKey 事后自增）之后，**①② 仍然全绿**——
//	顺序语义在旧写法下本来就是对的，能区分新旧形态的只有 ④（旧写法实测放行 9 次／上限 5 ⇒ 红）。
//	所以 ①② 是**不变量锁**（守住"拒绝不许计费"），④ 才是 D-8 的复现锁；
//	③ 另配反证：把计数挪进 authenticateAPIKeyNoTouch 立刻红（call_count=5）。
//	读这份文件的人若以为"① 绿了就是配额接对了"，正好踩回 D-8 当初藏身的盲区。
//
// 方言自钉（AGENTS §一·4）：本文件自己钉 sqlite 并在结束时恢复 config.C，
// 绝不把 DB_DRIVER 泄漏给同包后续用例；④ 用命名共享缓存内存库（原因见 newQuotaAPIStore 注释）。
// ============================================================================
package api

import (
	"database/sql"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/errors"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// pinQuotaDialect ★ AGENTS §一·4：自钉 sqlite + 结束时恢复，防 run_uat 的 PG env 渗漏到本包后续用例。
func pinQuotaDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newQuotaAPIStore 建一份**只服务本文件**的内存 Store。
//
// share=true 时用命名共享缓存 DSN（file:<唯一名>?mode=memory&cache=shared），
// 这是并发腿④的必需条件：":memory:" 在 database/sql 下是「每条连接各自的私有库」，
// 迁移跑在连接 1，并发 goroutine 被拨到连接 2..N 时那里根本没有 api_keys 表——
// 用例不是红在配额语义上，而是红在夹具上（同一份坑在 store 侧真踩过一次）。
// 连接池刻意不收口到 1：store.New 的 PackagesTenantMigrate 会在 PRAGMA 游标未关时
// 再开一条嵌套查询，单连接池会直接死锁在 store.New（本仓既有夹具的同款注记）。
func newQuotaAPIStore(t *testing.T, share bool) *store.Store {
	t.Helper()
	var (
		raw *sql.DB
		err error
	)
	if share {
		dsn := fmt.Sprintf("file:apiq_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate",
			strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
		raw, err = sql.Open("sqlite", dsn)
	} else {
		raw, err = sql.Open("sqlite", ":memory:")
	}
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if share {
		raw.SetMaxOpenConns(4)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st
}

// callAuth 走一次真实鉴权入口并返回错误码（""=放行）。
func callAuth(s *Server, key string) (string, *store.APIKey) {
	r := httptest.NewRequest("POST", "/openapi/v1/translate", strings.NewReader("{}"))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	ak, ec := s.authenticateAPIKey(r)
	return ec, ak
}

// readQuotaRow 读回某把 Key 的当日用量三元组，判据用。
func readQuotaRow(t *testing.T, s *Server, id int64) (callCount, callsToday int64, date string) {
	t.Helper()
	err := s.Store.DB().QueryRow(`SELECT call_count, calls_today, calls_today_date FROM api_keys WHERE id=?`, id).
		Scan(&callCount, &callsToday, &date)
	if err != nil {
		t.Fatalf("读回配额行失败: %v", err)
	}
	return
}

// TestAuthenticateAPIKeyQuotaRefusesAndDoesNotCount ①+②：打满即拒、被拒不进展示用量。
func TestAuthenticateAPIKeyQuotaRefusesAndDoesNotCount(t *testing.T) {
	pinQuotaDialect(t)
	st := newQuotaAPIStore(t, false)
	s := &Server{Store: st}

	const limit = 2
	plain, err := st.CreateAPIKey(1, 7, "接口层配额闸门", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.GetAPIKeyByHash(store.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= limit; i++ {
		ec, _ := callAuth(s, plain)
		if ec != "" {
			t.Fatalf("第 %d 次（上限 %d）被拒：%s ⇒ 没打满就挡人，客户侧等于服务不可用", i, limit, ec)
		}
	}
	ec, _ := callAuth(s, plain)
	if ec != string(errors.OpenAPIKeyQuotaExceeded) {
		t.Fatalf("第 %d 次放行（返回码 %q）⇒ 配额闸门没接在鉴权咽喉上（D-8 的形态）", limit+1, ec)
	}
	// ② 被拒那次不许吃展示用量：call_count 与 calls_today 都必须停在 limit
	callCount, callsToday, date := readQuotaRow(t, s, k.ID)
	if callsToday != limit || callCount != limit {
		t.Fatalf("拒绝调用被计数了：calls_today=%d call_count=%d（都应=%d）⇒ 客户管理台用量虚高，对账必打架",
			callsToday, callCount, limit)
	}
	if date != time.Now().Format("2006-01-02") {
		t.Fatalf("当日日期锚没写：%q", date)
	}

	// 换一把不限额的 Key 自证「拒绝不是全局开关坏了」：同一 Server 下必须照常放行
	plainFree, err := st.CreateAPIKey(1, 7, "不限额对照", "translate", 0)
	if err != nil {
		t.Fatal(err)
	}
	if ec2, _ := callAuth(s, plainFree); ec2 != "" {
		t.Fatalf("不限额 Key 被拒：%s ⇒ 配额判定漏到了别的 Key 身上", ec2)
	}
}

// TestAuthenticateAPIKeyNoTouchDoesNotCount ③：中间件那次解析不得产生计数（A3 口径）。
// 反证逻辑藏在断言本身：若有人把 NoTouch 也接上 ReserveAPICall，这里立刻红；
// 而若把 authenticateAPIKey 的计数摘掉（变成"只校验不计数"），
// 上面 ① 那条会红——两条合起来才把「谁计数、谁不计数」钉死。
func TestAuthenticateAPIKeyNoTouchDoesNotCount(t *testing.T) {
	pinQuotaDialect(t)
	st := newQuotaAPIStore(t, false)
	s := &Server{Store: st}

	plain, err := st.CreateAPIKey(1, 7, "NoTouch 不计数", "translate", 100)
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.GetAPIKeyByHash(store.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/openapi/v1/balance", strings.NewReader(""))
	r.Header.Set("Authorization", "Bearer "+plain)
	for i := 0; i < 5; i++ {
		if ak, ec := s.authenticateAPIKeyNoTouch(r); ec != "" || ak == nil {
			t.Fatalf("NoTouch 第 %d 次校验失败（ec=%q）⇒ 前置没通过，本用例失去意义", i+1, ec)
		}
	}
	if callCount, callsToday, _ := readQuotaRow(t, s, k.ID); callCount != 0 || callsToday != 0 {
		t.Fatalf("NoTouch 产生了计数：call_count=%d calls_today=%d ⇒ 一次业务调用被算成两次，limit=N 实际只放行 N/2（2026-08-26 A3 同款）",
			callCount, callsToday)
	}
	// NoTouch 也不是配额闸门（它在打满后仍应只回预检结论，权威判定留给 authenticateAPIKey）：
	// 这里显式放行一次，让配额真被占掉，再看 NoTouch 与 authenticateAPIKey 的分工是否仍然成立。
	if ec, _ := callAuth(s, plain); ec != "" {
		t.Fatalf("首次业务调用被拒：%s", ec)
	}
	if _, ec := s.authenticateAPIKeyNoTouch(r); ec == string(errors.OpenAPIKeyQuotaExceeded) {
		t.Log("  ↳ NoTouch 在快照已打满时回预检码：允许（它是止损预检，不是权威闸门）")
	}
}

// TestAuthenticateAPIKeyConcurrentExactlyLimit ④ 并发真接线（HTTP 层的 D-8 正身）：
// limit=5、并发 15 次 ⇒ 恰好 5 次拿到空错误码。
// 用共享缓存库（见 newQuotaAPIStore），否则 15 个 goroutine 落在 15 个私有库上，
// 判据测的是夹具而不是接线。
func TestAuthenticateAPIKeyConcurrentExactlyLimit(t *testing.T) {
	pinQuotaDialect(t)
	st := newQuotaAPIStore(t, true)
	s := &Server{Store: st}

	const limit = 5
	plain, err := st.CreateAPIKey(1, 7, "并发接口配额", "translate", limit)
	if err != nil {
		t.Fatal(err)
	}
	k, err := st.GetAPIKeyByHash(store.HashAPIKey(plain))
	if err != nil {
		t.Fatal(err)
	}

	var passed int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < limit*3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if ec, _ := callAuth(s, plain); ec == "" {
				atomic.AddInt64(&passed, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt64(&passed); got != limit {
		t.Errorf("并发下放行 %d 次（上限 %d）⇒ 判据与计数又分家了（D-8）", got, limit)
	}
	if _, callsToday, _ := readQuotaRow(t, s, k.ID); callsToday != limit {
		t.Errorf("落库当日用量=%d 应等于放行次数 %d ⇒ 放行数与用量数不是一一对应", callsToday, limit)
	}
}
