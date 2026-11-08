// @vitest-environment jsdom
// ============================================================================
// SalesDrafts.copySingleSource.lock.test.tsx — 二期 §9.4 锁 2：耗尽/停用文案单源
//
// 判据：
//   A 组件执行代码里不许出现「销售忙」字面（第三处自造文案即红，一期 X3 同族）；
//   B 组件必须真的渲染 ExhaustNotice（copyFor 的唯一渲染出口），不许只 import 不用；
//   C 正向对照：词典里确实有「销售忙」这句，否则 A 那条负向锁是空转；
//   D 行为：能力 off 那档出卖家面事实文案（exhaust-copy），on 那档不出；
//   E 反证：往组件执行代码塞一句「销售忙」，A 的判据函数必须报红。
// 与一期 ProductShelf.exhausted.dom.test.tsx 的分工：那边管 copyFor 映射表本身，
// 这边管「草稿面这个新消费方有没有绕过单源自造文案」——两把尺子各治一病。
// 运行：npx vitest run src/SalesDrafts.copySingleSource.lock.test.tsx
// ============================================================================
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

/** 接口层替身：off/on 两档由 salesCapability 的返回值切换 */
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

/** 读源码：静态判据按实现行说话，注释里提到文案不算自造 */
function readSrc(rel: string): string {
  return readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf-8')
}
/** 剥注释：块注释＋整行行注释，只留执行代码供字面量判据 */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** 被检组件源码现读（与变异体喂同一把尺子） */
const COMPONENT_SRC = readSrc('./components/SalesDrafts.tsx')

/** 判据函数：组件执行代码里自造「销售忙」即红（真实源码与变异体喂同一把尺子） */
function homemadeBusyFailures(src: string): string[] {
  return stripComments(src).includes('销售忙') ? ['组件执行代码里自造了「销售忙」文案（第三处出口）'] : []
}

afterEach(() => cleanup())
beforeEach(() => {
  vi.clearAllMocks()
  api.salesDrafts.mockResolvedValue({ success: true, drafts: [] })
})

describe('草稿面耗尽/停用文案单源（§9.4 锁 2）', () => {
  it('A 组件执行代码不许出现「销售忙」字面', () => {
    expect(homemadeBusyFailures(COMPONENT_SRC)).toEqual([])
  })

  it('B 组件必须真渲染 ExhaustNotice（copyFor 唯一出口），不许只 import 不用', () => {
    const code = stripComments(COMPONENT_SRC)
    expect(code, '没有 import ExhaustNotice').toMatch(/import\s*\{[^}]*ExhaustNotice[^}]*\}\s*from\s*'\.\/ProductShelf'/)
    expect(code, 'import 了却没渲染 ExhaustNotice').toMatch(/<ExhaustNotice\b/)
  })

  it('C 正向对照：词典里确实有「销售忙」（否则 A 是空转负向锁）', () => {
    expect(readSrc('./i18n/dicts.zh.ts')).toContain('销售忙')
  })

  it('D 行为：off 出卖家面事实文案，on 不出', async () => {
    api.salesCapability.mockResolvedValue({ success: true, status: 'off' })
    render(<SalesDrafts />)
    await waitFor(() => expect(screen.getByTestId('sales-capability-state')).toBeTruthy())
    expect(screen.getByTestId('exhaust-copy').textContent || '').toContain('关档')

    cleanup()
    api.salesCapability.mockResolvedValue({ success: true, status: 'on' })
    render(<SalesDrafts />)
    await waitFor(() => expect(screen.getByTestId('sales-capability-state')).toBeTruthy())
    expect(screen.queryByTestId('exhaust-copy'), 'on 那档不许出耗尽/停用文案').toBeNull()
  })

  it('E 反证：往组件塞一句「销售忙」，判据函数必须报红', () => {
    const mutated = COMPONENT_SRC.replace(
      "const FILTERS = ['', 'draft', 'approved', 'rejected'] as const",
      "const FILTERS = ['', 'draft', 'approved', 'rejected'] as const\nconst _busy = '销售忙'",
    )
    expect(mutated, '变异没改动源码（锚点漂了，反证空转）').not.toBe(COMPONENT_SRC)
    expect(homemadeBusyFailures(mutated).length, '判据必须抓住第三处文案').toBeGreaterThan(0)
  })
})