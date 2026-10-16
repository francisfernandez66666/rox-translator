// ============================================================================
// src/translateFlowE2eLock.test.ts — e2e「气泡尾部那一排」判据的**形状锁**（★ 任务 #51 连带的 2026-10-15 改判据）
//
// 为什么要有这一份：translate_flow.spec.ts 的 TF4 原来写的是 `expect(tail.length).toBe(4)`，
// 本批 P3 译文朗读（SpeakButton）落进同一排后它当场恒红（实到 5 件）。这类红**不是**回归，
// 而是"钉死数字的锁过时了还在替旧口径说话"（同 AGENTS §一·14 那条清单长度锁的理由）。
// 改成派生判据（点名件都在 ＋ 谁都不许是第三类／被再包一层 ＋ 件数下界当射程自证）之后，
// 本文件负责钉住**改完的那一侧**：
//   P1 负向：剥掉注释后，spec 里不许再出现「尾部件数＝某个写死数字」那种等值判据；
//   P2 正向：三条派生腿必须逐条在位（缺任一条＝锁被摘掉，而不是"改了个写法"）；
//   P3 锚点等值：点名清单里的每一个 data-testid 必须真在 MessageBubble.tsx 里存在
//      ——否则"点名正锁"会在控件改名时空转（判据看不见＝假绿，同 §三「度量腿要先自证射程」）；
//   R1 反证：喂一份"把派生腿摘掉、换回写死件数"的变异文本，必须被 P1/P2 判红。
//
// ⚠️ 判据只问**剥注释之后**的正文：文件头与段内大段理由注释里**故意留着**「件数＝4」这段历史，
//    那是改判据方向的记录（AGENTS 要求写理由），不是违规写法（同 §一·5「注释不许冒充实现字面量」的反向处理）。
// 运行：npx vitest run src/translateFlowE2eLock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SPEC = path.join(HERE, '..', 'e2e', 'translate_flow.spec.ts')
const BUBBLE = path.join(HERE, 'components', 'MessageBubble.tsx')

// 剥注释：先块注释再行注释（行注释那条要小心字符串里的 //，本 spec 的正文里没有裸 URL，够用且保守——
// 宁可少剥也不许把正文剥掉，剥过头会让负向锁"看起来更干净"＝假绿方向）。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

// 尾部那一排的写死件数等值判据（改判据之前的旧形态：`expect(tail.length, …).toBe(4)`，
// 以及"把它改个数"的任何变体 `tail.items.length`／`tail.children.length` 那一族）。
// ⚠️ 链式取长必须整段吃完再判 `.length`／`.count`，只写 `tail\.(length|count)` 会漏掉
//    `tail.items.length` 这种同义写法（本文件反证首跑就是这么漏的：变异落地了、判据却抓不到＝假反证）。
function hardcodedTailCount(code: string): string[] {
  const hits: string[] = []
  code.split('\n').forEach((line, i) => {
    if (/tail[\w.]*\.(?:length|count)\b.*\.toBe\(\d+\)/.test(line)) hits.push(`${i + 1}: ${line.trim()}`)
  })
  return hits
}

// 三条派生腿：逐条点名，缺一条即判红（清单式判据在这里够用，因为钉的是"这三条必须存在"，
// 数量随修法自然增长，不是"件数=某数字"那种会被产品迭代顶红的写法）。
function derivedLegs(code: string): string[] {
  const need: Array<[string, RegExp]> = [
    ['点名件缺席判据（tail.missing ⇒ toEqual([])）', /tail\.missing[\s\S]{0,120}\.toEqual\(\[\]\)/],
    ['第三类控件判据（tail.foreign ⇒ toEqual([])）', /tail\.foreign[\s\S]{0,160}\.toEqual\(\[\]\)/],
    ['射程自证（件数下界＝点名件数，取空即判失明）', /tail\.count[\s\S]{0,140}\.toBeGreaterThanOrEqual\(NAMED\.length\)/],
    ['逐件字号等值 14px', /c\.fs[\s\S]{0,160}\.toBe\(14\)/],
    ['逐件垂直中心同带', /c\.off[\s\S]{0,160}\.toBeLessThanOrEqual\(6\)/],
  ]
  return need.filter(([, re]) => !re.test(code)).map(([name]) => name)
}

// TF4 点名清单（从 spec 正文里现读，不在本文件写死第二份 —— 写死就是"两把尺子"）
function namedTestids(code: string): string[] {
  const m = code.match(/const NAMED = \[([^\]]*)\]/)
  if (!m) return []
  return m[1].split(',').map((s) => s.trim().replace(/^['"]|['"]$/g, '')).filter(Boolean)
}

// ★ 2026-10-16 补的第二族判据：点名正锁必须**按服务端能力分派**。
// 由来：朗读钮自本批起由 `/api/health.tts_ready` 收门（false ⇒ 整颗不渲染，AGENTS §一·17），
// 而这条 e2e 跑的本机／CI／UAT 实例**都没有 TTS 权重** ⇒ 无条件点名会让它在无权重环境恒红
// （恒红即钝化）。三档与产品侧同口径：true ⇒ 必须在场；false ⇒ 必须不在场（反向腿）；
// null（探不到）⇒ 不判。三腿缺一，这条锁要么恒红、要么退化成"永远放行"的单向锁。
function capabilityLegs(code: string): string[] {
  const need: Array<[string, RegExp]> = [
    ['读实例能力（/api/health 的 tts_ready，按布尔取值）', /\/api\/health[\s\S]{0,240}tts_ready/],
    ['按能力分派点名清单（探不到＝不判，fail-open）', /NAMED\.filter\([\s\S]{0,140}!== false\)/],
    ['反向腿（能力明确为假时这颗钮必须在场即判红）', /ttsCap === false[\s\S]{0,240}speak-translation[\s\S]{0,160}\.toBe\(0\)/],
  ]
  return need.filter(([, re]) => !re.test(code)).map(([name]) => name)
}

describe('e2e TF4：气泡尾部那一排的判据形状（派生式，不写死件数）', () => {
  const specRaw = readFileSync(SPEC, 'utf8')
  const code = stripComments(specRaw)

  it('P1 负向：正文里不许再有「尾部件数 = 写死数字」', () => {
    const hits = hardcodedTailCount(code)
    expect(hits, `尾部件数又被写死成字面数字：\n${hits.join('\n')}（本批加一枚控件就得改一次锁 ⇒ 用派生判据）`).toEqual([])
  })

  it('P2 正向：五条派生腿逐条在位（缺任一条＝锁被摘掉）', () => {
    const missing = derivedLegs(code)
    expect(missing, `TF4 派生判据缺腿（闸门失明）：${missing.join(' / ')}`).toEqual([])
  })

  it('P3 锚点等值：点名清单里的 data-testid 必须真在 MessageBubble 里存在', () => {
    const named = namedTestids(code)
    expect(named.length, '锚点漂了：spec 正文里取不到 const NAMED = [...]，本条 P3 没有射程（不是"通过"）').toBeGreaterThan(0)
    const bubble = readFileSync(BUBBLE, 'utf8')
    for (const id of named) {
      expect(bubble, `点名件 data-testid="${id}" 不在 MessageBubble.tsx 里：TF4 那条正锁会恒判缺席（或早已空转）`)
        .toContain(`data-testid="${id}"`)
    }
  })

  it('P4 正向：点名正锁按服务端 TTS 能力分派（三腿：读能力／按能力分派／反向腿）', () => {
    const missing = capabilityLegs(code)
    expect(missing, `TF4 的「按能力分派」缺腿：${missing.join(' / ')}（缺了它就成单向锁：要么在无权重实例恒红，要么永远放行）`).toEqual([])
  })

  it('R1 反证：换回"写死件数"的旧形态必须被 P1/P2 判红', () => {
    // 变异＝精确复现改造前那三行：数组长句写死 4，且没有点名/第三类/下界三条腿。
    const mutated = code
      .replace(/const NAMED = \[[^\]]*\]/, 'const NAMED = []')
      .replace(/expect\(tail\.count[\s\S]*?\.toBeGreaterThanOrEqual\(NAMED\.length\);/, '')
      .replace(/expect\(tail\.missing[\s\S]*?\.toEqual\(\[\]\);/, 'expect(tail.items.length).toBe(4);')
      .replace(/expect\(tail\.foreign[\s\S]*?\.toEqual\(\[\]\);/, '')
    expect(mutated, '反证失效：变异没落地（替换没命中，等于没反证）').not.toBe(code)
    // 写死件数那条必须被抓到
    expect(hardcodedTailCount(mutated).length, '反证 R1 失效：写死件数没被判红').toBeGreaterThan(0)
    // 派生腿缺条必须被抓到
    expect(derivedLegs(mutated).length, '反证 R1 失效：摘掉派生腿没被判红').toBeGreaterThan(0)
    // 且变异体不能把 P3 的锚点取空当成"通过"
    expect(namedTestids(mutated).length, '反证 R1 的 NAMED 必须为空 ⇒ P3 必须走"锚点漂了"那条正锁').toBe(0)
  })

  it('R2 反证：把能力分派摘回"无条件点名"必须被 P4 判红', () => {
    // 变异＝① 过滤式改成原样点名（等价于"朗读钮永远必须在场"）；② 删掉能力为假那条反向腿。
    const mutated = code
      .replace(/const EXPECT = NAMED\.filter\([^\n]*/, 'const EXPECT = NAMED')
      .replace(/if \(ttsCap === false\) \{[\s\S]*?\n    \}/, '')
    expect(mutated, '反证失效：变异没落地（替换没命中，等于没反证）').not.toBe(code)
    expect(capabilityLegs(mutated).length, '反证 R2 失效：无条件点名＋无反向腿必须被判红（这是"无权重实例恒红"那条老病的形态）')
      .toBeGreaterThan(0)
    // 正向对照：原文件这条判据必须是绿的（否则 R2 红了也说明不了什么）
    expect(capabilityLegs(code)).toEqual([])
  })
})
