// ============ quote_guard.go · 职责说明 ============
// 报价的**出栈核验**：模型自己在正文里做算术（乘除、字数换算、现算总额）时，
// 把那一句换成按现值口径写的"不承诺总额"那句。
//
// 为什么提示词那一层不够（★ 092x 红腿二，2026-09-29 现网复问第三条）：
// system_values.go 文件头第 4 条已经把「算术不交给模型」做实了一半——
// 总额由 renderQuoteExamples 在服务端算好、再配一条【报价纪律】禁自乘除。
// 可现网仍抓到这句：「专业模式按 2000×400+7.5 计约 150 积分」，
// 而 150 用当期系数怎么组合都复算不出来。这就是【报价纪律】那条的**另一半**：
// 纪律是请求，出栈核验才是保证（同【回复语言】段与 enforceReplyLang 的关系，
// 见 reply_lang_check.go 文件头）。
//
// 判据只有两条，都是"一眼定性"的形态：
//  1. 句中出现**算式**（数字×数字、数字+数字、带等号的推导）——客户面本就不该出现我们的计价算式，
//     它要么算对要么算错，两种都不许发出去（算对了也不许：客户会拿它去推算别的量级，
//     而字数↔字符之间从来没有官方换算）；
//  2. 句中出现**复算不出的总额**——报价数字必须能由同句出现的源字符数×现值系数复算，
//     或等于系统给过的现算示例／系数／建单固定值；对不上就是模型现编的（对外错报）。
//
// 三条 fail-soft 边界（误删客户正文的代价高于漏掉一句错报价，所以刻意收窄）：
//   - 只管 `Source=="llm"` 的正文：话术直配／流程／兜底是人写的文案，
//     里面的旧价有另一条治理线（scripts/assist_kb_sync.py），不该由数字核验来改；
//   - **现值取不到时只判算式，不判总额**：没有系数就没有"复算得出"的基准，
//     这时候把知识条目里的合法面值（「专业版 299 元」这类）一并抹掉就是误伤；
//   - 替换单位是**整句**而不是抠掉算式：剥掉「2000×400+7.5」只会剩下
//     「专业模式按 计约 150 积分」——半截句子比原句更容易被当成报价。
//
// 每一次替换都记 WARN 并带上原句：这条链的发生率与句式只能从这里看见，
// 下一批要禁的新形态先出现在日志里，而不是先出现在客户截图里。
// =============================================
package engine

import (
	"context"
	"math"
	"regexp"
	"strconv"
	"strings"

	"translator/internal/observability"
)

// quoteArithmeticPat 模型"自己算钱"留下的算式形态。
// 数字之间夹 ×／＊／*／x／✕／＋／+／= 就算命中；两侧都必须贴着数字，
// 所以「2 x 3cm 的图」这类尺寸描述不会被吃进来（x 后面跟的是空格＋单位）。
// 刻意不收除号：「每 1000 源字符」本来就是平台公示的除法口径，出现除法不等于模型在自算。
var quoteArithmeticPat = regexp.MustCompile(`[0-9][0-9,.]*\s*[×✕＊=＝+＋]\s*[0-9]|[0-9][0-9,.]*[*x][0-9]`)

// quoteDelims 分句边界。含换行：列表里每一行单独判，避免一行错连累整段。
const quoteDelims = "。！？!?；;\n"

// quotePointsUnits 计费单位在各语种正文里的写法（用于判断"这个数字是在说积分"）。
// 收的是 pointsTermByLang 的全部值＋中文两种＋英文单复数，
// 判据必须跟【回复语言】段告诉模型的那个词一致，否则刚钉好的术语句会被自己的守卫判成错报。
var quotePointsUnits = []string{
	"积分", "積分", "credit", "Credit", "credits", " Credits", "クレジット",
	"ポイント", "кредит", "кредитов", "créditos", "pontos", "Punkte", "points",
	"Point", "포인트", "คะแนน", "점수",
}

// quoteMoneyUnits 金额单位（判断"这个数字是在说人民币"）。
var quoteMoneyUnits = []string{"元", "¥", "￥", "人民币", "RMB", "CNY", "yuan"}

// quoteCharUnits 计费基数单位（判断"这个数字是源字符数"，用来复算总额）。
var quoteCharUnits = []string{
	"源字符", "字符", "字", "文字", "文字数", "字数", "char", "chars",
	"character", "characters", "文字あたり", "ソース文字",
}

// quoteNumberPat 数字串（允许千分位逗号，因为英文报价常用 "1,000 characters"）。
var quoteNumberPat = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

// guardReplyQuote 出站咽喉上的报价核验（调用点见 engine.go Respond）。
// 参数：answerLang 本轮作答语言（接管后的那个，见 AGENTS §一·11 与 reply_lang.go）。
func (e *Engine) guardReplyQuote(ctx context.Context, answerLang string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" || rep.Source != "llm" {
		return rep
	}
	doc := e.cachedPricing() // 只读缓存，绝不在出站路上现取（见下面注释）
	pieces := splitQuoteSentences(rep.Content)
	replaced := 0
	var before []string
	for i, s := range pieces {
		if !isPricingSentence(s) {
			continue
		}
		// 【go:key】控制序列所在的句子**整句豁免**：postProcess 已经摘过控制序列，
		// 走到这里的正文理论上不该再有它；真出现就是"标记和报价写在同一行"的形态，
		// 换掉整句会顺手把功能卡入口一起删掉——那是把一条对外错报换成一条功能缺失，不划算。
		if strings.Contains(s, goMarkerHead) {
			continue
		}
		hasMath := quoteArithmeticPat.MatchString(s)
		if !hasMath && (doc == nil || allQuoteNumbersVerifiable(s, doc)) {
			continue // 没算式、数字又都复算得出 → 这是合格报价，一个字都不动
		}
		before = append(before, strings.TrimSpace(s))
		// 句尾标点/换行**照原样留下**：分句时它们留在前一段，替换整句时若丢掉换行，
		// 列表就会挤成一行、气泡排版被这条守卫改坏（守卫只许改数字口径，不许动排版）。
		pieces[i] = quoteSafeLine(answerLang, doc, s) + tailNewlines(s)
		replaced++
	}
	if replaced == 0 {
		return rep
	}
	observability.Warn(ctx, "assist.engine 正文出现模型自算的报价，已换成现值口径句（原句进日志攒证据）",
		"lang", canonicalLang(answerLang), "count", replaced,
		"pricing_available", doc != nil, "sentences", strings.Join(before, " | "))
	rep.Content = strings.Join(pieces, "")
	return rep
}

// cachedPricing 读【系统现值】当前缓存里那份结构化系数；没有则 nil。
//
// 为什么这里**不调** systemValuesBlock（那条会带 TTL 刷新）：本函数挂在每一条回复的出栈路上，
// 出站时现取一次 HTTP 等于把访客等回复的时间加上最多 3 秒（两条口的超时预算，见 system_values.go）。
// 缓存是建 prompt 时刚填的那份，判据与模型看到的数字同源，正是核验该用的基准；
// 缓存过期就按"没现值"处理（只判算式），宁可少改一句，也不在出栈路上打网络。
func (e *Engine) cachedPricing() *pricingMetaDoc {
	e.sysValMu.Lock()
	defer e.sysValMu.Unlock()
	return e.sysValDoc
}

// tailNewlines 只保留句尾的**换行**（丢掉末个标点）。
// 换行必须原样还回去——列表行被守卫挤成一行是排版破坏；
// 而标点不用还：三条语种的替换句自己就是带终止标点收尾的完整句，再补一个就成「。。」。
func tailNewlines(s string) string {
	r := []rune(s)
	i := len(r)
	for i > 0 && (r[i-1] == '\n' || r[i-1] == ' ' || r[i-1] == '\t' ||
		strings.ContainsRune("。！？!?；;.", r[i-1])) {
		i--
	}
	var sb strings.Builder
	for _, c := range r[i:] {
		if c == '\n' {
			sb.WriteRune(c)
		}
	}
	return sb.String()
}

// isPricingSentence 这一句是否在报价（含计费单位或金额单位，或直接出现算式）。
// 有算式就算：模型写「2000×400+7.5」时哪怕这句没带单位，也是在算钱。
func isPricingSentence(s string) bool {
	if s == "" {
		return false
	}
	if quoteArithmeticPat.MatchString(s) {
		return true
	}
	return containsAnyUnit(s, quotePointsUnits) || containsAnyUnit(s, quoteMoneyUnits)
}

// containsAnyUnit 单位大小写不敏感地命中任意一条。
// 用 ToLower 而不是 EqualFold 逐个比：这里要在一段文本里找子串，
// EqualFold 只能整串等值，写出来就是一段更绕的循环且漏 "CREDITS"。
func containsAnyUnit(s string, units []string) bool {
	low := strings.ToLower(s)
	for _, u := range units {
		if strings.Contains(low, strings.ToLower(u)) {
			return true
		}
	}
	return false
}

// splitQuoteSentences 按边界切句，**分隔符留在前一段尾部**——
// 这样 join 回去逐字节等于原文，替换某一句不会顺手改掉标点与换行
// （标点被吃掉这种事在中文气泡里一眼就能看出来，属于"修一个缺陷长出一个新缺陷"）。
func splitQuoteSentences(s string) []string {
	var out []string
	cur := strings.Builder{}
	for _, r := range s {
		cur.WriteRune(r)
		if strings.ContainsRune(quoteDelims, r) {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// allQuoteNumbersVerifiable 句子里所有"在说积分/金额"的数字都必须能复算或落在系统给过的数集里。
// 返回假表示至少有一个数字是模型自己算出来的（或它引用的总额与现值系数对不上）。
//
// 可接受的四类数字：
//
//	① 源字符数本身（跟着「字符/字/char」那类单位的，它是**输入量**不是报价，不参与核验）；
//	② 由**同句出现的源字符数**×现值系数算出的总额（含建单固定值）；
//	③ 系统给过的现算示例值（quoteExampleTiers 那三档 × 每个模式，积分与人民币两个口径）；
//	④ 系数与建单固定值本身（「每 1000 源字符 = 200 积分」是公示单价，必须放行——
//	   这条最容易踩：它按②算会差出一个建单固定值，判成错报就把合格报价改了）。
func allQuoteNumbersVerifiable(s string, doc *pricingMetaDoc) bool {
	chars := numbersWithUnits(s, quoteCharUnits)
	tolerance := quoteAcceptSet(doc, chars)
	for _, n := range numbersWithUnits(s, quotePointsUnits) {
		if !tolerance.matchesPoints(n, chars, doc) {
			return false
		}
	}
	for _, m := range numbersWithUnits(s, quoteMoneyUnits) {
		if !tolerance.matchesMoney(m, doc) {
			return false
		}
	}
	return true
}

// acceptValues 系统给过的数字集合（积分口径与人民币口径分开存，判据不同容差不同）。
type acceptValues struct {
	points []float64
	money  []float64
}

// quoteAcceptSet 算出上面第③④类数字。
// 参数：chars 是同句出现的源字符数（第②类要按它现算，所以必须传进来）。
func quoteAcceptSet(doc *pricingMetaDoc, chars []float64) acceptValues {
	var av acceptValues
	if doc == nil {
		return av
	}
	add := func(p float64) { av.points = append(av.points, p) }
	for _, m := range doc.Modes {
		add(m.PointsPer1kChars) // ④ 系数本身
		add(m.PointsFixed)      // ④ 建单固定值
		for _, tier := range quoteExampleTiers {
			p := m.PointsPer1kChars*float64(tier)/1000 + m.PointsFixed // ③ 现算示例
			add(p)
			av.money = append(av.money, round2(p*doc.PointsPriceMoney))
		}
		// ② 同句字符数对应的总额
		for _, c := range chars {
			p := m.PointsPer1kChars*c/1000 + m.PointsFixed
			add(p)
			av.money = append(av.money, round2(p*doc.PointsPriceMoney))
		}
	}
	av.money = append(av.money, doc.PointsPriceMoney) // 「1 积分 ≈ 0.099668 元」这条单价也是系统给的
	return av
}

// matchesPoints 积分数字是否在容差内成立。
// 容差 max(0.5, 2%)：模型写「约 208 积分」而现算是 207.5 属正常四舍五入，
// 不放这一档就会把合格的"约"字句判成错报（那是误伤，不是抓到问题）。
func (av acceptValues) matchesPoints(n float64, chars []float64, doc *pricingMetaDoc) bool {
	for _, c := range chars {
		for _, m := range doc.Modes {
			if nearly(m.PointsPer1kChars*c/1000+m.PointsFixed, n) {
				return true
			}
		}
	}
	for _, v := range av.points {
		if nearly(v, n) {
			return true
		}
	}
	return false
}

// matchesMoney 人民币数字是否在容差内成立（容差 max(1, 5%)：金额是"约"出来的，比积分更松一档）。
func (av acceptValues) matchesMoney(n float64, doc *pricingMetaDoc) bool {
	for _, v := range av.money {
		if nearlyMoney(v, n) {
			return true
		}
	}
	// 「X 积分 ≈ Y 元」里的 X 已经过积分侧核验时，Y 由它换算也算复算得出
	for _, p := range av.points {
		if nearlyMoney(round2(p*doc.PointsPriceMoney), n) {
			return true
		}
	}
	return false
}

// nearly 积分口径容差（绝对 0.5 或相对 2%，取宽的那个）。
func nearly(want, got float64) bool {
	return math.Abs(want-got) <= math.Max(0.5, math.Abs(want)*0.02)
}

// nearlyMoney 金额口径容差（绝对 1 元或相对 5%，取宽的那个）。
func nearlyMoney(want, got float64) bool {
	return math.Abs(want-got) <= math.Max(1, math.Abs(want)*0.05)
}

// numbersWithUnits 取「**紧跟其后的第一段文字**里出现指定单位」的数字——那段文字到下一个数字为止。
//
// 为什么看后面而不是前面：报价句的两种写法都要覆盖——「207.5 积分」（数字在前）与
// 日文「207.5ポイント」（同形）。
//
// ★ 为什么窗口必须**截到下一个数字**（而不是原写的"固定 14 个字符"）：
// 「每 1000 源字符 = 8 积分」这一句里，1000 后面第 14 个字符窗口能伸到下一个数字之后的「积分」，
// 于是把公示的**字符基数**当成一笔复算不出的报价，把系统自己发给模型看的示例句判成错报。
// 按"最近的单位"判才是这句话本来的读法：数字属于谁，看它和单位之间有没有插进另一个数字。
// 窗口另设 32 字符上限，防止数字后面整段没有别的数字时把后半篇单位都算到它头上。
func numbersWithUnits(s string, units []string) []float64 {
	var out []float64
	low := strings.ToLower(s)
	locs := quoteNumberPat.FindAllStringIndex(s, -1)
	for k, loc := range locs {
		tail := low[loc[1]:]
		if k+1 < len(locs) {
			if cut := locs[k+1][0] - loc[1]; cut >= 0 && cut < len(tail) {
				tail = tail[:cut] // 到下一个数字为止
			}
		}
		if rt := []rune(tail); len(rt) > 32 {
			tail = string(rt[:32])
		}
		for _, u := range units {
			if strings.Contains(tail, strings.ToLower(u)) {
				if f, err := strconv.ParseFloat(strings.ReplaceAll(s[loc[0]:loc[1]], ",", ""), 64); err == nil {
					out = append(out, f)
				}
				break
			}
		}
	}
	return out
}

// quoteSafeLine 替换句：按语种给"不承诺总额、只指向现值"的那句。
//
// 只有当**句中能认出具体模式名**（专业／快速）且现值在位时，才带上该模式的单价系数；
// 认不出模式就一句不带数字的指路话——带错档的系数比不带数字更糟（那是新的对外错报）。
// 语种只分三档（简中／日文／其余用英文），其余语种回落英文是既有口径
// （同 lang→en→zh 回退链）；日文那档的措辞用「料金ページ」而不用「套餐页面」直译。
func quoteSafeLine(answerLang string, doc *pricingMetaDoc, sentence string) string {
	c := canonicalLang(answerLang)
	mode := quoteModeInSentence(sentence, doc)
	withRate := mode != nil && doc != nil

	switch c {
	case "ja":
		if withRate {
			return "具体的なポイントは実際のソース文字数で決まります。" +
				quoteModeNameJa(mode.Code) + "はソーステキスト 1,000 文字あたり " + trimNum(mode.PointsPer1kChars) +
				" ポイント、さらに 1 件の依頼ごとに " + trimNum(mode.PointsFixed) +
				" ポイントが必要です。ファイルを送っていただければ正確な総額をお出しします。"
		}
		return "具体的なポイントは実際のソース文字数で決まります。料金のページの最新表示を基準にし、" +
			"ファイルを送っていただければ正確な総額をお出しします。"
	case "zh", "zh_hant", "":
		if withRate {
			return "具体额度按实际源字符数计：" + quoteModeNameZh(mode.Code) + "每 1000 源字符 = " +
				trimNum(mode.PointsPer1kChars) + " 积分，另每次建单固定 " + trimNum(mode.PointsFixed) +
				" 积分。把文件发我，我按实测字符数给你算准总额。"
		}
		return "具体额度按实际源字符数计，以套餐页面公示的现值为准。把文件发我，我按实测字符数给你算准总额。"
	default:
		if withRate {
			return "The exact cost depends on the real source-character count: " +
				quoteModeNameEn(mode.Code) + " costs " + trimNum(mode.PointsPer1kChars) +
				" credits per 1,000 source characters plus " + trimNum(mode.PointsFixed) +
				" credits per job. Send me the file and I can size it precisely."
		}
		return "The exact cost depends on the real source-character count, so please take the current figures " +
			"on the pricing page as the reference. Send me the file and I can size it precisely."
	}
}

// quoteModeInSentence 从句子里认出模式（专业／快速）。认不出返回 nil，交由调用方走不带数字那句。
func quoteModeInSentence(s string, doc *pricingMetaDoc) *pricingMode {
	if doc == nil {
		return nil
	}
	want := ""
	low := strings.ToLower(s)
	switch {
	case strings.Contains(s, "专业"), strings.Contains(low, "pro mode"), strings.Contains(low, "professional"):
		want = "pro"
	case strings.Contains(s, "快速"), strings.Contains(low, "fast mode"), strings.Contains(low, "quick mode"):
		want = "fast"
	default:
		return nil
	}
	for i := range doc.Modes {
		if doc.Modes[i].Code == want {
			return &doc.Modes[i]
		}
	}
	return nil
}

// quoteModeNameZh／quoteModeNameEn／quoteModeNameJa 模式名的三档写法。
// 单一事实源仍是现值接口给的 code（fast/pro），这里只负责"把它说成对方语言的词"，
// 绝不另造第三种档位名——那会和套餐页面打架（同 AGENTS §一·5 那条单一事实源）。
func quoteModeNameZh(code string) string { return modeLabel(code) }

// quoteModeNameEn 见 quoteModeNameZh。
func quoteModeNameEn(code string) string {
	switch code {
	case "pro":
		return "Pro mode"
	case "fast":
		return "Fast mode"
	default:
		return "This mode"
	}
}

// quoteModeNameJa 见 quoteModeNameZh。
func quoteModeNameJa(code string) string {
	switch code {
	case "pro":
		return "プロモード"
	case "fast":
		return "高速モード"
	default:
		return "このモード"
	}
}

// round2 已收在 system_values.go（同一口径只留一份，这里不再声明）。
