// ============================================================================
// components/WordSwap.tsx — ★D1 「删除错误词 → 更新正词」共享换词动效（#24）
// 视觉语言移植自落地页 HeroDemo 的 compare→benchmark 段（红线划掉机翻错词 →
// 白字辉光打出行业正译），现抽取为独立可复用组件：动效节拍、DOM 结构与
// .ws-* 样式（theme.css）只此一份。
// ★ 2026-09-19 用户追加需求：加载动效「多语言循环播放」——缺省词对从 3 组英文
//   术语改为覆盖全站 12 种界面语言（中/英/俄/法/阿/西/葡/德/日/韩/泰/繁中）的
//   「同一概念 × 各语言 错词→正词」轮播，每拍前置语种标签（.ws-tag），
//   阿语拍自动切 dir=rtl。加载位循环播放即是产品多语言能力的活演示。
// ★ 2026-09-22 用户追加需求：① 加载动效要「久一点、至少读完三个语种再载入」——
//   单拍定版停留从 900 回到 1150（一拍含打字/划线约 2.7~3.1s），并新增 useWordSwapGate 载入闸门：
//   加载态撤销后仍占位到本组件演满 MIN_READ_BEATS(=3) 拍才放行，顺带给首屏前后端
//   调用留出时间。演出本身仍不走 React state，闸门只消费 onBeat 回传的累计拍数。
// 当前接入点：App 冷启（会话恢复/后端探活）、路由懒加载、聊天草稿流（.draft-*）、
// 文件逐段上屏（.file-segs）、工单「执行进度」弹窗（.tk-prog-ws）。
// 演出方式与 HeroDemo 同源：不走 React state，逐字直写 textContent + 类名切换，
// 代际计数器 gen 防卸载/重挂后旧异步链继续写字；prefers-reduced-motion 下
// 直接落终态（错词保留红线 + 正词全显），不做任何逐帧演出。
// 为什么不改成 React state 驱动：一拍要改几十次字符（type 逐字 append），走 setState 等于
//   每 42ms 让外层（App 冷启 / 草稿流 / 工单进度弹窗）重渲染一次，而动画节拍必须精确到 ms，
//   React 的调度与批处理给不了这个确定性；命令式写 DOM 后演出与父组件渲染完全解耦。
// 尺寸档不吃 props：本组件只负责挂类名（ws / ws--lg / tk-prog-ws / draft-ws / file-segs-ws），
//   字号与间距全在 styles/theme.css 的 .ws-* 规则里，同一份演出可由各接入位自行缩放。
// ============================================================================
import { memo, useCallback, useEffect, useRef, useState } from 'react'

/** WordSwapPair 一拍换词的数据：错词 bad 逐字打出→划红线，正词 good 再打出并定版。
 *  tag 是这一拍的语种标签，rtl 仅阿语拍需要（决定文字方向样式）。 */
export interface WordSwapPair {
  tag: string // 语种标签（大写拉丁码或简中/繁中字面），循环到该拍时前置显示
  bad: string // 机翻错词：逐字打出后被红线划掉
  good: string // 行业正译：错词划掉后带辉光逐字打出并定版
  rtl?: boolean // 阿拉伯语等从右到左书写语种：该拍文字节点切 dir=rtl
}

// 缺省词对（★ 多语言循环）：概念统一取落地页埋过的「竞品对标 benchmark」，
// 每种界面语言一对——错词=按字面直译的机翻腔，正译=该语言行业里真正叫法。
// 顺序即播放顺序（EN 起手、简中收尾），轮播一遍 = 12 拍；数组长度也决定闸门补拍的轮次厚度。
// 提到模块级常量而不是写在组件默认值里：默认 prop 每次渲染都要引用同一份数组，
// 若在 JSX 里现造字面量，effect deps [pairs] 每帧都变、循环会一直从第一拍重启。
export const DEFAULT_PAIRS: WordSwapPair[] = [
  { tag: 'EN', bad: 'compare', good: 'benchmark' },
  { tag: 'RU', bad: 'сравнение', good: 'бенчмарк' },
  { tag: 'JA', bad: '比較', good: 'ベンチマーク' },
  { tag: 'KO', bad: '비교', good: '벤치마크' },
  { tag: 'DE', bad: 'Vergleich', good: 'Benchmark' },
  { tag: 'FR', bad: 'comparaison', good: 'étalon' },
  { tag: 'ES', bad: 'comparación', good: 'referencia' },
  { tag: 'PT', bad: 'comparação', good: 'referência' },
  { tag: 'AR', bad: 'مقارنة', good: 'معيار', rtl: true },
  { tag: 'TH', bad: 'เปรียบเทียบ', good: 'มาตรฐาน' },
  { tag: '繁中', bad: '對比', good: '基準' },
  { tag: '简中', bad: '对比', good: '对标' },
]

// 节拍（ms）：沿用 HeroDemo 实测节奏，加载态只保留「打字/停留/划掉/定版」四拍；
// ★ 多语言轮播拍数 ×12，2026-09-20 曾把定版停留压到 900 求「不拖沓」，结果一拍读不完
//   就切语种（用户 2026-09-22 反馈：太赶、读不出正词）——回到 1150，一拍约 3.3s 够读完。
const SP = { type: 42, holdBad: 220, strike: 360, holdGap: 160, typeGood: 46, lock: 460, hold: 1150, clear: 260 }

// ★ 载入闸门口径：一次「真加载」至少要让用户读完 3 个语种再进系统
//   （3 拍 ≈ 9s，与后端冷启动/首屏接口往返的时间窗天然吻合）。
export const MIN_READ_BEATS = 3

/** prefersReducedMotion · 全站「减弱动效」判定唯一口径（WordSwap 演出与载入闸门共用） */
export function prefersReducedMotion(): boolean {
  return !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

/** useWordSwapGate · 载入闸门：把「后端/会话还在加载」翻译成「占位还要不要留」
 *  - loading 期间正常占位；loading 撤销后，若本组件还没演满 minBeats 拍则继续占位；
 *  - loading 从未出现过（如未登录直出登录页）则不额外停留，避免凭空卡首屏；
 *  - 减弱动效下不做补拍（系统偏好即「别给我看动画」），只跟随 loading 本身。
 *  返回 busy 供占位渲染判断、onBeat 传给 WordSwap 回传已演完的累计拍数。 */
export function useWordSwapGate(loading: boolean, minBeats = MIN_READ_BEATS) {
  const [played, setPlayed] = useState(0) // WordSwap 回传的「已完整演完」累计拍数
  const [need, setNeed] = useState(0) // 需要补足的目标拍数；0 = loading 从未出现过，闸门不额外拦人
  useEffect(() => {
    // 只在「确实发生过加载」的那次挂载上补足拍数；重复触发取最大值，不回退
    // （loading 反复抖动时 need 仍锁在 minBeats，不会累加成 6 拍、9 拍）
    if (loading && !prefersReducedMotion()) setNeed((n) => Math.max(n, minBeats))
  }, [loading, minBeats])
  // done 取「覆盖」而非「累加」：WordSwap 回传的是它自己那次挂载的累计拍数，
  // 占位组件重挂（新实例又从第 1 拍开始数）时覆盖语义才不会把两代的拍数加在一起；
  // need 不回退，于是重挂只会让闸门重新关上、等新一代理满，不会提前放行。
  const onBeat = useCallback((done: number) => setPlayed(done), [])
  // busy 的两个来源互不干扰：loading 期间一定占位；loading 撤销后改由拍数决定何时收场
  return { busy: loading || played < need, onBeat }
}

/** WordSwap · 职责说明：循环演出「语种标签→错词打字→红线划掉→正词辉光定版→擦除重来」
 *  的多语言加载动效；size=lg 用于全屏加载位，默认行内小档；
 *  onBeat 每演完一拍（正词定版停留结束、擦除之前）回传累计拍数，供载入闸门判定；
 *  纯装饰节点，无障碍口径由外层 aria-label 承担 */
function WordSwap({ pairs = DEFAULT_PAIRS, className = '', ariaLabel, onBeat }: {
  pairs?: WordSwapPair[]
  className?: string
  ariaLabel?: string
  onBeat?: (done: number) => void
}) {
  const rootRef = useRef<HTMLSpanElement>(null)
  // 回调走 ref：父组件重渲染换函数引用时不重启动画循环（effect deps 仍只看 pairs）
  const beatRef = useRef(onBeat)
  beatRef.current = onBeat

  useEffect(() => {
    const root = rootRef.current
    if (!root || !pairs.length) return // 词表为空直接不演（宁可不演也不要只剩标签在闪）
    // 四个节点用 data-ws 选择器取（与下方 return 的 DOM 是结构契约，缺一个整段就不演）：
    // 不建四个 ref 是因为演出只碰 textContent / classList / dir 三件事，全程不进 React，
    // 用 ref 反而要为纯命令式消费引入 ref 样板；! 断言的前提就是 JSX 与选择器同步演进。
    const tagEl = root.querySelector<HTMLElement>('[data-ws="tag"]')!
    const wEl = root.querySelector<HTMLElement>('[data-ws="w"]')!
    const wtEl = root.querySelector<HTMLElement>('[data-ws="wt"]')!
    const rEl = root.querySelector<HTMLElement>('[data-ws="r"]')!

    // 减弱动效：不循环、不打字，直接落「错词带红线 + 正词全显」终态（与 HeroDemo 兜底同口径）
    if (prefersReducedMotion()) {
      const p = pairs[0]
      tagEl.textContent = p.tag
      wtEl.textContent = p.bad
      rEl.textContent = p.good
      wEl.classList.add('struck')
      return
    }

    let gen = 0 // 代际计数器：effect 重跑/卸载即 ++gen，各 await 后比对失败立刻弃演
    //   （sleep 无法被 clearTimeout，只能靠「醒来后发现自己已过气」退出，这是本组件唯一的终止手段）
    let played = 0 // 本次挂载已完整演完（含定版停留）的拍数，供载入闸门判定
    // ★ 2026-09-24（#7 批）：window.setTimeout → 裸 setTimeout——vitest 拆 jsdom 环境后
    //   `window` 标识符直接 ReferenceError，演出链若恰好在销毁后才醒来排下一拍，
    //   会炸出一个「Unhandled Rejection」把整轮 npm test 顶红（浏览器里两者本就是同一函数，行为无差）。
    const sleep = (ms: number) => new Promise<void>((res) => setTimeout(res, ms))
    // 逐字打字：每字在 speed 之上再加 0~50% 随机延时（等速间隔一眼机械感），
    // 每写一个字先验一次 g===gen，卸载后不会有半个字符落到 DOM 上
    const type = async (node: HTMLElement, text: string, speed: number, g: number) => {
      node.textContent = ''
      node.classList.add('typing')
      for (let i = 0; i < text.length; i++) {
        if (g !== gen) return
        node.textContent += text.charAt(i)
        await sleep(speed + Math.random() * speed * 0.5)
      }
      node.classList.remove('typing')
    }

    const play = async () => {
      const g = ++gen // 本次演出的身份证号：后续所有分支只认这个 g
      // while 条件是唯一的循环出口——gen 变化后这一层自然不再进下一轮，
      // 不必也不能 clearTimeout：链上任意一次 await 醒来都会先比对 gen 再落 DOM。
      // 卸载场景下旧链握着的是已脱离文档的节点引用，晚一步的写入不可见；
      // 每拍开头那次标签赋值前面没有 gen 检查，所以旧链最多多写「一个标签」就必然撞上下一个检查点。
      while (g === gen) {
        for (const p of pairs) {
          // 语种标签先落位再打词；RTL 语种（阿）本拍文字节点切 dir=rtl，下一拍复位
          tagEl.textContent = p.tag
          wtEl.dir = p.rtl ? 'rtl' : 'ltr'
          rEl.dir = p.rtl ? 'rtl' : 'ltr'
          // 错词：打出 → 停留 → 红线划掉（struck 挂 w 位，红线是 wt 的 ::before）
          await type(wtEl, p.bad, SP.type, g)
          if (g !== gen) return
          await sleep(SP.holdBad)
          wEl.classList.add('struck')
          await sleep(SP.strike)
          if (g !== gen) return
          // 正词：先挂 glow 辉光再打字，打完 lock 定版缩放
          rEl.classList.add('glow')
          await type(rEl, p.good, SP.typeGood, g)
          if (g !== gen) return
          rEl.classList.add('lock')
          await sleep(SP.hold)
          if (g !== gen) return
          // 正词已完整展示并停留到位＝这一拍「读得完」，此时才向闸门计数
          beatRef.current?.(++played)
          // 擦除回到空白，进入下一拍
          rEl.classList.remove('glow', 'lock')
          rEl.textContent = ''
          wEl.classList.remove('struck')
          wtEl.textContent = ''
          await sleep(SP.clear)
        }
      }
    }
    play().catch(() => { /* 环境销毁/节点失联等残余异步噪声静默吞掉：演出是纯装饰，不值得冒未处理拒绝红灯 */ })
    return () => { gen++ } // 不做 DOM 清理：节点随组件卸载一起消失，演出链靠 gen 自灭
  }, [pairs])

  return (
    // role="img" + 单一 aria-label：三个子节点的文字是被逐字改写的，若留在可访问性树里，
    // 读屏会念出「b e n c h」这类半截词；声明成一枚图片后整棵子树对 AT 不可见，只报外层说明。
    // 缺省 label 是中文描述（与全站「装饰节点由外层 aria-label 承担」口径一致），外层按语境覆盖。
    <span ref={rootRef} className={`ws${className ? ' ' + className : ''}`} role="img" aria-label={ariaLabel ?? '多语言术语纠正演示'}>
      <span className="ws-tag" data-ws="tag" />
      <span className="ws-w" data-ws="w"><span className="ws-wt" data-ws="wt" /></span>
      <span className="ws-r" data-ws="r" />
    </span>
  )
}

// memo 导出：动效存续的几秒里外层往往在高频重渲染（App 冷启的闸门 state、工单 3s 轮询、
// 草稿流打字），props 全是稳定引用时直接跳过 render——反正演出在 effect 里，重渲染不推进它。
export default memo(WordSwap)
