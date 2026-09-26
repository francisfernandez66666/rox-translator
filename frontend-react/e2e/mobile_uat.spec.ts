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

// 移动端工作台核对：输入栏可见、页面无水平溢出（验证 .chat-input-row 换行生效）
// ＋ ★ F-48（批 I-5 2026-09-26）：窄屏顶栏余额徽标必须**可见**——旧 mobile.css 写
//   `.app-header .pkg-line-tag { display:none }`，把「我还剩多少积分」这个唯一常驻余额口径
//   在手机上整块抹掉（用户得翻进「套餐/账单」二级页），与「余额条不刷新」叠成同一件事。
//   现行口径：显示 + 单行省略号，且长文案不得撑破 390px 视口。
test('移动端工作台：输入栏可换行、无横向溢出、余额徽标窄屏仍可见', async ({ page }) => {
  // ★ 09-27 复跑红账（O-9 × F-48 交界）：本用例原以超管 admin 登录测余额徽标，但 O-9（批 I-10）
  //   已把「平台计费上下文」（超管未切入任何租户，isPlatformBillingContext）的余额胶囊置空——
  //   后端该上下文固定回 0，照渲染就是假「余额 0 积分」+ 误亮「余额不足」横幅。
  //   徽标可见性的真载体是**租户计费用户**，故本用例登录 uatuser_a（与 pixel/translate_flow 同口径）。
  await login(page, 'home', process.env.UAT_USER || 'uatuser_a', process.env.UAT_PASS || 'uatpass123');
  await page.waitForSelector('.cw-dialog, .app-header', { timeout: 30000 });
  await page.waitForTimeout(1500);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
  console.log('工作台水平溢出:', overflow);
  expect(overflow).toBe(false);
  // 输入栏存在（★ 2026-09-18 UI 迁移：原生 textarea 挂 LangCross 皮肤类 .lc-textarea，旧 .chat-inputbar 已废弃）
  await expect(page.locator('.lc-textarea')).toBeVisible();

  // ---- F-48② 余额徽标可见性锁（真机视口，非源码 grep）----
  const pkgTag = page.locator('.app-header .pkg-line-tag');
  // 徽标只在 myPackage 拉到数值后才渲染，故用 poll 等它出现（不盲等固定毫秒）
  // ★ 09-27 复跑红账：首轮冷实例要叠「冷启动载入闸门 ≈9s + 首次资源全冷读」的税，15s 窗会被
  //   吃掉（本轮首跑红、retry 秒过＝同一形态）；窗口与上方页面等待同尺取 30s。
  //   锁语义不变：仍然必须真渲染、真可见，只是把「载入完毕」的判定推迟到闸门预算之外。
  await expect.poll(() => pkgTag.count(), { message: '顶栏余额徽标未渲染：余额接口没回数值？', timeout: 30000 }).toBe(1);
  await expect(pkgTag, '★ F-48：窄屏不得再 display:none 掉余额徽标').toBeVisible();
  const tagStyle = await pkgTag.evaluate((el) => {
    const cs = getComputedStyle(el);
    return { display: cs.display, overflow: cs.overflowX, textOverflow: cs.textOverflow, nowrap: cs.whiteSpace === 'nowrap' };
  });
  console.log('余额徽标计算样式:', tagStyle);
  expect(tagStyle.display, 'display:none/contents 即旧缺陷复活').not.toBe('none');
  // 省略号三件套：overflow:hidden + text-overflow:ellipsis + nowrap（缺一项长文案就撑破视口）
  expect(tagStyle.overflow, 'overflow 必须 hidden，否则 ellipsis 不生效').toBe('hidden');
  expect(tagStyle.textOverflow, 'text-overflow 必须是 ellipsis').toBe('ellipsis');
  expect(tagStyle.nowrap, 'white-space 必须 nowrap（换行会把页眉撑高）').toBe(true);
  const box = await pkgTag.boundingBox();
  expect(box, '徽标未渲染').not.toBeNull();
  const vp = page.viewportSize();
  expect(box!.width, `徽标宽度不得超过视口（${vp?.width}px）`)
    .toBeLessThanOrEqual((vp?.width ?? 390) + 0.5);
  // 徽标必须在视口内（右边界不越界＝真的被截断而不是溢出到屏外）
  expect(box!.x + box!.width, '徽标右边界越出视口').toBeLessThanOrEqual((vp?.width ?? 390) + 0.5);
  // 溢出复查一次：徽标显示后不应引入横向滚动
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2),
      { message: '余额徽标显示后不得引入横向溢出', timeout: 8000 })
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
  await page.waitForSelector('.app-header', { timeout: 30000 });
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
