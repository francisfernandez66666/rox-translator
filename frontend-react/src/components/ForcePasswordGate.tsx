// ============================================================================
// components/ForcePasswordGate.tsx — 全局「强制改密」闸门（★ 2026-10-21 IAM 重做 P0-8）
//
// 为什么需要它：后端 `withPasswordGate` 对 must_change_pwd 账号的非认证接口一律回 428
// （PASSWORD_CHANGE_REQUIRED）。这条 428 发生在**已经进了工作台之后**，而改密弹窗原先
// 只挂在登录屏（Login.tsx 的 ForcePwdDialog）——于是用户被服务端拦住、界面上却没有任何
// 地方能把密码改掉，只能看到一句「请求失败 (428)」。
//
// 口径：
//   · 触发源只有一处：api/core.ts 的 handlePasswordChangeRequired() 派发
//     `lc:force-password` 事件（core 不依赖组件层，故用事件总线）；
//   · 本组件挂在 main.tsx 的 ToastProvider 内（要用 toast），与 App/PublicRoot 的选路无关；
//   · 不可用遮罩关闭：用户必须改完（或主动退出登录）。取消键＝退出登录，
//     因为「忘了管理员给的初始口令」确实是一条合理出路，不能把人锁死在弹窗里；
//   · 改密成功会抬 token_version，服务端随响应重签一条新 token——必须落地，
//     否则下一个请求立刻又吃 401/428（本批 Login.tsx 同一条口径）。
// ============================================================================
import { useEffect, useState } from 'react'
import { Dialog, Field, Input, useToast } from '@/ui/langcross/src'
// ⚠️ 只从域文件直取，**不要** `import { … } from '@/api'`：本组件在入口包里，
//   那个桶会顺着 index.ts 把全部 API 域一起拉进入口 bundle（T8 420,000 B 直接被顶破）。
import { changePassword } from '@/api/auth'
import { setAuthToken, FORCE_PASSWORD_EVENT } from '@/api/core'
import { PWD_MIN_LEN } from '@/lib/accountForm'
import { t } from '@/i18n'

/** ForcePasswordGate 首登强制改密弹窗：收到 428 事件后打开，成功后清令牌并提示重新登录。 */
export default function ForcePasswordGate() {
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [old, setOld] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [msg, setMsg] = useState('')

  useEffect(() => {
    const onForce = () => {
      // 只开框、不自动清空用户已经敲进去的内容（连续几条 428 不该把输入冲掉）
      setMsg('')
      setOpen(true)
    }
    window.addEventListener(FORCE_PASSWORD_EVENT, onForce)
    return () => window.removeEventListener(FORCE_PASSWORD_EVENT, onForce)
  }, [])

  async function submit() {
    if (!old) { setMsg(t('pwd.oldRequired')); return }
    if (next.length < PWD_MIN_LEN) { setMsg(t('pwd.tooShort')); return }
    if (next !== confirm) { setMsg(t('pwd.mismatch')); return }
    setBusy(true); setMsg('')
    try {
      const r = await changePassword(old, next)
      if (!r.success) { setMsg(r.message || t('auth.forcePwdFail')); return }
      // 新 token 必须落地：改密会抬 token_version，旧 token 从下一个请求起即被判撤销
      if (typeof r.token === 'string' && r.token) setAuthToken(r.token)
      else setAuthToken('') // 服务端重签失败：如实回落（重新登录），不假装会话仍然有效
      toast({ title: t('pwd.done'), tone: 'success' })
      setOpen(false)
      setOld(''); setNext(''); setConfirm('')
      // 整页重载：让所有已经拉过数据的面板按新会话重取（比逐处刷新可靠，且这一刻没有未保存状态）
      window.location.reload()
    } catch (e) {
      setMsg((e as Error).message || t('auth.forcePwdFail'))
    } finally { setBusy(false) }
  }

  function signOut() {
    // 放弃改密＝登出：清本地会话后回登录页（整页跳转，顺带把内存里的用户态一起丢掉）。
    // 这里不 import stores/auth 是为了让本组件与 zustand 的初始化顺序无关——
    // 清掉 token 之后，入口分诊 pickEntry() 自然会把 /login 路由到未登录门面。
    setAuthToken('')
    window.location.assign('/login')
  }

  return (
    <Dialog
      open={open}
      title={t('pwd.forceTitle')}
      confirmText={t('pwd.forceSubmit')}
      onConfirm={submit}
      onCancel={() => { if (!busy) signOut() }}
      dismissOnOverlay={false}
    >
      <p style={{ fontSize: 15, color: 'var(--lc-text-3)', lineHeight: 1.7, margin: '0 0 14px' }}>{t('pwd.forceHint')}</p>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        <Field label={t('pwd.oldPassword')}>
          <Input type="password" value={old} onChange={(e) => setOld(e.target.value)} />
        </Field>
        <Field label={t('auth.newPassword')}>
          <Input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </Field>
        <Field label={t('pwd.confirmPlaceholder')}>
          <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </Field>
        {!!msg && <div className="auth-err" style={{ color: 'var(--lc-danger)', fontSize: 15, lineHeight: 1.6 }}>{msg}</div>}
      </div>
    </Dialog>
  )
}
