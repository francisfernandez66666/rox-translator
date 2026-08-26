// ============================================================================
// components/modals.tsx — 四个应用内弹窗（TDesign Dialog 实现）
// FeedbackModal（翻译结果/工单反馈）/ PasswordModal（改密：验证码+确认密码）/
// EmailBindModal（绑定/换绑邮箱）/ DeactivateModal（自助注销）
// 行为、表单字段、i18n 键均与 Vue 对应组件对齐；成功后回调父级刷新。
// ============================================================================
import { useState } from 'react'
import { Dialog, Input, Button, MessagePlugin, Textarea, Switch, Checkbox } from 'tdesign-react'
import {
  createFeedback, sendPwdCode, submitNewPassword,
  meEmailCode, updateEmail, deactivateAccount,
} from '@/api'
import type { ChatMessage } from '@/types'
import { t, tpl } from '@/i18n'
import { useAuth } from '@/stores/auth'

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
  const [content, setContent] = useState('')
  const [withContext, setWithContext] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  // 是否存在可附带的上下文（源文或译文任一非空）—— 仅此时显示勾选框
  // 计算是否拥有可附带的翻译上下文
  const hasContext = !!(
    props.target.source_text ||
    (props.target.translations && Object.keys(props.target.translations).length)
  )
  const ctxPreview = (props.target.source_text || '').slice(0, 40)

  // 提交反馈：校验非空 → 调用接口 → 成功提示并关闭弹窗
  async function submit() {
    if (!content.trim()) { void MessagePlugin.warning(t('fb.needContent')); return }
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
      if (r.success) { void MessagePlugin.success(t('fb.done')); props.onClose() }
      else void MessagePlugin.error(r.message || t('fb.fail'))
    } finally { setSubmitting(false) }
  }

  return (
    <Dialog header={t('fb.title')} visible onClose={props.onClose} width={520}
            footer={
              <>
                <Button variant="outline" onClick={props.onClose}>{t('common.cancel')}</Button>
                <Button disabled={!content.trim() || submitting} loading={submitting} onClick={submit}>
                  {submitting ? t('fb.submitting') : t('fb.submit')}
                </Button>
              </>
            }>
      <p className="fb-hint">{t('fb.hint')}</p>
      <Textarea autosize={{ minRows: 4 }} maxlength={1000} value={content} onChange={(v) => setContent(v as string)}
                placeholder={t('fb.placeholder')} />
      {hasContext && (
        <label className="fb-check">
          <Checkbox checked={withContext} onChange={(v) => setWithContext(v as boolean)} />
          <span style={{ marginLeft: 6 }}>{t('fb.withContext')}</span>
          {withContext && ctxPreview && <span className="fb-ctx-preview">（{ctxPreview}）</span>}
        </label>
      )}
    </Dialog>
  )
}

// ---------------- PasswordModal ----------------
// 修改密码弹窗：邮箱验证码 + 新密码 + 确认密码
export function PasswordModal(props: { onClose: () => void; onDone?: () => void; email?: string }) {
  const { user } = useAuth()
  const username = user?.username || ''
  const email = props.email || ''

  const [code, setCode] = useState('')
  const [newPwd, setNewPwd] = useState('')
  const [confirmPwd, setConfirmPwd] = useState('')
  const [msg, setMsg] = useState('')
  const [msgOk, setMsgOk] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [cooldown, setCooldown] = useState(0)

  // 发送改密验证码（带 60s 倒计时冷却）
  async function sendCode() {
    try {
      const r = await sendPwdCode({ username, email })
      setMsgOk(true)
      setMsg(r.message || t('pwd.codeSent'))
      setCooldown(60)
      const iv = window.setInterval(() => {
        setCooldown((c) => { if (c <= 1) { window.clearInterval(iv); return 0 } return c - 1 })
      }, 1000)
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
      window.alert(t('pwd.done'))
      props.onDone?.()
      props.onClose()
    } catch (e) {
      setMsgOk(false)
      setMsg(e instanceof Error ? e.message : String(e))
    } finally { setSubmitting(false) }
  }

  return (
    <Dialog header={t('pwd.title')} visible onClose={props.onClose} width={440}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <p className="fb-hint">{tpl('pwd.hint', { user: username })}</p>
        {email && (
          <div className="eb-old-row">
            <span className="eb-old-label">{t('emailBind.oldEmail')}</span>
            <span className="fb-addr">{email}</span>
          </div>
        )}
        <div className="pwd-code-row">
          <Input className="pwd-code-input" value={code} onChange={setCode} placeholder={t('login.verificationCode')} />
          <Button disabled={cooldown > 0} loading={submitting} onClick={sendCode}>
            {cooldown > 0 ? tpl('login.codeResend', { n: cooldown }) : t('login.sendCode')}
          </Button>
        </div>
        <Input type="password" value={newPwd} onChange={setNewPwd} placeholder={t('login.newPassword')} />
        <Input type="password" value={confirmPwd} onChange={setConfirmPwd} placeholder={t('pwd.confirmPlaceholder')} />
        {!!msg && (
          <div className={msgOk ? 'login-ok-hint' : 'login-error'}>{msg}</div>
        )}
        <div className="fb-actions">
          <Button variant="outline" onClick={props.onClose}>{t('common.cancel')}</Button>
          <Button theme="primary" disabled={submitting || !code || !newPwd} loading={submitting} onClick={submit}>
            {submitting ? t('pwd.submitting') : t('pwd.submit')}
          </Button>
        </div>
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
  const [newCooldown, setNewCooldown] = useState(0)
  const [oldCooldown, setOldCooldown] = useState(0)
  const [sendingNew, setSendingNew] = useState(false)
  const [sendingOld, setSendingOld] = useState(false)

  const valid = /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(newEmail.trim())

  // 通用倒计时启动：置 60 并在每秒递减至 0 后清除定时器
  function startCd(setter: (v: number | ((prev: number) => number)) => void) {
    setter(60)
    const iv = window.setInterval(() => {
      setter((c: number) => { if (c <= 1) { window.clearInterval(iv); return 0 } return c - 1 })
    }, 1000)
  }

  // 向新邮箱发送验证码并启动新邮箱倒计时
  async function sendNewCode() {
    if (!valid || newCooldown > 0 || sendingNew) return
    setSendingNew(true)
    try {
      const r = await meEmailCode(newEmail.trim())
      if (!r.success) { setOk(false); setMsg(r.message || t('pwd.sendFail')); return }
      startCd(setNewCooldown)
      setMsg(r.message || t('pwd.codeSent'))
      setOk(true)
    } finally { setSendingNew(false) }
  }

  // 向旧邮箱发送验证码并启动旧邮箱倒计时（仅在换绑时可用）
  async function sendOldCode() {
    if (!props.oldEmail || oldCooldown > 0 || sendingOld) return
    setSendingOld(true)
    try {
      const r = await meEmailCode(props.oldEmail)
      if (!r.success) { setOk(false); setMsg(r.message || t('pwd.sendFail')); return }
      startCd(setOldCooldown)
      setMsg(r.message || t('pwd.codeSent'))
      setOk(true)
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
    <Dialog header={props.dismissible ? t('emailBind.changeTitle') : t('emailBind.title')}
            visible onClose={props.onClose} closeBtn={props.dismissible !== false} width={460}
            footer={
              <>
                <Button variant="outline" onClick={props.onClose}>{t('common.cancel')}</Button>
                <Button theme="primary" disabled={!valid} loading={saving} onClick={save}>
                  {saving ? t('common.save') + '…' : t('emailBind.save')}
                </Button>
              </>
            }>
      <p className="eb-hint">{t('emailBind.reason')}</p>
      {props.oldEmail && (
        <div className="eb-old-row">
          <span className="eb-old-label">{t('emailBind.oldEmail')}</span>
          <span className="eb-old-addr">{props.oldEmail}</span>
        </div>
      )}
      {props.oldEmail && (
        <div className="eb-code-row">
          <Input className="eb-code-input" value={oldCode} onChange={setOldCode} placeholder={t('emailBind.oldCodePlaceholder')} />
          <Button variant="outline" disabled={oldCooldown > 0} loading={sendingOld} onClick={sendOldCode}>
            {oldCooldown > 0 ? tpl('login.codeResend', { n: oldCooldown }) : t('login.sendCode')}
          </Button>
        </div>
      )}
      <Input type="text" value={newEmail} onChange={setNewEmail} placeholder={t('emailBind.newEmailPlaceholder')} onEnter={save} />
      <div className="eb-code-row">
        <Input className="eb-code-input" value={code} onChange={setCode} placeholder={t('login.verificationCode')} onEnter={save} />
        <Button variant="outline" disabled={newCooldown > 0 || !valid || sendingNew} loading={sendingNew} onClick={sendNewCode}>
          {newCooldown > 0 ? tpl('login.codeResend', { n: newCooldown }) : t('login.sendCode')}
        </Button>
      </div>
      {!!msg && <p className={ok ? 'eb-ok' : 'eb-err'}>{msg}</p>}
    </Dialog>
  )
}

// ---------------- DeactivateModal ----------------
// 自助注销账号弹窗：需勾选确认后方可注销
export function DeactivateModal(props: { onClose: () => void }) {
  const { logout } = useAuth()
  const [acknowledged, setAcknowledged] = useState(false)
  const [busy, setBusy] = useState(false)

  // 执行注销：需先勾选确认 → 调用接口 → 成功提示、登出并关闭
  async function submit() {
    if (!acknowledged) { void MessagePlugin.warning(t('deact.needConfirm')); return }
    setBusy(true)
    try {
      const r = await deactivateAccount()
      if (!r.success) { void MessagePlugin.error(r.message || t('deact.fail')); return }
      void MessagePlugin.success(t('deact.done'))
      logout()
      props.onClose()
    } finally { setBusy(false) }
  }

  return (
    <Dialog header={t('deact.title')} visible onClose={props.onClose} width={400}
            footer={
              <>
                <Button variant="outline" onClick={props.onClose}>{t('common.cancel')}</Button>
                <Button theme="danger" disabled={!acknowledged || busy} loading={busy} onClick={submit}>
                  {busy ? t('deact.processing') : t('deact.confirm')}
                </Button>
              </>
            }>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
        <p style={{ fontSize: 13.5, color: '#333', lineHeight: 1.6, margin: 0 }}>{t('deact.line1')}</p>
        <ul style={{ margin: '0 0 4px 18px', fontSize: 13, color: '#555', lineHeight: 1.8 }}>
          <li>{t('deact.point1')}</li>
          <li>{t('deact.point2')}</li>
          <li>{t('deact.point3')}</li>
        </ul>
        <label className="fb-confirm-row">
          <Checkbox checked={acknowledged} onChange={(v) => setAcknowledged(v as boolean)} />
          <span style={{ fontSize: 13, color: '#333' }}>{t('deact.ack')}</span>
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
