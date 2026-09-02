// ============================================================================
// components/admin/DataSourcesP.tsx — 行业包/语言文化包自动采集面板（超管 L4）
// 职责：数据源 CRUD/启停/手动采集 + 待审增量批量审批（通过→热加载）+ 概览。
// 依赖后端：/api/admin/kb-scrape/*（见 api/scrape.ts）。
// ============================================================================
import { useEffect, useMemo, useState } from 'react'
import { Button, Input, MessagePlugin, Select, Switch, Table, Tag, Tabs, RadioGroup, Radio } from 'tdesign-react'
import { useT } from '@/i18n'
import { Panel, toastResp } from './parts'
import {
  scrapeSources, scrapeSourceCreate, scrapeSourceUpdate, scrapeSourceStatus, scrapeSourceRun,
  scrapeStaged, scrapeApprove, scrapeSummary,
  kbRewardConfigGet, kbRewardConfigSet,
  type ScrapeSource, type StagedEntry, type StagedPhrase, type ScrapeSummary,
} from '@/api/scrape'

/** 数据源类型中文名 */
/** 数据源类型中文名 */
function kindName(kind: string): string {
  return kind === 'official_api' ? '官方API' : kind === 'limited_web' ? '受限抓取' : kind === 'llm_gen' ? 'LLM生成' : kind
}

/** tier 可信度徽标 */
function tierTag(tier: number): React.ReactNode {
  const map: Record<number, { text: string; theme: 'success' | 'warning' | 'danger' | 'default' }> = {
    1: { text: '1·官方', theme: 'success' },
    2: { text: '2·受限抓取', theme: 'warning' },
    3: { text: '3·LLM', theme: 'danger' },
  }
  const m = map[tier] || { text: `t${tier}`, theme: 'default' as const }
  return <Tag theme={m.theme} variant="light">{m.text}</Tag>
}

/** 数据源面板组件（超管 L4）：数据源 CRUD/启停/手动采集 + 待审池批量审批 + 概览 */
export default function DataSourcesP() {
  const [, t] = useT()
  // ---- 数据源 ----
  const [sources, setSources] = useState<ScrapeSource[]>([])
  const [summary, setSummary] = useState<ScrapeSummary | null>(null)
  const [showForm, setShowForm] = useState(false)
  const [running, setRunning] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState<Partial<ScrapeSource>>({
    kind: 'official_api', name: '', base_url: '', lang: 'en', industry: '', pack_type: 'locale', tier: 1, freq_hours: 24,
  })

  // ---- 待审池 ----
  const [tab, setTab] = useState<'sources' | 'staged'>('sources')
  const [entries, setEntries] = useState<StagedEntry[]>([])
  const [phrases, setStagedPhrases] = useState<StagedPhrase[]>([])
  const [stagedFilter, setStagedFilter] = useState<{ pack_type: string; status: string; lang: string }>({ pack_type: '', status: 'pending', lang: '' })
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [approving, setApproving] = useState(false)

  // ---- KB 上传奖励（功能⑥） ----
  const [rewardCfg, setRewardCfg] = useState<{ enabled: boolean; per_char: number; daily_cap: number } | null>(null)
  const [savingReward, setSavingReward] = useState(false)

  /** 读取奖励配置 */
  const loadReward = async () => {
    const r = await kbRewardConfigGet()
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
    if (!toastResp(r, '奖励配置已保存')) return
    await loadReward()
  }

  /** 刷新数据源 + 概览 */
  /** 刷新数据源 + 概览 */
  const reload = async () => {
    const [s, sm] = await Promise.all([scrapeSources(), scrapeSummary()])
    if (s.success) setSources(s.sources || [])
    if (sm.success) setSummary(sm.summary || null)
  }

  /** 刷新待审池 */
  const reloadStaged = async () => {
    const r = await scrapeStaged(stagedFilter)
    if (r.success) {
      setEntries(r.entries || [])
      setStagedPhrases(r.phrases || [])
    }
  }

  useEffect(() => { reload() }, [])
  useEffect(() => { if (tab === 'staged') reloadStaged() }, [tab, stagedFilter])
  useEffect(() => { loadReward() }, [])

  /** 新增数据源 */
  const onCreate = async () => {
    if (!form.name?.trim()) { void MessagePlugin.error('请填写数据源名称'); return }
    setSaving(true)
    const r = await scrapeSourceCreate(form)
    setSaving(false)
    if (!toastResp(r, '数据源已创建')) return
    setShowForm(false)
    setForm({ kind: 'official_api', name: '', base_url: '', lang: 'en', industry: '', pack_type: 'locale', tier: 1, freq_hours: 24 })
    reload()
  }

  /** 手动采集一轮 */
  const onRun = async () => {
    setRunning(true)
    const r = await scrapeSourceRun()
    setRunning(false)
    toastResp(r, '采集已触发（高占用自动暂停，可续传）')
    reload()
  }

  /** 批量审批 */
  const onApprove = async (action: 'approve' | 'reject') => {
    if (!selectedIds.length) { void MessagePlugin.error('请先勾选条目'); return }
    setApproving(true)
    const label = action === 'approve' ? '通过' : '驳回'
    // 条目与安全句分两类处理
    let ok = true
    let rewardNote = ''
    if (entries.length) {
      const r = await scrapeApprove('entries', selectedIds, action)
      if (action === 'approve' && r.rewards?.length) rewardNote = `，发放奖励 ${r.rewards.reduce((x: number, y) => x + (y.tokens ?? 0), 0)} token`
      if (!toastResp(r, ok ? `已${label} ${r.applied ?? 0} 条并热加载${rewardNote}` : undefined)) ok = false
    }
    if (phrases.length) {
      const r = await scrapeApprove('phrases', selectedIds, action)
      if (!toastResp(r, ok ? `已${label} ${r.applied ?? 0} 条并热加载` : undefined)) ok = false
    }
    setApproving(false)
    setSelectedIds([])
    reloadStaged()
    reload()
  }

  /** 拼接待审合并行（条目+安全句统一展示） */
  const mergedRows = useMemo(() => {
    const es = entries.map((e) => ({
      id: e.id, kind: 'entries' as const, tier: e.tier, pack_type: e.pack_type,
      lang: `${e.src_lang}→${e.tgt_lang}`, src: e.src_text, tgt: e.tgt_text, source_url: e.source_url,
    }))
    const ps = phrases.map((p) => ({
      id: p.id, kind: 'phrases' as const, tier: p.tier, pack_type: 'locale',
      lang: p.lang, src: `[${p.kind}] ${p.phrase}`, tgt: p.replacement || '', source_url: '语言文化规范',
    }))
    return [...es, ...ps]
  }, [entries, phrases])

  /** 表格列定义 */
  const srcCols = [
    { colKey: 'name', title: '名称' },
    { colKey: 'kind', title: '类型', cell: ({ row }: any) => kindName(row.kind) },
    { colKey: 'pack_type', title: '包类型', cell: ({ row }: any) => row.pack_type === 'industry' ? '行业包' : '语言文化包' },
    { colKey: 'lang', title: '语言', cell: ({ row }: any) => row.lang || '不限' },
    { colKey: 'industry', title: '行业', cell: ({ row }: any) => row.industry || '—' },
    { colKey: 'tier', title: '可信度', cell: ({ row }: any) => tierTag(row.tier) },
    { colKey: 'base_url', title: 'URL', cell: ({ row }: any) => row.base_url ? <span style={{ wordBreak: 'break-all' }}>{row.base_url}</span> : '—' },
    { colKey: 'last_status', title: '最近状态', cell: ({ row }: any) => <span style={{ color: row.last_status === 'ok' ? '#2f9e44' : '#d03050' }}>{row.last_status || '未采集'}</span> },
    { colKey: 'op', title: '操作', cell: ({ row }: any) => (
      <Switch size="small" value={row.enabled === 1} onChange={(v: boolean) => { scrapeSourceStatus(row.id, v ? 1 : 0).then((r) => { toastResp(r); reload() }) }} />
    ) },
  ]

  /** 待审池表格列定义 */
  const stagedCols = [
    { colKey: 'id', title: 'ID', width: 70 },
    { colKey: 'pack_type', title: '类型', width: 90, cell: ({ row }: any) => row.pack_type === 'industry' ? <Tag theme="primary" variant="light">行业</Tag> : <Tag theme="success" variant="light">语言文化</Tag> },
    { colKey: 'tier', title: '可信度', width: 90, cell: ({ row }: any) => tierTag(row.tier) },
    { colKey: 'lang', title: '语言', width: 90 },
    { colKey: 'src', title: '源文本' },
    { colKey: 'tgt', title: '目标/规范' },
    { colKey: 'source_url', title: '来源' },
  ]

  return (
    <div>
      {/* 概览 */}
      <Panel title="行业包/语言文化包自动采集" extra={
        <div style={{ display: 'flex', gap: 8 }}>
          <Tag theme="primary" variant="light">待审条目 {summary?.pending_entries ?? 0}</Tag>
          <Tag theme="success" variant="light">待审安全句 {summary?.pending_phrases ?? 0}</Tag>
          <Tag theme="warning" variant="light">数据源 {summary?.sources_enabled ?? 0}/{summary?.sources_total ?? 0}</Tag>
          <Tag variant="light">最近采集 {summary?.last_daily || '未开始'}</Tag>
          <Button size="small" loading={running} onClick={onRun}>立即采集一轮</Button>
        </div>
      }>
        {/* 功能⑥ KB 上传奖励开关（超管） */}
        <div style={{ border: '1px solid #e5e6eb', borderRadius: 8, padding: 12, marginBottom: 12, display: 'flex', gap: 24, alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontWeight: 600, fontSize: 13 }}>KB 上传奖励</span>
          {rewardCfg && (
            <>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                <Switch size="small" value={rewardCfg.enabled} onChange={(v: boolean) => setRewardCfg((c) => (c ? { ...c, enabled: v } : c))} />
                发放开关
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                1 字符
                <Input type="number" style={{ width: 110 }} value={String(rewardCfg.per_char)} onChange={(v: string) => setRewardCfg((c) => (c ? { ...c, per_char: Number(v) || 0 } : c))} />
                token
              </label>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 }}>
                单租户日封顶
                <Input type="number" style={{ width: 130 }} value={String(rewardCfg.daily_cap)} onChange={(v: string) => setRewardCfg((c) => (c ? { ...c, daily_cap: Number(v) || 0 } : c))} />
                token
              </label>
              <Button size="small" theme="primary" loading={savingReward} onClick={onSaveReward}>保存</Button>
            </>
          )}
        </div>
        <Tabs value={tab} onChange={(v) => setTab(v as 'sources' | 'staged')}>
          <Tabs.TabPanel value="sources" label={`数据源（${sources.length}）`}>
            <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 8 }}>
              <Button size="small" theme="primary" onClick={() => setShowForm((v) => !v)}>{showForm ? '收起' : '新增数据源'}</Button>
            </div>
            {showForm && (
              <div style={{ border: '1px solid #e5e6eb', borderRadius: 8, padding: 16, marginBottom: 12, display: 'grid', gap: 10, gridTemplateColumns: 'repeat(auto-fill, minmax(200px,1fr))' }}>
                <div><div style={{ marginBottom: 4 }}>名称*</div><Input value={form.name} onChange={(v: string) => setForm((f) => ({ ...f, name: v }))} placeholder="如 维基词典API" /></div>
                <div><div style={{ marginBottom: 4 }}>类型</div>
                  <Select value={form.kind} onChange={(v: any) => setForm((f) => ({ ...f, kind: String(v) }))} options={[{ value: 'official_api', label: '官方API' }, { value: 'limited_web', label: '受限抓取' }, { value: 'llm_gen', label: 'LLM生成' }]} /></div>
                <div><div style={{ marginBottom: 4 }}>包类型</div>
                  <Select value={form.pack_type} onChange={(v: any) => setForm((f) => ({ ...f, pack_type: String(v) }))} options={[{ value: 'locale', label: '语言文化包' }, { value: 'industry', label: '行业包' }]} /></div>
                <div><div style={{ marginBottom: 4 }}>语言（industry 目标语言 / locale 语言）</div><Input value={form.lang} onChange={(v: string) => setForm((f) => ({ ...f, lang: v }))} placeholder="en" /></div>
                <div><div style={{ marginBottom: 4 }}>行业 code（语言文化包可空）</div><Input value={form.industry} onChange={(v: string) => setForm((f) => ({ ...f, industry: v }))} placeholder="auto / general" /></div>
                <div><div style={{ marginBottom: 4 }}>可信度</div>
                  <Select value={form.tier} onChange={(v: any) => setForm((f) => ({ ...f, tier: Number(v) }))} options={[{ value: 1, label: '1·官方' }, { value: 2, label: '2·受限抓取' }, { value: 3, label: '3·LLM' }]} /></div>
                <div><div style={{ marginBottom: 4 }}>采集频次（小时）</div><Input type="number" value={String(form.freq_hours ?? 24)} onChange={(v: string) => setForm((f) => ({ ...f, freq_hours: parseInt(v) || 24 }))} /></div>
                <div style={{ gridColumn: '1 / -1' }}><div style={{ marginBottom: 4 }}>入口 URL / 词表URL（official_api=种子词表；limited_web=术语表页；llm_gen 可空）</div><Input value={form.base_url} onChange={(v: string) => setForm((f) => ({ ...f, base_url: v }))} placeholder="https://…" /></div>
                <div style={{ gridColumn: '1 / -1', display: 'flex', gap: 8 }}>
                  <Button size="small" theme="primary" loading={saving} onClick={onCreate}>保存</Button>
                  <Button size="small" variant="outline" onClick={() => setShowForm(false)}>取消</Button>
                </div>
              </div>
            )}
            <Table rowKey="id" data={sources} columns={srcCols} size="small" bordered />
          </Tabs.TabPanel>
          <Tabs.TabPanel value="staged" label={`待审增量（${mergedRows.length}）`}>
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8, flexWrap: 'wrap' }}>
              <RadioGroup value={stagedFilter.pack_type} onChange={(v: string) => setStagedFilter((f) => ({ ...f, pack_type: v }))}>
                <Radio value="">全部</Radio>
                <Radio value="industry">行业</Radio>
                <Radio value="locale">语言文化</Radio>
              </RadioGroup>
              <RadioGroup value={stagedFilter.status} onChange={(v: string) => setStagedFilter((f) => ({ ...f, status: v }))}>
                <Radio value="pending">待审</Radio>
                <Radio value="approved">已通过</Radio>
                <Radio value="rejected">已驳回</Radio>
              </RadioGroup>
              <div style={{ width: 140 }}><Input placeholder="语言过滤" value={stagedFilter.lang} onChange={(v: string) => setStagedFilter((f) => ({ ...f, lang: v }))} /></div>
              <div style={{ flex: 1 }} />
              <Button size="small" theme="primary" loading={approving} onClick={() => onApprove('approve')}>批量通过并热加载</Button>
              <Button size="small" theme="danger" variant="outline" loading={approving} onClick={() => onApprove('reject')}>批量驳回</Button>
            </div>
            <Table
              rowKey="id"
              data={mergedRows}
              columns={stagedCols}
              size="small"
              bordered
              selectedRowKeys={selectedIds}
              onSelectChange={(keys: any) => setSelectedIds(keys)}
              pagination={{ pageSize: 20, total: mergedRows.length }}
            />
          </Tabs.TabPanel>
        </Tabs>
      </Panel>
    </div>
  )
}
