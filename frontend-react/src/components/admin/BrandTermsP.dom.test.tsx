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

// ★ F-58：toast 全量 mock 成哨兵——本批的正向锁是「中止/部分失败时**不得**出现成功 toast」，
//   真 toast 只往总线发事件、jsdom 里无可断言的落点，改成 spies 后调用次数与文案都能等值核对。
const toasts = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn(), warn: vi.fn() }))
vi.mock('@/lib/toastBus', () => ({ toastSuccess: toasts.success, toastError: toasts.error, toastWarn: toasts.warn }))

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

// ============================================================================
// ★ F-58（2026-09-26 〇-U 批 I-8）：21 语种串行写入的「中止」与「诚实回执」
//   原缺陷：新增品牌名对 21 个语种串行发 21 次请求，但只在末尾无脑 toastSuccess，
//   中途某语种 5xx／网络异常既不回滚也不报错（界面说「已新增」，实际写了一半），
//   且弹窗一关就没法停，客户只能干等整串跑完。
//   两条等值锁（全部对齐改法：逐语种判 r.success + 中止只停后续语种 + 三态 toast）：
//   ① 中止路径：请求次数**等于**已确认的 2 次（不是 21 次跑满）、成功 toast 次数**等于 0**、
//      取消钮语义在此期间是「中止」（点它不关窗），中止回执文案逐字等值含「2/21」，
//      输入框两值**保留**（可直接改正重跑，幂等覆盖不重复插行）；
//   ② 部分失败路径：第 3 个语种（de）返回 success:false 时，toastError 恰好 1 次且带
//      「20/21」与失败语种名，成功 toast 次数仍**等于 0**，弹窗不关、输入保留。
//   反向说明（为何这两条不是假绿）：把 addBrand 改回「循环后无条件 toastSuccess」⇒①②同红；
//   去掉 cancelRef 判定 ⇒①红（请求数=21 且出现成功 toast）；判错 r.success ⇒②红。
// 运行：npx vitest run src/components/admin/BrandTermsP.dom.test.tsx
// ============================================================================

/** 手工挂起的请求队列：每次 kbEntryAdd 返回一个由测试决定何时落地的 promise。
 *  total=累计发出数（中止判据看这个），pending=当前还挂着没落地的数量。 */
function deferredQueue(result?: (n: number) => unknown) {
  const rs: Array<(v: unknown) => void> = []
  let total = 0
  return {
    impl: () => new Promise((res) => { total += 1; rs.push(res) }),
    /** 累计发出的请求数（= 组件真正打到接口上的次数） */
    total: () => total,
    /** 仍在挂起（未落地）的请求数 */
    pending: () => rs.length,
    /** 放行最前面 n 个挂起请求；result(i) 可按序号定制回执 */
    settle: (n = 1) => {
      for (let k = 0; k < n; k++) {
        const r = rs.shift()
        if (r) r(result ? result(total - rs.length + k) : { success: true })
      }
    },
  }
}

/** 打开「新增品牌名」弹窗并填好两个字段（品牌名 + 统一外语译法） */
async function openNewBrandDialog(brand = '极石', en = 'ROX') {
  fireEvent.click(screen.getByRole('button', { name: '＋ 新增品牌名' }))
  await vi.waitFor(() => { expect(screen.getByText('新增品牌名')).toBeTruthy() })
  const inputs = document.querySelectorAll('.lc-dialog input.lc-input')
  fireEvent.change(inputs[0], { target: { value: brand } })
  fireEvent.change(inputs[1], { target: { value: en } })
  return inputs
}

describe('品牌术语 · 串行写入的中止与诚实回执（★ F-58 锁①②）', () => {
  it('中止：请求数停在已放行的 2 次（等于锁），成功 toast 等于 0，回执说「2/21」且输入保留', async () => {
    await mountLoaded()
    const q = deferredQueue()
    mocks.kbEntryAdd.mockImplementation(q.impl)
    await openNewBrandDialog()
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    // 第 1 个语种请求已发出且挂起：此时按钮语义已翻成「进度 + 中止」
    await vi.waitFor(() => { expect(q.total()).toBe(1) })
    expect(screen.getByRole('button', { name: /写入中/ })).toBeTruthy()
    q.settle() // 放行 en ⇒ done=1，循环随即发出 ar 的请求
    await vi.waitFor(() => { expect(q.total()).toBe(2) })
    // 点「中止」：只停后续语种，不发请求、不关窗
    fireEvent.click(screen.getByRole('button', { name: '中止' }))
    q.settle() // 在途的 ar 落地后循环在语种顶部看到中止标志即 break
    await vi.waitFor(() => { expect(toasts.warn.mock.calls.length).toBe(1) })
    // 等值锁：接口只被打 2 次（不是 21 次跑满），其余语种一次都没发
    expect(mocks.kbEntryAdd.mock.calls.length).toBe(2)
    expect(q.pending()).toBe(0)
    // 诚实回执：warn 恰好 1 次（内容 = bt.addCancelled 填充 2/21），成功 toast **等于 0**
    expect(String(toasts.warn.mock.calls[0][0])).toContain('2/21')
    expect(toasts.success.mock.calls.length).toBe(0)
    // 弹窗不关 + 两输入保留：客户可改正后直接重跑补齐其余语种
    expect(screen.getByText('新增品牌名')).toBeTruthy()
    const inputs = document.querySelectorAll('.lc-dialog input.lc-input')
    expect((inputs[0] as HTMLInputElement).value).toBe('极石')
    expect((inputs[1] as HTMLInputElement).value).toBe('ROX')
    // 中止后仍回刷列表（已写进去的 2 行必须马上可见，不能停在旧快照）
    expect(mocks.brandTerms.mock.calls.length).toBeGreaterThan(1)
    // 收尾复位：按钮语义回到「保存」，adding 态不会永久卡住
    await vi.waitFor(() => { expect(screen.getByRole('button', { name: '保存' })).toBeTruthy() })
  })

  it('部分失败：de 语种 success:false ⇒ toastError 恰好 1 次（带 20/21 与语种名），成功 toast 等于 0', async () => {
    await mountLoaded()
    // 逐语种回执：命中 de 时如实返回失败（BRAND_LANGS 第 3 项），其余成功
    mocks.kbEntryAdd.mockImplementation((p: { target_lang: string }) =>
      Promise.resolve(p.target_lang === 'de' ? { success: false, message: '服务不可用' } : { success: true }))
    await openNewBrandDialog()
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await vi.waitFor(() => { expect(toasts.error.mock.calls.length).toBe(1) })
    expect(mocks.kbEntryAdd.mock.calls.length).toBe(21) // 未中止 ⇒ 全语种跑满
    const copy = String(toasts.error.mock.calls[0][0])
    expect(copy).toContain('20/21')
    expect(copy).toContain('de')
    expect(toasts.success.mock.calls.length).toBe(0)
    expect(toasts.warn.mock.calls.length).toBe(0)
    // 失败不清输入、不关窗（与中止路径同口径：改正后可原地重跑）
    expect(screen.getByText('新增品牌名')).toBeTruthy()
    expect((document.querySelectorAll('.lc-dialog input.lc-input')[0] as HTMLInputElement).value).toBe('极石')
  })

  it('全绿路径仍发成功 toast 且关窗清输入（正向对照，防「三态分支把成功也说成失败」）', async () => {
    await mountLoaded()
    mocks.kbEntryAdd.mockResolvedValue({ success: true })
    await openNewBrandDialog('极石汽车', 'ROX Motors')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await vi.waitFor(() => { expect(toasts.success.mock.calls.length).toBe(1) })
    expect(String(toasts.success.mock.calls[0][0])).toContain('ROX Motors')
    expect(toasts.error.mock.calls.length).toBe(0)
    expect(toasts.warn.mock.calls.length).toBe(0)
    await vi.waitFor(() => { expect(screen.queryByText('新增品牌名')).toBeNull() })
  })

  it('空输入前置拦截：品牌名/译法任一为空 ⇒ 一次请求都不发（kbEntryAdd 次数等于 0）', async () => {
    await mountLoaded()
    await openNewBrandDialog('', '')
    fireEvent.click(screen.getByRole('button', { name: '保存' }))
    await vi.waitFor(() => { expect(toasts.warn.mock.calls.length).toBe(1) })
    expect(mocks.kbEntryAdd.mock.calls.length).toBe(0)
    expect(screen.getByText('新增品牌名')).toBeTruthy()
  })
})
