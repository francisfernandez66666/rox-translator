import { test, expect } from '@playwright/test';

// ============================================================================
// e2e/all_langs_full.spec.ts — 全站十语种浏览器级回归（★ #32，2026-09-21）
// 锁定「全站十语种」交付：十个非简中/英文语种下，
//   ① 落地页长尾键（land.qs4.n 质量数字卡）直接渲染该语种译文，不再走 lang→en→zh 回退；
//   ② 顶栏导航不露简体中文（★ 射程扩展 2026-10 用户拍板⑦后批）：header 内**任何汉字**都算泄漏，
//      不再只点名三条营销词——zh/zh_hant 两档作正向对照支（header 必须含汉字），证明判据真走到。
//      ⚠️ 例外：ja 界面与中文共享 CJK 表意区（機能/料金等正常日语汉字落在 [一-鿿]），
//      正则无法区分「日语正常用字」与「中文泄漏」，故 ja 只保留三条营销词字面负向。
// 断言串取自各语种词典实际值（land.qs4.n），词典改动需同步这里。
// ★ 反证口径（收口时实跑）：把 header 的选择器换成空选择器（如 `.no-such-header`）⇒
//   对照支「zh header 必须含汉字」当场红（textContent 为空）；负向支换空选择器恒绿=空转，
//   由对照支兜住。把触发钮自称名写回 LangSelect.tsx ⇒ 由 LangSelect.dom.test.tsx 正向词表锁红。
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
  ['zh', '降本 80%–90%'], // ★ 对照支：zh 界面 header 必须含汉字（见文件头反证口径）
];

test.describe('全站十语种落地页（#32）', () => {
  for (const [lang, want] of CASES) {
    test(`${lang}：长尾键出该语种译文且导航不漏中文`, async ({ page }) => {
      // 用 addInitScript 在应用读 storage 前预置语种（等价首访选择）
      await page.addInitScript((l) => localStorage.setItem('app_lang', l), lang);
      await page.goto('/');
      await expect(page.locator('body')).toContainText(want, { timeout: 10_000 });
      const nav = page.locator('header');
      if (lang === 'zh' || lang === 'zh_hant') {
        // ★ 正向对照支：中文系界面 header 必须含汉字（品牌名「能言」/导航词条），
        //   证明选择器与汉字正则真走到——否则负向支的「没匹配到」可能是空转假绿
        const navText = (await nav.textContent()) ?? '';
        expect((navText.match(/[一-鿿]/g) ?? []).length, `${lang} 界面 header 应含汉字（对照支，判据走到的证据）`).toBeGreaterThan(0);
      } else if (lang === 'ja') {
        // ja：CJK 表意区共享，正则不可用，退回三条营销词字面负向（老判据保留）
        await expect(nav).not.toContainText('免费试用');
        await expect(nav).not.toContainText('价格方案');
        await expect(nav).not.toContainText('常见问题');
      } else {
        // 其余 8 语种：header 内任何汉字都是泄漏（三条营销词已并入该正则）
        const navText = (await nav.textContent()) ?? '';
        const hanzi = navText.match(/[一-鿿]/g) ?? [];
        expect(hanzi, `${lang} 界面 header 出现汉字（中文泄漏）：${hanzi.join('')}`).toEqual([]);
      }
    });
  }
});
