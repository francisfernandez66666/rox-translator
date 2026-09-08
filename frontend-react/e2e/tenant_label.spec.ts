// ============================================================================
// e2e/tenant_label.spec.ts — 问题1专项冒烟：顶栏租户名展示（前端渲染验证）
// 修复点回归：前端不再只显示平台品牌（能言/翻译平台），而是叠加显示用户所属租户名。
//   - 企业用户 corp1 (租户"汽车公司") → 顶栏 Tag 含 "汽车公司"
//   - 个人用户 person1 (租户"小明")   → 顶栏 Tag 含 "小明"
//   - 后台管理面板（非超管）管理范围标签显示其租户名
// 依赖：后端含 handleTenantInviteEnabledGet 返回 tenant_name（dev3.db 联调实例）
// 运行：BASE_URL=http://127.0.0.1:8787 npx playwright test e2e/tenant_label.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8787';

async function login(page: Page, username: string, password: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username, password } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  // 构建产物下模块首次加载即读 localStorage，addInitScript 在导航前注入 token 即生效
  // （vite dev 的 304 模块缓存会让 core.ts 在注入前执行，故 dev 环境需 cache:'reload' 兜底）
  await page.addInitScript((tk) => localStorage.setItem('auth_token', tk), body.token);
  await page.goto(`${BASE}/`);
}

// 顶栏租户 Tag：myTenantName 渲染成 <Tag title={tenantName}>{tenantName}</Tag>
// 选择器：header 内带 title 且文本非空的小标签（避开品牌 span 的 alt/title）
async function headerTenantTag(page: Page) {
  const tag = page.locator('header span[title]').filter({ hasText: /[\u4e00-\u9fa5]/ }).first();
  await expect(tag).toBeVisible();
  return (await tag.getAttribute('title')) || '';
}

test.describe('问题1 顶栏租户名展示', () => {
  test('企业用户顶栏显示其租户名（汽车公司）', async ({ page }) => {
    await login(page, 'corp1', 'uatpass123');
    // login 内已 goto 首页，直接等租户 Tag 出现
    await expect(page.locator('header span[title]').filter({ hasText: /[\u4e00-\u9fa5]/ }).first()).toBeVisible({ timeout: 15000 });
    expect(await headerTenantTag(page)).toBe('汽车公司');
    await page.screenshot({ path: 'artifacts/tenant_label_corp.png' });
  });

  test('个人用户顶栏显示其租户名（小明）', async ({ page }) => {
    await login(page, 'person1', 'uatpass123');
    await expect(page.locator('header span[title]').filter({ hasText: /[\u4e00-\u9fa5]/ }).first()).toBeVisible({ timeout: 15000 });
    expect(await headerTenantTag(page)).toBe('小明');
    await page.screenshot({ path: 'artifacts/tenant_label_personal.png' });
  });

  test('后台管理面板非超管显示其租户管理范围', async ({ page }) => {
    await login(page, 'corp1', 'uatpass123');
    await page.goto(`${BASE}/admin`);
    await expect(page.locator('body')).toContainText('汽车公司', { timeout: 15000 });
    await page.screenshot({ path: 'artifacts/tenant_label_admin.png' });
  });
});
