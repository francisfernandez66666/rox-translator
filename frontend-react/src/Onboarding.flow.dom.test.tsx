// @vitest-environment jsdom
// ============================================================================
// Onboarding.flow.dom.test.tsx — 引导流"每一步真的写进了后端"的判据（O 组；H1 改挂对话式外壳后重写）
//
// 为什么不能只靠 onboardingSchema.lock.test.ts 那张静态表：静态守卫证明的是
// "配置指向的口存在"，证明不了"这一步走完确实发出过一次写请求、且失败时不假装成功"。
// 而引导流最坏的形态恰恰是：走完四步、屏幕上像一切都好，库里一个产品都没有
// ⇒ 横幅下次登录继续弹（§三「有脚本无调度要问到第二层」在引导层的同形）。
//
// ★ H1（2026-11-06）改形后的两条新判据（这两条是这次改形的本体，不是顺带）：
//   · 引导页必须**复用 guidedchat 那台外壳**（进度/气泡/上一步都在外壳里），
//     所以这里按外壳的真锚点取读数：进度读 `.ar-prog-label`、输入块读 `.ar-form`，
//     **禁止**再按旧版那页朴素表单的 `data-testid="onboarding"` 取点（锚点搬家会结构性找不到目标）。
//   · 模型整理腿**只出候选、不落库**：⑦a 判"候选进了表单而 productSave 一次都没被调"，
//     ⑦b 判"空候选只出声、不挡下一步"（挡住向导＝把一次上游抖动变成客户走不完的第一公里）。
//
// 七条判据：
//   ① 打开就是第一步（公司与行业），且不写任何库
//   ② 必填没填 ⇒ 保存请求一条都不发（防"空名字建档"在库里长出垃圾行）
//   ③ 填了名字 ⇒ productSave 带上这一步采集的三个字段
//   ④ 语种那一步 ⇒ 交**数组** target_langs，且**整份档案**一起交（带 id 的 save 不是 PATCH）
//   ⑤ 后端回 success:false ⇒ 出错误文案、**不许前进到下一步**（防"写失败也当走完"）
//   ⑥ 最后一步把 SEO 开关交给唯一写点 seoSet
//   ⑦ 模型整理腿：候选填进表单但不落库／空候选出声但不挡路
// 运行：npx vitest run src/Onboarding.flow.dom.test.tsx
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  productList: vi.fn(),
  productSave: vi.fn(),
  seoSet: vi.fn(),
  onboardDraft: vi.fn(),
}))

vi.mock('@/api/product', () => ({
  productList: api.productList,
  productSave: api.productSave,
  onboardDraft: api.onboardDraft,
  productStatus: vi.fn(async () => ({ success: true })),
  productDelete: vi.fn(),
  factList: vi.fn(async () => ({ success: true, facts: [] })),
  factConfirm: vi.fn(),
  factWithdraw: vi.fn(),
  assetList: vi.fn(async () => ({ success: true, assets: [] })),
  kbPhrases: vi.fn(async () => ({ success: true, phrases: [] })),
  seoGet: vi.fn(async () => ({ success: true, seo_indexable: true })),
  seoSet: api.seoSet,
}))

const nav = vi.hoisted(() => ({ navigate: vi.fn(), pathname: '/onboarding', search: '' }))
vi.mock('@/lib/nav', () => ({ useNav: () => nav }))

import Onboarding from './components/Onboarding'

beforeEach(() => {
  // 桩 matchMedia → 命中 prefers-reduced-motion：气泡文案直出终态，不等打字机逐字
  //（与 AiRegisterFlow.dom.test.tsx 同一套夹具口径——外壳的动效档位只有这一个开关）
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (q: string) => ({
      matches: String(q).includes('reduce'), media: String(q), onchange: null,
      addEventListener: () => {}, removeEventListener: () => {},
    }),
  })
  vi.clearAllMocks()
  api.productList.mockResolvedValue({ success: true, products: [] })
  api.productSave.mockResolvedValue({ success: true, id: 'prd_new' })
  api.seoSet.mockResolvedValue({ success: true, seo_indexable: true })
  api.onboardDraft.mockResolvedValue({ success: true, draft: {} })
})

afterEach(() => cleanup())

/** 外壳的进度读数：步骤名与第几步都在 `.ar-prog-label` 那一行（旧版那页的 onboard-progress 已随改形退役） */
async function expectStep(idx: number, total = 4) {
  await waitFor(() => {
    const el = document.querySelector('.ar-prog-label')
    expect(el?.textContent || '').toContain(`${idx}`)
    expect(el?.textContent || '').toContain(`${total}`)
  })
}

describe('引导流写侧接线（O 组 ＋ H1 外壳）', () => {
  it('① 打开就是第一步（公司与行业），且不写任何库', async () => {
    render(<Onboarding />)
    await expectStep(1)
    expect(api.productSave).not.toHaveBeenCalled()
    // 外壳的骨架必须在：进度条与聊天流都归外壳，不在本页各画一份
    expect(document.querySelector('.ar-track')).toBeTruthy()
    expect(document.querySelector('.ar-chat')).toBeTruthy()
  })

  it('② 产品名空着 ⇒ 下一步禁用，保存请求一条都不发', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next')) // 第 1 步（skip）可以走
    await expectStep(2)
    expect(api.productSave).not.toHaveBeenCalled()
    expect((screen.getByTestId('onboard-next') as HTMLButtonElement).disabled).toBe(true)
  })

  it('③ 填了名字 ⇒ productSave 带上这一步采集的三个字段', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await expectStep(2)
    fireEvent.change(screen.getByTestId('onboard-field-name'), { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.productSave).toHaveBeenCalledTimes(1))
    const arg = api.productSave.mock.calls[0][0]
    expect(arg.name).toBe('整机 X1')
    expect('tagline' in arg).toBe(true)
    expect('use_case' in arg).toBe(true)
  })

  it('④ 语种那一步交的是数组 target_langs，并且整份档案一起交（带 id 的 save 不是 PATCH）', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await expectStep(2)
    fireEvent.change(screen.getByTestId('onboard-field-name'), { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next')) // → 第 3 步（语种）
    await expectStep(3)
    const chips = screen.getByTestId('onboard-langs').querySelectorAll('button')
    fireEvent.click(chips[0])
    fireEvent.click(chips[1])
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.productSave).toHaveBeenCalledTimes(2))
    const arg = api.productSave.mock.calls[1][0]
    expect(Array.isArray(arg.target_langs)).toBe(true)
    expect(arg.target_langs.length).toBe(2)
    expect(arg.id).toBe('prd_new') // 语种写的是**刚建的那一份**，不是凭空新行
    // ★ 只交 `{id, target_langs}` 会被后端如实拒（「产品名不能为空」，第 3 步走不完）；
    //   而顺着"省略＝不表态"把 name 放开，第 2 步的定位/用途会被这一枪静默洗成空。⇒ 整份交。
    expect(arg.name).toBe('整机 X1')
    expect(Object.prototype.hasOwnProperty.call(arg, 'tagline')).toBe(true)
    expect(Object.prototype.hasOwnProperty.call(arg, 'use_case')).toBe(true)
  })

  it('⑤ 后端回 success:false ⇒ 出错误文案且不前进（写失败不许被读成"引导已走完"）', async () => {
    api.productSave.mockResolvedValue({ success: false, message: '产品名不能为空' })
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await expectStep(2)
    fireEvent.change(screen.getByTestId('onboard-field-name'), { target: { value: '  ' } })
    // 全空格的必填名：按钮该直接禁用（判据在 schema 层），不发请求
    expect((screen.getByTestId('onboard-next') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByTestId('onboard-field-name'), { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(screen.getByTestId('onboard-error')).toBeTruthy())
    expect(screen.getByTestId('onboard-error').textContent).toContain('产品名不能为空')
    await expectStep(2) // 仍停在第 2 步
  })

  it('⑥ 最后一步把 SEO 开关交给唯一写点 seoSet（出厂默认开，勾着就是 true）', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await expectStep(2)
    fireEvent.change(screen.getByTestId('onboard-field-name'), { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    // 每点一次下一步都是"先 await 写口、再切步"，所以必须逐次等进度——
    // 连着点两下只会点中同一步两次（写口发两次、进度还停在 3/4），那是测试自己的错，不是产品的错
    await expectStep(3)
    fireEvent.click(screen.getByTestId('onboard-next')) // 语种（未勾＝显式空数组）→ 第 4 步
    await expectStep(4)
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.seoSet).toHaveBeenCalledWith('prd_new', true))
  })

  it('⑦ 模型整理腿：候选填进表单，但**一次都不落库**（确认权在用户手里）', async () => {
    api.onboardDraft.mockResolvedValue({
      success: true,
      draft: { name: '整机 X1', tagline: '便携储能', use_case: '户外与应急', target_langs: ['en', 'xx-not-a-lang'] },
    })
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await expectStep(2)
    fireEvent.change(screen.getByTestId('onboard-draft-text'), { target: { value: '一台 1000W 的便携储能电源，主打户外露营。' } })
    fireEvent.click(screen.getByTestId('onboard-draft'))
    await waitFor(() => expect(api.onboardDraft).toHaveBeenCalledTimes(1))
    // 候选进了表单：名字与定位必须现读得到
    await waitFor(() => expect((screen.getByTestId('onboard-field-name') as HTMLInputElement).value).toBe('整机 X1'))
    expect((screen.getByTestId('onboard-field-tagline') as HTMLInputElement).value).toBe('便携储能')
    // ★ 但这一枪**不写库**：productSave 一次都没被调（写只由「下一步」触发）
    expect(api.productSave).not.toHaveBeenCalled()
    // 语种候选要走到第 3 步才看得见：模型造出来的不认识的码必须被丢掉（界面不替客户决定对外说法）
    fireEvent.click(screen.getByTestId('onboard-next'))
    await expectStep(3)
    const picked = screen.getByTestId('onboard-langs').querySelectorAll('.ar-opt--picked')
    expect(picked.length).toBe(1)
  })

  it('⑦b 整理腿空手而归 ⇒ 出声一句"自己填也一样"，且**不挡下一步**', async () => {
    api.onboardDraft.mockResolvedValue({ success: true, draft: {} })
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await expectStep(2)
    fireEvent.change(screen.getByTestId('onboard-draft-text'), { target: { value: '随便一段没有可用字段的描述' } })
    fireEvent.click(screen.getByTestId('onboard-draft'))
    await waitFor(() => expect(screen.getByTestId('onboard-draft-note')).toBeTruthy())
    // 仍然可以手填并前进（把上游抖动写成"走不完的第一公里"是最坏的降级方向）
    fireEvent.change(screen.getByTestId('onboard-field-name'), { target: { value: '整机 X2' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.productSave).toHaveBeenCalledTimes(1))
    await expectStep(3)
  })
})
