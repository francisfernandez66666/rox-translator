// ============================================================================
// i18n/missingKeyGate.test.ts — ★「取词踩空」静态扫描闸门（2026-09-22 建立）
//
// 为什么要扫：本仓 i18n 的 t()/tpl()（含 `import { t as gt, tpl as gtpl }` 别名写法）
//   对**词典里不存在的键**不抛错、不告警，而是把键名字面量原样回显（见 i18n/index.ts
//   的 `|| key` 兜底）。也就是说写错一个键 = 界面上直接出现 "auth.togglePwd" 这种字符串。
// 真实用户后果（本仓历史上发生过）：
//   ① 用户在登录页/后台按钮上看到裸键名而不是文案，产品直接露馅；
//   ② 屏幕阅读器会把 "auth.togglePwd" 逐字符念出来（无障碍链路被污染，且只在中文界面复现）；
//   ③ 其余 11 语种走 lang→en→zh 回退时同样落空，一个错键 = 十二语种全坏，而不只是某语种漏译；
//   ④ 这类缺陷**运行时零信号**，不会进监控、不会进 e2e（除非恰好断言到那段文案），
//      只能靠静态闸门在提交前拦住。
// 与既有闸门的分工：
//   - locales.core.test.ts 管「十语种 vs ALL_KEYS 的键完整性」（词典之间）；
//   - parity.test.ts 管「面板中英对等」（词典之间）；
//   - auditActionCoverage.test.ts 管「后端审计动作码是否登记词典」（调用点→词典，但只覆盖 LogAudit）；
//   - 本闸门补上最后一块：**代码里引用的每个字面量键，是否真的存在于 zh 全量词典**。
// 合法键口径：src/i18n/index.ts 导出的 ALL_KEYS（合并词典全量键清单，词典的权威来源；
//   zh 与 en 的键集由 parity.test.ts 逐面板锁死为一致，十语种又由 locales.core.test.ts
//   锁死为逐键覆盖，因此 ALL_KEYS 即「zh 全量词典」的等价权威集合）。
// 如何新增键才不会再踩空：先在 panels/<域>.ts 的 **zh 与 en 两份**里登记同一键
//   （新面板须登记进 index.ts 合并位与 parity.test.ts 的 PANELS 表），再在组件里引用；
//   跨语种补译按 AGENTS.md 一.5 的 12 语种口径同步（十份 locale 逐键）。
//   引用侧只能用完整字符串字面量；写 `t(\`admin.x.${k}\`)` 这类动态键本闸门看不到（会被计入
//   「跳过」统计），必须自行保证运行期取到的键存在。
// 基线（只减不增）：存量踩空键写进 BASELINE 白名单，补译/改对后**必须删掉对应项**；
//   新增踩空键即红灯。与 backend-go/internal/observability/logratchet_test.go、
//   本目录 auditActionCoverage.test.ts 同一棘轮手法。
// 运行：npx vitest run src/i18n/missingKeyGate.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { ALL_KEYS } from './index'

/** SRC 根：本文件位于 src/i18n 下，上一层即 src */
const SRC = fileURLToPath(new URL('..', import.meta.url))

/**
 * 取词函数名（**单点维护**：下方三条正则一律由 FN_ALT 拼出）：
 *  - t / tpl：i18n/index.ts 直导出；
 *  - gt / gtpl：本仓惯用别名 `import { t as gt, tpl as gtpl } from '@/i18n'`
 *    （见 src/App.tsx、src/hooks/useChat.tsx、components/admin/BrandTermsP.tsx）。
 * 新增取词别名只加这一项即可，三条正则同步生效——防止「改了别名却漏改某条正则」造成静默漏扫。
 */
const FN = ['t', 'tpl', 'gt', 'gtpl']

/** FN 的正则分支串。三条扫描正则共用，避免「加了别名却忘了改正则」这类静默漏扫。 */
const FN_ALT = FN.join('|')

/**
 * 扫描范围排除项（WHY）：
 *  - *.test.ts(x)：测试里的键常是构造的假键，不代表生产口径；
 *  - i18n/panels/**、i18n/locales/**、i18n/dicts.*.ts：这些是**词典本体**，不是引用点，
 *    扫进来会把词典自身键当成引用（且 panels 内部还有键名拼接），只留下噪声；
 *  - node_modules/dist：构建产物与三方包。
 */
function skipDir(name: string): boolean {
  return name === 'node_modules' || name === 'dist' || name === 'panels' || name === 'locales'
}

/** collect 递归收集待扫源文件 */
function collect(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = `${dir}/${name}`
    const st = statSync(p)
    if (st.isDirectory()) {
      if (!skipDir(name)) collect(p, out)
      continue
    }
    if (!/\.(ts|tsx)$/.test(name)) continue
    if (/\.test\.tsx?$/.test(name)) continue
    if (/^dicts\.(zh|en)\.ts$/.test(name)) continue // 基础词典本体
    out.push(p)
  }
  return out
}

/**
 * 抹掉注释内容但**保留行号与字符偏移**（替换成空格），避免注释/doc 里提到的
 * t('x.y') 例子被当成真实引用（本仓 i18n 说明块大量出现这种示例）。
 */
function stripComments(src: string): string {
  const b = src.split('')
  const blank = (from: number, to: number) => {
    for (let i = from; i < to && i < b.length; i++) if (b[i] !== '\n') b[i] = ' '
  }
  let i = 0
  let mode: 'code' | 'line' | 'block' | 'sq' | 'dq' | 'tpl' = 'code'
  while (i < src.length) {
    const c = b[i]
    const d = b[i + 1]
    if (mode === 'code') {
      if (c === '/' && d === '/') { mode = 'line'; blank(i, i + 2); i += 2; continue }
      if (c === '/' && d === '*') { mode = 'block'; blank(i, i + 2); i += 2; continue }
      if (c === "'") mode = 'sq'
      else if (c === '"') mode = 'dq'
      else if (c === '`') mode = 'tpl'
      i++
      continue
    }
    if (mode === 'line') {
      if (c === '\n') mode = 'code'
      else blank(i, i + 1)
      i++
      continue
    }
    if (mode === 'block') {
      if (c === '*' && d === '/') { mode = 'code'; blank(i, i + 2); i += 2; continue }
      blank(i, i + 1)
      i++
      continue
    }
    // 字符串内部：处理转义，遇到结束引号回到 code（模板串的 ${} 内再嵌注释不在本闸门职责内）
    if (c === '\\') { i += 2; continue }
    if ((mode === 'sq' && c === "'") || (mode === 'dq' && c === '"') || (mode === 'tpl' && c === '`')) mode = 'code'
    i++
  }
  return b.join('')
}

/** 一次取词引用的抽取结果 */
interface Ref { key: string; file: string; line: number }

/**
 * 取词调用扫描正则（函数名分支由 FN_ALT 单点维护）：
 *   (^|[^A-Za-z0-9_$.])  —— 词边界，排除 fmt(/match(/obj.t( 等同名后缀与属性调用
 *   (t|tpl|gt|gtpl)       —— 函数名
 *   \s*\(\s*             —— 左括号
 *   (['"])((?:[^'"\\\n]|\\.)*)\2  —— **第一个实参必须是完整字符串字面量**
 *   \s*[,)]              —— 后面紧跟 ,（有第二参）或 )（无参），保证不是 'a.b' + x 这种拼接
 */
const LITERAL_RE = new RegExp(String.raw`(^|[^A-Za-z0-9_$.])(${FN_ALT})\s*\(\s*(['"])((?:[^'"\\\n]|\\.)*)\3\s*[,)]`, 'g')
/** 动态键形态（无法静态判定，计入「跳过」统计）：模板串 / 变量或表达式 / 三元 */
const DYN_TPL_RE = new RegExp(String.raw`(^|[^A-Za-z0-9_$.])(${FN_ALT})\s*\(\s*` + '`', 'g')
const DYN_EXPR_RE = new RegExp(String.raw`(^|[^A-Za-z0-9_$.])(${FN_ALT})\s*\(\s*(?!\s*['"])([A-Za-z0-9_$.<>{[\s?!+-]*)`, 'g')

/** scan 扫描全部源文件，返回字面量引用清单与动态引用计数 */
function scan() {
  const refs: Ref[] = []
  const dynamic: { template: number; expression: number } = { template: 0, expression: 0 }
  for (const abs of collect(SRC)) {
    const rel = abs.slice(SRC.length + 1).replace(/\\/g, '/')
    const src = stripComments(readFileSync(abs, 'utf8'))
    for (const m of src.matchAll(LITERAL_RE)) {
      const line = src.slice(0, m.index).split('\n').length
      refs.push({ key: m[4], file: rel, line })
    }
    // 先数模板串（`t(`a.b.${k}`)`），再从"表达式"计数里排除同一位置，避免重复统计
    const tplIdx = new Set<number>()
    for (const m of src.matchAll(DYN_TPL_RE)) { dynamic.template++; tplIdx.add(m.index) }
    for (const m of src.matchAll(DYN_EXPR_RE)) { if (!tplIdx.has(m.index)) dynamic.expression++ }
  }
  return { refs, dynamic }
}

/** 一条基线豁免：踩空键 + 位置（文件:行号）+ 中文备注（待补译 / 待修） */
interface BaselineItem { at: string; key: string; note: string }

// 2026-09-22 建闸时的存量踩空 13 处 / 11 个键已**全部清零**：
//  - 10 个键属词典真缺，按 AGENTS.md 一.5 补进 zh + en + 十份 locales
//    （common.fail、chat.searchClear、auth.orgInvite、auth.selectIndustry、
//     tk.edTitle / edColLang / edLoad / edSave / edTermsHit / edEmptyHint，
//     批量补译脚本 scripts/i18n/add_gate_keys.py，可重跑）；
//  - 1 处（Login.tsx 的 auth.roleMember）词典本有语义正确的 auth.roleStaff，改引用即修。
// 之后新增踩空键一律红灯；确需临时豁免才往本数组加项，并写清「为何豁免 / 摘除条件」。
const BASELINE: BaselineItem[] = []

/**
 * inDict —— 判定谓词：键是否在 zh 全量词典（ALL_KEYS）内。
 * 抽成独立函数供「反影子自检」用例喂假键验证（同 backend-go/internal/db/guard_test.go 的思路：
 * 证明规则不是恒真，正则失配/集合取空时自检用例会先红灯）。
 */
function inDict(key: string, legal: Set<string>): boolean {
  return legal.has(key)
}

describe('i18n 取词踩空静态闸门（引用键 ⊆ zh 全量词典，棘轮只减不增）', () => {
  const { refs, dynamic } = scan()
  const legal = new Set(ALL_KEYS)
  const missing = refs.filter((r) => !inDict(r.key, legal))
  const missingIds = new Set(missing.map((r) => `${r.file}:${r.line} ${r.key}`))
  const fmt = (r: Ref) => `${r.file}:${r.line} → ${r.key}`

  it('扫描有效：确实取到足量字面量取词引用（正则失配会让本闸门静默失效）', () => {
    expect(refs.length, '未扫到取词字面量引用，正则或扫描目录可能已失配').toBeGreaterThan(1000)
    // 抽样锚点：这几个键必然存在，扫不到即说明扫描根或正则错了
    for (const anchor of ['app.title', 'common.cancel']) {
      expect(refs.some((r) => r.key === anchor), `抽样锚点 ${anchor} 未被扫到`).toBe(true)
    }
  })

  it('动态键计入跳过统计（模板串/变量/三元静态不可判定，此处只做量级可见）', () => {
    console.log(`[missingKeyGate] 字面量引用 ${refs.length} 处；静态跳过（模板串 ${dynamic.template} / 表达式 ${dynamic.expression}）共 ${dynamic.template + dynamic.expression} 处`)
    // 断言量级而非精确值：拼接键（t(`admin.x.${k}`)）是本仓既有写法，属正常存量；
    // 但数值异常放大通常意味着有人大范围改在取词处用动态键、绕开本闸门，需人工过目。
    expect(dynamic.template + dynamic.expression, '动态取词数量异常，疑似绕开静态闸门').toBeLessThan(400)
  })

  it('新增踩空键 = 0：代码引用的键必须存在于 zh 全量词典（不在基线内即红灯）', () => {
    const fresh = missing.filter((r) => !BASELINE.some((b) => b.at === `${r.file}:${r.line}` && b.key === r.key))
    expect(fresh, `取词踩空（词典无此键，界面会直接显示裸键名 / 屏幕阅读器念出键名）：\n${fresh.map(fmt).join('\n')}`).toEqual([])
  })

  it('基线不养僵尸：BASELINE 里的项必须仍是真实踩空（修好一条就删一条）', () => {
    const zombie = BASELINE.filter((b) => !missingIds.has(`${b.at} ${b.key}`))
    expect(zombie, `BASELINE 中存在已修复/已失效的僵尸豁免项，请从基线删除：\n${zombie.map((b) => `${b.at} → ${b.key}`).join('\n')}`).toEqual([])
  })

  it('棘轮总数：踩空总量不得超过建立基线时的存量', () => {
    expect(missing.length, `踩空 ${missing.length} 处 > 基线 ${BASELINE.length} 处：\n${missing.map(fmt).join('\n')}`)
      .toBeLessThanOrEqual(BASELINE.length)
  })

  it('反影子自检：判定谓词不是恒真（合法键过、故意踩空的键必被判缺）', () => {
    // 取词典里真实存在的键 + 一个绝不可能存在的假键喂给同一谓词。
    // 若哪天 ALL_KEYS 取空或谓词被改坏（例如误写成 || true），本用例先红灯，
    // 而不是让上面几条断言「永远通过」——参照 internal/db/guard_test.go 的反影子写法。
    expect(legal.size, 'ALL_KEYS 为空：词典合并或导出形态已变，本闸门失效').toBeGreaterThan(1000)
    const realKey = ALL_KEYS[0]
    expect(inDict(realKey, legal), `词典自身键 ${realKey} 应判存在`).toBe(true)
    expect(inDict('auth.__definitely_not_a_real_key__', legal)).toBe(false)
  })
})
