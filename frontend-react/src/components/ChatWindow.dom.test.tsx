// ============================================================================
// ChatWindow.dom.test.tsx — 即时翻译工作台组件测试（★ 2026-09-22 形态回退版）
// 锁住四条口径：
//   ① 形态＝两段式：吸顶输入卡在上、译文结果气泡在其下方展开（两者不同容器），
//      这条是「回退形态、保留功能」的形态锁——一旦有人再把输入和气泡合并进同一个框，即红灯；
//   ② 即时翻译不支持文件翻译（#36 决定保留）：不得再渲染任何 file input / 上传按钮，文案不得提「上传」；
//   ③ 缩翻控件有意义：勾选后把 max_length 透传给 sendMessage（文本路径）；
//   ④ 提示词与欢迎语按两段式文案渲染（十二语种 welcomeSub 同步为「显示在输入框下方」）。
// 运行：npx vitest run src/components/ChatWindow.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'

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

vi.mock('@/hooks/useChat', () => ({ useChat: () => mocks.chat }))
// 余额/健康等非本次断言重点，一律给「无数据」假实现，避免测试期打网络
vi.mock('@/api', () => ({
  myPackage: vi.fn(async () => ({ success: false })),
  meContext: vi.fn(async () => ({ success: false })),
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

/** ChatWindow 内部用 useToast 提示，必须在 ToastProvider 下渲染 */
function renderWindow() {
  return render(<ToastProvider><ChatWindow /></ToastProvider>)
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  setLang('zh')
  mocks.chat.messages = []
  mocks.chat.isLoading = false
  mocks.chat.errorMessage = ''
  mocks.chat.selectedLangs = ['en']
  localStorage.removeItem('translate_mode')
  // jsdom 不实现 scrollTo，组件滚底会踩空
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
})

describe('即时翻译工作台（吸顶输入卡 + 下方气泡列表）', () => {
  it('① 输入卡吸顶在上、结果气泡在其下方展开（两段式形态锁）', async () => {
    mocks.chat.messages = [
      { id: 'u1', role: 'user', content: '早上好', timestamp: Date.now() },
      { id: 'a1', role: 'assistant', content: 'Good morning', timestamp: Date.now() },
    ]
    const { container } = renderWindow()
    const stage = container.querySelector('.cw-stage')
    const card = container.querySelector('.cw-card') as HTMLElement | null
    const results = container.querySelector('.cw-results')
    expect(stage).toBeTruthy()
    expect(card).toBeTruthy()
    expect(results).toBeTruthy()
    await waitFor(() => expect(container.querySelectorAll('.bubble-row').length).toBe(2))
    // 卡与结果区同属一块滚动舞台，且卡排在结果区之前（译文在输入下方展开）
    expect(stage!.contains(card!)).toBe(true)
    expect(stage!.contains(results!)).toBe(true)
    expect(card!.compareDocumentPosition(results!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // 输入框只在吸顶卡内，不在结果区里（合并形态的判据正是两者同容器）
    const ta = container.querySelector('[data-testid="translate-input"]')!
    expect(card!.contains(ta)).toBe(true)
    expect(results!.contains(ta)).toBe(false)
    expect(results!.contains(container.querySelector('.bubble-row')!)).toBe(true)
    // 吸顶由内联样式承担：jsdom 不解析 <style>，故只认 element.style，合并版没有这条即红灯
    expect(card!.style.position).toBe('sticky')
    expect(card!.style.top).toBe('0px')
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

  it('④ 提示词与欢迎语按两段式口径渲染（不提文件、不提「对话框内」）', () => {
    renderWindow()
    const ta = screen.getByTestId('translate-input') as HTMLTextAreaElement
    expect(ta.placeholder).toBe('输入要翻译的文本')
    expect(ta.getAttribute('aria-label')).toBe('输入要翻译的文本')
    expect(screen.getByText('输入文本，选择目标语言后开始翻译')).toBeTruthy()
    expect(screen.getByText(/译文会显示在输入框下方/)).toBeTruthy()
  })
})
