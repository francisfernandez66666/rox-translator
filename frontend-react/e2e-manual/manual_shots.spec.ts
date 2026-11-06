// ============================================================================
// e2e-manual/manual_shots.spec.ts — 手册截图采集执行体（E 组定稿-5/6，2026-11-06）
//
// 为什么放 e2e-manual 而不是 e2e：这条链需要**一个活的后端＋一份夹具数据**才有画面可截，
// 它不是回归判据，而是**交付物生产工具**（AGENTS §一·6：需要人工环境的用例一律放这里并带 env 守卫，
// 否则会把发布闸门永久拖红、钝化对真回归的敏感度）。
//
//   跑法（三条都要给，缺一条就整条 skip，不静默半跑）：
//     BASE_URL=http://127.0.0.1:8899  后端地址（与 run_uat 起的实例同形）
//     SHOT_TOKEN=<>                    夹具账号的 Bearer token（由登录脚本/手工取，禁止写进文件）
//     SHOT_OUT_DIR=<仓库内路径>         输出目录；本仓约定 `产品手册/img/<章节>/<lang>.png`
//     MANUAL_SHOTS=1                   显式开闸（缺省不跑）
//     SHOT_MASK=<逗号分隔的真短码>       截图前脱敏清单（夹具的 tenant_code／page_code）；缺省＝不脱敏，
//                                      此时任何带真码的画面都会被隐私闸判红——**红是对的，别放宽闸**
//   例：`MANUAL_SHOTS=1 SHOT_OUT_DIR=../../产品手册/img npx playwright test e2e-manual/manual_shots.spec.ts`
//
// ★ 四条硬口径（这是"隐私"这件事的实现，不是排版问题）：
//  ① **先过闸再落图**：每张图保存前必须跑 `judgeShotPrivacy(page.innerText)`，
//     命中 ⇒ 该图**不保存**、用例直接失败并逐条点名（规则＋原值＋上下文）。
//     没有"先截下来回头再看"这种顺序——回看的人永远比现场少。
//  ② **只准截夹具**：本文件不允许接现网地址；夹具账号/公司/产品名一律是 FIXTURE 里那几个演示值。
//     一旦 BASE_URL 指向真实部署，token 与画面里的租户数据就一起进图了。
//  ③ 截不到的章节**不许硬凑**：`shot` 为 null 的章按定稿-5 走"以前端实际实现为准"的文字路径，
//     本文件把它如实列进 SHOT_PLAN 里（一张表同时是"哪些章有图、哪些章只能靠文字"的账）。
//  ④ 允许集只放行**命中值本身**相等的界面固定文案（见 manualShotPrivacy 那条语义钉），
//     不许把整页文字抄进 allowTexts 换常绿——那是把闸关成摆设。
// ============================================================================
import { expect, test } from '@playwright/test'
import * as fs from 'node:fs'
import * as path from 'node:path'
// 相对路径引源码那一份实现：★ 不许在本文件里再抄一份正则（第二把尺子迟早和白名单分叉）
import { FIXTURE_ALLOW, formatVerdict, judgeShotPrivacy } from '../src/lib/manualShotPrivacy'
// 语种真值也只此一处：截图"到底是不是那一种语言的界面"必须问界面自己写出去的 BCP47，
// 不许在本文件里再列一份 lang→标签的对照（那会变成第二把尺子，而且首跑就是它没在，12 套全英文照样全绿）
import { BCP47_BY_LANG } from '../src/i18n/script'

const GATE = process.env.MANUAL_SHOTS === '1'
const BASE = process.env.BASE_URL || ''
const TOKEN = process.env.SHOT_TOKEN || ''
const OUT = process.env.SHOT_OUT_DIR || ''
// 脱敏串清单（逗号分隔）：夹具的 tenant_code／page_code 等真短码由采集者现读现传。
// ★ 禁止把它们写进本文件——写进来就等于把真码永久留在仓库里，与「文档不外推」同一条纪律的方向相反。
const MASKS = (process.env.SHOT_MASK || '').split(',').map((x) => x.trim()).filter(Boolean)

test.skip(!GATE || !BASE || !TOKEN || !OUT, '手册截图需要 MANUAL_SHOTS/BASE_URL/SHOT_TOKEN/SHOT_OUT_DIR 四者齐备')
test.describe.configure({ mode: 'serial', retries: 0 })

/** 12 个界面语种码（与 route_meta.json 的 languages 同源；顺序即产出顺序） */
const LANGS = ['zh', 'zh_hant', 'en', 'ru', 'fr', 'ar', 'es', 'pt', 'de', 'ja', 'ko', 'th'] as const

/**
 * 章节 → 采集计划。
 * `path` 为 null ＝ 这一章**不配图**（没有可独立截取的页面，或页面上的数据都是真实业务数据），
 * 按定稿-5 由手册正文用"前端实际实现的文字路径"描述；这张表就是那份账。
 */
const SHOT_PLAN: Array<{ chapter: string; route: string | null; note: string }> = [
  { chapter: '01-产品概览', route: null, note: '概览是叙事，无功能画面可截（硬配只会截到工作台，与本章内容不符）' },
  { chapter: '02-快速上手', route: '/onboarding', note: '引导第一步：建第一份产品' },
  { chapter: '03-角色与权限', route: null, note: '后台成员表必然带真实邮箱 ⇒ 不配图，正文写"组织 → 成员"的文字路径' },
  { chapter: '04-即时翻译', route: '/', note: '工作台输入区与语种多选' },
  { chapter: '05-翻译工单', route: '/tickets', note: '工单列表（夹具单）' },
  { chapter: '06-对照编辑', route: '/editor', note: '需要一条夹具工单；无夹具时该行会空，改走文字路径' },
  { chapter: '07-知识库与翻译记忆', route: null, note: '库里内容是真实语料 ⇒ 整章不配图' },
  { chapter: '08-账户与个人设置', route: '/my', note: '设置页（夹具账号）' },
  { chapter: '09-组织与成员管理', route: null, note: '同 03：成员名单是隐私，不截' },
  { chapter: '10-外部调用与集成', route: null, note: '页面会显示 API Key 形态 ⇒ 明确不截（隐私闸的 credential 规则同源）' },
  { chapter: '11-通知与AI助手', route: null, note: '通知内容带真实业务数据 ⇒ 不截' },
  { chapter: '12-常见问题', route: null, note: '纯文字章' },
  { chapter: '13-产品档案与对外页', route: '/products/detail', note: 'X1 写侧单页四段（本轮新增能力，手册必须有脸）' },
]

/** 登录态＋语种注入：token 与 app_lang **不在同一个存储里**，各写各的才都生效 */
async function seedLogin(page: import('@playwright/test').Page, lang: string) {
  await page.addInitScript(([tok, l]) => {
    // 入口分诊在 main.tsx 模块初始化时读一次 getAuthToken()（内存态，由 sessionStorage 水合），
    // 晚一帧就到不了 ⇒ token 必须留在 sessionStorage
    sessionStorage.setItem('auth_token', tok)
    // ★ 但界面语种在 **localStorage**（src/i18n/index.ts:197 现读 localStorage.getItem('app_lang')）。
    //   首跑这里写成 sessionStorage ⇒ 12 语种"各一套"截图实际全是同一套英文界面，
    //   而截图能打开、隐私闸也过，屏幕上零线索——正是「能打开不算验收」那一族。
    try { localStorage.setItem('app_lang', l) } catch { /* 站点数据被拒时回落检测档，不拦采集 */ }
  }, [TOKEN, lang])
}

/**
 * 截图前脱敏：把夹具的**真短码**换成演示串。
 * ★ 为什么必须排在隐私闸之前：`tenant_code`／`page_code` 是 32 位十六进制，闸按
 *   short-code-token／page-code／long-number 判它们命中是**对的**（对外页链接＝可访问链接，
 *   写进手册等于把租户段＋页面段一起公开）。出路不是放宽闸，而是让画面里根本没有那串——
 *   闸随后读的就是这张图真正会呈现的文字。
 * ⚠️ 不许在这里加"整页文本替换"之外的任何放宽；也不许把真短码塞进 FIXTURE_ALLOW
 *   （那是把闸开成一个洞，同 AGENTS §一·5「空态不许伪装成检查过」那一族）。
 */
async function maskSecrets(page: import('@playwright/test').Page, masks: string[]): Promise<number> {
  if (masks.length === 0) return 0
  return page.evaluate((list: string[]) => {
    let n = 0
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    const nodes: Text[] = []
    for (let cur = walker.nextNode(); cur; cur = walker.nextNode()) nodes.push(cur as Text)
    for (const node of nodes) {
      let s = node.nodeValue || ''
      for (const one of list) {
        if (!one || !s.includes(one)) continue
        // 替身串刻意取 `demo`（4 个字母）：page-code 那条规则要的是"两段 ≥8 位不可枚举码"，
        // 用 ≥8 位的替身（如 DEMO-CODE）会**让闸继续判红**——那不是闸坏了，是替身自己长得像真码。
        s = s.split(one).join('demo')
        n++
      }
      if (s !== (node.nodeValue || '')) node.nodeValue = s
    }
    return n
  }, masks)
}

test('逐语种逐章采集（先脱敏、再过隐私闸、最后落图）', async ({ page, browser }) => {
  fs.mkdirSync(OUT, { recursive: true })
  const shots: string[] = []
  const skipped: string[] = []
  /** 每张图被脱敏替换的处数（0＝这一屏本来没有可暴露的串）——这份账用来自证「闸前真的动过手」 */
  const maskedBy: Record<string, number> = {}

  for (const lang of LANGS) {
    for (const plan of SHOT_PLAN) {
      if (!plan.route) { skipped.push(`${plan.chapter}/${lang}：${plan.note}`); continue }
      const ctx = await browser.newContext({ baseURL: BASE, viewport: { width: 1440, height: 900 } })
      const p = await ctx.newPage()
      await seedLogin(p, lang)
      await p.goto(plan.route, { waitUntil: 'networkidle' }).catch(() => undefined)
      // ★ 截图必须等到"界面真起来了"：懒加载路由分片到手前那一屏是 BootLoading（`.ws` 那组 span），
      //   它有一行可读文本，所以隐私闸拦不住它——不等到终态就会把 72 张加载动画当成 72 张功能截图。
      //   判据问的是**加载态自己那个节点在不在**，不问文案（12 语种文案会变，同 AGENTS §一·5 那条纪律）。
      await p.waitForFunction(() => !document.querySelector('[data-ws="wt"]'), null, { timeout: 30000 }).catch(() => undefined)
      await p.waitForTimeout(1500)
      const loading = await p.locator('[data-ws="wt"]').count()
      expect(loading, `${plan.chapter}/${lang}：仍停在加载态 ⇒ 这一屏不是功能画面`).toBe(0)
      // ★ 语种射程腿：界面必须真是**这一种语言**（documentElement.lang 由 i18n 的 BCP47 映射写出）。
      //   没有这一条，app_lang 注错存储位就会得到 12 套一模一样的英文截图而全绿——
      //   截图能打开、隐私闸也过，屏幕上零线索（本文件首跑真踩过）。
      const htmlLang = await p.evaluate(() => document.documentElement.lang)
      expect(htmlLang, `${plan.chapter}/${lang}：界面语种不对（注入口没生效），这一张不能算该语种的图`).toBe(BCP47_BY_LANG[lang as keyof typeof BCP47_BY_LANG])
      const maskedCount = await maskSecrets(p, MASKS)
      // 页面真渲染出来才算一张图：读文本既给隐私闸当输入，也给"骨架屏冒充截图"当探测器
      const text = await p.locator('body').innerText()
      expect(text.length, `${plan.chapter}/${lang}：正文过短 ⇒ 页面没起来，不许出一张黑图`).toBeGreaterThan(120)
      const verdict = judgeShotPrivacy(text, [])
      if (!verdict.pass) {
        await ctx.close()
        // ★ 本仓 Playwright 版本的 expect 没有 .fail()（写了会在**判据之前**先抛 TypeError，
        //   于是"隐私闸命中"被读成"脚本坏了"）——命中必须用 throw 出声，逐条点名规则与原值。
        throw new Error(formatVerdict(verdict, plan.chapter, lang))
      }
      const dir = path.join(OUT, plan.chapter)
      fs.mkdirSync(dir, { recursive: true })
      // ★ 落盘名用**手册语种码**（zh-hant），不是界面语种码（zh_hant）：
      //   生成器按 manual.<lang>.md 的那个 lang 去取 `img/<章节>/<lang>.png`，
      //   两种拼法混用 ⇒ 繁体那一套图**静默一张不进**（构建只出一条 WARN，PDF 看着照样正常）。
      const file = path.join(dir, `${lang.replace('_', '-')}.png`)
      await p.screenshot({ path: file })
      shots.push(file)
      maskedBy[file] = maskedCount
      await ctx.close()
    }
  }

  // 落账：截了多少、哪些章按"前端实现文字路径"处理——这份清单要贴进手册制作记录
  fs.writeFileSync(path.join(OUT, 'manifest.json'), JSON.stringify({ shots, skipped, maskedBy, mask_terms: MASKS.length }, null, 2))
  expect(shots.length, '一张都没截到 ⇒ 采集链没接通（BASE_URL/token/路由任一失效），不许读成"通过"').toBeGreaterThan(0)
})

test('夹具白名单本身不含可疑真实值（这张表会被贴进验收文档）', () => {
  for (const one of FIXTURE_ALLOW) {
    expect(/example\.test|演示数据|demo data|Sample/i.test(one), `夹具白名单里出现可疑真实值：${one}`).toBe(true)
  }
})
