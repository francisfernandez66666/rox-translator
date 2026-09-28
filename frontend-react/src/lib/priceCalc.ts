// ============================================================================
// lib/priceCalc.ts — 公开算价口径的**唯一一份**前端算式（★ 2026-09-28 〇-Y #64）
// ----------------------------------------------------------------------------
// 为什么要把式子从 PriceComparePage.tsx 抽出来：用户要求官网首页也放一个快速算价卡
// （「比价和算价计算器放首页一下，供用户快速计算，但细节要点击进入现在的比价和算价链接」），
// 于是首页卡与 /compare 页成了**两个消费方**。两处各写一遍式子就是 F-12
// 「定价页 / 收银台 / 管理台三口径打架」的前端版：改档时漏改一处，客户在首页看到的钱
// 与点进详情页看到的钱不一样，而这类分歧两边各测各的都不会红。
// 所以本文件只放「一条式子 + 三个边界常量」，两侧一律从这里 import：
//   · /compare 全量页：src/components/PriceComparePage.tsx
//   · 首页快速卡：src/components/PriceQuickCalc.tsx
// 等值性由两处 dom 测试用**同一组输入 → 同一个期望值**交叉锁住
// （PriceComparePage.dom.test ② 与 PriceQuickCalc.dom.test ①）。
//
// 数值红线（AGENTS §一·5「公开接口零 token 裸值」的前端延伸）：
//   本文件**不写任何积分系数或金额**——per1k / fixed / 每积分单价一律来自
//   GET /api/pricing/meta（见 components/usePricingMeta.ts 的「宁缺勿错」口径）。
//   这里唯一写死的两个数是**人工对照单价** 0.20 / 0.30 元/源字符，它们不属于我们的价，
//   而是行业公开报价：出处、采集日期与口径边界见
//   《发布前E2E_UAT_20260926/证据/人工单价公开来源取证_20260927.md》
//   （厦门市翻译协会《笔译服务指导价格》通用级 230–300 元/千字、百度人工翻译公开价目页
//    快译 0.26 元/字 / 专业应用级 240–300 元/千字，采集日期 2026-09-27）。
//   ⚠️ 改这三个常量必须同批改 /compare 页的出处文案与 dom 锁（⑧ 人工对照价口径锁），
//      否则「页面显示的区间」与「页面声称的来源」会各说一套。
// ============================================================================

/** PriceCoeff 单档算价系数（积分口径）：与 PricingModeMeta 同形，这里独立命名以免 lib 反向依赖组件 */
export interface PriceCoeff {
  /** 每「1000 源字符 × 1 个目标语种」的线性积分档 */
  per1k: number
  /** 每次建单的一次性固定积分档（可能是小数，如 pro=7.5） */
  fixed: number
}

// HUMAN_MIN_PER_CHAR / HUMAN_MAX_PER_CHAR 人工笔译单价区间（元/源字，中译英主线）。
// 口径边界（对外必须这么写）：这是「行业公开报价的通用级到专业级入门区间」，
// **不是**行业均价、也不是上限；不含法律公证 / 出版发行 / 母语润色 / 专业排版。
const HUMAN_MIN_PER_CHAR = 0.20
const HUMAN_MAX_PER_CHAR = 0.30
// HUMAN_QUOTE_DATE 人工报价的采集日期（对外标注「来源与时间」用，改数据必须同批改这里）
const HUMAN_QUOTE_DATE = '2026-09-27'

// CHARS_MAX 输入上限（防呆）：一千万源字符已远超单笔业务量级，
// 再大只会把「理论上的一次消耗」显示成看不清的天文数字。
const CHARS_MAX = 10_000_000
// LANGS_MAX 语种数上限：界面语种是 12 个口径，一次建单的目标语种数不会超过它
const LANGS_MAX = 12

// EXAMPLE_CHARS / EXAMPLE_LANGS 「载入示例」与首屏默认预置量：8 万字手册 × 单语种
//（一线客户最常见的整册技术手册场景，也是与人工报价差距最能拉开的一档；
//  首页卡与 /compare 页共用同一组初值，两侧「没改输入时看到的价」必须一模一样）
const EXAMPLE_CHARS = 80000
const EXAMPLE_LANGS = 1

/**
 * calcEstimatePoints 把一个档的系数算成预估积分。
 * 与后端建单预检同一条两段式（F-72：固定项 + 线性项），两侧等式分别由
 * pricing_meta_test.go（后端）与两侧 dom 测试（前端）钉住；四舍五入取整方向也一致。
 * 非法输入（0 / 负数 / NaN / Infinity）一律回 0，**不进算式**——
 * 半截字数乘出来的「预估」比不预估更危险，客户会拿它当预算依据。
 */
export function calcEstimatePoints(m: PriceCoeff, chars: number, langs: number): number {
  if (!Number.isFinite(chars) || !Number.isFinite(langs) || chars <= 0 || langs <= 0) return 0
  return Math.round(m.fixed + (chars / 1000) * m.per1k * langs)
}

/** clampNum 输入框取值归一：非法/空一律 0，上限钉死（负数与 NaN 都不许进算式） */
export function clampNum(raw: string, max: number): number {
  const v = Math.floor(Number(raw))
  if (!Number.isFinite(v) || v <= 0) return 0
  return Math.min(v, max)
}

/** HumanQuote 同一批字数与语种数下的人工笔译报价区间（元） */
export interface HumanQuote {
  lo: number
  hi: number
  /** 单位价（元/源字符），界面文案要把「按多少钱一个字算的」写出来 */
  minPerChar: number
  maxPerChar: number
  /** 采集日期（对外标注「来源与时间」） */
  quoteDate: string
}

/** humanQuoteOf 按公开单价折算人工对照区间（首页卡只显示低档，全区间留给 /compare） */
export function humanQuoteOf(chars: number, langs: number): HumanQuote {
  const n = chars * langs
  return {
    lo: n * HUMAN_MIN_PER_CHAR,
    hi: n * HUMAN_MAX_PER_CHAR,
    minPerChar: HUMAN_MIN_PER_CHAR,
    maxPerChar: HUMAN_MAX_PER_CHAR,
    quoteDate: HUMAN_QUOTE_DATE,
  }
}

/**
 * savePctOf 相对人工**低档**省下的百分比（取整，负值一律归 0）。
 * 为什么只对低档取：高档算出来的「省」更大、更接近吹牛，对外只说保守的那一头
 * （用户口径「省比只对低档取」，见项目记忆「官网对外定价口径」）。
 */
export function savePctOf(estMoney: number, humanLo: number): number {
  if (!(humanLo > 0) || !(estMoney > 0)) return 0
  return Math.max(0, Math.round((1 - estMoney / humanLo) * 100))
}

export {
  HUMAN_MIN_PER_CHAR,
  HUMAN_MAX_PER_CHAR,
  HUMAN_QUOTE_DATE,
  CHARS_MAX,
  LANGS_MAX,
  EXAMPLE_CHARS,
  EXAMPLE_LANGS,
}
