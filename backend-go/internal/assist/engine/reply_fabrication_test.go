// ============ reply_fabrication_test.go · 职责说明 ============
// 锁 0AR 第 4 波 ⑲：挂件**编造承诺**的出站守卫（假存量／能力清单外的交付物／模型自配的词条译法）。
//
// 本文件的靶子不是"正则写得对不对"，而是三件更容易在将来被改坏的事：
//   - **判据问库、不问名单**（②③）：运营补一条功能卡就该让那句话合法发出去，不需要发版。
//     所以派生对照（加一行 → 同一句必须放行）是这一族的核心锁，删掉它等于把守卫退化成写死名单，
//     而"名单版"在单测里跑得和"问库版"一样绿——只有那一条对照能分开两者。
//   - **闸门必须双向**（每条正向都配反向对照）：这条守卫删的是**客户要看的正文**，
//     误删一族的严重性不低于放过一句编造（拒绝句被删＝客户以为默认支持，文件头那条口径）。
//   - **两条闸不许互相顶**：合法报价数字必须一字不动（`...KeepsLegalQuote`），
//     否则 ⑲ 会把 `guardReplyQuote` 的射程整段吞掉，现网表现是"报价突然全没了"。
//
// ★ 一批反证（写在各用例注释里，破坏点 → 哪一条红）：
//   - 摘掉 `engine.go` 里的调用点 ⇒ 接线锁＋端到端红；
//   - 把 ② 的 corpus 判据换成"名单里有就判" ⇒ 派生对照红；
//   - 把拒绝句豁免摘掉 ⇒ 拒绝句红；把它挪到 ① **之前** ⇒ 复合形态（数字＋没有）红；
//   - 把 `[^文本者]` 那一支删掉 ⇒ 现网取证那一形态（裸「固定译 "X"」）红；
//   - 把剥空回退摘掉 ⇒ 空气泡红。
//   - ★ 反证批自己踩出来的一条**写法教训**：单句靶子会被"剥空即回退"掩护
//     （A11 摘掉来源判据时装的是单句 script 回复，那一句被整条退还 ⇒ 测试仍绿）。
//     凡是断言"正文一字不动"的段，靶子都必须是**多句**，否则红会落在别的段上、
//     看起来像"这条锁有射程"，实际射程在别处。
//   - ★ 第二条教训（A12 同一批踩出）：**判据级断言代替不了守卫级**。上面那两条清单为空的断言
//     走的是 `fabScopeOf`（测试自己造 scope），所以把 `guardReplyFabrication` 里那一行
//     scope 构造改坏（corpusOK 硬编码成永远可用）时全绿；补了 ②b 那段守卫级同规格断言才红。
//     ⇒ 凡"守卫拿到的输入是算出来的"这类判据，除了直接调判据函数，必须在守卫入口再跑一次同一形态。
//
// =============================================
package engine

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
)

// fabScopeOf 按**当前库**造出守卫用的清单视图。
// 判据级用例直接调 `fabricationReasonFor`，免得为了测一条字符串判据把会话／上游桩都拉进来；
// 清单必须现取现算（夹具里改过 feature_links 之后，拿旧的 scope 判等于测的是上一秒的库）。
func fabScopeOf(t *testing.T, e *Engine) fabricationScope {
	t.Helper()
	corpus, ok := e.capabilityCorpus()
	return buildFabricationScope(corpus, ok)
}

// mustFabricate 这句必须命中某个分档，且 detail 里要点名到肇事的那一串
// （reason＋detail 是对外排障契约：运维靠它决定"该收紧提示词"还是"该补知识库条目"）。
func mustFabricate(t *testing.T, sc fabricationScope, sent, wantReason, wantDetail string) {
	t.Helper()
	reason, detail := fabricationReasonFor(sent, sc)
	if reason != wantReason {
		t.Fatalf("%q\n 判成分档 %q（期望 %q，detail=%q）", sent, reason, wantReason, detail)
	}
	if wantDetail != "" && !strings.Contains(strings.ToLower(detail), strings.ToLower(wantDetail)) {
		t.Fatalf("%q 的 detail 没点名 %q（排障只看到分档名不知道该补哪条库）：%q", sent, wantDetail, detail)
	}
}

// mustPass 这句必须放行（空分档名）。反向对照的每一条都在问这件事。
func mustPass(t *testing.T, sc fabricationScope, sent string) {
	t.Helper()
	if reason, detail := fabricationReasonFor(sent, sc); reason != "" {
		t.Fatalf("正常正文被误杀：%q ⇒ reason=%q detail=%q", sent, reason, detail)
	}
}

// disableAllRows 把一张表全部置为停用（含本来停用的行），用来造"能力清单取不到"那一档。
// ⚠️ 用 UPDATE 置 0 而不是删行：与运维在管理台点停用的实际形态一致（行还在、enabled=0），
// 判据读的是 `List(table, true)` 那一档，删行测不到同一件事的可能只是碰巧。
func disableAllRows(t *testing.T, e *Engine, table string) {
	t.Helper()
	rows, err := e.db.List(table, false)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", table, err)
	}
	for _, r := range rows {
		if err := e.db.Update(table, rowID(r), map[string]any{"enabled": 0}); err != nil {
			t.Fatalf("停用 %s 失败：%v", table, err)
		}
	}
}

// TestGuardReplyFabricationDropsCountedClaims 判据 ①：具数声称「库里有多少条」。
//
// 现网取证那一形态（「汽车零件术语库存了 327 个标准词」）在本档之外**不需要查任何语料**：
// 库存条数不是对客户讲的事，【相关知识】里也没有它，出现即编造。
//
// ★ 最后一段钉的是**顺序**（① 排在拒绝句豁免之前）：复合形态「我们有 327 个标准词，不过没有…」
// 里那句拒绝标记不能给假数字发通行证。反证：把豁免循环挪到两条正则**前面** ⇒ 那一段当场红
// （其余用例照绿——这一族破坏只有那一条用例看得见，别把它当成冗余段删掉）。
//
// 反向对照钉三件事：报价数字不归本腿管、无数字的术语句照发、
// 以及**单字豁免「别」已删**（「特别／别的／识别」里都有这个字，留着它等于把这条守卫调成静默）。
func TestGuardReplyFabricationDropsCountedClaims(t *testing.T) {
	e := newTestEngine(t)
	sc := fabScopeOf(t, e)

	for _, c := range []struct{ sent, detail string }{
		{"我们术语库里存了 327 个标准词，覆盖汽车零件。", "标准词"},
		{"词库里已经有 1，200 条词条可以直接复用。", "词条"},
		{"高频行业词大概 3000+ 个，都在库里。", "行业词"},
		{"We keep 327 standard terms for automotive parts.", "standard terms"},
		{"The glossary has 12,000 entries already.", "entries"},
	} {
		mustFabricate(t, sc, c.sent, fabrCountClaim, c.detail)
	}

	// ★ 顺序段：同句里带拒绝标记**不许**把假数字放行（① 不享受豁免）
	mustFabricate(t, sc, "我们有 327 个标准词，不过没有覆盖全部行业。", fabrCountClaim, "标准词")

	// 反向对照①：报价数字不归本腿管（那是 guardReplyQuote 的尺子）
	for _, ok := range []string{
		"标准模式 1000 源字符 23.5 积分。",
		"折算下来每一千字不到 1 元。",
		"术语库可以按您的行业自定义，词条由您维护。",
		"这一项特别容易处理，别的文件也支持。", // 「别」已从豁免标记里删掉，这里靠"本来就没数字接名词"放行
		"We translate 1000 characters for 5 credits.",
	} {
		mustPass(t, sc, ok)
	}
}

// TestGuardReplyFabricationDropsUnknownDeliverables 判据 ②：能力清单外的交付物／环境。
//
// ★ 本用例的重心是**中间那一段派生对照**：同一句 sandbox 文案，先被丢掉，
// 给 `feature_links` 补一张启用卡（名字里带 Sandbox）之后**必须一字不动地放行**，
// 再把那张卡停用又要重新判。三段合起来证明判据问的是库：
//   - 只测第一段 ⇒ 名单版实现（写死 denylist）同样绿，将来运营补了功能卡也救不回那句话；
//   - 少了第三段 ⇒ "读全表而不读启用行"的实现同样绿，停用的能力会继续给客户承诺。
//
// 拉丁档另有一段**词边界**反证（`clients` 里含 `cli`、`democracy` 里含 `ocr`）：
// 子串匹配会把正常英文单词判成交付物，这一档只有按整词问库才成立。
func TestGuardReplyFabricationDropsUnknownDeliverables(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	sent := "我们可以给您开一个 API sandbox 直接联调。"

	// ① 夹具的启用清单里没有 sandbox ⇒ 判编造
	mustFabricate(t, fabScopeOf(t, e), sent, fabrUnknownDeliver, "sandbox")
	// 汉字档同一族
	mustFabricate(t, fabScopeOf(t, e), "沙箱环境里有现成的数据可以试。", fabrUnknownDeliver, "沙箱")
	// ★ 豁免标记必须窄到**成词的否定**：写回单字「别」（曾被收过）之后，
	// 「特**别**」「识**别**」这种正常词会把整句领走，这一句就漏判了。
	mustFabricate(t, fabScopeOf(t, e), "特别提示：我们可以给您开一个 API sandbox。", fabrUnknownDeliver, "sandbox")

	// ② 派生白名单：运营补一张**启用**功能卡 ⇒ 同一句必须放行（判据问库，不需要发版）
	addFeatureCard(t, e, "sandbox", "API 联调 Sandbox")
	mustPass(t, fabScopeOf(t, e), sent)
	// 守卫级再走一遍：正文一个字都不许动
	rep := &Reply{Content: sent, Source: "llm"}
	if got := e.guardReplyFabrication(ctx, "zh", rep); got.Content != sent {
		t.Fatalf("库里已启用该能力，正文仍被改动：\n got=%q", got.Content)
	}

	// ③ 那张卡停用 ⇒ 白名单跟着收回（与写侧读同一档 enabled）
	rows, err := e.db.List("feature_links", false)
	if err != nil {
		t.Fatalf("读 feature_links 失败：%v", err)
	}
	for _, r := range rows {
		if asStr(r["key"]) == "sandbox" {
			if err := e.db.Update("feature_links", rowID(r), map[string]any{"enabled": 0}); err != nil {
				t.Fatalf("停用 sandbox 卡失败：%v", err)
			}
		}
	}
	mustFabricate(t, fabScopeOf(t, e), sent, fabrUnknownDeliver, "sandbox")

	// 反向对照①：拉丁档必须按**整词**匹配
	for _, ok := range []string{
		"This is safe for your clients and their internal data.",
		"We can translate a text about democracy and elections.",
		"我们的在线编辑器可以直接改译文，插件也支持。", // 名单里没这些词，压根不进 ② 的射程
	} {
		mustPass(t, fabScopeOf(t, e), ok)
	}
	// 反向对照②：拒绝句豁免（promise.go 第三节教模型直说不支持，那些句子必须发得出去）
	for _, ok := range []string{
		"我们不支持语音翻译，也没有图片翻译。",
		"扫描件暂不提供，麻烦先转成可编辑文档。",
		"We do not offer a webhook yet.",
		"OCR is not available for scanned pages.",
	} {
		mustPass(t, fabScopeOf(t, e), ok)
	}
	// ★ 反向对照③：豁免标记**不收裸 "not"**（收了就是"外文正文里凡带 not 的句子全部放行"）。
	// 「not only」这种非否定的用法必须仍然把 sandbox 判出来——这一条钉的是标记表的窄度。
	mustFabricate(t, fabScopeOf(t, e), "We support sandbox access, not only for admins.", fabrUnknownDeliver, "sandbox")
}

// TestGuardReplyFabricationDropsSelfMadeTermExamples 判据 ③：模型自己配的词条对应关系。
//
// 现网取证：「"火花塞"固定译 "ignition plug"」——错译，正确是 spark plug。
// 放行条件刻意做成**两个词都在启用知识里**（＝它真的在引用语料）：
// 中间那一段夹具先塞一条只提「火花塞」不提 spark plug 的知识，此时那句错译**必须仍被判**——
// 这是"源语词在库里就放行"那种宽松档的反证（宽松档对本缺陷完全无感，等于摘掉 ③）。
// 最后一段把**正确**译法（spark plug）写进知识：那句照库说的必须放行，
// 而库里没写的 ignition plug 那一句**照判**——库里已经给了正确答案、模型还自己配一个错的，
// 这一族恰恰是 ③ 存在的理由（放行了它，等于守卫只对"库里没词条"负责、对"配错词条"无感）。
//
// ★ 「译」后面那一截的形态也在本用例里钉：现网那句是裸「固定译 "X"」，没有「为」字。
// 只支持「固定译为 X」的实现会让整条腿对现网缺陷无感，而单测若只测带「为」的写法就一直绿
// （反证：把 `[^文本者]` 那一支删掉 ⇒ 第二段红）。
func TestGuardReplyFabricationDropsSelfMadeTermExamples(t *testing.T) {
	e := newTestEngine(t)
	wrong := "「火花塞」固定译 " + `"ignition plug"` + "。"

	// 夹具里没有火花塞 ⇒ 两个词都不在库 ⇒ 判
	mustFabricate(t, fabScopeOf(t, e), wrong, fabrTermExample, "火花塞")

	// 宽松档反证：源语词进了库、目标语词仍是模型自己配的 ⇒ 照判
	if _, err := e.db.Create("kb_entries", map[string]any{
		"key": "kb-auto-parts", "category": "usage", "title": "汽车零件", "priority": 8, "enabled": 1,
		"content": "汽车零件术语覆盖火花塞、刹车片。", "keywords": "火花塞,刹车片", "link_keys": "",
	}); err != nil {
		t.Fatalf("预置零件知识失败：%v", err)
	}
	mustFabricate(t, fabScopeOf(t, e), wrong, fabrTermExample, "火花塞")

	// 带「为」／带「译法为」的同族形态一起判（现网不止一种写法）
	sc := fabScopeOf(t, e)
	mustFabricate(t, sc, "「附件」统一译为 attachment。", fabrTermExample, "附件")
	mustFabricate(t, sc, "「工单」的标准译法为 work sheet。", fabrTermExample, "工单")

	// 正确译法进了库 ⇒ 那句**照引用库**的说法成为合法引用，必须放行；
	// 而同一句换成库里没有的 ignition plug 仍要判——这正是现网那一条的形态：
	// 库里给了正确译法，模型照样自己配一个错的（宽松档"源语词在库里就放行"会放过它）。
	legal := "「火花塞」固定译 " + `"spark plug"` + "。"
	mustFabricate(t, fabScopeOf(t, e), legal, fabrTermExample, "火花塞")
	if _, err := e.db.Create("kb_entries", map[string]any{
		"key": "kb-sparkplug", "category": "terms", "title": "零件译法", "priority": 9, "enabled": 1,
		"content": "火花塞的标准译法是 spark plug。", "keywords": "火花塞,spark plug", "link_keys": "",
	}); err != nil {
		t.Fatalf("预置零件译法失败：%v", err)
	}
	mustPass(t, fabScopeOf(t, e), legal)
	mustFabricate(t, fabScopeOf(t, e), wrong, fabrTermExample, "ignition plug")

	// 反向对照：不带限定词的正常说法一律不判（限定词可选就会把整段回答扫成编造）
	for _, ok := range []string{
		"这一段译为中文即可，不用逐字对照。",
		"「品牌名」统一翻译为中文，不加空格。",
		"「术语」的译文按您的词库走。",
	} {
		mustPass(t, fabScopeOf(t, e), ok)
	}
}

// TestGuardReplyFabricationKeepsLegalQuote ★正向对照：合法报价数字必须一字不动。
//
// 这一条钉的是两条闸的**分工**，不是"报价句碰巧不含敏感词"：
// 报价句独立成句时本腿不动它，`guardReplyQuote` 才有原稿可复算；
// 本腿一旦把报价句一起吞了，现网表现是"挂件突然不报价了"，而报价腿的单测一行都不会红。
// 第二段的守卫级断言把两件事一起钉：假存量那句被删、报价那句**逐字节等值**留下。
func TestGuardReplyFabricationKeepsLegalQuote(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	quote := "标准模式 1000 源字符 23.5 积分。"
	sc := fabScopeOf(t, e)
	mustPass(t, sc, quote)
	mustPass(t, sc, "折合每一千字约 0.8 元，套餐内额度可抵扣。")
	mustPass(t, sc, "The price is 23.5 credits for 1000 source characters.")

	rep := &Reply{Content: quote + "我们术语库里存了 327 个标准词。", Source: "llm"}
	got := e.guardReplyFabrication(ctx, "zh", rep)
	if got.Content != quote {
		t.Fatalf("该只删假存量那句，报价句被动了：\n got=%q\nwant=%q", got.Content, quote)
	}
}

// TestGuardReplyFabricationFailSoftAndScope 三条 fail-soft＋射程边界（挂载范围的判定在这里，接线在另一条锁里）。
//
//   - **只跑 Source=="llm"**：话术直配／流程／兜底都是人写的文案，不经模型、不可能编造；
//     给它们跑过滤＝把运营在管理台写的那句话当缺陷删掉（现网表现是话术突然缺半句）。
//   - **能力清单取不到 ⇒ ②③ 整条腿不判、① 照判**：清单一空就把正文删一半不是"安全方向"，
//     但假存量那一档压根不依赖清单，不该跟着一起哑火。
//   - **剥空即回退**：整条回复全被判编造属异常形态，宁可不发空气泡。
//   - **控制序列所在句豁免**：【go:key】那一行连着编造词也要留着，别把功能入口连坐删掉（同 guardReplyQuote 的口径）。
//
// 反证：摘掉 `rep.Source != "llm"` 那一行 ⇒ 第一段红；摘掉 corpusOK ⇒ 第二段红；
// 摘掉 `joined == ""` 那段 ⇒ 第三段红（发出空气泡）。
func TestGuardReplyFabricationFailSoftAndScope(t *testing.T) {
	ctx := context.Background()
	e := newTestEngine(t)
	fab := "我们可以给您开一个 API sandbox 直接联调。"

	// ① 非模型来源不动一个字。
	// ★ 必须写成**两句**：只给一句时它会被"剥空即回退"那档原样退还，
	// 于是"来源判据被摘掉"这一族破坏在单句靶子上完全看不见（本批反证实测：A11 装上去仍绿，
	// 红的是第三段而不是第一段——补成多句之后才落到本段该抓的位置）。
	ruleBody := fab + "这一批文件今天就能出。"
	rule := &Reply{Content: ruleBody, Source: "script"}
	if got := e.guardReplyFabrication(ctx, "zh", rule); got.Content != ruleBody {
		t.Fatalf("话术直配的文案被幻觉守卫改了（那是运营写的话，不是模型编的）：\n got=%q\nwant=%q", got.Content, ruleBody)
	}

	// ② 清单为空：② 不判、① 照判
	disableAllRows(t, e, "feature_links")
	disableAllRows(t, e, "kb_entries")
	if _, ok := e.capabilityCorpus(); ok {
		t.Fatal("夹具已全部停用，能力清单却还判得出——本段的靶子没了")
	}
	sc := fabScopeOf(t, e)
	mustPass(t, sc, fab)
	mustFabricate(t, sc, "我们术语库里存了 327 个标准词。", fabrCountClaim, "标准词")

	// ②b 守卫级同规格：清单取不到时**整条回复照发**（只有 ① 那一档还在判）。
	// ★ 这一段是反证 A12 的射程来源：上面那两条只走 fabScopeOf（测试自己造的 scope），
	//   看不见 `guardReplyFabrication` 里那一行 scope 构造被改坏——本批实测 A12（把 corpusOK
	//   写成 `corpusOK || true`）装上去仍然全绿，直到补了这条守卫级断言才落到该抓的位置。
	//   靶子照 ① 那条写成**两句**：单句会被"剥空即回退"掩护（见上面那段注释）。
	softReply := &Reply{Content: fab + "这一批文件今天就能出。", Source: "llm"}
	wantSoft := softReply.Content
	if got := e.guardReplyFabrication(ctx, "zh", softReply); got.Content != wantSoft {
		t.Fatalf("能力清单读不到却仍按空清单删正文（fail-soft 反了方向：拿不到清单＝不知道什么是编造，不是什么都算编造）：\n got=%q\nwant=%q", got.Content, wantSoft)
	}

	// ③ 剥空即回退：整条都是编造 ⇒ 原样留着（连同 WARN 交人看），不发空气泡。
	// ★ 两句都走 ① 那一档：此刻清单已被停用、② 整条腿不判，
	// 拿 sandbox 那句凑"全被判编造"根本凑不出（它现在本来就放行），这段就退化成空转锁。
	allFab := &Reply{Content: "我们术语库里存了 327 个标准词。高频行业词大概有 5000 个。", Source: "llm"}
	before := allFab.Content
	if got := e.guardReplyFabrication(ctx, "zh", allFab); got.Content != before || got.Content == "" {
		t.Fatalf("剥空回退没生效（发出空气泡或改成了别的）：%q", got.Content)
	}

	// ④ 控制序列所在句豁免（用 ① 那一档造句：清单此刻是空的，② 本来就不判，
	// 拿 sandbox 造这一句测不到"连坐"那一族——同 ③ 一个道理）
	marker := &Reply{Content: "【go:pricing】这里我们有 327 个标准词可以复用。", Source: "llm"}
	wantMarker := marker.Content // ⚠️ 守卫是**就地改** rep，事后读 marker.Content 等于拿结果比结果（恒真）
	if got := e.guardReplyFabrication(ctx, "zh", marker); got.Content != wantMarker {
		t.Fatalf("控制序列所在句被连坐删掉（功能入口丢失）：%q", got.Content)
	}
}

// TestGuardReplyFabricationOnlyAtRespondThroat 这一腿只有一个调用点，且必须在那条咽喉上、排在报价腿之前。
//
// AGENTS §一·13 给守卫链立的规矩（"新增守卫一律挂 Respond 这一条链，不许挂进四道回复分支"）
// 对幻觉守卫同样成立：挂进某一道分支，就是下一次「走模型的回复滤了、走流程步进的那道没滤」的产地，
// 而两侧各自单测都绿。顺序也要钉：本腿删句子、报价腿对着**留下的正文**复算，
// 两条换了次序就变成"报价腿先替换整句、幻觉句跟着被顺带带走"——
// 那种侥幸绿正是 093x 在 `TestRespondThroughOutboundGuards` 里点名修过的形态。
//
// 写法是派生式的（扫全包非测试文件），不写死文件清单：新增调用点当场红，
// 把调用点挪出 `Respond` 的行区间、或挪到 `guardReplyQuote` 之后，也当场红。
// 反证：在 `respond` 的某道分支里再调一次 ⇒ 计数 2 ⇒ 红。
func TestGuardReplyFabricationOnlyAtRespondThroat(t *testing.T) {
	fabPat := regexp.MustCompile(`\.guardReplyFabrication\(`)
	quotePat := regexp.MustCompile(`\.guardReplyQuote\(`)
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败：%v", err)
	}
	type site struct {
		file string
		line int
	}
	var fabSites, quoteSites []site
	respondRange := [2]int{-1, -1}
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读 %s 失败：%v", name, err)
		}
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if strings.HasPrefix(l, "func (e *Engine) Respond(") {
				respondRange[0] = i
				for j := i + 1; j < len(lines); j++ {
					if strings.HasPrefix(lines[j], "func ") {
						respondRange[1] = j
						break
					}
				}
				if respondRange[1] < 0 {
					respondRange[1] = len(lines)
				}
				break
			}
		}
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue // 说明注释里提这个名字不算调用点
			}
			if fabPat.MatchString(l) {
				fabSites = append(fabSites, site{file: name, line: i + 1})
			}
			if quotePat.MatchString(l) {
				quoteSites = append(quoteSites, site{file: name, line: i + 1})
			}
		}
	}
	if len(fabSites) != 1 {
		t.Fatalf("幻觉守卫有 %d 个调用点（只允许 Respond 咽喉那一个）：%+v", len(fabSites), fabSites)
	}
	if fabSites[0].file != "engine.go" || respondRange[0] < 0 ||
		fabSites[0].line <= respondRange[0] || fabSites[0].line > respondRange[1] {
		t.Fatalf("唯一的调用点不在 Engine.Respond 里：%+v（Respond 区间 %v）", fabSites[0], respondRange)
	}
	if len(quoteSites) != 1 || quoteSites[0].file != fabSites[0].file {
		t.Fatalf("报价腿的调用点读数异常（本用例要拿它做顺序对照）：%+v", quoteSites)
	}
	if fabSites[0].line >= quoteSites[0].line {
		t.Fatalf("幻觉守卫排到了报价腿之后（应先删编造句、报价腿对着留下的正文复算）：fab=%d quote=%d",
			fabSites[0].line, quoteSites[0].line)
	}
}

// TestRespondDropsFabricationAtThroat 端到端接线证明：从 `Respond` 打进一条带假存量的回答，
// 要求出栈时那一句整句消失、其余正文照留，而且**守卫不给回复加一次上游往返**
// （这一腿是纯字符串＋本地两表读数，打网络就是每条回复多付一次延迟）。
//
// 反证：摘掉 `engine.go` 那一行调用 ⇒ 第一段红（假存量照样发给客户）；
// 把守卫里的两表读数换成一次 llm 调用 ⇒ 次数断言红。
func TestRespondDropsFabricationAtThroat(t *testing.T) {
	st := newSeqStub(t, "We keep 327 standard terms in the glossary.\nThe glossary can be customized for your own industry.")
	e := st.engine(t)
	e.sysValDoc = docForGuard()
	newSession(t, e, "s-fab")

	rep := e.Respond(context.Background(), "s-fab", "How many terms do you have?", "/", "en", nil)
	if strings.Contains(rep.Content, "327") || strings.Contains(rep.Content, "standard terms") {
		t.Fatalf("假存量从 Respond 漏出去了（客户屏上还是那句编造）：%q", rep.Content)
	}
	if !strings.Contains(rep.Content, "customized") {
		t.Fatalf("只该删编造那一句，其余正文被一起吞了：%q", rep.Content)
	}
	if got := st.countExcluding(cannedSurface); got != 1 {
		t.Fatalf("上游拨了 %d 次（应为生成那 1 次；幻觉守卫是本地判据，不该加往返）", got)
	}
}
