// ============================================================================
// components/admin/DataSourcesP.tsx — 行业包/语言文化包自动采集面板（超管 L4）
// 职责：数据源 CRUD/启停/手动采集 + 待审增量批量审批（通过→热加载）+ 概览。
// 依赖后端：/api/admin/kb-scrape/*（见 api/scrape.ts）。
// ============================================================================
import { useEffect, useMemo, useState } from 'react'
import { runGuarded } from '@/lib/runGuarded'
import { Button, Dialog, Input, MessagePlugin, Select, Switch, Table, Tag, Tabs, RadioGroup, Radio } from 'tdesign-react'
import { useT, t, tpl } from '@/i18n'
import { fmtPoints } from '@/utils/points' // ★ S1 审批奖励积分数展示
import { industryName, INDUSTRY_META } from '@/lib/industries'
import { industries as fetchIndustries } from '@/api/industry'
import { LANG_META } from '@/lib/langNames'
import { Panel, toastResp } from './parts'
import { confirmDialog } from '@/components/uiDialogs'
import {
  scrapeSources, scrapeSourceCreate, scrapeSourceStatus, scrapeSourceRun,
  scrapeStaged, scrapeApprove, scrapeRestore, scrapeSummary,
  kbRewardConfigGet, kbRewardConfigSet,
  type ScrapeSource, type StagedMergedRow, type ScrapeSummary,
} from '@/api/scrape'

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
  return <Tag theme={m.theme} variant="light">{m.text}</Tag>
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
  useEffect(() => {
    (async () => {
      try {
        const r = await runGuarded(() => fetchIndustries())
        if (!r) return // ★ E10：网络/超时异常已提示，中断后续
        if (r.success && r.industries) setIndList(r.industries)
      } catch { /* ignore */ }
    })()
  }, [])

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
  const [savingEdit, setSavingEdit] = useState(false)

  // ---- KB 上传奖励（功能⑥） ----
  const [rewardCfg, setRewardCfg] = useState<{ enabled: boolean; per_char: number; daily_cap: number } | null>(null)
  const [savingReward, setSavingReward] = useState(false)

  /** 读取奖励配置 */
  const loadReward = async () => {
    const r = await runGuarded(() => kbRewardConfigGet())
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
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
    if (!form.name?.trim()) { void MessagePlugin.error(t('ds.s7')); return }
    setSaving(true)
    const r = await runGuarded(() => scrapeSourceCreate(form))
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
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
    if (!r) return // ★ E10：网络/超时异常已提示，中断后续
    setRunning(false)
    toastResp(r, t('ds.s9'))
    reload()
  }

  /** 批量审批 */
  const onApprove = async (action: 'approve' | 'reject') => {
    if (!selectedKeys.length) { void MessagePlugin.error(t('ds.s10')); return }
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
      if (!r) return // ★ E10：网络/超时异常已提示，中断后续
      if (action === 'approve' && r.rewards?.length) rewardNote = tpl('ds.s13', { a1: fmtPoints(r.rewards.reduce((x: number, y: any) => x + (Number(y.tokens) || 0), 0)) })
      if (!toastResp(r, ok ? tpl('ds.s14', { a1: label, a2: r.applied ?? 0, a3: rewardNote }) : undefined)) ok = false
    }
    if (pIds.length) {
      const r = await runGuarded(() => scrapeApprove('phrases', pIds, action))
      if (!r) return // ★ E10：网络/超时异常已提示，中断后续
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
    if (!selectedKeys.length) { void MessagePlugin.error(t('ds.s16')); return }
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
      if (!r) return // ★ E10：网络/超时异常已提示，中断后续
      if (toastResp(r, undefined)) reverted += r.reverted ?? 0
    }
    if (pIds.length) {
      const r = await runGuarded(() => scrapeRestore('phrases', pIds))
      if (!r) return // ★ E10：网络/超时异常已提示，中断后续
      if (toastResp(r, undefined)) reverted += r.reverted ?? 0
    }
    setApproving(false)
    setSelectedKeys([])
    if (reverted > 0) void MessagePlugin.success(tpl('ds.s20', { a1: reverted }))
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
  const srcCols = [
    { colKey: 'name', title: t('ds.s22') },
    { colKey: 'kind', title: t('ds.s23'), cell: ({ row }: any) => kindName(row.kind) },
    { colKey: 'pack_type', title: t('ds.s24'), cell: ({ row }: any) => row.pack_type === 'industry' ? t('ds.s25') : t('ds.s26') },
    { colKey: 'lang', title: t('ds.s27'), cell: ({ row }: any) => row.lang ? langLabelCN(row.lang) : t('ds.s28') },
    { colKey: 'industry', title: t('ds.s29'), cell: ({ row }: any) => row.industry ? industryName(row.industry, lang) : '—' },
    { colKey: 'tier', title: t('ds.s30'), cell: ({ row }: any) => tierTag(row.tier) },
    { colKey: 'base_url', title: 'URL', cell: ({ row }: any) => row.base_url ? <span style={{ wordBreak: 'break-all' }}>{row.base_url}</span> : '—' },
    { colKey: 'last_status', title: t('ds.s31'), cell: ({ row }: any) => <span style={{ color: row.last_status === 'ok' ? '#2f9e44' : '#d03050' }}>{row.last_status || t('ds.s32')}</span> },
    { colKey: 'op', title: t('ds.s33'), cell: ({ row }: any) => (
      <Switch size="small" value={row.enabled === 1} onChange={(v: boolean) => { scrapeSourceStatus(row.id, v ? 1 : 0).then((r) => { toastResp(r); reload() }) }} />
    ) },
  ]

  /** 待审池表格列定义（首列为多选列，供批量操作勾选） */
  const stagedCols = [
    { colKey: 'row-select', type: 'multiple' as const, width: 44 },
    { colKey: 'id', title: 'ID', width: 70 },
    { colKey: 'pack_type', title: t('ds.s34'), width: 90, cell: ({ row }: any) => row.pack_type === 'industry' ? <Tag theme="primary" variant="light">{t('ds.s35')}</Tag> : <Tag theme="success" variant="light">{t('ds.s36')}</Tag> },
    { colKey: 'industry', title: t('ds.s37'), width: 110, cell: ({ row }: any) => row.industry ? <Tag variant="light">{industryName(row.industry, lang)}</Tag> : '—' },
    { colKey: 'tier', title: t('ds.s38'), width: 90, cell: ({ row }: any) => tierTag(row.tier) },
    { colKey: 'lang', title: t('ds.s39'), width: 90 },
    { colKey: 'src', title: t('ds.s40') },
    { colKey: 'tgt', title: t('ds.s41') },
    { colKey: 'source_url', title: t('ds.s42') },
    { colKey: 'status', title: t('ds.s43'), width: 84, cell: ({ row }: any) => (
      <Tag theme={row.status === 'approved' ? 'success' : row.status === 'rejected' ? 'danger' : 'warning'} variant="light">
        {row.status === 'approved' ? t('ds.s44') : row.status === 'rejected' ? t('ds.s45') : t('ds.s46')}
      </Tag>
    ) },
    { colKey: 'op', title: t('ds.s47'), width: 96, cell: ({ row }: any) => (
      <Button size="small" variant="outline" onClick={() => openEditRestore(row)}>{t('ds.s48')}</Button>
    ) },
  ]

  return (
    <div>
      {/* 概览 */}
      <Panel title={t("ds.s49")} extra={
        <div style={{ display: 'flex', gap: 8 }}>
          <Tag theme="primary" variant="light">{tpl('ds.pendingEntriesFmt', { n: summary?.pending_entries ?? 0 })}</Tag>
          <Tag theme="success" variant="light">{tpl('ds.pendingPhrasesFmt', { n: summary?.pending_phrases ?? 0 })}</Tag>
          <Tag theme="warning" variant="light">{tpl('ds.sourcesFmt', { en: summary?.sources_enabled ?? 0, tot: summary?.sources_total ?? 0 })}</Tag>
          <Tag variant="light">{tpl('ds.lastDailyFmt', { d: summary?.last_daily || t('ds.s50') })}</Tag>
          <Button size="small" loading={running} onClick={onRun}>{t('ds.s51')}</Button>
        </div>
      }>
        {/* 功能⑥ KB 上传奖励开关（超管） */}
        <div style={{ border: '1px solid var(--adm-line)', borderRadius: 8, padding: 12, marginBottom: 12, display: 'flex', gap: 24, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 600, fontSize: 13 }}>{t('ds.kb52')}</span>
          {rewardCfg && (
            <>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                <Switch size="small" value={rewardCfg.enabled} onChange={(v: boolean) => setRewardCfg((c) => (c ? { ...c, enabled: v } : c))} />
                {t('ds.rewardEnabled')}
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                {t('ds.perCharLabel')}
                <Input type="number" style={{ width: 110 }} value={String(rewardCfg.per_char)} onChange={(v: string) => setRewardCfg((c) => (c ? { ...c, per_char: Number(v) || 0 } : c))} />
                token
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                {t('ds.dailyCap')}
                <Input type="number" style={{ width: 130 }} value={String(rewardCfg.daily_cap)} onChange={(v: string) => setRewardCfg((c) => (c ? { ...c, daily_cap: Number(v) || 0 } : c))} />
                token
              </label>
              <Button size="small" theme="primary" loading={savingReward} onClick={onSaveReward}>{t('ds.s53')}</Button>
            </>
          )}
        </div>
        <Tabs value={tab} onChange={(v) => setTab(v as 'sources' | 'staged')}>
          {/* Tab 面板 */}
          <Tabs.TabPanel value="sources" label={tpl('ds.s54', { a1: sources.length })}>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
              <Button size="small" theme="primary" onClick={() => setShowForm((v) => !v)}>{showForm ? t('ds.s55') : t('ds.s56')}</Button>
            </div>
            {showForm && (
              <div style={{ border: '1px solid var(--adm-line)', borderRadius: 8, padding: 16, marginBottom: 12, display: 'grid', gap: 10, gridTemplateColumns: 'repeat(auto-fill, minmax(200px,1fr))' }}>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s58')}</div><Input value={form.name} onChange={(v: string) => setForm((f) => ({ ...f, name: v }))} placeholder={t("ds.s57")} /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s59')}</div>
                  <Select value={form.kind} onChange={(v: any) => setForm((f) => ({ ...f, kind: String(v) }))} options={[{ value: 'official_api', label: t('ds.s60') }, { value: 'limited_web', label: t('ds.s61') }, { value: 'llm_gen', label: t('ds.llm62') }]} /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s63')}</div>
                  <Select value={form.pack_type} onChange={(v: any) => setForm((f) => ({ ...f, pack_type: String(v) }))} options={[{ value: 'locale', label: t('ds.s64') }, { value: 'industry', label: t('ds.s65') }]} /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s66')}</div><Input value={form.lang} onChange={(v: string) => setForm((f) => ({ ...f, lang: v }))} placeholder="en" /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s67')}</div><Input value={form.industry} onChange={(v: string) => setForm((f) => ({ ...f, industry: v }))} placeholder="auto / general" /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s68')}</div>
                  <Select value={form.tier} onChange={(v: any) => setForm((f) => ({ ...f, tier: Number(v) }))} options={[{ value: 1, label: t('ds.s69') }, { value: 2, label: t('ds.s70') }, { value: 3, label: '3·LLM' }]} /></div>
                <div><div style={{ marginBottom: 4 }}>{t('ds.s71')}</div><Input type="number" value={String(form.freq_hours ?? 24)} onChange={(v: string) => setForm((f) => ({ ...f, freq_hours: parseInt(v) || 24 }))} /></div>
                <div style={{ gridColumn: '1 / -1' }}><div style={{ marginBottom: 4 }}>{t('ds.s72')}</div><Input value={form.base_url} onChange={(v: string) => setForm((f) => ({ ...f, base_url: v }))} placeholder="https://…" /></div>
                <div style={{ gridColumn: '1 / -1', display: 'flex', gap: 8 }}>
                  <Button size="small" theme="primary" loading={saving} onClick={onCreate}>{t('ds.s73')}</Button>
                  <Button size="small" variant="outline" onClick={() => setShowForm(false)}>{t('ds.s74')}</Button>
                </div>
              </div>
            )}
            {/* 数据表格 */}
            <Table rowKey="id" data={sources} columns={srcCols} size="small" bordered />
          </Tabs.TabPanel>
          {/* Tab 面板 */}
          <Tabs.TabPanel value="staged" label={tpl('ds.s75', { a1: stagedTotal })}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8, flexWrap: 'wrap' }}>
              <RadioGroup value={stagedFilter.pack_type} onChange={(v: string) => setStagedFilter((f) => ({ ...f, pack_type: v }))}>
                <Radio value="">{t('ds.s76')}</Radio>
                <Radio value="industry">{t('ds.s77')}</Radio>
                <Radio value="locale">{t('ds.s78')}</Radio>
              </RadioGroup>
              <RadioGroup value={stagedFilter.status} onChange={(v: string) => setStagedFilter((f) => ({ ...f, status: v }))}>
                <Radio value="pending">{t('ds.s79')}</Radio>
                <Radio value="approved">{t('ds.s80')}</Radio>
                <Radio value="rejected">{t('ds.s81')}</Radio>
              </RadioGroup>
              <div style={{ width: 140 }}><Input placeholder={t("ds.s82")} value={stagedFilter.lang} onChange={(v: string) => setStagedFilter((f) => ({ ...f, lang: v }))} /></div>
              <div style={{ width: 160 }}>
                <Select
                  value={stagedFilter.industry}
                  onChange={(v: any) => setStagedFilter((f) => ({ ...f, industry: String(v ?? '') }))}
                  clearable
                  placeholder={t("ds.s83")}
                  options={indList.length > 0
                    ? indList.map((m) => ({ label: m.name, value: m.code }))
                    : Object.values(INDUSTRY_META).map((m) => ({ label: industryName(m.code, lang), value: m.code }))}
                />
              </div>
              <div style={{ flex: 1 }} />
              {stagedFilter.status === 'pending' ? (
                <>
                  <Button size="small" theme="primary" loading={approving} onClick={() => onApprove('approve')}>{t('ds.s84')}</Button>
                  <Button size="small" theme="danger" variant="outline" loading={approving} onClick={() => onApprove('reject')}>{t('ds.s85')}</Button>
                </>
              ) : (
                <Button size="small" theme="primary" variant="outline" loading={approving} onClick={onBatchRestore}>{t('ds.s86')}</Button>
              )}
            </div>
            {/* 数据表格 */}
            <Table
              rowKey="key"
              data={mergedRows}
              columns={stagedCols}
              size="small"
              bordered
              selectedRowKeys={selectedKeys}
              onSelectChange={(keys: any) => setSelectedKeys(keys)}
              pagination={{
                current: stagedPage,
                pageSize: STAGED_PAGE_SIZE,
                total: stagedTotal,
                showJumper: true,
                onChange: (pi: unknown) => {
                  const p = typeof pi === 'number' ? pi : Number((pi as { current?: number })?.current || 1)
                  if (p === stagedPage) return
                  setSelectedKeys([])
                  setStagedPage(p)
                  reloadStaged(p)
                },
              }}
            />
          </Tabs.TabPanel>
        </Tabs>
      </Panel>
      {/* 还原前编辑内容弹窗（修改后还原为待审） */}
      <Dialog visible={!!editRow} onClose={() => setEditRow(null)} header={t("ds.s87")} width={560}
        footer={
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
            <Button variant="outline" onClick={() => setEditRow(null)}>{t('ds.s88')}</Button>
            <Button theme="primary" loading={savingEdit} onClick={confirmEditRestore}>{t('ds.s89')}</Button>
          </div>
        }>
        {editRow && (
          <div style={{ display: 'grid', gap: 12 }}>
            <div>
              <div style={{ marginBottom: 4, fontSize: 13, color: 'var(--adm-hint)' }}>
                {editRow.kind === 'entries' ? t('ds.s90') : t('ds.s91')}
              </div>
              <Input value={editSrc} onChange={(v: string) => setEditSrc(v)} />
            </div>
            <div>
              <div style={{ marginBottom: 4, fontSize: 13, color: 'var(--adm-hint)' }}>
                {editRow.kind === 'entries' ? t('ds.s92') : t('ds.s93')}
              </div>
              <Input value={editTgt} onChange={(v: string) => setEditTgt(v)} />
            </div>
            <div style={{ fontSize: 13, color: 'var(--adm-faint)' }}>{t('ds.s94')}</div>
          </div>
        )}
      </Dialog>
    </div>
  )
}
