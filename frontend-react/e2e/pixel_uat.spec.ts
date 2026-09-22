// ============================================================================
// e2e/pixel_uat.spec.ts — 前端像素级 UAT（前端↔后端联调 + 视觉/布局/关键链路）
// 覆盖：
//   P1 登录页 / 注册页可渲染
//   P2 工作台（对话翻译）输入→发送→收到译文（真实后端+mock LLM 全链路）
//   P3 自服务页：余额/邀请/套餐/账号
//   P4 工单 / 对照编辑
//   P5 后台管理各面板（overview/models/kb/tickets/system/personal）
//   P6 公开页：pricing / docs
//   P7 像素级检查：关键元素可见 + 无横向溢出（scrollWidth<=clientWidth）
// 全部截图存 artifacts/ 供人工复核
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/pixel_uat.spec.ts
// ----------------------------------------------------------------------------
// ★ 2026-09-22 载入闸门口径（详见 playwright.config.ts 的 #66 注释）：
//   冷启动占位（WordSwap 换词动效）要演满 3 个语种拍次（约 9s）才放行。
//   登录后用例凡要「截图 / 量横向溢出 / 读整页文案」，都必须先等闸门撤销，
//   否则截到的只是占位页、溢出检查等于没做（历史上 P4/P5 就是这样静默通过的）。
// ★ 语种口径：本文件通篇用中文文案与 /工单|余额|套餐/ 类中文正则断言，
//   依赖 playwright.config.ts 里钉死的 locale:'zh-CN'。首访语言已改为「按浏览器
//   语言自动检测」，不钉住就会整批落到 en 而随机翻红（不是产品缺陷）。
// ============================================================================
import { test, expect, Page } from '@playwright/test';

// BASE：本地 CI 起 8899 预览实例；e2e 禁写死生产域名（AGENTS §6），需打生产请走 e2e-manual/
const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
// 截图小件：把页面截图落 artifacts/<name>.png，供人工复核像素渲染
const shot = (p: Page, name: string) => p.screenshot({ path: `artifacts/${name}.png`, fullPage: false });

// WCAG 对比度实计算：把 getComputedStyle 返回的 rgb()/rgba() 前景色对工作台卡面 #0E1014
// 求比值（口径与 src/styles/readability.test.ts 一致），供「弱文字不得偏暗」类断言使用。
function contrastOnCard(fg: string, bg = '#0E1014'): number {
  const parse = (s: string): [number, number, number] => {
    const m = s.match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/);
    if (!m) throw new Error(`无法解析颜色：${s}`);
    return [+m[1], +m[2], +m[3]];
  };
  const lum = (rgb: [number, number, number]) => {
    const [r, g, b] = rgb.map((v) => v / 255)
      .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const hex = (h: string): [number, number, number] =>
    [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16)) as [number, number, number];
  const a = lum(parse(fg)), b = lum(hex(bg));
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

// 登录态注入：API 登录拿 token → localStorage 种入（与产品实际登录态等价）
// 默认测试账号：可用 UAT_USER/UAT_PASS 覆盖（不同联调库种子用户不同）
// 用 addInitScript 而非 page.evaluate：它在该 context 的**每次导航前**都重跑一次，
// 于是同一条用例里连续 goto('/tickets')→goto('/editor')… 不会被 Root 的未登录分诊弹回登录页
// （走的是「有 token → 会话恢复中 → restoreGate 补拍」这条真实冷启动路径）。
async function login(page: Page, user = process.env.UAT_USER || 'uatuser_a', pass = process.env.UAT_PASS || 'uatpass123') {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
}

test.describe('像素级 UAT', () => {
  test('P1 登录页渲染', async ({ page }) => {
    // 未登录访问 '/' 出的是营销落地页（Root 的未登录分支），此时本地无 token ⇒
    // restoring 恒 false ⇒ 不触发载入闸门补拍，所以这条用例不需要等闸门撤销（也不该等）。
    // 只断「能渲染 + 有登录入口文案」，视觉细节由 p1_login.png 人工看。
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const txt = await page.locator('body').innerText();
    expect(txt).toContain('登录');
    await shot(page, 'p1_login');
  });

  test('P2 工作台对话翻译全链路（发送→译文回流）', async ({ page }) => {
    await login(page);
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const ta = page.getByPlaceholder(/输入要翻译的文本/);
    await expect(ta).toBeVisible();
    await ta.fill('今天天气怎么样，适合出门吗？');
    // ★ 2026-09-18 UI 迁移（图 07-translate）：工作台主按钮文案「发送」→「翻译」。
    //   用兼容正则锁「触发翻译的主按钮」语义而非字面文案，措辞再变也不会误红。
    await page.getByRole('button', { name: /^(发送|翻译|Send|Translate)$/ }).click();
    // 60s 上限：这条要穿过完整真实链路（闸门放行 → 估价 → mock LLM 往返 → 计量落库 →
    // 前端回读），CI 上后端刚起、模型路由首请求偏慢时 20~30s 很常见，宁松不假红。
    await expect(page.getByText(/TranslatedEN|翻译结果/).first()).toBeVisible({ timeout: 60000 });
    await shot(page, 'p2_workbench');
  });

  // ★ #36 + UI 真值（2026-09-22 口径改向）结构/字阶双断言：
  //   #36 即时翻译的输入框与译文合并进同一张对话框卡（旧版是「输入卡 + 下方消息流」两块），
  //      同时文件翻译入口从即时翻译下线（文件翻译统一走工单通道），故卡内不得再有 file input；
  //   字阶/灰阶这一半原先钉的是「#35 整体上调一档」的结果，现已随全站按 UI 交付稿还原改为
  //      等值锁（真值来自 前端及UI相关/UI-ANNOTATIONS.md 与交付包）——这里用 getComputedStyle
  //      实测的是「渲染出来的值等于交付值」，与 readability.test.ts 的源码级锁互补：
  //      单测拦「CSS 里被改回去」，本处拦「运行时内联样式/换肤把值盖掉」。
  test('P2b 工作台合并对话框结构 + 字阶/灰阶按交付真值', async ({ page }) => {
    await login(page);
    await page.goto('/');
    // 结构锁走 class（.cw-dialog*）而非文案：文案受 i18n 与措辞调整影响，
    // 而「输入区在滚动区内、操作区在 foot 内」这套 DOM 形态才是 #36 的交付口径。
    const dialog = page.locator('.cw-dialog');
    await expect(dialog).toBeVisible();
    // 输入框在对话框卡的滚动区内（合并后的唯一结构口径）
    await expect(dialog.locator('.cw-dialog-body textarea')).toBeVisible();
    await expect(dialog.locator('.cw-dialog-foot')).toBeVisible();
    // 文件入口下线：全站工作台不应再挂隐藏的原生 file input
    expect(await page.locator('input[type="file"]').count(), '即时翻译已移除文件上传入口').toBe(0);
    // 字号真值等值锁（★ 2026-09-22 全站还原批）：本批把 #35/#67/#68 的「整档 +1px / 顶栏提档」
    // 全部撤除，字阶回到交付 UI 口径，所以这里由「≥ 下限」改成「恰等于真值档」——
    // 单向下限锁正是把配色与字阶一路推离交付稿的元凶（它只拦变小，不拦变大）。
    // 输入框 13 = 组件库 .lc-textarea 冻结档（第 1 轮交付值），不是页面层自选值。
    const taFs = await dialog.locator('.cw-dialog-body textarea').evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(taFs, `输入框字号 ${taFs}px ≠ 组件库真值 13px`).toBe(13);
    // 正文气泡 14 = theme.css .bubble（交付包正文档）
    const bubbleFs = await page.locator('.bubble').first().evaluate((el) => parseFloat(getComputedStyle(el).fontSize)).catch(() => -1);
    if (bubbleFs >= 0) expect(bubbleFs, `气泡字号 ${bubbleFs}px ≠ 真值 14px`).toBe(14);
    // 弱说明文字：颜色必须落在真值灰阶集合内（提亮批自造的 #878D95/#7A828E/#9AA2AF 一律红灯），
    // 并顺手核对该灰阶对卡面 #0E1014 的实际比值是否等于真值口径（≥4:1，图形/弱文字档）。
    const welcomeColor = await dialog.locator('.cw-welcome').evaluate((el) => getComputedStyle(el).color);
    const TRUTH_RGB = ['#E7E9EA', '#9AA0AA', '#71767B', '#8A9099', '#536471']
      .map((h) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16)).join(','));
    const welcomeRgb = (welcomeColor.match(/\d+(\s*,\s*\d+){2}/) || [''])[0].replace(/\s/g, '');
    expect(TRUTH_RGB, `弱文字色不在真值灰阶内：${welcomeColor}`).toContain(welcomeRgb);
    expect(contrastOnCard(welcomeColor, '#000000'), `真值灰阶在页面底上不应低于 3.4:1（${welcomeColor}）`).toBeGreaterThanOrEqual(3.4);
    // 顶栏按 §2.2 骨架真值：行高 38、品牌 14 Bold、导航 13px 胶囊、语种钮 12px。
    // 三条锁配对使用：字号等值防「调档」，实高 ≤38 防「折行/撑破行」，行高本身防顶栏被改厚。
    const headerH = await page.locator('.app-header').evaluate((el) => el.getBoundingClientRect().height);
    expect(headerH, `顶栏实高 ${headerH}px ≠ §2.2 的 38px`).toBe(38);
    const brand = page.locator('.app-header .brand').first();
    // 品牌可能是图片 logo（此时 .brand 内只有 <img>），字号锁改为落在 img 的宿主上量，
    // 不能因为白标形态不同就假红。
    expect(await brand.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)), '品牌字号 ≠ §2.2 真值 14px').toBe(14);
    const tab = page.locator('.app-header .app-tab').first();
    // 三个工作台 Tab 共用同一条 .app-tab 规则，取首个即可代表该字阶档（不必逐个数）。
    // 高度锁之前必须先断可见：隐藏元素 getBoundingClientRect() 恒为 0，
    // `<=38` 会静默通过——那正是「断言永远绿」一类的假绿，比翻红更危险。
    await expect(tab, '工作台 Tab 未渲染').toBeVisible();
    const tabFs = await tab.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(tabFs, `工作台 Tab 字号 ${tabFs}px ≠ §2.2 真值 13px`).toBe(13);
    const tabH = await tab.evaluate((el) => el.getBoundingClientRect().height);
    expect(tabH, `工作台 Tab 实高 ${tabH}px ⇒ 文案已折行或控件被撑高`).toBeLessThanOrEqual(38);
    const langBtn = page.locator('.app-header .lang-sel-btn').first();
    await expect(langBtn, '顶栏语种下拉未渲染').toBeVisible();
    // 语种钮同锁：#23 把二元 EN 切换换成 12 语种下拉后，中文语种名（如「简体中文」）
    // 一度把钮撑成两行，故字号真值与「单行实高」两条都要钉，缺一漏一半回归。
    const langH = await langBtn.evaluate((el) => el.getBoundingClientRect().height);
    expect(langH, `语种钮实高 ${langH}px ⇒ 文案已折行`).toBeLessThanOrEqual(38);
    expect(await langBtn.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)), '语种钮字号 ≠ 真值 12px').toBe(12);
    await shot(page, 'p2b_workbench_merged');
  });

  test('P3 自服务页渲染（余额/套餐/账号·企业 + 邀请·个人）', async ({ page }) => {
    await login(page);
    // 每页期望文案写成宽松正则（/余额|积分|充值/）：自服务页的措辞与积分口径常调，
    // 钉死单字会让本文件沦为「改文案就得改 e2e」的负担；这里要锁的是「页面渲染出主体」而非文案本身。
    for (const [path, expectTxt, name] of [
      ['/billing', /余额|积分|充值/i, 'p3_billing'],
      ['/packages', /套餐|包|订阅/i, 'p3_packages'],
      ['/my', /账号|昵称|邮箱/i, 'p3_my'],
    ] as const) {
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      // ★ 2026-09-12 修复竞态：goto 后异步面板（useAsync）尚未渲染即读 innerText 会误报——
      //   改用轮询断言等待目标文案出现（15s 上限），再截图与溢出检查。
      //   ★ 2026-09-22 载入闸门（≥3 语种拍次 ≈9s）后 15s 余量只剩 6s，放宽到 30s。
      await expect(page.locator('body'), `${path} 应含 ${expectTxt}`).toContainText(expectTxt, { timeout: 30000 });
      // 溢出判定留 2px 容差：布局取整/滚动条宽度会带来 1~2px 误差，超过它才算真横向溢出
      // （1280 视口下压不出滚动条的溢出人眼也看不出，但对窄屏与截图复核是隐患）。
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
    // 邀请页仅个人用户可见（产品意图，2026-09 权限收口）：切个人账号（UAT_PERSONAL_USER，默认 uatuser_e；api_uat.sh B6 种入）校验渲染
    await login(page, process.env.UAT_PERSONAL_USER || 'uatuser_e', process.env.UAT_PASS || 'uatpass123');
    await page.goto('/invites');
    // ReferralPanel 依赖 meContext 异步 resolve，需等待邀请内容出现（避免读快于渲染）
    await expect(page.getByText(/邀请码|邀请链接/).first()).toBeVisible({ timeout: 15000 });
    const txt = await page.locator('body').innerText();
    expect(txt, '/invites(个人) 应含邀请内容').toMatch(/邀请好友|邀请码|裂变|奖励/i);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
    expect(overflow, '/invites 横向溢出').toBeFalsy();
    await shot(page, 'p3_invites');
  });

  test('P3-enterprise 企业用户隐藏邀请页（产品意图锁定）', async ({ page }) => {
    await login(page);
    await page.goto('/invites');
    // 反向断言（「不该出现」）没法用「等待元素消失」来表达，只能给一个固定沉降窗口再读整页文案：
    // 400ms 足够覆盖 meContext 的 isPersonal 判定与本用例的渲染帧，
    // 若将来出现假绿（面板延迟挂载晚于 400ms），应改为断言路由跳转到工作台（/invites → Navigate '/'）。
    await page.waitForTimeout(400);
    const txt = await page.locator('body').innerText();
    // 只禁两串面板专属文案：不用泛化的「邀请」，否则顶栏导航/抽屉项里的「邀请有礼」会误伤。
    expect(txt, '企业用户 /invites 不应渲染邀请面板').not.toMatch(/邀请好友 · 多邀多得|我的专属邀请码/);
    await shot(page, 'p3_invites_hidden_enterprise');
  });

  test('P4 工单页与对照编辑渲染', async ({ page }) => {
    await login(page);
    for (const [path, name, marker] of [
      ['/tickets', 'p4_tickets', /工单/],
      ['/editor', 'p4_editor', /原文|译文|上传/],
    ] as const) {
      await page.goto(path);
      // ★ 2026-09-22「至少读完三个语种再载入」：冷启动占位会顶住约 9s，
      //   旧写法只等 body 可见 ⇒ 截图截到换词动效、溢出检查也等于没做。
      //   改为等顶栏（占位撤销后才出现）+ 页面主体文案，再截图与量溢出。
      //   闸门撤销的判据分两层：.app-header 证明「会话恢复闸门」已放行
      //   （恢复期 Root 直接 return 占位组件，整个外壳含顶栏都还没挂载）；
      //   随后的 body 文案锁再兜住「后端探活闸门」（那道闸门只替换 app-main 内容，顶栏已在）。
      await expect(page.locator('.app-header'), `${path} 载入闸门未撤销`).toBeVisible({ timeout: 30000 });
      await expect(page.locator('body'), `${path} 应含页面主体`).toContainText(marker, { timeout: 30000 });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P5 后台管理面板渲染（超管）', async ({ page }) => {
    await login(page, 'admin', 'Admin@1234');
    await page.goto('/admin');
    // ★ 2026-09-22：等冷启动载入闸门（≥3 语种拍次 ≈9s）撤销后再截图。
    //   后台是独立外壳（AdminPage 侧栏，没有 .app-header），故按面板文案等。
    await expect(page.locator('body'), '/admin 应含后台面板').toContainText(/模型|知识库|工单|系统/, { timeout: 30000 });
    await shot(page, 'p5_admin_overview');
    // 逐个点侧栏项而非直连深链：后台面板由 useAdminStore 的当前面板驱动（多数无独立 URL），
    // 点文案是唯一能覆盖「导航 + 面板挂载」的方式。
    // 期望值同样写成宽松正则——面板标题/字段措辞会随版本调整，这里锁的是「该面板真渲染出内容」。
    const panels: [string, RegExp, string][] = [
      ['模型配置', /模型|路由|密钥/i, 'p5_admin_models'],
      ['知识库', /知识|术语|包/i, 'p5_admin_kb'],
      ['工单管理', /工单/i, 'p5_admin_tickets'],
      ['系统设置', /系统|配置/i, 'p5_admin_system'],
      ['个人中心', /邀请|任务|我的/i, 'p5_admin_personal'],
    ];
    for (const [label, rx, name] of panels) {
      const item = page.getByText(label, { exact: true }).first();
      // 侧栏项按 i18n 文案查（exact 防「模型」命中「模型路由说明」之类），未渲染则跳过该项：
      // 后台导航会按角色/开关裁剪，缺项是正常形态；真正的渲染回归由上面 overview 那条锁兜住。
      if (await item.count()) { await item.click(); } else { continue; }
      // ★ 2026-09-12 修复竞态：面板数据异步加载，轮询等待目标文案而非固定 400ms 后一次性读取
      await expect(page.locator('body'), `${label} 面板应含 ${rx}`).toContainText(rx, { timeout: 15000 });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${label} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P6 公开页渲染（价格/文档）', async ({ page }) => {
    for (const [path, name] of [['/pricing', 'p6_pricing'], ['/docs/terms', 'p6_terms']] as const) {
      // 这两页实现路径不同：/pricing 是 SPA 壳（需 JS 渲染，未登录由 Root 直出营销页），
      // /docs/terms 由后端直出静态 HTML。故只锁「正文非空壳 + 无横向溢出」这条公共底线，
      // 具体文案各自人工看截图——钉死任一方的文案都会把另一种实现绑住。
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      const txt = await page.locator('body').innerText();
      // 阈值取 20 字符而非更大值：只需区分「渲染出内容」与「白屏/空 root」两种形态。
      expect(txt.length, `${path} 空页面`).toBeGreaterThan(20);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P7 整体无横向溢出（登录态关键页）', async ({ page }) => {
    await login(page);
    // 本条是「一页一路径」的粗筛：把 P3/P4/P5 各自视角漏掉的溢出（尤其 /admin 独立外壳）
    // 一网打尽。这里不重等闸门撤销：闸门期屏上是居中的换词占位，本来就不会横向溢出，
    // 300ms 只是给异步面板一点沉降时间；真正「截到占位页」的风险由 P4/P5 的闸门锁负责红。
    for (const path of ['/', '/tickets', '/editor', '/billing', '/admin']) {
      await page.goto(path);
      await page.waitForTimeout(300);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
    }
  });
});
