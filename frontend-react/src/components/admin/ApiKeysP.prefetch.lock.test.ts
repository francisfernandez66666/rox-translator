// ============================================================================
// admin/ApiKeysP.prefetch.lock.test.ts — 「非超管挂载即预取超管文档」静态锁
// （★ 2026-10-21 UAT 收口 M6，读数见 10N1 N1-docs：非超管 403 + trace_id）
//
// 缺陷形态：面板挂载 effect 无条件调 getOpenAPIDocs()，而文档卡与编辑钮本来只在
// isSuper 分支里渲染（同文件 :108 / :174）⇒ 非超管每次打开这个面板都白打一条注定
// 403 的请求，控制台留一行红字噪音，对用户没有任何可用信息。
//
// 判据（按形状判，不看"文件里出现过 isSuper"）：
//   A. 找到以 `void loadKeys()` 开头的挂载 effect；
//   B. 它体内调用 getOpenAPIDocs() 之前必须有 `if (!isSuper) return`；
//   C. 该 effect 的依赖数组必须带 isSuper（漏了会让角色切换后仍不预取）；
//   D. 射程自证：剥注释后仍能找到靶子（找不到＝锚点漂了，必须判红而不是静默放行）。
// 反证：喂历史形态（摘掉角色闸 / 摘掉依赖）给同一判据函数必须判红。
// 运行：npx vitest run src/components/admin/ApiKeysP.prefetch.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = readFileSync(join(HERE, 'ApiKeysP.tsx'), 'utf8')

// 剥注释：说明里会写到 getOpenAPIDocs / isSuper，注释冒充实现会让判据恒绿。
function stripComments(src: string): string {
  return src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^[ \t]*\/\/.*$/gm, '')
}

/** 返回违规原因（空串＝合规）。导出以便反证用例直接喂历史形态。 */
export function prefetchRoleViolation(raw: string): string {
  const src = stripComments(raw)
  const idx = src.indexOf('void loadKeys()')
  if (idx < 0) return '锚点漂了：找不到以 void loadKeys() 开头的挂载 effect'
  const tail = src.indexOf('}, [', idx)
  if (tail < 0) return '锚点漂了：挂载 effect 的依赖数组没找到'
  const body = src.slice(idx, tail)
  const call = body.indexOf('getOpenAPIDocs(')
  if (call < 0) return '锚点漂了：挂载 effect 里没有文档预取'
  if (!/if\s*\(\s*!\s*isSuper\s*\)\s*return/.test(body.slice(0, call))) {
    return '文档预取前缺 isSuper 角色闸：非超管会白打一条 403'
  }
  const depsEnd = src.indexOf(']', tail)
  const deps = depsEnd < 0 ? '' : src.slice(tail, depsEnd)
  if (!/\bisSuper\b/.test(deps)) {
    return '依赖数组缺 isSuper：角色切换后预取不会重新判定'
  }
  return ''
}

describe('ApiKeysP 挂载预取的角色闸（M6）', () => {
  it('现役源码：非超管不预取超管文档', () => {
    expect(prefetchRoleViolation(SRC)).toBe('')
  })

  it('反证 A：摘掉 isSuper 角色闸必须判红', () => {
    const mutated = SRC.replace(/if \(!isSuper\) return\r?\n/, '')
    expect(mutated).not.toBe(SRC) // 空替换＝no-op，不能伪装成"锁抓住了破坏"
    expect(prefetchRoleViolation(mutated)).toContain('缺 isSuper 角色闸')
  })

  it('反证 B：依赖数组漏掉 isSuper 必须判红', () => {
    const mutated = SRC.replace(/\}, \[loadKeys, isSuper\]\)/, '}, [loadKeys])')
    expect(mutated).not.toBe(SRC)
    expect(prefetchRoleViolation(mutated)).toContain('依赖数组缺 isSuper')
  })

  it('射程自证：判据必须能认出靶子（不是恒绿空转）', () => {
    expect(prefetchRoleViolation('export function X() { return null }')).toContain('锚点漂了')
  })
})
