// ============ canned_guard.go · 职责说明 ============
// 挂件**首屏固定话术**（欢迎词／chips）的两条 096x 防线，都只在 canned 这一路上生效：
//
//	① 按源文投口径（`srcTopicsOf` ＋ localize.go 的 `translateContract`）——
//	   原文没提品牌名，就不给模型那句「品牌名一律写作 X」的邀请；原文没提计费单位，就不拼那一句；
//	② 落库之前的出栈闸（`cannedOutboundReject`）——
//	   「翻译调用成功」不等于「产物可用」：还带着没翻的中文词、或者凭空多出品牌名的那一稿，
//	   **既不发给访客、也不写进缓存**（发给访客的是中文原文，缓存一行不动）。
//
// ★ 096x-1（2026-10-01 现网 12 语种逐个复问，只读探针，读数留档
//
//	《发布前E2E_UAT_20260926/证据/1001_08AF_真机往返/现网复问_run2_095x.out》）。四条实证：
//	- chips 的中文原文四条里**没有一条**提到品牌名（「怎么上传文件翻译？,积分怎么收费？,
//	  企业术语库怎么建？,支持哪些格式？」），可日文档四条**每条尾部**各粘一个「能言」、
//	  俄文档四条**每条开头**各挂一个 "LangCross: "；
//	- 英文欢迎词里留着 `credits充值`（补翻被行数判据拒掉之后，那份**带残片的稿子照样落了库**）；
//	- 韩文欢迎词把品牌名包成 `⟨LangCross⟩`（模型把占位符的方括号换成了尖括号，
//	  还原步命不中，而 `stripBrandTokenResidue` 的前置判断只认 "BRAND" 这个词 ⇒ 整条早退）；
//	- 韩文 chips 整串回落中文（行数判据那条，属**设计内**，本文件不碰）。
//
// 这四条里前三条有一个共同点：**它们在代码眼里都是"成功译文"**。
// 所以既没走失败分支那行 WARN（日志里完全静默），也没有任何一道闸挡在 `SetConfig` 之前——
// 于是坏形态不是"这一次显示错了"，而是**被缓存成访客长期看到的那一屏**
// （AGENTS.md §13 末那条 ⚠️ 登记的就是这个盲区：四道守卫的射程只有对话正文）。
//
// ★ 为什么①是根因、②仍然必须有：
//
//	①治的是"模型为什么会长出这个词"——旧版无条件拼「品牌名一律写作『X』」，
//	对翻译模型那是一句"本轮要用这个词"的邀请，四条短问句里没有落点，就粘到句首／句尾
//	（同"提示词是概率性请求不是保证"那条老账，只是这次请求的内容是**多加一个原文没有的词**）。
//	②治的是"模型照不办怎么办"——提示词永远只是概率，改口径不能替代出栈判据；
//	没有②，下一次换一个小模型就是把 ① 的成果吃掉。
//
// ★ 0AF（2026-10-01）为什么闸门拒掉的那一稿**不触发后台补翻**（同步腿失败却会触发，见 localize_async.go）：
//
//	上游失败＝没拿到产物，重拨一次的期望收益明确（现网那条冷语种真的只是慢）；
//	闸门拒稿＝**上游答得很好，只是产物不合格**——同一句提示词立刻重拨一次期望收益接近零，
//	代价却是"模型系统性翻坏这一语种"时把上游打成重拨风暴（N 个访客 × 每人两次）。
//	留在原形态由**访客流量自带节流**：每次 greet 各判一次，模型哪天改好了当天就生效。
//	⇒ 判据不变、射程不变，只是这一条分工要在两侧注释里对齐，否则后人会以为后台腿漏接了这一档。
//
// ★ 为什么拒绝的那一稿**不落缓存**（这一条刻意与"上游失败"同形）：
//
//	失败侧本来就没有负缓存（`localize` 里 err 分支直接 return text），每次 greet 重拨一次；
//	给"模型改好了"这件事留一条自动生效的路，比省一次往返重要——
//	负缓存要么永久投中文（访客永远看不到修好的那一天），要么得再造一套 TTL 状态机。
//	代价写在注释里给后人：**这一路被拒时会退化成每次 greet 现翻**，
//	现网真出现"首屏慢"就从这条日志的频次判断该不该加负缓存，而不是先加。
//
// ★ 0AR 第 4 波（2026-10-05）闸门从三条补到**五道拒绝 ＋ 两道纯观测**，
// 因为现网库里有三条**指纹匹配、正在被 HIT** 的坏行，而它们在旧三道判据眼里全是"成功译文"：
//   - `i18n:chips:ar` 尾部多一行 `---`（模型照抄输入围栏）⇒ 走 `canned_separator_residue`／
//     `canned_line_count`；**这一条的真正杀伤在"写缓存之后才被丢弃"**，所以条数契约同时前移
//     （见 cannedOutboundReject 的 wantLines 与 localize.go 的作废腿）；
//   - `i18n:welcome:th` 留着没替换的字面量 `⟨BRAND⟩` ⇒ `canned_placeholder_residue`；
//   - `i18n:welcome:ko` 品牌名整个脱落（`안녕하세요, 의 AI…` 那句连语法都是破的）⇒ `canned_brand_dropped`
//     **只出声不拦**（曾做成拒绝判据，实测会把合格的英文首屏整体退回中文，见那一档的注释）；
//   - `i18n:chips:th` 是纯泰文却拼错了音译（`อัปโหร์`）⇒ 判据抓不到，先由 `canned_script_impure`
//     **只记读数不拦**（误拒的代价是整段回中文，比乱码更糟；阈值等现网分布）。
//
// ⚠️ 与"品牌缺失只 WARN 不判失败"那条 092x 口径的分工：**对话正文那一路一字未改**，
// 而 canned 这一路经实测也**只能观测**（理由同上那条证伪记录）——两族现在同口径，
// 差别只在 canned 多一行带 `stage` 的日志与一个计数读数。别把它再改回拒绝判据。
//
// 中文原文难看，但它是真话；半句机翻＋一个客户公司从没说过的自称，是对外错报。
// =============================================
package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// srcTopics 「这一枪的源文里到底提没提这两个话题」——决定给模型哪句口径（见 translateContract）。
//
// 收成一个小结构体而不是两个 bool 参数：口径段现在有两类话题，将来加第三类（比如"套餐名"）时，
// 参数列表会逼着所有调用点重排一次，而字段名不会——漂移通常就发生在"顺手少传一个 bool"那种调用上。
type srcTopics struct {
	brand  bool // 源文提到品牌名（中英文两种写法任一，见 brandSourceForms）
	points bool // 源文提到计费单位「积分」
}

// pointsSourceForms 源文里"提到计费单位"的写法。
// ⚠️ 只按**中文词形**判：canned 的源文永远是运营写的中文（configs.welcome／quick_chips），
// 而 credits／ポイント 这些是**译文侧**的词，拿它们判"源文提没提"会把话题判断变成内容判断。
var pointsSourceForms = []string{"积分", "積分"}

// srcTopicsOf 扫一遍源文，看这两个话题在不在。
// 判据是**字面 Contains**，不做同义扩展（"费用／点数"不算提到积分）：
// 宁可漏判成"不提这条口径"（模型自己挑个说法，还有判残与剥复述两道闸兜着），
// 不可误判成"必须用这个词"（那正是本文件要消灭的注入形态）。
func srcTopicsOf(text string) srcTopics {
	var t srcTopics
	for _, f := range brandSourceForms {
		if strings.Contains(text, f) {
			t.brand = true
			break
		}
	}
	for _, f := range pointsSourceForms {
		if strings.Contains(text, f) {
			t.points = true
			break
		}
	}
	return t
}

// replyRepairTopics 对话正文补翻那一路的口径档：**两条都拼**（＝ 096x 之前的行为，一字未变）。
//
// 为什么这一路不做"按源文筛"：它的输入已经是**目标语言**的正文，没有中文原文可判"这句本来提没提品牌名"，
// 而品牌名与计费单位恰恰是回答里最可能**需要写对**的两个词（被判点名的残片里就带着它们）。
// 拿"原文没提"去约束这一路，等于给模型一张删掉品牌名的许可证——那是把 092x 红腿三修出来的
// 品牌还原又反向拆掉。canned 那一路能做筛选，是因为它有中文源文这个事实可问。
func replyRepairTopics() srcTopics { return srcTopics{brand: true, points: true} }

// canned 出栈闸的拒绝分档（★ 096x-1 立三条；★ 0AR 第 4 波补到六道 ＋ 一道纯观测档）。
// 与 han_residue.go 那七个 reject* 同族：**日志里看得见的名字就是对外排障契约**，
// 逐字钉进 canned_guard_test.go，改字面量当场红（"拒绝但说不出为什么"是 094x 修过的账）。
const (
	cannedRejectEmpty       = "canned_empty"               // 清洗后为空（理论上 translateOnce 已拦，留这一腿防退化）
	cannedRejectResidue     = "canned_han_residue"         // 还带着没翻的中文词（补翻没救回来那一档，现网 `credits充值`）
	cannedRejectBrandAdded  = "canned_brand_injected"      // 原文没提品牌名，译文里却冒出品牌名（现网 ja 尾粘「能言」／ru 前挂 "LangCross:"）
	cannedRejectLineCount   = "canned_line_count"          // ★ 0AR：行数与源文不符（多出来那一行常是模型照抄的输入围栏）
	cannedRejectPlaceholder = "canned_placeholder_residue" // ★ 0AR：占位符／花括号／`<IDENT>` 这类**内部记号**留在客户屏幕上
	cannedRejectSeparator   = "canned_separator_residue"   // ★ 0AR：独立成行的分隔线残渣（`---`／`***`／`===`／空的 `【】`）
	// ★ 0AR **纯观测档，不参与不合格判定**（为什么先不拦见 cannedScriptImpurity）
	cannedScriptImpure = "canned_script_impure" // 目标语脚本不纯（泰文行里混汉字／谚文行里一个谚文都没有）
	// cannedBrandDropped ★ 0AR 新增的**观测档**：原文提了品牌名、译文里一个品牌痕迹都没有
	// （现网 ko `안녕하세요, 의 AI 어시스턴트 👋`——名字被吃掉、`의` 悬在半空）。
	// 它一度被写成第六道**拒绝**判据，随即被现有用例证伪：一句完全合格的
	// `Hi, what can I translate for you?`（模型换了说法、不自称品牌名）会被判不合格
	// ⇒ 整个英文首屏退回中文。这正是本文件取向那句「只砍模型多出来的东西，绝不砍模型没写够的东西」
	// 要防的形态，也与 092x「品牌缺失只 WARN 不判失败」同口径（见 restoreBrandAfterTranslation）。
	// ⇒ 射程定为**看得见、不拦正文**；现网那一行由「抬 rev ＋ 一次性清库」处理（本批第 ②③ 步）。
	cannedBrandDropped = "canned_brand_dropped"
	// cannedRepaired ★ 0AR **修正腿生效**的读数档：它不是一种失败（产物已可用、也已回写），
	// 出现的意义只有排障——"这一语种的首屏是被清洗救回来的"，说明上游那一稿本身还带坏形态，
	// 该去查提示词或换模型，而不是长期依赖这一腿。逐字钉进单测（同其余分档）。
	cannedRepaired = "canned_repaired"
)

// cannedOutboundReject 译文**写进 configs 之前**的最后一道闸。
// 返回「分档名 + 命中的具体内容 + 是否不合格」；不合格时调用方按 fail-soft 出中文原文且**不落缓存**。
//
// wantLines>0 时**先按行数判**（★ 0AR：chips 的条数契约前移到写缓存之前）。
// 这个数由调用方 `LocalizeChips` 传下来，刻意不在这里再拆一次 csv——两处各数一遍
// 就是下一次"两处结果不一样时到底谁说了算"的争吵（§一·11 那条「三把尺子」的同族形态）。
// welcome 传 0＝不判行数。
//
// ★ 0AR 新增四道的现网实证（读数留档《发布前E2E_UAT_20261003/修改文档》§六 ㉞／§七 ⑰）：
//   - `i18n:chips:ar`＝4 行正确阿语 **＋ 第 5 行 `---`** ⇒ 回到 `LocalizeChips` 被条数校验丢弃，
//     而丢弃分支**零日志、缓存行也不作废** ⇒ 那一行既用不上、又永不重翻、又不出声（自锁三件套），
//     每一个阿语访客拿到的是 4 条中文 chips；
//   - `i18n:welcome:th`（旧代孤儿行）里留着**没替换的字面量 `⟨BRAND⟩`** ＋ 中文残渣 `可以直接`；
//   - `i18n:welcome:ko`＝`안녕하세요, 의 AI 어시스턴트 👋`——品牌名整个脱落，
//     句子被吃出一个介词悬空（`의` 前面本该是品牌名），**当代写入、正在 HIT**。
//
// 这四条在旧版代码眼里**全是"成功译文"**：闸门只有三道，缺哪一道就漏哪一族的形态，
// 而漏下去的那一稿会常驻首屏（这就是 canned 与对话正文最大的区别：正文错了下一轮能重问，
// 缓存错了要等运营手工动库）。
//
// 判据的取向仍是同一句话：**只砍模型多出来的东西，绝不砍模型没写够的东西**。
// 所以这里没有"译文太短"这类判据——判多了就是把访客退回看中文，方向反了。
// ⚠️ 曾经把 `canned_brand_dropped` 写成这条取向的"有意例外"（拒绝判据），本批实测把它翻回了
// **观测档**（理由与证伪读数见常量 cannedBrandDropped）：拿"没写够"当拒绝理由，
// 代价是整个语种的首屏退回中文，比少一个自称严重一个量级——这正是 092x 当年翻档的同一个判断。
//
// ★ 0AR 第 4 波还添了闸门内部的**第一道确定性修正腿**（`repairCannedOutbound`）：
// 现网 ko 那一行落库时是 `⟨LangCross⟩`、ar 那一行第 5 个 `---` ——两个都属于
// "我们已知的形态、且确定性地能改回去"。旧流程是**先判不合格、再想补救**，于是这类稿子
// 明明一行就能救活却被整条退回中文（代价＝该语种每次 greet 白等 8 秒 ＋ 缓存永不命中）。
// 现在改成"先就地修、修完再判"：判据一条没放松，只是判的是**清洗后的最终形态**，
// 而那个形态也正是访客会看到、缓存会存下的那一串字节。
//
// ★ 所以第一返回值 `final` 就是"要发给访客、要写进缓存"的那一串（可能与传进来的 out 不同）。
// 调用方**必须**用 final，不许继续发 out——闸门判的是清洗后的字节，发出去的却是清洗前的，
// 等于"判据放行的那一稿从来没人看过"（同 §一·13「守卫只在 Respond 咽喉」那条纪律的反面）。
// bad 真时 final 无意义（调用方按 fail-soft 出中文原文）。
func cannedOutboundReject(uiLang, src, out string, t srcTopics, wantLines int) (final, reason, detail string, bad bool) {
	if strings.TrimSpace(out) == "" {
		return out, cannedRejectEmpty, "", true
	}
	// ★ 0AR 修正腿：把"已知且能确定性改回去"的形态先改掉，再让**改后的那一串**去过分档。
	// ⚠️ 只有"汉字残渣"与"凭空多出的品牌名"两档看得到修正结果——这两档修的正是
	// 品牌名/内部记号这类**我们塞进提示词的东西**；而 `hanResidueRuns` 与 `strayBrandForm`
	// 之外的判据（行数、装饰线、占位符）一律按原稿判：改坏客户正文的风险比救回一稿的收益大
	// （取向同 §一·13 那句"宁可漏改，不可误改"，见 brandCJKLocalesForFix 只认 ja 的同族判据）。
	repaired := repairCannedOutbound(uiLang, out)
	// 行数先判：多出来的那一行十有八九就是装饰线，先按条数拦下来比逐形认残渣更省事，
	// 而且这条判据必须**在写缓存之前**——旧形态是先落库、回到 LocalizeChips 才丢弃，坏形态因此常驻。
	if wantLines > 0 {
		if got := lineCountOf(out); got != wantLines {
			return repaired, cannedRejectLineCount,
				"got=" + strconv.Itoa(got) + ",want=" + strconv.Itoa(wantLines), true
		}
	}
	// 内部记号残渣：那是机制外露，不是内容（任何语种、任何形态都不该出现在客户屏幕上）
	if f := strayPlaceholderForm(out); f != "" {
		return repaired, cannedRejectPlaceholder, f, true
	}
	if f := straySeparatorLine(out); f != "" {
		return repaired, cannedRejectSeparator, f, true
	}
	// 判残用的是与补翻**同一把尺子**（hanResidueRuns）：补翻成功时这里必然空手，
	// 补翻被拒时这里就是那条被拒的稿子——不另立第二把尺子，否则两边永远会长歪。
	if leaks := hanResidueRuns(uiLang, src, repaired); len(leaks) > 0 {
		return repaired, cannedRejectResidue, strings.Join(leaks, ","), true
	}
	if !t.brand {
		if f := strayBrandForm(repaired); f != "" {
			return repaired, cannedRejectBrandAdded, f, true
		}
	} else if !hasAnyBrandForm(uiLang, out) {
		// ★ 0AR **只出声、不拦**（这一档曾写成拒绝，被现有用例证伪后翻档，理由见 cannedBrandDropped）：
		// 原文提了品牌名而译文一个痕迹都没有——现网 ko 那一行就是这一档，
		// 但同一判据也会把「换了一种说法、没自称品牌名」的合格英文欢迎词一起挡成中文首屏。
		// 正文照发、缓存照写；这一行 WARN ＋ /health 的计数是它唯一的露面机会。
		return repaired, cannedBrandDropped, brandNameFor(uiLang), false
	}
	return repaired, "", "", false
}

// repairCannedOutbound 出栈前的**确定性清洗腿**（★ 0AR 第 4 波）：只改"我们已知的形态"，
// 一行不改语义、不补内容、不调模型。
//
// 三件事全部复用既有工具函数（同 §一·13 那条「两条腿共用一把尺子」的口径——这里再写一份
// 括号表／品牌归一表，就是下一次"清洗侧改进了、补翻侧还在旧表"的产地）：
//  1. `normalizeBrandForms`：拼音错形（nengyan／NengYan…）与 ja 档的「能与」→ 该语种档写法。
//     ⚠️ 该函数自带语种白名单（brandCJKLocalesForFix 只认 ja），中文档的「能与」是正常词，不动。
//  2. `stripBrandTokenResidue`：被模型改坏括号的内部记号 `⟦BRAND⟧`／`[BRAND]` 那一带残渣。
//  3. `stripBrandDecorBrackets`：紧贴品牌名的那一对装饰括号（现网实证 `⟨LangCross⟩`）。
//
// ⚠️ **不在此处**动分隔线、行数、汉字残渣：那三类是"多出来的内容"，删谁一行、留谁一行都是
// 在替客户决定正文长什么样（现网 ar 那一行的正确处置是**作废重翻**，不是把第 5 行裁掉——
// 万一那 5 行里第 3 行才是错的呢）。取向同 brandCJKLocalesForFix 那句"宁可漏改，不可误改"。
//
// 幂等性：三条都是"改到不动为止"的形态，所以 final 再过一次这道腿结果不变
// ——这一条有断言（TestCannedRepairIsIdempotent），因为读侧会拿 final 回写缓存，
// 不幂等的清洗会让同一行每次读都变一点（那是把缓存写成了流，不是缓存）。
func repairCannedOutbound(uiLang, out string) string {
	if strings.TrimSpace(out) == "" {
		return out
	}
	cur, _ := normalizeBrandForms(out, uiLang)
	cur = stripBrandDecorBrackets(stripBrandTokenResidue(cur))
	return cur
}

// hasAnyBrandForm 译文里有没有**任何一种**品牌痕迹（该语种档／两种源文写法／拼音错形／日文「能与」）。
//
// 判"丢失"用宽口径是刻意的：只要名字还在，写法不对自有别的腿去治（normalizeBrandForms／判残补翻），
// 这里只认"整块没了"那一种——窄口径会把「能与」这种好稿子判成不合格，代价是访客退回看中文。
// ⚠️ 汉字族档（zh／zh_hant／ja 的品牌名本来就是「能言」，见 brandKanjiLocales）不许照旧文再"修"一遍：
// ja 那一档的真缺陷只有「四条 chips 每条尾部各粘一个能言」这种**注入**形态，正向对照必须放行。
func hasAnyBrandForm(uiLang, text string) bool {
	if strings.Contains(strings.ToUpper(text), "BRAND") {
		return true // 占位符残渣也算"痕迹还在"（它由 canned_placeholder_residue 那一档处理，两档不重复报）
	}
	if n := brandNameFor(uiLang); n != "" && strings.Contains(text, n) {
		return true
	}
	for _, f := range brandSourceForms {
		if strings.Contains(text, f) {
			return true
		}
	}
	for _, p := range brandPinyinForms {
		if strings.Contains(text, p) {
			return true
		}
	}
	return strings.Contains(text, brandJaMisForm) // 「能与」＝名字在但字形错：不算丢失
}

// strayPlaceholderForm 返回正文里第一个"内部记号残渣"，没有则空串。
//
// 三类都是**我们自己塞进提示词的记号**，出现在客户屏幕上就是机制外露
// （同【go:key】控制序列必须摘干净那条）：
//   - 原样留着的 ⟦BRAND⟧，以及被模型改坏括号的 BRAND（含 096x 现网那对尖括号的同族形态）；
//   - `{{…}}` 模板占位（小模型会把"未替换的模板"当成一种格式）；
//   - `<IDENT>` 形占位。**刻意要求内容里至少有一个大写字母**（`<BRAND>`／`<API_KEY>` 这类），
//     否则正文里一个 `<html>` 或一段数学式就被判成不合格——误拒的代价是整段回中文。
func strayPlaceholderForm(text string) string {
	if strings.Contains(text, brandToken) {
		return brandToken
	}
	if m := brandTokenResiduePat.FindString(text); m != "" {
		return m
	}
	if i := strings.Index(text, "{{"); i >= 0 {
		if k := strings.Index(text[i+2:], "}}"); k >= 0 {
			return firstRunes(text[i:i+k+4], 40)
		}
		return firstRunes(text[i:], 40)
	}
	if m := placeholderAnglePat.FindString(text); m != "" {
		return m
	}
	return ""
}

// placeholderAnglePat `<IDENT>` 形占位符，内容至少含一个大写字母（不许吃进正文里的普通尖括号）。
var placeholderAnglePat = regexp.MustCompile(`<[A-Za-z0-9_]*[A-Z][A-Za-z0-9_]*>`)

// straySeparatorLine 返回第一条"独立成行的装饰残渣"，没有则空串。
//
// 判据只认**整行都是分隔符**这一种形态（`---`／`***`／`===`／`———`／空的 `【】`），
// 不认行内出现——「——」在正文里是正常的破折号，「=」在算式里是正常的符号，
// 把它们判成残渣就是把好译文杀掉（取向同上面那句「只砍模型多出来的东西」）。
// 现网实证的形态正是**整行** `---`：模型把提示词里那对「---\n源文\n---」的**输入围栏**照抄进了输出
// （见 translateOnce 拼 prompt 的那两句），所以这一条判据的射程不是"装饰癖"，是**复述输入格式**。
func straySeparatorLine(text string) string {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if emptyBracketLinePat.MatchString(line) {
			return line
		}
		if sepLinePat.MatchString(line) {
			return line
		}
	}
	return ""
}

// 「整行不成内容」的两条形态判据，供 `straySeparatorLine` 逐行问（出栈闸的 `canned_separator_residue` 档）。
//
// 这一族是 0AR 第 4 波 ① 收进来的：模型译文里最常见的一种坏形态是**分了节却没给出内容**——
// 只剩一行 `---` 或一对空括号。它清洗不掉（不是回声标记、不是汉字残渣），判残也抓不到（整行都是合法字符），
// 所以单独给它一档 reason，让客户看到的是一整段中文原文而不是一屏骨架。
var (
	// sepLinePat 整行只有分隔字符且 ≥2 个（单个 `-` 可能是列表项，不算残渣）。
	sepLinePat = regexp.MustCompile(`^[-=*~_—–─━·\s]{2,}$`)
	// emptyBracketLinePat 整行是一对空括号（`【】`／`()`／`[]`）：模型分了节却没给出内容。
	emptyBracketLinePat = regexp.MustCompile(`^[【\[（(]\s*[】\]）)]$`)
)

// cannedScriptImpurity ★ 0AR **纯观测腿**：目标语脚本纯度读数，**不参与不合格判定**。
//
// 为什么先不拦（台账 ⑰ 修法第 4 条自己写下的取舍）：误拒的代价是"整段回中文原文"，
// 比现网那一屏泰文乱码**更糟**——乱码里至少有几个词是对的，中文整段是零可用性；
// 而阈值（那一行 ratio 线）在没有真实分布之前就是拍脑袋。所以这一腿只做一件事：
// **把可疑形态连读数一起记进日志**，让下一批按现网真实分布定阈值
// （手法同 unstrippedAsides 那套「只记 WARN 不改正文」的观测腿）。
// 现网实证：`i18n:chips:th`＝`วิธีการอัปโหร์…`（"上传"被写成音译的 `อัปโหร์`，
// 脚本是纯泰文、判残与补翻都抓不到，界面却是乱码）⇒ 这一族缺陷**拦不住，只能先看得见**。
//
// 读数口径：目标脚本字数／非空白总字数 ＋ 汉字字数。拉丁（AI／LangCross）本就合法，不单独报。
func cannedScriptImpurity(uiLang, text string) (bool, string) {
	var lo, hi, lo2, hi2 rune
	switch canonicalLang(uiLang) {
	case "th":
		lo, hi = 0x0E00, 0x0E7F
	case "ko":
		lo, hi, lo2, hi2 = 0xAC00, 0xD7A3, 0x1100, 0x11FF
	default:
		return false, "" // 其余语种先不攒读数（日文含汉字是正常形态，混在这里会把观测腿刷成噪音）
	}
	var target, han, total int
	for _, r := range text {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			continue
		}
		total++
		switch {
		case r >= 0x4E00 && r <= 0x9FFF:
			han++
		case r >= lo && r <= hi, lo2 != 0 && r >= lo2 && r <= hi2:
			target++
		}
	}
	if total == 0 {
		return false, ""
	}
	ratio := float64(target) / float64(total)
	return han > 0 || ratio < 0.5,
		fmt.Sprintf("target_script=%d total=%d han=%d ratio=%.2f", target, total, han, ratio)
}

// strayBrandForm 返回正文里第一个"品牌名痕迹"，没有则空串。
//
// ⚠️ 刻意**不收** brandJaMisForm「能与」：中文源文里「能与…」是完全正常的说法（那时 t.brand 是 false），
// 收进来就是把一条好译文判成不合格、把访客退回看中文。判"凭空多出品牌名"只认那些
// 在任何语种里都不可能是普通词的写法（品牌名本身＋它的拼音）。
func strayBrandForm(text string) string {
	for _, f := range brandSourceForms {
		if strings.Contains(text, f) {
			return f
		}
	}
	for _, p := range brandPinyinForms {
		if strings.Contains(text, p) {
			return p
		}
	}
	return ""
}
