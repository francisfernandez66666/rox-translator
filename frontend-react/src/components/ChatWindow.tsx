// ============================================================================
// components/ChatWindow.tsx — 前台即时翻译工作台（★ 2026-09-22 形态回退：输入卡吸顶 + 译文气泡在下方展开）
// 结构：离线横幅 + 余额/用量条 + **一块滚动舞台**；舞台内第一块是吸顶的原文输入卡
//       （语言选择与操作按钮都在卡内），其下依次是欢迎空态与译文结果气泡列表。
// ★ 形态沿革：#36（2026-09-21，提交 106ce50）曾把输入卡与气泡合并成一张吃满整屏的对话框，
//   用户判「原来的气泡对话框没了」，2026-09-22 按「回退形态、保留功能」恢复两段式；
//   合并期间新增/改动的功能（会话搜索·导出·清空、缩翻接文本、预估与余额、气泡上下文与反馈）
//   **全部保留**，只回退 DOM 形态。文件翻译入口维持 #36 的下线决定（统一走「文档翻译」工单页）。
// ★ #36 同时移除即时翻译的文件翻译入口（上传按钮/隐藏 file input/校验/发送）：
//       文件翻译统一走「文档翻译」工单页（TicketsPage → /api/tickets/create-file），
//       即时翻译只做文本，避免同一份文件两条口径不一致的链路。
// 业务逻辑/API 全部保留，仅重写呈现层，文案走词典（无硬编码中文）。
// 呈现层已迁至 @/ui/langcross/src 纯黑组件库（TDesign 全部移除）。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Button, Input, useToast,
  SearchIcon, DownloadIcon, TrashIcon, CloseIcon,
} from '@/ui/langcross/src'
import MessageBubble from './MessageBubble'
import { FeedbackModalFromMessage } from './modals'
import { useChat } from '@/hooks/useChat'
import { myPackage, meContext } from '@/api'
import { estimateTranslation } from '@/api/translate'
import { fmtPoints } from '@/utils/points'
import type { ChatMessage } from '@/types'
import { useT, t, tpl } from '@/i18n'
import LangMultiSelect, { LangChips } from '@/components/LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'

// 数字千分位格式化，并处理 undefined/负数，用于余额与用量展示
// floor + Math.max(0,…) 是必要的：本函数喂的是「≈句数」这类折算值（balance_sentences_approx、
// estimate 的 s），后端回传小数或早期脏数据为负时，直接格式化会显示成「-1,234」这种吓人数字。
function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(Math.max(0, Math.floor(n || 0)))
}

// 卡片样式（纯黑体系：面板 #0E1014 + 1.2px 描边 + 圆角 14）
// ★ #68：描边走 --lc-border-card 令牌（旧字面 #464C58 在纯黑上不足 3:1，看不见边）。
// 卡片规格集中成常量而不是散进 JSX 内联：内联字面值正是 #68 闸门要收口的形态，
// 走令牌后描边档位由 theme.css §十 统一调，页面不需要跟着改。
const CARD: React.CSSProperties = {
  background: '#0E1014', border: '1.2px solid var(--lc-border-card)', borderRadius: 14,
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

// 默认导出组件：前台即时翻译工作台（吸顶输入卡 + 下方译文气泡列表）
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
  const [estimate, setEstimate] = useState<{ min: number; max: number; s: number; low: boolean } | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQ, setSearchQ] = useState('')

  // ★ 余额 / 用量（2026-09-19 全积分口径：API 出参即积分，前端零换算）
  const [balance, setBalance] = useState<{ points: number; approx: number } | null>(null)
  const [usage, setUsage] = useState<{ today: number } | null>(null)
  const [orgBudget, setOrgBudget] = useState<{ limit: number; used: number; name: string } | null>(null)

  // scrollRef 指向滚动舞台：输入卡吸顶在其内、译文气泡在卡下方展开，滚底即跟随最新一条译文
  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  // ---- 余额 / 用量加载 ----
  // 从 myPackage 接口读取个人余额、今日用量及企业预算额度（均为积分）
  // org_budget 只在 points_limit>0 时才有意义：未开预算的企业回传 0，
  // 若照样 set 就会在余额条里渲染「已用 X / 共 0」这种误导文案，故显式清成 null。
  const loadBalance = useCallback(async () => {
    try {
      const r: any = await myPackage()
      if (r && r.success) {
        if (typeof r.points_balance === 'number') {
          setBalance({ points: r.points_balance, approx: r.balance_sentences_approx ?? 0 })
        }
        if (typeof r.points_used_today === 'number') {
          setUsage({ today: r.points_used_today })
        }
        if (r.org_budget && r.org_budget.points_limit > 0) {
          setOrgBudget({ limit: r.org_budget.points_limit, used: r.org_budget.points_used_this_month, name: r.org_budget.name })
        } else {
          setOrgBudget(null)
        }
      }
    } catch { setBalance(null) }
  }, [])

  // 组件挂载时初次加载余额与用户上下文
  useEffect(() => {
    void loadBalance()
    void meContext().catch(() => { /* 会话有效性由请求层兜底 */ })
  }, [loadBalance])

  // 每轮翻译结束（消息数变化）后刷新剩余量
  useEffect(() => {
    if (chat.messages.length) void loadBalance()
  }, [chat.messages.length, loadBalance])

  // ---- 进度/消息变化自动滚底（滚动发生在舞台内，整页不滚）----
  // 依赖只挂 chat.messages：流式回写每来一段都会换数组引用，天然把「跟随最新」滚动驱动起来；
  // 代价是用户手动上滚看历史时也会被拉回底部——本组件没有「已离线阅读即暂停跟随」的判定。
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [chat.messages])

  // ---- 输入区初始自适应高度 ----
  // 空依赖：仅挂载时归一一次高度，之后由 onChange、窗口 resize、发送后清空三条路径驱动
  // （autoResize 是唯一算高度的地方，几处共用才不会各自实现出不同的封顶值）。
  useEffect(() => { autoResize() }, [])

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
      const r = await estimateTranslation(text, chat.selectedLangs, mode)
      if (!alive || !r) return
      setEstimate({ min: r.points_min, max: r.points_max, s: r.cost_sentences_approx ?? 0, low: r.points_balance < r.points_max })
    }, 600)
    return () => { alive = false; clearTimeout(timer) }
  }, [input, chat.selectedLangs, mode])

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

  // 窗口尺寸变化时重算输入区高度（吸顶输入卡有 220px 高度预算，见 autoResize）
  useEffect(() => {
    const onResize = () => autoResize()
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // F7：导出当前会话为 Markdown（纯前端拼装，不经后端，故不额外消耗积分）
  function exportChat() {
    const lines: string[] = ['# ' + t('chat.exportTitle'), '']
    for (const m of chat.messages) {
      lines.push(`### ${m.role === 'user' ? t('chat.roleQ') : t('chat.roleA')} ${new Date(m.timestamp).toLocaleString()}`)
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
  // 上限 220px 是「吸顶输入卡」的可视高度预算——输入卡是 sticky 的，再高就会把下方译文区
  // 整块挤没（长文该走「文档翻译」工单页），40% 视口系数只在小屏上把预算再收一档。
  function autoResize() {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    const cap = Math.max(96, Math.min(220, Math.round(window.innerHeight * 0.35)))
    el.style.height = Math.min(el.scrollHeight, cap) + 'px'
  }

  // 停止生成：只中断前端流式读取；本轮已消耗的 token 不退还（产品规则，故同步 toast 告知）
  function handleStop() {
    chat.stopGeneration()
    toast({ title: t2('chat.stopTokenNote'), tone: 'success' })
  }

  // ---- 发送 ----
  // 目标语言取聊天全局 selectedLangs（不在输入框里重复选）；源文先 trim 后再判空，
  // 但发送的是 rawText——已 trim，避免把纯空白当一次有效翻译请求计费
  async function handleSend() {
    const rawText = input.trim()
    if (!rawText) return
    // 文件入口已下线（走工单页），进行中的翻译是唯一并发源：保留「忙」提示口径
    if (chat.isLoading) { toast({ title: t2('chat.busy'), tone: 'warn' }); return }
    setInput('')
    autoResize()
    const options: Record<string, unknown> = { target_langs: chat.selectedLangs, lang }
    // ★ #36：缩翻接到文本翻译（0=未启用，后端 maxLengthOption 自行忽略）
    if (condenseOn && condenseMax > 0) options.max_length = condenseMax
    chat.sendMessage(rawText, options)
  }

  const canSend = input.trim().length > 0
  // F7：会话内搜索过滤（空串 = 全量）
  const shownMessages = searchQ.trim()
    ? chat.messages.filter((m) => (m.content || '').toLowerCase().includes(searchQ.trim().toLowerCase()))
    : chat.messages

  return (
    /* 外层高度 = 视口减去页眉（即时翻译页无页脚，SiteFooter 只在抽屉内渲染）；
       39 = 顶栏 38 + 1px 下边框（★ 2026-09-22 还原 UI-ANNOTATIONS §2.2 顶栏真值高 38；
       旧值 57 是 10px 上下内边距时代的实测耦合值，勿凭手感回调——顶栏改尺寸时此处必须同步）。
       minHeight:0 必须显式给：flex 列里的滚动子项默认 min-height:auto，
       不置 0 则内部 overflow 永不生效（整页滚而非框内滚）。
       background:#000 与 theme.css 的 html,body 底色同值：懒加载占位/回弹露出的底色
       必须与本页一致，否则切页瞬间会闪一块异色（#66 换词动效糊白底那次的成因）。 */
    <div className="cw-root" style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 39px)', minHeight: 0, background: '#000' }}>
      <style>{CW_CSS}</style>
      {/* 离线横幅：琥珀薄底 + 语义色文字（交付包里唯一的非单色告警档），
          不用红色——后端不可达多是网络抖动/发版窗口，属「待恢复」而非「用户出错」。
          重试钮只在 !isBackendLoading 时出现：离线后 useChat 会自行按秒重探（最长 30 次），
          那段窗口里再给一个手动重试，等于和后台轮询抢同一个 health 接口。 */}
      {!chat.isBackendOnline && (
        <div style={{ background: 'rgba(210,153,34,0.10)', borderBottom: '1px solid rgba(210,153,34,0.32)', padding: '8px 6%', display: 'flex', gap: 10, alignItems: 'center' }}>
          <span style={{ fontSize: 13, color: '#D29922' }}>
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
      {(balance || usage || orgBudget) && (
        <div style={{ background: 'rgba(231,233,234,0.06)', color: 'var(--lc-text-2)', fontSize: 11, minHeight: 20, padding: '2px 6%', display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'center', borderBottom: '1px solid var(--lc-border-faint)' }}>
          {/* data-testid 只给 e2e 用（★ 任务 #43 翻译主流程端到端）：余额/今日已耗是扣费可见性的
              唯一界面口径，锚点必须与 i18n 文案解耦——文案随 12 语种变，锚点不能跟着变。 */}
          {balance && <span data-testid="chat-balance">{tpl('chat.balanceTokens', { n: fmtPoints(balance.points), s: fmtNum(balance.approx) })}</span>}
          {usage && <span data-testid="chat-usage">{tpl('chat.usedTokens', { n: fmtPoints(usage.today) })}</span>}
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

      {/* ★ 形态回退：一块滚动舞台，内含「吸顶输入卡 + 下方译文气泡列表」两段。
          输入卡 sticky 在舞台顶端，译文气泡在它下方展开——即用户要求的「原来的气泡对话框」形态。 */}
      <div className="cw-stage" ref={scrollRef}>
        {/* 输入卡：position/top/zIndex 走内联，便于单测在 jsdom 里直接读 element.style 钉住吸顶形态 */}
        <section className="cw-card" style={{ ...CARD, position: 'sticky', top: 0, zIndex: 5 }}>
          <div className="cw-card-head">
            <span style={{ fontSize: 13, fontWeight: 600, color: '#E7E9EA', letterSpacing: '.04em' }}>{t('chat.srcLabel')}</span>
            <span style={{ fontSize: 12, color: 'var(--lc-text-3)', flex: 1 }}>{t('chat.sourceAuto')}</span>
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
          </div>

          {/* F3：dir="auto" 让阿/法等 RTL 文本按内容方向渲染。
              用原生 textarea + 组件库 .lc-textarea 类：autoResize 需要 ref 到真实节点量 scrollHeight；
              Enter 发送 / Shift+Enter 换行（与工单页一致的肌肉记忆）
              内联只覆写底色与描边：底色取最深一档 #0A0B0D 让输入区从卡面 #0E1014 里「凹」下去，
              描边走 --lc-border-input 令牌（★ #68：写死暗值会被 readability.test.ts 的描边锁判红） */}
          <textarea
            className="lc-textarea cw-input"
            ref={inputRef}
            dir="auto"
            aria-label={t('chat.placeholder')}
            data-testid="translate-input"
            value={input}
            onChange={(e) => { setInput(e.target.value); autoResize() }}
            placeholder={t('chat.placeholder')}
            rows={2}
            style={{ width: '100%', background: '#0A0B0D', borderColor: 'var(--lc-border-input)' }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                void handleSend()
              }
            }}
          />

          {/* 目标语言 + 已选语种 chips */}
          <div className="cw-card-langs">
            <span style={{ fontSize: 12, color: 'var(--lc-text-3)', whiteSpace: 'nowrap' }}>{t('chat.targetLangLabel')}</span>
            <div style={{ minWidth: 260, flex: 1 }}>
              <LangMultiSelect value={chat.selectedLangs} onChange={chat.setSelectedLangs} />
            </div>
          </div>
          <div style={{ marginTop: 6 }}>
            <LangChips langs={chat.selectedLangs} onRemove={chat.setSelectedLangs} />
          </div>
          {!!chat.errorMessage && (
            <div style={{ color: '#F85149', fontSize: 13 }}>{chat.errorMessage}</div>
          )}

          {/* 操作行：双模式 / 缩翻 / 主按钮（右内边距让位给右下角 AI 助手浮球） */}
          <div className="cw-card-acts">
            <ModeToggle value={mode} onChange={setMode2} />
            <label style={{ fontSize: 12, color: 'var(--lc-text-2)', display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
              <input type="checkbox" checked={condenseOn} onChange={(e) => setCondenseOn(e.target.checked)} /> {t('app.condense')}
            </label>
            <div style={{ width: 72, flexShrink: 0 }}>
              {condenseOn && (
                <input type="number" min={1} max={10000} value={condenseMax}
                  onChange={(e) => setCondenseMax(parseInt(e.target.value) || 0)}
                  style={{ width: '100%', boxSizing: 'border-box', height: 28, fontSize: 12, background: '#0A0B0D', border: '1.2px solid var(--lc-border-input)', borderRadius: 6, padding: '0 6px', color: '#E7E9EA' }}
                  title={t('chat.s41')} />
              )}
            </div>
            {/* 弹性占位：把主按钮推到行尾；行尾再留 68px 让位给固定定位的 .na-fab */}
            <div style={{ flex: 1 }} />
            {chat.isLoading ? (
              <Button variant="primary" icon={<StopGlyph />} onClick={handleStop}>{t2('chat.stop')}</Button>
            ) : (
              <Button variant="primary" disabled={!canSend} onClick={() => void handleSend()}>{t('chat.translate')}</Button>
            )}
          </div>
        </section>

        {/* 空状态：欢迎语（首轮翻译前的引导，文案不再提文件） */}
        {!chat.messages.length && !chat.isLoading && (
          <div className="cw-welcome">
            <div style={{ fontSize: 15, color: 'var(--lc-text-2)', marginBottom: 8 }}>{t('chat.welcome')}</div>
            <div style={{ fontSize: 13, maxWidth: 520, margin: '0 auto', lineHeight: 1.8 }}>
              {t2('chat.welcomeSub')}
            </div>
          </div>
        )}

        {/* 结果气泡列表：在输入卡下方展开；assistant 气泡的 source=其上一条 user 消息（上下文展示）
            取法是 slice 到真实下标后 reverse().find()：先在原始 messages 里定位（shownMessages
            是过滤后的子集，不能用它的下标），再往前找「最近一条 user」——
            比直接取 i-1 稳，两条消息之间可能夹非 user 行。 */}
        <div className="cw-results">
          {shownMessages.map((m) => {
            const src = m.role !== 'user'
            ? [...chat.messages].slice(0, chat.messages.indexOf(m)).reverse().find((x) => x.role === 'user')?.content
            : undefined
          return <MessageBubble key={m.id} message={m} source={src} onFeedback={setFeedbackMsg} />
          })}
        </div>
      </div>

      {/* 反馈弹窗（由 MessageBubble 的反馈按钮触发） */}
      {feedbackMsg && <FeedbackModalFromMessage message={feedbackMsg} onClose={() => setFeedbackMsg(null)} />}
    </div>
  )
}

// —— 页面级样式（cw- 前缀，避免与组件库类名重名）——
// .cw-stage 是唯一的滚动容器（flex:1 + min-height:0 吃满外层剩余高度，内部 overflow 才生效）；
// .cw-card 吸顶由内联 position:sticky 承担，这里只给卡内布局与外边距——
//   吸顶卡必须自带不透明底色（CARD 的 #0E1014），否则译文滚到卡下会透上来。
// .cw-card-acts 的右内边距是给固定定位的 AI 助手浮球让位，避免遮住主按钮。
// 描边/文字一律取 --lc-border-* 与 --lc-text-* 令牌（本文件进 #68 闸门扫描范围，
// 写死暗值会红）；圆角走 --lc-r-ctl，与组件库控件同档，避免「同为按钮却两种圆角」。
// 焦点环单独写 :focus-visible：纯黑底上默认 UA 焦点样式几乎看不见，必须自绘 outline。
const CW_CSS = `
.cw-root{box-sizing:border-box}
.cw-stage{flex:1;min-height:0;overflow-y:auto;overflow-x:hidden;padding:14px 6% 18px;scroll-behavior:smooth}
.cw-card{padding:16px;margin-bottom:14px;box-shadow:0 8px 24px rgba(0,0,0,.4)}
.cw-card-head{display:flex;align-items:center;gap:8px;margin-bottom:8px;flex-wrap:wrap}
.cw-card-langs{margin-top:10px;display:flex;flex-wrap:wrap;gap:8px;align-items:center}
.cw-card-acts{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:12px;padding-right:68px}
.cw-results{display:flex;flex-direction:column;gap:12px}
.cw-results .bubble-row{max-width:100%}
.cw-welcome{text-align:center;padding:28px 12px;color:var(--lc-text-3)}
.cw-icon-btn{display:inline-flex;align-items:center;justify-content:center;width:32px;height:32px;
  border:1.2px solid var(--lc-border-pill);background:transparent;color:var(--lc-text-2);border-radius:var(--lc-r-ctl);cursor:pointer;padding:0;line-height:0}
.cw-icon-btn:hover{color:var(--lc-text);border-color:var(--lc-border-done)}
.cw-icon-btn:focus-visible{outline:2px solid var(--lc-border-strong);outline-offset:2px}
.cw-search-clear{display:inline-flex;align-items:center;justify-content:center;border:0;background:none;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0}
.cw-search-clear:hover{color:var(--lc-text)}
.cw-spin{display:inline-block;width:14px;height:14px;border:2px solid rgba(231,233,234,.35);border-top-color:#E7E9EA;border-radius:50%;animation:cw-spin .7s linear infinite}
@keyframes cw-spin{to{transform:rotate(360deg)}}
@media (max-width:900px){
  /* 平板：舞台左右内边距 6%→4%（原 mobile.css 的 .chat-scroll 口径，页面级规则已删，
     必须由本组件自己下发，否则会被上面的 .cw-stage 规则盖掉） */
  .cw-stage{padding:12px 4% 14px}
}
@media (max-width:640px){
  /* 窄屏：内边距再收窄、卡内边距减半，浮球右下仍占位，故主按钮行右内边距减半 */
  .cw-stage{padding:10px 3% 12px}
  .cw-card{padding:12px}
  .cw-card-acts{padding-right:40px}
  .cw-input{min-height:96px}
}
@media (prefers-reduced-motion: reduce){
  .cw-spin{animation:none}
  .cw-stage{scroll-behavior:auto}
}
`
