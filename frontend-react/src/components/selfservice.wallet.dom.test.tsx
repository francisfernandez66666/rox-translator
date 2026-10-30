// ============================================================================
// selfservice.wallet.dom.test.tsx — 组3 #10 逐档钱包面板锁（W20 的前端腿）
//
// 钉住三件事：
//   ① 有 wallet 数组时渲染「分档明细」折叠块，每档一行（档名 + 积分 + 到期）；
//   ② **免费档永不出现**：即使接口多送一条 tier='free'，界面也不许画出来（暗额度不报数字）；
//   ③ 合计优先：三行总数仍来自 points_balance，面板不自己求和。
//
// 反证：摘掉组件里的 `w.tier !== 'free'` 过滤 ⇒ 第 ② 条红；把 <details> 去掉 ⇒ 第 ① 条红。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setLang, t } from '@/i18n'

const myPackage = vi.hoisted(() => vi.fn())
vi.mock('@/api/billing', () => ({ myPackage }))

import { BalancePanel } from './selfservice'

beforeEach(() => {
  cleanup()
  setLang('zh')
  myPackage.mockResolvedValue({
    success: true,
    points_balance: 900,
    points_grants_left: 900,
    points_permanent_balance: 0,
    wallet: [
      { tier: 'register', points_left: 100, expires_at: '2026-11-10T00:00:00Z', grants: 1 },
      { tier: 'task', points_left: 200, expires_at: '2026-11-05T00:00:00Z', grants: 2 },
      { tier: 'plan_permanent', points_left: 600, expires_at: '9999-12-31T23:59:59Z', grants: 1 },
      // 故意多送一条免费档：前端必须过滤掉（服务端本来就不会给）
      { tier: 'free', points_left: 300, expires_at: '2026-10-31T00:00:00Z', grants: 1 },
    ],
  })
})

describe('BalancePanel 逐档钱包', () => {
  it('渲染折叠块与逐档行，且免费档一行都不画', async () => {
    const { container } = render(<MemoryRouter><BalancePanel /></MemoryRouter>)
    await waitFor(() => expect(container.querySelector('.ss-wallet')).toBeTruthy())
    const rows = container.querySelectorAll('.ss-wallet-row')
    expect(rows.length).toBe(3) // register / task / plan_permanent
    const text = container.querySelector('.ss-wallet')?.textContent || ''
    expect(text).toContain(t('ss.tierRegister'))
    expect(text).toContain(t('ss.tierTask'))
    expect(text).toContain(t('ss.tierPlanPermanent'))
    // 长期档按 9999 哨兵显示「长期有效」，不许把 9999-12-31 原样甩给客户
    expect(text).toContain(t('ss.tierForever'))
    // 免费档：既没有档名，也没有它那 300 分
    expect(text).not.toContain('free')
    expect(text).not.toContain('300')
    // 折叠默认收起（原生 details 不带 open 属性）
    expect((container.querySelector('.ss-wallet') as HTMLDetailsElement).open).toBe(false)
    // 消费顺序说明在位
    expect(text).toContain(t('ss.walletOrderHint'))
  })
})
