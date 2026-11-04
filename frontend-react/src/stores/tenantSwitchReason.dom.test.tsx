// ============================================================================
// tenantSwitchReason.dom.test.tsx — ★ 2026-11-04 体验批 §3.2 路线② 的守卫锁。
//
// 用户报的第 3 条是「平台管理员切不到租户，只能在平台根用」。实读结论（方案 §3.1）：
// 切换链**没被删过**（切换器 → X-Tenant-ID → effTenant 三段全在，UAT 也在跑），
// 坏的是"你是谁"这一判：后端 IsSuperAdmin = level>=4 **且 tenant_id==0**，
// 前端 stores/admin.tsx 同步钳级——于是历史旁路产生的「role=admin 且挂在某个租户下」的账号
// 看到的就是**切换器凭空消失，且没有任何一句话说明为什么**。
//
// 本锁判两半，缺任一半都等于没修（AGENTS §一·5 那条「只查顶栏有没有」的教训）：
//   R1 家行在平台根（tenant_id=0）⇒ isSuper=true 且 platformBlocked=false（切换器该在）；
//   R2 等级够但行不在平台根     ⇒ isSuper=false（钳级照旧，不放宽安全面）**且** platformBlocked=true（原因该在）；
//   R3 接线：AdminDashboard 必须把那两个布尔**各用一个分支**接上——
//      钳级判据在位、切换器随 isSuper、原因随 platformBlocked。只测 R1/R2 的话，
//      "store 算对了但界面根本没读"照样全绿（判据级代替不了守卫级）。
//
// ⚠️ 本批刻意**不**放宽判定（方案 §3.2 路线③被否）：admin_authz_scale_gate_test.go 明令
//   不许再造第二把超管尺子；这里只把沉默变成一句话。
// 运行：cd frontend-react && npx vitest run src/stores/tenantSwitchReason.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeAll } from 'vitest'
import { readFileSync } from 'node:fs'
import { renderHook } from '@testing-library/react'
import { useAdmin } from './admin'
import { useAuthStore } from './auth'

// 取一份 useAdmin 的读数（user 由 auth store 注入，租户上下文走 zustand 默认值）
function readAdmin(user: { id: number; username: string; role: string; tenant_id: number }) {
  useAuthStore.setState({ user: user as never, restoring: false })
  const { result } = renderHook(() => useAdmin())
  return result.current
}

describe('后台身份钳级与"为什么被钳"那句（§3.2 路线②）', () => {
  beforeAll(() => { localStorage.setItem('app_lang', 'zh') })

  it('R1：账号挂在平台根 ⇒ 是超管、不被钳（切换器本该在位）', () => {
    const a = readAdmin({ id: 1, username: 'root_admin', role: 'admin', tenant_id: 0 })
    expect(a.isSuper).toBe(true)
    expect(a.platformBlocked).toBe(false)
  })

  it('R2：等级够但账号不在平台根 ⇒ 钳级照旧，但原因布尔必须翻起来', () => {
    const a = readAdmin({ id: 2, username: 'tenant_admin_l4', role: 'admin', tenant_id: 5 })
    // 钳级这一半**不许放宽**（后端按 3 级处理，前端跟着钳是纵深防御）
    expect(a.isSuper).toBe(false)
    expect(a.myLevel).toBe(3)
    // 出声那一半：过去这里只有静默钳级，当事人看到的是"功能没了"
    expect(a.platformBlocked).toBe(true)
  })

  it('R3：AdminDashboard 把两个布尔各自接上（守卫级，不是判据级）', () => {
    const src = readFileSync('src/components/admin/AdminDashboard.tsx', 'utf8')
      .split('\n').filter((l) => !l.trim().startsWith('//') && !l.trim().startsWith('*')).join('\n')
    // 切换器仍随 isSuper（没被顺手改成"谁都能切"）
    expect(src).toMatch(/ad\.isSuper\s*&&/)
    // 原因那句必须随 platformBlocked 出现，且取的是那个键（不是硬编码中文——硬编码＝12 语种之外的又一处文案）
    expect(src).toMatch(/ad\.platformBlocked\s*&&/)
    expect(src).toContain("t('admin.notOnPlatformRoot')")
    // 反向对照：原因不许被挂在 !ad.isSuper 那种"所有非超管都看到"的宽分支上
    //   （租户管理员/部门管理员本来就无切换需求，给他们弹这句是噪音，也会把真信号埋掉）
    expect(src).not.toMatch(/!\s*ad\.isSuper\s*&&[^]*?admin\.notOnPlatformRoot/)
  })
})
