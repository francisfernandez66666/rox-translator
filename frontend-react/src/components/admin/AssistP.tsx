// ============================================================================
// components/admin/AssistP.tsx — AI 助手管理面板（★ #34 前端重做，2026-09-21）
//
// 这版把「iframe 内嵌 assist 自带管理台」整个换成原生 React 面板，目的是让 AI 助手管理
// 与后台其它面板**同一套交互**：同一套 LangCross 组件、同一套暗色主题、同一套 i18n（12 语种）、
// 同一套 Toast/确认弹窗，并且只走主后台同源接口（api/assistAdmin.ts）。
//
// 三条硬口径（改动前请先读）：
//   1. 管理 Token 不进浏览器：读写由主后台 /api/admin/assist/* 代理，服务端注入 X-Assist-Admin；
//      面板只显示「已配置/未配置 + 来源 + 掩码」，保存语义与模型配置（ModelsP）完全一致
//      （★ 〇-LK 按用户口径「参考我其他 llm 配置的方式重新做」）：输入框不回填、type=password、
//      autoComplete=new-password，留空=不修改，清除必须点「清除」按钮（显式 clear=true），
//      环境变量占住生效位时给锁定提示而不是静默显示「未配置」。
//   2. fail-closed：服务不可达或 Token 未配置时，状态条直接给处置指引，各页签不渲染假数据；
//      真实商户凭据缺失时同理（不静默回退）。
//   3. 功能尺寸对齐旧管理台（internal/assist/web/admin.html）：四类数据 CRUD + 启停 + 删除确认、
//      LLM 四项配置与连通测试、对话配置六项、同义词归一、会话统计与未答问题清单，一项不少；
//      assist 自带页面仍可通过「备用管理台」链接直达（未删除，只是不再是主路径）。
//
// 数据面（assist 独立服务的表结构，见 backend-go/internal/assist/store/store.go）：
//   kb_entries(key,category,title,content,keywords,link_keys,priority,enabled)
//   scripts(key,stype,title,keywords,link_keys,content,priority,enabled)
//   flows(key,name,description,trigger_keywords,steps_json,enabled)
//   feature_links(key,name,description,url,ftype,icon,sort,enabled)
// ============================================================================
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, DataTable, Dialog, InlineBanner, StatusPill, Switch, Tabs } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import { toastError, toastSuccess, toastWarn } from '@/lib/toastBus'
import { runGuarded } from '@/lib/runGuarded'
import { useT } from '@/i18n'
import { Panel, Field } from './parts'
import { ASSIST_API } from '@/api/assist'
import { adminAssistToken, adminAssistTokenRotate } from '@/api'
import type { AssistTokenResp } from '@/api'
import {
  AssistBizError, assistAdminConfig, assistAdminConfigSet, assistAdminCreate, assistAdminDelete,
  assistAdminLLMTest, assistAdminList, assistAdminSessions, assistAdminStatus, assistAdminUpdate,
} from '@/api/assistAdmin'
import type { AssistArea, AssistRow, AssistSessionsResp } from '@/api/assistAdmin'

// 顾问管理台各面板共用的宽松记录别名（后端字段渐进增加，避免每加一列改一处类型）
type Any = Record<string, any>

/** 表单字段描述：kind 决定控件；options 仅 select 用（值 + 文案 i18n 键） */
interface FormField {
  field: string
  labelKey: string
  kind: 'text' | 'textarea' | 'number' | 'select'
  options?: [string, string][]
  wide?: boolean
}

/** 区域定义：列展示 + 表单字段 + 空值默认（与 assist 侧建表默认值一致，避免提交漏字段） */
interface AreaDef {
  area: AssistArea
  tabKey: string
  titleKey: string
  cols: { field: string; labelKey: string; width: number; wide?: boolean }[]
  fields: FormField[]
  defaults: AssistRow
}

/** AREAS 四类管理对象的唯一事实源（新增一类只改这里，表格与表单自动跟随） */
const AREAS: AreaDef[] = [
  {
    area: 'kb', tabKey: 'kb', titleKey: 'assist.tabKb',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'category', labelKey: 'assist.fCategory', width: 100 },
      { field: 'title', labelKey: 'assist.fTitle', width: 180, wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', width: 180, wide: true },
      { field: 'priority', labelKey: 'assist.fPriority', width: 80 },
    ],
    fields: [
      { field: 'category', labelKey: 'assist.fCategory', kind: 'text' },
      { field: 'title', labelKey: 'assist.fTitle', kind: 'text' },
      { field: 'content', labelKey: 'assist.fContent', kind: 'textarea', wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', kind: 'text', wide: true },
      { field: 'link_keys', labelKey: 'assist.fLinkKeys', kind: 'text' },
      { field: 'priority', labelKey: 'assist.fPriority', kind: 'number' },
    ],
    defaults: { category: 'usage', title: '', content: '', keywords: '', link_keys: '', priority: 5, enabled: 1 },
  },
  {
    area: 'scripts', tabKey: 'scripts', titleKey: 'assist.tabScripts',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'stype', labelKey: 'assist.fStype', width: 100 },
      { field: 'title', labelKey: 'assist.fTitle', width: 160, wide: true },
      { field: 'content', labelKey: 'assist.fContent', width: 240, wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', width: 160, wide: true },
    ],
    fields: [
      { field: 'stype', labelKey: 'assist.fStype', kind: 'select', options: [['keyword', 'assist.stKeyword'], ['greeting', 'assist.stGreeting'], ['fallback', 'assist.stFallback']] },
      { field: 'title', labelKey: 'assist.fTitle', kind: 'text' },
      { field: 'content', labelKey: 'assist.fContent', kind: 'textarea', wide: true },
      { field: 'keywords', labelKey: 'assist.fKeywords', kind: 'text', wide: true },
      { field: 'link_keys', labelKey: 'assist.fLinkKeys', kind: 'text' },
      { field: 'priority', labelKey: 'assist.fPriority', kind: 'number' },
    ],
    defaults: { stype: 'keyword', title: '', content: '', keywords: '', link_keys: '', priority: 5, enabled: 1 },
  },
  {
    area: 'flows', tabKey: 'flows', titleKey: 'assist.tabFlows',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'name', labelKey: 'assist.fName', width: 160, wide: true },
      { field: 'trigger_keywords', labelKey: 'assist.fTrigger', width: 200, wide: true },
      { field: 'steps_json', labelKey: 'assist.fSteps', width: 240, wide: true },
    ],
    fields: [
      { field: 'name', labelKey: 'assist.fName', kind: 'text' },
      { field: 'description', labelKey: 'assist.fDesc', kind: 'text', wide: true },
      { field: 'trigger_keywords', labelKey: 'assist.fTrigger', kind: 'text', wide: true },
      { field: 'steps_json', labelKey: 'assist.fSteps', kind: 'textarea', wide: true },
    ],
    defaults: { name: '', description: '', trigger_keywords: '', steps_json: '[]', enabled: 1 },
  },
  {
    area: 'features', tabKey: 'features', titleKey: 'assist.tabFeatures',
    cols: [
      { field: 'key', labelKey: 'assist.colKey', width: 140 },
      { field: 'name', labelKey: 'assist.fName', width: 160, wide: true },
      { field: 'url', labelKey: 'assist.fUrl', width: 220, wide: true },
      { field: 'ftype', labelKey: 'assist.fFtype', width: 100 },
      { field: 'sort', labelKey: 'assist.fSort', width: 80 },
    ],
    fields: [
      { field: 'name', labelKey: 'assist.fName', kind: 'text' },
      { field: 'description', labelKey: 'assist.fDesc', kind: 'text', wide: true },
      { field: 'url', labelKey: 'assist.fUrl', kind: 'text', wide: true },
      { field: 'ftype', labelKey: 'assist.fFtype', kind: 'select', options: [['route', 'assist.ftRoute'], ['link', 'assist.ftLink']] },
      { field: 'icon', labelKey: 'assist.fIcon', kind: 'text' },
      { field: 'sort', labelKey: 'assist.fSort', kind: 'number' },
    ],
    defaults: { name: '', description: '', url: '', ftype: 'route', icon: '', sort: 50, enabled: 1 },
  },
]

/** 对话配置六项 + LLM 四项：键名直接用 assist 侧真实配置键（运维排查时要能对上），文案只给说明 */
const CHAT_CFG = ['welcome', 'persona', 'temperature', 'max_tokens', 'quick_chips']
// LLM 配置四键的固定顺序：掩码回显与「留空不改」按这个序遍历
const LLM_CFG = ['llm_base_url', 'llm_api_key', 'llm_model', 'llm_model_backup']

/** 面板页签 */
type Tab = 'overview' | 'config' | 'kb' | 'scripts' | 'flows' | 'features'

// AssistP AI 助手管理面板（仅超管；后端代理双端把关）
export default function AssistP() {
  const [, t] = useT()
  const [tab, setTab] = useState<Tab>('overview')
  const [status, setStatus] = useState<Any | null>(null)
  // tokState：管理 Token 的掩码态（set/掩码/来源/env 覆盖），来自 /api/admin/assist/token
  const [tokState, setTokState] = useState<AssistTokenResp | null>(null)
  const [rotateVal, setRotateVal] = useState('')
  const [rotating, setRotating] = useState(false)
  const [bizErr, setBizErr] = useState('')

  // 概览数据（会话统计 + 未答问题 + LLM 生效模式）
  const [sess, setSess] = useState<AssistSessionsResp | null>(null)
  // 数据类页签的行缓存与表单弹窗
  const [rowsMap, setRowsMap] = useState<Record<string, AssistRow[]>>({})
  const [form, setForm] = useState<{ def: AreaDef; row: Any } | null>(null)
  const [busy, setBusy] = useState(false)
  // 配置页签
  const [cfg, setCfg] = useState<Record<string, string>>({})
  const [testOut, setTestOut] = useState('')
  const [testing, setTesting] = useState(false)

  const refreshStatus = useCallback(async () => {
    const r = await runGuarded(() => assistAdminStatus())
    if (r) setStatus(r)
  }, [])

  // refreshTok 拉 Token 掩码态：与 refreshStatus 分开拉，代理层状态只说「通不通」，
  // Token 是否配置、配在哪一处由主后台库自己回答（assist 服务挂了也要能改 Token）
  const refreshTok = useCallback(async () => {
    const r = await runGuarded(() => adminAssistToken())
    if (r?.success) setTokState(r)
  }, [])

  useEffect(() => { void refreshStatus(); void refreshTok() }, [refreshStatus, refreshTok])

  // bizFail 统一处理业务失败：代理层 fail-closed 的 message 已是给用户看的处置指引
  function bizFail(e: unknown) {
    const msg = e instanceof AssistBizError ? e.message : String((e as any)?.message || e)
    setBizErr(msg)
    void toastError(msg)
  }

  const loadSessions = useCallback(async () => {
    try {
      setSess(await assistAdminSessions())
      setBizErr('')
    } catch (e) { bizFail(e) }
  }, [])

  const loadArea = useCallback(async (area: AssistArea) => {
    try {
      const rs = await assistAdminList(area)
      setRowsMap((m) => ({ ...m, [area]: rs }))
      setBizErr('')
    } catch (e) { bizFail(e) }
  }, [])

  const loadConfig = useCallback(async () => {
    try {
      const list = await assistAdminConfig()
      const m: Record<string, string> = {}
      for (const c of list) m[c.key] = String(c.value ?? '')
      setCfg(m)
      setBizErr('')
    } catch (e) { bizFail(e) }
  }, [])

  // 切页签按需拉数据（失败态不缓存空列表，重试只需再点一次页签）
  useEffect(() => {
    if (tab === 'overview') void loadSessions()
    else if (tab === 'config') { void loadConfig(); void loadSessions() }
    else void loadArea(tab as AssistArea)
  }, [tab, loadSessions, loadConfig, loadArea])

  // applyTokenResp 用保存接口的返回体刷新掩码态并分级提示（保存/清除共用）
  function applyTokenResp(r: AssistTokenResp) {
    if (r.success === false) { void toastError(String(r.message || t('assist.saveFail'))); return }
    setTokState(r)
    setRotateVal('')
    void refreshStatus()
    // Token 变更后当前页签数据可能已不可用，立即重拉一次，避免「保存成功但列表还是空的」
    if (tab === 'overview') void loadSessions()
    else if (tab === 'config') void loadConfig()
    else void loadArea(tab as AssistArea)
  }

  // saveToken 保存（轮换）管理 Token。口径与 ModelsP 的密钥一致：
  //   留空或仍是掩码 = 不修改（不误删），清除走「清除」按钮显式传 clear=true。
  async function saveToken() {
    if (rotating) return
    const next = rotateVal.trim()
    // 掩码串（含 ****）被当成新值提交会把凭据改成字面的 ****——直接拦下，别发请求
    if (next.includes('****')) { void toastWarn(t('assist.tokenMaskTyped')); return }
    if (!next) { void toastWarn(t('assist.tokenUnchanged')); return }
    setRotating(true)
    try {
      // clear 显式传 false：清除凭据这件事只允许走 clearToken 那条带二次确认的路径
      const r = await adminAssistTokenRotate(next, false)
      if (!r.success) { void toastError(String(r.message || t('assist.saveFail'))); return }
      // 分级提示：助手侧同步成功=即时生效；同步失败=还要重启 translator-assist（不能只说「已保存」）
      if (r.changed && r.pushed) void toastSuccess(t('assist.tokenPushed'))
      else if (r.changed && !r.pushed) void toastWarn(t('assist.tokenSaved'), t('assist.tokenNotPushed'))
      else void toastWarn(t('assist.tokenUnchanged'))
      applyTokenResp(r)
    } catch (e) { bizFail(e) } finally { setRotating(false) }
  }

  // clearToken 显式清除库内 Token（回落环境变量）——必须二次确认，旧版「空串=清除」太易误触
  async function clearToken() {
    if (rotating) return
    if (!(await confirmDialog({ body: t('assist.tokenClearConfirm'), confirmText: t('common.delete') }))) return
    setRotating(true)
    try {
      const r = await adminAssistTokenRotate('', true)
      if (!r.success) { void toastError(String(r.message || t('assist.saveFail'))); return }
      void toastSuccess(t('assist.tokenCleared'))
      applyTokenResp(r)
    } catch (e) { bizFail(e) } finally { setRotating(false) }
  }

  // saveRow 新增/编辑提交：数字字段转数，空 key 直接拦（assist 侧 key 是 UNIQUE 且被前端当主展示列）
  async function saveRow() {
    if (!form || busy) return
    const { def, row } = form
    const key = String(row.key || '').trim()
    if (!key) { void toastError(t('assist.needKey')); return }
    const payload: Any = { ...def.defaults, ...row, key }
    for (const f of def.fields) if (f.kind === 'number') payload[f.field] = Number(payload[f.field]) || 0
    payload.enabled = Number(row.enabled ?? 1) ? 1 : 0
    setBusy(true)
    try {
      const id = Number(row.id) || 0
      if (id > 0) await assistAdminUpdate(def.area, id, payload)
      else await assistAdminCreate(def.area, payload)
      void toastSuccess(t('assist.saved'))
      setForm(null)
      await loadArea(def.area)
    } catch (e) { bizFail(e) } finally { setBusy(false) }
  }

  // toggleRow 启停：只改 enabled，会话侧立即生效（assist 读表时按 enabled=1 过滤）
  async function toggleRow(def: AreaDef, row: AssistRow, on: boolean) {
    try {
      await assistAdminUpdate(def.area, Number(row.id), { enabled: on ? 1 : 0 })
      await loadArea(def.area)
    } catch (e) { bizFail(e) }
  }

  async function removeRow(def: AreaDef, row: AssistRow) {
    if (!(await confirmDialog({ body: t('assist.deleteConfirm'), confirmText: t('common.delete') }))) return
    try {
      await assistAdminDelete(def.area, Number(row.id))
      void toastSuccess(t('assist.deleted'))
      await loadArea(def.area)
    } catch (e) { bizFail(e) }
  }

  // saveCfgGroup 批量保存配置：掩码值（含 ***）由 assist 侧自行跳过写库，前端原样提交即可
  async function saveCfgKeys(keys: string[]) {
    try {
      for (const k of keys) await assistAdminConfigSet(k, String(cfg[k] ?? ''))
      void toastSuccess(t('assist.saved'))
      await loadConfig()
    } catch (e) { bizFail(e) }
  }

  async function testLLM() {
    if (testing) return
    setTesting(true)
    setTestOut('')
    try {
      const r = await assistAdminLLMTest()
      setTestOut(r.ok ? t('assist.llmOk').replace('{model}', String(r.model || '')).replace('{ms}', String(r.ms ?? 0))
        : t('assist.llmFail').replace('{error}', String(r.error || t('assist.unknown'))))
    } catch (e) { bizFail(e) } finally { setTesting(false) }
  }

  const srcLabel = useMemo(() => {
    // 优先取 Token 接口自己的来源；拉不到时回落代理状态，两者口径同为 env/db/none
    const s = String(tokState?.source ?? status?.token_src ?? '')
    return s === 'env' ? t('assist.srcEnv') : s === 'db' ? t('assist.srcDb') : t('assist.srcNone')
  }, [status, tokState, t])

  const tabs = [
    { key: 'overview', label: t('assist.tabOverview') },
    { key: 'config', label: t('assist.tabConfig') },
    { key: 'kb', label: t('assist.tabKb') },
    { key: 'scripts', label: t('assist.tabScripts') },
    { key: 'flows', label: t('assist.tabFlows') },
    { key: 'features', label: t('assist.tabFeatures') },
  ]

  const reachable = status?.reachable === true
  // tokenSrc：两处来源同口径（env/db/none），Token 接口先到位就用它，避免代理慢半拍时横幅说「未配置」
  const tokenSrc = String(tokState?.source ?? status?.token_src ?? '')
  // 色带口径（InlineBanner 只有 success/warn/error 三档）：不可达=error（红，功能真的不能用），
  // 未配置 Token 与检测中=warn（黄，按提示补一下就通），全绿才 success。
  const bannerTone: 'success' | 'warn' | 'error' = !status
    ? 'warn'
    : !reachable
      ? 'error'
      : tokenSrc === 'none'
        ? 'warn'
        : 'success'
  const bannerText = !status
    ? t('assist.checking')
    : !reachable
      ? t('assist.downHint')
      : tokenSrc === 'none'
        ? t('assist.noTokenHint')
        : t('assist.readyHint').replace('{src}', srcLabel)

  return (
    <div>
      <Panel title={t('assist.title')} extra={
        <Button size="sm" variant="secondary" onClick={() => { void refreshStatus(); void refreshTok() }}>{t('assist.recheck')}</Button>
      }>
        <div style={{ fontSize: 14, color: 'var(--adm-hint)', marginBottom: 10 }}>{t('assist.subtitle')}</div>
        <InlineBanner tone={bannerTone}>{bannerText}</InlineBanner>

        {/* ===== 管理 Token 区（★ 〇-LK：与 ModelsP 密钥区同一套范式） =====
            显示行只回掩码态；输入框刻意不回填任何已有值（后端也不下发），
            所以「打开面板 → 直接点保存」不可能把凭据清空，这与旧版 iframe 手填 Token 的体验断层是两回事。 */}
        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', marginTop: 12 }}>
          <StatusPill tone={tokState?.set ? 'success' : 'warn'}>
            {tokState?.set ? t('assist.tokenSet') : t('assist.tokenUnset')}
          </StatusPill>
          <span style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{t('assist.tokenSrcLabel')}：{srcLabel}</span>
          {tokState?.set && <code style={{ fontSize: 12.5 }}>{tokState.masked}</code>}
          <span style={{ fontSize: 12, color: 'var(--adm-hint)' }}>{t('assist.tokenMaskNote')}</span>
        </div>
        {tokState?.env_overridden && (
          <div style={{ marginTop: 8 }}><InlineBanner tone="warn">{t('assist.tokenEnvLocked')}</InlineBanner></div>
        )}
        <form onSubmit={(e) => { e.preventDefault(); void saveToken() }}
          style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 10 }}>
          {/* autoComplete="new-password"：屏蔽密码管理器对凭据框的自动填充/保存（与模型密钥框同口径） */}
          <input
            className="lc-input" type="password" autoComplete="new-password" aria-label={t('assist.tokenLabel')}
            value={rotateVal} placeholder={t('assist.tokenPh')} disabled={rotating}
            onChange={(e) => setRotateVal(e.target.value)} style={{ maxWidth: 420 }}
          />
          <Button size="sm" variant="primary" type="submit" disabled={rotating || !rotateVal.trim()}>{t('assist.tokenSave')}</Button>
          {tokState?.set && tokState?.source === 'db' && (
            <Button size="sm" variant="danger" disabled={rotating} onClick={() => void clearToken()}>{t('assist.tokenClear')}</Button>
          )}
          <a className="lc-link" href={`${ASSIST_API}/assist/admin`} target="_blank" rel="noreferrer" style={{ fontSize: 13 }}>
            {t('assist.legacyAdmin')}
          </a>
        </form>
        {bizErr && <div style={{ marginTop: 8 }}><InlineBanner tone="error">{bizErr}</InlineBanner></div>}
      </Panel>

      {/* data-testid 只给 e2e 用：侧边栏也有「知识库」等同类文案，页签必须限定在本面板内点击，
          否则 Playwright 的 .first() 会命中侧栏菜单并把面板切走（2026-09-21 A3 假失败根因） */}
      <div style={{ marginTop: 14 }} data-testid="assist-tabs">
        <Tabs items={tabs} activeKey={tab} onChange={(k) => setTab(k as Tab)} />
      </div>

      {tab === 'overview' && (
        <Panel title={t('assist.tabOverview')}>
          <div style={{ display: 'flex', gap: 26, flexWrap: 'wrap', fontSize: 14 }}>
            <span>{t('assist.statSessions')}：<b>{sess?.total ?? '—'}</b></span>
            <span>{t('assist.statMessages')}：<b>{sess?.messages ?? '—'}</b></span>
            <span>{t('assist.statUnanswered')}：<b>{sess?.unanswered.length ?? '—'}</b></span>
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              LLM
              <StatusPill tone={sess?.llm_mode === 'rule' ? 'warn' : 'success'}>
                {sess?.llm_mode === 'env' ? t('assist.modeEnv') : sess?.llm_mode === 'db' ? t('assist.modeDb') : t('assist.modeRule')}
              </StatusPill>
            </span>
          </div>
          {(sess?.unanswered.length ?? 0) > 0 && (
            <div style={{ marginTop: 14 }}>
              <div style={{ fontSize: 14, marginBottom: 6 }}>{t('assist.unansweredTitle')}</div>
              <ul style={{ margin: 0, paddingLeft: 20, fontSize: 13, lineHeight: 1.9 }}>
                {sess!.unanswered.map((q, i) => <li key={i}>{q}</li>)}
              </ul>
            </div>
          )}
          <div style={{ marginTop: 14 }}>
            <DataTable
              rowKey={(r) => String((r as Any).id)} rows={(sess?.sessions as Any[]) || []} emptyText={t('assist.noRows')}
              columns={[
                { key: 'id', title: t('assist.colSession'), width: 200, render: (r) => <code>{String((r as Any).id)}</code> },
                { key: 'page_url', title: t('assist.colPage'), width: 220, render: (r) => String((r as Any).page_url || '-') },
                { key: 'msg_count', title: t('assist.colMsgCount'), width: 90 },
                { key: 'in_flow', title: t('assist.colFlow'), width: 140, render: (r) => String((r as Any).in_flow || '-') },
                { key: 'last_at', title: t('assist.colLastAt'), width: 190, render: (r) => String((r as Any).last_at || '').slice(0, 19) },
              ]}
            />
          </div>
        </Panel>
      )}

      {tab === 'config' && (
        <Panel title={t('assist.tabConfig')}>
          <div style={{ fontSize: 13, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('assist.llmHint')}</div>
          {LLM_CFG.map((k) => (
            <Field key={k} label={k}>
              <input
                className="lc-input" style={{ width: 420 }} value={cfg[k] ?? ''}
                type={k === 'llm_api_key' ? 'password' : 'text'} aria-label={k}
                onChange={(e) => setCfg({ ...cfg, [k]: e.target.value })}
              />
            </Field>
          ))}
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', margin: '6px 0 16px' }}>
            <Button variant="primary" size="sm" onClick={() => void saveCfgKeys(LLM_CFG)}>{t('assist.llmSave')}</Button>
            <Button variant="secondary" size="sm" disabled={testing} onClick={() => void testLLM()}>{t('assist.llmTest')}</Button>
            {testOut && <span style={{ fontSize: 13, color: 'var(--adm-hint)' }}>{testOut}</span>}
          </div>
          <div style={{ fontSize: 13, color: 'var(--adm-hint)', marginBottom: 8 }}>{t('assist.cfgHint')}</div>
          {CHAT_CFG.map((k) => (
            <Field key={k} label={k}>
              <textarea
                className="lc-textarea" style={{ minHeight: 56, width: '100%', maxWidth: 620 }} value={cfg[k] ?? ''}
                aria-label={k} onChange={(e) => setCfg({ ...cfg, [k]: e.target.value })}
              />
            </Field>
          ))}
          <Field label="synonyms">
            <textarea
              className="lc-textarea" style={{ minHeight: 88, width: '100%', maxWidth: 620 }} value={cfg.synonyms ?? ''}
              placeholder={t('assist.synPh')} aria-label="synonyms"
              onChange={(e) => setCfg({ ...cfg, synonyms: e.target.value })}
            />
          </Field>
          <div style={{ fontSize: 12.5, color: 'var(--adm-hint)', margin: '4px 0 10px' }}>{t('assist.synHint')}</div>
          <Button variant="primary" size="sm" onClick={() => void saveCfgKeys([...CHAT_CFG, 'synonyms'])}>{t('assist.cfgSave')}</Button>
        </Panel>
      )}

      {tab !== 'overview' && tab !== 'config' && (() => {
        const def = AREAS.find((a) => a.tabKey === tab)!
        return (
          <Panel title={t(def.titleKey)} extra={
            <Button size="sm" variant="primary" onClick={() => setForm({ def, row: { ...def.defaults, id: 0, key: '', enabled: 1 } })}>
              {t('assist.create')}
            </Button>
          }>
            <DataTable
              rowKey={(r) => String((r as Any).id)} rows={rowsMap[def.area] || []} emptyText={t('assist.noRows')}
              columns={[
                ...def.cols.map((c) => ({
                  key: c.field, title: t(c.labelKey), width: c.width,
                  render: (r: Any) => (c.wide ? String(r[c.field] ?? '') : <code>{String(r[c.field] ?? '')}</code>),
                })),
                {
                  key: 'enabled', title: t('common.status'), width: 130, render: (r: Any) => (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <Switch checked={Number(r.enabled) === 1} onChange={(e) => void toggleRow(def, r as AssistRow, e.target.checked)} />
                      <StatusPill tone={Number(r.enabled) === 1 ? 'success' : 'idle'}>
                        {Number(r.enabled) === 1 ? t('assist.enabled') : t('ind.off')}
                      </StatusPill>
                    </div>
                  ),
                },
                {
                  key: 'op', title: '', width: 150, render: (r: Any) => (
                    <div style={{ display: 'flex', gap: 12 }}>
                      <a className="lc-link" onClick={() => setForm({ def, row: { ...def.defaults, ...r } })}>{t('coupons.edit')}</a>
                      <a className="lc-link" style={{ color: 'var(--lc-danger, #E5484D)' }} onClick={() => void removeRow(def, r as AssistRow)}>{t('common.delete')}</a>
                    </div>
                  ),
                },
              ]}
            />
          </Panel>
        )
      })()}

      {form && (
        <Dialog
          open onCancel={() => setForm(null)} onConfirm={() => void saveRow()}
          title={`${Number(form.row.id) > 0 ? t('assist.editTitle') : t('assist.create')}${t(form.def.titleKey)}`}
          confirmText={t('common.save')} cancelText={t('common.cancel')}
        >
          <Field label={t('assist.colKey')}>
            {/* key 是 assist 侧的唯一定位标识（话术/知识命中都按它），编辑时不允许改，避免断引用 */}
            <input className="lc-input" style={{ width: 240 }} value={String(form.row.key ?? '')} aria-label={t('assist.colKey')}
              disabled={Number(form.row.id) > 0} onChange={(e) => setForm({ ...form, row: { ...form.row, key: e.target.value } })} />
          </Field>
          {form.def.fields.map((f) => (
            <Field key={f.field} label={t(f.labelKey)}>
              {f.kind === 'textarea' && (
                <textarea className="lc-textarea" style={{ minHeight: 76, width: '100%' }} aria-label={t(f.labelKey)}
                  value={String(form.row[f.field] ?? '')} onChange={(e) => setForm({ ...form, row: { ...form.row, [f.field]: e.target.value } })} />
              )}
              {f.kind === 'number' && (
                <input className="lc-input" style={{ width: 140 }} type="number" min={0} aria-label={t(f.labelKey)}
                  value={String(form.row[f.field] ?? '')} onChange={(e) => setForm({ ...form, row: { ...form.row, [f.field]: e.target.value } })} />
              )}
              {f.kind === 'select' && (
                <select className="lc-select" style={{ width: 200 }} aria-label={t(f.labelKey)}
                  value={String(form.row[f.field] ?? f.options?.[0]?.[0] ?? '')}
                  onChange={(e) => setForm({ ...form, row: { ...form.row, [f.field]: e.target.value } })}>
                  {(f.options || []).map(([v, k]) => <option key={v} value={v}>{t(k)}</option>)}
                </select>
              )}
              {f.kind === 'text' && (
                <input className="lc-input" style={{ width: f.wide ? '100%' : 300, maxWidth: 460 }} aria-label={t(f.labelKey)}
                  value={String(form.row[f.field] ?? '')} onChange={(e) => setForm({ ...form, row: { ...form.row, [f.field]: e.target.value } })} />
              )}
            </Field>
          ))}
          <Field label={t('assist.enabled')}>
            <Switch checked={Number(form.row.enabled ?? 1) === 1}
              onChange={(e) => setForm({ ...form, row: { ...form.row, enabled: e.target.checked ? 1 : 0 } })} />
          </Field>
        </Dialog>
      )}
    </div>
  )
}
