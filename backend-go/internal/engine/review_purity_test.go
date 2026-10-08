// ============ review_purity_test.go · 职责说明 ============
// ★ 〇-AR 第 8 波（㊶）：en→zh 出栈把「英文回译」拼在中文译文后面（现网 3/3 复现）。
//
// 现网字节级读数（匿名 POST /api/trial/translate，三次全 200）：
//
//	我们需要为下周的法兰克福汽车展翻译产品手册。We need to translate the product manual for next week's Frankfurt Motor Show.
//	我们的刹车片适用于大多数欧洲款轿车车型。Our brake pads are suitable for most European car models.
//	感谢您对我们空气悬挂套件的咨询。Thank you for your inquiry about our air suspension kit.
//
// 决定性读数：尾段英文**不是原文逐字**（auto show→Motor Show、compatible→suitable、kits→kit），
// 它是**中文译文的回译** ⇒ 缺陷本体在审校腿（只有它手里同时有【原文】与那份中文【待审校译文】），
// 且它的产物会在 text.go 的校对环节**直接覆盖**正确的初翻。
//
// 本文件钉住四条腿：
//
//	② 审校/改写产物脚本不纯 ⇒ 整份丢弃、保留上一版（单段／批量／驳回重译三条腿共用一把尺子）；
//	④ 出栈尾段整句异脚本 ⇒ 只剥尾段（初翻腿没有"上一版"可退，只能剥不能丢）；
//	③ 提示词层：全部目标语种分支都带齐「不得复述原文」＋「不得附加回译」（派生式锁，不留例外档）；
//	观测腿：三个动作档（review_rejected／review_batch_rejected／tail_stripped）计数＋日志行，
//	       否则"界面正常、只是少润色一句"这种失败形态在现网完全读不到。
//
// ★ 反证（本批在 /tmp 副本树上实跑，结果记在《缺陷与缺失清单》㊶ 段与提交说明里）：
//
//	P1 把 reviewOutputRejectReason 的阈值判据改成恒返回 ""（＝②整条摘掉）
//	   ⇒ 六条一起红（副本实跑读数）：TestReviewPurityThresholdOnRealReadings／
//	     TestReviewOutputScriptPurityRejectsMixed／TestReviewBatchMixedLineFallsBackToOriginal／
//	     TestTranslateWithFeedbackExRejectsImpure／TestReviewLegJudgesRawContentNotPostProcessed／
//	     TestHandleTextKeepsInitialWhenReviewLegMixed；
//	P2 把三条腿的判据从「清洗前的 content」改回「PostProcessTranslation 之后」
//	   ⇒ TestReviewOutputScriptPurityRejectsMixed／TestReviewLegJudgesRawContentNotPostProcessed／
//	     TestHandleTextKeepsInitialWhenReviewLegMixed 三条红（副本实跑只命中 2 处判据：
//	     批量腿吃的是逐行的 revised 而非 content，故它不在这一次改动的射程里，另有 M1 覆盖）；
//	P3 摘掉 stripTrailingForeignResidual 的「数字序列逐字不变」前置
//	   ⇒ TestStripTrailingForeignResidualPrerequisites 红（带数量的尾段被剥＝把信息删了）；
//	P4 把 default 分支补的那句「不得附加回译」删掉（中/英两侧各一次）
//	   ⇒ TestTranslateInstructionForbidsSourceEchoForAllTargets 红（派生式遍历，语种漏一条当场现形）；
//	P5 摘掉生产侧的 recordPurityAction 调用
//	   ⇒ TestPostProcessTranslationStripsForeignTailForZh（④）与
//	     TestReviewOutputScriptPurityRejectsMixed／TestReviewBatchMixedLineFallsBackToOriginal（②）红；
//	     ⚠️ TestPuritySnapshotIsCopyAndCounts 在 P5 下**仍然绿**——它测的是计数器本体的形态
//	     （自己直接调 recordPurityAction），不是"生产侧有没有调"。所以"调用点在不在"这一档
//	     的锁只能落在上面那三条按业务腿断言的用例上（登记于此，防后人把它当调用点锁）。
//
// 首跑真踩（两条都记进实现，不在这里只留结论）：
//
//	① 剥点原写作「最后一个目标脚本字母」，会把正文的「。」一起削掉（出栈变成"…产品手册"没有句号）
//	   ⇒ 已改成「最后一个目标脚本字母之后的第一个拉丁字母」，共用标点留在正文里，
//	     等值锁见 TestStripTrailingForeignResidualPrerequisites 的标点那一段；
//	② 并发腿两条 WaitGroup 挂在同一个 wg 上＝「wg.Wait() 等 stop、stop 等 wg.Wait()」的自设死锁，
//	   表现为测试挂到超时而不是当场红（见 TestPuritySnapshotIsCopyAndCounts 注释）。
//
// 运行：env DB_DRIVER=sqlite go test -race -count=1 ./internal/engine/ -run 'Purity|Review|StripTrailingForeign|HandleTextKeepsInitial'
// =============================================
package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"translator/internal/config"
	"translator/internal/tenant"
)

// ------------------------------------------------------------
// 现网三条读数（逐字拷贝，作为判据阈值与"改前形态"的唯一真值）
// ------------------------------------------------------------

const (
	// purityReal1 第一次实跑：尾段 13 个拉丁词，且与原文逐字不同（回译）。
	purityReal1 = "我们需要为下周的法兰克福汽车展翻译产品手册。We need to translate the product manual for next week's Frankfurt Motor Show."
	// purityReal2 第二次实跑：尾段 10 词（compatible→suitable、sedan models→car models）。
	purityReal2 = "我们的刹车片适用于大多数欧洲款轿车车型。Our brake pads are suitable for most European car models."
	// purityReal3 第三次实跑：尾段 10 词（kits→kit 单数化）。
	purityReal3 = "感谢您对我们空气悬挂套件的咨询。Thank you for your inquiry about our air suspension kit."
)

// lockedBuf 并发安全的日志缓冲（② 那三条腿跑在校对阶段的 goroutine 里，不能用裸 bytes.Buffer）。
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write 实现 io.Writer（加锁）。
func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// String 取当前快照（加锁拷贝，避免与并发写撕裂）。
func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// capturePurityLogs 把 slog 与标准库 log 同时引到内存缓冲，测试结束原样接回。
// 返回缓冲指针；用例按档名/关键中文串在其中断言，缺一条即"观测腿静默"。
func capturePurityLogs(t *testing.T) *lockedBuf {
	t.Helper()
	buf := &lockedBuf{}
	oldSlog := slog.Default()
	oldLog := log.Writer()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(buf)
	t.Cleanup(func() {
		slog.SetDefault(oldSlog)
		log.SetOutput(oldLog)
	})
	return buf
}

// purityCount 读一个 (语种,动作) 档的累计值。
func purityCount(lang, action string) int64 {
	return PuritySnapshot()[lang+"|"+action]
}

// ------------------------------------------------------------
// 假上游：按提示词把三条腿分开喂
// ------------------------------------------------------------

// purityPromptKind 判定一次上游调用属于哪条腿（口径：只有审校族提示词里同时有【原文】与中文【待审校译文】）。
func purityPromptKind(prompt string) string {
	switch {
	case strings.Contains(prompt, "资深翻译审校"):
		return "review" // 单段与批量两条审校腿共用这句开头
	case strings.Contains(prompt, "前一次翻译被"):
		return "feedback" // 驳回重译腿（同样会覆盖上一版，故共用 ② 的尺子）
	default:
		return "initial"
	}
}

// chatOpenAIResp 把文本包成 OpenAI 兼容回执。
func chatOpenAIResp(content string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	})
	return string(b)
}

// purityEngine 造一台「假上游按腿回话」的引擎。
// 参数 reply: 输入（腿名, 提示词）→ 该腿要回的内容；返回值同时把每次收到的提示词记进 prompts（供断言）。
func purityEngine(t *testing.T, reply func(kind, prompt string) string) (e *Engine, prompts *lockedBuf) {
	t.Helper()
	rec := &lockedBuf{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		var joined strings.Builder
		for _, m := range body.Messages {
			joined.WriteString(m.Content)
			joined.WriteString("\n")
		}
		prompt := joined.String()
		rec.mu.Lock()
		_, _ = rec.b.WriteString(purityPromptKind(prompt) + "\x00" + prompt + "\x01")
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatOpenAIResp(reply(purityPromptKind(prompt), prompt)))
	}))
	t.Cleanup(srv.Close)

	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 自钉方言（AGENTS §一·4）
	cfg.OnlineAPIBase = srv.URL + "/v1"
	cfg.OnlineAPIKey = "test-key"
	cfg.OnlineModel = "test-model"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return NewEngine(cfg, nil, nil, nil), rec
}

// ------------------------------------------------------------
// 判据本体（纯函数）
// ------------------------------------------------------------

// TestReviewPurityThresholdOnRealReadings 阈值腿：现网三条必须判不纯，合法形态必须判纯。
//
// 这一条同时钉住三件事：
//  1. 词数读数 13／10／10 与文档一致（判据不是"看起来有英文就拦"，是"整句"才拦）；
//  2. 阈值 8 这个对外契约字面量（改它必须连致例一起重跑，见 reviewLatinRunMinWords 注释）；
//  3. 拉丁目标与不认识的语种**一律不判**（fail-soft：拦错的代价是把正确译文删掉）。
func TestReviewPurityThresholdOnRealReadings(t *testing.T) {
	// ① 现网三条：必须拦，且词数与清单记录一致
	for _, tc := range []struct {
		out   string
		words int
	}{
		{purityReal1, 13},
		{purityReal2, 10},
		{purityReal3, 10},
	} {
		if got := maxLatinWordRun(tc.out); got != tc.words {
			t.Fatalf("最长拉丁连跑读数 %d，期望 %d：%q", got, tc.words, tc.out)
		}
		if r := reviewOutputRejectReason("zh", tc.out); r != "review_latin_run_in_nonlatin_target" {
			t.Fatalf("㊶ 现网形态没被拦（reason=%q）：%q", r, tc.out)
		}
	}
	// ② 阈值边界：8 词即拦、7 词不拦（"整句"与"术语行"的分界就钉在这两个数上）
	eight := "详见下列说明。Please read the product manual before using the device."
	seven := "请参照 Product Manual for the Frankfurt Auto Show 获取参数"
	if got := maxLatinWordRun(eight); got < reviewLatinRunMinWords {
		t.Fatalf("阈值正锁失效：这条应 ≥%d 词，实得 %d：%q", reviewLatinRunMinWords, got, eight)
	}
	if r := reviewOutputRejectReason("zh", eight); r == "" {
		t.Fatalf("恰好到阈值的整句回译必须拦：%q", eight)
	}
	if got := maxLatinWordRun(seven); got >= reviewLatinRunMinWords {
		t.Fatalf("反证失效：这条合法术语行应 <8 词，实得 %d：%q", got, seven)
	}
	if r := reviewOutputRejectReason("zh", seven); r != "" {
		t.Fatalf("合法英文书名（7 词）被误判成回译：%q reason=%q", seven, r)
	}
	// ③ 合法形态不拦：术语与单位、缩写、带音字母（\p{Latin} 口径，不是 [A-Za-z]）
	// 带音字母那一侧先钉一次**词数读数**：[A-Za-z] 会把 "l’expérience" 拆成 2 词、"café" 拆成 2 词，
	// 于是同一条串在两种写法下计数不同 ⇒ 这一句就是 \p{Latin} 的等值证据（不靠注释说"我们用了 \p{Latin}"）。
	if got := maxLatinWordRun("Voici l’expérience café"); got != 3 {
		t.Fatalf("带音字母/撇号的拉丁词计数 %d，期望 3（\\p{Latin}＋词内撇号口径）", got)
	}
	for _, legit := range []string{
		"支持 Bluetooth 5.0 与 iOS 17 及以上版本的数字钥匙，整机重量约 2.3 kg。",
		"国际标准化组织 International Organization for Standardization 的缩写是 ISO。",
		"请按 café 与 state-of-the-art 版的说明操作。",
		"结论：符合 EN 14350 与 ASTM F963 两项标准，样品已送 TUV SUD 实验室。",
	} {
		if r := reviewOutputRejectReason("zh", legit); r != "" {
			t.Fatalf("合法译文被判不纯（reason=%q，连跑读数 %d）：%q", r, maxLatinWordRun(legit), legit)
		}
	}
	// ④ fail-soft 两侧：拉丁目标不判（中文残留已由 StripChineseInNonZh 收口）、陌生语种不判
	if r := reviewOutputRejectReason("en", "We need the manual. 我们需要为下周的法兰克福汽车展翻译产品手册。"); r != "" {
		t.Fatalf("拉丁目标不该走纯度判据（会把合法英文译文拦掉）：%q", r)
	}
	if r := reviewOutputRejectReason("xx", purityReal1); r != "" {
		t.Fatalf("不认识的语种必须 fail-soft（不拦），实得 %q", r)
	}
	// ⑤ 契约字面量：档名与阈值同为对外排障契约，逐字钉死
	if reviewLatinRunMinWords != 8 {
		t.Fatalf("阈值字面量被改动：%d（改名/改数都得先把文档与用例一起过一遍）", reviewLatinRunMinWords)
	}
}

// TestStripTrailingForeignResidualPrerequisites ④ 的前置逐条点名（任一不过即**原样返回**）。
func TestStripTrailingForeignResidualPrerequisites(t *testing.T) {
	// 正锁：现网三条的尾段都该被剥，且剥后正文一字不少
	for _, pair := range [][2]string{
		{purityReal1, "我们需要为下周的法兰克福汽车展翻译产品手册。"},
		{purityReal2, "我们的刹车片适用于大多数欧洲款轿车车型。"},
		{purityReal3, "感谢您对我们空气悬挂套件的咨询。"},
	} {
		got, did := stripTrailingForeignResidual("zh", pair[0])
		if !did {
			t.Fatalf("㊶ 初翻腿形态没被剥（尾段 10–13 词的整句回译）：%q", pair[0])
		}
		if got != pair[1] {
			t.Fatalf("剥后正文被改动\n实得 %q\n期望 %q", got, pair[1])
		}
	}
	// 前置 1 的标点位（首跑真踩）：剥点必须落在**第一个拉丁字母**上，
	// 把正文的中文标点留在 head 里——钉在最后一个目标脚本字母上会把「。」/「！」/「」一起削掉，
	// 那是改了客户要看的正文，超出"只剥尾段"的承诺。
	for _, pair := range [][2]string{
		{"结论！We need to translate the product manual for the Frankfurt auto show next week.", "结论！"},
		{"他说：「没问题」 We need to translate the product manual for the Frankfurt auto show.", "他说：「没问题」"},
		{"手册。 Please find the attached product manual for the Frankfurt Motor Show.", "手册。"},
	} {
		got, did := stripTrailingForeignResidual("zh", pair[0])
		if !did {
			t.Fatalf("共用标点后缀没被剥（连跑读数 %d）：%q", maxLatinWordRun(pair[0]), pair[0])
		}
		if got != pair[1] {
			t.Fatalf("共用标点被削掉\n实得 %q\n期望 %q", got, pair[1])
		}
	}
	// 前置：尾段不足阈值 ⇒ 不剥（合法术语行不许被动刀）
	if _, did := stripTrailingForeignResidual("zh", "请参照 Product Manual for the Frankfurt Auto Show 获取参数。"); did {
		t.Fatal("7 词的合法英文书名被剥掉了")
	}
	// 前置：尾段带数字 ⇒ 不剥（可能在传信息，不许猜）
	if got, did := stripTrailingForeignResidual("zh", "订单已确认。We need 200 units for the Frankfurt Motor Show next week."); did {
		t.Fatalf("尾段带数量 200 的整句被剥＝把信息删了，实得 %q", got)
	}
	// 前置：纯拉丁整段（没有目标脚本字符可锚）⇒ 不剥（剥了就剩空串）
	if got, did := stripTrailingForeignResidual("zh", "We need to translate the product manual for next week's Frankfurt Motor Show."); did {
		t.Fatalf("整段皆外语时不许剥（剥后为空），实得 %q", got)
	}
	// fail-soft：拉丁目标与陌生语种一律不动
	if got, did := stripTrailingForeignResidual("en", "We need the manual. 我们需要产品手册。"); did {
		t.Fatalf("拉丁目标不该剥尾段：%q", got)
	}
	if got, did := stripTrailingForeignResidual("xx", purityReal1); did {
		t.Fatalf("陌生语种不该动刀：%q", got)
	}
	// 多行形态（批量／段落译文）：只剥尾段，前面的行一行都不许少
	multi := "第一行结论。\n第二行说明。\nPlease find the attached product manual for the Frankfurt Motor Show."
	got, did := stripTrailingForeignResidual("zh", multi)
	if !did {
		t.Fatalf("多行尾段整句没被剥：%q", multi)
	}
	if strings.Contains(got, "Please find") || !strings.Contains(got, "第一行结论。") || !strings.Contains(got, "第二行说明。") {
		t.Fatalf("多行剥离越界（应只削尾段）：%q", got)
	}
}

// TestStripTrailingForeignResidualInvariants 把"由实现结构保证、因此不写死支判据"的两条不变量钉住。
//
// AGENTS §一·13 说"死支别写断言"——剥点锚在最后一个目标脚本字符上，
// 所以「剥后非空」与「带目标脚本的行一行不少」在生产代码里是**走不到的分支**；
// 但它们是这一刀的安全边界，必须以**性质断言**的形式存在（改实现时若把它们变成可破坏的，当场红）。
func TestStripTrailingForeignResidualInvariants(t *testing.T) {
	cases := []string{
		purityReal1, purityReal2, purityReal3,
		"支持 Bluetooth 5.0。\n整机重量约 2.3 kg。\nPlease read the product manual before using this device carefully.",
		"結論。\n",
	}
	for _, text := range cases {
		got, did := stripTrailingForeignResidual("zh", text)
		if !did {
			continue
		}
		if strings.TrimSpace(got) == "" {
			t.Fatalf("不变量破：剥后为空（宁可原样返回也不发空译文）：%q", text)
		}
		if nativeLineCount(got) != nativeLineCount(text) {
			t.Fatalf("不变量破：含目标脚本的行数变了 %d→%d：%q → %q",
				nativeLineCount(text), nativeLineCount(got), text, got)
		}
		// 第三条前置同样作为性质复验（生产判据与不变量同源，这里防实现被改成"从中间削"）
		if a, b := digitSeqRe.FindAllString(got, -1), digitSeqRe.FindAllString(text, -1); strings.Join(a, "|") != strings.Join(b, "|") {
			t.Fatalf("不变量破：数字序列少了 %v → %v：%q", b, a, text)
		}
	}
}

// TestPostProcessTranslationStripsForeignTailForZh ④ 端到端：走真实出栈链（含观测腿）。
func TestPostProcessTranslationStripsForeignTailForZh(t *testing.T) {
	logs := capturePurityLogs(t)
	before := purityCount("zh", "tail_stripped")

	got := PostProcessTranslation(purityReal1, "zh")
	if strings.Contains(got, "We need to translate") {
		t.Fatalf("出栈仍带整句回译（④ 没接进 PostProcessTranslation）：%q", got)
	}
	if got != "我们需要为下周的法兰克福汽车展翻译产品手册。" {
		t.Fatalf("剥后正文被改动：%q", got)
	}
	if after := purityCount("zh", "tail_stripped"); after != before+1 {
		t.Fatalf("tail_stripped 计数没跟上（%d→%d）：观测腿静默＝现网读不到这一刀", before, after)
	}
	// 日志口径（2026-10-08 改）：这一条从标准库 log 换成 observability 的结构化 WARN
	// （AGENTS §一·2 棘轮：internal 存量只减不增，恰=176，新增一行标准库 log 当场顶红），
	// 所以断言问的是 **JSON 字段**而不是旧的 `lang=zh` 文本形态——
	// 字段名（lang／latin_words／raw）与档位文案同为现网排障契约，逐字钉住。
	if l := logs.String(); !strings.Contains(l, "译文出栈尾段整句外语残留") ||
		!strings.Contains(l, `"lang":"zh"`) || !strings.Contains(l, `"latin_words":13`) {
		t.Fatalf("④ 缺结构化 WARN 日志行（现网定位抓手）：%q", l)
	}

	// 反证方向：合法形态走完同一条链必须一字不动、且不计数
	before2 := purityCount("zh", "tail_stripped")
	legit := "支持 Bluetooth 5.0 与 iOS 17 及以上版本的数字钥匙，整机重量约 2.3 kg。"
	if got2 := PostProcessTranslation(legit, "zh"); got2 != legit {
		t.Fatalf("合法译文被通用后处理改动：\n实得 %q\n原文 %q", got2, legit)
	}
	if after2 := purityCount("zh", "tail_stripped"); after2 != before2 {
		t.Fatalf("合法译文被误计数（%d→%d）：说明判据比「整句」宽", before2, after2)
	}
}

// ------------------------------------------------------------
// ② 三条替换腿
// ------------------------------------------------------------

// TestReviewOutputScriptPurityRejectsMixed ②单段腿：不纯 ⇒ 返回 ""（调用方保留初翻）＋WARN＋计数。
func TestReviewOutputScriptPurityRejectsMixed(t *testing.T) {
	logs := capturePurityLogs(t)
	const initial = "弊社はスマートハードウェアの研究開発に専念します。"
	mixed := initial + " The company focuses on smart hardware research and development and manufacturing."

	e, _ := purityEngine(t, func(kind, _ string) string {
		if kind == "review" {
			return mixed
		}
		return initial
	})
	before := purityCount("ja", "review_rejected")

	got := e.ReviewTranslation(context.Background(), "本公司专注于智能硬件的研发与制造", initial, "ja", config.StageReview)
	if got != "" {
		t.Fatalf("② 没拦：审校腿的混合产物被当成修正版返回，会**覆盖**正确初翻：%q", got)
	}
	if after := purityCount("ja", "review_rejected"); after != before+1 {
		t.Fatalf("review_rejected 计数没跟上（%d→%d）", before, after)
	}
	l := logs.String()
	if !strings.Contains(l, "review_latin_run_in_nonlatin_target") || !strings.Contains(l, "审校产物脚本不纯") {
		t.Fatalf("② 缺 WARN 行或档名不是契约值：%q", l)
	}
	if !strings.Contains(l, `"lang":"ja"`) {
		t.Fatalf("WARN 行必须带语种（现网按语种定位是哪条腿坏）：%q", l)
	}
}

// TestReviewOutputKeepsLegitRevision 正向对照：合格的审校产物必须照采。
//
// ★ 没有这一条，② 判据写成"恒拦"也能让上面那条绿灯（AGENTS：负向锁必须配正向对照）。
func TestReviewOutputKeepsLegitRevision(t *testing.T) {
	const initial = "弊社はスマートハードウェアの研究開発に専念します。"
	const revised = "弊社はスマートハードウェアの研究開発に専念しております。"

	e, _ := purityEngine(t, func(kind, _ string) string {
		if kind == "review" {
			return revised
		}
		return initial
	})
	before := purityCount("ja", "review_rejected")

	got := e.ReviewTranslation(context.Background(), "本公司专注于智能硬件的研发", initial, "ja", config.StageReview)
	if got != revised {
		t.Fatalf("合格审校产物没被采纳（实得 %q）——② 把射程写宽了，客户永远拿不到润色版", got)
	}
	if after := purityCount("ja", "review_rejected"); after != before {
		t.Fatalf("合格产物被误计数（%d→%d）", before, after)
	}
}

// TestReviewBatchMixedLineFallsBackToOriginal ②批量腿：逐条判、逐条丢，其余照采。
func TestReviewBatchMixedLineFallsBackToOriginal(t *testing.T) {
	logs := capturePurityLogs(t)
	const a1 = "弊社は研究開発に専念します。"
	const a2 = "当社の刹车片は欧洲车型に适配します。" // 故意：第 2 条审校回混合
	const a2Fix = "当社のブレーキパッドは欧州セダン車種に適合します。"
	a2Bad := a2Fix + " Our brake pads are suitable for most European car models sold in the market."

	e, _ := purityEngine(t, func(kind, _ string) string {
		if kind == "review" {
			return "1. " + a1 + "\n2. " + a2Bad
		}
		return a1
	})
	before := purityCount("ja", "review_batch_rejected")

	out := e.ReviewTranslationBatch(context.Background(),
		[]string{"本公司专注于研发", "我们的刹车片适用于欧洲车型"},
		[]string{a1, a2}, "ja", config.StageReview)
	if len(out) != 2 {
		t.Fatalf("批量出参长度必须与入参一致，实得 %d", len(out))
	}
	if out[0] != a1 {
		t.Fatalf("第 1 条合格却被改动：%q", out[0])
	}
	if out[1] != a2 {
		t.Fatalf("第 2 条不纯却没回退原译文：%q", out[1])
	}
	if strings.Contains(out[1], "Our brake pads") {
		t.Fatalf("回译整句进了客户屏幕：%q", out[1])
	}
	if after := purityCount("ja", "review_batch_rejected"); after != before+1 {
		t.Fatalf("review_batch_rejected 计数没跟上（%d→%d）", before, after)
	}
	if l := logs.String(); !strings.Contains(l, "批量审校某条产物脚本不纯") || !strings.Contains(l, `"index":2`) {
		t.Fatalf("批量腿 WARN 缺行或缺序号（现网要问是哪一条）：%q", l)
	}
}

// TestTranslateWithFeedbackExRejectsImpure ②第三腿（驳回重译）：同样不纯即弃、保留上一版。
func TestTranslateWithFeedbackExRejectsImpure(t *testing.T) {
	const prev = "弊社は研究開発に専念します。"
	bad := prev + " The company focuses on research and development of smart hardware devices."

	e, _ := purityEngine(t, func(kind, _ string) string {
		if kind == "feedback" {
			return bad
		}
		return prev
	})
	before := purityCount("ja", "review_rejected")

	got := e.TranslateWithFeedbackEx(context.Background(), "本公司专注于研发", "ja", "术语不准", config.StageAIInitial, nil)
	if got != "" {
		t.Fatalf("驳回重译腿产物不纯却没被丢弃（会把「这一版」覆盖上去）：%q", got)
	}
	if after := purityCount("ja", "review_rejected"); after != before+1 {
		t.Fatalf("驳回重译腿没走同一把尺子（计数 %d→%d）", before, after)
	}
}

// TestReviewLegJudgesRawContentNotPostProcessed P2 反证的正向锁：
// 判据必须吃**清洗前**的原始产物。这条专门盯"先洗后判"那一版错写法——
// 先过 PostProcessTranslation 会把尾段剥干净，② 整档永不触发，
// 客户拿到的是"会多吐一句的模型"改出来的中文（保守方向应是退回初翻）。
func TestReviewLegJudgesRawContentNotPostProcessed(t *testing.T) {
	logs := capturePurityLogs(t)
	const initial = "弊社はスマートハードウェアの研究開発に専念します。"
	mixed := initial + " The company focuses on smart hardware research and development and manufacturing."

	e, _ := purityEngine(t, func(kind, _ string) string {
		if kind == "review" {
			return mixed
		}
		return initial
	})
	// 清洗链自己会把尾段剥掉 ⇒ 剥后的形态是"纯日语"，若判据吃它就必然放行
	stripped := PostProcessTranslation(mixed, "ja")
	if reviewOutputRejectReason("ja", stripped) != "" {
		t.Fatalf("前置读数不对：清洗后的形态本应判纯，否则这条锁证明不了什么：%q", stripped)
	}
	if reviewOutputRejectReason("ja", mixed) == "" {
		t.Fatal("原始产物必须判不纯（判据的输入口径就是它）")
	}
	before := purityCount("ja", "review_rejected")
	if got := e.ReviewTranslation(context.Background(), "本公司专注于智能硬件的研发", initial, "ja", config.StageReview); got != "" {
		t.Fatalf("审校腿在吃清洗后的字符串（P2 错写法回来了）：%q", got)
	}
	if purityCount("ja", "review_rejected") == before {
		t.Fatal("没有拒绝读数＝这一腿根本没判，判据可能被旁路")
	}
	// 计数与日志都到位才算闭环
	if l := logs.String(); !strings.Contains(l, "latin_words") {
		t.Fatal("WARN 行必须带拉丁连跑词数读数（调阈值与分档排障都靠它）")
	}
}

// TestHandleTextKeepsInitialWhenReviewLegMixed 管道级：审校腿被丢后，客户屏幕上必须还是那份正确初翻。
//
// 这一条才是 ㊶ 的"客户视角"判据：单测只测 ReviewTranslation 回 ""，
// 而 text.go 的校对环节把 "" 当"这一轮没改"——两边都要钉住才算闭环。
func TestHandleTextKeepsInitialWhenReviewLegMixed(t *testing.T) {
	logs := capturePurityLogs(t)
	const initial = "弊社はスマートハードウェアの研究開発に専念します。"
	mixed := initial + " The company focuses on smart hardware research and development and manufacturing."

	e, prompts := purityEngine(t, func(kind, _ string) string {
		if kind == "review" {
			return mixed
		}
		return initial
	})
	before := purityCount("ja", "review_rejected")

	options := map[string]interface{}{
		"target_langs": toInterfaceLangs([]string{"ja"}),
		"mode":         "fast",
		"lang":         "zh",
	}
	res := e.HandleText(tenant.WithMode(context.Background(), "fast"), "本公司专注于智能硬件的研发", options, nil)
	if res == nil || res.Error != "" {
		t.Fatalf("主链不应整体失败（Error=%q）——② 丢弃审校不是整单失败", func() string {
			if res == nil {
				return "nil"
			}
			return res.Error
		}())
	}
	got := res.Data.Translations["ja"]
	if got == "" {
		t.Fatal("审校被丢弃后客户拿不到任何译文＝把「少润色一次」升级成了「没产物」")
	}
	if strings.Contains(got, "The company focuses") {
		t.Fatalf("㊶ 现网形态回来了（译文尾巴挂着整句回译）：%q", got)
	}
	if !strings.Contains(got, "弊社") {
		t.Fatalf("初翻没被保留（实得 %q）：② 的退路就是上一版，不能是空", got)
	}
	if purityCount("ja", "review_rejected") <= before {
		t.Fatal("管道级拒绝没有读数（校对环节可能压根没走判据）")
	}
	if p := prompts.String(); !strings.Contains(p, "review\x00") {
		t.Fatal("假上游没被审校腿拨到，本用例失去意义（管道编排变了）")
	}
	if l := logs.String(); !strings.Contains(l, "review_latin_run_in_nonlatin_target") {
		t.Fatalf("管道里没出档名：%q", l)
	}
}

// ------------------------------------------------------------
// ③ 提示词层（派生式，遍历全部语种）
// ------------------------------------------------------------

// TestTranslateInstructionForbidsSourceEchoForAllTargets ㊶③：全部目标语种×两套界面语言都必须带齐两句。
//
// 写法刻意是**派生式**（遍历 config.TranslateLangs ＋ zh ＋ 空串/陌生码两个兜底档），
// 不写语种清单：新增一个语种只要进了 TranslateLangs，漏补约束就当场红
// （AGENTS：清单式锁改派生式并钉空清单正锁）。
func TestTranslateInstructionForbidsSourceEchoForAllTargets(t *testing.T) {
	targets := append([]string{}, config.TranslateLangs...)
	targets = append(targets, "zh", "", "zz_unknown")

	echoEN := []string{"without reproducing the original text", "with no original text"}
	echoZH := []string{"不要复述原文", "不得复述原文"}
	for _, ui := range []string{"en", "zh"} {
		oneEcho := echoEN
		if ui == "zh" {
			oneEcho = echoZH
		}
		hit := 0
		for _, tg := range targets {
			instr := translateInstruction("zh", tg, ui)
			ok := false
			for _, kw := range oneEcho {
				if strings.Contains(instr, kw) {
					ok = true
					break
				}
			}
			if !ok {
				t.Fatalf("target=%q ui=%q 缺「不得复述原文」一句：%q", tg, ui, instr)
			}
			backKw := "back-translation"
			if ui == "zh" {
				backKw = "回译"
			}
			if !strings.Contains(instr, backKw) {
				t.Fatalf("target=%q ui=%q 缺「不得附加回译」一句（㊶ 现网形态就是回译）：%q", tg, ui, instr)
			}
			hit++
		}
		if hit == 0 {
			t.Fatalf("ui=%q 一个语种都没遍历到（清单空转＝锁失去射程）", ui)
		}
		t.Logf("ui=%q 覆盖 %d 个目标语种", ui, hit)
	}
	// 繁体界面走中文提示词（同一条分支判据），顺带钉一次"简/繁当一个书写体系"的口径
	if instr := translateInstruction("en", "zh_hant", "zh_hant"); !strings.Contains(instr, "繁体中文") {
		t.Fatalf("繁体界面必须走中文提示词的繁体档：%q", instr)
	}
}

// ------------------------------------------------------------
// 观测腿本体（计数器形态锁）
// ------------------------------------------------------------

// TestPuritySnapshotIsCopyAndCounts 快照必须是**拷贝**，且并发读写不许撕裂。
//
// AGENTS §一·3：把内部 map 直接交出去，采集腿 range 时业务腿正在写 ⇒
// runtime fatal error（不是 panic，recover 兜不住，进程直接挂，现网表现是偶发 502）。
// 本用例必须配 -race 跑。
func TestPuritySnapshotIsCopyAndCounts(t *testing.T) {
	// ① 拷贝语义：改返回值不许影响真账
	recordPurityAction("de", "tail_stripped")
	base := PuritySnapshot()["de|tail_stripped"]
	if base == 0 {
		t.Fatal("计数没落进账本")
	}
	dirty := PuritySnapshot()
	dirty["de|tail_stripped"] = 99999
	delete(dirty, "de|tail_stripped")
	if got := PuritySnapshot()["de|tail_stripped"]; got != base {
		t.Fatalf("快照不是拷贝（外部改动进了内部账本）：%d → %d", base, got)
	}
	// ② 空语种归成 unknown，一条腿不许静默消失
	recordPurityAction("", "review_rejected")
	if got := PuritySnapshot()["unknown|review_rejected"]; got == 0 {
		t.Fatal("语种为空的拒绝被记成静默（报表上找不到，等于没发生）")
	}
	// ③ 并发：一边记一边采（绕开连接池那类"顺带串行化"的掩护，见 §一·13 反证③）
	// ★ 两条 WaitGroup 必须分开：记账腿收完才关 stop，观测腿等 stop。
	//	若观测腿也挂在同一个 wg 上，就是「wg.Wait() 等 stop、stop 等 wg.Wait()」的自设死锁
	//	（本用例首跑真踩：测试挂到超时才暴露，而不是当场红）。
	var writers, watchers sync.WaitGroup
	// observed 是观测腿自己的读数：并发 range 的结果必须**被消费**，
	// 否则"空 body 的 range"属于跑没跑都不影响绿灯的假射程（末尾断言 >0 才是正锁）。
	var observed int64
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		watchers.Add(1)
		go func() {
			defer watchers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					snap := PuritySnapshot()
					acc := int64(len(snap))
					for _, v := range snap {
						acc += v
					}
					atomic.AddInt64(&observed, acc)
					time.Sleep(50 * time.Microsecond) // 让出 CPU，但不取消并发窗口
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for j := 0; j < 200; j++ {
				recordPurityAction("th", "tail_stripped")
			}
		}()
	}
	writers.Wait()
	close(stop)
	watchers.Wait()
	if got := PuritySnapshot()["th|tail_stripped"]; got < 1600 {
		t.Fatalf("并发记账丢数（实得 %d，应 ≥1600）", got)
	}
	if atomic.LoadInt64(&observed) == 0 {
		t.Fatal("观测腿一次都没采到值：并发窗口根本没跑起来，这一半用例是空转")
	}
}

// TestClipRunesIsRuneSafe 钉住日志样本的截断口径：**按字符**截、绝不劈开 UTF-8 序列。
//
// 这条用例的存在理由（不是给工具函数凑覆盖率）：④ 的 WARN 里带 raw 样本，旧写法
// `%.80q` 按**字节**切——一条中文译文正好被切在第 80 字节上就是半个汉字，
// slog 的 JSON handler 把它写成替换字符，现网读日志的人会以为「译文自己带了个乱码尾巴」，
// 而真相是日志层劈了一刀（与 AGENTS §三「按字节截会劈成非法 UTF-8」同族，只是落在观测面）。
// 反证：把 clipRunes 改成 `s[:n]` 字节切片，本用例的 utf8.ValidString 腿立刻红。
func TestClipRunesIsRuneSafe(t *testing.T) {
	// 81 个汉字（243 字节）截到 80 字符：结果必须是 80 个汉字 + 一个省略号，且整体合法 UTF-8
	long := strings.Repeat("译", 81)
	got := clipRunes(long, 80)
	if !utf8.ValidString(got) {
		t.Fatalf("截后不是合法 UTF-8（按字节切的形态）：%q", got)
	}
	if r := []rune(got); len(r) != 81 || r[80] != '…' {
		t.Fatalf("截后长度/收尾不对：应 80 字符＋…，实得 %d 字符、末位 %q", len(r), string(r[len(r)-1]))
	}
	if strings.Count(got, "译") != 80 {
		t.Fatalf("截取的字符数不是 80：%q", got)
	}
	// 反证方向：不超长的必须**逐字原样**返回（不补省略号，否则排障时看不出是全文还是样本）
	short := "我们需要为下周的法兰克福汽车展翻译产品手册。"
	if s := clipRunes(short, 80); s != short {
		t.Fatalf("未超长却被改动：\n实得 %q\n原文 %q", s, short)
	}
	// 边界：恰好 n 个字符不截（省略号只属于"真被截过"的那一档）
	if s := clipRunes(strings.Repeat("译", 80), 80); strings.HasSuffix(s, "…") {
		t.Fatal("恰好等长却补了省略号：读日志的人会以为后面还有内容")
	}
}
