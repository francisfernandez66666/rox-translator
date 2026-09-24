// ============================================================================
// components/admin/PersonalCenterP.dom.test.tsx — 个人中心默认落页断言（★ 2026-09-24 〇-R/#10）
// 背景：用户投诉「个人中心的任务中心是默认不打开的」并下令「所有 tab 点击后都要默认打开一个页面」。
// 本测试锁死修复后的口径：进入个人中心即渲染第一个子页——
//   个人用户=邀请好友（ReferralP），企业/超管=任务中心（TaskCenterP），
//   且企业用户任何情况下不得渲染邀请面板（权限收口回归位）。
// 子面板用替身：只验「默认选中哪一页 + 渲染了没有」，两面板各自有独立测试。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'

vi.mock('@/components/admin/TaskCenterP', () => ({
  default: () => <div data-testid="task-center" />,
}))
vi.mock('@/components/admin/panels_c', () => ({
  ReferralP: () => <div data-testid="referral-center" />,
}))

import { useAdminStore } from '@/stores/admin'
import PersonalCenterP from '@/components/admin/PersonalCenterP'

describe('个人中心 · 默认打开一个子页（#10）', () => {
  beforeEach(() => {
    useAdminStore.setState({ isPersonal: false })
  })
  // 本仓 vitest 未开 globals，RTL 不会自动注册 afterEach(cleanup)；
  // 不清理会把上一个用例的 DOM 留到下一个断言里（曾致 task-center 跨用例假红）。
  afterEach(() => cleanup())
  it('企业用户：默认落在任务中心（不是空白页）', () => {
    render(<PersonalCenterP />)
    expect(screen.getByTestId('task-center')).toBeTruthy()
    expect(screen.queryByTestId('referral-center')).toBeNull()
  })
  it('个人用户：默认落在邀请好友（权限收口下唯一的首个 tab）', () => {
    useAdminStore.setState({ isPersonal: true })
    render(<PersonalCenterP />)
    expect(screen.getByTestId('referral-center')).toBeTruthy()
    expect(screen.queryByTestId('task-center')).toBeNull()
  })
})
