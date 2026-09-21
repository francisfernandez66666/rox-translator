// ============================================================================
// e2e/translate_flow.spec.ts — 翻译主流程端到端（★ 任务 #43，2026-09-22）
// 为什么存在：全量 UAT §2.2/§6 指出 Playwright 50 条里从来没有一条把
//   「提交即时翻译 → 译文上屏 → 扣费可在界面上看见」串起来：
//   · pixel_uat P2 只断言「译文出现」，看不到钱有没有扣（扣费回归只能靠 shell 侧 api_uat 的字节断言，
//     而 UI 侧「用户能不能看到余额变了」一直是零覆盖）；
//   · multilang_zip M1 已经覆盖「文件工单上传 → 完成 → 下载 zip 双语言产物」，本 spec 不重复；
//   · 余额不足/失败路径由 api_uat_txn（T7 OpenAPI 余额硬闸、T29 扣费事务）覆盖，此处不重复。
// 本 spec 只钉三条既有稳定行为（改坏任意一条都直接影响付费用户）：
//   TF1 中文源文 → 目标语译文上屏：译文非空、不等于原文、含拉丁字母，且剥掉括号内的源文回显后不含汉字；
//   TF2 扣费可见：一轮翻译完成后「今日已耗」严格增加、「余额」严格减少（全部走页面既有数值，不直连 DB）；
//   TF3 余额条随每轮刷新（不是只在挂载时读一次）——即 TF2 的读数必须是二次刷新后的值。
// 锚点策略（A3 红灯的教训：可见文案不能当唯一锚点）：
//   输入框 data-testid="translate-input"（既有）、余额条 data-testid="chat-balance"、
//   用量条 data-testid="chat-usage"（本次补，见汇报）；数值解析只取文本里的第一个数字，
//   不依赖词典文案，所以语种翻红不会牵连本用例。
// ★ 关于「不含汉字」为什么不能整串断言：UAT 的 mock LLM（scripts/uat/mock_llm.py:32）
//   返回 `TranslatedEN(<源文前 30 字>)`，括号里就是中文源文，整串断言「无汉字」必然假红。
//   故把括号内的回显剥掉后再断言无汉字——真译文（KB 直配的纯英文）与 mock 译文都能通过。
// ★ 关于净扣费为什么要重试测量：每日任务「发起翻译」奖励 100 积分在翻译请求内**同步发放**
//   （internal/api/task_hooks.go:101），且按天判重；并行 worker（pixel_uat P2 同为 uatuser_a）
//   可能与本用例抢这一次发放，导致「余额反而变大」。先跑一轮预热吃掉发放窗口，
//   再做最多 3 轮基线对比——抢发全天只发生一次，第二轮起基线必然干净。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/translate_flow.spec.ts
//      （由 scripts/uat/run_uat.sh 统一编排：真实后端 + mock LLM，勿裸跑）
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
// 沿用现有 spec 的既有常量口径：账号由 run_uat 预先注册，本 spec 不做注册流程
const USER = process.env.UAT_USER || 'uatuser_a';
const PASS = process.env.UAT_PASS || 'uatpass123';

// 每轮源文都必须唯一（含本次运行的时间戳）：撞 TM/知识库精确命中会走免扣费的复用路径，
// 「今日已耗增加 / 余额减少」就失去被测意义；跨次运行也不让缓存把断言变成假绿。
const RUN_TAG = 'e2etf' + Date.now();
const srcText = (tag: string): string => `翻译主流程回归 ${RUN_TAG}_${tag}：设备需在傍晚前送达客户。`;

// 登录态注入：API 登录拿 token → sessionStorage（与 pixel_uat / assist_admin 同口径）。
// 同时钉 app_lang=zh：首访语言改成了按浏览器语言自动检测（AGENTS.md §5），
// 中文锚点（如工单模式按钮）必须自行钉语种，否则换机器跑就整批翻红。
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败：${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  await page.addInitScript(() => localStorage.setItem('app_lang', 'zh'));
}

/** 从积分条读数值：取文本里第一个数字（含千分位逗号），与 i18n 文案彻底解耦 */
async function readPoints(page: Page, testid: string): Promise<number> {
  const txt = await page.getByTestId(testid).innerText({ timeout: 10000 });
  const m = txt.match(/[\d,]+/);
  expect(m, `${testid} 未渲染出积分数值：${txt.slice(0, 120)}`).toBeTruthy();
  return Number((m as RegExpMatchArray)[0].replace(/,/g, ''));
}

/**
 * 提交一轮即时翻译并断言「译文真的上屏了」。
 * @param round 第几轮（用于按结果表数量等本轮气泡——.last() 在新一轮渲染前仍指向上一轮，
 *              直接 toBeVisible 会读到过期元素，属于必须避开的假绿）
 */
async function translateOnce(page: Page, source: string, round: number) {
  const input = page.getByTestId('translate-input');
  await expect(input, '工作台输入框未渲染').toBeVisible({ timeout: 20000 });
  await input.fill(source);
  // 主按钮文案随 UI 迁移在「发送/翻译」间改过，沿用 P2 的兼容正则锁语义而非字面文案
  await page.getByRole('button', { name: /^(发送|翻译|Send|Translate)$/ }).click();

  const results = page.locator('.translation-results');
  await expect(results, `第 ${round} 轮译文结果表未在 90s 内出现`).toHaveCount(round, { timeout: 90_000 });
  const target = page.locator('.lang-text').last();
  const out = (await target.innerText()).trim();
  // ① 非空 ② 不等于原文（引擎回显/同文判定失手时会原样返回，属真实回归）
  expect(out.length, '译文为空').toBeGreaterThan(0);
  expect(out.replace(/\s+/g, ' '), '译文与原文一致（未发生改写）').not.toBe(source.replace(/\s+/g, ' '));
  // ③ 出目标语字符（en 目标至少要有成串拉丁字母）
  expect(/[A-Za-z]{3,}/.test(out), `译文未见目标语拉丁内容：${out.slice(0, 120)}`).toBeTruthy();
  // ④ 剥掉 mock 回显括号后不得残留汉字（真·纯英文译文恒通过；混进中文说明漏译/回显）
  const stripped = out.replace(/\([^)]*\)/g, '');
  expect(/[\u4e00-\u9fff]/.test(stripped), `剥掉回显括号后仍含汉字（疑似漏译）：${out.slice(0, 120)}`).toBeFalsy();
  return out;
}

test.describe('翻译主流程（即时翻译 → 译文上屏 → 扣费可见）', () => {
  test('TF1+TF2+TF3 即时翻译译文上屏且余额净减少', async ({ page }) => {
    // 多轮 mock LLM 翻译 + 每轮 90s 上限，默认 30s 必然不够
    test.setTimeout(300_000);
    await login(page, USER, PASS);
    await page.goto('/');

    // TF3 前置：余额/用量条必须出现（这两条只在 myPackage 返回数值时渲染）
    await expect(page.getByTestId('chat-balance'), '余额条未渲染（myPackage 未出 points_balance）')
      .toBeVisible({ timeout: 20000 });
    await expect(page.getByTestId('chat-usage'), '今日已耗条未渲染（myPackage 未出 points_used_today）')
      .toBeVisible({ timeout: 20000 });

    // 预热轮：吃掉「当日首次发起翻译」的任务奖励发放与首帧渲染抖动
    const warmBal = await readPoints(page, 'chat-balance');
    const warmUsed = await readPoints(page, 'chat-usage');
    await translateOnce(page, srcText('warmup'), 1);
    // 只等「积分条被刷新过」这一件事，不做数值判定：预热轮可能被每日奖励补成正数增量，
    // 拿它当断言会把用例写成随机红（TF3 的刷新证据在后面的正式测量轮里硬断言）。
    try {
      await expect
        .poll(() => readPoints(page, 'chat-balance') + ':' + readPoints(page, 'chat-usage'), { timeout: 20_000 })
        .not.toBe(`${warmBal}:${warmUsed}`);
    } catch {
      // 预热轮没观察到刷新也继续：正式测量轮自带「已耗增加 + 余额减少」的硬断言
    }

    // 净扣费测量：最多 3 轮，抢发（并发 worker 触发同一次每日奖励）只会污染前一两轮
    let measured = false;
    let lastBefore = 0;
    let lastAfter = 0;
    for (let attempt = 1; attempt <= 3 && !measured; attempt++) {
      const round = attempt + 1;
      const balBefore = await readPoints(page, 'chat-balance');
      const usedBefore = await readPoints(page, 'chat-usage');
      await translateOnce(page, srcText(`m${round}`), round);

      // TF2-a：今日已耗严格增加（发放积分不影响该计数，是抗干扰的扣费证据）
      await expect
        .poll(() => readPoints(page, 'chat-usage'), { timeout: 30_000, message: `第 ${round} 轮完成后今日已耗未增加` })
        .toBeGreaterThan(usedBefore);

      // TF2-b：余额严格减少（同一轮 myPackage 响应里带回，故与已耗同批刷新）
      lastBefore = balBefore;
      lastAfter = await readPoints(page, 'chat-balance');
      measured = lastAfter < balBefore;
    }
    expect(measured, `连续 3 轮翻译后余额未净减少（${lastBefore} → ${lastAfter}）：扣费未生效或界面未刷新`).toBe(true);

    await page.screenshot({ path: 'artifacts/tf1_translate_flow.png' });
  });
});
