// ============================================================================
// components/LeadForm.tsx — 官网留资表单（★ P1-3，2026-09-18）
// 挂在 Landing 收尾 CTA 白块内：公司/邮箱/意向语言 + 可选留言 → POST /api/lead，
// 后端落 feedbacks（target_type='lead'），超管反馈面板即跟进工作台。
// 口径：
//   1. 文案全部走 land.lead* 键（src/i18n/panels/landing.ts，中英同步）；
//   2. 人机验证条件接入——register-config 开 captcha 才渲染 Turnstile（同 Login.tsx 模式）；
//   3. 蜜罐字段 site：视觉上不存在（display:none 由 .lc-lead-hp 承担），真人永不触发；
//   4. 样式类 .lc-lead-* 定义在 Landing.tsx 的 LANDING_CSS 里（官网视觉唯一来源，不另开 CSS 文件）。
// ============================================================================
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { registerConfig, createLead } from '@/api'
import { useT } from '@/i18n'

/** 提交状态机：idle=可编辑，busy=请求中，ok=成功（表单整体换成回执），err=失败（保留输入可重试） */
type Phase = 'idle' | 'busy' | 'ok' | 'err'

export interface LeadFormProps {
  source?: string // 来源页标记（landing/pricing/footer），进后端白名单校验
}

/** 意向语言候选：与产品语种盘对齐的常用项，纯文本逗号拼接即可，不做多选控件 */
const LANG_OPTIONS = ['English', '日本語', '한국어', 'Deutsch', 'Français', 'Español']

export function LeadForm({ source = 'landing' }: LeadFormProps) {
  const [, t] = useT() // 语种位留空：t 内部已订阅语种变化触发重渲染
  const [company, setCompany] = useState('')
  const [email, setEmail] = useState('')
  const [langs, setLangs] = useState<string[]>([])
  const [message, setMessage] = useState('')
  const [honeypot, setHoneypot] = useState('') // 蜜罐：唯一允许出现在 DOM 却永远为空（真人看不见、bot 必填）
  const [phase, setPhase] = useState<Phase>('idle')
  const [errText, setErrText] = useState('')
  const [captchaOn, setCaptchaOn] = useState(false)
  const captchaBoxRef = useRef<HTMLDivElement>(null)
  const captchaTokenRef = useRef('')

  // 只在后台开启人机验证时才拉 Turnstile：未开启时表单零第三方请求
  useEffect(() => {
    let dead = false // 卸载守卫：config 返回时组件可能已随路由卸载
    ;(async () => {
      try {
        const c = await registerConfig()
        const cap = c as unknown as { captcha_enabled?: boolean; captcha_site_key?: string }
        if (!dead && c.success && cap.captcha_enabled && cap.captcha_site_key) {
          setCaptchaOn(true)
          renderTurnstile(cap.captcha_site_key)
        }
      } catch { /* 配置拉取失败按「无需验证码」处理：后端提交时仍会兜底校验 */ }
    })()
    return () => { dead = true }
  }, [])

  // Cloudflare Turnstile 显式渲染（与 Login.tsx 同一套守卫：已有 widget 不重建、script 全局只注入一份）
  function renderTurnstile(siteKey: string) {
    const mount = () => {
      const el = captchaBoxRef.current
      const ts = (window as unknown as { turnstile?: { render: (el: HTMLElement, o: unknown) => void } }).turnstile
      if (!el || !ts || el.childElementCount > 0) return
      ts.render(el, {
        sitekey: siteKey,
        callback: (tk: string) => { captchaTokenRef.current = tk },
        'expired-callback': () => { captchaTokenRef.current = '' },
      })
    }
    const w = window as unknown as { turnstile?: unknown; __tsLoading?: boolean }
    if (w.turnstile) { mount(); return }
    if (w.__tsLoading) return
    w.__tsLoading = true
    const s = document.createElement('script')
    s.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
    s.async = true
    s.onload = mount
    document.head.appendChild(s)
  }

  function validEmail(v: string): boolean {
    const at = v.lastIndexOf('@')
    if (at <= 0 || at !== v.indexOf('@') || /\s/.test(v)) return false
    const dom = v.slice(at + 1)
    const dot = dom.lastIndexOf('.')
    return dot > 0 && dot < dom.length - 1 && !/[(),;]/.test(dom)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (phase === 'busy') return // 双击护栏：请求在途时按钮已禁用，但回车仍可能二次触发
    if (!company.trim()) { setErrText(t('land.leadNeedCompany')); setPhase('err'); return }
    if (!validEmail(email.trim())) { setErrText(t('land.leadNeedEmail')); setPhase('err'); return }
    setPhase('busy')
    try {
      await createLead({
        company: company.trim(),
        email: email.trim().toLowerCase(),
        langs: langs.join(','),
        message: message.trim(),
        source,
        captcha_token: captchaTokenRef.current,
        site: honeypot, // 蜜罐原样送出：真人恒空串，后端见非空即静默假成功
      })
      setPhase('ok')
    } catch (err) {
      setErrText(err instanceof Error && err.message ? err.message : t('land.leadFail'))
      setPhase('err')
    }
  }

  if (phase === 'ok') {
    return (
      <div className="lc-lead-ok" role="status">
        {/* 成功回执整块替换表单：字段已完成使命，留着只会引导用户再提交一次 */}
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M4 12.5l5 5L20 6.5" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
        {t('land.leadSuccess')}
      </div>
    )
  }

  return (
    <form className="lc-lead" onSubmit={submit} noValidate>
      <div className="lc-lead-row">
        <label className="lc-lead-field">
          <span className="lc-lead-label">{t('land.leadCompany')}</span>
          <input className="lc-input lc-lead-input" value={company} onChange={(e) => setCompany(e.target.value)}
            maxLength={100} autoComplete="organization" required />
        </label>
        <label className="lc-lead-field">
          <span className="lc-lead-label">{t('land.leadEmail')}</span>
          <input className="lc-input lc-lead-input" type="email" value={email} onChange={(e) => setEmail(e.target.value)}
            maxLength={120} autoComplete="email" required />
        </label>
      </div>
      <div className="lc-lead-langs">
        <span className="lc-lead-label">{t('land.leadLangs')}</span>
        {/* 语言胶囊多选：纯前端 set 拼接，不对接任何字典接口（意向登记不是下单） */}
        <div className="lc-lead-chips">
          {LANG_OPTIONS.map((lg) => (
            <button type="button" key={lg} aria-pressed={langs.includes(lg)}
              className={'lc-lead-chip' + (langs.includes(lg) ? ' on' : '')}
              onClick={() => setLangs((cur) => (cur.includes(lg) ? cur.filter((x) => x !== lg) : [...cur, lg]))}>
              {lg}
            </button>
          ))}
        </div>
      </div>
      <label className="lc-lead-field">
        <span className="lc-lead-label">{t('land.leadMessage')}</span>
        <textarea className="lc-input lc-lead-input lc-lead-msg" value={message} onChange={(e) => setMessage(e.target.value)}
          maxLength={500} rows={2} />
      </label>
      {/* 蜜罐：tabIndex=-1 + aria-hidden + 视觉隐藏，真人不可能填到 */}
      <div className="lc-lead-hp" aria-hidden="true">
        <input tabIndex={-1} autoComplete="off" value={honeypot} onChange={(e) => setHoneypot(e.target.value)} />
      </div>
      {captchaOn && <div ref={captchaBoxRef} className="lc-lead-captcha" />}
      {phase === 'err' && <p className="lc-lead-err" role="alert">{errText}</p>}
      <button type="submit" className="lc-lead-btn" disabled={phase === 'busy'}>
        {phase === 'busy' ? t('land.leadSubmitting') : t('land.leadSubmit')}
      </button>
    </form>
  )
}
