// ============================================================================
// lib/toastBus.ts — 命令式 toast 总线（MessagePlugin 的等价替换，非组件环境专用）
// 背景：runGuarded 等 lib 层/深层回调拿不到 useToast hook，过去直接用 TDesign 的
// MessagePlugin。迁移后统一走本总线：<ToastBridge/>（挂在 ToastProvider 内部）
// 把总线接回 useToast，任何模块 import { toastError } 即可，行为与 hook 版一致
// （右上 72/24、最多 3 条、4.2s 自动消失）。
// ★ 2026-11-04 体验批 A10：总线支持一颗可选「retry」动作钮（超时红条一键重发），
//   手法是把按钮合成进 ReactNode desc——Toast.tsx 本就把 desc 当 ReactNode 渲染，
//   所以渲染层零改动、通道形态不外扩（不是新增一套 action 体系）。
// ============================================================================
import { createElement, type ReactNode } from 'react'

// 跨组件提示事件载荷：语气/标题/描述（+ 可选重试动作）
export interface ToastPayload {
  title: ReactNode
  desc?: ReactNode
  tone?: 'success' | 'error' | 'warn'
  /** 停留毫秒数，默认 4200 */
  duration?: number
  /** 可选重试动作（★ A10 超时红条）：toast() 把它渲染成 desc 内的一颗按钮，点击执行 onRetry；
   *  复用 Toast.tsx 对 ReactNode desc 的既有渲染能力，渲染层零改动。 */
  retry?: { label: ReactNode; onRetry: () => void }
}

// 提示总线订阅者签名
type Handler = (p: ToastPayload) => void
// 模块级单变量而不是 Context：调用点常在 React 之外（lib 层函数、await 之后的回调），
// 那里没有 hook 可用，只能靠一个进程内全局引用把消息递回组件树
let handler: Handler | null = null

/** ToastBridge 挂载时注册、卸载时置 null（StrictMode 双挂载安全） */
export function registerToastHandler(fn: Handler | null) {
  handler = fn
}

/** 弹一条提示：没挂 ToastBridge（单测、独立预览页）时静默丢掉。
 *  刻意不抛错也不 console 告警——提示失败不应该让调用方的业务动作跟着失败。
 *  ★ 2026-11-04 A10：带 retry 时把「重试」按钮合成进 desc（复用 Toast.tsx 的 ReactNode desc），
 *    按钮点击只调 onRetry；不带 retry 时与原形态逐字等价，旧调用点零感知。 */
export function toast(p: ToastPayload) {
  if (!handler) return
  const { retry, ...rest } = p
  if (!retry) { handler(rest); return }
  const btn = createElement('button', {
    type: 'button',
    onClick: retry.onRetry,
    style: {
      marginInlineStart: 8, padding: '2px 10px', cursor: 'pointer',
      border: '1px solid currentColor', borderRadius: 6,
      background: 'transparent', color: 'inherit', fontSize: 13,
    },
  }, retry.label)
  handler({ ...rest, desc: rest.desc ? createElement('span', null, rest.desc, btn) : btn })
}

// 下面三个只是把 tone 钉死的便捷包装：省掉调用方重复写字面量，也避免拼错成 'succsee' 这种静默降级

/** 成功提示快捷入口 */
export function toastSuccess(title: ReactNode, desc?: ReactNode) {
  toast({ title, desc, tone: 'success' })
}

/** 错误提示快捷入口（组件外也能调，不用 hook）。
 *  ★ 2026-11-04 A10：可选 retry 给「超时红条」挂一键重试按钮；其余调用点仍用
 *    (title, desc) 两参写法，行为一字不变（retry 缺省即原形态）。 */
export function toastError(title: ReactNode, desc?: ReactNode, retry?: { label: ReactNode; onRetry: () => void }) {
  toast({ title, desc, tone: 'error', retry })
}

/** 警示提示快捷入口（语气档 warn，色取 #D29922） */
export function toastWarn(title: ReactNode, desc?: ReactNode) {
  toast({ title, desc, tone: 'warn' })
}
