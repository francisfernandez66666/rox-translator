// ============================================================================
// components/admin/BillingHubP.tsx — 「计费与套餐」聚合 Hub（2026-09-15 Tab 精简）
// 职责：套餐价目（PlansP）+ 租户管理（TenantsP，L4）+ 成本对账（ReconcileP，L4）
//       合并为单一一级菜单；L3 企业管理员只见套餐子 tab。
// ============================================================================

/**
 * BillingHubP.tsx · 职责说明
 * Tabs：套餐价目（PlansP）｜ 租户管理（TenantsP，L4）｜ 成本对账（ReconcileP，L4）
 */

import { useState } from 'react'
import { Tabs } from 'tdesign-react'
import { useT } from '@/i18n'
import { useAdmin } from '@/stores/admin'
import { PlansP } from './panels_c'
import { TenantsP } from './TenantsP'
import { ReconcileP } from './ReconcileP'

/** 计费与套餐 Hub：套餐/订单（L3）+ 租户/对账（L4 门控） */
export default function BillingHubP() {
  const [, t] = useT()
  const { myLevel } = useAdmin()
  const [tab, setTab] = useState<'plans' | 'tenants' | 'reconcile'>('plans')

  return (
    <Tabs value={tab} onChange={(v) => setTab(v as 'plans' | 'tenants' | 'reconcile')}>
      <Tabs.TabPanel value="plans" label={t('hub.tabPlans')}>
        <PlansP />
      </Tabs.TabPanel>
      {myLevel >= 4 && (
        <Tabs.TabPanel value="tenants" label={t('hub.tabTenants')}>
          <TenantsP />
        </Tabs.TabPanel>
      )}
      {myLevel >= 4 && (
        <Tabs.TabPanel value="reconcile" label={t('hub.tabReconcile')}>
          <ReconcileP />
        </Tabs.TabPanel>
      )}
    </Tabs>
  )
}
