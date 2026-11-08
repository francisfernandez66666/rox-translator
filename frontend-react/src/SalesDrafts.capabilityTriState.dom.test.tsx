// @vitest-environment jsdom
// ============================================================================
// SalesDrafts.capabilityTriState.dom.test.tsx — 二期 §9.4 锁 3：能力三态各不同形
//
// 判据：off/half/on 三态渲染**互不相同**的事实文案（半套配置不许呈现为已生效，
// 与后端 salesSwitchStatus 的 B2 同口径）；探针失败/未回（null）fail-open——
// 不渲染能力行但页面照常可用（一次抖动不许钉成「这个站永远没有销售引擎」）。
// 运行：npx vitest run src/SalesDrafts.capabilityTriState.dom.test.tsx
// ============================================================================
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/** 接口层替身：四个调用各自独立计数，三态用例只改 salesCapability 的返回值 */
const api = vi.hoisted(() => ({
  salesDrafts: vi.fn(),
  salesDraft: vi.fn(),
  salesDraftApprove: vi.fn(),
  salesCapability: vi.fn(),
}))
vi.mock('@/api/sales', () => ({
  salesDrafts: api.salesDrafts,
  salesDraft: api.salesDraft,
  salesDraftApprove: api.salesDraftApprove,
  salesCapability: api.salesCapability,
}))

import SalesDrafts from './components/SalesDrafts'

afterEach(() => cleanup())
beforeEach(() => {
  vi.clearAllMocks()
  api.salesDrafts.mockResolvedValue({ success: true, drafts: [] })
})

describe('能力三态各不同形＋fail-open（§9.4 锁 3）', () => {
  it('off/half/on 三态文案互不相同（半套配置不许读成已生效）', async () => {
    const seen = new Set<string>()
    for (const state of ['off', 'half', 'on']) {
      cleanup()
      api.salesCapability.mockResolvedValue({ success: true, status: state })
      render(<SalesDrafts />)
      await waitFor(() => expect(screen.getByTestId('sales-capability-state')).toBeTruthy())
      const row = screen.getByTestId('sales-capability-state')
      const text = row.textContent || ''
      expect(text.length, `${state} 那档文案为空`).toBeGreaterThan(0)
      // ★ 防结构性假绿：若词典没加载，t() 会直出三个互不相同但**未翻译**的键，
      //   单靠「三态文案互不相同」会一路绿灯。这里必须同时证伪「直出词典键」。
      expect(text, '三态文案不许把未翻译的词典键直出给卖家').not.toContain('sales.capability.')
      expect(row.getAttribute('data-state'), `${state} 那档的状态词必须与后端读数等值`).toBe(state)
      seen.add(text)
    }
    expect(seen.size, '三态必须互不相同（off/half/on 各不同形）').toBe(3)
  })

  it('探针失败 fail-open：不渲染能力行，但草稿列表照常可用', async () => {
    api.salesCapability.mockRejectedValue(new Error('network'))
    render(<SalesDrafts />)
    await waitFor(() => expect(screen.getByTestId('sales-drafts-list')).toBeTruthy())
    expect(screen.queryByTestId('sales-capability-state'), '探不到时不许钉死一个状态').toBeNull()
    // 页面主体仍在（空态文案可见）
    expect(screen.getByTestId('sales-drafts-list').textContent || '').toContain('还没有待审说法')
  })
})