// ============================================================================
// components/admin/panels_e.tsx — 运营策略引擎面板
// 计费/模式定价/套餐/推广时间窗/邀请/任务中心/注册/限额/支付/内容因子配置。
// ★ 2026-09 权限收口：运营策略为平台级中台配置，仅超管可设置（后端 scope 恒 platform，
//   租户管理员仅可读）；本面板按 isSuper 决定可编辑性，AdminDashboard 菜单亦限超管。
// ★ 2026-09 运营时间窗 UI 重构：起止用 DateRangePicker（可带时间），
//   并给出可覆盖因子的名称/公式速查，替代裸 JSON 输入框。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, Dialog, Input, Select, Switch, Tag, Textarea, MessagePlugin, DateRangePicker } from 'tdesign-react'
import { Panel, Field, toastResp } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { opsPolicy, opsSlo, opsRoutes, opsPolicySave, opsWindowSave, opsPackageReset } from '@/api/ops'

// 数字输入小件：Input 数值化（tdesign Input onChange 返回字符串）
function NumInput({ value, onChange, style, disabled }: { value: number; onChange: (n: number) => void; style?: React.CSSProperties; disabled?: boolean }) {
  return (
    <Input value={String(value ?? 0)} style={{ width: 110, ...style }} disabled={disabled}
      onChange={(v) => { const n = Number(v); onChange(Number.isFinite(n) ? n : 0) }} />
  )
}

// 推广窗口内可覆盖因子速查：因子名（JSON 路径）/ 公式 / 示例值
const OVERRIDE_FACTORS: { factor: string; formula: string }[] = [
  { factor: 'billing.enforced', formula: 'true | false（强制计费总开关）' },
  { factor: 'billing.markup_multiplier', formula: '≥1 的成本系数，0=沿用全局' },
  { factor: 'billing.mode_rules.{fast|pro}.enabled', formula: 'true | false（模式启用）' },
  { factor: 'billing.mode_rules.{fast|pro}.charge', formula: 'true | false（false=推广期免费）' },
  { factor: 'billing.mode_rules.{fast|pro}.markup', formula: '模式成本系数，0=沿用全局' },
  { factor: 'billing.mode_rules.{fast|pro}.limit_chars', formula: '单次输入上限(字符)，0=不限' },
  { factor: 'package.trial_tokens', formula: '体验 token（注册发放）' },
  { factor: 'package.trial_days', formula: '体验时长(天)' },
  { factor: 'package.monthly_reset_enabled', formula: 'true | false（月度用量重置）' },
  { factor: 'package.monthly_reset_limit', formula: '每月重置次数上限' },
  { factor: 'invite.enabled', formula: 'true | false（邀请奖励总开关）' },
  { factor: 'invite.reward_tokens', formula: '注册邀请奖励 token' },
  { factor: 'invite.reward_days', formula: '奖励有效期(天)' },
  { factor: 'invite.paid_reward_tokens', formula: '付费邀请奖励 token（多邀多得）' },
  { factor: 'invite.paid_reward_days', formula: '付费奖励有效期(天)，0=永久' },
  { factor: 'invite.max_daily_rewards', formula: '单日发奖上限' },
  { factor: 'registration.enabled', formula: 'true | false（注册开关）' },
  { factor: 'registration.ip_min_interval_sec', formula: '同 IP 间隔(秒)' },
  { factor: 'registration.ip_daily_limit', formula: '同 IP 日上限' },
  { factor: 'limits.max_qps / max_concurrent', formula: '全局 QPS / 并发上限' },
  { factor: 'limits.default_max_daily_chars', formula: '新租户日字符上限' },
  { factor: 'limits.default_max_daily_tokens', formula: '新租户日 token 上限' },
  { factor: 'payment.mode', formula: 'mock | wechat | alipay | static_qr' },
  { factor: 'payment.auto_charge', formula: 'true | false（下单即到账）' },
  { factor: 'content.file_max_mb', formula: '文件翻译上限(MB)' },
  { factor: 'task.enabled', formula: 'true | false（任务中心奖励总开关）' },
]

/** 运营策略引擎面板 */
export function OpsP() {
  const [, t] = useT()
  const { isSuper, activeTenantId } = useAdmin()
  // 策略草稿（本地编辑；2026-09 起仅超管平台级可写）
  const [pol, setPol] = useState<Record<string, any>>({ billing: { mode_rules: {} }, package: {}, invite: {}, registration: {}, limits: {}, payment: {}, content: {}, task: {} })
  const [windows, setWindows] = useState<any[]>([])
  const [, setNow] = useState('') // ★ E14
  // 推广窗口编辑弹窗
  const [winDlg, setWinDlg] = useState<null | { index: number; id: string; name: string; start: string; end: string; priority: number; tz: string; overrides: string }>(null)
  // ★ H11 SLO 状态卡片数据
  const [slo, setSlo] = useState<any[]>([])
  const [routes, setRoutes] = useState<any | null>(null)

  const load = useCallback(async () => {
    try {
      const r = await opsPolicy() as any
      if (r.success) {
        const eff = r.effective || {}
        // 以「当前生效」策略为底初始化草稿（平台级全量保存回写）
        setPol({
          tz: eff.tz || 'Asia/Shanghai',
          billing: {
            enforced: eff.enforced,
            markup_multiplier: eff.markup_multiplier,
            mode_rules: eff.mode_rules || {},
          },
          package: eff.package || {},
          invite: eff.invite || {},
          registration: eff.registration || {},
          limits: eff.limits || {},
          payment: eff.payment || {},
          content: eff.content || {},
          task: eff.task || {},
        })
        setWindows(r.windows || [])
        try {
          const sr = await opsSlo() as any
          if (sr.success) setSlo(sr.slos || [])
        } catch { /* ignore */ }
        try {
          const rr = await opsRoutes() as any
          if (rr.success) setRoutes(rr)
        } catch { /* ignore */ }
        setNow(r.now || '')
      }
    } catch { /* ignore */ }
  }, [])

  useEffect(() => { void load() }, [activeTenantId, load])

  // —— 草稿 setter：把某因子域的局部补丁并入 pol 草稿（不可变更新）——
  const setMode = (m: string, patch: Record<string, any>) => {
    setPol((p) => ({ ...p, billing: { ...p.billing, mode_rules: { ...p.billing.mode_rules, [m]: { ...p.billing.mode_rules[m], ...patch } } } }))
  }
  const setPkg = (patch: Record<string, any>) => setPol((p) => ({ ...p, package: { ...p.package, ...patch } }))
  const setInvite = (patch: Record<string, any>) => setPol((p) => ({ ...p, invite: { ...p.invite, ...patch } }))
  const setReg = (patch: Record<string, any>) => setPol((p) => ({ ...p, registration: { ...p.registration, ...patch } }))
  const setLimits = (patch: Record<string, any>) => setPol((p) => ({ ...p, limits: { ...p.limits, ...patch } }))
  const setPay = (patch: Record<string, any>) => setPol((p) => ({ ...p, payment: { ...p.payment, ...patch } }))
  const setContent = (patch: Record<string, any>) => setPol((p) => ({ ...p, content: { ...p.content, ...patch } }))
  const setTask = (patch: Record<string, any>) => setPol((p) => ({ ...p, task: { ...p.task, ...patch } }))

  // 保存当前草稿（平台级，仅超管）；成功即重新加载生效策略
  const save = async () => {
    if (!isSuper) return
    if (toastResp(await opsPolicySave('platform', pol), t('ops.saved'))) void load()
  }
  // 套餐月度重置（消耗租户重置次数，后台二次确认后调用）
  const doReset = async () => {
    try {
      const r = await opsPackageReset() as any
      if (r.success) {
        void MessagePlugin.success(`${t('ops.pkgResetDone')}（${r.remaining}）`)
        void load()
      } else void MessagePlugin.error(r.message || '')
    } catch { /* ignore */ }
  }
  // 保存推广窗口（校验 overrides JSON，合法后提交并刷新窗口列表）
  const saveWindow = async () => {
    if (!winDlg) return
    let overrides: any = {}
    try { overrides = winDlg.overrides ? JSON.parse(winDlg.overrides) : {} } catch { void MessagePlugin.error('overrides JSON 非法'); return }
    const ok = toastResp(await opsWindowSave({
      id: winDlg.id || `win_${Date.now()}`, name: winDlg.name, start: winDlg.start, end: winDlg.end,
      priority: Number(winDlg.priority) || 0, tz: winDlg.tz || '', overrides,
    }), t('ops.promoSaved'))
    setWinDlg(null)
    if (ok) void load()
  }

  // 便捷读取：某模式的草稿因子
  const mode = (m: string) => (pol.billing.mode_rules || {})[m] || {}

  // ★ F9：时间窗覆盖因子表单化编辑器——表单与原始 JSON 双向同步（单一数据源=winDlg.overrides，
  //   B6 白名单由服务端 ValidateWindowOverrides 兜底；表单外键保留不删除）
  type OvField = { path: string; kind: 'num' | 'bool'; label: string }
  const OV_FIELDS: OvField[] = [
    { path: 'billing.markup_multiplier', kind: 'num', label: t('ops.foMarkupGlobal') },
    { path: 'billing.mode_rules.fast.markup', kind: 'num', label: t('ops.foFastMarkup') },
    { path: 'billing.mode_rules.fast.limit_chars', kind: 'num', label: t('ops.foFastChars') },
    { path: 'billing.mode_rules.pro.markup', kind: 'num', label: t('ops.foProMarkup') },
    { path: 'billing.mode_rules.pro.limit_chars', kind: 'num', label: t('ops.foProChars') },
    { path: 'invite.enabled', kind: 'bool', label: t('ops.foInviteEnabled') },
    { path: 'invite.reward_tokens', kind: 'num', label: t('ops.foRewardTokens') },
    { path: 'invite.reward_days', kind: 'num', label: t('ops.foRewardDays') },
    { path: 'invite.paid_reward_tokens', kind: 'num', label: t('ops.foPaidTokens') },
    { path: 'invite.paid_reward_days', kind: 'num', label: t('ops.foPaidDays') },
    { path: 'invite.max_daily_rewards', kind: 'num', label: t('ops.foMaxDaily') },
    { path: 'limits.max_qps', kind: 'num', label: t('ops.foQps') },
    { path: 'limits.max_concurrent', kind: 'num', label: t('ops.foConc') },
    { path: 'limits.default_max_daily_chars', kind: 'num', label: t('ops.foDailyChars') },
    { path: 'limits.default_max_daily_tokens', kind: 'num', label: t('ops.foDailyTokens') },
  ]
  const ovParse = (): Record<string, any> => { try { return JSON.parse(winDlg?.overrides || '{}') || {} } catch { return {} } }
  const ovGet = (o: Record<string, any>, path: string): string => {
    const v = path.split('.').reduce<any>((a, k) => (a == null ? a : a[k]), o)
    if (v == null) return ''
    if (typeof v === 'boolean') return v ? '1' : '0'
    return String(v)
  }
  const ovDeletePrune = (o: Record<string, any>, path: string) => {
    const keys = path.split('.')
    const parents: Record<string, any>[] = [o]
    for (let i = 0; i < keys.length - 1; i++) { parents.push(parents[i]?.[keys[i]]); }
    let cur = o; let ok = true
    for (let i = 0; i < keys.length - 1; i++) { if (cur && typeof cur === 'object' && keys[i] in cur) { cur = cur[keys[i]]; } else { ok = false; break } }
    if (ok && cur && typeof cur === 'object') delete cur[keys[keys.length - 1]]
    // 自底向上剪空对象，避免留下 "billing": {} 之类空壳
    for (let i = parents.length - 2; i >= 0; i--) {
      const child = parents[i + 1]
      const p = parents[i]
      if (child && typeof child === 'object' && Object.keys(child).length === 0 && p && typeof p === 'object') {
        const k = keys[i]; if (p[k] === child) delete p[k]
      }
    }
  }
  const ovPatch = (field: OvField, raw: string) => {
    if (!winDlg) return
    const o = ovParse()
    if (raw === '') { ovDeletePrune(o, field.path) }
    else {
      const keys = field.path.split('.')
      let cur = o
      for (let i = 0; i < keys.length - 1; i++) {
        if (typeof cur[keys[i]] !== 'object' || cur[keys[i]] == null) cur[keys[i]] = {}
        cur = cur[keys[i]]
      }
      cur[keys[keys.length - 1]] = field.kind === 'bool' ? raw === '1' : (Number(raw) as number)
    }
    setWinDlg({ ...winDlg, overrides: JSON.stringify(o) })
  }
  const ovHasExtra = (() => {
    const o = ovParse()
    const leafPaths = new Set(OV_FIELDS.map((f) => f.path))
    const walk = (v: any, pre: string): boolean => {
      if (v == null || typeof v !== 'object' || Array.isArray(v)) return pre !== '' && !leafPaths.has(pre)
      return Object.keys(v).some((k) => walk(v[k], pre ? `${pre}.${k}` : k))
    }
    return walk(o, '')
  })()

  return (
    <>
      <h2 style={{ margin: '4px 0 8px' }}>{t('ops.title')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('ops.hint')}</p>
      {isSuper && routes && (routes.routes || []).length > 0 && (
        <div style={{ margin: '0 0 12px', fontSize: 12, color: '#556' }}>
          <span style={{ marginRight: 8 }}>{`路由实时统计（动态权重 ${routes.dynamic_routing ? '开' : '关'} / 竞速 ${routes.hedge_enabled ? '开' : '关'}）`}</span>
          {(routes.routes || []).map((x: any) => (
            <Tag key={x.route} theme={x.err_rate > 0.2 ? 'danger' : x.err_rate > 0.05 ? 'warning' : 'success'} variant="light" style={{ marginRight: 6 }}>
              {String(x.route).split('|').pop()} P50 {Math.round(x.p50_ms)}ms · P95 {Math.round(x.p95_ms)}ms · 错误 {(x.err_rate * 100).toFixed(1)}% · tok/次 {Math.round(x.tokens_per_call)}
            </Tag>
          ))}
        </div>
      )}
      {isSuper && slo.length > 0 && (
        <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', margin: '0 0 12px' }}>
          {slo.map((x: any) => {
            const burn = Number(x.burn_1h || 0)
            const lv = burn >= 2 ? 'danger' : burn >= 1 ? 'warning' : 'success'
            return (
              <Tag key={x.key} theme={lv as any} variant="outline" title={`1h burn=${x.burn_1h} 6h burn=${x.burn_6h}${x.budget_left_pct != null ? ` 预算剩余 ${x.budget_left_pct}%` : ''}`}>
                SLO {x.name} {x.target}{x.key === 'latency_p99' ? 'ms' : '%'} · 燃烧率 {burn}
              </Tag>
            )
          })}
        </div>
      )}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12, flexWrap: 'wrap' }}>
        <Tag theme="primary" variant="outline">{t('ops.platformScope')}</Tag>
        {windows.filter((w) => w.active).map((w) => (
          <Tag key={w.id} theme="success">{t('ops.effectiveTag')}: {w.name || w.id}</Tag>
        ))}
        <div style={{ flex: 1 }} />
        <Button variant="outline" onClick={() => void load()}>↻</Button>
        <Button theme="primary" disabled={!isSuper} onClick={() => void save()}>{t('ops.save')}</Button>
      </div>
      {!isSuper && (
        <p style={{ fontSize: 12, color: '#ad6800', margin: '0 0 12px', background: '#fff7e6', border: '1px solid #ffd591', borderRadius: 6, padding: '6px 10px' }}>{t('ops.superOnlyHint')}</p>
      )}

      {/* 模式定价因子 */}
      <Panel title={t('ops.modeTitle')} extra={<span style={{ fontSize: 12, color: '#889' }}>{t('ops.modeHint')}</span>}>
        {['fast', 'pro'].map((m) => (
          <div key={m} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0', flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
            <b style={{ width: 110 }}>{m === 'fast' ? t('ops.modeFast') : t('ops.modePro')}</b>
            <label>{t('ops.modeEnabled')}<Switch disabled={!isSuper} value={!!mode(m).enabled} onChange={(v: any) => setMode(m, { enabled: !!v })} /></label>
            <label>{t('ops.modeCharge')}<Switch disabled={!isSuper} value={!!mode(m).charge} onChange={(v: any) => setMode(m, { charge: !!v })} /></label>
            {!mode(m).charge && <Tag theme="warning">{t('ops.modeFreeTag')}</Tag>}
            <label>{t('ops.modeMarkup')}<NumInput disabled={!isSuper} value={mode(m).markup || 0} onChange={(n) => setMode(m, { markup: n })} /></label>
            <label>{t('ops.modeLimitChars')}<NumInput disabled={!isSuper} value={mode(m).limit_chars || 0} onChange={(n) => setMode(m, { limit_chars: n })} /></label>
          </div>
        ))}
      </Panel>

      {/* 推广期时间窗（★ 2026-09：DateRangePicker + 因子名称/公式速查） */}
      <Panel title={t('ops.promoTitle')} extra={
        <Button variant="outline" size="small" disabled={!isSuper} onClick={() => setWinDlg({ index: -1, id: '', name: '', start: '', end: '', priority: 0, tz: '', overrides: '{}' })}>{t('ops.promoAdd')}</Button>
      }>
        <p style={{ fontSize: 12, color: '#889', margin: '0 0 8px' }}>{t('ops.promoHint')}</p>
        {windows.map((w, i) => (
          <div key={w.id || i} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0', flexWrap: 'wrap' }}>
            <code style={{ fontSize: 12 }}>{w.id}</code>
            <span style={{ width: 120 }}>{w.name || '-'}</span>
            <span style={{ fontSize: 12, color: '#667' }}>{w.start} ~ {w.end}</span>
            <Tag variant="outline">p={w.priority}</Tag>
            {w.active && <Tag theme="success">{t('ops.promoActive')}</Tag>}
            <Button size="small" variant="outline" disabled={!isSuper} onClick={() => setWinDlg({ index: i, id: w.id, name: w.name, start: w.start, end: w.end, priority: w.priority, tz: w.tz || '', overrides: JSON.stringify(w.overrides || {}) })}>{t('ops.promoEdit')}</Button>
          </div>
        ))}
      </Panel>

      {/* 套餐因子 */}
      <Panel title={t('ops.pkgTitle')} extra={
        <Button variant="outline" size="small" disabled={!isSuper} onClick={() => { if (window.confirm(t('ops.pkgResetConfirm'))) void doReset() }}>{t('ops.pkgResetBtn')}</Button>
      }>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.pkgTrialTokens')}><NumInput disabled={!isSuper} value={pol.package.trial_tokens || 0} onChange={(n) => setPkg({ trial_tokens: n })} /></Field>
          <Field label={t('ops.pkgTrialDays')}><NumInput disabled={!isSuper} value={pol.package.trial_days || 0} onChange={(n) => setPkg({ trial_days: n })} /></Field>
          <Field label={t('ops.pkgResetEnabled')}><Switch disabled={!isSuper} value={!!pol.package.monthly_reset_enabled} onChange={(v: any) => setPkg({ monthly_reset_enabled: !!v })} /></Field>
          <Field label={t('ops.pkgResetLimit')}><NumInput disabled={!isSuper} value={pol.package.monthly_reset_limit || 0} onChange={(n) => setPkg({ monthly_reset_limit: n })} /></Field>
        </div>
      </Panel>

      {/* 邀请奖励因子（总开关 = 前台「邀请好友」入口是否生效） */}
      <Panel title={t('ops.inviteTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.inviteEnabled')}><Switch disabled={!isSuper} value={!!pol.invite.enabled} onChange={(v: any) => setInvite({ enabled: !!v })} /></Field>
          <Field label={t('ops.inviteTokens')}><NumInput disabled={!isSuper} value={pol.invite.reward_tokens || 0} onChange={(n) => setInvite({ reward_tokens: n })} /></Field>
          <Field label={t('ops.inviteDays')}><NumInput disabled={!isSuper} value={pol.invite.reward_days || 0} onChange={(n) => setInvite({ reward_days: n })} /></Field>
          <Field label={t('ops.invitePaidTokens')}><NumInput disabled={!isSuper} value={pol.invite.paid_reward_tokens || 0} onChange={(n) => setInvite({ paid_reward_tokens: n })} /></Field>
          <Field label={t('ops.invitePaidDays')}><NumInput disabled={!isSuper} value={pol.invite.paid_reward_days || 0} onChange={(n) => setInvite({ paid_reward_days: n })} /></Field>
          <Field label={t('ops.inviteMaxDaily')}><NumInput disabled={!isSuper} value={pol.invite.max_daily_rewards || 0} onChange={(n) => setInvite({ max_daily_rewards: n })} /></Field>
        </div>
      </Panel>

      {/* 任务中心奖励因子（★ 2026-09 新增总开关） */}
      <Panel title={t('ops.taskTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', alignItems: 'center', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.taskEnabled')}><Switch disabled={!isSuper} value={!!pol.task.enabled} onChange={(v: any) => setTask({ enabled: !!v })} /></Field>
          <span style={{ fontSize: 12, color: '#889', maxWidth: 420, lineHeight: 1.7 }}>{t('ops.taskHint')}</span>
        </div>
      </Panel>

      {/* 注册 / 限额 / 支付 / 内容因子 */}
      <Panel title={t('ops.regTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.regEnabled')}><Switch disabled={!isSuper} value={!!pol.registration.enabled} onChange={(v: any) => setReg({ enabled: !!v })} /></Field>
          <Field label={t('ops.regIpInterval')}><NumInput disabled={!isSuper} value={pol.registration.ip_min_interval_sec || 0} onChange={(n) => setReg({ ip_min_interval_sec: n })} /></Field>
          <Field label={t('ops.regIpDaily')}><NumInput disabled={!isSuper} value={pol.registration.ip_daily_limit || 0} onChange={(n) => setReg({ ip_daily_limit: n })} /></Field>
          <Field label={t('ops.regEmailVerify')}><Switch disabled={!isSuper} value={!!pol.registration.email_verify_enabled} onChange={(v: any) => setReg({ email_verify_enabled: !!v })} /></Field>
        </div>
      </Panel>

      <Panel title={t('ops.limitsTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.limitsQps')}><NumInput disabled={!isSuper} value={pol.limits.max_qps || 0} onChange={(n) => setLimits({ max_qps: n })} /></Field>
          <Field label={t('ops.limitsConcurrent')}><NumInput disabled={!isSuper} value={pol.limits.max_concurrent || 0} onChange={(n) => setLimits({ max_concurrent: n })} /></Field>
          <Field label={t('ops.limitsDailyChars')}><NumInput disabled={!isSuper} value={pol.limits.default_max_daily_chars || 0} onChange={(n) => setLimits({ default_max_daily_chars: n })} /></Field>
          <Field label={t('ops.limitsDailyTokens')}><NumInput disabled={!isSuper} value={pol.limits.default_max_daily_tokens || 0} onChange={(n) => setLimits({ default_max_daily_tokens: n })} /></Field>
        </div>
      </Panel>

      <Panel title={t('ops.payTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.payMode')}>
            <select disabled={!isSuper} style={{ height: 30, minWidth: 120 }} value={pol.payment.mode || ''} onChange={(e) => setPay({ mode: e.target.value })}>
              <option value="">-</option><option value="mock">mock</option><option value="wechat">wechat</option><option value="alipay">alipay</option><option value="static_qr">static_qr</option>
            </select>
          </Field>
          <Field label={t('ops.payAutoCharge')}><Switch disabled={!isSuper} value={!!pol.payment.auto_charge} onChange={(v: any) => setPay({ auto_charge: !!v })} /></Field>
        </div>
      </Panel>

      <Panel title={t('ops.contentTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isSuper ? 1 : 0.55 }}>
          <Field label={t('ops.fileMaxMb')}><NumInput disabled={!isSuper} value={pol.content.file_max_mb || 0} onChange={(n) => setContent({ file_max_mb: n })} /></Field>
        </div>
      </Panel>

      {/* 推广窗口编辑弹窗 */}
      <Dialog header={t('ops.promoEditTitle')} visible={!!winDlg} style={{ width: 560 }}
        onClose={() => setWinDlg(null)}
        footer={
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Button variant="outline" onClick={() => setWinDlg(null)}>Cancel</Button>
            <Button theme="primary" onClick={() => void saveWindow()}>{t('ops.promoSave')}</Button>
          </div>
        }>
        {winDlg && (
          <div style={{ display: 'grid', gap: 10 }}>
            <Field label="ID"><Input value={winDlg.id} onChange={(v) => setWinDlg({ ...winDlg, id: v as string })} /></Field>
            <Field label={t('ops.promoName')}><Input value={winDlg.name} onChange={(v) => setWinDlg({ ...winDlg, name: v as string })} /></Field>
            <Field label={t('ops.promoRange')}>
              <DateRangePicker
                mode="date"
                enableTimePicker
                valueType="YYYY-MM-DD HH:mm"
                clearable
                allowInput
                style={{ width: '100%' }}
                value={winDlg.start && winDlg.end ? [winDlg.start, winDlg.end] : []}
                onChange={(v) => {
                  const arr = (Array.isArray(v) ? v : []) as (string | Date)[]
                  setWinDlg({ ...winDlg, start: arr[0] ? String(arr[0]).slice(0, 16) : '', end: arr[1] ? String(arr[1]).slice(0, 16) : '' })
                }}
                placeholder={[t('ops.promoStart'), t('ops.promoEnd')]}
              />
            </Field>
            <Field label={t('ops.promoPriority')}><NumInput value={winDlg.priority} onChange={(n) => setWinDlg({ ...winDlg, priority: n })} /></Field>
            <Field label={t('ops.foTitle')}>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 6, border: '1px solid #e3e6ef', borderRadius: 8, padding: 8 }}>
                {OV_FIELDS.map((f) => (
                  <label key={f.path} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
                    <span style={{ minWidth: 118, color: '#556' }}>{f.label}</span>
                    {f.kind === 'bool' ? (
                      <Select size="small" style={{ width: 90 }} value={ovGet(ovParse(), f.path)}
                              onChange={(v: any) => ovPatch(f, String(v ?? ''))}
                              options={[{ value: '', label: '—' }, { value: '1', label: t('ops.foOn') }, { value: '0', label: t('ops.foOff') }]} />
                    ) : (
                      <Input size="small" type="number" style={{ width: 110 }} value={ovGet(ovParse(), f.path)} placeholder="—"
                             onChange={(v: any) => ovPatch(f, String(v ?? ''))} />
                    )}
                  </label>
                ))}
              </div>
            </Field>
            <details>
              <summary style={{ fontSize: 12, color: '#889', cursor: 'pointer' }}>{t('ops.foRaw')}</summary>
              {ovHasExtra && <p style={{ fontSize: 12, color: '#c66900', margin: '4px 0' }}>{t('ops.foExtraKeys')}</p>}
              <Textarea value={winDlg.overrides} onChange={(v) => setWinDlg({ ...winDlg, overrides: v as string })} />
            </details>
            <p style={{ fontSize: 12, color: '#889', margin: 0 }}>{t('ops.promoOverridesHint')}</p>
            <code style={{ fontSize: 11, color: '#5b6270', background: '#f4f6fa', borderRadius: 6, padding: '6px 8px', wordBreak: 'break-all' }}>{t('ops.promoOverridesExample')}</code>
            <div style={{ borderTop: '1px dashed #dbe0ea', paddingTop: 10 }}>
              <div style={{ fontWeight: 600, fontSize: 13, color: '#455a64', marginBottom: 6 }}>{t('ops.promoFactorsTitle')}</div>
              <div style={{ maxHeight: 220, overflow: 'auto', border: '1px solid #e3e6ef', borderRadius: 8 }}>
                {OVERRIDE_FACTORS.map((f) => (
                  <div key={f.factor} style={{ display: 'flex', gap: 8, padding: '5px 10px', fontSize: 12, borderBottom: '1px solid #f0f2f7' }}>
                    <code style={{ color: 'var(--td-brand-color-active, #1f33d6)', minWidth: 240, flexShrink: 0 }}>{f.factor}</code>
                    <span style={{ color: '#667' }}>{f.formula}</span>
                  </div>
                ))}
              </div>
            </div>
          </div>
        )}
      </Dialog>
    </>
  )
}
