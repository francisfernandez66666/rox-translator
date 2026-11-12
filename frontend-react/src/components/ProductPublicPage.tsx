// ============================================================================
// components/ProductPublicPage.tsx — 一期对外公开页 /p/{tenantCode}/{pageCode}（施工_一期 §5.7／§5.6）
//
// 匿名免登录可达的两段短码页面。三条硬口径：
//  ① 受控可见性（§5.6 V4）：visibility='gated' 的事实项**值字面量整段不进 DOM**，
//     只出替身文案（product.public.gatedTip）＋ 一个真实留资出口；
//  ② 真实出口＝现成免验证通道 /api/lead/public，复用 index.html #__boot_lead 的四道自保形态
//     （蜜罐 site 位移隐藏、hidden 产品上下文、lead-product: 前缀），不新造第二套留资协议；
//  ③ 全页只消费门面档词典键（dicts.*），不碰 ensureAppPanels() 之后的懒加载面板键
//     —— 否则匿名首屏会露键名（appPanelSplit.test.ts P2 的射程）。
// 访客面「销售忙」文案只在 exhausted_today 出栈，且只经 copyText 这一处出口（§8.5）。
// 本文件由 PublicRoot.tsx 与 App.tsx 的未登录分诊块**各自 React.lazy** 加载，禁止入口静态 import。
// ============================================================================
import { useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { Input, Button } from '@/ui/langcross/src'
import { publicProduct, type PublicItem } from '@/api/product'
import { copyText, type ExhaustReason } from '@/lib/productExhaustCopy'

/** 公开页版式：设计变量一律取 tokens.css 的 --lc-*，不写死色值、不写内联布局样式。 */
const PAGE_CSS = `
.lc-pp-main { max-width: 960px; margin: 0 auto; padding: 24px 20px; color: var(--lc-text); }
.lc-pp-title { font-size: 24px; font-weight: 600; color: var(--lc-text); }
.lc-pp-tagline { color: var(--lc-text-2); }
.lc-pp-note { font-size: 13px; color: var(--lc-text-3); }
.lc-pp-facts { list-style: none; margin: 0; padding: 0; }
.lc-pp-fact { padding: 10px 0; border-bottom: 1px solid var(--lc-border-card-dim); }
.lc-pp-fact-key { margin-right: 12px; color: var(--lc-text-3); }
.lc-pp-lead { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 24px; }
.lc-input.lc-pp-hp { position: absolute; left: -9999px; width: 1px; height: 1px; padding: 0; border: 0; opacity: 0; }
`

/** 对外公开页入参：两段不可枚举短码（创建即定、改名不改 URL，§2.1 H1）；耗尽原因可选注入 */
export interface ProductPublicPageProps {
  tenantCode: string
  pageCode: string
  exhaustReason?: ExhaustReason
}

/** 只留可公开条目：后端那一道 SQL 闸门已经挡掉受控行，这里再写一遍同条件（§5.6 V4 的渲染侧动作） */
function publicOnly(items: PublicItem[]): PublicItem[] {
  return items.filter((f) => f.visibility !== 'gated')
}

/** 对外公开页：未登录门面，套在 PublicShell 未登录分诊块内（§5.7） */
export default function ProductPublicPage({ tenantCode, pageCode, exhaustReason = 'available' }: ProductPublicPageProps) {
  const [, t] = useT()
  const [name, setName] = useState('')
  const [tagline, setTagline] = useState('')
  const [facts, setFacts] = useState<PublicItem[]>([])
  const [gatedCount, setGatedCount] = useState(0)
  const [notFound, setNotFound] = useState(false)
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    let alive = true
    publicProduct(tenantCode, pageCode)
      .then((r) => {
        if (!alive) return
        if (!r.success || r.not_found || !r.product) {
          setNotFound(true)
        } else {
          setName(String(r.product.name || ''))
          setTagline(String(r.product.tagline || ''))
          setFacts(Array.isArray(r.facts) ? r.facts : [])
          // 替身文案的依据由后端给**条数**（值字面量永不出栈），前端不再靠"少了几行"猜
          setGatedCount(Number(r.gated_count || 0))
        }
      })
      .catch(() => { if (alive) setNotFound(true) })
      .finally(() => { if (alive) setLoaded(true) })
    return () => { alive = false }
  }, [tenantCode, pageCode])

  const visible = publicOnly(facts)
  const busyCopy = exhaustReason === 'exhausted_today' ? copyText('exhausted_today', 'visitor') : ''
  const leadSource = `lead-product:${tenantCode}:${pageCode}`

  return (
    <>
      <style>{PAGE_CSS}</style>
      <main className="lc-pp-main">
        <h1 className="lc-pp-title">{name || t('product.public.title')}</h1>
        {tagline && <p className="lc-pp-tagline">{tagline}</p>}
        <p className="lc-pp-note">{t('product.public.seoNotice')}</p>

        {loaded && notFound && <p data-testid="public-not-found">{t('product.public.notFound')}</p>}

        <ul className="lc-pp-facts">
          {visible.map((f) => (
            <li key={f.id} className="lc-pp-fact">
              <span className="lc-pp-fact-key">{f.key}</span>
              <span>{f.text}</span>
            </li>
          ))}
        </ul>

        {/* 受控项整段不出：只出替身文案，绝不渲染 gated 行的 value（§5.6） */}
        {gatedCount > 0 && <p data-testid="gated-tip">{t('product.public.gatedTip')}</p>}
        {busyCopy && <p data-testid="visitor-exhaust">{busyCopy}</p>}

        {/* 公开面 SEO／留资契约的例外：后端只认 /api/lead/public 的浏览器原生表单 POST，
            故保留 <form action=...> 原生提交（不挂 onSubmit、不 preventDefault），内部控件全部换成
            组件库；蜜罐 site 仍走位移隐藏而非 display:none；受控字段与短码值不进 meta/JSON-LD/首屏。 */}
        <form className="lc-pp-lead" action="/api/lead/public" method="post" autoComplete="off" data-testid="lead-form">
          <Input type="text" name="company" required maxLength={100}
                 placeholder={t('land.leadCompany')} aria-label={t('land.leadCompany')} />
          <Input type="email" name="email" required maxLength={120}
                 placeholder={t('land.leadEmail')} aria-label={t('land.leadEmail')} />
          <Input className="lc-pp-hp" type="text" name="site" value="" tabIndex={-1}
                 autoComplete="off" aria-hidden="true" readOnly />
          <Input type="hidden" name="source" value={leadSource} />
          <Input type="hidden" name="tenant_code" value={tenantCode} />
          <Input type="hidden" name="page_code" value={pageCode} />
          <Input type="hidden" name="lead_source" value="lead-product:" />
          <Button type="submit">{t('product.public.leadCta')}</Button>
        </form>
      </main>
    </>
  )
}
