// @vitest-environment jsdom
// ============================================================================
// SalesDrafts.noAutoApprove.lock.test.tsx — 二期 §9.4 锁 1：草稿面唯一写动作＝转正
//
// 判据（源码级＋行为级各一，缺一就是半把尺子）：
//   A 组件里被调用的「写侧 api 函数」集合必须恰好＝{salesDraftApprove}（读侧不算）；
//   B api/sales.ts 里 method:'POST' 的导出函数必须恰好＝{salesDraftApprove}（不许长出
//     删除／批量／自动生效第二个写口）；
//   C 组件不许绕过 api 层直接 fetch(／request(（第二把尺子的典型形态）；
//   D 射程自证：往合成组件里塞一个额外写口调用，扫描器必须报出来（防恒绿）；
//   E 行为：转正钮只在 status=draft 渲染；approved/rejected 没有可写按钮。
// 运行：npx vitest run src/SalesDrafts.noAutoApprove.lock.test.tsx
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/** 接口层替身：写侧调用次数由此计数，源码级判据另从磁盘现读实现 */
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

/** 读源码（静态判据按实现行说话；注释里提到函数名不算调用） */
function readSrc(rel: string): string {
  return readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8')
}
/** 剥注释：块注释＋整行行注释，只留执行代码供字面量判据 */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** 组件与接口层源码现读：静态判据按磁盘上的实现行说话，不靠测试里的替身 */
const COMPONENT_SRC = readSrc('./components/SalesDrafts.tsx')
/** 写口真值来源（method:'POST' 的导出函数集合由此现算） */
const API_SRC = readSrc('./api/sales.ts')

/** api/sales.ts 里带 method:'POST' 的导出函数名集合（写口真值，从源码现读不写死）。
 *  按「下一个 export 之前」切函数体，单行/多行导出都看得见（防只认 \n} 的盲区）。 */
function writeExportNames(apiSrc: string): string[] {
  const code = stripComments(apiSrc)
  const names: string[] = []
  const re = /export async function (\w+)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(code))) {
    const next = code.indexOf('export async function', m.index + 1)
    const body = code.slice(m.index, next === -1 ? code.length : next)
    if (/method:\s*'POST'/.test(body)) names.push(m[1])
  }
  return names
}

/** 组件执行代码里真正被调用的写侧函数（与 api 写口真值取交集） */
function calledWriteFns(componentSrc: string, apiSrc: string): string[] {
  const code = stripComments(componentSrc)
  return writeExportNames(apiSrc).filter((fn) => new RegExp(`\\b${fn}\\s*\\(`).test(code))
}

afterEach(() => cleanup())
beforeEach(() => {
  vi.clearAllMocks()
  api.salesCapability.mockResolvedValue({ success: true, status: 'on' })
  api.salesDrafts.mockResolvedValue({ success: true, drafts: [] })
  api.salesDraftApprove.mockResolvedValue({ success: true, id: 'd1', status: 'approved' })
})

describe('草稿面唯一写动作＝转正（§9.4 锁 1）', () => {
  it('A 组件调用的写侧函数集合恰好＝{salesDraftApprove}', () => {
    expect(calledWriteFns(COMPONENT_SRC, API_SRC), '组件调用的写侧函数必须恰好＝{salesDraftApprove}').toEqual(['salesDraftApprove'])
  })

  it('B api/sales.ts 的 POST 写口恰好＝{salesDraftApprove}（不许长第二个写口）', () => {
    expect(writeExportNames(API_SRC), 'api/sales.ts 的 POST 写口必须恰好＝{salesDraftApprove}').toEqual(['salesDraftApprove'])
  })

  it('C 组件不许绕过 api 层直接 fetch(/request(', () => {
    const code = stripComments(COMPONENT_SRC)
    expect(code, '组件里出现裸 fetch(＝绕过 api 层的第二把尺子').not.toMatch(/\bfetch\s*\(/)
    expect(code, '组件里出现裸 request(＝绕过 bizResp 收敛').not.toMatch(/\brequest\s*\(/)
  })

  it('D 射程自证：合成组件多塞一个写口调用，扫描器必须报出来（防恒绿）', () => {
    const rogueApi = API_SRC + "\nexport async function salesDraftDelete(id: string) { return bizResp(() => request('/api/sales/draft/delete', { method: 'POST', body: JSON.stringify({ id }) })) }\n"
    const rogueComponent = COMPONENT_SRC.replace(
      'const approve = () => {',
      "const approve = () => { salesDraftDelete('d1');",
    )
    expect(rogueComponent, '变异没改动组件源码（锚点漂了，反证空转）').not.toBe(COMPONENT_SRC)
    const called = calledWriteFns(rogueComponent, rogueApi)
    expect(called).toContain('salesDraftDelete')
    expect(called.length, '扫描器必须看见第二个写口').toBeGreaterThan(1)
  })

  it('E 行为：转正钮只在 draft 态渲染；approved 态没有可写按钮', async () => {
    const draft = { id: 'd1', tenant_id: 1, anchor: 'price', source_text: '太贵了', suggested_text: '回应价格锚', attribution_ref: 'a1', status: 'draft', created_at: '', updated_at: '' }
    api.salesDrafts.mockResolvedValue({ success: true, drafts: [draft] })
    api.salesDraft.mockResolvedValue({ success: true, draft })
    render(<SalesDrafts />)
    await waitFor(() => expect(screen.getByTestId('sales-drafts-list')).toBeTruthy())
    fireEvent.click(screen.getByText('回应价格锚'))
    await waitFor(() => expect(screen.getByTestId('sales-approve-btn')).toBeTruthy())
    // 唯一写动作真发出去了，且只带 id
    fireEvent.click(screen.getByTestId('sales-approve-btn'))
    await waitFor(() => expect(api.salesDraftApprove).toHaveBeenCalledWith('d1'))
    expect(api.salesDraftApprove).toHaveBeenCalledTimes(1)

    // approved 态：详情里没有转正钮
    cleanup()
    const approved = { ...draft, status: 'approved' }
    api.salesDrafts.mockResolvedValue({ success: true, drafts: [approved] })
    api.salesDraft.mockResolvedValue({ success: true, draft: approved })
    render(<SalesDrafts />)
    await waitFor(() => expect(screen.getByTestId('sales-drafts-list')).toBeTruthy())
    fireEvent.click(screen.getByText('回应价格锚'))
    await waitFor(() => expect(screen.getByTestId('sales-draft-status')).toBeTruthy())
    expect(screen.queryByTestId('sales-approve-btn'), '已转正的草稿不许再有转正钮').toBeNull()
  })
})