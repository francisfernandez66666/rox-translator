// ============================================================================
// components/admin/KbP.tsx — 知识库管理面板
// 职责：知识包 CRUD、条目管理、文件导入、语言文化规范（安全句）
// 从 panels_d.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, Textarea, MessagePlugin, Tabs,
} from 'tdesign-react'
import { confirmDialog } from '@/components/uiDialogs'
import {
  kbPackages, kbPackageCreate, kbPackageDelete, kbEntries, kbEntryAdd, kbEntryDelete, kbEntryUpdate,
  kbEntriesImport, bitextImport, tmxImport,
  kbPackageStatus, kbPackageShare, kbIndexRebuild,
  safetyPhrases, safetyPhraseAdd, safetyPhraseDelete, safetyPhraseStatus, safetyBulkImport,
} from '@/api'
import { Panel, toastResp } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { orgList, type OrgInfo } from '@/api/org'
import KbUploadDialog from '@/components/KbUploadDialog'
import DataSourcesP from './DataSourcesP'
import BrandTermsP from './BrandTermsP'
import IndustriesP from './IndustriesP'

type Any = Record<string, any>

const rowStyle: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }
const rowMt: any = { ...rowStyle, marginTop: 8 }
const rowTop: any = { ...rowStyle, marginTop: 8, borderTop: '1px dashed #e0e0e0', paddingTop: 10 }
// resStyle 校验结果文字样式：通过=绿色，不通过=红色。
const resStyle = (ok: boolean): any => ({ color: ok ? '#1a7f37' : '#c0392b', fontSize: 13, marginTop: 6 })

const SAFETY_LANGS = ['en', 'ar', 'de', 'es', 'fr', 'id_lang', 'kk', 'pt', 'ru', 'th', 'tr', 'zh_hant']
  .map((x) => ({ label: x === 'id_lang' ? 'id' : x === 'zh_hant' ? 'zh-Hant' : x, value: x }))

// packTypeLabel 包类型中文名（部门包按是否跨部门共享细分文案；t=翻译函数）。
function packTypeLabel(p: Any, t: (k: string) => string): string {
  if (p.pack_type === 'department') return (p.share_cross_dept ?? 1) === 1 ? t('kb.typeCrossDept') : t('kb.typeDepartment')
  if (p.pack_type === 'cross_dept') return t('kb.typeCrossDept')
  if (p.pack_type === 'tenant') return t('kb.typeTenant')
  if (p.pack_type === 'industry') return t('kb.typeIndustry')
  if (p.pack_type === 'locale') return t('kb.typeLocale')
  return String(p.pack_type)
}

// packScopeLabel 包作用域说明文案（通用/行业/租户/部门；tpl=带变量模板函数）。
function packScopeLabel(p: Any, t: (k: string) => string, tpl: (k: string, vars?: Record<string, string | number>) => string): string {
  if (p.pack_type === 'locale') return t('kb.scopeUniversal')
  if (p.pack_type === 'industry') return t('kb.scopeIndustry')
  if (p.pack_type === 'tenant') return t('kb.scopeTenant')
  if (p.pack_type === 'department') return (p.share_cross_dept ?? 1) === 1 ? t('kb.scopeCross') : t('kb.scopeDept')
  if (p.pack_type === 'cross_dept') return p.cross_all ? t('kb.scopeCrossAll') : tpl('kb.scopeCrossDepts', { n: (p.cross_orgs || []).length })
  return ''
}

// orgPath 沿父链拼组织全路径（如「总部/研发部/平台组」；map=组织 ID→信息索引）。
function orgPath(map: Map<number, OrgInfo>, id: number): string {
  const parts: string[] = []
  let cur = map.get(id)
  while (cur) {
    parts.unshift(cur.name)
    cur = map.get(cur.parent_id)
  }
  return parts.join(' / ')
}

// packDisplayName 包展示名：部门/租户包拼上所属组织路径，其余直接用原名。
function packDisplayName(p: Any, orgMap: Map<number, OrgInfo>): string {
  if (p.pack_type === 'tenant') return p.tenant_name || p.name
  if (p.pack_type === 'department') {
    if (orgMap.has(p.org_id)) return orgPath(orgMap, p.org_id)
    return p.org_name || p.name
  }
  return p.name
}

// KbP 知识库管理面板主组件：知识包列表/CRUD、条目导入与管理、语言文化规范（安全句）三大 Tab。
export function KbP() {
  const [, t, tpl] = useT()
  const { myLevel, isSuper, activeTenantId, orgs } = useAdmin()
  const [pkgs, setPkgs] = useState<Any[]>([])
  const [entriesMap, setEntriesMap] = useState<Record<number, number>>({})
  const [pkgTypeFilter, setPkgTypeFilter] = useState('')
  const filteredPkgs = useMemo<Any[]>(() => {
    if (!pkgTypeFilter) return pkgs
    return pkgs.filter((p: Any) => p.pack_type === pkgTypeFilter)
  }, [pkgs, pkgTypeFilter])
  const [orgMap, setOrgMap] = useState<Map<number, OrgInfo>>(new Map())
  const [selectedPkg, setSelectedPkg] = useState<number | null>(null)
  const [entries, setEntries] = useState<Any[]>([])
  const [entryTotal, setEntryTotal] = useState(0)
  const [entryPage, setEntryPage] = useState(1)
  const [entryPageSize, setEntryPageSize] = useState(20)
  const [entryFilter, setEntryFilter] = useState<Any>({ layer: 0, target_lang: '', q: '' })
  const [pForm, setPForm] = useState<Any>({ code: '', name: '', pack_type: 'department' })
  const [eForm, setEForm] = useState<Any>({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
  const [editingId, setEditingId] = useState<number | null>(null)
  const [bulkText, setBulkText] = useState('')
  const [bulkTextMsg, setBulkTextMsg] = useState('')
  const [kbDlg, setKbDlg] = useState(false)
  const [bitextFile, setBitextFile] = useState<File | null>(null)
  const [bitextImporting, setBitextImporting] = useState(false)
  const [bitextMsg, setBitextMsg] = useState('')
  const [bitextOk, setBitextOk] = useState(false)
  const [tmxFile, setTmxFile] = useState<File | null>(null)
  const [tmxImporting, setTmxImporting] = useState(false)
  const [tmxMsg, setTmxMsg] = useState('')
  const [tmxOk, setTmxOk] = useState(false)
  const [safetyList, setSafetyList] = useState<Any[]>([])
  const [safetyTotal, setSafetyTotal] = useState(0)
  const [safetyPage, setSafetyPage] = useState(1)
  const SAFETY_PAGE_SIZE = 20
  const [safetyQ, setSafetyQ] = useState('')
  const [safetyFilter, setSafetyFilter] = useState<Any>({ lang: '', kind: '' })
  const [safetyPkgId, setSafetyPkgId] = useState<number>(0)
  const [safetyStatusFilter, setSafetyStatusFilter] = useState('')
  const [bulkJson, setBulkJson] = useState('')
  const [sf, setSf] = useState<Any>({ lang: 'en', kind: 'style', phrase: '', replacement: '' })
  const [rebuilding, setRebuilding] = useState(false)
  const [kbTab, setKbTab] = useState('kb')

  const packTypeOptions = useMemo(() => {
    if (myLevel <= 2) return [
      { value: 'department', label: t('kb.typeDepartment') },
      { value: 'cross_dept', label: t('kb.typeCrossDept') },
    ]
    const base = [
      { value: 'tenant', label: t('kb.typeTenant') },
      { value: 'department', label: t('kb.typeDepartment') },
      { value: 'cross_dept', label: t('kb.typeCrossDept') },
    ]
    if (isSuper) {
      base.push({ value: 'industry', label: t('kb.typeIndustry') })
      base.push({ value: 'locale', label: t('kb.typeLocale') })
    }
    return base
  }, [myLevel, isSuper, t])

  const deptOrgs = useMemo(() => (orgs || []).filter((o: OrgInfo) => o.type === 'dept'), [orgs])
  const localePackages = useMemo(() => pkgs.filter((p: Any) => p.pack_type === 'locale'), [pkgs])
  const filteredSafety = useMemo(() => safetyList, [safetyList])
  const hasReplace = useMemo(() => safetyList.some((s: Any) => s.kind === 'replace'), [safetyList])
  const phrasePlaceholder = useMemo(() =>
    sf.kind === 'forbidden' ? t('kb.phraseForbidden') : sf.kind === 'replace' ? t('kb.phraseReplace') : t('kb.phraseStyle')
  , [sf.kind, t])

  const loadPackages = useCallback(async () => {
    const r = await kbPackages()
    if (r.success) {
      const list = ((r as unknown as { packages?: Any[] }).packages) || []
      setPkgs(list)
      const map: Record<number, number> = {}
      for (const p of list) map[Number(p.id)] = Number((p as Any).entry_count) || 0
      setEntriesMap(map)
    }
    try {
      const o = await orgList()
      if (o && (o as unknown as { success?: boolean }).success) {
        const m = new Map<number, OrgInfo>()
        for (const x of ((o as unknown as { orgs?: OrgInfo[] }).orgs) || []) m.set(x.id, x)
        setOrgMap(m)
      }
    } catch { /* 忽略 */ }
  }, [activeTenantId])

  const loadSafety = useCallback(async () => {
    const r = await safetyPhrases({
      package_id: safetyPkgId || undefined,
      status: safetyStatusFilter || undefined,
      lang: safetyFilter.lang || undefined,
      kind: safetyFilter.kind || undefined,
      q: safetyQ || undefined,
      page: safetyPage,
      page_size: SAFETY_PAGE_SIZE,
    })
    if (r.success) {
      const list = ((r as unknown as { phrases?: Any[] }).phrases) || []
      setSafetyList(list)
      setSafetyTotal(Number((r as unknown as { total?: number }).total) || 0)
      if (!safetyPkgId && localePackages.length) setSafetyPkgId(Number((localePackages[0] as Any).id))
    }
  }, [safetyPkgId, localePackages, safetyPage, safetyStatusFilter, safetyFilter, safetyQ])

  const querySafety = useCallback(async (query: Any) => {
    const r = await safetyPhrases({
      package_id: Number(query.pkg_id) || undefined,
      lang: query.lang || undefined,
      kind: query.kind || undefined,
      status: query.status || undefined,
      q: query.q || undefined,
      page: Number(query.page),
      page_size: SAFETY_PAGE_SIZE,
    })
    if (r.success) {
      setSafetyList(((r as unknown as { phrases?: Any[] }).phrases) || [])
      setSafetyTotal(Number((r as unknown as { total?: number }).total) || 0)
    }
  }, [])

  const applySafetyQuery = useCallback((q: Any) => {
    setSafetyPkgId(Number(q.pkg_id) || 0)
    setSafetyStatusFilter(String(q.status || ''))
    setSafetyFilter((f: Any) => ({ ...f, lang: String(q.lang || ''), kind: String(q.kind || '') }))
    setSafetyQ(String(q.q || ''))
    setSafetyPage(1)
    void querySafety({ ...q, page: 1 })
  }, [querySafety])

  useEffect(() => { void loadPackages() }, [loadPackages])
  useEffect(() => { void loadSafety() }, [loadSafety])

  async function createPackage() {
    if (!pForm.code || !pForm.name) { MessagePlugin.warning(t('kb.errorCodeNameRequired')); return }
    const data: Any = { code: String(pForm.code), name: String(pForm.name), pack_type: String(pForm.pack_type), role: 'source' }
    if (pForm.pack_type === 'cross_dept') {
      data.cross_all = !!pForm.cross_all
      if (!data.cross_all) data.cross_orgs = (pForm.cross_orgs || []).map((x: Any) => Number(x))
    }
    const r = await kbPackageCreate(data as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    setPForm({ code: '', name: '', pack_type: 'department', cross_all: false, cross_orgs: [] })
    await loadPackages()
  }
  async function togglePackage(p: Any) {
    const next = p.enabled === 0 ? 1 : 0
    const r = await kbPackageStatus(Number(p.id), next)
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadPackages()
  }
  async function toggleShare(p: Any) {
    const next = (p.share_cross_dept ?? 1) === 1 ? 0 : 1
    const r = await kbPackageShare(Number(p.id), next)
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadPackages()
  }
  async function removePackage(p: Any) {
    if (!(await confirmDialog({ body: tpl('kb.confirmDeletePackage', { name: String(p.name) }) }))) return
    const r = await kbPackageDelete(Number(p.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadPackages()
  }
  async function rebuildIndex() {
    if (!(await confirmDialog({ body: t('kb.rebuildConfirm') }))) return
    setRebuilding(true)
    try {
      const r = await kbIndexRebuild()
      if (!r.success) { MessagePlugin.error(r.message); return }
      MessagePlugin.success(tpl('kb.rebuildDone', { n: (r as unknown as { embedded?: number }).embedded ?? 0 }))
    } finally { setRebuilding(false) }
  }

  async function queryEntries(pkgId: number, filter?: Any, page?: number) {
    const f = filter && typeof filter === 'object' ? filter : entryFilter
    const pg = typeof page === 'number' ? page : entryPage
    const r = await kbEntries(pkgId, {
      layer: f.layer || undefined,
      target_lang: f.target_lang || undefined,
      q: f.q || undefined,
      page: pg,
      page_size: entryPageSize,
    })
    if (r.success) {
      setEntries(((r as unknown as { entries?: Any[] }).entries) || [])
      setEntryTotal(Number((r as unknown as { total?: number }).total) || 0)
    }
  }
  async function openEntries(p: Any) {
    setSelectedPkg(Number(p.id))
    const f = { layer: 0, target_lang: '', q: '' }
    setEntryFilter(f)
    setEntryPage(1)
    await queryEntries(Number(p.id), f, 1)
  }
  async function loadEntries(p: Any) {
    await queryEntries(Number(p.id))
  }
  async function addEntry(pkgId: number) {
    if (!eForm.source_text) { MessagePlugin.warning(t('kb.errorSourceRequired')); return }
    const r = await kbEntryAdd({
      package_id: pkgId, layer: Number(eForm.layer || 2), source_text: String(eForm.source_text),
      target_lang: String(eForm.target_lang || 'en'), target_text: String(eForm.target_text), module: String(eForm.module || ''),
    } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    setEForm({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
    await loadEntries({ id: pkgId })
    await loadPackages()
  }
  async function saveEntry(pkgId: number | null) {
    if (!eForm.source_text) { MessagePlugin.warning(t('kb.errorSourceRequired')); return }
    if (editingId != null) {
      const r = await kbEntryUpdate({
        id: editingId, layer: Number(eForm.layer || 2), source_text: String(eForm.source_text),
        target_lang: String(eForm.target_lang || 'en'), target_text: String(eForm.target_text), module: String(eForm.module || ''),
      } as never)
      if (!r.success) { MessagePlugin.error(r.message); return }
      MessagePlugin.success(t('kb.saved'))
      setEditingId(null)
      setEForm({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
      if (pkgId != null) await loadEntries({ id: pkgId })
      await loadPackages()
      return
    }
    if (pkgId == null) return
    await addEntry(pkgId)
  }
  async function startEditEntry(e: Any) {
    setEditingId(Number(e.id))
    setEForm({ source_text: String(e.source_text || ''), layer: Number(e.layer) || 2, target_lang: String(e.target_lang || 'en'), target_text: String(e.target_text || ''), module: String(e.module || '') })
  }
  async function removeEntry(e: Any) {
    const r = await kbEntryDelete(Number(e.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    const p = pkgs.find((x: Any) => x.id === selectedPkg)
    if (p) await loadEntries(p)
  }
  async function bulkImport(pkgId: number) {
    const items: Any[] = []
    for (const line of bulkText.split('\n')) {
      const parts = line.split('|').map((s) => s.trim())
      if (parts.length < 3 || !parts[0]) continue
      items.push({ source_text: parts[0], target_lang: parts[1], target_text: parts[2], layer: parts.length >= 4 && Number(parts[3]) ? Number(parts[3]) : 2 })
    }
    if (!items.length) { MessagePlugin.warning(t('kb.errorNoValidLine')); return }
    const r = await kbEntriesImport({ package_id: pkgId, entries: items } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    setBulkTextMsg(tpl('kb.bulkResult', { added: (r as unknown as { added?: number }).added ?? 0, skipped: (r as unknown as { skipped?: number }).skipped ?? 0 }))
    setBulkText('')
    await loadEntries({ id: pkgId })
    await loadPackages()
  }

  async function startBitextImport() {
    if (!bitextFile) return
    setBitextImporting(true)
    try {
      const r = await bitextImport(bitextFile)
      setBitextOk(!!r.success)
      setBitextMsg(r.success
        ? `${t('kb.bitextDone')} +${((r as unknown as { added?: number }).added) ?? 0} / ${t('kb.bitextSkipped')} ${((r as unknown as { skipped?: number }).skipped) ?? 0}`
        : (r.message || '导入失败'))
      if (r.success) { setBitextFile(null) }
    } finally { setBitextImporting(false) }
  }
  async function startTmxImport() {
    if (!tmxFile) return
    setTmxImporting(true)
    try {
      const r = await tmxImport(tmxFile)
      setTmxOk(!!r.success)
      setTmxMsg(r.success
        ? `${tpl('kb.tmxTus', { n: ((r as unknown as { tus?: number }).tus) ?? 0 })} · ${t('kb.bitextDone')} +${((r as unknown as { added?: number }).added) ?? 0} / ${t('kb.bitextSkipped')} ${((r as unknown as { skipped?: number }).skipped) ?? 0}`
        : (r.message || '导入失败'))
      if (r.success) { setTmxFile(null) }
    } finally { setTmxImporting(false) }
  }

  const reloadSafety = useCallback(async () => {
    await querySafety({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ, page: safetyPage })
  }, [querySafety, safetyPkgId, safetyStatusFilter, safetyFilter, safetyQ, safetyPage])

  async function addSafety() {
    if (!safetyPkgId || !sf.phrase.trim()) return
    const r = await safetyPhraseAdd({
      package_id: safetyPkgId, lang: String(sf.lang), phrase: sf.phrase.trim(),
      kind: String(sf.kind), replacement: sf.kind === 'replace' ? sf.replacement.trim() : '',
    } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    setSf({ ...sf, phrase: '', replacement: '' })
    await reloadSafety()
  }
  async function setSafetyStatus(sp: Any, status: string) {
    const r = await safetyPhraseStatus(Number(sp.id), status)
    if (!r.success) { MessagePlugin.error(r.message); return }
    await reloadSafety()
  }
  async function removeSafety(sp: Any) {
    if (!(await confirmDialog({ body: t('kb.deleteConfirm') }))) return
    const r = await safetyPhraseDelete(Number(sp.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await reloadSafety()
  }
  async function importSafety() {
    let items: Any[]
    try { items = JSON.parse(bulkJson) } catch { MessagePlugin.warning(t('kb.bulkInvalid')); return }
    if (!Array.isArray(items) || !items.length) { MessagePlugin.warning(t('kb.bulkInvalid')); return }
    const r = await safetyBulkImport(safetyPkgId, items as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(tpl('kb.bulkDone', { n: (r as unknown as { added?: number }).added ?? 0 }))
    setBulkJson('')
    await reloadSafety()
  }
  function kindLabel(k?: string) { return k === 'forbidden' ? t('kb.kindForbidden') : k === 'replace' ? t('kb.kindReplace') : t('kb.kindStyle') }
  function statusLabel(s?: string) { return s === 'pending' ? t('kb.pending') : s === 'rejected' ? t('kb.rejected') : t('kb.approved') }

  return (
    <>
      <h2 style={{ margin: '4px 0 12px' }}>{t('kb.title')}</h2>
      <Tabs value={kbTab} onChange={(v) => setKbTab(String(v))}>
        <Tabs.TabPanel value="kb" label={`📚 ${t('kb.title')}`}>
      <Panel title={t('kb.uploadTitle')} extra={<Button theme="primary" onClick={() => setKbDlg(true)}>{t('kb.topbarUpload')}</Button>}>
        <div style={{ ...rowStyle, marginBottom: 6 }}><span style={{ fontSize: 13, color: '#556' }}>{t('kb.uploadHint')}</span></div>
        <div style={{ fontSize: 13, color: '#889' }}>{t('kb.uploadSameAsFrontend')}</div>
      </Panel>
      <Panel title={t('kb.alignTitle')}>
        <div style={rowTop}>
          <input type="file" accept=".csv,.xlsx,.xls" onChange={(e: any) => { setBitextFile(e.target.files?.[0] || null); setBitextMsg(''); e.currentTarget.value = '' }} />
          <Button onClick={() => void startBitextImport()} disabled={!bitextFile || bitextImporting} loading={bitextImporting}>{bitextImporting ? t('kb.bitextImporting') : t('kb.bitextImport')}</Button>
          <input type="file" accept=".tmx,.xml" onChange={(e: any) => { setTmxFile(e.target.files?.[0] || null); setTmxMsg(''); e.currentTarget.value = '' }} style={{ marginLeft: 8 }} />
          <Button onClick={() => void startTmxImport()} disabled={!tmxFile || tmxImporting} loading={tmxImporting}>{tmxImporting ? t('kb.tmxImporting') : t('kb.tmxImport')}</Button>
        </div>
        {bitextMsg && <div style={resStyle(bitextOk)}>{bitextMsg}</div>}
        {tmxMsg && <div style={resStyle(tmxOk)}>{tmxMsg}</div>}
      </Panel>
      <KbUploadDialog visible={kbDlg} onClose={() => setKbDlg(false)} />

      <div style={rowMt}>
        <Input value={String(pForm.code || '')} onChange={(v: any) => setPForm({ ...pForm, code: v })} placeholder={t('kb.codePlaceholder')} style={{ minWidth: 160 }} />
        <Input value={String(pForm.name || '')} onChange={(v: any) => setPForm({ ...pForm, name: v })} placeholder={t('kb.namePlaceholder')} style={{ minWidth: 180 }} />
        <Select value={String(pForm.pack_type)} onChange={(v: any) => setPForm({ ...pForm, pack_type: v })} options={packTypeOptions} style={{ minWidth: 180 }} />
        <Button onClick={() => void createPackage()}>{t('kb.createPackage')}</Button>
      </div>
      {pForm.pack_type === 'cross_dept' && (
        <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10 }}>
          <label style={{ fontSize: 13 }}>
            <input type="checkbox" checked={!!pForm.cross_all} onChange={(e: any) => setPForm({ ...pForm, cross_all: e.target.checked, cross_orgs: e.target.checked ? [] : (pForm.cross_orgs || []) })} />
            {' '}{t('kb.scopeCrossAll')}
          </label>
          {!pForm.cross_all && (
            <>
              <span style={{ fontSize: 13, color: '#889' }}>{tpl('kb.scopeCrossDepts', { n: (pForm.cross_orgs || []).length })}:</span>
              {deptOrgs.map((o: OrgInfo) => (
                <label key={o.id} style={{ fontSize: 13 }}>
                  <input type="checkbox" checked={(pForm.cross_orgs || []).includes(o.id)} onChange={(e: any) => {
                    const set = new Set<number>(pForm.cross_orgs || [])
                    if (e.target.checked) set.add(o.id); else set.delete(o.id)
                    setPForm({ ...pForm, cross_orgs: Array.from(set) })
                  }} />
                  {' '}{o.name}
                </label>
              ))}
            </>
          )}
        </div>
      )}
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8 }}>
        <span style={{ fontSize: 12, color: '#889' }}>{t('kb.entriesHint')}</span>
        {isSuper && <Button size="small" disabled={rebuilding} onClick={() => void rebuildIndex()}>{rebuilding ? t('kb.rebuilding') : t('kb.rebuildIndex')}</Button>}
      </div>

      <div style={{ display: 'flex', gap: 8, alignItems: 'center', margin: '10px 0 6px' }}>
        <span style={{ fontSize: 13, color: '#556' }}>{t('kb.filterType')}</span>
        <Select value={pkgTypeFilter} onChange={(v: any) => setPkgTypeFilter(String(v))}
          options={[
            { label: t('kb.filterAll'), value: '' },
            { label: t('kb.typeTenant'), value: 'tenant' },
            { label: t('kb.typeIndustry'), value: 'industry' },
            { label: t('kb.typeLocale'), value: 'locale' },
            { label: t('kb.typeDepartment'), value: 'department' },
            { label: t('kb.typeCrossDept'), value: 'cross_dept' },
          ]} style={{ width: 180 }} />
      </div>
      <Table rowKey="id" size="small" data={filteredPkgs} style={{ marginTop: 8 }}
        columns={[
           { colKey: 'id', title: 'ID', width: 60 },
           { colKey: 'code', title: t('kb.codePlaceholder'), width: 120 },
           { colKey: 'name', title: t('kb.namePlaceholder'), cell: ({ row }: any) => packDisplayName(row, orgMap) },
           { colKey: 'pack_type', title: t('kb.colType'), width: 130, cell: ({ row }: any) => packTypeLabel(row, t) },
           { colKey: 'scope', title: t('kb.colScope'), width: 180, cell: ({ row }: any) => packScopeLabel(row, t, tpl) },
           { colKey: 'enabled', title: '启用', width: 80, cell: ({ row }: any) =>
             <Switch size="small" value={row.enabled !== 0} onChange={async () => { await togglePackage(row) }} /> },
           { colKey: 'share_cross_dept', title: t('kb.colCross'), width: 110, cell: ({ row }: any) =>
             row.pack_type === 'department'
               ? <Switch size="small" value={(row.share_cross_dept ?? 1) === 1} onChange={async () => { await toggleShare(row) }} />
               : <span /> },
          { colKey: 'op', title: t('org.colActions'), width: 200, cell: ({ row }: any) => (
            <Space size={2}>
              <Button size="small" variant="text" onClick={() => openEntries(row)}>{tpl('kb.viewEntries', { count: entriesMap[Number(row.id)] || 0 })}</Button>
              <Popconfirm content={tpl('kb.confirmDeletePackage', { name: String(row.name) })} onConfirm={async () => { await removePackage(row) }}>
                <Button size="small" variant="text" theme="danger">{t('kb.deletePackage')}</Button>
              </Popconfirm>
            </Space>
          ) },
        ] as never} />

      <Dialog visible={selectedPkg !== null} onClose={() => setSelectedPkg(null)}
        header={selectedPkg ? `${tpl('kb.viewEntries', { count: entriesMap[selectedPkg] || 0 })} #${selectedPkg}` : ''} width={900} footer={false}>
        <div style={rowMt}>
          <Input value={String(eForm.source_text || '')} onChange={(v: any) => setEForm({ ...eForm, source_text: v })} placeholder={t('kb.sourcePlaceholder')} style={{ flex: 1 }} />
          <Select value={String(eForm.layer)} onChange={(v: any) => setEForm({ ...eForm, layer: Number(v) })}
            options={[1, 2, 3, 4].map((n) => ({ label: t('kb.layer' + n), value: n }))} style={{ width: 120 }} />
          <Input value={String(eForm.target_lang || '')} onChange={(v: any) => setEForm({ ...eForm, target_lang: v })} placeholder={t('kb.targetLangPlaceholder')} style={{ width: 120 }} />
          <Input value={String(eForm.target_text || '')} onChange={(v: any) => setEForm({ ...eForm, target_text: v })} placeholder={t('kb.translationPlaceholder')} style={{ flex: 1 }} />
          {editingId != null && <Button variant="outline" onClick={() => { setEditingId(null); setEForm({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' }) }}>{t('kb.cancelEdit')}</Button>}
          <Button theme="primary" onClick={() => selectedPkg != null && void saveEntry(selectedPkg)}>{editingId != null ? t('kb.saveEdit') : t('kb.add')}</Button>
        </div>
        <details style={{ marginTop: 8 }}>
          <summary>{t('kb.bulkImportSummary')}</summary>
          <Textarea autosize={{ minRows: 4 }} value={bulkText} onChange={(v: any) => setBulkText(v)} placeholder={t('kb.bulkPlaceholder')} />
          <Button style={{ marginTop: 6 }} onClick={() => selectedPkg != null && void bulkImport(selectedPkg)}>{t('kb.bulkImport')}</Button>
          {bulkTextMsg && <span style={{ marginLeft: 10, fontSize: 12, color: '#1a7f37' }}>{bulkTextMsg}</span>}
        </details>
        <div style={{ ...rowMt, marginBottom: 8 }}>
          <Select value={String(entryFilter.layer ?? 0)} onChange={(v: any) => {
            const f = { ...entryFilter, layer: Number(v) }
            setEntryFilter(f); setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1)
          }}
            options={[{ label: t('kb.allLayer'), value: '0' }, { label: t('kb.layer1'), value: '1' }, { label: t('kb.layer2'), value: '2' }, { label: t('kb.layer3'), value: '3' }, { label: t('kb.layer4'), value: '4' }]} style={{ width: 140 }} />
          <Input placeholder={t('kb.targetLangPlaceholder')} value={String(entryFilter.target_lang || '')}
            onChange={(v: string) => setEntryFilter((f: Any) => ({ ...f, target_lang: v.trim() }))}
            onEnter={() => { const f = entryFilter; setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1) }} style={{ width: 150 }} />
          <Input placeholder={t('kb.searchEntries')} value={String(entryFilter.q || '')}
            onChange={(v: string) => setEntryFilter((f: Any) => ({ ...f, q: v }))}
            onEnter={() => { const f = entryFilter; setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1) }} style={{ flex: 1 }} />
          <Button theme="primary" size="small" onClick={() => { const f = entryFilter; setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1) }}>{t('kb.search')}</Button>
        </div>
        <Table rowKey="id" size="small" maxHeight={360} data={entries} style={{ marginTop: 8 }}
          pagination={{
            current: entryPage,
            pageSize: entryPageSize,
            total: entryTotal,
            showJumper: true,
            onChange: async (pi: unknown) => {
              const p = typeof pi === 'number' ? pi : Number((pi as { current?: number })?.current || 1)
              if (p === entryPage) return
              setEntryPage(p)
              await queryEntries(Number(selectedPkg), entryFilter, p)
            },
          }}
          columns={[
            { colKey: 'id', title: 'ID', width: 70 },
            { colKey: 'layer', title: t('kb.colLayer'), width: 60, cell: ({ row }: any) => `L${row.layer}` },
            { colKey: 'source_text', title: t('kb.colSource'), ellipsis: true },
            { colKey: 'target_lang', title: t('kb.colLang'), width: 90 },
            { colKey: 'target_text', title: t('kb.colTranslation'), ellipsis: true },
            { colKey: 'op', title: '', width: 130, cell: ({ row }: any) => (
              <Space size={4}>
                <Button size="small" variant="text" theme="primary" onClick={() => void startEditEntry(row)}>{t('kb.edit')}</Button>
                <Popconfirm content={t('kb.delete')} onConfirm={async () => { await removeEntry(row) }}>
                  <Button size="small" variant="text" theme="danger">{t('kb.delete')}</Button>
                </Popconfirm>
              </Space>
            ) },
          ] as never} />
      </Dialog>

      <Panel title={t('kb.safetyTitle')}>
        <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{t('kb.safetyHint')}</div>
        <div style={rowMt}>
          <Select value={safetyPkgId} onChange={(v: any) => applySafetyQuery({ pkg_id: Number(v), status: safetyStatusFilter, ...safetyFilter, q: safetyQ })}
            options={localePackages.map((p: Any) => ({ label: packDisplayName(p, orgMap), value: Number(p.id) }))} style={{ minWidth: 200 }} placeholder={t('kb.selectPkg')} />
          <Select value={safetyStatusFilter} onChange={(v: any) => applySafetyQuery({ pkg_id: safetyPkgId, status: String(v), ...safetyFilter, q: safetyQ })}
            options={[{ label: t('kb.allStatus'), value: '' }, { label: t('kb.pending'), value: 'pending' }, { label: t('kb.approved'), value: 'approved' }, { label: t('kb.rejected'), value: 'rejected' }]} style={{ width: 140 }} />
          <Select value={String(safetyFilter.lang || '')} onChange={(v: any) => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, lang: String(v), kind: safetyFilter.kind, q: safetyQ })}
            options={[{ label: t('kb.allLang'), value: '' }, ...SAFETY_LANGS.map((l: Any) => ({ label: String(l), value: String(l) }))]} style={{ width: 100 }} />
          <Select value={String(safetyFilter.kind || '')} onChange={(v: any) => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, lang: safetyFilter.lang, kind: String(v), q: safetyQ })}
            options={[{ label: t('kb.allKind'), value: '' }, { label: t('kb.kindStyle'), value: 'style' }, { label: t('kb.kindForbidden'), value: 'forbidden' }, { label: t('kb.kindReplace'), value: 'replace' }]} style={{ width: 110 }} />
          <Input placeholder={t('kb.searchSafety')} value={safetyQ}
            onChange={(v: string) => setSafetyQ(v)}
            onEnter={() => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ })}
            style={{ flex: 1, minWidth: 180 }} />
          <Button size="small" theme="primary" onClick={() => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ })}>{t('kb.search')}</Button>
          <span style={{ fontSize: 12, color: '#889' }}>{tpl('kb.safetyCount', { n: safetyTotal })}</span>
        </div>
        <div style={rowMt}>
          <Select value={String(sf.lang)} onChange={(v: any) => setSf({ ...sf, lang: v })} options={SAFETY_LANGS} style={{ width: 110 }} />
          <Select value={String(sf.kind)} onChange={(v: any) => setSf({ ...sf, kind: v })}
            options={[{ label: t('kb.kindStyle'), value: 'style' }, { label: t('kb.kindForbidden'), value: 'forbidden' }, { label: t('kb.kindReplace'), value: 'replace' }]} style={{ width: 130 }} />
          <Input value={String(sf.phrase || '')} onChange={(v: any) => setSf({ ...sf, phrase: v })} placeholder={phrasePlaceholder} style={{ flex: 1 }} />
          {sf.kind === 'replace' && <Input value={String(sf.replacement || '')} onChange={(v: any) => setSf({ ...sf, replacement: v })} placeholder={t('kb.replacementPlaceholder')} style={{ flex: 1 }} />}
          <Button theme="success" disabled={!safetyPkgId || !sf.phrase.trim()} onClick={() => void addSafety()}>{t('users.create')}</Button>
        </div>
        <div style={rowMt}>
          <Input value={bulkJson} onChange={(v: any) => setBulkJson(v)} placeholder={t('kb.bulkPlaceholder')} style={{ flex: 1 }} />
          <Button disabled={!safetyPkgId || !bulkJson.trim()} onClick={() => void importSafety()}>{t('kb.bulkImport')}</Button>
        </div>
        <Table rowKey="id" size="small" data={filteredSafety} style={{ marginTop: 8 }}
          pagination={{
            current: safetyPage,
            pageSize: SAFETY_PAGE_SIZE,
            total: safetyTotal,
            showJumper: true,
            onChange: async (pi: unknown) => {
              const p = typeof pi === 'number' ? pi : Number((pi as { current?: number })?.current || 1)
              if (p === safetyPage) return
              setSafetyPage(p)
              await querySafety({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ, page: p })
            },
          }}
          columns={[
            { colKey: 'lang', title: t('kb.colLang'), width: 80 },
            { colKey: 'kind', title: t('kb.colKind'), width: 110, cell: ({ row }: any) => kindLabel(row.kind) },
            { colKey: 'phrase', title: t('kb.colRule') },
            ...(hasReplace ? [{ colKey: 'replacement', title: t('kb.colReplacement'), cell: ({ row }: any) => (row.kind === 'replace' ? (row.replacement || '—') : '') }] : []),
            { colKey: 'status', title: t('kb.colStatus'), width: 90, cell: ({ row }: any) =>
              <Tag theme={(row.status || 'approved') === 'approved' ? 'success' : 'default'}>{(row.status || 'approved') === 'approved' ? t('kb.approved') : statusLabel(row.status)}</Tag> },
            { colKey: 'source', title: t('kb.colSource'), width: 90, cell: ({ row }: any) => (row.source === 'llm' ? 'LLM' : t('kb.srcManual')) },
            { colKey: 'op', title: t('org.colActions'), width: 200, cell: ({ row }: any) => (
              <Space size={4}>
                {(row.status || 'approved') !== 'approved' && <Button size="small" variant="text" onClick={() => void setSafetyStatus(row, 'approved')}>{t('kb.approve')}</Button>}
                {(row.status || 'approved') === 'pending' && <Button size="small" variant="text" theme="danger" onClick={() => void setSafetyStatus(row, 'rejected')}>{t('kb.reject')}</Button>}
                <Popconfirm content={t('kb.deleteConfirm')} onConfirm={() => void removeSafety(row)}><Button size="small" variant="text" theme="danger">✕</Button></Popconfirm>
              </Space>
            ) },
          ] as never} />
        </Panel>
        </Tabs.TabPanel>
        {isSuper && (
          <Tabs.TabPanel value="industries" label={`🏭 行业管理`}>
            <IndustriesP />
          </Tabs.TabPanel>
        )}
        <Tabs.TabPanel value="brand" label={`🏷️ 品牌名`}>
          <BrandTermsP />
        </Tabs.TabPanel>
        {isSuper && (
          <Tabs.TabPanel value="scrape" label={`🕷️ ${t('admin.menuDataSources')}`}>
            <DataSourcesP />
          </Tabs.TabPanel>
        )}
      </Tabs>
    </>
  )
}
