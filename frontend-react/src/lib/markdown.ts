// ============ markdown.ts · 职责说明 ============
// 气泡 Markdown 轻量渲染（★ F11 自 MessageBubble 抽纯，可脱离组件单测；E17 正则无环视）。
// =============================================

// 转义 HTML 实体（& < > " '），渲染前先清洗源文本，杜绝 XSS 注入
function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;')
}

// 将消息正文渲染为受限 Markdown HTML：引用、标题、列表、分隔线、行内加粗/斜体/代码
export function renderMarkdown(text: string): string {
  let out = text || ''

  // 0. 转义
  out = escapeHtml(out)

  // 1. 引用块
  out = out.replace(/(?:^|\n)((?:&gt;\s*.*\n?)+)/g, (_m, block: string) => {
    const lines = block.trim().split('\n').map((l: string) => l.replace(/^&gt;\s*/, ''))
    return `\n<blockquote>${lines.join('<br>')}</blockquote>\n`
  })

  // 2. 标题 h1–h6
  out = out.replace(/^(#{1,6})\s+(.+)$/gm, (_m, hashes: string, content: string) => {
    const level = hashes.length
    return `<h${level}>${content.trim()}</h${level}>`
  })

  // 3. 分隔线
  out = out.replace(/^(?:---|\*\*\*)\s*$/gm, '<hr>')

  // 4. 列表项
  out = out.replace(/^(\s*)([-*+])\s+(.+)$/gm, '<li class="li-unordered">$3</li>')
  out = out.replace(/^(\s*)(\d+[.)])\s+(.+)$/gm, '<li class="li-ordered">$3</li>')
  out = out.replace(/((?:<li class="li-unordered">.*?<\/li>\s*)+)/gs, (_m, items: string) => {
    const clean = items.replace(/ class="li-unordered"/g, '')
    return `<ul>${clean}</ul>`
  })
  out = out.replace(/((?:<li class="li-ordered">.*?<\/li>\s*)+)/gs, (_m, items: string) => {
    const clean = items.replace(/ class="li-ordered"/g, '')
    return `<ol>${clean}</ol>`
  })

  // 5. 段落
  const lines = out.split('\n')
  const result: string[] = []
  let para: string[] = []
  const flush = () => {
    if (para.length) {
      const p = para.join(' ').trim()
      if (p) result.push(`<p>${p}</p>`)
      para = []
    }
  }
  for (const line of lines) {
    const trimmed = line.trim()
    const isBlock = trimmed.match(/^<(h[1-6]|ul|ol|li|blockquote|hr|p|table)/)
    if (isBlock) { flush(); result.push(line) }
    else if (trimmed === '') { flush() }
    else para.push(trimmed)
  }
  flush()
  out = result.join('\n')

  // 6. 行内格式
  out = out.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
  out = out.replace(/__(.+?)__/g, '<strong>$1</strong>')
  // ★ E17：旧写法 (?<!\*) 为 lookbehind——Safari <16.4 在【脚本编译期】即抛 SyntaxError 整页白屏。
  //   等价改写：前导用 (^|[^*]) 捕获组、尾部仅用 lookahead（各引擎均支持）。
  out = out.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>')
  out = out.replace(/`([^`]+)`/g, '<code>$1</code>')

  // 7. 清理段落内多余 <br>
  out = out.replace(/<p>(.*?)<\/p>/gs, (_m, inner: string) => {
    const cleaned = inner.replace(/<br>\s*$/, '')
    return `<p>${cleaned}</p>`
  })

  return out
}

// ---- 工具函数（文件名/图标/类型标签/图片判定，行为同 Vue 版）----
// 从路径中解码并提取文件名字段
