// ============ assistOverflow.lock.test.ts · R5 判据锁 ============
// 四期 R5：overflowProblems 的**进程内正反自证**（不依赖后端/夹具，永远不被 skip）。
//
// 为什么要有这一层：e2e/product_reception.spec.ts 的运行时量测需要真后端和真 /p/ 夹具，
// UAT 没跑时那条腿就是「写了但没执行」。这里把同一个纯函数喂正向/越界/误伤三组读数，
// 保证「判据被改坏」在任何一次 vitest 里都会红（变异体 M0359 的靶）。
//
// 三条反证缺一不可：
//  ① 右边界越出视口必须被点名（R5 要抓的主形态）；
//  ② 中间滚动祖先被撑宽必须被点名（只问 documentElement 会漏）；
//  ③ products_old 式的相邻名字不许误伤（否则闸门逼人改名而不是改布局）。
// ==============================================================================
import { describe, expect, it } from 'vitest'

import { TOL_PX, overflowProblems } from './assistOverflow'

describe('R5 overflowProblems 判据自证', () => {
  it('正常 320 档读数放行', () => {
    const problems = overflowProblems(
      320,
      740,
      [{ label: '.na-fab', rect: { left: 250, right: 306, top: 660, bottom: 716 } }],
      [{ label: 'html', scrollWidth: 320, clientWidth: 320 }],
    )
    expect(problems).toEqual([])
  })

  it('右边界越界必须被点名（M0359 变异：放宽右边界即红）', () => {
    const problems = overflowProblems(
      320,
      740,
      [{ label: '.na-fab', rect: { left: 290, right: 346, top: 660, bottom: 716 } }],
      [],
    )
    expect(problems.length).toBeGreaterThan(0)
    expect(problems.join('；')).toContain('.na-fab')
  })

  it('中间滚动祖先被撑宽必须被点名（documentElement 恒真那一族的补腿）', () => {
    const problems = overflowProblems(320, 740, [], [
      { label: '.lc-pp-lead', scrollWidth: 348, clientWidth: 320 },
    ])
    expect(problems.length).toBeGreaterThan(0)
    expect(problems.join('；')).toContain('.lc-pp-lead')
  })

  it('浮球被整体挪到视口左侧（right 为负）必须被点名（方案 §2.5 反证形态）', () => {
    const problems = overflowProblems(320, 740, [
      { label: '.na-fab', rect: { left: -80, right: -24, top: 660, bottom: 716 } },
    ], [])
    expect(problems.length).toBeGreaterThan(0)
    expect(problems.join('；')).toContain('左边界')
  })

  it('容差内不误伤：2px 描边取整不算溢出', () => {
    const problems = overflowProblems(320, 740, [], [
      { label: 'body', scrollWidth: 320 + TOL_PX, clientWidth: 320 },
    ])
    expect(problems).toEqual([])
  })
})
