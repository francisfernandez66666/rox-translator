// input_lang_test.go — ★ 082x 第九条（2026-09-29）的用户定稿口径断言：
// 「默认的打开词根据用户前台语言，但后续用户用什么语言，就回复什么语言。」
//
// 这条口径有两个方向，两个都要锁：
//   - 打开词（欢迎词 / chips）**只看界面语言**——访客还没开口，且这一句要缓存、要让运营在管理台改；
//   - 对话正文**看他这句话用的语言**，双向接管：中文界面里的英文提问答英文（用户实测报的那条缺陷），
//     英文界面里的中文提问答中文。
//
// 接管靠的是「一眼定性」的粗判，所以**判不出时的回落档**和**不该接管的正常输入**这两组负向对照，
// 比正向那几条更重要：正向漏一条只是少修一个语种，负向漏一条会把法文访客的正文补翻成英文、
// 或把中文访客的产品名提问当成换语种、当场把他的中文话术撤走（那是把修复做成回退）。
package engine

import (
	"context"
	"strings"
	"testing"

	"translator/internal/assist/llm"
)

// TestDetectInputLang 语种粗判的正负对照表。
// 每一行都写清"为什么在这"，改这张表时按行判能不能删——尤其是期望空串的那些。
func TestDetectInputLang(t *testing.T) {
	cases := []struct {
		in   string
		want string
		why  string
	}{
		// —— 正向：用户报的那条缺陷与同类形态 ——
		{"what kinds of features do you have?", "en", "现网实测原句：中文界面里的英文提问"},
		{"what is the 价格 like", "en", "零星汉字（<4）＋成句英文：那是他引用界面上的词，人说的是英语"},
		{"how much is 积分?", "en", "现网真实形态：术语引用不算中文输入（接管前永远修不掉）"},
		{"多少钱", "zh", "三个字以下的纯中文短句"},
		{"这个怎么收费", "zh", "中文短句"},
		{"帮我翻一下这段：The quick brown fox jumps over the lazy dog, and can you keep the layout?", "zh",
			"够四个汉字＝访客自己在写中文，后面那一大段英文是**材料**不是他的语言"},
		{"Word 和 Excel 都能翻吗", "zh", "中英混排的产品名提问：接管成 en 会把他的中文话术撤走"},
		{"こんにちは、いくらですか", "ja", "假名在场即定性（中文正文里永远不会出现假名）"},
		{"번역 가격은 얼마인가요", "ko", "谚文"},
		{"Сколько это стоит?", "ru", "西里尔"},
		{"كم السعر؟", "ar", "阿拉伯"},
		{"ราคาเท่าไหร่", "th", "泰文"},
		// —— 负向：判不出必须回空串（回落界面语言），绝不猜 ——
		{"Combien coûte la traduction d'un document?", "", "法文：只有一个英文同形词（document），不够两个"},
		{"Wie viel kostet die Übersetzung?", "", "德文：一个英文标记都没有"},
		{"¿Cuántos créditos necesito para un PDF?", "", "西文：拉丁字母但不是英语，判成 en 就是把对的译文改错"},
		{"PDF", "", "产品名/缩写：字母量不够门槛"},
		{"Word excel", "", "两个产品名，一个英文虚词都没有"},
		{"https://example.com/pricing", "", "一串链接：命中的英文标记不足两个"},
		{"ok", "", "单字符号级回应：不够字母量"},
		{"thanks", "", "单个英文词：标记数不到 2，中文界面里这可能只是随手一句"},
		{"", "", "空输入"},
		{"   \n ", "", "只有空白"},
		{"123 456", "", "纯数字：没有任何书写形态可判"},
	}
	for _, c := range cases {
		if got := detectInputLang(c.in); got != c.want {
			t.Errorf("detectInputLang(%q)=%q，期望 %q（%s）", c.in, got, c.want, c.why)
		}
	}
}

// TestEffectiveReplyLangTakesOverBothWays 接管函数：界面语言打底、输入语言双向接管、简繁跟界面。
func TestEffectiveReplyLangTakesOverBothWays(t *testing.T) {
	cases := []struct {
		ui, in, want, why string
	}{
		{"zh", "what is the 价格 like", "en", "用户实测那条：中文界面里的英文提问"},
		{"zh", "多少钱", "zh", "中文界面里的中文提问（主路径，不许被撤）"},
		{"zh", "Combien coûte la traduction?", "zh", "判不出语种 ⇒ 回落界面语言"},
		{"", "what is the price", "en", "老挂件不带 lang：英文输入照样接管"},
		{"", "多少钱", "zh", "老挂件 + 中文输入按中文放行"},
		{"en", "多少钱", "zh", "★ 反方向接管：英文界面里的中国访客拿中文答（用户 09-29 定稿）"},
		{"en", "what is the price", "en", "英文界面英文提问：语种本来就一致，不动"},
		{"ja", "こんにちは", "ja", "日文界面日文提问"},
		{"ja", "how do I upload this file", "en", "日文界面里的英文提问改答英文"},
		{"ru", "Сколько стоит", "ru", "俄文界面俄文提问"},
		{"ru", "多少钱", "zh", "俄文界面里的中文提问"},
		{"zh-Hant", "多少錢", "zh_hant", "简繁是书写档不是语种：繁体界面的访客继续拿繁体"},
		{"zh_hant", "what is the price", "en", "繁体界面里的英文提问仍按英文（术语与品牌跟着英文档走）"},
		{"EN", "多少钱", "zh", "语种码大写要先归一，否则同一访客两种写法拿到不同待遇"},
	}
	for _, c := range cases {
		if got := effectiveReplyLang(c.ui, c.in); got != c.want {
			t.Errorf("effectiveReplyLang(%q, %q)=%q，期望 %q（%s）", c.ui, c.in, got, c.want, c.why)
		}
	}
	// 接管不许把「界面语言本来就非中文」这条路反向判成可吃中文 canned：
	// visitorWantsChinese 吃的就是接管后的值，所以这行必须为假（英文界面 + 英文提问）。
	if visitorWantsChinese(effectiveReplyLang("en", "what is the price")) {
		t.Fatal("英文界面英文提问被判成可直出中文 canned")
	}
	// 而这一行必须为真（英文界面 + 中文提问）：反向接管后他就是中文访客。
	if !visitorWantsChinese(effectiveReplyLang("en", "多少钱")) {
		t.Fatal("反向接管没把中文提问的访客认回中文档")
	}
}

// TestWelcomeStaysOnUiLangWhileReplyFollowsInput 用户口径的两半必须各自成立：
// 「打开词按前台语言」＋「回复按输入语言」。
// 只锁其中一半的话，把两处合并成一个语种来源也照样绿——而合并会立刻坏掉另一半：
// 打开词跟着输入走，运营在管理台改的那句中文欢迎词就再也不会被读到（缓存键按界面语言落库）。
func TestWelcomeStaysOnUiLangWhileReplyFollowsInput(t *testing.T) {
	url, hits, bodies := scriptedLLM(t,
		llmCall{content: chineseReplySample},                                            // 第 1 次：模型没照语言段办，答了中文
		llmCall{content: "We bill by credits; the current rate shows in your console."}, // 第 2 次：出站补翻
	)
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	ctx := context.Background()

	// ① 中文界面：打开词原样中文，一次上游都不许多打
	welcome := "你好，我是能言 AI 助手"
	if got := e.LocalizeGreeting(ctx, welcome, "zh"); got != welcome {
		t.Fatalf("中文界面的打开词被按输入语种改口了：%q", got)
	}
	// ② 同一界面、同一台引擎，访客这句用英文问：正文必须变成英文
	newSession(t, e, "s-mix-1")
	rep := e.Respond(ctx, "s-mix-1", "what is the 价格 like", "/", "zh", nil)
	if !rep.LangLocalized || strings.Contains(rep.Content, "积分") {
		t.Fatalf("中文界面里的英文提问没被翻成英文（source=%q localized=%v content=%q）",
			rep.Source, rep.LangLocalized, rep.Content)
	}
	// ③ 补翻那一次的提示词必须按**英文档**教：品牌名 LangCross、术语 credits。
	//    抓真请求体而不是内存串——落库的口径写错了界面是看不出来的。
	last := bodies.at(1)
	if !strings.Contains(last, "「credits」") || !strings.Contains(last, "LangCross") {
		t.Fatalf("补翻提示词没按英文档钉术语/品牌（它还在吃界面语言 zh）：\n%s", last)
	}
	// ④ 反向：英文界面 + 中文提问 ⇒ 中文话术直出，且**不该**发生补翻
	//    （把它翻成英文就是用户明确否掉的那个行为）。
	newSession(t, e, "s-mix-2")
	before := hits.Load()
	back := e.Respond(ctx, "s-mix-2", "多少钱", "/", "en", nil)
	if back.Source != "rule" || back.LangLocalized {
		t.Fatalf("英文界面里的中文提问被改口成英文或走了模型：source=%q localized=%v content=%q",
			back.Source, back.LangLocalized, back.Content)
	}
	if hits.Load() != before {
		t.Fatalf("中文提问这一轮多打了 %d 次上游（反向接管后语言收口必须零开销）", hits.Load()-before)
	}
}

// TestTakeoverFeedsPromptForEnglishQuestionInChineseUI 用户报的缺陷在提示词侧的落点：
// 中文界面 + 英文提问那一轮，模型收到的【回复语言】段必须是英文档（不是「默认用中文写」）。
// 这条与 ② ③ 互补：② 锁正文、③ 锁补翻提示词，这里锁**对话生成**的提示词——
// 三个位置任一处漏改，现网就还是那句中英混排。
func TestTakeoverFeedsPromptForEnglishQuestionInChineseUI(t *testing.T) {
	url, _, bodies := scriptedLLM(t, llmCall{content: "We bill by credits."})
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	newSession(t, e, "s-prompt-zhui-enq")
	e.Respond(context.Background(), "s-prompt-zhui-enq", "what is the 价格 like", "/", "zh", nil)
	first := bodies.at(0)
	if !strings.Contains(first, "本轮作答语言：English（English）") {
		t.Fatalf("中文界面里的英文提问，对话提示词仍在教中文：\n%s", first)
	}
	if strings.Contains(first, "Simplified Chinese（简体中文）") {
		t.Fatalf("提示词里同时出现中文档作答语言（两档混教，模型就会混答）：\n%s", first)
	}
	// 品牌与术语跟着英文档：现网混排那句「翻译后版式基本还原…」正是中文素材没被要求译出去
	if !strings.Contains(first, "本轮一律写 LangCross") || !strings.Contains(first, "「credits」") {
		t.Fatalf("英文档品牌/术语口径没进对话提示词：\n%s", first)
	}
	// 对照：中文界面 + 中文提问必须教中文（别把接管写成"逢英文词就翻"）。
	// 这句刻意避开话术关键词（带「价格」会被毫秒级直配抢走，根本不发模型请求，对照腿就空转）。
	newSession(t, e, "s-prompt-zhui-zhq")
	e.Respond(context.Background(), "s-prompt-zhui-zhq", "你们支持哪些文件格式", "/", "zh", nil)
	if s := bodies.at(1); !strings.Contains(s, "本轮作答语言：Simplified Chinese（简体中文）") {
		t.Fatalf("中文提问那轮被改口成别的语种：\n%s", s)
	}
}

// TestDetectInputLangThresholdsAreLive 门槛常量与判定必须同源：
// 判据里那两个门槛（汉字 4 / 英文标记 2 / 拉丁字母 4）是这张表唯一的"尺度"，
// 把它俩写死在别处（或有人顺手调档）而不动对照，正负对照会一起失真。
// 这里用**临界样本**逐个钉住边界，改数字必红一半。
func TestDetectInputLangThresholdsAreLive(t *testing.T) {
	// 汉字档：3 个汉字配成句英文算英文，4 个汉字算中文
	if got := detectInputLang("积分呢 how do I start the trial plan"); got != "en" {
		t.Errorf("3 汉字＋成句英文应判 en，实际 %q", got)
	}
	if got := detectInputLang("这个积分多少 how do I start the trial plan"); got != "zh" {
		t.Errorf("5 汉字应判 zh（材料再长也是中文访客），实际 %q", got)
	}
	// 英文标记档：1 个标记不接管，2 个才接管
	if got := detectInputLang("hello world"); got != "" {
		t.Errorf("只有一个英文标记词时不该接管，实际 %q", got)
	}
	if got := detectInputLang("please translate it for me"); got != "en" {
		t.Errorf("两个以上英文标记词该判 en，实际 %q", got)
	}
	// 字母量档（防御性第二道：单字母／缩写级输入一律不接管）
	if got := detectInputLang("hi"); got != "" {
		t.Errorf("字母量不足（<%d）时不该接管，实际 %q", inputLangMinLatinLetters, got)
	}
	if inputLangMinLatinLetters < 4 || inputLangMinEnglishWords < 2 || inputLangMinChineseVoice < 4 {
		t.Fatalf("三个门槛被调到 %d/%d/%d——低于这些值就是把'随便一个英文词'当换语种证据，"+
			"中文访客贴产品名就会被撤掉中文话术（负向对照表会一起失真）",
			inputLangMinLatinLetters, inputLangMinEnglishWords, inputLangMinChineseVoice)
	}
	// countEnglishWords 只数**互不相同**的标记词：重复同一个词不该够门槛
	if n := countEnglishWords("what what what what"); n != 1 {
		t.Errorf("countEnglishWords 把重复词数成了 %d，应只数互不相同的标记", n)
	}
}
