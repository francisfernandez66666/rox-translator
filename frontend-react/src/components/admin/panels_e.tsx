// ============================================================================
// components/admin/panels_e.tsx — 运营策略引擎面板
// 计费/模式定价/套餐/推广时间窗/邀请奖励/注册/限额/支付/内容因子配置。
// 与 WorkflowP（panels_d.tsx）同范式：加载 → 本地编辑 → 提交。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, Dialog, Input, Switch, Tag, MessagePlugin } from 'tdesign-react'
import { Panel, Field, toastResp } from './parts'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { opsPolicy, opsPolicySave, opsWindowSave, opsPackageReset } from '@/api/ops'

// 数字输入小件：Input 数值化（tdesign Input onChange 返回字符串）
function NumInput({ value, onChange, style }: { value: number; onChange: (n: number) => void; style?: React.CSSProperties }) {
  return (
    <Input value={String(value ?? 0)} style={{ width: 110, ...style }}
      onChange={(v) => { const n = Number(v); onChange(Number.isFinite(n) ? n : 0) }} />
  )
}

/** 运营策略引擎面板 */
export function OpsP() {
  const [, t] = useT()
  const { activeTenantId } = useAdmin()
  // 策略草稿（本地编辑）
  const [pol, setPol] = useState<Record<string, any>>({ billing: { mode_rules: {} }, package: {}, invite: {}, registration: {}, limits: {}, payment: {}, content: {} })
  const [scope, setScope] = useState<'platform' | 'tenant'>('tenant')
  const [windows, setWindows] = useState<any[]>([])
  const [now, setNow] = useState('')
  // 推广窗口编辑弹窗
  const [winDlg, setWinDlg] = useState<null | { index: number; id: string; name: string; start: string; end: string; priority: number; tz: string; overrides: string }>(null)

  const load = useCallback(async () => {
    try {
      const r = await opsPolicy() as any
      if (r.success) {
        const eff = r.effective || {}
        // 以「当前生效」策略为底初始化草稿（全量保存回写，后端按 scope 裁剪）
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
        })
        setWindows(r.windows || [])
        setNow(r.now || '')
        // 作用域：平台上下文（超管）→ platform；否则租户级
        setScope((r.super && !(r.tenant_id > 0)) ? 'platform' : 'tenant')
      }
    } catch { /* ignore */ }
  }, [])

  useEffect(() => { void load() }, [activeTenantId, load])

  // —— 草稿 setter：把某因子域的局部补丁并入 pol 草稿（不可变更新，供各 Panel 输入控件调用）——
  const setMode = (m: string, patch: Record<string, any>) => {
    setPol((p) => ({ ...p, billing: { ...p.billing, mode_rules: { ...p.billing.mode_rules, [m]: { ...p.billing.mode_rules[m], ...patch } } } }))
  }
  const setPkg = (patch: Record<string, any>) => setPol((p) => ({ ...p, package: { ...p.package, ...patch } }))
  const setInvite = (patch: Record<string, any>) => setPol((p) => ({ ...p, invite: { ...p.invite, ...patch } }))
  const setReg = (patch: Record<string, any>) => setPol((p) => ({ ...p, registration: { ...p.registration, ...patch } }))
  const setLimits = (patch: Record<string, any>) => setPol((p) => ({ ...p, limits: { ...p.limits, ...patch } }))
  const setPay = (patch: Record<string, any>) => setPol((p) => ({ ...p, payment: { ...p.payment, ...patch } }))
  const setContent = (patch: Record<string, any>) => setPol((p) => ({ ...p, content: { ...p.content, ...patch } }))

  // 保存当前草稿（按作用域：超管平台级 / 租户级），成功即重新加载生效策略
  const save = async () => {
    if (toastResp(await opsPolicySave(scope, pol), t('ops.saved'))) void load()
  }
  // 租户级「恢复默认」：向租户覆盖写空对象，回落平台/内置默认
  const resetTenant = async () => {
    if (toastResp(await opsPolicySave('tenant', {}), t('ops.resetDone'))) void load()
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

  // 便捷读取：某模式的草稿因子 / 当前是否平台作用域（决定可配范围与只读灰显）
  const mode = (m: string) => (pol.billing.mode_rules || {})[m] || {}
  const isPlatform = scope === 'platform'

  return (
    <>
      <h2 style={{ margin: '4px 0 8px' }}>{t('ops.title')}</h2>
      <p style={{ fontSize: 13, color: '#667', margin: '0 0 12px' }}>{t('ops.hint')}</p>
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
        <Tag theme="primary" variant="outline">{isPlatform ? t('ops.platformScope') : t('ops.tenantScope')}</Tag>
        {windows.filter((w) => w.active).map((w) => (
          <Tag key={w.id} theme="success">{t('ops.effectiveTag')}: {w.name || w.id}</Tag>
        ))}
        <div style={{ flex: 1 }} />
        <Button variant="outline" onClick={() => void load()}>↻</Button>
        {!isPlatform && <Button variant="outline" onClick={() => void resetTenant()}>{t('ops.resetToDefault')}</Button>}
        <Button theme="primary" onClick={() => void save()}>{t('ops.save')}</Button>
      </div>

      {/* 模式定价因子 */}
      <Panel title={t('ops.modeTitle')} extra={<span style={{ fontSize: 12, color: '#889' }}>{t('ops.modeHint')}</span>}>
        {['fast', 'pro'].map((m) => (
          <div key={m} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0', flexWrap: 'wrap' }}>
            <b style={{ width: 110 }}>{m === 'fast' ? t('ops.modeFast') : t('ops.modePro')}</b>
            <label>{t('ops.modeEnabled')}<Switch value={!!mode(m).enabled} onChange={(v: any) => setMode(m, { enabled: !!v })} /></label>
            <label>{t('ops.modeCharge')}<Switch value={!!mode(m).charge} onChange={(v: any) => setMode(m, { charge: !!v })} /></label>
            {!mode(m).charge && <Tag theme="warning">{t('ops.modeFreeTag')}</Tag>}
            <label>{t('ops.modeMarkup')}<NumInput value={mode(m).markup || 0} onChange={(n) => setMode(m, { markup: n })} /></label>
            <label>{t('ops.modeLimitChars')}<NumInput value={mode(m).limit_chars || 0} onChange={(n) => setMode(m, { limit_chars: n })} /></label>
          </div>
        ))}
      </Panel>

      {/* 推广期时间窗 */}
      <Panel title={t('ops.promoTitle')} extra={
        <Button variant="outline" size="small" onClick={() => setWinDlg({ index: -1, id: '', name: '', start: '', end: '', priority: 0, tz: '', overrides: '{}' })}>{t('ops.promoAdd')}</Button>
      }>
        <p style={{ fontSize: 12, color: '#889', margin: '0 0 8px' }}>{t('ops.promoHint')}</p>
        {windows.map((w, i) => (
          <div key={w.id || i} style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '6px 0', flexWrap: 'wrap' }}>
            <code style={{ fontSize: 12 }}>{w.id}</code>
            <span style={{ width: 120 }}>{w.name || '-'}</span>
            <span style={{ fontSize: 12, color: '#667' }}>{w.start} ~ {w.end}</span>
            <Tag variant="outline">p={w.priority}</Tag>
            {w.active && <Tag theme="success">{t('ops.promoActive')}</Tag>}
            <Button size="small" variant="outline" onClick={() => setWinDlg({ index: i, id: w.id, name: w.name, start: w.start, end: w.end, priority: w.priority, tz: w.tz || '', overrides: JSON.stringify(w.overrides || {}) })}>{t('ops.promoEdit')}</Button>
          </div>
        ))}
      </Panel>

      {/* 套餐因子 */}
      <Panel title={t('ops.pkgTitle')} extra={
        <Button variant="outline" size="small" onClick={() => { if (window.confirm(t('ops.pkgResetConfirm'))) void doReset() }}>{t('ops.pkgResetBtn')}</Button>
      }>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
          <Field label={t('ops.pkgTrialTokens')}><NumInput value={pol.package.trial_tokens || 0} onChange={(n) => setPkg({ trial_tokens: n })} /></Field>
          <Field label={t('ops.pkgTrialDays')}><NumInput value={pol.package.trial_days || 0} onChange={(n) => setPkg({ trial_days: n })} /></Field>
          <Field label={t('ops.pkgResetEnabled')}><Switch value={!!pol.package.monthly_reset_enabled} onChange={(v: any) => setPkg({ monthly_reset_enabled: !!v })} /></Field>
          <Field label={t('ops.pkgResetLimit')}><NumInput value={pol.package.monthly_reset_limit || 0} onChange={(n) => setPkg({ monthly_reset_limit: n })} /></Field>
        </div>
      </Panel>

      {/* 邀请奖励因子 */}
      <Panel title={t('ops.inviteTitle')}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
          <Field label={t('ops.inviteEnabled')}><Switch value={!!pol.invite.enabled} onChange={(v: any) => setInvite({ enabled: !!v })} /></Field>
          <Field label={t('ops.inviteTokens')}><NumInput value={pol.invite.reward_tokens || 0} onChange={(n) => setInvite({ reward_tokens: n })} /></Field>
          <Field label={t('ops.inviteDays')}><NumInput value={pol.invite.reward_days || 0} onChange={(n) => setInvite({ reward_days: n })} /></Field>
          <Field label={t('ops.invitePaidTokens')}><NumInput value={pol.invite.paid_reward_tokens || 0} onChange={(n) => setInvite({ paid_reward_tokens: n })} /></Field>
          <Field label={t('ops.invitePaidDays')}><NumInput value={pol.invite.paid_reward_days || 0} onChange={(n) => setInvite({ paid_reward_days: n })} /></Field>
          <Field label={t('ops.inviteMaxDaily')}><NumInput value={pol.invite.max_daily_rewards || 0} onChange={(n) => setInvite({ max_daily_rewards: n })} /></Field>
        </div>
      </Panel>

      {/* 注册 / 限额 / 支付 / 内容因子（租户只读，超管平台级可配） */}
      <Panel title={t('ops.regTitle')} extra={!isPlatform ? <span style={{ fontSize: 12, color: '#889' }}>{t('ops.platformOnly')}</span> : null}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isPlatform ? 1 : 0.55 }}>
          <Field label={t('ops.regEnabled')}><Switch value={!!pol.registration.enabled} onChange={(v: any) => setReg({ enabled: !!v })} /></Field>
          <Field label={t('ops.regIpInterval')}><NumInput value={pol.registration.ip_min_interval_sec || 0} onChange={(n) => setReg({ ip_min_interval_sec: n })} /></Field>
          <Field label={t('ops.regIpDaily')}><NumInput value={pol.registration.ip_daily_limit || 0} onChange={(n) => setReg({ ip_daily_limit: n })} /></Field>
          <Field label={t('ops.regEmailVerify')}><Switch value={!!pol.registration.email_verify_enabled} onChange={(v: any) => setReg({ email_verify_enabled: !!v })} /></Field>
        </div>
      </Panel>

      <Panel title={t('ops.limitsTitle')} extra={!isPlatform ? <span style={{ fontSize: 12, color: '#889' }}>{t('ops.platformOnly')}</span> : null}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isPlatform ? 1 : 0.55 }}>
          <Field label={t('ops.limitsQps')}><NumInput value={pol.limits.max_qps || 0} onChange={(n) => setLimits({ max_qps: n })} /></Field>
          <Field label={t('ops.limitsConcurrent')}><NumInput value={pol.limits.max_concurrent || 0} onChange={(n) => setLimits({ max_concurrent: n })} /></Field>
          <Field label={t('ops.limitsDailyChars')}><NumInput value={pol.limits.default_max_daily_chars || 0} onChange={(n) => setLimits({ default_max_daily_chars: n })} /></Field>
          <Field label={t('ops.limitsDailyTokens')}><NumInput value={pol.limits.default_max_daily_tokens || 0} onChange={(n) => setLimits({ default_max_daily_tokens: n })} /></Field>
        </div>
      </Panel>

      <Panel title={t('ops.payTitle')} extra={!isPlatform ? <span style={{ fontSize: 12, color: '#889' }}>{t('ops.platformOnly')}</span> : null}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isPlatform ? 1 : 0.55 }}>
          <Field label={t('ops.payMode')}>
            <select style={{ height: 30, minWidth: 120 }} value={pol.payment.mode || ''} onChange={(e) => setPay({ mode: e.target.value })}>
              <option value="">-</option><option value="mock">mock</option><option value="wechat">wechat</option><option value="alipay">alipay</option><option value="static_qr">static_qr</option>
            </select>
          </Field>
          <Field label={t('ops.payAutoCharge')}><Switch value={!!pol.payment.auto_charge} onChange={(v: any) => setPay({ auto_charge: !!v })} /></Field>
        </div>
      </Panel>

      <Panel title={t('ops.contentTitle')} extra={!isPlatform ? <span style={{ fontSize: 12, color: '#889' }}>{t('ops.platformOnly')}</span> : null}>
        <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap', opacity: isPlatform ? 1 : 0.55 }}>
          <Field label={t('ops.fileMaxMb')}><NumInput value={pol.content.file_max_mb || 0} onChange={(n) => setContent({ file_max_mb: n })} /></Field>
        </div>
      </Panel>

      {/* 推广窗口编辑弹窗 */}
      <Dialog header={t('ops.promoEditTitle')} visible={!!winDlg} style={{ width: 520 }}
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
              <div style={{ display: 'flex', gap: 6 }}>
                <Input value={winDlg.start} placeholder="2026-09-05" onChange={(v) => setWinDlg({ ...winDlg, start: v as string })} />
                ~ <Input value={winDlg.end} placeholder="2026-10-05" onChange={(v) => setWinDlg({ ...winDlg, end: v as string })} />
              </div>
            </Field>
            <Field label={t('ops.promoPriority')}><NumInput value={winDlg.priority} onChange={(n) => setWinDlg({ ...winDlg, priority: n })} /></Field>
            <Field label={t('ops.promoOverrides')}><Input value={winDlg.overrides} onChange={(v) => setWinDlg({ ...winDlg, overrides: v as string })} /></Field>
          </div>
        )}
      </Dialog>
    </>
  )
}
