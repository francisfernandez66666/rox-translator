// ============================================================================
// components/ToastBridge.tsx — 命令式 toast 总线 ↔ useToast 的桥
// 挂在 main.tsx 的 <ToastProvider> 内部一次即可；此后 lib 层/任意回调深处
// import { toastError/toastSuccess/toastWarn } from '@/lib/toastBus' 直接弹提示。
// ============================================================================
import { useEffect } from 'react'
import { useToast } from '@/ui/langcross/src'
import { registerToastHandler } from '@/lib/toastBus'

/** 总线接线桥：把 ToastProvider 的 toast 交给 lib/toastBus，自身不渲染任何 DOM */
export default function ToastBridge() {
  const { toast } = useToast()
  // 依赖写 [toast] 而不是 []：一旦上层 Provider 重建导致 toast 引用换了，总线要跟着改接新闭包，
  // 否则会一直朝旧的 setter 里塞消息（表现就是「提示不弹了但也没报错」）。
  // cleanup 置 null 覆盖 StrictMode 双挂载：卸载→重挂后留在总线上的始终是最后一次注册的 handler。
  // ⚠ 全局只挂这一个：<ToastBridge/> 挂两处就会互相覆盖（模块级单变量），后挂载的那个赢。
  useEffect(() => {
    registerToastHandler(toast)
    return () => registerToastHandler(null)
  }, [toast])
  return null
}
