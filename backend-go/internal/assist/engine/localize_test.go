// localize_test.go — ★ 082x 第七条（2026-09-29 换件后现网复问第二批）的两条断言：
//  1. canned 译文缓存的失效判据必须带上**翻译口径**（品牌名表／计费单位表／固定句式版本号）。
//     现网实测：术语口径上一批已经上线，英文首屏照样念 "integral"——因为缓存只认「中文原文变了没」，
//     而原文常年不改。**换件对这条软路径完全无效，界面却一切正常**，这是最坏的一种失效。
//  2. 译文里留着没翻的中文词时要补翻一次，且**只在确实改善时**才采用新稿
//     （现网读数：英文首屏 "credits充值"、日文首屏「翻訳什么？」——术语修对了，句子只翻半句）。
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

	"translator/internal/assist/llm"
)

// seqStub 按顺序吐多条固定正文的假上游，并记下每次收到的请求体。
// 与 stubLLM 的差别只有「响应会变」：补翻这条链要看第二次调用发了什么提示词、最终采用哪一稿，
// 单条固定响应会让补翻恒判「没改善」，那条路就永远测不到。
type seqStub struct {
	url     string
	hits    atomic.Int64
	mu      sync.Mutex
	prompts []string
}

func newSeqStub(t *testing.T, contents ...string) *seqStub {
	t.Helper()
	if len(contents) == 0 {
		t.Fatal("seqStub 至少要给一条响应")
	}
	s := &seqStub{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		n := s.hits.Add(1) - 1
		s.mu.Lock()
		s.prompts = append(s.prompts, string(b))
		s.mu.Unlock()
		content := contents[len(contents)-1] // 超出条数就重复最后一条：防止用例写成"第二次拿到空响应"这种假失败
		if n < int64(len(contents)) {
			content = contents[n]
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(content) +
			`},"finish_reason":"stop"}],"usage":{"completion_tokens":10}}`))
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// count 上游被打了几次。
func (s *seqStub) count() int64 { return s.hits.Load() }

// body 第 i 次请求的原始请求体（越界回空串）。
func (s *seqStub) body(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.prompts) {
		return ""
	}
	return s.prompts[i]
}

// engine 带这个假上游的测试引擎。
func (s *seqStub) engine(t *testing.T) *Engine {
	t.Helper()
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: s.url, APIKey: "k", Model: "m"}}, 5)
	return e
}

// TestCannedCacheInvalidatedByTranslationContract 缓存指纹必须含口径。
// 五腿：首翻一次 → 命中不重复打上游 → 指纹等值（不是"变了就行"）→ 改术语表／品牌表必须重翻 →
// 人工档（!manual）在口径变更后仍然永久放行。
func TestCannedCacheInvalidatedByTranslationContract(t *testing.T) {
	st := newSeqStub(t, "Hello, this is LangCross")
	e := st.engine(t)
	ctx := context.Background()
	// ★ 096x-1：源文必须**同时提到品牌名与积分**，下面 ④⑤ 两条腿才各自成立——
	// 口径现在按源文投（见 translateContract），源文没提「积分」时术语表根本进不了这一枪的口径段，
	// 拿一个不含积分的源文去断言"改术语表要重翻"会恒真地红（那是用例的口径过期，不是代码退化）。
	src := "你好，我是能言 AI 助手，积分充值随时开通"

	if got := e.LocalizeGreeting(ctx, src, "en"); !strings.Contains(got, "Hello") {
		t.Fatalf("英文界面没拿到译文：%q", got)
	}
	if st.count() != 1 {
		t.Fatalf("首翻应当只打一次上游，实际 %d 次", st.count())
	}
	// ② 同原文同语种第二次命中缓存
	if got := e.LocalizeGreeting(ctx, src, "en"); !strings.Contains(got, "Hello") || st.count() != 1 {
		t.Fatalf("缓存没命中（上游被打到 %d 次）：%q", st.count(), got)
	}
	// ③ 指纹等值锁：库里那行头**就是**「原文＋口径」的指纹，不是别的什么串
	wantFp := srcFingerprint(src + "\x00" + localizeContract("en", srcTopicsOf(src)))
	if cached := e.db.GetConfig("i18n:welcome:en", ""); !strings.HasPrefix(cached, wantFp+"\n") {
		t.Fatalf("缓存指纹没带上翻译口径（期望前缀 %s，实际 %q）——只算原文就是今天线上那个形态", wantFp, firstLine(cached))
	}
	// ④ 计费单位表改一个词＝口径变了＝旧译文作废（今天现网事故的正身）
	oldTerm := pointsTermByLang["en"]
	t.Cleanup(func() { pointsTermByLang["en"] = oldTerm })
	pointsTermByLang["en"] = "creditsRenewed"
	e.LocalizeGreeting(ctx, src, "en")
	if st.count() != 2 {
		t.Fatalf("术语口径变了却没重翻——线上就是「换件等于没修」这个形态（上游次数 %d）", st.count())
	}
	// ⑤ 品牌名表同理（两条口径都在 translateContract 里，缺一条就是另一版本的同一事故）
	oldBrand := brandKanjiLocales["en"]
	t.Cleanup(func() { brandKanjiLocales["en"] = oldBrand })
	brandKanjiLocales["en"] = true
	e.LocalizeGreeting(ctx, src, "en")
	if st.count() != 3 {
		t.Fatalf("品牌名口径变了却没重翻（上游次数 %d）", st.count())
	}
	// ⑥ 人工改过的译文不许被口径变更吃掉（放行在指纹比对之前）
	_ = e.db.SetConfig("i18n:welcome:en", "deadbeefcafe!manual\nHuman written welcome")
	pointsTermByLang["en"] = "creditsOnceMore"
	if got := e.LocalizeGreeting(ctx, src, "en"); got != "Human written welcome" || st.count() != 3 {
		t.Fatalf("人工译文被口径变更覆盖或重翻了：%q（次数 %d）", got, st.count())
	}
}

// TestHanResidueRunsByLocale 判残按语种分档。
// 核心风险是**把正常日文判成残留**：日文正文本来就写汉字，一律判残会让每次日文 greet 白打一次补翻，
// 并把好端端的译文拿去重写。所以日文档只认「能在源文里原样对上、且含简体独有字形」的最长子串。
func TestHanResidueRunsByLocale(t *testing.T) {
	src := "你好，我是能言 AI 助手，积分充值随时开通，你想翻译什么？"
	cases := []struct {
		lang string
		out  string
		want []string
	}{
		{"en", "Hi, this is LangCross, credits充值 anytime", []string{"充值"}},
		{"en", "Hi, this is LangCross, credits anytime", nil},
		{"en", "积分 recharged, 积分 again", []string{"积分"}}, // 去重且按出现顺序
		{"ru", "Привет, 积分 пополнение", []string{"积分"}},
		{"ko", "안녕하세요 积分", []string{"积分"}},
		{"th", "สวัสดี 积分", []string{"积分"}},
		// 中文系访客：正文里有汉字是**正常渲染**，判残＝每次白打一次补翻
		{"zh", src, nil},
		{"zh_hant", "你好，我是能言", nil},
		{"", src, nil},
		// 日文：正常汉字正文不许判残（翻訳／会話／企業 都是日文正常写法）
		{"ja", "文書翻訳・会話翻訳・企業用語ベース・ポイント充実", nil},
		// 日文：照抄源文且含简体字形的那一段才是残留——现网那条 `翻訳什么？` 就是这个形态
		{"ja", "ポイント充実、「翻訳什么？」とお答えします", []string{"什么"}},
		{"ja", "翻訳できます", nil},
		{"ja", "积分を充值しました", []string{"积分", "充值"}},
		// 表外语种按「非汉字档」处理：有汉字就判残，绝不猜语种
		{"xx", "xx 汉字", []string{"汉字"}},
	}
	for _, c := range cases {
		if got := hanResidueRuns(c.lang, src, c.out); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("hanResidueRuns(%q, %q) = %v，期望 %v", c.lang, c.out, got, c.want)
		}
	}
	// 顺序腿：两段不同残留按出现顺序给，补翻提示词才能点名点全
	if got := hanResidueRuns("en", src, "充值 x 积分 y"); strings.Join(got, "|") != "充值|积分" {
		t.Fatalf("残留顺序或去重不对：%v", got)
	}
}

// TestHanResidueRunsCatchesChineseWordForms ★ 082x 第八条：字形都对、词形是中文的那一档残留。
//
// 现网读数：日文首屏拿到「文件翻訳・会話翻訳」——上一档判残只认「简体独有字形」，
// 而 文/件 两个字日文都写（文件＝文書/ファイル），于是最典型的中文词形照样放行。
// 这条断言锁两侧：中文词形要抓到，日文正常汉字词（会話／企業／翻訳／情報系）不许抓。
func TestHanResidueRunsCatchesChineseWordForms(t *testing.T) {
	src := "你好，我是能言 AI 助手，文件翻译、对话翻译、企业术语库、积分充值、邮件通知"
	cases := []struct {
		out  string
		want []string
	}{
		// 现网形态：中文词形「文件」照抄进日文（日文该写 ファイル／文書）
		{"ファイル翻訳は元のレイアウトを保持", nil}, // 全假名＋日文汉字形态，一个中文词形都不许抓
		{"文件翻訳・会話翻訳・企業用語ベース", []string{"文件翻"}},
		// 日文正常汉字词一律不许判残：这些词里的每一段都能在源文里对上，但日文本来就这么写
		{"文書翻訳・会話翻訳・企業用語ベース・ポイント", nil},
		// 「邮件／信息／搜索」同族：单字合法、组合是中文
		{"メールではなく邮件で連絡ください", []string{"邮件"}},
	}
	for _, c := range cases {
		if got := hanResidueRuns("ja", src, c.out); strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("ja 判残 %q ⇒ %v，期望 %v", c.out, got, c.want)
		}
	}
	// 反向锁：中文词形表**不能**把英文档的判据放宽——en 段里有汉字就判残，跟词形无关
	if got := hanResidueRuns("en", src, "please upload the 文件"); len(got) == 0 {
		t.Fatalf("en 档漏判：汉字段必须判残，实际 %v", got)
	}
}

// TestTranslateOnceStripsContractEcho ★ 082x 第八条：翻译模型把**翻译要求本身**写进译文时要剥掉，
// 且剥完不能把整条译文判成空（现网实证：日文首屏尾巴挂着「（行数一致、品牌名「能言」保持…）」，
// 而这一条被当成功译文**落了库**——脏尾巴从此常驻英文/日文访客的首屏）。
func TestTranslateOnceStripsContractEcho(t *testing.T) {
	ctx := context.Background()
	src := "你好，我是能言 AI 助手"

	t.Run("夹带口径复述：剥括号段、留好译文、落的缓存是干净那份", func(t *testing.T) {
		st := newSeqStub(t, "こんにちは、能言のAIアシスタントです（行数一致、品牌名「能言」保持、ポイント）")
		e := st.engine(t)
		got := e.LocalizeGreeting(ctx, src, "ja")
		if strings.Contains(got, "行数一致") || strings.Contains(got, "品牌名") {
			t.Fatalf("译文里的口径复述没剥净：%q", got)
		}
		if !strings.Contains(got, "AIアシスタント") {
			t.Fatalf("剥复述时把好译文一起吃掉了：%q", got)
		}
		if cached := e.db.GetConfig("i18n:welcome:ja", ""); strings.Contains(cached, "行数一致") {
			t.Fatalf("脏译文还是落了库（下次 greet 直接命中它）：%q", cached)
		}
	})

	t.Run("整条都是口径复述：判失败出中文原文，不落缓存", func(t *testing.T) {
		st := newSeqStub(t, "（行数一致、品牌名一律写作能言、不许加解释）")
		e := st.engine(t)
		if got := e.LocalizeGreeting(ctx, src, "ja"); got != src {
			t.Fatalf("译文只剩复述时应按失败处理、原样出中文，实际：%q", got)
		}
		if cached := e.db.GetConfig("i18n:welcome:ja", ""); cached != "" {
			t.Fatalf("失败的译文被写进缓存了：%q", cached)
		}
	})

	t.Run("正常带括号的译文不许被剥（负向对照，防止把答案吃掉）", func(t *testing.T) {
		st := newSeqStub(t, "Hello, I'm LangCross (available 24/7)")
		e := st.engine(t)
		if got := e.LocalizeGreeting(ctx, src, "en"); got != "Hello, I'm LangCross (available 24/7)" {
			t.Fatalf("正常括号说明被误伤：%q", got)
		}
	})
}

// 任一不成立一律保留上一稿——补翻是修饰，不许拿一份没验过的新稿把业务数字改坏。
// TestRepairHanResidueAdoptsOnlyImprovement 补翻三条硬判据：残片变少、行数不变、调用成功。
func TestRepairHanResidueAdoptsOnlyImprovement(t *testing.T) {
	ctx := context.Background()
	src := "你好，我是能言 AI 助手\n积分充值随时开通"

	t.Run("改善则采用并落缓存", func(t *testing.T) {
		st := newSeqStub(t,
			"Hello, this is LangCross\ncredits充值 anytime",
			"Hello, this is LangCross\ncredits recharge anytime")
		e := st.engine(t)
		got := e.LocalizeGreeting(ctx, src, "en")
		if !strings.Contains(got, "credits recharge anytime") || strings.Contains(got, "充值") {
			t.Fatalf("补翻生效却没采用新稿：%q", got)
		}
		if st.count() != 2 {
			t.Fatalf("补翻应当只多打一次上游（一次就收手），实际 %d 次", st.count())
		}
		// 第二次请求必须**点名残片**并禁改数字：不点名的补翻等于重新翻一遍，成本一样而成功率更低
		second := st.body(1)
		if !strings.Contains(second, "充值") || !strings.Contains(second, "不许改任何数字") {
			t.Fatalf("补翻提示词没点名残片或没禁改数字：\n%s", second)
		}
		// 采用的那一稿必须进缓存，键头指纹仍是「原文＋口径」
		cached := e.db.GetConfig("i18n:welcome:en", "")
		if !strings.Contains(cached, "credits recharge anytime") ||
			!strings.HasPrefix(cached, srcFingerprint(src+"\x00"+localizeContract("en", srcTopicsOf(src)))+"\n") {
			t.Fatalf("补翻后的译文没进缓存或指纹不对：%q", cached)
		}
	})

	t.Run("无改善保留上一稿：出栈闸把那份带残片的稿子整条挡下", func(t *testing.T) {
		st := newSeqStub(t,
			"Hello, this is LangCross\ncredits充值 anytime",
			"Hello, this is LangCross\n积分 recharge anytime")
		e := st.engine(t)
		// 补翻层"保留上一稿"的语义在 canned_guard_test.go 的 TestCannedRepairKeepsPreviousDraft 里
		// 直接钉（那两层各锁各的：这一层管"拿哪一稿"，出栈层管"那一稿能不能发给客户"）。
		// 端到端看到的必须是**中文原文**——096x 之前这里会原样把 `credits充值` 发给访客并落库，
		// 现网英文首屏那条读数就是这么常驻下来的。
		if got := e.LocalizeGreeting(ctx, src, "en"); got != src {
			t.Fatalf("补翻没救回来的残片稿被发给访客了：%q", got)
		}
		if st.count() != 2 {
			t.Fatalf("应当打过补翻那一枪（次数 %d）", st.count())
		}
		if cached := e.db.GetConfig("i18n:welcome:en", ""); cached != "" {
			t.Fatalf("被挡下的译文落了库（下次 greet 直接命中）：%q", firstLine(cached))
		}
	})

	t.Run("行数变了保留上一稿：同样不许发给客户也不许落库", func(t *testing.T) {
		st := newSeqStub(t,
			"Hello, this is LangCross\ncredits充值 anytime",
			"Hello, this is LangCross and credits recharge anytime")
		e := st.engine(t)
		// 新稿少了行数 ⇒ 补翻层不采用、保留上一稿（那份仍带残片），出栈层再把它挡下。
		// ⚠️ 这一档在 096x 之前是"两层都放行"：行数判据挡住了新稿，旧稿却照样出栈并缓存。
		if got := e.LocalizeGreeting(ctx, src, "en"); got != src {
			t.Fatalf("行数判据挡下补翻后，那份带残片的旧稿被直接送出去了：%q", got)
		}
		if cached := e.db.GetConfig("i18n:welcome:en", ""); cached != "" {
			t.Fatalf("同上，旧稿落了库：%q", firstLine(cached))
		}
	})

	t.Run("中文界面不判残也不补翻", func(t *testing.T) {
		st := newSeqStub(t, "你好，我是能言 AI 助手")
		e := st.engine(t)
		if got := e.LocalizeGreeting(ctx, src, "zh"); got != src {
			t.Fatalf("中文界面原样出中文即可，实际：%q", got)
		}
		if st.count() != 0 {
			t.Fatalf("中文界面一次都不该打上游，实际 %d 次", st.count())
		}
	})
}

// firstLine 取缓存值首行（指纹）；报错信息里只给这一行，别把整段译文糊进测试输出。
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
