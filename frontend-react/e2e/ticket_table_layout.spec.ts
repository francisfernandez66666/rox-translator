import { expect, test } from '@playwright/test'

// 超长工单名用于验证中间省略与移动端展开。
const longTitle = 'ROX LEAD-AI 项目立项与实施方案 0901 副本_' + '超长工单名称'.repeat(12) + '.md'

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    sessionStorage.setItem('auth_token', 'e2e-layout-token')
    localStorage.setItem('app_lang', 'zh')
  })
  await page.route('**/api/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    if (!path.startsWith('/api/')) {
      await route.continue()
      return
    }
    if (path === '/api/tickets') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ success: true, tickets: [{
          id: 1, ticket_no: 'T20261124162536Z2G', title: longTitle, status: 'completed',
          target_langs: 'en,ru,ar', created_at: '2026-11-24T16:25:00+08:00',
        }] }),
      })
      return
    }
    if (path === '/api/auth/me') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ success: true, token: 'e2e-layout-token', user: {
          id: 1, username: 'e2e', display_name: 'E2E', role: 'user', tenant_id: 1,
          email: 'e2e@example.com', must_change_pwd: 0, profile_setup_required: 0,
        }, identities: [] }),
      })
      return
    }
    if (path === '/api/auth/identities') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true, identities: [] }) })
      return
    }
    if (path === '/api/me/context') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({
        success: true, email: 'e2e@example.com', tenant_id: 1, is_personal: true, instant_tier: 'paid',
      }) })
      return
    }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({ success: true }) })
  })
})

test('桌面操作列固定右侧，名称中间省略且悬停可见全文', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/tickets')
  const name = page.getByTestId('tk-ticket-name')
  await expect(name).toBeVisible()
  await expect(name).toHaveAttribute('title', longTitle)
  await expect(name).toContainText('…')

  const row = name.locator('xpath=ancestor::tr')
  const op = row.locator('td').nth(6)
  const geometry = await page.locator('.tk-table').evaluate((wrap) => {
    const table = wrap.querySelector('table')!
    const opCell = table.querySelector('tbody tr td:last-child') as HTMLElement
    const a = opCell.getBoundingClientRect()
    const b = wrap.getBoundingClientRect()
    return { opWidth: a.width, opRight: a.right, wrapRight: b.right, scroll: table.scrollWidth, client: wrap.clientWidth }
  })
  expect(geometry.opWidth).toBeGreaterThanOrEqual(270)
  expect(geometry.opRight).toBeLessThanOrEqual(geometry.wrapRight + 1)
  expect(geometry.scroll).toBeGreaterThan(geometry.client)
  await expect(op.getByRole('button').first()).toBeVisible()
})

test('RTL 桌面操作列粘在逻辑行尾左侧', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto('/tickets')
  await page.evaluate(() => { document.documentElement.dir = 'rtl' })
  const name = page.getByTestId('tk-ticket-name')
  await expect(name).toBeVisible()
  const geometry = await page.locator('.tk-table').evaluate((wrap) => {
    const table = wrap.querySelector('table')!
    const opCell = table.querySelector('tbody tr td:last-child') as HTMLElement
    const a = opCell.getBoundingClientRect()
    const b = wrap.getBoundingClientRect()
    return { opLeft: a.left, opRight: a.right, wrapLeft: b.left, wrapRight: b.right, position: getComputedStyle(opCell).position, dir: document.documentElement.dir, direction: getComputedStyle(opCell).direction }
  })
  expect(geometry.position).toBe('sticky')
  expect(Math.abs(geometry.opLeft - geometry.wrapLeft), JSON.stringify(geometry)).toBeLessThanOrEqual(1)
  expect(geometry.opRight).toBeLessThanOrEqual(geometry.wrapRight + 1)
})

test('移动端点击名称展开全文，操作按钮保持可见', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/tickets')
  const name = page.getByTestId('tk-ticket-name')
  await expect(name).toBeVisible()
  await name.click()
  await expect(name).toHaveAttribute('aria-expanded', 'true')
  await expect(name).toHaveText(longTitle)
  const row = name.locator('xpath=ancestor::tr')
  await expect(row.locator('td').nth(6).getByRole('button').first()).toBeVisible()
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
})
