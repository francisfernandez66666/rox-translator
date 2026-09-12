// ============================================================================
// components/admin/TicketsP.tsx — 反馈 / 审批台 / TM 审核面板
// 职责：用户反馈、审批工单、TM 记忆审核三合一工作台
// 从 panels_d.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Tag, Space, Textarea, MessagePlugin,
} from 'tdesign-react'
import { confirmDialog } from '@/components/uiDialogs'
import {
  approveList, approveAction,
  feedbackList, feedbackReply, resolveFeedback, createFeedback,
  listTmReview, approveTmReview, rejectTmReview,
} from '@/api'
import { Panel, Field, toastResp } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'

type Any = Record<string, any>

const rowMt: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 8 }
const cardStyle: any = { border: '1px solid #e3e6ef', borderRadius: 8, padding: 14, marginBottom: 12 }

// firstTranslation 从工单 final_result JSON 中取第一个目标语种的译文（预览用；解析失败返回空串）。
function firstTranslation(finalResult: unknown): string {
  try {
    const p = typeof finalResult === 'string' && finalResult ? JSON.parse(finalResult) : null
    const tr = (p?.translations || {}) as Record<string, string>
    const k = Object.keys(tr)[0]
    return k ? tr[k] : ''
  } catch { return '' }
}

/** 反馈 / 审批台 / TM 审核面板 */
export function TicketsP() {
  const [, t, tpl] = useT()
  const { isSuper, activeTenantId, consumeFeedback } = useAdmin()
  const [feedbacks, setFeedbacks] = useState<Any[]>([])
  const [statusFilter, setStatusFilter] = useState('')
  const [selected, setSelected] = useState<Any | null>(null)
  const [replyDraft, setReplyDraft] = useState('')
  const [newContent, setNewContent] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [fbTab, setFbTab] = useState<'feedback' | 'review'>('feedback')
  const [reviews, setReviews] = useState<Any[]>([])
  const [rvFilter, setRvFilter] = useState('pending')
  const [approvalTickets, setApprovalTickets] = useState<Any[]>([])
  const [approveDlg, setApproveDlg] = useState<null | { row: Any; text: string; reason: string; suggestion: string; action: 'approve' | 'reject' }>(null)

  const loadFeedbacks = useCallback(async () => {
    const r = await feedbackList(statusFilter)
    if (r.success) setFeedbacks((r as unknown as { feedbacks?: Any[] }).feedbacks || [])
  }, [statusFilter])
  const loadReviews = useCallback(async () => {
    const r = await listTmReview(rvFilter)
    if (r.success) setReviews((r as unknown as { candidates?: Any[] }).candidates || [])
  }, [rvFilter])
  const loadApproval = useCallback(async () => {
    const r = await approveList()
    if (r.success) setApprovalTickets((r as unknown as { tickets?: Any[] }).tickets || [])
  }, [])

  useEffect(() => { void loadFeedbacks(); void loadApproval() }, [activeTenantId, loadFeedbacks, loadApproval])
  useEffect(() => { if (isSuper && fbTab === 'review') void loadReviews() }, [isSuper, fbTab, loadReviews])
  useEffect(() => { const fid = consumeFeedback(); if (fid) void jumpFeedback(fid); /* eslint-disable-next-line */ }, [consumeFeedback])

  function openDetail(f: Any) { setSelected(f); setReplyDraft('') }
  async function jumpFeedback(fid: number) {
    setFbTab('feedback'); setStatusFilter('')
    await loadFeedbacks()
    const f = feedbacks.find((x) => x.id === fid)
    if (f) openDetail(f)
  }
  function srcLabel(src: string) {
    return src === 'bitext' ? t('tmr.srcBitext') : src === 'tmx' ? t('tmr.srcTmx') : src === 'feedback' ? t('tmr.srcFeedback') : t('tmr.srcCount')
  }
  function ctxTranslations(f: Any): Record<string, string> {
    try { return JSON.parse(f.translations_json || '{}') } catch { return {} }
  }
  function fmtAt(iso: string): string {
    if (!iso) return ''
    const d = new Date(iso)
    return isNaN(+d) ? iso : d.toLocaleString()
  }
  async function submitFeedback() {
    const content = newContent.trim(); if (!content) return
    setSubmitting(true)
    try {
      const r = await createFeedback({ target_type: 'text', content })
      if (!r.success) { MessagePlugin.error(r.message); return }
      setNewContent(''); await loadFeedbacks()
    } finally { setSubmitting(false) }
  }
  async function doReply() {
    if (!selected || !replyDraft.trim()) return
    const r = await feedbackReply(Number(selected.id), replyDraft.trim())
    if (!r.success) { MessagePlugin.error(r.message); return }
    setSelected({ ...selected, replies: (r as unknown as { replies?: Any[] }).replies || [] })
    setReplyDraft('')
  }
  async function doResolve() {
    if (!selected) return
    if (!(await confirmDialog({ body: t('fb.resolveConfirm') }))) return
    const r = await resolveFeedback(Number(selected.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    setSelected({ ...selected, status: 'resolved', handled_at: new Date().toISOString() })
    await loadFeedbacks()
  }
  async function doApproveReview(c: Any) {
    const r = await approveTmReview(Number(c.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadReviews()
  }
  async function doRejectReview(c: Any) {
    const r = await rejectTmReview(Number(c.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadReviews()
  }
  async function doApprove(tk: Any, action: 'approve' | 'reject') {
    const r = await approveAction(Number(tk.id), action, tk._reason || '', tk._suggestion || '', '')
    if (!r.success) { MessagePlugin.error(r.message); return }
    tk._reason = ''; tk._suggestion = ''
    await loadApproval()
  }

  return (
    <>
      <h2 style={{ margin: '4px 0 4px' }}>{t('fb.workbench')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{isSuper ? t('fb.superHint') : t('fb.userHint')}</p>

      {isSuper && (
        <div style={{ marginBottom: 12 }}>
          <Button size="small" variant={fbTab === 'feedback' ? 'outline' : 'text'} onClick={() => setFbTab('feedback')}>💬 {t('fb.tabFeedback')}</Button>
          <Button size="small" variant={fbTab === 'review' ? 'outline' : 'text'} onClick={() => setFbTab('review')}>📚 {t('fb.tabReview')}</Button>
        </div>
      )}

      {isSuper && fbTab === 'review' && (
        <Panel title={t('fb.tabReview')}>
          <div style={rowMt}>
            <Select value={rvFilter} onChange={(v: any) => setRvFilter(String(v))} style={{ width: 160 }}
              options={[{ label: t('tmr.pending'), value: 'pending' }, { label: t('tmr.approved'), value: 'approved' }, { label: t('tmr.rejected'), value: 'rejected' }]} />
            <Button size="small" onClick={() => void loadReviews()}>{t('tickets.refresh')}</Button>
          </div>
          <Table rowKey="id" size="small" data={reviews}
            columns={[
              { colKey: 'id', title: 'ID', width: 60 },
              { colKey: 'zh', title: t('tmr.zh'), ellipsis: true },
              { colKey: 'trans', title: t('tmr.trans'), ellipsis: true },
              { colKey: 'lang', title: t('tmr.colLangs'), width: 90 },
              { colKey: 'source', title: t('tmr.source'), width: 130, cell: ({ row }: any) => (
                <span>{srcLabel(row.source)}{row.ref_type === 'feedback' && <> · <a href="#" onClick={(e: any) => { e.preventDefault(); void jumpFeedback(Number(row.ref_id)) }}>{t('tmr.linkFb')}#{row.ref_id}</a></>}</span>
              ) },
              { colKey: 'hit_count', title: t('tmr.hits'), width: 90 },
              { colKey: 'status', title: t('users.colStatus' as never), width: 90, cell: ({ row }: any) =>
                <Tag theme={row.status === 'approved' ? 'success' : row.status === 'pending' ? 'warning' : 'default'}>{row.status === 'approved' ? t('tmr.approved') : row.status === 'rejected' ? t('tmr.rejected') : t('tmr.pending')}</Tag> },
              { colKey: 'op', title: t('org.colActions'), width: 120, cell: ({ row }: any) => row.status === 'pending' ? (
                <Space size={4}>
                  <Button size="small" theme="success" onClick={() => void doApproveReview(row)}>✔</Button>
                  <Button size="small" theme="danger" onClick={() => void doRejectReview(row)}>✘</Button>
                </Space>
              ) : <span /> },
            ] as never} />
        </Panel>
      )}

      {selected && (
        <div style={cardStyle}>
          <Button size="small" style={{ float: 'right' }} onClick={() => setSelected(null)}>← {t('fb.backToList')}</Button>
          <h3 style={{ marginTop: 0 }}>
            #{selected.id} · {selected.user_name || ('#' + selected.user_id)}{' '}
            <Tag theme={selected.status === 'resolved' ? 'success' : 'warning'}>{selected.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen')}</Tag>
          </h3>
          <pre style={{ whiteSpace: 'pre-wrap', fontFamily: 'inherit', margin: 0 }}>{selected.content}</pre>
          {selected.with_context && (
            <div style={{ background: '#fafafa', border: '1px dashed #ddd', borderRadius: 8, padding: '8px 10px', marginTop: 8, fontSize: 12.5 }}>
              <b>{t('fb.ctxAttached')}</b>
              {selected.source_text && <pre style={{ whiteSpace: 'pre-wrap', margin: '4px 0' }}>{selected.source_text}</pre>}
              {Object.entries(ctxTranslations(selected)).map(([k, v]) => (
                <div key={k} style={{ marginTop: 4 }}><b>[{k}]</b> {String(v)}</div>
              ))}
            </div>
          )}
          {selected.replies && selected.replies.length ? (
            <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 6 }}>
              {selected.replies.map((r: Any, i: number) => (
                <div key={i} style={{ background: r.role === 'admin' ? '#e8f0fe' : '#f5f6f8', borderRadius: 8, padding: '6px 10px', fontSize: 13 }}>
                  <div style={{ fontSize: 11, color: '#888', marginBottom: 2 }}>{r.name} · {r.role === 'admin' ? t('tickets.roleAdmin') : t('tickets.roleUser')} · {fmtAt(r.at)}</div>
                  <div style={{ whiteSpace: 'pre-wrap' }}>{r.content}</div>
                </div>
              ))}
            </div>
          ) : <div style={{ fontSize: 12, color: '#889', marginTop: 8 }}>{t('fb.noReplies')}</div>}
          {selected.status === 'open' ? (
            <div style={rowMt}>
              <Input value={replyDraft} onChange={(v: any) => setReplyDraft(v)} placeholder={t('fb.replyPlaceholder')} style={{ flex: 1 }} />
              <Button disabled={!replyDraft.trim()} onClick={() => void doReply()}>↩ {t('fb.reply')}</Button>
              {isSuper && <Button theme="success" onClick={() => void doResolve()}>✔ {t('fb.complete')}</Button>}
            </div>
          ) : <div style={{ fontSize: 12, color: '#1a7f37', marginTop: 8 }}>✅ {t('fb.archivedHint')}</div>}
        </div>
      )}

      {!selected && fbTab !== 'review' && (
        <>
          {!isSuper && (
            <Panel title={t('fb.submitTitle')}>
              <Textarea autosize={{ minRows: 3 }} value={newContent} onChange={(v: any) => setNewContent(v)} placeholder={t('fb.contentPlaceholder')} maxlength={1000} />
              <div style={{ ...rowMt, marginTop: 8 }}>
                <span style={{ flex: 1, fontSize: 12, color: '#889' }}>{newContent.length}/1000</span>
                <Button theme="success" disabled={!newContent.trim() || submitting} onClick={() => void submitFeedback()}>
                  {submitting ? t('fb.submitting') : t('fb.submit')}
                </Button>
              </div>
            </Panel>
          )}
          <div style={rowMt}>
            <Select value={statusFilter} onChange={(v: any) => setStatusFilter(String(v))} style={{ width: 160 }}
              options={[{ label: t('fb.filterAll'), value: '' }, { label: t('fb.statusOpen'), value: 'open' }, { label: t('fb.statusResolved'), value: 'resolved' }]} />
            <Button size="small" onClick={() => void loadFeedbacks()}>{t('tickets.refresh')}</Button>
            <span style={{ fontSize: 12, color: '#889' }}>{tpl('fb.count', { n: feedbacks.length })}</span>
          </div>
          <Table rowKey="id" size="small" data={feedbacks} style={{ marginTop: 8 }}
            columns={[
              { colKey: 'id', title: 'ID', width: 60 },
              { colKey: 'user', title: t('users.colUser' as never), width: 130, cell: ({ row }: any) => row.user_name || ('#' + row.user_id) },
              { colKey: 'target', title: t('fb.colTarget'), width: 130, cell: ({ row }: any) => row.target_type === 'ticket' ? `🎫 #${row.ticket_id}` : `📝 ${t('fb.targetText')}` },
              { colKey: 'content', title: t('fb.colContent'), ellipsis: true },
              { colKey: 'mode', title: t('fb.colMode'), width: 70, cell: ({ row }: any) => row.mode === 'fast' ? '⚡' : '🎓' },
              { colKey: 'created_at', title: t('overview.colTime'), width: 160, cell: ({ row }: any) => fmtAt(row.created_at) },
              { colKey: 'status', title: t('fb.colStatus'), width: 90, cell: ({ row }: any) =>
                <Tag theme={row.status === 'resolved' ? 'success' : 'warning'}>{row.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen')}</Tag> },
              { colKey: 'op', title: t('org.colActions'), width: 100, cell: ({ row }: any) =>
                <Button size="small" variant="text" onClick={() => openDetail(row)}>👁 {t('fb.viewDetail')}</Button> },
            ] as never} />
        </>
      )}

      <h2 style={{ margin: '32px 0 8px' }}>{t('tickets.approvalTitle')}</h2>
      <Button variant="outline" onClick={() => void loadApproval()}>{t('tickets.refresh')}</Button>
      {approvalTickets.map((tk) => (
        <div key={tk.id} style={{ ...cardStyle, marginTop: 12 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
            <b>{tk.ticket_no} — {tk.title}</b>
            <span style={{ fontSize: 12, color: '#889' }}>{tk.status}</span>
          </div>
          <p style={{ fontSize: 13, color: '#556', margin: '0 0 8px' }}>{tk.source_text}</p>
          <Textarea value={tk.final_result} readonly autosize={{ minRows: 3 }} />
          <div style={rowMt}>
            <Button theme="success" onClick={() => void doApprove(tk, 'approve')}>{t('tickets.approve')}</Button>
            <Input value={tk._reason || ''} onChange={(v: any) => { tk._reason = v }} placeholder={t('tickets.reasonPlaceholder')} style={{ flex: 1 }} />
            <Input value={tk._suggestion || ''} onChange={(v: any) => { tk._suggestion = v }} placeholder={t('tickets.suggestionPlaceholder')} style={{ flex: 1 }} />
            <Button theme="danger" onClick={() => void doApprove(tk, 'reject')}>{t('tickets.reject')}</Button>
          </div>
        </div>
      ))}
      {!approvalTickets.length && <div style={{ fontSize: 13, color: '#889' }}>{t('tickets.noApproval')}</div>}

      <Dialog visible={!!approveDlg} onClose={() => setApproveDlg(null)}
        header={`${t('tickets.approve')} ${approveDlg ? String(approveDlg.row.ticket_no) : ''}`} width={640}
        footer={
          <>
            <Button variant="outline" onClick={() => setApproveDlg(null)}>{t('fb.backToList')}</Button>
            {approveDlg?.action === 'reject' && (
              <Button theme="danger" onClick={async () => {
                if (!approveDlg.reason.trim()) { MessagePlugin.warning(t('tickets.reasonPlaceholder')); return }
                const r = await approveAction(Number(approveDlg.row.id), 'reject', approveDlg.reason, approveDlg.suggestion, '')
                if (toastResp(r, t('tickets.reject'))) setApproveDlg(null)
                await loadApproval()
              }}>{t('tickets.reject')}</Button>
            )}
            {approveDlg?.action === 'approve' && (
              <Button theme="success" onClick={async () => {
                const r = await approveAction(Number(approveDlg.row.id), 'approve', '', '', approveDlg.text)
                if (toastResp(r, t('tickets.approve'))) setApproveDlg(null)
                await loadApproval()
              }}>{t('tickets.approve')}{approveDlg.text ? `（含${t('fb.complete')}）` : ''}</Button>
            )}
          </>
        }>
        {approveDlg && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ fontSize: 13, color: '#556' }}>{String(approveDlg.row.source_text)}</div>
            {approveDlg.action === 'approve' && (
              <Field label={t('fb.complete')}>
                <Textarea autosize={{ minRows: 3 }} value={approveDlg.text} onChange={(v: any) => setApproveDlg({ ...approveDlg, text: v })}
                  placeholder={t('tickets.finalResultPlaceholder')} />
              </Field>
            )}
            {approveDlg.action === 'reject' && (
              <>
                <Field label={t('tickets.reasonPlaceholder')}><Textarea autosize={{ minRows: 2 }} value={approveDlg.reason} onChange={(v: any) => setApproveDlg({ ...approveDlg, reason: v })} /></Field>
                <Field label={t('tickets.suggestionPlaceholder')}><Textarea autosize={{ minRows: 2 }} value={approveDlg.suggestion} onChange={(v: any) => setApproveDlg({ ...approveDlg, suggestion: v })} /></Field>
              </>
            )}
            <div style={rowMt}>
              <Button theme="success" onClick={() => setApproveDlg({ ...approveDlg, action: 'approve', text: firstTranslation(approveDlg.row.final_result) })}>{t('tickets.approve')}</Button>
              <Button variant="outline" onClick={() => setApproveDlg(null)}>{t('fb.backToList')}</Button>
            </div>
          </div>
        )}
      </Dialog>
    </>
  )
}
