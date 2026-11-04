// ============================================================================
// lib/runGuarded.ts — 异步操作统一兜底（★ E10）
// 背景：全站十余处 submit/操作直接 `await api()` 无 catch——网络错误/超时被
// unhandled rejection 静默吞掉，用户以为成功。约定：所有用户触发的异步动作
// 用 runGuarded 包裹，失败统一 toast 错误信息并返回 undefined 供调用方短路。
// 2026-09-17 纯黑换肤：提示出口由 TDesign MessagePlugin 改为 lib/toastBus 的
// toastError（内部再经 <ToastBridge/> 转接到 LangCross ToastProvider），
// 目的是让「非组件环境」也能弹提示——toastBus 不依赖 hook 上下文。
// ★ 2026-11-04 体验批 A10：这里就是「超时红条」的错误展示面。超时那一档在
//   toastError 的 desc 里多挂一颗一键重试钮（§2.10 Q-M1），点击经 core.retryRequest
//   重发同一请求；其余失败档逐字保持原形态。
// ============================================================================

// toastBus 而非 useToast：本文件被 lib/组件外的工具链路引用，拿不到 ToastProvider 的
// hook 上下文，只能走「模块级总线 + Provider 内桥接组件」这一层间接调用。
import { toastError } from '@/lib/toastBus'
import { t } from '@/i18n' // ★ 2026-09-24 后台去写死中文：兜底文案按界面语言取词（toastBus 同为非 hook 通道，无上下文问题）
// ★ 2026-11-04 A10：超时判定/重发由 api/core 以纯函数导出（lib → api 单向，不反向依赖组件层）
import { isTimeoutError, retryRequest } from '@/api/core'

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
    const text = msg || opts?.fallback || t('common.fail')
    if (opts?.onError) { opts.onError(text, e); return undefined }
    // ★ 2026-11-04 体验批 A10（§2.10 Q-M1）：只有「这一跳没走完」这一档给一键重试出口，
    //   点击重发同一请求（core.retryRequest 复用原始 url/init/timeoutMs）；5xx「服务异常」等
    //   其余失败档照旧只弹原文——慢与坏分开报，别把重试钮挂到真故障上。
    //   重发本身也过 runGuarded：再失败会再出声（并再带一颗重试钮），不产生 unhandled rejection。
    if (isTimeoutError(e)) {
      toastError(text, undefined, {
        label: t('common.retryRequest'),
        onRetry: () => { void runGuarded(() => retryRequest(e)) },
      })
    } else {
      void toastError(text)
    }
    return undefined
  }
}
