import { test, expect } from '@playwright/test';

// ============================================================================
// e2e/landing_i18n.spec.ts — 落地页多语言入口回归（★ 2026-09-20 用户反馈④）
// 锁定两条行为：
//   ① 落地页顶栏挂 12 语种 LangSelect，切换到 English 后整页文案转英文且刷新持久；
//   ② 首次访问（无 app_lang）按浏览器语言自动选语种（fr-FR → Français），
//      非中文访客不再被默认扣在中文页上。
// 运行：随 run_uat Playwright 矩阵或本地 npx playwright test e2e/landing_i18n.spec.ts
// ============================================================================

test.describe('落地页多语言（反馈④）', () => {
  test('顶栏语言切换：中文态 → 选 English 整页转英文，刷新保持', async ({ page }) => {
    await page.goto('/');
    const btn = page.locator('header .lang-sel-btn');
    await expect(btn).toBeVisible();
    await expect(btn).toContainText('简体中文');
    await btn.click();
    await page.getByRole('option', { name: 'English' }).click();
    await expect(page.locator('.lc-nav-cta')).toContainText('Start free');
    await expect(page.getByText('免费试用')).toHaveCount(0);
    // 刷新后仍英文（app_lang 持久化，不会退回浏览器/默认语种）
    await page.reload();
    await expect(page.locator('header .lang-sel-btn')).toContainText('English');
  });
});

test.describe('首次访问浏览器语言自动检测', () => {
  // locale 覆盖 config 的 zh-CN 钉底，模拟法国新访客（上下文 localStorage 天然为空）
  test.use({ locale: 'fr-FR' });
  test('fr-FR → Français + 英文回退落地页', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('header .lang-sel-btn')).toContainText('Français');
    // land.* 长尾键走 lang→en→zh 回退：法语访客看到英文营销文案而非中文
    // （'Start free' 在落地页出现多处——顶栏/收尾 CTA/页脚，strict 模式取首个即可）
    await expect(page.locator('.lc-nav-cta')).toContainText('Start free');
    await expect(page.getByText('免费试用')).toHaveCount(0);
  });
});
