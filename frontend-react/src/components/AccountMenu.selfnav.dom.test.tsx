// ============================================================================
// components/AccountMenu.selfnav.dom.test.tsx — 账号下拉自助入口断言（★ 2026-09-24 〇-S/#7）
// 背景：用户拍板「左侧汉堡退役，套餐/余额/账号并入右上角下拉」。本测试锁三件事：
//   ① selfNav 传入时下拉露出「我的套餐/我的余额/我的账号」，点击回调带正确路由；
//   ② 邀请有礼仅 showInvites=true（个人用户）可见——与 /invites 路由守卫同口径；
//   ③ 后台侧不传 selfNav 时一个自助项都不露（后台有自己的菜单，不混入前台入口）。
// 弹窗件（modals）与 meContext 全部替身：本测试只验菜单装配，不测改密/换绑链路。
// 注意：本仓 vitest 未开 globals，RTL 不自动 cleanup，必须显式 afterEach(cleanup)。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { ToastProvider } from '@/ui/langcross/src'

// 部分替身：只换 meContext，getAuthToken 等真实导出留给 stores/auth 初始化用
vi.mock('@/api', async (io) => ({
  ...(await io<typeof import('@/api')>()),
  meContext: vi.fn(async () => ({ success: true, email: 'a@b.c', job_role: '' })),
}))
vi.mock('@/components/modals', () => ({
  PasswordModal: () => null,
  EmailBindModal: () => null,
  DeactivateModal: () => null,
  JobRoleModal: () => null,
}))

import AccountMenu from '@/components/AccountMenu'

// 打开账号下拉：触发钮是组件自绘的 .am-trigger（ContextMenu 走 portal 挂 body，screen 查得到）
function openMenu(props: React.ComponentProps<typeof AccountMenu>) {
  render(
    <ToastProvider>
      <AccountMenu {...props} />
    </ToastProvider>,
  )
  fireEvent.click(document.querySelector('.am-trigger') as Element)
}

afterEach(() => cleanup())

describe('AccountMenu · 自助入口并入下拉（#7）', () => {
  it('selfNav + showInvites：套餐/余额/账号/邀请四项齐全且路由正确', () => {
    const nav = vi.fn()
    openMenu({ selfNav: nav, showInvites: true })
    // 菜单项 onSelect 后 ContextMenu 自动收起（closeOnSelect），逐项点击必须每次重开
    const trigger = document.querySelector('.am-trigger') as Element
    const clickItem = (label: string) => {
      fireEvent.click(trigger)
      fireEvent.click(screen.getByText(label))
    }
    clickItem('我的套餐')
    expect(nav).toHaveBeenLastCalledWith('/packages')
    clickItem('我的余额')
    expect(nav).toHaveBeenLastCalledWith('/billing')
    clickItem('我的邀请')
    expect(nav).toHaveBeenLastCalledWith('/invites')
    clickItem('我的账号')
    expect(nav).toHaveBeenLastCalledWith('/my')
  })

  it('企业用户（showInvites 缺省 false）：邀请有礼不露，其余三项照常', () => {
    const nav = vi.fn()
    openMenu({ selfNav: nav })
    expect(screen.queryByText('我的邀请')).toBeNull()
    expect(screen.getByText('我的套餐')).toBeTruthy()
    expect(screen.getByText('我的余额')).toBeTruthy()
    expect(screen.getByText('我的账号')).toBeTruthy()
  })

  it('后台侧（不传 selfNav）：一个自助项都不露，改密/换绑/退出仍在', () => {
    openMenu({ showWorkbench: true })
    expect(screen.queryByText('我的套餐')).toBeNull()
    expect(screen.queryByText('我的余额')).toBeNull()
    expect(screen.queryByText('我的账号')).toBeNull()
    expect(screen.getByText('修改密码')).toBeTruthy()
    // common.logout 的中文交付值是「退出」两字（非「退出登录」），断言按实际取词
    expect(screen.getByText('退出')).toBeTruthy()
  })
})
