// ============================================================================
// components/EditorPage.tsx — 对照编辑器（工作流 D，新 feature）
// 双栏：左=源文（只读）+ 术语高亮；右=可编辑译文 + 通过/驳回批注。
// 文本工单解析 FinalResult；文件工单解析 xlsx/csv 对照表产物（后端负责）。
// 逐段保存至后端 translation_edits，状态 pending/approved/rejected。
// 呈现层已迁 @/ui/langcross/src：TDesign Dialog/Select/Textarea/Tag/MessagePlugin
// → 原生 select/textarea + lc-* 类 + StatusPill + useToast（一屏一个 primary）。
// ============================================================================
import { useCallback, useMemo, useState } from 'react'
import { Button, Input, StatusPill, useToast } from '@/ui/langcross/src'
import { t, tpl, useLang } from '@/i18n'
import { getSegments, getSegmentsByKey, saveSegments, type EditorSegment, type SegmentEdit } from '@/api/tickets'

/** 行本地编辑态 */
interface RowState {
  edited_text: string
  status: string
  note: string
}

// STATUS_OPTIONS 译文状态选项（★ F2 i18n 化；取词延迟到组件内以获得语言切换刷新）
const statusOptions = () => [
  { label: t('tk.edStatusPending'), value: 'pending' },
  { label: t('tk.edStatusApproved'), value: 'approved' },
  { label: t('tk.edStatusRejected'), value: 'rejected' },
]

/** 将源文中命中的术语串包裹为高亮 <mark> */
function highlightTerms(text: string, terms: string[]): React.ReactNode {
  if (!terms.length) return text
  // 术语来自知识库（可能含 . + ( ) 等正则元字符）：必须逐个转义后再拼交替式，
  // 否则 new RegExp 会抛错或把「C++」当量词误匹配；长度 ≤1 的词到处命中、噪声大于收益，直接丢
  const escaped = terms
    .filter((t) => t && t.length > 1)
    .map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  if (!escaped.length) return text
  const re = new RegExp(`(${escaped.join('|')})`, 'g')
  const parts = text.split(re)
  return parts.map((p, i) =>
    terms.includes(p) ? (
      <mark key={i} style={{ background: 'rgba(210,153,34,0.30)', color: '#E7E9EA', padding: '0 2px', borderRadius: 2 }}>{p}</mark>
    ) : (
      <span key={i}>{p}</span>
    ),
  )
}

/** EditorPage · 职责说明：对照编辑器页面，双栏展示源文与可编辑译文，支持逐段修改/通过/驳回并保存到后端 */
export default function EditorPage() {
  useLang() // ★ F2：语言切换即时重渲染（状态选项等）
  // 组件内提示走 ToastProvider；本页所有反馈（保存成功/失败、需先解析工单）都经它，不再引 MessagePlugin
  const { toast } = useToast()

  const [ticketId, setTicketId] = useState('')
  const [lang, setLang] = useState('en')
  const [langs, setLangs] = useState<string[]>([])
  const [type, setType] = useState('')
  const [segments, setSegments] = useState<EditorSegment[]>([])
  const [terms, setTerms] = useState<string[]>([])
  const [rows, setRows] = useState<Record<number, RowState>>({})
  const [loading, setLoading] = useState(false)

  // load 加载工单分段：输入为数字 ID 走 getSegments，为工单号（T 开头）走 getSegmentsByKey，并初始化每行的编辑态
  const load = useCallback(async () => {
    const raw = String(ticketId || '').trim()
    if (!raw) {
      toast({ title: t('tk.edNeedId'), tone: 'warn' })
      return
    }
    const id = Number(raw)
    if (!id) {
      // 粘贴了工单号（T 开头非数字）→ 由后端按 ticket_no 回查，直接传字符串
      setLoading(true)
      try {
        const resp = await getSegmentsByKey(raw, lang)
        if (!resp.success) {
          toast({ title: resp.message || t('tk.edLoadFail'), tone: 'error' })
          return
        }
        setSegments(resp.segments || [])
        setTerms(resp.terms || [])
        setType(resp.type || 'text')
        setLangs(resp.langs || (resp.lang ? [resp.lang] : []))
        // ★ E18：工单号解析成功后把输入框归一为数字 ID，后续保存/重载不再走 T 号分支
        if (resp.ticket_id) setTicketId(String(resp.ticket_id))
        const init: Record<number, RowState> = {}
        for (const s of resp.segments || []) {
          init[s.index] = {
            edited_text: s.edited_text || s.target,
            status: s.status || 'pending',
            note: s.note || '',
          }
        }
        setRows(init)
      } catch (e) {
        toast({ title: tpl('tk.edLoadFailErr', { err: String(e) }), tone: 'error' })
      } finally {
        setLoading(false)
      }
      return
    }
    setLoading(true)
    try {
      const resp = await getSegments(id, lang)
      if (!resp.success) {
        toast({ title: resp.message || t('tk.edLoadFail'), tone: 'error' })
        return
      }
      setSegments(resp.segments || [])
      setTerms(resp.terms || [])
      setType(resp.type || 'text')
      setLangs(resp.langs || (resp.lang ? [resp.lang] : []))
      const init: Record<number, RowState> = {}
      for (const s of resp.segments || []) {
        init[s.index] = {
          edited_text: s.edited_text || s.target,
          status: s.status || 'pending',
          note: s.note || '',
        }
      }
      setRows(init)
    } catch (e) {
      toast({ title: tpl('tk.edLoadFailErr', { err: String(e) }), tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [ticketId, lang])

  // rowOf 取某分段的本地编辑态，尚未编辑时回退到系统译文/状态/批注
  const rowOf = (s: EditorSegment): RowState =>
    rows[s.index] || { edited_text: s.edited_text || s.target, status: s.status || 'pending', note: s.note || '' }

  // getRow 按序号取本地编辑态，无记录时返回空默认值
  const getRow = (idx: number): RowState => rows[idx] || { edited_text: '', status: 'pending', note: '' }

  // update 局部更新某分段的编辑态字段（与已有状态合并）
  const update = (idx: number, patch: Partial<RowState>) =>
    setRows((prev) => ({ ...prev, [idx]: { ...getRow(idx), ...patch } }))

  // dirtyEdits 对比系统原值，筛出有改动的分段列表（供保存时提交给后端）
  const dirtyEdits = useMemo<SegmentEdit[]>(() => {
    const out: SegmentEdit[] = []
    for (const s of segments) {
      const r = rowOf(s)
      if (r.edited_text !== (s.edited_text || s.target) || r.status !== (s.status || 'pending') || r.note !== (s.note || '')) {
        out.push({ index: s.index, edited_text: r.edited_text, status: r.status, note: r.note })
      }
    }
    return out
  }, [segments, rows])

  // save 将有改动的分段提交到后端保存，成功后提示并重新加载
  const save = useCallback(async () => {
    // ★ E18：数字工单 ID 校验——旧实现直接 Number(ticketId)，粘贴工单号（T 开头）时得 NaN
    //   仍照发请求（?id=NaN）。数字 ID 已由 load 回填归一，此处仅兜底拦截。
    const id = Number(ticketId)
    if (!Number.isInteger(id) || id <= 0) {
      toast({ title: t('tk.edResolveFirst'), tone: 'warn' })
      return
    }
    if (!dirtyEdits.length) {
      toast({ title: t('tk.edNoChanges'), tone: 'success' })
      return
    }
    setLoading(true)
    try {
      const resp = await saveSegments(id, lang, dirtyEdits)
      if (resp.success) {
        toast({ title: tpl('tk.edSavedN', { n: Number(resp.saved ?? dirtyEdits.length) }), tone: 'success' })
        await load()
      } else {
        toast({ title: resp.message || t('tk.edSaveFail'), tone: 'error' })
      }
    } catch (e) {
      toast({ title: tpl('tk.edSaveFailErr', { err: String(e) }), tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [dirtyEdits, ticketId, lang, load])

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto', padding: 16, width: '100%', minWidth: 0 }}>
      <h2 style={{ margin: '8px 0' }}>{t('tk.edTitle')}</h2>
      <div className="editor-toolbar" style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 12 }}>
        <Input placeholder={t("tk.edIdPlaceholder")} value={ticketId} onChange={(e) => setTicketId(String(e.target.value))} style={{ width: 160 }} />
        {/* 语种下拉：空值项复用列头文案作占位。选空时后端 parseTicketIDLang 会回退到
            工单首个目标语种（再兜底 en），故「不选」也是可用路径而不是错误态 */}
        <select className="lc-input" value={lang} onChange={(e) => setLang(e.target.value)} style={{ width: 140 }}>
          <option value="">{t('tk.edColLang')}</option>
          {langs.map((l) => (
            <option key={l} value={l}>{l}</option>
          ))}
        </select>
        <Button variant="secondary" onClick={load} disabled={loading}>{t('tk.edLoad')}</Button>
        <Button variant="primary" onClick={save} disabled={loading || !segments.length}>{t('tk.edSave')}</Button>
        {dirtyEdits.length > 0 && <StatusPill tone="warn">{tpl('tk.pendingSaveFmt', { n: dirtyEdits.length })}</StatusPill>}
      </div>

      {/* type=unsupported：后端判定「文件工单但格式无法逐段对照」（非 xlsx/csv 对照表），
          此时只给提示横幅、不渲染空表格，避免用户对着空编辑器以为数据丢了 */}
      {type === 'unsupported' && (
        <div style={{ padding: 12, background: 'rgba(210,153,34,0.10)', border: '1.2px solid rgba(210,153,34,0.32)', borderRadius: 6, marginBottom: 12 }}>
          {t('tk.fileOnlyEditTip')}
        </div>
      )}

      {/* 命中术语仅作概览（后端最多回 200 条）：只渲染前 30 个，其余仍参与左侧高亮 */}
      {terms.length > 0 && (
        <div style={{ marginBottom: 12 }}>
          <span style={{ color: '#888', marginRight: 6 }}>{t('tk.edTermsHit')}</span>
          {terms.slice(0, 30).map((t, i) => (
            <StatusPill key={i} tone="idle" className="ed-term">{t}</StatusPill>
          ))}
        </div>
      )}

      {segments.map((s) => {
        const r = rowOf(s)
        // 状态底色只用 6%~10% 低透明层（纯黑体系禁止大色块铺底）：通过=提亮、驳回=语义红薄底，
        // pending 保持面板底色
        return (
          <div
            key={s.index}
            className="ed-seg"
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr',
              gap: 12,
              padding: 12,
              border: '1.2px solid #464C58',
              borderRadius: 8,
              marginBottom: 12,
              background: r.status ==='approved'?'rgba(231,233,234,0.06)': r.status ==='rejected'?'rgba(229,72,77,0.10)':'#0E1014',
            }}
          >
            <div>
              <div style={{ fontSize: 12, color: '#999', marginBottom: 4 }}>{tpl('tk.srcIdxFmt', { i: s.index + 1 })}</div>
              <div style={{ whiteSpace: 'pre-wrap', minHeight: 40 }}>{highlightTerms(s.source, terms)}</div>
            </div>
            <div>
              <div style={{ fontSize: 12, color: '#999', marginBottom: 4 }}>
                {tpl('tk.edTargetTpl', { state: s.target ? t('tk.edHas') : t('tk.edEmpty') })}
              </div>
              <textarea
                className="lc-textarea"
                value={r.edited_text}
                onChange={(e) => update(s.index, { edited_text: e.target.value })}
                aria-label={t('tk.edTargetAria')}
                rows={3}
                style={{ minHeight: 56, maxHeight: 220, resize: 'vertical' }}
              />
              <div style={{ display: 'flex', gap: 8, marginTop: 6, alignItems: 'center' }}>
                <select
                  className="lc-select"
                  value={r.status}
                  onChange={(e) => update(s.index, { status: e.target.value })}
                  style={{ width: 120 }}
                >
                  {statusOptions().map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </select>
                <Input
                  placeholder={t('tk.edNotePlaceholder')}
                  value={r.note}
                  onChange={(e) => update(s.index, { note: e.target.value })}
                  style={{ flex: 1 }}
                />
              </div>
            </div>
          </div>
        )
      })}

      {!loading && segments.length === 0 && (
        <div style={{ color: '#999', padding: 24, textAlign: 'center' }}>{t('tk.edEmptyHint')}</div>
      )}
    </div>
  )
}
