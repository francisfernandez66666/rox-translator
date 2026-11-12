// ============ avoid_phrases.go · 职责说明 ============
// 三期 W4：销售声称守卫命中时的「回避 + 转其他话题」12 语种常量表（用户裁定 2，§0.4 第 7 条）。
//
//   - 命中「触及受控键 / 无事实头 / 验签失败 / 事实集为空 / 声称锚点对不上」时，回复**不是打回、
//     不是放行**，而是改成对应语种的回避句 + 转其他话题，并把 Reply.PendingHuman 置真。
//   - 每条都含两段语义：①「我确认一下再给您回复」类回避；②「换个话题」的安全转场。
//   - ★ 全表**不含任何事实值**（无数值／单位／型号／认证编号／品牌名）：A13 断言逐语种扫描，
//     把回避句换成含事实值的句子必红（M0232）。事实值只在已准入译文里，绝不借回避句泄露。
//   - ★ 用户裁定：回避句本期放 Go 侧常量表（不是前端 i18n 的 assist.evasion.confirm_later），
//     前端本期零改动；按 uiLang 选，缺省／未收录语种回落中文并返回 fallback 供调用方 WARN 留痕。
//
// 12 语种内部码与 reply_lang.go:langLabels、store.FactI18nLangs 同一集合，不许另写一份清单。
// ================================================
package engine

// avoidPhraseKey 回避句在 i18n 词表里的逻辑键（仅作口径登记，本期文案落在本 Go 表）。
const avoidPhraseKey = "assist.evasion.confirm_later"

// avoidPhrases 12 语种「回避 + 转其他话题」文案（Go 侧单一真值）。
// ⚠️ 任何一条都不许塞进数值／单位／品牌名等事实值：守卫用它替换的是「可能有假声称」的正文，
//
//	回避句里出现一个数字就等于把没准入的值又送回了访客。
var avoidPhrases = map[string]string{
	"zh":      "这个我确认一下再给您回复。咱们先换个话题，您还想了解别的方面吗？",
	"zh_hant": "這個我確認一下再回覆您。咱們先換個話題，您還想了解別的方面嗎？",
	"en":      "Let me double-check that and get back to you. Meanwhile, feel free to ask me about anything else.",
	"ru":      "Давайте я уточню и вернусь к вам с ответом. А пока могу помочь с другим вопросом?",
	"fr":      "Je vérifie ce point et je reviens vers vous. En attendant, puis-je vous aider sur un autre sujet ?",
	"ar":      "دعني أتحقق من ذلك ثم أعود إليك بالرد. في الوقت نفسه، هل أستطيع مساعدتك في موضوع آخر؟",
	"es":      "Déjame confirmarlo y te respondo enseguida. Mientras tanto, ¿hay algo más en lo que pueda ayudarte?",
	"pt":      "Deixe-me confirmar e depois lhe respondo. Enquanto isso, posso ajudar com outro assunto?",
	"de":      "Das prüfe ich kurz und melde mich dann bei Ihnen. In der Zwischenzeit kann ich Ihnen bei etwas anderem helfen?",
	"ja":      "確認してから改めてご返信します。それまでに、他にご相談したいことはありますか？",
	"ko":      "확인한 뒤 다시 답변드리겠습니다. 그동안 다른 궁금한 점이 있으신가요?",
	"th":      "ขอตรวจสอบก่อนแล้วจะกลับมาตอบคุณนะคะ ระหว่างนี้มีเรื่องอื่นให้ช่วยไหมคะ?",
}

// AvoidPhraseLangs 返回本表收录的 12 语种码（顺序稳定，供 A13 逐语种扫描用）。
// 参数：无；返回语种码切片。
func AvoidPhraseLangs() []string {
	return []string{"zh", "zh_hant", "en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th"}
}

// AvoidPhrase 按本轮作答语言取回避句；未收录／空语种回落中文，fallback=true 供调用方 WARN 留痕。
// 参数：uiLang=本轮作答语言码；返回回避句、是否回落中文。
func AvoidPhrase(uiLang string) (string, bool) {
	c := canonicalLang(uiLang)
	if v, ok := avoidPhrases[c]; ok && v != "" {
		return v, false
	}
	return avoidPhrases["zh"], true
}
