// ============================================================================
// components/assistRouteWhitelist.lock.test.ts — AI 顾问深链白名单的**全集**判据（H3，2026-11-06）
//
// 要防的缺陷不是"白名单写错"，而是**新增了一个入口页，而对话里那颗按钮永远跳不进去**：
// 现网形态＝用户在挂件里问「怎么建第一个产品」，功能卡配的 url 是 `/onboarding`，
// 而 `AiAssist.tsx` 的 `ROUTE_PATHS` 里没有这一条 ⇒ 它按"非站内路径"分支**当外链新开窗口**，
// 访客在新窗口里落到未登录门面（或直接 404），全程零报错、零日志。
// 这类"没被接住的入口"是 §三「有脚本无调度要问到第二层」在客户端的形态。
//
// 判据取向（三条，一条都不许省）：
//  ① **按 App.tsx 的路由全集判**（现读 `path="…"`），不是按"我记得有哪几页"判；
//  ② 不在白名单里的路由必须**逐条挂在豁免表上并写明理由**——空理由与"只加 key 不写原因"都判红；
//  ③ 两条**射程自证**：App.tsx 解析出的路由数必须 ≥ 下限（解析失效＝闸门失明，不是"都过了"），
//     白名单解析出的条数必须 ≥ 1 且真的来自那条 `new Set([...])`。
//
// 本文件是静态源码锁（读源文件、不起组件）⇒ 按仓内口径放 **node 环境**（见 vitest.config 的 project 分档）。
// ============================================================================
import { readFileSync } from 'node:fs'
import * as path from 'node:path'
import { describe, expect, it } from 'vitest'

const ROOT = path.resolve(__dirname, '../..')
const APP_SRC = readFileSync(path.join(ROOT, 'src/App.tsx'), 'utf8')
const ASSIST_SRC = readFileSync(path.join(ROOT, 'src/components/AiAssist.tsx'), 'utf8')

/**
 * 豁免表：**站内存在但刻意不让挂件深链跳**的路由，每条必须带理由。
 * 理由写不出来的路由就不该进这张表（要么加进白名单，要么这页本身不该存在）。
 */
const EXEMPT: Record<string, string> = {
  '/login': '挂件在登录/注册页整颗不渲染（isHiddenPath），没有"从对话跳过去"这一说',
  '/compare': '公开比价门面，登录态里由左栏与定价页互跳；挂件配的是外链形态的运营页，不占站内白名单',
  '/chat-history': '次级列表页，入口在工作台内的历史浮层；跳过去会丢当前会话上下文，刻意不做深链',
  '/products/facts': '一期老面板路由，货架 /products 才是入口（跳子页会让"从哪开始"分叉成三处）',
  '/products/answers': '同上：知识问答是货架上的一项动作，入口统一收在 /products',
  '/products/detail': '详情页必须带 ?id= 才有意义，裸路径跳过去是空态；由货架与引导流各自带参跳',
}

/** 从 App.tsx 现读注册在案的路由（排除通配兜底那条） */
function appRoutes(src: string): string[] {
  const out = new Set<string>()
  const re = /path="([^"]+)"/g
  let m: RegExpExecArray | null
  while ((m = re.exec(src)) !== null) {
    const p = m[1]
    if (p === '*' || p === '') continue
    out.add(p)
  }
  return [...out].sort()
}

/** 从 AiAssist.tsx 现读 ROUTE_PATHS 那一条 new Set([...]) 的字面量成员 */
function whitelist(src: string): string[] {
  const decl = src.match(/const ROUTE_PATHS = new Set\(\[([^\]]*)\]\)/)
  if (!decl) return []
  const out: string[] = []
  const re = /'([^']+)'/g
  let m: RegExpExecArray | null
  while ((m = re.exec(decl[1])) !== null) out.push(m[1])
  return out
}

describe('AI 顾问深链白名单必须覆盖站内入口路由（全集判，不是点位判）', () => {
  const routes = appRoutes(APP_SRC)
  const allow = whitelist(ASSIST_SRC)

  it('R0 射程自证：两侧都真解析出了东西（解析失效＝闸门失明，不许读成"通过"）', () => {
    expect(routes.length, `App.tsx 只解析出 ${routes.length} 条路由，低于下限 12 ⇒ 解析腿漂了`).toBeGreaterThanOrEqual(12)
    expect(allow.length, 'ROUTE_PATHS 解析为空 ⇒ 本锁没在读真白名单').toBeGreaterThanOrEqual(10)
    // 白名单里必须至少有一条是"只有挂件会用到"的站内路径，证明读的是那一行而不是别的 new Set
    expect(allow).toContain('/tickets')
  })

  it('R1 每一条站内路由：要么在挂件白名单里，要么挂在豁免表上并写明理由', () => {
    const missing: string[] = []
    for (const r of routes) {
      if (allow.includes(r)) continue
      const why = EXEMPT[r]
      if (!why) { missing.push(r); continue }
      expect(why.trim().length, `${r} 挂了豁免但没有理由`).toBeGreaterThan(8)
    }
    expect(missing, `这些路由既不在挂件深链白名单、也没写豁免理由 ⇒ 对话里点不到、也不报错：${missing.join(' , ')}`).toEqual([])
  })

  it('R2 第一公里两页必须在白名单里（本批的缺陷本体，不许被"顺手豁免"绕开）', () => {
    expect(allow, '引导流入口没进白名单 ⇒ 挂件问"怎么建第一个产品"时跳不进去').toContain('/onboarding')
    expect(allow, '卖家货架没进白名单 ⇒ 同上').toContain('/products')
    expect(EXEMPT['/onboarding'], '引导流**不许**被写进豁免表').toBeUndefined()
    expect(EXEMPT['/products'], '货架**不许**被写进豁免表').toBeUndefined()
  })

  it('R3 豁免表不许长出"其实已经在白名单里"的重复条目（两张嘴说同一件事迟早相反）', () => {
    const both = Object.keys(EXEMPT).filter((r) => allow.includes(r))
    expect(both, `这些路径同时被声明为"不让跳"与"可以跳"：${both.join(' , ')}`).toEqual([])
  })

  it('R4 反证：把白名单里那两条第一公里路由摘掉，本锁必须判红（证明它测的是这一行）', () => {
    // ★ 变异**从真声明里派生**，不在本文件里再抄一份完整白名单字面量——
    //   抄一份的后果是：将来有人往白名单加第三条路由，这条反证会因为"锚点找不到"而变成空转，
    //   而空转的反证比没有反证更坏（它让所有人以为这条锁被证过）。
    const mutated = ASSIST_SRC.replace(
      /const ROUTE_PATHS = new Set\(\[([^\]]*)\]\)/,
      (_s, inner: string) => `const ROUTE_PATHS = new Set([${inner.replace(/'\/onboarding',\s*/, '').replace(/, *'\/products'/, '')}])`,
    )
    expect(mutated, '变异没改动源码（锚点漂了，这条反证是空转）').not.toBe(ASSIST_SRC)
    const m = whitelist(mutated)
    expect(m).not.toContain('/onboarding')
    expect(m).not.toContain('/products')
    // 摘掉后 R1 的判据必须现形（这两页既不在白名单、也不许在豁免表里）
    expect(appRoutes(APP_SRC).filter((r) => !m.includes(r) && !EXEMPT[r])).toContain('/onboarding')
  })
})
