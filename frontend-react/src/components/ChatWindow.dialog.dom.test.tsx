// ============================================================================
// ChatWindow.dialog.dom.test.tsx — ★ 决策⑩ 对话框形态四态断言（2026-10-10）
// 锁住：
//   ① 空态收起：空输入且无会话 ⇒ 整卡收成单行入口条（cw-collapsed 档）；
//   ② 点击入口条展开（收起是 CSS 档不是卸载：框体仍在、输入行仍在，收起档一撤全量结构即回）；
//   ③ 有会话不收起（历史可见性优先，只收输入框不做整卡最小化）；
//   ④ 默认高度档：未拖过时无内联高度（min(70vh,640px) 由 CSS 承担）；
//      拖拽可调、钳制 320px–90vh、松手记 localStorage、双击回默认并清记忆。
// 形态沿革见 ChatWindow.tsx 文件头（〇-LK/〇-M 定档 ＋ 本批决策⑩）。
// 运行：npx vitest run src/components/ChatWindow.dialog.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent, act } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { PkgRefreshCtx } from '@/hooks/useChat'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'

// 依赖桩：与 ChatWindow.dom.test.tsx 同一形态（useChat 状态桩 + 取数接口桩 + 子组件桩）
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

vi.mock('@/hooks/useChat', async () => {
  const { createContext } = await import('react')
  return { useChat: () => mocks.chat, PkgRefreshCtx: createContext<unknown>(null) }
})
const apiMocks = vi.hoisted(() => ({
  myPackage: vi.fn(async () => ({ success: false })),
  meContext: vi.fn(async () => ({ success: false })),
}))
vi.mock('@/api', () => ({
  myPackage: apiMocks.myPackage, meContext: apiMocks.meContext,
  getAuthToken: () => '', getActiveTenantId: () => 0,
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

function renderWindow(hub: unknown = null) {
  return render(
    <ToastProvider>
      <PkgRefreshCtx.Provider value={hub as never}>
        <ChatWindow />
      </PkgRefreshCtx.Provider>
    </ToastProvider>,
  )
}

const H_KEY = 'lc-cw-dialog-h'

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  apiMocks.myPackage.mockImplementation(async () => ({ success: false }))
  setLang('zh')
  mocks.chat.messages = []
  mocks.chat.isLoading = false
  mocks.chat.errorMessage = ''
  mocks.chat.selectedLangs = ['en']
  localStorage.removeItem(H_KEY)
  localStorage.removeItem('translate_mode')
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
})

describe('对话框形态四态（决策⑩）', () => {
  it('① 空态收起：空输入且无会话 ⇒ 整卡收成单行入口条（cw-collapsed）', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog')!
    expect(dialog).toBeTruthy()
    expect(dialog.className).toContain('cw-collapsed')
    // 收起档下输入行仍在（入口条本体就是 composer 的输入行），框头/滚动区被 CSS 藏起
    expect(dialog.querySelector('[data-testid="translate-input"]')).toBeTruthy()
    expect(dialog.querySelector('.cw-dialog-body')).toBeTruthy() // DOM 在、display:none（CSS 档）
  })

  it('② 点击入口条展开：收起档撤除、恢复完整三段式结构', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog')!
    expect(dialog.className).toContain('cw-collapsed')
    act(() => { fireEvent.click(dialog) })
    expect(dialog.className).not.toContain('cw-collapsed')
    // 展开后输入行与工具条都可见（收起只是 CSS 档，展开即回完整结构）
    expect(dialog.querySelector('.cw-toolbar')).toBeTruthy()
  })

  it('③ 有会话不收起：空输入但消息流非空 ⇒ 不进 cw-collapsed 档（历史可见性优先）', () => {
    mocks.chat.messages = [
      { id: 'u1', role: 'user', content: '早上好', timestamp: Date.now() },
      { id: 'a1', role: 'assistant', content: 'Good morning', timestamp: Date.now() },
    ]
    const { container } = renderWindow()
    expect(container.querySelector('.cw-dialog')!.className).not.toContain('cw-collapsed')
  })

  it('④ 默认高度档：未拖过无内联高度（min(70vh,640px) 由 CSS 承担）', () => {
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '已有会话', timestamp: Date.now() }]
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    expect(dialog.style.height).toBe('')
  })

  it('⑤ 拖拽调高：方向向上变高、钳制 320–90vh、松手记 localStorage', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    // 先展开（空态默认收起；收起档内联高度不生效——见 ChatWindow.tsx 内联高度的条件）
    fireEvent.click(dialog)
    expect(dialog.className).not.toContain('cw-collapsed')
    const handle = container.querySelector('[data-testid="cw-resize-handle"]') as HTMLElement
    expect(handle).toBeTruthy()
    // 起拖（clientY=500，起点取默认档 JS 读数 min(768×0.7, 640)=538）→ 上移 100px ⇒ 638
    fireEvent.mouseDown(handle, { clientY: 500 })
    fireEvent.mouseMove(window, { clientY: 400 })
    expect(dialog.style.height).toBe('638px')
    // 继续向上狠拖 ⇒ 变高，钳到 90vh 上限（jsdom innerHeight=768 ⇒ 691）
    fireEvent.mouseMove(window, { clientY: -2000 })
    expect(Number.parseInt(dialog.style.height, 10)).toBe(Math.round(768 * 0.9))
    // 向下狠拖 ⇒ 变矮，钳到 320px 下限
    fireEvent.mouseMove(window, { clientY: 2000 })
    expect(dialog.style.height).toBe('320px')
    // 松手落库（存的是最后停在的下限档）
    fireEvent.mouseUp(window)
    expect(localStorage.getItem(H_KEY)).toBe('320')
  })

  it('⑥ 会话恢复钳制 + 双击回默认并清记忆', () => {
    localStorage.setItem(H_KEY, '99999') // 上次会话留下的越界记忆
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '已有会话', timestamp: Date.now() }]
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    // 恢复时同样钳到 90vh，不许越界占满整屏
    expect(Number.parseInt(dialog.style.height, 10)).toBe(Math.round(768 * 0.9))
    // 双击手柄 ⇒ 回默认档（内联高度清除）+ 记忆清空
    fireEvent.doubleClick(container.querySelector('[data-testid="cw-resize-handle"]')!)
    expect(dialog.style.height).toBe('')
    expect(localStorage.getItem(H_KEY)).toBeNull()
  })
})
