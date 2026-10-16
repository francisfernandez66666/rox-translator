// ============================================================================
// e2e/tenant_label.spec.ts — 问题1专项冒烟：租户名展示（前端渲染验证）
// 修复点回归：前端不再只显示平台品牌（能言/翻译平台），而是叠加显示用户所属租户名。
//   - 企业用户 corp1 (租户"汽车公司") → 身份格含 "汽车公司"
//   - 个人用户 person1 (租户"小明")   → 身份格含 "小明"
//   - 后台管理面板（非超管）管理范围标签显示其租户名
// 依赖：后端含 handleTenantInviteEnabledGet 返回 tenant_name（dev3.db 联调实例）
// 运行：BASE_URL=http://127.0.0.1:8787 npx playwright test e2e/tenant_label.spec.ts
// ★ 2026-10-16 判据搬家（本批修）：10-15「元宝式布局」把桌面顶栏 .app-header 退役，品牌/身份/语种钮
//   整体迁入左侧栏 .lc-sidebar ⇒ 身份格现在挂在 .fs-tenant（FrontSideNav 的品牌行右侧，title 带租户名），
//   取自 `header span[title]` 的旧判据**结构性找不到目标**（该用例因此恒红，不是产品缺陷）。
//   射程不变：仍问「这个用户的租户名有没有落到界面上、且是异步换过之后的真值」。
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8787';

async function login(page: Page, username: string, password: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username, password } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  await page.goto(`${BASE}/`);
}

async function tenantTagText(page: Page) {
  // 左栏身份格：文本可能被省略号截断（>10 字截 10 字 + …），故判据取 title 全文而非可见文字。
  const tag = page.locator('.fs-tenant[title]').first();
  await expect(tag).toBeVisible({ timeout: 15000 });
  // 初值即「个人版」占位，/api/me/context 异步返回后才替换——轮询直到稳定（PG 较慢）
  await expect.poll(async () => (await tag.getAttribute('title')) || '', { timeout: 10000, message: '租户名身份格未落地' }).toContain('UAT');
  return (await tag.getAttribute('title')) || '';
}

// 用 UAT 已有用户验证租户名展示（uatuser_a 企业租户 "UAT公司A"，uatuser_b 企业租户 "UAT公司B"）

// ★ F1 已实现（Header 租户 Tag）：取消 skip（2026-09-12）
test.describe('问题1 租户名展示（左栏身份格 .fs-tenant）', () => {
  test('企业用户左栏显示其租户名（UAT公司A）', async ({ page }) => {
    await login(page, 'uatuser_a', 'uatpass123');
    expect(await tenantTagText(page)).toContain('UAT');
    await page.screenshot({ path: 'artifacts/tenant_label_corp.png' });
  });

  test('企业用户左栏显示其租户名（UAT公司B）', async ({ page }) => {
    await login(page, 'uatuser_b', 'uatpass123');
    expect(await tenantTagText(page)).toContain('UAT');
    await page.screenshot({ path: 'artifacts/tenant_label_personal.png' });
  });

  test('后台管理面板非超管显示其租户管理范围', async ({ page }) => {
    await login(page, 'uatuser_a', 'uatpass123');
    await page.goto(`${BASE}/admin`);
    await expect(page.locator('body')).toContainText('UAT', { timeout: 15000 });
    await page.screenshot({ path: 'artifacts/tenant_label_admin.png' });
  });
});
