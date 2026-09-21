// ============================================================================
// components/admin/BillingHubP.tsx — 「计费与套餐」聚合 Hub（2026-09-15 Tab 精简）
// 职责：套餐价目（PlansP）+ 租户管理（TenantsP，L4）+ 成本对账（ReconcileP，L4）
//       合并为单一一级菜单；L3 企业管理员只见套餐子 tab。
// 2026-09-18（UI 融合）：tab 壳从 TDesign Tabs（嵌套 TabPanel）换成 ui/langcross Tabs
//   （items 声明式 + 内容在外部条件渲染），权限门控口径不变。
// ============================================================================

/**
 * BillingHubP.tsx · 职责说明
 * Tabs：套餐价目（PlansP）｜ 租户管理（TenantsP，L4）｜ 成本对账（ReconcileP，L4）
 */

import { useState } from 'react'
import { Tabs } from '@/ui/langcross/src'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { PlansP } from './panels_c'
import { TenantsP } from './TenantsP'
import { ReconcileP } from './ReconcileP'
import { CouponsP } from './CouponsP'

/** 计费与套餐 Hub：套餐/订单（L3）+ 租户/对账/优惠券（L4 门控） */
export default function BillingHubP() {
  const [, t] = useT()
  const { myLevel } = useAdmin()
  const [tab, setTab] = useState<'plans' | 'tenants' | 'reconcile' | 'coupons'>('plans')

  // 子 tab 列表按等级动态裁剪：L3 只给「套餐价目」，租户与对账（平台级资金视图）仅 L4 出现
  const items = [
    { key: 'plans', label: t('hub.tabPlans') },
    ...(myLevel >= 4 ? [{ key: 'tenants', label: t('hub.tabTenants') }] : []),
    ...(myLevel >= 4 ? [{ key: 'reconcile', label: t('hub.tabReconcile') }] : []),
    // ★ #41 优惠券：券模板与核销流水是平台级经营资产，仅超管可见（后端亦 requireSuperAdmin）
    ...(myLevel >= 4 ? [{ key: 'coupons', label: t('hub.tabCoupons') }] : []),
  ]
  return (
    <>
      {/* langcross Tabs 只渲染 tab 头，面板内容由这里条件挂载：
          切走即卸载、切回重新挂载并重取数据（子面板之间不共享状态），
          因此这里除 tab 可见性外再判一次 myLevel>=4，双保险防止越权面板被渲染 */}
      <Tabs activeKey={tab} onChange={(k) => setTab(k as 'plans' | 'tenants' | 'reconcile' | 'coupons')} items={items} />
      {tab === 'plans' && <PlansP />}
      {tab === 'tenants' && myLevel >= 4 && <TenantsP />}
      {tab === 'reconcile' && myLevel >= 4 && <ReconcileP />}
      {tab === 'coupons' && myLevel >= 4 && <CouponsP />}
    </>
  )
}
