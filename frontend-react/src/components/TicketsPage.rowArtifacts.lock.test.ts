// ============================================================================
// TicketsPage.rowArtifacts.lock.test.ts — §5.2/§5.3 行内两颗下载钮的源码级守卫锁
//
// 钉四件事（都是"改错了不会报错、只会悄悄变回原样"的那一类）：
//   L1 行内 md 与原格式各**恰一处**渲染（多包一层容器／重复渲染都判红）；
//   L2 顺序判据：`tk.downloadMdRow` 必须出现在 `tk.downloadOriginalRow` **之前**
//      ——这就是用户要的那句"先看文字、再看原文件"的唯一机械落点，不能靠 review 记着；
//   L3 进行中那一档必须是**灰态且不含任何可点形状**：灰态分支里不许出现 dl/rowArtifactDl 调用
//      （服务端"未完成不给取产物"那道 409 保留＝完成态才可下，用户已拍板；
//       给用户一颗点了必 409 的钮才是缺陷本体）；
//   L4 两颗钮的产物 id 只许来自列表出栈的 artifact_summary（不许前端自己再拼一套选取规则，
//      那是"两处各取各的"的起点；后端同源判据在 api/ticket_artifact_summary_test.go C3/C4）。
//
// ⚠️ 判据先剥注释：本文件对应的实现里，注释逐字抄着"该状态才露这个钮"这条**被有意破一次的旧纪律**
//   与旧写法，不剥注释会把记录当成违规（AGENTS §一·5 那条同判）。
// 运行：cd frontend-react && npx vitest run src/components/TicketsPage.rowArtifacts.lock.test.ts
// ============================================================================
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'

const raw = readFileSync('src/components/TicketsPage.tsx', 'utf8')
const code = raw.split('\n').filter((l) => !l.trim().startsWith('//')).join('\n')

describe('工单行内两颗下载钮（§5.2/§5.3）', () => {
  it('L1 行内 md 与原格式各恰一处', () => {
    expect(code.match(/tk\.downloadMdRow/g)?.length ?? 0).toBe(1)
    expect(code.match(/tk\.downloadOriginalRow/g)?.length ?? 0).toBe(1)
    expect(code.match(/tk\.originalRowMissing/g)?.length ?? 0).toBe(1)
    expect(code.match(/tk\.mdAfterDone/g)?.length ?? 0).toBe(1)
  })

  it('L2 顺序：md 在前、原格式在后', () => {
    const i = code.indexOf('tk.downloadMdRow')
    const j = code.indexOf('tk.downloadOriginalRow')
    expect(i).toBeGreaterThan(-1)
    expect(j).toBeGreaterThan(-1)
    expect(i).toBeLessThan(j) // 反证：把两颗钮调换位置 ⇒ 本条红
  })

  it('L3 进行中那一档是灰态，且分支里没有任何可点下载形状', () => {
    const i = code.indexOf('tk.mdAfterDone')
    expect(i).toBeGreaterThan(-1)
    // 取那一档所在的 JSX 条件块（从 queued/in_progress 判定到下一个右花括号簇）
    const start = code.lastIndexOf("['queued', 'in_progress']", i)
    expect(start, 'mdAfterDone 不在进行中那一档的条件渲染里（判据锚点漂了）').toBeGreaterThan(-1)
    const block = code.slice(start, i + 220)
    expect(block).not.toMatch(/(<Link|rowArtifactDl\(|downloadText\(|download\()/)
    // 而且这一档必须**在 DOM 里**（灰态＋原因，不是整颗消失——缺席必须点名，不能沉默）
    expect(block).toContain('data-testid="tk-row-dl-waiting"')
  })

  it('L4 原格式那颗钮的产物 id 只来自列表摘要', () => {
    expect(code).toMatch(/rowArtifactDl\(row, Number\(row\.artifact_summary\?\.original\?\.id\)\)/)
    // 不许前端自己按 kind/lang 再挑一次（那是把后端刚定好的选组规则抄成第二份）
    expect(code).not.toMatch(/row\.artifacts\?\.find|deliverables\.find\(\s*\(d[:\s]*any\)\s*=>\s*d\.kind === 'original'\s*\)/)
  })

  it('L5 完成态才渲染两颗可点钮；draft／cancelled／rejected 一律不放钮', () => {
    const iMd = code.indexOf('tk.downloadMdRow')
    const guard = code.lastIndexOf("row.status === 'completed'", iMd)
    expect(guard, '行内 md 那颗钮不在 completed 条件里').toBeGreaterThan(-1)
    // 反向对照：灰态那一档的条件是 queued/in_progress，与可点档互斥
    expect(code).toMatch(/\['queued', 'in_progress'\]\.includes\(row\.status\)/)
  })
})
