// @vitest-environment jsdom
// ============================================================================
// Onboarding.flow.dom.test.tsx — 引导流"每一步真的写进了后端"的判据（O 组，2026-11-06）
//
// 为什么不能只靠 onboardingSchema.lock.test.ts 那张静态表：静态守卫证明的是
// "配置指向的口存在"，证明不了"这一步走完确实发出过一次写请求、且失败时不假装成功"。
// 而引导流最坏的形态恰恰是：走完四步、屏幕上像一切都好，库里一个产品都没有
// ⇒ 横幅下次登录继续弹（§三「有脚本无调度要问到第二层」在引导层的同形）。
//
// 五条判据：
//   ① 没产品不弹向导，但也不报错（横幅那半边另有 S6 判据，这里只测页面本身）
//   ② 必填没填 ⇒ 保存请求一条都不发（防"空名字建档"在库里长出垃圾行）
//   ③ 填了名字 ⇒ productSave 被调用且带 name/tagline/use_case
//   ④ 语种那一步 ⇒ 交的是**数组** target_langs（键名与后端解析同源，S3 那把静态尺子的运行时对照）
//   ⑤ 后端回 success:false ⇒ 屏上出错误文案、**不许前进到下一步**（防"写失败也当走完"）
// 运行：npx vitest run src/Onboarding.flow.dom.test.tsx
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  productList: vi.fn(),
  productSave: vi.fn(),
  seoSet: vi.fn(),
}))

vi.mock('@/api/product', () => ({
  productList: api.productList,
  productSave: api.productSave,
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
  vi.clearAllMocks()
  api.productList.mockResolvedValue({ success: true, products: [] })
  api.productSave.mockResolvedValue({ success: true, id: 'prd_new' })
  api.seoSet.mockResolvedValue({ success: true, seo_indexable: true })
})

afterEach(() => cleanup())

describe('引导流写侧接线（O 组）', () => {
  it('① 打开就是第一步（公司与行业），且不写任何库', async () => {
    render(<Onboarding />)
    expect(await screen.findByTestId('onboarding')).toBeTruthy()
    expect(screen.getByTestId('onboard-progress').textContent).toContain('1/4')
    expect(api.productSave).not.toHaveBeenCalled()
  })

  it('② 产品名空着 ⇒ 下一步禁用，保存请求一条都不发', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next')) // 第 1 步（skip）可以走
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('2/4'))
    expect(api.productSave).not.toHaveBeenCalled()
    expect((screen.getByTestId('onboard-next') as HTMLButtonElement).disabled).toBe(true)
  })

  it('③ 填了名字 ⇒ productSave 带上这一步采集的三个字段', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('2/4'))
    fireEvent.change(screen.getByTestId('onboard-field-name').querySelector('input') as HTMLInputElement, { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.productSave).toHaveBeenCalledTimes(1))
    const arg = api.productSave.mock.calls[0][0]
    expect(arg.name).toBe('整机 X1')
    expect('tagline' in arg).toBe(true)
    expect('use_case' in arg).toBe(true)
  })

  it('④ 语种那一步交的是数组 target_langs，没勾就是空数组（＝显式未设定，不是省略）', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('2/4'))
    fireEvent.change(screen.getByTestId('onboard-field-name').querySelector('input') as HTMLInputElement, { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next')) // → 第 3 步（语种）
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('3/4'))
    const boxes = screen.getByTestId('onboard-langs').querySelectorAll('input[type=checkbox]')
    fireEvent.click(boxes[0])
    fireEvent.click(boxes[1])
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.productSave).toHaveBeenCalledTimes(2))
    const arg = api.productSave.mock.calls[1][0]
    expect(Array.isArray(arg.target_langs)).toBe(true)
    expect(arg.target_langs.length).toBe(2)
    expect(arg.id).toBe('prd_new') // 语种写的是**刚建的那一份**，不是凭空新行
    // ★ 带 id 的 save 契约＝"把这份档案存回去"（不是 PATCH）⇒ 语种这一枪必须**整份交**。
    //   旧写法只交 `{id, target_langs}`：后端如实回「产品名不能为空」把第 3 步打死（现网＝引导走不完），
    //   而就算后端放过空串，第 2 步收到的名字/定位/用途也会被这一枪静默洗成空。
    //   这条腿判的是"名字与第 2 步的值还在请求里"，不是判"调用成功"——成功与否由 ⑤ 判。
    expect(arg.name).toBe('整机 X1')
    expect(Object.prototype.hasOwnProperty.call(arg, 'tagline')).toBe(true)
    expect(Object.prototype.hasOwnProperty.call(arg, 'use_case')).toBe(true)
  })

  it('⑤ 后端回 success:false ⇒ 出错误文案且不前进（写失败不许被读成"引导已走完"）', async () => {
    api.productSave.mockResolvedValue({ success: false, message: '产品名不能为空' })
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('2/4'))
    fireEvent.change(screen.getByTestId('onboard-field-name').querySelector('input') as HTMLInputElement, { target: { value: '  ' } })
    // 全空格的必填名：按钮该直接禁用（判据在 schema 层），不发请求
    expect((screen.getByTestId('onboard-next') as HTMLButtonElement).disabled).toBe(true)
    fireEvent.change(screen.getByTestId('onboard-field-name').querySelector('input') as HTMLInputElement, { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(screen.getByTestId('onboard-error')).toBeTruthy())
    expect(screen.getByTestId('onboard-error').textContent).toContain('产品名不能为空')
    expect(screen.getByTestId('onboard-progress').textContent).toContain('2/4')
  })

  it('⑥ 最后一步把 SEO 开关交给唯一写点 seoSet（出厂默认开，勾着就是 true）', async () => {
    render(<Onboarding />)
    fireEvent.click(await screen.findByTestId('onboard-next'))
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('2/4'))
    fireEvent.change(screen.getByTestId('onboard-field-name').querySelector('input') as HTMLInputElement, { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('onboard-next'))
    // 每次点下一步都是"先 await 写口、再切步"，所以这里必须逐次等进度——
    // 连着点两下只会点中同一步两次（写口发两次，进度还停在 3/4），那是测试自己的错，不是产品的错
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('3/4'))
    fireEvent.click(screen.getByTestId('onboard-next')) // 语种（未勾＝显式空数组）→ 第 4 步
    await waitFor(() => expect(screen.getByTestId('onboard-progress').textContent).toContain('4/4'))
    fireEvent.click(screen.getByTestId('onboard-next'))
    await waitFor(() => expect(api.seoSet).toHaveBeenCalledWith('prd_new', true))
  })
})
