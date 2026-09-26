// ============================================================================
// ChatWindow.dom.test.tsx — 即时翻译工作台组件测试（★ 形态定档：整屏合并 AI 对话框 + 输入区单卡单工具条，2026-09-22 〇-LK / 09-23 〇-M）
// 锁住五条口径：
//   ① 对话框合并 + 对话页布局：气泡与输入框同在一张 .cw-dialog 内，且**消息流在上、
//      输入区（textarea）常驻底部 .cw-dialog-foot**（输入摆最上面是用户判错的形态）；
//      合并框吃满剩余高度由 CSS flex 承担，此处断言 DOM 归属与先后顺序；
//   ② 即时翻译不再支持文件翻译：不得再渲染任何 file input / 上传按钮，文案不得提「上传」；
//   ③ 缩翻控件在文件入口下线后仍有意义：勾选后把 max_length 透传给 sendMessage（文本路径）；
//   ④ 提示词去文件化：输入框占位与欢迎语按新文案渲染（防止改回「或点＋上传文件」）；
//   ⑤ ★ 〇-M 输入区压缩：textarea 与「语种/模式/缩翻/主按钮」同处一张 .cw-composer 内的
//      唯一一排 .cw-toolbar，框脚不再排第二、第三行（旧的 .cw-composer-label / .cw-dialog-acts 不得复活）。
// 运行：npx vitest run src/components/ChatWindow.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { PkgRefreshCtx } from '@/hooks/useChat'
import { myPackage } from '@/api'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'

// 依赖桩集合（hoisted 供 vi.mock 工厂引用）：useChat 会话状态、各子组件与取数接口
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
}))

// ★ F-48（批 I-5）：ChatWindow 除 useChat 外还要从本模块取 PkgRefreshCtx（余额条订阅广播），
// 因此桩工厂必须一并导出这颗 Context——否则 useContext(undefined) 直接抛
// 「Please pass a Context object as the first argument」，整文件红。
// 真实 context 的语义（单槽 vs 广播）在 hooks 侧测（useChat.pkgRefresh.dom.test.tsx 锁④），
// 这里用 React 现造一颗同名 Context 只为把 Provider 喂进组件。
vi.mock('@/hooks/useChat', async () => {
  const { createContext } = await import('react')
  return { useChat: () => mocks.chat, PkgRefreshCtx: createContext<unknown>(null) }
})
// 余额/健康等非本次断言重点，一律给「无数据」假实现，避免测试期打网络
// （★ F-48 锁⑥ 自行 vi.mocked 改返回值以走「有真实余额」分支，默认形态仍是无数据）
const apiMocks = vi.hoisted(() => ({
  myPackage: vi.fn(async () => ({ success: false })),
  meContext: vi.fn(async () => ({ success: false })),
}))
// ★ O-9（批 I-10）：ChatWindow 现在要读登录态（平台上下文判定 isPlatformBillingContext →
//   @/stores/auth），而该 store 模块初始化就会调 getAuthToken()、判定里再调 getActiveTenantId()。
//   本文件的 @/api 桩是「整模块替换」形态，缺这两个导出会直接让套件起不来
//   （vitest 报 No "getAuthToken" export is defined on the "@/api" mock）。
//   给空值即可：空 token=未登录、tid=0，配合下面 user.role 的默认口径，
//   余额条仍按「租户上下文」渲染，不改变本文件既有断言的语义。
vi.mock('@/api', () => ({
  myPackage: apiMocks.myPackage, meContext: apiMocks.meContext,
  getAuthToken: () => '', getActiveTenantId: () => 0,
}))
vi.mock('@/api/translate', () => ({ estimateTranslation: vi.fn(async () => null) }))
// 子组件与本用例无关，桩化后可稳定断言气泡的 DOM 归属
vi.mock('./MessageBubble', () => ({
  default: ({ message }: any) => <div className="bubble-row" data-role={message.role}>{message.content}</div>,
}))
vi.mock('./modals', () => ({ FeedbackModalFromMessage: () => null }))
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-select" />,
  LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))

// ChatWindow 内部用 useToast 提示，必须在 ToastProvider 下渲染
// ★ F-48：hub 传入时外层套 PkgRefreshCtx.Provider（余额条订阅广播）；缺省（null）即
// 后台路由那种「没有枢纽」的形态，组件必须照常渲染不炸。
function renderWindow(hub: unknown = null) {
  return render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={hub as never}>
        <ChatWindow />
      </PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  apiMocks.myPackage.mockImplementation(async () => ({ success: false }))
  setLang('zh')
  mocks.chat.messages = []
  mocks.chat.isLoading = false
  mocks.chat.errorMessage = ''
  mocks.chat.selectedLangs = ['en']
  localStorage.removeItem('translate_mode')
  // jsdom 不实现 scrollTo，组件滚底会踩空
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
})

describe('即时翻译工作台（#36 合并对话框）', () => {
  it('① 单框结构：消息流在上、输入区常驻底部（★ 〇-LK 形态定稿）', async () => {
    mocks.chat.messages = [
      { id: 'u1', role: 'user', content: '早上好', timestamp: Date.now() },
      { id: 'a1', role: 'assistant', content: 'Good morning', timestamp: Date.now() },
    ]
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog')
    expect(dialog).toBeTruthy()
    await waitFor(() => expect(container.querySelectorAll('.bubble-row').length).toBe(2))
    // 气泡与输入框都在合并框内（旧版气泡排在输入卡之外）
    const input = container.querySelector('[data-testid="translate-input"]')!
    expect(dialog!.contains(input)).toBe(true)
    expect(dialog!.contains(container.querySelector('.bubble-row')!)).toBe(true)
    // 内部滚动区只承载消息（整页不再滚动）
    const body = dialog!.querySelector('.cw-dialog-body')!
    expect(body.contains(container.querySelector('.bubble-row')!)).toBe(true)
    // ★ 输入区必须在框脚（composer），不能回到滚动区顶部——
    //   「对话框放最上面」是用户 2026-09-22 明确判错的形态，这条是本形态的唯一源码级锁。
    const foot = dialog!.querySelector('.cw-dialog-foot')!
    expect(foot.contains(input), '输入框必须贴在对话框底部（.cw-dialog-foot 内）').toBe(true)
    expect(body.contains(input), '输入框不得再出现在消息流里').toBe(false)
    // DOM 顺序即视觉顺序：消息流在前、输入区在后
    expect(!!(body.compareDocumentPosition(foot) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
  })

  it('② 不再渲染任何文件上传入口，页面文案不提「上传/文件翻译」', () => {
    const { container } = renderWindow()
    expect(container.querySelector('input[type="file"]')).toBeNull()
    expect(container.innerHTML).not.toMatch(/TRANSLATE_FILE_ACCEPT|validateTranslateFile/)
    const text = container.textContent || ''
    expect(text).not.toMatch(/上传/)
    expect(text).not.toMatch(/文件翻译/)
  })

  it('③ 缩翻勾选后把 max_length 透传给文本翻译（文件入口移除后控件不失义）', () => {
    renderWindow()
    const box = screen.getByRole('checkbox')
    fireEvent.click(box)
    const num = document.querySelector('input[type="number"]') as HTMLInputElement
    fireEvent.change(num, { target: { value: '120' } })
    fireEvent.change(screen.getByTestId('translate-input'), { target: { value: '今天天气不错' } })
    fireEvent.click(screen.getByText('翻译'))
    expect(mocks.chat.sendMessage).toHaveBeenCalledTimes(1)
    const [text, options] = mocks.chat.sendMessage.mock.calls[0]
    expect(text).toBe('今天天气不错')
    expect(options).toMatchObject({ target_langs: ['en'], max_length: 120 })
  })

  it('③b 未勾选缩翻时不下发 max_length（后端按未启用处理）', () => {
    renderWindow()
    fireEvent.change(screen.getByTestId('translate-input'), { target: { value: 'hello' } })
    fireEvent.click(screen.getByText('翻译'))
    const [, options] = mocks.chat.sendMessage.mock.calls[0]
    expect(options.max_length).toBeUndefined()
  })

  it('④ 提示词已去文件化：占位符与欢迎语按新文案渲染', () => {
    renderWindow()
    const ta = screen.getByTestId('translate-input') as HTMLTextAreaElement
    expect(ta.placeholder).toBe('输入要翻译的文本')
    expect(ta.getAttribute('aria-label')).toBe('输入要翻译的文本')
    expect(screen.getByText('输入文本，选择目标语言后开始翻译')).toBeTruthy()
    expect(screen.getByText(/译文会直接显示在这个对话框里/)).toBeTruthy()
  })

  // ★ 〇-M（2026-09-23）形态锁：输入区压成「一张内凹输入卡 = 输入行 + 一行工具条」。
  //   用户判旧形态「太长太占空间」，四排折成一排；这里钉的是**归属与排数**，
  //   真实像素高度由 pixel_uat P2b 的运行时等值锁承担（jsdom 无布局，量不到高度）。
  it('⑤ 输入区单卡单工具条：语种/模式/缩翻/主按钮全在 .cw-toolbar 同一排，无独立语种行', () => {
    const { container } = renderWindow()
    const composer = container.querySelector('.cw-composer')!
    const toolbar = container.querySelector('.cw-toolbar')!
    // 输入卡里只有两层：textarea 与工具条（工具条必须是 composer 的子节点，不能再浮在卡外）
    expect(composer.contains(toolbar)).toBe(true)
    expect(composer.querySelector('textarea')).toBeTruthy()
    expect(container.querySelectorAll('.cw-toolbar').length, '工具条只允许一排').toBe(1)
    // 四件套全部收进这一排：目标语言、已选 chips、模式、主按钮（缩翻紧随其后一并校验）
    for (const sel of ['[data-stub="lang-select"]', '[data-stub="lang-chips"]', '[data-stub="mode-toggle"]']) {
      expect(toolbar.querySelector(sel), `${sel} 必须落在 .cw-toolbar 内`).toBeTruthy()
    }
    expect(toolbar.contains(screen.getByRole('checkbox'))).toBe(true)
    expect(toolbar.contains(screen.getByText('翻译'))).toBe(true)
    // 负向锁（运行时 DOM，不受「旧写法说明注释」干扰）：旧的四排结构不得复活
    expect(container.querySelector('.cw-composer-label'), '「原文/自动检测」独立标签行已折进工具条胶囊').toBeNull()
    expect(container.querySelector('.cw-dialog-acts'), '操作行已并入 .cw-toolbar，不得再单独成排').toBeNull()
    // 框脚只剩输入卡本身（错误提示行仅在有 errorMessage 时追加）
    expect(container.querySelectorAll('.cw-dialog-foot > *').length, '框脚内除输入卡外不得再排其他控件').toBe(1)
  })

  // ★ F-48（〇-U 批 I-5 2026-09-26）：余额条的刷新触发点从「chat.messages.length 变化」
  // 改成「订阅 PkgRefreshCtx 枢纽、由聊天层在流终态后广播」。旧判据两头都错：
  //   ① 关页 / 清空记录 / 切账号会让长度变小或归零再变大 → 白打一枪 myPackage；
  //   ② 同一条气泡的流式回写不改数组长度，一轮翻译真正扣点的 done 帧可能一枪都不打
  //     → 界面停在扣费前的余额（客户视角就是「翻译完不知道还剩多少」）。
  // 本用例用**真实数值回执**（success + points_balance）驱动，断三件事：
  //   a) 余额条确实订阅了枢纽（count===1）；b) 一次广播恰打一次 myPackage 且文案变成新值；
  //   c) 消息数组变长本身不再触发刷新（旧触发点的负向锁）。
  it('⑥ 余额条由枢纽广播驱动刷新：一次广播恰一次 myPackage、文案变成新值；消息数变化不再触发（★ F-48）', async () => {
    const subs = new Set<() => void>()
    // 本地最小枢纽：本文件把 @/hooks/useChat 整模块桩掉（取不到真的 createPkgRefreshHub），
    // 真枢纽自身的广播/退订/抛错语义在 useChat.pkgRefresh.dom.test.tsx 锁④⑤⑥⑦ 承担，
    // 这里只验「ChatWindow 是否按同一契约订阅与消费广播」。
    const hub = {
      subscribe: (fn: () => void) => { subs.add(fn); return () => { subs.delete(fn) } },
      emit: () => { for (const fn of [...subs]) fn() },
      count: () => subs.size,
    }
    // 第 1 次（挂载首拉）回 500，第 2 次起回 480：只有「广播后重新拉到新值」才会让文案变化
    let calls = 0
    vi.mocked(myPackage).mockImplementation(async () => {
      calls += 1
      return calls === 1
        ? { success: true, points_balance: 500, balance_sentences_approx: 100, points_used_today: 10 }
        : { success: true, points_balance: 480, balance_sentences_approx: 96, points_used_today: 30 }
    })
    const { rerender } = renderWindow(hub)
    await waitFor(() => expect(screen.getByTestId('chat-balance').textContent).toContain('500'))
    expect(hub.count(), '余额条必须挂在广播名单里（订阅丢失＝旧触发点被删空、余额永远不刷）').toBe(1)
    expect(myPackage).toHaveBeenCalledTimes(1)

    // a) 广播 → 恰一次刷新 → 文案换新值
    act(() => { hub.emit() })
    await waitFor(() => expect(screen.getByTestId('chat-balance').textContent).toContain('480'))
    expect(myPackage, '一次广播只准打一次 myPackage（不得重复订阅）').toHaveBeenCalledTimes(2)

    // b) 消息数组变长不再触发刷新（旧 messages.length 触发点的负向锁）
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '新增气泡', timestamp: Date.now() }]
    rerender(
      <ToastProvider>
        <PkgRefreshCtx.Provider value={hub}>
          <ChatWindow />
        </PkgRefreshCtx.Provider>
      </ToastProvider>,
    )
    await act(async () => { await Promise.resolve() })
    expect(myPackage, '消息数变化不得再触发刷新（F-48 旧判据白打请求）').toHaveBeenCalledTimes(2)

    // c) 卸载即退订：广播给已卸载的余额位不得再打接口
    cleanup()
    hub.emit()
    await act(async () => { await Promise.resolve() })
    expect(myPackage, '组件卸载后不得继续被广播叫醒（退订丢失＝后台路由下白打请求）').toHaveBeenCalledTimes(2)
  })
})
