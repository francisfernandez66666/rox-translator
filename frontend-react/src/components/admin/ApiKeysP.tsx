// ============================================================================
// components/admin/ApiKeysP.tsx — API Key + OpenAPI 文档面板
// 职责：API Key 创建/启停/轮换/限额/删除；超管可维护多语言 OpenAPI 在线文档
// 从 panels_c.tsx 拆分
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import {
  Button, Table, Input, Select, Space, Tag, Popconfirm, Textarea, MessagePlugin,
} from 'tdesign-react'
import { confirmDialog, promptText } from '@/components/uiDialogs'
import {
  apiKeys as apiApiKeys, apiKeyCreate, apiKeyStatus, apiKeyRotate, apiKeyDelete,
  apiKeyLimit, getOpenAPIDocs, saveOpenAPIDocs, previewOpenAPIDocs,
} from '@/api'
import { Panel, toastResp } from './parts'
import { maskKey } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'

type Any = Record<string, any>

/** API Key + OpenAPI 文档面板 */
export function ApiKeysP() {
  const ad = useAdmin()
  const [, t, tpl] = useT()
  const isSuper = ad.isSuper
  const [keys, setKeys] = useState<Any[]>([])
  const [newKey, setNewKey] = useState('')
  const [copied, setCopied] = useState(false)
  const [kForm, setKForm] = useState<Any>({ name: '', perms: 'translate', daily_call_limit: '' })
  const [docsCardOpen, setDocsCardOpen] = useState(false)
  const [docsLang, setDocsLang] = useState<'zh' | 'en'>('zh')
  const [docsMDZh, setDocsMDZh] = useState('')
  const [docsMDEn, setDocsMDEn] = useState('')
  const [docsSaving, setDocsSaving] = useState(false)
  const [docsDefaultBadge, setDocsDefaultBadge] = useState(false)
  const [docsLoaded, setDocsLoaded] = useState(false)
  const docsMD = docsLang === 'en' ? docsMDEn : docsMDZh
  const setDocsMD = (v: string) => { if (docsLang === 'en') setDocsMDEn(v); else setDocsMDZh(v) }

  const loadKeys = useCallback(async () => {
    const r: Any = await apiApiKeys()
    if (r.success) setKeys((r.keys as Any[]) || (r.api_keys as Any[]) || [])
  }, [])
  useEffect(() => {
    void loadKeys()
    void (async () => { const d: Any = await getOpenAPIDocs(); if (d.success) { setDocsMDZh(d.md_zh || ''); setDocsMDEn(d.md_en || ''); setDocsDefaultBadge(!!d.default_zh && !!d.default_en) } })()
  }, [loadKeys])

  async function copyNewKey() {
    try { await navigator.clipboard.writeText(newKey); setCopied(true); setTimeout(() => setCopied(false), 2000) }
    catch { void MessagePlugin.error(t('apikeys.copyFail')) }
  }
  async function createKey() {
    if (!kForm.name) { void MessagePlugin.warning(t('apikeys.nameRequired')); return }
    const r: Any = await apiKeyCreate({ name: String(kForm.name || ''), perms: String(kForm.perms || 'translate'), daily_call_limit: kForm.daily_call_limit === '' ? undefined : Number(kForm.daily_call_limit) })
    if (!r.success) { void MessagePlugin.error(r.message || ''); return }
    setNewKey(r.api_key || '')
    setKForm({ name: '', perms: 'translate', daily_call_limit: '' })
    await loadKeys()
  }
  async function toggleKey(k: Any) { await apiKeyStatus(Number(k.id), k.status === 'active' ? 'disabled' : 'active'); await loadKeys() }
  async function deleteKey(k: Any) {
    if (!(await confirmDialog({ body: t('apikeys.confirmDelete') }))) return
    await apiKeyDelete(Number(k.id)); await loadKeys()
  }
  async function rotateKey(k: Any) {
    if (!(await confirmDialog({ body: tpl('apikeys.confirmRotate', { name: k.name }) }))) return
    const r: Any = await apiKeyRotate(Number(k.id))
    if (!r.success) { void MessagePlugin.error(r.message || ''); return }
    setNewKey(r.api_key || '')
    await loadKeys()
  }
  async function setLimit(k: Any) {
    const input = await promptText({ body: tpl('apikeys.limitPrompt', { name: k.name, cur: k.daily_call_limit || 0 }) })
    if (input === null) return
    const n = Number(input)
    if (!Number.isFinite(n) || n < 0) { void MessagePlugin.warning(t('apikeys.limitInvalid')); return }
    const r: Any = await apiKeyLimit(Number(k.id), Math.floor(n))
    if (!r.success) { void MessagePlugin.error(r.message || ''); return }
    await loadKeys()
  }

  async function loadDocs() {
    if (docsLoaded) return
    try {
      const r: Any = await getOpenAPIDocs()
      if (r.success) { setDocsMDZh(r.md_zh || ''); setDocsMDEn(r.md_en || ''); setDocsDefaultBadge(!!r.default_zh && !!r.default_en); setDocsLoaded(true) }
    } catch { /* 非超管或网络失败：静默 */ }
  }
  useEffect(() => { if (isSuper && docsCardOpen) void loadDocs() }, [isSuper, docsCardOpen])
  async function refreshDocsState() { setDocsLoaded(false); await loadDocs() }
  async function saveDocs() {
    if (!(await confirmDialog({ body: t('docsEdit.confirmSave') }))) return
    setDocsSaving(true)
    try {
      const r: Any = await saveOpenAPIDocs({ lang: docsLang, md: docsMD })
      if (!r.success) { void MessagePlugin.error(r.message || ''); return }
      void MessagePlugin.success(t('docsEdit.saved'))
      await refreshDocsState()
    } finally { setDocsSaving(false) }
  }
  async function previewDocs() {
    try {
      const r: Any = await previewOpenAPIDocs({ lang: docsLang, md: docsMD })
      if (!r.success) { void MessagePlugin.error(r.message || ''); return }
      const w = window.open('', '_blank')
      if (w) { w.document.open(); w.document.write(r.html as string); w.document.close() }
    } catch (e) { void MessagePlugin.error(String(e)) }
  }
  function importDocs(e: any) {
    const file = e.target.files ? e.target.files[0] : null
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => setDocsMD(String(reader.result || ''))
    reader.readAsText(file)
    e.target.value = ''
  }
  function exportDocs() {
    const blob = new Blob([docsMD], { type: 'text/markdown;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob); a.download = 'openapi-docs.md'
    a.click(); URL.revokeObjectURL(a.href)
  }
  async function resetDocs() {
    if (!(await confirmDialog({ body: t('docsEdit.confirmReset') }))) return
    const r: Any = await saveOpenAPIDocs({ lang: docsLang, md: '' })
    if (!r.success) { void MessagePlugin.error(r.message || ''); return }
    setDocsMD('')
    await refreshDocsState()
    void MessagePlugin.success(t('docsEdit.resetDone'))
  }
  function openDocs() { window.open('/openapi/docs', '_blank') }

  function isKeyOverQuota(k: Any): boolean {
    if (!k.daily_call_limit || Number(k.daily_call_limit) <= 0) return false
    const today = new Date().toISOString().slice(0, 10)
    const used = k.calls_today_date === today ? Number(k.calls_today) : 0
    return used >= Number(k.daily_call_limit)
  }
  function fmtToday(k: Any): string {
    const limit = k.daily_call_limit && Number(k.daily_call_limit) > 0 ? k.daily_call_limit : '∞'
    const today = new Date().toISOString().slice(0, 10)
    const used = k.calls_today_date === today ? Number(k.calls_today) : 0
    return `${used}/${limit}`
  }

  return (
    <>
      <Panel title={t('apikeys.title')} extra={
        <Space size={8}>
          <Button variant="outline" onClick={openDocs}>📄 {t('apikeys.docs')}</Button>
          {isSuper && <Button onClick={() => setDocsCardOpen((v) => !v)}>{docsCardOpen ? '▲' : '▼'} {t('docsEdit.title')}</Button>}
        </Space>
      }>
        {!!newKey && (
          <div style={{ background: '#fff8e1', border: '1px solid #ffe082', borderRadius: 8, padding: 10, marginBottom: 10 }}>
            ⚠️ {t('apikeys.newKeyOnce')}：<b style={{ userSelect: 'all' }}>{newKey}</b>
            <Button size="small" style={{ marginLeft: 8 }} onClick={copyNewKey}>📋 {t('apikeys.copy')}</Button>
            {copied && <span style={{ fontSize: 12, color: '#667' }}> {t('apikeys.copied')}</span>}
          </div>
        )}
        <Space size={8} align="center">
          <Input value={String(kForm.name || '')} onChange={(v) => setKForm({ ...kForm, name: v })} placeholder={t('apikeys.keyName')} style={{ width: 180 }} />
          <Select value={String(kForm.perms || 'translate')} onChange={(v) => setKForm({ ...kForm, perms: v })} style={{ width: 140 }}
                  options={['translate', 'kb', 'all'].map((x) => ({ label: x, value: x }))} />
          <Input type="number" value={String(kForm.daily_call_limit || '')} onChange={(v) => setKForm({ ...kForm, daily_call_limit: v })} placeholder={t('apikeys.limitPlaceholder')} style={{ width: 130 }} />
          <Button onClick={createKey}>{t('apikeys.create')}</Button>
        </Space>

        <Table rowKey="id" size="small" data={keys} style={{ marginTop: 10 }}
               columns={[
                 { colKey: 'id', title: t('apikeys.colId'), width: 70 },
                 { colKey: 'key_prefix', title: t('apikeys.colPrefix'), width: 160, cell: ({ row }: any) => maskKey(`${row.key_prefix || ''}…`) },
                 { colKey: 'name', title: t('apikeys.colName') },
                 { colKey: 'perms', title: t('apikeys.colPerms'), width: 100 },
                 { colKey: 'status', title: t('apikeys.colStatus'), width: 90, cell: ({ row }: any) => <Tag theme={row.status === 'active' ? 'success' : 'default'}>{row.status}</Tag> },
                 { colKey: 'calls', title: t('apikeys.colCalls'), width: 110, cell: ({ row }: any) => <span style={{ color: isKeyOverQuota(row) ? '#c62828' : '' }}>{fmtToday(row)}</span> },
                 { colKey: 'op', title: t('apikeys.colActions'), width: 300, cell: ({ row }: any) => (
                   <Space size={4}>
                     <Button size="small" variant="text" onClick={() => toggleKey(row)}>{row.status === 'active' ? t('apikeys.disable') : t('apikeys.enable')}</Button>
                     <Button size="small" variant="text" onClick={() => rotateKey(row)}>{t('apikeys.rotate')}</Button>
                     <Button size="small" variant="text" onClick={() => setLimit(row)}>📐 {t('apikeys.setLimit')}</Button>
                     <Popconfirm content={t('apikeys.confirmDelete')} onConfirm={() => deleteKey(row)}>
                       <Button size="small" variant="text" theme="danger">{t('apikeys.delete')}</Button>
                     </Popconfirm>
                   </Space>
                 ) },
               ] as never} />
      </Panel>

      {isSuper && docsCardOpen && (
        <Panel title={t('docsEdit.title')}>
          <div style={{ fontSize: 13, color: '#667', marginBottom: 8 }}>{t('docsEdit.hint')}</div>
          <Space size={6} style={{ marginBottom: 8 }}>
            <Button size="small" theme={docsLang === 'zh' ? 'primary' : 'default'} onClick={() => setDocsLang('zh')}>{t('docsEdit.langZh')}</Button>
            <Button size="small" theme={docsLang === 'en' ? 'primary' : 'default'} onClick={() => setDocsLang('en')}>{t('docsEdit.langEn')}</Button>
          </Space>
          <Textarea autosize={{ minRows: 16 }} value={docsMD} onChange={(v) => setDocsMD(v as string)} placeholder={t('docsEdit.placeholder')}
                    style={{ width: '100%', fontFamily: 'SFMono-Regular, Consolas, monospace', fontSize: 13, lineHeight: 1.55, resize: 'vertical' }} />
          <Space size={8} style={{ marginTop: 8 }}>
            <Button theme="success" disabled={docsSaving || !docsMD.trim()} onClick={saveDocs}>💾 {docsSaving ? t('docsEdit.saving') : t('common.save')}</Button>
            <Button disabled={!docsMD.trim()} onClick={previewDocs}>👁 {t('docsEdit.preview')}</Button>
            <label style={{ cursor: 'pointer' }}>
              📂 {t('docsEdit.import')}
              <input type="file" accept=".md,.markdown,.txt" hidden onChange={importDocs} />
            </label>
            <Button onClick={exportDocs}>⬇️ {t('docsEdit.export')}</Button>
            <Button theme="danger" onClick={resetDocs}>↺ {t('docsEdit.reset')}</Button>
            {docsDefaultBadge && <span style={{ fontSize: 12, color: '#667' }}>{t('docsEdit.isDefault')}</span>}
          </Space>
        </Panel>
      )}
    </>
  )
}
