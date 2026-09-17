import { test, expect } from '@playwright/test';

// ============================================================================
// e2e-manual/iframe_csp_probe.spec.ts — AI 助手管理台同源 iframe 嵌入 + CSP 探针
//   （手工运行，不进发布闸门）
// 背景（2026-09-17）：本用例原为 e2e/_tmp_iframe.spec.ts，写死生产站地址
//   https://langcross.lexicorn.cn/，且 testDir=./e2e 会把它纳入发布闸门——与
//   _tmp_admin.spec.ts 同类缺陷（打生产站 + 环境不可控 → 闸门长期带已知红）。
//   已移出 testDir（playwright testDir=./e2e 不含 e2e-manual），并加 skip 守卫。
// 用途：验证 caddy CSP 互斥分流后，管理台可在主站同源 iframe 内正常加载、零 CSP 报错。
// 手工运行：
//   ASSIST_IFRAME_PROBE=1 TARGET=https://langcross.lexicorn.cn \
//   npx playwright test e2e-manual/iframe_csp_probe.spec.ts --config playwright.config.ts
// ============================================================================
const TARGET = process.env.TARGET || process.env.BASE_URL || 'https://langcross.lexicorn.cn';

// 手工探针：仅当显式设置 ASSIST_IFRAME_PROBE 时执行（默认 skipped，闸门安全）
test.skip(!process.env.ASSIST_IFRAME_PROBE, '手工生产探针：需 ASSIST_IFRAME_PROBE=1（见文件头注释），不进发布闸门');

test('same-origin iframe embeds assist admin, zero CSP errors', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });
  await page.goto(`${TARGET}/`);
  await page.evaluate(() => {
    const f = document.createElement('iframe');
    f.id = 'probe'; f.src = '/assist-api/assist/admin';
    f.width = '900'; f.height = '600';
    document.body.appendChild(f);
  });
  await page.waitForTimeout(3000);
  const probe = await page.evaluate(() => {
    const f = document.getElementById('probe') as HTMLIFrameElement | null;
    try { const d = f!.contentDocument; return d ? 'LOADED:' + d.title.slice(0, 12) : 'BLOCKED'; }
    catch { return 'BLOCKED'; }
  });
  console.log('PROBE=' + probe);
  const csp = errors.filter((e) => /Content Security Policy|Refused/i.test(e));
  console.log('CSP_ERRORS=' + csp.length);
  expect(probe.startsWith('LOADED:AI 助手管理台')).toBeTruthy();
  expect(csp.length).toBe(0);
});
