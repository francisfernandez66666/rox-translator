// ============================================================================
// e2e/translate_flow.spec.ts — 翻译主流程端到端（★ 任务 #43，2026-09-22）
// 为什么存在：全量 UAT §2.2/§6 指出 Playwright 50 条里从来没有一条把
//   「提交即时翻译 → 译文上屏 → 扣费可在界面上看见」串起来：
//   · pixel_uat P2 只断言「译文出现」，看不到钱有没有扣（扣费回归只能靠 shell 侧 api_uat 的字节断言，
//     而 UI 侧「用户能不能看到余额变了」一直是零覆盖）；
//   · multilang_zip M1 已经覆盖「文件工单上传 → 完成 → 下载 zip 双语言产物」，本 spec 不重复；
//   · 余额不足/失败路径由 api_uat_txn（T7 OpenAPI 余额硬闸、T29 扣费事务）覆盖，此处不重复。
// 本 spec 只钉三条既有稳定行为（改坏任意一条都直接影响付费用户）＋★ 2026-10-13 新增的 TF4：
//   TF1 中文源文 → 目标语译文上屏：译文非空、不等于原文、含拉丁字母，且剥掉括号内的源文回显后不含汉字；
//   TF2 扣费可见：一轮翻译完成后**本轮气泡的「消耗 N 积分」读数**大于 0 且「余额」严格减少（全部走页面既有数值，不直连 DB）；
//   TF3 余额条随每轮刷新（不是只在挂载时读一次）——即 TF2 的读数必须是二次刷新后的值；
//   TF4 ★ 用户令③ 的气泡尾部那一排同排同档（字号等值 14px ＋ 垂直中心同带；★ 2026-10-15 件数改成派生判据）——
//       完成态气泡只有这条真翻译链上才有，故运行时锁挂在这里而非 pixel_uat（那边是空态工作台）。
// ★★ 2026-10-14 红框批的判据翻转（★ 改方向必须写理由，不许只删断言）：
//   旧 TF2-a 读的是顶部余额栏里那颗「今日已耗 N 积分」（data-testid="chat-usage"）。
//   那颗徽标按用户令**从界面撤下**（`ChatWindow.tsx` 第 4 栏注释即当时留的账），
//   而撤它的批次只改了 `platformBilling` 单测，**这条 Playwright 没跟着改锚**——
//   当时 `ChatWindow.tsx` 的注释里写着「三处消费方已同批改锚」，那句话对 e2e 这一处**不成立**，
//   本轮主矩阵 TF1 段以「element(s) not found」当场把它坐实（复跑仍红 ⇒ 不是 flaky，是旧断言）。
//   新锚点＝**本轮气泡自己的** `msg-meta`「消耗 N 积分」，理由三条：
//     ① 它同样是**界面口径**（用户看得见的那一处），不是 DB 直连，spec 原有的纪律不变；
//     ② 它是**按轮**的读数，比"今日累计"更贴这一轮有没有扣钱——累计值会被同日其他链路抬动；
//     ③ 每日任务「发起翻译 +100」奖励**不污染它**（奖励进余额不进本轮消耗），
//        旧的那条"抗干扰"性质不但保留，还更强（旧写法靠"今日已耗"躲奖励，新写法连预热轮的干扰都躲开了）。
//   ⚠️ 出参 `points_used_today` 后端一字未动（管理台／看板／账单／SDK 仍读它），撤的只是那一块显示面；
//      词典键 `chat.usedTip`（那颗徽标的 tooltip）按「实装优先禁删除」留着，12 语种同步删键的收益是零。
// ★ 同批改锚还悄悄拆掉了一条**隐含等式**（2026-10-15 归因并补腿）：旧 TF2-a poll 的「今日已耗」与
//   余额同由一次 `loadBalance` 赋值，poll 到它涨就顺带等到了余额刷新；`msg-meta` 那条消耗行随
//   SSE done 帧当场出，余额却要等 2s debounce＋一次 myPackage 往返，于是"单发读余额"读到旧值、
//   三轮全判「未净减少」（实测 192986→192986、跨轮 −5/轮 ⇒ 钱扣了，红的是时机）。
//   ⇒ TF3 现在是**独立硬断言**（本轮余额条必须变），TF2-b 在已刷新那份读数上比大小。
// 锚点策略（A3 红灯的教训：可见文案不能当唯一锚点）：
//   输入框 data-testid="translate-input"（既有）、余额条 data-testid="chat-balance"、
//   本轮消耗 data-testid="msg-meta"（★ 2026-10-14 起取代 chat-usage，见上面那段）；
//   数值解析只取文本里的第一个数字，不依赖词典文案，所以语种翻红不会牵连本用例。
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
 * 读**最近一轮气泡自己**的「消耗 N 积分」（data-testid="msg-meta"）。
 * ★ 2026-10-14 起它是 TF2-a 的锚点，取代被撤下的那颗「今日已耗」徽标（理由见文件头那段）。
 * 用 `.last()` 而不是 `.first()`：每完成一轮就多一颗气泡，first 会永远停在预热轮那一颗
 * ——那正是记忆里「`.first()` 命中路由切换遗留的隐藏节点」同族的形态（度量腿要先自证射程）。
 * 这一件只在 `points_used` 非 0 时渲染（免扣费的 TM 精确命中不出这一行），
 * 所以"读得到数"本身就是"这一轮真扣了点"的界面证据。
 */
async function readRoundCost(page: Page): Promise<number> {
  const txt = await page.getByTestId('msg-meta').last().innerText({ timeout: 10000 });
  const m = txt.match(/[\d,]+/);
  expect(m, `本轮气泡的消耗读数里没解析出数字：${txt.slice(0, 120)}`).toBeTruthy();
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

    // TF3 前置：余额条必须出现（它只在 myPackage 返回数值时渲染）。
    // ★ 这里**不再**断言那颗「今日已耗」徽标——2026-10-14 红框批按用户令把它从界面撤了，
    //   扣费可见性的第二只眼睛改由**本轮气泡的消耗读数**（msg-meta）担任，见文件头那段翻转理由。
    await expect(page.getByTestId('chat-balance'), '余额条未渲染（myPackage 未出 points_balance）')
      .toBeVisible({ timeout: 20000 });

    // 预热轮：吃掉「当日首次发起翻译」的任务奖励发放与首帧渲染抖动
    const warmBal = await readPoints(page, 'chat-balance');
    await translateOnce(page, srcText('warmup'), 1);
    // ★ 预热轮的消耗读数**只做"存在性"取证、不做数值判定**：预热轮可能被每日奖励补成正数增量，
    //   拿它当断言会把用例写成随机红（TF3 的刷新证据在后面的正式测量轮里硬断言）。
    try {
      await expect.poll(() => readRoundCost(page), { timeout: 20_000 }).toBeGreaterThan(0);
    } catch {
      // 预热轮没等到消耗行也继续：正式测量轮自带「本轮消耗 > 0 ＋ 余额减少」的硬断言
    }
    try {
      await expect
        .poll(() => readPoints(page, 'chat-balance'), { timeout: 20_000 })
        .not.toBe(warmBal);
    } catch {
      // 同上：预热轮没观察到余额条刷新也继续
    }

    // 净扣费测量：最多 3 轮，抢发（并发 worker 触发同一次每日奖励）只会污染前一两轮
    let measured = false;
    let refreshSeen = false;
    let lastBefore = 0;
    let lastAfter = 0;
    for (let attempt = 1; attempt <= 3 && !measured; attempt++) {
      const round = attempt + 1;
      const balBefore = await readPoints(page, 'chat-balance');
      await translateOnce(page, srcText(`m${round}`), round);

      // TF2-a：本轮气泡自己报出「消耗 N 积分」且 N > 0
      //   （按轮读数 ⇒ 同日其他链路的累计增量与每日奖励都污染不了它，这是"这一轮真扣了点"的界面证据）
      await expect
        .poll(() => readRoundCost(page), { timeout: 30_000, message: `第 ${round} 轮完成后本轮消耗读数未出现或未大于 0` })
        .toBeGreaterThan(0);

      // TF3：余额条必须在**本轮**被刷新过（不是只在挂载时读一次）——这条现在是显式硬断言。
      //   ★★ 2026-10-15 归因批（本批 10-14 改锚留下的测量缺陷，非产品缺陷）：
      //   这一腿过去**隐含**在旧 TF2-a 里——旧写法 poll 的是顶部「今日已耗」，而它与余额由同一次
      //   `loadBalance` 一起赋值（ChatWindow 里 setBalance/setUsage 同批），poll 到它涨＝余额那一侧
      //   必然也已经刷过。10-14 把锚点换成气泡自己的 `msg-meta` 之后这条隐含等式**断了**：
      //   消耗行随 SSE done 帧当场渲染，余额却要等 `useChat` 的 PKG_REFRESH_DEBOUNCE_MS（2s）
      //   ＋一次 myPackage 往返 ⇒ 下面原先那次"单发读余额"读到的是刷新前的旧值，
      //   于是三轮全判「未净减少」。现网实测读数是 192986→192986、192981→192981、192976→192976
      //   （跨轮 −5/轮 ⇒ 钱确实扣了，红的是**时机**）。
      //   ⇒ 修法＝把"刷没刷"挑明成一条独立断言，再在**已刷新那份读数**上比大小；判据一点没放宽，
      //     比原来多一条硬断言（旧写法在"已耗涨、余额没刷"这种撕裂形态下反而会误判）。
      let refreshed = false;
      try {
        await expect
          .poll(() => readPoints(page, 'chat-balance'), { timeout: 30_000 })
          .not.toBe(balBefore);
        refreshed = true;
        refreshSeen = true;
      } catch {
        // 本轮没等到刷新：留给下一轮（三轮都没刷新的情形由循环后第一条 expect 判红）
      }

      // TF2-b：余额严格减少（比的是"本轮刷新后"的值，不是刷新前的旧值）
      lastBefore = balBefore;
      lastAfter = await readPoints(page, 'chat-balance');
      measured = refreshed && lastAfter < balBefore;
    }
    // 两条红分开报，别混成一条：分不清「余额条压根不刷」和「刷了但没净减」就没法定位。
    expect(refreshSeen, `连续 3 轮翻译后余额条一次都没刷新（读数恒 ${lastBefore}）：myPackage→余额条刷新链路断了`).toBe(true);
    expect(measured, `连续 3 轮翻译后余额未净减少（${lastBefore} → ${lastAfter}）：扣费未生效或被每日奖励补平`).toBe(true);

    // ★ 2026-10-13 用户令③ TF4：气泡尾部那一排（查看原文／复制／**朗读**／反馈／消耗）**同排、同字号档**。
    //   为什么只能量在这里：jsdom 单测不做 flex 布局、也不加载 theme.css（源码级锁见
    //   MessageBubble.dom.test.tsx ⑨⑩），而 pixel_uat 那批几何锁跑的是空态工作台——**完成态气泡**
    //   只有这条真翻译链上才有。投诉本体是「两排两档两种高低」，所以两条腿都要量：
    //     字号等值（14，不是「只准更大」的单向锁）＋ 垂直中心同带（同排＝同一高低）。
    //   ★★ 2026-10-15 把「件数＝4」翻成**派生判据**（原写法恒红复现：本批 P3 译文朗读把
    //   `SpeakButton` 放进了同一排，`Array.from(row.children)` 数到 5）：
    //   钉死数字这类锁的毛病不是"会过时"而是"过时了还在替旧口径说话"——它问的其实不是
    //   「这一排有几件」，而是「有没有哪件掉出这一排 / 各走一档字号」。改成三条派腿后：
    //     ① 每一枚直接子节点都必须属于 `.msg-act-btn` / `.msg-meta` 这两类之一（第三方类名＝有控件被分排或另加容器）；
    //     ② 点名的功能件逐个必须在场（缺件＝那一件从界面上掉了，这条正是原「数件数」想抓的东西）；
    //     ③ 射程自证：子节点总数不得低于点名件数（取到空行/锚点漂了 ⇒ 判"锁失明"而不是"通过"，同 §一·14 P0 那条口径）。
    //   ⇒ 将来再往这一排加第 N 件（只要它还是 `.msg-act-btn`）不需要改这条锁；而它会**自动**被
    //     下面的字号/同带逐件判据量到，这是写死 4 做不到的。
    const NAMED = ['show-src', 'copy-translation', 'speak-translation', 'msg-meta'];
    const tail = await page.locator('.msg-actions').last().evaluate((row, named) => {
      const rc = row.getBoundingClientRect();
      const center = rc.top + rc.height / 2;
      const kids = Array.from(row.children) as HTMLElement[];
      return {
        count: kids.length,
        foreign: kids
          .filter((el) => !el.classList.contains('msg-act-btn') && !el.classList.contains('msg-meta'))
          .map((el) => el.className || el.tagName),
        missing: named.filter((id) => !row.querySelector(`[data-testid="${id}"]`)),
        feedback: row.querySelectorAll('.msg-fb-btn').length,
        items: kids.map((el) => {
          const b = el.getBoundingClientRect();
          return {
            cls: el.className,
            fs: parseFloat(getComputedStyle(el).fontSize),
            off: Math.abs(b.top + b.height / 2 - center),
          };
        }),
      };
    }, NAMED);
    expect(tail.count, `尾部操作行数到 ${tail.count} 件 < 点名的 ${NAMED.length} 件：锚点漂了或这一排压根没渲染（闸门失明）`)
      .toBeGreaterThanOrEqual(NAMED.length);
    expect(tail.missing, `尾部点名件缺席：${tail.missing.join(' / ')}（那件控件从完成态气泡上掉了）`).toEqual([]);
    expect(tail.feedback, `反馈入口不是恰好一枚（实到 ${tail.feedback}）`).toBe(1);
    expect(tail.foreign, `尾部出现第三类控件／被别的容器包了一层：${tail.foreign.join(' / ')}（同排判据要求只有 .msg-act-btn 与 .msg-meta）`)
      .toEqual([]);
    for (const c of tail.items) {
      expect(c.fs, `尾部控件「${c.cls}」字号 ${c.fs}px ≠ 14px（同排必须同档，字号只在 .msg-actions 声明一次）`).toBe(14);
      expect(c.off, `尾部控件「${c.cls}」垂直中心偏离行中心 ${c.off}px ⇒ 已不是一排（高低不统一）`).toBeLessThanOrEqual(6);
    }

    await page.screenshot({ path: 'artifacts/tf1_translate_flow.png' });
  });
});
