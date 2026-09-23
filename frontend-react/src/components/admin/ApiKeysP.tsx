// ============================================================================
// components/admin/ApiKeysP.tsx — API Key + OpenAPI 文档面板
// 职责：API Key 创建/启停/轮换/限额/删除；超管可维护多语言 OpenAPI 在线文档
// 从 panels_c.tsx 拆分
// 2026-09-18：按钮/提示位的 emoji 全部换成 ui/langcross <Icon> 自绘 SVG
//   （emoji 是设计稿图标占位，不属于文案；见 MI 常量统一处理行内基线）。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, DataTable, Link, StatusPill } from '@/ui/langcross/src'
import { confirmDialog, promptText } from '@/components/uiDialogs'
import { apiKeys as apiApiKeys, apiKeyCreate, apiKeyStatus, apiKeyRotate, apiKeyDelete, apiKeyLimit, getOpenAPIDocs, saveOpenAPIDocs, previewOpenAPIDocs, openAPIDocsUrl, type Any } from '@/api'
import { Panel } from './parts'
import { maskKey } from '@/lib/ui'
import { useAdmin } from '@/stores/admin'
import { useT } from '@/i18n'
import { Icon } from '@/ui/langcross/src'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'

// 行内图标基线对齐（16×16 SVG，跟随文案）
// verticalAlign:-3px 让 SVG 与中文按钮文字基线视觉居中；marginInlineEnd:4 补回原先
// emoji 自带的气隙。集中成一个常量，避免十几处按钮各写一遍 style 造成漂移。
const MI: React.CSSProperties = { verticalAlign: '-3px', marginInlineEnd: 4 }

/** API Key + OpenAPI 文档面板 */
export function ApiKeysP() {
  const ad = useAdmin()
  // ===== 面板状态：Key 列表、新建结果、限额编辑、开放文档卡片 =====
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

  // loadKeys 拉取开放 API Key 列表（兼容 keys / api_keys 两种出参字段）
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
    catch { toastError(t('apikeys.copyFail')) }
  }
  async function createKey() {
    if (!kForm.name) { void toastWarn(t('apikeys.nameRequired')); return }
    const r: Any = await apiKeyCreate({ name: String(kForm.name || ''), perms: String(kForm.perms || 'translate'), daily_call_limit: kForm.daily_call_limit === '' ? undefined : Number(kForm.daily_call_limit) })
    if (!r.success) { toastError(r.message || ''); return }
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
    if (!r.success) { toastError(r.message || ''); return }
    setNewKey(r.api_key || '')
    await loadKeys()
  }
  async function setLimit(k: Any) {
    const input = await promptText({ body: tpl('apikeys.limitPrompt', { name: k.name, cur: k.daily_call_limit || 0 }) })
    if (input === null) return
    const n = Number(input)
    if (!Number.isFinite(n) || n < 0) { void toastWarn(t('apikeys.limitInvalid')); return }
    const r: Any = await apiKeyLimit(Number(k.id), Math.floor(n))
    if (!r.success) { toastError(r.message || ''); return }
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
      if (!r.success) { toastError(r.message || ''); return }
      toastSuccess(t('docsEdit.saved'))
      await refreshDocsState()
    } finally { setDocsSaving(false) }
  }
  async function previewDocs() {
    try {
      const r: Any = await previewOpenAPIDocs({ lang: docsLang, md: docsMD })
      if (!r.success) { toastError(r.message || ''); return }
      // ★ E7：document.write 打开的后端 HTML 会继承当前页 origin（后端 XSS 可升格为同源），
      //   且部分 CSP/沙箱环境下直接失效。改为同源受限的 Blob URL 预览。
      const url = URL.createObjectURL(new Blob([r.html as string], { type: 'text/html;charset=utf-8' }))
      const w = window.open(url, '_blank')
      if (!w) toastError(t('docsEdit.popupBlocked'))
      setTimeout(() => URL.revokeObjectURL(url), 60000)
    } catch (e) { toastError(String(e)) }
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
    if (!r.success) { toastError(r.message || ''); return }
    setDocsMD('')
    await refreshDocsState()
    toastSuccess(t('docsEdit.resetDone'))
  }
  function openDocs() { window.open(openAPIDocsUrl(), '_blank') }

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
        <div style={{ display: 'flex', gap: 8 }}>
          <Button variant="secondary" onClick={openDocs}><Icon n="doc" style={MI} />{t('apikeys.docs')}</Button>
          {isSuper && <Button variant="secondary" onClick={() => setDocsCardOpen((v) => !v)}>{docsCardOpen ? '▲' : '▼'} {t('docsEdit.title')}</Button>}
          </div>
      }>
        {!!newKey && (
          <div style={{ background: 'var(--adm-warn-bg)', border: '1.2px solid var(--adm-warn-bd)', borderRadius: 8, padding: 10, marginBottom: 10 }}>
            <Icon n="alert" style={MI} />{t('apikeys.newKeyOnce')}：<b style={{ userSelect: 'all' }}>{newKey}</b>
            <Button size="sm" variant="secondary" style={{ marginInlineStart: 8 }} onClick={copyNewKey}><Icon n="clipboard" style={MI} />{t('apikeys.copy')}</Button>
            {copied && <span style={{ fontSize: 14, color: 'var(--adm-hint)' }}> {t('apikeys.copied')}</span>}
          </div>
        )}
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <input className="lc-input" value={String(kForm.name || '')} onChange={(e) => setKForm({ ...kForm, name: e.target.value })} placeholder={t('apikeys.keyName')} style={{ width: 180 }} />
          <select className="lc-select" value={String(kForm.perms || 'translate')} onChange={(e) => setKForm({ ...kForm, perms: e.target.value })} style={{ width: 140 }}>
            {['translate', 'kb', 'all'].map((x) => <option key={x} value={x}>{x}</option>)}
          </select>
          <input className="lc-input" type="number" value={String(kForm.daily_call_limit || '')} onChange={(e) => setKForm({ ...kForm, daily_call_limit: e.target.value })} placeholder={t('apikeys.limitPlaceholder')} style={{ width: 130 }} />
          <Button variant="primary" onClick={createKey}>{t('apikeys.create')}</Button>
          </div>

        <div style={{ marginTop: 10 }}>
        <DataTable<any> rowKey={(row) => String(row.id)} rows={keys}
               columns={[
                 { key: 'id', title: t('apikeys.colId'), width: 70 },
                 { key: 'key_prefix', title: t('apikeys.colPrefix'), width: 160, render: (row) => maskKey(`${row.key_prefix || ''}…`) },
                 { key: 'name', title: t('apikeys.colName') },
                 { key: 'perms', title: t('apikeys.colPerms'), width: 100 },
                 { key: 'status', title: t('apikeys.colStatus'), width: 90, render: (row) => <StatusPill tone={row.status === 'active' ? 'success' : 'idle'}>{row.status}</StatusPill> },
                 { key: 'calls', title: t('apikeys.colCalls'), width: 110, render: (row) => <span style={{ color: isKeyOverQuota(row) ? 'var(--lc-danger)' : undefined }}>{fmtToday(row)}</span> },
                 { key: 'op', title: t('apikeys.colActions'), width: 300, render: (row) => (
                   <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                     <Link onClick={() => toggleKey(row)}>{row.status === 'active' ? t('apikeys.disable') : t('apikeys.enable')}</Link>
                     <Link onClick={() => rotateKey(row)}>{t('apikeys.rotate')}</Link>
                     <Link onClick={() => setLimit(row)}><Icon n="gauge" style={MI} />{t('apikeys.setLimit')}</Link>
                     <Link tone="danger" onClick={async () => { if (!(await confirmDialog({ body: t('apikeys.confirmDelete') }))) return; deleteKey(row) }}>{t('apikeys.delete')}</Link>
          </div>
                 ) },
               ]}  />
          </div>
      </Panel>

      {isSuper && docsCardOpen && (
        <Panel title={t('docsEdit.title')}>
          <div style={{ fontSize: 15, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('docsEdit.hint')}</div>
          <div style={{ display: 'flex', gap: 6, marginBottom: 8 }}>
            <Button size="sm" variant={docsLang === 'zh' ? 'primary' : 'secondary'} onClick={() => setDocsLang('zh')}>{t('docsEdit.langZh')}</Button>
            <Button size="sm" variant={docsLang === 'en' ? 'primary' : 'secondary'} onClick={() => setDocsLang('en')}>{t('docsEdit.langEn')}</Button>
          </div>
          <textarea className="lc-textarea" rows={16} value={docsMD} onChange={(e) => setDocsMD(e.target.value)} placeholder={t('docsEdit.placeholder')}
                    style={{ width: '100%', fontFamily: 'SFMono-Regular, Consolas, monospace', fontSize: 15, lineHeight: 1.55, resize: 'vertical', minHeight: 380 }} />
          <div style={{ display: 'flex', gap: 8, marginTop: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            <Button variant="primary" disabled={docsSaving || !docsMD.trim()} onClick={saveDocs}><Icon n="checkcircle" style={MI} />{docsSaving ? t('docsEdit.saving') : t('common.save')}</Button>
            <Button variant="secondary" disabled={!docsMD.trim()} onClick={previewDocs}><Icon n="eye" style={MI} />{t('docsEdit.preview')}</Button>
            <label style={{ cursor: 'pointer' }}>
              <Icon n="upload" style={MI} />{t('docsEdit.import')}
              <input type="file" accept=".md,.markdown,.txt" hidden onChange={importDocs} />
            </label>
            <Button variant="secondary" onClick={exportDocs}><Icon n="download" style={MI} />{t('docsEdit.export')}</Button>
            <Button variant="danger" onClick={resetDocs}>↺ {t('docsEdit.reset')}</Button>
            {docsDefaultBadge && <span style={{ fontSize: 14, color: 'var(--adm-hint)' }}>{t('docsEdit.isDefault')}</span>}
          </div>
        </Panel>
      )}
    </>
  )
}
