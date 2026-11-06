// ============================================================================
// components/Onboarding.tsx — O 组「注册/登录后没有产品就引导」的两个面（2026-11-06）
//
//   · 默认导出 `Onboarding` ＝ 引导页本体（步骤全部由 lib/onboardingSchema 驱动，本页不写死任何一步）
//   · 具名导出 `OnboardingPrompt` ＝ 登录后那条**不挡界面的底部横幅**（判据现读，读失败一律不出）
//
// ★ 四条口径（为什么长成这个样子）：
//  ① 判据只认「确实没有」：`productList` 必须 success:true 且数组长度为 0 才提醒。
//     接口 500／超时／返回非数组都不算——把一次抖动读成"这人没产品"，会把老用户一次次推进向导
//     （AGENTS §一·5「空态不许伪装成检查过」在引导流上的同一形态）。
//  ② 横幅用 `position:fixed` 的底部条，**不进主布局流**：塞进 Routes 上方会推动整页内容，
//     后台像素锁（工具条/框脚几何档）会因为一条"只有新用户才看得见"的横幅而随机翻红。
//  ③ 不与 ProfileSetupGate 抢屏：那个闸门管"改密码"，走完它才会走到这里（顺序写在 Root 的挂载条件里），
//     两个全屏引导同屏＝"弹两次点了没反应"那一族观感。
//  ④ 完成判据不落库：有没有产品这件事本身就是判据，加一列"引导完成标记"只会多一个漏挂钩的写点。
//
// 本文件由 App.tsx 的 React.lazy 加载（引导页），Prompt 同样走懒加载分片。
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useT } from '@/i18n'
import { useNav } from '@/lib/nav'
import { productList, productSave, seoSet } from '@/api/product'
import { SCENE_STEPS, LANG_OPTIONS, WRITE_TARGETS, parseOnboardScene, stepIsCompletable } from '@/lib/onboardingSchema'

/** 引导页：按场景取步骤，逐步写口，走完即落到产品详情页 */
export default function Onboarding() {
  const [, t] = useT()
  const nav = useNav()
  const scene = parseOnboardScene(new URLSearchParams(nav.search || '').get('scene'))
  const steps = SCENE_STEPS[scene]
  const [idx, setIdx] = useState(0)
  const [values, setValues] = useState<Record<string, unknown>>({ indexable: true })
  const [productId, setProductId] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const step = steps[Math.min(idx, steps.length - 1)]
  const canNext = useMemo(() => stepIsCompletable(step, values), [step, values])

  /** 执行一步的写口：口名全部来自 WRITE_TARGETS 那张真值表，配置里写了不存在的口就当场报错（不静默） */
  const commit = useCallback(async (s: typeof step, v: Record<string, unknown>) => {
    const target = WRITE_TARGETS[s.write]
    if (s.write === 'skip' || target === '') return true
    if (s.write === 'product.profile') {
      const r = await productSave({ name: String(v.name || ''), tagline: String(v.tagline || ''), use_case: String(v.use_case || '') }).catch(() => null)
      if (!r || !r.success) { setError((r && (r as { message?: string }).message) || t('onboard.saveFailed')); return false }
      const id = String((r as { id?: unknown }).id || '')
      if (id) setProductId(id)
      return true
    }
    if (s.write === 'product.langs') {
      // 没勾就是**空数组**（＝显式"暂未设定"），这与"省略＝不表态"是两种话，界面要能分开显示
      const langs = Array.isArray(v.target_langs) ? (v.target_langs as string[]) : []
      if (!productId) return true // 还没建档就跳到这步（不该发生，但也不许凭空写库）
      // ★ 带 id 的 save 是"把这份档案存回去"，**不是 PATCH**：只交 `{id, target_langs}` 会被后端
      //   判成"名字空了"（400），而就算后端放过空串，第 2 步收到的 tagline/use_case 也会被这一枪
      //   静默洗成空——那是 §29「省略＝不表态」在产品面上第三次长出来。⇒ 这里必须整份交。
      const r = await productSave({
        id: productId,
        name: String(v.name || ''),
        tagline: String(v.tagline || ''),
        use_case: String(v.use_case || ''),
        target_langs: langs,
      }).catch(() => null)
      if (!r || !r.success) { setError((r && (r as { message?: string }).message) || t('onboard.saveFailed')); return false }
      return true
    }
    if (s.write === 'product.seo') {
      if (!productId) return true
      const r = await seoSet(productId, v.indexable !== false).catch(() => null)
      if (!r || !r.success) { setError((r && (r as { message?: string }).message) || t('onboard.saveFailed')); return false }
      return true
    }
    setError(t('onboard.unknownTarget'))
    return false
  }, [productId, t])

  const next = async () => {
    setBusy(true); setError('')
    const ok = await commit(step, values)
    setBusy(false)
    if (!ok) return
    if (idx >= steps.length - 1) {
      nav.navigate(productId ? `/products/detail?id=${encodeURIComponent(productId)}` : '/products/detail', { replace: true })
      return
    }
    setIdx(idx + 1)
  }

  return (
    <section data-testid="onboarding">
      <h2>{t('onboard.title')}</h2>
      <p data-testid="onboard-progress">{t(`onboard.step.${step.id}`)} · {idx + 1}/{steps.length}</p>
      <p>{t(`onboard.step.${step.id}.desc`)}</p>
      {error && <p data-testid="onboard-error" role="alert">{error}</p>}
      {step.fields.map((f) => (
        <div key={f.key} data-testid={`onboard-field-${f.key}`}>
          <label>{t(f.labelKey)}</label>
          {f.kind === 'text' && (
            <input value={String(values[f.key] ?? '')} onChange={(e) => setValues({ ...values, [f.key]: e.target.value })} />
          )}
          {f.kind === 'textarea' && (
            <textarea value={String(values[f.key] ?? '')} onChange={(e) => setValues({ ...values, [f.key]: e.target.value })} />
          )}
          {f.kind === 'toggle' && (
            <input type="checkbox" checked={values[f.key] !== false} onChange={(e) => setValues({ ...values, [f.key]: e.target.checked })} />
          )}
          {f.kind === 'langs' && (
            <div data-testid="onboard-langs">
              {LANG_OPTIONS.map((code) => {
                const picked = Array.isArray(values[f.key]) && (values[f.key] as string[]).includes(code)
                return (
                  <label key={code}>
                    <input type="checkbox" checked={picked}
                      onChange={(e) => {
                        const cur = Array.isArray(values[f.key]) ? [...(values[f.key] as string[])] : []
                        if (e.target.checked) {
                          if (!f.maxPick || cur.length < f.maxPick) cur.push(code)
                        } else {
                          cur.splice(cur.indexOf(code), 1)
                        }
                        setValues({ ...values, [f.key]: cur })
                      }} />
                    {code}
                  </label>
                )
              })}
            </div>
          )}
          {f.hintKey && <p>{t(f.hintKey)}</p>}
        </div>
      ))}
      <button data-testid="onboard-next" disabled={!canNext || busy} onClick={() => void next()}>{t('onboard.next')}</button>
      <button data-testid="onboard-later" onClick={() => nav.navigate('/', { replace: true })}>{t('onboard.later')}</button>
    </section>
  )
}

/**
 * OnboardingPrompt —— 登录后那条底部横幅。
 *
 * 判据现读、失败不出；同一次会话里关掉就不再出现（会话态只用来**避免重复打扰**，
 * 不用来充当"走没走过"的事实源——那是两条不同的东西，混用就会误伤老用户）。
 */
export function OnboardingPrompt() {
  const [, t] = useT()
  const nav = useNav()
  const [show, setShow] = useState(false)
  const KEY = '***'

  useEffect(() => {
    if (sessionStorage.getItem(KEY) === '1') return
    let alive = true
    productList()
      .then((r) => {
        if (!alive) return
        // 只有「确实取到、确实为空」才提醒；其余形态（失败/字段缺/不是数组）一律不出
        if (r && r.success === true && Array.isArray(r.products) && r.products.length === 0) setShow(true)
      })
      .catch(() => { /* 取不到＝不知道，不许据此把人推进引导 */ })
    return () => { alive = false }
  }, [])

  if (!show) return null
  return (
    <div data-testid="onboarding-prompt" role="status" style={{ position: 'fixed', left: 0, right: 0, bottom: 0, zIndex: 90 }}>
      <span>{t('onboard.prompt')}</span>
      <button data-testid="prompt-goto-onboarding" onClick={() => nav.navigate('/onboarding')}>{t('onboard.prompt.cta')}</button>
      <button onClick={() => { sessionStorage.setItem(KEY, '1'); setShow(false) }}>{t('onboard.later')}</button>
    </div>
  )
}
