// ============================================================================
// components/ToastBridge.tsx — 命令式 toast 总线 ↔ useToast 的桥
// 挂在 main.tsx 的 <ToastProvider> 内部一次即可；此后 lib 层/任意回调深处
// import { toastError/toastSuccess/toastWarn } from '@/lib/toastBus' 直接弹提示。
// ★ 2026-09-22 §4.2-3：同时向 api/core 注入 403 对外文案解析器——core 基础设施层刻意
//   不反向 import i18n（会崩 api 层 node 环境单测，见 core.ts 注释），本地化文案在这里
//   由具备 i18n 能力的组件层提供（复用全站既有权限键 admin.forbid，不新增键）。
// ============================================================================
import { useEffect } from 'react'
import { useToast } from '@/ui/langcross/src'
import { registerToastHandler } from '@/lib/toastBus'
import { setForbiddenCopyResolver } from '@/api'
import { t } from '@/i18n'

/** 总线接线桥：把 ToastProvider 的 toast 交给 lib/toastBus，自身不渲染任何 DOM */
export default function ToastBridge() {
  const { toast } = useToast()
  // 依赖写 [toast] 而不是 []：一旦上层 Provider 重建导致 toast 引用换了，总线要跟着改接新闭包，
  // 否则会一直朝旧的 setter 里塞消息（表现就是「提示不弹了但也没报错」）。
  // cleanup 置 null 覆盖 StrictMode 双挂载：卸载→重挂后留在总线上的始终是最后一次注册的 handler。
  // ⚠ 全局只挂这一个：<ToastBridge/> 挂两处就会互相覆盖（模块级单变量），后挂载的那个赢。
  useEffect(() => {
    registerToastHandler(toast)
    // 403 文案注入：命中越权一律回落到本地化「无管理权限」既有键（12 语种全覆盖）；
    // 卸载时复位为 null，避免残留解析器指向已销毁的渲染上下文。
    setForbiddenCopyResolver(() => t('admin.forbid'))
    return () => { registerToastHandler(null); setForbiddenCopyResolver(null) }
  }, [toast])
  return null
}
