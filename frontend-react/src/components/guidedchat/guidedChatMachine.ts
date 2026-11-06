// ============================================================================
// components/guidedchat/guidedChatMachine.ts — 对话式引导的**唯一**状态机（H1，2026-11-06）
//
// 为什么要把这套东西从 AiRegisterFlow.tsx 里抽出来（用户当场拍的口径）：
//   仓里原本**只有一处**"AI 引导填写"＝注册面那台对话卡（气泡＋打字机＋选项 chips＋进度＋上一步），
//   而引导建产品（/onboarding）我另起了一页朴素表单 ⇒ 同一个产品里长出两种引导形态，
//   用户看到的"引导"到底该怎么走，取决于他先碰到哪一面。这不是审美问题，是**同一动作两套语义**。
//   修法只有"把外壳收成一份，两个场景各接自己的内容"，**禁止**在新场景里再造一套打字机/步进栈
//   （第二把尺子迟早和第一把分叉，AGENTS §一·5 那一族）。
//
// 三条硬口径（改这个文件前必读）：
//  ① **步进栈的语义是"重放而不是缓存 DOM"**：`goBack` 截断到上一步起点再调用它的 render，
//     所以回退后的那一屏与首次进入逐字一致（含打字动效）。把 render 换成"保存旧 JSX"会丢掉动效，
//     并且会让"上一步之后再往前"拿到一份过期快照。
//  ② **代际（gen）是这条链唯一的作废凭据**：任何异步腿（打字、上游回执、定时器）都必须用
//     调用点传下去的 `my` 判 `alive(my)`，`goBack`/卸载 bump 一次整条链一起停。
//     点击类回调必须**现取** `genRef.current`，不许用渲染时闭包里的快照——
//     中途回退过一次的旧闭包会把新点击误判成过期（这条在注册面真踩过，注释留在 AiRegisterFlow 里）。
//  ③ **total 可以为 null**（分支问完才知道总步数）：进度按"已答步数＋预估 2 步"给，
//     不许停在 0 看着像没动；预估常数只有 `PENDING_STEPS` 这一处。
//
// 本文件**不含 JSX、不含 fetch、不含任何场景话术**——场景内容由各调用方自己投喂。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { chatTypingMs, typingUnitOf } from '@/i18n/script'
import { getLang } from '@/i18n'

/** 动效节拍之间的统一间隔单位：引导链所有「等一下」都走它，避免散落 setTimeout(数字) 难调 */
export const sleep = (ms: number) => new Promise<void>((res) => setTimeout(res, ms))

/** 分支未定时进度条的"还差几步"预估（口径③，唯一一份） */
const PENDING_STEPS = 2

/** 一条引导消息的最小公共形状；场景各自挂自己的字段（选项/表单/摘要卡…） */
export interface GuidedMsgBase {
  id: number
  side: 'ai' | 'user'
  /** 打字机目标全文（同时作为 ghost 占位撑高，避免逐字打字时行高跳动） */
  full?: string
  /** 已打出的文本 */
  text?: string
  typing?: boolean
}

/** 步进栈的一项：回退时按 startId 截断消息流，再重放 render */
interface StackEntry<M> {
  idx: number
  total: number | null
  name: string
  startId: number
  render: (my: number) => void
  /** 本步开始时消息流的起点（截断用；render 追加的新消息从此 id 之后） */
  _m?: M
}

/** 状态机对外契约：场景拿到的是这一份，不许各自再摸 msgs/gen/栈 */
export interface GuidedChatApi<M extends GuidedMsgBase> {
  msgs: M[]
  /** 代际 ref（口径②）：点击类回调必须**现取** `genRef.current`，而不是渲染时闭包里的快照 */
  genRef: React.MutableRefObject<number>
  /** 消息自增 id（同时也是「本步起点」的界标）；场景要按它定点更新某条消息 */
  idRef: React.MutableRefObject<number>
  /** 追加一条消息并返回它的 id（后续打字/点亮都要按 id 定点更新这一条） */
  addMsg: (m: Omit<M, 'id'>) => number
  /** 定点改写某条消息（打字机与"点亮第几格"都靠它） */
  patchMsg: (id: number, patch: Partial<M>) => void
  /** 打字机：按界面语种选打字单元与速度；ghost 撑高，窄屏不跳高。返回 false＝这一代已作废 */
  typeText: (id: number, text: string, my: number) => Promise<boolean>
  /** 进入某一步：压栈 → 截断到本步起点 → 更新进度与步骤名 → 渲染 */
  stepRender: (idx: number, total: number | null, name: string, render: (my: number) => void) => void
  /** 回上一步（首步与收尾后不可回） */
  goBack: () => void
  /** 本异步链是否仍属当前代际 */
  alive: (my: number) => boolean
  /** 现取当前代际（点击类回调必须用它，而不是渲染时快照） */
  currentGen: () => number
  /** 开新代际：把还在跑的异步链一次性作废（挂载/重置时调） */
  bumpGen: () => number
  /** 清空消息流与步骤栈（面板每次挂上都是从第一步重来） */
  resetFlow: () => void
  stepLabel: string
  trackPct: number
  /** 收尾那一步由场景自己把进度钉满并写最后一条步骤名（注册面 finish 那一支在用） */
  setStepLabel: (v: string) => void
  setTrackPct: (v: number) => void
  backHidden: boolean
  /** 收尾后禁止回退（调用方在 finish 时置 true） */
  setFinished: (v: boolean) => void
  /** 首步（栈深 ≤1）没有可回的地方 */
  updateBack: () => void
  /** 屏幕是否偏好减少动效：true＝所有文本直出终态、节拍照走但不再逐字 */
  reduced: boolean
  /** 聊天容器 ref（追加消息后必须手动贴底，见 addMsg 注释） */
  chatRef: React.MutableRefObject<HTMLDivElement | null>
  scrollBottom: () => void
}

/**
 * 对话式引导状态机。
 * 参数：getTplText 取词函数（场景各自注入，本模块不碰词典键）；
 * 返回：见 GuidedChatApi。泛型 M 让场景挂自己的消息字段而不丢类型。
 */
export function useGuidedChat<M extends GuidedMsgBase>(
  labelOf: (idx: number, total: number | null, name: string) => string,
): GuidedChatApi<M> {
  const [msgs, setMsgs] = useState<M[]>([])
  const [stepLabel, setStepLabel] = useState('')
  const [trackPct, setTrackPct] = useState(0)
  const [backHidden, setBackHidden] = useState(true)
  const [reduced, setReduced] = useState(false)
  const idRef = useRef(0)
  const genRef = useRef(0)
  const finishedRef = useRef(false)
  const chatRef = useRef<HTMLDivElement | null>(null)
  const stepStack = useRef<StackEntry<M>[]>([])

  // prefers-reduced-motion 必须带守卫：jsdom / 老旧 WebView 上 matchMedia 不存在，裸调会在 effect 里直接抛
  useEffect(() => {
    if (typeof window === 'undefined' || !window.matchMedia) return
    const m = window.matchMedia('(prefers-reduced-motion: reduce)')
    const f = () => setReduced(m.matches)
    f()
    m.addEventListener?.('change', f)
    return () => m.removeEventListener?.('change', f)
  }, [])

  // 追加消息后必须手动贴底：容器是 overflow-y:auto 的定高卡，浏览器不会替聊天流滚到底
  const scrollBottom = useCallback(() => {
    requestAnimationFrame(() => { if (chatRef.current) chatRef.current.scrollTop = chatRef.current.scrollHeight })
  }, [])

  const addMsg = useCallback((m: Omit<M, 'id'>): number => {
    const id = ++idRef.current
    setMsgs((s) => [...s, { ...m, id } as M])
    scrollBottom()
    return id
  }, [scrollBottom])

  const patchMsg = useCallback((id: number, patch: Partial<M>) => {
    setMsgs((s) => s.map((m) => (m.id === id ? ({ ...m, ...patch } as M) : m)))
  }, [])

  const alive = useCallback((my: number) => my === genRef.current, [])
  const currentGen = useCallback(() => genRef.current, [])
  const bumpGen = useCallback(() => { genRef.current++; return genRef.current }, [])

  const typeText = useCallback(async (id: number, text: string, my: number) => {
    if (reduced) {
      // 减少动效：直接写满文本并返回 true，保持调用方 `.then(ok => 下一步)` 的推进语义不变
      setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text, typing: false } as M : m)))
      return true
    }
    // 〇-Q：打字单元（CJK 逐字 / 拉丁逐词）与速度按界面语种走，动效随语种变
    const lang = getLang()
    const unit = typingUnitOf(lang)
    const step = chatTypingMs(lang)
    if (unit === 'word') {
      // 拉丁/西里尔/阿语：逐词推进——逐字母打会像乱码闪烁
      const toks = text.split(/(\s+)/) // 保留空白 token，重建后文本与原文逐字符一致
      let acc = ''
      for (const tk of toks) {
        if (my !== genRef.current) return false
        acc += tk
        setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text: acc, typing: true } as M : m)))
        scrollBottom()
        await sleep(step)
      }
    } else {
      for (let i = 0; i < text.length; i++) {
        if (my !== genRef.current) return false
        const slice = text.slice(0, i + 1)
        setMsgs((s) => s.map((m) => (m.id === id ? { ...m, text: slice, typing: true } as M : m)))
        scrollBottom()
        await sleep(step)
      }
    }
    if (my !== genRef.current) return false
    setMsgs((s) => s.map((m) => (m.id === id ? { ...m, typing: false } as M : m)))
    return true
  }, [reduced, scrollBottom])

  const updateBack = useCallback(() => {
    setBackHidden(finishedRef.current || stepStack.current.length <= 1)
  }, [])

  const stepRender = useCallback((idx: number, total: number | null, name: string, render: (my: number) => void) => {
    const startId = idRef.current + 1 // 本步第一条消息的 id：goBack 时按它截断
    stepStack.current.push({ idx, total, name, startId, render })
    setMsgs((s) => s.filter((m) => m.id < startId))
    setStepLabel(labelOf(idx, total, name))
    // 口径③：total 未知时按「已答步数＋预估」给进度，别让它停在 0 看着像没动
    setTrackPct(total ? (idx / total) * 100 : (idx / (idx + PENDING_STEPS)) * 100)
    render(genRef.current)
    updateBack()
  }, [labelOf, updateBack])

  const goBack = useCallback(() => {
    if (finishedRef.current || stepStack.current.length < 2) return
    const my = ++genRef.current // 中断进行中的打字/回调
    stepStack.current.pop() // 当前步作废
    const prev = stepStack.current[stepStack.current.length - 1]
    // 截断到上一步起点之前，再**重放上一步的 render**（不是缓存旧 DOM，见文件头口径①）
    setMsgs((s) => s.filter((m) => m.id < prev.startId))
    if (prev.render) prev.render(my)
    updateBack()
  }, [updateBack])

  const resetFlow = useCallback(() => {
    genRef.current++
    setMsgs([])
    stepStack.current = []
    finishedRef.current = false
  }, [])

  const setFinished = useCallback((v: boolean) => {
    finishedRef.current = v
    setBackHidden(v || stepStack.current.length <= 1)
  }, [])

  return {
    msgs, genRef, idRef, addMsg, patchMsg, typeText, stepRender, goBack, alive, currentGen, bumpGen, resetFlow,
    stepLabel, trackPct, setStepLabel, setTrackPct, backHidden, setFinished, updateBack, reduced, chatRef, scrollBottom,
  }
}
