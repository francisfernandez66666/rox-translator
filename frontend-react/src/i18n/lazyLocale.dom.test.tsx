// ============================================================================
// i18n/lazyLocale.dom.test.tsx — ★ 2026-10-11 跨境链路瓶颈批「语种词典懒加载」闸门
//
// 背景（一句话）：十份 locales/*.ts 是 ALL_KEYS 全量词典（源 2.1 MB），过去静态 import 进
// i18n/index.ts ⇒ 入口 JS 2.63 MB／gzip 442 KB，在实测塌到 KB/s 的跨境链路上就是
// 「刷新页面动效加载十几秒」的分子。现在改成按当前语种 dynamic import，各取自己那一份 chunk。
//
// 本文件锁四件事（都是这次改造**唯一会炸**的四个面）：
//   A 源码层：index.ts 里不许再有 locales 的**静态** import；十个语种的 dynamic import 字面量一个不缺
//             （Vite 靠字面量切 chunk——写成 `import('./locales/' + lang)` 变量拼接，chunk 会整代丢失）；
//   B 取词层：三个取词点（t／textOr／translateIn）一律经 dictFor，旧 `dicts` 表已删——
//             懒加载最大的硬风险是「词典未到手时对 undefined 取下标」＝整站白屏；
//   C 行为层：词典未到手时必须**安静地**走 lang→en→zh 回退链（不抛、不露裸键）；
//             ensureLocale 到手后同一键必须换成母语值，且同一语种的并发请求只拨一次；
//   D 渲染层：语种到手要 emit() 触发重渲染（否则用户停在英文那一帧不再动＝懒加载变成"永远英文"）。
//             ★ 2026-10-13 加 D2：emit() 在位**不等于**组件会重渲染——React 只在快照值变了才重渲染，
//             而快照旧形态只有语种一个量，"chunk 到手而语种没变"那一帧被整个跳过（D 段拆成两条的正当理由）。
//
// ★ 反证口径（收口时实跑，见批次文档）：
//   R1 把某语种的 dynamic 字面量删掉 ⇒ A 的等值计数红；
//   R2 把 dynamic 改成变量拼接 `import(`./locales/${lang}`)` ⇒ A 的字面量锁红（chunk 不会再被切出）；
//   R3 把 dictFor 的 `|| {}` 摘掉（返回 undefined）⇒ B/C 的"未到手不许抛"当场红（TypeError 击穿全站）；
//   R4 把 ensureLocale 里那句 emit() 删掉 ⇒ D 的渲染锁红（界面停在英文那一帧）；
//   R5 把 inflight 复用摘掉（每次新建 promise）⇒ C 的"同语种只拨一次"红；
//   ★ R4b（2026-10-13 实跑）只把 `dictRev++／bumpSnapKey()` 摘掉、**emit() 留着** ⇒ **D1 全绿、只有 D2 红**。
//        这正是 D1 的盲区：它把"切语种"和"chunk 到手"挤在同一个 act 里，React 至少拿到一次语种变化，
//        于是到手那一帧被顺带重渲染掩盖——现网 flaky（葡语/泰语/法语首屏停在英文）就是这么藏下来的。
//
// 运行：npx vitest run src/i18n/lazyLocale.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { act, render, cleanup } from '@testing-library/react'
import { createElement } from 'react'
import { LANG_OPTIONS, type Lang } from './index'
import { dict as ruDict } from './locales/ru'

// IDX 被测源文件：按 **cwd 相对路径**读（vitest 的 root 就是 frontend-react）。
// ⚠ 别改成 `fileURLToPath(new URL('./index.ts', import.meta.url))`：本文件走 jsdom 环境，
//   那里 import.meta.url 是 http 形态，fileURLToPath 直接抛「The URL must be of scheme file」，
//   整套用例在解析期就全灭（2026-10-11 实跑撞到，故写成注释留给下一位）。
const IDX = readFileSync('src/i18n/index.ts', 'utf8')

// codeOnly 把源码剥成"只剩代码"：本文件的判据问的是**代码里有没有**静态 import／`dicts` 标识符，
// 而这两串字样在改造说明里必须以自然语言存在（同 §一·5「负向锁误伤说明注释」那族自伤）。
// ⚠ 第一版用 `/\/\*[\s\S]*?\*\//g` 剥块注释，结果被**自己的说明注释**里那句「只剥 星斜杠…斜杠星」
//   点着，把整份文件从那一行起吞成空串（A 段四条同时 0 命中＝典型假绿）。
//   现改为逐行判定：整行注释（// 开头、块注释的 * 与 */ 续行）整行丢弃，代码行再截掉行尾 // 之后的部分。
//   截尾时要求 // 前是行首或空白，避免吃掉串内的斜杠（本文件与 index.ts 都没有 URL 串，属预防）。
function codeOnly(src: string): string {
  return src
    .split('\n')
    .filter((l) => {
      const s = l.trim()
      return !(s.startsWith('//') || s.startsWith('/*') || s.startsWith('*/') || s.startsWith('*'))
    })
    .map((l) => l.replace(/(^|\s)\/\/.*$/, ''))
    .join('\n')
}
/** 剥注释后的 index.ts 源码：静态锁只判真代码，注释里出现 import 语句不该算退化。 */
const CODE = codeOnly(IDX)

// 懒加载射程内的十个语种（zh／en 在入口包里，没有 loader）
const LAZY_LANGS: Lang[] = LANG_OPTIONS.map((o) => o.code).filter((c) => c !== 'zh' && c !== 'en')
// 文件名口径：内部码 zh_hant → 文件 zh-hant（历史命名，loader 与磁盘必须一致，否则 chunk 404）
const fileOf = (c: Lang) => (c === 'zh_hant' ? 'zh-hant' : c)

// freshEnModule 冷启动一个"当前语种＝en 且没有任何在途加载"的模块副本（C／D 两段共用，
// 故必须放模块作用域——放 describe 回调里会让 D 段够不着，实跑撞过一次 ReferenceError）。
// 判据要在"词典未到手"那一刻读值，就不能让模块初始化去 ensureLocale 那个语种，否则
// 断言与后台加载赛跑，赢输看调度。做法：app_lang 写语种表外的野值 ⇒ 走浏览器检测，
// jsdom 报 en-US ⇒ currentLang='en'，而 en 在入口包里、loader 表根本没有它（立即完成）。
async function freshEnModule() {
  localStorage.setItem('app_lang', 'klingon')
  vi.resetModules()
  const m = await import('./index')
  expect(m.getLang()).toBe('en')
  return m
}

// 渲染锁用的探针组件由 D 段**就地造**：它必须订阅 useLang，否则 emit() 不会让它重渲染，
// 那条"到手自动换母语"的判据就变成"测了个不订阅的壳"（同 §三 那条「判据级断言代替不了守卫级」）。

beforeEach(() => {
  cleanup()
  vi.resetModules()
})
afterEach(() => {
  localStorage.setItem('app_lang', 'zh') // 复位，别把非中文态漏给后续用例（同 LangSelect 测试口径）
})

describe('A · 源码层：入口包不许再静态背十份词典', () => {
  it('A1 index.ts 里零条 locales 静态 import（正向对照：dynamic import 必须成串在位）', () => {
    const staticHits = CODE.match(/from\s+['"]\.\/locales\/[^'"]+['"]/g) || []
    expect(staticHits, `残留静态 import：${staticHits.join(' / ')}`).toEqual([])
    // 正对照：负向恒真最常见的原因是判据根本打不到目标——这里数"应有的 dynamic 字面量"
    const dyn = CODE.match(/\(\s*['"]\.\/locales\/[A-Za-z-]+['"]\s*\)/g) || []
    expect(dyn.length, `dynamic import 字面量 ${dyn.length} 条，期望 ${LAZY_LANGS.length} 条`).toBe(LAZY_LANGS.length)
  })

  it('A2 十个语种各自的 dynamic import 字面量逐一点名（缺一个＝那个语种永远拿不到词典）', () => {
    for (const c of LAZY_LANGS) {
      expect(CODE, `${c} 的 chunk 字面量不在位`).toContain(`import('./locales/${fileOf(c)}')`)
    }
  })

  it('A3 loader 一律取模块的 dict 导出（漏 .then(m=>m.dict) 会把整个模块对象当词典，取词恒空）', () => {
    const thenDict = CODE.match(/then\(\s*\(?\s*m\s*\)?\s*=>\s*m\.dict\s*\)/g) || []
    expect(thenDict.length, `then(m=>m.dict) 只有 ${thenDict.length} 处，期望 ${LAZY_LANGS.length} 处`).toBe(LAZY_LANGS.length)
  })

  it('A4 档位常量不许写死第二份：loader 表条目数 = LANG_OPTIONS 去掉 zh／en', () => {
    // 派生式判据：以 LANG_OPTIONS 现读为基准，加语种却忘了配 loader ⇒ 这一条红（而不是静默走英文）
    expect(LAZY_LANGS.length).toBe(LANG_OPTIONS.length - 2)
    const entries = CODE.match(/^\s*[a-z_]+:\s*\(\)\s*=>\s*import\(/gm) || []
    expect(entries.length, `loader 表 ${entries.length} 条，语种表懒加载档 ${LAZY_LANGS.length} 条`).toBe(LAZY_LANGS.length)
  })
})

describe('B · 取词层：所有取词点收口在 dictFor', () => {
  it('B1 旧 `dicts` 合并表已删（它会让未加载语种以 undefined 出现在取下标的位置）', () => {
    // 判据问的是"那张按语种索引的合并表还在不在"＝**取下标形态**与**表本体声明**，
    // 不是标识符 `dicts` 字样——基础词典的文件名就叫 dicts.zh.ts／dicts.en.ts（第一版拿 \bdicts\b
    // 去判，被那两条 import 语句误命中，实跑红了一次；同 §一·5「负向锁误伤」那族，只是这次伤在文件名上）。
    expect(CODE, '旧合并表还在被索引 ⇒ 未加载语种会 TypeError').not.toMatch(/\bdicts\s*\[/)
    expect(CODE, '旧合并表本体还在 ⇒ 懒加载只是加了一层没拆掉它').not.toMatch(/\bconst\s+dicts\s[=|]/)
    expect(CODE).toMatch(/function dictFor\(/) // 正对照：判据的目标形态确实在
    expect(CODE).toMatch(/from\s+'\.\/dicts\.zh'/) // 正对照：基础字典文件仍在（改名式破坏要当场红）
  })

  it('B2 三个取词点都走 dictFor（少一个就是漏一条腿，且只有非中英界面才现形）', () => {
    const current = CODE.match(/dictFor\(currentLang\)\[key\]/g) || []
    const explicit = CODE.match(/dictFor\(lang\)\[key\]/g) || []
    expect(current.length, `t／textOr 两处按当前语种取词的腿现在 ${current.length} 处`).toBe(2)
    expect(explicit.length, `translateIn 那一条腿现在 ${explicit.length} 处`).toBe(1)
  })
})

describe('C · 行为层：未到手走回退链，到手换母语，且只拨一次', () => {

  it('C1 词典未到手：取词安静回落英文，绝不抛、绝不露裸键', async () => {
    const m = await freshEnModule()
    const before = m.t('chat.send')
    // 回落英文＝en 词典里那个值；抛异常的话根本走不到这一行（这一句同时是"不抛"的判据）
    expect(before).toBeTruthy()
    expect(before).not.toBe('chat.send')
    expect(before).toBe(m.translateIn('en', 'chat.send'))
    expect(m.textOr('chat.send', '兜底')).toBe(before)
  })

  it('C2 ensureLocale 到手后同一键换成母语；zh／en 恒立即完成', async () => {
    const m = await freshEnModule()
    expect(m.translateIn('ru', 'chat.send'), '未加载时 ru 档应回落英文').toBe(m.translateIn('en', 'chat.send'))
    await m.ensureLocale('ru')
    expect(m.translateIn('ru', 'chat.send'), '加载后 ru 档必须换成俄语词典值').toBe(ruDict['chat.send'])
    // 入口包自带的两档：loader 表里没有它们 ⇒ 立即 resolve，不许出现"中文界面在等一个不存在的请求"
    await expect(m.ensureLocale('zh')).resolves.toBeUndefined()
    await expect(m.ensureLocale('en')).resolves.toBeUndefined()
  })

  it('C3 同一语种并发只拨一次（两次 ensure 拿到同一个 promise；到手后走同步分支）', async () => {
    const m = await freshEnModule()
    const p1 = m.ensureLocale('de')
    const p2 = m.ensureLocale('de')
    expect(p2, '并发第二次另起了一个请求 ⇒ 同一 chunk 被拨两遍、还各自触发一次重渲染').toBe(p1)
    await p1
    const p3 = m.ensureLocale('de') // 已到手：走 loadedLocales 那一支
    await p3
    expect(m.translateIn('de', 'chat.send')).toBeTruthy()
  })

  it('C4 十二语种逐一点名：未加载／已加载两种形态下取词都必须是非空字符串（dictFor 不许回 undefined）', async () => {
    const m = await freshEnModule()
    for (const c of LANG_OPTIONS.map((o) => o.code) as Lang[]) {
      const raw = m.translateIn(c, 'chat.send')
      expect(typeof raw, `${c} 档取词类型不是 string`).toBe('string')
      expect(raw.length, `${c} 档取词回空串`).toBeGreaterThan(0)
      await m.ensureLocale(c)
      const after = m.translateIn(c, 'chat.send')
      expect(after.length, `${c} 加载后取词回空串`).toBeGreaterThan(0)
      if (c !== 'zh' && c !== 'en') expect(after, `${c} 加载后仍等于英文回退 ⇒ loader 取到的不是词典`).not.toBe(m.translateIn('en', 'chat.send'))
    }
  })
})

describe('D · 渲染层：词典到手必须 emit，界面停在母语那一帧', () => {
  it('D1 setLang 切语种后：先英文那一帧，chunk 到手自动重渲染成母语（不需要用户再操作一次）', async () => {
    const m = await freshEnModule()
    // 探针必须**订阅** m.useLang（不是把 t 当参数传进去）：emit() 只会通知订阅者，
    // 不订阅的壳会让这一条变成"渲染了一次就不再动"的假绿。
    function Probe() {
      m.useLang()
      return createElement('span', { 'data-testid': 'probe' }, m.t('chat.send'))
    }
    const { container } = render(createElement(Probe))
    const shown = () => (container.querySelector('[data-testid="probe"]') as HTMLElement).textContent
    expect(shown(), '首帧应取到词（哪怕是英文回退）').toBeTruthy()
    const enFrame = shown()

    // 切到 ru：setLang 内部起 ensureLocale('ru')；到手后必须自己 emit——测试只等这一件事发生
    await act(async () => {
      m.setLang('ru')
      await m.ensureLocale('ru')
    })
    expect(shown(), 'chunk 到手后界面没换 ⇒ ensureLocale 里那句 emit() 被摘了（D 段反证 R4）').not.toBe(enFrame)
    expect(shown()).toBe(ruDict['chat.send'])
    cleanup()
  })

  // D2 ★ 2026-10-13 现网 flaky 本体（Playwright all_langs_full pt／th、landing_i18n fr 三条同因）：
  // D1 测的是「setLang 切语种 + chunk 到手」，那两步之间 React 至少拿到一次**语种变了**的快照，
  // 于是即使 ensureLocale 的 emit() 唤不醒任何组件，act 收尾时那次顺带重渲染也会把界面翻成母语——
  // **假绿**。真实形态是 D1 没覆盖的：访客首屏语种由浏览器检测／存值决定（pt），从此再没人切语言，
  // chunk 到手那一刻 currentLang 一个字没变，useSyncExternalStore 按"快照没变"直接跳过重渲染，
  // 于是「英文一闪」变成「英文常驻」，只有等某个不相干的状态变更顺带重渲染才会换回来
  // （现网截图正是那副样子：导航还是英文，晚挂载的葡语快算卡已经是葡语）。
  // ⇒ 修法＝把「词典到手」本身做进快照（index.ts 的 dictRev／snapKey），本条锁的就是这一层。
  it('D2 首屏语种固定＋chunk 稍后才到：没有任何"语种变化"可通知 React，也必须自动换成母语', async () => {
    // 把 pt 词典换成"测试手里可控的那一份"：先让它一直在路上（界面必然停在英文那一帧，
    // 不与调度赛跑），再由测试放行 ⇒ "到手那一刻"完全由本用例掌控。
    let release!: (d: Record<string, string>) => void
    const gated = new Promise<Record<string, string>>((r) => { release = r })
    const FAB = 'Enviar (chunk 到手)'
    localStorage.setItem('app_lang', 'pt')
    vi.resetModules()
    vi.doMock('./locales/pt', () => ({ dict: gated }))
    const m = await import('./index')
    expect(m.getLang(), '本用例前提是首屏语种＝pt（不是切过去，切语种那条腿 D1 已测）').toBe('pt')

    function Probe() {
      m.useLang()
      return createElement('span', { 'data-testid': 'd2' }, m.t('chat.send'))
    }
    const { container } = render(createElement(Probe))
    const shown = () => (container.querySelector('[data-testid="d2"]') as HTMLElement).textContent
    // 首帧：pt 词典还没到手 ⇒ 安静回退英文（这是懒加载的设计形态，见 index.ts dictFor 上方）
    expect(shown(), '首帧本该是英文回退').toBe(m.translateIn('en', 'chat.send'))

    // 只放行 chunk，**不做任何切语种、不 rerender、不碰 props**——这就是"没人再来推第二下"的真实形态
    await act(async () => {
      release({ 'chat.send': FAB })
      await gated
    })
    expect(shown(), 'chunk 到手后界面还停在英文 ⇒ 到手事件没进 useSyncExternalStore 的快照'
      + '（反证 R4b：把 index.ts 里的 dictRev++／bumpSnapKey() 摘掉即红在这里，而 D1 依然全绿）').toBe(FAB)
    cleanup()
    vi.doUnmock('./locales/pt')
  })
})
