// ============================================================================
// components/ChatWindow.tsx — 前台工作台（Vue ChatWindow.vue 等价实现）
// 能力：欢迎示例(自动发送)/离线横幅+重试、消息流(自动滚底)、源语言选择、KB/其他/更多
//       语言多选、自定义语言前缀、文件上传翻译(完整 accept)、双模式(pro/fast)持久化、
//       余额/用量展示、停止生成、清空、反馈弹窗。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Input, Textarea } from 'tdesign-react'
import { StopCircleIcon, ClearIcon } from 'tdesign-icons-react'
import { MessagePlugin } from 'tdesign-react'
import MessageBubble from './MessageBubble'
import { FeedbackModalFromMessage } from './modals'
import { useChat } from '@/hooks/useChat'
import { myPackage, meContext } from '@/api'
import { estimateTranslation } from '@/api/translate' // ★ F7：翻译前消耗预估
import { fmtPoints } from '@/utils/points'
import { sentenceRateOf, approxSentencesOf } from '@/lib/quotaCalc' // ★ F11：换算抽纯 // ★ E14：删除死导入 request（无调用点）
import type { ChatMessage } from '@/types'
import { useT, t, tpl } from '@/i18n'
import LangMultiSelect, { LangChips } from '@/components/LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'

// ============ 本文件职责中文说明 ============
// 前台工作台（聊天主界面）：消息流、语言选择、文件翻译、双模式与反馈。
// ========================================

// ★ E14：LangItem / SOURCE_LANG_OPTIONS / _LANG_NAME_TO_CODE 死定义移除（语言下拉统一走 LangMultiSelect 数据源）

// ★ 源语言选项（互译方向；auto=自动检测）——用于语言面板顶部的"源语言"选择



// ★ 语言名→代码的本地映射（常见语言中文名/英文名→ISO代码）——用于自定义语言输入解析

// 数字千分位格式化，并处理 undefined/负数，用于余额与用量展示
function fmtNum(n: number): string {
  return new Intl.NumberFormat().format(Math.max(0, Math.floor(n || 0)))
}

// 默认导出组件：前台翻译工作台，承载消息流、语言选择、双模式与余额展示（等价 Vue ChatWindow.vue）
export default function ChatWindow() {
  const [lang, t2] = useT()
  const chat = useChat()
  const [input, setInput] = useState('')
  const [feedbackMsg, setFeedbackMsg] = useState<ChatMessage | null>(null)

  // ★ 双模式（fast/pro）持久化
  const [mode, setMode] = useState<'fast' | 'pro'>(
    (localStorage.getItem('translate_mode') as 'fast' | 'pro') || 'pro',
  )

  // ★ 缩翻（任务7）：勾选+最长字符限制（0=未启用）
  const [condenseOn, setCondenseOn] = useState(false)
  // ★ F7：输入预估（防抖 600ms）/ 会话搜索 / 导出
  const [estimate, setEstimate] = useState<{ min: number; max: number; low: boolean } | null>(null)
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQ, setSearchQ] = useState('')
  const [condenseMax, setCondenseMax] = useState(200)

  // ★ 余额 / 用量
  const [balance, setBalance] = useState<{ tokens: number; approx: number } | null>(null)
  const [usage, setUsage] = useState<{ today: number; todaySentences: number } | null>(null)
  const [orgBudget, setOrgBudget] = useState<{ limit: number; used: number; name: string } | null>(null)

  const scrollRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)


  // ---- 余额 / 用量加载 ----
  // 从 myPackage 接口读取个人余额、今日用量及企业预算额度
  const loadBalance = useCallback(async () => {
    try {
      const r: any = await myPackage()
      if (r && r.success) {
        if (typeof r.balance_tokens === 'number') {
          setBalance({
            tokens: r.balance_tokens,
            approx: r.balance_sentences_approx ?? approxSentencesOf(r.balance_tokens),
          })
        }
        const today = typeof r.tokens_used_today === 'number' ? r.tokens_used_today : null
        if (today !== null) {
          // ★ 修复（2026-09-02 前端契约审计）：后端 /api/me/package 无 estimate_rate 字段。
          //   改用余额行「可用 token ÷ ≈句数」反推实际换算率（无余额时兜底 500 句/token）。
          const rate = sentenceRateOf(r.balance_tokens, r.balance_sentences_approx)
          setUsage({ today, todaySentences: Math.floor(today / rate) })
        }
        if (r.org_budget && r.org_budget.limit > 0) {
          setOrgBudget({ limit: r.org_budget.limit, used: r.org_budget.used_this_month, name: r.org_budget.name })
        } else {
          setOrgBudget(null)
        }
      }
    } catch { setBalance(null) }
  }, [])


  // 组件挂载时初次加载余额与用户上下文
  useEffect(() => {
    void loadBalance()
    void meContext().catch(() => { /* 会话有效性由请求层兜底（★ E14：原 loadMe 死变量包装移除 */ })
  }, [loadBalance])

  // 每轮翻译结束（消息数变化）后刷新剩余量
  useEffect(() => {
    if (chat.messages.length) void loadBalance()
  }, [chat.messages.length, loadBalance])

  // ---- 进度/消息变化自动滚底 ----
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [chat.messages])

  // ---- 切换模式并持久化 ----
  // fast/pro 模式切换，同时写入 localStorage 以便跨会话记忆
  function setMode2(m: 'fast' | 'pro') {
    setMode(m)
    localStorage.setItem('translate_mode', m)
  }

  // ★ F7：输入内容变化防抖预估 token 消耗；余额低于上限给出低额提示
  useEffect(() => {
    const text = input.trim()
    if (!text || chat.selectedLangs.length === 0) { setEstimate(null); return }
    let alive = true
    const timer = setTimeout(async () => {
      const r = await estimateTranslation(text, chat.selectedLangs, mode)
      if (!alive || !r) return
      setEstimate({ min: r.tokens_min, max: r.tokens_max, low: r.balance_tokens < r.tokens_max })
    }, 600)
    return () => { alive = false; clearTimeout(timer) }
  }, [input, chat.selectedLangs, mode])

  // ★ F7：快捷键 Cmd/Ctrl+K 聚焦搜索框
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setSearchOpen(true)
        setTimeout(() => document.querySelector<HTMLInputElement>('.chat-search input')?.focus(), 50)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // ★ F7：导出当前会话为 Markdown（本地生成，无后端依赖）
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
    setTimeout(() => URL.revokeObjectURL(a.href), 5000)
  }

  const shownMessages = searchQ.trim()
    ? chat.messages.filter((m) => (m.content || '').toLowerCase().includes(searchQ.trim().toLowerCase()))
    : chat.messages

  // ---- textarea 自动高度 ----
  // 根据内容自动调整输入框高度（最大 120px），避免长文本溢出
  function autoResize() {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 120) + 'px'
  }

  // 停止生成：中断当前翻译流；已翻译部分消耗的 token 不会退还（产品规则）
  function handleStop() {
    chat.stopGeneration()
    void MessagePlugin.info(t2('chat.stopTokenNote'))
  }

  // ---- 发送 ----
  // 走文本翻译；目标语言直接取自聊天全局 selectedLangs
  // ★ 缩翻（任务7）：勾选后把最长字符限制透传后端（0=未启用）
  async function handleSend() {
    const rawText = input.trim()
    if (!rawText) return
    setInput('')

    const options: Record<string, unknown> = { target_langs: chat.selectedLangs, lang }
    if (condenseOn && condenseMax > 0) options.max_length = condenseMax
    chat.sendMessage(rawText, options)
  }

  const canSend = input.trim().length > 0

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 57px)' }}>
      {/* 离线横幅 */}
      {!chat.isBackendOnline && (
        <div style={{ background: '#fff3e0', borderBottom: '1px solid #ffe0b2', padding: '8px 6%', display: 'flex', gap: 10, alignItems: 'center' }}>
          <span style={{ fontSize: 13 }}>
            {chat.isBackendLoading ? t2('chat.checking') : t2('chat.offline')}
          </span>
          {!chat.isBackendLoading && (
            <Button size="small" variant="outline" loading={chat.isBackendChecking} onClick={() => void chat.retryHealth()}>
              {t2('chat.retry')}
            </Button>
          )}
        </div>
      )}

      {/* 余额 / 用量条 */}
      {(balance || usage || orgBudget) && (
        <div style={{ background: '#e8f0fe', color: 'var(--td-brand-color, #2f47f5)', fontSize: 12, padding: '4px 6%', display: 'flex', gap: 14, flexWrap: 'wrap', alignItems: 'center' }}>
          {balance && <span>{tpl('chat.balanceFmt', { points: fmtPoints(balance.tokens), sents: fmtNum(balance.approx) })}</span>}
          {usage && <span>{tpl('chat.todayFmt', { points: fmtPoints(usage.today), sents: fmtNum(usage.todaySentences) })}</span>}
          {orgBudget && <span>{tpl('chat.orgBudgetFmt', { name: orgBudget.name, used: fmtPoints(orgBudget.used), limit: fmtPoints(orgBudget.limit) })}</span>}
          {estimate && (
            <span style={estimate.low ? { color: '#c66900', fontWeight: 600 } : undefined}>
              {tpl('chat.estFmt', { min: fmtPoints(estimate.min), max: fmtPoints(estimate.max) })}
              {estimate.low && ` · ${t('chat.estLow')}`}
            </span>
          )}
        </div>
      )}

      {/* ★ F7：会话内搜索（Cmd/Ctrl+K） */}
      {searchOpen && (
        <div className="chat-search" style={{ padding: '6px 6% 0' }}>
          <Input size="small" clearable autofocus aria-label={t('chat.searchPh')} placeholder={t('chat.searchPh')} value={searchQ} onChange={(v: string) => setSearchQ(v)} />
        </div>
      )}

      {/* 消息滚动区 */}
      <div className="chat-scroll" ref={scrollRef}>
        {chat.messages.length === 0 && (
          <div style={{ textAlign: 'center', marginTop: 60 }}>
            <div style={{ fontSize: 26, fontWeight: 800, color: 'var(--td-brand-color-active, #1f33d6)' }}>{t2('chat.welcome')}</div>
            <div style={{ fontSize: 14, color: '#5f6b7a', marginTop: 10, maxWidth: 480, margin: '10px auto 0' }}>
              {t2('chat.welcomeSub')}
            </div>
          </div>
        )}
        {shownMessages.map((m, _i) => {
          const src = m.role === 'assistant'
            ? [...chat.messages].slice(0, chat.messages.indexOf(m)).reverse().find((x) => x.role === 'user')?.content
            : undefined
          return <MessageBubble key={m.id} message={m} source={src} onFeedback={setFeedbackMsg} />
        })}
      </div>

      {/* 输入区 */}
      <div className="chat-inputbar">
        {/* 已选语言 chip 行（任务⑤：选中结果唯一展示位；组件化与工单页共用） */}
        <LangChips langs={chat.selectedLangs} onRemove={chat.setSelectedLangs} />

        {!!chat.errorMessage && (
          <div style={{ color: '#c62828', fontSize: 13 }}>{chat.errorMessage}</div>
        )}

        <div className="chat-input-row" style={{ display: 'flex', gap: 8, alignItems: 'flex-end' }}>
          {/* 上传 + 语言选择 */}
          <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexShrink: 1, minWidth: 0 }}>
            <div className="lang-multi-sel" style={{ minWidth: 0, flex: '1 1 200px' }}>
              <LangMultiSelect value={chat.selectedLangs} onChange={chat.setSelectedLangs} />
            </div>
          </div>

          <Textarea
            dir="auto" // ★ F3：阿/法等 RTL 文本按内容方向渲染
            aria-label={t2('chat.placeholder')}
            data-testid="translate-input"
            autosize={{ minRows: 1, maxRows: 5 }}
            value={input}
            onChange={(v) => { setInput(v); autoResize() }}
            placeholder={t2('chat.placeholder')}
            onKeydown={(_v, ctx) => {
              if (ctx.e.key === 'Enter' && !ctx.e.shiftKey) {
                ctx.e.preventDefault()
                void handleSend()
              }
            }}
            style={{ flex: 1 }}
          />

          {/* 双模式切换（与翻译工单共用 ModeToggle，顺序与工单页保持一致：左快速/右专业） */}
          <ModeToggle value={mode} onChange={setMode2} fastFirst />
          {/* ★ 缩翻（任务7）：勾选并输入最长字符限制，提示模型精简输出。
              预留定宽槽位（72px）——勾选只显隐输入框、不改变行宽，避免模式/清空/发送按钮位置跳动 */}
          <div style={{ display: 'flex', alignItems: 'center', gap: 4, flexShrink: 0 }}>
            <label style={{ fontSize: 12, color: '#555', display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
              <input type="checkbox" checked={condenseOn} onChange={(e) => setCondenseOn(e.target.checked)} /> {t('app.condense')}
            </label>
            <div style={{ width: 72, flexShrink: 0 }}>
              {condenseOn && (
                <input type="number" min={1} max={10000} value={condenseMax}
                  onChange={(e) => setCondenseMax(parseInt(e.target.value) || 0)}
                  style={{ width: '100%', boxSizing: 'border-box', height: 28, fontSize: 12, border: '1px solid #d8dee6', borderRadius: 6, padding: '0 6px' }}
                  title={t('chat.s41')} />
              )}
            </div>
          </div>
          <Button variant="text" theme="default" size="medium" title={t('chat.searchPh')} onClick={() => { setSearchOpen((v) => !v); setSearchQ('') }}>🔍</Button>
          <Button variant="text" theme="default" size="medium" title={t('chat.exportMd')} onClick={exportChat}>⬇</Button>
          <Button variant="text" theme="default" size="medium" icon={<ClearIcon />}
                  onClick={() => { chat.clearMessages() }}>
            {t2('chat.clearChat')}
          </Button>

          {chat.isLoading ? (
            <Button theme="warning" size="medium" icon={<StopCircleIcon />} onClick={handleStop}>{t2('chat.stop')}</Button>
          ) : (
            <Button theme="primary" size="medium" disabled={!canSend} onClick={() => void handleSend()}>{t2('chat.send')}</Button>
          )}
        </div>
      </div>

      {feedbackMsg && (
        <FeedbackModalFromMessage message={feedbackMsg} onClose={() => setFeedbackMsg(null)} />
      )}
    </div>
  )
}
