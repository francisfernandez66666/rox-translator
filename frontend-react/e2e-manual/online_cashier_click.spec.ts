// ============================================================================
// e2e-manual/online_cashier_click.spec.ts — 线上收银台「真机点一单（不付款）」留档（★ 2026-09-27 批 〇-V #34）
//
// 要验什么：static_qr ＋ USDT 两条真实收款链在**生产前端**上从「选渠道 → 下单 → 出收款台」
// 这一段是否顺。〇-U 发版后两站 pay_mode 已是非 mock（发布红线），此后线上客户看到的收银台
// 就是这里点的那一单——mock 链路的 UAT（usdt_payment.spec.ts U2、PlansP.dom 用例）都在本地实例上跑，
// 拿不到「生产配置态＋生产收款码图片＋生产汇率」这三件同时成立时的真实形态。
//
// 为何必须手工（AGENTS.md §一·6）：本用例会**在生产库里落下真实的 pending 订单**，
// 且需要一个可登录的账号。发布闸门里既不碰生产库、也没有生产账号，因此放 e2e-manual ＋ env 守卫，
// 缺 env 时整文件 skip，永不拖红。
//
// ★ 安全红线（不可放宽）：
//   1. 口令一律走环境变量注入，禁止写进本文件或任何提交物；
//   2. 本用例**只下单、绝不付款、也绝不点「我已付费 / 我已转账（声明哈希）」**——
//      点了声明就是把单送进人工核账队列，等于给平台造一条假到账申请；
//   3. 探针账号与它产生的 pending 单在同一次执行账里清理（取消订单 ＋ 删除账号），
//      留证据截图，不留测试数据在客户视野里。
//
// 运行（口令从环境注入，例：把值放进当前 shell 而不出现在命令行历史/文件里）：
//   CASHIER_BASE=https://langcross.lexicorn.cn \
//   CASHIER_USER=<探针账号> CASHIER_PASS=<探针口令> CASHIER_POINTS=100 \
//     npx playwright test -c playwright.manual.config.ts e2e-manual/online_cashier_click.spec.ts
//   截图落 CASHIER_ARTIFACTS_DIR（默认 frontend-react/artifacts/cashier_0v/）。
// ============================================================================
import { test, expect, Page } from '@playwright/test';
import fs from 'fs';
import path from 'path';

// 被测站点根地址：由环境变量决定，禁止在文件里写死生产/演示域名（AGENTS §一·6 e2e 红线）
const BASE = (process.env.CASHIER_BASE || '').replace(/\/+$/, '');
// 收银探针账号名：只从环境变量注入，绝不落进本文件、日志或任何提交物
const USER = process.env.CASHIER_USER || '';
// 探针账号口令：同上；BASE/USER/PASS 任一缺失即整档 skip（见下方 test.skip），永不拖发布闸门
const PASS = process.env.CASHIER_PASS || '';
// 下单积分额度：默认 100，够走通两种收款形态又不动大额（只点单不付款）
const POINTS = process.env.CASHIER_POINTS || '100';
// 实拍截图落盘目录：跑完把这两张 png 挪进《发布前E2E_UAT_20260926/证据/》归档（该目录永久排除在推送之外）
const SHOT_DIR = process.env.CASHIER_ARTIFACTS_DIR || path.resolve('artifacts/cashier_0v');

test.skip(!BASE || !USER || !PASS, '手工留档：需 CASHIER_BASE + CASHIER_USER + CASHIER_PASS（真库落单＋真实收款配置），不进发布闸门');

/** 登录并把会话塞进 sessionStorage（与 e2e/usdt_payment.spec.ts 同口径：不打登录页表单，省一次限流预算）。 */
async function login(page: Page): Promise<void> {
  // ★ 首访语言自动检测（AGENTS §一·5）：本配置无 locale 钉底，Playwright 默认 en-US 会把界面翻成英文，
  //   下面全部中文文案断言即结构性必红。手工探针要的是「客户中文视角的那一单」，故先钉 app_lang=zh。
  await page.addInitScript(() => localStorage.setItem('app_lang', 'zh'));
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: USER, password: PASS } });
  const body = await res.json().catch(() => ({} as Record<string, unknown>));
  expect(Boolean(body.success), `登录未成功（HTTP ${res.status()}）`).toBe(true);
  expect(typeof body.token === 'string' && (body.token as string).length > 0, '登录未回令牌').toBe(true);
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk as string), body.token as string);
}

/** 进「计费与套餐」面板并等充值卡渲染；返回 #plans-topup 定位器。 */
async function openCashier(page: Page) {
  await page.goto(`${BASE}/admin`);
  await page.locator('.lc-sidebar').getByText('计费与套餐').first().click();
  const topup = page.locator('#plans-topup');
  await expect(topup, '充值面板未渲染 ⇒ 当前账号无租户计费视角或页面链路没通').toBeVisible({ timeout: 15000 });
  return topup;
}

/** 选渠道 → 填积数 → 点「去支付」，并断言收款弹窗真的开了（链路可达探针：弹窗没开就是链路没通，不留兜底假绿）。 */
async function createOrder(topup: ReturnType<Page['locator']>, page: Page, channelValue: string) {
  const chSel = topup.locator('select.lc-select').first();
  await expect(chSel.locator(`option[value="${channelValue}"]`), `渠道选项未出现：${channelValue}`).toBeAttached({ timeout: 8000 });
  await chSel.selectOption(channelValue);
  await topup.locator(`input.lc-input[type="number"]`).fill(POINTS);
  await topup.getByRole('button', { name: '去支付' }).click();
  const dlg = page.locator('.t-dialog').last();
  await expect(dlg.getByText('收银台'), '下单后收款弹窗未出现').toBeVisible({ timeout: 15000 });
  return dlg;
}

test.describe('线上收银台真机点单（不付款）', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(() => {
    fs.mkdirSync(SHOT_DIR, { recursive: true });
  });

  test('C1 静态收款码渠道：出单即停手，不点「我已付费」', async ({ page }) => {
    await login(page);
    const topup = await openCashier(page);
    const dlg = await createOrder(topup, page, 'manual');

    // 收款台形态：静态收款码提示语 + 真实图片（不是兜底 <pre> 文本），且订单号已下发
    await expect(dlg.getByText('请扫描下方收款码完成支付')).toBeVisible();
    const img = dlg.locator('img');
    await expect(img.first(), '收款码图片未渲染 ⇒ 超管 static_qr_image 配置到不了收银台').toBeVisible();
    const orderLine = topup.locator('p', { hasText: '当前订单' });
    await expect(orderLine, '下单后收银台未回显本单（订单号/应收）').toBeVisible({ timeout: 10000 });
    const orderText = (await orderLine.innerText()).trim();
    // 结构性判据：订单号非空、金额是正数（¥ 后跟数字），不对措辞做判定
    expect(/当前订单\s+(\S+)：待支付/.test(orderText), `订单回显缺订单号：${orderText}`).toBe(true);
    expect(/应收 ¥[1-9][0-9]*\.\d{2}/.test(orderText.replace(/\s+/g, ' ')), `应收金额异常：${orderText}`).toBe(true);
    // 反向锁：mock 通道在生产 static_qr 模式下不得出现在渠道下拉（F-09 红线）
    await expect(topup.locator('select.lc-select').first().locator('option[value="mock"]'), '生产收银台露出测试后门').toHaveCount(0);

    await page.screenshot({ path: path.join(SHOT_DIR, 'C1_static_qr_checkout.png'), fullPage: false });
    // ★ 不付款、不声明：只关窗。「我已付费」存在即证明人工确认闸在位，但一次都不点。
    await expect(dlg.getByRole('button', { name: '我已付费' })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.locator('.t-dialog').last()).toBeHidden({ timeout: 10000 });
  });

  test('C2 USDT(TRC-20) 渠道：出精确金额与地址，声明哈希闸在位但不声明', async ({ page }) => {
    await login(page);
    const topup = await openCashier(page);
    const dlg = await createOrder(topup, page, 'usdt:trc20');

    await expect(dlg.getByText('应付金额')).toBeVisible();
    const usdtAmount = dlg.locator('div', { hasText: /USDT$/ }).first();
    expect(await usdtAmount.innerText(), 'USDT 应收金额未渲染').toMatch(/[0-9.]+\s*USDT/);
    await expect(dlg.getByText('收款地址')).toBeVisible();
    const addr = await dlg.locator('code').first().innerText();
    // 生产收款地址必须是 TRON base58（T 开头、非空、非占位），否则说明超管配置链没通
    expect(/^T[1-9A-HJ-NP-Za-km-z]{33}$/.test(addr.trim()), `USDT 收款地址形态异常：${addr.trim()}`).toBe(true);
    await expect(dlg.locator('img[alt="usdt-qr"]'), 'USDT 支付二维码未渲染').toBeVisible();

    // 声明闸负向证明：哈希未填时「我已转账」必须禁用（F-67 三视图锁的线上版）
    const declare = dlg.getByRole('button', { name: '我已转账（声明哈希）' });
    await expect(declare).toBeVisible();
    await expect(declare, '声明闸失效：未填 txHash 就能声明').toBeDisabled();

    await page.screenshot({ path: path.join(SHOT_DIR, 'C2_usdt_checkout.png'), fullPage: false });
    // ★ 同样不付款、不声明、也不往哈希输入框里填任何值（填了才有机会误点）
    await page.keyboard.press('Escape');
    await expect(page.locator('.t-dialog').last()).toBeHidden({ timeout: 10000 });
  });
});
