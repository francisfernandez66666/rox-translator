// ============================================================================
// brandTabPlacement.lock.test.ts — 2026-10-23 后台报障 F2 的归属锁（node 环境）
//
// 用户令：品牌 tab 从「系统与运维」（超管专属）挪到「组织和成员」（租户管理员可见），
// 页脚链接留在「系统与运维」。这一族最坏的形态不是"挪错"，而是"两处都留一份"或
// "挪走了但旧 key 还能渲染"——界面看起来正常，权限面却多开了一道。
//
// 四条判据（每条都先证明文件读到了锚点，防路径漂移导致空读假绿）：
//   L1 SystemSettingsP 有 footer、无 brand（且不再 import BrandP）
//   L2 OrgP 有 brand 子 tab 并渲染 BrandP
//   L3 BrandP 内部不再有 footer 子 tab / FooterP
//   L4 深链分发仍保留：AdminDashboard 的 renderPanel 对 'brand'/'footer' 两个 key 都有分支
// 运行：npx vitest run src/components/admin/brandTabPlacement.lock.test.ts
// ============================================================================
// @vitest-environment node
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

/** 读取后台组件源码；先断言长度，防路径漂移导致下面的负向锁空转 */
function src(file: string): string {
  const p = resolve(process.cwd(), 'src/components/admin', file)
  const s = readFileSync(p, 'utf8')
  expect(s.length, `${file} 读空了（路径漂移会让下面所有负向锁空转）`).toBeGreaterThan(200)
  return s
}

describe('F2 品牌/页脚 tab 归属', () => {
  it('L1 系统与运维：只留页脚，不再渲染品牌', () => {
    const s = src('SystemSettingsP.tsx')
    expect(s).toContain('export default function SystemSettingsP')
    expect(s).toContain("key: 'footer'")
    expect(s).toContain("t('hub.tabFooter')")
    expect(s).toContain('<FooterP />')
    expect(s).not.toContain("key: 'brand'")
    expect(s).not.toContain('<BrandP')
  })

  it('L2 组织与成员：品牌子 tab 在位并渲染 BrandP', () => {
    const s = src('OrgP.tsx')
    expect(s).toContain('export function OrgP')
    expect(s).toMatch(/'invite' \| 'users' \| 'brand'/)
    expect(s).toContain("t('hub.tabBrand')")
    expect(s).toContain('<BrandP />')
  })

  it('L3 品牌面板内部不再挂页脚', () => {
    const s = src('BrandP.tsx')
    expect(s).toContain('export default function BrandP')
    expect(s).toBeTruthy()
    expect(s).not.toContain('FooterP')
    expect(s).not.toMatch(/\bTabs\b/)
    expect(s).not.toContain("'footer'")
  })

  it('L4 历史深链仍能分发到两个面板（删除分发会让书签白屏）', () => {
    const s = src('AdminDashboard.tsx')
    expect(s).toMatch(/case 'brand': return <BrandP \/>/)
    expect(s).toMatch(/case 'footer': return <FooterP \/>/)
  })
})
