// reply_lang_test.go — ★ 082x（2026-09-29，用户指令「不能根据用户的前台语言和使用语言来回复，
// 一律用中文」+「品牌英文名叫 LangCross，不叫 nengyan」）的复现断言。
//
// 三条腿，缺一条这个 bug 就能复活：
//  1. 【回复语言】段真进了 prompt，且位置在中文素材**之后**（否则模型照素材语言说）；
//  2. 中文话术/流程这些 canned 中文文案在非中文访客面前**让位**（这条是主根因——
//     只改提示词的话，撞关键词的中文话术根本不经过模型，照样甩中文）；
//  3. 欢迎词/chips 的按需翻译：翻得到就出对方语言，翻不到就原样出中文（绝不编译文），
//     且缓存命中不再打上游、人工改过的译文不被机翻覆盖。
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

// TestReplyLangBlockSitsAfterChineseMaterial 【回复语言】段进 prompt + 位置判据。
// 位置不是审美：persona/语气/承诺/现值/知识五段全是中文，语言口径排在它们后面，
// 才是模型开口前读到的最后一条指令。排前面＝被五段中文素材的语域带跑（现网那条
// 「英文提问回中文」就是这么来的）。
func TestReplyLangBlockSitsAfterChineseMaterial(t *testing.T) {
	e := newTestEngine(t)
	sys := e.buildSystemPrompt(context.Background(),
		[]entry{{key: "kb-epub", title: "格式", content: "支持 epub"}}, "en")
	for _, want := range []string{
		"【回复语言】",
		"访客界面语言：English（English）",
		"跟访客输入走",          // 第 2 条：输入语言优先于界面语言
		"本轮一律写 LangCross", // 第 3 条：品牌名分语言
		"Nengyan",         // 负向也得点名：拼音写法要被明确禁掉（出现在禁词列举里）
		"不许把中文原句直接贴出去",    // 素材是中文写的，要译过去再说
	} {
		if !strings.Contains(sys, want) {
			t.Fatalf("【回复语言】段缺 %q：\n%s", want, sys)
		}
	}
	// 次序腿：语言段必须排在知识段之后、「直接回复用户」之前
	iKnow, iLang, iGo := strings.Index(sys, "【相关知识】"), strings.Index(sys, "【回复语言】"), strings.Index(sys, "直接回复用户：")
	if !(iKnow < iLang && iLang < iGo) {
		t.Fatalf("【回复语言】没排在知识段之后、收尾语之前（知识=%d 语言=%d 收尾=%d）：\n%s", iKnow, iLang, iGo, sys)
	}
	// 中文界面：拿到的是中文档名，且不该出现「没拿到界面语言」那句兜底
	zh := e.buildSystemPrompt(context.Background(), nil, "zh")
	if !strings.Contains(zh, "Simplified Chinese（简体中文）") || strings.Contains(zh, "没拿到访客的界面语言") {
		t.Fatalf("中文界面的语言段不对：\n%s", zh)
	}
	// 繁体是另一个书写档，不许被当成简体中文
	hant := e.buildSystemPrompt(context.Background(), nil, "zh_hant")
	if !strings.Contains(hant, "Traditional Chinese（繁體中文）") {
		t.Fatalf("繁体界面没落到繁体档：\n%s", hant)
	}
	// 空语言（老缓存包/082x 之前的前端根本不带这个字段）：明说「按访客输入判断」，不猜
	empty := e.buildSystemPrompt(context.Background(), nil, "")
	if !strings.Contains(empty, "没拿到访客的界面语言") {
		t.Fatalf("空界面语言没走「按输入判断」分支：\n%s", empty)
	}
}

// TestLangLabelsCoverFrontendLocales 界面语言码表与前端 12 语种口径交叉锁。
// 前端加一语种而这里漏加，后果是那个语种的访客拿到「未知语言」分支（canned 中文照送），
// 而且**不会有任何红灯**——所以按码逐个点名。
func TestLangLabelsCoverFrontendLocales(t *testing.T) {
	for _, c := range []string{"zh", "en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th", "zh_hant"} {
		if langLabel(c) == "" {
			t.Errorf("界面语言 %q 在 langLabels 里没有映射（该语种访客会掉进未知语言分支）", c)
		}
	}
	if langLabel("xx") != "" || langLabel("") != "" {
		t.Error("未知/空语言必须回空串（由调用方走「按输入判断」分支），不许猜一个语种")
	}
}

// TestCannedChineseYieldsToNonChineseVisitor 中文话术/流程在非中文访客面前让位。
// 这条才是主根因：话术直配与流程**不经过模型**，只补提示词等于没补。
func TestCannedChineseYieldsToNonChineseVisitor(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()

	// ① 话术直配：夹具里 sc-price 的关键词是「多少钱,价格」
	newSession(t, e, "s-zh-sc")
	if rep := e.Respond(ctx, "s-zh-sc", "多少钱", "/", "zh", nil); rep.Source != "rule" {
		t.Fatalf("中文界面应走话术直配（毫秒级），实际 source=%q", rep.Source)
	}
	newSession(t, e, "s-en-sc")
	rep := e.Respond(ctx, "s-en-sc", "多少钱", "/", "en", nil)
	if rep.Source == "rule" {
		t.Fatalf("英文界面仍把中文话术原样送出（content=%q）——让位没生效", rep.Content)
	}
	// ② 流程：夹具里 fl-onboard 的触发词是「新手,上手」，两步 ask 都是中文
	newSession(t, e, "s-zh-fl")
	if rep := e.Respond(ctx, "s-zh-fl", "我是新手", "/", "zh", nil); rep.Source != "flow" {
		t.Fatalf("中文界面应进流程，实际 source=%q", rep.Source)
	}
	newSession(t, e, "s-en-fl")
	if rep := e.Respond(ctx, "s-en-fl", "我是新手", "/", "en", nil); rep.Source == "flow" {
		t.Fatal("英文界面进了中文流程（会连着甩三步中文 ask）")
	}
	// ③ 空语言按中文放行：082x 之前的挂件根本不带 lang，
	//    把它判成非中文会让全站话术在升级瞬间集体失效（那是行为回退，不是修复）
	newSession(t, e, "s-no-lang")
	if rep := e.Respond(ctx, "s-no-lang", "多少钱", "/", "", nil); rep.Source != "rule" {
		t.Fatalf("空 lang 应仍走话术直配，实际 source=%q", rep.Source)
	}
	// ④ 让位之后不许把访客晾着：无 LLM 时兜底仍要给出知识素材（中文），
	//    并**不是**返回空回复（空回复比中文回复更接近事故）
	later := e.Respond(ctx, "s-en-sc2", "积分价格", "/", "en", nil)
	if strings.TrimSpace(later.Content) == "" {
		t.Fatal("英文界面让位后拿到空回复")
	}
}

// stubLLM 起一个假上游，返回固定正文并计数被调用次数。
func stubLLM(t *testing.T, content string) (url string, hits *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.ReadAll(r.Body) // 不读完会让上游连接复用出诡异行为
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` +
			jsonString(content) + `},"finish_reason":"stop"}],"usage":{"completion_tokens":10}}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

// TestGreetingAndChipsLocalizedForNonChineseVisitor 开场白/chips 的按需翻译三态：
// 翻得到 → 出对方语言并进缓存；缓存命中 → 不再打上游；翻不到（无 LLM）→ 原样中文。
func TestGreetingAndChipsLocalizedForNonChineseVisitor(t *testing.T) {
	url, hits := stubLLM(t, "Hi, what can I translate for you?")
	e := newTestEngine(t)
	_ = e.db.SetConfig("welcome", "你好，我是能言 AI 助手")
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	ctx := context.Background()

	// ① 中文界面：一次都不该打上游（这是 greet 的关键路径，白付一次 LLM 往返）
	before := hits.Load()
	if got := e.LocalizeGreeting(ctx, "你好，我是能言 AI 助手", "zh"); got != "你好，我是能言 AI 助手" {
		t.Fatalf("中文界面被翻了：\n%s", got)
	}
	if hits.Load() != before {
		t.Fatalf("中文界面打了上游 LLM（%d→%d）", before, hits.Load())
	}
	// ② 英文界面：出译文
	if got := e.LocalizeGreeting(ctx, "你好，我是能言 AI 助手", "en"); !strings.Contains(got, "Hi") {
		t.Fatalf("英文界面没拿到译文：\n%s", got)
	}
	// ③ 缓存命中：同原文同语种再翻一次不许再打上游
	before = hits.Load()
	if got := e.LocalizeGreeting(ctx, "你好，我是能言 AI 助手", "en"); !strings.Contains(got, "Hi") {
		t.Fatalf("缓存没命中：\n%s", got)
	}
	if hits.Load() != before {
		t.Fatalf("缓存没命中，又打了一次上游（%d→%d）", before, hits.Load())
	}
	// ④ 运营改了原文 → 指纹变了，必须重翻（旧译文留着就是对外错报）
	_ = e.db.SetConfig("welcome", "你好，我是能言 AI 助手（改版）")
	before = hits.Load()
	if got := e.LocalizeGreeting(ctx, "你好，我是能言 AI 助手（改版）", "en"); !strings.Contains(got, "Hi") {
		t.Fatalf("原文变更后没重翻：\n%s", got)
	}
	if hits.Load() == before {
		t.Fatal("原文变了却没重新翻译（缓存按源文指纹失效这条没生效）")
	}
	// ⑤ 人工改过的译文永久保留：指纹后带 !manual 即放行，机翻不许吃回去
	_ = e.db.SetConfig("i18n:welcome:en", "deadbeefcafe!manual\nHuman written English welcome")
	if got := e.LocalizeGreeting(ctx, "你好，我是能言 AI 助手（改版）", "en"); got != "Human written English welcome" {
		t.Fatalf("人工译文被机翻覆盖/忽略：\n%s", got)
	}
	// ⑥ 无 LLM（规则模式）：原样出中文，绝不编一份译文
	e2 := newTestEngine(t)
	_ = e2.db.SetConfig("welcome", "你好，我是能言 AI 助手")
	if got := e2.LocalizeGreeting(ctx, "你好，我是能言 AI 助手", "en"); got != "你好，我是能言 AI 助手" {
		t.Fatalf("LLM 不可用时不该编译文，实际：\n%s", got)
	}
}

// TestLocalizeChipsKeepsOrderOrGivesUp chips 翻译的「条数对不上就整串放弃」判据。
// 模型把两行并一行、或多送一行都很常见；按位置对齐着送出去会让 chip 文案串案
// （点「怎么充值」结果发出去的是「支持哪些语言」）。
func TestLocalizeChipsKeepsOrderOrGivesUp(t *testing.T) {
	ctx := context.Background()
	// 夹具里 quick_chips 未设，这里显式给三条
	csv := "怎么上传文件,积分怎么收费,支持哪些语言"

	t.Run("条数一致按序拆回", func(t *testing.T) {
		url, _ := stubLLM(t, "How do I upload a file?\nHow is billing counted?\nWhich languages?")
		e := newTestEngine(t)
		e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
		got := e.LocalizeChips(ctx, csv, "en")
		lines := strings.Split(got, ",")
		if len(lines) != 3 || lines[0] != "How do I upload a file?" || lines[2] != "Which languages?" {
			t.Fatalf("chips 没按原顺序拆回：%q", got)
		}
	})
	t.Run("条数不符整串放弃", func(t *testing.T) {
		url, _ := stubLLM(t, "Only one line, merged together")
		e := newTestEngine(t)
		e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
		if got := e.LocalizeChips(ctx, csv, "en"); got != csv {
			t.Fatalf("条数不符时应原样返回中文串，实际：%q", got)
		}
	})
	t.Run("中文界面不打上游", func(t *testing.T) {
		url, hits := stubLLM(t, "whatever")
		e := newTestEngine(t)
		e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
		before := hits.Load()
		if got := e.LocalizeChips(ctx, csv, "zh"); got != csv {
			t.Fatalf("中文界面 chips 被翻了：%q", got)
		}
		if hits.Load() != before {
			t.Fatal("中文界面打了上游 LLM")
		}
	})
}

// TestBrandNameFollowsLocaleMatrix 品牌名分语种口径（082x 用户指令第一条）。
//
// 为什么单独锁一张表而不是只锁提示词文案：挂件标题（前端 chat.assistTitle / app.title）和
// 模型自称是两条独立链路，历史上就是因为没人把它们钉在一起，才会长出「同一界面两个品牌名」。
// 现在这张表的跨端等值由 frontend-react/src/i18n/brandName.test.ts 直接读这里的源码来锁
// ——改 brandKanjiLocales 不同步改 locales，那侧就红灯。
func TestBrandNameFollowsLocaleMatrix(t *testing.T) {
	kanji := map[string]bool{"zh": true, "zh_hant": true, "zh-Hant": true, "ja": true}
	for c := range kanji {
		if got := brandNameFor(c); got != "能言" {
			t.Errorf("brandNameFor(%q)=%q，该语种界面标题用的是汉字名，模型不许改口成别家", c, got)
		}
	}
	for _, c := range []string{"en", "ru", "fr", "ar", "es", "pt", "de", "ko", "th", "EN", ""} {
		if got := brandNameFor(c); got != "LangCross" {
			t.Errorf("brandNameFor(%q)=%q，非汉字语种一律 LangCross", c, got)
		}
	}
	// 大小写/连字符形态不许改变判定：'EN' 当成未知品牌档、'zh-Hant' 当成英文档，
	// 都会让同一访客在两次请求之间被换个名字称呼（缓存键归一同理，见下）。
	if brandNameFor("EN") != "LangCross" || brandNameFor(" zh_hant ") != "能言" {
		t.Error("界面语言码未归一就参与品牌判定")
	}
	// 模型侧：日文界面给汉字名口径，英文界面给 LangCross＋拼音禁令。
	e := newTestEngine(t)
	ja := e.buildSystemPrompt(context.Background(), nil, "ja")
	if !strings.Contains(ja, "本轮用中文写法「能言」") || strings.Contains(ja, "本轮一律写 LangCross") {
		t.Fatalf("日文界面的品牌口径不对（该语种标题是汉字名）：\n%s", ja)
	}
	en := e.buildSystemPrompt(context.Background(), nil, "en")
	if !strings.Contains(en, "本轮一律写 LangCross") || !strings.Contains(en, "Nengyan") {
		t.Fatalf("英文界面缺 LangCross 口径或拼音禁令：\n%s", en)
	}
	// 翻译提示词复用同一张表：欢迎词翻成日文时必须写「能言」，不许另一条链路自己编规则。
	// 判据抓的是**真发出去的提示词**（stub 里记下请求体），不是缓存里残存的译文——
	// 后者只能证明"翻过一次"，证明不了"按哪条规则翻"。
	prompt := captureLocalizePrompt(t, "ja")
	if !strings.Contains(prompt, "品牌名一律写作「能言」") {
		t.Fatalf("日文欢迎词的翻译提示词没把品牌名钉成「能言」（该语种界面标题就是汉字名）：\n%s", prompt)
	}
	if strings.Contains(prompt, "品牌名一律写作「LangCross」") {
		t.Fatalf("日文界面被钉成 LangCross，和该语种挂件标题打架：\n%s", prompt)
	}
	enPrompt := captureLocalizePrompt(t, "en")
	if !strings.Contains(enPrompt, "品牌名一律写作「LangCross」") || !strings.Contains(enPrompt, "Nengyan") {
		t.Fatalf("英文欢迎词的翻译提示词缺 LangCross 口径或拼音禁令：\n%s", enPrompt)
	}
}

// TestPointsTermFollowsFrontendDict ★ 082x 增补（2026-09-29 换件后现网复问抓到）：
// 计费单位「积分」的跨语种写法必须跟**官网界面**同源。现网读数：英文访客问价格，
// 补翻把「积分」写成 "integral"（"7.5 integral fee" / "400 integral per 1,000 characters"），
// 而该语种界面上写的是 credits ——客户拿它对账的一句话出现两个名字＝对外错报
// （与 F-12「报价三口径打架」同族，只是这次分叉发生在语言之间）。
//
// 判据三条：① 表内每个语种都在【回复语言】段与翻译提示词里出现（**抓真发出去的请求体**，
// 不抓内存里的串）；② 中文系不追加（素材本来就是中文）；③ 表里没有的语种**宁可不提**，
// 也绝不现场编一个词——编出来的词一定跟界面对不上，这一条是负向锁，缺了它第①条形同虚设。
// 词表本身与前端的等值由 frontend-react/src/i18n/pointsTerm.test.ts 直接读本文件源码核对。
func TestPointsTermFollowsFrontendDict(t *testing.T) {
	// ① 表内语种逐个：口径那句必须带上该语种界面里的那个词
	want := map[string]string{
		"en": "credits", "zh_hant": "積分", "ja": "ポイント", "ko": "포인트", "de": "Punkte",
		"fr": "points", "ru": "кредитов", "es": "créditos", "pt": "pontos", "ar": "نقطة", "th": "คะแนน",
	}
	for c, term := range want {
		line := pointsTermLine(c)
		if !strings.Contains(line, "计费单位") || !strings.Contains(line, "「"+term+"」") {
			t.Errorf("pointsTermLine(%q) 没把计费单位钉成界面那个词：%q", c, line)
		}
		// 大小写／连字符形态不许改变判定（同一访客两次请求被教两个词）
		if pointsTermLine(strings.ToUpper(c)) != line {
			t.Errorf("语种码 %q 大写写法与规范写法拿到不同口径", c)
		}
		if c == "zh_hant" && pointsTermLine("zh-Hant") != line {
			t.Errorf("zh-Hant（前端文件名口径）没归一到 zh_hant")
		}
		// 模型侧与翻译侧必须同源：两条句式不同，但词面只能有一个
		prose := pointsTranslationLine(c)
		if !strings.Contains(prose, "「"+term+"」") {
			t.Errorf("pointsTranslationLine(%q) 词面与 pointsTermLine 分叉：%q", c, prose)
		}
		// 规范形态在两条 prompt 里都得真的落地（不是只存在于表里）
		e := newTestEngine(t)
		sys := e.buildSystemPrompt(context.Background(), nil, c)
		if !strings.Contains(sys, "「"+term+"」") {
			t.Errorf("%s 界面的系统提示词没有计费单位口径", c)
		}
	}
	// ② 中文系不追加：素材本来就是「积分」，多一句只会把模型带偏
	for _, c := range []string{"zh", "zh_CN", "  "} {
		if pointsTermLine(c) != "" || pointsTranslationLine(c) != "" {
			t.Errorf("%q 是中文系／空语种，不该追加计费单位口径：%q", c, pointsTermLine(c))
		}
	}
	// ③ 表漏档的语种宁可不提（现场编词必与界面对不上）
	if pointsTermLine("vi") != "" || pointsTranslationLine("vi") != "" {
		t.Error("表里没有的语种被现场编了个计费单位词")
	}
	// ④ 抓真请求体：英文/日文欢迎词的翻译提示词里必须出现该语种界面那个词＋反面禁令
	enPrompt := captureLocalizePrompt(t, "en")
	if !strings.Contains(enPrompt, "「credits」") {
		t.Fatalf("英文欢迎词的翻译提示词没有计费单位口径：\n%s", enPrompt)
	}
	if !strings.Contains(enPrompt, "integral") {
		t.Fatalf("英文提示词没点名禁令 integral（现网就是翻成这个词漏出去的）：\n%s", enPrompt)
	}
	jaPrompt := captureLocalizePrompt(t, "ja")
	if !strings.Contains(jaPrompt, "「ポイント」") {
		t.Fatalf("日文欢迎词的翻译提示词没把计费单位钉成「ポイント」：\n%s", jaPrompt)
	}
}

// captureLocalizePrompt 起一个记下请求体的假上游，翻一次欢迎词，回吐模型真收到的提示词原文。
// 每个语种各起一个新引擎：翻译缓存按语种落库，复用同一个引擎会让第二个语种命中缓存而根本不发请求。
func captureLocalizePrompt(t *testing.T, lang string) string {
	t.Helper()
	var mu sync.Mutex
	var last string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		last = string(b)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(srv.Close)
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "m"}}, 5)
	e.LocalizeGreeting(context.Background(), "你好，我是能言 AI 助手", lang)
	mu.Lock()
	defer mu.Unlock()
	if last == "" {
		t.Fatalf("%s 界面根本没有发起翻译请求（提示词无法验证）", lang)
	}
	return last
}

// TestLocalizeCacheKeyIsNormalized 缓存键归一：'EN' 与 'en' 必须命中同一条缓存。
// 不归一的后果不是浪费一次调用这么简单——库里会同语种留两份译文，
// 运营手工改过的那份（带 !manual）可能被另一条键绕过，变成「改了没生效」。
func TestLocalizeCacheKeyIsNormalized(t *testing.T) {
	url, hits := stubLLM(t, "Hello from LangCross")
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	text := "你好，我是能言 AI 助手"
	ctx := context.Background()
	before := hits.Load()
	if got := e.LocalizeGreeting(ctx, text, "EN"); !strings.Contains(got, "Hello") {
		t.Fatalf("'EN' 没翻成：\n%s", got)
	}
	if got := e.LocalizeGreeting(ctx, text, "en"); !strings.Contains(got, "Hello") {
		t.Fatalf("'en' 没命中缓存：\n%s", got)
	}
	if hits.Load() != before+1 {
		t.Fatalf("同一语种的大小写两种写法打了 %d 次上游，应为 1 次（缓存键没归一）", hits.Load()-before)
	}
	if e.db.GetConfig("i18n:welcome:en", "") == "" {
		t.Fatal("缓存没落在归一键 i18n:welcome:en 上")
	}
}

// jsonString 把 Go 串转成 JSON 字符串字面量（测试桩里拼响应体用，避免手写转义出错）。
func jsonString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
