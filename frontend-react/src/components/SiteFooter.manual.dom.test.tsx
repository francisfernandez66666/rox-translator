// ============================================================================
// SiteFooter.manual.dom.test.tsx — ★ D-4 页脚《产品手册》入口回归（2026-09-29）
// 缺陷因果链：12 语种手册 PDF 的后端面（GET /docs/manual/{语种码}.pdf，批 I-8/F-69）与注册邮件
//   附件一直在用，但**界面零入口**——客户在站上找不到手册，只能偶然点到邮件里那条链接。
//   这条链最容易被后续改动弄坏的有两点，本用例各锁一条：
//   ① href 必须随当前界面语种变化（写死 zh 或写死 en 都是对外错报），
//      且内部码 zh_hant 要映射成公开码 zh-hant（后端 docs_manual.go 的白名单是连字符写法，
//      带下划线的内部码会被判「不支持的语种」400）；
//   ② 手册入口必须**无条件渲染**：页脚链接（footer_links）一旦配置就会整块替换默认协议入口，
//      手册若挂在那条分支里，超管配一次链接就把产品手册入口抹掉了。
// 回落链（精确语种 → en → zh）由后端 loadManualPDF 判定，前端不复制一份——
//   所以这里不断言前端有回落逻辑，只断言它**老实带上当前语种码**交给后端判。
// 运行：npx vitest run src/components/SiteFooter.manual.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { setLang, t } from '@/i18n'

// branding 的页脚链接是唯一的外部数据来源：用可控替身分别喂「空」与「已配置」两种形态
const footerLinksGet = vi.fn()
vi.mock('@/api/branding', () => ({
  footerLinksGet: () => footerLinksGet(),
}))

import SiteFooter from './SiteFooter'

const anchors = () => Array.from(document.querySelectorAll('footer a')) as HTMLAnchorElement[]
const manual = () => anchors().find((a) => a.getAttribute('href')?.startsWith('/docs/manual/'))

beforeEach(() => {
  cleanup()
  footerLinksGet.mockReset().mockResolvedValue({ success: true, links: [] })
})

describe('SiteFooter 手册入口', () => {
  it('① 中文界面：href 指向 /docs/manual/zh.pdf 且取 footer.manual 文案', async () => {
    setLang('zh')
    render(<SiteFooter />)
    await waitFor(() => expect(anchors().length).toBeGreaterThan(0))
    const a = manual()
    expect(a, '页脚必须有手册入口（后端能力早已存在，缺的是界面入口）').toBeTruthy()
    expect(a!.getAttribute('href')).toBe('/docs/manual/zh.pdf')
    expect(a!.textContent).toBe(t('footer.manual'))
    // 空白新标签打开的外链一律补 rel=noreferrer（与协议链接同口径，不漏 Referer）
    expect(a!.getAttribute('rel')).toBe('noreferrer')
  })

  it('② 语种切换：href 随界面语种走（写死任何一档都是对外错报）', async () => {
    setLang('ja')
    const { rerender } = render(<SiteFooter />)
    await waitFor(() => expect(manual()).toBeTruthy())
    expect(manual()!.getAttribute('href')).toBe('/docs/manual/ja.pdf')
    setLang('en')
    rerender(<SiteFooter />)
    await waitFor(() => expect(manual()!.getAttribute('href')).toBe('/docs/manual/en.pdf'))
  })

  it('③ 内部码 zh_hant 必须翻成公开码 zh-hant（带下划线后端会判 400）', async () => {
    setLang('zh_hant')
    render(<SiteFooter />)
    await waitFor(() => expect(manual()).toBeTruthy())
    expect(manual()!.getAttribute('href')).toBe('/docs/manual/zh-hant.pdf')
    expect(manual()!.getAttribute('href')).not.toContain('_')
  })

  it('④ 超管配置了 footer_links 时，手册入口照旧在（不随默认协议分支被替换掉）', async () => {
    footerLinksGet.mockResolvedValue({
      success: true,
      links: [{ label: '服务条款', label_en: 'Terms', url: 'https://example.com/terms' }],
    })
    setLang('zh')
    render(<SiteFooter />)
    await waitFor(() => expect(anchors().map((a) => a.getAttribute('href'))).toContain('https://example.com/terms'))
    expect(manual(), 'footer_links 只替换协议入口，不许顺带抹掉产品手册').toBeTruthy()
    // 反向对照：配置的链接确实顶掉了默认《用户协议》《隐私协议》两条入口
    const hrefs = anchors().map((a) => a.getAttribute('href'))
    expect(hrefs).not.toContain('/docs/terms')
    expect(hrefs).not.toContain('/docs/privacy')
  })

  it('⑤ 页脚链接拉取失败（后端异常）时手册入口不受影响', async () => {
    footerLinksGet.mockRejectedValue(new Error('network down'))
    setLang('zh')
    render(<SiteFooter />)
    await waitFor(() => expect(manual()).toBeTruthy())
    expect(manual()!.getAttribute('href')).toBe('/docs/manual/zh.pdf')
  })
})
