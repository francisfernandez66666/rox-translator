// ============================================================================
// MailTplP.manualBody.dom.test.tsx — manual（注册欢迎手册）模板的正文框退役锁
// （★ 2026-11-07 用户拍板 B：AGENTS §31 留的死字段口子按"摘面"收口）
// 判据四件事：
//   ① manual 卡**没有正文 textarea**，取而代之的是"正文由系统生成"的说明条（body 框＝改了不生效的假入口）；
//   ② 其余模板卡**照旧有正文 textarea**（正向对照——摘掉豁免条件时这一条不许跟着变绿，证明锁有射程）；
//   ③ 改 manual 主题并保存 ⇒ 上送的 body **等于库里现值**（透传，不许借"摘框"顺手把配置抹成空串）；
//   ④ 说明条文案取词非空（12 语种键真在词典里，不是渲染出裸 key 或空串）。
// 运行：npx vitest run src/components/admin/MailTplP.manualBody.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import MailTplP from './MailTplP'

const mocks = vi.hoisted(() => ({
  mailTemplatesGet: vi.fn(),
  mailTemplatesSave: vi.fn(),
}))

// 只替换本面板用到的两个接口（整模块打空会波及无关导出，照 ApiKeysP.delete 的先例）
vi.mock('@/api/admin', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/admin')>()
  return { ...actual, mailTemplatesGet: mocks.mailTemplatesGet, mailTemplatesSave: mocks.mailTemplatesSave }
})

vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}))

const MANUAL_BODY = '历史长文正文（库里现值，投递并不读它）'
const TPLS = [
  { code: 'manual', name: '产品手册(注册欢迎)', desc: '说明', vars: ['username', 'brand'], subject: '欢迎主题', body: MANUAL_BODY, cc: '', is_modified: false },
  { code: 'welcome', name: '欢迎信', desc: '说明', vars: ['brand'], subject: '普通欢迎', body: '欢迎正文', cc: '', is_modified: false },
]

describe('MailTplP manual 正文框退役', () => {
  beforeEach(() => {
    mocks.mailTemplatesGet.mockResolvedValue({ success: true, templates: TPLS })
    mocks.mailTemplatesSave.mockResolvedValue({ success: true })
  })
  afterEach(() => { cleanup(); vi.clearAllMocks() })

  it('① manual 卡没有正文 textarea，取而代之是说明条', async () => {
    render(<MailTplP />)
    await screen.findByText('产品手册(注册欢迎)')
    const note = await screen.findByTestId('mailtpl-body-locked')
    expect(note).toBeTruthy()
    // 说明条所在卡片内不许再出现任何 textarea：面板共两卡，manual 卡 0 个、welcome 卡 1 个 ⇒ 总数必须恰为 1
    expect(document.querySelectorAll('textarea').length).toBe(1)
  })

  it('② 其余模板卡照旧有正文 textarea（正向对照，射程自证）', async () => {
    render(<MailTplP />)
    await screen.findByText('欢迎信')
    const tas = Array.from(document.querySelectorAll('textarea'))
    expect(tas.some((x) => (x as HTMLTextAreaElement).value === '欢迎正文')).toBe(true)
  })

  it('③ 改 manual 主题保存 ⇒ body 原样透传库里现值', async () => {
    render(<MailTplP />)
    await screen.findByText('产品手册(注册欢迎)')
    // manual 卡的主体输入框＝该卡第一个 input（testid 锚点由组件提供）
    const manualCard = screen.getByTestId('mailtpl-card-manual')
    const subj = manualCard.querySelector('input') as HTMLInputElement
    fireEvent.change(subj, { target: { value: '新主题' } })
    fireEvent.click(manualCard.querySelectorAll('button')[0])
    await waitFor(() => expect(mocks.mailTemplatesSave).toHaveBeenCalled())
    const arg = mocks.mailTemplatesSave.mock.calls[0][0]
    expect(arg.manual.subject).toBe('新主题')
    expect(arg.manual.body).toBe(MANUAL_BODY)
    expect(arg.manual.body).not.toBe('')
  })

  it('④ 说明条取词非空且不是裸 key', async () => {
    render(<MailTplP />)
    const note = await screen.findByTestId('mailtpl-body-locked')
    const tx = note.textContent || ''
    expect(tx.length).toBeGreaterThan(8)
    expect(tx).not.toContain('mailTpl.bodyBySystem')
  })
})
