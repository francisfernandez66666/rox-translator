// ============================================================================
// components/modals.tsx — 应用内弹窗集合（langcross Dialog 实现）
// FeedbackModal（翻译结果/工单反馈）/ PasswordModal（改密：验证码+确认密码）/
// EmailBindModal（绑定/换绑邮箱）/ DeactivateModal（自助注销）/ JobRoleModal（职业角色维护）
// 行为、表单字段、i18n 键均与 Vue 对应组件对齐；成功后回调父级刷新。
// 呈现层替换：TDesign Dialog→受控 Dialog、Input/Textarea/Checkbox→langcross 同名组件、
//             Button(variant=outline/theme)→langcross Button、MessagePlugin→useToast。
// ============================================================================
import { useEffect, useState } from 'react'
import { Dialog, Input, Button, Textarea, Checkbox, useToast } from '@/ui/langcross/src'
import {
  createFeedback, sendPwdCode, submitNewPassword,
  meEmailCode, updateEmail, deactivateAccount,
} from '@/api'
import { registerPersonas, setMyJobRole } from '@/api/persona'
import { PERSONA_FALLBACK, personaName } from '@/lib/personas'
import { useCountdown } from '@/lib/useCountdown'
import type { ChatMessage } from '@/types'
import { t, tpl, useLang } from '@/i18n'
import { useAuth } from '@/stores/auth'

// ============ 本文件职责中文说明 ============
// 应用内弹窗：反馈、改密、换绑邮箱、自助注销、职业角色维护。
// ========================================

// ---------------- FeedbackModal ----------------
// 反馈目标描述：区分文本反馈与工单反馈，并携带可附带的上下文
export interface FeedbackTarget {
  type: 'text' | 'ticket'
  ticket_id?: number
  source_text?: string
  translations?: Record<string, string>
  mode?: string
}

// 反馈弹窗：输入反馈内容并可选择附带源文/译文上下文，提交到平台
export function FeedbackModal(props: { target: FeedbackTarget; onClose: () => void }) {
  const { toast } = useToast()
  const [content, setContent] = useState('')
  const [withContext, setWithContext] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  // 是否存在可附带的上下文（源文或译文任一非空）—— 仅此时显示勾选框
  // 计算是否拥有可附带的翻译上下文
  const hasContext = !!(
    props.target.source_text ||
    (props.target.translations && Object.keys(props.target.translations).length)
  )
  // ctxPreview 反馈上下文中源文的前 40 字符预览（仅用于勾选框旁提示）
  const ctxPreview = (props.target.source_text || '').slice(0, 40)

  // 提交反馈：校验非空 → 调用接口 → 成功提示并关闭弹窗
  async function submit() {
    if (!content.trim()) { toast({ title: t('fb.needContent'), tone: 'warn' }); return }
    setSubmitting(true)
    try {
      const r = await createFeedback({
        target_type: props.target.type,
        ticket_id: props.target.ticket_id,
        content: content.trim(),
        with_context: hasContext ? withContext : false,
        source_text: props.target.type === 'text' ? props.target.source_text : undefined,
        translations: props.target.type === 'text' ? props.target.translations : undefined,
        mode: props.target.mode,
      })
      if (r.success) { toast({ title: t('fb.done'), tone: 'success' }); props.onClose() }
      else toast({ title: r.message || t('fb.fail'), tone: 'error' })
    } catch (e) { // ★ E10：异常必须可见（旧实现 try/finally，网络错误静默）
      toast({ title: e instanceof Error ? e.message : t('common.submitFail'), tone: 'error' })
    } finally { setSubmitting(false) }
  }

  return (
    <Dialog
      open
      title={t('fb.title')}
      onCancel={props.onClose}
      confirmText={submitting ? t('fb.submitting') : t('fb.submit')}
      onConfirm={submit}
    >
      <p className="fb-hint">{t('fb.hint')}</p>
      <Textarea rows={4} maxLength={1000} value={content} onChange={(e) => setContent(e.target.value)}
                aria-label={t('fb.placeholder')} placeholder={t('fb.placeholder')} />
      {hasContext && (
        <label className="fb-check">
          <Checkbox checked={withContext} onChange={(e) => setWithContext(e.target.checked)} />
          <span style={{ marginInlineStart: 6 }}>{t('fb.withContext')}</span>
          {withContext && ctxPreview && <span className="fb-ctx-preview">（{ctxPreview}）</span>}
        </label>
      )}
    </Dialog>
  )
}

// ---------------- PasswordModal ----------------
// 修改密码弹窗：邮箱验证码 + 新密码 + 确认密码
export function PasswordModal(props: { onClose: () => void; onDone?: () => void; email?: string }) {
  const { toast } = useToast()
  const { user } = useAuth()
  const username = user?.username || ''
  const email = props.email || ''

  const [code, setCode] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [msg, setMsg] = useState('')
  const [msgOk, setMsgOk] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  // ★ #42：冷却倒计时改走 useCountdown——旧写法在弹窗关闭后仍会继续 tick（无卸载清理），
  //   且句柄只活在回调闭包里，二次触发会并存两个定时器把 60s 打成 30s。
  const cd = useCountdown(60)

  // 发送改密验证码（带 60s 倒计时冷却）
  async function sendCode() {
    try {
      const r = await sendPwdCode({ username, email })
      setMsgOk(true)
      setMsg(r.message || t('pwd.codeSent'))
      cd.start()
    } catch (e) {
      setMsgOk(false)
      setMsg(e instanceof Error ? e.message : String(e))
    }
  }

  // 提交新密码：校验长度与一致性 → 调用接口 → 成功提示并关闭
  async function submit() {
    if (newPwd.length < 6) { setMsgOk(false); setMsg(t('pwd.tooShort')); return }
    if (newPwd !== confirmPwd) { setMsgOk(false); setMsg(t('pwd.mismatch')); return }
    setSubmitting(true)
    try {
      const r = await submitNewPassword({ username, code: code.trim(), new_password: newPwd })
      if (!r.success) { setMsgOk(false); setMsg(r.message || t('pwd.codeBad')); return }
      setMsgOk(true)
      toast({ title: t('pwd.done'), tone: 'success' })
      props.onDone?.()
      props.onClose()
    } catch (e) {
      setMsgOk(false)
      setMsg(e instanceof Error ? e.message : String(e))
    } finally { setSubmitting(false) }
  }

  return (
    <Dialog
      open
      title={t('pwd.title')}
      onCancel={props.onClose}
      confirmText={submitting ? t('pwd.submitting') : t('pwd.submit')}
      onConfirm={submit}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <p className="fb-hint">{tpl('pwd.hint', { user: username })}</p>
        {email && (
          <div className="eb-old-row">
            <span className="eb-old-label">{t('emailBind.oldEmail')}</span>
            <span className="fb-addr">{email}</span>
          </div>
        )}
        <div className="pwd-code-row">
          <Input className="pwd-code-input" value={code} onChange={(e) => setCode(e.target.value)} placeholder={t('login.verificationCode')} />
          <Button variant="secondary" disabled={cd.left > 0} onClick={sendCode}>
            {cd.left > 0 ? tpl('login.codeResend', { n: cd.left }) : t('login.sendCode')}
          </Button>
        </div>
        <form onSubmit={(e) => e.preventDefault()}>
          <Input type="password" autoComplete="new-password" value={newPwd} onChange={(e) => setNewPwd(e.target.value)} placeholder={t('login.newPassword')} />
          <Input type="password" autoComplete="new-password" value={confirmPwd} onChange={(e) => setConfirmPwd(e.target.value)} placeholder={t('pwd.confirmPlaceholder')} />
        </form>
        {!!msg && (
          <div className={msgOk ? 'login-ok-hint' : 'login-error'}>{msg}</div>
        )}
      </div>
    </Dialog>
  )
}

// ---------------- EmailBindModal ----------------
// 绑定/换绑邮箱弹窗：新邮箱验证码，换绑时还需旧邮箱验证码
export function EmailBindModal(props: { hasOldEmail: boolean; oldEmail?: string; dismissible?: boolean; onClose: () => void; onDone?: (email: string) => void }) {
  const [newEmail, setNewEmail] = useState('') // 新邮箱默认置空（不带入老邮箱）
  const [code, setCode] = useState('')
  const [oldCode, setOldCode] = useState('')
  const [msg, setMsg] = useState('')
  const [ok, setOk] = useState(false)
  const [saving, setSaving] = useState(false)
  // ★ #42：两个冷却各自一个 useCountdown（旧写法是共用一个 startCd 手写 setInterval：
  //   弹窗关闭后定时器仍在跳，且同一状态被二次触发会并存两个递减源，60s 冷却提前见底）。
  const newCd = useCountdown(60)
  const oldCd = useCountdown(60)
  const [sendingNew, setSendingNew] = useState(false)
  const [sendingOld, setSendingOld] = useState(false)

  // valid 新邮箱格式校验（简单正则：非空用户名 + 域名）
  const valid = /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(newEmail.trim())

  // 向新邮箱发送验证码并启动新邮箱倒计时
  async function sendNewCode() {
    if (!valid || newCd.left > 0 || sendingNew) return
    setSendingNew(true)
    try {
      const r = await meEmailCode(newEmail.trim())
      if (!r.success) { setOk(false); setMsg(r.message || t('pwd.sendFail')); return }
      newCd.start()
      setMsg(r.message || t('pwd.codeSent'))
      setOk(true)
    } catch (e) { // ★ E10
      { setOk(false); setMsg(e instanceof Error ? e.message : String(t('pwd.sendFail'))) }
    } finally { setSendingNew(false) }
  }

  // 向旧邮箱发送验证码并启动旧邮箱倒计时（仅在换绑时可用）
  async function sendOldCode() {
    if (!props.oldEmail || oldCd.left > 0 || sendingOld) return
    setSendingOld(true)
    try {
      const r = await meEmailCode(props.oldEmail)
      if (!r.success) { setOk(false); setMsg(r.message || t('pwd.sendFail')); return }
      oldCd.start()
      setMsg(r.message || t('pwd.codeSent'))
      setOk(true)
    } catch (e) { // ★ E10
      { setOk(false); setMsg(e instanceof Error ? e.message : String(t('pwd.sendFail'))) }
    } finally { setSendingOld(false) }
  }

  // 提交换绑：校验邮箱格式 → 调用更新接口 → 成功回调并关闭
  async function save() {
    if (!valid || saving) return
    setSaving(true)
    try {
      const r = await updateEmail(newEmail.trim(), code.trim(), oldCode.trim())
      if (!r.success) { setOk(false); setMsg(r.message || 'failed'); return }
      setOk(true)
      props.onDone?.(newEmail.trim())
      props.onClose()
    } catch (e) {
      setOk(false)
      setMsg(e instanceof Error ? e.message : String(e))
    } finally { setSaving(false) }
  }

  return (
    <Dialog
      open
      title={props.dismissible ? t('emailBind.changeTitle') : t('emailBind.title')}
      onCancel={props.onClose}
      confirmText={saving ? t('common.save') + '…' : t('emailBind.save')}
      onConfirm={save}
    >
      <p className="eb-hint">{t('emailBind.reason')}</p>
      {props.oldEmail && (
        <div className="eb-old-row">
          <span className="eb-old-label">{t('emailBind.oldEmail')}</span>
          <span className="eb-old-addr">{props.oldEmail}</span>
        </div>
      )}
      {props.oldEmail && (
        <div className="eb-code-row">
          <Input className="eb-code-input" value={oldCode} onChange={(e) => setOldCode(e.target.value)} placeholder={t('emailBind.oldCodePlaceholder')} />
          <Button variant="secondary" disabled={oldCd.left > 0} onClick={sendOldCode}>
            {oldCd.left > 0 ? tpl('login.codeResend', { n: oldCd.left }) : t('login.sendCode')}
          </Button>
        </div>
      )}
      <Input type="text" value={newEmail} onChange={(e) => setNewEmail(e.target.value)} placeholder={t('emailBind.newEmailPlaceholder')} onKeyDown={(e) => { if (e.key === 'Enter') save() }} />
      <div className="eb-code-row">
        <Input className="eb-code-input" value={code} onChange={(e) => setCode(e.target.value)} placeholder={t('login.verificationCode')} onKeyDown={(e) => { if (e.key === 'Enter') save() }} />
        <Button variant="secondary" disabled={newCd.left > 0 || !valid || sendingNew} onClick={sendNewCode}>
          {newCd.left > 0 ? tpl('login.codeResend', { n: newCd.left }) : t('login.sendCode')}
        </Button>
      </div>
      {!!msg && <p className={ok ? 'eb-ok' : 'eb-err'}>{msg}</p>}
    </Dialog>
  )
}

// ---------------- DeactivateModal ----------------
// 自助注销账号弹窗：需勾选确认后方可注销
export function DeactivateModal(props: { onClose: () => void }) {
  const { toast } = useToast()
  const { logout } = useAuth()
  const [acknowledged, setAcknowledged] = useState(false)
  const [busy, setBusy] = useState(false)

  // 执行注销：需先勾选确认 → 调用接口 → 成功提示、登出并关闭
  async function submit() {
    if (!acknowledged) { toast({ title: t('deact.needConfirm'), tone: 'warn' }); return }
    setBusy(true)
    try {
      const r = await deactivateAccount()
      if (!r.success) { toast({ title: r.message || t('deact.fail'), tone: 'error' }); return }
      toast({ title: t('deact.done'), tone: 'success' })
      logout()
      props.onClose()
    } catch (e) { // ★ E10
      toast({ title: e instanceof Error ? e.message : String(t('deact.fail')), tone: 'error' })
    } finally { setBusy(false) }
  }

  return (
    <Dialog
      open
      title={t('deact.title')}
      danger
      onCancel={props.onClose}
      confirmText={busy ? t('deact.processing') : t('deact.confirm')}
      onConfirm={submit}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        <p style={{ fontSize: 15.5, color: 'var(--lc-text-2)', lineHeight: 1.6, margin: 0 }}>{t('deact.line1')}</p>
        <ul style={{ margin: '0 0 4px 18px', fontSize: 15, color: 'var(--lc-text-3)', lineHeight: 1.8 }}>
          <li>{t('deact.point1')}</li>
          <li>{t('deact.point2')}</li>
          <li>{t('deact.point3')}</li>
        </ul>
        <label className="fb-confirm-row">
          <Checkbox checked={acknowledged} onChange={(e) => setAcknowledged(e.target.checked)} />
          <span style={{ fontSize: 15, color: 'var(--lc-text-2)' }}>{t('deact.ack')}</span>
        </label>
      </div>
    </Dialog>
  )
}

// 兼容旧引用：ChatWindow 透传整条消息时构造 FeedbackTarget
// 从消息 data 中提取源文/译文/模式，转为文本类反馈目标
export function FeedbackModalFromMessage(props: { message: ChatMessage; onClose: () => void }) {
  const d = (props.message.data || {}) as Record<string, any>
  return (
    <FeedbackModal
      target={{ type: 'text', source_text: d.source_text, translations: d.translations, mode: String(d.mode || '') }}
      onClose={props.onClose}
    />
  )
}

// ---------------- JobRoleModal ----------------
// 职业角色维护弹窗（2026-09-19）：角色只绑用户不绑企业，转岗/转行随时切换，
// 清空即回落通用翻译。选项动态取后端角色字典（租户0 persona 包），接口失败落本地兜底词库。
export function JobRoleModal(props: { current: string; onClose: () => void; onSaved?: (code: string) => void }) {
  const { toast } = useToast()
  const lang = useLang() // ★ 2026-09-24：内置角色名按界面语言本地化
  const [list, setList] = useState<Array<{ code: string; name: string }>>(PERSONA_FALLBACK)
  const [sel, setSel] = useState(props.current)
  const [saving, setSaving] = useState(false)
  // 角色字典拉取失败静默保留兜底：下拉仍可用，保存由后端按启用中字典二次校验
  useEffect(() => {
    (async () => {
      try {
        const r = await registerPersonas()
        if (r.success && Array.isArray(r.personas) && r.personas.length > 0) setList(r.personas)
      } catch { /* ignore */ }
    })()
  }, [])
  async function submit() {
    setSaving(true)
    try {
      const r = await setMyJobRole(sel)
      if (r.success) {
        toast({ title: sel ? t('role.saved') : t('role.cleared'), tone: 'success' })
        props.onSaved?.(sel)
        props.onClose()
      } else {
        toast({ title: r.message || t('role.saveFail'), tone: 'error' })
      }
    } catch (e) { // ★ E10：异常必须可见
      toast({ title: e instanceof Error ? e.message : t('role.saveFail'), tone: 'error' })
    } finally { setSaving(false) }
  }
  return (
    <Dialog
      open
      title={t('role.title')}
      onCancel={props.onClose}
      confirmText={t('common.save')}
      onConfirm={submit}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <p style={{ fontSize: 15, color: 'var(--lc-text-2)', lineHeight: 1.6, margin: 0 }}>{t('role.hint')}</p>
        <select className="lc-input" value={sel} disabled={saving}
                aria-label={t('role.title')} onChange={(e) => setSel(e.target.value)}>
          <option value="">{t('role.none')}</option>
          {list.map((x) => <option key={x.code} value={x.code}>{personaName(x.code, x.name, lang)}</option>)}
        </select>
      </div>
    </Dialog>
  )
}
