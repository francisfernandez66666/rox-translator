// ============ e2e/product_detail_write.spec.ts · 职责说明 ============
// 一期 X1/X2 卖家写侧单页 /products/detail（ProductDetail.tsx，2026-11-06）的**真浏览器**运行时锁。
// 与 e2e/product_shelf.spec.ts（读侧货架）同面孔：登录态、storageState 与夹具 env 全沿用那一套
// （PRODUCT_SHELF_AUTH_STATE ＋ PRODUCT_TOKEN；由 run_uat.sh 的产品 e2e 夹具段注入），
// **不自造第二种登录方式**。
//
// 四条硬口径（对应本批要钉的形态）：
//  ① 链路型用例自带**可达探针**（AGENTS §一·6）：接口腿显式带 Bearer（request fixture 不吃
//     sessionStorage），页面侧再断 pd-name 真回读到建档时那一个字面名——离线兜底会给空表单，
//     只判「界面渲染了」会一路绿灯。
//  ② 判**确认框只弹一次**按 uiDialogs 的实际形态来：confirmDialog 是自建浮层
//     （DialogHost → langcross Dialog，role=alertdialog，portal 到 body），**不是原生 dialog**，
//     所以不许 page.on('dialog')——那条事件在这里永远不触发，写了就是空锁。
//     旧缺陷形态（AGENTS §一·5 doubleConfirmGate 那族）＝点第一次「确定」时请求**还没发出去**；
//     判据因此问两件事：点一次确认后 DELETE 请求必须真的发出（waitForRequest），
//     且屏幕上不得再坐着第二个 alertdialog。
//  ③ 删完必须验**列表真刷新**：页面侧 pd-name 不再是被删那一份，服务端 /api/product/list
//     也不再含其 id（接口说成功不算验收，库侧/服务端事实才算，§一·8 同族）。
//  ④ 本文件**只删自己建的两份产品**（A 用来走全流程并删除，B 用来证明「删 A 后列表还有 B、
//     页面没被删空」），收尾 best-effort 摘干净——不许碰夹具产品（货架那条 spec 依赖它非空）。
//
// ⚠️ 需要真后端 ＋ 登录态 storageState ＋ PRODUCT_TOKEN 才能跑；未纳入 vitest，
//   也不在 tsc 的 include 内（与 e2e/product_shelf.spec.ts 同形态）。
// ==============================================================
import { expect, test } from '@playwright/test'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const AUTH_STATE = process.env.PRODUCT_SHELF_AUTH_STATE || ''
const TOKEN = process.env.PRODUCT_TOKEN || ''
const DETAIL_URL = process.env.PRODUCT_DETAIL_URL || '/products/detail'

/** 假 PDF：首字节 %PDF-、几 KB；后端上传口只判扩展名与体积，不解析内容 */
const PDF_NAME = 'e2e-asset-uat.pdf'

test.describe('卖家写侧 /products/detail（X1/X2 写链路＋一次确认＋列表刷新）', () => {
  test.skip(!AUTH_STATE || !TOKEN, '需 PRODUCT_SHELF_AUTH_STATE ＋ PRODUCT_TOKEN 才能打登录态写侧页')
  test.use({ storageState: AUTH_STATE })

  const stamp = Date.now()
  const nameA = `E2E-DETAIL-A-${stamp}`
  const nameB = `E2E-DETAIL-B-${stamp}`
  const factKey = `e2e-key-${stamp}`
  const factValue = `E2E-FACT-${stamp}`
  const assetBody = `E2E-ASSET-BODY-${stamp}`
  const createdIds: string[] = []

  const authHeaders = { Authorization: `Bearer ${TOKEN}` }

  /** 经 API 建一份产品并记下 id（页面只负责把这份的写链路走完；afterEach 兜底清场） */
  async function createProduct(request: import('@playwright/test').APIRequestContext, name: string): Promise<string> {
    const r = await request.post('/api/product/save', {
      headers: { ...authHeaders, 'Content-Type': 'application/json' },
      data: { name, tagline: 'e2e 写侧夹具' },
    })
    expect(r.ok(), `建档接口不可达（链路没通：POST /api/product/save 非 2xx）`).toBeTruthy()
    const body = await r.json()
    expect(body && body.success === true, `建档未返回 success：${JSON.stringify(body).slice(0, 200)}`).toBeTruthy()
    const id = String(body.id || (body.product && body.product.id) || '')
    expect(id, '建档出栈取不到 id（两短码契约裂了，页面腿无从谈起）').not.toBe('')
    createdIds.push(id)
    return id
  }

  test.afterEach(async ({ request }) => {
    // 收尾 best-effort：测试中途红也不能把 E2E 产品留在库里污染货架计数
    for (const id of createdIds.splice(0)) {
      await request.post('/api/product/delete', {
        headers: { ...authHeaders, 'Content-Type': 'application/json' },
        data: { id },
      }).catch(() => null)
    }
  })

  test('建档→事实→物料文件→live→确认框只弹一次→删后列表真没有', async ({ page, request }) => {
    test.setTimeout(120000)

    // ④ 可达探针（接口腿）：登录态接口必须显式带 Bearer（request fixture 不吃 storageState/sessionStorage）
    const probe = await request.get('/api/product/list', { headers: authHeaders })
    expect(probe.ok(), '链路没通：/api/product/list 不可达（不许拿兜底空态当通过）').toBeTruthy()
    const probeBody = await probe.json()
    expect(probeBody && probeBody.success === true, '链路探针：货架接口未返回 success').toBeTruthy()

    const idA = await createProduct(request, nameA)
    const idB = await createProduct(request, nameB)

    // 假 PDF 落临时目录（文件名带扩展名，multipart 直接用它）
    const dir = mkdtempSync(join(tmpdir(), 'e2e-pdf-'))
    const pdfPath = join(dir, PDF_NAME)
    writeFileSync(pdfPath, `%PDF-1.4\n${'x'.repeat(4096)}\n`)

    // 登录态显式写进 sessionStorage：入口分诊在 main.tsx 模块初始化时读一次 getAuthToken()，
    // 晚一帧就按未登录走公开壳（product_shelf.spec.ts 同段实测教训，原样沿用）。
    await page.addInitScript((t) => {
      try { sessionStorage.setItem('auth_token', t) } catch { /* 站点数据被拒＝未登录起步，下面的判据自己会红 */ }
    }, TOKEN)

    await page.goto(`${DETAIL_URL}?id=${encodeURIComponent(idA)}`)

    // ① 可达探针（页面腿）：pd-basic 出现 **且** pd-name 回读到建档时那一个字面名。
    //    离线兜底态下表单是空的（loadProducts 失败 ⇒ list=[] ⇒ current=null），这条会红而不是绿灯。
    await expect(page.getByTestId('pd-basic')).toBeVisible()
    await expect(page.getByTestId('pd-name')).toHaveValue(nameA)

    // ② 事实录入：pd-facts 里两个裸 input（键/值），保存后应出现一行 unverified 状态的家养事实
    await page.locator('[data-testid="pd-facts"] input').nth(0).fill(factKey)
    await page.locator('[data-testid="pd-facts"] input').nth(1).fill(factValue)
    await page.getByTestId('pd-fact-save').click()
    await expect(page.getByTestId('pd-fact-unverified')).toHaveCount(1)
    await expect(page.getByTestId('pd-facts').getByText(factValue)).toBeVisible()

    // ③ 物料文件腿：选文件即上传（pd-asset-ref 显示的是**原文件名**），再存正文 ⇒ 货架多出一行
    await page.getByTestId('pd-asset-file').setInputFiles(pdfPath)
    await expect(page.getByTestId('pd-asset-ref')).toContainText(PDF_NAME)
    await page.getByTestId('pd-asset-body').fill(assetBody)
    await page.getByTestId('pd-asset-save').click()
    await expect(page.locator('[data-testid="pd-assets"] li')).toHaveCount(1)

    // 切 live：按钮的**条件渲染**就是状态读数（statusOf==='live' 时这颗钮不再存在）
    await expect(page.getByTestId('pd-to-live')).toBeVisible()
    await page.getByTestId('pd-to-live').click()
    await expect(page.getByTestId('pd-to-live')).toBeHidden()

    // ⑤ 删除：确认框只弹一次（自建浮层 role=alertdialog，不许 page.on('dialog')——原生事件在这里永不触发）
    await page.getByTestId('pd-delete').click()
    const dialogs = page.getByRole('alertdialog')
    await expect(dialogs).toHaveCount(1)
    // 点「确定」之前先架好请求探针：旧缺陷形态是第一次确认时 DELETE 还没发出（屏幕再弹第二次框），
    // 所以「请求真发出」＋「不再剩第二个框」两条一起判，才分得清「弹两次」与「没反应」。
    const delReq = page.waitForRequest((r) => r.url().includes('/api/product/delete') && r.method() === 'POST', { timeout: 15000 })
    await dialogs.locator('.lc-dialog__actions button').last().click()
    await delReq
    await expect(dialogs).toHaveCount(0)

    // 列表真刷新（页面腿）：当前份不再是 A——只剩 B 就显示 B，被删空则整个 pd-empty；两种都不许还显示 A
    await expect(page.getByTestId('pd-notice')).toBeVisible()
    await expect(page.getByTestId('pd-name')).not.toHaveValue(nameA, { timeout: 15000 })

    // 列表真刷新（服务端腿）：/api/product/list 不再含 A、仍含 B——接口说成功不算验收，服务端事实才算
    const listAfter = await request.get('/api/product/list', { headers: authHeaders })
    const listBody = await listAfter.json()
    const ids = ((listBody.products || []) as Array<{ id: unknown }>).map((p) => String(p.id))
    expect(ids.includes(idA), `删除后服务端列表仍含 ${idA} ⇒ 列表根本没刷新/删除没生效`).toBeFalsy()
    expect(ids.includes(idB), `同页另一份 ${idB} 不该被连带删掉`).toBeTruthy()

    // 清 B 并反验（afterEach 还会兜底一次，这里要的是**当场**摘干净）
    await request.post('/api/product/delete', {
      headers: { ...authHeaders, 'Content-Type': 'application/json' },
      data: { id: idB },
    })
    const listFinal = await request.get('/api/product/list', { headers: authHeaders })
    const finalBody = await listFinal.json()
    const finalIds = ((finalBody.products || []) as Array<{ id: unknown }>).map((p) => String(p.id))
    expect(finalIds.includes(idB), `B 摘除后列表仍含 ${idB}（清场没生效，会污染货架计数腿）`).toBeFalsy()
  })
})
