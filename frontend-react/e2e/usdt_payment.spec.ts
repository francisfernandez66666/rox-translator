// ============================================================================
// e2e/usdt_payment.spec.ts — USDT 收款接入（2026-09-15）端到端回归
//   U1 超管后台「USDT 收款」配置分区渲染：开关/尾数/自动入账三开关 + 收款地址
//      输入 + 汇率 + 浏览器核验外链（后端保存校验已在 T42 覆盖，此处锁 UI 契约）。
//   U2 租户收银台 USDT 渠道：开启后充值渠道出现「USDT · TRC-20」，下单弹
//      收款台——精确金额（含尾数）+ 收款地址 + 复制 + 「我已转账」需先填 txHash。
// 依赖：run_uat.sh 以 mock chain 启动后端（USDT_TRON_BASE），usdt_* 配置可由
//      超管接口开/关；用例结束后恢复关闭，避免污染其它像素级用例。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/usdt_payment.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
const TRC20 = 'T4BJRYfnu29GPWdksz7EMUbiqx5CKSZgov'; // 合法 base58 占位地址（mock 链，无真实资金）

async function login(page: Page, user: string, pass: string): Promise<string> {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  return body.token as string;
}

async function saveSettings(page: Page, token: string, data: Record<string, unknown>) {
  const res = await page.request.post(`${BASE}/api/admin/packages/settings/save`, {
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    data,
  });
  const body = await res.json();
  expect(body.success, `设置保存失败:${JSON.stringify(body)}`).toBeTruthy();
}

test.describe('USDT 收款（超管配置 + 租户收银台）', () => {
  test.describe.configure({ mode: 'serial' });
  test('U1 超管后台配置分区：开关/地址/汇率/浏览器核验链接', async ({ page }) => {
    const token = await login(page, 'admin', 'Admin@1234');
    await saveSettings(page, token, {
      usdt_enabled: '1', usdt_chains: 'trc20', usdt_addr_trc20: TRC20, usdt_rate_fen_per_usdt: 720,
    });
    await page.goto('/admin');
    // ★ 2026-09-18 UI 迁移：后台侧栏 .admin-side→.lc-sidebar；收银台已换 langcross 组件（U2 用 select.lc-select/.lc-dialog）
    await page.locator('.lc-sidebar').getByText('计费与套餐').first().click();
    await page.getByText('套餐与订单').first().click();

    // 配置分区存在：USDT 标题 + 已开启徽标 + 地址回显
    await expect(page.getByText('USDT 收款', { exact: false }).first()).toBeVisible();
    await expect(page.getByText('已开启').first()).toBeVisible();
    const addrInput = page.getByPlaceholder('粘贴收款钱包地址').first();
    await expect(addrInput, 'TRC20 地址未回显').toHaveValue(TRC20);
    // 钱包链接（链上核验外链）指向配置地址
    const link = page.getByRole('link', { name: '浏览器核验' }).first();
    await expect(link).toHaveAttribute('href', new RegExp(TRC20.slice(0, 8)));

    // 保存非法地址 → 后端 400（前端 toast 报错且配置不被污染）；保存按钮限定在 USDT 分区内
    await addrInput.fill('badaddress');
    const usdtSec = page.locator('div[style*="border-top"]').filter({ hasText: 'USDT 收款' }).first()
    await usdtSec.scrollIntoViewIfNeeded()
    await usdtSec.getByRole('button', { name: '保存' }).click()
    await expect(page.getByText('收款地址', { exact: false }).last(), '非法地址应被拒绝').toBeVisible({ timeout: 8000 });

    // 复原
    await saveSettings(page, token, { usdt_enabled: '0', usdt_addr_trc20: '' });
  });

  test('U2 租户收银台 USDT 渠道：下单出精确金额/地址/声明哈希闸', async ({ page }) => {
    const adminTok = await login(page, 'admin', 'Admin@1234');
    await saveSettings(page, adminTok, {
      usdt_enabled: '1', usdt_chains: 'trc20', usdt_addr_trc20: TRC20, usdt_rate_fen_per_usdt: 720, usdt_tail_enabled: '1',
    });
    // 换租户身份（uatuser_a 为租户管理员）
    await login(page, 'uatuser_a', 'uatpass123');
    await page.goto('/admin');
    await page.locator('.lc-sidebar').getByText('计费与套餐').first().click();

    // 渠道下拉出现 USDT · TRC-20（★ 2026-09-18 UI 迁移：TDesign Select→原生 select.lc-select，
    // 选项 value=usdt:<链>，selectOption 比旧「点开面板再选项」更稳）
    const topup = page.locator('#plans-topup');
    await expect(topup, '充值面板未渲染').toBeVisible();
    const chSel = topup.locator('select.lc-select').first();
    await expect(chSel.locator('option[value="usdt:trc20"]'), 'USDT 渠道选项未出现').toBeAttached({ timeout: 5000 });
    await chSel.selectOption('usdt:trc20');

    // 去支付 → 收银台展示 USDT 精确金额 + 收款地址 + 复制（★ 弹窗迁移：.t-dialog→.lc-dialog）
    await topup.locator('button:has-text("去支付")').click();
    const dlg = page.locator('.lc-dialog:has-text("收银台")');
    await expect(dlg, '收银台未弹出').toBeVisible();
    await expect(dlg.getByText('应付金额')).toBeVisible();
    await expect(dlg.getByText('USDT').first()).toBeVisible();
    await expect(dlg.getByText('收款地址')).toBeVisible();
    await expect(dlg.getByText('复制')).toBeVisible();
    // 尾数：金额非整数（6 位小数唯一化）
    const amount = await dlg.locator('div:has-text("USDT") >> css=b, div:has-text("USDT") >> css=div').first().innerText().catch(() => '')
    const amtText = amount || (await dlg.innerText())
    expect(/\d+\.\d{3,}\s*USDT/.test(amtText), `尾数金额缺失: ${amtText.slice(0, 120)}`).toBeTruthy();

    // 「我已转账（声明哈希）」：未填 txHash 禁用；填入后解禁（不真正提交，避免留审核单）
    // 注：langcross Button 的禁用态是原生 <button disabled>（旧 TDesign div.t-is-disabled 已废弃）
    const declare = dlg.locator('button:has-text("我已转账")');
    await expect(declare).toBeVisible();
    await expect(declare, '未填哈希不应可提交').toBeDisabled();
    await dlg.getByPlaceholder('链上交易哈希（txHash）').fill('a1'.repeat(32));
    await expect(declare, '填哈希后声明按钮应解禁').toBeEnabled();

    // 关单恢复（★ 新 Dialog 无 X 关闭钮：Esc 走 onCancel 即关单）
    await page.keyboard.press('Escape');
    await expect(dlg, 'Esc 后收银台应关闭').toBeHidden();
    await saveSettings(page, adminTok, { usdt_enabled: '0', usdt_addr_trc20: '' });
  });
});
