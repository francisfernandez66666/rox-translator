// ============ assistOverflow.ts · 职责说明 ============
// 四期 R5：对外页挂件浮球 + 留资表单的「横向溢出」纯度量函数。
//
// ★ 为什么单独成文件：R5 的运行时量测在 e2e/product_reception.spec.ts（Playwright，
//   需要真后端夹具），而**判据函数本身必须有一个不依赖后端的 vitest 锁**
//   （assistOverflow.lock.test.ts，登记变异体 M0359）。两处都 import 这一份，
//   杜绝「e2e 里一份、锁里另一份」那种各写一遍的空转形态。
//
// ★ 判据形态照 AGENTS §一·5：只问 documentElement 是结构性恒真，
//   必须逐层问滚动祖先的 scrollWidth/clientWidth，并把越界件点名到标签。
// ==============================================================================
export type Rect = { left: number; right: number; top: number; bottom: number }
// ScrollBox 是一次滚动祖先读数；label 用于红字点名是哪个容器被撑宽，两个宽度必须同一时刻取自同一元素。
export type ScrollBox = { label: string; scrollWidth: number; clientWidth: number }

/** 判定容差：亚像素描边/取整不判越界；容器宽差 2px 内不判溢出（与 pixel_uat 同档）。 */
export const TOL_PX = 2

/**
 * overflowProblems 把「视口内 + 滚动祖先不越界」写成一次判定。
 * 参数：vw/vh=视口宽高；boxes=必须四边夹进视口的件（label 用于红字点名）；
 *       scrollAncestors=该件的滚动祖先逐层读数。
 * 返回：问题清单（空＝通过）。反向用例喂越界读数必须非空，否则本函数是空转锁。
 */
export function overflowProblems(
  vw: number,
  vh: number,
  boxes: Array<{ label: string; rect: Rect }>,
  scrollAncestors: ScrollBox[],
): string[] {
  const out: string[] = []
  for (const b of boxes) {
    const r = b.rect
    if (r.left < -TOL_PX) out.push(`${b.label} 左边界越出视口 left=${Math.round(r.left)}`)
    if (r.right > vw + TOL_PX) out.push(`${b.label} 右边界越出视口 right=${Math.round(r.right)} > ${vw}`)
    if (r.top < -TOL_PX) out.push(`${b.label} 上边界越出视口 top=${Math.round(r.top)}`)
    if (r.bottom > vh + TOL_PX) out.push(`${b.label} 下边界越出视口 bottom=${Math.round(r.bottom)} > ${vh}`)
  }
  for (const s of scrollAncestors) {
    if (s.scrollWidth > s.clientWidth + TOL_PX) {
      out.push(`${s.label} 被撑宽 ${s.scrollWidth} > ${s.clientWidth}`)
    }
  }
  return out
}
