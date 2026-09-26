// ============================================================================
// e2e/pixel_uat.spec.ts — 前端像素级 UAT（前端↔后端联调 + 视觉/布局/关键链路）
// 覆盖：
//   P1 登录页 / 注册页可渲染
//   P2 工作台（对话翻译）输入→发送→收到译文（真实后端+mock LLM 全链路）
//   P3 自服务页：余额/邀请/套餐/账号
//   P4 工单 / 对照编辑
//   P5 后台管理各面板（overview/models/kb/tickets/system/personal）
//   P6 公开页：pricing / docs
//   P6b 主投白底件运行时渲染为纯白 #FFFFFF（getComputedStyle 实测）
//   P6c 后端直出的 /openapi/docs 与 /office/taskpane 渲染为 §1.1 单色纯黑
//   P7 像素级检查：关键元素可见 + 无横向溢出（scrollWidth<=clientWidth）
// 全部截图存 artifacts/ 供人工复核
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/pixel_uat.spec.ts
// ----------------------------------------------------------------------------
// ★ 2026-09-22 载入闸门口径（详见 playwright.config.ts 的 #66 注释）：
//   冷启动占位（WordSwap 换词动效）要演满 3 个语种拍次（约 9s）才放行。
//   登录后用例凡要「截图 / 量横向溢出 / 读整页文案」，都必须先等闸门撤销，
//   否则截到的只是占位页、溢出检查等于没做（历史上 P4/P5 就是这样静默通过的）。
// ★ 语种口径：本文件通篇用中文文案与 /工单|余额|套餐/ 类中文正则断言，
//   依赖 playwright.config.ts 里钉死的 locale:'zh-CN'。首访语言已改为「按浏览器
//   语言自动检测」，不钉住就会整批落到 en 而随机翻红（不是产品缺陷）。
// ============================================================================
import { test, expect, Page } from '@playwright/test';

// BASE：本地 CI 起 8899 预览实例；e2e 禁写死生产域名（AGENTS §6），需打生产请走 e2e-manual/
const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';
// 截图小件：把页面截图落 artifacts/<name>.png，供人工复核像素渲染
const shot = (p: Page, name: string) => p.screenshot({ path: `artifacts/${name}.png`, fullPage: false });

// WCAG 对比度实计算：把 getComputedStyle 返回的 rgb()/rgba() 前景色对工作台卡面求比值
// （口径与 src/styles/readability.test.ts 一致），供「弱文字不得偏暗」类断言使用。
// ★ 〇-P（2026-09-23）撤销 〇-O：默认卡面回到交付值 #0E1014；调用点一律显式传实际底色的，不受默认值影响。
function contrastOnCard(fg: string, bg = '#0E1014'): number {
  const parse = (s: string): [number, number, number] => {
    const m = s.match(/rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/);
    if (!m) throw new Error(`无法解析颜色：${s}`);
    return [+m[1], +m[2], +m[3]];
  };
  const lum = (rgb: [number, number, number]) => {
    const [r, g, b] = rgb.map((v) => v / 255)
      .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const hex = (h: string): [number, number, number] =>
    [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16)) as [number, number, number];
  const a = lum(parse(fg)), b = lum(hex(bg));
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

// 登录态注入：API 登录拿 token → localStorage 种入（与产品实际登录态等价）
// 默认测试账号：可用 UAT_USER/UAT_PASS 覆盖（不同联调库种子用户不同）
// 用 addInitScript 而非 page.evaluate：它在该 context 的**每次导航前**都重跑一次，
// 于是同一条用例里连续 goto('/tickets')→goto('/editor')… 不会被 Root 的未登录分诊弹回登录页
// （走的是「有 token → 会话恢复中 → restoreGate 补拍」这条真实冷启动路径）。
async function login(page: Page, user = process.env.UAT_USER || 'uatuser_a', pass = process.env.UAT_PASS || 'uatpass123') {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
}

test.describe('像素级 UAT', () => {
  test('P1 登录页渲染', async ({ page }) => {
    // 未登录访问 '/' 出的是营销落地页（Root 的未登录分支），此时本地无 token ⇒
    // restoring 恒 false ⇒ 不触发载入闸门补拍，所以这条用例不需要等闸门撤销（也不该等）。
    // 只断「能渲染 + 有登录入口文案」，视觉细节由 p1_login.png 人工看。
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const txt = await page.locator('body').innerText();
    expect(txt).toContain('登录');
    await shot(page, 'p1_login');
  });

  test('P2 工作台对话翻译全链路（发送→译文回流）', async ({ page }) => {
    await login(page);
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const ta = page.getByPlaceholder(/输入要翻译的文本/);
    await expect(ta).toBeVisible();
    await ta.fill('今天天气怎么样，适合出门吗？');
    // ★ 2026-09-18 UI 迁移（图 07-translate）：工作台主按钮文案「发送」→「翻译」。
    //   用兼容正则锁「触发翻译的主按钮」语义而非字面文案，措辞再变也不会误红。
    await page.getByRole('button', { name: /^(发送|翻译|Send|Translate)$/ }).click();
    // 60s 上限：这条要穿过完整真实链路（闸门放行 → 估价 → mock LLM 往返 → 计量落库 →
    // 前端回读），CI 上后端刚起、模型路由首请求偏慢时 20~30s 很常见，宁松不假红。
    await expect(page.getByText(/TranslatedEN|翻译结果/).first()).toBeVisible({ timeout: 60000 });
    await shot(page, 'p2_workbench');
  });

  // ★ #36 + UI 真值（2026-09-22 口径改向）结构/字阶双断言：
  //   #36 即时翻译的输入框与译文合并进同一张对话框卡（旧版是「输入卡 + 下方消息流」两块），
  //      同时文件翻译入口从即时翻译下线（文件翻译统一走工单通道），故卡内不得再有 file input；
  //   字阶/灰阶这一半原先钉的是「#35 整体上调一档」的结果，现已随全站按 UI 交付稿还原改为
  //      等值锁（真值来自 前端及UI相关/UI-ANNOTATIONS.md 与交付包）——这里用 getComputedStyle
  //      实测的是「渲染出来的值等于交付值」，与 readability.test.ts 的源码级锁互补：
  //      单测拦「CSS 里被改回去」，本处拦「运行时内联样式/换肤把值盖掉」。
  test('P2b 工作台合并对话框结构（消息在上、输入贴底单卡单排）+ 字阶/灰阶按交付真值', async ({ page }) => {
    await login(page);
    await page.goto('/');
    // 结构锁走 class（.cw-dialog*）而非文案：文案受 i18n 与措辞调整影响，
    // 而「消息流在滚动区、输入区在框脚」这套 DOM 形态才是交付口径。
    const dialog = page.locator('.cw-dialog');
    await expect(dialog).toBeVisible();
    // ★ 〇-LK（2026-09-22）形态定稿：输入区必须在**框脚**、消息流在它上方。
    //   上一版这里锁的是「输入框在滚动区内」，滚动区第一位就是输入框——
    //   用户看后判「你们家对话框是放顶部的啊」，故本锁连同下面的几何锁一起改向。
    await expect(dialog.locator('.cw-dialog-foot textarea')).toBeVisible();
    expect(await dialog.locator('.cw-dialog-body textarea').count(), '输入框不得回到消息流里').toBe(0);
    await expect(dialog.locator('.cw-dialog-body')).toBeVisible();
    // 几何锁（运行时实测，防「DOM 顺序对但 CSS 把它顶回上面」）。
    // ★ 〇-M 等值锁 → 〇-N 重定档 → 〇-P 回落（2026-09-24 复跑实测校准）：
    //   〇-N 的「框脚 114 / 输入卡 94 / 工具条 50」是在描边 2px 档下测的；〇-P 把描边
    //   回落到交付档 1.2px 后，mt-seg 实测 34px（28 项 + 4 内垫 + 2×1 描边取整），
    //   工具条=34+6+8=48、输入卡=48+40+2=90、框脚=109——即回到 〇-M 的原实测值。
    //   这里是 1280×720、默认一个目标语种、空输入下的等值锁。
    //   为什么必须等值而不是「≤ 某上限」：上一版就是只锁方向，结果四排一路长到 250+px 也没人拦，
    //   直到用户判「太长太占空间」才返工（返工面 = 全站唯一的输入区）。
    //   工具条「只有一排」用行号集合判：把换行（视觉上变两排）直接判红，而不是靠高度猜。
    const geo = await dialog.evaluate((el) => {
      const d = el.getBoundingClientRect();
      const ta = el.querySelector('.cw-dialog-foot textarea')!.getBoundingClientRect();
      const foot = el.querySelector('.cw-dialog-foot')!.getBoundingClientRect();
      const body = el.querySelector('.cw-dialog-body')!.getBoundingClientRect();
      const composer = el.querySelector('.cw-composer')!.getBoundingClientRect();
      const toolbar = el.querySelector('.cw-toolbar')!;
      const tb = toolbar.getBoundingClientRect();
      // 零高占位（把主按钮推到行尾的 flex spacer）不参与排数统计
      const live = [...toolbar.children].filter((c) => c.getBoundingClientRect().height > 0);
      const rows = new Set(live.map((c) => Math.round(c.getBoundingClientRect().top / 24)));
      return {
        dialogTop: d.top, dialogBottom: d.bottom, height: d.height, taTop: ta.top, footBottom: foot.bottom, footTop: foot.top, bodyTop: body.top,
        footH: Math.round(foot.height), composerH: Math.round(composer.height), toolbarH: Math.round(tb.height), taH: Math.round(ta.height),
        toolbarRows: rows.size, liveControls: live.length,
      };
    });
    expect(geo.taTop, `输入框顶边 ${geo.taTop} 不应高于消息流顶边 ${geo.bodyTop}`).toBeGreaterThan(geo.bodyTop);
    // 框脚必须承在对话框最底部（亚像素容差 4px）：它一旦被顶回消息流中间，就是「输入框放顶部」的原缺陷形态
    expect(Math.abs(geo.dialogBottom - geo.footBottom), `框脚未贴对话框底沿（差 ${geo.dialogBottom - geo.footBottom}px）`).toBeLessThan(4);
    expect(geo.footTop, '框脚必须低于消息流起点').toBeGreaterThan(geo.bodyTop);
    // 输入区占住下半部：45% 分位给消息流留了多数高度，输入框若跑到上半部即为形态回退
    expect(geo.taTop, `输入框未落在对话框下半部（top ${geo.taTop}，对话框 ${geo.dialogTop}~${geo.dialogBottom}）`)
      .toBeGreaterThan(geo.dialogTop + geo.height * 0.45);
    // ★ 〇-M 等值锁：单卡 + 单排工具条的尺寸档
    expect(geo.liveControls, '工具条控件数与实现不符（源语言胶囊/语种/模式/缩翻/主按钮…）').toBeGreaterThanOrEqual(6);
    expect(geo.toolbarRows, `工具条必须是**一排**，实测 ${geo.toolbarRows} 排（chips 或控件换行即回退成多排）`).toBe(1);
    expect(geo.taH, `空态输入框实高 ${geo.taH}px ≠ 〇-N 真值 40px（一行自适应档）`).toBe(40);
    expect(geo.toolbarH, `工具条实高 ${geo.toolbarH}px ≠ 〇-P 真值 48px（描边回落 1.2px 后的实测档）`).toBe(48);
    expect(geo.composerH, `输入卡实高 ${geo.composerH}px ≠ 〇-P 真值 90px（输入 40 + 工具条 48 + 上下边框 2）`).toBe(90);
    expect(geo.footH, `框脚实高 ${geo.footH}px ≠ 〇-P 真值 109px（四排压成一排是本批的交付口径）`).toBe(109);
    // ★ 〇-M 负向锁：语种面板在贴底的工具条里必须**向上弹**——宿主 .cw-dialog 是 overflow:hidden，
    //   向下弹会被裁掉（表现为「点了没反应」），而 DOM 顺序与结构锁全都扫不到这种失效。
    await page.locator('[data-testid="lang-multi-trigger"]').click();
    const panel = await page.locator('[data-testid="lang-multi-panel"]').evaluate((el) => {
      const p = el.getBoundingClientRect();
      const trig = document.querySelector('[data-testid="lang-multi-trigger"]')!.getBoundingClientRect();
      return { cls: el.className, top: Math.round(p.top), bottom: Math.round(p.bottom), trigTop: Math.round(trig.top), vh: innerHeight };
    });
    expect(panel.cls, '贴底触发时面板必须走 .lms-panel--up 向上弹').toContain('lms-panel--up');
    expect(panel.bottom, `面板底沿 ${panel.bottom} 必须收在触发框上沿 ${panel.trigTop} 之上（否则被 overflow 裁掉）`).toBeLessThanOrEqual(panel.trigTop);
    expect(panel.top, `面板顶沿 ${panel.top} 越出视口上方`).toBeGreaterThanOrEqual(0);
    // ★ 〇-N（2026-09-23 用户投诉第 1 条「黑色的 UI 不该配黑色的字」）：选项这一层必须实测看得见。
    //   根因（文字色只挂在 html[data-theme='dark'] 覆写层 + 主题默认 auto 跟随系统）由
    //   dark_admin_upload.spec.ts 的浅色宿主 D1 钉住；本锁补的是「面板展开后逐档量对比度」，
    //   因为根因修好后仍可能有页面层把选项文字写成弱灰——那类回退 DOM 锁扫不出来。
    const optInfo = await page.locator('[data-testid="lang-multi-panel"]').evaluate((el) => {
      const hex = (c: string) => '#' + (c.match(/\d+(\.\d+)?/g) || []).slice(0, 3).map((n) => (+n).toString(16).padStart(2, '0')).join('');
      const opts = [...el.querySelectorAll('[role="option"]')];
      const pick = (n?: Element) => (n ? { color: getComputedStyle(n).color, fs: parseFloat(getComputedStyle(n).fontSize) } : null);
      return {
        panelBg: hex(getComputedStyle(el).backgroundColor), count: opts.length,
        on: pick(opts.find((o) => o.getAttribute('aria-selected') === 'true')),
        off: pick(opts.find((o) => o.getAttribute('aria-selected') !== 'true')),
      };
    });
    expect(optInfo.count, '语种面板没有渲染出可选项').toBeGreaterThanOrEqual(10);
    expect(optInfo.off?.fs, `语种选项字号 ${optInfo.off?.fs}px ≠ 〇-N 后档 15px`).toBe(15);
    for (const [k, o] of [['未选', optInfo.off], ['已选', optInfo.on]] as const) {
      if (!o) continue;
      expect(contrastOnCard(o.color, optInfo.panelBg), `${k}项文字色 ${o.color} 对面板底 ${optInfo.panelBg} 不应低于 4.5:1（正文档）`).toBeGreaterThanOrEqual(4.5);
    }
    await page.locator('[data-testid="lang-multi-trigger"]').click(); // 收起，别把浮层留给后续断言
    // 文件入口下线：全站工作台不应再挂隐藏的原生 file input
    expect(await page.locator('input[type="file"]').count(), '即时翻译已移除文件上传入口').toBe(0);
    // 字号真值等值锁（★ 2026-09-22 全站还原批立锁，★ 〇-N 2026-09-23 按新档重定）：
    // 单向下限锁正是把配色与字阶一路推离交付稿的元凶（它只拦变小，不拦变大），所以一律写「恰等于」。
    // 〇-N 的用户后令是「字号 +2px、线框加粗、不改颜色」，故这里全部由交付档整体 +2：
    // 13→15 / 14→16 / 12→14 / 15→17。色值一档未动（见下面的真值灰阶锁）。
    // 输入框 15 = 组件库 .lc-textarea 冻结档（交付 13 + 〇-N 后令 2），不是页面层自选值。
    const taFs = await dialog.locator('.cw-dialog-foot textarea').evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(taFs, `输入框字号 ${taFs}px ≠ 〇-N 后组件库档 15px`).toBe(15);
    // 正文气泡 16 = theme.css .bubble（交付 14 + 〇-N 后令 2）
    const bubbleFs = await page.locator('.bubble').first().evaluate((el) => parseFloat(getComputedStyle(el).fontSize)).catch(() => -1);
    if (bubbleFs >= 0) expect(bubbleFs, `气泡字号 ${bubbleFs}px ≠ 〇-N 后档 16px`).toBe(16);
    // 弱说明文字：颜色必须落在真值灰阶集合内（提亮批自造的 #878D95/#7A828E/#9AA2AF 一律红灯），
    // 并顺手核对该灰阶对页面底 #000000 的实际比值是否等于真值口径（≥4:1，图形/弱文字档）。
    const welcomeColor = await dialog.locator('.cw-welcome').evaluate((el) => getComputedStyle(el).color);
    const TRUTH_RGB = ['#E7E9EA', '#9AA0AA', '#71767B', '#8A9099', '#536471']
      .map((h) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16)).join(','));
    const welcomeRgb = (welcomeColor.match(/\d+(\s*,\s*\d+){2}/) || [''])[0].replace(/\s/g, '');
    expect(TRUTH_RGB, `弱文字色不在真值灰阶内：${welcomeColor}`).toContain(welcomeRgb);
    expect(contrastOnCard(welcomeColor, '#000000'), `真值灰阶在页面底上不应低于 3.4:1（${welcomeColor}）`).toBeGreaterThanOrEqual(3.4);
    // 顶栏按 §2.2 骨架的 〇-N 后档：行高仍是 38（骨架未加厚），字阶整体 +2 ⇒ 品牌 16 Bold、
    // 导航 15px 胶囊、语种钮 14px。三条锁配对使用：字号等值防「调档」，实高 ≤38 防「折行/撑破行」，
    // 行高本身防顶栏被改厚——字变大后最容易先崩的就是「一行放得下吗」这一条。
    const headerH = await page.locator('.app-header').evaluate((el) => el.getBoundingClientRect().height);
    expect(headerH, `顶栏实高 ${headerH}px ≠ §2.2 的 38px`).toBe(38);
    const brand = page.locator('.app-header .brand').first();
    // 品牌可能是图片 logo（此时 .brand 内只有 <img>），字号锁改为落在 img 的宿主上量，
    // 不能因为白标形态不同就假红。
    expect(await brand.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)), '品牌字号 ≠ 〇-N 后档 16px').toBe(16);
    const tab = page.locator('.app-header .app-tab').first();
    // 三个工作台 Tab 共用同一条 .app-tab 规则，取首个即可代表该字阶档（不必逐个数）。
    // 高度锁之前必须先断可见：隐藏元素 getBoundingClientRect() 恒为 0，
    // `<=38` 会静默通过——那正是「断言永远绿」一类的假绿，比翻红更危险。
    await expect(tab, '工作台 Tab 未渲染').toBeVisible();
    const tabFs = await tab.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
    expect(tabFs, `工作台 Tab 字号 ${tabFs}px ≠ 〇-N 后档 15px`).toBe(15);
    const tabH = await tab.evaluate((el) => el.getBoundingClientRect().height);
    expect(tabH, `工作台 Tab 实高 ${tabH}px ⇒ 文案已折行或控件被撑高`).toBeLessThanOrEqual(38);
    const langBtn = page.locator('.app-header .lang-sel-btn').first();
    await expect(langBtn, '顶栏语种下拉未渲染').toBeVisible();
    // 语种钮同锁：#23 把二元 EN 切换换成 12 语种下拉后，中文语种名（如「简体中文」）
    // 一度把钮撑成两行，故字号真值与「单行实高」两条都要钉，缺一漏一半回归。
    const langH = await langBtn.evaluate((el) => el.getBoundingClientRect().height);
    expect(langH, `语种钮实高 ${langH}px ⇒ 文案已折行`).toBeLessThanOrEqual(38);
    expect(await langBtn.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)), '语种钮字号 ≠ 〇-N 后档 14px').toBe(14);
    await shot(page, 'p2b_workbench_merged');
  });

  // ★ 〇-P（2026-09-23 用户后令「严格按 UI 交付稿来」）运行时等值锁：撤销 〇-O 的纯白框与 +8 面色。
  // 为什么必须有一侧运行时锁：〇-O/〇-P 的改动 90% 落在令牌层（tokens.css / theme.css 的 --*-line、
  // --*-card），src/styles/readability.test.ts 的 A/F 段量的是**源码里的声明**，而「令牌声明改了、
  // 组件被更具体的页面规则盖住」这类失效只有浏览器算完层叠才看得见——历史上〇-L 的灰描边
  // 就是这么在源码锁全绿的情况下留在页面上的。
  test('P2d 〇-P 灰阶框 + 交付面色台阶运行时等值（工作台 + 后台）', async ({ page }) => {
    const rgb = (h: string) => {
      const v = [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16));
      return `rgb(${v[0]}, ${v[1]}, ${v[2]})`;
    };
    // 交付灰阶框档（§1.1 border-1…7），运行时实测必须落在这一族里
    const BORDER_GRAY = ['#8B939F', '#6E7683', '#5A6270', '#464C58', '#424956', '#3A404C', '#2A2F3A'].map(rgb);
    // 交付面色台阶档（probe 里把 computed backgroundColor 转回 hex 再比，避免 rgb/hex 两套写法混着比）
    const FACE_STEPS = ['#0E1014', '#16181C', '#0A0B0D', '#000000', '#050607'];
    // 量一个元素：边框色/宽 + 背景（背景写成 hex 便于与台阶档直接等值比对）。
    // ⚠️ probe/hex 必须定义在 evaluate 回调**内部**：回调是在浏览器进程里执行的，
    // 引用 Node 侧闭包会直接 ReferenceError（首版就是这么写，P2d 恒红而非假绿）。
    // —— 工作台：对话框框、输入卡、语种胶囊 ——
    await login(page);
    await page.goto('/');
    await expect(page.locator('.cw-dialog'), '工作台对话框未渲染（载入闸门或选择器改名）').toBeVisible({ timeout: 30000 });
    const wb = await page.evaluate(() => {
      const hex = (c: string) => '#' + (c.match(/\d+(\.\d+)?/g) || []).slice(0, 3)
        .map((n) => (+n).toString(16).padStart(2, '0')).join('').toUpperCase();
      const probe = (el: Element) => {
        const s = getComputedStyle(el);
        return {
          bw: [s.borderTopWidth, s.borderRightWidth, s.borderBottomWidth, s.borderLeftWidth],
          bc: [s.borderTopColor, s.borderRightColor, s.borderBottomColor, s.borderLeftColor],
          bg: hex(s.backgroundColor),
        };
      };
      const dialog = document.querySelector('.cw-dialog')!;
      const composer = dialog.querySelector('.cw-composer')!;
      const chip = document.querySelector('.app-header .lang-sel-btn')!;
      return {
        dialog: probe(dialog), composer: probe(composer), chip: probe(chip),
        pageBg: getComputedStyle(document.body).backgroundColor,
        // 框线不得残留「〇-O 的纯白」：顶边是纯白即红（透明边算未画框）。
        // 灰阶判据与源码锁 readability.test.ts 的 BORDER_HEX_ALLOW 同口径：〇-P **要求**框线落在
        // 不透明灰阶档（#464C58/#3A404C/#424956…），半透明白（.tag-lang 这类弱标签、
        // 加载转圈的 fade）与语义状态边一律放行。
        whiteFrame: [...dialog.querySelectorAll<HTMLElement>('*')].filter((n) => {
          const s = getComputedStyle(n);
          const w = parseFloat(s.borderTopWidth);
          if (w === 0 || s.borderTopColor === 'rgba(0, 0, 0, 0)') return false;
          if (/^rgba\((255, 255, 255|231, 233, 234), /.test(s.borderTopColor)) return false;
          // 语义状态边（判错红 / 危险红底 / 琥珀）放行，它们不是框线档
          if (/rgb\((229|248|210),/.test(s.borderTopColor)) return false;
          return s.borderTopColor === 'rgb(255, 255, 255)';
        }).map((n) => `${n.className || n.tagName}:${getComputedStyle(n).borderTopColor}`),
      };
    });
    expect(BORDER_GRAY, `对话框顶边 ${wb.dialog.bc[0]} 不在交付灰阶档上（〇-O 纯白已作废）`).toContain(wb.dialog.bc[0]);
    // 边宽锁的是「取整后的等值档」：交付源码 1.2px 在 Chrome 151 的 CSSOM 里按设备像素
    // 取整上报（实测 dpr=1 与 dpr=2.5 均回 '1px'，一次性脚本量过），所以运行时**不可能**
    // 读到字面 '1.2px'——那是本锁 09-24 复跑翻红的根因，不是产品回退。
    // 1.2px 源码等值由 `readability.test.ts` A 段（令牌等值）与 G/H/I 段负向清零承担；
    // 本锁的射程 = 运行时内联覆写：抬回 2px（〇-N 旧档）或框被抹成 0px 都在此判红。
    expect(wb.dialog.bw[0], `对话框边宽 ${wb.dialog.bw[0]} ≠ 〇-P 交付档 1.2px 的取整等值 1px（2px/0px 一律视为回退）`).toBe('1px');
    expect(wb.dialog.bg, `对话框面 ${wb.dialog.bg} ≠ 交付面板档 #0E1014`).toBe('#0E1014');
    // 语种钮是「有框才谈档」：它当前确实带胶囊边，但真不画框也不算跑偏
    if (parseFloat(wb.chip.bw[0]) > 0) {
      expect(BORDER_GRAY, `语种钮边 ${wb.chip.bc[0]} 不在交付灰阶档上`).toContain(wb.chip.bc[0]);
    }
    expect(wb.composer.bc.some((c) => BORDER_GRAY.includes(c)) || wb.composer.bw.every((w) => parseFloat(w) === 0),
      `输入卡边 ${JSON.stringify(wb.composer.bc)} 既不在交付灰阶档也非「无框（嵌在框脚里）」`).toBeTruthy();
    expect(wb.pageBg, '页面底必须仍是纯黑（〇-P 分层靠面色台阶，不是把底抬亮）').toBe('rgb(0, 0, 0)');
    expect(wb.whiteFrame, `工作台内仍有 〇-O 遗留的纯白框线：\n${wb.whiteFrame.join('\n')}`).toEqual([]);
    // —— 后台：侧栏外壳与统计卡 ——
    await login(page, 'admin', 'Admin@1234');
    await page.goto('/admin');
    await expect(page.locator('body'), '/admin 应含后台面板').toContainText(/模型|知识库|工单|系统/, { timeout: 30000 });
    const probeAdminFrames = () => page.evaluate(() => {
      const hex = (c: string) => '#' + (c.match(/\d+(\.\d+)?/g) || []).slice(0, 3)
        .map((n) => (+n).toString(16).padStart(2, '0')).join('').toUpperCase();
      // 取后台里「画了框」的代表件：卡片/面板类元素（class 含 card|panel|box|table|input）
      const els = [...document.querySelectorAll<HTMLElement>('div,section,table,input,button,select')]
        .filter((n) => parseFloat(getComputedStyle(n).borderTopWidth) > 0
          && getComputedStyle(n).borderTopColor !== 'rgba(0, 0, 0, 0)'
          && /card|panel|box|table|input|btn|button|chip|pill/i.test(n.className || ''));
      const stillWhite = els.filter((n) => {
        const c = getComputedStyle(n).borderTopColor;
        // 语义状态边（琥珀提示盒 / 判错红 / 危险红底）不算框线档，白名单同工作台侧；
        // 半透明白（白族降透明）同样放行，判据与源码锁 BORDER_HEX_ALLOW 一致
        if (/^rgba\((255, 255, 255|231, 233, 234), /.test(c)) return false;
        return c === 'rgb(255, 255, 255)';
      });
      const card = els[0];
      return {
        sampled: els.length,
        // ★ 09-27 自诊断：计数临界翻红时（3 vs >3）光看数字定位不了「少的是哪一件」，
        //   把扫到的类名随失败信息带出来，一次复跑就能对账是哪块框掉了。
        sample: els.slice(0, 8).map((n) => `${n.tagName}.${String(n.className).slice(0, 40)}`),
        stillWhite: stillWhite.slice(0, 12).map((n) => `${n.className}:${getComputedStyle(n).borderTopColor}`),
        cardBg: card ? hex(getComputedStyle(card).backgroundColor) : '',
      };
    });
    // ★ 09-27 复跑红账（批 I-10 收尾）：外壳文案到位 ≠ 面板件已挂载——冷启动载入闸门撤下后
    //   统计卡/表单件仍是异步数据回来才渲染，一次性快照会撞进「只剩壳的 3 件」窗口
    //   （本轮首跑实扫 3、retry 即过＝典型时序红）。改成轮询到件数稳定再量，
    //   「>3 防空转」的锁语义一字不动，只把取值时机钉稳；超时后失败信息仍带类名清单。
    let ad = await probeAdminFrames();
    await expect
      .poll(async () => { ad = await probeAdminFrames(); return ad.sampled; },
        { message: `后台带框件迟迟没载入齐（等 20s 仍 ≤3 即产品问题）：\n${ad.sample.join('\n')}`, timeout: 20000 })
      .toBeGreaterThan(3);
    expect(ad.sampled, `后台一个带框的卡片/输入件都没扫到 ⇒ 本锁已空转（选择器或类名口径变了）。实扫 ${ad.sampled} 件：\n${ad.sample.join('\n')}`).toBeGreaterThan(3);
    expect(ad.stillWhite, `后台仍有 〇-O 遗留的纯白框线：\n${ad.stillWhite.join('\n')}`).toEqual([]);
    expect(FACE_STEPS, `后台首个带框件的面 ${ad.cardBg} 不在交付面色台阶档上`).toContain(ad.cardBg);
    await shot(page, 'p2d_gray_frames');
  });

  // ★ 〇-Q（2026-09-25 批G F-18）WordSwap 尺寸档运行时等值锁：规格先钉在 UI-ANNOTATIONS.md §1.5
  // （两元组：宽屏现档 + ≤640px 窄屏档），源码级锁是 readability.test.ts J 段；本条量的是
  // 「浏览器在该视口真正算出来的 computed 值」，防别的样式表在后面把 .ws-* 再覆写一层。
  // 为什么挂合成节点而不是等冷启动占位：.ws--lg 只在载入闸门那段（≈9s）窗口里存在，
  // 跟闸门抢时序读它会做成交替假红/假绿的 flaky 锁；而本锁要证的只是 theme.css 的档在
  // 真实文档里按视口生效——挂一个与 WordSwap 同构（span.ws[档名] > ws-tag/ws-w>ws-wt/ws-r）
  // 的节点，getComputedStyle 走的就是同一份层叠与媒体查询命中，量完即摘，不留渲染残渣。
  // ⚠️ 亚像素教训（P2d 血案）：Chrome 的 CSSOM 把 border/几何 used-value 按设备像素取整上报，
  //   所以红线只量整数档（3px/2px），基础红线 1.6px 归 J 段源码锁管，不在此实测；
  //   font-size 走的是 computed 值而非布局 used-value（本批 360/1440 实跑验证过 13.5px 原样回读），
  //   故五档全部 w/r/tag/gap 等值进本锁。
  test('P2e WordSwap 尺寸档 360/1440 双视口运行时等值（〇-Q §1.5）', async ({ page }) => {
    // 无需登录态：theme.css 由 main.tsx 全局引入，任何路由下文档都带着这套规则；
    // '/' 未登录直出营销页，body 可见即证明 SPA 真的挂载了（about:blank 量不到样式层，不作数）。
    await page.goto('/');
    await expect(page.locator('body')).toBeVisible();
    const probe = () => page.evaluate(() => {
      const mk = (extra: string) => {
        const host = document.createElement('div');
        // DOM 与 WordSwap.tsx 的渲染结构一致（tag → w>wt → r），档名挂在根节点上，
        // 若组件类名口径变了，J 段源码锁与本锁会一起红灯，不存在静默失配。
        host.innerHTML =
          `<span class="ws${extra ? ' ' + extra : ''}">` +
          `<span class="ws-tag">EN</span>` +
          `<span class="ws-w"><span class="ws-wt">compare</span></span>` +
          `<span class="ws-r">benchmark</span></span>`;
        document.body.appendChild(host);
        const root = host.firstElementChild as HTMLElement;
        const fs = (sel: string) => parseFloat(getComputedStyle(root.querySelector(sel)!).fontSize);
        const out: { gap: number; tag: number; w: number; r: number; strike?: number } = {
          gap: parseFloat(getComputedStyle(root).columnGap),
          tag: fs('.ws-tag'), w: fs('.ws-w'), r: fs('.ws-r'),
        };
        // 红线粗只量大档（3px/2px 整数档）；基础档 1.6px 不读（见上方亚像素口径）。
        if (extra === 'ws--lg') {
          out.strike = parseFloat(getComputedStyle(root.querySelector('.ws-wt')!, '::before').height);
        }
        host.remove();
        return out;
      };
      // 五档全量回传：宽窄两轮各测一次，「不缩」档由两轮 toEqual 相等来钉，而不是只测宽屏。
      return {
        lg: mk('ws--lg'), base: mk(''), draft: mk('draft-ws'), segs: mk('file-segs-ws'), tk: mk('tk-prog-ws'),
      };
    });
    // 先钉 1440（>640px，媒体查询不命中 ⇒ 宽屏现档），再压到 360（≤640px ⇒ 窄屏档）。
    // setViewportSize 会即时重算媒体查询，无需重新 goto。
    await page.setViewportSize({ width: 1440, height: 900 });
    const wide = await probe();
    await page.setViewportSize({ width: 360, height: 800 });
    const narrow = await probe();
    // —— 宽屏现档（§1.5 第一列；.ws--lg 五维 44/52/32/23/3px）——
    expect(wide.lg, `宽屏大档实测 ${JSON.stringify(wide.lg)} ≠ §1.5 现档 44/52/32/23/3px`).toEqual({ gap: 32, tag: 23, w: 44, r: 52, strike: 3 });
    expect(wide.base, `宽屏基础档实测 ${JSON.stringify(wide.base)} ≠ §1.5 现档 16/17/12`).toEqual({ gap: 12, tag: 13.5, w: 16, r: 17 });
    expect(wide.draft, `宽屏草稿档 ≠ §1.5 现档 15/16/9/12`).toEqual({ gap: 9, tag: 12, w: 15, r: 16 });
    expect(wide.segs, `宽屏逐段档 ≠ §1.5 现档 14/15/8/12`).toEqual({ gap: 8, tag: 12, w: 14, r: 15 });
    expect(wide.tk, `宽屏进度档 ≠ §1.5 现档 18/17/10/14`).toEqual({ gap: 10, tag: 14, w: 18, r: 17 });
    // —— 窄屏 ≤640px 档（〇-Q 新钉：大档 26/30/16/14/2px、进度档 15/16/12，gap 10 不缩）——
    expect(narrow.lg, `窄屏大档实测 ${JSON.stringify(narrow.lg)} ≠ §1.5 窄档 26/30/16/14/2px`).toEqual({ gap: 16, tag: 14, w: 26, r: 30, strike: 2 });
    expect(narrow.tk, `窄屏进度档实测 ${JSON.stringify(narrow.tk)} ≠ §1.5 窄档 15/16/12（gap 10 不缩）`).toEqual({ gap: 10, tag: 12, w: 15, r: 16 });
    // —— 「不缩」三档：窄屏实测必须与宽屏**完全相等**（等值传递，不是「差不多小」）——
    expect(narrow.base, '基础档被窄屏偷缩（§1.5 钉不缩）').toEqual(wide.base);
    expect(narrow.draft, '草稿档被窄屏偷缩（§1.5 钉不缩，缩了与文本基线错位）').toEqual(wide.draft);
    expect(narrow.segs, '逐段档被窄屏偷缩（§1.5 钉不缩）').toEqual(wide.segs);
    await shot(page, 'p2e_wordswap_sizes');
  });

  test('P3 自服务页渲染（余额/套餐/账号·企业 + 邀请·个人）', async ({ page }) => {
    await login(page);
    // 每页期望文案写成宽松正则（/余额|积分|充值/）：自服务页的措辞与积分口径常调，
    // 钉死单字会让本文件沦为「改文案就得改 e2e」的负担；这里要锁的是「页面渲染出主体」而非文案本身。
    for (const [path, expectTxt, name] of [
      ['/billing', /余额|积分|充值/i, 'p3_billing'],
      ['/packages', /套餐|包|订阅/i, 'p3_packages'],
      ['/my', /账号|昵称|邮箱/i, 'p3_my'],
    ] as const) {
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      // ★ 2026-09-12 修复竞态：goto 后异步面板（useAsync）尚未渲染即读 innerText 会误报——
      //   改用轮询断言等待目标文案出现（15s 上限），再截图与溢出检查。
      //   ★ 2026-09-22 载入闸门（≥3 语种拍次 ≈9s）后 15s 余量只剩 6s，放宽到 30s。
      await expect(page.locator('body'), `${path} 应含 ${expectTxt}`).toContainText(expectTxt, { timeout: 30000 });
      // 溢出判定留 2px 容差：布局取整/滚动条宽度会带来 1~2px 误差，超过它才算真横向溢出
      // （1280 视口下压不出滚动条的溢出人眼也看不出，但对窄屏与截图复核是隐患）。
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
    // 邀请页仅个人用户可见（产品意图，2026-09 权限收口）：切个人账号（UAT_PERSONAL_USER，默认 uatuser_e；api_uat.sh B6 种入）校验渲染
    await login(page, process.env.UAT_PERSONAL_USER || 'uatuser_e', process.env.UAT_PASS || 'uatpass123');
    await page.goto('/invites');
    // ReferralPanel 依赖 meContext 异步 resolve，需等待邀请内容出现（避免读快于渲染）
    await expect(page.getByText(/邀请码|邀请链接/).first()).toBeVisible({ timeout: 15000 });
    const txt = await page.locator('body').innerText();
    expect(txt, '/invites(个人) 应含邀请内容').toMatch(/邀请好友|邀请码|裂变|奖励/i);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
    expect(overflow, '/invites 横向溢出').toBeFalsy();
    await shot(page, 'p3_invites');
  });

  test('P3-enterprise 企业用户隐藏邀请页（产品意图锁定）', async ({ page }) => {
    await login(page);
    await page.goto('/invites');
    // 反向断言（「不该出现」）没法用「等待元素消失」来表达，只能给一个固定沉降窗口再读整页文案：
    // 400ms 足够覆盖 meContext 的 isPersonal 判定与本用例的渲染帧，
    // 若将来出现假绿（面板延迟挂载晚于 400ms），应改为断言路由跳转到工作台（/invites → Navigate '/'）。
    await page.waitForTimeout(400);
    const txt = await page.locator('body').innerText();
    // 只禁两串面板专属文案：不用泛化的「邀请」，否则顶栏导航/抽屉项里的「邀请有礼」会误伤。
    expect(txt, '企业用户 /invites 不应渲染邀请面板').not.toMatch(/邀请好友 · 多邀多得|我的专属邀请码/);
    await shot(page, 'p3_invites_hidden_enterprise');
  });

  test('P4 工单页与对照编辑渲染', async ({ page }) => {
    await login(page);
    for (const [path, name, marker] of [
      ['/tickets', 'p4_tickets', /工单/],
      ['/editor', 'p4_editor', /原文|译文|上传/],
    ] as const) {
      await page.goto(path);
      // ★ 2026-09-22「至少读完三个语种再载入」：冷启动占位会顶住约 9s，
      //   旧写法只等 body 可见 ⇒ 截图截到换词动效、溢出检查也等于没做。
      //   改为等顶栏（占位撤销后才出现）+ 页面主体文案，再截图与量溢出。
      //   闸门撤销的判据分两层：.app-header 证明「会话恢复闸门」已放行
      //   （恢复期 Root 直接 return 占位组件，整个外壳含顶栏都还没挂载）；
      //   随后的 body 文案锁再兜住「后端探活闸门」（那道闸门只替换 app-main 内容，顶栏已在）。
      await expect(page.locator('.app-header'), `${path} 载入闸门未撤销`).toBeVisible({ timeout: 30000 });
      await expect(page.locator('body'), `${path} 应含页面主体`).toContainText(marker, { timeout: 30000 });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P5 后台管理面板渲染（超管）', async ({ page }) => {
    await login(page, 'admin', 'Admin@1234');
    await page.goto('/admin');
    // ★ 2026-09-22：等冷启动载入闸门（≥3 语种拍次 ≈9s）撤销后再截图。
    //   后台是独立外壳（AdminPage 侧栏，没有 .app-header），故按面板文案等。
    await expect(page.locator('body'), '/admin 应含后台面板').toContainText(/模型|知识库|工单|系统/, { timeout: 30000 });
    await shot(page, 'p5_admin_overview');
    // 逐个点侧栏项而非直连深链：后台面板由 useAdminStore 的当前面板驱动（多数无独立 URL），
    // 点文案是唯一能覆盖「导航 + 面板挂载」的方式。
    // 期望值同样写成宽松正则——面板标题/字段措辞会随版本调整，这里锁的是「该面板真渲染出内容」。
    const panels: [string, RegExp, string][] = [
      ['模型配置', /模型|路由|密钥/i, 'p5_admin_models'],
      ['知识库', /知识|术语|包/i, 'p5_admin_kb'],
      ['工单管理', /工单/i, 'p5_admin_tickets'],
      ['系统设置', /系统|配置/i, 'p5_admin_system'],
      ['个人中心', /邀请|任务|我的/i, 'p5_admin_personal'],
    ];
    for (const [label, rx, name] of panels) {
      const item = page.getByText(label, { exact: true }).first();
      // 侧栏项按 i18n 文案查（exact 防「模型」命中「模型路由说明」之类），未渲染则跳过该项：
      // 后台导航会按角色/开关裁剪，缺项是正常形态；真正的渲染回归由上面 overview 那条锁兜住。
      if (await item.count()) { await item.click(); } else { continue; }
      // ★ 2026-09-12 修复竞态：面板数据异步加载，轮询等待目标文案而非固定 400ms 后一次性读取
      await expect(page.locator('body'), `${label} 面板应含 ${rx}`).toContainText(rx, { timeout: 15000 });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${label} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
  });

  test('P6 公开页渲染（价格/文档）', async ({ page }) => {
    for (const [path, name] of [['/pricing', 'p6_pricing'], ['/docs/terms', 'p6_terms']] as const) {
      // 这两页实现路径不同：/pricing 是 SPA 壳（需 JS 渲染，未登录由 Root 直出营销页），
      // /docs/terms 由后端直出静态 HTML。故只锁「正文非空壳 + 无横向溢出」这条公共底线，
      // 具体文案各自人工看截图——钉死任一方的文案都会把另一种实现绑住。
      await page.goto(path);
      await expect(page.locator('body')).toBeVisible();
      const txt = await page.locator('body').innerText();
      // 阈值取 20 字符而非更大值：只需区分「渲染出内容」与「白屏/空 root」两种形态。
      expect(txt.length, `${path} 空页面`).toBeGreaterThan(20);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
      await shot(page, name);
    }
    // ★ 2026-09-22 白色填充还原批：/docs/* 三页由后端直出内嵌 HTML（public.go），
    //   前端那套令牌锁扫不到它——历史上它就带着旧的蓝靛浅底主题跑了很多批。
    //   这里补一条运行时实测：页面底必须纯黑、主按钮必须纯白，浅底主题一旦复活立刻红灯。
    await page.goto('/docs/terms');
    const docBodyBg = await page.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(docBodyBg, '/docs/terms 页面底 ≠ §1.1 --lc-bg #000000').toBe('rgb(0, 0, 0)');
    const docBtnBg = await page.locator('.header .btn').first().evaluate((el) => getComputedStyle(el).backgroundColor);
    expect(docBtnBg, '/docs/terms 管理后台按钮 ≠ 交付真值白底 #FFFFFF').toBe('rgb(255, 255, 255)');
    const docCard = page.locator('.card').first();
    const docCardBg = await docCard.evaluate((el) => getComputedStyle(el).backgroundColor);
    // ★ 〇-P（2026-09-23）：撤销 〇-O，直出页面板回交付 #0E1014、框线回灰阶 #3A404C
    //   ——两处都得实测，因为 public.go 的令牌块是手抄的第二套真值，源码里改对了、抄漏了都可能。
    expect(docCardBg, '/docs/terms 内容面板 ≠ 交付面板档 #0E1014').toBe('rgb(14, 16, 20)');
    const docCardLine = await docCard.evaluate((el) => getComputedStyle(el).borderTopColor);
    expect(docCardLine, '/docs/terms 内容面板描边 ≠ 交付卡片档 #3A404C').toBe('rgb(58, 64, 76)');
  });

  // P6b 主投白底件运行时必须是纯白（2026-09-22 白色填充还原批新增）
  // 为什么还要一条 e2e：readability.test.ts 的 G 锁读源码，挡不住样式表级联把
  // background 覆写回灰档（历史上 §十/§十一 覆写层就是这么把颜色改跑的）。
  // 这里读 getComputedStyle 拿真实渲染值，rgb(255,255,255) 才是交付真值。
  test('P6b 营销页主按钮与收尾白块渲染为纯白', async ({ page }) => {
    await page.goto('/');
    // 未登录访问 '/' 由 Root 直出营销页；先等主投按钮挂载，选择器一旦改名本条直接红灯
    // （而不是 catch 成假绿）。
    const pri = page.locator('.lc-mkt-btn--pri').first();
    await expect(pri, '营销页主投按钮 .lc-mkt-btn--pri 未渲染，请同步本锁').toBeVisible({ timeout: 30000 });
    expect(await pri.evaluate((el) => getComputedStyle(el).backgroundColor),
      '主投按钮底色 ≠ 交付真值 #FFFFFF（#E7E9EA 是文字/活跃档，做整块填充会显脏偏蓝）').toBe('rgb(255, 255, 255)');
    const cta = page.locator('.lc-cta');
    await expect(cta, '收尾白块 .lc-cta 未渲染，请同步本锁').toBeAttached({ timeout: 30000 });
    expect(await cta.evaluate((el) => getComputedStyle(el).backgroundColor),
      '收尾白块底色 ≠ #FFFFFF').toBe('rgb(255, 255, 255)');
    await shot(page, 'p6b_white_fill');
  });

  // P6c 另两处后端直出页的运行时单色实测（2026-09-22 白色填充还原批新增）
  // /openapi/docs（goldmark 渲染壳）与 /office/taskpane.html（Word 加载项窗格）此前整套是
  // Google 蓝 + indigo + 白底，和 /docs/* 属同一类「前端令牌扫不到」的盲区。
  // 单测（public_ui_test.go）锁源码字面量，这里补真实渲染值，两侧都钉住。
  test('P6c 开放 API 文档页与 Office 窗格渲染为单色纯黑', async ({ page }) => {
    await page.goto('/openapi/docs');
    await expect(page.locator('h1').first(), '/openapi/docs 未渲染出正文，请同步本锁').toBeVisible({ timeout: 30000 });
    expect(await page.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor),
      '/openapi/docs 页面底 ≠ §1.1 --lc-bg #000000').toBe('rgb(0, 0, 0)');
    expect(await page.locator('.lang-btn.on').first().evaluate((el) => getComputedStyle(el).backgroundColor),
      '语言切换活跃档 ≠ 交付真值白底 #FFFFFF').toBe('rgb(255, 255, 255)');
    await shot(page, 'p6c_openapi_docs');

    // Office 窗格页会拉 Office.js（外网 CDN），CI 取不到时 office.initialize 会挂住不渲染，
    // 因此只断言样式壳本身（源码级字面量由 backend-go 的 TestOfficeTaskPaneMonochromeTruth 承担）。
    const pane = await page.evaluate(async () => {
      const res = await fetch('/office/taskpane.html');
      return { status: res.status, text: await res.text() };
    });
    expect(pane.status, '/office/taskpane.html 不可达').toBe(200);
    expect(pane.text, 'Office 窗格仍是浅底/蓝主题').toContain('background:var(--lc-bg)');
    expect(pane.text, 'Office 窗格主按钮仍是蓝底').toContain('background:var(--lc-white);color:#000000');
  });

  test('P7 整体无横向溢出（登录态关键页）', async ({ page }) => {
    await login(page);
    // 本条是「一页一路径」的粗筛：把 P3/P4/P5 各自视角漏掉的溢出（尤其 /admin 独立外壳）
    // 一网打尽。这里不重等闸门撤销：闸门期屏上是居中的换词占位，本来就不会横向溢出，
    // 300ms 只是给异步面板一点沉降时间；真正「截到占位页」的风险由 P4/P5 的闸门锁负责红。
    for (const path of ['/', '/tickets', '/editor', '/billing', '/admin']) {
      await page.goto(path);
      await page.waitForTimeout(300);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 2);
      expect(overflow, `${path} 横向溢出`).toBeFalsy();
    }
  });
});
