// ============================================================================
// components/admin/TicketsP.detail.dom.test.tsx — 反馈详情面板三连锁
// （★ F-45 脏 JSON 白屏 / ★ F-57② 返回列表重取 / ★ F-59 回复署名，2026-09-27 批 〇-U）
//
// 锁定的三个缺陷（因果链各自独立，共用同一套 fetch mock 与渲染手法，
// mock 骨架逐字沿用 TicketsP.dom.test.tsx（O-12），避免两套桩各红各的）：
//
// · F-45（反馈详情白屏）：详情上下文块读 `translations_json`，历史脏数据是字符串
//   "null"——它是**合法 JSON**，旧写法 try/catch 里 JSON.parse 不抛错而返回 null，
//   下一步 Object.entries(null) 才抛 TypeError ⇒ React 渲染期炸掉整块详情
//   （lib/safeJson.test.ts 只锁了解析器本身，渲染层一直没锁，本文件补上）。
//   喂四种形态：'null'（事故形态）、'{}'（合法空映射）、'[1,2]'（合法 JSON 但不是映射）、
//   '{"en":"Hello there"}'（正常映射，反向对照——防止「一律降级空对象」的过度修复
//   把正常上下文也吞掉造成假绿）。
//
// · F-57②（返回列表不重取）：详情态**整体替换**列表渲染，期间可能已回复/结案；
//   旧写法「← 返回列表」只 setSelected(null)，回到的还是进详情前的快照，
//   刚处理完那条仍显示「待处理」。现源码是 `setSelected(null); void loadFeedbacks()`，
//   锁法：点击前后数 feedbackList 的调用次数，必须 +1（这条接口桩本身就是列表读口的
//   身份，不需要再验 URL 字符串）。
//
// · F-59（署名判据）：回复行 `const staff = roleLevel(r.role) >= 2`。旧判据是
//   `role === 'admin'`，而角色域真实取值里**没有 'admin'**（super_admin/tenant_admin/
//   dept_admin/user…），于是租户管理员的平台回复全被署成「用户」、底色也错档。
//   等值锁：super_admin / tenant_admin 两条回复署名段 **等于** 词典字面
//   baseZh['tickets.roleAdmin']，roleLevel<2 的 user 回复署名段等于 roleUser
//   且**不得**出现 roleAdmin 字样；底色（--adm-info-bg vs --adm-soft）一并锁，
//   因为同一分支同时决定署名与配色，只锁文字会漏掉错档的另一半。
//   期望串从 src/i18n/dicts.zh.ts 现读，禁止把「超管/用户」写死在测试里。
//
// 运行：npx vitest run src/components/admin/TicketsP.detail.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { TicketsP } from './TicketsP'
// 等值锁的期望串直接从词典取（改文案即改期望，测试与文案不留两套真相）：
// fb.* 与 tickets.role* 都在跨模块基础词典 dicts.zh（panels/feedback.ts 里并没有这些键）
import { baseZh } from '@/i18n/dicts.zh'

// @/api 局部桩（importOriginal 展开真模块再覆盖九个工作台接口）——与 O-12 同一手法：
// 整模块替换会让 stores/auth.tsx 的 getAuthToken 取不到真实现而直接抛错。
const m = vi.hoisted(() => ({
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
vi.mock('@/api', async (importOriginal) => ({ ...((await importOriginal()) as object), ...m.api }))
vi.mock('@/stores/admin', () => ({ useAdmin: () => m.admin }))

// 样本行设计（id 连号 101–105，「查看详情」链接在 DataTable 里按数组顺序渲染，
// 故 openDetail(101+i) 直接按索引点，不必再按行内容找）：
//   101 'null'    — 事故形态（parse 成功出 null，旧写法死在 Object.entries）
//   102 '{}'      — 合法空映射（相邻形态①：应该渲染空上下文块而不是崩）
//   103 '[1,2]'   — 合法 JSON 但是数组（相邻形态②：safeJson 必须降级，详情照常）
//   104 正常映射  — 反向对照（降级判据不得把可用数据也吞了）
//   105 三条回复  — F-59 署名样本（super_admin / tenant_admin / user）
const dirtyRows: Record<string, unknown>[] = [
  { id: 101, user_id: 7, user_name: '测试甲', target_type: 'text', ticket_id: 0, content: '脏值-null样本', mode: '', status: 'open', created_at: '2026-09-27T02:00:00Z', with_context: true, source_text: '原文样本A', translations_json: 'null', replies: [] },
  { id: 102, user_id: 7, user_name: '测试乙', target_type: 'text', ticket_id: 0, content: '脏值空对象样本', mode: '', status: 'open', created_at: '2026-09-27T02:05:00Z', with_context: true, source_text: '原文样本B', translations_json: '{}', replies: [] },
  { id: 103, user_id: 7, user_name: '测试丙', target_type: 'text', ticket_id: 0, content: '脏值数组样本', mode: '', status: 'open', created_at: '2026-09-27T02:10:00Z', with_context: true, source_text: '原文样本C', translations_json: '[1,2]', replies: [] },
  { id: 104, user_id: 7, user_name: '测试丁', target_type: 'text', ticket_id: 0, content: '正常上下文样本', mode: '', status: 'open', created_at: '2026-09-27T02:15:00Z', with_context: true, source_text: '原文样本D', translations_json: '{"en":"Hello there"}', replies: [] },
  { id: 105, user_id: 8, user_name: '测试戊', target_type: 'text', ticket_id: 0, content: '署名样本', mode: '', status: 'open', created_at: '2026-09-27T02:20:00Z', with_context: false, translations_json: '', replies: [
    { role: 'super_admin', name: '值班员甲', content: '已同步研发', at: '2026-09-27T03:00:00Z' },
    { role: 'tenant_admin', name: '值班员乙', content: '先按术语表处理', at: '2026-09-27T03:10:00Z' },
    { role: 'user', name: '提问用户', content: '好的，谢谢', at: '2026-09-27T03:20:00Z' },
  ] },
]

beforeEach(() => {
  cleanup()
  Object.values(m.api).forEach((f: any) => f.mockReset())
  m.admin.consumeFeedback = vi.fn(() => 0)
  m.api.feedbackList.mockResolvedValue({ success: true, feedbacks: dirtyRows })
  m.api.approveList.mockResolvedValue({ success: true, tickets: [] })
  m.api.listTmReview.mockResolvedValue({ success: true, candidates: [] })
})

// 数据到齐锚点：与 F-45/F-59 判据都无关的「有名字的行出现」，
// 红只落在真正依赖被锁逻辑的用例上（O-12 文件里注释过的同一教训）。
async function waitForRows() {
  await vi.waitFor(() => expect(screen.queryAllByText('测试甲').length).toBe(1))
}

// openDetail 点第 index 行的「查看详情」（期望文案从词典取，不写死中文）。
// ★ 匹配口径：testing-library 的文本匹配取的是元素**自身**的直接文本节点
// （Icon 子元素不计），故 trim 后等值；Link 渲染文本为「 查看详情」（图标与文案间有格），
// 用等值 trim 而不是 includes，避免把 td/tr 这类祖先一并匹配进来点到假目标。
const viewDetailMatcher = (_content: string, el: Element | null): boolean =>
  !!el && el.tagName !== 'TR' && el.tagName !== 'TABLE' &&
  (Array.from(el.childNodes).filter((n) => n.nodeType === 3).map((n) => n.textContent).join('').trim() === baseZh['fb.viewDetail'])
async function openDetail(index: number) {
  const links = screen.getAllByText(viewDetailMatcher).filter((a) => a.closest('tr'))
  expect(links.length).toBeGreaterThanOrEqual(index + 1)
  fireEvent.click(links[index])
  const id = 101 + index
  await vi.waitFor(() => {
    const head = Array.from(document.querySelectorAll('h3')).find((n) => (n.textContent ?? '').includes(`#${id}`))
    expect(head).toBeTruthy()
  })
}

describe('TicketsP 详情 · F-45 上下文脏 JSON 不得白屏', () => {
  // 三种脏值形态共用同一断言组：详情整块必须活着（标题/正文/返回钮/上下文块标题/
  // 空回复提示全在），且上下文块里不得渲染出任何 [语种] 条目（降级=空对象）。
  // 旧实现在点进这条的瞬间 Object.entries(null) 抛 TypeError，React 渲染期炸整棵树，
  // 「返回钮找不到」就是那时最稳定的红灯形态。
  it.each([
    ['"null"（事故形态：合法 JSON parse 出 null）', 0],
    ['"{}"（合法空映射：块在、条目为零）', 1],
    ['"[1,2]"（合法 JSON 但非映射：必须降级而不是 entries 出 0/1 键）', 2],
  ])('脏值 %s：详情完整渲染且不出现任何语种条目', async (_name: string, idx: number) => {
    render(<TicketsP />)
    await waitForRows()
    await openDetail(idx)
    // 详情骨架逐项在场（少任何一项都说明渲染在上下文块处断过）
    expect(document.body.textContent).toContain(baseZh['fb.backToList'])
    expect(document.body.textContent).toContain(baseZh['fb.ctxAttached'])
    expect(document.body.textContent).toContain(baseZh['fb.noReplies'])
    // 上下文块本体：有原文 pre，但没有任何「[xx]」语种条目
    const ctxBlock = Array.from(document.querySelectorAll('div')).find(
      (d) => (d.textContent ?? '').includes(baseZh['fb.ctxAttached']),
    )!
    expect(ctxBlock).toBeTruthy()
    expect(ctxBlock.textContent).toContain('原文样本') // 上下文块活着：原文 pre 仍在块内
    expect(ctxBlock.querySelectorAll('b').length).toBe(1) // 只有块标题那一个 <b>，条目 <b>[lang]</b> 为零
    expect(ctxBlock.textContent).not.toMatch(/\[\w+\]/) // 块内不得出现任何「[语种]」条目
  })

  // 反向对照（防「过度止血」假绿）：正常映射必须照旧渲染 [en] Hello there——
  // 若有人把 ctxTranslations 改成一律返回 {}，上面三条仍然全绿，只有这条会红。
  it('正常映射 {"en":"Hello there"}：语种条目照常逐条渲染（降级判据没把可用数据吞掉）', async () => {
    render(<TicketsP />)
    await waitForRows()
    await openDetail(3)
    const ctxBlock = Array.from(document.querySelectorAll('div')).find(
      (d) => (d.textContent ?? '').includes(baseZh['fb.ctxAttached']),
    )!
    expect(ctxBlock.textContent).toContain('[en]')
    expect(ctxBlock.textContent).toContain('Hello there')
  })
})

describe('TicketsP 详情 · F-57② 返回列表必须重取列表', () => {
  // 挂载已经打过 1 次 feedbackList；点「← 返回列表」后必须再打 1 次。
  // 旧写法只 setSelected(null) ⇒ 次数停在 1，回到的是进详情前的快照。
  it('点「返回列表」：feedbackList 调用次数 +1，且列表视图恢复', async () => {
    render(<TicketsP />)
    await waitForRows()
    const before = m.api.feedbackList.mock.calls.length
    expect(before, '挂载首拉应恰好 1 次（否则下面的 +1 判据失去基准）').toBe(1)
    await openDetail(0)
    // 在详情里点返回钮（文案带「← 」前缀，用包含匹配定位按钮元素）
    const back = Array.from(document.querySelectorAll('button')).find(
      (b) => (b.textContent ?? '').includes(baseZh['fb.backToList']),
    )!
    expect(back).toBeTruthy()
    fireEvent.click(back)
    await vi.waitFor(() => {
      expect(m.api.feedbackList.mock.calls.length).toBe(before + 1)
    })
    // 重取之后还得是列表视图在场：每行一枚「查看详情」回来了
    await vi.waitFor(() =>
      expect(screen.getAllByText(viewDetailMatcher).filter((a) => a.closest('tr')).length).toBe(dirtyRows.length),
    )
    // 点返回本身不得把列表请求打成风暴（一次返回恰好一次重取）
    expect(m.api.feedbackList.mock.calls.length).toBe(before + 1)
  })
})

describe('TicketsP 详情 · F-59 回复署名等值锁', () => {
  // 署名行的结构是「{name} · {角色标签} · {时间}」三段（同一直排 div，无子元素）。
  // 用「文本以姓名开头且无子节点」定位该行，再对第二段做**等值**断言——
  // 比 toContain 强的地方在于：旧 bug（一律署「用户」）与半修复（署成别的什么）都会红。
  const sigRow = (name: string): HTMLElement => {
    const el = Array.from(document.querySelectorAll('div')).find(
      (d) => d.children.length === 0 && (d.textContent ?? '').startsWith(name),
    )
    if (!el) throw new Error(`未找到署名行：${name}`)
    return el as HTMLElement
  }

  it('super_admin / tenant_admin 署名段等于词典 tickets.roleAdmin；user 回复不得出现该署名', async () => {
    render(<TicketsP />)
    await waitForRows()
    await openDetail(4)
    // ① 两个管理侧角色：署名段逐字等于词典值，底色走管理侧 info-bg 档
    for (const n of ['值班员甲', '值班员乙']) {
      const seg = sigRow(n).textContent!.split(' · ')
      expect(seg[1]).toBe(baseZh['tickets.roleAdmin'])
      expect(sigRow(n).parentElement?.getAttribute('style') ?? '').toContain('--adm-info-bg')
    }
    // ② roleLevel<2 的普通用户：署 roleUser，且整行**不得**含 roleAdmin 字样
    //    （底色同锁：错档的另一半就错在这个三元分支上）
    const u = sigRow('提问用户').textContent!.split(' · ')
    expect(u[1]).toBe(baseZh['tickets.roleUser'])
    expect(u[1]).not.toBe(baseZh['tickets.roleAdmin'])
    expect(sigRow('提问用户').parentElement?.getAttribute('style') ?? '').toContain('--adm-soft')
  })
})
