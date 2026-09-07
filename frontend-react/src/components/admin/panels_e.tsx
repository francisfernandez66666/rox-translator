// ============================================================================
// components/admin/panels_e.tsx — 运营策略引擎面板
// 计费/模式定价/套餐/推广时间窗/邀请/任务中心/注册/限额/支付/内容因子配置。
// ★ 2026-09 权限收口：运营策略为平台级中台配置，仅超管可设置（后端 scope 恒 platform，
//   租户管理员仅可读）；本面板按 isSuper 决定可编辑性，AdminDashboard 菜单亦限超管。
// ★ 2026-09 运营时间窗 UI 重构：起止用 DateRangePicker（可带时间），
//   并给出可覆盖因子的名称/公式速查，替代裸 JSON 输入框。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, Dialog, Input, Switch, Tag, Textarea, MessagePlugin, DateRangePicker } from 'tdesign-react'
import { Panel, Field, toastResp } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { opsPolicy, opsPolicySave, opsWindowSave, opsPackageReset } from '@/api/ops'

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
  const [now, setNow] = useState('')
  // 推广窗口编辑弹窗
  const [winDlg, setWinDlg] = useState<null | { index: number; id: string; name: string; start: string; end: string; priority: number; tz: string; overrides: string }>(null)

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

  return (
    <>
      <h2 style={{ margin: '4px 0 8px' }}>{t('ops.title')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('ops.hint')}</p>
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
            <Field label={t('ops.promoOverrides')}><Textarea value={winDlg.overrides} onChange={(v) => setWinDlg({ ...winDlg, overrides: v as string })} /></Field>
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
