// ============================================================================
// src/lib/textLineTiers.crossSide.lock.test.ts — 逐行翻译档位区间的前后端**跨侧等值**静态锁
// （★ 2026-10-26 用户追加裁决九-4 配套；node 环境，见下方「为什么不在 jsdom 里」）
//
// 这条锁问的只有一件事：`src/lib/textLineTiers.ts` 里那份区间（1~5 / 2~10）与
// `backend-go/internal/api/text_line_config.go` 的 `textLineCfgFields` 的 lo/hi
// 是否**逐字相等**。
//
// 为什么值得单独一道机械锁（不是洁癖）：
//   区间在这条链上是**判据**而不是文案。前端拿它做保存前的预拦，后端拿它做写入侧拒收，
//   读侧另有 engine 的钳位。三份迟早漂；漂了的表现两侧都"看起来对"——
//   前端 1~9、后端 1~5 ⇒ 运营填 7 被后端 400，读到一句天书；
//   前端 1~3、后端 1~5 ⇒ 一个本来合法的档被前端拦下，运营以为产品不支持。
//   两边都以为是对方错了，而没有第三条腿会红。
//
// 三条判据取向（本仓对闸门的一贯要求，缺一条这道锁就是摆设）：
//   ① **读不到源文件 / 解析不到 2 行 ⇒ 判红**，不当成"没问题"。锚点漂了（后端把字段挪进
//      别的文件、或改了行内形状）与"两侧完全一致"在 grep 计数上长得一模一样，
//      前者更坏——后者是真安全，前者是闸门失明。
//   ② **反证走真变异**：把同一把判据函数喂一份**改成 1~9 的前端区间副本**，必须在**那一条**上
//      报出不等；再喂真实区间必须报零差。只写 `expect(1!==9)` 那种"算一次的对照"证明不了
//      比较是逐条做的（AGENTS §三 那条「判据级断言代替不了守卫级」）。
//   ③ 只比**一条**（刻意不两条一起改）：两条一起变异就退化成"两侧是否整体相等"，
//      看不出逐条比对着陆在哪一档。
//
// ⚠️ 为什么这个文件是 node 环境：它要用 `readFileSync` 读**源文件**。jsdom 下
//   `import.meta.url` 不是 `file:`（vitest 给的是 http 形态），拿它推仓库根会抛；
//   所以一律用 **cwd 相对路径**（vitest 的 cwd 就是 frontend-react，`../backend-go/...`
//   即仓库同级后端）。与 §一·5 那条「读源文件的静态锁必须放 node 环境文件」同口径。
//
// 运行：npx vitest run src/lib/textLineTiers.crossSide.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import {
  TEXT_LINE_TIER_FIELDS,
  TEXT_LINE_TIER_RANGES,
  TEXT_LINE_TIER_CONFIG_KEYS,
  TEXT_LINE_TIER_DEFAULTS,
  type TextLineTierField,
} from './textLineTiers'

// BACKEND_FILE 是**cwd 相对**路径：vitest 根目录＝frontend-react，故 ../backend-go 就是仓库同级后端。
// 写成绝对路径会把锁钉死在某个人的机器上（同仓其余静态锁一律用这个口径）。
const BACKEND_FILE = '../backend-go/internal/api/text_line_config.go'

// BackendRow 是后端白名单里一条档位声明解析出来的四件事：
// json 字段名 / system_config 键名 / 代码缺省 def / 写入侧区间 lo~hi。
interface BackendRow { field: string; key: string; def: number; lo: number; hi: number }

// readBackend 解析 textLineCfgFields 的逐条声明。
// 形状（现役，改动它必须同步这里）：{"retry_max", "text_line_retry_max", 3, 1, 5},
// 列序＝ jsonField, 配置键, def, lo, hi —— 与后端 textLineCfgField 结构体字段序一致。
function readBackend(): BackendRow[] {
  let src: string
  try {
    src = readFileSync(BACKEND_FILE, 'utf8')
  } catch (e) {
    // 读不到文件＝闸门失明。判红而不是跳过：跳过会让"后端把这个函数搬走了"读成"区间仍然一致"。
    throw new Error(`读不到后端档位源文件 ${BACKEND_FILE}：${(e as Error).message}（锚漂了 ⇒ 本条锁失明，判红）`)
  }
  const rows: BackendRow[] = []
  for (const line of src.split('\n')) {
    const m = line.match(/\{"([a-z_]+)",\s*"([a-z_]+)",\s*(\d+),\s*(\d+),\s*(\d+)\},/)
    if (m && m[2].startsWith('text_line_')) {
      rows.push({ field: m[1], key: m[2], def: Number(m[3]), lo: Number(m[4]), hi: Number(m[5]) })
    }
  }
  if (rows.length !== TEXT_LINE_TIER_FIELDS.length) {
    throw new Error(
      `后端 textLineCfgFields 解析到 ${rows.length} 条档位（前端有 ${TEXT_LINE_TIER_FIELDS.length} 条）`
      + `⇒ 锚漂了或两侧数量已不等，本条锁失明，判红（读不到不等于一致）`,
    )
  }
  return rows
}

// diffRanges 是**唯一**的比对判据函数：反证与正证必须共用它（同一把尺子跑两次，
// 而不是"正证用 expect、反证另算一次"——那样反证证明的是算术，不是这条判据）。
// 返回不等读数清单（空数组＝逐条等值）。
function diffRanges(
  backend: BackendRow[],
  ranges: Record<TextLineTierField, [number, number]>,
  configKeys: Record<TextLineTierField, string>,
  defaults: Record<TextLineTierField, number>,
): string[] {
  const out: string[] = []
  for (const b of backend) {
    const k = b.field as TextLineTierField
    const front = ranges[k]
    if (!front) { out.push(`前端区间表里没有档位 ${b.field} ⇒ 两侧不同源`); continue }
    if (front[0] !== b.lo || front[1] !== b.hi) {
      out.push(`档位 ${b.field}：前端 ${front[0]}~${front[1]} ≠ 后端 ${b.lo}~${b.hi}`)
    }
    // 同一条后端行还带着配置键与代码缺省：一并比，一次改动（后端把 def 挪成 4）不必等第二次事故
    if (configKeys[k] !== b.key) out.push(`档位 ${b.field}：前端配置键 ${configKeys[k]} ≠ 后端 ${b.key}`)
    if (defaults[k] !== b.def) out.push(`档位 ${b.field}：前端缺省 ${defaults[k]} ≠ 后端 ${b.def}`)
  }
  return out
}

describe('逐行翻译档位区间：前后端跨侧等值（裁决九-4）', () => {
  const backend = readBackend()

  it('X1 前端 TEXT_LINE_TIER_RANGES 逐字等于后端 textLineCfgFields 的 lo/hi（含配置键与缺省）', () => {
    const diffs = diffRanges(backend, TEXT_LINE_TIER_RANGES, TEXT_LINE_TIER_CONFIG_KEYS, TEXT_LINE_TIER_DEFAULTS)
    expect(diffs, '前后端档位判据已漂开：\n' + diffs.join('\n')).toEqual([])
  })

  it('X2 区间数值本身钉死为 1~5 与 2~10（两侧同时改错时 X1 仍绿，这一条抓的是"改成另一对"）', () => {
    // 正向对照之外再钉一次绝对值：X1 只问"相不相等"，若有人把**两侧**一起改成 1~9，
    // X1 照绿而运营填 5 会被读侧钳位——绝对值那一刀只有这里能补上。
    expect(TEXT_LINE_TIER_RANGES).toEqual({ retry_max: [1, 5], upstream_dead_streak: [2, 10] })
  })

  it('X3 反证（射程自证）：把**其中一条**前端区间改成 1~9，同一把判据必须在**那一条**上报不等', () => {
    const mutated: Record<TextLineTierField, [number, number]> = {
      ...TEXT_LINE_TIER_RANGES,
      retry_max: [1, 9],
    }
    // 后端那一份**不动**（它就是现网事实），只把前端区间换成 1~9 —— 变异必须落在被测的那一侧，
    // 两侧一起变就退化成"整体相不相等"，抓不到单条漂移。
    const diffs = diffRanges(backend, mutated, TEXT_LINE_TIER_CONFIG_KEYS, TEXT_LINE_TIER_DEFAULTS)
    expect(diffs.length, '把 retry_max 改成 1~9 竟判等 ⇒ X1 的比对是摆设').toBeGreaterThan(0)
    expect(diffs.join('\n')).toContain('retry_max')
    // 逐条对照：另一档没动，所以不等读数里**只许**出现 retry_max 那一条（证明比较是逐条做的，
    // 不是"任一条不等就整体判不等"那种粗判）
    expect(diffs.some((d) => d.includes('upstream_dead_streak')), '未改动的档位也被判不等 ⇒ 比对取错了行').toBe(false)

    // 反向对照：喂真实区间必须零差（否则"不等"来自判据本身而不是变异）
    expect(diffRanges(backend, TEXT_LINE_TIER_RANGES, TEXT_LINE_TIER_CONFIG_KEYS, TEXT_LINE_TIER_DEFAULTS)).toEqual([])
  })

  it('X4 前端字段清单与后端声明顺序等值（顺序＝表单展示顺序，也是"两项一次交齐"的判据来源）', () => {
    expect(backend.map((b) => b.field)).toEqual([...TEXT_LINE_TIER_FIELDS])
  })

  it('X5 这两档**没有 0 档语义**：两侧下界都必须 ≥1（0＝一枪都不打／一行失败就打死整语种）', () => {
    for (const b of backend) {
      expect(b.lo, `后端档位 ${b.field} 下界成了 ${b.lo} ⇒ 0 档被放开，这条链的语义就反了`).toBeGreaterThanOrEqual(1)
    }
    for (const k of TEXT_LINE_TIER_FIELDS) {
      expect(TEXT_LINE_TIER_RANGES[k][0]).toBeGreaterThanOrEqual(1)
    }
  })
})
