// ============================================================================
// lib/runGuarded.ts — 异步操作统一兜底（★ E10）
// 背景：全站十余处 submit/操作直接 `await api()` 无 catch——网络错误/超时被
// unhandled rejection 静默吞掉，用户以为成功。约定：所有用户触发的异步动作
// 用 runGuarded 包裹，失败统一 toast 错误信息并返回 undefined 供调用方短路。
// 2026-09-17 纯黑换肤：提示出口由 TDesign MessagePlugin 改为 lib/toastBus 的
// toastError（内部再经 <ToastBridge/> 转接到 LangCross ToastProvider），
// 目的是让「非组件环境」也能弹提示——toastBus 不依赖 hook 上下文。
// ============================================================================

// toastBus 而非 useToast：本文件被 lib/组件外的工具链路引用，拿不到 ToastProvider 的
// hook 上下文，只能走「模块级总线 + Provider 内桥接组件」这一层间接调用。
import { toastError } from '@/lib/toastBus'

/**
 * runGuarded 执行一个异步动作；抛错时提示并返回 undefined（成功返回原结果）。
 * 业务失败（HTTP 200 + success:false）不走异常，由调用方自行判断 resp.success。
 * @param opts.onError 传入则接管失败（不再走默认 toast）；两者互斥，避免双提示。
 */
export async function runGuarded<T>(
  fn: () => Promise<T>,
  opts?: { fallback?: string; onError?: (msg: string, e: unknown) => void },
): Promise<T | undefined> {
  try {
    return await fn()
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    if (opts?.onError) opts.onError(msg || opts?.fallback || '操作失败', e)
    else void toastError(msg || opts?.fallback || '操作失败')
    return undefined
  }
}
