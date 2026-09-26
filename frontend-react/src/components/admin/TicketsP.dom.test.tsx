// ============================================================================
// components/admin/TicketsP.dom.test.tsx — 反馈列表「提交人」列收口测试（★ O-12，2026-09-26 批 I-8）
//
// 缺陷因果链（为什么只锁这一列也要写测试）：
//   ① 匿名留资走 POST /api/lead，后端**复用 feedbacks 通道**落库：
//      TenantID:0 / UserID:0 / TargetType:"lead"（internal/api/lead.go），本来就没有账号可指；
//   ② 旧渲染 `row.user_name || ('#' + row.user_id)` 于是把这类行打成 `#0`，
//      对象列又只有 ticket / 「其余=文本」两档 ⇒ lead 被标成「文本」——
//      运营读起来像一条脏数据（UAT R5.11 实测）；
//   ③ 与 F-64 同族：状态码与界面都「有值」，但值在说谎。
//
// 锁定的四组行为（等值锁 + 负向锁，不写「含关键字」这种弱判据）：
//   ① lead 行用户列 **等于** 词典字面「留资访客（未注册）」，且整行**不得**出现 `#0`（负向）；
//   ② lead 行对象列 **等于** 词典字面「留资」（旧写法会吃「文本」分支，故单独锁）；
//   ③ 反向对照：真实用户行没有 display_name 时仍渲染 `#<id>`——
//      防止「为消灭 #0 把全部兜底 ID 一并抹掉」这类过度修复（实装优先≠拆掉原处）；
//   ④ 详情头同口径：lead 行进详情后标题署「留资访客」（列表侧普通行由 ③ 锁，不在此重复）。
//
// 运行：npx vitest run src/components/admin/TicketsP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { TicketsP } from './TicketsP'
// 等值锁的期望串直接从词典取（改文案即改期望，测试与文案不留两套真相）
import { zh as fbZh } from '@/i18n/panels/feedback'

// @/api 局部桩（importOriginal 展开真模块再覆盖九个工作台接口）：
// 整模块替换会让 stores/auth.tsx 的 getAuthToken 取不到真实现而直接抛错——
// 桩要收敛到「本面板用到的那九个」，其余导出保持真实，别顺手把整个 api 门面抹平。
const m = vi.hoisted(() => ({
  // consumeFeedback 是「取完即清」的一次性跳转参数读取器，组件挂载即调用 ⇒ 必须是函数；
  // 这里恒返 0（无跳转），把射程收敛到列表渲染本身。
  admin: { myLevel: 3, isSuper: true, activeTenantId: 0, consumeFeedback: vi.fn(() => 0) },
  api: {
    feedbackList: vi.fn(),
    feedbackReply: vi.fn(),
    resolveFeedback: vi.fn(),
    createFeedback: vi.fn(),
    approveList: vi.fn(),
    approveAction: vi.fn(),
    listTmReview: vi.fn(),
    approveTmReview: vi.fn(),
    rejectTmReview: vi.fn(),
  },
}))
vi.mock('@/api', async (importOriginal) => ({...((await importOriginal()) as object), ...m.api }))
vi.mock('@/stores/admin', () => ({ useAdmin: () => m.admin }))

// 三条样本：① 匿名留资（user_id=0、无名字、target_type=lead）
//           ② 普通文本反馈（有名字，走 user_name 分支）
//           ③ 真实用户但后端没回名字（user_id=7）⇒ 必须仍是 #7，用来反证「判据没被放宽成一律访客」
const leadRow = {
  id: 91, user_id: 0, user_name: '', target_type: 'lead', ticket_id: 0,
  content: '【销售线索】公司：UAT公司｜邮箱：sales@uat-lead.com｜意向语言：English｜来源：pricing｜留言：请报价',
  mode: '', status: 'open', created_at: '2026-09-26T02:00:00Z',
}
const namedRow = {
  id: 92, user_id: 7, user_name: '张三', target_type: 'text', ticket_id: 0,
  content: '术语翻错了', mode: 'fast', status: 'open', created_at: '2026-09-26T02:10:00Z',
}
const idRow = {
  id: 93, user_id: 7, user_name: '', target_type: 'text', ticket_id: 0,
  content: '没有名字的真实用户', mode: 'pro', status: 'open', created_at: '2026-09-26T02:20:00Z',
}

beforeEach(() => {
  cleanup()
  Object.values(m.api).forEach((f: any) => f.mockReset())
  m.api.feedbackList.mockResolvedValue({ success: true, feedbacks: [leadRow, namedRow, idRow] })
  m.api.approveList.mockResolvedValue({ success: true, tickets: [] })
  m.api.listTmReview.mockResolvedValue({ success: true, candidates: [] })
})

// 数据到齐锚点：**故意不用 lead 标签本身**。
// 早先用 fb.userLead 当锚点，一旦 lead 判据被改坏，四条用例会一起卡在锚点超时上
// （包括那条本该反证「普通行仍显示 #7」的 ③），红得没有归属、也看不出是谁坏了。
// 换成「有名字的那一行出现」＝与 lead 判据无关的到达信号，红只落在真正依赖判据的用例上。
async function waitForRows() {
  await vi.waitFor(() => expect(screen.queryAllByText('张三').length).toBe(1))
}

describe('TicketsP · O-12 留资行的提交人/对象标注', () => {
  it('① lead 行用户列等于词典字面值，整行不出现 #0', async () => {
    render(<TicketsP />)
    await waitForRows()
    const cell = screen.getByText(fbZh['fb.userLead'])
    expect(cell.textContent).toBe(fbZh['fb.userLead'])           // 等值，不是「包含」
    expect(cell.closest('tr')?.textContent ?? '').not.toMatch(/#0/) // 负向：脏数据观感清零
    expect(document.body.textContent).not.toMatch(/#0/)
  })

  it('② lead 行对象列等于「留资」，不得再吃「文本」分支', async () => {
    render(<TicketsP />)
    await waitForRows()
    const tr = screen.getByText(fbZh['fb.userLead']).closest('tr')
    const text2 = tr?.textContent ?? ''
    expect(text2).toContain(fbZh['fb.targetLead'])
    expect(text2).not.toContain(fbZh['fb.targetText'])
  })

  it('③ 反向对照：有 user_id 无名字的普通反馈仍渲染 #7（判据没被放宽）', async () => {
    render(<TicketsP />)
    await waitForRows()
    expect(screen.queryAllByText('#7').length).toBe(1)
    expect(screen.queryAllByText('张三').length).toBeGreaterThanOrEqual(1)
  })

  it('④ 详情头同口径：lead 行进详情后仍署访客标签（列表侧的普通行由 ③ 锁）', async () => {
    render(<TicketsP />)
    await waitForRows()
    // 详情动作 = 每行的「查看详情」链接；lead 是列表第一条，故取第一条
    const leads = screen.getAllByText(/查看详情|Detail|查看/).filter((a) => a.closest('tr'))
    expect(leads.length).toBe(3)
    fireEvent.click(leads[0])
    // 详情头是「#91 · 留资访客（未注册） [待处理]」拼在一个节点里，
    // 故按 #id 找标题再断言其整串文本，不用 getByText(标签)（那要求整节点等值，会假红）。
    const head = () => Array.from(document.querySelectorAll('h3')).find((n) => (n.textContent ?? '').includes('#91'))
    await vi.waitFor(() => expect(head()).toBeTruthy())
    expect(head()?.textContent ?? '').toContain(fbZh['fb.userLead'])
    expect(head()?.textContent ?? '').not.toMatch(/#0/)
  })
})

// ============================================================================
// 反证实跑记录（2026-09-26 建锁时逐条改坏再复跑；每条改完即 cp 还原并复跑回 4 passed）：
//   · 锁①②④（判据失效面）：把 isLeadRow 改成 `return false`（= 退回旧渲染口径）
//     ⇒ **3 failed | 1 passed**，红的正是 ①②④ 三条；
//     ③ 保持绿——它压根不依赖 lead 判据，这条「该绿的必须还绿」同样是反证的一部分
//     （否则四条一起红等于没做归属）。此时 lead 行用户列回到 `#0`、对象列回到「文本」。
//   · 锁③（过度修复面）：把用户列写成 `row.user_name || t('fb.userLead')`
//     （= 为了消灭 #0 把所有无名行一律标成访客）
//     ⇒ **3 failed | 1 passed**：①② 因页面出现两枚访客标签、getByText 歧义而红，
//     ③ 因 `#7` 消失而红；④ 不红（详情头那一条确实是 lead 行，语义没变）。
//   · 锚点选型顺带记录：数据到达锚点用「有名字那行（张三）」而不是 lead 标签，
//     就是为了让上面两种坏法各自只红在真正依赖它的用例上。
// 还原复跑：`Tests 4 passed (4)`。
// ============================================================================
