// ============================================================================
// branding.tsx — 租户级品牌定制上下文
// 职责：按访问域名/显式租户 ID 解析品牌展示信息（品牌名、Logo、登录页背景与布局、
// 首页背景图层、网页标题、聊天气泡配色等），通过 React Context 向全站下发。
// 品牌只由「访问域名」决定：根域名=平台品牌，租户专属子域=该租户品牌；
// 支持服务端在 index.html 注入 window.__BRANDING__ 以首屏即生效、避免闪烁。
// 2026-09-17 纯黑换肤：本文件只涉及「配色常量」（首页背景兜底色、气泡/面板 CSS 变量），
// 品牌解析与下发链路不变。
// ============================================================================

/**
 * branding.tsx · 职责说明
 * 租户级品牌定制 Context，提供以下功能：
 * - 品牌解析：按访问域名自动解析租户品牌信息
 * - 品牌应用：网页标题、聊天气泡配色、登录页背景等
 * - 背景图层：未登录首页背景图的渲染（支持 cover/contain/tile 模式）
 * - 布局配置：登录卡片位置、登录页布局（全屏/分栏）
 * - 首屏优化：支持服务端注入 window.__BRANDING__ 避免闪烁
 */

// 依赖引入：React 基础 Hooks（createContext/useContext/useEffect/useMemo/useState）与类型 ReactNode
import { useEffect, useMemo } from 'react'
import type { ReactNode } from 'react'
import { create } from 'zustand'
import { API_BASE } from '@/api'

// BrandLink 品牌页脚/导航链接条目：含中英文标签与跳转地址
export interface BrandLink {
   label: string    // 中文标签
   label_en: string // 英文标签
   url: string      // 跳转地址
}

// Branding 品牌定制完整结构：描述某租户的品牌名、Logo、登录/首页布局与注册场景等
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

// DEFAULT 默认品牌配置对象（未解析到租户时回退平台品牌）
const DEFAULT: Branding = {
  tenantId: 0, tenantName: '', brandName: '', brandLogo: '', domain: '',
  brandHomeBg: '', brandHomeBgStyle: '', brandLoginCardPos: '', brandLoginLayout: '', code: '', industry: '', industryName: '', dedicatedRegister: false,
}

// 背景图样式默认值：充满(cover) + 不缩放(scale=1) + 居中(x=50,y=50)
export interface BgStyle { scale: number; x: number; y: number; mode: 'tile' | 'cover' | 'contain' }

// 登录卡片位置默认值：居中（百分比为卡片中心相对视口）
export interface CardPos { x: number; y: number }
// parseCardPos 解析品牌配置 JSON 中的登录卡片位置（缺省居中 50/50）
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
// parseLoginLayout 解析品牌配置 JSON 中的登录页布局（全屏/分栏+卡片方位，缺省 full/right）
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

// ★ H8：品牌状态唯一来源（Zustand）。默认值=平台品牌，未解析到租户时回退；
// 组件树外（登录页标题同步等）也可读取。
export const useBrandingStore = create<Branding>()(() => ({ ...(brandingFromGlobal() ?? DEFAULT) }))

// 在组件树中读取当前品牌信息的 Hook（zustand 全量订阅，字段变化即重渲染）
export const useBranding = () => useBrandingStore()

// BrandBgLayer 按样式渲染未登录首页背景图层（单张图，铺满父容器）：
// - cover 充满（默认）：objectFit cover，scale 为缩放倍数
// - contain 适应：objectFit contain，scale 为缩放倍数
// 外层容器向四周外扩 5%（inset:-5%）并裁切，保证图片始终溢出屏幕边缘，
// 不会因图片自带白边或留白而在页面四周露出白色；底层填充深色避免任何缝隙显白。
export function BrandBgLayer({ src, styleJson }: { src: string; styleJson?: string }) {
  const s = parseBgStyle(styleJson)
  if (!src) return null
  return (
    // 兜底底色用近黑 #050607（旧版 #0d1b3e 是深蓝）：纯黑主题下图片四周若留白，
    // 露出的必须是同色系黑底，而不是闪出一圈蓝。
    <div style={{ position:'absolute', inset:'-5%', zIndex: 0, backgroundColor:'#050607', overflow:'hidden'}}>
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

// brandingFromGlobal 读取服务端在 index.html 注入的 window.__BRANDING__（按域名解析，首屏即用，避免闪烁）。
// 返回 null 表示未注入（如本地开发无注入），此时回退到 DEFAULT 并走异步拉取。
function brandingFromGlobal(): Branding | null {
  const g = (window as unknown as { __BRANDING__?: any }).__BRANDING__
  if (!g || !g.success) return null
  return {
    tenantId: g.tenant_id || 0,
    tenantName: g.name || '',
    brandName: g.brand_name || '',
    brandLogo: g.brand_logo || '',
    domain: g.domain || '',
    brandHomeBg: g.brand_home_bg || '',
    brandHomeBgStyle: g.brand_home_bg_style || '',
    brandLoginCardPos: g.brand_login_card_pos || '',
    brandLoginLayout: g.brand_login_layout || '',
    code: g.code || '',
    industry: g.industry || '',
    industryName: g.industry_name || '',
    dedicatedRegister: !!g.dedicated_register,
  }
}

// BrandingProvider 在挂载时按访问域名解析品牌。品牌只由「访问域名」决定：根域名永远为平台品牌
// （能言 LangCross），租户专属子域才显示该租户品牌；登录用户不改写站点品牌，避免根域名误显示租户品牌。
// 仅当显式传入 tenantId（超管在后台切换租户预览/编辑）时才按指定租户解析。
// 优化：若服务端已在首屏 index.html 注入 window.__BRANDING__（按 Host 解析），直接作为初值使用，
// 跳过一次首屏异步拉取，消除「先通用设计、后品牌设计」的闪烁。
export function BrandingProvider({ tenantId, children }: { tenantId?: number; children: ReactNode }) {
  // 首屏品牌初值：优先使用服务端注入（无闪烁），否则回退 DEFAULT；写入 zustand 单一状态源
  const initial = useMemo(() => brandingFromGlobal(), [])
  const b = useBrandingStore()
  const setB = (v: Branding) => useBrandingStore.setState(v)
  void b
  // 解析优先级：显式 tenantId（超管预览）> 按访问域名（后端按 Host 解析，根域名=平台品牌）
  const effectiveTenantId = tenantId ?? 0
  useEffect(() => {
    // 已注入且为按域名解析（非超管预览指定租户）：直接采用注入值，无需再拉取
    if (effectiveTenantId <= 0 && initial) return
    let alive = true
    const url = API_BASE + '/api/tenant/branding' + (effectiveTenantId ? `?tenant_id=${effectiveTenantId}` : '')
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
  // 注入工作台/聊天气泡配色（ChatWindow、MessageBubble 等引用的 CSS 变量）。
  // 品牌数据暂无独立主色字段，统一以平台主色派生，避免与 TDesign 令牌冲突；
  // 组件卸载时清除，避免多租户间变量泄漏。
  //
  // 2026-09-17 纯黑换肤：以下取值整体转暗——用户气泡=半透明白（rgba 231,233,234,.06）、
  // AI 气泡=面板黑 #0E1014、描边 #3A404C；主动发出的消息（msg-out）改为白底黑字，
  // 与交付包「正向=白」一致（原 #2f47f5 蓝底白字废止）。
  // ★ 2026-09-22 还原批：--text/--muted 两条漏网的历史浅底暗字值（#141b2d/#525c70，
  //   蓝调、只可能在白底上读）随本 palette 的暗色口径一并对齐纯黑真值
  //   （主文字 #E7E9EA / 三级灰 #71767B）。
  // ★ 2026-09-23 〇-P：上面 09-17 行写的是**当批**取值，现值已随后续批次改动——面档回到交付值
  //   #0E1014 / #16181C，描边回到交付灰阶档（〇-O 曾整体翻纯白，已撤销）。读注释时按「历史
  //   记录」理解，不要拿它当现行真值（现行真值以 tokens.css 与 UI-ANNOTATIONS §1.1/§1.3 为准）。
  // 注意：这批 --bubble-*/--bg/--panel/--text/--border/--muted/--msg-out-* 是历史
  // Vue 版配色的挂点，当前 React 代码已无 var() 消费方（气泡样式改由 theme.css 与
  // .lc-* 类决定），此处仅作为品牌可覆色的注入钩子保留。
  useEffect(() => {
    const root = document.documentElement
    const palette: Record<string, string> = {
      '--bubble-user-bg': 'rgba(231,233,234,0.06)',
      '--bubble-user-border': '#FFFFFF',
      '--bubble-ai-bg': '#0E1014',
      '--bubble-ai-border': '#FFFFFF',
      '--bg': '#0E1014',
      '--panel': '#0E1014',
      '--text': '#E7E9EA',
      '--border': '#FFFFFF',
      '--muted': '#71767B',
      '--msg-out-bg': '#E7E9EA',
      '--msg-out-color': '#000000',
    }
    Object.entries(palette).forEach(([k, v]) => root.style.setProperty(k, v))
    return () => { Object.keys(palette).forEach((k) => root.style.removeProperty(k)) }
  }, [])
  void b
  return <>{children}</>
}
