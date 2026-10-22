// ============================================================================
// components/ProfileSetupGate.tsx — 登录态「补全资料」提醒闸门（★ 2026-10-21 IAM 重做）
//
// 为什么需要它：登录响应里的 profile_setup_required 原先只驱动 Login.tsx 的本屏弹窗。
// 用户刷新页面后走 /api/auth/me 恢复会话，不再经过登录屏，于是「可跳过、下次仍提醒」
// 的产品口径实际只剩「未刷新时提醒一次」。本闸门挂在 App 根，会话恢复与登录成功共用。
//
// 口径：
//   · 只提醒、不阻断：完成才清标记；暂时跳过只关本轮，库内标记仍为 1；
//   · 同一会话内按 user.id 记「已跳过」，避免 skip 返回 profile_setup_required=1 后立即重开；
//   · 刷新页面后 dismissed 自然失效，与下次登录仍提醒的语义一致。
// ★ 2026-10-22 统一登录批：主站（根域名 branding.tenantId===0）的补全从「简陋对话框」
//   升级为 AI 完善面板（AiRegisterFlow mode='complete'）——登录后直接弹出，问答式收集
//   用户名/密码/职业角色，企业分支走 authRegister 升级通道建组织。
//   门控信号与登录页一致：租户子域/演示站（tenantId>0）保持原对话框，这一批只影响主站。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { Dialog, Field, Input, useToast } from '@/ui/langcross/src'
import { profileSetup, registerConfig } from '@/api/auth'
import { setAuthToken } from '@/api/core'
import { useAuth } from '@/stores/auth'
import { PWD_MIN_LEN } from '@/lib/accountForm'
import { t } from '@/i18n'
import { useBranding } from '@/branding'
import AiRegisterFlow from './AiRegisterFlow'

/** ProfileSetupGate 已登录资料补全弹窗；完成、跳过或登出后自动收摊。 */
export default function ProfileSetupGate() {
  const { user, onLogin } = useAuth()
  const branding = useBranding()
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const dismissedRef = useRef(new Set<number>())
  // ★ 2026-10-22 统一登录批：主站判定与登录页同源（根域名=平台品牌 tenantId 0）。
  //   主站弹 AI 完善面板；租户子域/演示站继续用下面的原对话框。
  const mainSite = branding.tenantId === 0
  // AI 面板的人机验证配置：登录屏那份 registerConfig 随登录页卸载，闸门根级常驻，
  // 必须自己拉一份；拉取失败按「关闭」处理（与后端 captcha_provider=none 同义）。
  const [captcha, setCaptcha] = useState<{ enabled: boolean; siteKey: string } | undefined>(undefined)

  const required = !!user && user.profile_setup_required === 1
  const dismissed = !!user && dismissedRef.current.has(user.id)

  useEffect(() => {
    if (required && !dismissed) setOpen(true)
    else setOpen(false)
  }, [required, dismissed, user?.id])

  useEffect(() => {
    if (!open || !mainSite) return
    let dead = false
    ;(async () => {
      try {
        const c = await registerConfig()
        if (dead) return
        const on = !!(c as unknown as { captcha_enabled?: boolean }).captcha_enabled
        const key = (c as unknown as { captcha_site_key?: string }).captcha_site_key || ''
        setCaptcha({ enabled: on, siteKey: key })
      } catch { /* 拉不到就当人机验证关闭，不阻断补全流程 */ }
    })()
    return () => { dead = true }
  }, [open, mainSite])

  async function submit(skip: boolean) {
    if (!user || busy) return
    if (!skip) {
      if (!name.trim()) { setMsg(t('auth.username')); return }
      if (password && password.length < PWD_MIN_LEN) { setMsg(t('pwd.tooShort')); return }
    }
    setBusy(true); setMsg('')
    try {
      const r = await profileSetup({
        skip,
        username: skip ? undefined : name.trim(),
        password: skip || !password ? undefined : password,
      })
      if (!r.success) { setMsg(r.message || t('login.fail')); return }
      if (typeof r.token === 'string' && r.token) setAuthToken(r.token)
      const nextUser = (r.user && typeof r.user === 'object') ? r.user : {
        ...user,
        username: skip ? user.username : name.trim(),
        profile_setup_required: skip ? 1 : 0,
      }
      if (skip) dismissedRef.current.add(user.id)
      else dismissedRef.current.delete(user.id)
      onLogin(nextUser)
      setOpen(false)
      setPassword('')
      if (!skip) toast({ title: t('pwd.done'), tone: 'success' })
    } catch (e) {
      setMsg((e as Error).message || t('login.fail'))
    } finally { setBusy(false) }
  }

  if (!open) return null

  // ★ 2026-10-22 统一登录批：主站弹 AI 完善面板（登录后引导补全）。
  //   onDone：完成（个人补全 / 企业升级注册）后 onLogin 换上新用户，
  //   profile_setup_required 归 0 ⇒ required 转 false，闸门自然收摊；
  //   onClose（×）：本轮暂时跳过，记 dismissedRef，与旧对话框 skip 同语义。
  if (mainSite && user) {
    return (
      <AiRegisterFlow
        mode="complete"
        prefillUsername={user.username || ''}
        dedicatedRegister={branding.dedicatedRegister}
        captcha={captcha}
        loginEmail={user.email || ''}
        sessionUser={user}
        onDone={(u) => { onLogin(u); setOpen(false) }}
        onClose={() => { dismissedRef.current.add(user.id); setOpen(false) }}
      />
    )
  }

  return (
    <Dialog
      open
      title={t('auth.profileTitle')}
      confirmText={busy ? t('pwd.submitting') : t('auth.profileDone')}
      cancelText={t('auth.skipForNow')}
      onConfirm={() => void submit(false)}
      onCancel={() => void submit(true)}
      dismissOnOverlay={false}
    >
      <p style={{ fontSize: 15, color: 'var(--lc-text-3)', lineHeight: 1.7, margin: '0 0 14px' }}>{t('auth.profileDesc')}</p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <Field label={t('auth.username')}>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t('auth.username')} />
        </Field>
        <Field label={t('auth.passwordOptional')}>
          <Input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        {!!msg && <div className="auth-err" style={{ color: 'var(--lc-danger)', fontSize: 15, lineHeight: 1.6 }}>{msg}</div>}
      </div>
    </Dialog>
  )
}
