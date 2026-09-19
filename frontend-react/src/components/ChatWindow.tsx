// ============================================================================
// components/ChatWindow.tsx — 前台即时翻译工作台（按 UI 图 07-translate 重建）
// 结构：离线横幅 + 余额/用量条 + 原文输入 → 语言选择 → 译文输出区（带检查点动效）。
// 业务逻辑/API 全部保留，仅重写呈现层，文案走词典（无硬编码中文）。
// 呈现层已迁至 @/ui/langcross/src 纯黑组件库（TDesign 全部移除）。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Button, Input, useToast,
  UploadIcon, SearchIcon, DownloadIcon, TrashIcon, CloseIcon,
} from '@/ui/langcross/src'
import MessageBubble from './MessageBubble'
import { FeedbackModalFromMessage } from './modals'
import { useChat } from '@/hooks/useChat'
import { myPackage, meContext } from '@/api'
import { estimateTranslation, validateTranslateFile, TRANSLATE_FILE_ACCEPT } from '@/api/translate'
import { fmtPoints } from '@/utils/points'
import type { ChatMessage } from '@/types'
import { useT, t, tpl } from '@/i18n'
import LangMultiSelect, { LangChips } from '@/components/LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'

// 数字千分位格式化，并处理 undefined/负数，用于余额与用量展示
function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(Math.max(0, Math.floor(n || 0)))
}

// 卡片样式（纯黑体系：面板 #0E1014 + 1.2px 描边 + 圆角 14）
const CARD: React.CSSProperties = {
  background: '#0E1014', border: '1.2px solid #464C58', borderRadius: 14,
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

// 默认导出组件：前台即时翻译工作台
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

  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const fileRef = useRef<HTMLInputElement>(null)

  // ---- 余额 / 用量加载 ----
  // 从 myPackage 接口读取个人余额、今日用量及企业预算额度（均为积分）
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

  // ---- 进度/消息变化自动滚底 ----
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [chat.messages])

  // ---- 输入框初始自适应高度（取代 TDesign autosize）----
  useEffect(() => { autoResize() }, [])

  // ---- 切换模式并持久化 ----
  // fast/pro 模式切换，同时写入 localStorage 以便跨会话记忆
  function setMode2(m: 'fast' | 'pro') {
    setMode(m)
    localStorage.setItem('translate_mode', m)
  }

  // F7：输入内容变化防抖预估积分消耗（后端直接给积分区间与 ≈句数，零换算）
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
  // 先置 auto 再读 scrollHeight：否则高度会被上一次赋值撑住、量不到真实内容高度；
  // 上限 220px 是工作台输入卡的可视高度预算（再高会挤掉下方译文区，超长应上传文件翻译）
  function autoResize() {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 220) + 'px'
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
    setInput('')
    const options: Record<string, unknown> = { target_langs: chat.selectedLangs, lang }
    chat.sendMessage(rawText, options)
  }

  // ---- 即时文件翻译 ----
  // 与工单通道共用后端校验口径（validateTranslateFile：扩展名白名单 + 40MB 上限），
  // 前端先行拦截给反馈，避免大文件传到一半才被后端拒；选中上一份即丢弃旧任务（一次一份）
  async function handleFilePicked(files: FileList | null) {
    if (!files || files.length === 0) return
    if (chat.isLoading) { toast({ title: t2('chat.busy'), tone: 'warn' }); return }
    const file = files[0]
    const err = validateTranslateFile(file)
    if (err) { toast({ title: err, tone: 'error' }); return }
    await chat.sendFile(file, chat.selectedLangs, '', condenseOn && condenseMax > 0 ? condenseMax : 0)
  }

  const canSend = input.trim().length > 0
  // F7：会话内搜索过滤（空串 = 全量）
  const shownMessages = searchQ.trim()
    ? chat.messages.filter((m) => (m.content || '').toLowerCase().includes(searchQ.trim().toLowerCase()))
    : chat.messages

  return (
    /* minHeight:0 必须显式给：flex 列里的滚动子项默认 min-height:auto，
       不置 0 则消息区会把容器撑高、内部 overflow 永不生效（整页滚而非区域滚） */
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 57px)', minHeight: 0, background: '#000' }}>
      <style>{CW_CSS}</style>
      {/* 离线横幅 */}
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

      {/* 余额 / 用量条 */}
      {(balance || usage || orgBudget) && (
        <div style={{ background: 'rgba(231,233,234,0.06)', color: '#9AA0AA', fontSize: 12, padding: '6px 6%', display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'center', borderBottom: '1px solid #1a1d22' }}>
          {balance && <span>{tpl('chat.balanceTokens', { n: fmtPoints(balance.points), s: fmtNum(balance.approx) })}</span>}
          {usage && <span>{tpl('chat.usedTokens', { n: fmtPoints(usage.today) })}</span>}
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
                   <button type="button" className="cw-search-clear" aria-label={t('chat.clear')} onClick={() => setSearchQ('')}>
                     <CloseIcon size={14} />
                   </button>
                 ) : null} />
        </div>
      )}

      {/* 原文输入 → 语言选择 → 译文输出 */}
      <div className="chat-scroll" ref={scrollRef} style={{ flex: 1, overflowY: 'auto', padding: '16px 6%', minHeight: 0 }}>
        {/* 输入卡：置顶吸顶，译文在下方展开 */}
        <div style={{ ...CARD, position: 'sticky', top: 0, zIndex: 5, padding: 16, marginBottom: 16, boxShadow: '0 8px 24px rgba(0,0,0,.4)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
            <span style={{ fontSize: 13, fontWeight: 600, color: '#E7E9EA', letterSpacing: '.04em' }}>{t('chat.srcLabel')}</span>
            <span style={{ fontSize: 12, color: '#878D95' }}>{t('chat.sourceAuto')}</span>
          </div>
          {/* F3：dir="auto" 让阿/法等 RTL 文本按内容方向渲染。
              用原生 textarea + 组件库 .lc-textarea 类：autoResize 需要 ref 到真实节点量 scrollHeight；
              Enter 发送 / Shift+Enter 换行（与工单页一致的肌肉记忆） */}
          <textarea
            className="lc-textarea"
            ref={inputRef}
            dir="auto"
            aria-label={t('chat.placeholder')}
            data-testid="translate-input"
            value={input}
            onChange={(e) => { setInput(e.target.value); autoResize() }}
            placeholder={t('chat.placeholder')}
            rows={2}
            style={{ width: '100%', background: '#0A0B0D', borderColor: '#5A6270' }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
                void handleSend()
              }
            }}
          />
          {/* 已选语言 chips + 目标语言选择 */}
          <div style={{ marginTop: 10, display: 'flex', flexWrap: 'wrap', gap: 8, alignItems: 'center' }}>
            <span style={{ fontSize: 12, color: '#878D95', whiteSpace: 'nowrap' }}>{t('chat.targetLangLabel')}</span>
            <div style={{ minWidth: 260, flex: 1 }}>
              <LangMultiSelect value={chat.selectedLangs} onChange={chat.setSelectedLangs} />
            </div>
          </div>
          <div style={{ marginTop: 6 }}>
        <LangChips langs={chat.selectedLangs} onRemove={chat.setSelectedLangs} />
          </div>

        {!!chat.errorMessage && (
            <div style={{ color: '#F85149', fontSize: 13, marginTop: 8 }}>{chat.errorMessage}</div>
          )}

          {/* 操作行：双模式 / 缩翻 / 上传 / 搜索 / 导出 / 清空 / 主按钮翻译 */}
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 12, flexWrap: 'wrap' }}>
            <ModeToggle value={mode} onChange={setMode2} />
            <label style={{ fontSize: 12, color: '#9AA0AA', display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
              <input type="checkbox" checked={condenseOn} onChange={(e) => setCondenseOn(e.target.checked)} /> {t('app.condense')}
            </label>
            <div style={{ width: 72, flexShrink: 0 }}>
              {condenseOn && (
                <input type="number" min={1} max={10000} value={condenseMax}
                  onChange={(e) => setCondenseMax(parseInt(e.target.value) || 0)}
                  style={{ width: '100%', boxSizing: 'border-box', height: 28, fontSize: 12, background: '#0A0B0D', border: '1.2px solid #5A6270', borderRadius: 6, padding: '0 6px', color: '#E7E9EA' }}
                  title={t('chat.s41')} />
              )}
            </div>
            {/* 弹性占位：把「附件/搜索/导出/清空 + 主按钮」整组推到行尾，窄屏 flexWrap 换行也不散开 */}
            <div style={{ flex: 1 }} />
            {/* 隐藏的原生 file input：由图标按钮触发点击。选完即清空 value，
                否则连续选同一个文件不会再触发 change（重传同一份是常态） */}
          <input ref={fileRef} type="file" accept={TRANSLATE_FILE_ACCEPT} style={{ display: 'none' }}
                 onChange={(e) => { void handleFilePicked(e.target.files); e.currentTarget.value = '' }} />
            {/* 纯图标按钮：迁移后按钮内不再有文字，title 给鼠标悬浮、aria-label 给读屏，二者缺一不可 */}
            <button type="button" className="cw-icon-btn" title={t('chat.attachFile')} aria-label={t('chat.attachFile')}
                    onClick={() => fileRef.current?.click()}>
              <UploadIcon size={16} />
            </button>
            <button type="button" className="cw-icon-btn" title={t('chat.searchPh')} onClick={() => { setSearchOpen((v) => !v); setSearchQ('') }}>
              <SearchIcon size={16} />
            </button>
            <button type="button" className="cw-icon-btn" title={t('chat.exportMd')} onClick={exportChat}>
              <DownloadIcon size={16} />
            </button>
            <button type="button" className="cw-icon-btn" title={t('chat.clearChat')} aria-label={t('chat.clearChat')} onClick={() => { chat.clearMessages() }}>
              <TrashIcon size={16} />
            </button>

          {chat.isLoading ? (
              <Button variant="primary" icon={<StopGlyph />} onClick={handleStop}>{t2('chat.stop')}</Button>
          ) : (
              <Button variant="primary" disabled={!canSend} onClick={() => void handleSend()}>{t('chat.translate')}</Button>
            )}
          </div>
        </div>

        {/* 空状态：欢迎语 */}
        {!chat.messages.length && !chat.isLoading && (
          <div style={{ textAlign: 'center', padding: '56px 12px', color: '#878D95' }}>
            <div style={{ fontSize: 15, color: '#9AA0AA', marginBottom: 8 }}>{t('chat.welcome')}</div>
            <div style={{ fontSize: 13, maxWidth: 520, margin: '0 auto', lineHeight: 1.8 }}>
              {t2('chat.welcomeSub')}
            </div>
          </div>
        )}

        {/* 消息流：assistant 气泡的 source=其上一条 user 消息（上下文展示） */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
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
const CW_CSS = `
.cw-icon-btn{display:inline-flex;align-items:center;justify-content:center;width:32px;height:32px;
  border:1.2px solid var(--lc-border-pill);background:transparent;color:var(--lc-text-2);border-radius:var(--lc-r-ctl);cursor:pointer;padding:0;line-height:0}
.cw-icon-btn:hover{color:var(--lc-text);border-color:var(--lc-border-done)}
.cw-icon-btn:focus-visible{outline:2px solid var(--lc-border-strong);outline-offset:2px}
.cw-search-clear{display:inline-flex;align-items:center;justify-content:center;border:0;background:none;color:var(--lc-text-3);cursor:pointer;padding:4px;line-height:0}
.cw-search-clear:hover{color:var(--lc-text)}
.cw-spin{display:inline-block;width:14px;height:14px;border:2px solid rgba(231,233,234,.35);border-top-color:#E7E9EA;border-radius:50%;animation:cw-spin .7s linear infinite}
@keyframes cw-spin{to{transform:rotate(360deg)}}
@media (prefers-reduced-motion: reduce){
  .cw-spin{animation:none}
}
`
