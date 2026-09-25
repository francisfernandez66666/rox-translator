// ============================================================================
// components/admin/BrandTermsP.dom.test.tsx — 品牌名设置面板交互收口测试（★ F-25，2026-09-25 发布前 UAT 批G）
// 三条等值/负向锁（全部对齐处方，不动既有测试已锁数字）：
//   ① 删除钮内必须渲染现成 <CloseIcon size={14}/>——querySelector('svg') 命中数**等于 1**
//      （此前 emoji 清理后删除钮只剩 aria-label、无任何可视图形）；
//   ② 点删除 → 先出站内确认框，kbEntryDelete 调用次数在「点钮时刻」**等于 0**、
//      点「确定」后才**等于 1**（取消路径反向锁：始终等于 0）；
//   ③ window.prompt 全程负向清零（调用次数**等于 0**），覆盖编辑（改）与补语言两条路径。
// 运行：npx vitest run src/components/admin/BrandTermsP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import BrandTermsP from './BrandTermsP'
// confirmDialog 需要 <DialogHost/> 宿主（与 main.tsx 同样的挂载方式）
import { DialogHost } from '@/components/uiDialogs'

// 知识库接口全量 mock：断言只看向 api/kb 的调用时序与载荷，不发真请求
const mocks = vi.hoisted(() => ({
  kbPackages: vi.fn(),
  brandTerms: vi.fn(),
  kbEntryAdd: vi.fn(),
  kbEntryUpdate: vi.fn(),
  kbEntryDelete: vi.fn(),
}))
vi.mock('@/api/kb', () => mocks)

// 一条品牌「极石」的阿拉伯语条目（id=11，删除/编辑两条路径都打在它身上）
const entryAr = { id: 11, package_id: 7, layer: 1, source_lang: 'zh', source_text: '极石', target_lang: 'ar', target_text: 'ROX', module: 'brand' }
// 同品牌俄语条目（id=12）：让芯片多一枚，验证删除钮逐枚带图形而非只第一枚
const entryRu = { id: 12, package_id: 7, layer: 1, source_lang: 'zh', source_text: '极石', target_lang: 'ru', target_text: 'РОКС', module: 'brand' }

// window.prompt 负向哨兵：F-25 后本组件任何路径都不许再触达原生 prompt
let promptSpy: ReturnType<typeof vi.spyOn>

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  promptSpy = vi.spyOn(window, 'prompt').mockReturnValue(null)
  mocks.kbPackages.mockResolvedValue({ packages: [{ id: 7, name: '默认包', code: 'default' }] })
  mocks.brandTerms.mockResolvedValue({ success: true, terms: [entryAr, entryRu] })
  mocks.kbEntryAdd.mockResolvedValue({ success: true })
  mocks.kbEntryUpdate.mockResolvedValue({ success: true })
  mocks.kbEntryDelete.mockResolvedValue({ success: true })
})
afterEach(() => {
  // 兜底负向清零：任何用例走到这里时 prompt 都必须一次没被调过
  expect(promptSpy.mock.calls.length).toBe(0)
  promptSpy.mockRestore()
})

/** 挂载面板 + 弹窗宿主，并等待包/条目加载完成（芯片渲染出来） */
async function mountLoaded() {
  render(<><BrandTermsP /><DialogHost /></>)
  await vi.waitFor(() => { expect(mocks.brandTerms).toHaveBeenCalled() })
  return screen.findAllByRole('button', { name: '删除该语言译法' })
}

describe('品牌术语 · 删除钮图形与确认时序（★ F-25 锁①②）', () => {
  it('删除钮内渲染 CloseIcon：svg 命中数等于 1（每枚芯片都是）', async () => {
    const btns = await mountLoaded()
    expect(btns.length).toBe(2) // 极石有 ar/ru 两枚语言条目
    for (const btn of btns) {
      // 等值锁：按钮内恰好一枚 svg（CloseIcon），不再是无图形的裸 aria-label 钮
      expect(btn.querySelectorAll('svg').length).toBe(1)
    }
  })

  it('点删除先出确认框（此刻 kbEntryDelete 调用次数等于 0）；点取消仍等于 0；点确定后才等于 1', async () => {
    const [delAr] = await mountLoaded()
    fireEvent.click(delAr)
    // 确认框文案上屏（zh 语种，{brand}/{lang} 两占位符已填充）——未确认前不得发删除请求
    await vi.waitFor(() => { expect(screen.getByText(/确定删除「极石」的 阿拉伯语 译法吗/)).toBeTruthy() })
    expect(mocks.kbEntryDelete.mock.calls.length).toBe(0)
    // 取消路径：确认后请求数**仍等于 0**（负向锁）
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    await vi.waitFor(() => { expect(screen.queryByText(/确定删除「极石」的 阿拉伯语 译法吗/)).toBeNull() })
    expect(mocks.kbEntryDelete.mock.calls.length).toBe(0)
    // 确认路径：点「确定」后恰好调用 1 次，且打的是被删条目的 id
    fireEvent.click(delAr)
    await vi.waitFor(() => { expect(screen.getByText(/确定删除「极石」的 阿拉伯语 译法吗/)).toBeTruthy() })
    fireEvent.click(screen.getByRole('button', { name: '确定' }))
    await vi.waitFor(() => { expect(mocks.kbEntryDelete.mock.calls.length).toBe(1) })
    expect(mocks.kbEntryDelete).toHaveBeenCalledWith(11)
    expect(promptSpy.mock.calls.length).toBe(0)
  })
})

describe('品牌术语 · 编辑/补语言走站内 Dialog（★ F-25 锁③：prompt 全程为 0）', () => {
  it('点「改」出编辑弹窗（语言为 BRAND_LANGS 下拉、21 项等值），保存后 kbEntryUpdate 恰好 1 次', async () => {
    await mountLoaded()
    const [editAr] = await screen.findAllByRole('button', { name: '改' })
    fireEvent.click(editAr)
    await vi.waitFor(() => { expect(screen.getByText('编辑品牌译法')).toBeTruthy() })
    // 原生 prompt 一次都不许被触达（等值 0）
    expect(promptSpy.mock.calls.length).toBe(0)
    // 语言字段是现成 BRAND_LANGS 常量渲染的 select：选项数等于 21、预填当前条目语种
    const sel = document.querySelector('.lc-dialog select.lc-select') as HTMLSelectElement
    expect(sel.options.length).toBe(21)
    expect(sel.value).toBe('ar')
    // 改译文（第二个输入框）→ 保存：走 kbEntryUpdate 且载荷逐字段等值
    const inputs = document.querySelectorAll('.lc-dialog input.lc-input')
    fireEvent.change(inputs[1], { target: { value: 'ROX-M' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await vi.waitFor(() => { expect(mocks.kbEntryUpdate.mock.calls.length).toBe(1) })
    expect(mocks.kbEntryUpdate).toHaveBeenCalledWith({ id: 11, layer: 1, source_text: '极石', target_lang: 'ar', target_text: 'ROX-M', module: 'brand' })
    expect(promptSpy.mock.calls.length).toBe(0)
  })

  it('点「+补语言」出同一弹窗，保存后 kbEntryAdd 恰好 1 次（默认语种 en）', async () => {
    await mountLoaded()
    fireEvent.click(screen.getByRole('button', { name: '+补语言' }))
    await vi.waitFor(() => { expect(screen.getByText('编辑品牌译法')).toBeTruthy() })
    const inputs = document.querySelectorAll('.lc-dialog input.lc-input')
    fireEvent.change(inputs[1], { target: { value: '록스' } })
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await vi.waitFor(() => { expect(mocks.kbEntryAdd.mock.calls.length).toBe(1) })
    expect(mocks.kbEntryAdd).toHaveBeenCalledWith({ package_id: 7, layer: 1, source_text: '极石', target_lang: 'en', target_text: '록스', module: 'brand' })
    expect(promptSpy.mock.calls.length).toBe(0)
  })
})
