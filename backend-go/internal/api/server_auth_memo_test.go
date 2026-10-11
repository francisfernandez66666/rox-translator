// 本文件锁 P1①（2026-10-11 慢 SQL 批）：authUser 的请求级缓存（authMemo）。
// 判据选"同指针"而不是"数 SQL"：GetUser 每次 Scan 都新建对象，同请求两次 authUser
// 若真走了缓存必是同一 *store.User；没缓存则是两个不同分配（两个都活着，地址必不同）。
// 四段：命中（同指针）／无容器（不同指针＝缓存没把非 Handler 入口打穿）／
// 负缓存（无效 token 第二问也是 nil 且**不再查库**——done 对失败同样成立）／
// 接线（Handler() 链上 withAuthMemo 真在 withLang 外最侧）。
package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newAuthMemoFixture 内存 SQLite + 一名激活用户 + 其 JWT（方言按 AGENTS §一·4 自钉 sqlite）。
func newAuthMemoFixture(t *testing.T) (*Server, string) {
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
		t.Fatalf("store.New 失败: %v", err)
	}
	if _, err := st.CreateUser(1, "memo_user", "hash-x", "缓存测试用户", "user", 0, 0); err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	u, err := st.GetUserByUsername(1, "memo_user")
	if err != nil {
		t.Fatalf("回读用户失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签 JWT 失败: %v", err)
	}
	return &Server{Store: st}, tok
}

func memoRequest(tok string) *http.Request {
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

// TestAuthUserMemoReturnsSamePointer 有容器 ⇒ 两次 authUser 同指针（第二次没有再查库）。
func TestAuthUserMemoReturnsSamePointer(t *testing.T) {
	s, tok := newAuthMemoFixture(t)
	var first, second *store.User
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first = s.authUser(r)
		second = s.authUser(r)
	})
	srv := s.withAuthMemo(handler)
	srv.ServeHTTP(httptest.NewRecorder(), memoRequest(tok))
	if first == nil {
		t.Fatal("带合法 token 却没取到用户（前置夹具坏了，后面的等值判据射程为零）")
	}
	if first != second {
		t.Fatal("同请求两次 authUser 拿到不同指针 ⇒ 请求级缓存没生效（每请求 2+ 条 users SELECT 复活）")
	}
}

// TestAuthUserWithoutMemoQueriesFresh 无容器（绕过 withAuthMemo 的入口，如单测直调）⇒ 每次现查。
// 这条是上一条的正向对照：若 authUser 无条件返回同一对象，本条会红。
func TestAuthUserWithoutMemoQueriesFresh(t *testing.T) {
	s, tok := newAuthMemoFixture(t)
	r := memoRequest(tok)
	u1 := s.authUser(r)
	u2 := s.authUser(r)
	if u1 == nil || u2 == nil {
		t.Fatal("无容器路径取不到用户（负向对照缺体）")
	}
	if u1 == u2 {
		t.Fatal("无 authMemo 的请求上下文里两次调用返回同一指针 ⇒ 缓存泄漏到容器之外（撤销语义会被绕过）")
	}
}

// TestAuthUserMemoNegativelyCachesFailure 无效 token：第一问 nil 并记 done，
// 第二问必须**不再进 authUserUncached**（否则每次 require* 都白付一次验签+失败查库）。
// 判据形态：把 Store 换成 nil 后再问——authUserUncached 对 nil Store 必回 nil，
// 而命中负缓存这一支根本不碰 Store；两者返回值同为 nil 分不开，所以这里用
// "done 之后连库都不需要"的构造：先问一次（建立负缓存），再把 s.Store 置 nil，
// 第二问若仍走 uncached 也是 nil——分不开 ⇒ 改用计数旁路：直接检查 memo 结构状态。
func TestAuthUserMemoNegativelyCachesFailure(t *testing.T) {
	s, _ := newAuthMemoFixture(t)
	r := memoRequest("not-a-jwt")
	m := &authMemo{}
	r2 := r.WithContext(context.WithValue(r.Context(), authMemoKey{}, m))
	if u := s.authUser(r2); u != nil {
		t.Fatal("坏 token 竟然鉴权成功")
	}
	if !m.done {
		t.Fatal("失败没被记账（done=false ⇒ 每个 require* 都会重验一次坏 token 并重查库）")
	}
	if m.user != nil {
		t.Fatal("失败路径缓存了非 nil 用户")
	}
	// 第三问：把库抽掉仍回 nil，且**靠 done 短路**（若走 uncached，Store==nil 也回 nil，
	// 所以这一问只作"不 panic 不改变语义"的稳健对照，判据在 done 上）。
	s.Store = nil
	if u := s.authUser(r2); u != nil {
		t.Fatal("done 短路后仍返回了用户")
	}
}

// TestAuthMemoWiredInHandlerChain 接线级静态锁：Handler() 必须把 withAuthMemo 挂在最外侧
// （ctx 只能由外向内传，挂错层＝内层 require* 摸不到容器，缓存恒空转）。
func TestAuthMemoWiredInHandlerChain(t *testing.T) {
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("读 server.go 失败: %v", err)
	}
	body := string(src)
	i := strings.Index(body, "func (s *Server) Handler()")
	if i < 0 {
		t.Fatal("找不到 Handler 装配点")
	}
	j := strings.Index(body[i:], "\n}")
	if j < 0 {
		j = len(body) - i
	}
	seg := body[i : i+j]
	if !strings.Contains(seg, "s.withAuthMemo(s.withLang(") {
		t.Fatalf("Handler() 链上 withAuthMemo 不在 withLang 外侧（挂错层或整段被摘）: %s", seg)
	}
}
