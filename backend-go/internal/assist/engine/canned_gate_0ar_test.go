// ============ canned_gate_0ar_test.go · 职责说明 ============
// 锁 0AR 第 4 波「出栈闸从三条补到六道拒绝＋修正腿＋纯观测档」（① 先补判据那一步）。
//
// 本文件的靶子不是"闸门会不会拒绝"（096x 那批已经钉过残片与凭空品牌名两档），而是
// 现网库里那三条**指纹匹配、正在被 HIT** 的坏行为什么一直没被拦住（台账 §六 ㉞）：
//   - `i18n:chips:ar`：4 行正确译文 ＋ 第 5 行 `---`（模型照抄输入围栏）。
//     旧代码是「命中缓存 ⇒ 直接返回 ⇒ 回到 LocalizeChips 才按条数丢弃 ⇒ 丢弃分支既不记日志也不作废那一行」，
//     三条凑成**自锁**：坏行用不上、不作废、不出声 ⇒ 那四个中文 chips 会一直投给每个阿语访客。
//   - `i18n:welcome:th`：留着没替换的字面量 `⟨BRAND⟩`（内部记号外露）。
//   - `i18n:welcome:ko`：品牌名连括号被改写成 `⟨LangCross⟩`（还原步命不中）。
//
// ★ 与 localize_test.go／canned_guard_test.go 的分工：那两处测**判据本身**，
// 这里测的是「判据生效的**位置**」——写缓存之前、读缓存之后、回写那一行之后。
// 位置错了就等于没修（现网那三条坏行全是"判据在下游、上游已经落库"的形态）。
// =============================================
package engine

import (
	"context"
	"strings"
	"testing"
)

// gate0arChipsCSV 现网那四条 chips（提到「积分」、**没提**品牌名 ⇒ 口径走"禁止注入"那一档）。
const gate0arChipsCSV = "怎么上传文件翻译？,积分怎么收费？,企业术语库怎么建？,支持哪些格式？"

// gate0arChipsGood 同一句源文的**合格**四行英文（反向对照用：闸门必须是双向的）。
const gate0arChipsGood = "How do I upload a file for translation?\nHow are credits charged?\n" +
	"How do I build a company glossary?\nWhich formats are supported?"

// TestCannedRejectsSeparatorResidue 独立成行的分隔线残渣：两道判据都要拦得住，且**按各自的射程**分档。
//
// 为什么一条现网坏行要钉两个分档：`---` 这一行同时踩了「条数不符」与「装饰线残渣」两条判据，
// 而**条数先判**是刻意的（多出来的那一行十有八九就是它，按条数拦比逐形认残渣更省事）。
// 如果将来有人把两条判据的顺序换掉，这一用例的分档读数当场红——那不是我要求顺序神圣，
// 而是 `reason` 是对外排障契约，运维按它决定「该改提示词」还是「该加判据」。
//
// 反证：摘掉 sepLinePat 那条 ⇒ 第二段红；摘掉 wantLines ⇒ 第一段红（现网那条坏行就是这么住下来的）。
func TestCannedRejectsSeparatorResidue(t *testing.T) {
	src := strings.ReplaceAll(gate0arChipsCSV, ",", "\n")
	topics := srcTopicsOf(src)
	if topics.brand {
		t.Fatal("用例前提没了：这四条 chips 不该被判成「提到了品牌名」")
	}
	withSep := gate0arChipsGood + "\n---"

	// ① chips 的形状（带条数契约）：先按条数拦下来
	if _, reason, detail, bad := cannedOutboundReject("en", src, withSep, topics, 4); !bad || reason != cannedRejectLineCount {
		t.Fatalf("五条 chips 未被条数判据拦下：bad=%v reason=%q", bad, reason)
	} else if !strings.Contains(detail, "want=4") {
		t.Fatalf("条数判据的 detail 没带上 want（现网排障只能看到「行数不对」）：%q", detail)
	}

	// ② 单行文本（welcome 那一档 wantLines=0）：装饰线那一档必须自己接得住
	oneSrc := "支持哪些格式？"
	if _, reason, detail, bad := cannedOutboundReject("en", oneSrc, "Which formats are supported?\n---",
		srcTopicsOf(oneSrc), 0); !bad || reason != cannedRejectSeparator {
		t.Fatalf("独立成行的 `---` 未被分隔线判据拦下：bad=%v reason=%q", bad, reason)
	} else if detail != "---" {
		t.Fatalf("分隔线判据没把命中的那一行交出来：%q", detail)
	}

	// 反向对照：合格四行必须整串放行（final 等于原稿、零 reason）
	final, reason, detail, bad := cannedOutboundReject("en", src, gate0arChipsGood, topics, 4)
	if bad || reason != "" || detail != "" || final != gate0arChipsGood {
		t.Fatalf("干净四行被误杀：bad=%v reason=%q detail=%q", bad, reason, detail)
	}
	// ⚠️ 正文里的 `---` 不算装饰线（Markdown 式写法可能在句中出现），只有**整行**才算：
	// 这一条不是洁癖，是把"误拒＝整段回中文"的半径钉住。
	if _, reason2, _, bad2 := cannedOutboundReject("en", oneSrc, "A — B — C: supported", srcTopicsOf(oneSrc), 0); bad2 {
		t.Fatalf("行内破折号被判成装饰线残渣（误拒会把首屏退回中文）：reason=%q", reason2)
	}
}

// TestCannedRejectsPlaceholderResidue 内部记号外露三族（现网 th 那一行就是第一族）一律拒绝。
//
// 三族的共同点：**这些串是我们自己塞进提示词的**，客户屏幕上出现任何一种都是机制外露。
// 反向对照钉的是「不许把正常尖括号当占位符」——`<html>` 与数学式在客户正文里是真内容，
// 误拒的代价同样是整段回中文（本文件每一条反向钉的都是同一件事：闸门必须是双向的）。
func TestCannedRejectsPlaceholderResidue(t *testing.T) {
	src := "有什么可以帮你的？"
	topics := srcTopicsOf(src)
	for _, c := range []struct{ name, out, wantDetail string }{
		{"原样占位符", "Hello, ⟦BRAND⟧ here", brandToken},
		{"被换成尖括号的占位符（现网 th）", "สวัสดี ⟨BRAND⟩", "⟨BRAND⟩"},
		{"模板双花括号", "Hello {{user_name}}", "{{user_name}}"},
		{"大写标识符尖括号", "Hello <API_KEY>", "<API_KEY>"},
	} {
		_, reason, detail, bad := cannedOutboundReject("en", src, c.out, topics, 0)
		if !bad || reason != cannedRejectPlaceholder {
			t.Fatalf("%s 未被内部记号判据拦下：bad=%v reason=%q out=%q", c.name, bad, reason, c.out)
		}
		if detail == "" || !strings.Contains(c.out, detail) {
			t.Fatalf("%s 的 detail 没回命中的那一串（排障要看字形，不能只给分档名）：detail=%q", c.name, detail)
		}
	}
	// 反向对照：客户正文里的普通尖括号与小写标签一律放行
	for _, ok := range []string{"<html> is supported", "a < b and c > d", "See the docs (v2)"} {
		if _, reason, _, bad := cannedOutboundReject("en", src, ok, topics, 0); bad {
			t.Fatalf("正常正文被内部记号判据误杀：%q ⇒ reason=%q", ok, reason)
		}
	}
}

// TestLocalizeChipsDropsStaleSeparatorRowAndRetries 读侧那道闸的主断言（现网 ar 那一行的病根本体）。
//
// 判据不是"这一屏别投坏行"（那是 LocalizeChips 下游原来就有的条数校验，它一直生效着），
// 而是**坏行必须当场作废并立刻重走一次**：只加日志不作废，下一位访客读的还是同一行。
// 三段判据缺一即未修好：
//
//	① 访客拿到的是重翻后的合格稿（不是中文原文）；
//	② 库里那一行被换成了新稿（旧行不作废＝这一腿白跑）；
//	③ 上游只被多拨**一次**（作废之后是同一次调用里重翻，不是"再放一次请求"）。
//
// 反证：摘掉读侧闸门 ⇒ ① 红（返回的是中文原文）、② 红（坏行原样躺着）；
// 把 `DeleteConfig` 换成"改写指纹头"⇒ ① 仍然红（后台腿的 `head != fp` 过期判定会永久拒写）。
//
// ★ 两档坏行各自点名（只测 `---` 那一档，读侧的 `wantLines` 传没传完全无感——
// 分隔线判据不看条数也会把它拦下）。「多一行正常英文」那一档只有条数契约抓得到，
// 这一条子用例就是 `wantLines` 这个参数本身的射程证明。
func TestLocalizeChipsDropsStaleSeparatorRowAndRetries(t *testing.T) {
	ctx := context.Background()
	src := strings.ReplaceAll(gate0arChipsCSV, ",", "\n")
	fp := srcFingerprint(src + "\x00" + localizeContract("en", srcTopicsOf(src)))

	for _, stale := range []struct{ name, body string }{
		{"现网 ar 那一行：尾部装饰线", gate0arChipsGood + "\n---"},
		{"第五条正常英文（只有条数契约抓得到）", gate0arChipsGood + "\nContact support anytime."},
	} {
		st := newSeqStub(t, gate0arChipsGood)
		e := st.engine(t)
		seedCannedRow(t, e, "i18n:chips:en", fp, stale.body)

		got := e.LocalizeChips(ctx, gate0arChipsCSV, "en")
		if want := strings.ReplaceAll(gate0arChipsGood, "\n", ","); got != want {
			t.Fatalf("%s：坏缓存行没被作废重翻（访客仍在看旧形态/中文）\n got=%q\nwant=%q", stale.name, got, want)
		}
		cached := e.db.GetConfig("i18n:chips:en", "")
		if !strings.HasPrefix(cached, fp+"\n") || strings.Contains(cached, "Contact support") || strings.Contains(cached, "---") {
			t.Fatalf("%s：库里那一行不是「同一把指纹＋干净正文」：%q", stale.name, firstLine(cached))
		}
		if st.count() != 1 {
			t.Fatalf("%s：重翻只该拨一次上游，实际 %d 次", stale.name, st.count())
		}
		// 第二次必须走"已修好的那一行"：一条上游都不再拨
		if _ = e.LocalizeChips(ctx, gate0arChipsCSV, "en"); st.count() != 1 {
			t.Fatalf("%s：修好的行没被复用（第二次又拨了上游，现在 %d 次）", stale.name, st.count())
		}
	}
}

// TestLocalizeChipsLineCountCheckedBeforeCacheWrite 条数契约必须**在 SetConfig 之前**（本批第 ② 步）。
//
// 旧形态是「上游回什么就落什么、回到 LocalizeChips 才按条数丢弃」⇒ 五行的坏稿常驻库里，
// 而每一次 greet 都命中它（指纹是对的，所以永不过期）。判据前移之后：坏稿连一次都不进库。
//
// 反证：把 localize 写侧那一次 `cannedOutboundReject` 的 wantLines 传成 0（只判残渣不判条数）
// ⇒ **第二段**红（库里多出 5 行）。⚠️ 第一段（`---` 那一档）在这一反证下**不会红**——
// 分隔线判据不看条数也拦得住它，所以两档都要压：只压前者会让 `wantLines` 变成一条无人看守的参数。
func TestLocalizeChipsLineCountCheckedBeforeCacheWrite(t *testing.T) {
	ctx := context.Background()
	for _, bad := range []struct{ name, body string }{
		{"尾部装饰线（现网 ar 形态）", gate0arChipsGood + "\n---"},
		{"多一条正常英文", gate0arChipsGood + "\nContact support anytime."},
	} {
		st := newSeqStub(t, bad.body)
		e := st.engine(t)
		if got := e.LocalizeChips(ctx, gate0arChipsCSV, "en"); got != gate0arChipsCSV {
			t.Fatalf("%s：五条 chips 的坏稿被发给访客了：%q", bad.name, got)
		}
		if cached := e.db.GetConfig("i18n:chips:en", ""); cached != "" {
			t.Fatalf("%s：未过条数契约的稿子落了库（现网 ar 那一行就是这么长出来的）：%q", bad.name, firstLine(cached))
		}
	}
	// 反向对照：同一句源文、四行合格稿 ⇒ 必须落库并复用
	st2 := newSeqStub(t, gate0arChipsGood)
	e2 := st2.engine(t)
	if got := e2.LocalizeChips(ctx, gate0arChipsCSV, "en"); !strings.Contains(got, "glossary") {
		t.Fatalf("合格稿被判不合格（访客退回看中文）：%q", got)
	}
	if cached := e2.db.GetConfig("i18n:chips:en", ""); !strings.Contains(cached, "glossary") {
		t.Fatalf("合格稿没落缓存（每次 greet 白打一次上游）：%q", firstLine(cached))
	}
	if _ = e2.LocalizeChips(ctx, gate0arChipsCSV, "en"); st2.count() != 1 {
		t.Fatalf("好稿没被缓存复用（上游 %d 次）", st2.count())
	}
}

// TestCannedRepairIsIdempotent 修正腿必须幂等——**这是"回写同一行"成立的前提**。
//
// 读侧命中后会拿 final 回写缓存（localize.go 那段"同指纹，非新稿"）。如果清洗不幂等，
// 同一行每读一次就变一点：那是把缓存写成了流，而且现网排障时"库里那一行"和
// "界面那一屏"永远对不上（AGENTS §一·13 那句「判据两侧必须同口径」的另一种形态）。
//
// 判据两层：① `repairCannedOutbound` 自身两次等于一次；② **整道闸**二次判定不再变
// （因为回写之后下一次读的是同一把尺子，两处分开不幂等都会让那一行继续漂）。
func TestCannedRepairIsIdempotent(t *testing.T) {
	for _, c := range []struct{ lang, dirty, mustNot string }{
		{"ko", "안녕하세요, ⟨LangCross⟩의 AI 어시스턴트입니다 👋", "⟨"},
		{"ja", "こんにちは、【能言】 AI アシスタントです", "【"},
		{"en", "Hello, this is [[BRAND]] speaking", "BRAND"},
		{"en", "Hello, this is NengYan speaking", "NengYan"},
	} {
		once := repairCannedOutbound(c.lang, c.dirty)
		if twice := repairCannedOutbound(c.lang, once); twice != once {
			t.Fatalf("修正腿不幂等（第二次读还会改一点）：%q → %q → %q", c.dirty, once, twice)
		}
		if strings.Contains(once, c.mustNot) {
			t.Fatalf("脏形态没被修掉：%q 里仍有 %q", once, c.mustNot)
		}
	}

	// ② 整道闸的二次判定：现网 ko 那一行（品牌名被包成尖括号）走的是"修好即放行"，
	//    所以第一次判就必须 bad=false；而**第二次判同一串字节**必须得到完全相同的结论与 final。
	src := "你好，我是能言 AI 助手 👋"
	dirty := "안녕하세요, ⟨LangCross⟩의 AI 어시스턴트입니다 👋"
	topics := srcTopicsOf(src)
	if !topics.brand {
		t.Fatal("用例前提没了：这句原文必须真提到品牌名")
	}
	f1, r1, d1, b1 := cannedOutboundReject("ko", src, dirty, topics, 0)
	if b1 {
		t.Fatalf("修正腿救回来的稿子仍被拦（现网 ko 首屏会继续投中文）：reason=%q detail=%q", r1, d1)
	}
	f2, r2, d2, b2 := cannedOutboundReject("ko", src, f1, topics, 0)
	if b2 || f2 != f1 || r2 != r1 || d2 != d1 {
		t.Fatalf("同一串字节二次判定结论变了（回写会让那一行一直漂）：f1=%q f2=%q r1=%q r2=%q", f1, f2, r1, r2)
	}
	if !strings.Contains(f1, "LangCross") {
		t.Fatalf("修完把品牌名一起删了（那是丢失不是清洗）：%q", f1)
	}
}

// TestCannedBrandDropIsObservationOnly 品牌名脱落那一档**只出声、不拦**（这一档本批写成拒绝后被证伪）。
//
// 证伪它的就是英文首屏：`Hi, what can I translate for you?` 是合格的欢迎词，
// 只是换了说法、没自称品牌名——同一判据会把它一起挡成中文，
// 而"语言保证"比"自称品牌名"重得多（082x 那批修的就是它，见 restoreBrandAfterTranslation 那条 ⚠️）。
// 三条腿都要钉：现网 ko 那一行（观测＋正文照发＋**缓存照写**）、英文反向对照（放行）、
// 汉字档正向对照（写了「能言」⇒ 连观测都不该出）。
func TestCannedBrandDropIsObservationOnly(t *testing.T) {
	ctx := context.Background()
	src := "你好，我是能言 AI 助手 👋"
	topics := srcTopicsOf(src)

	final, reason, _, bad := cannedOutboundReject("ko", src, "안녕하세요, AI 어시스턴트입니다 👋", topics, 0)
	if bad {
		t.Fatalf("品牌名脱落被判成拒绝（会把首屏退回中文）：reason=%q", reason)
	}
	if reason != cannedBrandDropped {
		t.Fatalf("品牌名脱落没有出声：reason=%q", reason)
	}
	if strings.Contains(final, "BRAND") {
		t.Fatalf("观测档把占位符发出去了：%q", final)
	}

	// 反向对照①：换了一种说法的合格英文欢迎词——必须放行（这一条就是翻档的那条实证）
	if _, rEn, _, badEn := cannedOutboundReject("en", src, "Hi, what can I translate for you?", topics, 0); badEn {
		t.Fatalf("合格英文欢迎词被拦成中文首屏：reason=%q", rEn)
	}
	// 反向对照②：写了本语种档品牌名 ⇒ 一个 reason 都不许出（观测档不许变常驻噪音）
	if _, rJa, dJa, badJa := cannedOutboundReject("ja", src, "こんにちは、能言 AI アシスタントです 👋", topics, 0); badJa || rJa != "" || dJa != "" {
		t.Fatalf("写了「能言」的日文档仍被观测/拒绝：reason=%q detail=%q", rJa, dJa)
	}

	// 端到端：正文照发＋缓存照写（只测判据不测这一半，就等于"出声出对了、稿子还是没投出去"）
	st := newSeqStub(t, "안녕하세요, AI 어시스턴트입니다 👋")
	e := st.engine(t)
	if got := e.LocalizeGreeting(ctx, src, "ko"); !strings.Contains(got, "어시스턴트") {
		t.Fatalf("观测档把正文拦回了中文：%q", got)
	}
	if cached := e.db.GetConfig("i18n:welcome:ko", ""); !strings.Contains(cached, "어시스턴트") {
		t.Fatalf("观测档没写缓存（每次 greet 白打一次上游）：%q", firstLine(cached))
	}
}
