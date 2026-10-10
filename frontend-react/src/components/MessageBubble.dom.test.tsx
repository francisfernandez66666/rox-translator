// ============================================================================
// MessageBubble.dom.test.tsx — ★ 14.4 复制净化 + ★ 决策⑪② 慢预警横幅断言（2026-10-10）
// 锁住：
//   ① 复制只带译文本体：单目标语＝那一句译文，粘贴即可用（不再复制后端拼好的整段）；
//   ② 多目标语＝按「语言名：译文」逐行复制（吃结构化 translations，与译文表同源）；
//   ③ 无结构化译文（普通问答）回落 message.content，行为同旧版；
//   ④ 模式与积分在气泡底部 meta 行（与反馈入口同排），不进 content；
//   ⑤ 慢预警横幅仅在流式进行态出现，「继续等待」本地消隐；无进行态不出现。
// 运行：npx vitest run src/components/MessageBubble.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
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

  it('④ 模式与积分落在气泡底部 meta 行（与反馈入口同排），不进气泡正文', () => {
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
    // 反馈入口与 meta 同排（14.4：meta 行挂在 msg-feedback-row 内）
    expect(meta.closest('.msg-feedback-row')).toBeTruthy()
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
