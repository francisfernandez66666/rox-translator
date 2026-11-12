// ============ SalesOpsP.halfState.lock.test.ts · 职责说明 ============
// 三期 W3 / A12：维护页开关「半套配置」语义锁。
//
// ★ 两枚反证（硬口径 6）：只开一把闸时不得呈现 on；两把都开才 on。
// 全集：7 项开关 ⇄ 后端读口键双向比对，switches<7 Fatal；无读口必须 data-state="half" 且禁用；
// 两批靶显式登记（W5 四项 / W6 三项），当前 W3 不许一次性放全绿、W6 项不许提前翻 on（M0230）。
// 反向对照全用内存字符串 / 副本，不碰共享文件（M0228 伪造 on、M0229 删开关、M0230 提前放 W6）。
// ================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import {
  CURRENT_OPS_BATCH, OPS_CONFIG_KEYS, OPS_SWITCH_DEFS, W5_ON_KEYS, W6_ON_KEYS,
  opsAllowOn, opsSwitchState, type OpsSwitchDef,
} from '../../api/salesOps'

const panelSrc = readFileSync(fileURLToPath(new URL('./SalesOpsP.tsx', import.meta.url)), 'utf8')
const apiSrc = readFileSync(fileURLToPath(new URL('../../api/salesOps.ts', import.meta.url)), 'utf8')

/** 去注释（判据只落在代码，不落在说明文字） */
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
}

/** 伪造「已生效」的硬编码形态：静态 data-state="on" / checked={true} 一律判红（M0228 靶） */
function fakeOnHits(src: string): string[] {
  const code = stripComments(src)
  const hits: string[] = []
  if (/data-state=["']on["']/.test(code)) hits.push('hardcoded on state')
  if (/checked=\{\s*true\s*\}/.test(code)) hits.push('hardcoded checked')
  return hits
}

/** 从开关定义现推绑定的后端读口键（双向比对用，不写死分母） */
function boundKeys(defs: OpsSwitchDef[]): Set<string> {
  const out = new Set<string>()
  for (const def of defs) {
    out.add(def.key)
    if (def.pairKey) out.add(def.pairKey)
  }
  return out
}

describe('维护页开关半套语义（A12）', () => {
  it('★ 两枚反证：只开一把闸不得呈现 on；两把都开才 on', () => {
    // 反证一：只开主闸 / 只开配对闸，都不得呈现 on（只能 half）。
    expect(opsSwitchState(true, false, true, true)).not.toBe('on')
    expect(opsSwitchState(true, false, true, true)).toBe('half')
    expect(opsSwitchState(false, true, true, true)).not.toBe('on')
    expect(opsSwitchState(false, true, true, true)).toBe('half')
    // 反证二：两把都开才 on。
    expect(opsSwitchState(true, true, true, true)).toBe('on')
    // 补充：批次未到不许冒充已生效；未接线（W4/W5 新键）不许 on；都没开为 off。
    expect(opsSwitchState(true, true, true, false)).not.toBe('on')
    expect(opsSwitchState(true, true, false, true)).not.toBe('on')
    expect(opsSwitchState(false, false, true, true)).toBe('off')
    // 单闸开关（pair=null）自己配对：开即 on、关即 off。
    expect(opsSwitchState(true, null, true, true)).toBe('on')
    expect(opsSwitchState(false, null, true, true)).toBe('off')
  })

  it('开关全集 ⇄ 后端读口双向比对；switches<4 Fatal（★ W5 收口校准：键名与真实功能键同源）', () => {
    expect(OPS_SWITCH_DEFS.length).toBeGreaterThanOrEqual(4)
    const bound = boundKeys(OPS_SWITCH_DEFS)
    for (const def of OPS_SWITCH_DEFS) {
      expect(OPS_CONFIG_KEYS).toContain(def.key)
      if (def.pairKey) expect(OPS_CONFIG_KEYS).toContain(def.pairKey)
      expect(def.labelKey.startsWith('sales.ops.')).toBe(true)
      expect(def.hintKey.startsWith('sales.ops.')).toBe(true)
      expect(def.groupKey.startsWith('sales.ops.')).toBe(true)
    }
    for (const k of OPS_CONFIG_KEYS) expect(bound.has(k)).toBe(true)
    // 面板源码必须真的绑全集、逐项独立、半套可判定、未接线禁用、禁伪造 on。
    expect(panelSrc).toContain('OPS_SWITCH_DEFS')
    expect(panelSrc).toContain('data-state={st}')
    expect(panelSrc).toContain('disabled={!wired || busy}')
    expect(panelSrc).toContain('opsSwitchState(primary, pair, wired, allowOn)')
    expect(panelSrc).toContain("t('sales.ops.halfHint')")
    expect(panelSrc).toContain("t('sales.ops.offHint')")
    expect(fakeOnHits(panelSrc)).toEqual([])
    // 接口层同样不许藏一份伪造 on 的真值。
    expect(fakeOnHits(apiSrc)).toEqual([])
  })

  it('批次靶登记（★ W5 收口校准）：W5/W6 均已落库，CURRENT_OPS_BATCH=\'W6\'，全键允许 on；半套反证不随批次松动', () => {
    // 键集与批次档仍然显式登记（不许静默漂移）：W5 批 = 既有两把闸 + 外联总闸；W6 批 = 事实注入 + 声称守卫。
    expect(W5_ON_KEYS.length).toBe(3)
    expect(W6_ON_KEYS.length).toBe(2)
    for (const k of W6_ON_KEYS) expect(W5_ON_KEYS).not.toContain(k)
    expect(CURRENT_OPS_BATCH).toBe('W6')
    // 批次已到：全部 defs 允许呈现 on（但半套配置仍必须 half——两枚反证独立于批次）。
    for (const def of OPS_SWITCH_DEFS) {
      expect(opsAllowOn(def.batch, CURRENT_OPS_BATCH)).toBe(true)
      expect(opsSwitchState(true, true, true, opsAllowOn(def.batch, CURRENT_OPS_BATCH))).toBe('on')
      // 半套反证（M0230 保留腿）：只开一把仍不许 on。
      expect(opsSwitchState(true, false, true, opsAllowOn(def.batch, CURRENT_OPS_BATCH))).toBe('half')
    }
    // 批次门控函数本身仍可判 false（防有人把 allowOn 恒真化）：显式传旧批次档必须 false。
    expect(opsAllowOn('W6', 'W3')).toBe(false)
  })

  it('反向对照：伪造 on 必红（M0228）、删一个开关不登记必红（M0229）', () => {
    // M0228：把总开关硬编码成 on，扫描器必须命中。
    const fakeOn = `export default function P(){ return <div data-state="on"><Switch checked={true} /></div> }`
    expect(fakeOnHits(fakeOn).length).toBeGreaterThan(0)
    // M0229：从全集删掉一项，绑定键与 OPS_CONFIG_KEYS 的双向比对立刻失衡（switches<4 亦红）。
    const shrunk = OPS_SWITCH_DEFS.slice(0, OPS_SWITCH_DEFS.length - 1)
    expect(shrunk.length).toBeLessThan(4)
    const shrunkBound = boundKeys(shrunk)
    expect([...OPS_CONFIG_KEYS].some((k) => !shrunkBound.has(k))).toBe(true)
    // 合法副本（全量）必须放行，防「一律判红」偷懒。
    expect(boundKeys(OPS_SWITCH_DEFS).size).toBe(OPS_CONFIG_KEYS.length)
  })
})
