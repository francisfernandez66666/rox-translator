// ============================================================================
// e2e/dark_admin_upload.spec.ts — 2026-09-14 两项缺陷回归防护
//   D1 管理后台暗色适配：后台骨架/卡片/统计块必须整体为暗，页面主体不得残留
//      近白色块（历史缺陷：admin 内联硬编码浅色 → 黑白混杂）。
//      ★ 〇-N（2026-09-23）追加根因断言：整站只有暗色一档，因此即便浏览器
//        prefers-color-scheme 为浅色、且本地残留三态时代的 app_theme=light，
//        <html data-theme> 仍须为 dark、body computed color 仍须等于文字真值
//        #E7E9EA。旧实现下这一条会翻成 rgb(0,0,0)（文字色只挂在 dark 覆写层，
//        light 档回退浏览器默认黑字）——用户截图报的「纯黑 UI + 黑字」即此。
//   U1 文件工单上传（multipart）：FormData 上传必须成功建单（历史缺陷：core.ts
//      request() 强制 Content-Type: application/json 抹掉 boundary → 400「文件解析
//      失败或超过大小上限（40MB）」）。
// 运行：BASE_URL=http://127.0.0.1:8899 npx playwright test e2e/dark_admin_upload.spec.ts
// ============================================================================
import { test, expect, Page } from '@playwright/test';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8899';

/** API 登录并种入会话 token（与 pixel_uat 同法） */
async function login(page: Page, user: string, pass: string) {
  const res = await page.request.post(`${BASE}/api/auth/login`, { data: { username: user, password: pass } });
  const body = await res.json();
  expect(body.success, `登录失败:${JSON.stringify(body)}`).toBeTruthy();
  await page.addInitScript((tk) => sessionStorage.setItem('auth_token', tk), body.token);
}

/** 解析 rgb(a) 字符串为 [r,g,b]（非 rgb 返回 null） */
function rgb(s: string): [number, number, number] | null {
  const m = s.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
  return m ? [+m[1], +m[2], +m[3]] : null;
}

const isDark = (c: [number, number, number]) => c[0] < 60 && c[1] < 60 && c[2] < 70;
const isNearWhite = (c: [number, number, number]) => c[0] > 240 && c[1] > 240 && c[2] > 240;

// ★ 〇-N：本文件整体钉在**浅色** prefers-color-scheme 下跑。
// 全站取消 light/auto 档之后，「宿主偏好是浅色」必须不再影响任何渲染结果；
// 反过来（默认跟随宿主）会让这条锁在 CI 机上随机绿随机红，等于没有锁。
test.use({ colorScheme: 'light' });

test.describe('后台暗色适配 + 文件工单上传回归', () => {
  test('D1 后台各面板暗色骨架（无近白残留）', async ({ page }) => {
    await login(page, 'admin', 'Admin@1234');
    // ★ 〇-N：故意种入三态时代的**浅色调**偏好，并用 emulate-media 把系统偏好也拨到浅色，
    //   复现历史缺陷环境（那时 body 文字色只挂在 html[data-theme='dark'] 覆写层，
    //   落到 light 档就回退成浏览器默认黑字 → 纯黑 UI 上什么都看不见）。
    await page.addInitScript(() => localStorage.setItem('app_theme', 'light'));
    await page.goto('/admin');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    // 恒暗契约①：文字色与底色同层写死，浅色调环境下仍等于交付真值 #E7E9EA
    const bodyColor = await page.evaluate(() => getComputedStyle(document.body).color);
    expect(bodyColor, '浅色 prefers-color-scheme 下 body 文字色不得回退成黑字').toBe('rgb(231, 233, 234)');
    // 恒暗契约②：废弃偏好键只做清理，不再左右渲染
    await expect(page.evaluate(() => localStorage.getItem('app_theme'))).resolves.toBeNull();
    await expect(page.locator('body')).toBeVisible();
    await page.waitForTimeout(1200); // 等面板异步渲染
    // ★ 2026-09-18 UI 迁移：后台外壳换 LangCross 皮肤，锚点 .admin-shell→.lc-shell、
    //   .admin-side→.lc-sidebar（.panel-card 保留）；旧类名已删，取不到元素会漏检暗色骨架。
    for (const sel of ['.lc-shell', '.lc-sidebar', '.panel-card']) {
      const el = page.locator(sel).first();
      if (await el.count()) {
        const bg = rgb(await el.evaluate((n) => getComputedStyle(n).backgroundColor));
        expect(bg, `${sel} 取色失败`).toBeTruthy();
        expect(isDark(bg!), `${sel} 暗色模式下应为深色，实际 ${bg}`).toBeTruthy();
      }
    }
    // 遍历可见容器：统计近白底色块（img/canvas 白底属正常——如 QR 码，排除之）
    const res = await page.evaluate(() => {
      const nearWhite = (s: string) => {
        const m = s.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
        return !!m && +m[1] > 240 && +m[2] > 240 && +m[3] > 240;
      };
      const samples: string[] = [];
      let n = 0;
      document.querySelectorAll('div,section,p,span,code,pre,li').forEach((e) => {
        const r = e.getBoundingClientRect();
        if (r.width < 24 || r.height < 12) return; // 细碎元素不计
        const st = getComputedStyle(e);
        if (st.display === 'none' || st.visibility === 'hidden') return;
        if (nearWhite(st.backgroundColor) && st.position !== 'fixed') {
          n++;
          if (samples.length < 8) samples.push(`${e.tagName}.${String(e.className).slice(0, 40)} "${(e.textContent || '').trim().slice(0, 24)}" w=${Math.round(r.width)} h=${Math.round(r.height)}`);
        }
      });
      return { n, samples };
    });
    expect(res.n, `暗色后台不应存在大面积近白自绘块；样本: ${res.samples.join(' | ')}`).toBeLessThanOrEqual(2);
    // 抽查两个代表面板（工单管理/套餐计费）同样成立
    for (const tab of [/工单/, /套餐|计费/]) {
      const btn = page.locator('text=' + tab.source).first();
      if (await btn.count()) {
        await btn.click().catch(() => {});
        await page.waitForTimeout(900);
        const again = await page.evaluate(() => {
          let n = 0;
          document.querySelectorAll('div,section,code,pre').forEach((e) => {
            const r = e.getBoundingClientRect();
            if (r.width < 24 || r.height < 12) return;
            const m = getComputedStyle(e).backgroundColor.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
            if (m && +m[1] > 240 && +m[2] > 240 && +m[3] > 240) n++;
          });
          return n;
        });
        expect(again, `面板 ${tab} 近白块过多`).toBeLessThanOrEqual(3);
      }
    }
    await page.screenshot({ path: 'artifacts/d1_admin_dark.png' });
  });

  test('U1 文件工单上传建单成功（multipart boundary 回归）', async ({ page }) => {
    await login(page, process.env.UAT_USER || 'uatuser_a', process.env.UAT_PASS || 'uatpass123');
    await page.goto('/tickets');
    // ★ 2026-09-18 UI 迁移：模式切换钮由 emoji（📎/📝）改为 i18n 文案「文件」「文本」
    await page.getByRole('button', { name: /^文件$/ }).click(); // 切换「文件」模式
    const input = page.locator('#tk-file-input');
    await expect(input).toBeAttached({ timeout: 10000 });
    await input.setInputFiles([{ name: 'e2e_upload.txt', mimeType: 'text/plain', buffer: Buffer.from('今天天气怎么样，适合出门吗？\n第二行内容。\n') }]);
    await page.getByRole('button', { name: /^文本$/ }).isVisible(); // 模式按钮仍渲染（防止选择器漂移）
    const title = page.locator('input[placeholder*="工单标题"]');
    await expect(title).toBeVisible();
    await title.fill('E2E上传回归单');
    await page.getByRole('button', { name: /创建并入队/ }).click();
    // 成功口径：列表出现新工单；绝不允许出现 boundary 类 400 文案
    await expect(page.locator('text=E2E上传回归单').first()).toBeVisible({ timeout: 20000 });
    const bodyTxt = await page.locator('body').innerText();
    expect(bodyTxt, '上传不应报 multipart 解析错误').not.toMatch(/文件解析失败或超过大小上限|不支持的文件格式/);
    await page.screenshot({ path: 'artifacts/u1_upload_ticket.png' });
  });
});
