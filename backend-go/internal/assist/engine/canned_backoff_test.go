// ============ canned_backoff_test.go · 职责说明 ============
// 锁 0AR 第 4 波 ㊷ 那三腿：**退避窗口**／**冷语种计数与 /health 读数**／**canned 专用快模型**。
//
// 现网实证（台账 §七 ㊷，两次独立复现）：泰文这一档走的是闭环——
// 同步腿必超时（GLM-Z1 是推理型模型，8 秒预算压根不够）⇒ 后台补一枪 ⇒ 那一稿被出栈闸**正确**拒掉
// ⇒ 不写缓存也不写负缓存 ⇒ 下一位访客从零再来。读数是 `assist.db` 的 mtime 停在两天前、
// 而日志里成对出现 `canned_sync_timeout` ＋ `canned_bg_gated`：**每个 th 访客白等 8 秒拿一屏中文**，
// 全程零告警面读数。
//
// ★ 本文件最重要的一条判据不是"窗口开了"，而是**窗口内一条上游都不拨**：
// 台账里那句"抬超时预算到 25 秒"看起来也是修法，实际只把白等拉长到 25 秒，
// 那一稿照样过不了闸（上游调用次数纹丝不动）。所以每条用例都数 hits，不数耗时。
// ⚠️ 唯一的例外是 TestCannedColdLangGreetDoesNotBlockOnUpstream 的第三腿：
// 那一腿要钉的是"零拨号之外还没白等"，光数次数抓不到"押住了却绕去等另一把锁"那一族。
//
// 时序纪律同 localize_async_test.go：不用 sleep 赌 goroutine，等收尾一律走 cannedBgWg（waitBg）。
// =============================================
package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/assist/llm"
)

// healthInt 从 CannedHealth() 取一个整档读数（map[string]any 里数字的型只有一档，取不到即判红：
// 读数面悄悄换型＝ /health 那一段 JSON 变了形态，运维的 jq 判据会静默拿到 null）。
func healthInt(t *testing.T, h map[string]any, field string) int {
	t.Helper()
	v, ok := h[field]
	if !ok {
		t.Fatalf("/health 的 canned 段没有 %q 字段（对外排障契约不许悄悄减字段）", field)
	}
	n, ok := v.(int)
	if !ok {
		t.Fatalf("字段 %q 不是整数档：%T", field, v)
	}
	return n
}

// healthReasons 取按档明细（reason → 次数）。
func healthReasons(t *testing.T, h map[string]any) map[string]int {
	t.Helper()
	m, ok := h["by_reason"].(map[string]int)
	if !ok {
		t.Fatalf("by_reason 档型变了：%T（/health 出栈形态是对外契约）", h["by_reason"])
	}
	return m
}

// TestCannedBgUpstreamFailureOpensBackoff 后台腿**上游失败**⇒ 开窗口 ⇒ 下一位访客不再拨上游。
//
// 三段各钉一件事：
//
//	① 两条腿都真拨过（hits==2）：退避不能是"压根没打"的遮羞布（那样计数永远是 0，
//	   而现网要区分的是"打了但失败"与"没打"）；
//	② /health 立刻看得见 backoff ＋ 两档明细（旧形态这里只有两行 WARN，没有任何读数面）；
//	③ 第二次 greet **一条上游都不再拨**、且立刻返回（这才是「首屏不再白等」的等价判据）。
//
// 反证：摘掉 `runCannedBackground` 里那两处 markCannedBackoff ⇒ ②③ 一起红（窗口永远不开）；
// 把退避闸挪到缓存读之后（也就是"命中缓存也押掉"）⇒ 本文件另一条用例（不押缓存读）红。
func TestCannedBgUpstreamFailureOpensBackoff(t *testing.T) {
	ctx := context.Background()
	url, hits, _ := scriptedLLM(t, llmCall{code: 500})
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: url, APIKey: "k", Model: "m"}}, 5)

	src := "有什么想了解的？我可以帮你选功能。"
	if got := e.LocalizeGreeting(ctx, src, "th"); got != src {
		t.Fatalf("上游失败必须原样出中文：%q", got)
	}
	waitBg(t, e, 10*time.Second)
	if n := hits.Load(); n != 2 {
		t.Fatalf("同步腿＋后台腿应各拨一次，实际 %d 次", n)
	}

	h := e.CannedHealth()
	if h["status"] != cannedHealthBackoff {
		t.Fatalf("后台失败后 /health 仍是 %v ⇒ 节流这件事在观测面上不存在", h["status"])
	}
	if healthInt(t, h, "backoff_active") != 1 {
		t.Fatalf("活跃窗口数不对：%v", h["backoff_active"])
	}
	rs := healthReasons(t, h)
	if rs[cannedSyncUpstream] < 1 || rs[cannedBgUpstream] < 1 {
		t.Fatalf("两档失败没有被分别数到（现网排障要分清该查上游还是该调预算）：%v", rs)
	}
	if h["prompt_rev"] != cannedPromptRev {
		t.Fatalf("/health 没带当前口径档：%v（排障先对这一档再对缓存键头）", h["prompt_rev"])
	}

	// ③ 窗口内：不再拨上游、立刻返回中文
	before := hits.Load()
	t0 := time.Now()
	if got := e.LocalizeGreeting(ctx, src, "th"); got != src {
		t.Fatalf("退避期间出的是非中文稿（那是编造）：%q", got)
	}
	if n := hits.Load(); n != before {
		t.Fatalf("退避窗口内又拨了上游（%d→%d）⇒ 访客那 8 秒白等照旧", before, n)
	}
	if el := time.Since(t0); el > time.Second {
		t.Fatalf("退避那一枪等了 %s 才返回：窗口没生效或被别处阻塞", el)
	}
	if healthReasons(t, e.CannedHealth())[cannedBgBackoff] < 1 {
		t.Fatal("被窗口挡住的这一次没被数到（运维分不清「在退避」与「这一枪失败了」）")
	}
}

// TestCannedBgGatedProductOpensBackoff 现网 th 闭环的**终点那一档**：后台那稿被出栈闸拒掉也要开窗口。
//
// 这一条是本批最容易漏的分支——闸门拒绝在旧代码里是"判据正常工作"，
// 但在这条闭环上它是"每次都判对、每次都白烧"：上游被打了两枪、缓存一行不写、访客白等。
// 拒绝档与失败档在这里同权重，因为**对访客的代价完全相同**。
func TestCannedBgGatedProductOpensBackoff(t *testing.T) {
	ctx := context.Background()
	bad := "สวัสดี 积分 recharge" // 泰文里留着没翻的中文词 ⇒ 同一把尺子（hanResidueRuns）判残
	st := newGateStub(t, blockCalls(1), contents(bad))
	e := st.engine(t, "1") // 同步预算 1 秒，上游第一枪挂住

	src := "有什么想了解的？我可以帮你选功能。"
	if got := e.LocalizeGreeting(ctx, src, "th"); got != src {
		t.Fatalf("同步超时必须出中文：%q", got)
	}
	waitBg(t, e, 10*time.Second)
	assertNoFlightLeak(t, e)
	if cached := e.db.GetConfig("i18n:welcome:th", ""); cached != "" {
		t.Fatalf("被闸拒掉的稿子落了库：%q", firstLine(cached))
	}
	rs := healthReasons(t, e.CannedHealth())
	if rs[cannedBgGated] < 1 {
		t.Fatalf("后台那稿被拒这件事没有读数：%v", rs)
	}
	if e.CannedHealth()["status"] != cannedHealthBackoff {
		t.Fatalf("闸门拒绝没开退避窗口（现网 th 就是停在这一档：两天零落库、每个访客各烧两枪）：%v", e.CannedHealth()["status"])
	}
	before := st.count()
	if got := e.LocalizeGreeting(ctx, src, "th"); got != src || st.count() != before {
		t.Fatalf("窗口内又打上游或出了非中文：got=%q 上游 %d→%d", got, before, st.count())
	}
}

// TestCannedBackoffWindowFollowsFingerprint 窗口的键＝(kind,语种,指纹)：
// **运营改了原文，新那一档必须立刻能重打**，不许替一次改动请假 5 分钟。
//
// 为什么单独钉这条而不是靠 TTL 语义：如果键只用 (kind,lang)，运营改完欢迎词后的第一批访客
// 仍然吃旧那一枪的退避——而旧那一枪的失败原因（旧原文翻不动）已经不存在了。
// 那一形态在日志里完全不可见（窗口那一行写的是"在退避"，看不出来退的是哪一份原文）。
//
// ⚠️ 并发去重那一半（同一键并发只允许一条腿）不受影响，由 TestCannedConcurrentMissDialsUpstreamOnce 钉。
func TestCannedBackoffWindowFollowsFingerprint(t *testing.T) {
	ctx := context.Background()
	st := newSeqStub(t, "What can I help you with today?")
	e := st.engine(t)
	oldSrc := "有什么想了解的？"
	newSrc := "有什么想了解的？随时问我。"
	fpOld := srcFingerprint(oldSrc + "\x00" + localizeContract("th", srcTopicsOf(oldSrc)))
	e.markCannedBackoff(cannedFlightKey("welcome", "th", fpOld))

	if got := e.LocalizeGreeting(ctx, oldSrc, "th"); got != oldSrc || st.count() != 0 {
		t.Fatalf("旧原文那一档应被窗口押住（不打上游、出中文）：got=%q 上游 %d 次", got, st.count())
	}
	got := e.LocalizeGreeting(ctx, newSrc, "th")
	if st.count() == 0 {
		t.Fatalf("改了原文还在吃旧窗口 ⇒ 运营那一次改动等于把首屏锁死 5 分钟")
	}
	if !strings.Contains(got, "help you") {
		t.Fatalf("新那一档没走通：got=%q（新原文必须从零重翻）", got)
	}
}

// TestCannedBackoffClampsAndStateTransitions 窗口的三道状态机判据：只有"开／到期就地删／成功清"三态，
// 外加长度硬夹（下限必须比两条预算之和大，否则窗口等于没有；上限不许长到"这一语种今天不再试了"）。
//
// 反证：把 `cannedBgInBackoff` 的到期就地删摘掉 ⇒ 第三段红（表会常驻增长）；
// 把 `clearCannedBackoff` 那一腿摘掉 ⇒ 最后一段红（后台成功后仍被按坏了押着＝把修法做成新故障）。
func TestCannedBackoffClampsAndStateTransitions(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	if d := e.CannedBackoff(); d != 300*time.Second {
		t.Fatalf("默认窗口长度 %s（应为 5 分钟）", d)
	}
	_ = e.db.SetConfig(cfgCannedBackoffSec, "5") // 比两条预算之和还短＝没有窗口 ⇒ 夹到下限
	if d := e.CannedBackoff(); d != time.Duration(cannedBackoffMinSec)*time.Second {
		t.Fatalf("下限没夹住：%s", d)
	}
	_ = e.db.SetConfig(cfgCannedBackoffSec, "99999999")
	if d := e.CannedBackoff(); d != time.Duration(cannedBackoffMaxSec)*time.Second {
		t.Fatalf("上限没夹住：%s（模型侧恢复是分钟级的，不该由我们代它请假一整天）", d)
	}
	_ = e.db.SetConfig(cfgCannedBackoffSec, "not-a-number")
	if d := e.CannedBackoff(); d != 300*time.Second {
		t.Fatalf("档位脏值没回落默认：%s", d)
	}

	key := cannedFlightKey("welcome", "th", "aaaaaaaaaaaa")
	if e.cannedBgInBackoff(key) {
		t.Fatal("没开过窗却被判成在窗口内")
	}
	e.markCannedBackoff(key)
	if !e.cannedBgInBackoff(key) || e.cannedBackoffRemaining(key) <= 0 {
		t.Fatal("开完窗口读不到（日志里 until_in_sec 会恒 0，运维无从判断还剩多久）")
	}
	// 到期就地删：直接把那一格拧到过去（不靠 sleep 赌时间）
	e.cannedColdMu.Lock()
	e.cannedBackoff[key] = time.Now().Add(-time.Minute)
	e.cannedColdMu.Unlock()
	if e.cannedBgInBackoff(key) {
		t.Fatal("过期窗口仍被当成活跃 ⇒ 这一 (kind,语种) 永久降级")
	}
	e.cannedColdMu.Lock()
	n := len(e.cannedBackoff)
	e.cannedColdMu.Unlock()
	if n != 0 {
		t.Fatalf("过期条目没就地删除（剩 %d 格）⇒ 这张表会长成需要后台清扫的常驻表", n)
	}

	// 计数与状态词的三档：ok → cold → backoff
	e2 := newTestEngine(t)
	if got := e2.CannedHealth()["status"]; got != cannedHealthOK {
		t.Fatalf("干净引擎的 /health 不是 ok：%v", got)
	}
	e2.noteCannedCold(ctx, "welcome", "th", cannedSyncTimeout)
	if got := e2.CannedHealth()["status"]; got != cannedHealthCold {
		t.Fatalf("有失败记录却仍报 ok：%v（cold 与 backoff 的运维动作不同，不许合并）", got)
	}
	if healthInt(t, e2.CannedHealth(), "fail_total") != 1 {
		t.Fatalf("计数没涨：%v", e2.CannedHealth()["fail_total"])
	}
	e2.markCannedBackoff(cannedFlightKey("welcome", "th", "bbbbbbbbbbbb"))
	if got := e2.CannedHealth()["status"]; got != cannedHealthBackoff {
		t.Fatalf("有活跃窗口却报 %v", got)
	}
	e2.clearCannedBackoff(cannedFlightKey("welcome", "th", "bbbbbbbbbbbb"))
	if got := e2.CannedHealth()["status"]; got != cannedHealthCold {
		t.Fatalf("清窗后应回 cold（失败记录还在、但此刻没在节流）：%v", got)
	}
}

// TestCannedColdLangGreetDoesNotBlockOnUpstream ㊷ 那句验收判据「首屏不再白等」的字面对应物
// （★ 0AR 第 4 波收尾；本文件其余用例都在数"上游被打了几次"，这一条额外钉**耗时**与**三个出口**）。
//
// 现网 th 那一档的代价不是"翻不出来"，而是**每个访客各白等 8 秒拿一屏中文**：
// 同步腿必超时（推理型模型）⇒ 后台补一枪 ⇒ 那一稿被出栈闸正确拒掉 ⇒ 不写缓存也不写负缓存 ⇒ 下一位从零再来。
// 三条出路都在"这一枪压根不需要上游"那一侧，缺一都不是修好：
//
//	① 运营手工定的那一行（`!manual`）——人工行本来就是终稿，节流与它无关（现网 th 正是靠这一档救的）；
//	② 库里有一行**现算指纹等值**的机器译文——退避只押**上游调用**，押到缓存读上就把修法做成了新故障
//	   （明明有可用译文，却给每个访客出中文）；
//	③ 既没有行、又在窗口内 ⇒ 立刻出中文，**且墙钟耗时远小于一档同步预算**
//	   （只数上游次数抓不到"押住了但绕去等另一把锁"这一族，所以这条必须问时间）；
//	④ 反向对照：**同一 (kind,语种,指纹) 档**把窗口摘掉 ⇒ 必须真拨那一枪并拿到译文。
//	   ⚠️ 少了这条，①②③ 的"零拨号"完全可以是"这一路压根不通"——负向锁必配正向对照。
//
// 反证（三条各自点名，见 /tmp 的变异批）：
//
//	摘掉 localize 里那道窗口闸 ⇒ ③ 红（拨号了，而且等满挂住的那一档）；
//	把窗口闸挪到缓存读**之前** ⇒ ② 红（有译文却出中文）；
//	把 `!manual` 那一腿的放行摘掉 ⇒ ① 红（人工稿投不出去，且白打上游）。
func TestCannedColdLangGreetDoesNotBlockOnUpstream(t *testing.T) {
	ctx := context.Background()
	src := cannedTestSource
	chipsCSV := "怎么上传文件翻译？,积分怎么收费？"

	// 假上游用**会挂住**的那一款：本用例有一条判据问的是耗时，
	// 瞬间返回的 seqStub 永远造不出"白等 8 秒"这个中间态（gateStub 的存在理由，见 localize_async_test.go）。
	st := newGateStub(t, blockCalls(1), withSlow(5*time.Second),
		contents("Hello, this is LangCross. Credits can be topped up anytime."))
	e := st.engine(t, "8") // 同步预算 8 秒＝现网默认档；③ 的墙钟判据拿它做对照

	// 现算指纹：清理腿/写侧/读侧共用同一对函数，这里也必须用同一对（否则②那一行永远命不中）
	fpTh := srcFingerprint(src + "\x00" + localizeContract("th", srcTopicsOf(src)))
	fpJa := srcFingerprint(src + "\x00" + localizeContract("ja", srcTopicsOf(src)))
	fpEn := srcFingerprint(src + "\x00" + localizeContract("en", srcTopicsOf(src)))

	// ① 人工行＋窗口同时在场：人工稿必须照样投出去，一条上游都不拨
	manualHead := "000000000000" + manualMark // 管理台落的就是「指纹＋标记」，判据只看后缀（见 splitCachedTranslation）
	manualWelcome := "สวัสดี นี่คือ LangCross — เติมเครดิตได้ทุกเมื่อ"
	manualChips := "อัปโหลดไฟล์เพื่อแปลได้อย่างไร\nเครดิตคิดค่าธรรมเนียมอย่างไร"
	seedCannedRow(t, e, "i18n:welcome:th", manualHead, manualWelcome)
	seedCannedRow(t, e, "i18n:chips:th", manualHead, manualChips)
	e.markCannedBackoff(cannedFlightKey("welcome", "th", fpTh))
	e.markCannedBackoff(cannedFlightKey("chips", "th", srcFingerprint(strings.ReplaceAll(chipsCSV, ",", "\n")+"\x00"+
		localizeContract("th", srcTopicsOf(strings.ReplaceAll(chipsCSV, ",", "\n"))))))
	if got := e.LocalizeGreeting(ctx, src, "th"); got != manualWelcome {
		t.Fatalf("运营手工定的欢迎词在退避期间没投出去：%q ⇒ 人工档与节流的先后关系写错了", got)
	}
	if got := e.LocalizeChips(ctx, chipsCSV, "th"); got != strings.ReplaceAll(manualChips, "\n", ",") {
		t.Fatalf("运营手工定的 chips 在退避期间没投出去：%q", got)
	}
	if n := st.count(); n != 0 {
		t.Fatalf("人工稿那一档还拨了 %d 次上游 ⇒ 首屏白等的正是这一类多余往返（而且人工行会被模型稿覆盖）", n)
	}

	// ② 机器译文（指纹等值）＋窗口：退避不许押缓存读
	jaBody := "こんにちは、能言 AI アシスタントです。クレジットはいつでもチャージできます。"
	seedCannedRow(t, e, "i18n:welcome:ja", fpJa, jaBody)
	e.markCannedBackoff(cannedFlightKey("welcome", "ja", fpJa))
	if got := e.LocalizeGreeting(ctx, src, "ja"); got != jaBody {
		t.Fatalf("有可用译文却出了别的（多半是中文原文）：%q ⇒ 退避闸挪到缓存读之前了", got)
	}
	if n := st.count(); n != 0 {
		t.Fatalf("命中缓存仍拨上游（%d 次）⇒ 「节流」被写成了「重翻」", n)
	}

	// ③ 无行＋窗口：立刻出中文，且**没等预算**
	e.markCannedBackoff(cannedFlightKey("welcome", "en", fpEn))
	t0 := time.Now()
	if got := e.LocalizeGreeting(ctx, src, "en"); got != src {
		t.Fatalf("退避期间必须原样出中文：%q", got)
	}
	if el := time.Since(t0); el > 2*time.Second {
		t.Fatalf("窗口内那一枪等了 %s 才返回（同步预算 8s）⇒ 上游次数是 0，但访客照样白等", el)
	}
	if n := st.count(); n != 0 {
		t.Fatalf("窗口内又拨了上游（累计 %d 次）⇒ 「首屏不再白等」根本没成立", n)
	}
	if healthReasons(t, e.CannedHealth())[cannedBgBackoff] < 1 {
		t.Fatal("被窗口挡住的这一次没有读数：运维分不清「在退避」与「这一枪失败了」")
	}
	assertNoFlightLeak(t, e)

	// ④ 反向对照：同一档（en／同一句原文／同一份口径）把窗口摘掉 ⇒ 必须真拨并拿到译文
	st2 := newSeqStub(t, "Hello, this is LangCross. Credits can be topped up anytime.")
	e2 := st2.engine(t)
	if got := e2.LocalizeGreeting(ctx, src, "en"); !strings.Contains(got, "LangCross") {
		t.Fatalf("没有窗口时这一档也出不了译文：%q ⇒ 上面三条「零拨号」是链路坏了，不是节流生效", got)
	}
	if n := st2.count(); n != 1 {
		t.Fatalf("该拨的那一枪没拨（或重复拨了）：%d 次", n)
	}
}

// TestCannedModelOverrideFallsBack canned 专用快模型的三档回落（㊷①）逐档点名。
//
// 为什么这一条必须钉"回落"而不是只钉"生效"：现网那几台 base/key 只存在于进程侧 env，
// configs 里取不到——如果这时候把 `canned_llm_model` 当成"照办"，
// cannedClient 会拿着空 base 构造出一个**永远失败**的 client，
// 首屏从"慢"变成"这一路彻底不通"，而且日志里只有一行回落 WARN（生效面看不出区别）。
//
// 三档：① 键为空 ⇒ 逐字等于今天（同一个 client）；② 有模型名但 base/key 缺 ⇒ 回落；
// ③ 齐 ⇒ 第一档真用那把模型（请求体读数，不看函数名）；最后回到 ① 时必须把缓存丢掉。
func TestCannedModelOverrideFallsBack(t *testing.T) {
	ctx := context.Background()
	src := "有什么想了解的？我可以帮你选功能。"

	// ① 没配专用模型：cannedClient 与 ensureLLM 是**同一个对象**（连指针都比，
	//    因为"重新 new 一个等价的"会让管理台改一次配置就多一次上游重拨风暴）
	st1 := newSeqStub(t, "What can I help you with today?")
	e1 := st1.engine(t)
	if c := e1.cannedClient(ctx); c != e1.ensureLLM(ctx) {
		t.Fatal("没配 canned_llm_model 却没有复用常规 client ⇒ 这一档的默认形态变了")
	}
	if e1.cannedModelNameForTest() != "" {
		t.Fatal("没配专用模型却留下了指纹（回落腿没清缓存）")
	}
	if got := e1.LocalizeGreeting(ctx, src, "en"); !strings.Contains(got, "help you") {
		t.Fatalf("默认档没走通（这一档就是今天的行为，不许变）：%q", got)
	}
	if body := st1.body(0); !strings.Contains(body, `"model":"m"`) {
		t.Fatalf("默认档发出去的模型名不对：%s", firstLine(body))
	}

	// ② 只配模型名、base/key 不在 configs（现网 env 那几台的形态）⇒ 必须回落，且真的译得出
	st2 := newSeqStub(t, "What can I help you with today?")
	e2 := st2.engine(t)
	_ = e2.db.SetConfig(cfgCannedModel, "fast-m")
	if c := e2.cannedClient(ctx); c != e2.ensureLLM(ctx) {
		t.Fatal("base/key 取不到却没回落 ⇒ 拿空地址构造 client，这一路从此恒失败")
	}
	if got := e2.LocalizeGreeting(ctx, src, "en"); !strings.Contains(got, "help you") {
		t.Fatalf("回落脚把首屏打挂了：%q", got)
	}
	if body := st2.body(0); strings.Contains(body, "fast-m") {
		t.Fatal("回落了却还是用专用模型名发的请求（档位与实际发出的那一枪不一致）")
	}

	// ③ 三档齐 ⇒ 第一枪真打那把快模型
	st3 := newSeqStub(t, "What can I help you with today?")
	e3 := st3.engine(t)
	_ = e3.db.SetConfig("llm_base_url", st3.url)
	_ = e3.db.SetConfig("llm_api_key", "k")
	_ = e3.db.SetConfig("llm_model", "m")
	_ = e3.db.SetConfig(cfgCannedModel, "fast-m")
	if got := e3.LocalizeGreeting(ctx, src, "en"); !strings.Contains(got, "help you") {
		t.Fatalf("专用模型档没走通：%q", got)
	}
	if body := st3.body(0); !strings.Contains(body, "fast-m") {
		t.Fatalf("配了 canned_llm_model 却没用它发第一枪（请求体里没这个名字）：%s", firstLine(body))
	}
	if e3.cannedModelNameForTest() == "" {
		t.Fatal("专用 client 没建指纹 ⇒ 每次 greet 都重建 client")
	}
	// 运营把这一档清空 ⇒ 必须回到常规 client（旧缓存不许继续押着快模型）
	_ = e3.db.SetConfig(cfgCannedModel, "")
	if c := e3.cannedClient(ctx); c != e3.ensureLLM(ctx) {
		t.Fatal("清掉了 canned_llm_model，canned 这一路还在吃旧 client")
	}
	if e3.cannedModelNameForTest() != "" {
		t.Fatal("清档后指纹没清 ⇒ 下一次填回同一个模型名会被当成「没变化」复用")
	}
}

// TestCannedColdCountersBounded 计数字典必须有容量上界（观测面宁可读数粗一点，也不许长成内存泄漏）。
//
// 现网这条链的 key 基数本来有界（kind 两类 × 12 语种 × 若干分档），
// 但 `noteCannedCold` 在**每一次失败**上都会被调用，而将来若有把指纹塞进 key 的改动
// （指纹随原文变）就会让这一张表随原文条数无界增长。overflow 那一格是它唯一的露面机会。
func TestCannedColdCountersBounded(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < cannedColdMaxKeys+40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.noteCannedCold(ctx, "welcome", "en", "reason-"+strings.Repeat("x", i%7)+string(rune('a'+i%26)))
		}(i)
	}
	wg.Wait()
	h := e.CannedHealth()
	if healthInt(t, h, "fail_total") != cannedColdMaxKeys+40 {
		t.Fatalf("总数没记全：%v（总数是这条链唯一的「烧了多少枪」读数）", h["fail_total"])
	}
	if v, _ := h["overflow"].(bool); !v {
		t.Fatal("字典到顶却没出 overflow 读数 ⇒ /health 看起来「明细齐全」，实际被截断")
	}
	if len(healthReasons(t, h)) > cannedColdMaxKeys {
		t.Fatalf("明细字典越过长到了 %d 格（上限 %d）", len(healthReasons(t, h)), cannedColdMaxKeys)
	}
}
