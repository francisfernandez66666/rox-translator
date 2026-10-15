// ============================================================================
// components/BrandTagline.dom.test.tsx — 页脚品宣尾注的**运行时**腿
// （2026-10-13 用户令②；静态五面覆盖锁见 BrandTagline.lock.test.ts）
//
// 为什么静态锁之外还要这一份：BrandTagline.lock.test.ts 读的是**源码文本**，
// 它判"这五份文件里引用了组件"，判不了"界面真的渲染出这句话、而且十二个语种渲染的是同一句"。
// 用户这条令的重点恰在后一半（品宣：访客无论用哪种界面语言都得认得同一个牌子），
// 而"不分语种"只有把 12 个语种逐个渲染出来才算证明——源码里写没写词典键是另一件事。
//
// ★ 词典懒加载配套（2026-10-12 批①之后所有 jsdom 界面文字锁都要做）：
//   本文件④那条"界面其它文案确实翻了"的对照腿，在词典未到手时会回落到英文，
//   于是「唯独尾注是英文」这件事看起来成立、实际是空转 ⇒ 先 ensureLocale 把十二语种取到手。
//
// 运行：npx vitest run src/components/BrandTagline.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeAll, beforeEach, afterEach, vi } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { setLang, translateIn, ensureLocale, LANG_OPTIONS, type Lang } from '@/i18n'
import { BRAND_TAGLINE, BrandTagline, TAGLINE_STYLE, TAGLINE_DOT_STYLE } from './BrandTagline'

// SiteFooter 的唯一外部数据来源是平台级页脚链接配置；给一个可控替身，
// 免得这条锁的形态跟着超管配没配链接而变（这里判的是尾注，不是链接）。
const footerLinksGet = vi.fn()
vi.mock('@/api/branding', () => ({
  footerLinksGet: () => footerLinksGet(),
}))

import SiteFooter from './SiteFooter'

const taglineEl = () => document.querySelector('[data-testid="brand-tagline"]') as HTMLElement | null

beforeAll(async () => {
  await Promise.all(LANG_OPTIONS.map((o) => ensureLocale(o.code as Lang)))
})

beforeEach(() => {
  footerLinksGet.mockReset().mockResolvedValue({ success: true, links: [] })
})

afterEach(() => {
  cleanup()
  setLang('zh') // 复位，别把非中文态漏给后续用例（同 LangSelect.dom.test.tsx 口径）
})

describe('页脚品宣尾注（运行时）', () => {
  it('① 组件单独渲染：文字逐字等于常量、且带 dir=ltr 与 isolate（RTL 界面里不被双向算法重排）', () => {
    render(<BrandTagline />)
    const el = taglineEl()
    expect(el, 'BrandTagline 没渲染出可定位的节点').toBeTruthy()
    expect(el!.textContent).toBe(BRAND_TAGLINE)
    expect(el!.getAttribute('dir')).toBe('ltr')
    expect(el!.style.unicodeBidi, '阿语界面里这句必须隔离').toBe('isolate')
  })

  it('② 全站页脚里真的渲染出这句，并紧跟版权行（不是只写在源码里）', async () => {
    setLang('zh')
    render(<SiteFooter />)
    await waitFor(() => expect(taglineEl()).toBeTruthy())
    const el = taglineEl()!
    expect(el.textContent).toBe(BRAND_TAGLINE)
    // 位置判据：挂在 footer 上、且不在行首（前面必须有版权那一段），
    // 用户要的是"页脚位置写一条"，渲染成页眉/独立浮层都算跑偏。
    const footer = document.querySelector('footer')!
    expect(el.parentElement, '尾注应直接挂在 footer 上').toBe(footer)
    expect(Array.from(footer.children).indexOf(el), '尾注应排在版权行之后').toBeGreaterThan(0)
  })

  it('③ 12 语种逐个渲染：同一句、逐字相同（品宣位**刻意不本地化**，也不随租户改名）', async () => {
    const seen: string[] = []
    for (const { code } of LANG_OPTIONS) {
      setLang(code as Lang)
      render(<SiteFooter />)
      await waitFor(() => expect(taglineEl()).toBeTruthy())
      const el = taglineEl()!
      expect(el.textContent, `${code} 界面下这句被改动过（不许本地化）`).toBe(BRAND_TAGLINE)
      seen.push(el.textContent as string)
      cleanup()
    }
    // 覆盖面正锁：12 个语种一个都没漏（清单被删短就红，同静态锁 A 段手法）
    expect(seen.length).toBe(12)
    expect(new Set(seen).size, '十二语种渲染出的必须是同一个字符串').toBe(1)
  })

  it('④ 对照腿：同一屏上**别的**页脚文案确实随语种变了（证明③的"同一句"不是整站没本地化的空转）', async () => {
    const zhManual = translateIn('zh', 'footer.manual')
    const arManual = translateIn('ar', 'footer.manual')
    expect(zhManual, '中文界面该取到中文手册名').toBe('产品手册')
    // 阿语档必须与中文档不同——若词典没到手/回落链生效，这里会相等，③ 就成了假绿
    expect(arManual, '阿语词典未到手（回落让对照腿空转）').not.toBe(zhManual)
    // 而尾注在这两档下都仍是那串固定英文：界面本地化了，这一行**故意不跟着走**
    expect(BRAND_TAGLINE).not.toBe(arManual)
    expect(BRAND_TAGLINE.includes('能言'), '汉字品牌名不许混进这串标识').toBe(false)
  })

  // ★ 2026-10-15 用户后令「plain muted text + tiny dot」⇒ 运行时这一档也要有脸。
  //   静态 G 段比的是"源码里那十几维等不等值"，它判不了**这一身有没有真的落到渲染出的那个节点上**
  //   （样式挪到外层 wrapper、被父级 style 覆盖、组件被换成裸 span 而样式留在常量里，源码侧都照样绿）。
  it('⑤ 灰字+圆点那一档真的落在渲染出的节点上（容器六维 + 圆点六维逐个点名，且不再是白底胶囊）', async () => {
    setLang('zh')
    render(<SiteFooter />)
    await waitFor(() => expect(taglineEl()).toBeTruthy())
    const el = taglineEl()!
    for (const [k, v] of Object.entries(TAGLINE_STYLE as Record<string, string | number>)) {
      // jsdom 保留 var(...) 原样（实测 el.style.color === 'var(--lc-text-3)'），所以这里按**值**钉。
      expect(String((el.style as unknown as Record<string, unknown>)[k] ?? ''), `${k} 没落到页脚那个节点上（值 ${JSON.stringify(String(v))} 丢了）`).toBe(String(v))
    }
    // 交付真值点名（等值锁）：页脚 meta 灰字档，10-15 起**不再有**白底反相胶囊那一档。
    expect(el.style.color, '尾注字色必须是页脚 meta 灰字档').toBe('var(--lc-text-3)')
    expect(el.style.background, '尾注不许再有底色（用户把白底胶囊判成"像按钮"）').toBe('')
    expect(el.style.borderRadius, '尾注不许再有胶囊圆角').toBe('')
    expect(el.style.padding, '尾注不许再有胶囊内垫').toBe('')
    // ★ 字号／行高不设：继承所在页脚那一行（全站 15px／报价·对照 14px），统一抬档属未授权的规格改动。
    expect(el.style.fontSize, '尾注不许自带字号档').toBe('')
    expect(el.style.lineHeight, '尾注不许自带行高档').toBe('')
    // 圆点：必须真的渲染出来、带上 TAGLINE_DOT_STYLE 那一身，且是纯装饰（aria-hidden）。
    const dot = document.querySelector('[data-testid="brand-tagline-dot"]') as HTMLElement | null
    expect(dot, '文字前那枚小圆点没渲染出来（用户要的 tiny dot 丢了）').toBeTruthy()
    expect(dot!.getAttribute('aria-hidden'), '圆点是纯装饰，必须 aria-hidden').toBe('true')
    for (const [k, v] of Object.entries(TAGLINE_DOT_STYLE as Record<string, string | number>)) {
      // jsdom 的 CSSOM 把颜色关键字小写化（currentColor → currentcolor），所以按小写比。
      expect(String((dot!.style as unknown as Record<string, unknown>)[k] ?? '').toLowerCase(), `圆点 ${k} 没落到节点上`).toBe(String(v).toLowerCase())
    }
    expect(dot!.style.background.toLowerCase(), '圆点取 currentColor 跟字色走').toBe('currentcolor')
    expect(dot!.style.width).toBe('5px')
    // 反证对照：若有人把圆点做成会占文字（写进 textContent），① 那条逐字比对会红。
    expect(el.textContent, '圆点不许进 textContent（品宣串必须逐字可辨）').toBe(BRAND_TAGLINE)
  })
})
