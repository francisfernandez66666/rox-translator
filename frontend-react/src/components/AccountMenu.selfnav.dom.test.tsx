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

// ★ 2026-10-15 用户令：① 通知铃铛退役，改以「通知中心」菜单项收进本下拉（点开弹浮层）；
//   ② 侧栏「我的账号」项删除，入口收进本下拉（onGotoMy）。两条都只在前台档露出。
describe('AccountMenu · 通知中心与我的账号（★ 2026-10-15 用户令）', () => {
  it('showNotify：菜单露出「通知中心」，点开渲染通知浮层（复用 Bell 的 NotifyPanel）', () => {
    openMenu({ showNotify: true })
    expect(screen.getByText('通知中心')).toBeTruthy()
    fireEvent.click(screen.getByText('通知中心'))
    // 浮层落点＝.am-notice 内（开在触发器上沿，账号菜单贴左栏底缘不能向下开）
    expect(document.querySelector('.am-notice .bell-panel')).toBeTruthy()
    expect(screen.getByText('全部已读')).toBeTruthy()
    expect(screen.getByText('暂无通知')).toBeTruthy()
  })

  // ★ 2026-10-17 用户报「打开通知中心会强制左边菜单栏拖动」：旧形态把浮层 position:absolute +
  //   写死 300px 静态留在左栏内，而左栏（237/295px 宽、overflow-y:auto）因此被撑出横向滚动条。
  //   形态腿（真几何由 e2e/pixel_uat.spec.ts P8 在真浏览器里量）：浮层必须 portal 到 body，
  //   即不再参与任何应用内滚动容器的排版流。
  it('通知浮层 portal 到 body：不再留在左栏滚动容器内', () => {
    openMenu({ showNotify: true })
    fireEvent.click(screen.getByText('通知中心'))
    const notice = document.querySelector('.am-notice') as HTMLElement
    const wrap = document.querySelector('.am-wrap') as HTMLElement
    expect(notice).toBeTruthy()
    expect(notice.parentElement, '浮层应直接挂在 body 上').toBe(document.body)
    expect(wrap.contains(notice), '浮层不得留在 .am-wrap（左栏内）').toBe(false)
    // 落点由 placeNotice() 现算（jsdom 无布局，矩形全 0）：夹边后仍是有限值，不是写死的 300px
    expect(Number.isFinite(Number(notice.style.width.replace('px', '')))).toBe(true)
  })

  it('onGotoMy（selfNav 缺省）：露出「我的账号」，点击走回调', () => {
    const my = vi.fn()
    openMenu({ onGotoMy: my })
    fireEvent.click(document.querySelector('.am-trigger') as Element)
    fireEvent.click(screen.getByText('我的账号'))
    expect(my).toHaveBeenCalledTimes(1)
  })

  it('selfNav 与 onGotoMy 同时传（移动档形态）：我的账号只出现一次，不撞重名', () => {
    openMenu({ selfNav: vi.fn(), onGotoMy: vi.fn() })
    expect(screen.getAllByText('我的账号').length).toBe(1)
  })

  it('缺省两参（后台/未升级外壳）：通知中心与我的账号都不露', () => {
    openMenu({})
    expect(screen.queryByText('通知中心')).toBeNull()
    expect(screen.queryByText('我的账号')).toBeNull()
  })
})
