// branding.tsx — 租户级品牌定制上下文（按域名/租户解析品牌展示信息）
import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import { useAuth } from './stores/auth'

export interface BrandLink {
  label: string    // 中文标签
  label_en: string // 英文标签
  url: string      // 跳转地址
}

export interface Branding {
  tenantId: number
  tenantName: string  // 租户名称（用于网页标题等）
  brandName: string  // 自定义品牌展示名（空=用默认）
  brandLogo: string  // 自定义品牌 Logo URL（空=用默认文字）
  domain: string     // 子域名前缀
}

// 默认品牌（未定制时回退）
export const DEFAULT_BRAND_NAME = '能言 LangCross'

const DEFAULT: Branding = { tenantId: 0, tenantName: '', brandName: '', brandLogo: '', domain: '' }

const Ctx = createContext<Branding>(DEFAULT)

export const useBranding = () => useContext(Ctx)

// BrandingProvider 在挂载时按访问域名解析品牌；登录后改用「当前用户所属租户」解析，
// 使品牌始终跟随登录用户（不依赖子域名 DNS 是否可达）。可传入 tenantId 覆盖（超管切换租户预览）。
export function BrandingProvider({ tenantId, children }: { tenantId?: number; children: ReactNode }) {
  const { user } = useAuth()
  const [b, setB] = useState<Branding>(DEFAULT)
  // 解析优先级：显式 tenantId > 登录用户所属租户 > 按访问域名/默认
  const effectiveTenantId = tenantId ?? (user?.tenant_id || 0)
  useEffect(() => {
    let alive = true
    const url = '/api/tenant/branding' + (effectiveTenantId ? `?tenant_id=${effectiveTenantId}` : '')
    fetch(url)
      .then((r) => r.json())
      .then((j: any) => {
        if (!alive || !j || !j.success) return
        setB({
          tenantId: j.tenant_id || 0,
          tenantName: j.name || '',
          brandName: j.brand_name || '',
          brandLogo: j.brand_logo || '',
          domain: j.domain || '',
        })
      })
      .catch(() => {})
    return () => { alive = false }
  }, [effectiveTenantId])
  // 网页标题随「租户名称 / 品牌名」定制，例如「ROX极石汽车 智能翻译平台」
  useEffect(() => {
    const name = b.brandName || b.tenantName
    document.title = name ? `${name} 智能翻译平台` : '智能翻译平台'
  }, [b])
  return <Ctx.Provider value={b}>{children}</Ctx.Provider>
}
