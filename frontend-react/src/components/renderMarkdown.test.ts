// ============================================================================
// renderMarkdown.test.ts — 气泡 Markdown 渲染（★ F11：E17 正则去 lookbehind 回归锚）
// ============================================================================
import { describe, expect, it } from 'vitest'
import { renderMarkdown } from '@/lib/markdown'

describe('renderMarkdown（E17）', () => {
  it('粗体 **x** 渲染为 <strong>，且不含环视语法（Safari 兼容写法）', () => {
    const html = renderMarkdown('这是 **重点** 内容')
    expect(html).toContain('<strong>重点</strong>')
    expect(html).not.toContain('(?<=')
    expect(html).not.toContain('(?<!')
  })
  it('单星号按斜体而非粗体处理（无回溯炸裂/误吞）', () => {
    const html = renderMarkdown('计算 3*4*5=60')
    expect(html).not.toContain('<strong>')
    expect(html).toContain('<em>4</em>')
  })
  it('标题与列表基础转换', () => {
    const html = renderMarkdown('## 小节\n- 甲\n- 乙')
    expect(html).toMatch(/<h2[^>]*>小节<\/h2>/)
    expect(html).toContain('<li>甲</li>')
  })
})
