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
// 中文原文难看，但它是真话；半句机翻＋一个客户公司从没说过的自称，是对外错报。
// =============================================
package engine

import "strings"

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

// canned 出栈闸的拒绝分档（★ 096x-1）。
// 与 han_residue.go 那七个 reject* 同族：**日志里看得见的名字就是对外排障契约**，
// 逐字钉进 canned_guard_test.go，改字面量当场红（"拒绝但说不出为什么"是 094x 修过的账）。
const (
	cannedRejectEmpty      = "canned_empty"          // 清洗后为空（理论上 translateOnce 已拦，留这一腿防退化）
	cannedRejectResidue    = "canned_han_residue"    // 还带着没翻的中文词（补翻没救回来那一档，现网 `credits充值`）
	cannedRejectBrandAdded = "canned_brand_injected" // 原文没提品牌名，译文里却冒出品牌名（现网 ja 尾粘「能言」／ru 前挂 "LangCross:"）
)

// cannedOutboundReject 译文**写进 configs 之前**的最后一道闸。
// 返回「分档名 + 命中的具体内容 + 是否不合格」；不合格时调用方按 fail-soft 出中文原文且**不落缓存**。
//
// 三条判据的取向全是同一句话：**只砍模型多出来的东西，绝不砍模型没写够的东西**。
// 所以这里没有"译文太短""没带品牌名"这类判据（092x 那条"品牌缺失只 WARN 不判失败"的口径照旧有效，
// 见 restoreBrandAfterTranslation 的注释）——判多了就是把访客退回看中文，方向反了。
func cannedOutboundReject(uiLang, src, out string, t srcTopics) (reason, detail string, bad bool) {
	if strings.TrimSpace(out) == "" {
		return cannedRejectEmpty, "", true
	}
	// 判残用的是与补翻**同一把尺子**（hanResidueRuns）：补翻成功时这里必然空手，
	// 补翻被拒时这里就是那条被拒的稿子——不另立第二把尺子，否则两边永远会长歪。
	if leaks := hanResidueRuns(uiLang, src, out); len(leaks) > 0 {
		return cannedRejectResidue, strings.Join(leaks, ","), true
	}
	if !t.brand {
		if f := strayBrandForm(out); f != "" {
			return cannedRejectBrandAdded, f, true
		}
	}
	return "", "", false
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
