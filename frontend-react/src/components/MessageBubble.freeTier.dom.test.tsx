// ============================================================================
// MessageBubble.freeTier.dom.test.tsx — ★ 2026-11-01 用户拍板
// 「即时翻译落在免费档内时：显示**等值积分**并注明**免扣**」的气泡侧锁。
//
// 四条运行时判据 + 一条词典全集判据，各对一个形态：
//   FT1 免费腿（points_used=0 ＋ points_equiv>0 ＋ points_free=true）
//       ⇒ 出「等值 N 积分 · 本次免扣」，且**不**出「本次消耗」——
//          把花了钱的说法安在没花钱的单子上，与反过来一样都是"读数给事实背假的"。
//   FT2 付费腿（points_used>0、两个新键缺席）⇒ 文案与改前逐字一致（旧行为不许被顺手改掉）。
//   FT3 撕裂态（points_used=0 且没有 points_free）⇒ **整行不出**。
//       「没有这个维度」与「这一维是 0」在出参上是两件事（后端用 omitempty 分开），
//       展示层若把两者混判，就会给「没记账」的单子编出一行数字。
//   FT4 免扣却没折出积分（points_free=true、points_equiv 缺/0）⇒ 整行也不出，
//       宁可这一行消失，也不出「等值 0 积分 · 本次免扣」那种零信息量句子。
//   FT5 「同一排只许一枚 msg-meta」按**全集**数（不是"存在一枚"）：
//       两个分支各渲染一枚就是把同排拆成两排（同用户令③那一族的投诉本体）。
//   FT6 `msg.pointsFree` 必须在**全部 12 份**词典里、且带 {n} 占位符
//       （§一·5「新增面板键必须十份 locale 同步补译」；漏一份＝该语种访客看到裸键名）。
//
// 运行：npx vitest run src/components/MessageBubble.freeTier.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import { setLang } from '@/i18n'
import type { ChatMessage } from '@/types'

vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '', handleUnauthorized: vi.fn() }))

import MessageBubble from './MessageBubble'

const mkMsg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  id: 'm1', role: 'assistant', content: 'Hello world', timestamp: 1, ...over,
})

const transData = { translations: { en: 'Hello world' }, lang_names: { en: '英语' }, mode: '快速档' } as any

// metas 取尾部那一排的全部消耗读数节点（FT5 用它按**全集**数，不是取第一枚）。
function metas(container: HTMLElement) {
  return Array.from(container.querySelectorAll('[data-testid="msg-meta"]'))
}

// renderFree 渲染一条带指定积分三维度的助手消息，返回容器与消耗行文本。
function renderFree(over: Partial<ChatMessage>) {
  const { container } = render(<MessageBubble message={mkMsg({ data: transData, ...over })} source="你好世界" />)
  const nodes = metas(container)
  return { container, text: nodes.map((n) => n.textContent || '').join('｜'), nodes }
}

beforeEach(() => {
  cleanup()
  setLang('zh')
})

describe('用户拍板 2026-11-01 免费档气泡：等值积分＋注明免扣', () => {
  it('FT1 免费腿：出「等值 N 积分 · 本次免扣」，绝不出「本次消耗」', () => {
    const { text, nodes } = renderFree({ points_used: 0, points_equiv: 9, points_free: true })
    expect(nodes).toHaveLength(1)
    expect(text, 'MUTFREE01：免扣那一行必须显示等值积分（回到「本次消耗」＝把没花钱说成花了钱）').toContain('等值')
    expect(text, 'MUTFREE02：只出数字不点名免扣，客户会把等值读成扣费').toContain('免扣')
    expect(text).toContain('9')
    expect(text, 'MUTFREE03：免费腿不许再出「本次消耗」那一支').not.toContain('本次消耗')
    // 出栈的那一行必须能一眼分清「没花钱」——只出数字不出"免扣"两字，客户会按扣费那一支读。
    expect(text).not.toBe('等值 9 积分')
  })

  it('FT2 付费腿：文案与改前逐字一致，两个新键缺席时不许被顺手改写', () => {
    const { text, nodes } = renderFree({ points_used: 12 })
    expect(nodes).toHaveLength(1)
    expect(text, 'MUTFREE04：付费腿必须仍出「本次消耗」（把两支合成一支＝把扣了钱说成免扣）').toContain('本次消耗')
    expect(text).toContain('12')
    expect(text).not.toContain('免扣')
    expect(text).not.toContain('等值')
  })

  it('FT3 撕裂态（实扣 0 且没有免扣标记）⇒ 整行不出，不给编造的数字留位置', () => {
    const { text, nodes } = renderFree({ points_used: 0 })
    expect(nodes, 'MUTFREE05：「没这个维度」与「这一维是 0」必须同形＝都不出这一行').toHaveLength(0)
    expect(text).toBe('')
    const { nodes: n2 } = renderFree({ points_used: 0, points_equiv: 9, points_free: false })
    expect(n2).toHaveLength(0)
  })

  it('FT4 免扣却没折出积分 ⇒ 整行也不出（「等值 0 积分 · 免扣」是零信息量句子）', () => {
    const { nodes } = renderFree({ points_used: 0, points_free: true })
    expect(nodes, 'MUTFREE06：折不出积分的一单不许出「等值 0 积分」那一行').toHaveLength(0)
    const { nodes: n2 } = renderFree({ points_used: 0, points_equiv: 0, points_free: true })
    expect(n2).toHaveLength(0)
  })

  it('FT5 同一排只许一枚 msg-meta：两分支各自渲染必须被抓住（按全集数）', () => {
    // 正向对照：免费腿与付费腿各渲染一次，两边都恰好一枚；
    // 反证形态（两枚）由 M0096 那类变异在 Go 侧闸门里跑，这里守的是**当前实现不复发**。
    for (const over of [
      { points_used: 0, points_equiv: 9, points_free: true },
      { points_used: 12 },
    ] as Array<Partial<ChatMessage>>) {
      const { nodes } = renderFree(over)
      expect(nodes).toHaveLength(1)
      // 都必须是 .msg-actions 的直接子节点——不许被再包一层（同用户令③那条派生判据）。
      const row = nodes[0].closest('.msg-actions')
      expect(row).toBeTruthy()
      expect(nodes[0].parentElement).toBe(row)
    }
  })
})

describe('词典全集：msg.pointsFree 在 12 份里都必须在位且带 {n}', () => {
  const ROOT = resolve(__dirname, '../i18n')
  const FILES = [
    'dicts.zh.ts', 'dicts.en.ts',
    'locales/zh-hant.ts', 'locales/ja.ts', 'locales/ko.ts', 'locales/fr.ts',
    'locales/de.ts', 'locales/es.ts', 'locales/pt.ts', 'locales/ru.ts',
    'locales/ar.ts', 'locales/th.ts',
  ]
  FILES.forEach((rel) => {
    it(`FT6 ${rel} 含 msg.pointsFree（占位符齐、且不许退化成 msg.pointsUsed 的复用）`, () => {
      const src = readFileSync(resolve(ROOT, rel), 'utf8')
      // 剥注释：说明注释里写着键名不算在位（AGENTS §一·5「注释不许冒充实现字面量」同族反向处理）。
      const code = src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
      const hit = code.match(/(['"])msg\.pointsFree\1\s*:\s*(['"])(.*?)\2/)
      expect(hit, `缺键 msg.pointsFree：${rel}`).toBeTruthy()
      const val = (hit as RegExpMatchArray)[3]
      expect(val, `{n} 占位符缺失：${rel}＝积分数字永远填不进去`).toContain('{n}')
      expect(val.trim().length, `空文案：${rel}`).toBeGreaterThan(3)
      // 负向：不许把免扣那一支写成"消耗"（同一句话两个分支＝这条锁空转）。
      expect(val).not.toContain('消耗')
    })
  })
})
