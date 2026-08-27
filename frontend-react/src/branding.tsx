// branding.tsx — 租户级品牌定制上下文（按域名/租户解析品牌展示信息）
import { createContext, useContext, useEffect, useState, ReactNode } from 'react'

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
  brandHomeBg: string   // 未登录首页背景图（base64 dataURL 或外链 URL，空=用默认）
  brandHomeBgStyle: string // 首页背景图样式 JSON（{scale,x,y,mode}：mode=tile/cover/contain）
  brandLoginCardPos: string // 登录/注册卡片位置 JSON（{x,y} 百分比，卡片中心相对视口，缺省居中）
  brandLoginLayout: string  // 登录页布局 JSON（{mode:'full'|'split', side:'left'|'right'}，缺省全屏背景）
  code: string          // 企业编码（专属域名注册自动带入展示）
  industry: string      // 行业编码（专属域名注册自动带入展示）
  industryName: string  // 行业名称（专属域名注册自动带入展示）
  dedicatedRegister: boolean // 当前是否处于「专属域名自助注册」场景
}

// 默认品牌（未定制时回退）
export const DEFAULT_BRAND_NAME = '能言 LangCross'

const DEFAULT: Branding = {
  tenantId: 0, tenantName: '', brandName: '', brandLogo: '', domain: '',
  brandHomeBg: '', brandHomeBgStyle: '', brandLoginCardPos: '', brandLoginLayout: '', code: '', industry: '', industryName: '', dedicatedRegister: false,
}

// 背景图样式默认值：充满(cover) + 不缩放(scale=1) + 居中(x=50,y=50)
export interface BgStyle { scale: number; x: number; y: number; mode: 'tile' | 'cover' | 'contain' }

// 登录卡片位置默认值：居中（百分比为卡片中心相对视口）
export interface CardPos { x: number; y: number }
export function parseCardPos(json?: string): CardPos {
  const d: CardPos = { x: 50, y: 50 }
  if (!json) return d
  try {
    const o = JSON.parse(json)
    if (typeof o.x === 'number') d.x = Math.min(100, Math.max(0, o.x))
    if (typeof o.y === 'number') d.y = Math.min(100, Math.max(0, o.y))
  } catch { /* 解析失败用默认居中 */ }
  return d
}

// 登录页布局默认值：全屏背景（full）；分栏时容器默认在右侧（right）
export interface LoginLayout { mode: 'full' | 'split'; side: 'left' | 'right' }
export function parseLoginLayout(json?: string): LoginLayout {
  const d: LoginLayout = { mode: 'full', side: 'right' }
  if (!json) return d
  try {
    const o = JSON.parse(json)
    if (o.mode === 'full' || o.mode === 'split') d.mode = o.mode
    if (o.side === 'left' || o.side === 'right') d.side = o.side
  } catch { /* 解析失败用默认全屏背景 */ }
  return d
}

// parseBgStyle 将后端返回的样式 JSON 解析为归一化对象（缺省=充满居中，单图不平铺）
export function parseBgStyle(json?: string): BgStyle {
  const d: BgStyle = { scale: 1, x: 50, y: 50, mode: 'cover' }
  if (!json) return d
  try {
    const o = JSON.parse(json)
    if (typeof o.scale === 'number') d.scale = o.scale
    if (typeof o.x === 'number') d.x = o.x
    if (typeof o.y === 'number') d.y = o.y
    if (o.mode === 'cover' || o.mode === 'contain' || o.mode === 'tile') d.mode = o.mode
  } catch { /* 解析失败用默认充满 */ }
  return d
}

// 品牌上下文实例（默认值为 DEFAULT，确保未解析到租户时回退平台品牌）
const Ctx = createContext<Branding>(DEFAULT)

// 在组件树中读取当前品牌信息的 Hook
export const useBranding = () => useContext(Ctx)

// BrandBgLayer 按样式渲染未登录首页背景图层（单张图，铺满父容器）：
// - cover 充满（默认）：objectFit cover，scale 为缩放倍数
// - contain 适应：objectFit contain，scale 为缩放倍数
// 外层容器向四周外扩 5%（inset:-5%）并裁切，保证图片始终溢出屏幕边缘，
// 不会因图片自带白边或留白而在页面四周露出白色；底层填充深色避免任何缝隙显白。
export function BrandBgLayer({ src, styleJson }: { src: string; styleJson?: string }) {
  const s = parseBgStyle(styleJson)
  if (!src) return null
  return (
    <div style={{ position: 'absolute', inset: '-5%', zIndex: 0, backgroundColor: '#0d1b3e', overflow: 'hidden' }}>
      <img src={src} alt="" style={{
        position: 'absolute', left: `${s.x}%`, top: `${s.y}%`,
        transform: `translate(-50%, -50%) scale(${s.scale})`,
        width: '100%', height: '100%',
        objectFit: s.mode === 'contain' ? 'contain' : 'cover',
        userSelect: 'none', pointerEvents: 'none',
      }} />
    </div>
  )
}

// BrandingProvider 在挂载时按访问域名解析品牌。品牌只由「访问域名」决定：根域名永远为平台品牌
// （能言 LangCross），租户专属子域才显示该租户品牌；登录用户不改写站点品牌，避免根域名误显示租户品牌。
// 仅当显式传入 tenantId（超管在后台切换租户预览/编辑）时才按指定租户解析。
export function BrandingProvider({ tenantId, children }: { tenantId?: number; children: ReactNode }) {
  const [b, setB] = useState<Branding>(DEFAULT)
  // 解析优先级：显式 tenantId（超管预览）> 按访问域名（后端按 Host 解析，根域名=平台品牌）
  const effectiveTenantId = tenantId ?? 0
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
          brandHomeBg: j.brand_home_bg || '',
          brandHomeBgStyle: j.brand_home_bg_style || '',
          brandLoginCardPos: j.brand_login_card_pos || '',
          brandLoginLayout: j.brand_login_layout || '',
          code: j.code || '',
          industry: j.industry || '',
          industryName: j.industry_name || '',
          dedicatedRegister: !!j.dedicated_register,
        })
      })
      .catch(() => {})
    return () => { alive = false }
  }, [effectiveTenantId])
  // 网页标题随「租户品牌 / 租户名称」定制；全局根域名（未解析到具体租户）回退为平台名「能言 LangCross」
  useEffect(() => {
    const name = b.brandName || b.tenantName
    document.title = name ? `${name} 智能翻译平台` : DEFAULT_BRAND_NAME
  }, [b])
  return <Ctx.Provider value={b}>{children}</Ctx.Provider>
}
