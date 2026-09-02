// ============================================================================
// components/EditorPage.tsx — 对照编辑器（工作流 D，新 feature）
// 双栏：左=源文（只读）+ 术语高亮；右=可编辑译文 + 通过/驳回批注。
// 文本工单解析 FinalResult；文件工单解析 xlsx/csv 对照表产物（后端负责）。
// 逐段保存至后端 translation_edits，状态 pending/approved/rejected。
// ============================================================================
import { useCallback, useMemo, useState } from 'react'
import { Button, Input, Select, Textarea, Tag, MessagePlugin } from 'tdesign-react'
import { getSegments, getSegmentsByKey, saveSegments, type EditorSegment, type SegmentEdit } from '@/api/tickets'

/** 行本地编辑态 */
interface RowState {
  edited_text: string
  status: string
  note: string
}

// STATUS_OPTIONS 译文状态选项：待处理/通过/驳回，供每行的状态下拉框选择
const STATUS_OPTIONS = [
  { label: '待处理', value: 'pending' },
  { label: '通过', value: 'approved' },
  { label: '驳回', value: 'rejected' },
]

/** 将源文中命中的术语串包裹为高亮 <mark> */
function highlightTerms(text: string, terms: string[]): React.ReactNode {
  if (!terms.length) return text
  const escaped = terms
    .filter((t) => t && t.length > 1)
    .map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  if (!escaped.length) return text
  const re = new RegExp(`(${escaped.join('|')})`, 'g')
  const parts = text.split(re)
  return parts.map((p, i) =>
    terms.includes(p) ? (
      <mark key={i} style={{ background: '#fff3a3', padding: '0 2px', borderRadius: 2 }}>{p}</mark>
    ) : (
      <span key={i}>{p}</span>
    ),
  )
}

/** EditorPage · 职责说明：对照编辑器页面，双栏展示源文与可编辑译文，支持逐段修改/通过/驳回并保存到后端 */
export default function EditorPage() {
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
      void MessagePlugin.warning('请输入工单 ID 或工单号（如 T20260902…）')
      return
    }
    const id = Number(raw)
    if (!id) {
      // 粘贴了工单号（T 开头非数字）→ 由后端按 ticket_no 回查，直接传字符串
      setLoading(true)
      try {
        const resp = await getSegmentsByKey(raw, lang)
        if (!resp.success) {
          void MessagePlugin.error(resp.message || '加载失败')
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
        void MessagePlugin.error('加载失败：' + String(e))
      } finally {
        setLoading(false)
      }
      return
    }
    setLoading(true)
    try {
      const resp = await getSegments(id, lang)
      if (!resp.success) {
        void MessagePlugin.error(resp.message || '加载失败')
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
      void MessagePlugin.error('加载失败：' + String(e))
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
    const id = Number(ticketId)
    if (!dirtyEdits.length) {
      void MessagePlugin.info('没有改动')
      return
    }
    setLoading(true)
    try {
      const resp = await saveSegments(id, lang, dirtyEdits)
      if (resp.success) {
        void MessagePlugin.success(`已保存 ${resp.saved ?? dirtyEdits.length} 段`)
        await load()
      } else {
        void MessagePlugin.error(resp.message || '保存失败')
      }
    } catch (e) {
      void MessagePlugin.error('保存失败：' + String(e))
    } finally {
      setLoading(false)
    }
  }, [dirtyEdits, ticketId, lang, load])

  return (
    <div style={{ maxWidth: 1100, margin: '0 auto', padding: 16 }}>
      <h2 style={{ margin: '8px 0' }}>✍️ 对照编辑器</h2>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginBottom: 12 }}>
        <Input placeholder="工单 ID" value={ticketId} onChange={(v) => setTicketId(String(v))} style={{ width: 160 }} />
        <Select
          value={lang}
          onChange={(v) => setLang(String(v))}
          options={langs.map((l) => ({ label: l, value: l }))}
          style={{ width: 140 }}
          placeholder="语言"
        />
        <Button theme="primary" onClick={load} loading={loading}>加载</Button>
        <Button theme="success" onClick={save} loading={loading} disabled={!segments.length}>保存改动</Button>
        {dirtyEdits.length > 0 && <Tag theme="warning">待保存 {dirtyEdits.length} 段</Tag>}
      </div>

      {type === 'unsupported' && (
        <div style={{ padding: 12, background: '#fff7e6', border: '1px solid #ffd591', borderRadius: 6, marginBottom: 12 }}>
          该工单为文件类型且产物不支持在线逐段编辑（仅 xlsx/csv 对照表支持）。请下载产物校对。
        </div>
      )}

      {terms.length > 0 && (
        <div style={{ marginBottom: 12 }}>
          <span style={{ color: '#888', marginRight: 6 }}>命中术语：</span>
          {terms.slice(0, 30).map((t, i) => (
            <Tag key={i} style={{ marginRight: 4 }}>{t}</Tag>
          ))}
        </div>
      )}

      {segments.map((s) => {
        const r = rowOf(s)
        return (
          <div
            key={s.index}
            style={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr',
              gap: 12,
              padding: 12,
              border: '1px solid #eee',
              borderRadius: 8,
              marginBottom: 12,
              background: r.status === 'approved' ? '#f6ffed' : r.status === 'rejected' ? '#fff1f0' : '#fff',
            }}
          >
            <div>
              <div style={{ fontSize: 12, color: '#999', marginBottom: 4 }}>源文 #{s.index + 1}</div>
              <div style={{ whiteSpace: 'pre-wrap', minHeight: 40 }}>{highlightTerms(s.source, terms)}</div>
            </div>
            <div>
              <div style={{ fontSize: 12, color: '#999', marginBottom: 4 }}>
                译文（系统：{s.target ? '有' : '空'}）
              </div>
              <Textarea
                value={r.edited_text}
                onChange={(v) => update(s.index, { edited_text: String(v) })}
                autosize={{ minRows: 2, maxRows: 8 }}
              />
              <div style={{ display: 'flex', gap: 8, marginTop: 6, alignItems: 'center' }}>
                <Select
                  value={r.status}
                  onChange={(v) => update(s.index, { status: String(v) })}
                  options={STATUS_OPTIONS}
                  style={{ width: 120 }}
                />
                <Input
                  placeholder="批注/驳回原因"
                  value={r.note}
                  onChange={(v) => update(s.index, { note: String(v) })}
                  style={{ flex: 1 }}
                />
              </div>
            </div>
          </div>
        )
      })}

      {!loading && segments.length === 0 && (
        <div style={{ color: '#999', padding: 24, textAlign: 'center' }}>输入工单 ID 并点击「加载」开始逐段校对</div>
      )}
    </div>
  )
}
