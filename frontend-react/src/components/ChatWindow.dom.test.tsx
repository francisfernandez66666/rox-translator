// ============================================================================
// ChatWindow.dom.test.tsx — 即时翻译工作台组件测试（★ 形态定档：整屏合并对话框，2026-09-22 〇-LJ）
// 锁住四条口径：
//   ① 对话框合并：原文输入与译文结果气泡在同一个 .cw-dialog 容器内（气泡不再排在框外），
//      合并框吃满剩余高度由 CSS flex 承担，此处断言 DOM 归属关系；
//   ② 即时翻译不再支持文件翻译：不得再渲染任何 file input / 上传按钮，文案不得提「上传」；
//   ③ 缩翻控件在文件入口下线后仍有意义：勾选后把 max_length 透传给 sendMessage（文本路径）；
//   ④ 提示词去文件化：输入框占位与欢迎语按新文案渲染（防止改回「或点＋上传文件」）。
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

describe('即时翻译工作台（#36 合并对话框）', () => {
  it('① 原文输入与结果气泡同在一个对话框容器内', async () => {
    mocks.chat.messages = [
      { id: 'u1', role: 'user', content: '早上好', timestamp: Date.now() },
      { id: 'a1', role: 'assistant', content: 'Good morning', timestamp: Date.now() },
    ]
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog')
    expect(dialog).toBeTruthy()
    await waitFor(() => expect(container.querySelectorAll('.bubble-row').length).toBe(2))
    // 气泡与输入框都在合并框内（旧版气泡排在输入卡之外）
    expect(dialog!.contains(container.querySelector('[data-testid="translate-input"]')!)).toBe(true)
    expect(dialog!.contains(container.querySelector('.bubble-row')!)).toBe(true)
    // 内部滚动区承载消息（整页不再滚动）
    const body = dialog!.querySelector('.cw-dialog-body')
    expect(body && body.contains(container.querySelector('.bubble-row')!)).toBe(true)
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
})
