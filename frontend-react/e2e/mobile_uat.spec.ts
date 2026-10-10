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
  // 账号 + 密码输入框（注册表单亦有同类输入框，登录卡先渲染）
  const inputs = page.locator('.lc-auth-card input').first();
  await inputs.fill(user);
  await page.locator('.lc-auth-card input[type="password"]').first().fill(pass);
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

// 移动端工作台核对：★ 批3（2026-10-10）S 档（≤640）前台外壳换成「移动三件套」——
//   .lc-statusbar(44) + .lc-mob-topbar(52) + 底部胶囊 .lc-tabbar，桌面顶栏 .app-header
//   在 S 档**整块不渲染**（互斥，永不同屏）。因此本用例的形态锁全部按新载体重钉：
//   ① 三件套存在且高度=令牌档（--lc-statusbar-h 44 / --lc-mob-topbar-h 52，等值锁）；
//   ② 顶栏子元素无纵向重叠（每个孩子的 rect 必须落在顶栏盒内——mobile.css 旧
//      `height:38px`+flex-wrap 自相矛盾导致第二行被裁/重叠，就是这条锁要抓的形态）；
//   ③ 底部 TabBar 四钮触点 ≥44px（§1.7 触点档）；
//   ④ F-48 余额可见性锁迁到新载体 .lc-mob-band（旧锚点 .app-header .pkg-line-tag 在
//      S 档结构性不存在——沿用旧锚点会让这条锁永远等一个不会出现的元素而假红）。
test('移动端工作台：三件套形态锁＋顶栏无纵向重叠＋TabBar 触点≥44＋余额带可见', async ({ page }) => {
  // 登录口径沿用 O-9 复跑账：余额载体的真用户是**租户计费用户**（超管平台上下文余额恒空），
  // 故用 uatuser_a（与 pixel/translate_flow 同口径）。
  await login(page, 'home', process.env.UAT_USER || 'uatuser_a', process.env.UAT_PASS || 'uatpass123');
  await page.waitForSelector('.lc-mob-topbar', { timeout: 30000 });
  await page.waitForTimeout(1500);

  // ---- ① 三件套存在＋高度等值（44/52，与 tokens.css --lc-statusbar-h/--lc-mob-topbar-h 逐像素对表）----
  const statusbar = page.locator('.lc-statusbar');
  const topbar = page.locator('.lc-mob-topbar');
  const tabbar = page.locator('.lc-tabbar');
  await expect(statusbar, 'S 档必须渲染状态栏').toBeVisible();
  await expect(topbar, 'S 档必须渲染移动顶栏').toBeVisible();
  await expect(tabbar, 'S 档必须渲染底部胶囊 TabBar').toBeVisible();
  const sbH = await statusbar.evaluate((el) => el.getBoundingClientRect().height);
  const tbH = await topbar.evaluate((el) => el.getBoundingClientRect().height);
  expect(sbH, '状态栏高 ≠ --lc-statusbar-h(44)').toBeCloseTo(44, 0);
  expect(tbH, '移动顶栏高 ≠ --lc-mob-topbar-h(52)').toBeCloseTo(52, 0);

  // ---- 互斥锁：S 档桌面顶栏不得同屏渲染（出现＝媒体查询/useState 判档被破坏）----
  expect(await page.locator('.app-header').count(), 'S 档 .app-header 必须整块不渲染（移动壳互斥）').toBe(0);

  // ---- ② 顶栏子元素无纵向重叠/被裁：每个孩子的 rect 落在顶栏盒内（±0.5px 取整余量）----
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

  // ---- ③ 底部 TabBar 触点 ≥44px（§1.7）：四个钮逐一量 ----
  const items = page.locator('.lc-tabbar .lc-tabbar__item');
  expect(await items.count(), 'TabBar 应有四个入口（翻译/工单/知识库/我的）').toBe(4);
  for (let i = 0; i < 4; i++) {
    const b = await items.nth(i).boundingBox();
    expect(b, `TabBar 第 ${i + 1} 钮未渲染`).not.toBeNull();
    expect(b!.height, `TabBar 第 ${i + 1} 钮触点高 <44`).toBeGreaterThanOrEqual(44);
    expect(b!.width, `TabBar 第 ${i + 1} 钮触点宽 <44`).toBeGreaterThanOrEqual(44);
  }
  // 胶囊整体必须钉在视口底部（距屏底 ≈ --lc-tabbar-bottom 20px，安全区 env 头less=0）
  const tabBox = await tabbar.boundingBox();
  const vpH = page.viewportSize()?.height ?? 844;
  const gapToBottom = vpH - (tabBox!.y + tabBox!.height);
  expect(gapToBottom, 'TabBar 未钉在视口底（距底应 ≈20px 档）').toBeLessThanOrEqual(26);

  // ---- ④ 水平溢出（原锁保留）----
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('工作台水平溢出:', overflow);
  expect(overflow).toBe(false);
  // 输入栏存在（★ 2026-09-18 UI 迁移：原生 textarea 挂 LangCross 皮肤类 .lc-textarea，旧 .chat-inputbar 已废弃）
  await expect(page.locator('.lc-textarea')).toBeVisible();

  // ---- F-48② 余额可见性锁（载体已迁 S 档第二信息带 .lc-mob-band）----
  const band = page.locator('.lc-mob-band');
  // 余额数值在 myPackage 拉到后才渲染，poll 等内容出现（窗口与载入闸门预算同尺取 30s）
  await expect
    .poll(() => band.textContent(), { message: 'S 档余额带未出数值：myPackage 没回？', timeout: 30000 })
    .toMatch(/·\s*\S/);
  await expect(band, '★ F-48：S 档余额口径必须可见（.lc-mob-band）').toBeVisible();
  const bandBox = await band.boundingBox();
  const vpW = page.viewportSize()?.width ?? 390;
  expect(bandBox!.x + bandBox!.width, '余额带右边界越出视口').toBeLessThanOrEqual(vpW + 0.5);
  // 余额数值必须是等宽数字档（fontVariantNumeric:tabular-nums，批3 规格）
  const numEl = band.locator('span').last();
  const numStyle = await numEl.evaluate((el) => ({
    variant: getComputedStyle(el).fontVariantNumeric,
    visible: getComputedStyle(el).display !== 'none',
  }));
  expect(numStyle.visible, '余额数值元素不可见').toBe(true);
  expect(numStyle.variant).toContain('tabular-nums');
  // 溢出复查一次：余额带显示后不应引入横向滚动
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2),
      { message: '余额带显示后不得引入横向溢出', timeout: 8000 })
      .toBe(false);
  await page.screenshot({ path: 'artifacts/mobile-workbench.png' });
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
