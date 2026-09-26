// ============================================================================
// components/admin/TmFlowP.dom.test.tsx — TM 提审进度面板收口测试（★ F-62，2026-09-26 批 I-8）
// 锁定的五组行为（全部等值锁 / 负向锁，不写单向阈值）：
//   ① 只读：面板内 button **恰好 1 枚**（只有「刷新」），select **恰好 1 枚**（状态筛选）。
//      审批权仍由超管独占——这里绝不能长出「通过 / 驳回」入口（负向清零）。
//   ② 等值文案锁：审核时效说明、三态摘要整串与 panels/kb.ts 词典取值**字面相等**
//      （摘要用词典字面值逐位替换占位符生成期望串，改词典即改期望——刻意的联动锁）。
//   ③ 筛选 → 请求参数等值：挂载首拉参数 **等于 ''**，把筛选切到 approved 后
//      末次参数 **等于 'approved'**（前端不传任何租户号：断言调用参数个数等于 1）。
//   ④ 三态收敛：空集出「暂无提审记录」；接口 reject 或 success:false 出「提审进度加载失败」
//      且**不得**同时出空态文案（负向锁——失败伪装成「你没数据」是 F-24 同族病）。
//   ⑤ 超管分支：listMyTmReview 调用次数 **等于 0**（不打必然 403 的请求），
//      只显示归属说明；且词典负向锁——提示语**不得**再把超管指向不存在的前台「审核台」页面。
//
// 反证：四条「改坏必红」的实跑记录（含各自主动的组件改动与红数）见文件末尾，
//        其中锁①的「审批入口不得长出」是按钮数等值 1 的静态形态锁，改一行即红。
// 运行：npx vitest run src/components/admin/TmFlowP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import TmFlowP from './TmFlowP'
// 词典字面值（等值锁的期望串直接从词典取，避免测试与文案两套真相）
import { zh as kbZh } from '@/i18n/panels/kb'
// common.* 键在基础词典里（不在 kb 面板词典），取词一律走运行时 t（当前语种 = zh 钉底）
import { t as tr } from '@/i18n'

// 后台上下文可控桩：isSuper 逐用例改写（租户视角 / 超管视角两条分支）
const m = vi.hoisted(() => ({
  admin: { myLevel: 3, isSuper: false, activeTenantId: 9 },
  api: { listMyTmReview: vi.fn() },
}))
vi.mock('@/stores/admin', () => ({ useAdmin: () => m.admin }))
vi.mock('@/api/tmreview', () => m.api)

// 两条本租户候选：一条待审、一条已生效（含 hit_count 与审核时间，覆盖三列取数）
const pendingRow = {
  id: 31, zh: '设备已完成校准', lang: 'en', trans: 'The device has been calibrated',
  source: 'bitext', status: 'pending', hit_count: 0,
  created_at: '2026-09-25T02:00:00Z', reviewed_at: '',
}
const approvedRow = {
  id: 32, zh: '请输入正确的手机号', lang: 'de', trans: 'Bitte geben Sie eine gültige Telefonnummer ein',
  source: 'hit_threshold', status: 'approved', hit_count: 3,
  created_at: '2026-09-24T02:00:00Z', reviewed_at: '2026-09-25T09:00:00Z',
}

/** 摘要期望串：直接拿词典字面值逐位替换占位符（词典改了期望跟着改，不造第二套文案） */
function expectSummary(pending: number, approved: number, rejected: number, total: number): string {
  return kbZh['kb.tmFlowSummary']
    .replace('{pending}', String(pending))
    .replace('{approved}', String(approved))
    .replace('{rejected}', String(rejected))
    .replace('{total}', String(total))
}

/** 取元素全文本（textContent 可能为 null，统一收口成字符串再断言，避开 TS 可空告警） */
function textOf(el: HTMLElement): string {
  return el.textContent ?? ''
}

/** 找到本面板那张 Panel 卡片（标题取自词典，不在测试里重抄中文） */
function panel(): HTMLElement {
  const found = Array.from(document.querySelectorAll('.panel-card'))
    .find((el) => el.querySelector('h2')?.textContent === kbZh['kb.tmFlowTitle'])
  if (!found) throw new Error('提审进度 Panel 未渲染')
  return found as HTMLElement
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  m.admin.myLevel = 3
  m.admin.isSuper = false
  m.admin.activeTenantId = 9
  m.api.listMyTmReview.mockResolvedValue({
    success: true,
    candidates: [pendingRow, approvedRow],
    summary: { pending: 1, approved: 1, rejected: 0, total: 2 },
    truncated: false,
  })
})

describe('提审进度 · 只读形态与等值文案（★ F-62 锁①②）', () => {
  it('挂载即拉全量；面板内 button 恰好 1 枚、select 恰好 1 枚（无审批/驳回入口）', async () => {
    render(<TmFlowP />)
    // 锚点必须落在「数据真的上屏」上：只等接口被调用会在 promise 续体之前放行（首跑实测假红）
    await vi.waitFor(() => { expect(panel().querySelectorAll('tbody tr').length).toBe(2) })
    const p = panel()
    // 等值锁：唯一按钮是「刷新」，唯一下拉是状态筛选
    expect(p.querySelectorAll('button').length).toBe(1)
    expect(p.querySelector('button')?.textContent).toBe(tr('common.refresh'))
    expect(p.querySelectorAll('select').length).toBe(1)
    // 负向清零：本面板不得出现任何审批动作字样（通过/驳回的按钮形态）
    expect(textOf(p).includes('驳回该候选')).toBe(false)
    // 两条候选都上屏（源句与译文各取其列）
    expect(textOf(p).includes(pendingRow.zh)).toBe(true)
    expect(textOf(p).includes(approvedRow.trans)).toBe(true)
    // 命中次数列：等值取 hit_count（0 与 3 各自成格，不拿索引冒充计数）
    expect(p.querySelectorAll('td').length).toBe(14) // 2 行 × 7 列
  })

  it('审核时效说明与三态摘要整串等值；筛选档位恰好 4 项（全部+三态）', async () => {
    render(<TmFlowP />)
    await vi.waitFor(() => { expect(screen.queryAllByText(expectSummary(1, 1, 0, 2)).length).toBe(1) })
    const p = panel()
    // 等值锁：SLA 说明字面等于词典值（F-62 的文案腿——「待审」之后会发生什么）
    expect(screen.getAllByText(kbZh['kb.tmFlowSla']).length).toBe(1)
    // 等值锁：摘要 1/1/0/2（不是 rows.length 冒充总数）
    expect(screen.getAllByText(expectSummary(1, 1, 0, 2)).length).toBe(1)
    // 筛选档位等值：'' + 三态 = 4 项，且中文标签取既有词典
    const opts = Array.from(p.querySelectorAll('select option'))
    expect(opts.map((o) => o.getAttribute('value'))).toEqual(['', 'pending', 'approved', 'rejected'])
    expect(opts.map((o) => o.textContent)).toEqual([
      kbZh['kb.allStatus'], kbZh['kb.pending'], kbZh['kb.approved'], kbZh['kb.rejected'],
    ])
  })

  it('列表被 200 条上限截断时如实说明，且摘要仍是全量口径（两串同时在屏）', async () => {
    m.api.listMyTmReview.mockResolvedValue({
      success: true,
      candidates: [pendingRow],
      summary: { pending: 8, approved: 250, rejected: 2, total: 260 },
      truncated: true,
    })
    render(<TmFlowP />)
    // 锚点用「摘要上屏」（数据到位才渲染）；空表也有一行占位 tr，不能拿来当锚点
    await vi.waitFor(() => { expect(screen.queryAllByText(expectSummary(8, 250, 2, 260)).length).toBe(1) })
    const n = 1
    expect(screen.getAllByText(kbZh['kb.tmFlowTruncated'].replace('{n}', String(n))).length).toBe(1)
    expect(screen.getAllByText(expectSummary(8, 250, 2, 260)).length).toBe(1)
  })
})

describe('提审进度 · 筛选参数与租户参数缺席（★ F-62 锁③）', () => {
  it('首拉参数等于 ""；切到 approved 后末次参数等于 "approved"，且调用只带 1 个入参（不传租户号）', async () => {
    render(<TmFlowP />)
    await vi.waitFor(() => { expect(m.api.listMyTmReview).toHaveBeenCalled() })
    expect(m.api.listMyTmReview.mock.calls[0]).toEqual([''])
    const sel = panel().querySelector('select') as HTMLSelectElement
    fireEvent.change(sel, { target: { value: 'approved' } })
    await vi.waitFor(() => { expect(m.api.listMyTmReview.mock.calls.length).toBe(2) })
    expect(m.api.listMyTmReview.mock.calls[1]).toEqual(['approved'])
    // 负向锁：任何一次调用都只有 status 一个参数——租户由后端从登录态取（F-55：头不能换租户）
    for (const c of m.api.listMyTmReview.mock.calls) expect(c.length).toBe(1)
  })
})

describe('提审进度 · 空态 / 失败态收敛（★ F-62 锁④）', () => {
  it('空集：出「暂无提审记录」，不出失败文案', async () => {
    m.api.listMyTmReview.mockResolvedValue({
      success: true, candidates: [], summary: { pending: 0, approved: 0, rejected: 0, total: 0 }, truncated: false,
    })
    render(<TmFlowP />)
    await vi.waitFor(() => { expect(screen.getAllByText(kbZh['kb.tmFlowEmpty']).length).toBe(1) })
    expect(screen.queryAllByText(kbZh['kb.tmFlowFail']).length).toBe(0)
    // 摘要仍渲染全零（0 不是「没查」，是真计数）
    expect(screen.getAllByText(expectSummary(0, 0, 0, 0)).length).toBe(1)
  })

  it('网络 reject：出失败文案且不出空态文案；success:false 同样收敛为失败态', async () => {
    m.api.listMyTmReview.mockRejectedValue(new Error('502'))
    render(<TmFlowP />)
    await vi.waitFor(() => { expect(screen.getAllByText(kbZh['kb.tmFlowFail']).length).toBe(1) })
    // 等值 0 枚：失败绝不伪装成「你还没有提审记录」
    expect(screen.queryAllByText(kbZh['kb.tmFlowEmpty']).length).toBe(0)
    cleanup()
    m.api.listMyTmReview.mockResolvedValue({ success: false, message: 'forbidden' })
    render(<TmFlowP />)
    await vi.waitFor(() => { expect(screen.getAllByText(kbZh['kb.tmFlowFail']).length).toBe(1) })
    expect(screen.queryAllByText(kbZh['kb.tmFlowEmpty']).length).toBe(0)
  })

  it('失败时不把后端原文甩进界面（失败文案等值 1 枚，无 502/forbidden 字面）', async () => {
    m.api.listMyTmReview.mockRejectedValue(new Error('502 Bad Gateway'))
    render(<TmFlowP />)
    await vi.waitFor(() => { expect(screen.getAllByText(kbZh['kb.tmFlowFail']).length).toBe(1) })
    const p = panel()
    expect(textOf(p).includes('502')).toBe(false)
    expect(textOf(p).includes('Bad Gateway')).toBe(false)
  })
})

describe('提审进度 · 超管分支不打必然 403 的请求（★ F-62 锁⑤）', () => {
  it('isSuper=true：listMyTmReview 调用次数等于 0，只显示归属说明、不渲染列表与筛选', async () => {
    m.admin.myLevel = 4
    m.admin.isSuper = true
    m.admin.activeTenantId = 0
    render(<TmFlowP />)
    // 等值 0 枚：接口一次都不能打（租户侧接口对 tenant_id=0 直接 403）
    expect(m.api.listMyTmReview.mock.calls.length).toBe(0)
    const p = panel()
    expect(textOf(p).includes(kbZh['kb.tmFlowSuperHint'])).toBe(true)
    // 负向清零：超管视图不渲染列表外壳（筛选下拉、刷新钮各 0 枚）
    expect(p.querySelectorAll('select').length).toBe(0)
    expect(p.querySelectorAll('button').length).toBe(0)
    expect(screen.queryAllByText(kbZh['kb.tmFlowSla']).length).toBe(0)
  })

  it('静态负向锁：超管提示不得把用户指向不存在的前台「审核台」页面', () => {
    // 事实：超管侧审批只有 /api/admin/tm-review/* 接口，前台没有审核台页面。
    // 提示语若写「请使用平台审核台」= 把 F-62 的「说到做不到」换个位置再犯一遍。
    expect(kbZh['kb.tmFlowSuperHint'].includes('请使用')).toBe(false)
    expect(/审核台.*查看全部/.test(kbZh['kb.tmFlowSuperHint'])).toBe(false)
    // 正向：必须自陈「前台暂无审核台页面」，并给出真实存在的那条腿（平台侧接口）
    expect(kbZh['kb.tmFlowSuperHint'].includes('前台暂无审核台页面')).toBe(true)
    expect(kbZh['kb.tmFlowSuperHint'].includes('/api/admin/tm-review')).toBe(true)
  })
})

// ----------------------------------------------------------------------------
// 反证实跑记录（2026-09-26 建锁时逐条改坏组件后实跑，红数取 vitest 输出，改完即还原）：
//   · 摘掉 useEffect 的 `if (isSuper) return` → 1 条红（锁⑤「调用次数等于 0」）；
//   · 把 catch 分支的 setFailed(true) 换成 setRows([]) → 2 条红（锁④两条失败态用例，
//     即「网络 reject」与「不把后端原文甩进界面」——空态文案伪装成正常态被抓住）；
//   · 把状态筛选的 onChange 写成恒发 '' → 1 条红（锁③末次参数等于 'approved'）；
//   · 把摘要四个数改成 rows.length 口径 → 2 条红（锁②等值摘要 + 截断用例的全量口径摘要）。
//   四条都验证过「改坏必红」，不是恒绿断言；还原后 9/9 全绿（diff 与 /tmp 备份逐字节一致）。
// ----------------------------------------------------------------------------
