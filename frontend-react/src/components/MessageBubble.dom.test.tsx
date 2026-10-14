// ============================================================================
// MessageBubble.dom.test.tsx — ★ 14.4 复制净化 + ★ 决策⑪② 慢预警横幅 + ★ 用户令③ 尾部操作行
// 锁住：
//   ① 复制只带译文本体：单目标语＝那一句译文，粘贴即可用（不再复制后端拼好的整段）；
//   ② 多目标语＝按「语言名：译文」逐行复制（吃结构化 translations，与译文表同源）；
//   ③ 无结构化译文（普通问答）回落 message.content，行为同旧版；
//   ④ 消耗读数落在气泡尾部操作行（与查看原文/复制/反馈同排），不进 content；模式只出栈一次；
//   ⑤ 慢预警横幅仅在流式进行态出现，「继续等待」本地消隐；无进行态不出现。
//   ⑦⑧⑨⑩ ★ 2026-10-13 用户令③（「字体大小和高低位置统一一下，至少把查看原文/反馈/消耗在同一行」）：
//      尾部各件同排、字号只在 .msg-actions 一处声明、旧的两排档不许复活（含源码级 CSS 等值锁＋反证；
//      ★ 2026-10-15：⑦ 的点名清单已含本批新增的「朗读」，件数不写死）。
// 运行：npx vitest run src/components/MessageBubble.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import { setLang } from '@/i18n'
import type { ChatMessage } from '@/types'

// '@/api' 整体替换：气泡只在附件 blob 鉴权时用这些导出，本测试无附件
vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '', handleUnauthorized: vi.fn() }))

import MessageBubble from './MessageBubble'

// mkMsg 构造最小助手消息
const mkMsg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  id: 'm1', role: 'assistant', content: '', timestamp: Date.now(), ...over,
})

const writeText = vi.fn().mockResolvedValue(undefined)

// ★ 用户令③ 源码级 CSS 锁的料：jsdom **不加载** theme.css（本测试没有 import 样式），
//   所以运行时量不到 font-size——字号这一档只能在**源码级**锁声明，
//   运行时侧锁的是「行内没有任何组件自己写内联字号」（两腿射程不同，互补而非重复）。
const CSS_SRC = readFileSync(resolve(__dirname, '../styles/theme.css'), 'utf8')

// stripCssComments 先剥 /* */ 注释：本批中文注释里就带着判据靶子字面（14px、font: inherit、
// 旧档名 msg-feedback-row 的历史说明），不剥就是「注释冒充实现」的假绿（同 ChatWindow.dialog 锁口径）。
function stripCssComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

// cssRuleBody 取某个**精确选择器**的规则体；选择器后必须紧跟 `{`，
// 因此 `.msg-actions` 不会误命中 `.msg-actions-wrap`。取不到返回空串（调用方判「锁失明」）。
function cssRuleBody(css: string, selector: string): string {
  const esc = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const m = new RegExp(`(?:^|[}\\s])${esc}\\s*\\{([^}]*)\\}`).exec(css)
  return m ? m[1] : ''
}

// tailRowViolations 是尾部操作行的唯一判据函数：真源与反证夹具喂同一个函数（同口径才有射程）。
function tailRowViolations(css: string): string[] {
  const c = stripCssComments(css)
  const row = cssRuleBody(c, '.msg-actions')
  const btn = cssRuleBody(c, '.msg-act-btn')
  const meta = cssRuleBody(c, '.msg-meta')
  // 取不到规则＝锁失明，必须单独判红（否则下面每一条负向都恒真、整段空转）
  if (!row || !btn || !meta) return ['锁失明：取不到尾部操作行规则体（.msg-actions/.msg-act-btn/.msg-meta）']
  const out: string[] = []
  if (!/font-size:\s*14px/.test(row)) out.push('.msg-actions 未把字号单点声明为 14px')
  if (!/font:\s*inherit/.test(btn)) out.push('.msg-act-btn 未继承行字号')
  if (/font-size/.test(btn)) out.push('.msg-act-btn 自带字号＝控件各走一档（用户令③ 的投诉本体）')
  if (!/font:\s*inherit/.test(meta)) out.push('.msg-meta 未继承行字号')
  if (/font-size/.test(meta)) out.push('.msg-meta 自带字号＝原 12px 小字档复活')
  // 负向腿（旧两排档不许复活）的正向对照＝上面 row/btn/meta 三条取到了规则体，
  // 否则「整段 CSS 被删空」也会把负向判成通过。
  if (/msg-feedback-row|feedback-btn|msg-srcbar/.test(c)) out.push('旧的两排控件档（反馈行／原文行）复活')
  return out
}

beforeEach(() => {
  cleanup()
  setLang('zh')
  writeText.mockClear()
  // jsdom 无 clipboard 实现：桩掉 writeText，断言复制产物
  Object.assign(navigator, { clipboard: { writeText } })
})

describe('14.4 复制净化（复制只带译文本体）', () => {
  it('① 单目标语：复制内容＝那一句译文本身', () => {
    const { getByTestId } = render(
      <MessageBubble message={mkMsg({
        content: 'Hello world',
        data: { translations: { en: 'Hello world' }, lang_names: { en: '英语' } } as any,
      })} />,
    )
    fireEvent.click(getByTestId('copy-translation'))
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(writeText).toHaveBeenCalledWith('Hello world')
  })

  it('② 多目标语：按「语言名：译文」逐行复制（与译文表同源的结构化字段）', () => {
    const { getByTestId } = render(
      <MessageBubble message={mkMsg({
        content: '英语：Hello\n日语：こんにちは',
        data: { translations: { en: 'Hello', ja: 'こんにちは' }, lang_names: { en: '英语', ja: '日语' } } as any,
      })} />,
    )
    fireEvent.click(getByTestId('copy-translation'))
    expect(writeText).toHaveBeenCalledWith('英语：Hello\n日语：こんにちは')
  })

  it('③ 无结构化译文（普通问答）：回落 message.content，行为同旧版', () => {
    const { getByTestId } = render(
      <MessageBubble message={mkMsg({ content: '支持 40+ 种语言互译。' })} />,
    )
    fireEvent.click(getByTestId('copy-translation'))
    expect(writeText).toHaveBeenCalledWith('支持 40+ 种语言互译。')
  })

  it('④ 模式与积分落在气泡尾部操作行（与查看原文/复制/反馈同排），不进气泡正文', () => {
    const { container } = render(
      <MessageBubble message={mkMsg({
        content: 'Hello world',
        points_used: 12,
        data: { translations: { en: 'Hello world' }, lang_names: { en: '英语' }, mode: '模型翻译（无知识库）' } as any,
      })} />,
    )
    const meta = container.querySelector('[data-testid="msg-meta"]')!
    expect(meta).toBeTruthy()
    expect(meta.textContent).toContain('本次消耗')
    expect(meta.textContent).toContain('12')
    // ★ 2026-10-13 用户令③重钉：原来这一条锁的是 `.msg-feedback-row`（反馈独占一排、右对齐），
    //   那一排已并进尾部操作行——类名跟着换成 .msg-actions，判据射程不变（meta 与反馈同排）。
    expect(meta.closest('.msg-actions')).toBeTruthy()
  })
})

describe('决策⑪② 慢预警横幅（deadline 前 ~15s 的 warning 帧）', () => {
  it('⑤ 流式进行态收到 slowWarning ⇒ 出「继续等待/转工单」两按钮；点继续等待即消隐', () => {
    const { container, getByTestId, queryByTestId } = render(
      <MessageBubble message={mkMsg({
        slowWarning: true,
        progress: { step: '机器翻译', percent: 30 },
        draft: { en: 'Hel' },
      })} />,
    )
    expect(queryByTestId('slow-warning')).toBeTruthy()
    expect(container.querySelector('[data-testid="slow-keep-wait"]')).toBeTruthy()
    expect(container.querySelector('[data-testid="slow-to-ticket"]')).toBeTruthy()
    fireEvent.click(getByTestId('slow-keep-wait'))
    expect(queryByTestId('slow-warning')).toBeNull()
  })

  it('⑥ 无进行态（done/error 收尾）时 slowWarning 不渲染横幅', () => {
    const { queryByTestId } = render(
      <MessageBubble message={mkMsg({
        slowWarning: true,
        data: { translations: { en: 'Hello' } } as any,
      })} />,
    )
    expect(queryByTestId('slow-warning')).toBeNull()
  })
})

// ============================================================================
// ★ 2026-10-13 用户令③（「前端翻译完成后的内容字体大小和高低位置统一一下，
//   至少把查看原文/反馈/消耗在同一行吧」）——气泡尾部操作行的四层锁。
//   投诉本体是**两排两档**：反馈＋模式/消耗在译文表里右对齐、meta 12px；
//   查看原文＋复制在它下面另起一行、左对齐 14px。修法＝四件同排、字号只在 .msg-actions 声明一次。
//   真值：《前端及UI相关/UI-ANNOTATIONS.md》§6「气泡尾部操作行」。
// ============================================================================
describe('用户令③ 气泡尾部操作行（同排同档；件数按点名正锁，不写死数字）', () => {
  // 一条消息凑齐全部件：有上一条原文（查看原文）＋有结构化译文（复制/朗读/反馈/读数）
  const mkFull = () => mkMsg({
    content: 'Hello world',
    points_used: 12,
    data: { translations: { en: 'Hello world' }, lang_names: { en: '英语' }, mode: '模型翻译（无知识库）' } as any,
  })

  it('⑦ 查看原文／复制／朗读／反馈／消耗全在同一个 .msg-actions 里（直接子节点，谁都不另起一排）', () => {
    const { container, getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const row = getByTestId('msg-actions')
    const meta = container.querySelector('[data-testid="msg-meta"]')!
    const fb = container.querySelector('.msg-fb-btn')!
    // 每一件都是这一排的直接子节点（closest 只证「在同一排内」，parentElement 才证「没被别的容器再包一层、
    // 也就是没有被拆回两个 flex 子块各占一排」）。
    // ★ 2026-10-15 补「朗读」这一枚：本批 P3 的 SpeakButton 落进同一排后，e2e TF4 那条"件数＝4"翻成了
    //   派生判据（见 translate_flow.spec.ts 里那段理由）；这里把新控件也纳入**点名正锁**，
    //   否则「加了一件」这件事在 jsdom 侧零覆盖，而下一次真掉件时没人看得见。
    for (const [name, el] of [['查看原文', getByTestId('show-src')], ['复制', getByTestId('copy-translation')],
                              ['朗读', getByTestId('speak-translation')], ['反馈', fb], ['消耗', meta]] as const) {
      expect(el, `「${name}」不在尾部操作行内`).toBeTruthy()
      expect(el.parentElement, `「${name}」不是 .msg-actions 的直接子节点（又被子块分排了）`).toBe(row)
    }
    // 旧的两排结构必须彻底不在场（负向；正向对照＝上面的四件点名都在）
    expect(container.querySelector('.msg-feedback-row')).toBeNull()
    expect(container.querySelector('.msg-srcbar')).toBeNull()
    // ★ 同一条「统一」里的第二半：模式只在表头出栈**一次**（那枚带色胶囊），
    //   尾部读数不许再念一遍纯文本——同屏两处同词、还各是一档字号，就是用户看到的乱。
    //   胶囊文本现读不写死（它由 modeBadge 归一：data.mode「模型翻译（无知识库）」上屏是「在线翻译」，
    //   写死字面会让上面那条正向对照先红），并以「胶囊非空」这条正锁给负向锁当对照腿。
    const pill = container.querySelector('.mode-badge')!
    expect(pill.textContent!.trim().length, '模式胶囊没出字＝下面那条负向锁没有射程（空转）').toBeGreaterThan(0)
    expect(container.querySelectorAll('.mode-badge').length).toBe(1)
    expect(meta.textContent, '尾部读数把模式又念了一遍（同词双现＝字号/高低不统一的另一半）')
      .not.toContain(pill.textContent!.trim())
  })

  it('⑧ 行内没有任何组件自己写内联字号（字号只有一个来源＝CSS 那一处声明）', () => {
    const { getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    const row = getByTestId('msg-actions')
    // 旧形态正是四件各写各的（meta 内联 12px、按钮内联 14px）；现在行内谁都不许带 style 属性
    expect(row.querySelectorAll('[style]').length,
      '尾部操作行里出现了内联样式——字号又会各走一档').toBe(0)
  })

  it('⑧b「查看原文」的展开面板长在行**下方**、不在行内（flex 行内展开会把整行顶歪）', () => {
    const { container, getByTestId } = render(<MessageBubble message={mkFull()} source="你好世界" />)
    expect(container.querySelector('[data-testid="src-panel"]')).toBeNull()
    fireEvent.click(getByTestId('show-src'))
    const panel = container.querySelector('[data-testid="src-panel"]')!
    expect(panel).toBeTruthy()
    expect(panel.textContent).toBe('你好世界')
    expect(panel.closest('.msg-actions')).toBeNull()
    expect(panel.parentElement).toBe(container.querySelector('.msg-actions-wrap'))
  })

  it('⑨ 源码级 CSS 等值锁：字号 14px 只在 .msg-actions 声明一次，按钮与读数一律 inherit，旧档名清零', () => {
    expect(tailRowViolations(CSS_SRC)).toEqual([])
  })

  it('⑩ 反证批：五枚夹具各打一条判据，真源作对照腿（防夹具脱靶＝假绿）', () => {
    // mut 找不到锚点就当场红：夹具脱靶会让反证「什么都没改」却被判成通过
    const mut = (from: string, to: string): string => {
      const out = CSS_SRC.replace(from, to)
      expect(out, `反证夹具脱靶：theme.css 里找不到「${from}」`).not.toBe(CSS_SRC)
      return out
    }
    // V1 读数自己抬回 12px 小字档（原样）
    const v1 = mut('.msg-meta { font: inherit;', '.msg-meta { font-size: 12px;')
    expect(tailRowViolations(v1)).toContain('.msg-meta 自带字号＝原 12px 小字档复活')
    // V2 行字号从 14 挪走（单向抬档同样判死——本条是等值锁不是「只准更大」）
    const v2 = mut('gap: 10px; font-size: 14px;', 'gap: 10px; font-size: 15px;')
    expect(tailRowViolations(v2)).toContain('.msg-actions 未把字号单点声明为 14px')
    // V3 动作件另立一档
    const v3 = mut('.msg-act-btn { border: none;', '.msg-act-btn { font-size: 16px; border: none;')
    expect(tailRowViolations(v3)).toContain('.msg-act-btn 自带字号＝控件各走一档（用户令③ 的投诉本体）')
    // V4 旧两排档复活（历史名回来＝组件里那两排也会被写回来）
    const v4 = `${CSS_SRC}\n.msg-feedback-row { margin-top: 6px; display: flex; justify-content: flex-end; }\n`
    expect(tailRowViolations(v4)).toContain('旧的两排控件档（反馈行／原文行）复活')
    // V5 选择器改名＝判据够不到实现：必须报「锁失明」而不是「全绿」（负向腿恒真的结构性防空转）
    const v5 = mut('.msg-actions { display: flex;', '.lc-actions { display: flex;')
    expect(tailRowViolations(v5).length).toBe(1)
    expect(tailRowViolations(v5)[0]).toContain('锁失明')
    // 对照腿：真源一枚都不报
    expect(tailRowViolations(CSS_SRC)).toEqual([])
  })
})
