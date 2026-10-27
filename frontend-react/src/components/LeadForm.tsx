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
// ★ 2026-10-27 D-02 批：留资表单此前**自己注入了一份 api.js**（旧 `__tsLoading` 守卫），
//   正是 2026-09-24 那次「两方各注入、另一方的回调永远等不到」的形态复发；
//   且只有它有选档、另外三处挂件没有。现在装载/选档/渲染/销毁全部走 lib/turnstile 那一份唯一实现。
import {
  loadTurnstile, renderTurnstile as mountTurnstileWidget, pickTurnstileSize,
  removeTurnstile, reexecTurnstile, retryTurnstile, turnstileLoadState,
  type TurnstileLoadState, type TurnstileSize,
} from '@/lib/turnstile'

/** 提交状态机：idle=可编辑，busy=请求中，ok=成功（表单整体换成回执），err=失败（保留输入可重试） */
type Phase = 'idle' | 'busy' | 'ok' | 'err'

// 留资表单入参：来源标记（落地页/定价页等，进埋点与站内信文案）
export interface LeadFormProps {
  source?: string // 来源页标记（landing/pricing/footer），进后端白名单校验
}

/** 意向语言候选：与产品语种盘对齐的常用项，纯文本逗号拼接即可，不做多选控件 */
const LANG_OPTIONS = ['English', '日本語', '한국어', 'Deutsch', 'Français', 'Español']

// 留资表单：提交后由后端建线索并回 toast，前端不落任何 token 裸值
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
  // ★ 2026-10-26 P2-01 补腿（新 e2e 抓到）：sitekey 必须进 state 并由 effect 驱动挂载。
  //   旧形态在拉配置的同一个 tick 里 `setCaptchaOn(true)` + `renderTurnstile(...)`，
  //   而容器 `{captchaOn && <div ref=...>}` 要等这次提交后才进 DOM ⇒ mount() 读到 null ref
  //   直接 return，且此后再没有人重试 ⇒ **人机验证挂件永远不渲染**（表单看起来正常，提交必被拦）。
  const [captchaSiteKey, setCaptchaSiteKey] = useState('')
  // ★ 2026-10-27 D-02：装载状态必须进渲染——「还在加载」与「确定加载不出来」是两张脸，
  //   旧形态两者都不出（容器空着 ＋ 提交时才报一句没有出路的红字）。
  const [captchaState, setCaptchaState] = useState<TurnstileLoadState>(turnstileLoadState())
  const captchaBoxRef = useRef<HTMLDivElement>(null)
  const captchaTokenRef = useRef('')
  // ★ 2026-10-26 P2-01：记录当前 widget id / 尺寸档 / sitekey，
  // 供视口跨档时 remove + 重挂（换尺寸后旧 token 失效，必须让用户重新验证）。
  const turnstileIdRef = useRef<string | number | null>(null)
  const turnstileSizeRef = useRef<TurnstileSize>('normal')
  const turnstileSiteKeyRef = useRef('')

  // 只在后台开启人机验证时才拉 Turnstile：未开启时表单零第三方请求
  useEffect(() => {
    let dead = false // 卸载守卫：config 返回时组件可能已随路由卸载
    ;(async () => {
      try {
        const c = await registerConfig()
        const cap = c as unknown as { captcha_enabled?: boolean; captcha_site_key?: string }
        if (!dead && c.success && cap.captcha_enabled && cap.captcha_site_key) {
          // 只落状态；真正的 render 在下面的 effect 里（容器 ref 先落 DOM 再挂件）。
          setCaptchaSiteKey(cap.captcha_site_key)
          setCaptchaOn(true)
        }
      } catch { /* 配置拉取失败按「无需验证码」处理：后端提交时仍会兜底校验 */ }
    })()
    return () => { dead = true }
  }, [])

  // mountWidget 按**容器现宽**选档渲染挂件；容器里已有挂件就不重建（重建会清掉用户刚完成的验证）。
  function mountWidget() {
    const el = captchaBoxRef.current
    const key = turnstileSiteKeyRef.current
    if (!el || !key) return
    const size = pickTurnstileSize(el)
    const id = mountTurnstileWidget(el, key, (tk) => { captchaTokenRef.current = tk }, { size })
    if (id !== null) {
      turnstileIdRef.current = id
      turnstileSizeRef.current = size
    }
  }

  // 容器进 DOM 之后订阅装载：ready 才真挂；loading/unavailable 只改界面上给访客看的那张脸。
  // 装载与选档都不在这里各写一遍（唯一实现在 lib/turnstile），本组件只消费。
  useEffect(() => {
    if (!captchaOn || !captchaSiteKey) return
    turnstileSiteKeyRef.current = captchaSiteKey
    const cancel = loadTurnstile(() => mountWidget(), (s) => setCaptchaState(s))
    return cancel
    // eslint-disable-next-line react-hooks/exhaustive-deps -- mountWidget 只读 ref 与固定入参，按 captchaOn/sitekey 触发即可
  }, [captchaOn, captchaSiteKey])

  // 视口跨档（桌面↔手机）时重挂 widget：尺寸档变了才动作，普通缩放不会反复重建
  // （避免用户验证到一半被清掉）；容器空着时（上一档被卸载/首次迟到）直接补挂。
  useEffect(() => {
    if (!captchaOn) return
    const onResize = () => {
      const el = captchaBoxRef.current
      if (!el || !window.turnstile) return
      if (el.childElementCount === 0) { mountWidget(); return }
      if (pickTurnstileSize(el) === turnstileSizeRef.current) return
      removeTurnstile(turnstileIdRef.current)
      turnstileIdRef.current = null
      captchaTokenRef.current = ''
      el.innerHTML = ''
      mountWidget()
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 同上：mountWidget 是稳定闭包
  }, [captchaOn])

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
    // ★ D-02（2026-10-27）：开验证却没拿到 token 时，**红字必须按装载状态分派**——
    //   「还在加载」与「确定加载不出来」不是一件事：前者再等一会儿就好（不发注定被后端拒的请求），
    //   后者要给「重试」出路（旧形态只留一句「请先完成人机验证」，指着一个永远不会出现的框）。
    if (captchaOn && !captchaTokenRef.current) {
      setErrText(t(captchaState === 'unavailable' ? 'auth.captchaUnavailable' : 'auth.captchaLoading'))
      setPhase('err')
      return
    }
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
      // token 一次一用：提交失败也可能已被服务端消费，清空并强制重新挑战领新码（同 Login.tsx 口径）
      if (captchaOn) { captchaTokenRef.current = ''; reexecTurnstile(turnstileIdRef.current) }
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
      {/* ★ D-02（2026-10-27）：装载中的读数要给访客看，放弃时更要给一条出路。
          旧形态这一整块什么都没有——容器空着、界面正常，直到点提交才撞一句「请先完成人机验证」，
          而那一句指着一个永远不会自己出现的框。 */}
      {captchaOn && captchaState === 'loading' && (
        <p className="lc-lead-hint" role="status" data-testid="captcha-notice">{t('auth.captchaLoading')}</p>
      )}
      {captchaOn && captchaState === 'unavailable' && (
        <p className="lc-lead-hint" role="alert" data-testid="captcha-notice">
          {t('auth.captchaUnavailable')}
          {' '}
          <button type="button" className="lc-lead-chip lc-lead-captcha-retry" data-testid="captcha-retry" onClick={() => retryTurnstile()}>
            {t('auth.captchaRetry')}
          </button>
        </p>
      )}
      {phase === 'err' && <p className="lc-lead-err" role="alert">{errText}</p>}
      <button type="submit" className="lc-lead-btn" disabled={phase === 'busy'}>
        {phase === 'busy' ? t('land.leadSubmitting') : t('land.leadSubmit')}
      </button>
    </form>
  )
}
