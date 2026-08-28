// ============================================================================
// components/Login.tsx — 登录 / 自助注册（TDesign 实现）
// 行为对齐：home/admin 双模式；注册角色双选（管理员建企业/邀请码加入）；邮箱验证码
// 显隐（registerConfig）；Turnstile 挂载点（captchaOn 时预留容器，脚本注入同 Vue 版）；
// ★ U4：/register 路径或 ?ref= 自动展开注册面板并捕获个人码。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { Button, Input, Select, Checkbox, MessagePlugin } from 'tdesign-react'
import {
  login, authRegister, registerIndustries, sendEmailCode, registerConfig,
  forgotPassword, resetPassword,
  setAuthToken, setActiveTenantId,
} from '@/api'
import { t, tpl, useT, toggleLang } from '@/i18n'
import { roleLevel } from '@/stores/auth'
import { useBranding, DEFAULT_BRAND_NAME, BrandBgLayer, parseLoginLayout, parseCardPos } from '@/branding'
import type { AuthUser } from '@/api'

// ============ 本文件职责中文说明 ============
// 登录 / 自助注册页面：前台与后台双模式、角色选择、邮箱验证码与找回密码。
// ========================================

// Login 组件入参：mode 区分前台(home)/后台(admin)登录；onLogin 登录成功后回调上层
interface Props {
  mode: 'home' | 'admin'
  onLogin: (u: AuthUser) => void
}

// 默认导出组件：登录 / 自助注册 / 忘记密码 一站式页面（等价 Vue Login.vue）
export default function Login({ mode, onLogin }: Props) {
  const [, , tplF] = useT()
  const branding = useBranding()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  // 注册面板状态
  const [view, setView] = useState<'signin' | 'register' | 'forgot'>('signin')
  const [regMsg, setRegMsg] = useState('')
  const [agreed, setAgreed] = useState(false)
  const [typeChoice, setTypeChoice] = useState<'personal' | 'enterprise'>('personal')
  // 企业用户角色选择：admin=我是管理员（新建企业）；member=我是普通成员（须凭企业邀请码加入）
  const [roleChoice, setRoleChoice] = useState<'admin' | 'member'>('admin')
  const [form, setForm] = useState({ code: '', name: '', invite: '', email: '', emailCode: '', industry: '' })
  const [industries, setIndustries] = useState<Array<{ code: string; name: string }>>([])
  const [emailVerifyOn, setEmailVerifyOn] = useState(false)
  const [captchaOn, setCaptchaOn] = useState(false)
  const [codeCooldown, setCooldown] = useState(0)
  const captchaBoxRef = useRef<HTMLDivElement>(null)
  const captchaTokenRef = useRef('')

  // 忘记密码流程

  const [forgotMsg, setForgotMsg] = useState('')
  const [forgotSent, setForgotSent] = useState(false)
  const [forgot, setForgot] = useState({ username: '', email: '', code: '', newPassword: '' })

  // ★ U4：进入登录页时，若路径为 /register 或携带 ?ref= 邀请码，则自动展开注册面板并捕获个人裂变码
  useEffect(() => {
    try {
      const p = new URLSearchParams(window.location.search)
      const ref = p.get('ref')?.trim() || ''
      if (mode === 'home' && (window.location.pathname === '/register' || ref)) setView('register')
      if (ref) setForm((f) => ({ ...f, invite: f.invite || '' })) // 企业邀请码与个人 ref 相互独立
    } catch { /* ignore */ }
  }, [mode])

  // 仅前台(home)加载注册配置与行业列表：邮箱验证开关、人机验证开关、Turnstile 站点配置
  useEffect(() => {
    if (mode !== 'home') return
    ;(async () => {
      try {
        const r = await registerIndustries()
        if (r.success) setIndustries(((r as unknown as { industries?: Array<{ code: string; name: string }> }).industries) || [])
      } catch { /* ignore */ }
      try {
        const c = await registerConfig()
        if (c.success) {
          setEmailVerifyOn(!!c.email_verify_enabled)
          setCaptchaOn(!!(c as unknown as { captcha_enabled?: boolean }).captcha_enabled)
          const key = (c as unknown as { captcha_site_key?: string }).captcha_site_key || ''
          if ((c as unknown as { captcha_enabled?: boolean }).captcha_enabled && key) renderTurnstile(key)
        }
      } catch { /* ignore */ }
    })()
  }, [mode])

  // 动态挂载 Cloudflare Turnstile 人机验证组件（已存在则跳过，避免重复渲染）
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

  // 执行登录：校验非空 → 调用登录接口 → 校验后台角色权限 → 写入 token/租户并回调
  async function doLogin() {
    if (!username || !password) { setError(t('login.needInput')); return }
    setLoading(true); setError('')
    try {
      const resp = await login(username, password)
      if (!resp.success || !resp.token) { setError(resp.message || t('login.fail')); return }
      if (mode === 'admin' && roleLevel(resp.user?.role) < 2) { setError(t('login.noAdmin')); return }
      // 品牌专属域名跳转（需求 1-B）：若后端返回 brand_host 且与当前域不一致，带 token 重定向过去
      if (resp.brand_host && resp.brand_host !== window.location.host) {
        const target = window.location.protocol + '//' + resp.brand_host + '/?token=' + encodeURIComponent(resp.token)
        window.location.replace(target)
        return
      }
      setAuthToken(resp.token)
      setActiveTenantId(0)
      onLogin(resp.user!)
    } finally { setLoading(false) }
  }

  // 发送邮箱验证码：校验邮箱与人机验证 → 调用接口 → 成功后进入 60s 倒计时冷却
  async function doSendCode() {
    if (!form.email.trim()) { void MessagePlugin.warning(t('login.emailPlaceholder')); return }
    if (captchaOn && !captchaTokenRef.current) { void MessagePlugin.warning('请先完成人机验证'); return }
    const r = await sendEmailCode(form.email.trim(), captchaTokenRef.current || undefined)
    if (r.success) {
      void MessagePlugin.success(r.noop ? t('pwd.codeNoop') : t('pwd.codeSent'))
      setCooldown(60)
      const iv = window.setInterval(() => setCooldown((c) => { if (c <= 1) { window.clearInterval(iv); return 0 } return c - 1 }), 1000)
    } else void MessagePlugin.error(r.message || t('pwd.sendFail'))
  }

  // 执行自助注册：逐项校验表单（密码长度、企业信息、邮箱、人机验证）→ 调用注册接口 → 成功后自动登录
  async function doRegister() {
    if (!username || !password) { setRegMsg(t('login.needInput')); return }
    if (password.length < 6) { setRegMsg(t('login.pwdShort')); return }
    if (!agreed) { setRegMsg(t('login.needAgree')); return }
    if (!form.email.trim()) { setRegMsg(t('login.emailPlaceholder')); return }
    if (emailVerifyOn && (!form.emailCode.trim() || !form.email.trim())) { setRegMsg('请填写邮箱并输入验证码'); return }
    if (!branding.dedicatedRegister && typeChoice === 'enterprise' && !form.code.trim()) { setRegMsg(t('login.orgCodeRequired')); return }
    if (!branding.dedicatedRegister && typeChoice === 'enterprise' && !form.name.trim()) { setRegMsg(t('login.orgNameRequired')); return }
    if (captchaOn && !captchaTokenRef.current) { setRegMsg('请先完成人机验证'); return }
    const urlRef = (() => { try { return new URLSearchParams(window.location.search).get('ref')?.trim() || undefined } catch { return undefined } })()
    const regType = (!branding.dedicatedRegister ? typeChoice : 'enterprise')
    // 个人用户：好友邀请码经 ref 走邀请裂变；企业用户：普通成员须凭企业邀请码加入
    const ref = (regType === 'personal') ? (form.invite.trim() || urlRef || undefined) : undefined
    // 品牌专属域名注册：企业邀请码必填（且须绑定该企业，由后端校验）；普通企业成员亦凭邀请码加入
    const entInvite = branding.dedicatedRegister
      ? (form.invite.trim() || undefined)
      : (regType === 'enterprise' && roleChoice === 'member' ? (form.invite.trim() || undefined) : undefined)
    if (branding.dedicatedRegister && !entInvite) { setRegMsg('请填写企业邀请码后再注册'); return }
    if (regType === 'enterprise' && roleChoice === 'member' && !entInvite) { setRegMsg('普通成员须凭企业邀请码加入，请填写邀请码'); return }
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
        industry: (regType === 'enterprise' ? (form.industry || undefined) : undefined),
        ref,
        agreed,
      })
      if (!r.success) { setRegMsg(r.message || t('register.fail')); return }
      // 注册成功自动登录（行为同 Vue 版）
      await doLogin()
    } finally { setLoading(false) }
  }

  // ===== 忘记密码流程 =====
  // 提交用户名/邮箱发起找回，后端向绑定邮箱发送验证码
  async function doForgot() {
    if (!forgot.username.trim() && !forgot.email.trim()) { setForgotMsg('请输入用户名或绑定邮箱'); return }
    setLoading(true); setForgotMsg('')
    try {
      const resp = await forgotPassword({ username: forgot.username.trim() || undefined, email: forgot.email.trim() || undefined })
      if (!resp.success) { setForgotMsg(resp.message || '发送失败'); return }
      setForgotSent(true)
      setForgotMsg('验证码已发送到绑定邮箱（未配置邮件时请在服务端日志查看）')
    } finally { setLoading(false) }
  }

  // 用验证码重置密码：校验验证码与新密码 → 调用接口 → 回填登录表单并关闭弹窗
  async function doReset() {
    if (!forgot.code.trim() || forgot.newPassword.length < 6) { setForgotMsg('请输入验证码和新密码（至少 6 位）'); return }
    setLoading(true); setForgotMsg('')
    try {
      const resp = await resetPassword({ username: forgot.username.trim(), code: forgot.code.trim(), new_password: forgot.newPassword })
      if (!resp.success) { setForgotMsg(resp.message || '重置失败'); return }
      setUsername(forgot.username.trim())
      setPassword(forgot.newPassword)
      closeForgot()
      setError('密码已重置，请使用新密码登录')
    } finally { setLoading(false) }
  }

  // 关闭忘记密码面板并清空其表单状态
  function closeForgot() {
    setView('signin')
    setForgotSent(false)
    setForgot({ username: '', email: '', code: '', newPassword: '' })
  }

  const layout = parseLoginLayout(branding.brandLoginLayout)
  const cardPos = parseCardPos(branding.brandLoginCardPos)
  const langBtn = (
    <div style={{ position: 'absolute', top: 10, right: 10, zIndex: 3 }}>
      <Button size="small" variant="outline" theme="primary" onClick={toggleLang}>
        {{ zh: 'EN', en: '中文' }[useLangSafe()]}
      </Button>
    </div>
  )
  // 登录/注册/忘记密码 卡片（三种形态按需渲染，统一交由布局容器定位）
  const cards = (
    <>
      {langBtn}
      {view === 'signin' && (
      <div className="login-card">
        <div className="login-logo">
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 108 }} />
            : <span style={{ fontSize: 28, fontWeight: 800 }}>{branding.brandName || branding.tenantName || (mode === 'admin' ? t('login.platformAdmin') : DEFAULT_BRAND_NAME)}</span>}
        </div>
        <div className="login-sub">{mode === 'admin' ? t('login.adminLogin') : t('login.enterWorkspace')}</div>
          <form onSubmit={(e) => e.preventDefault()} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Input value={username} onChange={setUsername} placeholder={t('login.username')}
                   autocomplete="username" onEnter={doLogin} clearable />
            <Input type="password" value={password} onChange={setPassword} placeholder={t('login.password')}
                   autocomplete="current-password" onEnter={doLogin} />
            {!!error && <div style={{ color: '#c62828', fontSize: 13 }}>{error}</div>}
            <Button block loading={loading} onClick={doLogin}>{t('login.signIn')}</Button>
            <Button block variant="text" onClick={() => setView('forgot')}>{t('login.forgot')}</Button>
            {mode === 'home' && (
              <Button block variant="text" onClick={() => setView('register')}>{t('login.selfRegister')}</Button>
            )}
          </form>
      </div>
      )}

      {/* 自助注册卡片 */}
      {view === 'register' && mode === 'home' && (
        <div className="login-card">
           <div className="login-logo">{t('login.selfRegisterTitle')}</div>
           {branding.dedicatedRegister ? (
             <>
               <div style={{ fontSize: 13, color: '#335', background: '#eef4ff', border: '1px solid #c9ddff', borderRadius: 8, padding: '10px 12px', marginBottom: 12, lineHeight: 1.6 }}>
                 {tpl('login.dedicatedInfo', {
                   name: branding.tenantName || branding.brandName || DEFAULT_BRAND_NAME,
                 })}
               </div>
               <Input value={form.invite} onChange={(v: any) => setForm({ ...form, invite: v })} placeholder="请输入企业邀请码（必填）" />
             </>
           ) : (
            <>
              <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
                <Button block variant={typeChoice === 'personal' ? 'base' : 'outline'} theme="primary"
                        onClick={() => setTypeChoice('personal')}>👤 {t('login.personalUser')}</Button>
                <Button block variant={typeChoice === 'enterprise' ? 'base' : 'outline'} theme="primary"
                        onClick={() => setTypeChoice('enterprise')}>🏢 {t('login.enterpriseUser')}</Button>
              </div>
              <div className="login-sub" style={{ marginBottom: 12 }}>
                {typeChoice === 'personal' ? t('login.personalRegisterSub') : t('login.enterpriseRegisterSub')}
              </div>
            </>
          )}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <Input value={username} onChange={setUsername} placeholder={t('login.username')} />
            <form onSubmit={(e) => e.preventDefault()}><Input type="password" value={password} onChange={setPassword} placeholder={t('login.password')} /></form>
            <Input value={form.email} onChange={(v) => setForm({ ...form, email: v })} placeholder={t('login.emailPlaceholder')} />
            {emailVerifyOn && (
              <div style={{ display: 'flex', gap: 8 }}>
                <Input style={{ flex: 1 }} value={form.emailCode} onChange={(v) => setForm({ ...form, emailCode: v })}
                       placeholder={t('login.emailCode')} />
                <Button variant="outline" disabled={codeCooldown > 0} loading={loading} onClick={doSendCode}>
                  {codeCooldown > 0 ? tplF('login.codeResend', { n: codeCooldown }) : t('login.sendCode')}
                </Button>
              </div>
            )}
            {/* 企业用户：区分「管理员 / 员工」角色（需求 7） */}
            {typeChoice === 'enterprise' && !branding.dedicatedRegister ? (
              <>
                <div style={{ display: 'flex', gap: 8 }}>
                  <Button block variant={roleChoice === 'admin' ? 'base' : 'outline'} theme="primary"
                          onClick={() => setRoleChoice('admin')}>管理员（新建企业）</Button>
                  <Button block variant={roleChoice === 'member' ? 'base' : 'outline'} theme="primary"
                          onClick={() => setRoleChoice('member')}>员工（加入企业）</Button>
                </div>
                {/* 员工（普通成员）仅须凭企业邀请码加入；无效/非企业码将被降级为个人用户（需求 2） */}
                {roleChoice === 'member' && (
                  <Input value={form.invite} onChange={(v) => setForm({ ...form, invite: v })}
                         placeholder="请输入企业邀请码" />
                )}
                {/* 管理员（新建企业）需填写企业编码 / 名称 / 行业；员工加入无需这些字段 */}
                {roleChoice === 'admin' && (
                  <>
                    <Input value={form.code} onChange={(v) => setForm({ ...form, code: v })} placeholder={t('login.orgCode')} />
                    <Input value={form.name} onChange={(v) => setForm({ ...form, name: v })} placeholder={t('login.orgName')} />
                    <Select value={form.industry} onChange={(v) => setForm({ ...form, industry: v as string })}
                            placeholder={t('login.selectIndustry')} clearable
                            options={industries.map((i) => ({ label: i.name, value: i.code }))} />
                  </>
                )}
              </>
            ) : null}
            {/* 个人用户：好友邀请码（选填）——邀请人将获得试用 token */}
            {typeChoice === 'personal' && !branding.dedicatedRegister ? (
              <>
                <Input value={form.invite} onChange={(v) => setForm({ ...form, invite: v })}
                       placeholder={t('login.friendInvite')} />
                <div style={{ fontSize: 12, color: '#8893a5', marginTop: -4 }}>{t('login.friendInviteHint')}</div>
              </>
            ) : null}
            <div ref={captchaBoxRef} id="__ts_widget__" />
            <Checkbox checked={agreed} onChange={(c) => setAgreed(!!c)}>
              {t('login.agreeTerms')}{' '}
              <a href="/docs/terms" target="_blank" rel="noreferrer">{t('login.userAgreement')}</a>
              {' & '}
              <a href="/docs/privacy" target="_blank" rel="noreferrer">{t('login.privacyPolicy')}</a>
            </Checkbox>
            {!!regMsg && <div style={{ color: '#c62828', fontSize: 13 }}>{regMsg}</div>}
            <Button block loading={loading} onClick={doRegister}>{t('login.registerAndLogin')}</Button>
            <Button block variant="text" onClick={() => setView('signin')}>{t('login.backToLogin')}</Button>
          </div>
        </div>
      )}

      {/* 忘记密码卡片 */}
      {view === 'forgot' && (
        <div className="login-card">
          <div className="login-logo">{t('login.forgotTitle')}</div>
          <div className="login-sub">{t('login.forgotSub')}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <Input value={forgot.username} onChange={(v) => setForgot((f) => ({ ...f, username: v }))} placeholder={t('login.username')} />
            <Input value={forgot.email} onChange={(v) => setForgot((f) => ({ ...f, email: v }))} placeholder={t('login.boundEmail')} />
            {!!forgotMsg && <div style={{ color: forgotSent ? '#2e7d32' : '#c62828', fontSize: 13 }}>{forgotMsg}</div>}
            {!forgotSent ? (
              <Button block loading={loading} onClick={doForgot}>{t('login.sendCode')}</Button>
            ) : (
              <>
                <Input value={forgot.code} onChange={(v) => setForgot((f) => ({ ...f, code: v }))} placeholder={t('login.verificationCode')} />
                <Input type="password" value={forgot.newPassword} onChange={(v) => setForgot((f) => ({ ...f, newPassword: v }))} placeholder={t('login.newPassword')} />
                <Button block loading={loading} onClick={doReset}>{t('login.resetPassword')}</Button>
              </>
            )}
            <Button block variant="text" onClick={closeForgot}>{t('login.backToLogin')}</Button>
            {mode === 'home' && (
              <Button block variant="text" onClick={() => { closeForgot(); setView('register') }}>{t('login.selfRegister')}</Button>
            )}
          </div>
        </div>
      )}
    </>
  )

  // 左右分栏布局：一侧背景图，另一侧登录容器（容器可在左/右切换）
  if (layout.mode === 'split') {
    const imgPanel = (
      <div style={{ flex: 1, position: 'relative', overflow: 'hidden', minHeight: '100vh' }}>
        <BrandBgLayer src={branding.brandHomeBg} styleJson={branding.brandHomeBgStyle} />
      </div>
    )
      const formPanel = (
        <div style={{ flex: 1, position: 'relative', background: '#eef1f8', padding: 24, minHeight: '100vh' }}>
          <div style={{ position: 'absolute', left: `${cardPos.x}%`, top: `${cardPos.y}%`, transform: 'translate(-50%,-50%)', zIndex: 2, width: '100%', maxWidth: 400, maxHeight: '100vh', overflowY: 'auto', padding: '0 16px', display: 'flex', flexDirection: 'column', gap: 16 }}>{cards}</div>
          </div>
      )
    return (
      <div className="login-wrap" style={{ position: 'relative', overflow: 'hidden', display: 'flex', flexDirection: 'row', alignItems: 'stretch' }}>
        {layout.side === 'left' ? (<>{formPanel}{imgPanel}</>) : (<>{imgPanel}{formPanel}</>)}
      </div>
    )
  }

  // 全屏背景 + 可定位登录卡片（默认布局，无遮罩）
  return (
    <div className="login-wrap" style={{ position: 'relative', overflow: 'hidden' }}>
      <BrandBgLayer src={branding.brandHomeBg} styleJson={branding.brandHomeBgStyle} />
      <div style={{ position: 'absolute', left: `${cardPos.x}%`, top: `${cardPos.y}%`, transform: 'translate(-50%,-50%)', zIndex: 2, width: '100%', maxWidth: 400, maxHeight: '100vh', overflowY: 'auto', padding: '0 16px', display: 'flex', flexDirection: 'column', gap: 16 }}>
        {cards}
      </div>
    </div>
  )
}

// 语言读取辅助：单独封装避免顶层 hook 规则冲突，返回当前界面语言 'zh' | 'en'
function useLangSafe(): 'zh' | 'en' {
  const [lang] = useT()
  return lang
}
