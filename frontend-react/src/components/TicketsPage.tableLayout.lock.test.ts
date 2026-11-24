import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'

// 读取工单页源码并剥掉整行注释，避免说明文字喂饱布局锁。
const raw = readFileSync('src/components/TicketsPage.tsx', 'utf8')
// 剥注释后的实现文本供三条布局判据共用。
const code = raw.split('\n').filter((line) => !line.trim().startsWith('//')).join('\n')

describe('工单表格操作列与名称列布局锁', () => {
  it('桌面表固定总宽，操作列粘在逻辑行尾且不被压缩', () => {
    expect(code).toContain('.tk-table .lc-table{min-width:1280px;table-layout:fixed}')
    expect(code).toMatch(/\.tk-table \.lc-table td:last-child\{[^}]*position:sticky;inset-inline-end:0;width:280px;min-width:280px/)
    expect(code).toMatch(/\.tk-table \.lc-table td:last-child\{[^}]*white-space:normal/)
    expect(code).toContain('[dir="rtl"] .tk-table .lc-table td:last-child{box-shadow:10px 0 14px -16px')
  })

  it('移动端退回卡片布局，解除粘性定位与定宽', () => {
    const mobile = code.slice(code.indexOf('@media (max-width: 640px)'))
    expect(mobile).toContain('.tk-table .lc-table{min-width:0;table-layout:auto}')
    expect(mobile).toMatch(/\.tk-table \.lc-table td:last-child\{position:static;width:auto;min-width:0;max-width:none/)
  })

  it('名称列走可展开组件，不再用普通 span', () => {
    expect(code).toContain('<TicketNameCell text={row.title || t(\'tk.defaultTitle\')} />')
    expect(code).toContain('className={`tk-ticket-name${expanded ? \' is-expanded\' : \'\'}`}')
    expect(code).toContain('title={text}')
  })
})
