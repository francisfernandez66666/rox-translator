// ============================================================================
// lib/toastBus.ts — 命令式 toast 总线（MessagePlugin 的等价替换，非组件环境专用）
// 背景：runGuarded 等 lib 层/深层回调拿不到 useToast hook，过去直接用 TDesign 的
// MessagePlugin。迁移后统一走本总线：<ToastBridge/>（挂在 ToastProvider 内部）
// 把总线接回 useToast，任何模块 import { toastError } 即可，行为与 hook 版一致
// （右上 72/24、最多 3 条、4.2s 自动消失）。
// ============================================================================
import type { ReactNode } from 'react'

export interface ToastPayload {
  title: ReactNode
  desc?: ReactNode
  tone?: 'success' | 'error' | 'warn'
  /** 停留毫秒数，默认 4200 */
  duration?: number
}

type Handler = (p: ToastPayload) => void
// 模块级单变量而不是 Context：调用点常在 React 之外（lib 层函数、await 之后的回调），
// 那里没有 hook 可用，只能靠一个进程内全局引用把消息递回组件树
let handler: Handler | null = null

/** ToastBridge 挂载时注册、卸载时置 null（StrictMode 双挂载安全） */
export function registerToastHandler(fn: Handler | null) {
  handler = fn
}

/** 弹一条提示：没挂 ToastBridge（单测、独立预览页）时静默丢掉。
 *  刻意不抛错也不 console 告警——提示失败不应该让调用方的业务动作跟着失败 */
export function toast(p: ToastPayload) {
  handler?.(p)
}

// 下面三个只是把 tone 钉死的便捷包装：省掉调用方重复写字面量，也避免拼错成 'succsee' 这种静默降级
export function toastSuccess(title: ReactNode, desc?: ReactNode) {
  toast({ title, desc, tone: 'success' })
}

export function toastError(title: ReactNode, desc?: ReactNode) {
  toast({ title, desc, tone: 'error' })
}

export function toastWarn(title: ReactNode, desc?: ReactNode) {
  toast({ title, desc, tone: 'warn' })
}
