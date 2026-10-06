// ============ canned_actions_0ar_test.go · 职责说明 ============
// 锁 0AR 第 4 波 ⑱：挂件**动作按钮名**（feature_links.name）纳入 canned 那一条翻译＋缓存＋出栈闸的路。
//
// 这一族用例问的是四件事，每一件都对应台账里那条现网形态（正文已是外文＋一排中文按钮）：
//  1. **翻得到**：逐键翻、逐键缓存、第二次不再拨上游；
//  2. **翻坏了不投**：单行契约（wantLines=1）在写缓存之前判，坏稿一行都不进库、按钮留中文原名
//     （留中文是设计内的降级，**空名字才是事故**——那是一个点不到的隐形入口）；
//  3. **不白等**：整排按钮共用**一条**同步预算，冷语种那一次 reply 最多等一档预算，
//     而不是「按钮数 × 预算」（现网反代 30 秒砍的是整次 reply，不是单条文本）；
//  4. **键形与清理腿对得上**：写进 `i18n:` 段的每一个键，启动期清理腿都必须能拆开并现算指纹
//     （漏了 `cannedSourceFor` 那一档的形态不是报错，是"清理跑绿、报 0 行、旧字节还在投"）。
//
// 反证逐条写在用例注释里（破坏点 → 哪一段红）。
// =============================================
package engine

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ⑱ 用例的统一按钮名（夹具里 feature_links 的 name 默认等于 key，是英文串，
// 拿它测"翻成外文"会恒真——先把要用的那几把改成中文原名，缺陷本体才是被测的形态）。
const (
	actBillingName = "充值与账单"
	actPricingName = "价格页"
	actTicketsName = "文件翻译"
	actKBAdminName = "能言知识库管理"
)

// setFeatureName 把某个功能卡 key 的 name 改成中文原名（只动启用行，与写侧同一个读法）。
func setFeatureName(t *testing.T, e *Engine, key, name string) {
	t.Helper()
	rows, err := e.db.List("feature_links", true)
	if err != nil {
		t.Fatalf("读 feature_links 失败：%v", err)
	}
	for _, r := range rows {
		if asStr(r["key"]) != key {
			continue
		}
		if err := e.db.Update("feature_links", rowID(r), map[string]any{"name": name}); err != nil {
			t.Fatalf("改 %s 的 name 失败：%v", key, err)
		}
		return
	}
	t.Fatalf("夹具里没有功能卡 %q，本用例的靶子没了", key)
}

// rowID 取动态行里的自增主键（int64 是当前驱动口径，另两档留给驱动变更时当场出声，不当静默失败）。
func rowID(r map[string]any) int64 {
	switch v := r["id"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return -1
}

// addFeatureCard 夹具里没有的功能卡（比如 kbadmin）自己补一张，name 用中文原名。
func addFeatureCard(t *testing.T, e *Engine, key, name string) {
	t.Helper()
	if _, err := e.db.Create("feature_links", map[string]any{
		"key": key, "name": name, "url": "/admin", "ftype": "route", "sort": 95, "enabled": 1,
	}); err != nil {
		t.Fatalf("补功能卡 %s 失败：%v", key, err)
	}
}

// actionFp 某个按钮名在当前口径下的缓存指纹（与 localize 里那一行同函数同入参，必然同值）。
func actionFp(key, name, lang string) string {
	return srcFingerprint(name + "\x00" + localizeContract(lang, srcTopicsOf(name)))
}

// TestLocalizeActionNamesTranslatesEachButton 主断言：逐键翻、逐键落库、第二次命中缓存零拨号，
// 且**入参切片不许被就地改**（口径 4）。
//
// 反证：
//   - 把 `localizeActionNames` 里那句 `e.localize(...)` 换成直接 `continue`（＝本批压根没实装）
//     ⇒ 第一段红（名字还是中文）；
//   - 把缓存键的 kind 写成裸 key（少一个前缀）⇒ ③ 那段指纹等值红；
//   - 就地改 `actions[i].Name` 而不是先 copy ⇒ ④ 红（调用方手里那份被悄悄换了字）。
func TestLocalizeActionNamesTranslatesEachButton(t *testing.T) {
	st := newSeqStub(t, "Top up & billing", "Pricing page")
	e := st.engine(t)
	setFeatureName(t, e, "billing", actBillingName)
	setFeatureName(t, e, "pricing", actPricingName)
	ctx := context.Background()

	actions := []Action{
		{Key: "billing", Name: actBillingName, URL: "/billing", FType: "route"},
		{Key: "pricing", Name: actPricingName, URL: "/pricing", FType: "route"},
	}
	got := e.localizeActionNames(ctx, actions, "en")

	// ① 两个按钮都拿到外文，且顺序／其它字段一字未动
	if got[0].Name != "Top up & billing" || got[1].Name != "Pricing page" {
		t.Fatalf("按钮名没逐键翻：%q / %q", got[0].Name, got[1].Name)
	}
	if got[0].Key != "billing" || got[0].URL != "/billing" || got[0].FType != "route" || got[0].Icon != "" {
		t.Fatalf("翻译腿把按钮的其它字段动了：%+v", got[0])
	}
	// ② 上游正好被打两次（一键一枪），且第一枪的提示词里**不许**出现"一律写作「X」"
	//    （「充值与账单」没提品牌名 ⇒ 096x-1 那条按源文投的口径在这一档同样成立；
	//     反过来给了"一律写作"就是请模型往按钮上粘品牌名）
	if n := st.count(); n != 2 {
		t.Fatalf("两个按钮打了 %d 次上游（期望一键一枪）", n)
	}
	if body := st.body(0); strings.Contains(body, "品牌名一律写作") {
		// 判据只点**品牌名那一句**：计费单位那句里也写着「一律写作」（源文提到积分时才会拼），
		// 拿"一律写作"整串做负向判据会把它误伤成缺陷——本仓为这类"负向锁射程写宽"红过一次。
		t.Fatalf("原文没提品牌名却给了品牌写法口径（＝请模型往按钮上粘品牌名）：%s", firstLine(body))
	} else if !strings.Contains(body, "原文里没有提到品牌名") {
		t.Fatalf("缺了那条明确禁止注入的口径段：%s", firstLine(body))
	}
	// ③ 库里两行的头**就是**当前指纹（不是"看起来像"，是等值）
	for _, c := range []struct{ key, name, want string }{
		{"billing", actBillingName, "Top up & billing"},
		{"pricing", actPricingName, "Pricing page"},
	} {
		key := cannedCacheKey(featureKindFor(c.key), "en")
		row := e.db.GetConfig(key, "")
		if !strings.HasPrefix(row, actionFp(c.key, c.name, "en")+"\n") {
			t.Fatalf("%s 那行的头不是当前指纹（清理腿与写侧已经分叉了）：%q", key, firstLine(row))
		}
		if strings.TrimPrefix(row, actionFp(c.key, c.name, "en")+"\n") != c.want {
			t.Fatalf("%s 那行的正文不对：%q", key, row)
		}
	}
	// ④ 入参没被就地改（调用方那份仍是库里的中文原名）
	if actions[0].Name != actBillingName || actions[1].Name != actPricingName {
		t.Fatalf("就地改了调用方给的切片：%q / %q", actions[0].Name, actions[1].Name)
	}
	// ⑤ 第二次同样两个字，且**一次都不拨**（缓存命中；顺带证明这一档真的落了库而不是每次现翻）
	again := e.localizeActionNames(ctx, actions, "en")
	if again[0].Name != "Top up & billing" || again[1].Name != "Pricing page" {
		t.Fatalf("第二次没命中缓存：%q / %q", again[0].Name, again[1].Name)
	}
	if n := st.count(); n != 2 {
		t.Fatalf("第二次多打了上游（%d 次）⇒ 这一档没被缓存", n)
	}
}

// TestLocalizeActionNamesBrandSourceGetsBrandClause 与上一条配对的正向对照：
// 名字里**真提了品牌名**（「能言知识库管理」）时，口径段必须切回"一律写作 LangCross"那一档。
//
// 这条不是装饰：⑱ 把 canned 的消费者从两档扩到 N 档，而 096x-1 那条按源文投的口径
// 是**在 localize 里按这句源文现算**的。新消费者只要绕开那一步（比如自己拼提示词、
// 或者把 topics 写死），这一条就红——它钉的是"新档也走同一把尺子"，不是品牌名本身。
func TestLocalizeActionNamesBrandSourceGetsBrandClause(t *testing.T) {
	st := newSeqStub(t, "LangCross Knowledge Base")
	e := st.engine(t)
	addFeatureCard(t, e, "kbadmin", actKBAdminName) // 夹具只有三张卡，这张自己补
	got := e.localizeActionNames(context.Background(),
		[]Action{{Key: "kbadmin", Name: actKBAdminName}}, "en")
	if got[0].Name != "LangCross Knowledge Base" {
		t.Fatalf("带品牌名的按钮名没翻：%q", got[0].Name)
	}
	body := st.body(0)
	if !strings.Contains(body, "一律写作「LangCross」") {
		t.Fatalf("原文提了品牌名却没给写法口径（模型就会自己猜一个写法）：%s", firstLine(body))
	}
	if row := e.db.GetConfig(cannedCacheKey(featureKindFor("kbadmin"), "en"), ""); !strings.Contains(row, "LangCross") {
		t.Fatalf("合格的带品牌名译文没落缓存（每次点按钮都白打一次上游）：%q", firstLine(row))
	}
}

// TestLocalizeActionNamesKeepsChineseWhenGateRejects 单行契约（wantLines=1）必须是**写缓存之前**判。
//
// 现网形态：模型把按钮名翻成"Top up & billing\n(credits recharge anytime)"——两行。
// 旧代码没有条数契约，这一串会被原样落库并投到按钮上（按钮里带换行＋一句解释）。
// 现在：第一个按钮保留中文原名、库里一行都不写；第二个按钮正常翻（证明不是整段放弃）。
//
// 反证：把写侧那一枪的 wantLines 传成 0（＝按钮名不判条数）⇒ ①② 两段一起红
// （名字变成两行串、库里多出那一行）。
func TestLocalizeActionNamesKeepsChineseWhenGateRejects(t *testing.T) {
	st := newSeqStub(t, "Top up & billing\n(credits recharge anytime)", "Pricing page")
	e := st.engine(t)
	setFeatureName(t, e, "billing", actBillingName)
	setFeatureName(t, e, "pricing", actPricingName)
	ctx := context.Background()

	got := e.localizeActionNames(ctx,
		[]Action{{Key: "billing", Name: actBillingName}, {Key: "pricing", Name: actPricingName}}, "en")

	// ① 被拒的那一个保留**中文原名**，绝不是空串、也绝不是那两行
	if got[0].Name != actBillingName {
		t.Fatalf("两行的坏稿被投到按钮上了：%q", got[0].Name)
	}
	// ② 库里一行都没写（坏形态从一开始就不进库，而不是"先落库、渲染时再丢"）
	if row := e.db.GetConfig(cannedCacheKey(featureKindFor("billing"), "en"), ""); row != "" {
		t.Fatalf("未过单行契约的按钮名落了库：%q", firstLine(row))
	}
	// ③ 另一个按钮不受牵连
	if got[1].Name != "Pricing page" {
		t.Fatalf("一个按钮被拒把整排都拖回中文了：%q", got[1].Name)
	}
	if row := e.db.GetConfig(cannedCacheKey(featureKindFor("pricing"), "en"), ""); !strings.Contains(row, "Pricing page") {
		t.Fatalf("合格的那一行没落缓存：%q", firstLine(row))
	}
	// ④ 被拒的那一枪**不许**被缓存成"空"：下一次还会再试（退避不押这一档，见 canned_guard.go 文件头）
	if n := st.count(); n != 2 {
		t.Fatalf("上游次数不是 2（%d）", n)
	}
}

// TestLocalizeActionNamesChineseAndUnknownLangsDialNothing 中文系／未知语种 ⇒ 一个键都不碰。
//
// 这条是"零成本"的正向锁：现网绝大多数访客是中文界面，如果这一腿在中文档也去读缓存、
// 甚至拨上游，那就是给每一次 reply 白加一次库读／一枪网络（greet 那 8 秒的账刚付过）。
//
// 反证：删掉函数开头那句 `visitorWantsChinese(answerLang)` 早退 ⇒ 中文档第一段红
// （localize 内部同样会早退，所以红点不在"翻出错"而在"白读了一遍库＋多了一条日志"？
// 不——真红点是 ②：中文档会去 `GetConfig` 拿那一行并按当前指纹命中缓存，
// 于是库里那行**外文**译文会被投给中文访客，本用例的 Name 等值当场红）。
func TestLocalizeActionNamesChineseAndUnknownLangsDialNothing(t *testing.T) {
	st := newSeqStub(t, "Pricing page")
	e := st.engine(t)
	setFeatureName(t, e, "pricing", actPricingName)
	ctx := context.Background()
	actions := []Action{{Key: "pricing", Name: actPricingName}}

	for _, lang := range []string{"zh", "zh_hant", "", "xx", "EN-never-heard"} {
		got := e.localizeActionNames(ctx, actions, lang)
		if got[0].Name != actPricingName {
			t.Fatalf("语种 %q 这一轮按钮名被换了：%q", lang, got[0].Name)
		}
	}
	if n := st.count(); n != 0 {
		t.Fatalf("中文／未知语种打了 %d 次上游 ⇒ 每一次 reply 都白烧一枪", n)
	}
	// 空按钮组同样零拨号（大多数回复根本没有按钮，这一腿必须对它们完全透明）
	if got := e.localizeActionNames(ctx, nil, "en"); got != nil {
		t.Fatalf("空按钮组被改写了：%+v", got)
	}
	if n := st.count(); n != 0 {
		t.Fatalf("空按钮组还打了上游（%d 次）", n)
	}
}

// TestLocalizeActionNamesUnsafeKeyIsSkippedAndNeverCached key 形状拼不出合法缓存键 ⇒ 不翻、出声、不落库。
//
// 为什么值得一条用例：缓存键是三段（`splitCannedCacheKey`），kind 里多一个冒号那一行就**拆不开**，
// 于是启动期清理永远扫不到它、写侧却照样命中它——坏行常驻且没人知道。
// 现在坏形状在**拨上游之前**就被挡掉，同批次里形状合规的那个按钮照常翻（不是整段放弃）。
//
// 反证：删掉 `featureKeyIsCacheSafe` 那道判据 ⇒ ①② 红（多打一枪＋库里多出拆不开的键）。
func TestLocalizeActionNamesUnsafeKeyIsSkippedAndNeverCached(t *testing.T) {
	st := newSeqStub(t, "Pricing page")
	e := st.engine(t)
	setFeatureName(t, e, "pricing", actPricingName)
	ctx := context.Background()

	got := e.localizeActionNames(ctx, []Action{
		{Key: "bad key", Name: "带空格的卡"},
		{Key: "a:b", Name: "带冒号的卡"},
		{Key: "pricing", Name: actPricingName},
	}, "en")
	// ① 两把坏键原名不动
	if got[0].Name != "带空格的卡" || got[1].Name != "带冒号的卡" {
		t.Fatalf("坏形状 key 的按钮名被动过：%q / %q", got[0].Name, got[1].Name)
	}
	// ② 只打了一枪（坏键压根不进翻译腿）
	if n := st.count(); n != 1 {
		t.Fatalf("坏键也去拨上游了（共 %d 枪，期望 1）", n)
	}
	// ③ 整段 `i18n:` 里**每一个**键都能被清理腿拆开（这条是本仓"写进去、清不掉"那一族的通用不变式）
	keys, err := e.db.ConfigKeysByPrefix(cannedCachePrefix)
	if err != nil {
		t.Fatalf("列 i18n 键失败：%v", err)
	}
	for _, k := range keys {
		if _, _, ok := splitCannedCacheKey(k); !ok {
			t.Fatalf("写进 i18n: 段的键拆不开（启动期清理扫不到它）：%q", k)
		}
	}
	if _, _, ok := splitCannedCacheKey(cannedCacheKey(featureKindFor("pricing"), "en")); !ok {
		t.Fatal("连合规的按钮档键都拆不开 ⇒ featureKindFor 的拼法与清理腿对不上")
	}
}

// TestLocalizeActionNamesSharesOneSyncBudget 整排按钮共用**一条**同步预算（口径 3）。
//
// 缺陷本体不是假设：三个按钮 × 默认 8 秒＝最坏 24 秒，加上正文那一枪就是现网那条
// 「greet/reply 被 /assist-api 的 30 秒反代砍成 502」的第二次复发（0AF 治的是欢迎词那一半）。
// 判据用挂得住的假上游：第 1 枪挂过预算 ⇒ 同步腿到点出中文并起后台腿；
// 第 2、3 个键因为预算已尽**连枪都不拨**，于是整段返回时间 ≈ 一档预算，而不是 N 档。
//
// 反证：把 `actCtx` 换成裸 `ctx`（每个键各吃一档预算）⇒ elapsed 变成 ~3 秒，第一段红；
// 把预算用尽那句 `continue` 删掉（继续 localize）⇒ elapsed 与「pricing 仍没被翻」两条都红。
func TestLocalizeActionNamesSharesOneSyncBudget(t *testing.T) {
	st := newGateStub(t, blockCalls(1, 2), withSlow(1200*time.Millisecond), contents("Top up & billing"))
	e := st.engine(t, "1") // 整段预算 1 秒
	setFeatureName(t, e, "billing", actBillingName)
	setFeatureName(t, e, "pricing", actPricingName)
	setFeatureName(t, e, "tickets", actTicketsName)
	ctx := context.Background()

	t0 := time.Now()
	got := e.localizeActionNames(ctx, []Action{
		{Key: "billing", Name: actBillingName},
		{Key: "pricing", Name: actPricingName},
		{Key: "tickets", Name: actTicketsName},
	}, "en")
	elapsed := time.Since(t0)

	// ① 访客这一轮最多等一档预算（放宽到 2.5 倍是给 CI 抖动，不是给"N×预算"留活路）
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("整排按钮等了 %s ⇒ 预算没共享（三个键各吃一档就是这个数）", elapsed)
	}
	// ② 三个名字本轮都保持中文（第 1 枪超时，2、3 直接跳过），且**不是空串**
	want := map[string]string{"billing": actBillingName, "pricing": actPricingName, "tickets": actTicketsName}
	for _, a := range got {
		if want[a.Key] == "" {
			t.Fatalf("用例给出的按钮组被改写了 key：%q", a.Key)
		}
		if a.Name == "" {
			t.Fatalf("%s 的按钮名变成空串 ⇒ 那是一个点不到的隐形入口，比留中文严重", a.Key)
		}
		if a.Name != want[a.Key] {
			t.Fatalf("%s 本轮就该出中文原名（预算内没翻出来），实际 %q", a.Key, a.Name)
		}
	}
	// ③ 后台腿随后把第 1 个键写好（**跳过其余键不能顺手把后台腿也押掉**——
	//    它是从 context.Background() 重新起的一条 ctx，见 cannedBackgroundCtx）
	waitBg(t, e, 8*time.Second)
	if row := e.db.GetConfig(cannedCacheKey(featureKindFor("billing"), "en"), ""); !strings.Contains(row, "Top up & billing") {
		t.Fatalf("后台腿没把按钮名写进缓存（下一次 reply 还要白等）：%q", firstLine(row))
	}
	if row := e.db.GetConfig(cannedCacheKey(featureKindFor("pricing"), "en"), ""); row != "" {
		t.Fatalf("被跳过的键凭空多出一行缓存：%q", firstLine(row))
	}
	// ④ 在途声明没泄漏（泄漏＝这一 (kind,lang) 之后永久直接出中文，日志一行错误都没有）
	assertNoFlightLeak(t, e)
}

// TestFeatureCannedRowsArePurgeable ⑱ 与启动期清理腿的**同步义务**（canned_actions.go 文件头第 4 条）。
//
// 三段：
//   - 该留的留：刚写下的那一行，清理腿现算出的指纹与它**逐字相等** ⇒ 删 0 行
//     （这一段就是"清理腿认不出这一档"的反事实判据——`cannedSourceFor` 不认 feature_ 前缀时
//     走的是 known 假 ⇒ 也是删 0 行，所以光看这一段的 0 分不出对错，必须配上下一段）；
//   - 该删的删：运营把名字改了 ⇒ 指纹变了 ⇒ 那一行必须被删掉，**不能**继续躺在库里当"当前译文"
//     （这一段才是 known 假与真等值的分水岭：不认这一档 ⇒ 一行都不删 ⇒ 本段红）；
//   - 停用／删卡 ⇒ 原文算不出来 ⇒ 那一行留着（宁可漏删不可误删，与文件头放行分支 2 同一条）。
func TestFeatureCannedRowsArePurgeable(t *testing.T) {
	st := newSeqStub(t, "Top up & billing")
	e := st.engine(t)
	setFeatureName(t, e, "billing", actBillingName)
	ctx := context.Background()

	// 用真实写侧落这一行（不是手工 seed：seed 出来的行证明不了"写侧与清理腿同源"）
	if got := e.localizeActionNames(ctx, []Action{{Key: "billing", Name: actBillingName}}, "en"); got[0].Name == actBillingName {
		t.Fatal("前置条件没成立：按钮名压根没翻，后面的清理判据测的是空靶子")
	}
	key := cannedCacheKey(featureKindFor("billing"), "en")

	// ① 当前代 ⇒ 删 0 行，且 `cannedSourceFor` 认这一档（这条等值直接问判据，不靠"删了几行"倒推）
	src, wantLines, known := e.cannedSourceFor(featureKindFor("billing"))
	if !known || src != actBillingName || wantLines != 1 {
		t.Fatalf("清理腿认不出按钮名这一档（known=%v src=%q wantLines=%d）⇒ 换代后那一行永远删不掉", known, src, wantLines)
	}
	if n := e.PurgeStaleCannedTranslations(ctx); n != 0 {
		t.Fatalf("当前代译文被清理腿删了 %d 行（清理变成了清库）", n)
	}
	if e.db.GetConfig(key, "") == "" {
		t.Fatal("删 0 行却没这一行？判据与库状态至少有一处是假的")
	}

	// ② 运营改名 ⇒ 指纹当场过期 ⇒ 必须删掉那一行
	setFeatureName(t, e, "billing", "积分充值页")
	if n := e.PurgeStaleCannedTranslations(ctx); n != 1 {
		t.Fatalf("改名后该清掉 1 行按钮译文，实际删 %d 行（0＝清理腿认不出这一档，就是文件头那句「跑绿、报 0 行、旧字节还在投」）", n)
	}
	if e.db.GetConfig(key, "") != "" {
		t.Fatalf("旧名字的按钮译文还留在库里：%q", firstLine(e.db.GetConfig(key, "")))
	}

	// ③ 卡片停用（原文算不出来）⇒ 一行都不许动
	rows, err := e.db.List("feature_links", true)
	if err != nil {
		t.Fatalf("读功能卡失败：%v", err)
	}
	disabled := false
	for _, r := range rows {
		if asStr(r["key"]) == "billing" {
			if err := e.db.Update("feature_links", rowID(r), map[string]any{"enabled": 0}); err != nil {
				t.Fatalf("停用功能卡失败：%v", err)
			}
			disabled = true
		}
	}
	if !disabled {
		t.Fatal("夹具里的 billing 卡没停掉 ⇒ 本段测的是空靶子")
	}
	if _, ok := e.featureNameFor("billing"); ok {
		t.Fatal("停用后还读得到 name ⇒ 清理腿会拿一个「已经没人看」的原文算指纹")
	}
	if n := e.PurgeStaleCannedTranslations(ctx); n != 0 {
		t.Fatalf("读不到原文时清理腿删了 %d 行（算不出就不删，与放行分支 2 同一条）", n)
	}
}

// TestLocalizeActionNamesOnlyAtRespondThroat 这一腿只有一个调用点，且必须在那条咽喉上。
//
// AGENTS §一·13 给守卫链立的规矩（"新增守卫一律挂 Respond 这一条链，不许挂进四道回复分支"）
// 对按钮名同样成立：挂进 `respond` 的某一道分支，就是下一次「话术直配的按钮翻了、
// 流程推进的那道没翻」的产地——而两侧各自都"对"，现网表现是某一种回复永远带中文按钮。
//
// 写法是派生式的（扫全包非测试文件），不写死文件清单：新增一个调用点当场红，
// 把调用点挪进 `respond` 也当场红（它落在 Respond 的行区间之外）。
// 反证：在 `respond` 的第 2 道分支里再调一次 ⇒ 计数 2 ⇒ 红。
func TestLocalizeActionNamesOnlyAtRespondThroat(t *testing.T) {
	callPat := regexp.MustCompile(`\.localizeActionNames\(`)
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读包目录失败：%v", err)
	}
	type site struct {
		file string
		line int
	}
	var sites []site
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
		// Respond 的行区间（到下一个顶层 func 为止）
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
			if callPat.MatchString(l) {
				sites = append(sites, site{file: name, line: i + 1})
			}
		}
	}
	if len(sites) != 1 {
		t.Fatalf("按钮名翻译腿有 %d 个调用点（只允许 Respond 咽喉那一个）：%+v", len(sites), sites)
	}
	if sites[0].file != "engine.go" || respondRange[0] < 0 ||
		sites[0].line <= respondRange[0] || sites[0].line > respondRange[1] {
		t.Fatalf("唯一的调用点不在 Engine.Respond 里：%+v（Respond 区间 %v）", sites[0], respondRange)
	}
}
