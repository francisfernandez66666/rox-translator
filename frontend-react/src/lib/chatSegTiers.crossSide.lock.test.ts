// ============================================================================
// src/lib/chatSegTiers.crossSide.lock.test.ts — 即时翻译分段档位的**三方跨侧等值**静态锁
// （★ 2026-11-01 DEF-P1-05 B2 运营面配套；node 环境——它要 readFileSync 读 Go 源文件）
//
// 这条锁问的只有一件事，但问的是三方：
//   ① 前端 src/lib/chatSegTiers.ts 的三份表（区间 / 配置键 / 缺省）
//   ② 后端 internal/api/chat_seg_config.go 的 chatSegCfgFields（写入侧拒收区间）
//   ③ 后端 internal/engine/chat_paragraphs.go 的那组常量（键名与钳位真值——唯一事实源）
// 三者必须**逐字相等**。②③ 之间还有一层"表达式解析"：api 那一张表的 def/lo/hi 写的是
// engine.XXX 引用而不是数字，所以本锁先把 engine 的 const 块建成符号表，再回来求值——
// 只比 ①② 的字面会把"api 引了一个不存在的常量名"放过（那种是编译错误，编译器已经在管），
// 而"api 引了另一个真实存在但语义不同的常量"编译器管不了，只有这里能抓。
//
// 为什么值得单独一道机械锁（不是洁癖）：
//   区间在这条链上是**判据**而不是文案。前端拿它做保存前预拦，后端拿它做写入侧拒收，
//   读侧另按 engine 的钳位解释。三份迟早漂；漂了的表现两侧都"看起来对"——
//   前端 [0,2]、后端 [0,1] ⇒ 运营填 2 被后端 400，读到一句天书；
//   前端 [1,N]、后端 [0,N] ⇒ "关掉分段"这颗闸在界面上按不下去，而接口和链路都是收的
//   （**这一族与逐行翻译那族唯一的判据差异就是 0 合法**，所以 X5 专门把两族的方向各钉一次）。
//
// 三条判据取向（本仓对闸门的一贯要求，缺一条这道锁就是摆设）：
//   ① **读不到源文件 / 解析不到 2 行 ⇒ 判红**，不当成"没问题"。锚点漂了（后端把字段挪进
//      别的文件、或改了行内形状）与"两侧完全一致"在 grep 计数上长得一模一样，
//      前者更坏——后者是真安全，前者是闸门失明。
//   ② **反证走真变异**：把同一把判据函数喂一份**改了一格的前端表副本**，必须在**那一条**上
//      报出不等；再喂真实表必须报零差（AGENTS §三「判据级断言代替不了守卫级」）。
//   ③ 只比**一格**（刻意不多格一起改）：多格一起变异就退化成"两侧是否整体相等"，
//      看不出逐条比对着陆在哪一档。
//
// ⚠️ 为什么这个文件是 node 环境：jsdom 下 `import.meta.url` 不是 `file:`，
//   一律用 **cwd 相对路径**（vitest 的 cwd＝frontend-react，`../backend-go/...` 即仓库同级后端）。
//
// 运行：npx vitest run src/lib/chatSegTiers.crossSide.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import {
  CHAT_SEG_TIER_FIELDS,
  CHAT_SEG_TIER_RANGES,
  CHAT_SEG_TIER_CONFIG_KEYS,
  CHAT_SEG_TIER_DEFAULTS,
  type ChatSegTierField,
} from './chatSegTiers'

// 三份源文件都是 **cwd 相对**路径：写成绝对路径会把锁钉死在某个人的机器上。
const API_FILE = '../backend-go/internal/api/chat_seg_config.go'
const ENGINE_FILE = '../backend-go/internal/engine/chat_paragraphs.go'
const READPOINT_FILE = '../backend-go/internal/api/chat_seg_plan.go'
// 反向对照用的另一族（它的判据方向与本族**相反**，X5 要两族各钉一次）
const TL_FILE = '../backend-go/internal/api/text_line_config.go'

// SymValue/SymTable：engine 常量块里只会出现字符串、整数与布尔三种字面值，符号表按这三类存。
// 布尔是必须留的一档——api 那张表的最后一列 zeroIsReal 写的就是 true/false；
// 把它塞进 string|number 会让"解析出 true 再判 === true"这条腿在类型上先自相矛盾。
type SymValue = string | number | boolean
type SymTable = Record<string, SymValue>

// EngineRow 是 api 那张表白名单里一行档位声明**解析并求值后**的五件事。
interface EngineRow { field: string; key: string; def: number; lo: number; hi: number; zeroIsReal: boolean }

// readSymbols 把 engine 的 const 块建成"名字 → 字面值"符号表。
// 为什么必须建表而不是只比 api 那一侧：api 的 def/lo/hi 写的就是这些名字（唯一事实源在 engine），
// 不还原成数字就没有"前端区间 == 引擎真值"这一层证据。
function readSymbols(): SymTable {
  const src = readSource(ENGINE_FILE, 'engine 分段档位常量')
  const out: SymTable = {}
  for (const line of src.split('\n')) {
    // ⚠️ 结尾**不许**写 \b：`"..."` 的收尾引号与后面的空格都是非词字符，那里没有词边界，
    //    带上 \b 会把两条字符串常量静默漏掉（首跑就是这样只解析到四个数字常量）。
    const m = line.match(/^\s*([A-Z][A-Za-z0-9_]*)\s*=\s*(?:"([^"]*)"|(\d+))/)
    if (!m) continue
    out[m[1]] = m[2] !== undefined ? m[2] : Number(m[3])
  }
  // 射程自证：六个本族常量必须都解析到（少一个＝锚漂了，下面的求值会静默退成 undefined）
  const need = ['CfgKeyChatPerParagraph', 'CfgKeyChatSegmentMaxPts', 'ChatPerParagraphDefault',
    'ChatSegmentMaxPtsDefault', 'ChatSegmentMaxPtsLo', 'ChatSegmentMaxPtsHi']
  const miss = need.filter((k) => out[k] === undefined)
  if (miss.length) {
    throw new Error(`engine 常量符号表缺 ${miss.join(', ')} ⇒ 解析锚漂了、本条锁失明，判红`)
  }
  return out
}

// readSource 统一读源文件：读不到即抛（判红而不是跳过——"读不到"与"两侧一致"在计数上同形）。
function readSource(p: string, what: string): string {
  try {
    return readFileSync(p, 'utf8')
  } catch (e) {
    throw new Error(`读不到${what}源文件 ${p}：${(e as Error).message}（锚漂了 ⇒ 本条锁失明，判红）`)
  }
}

// resolve 把一个"表式里的值"还原成数字或字符串：engine.XXX 查符号表，字面量直接用。
function resolve(tok: string, sym: SymTable, where: string): SymValue {
  if (/^-?\d+$/.test(tok)) return Number(tok)
  if (/^true$|^false$/.test(tok)) return tok === 'true'
  const q = tok.match(/^"([^"]*)"$/)
  if (q) return q[1]
  const m = tok.match(/^engine\.([A-Za-z0-9_]+)$/)
  if (!m) throw new Error(`${where} 里出现无法解析的值写法 ${tok} ⇒ 表式形状变了，本条锁失明，判红`)
  const v = sym[m[1]]
  if (v === undefined) throw new Error(`${where} 引用了 engine 里不存在的常量 ${m[1]} ⇒ 判据断链，判红`)
  return v
}

// readBackend 解析 chatSegCfgFields 的逐条声明并求值。
// 形状（现役，改动它必须同步这里）：
//
//	{"per_paragraph", engine.CfgKeyChatPerParagraph, engine.ChatPerParagraphDefault, 0, 1, true},
//	列序＝ jsonField, 配置键, def, lo, hi, zeroIsReal —— 与后端 chatSegCfgField 字段序一致。
function readBackend(sym: SymTable): EngineRow[] {
  const src = readSource(API_FILE, '后端分段档位')
  const start = src.indexOf('var chatSegCfgFields = []chatSegCfgField{')
  if (start < 0) {
    throw new Error('后端 chatSegCfgFields 声明找不到 ⇒ 锚漂了、本条锁失明，判红（读不到不等于一致）')
  }
  const block = src.slice(start)
  const endOfBlock = block.indexOf('\n}')
  const body = endOfBlock < 0 ? block : block.slice(0, endOfBlock)
  const rows: EngineRow[] = []
  for (const line of body.split('\n')) {
    const m = line.match(/\{"([a-z_]+)",\s*([^,]+),\s*([^,]+),\s*([^,]+),\s*([^,]+),\s*([^,]+)\},/)
    if (!m) continue
    const trim = (s: string) => s.trim()
    rows.push({
      field: m[1],
      key: String(resolve(trim(m[2]), sym, '配置键')),
      def: Number(resolve(trim(m[3]), sym, '代码缺省')),
      lo: Number(resolve(trim(m[4]), sym, '下界')),
      hi: Number(resolve(trim(m[5]), sym, '上界')),
      zeroIsReal: resolve(trim(m[6]), sym, '0 档语义') === true,
    })
  }
  if (rows.length !== CHAT_SEG_TIER_FIELDS.length) {
    throw new Error(
      `后端 chatSegCfgFields 解析到 ${rows.length} 条档位（前端有 ${CHAT_SEG_TIER_FIELDS.length} 条）`
      + `⇒ 锚漂了或两侧数量已不等，本条锁失明，判红（读不到不等于一致）`,
    )
  }
  return rows
}

// diffTiers 是**唯一**的比对判据函数：反证与正证必须共用它（同一把尺子跑两次，
// 而不是"正证用 expect、反证另算一次"——那样反证证明的是算术，不是这条判据）。
// 返回不等读数清单（空数组＝逐条等值）。
function diffTiers(
  backend: EngineRow[],
  ranges: Record<ChatSegTierField, [number, number]>,
  configKeys: Record<ChatSegTierField, string>,
  defaults: Record<ChatSegTierField, number>,
): string[] {
  const out: string[] = []
  for (const b of backend) {
    const k = b.field as ChatSegTierField
    const front = ranges[k]
    if (!front) { out.push(`前端区间表里没有档位 ${b.field} ⇒ 两侧不同源`); continue }
    if (front[0] !== b.lo || front[1] !== b.hi) {
      out.push(`档位 ${b.field}：前端 ${front[0]}~${front[1]} ≠ 后端 ${b.lo}~${b.hi}`)
    }
    // 同一行还带着配置键与代码缺省：一并比，一次改动（后端把 def 挪成 61）不必等第二次事故
    if (configKeys[k] !== b.key) out.push(`档位 ${b.field}：前端配置键 ${configKeys[k]} ≠ 后端 ${b.key}`)
    if (defaults[k] !== b.def) out.push(`档位 ${b.field}：前端缺省 ${defaults[k]} ≠ 后端 ${b.def}`)
    if (b.zeroIsReal && front[0] !== 0) {
      out.push(`档位 ${b.field}：后端声明 0 是真实档位，前端下界却是 ${front[0]} ⇒ 那颗"0"在界面上按不下去`)
    }
  }
  return out
}

describe('即时翻译分段档位：前端 ⇔ api 白名单 ⇔ engine 常量 三方等值（DEF-P1-05 B2）', () => {
  const sym = readSymbols()
  const backend = readBackend(sym)

  it('X1 前端三份表逐字等于后端 chatSegCfgFields（区间/配置键/缺省，且已回溯到 engine 常量真值）', () => {
    const diffs = diffTiers(backend, CHAT_SEG_TIER_RANGES, CHAT_SEG_TIER_CONFIG_KEYS, CHAT_SEG_TIER_DEFAULTS)
    expect(diffs, '三方判据已漂开：\n' + diffs.join('\n')).toEqual([])
  })

  it('X2 绝对值也各钉一次（两侧一起改错时 X1 仍绿，这一条抓的是"改成另一对"）', () => {
    expect(CHAT_SEG_TIER_RANGES).toEqual({ per_paragraph: [0, 1], max_points: [0, 100000] })
    expect(CHAT_SEG_TIER_DEFAULTS).toEqual({ per_paragraph: 1, max_points: 60 })
    expect(CHAT_SEG_TIER_CONFIG_KEYS).toEqual({
      per_paragraph: 'text_chat_per_paragraph',
      max_points: 'chat_segment_max_points',
    })
    // engine 那一侧同样钉一次绝对值：X1 只问等不等，engine 与 api 一起改成 61 时它照绿
    expect(sym.ChatSegmentMaxPtsDefault).toBe(60)
    expect(sym.ChatPerParagraphDefault).toBe(1)
    expect(sym.ChatSegmentMaxPtsLo).toBe(0)
    expect(sym.ChatSegmentMaxPtsHi).toBe(100000)
  })

  it('X3 反证（射程自证）：只改前端**一格**区间，同一把判据必须在**那一条**上报不等', () => {
    const mutated: Record<ChatSegTierField, [number, number]> = {
      ...CHAT_SEG_TIER_RANGES,
      // 这一格正是本族最容易写错的方向：把 0 当脏值 ⇒ 下界抬到 1 ⇒ "关掉分段"按不下去
      per_paragraph: [1, 1],
    }
    const diffs = diffTiers(backend, mutated, CHAT_SEG_TIER_CONFIG_KEYS, CHAT_SEG_TIER_DEFAULTS)
    expect(diffs.length, '把 per_paragraph 改成 1~1 竟判等 ⇒ X1 的比对是摆设').toBeGreaterThan(0)
    expect(diffs.join('\n')).toContain('per_paragraph')
    // 逐条对照：另一档没动，不等读数里**只许**出现被改那一档（证明比对是逐条做的）
    expect(diffs.some((d) => d.includes('max_points')), '未改动的档位也被判不等 ⇒ 比对取错了行').toBe(false)
    // 反向对照：喂真实表必须零差（否则"不等"来自判据本身而不是变异）
    expect(diffTiers(backend, CHAT_SEG_TIER_RANGES, CHAT_SEG_TIER_CONFIG_KEYS, CHAT_SEG_TIER_DEFAULTS)).toEqual([])
  })

  it('X4 前端字段清单与后端声明顺序等值（顺序＝表单展示顺序，也是"两项一次交齐"的判据来源）', () => {
    expect(backend.map((b) => b.field)).toEqual([...CHAT_SEG_TIER_FIELDS])
  })

  it('X5 两族判据方向各自钉死：本族下界必须==0 且后端 zeroIsReal==true；逐行翻译那族下界必须≥1', () => {
    // 本族：0 是真实档位（0＝关分段／0＝不设上限）。谁把它抬到 1，就是把那颗闸从运营手里拿走。
    for (const b of backend) {
      expect(b.zeroIsReal, `后端档位 ${b.field} 的 0 档语义被改成"拒收" ⇒ 与施工方案 §B2-2 相反`).toBe(true)
      expect(b.lo, `后端档位 ${b.field} 下界成了 ${b.lo} ⇒ 0 这一档不再可配`).toBe(0)
    }
    for (const k of CHAT_SEG_TIER_FIELDS) {
      expect(CHAT_SEG_TIER_RANGES[k][0], `前端档位 ${k} 下界不再是 0`).toBe(0)
    }
    // 反向对照（同一条锁里判**另一族**，防"把两族合并成一条规则"）：
    // 逐行翻译那族刻意**没有** 0 档语义（0＝一枪都不打／一行失败就打死整语种），下界必须 ≥1。
    // 这一半不是顺手加的：两族共用一张表单，下一位最省事的动作就是照抄判据——方向抄反了
    // 要么把关分段的闸做成摆设，要么把"填 0"变成打死补译链的脏值。
    const tl = readSource(TL_FILE, '后端逐行翻译档位')
    const rows = Array.from(tl.matchAll(/\{"([a-z_]+)",\s*"[a-z_]+",\s*(\d+),\s*(\d+),\s*(\d+)\},/g))
    expect(rows.length, '逐行翻译那族的两行声明解析不到 ⇒ 本条反向对照失明（不是"那族已删除"）').toBe(2)
    for (const r of rows) {
      expect(Number(r[4]), `逐行翻译档位 ${r[1]} 的下界被写成 ${r[4]} ⇒ 那一族的"0 拒收"方向被改反`).toBeGreaterThanOrEqual(1)
    }
  })

  it('X6 读点必须引用 engine 常量而不是字面量（改一侧漏一侧＝界面显示开、链路按关跑）', () => {
    const src = readSource(READPOINT_FILE, '分段档位读点')
    // 剥注释后再判：说明注释里写着 ConfigIntAllowZero 与那几个常量名，裸词判会被注释喂饱
    const code = src.split('\n').filter((l) => !l.trim().startsWith('//')).join('\n')
    const calls = Array.from(code.matchAll(/ConfigIntAllowZero\(([^)]*)\)/g))
    expect(calls.length, '读点里 ConfigIntAllowZero 调用数不是 2 ⇒ 读点搬家或本条锁失明，判红').toBe(2)
    const bareDefaults = calls.filter((m) => /,\s*\d+\s*\)/.test(m[0]))
    expect(bareDefaults.map((m) => m[0].trim()),
      '两处 AllowZero 的缺省参数里有字面量数字 ⇒ 与配置口不是同一个数（改 engine 常量一侧漏一侧）').toEqual([])
    // 正向对照：两处都必须是 engine.常量（否则上面那条"零字面量"会因为整行消失而空转）
    const withEngine = calls.filter((m) => /engine\.[A-Z][A-Za-z0-9_]*\s*\)/.test(m[0]))
    expect(withEngine.length, '两处 AllowZero 的缺参都不是 engine 常量 ⇒ 比对的是空集').toBe(2)
  })
})
