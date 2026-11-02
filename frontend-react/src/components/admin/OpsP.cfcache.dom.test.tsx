// ============================================================================
// OpsP.cfcache.dom.test.tsx — 运营策略面板里的「边缘缓存（Cloudflare）」卡片
// ★ 2026-11-02 用户令「把 CF 的 token 配置也放在管理后台，平台管理员配置并可热更新」。
//
// 锁的是**界面这一侧**的四条（后端 admin_cf_cache_gate_test.go 锁服务端那四条）：
//   ① 掩码只做 placeholder，输入框**永远不回填真值**（type=password + 空 value）；
//   ② 保存把三个字段原样（trim 后）送出去，`file_note` 非空时必须出声——
//      包括「新投影写成了、旧投影没删掉」这一档（旧形态只看 file_written，这一档全程无声）；
//   ③ 清空是危险动作：先弹**一次**确认框（danger），取消则一个请求都不发；
//   ④ 投影文件在场/缺失的 badge 文案走词典（旧形态写死英文 `file ✓/—`）。
//
// 运行：npx vitest run src/components/admin/OpsP.cfcache.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { OpsP } from './panels_e'
import { DialogHost } from '@/components/uiDialogs'
import { toastSuccess, toastError } from '@/lib/toastBus'

// 把所有后端调用打桩：本文件只验"面板怎么画、点了发什么"，不碰真网络。
const mocks = vi.hoisted(() => ({
  opsPolicy: vi.fn(),
  opsSlo: vi.fn(),
  opsRoutes: vi.fn(),
  opsPolicySave: vi.fn(),
  opsWindowSave: vi.fn(),
  opsPackageReset: vi.fn(),
  cfCacheState: vi.fn(),
  cfCacheSave: vi.fn(),
  cfCachePurge: vi.fn(),
}))

// 只替换本卡片真正会调的接口，其余保持真实导出（stores/admin 也从 @/api 取，
// 整模块 mock 会把它们一起打空——同 ApiKeysP.delete 那条夹具的教训）。
vi.mock('@/api/ops', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/ops')>()
  return {
    ...actual,
    opsPolicy: mocks.opsPolicy,
    opsSlo: mocks.opsSlo,
    opsRoutes: mocks.opsRoutes,
    opsPolicySave: mocks.opsPolicySave,
    opsWindowSave: mocks.opsWindowSave,
    opsPackageReset: mocks.opsPackageReset,
    cfCacheState: mocks.cfCacheState,
    cfCacheSave: mocks.cfCacheSave,
    cfCachePurge: mocks.cfCachePurge,
  }
})

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 0, myLevel: 4, tenants: [], switchTenant: () => {} }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}))

/** 一份「已配置且投影文件在场」的状态夹具（字段名与后端出参逐字同形）。 */
const CF_SET = {
  success: true,
  set: true,
  source: 'db',
  masked: 'cfut****cdef',
  db_masked: 'cfut****cdef',
  env_overridden: false,
  zone: 'lexicorn.cn',
  token_file: '/opt/translator/data/cf_token',
  file_present: true,
  purge_last: '',
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.opsPolicy.mockResolvedValue({ success: true, effective: {} })
  mocks.opsSlo.mockResolvedValue({ success: true, slos: [] })
  mocks.opsRoutes.mockResolvedValue({ success: true })
  mocks.cfCacheState.mockResolvedValue({ ...CF_SET })
  mocks.cfCacheSave.mockResolvedValue({ ...CF_SET, changed: true, file_written: true, file_note: '' })
  mocks.cfCachePurge.mockResolvedValue({ success: true, ok: true, zone: 'lexicorn.cn', http_code: '200', source: 'db' })
})

// 确认框探针：危险动作（清除凭据）走统一的 DialogHost，靠 DOM 里那一层 `.lc-dialog` 判在不在。
const dialogOpen = () => !!document.querySelector('.lc-dialog')
// 「确定」/「危险」两枚主按钮（清除凭据带 danger 档，两者的选择器都认）。
const dialogConfirm = () => document.querySelector('.lc-dialog .lc-btn--primary, .lc-dialog .lc-btn--danger') as HTMLElement

/** 等卡片把 GET 状态读回来（token 输入框的 placeholder 是"已配置"那一支的唯一证据）。 */
async function waitCard() {
  await vi.waitFor(() => {
    expect(screen.getByTestId('ops-cf-token')).toBeTruthy()
    expect((screen.getByTestId('ops-cf-zone') as HTMLInputElement).value).toBe('lexicorn.cn')
  })
}

describe('OpsP · 边缘缓存（Cloudflare）卡片', () => {
  it('掩码只进 placeholder；输入框不回填真值；badge 文案走词典', async () => {
    render(<OpsP />)
    await waitCard()
    const tok = screen.getByTestId('ops-cf-token') as HTMLInputElement
    // ① 真值绝不进 value（浏览器拿不到明文是后端口径，这里是它的第二道）
    expect(tok.value).toBe('')
    expect(tok.getAttribute('type')).toBe('password')
    expect(tok.placeholder).toBe(CF_SET.masked)
    // ② 投影文件在场 ⇒ 词典里那句中文（旧形态是写死的英文 `file ✓`）
    const badge = screen.getByTestId('ops-cf-file-badge')
    expect(badge.textContent).toBe('投影文件在场')
    // title 给路径，鼠标悬停即可核对投影落在哪
    expect(badge.getAttribute('title')).toBe(CF_SET.token_file)
  })

  it('未配置时 badge 报"缺失"、placeholder 报"未配置"', async () => {
    mocks.cfCacheState.mockResolvedValue({ ...CF_SET, set: false, masked: '', file_present: false })
    render(<OpsP />)
    await waitCard()
    expect((screen.getByTestId('ops-cf-token') as HTMLInputElement).placeholder).toBe('未配置')
    expect(screen.getByTestId('ops-cf-file-badge').textContent).toBe('投影文件缺失')
  })

  it('保存把三个字段 trim 后送出；file_note 非空必须出声（含"新文件写成、旧文件没删掉"那一档）', async () => {
    render(<OpsP />)
    await waitCard()
    // 受控输入必须走原生 setter + input 事件（直接赋 value React 不认）
    const fire = (el: HTMLElement, v: string) => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(el, v)
      el.dispatchEvent(new Event('input', { bubbles: true }))
    }
    fire(screen.getByTestId('ops-cf-token'), '  cfut_NEW_0123456789  ')
    fire(screen.getByTestId('ops-cf-zone'), ' lexicorn.cn ')
    fire(screen.getByTestId('ops-cf-file'), ' /opt/translator/data/cf_token ')

    // ① 正常保存：成功提示、无错误提示
    screen.getByTestId('ops-cf-save').click()
    await vi.waitFor(() => { expect(mocks.cfCacheSave).toHaveBeenCalledTimes(1) })
    expect(mocks.cfCacheSave).toHaveBeenCalledWith({
      token: 'cfut_NEW_0123456789', zone: 'lexicorn.cn', token_file: '/opt/translator/data/cf_token',
    })
    await vi.waitFor(() => { expect(toastSuccess).toHaveBeenCalled() })
    expect(toastError).not.toHaveBeenCalled()

    // ② 这一档才是旧形态漏掉的：file_written=true 但 file_note 非空（旧投影删不掉）
    vi.clearAllMocks()
    mocks.cfCacheSave.mockResolvedValue({
      ...CF_SET, changed: true, file_written: true, file_note: '旧导出文件删除失败：directory not empty',
    })
    fire(screen.getByTestId('ops-cf-file'), ' /opt/translator/data/cf_token ')
    screen.getByTestId('ops-cf-save').click()
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledTimes(1) })
    expect(String((toastError as any).mock.calls[0][0])).toContain('旧导出文件删除失败')
    expect(toastSuccess).not.toHaveBeenCalled()
  })

  it('投影没写成（file_written=false）也出声，文案带后端那句原因', async () => {
    mocks.cfCacheSave.mockResolvedValue({ ...CF_SET, changed: true, file_written: false, file_note: '导出目录不存在：/nope' })
    render(<OpsP />)
    await waitCard()
    screen.getByTestId('ops-cf-save').click()
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledTimes(1) })
    expect(String((toastError as any).mock.calls[0][0])).toContain('导出目录不存在')
  })

  it('清空：弹一次 danger 确认；取消则一个请求都不发', async () => {
    render(<><OpsP /><DialogHost /></>)
    await waitCard()
    screen.getByTestId('ops-cf-clear').click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    // 危险动作必须是 danger 档
    expect(document.querySelector('.lc-dialog .lc-btn--danger')).toBeTruthy()
    // 取消位是 secondary 里的「取消」
    const cancel = Array.from(document.querySelectorAll('.lc-dialog .lc-btn--secondary')).pop() as HTMLElement
    cancel.click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(false) })
    expect(mocks.cfCacheSave).not.toHaveBeenCalled()
  })

  it('清空确认一次 ⇒ 请求已发（clear=true）、弹窗已关', async () => {
    render(<><OpsP /><DialogHost /></>)
    await waitCard()
    screen.getByTestId('ops-cf-clear').click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    dialogConfirm().click()
    await vi.waitFor(() => { expect(mocks.cfCacheSave).toHaveBeenCalledTimes(1) })
    expect(mocks.cfCacheSave).toHaveBeenCalledWith({ clear: true })
    await vi.waitFor(() => { expect(dialogOpen()).toBe(false) })
  })

  it('立即清理：调 purge 并报出 zone；未配置时两颗按钮都禁用', async () => {
    render(<OpsP />)
    await waitCard()
    screen.getByTestId('ops-cf-purge').click()
    await vi.waitFor(() => { expect(mocks.cfCachePurge).toHaveBeenCalledTimes(1) })
    await vi.waitFor(() => { expect(toastSuccess).toHaveBeenCalled() })
    expect(String((toastSuccess as any).mock.calls[0][0])).toContain('lexicorn.cn')

    // 未配置：purge/clear 必须禁用（点下去必然失败的按钮＝缺陷）
    cleanup()
    mocks.cfCacheState.mockResolvedValue({ ...CF_SET, set: false, masked: '', file_present: false })
    render(<OpsP />)
    await waitCard()
    expect((screen.getByTestId('ops-cf-purge') as HTMLButtonElement).disabled).toBe(true)
    expect((screen.getByTestId('ops-cf-clear') as HTMLButtonElement).disabled).toBe(true)
  })
})
