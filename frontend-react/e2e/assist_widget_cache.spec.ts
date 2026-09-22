// ============================================================================
// e2e/assist_widget_cache.spec.ts — C 端 AI 助手挂件「刷新不丢对话」端到端
// 立锁背景（★ 2026-09-22 用户反馈「ai 助手要带缓存，不然刷新一次页面就没了很尴尬」）：
//   挂件历史原本只有服务端 history 一条路，而它要求 sid+tok 成对有效；
//   当时 sessKey 由管理 Token 派生，assist 进程重启或 Token 轮换即让全部访客 tok 作废，
//   刷新页面就等于「聊过的全没了」。该根因已在服务端修掉（sess_key 随机生成并落库，见
//   internal/assist/api 的 TestSessKeySurvivesRestart），本 spec 锁的是缓存这一层兜底：
//   即便令牌因跨 origin / 清站点数据 / 服务端不可达而拿不回来，气泡也不能消失。
// 本 spec 锁住三件事（真浏览器 + 真刷新，jsdom 单测给不了这个置信度）：
//   W1 一轮对话后本地确实落了缓存（内容逐条对齐，不含推荐按钮文案）；
//   W2 reload 后重开面板，刷新前的提问与回复原样在（★ 核心诉求）；
//   W3 令牌被改成失效值（模拟 assist 重启/Token 轮换）后刷新，气泡仍不消失，
//      也不会被打回成一句新欢迎词——那正是历史上最尴尬的形态。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/assist_widget_cache.spec.ts
//      （由 scripts/uat/run_uat.sh 统一编排：主服务 + assist-server + mock LLM）
// 前置：BASE_URL 必须能应答 /assist-api/*（vite proxy / Caddy / 主服务自带转发三者之一）。
//      缺了这条链路本 spec 会「假绿」——界面上跑的是离线兜底话术、缓存层照样一致，
//      所以每条用例都先过 expectLiveLink 探针（见该函数注释）。
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
/** 挂件消息缓存键（与 api/assist.ts 的 MSG_KEY 同口径） */
const MSG_KEY = 'ny_assist_msgs';

/** 登录并进入挂件常驻的工作台 */
async function openWorkbench(page: Page) {
  const res = await page.request.post(`${BASE}/api/auth/login`, {
    data: { username: 'admin', password: process.env.ADMIN_PASS || 'Admin@1234' },
  });
  const body = await res.json();
  expect(body.success, `登录失败：${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  await page.goto('/');
  // 冷启动占位要演完三个语种拍次（#66），故这里放宽到 25s 再等 FAB
  await expect(page.locator('.na-fab')).toBeVisible({ timeout: 25000 });
}

/**
 * 展开面板并取回气泡正文数组。
 * 必须剔掉 .na-acts（推荐功能入口按钮）再取文本：那部分不在缓存的 content 字段里，
 * 混进来会让「缓存内容 == 界面内容」的逐条断言假红。
 * 等待态的三点动画行正文为空，交由调用方 filter(Boolean) 排除，避免把 spinner 误当回复。
 */
async function bubbleTexts(page: Page): Promise<string[]> {
  if (await page.locator('.na-fab').count()) await page.locator('.na-fab').click();
  await expect(page.locator('.na-panel')).toBeVisible();
  const texts = await page.locator('.na-row .na-bubble').evaluateAll((els) =>
    els.map((el) => {
      const c = el.cloneNode(true) as HTMLElement;
      c.querySelectorAll('.na-acts').forEach((n) => n.remove());
      return (c.textContent || '').trim();
    }));
  return texts.filter(Boolean)
}

/** 发一句并等回复上屏（以「非空气泡比发送前多两条」为准，绕开三点等待态） */
async function ask(page: Page, q: string): Promise<void> {
  const before = (await bubbleTexts(page)).length;
  await page.locator('.na-input input').fill(q);
  await page.locator('.na-send').click();
  await expect
    .poll(async () => (await bubbleTexts(page)).length, { timeout: 25000, message: '助手回复未上屏' })
    .toBe(before + 2);
}

/** 读缓存里的消息正文（逐条，与界面口径一致） */
async function cachedTexts(page: Page): Promise<string[]> {
  const raw = await page.evaluate((k) => localStorage.getItem(k), MSG_KEY);
  if (!raw) return [];
  const p = JSON.parse(raw) as { msgs?: { content: string }[] };
  return (p.msgs || []).map((m) => m.content);
}

/**
 * 链路可达探针（★ 2026-09-22，本 spec 曾被它照出一个真缺陷）：
 * 挂件走同源 /assist-api，这条前缀此前只存在于 vite dev proxy 与生产 Caddy；
 * 主服务直出 dist 的形态（含发布闸门）下请求落进 SPA 兜底拿回 index.html，
 * 于是三条用例全都「绿」——因为界面上是离线兜底话术，缓存层照样一致。
 * 现已由主服务自己转发（internal/api/assist_open_proxy.go），这里钉两件事：
 * 服务端确实下发过会话 id（greet 打到 assist），且界面上没有离线徽标。
 */
async function expectLiveLink(page: Page, ctx: string): Promise<void> {
  expect(await page.locator('.na-offline').count(), `${ctx}：挂件处于离线兜底态 ⇒ /assist-api 链路没通`).toBe(0);
  const sid = await page.evaluate(() => localStorage.getItem('ny_assist_sid') || '');
  expect(sid, `${ctx}：greet 应已下发会话 id（拿不到即 assist 不可达）`).toBeTruthy();
}

test.describe('C 端 AI 助手挂件会话缓存', () => {
  test('W1 一轮对话后缓存与界面逐条一致', async ({ page }) => {
    await openWorkbench(page);
    const q = `缓存回归测试 ${Date.now()}`;
    await ask(page, q);
    await expectLiveLink(page, 'W1');
    const shown = await bubbleTexts(page);
    const cached = await cachedTexts(page);
    expect(cached.length, '对话必须写入 localStorage 缓存').toBeGreaterThan(0);
    // 缓存定位是「界面所见即所存」：条数与内容都应与当前气泡对齐
    expect(cached).toEqual(shown);
    expect(cached).toContain(q);
  });

  test('W2 刷新后重开面板，刷新前的提问与回复原样回显', async ({ page }) => {
    await openWorkbench(page);
    const q = `刷新前的问题 ${Date.now()}`;
    await ask(page, q);
    await expectLiveLink(page, 'W2');
    const before = await bubbleTexts(page);
    expect(before).toContain(q);

    await page.reload();
    await expect(page.locator('.na-fab')).toBeVisible({ timeout: 25000 });
    const after = await bubbleTexts(page);
    expect(after.join('\n')).toContain(q);
    expect(after.length, '刷新后气泡条数不得少于刷新前').toBeGreaterThanOrEqual(before.length);
  });

  test('W3 tok 失效后刷新：气泡不消失、也不被打回成新欢迎词', async ({ page }) => {
    await openWorkbench(page);
    const q = `令牌失效也要留着 ${Date.now()}`;
    await ask(page, q);
    const before = await bubbleTexts(page);
    await expectLiveLink(page, 'W3');

    // 模拟 assist 进程重启/管理 Token 轮换：本地 tok 作废 → 服务端 history 必回 401
    await page.evaluate(() => localStorage.setItem('ny_assist_tok', 'invalid-token-for-test'));
    await page.reload();
    await expect(page.locator('.na-fab')).toBeVisible({ timeout: 25000 });
    const after = await bubbleTexts(page);
    expect(after.join('\n')).toContain(q);
    expect(after.length, '401 之后不得把缓存冲成一两条').toBeGreaterThanOrEqual(before.length);
  });
});
