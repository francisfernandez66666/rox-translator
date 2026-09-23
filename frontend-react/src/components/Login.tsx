// ============================================================================
// components/Login.tsx — 登录 / 自助注册 / 忘记密码（LangCross 纯黑组件库实现）
// 设计：认证卡用 AuthCard（380 宽、内边距 32、圆角 14、外层 .lc-auth-bg）。
// 几何口径来自画布 57:3（登录）/ 57:117（找回密码）实测；三屏标题字号见 components.css 注释。
// 注册交互：点「免费注册」→ 传统表单卡闪烁三次 → 整页 120ms 退出 → AI 助理面板接管
//          （AiRegisterFlow.tsx，交付包 §5 / demo-register-ai-motion.html）。
// 业务逻辑与 API 调用（登录/注册/验证码/找回密码/首登改密/Turnstile/UTM）原样保留。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import {
  AuthCard, Field, Input, Button, Checkbox, Dialog, useToast, EyeIcon, EyeOffIcon,
} from '@/ui/langcross/src'
import { readUtm, clearUtm } from '@/utils/utm'
import {
  login, authRegister, sendEmailCode, registerConfig,
  forgotPassword, resetPassword, changePassword,
  setAuthToken, setActiveTenantId,
  ssoProviders, ssoLoginUrl, type AuthUser, type SSOProvider,
} from '@/api'
import { t, useT } from '@/i18n'
import { LangSelect } from '@/components/LangSelect' // ★ #23：12 语种语言下拉
import { useBranding, DEFAULT_BRAND_NAME } from '@/branding'
import { roleLevel } from '@/stores/auth'
import { industryCodeOf, industryOptions } from '@/lib/industries'
import { PERSONA_FALLBACK } from '@/lib/personas'
import { useCountdown } from '@/lib/useCountdown' // ★ #42：验证码冷却收口（自带卸载清理 + 幂等重启）
import AiRegisterFlow from './AiRegisterFlow'

/** Login 入参：mode 区分前台(home)/后台(admin)登录；onLogin 登录成功后回调上层挂载工作台 */
interface Props {
  mode: 'home' | 'admin'
  onLogin: (u: AuthUser) => void
}

// 注册接管阶段：form 传统表单 → flashing 闪烁三次 → exiting 整页退出 → ai 由 AI 助理接管
type RegPhase = 'form' | 'flashing' | 'exiting' | 'ai'

/**
 * 登录 / 自助注册 / 忘记密码 三屏合一页（等价 Vue 版 Login.vue）。
 * 2026-09-18 改造：整层皮肤从 TDesign 迁到 LangCross 组件库（AuthCard/Field/Input/useToast），
 * 鉴权链路（登录、注册、邮箱码、找回密码、首登强制改密、Turnstile、UTM 归因）原样保留。
 * 三屏各按 view 提前 return 渲染各自的卡，不复用同一张：设计稿里三屏的卡宽、标题字号、
 * 底部链接行数都不同，共用一张只会把样式分支写成嵌套三元。
 */
export default function Login({ mode, onLogin }: Props) {
  const [lang, , tplF] = useT() // tplF：带 {n} 占位的模板取词（验证码倒计时「{n}s 后重发」用）
  const { toast } = useToast() // 迁移口径：原 TDesign MessagePlugin.warning/error 全部换成组件库 toast
  const branding = useBranding()
  // 邀请裂变个人码（?ref=），登录页与注册提交共用
  // 提到组件顶层而非在提交前现取：注册表单、传统注册提交、AI 接管流三处都要读；
  // 认证页存活期间地址栏 query 不变，故不必 useMemo
  const ref = new URLSearchParams(window.location.search).get('ref') || ''
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPwd, setShowPwd] = useState(false) // 密码明文/密文切换（画布 57:8 的眼睛按钮）
  // 只留 setter 不留值：迁移后 AuthCard 的通栏提交按钮不暴露 loading 属性，loading 不再参与渲染；
  // 保留 setLoading 是为与各异步处理函数「前置位 / finally 复位」的原有时序完全一致
  const [, setLoading] = useState(false)
  // 登录 / 注册 / 找回 三类错误提示
  const [error, setError] = useState('')
  const [regMsg, setRegMsg] = useState('')
  // 首登强制改密弹窗
  const [forceOpen, setForceOpen] = useState(false)
  const [forcePending, setForcePending] = useState<AuthUser | null>(null)
  const [forceOld, setForceOld] = useState('')
  const [forceNew, setForceNew] = useState('')
  const [forceConfirm, setForceConfirm] = useState('')
  const [forceMsg, setForceMsg] = useState('')
  const [forceBusy, setForceBusy] = useState(false)
  // 视图与注册接管阶段
  const [view, setView] = useState<'signin' | 'register' | 'forgot'>('signin')
  const [regPhase, setRegPhase] = useState<RegPhase>('form')
  const [typeChoice, setTypeChoice] = useState<'personal' | 'enterprise'>('personal')
  // 企业用户角色：admin=我是管理员（新建企业）；member=我是普通成员（须凭企业邀请码加入）
  const [roleChoice, setRoleChoice] = useState<'admin' | 'member'>('admin')
  // 注册表单聚合到一个对象：字段随「个人/企业 × 管理员/成员」分支增减，散成 9 个 useState 只会让重置更难维护
  // orgEn（组织英文名）为本次设计稿新增字段，后端暂无独立列，只在表单里占位不提交
  const [form, setForm] = useState({ code: '', name: '', invite: '', email: '', emailCode: '', industry: '', jobRole: '', brandName: '', brandNameEn: '', orgEn: '' })
  const [agreed, setAgreed] = useState(false)
  // 邮箱验证 / 人机验证两个开关由 registerConfig 下发（后台可关），不能前端写死
  const [emailVerifyOn, setEmailVerifyOn] = useState(false)
  const [captchaOn, setCaptchaOn] = useState(false)
  // 2026-09-10 行业字典动态化：超管在后台「行业管理」维护，注册下拉优先用后端返回值，空则回落本地词库
  const [industries, setIndustries] = useState<Array<{ code: string; name: string }>>([])
  // 2026-09-19 职业角色字典：与行业同口径由 registerConfig 下发（租户0 persona 包），
  // 角色只绑用户不绑企业——个人/企业所有分支都展示同一下拉
  const [personas, setPersonas] = useState<Array<{ code: string; name: string }>>(PERSONA_FALLBACK)
  // ★ #42：验证码冷却改走 useCountdown（旧写法 setInterval 的句柄只活在闭包里：
  //   离开登录页时定时器不会被清，且二次触发会并存两个递减源把 60s 冷却打成 30s）
  const codeCd = useCountdown(60)
  const captchaBoxRef = useRef<HTMLDivElement>(null) // Turnstile 挂载容器（脚本会往里塞 iframe）
  const captchaTokenRef = useRef('') // token 用 ref 而非 state：回调写入时不需要触发重渲染
  // 忘记密码
  const [forgotMsg, setForgotMsg] = useState('')
  const [forgotSent, setForgotSent] = useState(false) // 已发码 → 同一张卡切换成「填码 + 新密码」形态
  const [forgot, setForgot] = useState({ username: '', email: '', code: '', newPassword: '' })
  // ★ #38（2026-09-21）：已启用的 IdP 列表；空数组=后端未接 SSO，登录卡不渲染第三方登录区块
  const [ssoList, setSsoList] = useState<SSOProvider[]>([])

  // 拉取 SSO 身份源：仅前台登录卡展示（回调固定落 SSO_FRONTEND_URL 根路径，后台卡跳过去会回不到 /admin）
  useEffect(() => {
    if (mode !== 'home') return
    // 查询失败（网络抖动 / 老后端无该端点）按「未启用 SSO」处理：登录主链路不能被辅助入口的可用性拖住
    void ssoProviders().then((r) => { if (r?.enabled) setSsoList(r.providers || []) }).catch(() => { /* 静默 */ })
  }, [mode])

  // 进入 /register 或带 ?ref= 自动展开注册并捕获邀请码
  useEffect(() => {
    try {
      if (mode === 'home' && (window.location.pathname === '/register' || ref)) setView('register')
      // ref（好友裂变个人码）刻意不写进 invite 输入框：invite 是「企业邀请码」，两者语义不同，
      // 混填会让用户以为已经带了好友码（实际由 doRegister 的 ref 字段单独上报）
      if (ref) setForm((f) => ({ ...f, invite: f.invite || '' }))
    } catch { /* ignore */ }
  }, [mode, ref])

  // 仅前台加载注册配置（邮箱验证 / 人机验证 / 行业字典）
  useEffect(() => {
    if (mode !== 'home') return
    ;(async () => {
      try {
        const c = await registerConfig()
        if (c.success) {
          setEmailVerifyOn(!!c.email_verify_enabled)
          setCaptchaOn(!!(c as unknown as { captcha_enabled?: boolean }).captcha_enabled)
          const key = (c as unknown as { captcha_site_key?: string }).captcha_site_key || ''
          if ((c as unknown as { captcha_enabled?: boolean }).captcha_enabled && key) renderTurnstile(key)
          const inds = (c as unknown as { industries?: Array<{ code: string; name: string }> }).industries
          if (Array.isArray(inds) && inds.length > 0) setIndustries(inds)
          const ps = (c as unknown as { personas?: Array<{ code: string; name: string }> }).personas
          if (Array.isArray(ps) && ps.length > 0) setPersonas(ps)
        }
      } catch { /* ignore */ }
    })()
  }, [mode])

  // 注册屏：进入后传统表单闪烁三次 → 整页 120ms 退出 → AI 接管（尊重 prefers-reduced-motion）
  useEffect(() => {
    if (view !== 'register' || mode !== 'home') return
    // matchMedia 必须带守卫：jsdom / 老旧 WebView 没有 window.matchMedia，裸调会在 effect 阶段直接抛错
    const reduced = typeof window !== 'undefined' && !!window.matchMedia
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    setRegPhase('form')
    if (reduced) return
    // 三段节拍对齐 demo-register-ai-motion.html：
    // 1300ms 让传统表单先站稳（观众得看清「原来要填 9 项」）→ 920ms 闪烁动画（= .auth-flash 时长）
    // → 130ms 覆盖 120ms 整页退出动画后再挂 AI 面板，中间不留「空白闪一下」的缝
    const id1 = window.setTimeout(() => setRegPhase('flashing'), 1300)
    const id2 = window.setTimeout(() => setRegPhase('exiting'), 1300 + 920)
    const id3 = window.setTimeout(() => setRegPhase('ai'), 1300 + 920 + 130)
    return () => { window.clearTimeout(id1); window.clearTimeout(id2); window.clearTimeout(id3) }
  }, [view, mode])

  // ---- Cloudflare Turnstile 人机验证（显式渲染，含 script 注入守卫 w.turnstile / w.__tsLoading）----
  function renderTurnstile(siteKey: string) {
    const mount = () => {
      const el = captchaBoxRef.current
      const ts = (window as unknown as { turnstile?: { render: (el: HTMLElement, o: unknown) => void } }).turnstile
      // 容器已有子节点 = widget 已渲染过，二次 render 会重建并清掉用户刚完成的验证，必须直接返回
      if (!el || !ts || el.childElementCount > 0) return
      ts.render(el, {
        sitekey: siteKey,
        callback: (tk: string) => { captchaTokenRef.current = tk },
        'expired-callback': () => { captchaTokenRef.current = '' }, // 过期即清空，避免拿旧 token 提交被判失败
      })
    }
    const w = window as unknown as { turnstile?: unknown; __tsLoading?: boolean }
    if (w.turnstile) { mount(); return } // 脚本已在（其他页面先加载过 / 同页重进）：跳过注入
    if (w.__tsLoading) return // 注入中：全局只允许一份 script，重复 append 会重复拉包并竞态渲染
    w.__tsLoading = true
    const s = document.createElement('script')
    s.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit' // explicit：不让脚本自动扫描 DOM，只由上面 mount() 手动挂到我们的容器
    s.async = true
    s.onload = mount
    document.head.appendChild(s)
  }

  // ---- 登录：校验 → 调登录接口 → 后台角色校验 → brand_host SSO 跳转 → 写 token/租户 → 首登改密 / 回调 ----
  async function doLogin() {
    if (!username || !password) { setError(t('auth.needInput')); return }
    setLoading(true); setError('')
    try {
      const resp = await login(username, password)
      if (!resp.success || !resp.token) { setError(resp.message || t('login.fail')); return }
      // 后台入口二次把关：roleLevel <2（普通成员）即便拿到 token 也不放行，防止手输 /admin 越界
      if (mode === 'admin' && roleLevel(resp.user?.role) < 2) { setError(t('login.noAdmin')); return }
      // 品牌专属域名跳转：跨子域改带一次性 sso_code，落地后由 App 经 /api/auth/sso/exchange 兑换 JWT
      if (resp.brand_host && resp.sso_code && resp.brand_host !== window.location.host) {
        const target = window.location.protocol + '//' + resp.brand_host + '/?sso_code=' + encodeURIComponent(resp.sso_code)
        window.location.replace(target) // replace 而非 assign：不留历史记录，后退不会再回到跳走前的登录页
        return
      }
      setAuthToken(resp.token)
      setActiveTenantId(0) // 先落在平台默认租户；进后台后由 stores/admin 的 switchTenant 改写 X-Tenant-ID
      const u = resp.user!
      // 首登强制改密（2026-09-02）：管理员批量导入的账号，初始密码可能进过导出文件，
      // 必须先改掉才能进系统 —— 此处只弹窗、不回调 onLogin
      if ((u as unknown as { must_change_pwd?: number }).must_change_pwd) {
        setForcePending(u); setForceOld(''); setForceNew(''); setForceConfirm(''); setForceMsg('')
        setForceOpen(true)
        return
      }
      onLogin(u)
    } catch (e: unknown) {
      // 凭证错误等会以 401 抛出：必须把后端 message 透出，否则用户只看到「点了没反应」
      setError((e as { message?: string }).message || t('login.fail'))
    } finally { setLoading(false) }
  }

  // ---- 首登强制改密：校验新密码长度与一致 → changePassword → 成功 toast 并 onLogin ----
  async function submitForcePwd() {
    if (!forceOld || forceNew.length < 6) { setForceMsg(t('pwd.tooShort')); return }
    if (forceNew !== forceConfirm) { setForceMsg(t('pwd.mismatch')); return }
    setForceBusy(true); setForceMsg('')
    try {
      const r = await changePassword(forceOld, forceNew)
      if (!r.success) { setForceMsg(r.message || t('auth.forcePwdFail')); return }
      toast({ title: t('pwd.done'), tone: 'success' })
      setForceOpen(false)
      if (forcePending) { onLogin(forcePending); setForcePending(null) }
    } catch (e) { toast({ title: (e as Error).message || t('auth.forcePwdFail'), tone: 'error' }) }
    finally { setForceBusy(false) }
  }

  // ---- 发送邮箱验证码：校验邮箱与人机验证 → 调接口 → 60s 倒计时冷却 ----
  async function doSendCode() {
    if (!form.email.trim()) { toast({ title: t('auth.emailRequired'), tone: 'warn' }); return }
    if (captchaOn && !captchaTokenRef.current) { toast({ title: t('auth.captchaRequired'), tone: 'warn' }); return }
    let r!: Awaited<ReturnType<typeof sendEmailCode>>
    try {
      r = await sendEmailCode(form.email.trim(), captchaTokenRef.current || undefined)
    } catch (e) { toast({ title: (e as Error).message || t('auth.sendCodeFail'), tone: 'error' }); return } // E10：网络/超时必须显式提示，早先是 unhandled rejection（用户只见无响应）
    if (r.success) {
      toast({ title: r.noop ? t('pwd.codeNoop') : t('pwd.codeSent'), tone: 'success' })
      codeCd.start() // 冷却 60s（到 0 自动停；组件卸载即停，见 lib/useCountdown.ts）
    } else toast({ title: r.message || t('pwd.sendFail'), tone: 'error' })
  }

  // ---- 传统表单注册（AI 接管前的「前身」形态，仍可手动提交）----
  // 迁移附带修正：这批提示原先是硬编码中文（如「请填写邮箱并输入验证码」），现全量走 auth.* 键，
  // 英文环境不再露出中文；校验按表单自上而下顺序、命中即返回，一次只提示一条。
  async function doRegister() {
    if (!username || !password) { setRegMsg(t('auth.needInput')); return }
    if (password.length < 6) { setRegMsg(t('auth.pwdShort')); return }
    if (!agreed) { setRegMsg(t('auth.needAgree')); return }
    if (!form.email.trim()) { setRegMsg(t('auth.emailRequired')); return }
    if (emailVerifyOn && (!form.emailCode.trim() || !form.email.trim())) { setRegMsg(t('auth.codeRequired')); return }
    if (!branding.dedicatedRegister && typeChoice === 'enterprise' && !form.code.trim()) { setRegMsg(t('auth.orgCodeRequired')); return }
    if (!branding.dedicatedRegister && typeChoice === 'enterprise' && !form.name.trim()) { setRegMsg(t('auth.orgNameRequired')); return }
    if (captchaOn && !captchaTokenRef.current) { setRegMsg(t('auth.captchaRequired')); return }
    const regType = (!branding.dedicatedRegister ? typeChoice : 'enterprise')
    // 个人用户：好友邀请码经 ref 走邀请裂变；企业用户：普通成员须凭企业邀请码加入
    // 专属域名（dedicatedRegister）下只允许企业注册，且邀请码无条件必填
    const entInvite = branding.dedicatedRegister
      ? (form.invite.trim() || undefined)
      : (regType === 'enterprise' && roleChoice === 'member' ? (form.invite.trim() || undefined) : undefined)
    if (branding.dedicatedRegister && !entInvite) { setRegMsg(t('auth.dedicatedInviteRequired')); return }
    if (regType === 'enterprise' && roleChoice === 'member' && !entInvite) { setRegMsg(t('auth.inviteRequired')); return }
    setLoading(true); setRegMsg('')
    try {
      const r = await authRegister({
        username, password,
        type: regType,
        code: (regType === 'enterprise' ? (form.code || undefined) : undefined),
        name: (regType === 'enterprise' ? (form.name || undefined) : undefined),
        invite: entInvite,
        role_choice: (regType === 'enterprise' ? roleChoice : undefined),
        email: form.email.trim() || undefined,
        email_code: form.emailCode || undefined,
        captcha_token: captchaTokenRef.current || undefined,
        industry: (regType === 'enterprise' ? (form.industry ? industryCodeOf(form.industry) : undefined) : undefined),
        // job_role 不带分支条件：角色绑用户不绑企业，个人/企业管理员/成员注册时都生效
        job_role: form.jobRole || undefined,
        brand_name: (regType === 'enterprise' && roleChoice === 'admin' ? (form.brandName.trim() || undefined) : undefined),
        brand_name_en: (regType === 'enterprise' && roleChoice === 'admin' ? (form.brandNameEn.trim() || undefined) : undefined),
        ref,
        agreed,
        landing_path: window.location.pathname, ...readUtm(), // S4 归因：落地页捕获的 UTM 随注册一次性上报
      })
      if (!r.success) { setRegMsg(r.message || t('register.fail')); return }
      clearUtm() // 归因只在首次注册消费，用完立即清掉，避免二次注册串号
      await doLogin() // 注册成功自动登录（行为同 Vue 版）
    } catch (e) { setRegMsg(e instanceof Error ? e.message : String(t('register.fail'))) } // E10：异常同样要落到卡内红字
    finally { setLoading(false) }
  }

  // ---- 忘记密码：发验证码 → 校验验证码重置新密码 ----
  // 提交找回：用户名或绑定邮箱二者其一即可（后端按绑定关系发码，不要求两个都填）
  async function doForgotSend() {
    if (!forgot.username.trim() && !forgot.email.trim()) { setForgotMsg(t('auth.forgotNeed')); return }
    setLoading(true); setForgotMsg('')
    try {
      const resp = await forgotPassword({ username: forgot.username.trim() || undefined, email: forgot.email.trim() || undefined })
      if (!resp.success) { setForgotMsg(resp.message || t('auth.sendFail')); return }
      setForgotSent(true)
      setForgotMsg(t('auth.forgotSent'))
    } catch (e) { setForgotMsg((e as Error).message || t('auth.sendFail')) }
    finally { setLoading(false) }
  }

  // 用验证码重置：成功后把用户名/新密码回填到登录表单再切回登录屏，
  // 用户不需要重新输入一遍（error 位显示「密码已重置」当作正向反馈复用同一条提示槽）
  async function doReset() {
    if (!forgot.code.trim() || forgot.newPassword.length < 6) { setForgotMsg(t('auth.resetNeed')); return }
    setLoading(true); setForgotMsg('')
    try {
      const resp = await resetPassword({ username: forgot.username.trim(), code: forgot.code.trim(), new_password: forgot.newPassword })
      if (!resp.success) { setForgotMsg(resp.message || t('auth.resetFail')); return }
      setUsername(forgot.username.trim())
      setPassword(forgot.newPassword)
      closeForgot()
      setError(t('auth.successReset'))
    } catch (e) { setForgotMsg((e as Error).message || t('auth.resetFail')) }
    finally { setLoading(false) }
  }

  // 关闭找回屏：视图与整张找回表单一起重置 —— 否则下次进来还会看到上一次的验证码/新密码残值
  function closeForgot() {
    setView('signin')
    setForgotSent(false)
    setForgot({ username: '', email: '', code: '', newPassword: '' })
  }

  // ====================== 渲染 ======================
  // 登录卡标题渲染品牌名而不是「登录」（画布 57:4 实测；Login.auth.dom.test ① 守这条）
  const brandName = branding.brandName || DEFAULT_BRAND_NAME

  // 卡右上角语言入口（★ #23：原 zh/en 胶囊换成 12 语种 LangSelect，三个认证屏共用）
  const langPill = <LangSelect align="right" />

  // 登录屏（卡宽 380、标题=品牌名、底部两行居中链接、密码框右端眼睛、卡右上角 EN 胶囊）
  if (view === 'signin') {
    return (
      <div className="lc-auth-bg">
        <AuthCard
          title={brandName}
          desc={t('auth.loginDesc')}
          submitText={t('auth.signIn')}
          onSubmit={doLogin}
          corner={langPill}
          foot={(
            <>
              <button type="button" className="auth-link" onClick={() => setView('forgot')}>{t('auth.forgot')}</button>
              {/* login.selfRegister 已是「没有账号？自助注册试用」整句（中英齐备），不再用 auth.noAccount + auth.freeRegister 拼 */}
              <button type="button" className="auth-link" onClick={() => setView('register')}>{t('login.selfRegister')}</button>
            </>
          )}
        >
          {/* 设计稿登录卡内只有 placeholder、没有字段标签；可访问名由 aria-label 承担 */}
          <Field>
            <Input aria-label={t('auth.username')} value={username} onChange={(e) => setUsername(e.target.value)} placeholder={t('auth.username')} autoComplete="username" />
          </Field>
          {/* 密码框：AuthCard 的提交键是普通 button（没有 form），迁移前 TDesign Input 自带的
              onEnter 提交能力必须在这里手动接回来，否则密码框回车不再登录 */}
          <Field>
            <Input
              aria-label={t('auth.password')}
              type={showPwd ? 'text' : 'password'}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={t('auth.password')}
              autoComplete="current-password"
              onKeyDown={(e) => { if (e.key === 'Enter') doLogin() }}
              trailing={(
                // aria-label 用现成的 showPwd/hidePwd 两键：原写法引用了词典里不存在的 auth.togglePwd，
                // 屏幕阅读器会念出键名字符串（★ #38 顺带修，属 a11y 缺陷）
                <button type="button" className="lc-input-affix auth-pwd-eye" aria-pressed={showPwd} onClick={() => setShowPwd((v) => !v)} aria-label={showPwd ? t('auth.hidePwd') : t('auth.showPwd')}>
                  {showPwd ? <EyeOffIcon /> : <EyeIcon />}
                </button>
              )}
            />
          </Field>
          {!!error && <div className="auth-err">{error}</div>}
          {/* ★ #38：第三方身份源登录入口。用 <a href> 而非按钮 + location.assign——
              /api/sso/login 回 302 到 IdP 且要下发 state cookie，fetch 会把整条重定向链吃掉；
              原生链接还顺带保住中键新开、复制链接这些浏览器默认能力。 */}
          {ssoList.length > 0 && (
            <div className="auth-sso">
              <div className="auth-sso-sep"><span>{t('auth.orSso')}</span></div>
              {ssoList.map((p) => (
                <a key={p.name} className="lc-btn lc-btn--secondary" href={ssoLoginUrl(p.name)}>
                  {tplF('auth.loginWith', { name: p.display_name || p.name })}
                </a>
              ))}
            </div>
          )}
        </AuthCard>
        <ForcePwdDialog open={forceOpen} msg={forceMsg} busy={forceBusy} old={forceOld} newP={forceNew} confirm={forceConfirm}
          onOld={setForceOld} onNew={setForceNew} onConfirmPwd={setForceConfirm} onSubmit={submitForcePwd}
          onCancel={() => { if (!forceBusy) setForceOpen(false) }} />
        <style>{CSS_AUTH}</style>
      </div>
    )
  }

  // 忘记密码屏（标题 20/Bold、两个输入框只带 placeholder、底部仅一行居中链接）
  if (view === 'forgot') {
    return (
      <div className="lc-auth-bg">
        {/* 同一张卡承载两步：未发码=提交找回申请，已发码=提交重置（submitText 与 onSubmit 一起切），
            不做两个页面是为了让用户停在同一个视觉位置上读验证码邮件 */}
        <AuthCard
          className="lc-auth-card--compact"
          title={t('auth.forgotTitle')}
          desc={t('auth.forgotDesc')}
          submitText={forgotSent ? t('auth.resetPassword') : t('auth.sendCode')}
          onSubmit={forgotSent ? doReset : doForgotSend}
          corner={langPill}
        >
          <Field>
            <Input aria-label={t('auth.username')} value={forgot.username} onChange={(e) => setForgot({ ...forgot, username: e.target.value })} placeholder={t('auth.username')} />
          </Field>
          <Field>
            <Input aria-label={t('auth.boundEmail')} value={forgot.email} onChange={(e) => setForgot({ ...forgot, email: e.target.value })} placeholder={t('auth.boundEmail')} />
          </Field>
          {!!forgotMsg && <div className={forgotSent ? 'auth-ok' : 'auth-err'}>{forgotMsg}</div>}
          {forgotSent && (
            <>
              <Field>
                <Input aria-label={t('auth.fieldEmailCode')} value={forgot.code} onChange={(e) => setForgot({ ...forgot, code: e.target.value })} placeholder={t('auth.fieldEmailCode')} />
              </Field>
              <Field>
                <Input aria-label={t('auth.newPassword')} type="password" value={forgot.newPassword} onChange={(e) => setForgot({ ...forgot, newPassword: e.target.value })} placeholder={t('auth.newPassword')} />
              </Field>
            </>
          )}
        </AuthCard>
        <ForcePwdDialog open={forceOpen} msg={forceMsg} busy={forceBusy} old={forceOld} newP={forceNew} confirm={forceConfirm}
          onOld={setForceOld} onNew={setForceNew} onConfirmPwd={setForceConfirm} onSubmit={submitForcePwd}
          onCancel={() => { if (!forceBusy) setForceOpen(false) }} />
        <style>{CSS_AUTH}</style>
      </div>
    )
  }

  // 注册屏：regPhase==='ai' 由 AI 助理面板接管；否则显示传统表单卡（可手动提交）
  if (regPhase === 'ai') {
    return (
      <div className="lc-auth-bg">
        {/* prefillUsername：已输入的用户名带进 AI 流程（对应话术「用户名我帮你带过来了」），
            是否可编辑由面板自己决定。onClose 一律退回传统表单 —— AI 接管不是单行道，
            用户随时有权自己把 9 项填完 */}
        <AiRegisterFlow
          prefillUsername={username}
          dedicatedRegister={branding.dedicatedRegister}
          onDone={onLogin}
          onClose={() => setRegPhase('form')}
        />
        <style>{CSS_AUTH}</style>
      </div>
    )
  }

  // 说明行随「个人 / 企业」切换整句文案（两张卡的必填项不同，不拼句只做二选一）
  const desc = typeChoice === 'personal' ? t('auth.personalDesc') : t('auth.enterpriseDesc')
  return (
    <div className="lc-auth-bg">
      <AuthCard
        className={regPhase === 'flashing' ? 'auth-flash' : regPhase === 'exiting' ? 'auth-pg-out' : ''}
        title={t('auth.registerTitle')}
        desc={desc}
        submitText={t('auth.registerAndLogin')}
        onSubmit={doRegister}
        corner={langPill}
      >
           {branding.dedicatedRegister ? (
          <Field label={t('auth.orgInvite')}>
            <Input value={form.invite} onChange={(e) => setForm({ ...form, invite: e.target.value })} placeholder={t('auth.orgInvite')} />
          </Field>
        ) : (
          <>
            {/* 个人 / 企业 分段 */}
            <div className="auth-seg-row">
              <button type="button" className={'auth-seg' + (typeChoice === 'personal' ? ' auth-seg--on' : '')} onClick={() => setTypeChoice('personal')}>{t('auth.tabPersonal')}</button>
              <button type="button" className={'auth-seg' + (typeChoice === 'enterprise' ? ' auth-seg--on' : '')} onClick={() => setTypeChoice('enterprise')}>{t('auth.tabEnterprise')}</button>
            </div>
            {/* 企业用户：管理员（建企业）/ 成员（凭邀请码加入）角色 */}
            {typeChoice === 'enterprise' && (
              <div className="auth-seg-row">
                <button type="button" className={'auth-seg' + (roleChoice === 'admin' ? ' auth-seg--on' : '')} onClick={() => setRoleChoice('admin')}>{t('auth.roleAdmin')}</button>
                <button type="button" className={'auth-seg' + (roleChoice === 'member' ? ' auth-seg--on' : '')} onClick={() => setRoleChoice('member')}>{t('auth.roleStaff')}</button>
              </div>
            )}
          </>
        )}

        {/* 企业成员 / 管理员 各自的字段（邀请码、组织名、行业、品牌译名）*/}
        {typeChoice === 'enterprise' && !branding.dedicatedRegister && (
          <>
                {roleChoice === 'member' && (
              <Field label={t('auth.orgCode')}>
                <Input value={form.invite} onChange={(e) => setForm({ ...form, invite: e.target.value })} placeholder={t('auth.orgCodeStaffPlaceholder')} />
              </Field>
            )}
                {roleChoice === 'admin' && (
              <>
                <Field label={t('auth.orgCode')}><Input value={form.code} onChange={(e) => setForm({ ...form, code: e.target.value })} placeholder={t('auth.orgCode')} /></Field>
                <Field label={t('auth.orgNameCn')}><Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder={t('auth.orgNameCn')} /></Field>
                <Field label={t('auth.orgNameEn')}><Input value={form.orgEn} onChange={(e) => setForm({ ...form, orgEn: e.target.value })} placeholder={t('auth.orgNameEn')} /></Field>
                <Field label={t('auth.industry')}>
                  <select className="lc-input" value={form.industry} onChange={(e) => setForm({ ...form, industry: e.target.value })}>
                    <option value="">{t('auth.selectIndustry')}</option>
                    {(industries.length > 0 ? industries.map((x) => ({ value: x.code, label: x.name })) : industryOptions(lang)).map((o) => (
                      <option key={o.value} value={o.value}>{o.label}</option>
                    ))}
                  </select>
                </Field>
              </>
            )}
          </>
        )}

        <Field label={t('auth.fieldUsername')}>
          <Input value={username} onChange={(e) => setUsername(e.target.value)} placeholder={t('auth.fieldUsername')} />
        </Field>
        <Field label={t('auth.fieldPassword')}>
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder={t('auth.fieldPassword')} />
        </Field>
        <Field label={t('auth.fieldEmail')}>
          <Input value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder={t('auth.fieldEmail')} />
        </Field>
        {/* 职业角色（2026-09-19）：个人/企业所有分支共用——角色只绑用户不绑企业，
            选填（后端对未知/空 code 静默忽略），注册后可在账号菜单随时改（转岗） */}
        <Field label={t('auth.jobRole')}>
          <select className="lc-input" value={form.jobRole} onChange={(e) => setForm({ ...form, jobRole: e.target.value })}>
            <option value="">{t('auth.selectJobRole')}</option>
            {personas.map((x) => (
              <option key={x.code} value={x.code}>{x.name}</option>
            ))}
          </select>
        </Field>
        {/* 邮箱验证码：只在后台开了邮箱验证（emailVerifyOn）时出现；发码按钮进 60s 冷却防刷 */}
            {emailVerifyOn && (
          <Field label={t('auth.fieldEmailCode')}>
            <div style={{ display: 'flex', gap: 8 }}>
              <Input style={{ flex: 1 }} value={form.emailCode} onChange={(e) => setForm({ ...form, emailCode: e.target.value })} placeholder={t('auth.fieldEmailCode')} />
              <Button variant="secondary" disabled={codeCd.left > 0} onClick={doSendCode}>
                {codeCd.left > 0 ? tplF('auth.codeResend', { n: codeCd.left }) : t('auth.sendCode')}
              </Button>
            </div>
          </Field>
        )}

        {/* 个人用户：好友邀请码（选填）*/}
        {typeChoice === 'personal' && !branding.dedicatedRegister && (
          <Field label={t('auth.friendInvite')}>
            <Input value={form.invite} onChange={(e) => setForm({ ...form, invite: e.target.value })} placeholder={t('auth.friendInvite')} />
          </Field>
        )}

        {/* 企业管理员：品牌固定译名（防翻译漂移）*/}
        {typeChoice === 'enterprise' && !branding.dedicatedRegister && roleChoice === 'admin' && (
          <>
            <Field label={t('auth.brandNameCn')}><Input value={form.brandName} onChange={(e) => setForm({ ...form, brandName: e.target.value })} placeholder={t('auth.brandNameCn')} /></Field>
            <Field label={t('auth.brandNameEn')}><Input value={form.brandNameEn} onChange={(e) => setForm({ ...form, brandNameEn: e.target.value })} placeholder={t('auth.brandNameEn')} /></Field>
          </>
        )}

        {/* Turnstile 挂载点：容器自己不留任何文案，开启人机验证时由 renderTurnstile 往里塞 widget */}
            <div ref={captchaBoxRef} id="__ts_widget__" />
        {/* 走 label 属性而不是 children：Checkbox 内部是 <input>（void 元素），children 若被摊进去会直接抛错 */}
        <Checkbox
          checked={agreed}
          onChange={(e) => setAgreed(!!e.target.checked)}
          label={(
            <>
              {t('auth.agreeTerms')}{''}
              <a href="/docs/terms" target="_blank" rel="noreferrer" className="auth-link">{t('auth.userAgreement')}</a>
              {' & '}
              <a href="/docs/privacy" target="_blank" rel="noreferrer" className="auth-link">{t('auth.privacyPolicy')}</a>
            </>
          )}
        />
        {!!regMsg && <div className="auth-err">{regMsg}</div>}
      </AuthCard>
      <ForcePwdDialog open={forceOpen} msg={forceMsg} busy={forceBusy} old={forceOld} newP={forceNew} confirm={forceConfirm}
        onOld={setForceOld} onNew={setForceNew} onConfirmPwd={setForceConfirm} onSubmit={submitForcePwd}
        onCancel={() => { if (!forceBusy) setForceOpen(false) }} />
      <style>{CSS_AUTH}</style>
    </div>
  )
}

// 首登强制改密弹窗（保留原逻辑，langcross Dialog 实现）
// 三个认证屏共用同一个实例（每屏末尾各挂一处），props 全部由上层受控：
// 这里不持有任何状态，改密成功/失败的反馈由 submitForcePwd 写回 forceMsg + toast。
// busy 期间禁遮罩关闭（dismissOnOverlay）：请求进行中关掉弹窗会让人以为已经改完了。
function ForcePwdDialog(props: {
  open: boolean; msg: string; busy: boolean
  old: string; newP: string; confirm: string
  onOld: (v: string) => void; onNew: (v: string) => void; onConfirmPwd: (v: string) => void
  onSubmit: () => void; onCancel: () => void
}) {
  return (
    <Dialog open={props.open} title={t('pwd.forceTitle')} confirmText={t('pwd.forceSubmit')}
            onConfirm={props.onSubmit} onCancel={props.onCancel} dismissOnOverlay={!props.busy}>
      <p style={{ fontSize: 15, color: 'var(--lc-text-3)', lineHeight: 1.7, margin: '0 0 14px' }}>{t('pwd.forceHint')}</p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <Field label={t('pwd.oldPassword')}><Input type="password" value={props.old} onChange={(e) => props.onOld(e.target.value)} /></Field>
        {/* 新密码/确认框标 new-password：否则浏览器密码管理器会把这次改密当成登录，
            自动回填初始密码，用户「改了」却没改 */}
        <Field label={t('auth.newPassword')}><Input type="password" autoComplete="new-password" value={props.newP} onChange={(e) => props.onNew(e.target.value)} /></Field>
        <Field label={t('pwd.confirmPlaceholder')}><Input type="password" autoComplete="new-password" value={props.confirm} onChange={(e) => props.onConfirmPwd(e.target.value)} /></Field>
        {!!props.msg && <div className="auth-err">{props.msg}</div>}
      </div>
    </Dialog>
  )
}

// ---------- 作用域样式（auth- 前缀，避免与组件库类名重名）----------
const CSS_AUTH = `
.lc-auth-bg{position:fixed;inset:0;display:flex;align-items:center;justify-content:center;background:#000;font-family:var(--lc-font);padding:24px;box-sizing:border-box;}
.lc-auth-card--compact{max-width:360px;}
.auth-link{background:none;border:0;color:var(--lc-text-3);font-size:14px;cursor:pointer;font-family:var(--lc-font);padding:0;}
.auth-link:hover{color:var(--lc-text);text-decoration:underline;}
/* 卡右上角语言胶囊（31×19、面 #16181C、纯白、无描边、圆角全圆） */
.auth-lang{height:19px;padding:0 8px;border:0;border-radius:999px;background:var(--lc-raised);color:#FFFFFF;font-family:var(--lc-font-latin);font-size:13px;font-weight:500;line-height:1;cursor:pointer;}
.auth-lang:hover{box-shadow:inset 0 0 0 2px var(--lc-border-pill);}
.auth-lang:focus-visible{outline: 2px solid var(--lc-border-input);outline-offset:2px;}
/* 错误 / 中性提示各占一行：找回发码成功用 .auth-ok（次级文字色），
   纯黑体系里不引入成功绿，避免一处绿把整屏配色基调带偏 */
.auth-err{color:var(--lc-danger);font-size:15px;line-height:1.6;}
.auth-ok{color:var(--lc-text-2);font-size:15px;line-height:1.6;}
/* ★ #38 第三方登录区：分隔线用一条 1.2px 细线 + 居中文字（纯黑体系不引入品牌色块），
   按钮等宽纵排，避免两个 IdP 时长短不齐看着像残排 */
.auth-sso{display:flex;flex-direction:column;gap:8px;margin-top:4px;}
.auth-sso-sep{display:flex;align-items:center;gap:8px;color:var(--lc-text-3);font-size:14px;}
.auth-sso-sep::before,.auth-sso-sep::after{content:"";flex:1;height:1px;background:var(--lc-border-card,#3A404C);}
.auth-sso .lc-btn{width:100%;justify-content:center;text-decoration:none;}
/* 分段选择器（个人/企业、管理员/成员）：画布是两枚等宽胶囊，故用按钮组而不是 Radio */
.auth-seg-row{display:flex;gap:8px;}
.auth-seg{flex:1;height:36px;border-radius:var(--lc-r-ctl);border:2px solid var(--lc-border-pill);background:transparent;color:var(--lc-text-2);font-size:15px;font-weight:500;cursor:pointer;font-family:var(--lc-font);}
.auth-seg--on{background:var(--lc-raised);color:var(--lc-text);border-color:var(--lc-border-done);font-weight:600;}
.auth-pwd-eye{display:flex;align-items:center;justify-content:center;background:none;border:0;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0;}
.auth-pwd-eye:hover{color:var(--lc-text);}
/* 接管前「传统表单闪三下」：三拍落在 8%/16%、38%/46%、72%/82%，
   steps(1,end) 让亮度是跳变而非被插值成糊的一团（时长 920ms 与 Login 里的节拍对齐） */
.auth-flash{animation:auth-flash .92s steps(1,end) forwards;}
@keyframes auth-flash{0%{filter:brightness(1)}8%{filter:brightness(1.8)}16%{filter:brightness(1)}38%{filter:brightness(1.62)}46%{filter:brightness(1)}72%{filter:brightness(1.45)}82%,100%{filter:brightness(1)}}
/* 整页退场：120ms 淡出 + 上移 6px，紧接 AI 面板挂上（130ms），中间不留空帧 */
.auth-pg-out{animation:auth-pg-out .12s ease forwards;pointer-events:none;}
@keyframes auth-pg-out{to{opacity:0;transform:translateY(-6px);}}
/* 偏好减少动效：动画一律不播；Login 的接管节拍同样会提前 return，用户停在传统表单自己填 */
@media (prefers-reduced-motion: reduce){
  .auth-flash{animation:none!important;}
  .auth-pg-out{animation:none!important;transition:none!important;}
}
`
