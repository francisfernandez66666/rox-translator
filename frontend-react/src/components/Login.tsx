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
import { useBranding } from '@/branding'
import type { AuthUser } from '@/api'

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
  const [showReg, setShowReg] = useState(false)
  const [regMsg, setRegMsg] = useState('')
  const [agreed, setAgreed] = useState(false)
  const [roleChoice, setRoleChoice] = useState<'admin' | 'user'>('admin')
  const [form, setForm] = useState({ code: '', name: '', invite: '', email: '', emailCode: '', industry: '' })
  const [industries, setIndustries] = useState<Array<{ code: string; name: string }>>([])
  const [emailVerifyOn, setEmailVerifyOn] = useState(false)
  const [captchaOn, setCaptchaOn] = useState(false)
  const [codeCooldown, setCooldown] = useState(0)
  const captchaBoxRef = useRef<HTMLDivElement>(null)
  const captchaTokenRef = useRef('')

  // 忘记密码流程
  const [showForgot, setShowForgot] = useState(false)
  const [forgotMsg, setForgotMsg] = useState('')
  const [forgotSent, setForgotSent] = useState(false)
  const [forgot, setForgot] = useState({ username: '', email: '', code: '', newPassword: '' })

  // ★ U4：进入登录页时，若路径为 /register 或携带 ?ref= 邀请码，则自动展开注册面板并捕获个人裂变码
  useEffect(() => {
    try {
      const p = new URLSearchParams(window.location.search)
      const ref = p.get('ref')?.trim() || ''
      if (mode === 'home' && (window.location.pathname === '/register' || ref)) setShowReg(true)
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

  // 执行自助注册：逐项校验表单（密码长度、邀请码、邮箱、人机验证）→ 调用注册接口 → 成功后自动登录
  async function doRegister() {
    if (!username || !password) { setRegMsg(t('login.needInput')); return }
    if (password.length < 6) { setRegMsg(t('login.pwdShort')); return }
    if (!agreed) { setRegMsg(t('login.needAgree')); return }
    if (!branding.dedicatedRegister && roleChoice === 'user' && !form.invite.trim()) { setRegMsg(t('login.userNeedsInvite')); return }
    if (emailVerifyOn && !form.invite.trim() && (!form.emailCode.trim() || !form.email.trim())) { setRegMsg('请填写邮箱并输入验证码'); return }
    if (!form.email.trim()) { setRegMsg(t('login.emailPlaceholder')); return }
    if (captchaOn && !captchaTokenRef.current) { setRegMsg('请先完成人机验证'); return }
    const ref = (() => { try { return new URLSearchParams(window.location.search).get('ref')?.trim() || undefined } catch { return undefined } })()
    setLoading(true); setRegMsg('')
    try {
      const r = await authRegister({
        username, password,
        code: (branding.dedicatedRegister ? undefined : (form.code || undefined)),
        name: (branding.dedicatedRegister ? undefined : (form.name || undefined)),
        invite: (branding.dedicatedRegister ? undefined : (form.invite || undefined)),
        email: form.email.trim() || undefined,
        email_code: form.emailCode || undefined,
        captcha_token: captchaTokenRef.current || undefined,
        industry: (branding.dedicatedRegister ? undefined : (form.industry || undefined)),
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
    setShowForgot(false)
    setForgotSent(false)
    setForgot({ username: '', email: '', code: '', newPassword: '' })
  }

  return (
    <div className="login-wrap" style={branding.brandHomeBg ? { backgroundImage: `url(${branding.brandHomeBg})`, backgroundSize: 'cover', backgroundPosition: 'center' } : undefined}>
      <div className="login-lang">
        <Button size="small" variant="outline" theme="primary" onClick={toggleLang}>
          {{ zh: 'English', en: '中文' }[useLangSafe()]}
        </Button>
      </div>

      {/* 登录卡片 */}
      <div className="login-card">
        <div className="login-logo">
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 108 }} />
            : <span style={{ fontSize: 28, fontWeight: 800 }}>{branding.brandName || (mode === 'admin' ? t('login.platformAdmin') : t('login.platform'))}</span>}
        </div>
        <div className="login-sub">{mode === 'admin' ? t('login.adminOnly') : t('login.enterWorkspace')}</div>
          <form onSubmit={(e) => e.preventDefault()} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <Input value={username} onChange={setUsername} placeholder={t('login.username')}
                   autocomplete="username" onEnter={doLogin} clearable />
            <Input type="password" value={password} onChange={setPassword} placeholder={t('login.password')}
                   autocomplete="current-password" onEnter={doLogin} />
            {!!error && <div style={{ color: '#c62828', fontSize: 13 }}>{error}</div>}
            <Button block loading={loading} onClick={doLogin}>{t('login.signIn')}</Button>
            <Button block variant="text" onClick={() => setShowForgot(true)}>{t('login.forgot')}</Button>
            <Button block variant="text" onClick={() => setShowReg(!showReg)}>
              {showReg ? t('login.backToLogin') : t('login.selfRegister')}
            </Button>
          </form>
      </div>

      {/* 自助注册卡片 */}
      {showReg && mode === 'home' && (
        <div className="login-card">
          <div className="login-logo">{t('login.selfRegisterTitle')}</div>
          <div className="login-sub">{t('login.selfRegisterSub')}</div>
          {branding.dedicatedRegister ? (
            <div style={{ fontSize: 13, color: '#335', background: '#eef4ff', border: '1px solid #c9ddff', borderRadius: 8, padding: '10px 12px', marginBottom: 12, lineHeight: 1.6 }}>
              {tpl('login.dedicatedInfo', {
                name: branding.tenantName || branding.brandName || t('login.platform'),
                code: branding.code || '-',
                industry: branding.industryName || branding.industry || '-',
              })}
            </div>
          ) : (
            <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
              <Button block variant={roleChoice === 'admin' ? 'base' : 'outline'} theme="primary"
                      onClick={() => setRoleChoice('admin')}>🏢 {t('login.roleAdmin')}</Button>
              <Button block variant={roleChoice === 'user' ? 'base' : 'outline'} theme="primary"
                      onClick={() => setRoleChoice('user')}>👤 {t('login.roleUser')}</Button>
            </div>
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
            {roleChoice === 'admin' && !branding.dedicatedRegister ? (
              <>
                <Input value={form.code} onChange={(v) => setForm({ ...form, code: v })} placeholder={t('login.orgCode')} />
                <Input value={form.name} onChange={(v) => setForm({ ...form, name: v })} placeholder={t('login.orgName')} />
                <Select value={form.industry} onChange={(v) => setForm({ ...form, industry: v as string })}
                        placeholder={t('login.selectIndustry')} clearable
                        options={industries.map((i) => ({ label: i.name, value: i.code }))} />
              </>
            ) : null}
            {/* 邀请码：user 必填；admin 可选（个人裂变码走 URL ?ref= 自动携带）；专属域名场景不展示 */}
            {!branding.dedicatedRegister && (
              <Input value={form.invite} onChange={(v) => setForm({ ...form, invite: v })}
                     placeholder={roleChoice === 'user' ? t('login.inviteRequired') : t('login.invite')} />
            )}
            <div ref={captchaBoxRef} id="__ts_widget__" />
            <Checkbox checked={agreed} onChange={(c) => setAgreed(!!c)}>
              {t('login.agreeTerms')}{' '}
              <a href="/docs/terms" target="_blank" rel="noreferrer">{t('login.userAgreement')}</a>
              {' & '}
              <a href="/docs/privacy" target="_blank" rel="noreferrer">{t('login.privacyPolicy')}</a>
            </Checkbox>
            {!!regMsg && <div style={{ color: '#c62828', fontSize: 13 }}>{regMsg}</div>}
            <Button block loading={loading} onClick={doRegister}>{t('login.registerAndLogin')}</Button>
          </div>
        </div>
      )}

      {/* 忘记密码卡片 */}
      {showForgot && (
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
          </div>
        </div>
      )}
    </div>
  )
}

// 语言读取辅助：单独封装避免顶层 hook 规则冲突，返回当前界面语言 'zh' | 'en'
function useLangSafe(): 'zh' | 'en' {
  const [lang] = useT()
  return lang
}
