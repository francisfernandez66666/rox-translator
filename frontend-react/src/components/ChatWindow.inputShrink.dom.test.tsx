// ============================================================================
// components/ChatWindow.inputShrink.dom.test.tsx — 「输入过内容后清空，必须回缩」（★ 2026-10-14 红框批；
//   ★ 2026-10-15 用户令覆盖「空态自动收起」那一半：默认展开，收起改由用户点框头收起钮触发）
//
// 用户最先报的是**整张输入卡**（截图红框圈的是卡片，不是 textarea 那一行）：
//   空态时整卡收成一行的入口条（.cw-collapsed），打过字之后卡片展开成完整三段式；
//   旧形态把「打过字」也当成一次手动展开（onChange 里 setExpandedLatch(true)），这一闩此后再也不清，
//   于是删空输入后卡片永久停在展开档——用户看到的「不会回缩」就是这一条。
//
// ★ 2026-10-15 用户令：空态**默认展开**（「聊天框打开后自动最小化了，啥也看不到」）。收起只有一条触发
//   路径＝点框头「收起」钮（collapsedLatch）；只要输入非空／有会话就自动回到展开。所以本文件现在锁的是
//   「收起闩在时内容驱动展开/收起」＋「闩的唯一写点是收起钮」两条。
//
// 替身读数：jsdom 不做排版，scrollHeight 恒 0，故给 HTMLTextAreaElement.prototype 装一个
//   按当前 value 算的 getter（一行 40，与组件 `Math.max(40, …)` 下限同源）。
//
// 运行：npx vitest run src/components/ChatWindow.inputShrink.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor, act } from '@testing-library/react'
import ChatWindow from './ChatWindow'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang } from '@/i18n'
import { useAuthStore } from '@/stores/auth'

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
vi.mock('@/api', () => ({
  myPackage: vi.fn(async () => ({ success: true, points_balance: 100, balance_sentences_approx: 10, points_used_today: 0, chat_max_chars: 20000 })),
  meContext: vi.fn(async () => ({ success: false })),
  getAuthToken: () => 'tok-under-test',
  getActiveTenantId: () => 1,
}))
vi.mock('@/api/translate', () => ({ estimateTranslation: vi.fn(async () => null) }))
vi.mock('./MessageBubble', () => ({ default: () => <div /> }))
vi.mock('./modals', () => ({ FeedbackModalFromMessage: () => null }))
vi.mock('@/components/LangMultiSelect', () => ({
  default: () => <div data-stub="lang-select" />,
  LangChips: () => <div data-stub="lang-chips" />,
}))
vi.mock('@/components/ModeToggle', () => ({ default: () => <div data-stub="mode-toggle" /> }))

// 替身高度口径：一行 40（与组件里 `Math.max(40, …)` 的下限同源），每 40 个字符多一行。
function fakeScrollHeight(value: string): number {
  const lines = Math.max(1, Math.ceil((value || '').length / 40))
  return 40 * lines
}

const proto = (globalThis as any).HTMLTextAreaElement?.prototype
let originalDescriptor: PropertyDescriptor | undefined

beforeEach(() => {
  cleanup()
  setLang('zh')
  useAuthStore.setState({ user: { id: 2, username: 'u2', role: 'user' } as never, restoring: false })
  mocks.chat.messages = []
  mocks.chat.isLoading = false
  mocks.chat.errorMessage = ''
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
  if (!proto) throw new Error('jsdom 没给 HTMLTextAreaElement.prototype——本文件的替身读数无处可装')
  originalDescriptor = Object.getOwnPropertyDescriptor(proto, 'scrollHeight')
  Object.defineProperty(proto, 'scrollHeight', {
    configurable: true,
    get(this: HTMLTextAreaElement) { return fakeScrollHeight(this.value) },
  })
})

afterEach(() => {
  if (originalDescriptor) Object.defineProperty(proto, 'scrollHeight', originalDescriptor)
  else delete proto.scrollHeight
})

function renderWindow() {
  return render(<ToastProvider><ChatWindow /></ToastProvider>)
}
const dialog = () => document.querySelector('.cw-dialog') as HTMLElement
const box = () => screen.getByTestId('translate-input') as HTMLTextAreaElement

describe('ChatWindow 输入卡回缩 · 高度双向 + 整卡收起', () => {
  it('① 高度双向：长文→长高，清空→回到一行高（锁的是「只有一条驱动路径」这个契约）', async () => {
    renderWindow()
    expect(box().style.height).toBe('40px')

    fireEvent.change(box(), { target: { value: 'x'.repeat(200) } }) // 5 行 ⇒ 200px，未触到 240 上限
    await waitFor(() => expect(box().style.height).toBe('200px'))

    fireEvent.change(box(), { target: { value: '' } })
    await waitFor(() => expect(box().style.height).toBe('40px'), { timeout: 2000 })
  })

  // ★★ 本文件的靶心：收起闩在时，内容驱动展开/收起（打字→展开、删空→回收起档）。
  it('② 收起钮 → 打字自动展开 → 删空回收起档（闩在时内容驱动切换）', async () => {
    renderWindow()
    expect(dialog().className, '默认展开，不该自动收起').not.toContain('cw-collapsed')

    act(() => { fireEvent.click(document.querySelector('[data-testid="cw-collapse"]')!) })
    expect(dialog().className, '点了收起钮必须进收起档').toContain('cw-collapsed')

    fireEvent.change(box(), { target: { value: '随便打点字' } })
    await waitFor(() => expect(dialog().className).not.toContain('cw-collapsed'))

    fireEvent.change(box(), { target: { value: '' } })
    await waitFor(() => expect(dialog().className).toContain('cw-collapsed'), {
      timeout: 2000,
    })
  })

  it('③ 默认展开档：未点收起钮时，打字再删空仍保持展开（2026-10-15 用户令：不自动收起）', async () => {
    renderWindow()
    expect(dialog().className).not.toContain('cw-collapsed')

    fireEvent.change(box(), { target: { value: '一段要删掉的草稿' } })
    await waitFor(() => expect(dialog().className).not.toContain('cw-collapsed'))
    fireEvent.change(box(), { target: { value: '' } })
    await waitFor(() => expect(box().style.height).toBe('40px'))
    // 没点过收起钮 ⇒ 删空后仍保持展开（框头/工具条不抽走）
    expect(dialog().className, '默认展开档下删空不该把它弹回收起档').not.toContain('cw-collapsed')
  })

  it('④ 有会话时空输入不收起（历史可见性优先，这条与闩无关但同判据面）', () => {
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '已有会话', timestamp: Date.now() }]
    renderWindow()
    expect(dialog().className).not.toContain('cw-collapsed')
  })

  it('⑤ 接线锁：置闩只许有一个入口（框头「收起」钮），onChange 里不许再置位', async () => {
    // 摘掉 [input] 依赖或把 setExpandedLatch 塞回 onChange，上面的行为腿会红；
    // 这条源码级判据的作用是**把"为什么不能那么写"钉在文件里**，防下一个人顺手改回去。
    const { readFileSync, existsSync } = await import('node:fs')
    // jsdom 环境下 import.meta.url 不是 file: 协议（fileURLToPath 当场抛），
    // 而按 cwd 相对路径读又会在"从仓库根跑 vitest"时读不到——**读不到必须报错，不许静默跳过**
    // （那正是本仓反复踩的"闸门看不见文件却判绿"形态）。所以枚举候选、一个都不在即红。
    let here = ''
    try {
      const { fileURLToPath } = await import('node:url')
      const p = fileURLToPath(new URL('./ChatWindow.tsx', import.meta.url))
      if (existsSync(p)) here = p
    } catch { /* jsdom 下预期走这条，交给下面的候选 */ }
    for (const cand of ['src/components/ChatWindow.tsx', 'frontend-react/src/components/ChatWindow.tsx']) {
      if (!here && existsSync(cand)) here = cand
    }
    if (!here) throw new Error('接线锁读不到 ChatWindow.tsx（候选全落空）⇒ 判据没有射程，不许当它过了')
    const raw = readFileSync(here, 'utf8')
    // ★ 判据必须先剥注释再看代码：本文件那几条说明注释里就写着 setCollapsedLatch(true) 这个字样
    //   （解释触发路径），不剥就会把"注释冒充实现"读成"第二处写点"。
    const src = raw.replace(/^\s*\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '')
    // 高度只由 [input] 的 effect 驱动（发送后清空／打字／换行共用同一条路径）
    expect(src).toMatch(/useEffect\(\(\) => \{ autoResize\(\) \}, \[input\]\)/)
    // 收起闩的唯一写点＝collapseDialog（框头「收起」钮）；出现第二处＝又有人把别的动作当成收起
    const hits = src.match(/setCollapsedLatch\(true\)/g) || []
    expect(hits.length, `setCollapsedLatch(true) 只许出现一次（框头收起钮），实读 ${hits.length} 次`).toBe(1)
    expect(src.split('setCollapsedLatch(true)')[0], '唯一置位点必须在 collapseDialog 体内').toContain('function collapseDialog() {')
    // 反证射程自证：往 onChange 里塞一句置位（模拟"打字也能收起"的错误形态），计数判据必须翻红
    const MUT = raw.replace('onChange={(e) => { setInput(e.target.value) }}',
      'onChange={(e) => { setInput(e.target.value); if (!e.target.value.trim()) setCollapsedLatch(true) }}')
    expect(MUT, '反证夹具没替换成功＝判据对象写漂了（onChange 那一行的形状变了）').not.toBe(raw)
    const mutSrc = MUT.replace(/^\s*\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '')
    expect((mutSrc.match(/setCollapsedLatch\(true\)/g) || []).length, '装回第二处置位后仍判 1 处＝这条计数腿没有射程').toBe(2)
  })
})
