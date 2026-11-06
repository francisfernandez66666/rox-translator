// @vitest-environment jsdom
// ============================================================================
// ProductAnswer.zeroToken.dom.test.tsx — 知识层问答零 LLM 调用（施工_一期 §7.3／§9.2）
//
// 问答出结果**且不发**任何「再调一次模型」的请求：面板只打一次 /api/product/answer，
// fetch 调用面必须为零（§7.3 N1/N2 要防的唯一真实坏法＝悄悄把请求转交模型）。
// 缺项由后端在 missing 里点名，前端照实出列，不补第二次请求。
// 运行：npx vitest run src/ProductAnswer.zeroToken.dom.test.tsx
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

/** 可编程问答夹具（vi.hoisted 保证 mock 工厂在 import 之前拿得到引用） */
const mocks = vi.hoisted(() => ({
  answer: vi.fn(async () => ({ success: true, answer: '本店报价以知识库已确认事实为准', source: 'knowledge_base', missing: ['warranty'] })),
}))

vi.mock('@/api/product', () => ({
  productList: async () => ({ success: true, products: [{ id: 'p1', tenant_code: 'tc01', page_code: 'pg01' }] }),
  answerProduct: mocks.answer,
}))

import ProductAnswers from './components/ProductAnswers'

afterEach(() => {
  cleanup()
  mocks.answer.mockClear()
  vi.unstubAllGlobals()
})

describe('知识层问答零 token（§7.3／§9.2）', () => {
  it('出一问一答，且 fetch 调用面为零（没有「再调一次模型」）', async () => {
    const fetchSpy = vi.fn(async () => { throw new Error('知识层问答不许另起 fetch 链') })
    vi.stubGlobal('fetch', fetchSpy)

    render(<ProductAnswers />)
    const btn = screen.getByRole('button') as HTMLButtonElement
    // 可达探针：两段短码没到手前按钮是禁用的，等到手才允许提问（否则点对空链一路绿灯）
    await waitFor(() => expect(btn.disabled).toBe(false))

    fireEvent.change(screen.getByRole('textbox'), { target: { value: '质保多久？' } })
    fireEvent.click(btn)

    await waitFor(() => expect(screen.getByTestId('knowledge-answer').textContent).toContain('知识库'))
    expect(mocks.answer, '问答必须只打一次 /api/product/answer').toHaveBeenCalledTimes(1)
    expect(fetchSpy, '知识层问答不许发起任何额外 fetch（零 LLM 调用）').not.toHaveBeenCalled()
    // 缺项照实点名，且不因此补第二次请求
    expect(screen.getByTestId('answer-missing').textContent).toContain('warranty')
    expect(mocks.answer).toHaveBeenCalledTimes(1)
  })
})
