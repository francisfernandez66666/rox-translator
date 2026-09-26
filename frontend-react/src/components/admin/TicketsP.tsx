// ============================================================================
// components/admin/TicketsP.tsx — 反馈 / 审批台 / TM 审核面板
// 职责：用户反馈、审批工单、TM 记忆审核三合一工作台
// 从 panels_d.tsx 拆分
// 2026-09-18：仅做图标位清理（💬📚✔✘✅🎫📝⚡🎓👁 等 emoji 从文案里移除），
//   组件已全部迁至 ui/langcross，数据获取与审批/复核逻辑未动。
//   ① TM 复核「通过/驳回」改用 Link + 文案；② 反馈表 mode 列补 Icon。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
// 2026-09-18：本面板整体迁到 ui/langcross（DataTable / Link / StatusPill / Dialog），
//   图标位统一走 <Icon n="...">，不再把 emoji 混进文案里（文案与图标彻底分离，暗色模式才能一起换色）
import { Button, DataTable, Dialog, Icon, Link, StatusPill } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import {
  approveList, approveAction,
  feedbackList, feedbackReply, resolveFeedback, createFeedback,
  listTmReview, approveTmReview, rejectTmReview, type Any,
} from '@/api'
import { Panel, Field, toastResp } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
// ★ F-59（批 I-8）：回复署名的「管理侧」判据取仓内既有角色分级（roleLevel>=2 = 主管及以上）
import { roleLevel } from '@/stores/auth'
// toastBus 是同步总线（不返回 Promise），替代 MessagePlugin 后调用点不再需要 void 吞返回值
import { toastError, toastWarn } from '@/lib/toastBus'
// ★ F-45（〇-U 批）：反馈上下文脏值（库里存过字面量 "null"）的「必须是对象」解析口径
import { parseStringMap } from '@/lib/safeJson'
import { fmtDateTime } from '../../lib/format'

// 行/卡片布局样式（横向排布 + 顶距/描边变体）
const rowMt: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 8 }
// 卡片容器样式（描边圆角 + 内边距）
const cardStyle: any = { border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: 14, marginBottom: 12 }

// firstTranslation 从工单 final_result JSON 中取第一个目标语种的译文（预览用；解析失败返回空串）。
function firstTranslation(finalResult: unknown): string {
  try {
    const p = typeof finalResult === 'string' && finalResult ? JSON.parse(finalResult) : null
    const tr = (p?.translations || {}) as Record<string, string>
    const k = Object.keys(tr)[0]
    return k ? tr[k] : ''
  } catch { return '' }
}

// ★ O-12（2026-09-26 〇-U 批 I-8）：匿名留资行判定。/api/lead 以 TenantID:0 / UserID:0 /
// TargetType:"lead" 复用 feedbacks 通道（本来就没有账号可指），旧写法在用户列渲染出 `#0`、
// 在对象列渲染成「文本」，运营读起来像一条脏数据。判据只认后端已有的 target_type，
// 不新造字段、不改落库口径；有名字的行一律先取名字（留资若将来带账号也不会被抹成访客）。
function isLeadRow(row: Any): boolean {
  return String(row?.target_type ?? '') === 'lead'
}

/** 反馈 / 审批台 / TM 审核面板 */
export function TicketsP() {
  // ===== 面板状态：工单/反馈/复核/审批四类列表与过滤条件、详情弹窗 =====
  const [, t, tpl] = useT()
  const { isSuper, activeTenantId, consumeFeedback } = useAdmin()
  // feedbacks 线上反馈列表；statusFilter 变化会重建 loadFeedbacks，所以过滤下拉一改就自动重拉
  const [feedbacks, setFeedbacks] = useState<Any[]>([])
  const [statusFilter, setStatusFilter] = useState('')
  // selected 详情态：非空即整块换成详情卡片（不是弹层），返回列表靠 setSelected(null)
  const [selected, setSelected] = useState<Any | null>(null)
  // replyDraft 回复草稿：切详情、发送成功都要手工清零，免得上一条串到下一条
  const [replyDraft, setReplyDraft] = useState('')
  // newContent/submitting 用户侧提交框与在途标志：反馈接口没有幂等键，双击就会留两条，故必须置灰
  const [newContent, setNewContent] = useState('')
  const [submitting, setSubmitting] = useState(false)
  // fbTab 超管专属页签；review 之外不挂复核队列，省掉每次进面板多拉一份平台级数据
  const [fbTab, setFbTab] = useState<'feedback' | 'review'>('feedback')
  // reviews/rvFilter 自迭代复核候选与其状态过滤（默认只看 pending，处理完的行会随重拉自动消失）
  const [reviews, setReviews] = useState<Any[]>([])
  const [rvFilter, setRvFilter] = useState('pending')
  // approvalTickets 审批台待办：进面板拉一次，之后只由「刷新」和每次批完重拉
  const [approvalTickets, setApprovalTickets] = useState<Any[]>([])
  // approveDlg 审批弹窗上下文（行 + 终稿草稿 + 理由 + 建议 + 本次动作），null 即关闭
  const [approveDlg, setApproveDlg] = useState<null | { row: Any; text: string; reason: string; suggestion: string; action: 'approve' | 'reject' }>(null)

  // loadFeedbacks 按状态过滤拉取线上反馈列表
  const loadFeedbacks = useCallback(async () => {
    const r = await feedbackList(statusFilter)
    if (r.success) setFeedbacks((r as unknown as { feedbacks?: Any[] }).feedbacks || [])
  }, [statusFilter])
  // loadReviews 拉取自迭代对照复核候选（TM review 队列）
  const loadReviews = useCallback(async () => {
    const r = await listTmReview(rvFilter)
    if (r.success) setReviews((r as unknown as { candidates?: Any[] }).candidates || [])
  }, [rvFilter])
  // loadApproval 拉取待审批工单队列
  const loadApproval = useCallback(async () => {
    const r = await approveList()
    if (r.success) setApprovalTickets((r as unknown as { tickets?: Any[] }).tickets || [])
  }, [])

  // 反馈与审批随租户切换一起刷；复核队列不在这里拉，见下一个 effect 的按需加载
  useEffect(() => { void loadFeedbacks(); void loadApproval() }, [activeTenantId, loadFeedbacks, loadApproval])
  // 复核候选是平台级数据：权限（isSuper）与视图（fbTab）双重收口，切走就不请求，避免无谓拉取
  useEffect(() => { if (isSuper && fbTab === 'review') void loadReviews() }, [isSuper, fbTab, loadReviews])
  // consumeFeedback 是「取完即清」的一次性跨页跳转参数，故依赖里只放函数本身——否则重渲染会反复跳回同一条
  useEffect(() => { const fid = consumeFeedback(); if (fid) void jumpFeedback(fid); /* eslint-disable-next-line */ }, [consumeFeedback])

  // openDetail 进详情：顺手清掉上一条的回复草稿（详情是复用同一块卡片渲染的）
  function openDetail(f: Any) { setSelected(f); setReplyDraft('') }
  // jumpFeedback 从复核来源列跳回对应反馈：先切页签、清状态过滤，否则目标很可能压根不在当前列表里
  async function jumpFeedback(fid: number) {
    setFbTab('feedback'); setStatusFilter('')
    await loadFeedbacks()
    // 注意：这里读的是本轮闭包的 feedbacks（setState 还没生效），命中不到时用户再点一次即可
    const f = feedbacks.find((x) => x.id === fid)
    if (f) openDetail(f)
  }
  // srcLabel TM 候选来源四态：双语句对文件 / TMX 导入 / 反馈回流 / 高频聚合
  function srcLabel(src: string) {
    return src === 'bitext' ? t('tmr.srcBitext') : src === 'tmx' ? t('tmr.srcTmx') : src === 'feedback' ? t('tmr.srcFeedback') : t('tmr.srcCount')
  }
  // ctxTranslations 反馈附带的多语上下文 JSON：脏数据一律降级为空对象，不让整个详情块崩掉
  // ★ F-45（〇-U 批）：口径收进 lib/safeJson.parseStringMap——原来这里只有 try/catch，
  // 而库里的脏值 "null" 是合法 JSON（parse 返回 null 不抛错），下一行 Object.entries(null)
  // 才炸，表现为「超管点进这条反馈，详情整块白屏」。
  function ctxTranslations(f: Any): Record<string, string> {
    return parseStringMap(f.translations_json)
  }
  // fmtAt ISO → 本地时间串；解析失败时原样回显，宁可难看也不给列表留 Invalid Date
  function fmtAt(iso: string): string {
    if (!iso) return ''
    const d = new Date(iso)
    return isNaN(+d) ? iso : fmtDateTime(d)
  }
  // submitFeedback 用户侧提交口（target_type 固定 text）：文件类反馈走工单流程，不从这里进
  async function submitFeedback() {
    const content = newContent.trim(); if (!content) return
    setSubmitting(true)
    try {
      const r = await createFeedback({ target_type: 'text', content })
      if (!r.success) { toastError(r.message); return }
      setNewContent(''); await loadFeedbacks()
    } finally { setSubmitting(false) } // 失败也要复位，否则提交按钮永久置灰
  }
  // doReply 用后端返回的完整 replies 覆盖本地：作者署名与时间以服务端为准，前端不自造一条拼上去
  async function doReply() {
    if (!selected || !replyDraft.trim()) return
    const r = await feedbackReply(Number(selected.id), replyDraft.trim())
    if (!r.success) { toastError(r.message); return }
    setSelected({ ...selected, replies: (r as unknown as { replies?: Any[] }).replies || [] })
    setReplyDraft('')
  }
  // doResolve 归档即结案（用户侧会看到已处理），所以先确认；成功后本地立刻改 status，
  // 让提示在当前详情视图即时生效，列表随后再整表重拉对齐
  async function doResolve() {
    if (!selected) return
    if (!(await confirmDialog({ body: t('fb.resolveConfirm') }))) return
    const r = await resolveFeedback(Number(selected.id))
    if (!r.success) { toastError(r.message); return }
    setSelected({ ...selected, status: 'resolved', handled_at: new Date().toISOString() })
    await loadFeedbacks()
  }
  // 复核通过=候选正式写入 TM（此后会被翻译命中），驳回=丢弃；两者都只重拉列表、不改本地行
  async function doApproveReview(c: Any) {
    const r = await approveTmReview(Number(c.id))
    if (!r.success) { toastError(r.message); return }
    await loadReviews()
  }
  async function doRejectReview(c: Any) {
    const r = await rejectTmReview(Number(c.id))
    if (!r.success) { toastError(r.message); return }
    await loadReviews()
  }
  // doApprove 卡片内联审批：末位参数是「改后的终稿」，内联入口恒传空串（改稿请走下方弹窗版）；
  // 理由/建议直接从行对象的 _reason/_suggestion 上取，见操作区那两个非受控 input
  async function doApprove(tk: Any, action: 'approve' | 'reject') {
    const r = await approveAction(Number(tk.id), action, tk._reason || '', tk._suggestion || '', '')
    if (!r.success) { toastError(r.message); return }
    // 手工清空即可：紧接着 loadApproval 会重建整个数组，不需要这两个字段参与响应式
    tk._reason = ''; tk._suggestion = ''
    await loadApproval()
  }

  return (
    <>
      <h2 style={{ margin: '4px 0 4px' }}>{t('fb.workbench')}</h2>
      <p style={{ fontSize: 15, color: 'var(--adm-hint)', margin: '0 0 12px' }}>{isSuper ? t('fb.superHint') : t('fb.userHint')}</p>

      {/* 两个页签只在超管视角出现：普通用户进来就是反馈列表，没有复核队列可看 */}
      {isSuper && (
        <div style={{ marginBottom: 12 }}>
          {/* 选中态用 primary/secondary 区分（原 outline/text 组合在新底座里没有对应档，
              视觉上由「描边=当前页」改为「实心=当前页」） */}
          <Button size="sm"variant={fbTab ==='feedback'?'primary':'secondary'} onClick={() => setFbTab('feedback')}> {t('fb.tabFeedback')}</Button>
          <Button size="sm"variant={fbTab ==='review'?'primary':'secondary'} onClick={() => setFbTab('review')}> {t('fb.tabReview')}</Button>
        </div>
      )}

      {isSuper && fbTab === 'review' && (
        <Panel title={t('fb.tabReview')}>
          <div style={rowMt}>
            <select className="lc-select" value={rvFilter} onChange={(e) => setRvFilter(e.target.value)} style={{ width: 160 }}>
              <option value="pending">{t('tmr.pending')}</option>
              <option value="approved">{t('tmr.approved')}</option>
              <option value="rejected">{t('tmr.rejected')}</option>
            </select>
            <Button size="sm" variant="secondary" onClick={() => void loadReviews()}>{t('tickets.refresh')}</Button>
          </div>
          {/* 数据表格 */}
          {/* 复核队列换成 DataTable：colKey/cell 改写为 key/render，rowKey 由字段名改成取 id 的函数 */}
          <DataTable<any> rowKey={(row) => String(row.id)} rows={reviews}
            columns={[
              { key: 'id', title: 'ID', width: 60 },
              // 原文/译文两列走 dim 弱化 + 兜底「—」：这两列宽度不定，缺值时也要占位否则行高跳动
              { key: 'zh', title: t('tmr.zh'), dim: true, render: (row) => String(row.zh ?? '—') },
              { key: 'trans', title: t('tmr.trans'), dim: true, render: (row) => String(row.trans ?? '—') },
              { key: 'lang', title: t('tmr.colLangs'), width: 90 },
              // 来源列带反馈回链：只有 ref_type=feedback 才有可跳转的对象，其余来源给了也无处可去
              { key: 'source', title: t('tmr.source'), width: 130, render: (row) => (
                <span>{srcLabel(row.source)}{row.ref_type === 'feedback' && <> · <a href="#" aria-label={t('tmr.linkFb')} onClick={(e: any) => { e.preventDefault(); void jumpFeedback(Number(row.ref_id)) }}>{t('tmr.linkFb')}#{row.ref_id}</a></>}</span>
              ) },
              { key: 'hit_count', title: t('tmr.hits'), width: 90 },
              // 状态三态用 StatusPill：approved=success、pending=warn（待办要醒目）、rejected=idle
              { key: 'status', title: t('users.colStatus' as never), width: 90, render: (row) =>
                <StatusPill tone={row.status === 'approved' ? 'success' : row.status === 'pending' ? 'warn' : 'idle'}>{row.status === 'approved' ? t('tmr.approved') : row.status === 'rejected' ? t('tmr.rejected') : t('tmr.pending')}</StatusPill> },
              // 通过/驳回：2026-09-18 由「清空后无子节点的色块按钮」补为 Icon+文案，
              // 动作语义不再只靠主题色辨认；非 pending 行不给操作（复核是单次决定，不做出台反悔）
              { key: 'op', title: t('org.colActions'), width: 120, render: (row) => row.status === 'pending' ? (
                <div style={{ display: 'flex', gap: 10, alignItems: 'center' }}>
                  <Link onClick={() => void doApproveReview(row)}><Icon n="checkcircle" /> {t('tmr.approved')}</Link>
                  <Link tone="danger" onClick={() => void doRejectReview(row)}><Icon n="close" /> {t('tmr.rejected')}</Link>
                </div>
              ) : <span /> },
            ]}  />
        </Panel>
      )}

      {/* 详情用行内卡片整体替换列表：原文/多语上下文/往来回复都是长文本，塞进弹层滚动体验很差 */}
      {selected && (
        <div style={cardStyle}>
          {/* ★ F-57（2026-09-26 〇-U 批 I-8）：返回列表时重取列表。
              详情态是**整体替换**列表渲染的（不是弹层），期间可能已回复/结案/复核，
              旧写法只 setSelected(null) ⇒ 回到的是进详情前那一份快照，
              刚处理完那条仍显示「待处理」，运营以为没生效又点一次。
              沿用本面板既有 loadFeedbacks()（同一读侧接口，不新造路径）。 */}
          <Button size="sm" variant="secondary" style={{ float: 'right' }} onClick={() => { setSelected(null); void loadFeedbacks() }}>← {t('fb.backToList')}</Button>
          <h3 style={{ marginTop: 0 }}>
            #{selected.id} · {selected.user_name || (isLeadRow(selected) ? t('fb.userLead' as never) : '#' + selected.user_id)}{' '}
            <StatusPill tone={selected.status === 'resolved' ? 'success' : 'warn'}>{selected.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen')}</StatusPill>
          </h3>
          {/* 反馈正文用 pre + pre-wrap：用户贴的报错/表格文本要保留换行，但又不能横向溢出卡片 */}
          <pre style={{ whiteSpace: 'pre-wrap', fontFamily: 'inherit', margin: 0 }}>{selected.content}</pre>
          {/* 带上下文提交的反馈才有这一块：逐语种列出当时实际送给模型的译文，是判断「模型看错上下文」还是「翻错」的依据 */}
          {selected.with_context && (
            <div style={{ background: 'var(--adm-soft)', border: '1.2px dashed var(--adm-line)', borderRadius: 8, padding: '8px 10px', marginTop: 8, fontSize: 14.5 }}>
              <b>{t('fb.ctxAttached')}</b>
              {selected.source_text && <pre style={{ whiteSpace: 'pre-wrap', margin: '4px 0' }}>{selected.source_text}</pre>}
              {Object.entries(ctxTranslations(selected)).map(([k, v]) => (
                <div key={k} style={{ marginTop: 4 }}><b>[{k}]</b> {String(v)}</div>
              ))}
            </div>
          )}
          {/* 往来回复：底色按角色区分、顺序完全照后端返回排，前端不再二次排序以免两端时间线不一致
              （★ 2026-09-22 还原：原浅色主题遗留的 #e8f0fe/#f5f6f8 蓝灰底已收敛为 --adm-* 暗色语义档） */}
          {selected.replies && selected.replies.length ? (
            <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 6 }}>
              {selected.replies.map((r: Any, i: number) => {
                // ★ F-59（2026-09-26 〇-U 批 I-8）：管理侧判据从 `role === 'admin'` 换成 roleLevel>=2。
                //   角色域的真实取值是 user / dept_admin / tenant_admin / super_admin（+历史 admin/approver，
                //   见 internal/iam/models.go 与 stores/auth.tsx 的 roleLevel 分级），**没有 'admin' 这个值**——
                //   于是租户管理员/部门管理员的平台方回复全被署成「用户」（本轮 UAT 实测：平台回复显示成
                //   「张三 · 用户」），底色也跟着错档。roleLevel 是仓内既有分级口径（>=2 = 主管及以上），
                //   不新造判据、不新增词条（tickets.roleAdmin/roleUser 两键 12 语种都在）。
                const staff = roleLevel(r.role as string) >= 2
                return (
                <div key={i} style={{ background: staff ? 'var(--adm-info-bg)' : 'var(--adm-soft)', borderRadius: 8, padding: '6px 10px', fontSize: 15 }}>
                  <div style={{ fontSize: 13, color: 'var(--adm-faint)', marginBottom: 2 }}>{r.name} · {staff ? t('tickets.roleAdmin') : t('tickets.roleUser')} · {fmtAt(r.at)}</div>
                  <div style={{ whiteSpace: 'pre-wrap' }}>{r.content}</div>
                </div>
                )
              })}
            </div>
          ) : <div style={{ fontSize: 14, color: 'var(--adm-faint)', marginTop: 8 }}>{t('fb.noReplies')}</div>}
          {/* 只有 open 态给「回复 / 完成」两个动作；归档后整块换成静态提示，
              免得在已结案的反馈上继续追加，让处理时限统计失真 */}
          {selected.status === 'open' ? (
            <div style={rowMt}>
              <input className="lc-input" value={replyDraft} onChange={(e) => setReplyDraft(e.target.value)} placeholder={t('fb.replyPlaceholder')} style={{ flex: 1 }} />
              <Button disabled={!replyDraft.trim()} onClick={() => void doReply()}>↩ {t('fb.reply')}</Button>
 {isSuper && <Button variant="primary"onClick={() => void doResolve()}> {t('fb.complete')}</Button>}
            </div>
) : <div style={{ fontSize: 14, color: 'var(--adm-ok-tx)', marginTop: 8 }}> {t('fb.archivedHint')}</div>}
        </div>
      )}

      {!selected && fbTab !== 'review' && (
        <>
          {/* 提交入口只在非超管视角渲染：超管自己提的测试反馈会混进待处理队列，干扰真实数据统计 */}
          {!isSuper && (
            <Panel title={t('fb.submitTitle')}>
              {/* maxLength 与下方 1000 计数同源：输入侧硬截断 + 展示侧提示，后端还有一道长度校验 */}
              <textarea className="lc-textarea" rows={3} maxLength={1000} value={newContent} onChange={(e) => setNewContent(e.target.value)} placeholder={t('fb.contentPlaceholder')} style={{ width: '100%', resize: 'vertical' }} />
              <div style={{ ...rowMt, marginTop: 8 }}>
                <span style={{ flex: 1, fontSize: 14, color: 'var(--adm-faint)' }}>{newContent.length}/1000</span>
                <Button variant="primary" disabled={!newContent.trim() || submitting} onClick={() => void submitFeedback()}>
                  {submitting ? t('fb.submitting') : t('fb.submit')}
                </Button>
              </div>
            </Panel>
          )}
          {/* 过滤状态下拉换原生 select：「全部」用一个 value="" 的 option 表达；
              选中即改 statusFilter，由 useCallback 依赖链自动触发重拉，不需要额外的查询按钮 */}
          <div style={rowMt}>
            <select className="lc-select" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} style={{ width: 160 }}>
              <option value="">{t('fb.filterAll')}</option>
              <option value="open">{t('fb.statusOpen')}</option>
              <option value="resolved">{t('fb.statusResolved')}</option>
            </select>
            <Button size="sm" variant="secondary" onClick={() => void loadFeedbacks()}>{t('tickets.refresh')}</Button>
            <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{tpl('fb.count', { n: feedbacks.length })}</span>
          </div>
          {/* 数据表格 */}
          {/* 外层 div 只为补回原来 Table 上的 marginTop（DataTable 不接受 style 透传，改成包一层） */}
          <div style={{ marginTop: 8 }}>
          <DataTable<any> rowKey={(row) => String(row.id)} rows={feedbacks}
            columns={[
              { key: 'id', title: 'ID', width: 60 },
              // 提交人列（★ O-12）：匿名留资行没有账号，如实标「留资访客」而不是 `#0`；
              // 真有 user_id 但没回名字的仍留 `#<id>`（那是另一种情况，别一并抹掉）。
              { key: 'user', title: t('users.colUser' as never), width: 130, render: (row) => row.user_name || (isLeadRow(row) ? t('fb.userLead' as never) : '#' + row.user_id) },
              // 目标列：挂在工单上的反馈给 #工单号，纯文本反馈没有可跳转对象（图标位待补，现留前导空格）
              // ★ O-12：lead 行从前也落进「文本」分支（对象列说谎），现按 target_type 单列一档。
              { key:'target', title: t('fb.colTarget'), width: 130, render: (row) => row.target_type ==='ticket'? ` #${row.ticket_id}` : row.target_type ==='lead'? ` ${t('fb.targetLead')}` : ` ${t('fb.targetText')}` },
              { key: 'content', title: t('fb.colContent'), dim: true, render: (row) => String(row.content ?? '—') },
              // mode 列 2026-09-18 补回：去 emoji 后两分支曾都渲染空串、整列空白，现用 <Icon> 区分
              // fast（快速）与 pro（精翻）两种模式（列宽 70 只够一个图标，要加文案得先扩宽）
              { key:'mode', title: t('fb.colMode'), width: 70, render: (row) => row.mode ==='fast' ? <Icon n="chat" /> : <Icon n="doc" /> },
              { key: 'created_at', title: t('overview.colTime'), width: 160, render: (row) => fmtAt(row.created_at) },
              // resolved=success、open=warn：待处理用 warn 而非 idle，是为了在列表里一眼扫出还有谁没回
              { key: 'status', title: t('fb.colStatus'), width: 90, render: (row) =>
                <StatusPill tone={row.status === 'resolved' ? 'success' : 'warn'}>{row.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen')}</StatusPill> },
              // 行内动作统一为 <Link>+<Icon>（DataTable 操作列规范），不再是 variant="text" 的小 Button
              { key: 'op', title: t('org.colActions'), width: 100, render: (row) =>
                <Link onClick={() => openDetail(row)}><Icon n="eye" /> {t('fb.viewDetail')}</Link> },
            ]}  />
          </div>
        </>
      )}

      {/* 审批台用卡片流而不是表格：要在原地看原文与终稿并就地填理由/建议，单元格装不下这个上下文量 */}
      <h2 style={{ margin: '32px 0 8px' }}>{t('tickets.approvalTitle')}</h2>
      <Button variant="secondary" onClick={() => void loadApproval()}>{t('tickets.refresh')}</Button>
      {approvalTickets.map((tk) => (
        <div key={tk.id} style={{ ...cardStyle, marginTop: 12 }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
            <b>{tk.ticket_no} — {tk.title}</b>
            <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{tk.status}</span>
          </div>
          <p style={{ fontSize: 15, color: 'var(--adm-hint)', margin: '0 0 8px' }}>{tk.source_text}</p>
          {/* 终稿框 readOnly：内联审批只出「批/驳」决定，真要改稿得走下面的弹窗版（approveDlg.text） */}
          <textarea className="lc-textarea" rows={3} readOnly value={tk.final_result || ''} style={{ width: '100%', resize: 'vertical' }} />
          <div style={rowMt}>
            <Button variant="primary" onClick={() => void doApprove(tk, 'approve')}>{t('tickets.approve')}</Button>
            {/* 非受控写法：onChange 直接写回行对象字段，省掉每张卡两组 state；
                代价是清空只能靠 doApprove 手工置空。这两个下划线字段纯属本地草稿，
                提交时由 doApprove 显式取值，不会整体随对象序列化进请求体 */}
            <input className="lc-input" value={tk._reason || ''} onChange={(e) => { tk._reason = e.target.value }} placeholder={t('tickets.reasonPlaceholder')} style={{ flex: 1 }} />
            <input className="lc-input" value={tk._suggestion || ''} onChange={(e) => { tk._suggestion = e.target.value }} placeholder={t('tickets.suggestionPlaceholder')} style={{ flex: 1 }} />
            <Button variant="danger" onClick={() => void doApprove(tk, 'reject')}>{t('tickets.reject')}</Button>
          </div>
        </div>
      ))}
      {!approvalTickets.length && <div style={{ fontSize: 15, color: 'var(--adm-faint)' }}>{t('tickets.noApproval')}</div>}

      {/* 审批弹窗（dlg 形态 approveDlg）：批与驳共用一个框，靠 action 切标题/按钮与正文分区；
          danger 让整框描边变红、确认按钮自动红底，替代旧 theme="danger" 的手工配色。
          注意本文件内目前没有把行数据塞进 approveDlg 的入口（审批现走上面卡片内联），
          这套「批准同时改终稿」的流程属保留待接状态 */}
      <Dialog open={!!approveDlg} onCancel={() => setApproveDlg(null)}
        title={`${t('tickets.approve')} ${approveDlg ? String(approveDlg.row.ticket_no) : ''}`}
        danger={approveDlg?.action === 'reject'}
        confirmText={approveDlg?.action === 'reject' ? t('tickets.reject') : t('tickets.approve')}
        onConfirm={async () => {
          if (!approveDlg) return
          if (approveDlg.action === 'reject') {
            // 驳回必填理由（后端同校验，这里先拦一次）；批准无必填项，终稿留空即按原稿通过
            if (!approveDlg.reason.trim()) { toastWarn(t('tickets.reasonPlaceholder')); return }
                const r = await approveAction(Number(approveDlg.row.id), 'reject', approveDlg.reason, approveDlg.suggestion, '')
                if (toastResp(r, t('tickets.reject'))) setApproveDlg(null)
            await loadApproval()
          } else {
                const r = await approveAction(Number(approveDlg.row.id), 'approve', '', '', approveDlg.text)
                if (toastResp(r, t('tickets.approve'))) setApproveDlg(null)
            await loadApproval()
          }
        }}>
        {approveDlg && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <div style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{String(approveDlg.row.source_text)}</div>
            {/* 批准分支才给终稿编辑区：改稿与批准是一次提交完成的，避免「先改后批」中间态被别人插队 */}
            {approveDlg.action === 'approve' && (
              <Field label={t('fb.complete')}>
                <textarea className="lc-textarea" rows={3} value={approveDlg.text} onChange={(e) => setApproveDlg({ ...approveDlg, text: e.target.value })}
                  placeholder={t('tickets.finalResultPlaceholder')} style={{ width: '100%', resize: 'vertical' }} />
              </Field>
            )}
            {/* 驳回分支拆理由 + 建议两栏填：前端分两个字段提交，后端在驳回时拼成一条驳回原因
                （「理由；建议: 建议」），所以两栏各写一件事、别把同一句话抄两遍 */}
            {approveDlg.action === 'reject' && (
              <>
                <Field label={t('tickets.reasonPlaceholder')}><textarea className="lc-textarea" rows={2} value={approveDlg.reason} onChange={(e) => setApproveDlg({ ...approveDlg, reason: e.target.value })} style={{ width: '100%', resize: 'vertical' }} /></Field>
                <Field label={t('tickets.suggestionPlaceholder')}><textarea className="lc-textarea" rows={2} value={approveDlg.suggestion} onChange={(e) => setApproveDlg({ ...approveDlg, suggestion: e.target.value })} style={{ width: '100%', resize: 'vertical' }} /></Field>
              </>
            )}
            <div style={rowMt}>
              {/* 批准前先用 firstTranslation 把终稿第一个语种预填进编辑区：审批人在既有译文上改，
                  而不是从空白重打；预填后仍可整段改写（见上方 approve 分支的 textarea） */}
              <Button variant="primary" onClick={() => setApproveDlg({ ...approveDlg, action: 'approve', text: firstTranslation(approveDlg.row.final_result) })}>{t('tickets.approve')}</Button>
              <Button variant="secondary" onClick={() => setApproveDlg(null)}>{t('fb.backToList')}</Button>
            </div>
          </div>
        )}
      </Dialog>
    </>
  )
}
