// ============================================================================
// e2e/register_captcha_token.spec.ts — F-03 浏览器级锁（2026-09-25 批G 收尾并入）
// 缺陷形态：Turnstile token 一次一用（服务端 siteverify 消费即废），传统表单发码/提交
//   消费掉 token 后必须 reexec 领新码；旧实现 reexecTurnstile 只调 execute 不调 reset，
//   已完成挑战的挂件 execute 不一定重新出码 → 第二次请求复用废 token 必吃 403。
//   单测侧已锁「reset 先于 execute 的调用序」（AiRegisterFlow.dom.test / Login dom 套件），
//   本文件补的是修复文档点名的 UAT 半句：**连点两次「发送验证码」，两次出网的
//   captcha_token 前 8 位必须不同**——证明领新码真的流到了请求载荷，而不是停在 ref 里。
// 施工形态：真实浏览器 + 真实 dist，只对两个接口做 route mock——
//   ① /api/auth/register-config 回「人机验证开 + 邮箱验证开」，让挂件容器与发码按钮出现；
//   ② /api/auth/email-code 一律回 200 {success:false}：失败分支不进 60s 冷却（成功才
//     codeCd.start()），才能立刻连点第二次；同时在 route 处理器里录下每次载荷的 token。
//   Turnstile 用 addInitScript 注入全局替身（render 挂 iframe、execute 出递增新码
//   TS-00001/TS-00002…），loadTurnstile 见 window.turnstile 即直挂，不碰真 CF 脚本——
//   这满足 e2e 红线（不依赖外部域名/生产配置），锁的是我们自己那条 token 消费链。
// 顺带钉住 F-07 的用户可见判据：AI 面板 2.35s 必然接管，点 × 退回后传统表单容器里
//   必须重新出现挂件 iframe——没有挂件就没有 token，下面的连点断言会整段失效，
//   所以这一条既是独立回归位也是本用例的前置地基。
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:5173';

// Turnstile 全局替身：必须在应用脚本执行前注入（addInitScript 保证）
function stubTurnstile(page: Page) {
  return page.addInitScript(() => {
    const w = window as unknown as {
      turnstile?: unknown;
      __uat_ts?: { render: number; execute: number; reset: number; cb: ((t: string) => void) | null; seq: number };
    };
    const st = { render: 0, execute: 0, reset: 0, cb: null as ((t: string) => void) | null, seq: 0 };
    w.__uat_ts = st;
    const nextToken = () => {
      // 前 8 位是递增序号段（TS-00001 / TS-00002），断言就吃这一段：每次出码必不同
      st.seq += 1;
      return 'TS-' + String(st.seq).padStart(5, '0') + '-' + Math.random().toString(36).slice(2, 8);
    };
    w.turnstile = {
      render: (el: HTMLElement, opts: { callback?: (t: string) => void }) => {
        st.render += 1;
        st.cb = opts.callback ?? null;
        const f = document.createElement('iframe'); // 模拟真挂件往容器塞 iframe（重挂判据靠它）
        el.appendChild(f);
        const tk = nextToken();
        setTimeout(() => { if (st.cb) st.cb(tk); }, 40); // 出码异步一拍，贴近真 CF 节奏
        return 'uat-widget-1';
      },
      // reset 只计数不动 cb：reexecTurnstile 是 reset→execute 两段式，
      // 若哪天 reset 调用被删，__uat_ts.reset 归零、下面那条等值锁先红
      reset: () => { st.reset += 1; },
      execute: () => {
        st.execute += 1;
        const tk = nextToken();
        setTimeout(() => { if (st.cb) st.cb(tk); }, 30);
        return tk;
      },
    };
  });
}

test.describe('F-03 · 连点两次发码，出网 captcha_token 前 8 位必须不同', () => {
  test('注册屏挂件重挂 + 双请求 token 递增', async ({ page }) => {
    await stubTurnstile(page);

    // 录每次发码请求载荷里的 token（route 处理器在 node 侧，数组直接闭包共享）
    const sentTokens: string[] = [];
    await page.route('**/api/auth/email-code', (route) => {
      try {
        const body = JSON.parse(route.request().postData() || '{}') as { captcha_token?: string };
        sentTokens.push(body.captcha_token || '');
      } catch { sentTokens.push(''); }
      return route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: false, message: 'e2e 桩：发码拒绝（只为立刻连点免进冷却）' }),
      });
    });
    // 开人机验证 + 开邮箱验证：前者让挂件挂载链走通，后者让「发送验证码」按钮渲染
    await page.route('**/api/auth/register-config', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, captcha_enabled: true, captcha_site_key: 'k-e2e-stub', email_verify_enabled: true }),
      }));

    await page.goto(`${BASE}/register`);

    // F-07 前置地基：AI 面板按时接管 → 点 × 退回 → 容器里必须重新出现挂件 iframe
    const closeBtn = page.locator('.ar-close');
    await expect(closeBtn, '注册屏应在数秒内被 AI 面板接管').toBeVisible({ timeout: 20000 });
    await closeBtn.click();
    const box = page.locator('#__ts_widget__');
    await expect(box.locator('iframe'), '退回传统表单后 Turnstile 挂件必须重挂').toBeVisible({ timeout: 10000 });

    // 首码到位（替身出码有 40ms 异步拍）后连点两次发码
    await expect.poll(() => page.evaluate(() => (window as unknown as { __uat_ts?: { render: number } }).__uat_ts?.render ?? 0),
      { message: '替身 render 必须被调用（挂件真实挂载）' }).toBeGreaterThan(0);
    // exact 必带：placeholder「邮箱」是「邮箱验证码」的前缀，宽匹配会 strict mode 双命中
    const email = page.getByPlaceholder('邮箱', { exact: true });
    await email.fill('uat_f03_replay@test.com');
    const sendBtn = page.getByRole('button', { name: '发送验证码' });
    await sendBtn.click();
    await expect.poll(() => sentTokens.length, { message: '第一次发码请求必须出网' }).toBe(1);
    // 第一次请求已消费旧码：finally 里 refreshCaptcha → reset+execute → 30ms 后新码落 ref。
    // 等出码拍子走完再点第二次，否则测的是「空 token 被前端守卫拦下」而不是复用不复用。
    await expect.poll(() => page.evaluate(() => (window as unknown as { __uat_ts?: { execute: number } }).__uat_ts?.execute ?? 0),
      { message: '消费后必须 reexec 领新码' }).toBeGreaterThanOrEqual(1);
    await sendBtn.click();
    await expect.poll(() => sentTokens.length, { message: '第二次发码请求必须出网' }).toBe(2);

    // ★ 修复文档点名的等值判据：两次出网 token 前 8 位必须不同（TS-00001 vs TS-00002）
    expect(sentTokens[0], '第一次请求必须带 token').toBeTruthy();
    expect(sentTokens[1], '第二次请求必须带新 token').toBeTruthy();
    expect(sentTokens[1].slice(0, 8), '连点两次的前 8 位必须不同（旧码不得复用）')
      .not.toBe(sentTokens[0].slice(0, 8));
    // reset 恰好先于第二次 execute 发生：reexecTurnstile 两段式在真实装载链里走通
    const counts = await page.evaluate(() => (window as unknown as { __uat_ts?: { reset: number; execute: number } }).__uat_ts);
    expect(counts?.reset, '每次消费后必须 reset 再 execute').toBeGreaterThanOrEqual(1);
  });
});
