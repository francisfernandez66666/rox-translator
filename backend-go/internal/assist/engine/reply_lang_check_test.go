// ============ reply_lang_check_test.go · 这批断言在锁什么 ============
// ★ 082x 增补批（2026-09-29）：换件后现网复问当场抓到的三条残留，每条一个正判据 + 一个反证。
//
// 为什么必须配反证：这一批的判据全是「不该翻的别翻、不该剥的别剥」型的软判断，
// 只写正向用例的话，把 enforceReplyLang 改成「非中文界面一律重翻一遍」也能全绿，
// 而那正是最贵又最容易把合格答案翻坏的形态（一次对话多付一整轮 LLM 往返）。
// 所以每个语种判据都带一条「合格回答必须零额外调用、原文一字不动」的对照腿。
//
// 三条腿分别对应：
//  1. replyLangMismatch 的语种判据（含日文汉字合法、引用中文文件名这两类必须放行的形态）；
//  2. Respond 咽喉上的补翻：LLM 那一路与**兜底那一路**都要过（兜底拼中文知识原文、根本不经过模型，
//     只在 llmReplyWith 里补翻就会漏——这里专门锁一条 source=fallback 被翻掉的用例）；
//  3. sanitizeVisitorText 的内部规则回声 / U+FFFD / 空行，外加一条 UTF-8 完整性反证
//     （字节下标算错会把全角括号前的中文切成乱码，那正是本批要修的症状之一，不能自己再造一遍）。
//
// =============================================
package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"translator/internal/assist/llm"
)

// 现网 2026-09-29 实测那条「lang=en 却回中文」的正文（86 汉字），这里压成同形态的一段中文做样本。
const chineseReplySample = "我们按积分计费，不同任务的扣费口径写在系统配置里，注册后可以在控制台看到当前档位的单价与赠送额度。" +
	"长文档会按页与字数折算，翻译完成后按实际消耗扣减，失败的部分不计费。需要体验可以直接注册试用。"

// llmCall 假上游的一次预设应答：code 非 0 时按该状态码回（用来造「对话调用失败、补翻调用成功」这种错位）；
// truncated 为真时 finish_reason 回 length（造 max_tokens 截断）。
type llmCall struct {
	code      int
	content   string
	truncated bool
}

// bodyLog 线程安全的请求体台账（假上游 handler 在另一个 goroutine 里写）。
type bodyLog struct {
	mu     sync.Mutex
	bodies []string
}

// add 追加记录一次请求体（假上游 handler 在独立 goroutine 里回调，故加锁）。
func (b *bodyLog) add(s string) {
	b.mu.Lock()
	b.bodies = append(b.bodies, s)
	b.mu.Unlock()
}

// at 取第 i 次请求体（越界回空串，让断言自然报"没有这次调用"而不是 panic）。
func (b *bodyLog) at(i int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= len(b.bodies) {
		return ""
	}
	return b.bodies[i]
}

// scriptedLLM 按调用次序回吐预设应答，序列用尽后一直回最后一条。
// 三个返回值分别是：上游地址、调用计数、每次请求体——
// 计数是本批最重要的证据（一次对话该打几次上游＝有没有把合格答案拖去重翻），
// 请求体用来核**发出去的 max_tokens 与提示词**（额度也是口径，不能只看函数名字）。
func scriptedLLM(t *testing.T, calls ...llmCall) (url string, hits *atomic.Int64, bodies *bodyLog) {
	t.Helper()
	var n atomic.Int64
	bl := &bodyLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		b, _ := io.ReadAll(r.Body)
		bl.add(string(b))
		if i >= len(calls) {
			i = len(calls) - 1
		}
		c := calls[i]
		if c.code != 0 {
			w.WriteHeader(c.code)
			_, _ = w.Write([]byte(`{"error":"upstream boom"}`))
			return
		}
		fr := "stop"
		if c.truncated {
			fr = "length"
		}
		body, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"content": c.content},
				"finish_reason": fr,
			}},
			"usage": map[string]any{"completion_tokens": 20},
		})
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n, bl
}

// TestReplyLangMismatchFiresOnlyOnWrongLanguageReply 语种判据矩阵（正例 + 四类必须放行的形态）。
//
// 这个函数是「一次短翻译调用」和「每轮对话都多付一次调用」之间的唯一分界，
// 所以判据要窄：窄到只会抓住「整段中文送给非中文访客」，不会碰任何一条合格回答。
func TestReplyLangMismatchFiresOnlyOnWrongLanguageReply(t *testing.T) {
	cases := []struct {
		name, lang, text string
		want             bool
	}{
		{"英文界面收到整段中文", "en", chineseReplySample, true},
		{"英文界面收到英文", "en", "We bill by credits; check the console for the current rate.", false},
		// 反证①：英文回答里引用一个中文文件名——这是最容易被误伤的正常形态
		{"英文界面里引用中文文件名", "en", "I translated 技术服务合同.docx into English for you.", false},
		{"俄文界面收到整段中文", "ru", chineseReplySample, true},
		{"韩文界面收到整段中文", "ko", chineseReplySample, true},
		{"泰文界面收到整段中文", "th", chineseReplySample, true},
		// 反证②：日文正文里出现汉字完全合法（汉字词＋假名语法），只有零假名才是「整段中文」
		{"日文界面收到正常日文", "ja", "長文ドキュメントにも対応しています。用語集で専門用語を固定できます。", false},
		{"日文界面收到纯中文", "ja", chineseReplySample, true},
		// 中文系／未送语言：一律放行（空语种时作答语言由访客输入决定，服务端没有可靠判据）
		{"中文界面收到中文", "zh", chineseReplySample, false},
		{"繁体界面收到中文", "zh_hant", chineseReplySample, false},
		{"没送界面语言", "", chineseReplySample, false},
		{"大小写与连字符形态不改变判定", "ZH-HANT", chineseReplySample, false},
		{"空正文不翻", "en", "   \n ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := replyLangMismatch(c.lang, c.text); got != c.want {
				t.Fatalf("replyLangMismatch(%q)=%v，期望 %v", c.lang, got, c.want)
			}
		})
	}
	// 反证③：判据必须真的在看**占比**，而不是「汉字一多就翻」。
	// 一段长英文回答里整段引用中文合同原文（85 汉字 / 占比一成出头）是**合格**回答——
	// 现网真有访客贴中文片段问英文，把它拖去重翻既白付一次调用，又把引文翻没了。
	diluted := chineseReplySample + strings.Repeat("The quick brown fox jumps over the lazy dog. ", 18)
	if replyLangMismatch("en", diluted) {
		h, _, tot := countScripts(diluted)
		t.Fatalf("汉字占比 %d/%d 的英文长回答被误判（引文不该被重翻）", h, tot)
	}
	if countHan(chineseReplySample) < 25 {
		t.Fatal("样本汉字太少，反证③不成立（它必须能证伪「按绝对量判」的写法）")
	}
}

// TestRespondTranslatesChineseReplyForEnglishVisitor 语言保证（LLM 那一路）：
// 模型用中文答了英文访客 ⇒ 出站补翻一次，正文变英文并打上 lang_localized 标记。
func TestRespondTranslatesChineseReplyForEnglishVisitor(t *testing.T) {
	url, hits, _ := scriptedLLM(t,
		llmCall{content: chineseReplySample},                                            // 第 1 次：对话生成（模型没照语言段办）
		llmCall{content: "We bill by credits. Check the console for the current rate."}, // 第 2 次：补翻
	)
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	newSession(t, e, "s-lang-en")
	rep := e.Respond(context.Background(), "s-lang-en", "what is the price", "/", "en", nil)
	if rep.Source != "llm" {
		t.Fatalf("source=%q，本例要的是 LLM 那一路", rep.Source)
	}
	if strings.ContainsAny(rep.Content, "的按翻译") {
		t.Fatalf("英文访客拿到中文正文（补翻没生效）：%q", rep.Content)
	}
	if !rep.LangLocalized {
		t.Fatal("补翻成功却没打 lang_localized 标记：运营看会话明细时分不清「模型答对了」和「我们翻回来的」")
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("上游调用 %d 次，应为 2（1 次对话 + 1 次补翻）", n)
	}
}

// TestCompliantReplyPaysExactlyOneCall 反证腿：模型已经用英文答了，就一次都不许多打。
// 这条是整个判据的成本闸门——没有它，把 enforceReplyLang 写成「非中文界面一律重翻」也能让上面那条绿。
func TestCompliantReplyPaysExactlyOneCall(t *testing.T) {
	url, hits, _ := scriptedLLM(t, llmCall{content: "We bill by credits, and the current rate is in your console."})
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	newSession(t, e, "s-lang-ok")
	before := hits.Load()
	rep := e.Respond(context.Background(), "s-lang-ok", "what is the price", "/", "en", nil)
	if rep.Content != "We bill by credits, and the current rate is in your console." {
		t.Fatalf("合格回答被改写了：%q", rep.Content)
	}
	if rep.LangLocalized {
		t.Fatal("没翻却打了 lang_localized")
	}
	if hits.Load()-before != 1 {
		t.Fatalf("上游调用 %d 次，合格回答只该 1 次", hits.Load()-before)
	}
	// 中文界面同一台引擎、同样的中文正文：语言收口一次上游都不许多打
	//（走的是话术直配那条毫秒级路径，收口判据必须让它原样过去）
	e2 := newTestEngine(t)
	e2.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	newSession(t, e2, "s-lang-zh")
	b2 := hits.Load()
	rep2 := e2.Respond(context.Background(), "s-lang-zh", "多少钱", "/", "zh", nil)
	if rep2.LangLocalized {
		t.Fatal("中文界面被补翻")
	}
	if !strings.Contains(rep2.Content, "积分") {
		t.Fatalf("中文界面的中文文案被改掉了：%q", rep2.Content)
	}
	if hits.Load()-b2 != 0 {
		t.Fatalf("中文界面多打了 %d 次上游，语言收口在该语种下必须零开销", hits.Load()-b2)
	}
}

// TestFallbackChineseKnowledgeAlsoGetsLocalized 兜底那一路也必须过咽喉。
// 场景是现网真实形态：对话调用 502 → fallbackReply 把**中文知识原文**拼出去（根本不经过模型），
// 补翻那次调用却是好的 ⇒ 访客仍应拿到英文气泡。只在 llmReplyWith 里补翻的话，这条必红。
func TestFallbackChineseKnowledgeAlsoGetsLocalized(t *testing.T) {
	url, hits, _ := scriptedLLM(t,
		llmCall{code: 502}, // 第 1 次：对话生成失败 → 走兜底
		llmCall{content: "Credits are billed per task; the current rate shows in your console."}, // 第 2 次：补翻成功
	)
	e := newTestEngine(t)
	// 夹具自带的知识条目只有四个字（"积分计费"），够不到判据的汉字量下限——
	// 补一条与现网同量级的中文素材，这条锁的才是"整段中文兜底被翻出去"而不是"够不够长"。
	// ★ 082x 第九条：keywords 里带上 price，因为访客这句**必须用英文问**。
	// 用「积分价格」＋en 界面已经测不到本意了——中文提问现在会被判成中文访客（中文直出才对），
	// 这条锁的场景是「英语访客 + 兜底中文素材」，两件事都得真成立才行。
	_, _ = e.db.Create("kb_entries", map[string]any{
		"key": "kb-long-billing", "category": "billing", "title": "计费说明", "priority": 20, "enabled": 1,
		"content": chineseReplySample, "keywords": "积分,价格,计费,price", "link_keys": "pricing",
	})
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	newSession(t, e, "s-lang-fb")
	rep := e.Respond(context.Background(), "s-lang-fb", "what is the price", "/", "en", nil)
	if rep.Source == "llm" {
		t.Fatal("上游第一路就失败了，不该拿到 source=llm")
	}
	if !rep.LangLocalized || strings.Contains(rep.Content, "积分") {
		t.Fatalf("兜底中文素材没被翻出去（source=%q content=%q）", rep.Source, rep.Content)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("上游调用 %d 次，应为 2（1 次对话失败 + 1 次补翻）", n)
	}
}

// TestTranslationFailureKeepsChineseOriginal 补翻也失败时**原样出中文**，不许编译文、不许报错。
// 访客已经拿到正确答案，只是语言不对；把整条回复打成「联系不上助手」比看到中文更糟（同 localize.go 口径）。
func TestTranslationFailureKeepsChineseOriginal(t *testing.T) {
	e := newTestEngine(t) // 无 LLM：ensureLLM 拿不到可用 provider
	ctx := context.Background()
	rep := &Reply{Content: chineseReplySample, Source: "fallback"}
	got := e.enforceReplyLang(ctx, rep, "en")
	if got.Content != chineseReplySample {
		t.Fatalf("补翻失败却改了正文：%q", got.Content)
	}
	if got.LangLocalized {
		t.Fatal("没翻成却打了 lang_localized（运营会以为语言问题已修）")
	}
	// 语言合格时连引擎都不该碰（nil 安全 + 零开销）
	if ok := e.enforceReplyLang(ctx, nil, "en"); ok != nil {
		t.Fatal("nil 回复不该被造出一个对象")
	}
	english := &Reply{Content: "English only."}
	if got := e.enforceReplyLang(ctx, english, "en"); got != english {
		t.Fatal("合格回复被换掉了")
	}
}

// TestSanitizeVisitorTextStripsOnlyInternalEcho 内部规则回声的收口：命中内部段名的括号才删，
// 正常补充说明、未闭合括号、以及正文本体一律留下。
func TestSanitizeVisitorTextStripsOnlyInternalEcho(t *testing.T) {
	// 现网 2026-09-29 中文轮实测漏出来的那句备注（同批截图），尾部还挂着空行
	echo := "翻译按积分计费，具体以注册页公示为准（不报具体价格数字，但必须强调系统现值里没有写的档位）。\n\n\n\n"
	got := sanitizeVisitorText(echo)
	if strings.Contains(got, "系统现值") || strings.Contains(got, "不报具体价格数字") {
		t.Fatalf("内部规则回声没剥净：%q", got)
	}
	if !strings.Contains(got, "翻译按积分计费，具体以注册页公示为准") {
		t.Fatalf("把正事一起剥掉了：%q", got)
	}
	if strings.Contains(got, "\n\n\n") || strings.HasSuffix(got, "\n") {
		t.Fatalf("尾部空行没收掉：%q", got)
	}

	t.Run("正常括号必须留下", func(t *testing.T) {
		s := "价格详见套餐说明（具体以注册页公示为准），40MB 以内单文件（含 PDF）"
		if got := sanitizeVisitorText(s); got != s {
			t.Fatalf("合格文案被改了：%q → %q", s, got)
		}
	})
	t.Run("空括号只剩标点残渣", func(t *testing.T) {
		if got := sanitizeVisitorText("支持 PDF 原格式输出（）。"); strings.Contains(got, "（）") || strings.Contains(got, "（）。") {
			t.Fatalf("空括号没清：%q", got)
		}
	})
	t.Run("未闭合括号原样留着", func(t *testing.T) {
		s := "这段被 max_tokens 截断了（系统现值"
		if got := sanitizeVisitorText(s); got != s {
			t.Fatalf("未闭合括号不许动（截断有自己的 WARN 与治理口径）：%q", got)
		}
	})
	t.Run("U+FFFD 残渣清掉且中文完好", func(t *testing.T) {
		got := sanitizeVisitorText("能言\uFFFD支持 PDF\uFFFD 原格式")
		if strings.ContainsRune(got, utf8.RuneError) {
			t.Fatalf("替换符没清：%q", got)
		}
		if !utf8.ValidString(got) || got != "能言支持 PDF 原格式" {
			t.Fatalf("清洗把正文切坏了：%q", got)
		}
	})
	// ★ 字节/rune 边界反证：括号前全是中文＋全角标点（每个 3 字节）。
	// 曾经这里按字节算括号宽度，切完是非法 UTF-8 —— 也就是本文件要修的第三条症状本身。
	t.Run("全角括号前的中文长度不许影响切点", func(t *testing.T) {
		for _, prefixLen := range []int{1, 2, 5, 13} {
			prefix := strings.Repeat("积分", prefixLen)
			s := prefix + "（此处引用【系统现值】口径）后续内容"
			got := sanitizeVisitorText(s)
			if !utf8.ValidString(got) {
				t.Fatalf("prefixLen=%d 时切出非法 UTF-8：%q", prefixLen, got)
			}
			if got != prefix+"后续内容" {
				t.Fatalf("prefixLen=%d 时切点错了：%q", prefixLen, got)
			}
		}
	})
}

// TestSanitizeStripsModelSelfNarration ★ 082x 第八条：模型把**给自己看的说话策略**写进正文时的末道卫生。
//
// 现网实测（lang=en 问「能否保留 PDF 原版式并自动重算公式」）：回答主体是合格英文，
// 句中嵌着「（先接住，用户可能期待否定或肯定，这里肯定但自带前提）」与「（同样引用边界）」
// ——50 个汉字混在英文气泡里。占比判据救不了它（汉字只占全篇一小部分，补翻闸门按主体语言放行），
// 所以这一段既不该翻、也不该留，只能剥：那是**旁白**，不是内容。
func TestSanitizeStripsModelSelfNarration(t *testing.T) {
	prod := "Sure! But PDF layout and formula recalculation are tricky.（先接住，用户可能期待否定或肯定，这里肯定但自带前提）" +
		" We keep structure where we can.（同样引用边界） Let me know if you send a sample file."
	got := sanitizeVisitorText(prod)
	if strings.ContainsAny(got, "接住期待引用边界") {
		t.Fatalf("旁白没剥净：%q", got)
	}
	for _, want := range []string{"PDF layout and formula recalculation are tricky.", "keep structure where we can.", "sample file"} {
		if !strings.Contains(got, want) {
			t.Fatalf("剥旁白把英文正文一起吃掉了 %q：%q", want, got)
		}
	}
	// 负向对照：面向访客的正常补充说明、以及「期待您的文档」这类客服话术一个字都不许动。
	// 这张清单之所以收得窄，就是因为误剥的后果是**吃掉客户要看的说明**。
	for _, s := range []string{
		"专业模式（保留版式）更适合合同，期待您的文件（40MB 以内）。",
		"按字符计费（1 积分≈0.0997 元），以套餐页为准（客户常问的那条）。",
		"你可以上传样本，我帮你看（用户手册里写的那三步）。",
	} {
		if got := sanitizeVisitorText(s); got != s {
			t.Fatalf("合格文案被误伤：%q → %q", s, got)
		}
	}
	// 清单本身要有对照：这三条都是旁白用词，缺一条就是又一轮 whack-a-mole
	for _, mk := range []string{"接住", "引用边界", "自带前提"} {
		if !strings.Contains(strings.Join(selfNarrationMarkers, ","), mk) {
			t.Fatalf("旁白清单缺 %q（现网实测形态）", mk)
		}
	}
	// 总表必须真的把两张源表都并进来（只改一张＝sanitize 仍旧漏，属于"改了没生效"）
	if len(visitorDropMarkers) != len(internalEchoMarkers)+len(selfNarrationMarkers) {
		t.Fatalf("visitorDropMarkers 没并全两张清单：%d ≠ %d+%d",
			len(visitorDropMarkers), len(internalEchoMarkers), len(selfNarrationMarkers))
	}
}

// TestPostProcessSanitizesAndKeepsGoMarkers 卫生挂在 postProcess 总口上，且不许影响【go:key】摘取
// （摘标记在前、卫生在后；摘完留下的空行也要一并收）。
func TestPostProcessSanitizesAndKeepsGoMarkers(t *testing.T) {
	e := newTestEngine(t)
	raw := "按积分计费，单价看控制台（不许把系统现值原文贴给用户）。\n\n【go:pricing】\n"
	rep := e.postProcess(raw, "m")
	if strings.Contains(rep.Content, "系统现值") || strings.Contains(rep.Content, "【go:") {
		t.Fatalf("正文没洗干净：%q", rep.Content)
	}
	if len(rep.Actions) != 1 || rep.Actions[0].Key != "pricing" {
		t.Fatalf("入口按钮被卫生逻辑吃掉了：%+v", rep.Actions)
	}
	if !utf8.ValidString(rep.Content) {
		t.Fatalf("正文非法 UTF-8：%q", rep.Content)
	}
}

// TestSanitizeStripsQuoteMechanicsAside ★ 082x 第十条（2026-09-29 用户带截图报「括号里的内容没清洗掉」）。
//
// 现网原文（中文轮问比价，红框里那一段整段是模型在执行提示词时的自我说明）：
//
//	（注：根据规则，此处需在最后单独输出标记，且 key 必须来自指定列表。
//	 用户输入"deep"属于延续比较场景，故推荐对比页面入口。）
//
// 第八条那 19 条形态一个都没命中（不含"接住/提示词/用户可能"任何一个），containsAny 判假 ⇒ 原样送出。
// 这一条断言钉三件事：① 新八条词条真在清单里；② 它们真被 sanitize 这条链消费（不是又一张死表）；
// ③ 收窄判据没被"多收几条"顺手推宽——同屏那条正常报价说明「（1000 字符约 40.61 元）」必须原样留着。
func TestSanitizeStripsQuoteMechanicsAside(t *testing.T) {
	prod := "专业模式按 2000×400+7.5 计约 150 积分（注：根据规则，此处需在最后单独输出标记，" +
		"且 key 必须来自指定列表。用户输入\"deep\"属于延续比较场景，故推荐对比页面入口。）如需精确额度请把文件发我。"
	got := sanitizeVisitorText(prod)
	for _, leaked := range []string{"注：", "根据规则", "指定列表", "用户输入", "推荐对比页面入口", "延续比较场景"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("旁白没剥净（残留 %q）：%q", leaked, got)
		}
	}
	// 正文两头都必须活着：剥的是括号段，不是整句。
	for _, want := range []string{"专业模式按 2000×400+7.5 计约 150 积分", "如需精确额度请把文件发我。"} {
		if !strings.Contains(got, want) {
			t.Fatalf("正事被一起吃掉了 %q：%q", want, got)
		}
	}
	if !utf8.ValidString(got) {
		t.Fatalf("剥完是非法 UTF-8：%q", got)
	}

	t.Run("同屏那条正常报价说明一个字都不许动", func(t *testing.T) {
		for _, s := range []string{
			"1000 字符约 40.61 元（1000字符约40.61元）。",
			"可以交付 PDF（输出为 PDF，按套餐规则计费），版式保留。",
			"标签在后台维护（需要登录后台看标签列表）。",
			"域名解析从这里开始（此处需填写你的域名）。",
			"你可以上传样本，我帮你看（用户手册里写的那三步）。",
		} {
			if g := sanitizeVisitorText(s); g != s {
				t.Fatalf("合格文案被误伤：%q → %q", s, g)
			}
		}
	})

	t.Run("新八条逐条独立生效（改名式破坏必须当场红）", func(t *testing.T) {
		// 每条各配一个只含它自己的括号段：少一条词条＝这一行红，
		// 而不是"清单少一项、整表还是绿的"那种清单式锁。
		for _, mk := range []string{
			"根据规则", "此处需在最后", "单独输出", "输出标记", "指定列表", "用户输入", "本轮输入", "思考过程",
		} {
			if !strings.Contains(strings.Join(selfNarrationMarkers, ","), mk) {
				t.Fatalf("旁白清单缺 %q", mk)
			}
			s := "价格以套餐页为准（" + mk + "）。"
			if g := sanitizeVisitorText(s); strings.Contains(g, mk) || g != "价格以套餐页为准。" {
				t.Fatalf("词条 %q 在清单里却没被这条链消费：%q → %q", mk, s, g)
			}
		}
	})

	t.Run("括号只吃命中那条，不吃邻居", func(t *testing.T) {
		s := "支持原格式（保留版式）与表格（根据规则这里是一段旁白说明）。"
		g := sanitizeVisitorText(s)
		if g != "支持原格式（保留版式）与表格。" {
			t.Fatalf("该留的括号被吃或该剥的没剥干净：%q", g)
		}
	})
}

// TestSanitizeStripsInstructionEchoLead ★ D-LLM-20261001-001（2026-10-01 用户带截图报）：
// 模型把提示词的回答策略当正文复述成开头的「先…再…：」引子，且它**不带括号**，
// 上面那套按括号段办事的清洗与观测全都没接住。本条钉三件事：
// ① 现网原形真被剥、冒号后的正文一个字不丢；② 合法的分步引导／口语开场不许被误伤；
// ③ 判据真的在同时看「先 + 策略词 + 冒号收尾 + 后面还有正文」，缺任一即不剥（反证）。
func TestSanitizeStripsInstructionEchoLead(t *testing.T) {
	// ① 现网原形：中文轮问「你和deepl」，第一句是策略引子，冒号后才是正文
	prod := "先肯定对比合理性，再分角度补充新细节：\n\n对比很正常，咱们跟通用工具真正不一样的是原版式保留和术语库锁定。"
	got := sanitizeVisitorText(prod)
	if strings.Contains(got, "先肯定") || strings.Contains(got, "再分角度") || strings.Contains(got, "补充新细节") {
		t.Fatalf("方法论引子没剥净：%q", got)
	}
	if !strings.Contains(got, "对比很正常，咱们跟通用工具真正不一样的是原版式保留和术语库锁定。") {
		t.Fatalf("剥引子把正文一起吃掉了：%q", got)
	}
	// 半角冒号 + 无空行的近邻形态也要剥
	if g := sanitizeVisitorText("先认可你的考虑, 再讲我们的差异:\n价格按源字符算。"); strings.Contains(g, "先认可") {
		t.Fatalf("半角冒号形态没剥：%q", g)
	}

	// ② 误伤对照：这些**合法正文**一个字都不许动（没有策略词 / 不以冒号收尾 / 只有一行）
	for _, keep := range []string{
		"先注册，再上传，最后下载：\n文件翻译三步走。",    // 分步引导：有"先…再…"但无策略名词
		"我再讲清楚一点：\n你传什么格式，出来还是什么格式。", // 口语开场：有"再讲"但开头不是"先"
		"第一步先确认余额：\n顶部徽标就能看到。",       // 操作指引：含"先"但整行不是"先"起头的策略盘算
		"先看看这个。", // 只有一行、无后续正文，病态形态不剥
	} {
		if g := sanitizeVisitorText(keep); g != strings.TrimSpace(keep) {
			t.Errorf("合法正文被误伤：%q → %q", keep, g)
		}
	}

	// ③ 反证：判据必须同时看四腿。把"先肯定…："单独放第一行、后面接正文 → 剥；
	//    把策略引子挪到**第二行**（不是开头）→ 不剥（本判据只管开头引子，不越权删正文中段）。
	if g := sanitizeVisitorText("先肯定他的考虑，再讲差异：\n真正不一样的是原版式。"); strings.Contains(g, "先肯定") {
		t.Fatalf("反证：开头引子应被剥却没剥：%q", g)
	}
	midLine := "好的。\n先肯定他的考虑，再讲差异：\n正文内容。"
	if g := sanitizeVisitorText(midLine); !strings.Contains(g, "先肯定") {
		t.Fatalf("反证：非开头的策略句不该被这条链删（只管第一行）：%q → %q", midLine, g)
	}
	// 反证：引子后面没正文时绝不剥（否则把整条回复清空）
	if g := stripInstructionEchoLead("先肯定对比合理性，再分角度补充新细节："); strings.Contains(g, "先肯定") == false {
		t.Fatalf("反证：无后续正文却把整段剥空了：%q", g)
	}
}

// TestUnstrippedAsidesOnlyLogsNeverEdits ★ 第十条的第二条腿：词表追不上模型措辞时，
// 观测腿负责把候选形态打进 WARN，但它**一个字都不许改正文**——
// 误删的代价是客户要看的说明，误报的代价只是人看一眼日志，两者不能换。
//
// 反证方向与上面那条相反：这里断言的是"报了但留着"，
// 所以谁把观测腿误接成清洗腿（顺手删正文），这一条会当场红。
func TestUnstrippedAsidesOnlyLogsNeverEdits(t *testing.T) {
	t.Run("非中文语种：整段中文括号备注进候选", func(t *testing.T) {
		s := "Sure, layout is kept（这条备注的词表还没收进来）。"
		got := sanitizeVisitorText(s)
		if got != s {
			t.Fatalf("词表外的括号被观测腿误删了：%q → %q", s, got)
		}
		if len(unstrippedAsides("en", s)) != 1 {
			t.Fatalf("观测腿没抓到非中文轮里的中文备注，下一批就没有词条证据：%q", s)
		}
	})
	t.Run("中文轮：按形态抓（元说明起手＋内部名词）", func(t *testing.T) {
		s := "总额看实际字符量（备注：模型需要先确认列表是否可用）。"
		if got := sanitizeVisitorText(s); got != s {
			t.Fatalf("观测形态被误删：%q → %q", s, got)
		}
		if len(unstrippedAsides("zh", s)) != 1 {
			t.Fatalf("中文轮的元说明备注没被抓出来：%q", s)
		}
	})
	t.Run("正常说明不进候选", func(t *testing.T) {
		for _, s := range []string{
			"1000 字符约 40.61 元（1000字符约40.61元）。",
			"域名解析从这里开始（此处需填写你的域名）。",
			"标签在后台维护（需要登录后台看标签列表）。",
		} {
			if got := unstrippedAsides("zh", s); len(got) != 0 {
				t.Fatalf("正常补充说明被当成候选：%q → %v", s, got)
			}
		}
	})
	t.Run("命中词表的段不再重复报告", func(t *testing.T) {
		s := "价格以套餐页为准（根据规则这里应当补一句说明）。"
		if got := unstrippedAsides("zh", s); len(got) != 0 {
			t.Fatalf("已被清洗的形态又进候选，计数会虚高：%v", got)
		}
	})
	t.Run("未闭合括号不报（那是截断，有自己的 WARN）", func(t *testing.T) {
		if got := unstrippedAsides("en", "Truncated aside（根据规则"); len(got) != 0 {
			t.Fatalf("截断被观测腿当成旁白候选：%v", got)
		}
	})
}

// TestTruncatedTranslationKeepsOriginal 补翻被 max_tokens 截断时**判失败**，不许把半句译文送出去。
// 这是「LLM 输出链静默失效」那批形态里最阴的一种：请求成功、状态 200、产物能读，
// 只有 finish_reason 说真话——半句英文回答比整段中文更接近对外错报。
func TestTruncatedTranslationKeepsOriginal(t *testing.T) {
	url, hits, _ := scriptedLLM(t,
		llmCall{content: chineseReplySample},
		llmCall{content: "We bill by credits, and the current ra", truncated: true}, // 半句
	)
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	newSession(t, e, "s-lang-trunc")
	rep := e.Respond(context.Background(), "s-lang-trunc", "what is the price", "/", "en", nil)
	if rep.LangLocalized {
		t.Fatalf("截断的半句译文被判成功了：%q", rep.Content)
	}
	if rep.Content != chineseReplySample {
		t.Fatalf("补翻截断后正文被改动，应原样留中文：%q", rep.Content)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("上游调用 %d 次，应为 2（第 2 次是那次失败的补翻）", n)
	}
}

// TestTranslationBudgetsDifferByTextShape 两路翻译的额度必须分开钉：
// canned 文案两三行（400 够），对话正文是整条回答（给到 1200）——
// 拿 400 去翻整条回答必然翻出半句，上面那条锁就永远不红。判据抓**真发出去的请求体**。
func TestTranslationBudgetsDifferByTextShape(t *testing.T) {
	url, _, bodies := scriptedLLM(t, llmCall{content: "Hello from LangCross"})
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)
	ctx := context.Background()

	e.LocalizeGreeting(ctx, "你好，我是能言 AI 助手", "en")
	if got := bodies.at(0); !strings.Contains(got, `"max_tokens":400`) {
		t.Fatalf("canned 文案的翻译额度变了（应为 400，思维链＋两行正文的实测档）：\n%s", got)
	}
	if _, err := e.LocalizeReply(ctx, chineseReplySample, "en"); err != nil {
		t.Fatalf("LocalizeReply 失败： %v", err)
	}
	if got := bodies.at(1); !strings.Contains(got, `"max_tokens":1200`) {
		t.Fatalf("补翻整条回答仍用 canned 的小额度（会稳定翻出半句）：\n%s", got)
	}
	// ★ 096x-1：品牌名口径现在**按源文投**（见 translateContract），所以这条"两条路同源"的锁拆成两条腿钉：
	//   - 源文提了品牌名 → 两条路都给同一张表里的写法（092x 红腿三那条不许退化）；
	//   - 源文没提品牌名 → 两条路都给同一句反向禁令（现网俄文 chips 四条前挂 "LangCross: " 缺的就是它）。
	// 原来那条单腿断言锁的正是本批改掉的旧形态（拿一句不含品牌名的正文去要求"一律写作 X"），故改写而非删掉。
	if got := bodies.at(1); !strings.Contains(got, "不许出现") {
		t.Fatalf("原文没提品牌名的那条回答，补翻提示词里却没有反向禁令：\n%s", got)
	}
	if _, err := e.LocalizeReply(ctx, "能言（LangCross）支持长文档按页折算。", "en"); err != nil {
		t.Fatalf("LocalizeReply（带品牌名的源文）失败：%v", err)
	}
	if got := bodies.at(2); !strings.Contains(got, "品牌名一律写作「LangCross」") || !strings.Contains(got, "Nengyan") {
		t.Fatalf("带品牌名的源文丢了写法口径（会和挂件标题打架）：\n%s", got)
	}
}

// countHan 测试侧独立数汉字，用来把「判据凭什么放行」写进失败信息（不靠实现里的私有函数自证）。
func countHan(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			n++
		}
	}
	return n
}

// TestSanitizeDropsJapaneseNarration ★ 093x（2026-09-30 真机挂件复问）：日文轮正文尾部那句
// 「（※日本語で回答するため…考慮して翻訳を実施。）」是模型在**交代自己的作答动作**，
// 而这一族此前两条腿全瞎——删除清单收的是中文指令用词，观测腿对日文轮整档豁免，
// 于是既没剥也没记，只能靠用户截图发现。本条把现网原文钉成基线。
func TestSanitizeDropsJapaneseNarration(t *testing.T) {
	got := sanitizeVisitorText(leakReply093x)
	for _, want := range []string{"で回答するため", "翻訳を実施", "考慮して"} {
		if strings.Contains(got, want) {
			t.Fatalf("日文旁白没剥净（残留 %q）：%q", want, got)
		}
	}
	// 同一份原文里的另一半残渣：那圈没有目标的方括号必须在这一道里一起收掉
	// （否则"末道卫生"只管圆括号，客户端继续看到 markdown 语法残骸）
	if strings.ContainsAny(got, "[]") {
		t.Fatalf("裸方括号没在 sanitize 这一道里收掉（只拆不接＝腿没挂上）：%q", got)
	}
	// 正文一个字都不许跟着丢：报价数字与那句"请把原文发我"是客户要看到的全部内容
	for _, keep := range []string{"150ポイント", "400ポイント", "1,000文字", "原文を送信いただければ"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("剥旁白把日文正文一起吃掉了 %q：%q", keep, got)
		}
	}
	// 清单本身要有对照：这两条都是现网逐字形态，缺一条就是又一轮 whack-a-mole
	for _, mk := range []string{"で回答するため", "翻訳を実施"} {
		if !containsAny(mk, selfNarrationMarkers) {
			t.Fatalf("旁白清单缺现网形态 %q", mk)
		}
	}
}

// TestSanitizeKeepsNormalParentheticalsAndBrackets 误伤对照：点名语种／提到"翻译"／方括号链接
// 这三种**正常对外说明**都不许被动到。没有这一条，上面的绿灯可能只是"逢括号就删"。
func TestSanitizeKeepsNormalParentheticalsAndBrackets(t *testing.T) {
	cases := []string{
		"12言語に対応しています（日本語・英語・中国語の12言語に対応）。",
		"翻訳は人間がチェックします（翻訳は人間がチェックします）。",
		"PDFのファイル形式をそのままに（PDFのファイル形式をそのままに）。",
		"具体以注册页公示为准（具体以注册页公示为准）。",
		"[料金表](/pricing) をご確認ください。", // 带目标的 markdown 链接：整条语法是完整的，不算残渣
		"案内はここ [料金表。", // 未闭合的左方括号＝截断，原样留着
	}
	for _, in := range cases {
		if got := sanitizeVisitorText(in); got != strings.TrimSpace(in) {
			t.Errorf("正常正文被卫生改动了：\n 原：%q\n 出：%q", in, got)
		}
		// 这些都不该被观测腿当成旁白**删掉**（观测腿允许报出来攒证据，删除判据一条都不能命中）
		if drop := dropParentheticals(in, visitorDropMarkers); drop != in {
			t.Errorf("删除判据误命中：%q → %q", in, drop)
		}
	}
}

// TestUnwrapBrokenLinkBrackets ★ 093x 现网第二条残渣：「[ pricing ページで詳細を確認]」——
// 模型写了 markdown 链接的方括号那一半，`(url)` 那一半根本没出。拆括号留文字，两种损失都没有。
func TestUnwrapBrokenLinkBrackets(t *testing.T) {
	got := unwrapBrokenLinkBrackets("ご案内できます。[ pricing ページで詳細を確認]")
	if strings.ContainsAny(got, "[]") {
		t.Fatalf("裸方括号没拆掉：%q", got)
	}
	if !strings.Contains(got, "pricing ページで詳細を確認") {
		t.Fatalf("拆括号把链接文字一起吃掉了：%q", got)
	}
	for _, keep := range []string{
		"[料金表](/pricing)",                   // 带目标＝完整语法
		"[推奨",                               // 未闭合＝那是截断，留着现场
		"[]",                                // 空括号
		"[" + strings.Repeat("長", 60) + "]", // 超长内容不当链接文字处理
	} {
		if got := unwrapBrokenLinkBrackets(keep); got != keep {
			t.Errorf("不该动的方括号被改了：%q → %q", keep, got)
		}
	}
	// 已知的**取舍**（不是缺陷，写死在这里防"以后有人给它加白名单"看不见代价）：
	// badge 式的「[推奨]」同样会被拆成「推奨」——括号里的话一个字都不丢，只少一圈括号。
	if got := unwrapBrokenLinkBrackets("角括弧は [推奨] のように書きます"); strings.ContainsAny(got, "[]") ||
		!strings.Contains(got, "推奨 のように") {
		t.Errorf("badge 式方括号的取舍形态变了：%q", got)
	}
}

// TestUnstrippedAsidesCatchesJapaneseNarrationVariant ★ 093x 观测腿第三条（点名语种＋交代作答动作）：
// 删除清单只收现网逐字实证的那两条日文形态，**换个说法的同类旁白必须先在日志里露一次面**，
// 否则下一族形态还是靠用户截图发现（本仓这一族已经连续四轮这么报上来）。
func TestUnstrippedAsidesCatchesJapaneseNarrationVariant(t *testing.T) {
	// 同一族但词表未命中的变体：日本語＋回答，却没写「で回答するため」
	variant := "ご案内できます。（※日本語で回答していますのでご確認ください）"
	got := unstrippedAsides("ja", variant)
	if len(got) == 0 {
		t.Fatalf("日文旁白变体没进观测清单（这条链又不能攒证据了）：%q", variant)
	}
	// 反向对照：只点名语种、或只提"翻译"这个动作，都不算旁白候选
	for _, keep := range []string{
		"12言語に対応しています（日本語・英語・中国語に対応）。",
		"人手で確認します（翻訳は人間がチェックします）。",
	} {
		if got := unstrippedAsides("ja", keep); len(got) != 0 {
			t.Errorf("正常说明被判成旁白候选：%q → %v", keep, got)
		}
	}
	// 已被删除清单命中的形态**不再重复报**（报了也只会淹掉那些真正待定性的候选）
	if got := unstrippedAsides("ja", leakReply093x); len(got) != 0 {
		t.Errorf("已经会剥掉的旁白又报了一遍候选（日志里就没法只看新形态了）：%v", got)
	}
}
