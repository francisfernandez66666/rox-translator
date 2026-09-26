// ============================================================================
// branding.tsx — 租户级品牌定制上下文
// 职责：按访问域名/显式租户 ID 解析品牌展示信息（品牌名、Logo、登录页背景与布局、
// 登录卡片位置、网页标题、聊天气泡配色等），通过 zustand 单一状态源向全站下发。
// 品牌只由「访问域名」决定：根域名=平台品牌，租户专属子域=该租户品牌；
// 支持服务端在 index.html 注入 window.__BRANDING__ 以首屏即生效、避免闪烁。
// 2026-09-17 纯黑换肤：本文件当时只涉及「配色常量」（品牌背景兜底色、气泡/面板 CSS 变量），
// 品牌解析与下发链路不变。
// ============================================================================

/**
 * branding.tsx · 职责说明
 * 租户级品牌定制 Context，提供以下功能：
 * - 品牌解析：按访问域名自动解析租户品牌信息
 * - 品牌应用：网页标题、聊天气泡配色、登录页背景等
 * - 背景图层：登录/注册页品牌背景的渲染（cover/contain 模式，见 BrandLoginShell／F-71）
 * - 布局配置：登录卡片位置、登录页布局（全屏/分栏）
 * - 首屏优化：支持服务端注入 window.__BRANDING__ 避免闪烁
 */

// 依赖引入：React 基础 Hooks（createContext/useContext/useEffect/useMemo/useState）与类型 ReactNode
import { useEffect, useMemo, useRef } from 'react'
import type { ReactNode } from 'react'
import { create } from 'zustand'
import { API_BASE } from '@/api'
import { tpl, useLang } from '@/i18n' // ★ 2026-09-24：标题后缀按界面语言取词

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
  brandHomeBg: string   // 登录/注册页背景图（★ 字段名沿用历史 `brand_home_bg`，实际消费面是登录壳，
                        //   见下方 BrandLoginShell 的 F-71 注释；★ F-46 批 I-9 起**只会是图片地址**：
                        //   http(s) 或 /brand/<名>，历史 dataURI 已在服务端读侧收敛成 URL；空=不渲染背景）
  brandHomeBgStyle: string // 背景图样式 JSON（{scale,x,y,mode}：mode=tile/cover/contain；消费方＝BrandLoginShell）
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

// BrandBgLayer 按样式渲染品牌背景图层（单张图，铺满父容器；★ F-71 起真实消费方是登录壳
// BrandLoginShell 与后台 BrandP 预览，父容器需自带 position 与 overflow:hidden）：
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

// ============================================================================
// ★ F-71 实装（2026-09-27 用户令「品牌背景图前端零消费要实装」）
// ----------------------------------------------------------------------------
// 背景：`brand_home_bg` / `brand_home_bg_style` / `brand_login_card_pos` /
// `brand_login_layout` 四个品牌字段自 Vue 版迁移过来后，前端**零消费方**——只有管理后台
// BrandP 的本地预览在用（预览里写的是「预览所见 ≈ 登录页实际观感」），租户在后台传了图、
// 调了布局，登录页实际渲染却仍是纯黑居中，属于「后台功能可配、线上不生效」。
// 消费面判定：后台词典把该字段定义为**登录页背景图**（`brand.homeBg`＝「登录页背景图」、
// `brand.homeBgHint`＝「登录页（含注册页）背景图」），且另两个字段就叫「登录页布局 / 登录卡片位置」，
// ⇒ 真实消费方是**登录/注册/找回密码屏（Login.tsx 四屏）**，不是营销首页 Landing。
// 故本组件只挂在 Login 上：不改公开落地页外观（〇-P 交付档与 pixel/e2e 锁全部不受影响）。
// 形态严格对齐 BrandP 预览：
//   · 无背景图（平台根域名恒为此态）→ 完全不介入，DOM 与改造前逐字节一致；
//   · full（默认）→ 背景图层 + `rgba(0,0,0,0.42)` 遮罩 + 卡片按 cardPos 定位；
//   · split → 一侧背景图、一侧登录容器（容器底 rgba(231,233,234,0.06)，左右随 side 切换），
//     卡片位置百分比相对**登录容器**（与预览的 splitFormRef 量纲一致）；
//   · 窄屏（≤860px）分栏回落成「纯黑居中」，避免两栏各 430px 把 400 宽认证卡挤破。
// 定位实现用「负外边距」而不是 `transform: translate(-50%,-50%)`：祖先带 transform 会成为
// fixed 后代的包含块，历史上这类改法会把弹窗/下拉的定位基准搬走，这里从源头避开。
// ============================================================================

// CSS_BRAND_LOGIN 品牌登录壳样式（仅在有背景图时随壳一起注入，默认形态一个字节都不加）
const CSS_BRAND_LOGIN = `
html .lc-auth-bg.lc-brand-login{display:flex;overflow:hidden;padding:0;}
.lc-brand-login__veil{position:absolute;inset:0;z-index:1;background:rgba(0,0,0,0.42);}
.lc-brand-login__pane{position:relative;z-index:1;flex:1 1 0;min-width:0;align-self:stretch;overflow:hidden;}
.lc-brand-login__pane--form{display:flex;align-items:center;justify-content:center;background:rgba(231,233,234,0.06);}
.lc-brand-login__card{position:absolute;z-index:2;left:clamp(min(var(--lc-brand-half-w,200px),50%),var(--lc-brand-x,50%),max(50%,calc(100% - var(--lc-brand-half-w,200px))));top:clamp(min(var(--lc-brand-half-h,220px),50%),var(--lc-brand-y,50%),max(50%,calc(100% - var(--lc-brand-half-h,220px))));margin-left:calc(0px - var(--lc-brand-half-w,200px));margin-top:calc(0px - var(--lc-brand-half-h,220px));max-width:calc(100% - 32px);}
@media (max-width:860px){
  .lc-brand-login--split .lc-brand-login__pane--bg{display:none;}
  .lc-brand-login--split .lc-brand-login__pane--form{background:#000000;}
}
`

// brandCardPosVars 把卡片中心百分比写成 CSS 自定义属性（供 clamp 定位用）
function brandCardPosVars(pos: CardPos): Record<string, string> {
  return { '--lc-brand-x': `${pos.x}%`, '--lc-brand-y': `${pos.y}%` }
}

// BrandLoginShell 登录/注册壳：按品牌配置渲染背景图层与卡片位置。
// children = 该屏的卡片（AuthCard / AI 注册面板）+ 屏内样式，与改造前的 .lc-auth-bg 内容一致。
export function BrandLoginShell({ children }: { children: ReactNode }) {
  const b = useBranding()
  const bg = b.brandHomeBg || ''
  const layout = parseLoginLayout(b.brandLoginLayout)
  const pos = parseCardPos(b.brandLoginCardPos)
  const cardRef = useRef<HTMLDivElement>(null)
  // 半宽/半高实测写回 CSS 变量：clamp() 需要「卡片自身的一半」才能把中心点夹在可视区内
  // （租户把 x 拖到 5% 时，400 宽的卡片不该有半张甩到屏幕外）。用 DOM 直写而非 state，
  // 避免 ResizeObserver 回调 setState 再触发一轮布局的抖动。
  useEffect(() => {
    const el = cardRef.current
    if (!bg || !el || typeof ResizeObserver === 'undefined') return
    const sync = () => {
      el.style.setProperty('--lc-brand-half-w', `${Math.round(el.offsetWidth / 2)}px`)
      el.style.setProperty('--lc-brand-half-h', `${Math.round(el.offsetHeight / 2)}px`)
    }
    sync()
    const ro = new ResizeObserver(sync)
    ro.observe(el)
    return () => ro.disconnect()
  }, [bg, layout.mode])
  // 无背景图＝平台默认形态：不加类、不加样式节点，保证公开/平台登录页与交付稿逐字节一致
  if (!bg) return <div className="lc-auth-bg">{children}</div>
  const card = (
    <div ref={cardRef} className="lc-brand-login__card" style={brandCardPosVars(pos)}>
      {children}
    </div>
  )
  if (layout.mode === 'split') {
    const bgPane = (
      <div className="lc-brand-login__pane lc-brand-login__pane--bg">
        <BrandBgLayer src={bg} styleJson={b.brandHomeBgStyle} />
      </div>
    )
    const formPane = (
      <div className="lc-brand-login__pane lc-brand-login__pane--form">
        {card}
      </div>
    )
    return (
      <div className="lc-auth-bg lc-brand-login lc-brand-login--split" data-brand-login="split" data-brand-side={layout.side} style={layout.side === 'left' ? { flexDirection: 'row-reverse' } : undefined}>
        <style>{CSS_BRAND_LOGIN}</style>
        {bgPane}
        {formPane}
      </div>
    )
  }
  return (
    <div className="lc-auth-bg lc-brand-login lc-brand-login--full" data-brand-login="full">
      <style>{CSS_BRAND_LOGIN}</style>
      <BrandBgLayer src={bg} styleJson={b.brandHomeBgStyle} />
      <div className="lc-brand-login__veil" />
      {card}
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
  const lang = useLang() // 界面语言（标题取词用；切语言即时重算 document.title）
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
  // ★ 2026-09-24 后台去写死中文：「智能翻译平台」后缀不再硬编码，走 app.brandTitle 键按界面语言取词；
  //   lang 进依赖，切语言即时刷新标题（此前只随品牌数据变化）
  useEffect(() => {
    const name = b.brandName || b.tenantName
    document.title = name ? tpl('app.brandTitle', { name }) : DEFAULT_BRAND_NAME
  }, [b, lang])
  // ★ 2026-09-24 全站 logo 统一（#8）：favicon 跟随品牌解析结果——
  //   有独立品牌 Logo 的租户（brandLogo 非空，base64 或 URL）用自家 Logo；
  //   平台根域名与未配置 Logo 的租户用 /logo.svg（首页顶栏标识同源）。
  //   index.html 已默认挂 /logo.svg，这里只在 brandLogo 变化时改写 link 节点。
  useEffect(() => {
    let link = document.querySelector<HTMLLinkElement>("link[rel~='icon']")
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      document.head.appendChild(link)
    }
    link.href = b.brandLogo || '/logo.svg'
  }, [b.brandLogo])
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
