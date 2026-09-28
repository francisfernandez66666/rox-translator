// ============================================================================
// components/PriceComparePage.tsx — 公开「比价与算价」页 /compare（★ 〇-X #55，2026-09-28）
// 区块：顶栏 → 标题/说明 → 试算器（字数 × 语种数 × 模式 → 积分与费用，对照人工报价区间）→
//       算价公式公示（含浮动说明）→ 我们替代的是哪一整段人工流程 → 人工单价口径与出处 →
//       免责与 CTA → 页脚。
// 数值口径：系数一律取 /api/pricing/meta 返回值（积分口径，接口侧零 token 裸值），
//           前端只做同一条式子的算术，**不写死任何价**（见 usePricingMeta 的文件头）。
// ★ 〇-Y #64（2026-09-28）：算式与人工单价已抽到 @/lib/priceCalc，与首页快速算价卡
//   （PriceQuickCalc）共用同一条式子；本页是「细节全在」的那一侧（公式公示、浮动说明、
//   人工价出处、口径边界、免责），首页只给「填两个数看一个价」的快查入口并回链到这里。
// 视觉规则：与 /pricing 同一套交付稿口径（纯黑底、面板 #0E1014、描边 1.2px 灰阶令牌、
//           主按钮白底黑字），字号不低于 11px——全站排版档等值锁见 styles/readability.test.ts I 段。
// ============================================================================
import { useState } from 'react'
import { Button, Icon, IdeaIcon } from '@/ui/langcross/src'
import { useNavigate } from 'react-router-dom'
import { useT } from '@/i18n'
import { useBranding } from '@/branding'
import { useAuth } from '@/stores/auth'
import { usePricingMeta } from './usePricingMeta'
import { fmtInt, fmtNum } from '../lib/format'

// 数值与算式口径：全部来自 @/lib/priceCalc（★ 〇-Y #64 抽出的一档事实源）。
// 本页与首页快速算价卡（PriceQuickCalc）必须算出同一个数，所以式子、输入上限、
// 人工对照单价与示例预置量都从那里 import，本页**不再留第二遍拷贝**。
// 为什么这几个数放在 lib/priceCalc 而不是词典里：它们要参与算术，页面文案里的 {lo}/{hi}
//   由这里注入，词条与算式不可能各说一套（dom 测试 cmpHumanPriceLock 逐字锁这条）。
import {
  calcEstimatePoints,
  clampNum,
  humanQuoteOf,
  savePctOf,
  HUMAN_MIN_PER_CHAR,
  HUMAN_MAX_PER_CHAR,
  HUMAN_QUOTE_DATE,
  CHARS_MAX,
  LANGS_MAX,
  EXAMPLE_CHARS,
  EXAMPLE_LANGS,
} from '../lib/priceCalc'

// 再导出：本页的历史 dom 测试（PriceComparePage.dom.test ②）是按「页面 import 的那条式子」
// 来锁公示公式的，re-export 让它继续锁的是**同一条**函数而不是第二份实现。
export { calcEstimatePoints }

/** PriceComparePage 公开比价与算价页 */
export default function PriceComparePage() {
  const [, t, tpl] = useT()
  const branding = useBranding()
  const { user } = useAuth()
  const navigate = useNavigate()
  const meta = usePricingMeta()

  const [charsText, setCharsText] = useState(String(EXAMPLE_CHARS))
  const [langsText, setLangsText] = useState(String(EXAMPLE_LANGS))
  const [mode, setMode] = useState<'fast' | 'pro'>('pro')

  const chars = clampNum(charsText, CHARS_MAX)
  const langs = clampNum(langsText, LANGS_MAX)
  const m = meta.modes[mode]
  // 试算三件套：积分、费用、人工同量报价区间（人工按 0.20–0.30 元/字 × 源字符 × 语种数）
  const estPoints = m ? calcEstimatePoints(m, chars, langs) : 0
  const estMoney = estPoints * meta.pointsPrice
  const human = humanQuoteOf(chars, langs)
  const humanLo = human.lo
  const humanHi = human.hi
  // 省比一律对**人工低档**取，高档算出来的"省"更大、更接近吹牛，对外只说保守的那一头
  const savePct = savePctOf(estMoney, humanLo)

  const brand = branding.brandName || t('land.brand')

  return (
    <div className="lc-cmp">
      <style>{COMPARE_CSS}</style>

      <header className="lc-cmp-nav">
        <a className="lc-cmp-brand" href="/">
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={brand} style={{ height: 22 }} />
            : <Icon n="brand" size={18} />}
          <span>{brand}</span>
        </a>
        <nav className="lc-cmp-links">
          {/* 当前页渲染成无链接文本（放 href 等于每次点击整页重载自己，与 /pricing 同口径） */}
          <span>{t('cmp.navEntry')}</span>
          <a href="/pricing">{t('land.pNavPricing')}</a>
          <a href="/docs/terms">{t('land.pNavTerms')}</a>
          <a href="/docs/privacy">{t('land.pNavPrivacy')}</a>
          {/* ★ 〇-Z #75：与 /pricing 同一口径——未登录时主按钮仍写「免费注册」（标签与落点保持一致，
              不改标签就不动 12 语种词典），旁边补一条低权重的「登录」文字链，
              让已经有账号的人不必被要求再注册一次 */}
          {user
            ? <Button size="sm" onClick={() => navigate('/admin')}>{t('land.pAdmin')}</Button>
            : <>
              <a href="/login">{t('land.navLogin')}</a>
              <Button size="sm" onClick={() => navigate('/register')}>{t('land.pRegBtn')}</Button>
            </>}
        </nav>
      </header>

      <main className="lc-cmp-panel">
        <h1 className="lc-cmp-title">{t('cmp.title')}</h1>
        <p className="lc-cmp-intro">{t('cmp.intro')}</p>

        {/* —— 试算器 —— */}
        <h2 className="lc-cmp-sec">{t('cmp.calcTitle')}</h2>
        {!meta.ready ? (
          // 系数没回来就明说取不到：这里绝不用前端写死的缺省值把页面填满，
          // 那会让客户对着一个过期价格做预算（F-12 三口径打架的公开页版本）
          <p className="lc-cmp-warn"><IdeaIcon size={16} />{t('cmp.unavailable')}</p>
        ) : (
          <>
            <div className="lc-cmp-inputs">
              <label className="lc-cmp-field">
                <span>{t('cmp.charsLabel')}</span>
                <input type="number" inputMode="numeric" min={0} max={CHARS_MAX}
                       value={charsText} onChange={(e) => setCharsText(e.target.value)} />
              </label>
              <label className="lc-cmp-field">
                <span>{t('cmp.langsLabel')}</span>
                <input type="number" inputMode="numeric" min={1} max={LANGS_MAX}
                       value={langsText} onChange={(e) => setLangsText(e.target.value)} />
              </label>
              <div className="lc-cmp-field">
                <span>{t('cmp.modeLabel')}</span>
                <div className="lc-cmp-modes">
                  {(['fast', 'pro'] as const).map((k) => (
                    <button key={k} type="button"
                            className={'lc-cmp-mode' + (mode === k ? ' on' : '')}
                            onClick={() => setMode(k)}>
                      {t(k === 'fast' ? 'cmp.modeFast' : 'cmp.modePro')}
                    </button>
                  ))}
                </div>
              </div>
              <button type="button" className="lc-cmp-example"
                      onClick={() => { setCharsText(String(EXAMPLE_CHARS)); setLangsText(String(EXAMPLE_LANGS)) }}>
                {t('cmp.example')}
              </button>
            </div>

            {chars > 0 && langs > 0 && m ? (
              <div className="lc-cmp-grid">
                <article className="lc-cmp-card main">
                  <div className="lc-cmp-cardname">{t('cmp.mineTitle')}</div>
                  {/* ¥ 符号内联（与 /pricing 的 ¥{price_money} 同一写法），数字按界面语种格式化 */}
                  <div className="lc-cmp-big">¥{fmtNum(estMoney, { min: 2, max: 2 })}</div>
                  <div className="lc-cmp-meta">
                    {tpl('cmp.pointsLine', { points: fmtInt(estPoints), unit: t('land.pointsUnit') })}
                  </div>
                  {savePct > 0 && <div className="lc-cmp-save">{tpl('cmp.save', { pct: savePct })}</div>}
                </article>
                <article className="lc-cmp-card">
                  <div className="lc-cmp-cardname">{tpl('cmp.humanLowTitle', { p: fmtNum(HUMAN_MIN_PER_CHAR, { min: 2, max: 2 }) })}</div>
                  <div className="lc-cmp-money">¥{fmtNum(humanLo, { min: 2, max: 2 })}</div>
                  <div className="lc-cmp-meta">{t('cmp.humanLowHint')}</div>
                </article>
                <article className="lc-cmp-card">
                  <div className="lc-cmp-cardname">{tpl('cmp.humanHighTitle', { p: fmtNum(HUMAN_MAX_PER_CHAR, { min: 2, max: 2 }) })}</div>
                  <div className="lc-cmp-money">¥{fmtNum(humanHi, { min: 2, max: 2 })}</div>
                  <div className="lc-cmp-meta">{t('cmp.humanHighHint')}</div>
                </article>
              </div>
            ) : (
              <p className="lc-cmp-hint">{t('cmp.needInput')}</p>
            )}
          </>
        )}

        {/* —— 公式公示 —— */}
        <h2 className="lc-cmp-sec">{t('cmp.formulaTitle')}</h2>
        <p className="lc-cmp-formula">{t('cmp.formula')}</p>
        {meta.ready && m && (
          <ul className="lc-cmp-coef">
            <li>{tpl('cmp.coefFixed', { mode: t(mode === 'fast' ? 'cmp.modeFast' : 'cmp.modePro'), v: fmtNum(m.fixed, { min: 2, max: 2 }) })}</li>
            <li>{tpl('cmp.coefLinear', { mode: t(mode === 'fast' ? 'cmp.modeFast' : 'cmp.modePro'), v: fmtInt(m.per1k) })}</li>
            <li>{tpl('cmp.coefPrice', { p: fmtNum(meta.pointsPrice, { min: 4, max: 4 }) })}</li>
          </ul>
        )}
        <p className="lc-cmp-note"><IdeaIcon size={16} />{t('cmp.formulaNote')}</p>

        {/* —— 浮动说明（用户点名要的口径：小语种 / 专业度 / 抽象程度会浮动）—— */}
        <h2 className="lc-cmp-sec">{t('cmp.varTitle')}</h2>
        <p className="lc-cmp-body">{t('cmp.varBody')}</p>

        {/* —— 替代的是哪一整段流程 + 后编辑口径 —— */}
        <h2 className="lc-cmp-sec">{t('cmp.scopeTitle')}</h2>
        <p className="lc-cmp-body">{t('cmp.scopeBody')}</p>
        <p className="lc-cmp-body">{tpl('cmp.postBody', { brand })}</p>

        {/* —— 人工单价口径与出处（来源 + 采集时间 + 口径边界）—— */}
        <h2 className="lc-cmp-sec">{t('cmp.humanTitle')}</h2>
        <p className="lc-cmp-body">
          {tpl('cmp.humanBody', {
            lo: fmtNum(HUMAN_MIN_PER_CHAR, { min: 2, max: 2 }),
            hi: fmtNum(HUMAN_MAX_PER_CHAR, { min: 2, max: 2 }),
          })}
        </p>
        <ul className="lc-cmp-src">
          <li>{t('cmp.humanSrc1')}</li>
          <li>{t('cmp.humanSrc2')}</li>
          <li>{tpl('cmp.humanSrcDate', { d: HUMAN_QUOTE_DATE })}</li>
        </ul>
        <p className="lc-cmp-body">{t('cmp.humanScope')}</p>
        <p className="lc-cmp-body">{t('cmp.humanFloor')}</p>

        <p className="lc-cmp-disclose">{t('cmp.disclaimer')}</p>
        <div className="lc-cmp-cta">
          <Button onClick={() => navigate('/pricing')}>{t('cmp.ctaPricing')}</Button>
          <Button variant="secondary" onClick={() => navigate(user ? '/admin' : '/register')}>
            {user ? t('land.pAdmin') : t('land.pRegBtn')}
          </Button>
        </div>
      </main>

      <footer className="lc-cmp-foot">
        © 2026 {brand} · <a href="/pricing">{t('land.pNavPricing')}</a> ·
        <a href="/docs/terms"> {t('land.fTerms')}</a> · <a href="/docs/privacy">{t('land.fPrivacy')}</a>
      </footer>
    </div>
  )
}

// —— 样式：与 /pricing 同一套交付令牌（纯黑底 / 面板 #0E1014 / 描边 1.2px 灰阶）——
const COMPARE_CSS = `
.lc-cmp{background:var(--lc-bg);color:var(--lc-text);font-family:var(--lc-font);min-height:100vh;padding-bottom:8px}
.lc-cmp a{color:inherit;text-decoration:none}
.lc-cmp-nav{display:flex;align-items:center;gap:24px;height:56px;padding:0 40px}
.lc-cmp-brand{display:flex;align-items:center;gap:9px;font-size:17px;font-weight:600;white-space:nowrap}
.lc-cmp-links{margin-inline-start:auto;display:flex;align-items:center;gap:22px;font-size:15px;color:var(--lc-text-2)}
.lc-cmp-links a:hover{color:var(--lc-text)}
.lc-cmp-panel{max-width:1018px;margin:12px auto 0;padding:30px 34px 34px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-modal)}
.lc-cmp-title{margin:0 0 10px;font-size:30px;font-weight:700}
.lc-cmp-intro{margin:0 0 6px;font-size:15px;line-height:1.85;color:var(--lc-text-2)}
.lc-cmp-sec{display:flex;align-items:center;gap:9px;margin:28px 0 16px;font-size:18px;font-weight:600}
.lc-cmp-sec::before{content:"";width:2px;height:15px;background:var(--lc-text-1)}
.lc-cmp-body{margin:0 0 12px;font-size:15px;line-height:1.9;color:var(--lc-text-2)}
.lc-cmp-hint,.lc-cmp-warn{display:flex;gap:10px;align-items:center;margin:0 0 12px;padding:14px 16px;font-size:14px;line-height:1.7;color:var(--lc-text-2);background:var(--lc-inset);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-card)}
.lc-cmp-inputs{display:flex;flex-wrap:wrap;gap:16px;align-items:flex-end;margin-bottom:18px}
.lc-cmp-field{display:flex;flex-direction:column;gap:7px;font-size:13px;color:var(--lc-text-2)}
.lc-cmp-field input{width:150px;padding:9px 12px;font-size:15px;color:var(--lc-text);background:var(--lc-surface-2);border:1.2px solid var(--lc-border-6);border-radius:var(--lc-r-bar)}
.lc-cmp-modes{display:flex;gap:8px}
.lc-cmp-mode{padding:9px 14px;font-size:14px;color:var(--lc-text-2);background:transparent;border:1.2px solid var(--lc-border-6);border-radius:var(--lc-r-bar);cursor:pointer}
/* 交付档红线（〇-P）：#FFFFFF 只作实心填充，**不作描边色**，所以下面 .on 只翻面色与字色，
   描边仍走灰阶令牌；全边框 1.2px、最小字号 11px 由 styles/readability.test.ts I 段全站扫描兜住。
   ⚠ 这段说明必须用 CSS 块注释写：CSS 没有斜杠斜杠注释，写成那样会让解析器把整行连同
   下一条规则的选择器一起吞成非法 prelude，.lc-cmp-mode.on 的白底反相直接失效（选中态看不见）。
   ⚠ 另外本串是 JS 模板字面量，注释里不能出现反引号——它会当场闭合模板串（tsc 直接炸）。 */
.lc-cmp-mode.on{color:#000;background:var(--lc-fill-white);font-weight:600}
.lc-cmp-example{padding:9px 14px;font-size:13px;color:var(--lc-text-2);background:transparent;border:1.2px solid var(--lc-border-6);border-radius:var(--lc-r-bar);cursor:pointer}
.lc-cmp-example:hover{color:var(--lc-text)}
.lc-cmp-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(270px,1fr));gap:22px;margin-bottom:6px}
.lc-cmp-card{display:flex;flex-direction:column;gap:7px;padding:18px;background:var(--lc-surface-2);border:1.2px solid var(--lc-border-6);border-radius:var(--lc-r-card)}
.lc-cmp-card.main{border-color:var(--lc-border-1)}
.lc-cmp-cardname{font-size:14px;font-weight:600;color:var(--lc-text-2)}
.lc-cmp-big{font-size:26px;font-weight:700;line-height:1.15;font-family:var(--lc-font-latin)}
.lc-cmp-money{font-size:22px;font-weight:700;line-height:1.15;font-family:var(--lc-font-latin)}
.lc-cmp-meta{font-size:13px;color:var(--lc-text-3);line-height:1.7}
.lc-cmp-save{font-size:14px;font-weight:600;color:var(--lc-text)}
.lc-cmp-formula{margin:0 0 12px;padding:16px 18px;font-size:15px;line-height:1.9;color:var(--lc-text);background:var(--lc-inset);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-card);font-family:var(--lc-font-latin)}
.lc-cmp-coef{margin:0 0 12px;padding-inline-start:20px;font-size:14px;line-height:2;color:var(--lc-text-2)}
.lc-cmp-note{display:flex;gap:10px;margin:0 0 12px;padding:16px 18px;font-size:14.5px;line-height:1.85;color:var(--lc-text-2);background:var(--lc-inset);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-card)}
.lc-cmp-src{margin:0 0 12px;padding-inline-start:20px;font-size:13.5px;line-height:1.9;color:var(--lc-text-3)}
.lc-cmp-disclose{margin:22px 0 0;padding:14px 16px;font-size:13px;line-height:1.8;color:var(--lc-text-3);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-card)}
.lc-cmp-cta{display:flex;gap:12px;margin-top:18px}
.lc-cmp-foot{padding:20px 24px 26px;text-align:center;font-size:14px;color:var(--lc-text-3)}
.lc-cmp-foot a:hover{color:var(--lc-text-2)}
@media (max-width:900px){
  .lc-cmp-nav{padding:0 20px;gap:16px}
  .lc-cmp-links{gap:16px}
  .lc-cmp-panel{margin:12px 16px 0;padding:24px 20px 26px}
}
@media (max-width:620px){
  .lc-cmp-nav{height:auto;flex-wrap:wrap;padding:12px 16px}
  .lc-cmp-links{width:100%;justify-content:flex-start;flex-wrap:wrap;gap:12px 16px}
  .lc-cmp-title{font-size:24px}
  .lc-cmp-field input{width:120px}
}
`
