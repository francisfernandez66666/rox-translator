// ============================================================================
// components/TicketsPage.tsx — 翻译工单页（Vue TicketsPage 等价实现）
// 能力：文本/多文件建单（fast/pro）、列表 Table、进度气泡（开气泡期间 3s 详情轮询 +
//       5s 列表轮询）、取消/删除/下载（blob 带鉴权）、已完成工单反馈。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import { Button, DataTable, Link, Switch } from '@/ui/langcross/src'
import { toastSuccess, toastError } from '@/lib/toastBus'
import {
  myTickets, ticketCreate, ticketCreateFile, ticketRun, ticketDetail,
  ticketDownload, ticketDelete, ticketCancel, createFeedback,
} from '@/api'
import { runGuarded } from '@/lib/runGuarded'
import { confirmDialog } from '@/components/uiDialogs'
import type { Ticket, TicketResp, TicketQuality, QAReportIssue } from '@/api/tickets'
import { TRANSLATE_FILE_ACCEPT, TEXT_DELIVERY_ACCEPT, validateTranslateFile } from '@/api/translate'
import LangMultiSelect, { LangChips } from './LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'
import { t, tpl, useLang } from '@/i18n'
import { langLabel } from '@/lib/langNames'
import { Icon } from '@/ui/langcross/src'

// ============ 本文件职责中文说明 ============
// 翻译工单页面：建单、列表、进度、取消/删除/下载与反馈。
// ========================================

// 步骤 key → 用户友好名称（与 Vue 对齐）——用于进度气泡中展示每个阶段中文名
const STEP_KEYS: Record<string, string> = {
  kb_match: 'tk.stepKm', ai_initial: 'tk.stepAi', evals_initial: 'tk.stepEvalI',
  review: 'tk.stepReview', evals_review: 'tk.stepEvalR', gate: 'tk.stepGate',
  culture_gate: 'tk.stepCulture', qa: 'tk.stepQa', file_extract: 'tk.stepExtract',
  file_translate: 'tk.stepTranslate', approval: 'tk.stepApproval', feedback: 'tk.stepFeedback',
  file_qa: 'tk.stepQa', file_writeback: 'tk.stepWriteback', writeback: 'tk.stepWriteback',
  // ★ P0-5（2026-09-18）：工单模式旁路的审计留痕步骤，进度气泡需显示中文标签而非裸 key
  mode_override: 'tk.stepModeOverride',
}
// ★ F2：步骤名经 STEP_KEYS→词典取词（渲染期调用 t，随语言切换生效）
const stepName = (step: string): string => { const k = STEP_KEYS[step]; return k ? t(k) : step }

// 步骤锚点阶梯（百分比）——各阶段完成/执行时对应的整体进度基准值
const STEP_WEIGHT: Record<string, number> = {
  upload: 20, file_extract: 20, extract: 20,
  translate: 40, file_translate: 40, init_translation: 40,
  proofread: 60, qa: 60, file_qa: 60, quality_check: 60,
  writeback: 80, file_writeback: 80, package: 80,
}

// 字节数格式化为 KB/MB，用于展示附件大小
const fmtKB = (bytes: number): string => {
  const kb = bytes / 1024
  return kb >= 1024 ? (kb / 1024).toFixed(1) + 'MB' : kb.toFixed(kb % 1 ? 1 : 0) + 'KB'
}

// ============ ★ 改造 4/5（2026-09-17）：质检透出 ============
// 背景：确定性质检（qa.Report）此前只落 xlsx 对照表「QA」列，界面零透出；评估分落 payload 后
// 即「死数据」。本次把两者透出到列表徽标 + 详情抽屉，让付费用户直接看见质检投入。

// 质检规则名 → 词典键（未知规则回退原始规则名，避免新增规则时丢展示）
const QA_RULE_KEYS: Record<string, string> = {
  empty: 'tk.qaRuleEmpty', same: 'tk.qaRuleSame', number: 'tk.qaRuleNumber',
  placeholder: 'tk.qaRulePlaceholder', length: 'tk.qaRuleLength', punctuation: 'tk.qaRulePunctuation',
}
// 渲染期取词（随语言切换生效）
const qaRuleLabel = (rule: string): string => { const k = QA_RULE_KEYS[rule]; return k ? t(k) : rule }

// 徽标基础样式（error 红 / warning 黄 / 存疑橙，均带浅底圆角）
const badgeStyle = (bg: string, fg: string): React.CSSProperties => ({
  display: 'inline-flex', alignItems: 'center', gap: 3, background: bg, color: fg,
  border: `1px solid ${fg}33`, borderRadius: 10, padding: '1px 7px', fontSize: 11.5, whiteSpace: 'nowrap',
})

// QualityBadges 列表行质检徽标（★ 改造 5）：
//   qa_errors>0 → 红「N 项错误」；仅 warnings → 黄「N 项提示」；quality_flagged=1 → 橙「质检存疑」。
// 三者皆无返回 null，保持无质检工单行干净。
function QualityBadges({ row }: { row: Ticket }) {
  const errs = row.qa_errors || 0
  const warns = row.qa_warnings || 0
  const flagged = row.quality_flagged === 1
  if (!errs && !warns && !flagged) return null
  return (
    <span style={{ display: 'inline-flex', gap: 4, flexWrap: 'wrap' }}>
      {errs > 0 && (
        <span style={badgeStyle('#fdecea', '#c5221f')} title={t('tk.qaErrorNote')}>
          ● {tpl('tk.qaErrors', { n: errs })}
        </span>
      )}
      {errs === 0 && warns > 0 && (
        <span style={badgeStyle('#fff6e0', '#b26a00')}>● {tpl('tk.qaWarnings', { n: warns })}</span>
      )}
      {flagged && (
        <span style={badgeStyle('#fff1e6', '#b45309')} title={t('tk.qaFlaggedTip')}>
          <Icon n="alert" /> {t('tk.qaFlagged')}
        </span>
      )}
    </span>
  )
}

// QualityBlock 详情抽屉「质检报告」区块（★ 改造 5）：
//   Pass/Errors/Warnings 汇总行 + Issues 明细（语言/规则/级别/说明）+ 各语言评估分。
// 无任何质检数据（草稿/未跑到质检步骤）返回 null，不占位、不误导。
function QualityBlock({ q, lang, flagged }: { q?: TicketQuality; lang: 'zh' | 'en'; flagged?: boolean }) {
  const scoreKeysAll = q ? Array.from(new Set([...Object.keys(q.eval_scores || {}), ...Object.keys(q.review_eval_scores || {})])) : []
  // 无 QA 报告、无评估分且未被列标记存疑 → 不渲染（草稿/未跑到质检步骤，不占位）
  if (!q?.qa_report && !scoreKeysAll.length && !flagged) return null
  const rep = q?.qa_report
  const evalScores = q?.eval_scores || {}
  const reviewScores = q?.review_eval_scores || {}
  const flaggedLangs = q?.quality_flagged_langs || []
  const scoreKeys = scoreKeysAll

  // 语言 → 「初翻 87.5 · 校对 90.2」；不达标语言加前缀
  const fmtScore = (lc: string): string => {
    const parts: string[] = []
    if (typeof evalScores[lc] === 'number') parts.push(`${t('tk.evalInitial')} ${evalScores[lc].toFixed(1)}`)
    if (typeof reviewScores[lc] === 'number') parts.push(`${t('tk.evalReview')} ${reviewScores[lc].toFixed(1)}`)
    return parts.join(' · ')
  }

  return (
    <div style={{ marginTop: 12, borderTop: '1px solid #2A2F3A', paddingTop: 10 }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 6 }}>{t('tk.qaTitle')}</div>

      {rep && (
        <>
          <div style={{ fontSize: 12, display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <span style={badgeStyle(rep.pass ? 'rgba(231,233,234,0.10)' : '#fdecea', rep.pass ? '#E7E9EA' : '#c5221f')}>
              {rep.pass ? `${t('tk.qaPass')}` : `${t('tk.qaFail')}`}
            </span>
            <span style={{ color: '#555' }}>{tpl('tk.qaSummary', { errors: rep.errors, warnings: rep.warnings })}</span>
          </div>
          {rep.errors > 0 && (
            <div style={{ fontSize: 11.5, color: 'var(--lc-danger)', marginTop: 6, lineHeight: 1.5 }}><Icon n="alert" /> {t('tk.qaErrorNote')}</div>
          )}
          {rep.issues && rep.issues.length > 0 ? (
            <div style={{ marginTop: 8, maxHeight: 180, overflowY: 'auto', border: '1.2px solid #2A2F3A', borderRadius: 6 }}>
              {rep.issues.map((it: QAReportIssue, i: number) => (
                <div key={`${it.lang}-${it.rule}-${i}`}
                     style={{ display: 'flex', gap: 6, alignItems: 'flex-start', padding: '5px 8px', fontSize: 11.5, borderTop: i ? '1px solid #2A2F3A' : 'none' }}>
                  <span style={badgeStyle('rgba(231,233,234,0.16)', '#9AA0AA')}>{langLabel(it.lang, lang)}</span>
                  <span style={badgeStyle('rgba(231,233,234,0.16)', '#9AA0AA')}>{qaRuleLabel(it.rule)}</span>
                  <span style={badgeStyle(it.level === 'error' ? '#fdecea' : '#fff6e0', it.level === 'error' ? '#c5221f' : '#b26a00')}>
                    {it.level === 'error' ? t('tk.qaLevelError') : t('tk.qaLevelWarning')}
                  </span>
                  <span style={{ flex: 1, color: '#555', wordBreak: 'break-word' }}>{it.detail}</span>
                </div>
              ))}
            </div>
          ) : (
            <div style={{ fontSize: 11.5, color: '#E7E9EA', marginTop: 6 }}>{t('tk.qaNoIssues')}</div>
          )}
        </>
      )}

      {scoreKeys.length > 0 && (
        <div style={{ marginTop: 10 }}>
          <div style={{ fontSize: 12, color: '#555', marginBottom: 4 }}>{t('tk.evalScores')}</div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
            {scoreKeys.map((lc) => (
              <div key={lc} style={{ fontSize: 11.5, display: 'flex', gap: 6, alignItems: 'center' }}>
                <span style={badgeStyle('rgba(231,233,234,0.16)', '#9AA0AA')}>
                  {langLabel(lc, lang)}
                </span>
                <span style={{ color: flaggedLangs.includes(lc) ? '#b45309' : '#555' }}>{fmtScore(lc)}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {(flaggedLangs.length > 0 || flagged) && (
        <div style={{ fontSize: 11.5, color: 'var(--lc-warn, #D29922)', marginTop: 8, lineHeight: 1.5 }}><Icon n="alert" /> {t('tk.qaFlaggedTip')}</div>
      )}
    </div>
  )
}

// 默认导出组件：翻译工单页，提供建单、工单列表、进度气泡与反馈（等价 Vue TicketsPage）
export default function TicketsPage() {
  const lang = useLang()
  const [mode, setMode] = useState<'text' | 'file'>('text')
  const [qualityMode, setQualityMode] = useState<string>(localStorage.getItem('translate_mode') || 'fast')
  const [title, setTitle] = useState('')
  const [text, setText] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [langs, setLangs] = useState<string[]>(['en'])
  const [creating, setCreating] = useState(false)
  const [imageHeavyHint, setImageHeavyHint] = useState(false)
  // ★ 缩翻（任务7）：勾选后输入最长字符限制，提示模型精简输出
  const [condenseOn, setCondenseOn] = useState(false)
  const [condenseMax, setCondenseMax] = useState(200)
  // ★ 工单双模式（2026-09-13）：交付方式 restore 还原文件模式（默认）/ text 纯文案模式
  const [delivery, setDelivery] = useState<string>(localStorage.getItem('ticket_delivery') || 'restore')

  const [tickets, setTickets] = useState<Ticket[]>([])
  const [detail, setDetail] = useState<TicketResp | null>(null)
  const [downloadingId, setDownloadingId] = useState<number | null>(null)

  // 反馈弹窗目标（已完成工单）
  const [feedbackTarget, setFeedbackTarget] = useState<{ type: 'ticket'; ticket_id: number; mode: string } | null>(null)

  // 详情轮询定时器句柄（列表轮询改用 ref + 一次性 effect，不再持有句柄）
  const detailTimer = useRef<number | null>(null)

  // 目标语言逗号拼接串（空时回退 en），随建单请求提交
  const langsJoined = langs.length ? langs.join(',') : 'en'

  // 拉取我的工单列表
  const load = useCallback(async () => {
    try {
      const r = await myTickets()
      if (r.success) setTickets(r.tickets || [])
    } catch { /* 忽略 */ }
  }, [])

  // ★ 保持最新 tickets 供定时器读取（不随 load 重建，避免 effect 循环触发高频请求）
  const ticketsRef = useRef(tickets)
  ticketsRef.current = tickets

  // 列表轮询：存在排队/进行中工单且页面可见时每 5s 刷新
  // 仅在页面可见且存在活跃工单时才刷新列表
  useEffect(() => {
    void load()
    const iv = window.setInterval(() => {
      if (document.hidden) return
      if (ticketsRef.current.some((x) => ['queued', 'in_progress'].includes(x.status))) void load()
    }, 5000)
    return () => { window.clearInterval(iv) }
  }, [load])

  // 详情轮询：打开气泡期间每 3s 刷新；工单完成自动停止
  // ★ E6：轮询回调经 ref 读取当前工单 id——旧实现闭包捕获 startDetailPoll 创建时刻的
  //   detail（打开详情时往往还是 null），轮询永远空转不刷新。
  const detailIdRef = useRef<number | null>(null)
  useEffect(() => { detailIdRef.current = detail?.ticket?.id ?? null }, [detail])
  const startDetailPoll = useCallback(() => {
    stopDetailPoll()
    detailTimer.current = window.setInterval(async () => {
      const id = detailIdRef.current
      if (!id || document.hidden) return
      const r = await ticketDetail(id)
      if (r.success) setDetail(r)
      const stt = r.ticket?.status
      if (stt && !['queued', 'in_progress'].includes(stt)) stopDetailPoll()
    }, 3000)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 停止详情轮询并清理定时器
  const stopDetailPoll = useCallback(() => {
    if (detailTimer.current) { window.clearInterval(detailTimer.current); detailTimer.current = null }
  }, [])

  useEffect(() => () => stopDetailPoll(), [stopDetailPoll])

  // 进度百分比：优先 detail.progress，否则按步骤锚点
  // 综合各阶段状态与文件回写进度计算整体百分比
  const ticketProgress = (): number | null => {
    if (!detail) return null
    if (typeof (detail as any).progress === 'number') return (detail as any).progress
    const st = (detail.states as any[]) || []
    const fs = (detail as any).files || []
    const tk = (detail.ticket as any) || {}
    if (tk.status === 'completed') return 100
    let pct = tk.status === 'queued' ? 10 : 5
    for (const x of st) {
      const w = STEP_WEIGHT[x.step]
      if (!w) continue
      if (x.status === 'success' || x.status === 'skipped') pct = Math.max(pct, w)
      // 进行中的步骤按权重减 10 当作当前进度（比上一完成步骤更靠前，最低 10%），体现「正在做」
      else if (x.status === 'running') pct = Math.max(pct, w - 10 > 10 ? w - 10 : w)
    }
    if (fs.some((f: any) => f.result_path || f.error)) pct = Math.max(pct, 80)
    return Math.min(100, Math.max(0, pct))
  }

  // 当前正在执行的步骤名
  // 返回进度气泡标题处展示的当前/已完成步骤文案
  const currentStepLabel = (): string => {
    const st = (detail?.states as any[]) || []
    const running = st.find((x) => x.status === 'running')
    if (running) return stepName(running.step)
    const fs = (detail as any)?.files || []
    if (fs.length) {
      const done = fs.filter((f: any) => f.result_path || f.error).length
      return tpl('tk.filesDone', { done, total: fs.length })
    }
    return ''
  }

  // 状态中文标签（使用 tk.st* i18n）
  // 将工单状态键映射为界面中文展示
  const statusLabel = (s: string): string => {
    switch (s) {
      case 'queued': return t('tk.stQueued')
      case 'in_progress': return t('tk.stRunning')
      case 'pending_approval': return t('tk.stPending')
      case 'approved': return t('tk.stApproved')
      case 'rejected': return t('tk.stRejected')
      case 'completed': return t('tk.stCompleted')
      case 'cancelled': return t('tk.stCancelled')
      default: return s || '—'
    }
  }

  // 处理多文件选择：按名称+大小去重后并入已选列表
  function onFileSelect(e: React.ChangeEvent<HTMLInputElement>) {
    const list = Array.from(e.target.files || [])
    if (!list.length) return
    const exist = new Set(files.map((f) => f.name + f.size))
    const next = [...files]
    for (const f of list) {
      const reason = validateTranslateFile(f, delivery === 'text')
      if (reason) { toastError(reason); continue }
      if (!exist.has(f.name + f.size)) { next.push(f); exist.add(f.name + f.size) }
    }
    setFiles(next)
    e.target.value = ''
  }
  // 按索引移除一个待上传文件
  function removeFileAt(i: number) { setFiles((prev) => prev.filter((_, idx) => idx !== i)) }

  // 创建工单：文本模式或文件模式二选一，调用对应建单接口后刷新列表
  async function create() {
    if (creating) return
    setCreating(true)
    try {
      let r: TicketResp
      const maxLength = condenseOn && condenseMax > 0 ? condenseMax : 0
      if (mode === 'text') {
        if (!text.trim()) return
        r = await ticketCreate({
          title: title.trim() || t('tk.defaultTitle'),
          source_text: text,
          target_langs: langsJoined,
          mode: qualityMode,
          max_length: maxLength,
        })
      } else {
        if (!files.length) return
        r = await ticketCreateFile([...files], { title: title.trim(), target_langs: langsJoined, mode: qualityMode, max_length: maxLength, delivery })
      }
      if (!r.success) { toastError(r.message || t('tk.createFail')); setCreating(false); return }
      setTitle(''); setText(''); setFiles([])
      void load()
    } catch (e: any) {
      toastError(e?.message || t('tk.createFail'))
    } finally { setCreating(false) }
  }

  // 运行草稿态工单
    // ★ E10：网络/超时异常同样可见（旧实现仅业务失败提示，异常被 unhandled rejection 吞）
  async function run(row: Ticket) {
    const r = await runGuarded(() => ticketRun(row.id), { fallback: t('tk.runFail') })
    if (!r) return
    if (!r.success) { toastError(r.message || t('tk.runFail')); return }
    void load()
  }
  // 取消排队/进行中的工单（需确认；确认按钮置文案「确认取消」避免与弹窗取消同级歧义）
  async function cancelTicket(row: Ticket) {
    if (!(await confirmDialog({ body: tpl('tk.cancelConfirm', { no: row.ticket_no || row.id }), confirmText: t('tk.confirmCancelAction') }))) return
    const r = await runGuarded(() => ticketCancel(row.id), { fallback: t('tk.opFail') })
    if (!r) return
    if (!r.success) { toastError(r.message || t('tk.opFail')); return }
    void load()
  }
  // 删除已完成/已取消的工单（需确认）
  async function deleteTicket(row: Ticket) {
    if (!(await confirmDialog({ body: tpl('tk.deleteConfirm', { no: row.ticket_no }), confirmText: t('tk.confirmDeleteAction') }))) return
    const r = await runGuarded(() => ticketDelete(row.id), { fallback: t('tk.opFail') })
    if (!r) return
    if (!r.success) { toastError(r.message || t('tk.opFail')); return }
    void load()
  }
  // 下载工单结果（带防重入标记）
  async function download(row: Ticket) {
    if (downloadingId !== null) return
    setDownloadingId(row.id)
    try { await ticketDownload(row.id) } catch (e: any) { toastError(e?.message || t('tk.downloadFail')) }
    finally { setDownloadingId(null) }
  }
  // ★ 工单双模式（2026-09-13）：仅下载译文纯文案（.md，不取还原产物）
  async function downloadText(row: Ticket) {
    if (downloadingId !== null) return
    setDownloadingId(row.id)
    try { await ticketDownload(row.id, { fmt: 'text' }) } catch (e: any) { toastError(e?.message || t('tk.downloadFail')) }
    finally { setDownloadingId(null) }
  }

  // 展开/收起步骤进度气泡
  // 点击行展开详情并启动轮询，再次点击或同一条已展开则收起
  async function toggleDetail(row: Ticket) {
    if (detail && detail.ticket?.id === row.id) { setDetail(null); stopDetailPoll(); return }
    const r = await ticketDetail(row.id)
    if (r.success) { setDetail(r); detailIdRef.current = r.ticket?.id ?? null; startDetailPoll() }
  }

  // 打开针对指定工单的反馈弹窗（携带工单 ID 与翻译模式）
  // 设置反馈目标为工单类型，触发反馈弹窗
  function openFeedback(tk: Ticket) {
    setFeedbackTarget({ type: 'ticket', ticket_id: tk.id, mode: (tk as any).mode || 'pro' })
  }

  // 当前工单的整体进度百分比、当前步骤文案与状态列表（用于进度气泡展示）
  const pct = ticketProgress()
  const stepLabel = currentStepLabel()
  const states = (detail?.states as any[]) || []

  // ★ 双模式（2026-09-13）：还原模式已完成文件工单若已有纯文案 .md 产物，
  //   进度气泡显示「仅下载译文文案」次级入口（纯文案模式主产物即 .md，不重复显示）
  const canDownloadTextArtifact = (() => {
    const tk = detail?.ticket
    if (!tk || !tk.file_path || tk.status !== 'completed') return false
    if ((tk.delivery || 'restore') === 'text') return false
    return !!(tk as any).text_result_path || (detail?.files || []).some((f: any) => !!f.text_result_path)
  })()

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto', padding: '20px 24px', width: '100%', minWidth: 0 }}>
      <style>{CSS_TK}</style>
      <h2 style={{ margin: '0 0 4px' }}>{t('tk.entry')}</h2>
      <p style={{ fontSize: 12, color: 'var(--lc-text-3)', margin: '0 0 12px' }}>{t('tk.createHint')}</p>

      {/* ===== 创建工单 ===== */}
      <div style={{ border: '1.2px solid #464C58', borderRadius: 8, padding: 16, marginBottom: 18 }}>
        {imageHeavyHint && (
          <div style={{ background: 'rgba(210,153,34,0.10)', border: '1.2px solid #f0c674', borderRadius: 8, padding: '8px 12px', marginBottom: 8, fontSize: 12 }}>
            <Icon n="alert" /> {t('tk.imageHeavyHint')}
            <Link onClick={() => setImageHeavyHint(false)} aria-label={t('common.close')}><Icon n="close" /></Link>
          </div>
        )}
        <h3 style={{ margin: '0 0 10px' }}>{t('tk.createTitle')}</h3>

        <div style={{ display: 'flex', gap: 8, marginBottom: 10 }}>
          <Button variant={mode === 'text' ? 'primary' : 'secondary'} onClick={() => setMode('text')}>{t('tk.modeText')}</Button>
          <Button variant={mode === 'file' ? 'primary' : 'secondary'} onClick={() => setMode('file')}>{t('tk.modeFile')}</Button>
        </div>

        {/* ★ 工单双模式（2026-09-13）：文件工单交付方式——还原文件 / 纯文案 */}
        {mode === 'file' && (
          <div style={{ marginBottom: 10 }}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <span style={{ fontSize: 13, color: 'var(--lc-text-3)' }}>{t('tk.deliveryLabel')}</span>
              <Button size="sm" variant={delivery === 'restore' ? 'primary' : 'secondary'}
                onClick={() => { setDelivery('restore'); localStorage.setItem('ticket_delivery', 'restore') }}>
                {t('tk.deliveryRestore')}
              </Button>
              <Button size="sm" variant={delivery === 'text' ? 'primary' : 'secondary'}
                onClick={() => { setDelivery('text'); localStorage.setItem('ticket_delivery', 'text') }}>
                {t('tk.deliveryText')}
              </Button>
            </div>
            <div style={{ fontSize: 12, color: 'var(--lc-text-3)', marginTop: 4 }}>
              {delivery === 'text' ? t('tk.deliveryTextTip') : t('tk.deliveryRestoreTip')}
            </div>
          </div>
        )}

        <input className="lc-input" value={title} onChange={(e) => setTitle(e.target.value)} aria-label={t('tk.titlePlaceholder')} placeholder={t('tk.titlePlaceholder')} style={{ width: '100%', marginBottom: 8 }} />

        {mode === 'text' ? (
          <textarea className="lc-textarea" rows={4} value={text} onChange={(e) => setText(e.target.value)} aria-label={t('tk.textPlaceholder')} placeholder={t('tk.textPlaceholder')} style={{ width: '100%', minHeight: 110, maxHeight: 360, resize: 'vertical' }} />
        ) : (
          <>
              <div onClick={() => document.getElementById('tk-file-input')?.click()}
                style={{ border: '2px dashed #464C58', borderRadius: 8, padding: 34, textAlign: 'center', cursor: 'pointer', color: 'var(--lc-text-3)', background: '#0E1014' }}>
                <input id="tk-file-input" type="file" multiple hidden accept={delivery === 'text' ? TEXT_DELIVERY_ACCEPT : TRANSLATE_FILE_ACCEPT} onChange={onFileSelect} />
              <div>{delivery === 'text' ? t('tk.fileHintText') : t('tk.fileHint')}<br /><span style={{ fontSize: 12 }}>{t('tk.multiHint')}</span></div>
            </div>
              {files.length > 0 && (
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8, alignItems: 'center' }}>
                  {files.map((f, idx) => (
                  <div key={f.name + f.size} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, background: '#0E1014', border: '1.2px solid #464C58', borderRadius: 8, padding: '3px 10px', fontSize: 12, maxWidth: 320 }}>
                    <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.name}</span>
                      <span style={{ color: '#999', fontSize: 11.5 }}>{fmtKB(f.size)}</span>
                    <Link tone="danger" onClick={() => removeFileAt(idx)} aria-label={`${t('common.delete')}: ${f.name}`}><Icon n="close" /></Link>
                  </div>
                ))}
                <div style={{ width: '100%', fontSize: 12, color: 'var(--lc-text-3)' }}>
                  {tpl('tk.filesCount', { n: files.length })} · {(files.reduce((a, f) => a + f.size, 0) / 1024).toFixed(0)} KB
                </div>
              </div>
            )}
          </>
        )}

        {/* ★ 任务⑤（2026-09-15）：已选语言 chip 行（与聊天窗一致，选中结果唯一展示位） */}
        <div style={{ marginTop: 10 }}>
          <LangChips langs={langs} onRemove={setLangs} />
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 10, flexWrap: 'wrap' }}>
          <label style={{ fontSize: 13, color: '#555', whiteSpace: 'nowrap' }}>{t('tk.langsLabel')}</label>
          <div style={{ minWidth: 300, flex: 1 }}>
            <LangMultiSelect value={langs} onChange={setLangs} />
          </div>
          <ModeToggle value={qualityMode as 'fast' | 'pro'} fastFirst
            onChange={(val) => { setQualityMode(val); localStorage.setItem('translate_mode', val) }} />
          {/* ★ 缩翻（任务7）：勾选并输入最长字符限制，提示模型精简输出。
              预留定宽槽位（72px）——勾选只显隐输入框、不改变行宽，避免模式切换/创建按钮位置跳动 */}
          <label style={{ fontSize: 13, color: '#555', display: 'flex', alignItems: 'center', gap: 4, whiteSpace: 'nowrap' }}>
            <input type="checkbox" checked={condenseOn} onChange={(e) => setCondenseOn(e.target.checked)} /> {t('app.condense')}
          </label>
          <div style={{ width: 72, flexShrink: 0 }}>
            {condenseOn && (
              <input type="number" min={1} max={10000} value={condenseMax}
                onChange={(e) => setCondenseMax(parseInt(e.target.value) || 0)}
                style={{ width: '100%', boxSizing: 'border-box', height: 30, fontSize: 12, border: '1.2px solid #464C58', borderRadius: 6, padding: '0 6px' }}
                title={t('tk.condenseMaxTitle')} />
            )}
          </div>
          <Button variant="primary" disabled={creating} onClick={create} style={{ marginLeft: 'auto' }}>
            {creating ? t('tk.submitting') : t('tk.create')}
          </Button>
        </div>
      </div>

      {/* ===== 我的工单 ===== */}
      <div style={{ border: '1.2px solid #464C58', borderRadius: 8, padding: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <h3 style={{ margin: 0 }}>{t('tk.myTickets')}</h3>
          <Button size="sm" variant="secondary" onClick={load} aria-label={t('common.refresh')}><Icon n="refresh" /></Button>
        </div>
        <DataTable<Ticket>
          rowKey={(row) => String(row.id)}
          rows={tickets}
          columns={[
            { key: 'ticket_no', title: t('tk.colNo'), width: 170, mono: true,
              render: (row) => <code>{row.ticket_no || row.id}</code> },
            { key: 'title', title: t('users.colName'), width: 220,
              render: (row) => <span title={row.title}>{row.title}</span> },
            { key: 'status', title: t('users.colStatus'), width: 110,
              render: (row) => <span>{statusLabel(row.status)}</span> },
            // ★ 改造 5：质检徽标列（error/warning 计数 + 质检存疑），无质检数据不渲染
            { key: 'quality', title: t('tk.qaTitle'), width: 190,
              render: (row) => <QualityBadges row={row} /> },
            { key: 'target_langs', title: t('tk.colLangs'), width: 150,
              render: (row) => (
                <span>
                  {String(row.target_langs || '')
                    .split(',')
                    .map((c: string) => c.trim())
                    .filter(Boolean)
                    .map((c: string) => langLabel(c, lang))
                    .join('、')}
                </span>
              ) },
            { key: 'created_at', title: t('tk.colCreatedAt'), width: 160, // E16：本域键（旧借 users.colLastLogin）
              render: (row) => fmtTime(row.created_at) },
            { key: 'op', title: t('org.colActions'), width: 280,
              render: (row) => (
                <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexWrap: 'wrap' }}>
                  {row.status === 'draft' && (
                    <Link onClick={() => run(row)}>{t('tk.run')}</Link>
                  )}
                  {row.status === 'completed' && (
                    <span style={{ opacity: downloadingId === row.id ? 0.5 : undefined }}>
                      <Link onClick={() => download(row)}>
                        {downloadingId === row.id ? t('tk.submitting') : t('tk.download')}
                      </Link>
                    </span>
                  )}
                  {row.status === 'completed' && (
                    <Link onClick={() => openFeedback(row)}><Icon n="chat" /> {t('fb.entry')}</Link>
                  )}
                  {['completed', 'cancelled'].includes(row.status) && (
                    <Link tone="danger" onClick={() => deleteTicket(row)}><Icon n="trash" /> {t('common.delete')}</Link>
                  )}
                  {['queued', 'in_progress'].includes(row.status) && (
                    <Link onClick={() => cancelTicket(row)}>{t('tk.cancel')}</Link>
                  )}
                  <Link onClick={() => toggleDetail(row)}>{t('tk.detail')}</Link>
                </div>
              ) },
          ]}
          emptyText={t('tk.empty')}
        />
      </div>

      {/* 进度气泡（Dialog 承载，等价 Vue Teleport 气泡内容） */}
      {/* ★ 改造 5：宽度按是否有质检数据自适应——质检明细表需要更宽的可读区（380 → 620） */}
      {detail && (
      <div className="tk-overlay" onMouseDown={(e) => { if (e.target === e.currentTarget) { setDetail(null); stopDetailPoll() } }}>
      <div className="tk-dialog" style={{ width: detail.quality ? 620 : 380 }}>
        <div className="tk-dialog__title">{detail.ticket?.title || t('tk.progress')}</div>
        <div className="tk-dialog__body">
        {pct !== null && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, margin: '10px 0 6px' }}>
            <div className="tk-progress" style={{ flex: 1 }}><div className="tk-progress__bar" style={{ width: `${pct}%` }} /></div>
            <span style={{ fontSize: 14, fontWeight: 700, color: 'var(--lc-text-1)', minWidth: 42 }}>{pct}%</span>
            {stepLabel && <span style={{ fontSize: 12, color: 'var(--lc-text-3)' }}>{stepLabel}</span>}
          </div>
        )}
        {states.length > 0 ? (
          <div style={{ marginTop: 8, maxHeight: 200, overflowY: 'auto' }}>
            {states.map((st: any) => (
              <div key={st.id} className={`st-${st.status}`} style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 12, padding: '3px 0' }}>
                <span style={{ flex: 1, color: '#555' }}>{stepName(st.step)}</span>
                <span style={{ fontSize: 11, padding: '1px 6px', borderRadius: 4,
                  background: st.status === 'success' ? 'rgba(231,233,234,0.10)' : st.status === 'running' ? 'rgba(231,233,234,0.16)' : st.status === 'error' ? 'rgba(229,72,77,0.10)' : '#16181C',
                  color: st.status === 'success' ? 'var(--lc-text-1)' : st.status === 'running' ? 'var(--lc-text-2)' : st.status === 'error' ? 'var(--lc-danger)' : 'var(--lc-text-3)' }}>{st.status}</span>
                {st.error && <span style={{ color: 'var(--lc-danger)', fontSize: 11 }}><Icon n="alert" /> {st.error}</span>}
              </div>
            ))}
          </div>
        ) : (
          <p style={{ fontSize: 12, color: 'var(--lc-text-3)', margin: '8px 0 0' }}>{t('tk.noSteps')}</p>
        )}
        {/* ★ 改造 5：详情抽屉「质检报告」区块（确定性 QA 汇总 + Issues 明细 + 各语言评估分） */}
        <QualityBlock q={detail?.quality} lang={lang} flagged={detail?.ticket?.quality_flagged === 1} />
        {/* ★ 工单双模式（2026-09-13）：还原模式已完成文件工单提供「仅下载译文文案(.md)」次级入口
            （版式不满意或对还原产物降级交付时直接取文案）；纯文案模式主产物即 .md，不重复显示 */}
        {canDownloadTextArtifact ? (
          <div style={{ marginTop: 10 }}>
            <Button size="sm" variant="secondary" onClick={() => detail && detail.ticket && downloadText(detail.ticket)}>
              <Icon n="doc" /> {t('tk.downloadMd')}
            </Button>
          </div>
        ) : null}
        </div>
      </div>
      </div>
      )}

      {/* 用户反馈弹窗（已完成工单 → 平台） */}
      {feedbackTarget && (
        <TicketFeedbackModal
          target={feedbackTarget}
          onClose={() => setFeedbackTarget(null)}
          onSubmitted={() => { toastSuccess(t('fb.done')); setFeedbackTarget(null) }}
        />
      )}
    </div>
  )
}

// 工单反馈弹窗（等价 Vue FeedbackModal：target.type='ticket'）
// 已完成工单提交反馈到平台的独立弹窗
function TicketFeedbackModal({ target, onClose, onSubmitted }: {
  target: { type: 'ticket'; ticket_id: number; mode: string }
  onClose: () => void
  onSubmitted: () => void
}) {
  const [content, setContent] = useState('')
  const [withContext, setWithContext] = useState(true)
  const [submitting, setSubmitting] = useState(false)

  // 提交工单反馈：校验非空 → 调用接口 → 成功回调关闭弹窗
  async function submit() {
    if (!content.trim()) return
    setSubmitting(true)
    try {
      const r = await createFeedback({
        target_type: target.type,
        ticket_id: target.ticket_id,
        content: content.trim(),
        with_context: withContext,
        mode: target.mode,
      })
      if (!r.success) { toastError(r.message); return }
      onSubmitted()
    } catch (e: any) {
      toastError(e instanceof Error ? e.message : String(e))
    } finally { setSubmitting(false) }
  }

  return (
    <div className="tk-overlay" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div className="tk-dialog" style={{ width: 520 }}>
        <div className="tk-dialog__title">{t('fb.title')}</div>
        <div className="tk-dialog__body">
          <p style={{ fontSize: 12, color: 'var(--lc-text-3)', margin: '0 0 10px' }}>{t('fb.hint')}</p>
          <textarea className="lc-textarea" rows={4} maxLength={1000} value={content} onChange={(e) => setContent(e.target.value)} aria-label={t('fb.placeholder')} placeholder={t('fb.placeholder')} />
      <div style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 8 }}>
            <Switch checked={withContext} onChange={(e) => setWithContext(e.target.checked)} />
            <span style={{ fontSize: 13, color: 'var(--lc-text-3)' }}>{t('fb.withContext')}</span>
          </div>
        </div>
        <div className="tk-dialog__actions">
          <Button variant="secondary" onClick={onClose}>{t('common.cancel')}</Button>
          <Button variant="primary" disabled={!content.trim() || submitting} onClick={submit}>
                  {submitting ? t('fb.submitting') : t('fb.submit')}
          </Button>
        </div>
      </div>
    </div>
  )
}

// fmtTime 兼容引入（与 Vue components/admin/ui 等价）——将时间字符串格式化为本地字符串
function fmtTime(s: string): string {
  try { return new Date(s).toLocaleString() } catch { return s }
}

// 页面级样式：tk- 前缀（防与组件库/其他页面类名重名）
const CSS_TK = `
.tk-overlay{position:fixed;inset:0;background:rgba(0,0,0,.72);display:flex;align-items:center;justify-content:center;z-index:1200}
.tk-dialog{background:var(--lc-panel);border:1.2px solid var(--lc-border-card);border-radius:14px;box-shadow:var(--lc-panel-highlight);max-width:calc(100vw - 32px);max-height:86vh;overflow:auto}
.tk-dialog__title{padding:18px 24px 0;font-size:16px;font-weight:600;color:var(--lc-text)}
.tk-dialog__body{padding:14px 24px 6px}
.tk-dialog__actions{display:flex;justify-content:flex-end;gap:10px;padding:12px 24px 20px}
.tk-progress{height:8px;border-radius:999px;background:var(--lc-inset);overflow:hidden}
.tk-progress__bar{height:100%;background:var(--lc-text-1);border-radius:999px;transition:width .3s}
`
