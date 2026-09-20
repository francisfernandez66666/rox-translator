import { test, expect } from '@playwright/test';

// ============================================================================
// e2e/all_langs_full.spec.ts — 全站十语种浏览器级回归（★ #32，2026-09-21）
// 锁定「全站十语种」交付：十个非简中/英文语种下，
//   ① 落地页长尾键（land.qs4.n 质量数字卡）直接渲染该语种译文，不再走 lang→en→zh 回退；
//   ② 顶栏导航不露简体中文营销词（对访客的中文泄漏是本轮要根除的主症状）。
// 断言串取自各语种词典实际值（land.qs4.n），词典改动需同步这里。
// 运行：本地 npx playwright test e2e/all_langs_full.spec.ts（需 5173 dev server）
// ============================================================================

// 语种 → 落地页质量数字卡应有译文片段（land.qs4.n）
const CASES: Array<[string, string]> = [
  ['ru', 'Дешевле на 80–90%'],
  ['fr', '80 à 90 % moins cher'],
  ['ar', 'تكلفة أقل بـ 80–90%'],
  ['es', '80–90% menos coste'],
  ['pt', '80–90% menos custo'],
  ['de', '80–90 % geringere Kosten'],
  ['th', 'ลดต้นทุน 80–90%'],
  ['ja', 'コスト 80–90% 削減'],
  ['ko', '비용 80–90% 절감'],
  ['zh_hant', '降本 80%–90%'],
];

test.describe('全站十语种落地页（#32）', () => {
  for (const [lang, want] of CASES) {
    test(`${lang}：长尾键出该语种译文且导航无中文`, async ({ page }) => {
      // 用 addInitScript 在应用读 storage 前预置语种（等价首访选择）
      await page.addInitScript((l) => localStorage.setItem('app_lang', l), lang);
      await page.goto('/');
      await expect(page.locator('body')).toContainText(want, { timeout: 10_000 });
      const nav = page.locator('header');
      await expect(nav).not.toContainText('免费试用');
      await expect(nav).not.toContainText('价格方案');
      await expect(nav).not.toContainText('常见问题');
    });
  }
});
