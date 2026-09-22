// ============================================================================
// TicketsPage.progress_ws.dom.test.tsx — 工单「执行进度」气泡换词动效锁
// （★ 2026-09-22 用户追加需求：「把执行进度的动画也加上这个动效」）
// 断言两件事：
//   ① 排队 / 执行中的工单打开进度气泡 → 出现全站唯一实现的 WordSwap（.ws 节点）；
//   ② 已完成工单的进度气泡 → 不出现该动效（终态不能继续「假装在干活」）。
// 口径说明：排队（queued）与执行中（in_progress）在实现里走同一个 progressAnimating 判定，
//   故只开一条 in_progress 用例，不重复铺第二种非终态。
// 本文件只查节点存在性、不查任何界面文案，所以不注 app_lang / 语种（vitest.setup.ts 预置的
//   zh 与本用例无关），也不会因首访语言自动检测而翻红。
// 运行：npx vitest run src/components/TicketsPage.progress_ws.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import TicketsPage from './TicketsPage'

// 两份工单只差 status：其余字段一致，保证红/绿只由「是否终态」决定
// id 写成固定的 11/12：详情桩是按 id 分叉的（见下面 ticketDetail），
// 若这里改成随机/自增，两条用例拿到的 states 与 progress 就会错位，锁的就不是 status 了。
const base = (status: string) => ({
  id: status === 'completed' ? 11 : 12,
  ticket_no: `T-${status}`, title: '进度动效', status, target_langs: 'en',
  created_at: '2026-09-22T00:00:00Z',
})

// 工单页挂载即拉列表、打开气泡即拉详情，不 mock 就会打真实后端（vitest 环境里没有 BASE_URL，
// 只会得到一堆网络报错并把真正的断言原因盖掉）。
// 工厂必须把 TicketsPage 用到的导出列全：漏一个会在调用处抛「xxx is not a function」，
// 红得跟本用例要查的问题毫无关系。
vi.mock('@/api', () => {
  const ok = (extra: Record<string, unknown> = {}) => ({ success: true, ...extra })
  return {
    // 列表两条：只差 status，其余字段一致（见上面 base()），本用例的 diff 面只有「是否终态」
    myTickets: vi.fn(async () => ok({ tickets: [base('in_progress'), base('completed')] })),
    ticketDetail: vi.fn(async (id: number) => ok({
      // 11=completed / 12=in_progress（与上面 base 的 id 对应）
      ticket: base(id === 11 ? 'completed' : 'in_progress'),
      // states/progress 按 id 分叉：非终态给一条 running 步骤 + 40%，终态给空步骤 + 100%，
      // 让进度区在两处都真实渲染出来（否则「没有动效」可能只是因为气泡压根没画进度）。
      states: id === 11 ? [] : [{ id: 1, step: 'file_translate', status: 'running' }],
      files: [],
      progress: id === 11 ? 100 : 40,
    })),
    ticketCreate: vi.fn(async () => ok()),
    ticketCreateFile: vi.fn(async () => ok()),
    ticketRun: vi.fn(async () => ok()),
    // 下载/删除/取消本用例都不该真正触发，但它们在操作列里被渲染成按钮，
    // 存在性必须给足（openProgress 会按位置点按钮，见下方注释）。
    ticketDownload: vi.fn(async () => undefined),
    ticketDelete: vi.fn(async () => ok()),
    ticketCancel: vi.fn(async () => ok()),
    ticketOpenApiTasks: vi.fn(async () => ok({ tasks: [] })),
  }
})

// jsdom 没实现 window.scrollTo（调用即报 "Not implemented"），而组件打开气泡时会滚动页面：
// 统一桩成空实现，免得环境噪音混进失败信息。
beforeEach(() => vi.spyOn(window, 'scrollTo').mockImplementation(() => undefined))
afterEach(cleanup)

// 打开指定工单的进度气泡（列表行「详情」按钮文案取 tk.detail）
// 全程不按文案找按钮：详情钮文案走 i18n 取词（tk.detail），换语种/改文案都会让本函数失联，
// 按「该行最后一个 button」定位才是稳定的（DataTable 的 Link 渲染为 <button type="button">）。
async function openProgress(no: string) {
  const cells = await screen.findAllByText(no)
  const row = cells[0].closest('tr') as HTMLElement
  // 注意：这一下点的是操作列**第一个**按钮（in_progress→「取消」、completed→「下载」），
  // 不是详情钮；这两个动作都已被上面的 vi.mock 拦住（confirmDialog 在测试里不会自动确认），
  // 所以它只起「让该行响应一次点击」的作用，改操作列顺序前请先回看这里。
  fireEvent.click(row.querySelector('button')! as HTMLElement)
  // 详情按钮在操作列末尾：直接点该行最后一个 button（Link 渲染为 button）
  const btns = [...row.querySelectorAll('button')]
  fireEvent.click(btns[btns.length - 1])
  // 等气泡挂载用 data-testid 而不是标题文案：toggleDetail 是异步取详情后再 setDetail 开层，
  // 直接查 .ws 会在「气泡还没开」这一步就假红，分不清是没渲染还是没打开。
  await waitFor(() => expect(document.querySelector('[data-testid="tk-progress-dialog"]')).toBeTruthy())
}

describe('工单执行进度气泡 · WordSwap 换词动效', () => {
  it('执行中工单：进度气泡内出现换词动效节点', async () => {
    render(<TicketsPage />)
    await openProgress('T-in_progress')
    // 查 .tk-prog-ws-row .ws 而不是只查 .ws：进度气泡里可能并存别的动效/节点，
    // 限定行容器才证明「动效是挂在执行进度那一块」，而不是别处顺带渲染的。
    expect(document.querySelector('.tk-prog-ws-row .ws')).toBeTruthy()
    // 第二个探针查 [data-ws="tag"]（语种标签位）：光有 .ws 类名可能只是一个空壳 div，
    // 标签位在才说明渲染的是 WordSwap 本体（全站唯一实现），不是同名 class 的仿品。
    expect(document.querySelector('.tk-prog-ws-row [data-ws="tag"]')).toBeTruthy()
    // 本用例只锁「渲染出这一处动效」，不锁演出过程与拍数（那是 WordSwap.dom.test.tsx 的职责），
    // 所以这里不注 matchMedia 桩：jsdom 无该属性 → 组件走演出路径，节点即刻存在。
  })

  it('已完成工单：进度气泡不再演换词动效', async () => {
    render(<TicketsPage />)
    await openProgress('T-completed')
    // 反向锁（产品意图）：完成/取消/失败等终态不能再「假装在干活」。
    // 查的是行容器 .tk-prog-ws-row 本身而不是里面的 .ws：progressAnimating 为假时整行都不该出现，
    // 只剩一个空的占位行同样算缺陷（会多出一段没有任何内容的间距）。
    expect(document.querySelector('.tk-prog-ws-row')).toBeNull()
  })
})
