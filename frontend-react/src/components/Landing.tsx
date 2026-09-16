// ============================================================================
// components/Landing.tsx — S5 官网落地页（★ 2026-09-14 试运营门面）
// 未登录访客访问 `/` 时渲染本页（登录迁 /login、注册迁 /register，App 门内切换）。
// 叙事口径按《品牌一页纸》：专业准确 + 原版式交付，透明计价为辅助卖点；
// 价目卡数据源 /api/plans（积分面值），CTA 捕获 UTM 存 localStorage（S4 归因前置）。
// ============================================================================
import { useEffect, useState } from 'react'
import { Button } from 'tdesign-react'
import { request } from '@/api/core'
import { t } from '@/i18n'

/** PlanLite 定价页套餐轻量出参（积分/价格/有效期） */
interface PlanLite { code: string; name: string; ptype: string; points: number; price_money: number; duration_days: number }

// 注册归因采集的 UTM 参数名清单（落 tenants 归因字段）
const UTMS = ['utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content']

/** Landing 官网落地页：品牌展示 + 定价 + 注册归因（未登录首页） */
export default function Landing() {
  const [plans, setPlans] = useState<PlanLite[]>([])
  const [trial, setTrial] = useState<{ points: number; days: number }>({ points: 1000, days: 14 })

  // UTM 捕获：落地页与注册页共用（注册提交时随 ref 一并上报为 S4 后续项）
  useEffect(() => {
    const q = new URLSearchParams(window.location.search)
    const saved: Record<string, string> = {}
    UTMS.forEach((k) => { const v = q.get(k); if (v) saved[k] = v })
    if (Object.keys(saved).length) {
      try { localStorage.setItem('utm', JSON.stringify({ ...saved, captured_at: Date.now() })) } catch { /* ignore */ }
    }
    void request('/api/plans').then((r: any) => {
      if (r?.success) {
        setPlans((r.plans || []).filter((p: PlanLite) => p.ptype === 'paid').slice(0, 4))
        setTrial({ points: r.free_trial_points ?? 1000, days: r.free_trial_days ?? 14 })
      }
    }).catch(() => { /* 价目卡留空即可 */ })
  }, [])

  return (
    <div style={{ fontFamily: 'inherit', color: 'var(--td-text-color-primary, #1a1a2e)' }}>
      {/* 顶栏 */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '14px 6%', borderBottom: '1px solid var(--td-border-level-1-color, #eee)', position: 'sticky', top: 0, background: 'rgba(255,255,255,.92)', backdropFilter: 'blur(8px)', zIndex: 10 }}>
        <b style={{ fontSize: 18 }}>🌐 {t('land.brand')}</b>
        <div style={{ display: 'flex', gap: 18, alignItems: 'center', fontSize: 14 }}>
          <a href="#features" style={{ color: 'inherit', textDecoration: 'none' }}>{t('land.navFeatures')}</a>
          <a href="#pricing" style={{ color: 'inherit', textDecoration: 'none' }}>{t('land.navPricing')}</a>
          <a href="/pricing" style={{ color: 'inherit', textDecoration: 'none' }}>{t('land.navDetails')}</a>
          <a href="/login" style={{ color: 'inherit', textDecoration: 'none' }}>{t('land.navLogin')}</a>
          <Button size="small" theme="primary" onClick={() => { window.location.href = '/register' }}>{t('land.ctaRegister')}</Button>
        </div>
      </div>

      {/* 头图区 */}
      <div style={{ padding: '72px 6% 56px', textAlign: 'center', background: 'linear-gradient(180deg, #eef2ff 0%, #ffffff 100%)' }}>
        <div style={{ fontSize: 13, color: 'var(--td-brand-color, #2f47f5)', fontWeight: 600, letterSpacing: 2 }}>{t('land.heroKicker')}</div>
        <h1 style={{ fontSize: 40, lineHeight: 1.25, margin: '14px auto 10px', maxWidth: 780 }}>{t('land.heroTitle')}</h1>
        <p style={{ fontSize: 17, color: 'var(--td-text-color-secondary, #555)', maxWidth: 640, margin: '0 auto 26px' }}>{t('land.heroSub')}</p>
        <div style={{ display: 'flex', gap: 12, justifyContent: 'center', flexWrap: 'wrap' }}>
          <Button theme="primary" size="large" onClick={() => { window.location.href = '/register' }}>{t('land.ctaFree')}</Button>
          <Button variant="outline" size="large" onClick={() => { window.location.href = '/login' }}>{t('land.ctaLogin')}</Button>
        </div>
        <div style={{ marginTop: 16, fontSize: 13, color: 'var(--td-text-color-placeholder, #888)' }}>
          {t('land.heroTrialNote').replace('{points}', String(trial.points)).replace('{days}', String(trial.days))}
        </div>
      </div>

      {/* 三卖点 */}
      <div id="features" style={{ padding: '56px 6%', display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(260px,1fr))', gap: 20, maxWidth: 1080, margin: '0 auto' }}>
        {(['accuracy', 'format', 'pricing'] as const).map((k) => (
          <div key={k} style={{ border: '1px solid var(--td-border-level-1-color, #eee)', borderRadius: 12, padding: '22px 20px' }}>
            <div style={{ fontSize: 22 }}>{k === 'accuracy' ? '🎯' : k === 'format' ? '📑' : '🧾'}</div>
            <b style={{ display: 'block', margin: '10px 0 6px', fontSize: 16 }}>{t(`land.f.${k}.t`)}</b>
            <span style={{ fontSize: 14, color: 'var(--td-text-color-secondary, #555)', lineHeight: 1.7 }}>{t(`land.f.${k}.d`)}</span>
          </div>
        ))}
      </div>

      {/* 价目卡 */}
      <div id="pricing" style={{ background: '#f7f8fc', padding: '56px 6%' }}>
        <h2 style={{ textAlign: 'center', fontSize: 26, margin: '0 0 8px' }}>{t('land.pricingTitle')}</h2>
        <p style={{ textAlign: 'center', fontSize: 14, color: 'var(--td-text-color-secondary, #555)', margin: '0 0 28px' }}>{t('land.pricingSub')}</p>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(200px,1fr))', gap: 14, maxWidth: 980, margin: '0 auto' }}>
          {plans.map((p) => (
            <div key={p.code} style={{ background: '#fff', borderRadius: 12, padding: 18, border: '1px solid var(--td-border-level-1-color, #eee)', display: 'flex', flexDirection: 'column', gap: 6 }}>
              <b>{p.name}</b>
              <div style={{ fontSize: 26, fontWeight: 700, color: 'var(--td-brand-color, #2f47f5)' }}>¥{p.price_money}<small style={{ fontSize: 12, fontWeight: 400 }}> / {p.duration_days}{t('land.unitDay')}</small></div>
              <div style={{ fontSize: 13, color: 'var(--td-text-color-secondary, #555)' }}>{p.points.toLocaleString()} {t('land.pointsUnit')}</div>
              <div style={{ fontSize: 12, color: '#c66900' }}>{t('land.halfOff')}</div>
              <Button size="small" theme="primary" variant="outline" style={{ marginTop: 6 }} onClick={() => { window.location.href = '/register' }}>{t('land.ctaRegister')}</Button>
            </div>
          ))}
        </div>
        <div style={{ textAlign: 'center', marginTop: 22, fontSize: 13 }}>
          <a href="/pricing" style={{ color: 'var(--td-brand-color, #2f47f5)' }}>{t('land.pricingAll')}</a>
        </div>
      </div>

      {/* 信任条 + FAQ */}
      <div style={{ padding: '52px 6%', maxWidth: 900, margin: '0 auto' }}>
        <div style={{ display: 'flex', gap: 22, flexWrap: 'wrap', justifyContent: 'center', fontSize: 13, color: 'var(--td-text-color-secondary, #555)', marginBottom: 34 }}>
          <span>🔒 {t('land.trustIsolation')}</span><span>🛡️ {t('land.trustCompliance')}</span><span>📄 <a href="/docs/sla" style={{ color: 'inherit' }}>{t('land.trustSla')}</a></span><span>👥 {t('land.trustReview')}</span>
        </div>
        {[1, 2, 3].map((i) => (
          <div key={i} style={{ marginBottom: 18 }}>
            <b style={{ display: 'block', marginBottom: 4 }}>{t(`land.faq${i}.q`)}</b>
            <span style={{ fontSize: 14, color: 'var(--td-text-color-secondary, #555)', lineHeight: 1.7 }}>{t(`land.faq${i}.a`)}</span>
          </div>
        ))}
      </div>

      {/* 底 CTA + 页脚 */}
      <div style={{ background: 'var(--td-brand-color, #2f47f5)', color: '#fff', textAlign: 'center', padding: '46px 6%' }}>
        <h2 style={{ margin: '0 0 10px', fontSize: 24 }}>{t('land.bottomTitle')}</h2>
        <p style={{ margin: '0 0 20px', opacity: .9, fontSize: 14 }}>{t('land.bottomSub')}</p>
        <Button theme="default" variant="base" size="large" onClick={() => { window.location.href = '/register' }}>{t('land.ctaBottom')}</Button>
      </div>
      <div style={{ textAlign: 'center', fontSize: 12, color: 'var(--td-text-color-placeholder, #999)', padding: '18px 6%' }}>
        © 2026 {t('land.brand')} · <a href="/docs/terms" style={{ color: 'inherit' }}>{t('land.fTerms')}</a> · <a href="/docs/privacy" style={{ color: 'inherit' }}>{t('land.fPrivacy')}</a> · <a href="/pricing" style={{ color: 'inherit' }}>{t('land.fPricing')}</a>
      </div>
    </div>
  )
}
