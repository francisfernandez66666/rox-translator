// ============================================================================
// e2e/tickets_dialog_a11y.spec.ts — 工单页自绘弹窗无障碍回归（★ 任务 #43，2026-09-22）
// 为什么存在：全量 UAT §4.2 实测「前端 a11y 仅剩 1 处真缺口」= TicketsPage 的两处 tk-overlay
//   自绘弹窗（进度气泡 / 工单反馈）。缺口不是配色而是模态语义：
//   ① 无 role="dialog"/aria-modal/aria-labelledby → 读屏不知道进入了弹窗、不知道弹窗叫什么；
//   ② 不能 Esc 关闭 → 键盘用户只能鼠标点遮罩；
//   ③ Tab 会跑到弹窗背后的列表按钮上 → 焦点逃出模态（WCAG 2.4.3 顺序漂移）；
//   ④ 关闭后焦点掉到 document.body → 键盘用户得从页头重新 Tab 一遍。
//   这四点全靠浏览器真焦点语义验证，jsdom 不实现 Tab 序列，写组件测试等于自欺，故放 e2e。
// 数据自给：用 fast 模式（免费档）建一条文本工单，只验「进度」弹窗能否打开——
//   不依赖别的 spec 留下的工单，也不会与 translate_flow 的余额测量互相扣费。
// 锚点策略：弹窗用 data-testid="tk-progress-dialog"；同时用 getByRole('dialog', { name: 工单标题 })
//   反证 aria-labelledby 真的指向了标题节点（可及名来自标题而不是文本巧合）。
//   行内「进度」触发钮按 A3 教训收敛在本行内定位（getByRole + 行 filter），不做全局 .first()。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/tickets_dialog_a11y.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
const USER = process.env.UAT_USER || 'uatuser_a';
const PASS = process.env.UAT_PASS || 'uatpass123';

// 登录并钉中语文种（首访语言按浏览器自动检测，中文锚点必须自行钉语种，见 AGENTS.md §5）
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败：${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  await page.addInitScript(() => localStorage.setItem('app_lang', 'zh'));
}

/** 当前焦点元素是否落在弹窗内部（含弹窗自身，tabIndex=-1 可聚焦） */
const focusInsideDialog = (page: Page) => page.evaluate(() => {
  const el = document.activeElement as HTMLElement | null;
  const dlg = document.querySelector('[data-testid="tk-progress-dialog"]');
  return !!el && !!dlg && (el === dlg || dlg.contains(el));
});

test.describe('工单页进度弹窗无障碍（§4.2 缺口收口）', () => {
  test('TK-A1 进度弹窗：模态语义 + Esc 关闭 + 焦点进出与圈定', async ({ page }) => {
    test.setTimeout(120_000); // 建单入队 + 列表轮询出现新行
    const title = 'e2e_a11y_' + Date.now();
    await login(page, USER, PASS);
    await page.goto('/tickets');

    // 建一条文本工单（默认「文本」模式 + fast 免费档，不产生扣费、不干扰其它 spec 的余额测量）
    await page.getByTestId('tk-title').fill(title);
    await page.getByTestId('tk-source').fill('无障碍回归用例：请填写收件地址后提交。');
    await page.getByRole('button', { name: /创建并入队/ }).click();
    const row = page.getByRole('row').filter({ hasText: title });
    await expect(row, '新建工单未出现在列表').toHaveCount(1, { timeout: 20_000 });

    // ① 打开进度弹窗：可及名必须来自弹窗标题（=工单标题），反证 aria-labelledby 生效
    const trigger = row.getByRole('button', { name: '进度' });
    await trigger.click();
    const dialog = page.getByRole('dialog', { name: title });
    await expect(dialog, '进度弹窗未以 role=dialog + 标题可及名出现').toBeVisible({ timeout: 15_000 });
    await expect(dialog).toHaveAttribute('aria-modal', 'true');

    // ② 焦点进入弹窗（读屏据此朗读标题；否则焦点还停在列表按钮上，等于没有模态）
    await expect(dialog, '打开弹窗后焦点未进入弹窗').toBeFocused();

    // ③ Tab 圈定：连按 6 次（多于弹窗内可聚焦元素数量，必然触发回绕）焦点都不得跑到弹窗外
    for (let i = 0; i < 6; i++) {
      await page.keyboard.press('Tab');
      expect(await focusInsideDialog(page), `第 ${i + 1} 次 Tab 后焦点逃出弹窗`).toBe(true);
    }
    // Shift+Tab 反向同样不得外逃
    await page.keyboard.press('Shift+Tab');
    expect(await focusInsideDialog(page), 'Shift+Tab 后焦点逃出弹窗').toBe(true);

    // ④ Esc 关闭 + 焦点归还触发元素（键盘用户不必从头 Tab）
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog')).toHaveCount(0, { timeout: 10_000 });
    await expect(trigger, '关闭后焦点未归还给行内「进度」触发钮').toBeFocused();

    await page.screenshot({ path: 'artifacts/tk_a11y_progress_dialog.png' });
  });

  test('TK-A2 反馈弹窗：模态语义与焦点圈定（含表单控件）', async ({ page }) => {
    test.setTimeout(120_000);
    await login(page, USER, PASS);
    await page.goto('/tickets');
    // 找一个「已完成」工单（反馈入口只对 completed 渲染）；没有就说明本次闸门里无完成件，跳过而非假红
    const doneRow = page.getByRole('row').filter({ hasText: /已完成/ }).first();
    const hasDone = await doneRow.isVisible().catch(() => false);
    test.skip(!hasDone, '列表内无已完成工单（反馈入口不渲染），跳过反馈弹窗断言');

    const trigger = doneRow.getByRole('button', { name: '反馈' });
    await expect(trigger).toBeVisible({ timeout: 10_000 });
    await trigger.click();

    const dialog = page.getByTestId('tk-feedback-dialog');
    await expect(dialog, '反馈弹窗未以 role=dialog 出现').toBeVisible({ timeout: 10_000 });
    await expect(dialog).toHaveAttribute('aria-modal', 'true');
    await expect(dialog, '打开反馈弹窗后焦点未进入弹窗').toBeFocused();
    // 提交按钮在未填内容时是 disabled：焦点圈定必须跳过它，第一个 Tab 应落到可输入的 textarea
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      const dlg = document.querySelector('[data-testid="tk-feedback-dialog"]');
      return !!el && !!dlg && dlg.contains(el) && el.tagName === 'TEXTAREA';
    }), 'Tab 未落到弹窗内的反馈输入框').toBe(true);
    await page.keyboard.press('Escape');
    await expect(page.getByTestId('tk-feedback-dialog')).toHaveCount(0, { timeout: 10_000 });
    await expect(trigger, '关闭反馈弹窗后焦点未归还触发元素').toBeFocused();
  });
});
