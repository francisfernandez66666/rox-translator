// ============================================================================
// MessageBubble.gateWarnings.dom.test.tsx — P0-01 质量/丢弃警告可见性锁
//
// 缺陷背景（2026-10-25 云端 UAT）：chat 气泡有结构化译文时只渲染译文表、
// 把 reply 藏起来，后端写在 reply 里的「质量校验提示」用户永远看不到；
// 「and」被交付时屏幕上只有一条垃圾译文，没有任何解释。
//
// 本锁钉住：data.gate_warnings 非空且存在译文时，气泡必须出现
// [data-testid="gate-warnings"] 警告块，并逐条展示后端原因。
// 反证：把 MessageBubble 里的警告块摘掉 ⇒ 本用例红。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { setLang } from '@/i18n'
import type { ChatMessage } from '@/types'

// 气泡只在附件 blob 鉴权时用 @/api 的导出；本用例无附件，整体替换即可
vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '', handleUnauthorized: vi.fn() }))

import MessageBubble from './MessageBubble'

// mkMsg 构造一条最小助手消息；本用例只关心 data.translations 与 gate_warnings。
const mkMsg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  id: 'm-gw', role: 'assistant', content: '', timestamp: Date.now(), ...over,
})

beforeEach(() => {
  cleanup()
  setLang('zh')
})

// 三条：有警告/无警告/脏值，覆盖“该出现时出现、不该出现时不出现”。
describe('MessageBubble gate_warnings 可见性', () => {
  it('有译文 + gate_warnings ⇒ 渲染警告块并展示原因', () => {
    const { container } = render(
      <MessageBubble
        message={mkMsg({
          data: {
            translations: { en: 'This is a valid translation.' },
            translations_source: { en: 'model' },
            target_langs: ['en'],
            gate_warnings: ['英语 译文不可用，已丢弃（unusable）'],
          },
        })}
      />,
    )
    const block = container.querySelector('[data-testid="gate-warnings"]')
    expect(block).toBeTruthy()
    expect(block?.textContent || '').toContain('译文不可用')
    // 译文表仍在：警告块是补充信息，不许把正常译文挤掉
    expect(container.querySelector('.translation-results')).toBeTruthy()
    expect(container.textContent || '').toContain('This is a valid translation.')
  })

  it('没有 gate_warnings ⇒ 不渲染警告块（零打扰）', () => {
    const { container } = render(
      <MessageBubble
        message={mkMsg({
          data: {
            translations: { en: 'This is a valid translation.' },
            translations_source: { en: 'model' },
            target_langs: ['en'],
          },
        })}
      />,
    )
    expect(container.querySelector('[data-testid="gate-warnings"]')).toBeNull()
  })

  it('空字符串/非数组的脏值 ⇒ 不渲染空警告块', () => {
    const { container } = render(
      <MessageBubble
        message={mkMsg({
          data: {
            translations: { en: 'This is a valid translation.' },
            gate_warnings: ['', '   ', null as unknown as string],
          },
        })}
      />,
    )
    expect(container.querySelector('[data-testid="gate-warnings"]')).toBeNull()
  })
})
