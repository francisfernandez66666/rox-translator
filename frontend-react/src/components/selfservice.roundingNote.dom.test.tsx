// ============================================================================
// selfservice.roundingNote.dom.test.tsx — P2-02 余额明细取整说明锁
//
// 背景（2026-10-25 云端 UAT）：/billing 的三个字段各自四舍五入，
// “永久余额 + 发放台账”相加可能与“可用总额”差 1。页面没有手动求和，
// 但用户会自己加；本锁钉住余额面板必须显示一句“明细取整、合计以可用总额为准”。
//
// 反证：把 BalancePanel 里的 .ss-note 摘掉 ⇒ 本用例红。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setLang, t } from '@/i18n'

// myPackage 由用例注入三桶数值；hoisted 保证 vi.mock 工厂能拿到。
const myPackage = vi.hoisted(() => vi.fn())
vi.mock('@/api/billing', () => ({ myPackage }))

import { BalancePanel } from './selfservice'

beforeEach(() => {
  cleanup()
  setLang('zh')
  myPackage.mockResolvedValue({
    success: true,
    points_balance: 10586,
    points_grants_left: 252,
    points_permanent_balance: 10333,
  })
})

// 一条：三行明细下方必须有取整说明（P2-02 的展示侧口径）。
describe('BalancePanel 取整说明', () => {
  it('三行明细下方显示“明细四舍五入，合计以可用总额为准”', async () => {
    const { container } = render(<MemoryRouter><BalancePanel /></MemoryRouter>)
    await waitFor(() => expect(container.textContent || '').toContain('10,586'))
    const note = container.querySelector('.ss-note')
    expect(note).toBeTruthy()
    expect(note?.textContent || '').toBe(t('ss.roundingNote'))
  })
})
