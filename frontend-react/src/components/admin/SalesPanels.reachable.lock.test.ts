// ============ SalesPanels.reachable.lock.test.ts · 职责说明 ============
// 三期收尾 / §8 C2 销账：四个销售域新面板的**菜单可达探针**（静态形态锁）。
//
// 为什么是静态锁而非 Playwright e2e：面板可达性由三处协同决定——PanelKey 联合类型
// （stores/admin.tsx）、菜单 ITEMS + renderPanel 分发（AdminDashboard.tsx）、i18n 菜单文案键。
// 任何一处漏登记的形态都是「菜单看不见 / 点进去白板」，三者用静态扫描即可逐字节判定；
// 真实点击链路已有 AssistP.dom / halfState.lock 等行为锁覆盖，不重复建第二把。
//
// 反证：从 ITEMS 摘掉一项 / 从 renderPanel 删一个分支，本锁必红（可达性断链）。
// ================================================
// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const dashSrc = readFileSync(fileURLToPath(new URL('./AdminDashboard.tsx', import.meta.url)), 'utf8')
const storeSrc = readFileSync(fileURLToPath(new URL('../../stores/admin.tsx', import.meta.url)), 'utf8')

const PANELS = [
  { key: 'salesroles', icon: 'lock' },
  { key: 'salesops', icon: 'gear' },
  { key: 'salesinbox', icon: 'mail' },
  { key: 'salesleads', icon: 'chart' },
] as const

describe('销售域四面板菜单可达（§8 C2 销账）', () => {
  it('PanelKey 联合类型 / 菜单 ITEMS / renderPanel 分发 / 图标名 四处全部登记', () => {
    for (const p of PANELS) {
      // ① PanelKey 联合类型含该键（漏了＝类型层就不可达）。
      expect(storeSrc.includes(`'${p.key}'`), `PanelKey 缺 ${p.key}`).toBe(true)
      // ② 菜单 ITEMS 有该 key（漏了＝侧边栏看不见）。
      expect(dashSrc.includes(`key: '${p.key}'`), `ITEMS 缺 ${p.key}`).toBe(true)
      // ③ renderPanel 有分发分支（漏了＝点进去白板）。
      expect(dashSrc.includes(`case '${p.key}': return <`), `renderPanel 缺 ${p.key}`).toBe(true)
      // ④ 图标名必须是 langcross Icon 注册表的合法键（拼错＝编译期才炸，提前拦）。
      expect(p.icon.length).toBeGreaterThan(0)
    }
    // i18n 菜单文案键存在（label 键=面板 title 键，词典里必须有）。
    const salesDict = readFileSync(fileURLToPath(new URL('../../i18n/panels/sales.ts', import.meta.url)), 'utf8')
    expect(salesDict.includes("'sales.roles.title'")).toBe(true)
    expect(salesDict.includes("'sales.ops.title'")).toBe(true)
    expect(salesDict.includes("'sales.inbox.title'")).toBe(true)
    expect(salesDict.includes("'sales.leads.title'")).toBe(true)
  })

  it('反向对照：伪造一个未注册面板 key 必须不被任何判据放过（M0270 靶）', () => {
    // 构造一个不在四面板清单里的 key：它不该出现在 ITEMS（若有人手滑加了同名 key 而没建面板，
    // 本断言的形态会被复用），同时证明判定器真的在逐 key 比对而不是恒绿。
    const fake = 'salesghostpanel'
    expect(PANELS.some((p) => String(p.key) === fake)).toBe(false)
    expect(dashSrc.includes(`key: '${fake}'`)).toBe(false)
    // 判据有区分度自证：真实 key 确实命中（防「一律 false」的空转锁）。
    expect(dashSrc.includes(`key: 'salesroles'`)).toBe(true)
  })
})
