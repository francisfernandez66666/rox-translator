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

import { SCAN_TESTS } from '../vitest.config'

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
})
