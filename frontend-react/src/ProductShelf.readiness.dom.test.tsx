// @vitest-environment jsdom
// ============================================================================
// ProductShelf.readiness.dom.test.tsx — 齐备度六格读数（施工_一期 §9.2）
//
// 六格渲染；「缺」与「已过期」文案/类名**不同形**（§2.3：missing 不落行、expired 落在行上，
// 两者若同形就是「什么都没出」与「有但过期」被读成一件事）；空态不出现 `null` 文本。
// 面板自身只做读数，数据由 @/api/product 的 productList/productReadiness 注入（此处 mock）。
// 运行：npx vitest run src/ProductShelf.readiness.dom.test.tsx
// ============================================================================
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/** 可编程的齐备度夹具（vi.hoisted 保证 mock 工厂在 import 之前就拿得到这个引用） */
const fixture = vi.hoisted(() => ({
  readiness: [] as Array<{ asset_type: string; lang: string; count: number; worst: string }>,
}))

vi.mock('@/api/product', () => ({
  productList: async () => ({ success: true, products: [{ id: 'p1' }] }),
  productReadiness: async () => ({ success: true, readiness: fixture.readiness }),
}))

import ProductShelf from './components/ProductShelf'

/** 六格夹具：valid／missing／expired 三态都要在（缺与过期不同形的可比靶子） */
const SIX = [
  { asset_type: 'tagline', lang: 'en', count: 1, worst: 'valid' },
  { asset_type: 'onepager', lang: 'en', count: 0, worst: 'missing' },
  { asset_type: 'quote_sheet', lang: 'en', count: 1, worst: 'expired' },
  { asset_type: 'image_card', lang: 'en', count: 2, worst: 'valid' },
  { asset_type: 'voice_script', lang: 'zh', count: 0, worst: 'missing' },
  { asset_type: 'qa_pack', lang: 'zh', count: 1, worst: 'expired' },
]

beforeEach(() => { fixture.readiness = [] })
afterEach(() => cleanup())

describe('齐备度六格读数（§9.2）', () => {
  it('六格逐格渲染（表头 1 + 数据 6 行）', async () => {
    fixture.readiness = SIX
    render(<ProductShelf />)
    await waitFor(() => expect(screen.getAllByRole('row').length).toBe(7))
    expect(screen.getAllByTestId(/^readiness-/)).toHaveLength(6)
  })

  it('「缺」与「已过期」文案不同形，且各自带独立 testid', async () => {
    fixture.readiness = SIX
    render(<ProductShelf />)
    await waitFor(() => expect(screen.getAllByTestId(/^readiness-/)).toHaveLength(6))
    // 六格里「缺」「已过期」各有两行（跨语种），取代表行比文案即可（不苛求唯一）
    const missing = screen.getAllByTestId('readiness-missing')[0].textContent || ''
    const expired = screen.getAllByTestId('readiness-expired')[0].textContent || ''
    expect(missing).toContain('缺')
    expect(expired).toContain('已过期')
    expect(missing).not.toBe(expired)
    expect(missing).not.toContain('已过期')
    expect(expired).not.toContain('缺')
  })

  it('空集出空态，且 DOM 里不出现 null 文本', async () => {
    fixture.readiness = []
    const { container } = render(<ProductShelf />)
    await waitFor(() => expect(container.textContent).toContain('暂无数据'))
    expect(container.textContent).not.toContain('null')
    expect(screen.queryAllByRole('row')).toHaveLength(0)
  })
})
