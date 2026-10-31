// ============================================================================
// e2e/ticket_delivery.spec.ts — 两段式交付（★ 2026-10-31 补齐 B15／B16）
// 为什么存在：组5 把文件工单的交付形态从「一个 zip」改成「N 个按（文件 × 语种）分开的
//   产物，md 先到、原格式后到」，但运行时面当时只有 jsdom 单测（TicketsPage.artifacts.dom）
//   覆盖渲染分派——**真后端真产物**的那一跳一直没人验过：
//   · 详情接口出新字段 `artifacts`、下载走 `/api/tickets/artifact?id=&aid=`（路径只从库里取），
//     这两条都在服务端，jsdom 判据看不见；
//   · multilang_zip M1 覆盖的是旧 zip 打包链（`/api/tickets/download`），它绿**不等于**
//     新的单件下载链绿——两条是不同的接口与不同的落盘解析。
// 本 spec 钉四条（都是"改坏了客户立刻看得见"的）：
//   TD1 建单 → 完成后详情里产物分组**非空**，且接口侧与 DOM 侧的分组数同源一致；
//   TD2 md 产物**预览非空**且不是 HTML 兜底（预览只认 kind=md，512KB 上限）；
//   TD3 原格式产物**按字节可下载**（200 ＋ content-type 非 text/html ＋ size>0），
//       UI 上「下载原格式」那一枚在场，且 aid 越界必须 404（归属校验）；
//   TD4 ★ 移动档（320）产物区**不把任何滚动祖先撑出横向溢出**，预览 `<pre>` 必须是可滚容器
//       ——「页面整体没有横向溢出」是一句**不完整的话**（AGENTS §一·5 通知浮层那条真事故），
//       所以这里遍历产物区的滚动祖先逐层判 scrollWidth ≤ clientWidth。
// ★ 可达性探针（AGENTS §一·6 红线）：前端对"没有 artifacts 行"有**回落旧 deliverables 清单**
//   的兜底形态，界面照常渲染。所以判序钉死：**先**问详情接口的 artifacts 长度，
//   为 0 直接红在「出栈腿断了」这一句，**禁止**让后面的内容判据对着空壳跑一遍再报
//   "预览为空"——那是把链路故障写成产品缺陷。
// ★ 时序（"md 先到、原格式后到"）**刻意不测**：mock LLM 下两段落盘间隔是毫秒级，
//   拿它当断言只会得到随机红；那一条语义由 ticketsArtifacts.lock.test.ts 与后端单测负责。
// ★ 锚点口径（本仓的坑，写死在这里）：`Link` 组件渲染的是 `<button type="button">`
//   （DataTable.tsx:90），**role 是 button 不是 link**——按 link 取会结构性找不到目标；
//   `tk.detail` 的中文文案是「进度」而不是「详情」。两条都按当前实现取词，
//   并各配 en 文案正则，换语种跑也不翻红。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/ticket_delivery.spec.ts
//      （由 scripts/uat/run_uat.sh 统一编排：真实后端 + mock LLM，勿裸跑；账号由 run_uat 预注册）
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
const USER = process.env.UAT_USER || 'uatuser_a';
const PASS = process.env.UAT_PASS || 'uatpass123';

// 标题带本次运行时间戳：工单列表是轮询回来的真实状态，历史运行留下的同名单会让
// 「定位自己那一行」拿到别人的产物（假绿），也可能把断言打在一张已清理的旧单上（假红）。
const RUN_TAG = 'e2etd' + Date.now();
const TICKET_TITLE = `E2E两段式交付 ${RUN_TAG}`;

// Node 侧请求头要用的裸 token（页面 storage 里的那份 page.request 读不到，同 multilang_zip）。
let TOKEN = '';

/** 登录态注入：API 登录拿 token → sessionStorage（每次导航重种，掉一次就会被弹回登录页假红）。
 *  同时钉 app_lang=zh：首访语言按浏览器语言自动检测，本 spec 的点名词（「文件」「预览」）
 *  取的是中文词典，不钉语种换台机器跑就整批翻红。 */
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败：${JSON.stringify(body)}`).toBeTruthy();
  TOKEN = body.token;
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
  await page.addInitScript(() => localStorage.setItem('app_lang', 'zh'));
}

/** 建一张文件工单（txt ＋ 还原模式 ＋ 默认单语种 en）并等到它跑完，返回工单 id。
 *  要 id 是因为字节级复验必须走 page.request：浏览器点下载只触发落盘，拿不到 buffer。 */
async function createTicket(page: Page): Promise<number> {
  await page.goto('/tickets');
  await page.getByRole('button', { name: /^文件$/ }).click();
  const input = page.locator('#tk-file-input');
  await expect(input, '文件模式下的上传口未渲染').toBeAttached({ timeout: 10_000 });
  await input.setInputFiles([{
    name: `e2e_td_${RUN_TAG}.txt`,
    mimeType: 'text/plain',
    buffer: Buffer.from('天空是蓝色的。\n书籍是人类进步的阶梯。\n'),
  }]);
  const title = page.getByTestId('tk-title');
  await expect(title, '工单标题输入框未渲染').toBeVisible({ timeout: 10_000 });
  await title.fill(TICKET_TITLE);
  await page.getByRole('button', { name: /创建并入队|Create/ }).click();
  // 用「列表里出现这一行」而不是 toast 判定建单成功：toast 会自己消失，行是轮询回来的事实。
  const row = page.locator('tr', { hasText: TICKET_TITLE }).first();
  await expect(row, '建单后列表没有出现这一行').toBeVisible({ timeout: 20_000 });
  await expect(row.getByRole('button', { name: /下载|Download/ }).first(), '150s 内工单未完成（检查 mock LLM/worker）')
    .toBeVisible({ timeout: 150_000 });

  const tid = await page.evaluate(async (arg: { base: string; title: string }) => {
    const r = await fetch(`${arg.base}/api/tickets`, { headers: { Authorization: `Bearer ${sessionStorage.getItem('auth_token')}` } });
    const d = await r.json();
    const rows = d.tickets || d.list || d.data || [];
    const hit = rows.find((x: any) => String(x.title || '').includes(arg.title));
    return hit ? hit.id : 0;
  }, { base: BASE, title: TICKET_TITLE });
  // 定位失败退回 0 会让下面的接口打到 id=0，报一条难懂的 404；先红在正确的位置。
  expect(tid, '未能定位本次测试工单 id').toBeGreaterThan(0);
  return tid as number;
}

/** 读详情接口的 artifacts 全集（Node 侧）：UI 与接口两侧的产物必须同源，
 *  否则「界面有钮、接口 404」这种两侧各自成立的真缺陷会被本用例判绿。 */
async function readArtifacts(page: Page, tid: number): Promise<any[]> {
  const resp = await page.request.get(`${BASE}/api/tickets/detail?id=${tid}`, {
    headers: { Authorization: `Bearer ${TOKEN}` },
  });
  expect(resp.status(), '详情接口响应码').toBe(200);
  const body = await resp.json();
  return Array.isArray(body?.artifacts) ? body.artifacts : [];
}

test.describe('文件工单两段式交付（产物分组 → md 预览 → 原格式按件下载 → 移动档不溢出）', () => {
  test('TD1+TD2+TD3+TD4 建单后产物非空、预览有内容、原格式可下载、移动档不溢出', async ({ page }) => {
    // 真异步工单（排队＋mock LLM 执行＋轮询）＋一轮移动端重排测量
    test.setTimeout(300_000);
    await login(page, USER, PASS);

    const tid = await createTicket(page);
    const artifacts = await readArtifacts(page, tid);

    // ===== TD1 可达性探针：新单必须写出 artifacts 行（判在内容之前） =====
    // 拿到 0 有两种成因，都得红，但必须**说清是哪一种**：
    //   ① 后端根本没写这张表（组5 交付链断了）；
    //   ② 写了但详情接口没出栈（前端静默回落旧 deliverables 清单 ⇒ 界面看着完全正常）。
    expect(artifacts.length, `详情接口 artifacts 为空（工单 ${tid} 已完成）⇒ 两段式交付的出栈腿断了；`
      + '前端此时会回落旧 deliverables 清单，界面照常渲染，所以必须在接口侧先判死').toBeGreaterThan(0);

    // 打开详情（行内展开，不是弹窗）。注意 `Link` 是 button，`tk.detail` 文案是「进度」。
    const row = page.locator('tr', { hasText: TICKET_TITLE }).first();
    await row.getByRole('button', { name: /进度|Progress/ }).first().click();
    const area = page.getByTestId('tk-artifacts');
    await expect(area, '详情里的两段式产物区未渲染').toBeVisible({ timeout: 15_000 });

    // 分组数＝（文件 × 语种）数：接口侧按 `file_id|lang` 去重，DOM 侧按分组 testid 数，
    // 两侧必须一致——不一致就是渲染分派与出栈不同源（同一份数据出两种形状）。
    const expectGroups = new Set(artifacts.map((a) => `${a.file_id || 0}|${a.lang || ''}`)).size;
    const gotGroups = await area.getByTestId('tk-artifact-group').count();
    expect(gotGroups, `产物分组数 DOM=${gotGroups} ≠ 接口=${expectGroups}（分组分派与出栈不同源）`).toBe(expectGroups);
    expect(gotGroups, '产物分组为零').toBeGreaterThan(0);

    // ===== TD2 md 预览非空，且不是 HTML 兜底 =====
    const md = artifacts.find((a) => a.kind === 'md');
    expect(md, `artifacts 里没有 kind=md 那一件（实际 kind：${artifacts.map((a) => a.kind).join('/') || '空'}）`).toBeTruthy();
    const previewBtn = area.getByRole('button', { name: /预览|Preview/ }).first();
    await expect(previewBtn, 'md 产物在，但「预览」入口没渲染').toBeVisible({ timeout: 10_000 });
    await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/tickets/artifact/preview'), { timeout: 30_000 }),
      previewBtn.click(),
    ]);
    const preview = area.getByTestId('tk-preview');
    await expect(preview, '预览请求已完成但预览块未渲染').toBeVisible({ timeout: 15_000 });
    const previewText = (await preview.innerText()).trim();
    expect(previewText.length, '预览内容为空（产物落盘了却没正文）').toBeGreaterThan(0);
    // HTML 兜底是这条链最坏的假绿形态（spa.go 对不存在的路径回整页 index.html 且状态码仍 200）：
    // 预览正文里出现整页 HTML 标记 ⇒ 打到的是兜底页而不是 md 文件。
    expect(/<html|<div id="root"/i.test(previewText), `预览正文疑似 HTML 兜底：${previewText.slice(0, 120)}`).toBeFalsy();

    // 接口侧同一件再读一次：UI 拿到的必须就是接口那份字节。
    const pv = await page.request.get(`${BASE}/api/tickets/artifact/preview?id=${tid}&aid=${md.id}`, {
      headers: { Authorization: `Bearer ${TOKEN}` },
    });
    expect(pv.status(), '预览接口响应码').toBe(200);
    expect(pv.headers()['content-type'] || '', '预览接口 Content-Type 必须是 text/markdown').toContain('text/markdown');
    expect((await pv.text()).trim().length, '预览接口正文为空').toBeGreaterThan(0);

    // ===== TD3 原格式按件下载：UI 那一枚在位 ＋ 接口按字节拿到东西 ＋ aid 越界 404 =====
    const orig = artifacts.find((a) => a.kind === 'original');
    if (!orig) {
      // mock LLM 环境里 txt 还原链有可能只出 md（无原格式件）。这不是缺陷，但**必须留下点名读数**，
      // 且终态无 original 时文案只能是「未生成／生成中」那一档，绝不许写"失败"
      // （把进行中等价成坏了 ⇒ 用户反复重提，是组5 定档时点名的缺陷形态）。
      const pending = area.getByTestId('tk-original-pending');
      await expect(pending, '既无 original 产物，也无"生成中/不可用"的点名').toBeVisible({ timeout: 10_000 });
      const pendingText = await pending.innerText();
      expect(/失败|failed/i.test(pendingText), `无原格式产物时文案写成了失败（应点名"未生成"或"生成中"）：${pendingText}`).toBeFalsy();
      await page.screenshot({ path: 'artifacts/td3_no_original.png' });
    } else {
      const origBtn = area.getByRole('button', { name: /原格式|Original format/ }).first();
      await expect(origBtn, 'original 产物在，但「下载原格式」入口没渲染').toBeVisible({ timeout: 10_000 });
      await Promise.all([
        page.waitForResponse((r) => /\/api\/tickets\/artifact\?/.test(r.url()), { timeout: 30_000 }),
        origBtn.click(),
      ]);
      const dl = await page.request.get(`${BASE}/api/tickets/artifact?id=${tid}&aid=${orig.id}`, {
        headers: { Authorization: `Bearer ${TOKEN}` },
      });
      expect(dl.status(), '单件下载接口响应码').toBe(200);
      const buf = await dl.body();
      const ctype = dl.headers()['content-type'] || '';
      // 三件一起判：200 不等于"发到了文件"（兜底页也是 200）。
      expect(/text\/html/i.test(ctype), `下载响应 Content-Type 是 HTML 兜底：${ctype}`).toBeFalsy();
      expect(buf.length, '原格式产物字节为零').toBeGreaterThan(0);
      expect(buf.slice(0, 64).toString('utf8')).not.toMatch(/^\s*\{?"?success"?\s*:/);
      // aid 越界必须 404（不泄露存在性，也不许把他单的件当本单产物发出去）：
      // 拿一个明显不存在的 aid 打，只判它**不**回 200。
      const bogus = await page.request.get(`${BASE}/api/tickets/artifact?id=${tid}&aid=99999999`, {
        headers: { Authorization: `Bearer ${TOKEN}` },
      });
      expect(bogus.status(), 'aid 越界仍发到文件 ⇒ 归属校验失效').toBe(404);
    }

    // ===== TD4 移动档（320）：产物区不许把任何滚动祖先撑出横向溢出 =====
    await page.setViewportSize({ width: 320, height: 640 });
    // 表格转卡片由 CSS 媒体查询承担，给一帧重排稳定时间再量（量早了测到的是桌面档）
    await page.waitForTimeout(800);
    await expect(area, '缩到 320 后产物区整块消失（移动档重排把它丢了）').toBeVisible();
    const metrics = await area.evaluate((el) => {
      // 遍历滚动祖先逐层判：只问 documentElement 会漏「浮层把某个中间容器撑出横向滚动条」这一族
      // （AGENTS §一·5 通知中心那条实测事故就是这个形态，页面整体读数一动不动）。
      const overflow: { cls: string; scrollWidth: number; clientWidth: number }[] = [];
      let node: HTMLElement | null = el.parentElement;
      while (node) {
        if (node.scrollWidth > node.clientWidth + 1) {
          overflow.push({ cls: node.className || node.tagName, scrollWidth: node.scrollWidth, clientWidth: node.clientWidth });
        }
        node = node.parentElement;
      }
      const pre = el.querySelector('[data-testid="tk-preview"]') as HTMLElement | null;
      const cs = pre ? getComputedStyle(pre) : null;
      return {
        docOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
        overflow,
        preFound: !!pre,
        preOverflowX: cs ? cs.overflowX : '',
        preOverflowY: cs ? cs.overflowY : '',
        preMaxH: cs ? parseFloat(cs.maxHeight || '0') : 0,
      };
    });
    expect(metrics.docOverflow, '320 档整页出现横向滚动（产物区把视口撑宽）').toBe(false);
    expect(metrics.overflow, `320 档产物区的滚动祖先被撑出横向溢出：${JSON.stringify(metrics.overflow)}`).toEqual([]);
    if (metrics.preFound) {
      // 预览必须是**可滚容器**（正文可能很长）：横向不许 visible，纵向必须可滚，max-height 必须在位，
      // 否则长 md 会把整张详情顶出屏外（用户看到"产物区下面什么都没有"）。
      expect(['auto', 'scroll', 'hidden'].includes(metrics.preOverflowX), `预览块横向可滚性失效：overflow-x=${metrics.preOverflowX}`).toBe(true);
      expect(['auto', 'scroll'].includes(metrics.preOverflowY), `预览块纵向不可滚：overflow-y=${metrics.preOverflowY}`).toBe(true);
      expect(metrics.preMaxH, `预览块 max-height 未生效（${metrics.preMaxH}）⇒ 长正文会把详情页顶出屏外`).toBeGreaterThan(0);
    }
    await page.screenshot({ path: 'artifacts/td4_mobile_320.png' });
  });
});
