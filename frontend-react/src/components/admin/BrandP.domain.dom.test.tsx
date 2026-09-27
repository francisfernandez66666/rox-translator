// ============================================================================
// components/admin/BrandP.domain.dom.test.tsx — 品牌域前缀的前端预检（★ F-76）
//
// 因果链：读侧一直是 `WHERE domain=?` 的裸等值匹配，而写侧过去把用户输入原样入库。
// 客户在后台填 `ROX`、`rox.lexicorn.cn`、`auto-bot` 都会「保存成功」，
// 但自己的品牌域永远打不开（白牌页）——只能截图找客服。后端已补归一 + 字符集 + 占用校验
// （Go 锁＝branding_domain_test.go）；本文件锁前端这一层：
//   ① 格式说明常驻在输入框下方（客户填之前就知道允许什么字符）；
//   ② 写错时同一位置换成词典原文的报错，且**点保存不得发出请求**（防「弹提示照发」的假拦截）；
//   ③ 反向对照 A：合规值照常提交，载荷是归一后的小写前缀；
//   ④ 反向对照 B：库里存量的不合规历史值（如演示站 rox-test）**整表回提时不拦**——
//      后端对未改动的存量值原样放行，前端若照判就会把"换个 Logo"一起堵死，两侧必须同一把尺子。
//
// 期望文案一律现读词典（baseZh），改文案即改期望，不写死字面量。
// 运行：npx vitest run src/components/admin/BrandP.domain.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import BrandP from './BrandP'
import { baseZh } from '@/i18n/dicts.zh'

// S = 可控状态：保存调用采集；回包 domain / brand_name 由用例注入（模拟"存量值"与"干净值"两种租户）
const S = vi.hoisted(() => ({
  saves: [] as Array<Record<string, unknown>>,
  initDomain: 'autobot',
  initBrandName: '极光汽车',
}))

vi.mock('@/api/branding', () => ({
  tenantBranding: vi.fn(async () => ({
    success: true,
    tenant_id: 3,
    brand_name: S.initBrandName,
    brand_name_en: '',
    brand_names: '{}',
    brand_logo: '/brand/logo.png',
    domain: S.initDomain,
    brand_home_bg: '',
    brand_home_bg_style: '',
    brand_login_card_pos: '',
    brand_login_layout: '',
    brand_links: '[]',
    brand_paid: true,
    brand_granted: false,
    brand_root: false,
    dedicated_register: false,
  })),
  tenantBrandingSave: vi.fn(async (p: Record<string, unknown>) => { S.saves.push(p); return { success: true } }),
  brandGrant: vi.fn(async () => ({ success: true })),
  footerLinksGet: vi.fn(async () => ({ success: true, links: [] })),
  footerLinksSet: vi.fn(async () => ({ success: true })),
}))

// toast 采集：报错文案是否走 toast、且保存接口零调用，是"假拦截"的判据对
const Toast = vi.hoisted(() => ({ ok: vi.fn(), err: vi.fn() }))
vi.mock('@/lib/toastBus', () => ({
  toastSuccess: Toast.ok,
  toastError: Toast.err,
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ myLevel: 4, activeTenantId: 3, tenants: [], switchTenant: () => {} }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

const $ = (sel: string) => Array.from(document.querySelectorAll(sel)) as HTMLElement[]

// domainInput 按占位符定位子域名输入框（面板里还有品牌名/Logo URL 等 lc-input，不能按顺序取）
function domainInput(): HTMLInputElement {
  const el = $('input.lc-input').find((i) => (i as HTMLInputElement).placeholder === baseZh['brand.domainPlaceholder'])
  if (!el) throw new Error('未找到品牌域输入框（BrandP 结构变了？锁需随迁）')
  return el as HTMLInputElement
}

function saveButton(): HTMLButtonElement {
  const btn = $('button').find((b) => (b.textContent ?? '').trim() === baseZh['brand.save'])
  if (!btn) throw new Error('未找到品牌保存钮')
  return btn as HTMLButtonElement
}

// ruleLine 取输入框下方那行提示（格式说明与报错文案共用同一位置）
function hintLines(): string[] {
  return $('div').map((d) => (d.textContent ?? '').trim()).filter((s) => s === baseZh['brand.domainRule'] || s === baseZh['brand.domainInvalid'])
}

async function mount() {
  render(<BrandP />)
  await vi.waitFor(() => expect(domainInput()).toBeTruthy())
  await vi.waitFor(() => expect(domainInput().value).toBe(S.initDomain))
}

beforeEach(() => {
  cleanup()
  S.saves.length = 0
  S.initDomain = 'autobot'
  S.initBrandName = '极光汽车'
  Toast.ok.mockClear()
  Toast.err.mockClear()
})

describe('BrandP · F-76 品牌域前缀预检', () => {
  it('① 格式说明常驻：未改动时显示词典原文，不显示报错', async () => {
    await mount()
    expect(document.body.textContent).toContain(baseZh['brand.domainRule'])
    expect(document.body.textContent).not.toContain(baseZh['brand.domainInvalid'])
  })

  it('② 写错字符集：同一位置变红字报错，点保存零请求（不是"弹提示照发"）', async () => {
    await mount()
    fireEvent.change(domainInput(), { target: { value: 'auto-bot' } })
    await vi.waitFor(() => expect(document.body.textContent).toContain(baseZh['brand.domainInvalid']))
    fireEvent.click(saveButton())
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length, '预检不过时不得调用保存接口').toBe(0)
    expect(Toast.err).toHaveBeenCalledWith(baseZh['brand.domainInvalid'])
  })

  it('③ 反向对照 A：合规值照常提交，且提交的是原样小写前缀', async () => {
    await mount()
    fireEvent.change(domainInput(), { target: { value: 'vector' } })
    expect(document.body.textContent).not.toContain(baseZh['brand.domainInvalid'])
    fireEvent.click(saveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0].domain).toBe('vector')
    expect(Toast.err).not.toHaveBeenCalled()
  })

  it('③b 大写写法被判非法（新写入必须落库为合规前缀，读侧才认得）', async () => {
    await mount()
    fireEvent.change(domainInput(), { target: { value: 'AUTOBOT2' } })
    await vi.waitFor(() => expect(hintLines()).toContain(baseZh['brand.domainInvalid']))
    fireEvent.click(saveButton())
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length).toBe(0)
  })

  it('④ 反向对照 B：存量不合规值整表回提不拦（后端放行、前端同尺子）', async () => {
    S.initDomain = 'legacy-test' // 历史入库值（F-76 之前的口径），读侧照旧生效
    await mount()
    expect(document.body.textContent).not.toContain(baseZh['brand.domainInvalid'])
    // 只改品牌名，domain 原样带回（后台是整表回提）
    fireEvent.click(saveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0].domain, '存量原值不得被前端改写').toBe('legacy-test')
    expect(S.saves[0].brand_name).toBe('极光汽车')
    // 一旦真改动就立刻回到新规则：改成另一个不合规值即被拦
    fireEvent.change(domainInput(), { target: { value: 'legacy_2' } })
    await vi.waitFor(() => expect(document.body.textContent).toContain(baseZh['brand.domainInvalid']))
    S.saves.length = 0
    fireEvent.click(saveButton())
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length, '豁免只放行"没动过"').toBe(0)
  })

  it('⑤ 清空＝解绑，允许提交（取消品牌域这条正常路径不能被预检堵死）', async () => {
    await mount()
    fireEvent.change(domainInput(), { target: { value: '' } })
    expect(document.body.textContent).not.toContain(baseZh['brand.domainInvalid'])
    fireEvent.click(saveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0].domain).toBe('')
  })
})
