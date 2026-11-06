// @vitest-environment jsdom
// ============================================================================
// ProductDetail.write.dom.test.tsx — 一期 X1「卖家写侧单页详情」判据（2026-11-06）
//
// 这一页存在的理由就是"客户自己能备出物料"，所以判据全部问**写侧动作真发出去了什么**，
// 不问界面好不好看：
//   ① 一个产品都没有时必须有"建第一份"那一颗钮（旧形态三张面板全是读，建不了档＝商品第一件产不出）；
//   ② factUpsert／assetPut 的入参**必须显式带 visibility 与 source**（后端 update 语义是
//      "省略＝没表态"，界面把档位显示给人看了却不上送＝只改一格就把该行静默打回 gated，AGENTS §29）；
//   ③ 删除的确认框**只弹一次**且带 danger（口径同 doubleConfirmGate：外层再弹一次＝用户点两次"确定"）；
//   ④ 文件下载那颗钮只在真有 artifact_ref 时渲染（能力缺失不摆一颗必失败的钮，§一·17③ 同族），
//      且无附件那一档必须如实显示后端给的那句话，而不是静默；
//   ⑤ live 状态不再显示"对外发布"（同屏两个入口指向同一个动作迟早各说一套）；
//   ⑥ 批量说法只有一种入参形状 `{phrases:[…]}`（§29 那条"前端送数组、后端解单条 ⇒ 报成功零落行"）。
// 运行：npx vitest run src/ProductDetail.write.dom.test.tsx
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  productList: vi.fn(),
  productSave: vi.fn(),
  productDelete: vi.fn(),
  productStatus: vi.fn(),
  seoGet: vi.fn(),
  seoSet: vi.fn(),
  factList: vi.fn(),
  factUpsert: vi.fn(),
  assetList: vi.fn(),
  assetPut: vi.fn(),
  assetReview: vi.fn(),
  assetDownload: vi.fn(),
  assetUploadFile: vi.fn(),
  assetFileDownload: vi.fn(),
  kbPhrases: vi.fn(),
  kbPhraseSave: vi.fn(),
}))

vi.mock('@/api/product', () => ({
  productList: api.productList,
  productSave: api.productSave,
  productDelete: api.productDelete,
  productStatus: api.productStatus,
  seoGet: api.seoGet,
  seoSet: api.seoSet,
  factList: api.factList,
  factUpsert: api.factUpsert,
  assetList: api.assetList,
  assetPut: api.assetPut,
  assetReview: api.assetReview,
  assetDownload: api.assetDownload,
  assetUploadFile: api.assetUploadFile,
  assetFileDownload: api.assetFileDownload,
  kbPhrases: api.kbPhrases,
  kbPhraseSave: api.kbPhraseSave,
}))

// 导航与确认框都替掉：本测试判的是"发了什么请求、弹了几次"，不是路由与真对话框
const nav = vi.hoisted(() => ({ navigate: vi.fn(), pathname: '/products/detail', search: '?id=p1' }))
vi.mock('@/lib/nav', () => ({ useNav: () => nav }))

const confirmDialog = vi.hoisted(() => vi.fn(async (_o: { header?: string; body?: string; danger?: boolean }) => true))
vi.mock('@/components/uiDialogs', () => ({ confirmDialog }))

import ProductDetail from './components/ProductDetail'

/** 一行物料夹具（带附件与不带附件各一，正好是 ④ 的两档对照） */
const ASSETS = [
  { id: 11, asset_type: 'onepager', lang: 'en', body: 'hello', review_status: 'approved', validity: 'valid', visibility: 'gated', artifact_ref: 'kbmerged_a.pdf' },
  { id: 12, asset_type: 'tagline', lang: 'en', body: 'pitch', review_status: 'unreviewed', validity: 'valid', visibility: 'public', artifact_ref: '' },
]

beforeEach(() => {
  vi.clearAllMocks()
  api.productList.mockResolvedValue({ success: true, products: [{ id: 'p1', name: '整机', tenant_code: 'tc1', page_code: 'pc1', status: 'draft' }] })
  api.factList.mockResolvedValue({ success: true, facts: [] })
  api.assetList.mockResolvedValue({ success: true, assets: ASSETS })
  api.kbPhrases.mockResolvedValue({ success: true, phrases: [] })
  api.seoGet.mockResolvedValue({ success: true, seo_indexable: true, url: '/p/tc1/pc1' })
  api.productSave.mockResolvedValue({ success: true, id: 'p1' })
  api.productStatus.mockResolvedValue({ success: true })
  api.productDelete.mockResolvedValue({ success: true })
  api.seoSet.mockResolvedValue({ success: true, seo_indexable: false })
  api.factUpsert.mockResolvedValue({ success: true, id: 31 })
  api.assetPut.mockResolvedValue({ success: true, id: 41 })
  api.assetReview.mockResolvedValue({ success: true })
  api.assetDownload.mockResolvedValue({ success: false, message: '这条物料引用的事实改过了' })
  api.assetUploadFile.mockResolvedValue({ success: true, artifact_ref: 'newfile.pdf' })
  api.assetFileDownload.mockResolvedValue('newfile.pdf')
  api.kbPhraseSave.mockResolvedValue({ success: true, imported: 1 })
})

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('卖家写侧单页详情（X1）', () => {
  it('① 没有任何产品时给出"建第一份"入口，而不是三块空面板', async () => {
    api.productList.mockResolvedValue({ success: true, products: [] })
    api.assetList.mockResolvedValue({ success: true, assets: [] })
    render(<ProductDetail />)
    const empty = await screen.findByTestId('pd-empty')
    expect(empty).toBeTruthy()
    // 名称空着点保存必须只是出声，不发建档请求（否则一次误点就在库里多一个空产品）
    fireEvent.click(screen.getByTestId('pd-save'))
    await waitFor(() => expect(screen.getByTestId('pd-notice')).toBeTruthy())
    expect(api.productSave).not.toHaveBeenCalled()
    fireEvent.change(screen.getByTestId('pd-name'), { target: { value: '整机 X1' } })
    fireEvent.click(screen.getByTestId('pd-save'))
    await waitFor(() => expect(api.productSave).toHaveBeenCalled())
  })

  it('② 事实与物料的上送入参显式带 visibility（与 source），界面档位＝交回去的那一份', async () => {
    render(<ProductDetail />)
    await screen.findByTestId('pd-basic')
    fireEvent.change(screen.getByPlaceholderText('项目名（如 产能 / 起订量）'), { target: { value: '产能' } })
    fireEvent.change(screen.getByPlaceholderText('内容'), { target: { value: '1200台/月' } })
    fireEvent.click(screen.getByTestId('pd-fact-save'))
    await waitFor(() => expect(api.factUpsert).toHaveBeenCalled())
    const factArg = api.factUpsert.mock.calls[0][0]
    expect(factArg.visibility).toBe('gated')
    expect(factArg.source).toBe('manual')

    fireEvent.change(screen.getByTestId('pd-asset-body'), { target: { value: '一份单页介绍' } })
    fireEvent.click(screen.getByTestId('pd-asset-save'))
    await waitFor(() => expect(api.assetPut).toHaveBeenCalled())
    const assetArg = api.assetPut.mock.calls[0][0]
    expect(assetArg.visibility).toBe('gated')
    expect(assetArg.body).toBe('一份单页介绍')
  })

  it('③ 删除只弹一次确认框，且带 danger', async () => {
    render(<ProductDetail />)
    await screen.findByTestId('pd-basic')
    fireEvent.click(screen.getByTestId('pd-delete'))
    await waitFor(() => expect(api.productDelete).toHaveBeenCalledWith('p1'))
    expect(confirmDialog).toHaveBeenCalledTimes(1)
    expect(confirmDialog.mock.calls[0][0]).toMatchObject({ danger: true })
  })

  it('④ 下载文件那颗钮只挂在真有附件的行上；无附件那一档走正文口并如实显示后端的话', async () => {
    render(<ProductDetail />)
    await screen.findByTestId('pd-assets')
    expect(screen.getAllByTestId('pd-asset-file-get').length).toBe(1)
    fireEvent.click(screen.getByTestId('pd-asset-file-get'))
    await waitFor(() => expect(api.assetFileDownload).toHaveBeenCalledWith(11))

    // 无附件行：点"看正文"打到 JSON 口，后端那句"引用已变更"必须出现在屏上（不许静默）
    // 行选择器必须按**行自己的 testid 形状**取（`/pd-asset-/` 会把 pd-asset-save／-body／-file-get
    // 三颗按钮一起算进来 ⇒ 数到 6 行，把"两行对照"判成实现错）
    const rows = screen.getAllByTestId(/^pd-asset-(approved|unreviewed|rejected)-(valid|expired)$/)
    expect(rows.length).toBe(2)
    const bodyBtns = screen.getAllByText('看正文')
    fireEvent.click(bodyBtns[1])
    await waitFor(() => expect(api.assetDownload).toHaveBeenCalledWith(12))
    const notice = await screen.findByTestId('pd-notice')
    expect(notice.textContent).toContain('这条物料引用的事实改过了')
  })

  it('⑤ 已经是 live 的行不再提供第二颗"对外发布"', async () => {
    api.productList.mockResolvedValue({ success: true, products: [{ id: 'p1', name: '整机', tenant_code: 'tc1', page_code: 'pc1', status: 'live' }] })
    render(<ProductDetail />)
    await screen.findByTestId('pd-status-actions')
    expect(screen.queryByTestId('pd-to-live')).toBeNull()
  })

  it('⑥ 已批准说法只有一种入参形状（数组，product_id 单独交）', async () => {
    render(<ProductDetail />)
    await screen.findByTestId('pd-phrases')
    fireEvent.change(screen.getByPlaceholderText('原说法'), { target: { value: '整机质保' } })
    fireEvent.change(screen.getByPlaceholderText('规定译法'), { target: { value: 'Whole-machine warranty' } })
    fireEvent.click(screen.getByTestId('pd-phrase-save'))
    await waitFor(() => expect(api.kbPhraseSave).toHaveBeenCalled())
    const [pid, rows] = api.kbPhraseSave.mock.calls[0]
    expect(pid).toBe('p1')
    expect(Array.isArray(rows)).toBe(true)
    expect(rows[0]).toMatchObject({ source_text: '整机质保', target_text: 'Whole-machine warranty', target_lang: 'en' })
  })

  it('⑦ SEO 开关的唯一写点就是 seoSet（改名／live 翻转不许由本页重新打开，§5.8 S4）', async () => {
    render(<ProductDetail />)
    await screen.findByTestId('pd-seo')
    fireEvent.click(screen.getByTestId('pd-seo').querySelector('input') as HTMLInputElement)
    await waitFor(() => expect(api.seoSet).toHaveBeenCalledWith('p1', false))
  })
})
