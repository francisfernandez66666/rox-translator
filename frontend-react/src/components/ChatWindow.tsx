// ============================================================================
// components/ChatWindow.tsx — 前台即时翻译工作台（★ 形态定档：整屏合并 AI 对话框，2026-09-22 〇-LJ/〇-LK）
// 结构：离线横幅 + 余额/用量条 + **一张占满除页眉页脚外整屏的对话框**；
//       框内三段自上而下 = 会话工具条 / 消息流（原文与译文气泡，内部滚动）/ **输入区常驻底部**。
// ★ 形态沿革（避免再被改错方向）：
//   #36（09-21，106ce50）把「吸顶输入卡 + 下方气泡」合并成整屏单框；
//   〇-LJ 曾按「回退形态」把它改回两段式，用户判**方向错**（要的是单框），同日改回；
//   但单框内我把 textarea 摆在了滚动区最上面，用户再看判**仍错**——
//   「你们家对话框是放顶部的啊」：AI 对话页的输入永远在**底部**，历史在它上方长。
//   〇-LK 因此定稿：**输入区（原文标签 + textarea + 目标语言 + 模式 + 主按钮）整体贴底**，
//   框头只留会话级工具（搜索/导出/清空），标题用 app.tabWorkbench。
//   ★ 〇-M（2026-09-23）按用户令再压一档：「输入框和选择器这一块太长了、太占空间，要类似元宝这种」
//   ⇒ 框脚四排（原文标签+输入框 / 整宽语种下拉 / chips 独立行 / 操作行）改为
//   **一张内凹输入卡 = 一行自适应输入 + 一行工具条**（源语言胶囊 · 目标语言胶囊 · 已选 chips
//   （超 3 折成「+n」，点开全展开）· 模式 · 缩翻 · 主按钮），1280×720 实测框脚 109px、输入卡 90px。
//   形态锁三处（本文件 .cw-dialog* + ChatWindow.dom.test.tsx ①⑤ + pixel_uat.spec.ts P2b）
//   已按「单框 + 输入在底 + 输入区单卡单工具条」钉死；再改形态必须先复述目标形态向用户确认，不要凭措辞反推。
// ★ 2026-10-13 用户令①（「对话框没占满上下区域（除页眉页脚）」）：
//   2026-10-10 决策⑩ 把默认档从 flex:1 吃满改成固定 min(70vh,640px)，实测就是这条投诉的成因——
//   高屏上对话框只到 640px，下面留一大片黑。现把默认档改回**吃满页眉与页脚之间**（见 .cw-dialog 的
//   flex:1 1 0），决策⑩ 的**能力**一项未减：顶缘手柄照旧可拖（拖过即进 .cw-resized 覆盖档）、
//   双击回默认、高度记 localStorage。真值见 UI-ANNOTATIONS §6「工作台对话框默认高度档」。
// ★ #36 同时移除即时翻译的文件翻译入口（上传按钮/隐藏 file input/校验/发送）：
//       文件翻译统一走「文档翻译」工单页（TicketsPage → /api/tickets/create-file），
//       即时翻译只做文本，避免同一份文件两条口径不一致的链路。
// 业务逻辑/API 全部保留，仅重写呈现层，文案走词典（无硬编码中文）。
// 呈现层已迁至 @/ui/langcross/src 纯黑组件库（TDesign 全部移除）。
// ============================================================================
import { useCallback, useContext, useEffect, useRef, useState } from 'react'
import {
  Button, Input, useToast,
  SearchIcon, DownloadIcon, TrashIcon, CloseIcon, UploadIcon,
} from '@/ui/langcross/src'
import MessageBubble from './MessageBubble'
import { FeedbackModalFromMessage } from './modals'
import { useChat, PkgRefreshCtx } from '@/hooks/useChat'
import { isPlatformBillingContext, useAuthStore } from '@/stores/auth' // ★ O-9（批 I-10）：平台上下文不渲染余额条
import { myPackage, meContext } from '@/api'
import { estimateTranslation } from '@/api/translate'
import { fmtPoints } from '@/utils/points'
import type { ChatMessage } from '@/types'
import { useT, t, tpl } from '@/i18n'
import LangMultiSelect, { LangChips } from '@/components/LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'
import { intlLocale, fmtDateTime } from '../lib/format'
// ★ F-52②（批 I-8）：「文本过长」本地闸（上限由本页 loadBalance 从 /api/me/package 喂养）
import { setChatMaxChars } from '../lib/chatLimit'
// ★ P3 语音两件套（麦克风录音 / 译文朗读）：录放链路在 api/chatVoice.ts；
//   识别引擎（服务端 / 端侧 WASM）的可插拔接缝在 api/asrEngines.ts（P4）。
import { micSupported } from '@/api/chatVoice'
// ★ 2026-10-16 能力门批：麦克风这条链的上游是服务端 ASR（部署机没装 sherpa/ffmpeg 时 asr_ready=false，
//   识别接口恒回 capability_missing）。与「浏览器不支持就不渲染」同一口径，这里再加一道**服务端能力门**。
import { useVoiceCapability } from '@/api/voiceCapability'
import { getAsrEngine, ServerAsrEngine, type AsrSession } from '@/api/asrEngines'
// ★ 2026-10-17 用户令（推翻 #36「即时翻译只做文本」旧决策）：即时翻译支持图片上传——
//   上传图片 → OCR 提取文字 → 走同一套翻译 → 回传**译后图（原位回填）＋ 各目标语译文文本**。
//   客户端见 api/chatImage.ts，端点 POST /api/chat/image（后端 api/chat_image.go）。
import { chatImageTranslate, ImageError, type ChatImageResult } from '@/api/chatImage'
// ★ 决策⑪①（2026-10-10）：发起前三档预检（字符数 × 模式 × 排队深度 → 直接译/明示较慢/直接拒）
import { slowVerdict } from '../lib/slowEstimate'

// 数字千分位格式化，并处理 undefined/负数，用于余额与用量展示
// floor + Math.max(0,…) 是必要的：本函数喂的是「≈句数」这类折算值（balance_sentences_approx、
// estimate 的 s），后端回传小数或早期脏数据为负时，直接格式化会显示成「-1,234」这种吓人数字。
function fmtNum(n: number): string {
  return new Intl.NumberFormat(intlLocale()).format(Math.max(0, Math.floor(n || 0)))
}

// 卡片样式（纯黑体系：面板 #0E1014 + 2px 纯白描边 + 圆角 14）
// ★ #68：描边走 --lc-border-card 令牌（旧字面 #464C58 在纯黑上不足 3:1，看不见边）。
// 卡片规格集中成常量而不是散进 JSX 内联：内联字面值正是 #68 闸门要收口的形态，
// 走令牌后描边档位由 theme.css §十 统一调，页面不需要跟着改。
const CARD: React.CSSProperties = {
  background: 'var(--lc-panel)', border: '1.2px solid var(--lc-border-card)', borderRadius: 14,
}

// ★ 决策⑩（2026-10-10）：对话框高度记忆键（带 lc- 前缀的站点约定）。存的是像素值。
// ★ 2026-10-13 用户令①（「对话框没占满上下区域（除页眉页脚）」）：默认档从固定 min(70vh,640px)
//   改回**吃满 .cw-root 的剩余高度**（页眉与页脚之间的那一段全给对话框），
//   于是 0/缺键＝默认档（CSS 的 flex:1 1 0 承担，JS 不写内联高度），
//   >0＝用户拖过的**覆盖档**（加 .cw-resized 让内联高度生效，flex 不再拉伸）。
//   范围钳在 320px–90vh（拖拽档与恢复档共用 cwClampDialogH 这一把尺子）。
const CW_DIALOG_H_KEY = 'lc-cw-dialog-h'
// 默认档的 JS 侧**估算**读数：只在实际高度量不到时用（jsdom 里 clientHeight 恒 0、
// 极端情况下手柄先于布局完成被按下）。渲染本身不再依赖这个数字——默认档由 flex 吃满，
// 所以这里估偏了也不会改变界面，只影响「从默认档起拖」的第一下行程。
function cwDefaultDialogH(): number {
  return Math.min(Math.round(window.innerHeight * 0.7), 640)
}
// 高度钳制纯函数：拖拽与会话恢复共用同一条 320px–90vh 档（低于下限＝输入区被压没，
// 高于上限＝拖过头要把页脚顶出屏幕，得自己滚回去才看得见）
function cwClampDialogH(h: number): number {
  return Math.max(320, Math.min(Math.round(window.innerHeight * 0.9), h))
}

// 停止生成图标（langcross 无等价，按组件库线性风格内联方块）
function StopGlyph() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      <rect x="3.5" y="3.5" width="9" height="9" rx="1.6" fill="currentColor" />
    </svg>
  )
}

// 加载中小转圈（离线重试按钮，替代 TDesign Button loading）
function Spinner() {
  return <span className="cw-spin" aria-hidden="true" />
}

// MicGlyph 麦克风线条图标（纯黑体系：**currentColor 描边**，与全站禁 emoji 同口径）。
// active=录音中：描边转语义红 #E5484D——"正在被记录"是必须一眼看见的状态，
function MicGlyph({ active }: { active: boolean }) {
  const c = active ? '#E5484D' : 'currentColor'
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      <rect x="6.2" y="2" width="3.6" height="7.2" rx="1.8" fill="none" stroke={c} strokeWidth="1.2" />
      <path d="M3.6 7.6a4.4 4.4 0 0 0 8.8 0" fill="none" stroke={c} strokeWidth="1.2" strokeLinecap="round" />
      <line x1="8" y1="12" x2="8" y2="14" stroke={c} strokeWidth="1.2" strokeLinecap="round" />
    </svg>
  )
}

// 收起图标（langcross 无等价，按组件库线性风格内联：向下的 V 形箭头，示意「缩下去」）
function CollapseGlyph() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      <path d="M4 6.2 8 10l4-3.8" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

// 默认导出组件：前台即时翻译工作台（输入与结果同框）
export default function ChatWindow() {
  const [lang, t2] = useT()
  // 组件内提示直接取 ToastProvider 的 hook；非组件环境（lib 层、深层回调）走 lib/toastBus
  const { toast } = useToast()
  const chat = useChat()
  const [input, setInput] = useState('')
  const [feedbackMsg, setFeedbackMsg] = useState<ChatMessage | null>(null)

  // ★ 双模式（fast/pro）持久化
  const [mode, setMode] = useState<'fast' | 'pro'>(
    (localStorage.getItem('translate_mode') as 'fast' | 'pro') || 'pro',
  )

  // ★ 缩翻（任务7）：勾选+最长字符限制（0=未启用）
  // ★ #36（2026-09-21）：文件翻译入口下线后，缩翻改为对**文本翻译**生效——
  //   options.max_length 在引擎文本路径同样被消费（engine/text.go maxLengthOption），
  //   因此保留控件并接进 handleSend，而不是连带删掉一个用户已在用的功能。
  const [condenseOn, setCondenseOn] = useState(false)
  const [condenseMax, setCondenseMax] = useState(200)
  // ★ F7：输入预估（防抖 600ms）/ 会话搜索 / 导出
  // ★ 决策⑪①：qd=后端排队深度代理值（llm_queue_depth），参与三档预检
  const [estimate, setEstimate] = useState<{ min: number; max: number; s: number; low: boolean; qd?: number } | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQ, setSearchQ] = useState('')

  // ★ 决策⑩（2026-10-10）：对话框高度（像素，0=默认档「吃满页眉与页脚之间」，>0=拖拽覆盖档，记忆到 localStorage）
  const [dialogH, setDialogH] = useState<number>(() => {
    try { return parseInt(localStorage.getItem(CW_DIALOG_H_KEY) || '0', 10) || 0 } catch { return 0 }
  })
  // 拖拽闭包里要读最新高度，state 异步更新会读到旧值——旁挂 ref 同步
  const dialogHRef = useRef(dialogH)
  function setDialogHeight(h: number) { dialogHRef.current = h; setDialogH(h) }
  // ★ 决策⑩（2026-10-10）：对话框收起闩——**默认展开**，只有用户点框头的「收起」钮才进收起档。
  // ★ 2026-10-15 用户令（覆盖决策⑩的「空态自动收起」那一半）：「聊天框打开后自动最小化了，啥也看不到」
  //   ——空态（无输入、无会话）不再自动收成单行入口条，必须点「收起」钮才收。原形态
  //   `!expandedLatch && input 空 && 无会话` 让任何人一进来就看到一条窄条。收起仍保留（用户可选），
  //   展开＝点入口条。
  const [collapsedLatch, setCollapsedLatch] = useState(false)

  // ★ 余额 / 用量（2026-09-19 全积分口径：API 出参即积分，前端零换算）
  const [balance, setBalance] = useState<{ points: number; approx: number } | null>(null)
  // ★ 2026-10-14 用户令（红框批）：界面不再显示「今日已耗」，这一份 today 状态随之撤下——
  //   留着它＝只有 setter 没有读点的死状态（下一个读代码的人会以为还有第三处消费方）。
  //   后端出参 `points_used_today` 一字未动（管理台/看板/账单/SDK 仍读它），撤的只是这块界面。
  const [orgBudget, setOrgBudget] = useState<{ limit: number; used: number; name: string } | null>(null)

  // scrollRef 现在指向合并框内的滚动区（原文输入 + 结果气泡都在其中）
  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  // ★ 2026-10-13 用户令①：默认档改回 flex 吃满后，「起拖时这一档实际多高」只有浏览器知道，
  //   所以拖拽起点要实测这个节点（量不到才回落到按视口推导的估算档）。
  const dialogRef = useRef<HTMLDivElement>(null)

  // ---- 余额 / 预算加载 ----
  // 从 myPackage 接口读取个人余额与企业预算额度（均为积分）
  // ★ 2026-10-14 红框批：这里原本还读「今日用量」，界面按用户令撤下后不再取它。
  // org_budget 只在 points_limit>0 时才有意义：未开预算的企业回传 0，
  // 若照样 set 就会在余额条里渲染「已用 X / 共 0」这种误导文案，故显式清成 null。
  const loadBalance = useCallback(async () => {
    try {
      const r: any = await myPackage()
      if (r && r.success) {
        // ★ O-9（批 I-10）：平台上下文（超管未切入任何租户）不参与计费，后端对 tid<=0 固定回 0。
        //   余额条与部门预算徽标照此渲染会长期显示「余额 0 积分」——一个不会扣点的身份
        //   天天被告知没钱。守卫只压这两处**展示**，不做提前 return：
        //   chat_max_chars（F-52② 本地闸的唯一数据源）在平台上下文同样要刷。
        //   判据与 App 顶栏同源（isPlatformBillingContext），用 getState() 取角色是为了
        //   不把 user 塞进本回调依赖、引起余额广播链路重建。
        const platformCtx = isPlatformBillingContext(useAuthStore.getState().user?.role)
        if (!platformCtx && typeof r.points_balance === 'number') {
          setBalance({ points: r.points_balance, approx: r.balance_sentences_approx ?? 0 })
        }
        // ★ 2026-10-14 红框批：这里原来还有一句 setUsage(r.points_used_today)——界面按用户令
        //   不再显示「今日已耗」，那个状态就成了只有 setter 没有读点的死状态，一并撤下。
        //   后端出参 `points_used_today` 一字未动（管理台/看板/账单/SDK 仍读它）。
        if (!platformCtx && r.org_budget && r.org_budget.points_limit > 0) {
          setOrgBudget({ limit: r.org_budget.points_limit, used: r.org_budget.points_used_this_month, name: r.org_budget.name })
        } else {
          setOrgBudget(null)
        }
        // ★ F-52②（批 I-8）：顺手喂养「文本过长」本地闸的上限（chat_max_chars 与后端
        //   stream.go 同一运营策略键、同一函数出参）。字段缺失（老后端/接口降级）时
        //   setChatMaxChars 收到 undefined ⇒ 清空为「不设闸」，不会误拦。
        setChatMaxChars(r.chat_max_chars)
      }
    } catch { setBalance(null) }
  }, [])

  // 组件挂载时初次加载余额与用户上下文
  useEffect(() => {
    void loadBalance()
    void meContext().catch(() => { /* 会话有效性由请求层兜底 */ })
  }, [loadBalance])

  // ★ F-48（批 I-5）：余额条刷新触发点从「消息数变化」改成「流终态广播之后」。
  // 旧判据有两个方向都错的坑：① 关页/清空记录/切换账号会让 messages.length 变小或归零再变大，
  // 白打一枪 myPackage；② 更要紧的是**同一条气泡的流式回写不改数组长度**，一轮翻译真正扣点的
  // done 帧反而可能一枪都不打——余额条停在翻译前的值，界面「用完不知道还剩多少」。
  // 现在由聊天 store 在 done/error 后 debounce 2s 经枢纽广播，顶栏与本条各订阅一份（互不挤掉）。
  const pkgHub = useContext(PkgRefreshCtx)
  useEffect(() => {
    if (!pkgHub) return
    return pkgHub.subscribe(() => { void loadBalance() })
  }, [pkgHub, loadBalance])

  // ---- 进度/消息变化自动滚底（滚动发生在合并框内部，不再滚动整页）----
  // 依赖只挂 chat.messages：流式回写每来一段都会换数组引用，天然把「跟随最新」滚动驱动起来；
  // 代价是用户手动上滚看历史时也会被拉回底部——本组件没有「已离线阅读即暂停跟随」的判定。
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [chat.messages])

  // ---- 输入区自适应高度 ----
  // ★ 2026-10-14 红框批：高度重挂到 [input] 的 effect 上——effect 跑在提交之后，
  //   打字、删字、程序化清空（发送／撤销回填）三种改法都只有一条驱动路径，
  //   不必在每个 setInput 旁边各补一次同步调用（旧形态那样写会随调用点增多而漏一处）。
  //   ⚠️ 归因订正：本批一开始把「清空不回缩」记成「onChange 量到旧的 scrollHeight」，
  //   反证推翻了这条——把改动摘掉、按旧形态跑，jsdom 里清空那一枪照样缩回 40px
  //   （浏览器在 change 事件发出**之前**就已经把新值写进 DOM，量到的一直是当前内容）。
  //   真缺陷是上面那条永久展开闩（用户圈的是整张卡，不是 textarea 那一行），见 expandedLatch 的注释。
  //   三条驱动路径（挂载／窗口 resize／输入变化）仍共用同一个 autoResize，封顶值只有一份。
  useEffect(() => { autoResize() }, [input])

  // ---- 切换模式并持久化 ----
  // fast/pro 模式切换，同时写入 localStorage 以便跨会话记忆
  function setMode2(m: 'fast' | 'pro') {
    setMode(m)
    localStorage.setItem('translate_mode', m)
  }

  // F7：输入内容变化防抖预估积分消耗（后端直接给积分区间与 ≈句数，零换算）
  // 600ms 是「打字停顿」量级：预估只用于展示，每敲一键就打一次 /estimate 会堆出一串注定被丢弃的请求。
  // alive 标志用于丢弃过期响应：切语种/清空输入会让 effect 重跑，
  // 旧请求可能后到，不判 alive 就会把上一次的区间回写成「最新」值。
  useEffect(() => {
    const text = input.trim()
    if (!text || chat.selectedLangs.length === 0) { setEstimate(null); return }
    let alive = true
    const timer = setTimeout(async () => {
      const r = await estimateTranslation(text, chat.selectedLangs, mode, lang)
      if (!alive || !r) return
      setEstimate({ min: r.points_min, max: r.points_max, s: r.cost_sentences_approx ?? 0, low: r.points_balance < r.points_max, qd: r.llm_queue_depth })
    }, 600)
    return () => { alive = false; clearTimeout(timer) }
    // ★ lang 进依赖：界面语言会改变后端提示词那一套（预估的固定开销按真实提示词现算），
    //   不重跑就会把「切换语言之前」那一份区间继续挂着。
  }, [input, chat.selectedLangs, mode, lang])

  // ★ F7：快捷键 Cmd/Ctrl+K 聚焦搜索框
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setSearchOpen(true)
        // 延迟一拍再聚焦：搜索框是 searchOpen 变 true 后才挂载的，同一帧内 querySelector 还取不到
        setTimeout(() => document.querySelector<HTMLInputElement>('.chat-search input')?.focus(), 50)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // 窗口尺寸变化时重算输入区高度（合并后输入区高度按视口比例封顶，见 autoResize）
  useEffect(() => {
    const onResize = () => autoResize()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // F7：导出当前会话为 Markdown（纯前端拼装，不经后端，故不额外消耗积分）
  function exportChat() {
    const lines: string[] = ['# ' + t('chat.exportTitle'), '']
    for (const m of chat.messages) {
      lines.push(`### ${m.role === 'user' ? t('chat.roleQ') : t('chat.roleA')} ${fmtDateTime(m.timestamp)}`)
      lines.push('', m.content || '', '')
    }
    const blob = new Blob([lines.join('\n')], { type: 'text/markdown;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'chat_' + new Date().toISOString().slice(0, 10) + '.md'
    a.click()
    // 延迟释放 blob：立即 revoke 会在部分浏览器取消尚未开始的下载；5s 足够落盘
    setTimeout(() => URL.revokeObjectURL(a.href), 5000)
  }

  // ---- textarea 自动高度 ----
  // 先置 auto 再读 scrollHeight：否则高度会被上一次赋值撑住、量不到真实内容高度。
  // ★ 〇-LK（2026-09-22）：输入框移到底部 composer 后上限收紧（贴在底部的输入区若还能长到半屏，
  //   会把上面的消息流挤没，就不是对话页而是表单了）；超过上限后 textarea 自身滚动，长文照样能看全。
  // ★ 〇-M（2026-09-23）：下限由 96 收到 40——元宝式单卡的空态就是**一行高**，
  //   留着 96 等于逼用户盯着一块空白；打字/换行才逐行长高。
  function autoResize() {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    const cap = Math.max(40, Math.min(240, Math.round(window.innerHeight * 0.28)))
    el.style.height = Math.min(el.scrollHeight, cap) + 'px'
  }

  // 停止生成：只中断前端流式读取；本轮已消耗的 token 不退还（产品规则，故同步 toast 告知）
  function handleStop() {
    chat.stopGeneration()
    toast({ title: t2('chat.stopTokenNote'), tone: 'success' })
  }

  // ---- ★ 决策⑩：顶缘拖拽调高 / 双击回默认 ----
  // 拖拽用 mousedown + window 级 mousemove/mouseup（而非 pointer capture）：jsdom 可直接
  // fireEvent 模拟三步序列，触屏端走默认行为不劫持。方向＝向上拖变高（delta 取负）。
  function startDrag(e: React.MouseEvent) {
    e.preventDefault()
    const startY = e.clientY
    // 起点优先级：已拖过的记忆高度 ＞ **当前实测高度**（默认档是 flex 吃满，只有量出来才知道多高）
    // ＞ 量不到时的视口估算档（jsdom 里 clientHeight 恒 0，走第三条）。
    // 钳制交给 cwClampDialogH（320px–90vh 与 .cw-resized 的 max-height 同档），这里只取起点。
    const measured = dialogRef.current ? dialogRef.current.clientHeight : 0
    const startH = dialogHRef.current > 0 ? dialogHRef.current : (measured > 0 ? measured : cwDefaultDialogH())
    const onMove = (ev: MouseEvent) => {
      setDialogHeight(cwClampDialogH(startH + (startY - ev.clientY)))
    }
    const onUp = () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
      try { localStorage.setItem(CW_DIALOG_H_KEY, String(dialogHRef.current)) } catch { /* 存储满静默 */ }
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }
  // 双击手柄：回默认档并清除记忆（下次进来重新按默认档开）
  function resetHeight() {
    setDialogHeight(0)
    try { localStorage.removeItem(CW_DIALOG_H_KEY) } catch { /* ignore */ }
  }

  // ★ 2026-10-15 用户令：默认展开——收起只由「用户点过收起钮」（collapsedLatch）触发；
  //   只要输入非空／有会话／搜索条开着／有错误提示，就自动回到展开（历史可见性优先，绝不给空条）。
  const hasSession = chat.messages.length > 0 || chat.isLoading
  const collapsed = collapsedLatch && input.trim() === '' && !hasSession && !searchOpen && !chat.errorMessage
  // ★ 2026-10-13 用户令①：只有「用户拖过」（记忆高度 >0）且当前不是收起态时，才进入覆盖档
  //   .cw-resized（flex:0 0 auto）——否则 flex:1 1 0 会把内联 height 直接吃掉，拖拽手柄形同失效。
  const resized = dialogH > 0 && !collapsed
  // 点击入口条展开并聚焦输入框（展开后本回合不再收起，避免边打字边抽走工具条）
  function expandFromEntry() {
    setCollapsedLatch(false)
    setTimeout(() => inputRef.current?.focus(), 0)
  }
  // 点框头「收起」钮：空态（无输入、无会话）才允许收起，避免把用户正看着的会话藏进收起档
  function collapseDialog() {
    setCollapsedLatch(true)
  }

  // ---- 发送 ----
  // 目标语言取聊天全局 selectedLangs（不在输入框里重复选）；源文先 trim 后再判空，
  // 但发送的是 rawText——已 trim，避免把纯空白当一次有效翻译请求计费
  async function handleSend() {
    sendNow(input.trim())
  }

  // ★ 2026-10-15 用户令「语音改成元宝那样」的发送口径已由 2026-10-19 用户令覆盖：
  //   语音识别产物改为**回填输入框**（见 stopMic），sendNow 现在只服务键盘发送（handleSend），
  //   但它仍是唯一的发送咽喉（慢文本预检 / 忙碌拦截 / 缩翻 / 目标语言四条守卫只此一份）。
  function sendNow(rawText: string) {
    if (!rawText) return
    // ★ 决策⑪①「宁误拦」：预检判 reject（预计超 90s 时限）直接不发送——
    //   发出去大概率白等 90s 收一个 chat_timeout；工单链路是这段文本的正解。
    const verdict = slowVerdict(rawText.length, mode, estimate?.qd)
    if (verdict === 'reject') { toast({ title: t2('chat.slowReject'), tone: 'warn' }); return }
    // 合并后文件入口不存在，进行中的翻译是唯一并发源：保留「忙」提示口径
    if (chat.isLoading) { toast({ title: t2('chat.busy'), tone: 'warn' }); return }
    setInput('')
    autoResize()
    const options: Record<string, unknown> = { target_langs: chat.selectedLangs, lang }
    // ★ #36：缩翻接到文本翻译（0=未启用，后端 maxLengthOption 自行忽略）
    if (condenseOn && condenseMax > 0) options.max_length = condenseMax
    chat.sendMessage(rawText, options)
  }

  // ---- ★ P3 麦克风录音（2026-10-14）----
  // ★ 2026-10-19 用户令（覆盖 2026-10-15「一次手势直接发送」口径）：识别出文本后**回填输入框**，
  //   允许用户二次编辑，点「翻译」才发送。理由＝误识别/口误先校对再计费，目标语种也能在发送前改；
  //   识别失败仍只提示、不回填、不发送（错原文发出去既费积分又没法挽回）。
  // 可用性：浏览器不支持就直接**不渲染这颗按钮**（不给一个点了必然报错的入口）。
  // ★ 2026-10-16：再加一道服务端能力门——cap.asr===false（服务端没装识别模型）时同样整颗不渲染；
  //   cap.asr===null（探不到）走 fail-open 照常渲染，真点了仍有识别失败的本地化提示兜底。
  const cap = useVoiceCapability()
  const canMic = micSupported() && cap.asr !== false
  const micRef = useRef<AsrSession | null>(null)
  const [micOn, setMicOn] = useState(false)
  const [micBusy, setMicBusy] = useState(false)

  async function startMic() {
    if (!canMic || micBusy) return
    // ★ P4 接缝：自动优先端侧 WASM（资产就绪才选），否则回落服务端——行为对 P3 透明。
    let engine
    try {
      engine = await getAsrEngine()
    } catch {
      // 极端情况（localStorage 不可达等）退回服务端，不阻断录音入口
      engine = new ServerAsrEngine()
    }
    const session = engine.start({
      onState: (s) => {
        if (s === 'denied' || s === 'failed') {
          setMicOn(false)
          toast({ title: s === 'denied' ? t2('chat.micDenied') : t2('chat.micFailed'), tone: 'warn' })
        }
      },
    })
    micRef.current = session
    setMicOn(true)
  }

  async function stopMic() {
    const session = micRef.current
    micRef.current = null
    setMicOn(false)
    if (!session) return
    session.stop()
    setMicBusy(true)
    try {
      const text = await session.final
      if (text && text.trim()) {
        // ★ 2026-10-19 用户令：识别出文本后**回填输入框**（允许二次编辑），由用户点「翻译」再发送
        //   ——推翻 2026-10-15「识别完直接发送」的做法：误识别/口误先校对再计费，目标语种也可在
        //   发送前改。expandFromEntry 同时把焦点落到输入框，方便接着改。
        expandFromEntry()
        setInput(text.trim())
      }
    } catch (e) {
      // ★ 界面这句必须来自词典（改造方案 §六 第 9 条）：服务端引擎抛的是 VoiceError，
      //   message 是后端给运维看的中文，直接 toast 即「俄语界面里一句中文」。
      //   ASR 失败在客户侧只有一个可行动作——重试或改用键盘输入，那句本就在词典里。
      // ★ 2026-10-19 G11：静音/极低能量（后端 reason='no_speech'）与"识别失败"分开说——
      //   前者要用户「再说一遍」，后者是能力/链路问题，行动不同。档位由后端 asrReason 给；
      //   键已进 12 语种词典（本批收尾补译），走 t() 取词。
      if ((e as { reason?: string } | null)?.reason === 'no_speech') {
        toast({ title: t('chat.micNoSpeech'), tone: 'warn' })
      } else {
        toast({ title: t2('chat.micFailed'), tone: 'warn' })
      }
    } finally {
      setMicBusy(false)
    }
  }

  // ★ 2026-10-17 用户令：即时翻译的图片上传（推翻 #36「只做文本」旧决策）。
  //   能力门与麦克风同口径（浮层只在该能力可用时渲染）：cap.ocr===false（服务端没装 OCR）时整颗不渲染；
  //   cap.ocr===null（探不到）走 fail-open 照常渲染，真点了仍有本地化失败提示兜底。
  //   结果是**临时卡片**而不是写进 chat.messages：译后图是 base64 data URL（可能数 MB），
  //   而消息流会落 localStorage（配额约 5MB），把图片写进历史会把整站存储顶爆——卡片不持久化是刻意的。
  const canImage = cap.ocr !== false
  const imgInputRef = useRef<HTMLInputElement>(null)
  const [imgBusy, setImgBusy] = useState(false)
  const [imgResult, setImgResult] = useState<ChatImageResult | null>(null)

  // onPickImage 选中图片后的整条链路：校验 → 上传翻译 → 出卡片；失败按 reason 档位取词典文案。
  async function onPickImage(f: File | null | undefined) {
    if (!f || imgBusy) return
    // 只收图片：accept 在系统选择框里生效，拖拽/旧缓存包能绕过来，故这里再按类型与扩展名各判一次。
    const okType = /^image\//i.test(f.type) || /\.(png|jpe?g|bmp|tiff?|webp|gif|heic)$/i.test(f.name)
    if (!okType) { toast({ title: t2('chat.imageFailed'), tone: 'warn' }); return }
    setImgBusy(true)
    setImgResult(null)
    try {
      const langs = chat.selectedLangs.length ? chat.selectedLangs : ['en']
      const res = await chatImageTranslate(f, langs, mode, lang)
      setImgResult(res)
    } catch (e) {
      // ★ 12 语种词典说话：后端 reason 档位 → 词典键（capability_missing/no_text/其余）。
      const reason = e instanceof ImageError ? e.reason : 'failed'
      const title = reason === 'capability_missing' ? t2('chat.imageCapability')
        : reason === 'no_text' ? t2('chat.imageNoText')
          : t2('chat.imageFailed')
      toast({ title, tone: 'warn' })
    } finally {
      setImgBusy(false)
      // 清空 value：同一张图连选两次也要能再次触发 change。
      if (imgInputRef.current) imgInputRef.current.value = ''
    }
  }

  const canSend = input.trim().length > 0
  // ★ 决策⑪①：发起前三档预检（宁误拦：排队深度未知走保守兜底，见 lib/slowEstimate 文件头）。
  //   ok=放行；slow=琥珀横幅提示可转工单（不拦发送）；reject=拦发送＋工单入口。
  const slowV = canSend ? slowVerdict(input.trim().length, mode, estimate?.qd) : 'ok'
  // F7：会话内搜索过滤（空串 = 全量）
  const shownMessages = searchQ.trim()
    ? chat.messages.filter((m) => (m.content || '').toLowerCase().includes(searchQ.trim().toLowerCase()))
    : chat.messages

  return (
    /* ★ 2026-09-24 〇-S（#7）：高度口径从「视口-顶栏 39px 自算」改为 flex:1 吃满 .app-main——
       视口里除了顶栏还有一行页脚，任何写死的 calc(100vh - 39px) 都会把页脚顶出首屏（永远滚不到）。
       ★ 2026-10-16 页脚已收进左栏折叠块（不再占右主列高度），本页口径不变（flex:1 仍是最稳的写法）。
       外壳（App.tsx FrontShell）已改 height:100dvh + .app-main overflowY:auto，
       本页 flex:1 + minHeight:0 即精确占满剩余空间，消息列表内滚、输入区与页脚常驻可见。
       （旧注释留档：39 = 顶栏 38 + 1px 下边框，UI-ANNOTATIONS §2.2 顶栏真值高 38。）
       background:#000 与 theme.css 的 html,body 底色同底：懒加载占位/回弹露出的底色
       必须与本页一致，否则切页瞬间会闪一块异色（#66 换词动效糊白底那次的成因）。 */
    <div className="cw-root" style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, background: '#000' }}>
      <style>{CW_CSS}</style>
      {/* 离线横幅：琥珀薄底 + 语义色文字（交付包里唯一的非单色告警档），
          不用红色——后端不可达多是网络抖动/发版窗口，属「待恢复」而非「用户出错」。
          重试钮只在 !isBackendLoading 时出现：离线后 useChat 会自行按秒重探（最长 30 次），
          那段窗口里再给一个手动重试，等于和后台轮询抢同一个 health 接口。 */}
      {!chat.isBackendOnline && (
        <div style={{ background: 'rgba(210,153,34,0.10)', borderBottom: '1px solid rgba(210,153,34,0.32)', padding: '8px 6%', display: 'flex', gap: 10, alignItems: 'center' }}>
          <span style={{ fontSize: 15, color: '#D29922' }}>
            {t2('chat.offline')}
          </span>
          {!chat.isBackendLoading && (
            <Button variant="secondary" size="sm" disabled={chat.isBackendChecking} onClick={() => void chat.retryHealth()}>
              {chat.isBackendChecking ? <Spinner /> : t2('chat.retry')}
            </Button>
          )}
        </div>
      )}

      {/* 余额 / 用量条：§2.2「余额条 高20 · 11px #9AA0AA · 左缩进 60」——11 号字按 1.45 行高约 16，
          上下各 2 内边距即凑足 20 高；多段并排放不下时仍靠 flexWrap 换行，故用 minHeight 不钉死。 */}
      {(balance || orgBudget) && (
        <div style={{ background: 'rgba(231,233,234,0.06)', color: 'var(--lc-text-2)', fontSize: 13, minHeight: 20, padding: '2px 6%', display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'center', borderBottom: '1px solid var(--lc-border-faint)' }}>
          {/* data-testid 只给 e2e 用（★ 任务 #43 翻译主流程端到端）：余额是扣费可见性的界面口径，
              锚点必须与 i18n 文案解耦——文案随 12 语种变，锚点不能跟着变。
              ★ 2026-10-14 用户令：「今日已耗 N 积分」这一颗**从界面撤掉**（红框批）。
              撤的是显示面，不是账：出参 `points_used_today` 仍由后端照旧给出，管理台/看板/账单照旧可读；
              扣费可见性的界面锚点收敛到 chat-balance（余额减少＝真扣了钱，比"今日已耗"更贴钱）。
              三处消费方（platformBilling 单测／e2e translate_flow／e2e-manual 实时余额）已同批改锚。 */}
          {balance && <span data-testid="chat-balance">{tpl('chat.balanceTokens', { n: fmtPoints(balance.points), s: fmtNum(balance.approx) })}</span>}
          {orgBudget && <span>{tpl('chat.orgBudgetFmt', { name: orgBudget.name, used: fmtPoints(orgBudget.used), limit: fmtPoints(orgBudget.limit) })}</span>}
          {/* 预估消耗：积分区间与 ≈句数都由后端直出（零换算）；low（预估上限已超余额）转琥珀加粗 */}
          {estimate && (
            <span style={estimate.low ? { color: '#D29922', fontWeight: 600 } : undefined}>
              {tpl('chat.estimateTokens', { min: fmtPoints(estimate.min), max: fmtPoints(estimate.max), s: fmtNum(estimate.s), bal: balance ? fmtPoints(balance.points) : '0' })}
              {estimate.low && ` · ${t('chat.estLow')}`}
            </span>
          )}
        </div>
      )}

      {/* F7：会话内搜索 */}
      {searchOpen && (
        <div className="chat-search" style={{ padding: '6px 6% 0' }}>
          <Input autoFocus aria-label={t('chat.searchPh')} placeholder={t('chat.searchPh')} value={searchQ}
                 onChange={(e) => setSearchQ(e.target.value)}
                 trailing={searchQ ? (
                   <button type="button" className="cw-search-clear" aria-label={t('chat.searchClear')} onClick={() => setSearchQ('')}>
                     <CloseIcon size={14} />
                   </button>
                 ) : null} />
        </div>
      )}

      {/* ★ #36 合并对话框：内部三段式（框头 / 滚动区 / 框脚）。
          ★ 2026-10-13 用户令①（覆盖 2026-10-10 决策⑩ 的固定默认档）：默认高度**吃满页眉与页脚
          之间的剩余空间**（.cw-dialog 走 flex:1 1 0，JS 不写内联高度），与本文件头第 3 行
          「一张占满除页眉页脚外整屏的对话框」的定档口径重新一致；
          顶缘手柄仍可拖 320px–90vh、双击回默认、高度记 localStorage（lc-cw-dialog-h），
          拖过之后进入覆盖档 .cw-resized（内联高度生效、不再被 flex 拉伸）；
          ★ 2026-10-15 用户令：默认展开；点框头「收起」钮且空输入无会话时才收成单行入口条
          （cw-collapsed：框头/滚动区/工具条隐藏，点入口条展开——收起是 CSS 档不是卸载）。 */}
      <div
        ref={dialogRef}
        className={`cw-dialog${collapsed ? ' cw-collapsed' : ''}${resized ? ' cw-resized' : ''}`}
        style={{ ...CARD, boxShadow: '0 8px 24px rgba(0,0,0,.4)', ...(resized ? { height: cwClampDialogH(dialogH) } : {}) }}
        onClick={collapsed ? expandFromEntry : undefined}
      >
        {/* ★ 决策⑩：顶缘拖拽手柄（6px 命中带，cursor:row-resize；双击回默认档） */}
        <div
          className="cw-resize-handle"
          data-testid="cw-resize-handle"
          role="separator"
          aria-orientation="horizontal"
          aria-label={t('chat.resizeHandle')}
          title={t('chat.resizeHandle')}
          onMouseDown={startDrag}
          onDoubleClick={resetHeight}
        />
        {/* 框头：会话标题 + 工具（搜索 / 导出 / 清空）。★ 〇-LK：「原文/自动检测」标签
            跟着输入框挪到了底部 composer，框头只留会话级操作——顶部是历史，底部是输入。 */}
        <div className="cw-dialog-head">
          {/* ★ 2026-10-15 用户令：框头标题与工单/编辑器页的页标题（h2）同档——原先 15px
             与左栏导航项同字阶，比其它 tab 的页标题小一大截。 */}
          <span style={{ fontSize: 20, fontWeight: 600, color: '#E7E9EA', letterSpacing: '.04em' }}>{t('app.tabWorkbench')}</span>
          <span style={{ flex: 1 }} />
          {/* 纯图标按钮：title 给鼠标悬浮、aria-label 给读屏，二者缺一不可 */}
          <button type="button" className="cw-icon-btn" title={t('chat.searchPh')} aria-label={t('chat.searchPh')}
                  onClick={() => { setSearchOpen((v) => !v); setSearchQ('') }}>
            <SearchIcon size={16} />
          </button>
          <button type="button" className="cw-icon-btn" title={t('chat.exportMd')} aria-label={t('chat.exportMd')} onClick={exportChat}>
            <DownloadIcon size={16} />
          </button>
          <button type="button" className="cw-icon-btn" title={t('chat.clearChat')} aria-label={t('chat.clearChat')} onClick={() => { chat.clearMessages() }}>
            <TrashIcon size={16} />
          </button>
          {/* ★ 2026-10-15 用户令：空态默认展开，收起改由用户显式点这颗钮触发（见 collapsedLatch 注释）。
              收起后框头被 CSS 隐藏，展开靠点单行入口条（整卡可点）。 */}
          <button type="button" className="cw-icon-btn" data-testid="cw-collapse"
                  title={t('chat.collapse')} aria-label={t('chat.collapse')} onClick={collapseDialog}>
            <CollapseGlyph />
          </button>
        </div>

        {/* 滚动区：会话消息流（★ 〇-LK：输入框不再放这里——AI 对话的输入永远在底部，
            气泡在其上方往上长；把 textarea 摆在最上面是「表单」不是「对话」） */}
        <div className="cw-dialog-body" ref={scrollRef}>
          {/* ★ 2026-10-17 图片翻译结果卡（临时、不落存储）：译后图（原位回填）＋各目标语译文文本。
              译后图是**择优产出**——后端回填失败时 image 为空串，此时只出译文文本并留一行降级说明。 */}
          {imgResult && (
            <div className="cw-img-result" data-testid="chat-image-result">
              <div className="cw-img-result-head">
                <span>{t2('chat.imageResultTitle')}</span>
                <button type="button" className="cw-icon-btn" aria-label={t2('chat.imageClose')}
                        title={t2('chat.imageClose')} onClick={() => setImgResult(null)}>
                  <CloseIcon size={14} />
                </button>
              </div>
              {imgResult.image ? (
                <a className="cw-img-wrap" href={imgResult.image}
                   download={`translated-${imgResult.image_lang || 'out'}.png`}
                   title={t2('chat.imageDownload')}>
                  <img className="cw-img-out" src={imgResult.image} alt={t2('chat.imageResultTitle')} />
                  <span className="cw-img-dl">
                    <DownloadIcon size={14} /> {t2('chat.imageDownload')}
                  </span>
                </a>
              ) : (
                <div className="cw-img-degraded">{t2('chat.imageDegraded')}</div>
              )}
              {/* 译文文本：逐语种一行（语种码大写当标题，正文走 dir="auto" 吃 RTL） */}
              <div className="cw-img-text" dir="auto">
                {Object.entries(imgResult.translations).map(([lg, tx]) => (
                  <div className="cw-img-line" key={lg}>
                    <span className="cw-img-lang">{lg.toUpperCase()}</span>
                    <span>{tx}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
          {/* 空状态：欢迎语（首轮翻译前的引导，文案不再提文件） */}
          {!chat.messages.length && !chat.isLoading && (
            <div className="cw-welcome">
              <div style={{ fontSize: 17, color: 'var(--lc-text-2)', marginBottom: 8 }}>{t('chat.welcome')}</div>
              <div style={{ fontSize: 15, maxWidth: 520, margin: '0 auto', lineHeight: 1.8 }}>
                {t2('chat.welcomeSub')}
              </div>
            </div>
          )}

          {/* 消息流：assistant 气泡的 source=其上一条 user 消息（上下文展示）
              取法是 slice 到真实下标后 reverse().find()：先在原始 messages 里定位（shownMessages
              是过滤后的子集，不能用它的下标），再往前找「最近一条 user」——
              比直接取 i-1 稳，两条消息之间可能夹非 user 行。 */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {shownMessages.map((m) => {
              const src = m.role !== 'user'
              ? [...chat.messages].slice(0, chat.messages.indexOf(m)).reverse().find((x) => x.role === 'user')?.content
              : undefined
            return <MessageBubble key={m.id} message={m} source={src} onFeedback={setFeedbackMsg} />
            })}
          </div>
        </div>

        {/* 框脚 = 对话输入区（composer）：★ 〇-M（2026-09-23）按用户令改成元宝式单卡——
            一张「凹」进卡面的输入卡，内部自上而下只有两层：输入行 + **一行工具条**
            （源语言口径 · 目标语言胶囊 · 已选语种 chips · 模式 · 缩翻 · 主按钮）。
            旧形态是四排（原文标签+输入框 / 整宽语种下拉 / chips 独立一行 / 模式+缩翻+按钮行），
            用户判「太长太占空间」。压后 1280×720 实测框脚 109px（输入卡 90 = 单行输入 40 + 工具条 48），
            等值锁见 pixel_uat.spec.ts P2b。功能一项未减，只是收进同一排。 */}
        <div className="cw-dialog-foot">
          <div className="cw-composer">
            {/* F3：dir="auto" 让阿/法等 RTL 文本按内容方向渲染。
                用原生 textarea + 组件库 .lc-textarea 类：autoResize 需要 ref 到真实节点量 scrollHeight；
                Enter 发送 / Shift+Enter 换行（与工单页一致的肌肉记忆）
                底色与描边现在由外层 .cw-composer 那张卡统一承担（textarea 自身透明无边），
                字号仍走 .lc-textarea 的 13px 冻结档——pixel_uat P2b 量的就是这个值。 */}
            <textarea
              className="lc-textarea cw-input"
              ref={inputRef}
              dir="auto"
              aria-label={t('chat.placeholder')}
              data-testid="translate-input"
              value={input}
              onChange={(e) => { setInput(e.target.value) }}
              placeholder={t('chat.placeholder')}
              rows={1}
              style={{ width: '100%', background: 'transparent', border: '0' }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  void handleSend()
                }
              }}
            />
            {/* 一行工具条：右内边距 68px 给固定定位的 AI 助手浮球让位（.na-fab），免得遮住主按钮 */}
            <div className="cw-toolbar">
              {/* 源语言仍是自动检测（原「原文 / 自动检测」标签行折进这颗胶囊，信息不减） */}
              <span className="cw-src-pill" title={t('chat.srcLabel') + ' · ' + t('chat.sourceAuto')}>
                {t('chat.srcLabel')} · {t('chat.sourceAuto')}
              </span>
              <LangMultiSelect compact value={chat.selectedLangs} onChange={chat.setSelectedLangs} />
              {/* 已选语种内联在同一行；超过 3 颗折成「+n」，点开就地展开全部（折走的仍可 ×） */}
              <LangChips langs={chat.selectedLangs} onRemove={chat.setSelectedLangs} max={3} dense />
              <ModeToggle value={mode} onChange={setMode2} />
              <label className="cw-condense">
                <input type="checkbox" checked={condenseOn} onChange={(e) => setCondenseOn(e.target.checked)} /> {t('app.condense')}
                {/* 数值框只在勾选后出现：未勾选时它不可用，摆着等于多一个空框占宽 */}
                {!!condenseOn && (
                  <input type="number" min={1} max={10000} value={condenseMax}
                    onChange={(e) => setCondenseMax(parseInt(e.target.value) || 0)}
                    className="cw-condense-num" title={t('chat.s41')} />
                )}
              </label>
              {/* 弹性占位：把主按钮推到行尾；窄屏 flex-wrap 换行时它自然收拢，不会把按钮挤没 */}
              <div style={{ flex: 1 }} />
              {/* ★ 2026-10-17 图片上传：canImage=false（服务端无 OCR）时整颗不渲染——同麦克风口径。
                  隐藏 file input 常驻，按钮只负责 click 它（accept 只在系统框生效，绕过由 onPickImage 兜）。 */}
              <input ref={imgInputRef} type="file" accept="image/*" data-testid="chat-image-input"
                     style={{ display: 'none' }} onChange={(e) => void onPickImage(e.target.files?.[0])} />
              {canImage && (
                <button type="button" className="cw-icon-btn" data-testid="chat-image-btn"
                  title={imgBusy ? t2('chat.imageWorking') : t2('chat.image')}
                  aria-label={imgBusy ? t2('chat.imageWorking') : t2('chat.image')}
                  disabled={imgBusy || chat.isLoading}
                  onClick={() => imgInputRef.current?.click()}>
                  <UploadIcon size={16} />
                </button>
              )}
              {/* ★ P3 麦克风：录完把识别文本填进输入框（不自动发送，★ 2026-10-19 用户令恢复此口径），
                  见上方 stopMic 注释。不支持录音的浏览器**整颗按钮不渲染**——留一个点了必报错的入口没有意义。 */}
              {canMic && (
                <button type="button" className="cw-icon-btn" data-testid="mic-btn"
                  title={micBusy ? t('chat.micWorking') : micOn ? t('chat.micStop') : t('chat.mic')}
                  aria-label={micBusy ? t('chat.micWorking') : micOn ? t('chat.micStop') : t('chat.mic')}
                  disabled={micBusy}
                  onClick={() => { if (micOn) void stopMic(); else void startMic() }}>
                  <MicGlyph active={micOn} />
                </button>
              )}
              {chat.isLoading ? (
                <Button className="cw-send" variant="primary" size="sm" icon={<StopGlyph />} onClick={handleStop}>{t2('chat.stop')}</Button>
              ) : (
                <Button className="cw-send" variant="primary" size="sm" disabled={!canSend || slowV === 'reject'} onClick={() => void handleSend()}>{t('chat.translate')}</Button>
              )}
            </div>
          </div>
          {/* ★ 决策⑪①：发起前三档预检横幅（slow=明示较慢；reject=已拦发送）。琥珀档同离线横幅；
              工单入口整页跳 /tickets——工作台页面无路由上下文依赖（测试可桩 location.assign）。 */}
          {slowV !== 'ok' && (
            <div className="cw-slow-est" data-testid="slow-estimate" role="status"
                 style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', fontSize: 13, color: '#D29922' }}>
              <span style={{ flex: 1, minWidth: 200 }}>{slowV === 'reject' ? t2('chat.slowReject') : t2('chat.slowEstimate')}</span>
              <button type="button" data-testid="slow-to-ticket" onClick={() => { window.location.assign('/tickets') }}
                      style={{ border: 'none', background: 'none', color: '#D29922', fontSize: 13, textDecoration: 'underline', cursor: 'pointer', padding: 0 }}>
                {t2('chat.slowToTicket')}
              </button>
            </div>
          )}
          {!!chat.errorMessage && (
            <div style={{ color: '#F85149', fontSize: 15 }}>{chat.errorMessage}</div>
          )}
        </div>
      </div>

      {/* 反馈弹窗（由 MessageBubble 的反馈按钮触发） */}
      {feedbackMsg && <FeedbackModalFromMessage message={feedbackMsg} onClose={() => setFeedbackMsg(null)} />}
    </div>
  )
}

// —— 页面级样式（cw- 前缀，避免与组件库类名重名）——
// .cw-dialog 默认档用 flex:1 吃满外层剩余高度（页眉与页脚之间），内部滚动只发生在 .cw-dialog-body；
// 拖过的覆盖档 .cw-resized 才撤掉拉伸、按内联高度定高（见上方 2026-10-13 用户令① 那段）。
// .cw-dialog-acts 的右内边距是给固定定位的 AI 助手浮球让位，避免遮住主按钮。
// 描边/文字一律取 --lc-border-* 与 --lc-text-* 令牌（本文件进 #68 闸门扫描范围，
// 写死暗值会红）；圆角走 --lc-r-ctl，与组件库控件同档，避免「同为按钮却两种圆角」。
// 焦点环单独写 :focus-visible：纯黑底上默认 UA 焦点样式几乎看不见，必须自绘 outline。
const CW_CSS = `
.cw-root{box-sizing:border-box}
/* ★ 2026-10-13 用户令①（覆盖决策⑩ 的固定默认档 min(70vh,640px)）：默认档＝**吃满 .cw-root
   里除横幅/余额条/搜索条之外的全部剩余高度**，也就是页眉与页脚之间那一段全给对话框
   （flex:1 1 0 而不是写死 vh——写死 vh 在矮屏会把页脚顶出首屏、在高屏又留一大片黑）。
   下限 320px 保住「框头＋一行输入＋工具条」不被压没；可拖上限由 .cw-resized 的 max-height 与
   JS 的 cwClampDialogH 同档承担（320–90vh）。 */
.cw-dialog{display:flex;flex-direction:column;overflow:hidden;margin:12px 6% 14px;flex:1 1 0;min-height:320px}
/* ★ 决策⑩：顶缘拖拽手柄——6px 命中带贴住卡片顶缘，hover 时给出可拖暗示 */
.cw-resize-handle{flex:0 0 6px;cursor:row-resize;background:transparent}
.cw-resize-handle:hover{background:var(--lc-border-faint)}
/* ★ 2026-10-13：覆盖档＝用户拖过（内联 height 才是最终高度）。必须撤掉 flex 的拉伸与收缩，
   否则 flex-grow 会把内联高度当摆设、手柄拖不动；90vh 上限与 JS 钳制同一把尺子。 */
.cw-dialog.cw-resized{flex:0 0 auto;max-height:90vh}
/* ★ 2026-10-15 用户令：空态默认展开，只有用户点框头「收起」钮才收成单行入口条——
   收起是 CSS 档（display:none）而非卸载，DOM 归属与既有结构锁不冲突；cursor:text 提示「点一下就能打字」。
   收起档同样必须撤 flex 拉伸（单行条不该被撑满），高度交给内容。 */
.cw-dialog.cw-collapsed{flex:0 0 auto;height:auto;min-height:0;cursor:text}
.cw-dialog.cw-collapsed .cw-resize-handle,
.cw-dialog.cw-collapsed .cw-dialog-head,
.cw-dialog.cw-collapsed .cw-dialog-body,
.cw-dialog.cw-collapsed .cw-toolbar{display:none}
/* ★ 决策⑪①：发起前预检横幅（slow/reject 共用，语义色由内联琥珀承担） */
.cw-slow-est{padding:0 2px}
.cw-dialog-head{display:flex;align-items:center;gap:8px;padding:10px 14px;border-bottom:1px solid var(--lc-border-faint);flex-wrap:wrap}
.cw-dialog-body{flex:1;min-height:0;overflow-y:auto;overflow-x:hidden;padding:14px;display:flex;flex-direction:column;gap:12px;scroll-behavior:smooth}
.cw-dialog-body .bubble-row{max-width:100%}
/* ★ 2026-10-17 图片翻译结果卡：与消息气泡同列（同 gap），面色走 L2 panel 档、描边走 1.2px 灰阶
   （〇-P 交付档：全边框 1.2px、面色台阶 --lc-panel）。译后图宽不超容器，点图即下载（外层 <a download> 包住）。 */
.cw-img-result{background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:var(--lc-r-ctl);padding:10px 12px;display:flex;flex-direction:column;gap:8px}
.cw-img-result-head{display:flex;align-items:center;justify-content:space-between;font-size:15px;color:var(--lc-text-1);font-weight:600}
.cw-img-wrap{position:relative;display:block;line-height:0;border-radius:8px;overflow:hidden;border:1.2px solid var(--lc-border-faint)}
.cw-img-out{display:block;max-width:100%;height:auto}
/* 悬浮下载提示条：默认半透明贴底，hover 提亮（纯提示，不参与计费/判定） */
.cw-img-dl{position:absolute;inset-inline-end:8px;bottom:8px;display:inline-flex;align-items:center;gap:4px;
  background:rgba(0,0,0,.6);color:var(--lc-text-1);font-size:13px;line-height:1;padding:5px 8px;border-radius:6px}
.cw-img-degraded{font-size:13px;color:var(--lc-text-3)}
.cw-img-text{display:flex;flex-direction:column;gap:6px;font-size:15px;color:var(--lc-text-1)}
.cw-img-line{display:flex;gap:8px;align-items:baseline}
.cw-img-lang{flex:0 0 auto;font-size:13px;color:var(--lc-text-3);font-weight:600}
.cw-dialog-foot{border-top:1px solid var(--lc-border-faint);padding:8px 12px 10px;display:flex;flex-direction:column;gap:6px}
/* 底部 composer（★ 〇-M 元宝式单卡）：输入行 + 一行工具条同处一张「凹」进卡面的输入卡里，
   整块贴底、不随消息流滚动。底色走 --lc-inset（输入框底真值档），描边走 --lc-border-input。 */
.cw-composer{display:flex;flex-direction:column;background:var(--lc-inset);border:1.2px solid var(--lc-border-input);border-radius:var(--lc-r-ctl)}
.cw-composer .cw-input{min-height:40px;max-height:240px;padding:10px 12px 2px;border:0;border-radius:0;background:transparent;resize:none}
/* 一行工具条：源语言口径 · 目标语言 · 已选 chips · 模式 · 缩翻 · 主按钮。
   右内边距 68px 给固定定位的 AI 助手浮球（.na-fab）让位，窄屏换行时各行同样受益。 */
.cw-toolbar{display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:6px 68px 8px 8px}
.cw-src-pill{display:inline-flex;align-items:center;height:28px;padding:0 10px;border:1.2px solid var(--lc-border-pill);
  border-radius:999px;font-size:14px;color:var(--lc-text-3);white-space:nowrap}
.cw-condense{display:inline-flex;align-items:center;gap:4px;font-size:14px;color:var(--lc-text-2);white-space:nowrap}
.cw-condense-num{width:64px;box-sizing:border-box;height:28px;font-size:14px;background:transparent;
  border:1.2px solid var(--lc-border-pill);border-radius:var(--lc-r-ctl);padding:0 8px;color:var(--lc-text)}
.cw-welcome{text-align:center;padding:32px 12px;color:var(--lc-text-3)}
.cw-icon-btn{display:inline-flex;align-items:center;justify-content:center;width:32px;height:32px;
  border:1.2px solid var(--lc-border-pill);background:transparent;color:var(--lc-text-2);border-radius:var(--lc-r-ctl);cursor:pointer;padding:0;line-height:0}
.cw-icon-btn:hover{color:var(--lc-text);border-color:var(--lc-border-done)}
.cw-icon-btn:focus-visible{outline:2px solid var(--lc-border-strong);outline-offset:2px}
.cw-search-clear{display:inline-flex;align-items:center;justify-content:center;border:0;background:none;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0}
.cw-search-clear:hover{color:var(--lc-text)}
.cw-spin{display:inline-block;width:14px;height:14px;border:1.2px solid rgba(255,255,255,.25);border-top-color:var(--lc-text);border-radius:50%;animation:cw-spin .7s linear infinite}
@keyframes cw-spin{to{transform:rotate(360deg)}}
@media (max-width:900px){
  /* 平板：卡片左右外边距 6%→4%（旧 mobile.css 的 .chat-scroll 内边距口径，
     #36 合并对话框后必须由组件自己下发，否则会被本块的 .cw-dialog 规则盖掉） */
  .cw-dialog{margin:10px 4% 12px}
}
@media (max-width:640px){
  /* 窄屏：整框贴边留白收窄；工具条靠 flex-wrap 自行换行，浮球让位量减半 */
  .cw-dialog{margin:8px 3% 10px}
  .cw-toolbar{padding-inline-end:40px}
  .cw-composer .cw-input{min-height:40px}
  /* ★ 2026-10-15 用户令（第五批）「移动端的聊天内选择也做成元宝式，放在输入框上方」：
     窄屏形态改成「选择行在上、输入行在下」，且麦克风/发送键回到输入行右端（元宝同款）。
     只重排既有节点，DOM 与桌面档一字不动——桌面仍是一行（输入卡 90 / 工具条 48，pixel_uat P2b 量的就是它）。
     做法：工具条 order:-1 提到输入框上方（它就是「选择行」）；麦克风与发送键绝对定位到输入行右端，
     输入框右内垫让出这条通道（96px ≈ 麦克风 32 + 间距 8 + 发送 ~52）。
     ⚠️ 与 AI 助手浮球（.na-fab，固定在视口右下）的关系：移动档页脚已收进抽屉 ⇒ 输入卡贴到视口底，
     这一行正落在浮球带里。故 AiAssist 的 ≤640 档把浮球抬到 bottom:74px（见该文件），
     让出「输入行」这一段；浮球只可能压住上方选择行的右端空白（弹性占位），不压任何按钮。 */
  .cw-composer{position:relative}
  .cw-toolbar{order:-1;padding:8px 8px 2px}
  .cw-composer .cw-input{padding:6px 96px 12px 12px}
  .cw-toolbar > [data-testid="mic-btn"]{position:absolute;right:70px;bottom:6px;z-index:1}
  .cw-toolbar > .cw-send{position:absolute;right:8px;bottom:6px;z-index:1}
}
@media (prefers-reduced-motion: reduce){
  .cw-spin{animation:none}
  .cw-dialog-body{scroll-behavior:auto}
}
`
