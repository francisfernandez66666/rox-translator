// ============================================================================
// components/admin/BrandP.platformRoot.dom.test.tsx — 2026-10-23 后台报障 F1/F3 前端锁
//
// 缺陷背景（生产实证）：
//   ① 超管未选租户时 BrandP 回落 `isSuper ? 1 : 0`，平台根（租户 0）结构上够不到；
//      选择器里还有一枚 value=1 的假「租户根（平台主站）」——1 实际是 rox 普通租户。
//   ② 注册只写 brand_names/brand_name_en 不写 brand_name ⇒ 中文名框空值灰显占位符
//      「能言 LangCross」，用户把占位符当真值；保存时载荷又不带 brand_names，
//      后端整列重建 ⇒ 一次保存把 {"zh":"比亚迪","en":"BYD"} 洗成 {"en":...}。
//
// 本文件锁四向（每条都配正/反方向，防"结构必绿"）：
//   P1 超管默认目标＝0：GET 带显式 0、选择器平台项 value=0、无假根 value=1、域名框禁用；
//   P2 保存在平台根发 id=0（ROX 场景的对照：切到 #1 后 id=1）；
//   P3 中文名框用 brand_names.zh 回填真值（不再被占位符遮住）；
//   P4 保存回传 brand_names 权威全集：清空英文名＝删除 en，其它语种保留。
// 运行：npx vitest run src/components/admin/BrandP.platformRoot.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import BrandP from './BrandP'
import { baseZh } from '@/i18n/dicts.zh'

// S = 可控状态：activeTenantId 决定超管当前目标；resp 模拟后端回包；calls/saves 采集入参
const S = vi.hoisted(() => ({
  activeTenantId: 0,
  getCalls: [] as Array<number | undefined>,
  saves: [] as Array<Record<string, unknown>>,
  resp: {
    success: true,
    tenant_id: 0,
    brand_name: '',
    brand_name_en: '',
    brand_names: '{}',
    brand_logo: '',
    domain: '',
    brand_home_bg: '',
    brand_home_bg_style: '',
    brand_login_card_pos: '',
    brand_login_layout: '',
    brand_links: '[]',
    brand_paid: false,
    brand_granted: false,
    brand_root: false,
    dedicated_register: false,
  },
}))

vi.mock('@/api/branding', () => ({
  tenantBranding: vi.fn(async (id?: number) => { S.getCalls.push(id); return { ...S.resp } }),
  tenantBrandingSave: vi.fn(async (p: Record<string, unknown>) => { S.saves.push(p); return { success: true } }),
  brandGrant: vi.fn(async () => ({ success: true })),
  footerLinksGet: vi.fn(async () => ({ success: true, links: [] })),
  footerLinksSet: vi.fn(async () => ({ success: true })),
}))

vi.mock('@/lib/toastBus', () => ({ toastSuccess: vi.fn(), toastError: vi.fn() }))

vi.mock('@/stores/admin', () => ({
  // 超管（level 4 / tid 0）；租户列表含 #1 rox 与 #12 BYD——旧形态的假根选项正是 #1
  useAdmin: () => ({
    myLevel: 4,
    activeTenantId: S.activeTenantId,
    myTenantId: 0,
    tenants: [{ id: 1, name: 'rox' }, { id: 12, name: 'BYD' }],
    switchTenant: () => {},
  }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

/** 按选择器取全部节点（断言只读 DOM，不做快照，改文案不会假红） */
const $ = (sel: string) => Array.from(document.querySelectorAll(sel)) as HTMLElement[]

/** 按 placeholder 定位品牌输入框（面板里同名 lc-input 很多，不能按顺序取） */
function inputByPlaceholder(ph: string): HTMLInputElement {
  const el = $('input.lc-input').find((i) => (i as HTMLInputElement).placeholder === ph)
  if (!el) throw new Error(`未找到 placeholder=${ph} 的输入框（BrandP 结构变了？锁需随迁）`)
  return el as HTMLInputElement
}

/** 品牌保存钮（文案取词典现值，改文案即改期望） */
function saveButton(): HTMLButtonElement {
  const btn = $('button').find((b) => (b.textContent ?? '').trim() === baseZh['brand.save'])
  if (!btn) throw new Error('未找到品牌保存钮')
  return btn as HTMLButtonElement
}

/** 挂载并等到回包落进表单（只等 getCalls 会在 loaded=false 的空壳上误判） */
async function mount() {
  render(<BrandP />)
  // 等"回包已落进表单"再断言：只等 getCalls 会在 loaded 还是 false 的瞬间读到空壳
  await vi.waitFor(() => expect(saveButton()).toBeTruthy())
}

beforeEach(() => {
  cleanup()
  S.activeTenantId = 0
  S.getCalls = []
  S.saves = []
  S.resp = {
    success: true, tenant_id: 0,
    brand_name: '', brand_name_en: '', brand_names: '{}',
    brand_logo: '', domain: '', brand_home_bg: '', brand_home_bg_style: '',
    brand_login_card_pos: '', brand_login_layout: '', brand_links: '[]',
    brand_paid: false, brand_granted: false, brand_root: false, dedicated_register: false,
  }
})

describe('BrandP · 超管平台根（F1-a/F1-b）', () => {
  it('P1 未选租户＝平台根：GET 显式 0、选择器无假根、域名框禁用', async () => {
    await mount()
    // 显式 0 必须真发出去（旧形态回落到 1，或 0 被 truthy 判断吞成"不带参数"）
    expect(S.getCalls[0]).toBe(0)

    const selects = $('select.lc-select') as HTMLSelectElement[]
    const tenantSel = selects.find((s) => Array.from(s.options).some((o) => o.textContent?.includes('#12 BYD')))
    expect(tenantSel, '未找到租户选择器').toBeTruthy()
    const rootOptions = Array.from(tenantSel!.options).filter((o) => o.textContent?.trim() === baseZh['brand.tenantRoot'])
    // 反假根：文案为「租户根（平台主站）」的选项恰一枚，且 value=0
    expect(rootOptions.length).toBe(1)
    expect(rootOptions[0].value).toBe('0')
    expect(tenantSel!.value).toBe('0')
    // rox 只能是普通租户 #1，不能再冒充平台根
    const rox = Array.from(tenantSel!.options).find((o) => o.value === '1')
    expect(rox?.textContent).toContain('#1')
    expect(rox?.textContent).not.toBe(baseZh['brand.tenantRoot'])

    // 平台根没有可编辑的品牌域（与 rox/租户 1 同档禁用）
    expect(inputByPlaceholder(baseZh['brand.domainPlaceholder']).disabled).toBe(true)
  })

  it('P2 保存平台根发 id=0；切到 #1 rox 后发 id=1（正对照）', async () => {
    await mount()
    fireEvent.click(saveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0].id).toBe(0)

    // 正对照：换目标后同一按钮必须跟着换 id（否则"永远是 0"也会绿）
    cleanup()
    S.getCalls = []
    S.saves = []
    S.activeTenantId = 1
    await mount()
    fireEvent.click(saveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0].id).toBe(1)
  })
})

describe('BrandP · 三列一致与保存合并（F3-b）', () => {
  it('P3 brand_name 为空时用 brand_names.zh 回填中文名框', async () => {
    S.resp = { ...S.resp, brand_name: '', brand_name_en: 'BYD', brand_names: '{"zh":"比亚迪","en":"BYD"}' }
    await mount()
    const nameInput = inputByPlaceholder(baseZh['brand.namePlaceholder'])
    expect(nameInput.value).toBe('比亚迪')
    expect(S.resp.brand_name).toBe('') // 反证：库里该列确实为空，显示的是 brand_names 的真值
  })

  it('P4 清空英文名保存 ⇒ brand_names 删 en、保留 zh 与其它语种', async () => {
    S.resp = { ...S.resp, brand_name: '比亚迪', brand_name_en: 'BYD', brand_names: '{"zh":"比亚迪","en":"BYD","ja":"ビーワイディー"}' }
    await mount()
    const enInput = inputByPlaceholder('LangCross')
    fireEvent.change(enInput, { target: { value: '' } })
    fireEvent.click(saveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    const names = JSON.parse(String(S.saves[0].brand_names))
    expect(names.zh).toBe('比亚迪')
    expect(names.ja).toBe('ビーワイディー')
    expect('en' in names).toBe(false)
  })
})
