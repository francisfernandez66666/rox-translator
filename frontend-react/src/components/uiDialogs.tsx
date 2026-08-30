// ============ 本文件职责中文说明 ============
// 确认/输入弹窗组件（基于 TDesign DialogPlugin，替代原生 confirm/prompt）
// =============================================
// uiDialogs.tsx — 基于 TDesign DialogPlugin 的确认/输入弹窗
// 替代 window.confirm / window.prompt（PROGRESS.md 禁用原生弹窗）。
// 以 Promise 形式返回，方便在 async 函数里 await，保持原有 if (!confirm) return 的控制流。
import { DialogPlugin, Input } from 'tdesign-react'

// 确认弹窗：用户点「确认」→ resolve(true)，取消/关闭/遮罩 → resolve(false)
export function confirmDialog(opts: { header?: string; body: string }): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false
    const done = (v: boolean) => { if (!settled) { settled = true; resolve(v) } }
    const inst = DialogPlugin.confirm({
      header: opts.header ?? '确认操作',
      body: opts.body,
      onConfirm: () => { inst.hide(); done(true) },
      onClose: () => done(false),
    })
  })
}

// 输入弹窗：取消/关闭 → resolve(null)，确认 → resolve(输入值)
export function promptText(opts: {
  header?: string
  body?: string
  defaultValue?: string
  placeholder?: string
}): Promise<string | null> {
  return new Promise((resolve) => {
    let settled = false
    let value = opts.defaultValue ?? ''
    const done = (v: string | null) => { if (!settled) { settled = true; resolve(v) } }
    const inst = DialogPlugin.confirm({
      header: opts.header ?? '请输入',
      body: (
        <div>
          {opts.body && <div style={{ marginBottom: 8 }}>{opts.body}</div>}
          <Input
            defaultValue={opts.defaultValue}
            placeholder={opts.placeholder}
            onChange={(v) => { value = String(v) }}
          />
        </div>
      ),
      onConfirm: () => { inst.hide(); done(value) },
      onClose: () => done(null),
    })
  })
}
