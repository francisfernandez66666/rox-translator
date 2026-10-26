// ============================================================================
// e2e/landing_lead_captcha_320.spec.ts — P2-01 320 视口留资验证码不溢出（方案 v2.0 §5.3）
//
// 缺陷形态：官网留资表单的 Turnstile 容器 `.lc-lead-captcha` 只有 align-self，
//   没有宽度约束；normal 档固有宽 300px，而 320 视口下表单从 x=40 起 ⇒ 右缘 340，
//   页面出现横向滚动（375/390/414 都在视口内，所以只有 <340 复现）。
//
// 本用例锁两半（缺一半都不算修好）：
//   ① 尺寸选档：容器/视口放不下 300px 时 LeadForm 必须选 `compact`（本用例读 stub 记录的 size）；
//   ② CSS 兜底：即便选档失效，页面 scrollWidth 也不许超过 320，容器右缘不越界。
//
// 不碰真实 Cloudflare：用 addInitScript 注入 turnstile 替身（同 register_captcha_token.spec.ts），
// 并按档位渲染不同宽度的 iframe，这样"控件已渲染"是真条件——只量空容器的用例是空转。
// 只 mock 一个本仓接口 `/api/auth/register-config` 打开人机验证；不依赖任何外部域名。
// ============================================================================
import { test, expect, Page } from '@playwright/test'

const SITE_KEY = '0x4AAAA-uat-stub-key'

// stubTurnstile 注入 Turnstile 替身：render 按 size 档塞入对应宽度的 iframe，并记录最近一档。
function stubTurnstile(page: Page) {
  return page.addInitScript(() => {
    const w = window as unknown as {
      turnstile?: unknown
      __uatLeadTs?: { size: string; renders: number }
    }
    const st = { size: '', renders: 0 }
    w.__uatLeadTs = st
    w.turnstile = {
      render: (el: HTMLElement, opts: { size?: string }) => {
        st.renders += 1
        st.size = opts?.size || 'normal'
        const f = document.createElement('iframe')
        // 与 Cloudflare 的固有宽度同档：normal=300px、compact≈150px
        f.style.width = st.size === 'compact' ? '150px' : '300px'
        f.style.height = '65px'
        f.style.border = '0'
        f.setAttribute('data-uat-turnstile', st.size)
        el.appendChild(f)
        return 'uat-lead-widget'
      },
      remove: () => { /* resize 重挂时调用；本用例不跨档 */ },
      reset: () => {},
      execute: () => {},
    }
  })
}

// openLandingWithCaptcha 打开首页并让人机验证挂件真的渲染出来。
async function openLandingWithCaptcha(page: Page) {
  await page.route('**/api/auth/register-config', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, captcha_enabled: true, captcha_site_key: SITE_KEY }),
    }),
  )
  await stubTurnstile(page)
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await page.locator('.lc-lead-captcha iframe').first().waitFor({ state: 'attached', timeout: 20000 })
}

// overflowOf 量页面级横向溢出与容器右缘（判据按方案 §5.3）。
async function overflowOf(page: Page) {
  return page.evaluate(() => {
    const el = document.querySelector('.lc-lead-captcha') as HTMLElement | null
    const rect = el?.getBoundingClientRect()
    return {
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
      right: rect ? Math.round(rect.right * 10) / 10 : null,
      width: rect ? Math.round(rect.width * 10) / 10 : null,
      size: (document.querySelector('[data-uat-turnstile]') as HTMLElement | null)?.getAttribute('data-uat-turnstile') || null,
    }
  })
}

test.describe('P2-01 320 留资验证码', () => {
  test('320×640：选 compact，页面零横向溢出，容器不越界', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 640 })
    await openLandingWithCaptcha(page)
    const m = await overflowOf(page)
    // ① 尺寸选档：容器宽 <300 必须走 compact（写死 normal 时这条先红）
    expect(m.size).toBe('compact')
    // ② 页面级与容器级都不许越界（CSS 兜底失效时这两条红）
    expect(m.scrollWidth).toBeLessThanOrEqual(320)
    expect(m.right).not.toBeNull()
    expect(m.right as number).toBeLessThanOrEqual(320.5)
    expect(m.width as number).toBeLessThanOrEqual(320)
  })

  // 414 档：视口 ≥400 且容器放得下 300px，必须回到 normal——只钉 320 会把
  // 「一律 compact」这种过度修法放过去（小屏不溢出，但桌面档被无谓缩小）。
  test('414×896：normal 档仍可用且不溢出（不回归）', async ({ page }) => {
    await page.setViewportSize({ width: 414, height: 896 })
    await openLandingWithCaptcha(page)
    const m = await overflowOf(page)
    expect(m.size).toBe('normal')
    expect(m.scrollWidth).toBeLessThanOrEqual(414)
    expect(m.right as number).toBeLessThanOrEqual(414.5)
  })
})
