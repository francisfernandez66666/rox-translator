// ============================================================================
// src/vitestScanLayout.lock.test.ts — 扫描型静态锁归位闸门（★ 2026-11-01 DEF-P2-08 的机械背兜）
//
// 一句话：凡"要遍历目录才能判"的测试文件，都必须写在 vitest.config.ts 的 SCAN_TESTS 名单里。
//
// 为什么要机械判而不是靠人记：unit project 的预算是 30s，scan project 是 180s。
// 有人新加一条"扫全仓找字面量"的静态锁却忘了归位 ⇒ 它在 30s 预算下**慢机器上随机假红**；
// 更坏的是它的红看起来像产品回归（断言本身没错），于是大家习惯性忽略红灯——
// 这正是 DEF-P2-08 记的那条"钝化闸门敏感度"的复发路径。
//
// 三条判据一起判，缺一条就是空锁（AGENTS §一·5「负向锁必须配正向对照」）：
//   ① 派生集合 ⊆ 名单      —— 新加的遍历型文件没进名单 ⇒ 红（本闸的存在理由）
//   ② 名单 ⊆ 派生集合      —— 名单里留着"已经不再遍历"的退化条目 ⇒ 红（白占 180s，且让独立 project 变成迷信）
//   ③ 名单条目必须真实存在  —— 文件改名/删掉后名单没跟着改 ⇒ 红（这条最容易漏：名单还在、判据早已没射程）
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

import rawConfig, { SCAN_TESTS } from '../vitest.config'

const HERE = path.dirname(fileURLToPath(import.meta.url)) // .../frontend-react/src
const FE_ROOT = path.resolve(HERE, '..') // .../frontend-react

// 遍历特征：目录读取 / glob / 跨仓现读。**必须先剥注释再判**——
// 说明注释里写一句"本文件会 readdirSync 全仓"就会把判据喂饱（本仓这类反证自伤踩过多次）。
const SCAN_MARKERS = /readdirSync\s*\(|readdir\s*\(|import\.meta\.glob|globSync|fast-glob|walk\w*\s*\(/

function stripComments(src: string): string {
  // ★ 按**行**剥，不做跨行 `/* … *​/` 匹配：正则字面量里出现的 `/*`（如注释里写 "*.ts 都必须在表里"）
  //   会让跨行匹配从那儿一路吞到下一个 `*/`，把**真代码整段吃掉**——首跑真踩（两个在册文件被误判成
  //   "不再遍历"）。按行剥足够挡住"说明注释里提一句 readdirSync"这一族误判，且不会误伤代码。
  return src
    .split('\n')
    .filter((l) => !/^\s*(\/\/|\/\*|\*)/.test(l)) // 整行注释、块注释起始行、jsdoc 续行
    .map((l) => l.replace(/(^|[^:'"`])\/\/[^\n]*$/g, '$1')) // 行尾注释（字符串里的 // 如 https:// 不算）
    .join('\n')
}

function listTestFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name)
    const st = statSync(p)
    if (st.isDirectory()) {
      out.push(...listTestFiles(p))
      continue
    }
    if (!/\.test\.(ts|tsx)$/.test(name)) continue
    out.push(path.relative(FE_ROOT, p).split(path.sep).join('/'))
  }
  return out
}

// 纯判据函数：反证与真实接线**共用这一个函数**（判据级断言代替不了守卫级，见 AGENTS §三）。
function layoutOffenders(derived: Set<string>, listed: string[]): string[] {
  const bad: string[] = []
  for (const f of derived) if (!listed.includes(f)) bad.push(`遍历型测试文件未进 SCAN_TESTS（会在 30s 预算下假红）: ${f}`)
  for (const f of listed) {
    if (!derived.has(f)) bad.push(`SCAN_TESTS 里留着不再遍历的退化条目（白占 180s 预算）: ${f}`)
    if (!existsSync(path.join(FE_ROOT, f))) bad.push(`SCAN_TESTS 里的文件不存在（改名或删了没同步名单）: ${f}`)
  }
  return bad
}

// ★ F-2.2 判据的输入形状（vitest 的 project 项：`{ test: {...} }`；配置里也允许平铺，两种都收）。
type ProjectCfg = {
  name?: string
  include?: string[]
  exclude?: string[]
  testTimeout?: number
  hookTimeout?: number
  test?: { name?: string; include?: string[]; exclude?: string[]; testTimeout?: number; hookTimeout?: number }
}

// ★ project 上的 test 段可能是"没写"（形状变了）；一律回 undefined 让判据点名"读不到"。
function projectTest(p: ProjectCfg) {
  return p.test ?? p
}

// 纯判据函数（与 layoutOffenders 同一形态：真配置与变异体喂**同一个函数**，
// 否则反证测的是"字段被删了"而不是"闸门会红"——AGENTS §三 那条「判据级代替不了守卫级」）。
function budgetOffenders(projects: ProjectCfg[], rootTimeout: number | undefined, listed: string[]): string[] {
  const bad: string[] = []
  const unit = projects.find((p) => projectTest(p).name === 'unit')
  const scan = projects.find((p) => projectTest(p).name === 'scan')
  if (!unit) bad.push('找不到 name=unit 的 project（project 形状变了，本闸已在空转）')
  if (!scan) bad.push('找不到 name=scan 的 project（独立预算整条没了）')
  for (const [label, t] of [['unit', unit && projectTest(unit)], ['scan', scan && projectTest(scan)]] as const) {
    if (!t) continue
    if (typeof t.testTimeout !== 'number') bad.push(`${label} project 的 testTimeout 丢了 ⇒ 退回 vitest 默认 5s，慢机器随机假红`)
    if (typeof t.hookTimeout !== 'number') bad.push(`${label} project 的 hookTimeout 丢了（vitest.setup 的 await 腿是超时暴露面）`)
  }
  if (unit) {
    const t = projectTest(unit)
    if (typeof t.testTimeout === 'number' && t.testTimeout > 60000) {
      bad.push('unit 预算被抬到 scan 那一档 ⇒ 真挂死的普通用例要占满 3 分钟才现形')
    }
    // 接线：unit 必须把名单**整份**排除，否则同一批文件被两个 project 各跑一次（时间翻倍且互踩）
    if (!(t.exclude ?? []).length || (t.exclude ?? []).length !== listed.length) {
      bad.push('unit project 不再排除 SCAN_TESTS（清单长度不等或为空）⇒ 名单没人读，扫描型锁仍留在 30s 预算里')
    }
  }
  if (scan) {
    const t = projectTest(scan)
    if (typeof t.testTimeout === 'number' && t.testTimeout < 180000) {
      bad.push('scan 预算低于 180s ⇒ 全仓扫描型锁又在撞默认档（DEF-P2-08 的病根）')
    }
    // 接线：scan 收的必须**恰是**名单（写成另一份字面量清单＝第二把尺子，迟早和名单打架）
    const inc = [...(t.include ?? [])].sort()
    const want = [...listed].sort()
    if (inc.length !== want.length || inc.some((f, i) => f !== want[i])) {
      bad.push('scan project 的 include 与 SCAN_TESTS 不等值 ⇒ 名单不再是唯一事实源')
    }
  }
  if (typeof rootTimeout !== 'number') bad.push('根 test 段的显式超时丢了（project 之外还有直接跑的入口）')
  return bad
}

describe('扫描型静态锁必须归入 scan project（DEF-P2-08 背兜）', () => {
  const derived = new Set(
    listTestFiles(path.join(FE_ROOT, 'src')).filter((rel) =>
      SCAN_MARKERS.test(stripComments(readFileSync(path.join(FE_ROOT, rel), 'utf-8'))),
    ),
  )

  it('射程自证：派生集合与名单都非空，且名单有下界', () => {
    expect(derived.size, '派生出 0 个遍历型文件 ⇒ 特征正则或遍历本身失效，本闸已失明').toBeGreaterThan(5)
    expect(SCAN_TESTS.length, '名单为空 ⇒ 三条判据全部空转').toBeGreaterThanOrEqual(15)
  })

  it('三条判据：派生⊆名单、名单⊆派生、名单条目真实存在', () => {
    const bad = layoutOffenders(derived, SCAN_TESTS)
    expect(bad, bad.join('\n')).toEqual([])
  })

  // ---- 反证：每例只改一处，且断"判红的正是那一条"（空替换的 no-op 会伪装成抓住了破坏）----
  it('反证 A：新加一个遍历型文件却不进名单 ⇒ 必须判红', () => {
    const withNew = new Set(derived)
    withNew.add('src/i18n/newScanGate.test.ts')
    const bad = layoutOffenders(withNew, SCAN_TESTS)
    expect(bad.length, '新遍历文件没进名单却全绿 ⇒ 本闸没有射程').toBeGreaterThan(0)
    expect(bad.some((x) => x.includes('newScanGate.test.ts') && x.includes('未进 SCAN_TESTS')),
      '判红的不是那一条（可能只是名单里别的原因）').toBe(true)
    expect(layoutOffenders(derived, SCAN_TESTS), '对照组：真实输入必须干净').toEqual([])
  })

  it('反证 B：名单里留一条早已不遍历的文件 ⇒ 必须判红', () => {
    const stale = [...SCAN_TESTS, 'src/somewhereNotTraversal.test.ts']
    const bad = layoutOffenders(derived, stale)
    expect(bad.some((x) => x.includes('退化条目')), '没抓住退化条目').toBe(true)
    expect(bad.some((x) => x.includes('文件不存在')), '没抓住名单里不存在的文件').toBe(true)
  })

  it('反证 C：把名单里的某条真实文件删掉（改名没同步）⇒ 必须判红', () => {
    const missing = SCAN_TESTS.filter((f) => f !== 'src/bootStorageGate.test.ts')
    expect(missing.length, '变异没落地').toBeLessThan(SCAN_TESTS.length)
    const bad = layoutOffenders(derived, missing)
    expect(bad.some((x) => x.includes('bootStorageGate.test.ts')), '删掉一条真实文件竟没被抓').toBe(true)
  })

  it('反证 D：特征判据不许被注释喂饱（剥注释这条腿在位）', () => {
    const onlyMentionedInComment = '// 本文件不再 readdirSync 全仓，所以不必进名单\n'
    expect(SCAN_MARKERS.test(stripComments(onlyMentionedInComment)),
      '注释里出现 readdirSync 就被判成遍历型 ⇒ 剥注释这条腿断了').toBe(false)
    // 正向对照：真用到时必须认得出（否则上面那条"注释不算"的宽松是白判的）
    expect(SCAN_MARKERS.test('const xs = readdirSync(dir)'), '真调用都认不出 ⇒ 特征正则失效').toBe(true)
  })

  // ============================================================================
  // ★ F-2.2（2026-11-02 补齐）：名单归位之外，**预算与接线本身**也要有人认领。
  //   上面四条判据全绿也回答不了这两件事：
  //     ① 有人把 `testTimeout: 180000` 删掉 ⇒ 名单还在、scan project 仍收到那些文件、
  //        但预算悄悄退回 vitest 默认的 5s ⇒ 慢机器上随机假红，而这正是 DEF-P2-08 的病文本体；
  //     ② 有人改了 project 的 include/exclude（不再消费 SCAN_TESTS）⇒ 名单变成一份没人读的登记表。
  //   ⇒ 判据吃的是**配置对象**而不是源码字面量（配置是导出的真值，正则只能抓"写过"不能抓"生效"）。
  //   判据收在纯函数 budgetOffenders 里，真配置与变异体喂**同一个函数**
  //   （AGENTS §三「判据级断言代替不了守卫级」——只判变异后的字段值等于什么都没判）。
  // ============================================================================
  const cfg = rawConfig as unknown as { test?: { testTimeout?: number; hookTimeout?: number; projects?: ProjectCfg[] } }
  const projects = cfg.test?.projects ?? []

  it('两个 project 的超时与接线都在位（预算不是无人认领的默认值，名单真的被消费）', () => {
    const bad = budgetOffenders(projects, cfg.test?.testTimeout, SCAN_TESTS)
    expect(bad, bad.join('\n')).toEqual([])
  })

  // 反证 E：三枚各拆一条腿（删预算／清排除／名单不再被 include 消费），每枚都必须让**对应那一条**判红
  it('反证 E：删掉 scan 预算、拆掉 unit 排除、include 不再读名单 ⇒ 三种形态都被抓住', () => {
    const clones = (): ProjectCfg[] => JSON.parse(JSON.stringify(projects)) as ProjectCfg[]
    const cases: { name: string; hits: string; mutate: (ps: ProjectCfg[]) => boolean }[] = [
      {
        name: 'E1 删掉 scan 的 testTimeout', hits: 'testTimeout',
        mutate: (ps) => {
          const t = ps.find((p) => p.test?.name === 'scan')?.test
          if (!t) return false
          const before = t.testTimeout
          delete t.testTimeout
          return before !== undefined && t.testTimeout === undefined
        },
      },
      {
        name: 'E2 清空 unit 的 exclude（名单不再被排除）', hits: '不再排除',
        mutate: (ps) => {
          const t = ps.find((p) => p.test?.name === 'unit')?.test
          if (!t) return false
          const landed = (t.exclude ?? []).length !== 0
          t.exclude = []
          return landed
        },
      },
      {
        name: 'E3 scan 的 include 换成一份写死名单', hits: 'include',
        mutate: (ps) => {
          const t = ps.find((p) => p.test?.name === 'scan')?.test
          if (!t) return false
          const landed = (t.include ?? []).length === SCAN_TESTS.length
          t.include = ['src/whatever.test.ts']
          return landed
        },
      },
    ]
    for (const c of cases) {
      const ps = clones()
      expect(c.mutate(ps), `${c.name}：变异没落地（no-op 会伪装成"锁抓住了破坏"）`).toBe(true)
      const bad = budgetOffenders(ps, cfg.test?.testTimeout, SCAN_TESTS)
      expect(bad.length, `${c.name} 没被抓（本闸对预算/接线没有射程）`).toBeGreaterThan(0)
      expect(bad.some((x) => x.includes(c.hits)), `${c.name} 红在了别的判据上：${bad.join(' / ')}`).toBe(true)
    }
    // 对照组：真实配置必须干净（否则上面那些"红"没有意义）
    expect(budgetOffenders(projects, cfg.test?.testTimeout, SCAN_TESTS),
      '真实配置本身是红的').toEqual([])
  })
})
