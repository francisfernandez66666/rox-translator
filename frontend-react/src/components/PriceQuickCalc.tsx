// ============================================================================
// components/PriceQuickCalc.tsx — 官网首页「快速算价」卡（★ 2026-09-28 〇-Y #64）
// 用户需求原话：「另外比价和算价计算器也放首页一下，供用户快速计算，
//                 但是细节要点击进入现在的比价和算价链接」
// 所以这张卡的分工是：
//   首页这侧＝快查（填字数 + 语种数 → 立刻看到我们多少钱、人工大约多少钱、省多少）；
//   /compare 那侧＝细节（公示公式与三条系数、费用为什么上下浮动、我们替代的是哪一整段
//                    人工流程、人工对照价的公开来源与采集日期、口径边界与免责声明）。
//   细节**不复制**到首页：卡片只留一个入口（.lc-qc-more），点进去才是全量页。
//
// 数值口径（与 /compare 完全同源，三条都是硬约束）：
//   ① 系数一律来自 GET /api/pricing/meta，本文件不写任何积分数或金额
//      （见 usePricingMeta 的「宁缺勿错」：接口没回来就明说取不到，绝不拿兜底值报价）；
//   ② 算式来自 @/lib/priceCalc 的 calcEstimatePoints——与 /compare 页、与后端建单预检
//      是同一条式子，不会出现「首页说的价和详情页说的价不一样」这种 F-12 形态；
//   ③ 人工对照单价（0.20 元/源字符）也取同一份常量，首页只报**低档**那一头
//      （省比只对低档取：高档算出来的「省」更大、更接近吹牛）。
//
// 与首页 #39 口径的关系（「套餐卡面只写档位口径、不抄具体金额」）：
//   那条约束管的是**套餐价目**（价目事实源唯一在 /api/plans，由 /pricing 渲染）。
//   本卡不报套餐价，只按访客自己填入的量做一次性试算，数来自 /api/pricing/meta，
//   所以不违反 #39；Landing.dom.test ⑨ 的扫描范围也刻意只覆盖 .lc-plan-price，
//   本卡用 .lc-qc-* 独立类名，两条口径互不干扰（各自的锁各自在）。
//
// 视觉：沿用首页交付档（面 #0E1014 系、描边 1.2px 灰阶令牌、最小字号 11px、
//       #FFFFFF 只作实心填充不作描边），全站等值锁见 styles/readability.test.ts I 段。
// ============================================================================
import { useState } from 'react'
import { ArrowRightIcon, IdeaIcon } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { usePricingMeta } from './usePricingMeta'
import { fmtInt, fmtNum } from '../lib/format'
import {
  calcEstimatePoints,
  clampNum,
  humanQuoteOf,
  savePctOf,
  CHARS_MAX,
  LANGS_MAX,
  EXAMPLE_CHARS,
  EXAMPLE_LANGS,
} from '../lib/priceCalc'

/** PriceQuickCalc 首页快速算价卡：两个输入、一行结果、一个详情入口 */
export default function PriceQuickCalc() {
  const [, t, tpl] = useT()
  const meta = usePricingMeta()

  // 初值与 /compare 页同一组预置量（8 万字 × 1 语种）：访客在两处看到的「没改输入时的价」必须一致
  const [charsText, setCharsText] = useState(String(EXAMPLE_CHARS))
  const [langsText, setLangsText] = useState(String(EXAMPLE_LANGS))
  // 默认专业档：与 /compare 同档，也是官网主推的那一档（首页不放档位切换钮，
  // 切档属于「细节」，按用户口径留给 /compare）
  const m = meta.modes.pro

  const chars = clampNum(charsText, CHARS_MAX)
  const langs = clampNum(langsText, LANGS_MAX)
  const estPoints = m ? calcEstimatePoints(m, chars, langs) : 0
  const estMoney = estPoints * meta.pointsPrice
  const human = humanQuoteOf(chars, langs)
  const savePct = savePctOf(estMoney, human.lo)

  return (
    <div className="lc-qc lc-reveal" data-qc="card">
      <div className="lc-qc-head">
        <h3 className="lc-qc-t">{t('land.qc.title')}</h3>
        <p className="lc-qc-sub">{t('land.qc.sub')}</p>
      </div>

      {!meta.ready ? (
        // 不可用态：明说取不到，**不渲染任何金额**（连 ¥ 符号都不出现）。
        // 这里最容易写错的做法是留一份「看起来像」的缺省系数把卡片填满：
        // 超管调档后首页会一直报旧价，而且永远不会自己红（F-12 的公开页形态）。
        <p className="lc-qc-warn" data-qc="unavailable">
          <IdeaIcon size={16} />
          {t('cmp.unavailable')}
        </p>
      ) : (
        <>
          <div className="lc-qc-inputs">
            <label className="lc-qc-field">
              <span>{t('cmp.charsLabel')}</span>
              <input type="number" inputMode="numeric" min={0} max={CHARS_MAX}
                     value={charsText} onChange={(e) => setCharsText(e.target.value)} />
            </label>
            <label className="lc-qc-field">
              <span>{t('cmp.langsLabel')}</span>
              <input type="number" inputMode="numeric" min={1} max={LANGS_MAX}
                     value={langsText} onChange={(e) => setLangsText(e.target.value)} />
            </label>
            {/* 数字是现算的，公式与系数不抄在这里：想看式子点下方详情入口 */}
            {chars > 0 && langs > 0 && m ? (
              <div className="lc-qc-out" data-qc="out">
                <div className="lc-qc-ours">
                  <span className="lc-qc-tag">{t('land.qc.ours')}</span>
                  <div className="lc-qc-money" data-qc="money">¥{fmtNum(estMoney, { min: 2, max: 2 })}</div>
                  <div className="lc-qc-points" data-qc="points">
                    {tpl('land.qc.points', { points: fmtInt(estPoints), unit: t('land.pointsUnit') })}
                  </div>
                </div>
                <div className="lc-qc-human">
                  <span className="lc-qc-tag">{tpl('land.qc.human', { p: fmtNum(human.minPerChar, { min: 2, max: 2 }) })}</span>
                  <div className="lc-qc-money dim" data-qc="human">¥{fmtNum(human.lo, { min: 2, max: 2 })}</div>
                  {savePct > 0 && <div className="lc-qc-save" data-qc="save">{tpl('land.qc.save', { pct: savePct })}</div>}
                </div>
              </div>
            ) : (
              // 空输入/非法输入：不给半截报价（与 /compare 页同口径）
              <p className="lc-qc-hint" data-qc="need">{t('cmp.needInput')}</p>
            )}
          </div>
        </>
      )}

      {/* 详情入口：公式、系数、浮动说明、人工价出处、口径边界、免责声明全在那一页 */}
      <a className="lc-qc-more" href="/compare" data-qc="more">
        {t('land.qc.detail')}
        <ArrowRightIcon size={16} />
      </a>
    </div>
  )
}
