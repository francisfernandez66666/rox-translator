// ============================================================================
// ApiKeysP.delete.dom.test.tsx — API Key 列表「删除」交互（★ 2026-10-16 用户当场报障）
// 锁定两件事（都是现网现象，不是推测）：
//   ① **只弹一次确认框**：旧代码在操作列 Link 的 onClick 里先 confirmDialog 一次，
//      再进 deleteKey() 里又 confirmDialog 一次 ⇒ 用户要点两次「确定」才真删（现网报「弹窗两次」）；
//      且点了第一次「确定」之后**请求还没发出去**（deleteKey 卡在第二个确认上）。
//   ② **删完必须刷新列表**：旧交互下任何一次「只确认一次 / 第二次点取消」都会让列表一动不动，
//      于是用户看到「删了没反应」，只有整页刷新才发现那行已经没了。
// 断言取向：确认一次 ⇒ 请求已发且行已消失、弹窗已关（这三条同时成立才算这条交互是闭合的）。
// 运行：npx vitest run src/components/admin/ApiKeysP.delete.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { ApiKeysP } from './ApiKeysP'
import { DialogHost } from '@/components/uiDialogs'
import { toastError } from '@/lib/toastBus'

const mocks = vi.hoisted(() => ({
  apiKeys: vi.fn(),
  apiKeyCreate: vi.fn(),
  apiKeyStatus: vi.fn(),
  apiKeyRotate: vi.fn(),
  apiKeyDelete: vi.fn(),
  apiKeyLimit: vi.fn(),
  getOpenAPIDocs: vi.fn(),
  saveOpenAPIDocs: vi.fn(),
  previewOpenAPIDocs: vi.fn(),
  openAPIDocsUrl: vi.fn(),
}))

// ★ 只替换本面板用到的 Key 域接口，其余保持真实导出——`stores/admin`、`stores/auth`
//   也从 `@/api` 取（tenantList/getAuthToken…），整模块 mock 会把它们一起打空，
//   失败信息会指向 auth store 的初始化，与本次要测的交互毫无关系（首跑真踩）。
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    apiKeys: mocks.apiKeys,
    apiKeyCreate: mocks.apiKeyCreate,
    apiKeyStatus: mocks.apiKeyStatus,
    apiKeyRotate: mocks.apiKeyRotate,
    apiKeyDelete: mocks.apiKeyDelete,
    apiKeyLimit: mocks.apiKeyLimit,
    getOpenAPIDocs: mocks.getOpenAPIDocs,
    saveOpenAPIDocs: mocks.saveOpenAPIDocs,
    previewOpenAPIDocs: mocks.previewOpenAPIDocs,
    openAPIDocsUrl: mocks.openAPIDocsUrl,
  }
})

vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}))

/** 两把 Key 的列表夹具：删掉 id=1 之后，第二次 apiKeys() 只回 id=2 */
const KEY1 = { id: 1, key_prefix: 'rk_aaaaaaa', name: '临时', perms: 'translate', status: 'active', daily_call_limit: 0 }
const KEY2 = { id: 2, key_prefix: 'rk_bbbbbbb', name: '生产', perms: 'all', status: 'active', daily_call_limit: 0 }

// 弹窗挂在 document.body（portal）；确认位=primary（普通）/danger 档（删除这类危险操作）
const dialogOpen = () => !!document.querySelector('.lc-dialog')
const dialogConfirm = () => document.querySelector('.lc-dialog .lc-btn--primary, .lc-dialog .lc-btn--danger') as HTMLElement

/** mockFetch 式接线：首次挂载回两把 Key，删掉 id=1 之后的第二次拉取只回 id=2
 *  （列表是否真按后端口径刷新，正是被测行为；夹具固定回同一份会让这条判据变成空转） */
function mockKeysTwoThenOne() {
  mocks.apiKeys.mockResolvedValueOnce({ success: true, keys: [KEY1, KEY2] })
  mocks.apiKeys.mockResolvedValue({ success: true, keys: [KEY2] })
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mockKeysTwoThenOne()
  mocks.apiKeyDelete.mockResolvedValue({ success: true })
  mocks.getOpenAPIDocs.mockResolvedValue({ success: true, md_zh: '', md_en: '' })
})

describe('API Key 删除交互（① 只弹一次确认 ② 删完刷新列表）', () => {
  it('点删除只弹一个确认框；确认一次后请求已发、行已消失、弹窗已关', async () => {
    render(<><ApiKeysP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('临时')).toBeTruthy() })
    expect(screen.getByText('生产')).toBeTruthy()

    // 删 id=1 那行（第一行操作列的「删除」是 danger 链）
    const row1 = screen.getByText('临时').closest('tr') as HTMLElement
    const del = row1.querySelector('.lc-link--danger') as HTMLElement
    del.click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })

    // 第一次「确定」：若代码里有两道确认（旧形态），此刻请求**还没发**、且第二个弹窗还在
    dialogConfirm().click()
    await vi.waitFor(() => { expect(mocks.apiKeyDelete).toHaveBeenCalledTimes(1) })
    expect(mocks.apiKeyDelete).toHaveBeenCalledWith(1)
    // 弹窗必须已关（旧形态这里还开着第二个确认框 ⇒ 用户要点第二次）
    await vi.waitFor(() => { expect(dialogOpen()).toBe(false) })

    // 删完列表必须自己刷新：id=1 那行消失、id=2 还在（不是整页刷新才没的）
    await vi.waitFor(() => { expect(screen.queryByText('临时')).toBeNull() })
    expect(screen.getByText('生产')).toBeTruthy()
    expect(mocks.apiKeys).toHaveBeenCalledTimes(2)
  })

  it('取消确认：不发删除请求，列表保持原样', async () => {
    render(<><ApiKeysP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('临时')).toBeTruthy() })
    const row1 = screen.getByText('临时').closest('tr') as HTMLElement
    ;(row1.querySelector('.lc-link--danger') as HTMLElement).click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    // 取消位是 secondary 按钮里的「取消」
    const cancel = Array.from(document.querySelectorAll('.lc-dialog .lc-btn--secondary')).pop() as HTMLElement
    cancel.click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(false) })
    expect(mocks.apiKeyDelete).not.toHaveBeenCalled()
    expect(screen.getByText('临时')).toBeTruthy()
  })

  // ★ 2026-10-16 D 族：启停（apiKeyStatus）走的也是 bizResp——业务 4xx 是 {success:false} 而**不抛**，
  //   旧写法「调用完就刷列表」把失败吞成"点了没反应"。这条锁住「失败必须出声 + 界面回读库内真实状态」。
  it('启停失败：出后端原文且仍回读列表（不许把业务失败吞成"点了没反应"）', async () => {
    mocks.apiKeyStatus.mockResolvedValue({ success: false, message: 'Key 不存在' })
    render(<><ApiKeysP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('临时')).toBeTruthy() })
    const row1 = screen.getByText('临时').closest('tr') as HTMLElement
    // 启停位是本行操作列里的第一个 Link（文案随状态变：active ⇒ 「禁用」）
    const toggle = row1.querySelectorAll('.lc-link, .lc-link--danger')[0] as HTMLElement
    toggle.click()
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledWith('Key 不存在') })
    expect(mocks.apiKeyStatus, '启停必须真发出去').toHaveBeenCalledWith(1, 'disabled')
    expect(mocks.apiKeys, '失败也要回读：以库里的真实状态为准（界面不许自行推断）').toHaveBeenCalledTimes(2)
  })

  it('后端回业务失败（如「Key 不存在」）：出提示且仍刷新列表，不静默', async () => {
    mocks.apiKeyDelete.mockResolvedValue({ success: false, message: 'Key 不存在' })
    render(<><ApiKeysP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('临时')).toBeTruthy() })
    const row1 = screen.getByText('临时').closest('tr') as HTMLElement
    ;(row1.querySelector('.lc-link--danger') as HTMLElement).click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    dialogConfirm().click()
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledWith('Key 不存在') })
    expect(mocks.apiKeys).toHaveBeenCalledTimes(2)
  })
})
