// ============================================================================
// components/admin/KbP.tsx — 知识库管理面板
// 职责：知识包 CRUD、条目管理、文件导入、语言文化规范（安全句）
// 从 panels_d.tsx 拆分
// 2026-09-18（UI 融合）：导入校验结果文字改暗色档位（通过=中性浅色、失败=红），
//   子 tab 文案去 emoji 前缀；权限与接口口径未变。
// 2026-09-18（langcross 迁移）：TDesign 全量替换为 @/ui/langcross 纯黑组件库，
//   业务/接口/权限/i18n 键未变；表格分页改用自制 KbPager（DataTable 无 pagination prop）。
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Button, DataTable, Dialog, Input, Textarea, Switch, StatusPill, Link, Tabs,
} from '@/ui/langcross/src'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'
import { confirmDialog } from '@/components/uiDialogs'
import {
  kbPackages, kbPackageCreate, kbPackageDelete, kbEntries, kbEntryAdd, kbEntryDelete, kbEntryUpdate,
  kbEntriesImport, bitextImport, tmxImport, tmxExport,
  kbPackageStatus, kbPackageShare, kbIndexRebuild,
  kbPackGrants, kbPackGrantSet, adminUsers,
  safetyPhrases, safetyPhraseAdd, safetyPhraseDelete, safetyPhraseStatus, safetyBulkImport, type Any,
} from '@/api'
import { Panel } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { orgList, type OrgInfo } from '@/api/org'
import KbUploadDialog from '@/components/KbUploadDialog'
import DataSourcesP from './DataSourcesP'
import BrandTermsP from './BrandTermsP'
import IndustriesP from './IndustriesP'
import PersonasP from './PersonasP'

// 行布局样式组（横向排布 + 间距/顶边框变体）
const rowStyle: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }
// 行布局变体（顶距 8px）
const rowMt: any = { ...rowStyle, marginTop: 8 }
// 行布局变体（顶距 + 虚线顶边框，分组分隔用）
const rowTop: any = { ...rowStyle, marginTop: 8, borderTop: '2px dashed var(--adm-line)', paddingTop: 10 }
// resStyle 校验结果文字样式：通过=中性浅色（2026-09-18 起不再用绿色，暗色主题下与正文同档），
//   不通过=红色（只有失败才需要抢眼）。
const resStyle = (ok: boolean): any => ({ color: ok ? 'var(--lc-success)' : 'var(--lc-danger)', fontSize: 15, marginTop: 6 })

// 安全句支持语言（安全短语料按语言入库）
const SAFETY_LANGS = ['en', 'ar', 'de', 'es', 'fr', 'id_lang', 'kk', 'pt', 'ru', 'th', 'tr', 'zh_hant']
  .map((x) => ({ label: x === 'id_lang' ? 'id' : x === 'zh_hant' ? 'zh-Hant' : x, value: x }))

// packTypeLabel 包类型中文名（部门包按是否跨部门共享细分文案；t=翻译函数）。
function packTypeLabel(p: Any, t: (k: string) => string): string {
  if (p.pack_type === 'department') return (p.share_cross_dept ?? 1) === 1 ? t('kb.typeCrossDept') : t('kb.typeDepartment')
  if (p.pack_type === 'cross_dept') return t('kb.typeCrossDept')
  if (p.pack_type === 'tenant') return t('kb.typeTenant')
  if (p.pack_type === 'industry') return t('kb.typeIndustry')
  if (p.pack_type === 'persona') return t('kb.typePersona')
  if (p.pack_type === 'locale') return t('kb.typeLocale')
  return String(p.pack_type)
}

// packScopeLabel 包作用域说明文案（通用/行业/租户/部门；tpl=带变量模板函数）。
function packScopeLabel(p: Any, t: (k: string) => string, tpl: (k: string, vars?: Record<string, string | number>) => string): string {
  if (p.pack_type === 'locale') return t('kb.scopeUniversal')
  if (p.pack_type === 'industry') return t('kb.scopeIndustry')
  if (p.pack_type === 'persona') return t('kb.scopePersona')
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
  const [entryPageSize] = useState(20)
  const [entryFilter, setEntryFilter] = useState<Any>({ layer: 0, target_lang: '', q: '' })
  const [pForm, setPForm] = useState<Any>({ code: '', name: '', pack_type: 'department' })
  const [eForm, setEForm] = useState<Any>({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
  const [editingId, setEditingId] = useState<number | null>(null)
  // ★ H3 包级授权（读/写/管理三级）
  const [grantPkg, setGrantPkg] = useState<Any | null>(null)
  const [grantList, setGrantList] = useState<Any[]>([])
  const [grantUsers, setGrantUsers] = useState<Any[]>([])
  const [gForm, setGForm] = useState<Any>({ user_id: null, role: 'read' })
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
  // ★ #38 TMX 导出：仅已审核开关（module=approved，与后端 buildTMX 的模块过滤口径一致）
  const [tmxExporting, setTmxExporting] = useState(false)
  const [exportApprovedOnly, setExportApprovedOnly] = useState(false)
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
      base.push({ value: 'persona', label: t('kb.typePersona') })
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

  // createPackage 新建知识包：按角色裁剪类型（≤2 级只能部门/跨部门），创建后刷新列表。
  async function createPackage() {
    if (!pForm.code || !pForm.name) { toastWarn(t('kb.errorCodeNameRequired')); return }
    const data: Any = { code: String(pForm.code), name: String(pForm.name), pack_type: String(pForm.pack_type), role: 'source' }
    if (pForm.pack_type === 'cross_dept') {
      data.cross_all = !!pForm.cross_all
      if (!data.cross_all) data.cross_orgs = (pForm.cross_orgs || []).map((x: Any) => Number(x))
    }
    const r = await kbPackageCreate(data as never)
    if (!r.success) { toastError(r.message); return }
    setPForm({ code: '', name: '', pack_type: 'department', cross_all: false, cross_orgs: [] })
    await loadPackages()
  }
  // togglePackage 启用/停用知识包（停用即退出翻译链路，可逆）。
  async function togglePackage(p: Any) {
    const next = p.enabled === 0 ? 1 : 0
    const r = await kbPackageStatus(Number(p.id), next)
    if (!r.success) { toastError(r.message); return }
    await loadPackages()
  }
  // toggleShare 切换部门包是否跨部门共享（share_cross_dept）。
  async function toggleShare(p: Any) {
    const next = (p.share_cross_dept ?? 1) === 1 ? 0 : 1
    const r = await kbPackageShare(Number(p.id), next)
    if (!r.success) { toastError(r.message); return }
    await loadPackages()
  }
  // removePackage 删除知识包（连带条目；二次确认后执行）。
  async function removePackage(p: Any) {
    if (!(await confirmDialog({ body: tpl('kb.confirmDeletePackage', { name: String(p.name) }) }))) return
    const r = await kbPackageDelete(Number(p.id))
    if (!r.success) { toastError(r.message); return }
    await loadPackages()
  }
  // openGrants 打开包授权抽屉：拉取该包读/写/管理三级成员与被授权用户列表。
  async function openGrants(p: Any) {
    setGrantPkg(p)
    setGForm({ user_id: null, role: 'read' })
    const r = await kbPackGrants(Number(p.id))
    setGrantList(r.success ? ((r as Any).grants || []) : [])
    if (!r.success) toastError(r.message)
    if (grantUsers.length === 0) {
      const ur = await adminUsers()
      if (ur.success) setGrantUsers(((ur as Any).users || []).filter((u: Any) => u.status !== 'disabled'))
    }
  }
  // setGrant 设置/取消单用户对某包的授权级别（role=read/write/manage）。
  async function setGrant(role: string, userId?: number) {
    const uid = userId ?? Number(gForm.user_id)
    if (!grantPkg || !uid) return
    const r = await kbPackGrantSet({ pack_id: Number(grantPkg.id), user_id: uid, role })
    if (!r.success) { toastError(r.message); return }
    toastSuccess(role ? '授权已更新' : '已撤销授权')
    const rr = await kbPackGrants(Number(grantPkg.id))
    setGrantList(rr.success ? ((rr as Any).grants || []) : [])
  }

  // rebuildIndex 触发向量索引重建（异步任务，前端仅提示发起成功）。
  async function rebuildIndex() {
    if (!(await confirmDialog({ body: t('kb.rebuildConfirm') }))) return
    setRebuilding(true)
    try {
      const r = await kbIndexRebuild()
      if (!r.success) { toastError(r.message); return }
      toastSuccess(tpl('kb.rebuildDone', { n: (r as unknown as { embedded?: number }).embedded ?? 0 }))
    } finally { setRebuilding(false) }
  }

  // queryEntries 分页查询指定包条目（层级/目标语言/关键词过滤）。
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
  // openEntries 点开某包：记录选中态并加载首屏条目。
  async function openEntries(p: Any) {
    setSelectedPkg(Number(p.id))
    const f = { layer: 0, target_lang: '', q: '' }
    setEntryFilter(f)
    setEntryPage(1)
    await queryEntries(Number(p.id), f, 1)
  }
  // loadEntries 按当前过滤条件重载选中包条目列表。
  async function loadEntries(p: Any) {
    await queryEntries(Number(p.id))
  }
  // addEntry 新增一条对照条目（源句+目标语译文），保存后局部刷新计数。
  async function addEntry(pkgId: number) {
    if (!eForm.source_text) { toastWarn(t('kb.errorSourceRequired')); return }
    const r = await kbEntryAdd({
      package_id: pkgId, layer: Number(eForm.layer || 2), source_text: String(eForm.source_text),
      target_lang: String(eForm.target_lang || 'en'), target_text: String(eForm.target_text), module: String(eForm.module || ''),
    } as never)
    if (!r.success) { toastError(r.message); return }
    setEForm({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
    await loadEntries({ id: pkgId })
    await loadPackages()
  }
  // saveEntry 保存条目编辑（编辑态复用同一表单，按 editingId 区分增/改）。
  async function saveEntry(pkgId: number | null) {
    if (!eForm.source_text) { toastWarn(t('kb.errorSourceRequired')); return }
    if (editingId != null) {
      const r = await kbEntryUpdate({
        id: editingId, layer: Number(eForm.layer || 2), source_text: String(eForm.source_text),
        target_lang: String(eForm.target_lang || 'en'), target_text: String(eForm.target_text), module: String(eForm.module || ''),
      } as never)
      if (!r.success) { toastError(r.message); return }
      toastSuccess(t('kb.saved'))
      setEditingId(null)
      setEForm({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
      if (pkgId != null) await loadEntries({ id: pkgId })
      await loadPackages()
      return
    }
    if (pkgId == null) return
    await addEntry(pkgId)
  }
  // startEditEntry 进入条目行内编辑态（回填表单）。
  async function startEditEntry(e: Any) {
    setEditingId(Number(e.id))
    setEForm({ source_text: String(e.source_text || ''), layer: Number(e.layer) || 2, target_lang: String(e.target_lang || 'en'), target_text: String(e.target_text || ''), module: String(e.module || '') })
  }
  // removeEntry 删除单条条目（走二次确认，安全句不允许误删）。
  async function removeEntry(e: Any) {
    if (!(await confirmDialog({ body: t('kb.delete') }))) return
    const r = await kbEntryDelete(Number(e.id))
    if (!r.success) { toastError(r.message); return }
    const p = pkgs.find((x: Any) => x.id === selectedPkg)
    if (p) await loadEntries(p)
  }
  // bulkImport 批量导入：粘贴 CSV/TSV 文本一次建多条条目。
  async function bulkImport(pkgId: number) {
    const items: Any[] = []
    for (const line of bulkText.split('\n')) {
      const parts = line.split('|').map((s) => s.trim())
      if (parts.length < 3 || !parts[0]) continue
      items.push({ source_text: parts[0], target_lang: parts[1], target_text: parts[2], layer: parts.length >= 4 && Number(parts[3]) ? Number(parts[3]) : 2 })
    }
    if (!items.length) { toastWarn(t('kb.errorNoValidLine')); return }
    const r = await kbEntriesImport({ package_id: pkgId, entries: items } as never)
    if (!r.success) { toastError(r.message); return }
    setBulkTextMsg(tpl('kb.bulkResult', { added: (r as unknown as { added?: number }).added ?? 0, skipped: (r as unknown as { skipped?: number }).skipped ?? 0 }))
    setBulkText('')
    await loadEntries({ id: pkgId })
    await loadPackages()
  }

  // startBitextImport 上传双语对照表（csv/xlsx）导入 KB（后端按列识别）。
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
  // startTmxImport 上传 TMX 翻译记忆文件导入 KB（标准 XLIFF 互换格式）。
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
  // startTmxExport 导出翻译记忆为 TMX（Trados/memoQ 桥）。失败只提示不弹窗：
  // 导出是离线动作，打错误 toast 足够，不值得中断当前配置上下文。
  async function startTmxExport() {
    setTmxExporting(true)
    try {
      await tmxExport(exportApprovedOnly ? { module: 'approved' } : undefined)
      setTmxOk(true); setTmxMsg(t('kb.tmxExportDone'))
    } catch (e: unknown) {
      setTmxOk(false); setTmxMsg((e as { message?: string }).message || t('kb.tmxExportFail'))
    } finally { setTmxExporting(false) }
  }

  const reloadSafety = useCallback(async () => {
    await querySafety({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ, page: safetyPage })
  }, [querySafety, safetyPkgId, safetyStatusFilter, safetyFilter, safetyQ, safetyPage])

  // addSafety 新增一条语言文化规范（style 提示 / replace 替换 / forbidden 禁用）。
  async function addSafety() {
    if (!safetyPkgId || !sf.phrase.trim()) return
    const r = await safetyPhraseAdd({
      package_id: safetyPkgId, lang: String(sf.lang), phrase: sf.phrase.trim(),
      kind: String(sf.kind), replacement: sf.kind === 'replace' ? sf.replacement.trim() : '',
    } as never)
    if (!r.success) { toastError(r.message); return }
    setSf({ ...sf, phrase: '', replacement: '' })
    await reloadSafety()
  }
  // setSafetyStatus 审核安全句状态（approve 生效 / reject 驳回）。
  async function setSafetyStatus(sp: Any, status: string) {
    const r = await safetyPhraseStatus(Number(sp.id), status)
    if (!r.success) { toastError(r.message); return }
    await reloadSafety()
  }
  // removeSafety 删除安全句（内部已二次确认）。
  async function removeSafety(sp: Any) {
    if (!(await confirmDialog({ body: t('kb.deleteConfirm') }))) return
    const r = await safetyPhraseDelete(Number(sp.id))
    if (!r.success) { toastError(r.message); return }
    await reloadSafety()
  }
  // importSafety 批量导入安全句 JSON（数组：{lang,kind,phrase,replacement}）。
  async function importSafety() {
    let items: Any[]
    try { items = JSON.parse(bulkJson) } catch { toastWarn(t('kb.bulkInvalid')); return }
    if (!Array.isArray(items) || !items.length) { toastWarn(t('kb.bulkInvalid')); return }
    const r = await safetyBulkImport(safetyPkgId, items as never)
    if (!r.success) { toastError(r.message); return }
    toastSuccess(tpl('kb.bulkDone', { n: (r as unknown as { added?: number }).added ?? 0 }))
    setBulkJson('')
    await reloadSafety()
  }
  function kindLabel(k?: string) { return k === 'forbidden' ? t('kb.kindForbidden') : k === 'replace' ? t('kb.kindReplace') : t('kb.kindStyle') }
  function statusLabel(s?: string) { return s === 'pending' ? t('kb.pending') : s === 'rejected' ? t('kb.rejected') : t('kb.approved') }

  return (
    <>
      <h2 style={{ margin: '4px 0 12px' }}>{t('kb.title')}</h2>
      {/* 知识库工作台区：kb（包/条目/安全句）为主 tab；
          「行业管理」「数据源采集」为平台级中台配置，仅 isSuper（L4）挂出，
          租管看不到这两个 tab；「品牌名」所有可见本面板的角色均可维护。
          注：tab 文案原带 emoji 前缀，2026-09-18 emoji 清理后残留一个前导空格（未影响功能）。 */}
      <Tabs activeKey={kbTab} onChange={(k) => setKbTab(k)} items={[
        { key: 'kb', label: t('kb.title') },
        ...(isSuper ? [{ key: 'industries', label: '行业管理' }] : []),
        ...(isSuper ? [{ key: 'personas', label: t('persona.tab') }] : []),
        { key: 'brand', label: '品牌名' },
        ...(isSuper ? [{ key: 'scrape', label: t('admin.menuDataSources') }] : []),
      ]} />
      {/* ===== kb 主 tab：知识包 / 条目 / 安全句 ===== */}
      {kbTab === 'kb' && (<>
      {/* 顶部工具卡：上传入口 + 包类型过滤 */}
      <Panel title={t('kb.uploadTitle')} extra={<Button variant="primary" onClick={() => setKbDlg(true)}>{t('kb.topbarUpload')}</Button>}>
        <div style={{ ...rowStyle, marginBottom: 6 }}><span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('kb.uploadHint')}</span></div>
        <div style={{ fontSize: 15, color: 'var(--adm-faint)' }}>{t('kb.uploadSameAsFrontend')}</div>
      </Panel>
      {/* 快速对照添加卡：免建包直投个人草稿层的轻量入口 */}
      <Panel title={t('kb.alignTitle')}>
        <div style={rowTop}>
          <input type="file" accept=".csv,.xlsx,.xls" onChange={(e: any) => { setBitextFile(e.target.files?.[0] || null); setBitextMsg(''); e.currentTarget.value = '' }} />
          <Button onClick={() => void startBitextImport()} disabled={!bitextFile || bitextImporting}>{bitextImporting ? t('kb.bitextImporting') : t('kb.bitextImport')}</Button>
          <input type="file" accept=".tmx,.xml" onChange={(e: any) => { setTmxFile(e.target.files?.[0] || null); setTmxMsg(''); e.currentTarget.value = '' }} style={{ marginLeft: 8 }} />
          <Button onClick={() => void startTmxImport()} disabled={!tmxFile || tmxImporting}>{tmxImporting ? t('kb.tmxImporting') : t('kb.tmxImport')}</Button>
          {/* ★ #38：导出端点早已存在但前端无入口，Trados/memoQ 单向导入无法回填 —— 补导出 + 仅已审核过滤 */}
          <Button onClick={() => void startTmxExport()} disabled={tmxExporting} style={{ marginLeft: 8 }}>{tmxExporting ? t('kb.tmxExporting') : t('kb.tmxExport')}</Button>
          <label style={{ fontSize: 16, display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <input type="checkbox" checked={exportApprovedOnly} onChange={(e: any) => setExportApprovedOnly(e.target.checked)} />
            {t('kb.tmxExportApprovedOnly')}
          </label>
        </div>
        {bitextMsg && <div style={resStyle(bitextOk)}>{bitextMsg}</div>}
        {tmxMsg && <div style={resStyle(tmxOk)}>{tmxMsg}</div>}
      </Panel>
      {/* KB 文件上传向导（识别→确认→导入三步，复用全局 KbUploadDialog） */}
      <KbUploadDialog visible={kbDlg} onClose={() => setKbDlg(false)} />

      <div style={rowMt}>
        <Input value={String(pForm.code || '')} onChange={(e) => setPForm({ ...pForm, code: e.target.value })} placeholder={t('kb.codePlaceholder')} style={{ minWidth: 160 }} />
        <Input value={String(pForm.name || '')} onChange={(e) => setPForm({ ...pForm, name: e.target.value })} placeholder={t('kb.namePlaceholder')} style={{ minWidth: 180 }} />
        <select className="lc-select" value={String(pForm.pack_type)} onChange={(e) => setPForm({ ...pForm, pack_type: e.target.value })} style={{ minWidth: 180 }}>
          {packTypeOptions.map((o: Any) => <option key={String(o.value)} value={String(o.value)}>{o.label}</option>)}
        </select>
        <Button onClick={() => void createPackage()}>{t('kb.createPackage')}</Button>
      </div>
      {pForm.pack_type === 'cross_dept' && (
        <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10 }}>
          <label style={{ fontSize: 15 }}>
            <input type="checkbox" checked={!!pForm.cross_all} onChange={(e: any) => setPForm({ ...pForm, cross_all: e.target.checked, cross_orgs: e.target.checked ? [] : (pForm.cross_orgs || []) })} />
            {' '}{t('kb.scopeCrossAll')}
          </label>
          {!pForm.cross_all && (
            <>
              <span style={{ fontSize: 15, color: 'var(--adm-faint)' }}>{tpl('kb.scopeCrossDepts', { n: (pForm.cross_orgs || []).length })}:</span>
              {deptOrgs.map((o: OrgInfo) => (
                <label key={o.id} style={{ fontSize: 15 }}>
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
        <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{t('kb.entriesHint')}</span>
        {isSuper && <Button size="sm" disabled={rebuilding} onClick={() => void rebuildIndex()}>{rebuilding ? t('kb.rebuilding') : t('kb.rebuildIndex')}</Button>}
      </div>

      <div style={{ display: 'flex', gap: 8, alignItems: 'center', margin: '10px 0 6px' }}>
        <span style={{ fontSize: 15, color: 'var(--adm-hint)' }}>{t('kb.filterType')}</span>
        <select className="lc-select" value={pkgTypeFilter} onChange={(e) => setPkgTypeFilter(e.target.value)} style={{ width: 180 }}>
          <option value="">{t('kb.filterAll')}</option>
          <option value="tenant">{t('kb.typeTenant')}</option>
          <option value="industry">{t('kb.typeIndustry')}</option>
          <option value="persona">{t('kb.typePersona')}</option>
          <option value="locale">{t('kb.typeLocale')}</option>
          <option value="department">{t('kb.typeDepartment')}</option>
          <option value="cross_dept">{t('kb.typeCrossDept')}</option>
        </select>
      </div>
      {/* 知识包列表：展示名/类型作用域/条目数/状态，行操作（启停/共享/授权/删除） */}
      <div style={{ marginTop: 8 }}>
      <DataTable<any> rowKey={(row) => String(row.id)} rows={filteredPkgs} columns={[
        { key: 'id', title: 'ID', width: 60 },
        { key: 'code', title: t('kb.codePlaceholder'), width: 120 },
        { key: 'name', title: t('kb.namePlaceholder'), width: 160, render: (row) => packDisplayName(row, orgMap) },
        { key: 'pack_type', title: t('kb.colType'), width: 130, render: (row) => packTypeLabel(row, t) },
        { key: 'scope', title: t('kb.colScope'), width: 180, render: (row) => packScopeLabel(row, t, tpl) },
        { key: 'enabled', title: '启用', width: 80, render: (row) =>
          <Switch checked={row.enabled !== 0} onChange={() => { void togglePackage(row) }} /> },
        { key: 'share_cross_dept', title: t('kb.colCross'), width: 110, render: (row) =>
          row.pack_type === 'department'
            ? <Switch checked={(row.share_cross_dept ?? 1) === 1} onChange={() => { void toggleShare(row) }} />
            : <span /> },
        { key: 'op', title: t('org.colActions'), width: 200, render: (row) => (
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <Link onClick={() => openEntries(row)}>{tpl('kb.viewEntries', { count: entriesMap[Number(row.id)] || 0 })}</Link>
            <Link onClick={() => void openGrants(row)}>授权</Link>
            <Link tone="danger" onClick={() => void removePackage(row)}>{t('kb.deletePackage')}</Link>
          </div>
        ) },
      ]} />
      </div>

      {/* 条目管理弹窗：分页过滤查询 + 行内编辑/删除 + 批量文本导入 */}
      <Dialog open={selectedPkg !== null} onCancel={() => setSelectedPkg(null)}
        title={selectedPkg ? `${tpl('kb.viewEntries', { count: entriesMap[selectedPkg] || 0 })} #${selectedPkg}` : ''}
        confirmText={t('common.close')} onConfirm={() => setSelectedPkg(null)}>
        <div style={rowMt}>
          <Input value={String(eForm.source_text || '')} onChange={(e) => setEForm({ ...eForm, source_text: e.target.value })} placeholder={t('kb.sourcePlaceholder')} style={{ flex: 1 }} />
          <select className="lc-select" value={String(eForm.layer)} onChange={(e) => setEForm({ ...eForm, layer: Number(e.target.value) })}
            style={{ width: 120 }}>
            {[1, 2, 3, 4].map((n) => <option key={n} value={String(n)}>{t('kb.layer' + n)}</option>)}
          </select>
          <Input value={String(eForm.target_lang || '')} onChange={(e) => setEForm({ ...eForm, target_lang: e.target.value })} placeholder={t('kb.targetLangPlaceholder')} style={{ width: 120 }} />
          <Input value={String(eForm.target_text || '')} onChange={(e) => setEForm({ ...eForm, target_text: e.target.value })} placeholder={t('kb.translationPlaceholder')} style={{ flex: 1 }} />
          {editingId != null && <Button variant="secondary" onClick={() => { setEditingId(null); setEForm({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' }) }}>{t('kb.cancelEdit')}</Button>}
          <Button variant="primary" onClick={() => selectedPkg != null && void saveEntry(selectedPkg)}>{editingId != null ? t('kb.saveEdit') : t('kb.add')}</Button>
        </div>
        <details style={{ marginTop: 8 }}>
          <summary>{t('kb.bulkImportSummary')}</summary>
          <Textarea value={bulkText} onChange={(e) => setBulkText(e.target.value)} placeholder={t('kb.bulkPlaceholder')} style={{ minHeight: 90 }} />
          <Button style={{ marginTop: 6 }} onClick={() => selectedPkg != null && void bulkImport(selectedPkg)}>{t('kb.bulkImport')}</Button>
          {bulkTextMsg && <span style={{ marginLeft: 10, fontSize: 14, color: 'var(--adm-ok-tx)' }}>{bulkTextMsg}</span>}
        </details>
        <div style={{ ...rowMt, marginBottom: 8 }}>
          <select className="lc-select" value={String(entryFilter.layer ?? 0)} onChange={(e) => {
            const f = { ...entryFilter, layer: Number(e.target.value) }
            setEntryFilter(f); setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1)
          }} style={{ width: 140 }}>
            <option value="0">{t('kb.allLayer')}</option>
            <option value="1">{t('kb.layer1')}</option>
            <option value="2">{t('kb.layer2')}</option>
            <option value="3">{t('kb.layer3')}</option>
            <option value="4">{t('kb.layer4')}</option>
          </select>
          <Input placeholder={t('kb.targetLangPlaceholder')} value={String(entryFilter.target_lang || '')}
            onChange={(e) => setEntryFilter((f: Any) => ({ ...f, target_lang: e.target.value.trim() }))}
            onKeyDown={(e) => { if (e.key === 'Enter') { const f = entryFilter; setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1) } }} style={{ width: 150 }} />
          <Input placeholder={t('kb.searchEntries')} value={String(entryFilter.q || '')}
            onChange={(e) => setEntryFilter((f: Any) => ({ ...f, q: e.target.value }))}
            onKeyDown={(e) => { if (e.key === 'Enter') { const f = entryFilter; setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1) } }} style={{ flex: 1 }} />
          <Button variant="primary" size="sm" onClick={() => { const f = entryFilter; setEntryPage(1); void queryEntries(Number(selectedPkg), f, 1) }}>{t('kb.search')}</Button>
        </div>
        {/* 数据表格 */}
        <div style={{ maxHeight: 360, overflow: 'auto', marginTop: 8 }}>
        <DataTable<any> rowKey={(row) => String(row.id)} rows={entries} columns={[
          { key: 'id', title: 'ID', width: 70 },
          { key: 'layer', title: t('kb.colLayer'), width: 60, render: (row) => `L${row.layer}` },
          { key: 'source_text', title: t('kb.colSource'), dim: true, render: (row) => String(row.source_text ?? '—') },
          { key: 'target_lang', title: t('kb.colLang'), width: 90 },
          { key: 'target_text', title: t('kb.colTranslation'), dim: true, render: (row) => String(row.target_text ?? '—') },
          { key: 'op', title: '', width: 130, render: (row) => (
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              <Link onClick={() => void startEditEntry(row)}>{t('kb.edit')}</Link>
              <Link tone="danger" onClick={async () => { if (!(await confirmDialog({ body: t('kb.delete') }))) return; removeEntry(row) }}>{t('kb.delete')}</Link>
            </div>
          ) },
        ]} />
        </div>
        <KbPager page={entryPage} pageSize={entryPageSize} total={entryTotal}
          onGo={(p) => { if (p === entryPage) return; setEntryPage(p); void queryEntries(Number(selectedPkg), entryFilter, p) }} />
      </Dialog>

      {/* ===== 语言文化规范（安全句）区：过滤条 + 新增表单 + 审核列表 ===== */}
      <Panel title={t('kb.safetyTitle')}>
        <div style={{ fontSize: 14, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('kb.safetyHint')}</div>
        <div style={rowMt}>
          <select className="lc-select" value={String(safetyPkgId)} onChange={(e) => applySafetyQuery({ pkg_id: Number(e.target.value), status: safetyStatusFilter, ...safetyFilter, q: safetyQ })}
            style={{ minWidth: 200 }}>
            <option value="">{t('kb.selectPkg')}</option>
            {localePackages.map((p: Any) => <option key={Number(p.id)} value={String(Number(p.id))}>{packDisplayName(p, orgMap)}</option>)}
          </select>
          <select className="lc-select" value={safetyStatusFilter} onChange={(e) => applySafetyQuery({ pkg_id: safetyPkgId, status: String(e.target.value), ...safetyFilter, q: safetyQ })}
            style={{ width: 140 }}>
            <option value="">{t('kb.allStatus')}</option>
            <option value="pending">{t('kb.pending')}</option>
            <option value="approved">{t('kb.approved')}</option>
            <option value="rejected">{t('kb.rejected')}</option>
          </select>
          <select className="lc-select" value={String(safetyFilter.lang || '')} onChange={(e) => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, lang: String(e.target.value), kind: safetyFilter.kind, q: safetyQ })}
            style={{ width: 100 }}>
            <option value="">{t('kb.allLang')}</option>
            {SAFETY_LANGS.map((l: Any) => <option key={String(l.value)} value={String(l.value)}>{String(l.label)}</option>)}
          </select>
          <select className="lc-select" value={String(safetyFilter.kind || '')} onChange={(e) => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, lang: safetyFilter.lang, kind: String(e.target.value), q: safetyQ })}
            style={{ width: 110 }}>
            <option value="">{t('kb.allKind')}</option>
            <option value="style">{t('kb.kindStyle')}</option>
            <option value="forbidden">{t('kb.kindForbidden')}</option>
            <option value="replace">{t('kb.kindReplace')}</option>
          </select>
          <Input placeholder={t('kb.searchSafety')} value={safetyQ}
            onChange={(e) => setSafetyQ(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ }) }}
            style={{ flex: 1, minWidth: 180 }} />
          <Button size="sm" variant="primary" onClick={() => applySafetyQuery({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ })}>{t('kb.search')}</Button>
          <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>{tpl('kb.safetyCount', { n: safetyTotal })}</span>
        </div>
        <div style={rowMt}>
          <select className="lc-select" value={String(sf.lang)} onChange={(e) => setSf({ ...sf, lang: e.target.value })} style={{ width: 110 }}>
            {SAFETY_LANGS.map((l: Any) => <option key={String(l.value)} value={String(l.value)}>{String(l.label)}</option>)}
          </select>
          <select className="lc-select" value={String(sf.kind)} onChange={(e) => setSf({ ...sf, kind: e.target.value })}
            style={{ width: 130 }}>
            <option value="style">{t('kb.kindStyle')}</option>
            <option value="forbidden">{t('kb.kindForbidden')}</option>
            <option value="replace">{t('kb.kindReplace')}</option>
          </select>
          <Input value={String(sf.phrase || '')} onChange={(e) => setSf({ ...sf, phrase: e.target.value })} placeholder={phrasePlaceholder} style={{ flex: 1 }} />
          {sf.kind === 'replace' && <Input value={String(sf.replacement || '')} onChange={(e) => setSf({ ...sf, replacement: e.target.value })} placeholder={t('kb.replacementPlaceholder')} style={{ flex: 1 }} />}
          <Button variant="primary" disabled={!safetyPkgId || !sf.phrase.trim()} onClick={() => void addSafety()}>{t('users.create')}</Button>
        </div>
        <div style={rowMt}>
          <Input value={bulkJson} onChange={(e) => setBulkJson(e.target.value)} placeholder={t('kb.bulkPlaceholder')} style={{ flex: 1 }} />
          <Button disabled={!safetyPkgId || !bulkJson.trim()} onClick={() => void importSafety()}>{t('kb.bulkImport')}</Button>
        </div>
        {/* 数据表格 */}
        <div style={{ marginTop: 8 }}>
        <DataTable<any> rowKey={(row) => String(row.id)} rows={filteredSafety} columns={[
          { key: 'lang', title: t('kb.colLang'), width: 80 },
          { key: 'kind', title: t('kb.colKind'), width: 110, render: (row) => kindLabel(row.kind) },
          { key: 'phrase', title: t('kb.colRule') },
          ...(hasReplace ? [{ key: 'replacement', title: t('kb.colReplacement'), dim: true, render: (row: Any) => (row.kind === 'replace' ? (row.replacement || '—') : '') }] : []),
          { key: 'status', title: t('kb.colStatus'), width: 90, render: (row) =>
            <StatusPill tone={(row.status || 'approved') === 'approved' ? 'success' : 'idle'}>{(row.status || 'approved') === 'approved' ? t('kb.approved') : statusLabel(row.status)}</StatusPill> },
          { key: 'source', title: t('kb.colSource'), width: 90, render: (row) => (row.source === 'llm' ? 'LLM' : t('kb.srcManual')) },
          { key: 'op', title: t('org.colActions'), width: 200, render: (row) => (
            // 行内动作按状态条件渲染：非 approved 才给「通过」、pending 才给「驳回」；
            // 删除走二次确认（安全句直接影响线上翻译兜底，不允许误点）。
            <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
              {(row.status || 'approved') !== 'approved' && <Link onClick={() => void setSafetyStatus(row, 'approved')}>{t('kb.approve')}</Link>}
              {(row.status || 'approved') === 'pending' && <Link tone="danger" onClick={() => void setSafetyStatus(row, 'rejected')}>{t('kb.reject')}</Link>}
              <Link tone="danger" onClick={() => void removeSafety(row)}>{t('kb.delete')}</Link>
            </div>
          ) },
        ]} />
        </div>
        <KbPager page={safetyPage} pageSize={SAFETY_PAGE_SIZE} total={safetyTotal}
          onGo={(p) => { if (p === safetyPage) return; setSafetyPage(p); void querySafety({ pkg_id: safetyPkgId, status: safetyStatusFilter, ...safetyFilter, q: safetyQ, page: p }) }} />
        </Panel>
      </>)}

      {/* Tab 面板条件渲染：行业管理 / 品牌名 / 数据源采集 */}
      {kbTab === 'industries' && isSuper && <IndustriesP />}
      {kbTab === 'personas' && isSuper && <PersonasP />}
      {kbTab === 'brand' && <BrandTermsP />}
      {kbTab === 'scrape' && isSuper && <DataSourcesP />}

      {/* 包授权弹窗：读/写/管理三级成员列表 + 添加授权（仅包管理者可见入口） */}
      <Dialog open={grantPkg !== null} onCancel={() => setGrantPkg(null)}
        title={`包级授权 · ${grantPkg ? String(grantPkg.name) : ''}`}
        confirmText={t('common.close')} onConfirm={() => setGrantPkg(null)}>
        <div style={rowStyle}>
          <select className="lc-select" value={gForm.user_id == null ? '' : String(gForm.user_id)} onChange={(e) => setGForm({ ...gForm, user_id: e.target.value ? Number(e.target.value) : null })}
            style={{ width: 260 }}>
            <option value="">选择用户</option>
            {grantUsers.map((u: Any) => <option key={Number(u.id)} value={String(Number(u.id))}>{`${u.display_name || u.username}（${u.username}）`}</option>)}
          </select>
          <select className="lc-select" value={String(gForm.role)} onChange={(e) => setGForm({ ...gForm, role: String(e.target.value) })} style={{ width: 130 }}>
            <option value="read">只读 read</option>
            <option value="write">编辑 write</option>
            <option value="manage">管理 manage</option>
          </select>
          <Button variant="primary" size="sm" onClick={() => void setGrant(String(gForm.role))}>授权</Button>
          <span style={{ fontSize: 14, color: 'var(--adm-faint)' }}>读 &lt; 写 &lt; 管理（高级别含低级别）；部门管理员及以上天然拥有全部权限</span>
        </div>
        {/* 数据表格 */}
        <div style={{ marginTop: 10 }}>
        <DataTable<any> rowKey={(row) => String(row.id)} rows={grantList} columns={[
          { key: 'username', title: '用户', render: (row) => `${row.display_name || row.username || '#' + row.user_id}` },
          { key: 'role', title: '级别', width: 110, render: (row) =>
            <StatusPill tone={row.role === 'manage' ? 'warn' : 'idle'}>{row.role}</StatusPill> },
          { key: 'created_at', title: '时间', width: 160, render: (row) => String(row.created_at || '').slice(0, 16) },
          { key: 'op', title: '操作', width: 80, render: (row) => (
            <Link tone="danger" onClick={() => void setGrant('', Number(row.user_id))}>撤销</Link>) },
        ]} />
        </div>
      </Dialog>
    </>
  )
}

/** 知识包/安全句列表分页器：上一页 / 页码 / 下一页 + 跳页（DataTable 无内置分页，配套自制）。 */
function KbPager({ page, pageSize, total, onGo }: {
  page: number; pageSize: number; total: number; onGo: (p: number) => void
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  if (pages <= 1) return null
  return (
    <div style={{ display: 'flex', gap: 8, alignItems: 'center', justifyContent: 'flex-end', marginTop: 10, flexWrap: 'wrap' }}>
      <Button size="sm" variant="secondary" disabled={page <= 1} onClick={() => onGo(page - 1)}>{'‹'}</Button>
      <span style={{ fontSize: 15, color: 'var(--lc-text-3)' }}>{page} / {pages}</span>
      <Button size="sm" variant="secondary" disabled={page >= pages} onClick={() => onGo(page + 1)}>{'›'}</Button>
      <input className="lc-input" type="number" value={page} min={1} max={pages} style={{ width: 64 }}
        onChange={(e) => { const v = Number(e.target.value); if (v >= 1 && v <= pages) onGo(v) }} />
    </div>
  )
}
