// ============================================================================
// components/admin/DataSourcesP.tsx — 行业包/语言文化包/角色包自动采集面板（超管 L4）
// 职责：数据源 CRUD/启停/手动采集 + 待审增量批量审批（通过→热加载）+ 概览。
// 依赖后端：/api/admin/kb-scrape/*（见 api/scrape.ts）。
// 2026-09-18（UI 融合）：仅调整「最近状态」列配色（成功态由绿色改为中性浅色，
//   与暗色主题正文统一）；数据链路、审批与奖励折算口径均未变。
// ============================================================================
import { useEffect, useMemo, useState } from 'react'
import { runGuarded } from '@/lib/runGuarded'
import { Badge, Button, DataTable, Dialog, StatusPill, Switch, Tabs, type TableColumn } from '@/ui/langcross/src'
import { useT, t, tpl } from '@/i18n'
import { fmtPoints } from '@/utils/points' // ★ S1 审批奖励积分数展示
import { industryName, INDUSTRY_META } from '@/lib/industries'
import { industries as fetchIndustries } from '@/api/industry'
import { personas as fetchPersonas } from '@/api/persona'
import { LANG_META } from '@/lib/langNames'
import { Panel, toastResp } from './parts'
import { confirmDialog } from '@/components/uiDialogs'
import {
  scrapeSources, scrapeSourceCreate, scrapeSourceStatus, scrapeSourceRun,
  scrapeStaged, scrapeApprove, scrapeRestore, scrapeSummary,
  kbRewardConfigGet, kbRewardConfigSet,
  type ScrapeSource, type StagedMergedRow, type ScrapeSummary,
} from '@/api/scrape'
import { toastSuccess, toastError } from '@/lib/toastBus'

/** 待审表格行类型（entries/phrases 合并行） */
type StagedRow = { key: string; id: number; kind: 'entries' | 'phrases'; tier: number; pack_type: string; lang: string; src: string; tgt: string; source_url: string; status: string; industry: string }

/** 数据源类型中文名 */
/** 数据源类型中文名 */
function kindName(kind: string): string {
  return kind === 'official_api' ? t('ds.s1') : kind === 'limited_web' ? t('ds.s2') : kind === 'llm_gen' ? t('ds.llm3') : kind
}

/** tier 可信度徽标 */
function tierTag(tier: number): React.ReactNode {
  const map: Record<number, { text: string; theme: 'success' | 'warning' | 'danger' | 'default' }> = {
    1: { text: t('ds.s4'), theme: 'success' },
    2: { text: t('ds.s5'), theme: 'warning' },
    3: { text: '3·LLM', theme: 'danger' },
  }
  const m = map[tier] || { text: `t${tier}`, theme: 'default' as const }
  const tone = m.theme === 'success' ? 'success' : m.theme === 'warning' ? 'warn' : m.theme === 'danger' ? 'danger' : 'idle'
  return <StatusPill tone={tone}>{m.text}</StatusPill>
}

/** 语言代码 → 中文名(English) 展示（如 "中文(Chinese)"）；未知代码原样返回 */
function langLabelCN(code: string): string {
  const m = LANG_META[code]
  if (!m) return code
  return m.en ? `${m.zh}(${m.en})` : m.zh
}

/** 数据源面板组件（超管 L4）：数据源 CRUD/启停/手动采集 + 待审池批量审批 + 概览 */
export default function DataSourcesP() {
  const [lang] = useT() // ★ E14：t 未使用
  // ---- 数据源 ----
  const [sources, setSources] = useState<ScrapeSource[]>([])
  const [summary, setSummary] = useState<ScrapeSummary | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [running, setRunning] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState<Partial<ScrapeSource>>({
    kind: 'official_api', name: '', base_url: '', lang: 'en', industry: '', pack_type: 'locale', tier: 1, freq_hours: 24,
  })

  // ★ 2026-09-10 行业字典动态化：待审筛选/数据源行业下拉动态拉取（超管在「行业管理」维护）
  const [indList, setIndList] = useState<Array<{ code: string; name: string }>>([])
  // ★ 2026-09-19 角色采集源：persona 包同样走本面板采集，role code 展示需角色字典
  const [roleList, setRoleList] = useState<Array<{ code: string; name: string }>>([])
  useEffect(() => {
    (async () => {
      try {
        const r = await runGuarded(() => fetchIndustries())
        if (!r) return //  E10：网络/超时异常已提示，中断后续
        if (r.success && r.industries) setIndList(r.industries)
      } catch { /* ignore */ }
    })()
    ;(async () => {
      try {
        const r = await runGuarded(() => fetchPersonas())
        if (!r) return
        if (r.success && r.personas) setRoleList(r.personas)
      } catch { /* ignore */ }
    })()
  }, [])
  // 采集源「行业/角色」列显示名：persona 源按角色字典翻译 role code，其余走行业字典
  const scopeName = (row: { pack_type?: string; industry?: string }) => {
    if (!row.industry) return '—'
    if (row.pack_type === 'persona') return roleList.find((x) => x.code === row.industry)?.name || row.industry
    return industryName(row.industry, lang)
  }

  // ---- 待审池（服务端分页） ----
  const [tab, setTab] = useState<'sources' | 'staged'>('sources')
  const [rows, setRows] = useState<StagedMergedRow[]>([])
  const [stagedTotal, setStagedTotal] = useState(0)
  const [stagedPage, setStagedPage] = useState(1)
  const STAGED_PAGE_SIZE = 20 // 待审数据分页单页条数（服务端分页，配套跳页器）
  const [stagedFilter, setStagedFilter] = useState<{ pack_type: string; status: string; lang: string; industry: string }>({ pack_type: '', status: 'pending', lang: '', industry: '' })
  // 选中行（合并行复合键 "entries:<id>" / "phrases:<id>"，避免两表自增 ID 撞车误伤）
  const [selectedKeys, setSelectedKeys] = useState<string[]>([])
  const [approving, setApproving] = useState(false)
  // 还原前编辑弹窗
  const [editRow, setEditRow] = useState<StagedRow | null>(null)
  const [editSrc, setEditSrc] = useState('')
  const [editTgt, setEditTgt] = useState('')
  const [, setSavingEdit] = useState(false)

  // ---- KB 上传奖励（功能⑥） ----
  const [rewardCfg, setRewardCfg] = useState<{ enabled: boolean; per_char: number; daily_cap: number } | null>(null)
  const [savingReward, setSavingReward] = useState(false)

  /** 读取奖励配置 */
  const loadReward = async () => {
    const r = await runGuarded(() => kbRewardConfigGet())
    if (!r) return //  E10：网络/超时异常已提示，中断后续
    if (r.success && r.enabled !== undefined) {
      setRewardCfg({ enabled: !!r.enabled, per_char: r.per_char ?? 100, daily_cap: r.daily_cap ?? 50000 })
    }
  }

  /** 保存奖励配置 */
  const onSaveReward = async () => {
    if (!rewardCfg) return
    setSavingReward(true)
    const r = await kbRewardConfigSet({
      enabled: rewardCfg.enabled,
      per_char: rewardCfg.per_char,
      daily_cap: rewardCfg.daily_cap,
    })
    setSavingReward(false)
    if (!toastResp(r, t('ds.s6'))) return
    await loadReward()
  }

  /** 刷新数据源 + 概览 */
  /** 刷新数据源 + 概览 */
  const reload = async () => {
    const res = await runGuarded(() => Promise.all([scrapeSources(), scrapeSummary()])) // ★ E10
    if (!res) return
    const [s, sm] = res
    if (s.success) setSources(s.sources || [])
    if (sm.success) setSummary(sm.summary || null)
  }

  /** 刷新待审池（服务端分页：limit/offset 拉当前页 + 真实总数 + 行业筛选） */
  const reloadStaged = async (page = stagedPage) => {
    const r = await scrapeStaged({
      pack_type: stagedFilter.pack_type,
      status: stagedFilter.status,
      lang: stagedFilter.lang,
      industry: stagedFilter.industry,
      limit: STAGED_PAGE_SIZE,
      offset: (page - 1) * STAGED_PAGE_SIZE,
    })
    if (r.success) {
      setRows(r.rows || [])
      setStagedTotal(r.total || 0)
    }
  }

  useEffect(() => { reload() }, [])
  useEffect(() => {
    if (tab === 'staged') { setStagedPage(1); reloadStaged(1) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, stagedFilter])
  useEffect(() => { loadReward() }, [])

  /** 新增数据源 */
  const onCreate = async () => {
    if (!form.name?.trim()) { toastError(t('ds.s7')); return }
    setSaving(true)
    const r = await runGuarded(() => scrapeSourceCreate(form))
    if (!r) return //  E10：网络/超时异常已提示，中断后续
    setSaving(false)
    if (!toastResp(r, t('ds.s8'))) return
    setShowForm(false)
    setForm({ kind: 'official_api', name: '', base_url: '', lang: 'en', industry: '', pack_type: 'locale', tier: 1, freq_hours: 24 })
    reload()
  }

  /** 手动采集一轮 */
  const onRun = async () => {
    setRunning(true)
    const r = await runGuarded(() => scrapeSourceRun())
    if (!r) return //  E10：网络/超时异常已提示，中断后续
    setRunning(false)
    toastResp(r, t('ds.s9'))
    reload()
  }

  /** 批量审批 */
  const onApprove = async (action: 'approve' | 'reject') => {
    if (!selectedKeys.length) { toastError(t('ds.s10')); return }
    // 按 kind 拆分选中行（entries/phrases 独立提交，避免 ID 撞车混传）
    const idsOf = (kind: 'entries' | 'phrases') =>
      selectedKeys.filter((k) => k.startsWith(kind + ':')).map((k) => parseInt(k.split(':')[1], 10))
    setApproving(true)
    const label = action === 'approve' ? t('ds.s11') : t('ds.s12')
    let ok = true
    let rewardNote = ''
    const eIds = idsOf('entries')
    const pIds = idsOf('phrases')
    if (eIds.length) {
      const r = await runGuarded(() => scrapeApprove('entries', eIds, action))
      if (!r) return //  E10：网络/超时异常已提示，中断后续
      // 只有 approve 才发奖励：r.rewards 是后端逐贡献者的发放明细（reward_points 积分口径），
      // 前端求和后直接回显。
      if (action === 'approve' && r.rewards?.length) rewardNote = tpl('ds.s13', { a1: fmtPoints(r.rewards.reduce((x: number, y: any) => x + (Number(y.reward_points) || 0), 0)) })
      if (!toastResp(r, ok ? tpl('ds.s14', { a1: label, a2: r.applied ?? 0, a3: rewardNote }) : undefined)) ok = false
    }
    if (pIds.length) {
      const r = await runGuarded(() => scrapeApprove('phrases', pIds, action))
      if (!r) return //  E10：网络/超时异常已提示，中断后续
      if (!toastResp(r, ok ? tpl('ds.s15', { a1: label, a2: r.applied ?? 0 }) : undefined)) ok = false
    }
    setApproving(false)
    setSelectedKeys([])
    reloadStaged()
    reload()
  }

  /** 打开还原编辑弹窗（仅单行触发；可改译文后还原为待审） */
  const openEditRestore = (row: StagedRow) => {
    setEditRow(row)
    setEditSrc(row.src.replace(/^\[[^\]]+\]\s*/, ''))
    setEditTgt(row.tgt)
    setSavingEdit(false)
  }

  /** 批量还原为待审（不改内容） */
  const onBatchRestore = async () => {
    if (!selectedKeys.length) { toastError(t('ds.s16')); return }
    const okGo = await confirmDialog({ header: t('ds.s18'), body: tpl('ds.s17', { a1: selectedKeys.length }), confirmText: t('ds.s19') })
    if (!okGo) return
    const idsOf = (kind: 'entries' | 'phrases') =>
      selectedKeys.filter((k) => k.startsWith(kind + ':')).map((k) => parseInt(k.split(':')[1], 10))
    setApproving(true)
    const eIds = idsOf('entries')
    const pIds = idsOf('phrases')
    let reverted = 0
    if (eIds.length) {
      const r = await runGuarded(() => scrapeRestore('entries', eIds))
      if (!r) return //  E10：网络/超时异常已提示，中断后续
      if (toastResp(r, undefined)) reverted += r.reverted ?? 0
    }
    if (pIds.length) {
      const r = await runGuarded(() => scrapeRestore('phrases', pIds))
      if (!r) return //  E10：网络/超时异常已提示，中断后续
      if (toastResp(r, undefined)) reverted += r.reverted ?? 0
    }
    setApproving(false)
    setSelectedKeys([])
    if (reverted > 0) toastSuccess(tpl('ds.s20', { a1: reverted }))
    reloadStaged()
    reload()
  }

  /** 还原并携带编辑内容 */
  const confirmEditRestore = async () => {
    if (!editRow) return
    setSavingEdit(true)
    let r: { success?: boolean; message?: string; reverted?: number } | undefined
    if (editRow.kind === 'entries') {
      r = await runGuarded(() => scrapeRestore('entries', [editRow.id], { [editRow.id]: { src_text: editSrc, tgt_text: editTgt } })) // ★ E10
    } else {
      r = await runGuarded(() => scrapeRestore('phrases', [editRow.id], { [editRow.id]: { phrase: editSrc, replacement: editTgt } }))
    }
    setSavingEdit(false)
    if (!r) return
    if (!toastResp(r, t('ds.s21'))) return
    setEditRow(null)
    setSelectedKeys([])
    reloadStaged()
    reload()
  }

  /** 拼接待审合并行（条目+安全句统一展示；复合键防两表 ID 撞车；服务端已分页） */
  const mergedRows = useMemo((): StagedRow[] => rows.map((e) => ({
    key: e.key, id: e.id, kind: e.kind, tier: e.tier, pack_type: e.pack_type, industry: e.industry || '',
    lang: e.kind === 'phrases'
      ? langLabelCN(e.src_lang)
      : `${langLabelCN(e.src_lang)} → ${langLabelCN(e.tgt_lang)}`,
    src: e.kind === 'phrases' && e.phrase_kind ? `[${e.phrase_kind}] ${e.src_text}` : e.src_text,
    tgt: e.tgt_text || '', source_url: e.source_url, status: e.status,
  })), [rows])

  /** 表格列定义 */
  const srcCols: TableColumn<ScrapeSource>[] = [
    { key: 'name', title: t('ds.s22') },
    { key: 'kind', title: t('ds.s23'), render: (row) => kindName(row.kind) },
    { key: 'pack_type', title: t('ds.s24'), render: (row) => row.pack_type === 'industry' ? t('ds.s25') : row.pack_type === 'persona' ? t('ds.sPersona') : t('ds.s26') },
    { key: 'lang', title: t('ds.s27'), render: (row) => row.lang ? langLabelCN(row.lang) : t('ds.s28') },
    { key: 'industry', title: t('ds.s29'), render: (row) => scopeName(row) },
    { key: 'tier', title: t('ds.s30'), render: (row) => tierTag(row.tier) },
    { key: 'base_url', title: 'URL', render: (row) => row.base_url ? <span style={{ wordBreak: 'break-all' }}>{row.base_url}</span> : '—' },
    // 最近采集状态：2026-09-18 起成功态不再用绿色强调（改中性字色 #E7E9EA），
    // 只保留失败态红色告警——正常态无需抢眼，异常才需要被一眼看到。
    { key:'last_status', title: t('ds.s31'), render: (row) => <span style={{ color: row.last_status === 'ok' ? 'var(--lc-text-1)' : 'var(--lc-danger)' }}>{row.last_status || t('ds.s32')}</span> },
    { key: 'op', title: t('ds.s33'), render: (row) => (
      <Switch checked={row.enabled === 1} onChange={(e) => { scrapeSourceStatus(row.id, e.target.checked ? 1 : 0).then((r) => { toastResp(r); reload() }) }} />
    ) },
  ]

  /** 待审池表格列定义（首列为多选列，供批量操作勾选） */
  const stagedCols: TableColumn<StagedRow>[] = [
    { key: 'row-select', title: '', width: 44, render: (row) => (
      <input type="checkbox" aria-label={String(row.key)} checked={selectedKeys.includes(String(row.key))}
        onChange={() => setSelectedKeys((ks) => toggleRowKeyIn(ks, String(row.key)))} style={{ accentColor: 'var(--lc-text-1)' }} />
    ) },
    { key: 'id', title: 'ID', width: 70 },
    { key: 'pack_type', title: t('ds.s34'), width: 90, render: (row) => row.pack_type === 'industry' ? <Badge>{t('ds.s35')}</Badge> : row.pack_type === 'persona' ? <Badge>{t('ds.sPersona')}</Badge> : <StatusPill tone="success">{t('ds.s36')}</StatusPill> },
    { key: 'industry', title: t('ds.s37'), width: 110, render: (row) => row.industry ? <Badge>{scopeName(row)}</Badge> : '—' },
    { key: 'tier', title: t('ds.s38'), width: 90, render: (row) => tierTag(row.tier) },
    { key: 'lang', title: t('ds.s39'), width: 90 },
    { key: 'src', title: t('ds.s40') },
    { key: 'tgt', title: t('ds.s41') },
    { key: 'source_url', title: t('ds.s42') },
    { key: 'status', title: t('ds.s43'), width: 84, render: (row) => (
      <StatusPill tone={row.status === 'approved' ? 'success' : row.status === 'rejected' ? 'danger' : 'warn'}>
        {row.status === 'approved' ? t('ds.s44') : row.status === 'rejected' ? t('ds.s45') : t('ds.s46')}
      </StatusPill>
    ) },
    { key: 'op', title: t('ds.s47'), width: 96, render: (row) => (
      <Button size="sm" variant="secondary" onClick={() => openEditRestore(row)}>{t('ds.s48')}</Button>
    ) },
  ]

  return (
    <div>
      {/* 概览 */}
      <Panel title={t("ds.s49")} extra={
        <div style={{ display: 'flex', gap: 8 }}>
          <Badge>{tpl('ds.pendingEntriesFmt', { n: summary?.pending_entries ?? 0 })}</Badge>
          <Badge>{tpl('ds.pendingPhrasesFmt', { n: summary?.pending_phrases ?? 0 })}</Badge>
          <Badge>{tpl('ds.sourcesFmt', { en: summary?.sources_enabled ?? 0, tot: summary?.sources_total ?? 0 })}</Badge>
          <Badge>{tpl('ds.lastDailyFmt', { d: summary?.last_daily || t('ds.s50') })}</Badge>
          <Button size="sm" variant="secondary" disabled={running} onClick={onRun}>{t('ds.s51')}</Button>
        </div>
      }>
        {/* 功能⑥ KB 上传奖励开关（超管） */}
        <div style={{ border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: 12, marginBottom: 12, display: 'flex', gap: 24, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 600, fontSize: 15 }}>{t('ds.kb52')}</span>
          {rewardCfg && (
            <>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 15 }}>
                <Switch checked={rewardCfg.enabled} onChange={(e) => setRewardCfg((c) => (c ? { ...c, enabled: e.target.checked } : c))} />
                {t('ds.rewardEnabled')}
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 15 }}>
                {t('ds.perCharLabel')}
                <input className="lc-input" type="number" style={{ width: 110 }} value={String(rewardCfg.per_char)} onChange={(e) => setRewardCfg((c) => (c ? { ...c, per_char: Number(e.target.value) || 0 } : c))} />
                token
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 15 }}>
                {t('ds.dailyCap')}
                <input className="lc-input" type="number" style={{ width: 130 }} value={String(rewardCfg.daily_cap)} onChange={(e) => setRewardCfg((c) => (c ? { ...c, daily_cap: Number(e.target.value) || 0 } : c))} />
                token
              </label>
              <Button size="sm" variant="primary" disabled={savingReward} onClick={onSaveReward}>{t('ds.s53')}</Button>
            </>
          )}
        </div>
        <Tabs activeKey={tab} onChange={(k) => setTab(k as 'sources' | 'staged')} items={[
          { key: 'sources', label: tpl('ds.s54', { a1: sources.length }) },
          { key: 'staged', label: tpl('ds.s75', { a1: stagedTotal }) },
        ]} />
          {/* Tab 面板 */}
          {tab === 'sources' && (<>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
              <Button size="sm" variant="primary" onClick={() => setShowForm((v) => !v)}>{showForm ? t('ds.s55') : t('ds.s56')}</Button>
            </div>
            {showForm && (
              <div style={{ border: '1.2px solid var(--adm-line)', borderRadius: 8, padding: 16, marginBottom: 12, display: 'grid', gap: 10, gridTemplateColumns: 'repeat(auto-fill, minmax(200px,1fr))' }}>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s58')}</div><input className="lc-input" value={form.name} onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))} placeholder={t("ds.s57")} /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s59')}</div>
                  <select className="lc-select" value={form.kind} onChange={(e) => setForm((f) => ({ ...f, kind: e.target.value }))}>
                    <option value="official_api">{t('ds.s60')}</option>
                    <option value="limited_web">{t('ds.s61')}</option>
                    <option value="llm_gen">{t('ds.llm62')}</option>
                  </select></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s63')}</div>
                  <select className="lc-select" value={form.pack_type} onChange={(e) => setForm((f) => ({ ...f, pack_type: e.target.value }))}>
                    <option value="locale">{t('ds.s64')}</option>
                    <option value="industry">{t('ds.s65')}</option>
                    <option value="persona">{t('ds.sPersona')}</option>
                  </select></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s66')}</div><input className="lc-input" value={form.lang} onChange={(e) => setForm((f) => ({ ...f, lang: e.target.value }))} placeholder="en" /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s67')}</div><input className="lc-input" value={form.industry} onChange={(e) => setForm((f) => ({ ...f, industry: e.target.value }))} placeholder="auto / general" /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s68')}</div>
                  <select className="lc-select" value={String(form.tier ?? 1)} onChange={(e) => setForm((f) => ({ ...f, tier: Number(e.target.value) }))}>
                    <option value="1">{t('ds.s69')}</option>
                    <option value="2">{t('ds.s70')}</option>
                    <option value="3">3·LLM</option>
                  </select></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s71')}</div><input className="lc-input" type="number" value={String(form.freq_hours ?? 24)} onChange={(e) => setForm((f) => ({ ...f, freq_hours: parseInt(e.target.value) || 24 }))} /></div>
                <div style={{ gridColumn: '1 / -1' }}><div style={{ marginBottom: 4 }}>{t('ds.s72')}</div><input className="lc-input" value={form.base_url} onChange={(e) => setForm((f) => ({ ...f, base_url: e.target.value }))} placeholder="https://…" /></div>
                <div style={{ gridColumn: '1 / -1', display: 'flex', gap: 8 }}>
                  <Button size="sm" variant="primary" disabled={saving} onClick={onCreate}>{t('ds.s73')}</Button>
                  <Button size="sm" variant="secondary" onClick={() => setShowForm(false)}>{t('ds.s74')}</Button>
                </div>
              </div>
            )}
            {/* 数据表格 */}
            <DataTable<any> rowKey={(row) => String(row.id)} rows={sources} columns={srcCols} />
          </>)}
          {/* Tab 面板 */}
          {tab === 'staged' && (<>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8, flexWrap: 'wrap' }}>
              <div style={{ display: 'flex', gap: 0 }}>
                {([['', t('ds.s76')], ['industry', t('ds.s77')], ['persona', t('ds.sPersonaFilter')], ['locale', t('ds.s78')]] as const).map(([v, l]) => (
                  <Button key={v} size="sm" variant={stagedFilter.pack_type === v ? 'primary' : 'secondary'}
                    onClick={() => setStagedFilter((f) => ({ ...f, pack_type: v }))}>{l}</Button>
                ))}
              </div>
              <div style={{ display: 'flex', gap: 0 }}>
                {([['pending', t('ds.s79')], ['approved', t('ds.s80')], ['rejected', t('ds.s81')]] as const).map(([v, l]) => (
                  <Button key={v} size="sm" variant={stagedFilter.status === v ? 'primary' : 'secondary'}
                    onClick={() => setStagedFilter((f) => ({ ...f, status: v }))}>{l}</Button>
                ))}
              </div>
              <div style={{ width: 140 }}><input className="lc-input" placeholder={t("ds.s82")} value={stagedFilter.lang} onChange={(e) => setStagedFilter((f) => ({ ...f, lang: e.target.value }))} /></div>
              <div style={{ width: 160 }}>
                <select className="lc-select" value={stagedFilter.industry}
                  onChange={(e) => setStagedFilter((f) => ({ ...f, industry: e.target.value }))}>
                  <option value="">{t('ds.s83')}</option>
                  {(indList.length > 0
                    ? indList
                    : Object.values(INDUSTRY_META).map((m) => ({ code: m.code, name: industryName(m.code, lang) }))
                  ).map((m) => <option key={m.code} value={m.code}>{m.name}</option>)}
                </select>
              </div>
              <div style={{ flex: 1 }} />
              {stagedFilter.status === 'pending' ? (
                <>
                  <Button size="sm" variant="primary" disabled={approving} onClick={() => onApprove('approve')}>{t('ds.s84')}</Button>
                  <Button size="sm" variant="danger" disabled={approving} onClick={() => onApprove('reject')}>{t('ds.s85')}</Button>
                </>
              ) : (
                <Button size="sm" variant="secondary" disabled={approving} onClick={onBatchRestore}>{t('ds.s86')}</Button>
              )}
            </div>
            {/* 数据表格 */}
            <DataTable<StagedRow> rowKey={(row) => String(row.key)} rows={mergedRows} columns={stagedCols} />
            <StagedPager page={stagedPage} pageSize={STAGED_PAGE_SIZE} total={stagedTotal}
              onGo={(p) => { if (p === stagedPage) return; setSelectedKeys([]); setStagedPage(p); reloadStaged(p) }} />
      </>)}
      </Panel>
      {/* 还原前编辑内容弹窗（修改后还原为待审） */}
      <Dialog open={!!editRow} onCancel={() => setEditRow(null)} title={t('ds.s87')}
        confirmText={t('ds.s89')} cancelText={t('ds.s88')} onConfirm={confirmEditRestore}>
        {editRow && (
          <div style={{ display: 'grid', gap: 12 }}>
            <div>
              <div style={{ marginBottom: 4, fontSize: 15, color: 'var(--adm-hint)' }}>
                {editRow.kind === 'entries' ? t('ds.s90') : t('ds.s91')}
              </div>
              <input className="lc-input" value={editSrc} onChange={(e) => setEditSrc(e.target.value)} />
            </div>
            <div>
              <div style={{ marginBottom: 4, fontSize: 15, color: 'var(--adm-hint)' }}>
                {editRow.kind === 'entries' ? t('ds.s92') : t('ds.s93')}
              </div>
              <input className="lc-input" value={editTgt} onChange={(e) => setEditTgt(e.target.value)} />
            </div>
            <div style={{ fontSize: 15, color: 'var(--adm-faint)' }}>{t('ds.s94')}</div>
          </div>
        )}
      </Dialog>
    </div>
  )
}

/** 行勾选辅助：勾中追加、再勾移除 */
function toggleRowKeyIn(keys: string[], k: string): string[] {
  return keys.includes(k) ? keys.filter((x) => x !== k) : [...keys, k]
}

/** 待审池分页器：上一页 / 页码 / 下一页（服务端分页配套） */
function StagedPager({ page, pageSize, total, onGo }: {
  page: number; pageSize: number; total: number; onGo: (p: number) => void
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (pages <= 1) return null
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'flex-end', marginTop: 10 }}>
      <Button size="sm" variant="secondary" disabled={page <= 1} onClick={() => onGo(page - 1)}>{'‹'}</Button>
      <span style={{ fontSize: 15, color: 'var(--lc-text-3)' }}>{page} / {pages}</span>
      <Button size="sm" variant="secondary" disabled={page >= pages} onClick={() => onGo(page + 1)}>{'›'}</Button>
    </div>
  )
}
