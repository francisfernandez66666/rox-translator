// ============================================================================
// components/TicketsPage.tsx — 翻译工单页（Vue TicketsPage 等价实现）
// 能力：文本/多文件建单（fast/pro）、列表 Table、进度气泡（开气泡期间 3s 详情轮询 +
//       5s 列表轮询）、取消/删除/下载（blob 带鉴权）、已完成工单反馈。
// ============================================================================
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  Button, Input, Table, Dialog, MessagePlugin, Progress, Space, Select, Textarea, Switch, Tooltip,
} from 'tdesign-react'
import {
  myTickets, ticketCreate, ticketCreateFile, ticketRun, ticketDetail,
  ticketDownload, ticketDelete, ticketCancel, createFeedback,
} from '@/api'
import type { Ticket, TicketResp } from '@/api/tickets'
import LangMultiSelect from './LangMultiSelect'
import ModeToggle from '@/components/ModeToggle'
import { t, tpl } from '@/i18n'

// 步骤 key → 用户友好名称（与 Vue 对齐）——用于进度气泡中展示每个阶段中文名
const STEP_NAMES: Record<string, string> = {
  kb_match: '知识库匹配', ai_initial: 'AI 初翻', evals_initial: '质量评估',
  review: '专业校对', evals_review: '校对评估', gate: '硬闸校验',
  culture_gate: '文化检查', qa: '校对', file_extract: '解析提取',
  file_translate: '初翻', approval: '审批', feedback: '反馈',
  file_qa: '校对', file_writeback: '回写文件', writeback: '回写文件',
}

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

// 默认导出组件：翻译工单页，提供建单、工单列表、进度气泡与反馈（等价 Vue TicketsPage）
export default function TicketsPage() {
  const [mode, setMode] = useState<'text' | 'file'>('text')
  const [qualityMode, setQualityMode] = useState<string>(localStorage.getItem('translate_mode') || 'fast')
  const [title, setTitle] = useState('')
  const [text, setText] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [langs, setLangs] = useState<string[]>(['en'])
  const [creating, setCreating] = useState(false)
  const [imageHeavyHint, setImageHeavyHint] = useState(false)

  const [tickets, setTickets] = useState<Ticket[]>([])
  const [detail, setDetail] = useState<TicketResp | null>(null)
  const [downloadingId, setDownloadingId] = useState<number | null>(null)

  // 反馈弹窗目标（已完成工单）
  const [feedbackTarget, setFeedbackTarget] = useState<{ type: 'ticket'; ticket_id: number; mode: string } | null>(null)

  const listTimer = useRef<number | null>(null)
  const detailTimer = useRef<number | null>(null)

  const langsJoined = langs.length ? langs.join(',') : 'en'

  // 拉取我的工单列表
  const load = useCallback(async () => {
    try {
      const r = await myTickets()
      if (r.success) setTickets(r.tickets || [])
    } catch { /* 忽略 */ }
  }, [])

  // 列表轮询：存在排队/进行中工单且页面可见时每 5s 刷新
  // 仅在页面可见且存在活跃工单时才刷新列表
  const pollActive = useCallback(() => {
    if (document.hidden) return
    if (tickets.some((x) => ['queued', 'in_progress'].includes(x.status))) void load()
  }, [tickets, load])

  useEffect(() => {
    void load()
    listTimer.current = window.setInterval(pollActive, 5000)
    return () => { if (listTimer.current) window.clearInterval(listTimer.current) }
  }, [load, pollActive])

  // 详情轮询：打开气泡期间每 3s 刷新；工单完成自动停止
  // 启动工单详情轮询，完成态到达后自动停止
  const startDetailPoll = useCallback(() => {
    stopDetailPoll()
    detailTimer.current = window.setInterval(async () => {
      const id = detail?.ticket?.id
      if (!id || document.hidden) return
      const r = await ticketDetail(id)
      if (r.success) setDetail(r)
      const stt = r.ticket?.status
      if (stt && !['queued', 'in_progress'].includes(stt)) stopDetailPoll()
    }, 3000)
  }, [detail])

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
    if (running) return STEP_NAMES[running.step] || running.step
    const fs = (detail as any)?.files || []
    if (fs.length) {
      const done = fs.filter((f: any) => f.result_path || f.error).length
      return `已完成 ${done}/${fs.length} 个文件`
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
    for (const f of list) { if (!exist.has(f.name + f.size)) { next.push(f); exist.add(f.name + f.size) } }
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
      if (mode === 'text') {
        if (!text.trim()) return
        r = await ticketCreate({
          title: title.trim() || t('tk.defaultTitle'),
          source_text: text,
          target_langs: langsJoined,
          mode: qualityMode,
        })
      } else {
        if (!files.length) return
        r = await ticketCreateFile([...files], { title: title.trim(), target_langs: langsJoined, mode: qualityMode })
      }
      if (!r.success) { alert(r.message); setCreating(false); return }
      setTitle(''); setText(''); setFiles([])
      void load()
    } finally { setCreating(false) }
  }

  // 运行草稿态工单
  async function run(row: Ticket) {
    const r = await ticketRun(row.id)
    if (!r.success) { void MessagePlugin.error(r.message || t('tk.runFail')); return }
    void load()
  }
  // 取消排队/进行中的工单（需确认）
  async function cancelTicket(row: Ticket) {
    if (!window.confirm(tpl('tk.cancelConfirm', { no: row.ticket_no || row.id }))) return
    const r = await ticketCancel(row.id)
    if (!r.success) { void MessagePlugin.error(r.message || t('tk.opFail')); return }
    void load()
  }
  // 删除已完成/已取消的工单（需确认）
  async function deleteTicket(row: Ticket) {
    if (!window.confirm(tpl('tk.deleteConfirm', { no: row.ticket_no }))) return
    const r = await ticketDelete(row.id)
    if (!r.success) { void MessagePlugin.error(r.message || t('tk.opFail')); return }
    void load()
  }
  // 下载工单结果（带防重入标记）
  async function download(row: Ticket) {
    if (downloadingId !== null) return
    setDownloadingId(row.id)
    try { await ticketDownload(row.id) } catch (e: any) { void MessagePlugin.error(e?.message || t('tk.downloadFail')) }
    finally { setDownloadingId(null) }
  }

  // 展开/收起步骤进度气泡
  // 点击行展开详情并启动轮询，再次点击或同一条已展开则收起
  async function toggleDetail(row: Ticket) {
    if (detail && detail.ticket?.id === row.id) { setDetail(null); stopDetailPoll(); return }
    const r = await ticketDetail(row.id)
    if (r.success) { startDetailPoll(); setDetail(r) }
  }

  // 打开针对指定工单的反馈弹窗（携带工单 ID 与翻译模式）
  // 设置反馈目标为工单类型，触发反馈弹窗
  function openFeedback(tk: Ticket) {
    setFeedbackTarget({ type: 'ticket', ticket_id: tk.id, mode: (tk as any).mode || 'pro' })
  }

  const pct = ticketProgress()
  const stepLabel = currentStepLabel()
  const states = (detail?.states as any[]) || []

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto', padding: '20px 24px' }}>
      <h2 style={{ margin: '0 0 4px' }}>📋 {t('tk.entry')}</h2>
      <p style={{ fontSize: 12.5, color: '#888', margin: '0 0 12px' }}>{t('tk.createHint')}</p>

      {/* ===== 创建工单 ===== */}
      <div style={{ border: '1px solid #e6e9ef', borderRadius: 12, padding: 16, marginBottom: 18 }}>
        {imageHeavyHint && (
          <div style={{ background: '#fff8e1', border: '1px solid #f0c674', borderRadius: 8, padding: '8px 12px', marginBottom: 8, fontSize: 12.5 }}>
            ⚠️ {t('tk.imageHeavyHint')}
            <button style={{ marginLeft: 8, border: 'none', background: 'transparent', cursor: 'pointer' }} onClick={() => setImageHeavyHint(false)}>✕</button>
          </div>
        )}
        <h3 style={{ margin: '0 0 10px' }}>{t('tk.createTitle')}</h3>

        <Space size={8} style={{ marginBottom: 10 }}>
          <Button variant={mode === 'text' ? 'base' : 'outline'} theme="primary" onClick={() => setMode('text')}>📝 {t('tk.modeText')}</Button>
          <Button variant={mode === 'file' ? 'base' : 'outline'} theme="primary" onClick={() => setMode('file')}>📎 {t('tk.modeFile')}</Button>
        </Space>

        <Input value={title} onChange={setTitle} placeholder={t('tk.titlePlaceholder')} style={{ width: '100%', marginBottom: 8 }} />

        {mode === 'text' ? (
          <Textarea autosize={{ minRows: 4, maxRows: 14 }} value={text} onChange={setText} placeholder={t('tk.textPlaceholder')} style={{ width: '100%' }} />
        ) : (
          <>
            <div onClick={() => document.getElementById('tk-file-input')?.click()}
                 style={{ border: '2px dashed #c9d4e3', borderRadius: 10, padding: 34, textAlign: 'center', cursor: 'pointer', color: '#667', background: '#fafbfd' }}>
              <input id="tk-file-input" type="file" multiple hidden accept=".docx,.xlsx,.pptx,.pdf,.txt,.csv,.srt,.vtt,.md,.json,.yaml,.yml" onChange={onFileSelect} />
              <div>📎 {t('tk.fileHint')}<br /><span style={{ fontSize: 12 }}>{t('tk.multiHint')}</span></div>
            </div>
            {files.length > 0 && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 8, alignItems: 'center' }}>
                {files.map((f, idx) => (
                  <div key={f.name + f.size} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, background: '#f5f7fb', border: '1px solid #d8dee6', borderRadius: 14, padding: '3px 10px', fontSize: 12.5, maxWidth: 320 }}>
                    <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>📄 {f.name}</span>
                    <span style={{ color: '#999', fontSize: 11.5 }}>{fmtKB(f.size)}</span>
                    <button style={{ border: 'none', background: 'transparent', cursor: 'pointer', color: '#c62828', fontSize: 12 }} onClick={() => removeFileAt(idx)}>✕</button>
                  </div>
                ))}
                <div style={{ width: '100%', fontSize: 12, color: '#888' }}>
                  {tpl('tk.filesCount', { n: files.length })} · {(files.reduce((a, f) => a + f.size, 0) / 1024).toFixed(0)} KB
                </div>
              </div>
            )}
          </>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginTop: 10, flexWrap: 'wrap' }}>
          <label style={{ fontSize: 13, color: '#555', whiteSpace: 'nowrap' }}>{t('tk.langsLabel')}</label>
          <div style={{ minWidth: 300, flex: 1 }}>
            <LangMultiSelect value={langs} onChange={setLangs} />
          </div>
          <ModeToggle value={qualityMode as 'fast' | 'pro'} fastFirst
            onChange={(val) => { setQualityMode(val); localStorage.setItem('translate_mode', val) }} />
          <Button theme="primary" loading={creating} onClick={create} style={{ marginLeft: 'auto' }}>
            {creating ? t('tk.submitting') : t('tk.create')}
          </Button>
        </div>
      </div>

      {/* ===== 我的工单 ===== */}
      <div style={{ border: '1px solid #e6e9ef', borderRadius: 12, padding: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <h3 style={{ margin: 0 }}>{t('tk.myTickets')}</h3>
          <Button size="small" variant="outline" onClick={load}>🔄</Button>
        </div>
        <Table
          rowKey="id"
          data={tickets}
          size="small"
          columns={[
            { colKey: 'ticket_no', title: t('tk.colNo'), width: 170,
              cell: ({ row }: any) => <code>{row.ticket_no || row.id}</code> },
            { colKey: 'title', title: t('users.colName'), width: 220, ellipsis: true,
              cell: ({ row }: any) => <span title={row.title}>{row.title}</span> },
            { colKey: 'status', title: t('users.colStatus'), width: 110,
              cell: ({ row }: any) => <span>{statusLabel(row.status)}</span> },
            { colKey: 'target_langs', title: t('tk.colLangs'), width: 150 },
            { colKey: 'created_at', title: t('users.colLastLogin'), width: 160,
              cell: ({ row }: any) => fmtTime(row.created_at) },
            { colKey: 'op', title: t('org.colActions'), width: 280,
              cell: ({ row }: any) => (
                <Space size={4}>
                  {row.status === 'draft' && (
                    <Button size="small" variant="text" theme="primary" onClick={() => run(row)}>{t('tk.run')}</Button>
                  )}
                  {row.status === 'completed' && (
                    <Button size="small" variant="text" theme="primary" disabled={downloadingId === row.id} onClick={() => download(row)}>
                      {downloadingId === row.id ? '⏳' : '⬇'} {downloadingId === row.id ? t('tk.submitting') : t('tk.download')}
                    </Button>
                  )}
                  {row.status === 'completed' && (
                    <Button size="small" variant="text" theme="primary" onClick={() => openFeedback(row)}>💬 {t('fb.entry')}</Button>
                  )}
                  {['completed', 'cancelled'].includes(row.status) && (
                    <Button size="small" variant="text" theme="danger" onClick={() => deleteTicket(row)}>🗑 {t('common.delete')}</Button>
                  )}
                  {['queued', 'in_progress'].includes(row.status) && (
                    <Button size="small" variant="text" theme="warning" onClick={() => cancelTicket(row)}>✕ {t('tk.cancel')}</Button>
                  )}
                  <Button size="small" variant="text" theme="default" onClick={() => toggleDetail(row)}>{t('tk.detail')}</Button>
                </Space>
              ) },
          ]}
          empty={t('tk.empty')}
        />
      </div>

      {/* 进度气泡（Dialog 承载，等价 Vue Teleport 气泡内容） */}
      <Dialog visible={!!detail} onClose={() => { setDetail(null); stopDetailPoll() }} width={380} footer={null}
              header={detail?.ticket?.title || t('tk.progress')}>
        {pct !== null && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, margin: '10px 0 6px' }}>
            <Progress theme="plump" percentage={pct} style={{ flex: 1 }} />
            <span style={{ fontSize: 14, fontWeight: 700, color: '#1a73e8', minWidth: 42 }}>{pct}%</span>
            {stepLabel && <span style={{ fontSize: 12.5, color: '#666' }}>{stepLabel}</span>}
          </div>
        )}
        {states.length > 0 ? (
          <div style={{ marginTop: 8, maxHeight: 200, overflowY: 'auto' }}>
            {states.map((st: any) => (
              <div key={st.id} className={`st-${st.status}`} style={{ display: 'flex', gap: 6, alignItems: 'center', fontSize: 12, padding: '3px 0' }}>
                <span style={{ flex: 1, color: '#555' }}>{STEP_NAMES[st.step] || st.step}</span>
                <span style={{ fontSize: 11, padding: '1px 6px', borderRadius: 4,
                  background: st.status === 'success' ? '#e6f4ea' : st.status === 'running' ? '#e8f0fe' : st.status === 'error' ? '#fce8e6' : '#eee',
                  color: st.status === 'success' ? '#2e7d32' : st.status === 'running' ? '#1a73e8' : st.status === 'error' ? '#c5221f' : '#888' }}>{st.status}</span>
                {st.error && <span style={{ color: '#c5221f', fontSize: 11 }}>⚠️ {st.error}</span>}
              </div>
            ))}
          </div>
        ) : (
          <p style={{ fontSize: 12.5, color: '#888', margin: '8px 0 0' }}>{t('tk.noSteps')}</p>
        )}
      </Dialog>

      {/* 用户反馈弹窗（已完成工单 → 平台） */}
      {feedbackTarget && (
        <TicketFeedbackModal
          target={feedbackTarget}
          onClose={() => setFeedbackTarget(null)}
          onSubmitted={() => { void MessagePlugin.success(t('fb.done')); setFeedbackTarget(null) }}
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
      if (!r.success) { alert(r.message); return }
      onSubmitted()
    } catch (e: any) {
      alert(e instanceof Error ? e.message : String(e))
    } finally { setSubmitting(false) }
  }

  return (
    <Dialog header={t('fb.title')} visible onClose={onClose} width={520}
            footer={
              <>
                <Button variant="outline" onClick={onClose}>{t('common.cancel')}</Button>
                <Button disabled={!content.trim() || submitting} loading={submitting} onClick={submit}>
                  {submitting ? t('fb.submitting') : t('fb.submit')}
                </Button>
              </>
            }>
      <p style={{ fontSize: 12, color: '#888', margin: '0 0 10px' }}>{t('fb.hint')}</p>
      <Textarea autosize={{ minRows: 4 }} maxlength={1000} value={content} onChange={(v) => setContent(v as string)} placeholder={t('fb.placeholder')} />
      <div style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 8 }}>
        <Switch size="small" value={withContext} onChange={(v) => setWithContext(v as boolean)} />
        <span style={{ fontSize: 13, color: '#667' }}>{t('fb.withContext')}</span>
      </div>
    </Dialog>
  )
}

// fmtTime 兼容引入（与 Vue components/admin/ui 等价）——将时间字符串格式化为本地字符串
function fmtTime(s: string): string {
  try { return new Date(s).toLocaleString() } catch { return s }
}
