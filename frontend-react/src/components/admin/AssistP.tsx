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
import { Button, DataTable, InlineBanner, StatusPill, Tabs, Input, Textarea, Link, Toolbar } from '@/ui/langcross/src'
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
  assistAdminLLMTest, assistAdminList, assistAdminSessions, assistAdminStatus, assistAdminSystemValues,
  assistAdminUpdate,
} from '@/api/assistAdmin'
import type { AssistArea, AssistRow, AssistSessionsResp, AssistSystemValuesResp } from '@/api/assistAdmin'
import { AREAS, AreaTable, AreaDialog } from './sales/SalesAreaTable'
import type { AreaDef, AreaFormState } from './sales/SalesAreaTable'

/** 面板版式：设计变量一律取 tokens.css 的 --lc-*，不写内联布局样式、不写死色值。 */
const ASSIST_CSS = `
.lc-as-sub { font-size: 16px; color: var(--lc-text-2); margin-bottom: 10px; }
.lc-as-row { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; margin-top: 12px; }
.lc-as-hint { font-size: 15px; color: var(--lc-text-3); }
.lc-as-hint-mb { margin-bottom: 8px; }
.lc-as-hint-sm { font-size: 14px; color: var(--lc-text-3); }
.lc-as-mono { font-size: 14.5px; }
.lc-as-mt8 { margin-top: 8px; }
.lc-as-mt14 { margin-top: 14px; }
.lc-as-stats { display: flex; gap: 26px; flex-wrap: wrap; font-size: 16px; }
.lc-as-inline { display: inline-flex; align-items: center; gap: 8px; }
.lc-as-sub2 { font-size: 16px; margin-bottom: 6px; }
.lc-as-list { margin: 0; padding-left: 20px; font-size: 15px; line-height: 1.9; }
.lc-as-actions { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin: 6px 0 16px; }
.lc-as-note { font-size: 14.5px; color: var(--lc-text-3); margin: 4px 0 10px; }
.lc-as-cell { display: flex; align-items: center; gap: 8px; }
.lc-as-ops { display: flex; gap: 12px; }
.lc-input.lc-as-w420 { width: 420px; max-width: 100%; }
.lc-input.lc-as-w240 { width: 240px; max-width: 100%; }
.lc-input.lc-as-w140 { width: 140px; max-width: 100%; }
.lc-input.lc-as-w300 { width: 300px; max-width: 460px; }
.lc-input.lc-as-wide { width: 100%; max-width: 460px; }
.lc-textarea.lc-as-ta { min-height: 56px; max-width: 620px; }
.lc-textarea.lc-as-ta-lg { min-height: 88px; max-width: 620px; }
.lc-textarea.lc-as-ta-md { min-height: 76px; }
.lc-select.lc-as-sel { width: 200px; }
.lc-as-svout { white-space: pre-wrap; font-size: 14.5px; color: var(--lc-text-3); margin-top: 10px; }
`

// 顾问管理台各面板共用的宽松记录别名（后端字段渐进增加，避免每加一列改一处类型）
type Any = Record<string, any>

// 区域定义与四类管理对象清单（AREAS）已抽到 ./sales/SalesAreaTable（W1 抽取，DOM 不变）；
// 本面板只持有数据与读写动作，表格/弹窗渲染走 AreaTable/AreaDialog。

/** 对话配置七项 + LLM 四项：键名直接用 assist 侧真实配置键（运维排查时要能对上），文案只给说明 */
const CHAT_CFG = ['welcome', 'persona', 'tone_rules', 'promise_rules', 'temperature', 'max_tokens', 'quick_chips']
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
  const [form, setForm] = useState<AreaFormState | null>(null)
  const [busy, setBusy] = useState(false)
  // 配置页签
  const [cfg, setCfg] = useState<Record<string, string>>({})
  const [testOut, setTestOut] = useState('')
  const [testing, setTesting] = useState(false)
  // 系统现值（价格系数/语种数实时值）：只在点「现取一次」时拨，失败在面板内出声
  const [sv, setSv] = useState<AssistSystemValuesResp | null>(null)
  const [svBusy, setSvBusy] = useState(false)
  const [svErr, setSvErr] = useState('')

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

  // saveMainBase 保存现值注入拨向的主服务地址（assist configs.main_base_url，60s 缓存后生效）
  async function saveMainBase() {
    try {
      await assistAdminConfigSet('main_base_url', String(cfg.main_base_url ?? '').trim())
      void toastSuccess(t('assist.svBaseSaved'))
      await loadConfig()
    } catch (e) { bizFail(e) }
  }

  // loadSystemValues 现取一次系统现值：ok/fresh 为空＝没取到（软路径按设计不报数），不是取到 0
  async function loadSystemValues() {
    if (svBusy) return
    setSvBusy(true)
    setSvErr('')
    try {
      setSv(await assistAdminSystemValues())
    } catch (e) {
      setSv(null)
      setSvErr(e instanceof AssistBizError ? e.message : String((e as any)?.message || e))
    } finally { setSvBusy(false) }
  }

  const srcLabel = useMemo(() => {
    // 优先取 Token 接口自己的来源；拉不到时回落代理状态，两者口径同为 env/db/none
    const s = String(tokState?.source ?? status?.token_src ?? '')
    return s === 'env' ? t('assist.srcEnv') : s === 'db' ? t('assist.srcDb') : t('assist.srcNone')
  }, [status, tokState, t])

  // 系统现值输出段：取数中 / 失败 / 还没取过 / 取到了 四态各说各的话（空＝没取到，不是取到 0）
  const svOut = svBusy ? t('assist.svFetching')
    : svErr ? svErr
      : !sv ? t('assist.svOutIdle')
        : (() => {
          const age = sv.cached_age_sec < 0 ? t('assist.svAgeNever')
            : t('assist.svAgeCached').replace('{sec}', String(Math.round(sv.cached_age_sec)))
          const head = t('assist.svOutHead').replace('{base}', sv.base_url).replace('{ms}', String(sv.ms)).replace('{age}', age)
          return `${head}\n\n${t('assist.svFreshLabel')}\n${sv.fresh || t('assist.svFreshEmpty')}`
        })()

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
      <style>{ASSIST_CSS}</style>
      <Panel title={t('assist.title')} extra={
        <Button size="sm" variant="secondary" onClick={() => { void refreshStatus(); void refreshTok() }}>{t('assist.recheck')}</Button>
      }>
        <div className="lc-as-sub">{t('assist.subtitle')}</div>
        <InlineBanner tone={bannerTone}>{bannerText}</InlineBanner>

        {/* ===== 管理 Token 区（★ 〇-LK：与 ModelsP 密钥区同一套范式） =====
            显示行只回掩码态；输入框刻意不回填任何已有值（后端也不下发），
            所以「打开面板 → 直接点保存」不可能把凭据清空，这与旧版 iframe 手填 Token 的体验断层是两回事。 */}
        <div className="lc-as-row">
          <StatusPill tone={tokState?.set ? 'success' : 'warn'}>
            {tokState?.set ? t('assist.tokenSet') : t('assist.tokenUnset')}
          </StatusPill>
          <span className="lc-as-hint">{t('assist.tokenSrcLabel')}：{srcLabel}</span>
          {tokState?.set && <code className="lc-as-mono">{tokState.masked}</code>}
          <span className="lc-as-hint-sm">{t('assist.tokenMaskNote')}</span>
        </div>
        {tokState?.env_overridden && (
          <div className="lc-as-mt8"><InlineBanner tone="warn">{t('assist.tokenEnvLocked')}</InlineBanner></div>
        )}
        {/* 组件库无 Form 容器：这一行改用 Toolbar 承载；保存走按钮 onClick，
            回车提交由输入框 onKeyDown 兜住，行为与原生 form 一致。 */}
        <Toolbar>
          {/* autoComplete="new-password"：屏蔽密码管理器对凭据框的自动填充/保存（与模型密钥框同口径） */}
          <Input
            className="lc-as-w420" type="password" autoComplete="new-password" aria-label={t('assist.tokenLabel')}
            value={rotateVal} placeholder={t('assist.tokenPh')} disabled={rotating}
            onChange={(e) => setRotateVal(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); void saveToken() } }}
          />
          <Button size="sm" variant="primary" disabled={rotating || !rotateVal.trim()} onClick={() => void saveToken()}>{t('assist.tokenSave')}</Button>
          {tokState?.set && tokState?.source === 'db' && (
            <Button size="sm" variant="danger" disabled={rotating} onClick={() => void clearToken()}>{t('assist.tokenClear')}</Button>
          )}
          {/* 备用管理台是外链：Link 为按钮语义，用 window.open 保持新标签页 + noreferrer 行为 */}
          <Link onClick={() => { window.open(`${ASSIST_API}/assist/admin`, '_blank', 'noreferrer') }}>
            {t('assist.legacyAdmin')}
          </Link>
        </Toolbar>
        {bizErr && <div className="lc-as-mt8"><InlineBanner tone="error">{bizErr}</InlineBanner></div>}
      </Panel>

      {/* data-testid 只给 e2e 用：侧边栏也有「知识库」等同类文案，页签必须限定在本面板内点击，
          否则 Playwright 的 .first() 会命中侧栏菜单并把面板切走（2026-09-21 A3 假失败根因） */}
      <div className="lc-as-mt14" data-testid="assist-tabs">
        <Tabs items={tabs} activeKey={tab} onChange={(k) => setTab(k as Tab)} />
      </div>

      {tab === 'overview' && (
        <Panel title={t('assist.tabOverview')}>
          <div className="lc-as-stats">
            <span>{t('assist.statSessions')}：<b>{sess?.total ?? '—'}</b></span>
            <span>{t('assist.statMessages')}：<b>{sess?.messages ?? '—'}</b></span>
            <span>{t('assist.statUnanswered')}：<b>{sess?.unanswered.length ?? '—'}</b></span>
            <span className="lc-as-inline">
              LLM
              <StatusPill tone={sess?.llm_mode === 'rule' ? 'warn' : 'success'}>
                {sess?.llm_mode === 'env' ? t('assist.modeEnv') : sess?.llm_mode === 'db' ? t('assist.modeDb') : t('assist.modeRule')}
              </StatusPill>
            </span>
          </div>
          {(sess?.unanswered.length ?? 0) > 0 && (
            <div className="lc-as-mt14">
              <div className="lc-as-sub2">{t('assist.unansweredTitle')}</div>
              <ul className="lc-as-list">
                {sess!.unanswered.map((q, i) => <li key={i}>{q}</li>)}
              </ul>
            </div>
          )}
          <div className="lc-as-mt14">
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
        <>
          <Panel title={t('assist.tabConfig')}>
            <div className="lc-as-hint lc-as-hint-mb">{t('assist.llmHint')}</div>
            {LLM_CFG.map((k) => (
              <Field key={k} label={k}>
                <Input
                  className="lc-as-w420" value={cfg[k] ?? ''}
                  type={k === 'llm_api_key' ? 'password' : 'text'} aria-label={k}
                  onChange={(e) => setCfg({ ...cfg, [k]: e.target.value })}
                />
              </Field>
            ))}
            <div className="lc-as-actions">
              <Button variant="primary" size="sm" onClick={() => void saveCfgKeys(LLM_CFG)}>{t('assist.llmSave')}</Button>
              <Button variant="secondary" size="sm" disabled={testing} onClick={() => void testLLM()}>{t('assist.llmTest')}</Button>
              {testOut && <span className="lc-as-hint">{testOut}</span>}
            </div>
            <div className="lc-as-hint lc-as-hint-mb">{t('assist.cfgHint')}</div>
            {CHAT_CFG.map((k) => (
              <Field key={k} label={k}>
                <Textarea
                  className="lc-as-ta" value={cfg[k] ?? ''}
                  aria-label={k} onChange={(e) => setCfg({ ...cfg, [k]: e.target.value })}
                />
              </Field>
            ))}
            <Field label="synonyms">
              <Textarea
                className="lc-as-ta-lg" value={cfg.synonyms ?? ''}
                placeholder={t('assist.synPh')} aria-label="synonyms"
                onChange={(e) => setCfg({ ...cfg, synonyms: e.target.value })}
              />
            </Field>
            <div className="lc-as-note">{t('assist.synHint')}</div>
            <Button variant="primary" size="sm" onClick={() => void saveCfgKeys([...CHAT_CFG, 'synonyms'])}>{t('assist.cfgSave')}</Button>
          </Panel>

          {/* 系统现值（★ 081x 旧管理台差集补齐）：现值注入是「取不到就整段丢掉」的软路径，
              失败时挂件界面与正常一模一样，所以这里必须能现取一次看真相；空＝没取到，不是取到 0。 */}
          <div className="lc-as-mt14">
            <Panel title={t('assist.svTitle')} extra={
              sv ? <StatusPill tone={sv.ok ? 'success' : 'warn'}>{sv.ok ? t('assist.svOk') : t('assist.svMissing')}</StatusPill> : undefined
            }>
              <div className="lc-as-hint lc-as-hint-mb">{t('assist.svHint')}</div>
              <Field label="main_base_url">
                <Input
                  className="lc-as-w420" value={cfg.main_base_url ?? ''}
                  placeholder="http://127.0.0.1:8787" aria-label="main_base_url"
                  onChange={(e) => setCfg({ ...cfg, main_base_url: e.target.value })}
                />
              </Field>
              <div className="lc-as-note">{t('assist.svBaseHint')}</div>
              <div className="lc-as-actions">
                <Button variant="primary" size="sm" onClick={() => void saveMainBase()}>{t('assist.svSaveBase')}</Button>
                <Button variant="secondary" size="sm" disabled={svBusy} onClick={() => void loadSystemValues()}>{t('assist.svFetch')}</Button>
              </div>
              <div className="lc-as-svout" data-testid="assist-sv-out">{svOut}</div>
            </Panel>
          </div>
        </>
      )}

      {tab !== 'overview' && tab !== 'config' && (() => {
        const def = AREAS.find((a) => a.tabKey === tab)!
        return (
          <AreaTable
            def={def}
            rows={rowsMap[def.area] || []}
            onCreate={() => setForm({ def, row: { ...def.defaults, id: 0, key: '', enabled: 1 } })}
            onEdit={(r) => setForm({ def, row: { ...def.defaults, ...r } })}
            onToggle={(r, on) => void toggleRow(def, r, on)}
            onRemove={(r) => void removeRow(def, r)}
          />
        )
      })()}

      {form && (
        <AreaDialog form={form} onChange={setForm} onSave={() => void saveRow()} onCancel={() => setForm(null)} />
      )}
    </div>
  )
}
