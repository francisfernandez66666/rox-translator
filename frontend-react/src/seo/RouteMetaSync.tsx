// ============ 本文件职责中文说明 ============
// App 树（有 react-router）里的路由 meta 同步器：路由/语言/品牌变化时调用
// `syncRouteMeta`。PublicRoot 不挂 router，使用同一函数（见 PublicRoot.tsx）。
// ============
import { useEffect } from 'react'
import { useLocation } from 'react-router-dom'
import { useLang } from '@/i18n'
import { useBranding } from '@/branding'
import { syncRouteMeta } from './routeMeta'

// RouteMetaSync App 树的路由 meta 同步器（无 UI 输出）。
export function RouteMetaSync() {
  const { pathname } = useLocation()
  const lang = useLang()
  const b = useBranding()
  useEffect(() => {
    syncRouteMeta(pathname, lang, b.brandName || b.tenantName || '')
  }, [pathname, lang, b.brandName, b.tenantName])
  return null
}
