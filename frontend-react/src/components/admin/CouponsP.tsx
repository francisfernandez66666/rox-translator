// ============================================================================
// components/admin/CouponsP.tsx — 优惠券管理面板（★ #41 商业洞三，2026-09-21）
// 职责：超管维护券模板（新建 / 编辑 / 启停 / 删除）并查看核销流水。
// 口径：
//   - 券只减订单应付金额，不减订单积分额度（store.ApplyCouponToOrder 改写 amount_money）；
//   - 删除只删模板，历史核销流水保留（对账与活动复盘要用）；
//   - 有效期入参一律转 RFC3339 UTC 提交（与 store 层 time.Parse(time.RFC3339) 同一口径）；
//     表单里用本地 datetime-local 展示，避免超管填的时间与客户看到的差一个时区。
// 挂载位置：计费 Hub（BillingHubP）子 tab「优惠券」，仅 L4 超管可见
//   （后端 5 个券接口同样 requireSuperAdmin / requireTenantAdmin 双端把关）。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import type { ChangeEvent } from 'react'
import { runGuarded } from '@/lib/runGuarded'
import { Button, DataTable, Dialog, Switch, StatusPill, Link } from '@/ui/langcross/src'
import { confirmDialog } from '@/components/uiDialogs'
import { toastSuccess, toastError, toastWarn } from '@/lib/toastBus'
import { adminCoupons, adminCouponSave, adminCouponDelete, adminCouponRedemptions, type Any } from '@/api'
import { Panel, Field, num } from './parts'
import { fmtTime } from '@/lib/ui'
import { useT } from '@/i18n'

/** 表单初值：折扣方式默认按比例、力度默认 20（%），其余 0=不限（与后端默认口径一致） */
const emptyForm = (): Any => ({
  id: 0, code: '', name: '', kind: 'any', discount_type: 'percent', discount_value: 20,
  max_discount: 0, min_amount: 0, max_uses: 0, per_tenant_limit: 0,
  valid_from: '', valid_until: '', enabled: 1, note: '',
})

/** toRFC 本地 datetime-local 值 → RFC3339 UTC 串（空值回空串=不限） */
function toRFC(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/** toLocal RFC3339 → datetime-local 输入框值（浏览器按本地时区显示，截到分钟） */
function toLocal(rfc: string): string {
  if (!rfc) return ''
  const d = new Date(rfc)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** couponPayload 表单 → 保存入参（编辑回填的 RFC 串先转本地输入值，保存时再转回 UTC） */
function formFromRow(row: Any): Any {
  return {
    ...emptyForm(), ...row,
    valid_from: toLocal(String(row.valid_from || '')),
    valid_until: toLocal(String(row.valid_until || '')),
  }
}

/**
 * CouponsP 优惠券管理面板（超管）：券模板列表 + 新建/编辑弹窗 + 启停 + 删除 + 核销流水。
 */
export function CouponsP() {
  const [, t, tpl] = useT()
  const [rows, setRows] = useState<Any[]>([])
  const [dlg, setDlg] = useState<Any | null>(null)
  const [busy, setBusy] = useState(false)
  const [redemptions, setRedemptions] = useState<Any[] | null>(null) // null=流水弹窗未开
  const [filterId, setFilterId] = useState(0) // 0=全平台流水

  const load = useCallback(async () => {
    const r = await runGuarded(() => adminCoupons())
    if (r?.success) setRows((r.coupons as Any[]) || [])
  }, [])
  useEffect(() => { void load() }, [load])

  // openEdit 新建（row 省略）/ 编辑共用同一表单弹窗
  function openEdit(row?: Any) { setDlg(row ? formFromRow(row) : emptyForm()) }

  // save 提交券模板：数字字段统一转数，空时间转空串（=不限），由后端做规则校验
  async function save() {
    if (!dlg || busy) return
    const code = String(dlg.code || '').trim()
    const value = Number(dlg.discount_value) || 0
    if (!code || value <= 0) { void toastWarn(t('coupons.needFields')); return }
    setBusy(true)
    try {
      const r = await runGuarded(() => adminCouponSave({
        id: Number(dlg.id) || 0, code, name: String(dlg.name || '').trim(),
        kind: String(dlg.kind || 'any'), discount_type: String(dlg.discount_type || 'percent'),
        discount_value: value, max_discount: Number(dlg.max_discount) || 0, min_amount: Number(dlg.min_amount) || 0,
        max_uses: Number(dlg.max_uses) || 0, per_tenant_limit: Number(dlg.per_tenant_limit) || 0,
        valid_from: toRFC(String(dlg.valid_from || '')), valid_until: toRFC(String(dlg.valid_until || '')),
        enabled: Number(dlg.enabled ?? 1) ? 1 : 0, note: String(dlg.note || ''),
      }))
      if (!r) return
      if (!r.success) { void toastError(String(r.message || t('coupons.saveFail'))); return }
      void toastSuccess(t('coupons.saved'))
      setDlg(null)
      await load()
    } finally { setBusy(false) }
  }

  // toggle 启停：停用只是让用户无法再用该券码下单，历史订单与流水都不动
  async function toggle(row: Any, on: boolean) {
    const r = await runGuarded(() => adminCouponSave({
      id: Number(row.id), code: String(row.code), name: String(row.name || ''),
      kind: String(row.kind || 'any'), discount_type: String(row.discount_type || 'percent'),
      discount_value: Number(row.discount_value) || 0, max_discount: Number(row.max_discount) || 0,
      min_amount: Number(row.min_amount) || 0, max_uses: Number(row.max_uses) || 0,
      per_tenant_limit: Number(row.per_tenant_limit) || 0,
      valid_from: String(row.valid_from || ''), valid_until: String(row.valid_until || ''),
      enabled: on ? 1 : 0, note: String(row.note || ''),
    }))
    if (r?.success) await load()
    else void toastError(String(r?.message || t('coupons.saveFail')))
  }

  async function remove(row: Any) {
    if (!(await confirmDialog({ body: t('coupons.deleteConfirm'), confirmText: t('common.delete') }))) return
    const r = await runGuarded(() => adminCouponDelete(Number(row.id)))
    if (r?.success) { void toastSuccess(t('coupons.deleted')); await load() }
    else void toastError(String(r?.message || t('coupons.saveFail')))
  }

  // openRedemptions 拉核销流水（couponId=0 看全平台）
  async function openRedemptions(couponId: number) {
    setFilterId(couponId)
    setRedemptions([])
    const r = await runGuarded(() => adminCouponRedemptions(couponId || undefined))
    if (r?.success) setRedemptions((r.redemptions as Any[]) || [])
    else setRedemptions(null)
  }

  // discountText 折扣力度展示：立减走金额口径，按比例走百分数（附折让上限）
  function discountText(row: Any): string {
    const v = Number(row.discount_value) || 0
    if (row.discount_type === 'amount') return tpl('billing.yuan', { amount: v.toFixed(2) })
    const cap = Number(row.max_discount) > 0 ? ` ≤ ${tpl('billing.yuan', { amount: Number(row.max_discount).toFixed(2) })}` : ''
    return `${v}%${cap}`
  }

  // quotaText 配额展示：「已核销 / 总量（剩余）」，总量 0 视为不限
  function quotaText(row: Any): string {
    const used = Number(row.used_count) || 0
    const max = Number(row.max_uses) || 0
    if (max <= 0) return `${used} / ${t('coupons.unlimited')}`
    return `${used} / ${max}（${t('coupons.remaining')} ${Math.max(0, Number(row.remaining) || 0)}）`
  }

  function kindLabel(k: string): string {
    return k === 'recharge' ? t('coupons.kindRecharge') : k === 'subscribe' ? t('coupons.kindSubscribe') : t('coupons.kindAny')
  }

  function validityText(row: Any): string {
    const f = String(row.valid_from || '')
    const u = String(row.valid_until || '')
    if (!f && !u) return t('coupons.unlimited')
    return `${f ? fmtTime(f) : '—'} → ${u ? fmtTime(u) : '—'}`
  }

  // numberField 数字输入绑定助手（少写十行重复 onChange）
  function numberField(key: string, extra?: Record<string, unknown>) {
    return {
      className: 'lc-input', type: 'number' as const, min: 0, style: { width: 180 },
      value: num(dlg?.[key]), onChange: (e: ChangeEvent<HTMLInputElement>) => setDlg({ ...(dlg || emptyForm()), [key]: Number(e.target.value) || 0 }),
      ...extra,
    }
  }

  return (
    <>
      <Panel title={t('coupons.title')} extra={<Button variant="primary" onClick={() => openEdit()}>{t('coupons.create')}</Button>}>
        <div style={{ display: 'flex', justifyContent: 'space-between', gap: 10, flexWrap: 'wrap', marginBottom: 10 }}>
          <span style={{ fontSize: 14, color: 'var(--adm-hint)', flex: 1, minWidth: 280 }}>{t('coupons.hint')}</span>
          <Link onClick={() => void openRedemptions(0)}>{t('coupons.redemptions')} · {t('coupons.all')}</Link>
        </div>
        <DataTable rowKey={(row) => String((row as Any).id)} rows={rows} emptyText={t('coupons.noRedemptions')}
          columns={[
            { key: 'code', title: t('coupons.code'), width: 140, render: (row) => <code>{(row as Any).code}</code> },
            { key: 'name', title: t('coupons.name'), width: 150 },
            { key: 'kind', title: t('coupons.kind'), width: 120, render: (row) => kindLabel(String((row as Any).kind)) },
            { key: 'discount', title: t('coupons.discountValue'), width: 140, render: (row) => discountText(row as Any) },
            { key: 'min_amount', title: t('coupons.minAmount'), width: 110, render: (row) => (Number((row as Any).min_amount) > 0 ? tpl('billing.yuan', { amount: Number((row as Any).min_amount).toFixed(2) }) : t('coupons.unlimited')) },
            { key: 'quota', title: t('coupons.maxUses'), width: 180, render: (row) => quotaText(row as Any) },
            { key: 'per_tenant_limit', title: t('coupons.perTenantLimit'), width: 110, render: (row) => (Number((row as Any).per_tenant_limit) > 0 ? String((row as Any).per_tenant_limit) : t('coupons.unlimited')) },
            { key: 'valid', title: t('coupons.validFrom'), width: 230, render: (row) => validityText(row as Any) },
            { key: 'total_discount', title: t('coupons.totalDiscount'), width: 120, render: (row) => Number((row as Any).total_discount ?? 0).toFixed(2) },
            { key: 'enabled', title: t('common.status'), width: 130, render: (row) => (
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <Switch checked={Number((row as Any).enabled) === 1} onChange={(e) => void toggle(row as Any, e.target.checked)} />
                <StatusPill tone={Number((row as Any).enabled) === 1 ? 'success' : 'idle'}>{Number((row as Any).enabled) === 1 ? t('coupons.enabled') : t('ind.off')}</StatusPill>
              </div>
            ) },
            { key: 'op', title: '', width: 210, render: (row) => (
              <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
                <Link onClick={() => openEdit(row as Any)}>{t('coupons.edit')}</Link>
                <Link onClick={() => void openRedemptions(Number((row as Any).id))}>{t('coupons.redemptions')}</Link>
                <Link tone="danger" onClick={() => void remove(row as Any)}>{t('common.delete')}</Link>
              </div>
            ) },
          ]} />
      </Panel>

      {dlg && (
        <Dialog open onCancel={() => setDlg(null)} title={Number(dlg.id) > 0 ? t('coupons.editTitle') : t('coupons.create')}
          confirmText={t('common.save')} cancelText={t('common.cancel')}
          onConfirm={() => void save()}>
          <Field label={t('coupons.code')}>
            {/* 券码输入即转大写：与后端 NormalizeCouponCode 同一口径，避免用户复制小写码却提示不存在 */}
            <input className="lc-input" value={String(dlg.code || '')} placeholder={t('coupons.codePh')} style={{ width: 200 }}
              onChange={(e) => setDlg({ ...dlg, code: e.target.value.toUpperCase() })} />
          </Field>
          <Field label={t('coupons.name')}>
            <input className="lc-input" value={String(dlg.name || '')} style={{ width: 200 }} onChange={(e) => setDlg({ ...dlg, name: e.target.value })} />
          </Field>
          <Field label={t('coupons.kind')}>
            <select className="lc-select" value={String(dlg.kind || 'any')} style={{ width: 200 }} onChange={(e) => setDlg({ ...dlg, kind: e.target.value })}>
              <option value="any">{t('coupons.kindAny')}</option>
              <option value="recharge">{t('coupons.kindRecharge')}</option>
              <option value="subscribe">{t('coupons.kindSubscribe')}</option>
            </select>
          </Field>
          <Field label={t('coupons.discountType')}>
            <select className="lc-select" value={String(dlg.discount_type || 'percent')} style={{ width: 200 }} onChange={(e) => setDlg({ ...dlg, discount_type: e.target.value })}>
              <option value="percent">{t('coupons.typePercent')}</option>
              <option value="amount">{t('coupons.typeAmount')}</option>
            </select>
          </Field>
          <Field label={t('coupons.discountValue')}>
            <input {...numberField('discount_value', { placeholder: t('coupons.discountValuePh'), title: t('coupons.discountValuePh') })} />
          </Field>
          <Field label={t('coupons.maxDiscount')}><input {...numberField('max_discount')} /></Field>
          <Field label={t('coupons.minAmount')}><input {...numberField('min_amount')} /></Field>
          <Field label={t('coupons.maxUses')}><input {...numberField('max_uses')} /></Field>
          <Field label={t('coupons.perTenantLimit')}><input {...numberField('per_tenant_limit')} /></Field>
          <Field label={t('coupons.validFrom')}>
            <input className="lc-input" type="datetime-local" value={String(dlg.valid_from || '')} style={{ width: 220 }} onChange={(e) => setDlg({ ...dlg, valid_from: e.target.value })} />
          </Field>
          <Field label={t('coupons.validUntil')}>
            <input className="lc-input" type="datetime-local" value={String(dlg.valid_until || '')} style={{ width: 220 }} onChange={(e) => setDlg({ ...dlg, valid_until: e.target.value })} />
          </Field>
          <Field label={t('coupons.enabled')}>
            <Switch checked={Number(dlg.enabled ?? 1) === 1} onChange={(e) => setDlg({ ...dlg, enabled: e.target.checked ? 1 : 0 })} />
          </Field>
          <Field label={t('coupons.note')}>
            <input className="lc-input" value={String(dlg.note || '')} style={{ width: 260 }} onChange={(e) => setDlg({ ...dlg, note: e.target.value })} />
          </Field>
        </Dialog>
      )}

      {redemptions !== null && (
        <Dialog open onCancel={() => setRedemptions(null)} onConfirm={() => setRedemptions(null)}
          title={`${t('coupons.redemptions')}${filterId ? '' : ` · ${t('coupons.all')}`}`}
          confirmText={t('common.close')} cancelText={t('common.cancel')}>
          <div style={{ minWidth: 620 }}>
            <DataTable rowKey={(row) => String((row as Any).id)} rows={redemptions} emptyText={t('coupons.noRedemptions')}
              columns={[
                { key: 'code', title: t('coupons.code'), width: 130, render: (row) => <code>{(row as Any).code}</code> },
                { key: 'order_no', title: t('coupons.colOrderNo'), width: 180 },
                { key: 'tenant_id', title: t('coupons.colTenant'), width: 80, render: (row) => `#${(row as Any).tenant_id}` },
                { key: 'origin_money', title: t('coupons.colOrigin'), width: 100, render: (row) => Number((row as Any).origin_money ?? 0).toFixed(2) },
                { key: 'discount_money', title: t('coupons.colDiscount'), width: 100, render: (row) => Number((row as Any).discount_money ?? 0).toFixed(2) },
                { key: 'paid_money', title: t('coupons.colPaid'), width: 100, render: (row) => Number((row as Any).paid_money ?? 0).toFixed(2) },
                { key: 'created_at', title: t('coupons.colTime'), width: 165, render: (row) => fmtTime((row as Any).created_at as string) },
              ]} />
          </div>
        </Dialog>
      )}
    </>
  )
}

export default CouponsP
