// ============================================================================
// MessageBubble.draft.dom.test.tsx — B1 流式双态气泡回归（2026-09-19）
// 锁定的历史缺陷：旧版 progress 与 content 互斥，流式译文写在 content 里
// 却被三关量尺整个遮住——用户全程看不到逐字输出，只剩一根进度条干等。
// 现断言：① draft+progress 共存出草稿区（量尺让位为细进度条）；
//         ② 阶段徽章按 percent≥67 从「初译中」切「审校中」；
//         ③ 无 draft 的降级路径（浑元/熔断）回退三关量尺；
//         ④ 译文表（定稿态）出现即盖过草稿，不双显。
// 运行：npx vitest run src/components/MessageBubble.draft.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { setLang } from '@/i18n'
import type { ChatMessage } from '@/types'

// '@/api' 整体替换：气泡只在附件 blob 鉴权时用 API_BASE/getAuthToken，本测试无附件
vi.mock('@/api', () => ({ API_BASE: '', getAuthToken: () => '' }))

import MessageBubble from './MessageBubble'

// mkMsg 构造最小助手消息：只铺 B1 判定所需字段
const mkMsg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  id: 'm1', role: 'assistant', content: '', timestamp: Date.now(), ...over,
})

beforeEach(() => { cleanup(); setLang('zh') })

describe('B1 流式双态 · 草稿区与进度共存', () => {
  it('① draft+progress 同时在场：出草稿区与正文，量尺不再抢位', () => {
    const { container, queryByText } = render(
      <MessageBubble message={mkMsg({
        progress: { step: '机器翻译', percent: 40 },
        draft: { en: 'Hallo Welt' },
      })} />,
    )
    expect(container.querySelector('.draft-area')).toBeTruthy()
    expect(queryByText('Hallo Welt')).toBeTruthy()
    expect(queryByText('初译中')).toBeTruthy()
    // 量尺（三关名字容器 .progress-area）在草稿态必须让位——这就是被修复的互斥缺陷
    expect(container.querySelector('.progress-area')).toBeNull()
    // 细进度条承接进度呈现
    expect(container.querySelector('.draft-bar')).toBeTruthy()
  })

  it('② percent≥67 进入第三关：徽章切「审校中」', () => {
    const { queryByText } = render(
      <MessageBubble message={mkMsg({
        progress: { step: '术语校准', percent: 80 },
        draft: { en: 'Hallo Welt' },
      })} />,
    )
    expect(queryByText('审校中')).toBeTruthy()
    expect(queryByText('初译中')).toBeNull()
  })

  it('③ 降级路径无 delta：draft 为空回退三关量尺（浑元/熔断/流式失败三态兜底）', () => {
    const { container } = render(
      <MessageBubble message={mkMsg({ progress: { step: '机器翻译', percent: 40 }, draft: {} })} />,
    )
    expect(container.querySelector('.draft-area')).toBeNull()
    expect(container.querySelector('.progress-area')).toBeTruthy()
  })

  it('④ 译文表（定稿态）出现即盖过草稿：不双显', () => {
    const { container, queryByText } = render(
      <MessageBubble message={mkMsg({
        draft: { en: 'Hallo Welt' },
        data: { translations: { en: 'Hallo, Welt!' } } as any,
      })} />,
    )
    expect(container.querySelector('.draft-area')).toBeNull()
    expect(container.querySelector('.translation-results')).toBeTruthy()
    expect(queryByText('Hallo Welt')).toBeNull()
  })

  it('⑤ 草稿行尾光标随每行渲染（流式进行态的可感知信号）', () => {
    const { container } = render(
      <MessageBubble message={mkMsg({ draft: { en: 'A', de: 'B' } })} />,
    )
    expect(container.querySelectorAll('.draft-row').length).toBe(2)
    expect(container.querySelectorAll('.draft-caret').length).toBe(2)
  })
})
