// ============================================================================
// ChatWindow.dialog.dom.test.tsx — ★ 对话框形态五态断言（2026-10-10 决策⑩，2026-10-13 用户令① 重钉，
//   2026-10-15 用户令覆盖「空态自动收起」那一半）
// 锁住：
//   ① 空态**默认展开**：空输入且无会话 ⇒ 也不进 cw-collapsed（2026-10-15 用户令：聊天框不该一进来就最小化）；
//   ② 点框头「收起」钮才收成单行入口条；点入口条恢复完整三段式（收起是 CSS 档不是卸载）；
//   ③ 有会话时即便点过收起也不收（历史可见性优先）；
//   ④ 默认高度档＝**吃满页眉与页脚之间**：未拖过时 JS 不写内联高度，排版由 CSS 的
//      `flex:1 1 0` 承担（★ 2026-10-13 用户令① 覆盖决策⑩ 的固定档 min(70vh,640px)——
//      那档在高屏上留一大片黑，正是「对话框没占满上下区域」这条投诉的成因）；
//   ⑤ 拖拽＝覆盖档：起点取**实测高度**（量不到才回落估算档）、进 cw-resized 让内联高度生效、
//      钳制 320px–90vh、松手记 localStorage、双击回默认并清记忆；
//   ⑥ CW_CSS 的三档声明（默认/覆盖/收起）等值锁 + 固定档负向清零——jsdom 不做 flex 布局，
//      运行时的「真吃满」由 e2e/pixel_uat.spec.ts P2b 的几何锁量，两侧同口径、缺一漏一半。
// 形态沿革见 ChatWindow.tsx 文件头（〇-LK/〇-M 定档 ＋ 决策⑩ ＋ 2026-10-13 用户令① ＋ 2026-10-15 用户令）。
// 运行：npx vitest run src/components/ChatWindow.dialog.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
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

// —— ⑥ 段用：把 CW_CSS 里的排版声明取出来判等值 ——
// ⚠️ 判据必须先**剥 /* */ 注释**再按选择器取规则体：ChatWindow.tsx 的说明注释里大量出现
// 「min(70vh,640px) 作废」这类字样，吃整份源码做子串匹配会既假红（负向腿命中注释）
// 又假绿（正向腿只命中注释、规则其实没写）——同族坑本仓已踩过三次。
const CW_SRC = readFileSync(resolve(__dirname, 'ChatWindow.tsx'), 'utf8')
function stripCssComments(s: string): string {
  return s.replace(/\/\*[\s\S]*?\*\//g, '')
}
/** 取某条选择器的规则体（去注释后的 CW_CSS 文本）；取不到返回 null＝该档压根没声明。 */
function cssRuleBody(src: string, selector: string): string | null {
  const code = stripCssComments(src)
  const re = new RegExp(`\\n\\s*${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\{([^}]*)\\}`)
  const m = code.match(re)
  return m ? m[1] : null
}
/** 三档排版的等值判据：返回违规清单（空＝合规）。喂真文件与喂反证夹具走同一个函数。 */
function dialogHeightViolations(src: string): string[] {
  const out: string[] = []
  const base = cssRuleBody(src, '.cw-dialog')
  const resized = cssRuleBody(src, '.cw-dialog.cw-resized')
  const collapsed = cssRuleBody(src, '.cw-dialog.cw-collapsed')
  if (!base) out.push('.cw-dialog 基础规则取不到＝锁失明（选择器改名或规则被删）')
  if (!resized) out.push('.cw-dialog.cw-resized 覆盖档缺失＝拖拽高度会被 flex-grow 吃掉')
  if (!collapsed) out.push('.cw-dialog.cw-collapsed 收起档缺失')
  if (base) {
    if (!/flex:1 1 0/.test(base)) out.push(`默认档不是 flex 吃满：${base}`)
    if (!/min-height:320px/.test(base)) out.push(`默认档缺 320px 下限：${base}`)
    // 固定档负向清零：写死 vh 那一族（决策⑩ 形态）不得复活，max-height 归覆盖档
    if (/height:min\(70vh/.test(base)) out.push(`固定默认档 min(70vh,640px) 复活：${base}`)
    if (/max-height/.test(base)) out.push(`基础档带 max-height＝上限会把吃满截断（上限只在 .cw-resized 上）：${base}`)
  }
  if (resized) {
    if (!/flex:0 0 auto/.test(resized)) out.push(`覆盖档没撤 flex 拉伸，内联高度不生效：${resized}`)
    if (!/max-height:90vh/.test(resized)) out.push(`覆盖档缺 90vh 上限（与 JS 钳制不同档）：${resized}`)
  }
  if (collapsed) {
    if (!/flex:0 0 auto/.test(collapsed)) out.push(`收起档没撤 flex 拉伸＝单行入口条被撑满：${collapsed}`)
  }
  return out
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
  localStorage.removeItem(H_KEY)
  localStorage.removeItem('translate_mode')
  ;(Element.prototype as any).scrollTo = (Element.prototype as any).scrollTo || vi.fn()
})

describe('对话框形态五态（决策⑩ ＋ 2026-10-13 用户令① ＋ 2026-10-15 用户令：默认展开）', () => {
  it('① 空态默认展开：空输入且无会话 ⇒ 也不进 cw-collapsed 档（用户令：不该一进来就最小化）', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog')!
    expect(dialog).toBeTruthy()
    expect(dialog.className, '空态必须默认展开').not.toContain('cw-collapsed')
    // 展开档＝完整三段式：框头 / 滚动区 / 工具条都在
    expect(dialog.querySelector('.cw-dialog-body')).toBeTruthy()
    expect(dialog.querySelector('.cw-toolbar')).toBeTruthy()
  })

  it('② 点框头「收起」钮才收成入口条；点入口条恢复完整三段式结构', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog')!
    expect(dialog.className).not.toContain('cw-collapsed')
    act(() => { fireEvent.click(container.querySelector('[data-testid="cw-collapse"]')!) })
    expect(dialog.className, '点了收起钮必须进收起档').toContain('cw-collapsed')
    // 收起档下输入行仍在（入口条本体就是 composer 的输入行），框头/滚动区被 CSS 藏起
    expect(dialog.querySelector('[data-testid="translate-input"]')).toBeTruthy()
    expect(dialog.querySelector('.cw-dialog-body')).toBeTruthy() // DOM 在、display:none（CSS 档）
    act(() => { fireEvent.click(dialog) })
    expect(dialog.className).not.toContain('cw-collapsed')
    expect(dialog.querySelector('.cw-toolbar')).toBeTruthy()
  })

  it('③ 有会话不收起：点过收起钮但消息流非空 ⇒ 仍不进 cw-collapsed 档（历史可见性优先）', () => {
    mocks.chat.messages = [
      { id: 'u1', role: 'user', content: '早上好', timestamp: Date.now() },
      { id: 'a1', role: 'assistant', content: 'Good morning', timestamp: Date.now() },
    ]
    const { container } = renderWindow()
    act(() => { fireEvent.click(container.querySelector('[data-testid="cw-collapse"]')!) })
    expect(container.querySelector('.cw-dialog')!.className).not.toContain('cw-collapsed')
  })

  it('④ 默认高度档：未拖过 ⇒ JS 不写内联高度、不进覆盖档（吃满由 CSS 的 flex:1 1 0 承担）', () => {
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '已有会话', timestamp: Date.now() }]
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    expect(dialog.style.height, '默认档不许写内联高度（写了就退化成固定档，不再跟着视口长）').toBe('')
    expect(dialog.className, '没拖过就不许进 .cw-resized 覆盖档').not.toContain('cw-resized')
  })

  it('⑤ 拖拽调高：起点取「实测高度」、进覆盖档、钳制 320–90vh、松手记 localStorage', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    // 空态自 2026-10-15 起默认展开，无需再点入口条
    expect(dialog.className).not.toContain('cw-collapsed')
    // ★ 2026-10-13 用户令① 后，默认档高度由 flex 算出来，jsdom 不做布局 ⇒ clientHeight 恒 0。
    //   这里按真浏览器已排版的样子把 500px 钉在这个节点上，专门验「起点取实测」这条腿
    //   （量不到才回落估算档，那条腿由 ⑤b 覆盖）。
    Object.defineProperty(dialog, 'clientHeight', { value: 500, configurable: true })
    const handle = container.querySelector('[data-testid="cw-resize-handle"]') as HTMLElement
    expect(handle).toBeTruthy()
    // 起拖（clientY=500，起点＝实测 500）→ 上移 100px ⇒ 600（不是估算档 538+100=638）
    fireEvent.mouseDown(handle, { clientY: 500 })
    fireEvent.mouseMove(window, { clientY: 400 })
    expect(dialog.style.height, '起点必须取实测高度，不许拿按视口推导的估算档凑数').toBe('600px')
    expect(dialog.className, '拖过之后必须进 .cw-resized，否则 flex-grow 会把内联高度当摆设').toContain('cw-resized')
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

  it('⑤b 量不到实测高度时回落估算档（jsdom／未排版完成那一支）', () => {
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    // 空态默认展开（2026-10-15）
    // 不钉 clientHeight ⇒ jsdom 回 0 ⇒ 起点走 cwDefaultDialogH()＝min(768×0.7,640)=538
    const handle = container.querySelector('[data-testid="cw-resize-handle"]') as HTMLElement
    fireEvent.mouseDown(handle, { clientY: 500 })
    fireEvent.mouseMove(window, { clientY: 400 })
    expect(dialog.style.height, '回落档读数与 ⑤ 的实测档必须不同，否则两条腿其实只有一条在跑').toBe('638px')
  })

  it('⑥ CW_CSS 三档排版声明等值 + 固定默认档负向清零（含四枚反证）', () => {
    expect(dialogHeightViolations(CW_SRC), '对话框高度三档声明与 2026-10-13 用户令① 的口径不符').toEqual([])
    // 反证一律走同一个判据函数，且每枚都要**恰好**点破对应那条腿
    const R1 = CW_SRC.replace('.cw-dialog{display:flex;flex-direction:column;overflow:hidden;margin:12px 6% 14px;flex:1 1 0;min-height:320px}',
      '.cw-dialog{display:flex;flex-direction:column;overflow:hidden;margin:12px 6% 14px;height:min(70vh,640px);min-height:320px;max-height:90vh}')
    expect(R1, '反证夹具没替换成功＝判据对象写漂了').not.toBe(CW_SRC)
    expect(dialogHeightViolations(R1).join('\n')).toContain('固定默认档 min(70vh,640px) 复活')
    const R2 = CW_SRC.replace('.cw-dialog.cw-resized{flex:0 0 auto;max-height:90vh}', '.cw-dialog.cw-resized{max-height:90vh}')
    expect(dialogHeightViolations(R2).join('\n')).toContain('覆盖档没撤 flex 拉伸')
    const R3 = CW_SRC.replace('.cw-dialog.cw-collapsed{flex:0 0 auto;', '.cw-dialog.cw-collapsed{')
    expect(dialogHeightViolations(R3).join('\n')).toContain('收起档没撤 flex 拉伸')
    const R4 = CW_SRC.replace('.cw-dialog.cw-resized{flex:0 0 auto;max-height:90vh}', '')
    expect(dialogHeightViolations(R4).join('\n')).toContain('覆盖档缺失')
    // 反证 ⑤：把注释里的字样当成实现——正向腿必须看不见注释
    const R5 = '/* 默认档＝flex:1 1 0、min-height:320px、覆盖档 flex:0 0 auto;max-height:90vh */'
    expect(dialogHeightViolations(R5).join('\n'), '判据吃了注释里的说明文字＝假绿形态').toContain('锁失明')
  })

  it('⑦ 收起态不吃记忆高度：点收起后虽有 localStorage 记忆但当前是空态入口条 ⇒ 不写内联高度、不进覆盖档', () => {
    localStorage.setItem(H_KEY, '500')
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    act(() => { fireEvent.click(container.querySelector('[data-testid="cw-collapse"]')!) })
    expect(dialog.className).toContain('cw-collapsed')
    expect(dialog.className).not.toContain('cw-resized')
    expect(dialog.style.height, '单行入口条不许被记忆高度撑成半屏').toBe('')
  })

  it('⑧ 会话恢复钳制 + 双击回默认并清记忆', () => {
    localStorage.setItem(H_KEY, '99999') // 上次会话留下的越界记忆
    mocks.chat.messages = [{ id: 'a1', role: 'assistant', content: '已有会话', timestamp: Date.now() }]
    const { container } = renderWindow()
    const dialog = container.querySelector('.cw-dialog') as HTMLElement
    // 恢复时同样钳到 90vh，并必须带上覆盖档 class（只有内联高度没有 flex:0 0 auto ＝ 拖拽记忆不生效）
    expect(Number.parseInt(dialog.style.height, 10)).toBe(Math.round(768 * 0.9))
    expect(dialog.className).toContain('cw-resized')
    // 双击手柄 ⇒ 回默认档（内联高度与覆盖档一起撤，重新吃满）+ 记忆清空
    fireEvent.doubleClick(container.querySelector('[data-testid="cw-resize-handle"]')!)
    expect(dialog.style.height).toBe('')
    expect(dialog.className, '回默认后必须撤掉覆盖档，否则 flex 不拉伸、高度停在 320 那一档').not.toContain('cw-resized')
    expect(localStorage.getItem(H_KEY)).toBeNull()
  })
})
