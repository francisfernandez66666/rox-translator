// ============================================================================
// components/WordSwap.tsx — ★D1 「删除错误词 → 更新正词」共享换词动效（#24）
// 视觉语言移植自落地页 HeroDemo 的 compare→benchmark 段（红线划掉机翻错词 →
// 白字辉光打出行业正译），现抽取为独立可复用组件：动效节拍、DOM 结构与
// .ws-* 样式（theme.css）只此一份。
// ★ 2026-09-19 用户追加需求：加载动效「多语言循环播放」——缺省词对从 3 组英文
//   术语改为覆盖全站 12 种界面语言（中/英/俄/法/阿/西/葡/德/日/韩/泰/繁中）的
//   「同一概念 × 各语言 错词→正词」轮播，每拍前置语种标签（.ws-tag），
//   阿语拍自动切 dir=rtl。加载位循环播放即是产品多语言能力的活演示。
// 当前接入点：App 冷启/路由懒加载、聊天草稿流（.draft-*）、文件逐段上屏（.file-segs）。
// 演出方式与 HeroDemo 同源：不走 React state，逐字直写 textContent + 类名切换，
// 代际计数器 gen 防卸载/重挂后旧异步链继续写字；prefers-reduced-motion 下
// 直接落终态（错词保留红线 + 正词全显），不做任何逐帧演出。
// ============================================================================
import { memo, useEffect, useRef } from 'react'

export interface WordSwapPair {
  tag: string // 语种标签（大写拉丁码或简中/繁中字面），循环到该拍时前置显示
  bad: string // 机翻错词：逐字打出后被红线划掉
  good: string // 行业正译：错词划掉后带辉光逐字打出并定版
  rtl?: boolean // 阿拉伯语等从右到左书写语种：该拍文字节点切 dir=rtl
}

// 缺省词对（★ 多语言循环）：概念统一取落地页埋过的「竞品对标 benchmark」，
// 每种界面语言一对——错词=按字面直译的机翻腔，正译=该语言行业里真正叫法。
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
// ★ 多语言轮播拍数 ×12，定版停留从 1400 收到 900，整圈节奏不拖沓
const SP = { type: 42, holdBad: 220, strike: 360, holdGap: 160, typeGood: 46, lock: 460, hold: 900, clear: 260 }

/** WordSwap · 职责说明：循环演出「语种标签→错词打字→红线划掉→正词辉光定版→擦除重来」
 *  的多语言加载动效；size=lg 用于全屏加载位，默认行内小档；
 *  纯装饰节点，无障碍口径由外层 aria-label 承担 */
function WordSwap({ pairs = DEFAULT_PAIRS, className = '', ariaLabel }: {
  pairs?: WordSwapPair[]
  className?: string
  ariaLabel?: string
}) {
  const rootRef = useRef<HTMLSpanElement>(null)

  useEffect(() => {
    const root = rootRef.current
    if (!root || !pairs.length) return
    const tagEl = root.querySelector<HTMLElement>('[data-ws="tag"]')!
    const wEl = root.querySelector<HTMLElement>('[data-ws="w"]')!
    const wtEl = root.querySelector<HTMLElement>('[data-ws="wt"]')!
    const rEl = root.querySelector<HTMLElement>('[data-ws="r"]')!

    // 减弱动效：不循环、不打字，直接落「错词带红线 + 正词全显」终态（与 HeroDemo 兜底同口径）
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      const p = pairs[0]
      tagEl.textContent = p.tag
      wtEl.textContent = p.bad
      rEl.textContent = p.good
      wEl.classList.add('struck')
      return
    }

    let gen = 0 // 代际计数器：effect 重跑/卸载即 ++gen，各 await 后比对失败立刻弃演
    const sleep = (ms: number) => new Promise<void>((res) => window.setTimeout(res, ms))
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
      const g = ++gen
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
          // 擦除回到空白，进入下一拍
          rEl.classList.remove('glow', 'lock')
          rEl.textContent = ''
          wEl.classList.remove('struck')
          wtEl.textContent = ''
          await sleep(SP.clear)
        }
      }
    }
    play()
    return () => { gen++ }
  }, [pairs])

  return (
    <span ref={rootRef} className={`ws${className ? ' ' + className : ''}`} role="img" aria-label={ariaLabel ?? '多语言术语纠正演示'}>
      <span className="ws-tag" data-ws="tag" />
      <span className="ws-w" data-ws="w"><span className="ws-wt" data-ws="wt" /></span>
      <span className="ws-r" data-ws="r" />
    </span>
  )
}

export default memo(WordSwap)
