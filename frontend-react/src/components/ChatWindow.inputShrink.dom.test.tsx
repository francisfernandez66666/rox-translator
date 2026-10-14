// ============================================================================
// components/ChatWindow.inputShrink.dom.test.tsx — 「输入过内容后清空，必须回缩」（★ 2026-10-14 红框批）
//
// 用户报的是**整张输入卡**（截图红框圈的是卡片，不是 textarea 那一行）：
//   空态时整卡收成一行的入口条（.cw-collapsed），打过字之后卡片展开成完整三段式；
//   旧形态把「打过字」也当成一次手动展开（onChange 里 setExpandedLatch(true)），这一闩此后再也不清，
//   于是删空输入后卡片永久停在展开档——用户看到的「不会回缩」就是这一条。
//   ⇒ 修法＝闩只由点击入口条（expandFromEntry）置位；打字不再置位，collapsed 重新由内容决定。
//   决策⑩那半条口径仍然保留：**用户明确点开过**这张卡之后不再自动收起（边打字边抽走工具条才是打扰）。
//
// ★★ 归因订正（反证推翻了我先前的假设，留在这里防下一个人按错的因果改回去）：
//   本批第一稿把缺陷记成「onChange 量到旧的 scrollHeight，所以 textarea 能长不能缩」。
//   把修法摘掉、按旧形态跑一遍，jsdom 里清空那一枪**照样缩回 40px**——因为浏览器在 change 事件
//   发出之前就已经把新值写进 DOM，量到的一直是当前内容。那条因果是错的。
//   下面第一条（高度双向）因此**对本缺陷没有判别力**，它锁的是「高度只有一条驱动路径」这个契约；
//   真正有射程的是第二条（整卡回缩）与第四条（源码接线）——反证只验这两条。
//
// 替身读数：jsdom 不做排版，scrollHeight 恒 0，故给 HTMLTextAreaElement.prototype 装一个
//   按当前 value 算的 getter（一行 40，与组件 `Math.max(40, …)` 下限同源）。
//
// ★ 反证实跑读数（2026-10-14 本批，两枚各自只点破自己那条腿）：
//   M1 把 onChange 里的 `setExpandedLatch(true)` 装回旧形态 ⇒ ②⑤ 红、①③④ 全绿
//      （③ 仍绿证明闩没被整个删掉，只是不再被打字顺带置位）；
//   M2 把 `useEffect(…, [input])` 的依赖退回 `[]` ⇒ ①⑤ 红、②③④ 全绿
//      （整卡回缩与这一维无关，两条腿互不遮蔽）。
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

  // ★★ 本文件的靶心：这一条在旧实现下**必红**（闩被打字永久置位）。
  it('② 整卡回缩：打字展开 → 删空必须回到单行入口条（cw-collapsed）', async () => {
    renderWindow()
    expect(dialog().className, '空态本就该收起').toContain('cw-collapsed')

    fireEvent.change(box(), { target: { value: '随便打点字' } })
    await waitFor(() => expect(dialog().className).not.toContain('cw-collapsed'))

    fireEvent.change(box(), { target: { value: '' } })
    await waitFor(() => expect(dialog().className).toContain('cw-collapsed'), {
      timeout: 2000,
    })
  })

  it('③ 手动点开过之后不许因删空而收起（决策⑩那半条口径保留，防把闩整个删掉）', async () => {
    renderWindow()
    const entry = dialog()
    act(() => { fireEvent.click(entry) })
    expect(dialog().className).not.toContain('cw-collapsed')

    fireEvent.change(box(), { target: { value: '一段要删掉的草稿' } })
    await waitFor(() => expect(dialog().className).not.toContain('cw-collapsed'))
    fireEvent.change(box(), { target: { value: '' } })
    await waitFor(() => expect(box().style.height).toBe('40px'))
    // 闩在：删空后仍保持展开（框头/工具条不抽走）
    expect(dialog().className, '用户明确点开过这张卡，删空也不该把它弹回收起档').not.toContain('cw-collapsed')
  })

  it('④ 有会话时空输入不收起（历史可见性优先，这条与闩无关但同判据面）', () => {
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '已有会话', timestamp: Date.now() }]
    renderWindow()
    expect(dialog().className).not.toContain('cw-collapsed')
  })

  it('⑤ 接线锁：置闩只许有一个入口（点击展开），onChange 里不许再置位', async () => {
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
    // ★ 判据必须先剥注释再看代码：本文件那几条说明注释里就写着 setExpandedLatch(true) 这个字样
    //   （解释旧形态错在哪），不剥就会把"注释冒充实现"读成"第二处写点"，
    //   而下一个改代码的人会顺着注释去找那个根本不存在的 onChange 置位点。
    const src = raw.replace(/^\s*\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '')
    // 高度只由 [input] 的 effect 驱动（发送后清空／打字／换行共用同一条路径）
    expect(src).toMatch(/useEffect\(\(\) => \{ autoResize\(\) \}, \[input\]\)/)
    // 闩的唯一写点＝expandFromEntry；出现第二处＝又有人把「打过字」当成「用户想展开」
    const hits = src.match(/setExpandedLatch\(true\)/g) || []
    expect(hits.length, `setExpandedLatch(true) 只许出现一次（点击展开），实读 ${hits.length} 次`).toBe(1)
    expect(src.split('setExpandedLatch(true)')[0], '唯一置位点必须在 expandFromEntry 体内').toContain('function expandFromEntry() {')
    // 反证射程自证：把 onChange 里那句置位装回去（模拟旧形态），计数判据必须翻红而不是静默通过
    const MUT = raw.replace('onChange={(e) => { setInput(e.target.value) }}',
      'onChange={(e) => { setInput(e.target.value); if (e.target.value.trim()) setExpandedLatch(true) }}')
    expect(MUT, '反证夹具没替换成功＝判据对象写漂了（onChange 那一行的形状变了）').not.toBe(raw)
    const mutSrc = MUT.replace(/^\s*\/\/.*$/gm, '').replace(/\/\*[\s\S]*?\*\//g, '')
    expect((mutSrc.match(/setExpandedLatch\(true\)/g) || []).length, '装回旧形态后仍判 1 处＝这条计数腿没有射程').toBe(2)
  })
})
