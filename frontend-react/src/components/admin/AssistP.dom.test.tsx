// ============================================================================
// AssistP.dom.test.tsx — AI 助手管理面板组件测试（★ #34 前端重做，2026-09-21）
// 旧版锁的是「Token 注入 localStorage + iframe」，那正是本次要废除的形态，故整体重写。
// 现在锁住四条改坏就有实际风险的行为：
//   ① 凭据不进浏览器：面板不渲染 iframe、不写 localStorage('assist_tok')，
//      状态横幅只回显上游可达性与 Token 来源（env/db/none）；
//   ② fail-closed 可读：服务不可达 / Token 未配置 / 上游业务失败时给出处置指引，
//      而不是白屏或把「拿不到数据」显示成空列表；
//   ③ 数据面 CRUD：切页签按需拉列表、新建提交补齐区域默认值、key 为空本地拦下不发请求、
//      编辑态 key 输入禁用（assist 侧按 key 命中，改 key 等于换条目）、删除需二次确认；
//   ④ 配置与连通测试：LLM 四项回填掩码密钥、「测试连通」回显模型与耗时、Token 轮换后重拉数据。
// 接口层用 vi.mock 的 importOriginal 形态：保留真实 AssistBizError（面板靠 instanceof 分流），
// 只把网络函数换成 spy——换成自造错误类会让 instanceof 恒 false，掩盖真实分支。
// 运行：npx vitest run src/components/admin/AssistP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import AssistP from './AssistP'
import { AssistBizError } from '@/api/assistAdmin'
import { DialogHost } from '@/components/uiDialogs'
import { toastError } from '@/lib/toastBus'

const m = vi.hoisted(() => ({
  status: vi.fn(), sessions: vi.fn(), list: vi.fn(), create: vi.fn(), update: vi.fn(),
  del: vi.fn(), config: vi.fn(), configSet: vi.fn(), llmTest: vi.fn(), rotate: vi.fn(),
}))

vi.mock('@/api/assistAdmin', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api/assistAdmin')>()
  return {
    ...real,
    assistAdminStatus: m.status,
    assistAdminSessions: m.sessions,
    assistAdminList: m.list,
    assistAdminCreate: m.create,
    assistAdminUpdate: m.update,
    assistAdminDelete: m.del,
    assistAdminConfig: m.config,
    assistAdminConfigSet: m.configSet,
    assistAdminLLMTest: m.llmTest,
  }
})

// 主后台 Token 轮换接口（面板保留的能力：后端写库为密文，前端只提交一次明文）
vi.mock('@/api', () => ({ adminAssistTokenRotate: m.rotate }))

vi.mock('@/api/assist', () => ({ ASSIST_API: '/assist-api' }))

vi.mock('@/lib/toastBus', () => ({ toastSuccess: vi.fn(), toastError: vi.fn(), toastWarn: vi.fn() }))

/** 一条知识库条目（含分类/关键词/优先级，覆盖 code 列与长文本列两类渲染） */
const kbRow = { id: 7, key: 'kb_price', category: 'billing', title: '价格咨询', content: '按字数计费', keywords: '价格,费用', link_keys: 'feats', priority: 10, enabled: 1 }

const dialogConfirm = () => document.querySelector('.lc-dialog .lc-btn--primary') as HTMLElement
/** 页签与其所在面板标题同名，取最后一个即 Tabs 的按钮位 */
const tabBtn = (label: string) => {
  const els = screen.getAllByText(label)
  return els[els.length - 1] as HTMLElement
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.clear()
  m.status.mockResolvedValue({ success: true, base_url: 'http://127.0.0.1:8790', reachable: true, token_src: 'db', message: '' })
  m.sessions.mockResolvedValue({
    sessions: [{ id: 's1757', page_url: '/translate', msg_count: 4, in_flow: '', last_at: '2026-09-21T08:00:00Z' }],
    total: 12, messages: 88, unanswered: ['你们支持印度语吗'], llm_mode: 'rule',
  })
  m.list.mockResolvedValue([kbRow])
  m.create.mockResolvedValue({ id: 8 })
  m.update.mockResolvedValue({ ok: true })
  m.del.mockResolvedValue({ ok: true })
  m.config.mockResolvedValue([
    { key: 'welcome', value: '你好，我是 LANGCross 助手' },
    { key: 'llm_base_url', value: 'https://api.example.com/v1' },
    { key: 'llm_api_key', value: 'sk-1***xy' },
    { key: 'llm_model', value: 'gpt-4o-mini' },
    { key: 'temperature', value: '0.3' },
  ])
  m.configSet.mockResolvedValue({ ok: true })
  m.llmTest.mockResolvedValue({ ok: true, model: 'gpt-4o-mini', ms: 412 })
  m.rotate.mockResolvedValue({ success: true })
})

describe('AI 助手面板 · 凭据不外泄（#34 核心约束）', () => {
  it('渲染原生面板：无 iframe、不写 assist_tok，横幅回显 Token 来源', async () => {
    const { container } = render(<AssistP />)
    await vi.waitFor(() => { expect(document.body.textContent).toMatch(/服务在线，管理凭据由后端注入/) })
    expect(container.querySelector('iframe')).toBeNull()
    expect(localStorage.getItem('assist_tok')).toBeNull()
    expect(document.body.textContent).toMatch(/主后台库内配置/)
    // 概览：统计三项 + 未答问题清单 + 会话表（旧 iframe 里才有的东西现在必须原生可读）
    expect(document.body.textContent).toMatch(/会话总数[\s\S]*12/)
    expect(document.body.textContent).toMatch(/你们支持印度语吗/)
    expect(screen.getByText('s1757')).toBeTruthy()
    expect(m.sessions).toHaveBeenCalled()
  })

  it('服务不可达：给出处置指引而不是空列表', async () => {
    m.status.mockResolvedValue({ success: true, base_url: 'http://127.0.0.1:8790', reachable: false, token_src: 'db', message: '' })
    render(<AssistP />)
    await vi.waitFor(() => { expect(document.body.textContent).toMatch(/AI 助手服务不可达/) })
    expect(document.body.textContent).toMatch(/assist-server/)
  })

  it('Token 未配置：提示补配置路径（fail-closed 的可读版本）', async () => {
    m.status.mockResolvedValue({ success: true, base_url: 'http://127.0.0.1:8790', reachable: true, token_src: 'none', message: '' })
    render(<AssistP />)
    await vi.waitFor(() => { expect(document.body.textContent).toMatch(/尚未配置管理 Token/) })
  })
})

describe('AI 助手面板 · 数据面 CRUD', () => {
  it('切到知识库页签按需拉列表并回显字段', async () => {
    render(<AssistP />)
    await vi.waitFor(() => { expect(m.sessions).toHaveBeenCalled() })
    fireEvent.click(tabBtn('知识库'))
    await vi.waitFor(() => {
      expect(m.list).toHaveBeenCalledWith('kb')
      expect(screen.getByText('kb_price')).toBeTruthy()
      expect(screen.getByText('价格咨询')).toBeTruthy()
    })
  })

  it('新建提交补齐区域默认值；key 为空本地拦下不发请求', async () => {
    render(<><AssistP /><DialogHost /></>)
    await vi.waitFor(() => { expect(m.sessions).toHaveBeenCalled() })
    fireEvent.click(tabBtn('话术'))
    await vi.waitFor(() => { expect(m.list).toHaveBeenCalledWith('scripts') })
    fireEvent.click(screen.getByText('新建'))
    expect(m.create).not.toHaveBeenCalled()
    fireEvent.click(dialogConfirm())
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledWith('标识 key 不能为空（它是这条记录的唯一键）') })
    expect(m.create).not.toHaveBeenCalled()

    fireEvent.change(document.querySelector('input[aria-label="标识 key"]') as HTMLInputElement, { target: { value: 'sc_price' } })
    fireEvent.change(document.querySelector('textarea[aria-label="内容"]') as HTMLTextAreaElement, { target: { value: '按字数计费' } })
    fireEvent.click(dialogConfirm())
    await vi.waitFor(() => { expect(m.create).toHaveBeenCalledTimes(1) })
    expect(m.create.mock.calls[0][0]).toBe('scripts')
    // stype/enabled/priority 等未填字段要带 assist 侧默认值，不能提交 undefined（动态表会漏列）
    expect(m.create.mock.calls[0][1]).toMatchObject({ key: 'sc_price', stype: 'keyword', enabled: 1, priority: 5, link_keys: '' })
    await vi.waitFor(() => { expect(m.list.mock.calls.filter((c) => c[0] === 'scripts').length).toBeGreaterThanOrEqual(2) })
  })

  it('编辑态 key 输入禁用，保存走 PUT 且按 id 定位', async () => {
    render(<><AssistP /><DialogHost /></>)
    await vi.waitFor(() => { expect(m.sessions).toHaveBeenCalled() })
    fireEvent.click(tabBtn('知识库'))
    await vi.waitFor(() => { expect(screen.getByText('kb_price')).toBeTruthy() })
    fireEvent.click(screen.getByText('编辑'))
    const keyInput = document.querySelector('input[aria-label="标识 key"]') as HTMLInputElement
    expect(keyInput.value).toBe('kb_price')
    expect(keyInput.disabled).toBe(true)
    fireEvent.click(dialogConfirm())
    await vi.waitFor(() => { expect(m.update).toHaveBeenCalledTimes(1) })
    expect(m.update.mock.calls[0][0]).toBe('kb')
    expect(m.update.mock.calls[0][1]).toBe(7)
  })

  it('启停只提交 enabled；删除需二次确认后才下发', async () => {
    const { container } = render(<><AssistP /><DialogHost /></>)
    await vi.waitFor(() => { expect(m.sessions).toHaveBeenCalled() })
    fireEvent.click(tabBtn('知识库'))
    await vi.waitFor(() => { expect(screen.getByText('kb_price')).toBeTruthy() })
    fireEvent.click(container.querySelector('input[type="checkbox"]') as HTMLInputElement)
    await vi.waitFor(() => { expect(m.update).toHaveBeenCalledWith('kb', 7, { enabled: 0 }) })

    m.update.mockClear()
    fireEvent.click(screen.getByText('删除'))
    await vi.waitFor(() => { expect(document.body.textContent).toMatch(/确定删除这条记录吗/) })
    expect(m.del).not.toHaveBeenCalled()
    fireEvent.click(dialogConfirm())
    await vi.waitFor(() => { expect(m.del).toHaveBeenCalledWith('kb', 7) })
  })

  it('上游业务失败（代理 fail-closed 的 message）走横幅展示，不静默吞掉', async () => {
    m.sessions.mockRejectedValue(new AssistBizError('AI 助手服务未配置管理 Token'))
    render(<AssistP />)
    await vi.waitFor(() => { expect(document.body.textContent).toMatch(/AI 助手服务未配置管理 Token/) })
  })
})

describe('AI 助手面板 · 配置与连通测试', () => {
  it('配置页签回填掩码密钥；「测试连通」回显模型与耗时', async () => {
    render(<AssistP />)
    await vi.waitFor(() => { expect(m.sessions).toHaveBeenCalled() })
    fireEvent.click(tabBtn('配置'))
    let keyInput: HTMLInputElement
    await vi.waitFor(() => {
      expect(m.config).toHaveBeenCalled()
      keyInput = document.querySelector('input[aria-label="llm_api_key"]') as HTMLInputElement
      expect(keyInput.value).toBe('sk-1***xy')
    })
    // 密钥必须走 password 型输入框（肩屏/录屏下不明文铺陈）
    expect(keyInput!.type).toBe('password')
    expect((document.querySelector('input[aria-label="llm_model"]') as HTMLInputElement).value).toBe('gpt-4o-mini')

    fireEvent.click(screen.getByText('测试连通'))
    await vi.waitFor(() => { expect(m.llmTest).toHaveBeenCalled() })
    await vi.waitFor(() => { expect(document.body.textContent).toMatch(/连通正常：gpt-4o-mini，耗时 412ms/) })
  })

  it('轮换 Token 成功后重拉状态与当前页签，且明文不进 localStorage', async () => {
    render(<AssistP />)
    await vi.waitFor(() => { expect(m.sessions).toHaveBeenCalled() })
    fireEvent.change(document.querySelector('input[aria-label="管理 Token"]') as HTMLInputElement, { target: { value: 'new-secret-token' } })
    fireEvent.click(screen.getByText('保存 Token'))
    await vi.waitFor(() => { expect(m.rotate).toHaveBeenCalledWith('new-secret-token') })
    await vi.waitFor(() => { expect(m.sessions.mock.calls.length).toBeGreaterThanOrEqual(2) })
    expect(m.status.mock.calls.length).toBeGreaterThanOrEqual(2)
    expect(localStorage.getItem('assist_tok')).toBeNull()
  })
})
