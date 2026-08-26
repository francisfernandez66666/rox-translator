// ============================================================================
// components/admin/panels_d.tsx — Kb / Models / Workflow / Tickets(反馈·审批·TM审核)
// 端口自 Vue 对应组件，行为与 i18n key 保持一致。
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Button, Table, Dialog, Input, Select, Switch, Tag, Space, Popconfirm, Textarea, MessagePlugin,
} from 'tdesign-react'
import {
  kbPackages, kbPackageCreate, kbPackageDelete, kbEntries, kbEntryAdd, kbEntryDelete,
  kbEntriesImport, kbRecognizeFile, bitextImport, tmxImport, kbImportFile,
  kbPackageStatus, kbPackageShare, kbIndexRebuild,
  safetyPhrases, safetyPhraseAdd, safetyPhraseDelete, safetyPhraseStatus, safetyBulkImport,
  adminModels, adminModelsSave, stageModels, stageModelsSave, adminPolicy, adminPolicySave,
  flowConfig, flowSave, evalsList as apiEvalsList,
  approveList, approveAction,
  feedbackList, feedbackReply, resolveFeedback, createFeedback,
  listTmReview, approveTmReview, rejectTmReview,
} from '@/api'
import { Panel, Field, toastResp, num } from './parts'
import { fmtTime } from '@/lib/ui'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'

type Any = Record<string, any>

const rowStyle: any = { display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }
const rowMt: any = { ...rowStyle, marginTop: 8 }
const rowTop: any = { ...rowStyle, marginTop: 8, borderTop: '1px dashed #e0e0e0', paddingTop: 10 }
const resStyle = (ok: boolean): any => ({ color: ok ? '#1a7f37' : '#c0392b', fontSize: 13, marginTop: 6 })
const cardStyle: any = { border: '1px solid #e3e6ef', borderRadius: 8, padding: 14, marginBottom: 12 }

/** firstTranslation 从工单载荷 JSON 中取首个语言的译文（供审批终稿预填；解析失败返回空） */
function firstTranslation(finalResult: unknown): string {
  try {
    const p = typeof finalResult === 'string' && finalResult ? JSON.parse(finalResult) : null
    const tr = (p?.translations || {}) as Record<string, string>
    const k = Object.keys(tr)[0]
    return k ? tr[k] : ''
  } catch { return '' }
}

// 安全句支持的语言选项（id_lang 显示为 id，zh_hant 显示为 zh-Hant）
const SAFETY_LANGS = ['en', 'ar', 'de', 'es', 'fr', 'id_lang', 'kk', 'pt', 'ru', 'th', 'tr', 'zh_hant']
  .map((x) => ({ label: x === 'id_lang' ? 'id' : x === 'zh_hant' ? 'zh-Hant' : x, value: x }))

// ==================== 知识库（Vue Kb.vue / KnowledgeBase.vue） ====================
export function KbP() {
  const [, t, tpl] = useT()
  const { myLevel, isSuper, activeTenantId } = useAdmin()
  // 包列表与各包条目数缓存
  const [pkgs, setPkgs] = useState<Any[]>([])
  const [entriesMap, setEntriesMap] = useState<Record<number, number>>({})
  // 当前选中的包与其条目
  const [selectedPkg, setSelectedPkg] = useState<number | null>(null)
  const [entries, setEntries] = useState<Any[]>([])
  // 新建包与新建条目表单
  const [pForm, setPForm] = useState<Any>({ code: '', name: '', pack_type: 'industry' })
  const [eForm, setEForm] = useState<Any>({ source_text: '', layer: 2, target_lang: 'en', target_text: '', module: '' })
  // 批量文本导入
  const [bulkText, setBulkText] = useState('')
  const [bulkTextMsg, setBulkTextMsg] = useState('')

  // ---- KB 文件上传 ----
  const [kbFile, setKbFile] = useState<File | null>(null)
  const [kbRecognizing, setKbRecognizing] = useState(false)
  const [kbRecognized, setKbRecognized] = useState<Any | null>(null)
  const [kbImportPkg, setKbImportPkg] = useState<number>(0)
  const [kbImporting, setKbImporting] = useState(false)
  const [kbImportResult, setKbImportResult] = useState<Any | null>(null)
  const [bitextFile, setBitextFile] = useState<File | null>(null)
  const [bitextImporting, setBitextImporting] = useState(false)
  const [bitextMsg, setBitextMsg] = useState('')
  const [bitextOk, setBitextOk] = useState(false)
  const [tmxFile, setTmxFile] = useState<File | null>(null)
  const [tmxImporting, setTmxImporting] = useState(false)
  const [tmxMsg, setTmxMsg] = useState('')
  const [tmxOk, setTmxOk] = useState(false)

  // ---- 语言文化规范（安全句）----
  const [safetyList, setSafetyList] = useState<Any[]>([])
  const [safetyPkgId, setSafetyPkgId] = useState<number>(0)
  const [safetyStatusFilter, setSafetyStatusFilter] = useState('')
  // 批量 JSON 导入安全句
  const [bulkJson, setBulkJson] = useState('')
  const [sf, setSf] = useState<Any>({ lang: 'en', kind: 'style', phrase: '', replacement: '' })
  const [rebuilding, setRebuilding] = useState(false)

  // 当前用户可创建的知识包类型（受角色等级限制）
  const packTypeOptions = useMemo(() => {
    if (myLevel <= 2) return [{ value: 'department', label: t('kb.typeDepartment') }]
    const base = [{ value: 'tenant', label: t('kb.typeTenant') }, { value: 'department', label: t('kb.typeDepartment') }]
    if (isSuper) {
      base.push({ value: 'industry', label: t('kb.typeIndustry') })
      base.push({ value: 'locale', label: t('kb.typeLocale') })
    }
    return base
  }, [myLevel, isSuper, t])

  // 安全句相关派生状态
  const localePackages = useMemo(() => pkgs.filter((p: Any) => p.pack_type === 'locale'), [pkgs])
  const filteredSafety = useMemo(() => safetyList.filter((s: Any) =>
    (!safetyPkgId || s.package_id === safetyPkgId) &&
    (!safetyStatusFilter || (s.status || 'approved') === safetyStatusFilter)
  ), [safetyList, safetyPkgId, safetyStatusFilter])
  const hasReplace = useMemo(() => filteredSafety.some((s: Any) => s.kind === 'replace'), [filteredSafety])
  const phrasePlaceholder = useMemo(() =>
    sf.kind === 'forbidden' ? t('kb.phraseForbidden') : sf.kind === 'replace' ? t('kb.phraseReplace') : t('kb.phraseStyle')
  , [sf.kind, t])

  // 加载知识包列表并统计每个包条目数
  const loadPackages = useCallback(async () => {
    const r = await kbPackages()
    if (r.success) {
      const list = ((r as unknown as { packages?: Any[] }).packages) || []
      setPkgs(list)
      const map: Record<number, number> = {}
      for (const p of list) {
        try { const e = await kbEntries(Number(p.id)); map[Number(p.id)] = (e as unknown as { entries?: Any[] }).entries?.length || 0 } catch { map[Number(p.id)] = 0 }
      }
      setEntriesMap(map)
    }
  }, [activeTenantId])

  // 加载语言文化规范（安全句）列表
  const loadSafety = useCallback(async () => {
    const r = await safetyPhrases()
    if (r.success) {
      const list = ((r as unknown as { phrases?: Any[] }).phrases) || []
      setSafetyList(list)
      if (!safetyPkgId && localePackages.length) setSafetyPkgId(Number((localePackages[0] as Any).id))
    }
  }, [safetyPkgId, localePackages])

  // 初始化加载包与安全句
  useEffect(() => { void loadPackages() }, [loadPackages])
  useEffect(() => { void loadSafety() }, [loadSafety])

  // ---- 包 CRUD ----
  // 创建知识包
  async function createPackage() {
    if (!pForm.code || !pForm.name) { MessagePlugin.warning(t('kb.errorCodeNameRequired')); return }
    const r = await kbPackageCreate({ code: String(pForm.code), name: String(pForm.name), pack_type: String(pForm.pack_type), role: 'source' } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    setPForm({ code: '', name: '', pack_type: 'industry' })
    await loadPackages()
  }
  // 启用/禁用知识包
  async function togglePackage(p: Any) {
    const next = p.enabled === 0 ? 1 : 0
    const r = await kbPackageStatus(Number(p.id), next)
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadPackages()
  }
  // 切换部门级知识包跨部门共享
  async function toggleShare(p: Any) {
    const next = (p.share_cross_dept ?? 1) === 1 ? 0 : 1
    const r = await kbPackageShare(Number(p.id), next)
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadPackages()
  }
  // 删除知识包
  async function removePackage(p: Any) {
    if (!window.confirm(tpl('kb.confirmDeletePackage', { name: String(p.name) }))) return
    const r = await kbPackageDelete(Number(p.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadPackages()
  }
  // 重建知识库索引（超管）
  async function rebuildIndex() {
    if (!window.confirm(t('kb.rebuildConfirm'))) return
    setRebuilding(true)
    try {
      const r = await kbIndexRebuild()
      if (!r.success) { MessagePlugin.error(r.message); return }
      MessagePlugin.success(tpl('kb.rebuildDone', { n: (r as unknown as { embedded?: number }).embedded ?? 0 }))
    } finally { setRebuilding(false) }
  }

  // ---- 条目 ----
  // 打开某个包的条目对话框并加载条目
  async function openEntries(p: Any) {
    setSelectedPkg(Number(p.id))
    const r = await kbEntries(Number(p.id))
    if (r.success) setEntries((r as unknown as { entries?: Any[] }).entries || [])
  }
  async function loadEntries(p: Any) {
    const r = await kbEntries(Number(p.id))
    if (r.success) setEntries((r as unknown as { entries?: Any[] }).entries || [])
  }
  // 向指定包添加单条条目
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
  // 删除单条条目
  async function removeEntry(e: Any) {
    const r = await kbEntryDelete(Number(e.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    const p = pkgs.find((x: Any) => x.id === selectedPkg)
    if (p) await loadEntries(p)
  }
  // 批量导入条目：每行格式 source_text|target_lang|target_text|[layer]
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

  // ---- 文件上传 ----
  // 识别上传文件中的多语言列
  async function startRecognize() {
    if (!kbFile) return
    setKbRecognizing(true); setKbImportResult(null)
    try {
      const r = await kbRecognizeFile(kbFile)
      if (r.success) setKbRecognized(r as Any)
      else MessagePlugin.error(r.message || t('kb.recognizeFailed'))
    } catch (err: any) { MessagePlugin.error(tpl('kb.recognizeErr', { msg: err?.message || t('kb.networkErr') })) }
    finally { setKbRecognizing(false) }
  }
  // 将识别结果导入指定知识包
  async function startImport() {
    if (!kbRecognized?.temp_id || !kbImportPkg) return
    setKbImporting(true); setKbImportResult(null)
    try {
      const r = await kbImportFile({ temp_id: String(kbRecognized.temp_id), package_id: kbImportPkg })
      setKbImportResult(r as Any)
      if (r.success) { await loadPackages(); setKbImportPkg(0); setKbRecognized(null) }
    } catch (err: any) {
      setKbImportResult({ success: false, message: tpl('kb.importErr', { msg: err?.message || t('kb.networkErr') }) } as Any)
    } finally { setKbImporting(false) }
  }
  // 导入双语文本文件
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
  // 导入 TMX 文件
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

  // ---- 安全句 ----
  // 新增语言文化规范
  async function addSafety() {
    if (!safetyPkgId || !sf.phrase.trim()) return
    const r = await safetyPhraseAdd({
      package_id: safetyPkgId, lang: String(sf.lang), phrase: sf.phrase.trim(),
      kind: String(sf.kind), replacement: sf.kind === 'replace' ? sf.replacement.trim() : '',
    } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    setSf({ ...sf, phrase: '', replacement: '' })
    await loadSafety()
  }
  // 审核/变更安全句状态
  async function setSafetyStatus(sp: Any, status: string) {
    const r = await safetyPhraseStatus(Number(sp.id), status)
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadSafety()
  }
  // 删除安全句
  async function removeSafety(sp: Any) {
    if (!window.confirm(t('kb.deleteConfirm'))) return
    const r = await safetyPhraseDelete(Number(sp.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadSafety()
  }
  // 批量 JSON 导入安全句
  async function importSafety() {
    let items: Any[]
    try { items = JSON.parse(bulkJson) } catch { MessagePlugin.warning(t('kb.bulkInvalid')); return }
    if (!Array.isArray(items) || !items.length) { MessagePlugin.warning(t('kb.bulkInvalid')); return }
    const r = await safetyBulkImport(safetyPkgId, items as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(tpl('kb.bulkDone', { n: (r as unknown as { added?: number }).added ?? 0 }))
    setBulkJson('')
    await loadSafety()
  }
  function kindLabel(k?: string) { return k === 'forbidden' ? t('kb.kindForbidden') : k === 'replace' ? t('kb.kindReplace') : t('kb.kindStyle') }
  function statusLabel(s?: string) { return s === 'pending' ? t('kb.pending') : s === 'rejected' ? t('kb.rejected') : t('kb.approved') }

  return (
    <>
      {/* 页面标题 */}
      <h2 style={{ margin: '4px 0 12px' }}>{t('kb.title')}</h2>

      {/* ===== 文件导入 ===== */}
      <Panel title={t('kb.uploadTitle')}>
        <div style={{ ...rowStyle, marginBottom: 6 }}><span style={{ fontSize: 13, color: '#556' }}>{t('kb.uploadHint')}</span></div>
        <div style={rowMt}>
          <input type="file" accept=".csv,.xlsx,.xls" onChange={(e: any) => { setKbFile(e.target.files?.[0] || null); setKbRecognized(null); setKbImportResult(null); setKbImportPkg(0); e.currentTarget.value = '' }} />
          <Button onClick={() => void startRecognize()} disabled={!kbFile || kbRecognizing}>{kbRecognizing ? t('kb.recognizing') : t('kb.recognize')}</Button>
        </div>
        {kbRecognized && (
          <div style={{ marginTop: 8, fontSize: 13 }}>
            <div>
              {tpl('kb.kbTotal', { total: (kbRecognized as Any).total, n: ((kbRecognized as Any).lang_cols || []).length })}
              {((kbRecognized as Any).new_langs || []).length > 0 && <span> {t('kb.kbNewLangs')} {((kbRecognized as Any).new_langs || []).map((l: string) => <Tag key={l} size="small">{l}</Tag>)}</span>}
            </div>
            <div style={rowMt}>
              <Select value={kbImportPkg} onChange={(v: any) => setKbImportPkg(Number(v))} placeholder={t('kb.selectPkg')}
                options={pkgs.map((p: Any) => ({ label: `[${p.pack_type}] ${p.name}`, value: Number(p.id) }))} style={{ minWidth: 220 }} />
              <Button theme="success" onClick={() => void startImport()} disabled={!kbImportPkg || kbImporting}>{t('kb.import')}</Button>
            </div>
            {kbImportResult && <div style={resStyle(!!(kbImportResult as Any).success)}>{(kbImportResult as Any).message}</div>}
          </div>
        )}
        <div style={rowTop}>
          <input type="file" accept=".csv,.xlsx,.xls" onChange={(e: any) => { setBitextFile(e.target.files?.[0] || null); setBitextMsg(''); e.currentTarget.value = '' }} />
          <Button onClick={() => void startBitextImport()} disabled={!bitextFile || bitextImporting}>{bitextImporting ? t('kb.bitextImporting') : t('kb.bitextImport')}</Button>
          <input type="file" accept=".tmx,.xml" onChange={(e: any) => { setTmxFile(e.target.files?.[0] || null); setTmxMsg(''); e.currentTarget.value = '' }} style={{ marginLeft: 8 }} />
          <Button onClick={() => void startTmxImport()} disabled={!tmxFile || tmxImporting}>{tmxImporting ? t('kb.tmxImporting') : t('kb.tmxImport')}</Button>
        </div>
        {bitextMsg && <div style={resStyle(bitextOk)}>{bitextMsg}</div>}
        {tmxMsg && <div style={resStyle(tmxOk)}>{tmxMsg}</div>}
      </Panel>

      {/* ===== 新建知识包 ===== */}
      <div style={rowMt}>
        <Input value={String(pForm.code || '')} onChange={(v: any) => setPForm({ ...pForm, code: v })} placeholder={t('kb.codePlaceholder')} style={{ minWidth: 160 }} />
        <Input value={String(pForm.name || '')} onChange={(v: any) => setPForm({ ...pForm, name: v })} placeholder={t('kb.namePlaceholder')} style={{ minWidth: 180 }} />
        <Select value={String(pForm.pack_type)} onChange={(v: any) => setPForm({ ...pForm, pack_type: v })} options={packTypeOptions} style={{ minWidth: 180 }} />
        <Button onClick={() => void createPackage()}>{t('kb.createPackage')}</Button>
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8 }}>
        <span style={{ fontSize: 12, color: '#889' }}>{t('kb.entriesHint')}</span>
        {isSuper && <Button size="small" disabled={rebuilding} onClick={() => void rebuildIndex()}>{rebuilding ? t('kb.rebuilding') : t('kb.rebuildIndex')}</Button>}
      </div>

      {/* ===== 知识包列表 ===== */}
      <Table rowKey="id" size="small" data={pkgs} style={{ marginTop: 8 }}
        columns={[
          { colKey: 'id', title: 'ID', width: 60 },
          { colKey: 'code', title: t('kb.codePlaceholder'), width: 120 },
          { colKey: 'name', title: t('kb.namePlaceholder') },
          { colKey: 'pack_type', title: '类型', width: 120 },
          { colKey: 'enabled', title: t('kb.enablePack') ? '启用' : '启用', width: 80, cell: ({ row }: any) =>
            <Switch size="small" value={row.enabled !== 0} onChange={async () => { await togglePackage(row) }} /> },
          { colKey: 'share_cross_dept', title: t('kb.shareOn'), width: 120, cell: ({ row }: any) =>
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

      {/* ===== 条目对话框 ===== */}
      <Dialog visible={selectedPkg !== null} onClose={() => setSelectedPkg(null)}
        header={selectedPkg ? `${tpl('kb.viewEntries', { count: entriesMap[selectedPkg] || 0 })} #${selectedPkg}` : ''} width={900} footer={false}>
        <div style={rowMt}>
          <Input value={String(eForm.source_text || '')} onChange={(v: any) => setEForm({ ...eForm, source_text: v })} placeholder={t('kb.sourcePlaceholder')} style={{ flex: 1 }} />
          <Select value={String(eForm.layer)} onChange={(v: any) => setEForm({ ...eForm, layer: Number(v) })}
            options={[1, 2, 3, 4].map((n) => ({ label: t('kb.layer' + n), value: n }))} style={{ width: 120 }} />
          <Input value={String(eForm.target_lang || '')} onChange={(v: any) => setEForm({ ...eForm, target_lang: v })} placeholder={t('kb.targetLangPlaceholder')} style={{ width: 120 }} />
          <Input value={String(eForm.target_text || '')} onChange={(v: any) => setEForm({ ...eForm, target_text: v })} placeholder={t('kb.translationPlaceholder')} style={{ flex: 1 }} />
          <Button theme="primary" onClick={() => selectedPkg != null && void addEntry(selectedPkg)}>{t('kb.add')}</Button>
        </div>
        <details style={{ marginTop: 8 }}>
          <summary>{t('kb.bulkImportSummary')}</summary>
          <Textarea autosize={{ minRows: 4 }} value={bulkText} onChange={(v: any) => setBulkText(v)} placeholder={t('kb.bulkPlaceholder')} />
          <Button style={{ marginTop: 6 }} onClick={() => selectedPkg != null && void bulkImport(selectedPkg)}>{t('kb.bulkImport')}</Button>
          {bulkTextMsg && <span style={{ marginLeft: 10, fontSize: 12, color: '#1a7f37' }}>{bulkTextMsg}</span>}
        </details>
        <Table rowKey="id" size="small" maxHeight={360} data={entries} style={{ marginTop: 8 }}
          columns={[
            { colKey: 'id', title: 'ID', width: 70 },
            { colKey: 'layer', title: t('kb.colLayer'), width: 60, cell: ({ row }: any) => `L${row.layer}` },
            { colKey: 'source_text', title: t('kb.colSource'), ellipsis: true },
            { colKey: 'target_lang', title: t('kb.colLang'), width: 90 },
            { colKey: 'target_text', title: t('kb.colTranslation'), ellipsis: true },
            { colKey: 'op', title: '', width: 80, cell: ({ row }: any) =>
              <Popconfirm content={t('kb.delete')} onConfirm={async () => { await removeEntry(row) }}>
                <Button size="small" variant="text" theme="danger">{t('kb.delete')}</Button>
              </Popconfirm> },
          ] as never} />
      </Dialog>

      {/* ===== 语言文化规范 / 安全句 ===== */}
      <Panel title={t('kb.safetyTitle')}>
        <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{t('kb.safetyHint')}</div>
        <div style={rowMt}>
          <Select value={safetyPkgId} onChange={(v: any) => setSafetyPkgId(Number(v))}
            options={localePackages.map((p: Any) => ({ label: `[${p.pack_type}] ${p.name}`, value: Number(p.id) }))} style={{ minWidth: 200 }} placeholder={t('kb.selectPkg')} />
          <Select value={safetyStatusFilter} onChange={(v: any) => setSafetyStatusFilter(String(v))}
            options={[{ label: t('kb.allStatus'), value: '' }, { label: t('kb.pending'), value: 'pending' }, { label: t('kb.approved'), value: 'approved' }, { label: t('kb.rejected'), value: 'rejected' }]} style={{ width: 140 }} />
          <span style={{ fontSize: 12, color: '#889' }}>{t('kb.safetyScopeHint')}</span>
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
    </>
  )
}

// ==================== 模型配置（Vue Models.vue） ====================
// 主模型路由、多供应商降级链、分阶段模型、翻译策略参数
export function ModelsP() {
  const [, t, tpl] = useT()
  const { activeTenantId } = useAdmin()
  // 主模型配置与多供应商路由
  const [mForm, setMForm] = useState<Any>({ api_base: '', api_key: '', model: '' })
  const [routeForm, setRouteForm] = useState<Any[]>([])
  const [routePreset, setRoutePreset] = useState('')
  // 翻译策略参数
  const [pForm2, setPForm2] = useState<Any>({ high_sim: 0.9, med_sim: 0.75, evals_pass_threshold: 75, cross_dept_fallback: true, data_feedback_opt_out: false })
  // 分阶段模型配置
  const [stForm, setStForm] = useState<Any>({})
  // ★ LLM 密钥（翻译/工单任务 + KB 向量重建，仅超管可配置）
  // eForm：Embedding 密钥录入表单（api_key 录入后提交即清空，避免明文残留于前端状态）
  const [eForm, setEForm] = useState<Any>({ api_key: '', api_base: '' })
  // keyState：从后端 GET /api/admin/models 回填的密钥状态，用于面板展示「已配置/未配置」与掩码
  //   translation：在线翻译/工单任务密钥是否已真实配置
  //   embedding / embeddingMasked / embeddingBase：Embedding 密钥状态、掩码、网关地址
  const [keyState, setKeyState] = useState<Any>({ translation: false, embedding: false, embeddingMasked: '', embeddingBase: '' })

  // 供应商预设：api_base 与默认模型
  const providerPresets: Record<string, { api_base: string; model: string }> = {
    openai: { api_base: 'https://api.openai.com/v1', model: 'gpt-4o-mini' },
    gemini: { api_base: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-1.5-flash' },
    deepseek: { api_base: 'https://api.deepseek.com/v1', model: 'deepseek-chat' },
    siliconflow: { api_base: 'https://api.siliconflow.cn/v1', model: 'tencent/Hunyuan-MT-7B' },
    zhipu: { api_base: 'https://open.bigmodel.cn/api/paas/v4', model: 'glm-4-flash' },
  }
  // 分阶段模型卡片定义
  const stageCards = [
    { key: 'ai_initial', title: t('models.s5Initial'), hint: t('models.s5InitialHint') },
    { key: 'kb_embed', title: t('models.s5Embed'), hint: t('models.s5EmbedHint') },
    { key: 'initial_evals', title: t('models.s5InitialEvals'), hint: t('models.s5InitialEvalsHint') },
    { key: 'review', title: t('models.s5Review'), hint: t('models.s5ReviewHint') },
    { key: 'review_evals', title: t('models.s5ReviewEvals'), hint: t('models.s5ReviewEvalsHint') },
  ]

  // 加载主模型与路由配置
  const loadModels = useCallback(async () => {
    const r = await adminModels()
    if (r.success) {
      const d = r as unknown as { model?: Any; embedding?: Any; routes?: Any[] }
      setMForm(d.model || {})
      setRouteForm(d.routes || [])
      // Embedding 表单仅回填网关地址（api_base），密钥本身不入表单（避免明文残留）；api_key 留空待录入
      setEForm({ api_key: '', api_base: (d.embedding?.api_base as string) || '' })
      // 回填密钥状态：translation 取 model.set（后端已区分占位 Key），embedding 取 embedding.set 与掩码
      setKeyState({ translation: !!(d.model && d.model.set), embedding: !!(d.embedding && d.embedding.set), embeddingMasked: (d.embedding?.masked as string) || '', embeddingBase: (d.embedding?.api_base as string) || '' })
    }
  }, [])
  // 加载翻译策略参数
  const loadPolicy = useCallback(async () => {
    const r = await adminPolicy()
    if (r.success) setPForm2((r as unknown as { policy?: Any }).policy || {})
  }, [])
  // 加载分阶段模型配置并补齐 preset 字段
  const loadStages = useCallback(async () => {
    const r = await stageModels()
    if (r.success) {
      const st = (r as unknown as { stages?: Record<string, Any> }).stages || {}
      const nf: Any = {}
      for (const c of stageCards) {
        const s = st[c.key] || {}
        nf[c.key] = { preset: '', provider: c.key, api_base: s.api_base || '', api_key: s.api_key || '', model: s.model || '' }
      }
      setStForm(nf)
    }
  }, [])
  // 组合加载；租户切换时刷新
  const loadAll = useCallback(async () => { await loadPolicy(); await loadModels(); await loadStages() }, [loadPolicy, loadModels, loadStages])
  useEffect(() => { void loadAll() }, [activeTenantId, loadAll])

  // 根据预设向路由链追加一项供应商
  function applyRoutePreset() {
    const p = providerPresets[routePreset]
    if (p) { setRouteForm([...routeForm, { provider: routePreset, api_base: p.api_base, api_key: '', model: p.model, weight: 0 }]); setRoutePreset('') }
  }
  // 根据预设填充分阶段模型表单（kb_embed 默认使用 embedding-2）
  function applyStagePreset(key: string) {
    const p = providerPresets[stForm[key]?.preset]
    if (p) setStForm({ ...stForm, [key]: { ...stForm[key], api_base: p.api_base, model: key === 'kb_embed' ? 'embedding-2' : p.model } })
  }
  const stActive = (key: string) => !!(stForm[key]?.api_base && stForm[key]?.model)
  const stageHint = stageCards.filter((c) => stActive(c.key)).length

  // 保存主模型与路由链
  async function saveModels() {
    const r = await adminModelsSave({ ...mForm, routes: routeForm } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedModels')); await loadModels()
  }
  // 保存模型路由链（过滤未填写的项）
  async function saveRoutes() {
    const valid = routeForm.filter((rt: Any) => rt.api_base && rt.model)
    const r = await adminModelsSave({ routes: valid } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedRoutes')); await loadModels()
  }
  // 保存分阶段模型配置
  async function saveStages() {
    const payload: Record<string, Any> = {}
    for (const c of stageCards) {
      const { preset, ...rest } = stForm[c.key] || {}
      void preset
      payload[c.key] = rest
    }
    const r = await stageModelsSave(payload as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedStages')); await loadStages()
  }
  // 保存翻译策略参数
  async function savePolicy() {
    const cross = !!pForm2.cross_dept_fallback
    const fbOut = !!pForm2.data_feedback_opt_out
    const policy = { high_sim: Number(pForm2.high_sim), med_sim: Number(pForm2.med_sim), evals_pass_threshold: Number(pForm2.evals_pass_threshold) }
    const r = await adminPolicySave(policy as never, cross, fbOut)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedPolicy'))
  }
  // ★ 保存 Embedding 密钥（KB 向量重建用）：仅提交 embed_api_key / embed_api_base，
  //   后端以密文落库并热同步；提交后重新拉取以刷新「已配置」状态与掩码展示。
  async function saveEmbed() {
    const r = await adminModelsSave({ embed_api_key: eForm.api_key, embed_api_base: eForm.api_base } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.savedEmbed')); await loadModels()
  }
  // ★ 清除 Embedding 密钥：向后端发送 clear_keys:["embedding"]，由后端置空配置并刷新状态。
  async function clearEmbed() {
    const r = await adminModelsSave({ clear_keys: ['embedding'] } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.clearedEmbed')); await loadModels()
  }
  // ★ 清除翻译/工单任务密钥：向后端发送 clear_keys:["translation"]，恢复占位 Key。
  async function clearTrans() {
    const r = await adminModelsSave({ clear_keys: ['translation'] } as never)
    if (!r.success) { MessagePlugin.error(r.message); return }
    MessagePlugin.success(t('models.clearedTrans')); await loadModels()
  }

  // 当前生效主模型（优先取路由链中 weight>0 的项）
  const mainModel = (routeForm.find((r: Any) => Number(r.weight) > 0)?.model) || mForm.model || '—'

  return (
    <>
      {/* 页面标题 */}
      <h2 style={{ margin: '4px 0 12px' }}>{t('models.title')}</h2>

      {/* ===== 模型路由策略（全局主路由 + 多供应商降级链） ===== */}
      <Panel title={t('models.routingTitle')}>
        <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{t('models.onlineHint')}</div>
        <div style={rowMt}>
          <Select value={routePreset} onChange={(v: any) => setRoutePreset(String(v))} placeholder={t('models.presetPlaceholder')} style={{ width: 220 }} clearable
            options={[{ label: 'OpenAI (ChatGPT)', value: 'openai' }, { label: 'Google Gemini', value: 'gemini' }, { label: 'DeepSeek', value: 'deepseek' }, { label: 'SiliconFlow', value: 'siliconflow' }, { label: 'Zhipu GLM', value: 'zhipu' }]} />
        </div>
        <Field label={t('models.apiBase')}><Input value={String(mForm.api_base ?? '')} onChange={(v: any) => setMForm({ ...mForm, api_base: v })} placeholder={t('models.apiBasePlaceholder')} /></Field>
        <Field label={t('models.apiKey')}><Input type="password" value={String(mForm.api_key ?? '')} onChange={(v: any) => setMForm({ ...mForm, api_key: v })} placeholder={t('models.apiKeyPlaceholder')} /></Field>
        <Field label={t('models.modelName')}><Input value={String(mForm.model ?? '')} onChange={(v: any) => setMForm({ ...mForm, model: v })} placeholder={t('models.modelNamePlaceholder')} /></Field>

        {routeForm.map((r, i) => (
          <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'center', marginTop: 8, flexWrap: 'wrap' }}>
            <Input value={String(r.provider || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], provider: v }; setRouteForm(n) }} placeholder={t('models.providerPlaceholder')} style={{ width: 150 }} />
            <Input value={String(r.api_base || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], api_base: v }; setRouteForm(n) }} placeholder={t('models.apiBasePlaceholder')} style={{ flex: 1 }} />
            <Input value={String(r.api_key || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], api_key: v }; setRouteForm(n) }} placeholder={t('models.apiKeyPlaceholder')} style={{ flex: 1 }} />
            <Input value={String(r.model || '')} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], model: v }; setRouteForm(n) }} placeholder={t('models.modelNamePlaceholder')} style={{ flex: 1 }} />
            <Input type="number" value={num(r.weight ?? 0)} onChange={(v: any) => { const n = [...routeForm]; n[i] = { ...n[i], weight: Number(v) }; setRouteForm(n) }} placeholder={t('models.weightPlaceholder')} style={{ width: 90 }} />
            <Button size="small" theme="danger" variant="text" onClick={() => setRouteForm(routeForm.filter((_, j) => j !== i))}>{t('models.delete')}</Button>
          </div>
        ))}

        <div style={rowMt}>
          <Button onClick={() => void saveModels()}>{t('models.saveModel')}</Button>
          <Button onClick={() => setRouteForm([...routeForm, { provider: '', api_base: '', api_key: '', model: '', weight: 0 }])}>{t('models.addRoute')}</Button>
          <Button theme="success" onClick={() => void saveRoutes()}>{t('models.saveRoutes')}</Button>
        </div>
        <p style={{ fontSize: 12, color: '#667', margin: '8px 0 0' }}>
          {routeForm.length ? tpl('models.routesActive', { count: routeForm.length, main: mainModel }) : t('models.routesNone')}
        </p>
      </Panel>

      {/* ===== LLM 密钥（翻译/工单任务 + KB 向量重建，仅超管，2026-08-27 由独立接口并入本 tab） ===== */}
      <Panel title={t('models.llmKeyTitle')}>
        <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{t('models.llmKeyHint')}</div>
        {/* 在线翻译/工单任务密钥状态：复用本 tab 顶部「在线模型」api_key 字段，这里只展示是否已配置并支持一键清除 */}
        <div style={rowMt}>
          <span style={{ fontSize: 13 }}>{t('models.translationKeyLabel')}：{keyState.translation ? `✓ ${t('models.configured')}` : `✗ ${t('models.notConfigured')}`}</span>
          {keyState.translation && <Button size="small" theme="danger" variant="outline" onClick={() => void clearTrans()}>{t('models.clearTranslation')}</Button>}
        </div>
        {/* Embedding 密钥：知识库向量重建专用，独立于翻译 Key，单独录入/保存/清除 */}
        <Field label={t('models.embedApiKey')}><Input type="password" value={String(eForm.api_key ?? '')} onChange={(v: any) => setEForm({ ...eForm, api_key: v })} placeholder={t('models.embedApiKeyPlaceholder')} /></Field>
        <Field label={t('models.embedApiBase')}><Input value={String(eForm.api_base ?? '')} onChange={(v: any) => setEForm({ ...eForm, api_base: v })} placeholder={t('models.embedApiBasePlaceholder')} /></Field>
        <div style={rowMt}>
          <Button onClick={() => void saveEmbed()}>{t('models.saveEmbed')}</Button>
          {/* 已配置时才显示「清除」按钮与掩码，避免无意义操作 */}
          {keyState.embedding && <Button size="small" theme="danger" variant="outline" onClick={() => void clearEmbed()}>{t('models.clearEmbed')}</Button>}
          {keyState.embedding && <span style={{ fontSize: 12, color: '#1a7f37' }}>✓ {keyState.embeddingMasked}</span>}
        </div>
      </Panel>

      {/* ===== 分阶段模型配置 ===== */}
      {stageCards.map((st) => (
        <Panel key={st.key} title={st.title}>
          <div style={{ fontSize: 12, color: '#667', marginBottom: 8 }}>{st.hint}</div>
          <div style={rowMt}>
            <Select value={stForm[st.key]?.preset || ''} onChange={(v: any) => { setStForm({ ...stForm, [st.key]: { ...stForm[st.key], preset: v } }); applyStagePreset(st.key) }} placeholder={t('models.presetPlaceholder')} style={{ width: 220 }} clearable
              options={[{ label: 'OpenAI (ChatGPT)', value: 'openai' }, { label: 'Google Gemini', value: 'gemini' }, { label: 'DeepSeek', value: 'deepseek' }, { label: 'SiliconFlow', value: 'siliconflow' }, { label: 'Zhipu GLM', value: 'zhipu' }]} />
            {stActive(st.key) && <span style={{ fontSize: 12, color: '#1a7f37' }}>✓ {t('models.stageConfigured' as never)}</span>}
          </div>
          <Field label={t('models.apiBase')}><Input value={String(stForm[st.key]?.api_base ?? '')} onChange={(v: any) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], api_base: v } })} placeholder={t('models.stageApiBasePlaceholder' as never)} /></Field>
          <Field label={t('models.apiKey')}><Input type="password" value={String(stForm[st.key]?.api_key ?? '')} onChange={(v: any) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], api_key: v } })} placeholder={t('models.stageApiKeyPlaceholder' as never)} /></Field>
          <Field label={t('models.modelName')}><Input value={String(stForm[st.key]?.model ?? '')} onChange={(v: any) => setStForm({ ...stForm, [st.key]: { ...stForm[st.key], model: v } })} placeholder={t('models.stageModelPlaceholder' as never)} /></Field>
        </Panel>
      ))}
      <div style={rowMt}>
        <Button theme="success" onClick={() => void saveStages()}>{t('models.saveStages')}</Button>
        <span style={{ fontSize: 12, color: '#667' }}>{stageHint ? tpl('models.stageActive', { count: stageHint }) : t('models.stageNone')}</span>
      </div>

      {/* ===== 翻译策略参数 ===== */}
      <Panel title={t('models.policyTitle')}>
        <Field label={t('models.highSim')}><Input type="number" value={num(pForm2.high_sim ?? 0)} onChange={(v: any) => setPForm2({ ...pForm2, high_sim: v })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.medSim')}><Input type="number" value={num(pForm2.med_sim ?? 0)} onChange={(v: any) => setPForm2({ ...pForm2, med_sim: v })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.evalsThreshold')}><Input type="number" value={num(pForm2.evals_pass_threshold ?? 0)} onChange={(v: any) => setPForm2({ ...pForm2, evals_pass_threshold: v })} style={{ maxWidth: 200 }} /></Field>
        <Field label={t('models.crossDeptFallback')}>
          <Select value={!!pForm2.cross_dept_fallback} onChange={(v: any) => setPForm2({ ...pForm2, cross_dept_fallback: v })}
            options={[{ label: t('models.crossOn'), value: true }, { label: t('models.crossOff'), value: false }]} style={{ width: 220 }} />
        </Field>
        <Field label={t('models.feedbackOptOut')}>
          <Select value={!!pForm2.data_feedback_opt_out} onChange={(v: any) => setPForm2({ ...pForm2, data_feedback_opt_out: v })}
            options={[{ label: t('models.fbOff'), value: true }, { label: t('models.fbOn'), value: false }]} style={{ width: 260 }} />
        </Field>
        <Button theme="primary" style={{ marginTop: 8 }} onClick={() => void savePolicy()}>{t('models.savePolicy')}</Button>
      </Panel>
    </>
  )
}

// ==================== 流程引擎（Vue Workflow.vue） ====================
// 翻译流程步骤开关配置与模型评估记录
export function WorkflowP() {
  const [, t] = useT()
  const { activeTenantId } = useAdmin()
  const [steps, setSteps] = useState<Any[]>([])
  const [evals, setEvals] = useState<Any[]>([])
  // 加载流程配置与评估记录
  const loadAll = useCallback(async () => {
    try { const f = await flowConfig(); if (f.success) setSteps((f as unknown as { steps?: Any[] }).steps || []) } catch {}
    try { const e = await apiEvalsList(); if (e.success) setEvals((e as unknown as { records?: Any[] }).records || []) } catch {}
  }, [])
  // 租户切换时刷新
  useEffect(() => { void loadAll() }, [activeTenantId, loadAll])

  return (
    <>
      {/* 页面标题与说明 */}
      <h2 style={{ margin: '4px 0 8px' }}>{t('workflow.title')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('workflow.hint')}</p>
      {/* 流程步骤开关列表 */}
      {steps.map((s, i) => (
        <div key={s.key} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0' }}>
          <Switch value={!!s.enable} onChange={(v: any) => setSteps(steps.map((x, j) => (j === i ? { ...x, enable: !!v } : x)))} />
          <span style={{ fontSize: 14 }}>{s.name}</span>
          <code style={{ fontSize: 12, color: '#889' }}>{s.key}</code>
        </div>
      ))}
      <Button theme="primary" style={{ marginTop: 8 }} onClick={async () => toastResp(await flowSave(steps as never), t('workflow.savedFlow'))}>{t('workflow.saveFlow')}</Button>

      {/* 模型评估记录 */}
      <h2 style={{ margin: '32px 0 8px' }}>{t('workflow.evalsTitle')}</h2>
      <Button variant="outline" style={{ marginBottom: 8 }} onClick={() => void loadAll()}>{t('workflow.refresh')}</Button>
      <Table rowKey="id" size="small" maxHeight={400} data={evals}
        columns={[
          { colKey: 'id', title: t('workflow.colId'), width: 70 },
          { colKey: 'task_type', title: t('workflow.colTask'), width: 100 },
          { colKey: 'model', title: t('workflow.colLang'), width: 120 },
          { colKey: 'total', title: t('workflow.colScore'), width: 80, cell: ({ row }: any) => Number(row.total).toFixed(1) },
          { colKey: 'status', title: t('workflow.colStatus'), width: 90 },
          { colKey: 'created_at', title: t('workflow.colTime'), width: 160, cell: ({ row }: any) => fmtTime(row.created_at as string) },
          { colKey: 'output_text', title: t('workflow.colOutput'), ellipsis: true },
        ] as never} />
    </>
  )
}

// ==================== 反馈 / 审批台 / TM 审核（Vue Tickets.vue / Feedback.vue） ====================
// 用户反馈、审批工单、TM 记忆审核三合一工作台
export function TicketsP() {
  const [, t, tpl] = useT()
  const { isSuper, activeTenantId, consumeFeedback } = useAdmin()
  // 反馈列表与详情
  const [feedbacks, setFeedbacks] = useState<Any[]>([])
  const [statusFilter, setStatusFilter] = useState('')
  const [selected, setSelected] = useState<Any | null>(null)
  const [replyDraft, setReplyDraft] = useState('')
  // 普通用户提交反馈
  const [newContent, setNewContent] = useState('')
  const [submitting, setSubmitting] = useState(false)
  // 超管 TM 审核子视图
  const [fbTab, setFbTab] = useState<'feedback' | 'review'>('feedback')
  const [reviews, setReviews] = useState<Any[]>([])
  const [rvFilter, setRvFilter] = useState('pending')
  // 审批台
  const [approvalTickets, setApprovalTickets] = useState<Any[]>([])
  const [approveDlg, setApproveDlg] = useState<null | { row: Any; text: string; reason: string; suggestion: string; action: 'approve' | 'reject' }>(null)

  // 加载反馈列表
  const loadFeedbacks = useCallback(async () => {
    const r = await feedbackList(statusFilter)
    if (r.success) setFeedbacks((r as unknown as { feedbacks?: Any[] }).feedbacks || [])
  }, [statusFilter])
  // 加载 TM 记忆审核候选
  const loadReviews = useCallback(async () => {
    const r = await listTmReview(rvFilter)
    if (r.success) setReviews((r as unknown as { candidates?: Any[] }).candidates || [])
  }, [rvFilter])
  // 加载审批工单
  const loadApproval = useCallback(async () => {
    const r = await approveList()
    if (r.success) setApprovalTickets((r as unknown as { tickets?: Any[] }).tickets || [])
  }, [])

  // 租户切换时刷新反馈与审批
  useEffect(() => { void loadFeedbacks(); void loadApproval() }, [activeTenantId, loadFeedbacks, loadApproval])
  // 切换到 TM 审核页时加载
  useEffect(() => { if (isSuper && fbTab === 'review') void loadReviews() }, [isSuper, fbTab, loadReviews])
  // 深链：消息中心点击 → 自动打开对应反馈详情
  useEffect(() => { const fid = consumeFeedback(); if (fid) void jumpFeedback(fid); /* eslint-disable-next-line */ }, [consumeFeedback])

  // 打开反馈详情
  function openDetail(f: Any) { setSelected(f); setReplyDraft('') }
  // 消息中心跳转：切到反馈列表并定位指定反馈
  async function jumpFeedback(fid: number) {
    setFbTab('feedback'); setStatusFilter('')
    await loadFeedbacks()
    const f = feedbacks.find((x) => x.id === fid)
    if (f) openDetail(f)
  }
  // TM 来源标签本地化
  function srcLabel(src: string) {
    return src === 'bitext' ? '双语文本' : src === 'tmx' ? 'TMX 导入' : src === 'feedback' ? '用户反馈修正' : '次数达标'
  }
  // 解析反馈上下文中的多语言译文
  function ctxTranslations(f: Any): Record<string, string> {
    try { return JSON.parse(f.translations_json || '{}') } catch { return {} }
  }
  // 格式化 ISO 时间为本地字符串
  function fmtAt(iso: string): string {
    if (!iso) return ''
    const d = new Date(iso)
    return isNaN(+d) ? iso : d.toLocaleString()
  }
  // 普通用户提交新反馈
  async function submitFeedback() {
    const content = newContent.trim(); if (!content) return
    setSubmitting(true)
    try {
      const r = await createFeedback({ target_type: 'text', content })
      if (!r.success) { MessagePlugin.error(r.message); return }
      setNewContent(''); await loadFeedbacks()
    } finally { setSubmitting(false) }
  }
  // 回复当前反馈
  async function doReply() {
    if (!selected || !replyDraft.trim()) return
    const r = await feedbackReply(Number(selected.id), replyDraft.trim())
    if (!r.success) { MessagePlugin.error(r.message); return }
    setSelected({ ...selected, replies: (r as unknown as { replies?: Any[] }).replies || [] })
    setReplyDraft('')
  }
  // 超管将反馈标记为已解决
  async function doResolve() {
    if (!selected) return
    if (!window.confirm(t('fb.resolveConfirm'))) return
    const r = await resolveFeedback(Number(selected.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    setSelected({ ...selected, status: 'resolved', handled_at: new Date().toISOString() })
    await loadFeedbacks()
  }
  // 批准 TM 记忆候选
  async function doApproveReview(c: Any) {
    const r = await approveTmReview(Number(c.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadReviews()
  }
  // 拒绝 TM 记忆候选
  async function doRejectReview(c: Any) {
    const r = await rejectTmReview(Number(c.id))
    if (!r.success) { MessagePlugin.error(r.message); return }
    await loadReviews()
  }
  // 审批工单：approve/reject，可附带原因、建议、终稿修订
  async function doApprove(tk: Any, action: 'approve' | 'reject') {
    const r = await approveAction(Number(tk.id), action, tk._reason || '', tk._suggestion || '', '')
    if (!r.success) { MessagePlugin.error(r.message); return }
    tk._reason = ''; tk._suggestion = ''
    await loadApproval()
  }

  return (
    <>
      {/* 页面标题与角色提示 */}
      <h2 style={{ margin: '4px 0 4px' }}>{t('fb.workbench')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{isSuper ? t('fb.superHint') : t('fb.userHint')}</p>

      {/* 超管子视图切换：反馈 / TM 审核 */}
      {isSuper && (
        <div style={{ marginBottom: 12 }}>
          <Button size="small" variant={fbTab === 'feedback' ? 'outline' : 'text'} onClick={() => setFbTab('feedback')}>💬 {t('fb.tabFeedback')}</Button>
          <Button size="small" variant={fbTab === 'review' ? 'outline' : 'text'} onClick={() => setFbTab('review')}>📚 {t('fb.tabReview')}</Button>
        </div>
      )}

      {/* ===== TM 记忆审核子视图（超管） ===== */}
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

      {/* ===== 反馈详情视图 ===== */}
      {selected && (
        <div style={cardStyle}>
          <Button size="small" style={{ float: 'right' }} onClick={() => setSelected(null)}>← {t('fb.backToList')}</Button>
          {/* 反馈头部：编号、用户、状态 */}
          <h3 style={{ marginTop: 0 }}>
            #{selected.id} · {selected.user_name || ('#' + selected.user_id)}{' '}
            <Tag theme={selected.status === 'resolved' ? 'success' : 'warning'}>{selected.status === 'resolved' ? t('fb.statusResolved') : t('fb.statusOpen')}</Tag>
          </h3>
          <pre style={{ whiteSpace: 'pre-wrap', fontFamily: 'inherit', margin: 0 }}>{selected.content}</pre>

          {/* 反馈附带的上下文原文与译文 */}
          {selected.with_context && (
            <div style={{ background: '#fafafa', border: '1px dashed #ddd', borderRadius: 8, padding: '8px 10px', marginTop: 8, fontSize: 12.5 }}>
              <b>{t('fb.ctxAttached')}</b>
              {selected.source_text && <pre style={{ whiteSpace: 'pre-wrap', margin: '4px 0' }}>{selected.source_text}</pre>}
              {Object.entries(ctxTranslations(selected)).map(([k, v]) => (
                <div key={k} style={{ marginTop: 4 }}><b>[{k}]</b> {String(v)}</div>
              ))}
            </div>
          )}

          {/* 回复列表 */}
          {selected.replies && selected.replies.length ? (
            <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 6 }}>
              {selected.replies.map((r: Any, i: number) => (
                <div key={i} style={{ background: r.role === 'admin' ? '#e8f0fe' : '#f5f6f8', borderRadius: 8, padding: '6px 10px', fontSize: 13 }}>
                  <div style={{ fontSize: 11, color: '#888', marginBottom: 2 }}>{r.name} · {r.role === 'admin' ? '超管' : '用户'} · {fmtAt(r.at)}</div>
                  <div style={{ whiteSpace: 'pre-wrap' }}>{r.content}</div>
                </div>
              ))}
            </div>
          ) : <div style={{ fontSize: 12, color: '#889', marginTop: 8 }}>{t('fb.noReplies')}</div>}

          {/* 未解决反馈：回复输入框 */}
          {selected.status === 'open' ? (
            <div style={rowMt}>
              <Input value={replyDraft} onChange={(v: any) => setReplyDraft(v)} placeholder={t('fb.replyPlaceholder')} style={{ flex: 1 }} />
              <Button disabled={!replyDraft.trim()} onClick={() => void doReply()}>↩ {t('fb.reply')}</Button>
              {isSuper && <Button theme="success" onClick={() => void doResolve()}>✔ {t('fb.complete')}</Button>}
            </div>
          ) : <div style={{ fontSize: 12, color: '#1a7f37', marginTop: 8 }}>✅ {t('fb.archivedHint')}</div>}
        </div>
      )}

      {/* ===== 反馈列表视图 ===== */}
      {!selected && fbTab !== 'review' && (
        <>
          {/* 普通用户：提交意见反馈表单 */}
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

      {/* ===== 审批台 ===== */}
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

      {/* 审批对话框（含终稿修订） */}
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
                  placeholder="留空=按系统译文批准；填写后将与载荷中对应语言的现有译文匹配并覆盖" />
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
              <Button theme="danger" onClick={() => setApproveDlg({ ...approveDlg, action: 'reject' })}>{t('tickets.reject')}</Button>
            </div>
          </div>
        )}
      </Dialog>
    </>
  )
}
