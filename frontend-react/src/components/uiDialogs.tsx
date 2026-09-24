// ============================================================================
// uiDialogs.tsx — 命令式确认/输入弹窗（langcross Dialog 驱动）
// 替代 window.confirm / window.prompt（PROGRESS.md 禁用原生弹窗）。
// 以 Promise 形式返回，方便在 async 函数里 await，保持原有 if (!confirm) return 的控制流。
// 用法：main.tsx 在 <ToastProvider> 内挂一次 <DialogHost/>；任意模块 import
// confirmDialog / promptText 直接调用（内部经模块级总线推给宿主渲染）。
// ============================================================================
import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { Dialog, Input } from '@/ui/langcross/src'
import { t } from '@/i18n' // ★ 2026-09-24 后台去写死中文：默认标题/按钮按界面语言取词（调用时刻求值）

// 命令式弹窗请求体：标题/正文/确认取消文案与 resolve 回调
interface DialogRequest {
  kind: 'confirm' | 'prompt'
  header: string
  body?: ReactNode
  danger: boolean
  defaultValue?: string
  placeholder?: string
  confirmText: string
  cancelText: string
  resolve: (v: boolean | string | null) => void
}

let seq = 0
let push: ((r: DialogRequest) => void) | null = null

/** 确认弹窗：用户点「确认」→ resolve(true)，取消/关闭/遮罩 → resolve(false)
 *  confirmText/cancelText 可定制按钮文案（如「确认删除」「确认取消」），
 *  避免动作按钮与弹窗取消按钮同名造成「点了没反应、再点确定才生效」的交互歧义。 */
export function confirmDialog(opts: {
  header?: string
  body?: ReactNode
  confirmText?: string
  cancelText?: string
  /** 危险操作（删除/停用等）：整框描边 #402323、确认按钮红底 */
  danger?: boolean
}): Promise<boolean> {
  return new Promise((resolve) => {
    if (!push) { resolve(false); return }
    push({
      kind: 'confirm',
      header: opts.header ?? t('common.confirmTitle'),
      body: opts.body,
      danger: !!opts.danger,
      confirmText: opts.confirmText ?? t('common.ok'),
      cancelText: opts.cancelText ?? t('common.cancel'),
      resolve: (v) => resolve(Boolean(v)),
    })
  })
}

/** 输入弹窗：取消/关闭 → resolve(null)，确认 → resolve(输入值) */
export function promptText(opts: {
  header?: string
  body?: ReactNode
  defaultValue?: string
  placeholder?: string
  confirmText?: string
  cancelText?: string
}): Promise<string | null> {
  return new Promise((resolve) => {
    if (!push) { resolve(null); return }
    push({
      kind: 'prompt',
      header: opts.header ?? t('common.inputTitle'),
      body: opts.body,
      danger: false,
      defaultValue: opts.defaultValue,
      placeholder: opts.placeholder,
      confirmText: opts.confirmText ?? t('common.ok'),
      cancelText: opts.cancelText ?? t('common.cancel'),
      resolve: (v) => resolve(typeof v === 'string' ? v : null),
    })
  })
}

/** 弹窗宿主：挂在 main.tsx <ToastProvider> 内一次；渲染当前请求并回传结果 */
export function DialogHost() {
  const [req, setReq] = useState<DialogRequest | null>(null)
  const [val, setVal] = useState('')

  useEffect(() => {
    push = (r) => { seq++; setVal(r.defaultValue ?? ''); setReq(r) }
    return () => { push = null }
  }, [])

  const close = (v: boolean | string | null) => {
    req?.resolve(v)
    setReq(null)
  }

  return (
    <Dialog
      key={seq}
      open={!!req}
      title={req?.header ?? ''}
      danger={req?.danger}
      confirmText={req?.confirmText}
      cancelText={req?.cancelText}
      onConfirm={() => close(req?.kind === 'prompt' ? val : true)}
      onCancel={() => close(req?.kind === 'prompt' ? null : false)}
    >
      {req?.kind === 'prompt' ? (
        <div>
          {req.body && <div style={{ marginBottom: 8 }}>{req.body}</div>}
          <Input autoFocus value={val} placeholder={req.placeholder}
                 onChange={(e) => setVal(e.target.value)} />
        </div>
      ) : req?.body}
    </Dialog>
  )
}
