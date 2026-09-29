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
	// 品牌名口径在两条路上同源：英文界面一律 LangCross，且禁拼音
	if got := bodies.at(1); !strings.Contains(got, "品牌名一律写作「LangCross」") || !strings.Contains(got, "Nengyan") {
		t.Fatalf("补翻提示词丢了品牌名口径（会和挂件标题打架）：\n%s", got)
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
