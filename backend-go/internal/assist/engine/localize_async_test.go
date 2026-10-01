// ============ localize_async_test.go · 职责说明 ============
// 锁 0AF（2026-10-01）那条修法：挂件首屏 canned 翻译 = **同步有界 + 后台补翻**。
//
// 现网实证（本文件的用例全部从这里长出来）：泰文这类冷缓存语种的第一次翻译在上游要跑满 >30 秒，
// 主站 `/assist-api` 反代 30 秒先砍连接 ⇒ 访客看到 HTTP 502 / 30.001890s / `UPSTREAM_UNAVAILABLE`，
// 而**缓存行永远写不上**，下一个访客再来还是同样的 502。
//
// 每条用例都配了「故意破坏 → 当场红」的反证，破坏点写在用例注释里。
// ⚠️ 时序纪律：本文件**不使用 time.Sleep 赌 goroutine**。要等「某一枪已经打进上游」就收
// gateStub.entered 的信号（阻塞通道 + 有 deadline），要等「后台腿收尾」就等 Engine.cannedBgWg
// （显式同步点）。历史上这类用例被定性的假绿形态就是"依赖 goroutine 刚好来得及"。
// =============================================
package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/assist/llm"
	"translator/internal/errors"
)

// gateStub 一条**可挂起、可手动放行**的假上游。
//
// 为什么现有夹具不够用：seqStub 是瞬间返回的，而本批要复现的形态恰恰是「上游正在被等」——
// 冷语种那一枪跑满几十秒。瞬间返回的上游永远造不出「同步预算到点」这个中间态，
// 于是"有界"和"后台补翻"两条腿都没有射程（这才是本批最需要锁住的两件事）。
type gateStub struct {
	srv *httptest.Server
	// hits 记上游客户数：并发去重与「不许重拨风暴」两条断言都问它。
	hits atomic.Int64
	// entered 每收到一枪 signal 一次枪号（从 1 起）。缓冲 16：测试不取也不会卡住 handler。
	entered chan int64
	// release close 后所有挂住的枪一起放行（收尾用，防 srv.Close 等在跑的请求）。
	release   chan struct{}
	closeOnce sync.Once
	// slow 挂住的枪最多挂这么久（模拟冷语种上游耗时）。
	// ⚠️ 刻意留这一档而不是只等 release：反证（把有界腿写回无条件等待）时用例必须**在 slow 到点就翻红**，
	// 而不是把整轮测试挂死到 Go 的 10 分钟总超时。
	slow time.Duration
	// block 第 N 枪要不要挂住（1 起）；建好后只读，多 goroutine 读安全。
	block map[int64]bool

	mu      sync.Mutex
	prompts []string
	content []string // 按枪号回吐的正文；越界重复最后一条
}

func newGateStub(t *testing.T, opts ...func(*gateStub)) *gateStub {
	t.Helper()
	s := &gateStub{
		entered: make(chan int64, 16),
		release: make(chan struct{}),
		slow:    6 * time.Second,
		block:   map[int64]bool{},
		content: []string{"placeholder"},
	}
	for _, o := range opts {
		o(s)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n := s.hits.Add(1)
		s.mu.Lock()
		s.prompts = append(s.prompts, string(b))
		s.mu.Unlock()
		select {
		case s.entered <- n:
		default:
		}
		if s.block[n] {
			select {
			case <-time.After(s.slow):
			case <-s.release:
			case <-r.Context().Done(): // 有界同步腿到点撤了：这一枪没人等了，直接收
				return
			}
		}
		content := s.content[len(s.content)-1]
		if int(n) <= len(s.content) {
			content = s.content[n-1]
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(content) +
			`},"finish_reason":"stop"}],"usage":{"completion_tokens":10}}`))
	}))
	// 注册顺序＝反序执行：先放行所有挂住的枪，再关服务，否则 srv.Close 会等在跑的请求上。
	t.Cleanup(s.releaseAll)
	t.Cleanup(srv.Close)
	s.srv = srv
	return s
}

// blockCalls 指定哪些枪号要挂住（1 起）。
func blockCalls(ns ...int64) func(*gateStub) {
	return func(s *gateStub) {
		for _, n := range ns {
			s.block[n] = true
		}
	}
}

// contents 按枪号给出正文。
func contents(cs ...string) func(*gateStub) {
	return func(s *gateStub) { s.content = cs }
}

// withSlow 挂住的枪最多挂多久。
func withSlow(d time.Duration) func(*gateStub) {
	return func(s *gateStub) { s.slow = d }
}

func (s *gateStub) releaseAll() { s.closeOnce.Do(func() { close(s.release) }) }

// count 上游被打了几次。
func (s *gateStub) count() int64 { return s.hits.Load() }

// waitHit 在 deadline 内等到「有一枪真的打进了上游」，返回枪号；等不到即判红。
// 这是本文件所有时序判据的**唯一**同步手段之一（另一个是 waitBg）。
func (s *gateStub) waitHit(t *testing.T, d time.Duration) int64 {
	t.Helper()
	select {
	case n := <-s.entered:
		return n
	case <-time.After(d):
		t.Fatalf("%s 内没有请求打进上游 ⇒ 用例前提没了（该拨的那一枪没拨）", d)
		return 0
	}
}

// engine 带这个假上游的测试引擎；secs 为同步预算（写进 configs，走真实配置读腿）。
func (s *gateStub) engine(t *testing.T, syncTimeoutSec string) *Engine {
	t.Helper()
	e := newTestEngine(t)
	// ★ 本包不 import translator/internal/config（assist 存储层是独立 SQLite，没有方言分支），
	// 所以 AGENTS §一·4 那条「自钉方言」在这里射程为零；钉了反而是假动作。
	if syncTimeoutSec != "" {
		if err := e.db.SetConfig(cfgCannedSyncTimeoutSec, syncTimeoutSec); err != nil {
			t.Fatalf("预置同步预算失败：%v", err)
		}
	}
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: s.srv.URL, APIKey: "k", Model: "m"}}, 5)
	return e
}

// waitBg 等后台补翻腿收尾（显式同步点）。
// 收尾判据取 WaitGroup 而不是轮询缓存：后台腿的 Done 排在 SetConfig **之后**执行，
// 所以它返回时缓存状态已经确定，不需要"再等等看"。
func waitBg(t *testing.T, e *Engine, d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		e.cannedBgWg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("后台补翻腿 %s 内没收尾 ⇒ 这条腿挂住了（它只该等自己的预算，不该无界）", d)
	}
}

// assertNoFlightLeak 在途声明必须随两条腿收尾清空：留着就是这一 (kind,lang) 之后**永久**直接出中文，
// 而且日志一行错误都没有——那是比 502 更难发现的坏状态。
func assertNoFlightLeak(t *testing.T, e *Engine) {
	t.Helper()
	e.cannedFlightsMu.Lock()
	n := len(e.cannedFlights)
	e.cannedFlightsMu.Unlock()
	if n != 0 {
		t.Fatalf("在途声明泄漏 %d 格 ⇒ 后续同键 greet 会永久直接出中文", n)
	}
}

// thSource 现网 502 那一类冷语种用例的源文（不带品牌名与积分，口径段走"禁止注入"那一档）。
const thSource = "有什么想了解的？我可以帮你选功能。"

// TestCannedColdLangTimesOutThenBackgroundFillsCache 本批的主断言（现网缺陷本体）：
// 冷缓存 + 上游慢于自有预算 ⇒ 访客链在**预算点**就返回中文原文（绝不等满反代那 30 秒），
// 后台腿随后把译文写进缓存 ⇒ 第二次调用直接拿到译文。
//
// 反证（两条都要红才算锁住）：
//   - 把同步腿写回 `e.translateOnce(ctx, ...)`（不等预算）⇒ 这一枪要挂满 stub.slow(6s) 且
//     返回的是译文而不是中文，本用例的「预算点返回」与「elapsed < 3s」当场红；
//   - 删掉后台腿 ⇒ 缓存永远为空、第二次调用仍是中文。
func TestCannedColdLangTimesOutThenBackgroundFillsCache(t *testing.T) {
	const thai = "อยากทราบอะไรไหม? ฉันช่วยเลือกให้ได้"
	st := newGateStub(t, blockCalls(1), contents(thai))
	e := st.engine(t, "1") // 预算 1 秒，上游第 1 枪挂 6 秒
	ctx := context.Background()

	t0 := time.Now()
	got := e.LocalizeGreeting(ctx, thSource, "th")
	elapsed := time.Since(t0)
	if got != thSource {
		t.Fatalf("冷语种超时后必须原样出中文（绝不编造译文），实际：%q", got)
	}
	// ⚠️ 这一条就是「502 消除」的等价判据：反代 30 秒砍不断一个 1 秒就撤的请求。
	// 预算 1 秒却等到 6 秒以上，说明有界那层根本没生效。
	if elapsed >= 3*time.Second {
		t.Fatalf("同步腿等了 %s ⇒ 没走自有预算（现网就是等满 30 秒被反代砍成 502）", elapsed)
	}
	// ⚠️ 这里不问 st.count()：后台腿就在这一秒被起来并打下第 2 枪，此刻读到 1 还是 2 由调度决定。
	// 枪数判据放在 waitBg 之后（显式同步点）读总数＝2，那才是确定值。

	// 后台腿：同一句再打一次（第 2 枪不挂），成功后写缓存。
	waitBg(t, e, 8*time.Second)
	cached := e.db.GetConfig("i18n:welcome:th", "")
	if !strings.Contains(cached, thai) {
		t.Fatalf("后台补翻没把译文写进缓存（下次 greet 还要现翻）：%q", firstLine(cached))
	}
	// 指纹仍是「原文＋口径」那一份，后台腿不许另算一套（096x-1 的指纹义务延伸到这条腿）
	wantFp := srcFingerprint(thSource + "\x00" + localizeContract("th", srcTopicsOf(thSource)))
	if !strings.HasPrefix(cached, wantFp+"\n") {
		t.Fatalf("后台腿写的指纹与本轮口径不是同一份：want=%s head=%s", wantFp, firstLine(cached))
	}
	if got2 := e.LocalizeGreeting(ctx, thSource, "th"); got2 != thai || st.count() != 2 {
		t.Fatalf("第二次调用应命中缓存（译文 %q，上游 %d 次）", got2, st.count())
	}
	assertNoFlightLeak(t, e)
}

// TestCannedSyncWithinBudgetStaysSynchronous 回归保护：上游在预算内回来时，**行为与今天一字不差**——
// 同步返回译文、只有一次上游、没有后台腿。
// 反证：给同步腿加"一律先出中文再后台补"（把有界改成异步）⇒ 第一次调用拿到中文，当场红；
// 这也是那 30+ 处既有「同步拿到译文」断言不许改语义的原因。
func TestCannedSyncWithinBudgetStaysSynchronous(t *testing.T) {
	st := newGateStub(t, contents("Hello, how can I help?"))
	e := st.engine(t, "5") // 预算给足，且上游并不慢
	got := e.LocalizeGreeting(context.Background(), thSource, "en")
	if !strings.Contains(got, "Hello, how can I help?") {
		t.Fatalf("预算内成功却仍出了中文：%q", got)
	}
	if st.count() != 1 {
		t.Fatalf("预算内应只有一次上游、零后台腿，实际 %d 次", st.count())
	}
	waitBg(t, e, 2*time.Second) // 这里必须**没有**后台腿在跑
	assertNoFlightLeak(t, e)
}

// TestCannedConcurrentMissDialsUpstreamOnce N 个并发访客同键 miss ⇒ 只有**一枪**上游。
//
// 为什么连同步腿也要占格（而不是"只让后台腿去重"）：冷语种上线那一刻是 N 个并发访客同时 miss，
// 后台腿各自去重时 N 条同步腿照样各拨一枪、各等满预算——现网那种"从没真跑过一次却被降级链兜住"的
// 形态就是这么藏起来的。
// 反证：删掉 claimCannedFlight（或只在后台腿里去重）⇒ 8 个并发各拨一枪，hits 变 8，当场红。
// 时序：第 1 枪挂在上游（不靠 sleep 赌），等它 signal 进来后再放其余 7 个 ⇒ 它们必然看见已占的格。
func TestCannedConcurrentMissDialsUpstreamOnce(t *testing.T) {
	st := newGateStub(t, blockCalls(1), withSlow(30*time.Second), contents("Hello, how can I help?"))
	e := st.engine(t, "5") // 预算比用例时长宽：这一条测的是去重，不是超时
	ctx := context.Background()

	// 赢家：挂在同步腿上，等手动放行
	type res struct{ out string }
	first := make(chan res, 1)
	go func() { first <- res{e.LocalizeGreeting(ctx, thSource, "en")} }()
	if n := st.waitHit(t, 5*time.Second); n != 1 {
		t.Fatalf("第一枪应为 1，实际 %d", n)
	}

	// 后来者：必须立刻出中文（不排队、不再各拨一枪）
	var wg sync.WaitGroup
	for i := 0; i < 7; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := e.LocalizeGreeting(ctx, thSource, "en"); got != thSource {
				t.Errorf("同键已有在途翻译时不该返回译文，也不该等：%q", got)
			}
		}()
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatalf("后来者没立刻返回 ⇒ 它们在排队等赢家那一枪（本批口径是不等）")
	}
	if got := st.count(); got != 1 {
		t.Fatalf("8 个并发 miss 只允许 1 次上游调用，实际 %d 次 ⇒ 单飞去重失效", got)
	}

	// 放行赢家：预算内成功 ⇒ 仍然是**同步**把译文交给这个访客，且不起后台腿
	st.releaseAll()
	got := (<-first).out
	if !strings.Contains(got, "Hello, how can I help?") {
		t.Fatalf("赢家那一枪预算内成功了却没拿到译文：%q", got)
	}
	if st.count() != 1 {
		t.Fatalf("赢家成功后不该再有第二次（后台重拨）：%d 次", st.count())
	}
	waitBg(t, e, 2*time.Second)
	if cached := e.db.GetConfig("i18n:welcome:en", ""); !strings.Contains(cached, "Hello") {
		t.Fatalf("赢家的译文没落缓存：%q", firstLine(cached))
	}
	assertNoFlightLeak(t, e)
}

// TestCannedBackgroundProductPassesOutboundGate 后台腿的产物**同样**过出栈闸：
// 给后台腿一份"调用成功但带中文残片"的译文 ⇒ 一行都不许写进缓存，也不许再重拨。
//
// 为什么这一条必须单独锁：后台腿写的是**常驻**首屏那一份（闸门在写侧，见 canned_guard.go），
// 只在同步腿补闸门等于坏形态换个入口继续落库——096x-1 修的正是这个盲区。
// 反证：runCannedBackground 里把 cannedOutboundReject 那一段去掉 ⇒ 带残片的稿子落库，当场红。
// 反证二：后台腿被拒后再起一条后台腿（重拨风暴）⇒ hits 会超过 3，同样红。
func TestCannedBackgroundProductPassesOutboundGate(t *testing.T) {
	src := "你好，我是能言 AI 助手\n积分充值随时开通"
	st := newGateStub(t, blockCalls(1),
		contents(
			"不会送达",
			"Hello, this is LangCross\ncredits充值 anytime",   // 后台腿第一稿：带残片
			"Hello, this is LangCross\n积分 recharge anytime", // 补翻那一枪：残片没减少 ⇒ 保留上一稿
		))
	e := st.engine(t, "1")
	if got := e.LocalizeGreeting(context.Background(), src, "en"); got != src {
		t.Fatalf("同步腿超时应出中文原文：%q", got)
	}
	waitBg(t, e, 8*time.Second)
	if cached := e.db.GetConfig("i18n:welcome:en", ""); cached != "" {
		t.Fatalf("后台腿把未过闸的译文写进了缓存（下次 greet 直接命中坏形态）：%q", firstLine(cached))
	}
	// 3 枪封顶：同步 1（挂死）+ 后台 1 + 后台补翻 1。不写负缓存＝不许在这之上继续重拨。
	if c := st.count(); c != 3 {
		t.Fatalf("后台腿应有的上游次数＝3（超时 1 + 补翻 1 + 重拨 1），实际 %d", c)
	}
	assertNoFlightLeak(t, e)
}

// TestCannedBackgroundSurvivesCanceledRequest 后台腿必须用**独立 ctx**：
// 请求 ctx 已经 cancel（访客走了／反代砍了）时，后台腿仍要把译文写进缓存。
//
// 反证＝本批最容易写错的一处：把 reqCtx 直接传给 launchCannedBackground（或派生自它）⇒
// 后台腿第一行就 ctx canceled，缓存永远空，第二次调用仍拿到中文 ⇒ 红。
// 这正是"修法看着上线了、其实没跑过一次"的形态（同 §一·12 那条「产物能打开不算验收」）。
func TestCannedBackgroundSurvivesCanceledRequest(t *testing.T) {
	st := newGateStub(t, contents("Hello, how can I help?"))
	e := st.engine(t, "5")

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel() // handler 已返回／连接已被砍
	if got := e.LocalizeGreeting(reqCtx, thSource, "en"); got != thSource {
		t.Fatalf("请求 ctx 已 cancel，同步腿不该返回译文：%q", got)
	}
	waitBg(t, e, 8*time.Second)
	// 取消的请求 ctx 让那一枪**没打到上游**（http 客户端直接失败），所以后台腿是唯一一次真调用。
	if c := st.count(); c != 1 {
		t.Fatalf("应只有后台腿那一枪真打了上游，实际 %d 次", c)
	}
	if cached := e.db.GetConfig("i18n:welcome:en", ""); !strings.Contains(cached, "Hello") {
		t.Fatalf("后台腿复用了已取消的请求 ctx ⇒ 译文没落库：%q", firstLine(cached))
	}
	assertNoFlightLeak(t, e)
}

// TestCannedBackgroundDoesNotOverwriteManualRow 后台腿在途期间运营手工改过那一行 ⇒ 后台稿作废不覆盖。
//
// 为什么值得单独一条：这条腿唯一的产物就是"写库"，而它写的是**运营看得见、会手改**的那张表
// （configs.i18n:*，见 localize.go 文件头的 !manual 口径）。同 F-75 那条教训一个形状：
// 把"我手上这一份"当成现值载回去，就把别人的真配置吃了。
// 反证：删掉 runCannedBackground 里的过期判定 ⇒ 手工译文被机翻盖掉，当场红。
func TestCannedBackgroundDoesNotOverwriteManualRow(t *testing.T) {
	st := newGateStub(t, blockCalls(1, 2), withSlow(30*time.Second),
		contents("第 1 枪无人接收", "Machine translated draft", "不该有第 3 枪"))
	e := st.engine(t, "1")
	ctx := context.Background()
	if got := e.LocalizeGreeting(ctx, thSource, "en"); got != thSource {
		t.Fatalf("同步腿超时应出中文：%q", got)
	}
	// 等第 2 枪真的打进上游（后台腿在途），此刻才允许运营"手工改"
	if n := st.waitHit(t, 5*time.Second); n != 1 {
		t.Fatalf("期望第 1 枪打进上游，实际 %d", n)
	}
	if n := st.waitHit(t, 5*time.Second); n != 2 {
		t.Fatalf("期望后台腿那一枪（第 2）打进上游，实际 %d ⇒ 后台腿没起跑，本用例的靶子没了", n)
	}
	const manual = "deadbeefcafe!manual\nHuman written welcome"
	if err := e.db.SetConfig("i18n:welcome:en", manual); err != nil {
		t.Fatalf("预置人工译文失败：%v", err)
	}
	st.releaseAll()
	waitBg(t, e, 8*time.Second)

	if got := e.db.GetConfig("i18n:welcome:en", ""); got != manual {
		t.Fatalf("后台腿覆盖了运营手工改过的那一行：%q", firstLine(got))
	}
	if got := e.LocalizeGreeting(ctx, thSource, "en"); got != "Human written welcome" {
		t.Fatalf("人工档应永久放行：%q", got)
	}
	assertNoFlightLeak(t, e)
}

// TestCannedFlightHandoffKeepsDedupeWhileBackgroundRuns 去重窗口必须**覆盖后台腿**那一段：
// 同步腿超时后把在途声明移交给后台腿，后台腿还在打上游时来的第二个访客照样不许再拨一枪。
//
// 为什么单独一条：移交那一步（同步腿的 defer 见 handedOff 就跳过释放）只在"失败之后"才生效，
// 上面那条并发用例走的是成功路径，压根碰不到它。写错成的形态是"同步腿一律释放"——
// 于是每个超时窗里都会多出一支并发枪，冷语种上线瞬间还是 N 次往返，只是每次不超过预算。
// 反证：同步腿的 defer 改成无条件 release（把 `if !handedOff` 写成无条件）⇒ 第二个访客拿到格并打第 3 枪，
// 已实测该反证先红在「第二个访客不该拿到译文」那一条（它拿到了第 3 枪的译文），紧随的枪数判据也会红。
//
// ⚠️ 第 2 枪的正文必须是**干净无残片**的一行：带汉字就会在 translateOnce 内部触发补翻那一枪
// （见 localize.go 的 hanResidueRuns 分支），上游次数变成 3，本用例的「同键两枪封顶」判据就不是
// 在测去重而是在测时序了。第 3 条正文只在反证态被读到，故给它一份能直接撞红第 421 行的好译文。
func TestCannedFlightHandoffKeepsDedupeWhileBackgroundRuns(t *testing.T) {
	st := newGateStub(t, blockCalls(1, 2), withSlow(30*time.Second),
		contents("第 1 枪无人接收", "Anything you want to know? I can help you pick.", "Hello, how can I help?"))
	e := st.engine(t, "1")
	ctx := context.Background()

	if got := e.LocalizeGreeting(ctx, thSource, "en"); got != thSource {
		t.Fatalf("同步腿超时应出中文：%q", got)
	}
	if n := st.waitHit(t, 5*time.Second); n != 1 {
		t.Fatalf("期望第 1 枪（同步腿）打进上游，实际 %d", n)
	}
	if n := st.waitHit(t, 5*time.Second); n != 2 {
		t.Fatalf("期望第 2 枪（后台腿）打进上游，实际 %d ⇒ 移交没发生，本用例的靶子没了", n)
	}
	// 第二个访客落在后台腿那一枪还没回来的窗口里
	if got := e.LocalizeGreeting(ctx, thSource, "en"); got != thSource {
		t.Fatalf("后台腿在途时第二个访客不该拿到译文，也不该重拨：%q", got)
	}
	if c := st.count(); c != 2 {
		t.Fatalf("同键两枪封顶（同步 1 + 后台 1），实际 %d 次 ⇒ 声明在移交后被提前释放", c)
	}
	st.releaseAll()
	waitBg(t, e, 8*time.Second)
	if cached := e.db.GetConfig("i18n:welcome:en", ""); !strings.Contains(cached, "Anything you want to know?") {
		t.Fatalf("后台腿那一枪的产物没落库：%q", firstLine(cached))
	}
	assertNoFlightLeak(t, e)
}

// TestCannedBudgetClamps 预算的三级来源与硬夹区间（同 AGENTS §一·10 的取向：档位数字不许写死在代码里，
// 也不许被配置顶穿安全线）。
// 反证：去掉 maxSec 那一道夹 ⇒ 管理台写 999 就拿到 999 秒 ⇒ 访客又回到"等满反代 30 秒被砍成 502"，
// 这一档是本批唯一能机械抓住「配置把修法关掉」的判据。
func TestCannedBudgetClamps(t *testing.T) {
	e := newTestEngine(t)
	setSec := func(v string) {
		if err := e.db.SetConfig(cfgCannedSyncTimeoutSec, v); err != nil {
			t.Fatalf("SetConfig：%v", err)
		}
	}
	t.Run("键缺失＝代码默认 8 秒", func(t *testing.T) {
		setSec("")
		if got := e.CannedSyncBudget(); got != 8*time.Second {
			t.Fatalf("默认预算 %s，期望 8s", got)
		}
	})
	t.Run("正常配置生效", func(t *testing.T) {
		setSec("12")
		if got := e.CannedSyncBudget(); got != 12*time.Second {
			t.Fatalf("配置没生效：%s", got)
		}
	})
	t.Run("顶穿反代 30 秒的配置被硬夹到 25 秒", func(t *testing.T) {
		setSec("999")
		if got := e.CannedSyncBudget(); got > 25*time.Second {
			t.Fatalf("预算 %s 超过 25 秒 ⇒ 又会被 /assist-api 的 30 秒砍断，本批修法等于关掉", got)
		}
	})
	t.Run("非法值与非正数回落安全档", func(t *testing.T) {
		setSec("abc")
		if got := e.CannedSyncBudget(); got != 8*time.Second {
			t.Fatalf("非数字应回落默认，实际 %s", got)
		}
		setSec("0")
		if got := e.CannedSyncBudget(); got != 1*time.Second {
			t.Fatalf("0 不该等于关闭，实际 %s", got)
		}
	})
	t.Run("后台腿预算同样有区间", func(t *testing.T) {
		if err := e.db.SetConfig(cfgCannedBgTimeoutSec, "99999"); err != nil {
			t.Fatalf("SetConfig：%v", err)
		}
		if got := e.cannedBgBudget(); got > 300*time.Second {
			t.Fatalf("后台腿预算没有上限：%s", got)
		}
		if err := e.db.SetConfig(cfgCannedBgTimeoutSec, ""); err != nil {
			t.Fatalf("SetConfig：%v", err)
		}
		if got := e.cannedBgBudget(); got != 60*time.Second {
			t.Fatalf("后台腿默认预算 %s，期望 60s", got)
		}
	})
}

// TestCannedFlightClaimReleaseTTL 在途声明的三态：占用中拒绝、释放后放行、超时（TTL）自动清理。
//
// TTL 那一档为什么必须有：正常路径释放全走 defer（含 panic 路径），但**将来**谁在腿里加一个
// 不看 ctx 的挂死调用，声明就会永久占格 ⇒ 这一 (kind,lang,指纹) 之后每次 greet 直接出中文，
// 而且日志一行错误都没有。这一档是那种退化形态唯一的机械防线。
// 反证：claimCannedFlight 里去掉时间戳判定 ⇒ 第三段一直拿不到格；release 写成空操作 ⇒ 第二段红。
func TestCannedFlightClaimReleaseTTL(t *testing.T) {
	e := newTestEngine(t)
	key := cannedFlightKey("welcome", "th", "abcdef012345")

	if !e.claimCannedFlight(key) {
		t.Fatal("空格应能拿到声明")
	}
	if e.claimCannedFlight(key) {
		t.Fatal("同键第二格必须被拒（否则并发访客各拨一枪）")
	}
	e.releaseCannedFlight(key)
	if !e.claimCannedFlight(key) {
		t.Fatal("释放后必须能重新拿格（否则一次失败就永久不出译文）")
	}
	// 把这一格的持有时间人为推到过去 ⇒ TTL 必须把它当已死之格回收
	e.cannedFlightsMu.Lock()
	e.cannedFlights[key] = time.Now().Add(-e.cannedFlightTTL() - time.Minute)
	e.cannedFlightsMu.Unlock()
	if !e.claimCannedFlight(key) {
		t.Fatal("超过 TTL 的残留声明没被回收 ⇒ 同键永久占格，后续 greet 恒出中文且零报错")
	}
	// 不同指纹（运营改了原文／口径升档）不该被旧那一格挡住
	if !e.claimCannedFlight(cannedFlightKey("welcome", "th", "999999999999")) {
		t.Fatal("换了指纹的新档必须能立刻重翻，不该等旧档的残留")
	}
}

// TestCannedAsyncReasonTagsAreContract 分档名是**对外排障契约**，逐字钉（同 094x 那七个 reject*
// 与 096x-1 那三个 cannedReject*）：现网排障是先 grep 日志里的 reason 再决定动哪一档配置，
// 改名＝把运维的抓手换成一句"看起来像"的中文。
func TestCannedAsyncReasonTagsAreContract(t *testing.T) {
	if cannedSyncTimeout != "canned_sync_timeout" || cannedSyncCanceled != "canned_sync_canceled" ||
		cannedSyncUpstream != "canned_sync_upstream" {
		t.Fatalf("同步腿分档名被改：%s / %s / %s", cannedSyncTimeout, cannedSyncCanceled, cannedSyncUpstream)
	}
	if cannedBgTimeout != "canned_bg_timeout" || cannedBgUpstream != "canned_bg_upstream" ||
		cannedBgGated != "canned_bg_gated" || cannedBgStale != "canned_bg_stale" || cannedBgPanic != "canned_bg_panic" {
		t.Fatalf("后台腿分档名被改：%s / %s / %s / %s / %s",
			cannedBgTimeout, cannedBgUpstream, cannedBgGated, cannedBgStale, cannedBgPanic)
	}
	// 三档必须能互相区分：把「超时」和「上游报错」合成一档就回到"只能猜"的原状态
	tags := map[string]bool{cannedSyncTimeout: true, cannedSyncCanceled: true, cannedSyncUpstream: true,
		cannedBgTimeout: true, cannedBgUpstream: true, cannedBgGated: true, cannedBgStale: true, cannedBgPanic: true}
	if len(tags) != 8 {
		t.Fatal("分档名出现重复，日志将无法区分两种不同的病")
	}
}

// TestCannedFailureReasonClassified 分档判据本身：**两问都要**（自己的有界 ctx ＋上游返回的 error）。
// 只看 errors.Is 会漏（http 客户端把 deadline 包进 *url.Error），只看 ctx.Err() 会把
// 「上游自己挂了但还没到预算」错记成超时——那两种病的运维动作完全不同。
func TestCannedFailureReasonClassified(t *testing.T) {
	expired, cancelE := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelE()
	// 有 deadline 的等待（不是 sleep）：确认这一条 ctx 的预算**真的**到点了
	select {
	case <-expired.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("夹具的有界 ctx 没到点，本用例的靶子没了")
	}
	if got := cannedFailureReason(io.ErrUnexpectedEOF, expired, cannedSyncTimeout, cannedSyncCanceled, cannedSyncUpstream); got != cannedSyncTimeout {
		t.Fatalf("预算已到点却判成 %q", got)
	}
	canceled, cancelC := context.WithCancel(context.Background())
	cancelC()
	<-canceled.Done() // 同上：等它真的进入 Canceled 态再判
	if got := cannedFailureReason(io.ErrUnexpectedEOF, canceled, cannedSyncTimeout, cannedSyncCanceled, cannedSyncUpstream); got != cannedSyncCanceled {
		t.Fatalf("请求被取消却判成 %q ⇒ 现网「反代砍连接」与「上游挂了」又是同一行字", got)
	}
	live, cancelL := context.WithCancel(context.Background())
	defer cancelL()
	if got := cannedFailureReason(io.ErrUnexpectedEOF, live, cannedBgTimeout, cannedBgUpstream, cannedBgUpstream); got != cannedBgUpstream {
		t.Fatalf("预算内上游报错应判 %q，实际 %q", cannedBgUpstream, got)
	}
	// ctx 为 nil（调用方没给有界 ctx）不许 panic，也不许伪装成超时
	if got := cannedFailureReason(io.ErrUnexpectedEOF, nil, cannedBgTimeout, cannedBgUpstream, cannedBgUpstream); got != cannedBgUpstream {
		t.Fatalf("无有界 ctx 时应回落其它档，实际 %q", got)
	}
	// 上游自己就把 deadline 写在错误里时（没有有界 ctx 可读）也必须归到超时档
	if got := cannedFailureReason(context.DeadlineExceeded, nil, cannedBgTimeout, cannedBgUpstream, cannedBgUpstream); got != cannedBgTimeout {
		t.Fatalf("errors.Is 那一腿没接住：%q", got)
	}
}

// TestCannedBackgroundCtxIsIndependent 后台腿那条 ctx 的**派生口径**单独钉（纪律第 1 条的机制层）。
// 这一行就是本批最容易写错的地方：`WithTimeout(reqCtx, …)` 语法更自然、编译更顺，
// 而它让后台腿一出生就带着「访客已经走了」，第一枪即 ctx canceled ⇒ 缓存永远写不上，
// 修法看起来上线了、其实从没跑过。端到端那一头是 TestCannedBackgroundSurvivesCanceledRequest。
// 反证：把 cannedBackgroundCtx 的父 ctx 换成传进来的 reqCtx ⇒ 前两条断言同时红。
func TestCannedBackgroundCtxIsIndependent(t *testing.T) {
	reqCtx, cancel := context.WithCancel(errors.WithTraceID(context.Background(), "trace0af-derived"))
	cancel() // 请求已经结束

	bgCtx, cancelBg := cannedBackgroundCtx(reqCtx, 3*time.Second)
	defer cancelBg()

	if bgCtx.Err() != nil {
		t.Fatalf("后台腿继承了已取消的请求 ctx ⇒ 它永远打不出去：%v", bgCtx.Err())
	}
	if got := errors.TraceIDFromContext(bgCtx); got != "trace0af-derived" {
		t.Fatalf("后台腿丢了 trace_id（日志串不回访客那一次 greet）：%q", got)
	}
	deadline, ok := bgCtx.Deadline()
	if !ok || deadline.Sub(time.Now()) <= 0 || deadline.Sub(time.Now()) > 3*time.Second {
		t.Fatalf("后台腿没挂上自己的预算：deadline ok=%v 剩余 %s", ok, time.Until(deadline))
	}
	// 请求 ctx 上没有 trace_id 时（后台腿自己造一条），也不许把 nil/空串送进日志
	bare, cancelBare := cannedBackgroundCtx(context.Background(), time.Second)
	defer cancelBare()
	if errors.TraceIDFromContext(bare) == "" {
		t.Fatal("reqCtx 没有 trace_id 时后台腿必须自己生成一条，否则这一枪在日志里查不到")
	}
}
