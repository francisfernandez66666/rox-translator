// ============================================================================
// AssistP.cfgParity.lock.test.ts — AssistP ⇄ 旧管理台能力差集 parity 闸（三期 W1 / A03）
//
// 判据两条：
//   ① AssistP.tsx 的 CHAT_CFG 键全集 ⇄ assist/web/admin.html 的 CFG_DEFS 键全集等值
//      （React 面板是主路径、admin.html 退备用；备用台多出一键＝迁移差集没补齐，2026-11-10 复核实测缺口）。
//   ② 主后台代理 admin_assist_proxy.go 的两个 map 都登记 system-values，且 assist 上游真实注册了该路由
//      （防「映射到不存在的口」空转）。
// 反向对照走内存字符串（与真实文件喂同一组纯函数）：删 tone_rules 必红、删映射必红、合法样本必放行。
// 运行：cd frontend-react && npx vitest run src/components/admin/AssistP.cfgParity.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'

// 本文件位于 src/components/admin/ 下，退两级是 src/，再退两级到仓库根
const SRC = fileURLToPath(new URL('../..', import.meta.url))
const REPO = join(SRC, '../..')

const read = (p: string) => readFileSync(p, 'utf8')

/** 从 AssistP.tsx 现读 CHAT_CFG 数组字面量（单行形态）；解析不到回空数组由射程腿判死 */
function chatCfgKeys(src: string): string[] {
  const m = src.match(/const CHAT_CFG = \[([^\]]*)\]/)
  if (!m) return []
  return [...m[1].matchAll(/'([^']+)'/g)].map((x) => x[1])
}

/** 从 admin.html 现读 CFG_DEFS 对象字面量的键（每行一键，值里不含行首 }） */
function cfgDefsKeys(html: string): string[] {
  const m = html.match(/const CFG_DEFS = \{([\s\S]*?)\n\}/)
  if (!m) return []
  return [...m[1].matchAll(/^\s*([a-z_]+)\s*:/gm)].map((x) => x[1])
}

/** 键集差集（双向点名）：等值锁的判据本体，真实文件与变异样本喂同一个函数 */
function keyDiff(chat: string[], defs: string[]): string[] {
  const sc = new Set(chat)
  const sd = new Set(defs)
  return [
    ...chat.filter((k) => !sd.has(k)).map((k) => `仅 CHAT_CFG 有: ${k}`),
    ...defs.filter((k) => !sc.has(k)).map((k) => `仅 CFG_DEFS 有: ${k}`),
  ]
}

/** 代理 routes 表映射在位（主后台路径 → assist 上游路径） */
function proxyRoutesOk(src: string): boolean {
  return src.includes('"/api/admin/assist/system-values": "/api/assist/admin/system-values"')
}

/** 代理 areas 表登记在位（审计 detail 用） */
function proxyAreasOk(src: string): boolean {
  return src.includes('"/api/admin/assist/system-values": "system_values"')
}

/** assist 上游真实注册了该路由（防映射到不存在的口） */
function upstreamRegistered(src: string): boolean {
  return src.includes('mux.HandleFunc("/api/assist/admin/system-values"')
}

describe('AssistP 配置键 ⇄ 旧管理台 CFG_DEFS 等值（A03）', () => {
  const assistSrc = read(join(SRC, 'components/admin/AssistP.tsx'))
  const adminHtml = read(join(REPO, 'backend-go/internal/assist/web/admin.html'))
  const proxySrc = read(join(REPO, 'backend-go/internal/api/admin_assist_proxy.go'))
  const upstreamSrc = read(join(REPO, 'backend-go/internal/assist/api/server.go'))

  it('射程自证：两侧键集都解析得出且非空（解析失败不许读成「等值」）', () => {
    expect(chatCfgKeys(assistSrc).length, 'CHAT_CFG 解析为空 ⇒ 锚点漂了，本闸已失明').toBeGreaterThanOrEqual(7)
    expect(cfgDefsKeys(adminHtml).length, 'CFG_DEFS 解析为空 ⇒ 锚点漂了，本闸已失明').toBeGreaterThanOrEqual(7)
  })

  it('CHAT_CFG 键全集与 CFG_DEFS 逐键等值（迁移差集必须补齐）', () => {
    expect(keyDiff(chatCfgKeys(assistSrc), cfgDefsKeys(adminHtml))).toEqual([])
  })

  it('system-values：代理两个 map 都登记，且 assist 上游真实注册', () => {
    expect(proxyRoutesOk(proxySrc), 'system-values 代理映射缺失（routes 表）').toBe(true)
    expect(proxyAreasOk(proxySrc), 'system-values 代理映射缺失（areas 表）').toBe(true)
    expect(upstreamRegistered(upstreamSrc), 'system-values 上游路由未注册（映射空转）').toBe(true)
  })

  it('反向对照：删 tone_rules 必红、删映射必红、合法样本必放行', () => {
    // 删 tone_rules（M0204 形态）：差集必须点名这个键
    const mutated = assistSrc.replace("'tone_rules', ", '')
    expect(mutated, '变异没落地（no-op 会伪装成抓住了破坏）').not.toBe(assistSrc)
    const diff = keyDiff(chatCfgKeys(mutated), cfgDefsKeys(adminHtml))
    expect(diff).toContain('仅 CFG_DEFS 有: tone_rules')

    // 删 routes 映射（M0205 形态）：映射判据必须翻 false
    const line = '"/api/admin/assist/system-values": "/api/assist/admin/system-values"'
    const mutatedProxy = proxySrc.replace(line, '// system-values 映射被移除')
    expect(mutatedProxy, '变异没落地').not.toBe(proxySrc)
    expect(proxyRoutesOk(mutatedProxy)).toBe(false)
    // areas 表那行仍在 ⇒ 两个 map 各判各的，不是同一把字面量喂饱两条
    expect(proxyAreasOk(mutatedProxy)).toBe(true)

    // 合法样本（正向对照）：真实两侧键集本来等值；解析器对合成合法样本也必须认得
    expect(keyDiff(chatCfgKeys(assistSrc), cfgDefsKeys(adminHtml))).toEqual([])
    expect(chatCfgKeys("const CHAT_CFG = ['welcome', 'persona']")).toEqual(['welcome', 'persona'])
    expect(cfgDefsKeys('const CFG_DEFS = {\n  welcome:\'x\',\n  persona:\'y\',\n}')).toEqual(['welcome', 'persona'])
  })
})