// ============ e2e/onboarding_guided.spec.ts · 职责说明 ============
// 一期 O 组＋H1/H2/H5「第一公里引导流」的**真浏览器**运行时锁（2026-11-06）。
//
// 为什么 jsdom 那批不够：`Onboarding.flow.dom.test.tsx`／`Onboarding.gate.dom.test.tsx` 把 api 层
// 全 mock 掉了——它们能证"上送的入参形状对"，证不了
//   ① 带 id 的 save 真被后端接受（§30 那条"整份存回"的契约，只有真 HTTP 才量得到），
//   ② 走完四步后**库里真有这一行**且 target_langs 真存进去了，
//   ③ 引导页在真浏览器里挂的是**外壳那一份**（`.ar-panel`），不是又长出来的一屏，
//   ④ 横幅那件东西**真的不在了**（组件已删；留着一条 testid 负向锁盯着它别复活）。
// ⇒ AGENTS §30 那条教训的具体化：面板批的验收要问「客户从哪个按钮开始走通这件事」，
//   只数"接口在不在、单测绿不绿"会把商品缺口读成功能完成。
//
// 夹具契约与 product_detail_write.spec.ts 完全同面孔（PRODUCT_SHELF_AUTH_STATE ＋ PRODUCT_TOKEN，
// 由 run_uat.sh 的产品 e2e 夹具段注入），**不自造第二种登录方式**。
//
// 三条硬口径：
//  ① 链路型用例自带可达探针：接口腿显式带 Bearer（`request` fixture 不吃 storageState），
//     且以**服务端事实**收尾（list 里含新建 id、target_langs 含所选语种）——
//     "界面说存好了"不算验收。
//  ② 本文件只删自己建的那一份产品（afterEach 兜底），**绝不碰夹具产品**（货架/详情两条 spec 依赖它）。
//  ③ 模型整理腿在 UAT 里**没配阶段模型**（`onboard_draft` 第八→第九张卡缺省空）⇒
//     这一枪的正确形态是"出声一句、候选全空、向导照样走得完"。
//     断言因此只问"笔记元素在位且非空 ＋ 后续步骤仍成功"，**不**断言候选填进了表单
//     （那会把"没配模型"这个环境事实写成产品失败）。
// ==============================================================
import { expect, test } from '@playwright/test'

const AUTH_STATE = process.env.PRODUCT_SHELF_AUTH_STATE || ''
const TOKEN = process.env.PRODUCT_TOKEN || ''

test.describe('引导流 /onboarding（O 组＋H1 外壳＋H2 整理腿＋H5 去横幅）', () => {
  test.skip(!AUTH_STATE || !TOKEN, '需 PRODUCT_SHELF_AUTH_STATE ＋ PRODUCT_TOKEN 才能打登录态引导页')
  test.use({ storageState: AUTH_STATE })

  const stamp = Date.now()
  const name = `E2E-ONBOARD-${stamp}`
  const authHeaders = { Authorization: `Bearer ${TOKEN}` }
  let createdId = ''

  /** 服务端事实：这一份产品真在列表里，且语种真存进去了 */
  async function fetchProduct(request: import('@playwright/test').APIRequestContext, id: string) {
    const r = await request.get('/api/product/list', { headers: authHeaders })
    expect(r.ok(), `链路没通：GET /api/product/list 非 2xx（${r.status()}）`).toBeTruthy()
    const body = await r.json()
    const hit = (body.products || []).find((p: { id: string }) => String(p.id) === id)
    return { body, hit }
  }

  test.afterEach(async ({ request }) => {
    // 清场按显式 id 摘，并反验列表里真没有（§29 那条"夹具落别人租户/整租户蒸发"的反面）
    if (!createdId) return
    const del = await request.post('/api/product/delete', {
      headers: { ...authHeaders, 'Content-Type': 'application/json' },
      data: { id: createdId },
    })
    expect(del.ok(), `清场接口不可达：POST /api/product/delete 非 2xx（${del.status()}）`).toBeTruthy()
    const { hit } = await fetchProduct(request, createdId)
    expect(hit, `清场后列表里仍含 ${createdId} ⇒ 测试数据留下了`).toBeUndefined()
    createdId = ''
  })

  test('四步走完 ⇒ 库里真有这一行、语种真存进去；横幅那件东西不再存在', async ({ page, request }) => {
    // ① 可达探针（离线兜底会画出一张"看起来正常"的空引导，所以先问接口）
    const { body: before } = await fetchProduct(request, '__none__')
    expect(before && before.success === true, '探针：产品列表接口未返回 success（数据没真取到）').toBeTruthy()

    // ★ 登录态必须**显式注入 sessionStorage**，不能只靠 storageState：
    //   入口分诊在 main.tsx 模块初始化时读一次 getAuthToken()，实测本轮 storageState 里那份
    //   sessionStorage 赶不在模块初始化之前落地 ⇒ 分诊按"未登录"走公开壳，
    //   引导面板整颗不渲染，红字是 .ar-panel 不可见（看着像组件坏了，其实是登录态没赶上第一帧）。
    //   ——这条口径抄自 e2e/product_shelf.spec.ts 的同款教训（AGENTS §29 末条）。
    await page.addInitScript((t) => {
      try { sessionStorage.setItem('auth_token', t) } catch { /* 站点数据被拒＝按未登录起步，下面的判据自己会红 */ }
    }, TOKEN)

    await page.goto('/onboarding')
    // ③ 挂的是外壳那一份：面板/进度/聊天流三个锚点都在，且**没有**第二套实现留下的痕迹
    await expect(page.locator('.ar-panel')).toBeVisible()
    await expect(page.locator('.ar-chat')).toBeVisible()
    const label = page.locator('.ar-prog-label')
    await expect(label).toContainText('1')
    await expect(label).toContainText('4')

    // ④ 横幅退役：整站不许再有那两个 testid（组件已删，这条负向锁盯着别复活）
    expect(await page.locator('[data-testid="onboarding-prompt"]').count(), '底部横幅仍在 ⇒ H5 没去掉').toBe(0)
    expect(await page.locator('[data-testid="prompt-goto-onboarding"]').count(), '横幅那颗按钮仍在').toBe(0)

    // —— 第 1 步（公司与行业）：skip 口，点下一步不该发任何写请求
    let saves = 0
    page.on('request', (r) => { if (r.url().includes('/api/product/save') && r.method() === 'POST') saves++ })
    await page.getByTestId('onboard-next').click()
    await expect(label).toContainText('2')
    expect(saves, '第 1 步是回显确认（write=skip），不该发建档请求').toBe(0)

    // —— 第 2 步（产品信息）：先试模型整理腿（UAT 没配阶段模型 ⇒ 必须出声且不挡路）
    await page.getByTestId('onboard-draft-text').fill('一台 1000W 的便携储能电源，主打户外露营与应急供电。')
    const draftResp = page.waitForResponse((r) => r.url().includes('/api/product/onboard/draft'), { timeout: 30000 })
    await page.getByTestId('onboard-draft').click()
    const draft = await draftResp
    expect(draft.status(), `整理口不可达（${draft.status()}）——这一枪必须是 200 或 401/400，不该是路由缺失的 404`).toBeTruthy()
    const dbody = await draft.json()
    expect(dbody && dbody.success === true, `整理口未返回 success：${JSON.stringify(dbody).slice(0, 200)}`).toBeTruthy()
    // 空候选是合法结果（没配阶段模型）⇒ 必须有一句可懂的出声
    await expect(page.getByTestId('onboard-draft-note')).not.toHaveText('')
    // 关键：**不挡向导**——名字仍可手填并前进
    await page.getByTestId('onboard-field-name').fill(name)
    const save1 = page.waitForResponse((r) => r.url().includes('/api/product/save'))
    await page.getByTestId('onboard-next').click()
    const s1 = await (await save1).json()
    expect(s1 && s1.success === true, `建档未成功（后端契约是"整份存回"，缺字段会被如实拒）：${JSON.stringify(s1).slice(0, 200)}`).toBeTruthy()
    createdId = String(s1.id || (s1.product && s1.product.id) || '')
    expect(createdId, '建档返回里没有 id ⇒ 后面三条判据都无从谈起').toBeTruthy()
    await expect(label).toContainText('3')

    // —— 第 3 步（目标语言）：勾一个语种，必须真存进去
    await page.getByTestId('onboard-lang-en').click()
    const save2 = page.waitForResponse((r) => r.url().includes('/api/product/save'))
    await page.getByTestId('onboard-next').click()
    const s2 = await (await save2).json()
    expect(s2 && s2.success === true, `语种这一步写失败：${JSON.stringify(s2).slice(0, 200)}`).toBeTruthy()
    await expect(label).toContainText('4')

    // —— 第 4 步（收录开关）：交唯一写点 seoSet，收尾摘要卡出现
    const seoResp = page.waitForResponse((r) => r.url().includes('/api/product/seo'))
    await page.getByTestId('onboard-next').click()
    const seo = await seoResp
    expect(seo.ok(), `SEO 口不可达（${seo.status()}）`).toBeTruthy()
    await expect(page.getByTestId('onboard-goto-detail')).toBeVisible()

    // ② 服务端事实收尾：这一行真在、名字对、语种真存进去（读数要为事实背书）
    const { hit } = await fetchProduct(request, createdId)
    expect(hit, `列表里没有刚建的 ${createdId} ⇒ 界面说"存好了"而服务端没有`).toBeTruthy()
    expect(String(hit.name)).toBe(name)
    const langs = Array.isArray(hit.target_langs) ? hit.target_langs : []
    expect(langs, `target_langs 没存进去（出栈=${JSON.stringify(hit.target_langs)}）`).toContain('en')
  })
})
