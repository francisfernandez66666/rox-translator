// ============================================================================
// components/EditorPage.tsx — 对照编辑器（工作流 D，新 feature）
// 双栏：左=源文（只读）+ 术语高亮；右=可编辑译文 + 通过/驳回批注。
// 文本工单解析 FinalResult；文件工单解析 xlsx/csv 对照表产物（后端负责）。
// 逐段保存至后端 translation_edits，状态 pending/approved/rejected。
// 呈现层已迁 @/ui/langcross/src：TDesign Dialog/Select/Textarea/Tag/MessagePlugin
// → 原生 select/textarea + lc-* 类 + StatusPill + useToast（一屏一个 primary）。
// ★ 2026-09-19 B2 计算收敛（性能方案 C0，先于 C1 虚拟化）：
//   ① 术语高亮正则从「每段每次渲染重编译」收敛为每次加载编译一次（buildTermMatcher，
//      词数封顶 + 长词优先），命中判定用 Set；
//   ② 行拆成 memo 化 SegRow：译文/批注改非受控（defaultValue+onBlur 提交），
//      键入期间零状态更新、零整表重渲染（旧版 578 段每敲一键全表重排）；
//   ③ update 稳定引用（useCallback 空依赖读 prev），不再从闭包 rows 取旧值；
//   ④ loadSeq 进 key：重新加载即整表重挂载，非受控框复位到服务端最新值。
// ★ 2026-09-19 B5 虚拟化（方案 C1，接在 B2 计算收敛之后）：
//   大表（>VIRTUALIZE_THRESHOLD 段）改 react-virtuoso 窗口滚动列表——DOM 里只保留
//   视口±缓冲的行（578 段大表不再一次性挂 578 个 textarea）；行高动态（源文长短、
//   textarea 手动拉伸）由 defaultItemHeight 起步 + 组件内 ResizeObserver 自动校正。
//   小表保持直渲染（零虚拟化开销，测试/编辑语义不变）；#seg-N 锚点直达在两种形态
//   下都可用（虚拟化走 scrollToIndex 先对齐再等挂载，非虚拟化走原生 scrollIntoView）。
// ============================================================================
import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso'
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

/** ★ B5 小表直渲染阈值：超过该段数才进 virtuoso 窗口列表。
 *  50 段以内 DOM 压力可忽略，直渲染保住「全行在场」的编辑/测试语义（B2 断言依赖）。 */
const VIRTUALIZE_THRESHOLD = 50

// ============ ★ B2 术语高亮匹配器（纯函数，编译成本从 O(段数×词数×渲染) 收敛到 O(词数)） ============
/** 参与高亮的术语条数封顶：后端最多回 200 条，全量拼交替式会让每段的 split 正则在
 *  大表上重新变贵；按长度降序保留前 60 条（长术语优先命中，短词被长词覆盖损失最小）。 */
const HIGHLIGHT_TERM_CAP = 60

/** TermMatcher 高亮匹配器的编译产物：re 供正则整体扫描，set 供逐词精确判定；
 *  re 为 null 表示当前无可用术语（术语表为空或全被裁掉），调用方须短路。 */
interface TermMatcher { re: RegExp | null; set: Set<string> }

/** buildTermMatcher 编译一次高亮匹配器：去重、丢 ≤1 字词（噪声大于收益）、转义正则元字符
 *  （术语来自知识库，可能含 . + ( )，不转义会把「C++」当量词）、长词优先排序后封顶。
 *  re 与 set 必须同源构造（同一份 usable）：命中判定用 Set 精确比对 split 的捕获组，
 *  若两侧口径不一致，就会出现「正则匹配到了但 Set 不认」而静默漏高亮。
 *  无可用术语时 re 置 null，highlightWith 直接短路返回原文，省掉整表 split。 */
function buildTermMatcher(terms: string[]): TermMatcher {
  const usable = Array.from(new Set(terms.filter((x) => !!x && x.length > 1)))
    .sort((a, b) => b.length - a.length)
    .slice(0, HIGHLIGHT_TERM_CAP)
  if (!usable.length) return { re: null, set: new Set() }
  const escaped = usable.map((x) => x.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  return { re: new RegExp(`(${escaped.join('|')})`, 'g'), set: new Set(usable) }
}

/** highlightWith 把源文中命中的术语串包裹为 <mark>（split 捕获组法与旧版一致，仅换查找结构）
 *  不用 dangerouslySetInnerHTML/字符串拼接：源文可能含 < > 等字符，走 JSX 才由 React 转义。 */
function highlightWith(text: string, m: TermMatcher): React.ReactNode {
  if (!m.re) return text
  const parts = text.split(m.re)
  return parts.map((p, i) =>
    // split 带捕获组时会把命中的术语本身也放进数组，故奇偶位无需判断，直接问 Set
    m.set.has(p) ? (
      <mark key={i} style={{ background: 'rgba(210,153,34,0.30)', color: '#E7E9EA', padding: '0 2px', borderRadius: 2 }}>{p}</mark>
    ) : (
      <span key={i}>{p}</span>
    ),
  )
}

// ============ ★ B2 行组件：memo 化，仅自身值/术语/状态选项变化时重渲染 ============
// memo 用的是默认浅比较，因此能生效的前提在父组件那侧：
// matcher / opts 走 useMemo、update 走空依赖 useCallback，s 直接取 segments 数组元素（重载前引用稳定），
// 真正逐段会变的只剩 editedText/status/note——任何一项退化成新对象/新函数，整表 memo 立即失效。
interface SegRowProps {
  s: EditorSegment
  editedText: string
  status: string
  note: string
  matcher: TermMatcher
  opts: { label: string; value: string }[]
  update: (idx: number, patch: Partial<RowState>) => void
}

/** SegRow 单段行组件：用 memo 包住，让「改一段」只重渲染那一行——
 *  整表重渲染在千段工单上会明显卡顿（父组件每次输入都会新建 props，故必须逐行 memo）。 */
const SegRow = memo(function SegRow({ s, editedText, status, note, matcher, opts, update }: SegRowProps) {
  // 状态底色只用 6%~10% 低透明层（纯黑体系禁止大色块铺底）：通过=提亮、驳回=语义红薄底，
  // pending 保持面板底色
  // id=seg-N 是 #/editor#seg-N 锚点的落点（非虚拟化形态下由 scrollIntoView 直接命中）；
  // 虚拟化时该行可能根本没挂载，父级改用 scrollToIndex 对齐窗口，见下方锚点 effect。
  // 描边走 --lc-border-card：本页在登录态扫描范围内，写死 #464C58 会红 readability.test.ts 的 #68 描边锁。
  return (
    <div
      id={`seg-${s.index}`}
      className="ed-seg"
      style={{
        display: 'grid',
        gridTemplateColumns: '1fr 1fr',
        gap: 12,
        padding: 12,
        border: '2px solid var(--lc-border-card)',
        borderRadius: 8,
        marginBottom: 12,
        background: status === 'approved' ? 'rgba(231,233,234,0.06)' : status === 'rejected' ? 'rgba(229,72,77,0.10)' : '#0E1014',
      }}
    >
      <div>
        <div style={{ fontSize: 14, color: 'var(--lc-text-3)', marginBottom: 4 }}>{tpl('tk.srcIdxFmt', { i: s.index + 1 })}</div>
        <div style={{ whiteSpace: 'pre-wrap', minHeight: 40 }}>{highlightWith(s.source, matcher)}</div>
      </div>
      <div>
        <div style={{ fontSize: 14, color: 'var(--lc-text-3)', marginBottom: 4 }}>
          {tpl('tk.edTargetTpl', { state: s.target ? t('tk.edHas') : t('tk.edEmpty') })}
        </div>
        {/* ★ B2 非受控：defaultValue 只做初值，键入不进 state——blur 时值有变化才提交一行。
            点「保存」前 mousedown 已触发 blur，未模糊的编辑不会丢（同 Excel 提交语义） */}
        <textarea
          className="lc-textarea"
          defaultValue={editedText}
          onBlur={(e) => { const v = e.target.value; if (v !== editedText) update(s.index, { edited_text: v }) }}
          aria-label={t('tk.edTargetAria')}
          rows={3}
          style={{ minHeight: 56, maxHeight: 220, resize: 'vertical' }}
        />
        <div style={{ display: 'flex', gap: 8, marginTop: 6, alignItems: 'center' }}>
          {/* 状态选择保持受控：低频离散操作，改一次整表也仅重算一次 dirtyEdits */}
          <select
            className="lc-select"
            value={status}
            onChange={(e) => update(s.index, { status: e.target.value })}
            style={{ width: 120 }}
          >
            {opts.map((o) => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
          {/* 批注同样非受控（原生 input 承接原 <Input>，同 lc-input 样式） */}
          <input
            className="lc-input"
            defaultValue={note}
            placeholder={t('tk.edNotePlaceholder')}
            onBlur={(e) => { const v = e.target.value; if (v !== note) update(s.index, { note: v }) }}
            style={{ flex: 1 }}
          />
        </div>
      </div>
    </div>
  )
})

/** EditorPage · 职责说明：对照编辑器页面，双栏展示源文与可编辑译文，支持逐段修改/通过/驳回并保存到后端 */
export default function EditorPage() {
  const langCode = useLang() // ★ F2：语言切换即时重渲染（状态选项等）
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
  // ★ B2：每次成功加载 +1 并进 SegRow 的 key——非受控框只认初值，
  // 换 key 强制整表重挂载，重新加载后 DOM 值与 rows state 必然一致
  const [loadSeq, setLoadSeq] = useState(0)

  // ★ B2：高亮匹配器随术语表编译一次（旧版在每段每次渲染里 new RegExp）
  const matcher = useMemo(() => buildTermMatcher(terms), [terms])
  // 状态选项随语言切换重建（新引用令所有 SegRow 同帧刷新文案）
  const opts = useMemo(() => statusOptions(), [langCode])

  // ★ B5：大表进 virtuoso；ref 用于 #seg-N 锚点直达时先对齐窗口
  const virtualized = segments.length > VIRTUALIZE_THRESHOLD
  const virtuosoRef = useRef<VirtuosoHandle>(null)

  // ★ B5 锚点直达：/editor#seg-<index> 在加载完成后滚到对应段。
  //   虚拟化形态下目标行可能未挂载，必须先 scrollToIndex 让窗口覆盖它；
  //   延迟一拍等首帧布局完成（数据刚落库时行高仍按 defaultItemHeight 估算）。
  useEffect(() => {
    const m = /^#seg-(\d+)$/.exec(window.location.hash || '')
    if (!m || !segments.length) return
    const target = Number(m[1])
    const pos = segments.findIndex((s) => s.index === target)
    if (pos < 0) return
    const timer = setTimeout(() => {
      if (virtualized) virtuosoRef.current?.scrollToIndex({ index: pos, align: 'start' })
      else document.getElementById(`seg-${target}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }, 60)
    return () => clearTimeout(timer)
  }, [segments, virtualized])

  // load 加载工单分段：输入为数字 ID 走 getSegments，为工单号（T 开头）走 getSegmentsByKey，并初始化每行的编辑态
  const load = useCallback(async () => {
    const raw = String(ticketId || '').trim()
    if (!raw) {
      toast({ title: t('tk.edNeedId'), tone: 'warn' })
      return
    }
    const id = Number(raw)
    // !id 一并挡掉 NaN（粘贴 T 号）与 0（空/非法数字）两种假值，故此处不再区分是哪种
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
        setLoadSeq((n) => n + 1) // ★ B2：整表重挂载，非受控框复位到刚拉取的服务端值
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
      setLoadSeq((n) => n + 1) // ★ B2：同上，数字 ID 分支加载成功后整表重挂载
    } catch (e) {
      toast({ title: tpl('tk.edLoadFailErr', { err: String(e) }), tone: 'error' })
    } finally {
      setLoading(false)
    }
  }, [ticketId, lang])

  // rowOf 取某分段的本地编辑态，尚未编辑时回退到系统译文/状态/批注
  const rowOf = (s: EditorSegment): RowState =>
    rows[s.index] || { edited_text: s.edited_text || s.target, status: s.status || 'pending', note: s.note || '' }

  // update 局部更新某分段的编辑态字段（与已有状态合并）
  // ★ B2 稳定引用：空依赖 useCallback + updater 内读 prev（旧版从闭包 rows 经 getRow 取旧值，
  //   每次渲染都是新函数，会把 memo 化的 SegRow 全部击穿）
  const update = useCallback((idx: number, patch: Partial<RowState>) => {
    setRows((prev) => ({
      ...prev,
      [idx]: { ...(prev[idx] || { edited_text: '', status: 'pending', note: '' }), ...patch },
    }))
  }, [])

  // dirtyEdits 对比系统原值，筛出有改动的分段列表（供保存时提交给后端）
  // 只依赖 [segments, rows]：键入期间两者都不变（非受控），所以大表不会每敲一键重算一遍；
  // 「原值」必须现取 s.edited_text || s.target（服务端可能已回写过），不能拿挂载时的快照比。
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
        <div style={{ padding: 12, background: 'rgba(210,153,34,0.10)', border: '2px solid rgba(210,153,34,0.32)', borderRadius: 6, marginBottom: 12 }}>
          {t('tk.fileOnlyEditTip')}
        </div>
      )}

      {/* 命中术语仅作概览（后端最多回 200 条）：只渲染前 30 个；
          ★ B2 高亮另按「长词优先 + 前 60 条」封顶（buildTermMatcher），防大词表拖慢每段 split */}
      {terms.length > 0 && (
        <div style={{ marginBottom: 12 }}>
          <span style={{ color: 'var(--lc-text-3)', marginRight: 6 }}>{t('tk.edTermsHit')}</span>
          {terms.slice(0, 30).map((t, i) => (
            <StatusPill key={i} tone="idle" className="ed-term">{t}</StatusPill>
          ))}
        </div>
      )}

      {/* ★ B2 行渲染：memo 化 SegRow + loadSeq 前缀 key（重载即整表重挂载复位非受控框）。
          键入不再触发父级 state，578 段大表敲一键只更新一个 textarea 的 DOM。
          ★ B5：大表（>阈值）套 virtuoso 窗口列表——DOM 行数收敛到视口±缓冲；
          computeItemKey 仍带 loadSeq 前缀，重载后行组件必然重挂载。
          useWindowScroll：滚动交给页面本身（不是内层滚动容器），与本页其余形态以及
          #seg-N 锚点的 scrollIntoView 保持同一条滚动条；
          defaultItemHeight=150 只是起点值，真实行高由组件侧 ResizeObserver 逐行校正
          （源文长短、textarea 手动拉伸都会改变它，写死高度必错）；
          initialItemCount=8 让首帧先渲染 8 行，避免未滚动时出现空白区；
          increaseViewportBy 上 400 / 下 800 是刻意不对称：阅读方向向下，向下多留缓冲。 */}
      {virtualized ? (
        <Virtuoso
          ref={virtuosoRef}
          useWindowScroll
          data={segments}
          defaultItemHeight={150}
          initialItemCount={8}
          increaseViewportBy={{ top: 400, bottom: 800 }}
          computeItemKey={(_, s) => `${loadSeq}:${s.index}`}
          itemContent={(_, s) => {
            const r = rowOf(s)
            return (
              <SegRow s={s} editedText={r.edited_text} status={r.status} note={r.note}
                      matcher={matcher} opts={opts} update={update} />
            )
          }}
        />
      ) : (
        segments.map((s) => {
          const r = rowOf(s)
          return (
            <SegRow key={`${loadSeq}:${s.index}`} s={s} editedText={r.edited_text} status={r.status} note={r.note}
                    matcher={matcher} opts={opts} update={update} />
          )
        })
      )}

      {!loading && segments.length === 0 && (
        <div style={{ color: 'var(--lc-text-3)', padding: 24, textAlign: 'center' }}>{t('tk.edEmptyHint')}</div>
      )}
    </div>
  )
}
