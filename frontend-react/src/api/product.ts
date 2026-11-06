// ============================================================================
// api/product.ts — 一期「确定的多语言能力（知识层商品）」接口层（施工_一期 §5.1 / §8.1）
//
// ★ 失败收敛纪律：本文件**每个**返回 `{success,...}` 信封的调用一律
//   `return bizResp(() => request(...))`——后端 F-64① 之后失败走结构化 4xx，
//   绕开 bizResp 直返裸 request 会让对应面板对着抛出的 ApiError 白屏。
//   新文件走 `src/api/bizRespGate.test.ts` 的**主判据**（零裸返点位），
//   **不进**任何豁免档（EXEMPT／READ_THROW_EXEMPT／LEGACY_UNWRAPPED 全不登记）。
//   定向锁见 `src/api/product.bizResp.test.ts`。
//
// 接口契约（§5.1，后端并行施工）：
//   /api/product/list|save|delete|status、fact/list|upsert|confirm|withdraw、
//   asset/list|put|review|download、readiness、kb/phrases|save、answer、seo，
//   公开页 /p/{tenantCode}/{pageCode}。
// ★ 对外页与知识问答是**免登录可达**面：不返 403、受控字段整段不进匿名响应（§5.6 V4）。
// ============================================================================
import { apiMsg, bizResp, request, authHeaders, type AdminResp } from './core'

/** 产品档案（两段短码 tenant_code/page_code 创建即定、改名不改 URL，§2.1 H1） */
export interface Product {
  id: string
  name: string
  tagline: string
  status: string
  tenant_code: string
  page_code: string
  seo_indexable: number | boolean
  [key: string]: unknown
}

/** 产品事实项（kind 九值名单由 i18n/productKindCoverage.test.ts 守；cert 缺省受控） */
export interface ProductFact {
  id: number
  kind: string
  key: string
  value: string
  status: string
  visibility: string
  source: string
  [key: string]: unknown
}

/** 物料货架行（validity 三态：valid／expired／missing 不落行，§2.3） */
export interface ProductAsset {
  id: number
  asset_type: string
  lang: string
  body: string
  review_status: string
  validity: string
  visibility: string
  [key: string]: unknown
}

/** 齐备度单格（六格 × 12 语种现算，worst 为最差态，§2.5） */
export interface ReadinessCell {
  asset_type: string
  lang: string
  count: number
  worst: string
  [key: string]: unknown
}

/** 产品列表出参：空集由后端归一成 []，不出 null */
export interface ProductListResp extends AdminResp {
  products: Product[]
}

/** 事实列表出参 */
export interface FactListResp extends AdminResp {
  facts: ProductFact[]
}

/** 物料列表出参 */
export interface AssetListResp extends AdminResp {
  assets: ProductAsset[]
}

/** 齐备度出参（空集出 []，不出 null） */
export interface ReadinessResp extends AdminResp {
  readiness: ReadinessCell[]
}

/** 已批准说法（薄委托 kb_entries）出参 */
export interface PhrasesResp extends AdminResp {
  phrases: Array<{ id: number; source_text: string; target_text: string; [key: string]: unknown }>
}

/** 知识层问答出参（零 LLM 调用；answer 来源标 knowledge_base，§7） */
export interface AnswerResp extends AdminResp {
  answer: string
  source: string
  missing: string[]
}

/** SEO 索引开关出参 */
export interface SeoResp extends AdminResp {
  seo_indexable: boolean
}

/** 对外页匿名出参里的公开条目（＝后端 `PublicCorpusItem` 的形状：文本在 `text`，不在 `value`）
 *  ★ 与登录态的 `ProductFact` **不是同一个形状**：那一份带 status/visibility 等治理字段，
 *    这一份是"已经过 SQL 闸门的对外文本"。把它们混成一个类型，页面就会去读不存在的 `value`
 *    ——渲染出来是"键在、值空"，比报错更难发现。 */
export interface PublicItem {
  id: number
  /** fact | asset：来源档（事实项／已备物料） */
  source: string
  kind: string
  key: string
  lang: string
  text: string
  /**
   * 这个口**永不**回 gated 行（SQL 硬带 visibility='public'，§5.6 V3），所以正常拿不到这个字段；
   * 声明成可选是为了让前端那一侧也留一道同名的显式过滤——源码级锁 productSeo.dom.test.ts S2
   * 判的就是"过滤动作写在渲染前"，而不是只信后端。
   */
  visibility?: string
}

/** 对外页匿名出参（gated 字段的值字面量整段缺席，§5.6 V4；`gated_count` 只出条数不出值） */
export interface PublicProductResp extends AdminResp {
  product: Product | null
  facts: PublicItem[]
  /** 受控条目条数（替身文案的事实依据；值字面量永不出栈） */
  gated_count?: number
  not_found: boolean
}

/** 通用写操作出参（save/delete/status/upsert/confirm/withdraw/put/review） */
export interface ProductActionResp extends AdminResp {
  id?: string | number
}

/** 构造带查询串的 GET 路径（只拼 ASCII 键与已编码值，避免注入） */
function withQuery(path: string, params: Record<string, string | number>): string {
  const q = Object.keys(params)
    .map((k) => `${encodeURIComponent(k)}=${encodeURIComponent(String(params[k]))}`)
    .join('&')
  return q ? `${path}?${q}` : path
}

/** JSON POST 请求体（统一 Content-Type 与认证头） */
function postBody(data: unknown): RequestInit {
  return { method: 'POST', headers: { ...authHeaders(), 'Content-Type': 'application/json' }, body: JSON.stringify(data) }
}

/** 产品列表（登录用户，一人多产品） */
export async function productList(): Promise<ProductListResp> {
  return bizResp(() => request('/api/product/list', { headers: authHeaders() }))
}

/** 产品建档／改档（写侧先落库再碰进程侧；不传短码时由后端签发） */
export async function productSave(data: Partial<Product>): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/save', postBody(data)))
}

/** 删除产品档案 */
export async function productDelete(id: string): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/delete', postBody({ id })))
}

/** 切换产品状态（draft／live／archived 三值，禁第四值） */
export async function productStatus(id: string, status: string): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/status', postBody({ id, status })))
}

/** 事实项列表 */
export async function factList(productId: string): Promise<FactListResp> {
  return bizResp(() => request(withQuery('/api/product/fact/list', { product_id: productId }), { headers: authHeaders() }))
}

/** 事实项新增／更新 */
export async function factUpsert(data: Partial<ProductFact> & { product_id: string }): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/fact/upsert', postBody(data)))
}

/** 确认事实（仅 source=manual 可执行；cert 走 P4 硬拒） */
export async function factConfirm(id: number): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/fact/confirm', postBody({ id })))
}

/** 撤回事实（引用它的物料翻 expired） */
export async function factWithdraw(id: number): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/fact/withdraw', postBody({ id })))
}

/** 物料列表 */
export async function assetList(productId: string): Promise<AssetListResp> {
  return bizResp(() => request(withQuery('/api/product/asset/list', { product_id: productId }), { headers: authHeaders() }))
}

/** 物料写入／更新 */
export async function assetPut(data: Partial<ProductAsset> & { product_id: string }): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/asset/put', postBody(data)))
}

/** 物料审核（unreviewed／approved／rejected） */
export async function assetReview(id: number, status: string): Promise<ProductActionResp> {
  return bizResp(() => request('/api/product/asset/review', postBody({ id, status })))
}

/** 物料下载（后端必须拦 validity=expired，返回可行动文案，不静默） */
export async function assetDownload(id: number): Promise<ProductActionResp> {
  return bizResp(() => request(withQuery('/api/product/asset/download', { id }), { headers: authHeaders() }))
}

/** 齐备度现算（六格 × 12 语种；SQL 必须带 product_id 前缀等值条件，§2.5 R5） */
export async function productReadiness(productId: string): Promise<ReadinessResp> {
  return bizResp(() => request(withQuery('/api/product/readiness', { product_id: productId }), { headers: authHeaders() }))
}

/** 已批准说法列表（薄委托 kb_entries，不另开口径） */
export async function kbPhrases(productId: string): Promise<PhrasesResp> {
  return bizResp(() => request(withQuery('/api/product/kb/phrases', { product_id: productId }), { headers: authHeaders() }))
}

/** 已批准说法（一条）——批量导入就是把它按序打 N 次，键名与后端 handleProductKBPhrases 的 POST 体同源 */
export interface KbPhrase {
  source_text: string
  target_text: string
  target_lang: string
  source_lang?: string
}

/**
 * 批量导入已批准说法（一次调用、一种入参形状：`{phrases:[…]}`，product_id 走 query）。
 * ★ 早期这里是"前端送数组、后端解单条"——两侧都返回成功而库里一行没落，
 *   正是 §一·8 那条「写路径 0 行影响不许回 success」在客户端侧的镜像。
 *   跨语言锁 Q1/Q4 现在按注册表逐键判，形状再分家就当场红。
 */
export async function kbPhraseSave(productId: string, phrases: KbPhrase[]): Promise<ProductActionResp & { imported?: number; failed?: number }> {
  return bizResp(() =>
    request(
      withQuery('/api/product/kb/phrases', { product_id: productId }),
      postBody({ phrases }),
    ))
}

/** 知识层问答：免登录可，带两段短码；零 LLM 调用，答案只走已确认事实 */
export async function answerProduct(tenantCode: string, pageCode: string, question: string): Promise<AnswerResp> {
  return bizResp(() => request('/api/product/answer', postBody({ tenant_code: tenantCode, page_code: pageCode, question })))
}

/**
 * 对外页匿名数据：**只走 `/api/product/public` 这一个 JSON 口**。
 * ★ 早期这里打的是 `/p/{tc}/{pc}`——那是**给人和爬虫看的 HTML 面**（由后端注入 title／
 *   JSON-LD／无 JS 首屏，见 product_ssr.go），不是数据接口；对一个返回 HTML 的路径要 JSON
 *   只会拿到整页壳 ⇒ 页面永远显示"未找到该产品页"，而单测全量 mock 掉 api 层看不见。
 *   两面各用各的口，才是"公开语料只有一个出口"（§5.6）在客户端这一侧的落点。
 */
export async function publicProduct(tenantCode: string, pageCode: string): Promise<PublicProductResp> {
  return bizResp(() =>
    request(withQuery('/api/product/public', { tenant_code: tenantCode, page_code: pageCode }), {
      headers: authHeaders(),
    }))
}

/** 读取单产品 SEO 索引开关（出厂默认开，卖家可显式关，§5.8） */
export async function seoGet(productId: string): Promise<SeoResp> {
  return bizResp(() => request(withQuery('/api/product/seo', { product_id: productId }), { headers: authHeaders() }))
}

/** 显式设置单产品 SEO 索引开关（唯一写点；改名/live 翻转不许重新打开，§5.8 S4） */
export async function seoSet(productId: string, indexable: boolean): Promise<SeoResp> {
  return bizResp(() => request('/api/product/seo', postBody({ product_id: productId, indexable })))
}

/**
 * assetUploadFile 上传一份**已备好的**物料文件（★ X2 物料文件腿，2026-11-06）。
 *
 * 只做一件事：把字节交到 `/api/product/asset/upload`，拿回后端签发的 `artifact_ref`（纯 basename）。
 * 为什么不顺手建行：行仍由 `assetPut` 写 ⇒ 同一张表只有一个写入口，不会出现
 * "上传建了半行、正文还没填"的第三种状态（§5.1 读写同源那条）。
 * ★ FormData 不预设 Content-Type：boundary 由浏览器带（口径同 chatImage／chatVoice，
 *   core.ts 的 isFormBody 那一支专门为此放行）。
 * 参数：file=待上传文件；返回 artifact_ref 与字节数。
 */
export async function assetUploadFile(file: File): Promise<ProductActionResp & { artifact_ref?: string; size_bytes?: number }> {
  const fd = new FormData()
  fd.append('file', file)
  return bizResp(() => request('/api/product/asset/upload', { method: 'POST', body: fd }))
}

/**
 * assetFileDownload 取回物料附件并触发浏览器下载（`/api/product/asset/file`）。
 *
 * ★ bizResp 正当豁免：二进制出栈——本函数拿回的是文件字节流，不是 `{success,...}` 信封，
 *   包 bizResp 会把「一个 PDF」当成信封去解 details（口径同 translate.ts 的 SSE／健康检查那一支）。
 *   这一行标记由 src/api/product.bizResp.test.ts 的「二进制档」判据现读：**只塞文件名不算豁免**，
 *   标记必须写在被豁免那个函数自己的说明里。
 *
 * 为什么走 fetch→blob 而不是 `<a href>`：那一个口先 `productActor(r)` 鉴权，**要带 Authorization**，
 * 导航式下载带不上必然 401（口径同 tickets.ts 的 editorExportFetch）。
 * ★ 出门前验完整性：`spa.go` 对不存在的路径回 200＋整页 index.html，只判状态码＝把兜底页存成物料。
 *   物料允许文档／图片／音频，格式五花八门，所以这里**不锁魔数**，只锁「不是 HTML 兜底」＋「非空」。
 * 参数：id=物料行主键；返回落盘文件名。
 */
export async function assetFileDownload(id: number): Promise<string> {
  const url = `/api/product/asset/file?id=${encodeURIComponent(String(id))}`
  const r = await fetch(url, { headers: authHeaders() })
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try {
      const j = await r.json()
      if (j && (j.message || j.error)) msg = j.message || j.error
    } catch { /* 非 JSON 错误体（网关页等）保持状态码文案 */ }
    throw new Error(msg)
  }
  const buf = await r.arrayBuffer()
  const ctype = (r.headers.get('Content-Type') || '').toLowerCase()
  if (buf.byteLength === 0 || ctype.includes('text/html')) {
    throw new Error(apiMsg('common.assetBadFile', '物料文件校验失败（取回的不是文件，下载链可能未通）'))
  }
  const disp = r.headers.get('Content-Disposition') || ''
  const m = /filename="?([^";]+)"?/.exec(disp)
  const name = m ? decodeURIComponent(m[1]) : `product-asset-${id}`
  const objUrl = URL.createObjectURL(new Blob([buf]))
  const a = document.createElement('a')
  a.href = objUrl
  a.download = name
  a.click()
  // 延迟释放：立即 revoke 会在部分浏览器取消尚未开始的下载（口径同 editorExportFetch）
  setTimeout(() => URL.revokeObjectURL(objUrl), 5000)
  return name
}
