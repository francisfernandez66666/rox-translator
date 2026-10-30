// ============================================================================
// TicketsPage.artifacts.dom.test.tsx — 组5 两段式交付的前端锁（B12）
//
// 钉住四件事（与后端 artifacts 语义一一对应）：
//   ① md 有行 ⇒ 「预览」「下载 md」在位；
//   ② original 未就绪且工单仍在跑 ⇒ 只出「生成中」文字、**不出**下载按钮（进行中不等于坏了）；
//   ③ original 未就绪且工单已终态 ⇒ 出「原格式未生成」原因（不是笼统"失败"）；
//   ④ original 就绪 ⇒ 两个下载都在；预览点击后 `<pre>` 出文本、截断时有提示。
//
// 反证：把 original 的 running 判断摘掉 ⇒ ② 红；把 preview 的 truncated 提示删掉 ⇒ ④ 后半红。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, cleanup, waitFor, fireEvent } from '@testing-library/react'
import { setLang, t } from '@/i18n'

// 产物接口走桩：预览返回固定文本并带截断标记。
const previewFn = vi.hoisted(() => vi.fn())
const dlFn = vi.hoisted(() => vi.fn())
vi.mock('@/api/tickets', async (orig) => {
  const mod: any = await orig()
  return { ...mod, ticketArtifactPreview: previewFn, ticketArtifactDownload: dlFn }
})

import { TicketArtifacts } from './TicketsPage'

const mdOnly = (running: boolean): any => ({
  ticketId: 7,
  status: running ? 'in_progress' : 'completed',
  files: [{ id: 3, file_name: '合同.pdf' }],
  lang: 'zh',
  t,
  groups: [{
    key: '3|en', fileId: 3, lang: 'en',
    items: [{ id: 11, file_id: 3, lang: 'en', kind: 'md' as const, name: '合同_en.md', size: 1200, primary: false, degraded: running ? false : true }],
  }],
})

beforeEach(() => {
  cleanup()
  setLang('zh')
  previewFn.mockReset()
  dlFn.mockReset()
  previewFn.mockResolvedValue({ text: 'Hello world', truncated: true })
})

describe('TicketArtifacts 两段式产物区', () => {
  it('md 在位、original 生成中 ⇒ 只出「生成中」，不出下载原格式', () => {
    const { getByTestId, container } = render(<TicketArtifacts {...mdOnly(true)} />)
    expect(getByTestId('tk-artifacts')).toBeTruthy()
    const pending = getByTestId('tk-original-pending')
    expect(pending.textContent).toBe(t('tk.originalPending'))
    // 不许出现「下载原格式」这个动作词
    expect(container.textContent || '').not.toContain(t('tk.downloadOriginal'))
  })

  it('工单已终态仍无 original ⇒ 给具体原因，不是"失败"', () => {
    const { getByTestId } = render(<TicketArtifacts {...mdOnly(false)} />)
    expect(getByTestId('tk-original-pending').textContent).toBe(t('tk.originalUnavailable'))
  })

  it('两件就绪 ⇒ 两个下载按钮都在；点预览出文本与截断提示', async () => {
    const props = mdOnly(false)
    props.groups[0].items.push({ id: 12, file_id: 3, lang: 'en', kind: 'original' as const, name: '合同_en.pdf', size: 9000, primary: true, degraded: false })
    const { container, getByText, findByTestId } = render(<TicketArtifacts {...props} />)
    expect(container.textContent || '').toContain(t('tk.downloadOriginal'))
    expect(container.textContent || '').toContain(t('tk.downloadMd'))
    fireEvent.click(getByText(t('tk.preview')))
    const pre = await findByTestId('tk-preview')
    expect(pre.textContent).toBe('Hello world')
    await waitFor(() => expect(container.textContent || '').toContain(t('tk.previewTruncated')))
  })
})
