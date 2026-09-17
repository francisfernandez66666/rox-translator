import { test, expect } from '@playwright/test';

// ============================================================================
// e2e-manual/assist_admin_probe.spec.ts — AI 助手管理台生产探针（手工运行，不进发布闸门）
// 背景（2026-09-17）：原 e2e/_tmp_admin.spec.ts 写死生产站地址且依赖仅手工注入的
// ASSIST_TOK，放进发布闸门后「必红」钝化回归敏感度——已移出 testDir（playwright
// testDir=./e2e 不含本目录）。手工运行方式：
//   ASSIST_TOK=<真实Token> TARGET=https://langcross.lexicorn.cn \
//   npx playwright test e2e-manual/assist_admin_probe.spec.ts --config playwright.config.ts
// 注意：本 spec 需覆盖 baseURL，故手工指定 --config 或用 BASE_URL 环境变量指向目标站。
// ============================================================================
const TARGET = process.env.BASE_URL || 'https://langcross.lexicorn.cn';

test.skip(!process.env.ASSIST_TOK, '手工探针：需 ASSIST_TOK 环境变量（见文件头注释），不进发布闸门');

test('admin console: bad token guidance then good token full render', async ({ page }) => {
  await page.goto(`${TARGET}/assist-api/assist/admin`);
  // 场景1：错误 token → 红色徽标「Token 不匹配」  await page.evaluate(() => localStorage.setItem('assist_tok', 'wrong-token-abc'));
  await page.reload();
  await expect(page.getByText('Token 不匹配').first()).toBeVisible({ timeout: 8000 });
  console.log('BAD_TOKEN_BADGE=OK');

  // 场景2：正确 token → 绿「已验证」→ 配置页 LLM 卡渲染
  await page.evaluate((tk) => { localStorage.setItem('assist_tok', tk); }, process.env.ASSIST_TOK!);
  await page.reload();
  await page.waitForTimeout(1500);
  await expect(page.getByText('Token 已验证').first()).toBeVisible({ timeout: 8000 });
  console.log('GOOD_TOKEN_BADGE=OK');
  await page.getByText('⚙️ 配置').click();
  await page.waitForTimeout(1200);
  await expect(page.getByText('🤖 LLM 接入').first()).toBeVisible();
  await expect(page.getByText('规则模式').first()).toBeVisible();
  console.log('CONFIG_PANEL=OK');
});
