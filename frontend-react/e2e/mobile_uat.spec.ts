// ============================================================================
// e2e/mobile_uat.spec.ts — 移动端自适应核对脚本
// 职责：以手机视口（390×844）验证主站与演示站的移动端体验：
//   - 后台侧边栏在窄屏转抽屉（汉堡唤起 / 遮罩关闭 / 点菜单关闭）
//   - 聊天工作台输入栏可换行、无横向溢出，且 ★ F-48 起顶栏余额徽标在窄屏仍可见（省略号截断）
//   - 全站关键页面（自服务/工单/对照编辑/公开定价）无横向溢出
// 前提：本地后端 + 构建后的前端 dist 已在 BASE_URL 服务（参考 run_uat.sh 启动方式）。
// ============================================================================
import { test, expect } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
test.use({ viewport: { width: 390, height: 844 } });

// 登录小件：admin 模式进后台（/admin），home 模式进前台（/）——默认沿用固定超管账号，
// 可显式传租户口径账号（见下方工作台用例的 O-9 说明）。
async function login(page: import('@playwright/test').Page, mode: 'admin' | 'home', user = 'admin', pass = 'Admin@1234') {
  // ★ 修复（2026-09-14）：home 模式改走 /login——`/` 已是营销 Landing 页（无登录卡），
  //   旧路径等待登录卡必超时（UAT 实测 mobile_uat 两条确定性失败即源于此）
  // ★ 修复（2026-09-18 UI 迁移）：登录卡皮肤 TDesign → LangCross，锚点 `.login-card` →
  //   `.lc-auth-card`；提交键必须点名 `.lc-auth-card__submit`——卡内第一个 button 是密码
  //   明暗切换眼睛键，button.first 会点错（点击后仅切换可见性，表单永不提交 → 静默超时）。
  await page.goto(`${BASE}/${mode === 'admin' ? 'admin' : 'login'}`, { waitUntil: 'networkidle' });
  await page.waitForSelector('.lc-auth-card', { timeout: 30000 });
  // ★ 2026-10-21 IAM 重做：登录卡默认是邮箱验证码档，密码是备用入口——
  //   直接 fill(input[type=password]) 会因该输入框根本未渲染而超时。
  //   这里显式点「用密码登录」再填（toggle 是卡内第一颗 button.auth-link）。
  const pwd = page.locator('.lc-auth-card input[type="password"]').first();
  if (await pwd.count() === 0) {
    await page.locator('.lc-auth-card button.auth-link').first().click();
  }
  // 账号 + 密码输入框（注册表单亦有同类输入框，登录卡先渲染）
  const inputs = page.locator('.lc-auth-card input').first();
  await inputs.fill(user);
  await pwd.fill(pass);
  await page.locator('.lc-auth-card__submit').click();
  await page.waitForLoadState('networkidle');
}

// 移动端后台核对：汉堡可见 → 侧栏默认移出屏外 → 点击滑入 + 遮罩出现 → 点菜单关闭 → 点遮罩关闭 → 无水平溢出
test('移动端后台：侧边栏转抽屉（汉堡唤起/遮罩关闭/无溢出）', async ({ page }) => {
  await login(page, 'admin');
  // ★ 2026-09-18 UI 迁移：后台外壳 TDesign Menu → ui/langcross AdminShell，
  //   锚点映射 .admin-shell→.lc-shell、.admin-nav-toggle→.lc-shell-burger、
  //   .admin-side→.lc-sidebar、.admin-side-mask→.lc-shell-scrim、.t-menu__item→.lc-side-item
  await page.waitForSelector('.lc-shell', { timeout: 30000 });

  // 汉堡按钮可见
  const toggle = page.locator('.lc-shell-burger');
  await expect(toggle).toBeVisible();
  await expect(toggle).toBeVisible(); // display:inline-flex（桌面隐藏）
  const toggleDisplay = await toggle.evaluate((el) => getComputedStyle(el).display);
  console.log('汉堡 display:', toggleDisplay);
  expect(toggleDisplay).not.toBe('none');

  // 侧边栏默认移出屏外
  const side = page.locator('.lc-sidebar');
  const x0 = await side.evaluate((el) => el.getBoundingClientRect().x);
  console.log('侧边栏初始 x:', x0);
  expect(x0).toBeLessThan(0);

  // 点击汉堡 → 抽屉滑入
  await toggle.click();
  await page.waitForTimeout(400);
  const x1 = await side.evaluate((el) => el.getBoundingClientRect().x);
  console.log('侧边栏打开后 x:', x1);
  expect(x1).toBeGreaterThanOrEqual(0);
  await expect(page.locator('.lc-shell-scrim')).toBeVisible();

  // 点击菜单项 → 抽屉关闭（AdminShell.onNavigate 内先 closeDrawer 再切面板，与旧口径一致）
  await page.locator('.lc-sidebar .lc-side-item').nth(1).click();
  await page.waitForTimeout(400);
  const x2 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x2).toBeLessThan(0);

  // 重新打开，点遮罩关闭
  await toggle.click();
  await page.waitForTimeout(400);
  await page.locator('.lc-shell-scrim').click({ position: { x: 380, y: 400 } });
  await page.waitForTimeout(400);
  const x3 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x3).toBeLessThan(0);

  // 水平溢出检测
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('后台页水平溢出:', overflow);
  expect(overflow).toBe(false);
  await page.screenshot({ path: 'artifacts/mobile-admin.png' });
});

// 移动端工作台核对：★ 2026-10-15 用户令（第五批）「参考元宝重做移动端，除会话外全部塞进汉堡」——
//   S 档（≤640）前台外壳＝「极简标题栏 .lc-mob-topbar(52)：汉堡 + 品牌簇」＋ 汉堡抽屉
//   （同一份 .lc-sidebar，--drawer 档）；底部胶囊 .lc-tabbar 撤除，假状态栏 .lc-statusbar
//   与第二信息带 .lc-mob-band 早已退役。本用例按新载体重钉：
//   ① 顶栏在位且高=令牌档（--lc-mob-topbar-h 52，等值锁）＋ TabBar 负向清零；
//   ② 汉堡旁边的品牌簇（更大 logo + 品牌名）在位且非空；
//   ③ 抽屉：默认移出屏外 → 点汉堡滑入 + 遮罩出现 → 点遮罩收起；抽屉内可见折叠页脚（默认收起）；
//   ④ 顶栏子元素无纵向重叠；
//   ⑤ AI 浮球不得压住输入行的发送键（页脚收进抽屉后输入卡贴到视口底，旧口径「不压 TabBar」随之换成这条）；
//   ⑥ 壳上无余额带（F-48 余额口径改由「我的」页承载，判据见文末）。
test('移动端工作台：顶栏＋汉堡抽屉形态锁＋折叠页脚＋浮球不压发送键＋壳上无余额带', async ({ page }) => {
  // 登录口径沿用 O-9 复跑账：余额载体的真用户是**租户计费用户**（超管平台上下文余额恒空），
  // 故用 uatuser_a（与 pixel/translate_flow 同口径）。
  await login(page, 'home', process.env.UAT_USER || 'uatuser_a', process.env.UAT_PASS || 'uatpass123');
  await page.waitForSelector('.lc-mob-topbar', { timeout: 30000 });
  await page.waitForTimeout(1500);

  // ---- ① 顶栏在位＋高度等值（52，与 tokens.css --lc-mob-topbar-h 逐像素对表）；TabBar 负向清零 ----
  const topbar = page.locator('.lc-mob-topbar');
  await expect(topbar, 'S 档必须渲染移动标题栏').toBeVisible();
  const tbH = await topbar.evaluate((el) => el.getBoundingClientRect().height);
  expect(tbH, '移动标题栏高 ≠ --lc-mob-topbar-h(52)').toBeCloseTo(52, 0);
  expect(await page.locator('.lc-tabbar').count(), '底部 TabBar 已按用户令撤除（旧「两件套」形态不再成立）').toBe(0);

  // ---- 互斥锁：S 档桌面顶栏不得同屏渲染（出现＝媒体查询/useState 判档被破坏）----
  expect(await page.locator('.app-header').count(), 'S 档 .app-header 必须整块不渲染（移动壳互斥）').toBe(0);
  // ---- 负向锁：假状态栏 / 第二信息带已退役（装回来当场红）----
  expect(await page.locator('.lc-statusbar').count(), '假状态栏应已退役（元宝式移动壳不重造系统状态栏）').toBe(0);
  expect(await page.locator('.lc-mob-band').count(), '第二信息带（常驻余额）应已退役').toBe(0);

  // ---- ② 汉堡旁边的品牌簇：更大 logo + 品牌名（用户令「汉堡旁显示更大 logo 和品牌名」）----
  const burger = page.locator('.lc-mob-topbar__icon');
  await expect(burger, '顶栏缺汉堡开关').toBeVisible();
  const brand = page.locator('.fs-mob-brand');
  await expect(brand, '顶栏缺品牌簇（logo + 品牌名）').toBeVisible();
  expect((await brand.innerText()).trim().length, '品牌名不得为空').toBeGreaterThan(0);
  // 品牌名必须比正文档显眼（20px 档；这里只判「比 15px 正文大」的形态，不钉死字阶）
  const brandName = page.locator('.fs-mob-brand__name').first();
  if (await brandName.count()) {
    const fs = await brandName.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(fs, '移动顶栏品牌名未放大（应 ≥18px）').toBeGreaterThanOrEqual(18);
  }

  // ---- ③ 汉堡抽屉：默认移出屏外 → 点汉堡滑入 + 遮罩 → 点遮罩收起 ----
  const side = page.locator('.lc-sidebar').first();
  const x0 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x0, '移动档左栏默认必须移出屏外（--drawer 档）').toBeLessThan(0);
  await burger.click();
  await page.waitForTimeout(400);
  const x1 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x1, '点汉堡后抽屉未滑入').toBeGreaterThanOrEqual(0);
  await expect(page.locator('.lc-shell-scrim'), '抽屉打开时必须出现遮罩').toBeVisible();
  // 抽屉内的折叠页脚：用户令「移动端页脚也缩入汉堡并且折叠」——存在（在左栏内）且默认是收起态
  const fold = page.locator('.lc-sidebar .fs-foot-fold');
  await expect(fold, '移动抽屉里必须有折叠页脚').toHaveCount(1);
  expect(await fold.evaluate((el) => el.hasAttribute('open')), '折叠页脚默认必须是收起态').toBe(false);
  // 展开后必须真出现协议/手册链接（收起态只是省地方，不是把页脚藏没）
  await fold.locator('summary').click();
  await expect(fold.locator('footer a').first(), '展开折叠页脚后看不到任何链接').toBeVisible();
  await page.locator('.lc-shell-scrim').click({ position: { x: 380, y: 400 } });
  await page.waitForTimeout(400);
  const x2 = await side.evaluate((el) => el.getBoundingClientRect().x);
  expect(x2, '点遮罩后抽屉未收起').toBeLessThan(0);

  // ---- ④ 顶栏子元素无纵向重叠/被裁：每个孩子的 rect 落在顶栏盒内（±0.5px 取整余量）----
  const overlap = await topbar.evaluate((el) => {
    const box = el.getBoundingClientRect();
    const bad: string[] = [];
    for (const child of Array.from(el.children)) {
      const r = child.getBoundingClientRect();
      if (r.height === 0) continue; // display:none 的孩子不参与
      if (r.top < box.top - 0.5 || r.bottom > box.bottom + 0.5) {
        bad.push(`${child.className || child.tagName} top=${r.top.toFixed(1)} bottom=${r.bottom.toFixed(1)} 盒=${box.top.toFixed(1)}~${box.bottom.toFixed(1)}`);
      }
    }
    return bad;
  });
  expect(overlap, `顶栏子元素越出顶栏盒（旧 height:38px+flex-wrap 裁切形态复活）：\n${overlap.join('\n')}`).toEqual([]);

  // ---- ⑤ ★ AI 助手浮球（.na-fab，全局 fixed）不得压住输入行的发送键 ----
  //   旧口径「不压底部 TabBar」随 TabBar 撤除失效；页脚收进抽屉后输入卡贴到视口底，
  //   .na-fab 原格（bottom:14px）正落在发送键上 ⇒ 现判「浮球盒与发送键盒不相交」。
  const send = page.locator('.cw-send').first();
  const fab = page.locator('.na-fab');
  if (await fab.count() && await send.count()) {
    const sb = await send.boundingBox();
    const fb = await fab.boundingBox();
    if (sb && fb) {
      const ox = Math.min(fb.x + fb.width, sb.x + sb.width) - Math.max(fb.x, sb.x);
      const oy = Math.min(fb.y + fb.height, sb.y + sb.height) - Math.max(fb.y, sb.y);
      expect(ox <= 0 || oy <= 0, '★ AI 浮球压住发送键（发不出去）').toBe(true);
    }
  }

  // ---- ⑥ 水平溢出（原锁保留）----
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('工作台水平溢出:', overflow);
  expect(overflow).toBe(false);
  // 输入栏存在（★ 2026-09-18 UI 迁移：原生 textarea 挂 LangCross 皮肤类 .lc-textarea，旧 .chat-inputbar 已废弃）
  await expect(page.locator('.lc-textarea')).toBeVisible();
  // ★ 用户令「聊天内选择做成元宝式，放在输入框上方」：窄屏下选择行（工具条）必须排在输入框**之前**
  //   （同一容器内的 DOM 顺序未变，靠 order 重排 ⇒ 判据只能量几何：工具条底 ≤ 输入框顶）。
  const geo = await page.evaluate(() => {
    const t = document.querySelector('.cw-toolbar');
    const i = document.querySelector('.cw-input');
    if (!t || !i) return null;
    return { tBottom: t.getBoundingClientRect().bottom, iTop: i.getBoundingClientRect().top };
  });
  if (geo) expect(geo.tBottom, '窄屏选择行没有排到输入框上方（元宝式布局未生效）').toBeLessThanOrEqual(geo.iTop + 1);

  await page.screenshot({ path: 'artifacts/mobile-workbench.png' });

  // ---- F-48（2026-10-15 改写载体）：余额不再常驻壳上，改由「我的」页承载 ----
  //   旧载体 .lc-mob-band（S 档第二信息带）随元宝式移动壳退役，故 F-48「余额不得在手机上隐身」
  //   的判据换成两条：
  //   ① 壳上负向清零（上文已断 .lc-mob-band / .lc-statusbar count===0）；
  //   ② 「我的」页必须有**可达的余额入口**，且点入 /billing 后真渲染出余额面板
  //      （不只是「有个按钮」——点进去要真能看到余额读数，否则把「入口在、读数为空」判成通过）。
  //   `.ss-quick button` 首个即余额入口（AccountPanel 的快捷入口顺序＝余额/邀请/套餐）。
  await page.goto(`${BASE}/my`, { waitUntil: 'networkidle' });
  const balanceEntry = page.locator('.ss-quick button').first();
  await expect(balanceEntry, '「我的」页缺少余额快捷入口（F-48 新载体）').toBeVisible({ timeout: 30000 });
  await balanceEntry.click();
  await expect(page, '余额入口未跳到 /billing').toHaveURL(/\/billing/);
  // 余额面板真渲染出读数（.ss-row 的 <b> 值），failure 信息点明是余额接口没回还是面板没挂
  await expect
    .poll(async () => (await page.locator('.ssc-card .ss-row b').count()), { message: '/billing 未渲染余额读数：myPackage 没回或面板没挂', timeout: 30000 })
    .toBeGreaterThan(0);
  await page.screenshot({ path: 'artifacts/mobile-my-balance.png' });
  // 溢出复查一次：切到我的/余额后不应引入横向滚动
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2),
      { message: '「我的」/余额页不得引入横向溢出', timeout: 8000 })
      .toBe(false);
});

// 移动端全站巡检：登录后依次访问自服务/工单/对照编辑各页 + 公开定价页，逐页断言无水平溢出
test('移动端全站页面无横向溢出巡检', async ({ page }) => {
  // ★ flaky 根治（2026-09-16 D4）：本用例需登录 + 逐条 networkidle 访问 6 个受保护路由
  //   + 公开定价页，累计真实耗时贴近默认 30s 上限，故首轮偶发超时（靠 retries=1 兜过）。
  //   显式抬高本用例超时到 90s，消除边界性 flaky（非产品缺陷，纯测试稳定性）。
  test.setTimeout(90000);
  await login(page, 'home');
  // ★ 批3（2026-10-10）：S 档（≤640）桌面顶栏 .app-header 整块不渲染（与移动壳互斥），
  //   旧锚点会永远等一个不会出现的元素而假红；巡检只等移动顶栏就位即可。
  await page.waitForSelector('.lc-mob-topbar', { timeout: 30000 });
  await page.waitForTimeout(1000);
  const routes = ['/billing', '/invites', '/packages', '/my', '/tickets', '/editor'];
  for (const r of routes) {
    await page.goto(`${BASE}${r}`, { waitUntil: 'networkidle' });
    // 溢出检测改为轮询至「无溢出」自动收敛，替代固定 1200ms 盲等（渲染慢时更稳）
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2),
        { message: `${r} 不应横向溢出`, timeout: 8000 })
      .toBe(false);
  }
  // 公开定价页
  await page.goto(`${BASE}/pricing`, { waitUntil: 'networkidle' });
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2),
      { message: '/pricing 不应横向溢出', timeout: 8000 })
    .toBe(false);
});

// 未登录门面移动壳（PublicShell）：★ 2026-10-17 用户令「未登录的移动端也做一下适配，
//   参考现在的登录态移动端设计」——四支未登录门面（首页/定价/比价/登录注册）统一套上登录态
//   前台外壳那套「顶栏(汉堡+品牌簇) ＋ 汉堡抽屉」形态；移动档由 publicshell.css 收起各页自带营销头部。
//   判据六腿：① 顶栏在位且高=令牌档(52)＋品牌簇非空；② ★ 2026-10-17 用户令「小屏幕把 language 选择器
//   和登录按钮拿出来放右上角」——顶栏右簇(.pub-topbar-actions)必须可见，语言钮可见文字逐字 = 'Lang'
//   （用 toHaveText 而非子串：'Lang' 是 'Language' 的子串，子串判据对旧长词恒真＝空转），登录链
//   (.pub-topbar-login → /login)在位，且**抽屉里不能再有第二份**（搬过去≠两处并存）；
//   ③ 点汉堡出抽屉＋遮罩、抽屉内跨页入口逐条可见、点遮罩收起；④ 各页自带头部**仍在 DOM**（页面真渲染）
//   但移动档 `:visible` 清零——两条一起才证明是「壳把页头收起」而不是「页头压根没渲染」；
//   ⑤ 无横向溢出；⑥ 桌面负向：1440 档不得出未登录移动顶栏（证明整个壳是移动档门禁的），页面自带头部须在；
//   ⑦ ★ 2026-10-17 用户报「两个按钮样式不统一」补：右簇两枚入口运行时**逐维同档**（高/圆角/内垫/字档/描边宽/行高）。
test('未登录门面移动壳：顶栏＋汉堡抽屉＋自带头部收起＋无溢出', async ({ page }) => {
  test.setTimeout(90000);
  // [路由, 该页自带的营销头部类名]——三支公开门面（登录页在末段单独判）
  const surfaces: Array<[string, string]> = [['/', '.lc-mkt-nav'], ['/pricing', '.lc-prc-nav'], ['/compare', '.lc-cmp-nav']];
  for (const [r, headCls] of surfaces) {
    await page.goto(`${BASE}${r}`, { waitUntil: 'networkidle' });
    // ① 顶栏在位＋高度等值＋品牌簇非空
    const topbar = page.locator('.pub-mob-topbar');
    await expect(topbar, `${r} 未登录门面缺移动顶栏（PublicShell 未生效）`).toBeVisible({ timeout: 30000 });
    expect(await topbar.evaluate((el) => el.getBoundingClientRect().height), `${r} 顶栏高 ≠ --lc-mob-topbar-h(52)`).toBeCloseTo(52, 0);
    expect((await page.locator('.pub-mob-brand').innerText()).trim().length, `${r} 顶栏品牌簇为空`).toBeGreaterThan(0);

    // ② 顶栏右簇：语言钮（固定词 Lang，不随界面语言变）＋ 登录链必须在右上角，抽屉里不许再有第二份
    await expect(page.locator('.pub-mob-topbar .pub-topbar-actions'), `${r} 顶栏右簇不在（语言/登录入口没搬到右上角）`).toBeVisible();
    await expect(page.locator('.pub-mob-topbar .pub-topbar-actions .lang-sel-cur'), `${r} 顶栏语言钮可见文字不是逐字 'Lang'`).toHaveText('Lang');
    const loginBtn = page.locator('.pub-topbar-login');
    await expect(loginBtn, `${r} 顶栏缺登录入口`).toBeVisible();
    expect(await loginBtn.getAttribute('href'), `${r} 顶栏登录入口不指向 /login`).toBe('/login');
    expect(await page.locator('.pub-drawer .lang-sel-btn').count(), `${r} 抽屉里还留着语言钮（应只在顶栏）`).toBe(0);

    // ②b ★ 2026-10-17 用户报「首页两个按钮组件样式都不统一」：右簇两枚入口必须**同档**——
    //   源码侧由 PublicShell.lock L6 锁"写在同一条规则里"，这一腿是运行时侧：
    //   逐维比对两枚的**计算样式**（高/圆角/内垫/字档/描边宽/行高），任一维不等即红。
    //   ⚠️ 只比两枚彼此，不与源码字面值比：Chrome 的 CSSOM 把 1.2px 亚像素描边按设备像素取整上报
    //   （dpr=1/2.5 实测都回 1px，见 AGENTS §一·5 〇-P 那条），拿字面值当运行时返回值会恒红。
    //   宽度刻意不比：语言钮多一枚地球图标，宽度本来就不等（那不是"样式不统一"）。
    const geo = (sel: string) => page.locator(sel).evaluate((el) => {
      const cs = getComputedStyle(el); const r = el.getBoundingClientRect()
      return { h: Math.round(r.height), radius: cs.borderRadius, pad: cs.paddingLeft + '/' + cs.paddingRight,
        font: cs.fontSize + '/' + cs.fontWeight, bw: cs.borderTopWidth, lh: cs.lineHeight }
    })
    const gLang = await geo('.pub-topbar-actions .lang-sel-btn')
    const gLogin = await geo('.pub-topbar-login')
    expect(gLang, `${r} 右簇两枚入口样式不统一：语言钮 ${JSON.stringify(gLang)} vs 登录钮 ${JSON.stringify(gLogin)}`).toEqual(gLogin);

    // ③ 各页自带头部：必须仍在 DOM（页面真渲染了），但移动档不可见（壳把它收起）
    const head = page.locator(headCls);
    await expect
      .poll(() => head.count(), { message: `${r} 的页头没渲染（页面本身没起来，本腿没有射程）`, timeout: 20000 })
      .toBeGreaterThan(0);
    expect(await page.locator(`${headCls}:visible`).count(), `${r} 移动档页头未收起（壳顶栏＋页头两条栏）`).toBe(0);

    // ③ 汉堡 → 抽屉滑入＋遮罩；抽屉内跨页入口逐条可见；点遮罩收起
    const drawer = page.locator('.pub-drawer');
    expect(await drawer.evaluate((el) => el.getBoundingClientRect().x), `${r} 抽屉默认必须移出屏外`).toBeLessThan(0);
    await page.locator('.pub-mob-topbar .lc-mob-topbar__icon').click();
    await page.waitForTimeout(400);
    expect(await drawer.evaluate((el) => el.getBoundingClientRect().x), `${r} 点汉堡后抽屉未滑入`).toBeGreaterThanOrEqual(0);
    await expect(page.locator('.pub-scrim'), `${r} 抽屉打开时必须出现遮罩`).toBeVisible();
    for (const href of ['/pricing', '/compare']) {
      await expect(page.locator(`.pub-drawer a[href="${href}"]`), `${r} 抽屉缺跨页入口 ${href}`).toBeVisible();
    }
    await expect(page.locator('.pub-drawer__cta'), `${r} 抽屉缺底部免费试用 CTA`).toBeVisible();
    await page.locator('.pub-scrim').click({ position: { x: 380, y: 400 } });
    await page.waitForTimeout(400);
    expect(await drawer.evaluate((el) => el.getBoundingClientRect().x), `${r} 点遮罩后抽屉未收起`).toBeLessThan(0);

    // ⑤ 无横向溢出
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2),
        { message: `${r} 未登录门面移动档横向溢出`, timeout: 8000 })
      .toBe(false);
  }

  // 登录页：同壳；固定定位的 .lc-auth-bg 必须下移到顶栏之下（否则卡片被顶栏压住）
  await page.goto(`${BASE}/login`, { waitUntil: 'networkidle' });
  await expect(page.locator('.pub-mob-topbar'), '/login 未登录门面缺移动顶栏').toBeVisible({ timeout: 30000 });
  const bgTop = await page.locator('.lc-auth-bg').first().evaluate((el) => el.getBoundingClientRect().top);
  expect(bgTop, '/login 固定壳未按顶栏高度让位（登录卡会被顶栏压住）').toBeGreaterThanOrEqual(50);
  await expect(page.locator('.lc-auth-card'), '/login 未渲染登录卡').toBeVisible({ timeout: 30000 });
  await page.screenshot({ path: 'artifacts/mobile-public-login.png' });

  // ⑥ 桌面负向：1440 档不得出未登录移动顶栏（壳是移动档门禁的），且页面自带头部必须回来
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${BASE}/pricing`, { waitUntil: 'networkidle' });
  expect(await page.locator('.pub-mob-topbar').count(), '桌面档渲染了未登录移动顶栏（壳没按断点收门）').toBe(0);
  await expect(page.locator('.lc-prc-nav'), '桌面档定价页自带头部不见了（被壳误伤）').toBeVisible({ timeout: 20000 });
  await page.screenshot({ path: 'artifacts/desktop-public-pricing.png' });
});
