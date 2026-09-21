// ============================================================================
// e2e-manual/real_llm_smoke.spec.ts — 真实模型端到端冒烟（★ #58 缺口补齐，2026-09-22）
//
// 为何必须手工（AGENTS.md 一.6）：本用例打**真模型 + 真计费**，需要一台已配置模型路由的
// 在线后端与一个有效 API Key。发布闸门里既没有这些凭据、也不该消耗真实积分，
// 因此放 e2e-manual 并用 env 守卫；缺凭据时整文件 skip，永不拖红。
//
// 与其余测试的分工：
//   - 单测/UAT 矩阵用 mock LLM（scripts/uat/mock_llm.py）验证**链路与闸门**；
//   - 本用例验证**真实模型返回经过整条清洗链后仍然可交付**——这是 mock 永远测不出来的：
//     模型违约吐伪标签、把指令回显成 `<Only output ...>`、压掉 Markdown 结构、
//     同文回显骗过硬闸，只有真模型才会发生（本仓 RC-2/RC-5 全部源自真实返回）。
//
// 运行：
//   REAL_LLM_BASE=https://<host> REAL_LLM_KEY=<API_KEY> \
//     npx playwright test -c playwright.manual.config.ts e2e-manual/real_llm_smoke.spec.ts
//   （或直接 npx playwright test e2e-manual/real_llm_smoke.spec.ts，同一 env 前缀）
// 断言全部是「结构性」的（长度、书写系统、指纹守恒），不对措辞做判定——
// 措辞质量属翻译质量问题，由人工抽检，写进断言只会随模型版本随机翻红。
// ============================================================================
import { test, expect } from '@playwright/test'

const BASE = (process.env.REAL_LLM_BASE || '').replace(/\/+$/, '')
const KEY = process.env.REAL_LLM_KEY || ''

test.skip(!BASE || !KEY, '手工冒烟：需 REAL_LLM_BASE + REAL_LLM_KEY（真模型+真计费），不进发布闸门');

// 一次真实同步翻译：POST /openapi/v1/translate（Bearer Key，与划译插件/Office 加载件同链路）
async function translate(text: string, targetLangs: string[], mode: 'fast' | 'pro' = 'pro') {
  const res = await fetch(`${BASE}/openapi/v1/translate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}` },
    body: JSON.stringify({ text, target_langs: targetLangs, mode }),
  });
  expect(res.status, `HTTP 状态异常：${res.status}`).toBe(200);
  const j = await res.json();
  expect(j.success, `接口返回未成功：${JSON.stringify(j).slice(0, 300)}`).toBe(true);
  return j as { translations?: Record<string, string>; points_used?: number };
}

const hasCJK = (s: string) => /[一-鿿]/.test(s);

test('SMOKE-1 中→英：真模型译文非空、非同文回显、不含汉字、无伪标签/指令残留', async () => {
  const src = '本产品支持 40 余种语言互译，并提供术语库一致性与交付质量报告。';
  const j = await translate(src, ['en']);
  const out = (j.translations?.en ?? '').trim();
  expect(out.length, '英文译文为空 ⇒ 交付残缺').toBeGreaterThan(0);
  expect(out.replace(/\s+/g, ' ')).not.toBe(src.replace(/\s+/g, ' ')); // RC-2 同文回显
  expect(hasCJK(out), `译文残留汉字（未译或清洗链漏网）：${out}`).toBe(false);
  // RC-2/RC-5 的真实形态：输出契约标签与「只输出译文」的指令回显绝不能进交付物
  expect(out).not.toContain('<t>');
  expect(out).not.toContain('</t>');
  expect(out.toLowerCase()).not.toContain('only output');
  expect(j.points_used, '真实计费必须记账（积分口径，零 token 裸值）').toBeTruthy();
});

test('SMOKE-2 多语种一次调用：每个目标语言都要有独立可交付译文', async () => {
  const src = '请在本周五前提交翻译终稿，并同步术语表变更。';
  const langs = ['en', 'fr', 'ru', 'ja'];
  const j = await translate(src, langs);
  for (const lc of langs) {
    const out = (j.translations?.[lc] ?? '').trim();
    expect(out.length, `${lc} 缺译文（静默漏语种是最难发现的交付缺陷）`).toBeGreaterThan(0);
    expect(out.replace(/\s+/g, ' ')).not.toBe(src.replace(/\s+/g, ' '));
    expect(out).not.toContain('<t>');
  }
  // ja 用汉字书写系统，同文判定只能靠「与源文逐字相等」，不能靠 hasCJK
  expect(j.translations?.ja).not.toBe(src);
});

test('SMOKE-3 结构守恒：Markdown 行内标记与表格在真模型往返后不得被吃掉', async () => {
  const src = '**加粗重点**与 `等宽术语` 并存。\n\n| 字段 | 说明 |\n| --- | --- |\n| 模式 | 专业校对 |\n';
  const j = await translate(src, ['en']);
  const out = j.translations?.en ?? '';
  expect(out.length).toBeGreaterThan(0);
  expect((out.match(/\*\*/g) ?? []).length, `** 数量守恒被破坏：${out}`).toBe(2);
  expect((out.match(/`/g) ?? []).length, '行内反引号不得丢失').toBeGreaterThanOrEqual(2);
  // 表格分隔行是内部提示词的指纹源（RC 系列首个 P0），译文里出现即结构降级
  expect(out).not.toContain('只输出译文');
  expect(out.split('\n').filter((l) => l.trim().startsWith('|')).length, '表格行数不得塌陷')
    .toBeGreaterThanOrEqual(3);
});

test('SMOKE-4 超长保护：闸门先于模型生效（不白烧积分）', async () => {
  // 5000 字符上限（api_openapi_tasks.go syncTranslateMaxChars）：构造明确超限的输入
  const over = '这是一段用于触发长度闸门的中文文本。'.repeat(400); // 15 × 400 = 6000 字符
  const res = await fetch(`${BASE}/openapi/v1/translate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${KEY}` },
    body: JSON.stringify({ text: over, target_langs: ['en'] }),
  });
  expect(res.status, `未超长请求应先被闸门拒绝（当前长度 ${over.length} 字符）`).toBe(400);
  const j = await res.json();
  expect(j.error_code ?? j.message ?? '').toMatch(/text_too_long|上限/);
});
