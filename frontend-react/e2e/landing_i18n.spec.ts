import { test, expect } from '@playwright/test';

// ============================================================================
// e2e/landing_i18n.spec.ts — 落地页多语言入口回归（★ 2026-09-20 用户反馈④）
// 锁定两条行为：
//   ① 落地页顶栏挂 12 语种 LangSelect，切换到 English 后整页文案转英文且刷新持久；
//   ② 首次访问（无 app_lang）按浏览器语言自动选语种（fr-FR → Français），
//      非中文访客不再被默认扣在中文页上。
// ★ 判据翻转说明（用户拍板⑦，非回归，2026-10）：触发钮文字从「当前语种自称名」改为固定词
//   「语言/Language/Langue…」（app.langBtn，随界面语言本地化）——选错语种后外国人读不懂
//   自称名会被钉死在陌生语言里。自称名的正向判据移到菜单展开后的选中项
//   [role=option][aria-selected="true"]（自称名词表一字未动）。
// 运行：随 run_uat Playwright 矩阵或本地 npx playwright test e2e/landing_i18n.spec.ts
// ============================================================================

test.describe('落地页多语言（反馈④）', () => {
  test('顶栏语言切换：中文态 → 选 English 整页转英文，刷新保持', async ({ page }) => {
    await page.goto('/');
    const btn = page.locator('header .lang-sel-btn');
    await expect(btn).toBeVisible();
    // ★ 判据翻转（用户拍板⑦）：触发钮固定词「语言」，自称名只在菜单选中项
    await expect(btn).toContainText('语言');
    await btn.click();
    const curZh = page.locator('[role="option"][aria-selected="true"]');
    await expect(curZh).toContainText('简体中文');
    await page.getByRole('option', { name: 'English' }).click();
    await expect(page.locator('.lc-nav-cta')).toContainText('Start free');
    await expect(page.getByText('免费试用')).toHaveCount(0);
    // 刷新后仍英文（app_lang 持久化，不会退回浏览器/默认语种）；触发钮仍是固定词
    await page.reload();
    await expect(page.locator('header .lang-sel-btn')).toContainText('Language');
    // 展开菜单，选中项携带英文自称名（自称名词表一字未动）
    await page.locator('header .lang-sel-btn').click();
    await expect(page.locator('[role="option"][aria-selected="true"]')).toContainText('English');
  });
});

test.describe('首次访问浏览器语言自动检测', () => {
  // locale 覆盖 config 的 zh-CN 钉底，模拟法国新访客（上下文 localStorage 天然为空）
  test.use({ locale: 'fr-FR' });
  test('fr-FR → Français + 落地页全量法语（★ 2026-09-20 全站十语种后不再走英文回退）', async ({ page }) => {
    await page.goto('/');
    // ★ 判据翻转（用户拍板⑦）：fr 界面下触发钮是固定词「Langue」，自称名在菜单选中项
    await expect(page.locator('header .lang-sel-btn')).toContainText('Langue');
    await page.locator('header .lang-sel-btn').click();
    await expect(page.locator('[role="option"][aria-selected="true"]')).toContainText('Français');
    // land.* 长尾键已在 locales/fr.ts 全量覆盖（2532 键口径）：法语访客直接看到法语 CTA
    await expect(page.locator('.lc-nav-cta')).toContainText('Essayer gratuitement');
    await expect(page.getByText('免费试用')).toHaveCount(0);
  });
});
