// ============================================================================
// components/PricingPage.tsx — 公开定价页 /pricing（ 2026-09-18 按设计图 05 重建）
// 区块：顶栏 → 「定价 Pricing」标题 + 说明 → 商业套餐 Plans（三列价格卡）→
// 计费口径提示条 → 常见问题 FAQ → 页脚。
// 价格 / 积分 / 有效期一律渲染 /api/plans 返回值（含免费体验包与增量包）。
// 视觉规则：纯黑底、面板 #0E1014、描边 1.2px #464C58、主按钮白底黑字、无蓝无绿。
// ============================================================================
import { Button, IdeaIcon } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { useBranding } from '@/branding'
import { useAuth } from '@/stores/auth'
import { usePlans } from './usePlans'
import type { PlanLite } from './usePlans'

// FAQ：固定三条（设计图 05）
const FAQ_INDEXES = [1, 2, 3] as const

// go 沿用现有页面跳转方式（整页跳转，不做 SPA 路由切换）
const go = (to: string) => { window.location.href = to }

/** BrandMark 品牌圆点图标（租户未配置 brand_logo 时的默认标记） */
function BrandMark() {
  return (
    <svg width="18"height="18"viewBox="0 0 18 18"aria-hidden="true"focusable="false">
      <circle cx="9"cy="9"r="7.3"fill="none"stroke="#E7E9EA"strokeWidth="1.4"/>
      <circle cx="9"cy="9"r="2.4"fill="#E7E9EA"/>
    </svg>
  )
}

/** PricingPage 公开定价页：全量价目表（体验包 / 增量包 / 付费包）+ 计费说明 + FAQ */
export default function PricingPage() {
  const [, t, tpl] = useT()
  const branding = useBranding()
  const { user } = useAuth()
  // plans 是全量上架包；trial 只用来兜「有效期」那一行文案（包没配 duration_days 时回落体验天数）
  const { plans, trial } = usePlans()
  const brand = branding.brandName || t('land.brand')

  // 套餐类型 → 卡面文案（后端 ptype：free / increment / paid）
  const typeLabel = (ptype: string) =>
    ptype ==='free'? t('land.pTypeFree') : ptype ==='increment'? t('land.pTypeIncrement') : t('land.pTypePaid')
  // 卡底动作：免费包=注册即送 / 增量包=即买即用即到账 / 付费包=新客首月 5 折
  const cardAction = (ptype: string) =>
    ptype ==='free'? t('land.pFreeBtn') : ptype ==='increment'? t('land.pTopupBtn') : t('land.pPlanBtn')
  // 有效期口径：增量包永久有效，其余按后端 duration_days（缺失回落体验天数）
  const periodText = (p: PlanLite) =>
    p.ptype === 'increment'
      ? `· ${t('land.pForever')}`
      : tpl('land.pPerDay', { days: p.duration_days > 0 ? p.duration_days : trial.days })

  return (
    <div className="lc-prc">
      <style>{PRICING_CSS}</style>

      {/* 顶栏：左 Logo，右 定价 / 用户协议 / SLA / 隐私协议 + 白底主按钮 */}
      <header className="lc-prc-nav">
        <a className="lc-prc-brand"href="/">
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={brand} style={{ height: 22 }} />
            : <BrandMark />}
          <span>{brand}</span>
        </a>
        <nav className="lc-prc-links">
          <a href="/pricing">{t('land.pNavPricing')}</a>
          <a href="/docs/terms">{t('land.pNavTerms')}</a>
          <a href="/docs/sla">{t('land.pNavSla')}</a>
          <a href="/docs/privacy">{t('land.pNavPrivacy')}</a>
          {/* 同一位置只放一枚按钮：已登录给「进入后台」，未登录给「免费注册」，不摆两个入口抢视线 */}
          {user
            ? <Button size="sm"onClick={() => go('/admin')}>{t('land.pAdmin')}</Button>
            : <Button size="sm"onClick={() => go('/register')}>{t('land.pRegBtn')}</Button>}
        </nav>
      </header>

      <main className="lc-prc-panel">
        <h1 className="lc-prc-title">{t('land.pTitle')}</h1>
        <p className="lc-prc-intro">{t('land.pIntro')}</p>

        {/* 商业套餐：后端返回的全部上架包，按 free → increment → paid 排列 */}
        <h2 className="lc-prc-sec">{t('land.pSectionPlans')}</h2>
        <div className="lc-prc-grid">
          {plans.map((p) => (
            <article key={p.code} className="lc-prc-card">
              <div className="lc-prc-name">{p.name}</div>
              {/* 对外口径统一积分（points 是积分面值，token 裸值不外露），千分位只为可读 */}
              <div className="lc-prc-meta">{p.points.toLocaleString()} {t('land.pointsUnit')} · {typeLabel(p.ptype)}</div>
              {/* 金额原样渲染后端 price_money（单位=元）：折扣、涨价一律在后台改包，前端不做二次换算 */}
              <div className="lc-prc-price">¥{p.price_money}</div>
              <div className="lc-prc-period">{periodText(p)}</div>
              {/* 卡底是徽标不是按钮（文档 §3.1-05：徽标 10 Medium 白底黑字） */}
              <span className="lc-prc-badge">{cardAction(p.ptype)}</span>
            </article>
          ))}
        </div>

        {/* 计费口径提示条 */}
        <p className="lc-prc-note"><IdeaIcon size={16} className="lc-prc-noteicon"/>{t('land.pNote')}</p>

        {/* 常见问题 */}
        <h2 className="lc-prc-sec">{t('land.pSectionFaq')}</h2>
        <div className="lc-prc-faq">
          {/* 问答文案自带「Q：」前缀（中英词典同口径），这里只做加粗与分行，不再补序号符号 */}
          {FAQ_INDEXES.map((i) => (
            <p key={i} className="lc-prc-faqitem">
              <b>{t(`land.pFaq${i}Q`)}</b> {t(`land.pFaq${i}A`)}
            </p>
          ))}
        </div>
      </main>

      <footer className="lc-prc-foot">
        © 2026 {brand} · {t('land.pFootPlatform')} · <a href="/docs/terms">{t('land.fTerms')}</a> · <a href="/docs/privacy">{t('land.fPrivacy')}</a>
      </footer>
    </div>
  )
}

// —— 样式：纯黑底 / 面板 #0E1014 / 描边 1.2px #464C58，全部走 --lc-* 令牌 ——
const PRICING_CSS = `
.lc-prc{background:var(--lc-bg);color:var(--lc-text);font-family:var(--lc-font);min-height:100vh;padding-bottom:8px}
.lc-prc a{color:inherit;text-decoration:none}
.lc-prc-nav{display:flex;align-items:center;gap:24px;height:56px;padding:0 40px}
.lc-prc-brand{display:flex;align-items:center;gap:9px;font-size:15px;font-weight:600;white-space:nowrap}
.lc-prc-links{margin-left:auto;display:flex;align-items:center;gap:22px;font-size:13px;color:var(--lc-text-2)}
.lc-prc-links a:hover{color:var(--lc-text)}
.lc-prc-panel{max-width:1018px;margin:12px auto 0;padding:30px 34px 34px;background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-modal)}
.lc-prc-title{margin:0 0 10px;font-size:30px;font-weight:700}
.lc-prc-intro{margin:0 0 6px;font-size:13px;line-height:1.85;color:var(--lc-text-2)}
.lc-prc-sec{display:flex;align-items:center;gap:9px;margin:28px 0 16px;font-size:16px;font-weight:600}
.lc-prc-sec::before{content:"";width:2px;height:15px;background:var(--lc-text-1)}
.lc-prc-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(270px,1fr));gap:26px}
.lc-prc-card{display:flex;flex-direction:column;gap:6px;padding:18px;background:var(--lc-surface-2);border:1.2px solid var(--lc-border-6);border-radius:var(--lc-r-card)}
.lc-prc-name{font-size:14px;font-weight:600}
.lc-prc-meta{font-size:11px;color:var(--lc-text-2)}
.lc-prc-price{font-size:22px;font-weight:700;line-height:1.15;font-family:var(--lc-font-latin)}
.lc-prc-period{font-size:11px;color:var(--lc-text-5)}
.lc-prc-badge{align-self:flex-start;margin-top:6px;padding:3px 8px;font-size:10px;font-weight:500;line-height:1.4;color:#000;background:var(--lc-text-1);border-radius:var(--lc-r-bar)}
.lc-prc-note{display:flex;gap:10px;margin:24px 0 0;padding:16px 18px;font-size:12.5px;line-height:1.85;color:var(--lc-text-2);background:var(--lc-inset);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-card)}
.lc-prc-noteicon{flex:none;color:var(--lc-text-3)}
.lc-prc-faq{margin-bottom:4px}
.lc-prc-faqitem{margin:0 0 16px;font-size:13px;line-height:1.85;color:var(--lc-text-2)}
.lc-prc-faqitem b{color:var(--lc-text);font-weight:600;margin-right:6px}
.lc-prc-foot{padding:20px 24px 26px;text-align:center;font-size:12px;color:var(--lc-text-3)}
.lc-prc-foot a:hover{color:var(--lc-text-2)}
@media (max-width:900px){
  .lc-prc-nav{padding:0 20px;gap:16px}
  .lc-prc-links{gap:16px}
  .lc-prc-panel{margin:12px 16px 0;padding:24px 20px 26px}
}
@media (max-width:620px){
  .lc-prc-nav{height:auto;flex-wrap:wrap;padding:12px 16px}
  .lc-prc-links{width:100%;justify-content:flex-start;flex-wrap:wrap;gap:12px 16px}
  .lc-prc-title{font-size:24px}
}
`
