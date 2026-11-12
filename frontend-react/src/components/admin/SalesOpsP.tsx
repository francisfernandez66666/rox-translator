// ============ SalesOpsP.tsx · 职责说明 ============
// 三期 W3 维护页（PanelKey 面板，不加 URL 路由、不动 App.tsx / spa.go）。
//
// 7 项开关逐项独立绑定**后端真实读口**（/api/admin/sales/ops 的配置值 + /api/sales/capability
// 的两把既有闸真实值）；半套配置（只开一把闸 / W4/W5 新键尚未落库）一律 data-state="half"
// 且显示 halfHint，绝不呈现「已生效」；两把都开且批次已到才 on（A12 两枚反证钉死）。
// ★ 当前 CURRENT_OPS_BATCH='W3'：W5/W6 项不许提前翻 on（两批靶显式登记在 salesOps.ts）。
// 所有文案一律 t('键名')，无硬编码中文；组件只用 @/ui/langcross/src 与 ./parts。
// ================================================
import { useCallback, useEffect, useState } from 'react'
import { useT } from '@/i18n'
import { Button, InlineBanner, PageHeader, StatusPill, Switch } from '@/ui/langcross/src'
import { Panel, Field } from './parts'
import {
  CURRENT_OPS_BATCH, OPS_SWITCH_DEFS, opsAllowOn, opsSwitchState,
  salesCapabilityGet, salesOpsGet, salesOpsSave, type OpsSwitchDef,
} from '@/api/salesOps'

/** 开关名文案（静态 switch，保证每个 t() 都是字面键，不用动态拼键绕闸） */
function labelFor(def: OpsSwitchDef, t: (key: string) => string): string {
  switch (def.labelKey) {
    case 'sales.ops.swSweep': return t('sales.ops.swSweep')
    case 'sales.ops.swFactsInjection': return t('sales.ops.swFactsInjection')
    case 'sales.ops.swClaimGuard': return t('sales.ops.swClaimGuard')
    case 'sales.ops.swDailyFreeLimit': return t('sales.ops.swDailyFreeLimit')
    case 'sales.ops.swFreqCap': return t('sales.ops.swFreqCap')
    case 'sales.ops.swOutbound': return t('sales.ops.swOutbound')
    case 'sales.ops.swSuppression': return t('sales.ops.swSuppression')
    default: return t('sales.ops.swSweep')
  }
}

/** 开关说明文案（静态 switch） */
function hintFor(def: OpsSwitchDef, t: (key: string) => string): string {
  switch (def.hintKey) {
    case 'sales.ops.swSweepHint': return t('sales.ops.swSweepHint')
    case 'sales.ops.swFactsInjectionHint': return t('sales.ops.swFactsInjectionHint')
    case 'sales.ops.swClaimGuardHint': return t('sales.ops.swClaimGuardHint')
    case 'sales.ops.swDailyFreeLimitHint': return t('sales.ops.swDailyFreeLimitHint')
    case 'sales.ops.swFreqCapHint': return t('sales.ops.swFreqCapHint')
    case 'sales.ops.swOutboundHint': return t('sales.ops.swOutboundHint')
    case 'sales.ops.swSuppressionHint': return t('sales.ops.swSuppressionHint')
    default: return t('sales.ops.swSweepHint')
  }
}

/** 分组标题文案（静态 switch，四组一一对应） */
function groupFor(g: string, t: (key: string) => string): string {
  switch (g) {
    case 'sales.ops.groupFacts': return t('sales.ops.groupFacts')
    case 'sales.ops.groupStrategy': return t('sales.ops.groupStrategy')
    case 'sales.ops.groupRuntime': return t('sales.ops.groupRuntime')
    case 'sales.ops.groupCompliance': return t('sales.ops.groupCompliance')
    default: return t('sales.ops.groupRuntime')
  }
}

/** 维护页面板（租户管理员及以上） */
export default function SalesOpsP() {
  const [, t] = useT()
  const [values, setValues] = useState<Record<string, string>>({})
  const [dirty, setDirty] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  const [saved, setSaved] = useState(false)

  /** 读后端真实配置值：新键取不到为空串（未接线），两把既有闸以能力面读数交叉核对 */
  const refresh = useCallback(() => {
    return salesOpsGet()
      .then((r) => {
        if (!r.success || !r.values) { setFailed(true); return }
        return salesCapabilityGet().then((cap) => {
          const v: Record<string, string> = { ...r.values }
          if (cap.success) {
            v.sales_engine_enabled = cap.engine_enabled ? '1' : '0'
            v.billing_enforced = cap.billing_enforced ? '1' : '0'
          }
          setValues(v)
          setDirty({})
          setFailed(false)
        })
      })
      .catch(() => setFailed(true))
  }, [])

  useEffect(() => { void refresh() }, [refresh])

  /** 本地切换一闸（逐项独立，攒到「保存」提交） */
  const toggle = (key: string, on: boolean) => {
    const v = on ? '1' : '0'
    setValues((prev) => ({ ...prev, [key]: v }))
    setDirty((d) => ({ ...d, [key]: v }))
  }

  /** 保存全部脏项（唯一写口 salesOpsSave，逐项独立提交） */
  const saveAll = () => {
    const entries = Object.entries(dirty)
    if (busy || entries.length === 0) return
    setBusy(true); setSaved(false); setFailed(false)
    Promise.all(entries.map(([key, value]) => salesOpsSave(key, value)))
      .then((rs) => {
        if (rs.every((r) => r.success)) { setSaved(true); setDirty({}); void refresh() }
        else setFailed(true)
      })
      .catch(() => setFailed(true))
      .finally(() => setBusy(false))
  }

  const groups = ['sales.ops.groupFacts', 'sales.ops.groupStrategy', 'sales.ops.groupRuntime', 'sales.ops.groupCompliance']

  return (
    <div data-testid="sales-ops-page">
      <PageHeader title={t('sales.ops.title')} />
      <InlineBanner tone="warn">{t('sales.ops.subtitle')}</InlineBanner>
      {failed && <InlineBanner tone="warn">{t('sales.ops.halfHint')}</InlineBanner>}
      {saved && <InlineBanner tone="success">{t('sales.ops.saved')}</InlineBanner>}

      {groups.map((g) => (
        <Panel key={g} title={groupFor(g, t)}>
          {OPS_SWITCH_DEFS.filter((def) => def.groupKey === g).map((def) => {
            // 主闸真实值；配对闸（总开关的第二把）独立读；wired=后端有明确 0/1 读口。
            const primary = values[def.key] === '1'
            const pair = def.pairKey ? values[def.pairKey] === '1' : null
            const wired = values[def.key] === '1' || values[def.key] === '0'
            const allowOn = opsAllowOn(def.batch, CURRENT_OPS_BATCH)
            const st = opsSwitchState(primary, pair, wired, allowOn)
            return (
              <div key={def.key} data-state={st} data-switch={def.key}>
                <Field label={labelFor(def, t)}>
                  <Switch
                    aria-label={def.key}
                    checked={primary}
                    disabled={!wired || busy}
                    onChange={(e) => toggle(def.key, e.target.checked)}
                  />
                  <StatusPill tone={st === 'on' ? 'success' : st === 'off' ? 'idle' : 'warn'}>
                    {st === 'on' ? t('sales.ops.swOn') : st === 'off' ? t('sales.ops.swOff') : t('sales.ops.halfHint')}
                  </StatusPill>
                </Field>
                {st === 'half' && <InlineBanner tone="warn">{t('sales.ops.halfHint')}</InlineBanner>}
                {st === 'off' && <InlineBanner tone="warn">{t('sales.ops.offHint')}</InlineBanner>}
                <span>{hintFor(def, t)}</span>
              </div>
            )
          })}
        </Panel>
      ))}

      <Button data-testid="sales-ops-reload" variant="secondary" disabled={busy} onClick={() => void refresh()}>
        {t('sales.ops.reload')}
      </Button>
      <Button data-testid="sales-ops-save" disabled={busy || Object.keys(dirty).length === 0} onClick={saveAll}>
        {t('sales.ops.save')}
      </Button>
    </div>
  )
}
