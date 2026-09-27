// ============ panels/cost.ts · 职责说明 ============
// 官网公开「比价与算价」页（未登录 `/compare`）i18n 键，★ 〇-X #55（2026-09-28）。
// 建页背景（用户四条口径逐条落地）：
//   ① 说清我们替代的是「初译 + 内部资料」这一整段人工流程（初译/校对/审核/发布），
//      以及需要法律公证、出版发行、母语润色、专业设计时走「后编辑」的工时与费用口径；
//   ② 人工对照价必须写出折算口径与**公开来源 + 采集时间**，不许把估算写成报价承诺；
//   ③ 算价公式要放出来（用户原话「公式放出来」），并明确说明会因小语种、
//      文案专业度/抽象程度而浮动；
//   ④ 积分单价按 1:400 汇率与 24,917 分/百万 token 的尺子现算（F-78），
//      本页所有系数一律来自 /api/pricing/meta，词典里**不写任何数值**——
//      带 {points}/{unit}/{pct}/{mode}/{v}/{p}/{lo}/{hi}/{d} 占位的词条由组件注入真实取值，
//      所以「页面显示的价」与「后端实际扣的价」不可能各说一套
//      （这条由 PriceComparePage 的 dom 测试与 pricing_meta_test.go 的等式锁共同钉住）。
// 铁律（AGENTS §一·5 公开接口零 token 裸值的前端侧延伸）：本页文案只出现「积分 / 元」，
//   禁止出现 token、K/F 系数、汇率、尺子这些内部量；要解释成本结构就说积分档。
// 新语种同步：本面板 39 键必须逐键进十份 locales/*.ts（locales.core.test.ts 以
//   ALL_KEYS 全量长度为基准红灯拦截），补译脚本见 scripts/i18n/insert_cmp_keys_20260928.py。
// =============================================
export const zh: Record<string, string> = {
  // —— 顶栏与标题 ——
  'cmp.navEntry': '比价与算价',
  'cmp.title': '翻译要花多少钱，这里当场算给你',
  'cmp.intro': '填字数、选语种与模式，页面按我们真实在用的算价公式当场出结果，并和同一批内容的人工笔译报价放在一起对照。公式、系数、对照价的来源全部公开，不做「起价很低、结算另算」那套。',
  // —— 试算器 ——
  'cmp.calcTitle': '自助试算',
  'cmp.unavailable': '算价系数暂时取不到，本页不提供估算结果。请稍后刷新，或先查看价格方案。',
  'cmp.charsLabel': '源文字符数',
  'cmp.langsLabel': '目标语种数',
  'cmp.modeLabel': '翻译模式',
  'cmp.modeFast': '快速档',
  'cmp.modePro': '专业档',
  'cmp.example': '载入示例：8 万字手册 × 1 语种',
  'cmp.mineTitle': 'LangCross 预估费用',
  'cmp.pointsLine': '预估消耗 {points} {unit}',
  'cmp.save': '比同量人工报价的低档再省约 {pct}%',
  'cmp.humanLowTitle': '人工笔译 · 入门档（{p} 元/源字符）',
  'cmp.humanLowHint': '按公开报价下限折算，同一批字数与语种数',
  'cmp.humanHighTitle': '人工笔译 · 常规档（{p} 元/源字符）',
  'cmp.humanHighHint': '按公开报价上限折算，专业领域与加急还会更高',
  'cmp.needInput': '请先填写有效的源文字符数与目标语种数，再查看结果。',
  // —— 公式公示 ——
  'cmp.formulaTitle': '我们怎么算这笔价',
  'cmp.formula': '预估积分 =（源文字符数 ÷ 1000 × 目标语种数 × 每千字积分档）+ 每次建单固定积分；预估费用 = 预估积分 × 每积分单价。',
  'cmp.coefFixed': '{mode}每次建单固定积分：{v}',
  'cmp.coefLinear': '{mode}每 1000 源字符 × 1 个目标语种：{v}',
  'cmp.coefPrice': '每积分单价：{p} 元',
  'cmp.formulaNote': '这里的系数与下单时的预检用的是同一条式子，你在页面上算出来的积分就是账单上的积分；上方数字随运营调档实时更新，以本页当前显示值为准。',
  // —— 浮动说明（用户点名口径）——
  'cmp.varTitle': '哪些情况会让实际费用上下浮动',
  'cmp.varBody': '试算给的是同一条公式在标准文本上的结果，实际消耗会随内容特性上下浮动：一是语种，小语种与形态复杂的语言（词形变化多、书写方向不同、可用术语资料少）单位成本更高；二是文本本身，专业度越高、越抽象、上下文越少，模型需要反复核对的轮次越多，消耗随之上升，而口语化的重复段落会明显更省；三是结构，含大量表格、编号、术语表的文件比纯正文更贵。所以上面的数字应当作量级参考，而不是逐单承诺——建单时预检会按同一公式给出这一单的上限，费用不会超过它。',
  // —— 替代范围与后编辑口径（用户批准文案口径）——
  'cmp.scopeTitle': '我们替代的是哪一段人工流程',
  'cmp.scopeBody': '一份内部资料走人工，通常要经过初译、校对、审核、定稿发布四道工序，报价里含的是这一整段的工时。LangCross 承接的是这整段：术语匹配、初译、质量评审、格式回写与交付都在系统内完成，交付的就是可以往下发的那一版。',
  'cmp.postBody': '如果你要的是法律公证、出版发行、母语润色或专业排版设计，这类资质与工艺仍然要交给对应业务的专员——但那时对方拿到的是已经成稿的译文，做的是后编辑而不是从零翻译。按这一口径，工时比从零开始省一半以上，费用省 80% 以上。',
  // —— 人工单价的出处与边界（用户要求「标注来源与时间」）——
  'cmp.humanTitle': '人工对照价怎么来的',
  'cmp.humanBody': '本页人工一侧的费用按行业公开人工单价 {lo}–{hi} 元/源字折算，字数与语种数取你当前填入的同一个值。',
  'cmp.humanSrc1': '厦门市翻译协会《笔译服务指导价格》：通用级 230–300 元/千字',
  'cmp.humanSrc2': '百度人工翻译公开价目页：快译 0.26 元/字，专业应用级 240–300 元/千字',
  'cmp.humanSrcDate': '以上公开报价的采集日期：{d}',
  'cmp.humanScope': '请注意这一档的口径边界：它是行业公开报价里通用级到专业级入门的区间，不是全行业均价，更不是人工报价的上限；法律公证、出版发行、母语润色与专业排版都不含在这一档内，那些服务按各自资质与工艺另行计价。',
  'cmp.humanFloor': '另外一个对我们不利但要说清的差别：不少人工译员按 300 字起计价，短内容的人工单价看起来会比这一档更高，本页不采用那种算法，只用同一把尺量同一批字数。',
  // —— 免责与 CTA ——
  'cmp.disclaimer': '本页为公开算价口径与费用量级对照，不构成报价承诺；正式费用以工单建单时的预检值与实际结算为准，套餐、活动与试用赠分按《价格方案》与《服务条款》计。',
  'cmp.ctaPricing': '查看价格方案',
}

// en 段：与上面 zh 段**逐键一一对应**（键集合与占位符集合由 parity.test.ts 的 PANELS 表对等性守护
// 机器校验，十份 locale 的占位符一致性由 scripts/i18n/insert_cmp_keys_20260928.py 在插入前挡住）。
// 措辞红线同 zh：不写任何系数数值，凡涉及数字处一律走占位符由组件注入。
export const en: Record<string, string> = {
  // —— Top nav and headline ——
  'cmp.navEntry': 'Pricing & estimate',
  'cmp.title': 'See what a translation actually costs, right here',
  'cmp.intro': 'Enter a word count, pick the target languages and the mode, and the page applies the same formula we bill with — next to the human translation quote for the very same volume. The formula, the coefficients and the source of the comparison price are all public; no “starting at” pricing that changes at checkout.',
  // —— Calculator ——
  'cmp.calcTitle': 'Self-service estimate',
  'cmp.unavailable': 'Pricing coefficients are temporarily unavailable, so this page shows no estimate. Please refresh later, or see the plans.',
  'cmp.charsLabel': 'Source characters',
  'cmp.langsLabel': 'Target languages',
  'cmp.modeLabel': 'Translation mode',
  'cmp.modeFast': 'Fast',
  'cmp.modePro': 'Professional',
  'cmp.example': 'Load example: 80k-character manual × 1 language',
  'cmp.mineTitle': 'Estimated LangCross cost',
  'cmp.pointsLine': 'Estimated usage: {points} {unit}',
  'cmp.save': 'About {pct}% below the lower bound of the human quote',
  'cmp.humanLowTitle': 'Human translation · entry ({p} CNY per source character)',
  'cmp.humanLowHint': 'Public rate floor applied to the same character and language counts',
  'cmp.humanHighTitle': 'Human translation · standard ({p} CNY per source character)',
  'cmp.humanHighHint': 'Public rate ceiling; specialist fields and rush work go higher',
  'cmp.needInput': 'Enter a valid source character count and target language count to see the result.',
  // —— Published formula ——
  'cmp.formulaTitle': 'How we price it',
  'cmp.formula': 'Estimated points = (source characters ÷ 1000 × target languages × points per 1k characters) + fixed points per order; estimated cost = estimated points × price per point.',
  'cmp.coefFixed': '{mode} fixed points per order: {v}',
  'cmp.coefLinear': '{mode} per 1,000 source characters × 1 target language: {v}',
  'cmp.coefPrice': 'Price per point: CNY {p}',
  'cmp.formulaNote': 'These coefficients are the same expression the pre-check applies when you place an order, so the points you read here are the points on the invoice. The numbers above follow operational adjustments in real time — treat the values currently shown on this page as authoritative.',
  // —— Variance (explicitly requested by the user) ——
  'cmp.varTitle': 'What makes the real cost move up or down',
  'cmp.varBody': 'The estimate applies one formula to standard text; actual usage varies with the content. First, language: less common languages and morphologically complex ones (heavy inflection, different script direction, thinner reference material) cost more per unit. Second, the text itself: the more specialised, the more abstract, or the less context available, the more verification passes are needed, while repetitive conversational passages come out markedly cheaper. Third, structure: files dense with tables, numbering and terminology cost more than plain prose. Treat the figures above as an order of magnitude rather than a per-order commitment — at checkout the pre-check states the ceiling for this order under the same formula, and the charge will not exceed it.',
  // —— Scope replaced and post-editing (user-approved wording) ——
  'cmp.scopeTitle': 'Which part of the human workflow we replace',
  'cmp.scopeBody': 'A human-processed internal document normally passes four stages — first translation, revision, review, and release — and a human quote covers the labour for all of them. LangCross takes the whole span: terminology matching, first translation, quality review, format write-back and delivery all happen inside the system, and what we hand over is the version you can send on.',
  'cmp.postBody': 'If you need notarisation, publication, native-speaker polishing or professional typesetting, that craft still belongs to the specialists in each of those fields — but they would start from a finished translation and post-edit rather than translate from scratch. On that basis the labour is more than half lower than starting from zero, and the cost 80% or more lower.',
  // —— Source and boundary of the human benchmark (user asked for source + date) ——
  'cmp.humanTitle': 'Where the human comparison price comes from',
  'cmp.humanBody': 'The human side of this page is converted at a publicly quoted human rate of {lo}–{hi} CNY per source character, applied to the same character and language counts you entered.',
  'cmp.humanSrc1': 'Xiamen Translators Association, guidance prices for translation services: general level CNY 230–300 per 1,000 characters',
  'cmp.humanSrc2': 'Baidu human translation public price list: fast tier CNY 0.26 per character, professional level CNY 240–300 per 1,000 characters',
  'cmp.humanSrcDate': 'Public quotes collected on: {d}',
  'cmp.humanScope': 'Note the boundary of this band: it is the entry-to-standard range of publicly quoted rates, not an industry average and not a ceiling for human work. Notarisation, publication, native polishing and professional typesetting are outside it and are priced separately by qualification and craft.',
  'cmp.humanFloor': 'One difference that is unflattering to us but should be said: many human translators bill a 300-character minimum, so short jobs look pricier per character than this band suggests. We do not use that method here — one ruler, the same volume, both sides.',
  // —— Disclaimer and CTA ——
  'cmp.disclaimer': 'This page states our public pricing formula and the order of magnitude of costs; it is not a quote commitment. Final charges follow the pre-check value at order creation and the actual settlement, with plans, campaigns and trial credits governed by the Plans page and the Terms of Service.',
  'cmp.ctaPricing': 'See the plans',
}
