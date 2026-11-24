// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { middleEllipsis, TicketNameCell } from './TicketsPage'

afterEach(cleanup)

describe('工单名称中间省略与展开', () => {
  it('短名称原样显示，不无故截断', () => {
    expect(middleEllipsis('ROX LEAD-AI 项目方案.md')).toBe('ROX LEAD-AI 项目方案.md')
  })

  it('超长名称保留首尾并只省略中间', () => {
    const text = 'ROX LEAD-AI 项目立项与实施方案 0901 副本_' + '很长的工单名称'.repeat(8) + '.md'
    const got = middleEllipsis(text, 31)
    expect(got.startsWith('ROX LEAD-AI')).toBe(true)
    expect(got.endsWith('.md')).toBe(true)
    expect(got).toContain('…')
    expect(Array.from(got).length).toBe(31)
  })

  it('移动端点击展开全文，再次点击收起', () => {
    const text = '超长工单名称' + '内容'.repeat(40) + '.docx'
    render(<TicketNameCell text={text} />)
    const btn = screen.getByTestId('tk-ticket-name') as HTMLButtonElement
    expect(btn.getAttribute('title')).toBe(text)
    expect(btn.textContent).toContain('…')
    fireEvent.click(btn)
    expect(btn.textContent).toBe(text)
    expect(btn.getAttribute('aria-expanded')).toBe('true')
    fireEvent.click(btn)
    expect(btn.textContent).toContain('…')
    expect(btn.getAttribute('aria-expanded')).toBe('false')
  })
})
