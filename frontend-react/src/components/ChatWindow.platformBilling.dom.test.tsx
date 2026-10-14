// ============================================================================
// components/ChatWindow.platformBilling.dom.test.tsx — 平台上下文不渲染「余额 0 积分」
// （★ O-9 行为锁，2026-09-27 批 〇-U；mock 骨架沿用 ChatWindow.dom.test.tsx 批 I-10
//   为 O-9 补的登录态桩套路：@/api 整模块桩 + 可变 tid + 真 stores/auth）
//
// 缺陷因果链：
//   /api/me/package 对 tid<=0（超管未切入任何租户）的语义是「平台账号不参与计费，
//   余额固定回 0」——出参没错，错在两处展示位照 points_balance 直渲染，
//   超管永久看到「余额 0 积分」并被 0<=0 点亮「余额不足」横幅。
//   前端守卫 isPlatformBillingContext(role)（stores/auth.tsx:47）：等级>=4 且本地无生效租户。
//   ChatWindow 侧（本文件锁）压 balance 与 orgBudget 两处展示，不做提前 return
//   （chat_max_chars＝F-52② 本地闸数据源，在平台上下文同样要刷）。
//   ★ 2026-10-14 红框批：栏里原来还有第三颗「今日已耗」，它不受守卫约束、当过 ① 的正向对照；
//     那颗按用户令撤下后，对照腿改成"① 与 ② 喂同一份响应、只换角色"（详见 ① 上方注释）。
//   （App.tsx 顶栏那处同一判据的 refreshPkgLine 在 FrontShell 内部、组件未导出，
//     jsdom 里只能整壳挂载才能触达 ⇒ 本轮未锁，需要源码给测试缝，详见交付报告。）
//
// 三条用例为什么能复现缺陷：
//   ① 平台上下文（super_admin + tid=0）喂 {points_balance:0, org_budget.limit>0}：
//      余额 span（data-testid=chat-balance）**不得渲染**、部门预算文案不得出现；
//      （旧稿这里还有半句「但 chat-usage 必须在场」——那颗徽标 2026-10-14 按用户令撤下，
//       对照腿已换成 ② 的"同一份响应、换角色即渲染"，见上面那条红框批注释；
//       两个方向都钉死才是等值锁）
//   ② 反向对照（普通租户角色 user，同一响应）：余额条**必须**渲染，且文案与词典
//      tpl('chat.balanceTokens', …) 逐字相等——没有这条，①就是「余额条从来不渲染」的假绿；
//   ③ 超管**已选定租户**（tid=3）：同一响应必须恢复渲染——锁的是复合判据里
//      「无生效租户」这一半（只按角色一刀切会把超管选租户后的正常余额也抹掉）。
//
// 运行：npx vitest run src/components/ChatWindow.platformBilling.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { PkgRefreshCtx } from '@/hooks/useChat'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang, tpl } from '@/i18n'
import { fmtPoints } from '@/utils/points'
import { useAuthStore } from '@/stores/auth'

// 依赖桩集合：与 ChatWindow.dom.test.tsx 同源（useChat 会话态、子组件、取数接口）
const mocks = vi.hoisted(() => ({
  chat: {
    messages: [] as any[],
    isLoading: false,
    isBackendOnline: true,
    isBackendLoading: false,
    isBackendChecking: false,
    errorMessage: '',
    selectedLangs: ['en'] as string[],
    setSelectedLangs: vi.fn(),
    sendMessage: vi.fn(),
    stopGeneration: vi.fn(),
    clearMessages: vi.fn(),
    retryHealth: vi.fn(),
  },
  // ★ O-9 的判据输入面：getActiveTenantId 必须**可控可变**（批 I-10 的套路是给常量，
  //   这里同一响应要在 tid=0 / tid=3 两种上下文下各跑一遍，常量就做不了③的对照）。
  //   restoring 初值读 getAuthToken()：给非空串只影响 restoring 标记，本文件不消费。
  apiState: { tid: 0 },
}))

vi.mock('@/hooks/useChat', async () => {
  const { createContext } = await import('react')
  return { useChat: () => mocks.chat, PkgRefreshCtx: createContext<unknown>(null) }
})
// /api/me/package 各用例自行 mockResolvedValue；meContext 给「无数据」避免测试期打网络
const apiMocks = vi.hoisted(() => ({
  myPackage: vi.fn(),
  meContext: vi.fn(async () => ({ success: false })),
}))
vi.mock('@/api', () => ({
  myPackage: apiMocks.myPackage, meContext: apiMocks.meContext,
  getAuthToken: () => 'tok-under-test',
  getActiveTenantId: () => mocks.apiState.tid,
}))
vi.mock('@/api/translate', () => ({ estimateTranslation: vi.fn(async () => null) }))
vi.mock('./MessageBubble', () => ({
  default: ({ message }: any) => <div className="bubble-row" data-role={message.role}>{message.content}</div>,
}))
vi.mock('./modals', () => ({ FeedbackModalFromMessage: () => null }))
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-select" />,
  LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))

// O-9 统一响应体：平台与租户两种上下文喂**同一份**数据，界面差异只能来自判据本身
const PKG_BODY = {
  success: true,
  points_balance: 0, balance_sentences_approx: 0,
  points_used_today: 7,
  org_budget: { points_limit: 5000, points_used_this_month: 100, name: '企业预算组' },
  chat_max_chars: 20000,
}
// 词典等值期望：与组件同一拼法（fmtPoints(0) 防格式化器改动，字符串不写死）
const balanceText = () => tpl('chat.balanceTokens', { n: fmtPoints(0), s: '0' })

function renderWindow() {
  return render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={null as never}>
        <ChatWindow />
      </PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  apiMocks.meContext.mockImplementation(async () => ({ success: false }))
  apiMocks.myPackage.mockResolvedValue(PKG_BODY as unknown as never)
  mocks.apiState.tid = 0
  mocks.chat.messages = []
  setLang('zh')
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
  // 登录态直接喂 zustand（真 stores/auth，判据走真实现；tid 由 apiState 桩控制）
  useAuthStore.setState({ user: { id: 1, username: 'u', role: 'super_admin' } as never, restoring: false })
})

describe('ChatWindow 余额条 · O-9 平台上下文置空 + 双反向对照', () => {
  // ★ 2026-10-14 红框批（用户令撤掉「今日已耗 N 积分」那颗徽标）改写过 ① 的对照腿：
  //   旧写法拿 `chat-usage` 当"数据到了、余额栏整体挂了"的正向对照（当时它是栏里第三颗、
  //   且不受 O-9 守卫约束）。徽标撤下后那一格物理上不存在了，对照腿只能换地方——
  //   现在由 ② 与 ① **喂同一份 PKG_BODY** 承担对照：同一响应、只换角色，
  //   ② 渲染得出而 ① 渲染不出，才叫 O-9 守卫在起作用，而不是"接口没回/栏没挂"。
  //   ⚠️ 撤的是显示面：后端 `points_used_today` 出参一字未动，本文件不锁它。
  it('① 平台上下文（super_admin 且未选租户）：整条余额栏不得渲染（余额与预算文案都不出现）', async () => {
    renderWindow()
    // 先确认这条链真的跑到了（myPackage 被调用过），否则下面的负向只是"还没取到数"
    await waitFor(() => expect(apiMocks.myPackage).toHaveBeenCalled())
    expect(screen.queryByTestId('chat-balance')).toBeNull()
    // 等值负向锁：词典拼出的「余额 0 积分 ≈ 0 句」整串不得出现在界面上
    expect(document.body.textContent).not.toContain(balanceText())
    expect(document.body.textContent).not.toContain('企业预算组') // orgBudget 同守卫压掉
  })

  it('② 反向对照（普通租户角色 user）：同一响应必须渲染余额，文案等于词典拼装值', async () => {
    useAuthStore.setState({ user: { id: 2, username: 'u2', role: 'user' } as never })
    renderWindow()
    await waitFor(() => expect(screen.getByTestId('chat-balance')).toBeTruthy())
    // 等值（不是「包含余额二字」的单向锁）：0 也要如实渲染成词典模板串
    expect(screen.getByTestId('chat-balance').textContent).toBe(balanceText())
    // 预算徽标在租户上下文恢复渲染（守卫只压展示，不该顺手清掉另一块）
    expect(document.body.textContent).toContain('企业预算组')
  })

  it('③ 反向对照（super_admin 已选定租户 tid=3）：平台判据不成立，余额必须照常渲染', async () => {
    mocks.apiState.tid = 3 // authHeaders 只在 tid>0 下发 X-Tenant-ID ⇒ 服务端此刻看到的是真租户
    renderWindow()
    await waitFor(() => expect(screen.getByTestId('chat-balance')).toBeTruthy())
    expect(screen.getByTestId('chat-balance').textContent).toBe(balanceText())
  })

  // ★ 红框批的新锁：「今日已耗」在任何上下文都不许再出现在界面上。
  //   判据问的是那串出栈文案（zh 档＝用户点名看到的那一格），不是 testid；
  //   正向对照＝同一次渲染里余额文案必须在场，否则"没出现"只是整条没挂，这条负向就没有射程。
  //   ★ 反证实跑读数（2026-10-14）：往余额栏里装回一颗 chat-usage「今日已耗 0 积分」徽标
  //   ⇒ 只有本条红、①②③ 全绿（① 是平台上下文、整条余额栏都不挂，那颗徽标抓不到它），
  //   即这条负向腿的射程确实落在"徽标有没有出栈"上，而不是"余额挂没挂"。
  it('④ 红框批：租户上下文里余额照常渲染，但「今日已耗」字样不得出现', async () => {
    useAuthStore.setState({ user: { id: 2, username: 'u2', role: 'user' } as never })
    renderWindow()
    await waitFor(() => expect(screen.getByTestId('chat-balance')).toBeTruthy())
    expect(document.body.textContent).toContain('余额') // 对照腿：栏真的挂了、文案真的出了
    expect(document.body.textContent).not.toContain('今日已耗')
    expect(screen.queryByTestId('chat-usage')).toBeNull()
  })
})
